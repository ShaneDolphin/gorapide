package arch

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/pattern"
)

func TestComponentSendCheckedReportsFullInbox(t *testing.T) {
	component := NewComponent("target", Interface("Target").Build(), gorapide.NewPoset(), WithBufferSize(1))
	if err := component.SendChecked(gorapide.NewEvent("first", "source", nil)); err != nil {
		t.Fatalf("first delivery: %v", err)
	}

	err := component.SendChecked(gorapide.NewEvent("second", "source", nil))
	if !errors.Is(err, ErrDeliveryRejected) {
		t.Fatalf("full inbox error = %v, want ErrDeliveryRejected", err)
	}
	const want = `target component rejected event delivery: component "target" inbox is full`
	if err.Error() != want {
		t.Fatalf("full inbox diagnostic = %q, want %q", err, want)
	}
	if component.Send(gorapide.NewEvent("third", "source", nil)) {
		t.Fatal("compatibility Send reported success for a full inbox")
	}
}

func TestComponentEmitPropagatesRouterRejection(t *testing.T) {
	poset := gorapide.NewPoset()
	component := NewComponent("source", Interface("Source").Build(), poset)
	component.onEmit = func(*gorapide.Event) error {
		return fmt.Errorf("%w: test router is full", ErrDeliveryRejected)
	}

	event, err := component.Emit("Output", nil)
	if event == nil {
		t.Fatal("Emit returned nil event after successful poset insertion")
	}
	if !errors.Is(err, ErrDeliveryRejected) {
		t.Fatalf("Emit error = %v, want ErrDeliveryRejected", err)
	}
	if _, ok := poset.Event(event.ID); !ok {
		t.Fatal("Emit did not retain the explicitly failed partial result in the poset")
	}
}

func TestArchitectureNotifyReportsStableQueueSaturation(t *testing.T) {
	architecture := NewArchitecture("saturated")
	for index := 0; index < cap(architecture.events); index++ {
		architecture.events <- gorapide.NewEvent("queued", "fixture", nil)
	}

	err := architecture.notify(gorapide.NewEvent("Audit", "external", nil))
	if !errors.Is(err, ErrDeliveryRejected) {
		t.Fatalf("notify error = %v, want ErrDeliveryRejected", err)
	}
	const want = `target component rejected event delivery: architecture "saturated" router queue is full for action "Audit" from "external"`
	if err.Error() != want {
		t.Fatalf("notify diagnostic = %q, want %q", err, want)
	}
}

func TestBindingExecutionReportsDeliveryFailure(t *testing.T) {
	manager := NewBindingManager()
	poset := gorapide.NewPoset()
	target := NewComponent("target", Interface("Target").InAction("Input").Build(), poset, WithBufferSize(1))
	if err := target.SendChecked(gorapide.NewEvent("occupied", "fixture", nil)); err != nil {
		t.Fatalf("prefill target: %v", err)
	}
	trigger := gorapide.NewEvent("Input", "source", nil)
	if err := poset.AddEvent(trigger); err != nil {
		t.Fatalf("add trigger: %v", err)
	}
	binding := &Binding{ID: "bind-stable", FromComp: "source", ToComp: "target", Kind: BasicConnection}

	results, err := manager.executeBinding(binding, trigger, target, poset)
	if len(results) != 0 {
		t.Fatalf("results = %d, want no reported successful deliveries", len(results))
	}
	if !errors.Is(err, ErrDeliveryRejected) {
		t.Fatalf("binding error = %v, want ErrDeliveryRejected", err)
	}
	if !strings.Contains(err.Error(), `binding "bind-stable" basic delivery`) {
		t.Fatalf("binding diagnostic lacks operation context: %v", err)
	}
}

func TestArchitectureCascadeUsesStableTargetOrderForFailure(t *testing.T) {
	architecture := NewArchitecture("stable-order")
	source := NewComponent("source", Interface("Source").OutAction("Ping").Build(), nil)
	targetZ := NewComponent("z-target", Interface("TargetZ").InAction("Pong").Build(), nil, WithBufferSize(1))
	targetA := NewComponent("a-target", Interface("TargetA").InAction("Pong").Build(), nil, WithBufferSize(1))
	for _, component := range []*Component{source, targetZ, targetA} {
		if err := architecture.AddComponent(component); err != nil {
			t.Fatalf("AddComponent %q: %v", component.ID, err)
		}
	}
	if err := targetA.SendChecked(gorapide.NewEvent("occupied", "fixture", nil)); err != nil {
		t.Fatalf("prefill a-target: %v", err)
	}
	if err := targetZ.SendChecked(gorapide.NewEvent("occupied", "fixture", nil)); err != nil {
		t.Fatalf("prefill z-target: %v", err)
	}
	connection := Connect("source", "*").On(pattern.MatchEvent("Ping")).Send("Pong").Build()
	if err := architecture.AddConnection(connection); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}
	trigger := gorapide.NewEvent("Ping", "source", nil)
	if err := architecture.Poset().AddEvent(trigger); err != nil {
		t.Fatalf("add trigger: %v", err)
	}

	err := architecture.processEventCascade(trigger)
	if !errors.Is(err, ErrDeliveryRejected) {
		t.Fatalf("cascade error = %v, want ErrDeliveryRejected", err)
	}
	if !strings.Contains(err.Error(), `target "a-target"`) {
		t.Fatalf("cascade did not select the lexically first failing target: %v", err)
	}
	if strings.Contains(err.Error(), `target "z-target"`) {
		t.Fatalf("cascade selected a map-iteration-dependent target: %v", err)
	}
}

func TestLegacyRouterRetainsBindingFailure(t *testing.T) {
	architecture := NewArchitecture("router-error")
	if _, err := architecture.bindings.BindWith("source", "missing"); err != nil {
		t.Fatalf("BindWith: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := architecture.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	event := gorapide.NewEvent("Input", "source", nil)
	if err := architecture.Poset().AddEvent(event); err != nil {
		t.Fatalf("add event: %v", err)
	}
	if err := architecture.notify(event); err != nil {
		t.Fatalf("notify: %v", err)
	}

	errCh := make(chan error, 1)
	go func() { errCh <- architecture.WaitError() }()
	select {
	case err := <-errCh:
		if !errors.Is(err, ErrLegacyRuntimeFailure) {
			t.Fatalf("WaitError = %v, want ErrLegacyRuntimeFailure", err)
		}
		if !strings.Contains(err.Error(), `binding "bind-1" target component "missing" is missing`) {
			t.Fatalf("WaitError lacks binding context: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for the router to expose its binding failure")
	}
	if err := architecture.Stop(); !errors.Is(err, ErrLegacyRuntimeFailure) {
		t.Fatalf("Stop = %v, want retained ErrLegacyRuntimeFailure", err)
	}
}

func TestSubArchitectureSendCheckedReportsFullInbox(t *testing.T) {
	sub := WrapArchitecture("sub", NewArchitecture("inner")).WithBufferSize(1).Build()
	if err := sub.SendChecked(gorapide.NewEvent("first", "parent", nil)); err != nil {
		t.Fatalf("first delivery: %v", err)
	}
	err := sub.SendChecked(gorapide.NewEvent("second", "parent", nil))
	if !errors.Is(err, ErrDeliveryRejected) {
		t.Fatalf("SendChecked = %v, want ErrDeliveryRejected", err)
	}
	const want = `target component rejected event delivery: sub-architecture "sub" inbox is full`
	if err.Error() != want {
		t.Fatalf("SendChecked diagnostic = %q, want %q", err, want)
	}
}

func TestSubArchitectureBridgeRetainsImportFailure(t *testing.T) {
	inner := NewArchitecture("inner")
	sub := WrapArchitecture("sub", inner).
		Import("Outer", "missing", "Inner").
		Build()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := sub.StartChecked(ctx); err != nil {
		t.Fatalf("StartChecked: %v", err)
	}
	if err := sub.SendChecked(gorapide.NewEvent("Outer", "parent", nil)); err != nil {
		t.Fatalf("SendChecked: %v", err)
	}

	errCh := make(chan error, 1)
	go func() { errCh <- sub.WaitError() }()
	select {
	case err := <-errCh:
		if !errors.Is(err, ErrLegacyRuntimeFailure) {
			t.Fatalf("WaitError = %v, want ErrLegacyRuntimeFailure", err)
		}
		if !strings.Contains(err.Error(), `target component "missing" is missing`) {
			t.Fatalf("WaitError lacks target context: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for the bridge to expose its import failure")
	}
	if err := sub.StopChecked(); !errors.Is(err, ErrLegacyRuntimeFailure) {
		t.Fatalf("StopChecked = %v, want retained ErrLegacyRuntimeFailure", err)
	}
}

func TestParentArchitectureRetainsSubArchitectureBridgeFailure(t *testing.T) {
	parent := NewArchitecture("parent")
	sub := WrapArchitecture("sub", NewArchitecture("inner")).
		WithInterface(Interface("Sub").InAction("Outer").Build()).
		Import("Outer", "missing", "Inner").
		Build()
	if err := parent.AddSubArchitecture(sub); err != nil {
		t.Fatalf("AddSubArchitecture: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := parent.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := parent.InjectChecked("Outer", nil); err != nil {
		t.Fatalf("InjectChecked: %v", err)
	}

	errCh := make(chan error, 1)
	go func() { errCh <- parent.WaitError() }()
	select {
	case err := <-errCh:
		if !errors.Is(err, ErrLegacyRuntimeFailure) {
			t.Fatalf("parent WaitError = %v, want ErrLegacyRuntimeFailure", err)
		}
		if !strings.Contains(err.Error(), `sub-architecture "sub"`) || !strings.Contains(err.Error(), `target component "missing" is missing`) {
			t.Fatalf("parent WaitError lacks hierarchy context: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for parent to retain bridge failure")
	}
	if err := parent.Stop(); !errors.Is(err, ErrLegacyRuntimeFailure) {
		t.Fatalf("Stop = %v, want retained ErrLegacyRuntimeFailure", err)
	}
}

func TestSubArchitectureExportPropagatesParentNotificationFailure(t *testing.T) {
	sub := WrapArchitecture("sub", NewArchitecture("inner")).
		Export("worker", "Done", "Outer").
		Build()
	sub.parentPoset = gorapide.NewPoset()
	sub.onEmit = func(*gorapide.Event) error {
		return fmt.Errorf("%w: parent router is full", ErrDeliveryRejected)
	}

	err := sub.handleExportChecked(gorapide.NewEvent("Done", "worker", nil))
	if !errors.Is(err, ErrDeliveryRejected) {
		t.Fatalf("handleExportChecked = %v, want ErrDeliveryRejected", err)
	}
	if !strings.Contains(err.Error(), `export "Outer" parent notification`) {
		t.Fatalf("export diagnostic lacks boundary context: %v", err)
	}
}

func TestArchitectureStartPropagatesInvalidSubArchitecture(t *testing.T) {
	parent := NewArchitecture("parent")
	sub := WrapArchitecture("sub", nil).WithInterface(Interface("Sub").Build()).Build()
	if err := parent.AddSubArchitecture(sub); err != nil {
		t.Fatalf("AddSubArchitecture: %v", err)
	}

	err := parent.Start(context.Background())
	if !errors.Is(err, ErrLegacyRuntimeFailure) {
		t.Fatalf("Start = %v, want ErrLegacyRuntimeFailure", err)
	}
	if !strings.Contains(err.Error(), `sub-architecture "sub" inner architecture is nil`) {
		t.Fatalf("Start diagnostic lacks construction context: %v", err)
	}
	parent.Wait()
}

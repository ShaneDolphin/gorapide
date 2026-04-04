package arch

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/beautiful-majestic-dolphin/gorapide"
)

// --- ActionKind Tests ---

func TestActionKindConstants(t *testing.T) {
	if InAction == OutAction {
		t.Error("InAction and OutAction must be distinct")
	}
}

// --- ParamDecl Tests ---

func TestPShorthand(t *testing.T) {
	pd := P("host", "string")
	if pd.Name != "host" {
		t.Errorf("Name: want host, got %s", pd.Name)
	}
	if pd.Type != "string" {
		t.Errorf("Type: want string, got %s", pd.Type)
	}
}

// --- InterfaceDecl Builder Tests ---

func TestInterfaceDeclBuilder(t *testing.T) {
	iface := Interface("PipeFilter").
		InAction("Receive", P("data", "[]byte")).
		OutAction("Send", P("data", "[]byte"), P("dest", "string")).
		Build()

	if iface.Name != "PipeFilter" {
		t.Errorf("Name: want PipeFilter, got %s", iface.Name)
	}
	if len(iface.Actions) != 2 {
		t.Fatalf("Actions: want 2, got %d", len(iface.Actions))
	}

	recv := iface.Actions[0]
	if recv.Name != "Receive" {
		t.Errorf("Action[0].Name: want Receive, got %s", recv.Name)
	}
	if recv.Kind != InAction {
		t.Errorf("Action[0].Kind: want InAction, got %d", recv.Kind)
	}
	if len(recv.Params) != 1 {
		t.Fatalf("Action[0].Params: want 1, got %d", len(recv.Params))
	}
	if recv.Params[0].Name != "data" || recv.Params[0].Type != "[]byte" {
		t.Errorf("Action[0].Params[0]: want data/[]byte, got %s/%s", recv.Params[0].Name, recv.Params[0].Type)
	}

	send := iface.Actions[1]
	if send.Name != "Send" {
		t.Errorf("Action[1].Name: want Send, got %s", send.Name)
	}
	if send.Kind != OutAction {
		t.Errorf("Action[1].Kind: want OutAction, got %d", send.Kind)
	}
	if len(send.Params) != 2 {
		t.Fatalf("Action[1].Params: want 2, got %d", len(send.Params))
	}
}

func TestInterfaceDeclBuilderWithService(t *testing.T) {
	iface := Interface("Server").
		Service("HTTP", func(s *ServiceBuilder) {
			s.InAction("Request", P("method", "string"), P("path", "string"))
			s.OutAction("Response", P("status", "int"))
		}).
		Build()

	if iface.Name != "Server" {
		t.Errorf("Name: want Server, got %s", iface.Name)
	}
	if len(iface.Services) != 1 {
		t.Fatalf("Services: want 1, got %d", len(iface.Services))
	}
	svc := iface.Services[0]
	if svc.Name != "HTTP" {
		t.Errorf("Service.Name: want HTTP, got %s", svc.Name)
	}
	if len(svc.Actions) != 2 {
		t.Fatalf("Service.Actions: want 2, got %d", len(svc.Actions))
	}
	if svc.Actions[0].Kind != InAction {
		t.Error("Service.Actions[0] should be InAction")
	}
	if svc.Actions[1].Kind != OutAction {
		t.Error("Service.Actions[1] should be OutAction")
	}
}

func TestInterfaceDeclBuilderEmpty(t *testing.T) {
	iface := Interface("Empty").Build()
	if iface.Name != "Empty" {
		t.Errorf("Name: want Empty, got %s", iface.Name)
	}
	if len(iface.Actions) != 0 {
		t.Errorf("Actions: want 0, got %d", len(iface.Actions))
	}
	if len(iface.Services) != 0 {
		t.Errorf("Services: want 0, got %d", len(iface.Services))
	}
}

// --- Component Tests ---

func TestNewComponentDefaults(t *testing.T) {
	iface := Interface("Test").Build()
	p := gorapide.NewPoset()
	c := NewComponent("comp1", iface, p)

	if c.ID != "comp1" {
		t.Errorf("ID: want comp1, got %s", c.ID)
	}
	if c.Interface.Name != "Test" {
		t.Errorf("Interface.Name: want Test, got %s", c.Interface.Name)
	}
}

func TestNewComponentWithBufferSize(t *testing.T) {
	iface := Interface("Test").Build()
	p := gorapide.NewPoset()
	c := NewComponent("comp1", iface, p, WithBufferSize(100))

	// Verify the component was created (buffer size is internal).
	if c.ID != "comp1" {
		t.Errorf("ID: want comp1, got %s", c.ID)
	}
}

// --- Emit Tests ---

func TestComponentEmit(t *testing.T) {
	iface := Interface("Emitter").
		OutAction("Ping", P("seq", "int")).
		Build()
	p := gorapide.NewPoset()
	c := NewComponent("emitter", iface, p)

	e, err := c.Emit("Ping", map[string]any{"seq": 1})
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if e.Name != "Ping" {
		t.Errorf("Name: want Ping, got %s", e.Name)
	}
	if e.Source != "emitter" {
		t.Errorf("Source: want emitter, got %s", e.Source)
	}
	if e.ParamInt("seq") != 1 {
		t.Errorf("Param seq: want 1, got %d", e.ParamInt("seq"))
	}

	// Event should be in the poset.
	found, ok := p.Get(e.ID)
	if !ok {
		t.Fatal("emitted event should be in poset")
	}
	if found.Name != "Ping" {
		t.Errorf("poset event name: want Ping, got %s", found.Name)
	}
}

func TestComponentEmitWithCauses(t *testing.T) {
	iface := Interface("Chain").
		OutAction("Step", P("n", "int")).
		Build()
	p := gorapide.NewPoset()
	c := NewComponent("chain", iface, p)

	e1, err := c.Emit("Step", map[string]any{"n": 1})
	if err != nil {
		t.Fatalf("Emit step 1: %v", err)
	}

	e2, err := c.Emit("Step", map[string]any{"n": 2}, e1.ID)
	if err != nil {
		t.Fatalf("Emit step 2: %v", err)
	}

	if !p.IsCausallyBefore(e1.ID, e2.ID) {
		t.Error("step 1 should be causally before step 2")
	}
}

func TestComponentEmitNilParams(t *testing.T) {
	iface := Interface("Simple").OutAction("Tick").Build()
	p := gorapide.NewPoset()
	c := NewComponent("simple", iface, p)

	e, err := c.Emit("Tick", nil)
	if err != nil {
		t.Fatalf("Emit with nil params: %v", err)
	}
	if e.Name != "Tick" {
		t.Errorf("Name: want Tick, got %s", e.Name)
	}
}

// --- Send Tests ---

func TestComponentSend(t *testing.T) {
	iface := Interface("Receiver").
		InAction("Msg", P("body", "string")).
		Build()
	p := gorapide.NewPoset()
	c := NewComponent("recv", iface, p)

	event := gorapide.NewEvent("Msg", "external", map[string]any{"body": "hello"})
	ok := c.Send(event)
	if !ok {
		t.Error("Send should succeed on unbuffered component")
	}
}

func TestComponentSendNonBlocking(t *testing.T) {
	iface := Interface("Receiver").InAction("Msg").Build()
	p := gorapide.NewPoset()
	c := NewComponent("recv", iface, p, WithBufferSize(1))

	// Fill the buffer.
	e1 := gorapide.NewEvent("Msg", "ext", nil)
	ok1 := c.Send(e1)
	if !ok1 {
		t.Fatal("first Send should succeed")
	}

	// Second send should not block; it should return false (buffer full).
	e2 := gorapide.NewEvent("Msg", "ext", nil)
	ok2 := c.Send(e2)
	if ok2 {
		t.Error("Send on full buffer should return false (non-blocking)")
	}
}

// --- Lifecycle Tests ---

func TestComponentStartStop(t *testing.T) {
	iface := Interface("Worker").
		InAction("Job").
		OutAction("Done").
		Build()
	p := gorapide.NewPoset()
	c := NewComponent("worker", iface, p)

	var received []*gorapide.Event
	var mu sync.Mutex

	c.OnReceive(func(comp *Component, e *gorapide.Event) {
		mu.Lock()
		defer mu.Unlock()
		received = append(received, e)
	})

	ctx := context.Background()
	c.Start(ctx)

	// Send events to the running component.
	for i := 0; i < 3; i++ {
		e := gorapide.NewEvent("Job", "test", map[string]any{"i": i})
		c.Send(e)
	}

	// Give the goroutine time to process.
	time.Sleep(50 * time.Millisecond)

	c.Stop()
	c.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(received) != 3 {
		t.Errorf("expected 3 received events, got %d", len(received))
	}
}

func TestComponentStartStopEmpty(t *testing.T) {
	iface := Interface("Idle").Build()
	p := gorapide.NewPoset()
	c := NewComponent("idle", iface, p)

	ctx := context.Background()
	c.Start(ctx)
	c.Stop()
	c.Wait()
	// Should not hang or panic.
}

func TestComponentWaitBlocksUntilStop(t *testing.T) {
	iface := Interface("Blocker").InAction("Ping").Build()
	p := gorapide.NewPoset()
	c := NewComponent("blocker", iface, p)

	ctx := context.Background()
	c.Start(ctx)

	done := make(chan struct{})
	go func() {
		c.Wait()
		close(done)
	}()

	// Wait should not return yet.
	select {
	case <-done:
		t.Fatal("Wait returned before Stop")
	case <-time.After(50 * time.Millisecond):
		// Expected.
	}

	c.Stop()

	select {
	case <-done:
		// Expected.
	case <-time.After(time.Second):
		t.Fatal("Wait did not return after Stop")
	}
}

func TestComponentContextCancel(t *testing.T) {
	iface := Interface("Cancellable").InAction("Ping").Build()
	p := gorapide.NewPoset()
	c := NewComponent("cancel", iface, p)

	ctx, cancel := context.WithCancel(context.Background())
	c.Start(ctx)

	done := make(chan struct{})
	go func() {
		c.Wait()
		close(done)
	}()

	cancel()

	select {
	case <-done:
		// Expected — context cancel should stop the component.
	case <-time.After(time.Second):
		t.Fatal("Wait did not return after context cancel")
	}
}

func TestComponentOnReceiveCalledInOrder(t *testing.T) {
	iface := Interface("Ordered").InAction("Msg").Build()
	p := gorapide.NewPoset()
	c := NewComponent("ordered", iface, p, WithBufferSize(10))

	var order []int
	var mu sync.Mutex

	c.OnReceive(func(comp *Component, e *gorapide.Event) {
		mu.Lock()
		defer mu.Unlock()
		order = append(order, e.ParamInt("i"))
	})

	// Pre-fill the inbox before starting so all events are queued.
	for i := 0; i < 5; i++ {
		e := gorapide.NewEvent("Msg", "test", map[string]any{"i": i})
		c.Send(e)
	}

	ctx := context.Background()
	c.Start(ctx)
	time.Sleep(50 * time.Millisecond)
	c.Stop()
	c.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(order) != 5 {
		t.Fatalf("expected 5 events, got %d", len(order))
	}
	for i, v := range order {
		if v != i {
			t.Errorf("order[%d]: want %d, got %d", i, i, v)
		}
	}
}

// --- Concurrent Safety ---

func TestComponentConcurrentEmit(t *testing.T) {
	iface := Interface("Concurrent").OutAction("Tick").Build()
	p := gorapide.NewPoset()
	c := NewComponent("conc", iface, p)

	var wg sync.WaitGroup
	const goroutines = 50
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_, err := c.Emit("Tick", map[string]any{"n": n})
			if err != nil {
				t.Errorf("Emit error: %v", err)
			}
		}(i)
	}
	wg.Wait()

	if p.Len() != goroutines {
		t.Errorf("poset should have %d events, got %d", goroutines, p.Len())
	}
}

// --- String Tests ---

func TestInterfaceDeclString(t *testing.T) {
	iface := Interface("MyIface").
		InAction("Foo").
		OutAction("Bar").
		Build()

	s := iface.String()
	if len(s) == 0 {
		t.Error("String() should not be empty")
	}
	if !containsStr(s, "MyIface") {
		t.Errorf("String() should contain interface name, got %s", s)
	}
}

func TestComponentString(t *testing.T) {
	iface := Interface("Test").Build()
	p := gorapide.NewPoset()
	c := NewComponent("comp1", iface, p)

	s := c.String()
	if len(s) == 0 {
		t.Error("String() should not be empty")
	}
	if !containsStr(s, "comp1") {
		t.Errorf("String() should contain component ID, got %s", s)
	}
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

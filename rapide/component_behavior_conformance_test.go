package rapide

import (
	"bytes"
	"runtime"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide/arch"
)

func moduleDenotedBehaviorSource(moduleBody string) []byte {
	return []byte(`
type Driver is interface action out Send(value : Integer); end interface Driver;
type Protocol is interface
  action in Request(value : Integer);
  action out Reply(value : Integer);
  behavior begin
    (?Value : Integer) Request(?Value) => Reply(?Value);;
end interface Protocol;
module ProtocolModule() return Protocol is
` + moduleBody + `
end module ProtocolModule;
architecture System() is
  driver : Driver;
  worker : Protocol is ProtocolModule();
connect
  driver.Send to worker.Request;
end architecture System;
`)
}

func executeModuleDenotedBehavior(t *testing.T, source []byte, inputAction string) (*arch.Architecture, *arch.ExecutionResult, arch.ExecutionJournal) {
	t.Helper()
	model, err := Compile(source, "System")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := arch.NewExecutionJournal(digest, 30, arch.InputEvent{
		Key: "request", Source: "driver", Action: inputAction, Params: map[string]any{"value": 7},
	})
	result, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	return model, result, journal
}

func TestModuleDenotedComponentBehaviorConstrainsWithoutExecuting(t *testing.T) {
	model, passing, journal := executeModuleDenotedBehavior(t, moduleDenotedBehaviorSource(`
serial
  when (?Value : Integer) Request(?Value) do Reply(?Value); end when;
`), "Send")
	replies := sourceNamedEvents(passing.Poset, "worker", "Reply")
	if len(replies) != 1 || replies[0].ParamInt("value") != 7 {
		t.Fatalf("module Reply events=%#v, want exactly one module-generated Reply(7)", replies)
	}
	if len(passing.ModuleConstraints) != 1 || passing.ModuleConstraints[0].ComponentID != "worker" ||
		!passing.ModuleConstraints[0].Report.Passed || len(passing.ModuleConstraints[0].Report.Reports) != 1 {
		t.Fatalf("passing component behavior report=%#v", passing.ModuleConstraints)
	}
	artifact, err := passing.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	artifactDigest, err := passing.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := model.ReplayDeterministic(journal, artifactDigest)
	if err != nil {
		t.Fatal(err)
	}
	replayArtifact, err := replayed.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(artifact, replayArtifact) {
		t.Fatal("module-denoted component behavior report did not replay byte-identically")
	}

	_, missing, _ := executeModuleDenotedBehavior(t, moduleDenotedBehaviorSource(`
serial
  when Request do null; end when;
`), "Send")
	if len(missing.ModuleConstraints) != 1 || missing.ModuleConstraints[0].Report.Passed ||
		len(missing.ModuleConstraints[0].Report.Reports) != 1 ||
		len(missing.ModuleConstraints[0].Report.Reports[0].Violations) != 1 {
		t.Fatalf("missing component behavior report=%#v", missing.ModuleConstraints)
	}
	violation := missing.ModuleConstraints[0].Report.Reports[0].Violations[0]
	if violation.Kind != "MustMatch" || violation.Clause != "super-poset" ||
		!strings.Contains(violation.Message, "component") ||
		!strings.Contains(violation.Message, "causal-order-preserving super-poset") {
		t.Fatalf("component behavior violation=%#v", violation)
	}
	if replies := sourceNamedEvents(missing.Poset, "worker", "Reply"); len(replies) != 0 {
		t.Fatalf("constraint shadow leaked production Reply events=%#v", replies)
	}
}

func TestNestedModuleDenotedComponentBehaviorConstraintIsOwnedExactly(t *testing.T) {
	source := []byte(`
type Driver is interface action out Send(value : Integer); end interface Driver;
type ChildBoundary is interface action in Request(value : Integer); action out Ready(value : Integer); end interface ChildBoundary;
type Protocol is interface
  action in Request(value : Integer); action out Reply(value : Integer);
  behavior begin (?Value : Integer) Request(?Value) => Reply(?Value);; end interface Protocol;
module ProtocolModule() return Protocol is parallel
  when (?Value : Integer) Request(?Value) do Reply(?Value); end when;
end module ProtocolModule;
architecture Child() return ChildBoundary is
  worker : Protocol is ProtocolModule();
connect
  Request to worker.Request;
  worker.Reply to Ready;
end architecture Child;
architecture System() is
  driver : Driver;
  child : ChildBoundary is Child();
connect driver.Send to child.Request;
end architecture System;
`)
	model, err := Compile(source, "System")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 40, arch.InputEvent{
		Key: "request", Source: "driver", Action: "Send", Params: map[string]any{"value": 9},
	}))
	if err != nil {
		t.Fatal(err)
	}
	workerID := arch.DeterministicArchitectureComponentID("child", "worker")
	if len(result.ModuleConstraints) != 1 || result.ModuleConstraints[0].ComponentID != workerID ||
		!result.ModuleConstraints[0].Report.Passed || len(result.ModuleConstraints[0].Report.Reports) != 1 {
		t.Fatalf("nested component behavior report=%#v", result.ModuleConstraints)
	}
	replies := sourceNamedEvents(result.Poset, workerID, "Reply")
	if len(replies) != 1 || replies[0].ParamInt("value") != 9 || !replies[0].HasObservation("child", "Ready") {
		t.Fatalf("nested module Reply=%#v", replies)
	}
}

func TestModuleDenotedComponentBehaviorConstraintStableAcrossOrderBehaviorCaseAndGOMAXPROCS(t *testing.T) {
	sources := [][]byte{
		moduleDenotedBehaviorSource(`parallel when (?Value : Integer) Request(?Value) do Reply(?Value); end when;`),
		[]byte(`
type Protocol is interface action out Reply(value : Integer); action in Request(value : Integer);
behavior begin (?value : Integer) request(?value) => reply(?value);; end interface Protocol;
module ProtocolModule() return Protocol is parallel when (?Value : Integer) Request(?Value) do Reply(?Value); end when; end module ProtocolModule;
type Driver is interface action out Send(value : Integer); end interface Driver;
architecture System() is worker : Protocol is ProtocolModule(); driver : Driver;
connect driver.Send to worker.Request; end architecture System;
`),
	}
	previous := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(previous)
	var baselineDigest string
	var baselineArtifact []byte
	for iteration := 0; iteration < 12; iteration++ {
		if iteration < 6 {
			runtime.GOMAXPROCS(1)
		} else {
			runtime.GOMAXPROCS(4)
		}
		variant := iteration % len(sources)
		model, result, _ := executeModuleDenotedBehavior(t, sources[variant], "Send")
		digest, err := model.DeterministicModelDigest()
		if err != nil {
			t.Fatal(err)
		}
		artifact, err := result.MarshalCanonical()
		if err != nil {
			t.Fatal(err)
		}
		if baselineDigest == "" {
			baselineDigest, baselineArtifact = digest, artifact
			continue
		}
		if digest != baselineDigest || !bytes.Equal(artifact, baselineArtifact) {
			t.Fatalf("component behavior order/case/GOMAXPROCS changed result: digest %s/%s\nbase=%s\n got=%s",
				baselineDigest, digest, baselineArtifact, artifact)
		}
	}
}

func TestModuleDenotedComponentBehaviorConstraintRejectsUnboundedOrEffectfulSubset(t *testing.T) {
	tests := []struct {
		name         string
		actions      string
		constituents string
		behavior     string
		moduleBody   string
		want         string
	}{
		{name: "state", behavior: "Count : var Integer := 0; begin Request => Reply();;", want: "behavior state is outside"},
		{name: "function", constituents: "provides F : function() return Integer;", behavior: "F : function() return Integer is begin return 1; end function F; begin Request => Reply();;", moduleBody: "F : function() return Integer is begin return 1; end function F;", want: "behavior functions are outside"},
		{name: "compound", behavior: "begin (Request ~ Request) => Reply();;", want: "compound triggers are outside"},
		{name: "timing", behavior: "begin Request => Reply() in C.Ticks(1);;", want: "behavior timing is outside"},
		{name: "cycle", actions: "action out Request(); action out Reply();", behavior: "begin Request => Reply();; Reply => Request();;", want: "behavior action cycle reply -> request -> reply"},
		{name: "unseeded output trigger", actions: "action out Notice(); action out Reply();", behavior: "begin Notice => Reply();;", want: "behavior trigger \"notice\" is not reachable from a direct in action"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actions := test.actions
			if actions == "" {
				actions = "action in Request(); action out Reply();"
			}
			source := []byte(`
type Protocol is interface ` + actions + ` ` + test.constituents + ` behavior ` + test.behavior + ` end interface Protocol;
module ProtocolModule() return Protocol is ` + test.moduleBody + ` end module ProtocolModule;
architecture System() is worker : Protocol is ProtocolModule(); end architecture System;
`)
			_, err := Compile(source, "System")
			if err == nil || !strings.Contains(err.Error(), test.want) || !strings.Contains(err.Error(), `component "worker"`) {
				t.Fatalf("error=%v, want component diagnostic containing %q", err, test.want)
			}
		})
	}
}

package rapide

import (
	"bytes"
	"runtime"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/arch"
)

func TestCompileModuleInitialPrecedesProcessesAndJournalInputs(t *testing.T) {
	source := []byte(`
type Driver is interface action out Input(n : Integer); end interface Driver;
type Worker is interface
  action out Boot(n : Integer);
  action in Delivered(n : Integer);
  action in Input(n : Integer);
  action out Ready(n : Integer);
  action out Later(n : Integer);
end interface Worker;

module Startup() return Worker is
  value : var Integer := 0;
initial
  value := 4;
  Boot($value);
parallel
  when (?N : Integer) Delivered(?N) do Ready(?N); end when;
||
  when (?N : Integer) Input(?N) do Later(?N); end when;
end module Startup;

architecture System() is
  driver : Driver;
  worker : Worker is Startup();
connect
  worker.Boot => worker.Delivered;
  driver.Input => worker.Input;
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
	journal := arch.NewExecutionJournal(digest, 30, arch.InputEvent{
		Key: "input", Source: "driver", Action: "Input", Params: map[string]any{"n": 9},
	})
	result, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	boot := result.Poset.ByName("Boot")
	delivered := result.Poset.ByName("Delivered")
	ready := result.Poset.ByName("Ready")
	later := result.Poset.ByName("Later")
	if len(boot) != 1 || len(delivered) != 1 || len(ready) != 1 || len(later) != 1 {
		t.Fatalf("source initial events boot=%d delivered=%d ready=%d later=%d",
			len(boot), len(delivered), len(ready), len(later))
	}
	if value, _ := ready[0].Param("n"); value != int64(4) {
		t.Fatalf("startup process value=%#v, want 4", value)
	}
	if value, _ := later[0].Param("n"); value != int64(9) {
		t.Fatalf("journal process value=%#v, want 9", value)
	}
	if !result.Poset.IsCausallyBefore(boot[0].ID, delivered[0].ID) ||
		!result.Poset.IsCausallyBefore(delivered[0].ID, ready[0].ID) ||
		!result.Poset.IsCausallyBefore(boot[0].ID, later[0].ID) {
		t.Fatal("source initial frontier did not precede startup and journal-triggered process work")
	}
	if result.Poset.IsCausallyBefore(ready[0].ID, later[0].ID) ||
		result.Poset.IsCausallyBefore(later[0].ID, ready[0].ID) {
		t.Fatal("parallel processes acquired a scheduler-only causal edge after startup")
	}
	if len(result.State) != 1 || result.State[0].ComponentID != "worker" ||
		result.State[0].Name != "value" || result.State[0].Version != 1 || result.State[0].Value.Text != "4" {
		t.Fatalf("source initial final state=%#v", result.State)
	}
	if len(result.Firings) == 0 || result.Firings[0].Transition != "initial" ||
		result.Firings[0].Target != "worker" || len(result.Firings[0].Generated) != 1 ||
		len(result.Firings[0].StateWrites) != 1 || len(result.Firings[0].StateReads) != 1 {
		t.Fatalf("source initial audit=%#v", result.Firings)
	}

	artifactDigest, err := result.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := model.ReplayDeterministic(journal, artifactDigest)
	if err != nil {
		t.Fatal(err)
	}
	left, _ := result.MarshalCanonical()
	right, _ := replayed.MarshalCanonical()
	if !bytes.Equal(left, right) {
		t.Fatal("source module initial replay was not byte-identical")
	}
}

func TestSourceModuleInitialSelfInterruptsBeforeProcessElaboration(t *testing.T) {
	source := []byte(`
type Worker is interface
  action in Request();
  action out Signal(value : Integer);
  private action Pulse();
  action out Recovered(value : Integer);
  action out AnyRecovered();
  action out AfterInitial();
  action out ProcessRan();
  action out Wrong();
end interface Worker;

module Startup() return Worker is
initial
  do
    Signal(4);
    Wrong();
  handler
    is Signal(value is ?Value) => Recovered(?Value);
  end do;
  do
    Pulse();
    Wrong();
  handler
    is any => AnyRecovered();
  end do;
  AfterInitial();
serial
  ProcessRan();
end module Startup;

architecture System() is worker : Worker is Startup(); end architecture System;
`)
	model, err := Compile(source, "System")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := arch.NewExecutionJournal(digest, 30)
	previous := runtime.GOMAXPROCS(1)
	result, err := model.ExecuteDeterministic(journal)
	if err != nil {
		runtime.GOMAXPROCS(previous)
		t.Fatal(err)
	}
	runtime.GOMAXPROCS(8)
	repeated, repeatedErr := model.ExecuteDeterministic(journal)
	runtime.GOMAXPROCS(previous)
	if repeatedErr != nil {
		t.Fatal(repeatedErr)
	}

	signal := sourceNamedEvents(result.Poset, "worker", "Signal")
	pulse := sourceNamedEvents(result.Poset, "worker", "Pulse")
	recovered := sourceNamedEvents(result.Poset, "worker", "Recovered")
	anyRecovered := sourceNamedEvents(result.Poset, "worker", "AnyRecovered")
	after := sourceNamedEvents(result.Poset, "worker", "AfterInitial")
	process := sourceNamedEvents(result.Poset, "worker", "ProcessRan")
	if len(signal) != 1 || len(pulse) != 1 || len(recovered) != 1 ||
		len(anyRecovered) != 1 || len(after) != 1 || len(process) != 1 ||
		len(result.Poset.ByName("Wrong")) != 0 {
		t.Fatalf("Signal/Pulse/Recovered/AnyRecovered/After/Process/Wrong=%d/%d/%d/%d/%d/%d/%d",
			len(signal), len(pulse), len(recovered), len(anyRecovered), len(after),
			len(process), len(result.Poset.ByName("Wrong")))
	}
	if recovered[0].ParamInt("value") != 4 {
		t.Fatalf("module initial interrupt binding=%#v", recovered[0])
	}
	module := lifecycleModuleByOccurrence(t, result, "component:worker")
	start, exists := result.Poset.Get(gorapide.EventID(module.StartEventID))
	if !exists {
		t.Fatalf("module lifecycle has no Start: %#v", module)
	}
	assertOnlyDirectCause(t, result.Poset, signal[0], start)
	assertOnlyDirectCause(t, result.Poset, recovered[0], signal[0])
	assertOnlyDirectCause(t, result.Poset, pulse[0], recovered[0])
	assertOnlyDirectCause(t, result.Poset, anyRecovered[0], pulse[0])
	assertOnlyDirectCause(t, result.Poset, after[0], anyRecovered[0])
	assertOnlyDirectCause(t, result.Poset, process[0], after[0])
	if len(result.ExceptionPropagations) != 0 {
		t.Fatalf("module initial action interrupt entered exception propagation: %#v", result.ExceptionPropagations)
	}

	artifact, _ := result.MarshalCanonical()
	repeatedArtifact, _ := repeated.MarshalCanonical()
	if !bytes.Equal(artifact, repeatedArtifact) {
		t.Fatal("GOMAXPROCS changed module initial self-interrupt execution")
	}
	expected, _ := result.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedArtifact, _ := replayed.MarshalCanonical()
	if !bytes.Equal(artifact, replayedArtifact) {
		t.Fatal("module initial self-interrupt replay changed canonical bytes")
	}
}

func TestCompileModuleInitialTimingRangeExploresAndReplays(t *testing.T) {
	source := []byte(`
type Worker is interface action out Boot(); end interface Worker;
module Startup() return Worker is
  C : Clock is MakeClock();
initial
  Boot() in C.Ticks range 1..2;
end module Startup;
architecture System() is worker : Worker is Startup(); end architecture System;
`)
	model, err := Compile(source, "System")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	expressionSource := bytes.Replace(source, []byte("C.Ticks range 1..2"), []byte("C.Ticks range 1 + 0..1 + 1"), 1)
	expressionModel, err := Compile(expressionSource, "System")
	if err != nil {
		t.Fatal(err)
	}
	expressionDigest, err := expressionModel.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if digest != expressionDigest {
		t.Fatalf("initial literal and closed-expression timing ranges differ: %s != %s", digest, expressionDigest)
	}
	journal := arch.NewExecutionJournal(digest, 20)
	canonical, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	if len(canonical.Choices) != 1 || !strings.HasPrefix(canonical.Choices[0].Domain, "timing-object:") {
		t.Fatalf("initial timing choices=%#v", canonical.Choices)
	}
	if len(canonical.Firings) != 1 || canonical.Firings[0].Transition != "initial" ||
		len(canonical.Firings[0].Scheduled) != 1 {
		t.Fatalf("initial timing audit=%#v", canonical.Firings)
	}
	expressionCanonical, err := expressionModel.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	canonicalBytes, _ := canonical.MarshalCanonical()
	expressionBytes, _ := expressionCanonical.MarshalCanonical()
	if !bytes.Equal(canonicalBytes, expressionBytes) {
		t.Fatal("initial literal and closed-expression timing ranges produced different computations")
	}
	clock := arch.ClockID("worker", "C")
	explored, err := model.ExploreDeterministic(journal, arch.ExplorationLimits{
		MaxExecutions: 4, MaxChoiceDepth: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !explored.Complete || len(explored.Computations) != 2 {
		t.Fatalf("initial timing exploration=%d complete=%v", len(explored.Computations), explored.Complete)
	}
	finishes := make(map[uint64]bool)
	for _, computation := range explored.Computations {
		boot := computation.Result.Poset.ByName("Boot")
		if len(boot) != 1 {
			t.Fatalf("explored initial Boot count=%d", len(boot))
		}
		timing, related := boot[0].Timing(clock)
		if !related || timing.Start != timing.Finish {
			t.Fatalf("explored initial timing=%#v,%v", timing, related)
		}
		finishes[timing.Finish] = true
		artifactDigest, err := computation.Result.ArtifactDigest()
		if err != nil {
			t.Fatal(err)
		}
		memberJournal := journal
		memberJournal.Choices = append([]arch.ChoiceDecision(nil), computation.Schedule...)
		replayed, err := model.ReplayDeterministic(memberJournal, artifactDigest)
		if err != nil {
			t.Fatal(err)
		}
		left, _ := computation.Result.MarshalCanonical()
		right, _ := replayed.MarshalCanonical()
		if !bytes.Equal(left, right) {
			t.Fatal("explored initial timing replay was not byte-identical")
		}
	}
	if !finishes[1] || !finishes[2] {
		t.Fatalf("initial timing exploration missed members: %#v", finishes)
	}
}

func TestModuleInitialFixedTicksMatchesSingletonSubtype(t *testing.T) {
	compile := func(timing string) (*arch.Architecture, string) {
		t.Helper()
		source := []byte(`
type Worker is interface action out Boot(); end interface Worker;
module Startup() return Worker is C : Clock is MakeClock(); initial
  Boot() in ` + timing + `;
end module Startup;
architecture System() is worker : Worker is Startup(); end architecture System;
`)
		model, err := Compile(source, "System")
		if err != nil {
			t.Fatal(err)
		}
		digest, err := model.DeterministicModelDigest()
		if err != nil {
			t.Fatal(err)
		}
		return model, digest
	}
	fixed, fixedDigest := compile("C.Ticks(2)")
	singleton, singletonDigest := compile("C.Ticks range 2..2")
	expression, expressionDigest := compile("C.Ticks(1 + 1)")
	namedSource := []byte(`
type Worker is interface action out Boot(); end interface Worker;
module Startup() return Worker is C : Clock is Make_Clock(); Two : C.Ticks is 2; initial
  Boot() in Two;
end module Startup;
architecture System() is worker : Worker is Startup(); end architecture System;
`)
	named, err := Compile(namedSource, "System")
	if err != nil {
		t.Fatal(err)
	}
	namedDigest, err := named.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if fixedDigest != singletonDigest || fixedDigest != expressionDigest || fixedDigest != namedDigest {
		t.Fatalf("initial fixed, singleton, closed-expression, and named timing differ: %s, %s, %s, %s",
			fixedDigest, singletonDigest, expressionDigest, namedDigest)
	}
	journal := arch.NewExecutionJournal(fixedDigest, 20)
	fixedResult, err := fixed.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	singletonResult, err := singleton.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	expressionResult, err := expression.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	namedResult, err := named.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	left, _ := fixedResult.MarshalCanonical()
	right, _ := singletonResult.MarshalCanonical()
	expressionArtifact, _ := expressionResult.MarshalCanonical()
	namedArtifact, _ := namedResult.MarshalCanonical()
	if len(fixedResult.Choices) != 0 || !bytes.Equal(left, right) ||
		!bytes.Equal(left, expressionArtifact) || !bytes.Equal(left, namedArtifact) {
		t.Fatal("initial fixed timing was not the choice-free singleton/closed-expression/named computation")
	}
	boot := fixedResult.Poset.ByName("Boot")
	if len(boot) != 1 {
		t.Fatalf("initial fixed Boot events=%d, want one", len(boot))
	}
	timing, related := boot[0].Timing(arch.ClockID("worker", "C"))
	if !related || timing.Start != 2 || timing.Finish != 2 {
		t.Fatalf("initial fixed timing=%#v,%v", timing, related)
	}
}

func TestModuleInitialStatementOrderIsSemantic(t *testing.T) {
	compile := func(initial string) (*arch.Architecture, string) {
		t.Helper()
		source := []byte(`
type Worker is interface action out Seen(n : Integer); end interface Worker;
module Startup() return Worker is value : var Integer := 0; initial ` + initial + ` end module Startup;
architecture System() is worker : Worker is Startup(); end architecture System;
`)
		model, err := Compile(source, "System")
		if err != nil {
			t.Fatal(err)
		}
		digest, err := model.DeterministicModelDigest()
		if err != nil {
			t.Fatal(err)
		}
		return model, digest
	}
	leftModel, leftDigest := compile("value := 1; Seen($value);")
	rightModel, rightDigest := compile("Seen($value); value := 1;")
	if leftDigest == rightDigest {
		t.Fatal("reordering module initial statements did not change model identity")
	}
	left, err := leftModel.ExecuteDeterministic(arch.NewExecutionJournal(leftDigest, 10))
	if err != nil {
		t.Fatal(err)
	}
	right, err := rightModel.ExecuteDeterministic(arch.NewExecutionJournal(rightDigest, 10))
	if err != nil {
		t.Fatal(err)
	}
	leftValue, _ := left.Poset.ByName("Seen")[0].Param("n")
	rightValue, _ := right.Poset.ByName("Seen")[0].Param("n")
	if leftValue != int64(1) || rightValue != int64(0) {
		t.Fatalf("initial statement order values left=%#v right=%#v", leftValue, rightValue)
	}
}

func TestModuleInitialLocalFunctionsExecuteSynchronouslyAndReplay(t *testing.T) {
	source := []byte(`
type Worker is interface
  action out Inside(n : Integer);
  action out Boot(value : Integer; touches : Integer);
  requires Calculate : function(n : Integer) return Integer;
  provides Compute : function(n : Integer) return Integer;
  provides Touch : function();
end interface Worker;

module Startup() return Worker is
  result : var Integer := 0;
  touches : var Integer := 0;
  Compute : function(n : Integer) return Integer is
  begin
    Inside(n);
    return n + 3;
  end function Compute;
  Touch : function() is
  begin
    touches := $touches + 1;
  end function Touch;
connect
  Calculate to Compute;
initial
  result := Calculate(4);
  Touch();
  Boot($result, $touches);
end module Startup;

architecture System() is
  worker : Worker is Startup();
end architecture System;
`)
	previous := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(previous)
	var baseline []byte
	for _, processors := range []int{1, 8} {
		runtime.GOMAXPROCS(processors)
		for run := 0; run < 10; run++ {
			model, err := Compile(source, "System")
			if err != nil {
				t.Fatal(err)
			}
			digest, err := model.DeterministicModelDigest()
			if err != nil {
				t.Fatal(err)
			}
			journal := arch.NewExecutionJournal(digest, 30)
			result, err := model.ExecuteDeterministic(journal)
			if err != nil {
				t.Fatal(err)
			}
			computeCall := result.Poset.ByName("Calculate'Call")
			inside := result.Poset.ByName("Inside")
			computeReturn := result.Poset.ByName("Calculate'Return")
			touchCall := result.Poset.ByName("Touch'Call")
			touchReturn := result.Poset.ByName("Touch'Return")
			boot := result.Poset.ByName("Boot")
			if len(computeCall) != 1 || len(inside) != 1 || len(computeReturn) != 1 ||
				len(touchCall) != 1 || len(touchReturn) != 1 || len(boot) != 1 {
				t.Fatalf("initial function event counts=%d/%d/%d/%d/%d/%d",
					len(computeCall), len(inside), len(computeReturn), len(touchCall), len(touchReturn), len(boot))
			}
			if !computeCall[0].HasObservation("worker", "Compute'Call") ||
				!computeReturn[0].HasObservation("worker", "Compute'Return") {
				t.Fatalf("module initial self-route observations call=%#v return=%#v",
					computeCall[0].ObservationViews(), computeReturn[0].ObservationViews())
			}
			if !result.Poset.IsCausallyBefore(computeCall[0].ID, inside[0].ID) ||
				!result.Poset.IsCausallyBefore(inside[0].ID, computeReturn[0].ID) ||
				!result.Poset.IsCausallyBefore(computeReturn[0].ID, touchCall[0].ID) ||
				!result.Poset.IsCausallyBefore(touchCall[0].ID, touchReturn[0].ID) ||
				!result.Poset.IsCausallyBefore(touchReturn[0].ID, boot[0].ID) {
				t.Fatal("module initial function call/body/return sequence lost causality")
			}
			if boot[0].ParamInt("value") != 7 || boot[0].ParamInt("touches") != 1 {
				t.Fatalf("initial function Boot=%#v", boot[0])
			}
			if len(result.State) != 2 || result.State[0].ComponentID != "worker" ||
				result.State[0].Name != "result" || result.State[0].Value.Text != "7" ||
				result.State[1].Name != "touches" || result.State[1].Value.Text != "1" {
				t.Fatalf("initial function state=%#v", result.State)
			}
			if len(result.Firings) != 1 || result.Firings[0].Transition != "initial" ||
				len(result.Firings[0].Generated) != 6 || len(result.Firings[0].StateWrites) != 2 {
				t.Fatalf("initial function audit=%#v", result.Firings)
			}
			encoded, err := result.MarshalCanonical()
			if err != nil {
				t.Fatal(err)
			}
			artifactDigest, err := result.ArtifactDigest()
			if err != nil {
				t.Fatal(err)
			}
			replayed, err := model.ReplayDeterministic(journal, artifactDigest)
			if err != nil {
				t.Fatal(err)
			}
			replayedBytes, _ := replayed.MarshalCanonical()
			if !bytes.Equal(encoded, replayedBytes) {
				t.Fatal("initial function replay was not byte-identical")
			}
			if processors == 1 && run == 0 {
				scheduledJournal := journal
				for _, resolution := range result.Choices {
					scheduledJournal.Choices = append(scheduledJournal.Choices, resolution.Decision())
				}
				explored, err := model.ExploreDeterministic(scheduledJournal, arch.ExplorationLimits{
					MaxExecutions: 64, MaxChoiceDepth: 16,
				})
				if err != nil {
					t.Fatal(err)
				}
				exploredAgain, err := model.ExploreDeterministic(scheduledJournal, arch.ExplorationLimits{
					MaxExecutions: 64, MaxChoiceDepth: 16,
				})
				if err != nil {
					t.Fatal(err)
				}
				exploredBytes, _ := explored.MarshalCanonical()
				exploredAgainBytes, _ := exploredAgain.MarshalCanonical()
				if !explored.Complete || len(explored.Computations) != 1 ||
					!bytes.Equal(exploredBytes, exploredAgainBytes) {
					t.Fatalf("initial function exploration=%d complete=%v",
						len(explored.Computations), explored.Complete)
				}
			}
			if baseline == nil {
				baseline = encoded
			} else if !bytes.Equal(baseline, encoded) {
				t.Fatalf("initial function execution changed under GOMAXPROCS=%d run=%d", processors, run)
			}
		}
	}
}

func TestModuleInitialCrossComponentFunctionRoutesFailExplicitly(t *testing.T) {
	direct := `
type Client is interface requires Lookup : function() return Integer; end interface Client;
type Provider is interface provides Fetch : function() return Integer; end interface Provider;
module ClientModule() return Client is
  result : var Integer := 0;
initial
  result := Lookup();
end module ClientModule;
module ProviderModule() return Provider is
  Fetch : function() return Integer is begin return 1; end function Fetch;
end module ProviderModule;
architecture System() is
  client : Client is ClientModule();
  provider : Provider is ProviderModule();
connect
  client.Lookup to provider.Fetch;
end architecture System;
`
	transitive := `
type Client is interface
  provides Initialize : function();
  requires Lookup : function() return Integer;
end interface Client;
type Provider is interface provides Fetch : function() return Integer; end interface Provider;
module ClientModule() return Client is
  result : var Integer := 0;
  Initialize : function() is begin result := Lookup(); end function Initialize;
initial
  Initialize();
end module ClientModule;
module ProviderModule() return Provider is
  Fetch : function() return Integer is begin return 1; end function Fetch;
end module ProviderModule;
architecture System() is
  client : Client is ClientModule();
  provider : Provider is ProviderModule();
connect
  client.Lookup to provider.Fetch;
end architecture System;
`
	for _, test := range []struct {
		name, source string
	}{{"direct", direct}, {"transitive", transitive}} {
		t.Run(test.name, func(t *testing.T) {
			_, err := Compile([]byte(test.source), "System")
			if err == nil || !strings.Contains(err.Error(),
				"cross-component calls require source-grounded module creation ordering") {
				t.Fatalf("got %v, want cross-component module-initial function diagnostic", err)
			}
		})
	}
}

func TestCompileRejectsMalformedOrUnsupportedModuleInitialParts(t *testing.T) {
	tests := []struct {
		name, declarations, initial, suffix, want string
	}{
		{name: "unknown state", initial: "missing := 1;", want: "targets undeclared state"},
		{name: "unknown action", initial: "Missing();", want: "is not a declared action or function"},
		{name: "missing clock", initial: "Boot() in Missing.Ticks range 1..1;", want: "has no basic clock"},
		{name: "standalone pause", declarations: "C : Clock is MakeClock();", initial: "pause C.Ticks range 1..1;", want: "requires startup continuation semantics"},
		{name: "action delay", declarations: "C : Clock is MakeClock();", initial: "Boot() delay C.Ticks range 1..1;", want: "requires startup continuation semantics"},
		{name: "return", initial: "return;", want: "return is only allowed in a function body"},
		{name: "external in-action interrupt", initial: "do Boot(); handler is Request => Boot(); end do;", want: "initial external in-action interrupt choice"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			provides := ""
			if strings.Contains(test.declarations, "F : function") {
				if strings.Contains(test.declarations, "return Integer") {
					provides = "provides F : function() return Integer;"
				} else {
					provides = "provides F : function();"
				}
			}
			source := []byte("type Worker is interface action in Request(); action out Boot(); " + provides + " end interface Worker; " +
				"module Startup() return Worker is " + test.declarations + " initial " + test.initial + test.suffix +
				" end module Startup; architecture System() is worker : Worker is Startup(); end architecture System;")
			_, err := Compile(source, "System")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v, want diagnostic containing %q", err, test.want)
			}
		})
	}
}

func TestSourceModuleInitialStableAcrossGOMAXPROCS(t *testing.T) {
	source := []byte(`
type Worker is interface action out Boot(n : Integer); end interface Worker;
module Startup() return Worker is
  value : var Integer := 0;
initial
  value := $value + 7;
  Boot($value);
end module Startup;
architecture System() is worker : Worker is Startup(); end architecture System;
`)
	previous := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(previous)
	var baseline []byte
	for _, processors := range []int{1, 8} {
		runtime.GOMAXPROCS(processors)
		for run := 0; run < 10; run++ {
			model, err := Compile(source, "System")
			if err != nil {
				t.Fatal(err)
			}
			digest, err := model.DeterministicModelDigest()
			if err != nil {
				t.Fatal(err)
			}
			result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 10))
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := result.MarshalCanonical()
			if err != nil {
				t.Fatal(err)
			}
			if baseline == nil {
				baseline = encoded
			} else if !bytes.Equal(baseline, encoded) {
				t.Fatalf("module initial changed under GOMAXPROCS=%d run=%d", processors, run)
			}
		}
	}
}

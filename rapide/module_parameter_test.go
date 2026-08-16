package rapide

import (
	"bytes"
	"runtime"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide/arch"
)

func TestParseModuleGeneratorFormalsAndComponentActuals(t *testing.T) {
	file, err := Parse([]byte(`
type Worker is interface end interface Worker;
module Parameterized(Initial : Integer; Enabled : Boolean) return Worker is
end module Parameterized;
architecture System(Base : Integer; Flag : Boolean) is
  worker : Worker is Parameterized(Base + 1, not Flag);
end architecture System;
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(file.Modules) != 1 || len(file.Modules[0].Parameters) != 2 {
		t.Fatalf("module declarations=%#v", file.Modules)
	}
	parameters := file.Modules[0].Parameters
	if parameters[0].Name != "Initial" || parameters[0].Type != "Integer" ||
		parameters[1].Name != "Enabled" || parameters[1].Type != "Boolean" {
		t.Fatalf("module parameters=%#v", parameters)
	}
	component := file.Architectures[0].Components[0]
	if component.Module != "Parameterized" || len(component.ModuleArguments) != 2 ||
		component.ModuleArguments[0].Kind != ExpressionBinary ||
		component.ModuleArguments[1].Kind != ExpressionUnary {
		t.Fatalf("component module application=%#v", component)
	}
}

func TestParseGeneratorDefaultsAndNamedModuleAssociations(t *testing.T) {
	file, err := Parse([]byte(`
type Worker is interface end interface Worker;
module Configured(Offset : Integer is 2; Enabled : Boolean is True) return Worker is
end module Configured;
architecture System(Count : Integer is 3) is
  worker : Worker is Configured(Enabled is False, Offset is Count + 1);
end architecture System;
`))
	if err != nil {
		t.Fatal(err)
	}
	parameters := file.Modules[0].Parameters
	if len(parameters) != 2 || parameters[0].Default == nil || parameters[1].Default == nil ||
		parameters[0].Default.Kind != ExpressionInteger || parameters[1].Default.Kind != ExpressionBoolean {
		t.Fatalf("module defaults=%#v", parameters)
	}
	architectureParameters := file.Architectures[0].Parameters
	if len(architectureParameters) != 1 || architectureParameters[0].Default == nil ||
		architectureParameters[0].Default.Kind != ExpressionInteger {
		t.Fatalf("architecture defaults=%#v", architectureParameters)
	}
	component := file.Architectures[0].Components[0]
	if len(component.ModuleArguments) != 2 || len(component.ModuleArgumentFormals) != 2 ||
		component.ModuleArgumentFormals[0] != "Enabled" || component.ModuleArgumentFormals[1] != "Offset" ||
		component.ModuleArguments[1].Kind != ExpressionBinary {
		t.Fatalf("named module associations=%#v", component)
	}
}

func TestModuleNamedAndDefaultAssociationsAreCanonicalAndExecutable(t *testing.T) {
	compile := func(application string) *arch.Architecture {
		source := []byte(`
type Worker is interface action out Ready(n : Integer); end interface Worker;
module Configured(Offset : Integer is 2; Enabled : Boolean is True) return Worker is
initial
  if Enabled then Ready(Offset); end if;
end module Configured;
architecture System() is
  worker : Worker is Configured(` + application + `);
end architecture System;
`)
		model, err := Compile(source, "System")
		if err != nil {
			t.Fatal(err)
		}
		return model
	}
	models := []*arch.Architecture{
		compile(""),
		compile("Enabled is True, Offset is 2"),
		compile("2, Enabled is True"),
	}
	var expectedDigest string
	var expectedArtifact []byte
	for index, model := range models {
		digest, err := model.DeterministicModelDigest()
		if err != nil {
			t.Fatal(err)
		}
		journal := arch.NewExecutionJournal(digest, 10)
		result, err := model.ExecuteDeterministic(journal)
		if err != nil {
			t.Fatal(err)
		}
		ready := result.Poset.ByName("Ready")
		if len(ready) != 1 || ready[0].Source != "worker" || ready[0].ParamInt("n") != 2 {
			t.Fatalf("association form %d Ready=%#v", index, ready)
		}
		artifact, err := result.MarshalCanonical()
		if err != nil {
			t.Fatal(err)
		}
		if index == 0 {
			expectedDigest, expectedArtifact = digest, artifact
			artifactDigest, err := result.ArtifactDigest()
			if err != nil {
				t.Fatal(err)
			}
			replayed, err := model.ReplayDeterministic(journal, artifactDigest)
			if err != nil {
				t.Fatal(err)
			}
			replayedBytes, err := replayed.MarshalCanonical()
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(artifact, replayedBytes) {
				t.Fatal("defaulted module application replay changed canonical bytes")
			}
			continue
		}
		if digest != expectedDigest || !bytes.Equal(artifact, expectedArtifact) {
			t.Fatalf("association form %d changed model or artifact identity", index)
		}
	}
	overridden := compile("Enabled is False")
	overriddenDigest, err := overridden.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if overriddenDigest == expectedDigest {
		t.Fatal("overriding a default was omitted from canonical model identity")
	}
	overriddenResult, err := overridden.ExecuteDeterministic(arch.NewExecutionJournal(overriddenDigest, 10))
	if err != nil {
		t.Fatal(err)
	}
	if ready := overriddenResult.Poset.ByName("Ready"); len(ready) != 0 {
		t.Fatalf("named default override did not specialize module behavior: %#v", ready)
	}
}

func TestArchitectureGeneratorClosedDefaultsMatchExplicitActuals(t *testing.T) {
	source := []byte(`
type Worker is interface end interface Worker;
architecture System(Count : Integer is 2; Enabled : Boolean is True) is
  workers : array[1..Count] of Worker;
end architecture System;
`)
	implicit, err := Compile(source, "System")
	if err != nil {
		t.Fatal(err)
	}
	explicit, err := CompileWithArguments(source, "system", map[string]any{
		"enabled": true, "count": int32(2),
	})
	if err != nil {
		t.Fatal(err)
	}
	implicitDigest, err := implicit.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	explicitDigest, err := explicit.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if implicitDigest != explicitDigest {
		t.Fatalf("defaulted and explicit architecture actuals differ: %s != %s", implicitDigest, explicitDigest)
	}
	overridden, err := CompileWithArguments(source, "System", map[string]any{"Count": 3})
	if err != nil {
		t.Fatal(err)
	}
	overriddenDigest, err := overridden.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if overriddenDigest == implicitDigest {
		t.Fatal("architecture default override was omitted from canonical identity")
	}
}

func TestArchitectureGeneratorDefaultsRejectInvalidDeclarationData(t *testing.T) {
	tests := []struct {
		name       string
		expression string
		want       string
	}{
		{name: "wrong type", expression: "True", want: "default has type Boolean, want Integer"},
		{name: "open", expression: "Missing", want: "default is not a closed deterministic expression"},
		{name: "failing", expression: "1 / 0", want: "division by zero"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := []byte(`architecture System(N : Integer is ` + test.expression + `) is end architecture System;`)
			_, err := CompileWithArguments(source, "System", map[string]any{"N": 1})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v, want diagnostic containing %q", err, test.want)
			}
		})
	}
}

func TestModuleGeneratorArgumentsSpecializeFreshDeterministicInstances(t *testing.T) {
	source := []byte(`
type Driver is interface action out Tick(); end interface Driver;
type Worker is interface
  action in Tick();
  action out Ready(n : Integer);
  action out Snapshot(n : Integer);
  provides Adjusted : function(value : Integer) return Integer;
end interface Worker;

module Parameterized(Initial : Integer; Enabled : Boolean) return Worker is
  count : var Integer := Initial;
  Adjusted : function(value : Integer) return Integer is
  begin
    return value + Initial;
  end function Adjusted;
initial
  if Enabled then Ready(Initial); end if;
parallel
  when Tick do
    if Enabled then count := Adjusted($count); end if;
    Snapshot($count);
  end when;
end module Parameterized;

architecture System(Base : Integer) is
  left_driver : Driver;
  right_driver : Driver;
  left : Worker is Parameterized(Base, True);
  right : Worker is Parameterized(Base + 3, False);
connect
  left_driver.Tick => left.Tick;
  right_driver.Tick => right.Tick;
end architecture System;
`)
	left, err := CompileWithArguments(source, "system", map[string]any{"BASE": int32(4)})
	if err != nil {
		t.Fatal(err)
	}
	right, err := CompileWithArguments(source, "SYSTEM", map[string]any{"base": int64(4)})
	if err != nil {
		t.Fatal(err)
	}
	leftDigest, err := left.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	rightDigest, err := right.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if leftDigest != rightDigest {
		t.Fatalf("equivalent architecture actuals changed specialized module identity: %s != %s", leftDigest, rightDigest)
	}

	journal := arch.NewExecutionJournal(leftDigest, 40,
		arch.InputEvent{Key: "left-tick", Source: "left_driver", Action: "Tick"},
		arch.InputEvent{Key: "right-tick", Source: "right_driver", Action: "Tick"},
	)
	previous := runtime.GOMAXPROCS(1)
	first, firstErr := left.ExecuteDeterministic(journal)
	runtime.GOMAXPROCS(8)
	second, secondErr := right.ExecuteDeterministic(journal)
	runtime.GOMAXPROCS(previous)
	if firstErr != nil {
		t.Fatal(firstErr)
	}
	if secondErr != nil {
		t.Fatal(secondErr)
	}
	firstBytes, err := first.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	secondBytes, err := second.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatal("module specialization changed across argument spelling, Go integer width, or GOMAXPROCS")
	}

	ready := first.Poset.ByName("Ready")
	if len(ready) != 1 || ready[0].Source != "left" {
		t.Fatalf("formal-controlled initial output=%#v", ready)
	}
	if value, _ := ready[0].Param("n"); value != int64(4) {
		t.Fatalf("formal-controlled Ready value=%#v, want 4", value)
	}
	snapshots := first.Poset.ByName("Snapshot")
	if len(snapshots) != 2 {
		t.Fatalf("Snapshot outputs=%d, want two", len(snapshots))
	}
	values := make(map[string]int64, len(snapshots))
	for _, event := range snapshots {
		value, _ := event.Param("n")
		values[event.Source] = value.(int64)
	}
	if values["left"] != 8 || values["right"] != 7 {
		t.Fatalf("specialized outputs crossed instances: %#v", values)
	}
	states := make(map[string]string, len(first.State))
	for _, state := range first.State {
		states[state.ComponentID+"."+state.Name] = state.Value.Text
	}
	if len(states) != 2 || states["left.count"] != "8" || states["right.count"] != "7" {
		t.Fatalf("specialized state was not isolated: %#v", states)
	}
	if len(first.Poset.ByName("Adjusted'Call")) != 1 || len(first.Poset.ByName("Adjusted'Return")) != 1 {
		t.Fatal("module formal was not captured by the specialized provided function")
	}

	artifactDigest, err := first.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := left.ReplayDeterministic(journal, artifactDigest)
	if err != nil {
		t.Fatal(err)
	}
	replayedBytes, err := replayed.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstBytes, replayedBytes) {
		t.Fatal("specialized module replay changed canonical artifact bytes")
	}
}

func TestUnusedModuleGeneratorArgumentRemainsCanonicalMembershipData(t *testing.T) {
	build := func(actual string) string {
		source := []byte(`
type Empty is interface end interface Empty;
module Tagged(Audit_Tag : Integer) return Empty is end module Tagged;
architecture System() is worker : Empty is Tagged(` + actual + `); end architecture System;
`)
		model, err := Compile(source, "System")
		if err != nil {
			t.Fatal(err)
		}
		digest, err := model.DeterministicModelDigest()
		if err != nil {
			t.Fatal(err)
		}
		return digest
	}
	if build("1") == build("2") {
		t.Fatal("unused module-generator actual was omitted from canonical model identity")
	}
}

func TestModuleGeneratorArgumentsElaboratePerInstanceConnectionTopology(t *testing.T) {
	source := []byte(`
type Driver is interface action out Send(n : Integer); end interface Driver;
type Relay is interface
  action in Receive(n : Integer);
  action out Forward(n : Integer);
end interface Relay;
module Configured(Offset : Integer; Enabled : Boolean) return Relay is
connect
  if Enabled generate
    (?N : Integer) Receive(?N) to Forward(?N + Offset);
  end generate if;
end module Configured;
architecture System() is
  left_driver : Driver;
  right_driver : Driver;
  enabled : Relay is Configured(5, True);
  disabled : Relay is Configured(5, False);
connect
  left_driver.Send => enabled.Receive;
  right_driver.Send => disabled.Receive;
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
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 30,
		arch.InputEvent{Key: "enabled", Source: "left_driver", Action: "Send", Params: map[string]any{"n": 3}},
		arch.InputEvent{Key: "disabled", Source: "right_driver", Action: "Send", Params: map[string]any{"n": 7}},
	))
	if err != nil {
		t.Fatal(err)
	}
	forwarded := result.Poset.ByName("Forward")
	if len(forwarded) != 1 || forwarded[0].Source != "enabled" {
		t.Fatalf("formal-specialized module connection outputs=%#v", forwarded)
	}
	if value, _ := forwarded[0].Param("n"); value != int64(8) {
		t.Fatalf("formal-specialized connection value=%#v, want 8", value)
	}
}

func TestModuleGeneratorArgumentsRejectUnsupportedOrInvalidApplications(t *testing.T) {
	application := func(formals, actuals string) []byte {
		return []byte(`
type Worker is interface end interface Worker;
module M(` + formals + `) return Worker is end module M;
architecture System() is worker : Worker is M(` + actuals + `); end architecture System;
`)
	}
	tests := []struct {
		name   string
		source []byte
		want   string
	}{
		{name: "missing", source: application("N : Integer", ""), want: "expects 1 actual parameters"},
		{name: "extra", source: application("N : Integer", "1, 2"), want: "expects 1 actual parameters"},
		{name: "wrong type", source: application("N : Integer", "True"), want: "has type Boolean, want Integer"},
		{name: "open actual", source: application("N : Integer", "Missing"), want: `name "Missing" is not declared`},
		{name: "invalid constant", source: application("N : Integer", "1 / 0"), want: "division by zero"},
		{name: "duplicate formal", source: application("N : Integer; n : Integer", "1, 2"), want: "duplicate or empty module generator parameter"},
		{name: "object type mismatch", source: application("Label : String", "1"), want: "has type Integer, want String"},
		{name: "type formal", source: application("type Element", "Integer"), want: "type formals are outside"},
		{name: "unknown named formal", source: application("N : Integer", "Missing is 1"), want: `no formal parameter named "Missing"`},
		{name: "duplicate named formal", source: application("N : Integer", "N is 1, n is 2"), want: `supplies module parameter "N" more than once`},
		{name: "positional after named", source: application("N : Integer; B : Boolean", "B is True, 1"), want: "positional module arguments must precede named associations"},
		{name: "missing required named formal", source: application("N : Integer; B : Boolean is True", "B is False"), want: `parameter "N" requires an explicit Integer argument`},
		{name: "wrong default type", source: application("N : Integer is True", "1"), want: "default has type Boolean, want Integer"},
		{name: "open default", source: application("N : Integer is Missing", "1"), want: `default is not a closed deterministic expression`},
		{name: "failing default", source: application("N : Integer is 1 / 0", "1"), want: "division by zero"},
		{
			name: "unused invalid declaration",
			source: []byte(`
type Worker is interface end interface Worker;
module M(N : Integer) return Worker is N : var Integer := 0; end module M;
architecture System() is end architecture System;
`),
			want: "conflicting declarations",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Compile(test.source, "System")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v, want diagnostic containing %q", err, test.want)
			}
		})
	}
}

package rapide

import (
	"bytes"
	"runtime"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide/arch"
)

const namedPatternAssociationSource = `
type Worker is interface
  action out Write(version : Integer, initial : Boolean);
end interface Worker;
architecture System() is
  worker : Worker;
constraint
  never (?V : Integer) worker.Write(initial is True, version is ?V);
end architecture System;
`

func TestNamedPatternAssociationsBindFilterOmitAndReplay(t *testing.T) {
	model, err := Compile([]byte(namedPatternAssociationSource), "System")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := arch.NewExecutionJournal(digest, 10,
		arch.InputEvent{Key: "false", Source: "worker", Action: "Write", Params: map[string]any{"version": 1, "initial": false}},
		arch.InputEvent{Key: "true", Source: "worker", Action: "Write", Params: map[string]any{"version": 2, "initial": true}},
	)
	result, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	if result.Constraints == nil || result.Constraints.Passed || len(result.Constraints.Reports) != 1 ||
		len(result.Constraints.Reports[0].Violations) != 1 {
		t.Fatalf("named association report=%#v", result.Constraints)
	}
	violation := result.Constraints.Reports[0].Violations[0]
	if len(violation.Events) != 1 || len(violation.Bindings) != 1 ||
		violation.Bindings[0].Placeholder != "V" {
		t.Fatalf("named association witness=%#v", violation)
	}
	matched := result.Poset.ByName("Write")
	if len(matched) != 2 {
		t.Fatalf("write events=%#v", matched)
	}
	witnessVersion := 0
	for _, event := range matched {
		if string(event.ID) == violation.Events[0] {
			witnessVersion = event.ParamInt("version")
		}
	}
	if witnessVersion != 2 {
		t.Fatalf("literal association selected version %d, want 2", witnessVersion)
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
	replayBytes, err := replayed.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, replayBytes) {
		t.Fatal("named basic-pattern association replay was not byte-identical")
	}
}

func TestNamedPatternAssociationOrderIsNotSemantic(t *testing.T) {
	compile := func(associations string) string {
		t.Helper()
		source := []byte(`
type Worker is interface action out Write(version : Integer, initial : Boolean); end interface Worker;
architecture System() is worker : Worker; constraint
  never (?V : Integer) worker.Write(` + associations + `);
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
		return digest
	}
	left := compile("version is ?V, initial is True")
	right := compile("initial is True, version is ?V")
	if left != right {
		t.Fatalf("named association order changed model identity: %s != %s", left, right)
	}
}

func TestMixedPositionalThenNamedAssociationAndOmittedWildcard(t *testing.T) {
	source := []byte(`
type Worker is interface
  action out Write(version : Integer, initial : Boolean, audit : Boolean);
  action out Accepted(version : Integer);
  behavior begin
    (?V : Integer) Write(?V, initial is True) => Accepted(?V);;
end interface Worker;
architecture System() is worker : Worker; end architecture System;
`)
	model, err := Compile(source, "System")
	if err != nil {
		t.Fatal(err)
	}
	digest, _ := model.DeterministicModelDigest()
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 10,
		arch.InputEvent{Key: "accepted", Source: "worker", Action: "Write", Params: map[string]any{"version": 7, "initial": true, "audit": false}},
		arch.InputEvent{Key: "filtered", Source: "worker", Action: "Write", Params: map[string]any{"version": 8, "initial": false, "audit": true}},
	))
	if err != nil {
		t.Fatal(err)
	}
	accepted := result.Poset.ByName("Accepted")
	if len(accepted) != 1 || accepted[0].ParamInt("version") != 7 {
		t.Fatalf("mixed/omitted association result=%#v", accepted)
	}
}

func TestLiteralAssociationFiltersBasicArchitectureAndModuleConnections(t *testing.T) {
	source := []byte(`
type Driver is interface action out Send(value : Integer, enabled : Boolean); end interface Driver;
type Relay is interface
  action in Receive(value : Integer, enabled : Boolean);
  action out Publish(value : Integer, enabled : Boolean);
end interface Relay;
module RelayModule() return Relay is
connect Receive(enabled is True) to Publish;
end module RelayModule;
architecture System() is driver : Driver; relay : Relay is RelayModule(); connect
  driver.Send(enabled is True) to relay.Receive;
end architecture System;
`)
	model, err := Compile(source, "System")
	if err != nil {
		t.Fatal(err)
	}
	digest, _ := model.DeterministicModelDigest()
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 20,
		arch.InputEvent{Key: "disabled", Source: "driver", Action: "Send", Params: map[string]any{"value": 1, "enabled": false}},
		arch.InputEvent{Key: "enabled", Source: "driver", Action: "Send", Params: map[string]any{"value": 2, "enabled": true}},
	))
	if err != nil {
		t.Fatal(err)
	}
	received, published := result.Poset.ByName("Receive"), result.Poset.ByName("Publish")
	if len(received) != 1 || len(published) != 1 || received[0].ParamInt("value") != 2 ||
		published[0].ParamInt("value") != 2 || received[0].ID != published[0].ID {
		t.Fatalf("literal-filtered connections receive=%#v publish=%#v", received, published)
	}
}

func TestConstraintAlphabetAcceptsClosedLiteralAssociation(t *testing.T) {
	source := []byte(`
type Worker is interface action out Write(initial : Boolean); end interface Worker;
architecture System() is worker : Worker; constraint
  observe from worker.Write(initial is True)
    match worker.Write(initial is True);
  end observe;
end architecture System;
`)
	model, err := Compile(source, "System")
	if err != nil {
		t.Fatal(err)
	}
	digest, _ := model.DeterministicModelDigest()
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 10,
		arch.InputEvent{Key: "false", Source: "worker", Action: "Write", Params: map[string]any{"initial": false}},
		arch.InputEvent{Key: "true", Source: "worker", Action: "Write", Params: map[string]any{"initial": true}},
	))
	if err != nil {
		t.Fatal(err)
	}
	if result.Constraints == nil || !result.Constraints.Passed || len(result.Constraints.Reports) != 1 ||
		result.Constraints.Reports[0].EvaluationPosetDigest == result.Constraints.Reports[0].PosetDigest {
		t.Fatalf("literal alphabet report=%#v", result.Constraints)
	}
}

func TestCompileRejectsMalformedOrUnsupportedPatternAssociations(t *testing.T) {
	tests := []struct {
		name, pattern, placeholders, want string
	}{
		{name: "positional after named", pattern: "Write(initial is True, ?V)", placeholders: "(?V : Integer)", want: "positional basic-pattern associations must precede named"},
		{name: "unknown formal", pattern: "Write(missing is True)", want: `has no parameter named "missing"`},
		{name: "duplicate formal", pattern: "Write(initial is True, initial is False)", want: `parameter "initial" is associated more than once`},
		{name: "positional and named duplicate", pattern: "Write(?V, version is ?V)", placeholders: "(?V : Integer)", want: `parameter "version" is associated more than once`},
		{name: "too many positional", pattern: "Write(?V, True, False)", placeholders: "(?V : Integer)", want: "supplies positional association 3"},
		{name: "undeclared placeholder", pattern: "Write(version is ?V)", want: "placeholder ?V is not declared"},
		{name: "wrong placeholder type", pattern: "Write(initial is ?V)", placeholders: "(?V : Integer)", want: "has type Integer but action parameter initial has type Boolean"},
		{name: "wrong literal type", pattern: "Write(initial is 1)", want: "has type Integer but the action parameter has type Boolean"},
		{name: "object expression", pattern: "Write(version is Constant)", want: "unsupported object or state-dependent expression"},
		{name: "nested placeholder", pattern: "Write(version is ?V + 1)", placeholders: "(?V : Integer)", want: "placeholder only as the entire actual parameter"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := []byte(`
type Worker is interface action out Write(version : Integer, initial : Boolean); end interface Worker;
architecture System() is worker : Worker; constraint never ` + test.placeholders + ` worker.` + test.pattern + `; end architecture System;
`)
			_, err := Compile(source, "System")
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(test.want)) {
				t.Fatalf("pattern %q error=%v, want %q", test.pattern, err, test.want)
			}
		})
	}
}

func TestNamedPatternAssociationsStableAcrossGOMAXPROCS(t *testing.T) {
	previous := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(previous)
	var baseline []byte
	for _, processors := range []int{1, 8} {
		runtime.GOMAXPROCS(processors)
		for run := 0; run < 10; run++ {
			model, err := Compile([]byte(namedPatternAssociationSource), "System")
			if err != nil {
				t.Fatal(err)
			}
			digest, _ := model.DeterministicModelDigest()
			result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 10,
				arch.InputEvent{Key: "false", Source: "worker", Action: "Write", Params: map[string]any{"initial": false, "version": 1}},
				arch.InputEvent{Key: "true", Source: "worker", Action: "Write", Params: map[string]any{"initial": true, "version": 2}},
			))
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
				t.Fatalf("named association artifact changed at GOMAXPROCS=%d run=%d", processors, run)
			}
		}
	}
}

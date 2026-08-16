package rapide

import (
	"bytes"
	"runtime"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide/arch"
)

const guardedConstraintSource = `
type Store is interface
  action out Write(version : Integer);
end interface Store;
architecture System() is store : Store;
constraint
  never (?V1, ?V2 : Integer)
    (store.Write(?V1) -> store.Write(?V2)) where ?V1 >= ?V2;
end architecture System;
`

func TestConstraintWhereUsesCompletePlaceholderMatchAndReplays(t *testing.T) {
	model, err := Compile([]byte(guardedConstraintSource), "System")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := arch.NewExecutionJournal(digest, 10,
		arch.InputEvent{Key: "new", Source: "store", Action: "Write", Params: map[string]any{"version": 2}},
		arch.InputEvent{Key: "old", Source: "store", Action: "Write", Params: map[string]any{"version": 1}, Causes: []string{"new"}},
	)
	result, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	if result.Constraints == nil || result.Constraints.Passed || len(result.Constraints.Reports) != 1 ||
		len(result.Constraints.Reports[0].Violations) != 1 {
		t.Fatalf("guarded constraint report=%#v", result.Constraints)
	}
	violation := result.Constraints.Reports[0].Violations[0]
	if len(violation.Events) != 2 || len(violation.Bindings) != 2 ||
		violation.Bindings[0].Placeholder != "V1" || violation.Bindings[0].Value.Text != "2" ||
		violation.Bindings[1].Placeholder != "V2" || violation.Bindings[1].Value.Text != "1" {
		t.Fatalf("guarded constraint witness=%#v", violation)
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
		t.Fatal("guarded constraint replay was not byte-identical")
	}
}

func TestConstraintWhereFalseMatchDoesNotViolate(t *testing.T) {
	model, err := Compile([]byte(guardedConstraintSource), "System")
	if err != nil {
		t.Fatal(err)
	}
	digest, _ := model.DeterministicModelDigest()
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 10,
		arch.InputEvent{Key: "old", Source: "store", Action: "Write", Params: map[string]any{"version": 1}},
		arch.InputEvent{Key: "new", Source: "store", Action: "Write", Params: map[string]any{"version": 2}, Causes: []string{"old"}},
	))
	if err != nil {
		t.Fatal(err)
	}
	if result.Constraints == nil || !result.Constraints.Passed {
		t.Fatalf("increasing versions failed guarded constraint: %#v", result.Constraints)
	}
}

func TestConstraintWhereIsDeterministicAcrossGOMAXPROCS(t *testing.T) {
	model, err := Compile([]byte(guardedConstraintSource), "System")
	if err != nil {
		t.Fatal(err)
	}
	digest, _ := model.DeterministicModelDigest()
	journal := arch.NewExecutionJournal(digest, 10,
		arch.InputEvent{Key: "new", Source: "store", Action: "Write", Params: map[string]any{"version": 2}},
		arch.InputEvent{Key: "old", Source: "store", Action: "Write", Params: map[string]any{"version": 1}, Causes: []string{"new"}},
	)
	previous := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previous)
	var baseline []byte
	for _, processors := range []int{1, 2, 4} {
		runtime.GOMAXPROCS(processors)
		result, err := model.ExecuteDeterministic(journal)
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
			t.Fatalf("guarded constraint artifact changed at GOMAXPROCS=%d", processors)
		}
	}
}

func TestConstraintWhereChangesCanonicalModelIdentity(t *testing.T) {
	compileDigest := func(operator string) string {
		t.Helper()
		source := strings.Replace(guardedConstraintSource, ">=", operator, 1)
		model, err := Compile([]byte(source), "System")
		if err != nil {
			t.Fatal(err)
		}
		digest, err := model.DeterministicModelDigest()
		if err != nil {
			t.Fatal(err)
		}
		return digest
	}
	if greaterEqual, lessEqual := compileDigest(">="), compileDigest("<="); greaterEqual == lessEqual {
		t.Fatalf("different pattern guards produced the same model digest %s", greaterEqual)
	}
}

func TestConstraintWhereSupportsRapideIntegerSubtypes(t *testing.T) {
	source := []byte(`
type Store is interface action out Write(version : Natural); end interface Store;
architecture System() is store : Store;
constraint never (?N : Natural) store.Write(?N) where ?N > 0;
end architecture System;
`)
	if _, err := Compile(source, "System"); err != nil {
		t.Fatalf("Natural-valued guard did not compile: %v", err)
	}
}

func TestInterfaceAndModuleConstraintWhereGuardsAreInstantiated(t *testing.T) {
	source := []byte(`
type Worker is interface
  private action Hidden(value : Integer);
  action out Error(value : Integer);
  constraint never (?N : Integer) Hidden(?N) where ?N < 0;
end interface Worker;
module WorkerModule() return Worker is
constraint never (?N : Integer) Error(?N) where ?N < 0;
initial Hidden(-1); Error(-2);
end module WorkerModule;
architecture System() is worker : Worker is WorkerModule(); end architecture System;
`)
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
	if len(result.ModuleConstraints) != 1 || result.ModuleConstraints[0].Report.Passed ||
		len(result.ModuleConstraints[0].Report.Reports) != 2 {
		t.Fatalf("guarded interface/module reports=%#v", result.ModuleConstraints)
	}
	for _, report := range result.ModuleConstraints[0].Report.Reports {
		if report.Passed || len(report.Violations) != 1 {
			t.Fatalf("guarded interface/module member=%#v", report)
		}
	}
}

func TestCompileRejectsUnsupportedConstraintWhereForms(t *testing.T) {
	tests := []struct {
		clause string
		want   string
	}{
		{clause: "never (?N : Integer) store.Write(?N) where ?N;", want: "pattern guard has type Integer, want Boolean"},
		{clause: "never (?N : Integer) store.Write(?N) where ?Missing > 0;", want: "placeholder ?Missing is not declared"},
		{clause: "never (?N : Integer) store.Write(?N) where $state > 0;", want: "is not declared in this constraint scope"},
		{clause: "never (?N : Integer) store.Write(?N) where ?N and True;", want: `operator "and" is not defined for Integer and Boolean`},
	}
	for _, test := range tests {
		t.Run(test.clause, func(t *testing.T) {
			source := []byte(`
type Store is interface action out Write(version : Integer); end interface Store;
architecture System() is store : Store; constraint ` + test.clause + ` end architecture System;
`)
			_, err := Compile(source, "System")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v, want %q", err, test.want)
			}
		})
	}
}

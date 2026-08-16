package rapide

import (
	"bytes"
	"runtime"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/arch"
	"github.com/ShaneDolphin/gorapide/constraint"
)

const moduleConstraintSource = `
type Worker is interface
  private action Hidden(n : Integer);
  action out Error();
  constraint
    match (?N : Integer) Hidden(?N);
end interface Worker;

module WorkerModule() return Worker is
constraint
  never Error;
initial
  Hidden(7);
end module WorkerModule;

architecture System() is
  worker : Worker is WorkerModule();
end architecture System;
`

func TestCompileInterfaceAndModuleConstraintsSeePrivateInitializer(t *testing.T) {
	model, err := Compile([]byte(moduleConstraintSource), "System")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := arch.NewExecutionJournal(digest, 10)
	result, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	if result.Constraints != nil {
		t.Fatalf("interface/module constraints leaked into architecture report: %#v", result.Constraints)
	}
	if len(result.ModuleConstraints) != 1 || result.ModuleConstraints[0].ComponentID != "worker" ||
		!result.ModuleConstraints[0].Report.Passed || len(result.ModuleConstraints[0].Report.Reports) != 2 {
		t.Fatalf("source module constraint report=%#v", result.ModuleConstraints)
	}
	hidden := result.Poset.ByName("Hidden")
	if len(hidden) != 1 {
		t.Fatalf("private initializer events=%#v", hidden)
	}
	value, ok := hidden[0].Param("n")
	if !ok || value != int64(7) {
		t.Fatalf("private initializer parameter=%#v,%v", value, ok)
	}
	encoded, err := result.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(encoded, []byte(`"module_constraint_reports"`)) ||
		!bytes.Contains(encoded, []byte(`"component_id":"worker"`)) {
		t.Fatalf("module constraint audit is absent from artifact: %s", encoded)
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
		t.Fatal("source module constraint replay was not byte-identical")
	}
}

func TestModuleGeneratorConstraintsApplyIndependentlyPerCall(t *testing.T) {
	source := []byte(`
type Driver is interface action out ActivateA(); action out ActivateB(); end interface Driver;
type Worker is interface action in Activate(); private action Hidden(); end interface Worker;
module WorkerModule() return Worker is
constraint
  never Hidden;
parallel
  when Activate do Hidden(); end when;
end module WorkerModule;
architecture System() is
  driver : Driver;
  a : Worker is WorkerModule();
  b : Worker is WorkerModule();
connect
  driver.ActivateA to a.Activate;
  driver.ActivateB to b.Activate;
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
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 20,
		arch.InputEvent{Key: "activate-a", Source: "driver", Action: "ActivateA"},
	))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.ModuleConstraints) != 2 || result.ModuleConstraints[0].ComponentID != "a" ||
		result.ModuleConstraints[1].ComponentID != "b" || result.ModuleConstraints[0].Report.Passed ||
		!result.ModuleConstraints[1].Report.Passed {
		t.Fatalf("per-call module constraint reports=%#v", result.ModuleConstraints)
	}
	violations := result.ModuleConstraints[0].Report.Reports[0].Violations
	if len(violations) != 1 || len(violations[0].Events) != 1 {
		t.Fatalf("private per-call violation=%#v", violations)
	}
	event, ok := result.Poset.Get(gorapide.EventID(violations[0].Events[0]))
	if !ok || event.Source != "a" || event.Name != "Hidden" {
		t.Fatalf("private violation witness=%#v,%v", event, ok)
	}
	if len(result.Poset.ByName("Hidden")) != 1 {
		t.Fatal("inactive module generator call produced a private event")
	}
}

func TestSourceModuleConstraintReadsStateAtMatchedEventCut(t *testing.T) {
	for _, test := range []struct {
		name       string
		initial    string
		wantPassed bool
		wantState  string
	}{
		{name: "assignment before match", initial: "version := 1; Check();", wantPassed: false, wantState: "1"},
		{name: "assignment after match", initial: "Check(); version := 1;", wantPassed: true, wantState: "1"},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := []byte(`
type Worker is interface action out Check(); end interface Worker;
module WorkerModule() return Worker is
  version : var Integer := 0;
constraint
  never Check where $version > 0;
initial
  ` + test.initial + `
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
			journal := arch.NewExecutionJournal(digest, 10)
			result, err := model.ExecuteDeterministic(journal)
			if err != nil {
				t.Fatal(err)
			}
			if len(result.ModuleConstraints) != 1 || result.ModuleConstraints[0].Report.Passed != test.wantPassed {
				t.Fatalf("state-guarded source report=%#v", result.ModuleConstraints)
			}
			if len(result.State) != 1 || result.State[0].Value.Text != test.wantState {
				t.Fatalf("source module state=%#v", result.State)
			}
			if !test.wantPassed {
				violations := result.ModuleConstraints[0].Report.Reports[0].Violations
				if len(violations) != 1 || len(violations[0].StateWitnesses) != 1 {
					t.Fatalf("source state witness=%#v", violations)
				}
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
				t.Fatal("source state-guarded constraint replay was not byte-identical")
			}
		})
	}
}

func TestSourceModuleNegativeMatchReadsStateAtMatchedEventCut(t *testing.T) {
	source := []byte(`
type Worker is interface action out Check(); end interface Worker;
module WorkerModule() return Worker is
  version : var Integer := 0;
constraint
  Policy: not match Check where $version > 0;
initial
  version := 1;
  Check();
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
	journal := arch.NewExecutionJournal(digest, 10)
	result, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.ModuleConstraints) != 1 || result.ModuleConstraints[0].Report.Passed ||
		len(result.ModuleConstraints[0].Report.Reports) != 1 ||
		len(result.ModuleConstraints[0].Report.Reports[0].Violations) != 1 {
		t.Fatalf("negative state source report=%#v", result.ModuleConstraints)
	}
	violation := result.ModuleConstraints[0].Report.Reports[0].Violations[0]
	if violation.Kind != constraint.MustNotMatch.String() || len(violation.StateWitnesses) != 1 {
		t.Fatalf("negative state source violation=%#v", violation)
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
		t.Fatal("negative state source replay was not byte-identical")
	}
}

func TestModuleConstraintDeclarationOrderIsNotSemantic(t *testing.T) {
	compile := func(clauses string) string {
		t.Helper()
		source := []byte(`
type Worker is interface private action Hidden(); action out Error(); end interface Worker;
module WorkerModule() return Worker is constraint ` + clauses + ` end module WorkerModule;
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
		return digest
	}
	left := compile("never Hidden; never Error;")
	right := compile("never Error; never Hidden;")
	if left != right {
		t.Fatalf("module constraint declaration order changed model identity: %s != %s", left, right)
	}
}

func TestCompileRejectsMalformedOrUnsupportedModuleConstraints(t *testing.T) {
	tests := []struct {
		name, constraint, want string
	}{
		{name: "empty", constraint: "constraint", want: "module 'match', 'never', or 'observe' constraint"},
		{name: "qualified", constraint: "constraint never self.Hidden;", want: "cannot be component-qualified"},
		{name: "missing action", constraint: "constraint never Missing;", want: "is not declared"},
		{name: "unsupported placeholder", constraint: "constraint never (?N : Duration) Hidden(?N);", want: "unsupported type \"Duration\""},
		{name: "unbound placeholder", constraint: "constraint never (?N : Integer) Hidden;", want: "placeholder ?N is never bound"},
		{name: "undeclared state guard", constraint: "constraint never Hidden where $missing > 0;", want: "not declared in this constraint scope"},
		{name: "duplicate", constraint: "constraint never Hidden; never Hidden;", want: "duplicate module-visible constraint"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := []byte(`
type Worker is interface private action Hidden(n : Integer); end interface Worker;
module WorkerModule() return Worker is ` + test.constraint + ` end module WorkerModule;
architecture System() is worker : Worker is WorkerModule(); end architecture System;
`)
			_, err := Compile(source, "System")
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(test.want)) {
				t.Fatalf("constraint %q error=%v, want %q", test.constraint, err, test.want)
			}
		})
	}
}

func TestModuleConstraintPartMustPrecedeConnectPart(t *testing.T) {
	source := []byte(`
type Worker is interface action in Trigger(); action out Published(); end interface Worker;
module WorkerModule() return Worker is
connect Trigger to Published;
constraint never Published;
end module WorkerModule;
architecture System() is worker : Worker is WorkerModule(); end architecture System;
`)
	if _, err := Compile(source, "System"); err == nil {
		t.Fatal("module constraint part after connect was accepted")
	}
}

func TestUnusedModuleGeneratorConstraintsAreValidated(t *testing.T) {
	source := []byte(`
type Worker is interface private action Hidden(); end interface Worker;
type Used is interface action out Ready(); end interface Used;
module Invalid() return Worker is constraint never Missing; end module Invalid;
architecture System() is used : Used; end architecture System;
`)
	_, err := Compile(source, "System")
	if err == nil || !strings.Contains(err.Error(), `action "Missing" is not declared`) {
		t.Fatalf("unused invalid module constraint error=%v", err)
	}
}

func TestSourceModuleConstraintsStableAcrossGOMAXPROCS(t *testing.T) {
	previous := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(previous)
	var baseline []byte
	for _, processors := range []int{1, 8} {
		runtime.GOMAXPROCS(processors)
		for run := 0; run < 10; run++ {
			model, err := Compile([]byte(moduleConstraintSource), "System")
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
				t.Fatalf("module constraint artifact changed at GOMAXPROCS=%d run=%d", processors, run)
			}
		}
	}
}

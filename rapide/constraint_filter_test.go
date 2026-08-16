package rapide

import (
	"bytes"
	"runtime"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide/arch"
)

const filteredArchitectureConstraintSource = `
type Worker is interface
  action out Start(); action out Noise(); action out Done();
end interface Worker;
architecture System() is
  worker : Worker;
constraint
  observe from worker.Start, worker.Done
    match (worker.Start -> worker.Done);
    never worker.Noise;
  end observe;
end architecture System;
`

func TestCompileArchitectureAlphabetFilterAuditsObservedAndFilteredComputations(t *testing.T) {
	model, err := Compile([]byte(filteredArchitectureConstraintSource), "System")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := arch.NewExecutionJournal(digest, 10,
		arch.InputEvent{Key: "start", Source: "worker", Action: "Start"},
		arch.InputEvent{Key: "noise", Source: "worker", Action: "Noise", Causes: []string{"start"}},
		arch.InputEvent{Key: "done", Source: "worker", Action: "Done", Causes: []string{"noise"}},
	)
	result, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	if result.Constraints == nil || !result.Constraints.Passed || len(result.Constraints.Reports) != 1 {
		t.Fatalf("filtered architecture report=%#v", result.Constraints)
	}
	for _, report := range result.Constraints.Reports {
		if !report.Passed || report.PosetDigest != result.Constraints.PosetDigest ||
			report.EvaluationPosetDigest == "" || report.EvaluationPosetDigest == report.PosetDigest {
			t.Fatalf("filtered member report=%#v", report)
		}
	}
	encoded, err := result.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(encoded, []byte(`"evaluation_poset_digest"`)) {
		t.Fatalf("filtered computation digest missing from artifact: %s", encoded)
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
		t.Fatal("filtered architecture constraint replay was not byte-identical")
	}
}

func TestCompileModuleAlphabetFilterCanSelectPrivateActions(t *testing.T) {
	source := []byte(`
type Worker is interface private action Hidden(); action out Noise(); end interface Worker;
module WorkerModule() return Worker is
constraint
  observe from Hidden
    match Hidden;
    never Noise;
  end;
initial
  Hidden();
  Noise();
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
	if result.Constraints != nil || len(result.ModuleConstraints) != 1 ||
		!result.ModuleConstraints[0].Report.Passed || len(result.ModuleConstraints[0].Report.Reports) != 1 {
		t.Fatalf("private module alphabet reports=%#v/%#v", result.Constraints, result.ModuleConstraints)
	}
	for _, report := range result.ModuleConstraints[0].Report.Reports {
		if report.EvaluationPosetDigest == report.PosetDigest {
			t.Fatal("module alphabet filter did not exclude the unlisted public action")
		}
	}
	if len(result.Poset.ByName("Hidden")) != 1 || len(result.Poset.ByName("Noise")) != 1 {
		t.Fatal("alphabet filtering mutated the full audit poset")
	}
}

func TestCompileInterfaceAlphabetFilterAppliesToGeneratedModule(t *testing.T) {
	source := []byte(`
type Worker is interface
  private action Hidden(); action out Noise();
  constraint observe from Hidden match Hidden; never Noise; end observe;
end interface Worker;
module WorkerModule() return Worker is initial Hidden(); Noise(); end module WorkerModule;
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
	if len(result.ModuleConstraints) != 1 || !result.ModuleConstraints[0].Report.Passed ||
		len(result.ModuleConstraints[0].Report.Reports) != 1 {
		t.Fatalf("interface alphabet reports=%#v", result.ModuleConstraints)
	}
}

func TestConstraintLabelsAndGroupedBodyLabelsAreCanonicalAuditIdentities(t *testing.T) {
	source := []byte(`
type Worker is interface
  action out Start(); action out Noise(); action out Done();
end interface Worker;
architecture System() is
  worker : Worker;
constraint
  Protocol: observe from worker.Start, worker.Noise, worker.Done
    RequiredFlow: match (worker.Start -> worker.Done);
    NoNoise: never worker.Noise;
  end observe;
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
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 10,
		arch.InputEvent{Key: "start", Source: "worker", Action: "Start"},
		arch.InputEvent{Key: "noise", Source: "worker", Action: "Noise", Causes: []string{"start"}},
		arch.InputEvent{Key: "done", Source: "worker", Action: "Done", Causes: []string{"noise"}},
	))
	if err != nil {
		t.Fatal(err)
	}
	if result.Constraints == nil || result.Constraints.Passed || len(result.Constraints.Reports) != 1 {
		t.Fatalf("grouped labeled report=%#v", result.Constraints)
	}
	report := result.Constraints.Reports[0]
	if len(report.Violations) != 1 {
		t.Fatalf("grouped labeled violations=%#v", report.Violations)
	}
	violation := report.Violations[0]
	if !strings.Contains(violation.Constraint, "label:protocol") || violation.Clause != "label:nonoise" {
		t.Fatalf("labeled violation identity=%#v", violation)
	}
	encoded, err := result.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	artifactDigest, err := result.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := model.ReplayDeterministic(
		arch.NewExecutionJournal(digest, 10,
			arch.InputEvent{Key: "start", Source: "worker", Action: "Start"},
			arch.InputEvent{Key: "noise", Source: "worker", Action: "Noise", Causes: []string{"start"}},
			arch.InputEvent{Key: "done", Source: "worker", Action: "Done", Causes: []string{"noise"}},
		), artifactDigest,
	)
	if err != nil {
		t.Fatal(err)
	}
	replayBytes, err := replayed.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, replayBytes) {
		t.Fatal("labeled grouped constraint replay was not byte-identical")
	}
}

func TestSimpleConstraintLabelIsTheConstraintIdentity(t *testing.T) {
	source := []byte(`
type Worker is interface action out Error(); end interface Worker;
architecture System() is worker : Worker;
constraint FailurePolicy: never worker.Error;
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
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 10,
		arch.InputEvent{Key: "error", Source: "worker", Action: "Error"},
	))
	if err != nil {
		t.Fatal(err)
	}
	violation := result.Constraints.Reports[0].Violations[0]
	if !strings.Contains(violation.Constraint, "label:failurepolicy") || violation.Clause != "source" {
		t.Fatalf("simple labeled violation identity=%#v", violation)
	}
}

func TestInterfaceAndModuleConstraintLabelsRemainSeparateLocalIdentities(t *testing.T) {
	source := []byte(`
type Worker is interface
  private action Hidden(); action out Error();
  constraint InterfacePolicy: observe from Hidden, Error
    InterfaceNoError: never Error;
  end;
end interface Worker;
module WorkerModule() return Worker is
constraint ModulePolicy: observe from Hidden, Error
  ModuleNoError: never Error;
end observe;
initial Hidden(); Error();
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
		t.Fatalf("labeled local reports=%#v", result.ModuleConstraints)
	}
	want := map[string]string{
		"label:interfacepolicy": "label:interfacenoerror",
		"label:modulepolicy":    "label:modulenoerror",
	}
	for _, report := range result.ModuleConstraints[0].Report.Reports {
		if len(report.Violations) != 1 {
			t.Fatalf("local labeled violations=%#v", report.Violations)
		}
		violation := report.Violations[0]
		matched := false
		for constraintIdentity, clauseIdentity := range want {
			if strings.Contains(violation.Constraint, constraintIdentity) {
				matched = true
				if violation.Clause != clauseIdentity {
					t.Fatalf("local labeled clause=%q, want %q", violation.Clause, clauseIdentity)
				}
				delete(want, constraintIdentity)
				break
			}
		}
		if !matched {
			t.Fatalf("unexpected local labeled constraint=%q", violation.Constraint)
		}
	}
	if len(want) != 0 {
		t.Fatalf("missing local label identities=%v", want)
	}
}

func TestConstraintAndBodyLabelsMayNotBeOverloaded(t *testing.T) {
	tests := []struct {
		name, declarations, want string
	}{
		{
			name: "constraint label",
			declarations: `Policy: never worker.Start;
policy: never worker.Done;`,
			want: "duplicate architecture constraint architecture:label:policy",
		},
		{
			name: "body label",
			declarations: `Policy: observe from worker.Start, worker.Done
  Required: match worker.Start;
  required: never worker.Done;
end;`,
			want: `duplicate architecture constraint body label "required"`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := []byte(`
type Worker is interface action out Start(); action out Done(); end interface Worker;
architecture System() is worker : Worker; constraint
` + test.declarations + `
end architecture System;
`)
			_, err := Compile(source, "System")
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(test.want)) {
				t.Fatalf("label overload error=%v, want %q", err, test.want)
			}
		})
	}
}

func TestGroupedConstraintComponentOrderIsNotSemantic(t *testing.T) {
	compile := func(body string) string {
		t.Helper()
		source := []byte(`
type Worker is interface action out Start(); action out Done(); end interface Worker;
architecture System() is worker : Worker; constraint
Policy: observe from worker.Start, worker.Done
` + body + `
end;
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
	left := compile("Required: match worker.Start; Forbidden: never worker.Done;")
	right := compile("Forbidden: never worker.Done; Required: match worker.Start;")
	if left != right {
		t.Fatalf("grouped body order changed model digest: %s != %s", left, right)
	}
}

func TestConstraintAlphabetOrderAndDuplicatesAreNotSemantic(t *testing.T) {
	compile := func(alphabet string) string {
		t.Helper()
		source := []byte(`
type Worker is interface action out Start(); action out Done(); end interface Worker;
architecture System() is worker : Worker; constraint
  observe from ` + alphabet + ` never worker.Done; end;
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
	left := compile("worker.Start, worker.Done, worker.Start")
	right := compile("worker.Done, worker.Start")
	if left != right {
		t.Fatalf("alphabet order/duplicates changed source model identity: %s != %s", left, right)
	}
}

func TestCompileRejectsMalformedOrUnsupportedConstraintAlphabetFilters(t *testing.T) {
	tests := []struct {
		name, constraint, want string
	}{
		{name: "missing from", constraint: "observe Start match Start; end;", want: "expected 'from'"},
		{name: "compound", constraint: "observe from (Start -> Done) match Start; end;", want: "requires basic patterns"},
		{name: "placeholder", constraint: "observe from Start(?N) match Start; end;", want: "placeholders are not defined"},
		{name: "unknown", constraint: "observe from Missing match Start; end;", want: `action "Missing" is not declared`},
		{name: "missing body", constraint: "observe from Start end;", want: "filtered 'match' or 'never'"},
		{name: "missing end", constraint: "observe from Start match Start;", want: "expected ';'"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := []byte(`
type Worker is interface action out Start(); action out Done(); constraint ` + test.constraint + ` end interface Worker;
architecture System() is worker : Worker; end architecture System;
`)
			_, err := Compile(source, "System")
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(test.want)) {
				t.Fatalf("constraint %q error=%v, want %q", test.constraint, err, test.want)
			}
		})
	}
}

func TestArchitectureAlphabetFilterCannotSelectPrivateAction(t *testing.T) {
	source := []byte(`
type Worker is interface private action Hidden(); action out Public(); end interface Worker;
architecture System() is worker : Worker; constraint
  observe from worker.Hidden never worker.Public; end;
end architecture System;
`)
	_, err := Compile(source, "System")
	if err == nil || !strings.Contains(err.Error(), "cannot observe private action") {
		t.Fatalf("private architecture alphabet error=%v", err)
	}
}

func TestSourceConstraintAlphabetFiltersStableAcrossGOMAXPROCS(t *testing.T) {
	previous := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(previous)
	var baseline []byte
	for _, processors := range []int{1, 8} {
		runtime.GOMAXPROCS(processors)
		for run := 0; run < 10; run++ {
			model, err := Compile([]byte(filteredArchitectureConstraintSource), "System")
			if err != nil {
				t.Fatal(err)
			}
			digest, err := model.DeterministicModelDigest()
			if err != nil {
				t.Fatal(err)
			}
			result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 10,
				arch.InputEvent{Key: "start", Source: "worker", Action: "Start"},
				arch.InputEvent{Key: "noise", Source: "worker", Action: "Noise", Causes: []string{"start"}},
				arch.InputEvent{Key: "done", Source: "worker", Action: "Done", Causes: []string{"noise"}},
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
				t.Fatalf("constraint alphabet artifact changed at GOMAXPROCS=%d run=%d", processors, run)
			}
		}
	}
}

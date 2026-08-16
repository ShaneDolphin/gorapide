package arch

import (
	"errors"
	"testing"

	"github.com/ShaneDolphin/gorapide/constraint"
	"github.com/ShaneDolphin/gorapide/pattern"
)

func stateGuardedModuleArchitecture(t *testing.T, assignBefore bool) *Architecture {
	t.Helper()
	architecture := NewArchitecture("module-state-constraint")
	worker := NewComponent("worker", Interface("Worker").OutAction("Check").Build(), nil)
	if err := worker.DeclareState(StateReference("version", "Integer", 0)); err != nil {
		t.Fatal(err)
	}
	statements := []Statement{
		CallAction("check", "Check"),
		SetState("version", LiteralValue(1)),
	}
	if assignBefore {
		statements[0], statements[1] = statements[1], statements[0]
	}
	if err := worker.SetInitialStatements(statements...); err != nil {
		t.Fatal(err)
	}
	guarded := pattern.Where(
		pattern.MatchEvent("Check"),
		pattern.BinaryCondition(
			pattern.ConditionGreater,
			pattern.StateCondition("worker\x00version", "Integer"),
			pattern.LiteralCondition(int64(0)),
		),
	)
	set := constraint.NewConstraintSet("worker-state-policy")
	set.Add(constraint.NewConstraint("positive-version").
		MustNever("check", guarded, "Check observed a positive version").
		Build())
	worker.SetModuleConstraints(set)
	if err := architecture.AddComponent(worker); err != nil {
		t.Fatal(err)
	}
	return architecture
}

func TestEngineEvaluatesModuleStateConstraintAtMatchedEventCut(t *testing.T) {
	for _, test := range []struct {
		name         string
		assignBefore bool
		wantPassed   bool
		wantVersion  uint64
	}{
		{name: "assignment before match", assignBefore: true, wantPassed: false, wantVersion: 1},
		{name: "assignment after match", assignBefore: false, wantPassed: true, wantVersion: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			architecture := stateGuardedModuleArchitecture(t, test.assignBefore)
			digest, err := architecture.DeterministicModelDigest()
			if err != nil {
				t.Fatal(err)
			}
			journal := NewExecutionJournal(digest, 10)
			result, err := architecture.ExecuteDeterministic(journal)
			if err != nil {
				t.Fatal(err)
			}
			if len(result.ModuleConstraints) != 1 || result.ModuleConstraints[0].Report.Passed != test.wantPassed {
				t.Fatalf("module state constraint report=%#v", result.ModuleConstraints)
			}
			member := result.ModuleConstraints[0].Report.Reports[0]
			if len(member.StateEvaluations) != 1 || member.StateEvaluations[0].GuardResult == test.wantPassed {
				t.Fatalf("module state evaluation audit=%#v", member.StateEvaluations)
			}
			check := result.Poset.ByName("Check")
			if len(check) != 1 {
				t.Fatalf("Check events=%#v", check)
			}
			augmented, err := result.AugmentedComputation()
			if err != nil {
				t.Fatal(err)
			}
			cuts, err := augmented.ConsistentCutStateWitnesses([]string{string(check[0].ID)}, ConsistentCutLimits{
				MaxCuts: journal.Limits.MaxConsistentCuts, MaxOptionalOccurrences: journal.Limits.MaxOptionalCutOccurrences,
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(cuts) != 1 || len(cuts[0].State) != 1 || cuts[0].State[0].Version != test.wantVersion {
				t.Fatalf("Check cut state=%#v", cuts)
			}
			if !test.wantPassed {
				violations := result.ModuleConstraints[0].Report.Reports[0].Violations
				if len(violations) != 1 || len(violations[0].StateWitnesses) != 1 ||
					violations[0].StateWitnesses[0] != cuts[0].Digest {
					t.Fatalf("constraint/cut witness linkage=%#v cuts=%#v", violations, cuts)
				}
			}
		})
	}
}

func TestExecutionJournalCarriesExplicitConsistentCutBounds(t *testing.T) {
	journal := NewExecutionJournalWithLimits("model", ExecutionLimits{
		MaxFirings: 3, MaxStatements: 7, MaxConsistentCuts: 11, MaxOptionalCutOccurrences: 13,
	})
	encoded, err := journal.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseExecutionJournal(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Limits != journal.Limits {
		t.Fatalf("cut limits round trip=%#v, want %#v", parsed.Limits, journal.Limits)
	}
	compatibility := NewExecutionJournalWithLimits("model", ExecutionLimits{MaxFirings: 3, MaxStatements: 7})
	if compatibility.Limits.MaxConsistentCuts == 0 || compatibility.Limits.MaxOptionalCutOccurrences == 0 {
		t.Fatalf("constructor did not materialize cut defaults: %#v", compatibility.Limits)
	}
}

func ambiguousStateConstraintArchitecture(t *testing.T) *Architecture {
	t.Helper()
	architecture := NewArchitecture("ambiguous-state-constraint")
	worker := NewComponent("worker", Interface("Worker").OutAction("Left").OutAction("Right").Build(), nil)
	if err := worker.DeclareState(StateReference("version", "Integer", 0)); err != nil {
		t.Fatal(err)
	}
	process := Process("update-after-left").StartAt("wait").States(
		AwaitState("wait", Await("update").On(pattern.MatchEvent("Left")).
			Do(SetState("version", LiteralValue(1))).Terminate().Build()),
	).Build()
	if err := worker.AddDeclarativeProcess(process); err != nil {
		t.Fatal(err)
	}
	guarded := pattern.Where(
		pattern.Independent(pattern.MatchEvent("Left"), pattern.MatchEvent("Right")),
		pattern.BinaryCondition(
			pattern.ConditionGreater,
			pattern.StateCondition("worker\x00version", "Integer"),
			pattern.LiteralCondition(int64(0)),
		),
	)
	set := constraint.NewConstraintSet("ambiguous-state-policy")
	set.Add(constraint.NewConstraint("positive-version").
		MustNever("left-right", guarded, "some match cut has a positive version").Build())
	worker.SetModuleConstraints(set)
	if err := architecture.AddComponent(worker); err != nil {
		t.Fatal(err)
	}
	return architecture
}

func TestEngineEnumeratesAllMatchRelativeStatesAndUsesExistentialGuard(t *testing.T) {
	architecture := ambiguousStateConstraintArchitecture(t)
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := NewExecutionJournal(digest, 10,
		InputEvent{Key: "left", Source: "worker", Action: "Left"},
		InputEvent{Key: "right", Source: "worker", Action: "Right"},
	)
	result, err := architecture.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	left, right := result.Poset.ByName("Left"), result.Poset.ByName("Right")
	if len(left) != 1 || len(right) != 1 ||
		result.Poset.IsCausallyBefore(left[0].ID, right[0].ID) || result.Poset.IsCausallyBefore(right[0].ID, left[0].ID) {
		t.Fatalf("input events are not independent: left=%#v right=%#v", left, right)
	}
	augmented, err := result.AugmentedComputation()
	if err != nil {
		t.Fatal(err)
	}
	cuts, err := augmented.ConsistentCutStateWitnesses(
		[]string{string(left[0].ID), string(right[0].ID)},
		ConsistentCutLimits{MaxCuts: 10, MaxOptionalOccurrences: 10},
	)
	if err != nil {
		t.Fatal(err)
	}
	versions := make(map[uint64]string)
	for _, cut := range cuts {
		if len(cut.State) != 1 {
			t.Fatalf("ambiguous cut state=%#v", cut)
		}
		versions[cut.State[0].Version] = cut.Digest
	}
	if len(cuts) != 2 || versions[0] == "" || versions[1] == "" {
		t.Fatalf("match-relative cut versions=%#v", cuts)
	}
	violations := result.ModuleConstraints[0].Report.Reports[0].Violations
	evaluations := result.ModuleConstraints[0].Report.Reports[0].StateEvaluations
	trueEvaluations := 0
	for _, evaluation := range evaluations {
		if evaluation.GuardResult {
			trueEvaluations++
		}
	}
	if len(evaluations) != 2 || trueEvaluations != 1 {
		t.Fatalf("ambiguous state evaluation ledger=%#v", evaluations)
	}
	if len(violations) != 1 || len(violations[0].StateWitnesses) != 1 ||
		violations[0].StateWitnesses[0] != versions[1] {
		t.Fatalf("existential state guard report=%#v cuts=%#v", violations, cuts)
	}

	limited := NewExecutionJournalWithLimits(digest, ExecutionLimits{
		MaxFirings: 10, MaxStatements: 100, MaxConsistentCuts: 1, MaxOptionalCutOccurrences: 10,
	}, journal.Inputs...)
	if _, err := architecture.ExecuteDeterministic(limited); !errors.Is(err, ErrConsistentCutLimit) {
		t.Fatalf("consistent-cut budget error=%v, want ErrConsistentCutLimit", err)
	}
}

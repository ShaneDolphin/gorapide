package arch

import (
	"errors"
	"testing"

	"github.com/ShaneDolphin/gorapide/pattern"
)

func caseMembershipModel(t *testing.T, statement Statement) *Architecture {
	t.Helper()
	architecture := NewArchitecture("case-membership")
	component := NewComponent("worker", Interface("Worker").
		OutAction("Start", P("positive", "Positive"), P("integer", "Integer")).
		OutAction("Matched").Build(), nil)
	if err := architecture.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	trigger := pattern.MatchEvent("Start").
		BindParam("positive", pattern.Var("P").WithType("Positive")).
		BindParam("integer", pattern.Var("I").WithType("Integer"))
	if err := component.AddDeclarativeRule(
		Rule("choose").On(trigger).Do(statement).Build(),
	); err != nil {
		t.Fatal(err)
	}
	return architecture
}

func TestCaseValidationUsesClosedConstrainedMembership(t *testing.T) {
	statement := CaseOf(BoundValue("P"), CaseXorMode,
		CaseWhenChoices([]CaseChoice{
			CaseValueChoice(LiteralValue(1)),
			CaseRangeChoice(LiteralValue(2), LiteralValue(3)),
		}, CallAction("matched", "Matched")),
	)
	architecture := caseMembershipModel(t, statement)
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	result, err := architecture.ExecuteDeterministic(NewExecutionJournal(digest, 10, InputEvent{
		Key: "start", Source: "worker", Action: "Start",
		Params: map[string]any{"positive": 3, "integer": 0},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Poset.ByName("Matched")) != 1 {
		t.Fatal("closed Positive range did not select its case alternative")
	}
}

func TestCaseValidationRejectsChoicesOutsideCaseType(t *testing.T) {
	tests := []struct {
		name      string
		statement Statement
	}{
		{
			name: "closed zero",
			statement: CaseOf(BoundValue("P"), CaseXorMode,
				CaseWhen(LiteralValue(0), NullStatement())),
		},
		{
			name: "open integer",
			statement: CaseOf(BoundValue("P"), CaseXorMode,
				CaseWhen(BoundValue("I"), NullStatement())),
		},
		{
			name: "range contains zero",
			statement: CaseOf(BoundValue("P"), CaseXorMode,
				CaseWhenRange(LiteralValue(0), LiteralValue(2), NullStatement())),
		},
		{
			name: "open range endpoint",
			statement: CaseOf(BoundValue("P"), CaseXorMode,
				CaseWhenRange(LiteralValue(1), BoundValue("I"), NullStatement())),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := caseMembershipModel(t, test.statement).DeterministicModelDigest()
			if !errors.Is(err, ErrUnsupportedDeterministicModel) || !errors.Is(err, ErrInvalidDeclarativeStatement) {
				t.Fatalf("error=%v, want deterministic invalid-statement rejection", err)
			}
		})
	}
}

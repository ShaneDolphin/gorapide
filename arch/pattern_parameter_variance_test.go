package arch

import (
	"errors"
	"testing"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/pattern"
)

func TestEventPatternMapAcceptsEventParameterWideningIntoPlaceholder(t *testing.T) {
	domainInterface := Interface("Domain").OutAction("Observed", P("value", "Positive")).Build()
	rangeInterface := Interface("Range").OutAction("Copied", P("value", "Integer")).Build()
	mapping := NewEventPatternMap("variance").
		FromObject("domain", domainInterface).
		ToInterface(rangeInterface).
		AddRule(MappingRule("copy").
			On(pattern.MatchEvent("Observed").BindParam("value", pattern.Var("Wide").WithType("Integer"))).
			Emit("Copied", BindingParam("value", "Wide")).Build()).Build()

	domain := gorapide.NewPoset()
	event := deterministicMapDomainEvent(t, "Observed", "positive", nil, map[string]any{"value": 2})
	if err := domain.AddEvent(event); err != nil {
		t.Fatal(err)
	}
	result, err := mapping.ExecuteDeterministic(domain, MapExecutionLimits{MaxFirings: 4, MaxRangeEvents: 4})
	if err != nil {
		t.Fatal(err)
	}
	copied := result.Range.ByName("Copied")
	if len(copied) != 1 || copied[0].ParamInt("value") != 2 {
		t.Fatalf("Copied events=%#v, want one value=2", copied)
	}
}

func TestEventPatternMapRejectsEventParameterNarrowingIntoPlaceholder(t *testing.T) {
	domainInterface := Interface("Domain").OutAction("Observed", P("value", "Integer")).Build()
	rangeInterface := Interface("Range").OutAction("Matched").Build()
	mapping := NewEventPatternMap("invalid-variance").
		FromObject("domain", domainInterface).
		ToInterface(rangeInterface).
		AddRule(MappingRule("match").
			On(pattern.MatchEvent("Observed").BindParam("value", pattern.Var("Narrow").WithType("Positive"))).
			Emit("Matched").Build()).Build()
	if _, err := mapping.DeterministicModelDigest(); !errors.Is(err, ErrInvalidEventPatternMap) {
		t.Fatalf("error=%v, want ErrInvalidEventPatternMap", err)
	}
}

package arch

import (
	"testing"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/pattern"
)

func moduleSourceScopeFixture(t *testing.T) (
	*gorapide.Poset,
	gorapide.EventSet,
	map[string]uint64,
	*functionExecutionRuntime,
	pattern.Pattern,
	*gorapide.Event,
	*gorapide.Event,
	*gorapide.Event,
) {
	t.Helper()
	poset := gorapide.NewPoset()
	add := func(source, action, occurrence string) *gorapide.Event {
		event, err := gorapide.NewDeterministicEvent(gorapide.EventProvenance{
			Profile: "stanford-rapide-1.0", Model: "module-source-scope",
			Instance: source, Action: action, Occurrence: occurrence,
		}, map[string]any{"step": 7})
		if err != nil {
			t.Fatal(err)
		}
		if err := poset.AddEvent(event); err != nil {
			t.Fatal(err)
		}
		return event
	}
	ownerLocal := add("owner", "Local", "owner-local")
	peerLocal := add("peer", "Local", "peer-local")
	peerClosing := add("peer", "Closing", "peer-closing")
	peerModule, err := gorapide.NewRapideModuleValue(gorapide.ModuleAllocationProvenance{
		Profile: "stanford-rapide-1.0", Model: "module-source-scope", Parent: "root",
		Generator: "PeerModule", Occurrence: "peer",
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime := &functionExecutionRuntime{
		components: map[string]*Component{
			"owner": NewComponent("owner", Interface("Owner").
				OutAction("Local", P("step", "Integer")).
				OutAction("Heard", P("step", "Integer")).Build(), nil),
			"peer": NewComponent("peer", Interface("Peer").
				OutAction("Local", P("step", "Integer")).
				OutAction("Closing", P("step", "Integer")).Build(), nil),
		},
		modules: map[string]gorapide.RapideModuleValue{"peer": peerModule},
	}
	number := pattern.Var("N").WithType("Integer")
	trigger := pattern.Union(
		pattern.MatchEvent("Closing").
			BindModuleSource(pattern.Var("Peer").WithType("Peer")).
			BindParam("step", number),
		pattern.MatchEvent("Local").BindParam("step", number),
	)
	observed := gorapide.EventSet{peerLocal, peerClosing, ownerLocal}
	ranks := map[string]uint64{
		observationRankKey(ownerLocal):  1,
		observationRankKey(peerLocal):   2,
		observationRankKey(peerClosing): 3,
	}
	return poset, observed, ranks, runtime, trigger, ownerLocal, peerLocal, peerClosing
}

func assertOwnerScopedMatch(
	t *testing.T,
	match pattern.MatchResult,
	ownerLocal, peerLocal, peerClosing *gorapide.Event,
) {
	t.Helper()
	if len(match.Events) != 2 || !match.Events.Contains(ownerLocal.ID) ||
		!match.Events.Contains(peerClosing.ID) || match.Events.Contains(peerLocal.ID) {
		t.Fatalf("module pattern imported peer-local witness: %v", match.Events.IDs())
	}
}

func TestDeclarativeRuleScopesMixedModulePatternPerLeaf(t *testing.T) {
	poset, observed, ranks, runtime, trigger, ownerLocal, peerLocal, peerClosing := moduleSourceScopeFixture(t)
	rules := []*DeclarativeRule{
		Rule("owner/mixed").On(trigger).Agent().Emit("Heard", BindingParam("step", "N")).Build(),
	}
	candidates, err := eligibleDeclarativeRuleCandidates(
		"owner", rules, poset, observed, ranks, NewRuleConsumption(),
		make(map[string]bool), nil, nil, runtime,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 {
		t.Fatalf("owner-scoped declarative candidates=%d, want 1", len(candidates))
	}
	assertOwnerScopedMatch(t, candidates[0].match, ownerLocal, peerLocal, peerClosing)
}

func TestAwaitScopesMixedModulePatternPerLeaf(t *testing.T) {
	poset, observed, ranks, runtime, trigger, ownerLocal, peerLocal, peerClosing := moduleSourceScopeFixture(t)
	state := AwaitState("waiting", Await("mixed").On(trigger).Terminate().Build())
	candidates, err := eligibleAwaitCandidates(
		"owner", "watcher", state, poset, observed, ranks,
		NewRuleConsumption(), processConsumptionScope("owner", "watcher"),
		nil, nil, nil, runtime,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 {
		t.Fatalf("owner-scoped await candidates=%d, want 1", len(candidates))
	}
	assertOwnerScopedMatch(t, candidates[0].match, ownerLocal, peerLocal, peerClosing)
}

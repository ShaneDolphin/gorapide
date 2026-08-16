package pattern

import (
	"bytes"
	"errors"
	"runtime"
	"testing"

	"github.com/ShaneDolphin/gorapide"
)

func TestProjectionPreservesCausalityAcrossHiddenIntermediate(t *testing.T) {
	poset := gorapide.NewPoset()
	visibleStart := addBindingTestEvent(t, poset, "VisibleStart", "start", nil)
	hidden := addBindingTestEvent(t, poset, "Hidden", "hidden", nil, visibleStart.ID)
	visibleEnd := addBindingTestEvent(t, poset, "VisibleEnd", "end", nil, hidden.ID)

	projection, err := NewProjection(poset, gorapide.EventSet{visibleEnd, visibleStart})
	if err != nil {
		t.Fatal(err)
	}
	if projection.Len() != 2 || len(projection.ByName("Hidden")) != 0 {
		t.Fatalf("projection leaked hidden event: all=%v hidden=%v", projection.All().Names(), projection.ByName("Hidden"))
	}
	if !projection.IsCausallyBefore(visibleStart.ID, visibleEnd.ID) {
		t.Fatal("hidden intermediate erased transitive causality")
	}
	if projection.IsCausallyBefore(visibleStart.ID, hidden.ID) {
		t.Fatal("hidden endpoint remained queryable")
	}
	if got := len(Seq(MatchEvent("VisibleStart"), MatchEvent("VisibleEnd")).Match(projection)); got != 1 {
		t.Fatalf("follows over projection returned %d matches, want 1", got)
	}
	if got := len(ImmSeq(MatchEvent("VisibleStart"), MatchEvent("VisibleEnd")).Match(projection)); got != 1 {
		t.Fatalf("hidden event incorrectly blocked immediate follows: got %d matches", got)
	}
	ancestors := projection.CausalAncestors(visibleEnd.ID)
	if len(ancestors) != 1 || ancestors[0].ID != visibleStart.ID {
		t.Fatalf("visible ancestors = %v, want only VisibleStart", ancestors.IDs())
	}
	chain, err := projection.CausalChain(visibleStart.ID, visibleEnd.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(chain) != 2 || chain.Contains(hidden.ID) {
		t.Fatalf("projected chain leaked hidden event: %v", chain.IDs())
	}
	if roots := projection.Roots(); len(roots) != 1 || roots[0].ID != visibleStart.ID {
		t.Fatalf("roots = %v", roots.IDs())
	}
	if leaves := projection.Leaves(); len(leaves) != 1 || leaves[0].ID != visibleEnd.ID {
		t.Fatalf("leaves = %v", leaves.IDs())
	}
	topological := projection.TopologicalSort()
	if len(topological) != 2 || topological[0].ID != visibleStart.ID || topological[1].ID != visibleEnd.ID {
		t.Fatalf("topological order = %v", gorapide.EventSet(topological).IDs())
	}
}

func TestProjectionRetainsQualifiedViewsButCountsOccurrences(t *testing.T) {
	poset := gorapide.NewPoset()
	event := addBindingTestEvent(t, poset, "SourceAction", "one", map[string]any{"value": 1})
	first := event.Snapshot()
	second := event.Snapshot()
	second.Name = "TargetAction"
	second.Source = "target"

	projection, err := NewProjection(poset, gorapide.EventSet{second, first})
	if err != nil {
		t.Fatal(err)
	}
	if projection.Len() != 1 || len(projection.All()) != 1 {
		t.Fatalf("one occurrence exposed as %d events", projection.Len())
	}
	if len(projection.ByName("SourceAction")) != 1 || len(projection.ByName("TargetAction")) != 1 {
		t.Fatalf("qualified views were not retained")
	}
}

func TestProjectionReturnsDefensiveSnapshots(t *testing.T) {
	poset := gorapide.NewPoset()
	event := addBindingTestEvent(t, poset, "Visible", "one", map[string]any{
		"nested": map[string]any{"value": "original"},
	})
	projection, err := NewProjection(poset, gorapide.EventSet{event})
	if err != nil {
		t.Fatal(err)
	}
	first := projection.All()[0]
	first.Params["nested"].(map[string]any)["value"] = "mutated"
	second := projection.All()[0]
	if second.Params["nested"].(map[string]any)["value"] != "original" {
		t.Fatal("caller mutation changed projected event")
	}
}

func TestProjectionRejectsEventOutsideSource(t *testing.T) {
	poset := gorapide.NewPoset()
	event := &gorapide.Event{ID: "outside", Name: "Outside"}
	_, err := NewProjection(poset, gorapide.EventSet{event})
	if !errors.Is(err, ErrInvalidProjection) {
		t.Fatalf("expected ErrInvalidProjection, got %v", err)
	}
}

func TestProjectionMatchesIgnoreVisibilityOrderAndGOMAXPROCS(t *testing.T) {
	originalProcs := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(originalProcs)

	var expected []byte
	for run := 0; run < 100; run++ {
		runtime.GOMAXPROCS(1 + 7*(run%2))
		poset := gorapide.NewPoset()
		independent, err := gorapide.NewDeterministicEvent(gorapide.EventProvenance{
			Profile: "stanford-rapide-1.0", Model: "projection-stress", Instance: "component",
			Action: "Independent", Occurrence: "independent",
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if run%2 == 1 {
			if err := poset.AddEvent(independent); err != nil {
				t.Fatal(err)
			}
		}
		start := addBindingTestEvent(t, poset, "Start", "start", map[string]any{"subject": "alpha"})
		hidden := addBindingTestEvent(t, poset, "Hidden", "hidden", nil, start.ID)
		end := addBindingTestEvent(t, poset, "End", "end", map[string]any{"subject": "alpha"}, hidden.ID)
		if run%2 == 0 {
			if err := poset.AddEvent(independent); err != nil {
				t.Fatal(err)
			}
		}
		visible := gorapide.EventSet{start, end, independent}
		if run%2 == 1 {
			visible[0], visible[2] = visible[2], visible[0]
		}
		projection, err := NewProjection(poset, visible)
		if err != nil {
			t.Fatal(err)
		}
		expression := ImmSeq(
			MatchEvent("Start").BindParam("subject", Var("S")),
			MatchEvent("End").BindParam("subject", Var("S")),
		)
		matches, err := MatchWithBindings(expression, projection)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := MarshalCanonicalMatches(matches)
		if err != nil {
			t.Fatal(err)
		}
		if run == 0 {
			expected = encoded
		} else if !bytes.Equal(encoded, expected) {
			t.Fatalf("run %d changed projected match bytes", run)
		}
	}
}

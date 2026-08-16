package rapide

import (
	"bytes"
	"runtime"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide/arch"
)

func booleanShortCircuitSource() []byte {
	return []byte(`
type Worker is interface
  action out Observed(
    default_andthen : Boolean; default_orelse : Boolean;
    skipped_andthen : Boolean; skipped_orelse : Boolean;
    evaluated_andthen : Boolean; evaluated_orelse : Boolean
  );
end interface Worker;
module Store(DefaultAnd : Boolean; DefaultOr : Boolean) return Worker is
  left_false : var Boolean := False;
  left_true : var Boolean := True;
  rhs : var Boolean := True;
initial
  Observed(DefaultAnd, DefaultOr,
    $left_false andthen $rhs, $left_true orelse $rhs,
    $left_true andthen $rhs, $left_false orelse $rhs);
end module Store;
architecture System(
  DefaultAnd : Boolean is False andthen (1 / 0 = 0);
  DefaultOr : Boolean is True orelse (1 / 0 = 0)
) is
  worker : Worker is Store(DefaultAnd, DefaultOr);
end architecture System;
`)
}

func TestBooleanShortCircuitSourceSkipsFailuresAndStateReadsAndReplays(t *testing.T) {
	model, err := Compile(booleanShortCircuitSource(), "System")
	if err != nil {
		t.Fatal(err)
	}
	explicit, err := CompileWithArguments(booleanShortCircuitSource(), "system", map[string]any{
		"DEFAULTAND": false, "DEFAULTOR": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	modelDigest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	explicitDigest, err := explicit.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if explicitDigest != modelDigest {
		t.Fatalf("short-circuit defaults changed model identity: %s != %s", explicitDigest, modelDigest)
	}
	journal := arch.NewExecutionJournal(modelDigest, 20)

	previous := runtime.GOMAXPROCS(1)
	first, err := model.ExecuteDeterministic(journal)
	if err != nil {
		runtime.GOMAXPROCS(previous)
		t.Fatal(err)
	}
	runtime.GOMAXPROCS(8)
	second, err := explicit.ExecuteDeterministic(journal)
	runtime.GOMAXPROCS(previous)
	if err != nil {
		t.Fatal(err)
	}

	observed := first.Poset.ByName("Observed")
	if len(observed) != 1 {
		t.Fatalf("Observed events=%#v, want one initial event", observed)
	}
	for name, want := range map[string]bool{
		"default_andthen": false, "default_orelse": true,
		"skipped_andthen": false, "skipped_orelse": true,
		"evaluated_andthen": true, "evaluated_orelse": true,
	} {
		value, ok := observed[0].Param(name)
		if !ok || value != want {
			t.Fatalf("Observed %s=%#v, want %t: %#v", name, value, want, observed[0])
		}
	}
	if len(first.Firings) != 1 || first.Firings[0].Transition != "initial" {
		t.Fatalf("short-circuit initial audit=%#v", first.Firings)
	}
	readCounts := map[string]int{}
	for _, read := range first.Firings[0].StateReads {
		readCounts[read.Name]++
	}
	if readCounts["left_false"] != 2 || readCounts["left_true"] != 2 || readCounts["rhs"] != 2 || len(first.Firings[0].StateReads) != 6 {
		t.Fatalf("short-circuit state reads=%#v counts=%v, want lefts twice and RHS only on two required branches", first.Firings[0].StateReads, readCounts)
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
		t.Fatal("short-circuit execution changed under explicit defaults or GOMAXPROCS")
	}
	artifactDigest, err := first.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := explicit.ReplayDeterministic(journal, artifactDigest)
	if err != nil {
		t.Fatal(err)
	}
	replayedBytes, err := replayed.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstBytes, replayedBytes) {
		t.Fatal("short-circuit replay changed canonical artifact bytes")
	}
}

func TestBooleanShortCircuitSourceSurfacesRequiredRightFailure(t *testing.T) {
	for _, expression := range []string{
		`True andthen (1 / 0 = 0)`,
		`False orelse (1 / 0 = 0)`,
	} {
		source := []byte("architecture System(B : Boolean is " + expression + ") is end architecture System;")
		_, err := Compile(source, "System")
		if err == nil || !strings.Contains(err.Error(), "division by zero") {
			t.Fatalf("required RHS diagnostic=%v, want division by zero", err)
		}
	}
}

package rapide

import (
	"bytes"
	"math"
	"runtime"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide/arch"
)

func integerPredSuccSource() []byte {
	return []byte(`
type Driver is interface action out Set(value : Integer); end interface Driver;
type Worker is interface
  Predecessor : Integer;
  Successor : Integer;
  Natural_Predecessor : Integer;
  Positive_Predecessor : Integer;
  action in Set(value : Integer);
  action out Observed(
    predecessor : Integer; successor : Integer;
    natural_predecessor : Integer; positive_predecessor : Integer;
    state_predecessor : Integer; state_successor : Integer
  );
end interface Worker;
module Store(
  Seed : Integer is 4;
  Natural_Seed : Natural is 0;
  Positive_Seed : Positive is 1
) return Worker is
  Predecessor : Integer is Seed.Pred();
  Successor : Integer is Seed.Succ();
  Natural_Predecessor : Integer is Natural_Seed.Pred();
  Positive_Predecessor : Integer is Positive_Seed.Pred();
  current : var Integer := 5;
initial
  Observed(Seed.Pred(), Seed.Succ(), Natural_Seed.Pred(), Positive_Seed.Pred(),
    $current.Pred(), $current.Succ());
serial
  when (?V : Integer) Set(?V)
    where ?V.Pred().Succ() = ?V and ?V.Succ().Pred() = ?V do
    current := ?V;
    Observed(?V.Pred(), ?V.Succ(), Natural_Seed.Pred(), Positive_Seed.Pred(),
      $current.Pred(), $current.Succ());
  end when;
end module Store;
architecture System(
  Seed : Integer is 4;
  Natural_Seed : Natural is 0;
  Positive_Seed : Positive is 1
) is
  driver : Driver;
  worker : Worker is Store(Seed, Natural_Seed, Positive_Seed);
connect
  driver.Set => worker.Set;
end architecture System;
`)
}

func integerPredSuccCaseSource() []byte {
	source := strings.ReplaceAll(string(integerPredSuccSource()), ".Pred()", ".pReD()")
	source = strings.ReplaceAll(source, ".Succ()", ".sUcC()")
	return []byte(source)
}

func TestIntegerPredSuccExecuteAndReplayCanonically(t *testing.T) {
	model, err := Compile(integerPredSuccSource(), "System")
	if err != nil {
		t.Fatal(err)
	}
	caseModel, err := Compile(integerPredSuccCaseSource(), "system")
	if err != nil {
		t.Fatal(err)
	}
	explicit, err := CompileWithArguments(integerPredSuccSource(), "system", map[string]any{
		"SEED": int64(4), "NATURAL_SEED": int64(0), "POSITIVE_SEED": int64(1),
	})
	if err != nil {
		t.Fatal(err)
	}
	modelDigest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []*arch.Architecture{caseModel, explicit} {
		digest, err := candidate.DeterministicModelDigest()
		if err != nil {
			t.Fatal(err)
		}
		if digest != modelDigest {
			t.Fatalf("Pred/Succ spelling/default changed model identity: %s != %s", digest, modelDigest)
		}
	}
	journal := arch.NewExecutionJournal(modelDigest, 20, arch.InputEvent{
		Key: "set", Source: "driver", Action: "Set", Params: map[string]any{"value": int64(-9)},
	})

	previous := runtime.GOMAXPROCS(1)
	first, err := model.ExecuteDeterministic(journal)
	if err != nil {
		runtime.GOMAXPROCS(previous)
		t.Fatal(err)
	}
	runtime.GOMAXPROCS(8)
	second, err := caseModel.ExecuteDeterministic(journal)
	runtime.GOMAXPROCS(previous)
	if err != nil {
		t.Fatal(err)
	}

	observed := first.Poset.ByName("Observed")
	if len(observed) != 2 {
		t.Fatalf("Observed events=%#v, want initial and guarded reaction", observed)
	}
	seenPred := map[int64]bool{}
	seenSucc := map[int64]bool{}
	seenStatePred := map[int64]bool{}
	seenStateSucc := map[int64]bool{}
	for _, event := range observed {
		predecessor, _ := event.Param("predecessor")
		successor, _ := event.Param("successor")
		naturalPredecessor, _ := event.Param("natural_predecessor")
		positivePredecessor, _ := event.Param("positive_predecessor")
		statePredecessor, _ := event.Param("state_predecessor")
		stateSuccessor, _ := event.Param("state_successor")
		if naturalPredecessor != int64(-1) || positivePredecessor != int64(0) {
			t.Fatalf("inherited Natural/Positive Pred results=%#v", event)
		}
		seenPred[predecessor.(int64)] = true
		seenSucc[successor.(int64)] = true
		seenStatePred[statePredecessor.(int64)] = true
		seenStateSucc[stateSuccessor.(int64)] = true
	}
	if !seenPred[3] || !seenPred[-10] || !seenSucc[5] || !seenSucc[-8] ||
		!seenStatePred[4] || !seenStatePred[-10] || !seenStateSucc[6] || !seenStateSucc[-8] {
		t.Fatalf("Pred/Succ results=%v/%v state=%v/%v", seenPred, seenSucc, seenStatePred, seenStateSucc)
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
		t.Fatal("Pred/Succ changed under case-equivalent syntax or GOMAXPROCS")
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
		t.Fatal("Pred/Succ replay changed canonical artifact bytes")
	}
}

func TestIntegerPredSuccDiagnosticsAreExplicit(t *testing.T) {
	for _, test := range []struct {
		name       string
		expression string
		want       string
	}{
		{name: "Pred Float", expression: `1.0.Pred()`, want: "receiver has type Float, want Integer"},
		{name: "Succ Boolean", expression: `True.Succ()`, want: "receiver has type Boolean, want Integer"},
		{name: "Pred argument", expression: `1.Pred(1)`, want: "unsupported closed behavior expression call"},
		{name: "Succ argument", expression: `1.Succ(1)`, want: "unsupported closed behavior expression call"},
		{name: "global Pred", expression: `Pred()`, want: "unsupported closed behavior expression call"},
		{name: "predecessor overflow", expression: `(-9223372036854775807 - 1).Pred()`, want: "integer overflow"},
		{name: "successor overflow", expression: `9223372036854775807.Succ()`, want: "integer overflow"},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := []byte("architecture System(N : Integer is " + test.expression + ") is end architecture System;")
			_, err := Compile(source, "System")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("diagnostic=%v, want %q", err, test.want)
			}
		})
	}
}

func TestIntegerPredSuccRuntimeOverflowIsDeterministic(t *testing.T) {
	model, err := Compile(integerPredSuccSource(), "System")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []int64{math.MinInt64, math.MaxInt64} {
		journal := arch.NewExecutionJournal(digest, 20, arch.InputEvent{
			Key: "boundary", Source: "driver", Action: "Set", Params: map[string]any{"value": value},
		})
		for _, processors := range []int{1, 8} {
			previous := runtime.GOMAXPROCS(processors)
			_, err := model.ExecuteDeterministic(journal)
			runtime.GOMAXPROCS(previous)
			if err == nil || !strings.Contains(err.Error(), "integer overflow") {
				t.Fatalf("value=%d GOMAXPROCS=%d overflow error=%v", value, processors, err)
			}
		}
	}
}

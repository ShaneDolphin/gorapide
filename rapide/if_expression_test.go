package rapide

import (
	"bytes"
	"runtime"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide/arch"
)

func sourceIfExpressionModel() []byte {
	return []byte(`
type Driver is interface action out Set(flag : Boolean); end interface Driver;
type Worker is interface
  action in Set(flag : Boolean);
  action out Observed(
    safe_true : Integer; safe_false : Integer; numeric_supertype : Natural;
    selected_text : String; selected_state : Integer
  );
end interface Worker;
module Store(N : Natural is 2; P : Positive is 3) return Worker is
  choose_then : var Boolean := True;
  then_value : var Integer := 7;
  else_value : var Integer := 9;
initial
  Observed(
    if True then 7 else 1 / 0 end if,
    if False then 1 / 0 else 9 end if,
    if True then P else N end if,
    if False then "unused" else "selected" end if,
    if $choose_then then $then_value else $else_value end if
  );
serial
  when (?Flag : Boolean) Set(?Flag)
    where if ?Flag then True else False end if do
    choose_then := False;
    Observed(7, 9, P,
      if ?Flag then "selected" else "unused" end if,
      if $choose_then then $then_value else $else_value end if);
  end when;
end module Store;
architecture System(N : Natural is 2; P : Positive is 3) is
  driver : Driver;
  worker : Worker is Store(N, P);
connect
  driver.Set => worker.Set;
end architecture System;
`)
}

func TestSourceIfExpressionTypesSkipsBranchesAuditsReadsAndReplays(t *testing.T) {
	model, err := Compile(sourceIfExpressionModel(), "System")
	if err != nil {
		t.Fatal(err)
	}
	explicit, err := CompileWithArguments(sourceIfExpressionModel(), "system", map[string]any{
		"N": int64(2), "P": int64(3),
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
		t.Fatalf("if-expression defaults changed model identity: %s != %s", explicitDigest, modelDigest)
	}
	journal := arch.NewExecutionJournal(modelDigest, 20, arch.InputEvent{
		Key: "set", Source: "driver", Action: "Set", Params: map[string]any{"flag": true},
	})

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
	if len(observed) != 2 {
		t.Fatalf("Observed events=%#v, want initial and guarded reaction", observed)
	}
	seenState := map[int64]bool{}
	for eventIndex, event := range observed {
		for name, want := range map[string]any{
			"safe_true": int64(7), "safe_false": int64(9), "numeric_supertype": int64(3),
			"selected_text": "selected",
		} {
			value, ok := event.Param(name)
			if !ok || value != want {
				t.Fatalf("Observed[%d] %s=%#v, want %#v: %#v", eventIndex, name, value, want, event)
			}
		}
		stateValue, ok := event.Param("selected_state")
		if !ok {
			t.Fatalf("Observed[%d] has no selected_state", eventIndex)
		}
		seenState[stateValue.(int64)] = true
	}
	if !seenState[7] || !seenState[9] {
		t.Fatalf("selected_state outputs=%v, want initial 7 and reaction 9", seenState)
	}
	readCounts := map[string]int{}
	for _, firing := range first.Firings {
		for _, read := range firing.StateReads {
			readCounts[read.Name]++
		}
	}
	if readCounts["choose_then"] != 2 || readCounts["then_value"] != 1 || readCounts["else_value"] != 1 {
		t.Fatalf("if-expression state-read counts=%v, want condition twice and one selected branch each", readCounts)
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
		t.Fatal("if-expression execution changed under explicit defaults or GOMAXPROCS")
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
		t.Fatal("if-expression replay changed canonical artifact bytes")
	}
}

func TestSourceIfExpressionDiagnosticsAndSelectedFailuresAreExplicit(t *testing.T) {
	for _, test := range []struct {
		name       string
		expression string
		want       string
	}{
		{name: "condition type", expression: `if 1 then True else False end if`, want: "condition has type Integer, want Boolean"},
		{name: "branch types", expression: `if True then True else 1 end if`, want: "branches have incompatible types Boolean and Integer"},
		{name: "selected then failure", expression: `if True then (1 / 0 = 0) else False end if`, want: "division by zero"},
		{name: "selected else failure", expression: `if False then True else (1 / 0 = 0) end if`, want: "division by zero"},
		{name: "missing else", expression: `if True then 1 end if`, want: "expected 'else'"},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := []byte("architecture System(Value : Boolean is " + test.expression + ") is end architecture System;")
			_, err := Compile(source, "System")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("if-expression diagnostic=%v, want %q", err, test.want)
			}
		})
	}
}

package rapide

import (
	"bytes"
	"runtime"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide/arch"
)

func booleanNegativeLogicalSource() []byte {
	return []byte(`
type Driver is interface action out Set(left : Boolean; right : Boolean); end interface Driver;
type Worker is interface
  action in Set(left : Boolean; right : Boolean);
  action out Observed(
    nand_value : Boolean; nor_value : Boolean; shared_precedence : Boolean;
    mixed_precedence : Boolean; state_value : Boolean
  );
end interface Worker;
module Store(Seed : Boolean is True; Other : Boolean is False) return Worker is
  current : var Boolean := True;
initial
  Observed(Seed nand True, Other nor False, True or False and False,
    True xor True nor False, $current nor False);
serial
  when (?L : Boolean; ?R : Boolean) Set(?L, ?R)
    where (?L nand ?R) and not (?L nor ?R) do
    current := ?L nor ?R;
    Observed(?L nand ?R, ?L nor ?R, True or False and False,
      True xor True nor False, $current nor False);
  end when;
end module Store;
architecture System(Seed : Boolean is True; Other : Boolean is False) is
  driver : Driver;
  worker : Worker is Store(Seed, Other);
connect
  driver.Set => worker.Set;
end architecture System;
`)
}

func booleanNegativeLogicalParenthesizedSource() []byte {
	source := string(booleanNegativeLogicalSource())
	source = strings.ReplaceAll(source, "True or False and False", "(True or False) and False")
	source = strings.ReplaceAll(source, "True xor True nor False", "(True xor True) nor False")
	return []byte(source)
}

func TestBooleanNegativeLogicalOperatorsUsePublishedPrecedenceAndReplay(t *testing.T) {
	unparenthesized, err := Compile(booleanNegativeLogicalSource(), "System")
	if err != nil {
		t.Fatal(err)
	}
	parenthesized, err := Compile(booleanNegativeLogicalParenthesizedSource(), "system")
	if err != nil {
		t.Fatal(err)
	}
	explicit, err := CompileWithArguments(booleanNegativeLogicalSource(), "system", map[string]any{
		"SEED": true, "OTHER": false,
	})
	if err != nil {
		t.Fatal(err)
	}
	modelDigest, err := unparenthesized.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []*arch.Architecture{parenthesized, explicit} {
		digest, err := candidate.DeterministicModelDigest()
		if err != nil {
			t.Fatal(err)
		}
		if digest != modelDigest {
			t.Fatalf("logical grouping/default changed model identity: %s != %s", digest, modelDigest)
		}
	}
	journal := arch.NewExecutionJournal(modelDigest, 20, arch.InputEvent{
		Key: "set", Source: "driver", Action: "Set", Params: map[string]any{"left": false, "right": true},
	})

	previous := runtime.GOMAXPROCS(1)
	first, err := unparenthesized.ExecuteDeterministic(journal)
	if err != nil {
		runtime.GOMAXPROCS(previous)
		t.Fatal(err)
	}
	runtime.GOMAXPROCS(8)
	second, err := parenthesized.ExecuteDeterministic(journal)
	runtime.GOMAXPROCS(previous)
	if err != nil {
		t.Fatal(err)
	}

	observed := first.Poset.ByName("Observed")
	if len(observed) != 2 {
		t.Fatalf("Observed events=%#v, want initial and guarded reaction", observed)
	}
	seenNand := map[bool]bool{}
	for eventIndex, event := range observed {
		nandValue, ok := event.Param("nand_value")
		if !ok {
			t.Fatalf("Observed[%d] has no nand_value: %#v", eventIndex, event)
		}
		nand := nandValue.(bool)
		seenNand[nand] = true
		want := map[string]bool{
			"nand_value": nand, "nor_value": !nand, "shared_precedence": false,
			"mixed_precedence": true, "state_value": nand,
		}
		for name, expected := range want {
			value, ok := event.Param(name)
			if !ok || value != expected {
				t.Fatalf("Observed[%d] %s=%#v, want %t: %#v", eventIndex, name, value, expected, event)
			}
		}
	}
	if !seenNand[false] || !seenNand[true] {
		t.Fatalf("nand outputs=%v, want one false initial and one true reaction", seenNand)
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
		t.Fatal("negative logical operators changed under equivalent grouping or GOMAXPROCS")
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
		t.Fatal("negative logical replay changed canonical artifact bytes")
	}
}

func TestBooleanLogicalOperatorDiagnosticsAreExplicit(t *testing.T) {
	for _, test := range []struct {
		name       string
		expression string
		want       string
	}{
		{name: "nand right Integer", expression: `True nand 1`, want: `operator "nand" is not defined for Boolean and Integer`},
		{name: "nor left Integer", expression: `1 nor False`, want: `operator "nor" is not defined for Integer and Boolean`},
		{name: "andthen right Integer", expression: `True andthen 1`, want: `operator "andthen" is not defined for Boolean and Integer`},
		{name: "orelse left Integer", expression: `1 orelse False`, want: `operator "orelse" is not defined for Integer and Boolean`},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := []byte("architecture System(B : Boolean is " + test.expression + ") is end architecture System;")
			_, err := Compile(source, "System")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("diagnostic=%v, want %q", err, test.want)
			}
		})
	}
}

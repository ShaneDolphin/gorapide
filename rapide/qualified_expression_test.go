package rapide

import (
	"bytes"
	"runtime"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/arch"
)

func qualifiedExpressionSource(alternateSpelling bool) []byte {
	booleanQualifier := "Boolean'(True)"
	integerQualifier := "Integer'(P)"
	if alternateSpelling {
		booleanQualifier = "boolean ' (True)"
		integerQualifier = "INTEGER ' (P)"
	}
	return []byte(`
type Driver is interface action out Set(flag : Boolean); end interface Driver;
type Worker is interface
  action in Set(flag : Boolean);
  action out Observed(
    boolean_value : Boolean; integer_value : Integer; natural_value : Natural;
    positive_value : Positive; float_value : Float; character_value : Character;
    string_value : String; successor_value : Integer
  );
end interface Worker;
module Store(N : Natural is 3; P : Positive is 2) return Worker is
initial
  Observed(` + booleanQualifier + `, ` + integerQualifier + `, Natural'(P), Positive'(P),
    Float'(1.25), Character'('A'), String'("A"), Integer'(P).Succ());
serial
  when (?Flag : Boolean) Set(?Flag) where Boolean'(?Flag) do
	    Observed(Boolean'(?Flag), Integer'(P), Natural'(P), Positive'(P),
      Float'(1.25), Character'('A'), String'("A"), Integer'(P).Succ());
  end when;
end module Store;
architecture System(N : Natural is 3; P : Positive is 2) is
  driver : Driver;
  worker : Worker is Store(N, P);
connect
  driver.Set => worker.Set;
end architecture System;
`)
}

func TestSourceQualifiedScalarsExecuteCanonicallyAndReplay(t *testing.T) {
	implicit, err := Compile(qualifiedExpressionSource(false), "System")
	if err != nil {
		t.Fatal(err)
	}
	explicit, err := CompileWithArguments(qualifiedExpressionSource(true), "system", map[string]any{
		"n": int64(3), "p": int64(2),
	})
	if err != nil {
		t.Fatal(err)
	}
	implicitDigest, err := implicit.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	explicitDigest, err := explicit.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if implicitDigest != explicitDigest {
		t.Fatalf("qualified-expression case, spacing, or explicit defaults changed model identity: %s != %s", implicitDigest, explicitDigest)
	}
	journal := arch.NewExecutionJournal(implicitDigest, 20, arch.InputEvent{
		Key: "set", Source: "driver", Action: "Set", Params: map[string]any{"flag": true},
	})

	previous := runtime.GOMAXPROCS(1)
	first, err := implicit.ExecuteDeterministic(journal)
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
	for index, event := range observed {
		for name, want := range map[string]any{
			"boolean_value": true, "integer_value": int64(2), "natural_value": int64(2),
			"positive_value": int64(2), "float_value": 1.25, "string_value": "A",
			"successor_value": int64(3),
		} {
			value, ok := event.Param(name)
			if !ok || value != want {
				t.Fatalf("Observed[%d].%s=%#v, want %#v", index, name, value, want)
			}
		}
		value, ok := event.Param("character_value")
		character, typed := value.(gorapide.RapideCharacter)
		if !ok || !typed || character.Code() != 65 {
			t.Fatalf("Observed[%d].character_value=%#v, want Character(65)", index, value)
		}
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
		t.Fatal("qualified-expression execution changed with spelling, defaults, or GOMAXPROCS")
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
		t.Fatal("qualified-expression replay changed canonical artifact bytes")
	}
}

func TestSourceQualifiedExpressionBoundariesAreExplicit(t *testing.T) {
	for _, test := range []struct {
		name       string
		expression string
		want       string
	}{
		{name: "unknown target", expression: `Clock'(True)`, want: "outside the direct predefined-scalar subset"},
		{name: "cross type", expression: `Float'(1)`, want: "Integer, which is not a subtype of Float"},
		{name: "constrained literal narrowing", expression: `Natural'(1)`, want: "Integer, which is not a subtype of Natural"},
		{name: "general type expression", expression: `Ref(Integer)'(1)`, want: "general type expressions"},
		{name: "attribute", expression: `True'Image`, want: "attributes are outside"},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := []byte("architecture System(Value : Boolean is " + test.expression + ") is end architecture System;")
			_, err := Compile(source, "System")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("qualified-expression diagnostic=%v, want %q", err, test.want)
			}
		})
	}

	narrowing := []byte(`
type Target is interface action out Value(p : Positive); end interface Target;
module Store(N : Natural is 1) return Target is
initial Value(Positive'(N));
end module Store;
architecture System(N : Natural is 1) is target : Target is Store(N); end architecture System;
`)
	if _, err := Compile(narrowing, "System"); err == nil || !strings.Contains(err.Error(), "Natural, which is not a subtype of Positive") {
		t.Fatalf("qualified-expression narrowing diagnostic=%v", err)
	}

	placeholder := ExpressionDecl{Kind: ExpressionPlaceholder, Name: "N"}
	guard := ExpressionDecl{Kind: ExpressionQualified, Name: "Natural", Left: &placeholder}
	_, _, err := compilePatternCondition(guard, map[string]behaviorBinding{
		"n": {name: "N", typeName: "Natural", placeholder: true},
	}, nil, "", "test")
	if err == nil || !strings.Contains(err.Error(), "constrained source-type preservation") {
		t.Fatalf("constrained pattern qualification diagnostic=%v", err)
	}
}

package rapide

import (
	"bytes"
	"runtime"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide/arch"
)

func equalityMemberSource() []byte {
	return []byte(`
type Driver is interface action out Set(text : String); end interface Driver;
type Worker is interface
  action in Set(text : String);
  action out Observed(
    boolean_equal : Boolean; integer_equal : Boolean; float_equal : Boolean;
    character_equal : Boolean; string_equal : Boolean; string_unequal : Boolean;
    constrained_equal : Boolean; state_equal : Boolean
  );
end interface Worker;
module Store(N : Natural is 1; P : Positive is 1) return Worker is
  current : var String := "seed";
initial
  Observed(True.=(True), 1.=(1), 1.0.=(1.0), 'A'.=('A'),
    "A".=("A"), "A"./=("B"), N.=(P), $current.=("seed"));
serial
  when (?S : String) Set(?S) where ?S.=(?S) and ?S./=("blocked") do
    current := ?S;
    Observed(True.=(True), 1.=(1), 1.0.=(1.0), 'A'.=('A'),
      "A".=("A"), "A"./=("B"), N.=(P), $current.=(?S));
  end when;
end module Store;
architecture System(N : Natural is 1; P : Positive is 1) is
  driver : Driver;
  worker : Worker is Store(N, P);
connect
  driver.Set => worker.Set;
end architecture System;
`)
}

func equalityInfixSource() []byte {
	source := string(equalityMemberSource())
	for _, replacement := range []struct{ old, new string }{
		{old: "True.=(True)", new: "True = True"},
		{old: "1.=(1)", new: "1 = 1"},
		{old: "1.0.=(1.0)", new: "1.0 = 1.0"},
		{old: "'A'.=('A')", new: "'A' = 'A'"},
		{old: `"A".=("A")`, new: `"A" = "A"`},
		{old: `"A"./=("B")`, new: `"A" /= "B"`},
		{old: "N.=(P)", new: "N = P"},
		{old: `$current.=("seed")`, new: `$current = "seed"`},
		{old: "?S.=(?S)", new: "?S = ?S"},
		{old: `?S./=("blocked")`, new: `?S /= "blocked"`},
		{old: "$current.=(?S)", new: "$current = ?S"},
	} {
		source = strings.ReplaceAll(source, replacement.old, replacement.new)
	}
	return []byte(source)
}

func TestEqualityMemberSyntaxesExecuteAndReplayCanonically(t *testing.T) {
	member, err := Compile(equalityMemberSource(), "System")
	if err != nil {
		t.Fatal(err)
	}
	infix, err := Compile(equalityInfixSource(), "system")
	if err != nil {
		t.Fatal(err)
	}
	explicit, err := CompileWithArguments(equalityMemberSource(), "system", map[string]any{
		"N": int64(1), "P": int64(1),
	})
	if err != nil {
		t.Fatal(err)
	}
	modelDigest, err := member.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []*arch.Architecture{infix, explicit} {
		digest, err := candidate.DeterministicModelDigest()
		if err != nil {
			t.Fatal(err)
		}
		if digest != modelDigest {
			t.Fatalf("equality member spelling/default changed model identity: %s != %s", digest, modelDigest)
		}
	}
	journal := arch.NewExecutionJournal(modelDigest, 20, arch.InputEvent{
		Key: "set", Source: "driver", Action: "Set", Params: map[string]any{"text": "next"},
	})

	previous := runtime.GOMAXPROCS(1)
	first, err := member.ExecuteDeterministic(journal)
	if err != nil {
		runtime.GOMAXPROCS(previous)
		t.Fatal(err)
	}
	runtime.GOMAXPROCS(8)
	second, err := infix.ExecuteDeterministic(journal)
	runtime.GOMAXPROCS(previous)
	if err != nil {
		t.Fatal(err)
	}

	observed := first.Poset.ByName("Observed")
	if len(observed) != 2 {
		t.Fatalf("Observed events=%#v, want initial and guarded reaction", observed)
	}
	for _, event := range observed {
		for _, name := range []string{
			"boolean_equal", "integer_equal", "float_equal", "character_equal",
			"string_equal", "string_unequal", "constrained_equal", "state_equal",
		} {
			value, ok := event.Param(name)
			if !ok || value != true {
				t.Fatalf("Observed %s=%#v, want true: %#v", name, value, event)
			}
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
		t.Fatal("equality member calls changed under equivalent syntax or GOMAXPROCS")
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
		t.Fatal("equality member replay changed canonical artifact bytes")
	}
}

func TestEqualityMemberDiagnosticsAreExplicit(t *testing.T) {
	for _, test := range []struct {
		name       string
		expression string
		want       string
	}{
		{name: "mixed equality", expression: `True.=(1)`, want: "requires equal operand types, got Boolean and Integer"},
		{name: "mixed inequality", expression: `1./=(1.0)`, want: "requires equal operand types, got Integer and Float"},
		{name: "missing equality argument", expression: `1.=()`, want: "unsupported closed behavior expression call"},
		{name: "extra equality argument", expression: `1.=(1, 1)`, want: "unsupported closed behavior expression call"},
		{name: "missing inequality argument", expression: `1./=()`, want: "unsupported closed behavior expression call"},
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

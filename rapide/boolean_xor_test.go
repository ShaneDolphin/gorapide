package rapide

import (
	"bytes"
	"runtime"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide/arch"
)

func booleanXorSource() []byte {
	return []byte(`
type Driver is interface action out Set(left : Boolean; right : Boolean); end interface Driver;
type Worker is interface
  Combined : Boolean;
  action in Set(left : Boolean; right : Boolean);
  action out Observed(infix_value : Boolean; member_value : Boolean; state_value : Boolean);
end interface Worker;
module Store(Seed : Boolean is True; Flip : Boolean is False) return Worker is
  Combined : Boolean is Seed xor Flip;
  current : var Boolean := True;
initial
  Observed(Seed xor Flip, Seed.Xor(Flip), $current xor False);
serial
  when (?L : Boolean; ?R : Boolean) Set(?L, ?R)
    where (?L xor ?R) = ?L.Xor(?R) do
    current := ?L xor ?R;
    Observed(?L xor ?R, ?L.Xor(?R), $current xor False);
  end when;
end module Store;
architecture System(Seed : Boolean is True; Flip : Boolean is False) is
  driver : Driver;
  worker : Worker is Store(Seed, Flip);
connect
  driver.Set => worker.Set;
end architecture System;
`)
}

func booleanXorMemberSource() []byte {
	source := string(booleanXorSource())
	for _, replacement := range []struct{ old, new string }{
		{old: "Seed xor Flip", new: "Seed.Xor(Flip)"},
		{old: "$current xor False", new: "$current.Xor(False)"},
		{old: "?L xor ?R", new: "?L.Xor(?R)"},
	} {
		source = strings.ReplaceAll(source, replacement.old, replacement.new)
	}
	return []byte(source)
}

func TestBooleanXorSyntaxesExecuteAndReplayCanonically(t *testing.T) {
	infix, err := Compile(booleanXorSource(), "System")
	if err != nil {
		t.Fatal(err)
	}
	member, err := Compile(booleanXorMemberSource(), "system")
	if err != nil {
		t.Fatal(err)
	}
	explicit, err := CompileWithArguments(booleanXorSource(), "system", map[string]any{
		"SEED": true, "FLIP": false,
	})
	if err != nil {
		t.Fatal(err)
	}
	modelDigest, err := infix.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []*arch.Architecture{member, explicit} {
		digest, err := candidate.DeterministicModelDigest()
		if err != nil {
			t.Fatal(err)
		}
		if digest != modelDigest {
			t.Fatalf("Boolean XOR spelling/default changed model identity: %s != %s", digest, modelDigest)
		}
	}
	journal := arch.NewExecutionJournal(modelDigest, 20, arch.InputEvent{
		Key: "set", Source: "driver", Action: "Set", Params: map[string]any{"left": true, "right": true},
	})

	previous := runtime.GOMAXPROCS(1)
	first, err := infix.ExecuteDeterministic(journal)
	if err != nil {
		runtime.GOMAXPROCS(previous)
		t.Fatal(err)
	}
	runtime.GOMAXPROCS(8)
	second, err := member.ExecuteDeterministic(journal)
	runtime.GOMAXPROCS(previous)
	if err != nil {
		t.Fatal(err)
	}

	observed := first.Poset.ByName("Observed")
	if len(observed) != 2 {
		t.Fatalf("Observed events=%#v, want initial and guarded reaction", observed)
	}
	seen := map[bool]bool{}
	for _, event := range observed {
		infixValue, _ := event.Param("infix_value")
		memberValue, _ := event.Param("member_value")
		stateValue, _ := event.Param("state_value")
		if infixValue != memberValue || infixValue != stateValue {
			t.Fatalf("XOR infix/member/state mismatch: %#v", event)
		}
		seen[infixValue.(bool)] = true
	}
	if !seen[true] || !seen[false] {
		t.Fatalf("XOR outputs=%v, want true initial and false reaction", seen)
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
		t.Fatal("Boolean XOR changed under equivalent syntax or GOMAXPROCS")
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
		t.Fatal("Boolean XOR replay changed canonical artifact bytes")
	}
}

func TestBooleanXorDiagnosticsAreExplicit(t *testing.T) {
	for _, test := range []struct {
		name       string
		expression string
		want       string
	}{
		{name: "infix right Integer", expression: `True xor 1`, want: `operator "xor" is not defined for Boolean and Integer`},
		{name: "member receiver Integer", expression: `1.Xor(True)`, want: "requires Boolean and Boolean, got Integer and Boolean"},
		{name: "member argument Integer", expression: `True.Xor(1)`, want: "requires Boolean and Boolean, got Boolean and Integer"},
		{name: "member missing argument", expression: `True.Xor()`, want: "unsupported closed behavior expression call"},
		{name: "member extra argument", expression: `True.Xor(False, True)`, want: "unsupported closed behavior expression call"},
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

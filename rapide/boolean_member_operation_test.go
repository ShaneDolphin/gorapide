package rapide

import (
	"bytes"
	"runtime"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide/arch"
)

func booleanMemberOperationSource() []byte {
	return []byte(`
type Driver is interface action out Set(left : Boolean; right : Boolean); end interface Driver;
type Worker is interface
  Negated : Boolean;
  Conjoined : Boolean;
  Disjoined : Boolean;
  action in Set(left : Boolean; right : Boolean);
  action out Observed(
    prefix_not : Boolean; member_not : Boolean;
    prefix_and : Boolean; member_and : Boolean;
    prefix_or : Boolean; member_or : Boolean; state_value : Boolean
  );
end interface Worker;
module Store(Seed : Boolean is True; Flip : Boolean is False) return Worker is
  Negated : Boolean is not Seed;
  Conjoined : Boolean is Seed and Flip;
  Disjoined : Boolean is Seed or Flip;
  current : var Boolean := True;
initial
  Observed(not Seed, Seed.Not(), Seed and Flip, Seed.And(Flip),
    Seed or Flip, Seed.Or(Flip), $current and True);
serial
  when (?L : Boolean; ?R : Boolean) Set(?L, ?R)
    where (not ?L) = ?L.Not() and (?L and ?R) = ?L.And(?R)
      and (?L or ?R) = ?L.Or(?R) do
    current := ?L.And(?R);
    Observed(not ?L, ?L.Not(), ?L and ?R, ?L.And(?R),
      ?L or ?R, ?L.Or(?R), $current.And(True));
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

func booleanMemberOnlySource() []byte {
	source := string(booleanMemberOperationSource())
	for _, replacement := range []struct{ old, new string }{
		{old: "not Seed", new: "Seed.Not()"},
		{old: "Seed and Flip", new: "Seed.And(Flip)"},
		{old: "Seed or Flip", new: "Seed.Or(Flip)"},
		{old: "not ?L", new: "?L.Not()"},
		{old: "?L and ?R", new: "?L.And(?R)"},
		{old: "?L or ?R", new: "?L.Or(?R)"},
		{old: "$current and True", new: "$current.And(True)"},
	} {
		source = strings.ReplaceAll(source, replacement.old, replacement.new)
	}
	return []byte(source)
}

func TestBooleanInterfaceMemberSyntaxesExecuteAndReplayCanonically(t *testing.T) {
	infix, err := Compile(booleanMemberOperationSource(), "System")
	if err != nil {
		t.Fatal(err)
	}
	member, err := Compile(booleanMemberOnlySource(), "system")
	if err != nil {
		t.Fatal(err)
	}
	explicit, err := CompileWithArguments(booleanMemberOperationSource(), "system", map[string]any{
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
			t.Fatalf("Boolean member spelling/default changed model identity: %s != %s", digest, modelDigest)
		}
	}
	journal := arch.NewExecutionJournal(modelDigest, 20, arch.InputEvent{
		Key: "set", Source: "driver", Action: "Set", Params: map[string]any{"left": false, "right": false},
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
	seenNot := map[bool]bool{}
	seenOr := map[bool]bool{}
	seenState := map[bool]bool{}
	for _, event := range observed {
		prefixNot, _ := event.Param("prefix_not")
		memberNot, _ := event.Param("member_not")
		prefixAnd, _ := event.Param("prefix_and")
		memberAnd, _ := event.Param("member_and")
		prefixOr, _ := event.Param("prefix_or")
		memberOr, _ := event.Param("member_or")
		stateValue, _ := event.Param("state_value")
		if prefixNot != memberNot || prefixAnd != memberAnd || prefixOr != memberOr {
			t.Fatalf("Boolean prefix/infix/member mismatch: %#v", event)
		}
		if prefixAnd != false {
			t.Fatalf("Boolean And result=%#v, want false for both observations", event)
		}
		seenNot[prefixNot.(bool)] = true
		seenOr[prefixOr.(bool)] = true
		seenState[stateValue.(bool)] = true
	}
	if !seenNot[false] || !seenNot[true] || !seenOr[false] || !seenOr[true] || !seenState[false] || !seenState[true] {
		t.Fatalf("Boolean member results not=%v or=%v state=%v", seenNot, seenOr, seenState)
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
		t.Fatal("Boolean member calls changed under equivalent syntax or GOMAXPROCS")
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
		t.Fatal("Boolean member replay changed canonical artifact bytes")
	}
}

func TestBooleanInterfaceMemberDiagnosticsAreExplicit(t *testing.T) {
	for _, test := range []struct {
		name       string
		expression string
		want       string
	}{
		{name: "Not receiver", expression: `1.Not()`, want: "Boolean.Not receiver has type Integer, want Boolean"},
		{name: "Not argument", expression: `True.Not(False)`, want: "unsupported closed behavior expression call"},
		{name: "And receiver", expression: `1.And(True)`, want: "requires Boolean and Boolean, got Integer and Boolean"},
		{name: "And argument", expression: `True.And(1)`, want: "requires Boolean and Boolean, got Boolean and Integer"},
		{name: "Or missing argument", expression: `True.Or()`, want: "unsupported closed behavior expression call"},
		{name: "Or extra argument", expression: `True.Or(False, True)`, want: "unsupported closed behavior expression call"},
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

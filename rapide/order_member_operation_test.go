package rapide

import (
	"bytes"
	"runtime"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide/arch"
)

func orderMemberSource() []byte {
	return []byte(`
type Driver is interface action out Set(value : Integer); end interface Driver;
type Worker is interface
  action in Set(value : Integer);
  action out Observed(
    integer_less : Boolean; constrained_less_equal : Boolean;
    float_greater : Boolean; character_greater_equal : Boolean;
    state_less : Boolean
  );
end interface Worker;
module Store(N : Natural is 1; P : Positive is 2) return Worker is
  current : var Integer := 3;
initial
  Observed(1.<(2), N.<=(P), 2.0.>(1.0), 'B'.>=('A'), $current.<(4));
serial
  when (?V : Integer) Set(?V) where ?V.<=(?V) and ?V.>=(?V) do
    current := ?V;
    Observed(1.<(2), N.<=(P), 2.0.>(1.0), 'B'.>=('A'), $current.<(4));
  end when;
end module Store;
architecture System(N : Natural is 1; P : Positive is 2) is
  driver : Driver;
  worker : Worker is Store(N, P);
connect
  driver.Set => worker.Set;
end architecture System;
`)
}

func orderInfixSource() []byte {
	source := string(orderMemberSource())
	for _, replacement := range []struct{ old, new string }{
		{old: "1.<(2)", new: "1 < 2"},
		{old: "N.<=(P)", new: "N <= P"},
		{old: "2.0.>(1.0)", new: "2.0 > 1.0"},
		{old: "'B'.>=('A')", new: "'B' >= 'A'"},
		{old: "$current.<(4)", new: "$current < 4"},
		{old: "?V.<=(?V)", new: "?V <= ?V"},
		{old: "?V.>=(?V)", new: "?V >= ?V"},
	} {
		source = strings.ReplaceAll(source, replacement.old, replacement.new)
	}
	return []byte(source)
}

func TestOrderMemberSyntaxesExecuteAndReplayCanonically(t *testing.T) {
	member, err := Compile(orderMemberSource(), "System")
	if err != nil {
		t.Fatal(err)
	}
	infix, err := Compile(orderInfixSource(), "system")
	if err != nil {
		t.Fatal(err)
	}
	explicit, err := CompileWithArguments(orderMemberSource(), "system", map[string]any{
		"N": int64(1), "P": int64(2),
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
			t.Fatalf("order member spelling/default changed model identity: %s != %s", digest, modelDigest)
		}
	}
	journal := arch.NewExecutionJournal(modelDigest, 20, arch.InputEvent{
		Key: "set", Source: "driver", Action: "Set", Params: map[string]any{"value": int64(5)},
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
	seenStateLess := map[bool]bool{}
	for eventIndex, event := range observed {
		for _, name := range []string{
			"integer_less", "constrained_less_equal", "float_greater", "character_greater_equal",
		} {
			value, ok := event.Param(name)
			if !ok || value != true {
				t.Fatalf("Observed[%d] %s=%#v, want true: %#v", eventIndex, name, value, event)
			}
		}
		stateLess, ok := event.Param("state_less")
		if !ok {
			t.Fatalf("Observed[%d] has no state_less: %#v", eventIndex, event)
		}
		seenStateLess[stateLess.(bool)] = true
	}
	if !seenStateLess[false] || !seenStateLess[true] {
		t.Fatalf("state_less outputs=%v, want true initial and false reaction", seenStateLess)
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
		t.Fatal("order member calls changed under equivalent syntax or GOMAXPROCS")
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
		t.Fatal("order member replay changed canonical artifact bytes")
	}
}

func TestOrderMemberDiagnosticsAreExplicit(t *testing.T) {
	for _, test := range []struct {
		name       string
		expression string
		want       string
	}{
		{name: "string order gated", expression: `"A".<("B")`, want: "Order.< is not defined for String and String"},
		{name: "boolean order", expression: `True.<(False)`, want: "Order.< is not defined for Boolean and Boolean"},
		{name: "mixed numeric order", expression: `1.<(1.0)`, want: "Order.< is not defined for Integer and Float"},
		{name: "missing order argument", expression: `1.<()`, want: "unsupported closed behavior expression call"},
		{name: "extra order argument", expression: `1.<(1, 2)`, want: "unsupported closed behavior expression call"},
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

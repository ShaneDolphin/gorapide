package rapide

import (
	"bytes"
	"runtime"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide/arch"
)

func numericArithmeticMemberSource() []byte {
	return []byte(`
type Driver is interface action out Set(value : Integer); end interface Driver;
type Worker is interface
  action in Set(value : Integer);
  action out Observed(
    integer_add : Integer; integer_subtract : Integer; integer_multiply : Integer;
    float_add : Float; float_subtract : Float; float_multiply : Float;
    integer_negate : Integer; float_negate : Float; state_value : Integer
  );
end interface Worker;
module Store(N : Natural is 2; P : Positive is 3) return Worker is
  current : var Integer := 4;
initial
  Observed(1.+(2), P.-(N), N.*(P), 1.25.+(0.5), 2.0.-(0.5),
    1.5.*(2.0), 1.-(), 1.25.-(), $current.+(1));
serial
  when (?V : Integer) Set(?V)
    where ?V.+(1) = 6 and ?V.-() = -5 and ?V.*(2) = 10 do
    current := ?V.*(2);
    Observed(1.+(2), P.-(N), N.*(P), 1.25.+(0.5), 2.0.-(0.5),
      1.5.*(2.0), 1.-(), 1.25.-(), $current.-(1));
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

func numericArithmeticInfixSource() []byte {
	source := string(numericArithmeticMemberSource())
	for _, replacement := range []struct{ old, new string }{
		{old: "1.+(2)", new: "1 + 2"},
		{old: "P.-(N)", new: "P - N"},
		{old: "N.*(P)", new: "N * P"},
		{old: "1.25.+(0.5)", new: "1.25 + 0.5"},
		{old: "2.0.-(0.5)", new: "2.0 - 0.5"},
		{old: "1.5.*(2.0)", new: "1.5 * 2.0"},
		{old: "1.-()", new: "-1"},
		{old: "1.25.-()", new: "-1.25"},
		{old: "$current.+(1)", new: "$current + 1"},
		{old: "?V.+(1)", new: "?V + 1"},
		{old: "?V.-()", new: "- ?V"},
		{old: "?V.*(2)", new: "?V * 2"},
		{old: "$current.-(1)", new: "$current - 1"},
	} {
		source = strings.ReplaceAll(source, replacement.old, replacement.new)
	}
	return []byte(source)
}

func TestNumericArithmeticMemberSyntaxesExecuteAndReplayCanonically(t *testing.T) {
	member, err := Compile(numericArithmeticMemberSource(), "System")
	if err != nil {
		t.Fatal(err)
	}
	infix, err := Compile(numericArithmeticInfixSource(), "system")
	if err != nil {
		t.Fatal(err)
	}
	explicit, err := CompileWithArguments(numericArithmeticMemberSource(), "system", map[string]any{
		"N": int64(2), "P": int64(3),
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
			t.Fatalf("numeric arithmetic member spelling/default changed model identity: %s != %s", digest, modelDigest)
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
	wantCommon := map[string]any{
		"integer_add": int64(3), "integer_subtract": int64(1), "integer_multiply": int64(6),
		"float_add": float64(1.75), "float_subtract": float64(1.5), "float_multiply": float64(3),
		"integer_negate": int64(-1), "float_negate": float64(-1.25),
	}
	seenStateValue := map[int64]bool{}
	for eventIndex, event := range observed {
		for name, want := range wantCommon {
			value, ok := event.Param(name)
			if !ok || value != want {
				t.Fatalf("Observed[%d] %s=%#v, want %#v: %#v", eventIndex, name, value, want, event)
			}
		}
		stateValue, ok := event.Param("state_value")
		if !ok {
			t.Fatalf("Observed[%d] has no state_value: %#v", eventIndex, event)
		}
		seenStateValue[stateValue.(int64)] = true
	}
	if !seenStateValue[5] || !seenStateValue[9] {
		t.Fatalf("state_value outputs=%v, want 5 initial and 9 reaction", seenStateValue)
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
		t.Fatal("numeric arithmetic member calls changed under equivalent syntax or GOMAXPROCS")
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
		t.Fatal("numeric arithmetic member replay changed canonical artifact bytes")
	}
}

func TestNumericArithmeticMemberDiagnosticsAreExplicit(t *testing.T) {
	for _, test := range []struct {
		name       string
		expression string
		want       string
	}{
		{name: "unary minus wrong type", expression: `True.-()`, want: "Numeric.- receiver has type Boolean, want Integer or Float"},
		{name: "addition mixed kinds", expression: `1.+(1.0)`, want: "Numeric.+ is not defined for Integer and Float"},
		{name: "subtraction wrong type", expression: `1.-(True)`, want: "Numeric.- is not defined for Integer and Boolean"},
		{name: "multiplication wrong type", expression: `1.*(True)`, want: "Numeric.* is not defined for Integer and Boolean"},
		{name: "addition extra argument", expression: `1.+(1, 2)`, want: "unsupported closed behavior expression call"},
		{name: "subtraction extra argument", expression: `1.-(1, 2)`, want: "unsupported closed behavior expression call"},
		{name: "multiplication missing argument", expression: `1.*()`, want: "unsupported closed behavior expression call"},
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

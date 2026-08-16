package rapide

import (
	"bytes"
	"math"
	"runtime"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide/arch"
)

func numericUnaryPlusSource() []byte {
	return []byte(`
type Driver is interface action out Set(integer_value : Integer; float_value : Float); end interface Driver;
type Worker is interface
  Identity_Integer : Integer;
  Identity_Float : Float;
  action in Set(integer_value : Integer; float_value : Float);
  action out Observed(
    prefix_integer : Integer; method_integer : Integer;
    prefix_float : Float; method_float : Float;
    state_integer : Integer; state_float : Float; zero_plus : Float
  );
end interface Worker;
module Store(Int_Seed : Integer is -7; Float_Seed : Float is -1.25) return Worker is
  Identity_Integer : Integer is + Int_Seed;
  Identity_Float : Float is + Float_Seed;
  integer_state : var Integer := -3;
  float_state : var Float := -0.25;
initial
  Observed(+ Int_Seed, Int_Seed.+(), + Float_Seed, Float_Seed.+(),
    + $integer_state, $float_state.+(), + (-0.0));
serial
  when (?I : Integer; ?F : Float) Set(?I, ?F)
    where + ?I = ?I.+() and + ?F = ?F.+() do
    integer_state := ?I;
    float_state := ?F;
    Observed(+ ?I, ?I.+(), + ?F, ?F.+(),
      + $integer_state, $float_state.+(), + (-0.0));
  end when;
end module Store;
architecture System(Int_Seed : Integer is -7; Float_Seed : Float is -1.25) is
  driver : Driver;
  worker : Worker is Store(Int_Seed, Float_Seed);
connect
  driver.Set => worker.Set;
end architecture System;
`)
}

func numericUnaryPlusMemberSource() []byte {
	source := string(numericUnaryPlusSource())
	for _, replacement := range []struct{ old, new string }{
		{old: "+ Int_Seed", new: "Int_Seed.+()"},
		{old: "+ Float_Seed", new: "Float_Seed.+()"},
		{old: "+ $integer_state", new: "$integer_state.+()"},
		{old: "+ ?I", new: "?I.+()"},
		{old: "+ ?F", new: "?F.+()"},
		{old: "+ (-0.0)", new: "(-0.0).+()"},
	} {
		source = strings.ReplaceAll(source, replacement.old, replacement.new)
	}
	return []byte(source)
}

func TestNumericUnaryPlusSyntaxesExecuteAndReplayCanonically(t *testing.T) {
	prefix, err := Compile(numericUnaryPlusSource(), "System")
	if err != nil {
		t.Fatal(err)
	}
	member, err := Compile(numericUnaryPlusMemberSource(), "system")
	if err != nil {
		t.Fatal(err)
	}
	explicit, err := CompileWithArguments(numericUnaryPlusSource(), "system", map[string]any{
		"INT_SEED": int64(-7), "FLOAT_SEED": -1.25,
	})
	if err != nil {
		t.Fatal(err)
	}
	modelDigest, err := prefix.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []*arch.Architecture{member, explicit} {
		digest, err := candidate.DeterministicModelDigest()
		if err != nil {
			t.Fatal(err)
		}
		if digest != modelDigest {
			t.Fatalf("unary plus spelling/default changed model identity: %s != %s", digest, modelDigest)
		}
	}
	journal := arch.NewExecutionJournal(modelDigest, 20, arch.InputEvent{
		Key: "set", Source: "driver", Action: "Set",
		Params: map[string]any{"integer_value": int64(-9), "float_value": -2.5},
	})

	previous := runtime.GOMAXPROCS(1)
	first, err := prefix.ExecuteDeterministic(journal)
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
	seenInteger := map[int64]bool{}
	seenFloat := map[float64]bool{}
	seenStateInteger := map[int64]bool{}
	seenStateFloat := map[float64]bool{}
	for _, event := range observed {
		prefixInteger, _ := event.Param("prefix_integer")
		methodInteger, _ := event.Param("method_integer")
		prefixFloat, _ := event.Param("prefix_float")
		methodFloat, _ := event.Param("method_float")
		stateInteger, _ := event.Param("state_integer")
		stateFloat, _ := event.Param("state_float")
		zeroPlus, _ := event.Param("zero_plus")
		if prefixInteger != methodInteger || prefixFloat != methodFloat ||
			zeroPlus != float64(0) || math.Signbit(zeroPlus.(float64)) {
			t.Fatalf("Observed prefix/member/zero mismatch: %#v", event)
		}
		seenInteger[prefixInteger.(int64)] = true
		seenFloat[prefixFloat.(float64)] = true
		seenStateInteger[stateInteger.(int64)] = true
		seenStateFloat[stateFloat.(float64)] = true
	}
	if !seenInteger[-7] || !seenInteger[-9] || !seenFloat[-1.25] || !seenFloat[-2.5] ||
		!seenStateInteger[-3] || !seenStateInteger[-9] || !seenStateFloat[-0.25] || !seenStateFloat[-2.5] {
		t.Fatalf("unary plus results=%v/%v state=%v/%v", seenInteger, seenFloat, seenStateInteger, seenStateFloat)
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
		t.Fatal("unary plus changed under equivalent syntax or GOMAXPROCS")
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
		t.Fatal("unary plus replay changed canonical artifact bytes")
	}
}

func TestNumericUnaryPlusDiagnosticsAreExplicit(t *testing.T) {
	for _, test := range []struct {
		name       string
		expression string
		want       string
	}{
		{name: "prefix wrong type", expression: `+ True`, want: "requires Integer or Float, got Boolean"},
		{name: "member wrong type", expression: `True.+()`, want: "receiver has type Boolean, want Integer or Float"},
		{name: "missing operand", expression: `+`, want: "expected closed behavior expression"},
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

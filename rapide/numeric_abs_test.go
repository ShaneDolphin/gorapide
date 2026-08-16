package rapide

import (
	"bytes"
	"math"
	"runtime"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide/arch"
)

func numericAbsSource() []byte {
	return []byte(`
type Driver is interface action out Set(integer_value : Integer; float_value : Float); end interface Driver;
type Worker is interface
  Integer_Magnitude : Integer;
  Float_Magnitude : Float;
  action in Set(integer_value : Integer; float_value : Float);
  action out Observed(
    prefix_integer : Integer; method_integer : Integer;
    prefix_float : Float; method_float : Float;
    state_integer : Integer; state_float : Float; zero_abs : Float
  );
end interface Worker;
module Store(Int_Seed : Integer is -7; Float_Seed : Float is -1.25) return Worker is
  Integer_Magnitude : Integer is abs Int_Seed;
  Float_Magnitude : Float is abs Float_Seed;
  integer_state : var Integer := -3;
  float_state : var Float := -0.25;
initial
  Observed(abs Int_Seed, Int_Seed.Abs(), abs Float_Seed, Float_Seed.Abs(),
    abs $integer_state, $float_state.Abs(), (-0.0).Abs());
serial
  when (?I : Integer; ?F : Float) Set(?I, ?F)
    where abs ?I = ?I.Abs() and abs ?F = ?F.Abs() do
    integer_state := ?I;
    float_state := ?F;
    Observed(abs ?I, ?I.Abs(), abs ?F, ?F.Abs(),
      abs $integer_state, $float_state.Abs(), (-0.0).Abs());
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

func numericAbsMethodSource() []byte {
	source := string(numericAbsSource())
	for _, replacement := range []struct{ old, new string }{
		{old: "abs Int_Seed", new: "Int_Seed.Abs()"},
		{old: "abs Float_Seed", new: "Float_Seed.Abs()"},
		{old: "abs $integer_state", new: "$integer_state.Abs()"},
		{old: "abs ?I", new: "?I.Abs()"},
		{old: "abs ?F", new: "?F.Abs()"},
	} {
		source = strings.ReplaceAll(source, replacement.old, replacement.new)
	}
	return []byte(source)
}

func TestNumericAbsSyntaxesExecuteAndReplayCanonically(t *testing.T) {
	prefix, err := Compile(numericAbsSource(), "System")
	if err != nil {
		t.Fatal(err)
	}
	method, err := Compile(numericAbsMethodSource(), "system")
	if err != nil {
		t.Fatal(err)
	}
	explicit, err := CompileWithArguments(numericAbsSource(), "system", map[string]any{
		"INT_SEED": int64(-7), "FLOAT_SEED": -1.25,
	})
	if err != nil {
		t.Fatal(err)
	}
	modelDigest, err := prefix.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []*arch.Architecture{method, explicit} {
		digest, err := candidate.DeterministicModelDigest()
		if err != nil {
			t.Fatal(err)
		}
		if digest != modelDigest {
			t.Fatalf("Numeric Abs spelling/default changed model identity: %s != %s", digest, modelDigest)
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
	second, err := method.ExecuteDeterministic(journal)
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
		zeroAbs, _ := event.Param("zero_abs")
		if prefixInteger != methodInteger || prefixFloat != methodFloat ||
			zeroAbs != float64(0) || math.Signbit(zeroAbs.(float64)) {
			t.Fatalf("Observed prefix/method/zero mismatch: %#v", event)
		}
		seenInteger[prefixInteger.(int64)] = true
		seenFloat[prefixFloat.(float64)] = true
		seenStateInteger[stateInteger.(int64)] = true
		seenStateFloat[stateFloat.(float64)] = true
	}
	if !seenInteger[7] || !seenInteger[9] || !seenFloat[1.25] || !seenFloat[2.5] ||
		!seenStateInteger[3] || !seenStateInteger[9] || !seenStateFloat[0.25] || !seenStateFloat[2.5] {
		t.Fatalf("Numeric Abs results=%v/%v state=%v/%v", seenInteger, seenFloat, seenStateInteger, seenStateFloat)
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
		t.Fatal("Numeric Abs changed under equivalent syntax or GOMAXPROCS")
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
		t.Fatal("Numeric Abs replay changed canonical artifact bytes")
	}
}

func TestNumericAbsDiagnosticsAreExplicit(t *testing.T) {
	for _, test := range []struct {
		name       string
		expression string
		want       string
	}{
		{name: "prefix wrong type", expression: `abs True`, want: "requires Integer or Float, got Boolean"},
		{name: "method wrong type", expression: `True.Abs()`, want: "receiver has type Boolean, want Integer or Float"},
		{name: "method argument", expression: `1.Abs(1)`, want: "unsupported closed behavior expression call"},
		{name: "global call", expression: `Abs()`, want: "expected closed behavior expression"},
		{name: "minimum Integer", expression: `abs (-9223372036854775807 - 1)`, want: "integer overflow"},
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

func TestNumericAbsRuntimeMinIntegerFailureIsDeterministic(t *testing.T) {
	model, err := Compile(numericAbsSource(), "System")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := arch.NewExecutionJournal(digest, 20, arch.InputEvent{
		Key: "minimum", Source: "driver", Action: "Set",
		Params: map[string]any{"integer_value": int64(math.MinInt64), "float_value": -1.0},
	})
	for _, processors := range []int{1, 8} {
		previous := runtime.GOMAXPROCS(processors)
		_, err := model.ExecuteDeterministic(journal)
		runtime.GOMAXPROCS(previous)
		if err == nil || !strings.Contains(err.Error(), "integer overflow") {
			t.Fatalf("GOMAXPROCS=%d Abs overflow error=%v", processors, err)
		}
	}
}

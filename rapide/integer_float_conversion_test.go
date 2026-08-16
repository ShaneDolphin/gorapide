package rapide

import (
	"bytes"
	"math"
	"runtime"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide/arch"
)

func integerFloatSource() []byte {
	return []byte(`
type Driver is interface action out Set(value : Integer); end interface Driver;
type Worker is interface
  Converted : Float;
  Natural_Float : Float;
  Positive_Float : Float;
  action in Set(value : Integer);
  action out Observed(
    object_value : Float; natural_value : Float; positive_value : Float;
    state_value : Float; direct_value : Float
  );
end interface Worker;
module Store(
  Seed : Integer is 9007199254740993;
  Natural_Seed : Natural is 0;
  Positive_Seed : Positive is 1
) return Worker is
  Converted : Float is Seed.Float();
  Natural_Float : Float is Natural_Seed.Float();
  Positive_Float : Float is Positive_Seed.Float();
  current : var Integer := -9007199254740995;
initial
  Observed(Seed.Float(), Natural_Seed.Float(), Positive_Seed.Float(),
    $current.Float(), Seed.Pred().Float());
serial
  when (?V : Integer) Set(?V) where ?V.Float() = ?V.Float() do
    current := ?V;
    Observed(Seed.Float(), Natural_Seed.Float(), Positive_Seed.Float(),
      $current.Float(), ?V.Float());
  end when;
end module Store;
architecture System(
  Seed : Integer is 9007199254740993;
  Natural_Seed : Natural is 0;
  Positive_Seed : Positive is 1
) is
  driver : Driver;
  worker : Worker is Store(Seed, Natural_Seed, Positive_Seed);
connect
  driver.Set => worker.Set;
end architecture System;
`)
}

func integerFloatCaseSource() []byte {
	return []byte(strings.ReplaceAll(string(integerFloatSource()), ".Float()", ".fLoAt()"))
}

func TestIntegerFloatConversionExecutesAndReplaysCanonically(t *testing.T) {
	model, err := Compile(integerFloatSource(), "System")
	if err != nil {
		t.Fatal(err)
	}
	caseModel, err := Compile(integerFloatCaseSource(), "system")
	if err != nil {
		t.Fatal(err)
	}
	explicit, err := CompileWithArguments(integerFloatSource(), "system", map[string]any{
		"SEED": int64(1<<53 + 1), "NATURAL_SEED": int64(0), "POSITIVE_SEED": int64(1),
	})
	if err != nil {
		t.Fatal(err)
	}
	modelDigest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []*arch.Architecture{caseModel, explicit} {
		digest, err := candidate.DeterministicModelDigest()
		if err != nil {
			t.Fatal(err)
		}
		if digest != modelDigest {
			t.Fatalf("Integer.Float spelling/default changed model identity: %s != %s", digest, modelDigest)
		}
	}
	journal := arch.NewExecutionJournal(modelDigest, 20, arch.InputEvent{
		Key: "maximum", Source: "driver", Action: "Set", Params: map[string]any{"value": int64(math.MaxInt64)},
	})

	previous := runtime.GOMAXPROCS(1)
	first, err := model.ExecuteDeterministic(journal)
	if err != nil {
		runtime.GOMAXPROCS(previous)
		t.Fatal(err)
	}
	runtime.GOMAXPROCS(8)
	second, err := caseModel.ExecuteDeterministic(journal)
	runtime.GOMAXPROCS(previous)
	if err != nil {
		t.Fatal(err)
	}

	observed := first.Poset.ByName("Observed")
	if len(observed) != 2 {
		t.Fatalf("Observed events=%#v, want initial and guarded reaction", observed)
	}
	seenState := map[uint64]bool{}
	seenDirect := map[uint64]bool{}
	for _, event := range observed {
		objectValue, _ := event.Param("object_value")
		naturalValue, _ := event.Param("natural_value")
		positiveValue, _ := event.Param("positive_value")
		stateValue, _ := event.Param("state_value")
		directValue, _ := event.Param("direct_value")
		if math.Float64bits(objectValue.(float64)) != math.Float64bits(math.Ldexp(1, 53)) ||
			naturalValue != float64(0) || positiveValue != float64(1) {
			t.Fatalf("object/inherited Integer.Float results=%#v", event)
		}
		seenState[math.Float64bits(stateValue.(float64))] = true
		seenDirect[math.Float64bits(directValue.(float64))] = true
	}
	negativeTie := math.Float64bits(-(math.Ldexp(1, 53) + 4))
	positiveMax := math.Float64bits(math.Ldexp(1, 63))
	positiveExact := math.Float64bits(math.Ldexp(1, 53))
	if !seenState[negativeTie] || !seenState[positiveMax] || !seenDirect[positiveExact] || !seenDirect[positiveMax] {
		t.Fatalf("Integer.Float state/direct bits=%v/%v", seenState, seenDirect)
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
		t.Fatal("Integer.Float changed under case-equivalent syntax or GOMAXPROCS")
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
		t.Fatal("Integer.Float replay changed canonical artifact bytes")
	}
}

func TestIntegerFloatConversionDiagnosticsAreExplicit(t *testing.T) {
	for _, test := range []struct {
		name       string
		expression string
		want       string
	}{
		{name: "Float receiver", expression: `1.0.Float()`, want: "receiver has type Float, want Integer"},
		{name: "Boolean receiver", expression: `True.Float()`, want: "receiver has type Boolean, want Integer"},
		{name: "method argument", expression: `1.Float(1)`, want: "unsupported closed behavior expression call"},
		{name: "global call", expression: `Float()`, want: "unsupported closed behavior expression call"},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := []byte("architecture System(N : Float is " + test.expression + ") is end architecture System;")
			_, err := Compile(source, "System")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("diagnostic=%v, want %q", err, test.want)
			}
		})
	}
}

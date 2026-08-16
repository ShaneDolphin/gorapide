package rapide

import (
	"bytes"
	"math"
	"runtime"
	"testing"

	"github.com/ShaneDolphin/gorapide/arch"
)

func TestSourceFloatOperationsRoundPerNodeAndReplay(t *testing.T) {
	source := []byte(`
type Calculator is interface
  action out Compute(left : Float; right : Float);
  action out Result(
    sum : Float; difference : Float; product : Float; quotient : Float;
    negative : Float; rounded : Float; defaulted : Float;
    equal : Boolean; less : Boolean; greater_equal : Boolean
  );
  provides Quarter : function(value : Float is 1.0 / 4.0) return Float;
  behavior
    default_value : var Float := 0.0;
    Quarter : function(value : Float) return Float is
    begin
      return value;
    end function Quarter;
  begin
    (?Left, ?Right : Float) Compute(?Left, ?Right)
      where ?Left / ?Right > 1.0 =>
      default_value := Quarter();
      Result(
        ?Left + ?Right,
        ?Left - ?Right,
        ?Left * ?Right,
        ?Left / ?Right,
        -?Right,
        1.0000000074505806 * 0.9999999925494194 - 1.0,
        $default_value,
        ?Left = ?Right,
        ?Right < ?Left,
        ?Left >= ?Right
      );
    ;
  end interface Calculator;
type Sink is interface action in Forwarded(value : Float); end interface Sink;
architecture System() is calculator : Calculator; sink : Sink;
connect
  (?Left, ?Right : Float) calculator.Compute(?Left, ?Right) => sink.Forwarded(?Left / ?Right);
end architecture System;
`)
	model, err := Compile(source, "System")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := arch.NewExecutionJournal(digest, 30, arch.InputEvent{
		Key: "compute", Source: "calculator", Action: "Compute",
		Params: map[string]any{"left": 4.0, "right": 2.0},
	})

	previous := runtime.GOMAXPROCS(1)
	first, err := model.ExecuteDeterministic(journal)
	if err != nil {
		runtime.GOMAXPROCS(previous)
		t.Fatal(err)
	}
	runtime.GOMAXPROCS(8)
	second, err := model.ExecuteDeterministic(journal)
	runtime.GOMAXPROCS(previous)
	if err != nil {
		t.Fatal(err)
	}

	results := first.Poset.ByName("Result")
	if len(results) != 1 {
		t.Fatalf("Result events=%#v, want one", results)
	}
	result := results[0]
	for name, want := range map[string]float64{
		"sum": 6, "difference": 2, "product": 8, "quotient": 2,
		"negative": -2, "rounded": 0, "defaulted": 0.25,
	} {
		got, exists := result.Param(name)
		if !exists || math.Float64bits(got.(float64)) != math.Float64bits(want) {
			t.Fatalf("Result.%s=%#v, want %g", name, got, want)
		}
	}
	for name, want := range map[string]bool{"equal": false, "less": true, "greater_equal": true} {
		got, exists := result.Param(name)
		if !exists || got != want {
			t.Fatalf("Result.%s=%#v, want %t", name, got, want)
		}
	}
	call := first.Poset.ByName("Quarter'Call")
	if len(call) != 1 {
		t.Fatalf("Quarter call=%#v, want one", call)
	}
	defaulted, _ := call[0].Param("value")
	if math.Float64bits(defaulted.(float64)) != math.Float64bits(0.25) {
		t.Fatalf("closed Float default=%#v, want 0.25", defaulted)
	}
	forwarded := first.Poset.ByName("Forwarded")
	if len(forwarded) != 1 {
		t.Fatalf("Float expression connection outputs=%#v, want one", forwarded)
	}
	forwardedValue, _ := forwarded[0].Param("value")
	if math.Float64bits(forwardedValue.(float64)) != math.Float64bits(2) {
		t.Fatalf("Float expression connection output=%#v, want 2", forwardedValue)
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
		t.Fatal("GOMAXPROCS changed software-rounded Float artifact bytes")
	}
	artifactDigest, err := first.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := model.ReplayDeterministic(journal, artifactDigest)
	if err != nil {
		t.Fatal(err)
	}
	replayedBytes, err := replayed.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstBytes, replayedBytes) {
		t.Fatal("software-rounded Float replay changed canonical artifact bytes")
	}
}

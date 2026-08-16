package rapide

import (
	"bytes"
	"runtime"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide/arch"
)

func constrainedFunctionDefaultSource(positiveDefault, naturalDefault string) []byte {
	return []byte(`
type Worker is interface
  action out Result(value : Integer);
  provides Total : function(p : Positive is ` + positiveDefault + `; n : Natural is ` + naturalDefault + `) return Integer;
end interface Worker;
module Calculator() return Worker is
  total : var Integer := 0;
  Total : function(p : Positive; n : Natural) return Integer is
  begin
    return p + n;
  end function Total;
initial
  total := Total();
  Result($total);
end module Calculator;
architecture System() is
  worker : Worker is Calculator();
end architecture System;
`)
}

func TestSourceFunctionDefaultsUseClosedConstrainedMembership(t *testing.T) {
	model, err := Compile(constrainedFunctionDefaultSource("1 + 1", "1 - 1"), "System")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}

	previous := runtime.GOMAXPROCS(1)
	first, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 20))
	if err != nil {
		runtime.GOMAXPROCS(previous)
		t.Fatal(err)
	}
	runtime.GOMAXPROCS(8)
	second, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 20))
	runtime.GOMAXPROCS(previous)
	if err != nil {
		t.Fatal(err)
	}

	call := first.Poset.ByName("Total'Call")
	result := first.Poset.ByName("Result")
	if len(call) != 1 || call[0].ParamInt("p") != 2 || call[0].ParamInt("n") != 0 {
		t.Fatalf("constrained defaults were not materialized before the call: %#v", call)
	}
	if len(result) != 1 || result[0].ParamInt("value") != 2 {
		t.Fatalf("constrained default result=%#v", result)
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
		t.Fatal("GOMAXPROCS changed constrained-default artifact bytes")
	}
	artifactDigest, err := first.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := model.ReplayDeterministic(arch.NewExecutionJournal(digest, 20), artifactDigest)
	if err != nil {
		t.Fatal(err)
	}
	replayedBytes, err := replayed.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstBytes, replayedBytes) {
		t.Fatal("constrained-default replay changed canonical artifact bytes")
	}
}

func TestSourceFunctionDefaultsRejectValuesOutsideConstrainedType(t *testing.T) {
	for _, test := range []struct {
		name            string
		positiveDefault string
		naturalDefault  string
		want            string
	}{
		{name: "zero is not Positive", positiveDefault: "0", naturalDefault: "0", want: "default has type Integer, want Positive"},
		{name: "closed zero expression is not Positive", positiveDefault: "1 - 1", naturalDefault: "0", want: "default has type Integer, want Positive"},
		{name: "negative is not Natural", positiveDefault: "1", naturalDefault: "0 - 1", want: "default has type Integer, want Natural"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := Compile(constrainedFunctionDefaultSource(test.positiveDefault, test.naturalDefault), "System")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("diagnostic=%v, want %q", err, test.want)
			}
		})
	}
}

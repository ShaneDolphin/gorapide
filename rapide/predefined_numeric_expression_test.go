package rapide

import (
	"bytes"
	"runtime"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide/arch"
)

func TestSourceExpressionsUseInheritedIntegerOperations(t *testing.T) {
	source := []byte(`
type Calculator is interface
  action out Start(p : Positive; n : Natural);
  action out Values(
    sum, difference, product, quotient, negative, literal_sum : Integer;
    equal, ordered : Boolean
  );
  provides Add : function(p : Positive; n : Natural) return Integer;
  behavior
    total : var Integer := 0;
    Add : function(p : Positive; n : Natural) return Integer is
    begin
      return p + n;
    end function Add;
  begin
    (?P : Positive; ?N : Natural) Start(?P, ?N) =>
      total := Add(?P, ?N);
      Values(
        $total,
        ?N - ?P,
        ?P * ?N,
        ?N / ?P,
        -?P,
        ?P + 1,
        ?P = ?N,
        ?P < 10
      );
    ;
end interface Calculator;
architecture System() is calculator : Calculator; end architecture System;
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
		Key: "start", Source: "calculator", Action: "Start", Params: map[string]any{"p": 2, "n": 4},
	})
	previous := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previous)
	result, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	values := result.Poset.ByName("Values")
	if len(values) != 1 {
		t.Fatalf("Values events=%#v, want one", values)
	}
	for name, want := range map[string]int{
		"sum": 6, "difference": 2, "product": 8, "quotient": 2, "negative": -2, "literal_sum": 3,
	} {
		if got := values[0].ParamInt(name); got != want {
			t.Fatalf("Values.%s=%d, want %d", name, got, want)
		}
	}
	if equal, _ := values[0].Param("equal"); equal != false {
		t.Fatalf("Values.equal=%#v, want false", equal)
	}
	if ordered, _ := values[0].Param("ordered"); ordered != true {
		t.Fatalf("Values.ordered=%#v, want true", ordered)
	}
	artifact, err := result.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	runtime.GOMAXPROCS(8)
	parallelResult, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	parallelArtifact, err := parallelResult.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(artifact, parallelArtifact) {
		t.Fatalf("inherited numeric operations changed with GOMAXPROCS:\none=%s\neight=%s", artifact, parallelArtifact)
	}
	artifactDigest, err := result.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := model.ReplayDeterministic(journal, artifactDigest)
	if err != nil {
		t.Fatal(err)
	}
	replayedArtifact, err := replayed.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(artifact, replayedArtifact) {
		t.Fatalf("inherited numeric operation replay changed artifact:\nexecute=%s\nreplay=%s", artifact, replayedArtifact)
	}
}

func TestSourceExpressionsRejectIncompatibleOperationOperands(t *testing.T) {
	tests := []struct {
		name       string
		parameters string
		bindings   string
		pattern    string
		expression string
		want       string
	}{
		{
			name:       "mixed Float and Integer arithmetic",
			parameters: "f : Float; p : Positive",
			bindings:   "?F : Float; ?P : Positive",
			pattern:    "?F, ?P",
			expression: "?F + ?P",
			want:       `operator "+" is not defined for Float and Positive`,
		},
		{
			name:       "boolean numeric equality",
			parameters: "flag : Boolean; p : Positive",
			bindings:   "?Flag : Boolean; ?P : Positive",
			pattern:    "?Flag, ?P",
			expression: "?Flag = ?P",
			want:       `operator "=" is not defined for Boolean and Positive`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := `type API is interface
  action out Start(` + test.parameters + `); action out Emit(value : Integer);
  behavior begin (` + test.bindings + `) Start(` + test.pattern + `) => Emit(` + test.expression + `); ;
end interface API;
architecture System() is api : API; end architecture System;`
			_, err := Compile([]byte(source), "System")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("diagnostic=%v, want %q", err, test.want)
			}
		})
	}
}

package rapide

import (
	"bytes"
	"runtime"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide/arch"
)

func TestSourcePatternParametersAcceptSafePredefinedWidening(t *testing.T) {
	source := []byte(`
type API is interface
  action out Positive_Value(value : Positive);
  action out Natural_Value(value : Natural);
  action out Positive_As_Integer(value : Integer);
  action out Positive_As_Natural(value : Natural);
  action out Natural_As_Integer(value : Integer);
  behavior
  begin
    (?Wide : Integer) Positive_Value(?Wide) => Positive_As_Integer(?Wide); ;
    (?Middle : Natural) Positive_Value(?Middle) => Positive_As_Natural(?Middle); ;
    (?Natural_Wide : Integer) Natural_Value(?Natural_Wide) => Natural_As_Integer(?Natural_Wide); ;
end interface API;
architecture System() is api : API;
constraint Iterator_Widening: match [I : 2..2 rel ->] api.Positive_Value(I);
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
	journal := arch.NewExecutionJournal(digest, 40,
		arch.InputEvent{Key: "positive", Source: "api", Action: "Positive_Value", Params: map[string]any{"value": 2}},
		arch.InputEvent{Key: "natural", Source: "api", Action: "Natural_Value", Params: map[string]any{"value": 0}},
	)
	previous := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previous)
	result, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	if result.Constraints == nil || !result.Constraints.Passed {
		t.Fatalf("Integer iterator did not widen from Positive event parameter: %#v", result.Constraints)
	}
	checks := []struct {
		name string
		want int
	}{
		{name: "Positive_As_Integer", want: 2},
		{name: "Positive_As_Natural", want: 2},
		{name: "Natural_As_Integer", want: 0},
	}
	for _, check := range checks {
		events := result.Poset.ByName(check.name)
		if len(events) != 1 || events[0].ParamInt("value") != check.want {
			t.Fatalf("%s events=%#v, want one value=%d", check.name, events, check.want)
		}
	}
	artifact, err := result.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	runtime.GOMAXPROCS(8)
	parallel, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	parallelArtifact, err := parallel.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(artifact, parallelArtifact) {
		t.Fatalf("pattern variance changed with GOMAXPROCS:\none=%s\neight=%s", artifact, parallelArtifact)
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
		t.Fatalf("pattern variance replay changed artifact:\nexecute=%s\nreplay=%s", artifact, replayedArtifact)
	}
}

func TestSourcePatternParametersRejectUnsafePredefinedNarrowing(t *testing.T) {
	tests := []struct {
		name       string
		actionType string
		bindType   string
	}{
		{name: "integer to natural", actionType: "Integer", bindType: "Natural"},
		{name: "natural to positive", actionType: "Natural", bindType: "Positive"},
		{name: "integer to positive", actionType: "Integer", bindType: "Positive"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := `type API is interface action out Observed(value : ` + test.actionType + `); action out Matched();
  behavior begin (?N : ` + test.bindType + `) Observed(?N) => Matched(); ; end interface API;
architecture System() is api : API; end architecture System;`
			_, err := Compile([]byte(source), "System")
			want := "placeholder ?N has type " + test.bindType + " but action parameter value has type " + test.actionType
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("diagnostic=%v, want %q", err, want)
			}
		})
	}
}

package rapide

import (
	"bytes"
	"runtime"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide/arch"
)

func TestClosedSourceExpressionsSatisfyConstrainedNumericMembership(t *testing.T) {
	source := []byte(`
type API is interface
  action out Start();
  action out Positive_Value(value : Positive);
  action out Natural_Value(value : Natural);
  action out Pattern_Matched();
  provides Need_Positive : function(value : Positive);
  provides Give_Positive : function() return Positive;
  behavior
    result : var Integer := 0;
    Need_Positive : function(value : Positive) is
    begin
      null;
    end function Need_Positive;
    Give_Positive : function() return Positive is
    begin
      return 1 + 1;
    end function Give_Positive;
  begin
    Start() =>
      Need_Positive(value is 1 + 1);
      result := Give_Positive();
      Positive_Value(value is 1 + 1);
      Natural_Value(1 - 1);
    ;
    Positive_Value(2) => Pattern_Matched(); ;
end interface API;
type Sink is interface action in Receive(value : Positive); end interface Sink;
architecture System() is api : API; sink : Sink;
connect api.Positive_Value() => sink.Receive(3);
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
	journal := arch.NewExecutionJournal(digest, 40, arch.InputEvent{
		Key: "start", Source: "api", Action: "Start",
	})
	previous := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previous)
	result, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	checks := []struct {
		name  string
		param string
		want  int
	}{
		{name: "Positive_Value", param: "value", want: 2},
		{name: "Natural_Value", param: "value", want: 0},
		{name: "Receive", param: "value", want: 3},
	}
	for _, check := range checks {
		events := result.Poset.ByName(check.name)
		if len(events) != 1 || events[0].ParamInt(check.param) != check.want {
			t.Fatalf("%s events=%#v, want one %s=%d", check.name, events, check.param, check.want)
		}
	}
	if matched := result.Poset.ByName("Pattern_Matched"); len(matched) != 1 {
		t.Fatalf("closed constrained pattern did not match: %#v", matched)
	}
	if len(result.State) != 1 || result.State[0].Value.Text != "2" {
		t.Fatalf("constrained function result state=%#v", result.State)
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
		t.Fatalf("constrained membership changed with GOMAXPROCS:\none=%s\neight=%s", artifact, parallelArtifact)
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
		t.Fatalf("constrained membership replay changed artifact:\nexecute=%s\nreplay=%s", artifact, replayedArtifact)
	}
}

func TestConstrainedNumericMembershipRejectsInvalidOrOpenValues(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{
			name: "positive action zero",
			source: `type API is interface action out Start(); action out Emit(value : Positive);
  behavior begin Start() => Emit(0); ; end interface API;
architecture System() is api : API; end architecture System;`,
			want: "action \"Emit\" call association is invalid",
		},
		{
			name: "positive function actual zero",
			source: `type API is interface action out Start(); provides Need : function(value : Positive);
  behavior begin Start() => Need(1 - 1); ; end interface API;
architecture System() is api : API; end architecture System;`,
			want: "matches 0 interface signatures",
		},
		{
			name: "positive function return zero",
			source: `type API is interface provides Bad : function() return Positive;
  behavior Bad : function() return Positive is begin return 0; end function Bad;
  begin end interface API;
architecture System() is api : API; end architecture System;`,
			want: "returns Integer, want Positive",
		},
		{
			name: "positive pattern zero",
			source: `type API is interface action out Emit(value : Positive); action out Bad();
  behavior begin Emit(0) => Bad(); ; end interface API;
architecture System() is api : API; end architecture System;`,
			want: "pattern value for parameter value has type Integer but the action parameter has type Positive",
		},
		{
			name: "open integer is not narrowed",
			source: `type API is interface action out Start(value : Integer); action out Emit(value : Positive);
  behavior begin (?N : Integer) Start(?N) => Emit(?N); ; end interface API;
architecture System() is api : API; end architecture System;`,
			want: "action \"Emit\" call association is invalid",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Compile([]byte(test.source), "System")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("diagnostic=%v, want %q", err, test.want)
			}
		})
	}
}

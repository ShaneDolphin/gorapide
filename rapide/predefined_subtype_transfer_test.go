package rapide

import (
	"bytes"
	"runtime"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide/arch"
)

func predefinedSubtypeTransferSource() []byte {
	return []byte(`
type Worker is interface
  action out Start(value : Positive);
  action out Emit(value : Integer);
  action out Forward(value : Natural);
  provides Widen : function(value : Integer) return Integer;
  provides Narrow : function(value : Positive) return Natural;
  provides Return_Wide : function(value : Positive) return Integer;
  behavior
    result : var Integer := 0;
    Widen : function(value : Integer) return Integer is
    begin
      return value;
    end function Widen;
    Narrow : function(value : Positive) return Natural is
    begin
      return value;
    end function Narrow;
    Return_Wide : function(value : Positive) return Integer is
    begin
      if True then
        return value;
      end if;
      return value;
    end function Return_Wide;
  begin
    (?P : Positive) Start(?P) =>
      result := ?P;
      result := Narrow(value is ?P);
      Widen(value is ?P);
      result := Return_Wide(?P);
      Emit(value is ?P);
      Forward(?P);
    ;
end interface Worker;
type Sink is interface
  action in Receive(value : Integer);
end interface Sink;
architecture System() is worker : Worker; sink : Sink;
connect
  (?N : Natural) worker.Forward(?N) => sink.Receive(?N);
end architecture System;
`)
}

func TestSourceValueTransfersUsePredefinedSubtyping(t *testing.T) {
	model, err := Compile(predefinedSubtypeTransferSource(), "System")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := arch.NewExecutionJournal(digest, 40, arch.InputEvent{
		Key: "start", Source: "worker", Action: "Start", Params: map[string]any{"value": 3},
	})
	previous := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previous)
	result, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Emit", "Forward", "Receive"} {
		events := result.Poset.ByName(name)
		if len(events) != 1 || events[0].ParamInt("value") != 3 {
			t.Fatalf("%s events=%#v, want one value 3", name, events)
		}
	}
	for _, name := range []string{"Widen'Call", "Narrow'Return", "Return_Wide'Return"} {
		events := result.Poset.ByName(name)
		if len(events) != 1 {
			t.Fatalf("%s events=%#v, want one", name, events)
		}
	}
	if len(result.State) != 1 || result.State[0].ComponentID != "worker" || result.State[0].Value.Text != "3" {
		t.Fatalf("widened result state=%#v", result.State)
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
		t.Fatalf("predefined subtype execution changed with GOMAXPROCS:\none=%s\neight=%s", artifact, parallelArtifact)
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
		t.Fatalf("predefined subtype replay changed artifact:\nexecute=%s\nreplay=%s", artifact, replayedArtifact)
	}
}

func TestSourceValueTransfersRejectReversedPredefinedSubtyping(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{
			name: "function actual",
			source: `type API is interface
  action out Start(value : Integer);
  provides Need : function(value : Positive);
  behavior begin (?N : Integer) Start(?N) => Need(?N); ;
end interface API;
architecture System() is api : API; end architecture System;`,
			want: "matches 0 interface signatures",
		},
		{
			name: "action actual",
			source: `type API is interface
  action out Start(value : Integer); action out Narrow(value : Positive);
  behavior begin (?N : Integer) Start(?N) => Narrow(?N); ;
end interface API;
architecture System() is api : API; end architecture System;`,
			want: "action \"Narrow\" call association is invalid",
		},
		{
			name: "function return",
			source: `type API is interface
  provides Bad : function(value : Integer) return Positive;
  behavior Bad : function(value : Integer) return Positive is begin return value; end function Bad;
  begin end interface API;
architecture System() is api : API; end architecture System;`,
			want: "returns Integer, want Positive",
		},
		{
			name: "connection target",
			source: `type Source is interface action out Send(value : Integer); end interface Source;
type Target is interface action in Receive(value : Positive); end interface Target;
architecture System() is source : Source; target : Target;
connect (?N : Integer) source.Send(?N) => target.Receive(?N);
end architecture System;`,
			want: "has type Integer but parameter value has type Positive",
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

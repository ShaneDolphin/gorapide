package rapide

import (
	"bytes"
	"runtime"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide/arch"
)

func TestSourceCaseChoicesUsePredefinedCaseTypeMembership(t *testing.T) {
	source := []byte(`
type API is interface
  action out Select(positive : Positive; natural : Natural; integer : Integer);
  action out Result(value : Integer);
  behavior
  begin
    (?P : Positive; ?N : Natural; ?I : Integer) Select(?P, ?N, ?I) =>
      case ?P of
        1 + 1 => Result(11);
        xor 2 .. 3 => Result(12);
        default => Result(10);
      end case;
      case ?N of
        0 => Result(20);
        xor 1 .. 2 => Result(21);
        default => Result(22);
      end case;
      case ?I of
        ?P .. ?P => Result(30);
        default => Result(31);
      end case;
    ;
end interface API;
architecture System() is api : API; end architecture System;
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
		Key: "select", Source: "api", Action: "Select",
		Params: map[string]any{"positive": 3, "natural": 0, "integer": 3},
	})
	previous := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previous)
	result, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	values := map[int]bool{}
	for _, event := range result.Poset.ByName("Result") {
		values[event.ParamInt("value")] = true
	}
	if len(values) != 3 || !values[12] || !values[20] || !values[30] {
		t.Fatalf("case results=%v, want {12,20,30}", values)
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
		t.Fatalf("case membership changed with GOMAXPROCS:\none=%s\neight=%s", artifact, parallelArtifact)
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
		t.Fatalf("case membership replay changed artifact:\nexecute=%s\nreplay=%s", artifact, replayedArtifact)
	}
}

func TestSourceCaseChoicesRejectValuesOutsideCaseType(t *testing.T) {
	tests := []struct {
		name       string
		parameters string
		body       string
		want       string
	}{
		{
			name: "positive zero choice", parameters: "?P : Positive",
			body: "case ?P of 0 => null; end case;",
			want: "case choice has type Integer, want Positive",
		},
		{
			name: "natural negative choice", parameters: "?N : Natural",
			body: "case ?N of -1 => null; end case;",
			want: "case choice has type Integer, want Natural",
		},
		{
			name: "open integer choice", parameters: "?P : Positive; ?I : Integer",
			body: "case ?P of ?I => null; end case;",
			want: "case choice has type Integer, want Positive",
		},
		{
			name: "positive range first", parameters: "?P : Positive",
			body: "case ?P of 0 .. 2 => null; end case;",
			want: "case choice has type Integer, want Positive",
		},
		{
			name: "positive range last", parameters: "?P : Positive",
			body: "case ?P of 1 .. 0 => null; end case;",
			want: "case range endpoint has type Integer, want Positive",
		},
		{
			name: "open integer range endpoint", parameters: "?P : Positive; ?I : Integer",
			body: "case ?P of 1 .. ?I => null; end case;",
			want: "case range endpoint has type Integer, want Positive",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := `type API is interface action out Start(positive : Positive; integer : Integer);
  behavior begin (` + test.parameters + `) Start(?P, ?I) => ` + test.body + ` ; end interface API;
architecture System() is api : API; end architecture System;`
			if test.parameters == "?P : Positive" {
				source = `type API is interface action out Start(positive : Positive);
  behavior begin (` + test.parameters + `) Start(?P) => ` + test.body + ` ; end interface API;
architecture System() is api : API; end architecture System;`
			} else if test.parameters == "?N : Natural" {
				source = `type API is interface action out Start(natural : Natural);
  behavior begin (` + test.parameters + `) Start(?N) => ` + test.body + ` ; end interface API;
architecture System() is api : API; end architecture System;`
			}
			_, err := Compile([]byte(source), "System")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("diagnostic=%v, want %q", err, test.want)
			}
		})
	}
}

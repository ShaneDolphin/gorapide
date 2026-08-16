package rapide

import (
	"bytes"
	"runtime"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide/arch"
)

const privateActionSource = `
type Driver is interface action out Send(n : Integer); end interface Driver;
type Worker is interface
  action in Request(n : Integer);
  action out Published(n : Integer);
  private
    action
      Hidden(n : Integer);
      Staged(n : Integer);
end interface Worker;
type Sink is interface action in Delivered(n : Integer); end interface Sink;

module Simple() return Worker is
connect
  (?N : Integer) Hidden(?N) to Staged(?N);
  Staged to Published;
initial
  Hidden(1);
parallel
  when (?N : Integer) Request(?N) do Hidden(?N); end when;
end module Simple;

architecture System() is
  driver : Driver;
  worker : Worker is Simple();
  sink : Sink;
connect
  driver.Send to worker.Request;
  worker.Published to sink.Delivered;
end architecture System;
`

func TestParseStanfordPrivateActionFormalSeparators(t *testing.T) {
	file, err := Parse([]byte(`
type Object is interface
  private
    action
      Write(call : Integer; value : Integer; version : Integer; initial : Boolean);
end interface Object;
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(file.Interfaces) != 1 || len(file.Interfaces[0].Actions) != 1 ||
		file.Interfaces[0].Actions[0].Mode != ActionPrivate ||
		len(file.Interfaces[0].Actions[0].Parameters) != 4 {
		t.Fatalf("private action AST=%#v", file.Interfaces)
	}
}

func TestCompileStanfordPrivateActionsAcrossInitialProcessAndModuleConnections(t *testing.T) {
	model, err := Compile([]byte(privateActionSource), "System")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := arch.NewExecutionJournal(digest, 30, arch.InputEvent{
		Key: "send", Source: "driver", Action: "Send", Params: map[string]any{"n": 2},
	})
	result, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Hidden", "Staged", "Published", "Delivered"} {
		if count := len(result.Poset.ByName(name)); count != 2 {
			t.Fatalf("%s observations=%d, want startup and process occurrences", name, count)
		}
	}
	for _, value := range []int{1, 2} {
		var ids []string
		for _, name := range []string{"Hidden", "Staged", "Published", "Delivered"} {
			found := false
			for _, event := range result.Poset.ByName(name) {
				if event.ParamInt("n") == value {
					ids = append(ids, string(event.ID))
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("%s(%d) is absent", name, value)
			}
		}
		if ids[0] != ids[1] || ids[1] != ids[2] || ids[2] != ids[3] {
			t.Fatalf("basic private/public chain changed identity for n=%d: %v", value, ids)
		}
	}
	artifactDigest, err := result.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := model.ReplayDeterministic(journal, artifactDigest)
	if err != nil {
		t.Fatal(err)
	}
	left, _ := result.MarshalCanonical()
	right, _ := replayed.MarshalCanonical()
	if !bytes.Equal(left, right) {
		t.Fatal("source private-action replay was not byte-identical")
	}
}

func TestModuleProvidedFunctionCanGeneratePrivateAction(t *testing.T) {
	source := []byte(`
type Driver is interface action out Start(n : Integer); end interface Driver;
type Worker is interface
  action in Start(n : Integer);
  action out Published(n : Integer);
  action out Done(n : Integer);
  private action Hidden(n : Integer);
  provides Record : function(n : Integer);
end interface Worker;
module M() return Worker is
  Record : function(n : Integer) is
  begin
    Hidden(n);
  end function Record;
connect Hidden to Published;
parallel
  when (?N : Integer) Start(?N) do Record(?N); Done(?N); end when;
end module M;
architecture System() is
  driver : Driver;
  worker : Worker is M();
connect driver.Start to worker.Start;
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
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 20,
		arch.InputEvent{Key: "start", Source: "driver", Action: "Start", Params: map[string]any{"n": 5}},
	))
	if err != nil {
		t.Fatal(err)
	}
	call := result.Poset.ByName("Record'Call")
	hidden := result.Poset.ByName("Hidden")
	published := result.Poset.ByName("Published")
	returned := result.Poset.ByName("Record'Return")
	done := result.Poset.ByName("Done")
	if len(call) != 1 || len(hidden) != 1 || len(published) != 1 || len(returned) != 1 || len(done) != 1 {
		t.Fatalf("function/private events call=%d hidden=%d published=%d return=%d done=%d",
			len(call), len(hidden), len(published), len(returned), len(done))
	}
	if hidden[0].ID != published[0].ID ||
		!result.Poset.IsCausallyBefore(call[0].ID, hidden[0].ID) ||
		!result.Poset.IsCausallyBefore(hidden[0].ID, returned[0].ID) ||
		!result.Poset.IsCausallyBefore(returned[0].ID, done[0].ID) {
		t.Fatal("private function event identity or call/body/return causality is incorrect")
	}
}

func TestPrivateActionDeclarationOrderIsNotSemantic(t *testing.T) {
	build := func(actions string) string {
		return `
type I is interface private action ` + actions + ` end interface I;
architecture System() is worker : I; end architecture System;
`
	}
	left, err := Compile([]byte(build("A(); B();")), "System")
	if err != nil {
		t.Fatal(err)
	}
	right, err := Compile([]byte(build("B(); A();")), "System")
	if err != nil {
		t.Fatal(err)
	}
	leftDigest, err := left.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	rightDigest, err := right.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if leftDigest != rightDigest {
		t.Fatalf("private declaration order changed model identity: %s != %s", leftDigest, rightDigest)
	}
}

func TestSourceRejectsArchitecturalObservationOfPrivateActions(t *testing.T) {
	tests := []struct {
		name, source, want string
	}{
		{
			name: "architecture connection",
			source: `
type I is interface private action Hidden(); end interface I;
type Sink is interface action in Delivered(); end interface Sink;
architecture System() is worker : I; sink : Sink; connect worker.Hidden to sink.Delivered; end architecture System;
`,
			want: "is not an out action",
		},
		{
			name: "architecture constraint",
			source: `
type I is interface private action Hidden(); end interface I;
architecture System() is worker : I; constraint never worker.Hidden; end architecture System;
`,
			want: "cannot observe private action",
		},
		{
			name: "unsupported private constituent",
			source: `
type I is interface private provides F : function(); end interface I;
architecture System() is worker : I; end architecture System;
`,
			want: "expected a private name declaration or 'end'",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Compile([]byte(test.source), "System")
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(test.want)) {
				t.Fatalf("error=%v, want %q", err, test.want)
			}
		})
	}
}

func TestSourcePrivateActionsStableAcrossGOMAXPROCS(t *testing.T) {
	previous := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(previous)
	var baseline []byte
	for _, processors := range []int{1, 8} {
		runtime.GOMAXPROCS(processors)
		for iteration := 0; iteration < 20; iteration++ {
			model, err := Compile([]byte(privateActionSource), "System")
			if err != nil {
				t.Fatal(err)
			}
			digest, err := model.DeterministicModelDigest()
			if err != nil {
				t.Fatal(err)
			}
			result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 30,
				arch.InputEvent{Key: "send", Source: "driver", Action: "Send", Params: map[string]any{"n": 2}},
			))
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := result.MarshalCanonical()
			if err != nil {
				t.Fatal(err)
			}
			if baseline == nil {
				baseline = encoded
			} else if !bytes.Equal(baseline, encoded) {
				t.Fatalf("private-action artifact changed at GOMAXPROCS=%d iteration=%d", processors, iteration)
			}
		}
	}
}

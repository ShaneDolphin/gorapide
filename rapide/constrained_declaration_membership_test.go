package rapide

import (
	"bytes"
	"runtime"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide/arch"
)

func constrainedDeclarationSource() []byte {
	return []byte(`
type Driver is interface action out Set(value : Positive); end interface Driver;
type Worker is interface
  Minimum : Natural;
  Seed : Positive;
  action in Set(value : Positive);
  action out Boot(object_natural : Natural; object_positive : Positive; state_natural : Natural; state_positive : Positive);
  action out Changed(state_natural : Natural; state_positive : Positive);
end interface Worker;
module Store() return Worker is
  Minimum : Natural is 1 - 1;
  Seed : Positive is 1 + 1;
  count : var Natural := 2 - 2;
  latest : var Positive := 2 * 2;
initial
  Boot(Minimum, Seed, $count, $latest);
serial
  when (?N : Positive) Set(?N) do
    count := ?N;
    latest := ?N;
    Changed($count, $latest);
  end when;
end module Store;
architecture System() is
  driver : Driver;
  worker : Worker is Store();
connect driver.Set => worker.Set;
end architecture System;
`)
}

func TestSourceDeclarationsUseClosedConstrainedMembership(t *testing.T) {
	model, err := Compile(constrainedDeclarationSource(), "System")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := arch.NewExecutionJournal(digest, 30, arch.InputEvent{
		Key: "set", Source: "driver", Action: "Set", Params: map[string]any{"value": 3},
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

	boot := first.Poset.ByName("Boot")
	if len(boot) != 1 || boot[0].ParamInt("object_natural") != 0 ||
		boot[0].ParamInt("object_positive") != 2 || boot[0].ParamInt("state_natural") != 0 ||
		boot[0].ParamInt("state_positive") != 4 {
		t.Fatalf("constrained declaration initial values=%#v", boot)
	}
	changed := first.Poset.ByName("Changed")
	if len(changed) != 1 || changed[0].ParamInt("state_natural") != 3 ||
		changed[0].ParamInt("state_positive") != 3 {
		t.Fatalf("constrained state assignment output=%#v", changed)
	}
	if len(first.State) != 2 || first.State[0].Value.Text != "3" || first.State[1].Value.Text != "3" {
		t.Fatalf("constrained final state=%#v", first.State)
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
		t.Fatal("GOMAXPROCS changed constrained declaration artifact bytes")
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
		t.Fatal("constrained declaration replay changed canonical artifact bytes")
	}
}

func TestSourceBehaviorStateUsesClosedConstrainedInitializers(t *testing.T) {
	source := []byte(`
type API is interface
  action out Start();
  action out Snapshot(n : Natural; p : Positive);
  behavior
    natural : var Natural := 1 - 1;
    positive : var Positive := 1 + 1;
  begin
    Start() => Snapshot($natural, $positive); ;
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
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 10, arch.InputEvent{
		Key: "start", Source: "api", Action: "Start",
	}))
	if err != nil {
		t.Fatal(err)
	}
	snapshot := result.Poset.ByName("Snapshot")
	if len(snapshot) != 1 || snapshot[0].ParamInt("n") != 0 || snapshot[0].ParamInt("p") != 2 {
		t.Fatalf("constrained behavior-state initializers=%#v", snapshot)
	}
}

func TestSourceDeclarationsRejectValuesOutsideConstrainedType(t *testing.T) {
	for _, test := range []struct {
		name        string
		declaration string
		want        string
	}{
		{name: "zero Positive object", declaration: "Value : Positive is 0;", want: "initializer has type Integer, want Positive"},
		{name: "negative Natural object", declaration: "Value : Natural is 0 - 1;", want: "initializer has type Integer, want Natural"},
		{name: "zero Positive state", declaration: "value : var Positive := 0;", want: "initializer has type Integer, want Positive"},
		{name: "negative Natural state", declaration: "value : var Natural := 0 - 1;", want: "initializer has type Integer, want Natural"},
	} {
		t.Run(test.name, func(t *testing.T) {
			interfaceDeclaration := ""
			if !strings.Contains(test.declaration, "var") {
				interfaceDeclaration = "Value : "
				if strings.Contains(test.declaration, "Positive") {
					interfaceDeclaration += "Positive;"
				} else {
					interfaceDeclaration += "Natural;"
				}
			}
			source := []byte(`type API is interface ` + interfaceDeclaration + ` end interface API;
module M() return API is ` + test.declaration + ` end module M;
architecture System() is api : API is M(); end architecture System;`)
			_, err := Compile(source, "System")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("diagnostic=%v, want %q", err, test.want)
			}
		})
	}
}

package rapide

import (
	"bytes"
	"runtime"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/arch"
)

func TestSourceSelfDenotesExecutingModuleAllocation(t *testing.T) {
	source := []byte(`
type API is interface
  action out Identity(value : API);
end interface API;

module Impl() return API is
initial
  Identity(Self);
end module Impl;

architecture System() is
  api : API is Impl();
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
	journal := arch.NewExecutionJournal(digest, 20)

	previous := runtime.GOMAXPROCS(1)
	result, err := model.ExecuteDeterministic(journal)
	if err != nil {
		runtime.GOMAXPROCS(previous)
		t.Fatal(err)
	}
	runtime.GOMAXPROCS(8)
	repeated, repeatedErr := model.ExecuteDeterministic(journal)
	runtime.GOMAXPROCS(previous)
	if repeatedErr != nil {
		t.Fatal(repeatedErr)
	}

	identities := sourceNamedEvents(result.Poset, "api", "Identity")
	moduleStarts := sourceNamedEvents(result.Poset, "$module/api", arch.ArchitectureStartAction)
	if len(identities) != 1 || len(moduleStarts) != 1 {
		t.Fatalf("Self Identity/module Start counts=%d/%d", len(identities), len(moduleStarts))
	}
	value, exists := identities[0].Param("value")
	self, ok := value.(gorapide.RapideModuleValue)
	if !exists || !ok || self.Identity() == "" {
		t.Fatalf("Identity(Self) value=%#v, want a Rapide module allocation", value)
	}

	var component arch.ModuleLifecycleRecord
	for _, lifecycle := range result.Modules {
		selfNames := 0
		for _, name := range lifecycle.Names {
			if name.Kind == "implicit-self" {
				selfNames++
				if name.Name != "Self" || name.Owner != lifecycle.ModuleID || !name.Live ||
					len(name.AcquiredAfter) != 1 || name.AcquiredAfter[0] != lifecycle.StartEventID {
					t.Fatalf("module %q Self edge=%#v", lifecycle.ModuleID, name)
				}
			}
			if name.NameID == "component-name:api" {
				component = lifecycle
			}
		}
		if selfNames != 1 {
			t.Fatalf("module %q has %d implicit Self edges, want 1", lifecycle.ModuleID, selfNames)
		}
	}
	if component.ModuleID == "" || component.ModuleID != self.Identity() ||
		component.StartEventID != string(moduleStarts[0].ID) {
		t.Fatalf("Self/lifecycle allocation mismatch: Self=%q lifecycle=%#v Start=%s",
			self.Identity(), component, moduleStarts[0].ID)
	}

	artifact, err := result.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	repeatedArtifact, err := repeated.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(artifact, repeatedArtifact) {
		t.Fatal("GOMAXPROCS changed Self allocation or artifact bytes")
	}
	expected, err := result.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedArtifact, err := replayed.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(artifact, replayedArtifact) {
		t.Fatal("Self replay changed canonical artifact bytes")
	}
}

func TestSourceSelfCannotBeDeclaredOrComparedAsIdentity(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{
			name: "explicit declaration",
			source: `
type API is interface action out Done(); end interface API;
module Impl() return API is Self : Integer is 1; initial Done(); end module Impl;
architecture System() is api : API is Impl(); end architecture System;
`,
			want: "conflicts with tool-supplied Self",
		},
		{
			name: "equality is not allocation identity",
			source: `
type API is interface action out Identity(value : Boolean); end interface API;
module Impl() return API is initial Identity(Self = Self); end module Impl;
architecture System() is api : API is Impl(); end architecture System;
`,
			want: `operator "=" is not defined for API and API`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Compile([]byte(test.source), "System")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Compile()=%v, want error containing %q", err, test.want)
			}
		})
	}
}

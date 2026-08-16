package rapide

import (
	"bytes"
	"runtime"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/arch"
)

func TestSourceStructuralPatternPlaceholderRequiresActionParameterType(t *testing.T) {
	_, err := Compile([]byte(`
type Other is interface end interface Other;
type API is interface
  action out Offer(value : API);
  action out Seen(value : API);
  behavior begin
    (?M : Other) Offer(?M) => Seen(?M);;
  end interface API;
module M() return API is initial Offer(Self); end module M;
architecture System() is api : API is M(); end architecture System;
`), "System")
	if err == nil || !strings.Contains(err.Error(), "placeholder ?M has type Other but action parameter value has type") {
		t.Fatalf("Compile()=%v, want structural placeholder/action disagreement", err)
	}
}

func TestSourcePatternModuleBindingLivesForTransitionRule(t *testing.T) {
	source := []byte(`
type API is interface
  action out Offer(value : API);
  action out Seen(value : API);
end interface API;
module M() return API is
initial
  Offer(Self);
serial
  when (?M : API) Offer(?M) do
    Seen(?M);
  end when;
end module M;
architecture System() is api : API is M(); end architecture System;
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
	prior := runtime.GOMAXPROCS(1)
	result, err := model.ExecuteDeterministic(journal)
	if err != nil {
		runtime.GOMAXPROCS(prior)
		t.Fatal(err)
	}
	runtime.GOMAXPROCS(8)
	repeated, repeatedErr := model.ExecuteDeterministic(journal)
	runtime.GOMAXPROCS(prior)
	if repeatedErr != nil {
		t.Fatal(repeatedErr)
	}

	offer := sourceNamedEvents(result.Poset, "api", "Offer")
	seen := sourceNamedEvents(result.Poset, "api", "Seen")
	if len(offer) != 1 || len(seen) != 1 {
		t.Fatalf("Offer/Seen counts=%d/%d", len(offer), len(seen))
	}
	offerValue, offerExists := offer[0].Param("value")
	seenValue, seenExists := seen[0].Param("value")
	offerModule, offerTyped := offerValue.(gorapide.RapideModuleValue)
	seenModule, seenTyped := seenValue.(gorapide.RapideModuleValue)
	if !offerExists || !seenExists || !offerTyped || !seenTyped ||
		!gorapide.SameRapideModule(offerModule, seenModule) {
		t.Fatalf("Offer/Seen module values=%#v/%#v", offerValue, seenValue)
	}
	assertOnlyDirectCause(t, result.Poset, seen[0], offer[0])

	var lifecycle arch.ModuleLifecycleRecord
	for _, record := range result.Modules {
		if record.ModuleID == offerModule.Identity() {
			lifecycle = record
			break
		}
	}
	if lifecycle.ModuleID == "" || !lifecycle.Namable || lifecycle.State != arch.ModuleRunningState {
		t.Fatalf("bound static module lifecycle=%#v", lifecycle)
	}
	bindings := make([]arch.ModuleNameRecord, 0, 1)
	for _, name := range lifecycle.Names {
		if name.Kind == "pattern-binding" {
			bindings = append(bindings, name)
		}
	}
	if len(bindings) != 1 || bindings[0].Live || bindings[0].Name != "?M" ||
		bindings[0].Owner != lifecycle.ModuleID || len(bindings[0].AcquiredAfter) != 1 ||
		bindings[0].AcquiredAfter[0] != string(offer[0].ID) || len(bindings[0].LostAfter) != 1 ||
		bindings[0].LostAfter[0] != string(seen[0].ID) {
		t.Fatalf("pattern-binding edge=%#v", bindings)
	}

	artifact, _ := result.MarshalCanonical()
	repeatedArtifact, _ := repeated.MarshalCanonical()
	if !bytes.Equal(artifact, repeatedArtifact) {
		t.Fatal("GOMAXPROCS changed structural pattern binding or name lifetime")
	}
	expected, _ := result.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedArtifact, _ := replayed.MarshalCanonical()
	if !bytes.Equal(artifact, replayedArtifact) {
		t.Fatal("structural pattern-binding replay changed canonical bytes")
	}
}

func TestSourcePatternModuleBindingLivesAcrossProcessSuspension(t *testing.T) {
	source := []byte(`
type API is interface
  action out Offer(value : API);
  action out Seen(value : API);
end interface API;
module M() return API is
C : Clock is Make_Clock();
initial Offer(Self);
serial
  when (?M : API) Offer(?M) do
    pause C.Ticks range 1..1;
    Seen(?M);
  end when;
end module M;
architecture System() is api : API is M(); end architecture System;
`)
	model, err := Compile(source, "System")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 30))
	if err != nil {
		t.Fatal(err)
	}
	offer := sourceNamedEvents(result.Poset, "api", "Offer")
	seen := sourceNamedEvents(result.Poset, "api", "Seen")
	if len(offer) != 1 || len(seen) != 1 {
		t.Fatalf("Offer/Seen counts=%d/%d", len(offer), len(seen))
	}
	value, ok := offer[0].Param("value")
	module, typed := value.(gorapide.RapideModuleValue)
	if !ok || !typed {
		t.Fatalf("Offer module value=%#v", value)
	}
	var binding *arch.ModuleNameRecord
	for _, lifecycle := range result.Modules {
		if lifecycle.ModuleID != module.Identity() {
			continue
		}
		for index := range lifecycle.Names {
			if lifecycle.Names[index].Kind == "pattern-binding" {
				candidate := lifecycle.Names[index]
				binding = &candidate
			}
		}
	}
	if binding == nil || binding.Live || len(binding.AcquiredAfter) != 1 ||
		binding.AcquiredAfter[0] != string(offer[0].ID) || len(binding.LostAfter) != 1 ||
		binding.LostAfter[0] != string(seen[0].ID) {
		t.Fatalf("suspended pattern-binding edge=%#v", binding)
	}
	if len(result.ClockAdvances) != 1 || result.ClockAdvances[0].To != "1" {
		t.Fatalf("suspended pattern-binding clock advances=%#v", result.ClockAdvances)
	}
}

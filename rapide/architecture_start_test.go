package rapide

import (
	"bytes"
	"runtime"
	"testing"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/arch"
)

func TestCompiledArchitectureAndModuleStartsPrecedeInitialAndInput(t *testing.T) {
	source := []byte(`
type Worker is interface
  action out Boot();
  action out Input();
end interface Worker;

module WorkerModule() return Worker is
initial
  Boot();
end module WorkerModule;

architecture System() is
  worker : Worker is WorkerModule();
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
	journal := arch.NewExecutionJournal(digest, 10, arch.InputEvent{
		Key: "root-input", Source: "worker", Action: "Input",
	})
	result, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	starts := sourceNamedEvents(result.Poset, arch.ArchitectureInterfaceID, arch.ArchitectureStartAction)
	moduleStarts := sourceNamedEvents(result.Poset, "$module/worker", arch.ArchitectureStartAction)
	boots := sourceNamedEvents(result.Poset, "worker", "Boot")
	inputs := sourceNamedEvents(result.Poset, "worker", "Input")
	if len(starts) != 1 || len(moduleStarts) != 1 || len(boots) != 1 || len(inputs) != 1 {
		t.Fatalf("compiled startup events architecture/module/boot/input=%d/%d/%d/%d",
			len(starts), len(moduleStarts), len(boots), len(inputs))
	}
	start, moduleStart, boot, input := starts[0], moduleStarts[0], boots[0], inputs[0]
	moduleCauses := result.Poset.DirectCauses(moduleStart.ID)
	if len(moduleCauses) != 1 || moduleCauses[0].ID != start.ID {
		t.Fatalf("module Start direct causes=%#v, want architecture Start", moduleCauses)
	}
	for _, event := range []struct {
		name string
		id   gorapide.EventID
	}{{"module initial", boot.ID}, {"root input", input.ID}} {
		if !result.Poset.IsCausallyBefore(start.ID, event.id) ||
			!result.Poset.IsCausallyBefore(moduleStart.ID, event.id) {
			t.Fatalf("architecture/module Start chain does not precede %s", event.name)
		}
		causes := result.Poset.DirectCauses(event.id)
		if len(causes) != 1 || causes[0].ID != moduleStart.ID {
			t.Fatalf("%s direct causes=%#v, want module Start", event.name, causes)
		}
	}
	if !result.Poset.IsCausallyIndependent(boot.ID, input.ID) {
		t.Fatal("common architecture Start falsely ordered module initial and independent root input")
	}
	artifactDigest, err := result.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := model.ReplayDeterministic(journal, artifactDigest)
	if err != nil {
		t.Fatal(err)
	}
	left, err := result.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	right, err := replayed.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(left, right) {
		t.Fatal("compiled architecture Start replay changed canonical artifact bytes")
	}
}

func TestStaticModuleStartsAreSiblingIndependentAndOrderStable(t *testing.T) {
	source := func(components string) []byte {
		return []byte(`
type Worker is interface action out Boot(); end interface Worker;
module WorkerModule() return Worker is initial Boot(); end module WorkerModule;
architecture System() is
` + components + `
end architecture System;
`)
	}
	variants := [][]byte{
		source("  alpha : Worker is WorkerModule();\n  beta : Worker is WorkerModule();"),
		source("  beta : Worker is WorkerModule();\n  alpha : Worker is WorkerModule();"),
	}
	previous := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(previous)
	var wantModel string
	var wantArtifact []byte
	for _, processors := range []int{1, 8} {
		runtime.GOMAXPROCS(processors)
		for variant, source := range variants {
			model, err := Compile(source, "System")
			if err != nil {
				t.Fatal(err)
			}
			digest, err := model.DeterministicModelDigest()
			if err != nil {
				t.Fatal(err)
			}
			result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 10))
			if err != nil {
				t.Fatal(err)
			}
			root := sourceNamedEvents(result.Poset, arch.ArchitectureInterfaceID, arch.ArchitectureStartAction)
			alphaStart := sourceNamedEvents(result.Poset, "$module/alpha", arch.ArchitectureStartAction)
			betaStart := sourceNamedEvents(result.Poset, "$module/beta", arch.ArchitectureStartAction)
			alphaBoot := sourceNamedEvents(result.Poset, "alpha", "Boot")
			betaBoot := sourceNamedEvents(result.Poset, "beta", "Boot")
			if len(root) != 1 || len(alphaStart) != 1 || len(betaStart) != 1 ||
				len(alphaBoot) != 1 || len(betaBoot) != 1 {
				t.Fatalf("static module lifecycle counts root/alpha-start/beta-start/alpha-boot/beta-boot=%d/%d/%d/%d/%d",
					len(root), len(alphaStart), len(betaStart), len(alphaBoot), len(betaBoot))
			}
			for _, start := range []*gorapide.Event{alphaStart[0], betaStart[0]} {
				causes := result.Poset.DirectCauses(start.ID)
				if len(causes) != 1 || causes[0].ID != root[0].ID {
					t.Fatalf("static module Start causes=%#v", causes)
				}
			}
			for _, pair := range [][2]*gorapide.Event{{alphaStart[0], alphaBoot[0]}, {betaStart[0], betaBoot[0]}} {
				causes := result.Poset.DirectCauses(pair[1].ID)
				if len(causes) != 1 || causes[0].ID != pair[0].ID {
					t.Fatalf("module initial causes=%#v, want module Start", causes)
				}
			}
			if !result.Poset.IsCausallyIndependent(alphaStart[0].ID, betaStart[0].ID) ||
				!result.Poset.IsCausallyIndependent(alphaBoot[0].ID, betaBoot[0].ID) {
				t.Fatal("sibling module creation or initialization acquired false causality")
			}
			artifact, err := result.MarshalCanonical()
			if err != nil {
				t.Fatal(err)
			}
			if wantModel == "" {
				wantModel, wantArtifact = digest, artifact
			} else if digest != wantModel || !bytes.Equal(artifact, wantArtifact) {
				t.Fatalf("static module lifecycle changed for processors=%d variant=%d", processors, variant)
			}
		}
	}
}

func TestCrossModuleJournalCauseRetainsTargetModuleStart(t *testing.T) {
	model, err := Compile([]byte(`
type Worker is interface action out Seen(n : Integer); end interface Worker;
module WorkerModule() return Worker is end module WorkerModule;
architecture System() is
  alpha : Worker is WorkerModule();
  beta : Worker is WorkerModule();
end architecture System;
`), "System")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := arch.NewExecutionJournal(digest, 10,
		arch.InputEvent{Key: "alpha", Source: "alpha", Action: "Seen", Params: map[string]any{"n": 1}},
		arch.InputEvent{Key: "beta-first", Source: "beta", Action: "Seen", Params: map[string]any{"n": 2}, Causes: []string{"alpha"}},
		arch.InputEvent{Key: "beta-second", Source: "beta", Action: "Seen", Params: map[string]any{"n": 3}, Causes: []string{"beta-first"}},
	)
	result, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	alpha := sourceNamedEvents(result.Poset, "alpha", "Seen")
	beta := sourceNamedEvents(result.Poset, "beta", "Seen")
	betaStart := sourceNamedEvents(result.Poset, "$module/beta", arch.ArchitectureStartAction)
	if len(alpha) != 1 || len(beta) != 2 || len(betaStart) != 1 {
		t.Fatalf("cross-module observations alpha/beta/start=%d/%d/%d", len(alpha), len(beta), len(betaStart))
	}
	var first, second *gorapide.Event
	for _, event := range beta {
		if event.ParamInt("n") == 2 {
			first = event
		} else if event.ParamInt("n") == 3 {
			second = event
		}
	}
	if first == nil || second == nil {
		t.Fatalf("beta observations=%#v", beta)
	}
	firstCauses := result.Poset.DirectCauses(first.ID)
	if len(firstCauses) != 2 {
		t.Fatalf("cross-module first direct causes=%#v", firstCauses)
	}
	foundAlpha, foundBetaStart := false, false
	for _, cause := range firstCauses {
		foundAlpha = foundAlpha || cause.ID == alpha[0].ID
		foundBetaStart = foundBetaStart || cause.ID == betaStart[0].ID
	}
	if !foundAlpha || !foundBetaStart {
		t.Fatalf("cross-module first causes=%#v, want declared parent plus target module Start", firstCauses)
	}
	secondCauses := result.Poset.DirectCauses(second.ID)
	if len(secondCauses) != 1 || secondCauses[0].ID != first.ID {
		t.Fatalf("same-module transitive Start was not minimized: %#v", secondCauses)
	}
}

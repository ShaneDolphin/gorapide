package arch

import (
	"bytes"
	"testing"

	"github.com/ShaneDolphin/gorapide"
)

func TestArchitectureGeneratorCreatesOneCanonicalStartRoot(t *testing.T) {
	architecture := NewArchitecture("empty")
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := NewExecutionJournal(digest, 10)
	first, err := architecture.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	second, err := architecture.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	events := first.Poset.Events()
	if len(events) != 1 {
		t.Fatalf("empty architecture events=%#v, want one predefined Start", events)
	}
	start := events[0]
	if start.Source != ArchitectureInterfaceID || start.Name != ArchitectureStartAction ||
		len(start.Params) != 0 || len(first.Poset.DirectCauses(start.ID)) != 0 {
		t.Fatalf("architecture Start=%#v causes=%#v", start, first.Poset.DirectCauses(start.ID))
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
		t.Fatal("architecture Start identity changed across identical executions")
	}
	artifactDigest, err := first.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := architecture.ReplayDeterministic(journal, artifactDigest)
	if err != nil {
		t.Fatal(err)
	}
	replayedBytes, _ := replayed.MarshalCanonical()
	if !bytes.Equal(firstBytes, replayedBytes) {
		t.Fatal("architecture Start replay changed canonical artifact bytes")
	}
	explored, err := architecture.ExploreDeterministic(journal, ExplorationLimits{
		MaxExecutions: 2, MaxChoiceDepth: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !explored.Complete || len(explored.Computations) != 1 ||
		len(explored.Computations[0].Result.Poset.Events()) != 1 {
		t.Fatalf("empty architecture exploration=%#v", explored)
	}
}

func TestArchitectureStartPrecedesInitialsIndependentInputsAndConnections(t *testing.T) {
	architecture := NewArchitecture("start-causality")
	producer := NewComponent("producer", Interface("Producer").
		OutAction("Boot").OutAction("Input").Build(), nil)
	receiver := NewComponent("receiver", Interface("Receiver").InAction("Receive").Build(), nil)
	peer := NewComponent("peer", Interface("Peer").OutAction("Input").Build(), nil)
	if err := producer.SetInitialStatements(CallAction("boot", "Boot")); err != nil {
		t.Fatal(err)
	}
	for _, component := range []*Component{producer, receiver, peer} {
		if err := architecture.AddComponent(component); err != nil {
			t.Fatal(err)
		}
	}
	if err := architecture.AddConnection(Connect("producer", "receiver").
		IdentifiedBy("boot-to-receive").
		Send("Receive").Build()); err != nil {
		t.Fatal(err)
	}
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	result, err := architecture.ExecuteDeterministic(NewExecutionJournal(digest, 20,
		InputEvent{Key: "producer-root", Source: "producer", Action: "Input"},
		InputEvent{Key: "peer-root", Source: "peer", Action: "Input"},
		InputEvent{Key: "producer-child", Source: "producer", Action: "Input", Causes: []string{"producer-root"}},
	))
	if err != nil {
		t.Fatal(err)
	}
	start := onlySourceNamedEvent(t, result.Poset, ArchitectureInterfaceID, ArchitectureStartAction)
	boot := onlySourceNamedEvent(t, result.Poset, "producer", "Boot")
	if !boot.HasObservation("receiver", "Receive") {
		t.Fatalf("basic startup connection did not alias Boot at receiver: %#v", boot.ObservationViews())
	}
	producerInputs := sourceEventsNamed(result.Poset, "producer", "Input")
	peerInput := onlySourceNamedEvent(t, result.Poset, "peer", "Input")
	if len(producerInputs) != 2 {
		t.Fatalf("producer inputs=%#v", producerInputs)
	}
	var producerRoot, producerChild *gorapide.Event
	for _, event := range producerInputs {
		causes := result.Poset.DirectCauses(event.ID)
		if len(causes) == 1 && causes[0].ID == start.ID {
			producerRoot = event
		} else {
			producerChild = event
		}
	}
	if producerRoot == nil || producerChild == nil {
		t.Fatalf("input root/child classification failed: %#v", producerInputs)
	}
	for _, event := range []*gorapide.Event{boot, producerRoot, producerChild, peerInput} {
		if !result.Poset.IsCausallyBefore(start.ID, event.ID) {
			t.Fatalf("architecture Start does not precede %s.%s", event.Source, event.Name)
		}
	}
	if causes := result.Poset.DirectCauses(boot.ID); len(causes) != 1 || causes[0].ID != start.ID {
		t.Fatalf("module initial direct causes=%#v, want architecture Start", causes)
	}
	if causes := result.Poset.DirectCauses(peerInput.ID); len(causes) != 1 || causes[0].ID != start.ID {
		t.Fatalf("independent input direct causes=%#v, want architecture Start", causes)
	}
	if causes := result.Poset.DirectCauses(producerChild.ID); len(causes) != 1 || causes[0].ID != producerRoot.ID {
		t.Fatalf("dependent input direct causes=%#v, want only journal parent", causes)
	}
	if result.Poset.IsCausallyBefore(producerRoot.ID, peerInput.ID) ||
		result.Poset.IsCausallyBefore(peerInput.ID, producerRoot.ID) {
		t.Fatal("common architecture Start ordered independent component inputs")
	}
}

func sourceEventsNamed(poset *gorapide.Poset, source, name string) gorapide.EventSet {
	result := make(gorapide.EventSet, 0)
	for _, event := range poset.ByName(name) {
		if event.HasObservation(source, name) {
			result = append(result, event)
		}
	}
	return result
}

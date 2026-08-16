package arch

import (
	"bytes"
	"runtime"
	"strings"
	"testing"
)

func TestDeterministicArchitectureInitialIsOrderedSnapshottedAndReplayable(t *testing.T) {
	architecture := NewArchitecture("architecture-initial")
	if err := architecture.SetReturnInterface(Interface("Boundary").
		OutAction("Boot", P("n", "Integer")).Build()); err != nil {
		t.Fatal(err)
	}
	statements := []Statement{
		CallAction("first", "Boot", LiteralParam("n", 1)),
		CallAction("second", "Boot", LiteralParam("n", 2)),
	}
	if err := architecture.SetDeterministicArchitectureInitialStatements(
		ArchitectureInterfaceID, statements...,
	); err != nil {
		t.Fatal(err)
	}
	// The caller retains and mutates its slice; model identity and behavior must
	// remain tied to the snapshotted statement list.
	statements[0] = CallAction("changed", "Boot", LiteralParam("n", 99))
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := NewExecutionJournal(digest, 20)
	previous := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previous)
	one, err := architecture.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	boots := sourceEventsNamed(one.Poset, ArchitectureInterfaceID, "Boot")
	if len(boots) != 2 {
		t.Fatalf("architecture initial outputs=%#v", boots)
	}
	var first, second = boots[0], boots[1]
	if first.ParamInt("n") == 2 {
		first, second = second, first
	}
	if first.ParamInt("n") != 1 || second.ParamInt("n") != 2 {
		t.Fatalf("architecture initial output values=%#v", boots)
	}
	if !one.Poset.IsCausallyBefore(first.ID, second.ID) {
		t.Fatal("ordered architecture initial statements did not retain sequential causality")
	}
	start := onlySourceNamedEvent(t, one.Poset, ArchitectureInterfaceID, ArchitectureStartAction)
	if causes := one.Poset.DirectCauses(first.ID); len(causes) != 1 || causes[0].ID != start.ID {
		t.Fatalf("first architecture initial causes=%#v, want Start", causes)
	}
	oneBytes, err := one.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	runtime.GOMAXPROCS(4)
	two, err := architecture.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	twoBytes, _ := two.MarshalCanonical()
	if !bytes.Equal(oneBytes, twoBytes) {
		t.Fatal("architecture initial artifact changed with GOMAXPROCS")
	}
	artifactDigest, err := one.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := architecture.ReplayDeterministic(journal, artifactDigest)
	if err != nil {
		t.Fatal(err)
	}
	replayedBytes, _ := replayed.MarshalCanonical()
	if !bytes.Equal(oneBytes, replayedBytes) {
		t.Fatal("architecture initial replay changed canonical artifact bytes")
	}
	explored, err := architecture.ExploreDeterministic(journal, ExplorationLimits{
		MaxExecutions: 2, MaxChoiceDepth: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !explored.Complete || len(explored.Computations) != 1 {
		t.Fatalf("architecture initial exploration=%#v", explored)
	}
	exploredBytes, _ := explored.Computations[0].Result.MarshalCanonical()
	if !bytes.Equal(oneBytes, exploredBytes) {
		t.Fatal("architecture initial exploration changed canonical artifact bytes")
	}

	reversed := NewArchitecture("architecture-initial")
	if err := reversed.SetReturnInterface(Interface("Boundary").
		OutAction("Boot", P("n", "Integer")).Build()); err != nil {
		t.Fatal(err)
	}
	if err := reversed.SetDeterministicArchitectureInitialStatements(
		ArchitectureInterfaceID,
		CallAction("second", "Boot", LiteralParam("n", 2)),
		CallAction("first", "Boot", LiteralParam("n", 1)),
	); err != nil {
		t.Fatal(err)
	}
	reversedDigest, err := reversed.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if reversedDigest == digest {
		t.Fatal("reversing semantic architecture initial statement order did not change model identity")
	}
}

func TestArchitectureInitialExceptionCatalogIsCanonicalOwnedAndSnapshotted(t *testing.T) {
	boundary := Interface("Boundary").OutAction("Done").Build()
	declarationID := "rapide:architecture:root:initializer-do:initial/0:label:stable:exception:local"
	declaration := DeclaredException(declarationID, "Local", P("code", "Integer"))

	build := func(exception ExceptionDeclaration) *Architecture {
		architecture := NewArchitecture("architecture-initial-exception-catalog")
		if err := architecture.SetReturnInterface(boundary); err != nil {
			t.Fatal(err)
		}
		if err := architecture.SetDeterministicArchitectureInitialExceptionDeclarations(
			ArchitectureInterfaceID, exception,
		); err != nil {
			t.Fatal(err)
		}
		if err := architecture.SetDeterministicArchitectureInitialStatements(
			ArchitectureInterfaceID, NullStatement(),
		); err != nil {
			t.Fatal(err)
		}
		return architecture
	}

	architecture := build(declaration)
	declaration.Declaration = "mutated"
	declaration.Name = "Mutated"
	declaration.Params[0].Name = "mutated"
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	cleanDigest, err := build(DeclaredException(
		declarationID, "Local", P("code", "Integer"),
	)).DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if digest != cleanDigest {
		t.Fatalf("caller mutation changed snapshotted architecture exception catalog: %s != %s", digest, cleanDigest)
	}
	changedDigest, err := build(DeclaredException(
		declarationID+":changed", "Local", P("code", "Integer"),
	)).DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if changedDigest == digest {
		t.Fatal("unused architecture initial exception declaration identity is absent from canonical model data")
	}
}

func TestArchitectureInitialExceptionCatalogBoundaryFailuresAreExplicit(t *testing.T) {
	var nilArchitecture *Architecture
	declaration := DeclaredException("architecture-initial:local", "Local")
	if err := nilArchitecture.SetDeterministicArchitectureInitialExceptionDeclarations(
		ArchitectureInterfaceID, declaration,
	); err == nil || !strings.Contains(err.Error(), "architecture is nil") {
		t.Fatalf("nil architecture error=%v", err)
	}

	architecture := NewArchitecture("architecture-initial-exception-boundaries")
	if err := architecture.SetDeterministicArchitectureInitialExceptionDeclarations(
		ArchitectureInterfaceID,
	); err == nil || !strings.Contains(err.Error(), "catalog is empty") {
		t.Fatalf("empty catalog error=%v", err)
	}
	if err := architecture.SetDeterministicArchitectureInitialExceptionDeclarations(
		"missing", declaration,
	); err == nil || !strings.Contains(err.Error(), `instance "missing" is not declared`) {
		t.Fatalf("missing owner error=%v", err)
	}
	if err := architecture.SetDeterministicArchitectureInitialExceptionDeclarations(
		ArchitectureInterfaceID, declaration,
	); err != nil {
		t.Fatal(err)
	}
	if err := architecture.SetDeterministicArchitectureInitialExceptionDeclarations(
		ArchitectureInterfaceID, declaration,
	); err == nil || !strings.Contains(err.Error(), "already has an initial exception catalog") {
		t.Fatalf("duplicate catalog error=%v", err)
	}
	if _, err := architecture.DeterministicModelDigest(); err == nil ||
		!strings.Contains(err.Error(), "without an initial part") {
		t.Fatalf("ownerless catalog model error=%v", err)
	}

	running := NewArchitecture("running-architecture-initial-exception-catalog")
	running.running = true
	if err := running.SetDeterministicArchitectureInitialExceptionDeclarations(
		ArchitectureInterfaceID, declaration,
	); err == nil || !strings.Contains(err.Error(), "is running") {
		t.Fatalf("running architecture error=%v", err)
	}
}

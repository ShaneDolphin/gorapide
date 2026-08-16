package rapide

import (
	"bytes"
	"runtime"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide/arch"
)

func TestSourceOutermostExceptionDeclarationHandlesLinkedPropagation(t *testing.T) {
	source := []byte(`
exception Failure(code : Integer);

type Aircraft is interface action out Register(value : Aircraft); action in Go(); end interface Aircraft;
type Sector is interface
  action in Accept(value : Aircraft);
  action out Ready();
  action out NamedRecovery(code : Integer);
  action out ElseRecovery();
end interface Sector;

module AircraftModule() return Aircraft is
initial Register(Self);
serial when Go do raise Failure(code is 7); end when;
end module AircraftModule;

module SectorModule() return Sector is
serial when (?Aircraft : Aircraft) Accept(?Aircraft) do
  Link(?Aircraft);
  Ready();
end when;
handler
  is Failure(code is ?Code) => NamedRecovery(?Code);
  else ElseRecovery();
end module SectorModule;

architecture System() is
  plane : Aircraft is AircraftModule();
  sector : Sector is SectorModule();
connect
  (?Aircraft : Aircraft) plane.Register(?Aircraft) to sector.Accept(?Aircraft);
  sector.Ready to plane.Go;
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
	journal := arch.NewExecutionJournal(digest, 50)
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

	failures := sourceNamedEvents(result.Poset, "plane", "Failure")
	named := sourceNamedEvents(result.Poset, "sector", "NamedRecovery")
	elseRecovery := sourceNamedEvents(result.Poset, "sector", "ElseRecovery")
	if len(failures) != 1 || len(named) != 1 || len(elseRecovery) != 0 {
		t.Fatalf("Failure/NamedRecovery/ElseRecovery=%d/%d/%d",
			len(failures), len(named), len(elseRecovery))
	}
	code, _ := named[0].Param("code")
	if code != int64(7) {
		t.Fatalf("NamedRecovery code=%#v", code)
	}
	assertOnlyDirectCause(t, result.Poset, named[0], failures[0])

	planeModule := lifecycleModuleByOccurrence(t, result, "component:plane")
	sectorModule := lifecycleModuleByOccurrence(t, result, "component:sector")
	propagation := exceptionPropagationBySource(t, result, planeModule.ModuleID)
	if propagation.ExceptionDeclaration != outermostExceptionDeclarationIdentity("Failure") {
		t.Fatalf("exception declaration identity=%q", propagation.ExceptionDeclaration)
	}
	sectorHandled := false
	for _, target := range propagation.Targets {
		if target.ModuleID == sectorModule.ModuleID {
			if target.Disposition != "handled" || strings.Join(target.Relations, ",") != "linked" {
				t.Fatalf("linked named handler target=%#v", target)
			}
			sectorHandled = true
		}
	}
	if !sectorHandled {
		t.Fatalf("linked named handler target absent: %#v", propagation)
	}

	encoded, _ := result.MarshalCanonical()
	repeatedEncoded, _ := repeated.MarshalCanonical()
	if !bytes.Equal(encoded, repeatedEncoded) {
		t.Fatal("GOMAXPROCS changed outermost-exception propagation")
	}
	expected, err := result.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedEncoded, _ := replayed.MarshalCanonical()
	if !bytes.Equal(encoded, replayedEncoded) {
		t.Fatal("outermost-exception replay changed canonical bytes")
	}
}

func TestSourceOutermostExceptionVisibilityIsLinear(t *testing.T) {
	source := []byte(`
type Worker is interface action out Wrong(); end interface Worker;
module WorkerModule() return Worker is
initial raise Failure;
end module WorkerModule;
exception Failure;
architecture System() is worker : Worker is WorkerModule(); end architecture System;
`)
	_, err := Compile(source, "System")
	if err == nil || !strings.Contains(err.Error(), "undeclared exception \"Failure\"") {
		t.Fatalf("late outermost exception error=%v", err)
	}
}

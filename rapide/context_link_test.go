package rapide

import (
	"bytes"
	"errors"
	"runtime"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/arch"
)

const sourceContextLinkModel = `
type Aircraft is interface
  action out Register(value : Aircraft);
  action in Go();
  action out Radio(message : Integer);
end interface Aircraft;

type Sector is interface
  action in Accept(value : Aircraft);
  action out Ready();
  action out Receive(message : Integer);
end interface Sector;

module AircraftModule() return Aircraft is
initial
  Register(Self);
serial
  when Go do
    Radio(7);
  end when;
end module AircraftModule;

module SectorModule() return Sector is
connect
  (?A : Aircraft; ?M : Integer) ?A.Radio(?M) ||> Receive(?M);
serial
  when (?A : Aircraft) Accept(?A) do
    LINK_BODY
    Ready();
  end when;
end module SectorModule;

architecture System() is
  plane : Aircraft is AircraftModule();
  sector : Sector is SectorModule();
connect
  (?A : Aircraft) plane.Register(?A) to sector.Accept(?A);
  sector.Ready to plane.Go;
end architecture System;
`

func compileContextLinkModel(t *testing.T, body string) (*arch.Architecture, arch.ExecutionJournal) {
	t.Helper()
	model, err := Compile([]byte(strings.Replace(sourceContextLinkModel, "LINK_BODY", body, 1)), "System")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	return model, arch.NewExecutionJournal(digest, 40)
}

func TestSourceLinkAddsDynamicCommunicationContextAndRoutesBroadcast(t *testing.T) {
	model, journal := compileContextLinkModel(t, "Link(?A); Link(?A);")
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

	register := sourceNamedEvents(result.Poset, "plane", "Register")
	accept := sourceNamedEvents(result.Poset, "sector", "Accept")
	radio := sourceNamedEvents(result.Poset, "plane", "Radio")
	receive := sourceNamedEvents(result.Poset, "sector", "Receive")
	if len(register) != 1 || len(accept) != 1 || len(radio) != 1 || len(receive) != 1 {
		t.Fatalf("Register/Accept/Radio/Receive counts=%d/%d/%d/%d",
			len(register), len(accept), len(radio), len(receive))
	}
	if register[0].ID != accept[0].ID {
		t.Fatal("basic Register-to-Accept connection changed occurrence identity")
	}
	if receive[0].ParamInt("message") != 7 || !result.Poset.IsCausallyBefore(radio[0].ID, receive[0].ID) {
		t.Fatalf("dynamic Radio delivery=%#v", receive[0])
	}
	value, exists := register[0].Param("value")
	plane, typed := value.(gorapide.RapideModuleValue)
	if !exists || !typed {
		t.Fatalf("Register module value=%#v", value)
	}
	sectorID := ""
	sectorStartID := ""
	for _, module := range result.Modules {
		if module.Occurrence == "component:sector" {
			sectorID = module.ModuleID
			sectorStartID = module.StartEventID
			break
		}
	}
	if sectorID == "" {
		t.Fatal("sector module lifecycle is absent")
	}
	var context *arch.CommunicationContextRecord
	for index := range result.Contexts {
		candidate := &result.Contexts[index]
		if candidate.Kind == "explicit-link" && candidate.Source == plane.Identity() && candidate.Destination == sectorID {
			context = candidate
			break
		}
	}
	if context == nil || !context.Live || len(context.AcquiredAfter) != 2 ||
		!containsString(context.AcquiredAfter, string(accept[0].ID)) ||
		!containsString(context.AcquiredAfter, sectorStartID) || len(context.LostAfter) != 0 {
		t.Fatalf("explicit communication Context edge=%#v", context)
	}
	contextNames := 0
	for _, module := range result.Modules {
		if module.ModuleID != plane.Identity() {
			continue
		}
		for _, name := range module.Names {
			if name.Kind == "context-link" && name.Owner == sectorID && name.Live {
				contextNames++
			}
		}
	}
	if contextNames != 1 {
		t.Fatalf("live context-link lifecycle names=%d, want 1", contextNames)
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
		t.Fatal("GOMAXPROCS changed dynamic Context execution bytes")
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
		t.Fatal("dynamic Context replay changed canonical bytes")
	}
}

func TestSourceUnlinkRemovesDynamicCommunicationContextBeforeBroadcast(t *testing.T) {
	model, journal := compileContextLinkModel(t, "Link(?A); Unlink(?A); Unlink(?A);")
	result, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	register := sourceNamedEvents(result.Poset, "plane", "Register")
	accept := sourceNamedEvents(result.Poset, "sector", "Accept")
	radio := sourceNamedEvents(result.Poset, "plane", "Radio")
	receive := sourceNamedEvents(result.Poset, "sector", "Receive")
	if len(register) != 1 || len(accept) != 1 || len(radio) != 1 || len(receive) != 0 {
		t.Fatalf("unlinked Register/Accept/Radio/Receive counts=%d/%d/%d/%d",
			len(register), len(accept), len(radio), len(receive))
	}
	value, _ := register[0].Param("value")
	plane, ok := value.(gorapide.RapideModuleValue)
	if !ok {
		t.Fatalf("Register module value=%#v", value)
	}
	var context *arch.CommunicationContextRecord
	sectorStartID := ""
	for _, module := range result.Modules {
		if module.Occurrence == "component:sector" {
			sectorStartID = module.StartEventID
			break
		}
	}
	for index := range result.Contexts {
		candidate := &result.Contexts[index]
		if candidate.Kind == "explicit-link" && candidate.Source == plane.Identity() {
			context = candidate
			break
		}
	}
	if context == nil || context.Live || len(context.AcquiredAfter) != 2 ||
		!containsString(context.AcquiredAfter, string(accept[0].ID)) ||
		!containsString(context.AcquiredAfter, sectorStartID) ||
		strings.Join(context.AcquiredAfter, "\x00") != strings.Join(context.LostAfter, "\x00") {
		t.Fatalf("released communication Context edge=%#v", context)
	}
}

func TestExecutionJournalCannotFabricateModuleValue(t *testing.T) {
	model, journal := compileContextLinkModel(t, "Link(?A);")
	forged, err := gorapide.NewRapideModuleValue(gorapide.ModuleAllocationProvenance{
		Profile: journal.Profile, Model: journal.ModelDigest, Parent: "external",
		Generator: "forged", Occurrence: "journal", Causes: []gorapide.EventID{"evt1-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
	})
	if err != nil {
		t.Fatal(err)
	}
	journal.Inputs = append(journal.Inputs, arch.InputEvent{
		Key: "forged-register", Source: "plane", Action: "Register",
		Params: map[string]any{"value": forged},
	})
	_, err = model.ExecuteDeterministic(journal)
	if !errors.Is(err, arch.ErrInvalidExecutionJournal) ||
		!strings.Contains(err.Error(), "module values must originate from registered Rapide execution") {
		t.Fatalf("journal module-value boundary=%v", err)
	}
}

func TestSourceContextCompilerRejectsUnsupportedForms(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
		want string
	}{
		{name: "zero argument Link", body: "Link();", want: "requires one positional module actual"},
		{name: "two argument Link", body: "Link(?A, ?A);", want: "requires one positional module actual"},
		{name: "named Link actual", body: "Link(value is ?A);", want: "requires one positional module actual"},
		{name: "scalar Link actual", body: "Link(7);", want: "scalar type Integer, want a module interface"},
		{name: "two argument Unlink", body: "Unlink(?A, ?A);", want: "requires one positional module actual"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := Compile([]byte(strings.Replace(sourceContextLinkModel, "LINK_BODY", test.body, 1)), "System")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Compile()=%v, want error containing %q", err, test.want)
			}
		})
	}

	_, err := Compile([]byte(`
type Aircraft is interface action in Radio(message : Integer); end interface Aircraft;
type Sector is interface action out Receive(message : Integer); end interface Sector;
module SectorModule() return Sector is
connect
  (?A : Aircraft; ?M : Integer) ?A.Radio(?M) ||> Receive(?M);
end module SectorModule;
architecture System() is sector : Sector is SectorModule(); end architecture System;
`), "System")
	if err == nil || !strings.Contains(err.Error(), "is not an out action broadcast") {
		t.Fatalf("dynamic in-action source boundary=%v", err)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

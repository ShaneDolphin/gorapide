package rapide

import (
	"bytes"
	"runtime"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/arch"
)

func recordObjectSource(declarations string) []byte {
	return []byte(`
type Employee is record
  Name : String;
  Salary : Integer;
end record Employee;

type API is interface
  Person : Employee;
  action out Ready(name : String; salary : Integer);
  action out Input();
end interface API;

module Impl() return API is
` + declarations + `
initial
  Ready(Person.Name, Person.Salary);
end module Impl;

architecture System() is
  api : API is Impl();
end architecture System;
`)
}

func TestSourceRecordLiteralAllocatesModuleAndSelectionFeedsInitial(t *testing.T) {
	source := recordObjectSource(`  Person : Employee is (Salary is 45000, Name is "Jack");`)
	model, err := Compile(source, "System")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := arch.NewExecutionJournal(digest, 20, arch.InputEvent{
		Key: "input", Source: "api", Action: "Input",
	})
	result, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	root := sourceNamedEvents(result.Poset, arch.ArchitectureInterfaceID, arch.ArchitectureStartAction)
	module := sourceNamedEvents(result.Poset, "$module/api", arch.ArchitectureStartAction)
	records := recordStartEvents(result.Poset)
	ready := sourceNamedEvents(result.Poset, "api", "Ready")
	input := sourceNamedEvents(result.Poset, "api", "Input")
	if len(root) != 1 || len(module) != 1 || len(records) != 1 || len(ready) != 1 || len(input) != 1 {
		t.Fatalf("Record lifecycle root/module/record/ready/input=%d/%d/%d/%d/%d",
			len(root), len(module), len(records), len(ready), len(input))
	}
	if !strings.HasPrefix(records[0].Source, "mod1-") || records[0].Source == module[0].Source {
		t.Fatalf("Record Start source %q is not a distinct allocation identity", records[0].Source)
	}
	var staticLifecycle, recordLifecycle arch.ModuleLifecycleRecord
	for _, lifecycle := range result.Modules {
		if lifecycle.Kind == "record-module" {
			recordLifecycle = lifecycle
		}
		for _, name := range lifecycle.Names {
			if name.NameID == "component-name:api" {
				staticLifecycle = lifecycle
			}
		}
	}
	if !strings.HasPrefix(staticLifecycle.ModuleID, "mod1-") ||
		recordLifecycle.Parent != staticLifecycle.ModuleID ||
		recordLifecycle.ModuleID != records[0].Source ||
		staticLifecycle.StartEventID != string(module[0].ID) {
		t.Fatalf("static/Record allocation graph=%#v/%#v", staticLifecycle, recordLifecycle)
	}
	assertOnlyDirectCause(t, result.Poset, module[0], root[0])
	assertOnlyDirectCause(t, result.Poset, records[0], module[0])
	assertOnlyDirectCause(t, result.Poset, ready[0], records[0])
	assertOnlyDirectCause(t, result.Poset, input[0], records[0])
	if !result.Poset.IsCausallyIndependent(ready[0].ID, input[0].ID) {
		t.Fatal("common Record Start falsely ordered module initial and independent input")
	}
	if ready[0].ParamString("name") != "Jack" || ready[0].ParamInt("salary") != 45000 {
		t.Fatalf("Record selections emitted Ready=%#v", ready[0].Params)
	}

	artifact, err := result.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	artifactDigest, err := result.ArtifactDigest()
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
	if !bytes.Equal(artifact, replayedBytes) {
		t.Fatal("Record literal replay changed canonical artifact bytes")
	}
}

func TestSourceRecordLiteralFieldOrderCaseAndGOMAXPROCSAreNonsemantic(t *testing.T) {
	variants := [][]byte{
		recordObjectSource(`  Person : Employee is (Salary is 45000, Name is "Jack");`),
		recordObjectSource(`  person : employee is (name is "Jack", SALARY is 45000);`),
	}
	previous := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(previous)
	var wantDigest string
	var wantArtifact []byte
	for index, source := range variants {
		runtime.GOMAXPROCS([]int{1, 8}[index])
		model, err := Compile(source, "system")
		if err != nil {
			t.Fatal(err)
		}
		digest, err := model.DeterministicModelDigest()
		if err != nil {
			t.Fatal(err)
		}
		result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 20))
		if err != nil {
			t.Fatal(err)
		}
		artifact, err := result.MarshalCanonical()
		if err != nil {
			t.Fatal(err)
		}
		if wantDigest == "" {
			wantDigest, wantArtifact = digest, artifact
		} else if digest != wantDigest || !bytes.Equal(artifact, wantArtifact) {
			t.Fatal("Record field/object spelling, field order, or GOMAXPROCS changed deterministic results")
		}
	}
}

func TestSourceRecordObjectsAllocatePerComponentInstance(t *testing.T) {
	source := []byte(`
type Item is record Value : Integer; end record Item;
type API is interface Object : Item; action out Ready(value : Integer); end interface API;
module Impl() return API is
  Object : Item is (Value is 7);
initial Ready(Object.Value);
end module Impl;
architecture System() is
  alpha : API is Impl();
  beta : API is Impl();
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
	journal := arch.NewExecutionJournal(digest, 30)
	previous := runtime.GOMAXPROCS(1)
	first, err := model.ExecuteDeterministic(journal)
	if err != nil {
		runtime.GOMAXPROCS(previous)
		t.Fatal(err)
	}
	runtime.GOMAXPROCS(8)
	second, secondErr := model.ExecuteDeterministic(journal)
	runtime.GOMAXPROCS(previous)
	if secondErr != nil {
		t.Fatal(secondErr)
	}
	records := recordStartEvents(first.Poset)
	alphaModule := sourceNamedEvents(first.Poset, "$module/alpha", arch.ArchitectureStartAction)
	betaModule := sourceNamedEvents(first.Poset, "$module/beta", arch.ArchitectureStartAction)
	alphaReady := sourceNamedEvents(first.Poset, "alpha", "Ready")
	betaReady := sourceNamedEvents(first.Poset, "beta", "Ready")
	if len(records) != 2 || len(alphaModule) != 1 || len(betaModule) != 1 ||
		len(alphaReady) != 1 || len(betaReady) != 1 {
		t.Fatalf("per-instance Record lifecycle records/modules/ready=%d/%d,%d/%d,%d",
			len(records), len(alphaModule), len(betaModule), len(alphaReady), len(betaReady))
	}
	if records[0].Source == records[1].Source {
		t.Fatal("separate component instances share one Record allocation identity")
	}
	var alphaRecord, betaRecord *gorapide.Event
	for _, record := range records {
		causes := first.Poset.DirectCauses(record.ID)
		if len(causes) != 1 {
			t.Fatalf("Record Start direct causes=%#v", causes)
		}
		switch causes[0].ID {
		case alphaModule[0].ID:
			alphaRecord = record
		case betaModule[0].ID:
			betaRecord = record
		}
	}
	if alphaRecord == nil || betaRecord == nil {
		t.Fatal("Record allocations are not rooted in their enclosing module instances")
	}
	assertOnlyDirectCause(t, first.Poset, alphaReady[0], alphaRecord)
	assertOnlyDirectCause(t, first.Poset, betaReady[0], betaRecord)
	if !first.Poset.IsCausallyIndependent(alphaRecord.ID, betaRecord.ID) ||
		!first.Poset.IsCausallyIndependent(alphaReady[0].ID, betaReady[0].ID) {
		t.Fatal("sibling component Record allocations acquired false causality")
	}
	firstBytes, _ := first.MarshalCanonical()
	secondBytes, _ := second.MarshalCanonical()
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatal("GOMAXPROCS changed per-component Record allocation artifacts")
	}
}

func TestSourceRecordDeclarationOrderIsSemanticModelData(t *testing.T) {
	source := func(declarations string) []byte {
		return []byte(`
type Item is record Value : Integer; end record Item;
type API is interface First, Second : Item; action out Done(); end interface API;
module Impl() return API is
` + declarations + `
initial Done();
end module Impl;
architecture System() is api : API is Impl(); end architecture System;
`)
	}
	left, err := Compile(source(`
  First : Item is (Value is 1);
  Second : Item is (Value is 2);`), "System")
	if err != nil {
		t.Fatal(err)
	}
	right, err := Compile(source(`
  Second : Item is (Value is 2);
  First : Item is (Value is 1);`), "System")
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
	if leftDigest == rightDigest {
		t.Fatal("successive Record literal declaration order disappeared from canonical model identity")
	}
}

func TestSourceRecordDeclarationsAllocateDistinctSequentialModules(t *testing.T) {
	source := []byte(`
type Pair is record Value : Integer; end record Pair;
type API is interface First, Second : Pair; action out Done(); end interface API;
module Impl() return API is
  First, Second : Pair is (Value is 1);
initial Done();
end module Impl;
architecture System() is api : API is Impl(); end architecture System;
`)
	model, err := Compile(source, "System")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 20))
	if err != nil {
		t.Fatal(err)
	}
	module := sourceNamedEvents(result.Poset, "$module/api", arch.ArchitectureStartAction)
	records := recordStartEvents(result.Poset)
	done := sourceNamedEvents(result.Poset, "api", "Done")
	if len(module) != 1 || len(records) != 2 || len(done) != 1 {
		t.Fatalf("sequential Record lifecycle module/record/done=%d/%d/%d", len(module), len(records), len(done))
	}
	if records[0].Source == records[1].Source {
		t.Fatal("separate Record literal evaluations share an allocation identity")
	}
	var first, second *gorapide.Event
	for _, candidate := range records {
		causes := result.Poset.DirectCauses(candidate.ID)
		if len(causes) == 1 && causes[0].ID == module[0].ID {
			first = candidate
		} else {
			second = candidate
		}
	}
	if first == nil || second == nil {
		t.Fatalf("Record Starts do not form a module-rooted sequence: %#v", records)
	}
	assertOnlyDirectCause(t, result.Poset, second, first)
	assertOnlyDirectCause(t, result.Poset, done[0], second)
}

func TestSourceRecordLiteralAndSelectionFailuresAreExplicit(t *testing.T) {
	base := func(declaration, initial string) []byte {
		return []byte(`
type Employee is record Name : String; Salary : Integer; end record Employee;
type API is interface Person : Employee; action out Ready(value : Integer); end interface API;
module Impl() return API is ` + declaration + ` initial ` + initial + ` end module Impl;
architecture System() is api : API is Impl(); end architecture System;
`)
	}
	tests := []struct {
		name   string
		source []byte
		want   string
	}{
		{"missing field", base(`Person : Employee is (Name is "Jack");`, `Ready(1);`), `does not initialize field "Salary"`},
		{"extra field", base(`Person : Employee is (Name is "Jack", Salary is 1, Extra is true);`, `Ready(1);`), `literal field "Extra" is not declared`},
		{"duplicate field", base(`Person : Employee is (Name is "Jack", name is "Jill", Salary is 1);`, `Ready(1);`), `duplicate literal field "name"`},
		{"wrong field type", base(`Person : Employee is (Name is 1, Salary is 1);`, `Ready(1);`), `field "Name" initializer has type Integer, want String`},
		{"missing selection", base(`Person : Employee is (Name is "Jack", Salary is 1);`, `Ready(Person.Missing);`), `has no field "Missing"`},
		{"Record as scalar", base(`Person : Employee is (Name is "Jack", Salary is 1);`, `Ready(Person);`), `is a structural module value`},
		{"literal outside Record context", base(`Person : Employee is (Name is "Jack", Salary is 1); Other : Integer is (Value is 1);`, `Ready(Other);`), `Record literal requires a direct named Record type context`},
		{"Record equality", base(`Person : Employee is (Name is "Jack", Salary is 1);`, `Ready(if Person = Person then 1 else 0 end if);`), `is a structural module value`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Compile(test.source, "System")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v, want error containing %q", err, test.want)
			}
		})
	}
}

func recordStartEvents(poset *gorapide.Poset) gorapide.EventSet {
	result := make(gorapide.EventSet, 0)
	for _, event := range poset.ByName(arch.ArchitectureStartAction) {
		if strings.HasPrefix(event.Source, "mod1-") {
			result = append(result, event)
		}
	}
	return result
}

func assertOnlyDirectCause(t *testing.T, poset *gorapide.Poset, event, cause *gorapide.Event) {
	t.Helper()
	causes := poset.DirectCauses(event.ID)
	if len(causes) != 1 || causes[0].ID != cause.ID {
		t.Fatalf("%s.%s direct causes=%#v, want only %s.%s", event.Source, event.Name, causes, cause.Source, cause.Name)
	}
}

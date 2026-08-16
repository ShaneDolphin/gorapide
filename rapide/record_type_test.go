package rapide

import (
	"bytes"
	"runtime"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/arch"
)

func TestParseStanfordRecordTypeFieldsAndDerivation(t *testing.T) {
	file, err := Parse([]byte(`
type Employee_Record is record
  Name : String;
  Salary : Integer;
end record Employee_Record;
type Manager_Record is record
  include Employee_Record;
  Department : String;
end Manager_Record;
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(file.Interfaces) != 2 || len(file.TypeAliases) != 0 {
		t.Fatalf("record declarations parsed into the wrong namespace: %#v", file)
	}
	employee := file.Interfaces[0]
	if !employee.Record || employee.Name != "Employee_Record" || len(employee.Objects) != 2 ||
		employee.Objects[0].Name != "Name" || employee.Objects[0].Type != "String" ||
		employee.Objects[1].Name != "Salary" || employee.Objects[1].Type != "Integer" {
		t.Fatalf("employee record AST=%#v", employee)
	}
	manager := file.Interfaces[1]
	if !manager.Record || manager.Name != "Manager_Record" || len(manager.Derivations) != 1 ||
		manager.Derivations[0].Source != "Employee_Record" || len(manager.Objects) != 1 ||
		manager.Objects[0].Name != "Department" {
		t.Fatalf("manager record AST=%#v", manager)
	}
	normalized, err := normalizeInterfaceDeclarations(file.Interfaces)
	if err != nil {
		t.Fatal(err)
	}
	manager = normalized["manager_record"]
	fieldNames := make(map[string]bool, len(manager.Objects))
	for _, field := range manager.Objects {
		fieldNames[field.Name] = true
	}
	if len(manager.Objects) != 3 || !fieldNames["Department"] ||
		!fieldNames["Name"] || !fieldNames["Salary"] {
		t.Fatalf("normalized manager fields=%#v", manager.Objects)
	}
}

func TestRecordTypeIsTheSameStructuralTypeAsEquivalentInterface(t *testing.T) {
	recordSource := []byte(`
type Employee is record Name : String; Salary : Integer; end record Employee;
architecture System() is employee : Employee; end architecture System;
`)
	interfaceSource := []byte(`
type Employee is interface Salary : Integer; Name : String; end interface Employee;
architecture System() is employee : Employee; end architecture System;
`)
	recordModel, err := Compile(recordSource, "System")
	if err != nil {
		t.Fatal(err)
	}
	interfaceModel, err := Compile(interfaceSource, "System")
	if err != nil {
		t.Fatal(err)
	}
	recordDigest, err := recordModel.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	interfaceDigest, err := interfaceModel.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if recordDigest != interfaceDigest {
		t.Fatalf("record spelling introduced nominal model identity: %s != %s", recordDigest, interfaceDigest)
	}

	recordType := mustCompiledComponentRapideType(t, recordModel, "employee")
	interfaceType := mustCompiledComponentRapideType(t, interfaceModel, "employee")
	recordBytes, err := recordType.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	interfaceBytes, err := interfaceType.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(recordBytes, interfaceBytes) {
		t.Fatalf("record and interface structural descriptors differ:\n%s\n%s", recordBytes, interfaceBytes)
	}
}

func TestRecordTypeSubtypingUsesStructuralWidthAndFieldCovariance(t *testing.T) {
	declarations := mustParseInterfaceDeclarations(t, `
type Details is interface Name : String; end interface Details;
type More_Details is include Details; interface Department : String; end interface More_Details;
type Summary is record Data : Details; end record Summary;
type Full is record Data : More_Details; Extra : String; end record Full;
`)
	normalized, err := normalizeInterfaceDeclarations(declarations)
	if err != nil {
		t.Fatal(err)
	}
	types, err := newSourceTypeElaborator(normalized, nil)
	if err != nil {
		t.Fatal(err)
	}
	summary, err := types.interfaceType("summary")
	if err != nil {
		t.Fatal(err)
	}
	full, err := types.interfaceType("full")
	if err != nil {
		t.Fatal(err)
	}
	isSubtype, err := gorapide.IsRapideSubtype(full, summary)
	if err != nil {
		t.Fatal(err)
	}
	if !isSubtype {
		t.Fatal("record with an extra field and covariantly narrower shared field is not a subtype")
	}
	isSubtype, err = gorapide.IsRapideSubtype(summary, full)
	if err != nil {
		t.Fatal(err)
	}
	if isSubtype {
		t.Fatal("record width subtyping was incorrectly treated as symmetric")
	}
}

func TestRecordDerivationResolvesNonNominalAliasChains(t *testing.T) {
	aliasedSource := []byte(`
type Employee_Alias is Employee;
type Public_Employee is Employee_Alias;
type Employee is record Name : String; end record Employee;
type Manager is record include Public_Employee; Salary : Integer; end record Manager;
architecture System() is manager : Manager; end architecture System;
`)
	directSource := []byte(`
type Employee is record Name : String; end record Employee;
type Manager is record include Employee; Salary : Integer; end record Manager;
architecture System() is manager : Manager; end architecture System;
`)
	aliased, err := Compile(aliasedSource, "System")
	if err != nil {
		t.Fatal(err)
	}
	direct, err := Compile(directSource, "System")
	if err != nil {
		t.Fatal(err)
	}
	aliasedDigest, err := aliased.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	directDigest, err := direct.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if aliasedDigest != directDigest {
		t.Fatalf("Record derivation alias introduced model identity: %s != %s", aliasedDigest, directDigest)
	}
}

func TestRecordDerivationAliasesAreStableAcrossOrderCaseAndGOMAXPROCS(t *testing.T) {
	sources := [][]byte{
		[]byte(`
type Employee_Alias is Employee;
type Public_Employee is Employee_Alias;
type Employee is record Name : String; end record Employee;
type Manager is record include Public_Employee; Salary : Integer; end record Manager;
architecture System() is manager : Manager; end architecture System;
`),
		[]byte(`
type Manager is record Salary : integer; include PUBLIC_EMPLOYEE; end record Manager;
type Public_Employee is employee_alias;
type Employee is record NAME : string; end record Employee;
type Employee_Alias is employee;
architecture System() is manager : manager; end architecture System;
`),
		[]byte(`
type Employee is record Name : String; end record Employee;
type Manager is record Name : String; Salary : Integer; end record Manager;
architecture System() is manager : Manager; end architecture System;
`),
	}
	previous := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previous)
	var baseline string
	for iteration := 0; iteration < 30; iteration++ {
		if iteration == 15 {
			runtime.GOMAXPROCS(8)
		}
		model, err := Compile(sources[iteration%len(sources)], "System")
		if err != nil {
			t.Fatal(err)
		}
		digest, err := model.DeterministicModelDigest()
		if err != nil {
			t.Fatal(err)
		}
		if baseline == "" {
			baseline = digest
		} else if digest != baseline {
			t.Fatalf("alias order/case/GOMAXPROCS changed Record derivation: %s != %s", baseline, digest)
		}
	}
}

func TestInterfaceDerivationResolvesNonNominalAlias(t *testing.T) {
	aliasedSource := []byte(`
type API_Alias is API;
type API is interface action out Ready(value : Integer); end interface API;
type Derived is include API_Alias; interface end interface Derived;
architecture System() is api : Derived; end architecture System;
`)
	directSource := []byte(`
type API is interface action out Ready(value : Integer); end interface API;
type Derived is include API; interface end interface Derived;
architecture System() is api : Derived; end architecture System;
`)
	aliased, err := Compile(aliasedSource, "System")
	if err != nil {
		t.Fatal(err)
	}
	direct, err := Compile(directSource, "System")
	if err != nil {
		t.Fatal(err)
	}
	aliasedDigest, err := aliased.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	directDigest, err := direct.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if aliasedDigest != directDigest {
		t.Fatalf("interface derivation alias introduced model identity: %s != %s", aliasedDigest, directDigest)
	}
}

func TestRecursiveRecordTypeUsesNameFreeStructuralIdentity(t *testing.T) {
	declarations := mustParseInterfaceDeclarations(t, `
type Node is record Value : Integer; Next : Node; end record Node;
`)
	normalized, err := normalizeInterfaceDeclarations(declarations)
	if err != nil {
		t.Fatal(err)
	}
	types, err := newSourceTypeElaborator(normalized, nil)
	if err != nil {
		t.Fatal(err)
	}
	node, err := types.interfaceType("node")
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := node.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	want := `{"format":"gorapide.rapide-type.v2","type":{"kind":"recursive_interface","members":[` +
		`{"region":"provides","name":"next","type":{"kind":"recursive_reference","depth":0}},` +
		`{"region":"provides","name":"value","type":{"kind":"predefined","name":"Integer"}}]}}`
	if string(encoded) != want {
		t.Fatalf("recursive record descriptor:\n%s\nwant:\n%s", encoded, want)
	}
	if strings.Contains(string(encoded), "Node") || strings.Contains(string(encoded), "node") {
		t.Fatalf("record declaration name leaked into structural identity: %s", encoded)
	}
}

func TestRecordTypeModelIsDeterministicAcrossOrderCaseAndGOMAXPROCS(t *testing.T) {
	sources := [][]byte{
		[]byte(`
type Person is record Name : String; Salary : Integer; end record Person;
architecture System() is person : Person; end architecture System;
`),
		[]byte(`
type Person is record SALARY : integer; NAME : string; end record Person;
architecture System() is person : person; end architecture System;
`),
	}
	previous := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previous)
	var baseline string
	for iteration := 0; iteration < 20; iteration++ {
		if iteration == 10 {
			runtime.GOMAXPROCS(8)
		}
		model, err := Compile(sources[iteration%2], "System")
		if err != nil {
			t.Fatal(err)
		}
		digest, err := model.DeterministicModelDigest()
		if err != nil {
			t.Fatal(err)
		}
		if baseline == "" {
			baseline = digest
		} else if digest != baseline {
			t.Fatalf("field order, case, or GOMAXPROCS changed record model: %s != %s", baseline, digest)
		}
	}
}

func TestRecordTypeRejectsNonRecordDerivation(t *testing.T) {
	tests := []struct {
		name   string
		source string
	}{
		{
			name: "direct",
			source: `
type Base is interface Name : String; end interface Base;
type Bad is record include Base; end record Bad;
architecture System() is end architecture System;
			`,
		},
		{
			name: "through alias",
			source: `
type Base_Alias is Base;
type Base is interface Name : String; end interface Base;
type Bad is record include Base_Alias; end record Bad;
architecture System() is end architecture System;
			`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Compile([]byte(test.source), "System")
			if err == nil || !strings.Contains(err.Error(), `record type "Bad" derives from non-record interface type "Base"`) {
				t.Fatalf("got %v, want explicit non-record derivation error", err)
			}
		})
	}
}

func TestDerivationAliasesRejectCyclesAndNonNamedInterfaceTypes(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{
			name: "alias cycle",
			source: `
type Left is Right; type Right is Left;
type Bad is interface include Left; end interface Bad;
architecture System() is end architecture System;
`,
			want: "interface derivation type alias cycle Left -> Right -> Left",
		},
		{
			name: "structural application",
			source: `
type Base is record Name : String; end record Base;
type Applied is Ref(Base);
type Bad is record include Applied; end record Bad;
architecture System() is end architecture System;
`,
			want: `interface derivation source alias "Applied" denotes structural application Ref(Base), not a named interface type`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Compile([]byte(test.source), "System")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v, want error containing %q", err, test.want)
			}
		})
	}
}

func TestRecordTypeRejectsDuplicateFields(t *testing.T) {
	tests := []struct {
		name   string
		source string
	}{
		{
			name: "direct",
			source: `
type Bad is record Name : String; name : Integer; end record Bad;
architecture System() is bad : Bad; end architecture System;
`,
		},
		{
			name: "inherited",
			source: `
type Base is record Name : String; end record Base;
type Bad is record include Base; NAME : Integer; end record Bad;
architecture System() is bad : Bad; end architecture System;
`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Compile([]byte(test.source), "System")
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), "duplicate record field") {
				t.Fatalf("got %v, want deterministic duplicate-field error", err)
			}
		})
	}
}

func TestSourceRecordStructuralTypeFormsRemainExplicitGates(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{
			name:   "function field",
			source: `type Bad is record Compute : function() return Integer; end record Bad;`,
			want:   "functions and module generators are not record fields",
		},
		{
			name:   "anonymous nested record type",
			source: `type Wrapper is Ref(record);`,
			want:   "rich type-expression form record is outside the current closed name/application subset",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Parse([]byte(test.source))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v, want error containing %q", err, test.want)
			}
		})
	}
}

func mustCompiledComponentRapideType(t *testing.T, model *arch.Architecture, name string) gorapide.RapideType {
	t.Helper()
	component, ok := model.Component(name)
	if !ok {
		t.Fatalf("compiled component %q is absent", name)
	}
	typ, ok := component.Interface.StructuralRapideType()
	if !ok {
		t.Fatalf("compiled component %q has no structural Rapide type", name)
	}
	return typ
}

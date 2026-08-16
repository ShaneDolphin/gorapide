package rapide

import (
	"bytes"
	"runtime"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide"
)

func TestParseStanfordEnumerationTypeLiterals(t *testing.T) {
	file, err := Parse([]byte(`
type Traffic_Lights is enum Red, Yellow, Green end enum Traffic_Lights;
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(file.Enumerations) != 1 || len(file.Unions) != 0 ||
		len(file.Interfaces) != 0 || len(file.TypeAliases) != 0 {
		t.Fatalf("Enumeration parsed into the wrong declaration namespace: %#v", file)
	}
	enumeration := file.Enumerations[0]
	if enumeration.Name != "Traffic_Lights" || len(enumeration.Literals) != 3 ||
		enumeration.Literals[0].Name != "Red" || enumeration.Literals[1].Name != "Yellow" ||
		enumeration.Literals[2].Name != "Green" {
		t.Fatalf("Enumeration AST=%#v", enumeration)
	}
}

func TestSourceEnumerationAttachesExactUnionOfTrivReduction(t *testing.T) {
	source := []byte(`
type Traffic_Lights is enum Red, Yellow, Green end enum Traffic_Lights;
type Schema is interface type Signal is Traffic_Lights; end interface Schema;
architecture System() is schema : Schema; end architecture System;
`)
	model, err := Compile(source, "System")
	if err != nil {
		t.Fatal(err)
	}
	got := mustCompiledComponentRapideType(t, model, "schema")
	enumeration, err := gorapide.NewRapideEnumerationType("Red", "Yellow", "Green")
	if err != nil {
		t.Fatal(err)
	}
	want := mustRootRapideInterfaceType(t,
		gorapide.ExactProvidedRapideTypeName("Signal", enumeration),
	)
	gotBytes, err := got.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	wantBytes, err := want.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotBytes, wantBytes) {
		t.Fatalf("source Enumeration reduction:\n%s\nwant:\n%s", gotBytes, wantBytes)
	}
}

func TestSourceEnumerationSubtypingUsesLiteralSetInclusion(t *testing.T) {
	file, err := Parse([]byte(`
type General is enum Red, Yellow, Green end enum General;
type Special is enum Red, Green end enum Special;
`))
	if err != nil {
		t.Fatal(err)
	}
	interfaces, err := normalizeInterfaceDeclarationsWithAliases(file.Interfaces, file.TypeAliases)
	if err != nil {
		t.Fatal(err)
	}
	types, err := newSourceTypeElaboratorWithUnionsAndEnumerations(
		interfaces, file.TypeAliases, file.Unions, file.Enumerations,
	)
	if err != nil {
		t.Fatal(err)
	}
	general, err := types.resolveNamed(Position{Line: 1, Column: 1}, "General", nil)
	if err != nil {
		t.Fatal(err)
	}
	special, err := types.resolveNamed(Position{Line: 1, Column: 1}, "Special", nil)
	if err != nil {
		t.Fatal(err)
	}
	if subtype, err := gorapide.IsRapideSubtype(special, general); err != nil || !subtype {
		t.Fatalf("source Enumeration subtype = %v, %v", subtype, err)
	}
	if subtype, err := gorapide.IsRapideSubtype(general, special); err != nil || subtype {
		t.Fatalf("reversed source Enumeration subtype = %v, %v", subtype, err)
	}
}

func TestSourceEnumerationIsCanonicalAcrossAliasOrderCaseAndGOMAXPROCS(t *testing.T) {
	sources := [][]byte{
		[]byte(`
type Traffic is enum Red, Yellow, Green end enum Traffic;
type Schema is interface type Signal is Traffic; end interface Schema;
architecture System() is schema : Schema; end architecture System;
`),
		[]byte(`
type Alias is traffic;
type Schema is interface type SIGNAL is alias; end interface Schema;
type Traffic is enum GREEN, red, YELLOW end enum traffic;
architecture System() is schema : schema; end architecture System;
`),
	}
	previous := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previous)
	var baseline string
	for iteration := 0; iteration < 30; iteration++ {
		if iteration == 15 {
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
			t.Fatalf("Enumeration alias/order/case/GOMAXPROCS changed model: %s != %s", baseline, digest)
		}
	}
}

func TestSourceEnumerationRejectsMalformedAndCollidingDeclarations(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{
			name:   "empty",
			source: `type Empty is enum end enum Empty;`,
			want:   "an Enumeration type requires at least one literal identifier",
		},
		{
			name:   "duplicate literal",
			source: `type Bad is enum Red, red end enum Bad;`,
			want:   `Enumeration literal "red" is declared more than once`,
		},
		{
			name: "predefined collision",
			source: `type Integer is enum One end enum Integer;
architecture System() is end architecture System;`,
			want: `Enumeration type "Integer" collides with predefined type "Integer"`,
		},
		{
			name: "Union collision",
			source: `type Choice is union Value : Triv; end union Choice;
type choice is enum Value end enum choice;
architecture System() is end architecture System;`,
			want: `Enumeration type "choice" collides with Union type "Choice"`,
		},
		{
			name:   "anonymous nested Enumeration",
			source: `type Wrapper is Ref(enum Red, Green end enum);`,
			want:   "rich type-expression form enum is outside the current closed name/application subset",
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

func TestEnumerationRuntimeOperationsRemainExplicitGates(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{
			name:   "tag query",
			source: `architecture System(Value : Integer is Tagof(Item)) is end architecture System;`,
			want:   "Union tag extraction requires first-class tagged values and Enumeration types",
		},
		{
			name:   "anonymous Enumeration",
			source: `type Wrapper is Ref(enum Red, Green end enum);`,
			want:   "rich type-expression form enum is outside the current closed name/application subset",
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

func TestEnumerationLiteralVisibilityRuleIsNotInvented(t *testing.T) {
	tests := []struct {
		name       string
		expression string
		want       string
	}{
		{
			name:       "unqualified implicit declaration",
			expression: "Red",
			want:       `behavior expression name "Red" is not declared in this body`,
		},
		{
			name:       "qualified contextual literal",
			expression: "Traffic'(Red)",
			want:       `qualified-expression type "Traffic" is outside the direct predefined-scalar subset`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := `
type Traffic is enum Red, Yellow, Green end enum Traffic;
type API is interface
  provides F : function() return Integer;
  behavior
    F : function() return Integer is begin return ` + test.expression + `; end function F;
  begin
end interface API;
architecture System() is api : API; end architecture System;
`
			_, err := Compile([]byte(source), "System")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v, want error containing %q", err, test.want)
			}
		})
	}
}

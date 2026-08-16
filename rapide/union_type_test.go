package rapide

import (
	"bytes"
	"runtime"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide"
)

func TestParseStanfordUnionTypeTags(t *testing.T) {
	file, err := Parse([]byte(`
type Choice is union
  Int : Integer;
  False_Value, True_Value : Boolean;
end union Choice;
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(file.Unions) != 1 || len(file.Interfaces) != 0 || len(file.TypeAliases) != 0 {
		t.Fatalf("Union parsed into the wrong declaration namespace: %#v", file)
	}
	union := file.Unions[0]
	if union.Name != "Choice" || len(union.Tags) != 3 ||
		union.Tags[0].Name != "Int" || union.Tags[0].Type != "Integer" ||
		union.Tags[1].Name != "False_Value" || union.Tags[1].Type != "Boolean" ||
		union.Tags[2].Name != "True_Value" || union.Tags[2].Type != "Boolean" {
		t.Fatalf("Union AST=%#v", union)
	}
}

func TestSourceUnionAttachesExactPublishedFunctionReduction(t *testing.T) {
	source := []byte(`
type Choice is union Int : Integer; Bool : Boolean; end union Choice;
type Schema is interface type Selection is Choice; end interface Schema;
architecture System() is schema : Schema; end architecture System;
`)
	model, err := Compile(source, "System")
	if err != nil {
		t.Fatal(err)
	}
	got := mustCompiledComponentRapideType(t, model, "schema")
	integer := mustRootRapidePredefinedType(t, "Integer")
	boolean := mustRootRapidePredefinedType(t, "Boolean")
	union, err := gorapide.NewRapideUnionType(
		gorapide.RapideUnionTag("Int", integer),
		gorapide.RapideUnionTag("Bool", boolean),
	)
	if err != nil {
		t.Fatal(err)
	}
	want := mustRootRapideInterfaceType(t,
		gorapide.ExactProvidedRapideTypeName("Selection", union),
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
		t.Fatalf("source Union reduction:\n%s\nwant:\n%s", gotBytes, wantBytes)
	}
}

func TestSourceUnionSubtypingUsesTagSubsetAndMemberCovariance(t *testing.T) {
	file, err := Parse([]byte(`
type Employee is interface Name : String; end interface Employee;
type Manager is interface Name : String; Department : String; end interface Manager;
type General is union Person : Employee; Enabled : Boolean; end union General;
type Special is union Person : Manager; end union Special;
`))
	if err != nil {
		t.Fatal(err)
	}
	interfaces, err := normalizeInterfaceDeclarationsWithAliases(file.Interfaces, file.TypeAliases)
	if err != nil {
		t.Fatal(err)
	}
	types, err := newSourceTypeElaboratorWithUnions(interfaces, file.TypeAliases, file.Unions)
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
		t.Fatalf("source Union subtype = %v, %v", subtype, err)
	}
	if subtype, err := gorapide.IsRapideSubtype(general, special); err != nil || subtype {
		t.Fatalf("reversed source Union subtype = %v, %v", subtype, err)
	}
}

func TestSourceUnionIsCanonicalAcrossAliasOrderCaseAndGOMAXPROCS(t *testing.T) {
	sources := [][]byte{
		[]byte(`
type Choice is union Int : Integer; Bool : Boolean; end union Choice;
type Schema is interface type Selection is Choice; end interface Schema;
architecture System() is schema : Schema; end architecture System;
`),
		[]byte(`
type Alias is choice;
type Schema is interface type SELECTION is alias; end interface Schema;
type Choice is union BOOL : boolean; INT : integer; end union Choice;
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
			t.Fatalf("Union alias/order/case/GOMAXPROCS changed model: %s != %s", baseline, digest)
		}
	}
}

func TestSourceUnionRejectsMalformedNamespacesTagsAndRecursion(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{
			name:   "empty",
			source: `type Empty is union end union Empty;`,
			want:   "a Union type requires at least one tag/member declaration",
		},
		{
			name:   "duplicate tag",
			source: `type Bad is union Value : Integer; value : Boolean; end union Bad;`,
			want:   `Union tag "value" is declared more than once`,
		},
		{
			name:   "rich member type",
			source: `type Bad is union Apply : function() return Integer; end union Bad;`,
			want:   "rich type-expression form function is outside the current closed name/application subset",
		},
		{
			name: "unknown member type",
			source: `type Bad is union Value : Missing; end union Bad;
architecture System() is end architecture System;`,
			want: `type "Missing" is outside the current deterministic Rapide type-expression subset`,
		},
		{
			name: "recursive Union",
			source: `type Tree is union Empty : Triv; Node : Branch; end union Tree;
type Branch is record Left : Tree; Right : Tree; end record Branch;
architecture System() is end architecture System;`,
			want: "recursive Union/function type cycle Tree -> Branch -> Tree requires general recursive function-type binders",
		},
		{
			name: `predefined collision`,
			source: `type Integer is union Value : String; end union Integer;
architecture System() is end architecture System;`,
			want: `Union type "Integer" collides with predefined type "Integer"`,
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

func TestUnionRuntimeObjectFormsFailExplicitly(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{
			name:   "literal",
			source: `architecture System(Value : Integer is (Int, 1)) is end architecture System;`,
			want:   "Union literals require contextual tagged-value typing",
		},
		{
			name:   "discrimination",
			source: `architecture System(Value : Integer is case(Item, Int is 1)) is end architecture System;`,
			want:   "Union discrimination requires first-class tagged values",
		},
		{
			name:   "tag extraction",
			source: `architecture System(Value : Integer is Tagof(Item)) is end architecture System;`,
			want:   "Union tag extraction requires first-class tagged values and Enumeration types",
		},
		{
			name:   "anonymous nested Union type",
			source: `type Wrapper is Ref(union);`,
			want:   "rich type-expression form union is outside the current closed name/application subset",
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

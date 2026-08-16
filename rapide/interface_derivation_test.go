package rapide

import (
	"runtime"
	"sort"
	"strings"
	"testing"
)

func TestParseStanfordInterfaceDerivationScopesAndModifiers(t *testing.T) {
	file, err := Parse([]byte(`
type Derived is
  include Base only (Input, Compute) replace (Input to Start, Compute to Run);
interface
  include Base except (Lookup);
provides
  include Base only (Compute);
requires
  include Base only (Lookup) replace (Lookup to Fetch);
end interface Derived;
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(file.Interfaces) != 1 || len(file.Interfaces[0].Derivations) != 4 {
		t.Fatalf("derivation AST=%#v", file.Interfaces)
	}
	derivations := file.Interfaces[0].Derivations
	if derivations[0].Region != InterfaceDerivationAll || derivations[0].Modifier != InterfaceDerivationOnly ||
		strings.Join(derivations[0].Names, ",") != "Input,Compute" || len(derivations[0].Replacements) != 2 {
		t.Fatalf("full derivation=%#v", derivations[0])
	}
	if derivations[1].Region != InterfaceDerivationProvides || derivations[1].Modifier != InterfaceDerivationExcept {
		t.Fatalf("default provides derivation=%#v", derivations[1])
	}
	if derivations[2].Region != InterfaceDerivationProvides || derivations[2].Modifier != InterfaceDerivationOnly {
		t.Fatalf("explicit provides derivation=%#v", derivations[2])
	}
	if derivations[3].Region != InterfaceDerivationRequires || derivations[3].Modifier != InterfaceDerivationOnly ||
		len(derivations[3].Replacements) != 1 || derivations[3].Replacements[0].To != "Fetch" {
		t.Fatalf("requires derivation=%#v", derivations[3])
	}
}

func TestNormalizeStanfordInterfaceDerivationRegionsOnlyExceptAndReplace(t *testing.T) {
	declarations := mustParseInterfaceDeclarations(t, `
type Base is interface
  action in Input(value : Integer);
  action out Output(value : Integer);
  private action Hidden(value : Integer);
  provides Compute : function(value : Integer) return Integer;
  requires Lookup : function(key : String) return Integer;
end interface Base;

type Full is include Base; interface end interface Full;
type DefaultProvides is interface include Base; end interface DefaultProvides;
type ExplicitProvides is interface provides include Base; end interface ExplicitProvides;
type ExplicitRequires is interface requires include Base; end interface ExplicitRequires;
type Selected is include Base only (Input, Compute); interface end interface Selected;
type Excluded is include Base except (Output, Lookup); interface end interface Excluded;
type Renamed is
  include Base replace (Input to Start, Output to Finish, Hidden to Secret, Compute to Run, Lookup to Fetch);
interface end interface Renamed;
`)
	normalized, err := normalizeInterfaceDeclarations(declarations)
	if err != nil {
		t.Fatal(err)
	}
	assertInterfaceNames(t, normalized["full"],
		[]string{"in:Input", "out:Output", "private:Hidden"},
		[]string{"provides:Compute", "requires:Lookup"})
	assertInterfaceNames(t, normalized["defaultprovides"], nil, []string{"provides:Compute"})
	assertInterfaceNames(t, normalized["explicitprovides"], nil, []string{"provides:Compute"})
	assertInterfaceNames(t, normalized["explicitrequires"], nil, []string{"requires:Lookup"})
	assertInterfaceNames(t, normalized["selected"], []string{"in:Input"}, []string{"provides:Compute"})
	assertInterfaceNames(t, normalized["excluded"],
		[]string{"in:Input", "private:Hidden"}, []string{"provides:Compute"})
	assertInterfaceNames(t, normalized["renamed"],
		[]string{"in:Start", "out:Finish", "private:Secret"},
		[]string{"provides:Run", "requires:Fetch"})
	for name, declaration := range normalized {
		if len(declaration.Derivations) != 0 {
			t.Fatalf("normalized interface %s retained derivations: %#v", name, declaration.Derivations)
		}
	}
}

func TestInterfaceDerivationCopiesNameDeclarationsButNotBehaviorOrConstraints(t *testing.T) {
	base := InterfaceDecl{
		Position: Position{Line: 1, Column: 1}, Name: "Base",
		Actions:     []ActionDecl{{Position: Position{Line: 1, Column: 1}, Mode: ActionOut, Name: "A"}},
		Behavior:    &BehaviorDecl{Position: Position{Line: 2, Column: 1}},
		Constraints: []ConstraintDecl{{Position: Position{Line: 3, Column: 1}}},
	}
	derived := InterfaceDecl{
		Position: Position{Line: 4, Column: 1}, Name: "Derived",
		Derivations: []InterfaceDerivationDecl{{
			Position: Position{Line: 4, Column: 17}, Source: "Base", Region: InterfaceDerivationAll,
		}},
	}
	normalized, err := normalizeInterfaceDeclarations([]InterfaceDecl{derived, base})
	if err != nil {
		t.Fatal(err)
	}
	got := normalized["derived"]
	if len(got.Actions) != 1 || got.Behavior != nil || len(got.Constraints) != 0 {
		t.Fatalf("derived interface copied non-name semantic parts: %#v", got)
	}
}

func TestInterfaceDerivationMatchesExplicitFlattening(t *testing.T) {
	derivedSource := []byte(`
type Base is interface
  action in Input(value : Integer);
  action out Output(value : Integer);
  private action Hidden(value : Integer);
  provides Compute : function(value : Integer) return Integer;
  requires Lookup : function(key : String) return Integer;
end interface Base;
type Derived is
  include Base replace (Input to Start, Output to Finish, Hidden to Secret, Compute to Run, Lookup to Fetch);
interface end interface Derived;
architecture System() is worker : Derived; end architecture System;
`)
	flatSource := []byte(`
type Derived is interface
  action in Start(value : Integer);
  action out Finish(value : Integer);
  private action Secret(value : Integer);
  provides Run : function(value : Integer) return Integer;
  requires Fetch : function(key : String) return Integer;
end interface Derived;
architecture System() is worker : Derived; end architecture System;
`)
	derived, err := Compile(derivedSource, "System")
	if err != nil {
		t.Fatal(err)
	}
	flat, err := Compile(flatSource, "System")
	if err != nil {
		t.Fatal(err)
	}
	derivedDigest, err := derived.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	flatDigest, err := flat.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if derivedDigest != flatDigest {
		t.Fatalf("derived model %s != explicitly normalized model %s", derivedDigest, flatDigest)
	}
}

func TestInterfaceDerivationDeclarationAndIncludeOrderAreNotSemantic(t *testing.T) {
	build := func(typeOrder, includeOrder string) []byte {
		return []byte(typeOrder + `
type Combined is ` + includeOrder + ` interface end interface Combined;
architecture System() is component : Combined; end architecture System;
`)
	}
	a := "type Alpha is interface action out A(value : Integer); provides F : function(); end interface Alpha;\n"
	b := "type Beta is interface action in B(value : Integer); requires G : function() return Integer; end interface Beta;\n"
	sources := [][]byte{
		build(a+b, "include Alpha; include Beta;"),
		build(b+a, "include Alpha; include Beta;"),
		build(b+a, "include Beta; include Alpha;"),
	}
	previous := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previous)
	var want string
	for iteration := 0; iteration < 18; iteration++ {
		if iteration == 9 {
			runtime.GOMAXPROCS(4)
		}
		model, err := Compile(sources[iteration%len(sources)], "System")
		if err != nil {
			t.Fatal(err)
		}
		digest, err := model.DeterministicModelDigest()
		if err != nil {
			t.Fatal(err)
		}
		if want == "" {
			want = digest
		} else if digest != want {
			t.Fatalf("declaration/include order or GOMAXPROCS changed digest: %s != %s", digest, want)
		}
	}
}

func TestInterfaceDerivationRejectsCyclesUnknownSourcesAndCollisions(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{
			name: "unknown source",
			source: `type A is include Missing; interface end interface A;
architecture System() is a : A; end architecture System;`,
			want: `derives from undeclared interface type "Missing"`,
		},
		{
			name: "direct cycle",
			source: `type A is include A; interface end interface A;
architecture System() is a : A; end architecture System;`,
			want: "interface derivation cycle A -> A",
		},
		{
			name: "indirect cycle",
			source: `type B is include C; interface end interface B;
type C is include A; interface end interface C;
type A is include B; interface end interface A;
architecture System() is a : A; end architecture System;`,
			want: "interface derivation cycle A -> B -> C -> A",
		},
		{
			name: "copied name collision",
			source: `type Base is interface action out A(); end interface Base;
type Derived is include Base; interface action out A(); end interface Derived;
architecture System() is d : Derived; end architecture System;`,
			want: `duplicate action "A" in interface "Derived"`,
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

func TestInterfaceDerivationRejectsMalformedSourceDeterministically(t *testing.T) {
	tests := []struct {
		name, derivation, want string
	}{
		{name: "empty only", derivation: "include Base only ();", want: "expected interface derivation name"},
		{name: "duplicate selection", derivation: "include Base only (A, a);", want: `interface derivation name "a" is named more than once`},
		{name: "repeated modifier", derivation: "include Base only (A) except (B);", want: "modifiers must occur at most once"},
		{name: "duplicate replacement", derivation: "include Base replace (A to B, a to C);", want: `replacement source "a" is named more than once`},
		{name: "rich source type", derivation: "include Base and Other;", want: "requires a named interface"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := []byte("type Base is interface action out A(); end interface Base; type D is " +
				test.derivation + " interface end interface D;")
			_, err := Parse(source)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v, want syntax error containing %q", err, test.want)
			}
		})
	}
	for name, source := range map[string]string{
		"action part": `
type Base is interface action out A(); end interface Base;
type D is interface action out B(); include Base; end interface D;`,
	} {
		t.Run(name+" include", func(t *testing.T) {
			_, err := Parse([]byte(source))
			if err == nil || !strings.Contains(err.Error(), "interface derivation is not permitted") {
				t.Fatalf("got %v", err)
			}
		})
	}
}

func TestInterfaceDerivationNormalizerRejectsMalformedManualAST(t *testing.T) {
	base := InterfaceDecl{Position: Position{Line: 1, Column: 1}, Name: "Base"}
	tests := []struct {
		name       string
		derivation InterfaceDerivationDecl
		want       string
	}{
		{
			name: "empty only list",
			derivation: InterfaceDerivationDecl{Position: Position{Line: 2, Column: 1}, Source: "Base",
				Region: InterfaceDerivationAll, Modifier: InterfaceDerivationOnly},
			want: "empty only identifier list",
		},
		{
			name: "empty replacement target",
			derivation: InterfaceDerivationDecl{Position: Position{Line: 2, Column: 1}, Source: "Base",
				Region:       InterfaceDerivationAll,
				Replacements: []InterfaceReplacementDecl{{Position: Position{Line: 2, Column: 14}, From: "A"}}},
			want: "has an empty name",
		},
		{
			name: "duplicate unmatched replacement",
			derivation: InterfaceDerivationDecl{Position: Position{Line: 2, Column: 1}, Source: "Base",
				Region: InterfaceDerivationAll,
				Replacements: []InterfaceReplacementDecl{
					{Position: Position{Line: 2, Column: 14}, From: "Missing", To: "A"},
					{Position: Position{Line: 2, Column: 28}, From: "missing", To: "B"},
				}},
			want: "repeated source",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			derived := InterfaceDecl{Position: Position{Line: 2, Column: 1}, Name: "Derived",
				Derivations: []InterfaceDerivationDecl{test.derivation}}
			_, err := normalizeInterfaceDeclarations([]InterfaceDecl{base, derived})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v, want error containing %q", err, test.want)
			}
		})
	}
}

func mustParseInterfaceDeclarations(t *testing.T, source string) []InterfaceDecl {
	t.Helper()
	file, err := Parse([]byte(source))
	if err != nil {
		t.Fatal(err)
	}
	return file.Interfaces
}

func assertInterfaceNames(t *testing.T, declaration InterfaceDecl, wantActions, wantFunctions []string) {
	t.Helper()
	actions := make([]string, 0, len(declaration.Actions))
	for _, action := range declaration.Actions {
		actions = append(actions, string(action.Mode)+":"+action.Name)
	}
	functions := make([]string, 0, len(declaration.Functions))
	for _, function := range declaration.Functions {
		functions = append(functions, string(function.Mode)+":"+function.Name)
	}
	sort.Strings(actions)
	sort.Strings(functions)
	sort.Strings(wantActions)
	sort.Strings(wantFunctions)
	if strings.Join(actions, ",") != strings.Join(wantActions, ",") ||
		strings.Join(functions, ",") != strings.Join(wantFunctions, ",") {
		t.Fatalf("interface %s actions=%v functions=%v, want actions=%v functions=%v",
			declaration.Name, actions, functions, wantActions, wantFunctions)
	}
}

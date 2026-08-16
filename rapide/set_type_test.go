package rapide

import (
	"bytes"
	"runtime"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide"
)

func TestSourceSetApplicationAttachesPublishedRecursiveInterface(t *testing.T) {
	source := []byte(`
type Integer_Set is Set(Integer);
type Schema is interface type Values is Integer_Set; end interface Schema;
architecture System() is schema : Schema; end architecture System;
`)
	model, err := Compile(source, "System")
	if err != nil {
		t.Fatal(err)
	}
	got := mustCompiledComponentRapideType(t, model, "schema")
	integer := mustRootRapidePredefinedType(t, "Integer")
	setType, err := gorapide.RapideSetType(integer)
	if err != nil {
		t.Fatal(err)
	}
	want := mustRootRapideInterfaceType(t,
		gorapide.ExactProvidedRapideTypeName("Values", setType),
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
		t.Fatalf("source Set application:\n%s\nwant:\n%s", gotBytes, wantBytes)
	}
}

func TestSourceSetEnforcesEqualityElementConstraint(t *testing.T) {
	source := []byte(`
type Employee is interface Name : String; end interface Employee;
type Employee_Set is Set(Employee);
architecture System() is end architecture System;
`)
	_, err := Compile(source, "System")
	if err == nil || !strings.Contains(err.Error(), "Set element type does not subtype Equality(element)") {
		t.Fatalf("non-equatable source Set element error = %v", err)
	}
}

func TestSourceSetIsCanonicalAcrossAliasesCaseAndGOMAXPROCS(t *testing.T) {
	sources := [][]byte{
		[]byte(`
type Integer_Set is Set(Integer);
type Schema is interface type Values is Integer_Set; end interface Schema;
architecture System() is schema : Schema; end architecture System;
`),
		[]byte(`
type Alias is SET(integer);
type Schema is interface type VALUES is alias; end interface Schema;
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
			t.Fatalf("Set alias/case/GOMAXPROCS changed model: %s != %s", baseline, digest)
		}
	}
}

func TestSourceSetRejectsWrongArityAndRuntimeValueForms(t *testing.T) {
	tests := []struct {
		name   string
		source string
		parse  bool
		want   string
	}{
		{
			name:   "wrong arity",
			source: `type Bad is Set(Integer, String); architecture System() is end architecture System;`,
			want:   "has 2 arguments, want 1",
		},
		{
			name:   "empty set function",
			source: `architecture System(Value : Integer is Empty_Set(Integer)) is end architecture System;`,
			parse:  true,
			want:   "Empty_Set requires first-class Set values outside the current expression subset",
		},
		{
			name:   "set literal",
			source: `architecture System(Value : Integer is {}) is end architecture System;`,
			parse:  true,
			want:   "unexpected character '{'",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var err error
			if test.parse {
				_, err = Parse([]byte(test.source))
			} else {
				_, err = Compile([]byte(test.source), "System")
			}
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v, want error containing %q", err, test.want)
			}
		})
	}
}

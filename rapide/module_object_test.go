package rapide

import (
	"bytes"
	"runtime"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide/arch"
)

func TestParseModuleObjectDeclarationNormalizesIdentifierList(t *testing.T) {
	file, err := Parse([]byte(`
type API is interface A, B : Integer; end interface API;
module Impl() return API is A, B : Integer is 1 + 2; end module Impl;
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(file.Modules) != 1 || len(file.Modules[0].Objects) != 2 ||
		file.Modules[0].Objects[0].Name != "A" || file.Modules[0].Objects[1].Name != "B" ||
		file.Modules[0].Objects[0].Type != "Integer" {
		t.Fatalf("module object AST=%#v", file.Modules)
	}
}

func TestModuleObjectsSatisfyMembershipAndAreExecutableConstants(t *testing.T) {
	source := []byte(`
type API is interface
  Limit : Integer;
  action out Ready(value : Integer);
private
  Enabled : Boolean;
end interface API;
module Impl() return API is
  Limit : Integer is 2 + 3;
  Enabled : Boolean is true;
initial
  if Enabled then Ready(Limit); end if;
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
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 10))
	if err != nil {
		t.Fatal(err)
	}
	ready := result.Poset.ByName("Ready")
	if len(ready) != 1 {
		t.Fatalf("Ready count=%d", len(ready))
	}
	if value, _ := ready[0].Param("value"); value != int64(5) {
		t.Fatalf("Ready value=%#v, want 5", value)
	}
	artifactDigest, err := result.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := model.ReplayDeterministic(arch.NewExecutionJournal(digest, 10), artifactDigest)
	if err != nil {
		t.Fatal(err)
	}
	first, _ := result.MarshalCanonical()
	second, _ := replayed.MarshalCanonical()
	if !bytes.Equal(first, second) {
		t.Fatal("module object execution did not replay byte-identically")
	}
}

func TestModuleObjectsResolveInFunctionsAndProcesses(t *testing.T) {
	source := []byte(`
type Driver is interface action out Start(); end interface Driver;
type API is interface
  Increment : Integer;
  Compute : function(value : Integer) return Integer;
  action in Start();
  action out Ready(value : Integer);
end interface API;
module Impl() return API is
  Increment : Integer is 2;
  answer : var Integer := 0;
  Compute : function(value : Integer) return Integer is
  begin return value + Increment; end function Compute;
serial
  when Start do
    answer := Compute(Increment);
    Ready($answer);
  end when;
end module Impl;
architecture System() is
  driver : Driver;
  api : API is Impl();
connect driver.Start => api.Start;
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
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 20,
		arch.InputEvent{Key: "start", Source: "driver", Action: "Start", Params: map[string]any{}}))
	if err != nil {
		t.Fatal(err)
	}
	ready := result.Poset.ByName("Ready")
	if len(ready) != 1 {
		t.Fatalf("Ready count=%d", len(ready))
	}
	if value, _ := ready[0].Param("value"); value != int64(4) {
		t.Fatalf("Ready value=%#v, want 4", value)
	}
}

func TestModuleObjectMayUseExactLocalTypeDenotation(t *testing.T) {
	source := []byte(`
type API is interface type Item; Default : Item; end interface API;
module Impl() return API is
  type Item is Integer;
  Default : Item is 7;
end module Impl;
architecture System() is api : API is Impl(); end architecture System;
`)
	model, err := Compile(source, "System")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := model.DeterministicModelDigest(); err != nil {
		t.Fatal(err)
	}
}

func TestModuleObjectValuesChangeCanonicalModelIdentity(t *testing.T) {
	build := func(value string) string {
		source := []byte(`
type API is interface Limit : Integer; end interface API;
module Impl() return API is Limit : Integer is ` + value + `; end module Impl;
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
		return digest
	}
	if build("1") == build("2") {
		t.Fatal("different module object values produced the same model identity")
	}
}

func TestModuleObjectMembershipRejectsMissingDuplicateAndInvalidInitializers(t *testing.T) {
	tests := []struct {
		name, body, want string
	}{
		{name: "missing", want: `does not supply object "limit"`},
		{name: "duplicate", body: `Limit : Integer is 1; limit : Integer is 2;`, want: `duplicate module object "limit"`},
		{name: "wrong type", body: `Limit : Integer is true;`, want: `initializer has type Boolean, want Integer`},
		{name: "open expression", body: `Limit : Integer is Missing;`, want: `expression name "Missing" is not declared`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := []byte(`type API is interface Limit : Integer; end interface API;
module Impl() return API is ` + test.body + ` end module Impl;
architecture System() is api : API is Impl(); end architecture System;`)
			_, err := Compile(source, "System")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v, want error containing %q", err, test.want)
			}
		})
	}
}

func TestModuleObjectLocalTypePreservesPredefinedMembership(t *testing.T) {
	source := []byte(`
type API is interface type Item; Default : Item; end interface API;
module Impl() return API is
  type Item is Natural;
  Default : Item is 0;
end module Impl;
architecture System() is api : API is Impl(); end architecture System;
`)
	model, err := Compile(source, "System")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := model.DeterministicModelDigest(); err != nil {
		t.Fatal(err)
	}
}

func TestModuleObjectNameConflictsFailCanonically(t *testing.T) {
	source := []byte(`
type API is interface Limit : Integer; end interface API;
module Impl() return API is
  type Limit is Integer;
  Limit : Integer is 1;
end module Impl;
architecture System() is api : API is Impl(); end architecture System;
`)
	_, err := Compile(source, "System")
	if err == nil || !strings.Contains(err.Error(),
		`module "Impl" has conflicting declarations: object limit conflicts with type limit`) {
		t.Fatalf("conflicting declaration error=%v", err)
	}
}

func TestModuleObjectsAreCanonicalAcrossOrderCaseAndGOMAXPROCS(t *testing.T) {
	sources := [][]byte{
		[]byte(`type API is interface A : Integer; B : Boolean; end interface API;
module Impl() return API is A : Integer is 1; B : Boolean is true; end module Impl;
architecture System() is api : API is Impl(); end architecture System;`),
		[]byte(`type API is interface B : boolean; A : integer; end interface API;
module IMPL() return API is b : BOOLEAN is TRUE; a : INTEGER is 1; end module impl;
architecture System() is api : API is impl(); end architecture System;`),
	}
	previous := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previous)
	var baseline string
	for iteration := 0; iteration < 20; iteration++ {
		if iteration == 10 {
			runtime.GOMAXPROCS(4)
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
			t.Fatalf("order/case/GOMAXPROCS changed object membership: %s != %s", baseline, digest)
		}
	}
}

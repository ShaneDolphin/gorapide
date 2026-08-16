package rapide

import (
	"bytes"
	"runtime"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide/arch"
)

func constrainedArchitectureGeneratorSource() []byte {
	return []byte(`
type RootBoundary is interface action out Root_Values(p : Positive; n : Natural); end interface RootBoundary;
type ChildBoundary is interface action out Child_Values(p : Positive; n : Natural); end interface ChildBoundary;
architecture Child(P : Positive is 1 + 1; N : Natural is 1 - 1) return ChildBoundary is
initial
  Child_Values(P, N);
end architecture Child;
architecture System(P : Positive is 1 + 1; N : Natural is 1 - 1) return RootBoundary is
  explicit_child : ChildBoundary is Child(1 + 1, 1 - 1);
  defaulted_child : ChildBoundary is Child();
initial
  Root_Values(P, N);
end architecture System;
`)
}

func TestArchitectureGeneratorFormalsUseConstrainedMembership(t *testing.T) {
	source := constrainedArchitectureGeneratorSource()
	implicit, err := Compile(source, "System")
	if err != nil {
		t.Fatal(err)
	}
	explicit, err := CompileWithArguments(source, "system", map[string]any{
		"p": int32(2), "N": int64(0),
	})
	if err != nil {
		t.Fatal(err)
	}
	implicitDigest, err := implicit.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	explicitDigest, err := explicit.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if implicitDigest != explicitDigest {
		t.Fatalf("constrained architecture defaults and explicit actuals differ: %s != %s", implicitDigest, explicitDigest)
	}

	previous := runtime.GOMAXPROCS(1)
	first, err := implicit.ExecuteDeterministic(arch.NewExecutionJournal(implicitDigest, 20))
	if err != nil {
		runtime.GOMAXPROCS(previous)
		t.Fatal(err)
	}
	runtime.GOMAXPROCS(8)
	second, err := explicit.ExecuteDeterministic(arch.NewExecutionJournal(explicitDigest, 20))
	runtime.GOMAXPROCS(previous)
	if err != nil {
		t.Fatal(err)
	}
	root := first.Poset.ByName("Root_Values")
	children := first.Poset.ByName("Child_Values")
	if len(root) != 1 || root[0].ParamInt("p") != 2 || root[0].ParamInt("n") != 0 {
		t.Fatalf("constrained root generator values=%#v", root)
	}
	if len(children) != 2 {
		t.Fatalf("constrained child generator values=%#v", children)
	}
	for _, event := range children {
		if event.ParamInt("p") != 2 || event.ParamInt("n") != 0 {
			t.Fatalf("constrained child generator event=%#v", event)
		}
	}
	firstBytes, err := first.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	secondBytes, err := second.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatal("argument spelling, integer width, or GOMAXPROCS changed constrained architecture artifact bytes")
	}
	artifactDigest, err := first.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := implicit.ReplayDeterministic(arch.NewExecutionJournal(implicitDigest, 20), artifactDigest)
	if err != nil {
		t.Fatal(err)
	}
	replayedBytes, err := replayed.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstBytes, replayedBytes) {
		t.Fatal("constrained architecture generator replay changed canonical artifact bytes")
	}
}

func TestModuleGeneratorFormalsUseConstrainedMembership(t *testing.T) {
	source := []byte(`
type Worker is interface action out Ready(p : Positive; n : Natural); end interface Worker;
module Store(P : Positive is 1 + 1; N : Natural is 1 - 1) return Worker is
initial Ready(P, N);
end module Store;
architecture System() is
  defaulted : Worker is Store();
  explicit : Worker is Store(1 + 1, 1 - 1);
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
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 20))
	if err != nil {
		t.Fatal(err)
	}
	ready := result.Poset.ByName("Ready")
	if len(ready) != 2 {
		t.Fatalf("constrained module-generator outputs=%#v", ready)
	}
	for _, event := range ready {
		if event.ParamInt("p") != 2 || event.ParamInt("n") != 0 {
			t.Fatalf("constrained module-generator event=%#v", event)
		}
	}
}

func TestGeneratorFormalsRejectValuesOutsideConstrainedType(t *testing.T) {
	for _, test := range []struct {
		name   string
		source []byte
		want   string
	}{
		{
			name:   "architecture Positive default zero",
			source: []byte(`architecture System(P : Positive is 0) is end architecture System;`),
			want:   "default has type Integer, want Positive",
		},
		{
			name: "child architecture Natural actual negative",
			source: []byte(`type Empty is interface end interface Empty;
architecture Child(N : Natural) return Empty is end architecture Child;
architecture System() is child : Empty is Child(0 - 1); end architecture System;`),
			want: "has type Integer, want Natural",
		},
		{
			name: "module Positive default zero",
			source: []byte(`type Empty is interface end interface Empty;
module M(P : Positive is 0) return Empty is end module M;
architecture System() is m : Empty is M(); end architecture System;`),
			want: "default has type Integer, want Positive",
		},
		{
			name: "module Natural actual negative",
			source: []byte(`type Empty is interface end interface Empty;
module M(N : Natural) return Empty is end module M;
architecture System() is m : Empty is M(0 - 1); end architecture System;`),
			want: "has type Integer, want Natural",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := Compile(test.source, "System")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("diagnostic=%v, want %q", err, test.want)
			}
		})
	}

	_, err := CompileWithArguments(
		[]byte(`architecture System(P : Positive) is end architecture System;`),
		"System", map[string]any{"P": 0},
	)
	if err == nil || !strings.Contains(err.Error(), `argument "P" does not match Positive`) {
		t.Fatalf("invalid explicit architecture argument diagnostic=%v", err)
	}
}

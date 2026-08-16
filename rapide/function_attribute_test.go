package rapide

import (
	"bytes"
	"runtime"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide/arch"
)

func functionAttributeSource(alternate bool) []byte {
	call := `Twice'Call(value is ?V)`
	returned := `Twice'Return(value is ?V, Return is ?R)`
	qualifiedCall := `worker.Twice'Call(value is ?V)`
	qualifiedReturn := `worker.Twice'Return(value is ?V, Return is ?R)`
	if alternate {
		call = `tWiCe'cAlL(value is ?V)`
		returned = `TWICE'RETURN(Return is ?R, value is ?V)`
		qualifiedCall = `worker.TWICE'CALL(value is ?V)`
		qualifiedReturn = `worker.twice'return(Return is ?R, value is ?V)`
	}
	return []byte(`
type Driver is interface action out Start(value : Integer); end interface Driver;
type Worker is interface
  action in Start(value : Integer);
  action out Done(value : Integer);
  action out Saw_Call(value : Integer);
  action out Saw_Return(value : Integer);
  provides Twice : function(value : Integer) return Integer;
  constraint
    match (?V, ?R : Integer) (` + call + ` -> ` + returned + `);
  behavior
    result : var Integer := 0;
    Twice : function(value : Integer) return Integer is
    begin
      return value * 2;
    end function Twice;
  begin
    (?V : Integer) Start(?V) =>
      result := Twice(?V);
      Done($result);
    ;
    (?V : Integer) ` + call + ` => Saw_Call(?V); ;
    (?V, ?R : Integer) ` + returned + ` => Saw_Return(?R); ;
end interface Worker;
architecture System() is
  driver : Driver;
  worker : Worker;
constraint
  match (?V, ?R : Integer) (` + qualifiedCall + ` -> ` + qualifiedReturn + `);
connect
  driver.Start => worker.Start;
end architecture System;
`)
}

func TestFunctionCallReturnAttributesMatchTypedImplicitEventsAndReplay(t *testing.T) {
	canonical, err := Compile(functionAttributeSource(false), "System")
	if err != nil {
		t.Fatal(err)
	}
	alternate, err := Compile(functionAttributeSource(true), "system")
	if err != nil {
		t.Fatal(err)
	}
	canonicalDigest, err := canonical.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	alternateDigest, err := alternate.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if canonicalDigest != alternateDigest {
		t.Fatalf("function attribute case/association order changed model identity: %s != %s", canonicalDigest, alternateDigest)
	}
	journal := arch.NewExecutionJournal(canonicalDigest, 20, arch.InputEvent{
		Key: "start", Source: "driver", Action: "Start", Params: map[string]any{"value": int64(4)},
	})

	previous := runtime.GOMAXPROCS(1)
	first, err := canonical.ExecuteDeterministic(journal)
	if err != nil {
		runtime.GOMAXPROCS(previous)
		t.Fatal(err)
	}
	runtime.GOMAXPROCS(8)
	second, err := alternate.ExecuteDeterministic(journal)
	runtime.GOMAXPROCS(previous)
	if err != nil {
		t.Fatal(err)
	}

	call := first.Poset.ByName("Twice'Call")
	returned := first.Poset.ByName("Twice'Return")
	sawCall := first.Poset.ByName("Saw_Call")
	sawReturn := first.Poset.ByName("Saw_Return")
	done := first.Poset.ByName("Done")
	if len(call) != 1 || len(returned) != 1 || len(sawCall) != 1 || len(sawReturn) != 1 || len(done) != 1 {
		t.Fatalf("function attribute event counts call=%d return=%d saw=%d/%d done=%d",
			len(call), len(returned), len(sawCall), len(sawReturn), len(done))
	}
	if value, _ := call[0].Param("value"); value != int64(4) {
		t.Fatalf("F'Call value=%#v, want 4", value)
	}
	if value, _ := returned[0].Param("value"); value != int64(4) {
		t.Fatalf("F'Return repeated call value=%#v, want 4", value)
	}
	if value, _ := returned[0].Param("Return"); value != int64(8) {
		t.Fatalf("F'Return Return=%#v, want 8", value)
	}
	if value, _ := sawCall[0].Param("value"); value != int64(4) {
		t.Fatalf("Saw_Call value=%#v, want 4", value)
	}
	if value, _ := sawReturn[0].Param("value"); value != int64(8) {
		t.Fatalf("Saw_Return value=%#v, want 8", value)
	}
	if !first.Poset.IsCausallyBefore(call[0].ID, returned[0].ID) ||
		!first.Poset.IsCausallyBefore(call[0].ID, sawCall[0].ID) ||
		!first.Poset.IsCausallyBefore(returned[0].ID, sawReturn[0].ID) ||
		!first.Poset.IsCausallyBefore(returned[0].ID, done[0].ID) {
		t.Fatal("function attribute patterns lost call/return causality")
	}
	if first.Constraints == nil || !first.Constraints.Passed ||
		len(first.ModuleConstraints) != 1 || !first.ModuleConstraints[0].Report.Passed {
		t.Fatalf("function attribute constraint reports architecture=%#v module=%#v", first.Constraints, first.ModuleConstraints)
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
		t.Fatal("function attribute execution changed with case, association order, or GOMAXPROCS")
	}
	artifactDigest, err := first.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := alternate.ReplayDeterministic(journal, artifactDigest)
	if err != nil {
		t.Fatal(err)
	}
	replayedBytes, err := replayed.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstBytes, replayedBytes) {
		t.Fatal("function attribute replay changed canonical artifact bytes")
	}
}

func TestFunctionAttributeUnsupportedBoundariesAreExplicit(t *testing.T) {
	for _, test := range []struct {
		name      string
		functions string
		pattern   string
		want      string
	}{
		{name: "module lifecycle", functions: `provides F : function(n : Integer) return Integer;`, pattern: `F'Running`, want: "module lifecycle attributes require"},
		{name: "action receiver", functions: `action out F(n : Integer);`, pattern: `F'Call`, want: `function "F" is not declared`},
		{name: "overloaded", functions: `provides F : function(n : Integer) return Integer; provides F : function(n : Float) return Float;`, pattern: `F'Call`, want: "resolves to 2 overloads"},
		{name: "void return root", functions: `provides F : function(n : Integer);`, pattern: `F'Return`, want: "implicit Return : Root"},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := []byte(`type API is interface ` + test.functions + ` constraint match ` + test.pattern + `; end interface API; architecture System() is api : API; end architecture System;`)
			_, err := Compile(source, "System")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("function attribute diagnostic=%v, want %q", err, test.want)
			}
		})
	}

	connection := []byte(`
type Source is interface requires F : function(n : Integer) return Integer; end interface Source;
type Target is interface action in Saw(n : Integer); end interface Target;
architecture System() is source : Source; target : Target;
connect (?N : Integer) source.F'Call(?N) => target.Saw(?N);
end architecture System;
`)
	if _, err := Compile(connection, "System"); err == nil || !strings.Contains(err.Error(), "attribute connection triggers") {
		t.Fatalf("function attribute connection diagnostic=%v", err)
	}
}

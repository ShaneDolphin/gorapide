package rapide

import (
	"bytes"
	"runtime"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide/arch"
)

func TestCompileModuleProvidedFunctionUsesModuleStateAndReplays(t *testing.T) {
	source := []byte(`
type Driver is interface action out Start(n : Integer); end interface Driver;
type Worker is interface
  action in Start(n : Integer);
  action out Done(n : Integer);
  provides Add : function(n : Integer);
  provides Increment : function(n : Integer) return Integer;
end interface Worker;

module Counter() return Worker is
  count : var Integer := 0;
  result : var Integer := 0;
  Add : function(n : Integer) is
  begin
    count := $count + n;
  end function Add;
  Increment : function(n : Integer) return Integer is
  begin
    Add(n);
    return $count;
  end function Increment;
parallel
  when (?N : Integer) Start(?N) do
    result := Increment(?N);
    Done($result);
  end when;
end module Counter;

architecture System() is
  driver : Driver;
  worker : Worker is Counter();
connect driver.Start => worker.Start;
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
	journal := arch.NewExecutionJournal(digest, 20, arch.InputEvent{
		Key: "start", Source: "driver", Action: "Start", Params: map[string]any{"n": 3},
	})
	result, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	call := result.Poset.ByName("Increment'Call")
	addCall := result.Poset.ByName("Add'Call")
	addReturn := result.Poset.ByName("Add'Return")
	returned := result.Poset.ByName("Increment'Return")
	done := result.Poset.ByName("Done")
	if len(call) != 1 || len(addCall) != 1 || len(addReturn) != 1 || len(returned) != 1 || len(done) != 1 {
		t.Fatalf("module function events increment=%d/%d add=%d/%d done=%d",
			len(call), len(returned), len(addCall), len(addReturn), len(done))
	}
	if !result.Poset.IsCausallyBefore(call[0].ID, addCall[0].ID) ||
		!result.Poset.IsCausallyBefore(addCall[0].ID, addReturn[0].ID) ||
		!result.Poset.IsCausallyBefore(addReturn[0].ID, returned[0].ID) ||
		!result.Poset.IsCausallyBefore(returned[0].ID, done[0].ID) {
		t.Fatal("nested local module function causality is incomplete")
	}
	if value, _ := done[0].Param("n"); value != int64(3) {
		t.Fatalf("module function result=%#v, want int64(3)", value)
	}
	state := make(map[string]arch.StateRecord, len(result.State))
	for _, snapshot := range result.State {
		state[snapshot.ComponentID+"."+snapshot.Name] = snapshot
	}
	if len(state) != 2 || state["worker.count"].Value.Text != "3" ||
		state["worker.result"].Value.Text != "3" {
		t.Fatalf("module function final state=%#v", result.State)
	}
	var countWrite, resultWrite bool
	for _, firing := range result.Firings {
		for _, read := range firing.StateReads {
			if read.ComponentID == "" {
				t.Fatalf("module function emitted an unqualified state read: %#v", read)
			}
		}
		for _, write := range firing.StateWrites {
			switch {
			case write.ComponentID == "worker" && write.Name == "count" && write.Value.Text == "3":
				countWrite = true
			case write.ComponentID == "worker" && write.Name == "result" && write.Value.Text == "3" &&
				len(write.Causes) == 1 && write.Causes[0] == string(returned[0].ID):
				resultWrite = true
			}
		}
	}
	if !countWrite || !resultWrite {
		t.Fatalf("module function state audit count=%v result=%v firings=%#v", countWrite, resultWrite, result.Firings)
	}

	artifactDigest, err := result.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := model.ReplayDeterministic(journal, artifactDigest)
	if err != nil {
		t.Fatal(err)
	}
	left, err := result.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	right, err := replayed.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(left, right) {
		t.Fatal("module function replay was not byte-identical")
	}
}

func TestCompileModuleRequiredFunctionRoutesToProviderModule(t *testing.T) {
	source := []byte(`
type Driver is interface action out Start(n : Integer); end interface Driver;
type Client is interface
  action in Start(n : Integer);
  action out Done(n : Integer);
  provides Compute : function(n : Integer) return Integer;
  requires Lookup : function(value : Integer) return Integer;
end interface Client;
type Server is interface
  action out Seen(n : Integer);
  provides Fetch : function(operand : Integer) return Integer;
end interface Server;

module ClientModule() return Client is
  scratch : var Integer := 0;
  result : var Integer := 0;
  Compute : function(n : Integer) return Integer is
  begin
    scratch := Lookup(n);
    return $scratch;
  end function Compute;
parallel
  when (?N : Integer) Start(?N) do
    result := Compute(?N);
    Done($result);
  end when;
end module ClientModule;

module ServerModule() return Server is
  calls : var Integer := 0;
  Fetch : function(operand : Integer) return Integer is
  begin
    calls := $calls + 1;
    Seen($calls);
    return operand * 2 + $calls;
  end function Fetch;
end module ServerModule;

architecture System() is
  driver : Driver;
  client : Client is ClientModule();
  server : Server is ServerModule();
connect
  driver.Start => client.Start;
  client.Lookup to server.Fetch;
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
	journal := arch.NewExecutionJournal(digest, 20, arch.InputEvent{
		Key: "start", Source: "driver", Action: "Start", Params: map[string]any{"n": 4},
	})
	result, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	requiredCall := result.Poset.ByName("Lookup'Call")
	providedCall := result.Poset.ByName("Fetch'Call")
	seen := result.Poset.ByName("Seen")
	providedReturn := result.Poset.ByName("Fetch'Return")
	requiredReturn := result.Poset.ByName("Lookup'Return")
	computeCall := result.Poset.ByName("Compute'Call")
	computeReturn := result.Poset.ByName("Compute'Return")
	done := result.Poset.ByName("Done")
	if len(requiredCall) != 1 || len(providedCall) != 1 || len(seen) != 1 ||
		len(providedReturn) != 1 || len(requiredReturn) != 1 || len(computeCall) != 1 ||
		len(computeReturn) != 1 || len(done) != 1 {
		t.Fatalf("routed module function event counts compute=%d/%d route=%d/%d seen=%d return=%d/%d done=%d",
			len(computeCall), len(computeReturn), len(requiredCall), len(providedCall), len(seen),
			len(providedReturn), len(requiredReturn), len(done))
	}
	if requiredCall[0].ID != providedCall[0].ID || requiredReturn[0].ID != providedReturn[0].ID {
		t.Fatal("routed module function duplicated caller/provider occurrences")
	}
	if !result.Poset.IsCausallyBefore(computeCall[0].ID, requiredCall[0].ID) ||
		!result.Poset.IsCausallyBefore(providedCall[0].ID, seen[0].ID) ||
		!result.Poset.IsCausallyBefore(seen[0].ID, providedReturn[0].ID) ||
		!result.Poset.IsCausallyBefore(requiredReturn[0].ID, computeReturn[0].ID) ||
		!result.Poset.IsCausallyBefore(computeReturn[0].ID, done[0].ID) {
		t.Fatal("routed module function lost synchronous causal edges")
	}
	if value, _ := done[0].Param("n"); value != int64(9) {
		t.Fatalf("routed module function result=%#v, want int64(9)", value)
	}
	state := make(map[string]string, len(result.State))
	for _, snapshot := range result.State {
		state[snapshot.ComponentID+"."+snapshot.Name] = snapshot.Value.Text
	}
	if len(state) != 3 || state["server.calls"] != "1" || state["client.scratch"] != "9" ||
		state["client.result"] != "9" {
		t.Fatalf("routed module function final state=%#v", result.State)
	}
	owners := make(map[string]bool)
	for _, firing := range result.Firings {
		for _, write := range firing.StateWrites {
			owners[write.ComponentID+"."+write.Name] = true
		}
	}
	if !owners["server.calls"] || !owners["client.scratch"] || !owners["client.result"] {
		t.Fatalf("routed module function state audit owners=%#v", owners)
	}

	artifactDigest, err := result.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := model.ReplayDeterministic(journal, artifactDigest)
	if err != nil {
		t.Fatal(err)
	}
	left, _ := result.MarshalCanonical()
	right, _ := replayed.MarshalCanonical()
	if !bytes.Equal(left, right) {
		t.Fatal("routed module function replay was not byte-identical")
	}
}

func TestModuleFunctionDeclarationOrderIsNotSemantic(t *testing.T) {
	compile := func(functions string) string {
		t.Helper()
		source := []byte(`
type API is interface
  provides A : function(n : Integer) return Integer;
  provides B : function(n : Integer) return Integer;
end interface API;
module Implementation() return API is
` + functions + `
end module Implementation;
architecture System() is api : API is Implementation(); end architecture System;
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
	a := "A : function(n : Integer) return Integer is begin return n + 1; end function A;"
	b := "B : function(n : Integer) return Integer is begin return n * 2; end function B;"
	if left, right := compile(a+b), compile(b+a); left != right {
		t.Fatalf("module function declaration order changed model identity: %s != %s", left, right)
	}
}

func TestCompileRejectsMalformedOrUnsupportedModuleFunctions(t *testing.T) {
	tests := []struct {
		name, declarations, moduleBody, want string
		digest                               bool
	}{
		{
			name: "missing implementation", declarations: "provides F : function(n : Integer) return Integer;",
			want: `provides function "F" with 0 matching implementations`,
		},
		{
			name: "required body", declarations: "requires F : function(n : Integer) return Integer;",
			moduleBody: "F : function(n : Integer) return Integer is begin return n; end function F;",
			want:       `matches 0 provided interface signatures`,
		},
		{
			name: "mismatched formal", declarations: "provides F : function(n : Integer) return Integer;",
			moduleBody: "F : function(value : Integer) return Integer is begin return value; end function F;",
			want:       `matches 0 provided interface signatures`,
		},
		{
			name: "duplicate body", declarations: "provides F : function(n : Integer) return Integer;",
			moduleBody: "F : function(n : Integer) return Integer is begin return n; end function F;" +
				"F : function(n : Integer) return Integer is begin return n; end function F;",
			want: `duplicate module function implementation`,
		},
		{
			name: "missing typed return", declarations: "provides F : function(n : Integer) return Integer;",
			moduleBody: "F : function(n : Integer) return Integer is begin null; end function F;",
			want:       `requires a final return expression`,
		},
		{
			name: "void returns value", declarations: "provides F : function(n : Integer);",
			moduleBody: "F : function(n : Integer) is begin return n; end function F;",
			want:       `cannot return a value`,
		},
		{
			name: "unknown state", declarations: "provides F : function(n : Integer) return Integer;",
			moduleBody: "F : function(n : Integer) return Integer is begin missing := n; return n; end function F;",
			want:       `targets undeclared state`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := []byte("type API is interface " + test.declarations + " end interface API; " +
				"module M() return API is " + test.moduleBody + " end module M; " +
				"architecture X() is api : API is M(); end architecture X;")
			model, err := Compile(source, "X")
			if err == nil && test.digest {
				_, err = model.DeterministicModelDigest()
			}
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v, want diagnostic containing %q", err, test.want)
			}
		})
	}
}

func TestSourceTimedFunctionRejectsNonProcessCallerBeforeCall(t *testing.T) {
	source := []byte(`
type API is interface provides F : function(); end interface API;
module M() return API is
  C : Clock is Make_Clock();
  F : function() is begin pause C.Ticks(1); end function F;
initial
  F();
end module M;
architecture X() is api : API is M(); end architecture X;
`)
	model, err := Compile(source, "X")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	_, err = model.ExecuteDeterministic(arch.NewExecutionJournalWithLimits(
		digest, arch.ExecutionLimits{MaxFirings: 20, MaxStatements: 20},
	))
	if err == nil || !strings.Contains(err.Error(), "requires a process-owned resumable call stack") {
		t.Fatalf("ExecuteDeterministic()=%v, want explicit non-process suspension boundary", err)
	}
}

func TestSourceModuleFunctionsStableAcrossGOMAXPROCS(t *testing.T) {
	source := []byte(`
type Driver is interface action out Start(n : Integer); end interface Driver;
type Worker is interface
  action in Start(n : Integer); action out Done(n : Integer);
  provides Twice : function(n : Integer) return Integer;
end interface Worker;
module M() return Worker is
  result : var Integer := 0;
  Twice : function(n : Integer) return Integer is begin return n * 2; end function Twice;
parallel
  when (?N : Integer) Start(?N) do result := Twice(?N); Done($result); end when;
end module M;
architecture X() is d : Driver; w : Worker is M(); connect d.Start => w.Start; end architecture X;
`)
	previous := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(previous)
	var baseline []byte
	for _, processors := range []int{1, 8} {
		runtime.GOMAXPROCS(processors)
		for run := 0; run < 10; run++ {
			model, err := Compile(source, "X")
			if err != nil {
				t.Fatal(err)
			}
			digest, err := model.DeterministicModelDigest()
			if err != nil {
				t.Fatal(err)
			}
			result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 20,
				arch.InputEvent{Key: "start", Source: "d", Action: "Start", Params: map[string]any{"n": 7}},
			))
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := result.MarshalCanonical()
			if err != nil {
				t.Fatal(err)
			}
			if baseline == nil {
				baseline = encoded
			} else if !bytes.Equal(baseline, encoded) {
				t.Fatalf("module functions changed under GOMAXPROCS=%d run=%d", processors, run)
			}
		}
	}
}

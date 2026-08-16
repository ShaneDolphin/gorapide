package rapide

import (
	"bytes"
	"runtime"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/arch"
)

func distinctSourceIteratorEventCount(events gorapide.EventSet) int {
	seen := make(map[gorapide.EventID]bool, len(events))
	for _, event := range events {
		seen[event.ID] = true
	}
	return len(seen)
}

func proceduralForSource(explicitType bool) []byte {
	typePart := ""
	if explicitType {
		typePart = " : Integer"
	}
	return []byte(`
type Worker is interface
  action out Start(); action out Emit(value : Integer); action out Done();
  behavior
  begin
    Start =>
      for I` + typePart + ` in 1..3 do
        Emit(I);
      end;
      Done();
    ;
end interface Worker;
architecture M() is worker : Worker; end architecture M;
`)
}

func TestCompileProceduralForUsesPublishedRangeIteratorProtocol(t *testing.T) {
	model, err := Compile(proceduralForSource(true), "M")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := arch.NewExecutionJournalWithLimits(digest,
		arch.ExecutionLimits{MaxFirings: 30, MaxStatements: 32},
		arch.InputEvent{Key: "start", Source: "worker", Action: "Start"},
	)
	first, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	second, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	firstBytes, _ := first.MarshalCanonical()
	secondBytes, _ := second.MarshalCanonical()
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatal("source procedural for execution was not byte-identical")
	}
	if len(first.Poset.ByName("Emit")) != 3 || len(first.Poset.ByName("Done")) != 1 ||
		distinctSourceIteratorEventCount(first.Poset.ByName("More'Call")) != 4 ||
		distinctSourceIteratorEventCount(first.Poset.ByName("Item'Call")) != 3 ||
		first.StatementSteps != 12 {
		t.Fatalf("source for emit/done/more/item/steps=%d/%d/%d/%d/%d",
			len(first.Poset.ByName("Emit")), len(first.Poset.ByName("Done")),
			distinctSourceIteratorEventCount(first.Poset.ByName("More'Call")),
			distinctSourceIteratorEventCount(first.Poset.ByName("Item'Call")), first.StatementSteps)
	}
	values := make(map[int64]bool)
	for _, event := range first.Poset.ByName("Emit") {
		value, _ := event.Param("value")
		values[value.(int64)] = true
	}
	if !values[1] || !values[2] || !values[3] {
		t.Fatalf("source for values=%#v", values)
	}
	var lifecycle *arch.ModuleLifecycleRecord
	for index := range first.Modules {
		if first.Modules[index].Kind == "predefined-range-iterator" {
			lifecycle = &first.Modules[index]
			break
		}
	}
	liveSelf, lostLexical := false, false
	if lifecycle != nil {
		for _, name := range lifecycle.Names {
			liveSelf = liveSelf || name.Kind == "implicit-self" && name.Live
			lostLexical = lostLexical || name.Kind != "implicit-self" && !name.Live
		}
	}
	if lifecycle == nil || lifecycle.State != arch.ModuleFinalizedState || lifecycle.Namable ||
		lifecycle.StartEventID == "" || lifecycle.FinishEventID == "" ||
		len(lifecycle.Names) != 2 || !liveSelf || !lostLexical {
		t.Fatalf("source range lifecycle=%#v", lifecycle)
	}
	finish, exists := first.Poset.Event(gorapide.EventID(lifecycle.FinishEventID))
	if !exists {
		t.Fatalf("source range Finish %q is absent", lifecycle.FinishEventID)
	}
	done := first.Poset.ByName("Done")[0]
	if !first.Poset.IsCausallyIndependent(finish.ID, done.ID) {
		t.Fatal("source range Finish incorrectly ordered the following statement")
	}
}

func TestProceduralForInferenceAndCaseAreCanonicalAcrossGOMAXPROCS(t *testing.T) {
	explicit, err := Compile(proceduralForSource(true), "M")
	if err != nil {
		t.Fatal(err)
	}
	inferredSource := bytes.ReplaceAll(proceduralForSource(false), []byte("for I"), []byte("for i"))
	inferredSource = bytes.ReplaceAll(inferredSource, []byte("Emit(I)"), []byte("Emit(i)"))
	inferred, err := Compile(inferredSource, "M")
	if err != nil {
		t.Fatal(err)
	}
	explicitDigest, err := explicit.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	inferredDigest, err := inferred.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if explicitDigest != inferredDigest {
		t.Fatalf("explicit/inferred Integer iterator changed model identity: %s != %s", explicitDigest, inferredDigest)
	}

	prior := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(prior)
	var baseline []byte
	for _, processors := range []int{1, 4} {
		runtime.GOMAXPROCS(processors)
		for run := 0; run < 3; run++ {
			result, err := inferred.ExecuteDeterministic(arch.NewExecutionJournalWithLimits(inferredDigest,
				arch.ExecutionLimits{MaxFirings: 30, MaxStatements: 32},
				arch.InputEvent{Key: "start", Source: "worker", Action: "Start"},
			))
			if err != nil {
				t.Fatal(err)
			}
			encoded, _ := result.MarshalCanonical()
			if baseline == nil {
				baseline = encoded
			} else if !bytes.Equal(baseline, encoded) {
				t.Fatalf("source iterator changed at GOMAXPROCS=%d run=%d", processors, run)
			}
		}
	}
}

func TestProceduralForOmittedIdentifierIsCanonicalAndUnbound(t *testing.T) {
	source := func(explicitType bool) []byte {
		typePart := ""
		if explicitType {
			typePart = " : Integer"
		}
		return []byte(`
type Worker is interface
  action out Start(); action out Tick(); action out Use(value : Integer);
  behavior begin Start =>
    for` + typePart + ` in 1..2 do Tick(); end;
  ; end interface Worker;
architecture M() is worker : Worker; end architecture M;
`)
	}
	inferred, err := Compile(source(false), "M")
	if err != nil {
		t.Fatal(err)
	}
	explicit, err := Compile(source(true), "M")
	if err != nil {
		t.Fatal(err)
	}
	inferredDigest, err := inferred.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	explicitDigest, err := explicit.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if inferredDigest != explicitDigest {
		t.Fatalf("omitted identifier inferred/explicit type changed model: %s != %s",
			inferredDigest, explicitDigest)
	}
	prefixlessSource := bytes.Replace(source(false), []byte("for in 1..2"), []byte("for 1..2"), 1)
	prefixless, err := Compile(prefixlessSource, "M")
	if err != nil {
		t.Fatal(err)
	}
	prefixlessDigest, err := prefixless.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if prefixlessDigest != inferredDigest {
		t.Fatalf("omitted whole iterator prefix changed model: %s != %s", prefixlessDigest, inferredDigest)
	}
	result, err := inferred.ExecuteDeterministic(arch.NewExecutionJournalWithLimits(inferredDigest,
		arch.ExecutionLimits{MaxFirings: 20, MaxStatements: 24},
		arch.InputEvent{Key: "start", Source: "worker", Action: "Start"},
	))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Poset.ByName("Tick")) != 2 {
		t.Fatalf("anonymous for Tick count=%d, want 2", len(result.Poset.ByName("Tick")))
	}

	invalid := bytes.Replace(source(false), []byte("Tick(); end;"), []byte("Use(I); end;"), 1)
	_, err = Compile(invalid, "M")
	if err == nil || !strings.Contains(err.Error(), "name \"I\" is not declared in this body") {
		t.Fatalf("omitted iterator leaked a binding: %v", err)
	}
}

func TestCompileGeneralForPreservesInitializerTestNextSemantics(t *testing.T) {
	source := []byte(`
type Worker is interface
  action out Start(); action out Emit(value : Integer); action out Done(value : Integer);
  behavior i : var Integer := 99;
  begin
    Start =>
      for i := 1 in ($i <= 4) next i := $i + 1 do
        if $i = 2 then next; end if;
        Emit($i);
        exit where $i = 3;
      end for;
      Done($i);
    ;
end interface Worker;
architecture M() is worker : Worker; end architecture M;
`)
	model, err := Compile(source, "M")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := arch.NewExecutionJournalWithLimits(digest,
		arch.ExecutionLimits{MaxFirings: 20, MaxStatements: 80},
		arch.InputEvent{Key: "start", Source: "worker", Action: "Start"},
	)
	result, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	values := make(map[int64]bool)
	for _, event := range result.Poset.ByName("Emit") {
		value, _ := event.Param("value")
		values[value.(int64)] = true
	}
	done := result.Poset.ByName("Done")
	if len(values) != 2 || !values[1] || !values[3] || len(done) != 1 {
		t.Fatalf("source general-for values=%#v done=%d", values, len(done))
	}
	if value, _ := done[0].Param("value"); value != int64(3) {
		t.Fatalf("source general-for final value=%#v, want 3", value)
	}
	if len(result.State) != 1 || result.State[0].Value.Text != "3" || result.State[0].Version != 3 {
		t.Fatalf("source general-for final state=%#v", result.State)
	}
	expected, err := result.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	left, _ := result.MarshalCanonical()
	right, _ := replayed.MarshalCanonical()
	if !bytes.Equal(left, right) {
		t.Fatal("source general-for replay changed canonical bytes")
	}
}

func TestCompileGeneralForFunctionObjectExpressions(t *testing.T) {
	source := []byte(`
type Worker is interface
  action out Start(); action out Emit(value : Integer);
  provides Initialize : function();
  provides More : function() return Boolean;
  provides Advance : function();
  behavior
    i : var Integer := 0;
    Initialize : function() is begin i := 1; end function Initialize;
    More : function() return Boolean is begin return $i <= 3; end function More;
    Advance : function() is begin i := $i + 1; end function Advance;
  begin
    Start =>
      for Initialize() in More() next Advance() do Emit($i); end for;
    ;
end interface Worker;
architecture M() is worker : Worker; end architecture M;
`)
	model, err := Compile(source, "M")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournalWithLimits(digest,
		arch.ExecutionLimits{MaxFirings: 30, MaxStatements: 128},
		arch.InputEvent{Key: "start", Source: "worker", Action: "Start"},
	))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Poset.ByName("Initialize'Call")) != 1 ||
		len(result.Poset.ByName("More'Call")) != 4 ||
		len(result.Poset.ByName("Advance'Call")) != 3 ||
		len(result.Poset.ByName("Emit")) != 3 {
		t.Fatalf("source object-expression calls init/more/advance/emit=%d/%d/%d/%d",
			len(result.Poset.ByName("Initialize'Call")), len(result.Poset.ByName("More'Call")),
			len(result.Poset.ByName("Advance'Call")), len(result.Poset.ByName("Emit")))
	}
	if len(result.State) != 1 || result.State[0].Value.Text != "4" || result.State[0].Version != 4 {
		t.Fatalf("source function-object general-for state=%#v", result.State)
	}
}

func TestGeneralForSourceRejectsNonBooleanTestAndActionExpression(t *testing.T) {
	tests := []struct {
		state string
		loop  string
		want  string
	}{
		{loop: "for 0 in 1 next 2 do null; end;", want: "test has type Integer, want Boolean"},
		{loop: "for Start() in true next 0 do null; end;", want: "action \"Start\" cannot be used as a general for object expression"},
		{
			state: "flag : var Boolean := false;",
			loop:  "for 0 in flag := true next 0 do null; end;",
			want:  "test has type Ref(Boolean), want Boolean",
		},
	}
	for _, test := range tests {
		source := []byte(`
type Worker is interface
  action out Start();
  behavior ` + test.state + ` begin Start => ` + test.loop + ` ; end interface Worker;
architecture M() is worker : Worker; end architecture M;
`)
		_, err := Compile(source, "M")
		if err == nil || !strings.Contains(err.Error(), test.want) {
			t.Fatalf("loop %q error=%v, want %q", test.loop, err, test.want)
		}
	}
}

func TestProceduralForRejectsUnsupportedTypesAndGeneralObjectIterators(t *testing.T) {
	tests := []struct {
		statement string
		want      string
	}{
		{"for I : Boolean in 1..2 do null; end;", "has type Boolean, want Integer"},
		{"for I in true..false do null; end;", "range endpoints have types Boolean and Boolean, want Integer"},
		{"for I in Items do null; end;", "first '.' in procedural iterator range"},
		{"for I in Make_Items() do null; end;", "module-generator call expressions are outside the current source subset"},
	}
	for _, test := range tests {
		source := []byte(`
type Worker is interface
  action out Start();
  behavior begin Start => ` + test.statement + ` ; end interface Worker;
architecture M() is worker : Worker; end architecture M;
`)
		_, err := Compile(source, "M")
		if err == nil || !strings.Contains(err.Error(), test.want) {
			t.Fatalf("statement %q error=%v, want %q", test.statement, err, test.want)
		}
	}
}

package arch

import (
	"testing"
)

func TestAllocatorInitializationSubsetRejectsNestedNew(t *testing.T) {
	wrapped := QualifyValue("Factory", ModuleNewValue("Factory"))
	for _, statement := range []Statement{
		SetState("Value", wrapped),
		AssertThat(wrapped),
		CallAction("allocated", "Allocated", ExpressionParam("value", wrapped)),
	} {
		if allocatorInitializationStatementSubset([]Statement{statement}) {
			t.Fatal("allocator initialization subset accepted a nested New expression")
		}
	}
}

func TestAllocatorInitializationSubsetAdmitsGeneralForAndRejectsNestedNewControls(t *testing.T) {
	general := ForObjectExpressions(
		ObjectAssignment("Count", LiteralValue(int64(0))),
		ObjectValue(LiteralValue(false)),
		ObjectAssignment("Count", AddValues(ReadState("Count"), LiteralValue(int64(1)))),
		NullStatement(),
	)
	if !allocatorInitializationStatementSubset([]Statement{general}) {
		t.Fatal("allocator initialization subset rejected closed general-for object expressions")
	}

	nested := QualifyValue("Factory", ModuleNewValue("Factory"))
	for _, statement := range []Statement{
		ForObjectExpressions(
			ObjectValue(nested), ObjectValue(LiteralValue(false)),
			ObjectValue(LiteralValue(int64(0))), NullStatement(),
		),
		ForObjectExpressions(
			ObjectValue(LiteralValue(int64(0))), ObjectValue(nested),
			ObjectValue(LiteralValue(int64(0))), NullStatement(),
		),
		ForObjectExpressions(
			ObjectValue(LiteralValue(int64(0))), ObjectValue(LiteralValue(false)),
			ObjectAssignment("Count", nested), NullStatement(),
		),
	} {
		if allocatorInitializationStatementSubset([]Statement{statement}) {
			t.Fatal("allocator initialization subset accepted nested New in a general-for control")
		}
	}
}

func TestAllocatorInitializationSubsetAdmitsNamedDoControl(t *testing.T) {
	statements := []Statement{
		NameDo("outer", LoopDo(
			NameDo("inner", LoopDo(
				NextNamedWhen("outer", LiteralValue(false)),
				ExitNamed("outer"),
			)),
		)),
		NameDo("plain", DoBlock(NextNamed("plain"))),
	}
	if !allocatorInitializationStatementSubset(statements) {
		t.Fatal("allocator initialization subset rejected canonical named do control")
	}
}

func TestAllocatorInitializationSubsetAdmitsExceptionEscapeAndSelfInterrupts(t *testing.T) {
	failure := "rapide:module:factory:exception:failure"
	other := "rapide:module:factory:exception:other"
	raiseFailure := RaiseDeclaredException("raise-failure", failure, "Failure")
	raiseOther := RaiseDeclaredException("raise-other", other, "Other")

	exact := HandleDo([]Statement{raiseFailure}, ExceptionHandler{Choices: []ExceptionHandlerChoice{
		HandleDeclaredException(failure, "Failure", nil, NullStatement()),
	}})
	catchAll := HandleDo([]Statement{raiseFailure}, ExceptionHandler{Else: []Statement{NullStatement()}})
	innerReraise := HandleDo([]Statement{raiseFailure}, ExceptionHandler{Choices: []ExceptionHandlerChoice{
		HandleDeclaredException(failure, "Failure", nil, ReraiseException()),
	}})
	outerRecovery := HandleDo([]Statement{innerReraise}, ExceptionHandler{Choices: []ExceptionHandlerChoice{
		HandleDeclaredException(failure, "Failure", nil, NullStatement()),
	}})
	for _, test := range []struct {
		name      string
		statement Statement
	}{
		{name: "exact", statement: exact},
		{name: "exception else", statement: catchAll},
		{name: "nested reraise", statement: outerRecovery},
		{name: "unhandled", statement: raiseFailure},
		{name: "same handler body escapes", statement: HandleDo([]Statement{raiseFailure}, ExceptionHandler{
			Choices: []ExceptionHandlerChoice{
				HandleDeclaredException(failure, "Failure", nil, raiseOther),
				HandleDeclaredException(other, "Other", nil, NullStatement()),
			},
		})},
		{name: "uncontained reraise", statement: HandleDo([]Statement{raiseFailure}, ExceptionHandler{
			Choices: []ExceptionHandlerChoice{
				HandleDeclaredException(failure, "Failure", nil, ReraiseException()),
			},
		})},
		{name: "action interrupt", statement: HandleDo([]Statement{
			CallAction("pulse", "Pulse"), NullStatement(),
		}, ExceptionHandler{
			Choices: []ExceptionHandlerChoice{HandleInterrupt("Pulse", nil, NullStatement())},
		})},
		{name: "any interrupt", statement: HandleDo([]Statement{
			CallAction("pulse", "Pulse"), NullStatement(),
		}, ExceptionHandler{
			Choices: []ExceptionHandlerChoice{HandleAnyEvent(NullStatement())},
		})},
	} {
		t.Run(test.name, func(t *testing.T) {
			if !allocatorInitializationStatementSubset([]Statement{test.statement}) {
				t.Fatal("allocator initialization subset rejected a supported handled or escaping exception")
			}
		})
	}

	tests := []struct {
		name      string
		statement Statement
	}{
		{name: "cross-call interrupt", statement: HandleDo([]Statement{
			CallFunction("helper", "Helper"),
		}, ExceptionHandler{
			Choices: []ExceptionHandlerChoice{HandleInterrupt("Pulse", nil, NullStatement())},
		})},
		{name: "overlapping any interrupt", statement: HandleDo([]Statement{NullStatement()}, ExceptionHandler{
			Choices: []ExceptionHandlerChoice{
				HandleAnyEvent(NullStatement()),
				HandleDeclaredException(failure, "Failure", nil, NullStatement()),
			},
		})},
		{name: "inactive reraise", statement: ReraiseException()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if allocatorInitializationStatementSubset([]Statement{test.statement}) {
				t.Fatal("allocator initialization subset accepted an interrupt or malformed reraise")
			}
		})
	}

	component := NewComponent("factory", Interface("Factory").
		InAction("Incoming").OutAction("Pulse").Build(), nil)
	external := HandleDo([]Statement{CallAction("pulse", "Pulse")}, ExceptionHandler{
		Choices: []ExceptionHandlerChoice{HandleInterrupt("Incoming", nil, NullStatement())},
	})
	if allocatorInitializationStatementListSubset(
		component.ID, component, []Statement{external}, nil, false,
		make(map[string]bool), nil, allocatorHandledException{},
	) {
		t.Fatal("allocator initialization subset accepted an external in-action interrupt choice")
	}
}

func TestAllocatorInitializationSubsetAdmitsDeferredInAndRejectsSuspension(t *testing.T) {
	component := NewComponent("factory", Interface("Factory").OutAction("Deferred").Build(), nil)
	if err := component.AddBasicClock("C"); err != nil {
		t.Fatal(err)
	}
	deferred := CallActionInRange("deferred", "Deferred", "C", 1, 2)
	if !allocatorInitializationStatementListSubset(
		component.ID, component, []Statement{deferred}, nil, false,
		make(map[string]bool), nil, allocatorHandledException{},
	) {
		t.Fatal("allocator initialization subset rejected non-suspending deferred in action")
	}
	for name, statement := range map[string]Statement{
		"pause": CallActionPauseRange("pause", "Deferred", "C", 1, 1),
		"delay": CallActionDelayRange("delay", "Deferred", "C", 1, 1),
	} {
		t.Run(name, func(t *testing.T) {
			if allocatorInitializationStatementListSubset(
				component.ID, component, []Statement{statement}, nil, false,
				make(map[string]bool), nil, allocatorHandledException{},
			) {
				t.Fatal("allocator initialization subset admitted a suspending timed action")
			}
		})
	}
}

func TestAllocatorInitializationSubsetTracksEscapingFunctionCallContexts(t *testing.T) {
	failure := "rapide:module:factory:exception:failure"
	component := NewComponent("factory", Interface("Factory").
		ProvidesFunction("Helper", "").
		ProvidesFunction("Leaf", "").
		Build(), nil)
	if err := component.AddExceptionDeclaration(DeclaredException(failure, "Failure")); err != nil {
		t.Fatal(err)
	}

	leaf := Function("Leaf", "").
		Do(RaiseDeclaredException("raise-failure", failure, "Failure")).
		Build()
	callLeaf := CallFunction("leaf", "Leaf")
	callLeaf.functionCall.functionKey = "leaf"
	helper := Function("Helper", "").Do(callLeaf).Build()
	callHelper := CallFunction("helper", "Helper")
	callHelper.functionCall.functionKey = "helper"
	callables := map[string]map[string]*FunctionImplementation{
		"factory": {"helper": helper, "leaf": leaf},
	}
	contained := HandleDo([]Statement{callHelper}, ExceptionHandler{Choices: []ExceptionHandlerChoice{
		HandleDeclaredException(failure, "Failure", nil, NullStatement()),
	}})
	if !allocatorInitializationStatementListSubset(
		"factory", component, []Statement{contained}, callables,
		false, make(map[string]bool), nil, allocatorHandledException{},
	) {
		t.Fatal("allocator subset rejected an escaping helper exception contained at its call site")
	}

	// The same reachable helper is valid both under the first handler and at a
	// following unprotected call. The latter produces the typed failed-module
	// initialization outcome at execution time. Validation must still visit the
	// two distinct call-site handler contexts rather than reusing one result.
	visited := make(map[string]bool)
	if !allocatorInitializationStatementListSubset(
		"factory", component, []Statement{contained, callHelper}, callables,
		false, visited, nil, allocatorHandledException{},
	) {
		t.Fatal("allocator subset rejected an ordinary function exception escaping module initialization")
	}
	if len(visited) != 4 {
		t.Fatalf("allocator call-site validation contexts=%d, want helper/leaf under protected and unprotected calls", len(visited))
	}

	// Recursive handlers can add another lexical instance of the same
	// declaration on every call. Eligibility depends on canonical catchability,
	// not unbounded stack multiplicity, so this graph must reach a fixed point.
	recursiveHelper := Function("Helper", "").Do(HandleDo(
		[]Statement{callHelper},
		ExceptionHandler{Choices: []ExceptionHandlerChoice{
			HandleDeclaredException(failure, "Failure", nil, NullStatement()),
		}},
	)).Build()
	recursiveCallables := map[string]map[string]*FunctionImplementation{
		"factory": {"helper": recursiveHelper},
	}
	if !allocatorInitializationStatementListSubset(
		"factory", component, []Statement{contained}, recursiveCallables,
		false, make(map[string]bool), nil, allocatorHandledException{},
	) {
		t.Fatal("allocator subset failed to close recursive equivalent handler context")
	}
}

func TestAllocatorGeneratorArgumentsRequireExactSpecialization(t *testing.T) {
	specialization := []ModuleGeneratorArgument{
		ModuleArgument("Seed", "Integer", int64(1)),
		ModuleArgument("Enabled", "Boolean", true),
	}
	if !allocatorGeneratorArgumentsEqual([]ModuleGeneratorArgument{
		ModuleArgument("seed", "Integer", 1),
		ModuleArgument("ENABLED", "Boolean", true),
	}, specialization) {
		t.Fatal("canonical equivalent actuals did not select the current specialization")
	}
	tests := []struct {
		name      string
		arguments []ModuleGeneratorArgument
	}{
		{name: "changed value", arguments: []ModuleGeneratorArgument{
			ModuleArgument("Seed", "Integer", 2),
			ModuleArgument("Enabled", "Boolean", true),
		}},
		{name: "missing formal", arguments: []ModuleGeneratorArgument{
			ModuleArgument("Seed", "Integer", 1),
		}},
		{name: "reordered formals", arguments: []ModuleGeneratorArgument{
			ModuleArgument("Enabled", "Boolean", true),
			ModuleArgument("Seed", "Integer", 1),
		}},
		{name: "wrong type", arguments: []ModuleGeneratorArgument{
			ModuleArgument("Seed", "Boolean", true),
			ModuleArgument("Enabled", "Boolean", true),
		}},
		{name: "duplicate formal", arguments: []ModuleGeneratorArgument{
			ModuleArgument("Seed", "Integer", 1),
			ModuleArgument("seed", "Integer", 1),
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if allocatorGeneratorArgumentsEqual(test.arguments, specialization) {
				t.Fatalf("arguments %#v unexpectedly selected %#v", test.arguments, specialization)
			}
		})
	}
}

func TestAllocatorGeneratorArgumentsAreCanonicalModelData(t *testing.T) {
	digestFor := func(value int64) string {
		architecture := NewArchitecture("allocator-argument-model")
		component := NewComponent("factory", Interface("Factory").
			ProvidesFunction("Spawn", "").Build(), nil)
		if err := component.SetModuleMembershipWithArguments(
			"FactoryModule",
			[]ModuleGeneratorArgument{ModuleArgument("Seed", "Integer", int64(1))},
			nil, nil,
		); err != nil {
			t.Fatal(err)
		}
		if err := component.AddFunctionImplementation(
			Function("Spawn", "").WithLocals(ModuleLocal("Child",
				ModuleNewValueWithArguments("Factory",
					ModuleArgument("Seed", "Integer", value)))).Build(),
		); err != nil {
			t.Fatal(err)
		}
		if err := architecture.AddComponent(component); err != nil {
			t.Fatal(err)
		}
		digest, err := architecture.DeterministicModelDigest()
		if err != nil {
			t.Fatal(err)
		}
		return digest
	}
	matching := digestFor(1)
	different := digestFor(2)
	if matching == different {
		t.Fatal("allocator generator actuals were omitted from canonical model identity")
	}
}

func TestModuleInitializationParametersAndAllocatorActualsAreCanonicalModelData(t *testing.T) {
	digestFor := func(defaultValue, actualValue int64) string {
		architecture := NewArchitecture("allocator-initialization-model")
		component := NewComponent("factory", Interface("Factory").
			OutAction("Ready", P("value", "Integer")).
			ProvidesFunction("Spawn", "").Build(), nil)
		if err := component.SetModuleMembershipWithArguments("FactoryModule", nil, nil, nil); err != nil {
			t.Fatal(err)
		}
		if err := component.DeclareState(StateReference("Current", "Integer", int64(0))); err != nil {
			t.Fatal(err)
		}
		parameters := []ModuleInitializationParameter{
			ModuleInitialParameter("Initial", "Integer", LiteralValue(defaultValue)),
		}
		if err := component.SetModuleInitializationParameters(parameters...); err != nil {
			t.Fatal(err)
		}
		if err := component.SetInitialStatements(
			CallAction("ready", "Ready", ExpressionParam("value", BoundValue("Initial"))),
		); err != nil {
			t.Fatal(err)
		}
		allocation := ModuleNewValueWithInitializationArguments(
			"Factory", nil,
			ModuleInitialArgument("Initial", "Integer", LiteralValue(actualValue)),
		)
		if err := component.AddFunctionImplementation(
			Function("Spawn", "").WithLocals(ModuleLocal("Child", allocation)).Build(),
		); err != nil {
			t.Fatal(err)
		}
		if err := architecture.AddComponent(component); err != nil {
			t.Fatal(err)
		}
		before, err := architecture.DeterministicModelDigest()
		if err != nil {
			t.Fatal(err)
		}
		parameters[0].Name = "Changed"
		parameters[0].Default = LiteralValue(int64(99))
		after, err := architecture.DeterministicModelDigest()
		if err != nil {
			t.Fatal(err)
		}
		if before != after {
			t.Fatal("caller mutation changed snapshotted initialization parameters")
		}
		return before
	}
	baseline := digestFor(1, 2)
	if digestFor(3, 2) == baseline {
		t.Fatal("different initialization defaults were omitted from canonical model identity")
	}
	if digestFor(1, 4) == baseline {
		t.Fatal("different allocator initialization actuals were omitted from canonical model identity")
	}
}

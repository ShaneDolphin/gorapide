package arch

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/pattern"
)

type allocatedModuleActual struct {
	moduleID   string
	nameID     string
	startID    gorapide.EventID
	frontier   []gorapide.EventID
	operations []stateOperationReference
}

// failedModuleInitialization is a language-level failed generator call, not a
// host error and not an ordinary exception re-raised at New's lexical call.
// The exact exception occurrence belongs to the fresh leaf child and must enter
// the poset before Section 9.9 termination, structural propagation, and
// finalization run for it and every enclosing unreturned creation caller.
type failedModuleInitialization struct {
	moduleID          string
	stateOwnerID      string
	raised            *raisedExceptionOccurrence
	canceledSchedules []string
	abandonedCallers  []failedModuleInitializationCaller
}

// failedModuleInitializationCaller identifies an enclosing fresh module whose
// own initializer was abandoned because a recursively evaluated generator call
// failed before returning a value. The leaf exception remains the one language
// occurrence; callers are recorded from the innermost fresh parent outward so
// exceptional creation finalization can be materialized deterministically.
type failedModuleInitializationCaller struct {
	moduleID     string
	stateOwnerID string
}

func resolveAllocatorInitializationArguments(
	componentID, occurrence string,
	parameters []ModuleInitializationParameter,
	arguments []ModuleInitializationArgument,
	callerBindings pattern.Bindings,
	callerCells map[string]*stateCell,
	initialControl []gorapide.EventID,
	initialOperations []stateOperationReference,
) (pattern.MatchResult, []StateReadRecord, []gorapide.EventID, []stateOperationReference, error) {
	if len(parameters) != len(arguments) {
		return pattern.MatchResult{}, nil, nil, nil, fmt.Errorf(
			"%w: allocator New at %q supplies %d initialization actuals, want %d",
			ErrInvalidDeclarativeStatement, occurrence, len(arguments), len(parameters),
		)
	}
	evaluationBindings := append(pattern.Bindings(nil), callerBindings...)
	initialBindings := make(pattern.Bindings, 0, len(parameters))
	reads := make([]StateReadRecord, 0)
	control := canonicalEventIDs(initialControl)
	operations := canonicalStateOperationReferences(initialOperations)
	for index, parameter := range parameters {
		argument := arguments[index]
		if !strings.EqualFold(argument.Name, parameter.Name) ||
			!strings.EqualFold(argument.Type, parameter.Type) {
			return pattern.MatchResult{}, nil, nil, nil, fmt.Errorf(
				"%w: allocator New at %q initialization actual %d does not match formal %s : %s",
				ErrInvalidDeclarativeStatement, occurrence, index+1, parameter.Name, parameter.Type,
			)
		}
		evaluated, err := evaluateClosedRuleValue(
			"allocator New "+occurrence+" initialization actual "+parameter.Name,
			argument.Value, evaluationBindings, callerCells,
		)
		if err != nil {
			return pattern.MatchResult{}, nil, nil, nil, err
		}
		if !valueMatchesPredefinedType(evaluated.value, parameter.Type) {
			return pattern.MatchResult{}, nil, nil, nil, fmt.Errorf(
				"%w: allocator New at %q initialization actual %q does not match %s",
				ErrInvalidDeclarativeStatement, occurrence, parameter.Name, parameter.Type,
			)
		}
		readOperations := stateOperationReferences(evaluated.reads, nil)
		dependencies := append(eventIDStrings(control), stateOperationReferenceIDs(operations)...)
		if err := addStateOperationDependencies(readOperations, dependencies...); err != nil {
			return pattern.MatchResult{}, nil, nil, nil, err
		}
		initialBindings = append(initialBindings, pattern.Binding{
			Placeholder: parameter.Name, Value: evaluated.value,
		})
		evaluationBindings = append(evaluationBindings, pattern.Binding{
			Placeholder: ModuleInitializationFormalBinding(parameter.Name), Value: evaluated.value,
		})
		sort.Slice(evaluationBindings, func(left, right int) bool {
			return evaluationBindings[left].Placeholder < evaluationBindings[right].Placeholder
		})
		reads = append(reads, evaluated.reads...)
		control = canonicalEventIDs(append(control, evaluated.causes...))
		operations = canonicalStateOperationReferences(append(operations, readOperations...))
	}
	sort.Slice(initialBindings, func(left, right int) bool {
		return initialBindings[left].Placeholder < initialBindings[right].Placeholder
	})
	return pattern.MatchResult{Bindings: initialBindings}, reads, control, operations, nil
}

func initializeAllocatedModuleState(
	templateID, moduleID string,
	model *deterministicModel,
) (map[string]*stateCell, error) {
	declarations := model.stateDeclarations[templateID]
	cells := make(map[string]*stateCell, len(declarations))
	for _, declaration := range declarations {
		values, err := gorapide.CanonicalizeParams(map[string]any{"value": declaration.Initial})
		if err != nil {
			return nil, err
		}
		history, err := newStateReferenceHistory(moduleID, declaration.Name, values["value"])
		if err != nil {
			return nil, err
		}
		cells[declaration.Name] = &stateCell{
			declaration: declaration, value: values["value"], history: history,
		}
	}
	return cells, nil
}

// allocatorNewSubset validates the bounded executable New slice. The allocator
// reevaluates an already-specialized generator: type/function
// constituents, generator-owned supported action/function self-routes, closed
// immutable predefined-scalar objects, predefined-scalar state, fresh basic
// clocks, and the bounded immediate initial/final parts may be elaborated.
// Successful non-suspending `in` actions retain the child through their exact
// future occurrences. Exact named exceptions may
// either be handled lexically or escape to the typed failed-initialization
// outcome completed by a process-owned caller, including through an unhandled
// recursive creation chain. A canonical process set may now
// be elaborated after successful initialization;
// declarative rules, dynamic module handlers, structural objects, constraints,
// related clocks, and other connection effects remain excluded.
func allocatorNewSubset(
	componentID string,
	requestedArguments []ModuleGeneratorArgument,
	runtime *functionExecutionRuntime,
) (string, gorapide.RapideModuleValue, error) {
	if runtime == nil || runtime.model == nil || runtime.lifecycle == nil {
		return "", gorapide.RapideModuleValue{}, fmt.Errorf(
			"%w: component %q has no allocator runtime", ErrInvalidDeclarativeStatement, componentID)
	}
	model := runtime.model
	templateID := runtime.templateComponentID(componentID)
	component := runtime.components[componentID]
	parent := runtime.modules[componentID]
	if component == nil || component.moduleMembership == nil || parent.Identity() == "" {
		return "", gorapide.RapideModuleValue{}, fmt.Errorf(
			"%w: component %q is not an allocated module-generator result", ErrInvalidDeclarativeStatement, componentID)
	}
	membership := component.moduleMembership
	if !allocatorGeneratorArgumentsEqual(requestedArguments, membership.GeneratorArguments) {
		return "", gorapide.RapideModuleValue{}, fmt.Errorf(
			"%w: allocator New actuals for component %q do not match its exact current generator specialization",
			ErrInvalidDeclarativeStatement, componentID)
	}
	if !allocatorObjectDenotationsSubset(membership.ObjectDenotations) ||
		len(membership.RecordObjects) != 0 ||
		len(model.rules[templateID]) != 0 ||
		(len(model.processes[templateID]) != 0 && model.moduleHandlers[templateID].Choices != nil) ||
		(len(model.processes[templateID]) != 0 && model.moduleHandlers[templateID].Else != nil) ||
		!allocatorInitializationStatementListSubset(
			templateID, component, model.initialStatements[templateID], model.callables,
			false, make(map[string]bool), nil, allocatorHandledException{},
		) ||
		model.moduleConstraints[templateID] != nil {
		return "", gorapide.RapideModuleValue{}, fmt.Errorf(
			"%w: allocator New for component %q requires the current deterministic dynamic-module specialization slice",
			ErrInvalidDeclarativeStatement, componentID)
	}
	for _, connection := range model.connections {
		if connection.Scope == ModuleConnectionScope &&
			(connection.From == templateID || connection.To == templateID) &&
			!isDynamicModuleActionRoute(templateID, connection) {
			return "", gorapide.RapideModuleValue{}, fmt.Errorf(
				"%w: allocator New for component %q can reevaluate only generator-owned supported action self-connections",
				ErrInvalidDeclarativeStatement, componentID)
		}
	}
	generator := model.staticModuleGenerators[templateID]
	if generator == "" || generator != membership.Generator {
		return "", gorapide.RapideModuleValue{}, fmt.Errorf(
			"%w: component %q has no canonical allocator generator", ErrInvalidDeclarativeStatement, componentID)
	}
	return generator, parent, nil
}

// allocatorInitializationStatementSubset admits immediate scalar state/action
// initialization plus the already-deterministic structured control nodes. Loop
// execution remains bounded by the enclosing explicit statement budget. Named
// do control uses the already-canonical lexical target graph. Effects that can
// suspend and cross-context calls remain outside this dynamic creation slice.
// A direct, untimed action actual or a declaration-ordered module-valued local
// in an allocation-free local call graph may recursively call the same
// specialized generator; the shared statement budget bounds synchronous
// elaboration depth and every child retains its own exact module identity,
// initialization frontier, and lexical/result-name lifetime.
// Non-suspending `in` actions use the ordinary clock scheduler.
// Exact exception handlers and immediate self-generated action/any interrupts
// are permitted. A process-owned handler may cross a synchronous function/New
// boundary through the retained generation-time visible poset. Direct
// process-owned protected-block allocation uses that same semantic path;
// non-process owners remain excluded.
func allocatorInitializationStatementSubset(statements []Statement) bool {
	return allocatorInitializationStatementListSubset(
		"", nil, statements, nil, false, make(map[string]bool), nil,
		allocatorHandledException{},
	)
}

type allocatorExceptionHandlerScope struct {
	declarations []string
	catchAll     bool
}

type allocatorHandledException struct {
	declaration string
	active      bool
	unknown     bool
}

func allocatorInitializationHandlerScope(
	component *Component,
	handler ExceptionHandler,
) (allocatorExceptionHandlerScope, bool, bool) {
	scope := allocatorExceptionHandlerScope{catchAll: handler.Else != nil}
	hasInterrupt := false
	hasAny := false
	for _, choice := range handler.Choices {
		switch {
		case choice.Any && choice.Action == "" && choice.Exception == "":
			hasInterrupt = true
			hasAny = true
			scope.catchAll = true
		case !choice.Any && choice.Action != "" && choice.Exception == "":
			hasInterrupt = true
			if component != nil {
				action, exists := handlerActionDeclaration(component, choice.Action)
				if !exists || (action.Kind != OutAction && action.Kind != PrivateAction) {
					return allocatorExceptionHandlerScope{}, false, false
				}
			}
		case !choice.Any && choice.Action == "" && choice.Exception != "" &&
			choice.ExceptionDeclaration != "":
			scope.declarations = append(scope.declarations, choice.ExceptionDeclaration)
		default:
			return allocatorExceptionHandlerScope{}, false, false
		}
	}
	if hasAny && (len(handler.Choices) != 1 || handler.Else != nil) {
		return allocatorExceptionHandlerScope{}, false, false
	}
	sort.Strings(scope.declarations)
	return scope, hasInterrupt, len(handler.Choices) != 0 || handler.Else != nil
}

func allocatorInitializationStatementListSubset(
	componentID string,
	component *Component,
	statements []Statement,
	callables map[string]map[string]*FunctionImplementation,
	inFunction bool,
	visitedFunctions map[string]bool,
	handlers []allocatorExceptionHandlerScope,
	handled allocatorHandledException,
) bool {
	for _, statement := range statements {
		switch statement.kind {
		case AssignmentStatement:
			if ruleValueContainsAllocatorNew(statement.assignment.Value) {
				return false
			}
		case AssertStatementKind:
			if ruleValueContainsAllocatorNew(statement.condition) {
				return false
			}
		case NullStatementKind:
			continue
		case RaiseStatementKind:
			if statement.exceptionDeclaration == "" ||
				(statement.raiseCondition != nil && ruleValueContainsAllocatorNew(*statement.raiseCondition)) {
				return false
			}
			for _, parameter := range statement.output.Parameters {
				if ruleValueContainsAllocatorNew(parameter.Value) {
					return false
				}
			}
		case ReraiseStatementKind:
			if !handled.active ||
				(statement.raiseCondition != nil && ruleValueContainsAllocatorNew(*statement.raiseCondition)) {
				return false
			}
		case EventCallStatement:
			if statement.timing != nil && statement.timing.Kind != InTimingClause {
				return false
			}
			for _, parameter := range statement.output.Parameters {
				containsNew := ruleValueContainsAllocatorNew(parameter.Value)
				if containsNew && (parameter.Value.kind != RuleNewValue || statement.timing != nil) {
					return false
				}
			}
			if component != nil {
				action, exists := handlerActionDeclaration(component, statement.output.Action)
				if !exists || (action.Kind != OutAction && action.Kind != PrivateAction) {
					return false
				}
			}
		case FunctionCallStatement:
			if !allocatorInitializationFunctionCallSubset(
				componentID, component, statement.functionCall, callables,
				visitedFunctions, handlers,
			) {
				return false
			}
		case IfStatementKind:
			if ruleValueContainsAllocatorNew(statement.condition) ||
				!allocatorInitializationStatementListSubset(
					componentID, component, statement.thenBranch, callables,
					inFunction, visitedFunctions, handlers, handled,
				) ||
				!allocatorInitializationStatementListSubset(
					componentID, component, statement.elseBranch, callables,
					inFunction, visitedFunctions, handlers, handled,
				) {
				return false
			}
		case CaseStatementKind:
			if ruleValueContainsAllocatorNew(statement.caseValue) {
				return false
			}
			for _, alternative := range statement.caseAlts {
				for _, choice := range alternative.choices {
					if ruleValueContainsAllocatorNew(choice.value) ||
						ruleValueContainsAllocatorNew(choice.first) ||
						ruleValueContainsAllocatorNew(choice.last) {
						return false
					}
				}
				if !allocatorInitializationStatementListSubset(
					componentID, component, alternative.body, callables,
					inFunction, visitedFunctions, handlers, handled,
				) {
					return false
				}
			}
			if !allocatorInitializationStatementListSubset(
				componentID, component, statement.caseDefault, callables,
				inFunction, visitedFunctions, handlers, handled,
			) {
				return false
			}
		case LoopStatementKind:
			if !allocatorInitializationStatementListSubset(
				componentID, component, statement.loopBody, callables,
				inFunction, visitedFunctions, handlers, handled,
			) {
				return false
			}
		case DoBlockStatementKind:
			if !allocatorInitializationStatementListSubset(
				componentID, component, statement.handledBody, callables,
				inFunction, visitedFunctions, handlers, handled,
			) {
				return false
			}
		case HandlerBlockStatementKind:
			scope, _, valid := allocatorInitializationHandlerScope(
				component, statement.handler,
			)
			if !valid {
				return false
			}
			protectedHandlers := append(
				append([]allocatorExceptionHandlerScope(nil), handlers...), scope,
			)
			if !allocatorInitializationStatementListSubset(
				componentID, component, statement.handledBody, callables,
				inFunction, visitedFunctions, protectedHandlers, handled,
			) {
				return false
			}
			for _, choice := range statement.handler.Choices {
				choiceHandled := allocatorHandledException{}
				if choice.Exception != "" && choice.ExceptionDeclaration != "" {
					choiceHandled = allocatorHandledException{
						declaration: choice.ExceptionDeclaration, active: true,
					}
				}
				if !allocatorInitializationStatementListSubset(
					componentID, component, choice.Statements, callables,
					inFunction, visitedFunctions, handlers, choiceHandled,
				) {
					return false
				}
			}
			if statement.handler.Else != nil &&
				!allocatorInitializationStatementListSubset(
					componentID, component, statement.handler.Else, callables,
					inFunction, visitedFunctions, handlers,
					allocatorHandledException{active: true, unknown: true},
				) {
				return false
			}
		case ForStatementKind:
			if statement.iteratorKind != rangeStatementIteratorKind ||
				statement.iteratorType != "Integer" ||
				ruleValueContainsAllocatorNew(statement.iteratorFirst) ||
				ruleValueContainsAllocatorNew(statement.iteratorLast) ||
				!allocatorInitializationStatementListSubset(
					componentID, component, statement.loopBody, callables,
					inFunction, visitedFunctions, handlers, handled,
				) {
				return false
			}
		case GeneralForStatementKind:
			for _, expression := range []ExecutableObjectExpression{
				statement.forInitial, statement.forTest, statement.forNext,
			} {
				if !allocatorInitializationObjectExpressionSubset(
					componentID, component, expression, callables,
					visitedFunctions, handlers,
				) {
					return false
				}
			}
			if !allocatorInitializationStatementListSubset(
				componentID, component, statement.loopBody, callables,
				inFunction, visitedFunctions, handlers, handled,
			) {
				return false
			}
		case ExitStatementKind, NextStatementKind:
			if ruleValueContainsAllocatorNew(statement.condition) {
				return false
			}
		case ReturnStatementKind:
			if !inFunction || (statement.returnValue != nil &&
				ruleValueContainsAllocatorNew(*statement.returnValue)) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func allocatorInitializationObjectExpressionSubset(
	componentID string,
	component *Component,
	expression ExecutableObjectExpression,
	callables map[string]map[string]*FunctionImplementation,
	visitedFunctions map[string]bool,
	handlers []allocatorExceptionHandlerScope,
) bool {
	switch expression.kind {
	case ObjectValueExpression:
		return !ruleValueContainsAllocatorNew(expression.value)
	case ObjectAssignmentExpression:
		return !ruleValueContainsAllocatorNew(expression.assignment.Value)
	case ObjectFunctionExpression:
		return allocatorInitializationFunctionCallSubset(
			componentID, component, expression.call, callables,
			visitedFunctions, handlers,
		)
	default:
		return false
	}
}

func allocatorInitializationFunctionCallSubset(
	componentID string,
	component *Component,
	call FunctionCall,
	callables map[string]map[string]*FunctionImplementation,
	visitedFunctions map[string]bool,
	handlers []allocatorExceptionHandlerScope,
) bool {
	for _, argument := range call.Arguments {
		if ruleValueContainsAllocatorNew(argument.Value) {
			return false
		}
	}
	implementation := callables[componentID][call.functionKey]
	if implementation == nil {
		return false
	}
	targetComponentID := implementation.targetComponent
	if targetComponentID == "" {
		targetComponentID = componentID
	}
	localProvided := implementation.connectionID == "" &&
		targetComponentID == componentID && len(implementation.routeAliases) == 0
	if !localProvided && !isDynamicModuleFunctionRoute(componentID, implementation) {
		return false
	}
	// Executable LRM 8.3.1 re-raises an exception that escapes a function at
	// the point of its call. The initializer's lexical handler stack's
	// canonical catchability is therefore part of this validation state.
	visitKey := componentID + "\x00" + call.functionKey +
		"\x00" + allocatorExceptionHandlerStackKey(handlers)
	if visitedFunctions[visitKey] {
		return true
	}
	visitedFunctions[visitKey] = true
	if implementation.Return != nil && ruleValueContainsAllocatorNew(*implementation.Return) {
		return false
	}
	return allocatorInitializationStatementListSubset(
		componentID, component, implementation.Statements, callables,
		true, visitedFunctions, handlers, allocatorHandledException{},
	)
}

func allocatorExceptionHandlerStackKey(handlers []allocatorExceptionHandlerScope) string {
	declarations := make(map[string]bool)
	catchAll := false
	for _, handler := range handlers {
		catchAll = catchAll || handler.catchAll
		for _, declaration := range handler.declarations {
			declarations[declaration] = true
		}
	}
	ordered := make([]string, 0, len(declarations))
	for declaration := range declarations {
		ordered = append(ordered, declaration)
	}
	sort.Strings(ordered)
	var builder strings.Builder
	if catchAll {
		builder.WriteByte('*')
	}
	for _, declaration := range ordered {
		builder.WriteByte('|')
		builder.WriteString(declaration)
	}
	return builder.String()
}

func ruleValueContainsAllocatorNew(value RuleValue) bool {
	if value.kind == RuleNewValue {
		return true
	}
	for _, operand := range value.operands {
		if ruleValueContainsAllocatorNew(operand) {
			return true
		}
	}
	return false
}

func allocatorGeneratorArgumentsEqual(
	requested, specialization []ModuleGeneratorArgument,
) bool {
	requested, _, requestedErr := canonicalizeNewGeneratorArguments(requested)
	specialization, _, specializationErr := canonicalizeNewGeneratorArguments(specialization)
	if requestedErr != nil || specializationErr != nil || len(requested) != len(specialization) {
		return false
	}
	for index := range requested {
		if !strings.EqualFold(requested[index].Name, specialization[index].Name) ||
			!strings.EqualFold(requested[index].Type, specialization[index].Type) ||
			!reflect.DeepEqual(requested[index].Value, specialization[index].Value) {
			return false
		}
	}
	return true
}

func allocatorObjectDenotationsSubset(denotations []gorapide.RapideObjectDenotation) bool {
	for _, denotation := range denotations {
		typeName, predefined := denotation.Type().PredefinedName()
		value, err := denotation.Value()
		if err != nil || !predefined || !gorapide.IsSupportedPredefinedType(typeName) ||
			!gorapide.CanonicalValueMatchesPredefinedType(value, typeName) {
			return false
		}
	}
	return true
}

func allocateModuleNewActual(
	componentID, modelDigest, occurrence, resultType string,
	arguments []ModuleGeneratorArgument,
	initializationArguments []ModuleInitializationArgument,
	bindings pattern.Bindings,
	cells map[string]*stateCell,
	runtime *functionExecutionRuntime,
	execution *statementExecution,
) (gorapide.RapideModuleValue, allocatedModuleActual, error) {
	generator, parent, err := allocatorNewSubset(componentID, arguments, runtime)
	if err != nil {
		return gorapide.RapideModuleValue{}, allocatedModuleActual{}, err
	}
	if resultType == "" || occurrence == "" || execution == nil || execution.clocks == nil {
		return gorapide.RapideModuleValue{}, allocatedModuleActual{}, fmt.Errorf(
			"%w: allocator New at %q is incomplete", ErrInvalidDeclarativeStatement, occurrence)
	}
	templateID := runtime.templateComponentID(componentID)
	initialMatch, parameterReads, causes, startOperations, err := resolveAllocatorInitializationArguments(
		componentID, occurrence, runtime.model.initializationParameters[templateID],
		initializationArguments, bindings, cells, execution.control, execution.pendingOperations,
	)
	if err != nil {
		return gorapide.RapideModuleValue{}, allocatedModuleActual{}, err
	}
	if len(causes) == 0 {
		return gorapide.RapideModuleValue{}, allocatedModuleActual{}, fmt.Errorf(
			"%w: allocator New at %q has no owning execution frontier", ErrInvalidDeclarativeStatement, occurrence)
	}
	module, err := gorapide.NewRapideModuleValue(gorapide.ModuleAllocationProvenance{
		Profile: CompatibilityProfile, Model: modelDigest, Parent: parent.Identity(),
		Generator: generator, Occurrence: occurrence, Causes: causes,
	})
	if err != nil {
		return gorapide.RapideModuleValue{}, allocatedModuleActual{}, err
	}
	start, err := gorapide.NewDeterministicEvent(gorapide.EventProvenance{
		Profile: CompatibilityProfile, Model: modelDigest, Instance: module.Identity(),
		Action: ArchitectureStartAction, Occurrence: "allocator-new:" + generator + ":start",
		Causes: causes,
	}, nil)
	if err != nil {
		return gorapide.RapideModuleValue{}, allocatedModuleActual{}, err
	}
	if err := addStateOperationSuccessors(startOperations, string(start.ID)); err != nil {
		return gorapide.RapideModuleValue{}, allocatedModuleActual{}, err
	}
	if err := runtime.lifecycle.addModule(moduleLifecycleRuntime{
		moduleID: module.Identity(), kind: "allocator-module", parent: parent.Identity(),
		generator: generator, occurrence: occurrence, startEventID: start.ID,
		state: ModuleCompletedState, initializing: true,
	}); err != nil {
		return gorapide.RapideModuleValue{}, allocatedModuleActual{}, err
	}
	if runtime.contexts == nil {
		return gorapide.RapideModuleValue{}, allocatedModuleActual{}, fmt.Errorf(
			"%w: allocator New at %q has no communication Context runtime", ErrInvalidDeclarativeStatement, occurrence)
	}
	if err := runtime.contexts.addInitialModule(module.Identity(), parent.Identity(), start.ID); err != nil {
		return gorapide.RapideModuleValue{}, allocatedModuleActual{}, err
	}
	component := runtime.components[componentID]
	if component == nil || runtime.moduleTemplates == nil {
		return gorapide.RapideModuleValue{}, allocatedModuleActual{}, fmt.Errorf(
			"%w: allocator New at %q has no module template runtime", ErrInvalidDeclarativeStatement, occurrence)
	}
	runtime.components[module.Identity()] = component
	runtime.modules[module.Identity()] = module
	runtime.moduleParents[module.Identity()] = parent.Identity()
	runtime.moduleTemplates[module.Identity()] = templateID
	if err := execution.clocks.addAllocatedComponentClocks(
		templateID, module.Identity(), runtime.model.basicClocks[templateID],
	); err != nil {
		return gorapide.RapideModuleValue{}, allocatedModuleActual{}, err
	}
	childState, err := initializeAllocatedModuleState(templateID, module.Identity(), runtime.model)
	if err != nil {
		return gorapide.RapideModuleValue{}, allocatedModuleActual{}, err
	}
	runtime.state[module.Identity()] = childState
	if err := registerAllocatedModuleFunctions(templateID, module.Identity(), runtime); err != nil {
		return gorapide.RapideModuleValue{}, allocatedModuleActual{}, err
	}
	if err := registerAllocatedModuleActionConnections(templateID, module.Identity(), runtime); err != nil {
		return gorapide.RapideModuleValue{}, allocatedModuleActual{}, err
	}
	startState := cloneStateCells(childState)
	execution.generated = append(execution.generated, generatedRuleOutput{
		localID: "new@" + occurrence + "/start", event: start, causes: causes,
		stateSnapshot: startState,
		observationSnapshots: map[string]map[string]*stateCell{
			module.Identity(): startState,
		},
	})
	execution.reads = append(execution.reads, qualifyStateReads(componentID, parameterReads)...)
	frontier := []gorapide.EventID{start.ID}
	childOperations := make([]stateOperationReference, 0)
	if statements := rebindAllocatedModuleStatementClocks(
		runtime.model.initialStatements[templateID], templateID, module.Identity(),
	); len(statements) != 0 {
		matchDigest, err := pattern.SemanticDigestMatches([]pattern.MatchResult{initialMatch})
		if err != nil {
			return gorapide.RapideModuleValue{}, allocatedModuleActual{}, err
		}
		rule := &DeclarativeRule{
			ID:                  "module-initial/" + templateID,
			Process:             RulePipeProcess,
			Body:                &RuleBody{Statements: statements},
			initializationOwned: true,
		}
		childExecution, err := executeRuleStatements(
			module.Identity(), component, rule, initialMatch, matchDigest, modelDigest,
			statements, runtime, childState, frontier, execution.budget, execution.clocks,
			"initial:"+module.Identity(), nil, execution,
		)
		if err != nil {
			return gorapide.RapideModuleValue{}, allocatedModuleActual{}, fmt.Errorf(
				"%w: allocated module %q initial part: %w",
				ErrInvalidDeclarativeStatement, module.Identity(), err,
			)
		}
		if childExecution.exitProcess && childExecution.initializationFailure == nil &&
			childExecution.raised == nil &&
			childExecution.pendingInterrupt == nil {
			return gorapide.RapideModuleValue{}, allocatedModuleActual{}, fmt.Errorf(
				"%w: allocated module %q initial part escaped the immediate initialization subset",
				ErrInvalidDeclarativeStatement, module.Identity(),
			)
		}
		for _, output := range childExecution.generated {
			output.localID = "new@" + occurrence + "/initial/" + output.localID
			execution.generated = append(execution.generated, output)
		}
		execution.reads = append(execution.reads,
			qualifyStateReads(module.Identity(), childExecution.reads)...,
		)
		execution.writes = append(execution.writes,
			qualifyStateWrites(module.Identity(), childExecution.writes)...,
		)
		execution.canceledSchedules = append(
			execution.canceledSchedules, childExecution.canceledSchedules...,
		)
		frontier = canonicalEventIDs(childExecution.control)
		childOperations = canonicalStateOperationReferences(childExecution.pendingOperations)
		if childExecution.initializationFailure != nil {
			failure := childExecution.initializationFailure
			failure.abandonedCallers = append(
				failure.abandonedCallers,
				failedModuleInitializationCaller{
					moduleID: module.Identity(), stateOwnerID: module.Identity(),
				},
			)
			failure.canceledSchedules = append(
				failure.canceledSchedules, scheduledActionIDs(childExecution.scheduled)...,
			)
			execution.control = append([]gorapide.EventID(nil), frontier...)
			execution.pendingOperations = append([]stateOperationReference(nil), childOperations...)
			execution.initializationFailure = failure
			return gorapide.RapideModuleValue{}, allocatedModuleActual{}, nil
		}
		if childExecution.pendingInterrupt != nil {
			if runtime.startupFrontiers != nil {
				runtime.startupFrontiers[module.Identity()] = append([]gorapide.EventID(nil), frontier...)
			}
			execution.control = append([]gorapide.EventID(nil), frontier...)
			execution.pendingOperations = append([]stateOperationReference(nil), childOperations...)
			execution.pendingInterrupt = childExecution.pendingInterrupt
			execution.canceledSchedules = append(
				execution.canceledSchedules, scheduledActionIDs(childExecution.scheduled)...,
			)
			// The interrupt abandons New before its allocator result is returned.
			// The fresh child has no external name and no elaborated processes, so
			// losing its implicit creation-time Self name finalizes it normally at
			// the exact interrupt frontier. Unlike exceptional initialization, its
			// ordinary final part remains eligible under Executable LRM 9.9.
			if err := runtime.lifecycle.completeInitialization(module.Identity()); err != nil {
				return gorapide.RapideModuleValue{}, allocatedModuleActual{}, err
			}
			finalized, err := releaseModuleName(
				modelDigest, "self-name:"+module.Identity(), frontier, runtime, execution,
			)
			if err != nil {
				return gorapide.RapideModuleValue{}, allocatedModuleActual{}, err
			}
			if finalized != module.Identity() {
				return gorapide.RapideModuleValue{}, allocatedModuleActual{}, fmt.Errorf(
					"%w: interrupted allocator New left unreturned module %q namable",
					ErrInvalidDeclarativeStatement, module.Identity(),
				)
			}
			return gorapide.RapideModuleValue{}, allocatedModuleActual{}, nil
		}
		if childExecution.raised != nil {
			if runtime.startupFrontiers != nil {
				runtime.startupFrontiers[module.Identity()] = append([]gorapide.EventID(nil), frontier...)
			}
			execution.control = append([]gorapide.EventID(nil), frontier...)
			execution.pendingOperations = append([]stateOperationReference(nil), childOperations...)
			execution.initializationFailure = &failedModuleInitialization{
				moduleID: module.Identity(), stateOwnerID: module.Identity(),
				raised:            childExecution.raised,
				canceledSchedules: scheduledActionIDs(childExecution.scheduled),
			}
			return gorapide.RapideModuleValue{}, allocatedModuleActual{}, nil
		}
		if len(childExecution.scheduled) != 0 {
			if err := runtime.lifecycle.addExternalRoot(scheduledActionNameOwner); err != nil {
				return gorapide.RapideModuleValue{}, allocatedModuleActual{}, err
			}
			for index := range childExecution.scheduled {
				action := &childExecution.scheduled[index]
				action.retentionNameID = "scheduled-action:" + action.scheduleID
				if err := runtime.lifecycle.addName(moduleNameRuntime{
					nameID: action.retentionNameID, moduleID: module.Identity(),
					owner: scheduledActionNameOwner, name: action.scheduleID, kind: "scheduled-action",
					acquiredAfter: action.acquiredAfter,
				}); err != nil {
					return gorapide.RapideModuleValue{}, allocatedModuleActual{}, err
				}
			}
			execution.scheduled = append(execution.scheduled, childExecution.scheduled...)
			if runtime.frontiers == nil {
				return gorapide.RapideModuleValue{}, allocatedModuleActual{}, fmt.Errorf(
					"%w: allocated module %q has no causal frontier runtime",
					ErrInvalidDeclarativeStatement, module.Identity(),
				)
			}
			runtime.frontiers.set("initial:"+module.Identity(), frontier)
		}
	}
	if runtime.startupFrontiers != nil {
		runtime.startupFrontiers[module.Identity()] = append([]gorapide.EventID(nil), frontier...)
	}
	if err := runtime.lifecycle.completeInitialization(module.Identity()); err != nil {
		return gorapide.RapideModuleValue{}, allocatedModuleActual{}, err
	}
	if err := registerAllocatedModuleProcesses(
		templateID, module.Identity(), frontier, childOperations, runtime,
	); err != nil {
		return gorapide.RapideModuleValue{}, allocatedModuleActual{}, err
	}
	nameID := "allocator-result:" + module.Identity()
	if err := runtime.lifecycle.addName(moduleNameRuntime{
		nameID: nameID, moduleID: module.Identity(), owner: parent.Identity(),
		name: "New", kind: "allocator-result", acquiredAfter: frontier,
	}); err != nil {
		return gorapide.RapideModuleValue{}, allocatedModuleActual{}, err
	}
	parameterOperations := stateOperationReferences(parameterReads, nil)
	return module, allocatedModuleActual{
		moduleID: module.Identity(), nameID: nameID, startID: start.ID,
		frontier:   frontier,
		operations: canonicalStateOperationReferences(append(parameterOperations, childOperations...)),
	}, nil
}

// registerAllocatedModuleFunctions instantiates the generator's local provided
// bodies and generator-owned basic function self-routes at the concrete
// allocation identity. Architecture aliases and cross-component routes are not
// copied into the fresh module.
func registerAllocatedModuleFunctions(
	templateID, moduleID string,
	runtime *functionExecutionRuntime,
) error {
	if runtime == nil || runtime.model == nil || runtime.callables == nil {
		return fmt.Errorf("%w: allocated module %q has no function runtime", ErrInvalidDeclarativeStatement, moduleID)
	}
	catalog := runtime.model.callables[templateID]
	instantiated := make(map[string]*FunctionImplementation, len(catalog))
	for key, implementation := range catalog {
		if implementation == nil {
			return fmt.Errorf("%w: allocated module %q has nil function %q", ErrInvalidDeclarativeStatement, moduleID, key)
		}
		if implementation.connectionID != "" && !isDynamicModuleFunctionRoute(templateID, implementation) {
			continue
		}
		copy := copyFunctionImplementation(implementation)
		copy.Statements = rebindAllocatedModuleStatementClocks(
			copy.Statements, templateID, moduleID,
		)
		copy.targetComponent = moduleID
		if copy.targetName == "" {
			copy.targetName = copy.Name
		}
		for index := range copy.routeAliases {
			copy.routeAliases[index].ComponentID = moduleID
		}
		instantiated[key] = copy
	}
	runtime.callables[moduleID] = instantiated
	return nil
}

func rebindAllocatedModuleStatementClocks(
	statements []Statement,
	templateID, moduleID string,
) []Statement {
	result := copyStatements(statements)
	var rebind func([]Statement)
	rebind = func(list []Statement) {
		for index := range list {
			statement := &list[index]
			if statement.timing != nil {
				prefix := templateID + "."
				if strings.HasPrefix(statement.timing.Clock, prefix) {
					statement.timing.Clock = moduleID + statement.timing.Clock[len(templateID):]
				}
			}
			rebind(statement.thenBranch)
			rebind(statement.elseBranch)
			rebind(statement.loopBody)
			for alternativeIndex := range statement.caseAlts {
				rebind(statement.caseAlts[alternativeIndex].body)
			}
			rebind(statement.caseDefault)
			rebind(statement.handledBody)
			for choiceIndex := range statement.handler.Choices {
				rebind(statement.handler.Choices[choiceIndex].Statements)
			}
			rebind(statement.handler.Else)
		}
	}
	rebind(result)
	return result
}

func rebindAllocatedProcessClocks(
	declaration *DeclarativeProcess,
	templateID, moduleID string,
) *DeclarativeProcess {
	if declaration == nil {
		return nil
	}
	result := &DeclarativeProcess{
		ID: declaration.ID, Initial: declaration.Initial,
		States: make([]ProcessState, len(declaration.States)),
	}
	for stateIndex, state := range declaration.States {
		copied := copyProcessState(state)
		for alternativeIndex := range copied.Alternatives {
			body := copyRuleBody(copied.Alternatives[alternativeIndex].Body)
			if body != nil {
				body.Statements = rebindAllocatedModuleStatementClocks(
					body.Statements, templateID, moduleID,
				)
			}
			copied.Alternatives[alternativeIndex].Body = body
		}
		if copied.Else != nil {
			elseBranch := copyAwaitAlternative(*copied.Else)
			body := copyRuleBody(elseBranch.Body)
			if body != nil {
				body.Statements = rebindAllocatedModuleStatementClocks(
					body.Statements, templateID, moduleID,
				)
			}
			elseBranch.Body = body
			copied.Else = &elseBranch
		}
		result.States[stateIndex] = copied
	}
	return result
}

// registerAllocatedModuleProcesses performs generator-call step five from
// Executable LRM 9.4.1 only after constituent elaboration and initialization
// have completed successfully. Each process receives the fresh module identity,
// its own exact causal owner, the complete initialization frontier, and copied
// clock references rebound to the allocation. The sealed template is unchanged.
func registerAllocatedModuleProcesses(
	templateID, moduleID string,
	frontier []gorapide.EventID,
	operations []stateOperationReference,
	runtime *functionExecutionRuntime,
) error {
	if runtime == nil || runtime.model == nil || runtime.lifecycle == nil ||
		runtime.frontiers == nil || runtime.processes == nil {
		return fmt.Errorf(
			"%w: allocated module %q has no dynamic process runtime",
			ErrInvalidDeclarativeStatement, moduleID,
		)
	}
	declarations := runtime.model.processes[templateID]
	if len(declarations) == 0 {
		return nil
	}
	if len(runtime.processes[moduleID]) != 0 {
		return fmt.Errorf(
			"%w: allocated module %q processes are already registered",
			ErrInvalidDeclarativeStatement, moduleID,
		)
	}
	frontier = canonicalEventIDs(frontier)
	if len(frontier) == 0 {
		return fmt.Errorf(
			"%w: allocated module %q process elaboration has no initialization frontier",
			ErrInvalidDeclarativeStatement, moduleID,
		)
	}
	for _, declaration := range declarations {
		instantiated := rebindAllocatedProcessClocks(declaration, templateID, moduleID)
		if instantiated == nil {
			return fmt.Errorf(
				"%w: allocated module %q has a nil canonical process declaration",
				ErrInvalidDeclarativeStatement, moduleID,
			)
		}
		process := newProcessRuntime(moduleID, instantiated, frontier, operations, true)
		runtime.processes[moduleID] = append(runtime.processes[moduleID], process)
		runtime.frontiers.set(process.causalOwner, frontier)
	}
	if err := runtime.lifecycle.setState(moduleID, ModuleRunningState); err != nil {
		return err
	}
	return nil
}

// isDynamicModuleActionRoute recognizes the action-connection subset that can
// be reevaluated from an allocator generator without importing architecture
// wiring: a closed, unqualified singleton basic/pipe/agent rule, nonempty
// unqualified compound pipe/agent rule, or direct module-qualified singleton
// rule on the generator's own returned interface.
func isDynamicModuleActionRoute(templateID string, connection *Connection) bool {
	if connection == nil || connection.Scope != ModuleConnectionScope ||
		connection.Kind < BasicConnection || connection.Kind > AgentConnection ||
		connection.From != templateID ||
		connection.To != templateID {
		return false
	}
	if connection.Kind == BasicConnection {
		if !pattern.IsUnqualifiedSingleEventPattern(connection.Trigger) &&
			!pattern.IsModuleQualifiedSingleEventPattern(connection.Trigger) {
			return false
		}
		_, err := pattern.DeterministicSingleEventKey(connection.Trigger)
		return err == nil
	}
	if pattern.IsModuleQualifiedSingleEventPattern(connection.Trigger) {
		_, err := pattern.DeterministicSingleEventKey(connection.Trigger)
		return err == nil
	}
	return pattern.IsUnqualifiedNonemptyEventPattern(connection.Trigger) ||
		pattern.IsContextualNonemptyEventPattern(connection.Trigger)
}

// registerAllocatedModuleActionConnections rebinds only generator constituents
// selected by isDynamicModuleActionRoute. The canonical template declarations
// and every other execution retain their original endpoints.
func registerAllocatedModuleActionConnections(
	templateID, moduleID string,
	runtime *functionExecutionRuntime,
) error {
	if runtime == nil || runtime.model == nil || runtime.connections == nil {
		return fmt.Errorf("%w: allocated module %q has no action-connection runtime", ErrInvalidDeclarativeStatement, moduleID)
	}
	for _, connection := range runtime.model.connections {
		if !isDynamicModuleActionRoute(templateID, connection) {
			continue
		}
		copy := copyExecutionConnection(connection)
		copy.From = moduleID
		copy.To = moduleID
		runtime.connections = append(runtime.connections, copy)
	}
	sort.SliceStable(runtime.connections, func(i, j int) bool {
		left := runtime.connections[i].ID + "\x00" + runtime.connections[i].From + "\x00" + runtime.connections[i].To
		right := runtime.connections[j].ID + "\x00" + runtime.connections[j].From + "\x00" + runtime.connections[j].To
		return left < right
	})
	return nil
}

func releaseAllocatedModuleActual(
	modelDigest string,
	actual allocatedModuleActual,
	after gorapide.EventID,
	runtime *functionExecutionRuntime,
	execution *statementExecution,
) error {
	_, err := releaseModuleName(
		modelDigest, actual.nameID, []gorapide.EventID{after}, runtime, execution,
	)
	if err != nil {
		return err
	}
	// A successful deferred initializer action owns a separate scheduler name.
	// Losing the expression-local allocator result therefore need not finalize
	// the module at this occurrence; the final scheduled-name release will do so.
	return nil
}

// releaseModuleName records one language-level loss. If this was the final
// reachable name of a completed or terminated module, it materializes Finish
// from the complete loss frontier without changing the enclosing statement's
// control frontier.
func releaseModuleName(
	modelDigest, nameID string,
	after []gorapide.EventID,
	runtime *functionExecutionRuntime,
	execution *statementExecution,
) (string, error) {
	if runtime == nil || runtime.lifecycle == nil || execution == nil {
		return "", fmt.Errorf("%w: module name %q has no lifecycle execution runtime", ErrInvalidDeclarativeStatement, nameID)
	}
	moduleID, causes, err := runtime.lifecycle.releaseName(nameID, after)
	if err != nil || moduleID == "" {
		return moduleID, err
	}
	if err := materializeModuleFinalization(
		modelDigest, moduleID, causes, nameID, runtime, execution,
	); err != nil {
		return "", err
	}
	return moduleID, nil
}

func materializeModuleFinalization(
	modelDigest, moduleID string,
	causes []gorapide.EventID,
	localID string,
	runtime *functionExecutionRuntime,
	execution *statementExecution,
) error {
	if runtime == nil || runtime.lifecycle == nil || execution == nil || moduleID == "" {
		return fmt.Errorf(
			"%w: finalized module %q has no lifecycle execution runtime",
			ErrInvalidDeclarativeStatement, moduleID,
		)
	}
	causes = maximalGeneratedEventFrontier(causes, execution.generated)
	finalCauses, err := executeModuleFinalPart(
		moduleID, causes, modelDigest, runtime, execution,
	)
	if err != nil {
		return err
	}
	if len(finalCauses) != 0 {
		causes = finalCauses
	}
	finish, err := gorapide.NewDeterministicEvent(gorapide.EventProvenance{
		Profile: CompatibilityProfile, Model: modelDigest, Instance: moduleID,
		Action: ModuleFinishAction, Occurrence: "module=" + moduleID + "|finish",
		Causes: causes,
	}, nil)
	if err != nil {
		return err
	}
	finalState := cloneStateCells(runtime.state[moduleID])
	execution.generated = append(execution.generated, generatedRuleOutput{
		localID: "finish@" + localID, event: finish, causes: causes,
		stateSnapshot:        finalState,
		observationSnapshots: map[string]map[string]*stateCell{moduleID: finalState},
	})
	if err := runtime.lifecycle.setFinish(moduleID, finish.ID); err != nil {
		return err
	}
	if runtime.contexts != nil {
		if err := runtime.contexts.closeFinalizedModule(moduleID, causes); err != nil {
			return err
		}
	}
	return nil
}

func maximalGeneratedEventFrontier(
	frontier []gorapide.EventID,
	generated []generatedRuleOutput,
) []gorapide.EventID {
	frontier = canonicalEventIDs(frontier)
	causes := make(map[gorapide.EventID][]gorapide.EventID, len(generated))
	for _, output := range generated {
		if output.event != nil {
			causes[output.event.ID] = canonicalEventIDs(output.causes)
		}
	}
	result := make([]gorapide.EventID, 0, len(frontier))
	for _, candidate := range frontier {
		dominated := false
		for _, later := range frontier {
			if candidate != later && generatedEventPrecedes(candidate, later, causes, nil) {
				dominated = true
				break
			}
		}
		if !dominated {
			result = append(result, candidate)
		}
	}
	return canonicalEventIDs(result)
}

func generatedEventPrecedes(
	earlier, later gorapide.EventID,
	causes map[gorapide.EventID][]gorapide.EventID,
	visiting map[gorapide.EventID]bool,
) bool {
	if earlier == "" || later == "" || earlier == later {
		return false
	}
	if visiting == nil {
		visiting = make(map[gorapide.EventID]bool)
	}
	if visiting[later] {
		return false
	}
	visiting[later] = true
	defer delete(visiting, later)
	for _, cause := range causes[later] {
		if cause == earlier || generatedEventPrecedes(earlier, cause, causes, visiting) {
			return true
		}
	}
	return false
}

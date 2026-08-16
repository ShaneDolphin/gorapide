package arch

import (
	"fmt"
	"sort"
	"strconv"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/pattern"
)

// MaxFiniteRangeIteratorCardinality is the compatibility-profile bound for
// materializing one procedural Range(Integer) iterator. The bound is semantic
// policy and is checked before any loop body executes.
const MaxFiniteRangeIteratorCardinality uint64 = 256

type finiteRangeIterator struct {
	module        gorapide.RapideModuleValue
	items         []any
	itemType      string
	next          uint64
	lifecycleName string
}

func initializeFiniteRangeIterator(
	componentID string,
	rule *DeclarativeRule,
	match pattern.MatchResult,
	matchDigest, modelDigest, statementPath string,
	statement Statement,
	functionRuntime *functionExecutionRuntime,
	cells map[string]*stateCell,
	execution *statementExecution,
) (*finiteRangeIterator, error) {
	first, err := evaluateClosedRuleValue(
		rule.ID+" statement "+statementPath+" iterator first",
		statement.iteratorFirst, match.Bindings, cells,
	)
	if err != nil {
		return nil, err
	}
	if err := incorporateEvaluatedStateReads(execution, first.reads, first.causes); err != nil {
		return nil, err
	}
	last, err := evaluateClosedRuleValue(
		rule.ID+" statement "+statementPath+" iterator last",
		statement.iteratorLast, match.Bindings, cells,
	)
	if err != nil {
		return nil, err
	}
	if err := incorporateEvaluatedStateReads(execution, last.reads, last.causes); err != nil {
		return nil, err
	}
	firstInteger, firstOK := first.value.(int64)
	lastInteger, lastOK := last.value.(int64)
	if !firstOK || !lastOK {
		return nil, fmt.Errorf(
			"%w: statement %s range evaluated to %T and %T, want Integer",
			ErrInvalidDeclarativeStatement, statementPath, first.value, last.value,
		)
	}
	items, err := materializeFiniteIntegerRange(firstInteger, lastInteger)
	if err != nil {
		return nil, fmt.Errorf("%w: statement %s: %v", ErrExecutionLimit, statementPath, err)
	}
	allocationCauses := canonicalEventIDs(execution.control)
	occurrence := rule.ID + "|match=" + matchDigest + "|statement=" + statementPath + "|range-iterator"
	parent := functionRuntime.executingModuleIdentity(componentID)
	module, err := gorapide.NewRapideModuleValue(gorapide.ModuleAllocationProvenance{
		Profile: CompatibilityProfile, Model: modelDigest, Parent: parent,
		Generator:  "predefined:Range(Integer).Iterator",
		Occurrence: occurrence,
		Causes:     append([]gorapide.EventID(nil), allocationCauses...),
	})
	if err != nil {
		return nil, err
	}
	iterator := &finiteRangeIterator{module: module, items: items, itemType: "Integer"}
	if err := startTemporaryIteratorModule(
		componentID, modelDigest, statementPath, statement.iteratorName,
		"predefined-range-iterator", "predefined:Range(Integer).Iterator", occurrence,
		"iterator@"+statementPath+"/range/start", module, iterator,
		functionRuntime, cells, execution,
	); err != nil {
		return nil, err
	}
	return iterator, nil
}

func initializeStatementIterator(
	componentID string,
	rule *DeclarativeRule,
	match pattern.MatchResult,
	matchDigest, modelDigest, statementPath string,
	statement Statement,
	functionRuntime *functionExecutionRuntime,
	cells map[string]*stateCell,
	execution *statementExecution,
) (*finiteRangeIterator, error) {
	switch statement.iteratorKind {
	case rangeStatementIteratorKind:
		return initializeFiniteRangeIterator(
			componentID, rule, match, matchDigest, modelDigest, statementPath,
			statement, functionRuntime, cells, execution,
		)
	case moduleStatementIteratorKind:
		evaluated, err := evaluateClosedRuleValue(
			rule.ID+" statement "+statementPath+" iterator expression",
			statement.iteratorValue, match.Bindings, cells,
		)
		if err != nil {
			return nil, err
		}
		if err := incorporateEvaluatedStateReads(execution, evaluated.reads, evaluated.causes); err != nil {
			return nil, err
		}
		module, ok := evaluated.value.(gorapide.RapideModuleValue)
		if !ok {
			return nil, fmt.Errorf("%w: statement %s iterator expression evaluated to %T",
				ErrInvalidDeclarativeStatement, statementPath, evaluated.value)
		}
		if functionRuntime == nil || functionRuntime.iterators == nil {
			return nil, fmt.Errorf("%w: statement %s iterator runtime is missing", ErrInvalidFiniteIteratorModule, statementPath)
		}
		iterator := functionRuntime.iterators[module.Identity()]
		if iterator == nil {
			return nil, fmt.Errorf("%w: statement %s module %s has no declared implementation",
				ErrInvalidFiniteIteratorModule, statementPath, module.Identity())
		}
		if iterator.itemType != statement.iteratorType {
			return nil, fmt.Errorf("%w: statement %s expects %s items from module %s, which supplies %s",
				ErrInvalidFiniteIteratorModule, statementPath, statement.iteratorType,
				module.Identity(), iterator.itemType)
		}
		return iterator, nil
	case generatorStatementIteratorKind:
		return initializeGeneratedFiniteIterator(
			componentID, rule, matchDigest, modelDigest, statementPath,
			statement, functionRuntime, cells, execution,
		)
	default:
		return nil, fmt.Errorf("%w: statement %s has iterator kind %q",
			ErrInvalidDeclarativeStatement, statementPath, statement.iteratorKind)
	}
}

func initializeGeneratedFiniteIterator(
	componentID string,
	rule *DeclarativeRule,
	matchDigest, modelDigest, statementPath string,
	statement Statement,
	functionRuntime *functionExecutionRuntime,
	cells map[string]*stateCell,
	execution *statementExecution,
) (*finiteRangeIterator, error) {
	if functionRuntime == nil || functionRuntime.iteratorGenerators == nil || functionRuntime.iterators == nil {
		return nil, fmt.Errorf("%w: statement %s iterator-generator runtime is missing",
			ErrInvalidFiniteIteratorGenerator, statementPath)
	}
	generator := functionRuntime.iteratorGenerators[statement.iteratorGenerator]
	if generator == nil {
		return nil, fmt.Errorf("%w: statement %s generator %q has no declared implementation",
			ErrInvalidFiniteIteratorGenerator, statementPath, statement.iteratorGenerator)
	}
	ruleID := ""
	if rule != nil {
		ruleID = rule.ID
	}
	allocationCauses := canonicalEventIDs(execution.control)
	parent := functionRuntime.executingModuleIdentity(componentID)
	module, err := gorapide.NewRapideModuleValue(gorapide.ModuleAllocationProvenance{
		Profile: CompatibilityProfile, Model: modelDigest, Parent: parent,
		Generator: statement.iteratorGenerator,
		Occurrence: ruleID + "|match=" + matchDigest + "|statement=" + statementPath +
			"|iterator-generator-call",
		Causes: append([]gorapide.EventID(nil), allocationCauses...),
	})
	if err != nil {
		return nil, err
	}
	if _, exists := functionRuntime.iterators[module.Identity()]; exists {
		return nil, fmt.Errorf("%w: statement %s repeated allocation identity %s",
			ErrInvalidFiniteIteratorGenerator, statementPath, module.Identity())
	}
	iterator, err := generator.instantiate(module)
	if err != nil {
		return nil, err
	}
	if iterator.itemType != statement.iteratorType {
		return nil, fmt.Errorf("%w: statement %s expects %s items from generator %s, which supplies %s",
			ErrInvalidFiniteIteratorGenerator, statementPath, statement.iteratorType,
			statement.iteratorGenerator, iterator.itemType)
	}

	occurrence := ruleID + "|match=" + matchDigest + "|statement=" + statementPath +
		"|iterator-generator-call"
	if err := startTemporaryIteratorModule(
		componentID, modelDigest, statementPath, statement.iteratorName,
		"iterator-generator-result", statement.iteratorGenerator, occurrence,
		"iterator@"+statementPath+"/generator/start", module, iterator,
		functionRuntime, cells, execution,
	); err != nil {
		return nil, err
	}
	return iterator, nil
}

func startTemporaryIteratorModule(
	componentID, modelDigest, statementPath, bindingName string,
	kind, generator, occurrence, localID string,
	module gorapide.RapideModuleValue,
	iterator *finiteRangeIterator,
	functionRuntime *functionExecutionRuntime,
	cells map[string]*stateCell,
	execution *statementExecution,
) error {
	if functionRuntime == nil || functionRuntime.lifecycle == nil || functionRuntime.iterators == nil {
		return fmt.Errorf("%w: statement %s module lifecycle runtime is missing",
			ErrInvalidDeclarativeStatement, statementPath)
	}
	if execution == nil || execution.clocks == nil || execution.owner == "" {
		return fmt.Errorf("%w: statement %s execution provenance is incomplete",
			ErrInvalidDeclarativeStatement, statementPath)
	}
	if iterator == nil || iterator.module.Identity() != module.Identity() {
		return fmt.Errorf("%w: statement %s iterator allocation is inconsistent",
			ErrInvalidDeclarativeStatement, statementPath)
	}
	if _, exists := functionRuntime.iterators[module.Identity()]; exists {
		return fmt.Errorf("%w: statement %s repeated allocation identity %s",
			ErrInvalidFiniteIteratorGenerator, statementPath, module.Identity())
	}
	allocationCauses := canonicalEventIDs(execution.control)
	startEvent, err := gorapide.NewDeterministicEvent(gorapide.EventProvenance{
		Profile: CompatibilityProfile, Model: modelDigest, Instance: module.Identity(),
		Action: ArchitectureStartAction, Occurrence: "module=" + module.Identity() + "|start",
		Causes: allocationCauses, Timings: execution.clocks.instantTimings(componentID),
	}, map[string]any{})
	if err != nil {
		return err
	}
	if err := addStateOperationSuccessors(execution.pendingOperations, string(startEvent.ID)); err != nil {
		return err
	}
	execution.pendingOperations = nil
	if err := functionRuntime.lifecycle.addExternalRoot(execution.owner); err != nil {
		return err
	}
	ownerModule := functionRuntime.executingModuleIdentity(componentID)
	if err := functionRuntime.lifecycle.addModule(moduleLifecycleRuntime{
		moduleID: module.Identity(), kind: kind, parent: ownerModule,
		generator: generator, occurrence: occurrence, startEventID: startEvent.ID,
		state: ModuleCompletedState,
	}); err != nil {
		return err
	}
	if functionRuntime.contexts == nil {
		return fmt.Errorf("%w: statement %s communication Context runtime is missing",
			ErrInvalidDeclarativeStatement, statementPath)
	}
	if err := functionRuntime.contexts.addInitialModule(
		module.Identity(), ownerModule, startEvent.ID,
	); err != nil {
		return err
	}
	nameID := "iterator-local:" + module.Identity()
	name := bindingName
	if name == "" {
		name = "@iterator"
	}
	if err := functionRuntime.lifecycle.addName(moduleNameRuntime{
		nameID: nameID, moduleID: module.Identity(), owner: execution.owner,
		name: name, kind: "lexical-iterator", acquiredAfter: []gorapide.EventID{startEvent.ID},
	}); err != nil {
		return err
	}
	iterator.lifecycleName = nameID
	functionRuntime.iterators[module.Identity()] = iterator
	emptyIteratorState := map[string]*stateCell{}
	execution.generated = append(execution.generated, generatedRuleOutput{
		localID: localID, event: startEvent, causes: allocationCauses,
		stateSnapshot: emptyIteratorState,
		observationSnapshots: map[string]map[string]*stateCell{
			module.Identity(): emptyIteratorState,
		},
	})
	execution.control = []gorapide.EventID{startEvent.ID}
	return nil
}

func releaseStatementIterator(
	componentID, modelDigest, statementPath string,
	iterator *finiteRangeIterator,
	functionRuntime *functionExecutionRuntime,
	execution *statementExecution,
) error {
	if iterator == nil || iterator.lifecycleName == "" {
		return nil
	}
	if functionRuntime == nil || functionRuntime.lifecycle == nil || execution == nil || execution.clocks == nil {
		return fmt.Errorf("%w: statement %s module lifecycle runtime is missing",
			ErrInvalidDeclarativeStatement, statementPath)
	}
	moduleID, causes, err := functionRuntime.lifecycle.releaseName(
		iterator.lifecycleName, canonicalEventIDs(execution.control),
	)
	if err != nil {
		return err
	}
	iterator.lifecycleName = ""
	if moduleID == "" {
		return nil
	}
	finishEvent, err := gorapide.NewDeterministicEvent(gorapide.EventProvenance{
		Profile: CompatibilityProfile, Model: modelDigest, Instance: moduleID,
		Action: ModuleFinishAction, Occurrence: "module=" + moduleID + "|finish",
		Causes: causes, Timings: execution.clocks.instantTimings(componentID),
	}, map[string]any{})
	if err != nil {
		return err
	}
	// Finish and the enclosing continuation are both successors of any pending
	// state operation. Finish is intentionally not installed as statement
	// control: Rapide finalization does not order the enclosing continuation.
	if err := addStateOperationSuccessors(execution.pendingOperations, string(finishEvent.ID)); err != nil {
		return err
	}
	emptyIteratorState := map[string]*stateCell{}
	execution.generated = append(execution.generated, generatedRuleOutput{
		localID: "iterator@" + statementPath + "/finish", event: finishEvent, causes: causes,
		stateSnapshot: emptyIteratorState,
		observationSnapshots: map[string]map[string]*stateCell{
			moduleID: emptyIteratorState,
		},
	})
	if err := functionRuntime.lifecycle.setFinish(moduleID, finishEvent.ID); err != nil {
		return err
	}
	if functionRuntime.contexts != nil {
		if err := functionRuntime.contexts.closeFinalizedModule(moduleID, causes); err != nil {
			return err
		}
	}
	return nil
}

func materializeFiniteIntegerRange(first, last int64) ([]any, error) {
	if first > last {
		return []any{}, nil
	}
	items := make([]any, 0)
	for current := first; ; current++ {
		if uint64(len(items)) >= MaxFiniteRangeIteratorCardinality {
			return nil, fmt.Errorf(
				"finite Range(Integer) cardinality exceeds deterministic bound %d",
				MaxFiniteRangeIteratorCardinality,
			)
		}
		items = append(items, current)
		if current == last {
			break
		}
	}
	return items, nil
}

func (iterator *finiteRangeIterator) more() bool {
	return iterator != nil && iterator.next < uint64(len(iterator.items))
}

func (iterator *finiteRangeIterator) item() (any, error) {
	if !iterator.more() {
		return nil, fmt.Errorf("%w: Item called after finite iterator exhaustion", ErrInvalidDeclarativeStatement)
	}
	value := iterator.items[iterator.next]
	iterator.next++
	return value, nil
}

func executeFiniteIteratorProtocolCall(
	componentID, modelDigest, statementPath string,
	iteration uint64,
	functionName, returnType string,
	returned any,
	iterator *finiteRangeIterator,
	cells map[string]*stateCell,
	execution *statementExecution,
) error {
	if iterator == nil || iterator.module.Identity() == "" {
		return fmt.Errorf("%w: statement %s has no allocated iterator", ErrInvalidDeclarativeStatement, statementPath)
	}
	iteratorID := iterator.module.Identity()
	occurrence := statementPath + "|iterator=" + iteratorID + "|probe=" + strconv.FormatUint(iteration, 10) + "|function=" + functionName
	callCauses := canonicalEventIDs(execution.control)
	callEvent, err := gorapide.NewDeterministicEvent(gorapide.EventProvenance{
		Profile: CompatibilityProfile, Model: modelDigest, Instance: componentID,
		Action: functionName + "'Call", Occurrence: occurrence + "|call",
		Causes: callCauses, Timings: execution.clocks.instantTimings(componentID),
	}, map[string]any{})
	if err != nil {
		return err
	}
	if err := addStateOperationSuccessors(execution.pendingOperations, string(callEvent.ID)); err != nil {
		return err
	}
	execution.pendingOperations = nil
	callEvent.Observations = append(callEvent.Observations, gorapide.EventObservation{
		Name: functionName + "'Call", Source: iteratorID, Params: map[string]any{},
	})
	emptyIteratorState := map[string]*stateCell{}
	callSnapshots := map[string]map[string]*stateCell{
		componentID: cloneStateCells(cells), iteratorID: emptyIteratorState,
	}
	prefix := "iterator@" + statementPath + "/probe/" + strconv.FormatUint(iteration, 10) + "/" + functionName
	execution.generated = append(execution.generated, generatedRuleOutput{
		localID: prefix + "/call", event: callEvent, causes: callCauses,
		stateSnapshot: cloneStateCells(cells), observationSnapshots: callSnapshots,
	})

	returnParameters, err := canonicalFunctionReturnParameters(returnType, map[string]any{}, returned)
	if err != nil {
		return err
	}
	returnCauses := []gorapide.EventID{callEvent.ID}
	returnEvent, err := gorapide.NewDeterministicEvent(gorapide.EventProvenance{
		Profile: CompatibilityProfile, Model: modelDigest, Instance: iteratorID,
		Action: functionName + "'Return", Occurrence: occurrence + "|return",
		Causes: returnCauses, Timings: execution.clocks.instantTimings(componentID),
	}, returnParameters)
	if err != nil {
		return err
	}
	returnEvent.Observations = append(returnEvent.Observations, gorapide.EventObservation{
		Name: functionName + "'Return", Source: componentID, Params: returnParameters,
	})
	returnSnapshots := map[string]map[string]*stateCell{
		iteratorID: emptyIteratorState, componentID: cloneStateCells(cells),
	}
	execution.generated = append(execution.generated, generatedRuleOutput{
		localID: prefix + "/return", event: returnEvent, causes: returnCauses,
		stateSnapshot: emptyIteratorState, observationSnapshots: returnSnapshots,
	})
	execution.control = []gorapide.EventID{returnEvent.ID}
	return nil
}

func bindingsWithIteratorValue(bindings pattern.Bindings, name string, value any) pattern.Bindings {
	if name == "" {
		return append(pattern.Bindings(nil), bindings...)
	}
	result := make(pattern.Bindings, 0, len(bindings)+1)
	for _, binding := range bindings {
		if binding.Placeholder != name {
			result = append(result, binding)
		}
	}
	result = append(result, pattern.Binding{Placeholder: name, Value: value})
	sort.Slice(result, func(left, right int) bool {
		return result[left].Placeholder < result[right].Placeholder
	})
	return result
}

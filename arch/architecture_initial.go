package arch

import (
	"errors"
	"fmt"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/pattern"
)

// ErrInvalidArchitectureInitial identifies a malformed closed architecture
// initial part.
var ErrInvalidArchitectureInitial = errors.New("invalid declarative Rapide architecture initial part")

// SetDeterministicArchitectureInitialStatements installs the ordered initial
// part of the root architecture or one declared static child. The owner is
// ArchitectureInterfaceID for the root and the architecture-instance ID for a
// child. Statements are snapshotted and become canonical model data.
func (a *Architecture) SetDeterministicArchitectureInitialStatements(
	owner string,
	statements ...Statement,
) error {
	if a == nil {
		return fmt.Errorf("%w: architecture is nil", ErrInvalidArchitectureInitial)
	}
	if len(statements) == 0 {
		return fmt.Errorf("%w: architecture %q initial part is empty", ErrInvalidArchitectureInitial, owner)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.running {
		return fmt.Errorf("arch: architecture %q is running", a.Name)
	}
	if owner != ArchitectureInterfaceID {
		if _, exists := a.architectureInstances[owner]; !exists {
			return fmt.Errorf("%w: deterministic architecture instance %q is not declared", ErrInvalidArchitectureInitial, owner)
		}
	}
	if _, exists := a.architectureInitials[owner]; exists {
		return fmt.Errorf("%w: architecture %q already has an initial part", ErrInvalidArchitectureInitial, owner)
	}
	a.architectureInitials[owner] = copyStatements(statements)
	return nil
}

// SetDeterministicArchitectureInitialExceptionDeclarations installs the exact
// lexical exception catalog used only by one root or static-child architecture
// initial part. Declarations are snapshotted independently of caller storage.
func (a *Architecture) SetDeterministicArchitectureInitialExceptionDeclarations(
	owner string,
	declarations ...ExceptionDeclaration,
) error {
	if a == nil {
		return fmt.Errorf("%w: architecture is nil", ErrInvalidArchitectureInitial)
	}
	if len(declarations) == 0 {
		return fmt.Errorf("%w: architecture %q initial exception catalog is empty", ErrInvalidArchitectureInitial, owner)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.running {
		return fmt.Errorf("arch: architecture %q is running", a.Name)
	}
	if owner != ArchitectureInterfaceID {
		if _, exists := a.architectureInstances[owner]; !exists {
			return fmt.Errorf("%w: deterministic architecture instance %q is not declared", ErrInvalidArchitectureInitial, owner)
		}
	}
	if _, exists := a.architectureInitialExceptions[owner]; exists {
		return fmt.Errorf("%w: architecture %q already has an initial exception catalog", ErrInvalidArchitectureInitial, owner)
	}
	result := make([]ExceptionDeclaration, len(declarations))
	for index, declaration := range declarations {
		result[index] = ExceptionDeclaration{
			Declaration: declaration.Declaration,
			Name:        declaration.Name,
			Params:      append([]ParamDecl(nil), declaration.Params...),
		}
	}
	a.architectureInitialExceptions[owner] = result
	return nil
}

func executeArchitectureInitialParts(
	model *deterministicModel,
	functionRuntime *functionExecutionRuntime,
	moduleState moduleStateRuntime,
	statementSteps *statementBudget,
	clocks *deterministicClockKernel,
	frontiers *causalFrontierRegistry,
	poset *gorapide.Poset,
	depths map[gorapide.EventID]uint64,
	queue *executionQueue,
	seenItems map[string]bool,
	stateSnapshots stateSnapshotRegistry,
	starts map[string]*gorapide.Event,
	maxFirings uint64,
	firings *[]FiringRecord,
) (*architectureInitializationAbandonment, error) {
	emptyMatch := pattern.MatchResult{Events: gorapide.EventSet{}, Bindings: pattern.Bindings{}}
	canonicalMatch, err := pattern.CanonicalizeMatch(emptyMatch)
	if err != nil {
		return nil, err
	}
	// Nested generator initial parts are evaluated bottom-up, followed by the
	// enclosing root. Traversal order is audit order only and adds no cross-owner
	// causal edge; each body begins solely from its own Start occurrence.
	owners := architectureInstancePostOrder(model.architectureInstances, model.architectureInstanceIDs)
	for _, ownerID := range owners {
		statements := model.architectureInitials[ownerID]
		if statements == nil {
			continue
		}
		start := starts[ownerID]
		if start == nil {
			return nil, fmt.Errorf("%w: architecture %q has no Start occurrence", ErrInvalidArchitectureInitial, ownerID)
		}
		if uint64(len(*firings)) >= maxFirings {
			return nil, fmt.Errorf("%w: max_firings=%d", ErrExecutionLimit, maxFirings)
		}
		boundaryID := architectureBoundaryID(ownerID)
		component := architectureBoundaryComponent(model, ownerID)
		causalOwner := "architecture-initial:" + ownerID
		rule := &DeclarativeRule{
			ID: "architecture-initial/" + ownerID, Process: RulePipeProcess,
			Body: &RuleBody{Statements: statements},
		}
		body, err := buildDeclarativeRuleBody(
			boundaryID, component, rule, emptyMatch, canonicalMatch,
			model.digest, nil, moduleState[boundaryID], functionRuntime,
			[]gorapide.EventID{start.ID}, nil, statementSteps, clocks,
			causalOwner, nil,
		)
		if err != nil {
			return nil, fmt.Errorf("%w: architecture %q: %w", ErrInvalidArchitectureInitial, ownerID, err)
		}
		if body.initializationFailure != nil {
			if err := validateArchitectureInitialFailedCreationScope(model, ownerID, functionRuntime); err != nil {
				return nil, fmt.Errorf("%w: architecture %q: %v", ErrInvalidArchitectureInitial, ownerID, err)
			}
		}
		if body.exitProcess && body.initializationFailure == nil {
			return nil, fmt.Errorf("%w: architecture %q initial control escaped its body", ErrInvalidArchitectureInitial, ownerID)
		}
		if body.raised != nil {
			return nil, fmt.Errorf("%w: architecture %q: %w: %s",
				ErrInvalidArchitectureInitial, ownerID, ErrUnhandledRapideException, body.raised.name)
		}
		generated := make([]GeneratedEventRecord, 0, len(body.generated))
		for _, output := range body.generated {
			if err := poset.AddEventWithCause(output.event, output.causes...); err != nil {
				return nil, fmt.Errorf("%w: architecture %q output %s: %w", ErrInvalidArchitectureInitial, ownerID, output.localID, err)
			}
			depth := eventDepth(poset, output.event, depths)
			depths[output.event.ID] = depth
			if err := enqueueGeneratedObservationViews(
				output, depth, moduleState, stateSnapshots, queue, seenItems,
			); err != nil {
				return nil, err
			}
			generated = append(generated, GeneratedEventRecord{
				OutputID: output.localID, EventID: string(output.event.ID), Exception: output.exception,
			})
		}
		canceledSchedules := append([]string(nil), body.canceledSchedules...)
		if body.initializationFailure != nil {
			canceledSchedules = append(canceledSchedules, scheduledActionIDs(body.scheduled)...)
		} else {
			clocks.addScheduled(body.scheduled)
		}
		canceledSchedules = canonicalStrings(canceledSchedules)
		frontiers.set(causalOwner, canonicalEventIDs(body.frontier))
		firing := FiringRecord{
			Sequence: uint64(len(*firings) + 1), Transition: "architecture-initial", Target: boundaryID,
			Generated: generated, Scheduled: scheduledPlans(body.scheduled),
			CanceledSchedules: canceledSchedules,
			StateReads:        body.stateReads, StateWrites: body.stateWrites,
		}
		if body.initializationFailure != nil {
			firing.Completion = "exception"
			firing.ExceptionEventID = string(body.initializationFailure.raised.event.ID)
		}
		*firings = append(*firings, firing)
		if body.initializationFailure != nil {
			terminationContext := processTerminationContext{
				modelDigest: model.digest, functionRuntime: functionRuntime,
				frontiers: frontiers, clocks: clocks, poset: poset, depths: depths,
				queue: queue, seenItems: seenItems, moduleState: moduleState,
				stateSnapshots: stateSnapshots, statementSteps: statementSteps, firings: firings,
			}
			outcome, err := finalizeFailedModuleInitializationChain(
				body.initializationFailure, terminationContext,
			)
			if err != nil {
				return nil, fmt.Errorf(
					"%w: architecture %q allocator initialization: %v",
					ErrInvalidArchitectureInitial, ownerID, err,
				)
			}
			abandonment, err := architectureInitialAbandonmentAfterFailedCreation(
				ownerID, outcome, terminationContext,
			)
			if err != nil {
				return nil, fmt.Errorf(
					"%w: architecture %q failed-creation completion: %v",
					ErrInvalidArchitectureInitial, ownerID, err,
				)
			}
			return abandonment, nil
		}
	}
	return nil, nil
}

func validateArchitectureInitialFailedCreationScope(
	model *deterministicModel,
	ownerID string,
	runtime *functionExecutionRuntime,
) error {
	if model == nil || runtime == nil || runtime.lifecycle == nil || runtime.contexts == nil {
		return fmt.Errorf("%w: architecture-initial failed-creation runtime is incomplete", ErrInvalidDeclarativeStatement)
	}
	if _, err := architectureInitializationAbandonmentLineage(model, ownerID); err != nil {
		return err
	}
	ownerModuleID := runtime.modules[ownerID].Identity()
	if ownerModuleID == "" {
		return fmt.Errorf("%w: architecture %q has no module identity", ErrInvalidDeclarativeStatement, ownerID)
	}
	return nil
}

func architectureInitialAbandonmentAfterFailedCreation(
	ownerID string,
	outcome failedModuleInitializationOutcome,
	context processTerminationContext,
) (*architectureInitializationAbandonment, error) {
	runtime := context.functionRuntime
	if runtime == nil || runtime.lifecycle == nil || runtime.contexts == nil ||
		runtime.propagations == nil || ownerID == "" || outcome.activeRaised == nil ||
		outcome.activeRaised.event == nil || outcome.structuralSourceModuleID == "" {
		return nil, fmt.Errorf("%w: architecture-initial abandonment outcome is incomplete", ErrUnhandledRapideException)
	}
	ownerModuleID := runtime.modules[ownerID].Identity()
	ownerLifecycle := runtime.lifecycle.modules[ownerModuleID]
	if ownerLifecycle == nil {
		return nil, fmt.Errorf("%w: architecture %q has no lifecycle", ErrUnhandledRapideException, ownerID)
	}
	if ownerLifecycle.state == ModuleTerminatedState {
		activeRaised := outcome.activeRaised
		if ownerLifecycle.terminationEventID != activeRaised.event.ID {
			replacement, err := propagatedExceptionOccurrenceDeliveredToTarget(
				ownerModuleID, ownerLifecycle.terminationEventID, context,
			)
			if err != nil {
				return nil, err
			}
			activeRaised = replacement
		}
		return &architectureInitializationAbandonment{
			ownerID: ownerID, activeRaised: activeRaised, architectureInitial: true,
		}, nil
	}
	if ownerLifecycle.state != ModuleCompletedState || ownerLifecycle.terminationEventID != "" {
		return nil, fmt.Errorf(
			"%w: architecture %q has invalid handled-abandonment lifecycle state %q",
			ErrUnhandledRapideException, ownerID, ownerLifecycle.state,
		)
	}
	recovery := canonicalEventIDs(outcome.handledFrontier)
	if len(recovery) == 0 {
		sourceLifecycle := runtime.lifecycle.modules[outcome.structuralSourceModuleID]
		if sourceLifecycle == nil || sourceLifecycle.parent == "" {
			return nil, fmt.Errorf(
				"%w: architecture-initial structural source %q has no parent",
				ErrUnhandledRapideException, outcome.structuralSourceModuleID,
			)
		}
		parentModuleID := sourceLifecycle.parent
		if !runtime.propagations.handledParentTarget(
			outcome.activeRaised.event.ID, outcome.structuralSourceModuleID, parentModuleID,
		) {
			return nil, fmt.Errorf(
				"%w: architecture %q neither terminated nor reached structural handler recovery",
				ErrUnhandledRapideException, ownerID,
			)
		}
		parentComponentID := runtime.contexts.componentByModule[parentModuleID]
		var err error
		recovery, err = moduleHandlerRecoveryFrontier(
			parentComponentID, outcome.activeRaised.event.ID, context,
		)
		if err != nil {
			return nil, err
		}
	}
	return &architectureInitializationAbandonment{
		ownerID: ownerID, handledFrontier: recovery, architectureInitial: true,
	}, nil
}

func propagatedExceptionOccurrenceDeliveredToTarget(
	targetModuleID string,
	eventID gorapide.EventID,
	context processTerminationContext,
) (*raisedExceptionOccurrence, error) {
	runtime := context.functionRuntime
	if runtime == nil || runtime.propagations == nil || targetModuleID == "" || eventID == "" {
		return nil, fmt.Errorf("%w: target propagation lookup is incomplete", ErrUnhandledRapideException)
	}
	sourceModuleID := ""
	for _, record := range runtime.propagations.recordsByKey {
		if record.ExceptionEventID != string(eventID) {
			continue
		}
		for _, target := range record.Targets {
			if target.ModuleID != targetModuleID || target.Disposition != exceptionDeliveredDisposition {
				continue
			}
			for _, relation := range target.Relations {
				if relation != exceptionParentRelation {
					continue
				}
				if sourceModuleID != "" && sourceModuleID != record.SourceModuleID {
					return nil, fmt.Errorf(
						"%w: target module %q has ambiguous propagation source for %q",
						ErrUnhandledRapideException, targetModuleID, eventID,
					)
				}
				sourceModuleID = record.SourceModuleID
			}
		}
	}
	if sourceModuleID == "" {
		return nil, fmt.Errorf(
			"%w: target module %q has no delivered parent propagation for %q",
			ErrUnhandledRapideException, targetModuleID, eventID,
		)
	}
	return propagatedExceptionOccurrenceBySource(sourceModuleID, eventID, context)
}

func architectureBoundaryComponent(model *deterministicModel, owner string) *Component {
	if model == nil {
		return nil
	}
	var component *Component
	if owner == ArchitectureInterfaceID {
		component = NewComponent(ArchitectureInterfaceID, model.returnInterface, nil)
	} else {
		declaration, exists := model.architectureInstances[owner]
		if !exists {
			return nil
		}
		component = NewComponent(architectureBoundaryID(owner), declaration.ReturnInterface, nil)
	}
	for _, declaration := range model.architectureInitialExceptions[owner] {
		if err := component.AddExceptionDeclaration(declaration); err != nil {
			return nil
		}
	}
	return component
}

func validateArchitectureInitialStatementSubset(
	component *Component,
	statements []Statement,
	placeholderTypes map[string]string,
	owner string,
	callables map[string]map[string]*FunctionImplementation,
	componentArchitectures map[string]string,
	instances map[string]ArchitectureInstanceDeclaration,
	visitedFunctions map[string]bool,
) error {
	for _, statement := range statements {
		switch statement.kind {
		case AssignmentStatement:
			return fmt.Errorf("%w: architecture state declarations are outside the current source subset", ErrInvalidArchitectureInitial)
		case FunctionCallStatement:
			if err := validateArchitectureInitialFunctionCall(
				owner, component.ID, statement.functionCall, callables,
				componentArchitectures, instances, visitedFunctions,
			); err != nil {
				return err
			}
		case TimedStatementKind:
			return fmt.Errorf("%w: architecture initial pause/delay requires unsupported architecture clocks", ErrInvalidArchitectureInitial)
		case EventCallStatement:
			if statement.timing != nil {
				return fmt.Errorf("%w: architecture initial action timing requires unsupported architecture clocks", ErrInvalidArchitectureInitial)
			}
			if !ruleOutputShapeMatchesKinds(component, statement.output, nil, placeholderTypes, OutAction) {
				return fmt.Errorf("%w: architecture initial action %q is not a returned-interface out action", ErrInvalidArchitectureInitial, statement.output.Action)
			}
		case DoBlockStatementKind:
			if err := validateArchitectureInitialStatementSubset(
				component, statement.handledBody, placeholderTypes, owner, callables,
				componentArchitectures, instances, visitedFunctions,
			); err != nil {
				return err
			}
		case HandlerBlockStatementKind:
			for _, choice := range statement.handler.Choices {
				if choice.Action == "" {
					continue
				}
				action, exists := handlerActionDeclaration(component, choice.Action)
				if !exists || action.Kind != OutAction {
					return fmt.Errorf("%w: architecture initial external in-action interrupt choice %q requires asynchronous startup semantics",
						ErrInvalidArchitectureInitial, choice.Action)
				}
			}
			if err := validateArchitectureInitialStatementSubset(
				component, statement.handledBody, placeholderTypes, owner, callables,
				componentArchitectures, instances, visitedFunctions,
			); err != nil {
				return err
			}
			for _, choice := range statement.handler.Choices {
				choicePlaceholderTypes := make(map[string]string, len(placeholderTypes)+len(choice.Bindings))
				for name, typeName := range placeholderTypes {
					choicePlaceholderTypes[name] = typeName
				}
				for _, binding := range choice.Bindings {
					choicePlaceholderTypes[binding.Placeholder] = binding.Type
				}
				if err := validateArchitectureInitialStatementSubset(
					component, choice.Statements, choicePlaceholderTypes, owner, callables,
					componentArchitectures, instances, visitedFunctions,
				); err != nil {
					return err
				}
			}
			if err := validateArchitectureInitialStatementSubset(
				component, statement.handler.Else, placeholderTypes, owner, callables,
				componentArchitectures, instances, visitedFunctions,
			); err != nil {
				return err
			}
		case IfStatementKind:
			if err := validateArchitectureInitialStatementSubset(
				component, statement.thenBranch, placeholderTypes, owner, callables,
				componentArchitectures, instances, visitedFunctions,
			); err != nil {
				return err
			}
			if err := validateArchitectureInitialStatementSubset(
				component, statement.elseBranch, placeholderTypes, owner, callables,
				componentArchitectures, instances, visitedFunctions,
			); err != nil {
				return err
			}
		case LoopStatementKind:
			if err := validateArchitectureInitialStatementSubset(
				component, statement.loopBody, placeholderTypes, owner, callables,
				componentArchitectures, instances, visitedFunctions,
			); err != nil {
				return err
			}
		case ForStatementKind:
			if statement.iteratorKind != rangeStatementIteratorKind || statement.iteratorType != "Integer" {
				return fmt.Errorf("%w: architecture initial for requires a finite Range(Integer) iterator",
					ErrInvalidArchitectureInitial)
			}
			bodyPlaceholderTypes := make(map[string]string, len(placeholderTypes)+1)
			for name, typeName := range placeholderTypes {
				bodyPlaceholderTypes[name] = typeName
			}
			if statement.iteratorName != "" {
				bodyPlaceholderTypes[statement.iteratorName] = statement.iteratorType
			}
			if err := validateArchitectureInitialStatementSubset(
				component, statement.loopBody, bodyPlaceholderTypes, owner, callables,
				componentArchitectures, instances, visitedFunctions,
			); err != nil {
				return err
			}
		case GeneralForStatementKind:
			return fmt.Errorf("%w: architecture initial general for is outside the current deterministic startup subset",
				ErrInvalidArchitectureInitial)
		case CaseStatementKind:
			for _, alternative := range statement.caseAlts {
				if err := validateArchitectureInitialStatementSubset(
					component, alternative.body, placeholderTypes, owner, callables,
					componentArchitectures, instances, visitedFunctions,
				); err != nil {
					return err
				}
			}
			if err := validateArchitectureInitialStatementSubset(
				component, statement.caseDefault, placeholderTypes, owner, callables,
				componentArchitectures, instances, visitedFunctions,
			); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateArchitectureInitialFunctionCall(
	owner, callerComponentID string,
	call FunctionCall,
	callables map[string]map[string]*FunctionImplementation,
	componentArchitectures map[string]string,
	instances map[string]ArchitectureInstanceDeclaration,
	visitedFunctions map[string]bool,
) error {
	implementation := callables[callerComponentID][call.functionKey]
	if implementation == nil {
		return fmt.Errorf(
			"%w: architecture %q initial call %q resolved to missing function signature %q",
			ErrInvalidArchitectureInitial, owner, call.ID, call.functionKey,
		)
	}
	targetComponentID := implementation.targetComponent
	if targetComponentID == "" {
		targetComponentID = callerComponentID
	}
	if !architectureInitialContainsComponent(
		owner, targetComponentID, componentArchitectures, instances,
	) {
		return fmt.Errorf(
			"%w: architecture %q initial function %q reaches component %q outside its elaborated subtree",
			ErrInvalidArchitectureInitial, owner, implementation.Name, targetComponentID,
		)
	}
	visitKey := targetComponentID + "\x00" + implementation.key
	if visitedFunctions[visitKey] {
		return nil
	}
	visitedFunctions[visitKey] = true
	return validateArchitectureInitialReachableFunctionStatements(
		owner, targetComponentID, implementation.Statements, callables,
		componentArchitectures, instances, visitedFunctions,
	)
}

func validateArchitectureInitialReachableFunctionStatements(
	owner, callerComponentID string,
	statements []Statement,
	callables map[string]map[string]*FunctionImplementation,
	componentArchitectures map[string]string,
	instances map[string]ArchitectureInstanceDeclaration,
	visitedFunctions map[string]bool,
) error {
	for _, statement := range statements {
		if statement.kind == FunctionCallStatement {
			if err := validateArchitectureInitialFunctionCall(
				owner, callerComponentID, statement.functionCall, callables,
				componentArchitectures, instances, visitedFunctions,
			); err != nil {
				return err
			}
		}
		if statement.kind == GeneralForStatementKind {
			for _, expression := range []ExecutableObjectExpression{
				statement.forInitial, statement.forTest, statement.forNext,
			} {
				if expression.kind != ObjectFunctionExpression {
					continue
				}
				if err := validateArchitectureInitialFunctionCall(
					owner, callerComponentID, expression.call, callables,
					componentArchitectures, instances, visitedFunctions,
				); err != nil {
					return err
				}
			}
		}
		groups := [][]Statement{
			statement.thenBranch, statement.elseBranch, statement.loopBody,
			statement.caseDefault, statement.handledBody, statement.handler.Else,
		}
		for _, alternative := range statement.caseAlts {
			groups = append(groups, alternative.body)
		}
		for _, choice := range statement.handler.Choices {
			groups = append(groups, choice.Statements)
		}
		for _, group := range groups {
			if err := validateArchitectureInitialReachableFunctionStatements(
				owner, callerComponentID, group, callables,
				componentArchitectures, instances, visitedFunctions,
			); err != nil {
				return err
			}
		}
	}
	return nil
}

func architectureInitialContainsComponent(
	owner, componentID string,
	componentArchitectures map[string]string,
	instances map[string]ArchitectureInstanceDeclaration,
) bool {
	componentOwner, exists := componentArchitectures[componentID]
	if !exists {
		return false
	}
	for {
		if componentOwner == owner {
			return true
		}
		if componentOwner == ArchitectureInterfaceID {
			return false
		}
		declaration, exists := instances[componentOwner]
		if !exists {
			return false
		}
		componentOwner = declaration.Parent
	}
}

package arch

import (
	"fmt"
	"strings"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/pattern"
)

// resumableFunctionCall is the semantic state created exactly once when a
// process enters a synchronous Rapide function. It deliberately contains no Go
// callback or closure: the process continuation owns the explicit statement
// frames, while this value retains the call/return protocol, local module
// names, routing views, and target state needed after any number of yields.
type resumableFunctionCall struct {
	callerComponentID string
	callerCells       map[string]*stateCell
	call              FunctionCall
	implementation    *FunctionImplementation
	targetComponentID string
	targetName        string
	targetComponent   *Component
	targetCells       map[string]*stateCell
	aliases           []functionRouteAlias
	aliasActuals      []map[string]any
	actuals           map[string]any
	providerActuals   map[string]any
	remote            bool
	occurrence        string
	prefix            string
	callEvent         *gorapide.Event
	functionRule      *DeclarativeRule
	functionMatch     pattern.MatchResult
	localModules      []functionModuleLocalRuntime
}

func beginResumableFunctionCall(
	componentID string,
	rule *DeclarativeRule,
	match pattern.MatchResult,
	matchDigest, modelDigest, statementPath string,
	call FunctionCall,
	functionRuntime *functionExecutionRuntime,
	cells map[string]*stateCell,
	parent *statementExecution,
	budget *statementBudget,
) (*resumableFunctionCall, statementExecution, error) {
	if functionRuntime == nil {
		return nil, statementExecution{}, fmt.Errorf(
			"%w: function execution runtime is missing", ErrInvalidFunctionImplementation,
		)
	}
	implementation := functionRuntime.callables[componentID][call.functionKey]
	if implementation == nil {
		return nil, statementExecution{}, fmt.Errorf(
			"%w: call %q resolved to missing function signature %q",
			ErrInvalidFunctionImplementation, call.ID, call.functionKey,
		)
	}
	if functionRuntime.architectureScopeClosed && functionImplementationUsesArchitectureRoute(implementation) {
		return nil, statementExecution{}, fmt.Errorf(
			"%w: function call %q requires an architecture route after its owning architecture scope closed",
			ErrInvalidFunctionImplementation, call.ID,
		)
	}
	targetComponentID := implementation.targetComponent
	if targetComponentID == "" {
		targetComponentID = componentID
	}
	targetName := implementation.targetName
	if targetName == "" {
		targetName = implementation.Name
	}
	targetComponent := functionRuntime.components[targetComponentID]
	targetCells := functionRuntime.state[targetComponentID]
	if targetComponent == nil || targetCells == nil {
		return nil, statementExecution{}, fmt.Errorf(
			"%w: function %q target component %q is unavailable",
			ErrInvalidFunctionImplementation, implementation.Name, targetComponentID,
		)
	}

	aliases := append([]functionRouteAlias(nil), implementation.routeAliases...)
	if len(aliases) == 0 && implementation.connectionID != "" {
		aliases = append(aliases, functionRouteAlias{
			ComponentID: targetComponentID,
			Name:        targetName,
			Params:      append([]ParamDecl(nil), implementation.targetParams...),
		})
	}
	remote := len(aliases) != 0
	actuals := make(map[string]any, len(call.Arguments))
	argumentCauses := make([]gorapide.EventID, 0)
	for _, argument := range call.Arguments {
		evaluated, err := evaluateClosedRuleValue(
			rule.ID+" statement "+statementPath+" function argument "+argument.Name,
			argument.Value, match.Bindings, cells,
		)
		if err != nil {
			return nil, statementExecution{}, err
		}
		actuals[argument.Name] = evaluated.value
		argumentCauses = append(argumentCauses, evaluated.causes...)
		if err := incorporateEvaluatedStateReads(parent, evaluated.reads, evaluated.causes); err != nil {
			return nil, statementExecution{}, err
		}
	}
	var err error
	actuals, err = gorapide.CanonicalizeParams(actuals)
	if err != nil {
		return nil, statementExecution{}, err
	}
	for _, formal := range implementation.Params {
		if !valueMatchesPredefinedType(actuals[formal.Name], formal.Type) {
			return nil, statementExecution{}, fmt.Errorf(
				"%w: function %q argument %q does not match %s",
				ErrInvalidFunctionImplementation, implementation.Name, formal.Name, formal.Type,
			)
		}
	}
	targetParams := implementation.targetParams
	if len(targetParams) == 0 && len(implementation.Params) != 0 {
		targetParams = implementation.Params
	}
	aliasActuals := make([]map[string]any, len(aliases))
	for index, alias := range aliases {
		aliasActuals[index], err = functionRouteActuals(implementation.Params, actuals, alias.Params)
		if err != nil {
			return nil, statementExecution{}, err
		}
	}
	providerActuals := actuals
	if len(aliasActuals) != 0 {
		providerActuals = aliasActuals[len(aliasActuals)-1]
	}

	controlCauses := append([]gorapide.EventID(nil), parent.control...)
	if strings.HasPrefix(parent.owner, "architecture-initial:") {
		startup, err := architectureInitialFunctionTargetFrontier(functionRuntime, targetComponentID)
		if err != nil {
			return nil, statementExecution{}, err
		}
		controlCauses = maximalKnownCausalFrontier(
			functionRuntime.poset, append(controlCauses, startup...),
		)
	}
	callCauses := canonicalEventIDs(append(controlCauses, argumentCauses...))
	occurrence := rule.ID + "|match=" + matchDigest + "|statement=" + statementPath +
		"|function=" + call.ID + "|signature=" + call.functionKey
	callEvent, err := gorapide.NewDeterministicEvent(gorapide.EventProvenance{
		Profile: CompatibilityProfile, Model: modelDigest, Instance: componentID,
		Action: implementation.Name + "'Call", Occurrence: occurrence + "|call",
		Causes: callCauses, Timings: functionRuntime.clocks.instantTimings(componentID, targetComponentID),
	}, actuals)
	if err != nil {
		return nil, statementExecution{}, err
	}
	if err := addStateOperationSuccessors(parent.pendingOperations, string(callEvent.ID)); err != nil {
		return nil, statementExecution{}, err
	}
	parent.pendingOperations = nil
	if remote {
		for index, alias := range aliases {
			callEvent.Observations = append(callEvent.Observations, gorapide.EventObservation{
				Name: alias.Name + "'Call", Source: alias.ComponentID, Params: aliasActuals[index],
			})
		}
	}
	prefix := "function@" + statementPath + "/" + call.ID
	callSnapshots := map[string]map[string]*stateCell{
		componentID: cloneStateCells(cells), targetComponentID: cloneStateCells(targetCells),
	}
	parent.generated = append(parent.generated, generatedRuleOutput{
		localID: prefix + "/call", event: callEvent, causes: callCauses,
		stateSnapshot: cloneStateCells(cells), observationSnapshots: callSnapshots,
	})

	functionMatch := pattern.MatchResult{Bindings: functionBindings(targetParams, providerActuals)}
	functionMatch, err = bindModuleSelf(functionMatch, targetComponentID, functionRuntime)
	if err != nil {
		return nil, statementExecution{}, err
	}
	child := statementExecution{
		control: []gorapide.EventID{callEvent.ID}, clocks: parent.clocks,
		owner: parent.owner, budget: budget,
	}
	for _, active := range parent.interruptHandlers {
		if targetComponentID == componentID || active.processOwned {
			child.interruptHandlers = append(child.interruptHandlers, active)
		}
	}
	retainSynchronousGenerationTimeConnectionState(parent, &child)
	inheritGenerationTimeConnectionState(parent, &child)
	functionMatch, localModules, err := allocateFunctionModuleLocals(
		targetComponentID, modelDigest, occurrence, implementation,
		functionMatch, functionRuntime, &child,
	)
	if err != nil {
		return nil, statementExecution{}, err
	}
	state := &resumableFunctionCall{
		callerComponentID: componentID,
		callerCells:       cells,
		call:              call,
		implementation:    implementation,
		targetComponentID: targetComponentID,
		targetName:        targetName,
		targetComponent:   targetComponent,
		targetCells:       targetCells,
		aliases:           aliases,
		aliasActuals:      aliasActuals,
		actuals:           actuals,
		providerActuals:   providerActuals,
		remote:            remote,
		occurrence:        occurrence,
		prefix:            prefix,
		callEvent:         callEvent,
		functionRule:      &DeclarativeRule{ID: "function/" + call.functionKey},
		functionMatch:     functionMatch,
		localModules:      localModules,
	}
	return state, child, nil
}

// drainResumableFunctionExecution transfers only the effects accumulated since
// the previous yield. The child retains its control and pending state operation
// frontier so resumption continues exactly where it stopped.
func drainResumableFunctionExecution(
	state *resumableFunctionCall,
	child, parent *statementExecution,
) {
	if state == nil || child == nil || parent == nil {
		return
	}
	for _, output := range child.generated {
		output.localID = state.prefix + "/body/" + output.localID
		parent.generated = append(parent.generated, output)
	}
	child.generated = nil
	parent.scheduled = append(parent.scheduled, child.scheduled...)
	child.scheduled = nil
	parent.canceledSchedules = append(parent.canceledSchedules, child.canceledSchedules...)
	child.canceledSchedules = nil
	parent.reads = append(parent.reads, qualifyStateReads(state.targetComponentID, child.reads)...)
	child.reads = nil
	parent.writes = append(parent.writes, qualifyStateWrites(state.targetComponentID, child.writes)...)
	child.writes = nil
	parent.control = canonicalEventIDs(child.control)
}

func finishResumableFunctionCall(
	state *resumableFunctionCall,
	child, parent *statementExecution,
	control statementControl,
	modelDigest string,
	functionRuntime *functionExecutionRuntime,
) (any, statementControl, error) {
	if state == nil || child == nil || parent == nil {
		return nil, statementContinue, fmt.Errorf(
			"%w: resumable function state is incomplete", ErrInvalidFunctionImplementation,
		)
	}
	if child.initializationFailure != nil {
		if err := releaseFunctionModuleLocals(
			modelDigest, state.localModules, functionRuntime, child,
		); err != nil {
			return nil, statementContinue, err
		}
		drainResumableFunctionExecution(state, child, parent)
		parent.pendingOperations = child.pendingOperations
		parent.initializationFailure = child.initializationFailure
		return nil, statementExitProcess, nil
	}
	if control == statementRaiseException || child.raised != nil {
		if err := releaseFunctionModuleLocals(
			modelDigest, state.localModules, functionRuntime, child,
		); err != nil {
			return nil, statementContinue, err
		}
		drainResumableFunctionExecution(state, child, parent)
		parent.pendingOperations = child.pendingOperations
		parent.raised = child.raised
		return nil, statementRaiseException, nil
	}
	if control == statementHandleInterrupt || child.pendingInterrupt != nil {
		if err := releaseFunctionModuleLocals(
			modelDigest, state.localModules, functionRuntime, child,
		); err != nil {
			return nil, statementContinue, err
		}
		drainResumableFunctionExecution(state, child, parent)
		parent.pendingOperations = child.pendingOperations
		parent.pendingInterrupt = child.pendingInterrupt
		return nil, statementHandleInterrupt, nil
	}
	if control != statementContinue && control != statementReturnFunction {
		return nil, statementContinue, fmt.Errorf(
			"%w: loop control escaped function %q",
			ErrInvalidFunctionImplementation, state.implementation.Name,
		)
	}

	var returned any
	if child.returned {
		returned = child.returnValue
	} else if state.implementation.Return != nil {
		evaluated, err := evaluateClosedRuleValue(
			"function "+state.targetName+" return", *state.implementation.Return,
			state.functionMatch.Bindings, state.targetCells,
		)
		if err != nil {
			return nil, statementContinue, err
		}
		returned = evaluated.value
		if err := incorporateEvaluatedStateReads(child, evaluated.reads, evaluated.causes); err != nil {
			return nil, statementContinue, err
		}
	}
	if err := releaseFunctionModuleLocals(
		modelDigest, state.localModules, functionRuntime, child,
	); err != nil {
		return nil, statementContinue, err
	}
	pendingOperations := append([]stateOperationReference(nil), child.pendingOperations...)
	drainResumableFunctionExecution(state, child, parent)
	if state.implementation.ReturnType != "" &&
		!valueMatchesPredefinedType(returned, state.implementation.ReturnType) {
		return nil, statementContinue, fmt.Errorf(
			"%w: function %q returned %T, want %s",
			ErrInvalidFunctionImplementation, state.targetName, returned,
			state.implementation.ReturnType,
		)
	}
	providerReturnParameters, err := canonicalFunctionReturnParameters(
		state.implementation.ReturnType, state.providerActuals, returned,
	)
	if err != nil {
		return nil, statementContinue, err
	}
	callerReturnParameters, err := canonicalFunctionReturnParameters(
		state.implementation.ReturnType, state.actuals, returned,
	)
	if err != nil {
		return nil, statementContinue, err
	}
	returnCauses := canonicalEventIDs(parent.control)
	returnEvent, err := gorapide.NewDeterministicEvent(gorapide.EventProvenance{
		Profile: CompatibilityProfile, Model: modelDigest,
		Instance: state.targetComponentID,
		Action:   state.targetName + "'Return", Occurrence: state.occurrence + "|return",
		Causes: returnCauses,
		Timings: functionRuntime.clocks.instantTimings(
			state.targetComponentID, state.callerComponentID,
		),
	}, providerReturnParameters)
	if err != nil {
		return nil, statementContinue, err
	}
	if err := addStateOperationSuccessors(pendingOperations, string(returnEvent.ID)); err != nil {
		return nil, statementContinue, err
	}
	if state.remote {
		returnEvent.Observations = append(returnEvent.Observations, gorapide.EventObservation{
			Name:   state.implementation.Name + "'Return",
			Source: state.callerComponentID, Params: callerReturnParameters,
		})
		for index := 0; index+1 < len(state.aliases); index++ {
			parameters, err := canonicalFunctionReturnParameters(
				state.implementation.ReturnType, state.aliasActuals[index], returned,
			)
			if err != nil {
				return nil, statementContinue, err
			}
			returnEvent.Observations = append(returnEvent.Observations, gorapide.EventObservation{
				Name:   state.aliases[index].Name + "'Return",
				Source: state.aliases[index].ComponentID, Params: parameters,
			})
		}
	}
	returnSnapshots := map[string]map[string]*stateCell{
		state.targetComponentID: cloneStateCells(state.targetCells),
		state.callerComponentID: cloneStateCells(state.callerCells),
	}
	parent.generated = append(parent.generated, generatedRuleOutput{
		localID: state.prefix + "/return", event: returnEvent, causes: returnCauses,
		stateSnapshot:        cloneStateCells(state.targetCells),
		observationSnapshots: returnSnapshots,
	})
	parent.control = []gorapide.EventID{returnEvent.ID}
	parent.pendingOperations = nil
	return returned, statementContinue, nil
}

func functionCallMaySuspend(
	componentID string,
	call FunctionCall,
	runtime *functionExecutionRuntime,
) bool {
	return functionCallMaySuspendWithState(
		componentID, call, runtime, make(map[string]bool), make(map[string]bool),
	)
}

func functionCallMaySuspendWithState(
	componentID string,
	call FunctionCall,
	runtime *functionExecutionRuntime,
	visiting, memo map[string]bool,
) bool {
	if runtime == nil {
		return false
	}
	implementation := runtime.callables[componentID][call.functionKey]
	if implementation == nil {
		return false
	}
	key := componentID + "\x00" + call.functionKey
	if memo[key] {
		return true
	}
	if visiting[key] {
		return false
	}
	visiting[key] = true
	targetComponentID := implementation.targetComponent
	if targetComponentID == "" {
		targetComponentID = componentID
	}
	result := statementListMaySuspendThroughFunctions(
		targetComponentID, implementation.Statements, runtime, visiting, memo,
	)
	delete(visiting, key)
	if result {
		memo[key] = true
	}
	return result
}

func statementListMaySuspendThroughFunctions(
	componentID string,
	statements []Statement,
	runtime *functionExecutionRuntime,
	visiting, memo map[string]bool,
) bool {
	for _, statement := range statements {
		if statementSuspendsProcess(statement) {
			return true
		}
		if statement.kind == FunctionCallStatement && functionCallMaySuspendWithState(
			componentID, statement.functionCall, runtime, visiting, memo,
		) {
			return true
		}
		if statement.kind == GeneralForStatementKind {
			for _, expression := range []ExecutableObjectExpression{
				statement.forInitial, statement.forTest, statement.forNext,
			} {
				if expression.kind == ObjectFunctionExpression &&
					functionCallMaySuspendWithState(
						componentID, expression.call, runtime, visiting, memo,
					) {
					return true
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
			if statementListMaySuspendThroughFunctions(
				componentID, group, runtime, visiting, memo,
			) {
				return true
			}
		}
	}
	return false
}

func ruleBodyMaySuspendThroughFunctions(
	componentID string,
	body *RuleBody,
	runtime *functionExecutionRuntime,
) bool {
	if body == nil {
		return false
	}
	return statementListMaySuspendThroughFunctions(
		componentID, body.Statements, runtime, make(map[string]bool), make(map[string]bool),
	)
}

func validateProcessFunctionSuspensionContexts(
	componentID string,
	statements []Statement,
	runtime *functionExecutionRuntime,
) error {
	return validateFunctionSuspensionStatementList(
		componentID, statements, runtime, make(map[string]bool), false,
	)
}

func processBodyNeedsInterruptConnectionContinuation(
	componentID string,
	statements []Statement,
	runtime *functionExecutionRuntime,
) bool {
	return statementListNeedsInterruptConnectionContinuation(
		componentID, statements, runtime, make(map[string]bool), false,
	)
}

// functionImplementationRequiresInitializationInterruptOwner identifies a
// function-owned action-handler lifetime that reaches generation-time work.
// Function bodies are canonical before their call sites are known, so the
// immediate call path uses this derived property to reject an unowned context
// before emitting Call. A same-component module initializer supplies the
// synchronous owner proven by the allocator subset; process-owned function
// handlers retain their separate continuation boundary.
func functionImplementationRequiresInitializationInterruptOwner(
	componentID string,
	implementation *FunctionImplementation,
	runtime *functionExecutionRuntime,
) bool {
	if implementation == nil || runtime == nil {
		return false
	}
	return statementListNeedsInterruptConnectionContinuation(
		componentID, implementation.Statements, runtime, make(map[string]bool), false,
	)
}

func functionCallNeedsInterruptConnectionContinuation(
	componentID string,
	call FunctionCall,
	runtime *functionExecutionRuntime,
) bool {
	implementation, targetComponentID, exists := functionCallTargetImplementation(
		componentID, call, runtime,
	)
	if !exists {
		return false
	}
	return functionImplementationRequiresInitializationInterruptOwner(
		targetComponentID, implementation, runtime,
	)
}

func functionCallTargetImplementation(
	componentID string,
	call FunctionCall,
	runtime *functionExecutionRuntime,
) (*FunctionImplementation, string, bool) {
	if runtime == nil || runtime.callables[componentID] == nil {
		return nil, "", false
	}
	implementation := runtime.callables[componentID][call.functionKey]
	if implementation == nil {
		return nil, "", false
	}
	targetComponentID := implementation.targetComponent
	if targetComponentID == "" {
		targetComponentID = componentID
	}
	return implementation, targetComponentID, true
}

func statementListNeedsInterruptConnectionContinuation(
	componentID string,
	statements []Statement,
	runtime *functionExecutionRuntime,
	visiting map[string]bool,
	inheritedInterrupt bool,
) bool {
	for _, statement := range statements {
		// A direct allocator expression can synchronously generate Start and
		// initializer action occurrences before New returns. While an enclosing
		// process handler is active, those occurrences require the same retained
		// generation-time visible-poset continuation as a cross-component call.
		// The immediate rule-body path marks handlers as statement-local and
		// therefore cannot preserve this process-owned lifetime across the fresh
		// child initializer.
		if inheritedInterrupt && statementTreeAllocatesModule(statement) {
			return true
		}
		checkCall := func(call FunctionCall) bool {
			if runtime == nil || runtime.callables[componentID] == nil {
				return false
			}
			implementation := runtime.callables[componentID][call.functionKey]
			if implementation == nil {
				return false
			}
			if inheritedInterrupt {
				for _, local := range implementation.Locals {
					if ruleValueAllocatesModule(local.Value) {
						return true
					}
				}
			}
			targetComponentID := implementation.targetComponent
			if targetComponentID == "" {
				targetComponentID = componentID
			}
			if inheritedInterrupt && targetComponentID != componentID {
				return true
			}
			key := componentID + "\x00" + call.functionKey + "\x00" + fmt.Sprint(inheritedInterrupt)
			if visiting[key] {
				return false
			}
			visiting[key] = true
			result := statementListNeedsInterruptConnectionContinuation(
				targetComponentID, implementation.Statements, runtime, visiting, inheritedInterrupt,
			)
			delete(visiting, key)
			return result
		}

		if statement.kind == FunctionCallStatement && checkCall(statement.functionCall) {
			return true
		}
		if statement.kind == GeneralForStatementKind {
			for _, expression := range []ExecutableObjectExpression{
				statement.forInitial, statement.forTest, statement.forNext,
			} {
				if expression.kind == ObjectFunctionExpression && checkCall(expression.call) {
					return true
				}
			}
		}

		for _, choice := range statement.handler.Choices {
			if statementListNeedsInterruptConnectionContinuation(
				componentID, choice.Statements, runtime, visiting, inheritedInterrupt,
			) {
				return true
			}
		}
		if statementListNeedsInterruptConnectionContinuation(
			componentID, statement.handler.Else, runtime, visiting, inheritedInterrupt,
		) {
			return true
		}
		protectedInterrupt := inheritedInterrupt
		if statement.kind == HandlerBlockStatementKind && handlerHasInterruptChoice(statement.handler) {
			protectedInterrupt = true
		}
		if statementListNeedsInterruptConnectionContinuation(
			componentID, statement.handledBody, runtime, visiting, protectedInterrupt,
		) {
			return true
		}
		groups := [][]Statement{
			statement.thenBranch, statement.elseBranch, statement.loopBody,
			statement.caseDefault,
		}
		for _, alternative := range statement.caseAlts {
			groups = append(groups, alternative.body)
		}
		for _, group := range groups {
			if statementListNeedsInterruptConnectionContinuation(
				componentID, group, runtime, visiting, inheritedInterrupt,
			) {
				return true
			}
		}
	}
	return false
}

func validateFunctionSuspensionStatementList(
	componentID string,
	statements []Statement,
	runtime *functionExecutionRuntime,
	visiting map[string]bool,
	inheritedInterrupt bool,
) error {
	for _, statement := range statements {
		validateCall := func(call FunctionCall) error {
			implementation := runtime.callables[componentID][call.functionKey]
			if implementation == nil {
				return fmt.Errorf(
					"%w: call %q resolved to a missing function",
					ErrInvalidFunctionImplementation, call.ID,
				)
			}
			targetComponentID := implementation.targetComponent
			if targetComponentID == "" {
				targetComponentID = componentID
			}
			key := componentID + "\x00" + call.functionKey + "\x00" + fmt.Sprint(inheritedInterrupt)
			if !visiting[key] {
				visiting[key] = true
				if err := validateFunctionSuspensionStatementList(
					targetComponentID, implementation.Statements, runtime, visiting, inheritedInterrupt,
				); err != nil {
					return err
				}
				delete(visiting, key)
			}
			return nil
		}

		if statement.kind == FunctionCallStatement {
			if err := validateCall(statement.functionCall); err != nil {
				return err
			}
		}
		if statement.kind == GeneralForStatementKind {
			for _, expression := range []ExecutableObjectExpression{
				statement.forInitial, statement.forTest, statement.forNext,
			} {
				if expression.kind == ObjectFunctionExpression {
					if err := validateCall(expression.call); err != nil {
						return err
					}
				}
			}
		}

		groups := [][]Statement{
			statement.thenBranch, statement.elseBranch, statement.loopBody,
			statement.caseDefault,
		}
		for _, alternative := range statement.caseAlts {
			groups = append(groups, alternative.body)
		}
		for _, choice := range statement.handler.Choices {
			if err := validateFunctionSuspensionStatementList(
				componentID, choice.Statements, runtime, visiting, inheritedInterrupt,
			); err != nil {
				return err
			}
		}
		if err := validateFunctionSuspensionStatementList(
			componentID, statement.handler.Else, runtime, visiting, inheritedInterrupt,
		); err != nil {
			return err
		}
		protectedInterrupt := inheritedInterrupt
		if statement.kind == HandlerBlockStatementKind && handlerHasInterruptChoice(statement.handler) {
			protectedInterrupt = true
		}
		if err := validateFunctionSuspensionStatementList(
			componentID, statement.handledBody, runtime, visiting, protectedInterrupt,
		); err != nil {
			return err
		}
		for _, group := range groups {
			if err := validateFunctionSuspensionStatementList(
				componentID, group, runtime, visiting, inheritedInterrupt,
			); err != nil {
				return err
			}
		}
	}
	return nil
}

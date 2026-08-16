package arch

import (
	"fmt"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/pattern"
)

type moduleExceptionHandlerExecutionResult struct {
	raised              *raisedExceptionOccurrence
	propagationComplete bool
}

// activateModuleHandlerExecution returns an isolated lookup set for one
// synchronous handler activation. A child exception raised while that handler
// body is executing must not select the same handler again. Nested handlers in
// other modules remain eligible and receive their own activation entry.
func activateModuleHandlerExecution(
	context processTerminationContext,
	moduleID string,
	incomingEventID gorapide.EventID,
) (processTerminationContext, error) {
	if moduleID == "" || incomingEventID == "" {
		return context, fmt.Errorf("%w: active module handler identity is incomplete", ErrInvalidExceptionHandler)
	}
	active := make(map[string]gorapide.EventID, len(context.activeModuleHandlers)+1)
	for existingModuleID, eventID := range context.activeModuleHandlers {
		active[existingModuleID] = eventID
	}
	if eventID, exists := active[moduleID]; exists {
		return context, fmt.Errorf(
			"%w: module handler %q is already active for exception %q",
			ErrInvalidExceptionHandler, moduleID, eventID,
		)
	}
	active[moduleID] = incomingEventID
	context.activeModuleHandlers = active
	return context, nil
}

// propagatedExceptionOccurrenceDeliveredToActiveHandler recovers the exact
// occurrence that terminated a handler owner while its handler body was still
// active. Unlike architecture-initial structural ownership, the delivery may
// be parent, linked, or both; the propagation record retains that distinction.
// A unique source is required so map traversal can never select the occurrence.
func propagatedExceptionOccurrenceDeliveredToActiveHandler(
	targetModuleID string,
	eventID gorapide.EventID,
	context processTerminationContext,
) (*raisedExceptionOccurrence, error) {
	runtime := context.functionRuntime
	if runtime == nil || runtime.propagations == nil || targetModuleID == "" || eventID == "" {
		return nil, fmt.Errorf("%w: active-handler propagation lookup is incomplete", ErrUnhandledRapideException)
	}
	sourceModuleID := ""
	for _, record := range runtime.propagations.recordsByKey {
		if record.ExceptionEventID != string(eventID) {
			continue
		}
		for _, target := range record.Targets {
			if target.ModuleID != targetModuleID || target.Disposition != exceptionDeliveredDisposition ||
				len(target.Relations) == 0 {
				continue
			}
			if sourceModuleID != "" && sourceModuleID != record.SourceModuleID {
				return nil, fmt.Errorf(
					"%w: active module handler %q has ambiguous propagation source for %q",
					ErrUnhandledRapideException, targetModuleID, eventID,
				)
			}
			sourceModuleID = record.SourceModuleID
		}
	}
	if sourceModuleID == "" {
		return nil, fmt.Errorf(
			"%w: active module handler %q has no delivered propagation for %q",
			ErrUnhandledRapideException, targetModuleID, eventID,
		)
	}
	return propagatedExceptionOccurrenceBySource(sourceModuleID, eventID, context)
}

func executeModuleExceptionHandler(
	componentID string,
	invocation *moduleExceptionHandlerInvocation,
	raised *raisedExceptionOccurrence,
	context processTerminationContext,
) (moduleExceptionHandlerExecutionResult, error) {
	var result moduleExceptionHandlerExecutionResult
	runtime := context.functionRuntime
	if runtime == nil || runtime.model == nil || invocation == nil || raised == nil || raised.event == nil {
		return result, fmt.Errorf("%w: module handler invocation is incomplete", ErrInvalidExceptionHandler)
	}
	component := runtime.components[componentID]
	if component == nil || context.statementSteps == nil || context.firings == nil {
		return result, fmt.Errorf("%w: module handler %q has no execution context", ErrInvalidExceptionHandler, componentID)
	}
	if uint64(len(*context.firings)) >= runtime.maxFirings {
		return result, fmt.Errorf("%w: max_firings=%d", ErrExecutionLimit, runtime.maxFirings)
	}
	moduleID := runtime.modules[componentID].Identity()
	if moduleID == "" {
		return result, fmt.Errorf("%w: module handler %q has no module identity", ErrInvalidExceptionHandler, componentID)
	}
	activeContext, err := activateModuleHandlerExecution(context, moduleID, raised.event.ID)
	if err != nil {
		return result, err
	}
	canonicalMatch, err := pattern.CanonicalizeMatch(invocation.match)
	if err != nil {
		return result, fmt.Errorf("%w: module handler %q match: %v", ErrInvalidExceptionHandler, componentID, err)
	}
	ruleID := "module-handler/" + componentID + "/" + invocation.selection + "/" + string(raised.event.ID)
	rule := &DeclarativeRule{
		ID: ruleID, Process: RuleAgentProcess,
		Body: &RuleBody{Statements: copyStatements(invocation.statements)},
	}
	bodyResult, err := buildDeclarativeRuleBody(
		componentID, component, rule, invocation.match, canonicalMatch,
		runtime.model.digest, nil, context.moduleState[componentID], runtime,
		nil, nil, context.statementSteps, context.clocks,
		"module-handler:"+componentID+"\x00"+string(raised.event.ID), nil, raised,
	)
	if err != nil {
		return result, fmt.Errorf("%w: module handler %q: %v", ErrInvalidExceptionHandler, componentID, err)
	}
	if bodyResult.initializationFailure == nil && (bodyResult.exitProcess || len(bodyResult.scheduled) != 0) {
		return result, fmt.Errorf("%w: module handler %q escaped the immediate recovery subset",
			ErrInvalidExceptionHandler, componentID)
	}
	for _, output := range bodyResult.generated {
		if err := context.poset.AddEventWithCause(output.event, output.causes...); err != nil {
			return result, fmt.Errorf("%w: module handler %q output %s: %v",
				ErrInvalidExceptionHandler, componentID, output.localID, err)
		}
	}
	generated := make([]GeneratedEventRecord, 0, len(bodyResult.generated))
	for _, output := range bodyResult.generated {
		depth := eventDepth(context.poset, output.event, context.depths)
		context.depths[output.event.ID] = depth
		if err := enqueueGeneratedObservationViews(
			output, depth, context.moduleState, context.stateSnapshots, context.queue, context.seenItems,
		); err != nil {
			return result, err
		}
		generated = append(generated, GeneratedEventRecord{
			OutputID: output.localID, EventID: string(output.event.ID), Exception: output.exception,
		})
	}
	canceledSchedules := append([]string(nil), bodyResult.canceledSchedules...)
	if bodyResult.initializationFailure != nil {
		canceledSchedules = append(canceledSchedules, scheduledActionIDs(bodyResult.scheduled)...)
	}
	canceledSchedules = canonicalStrings(canceledSchedules)
	firing := FiringRecord{
		Sequence: uint64(len(*context.firings) + 1), Transition: "module-handler",
		RuleID: ruleID, RuleProcess: invocation.selection,
		MatchedEvents: append([]string(nil), canonicalMatch.Events...),
		Bindings:      append([]pattern.CanonicalBinding(nil), canonicalMatch.Bindings...),
		Target:        componentID, Generated: generated,
		Scheduled:         scheduledPlans(bodyResult.scheduled),
		CanceledSchedules: canceledSchedules,
		StateReads:        bodyResult.stateReads, StateWrites: bodyResult.stateWrites,
	}
	if bodyResult.initializationFailure != nil {
		firing.Completion = "exception"
		firing.ExceptionEventID = string(bodyResult.initializationFailure.raised.event.ID)
	}
	*context.firings = append(*context.firings, firing)
	if bodyResult.initializationFailure == nil {
		result.raised = bodyResult.raised
		return result, nil
	}

	failureOutcome, err := finalizeFailedModuleInitializationChain(
		bodyResult.initializationFailure, activeContext,
	)
	if err != nil {
		return result, fmt.Errorf(
			"%w: module handler %q allocator initialization: %v",
			ErrInvalidExceptionHandler, componentID, err,
		)
	}
	ownerLifecycle := runtime.lifecycle.modules[moduleID]
	if ownerLifecycle == nil {
		return result, fmt.Errorf("%w: module handler %q lifecycle is unavailable", ErrInvalidExceptionHandler, componentID)
	}
	if ownerLifecycle.terminationEventID == "" {
		if ownerLifecycle.state != ModuleRunningState && ownerLifecycle.state != ModuleCompletedState {
			return result, fmt.Errorf(
				"%w: module handler %q abandoned on failed creation with invalid owner state %q",
				ErrInvalidExceptionHandler, componentID, ownerLifecycle.state,
			)
		}
		// Recovery inside the fresh creation chain prevented any exception from
		// reaching the handler owner. The handler activation still cannot resume
		// because its synchronous generator call produced no value.
		return result, nil
	}
	if ownerLifecycle.state != ModuleTerminatedState && ownerLifecycle.state != ModuleFinalizedState {
		return result, fmt.Errorf(
			"%w: module handler %q has termination occurrence %q in state %q",
			ErrInvalidExceptionHandler, componentID, ownerLifecycle.terminationEventID, ownerLifecycle.state,
		)
	}
	escaped, err := propagatedExceptionOccurrenceDeliveredToActiveHandler(
		moduleID, ownerLifecycle.terminationEventID, activeContext,
	)
	if err != nil {
		return result, fmt.Errorf(
			"%w: module handler %q failed-creation escape: %v (leaf %q from %q)",
			ErrInvalidExceptionHandler, componentID, err,
			failureOutcome.activeRaised.event.ID, failureOutcome.structuralSourceModuleID,
		)
	}
	result.raised = escaped
	result.propagationComplete = true
	return result, nil
}

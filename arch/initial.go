package arch

import (
	"errors"
	"fmt"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/pattern"
)

// ErrInvalidModuleInitial identifies a malformed closed module initial part.
var ErrInvalidModuleInitial = errors.New("invalid declarative Rapide module initial part")

// SetInitialStatements installs the closed ordinary-statement list executed
// once after module state and function routes have been elaborated, but before
// any module process is activated or journal input is observed.
func (component *Component) SetInitialStatements(statements ...Statement) error {
	if component == nil {
		return fmt.Errorf("%w: component is nil", ErrInvalidModuleInitial)
	}
	if len(statements) == 0 {
		return fmt.Errorf("%w: component %q initial part is empty", ErrInvalidModuleInitial, component.ID)
	}
	component.mu.Lock()
	defer component.mu.Unlock()
	if component.initialStatements != nil {
		return fmt.Errorf("%w: component %q already has an initial part", ErrInvalidModuleInitial, component.ID)
	}
	component.initialStatements = copyStatements(statements)
	return nil
}

func executeModuleInitialParts(
	model *deterministicModel,
	functionRuntime *functionExecutionRuntime,
	moduleState moduleStateRuntime,
	processes map[string][]*processRuntime,
	statementSteps *statementBudget,
	clocks *deterministicClockKernel,
	frontiers *causalFrontierRegistry,
	poset *gorapide.Poset,
	depths map[gorapide.EventID]uint64,
	queue *executionQueue,
	seenItems map[string]bool,
	stateSnapshots stateSnapshotRegistry,
	startupFrontiers map[string][]gorapide.EventID,
	maxFirings uint64,
	firings *[]FiringRecord,
) (*architectureInitializationAbandonment, error) {
	for _, componentID := range model.componentIDs {
		startupFrontier := canonicalEventIDs(startupFrontiers[componentID])
		if len(startupFrontier) != 1 {
			return nil, fmt.Errorf("%w: component %q has invalid architecture startup frontier %#v",
				ErrInvalidModuleInitial, componentID, startupFrontier)
		}
		for _, runtime := range processes[componentID] {
			frontiers.set(runtime.causalOwner, startupFrontier)
			runtime.frontier = canonicalEventIDs(startupFrontier)
		}
		statements := model.initialStatements[componentID]
		if statements == nil {
			for _, runtime := range processes[componentID] {
				runtime.elaborated = true
			}
			continue
		}
		if uint64(len(*firings)) >= maxFirings {
			return nil, fmt.Errorf("%w: max_firings=%d", ErrExecutionLimit, maxFirings)
		}
		initialMatch, parameterReads, initialControl, _, err := resolveModuleInitializationDefaults(
			componentID, "module initial "+componentID,
			model.initializationParameters[componentID], moduleState[componentID], startupFrontier, nil,
		)
		if err != nil {
			return nil, fmt.Errorf("%w: component %q parameters: %w", ErrInvalidModuleInitial, componentID, err)
		}
		canonicalMatch, err := pattern.CanonicalizeMatch(initialMatch)
		if err != nil {
			return nil, err
		}
		owner := "initial:" + componentID
		rule := &DeclarativeRule{
			ID: "module-initial/" + componentID, Process: RulePipeProcess,
			Body: &RuleBody{Statements: statements}, initializationOwned: true,
		}
		body, err := buildDeclarativeRuleBody(
			componentID, model.components[componentID], rule, initialMatch, canonicalMatch,
			model.digest, nil, moduleState[componentID], functionRuntime, initialControl, parameterReads,
			statementSteps, clocks, owner, nil,
		)
		if err != nil {
			return nil, fmt.Errorf("%w: component %q: %w", ErrInvalidModuleInitial, componentID, err)
		}
		if body.exitProcess && body.initializationFailure == nil && body.raised == nil {
			return nil, fmt.Errorf("%w: component %q initial control escaped its body", ErrInvalidModuleInitial, componentID)
		}
		generated := make([]GeneratedEventRecord, 0, len(body.generated))
		for _, output := range body.generated {
			if err := poset.AddEventWithCause(output.event, output.causes...); err != nil {
				return nil, fmt.Errorf("%w: component %q output %s: %w", ErrInvalidModuleInitial, componentID, output.localID, err)
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
		initialFrontier := canonicalEventIDs(body.frontier)
		startupFrontiers[componentID] = append([]gorapide.EventID(nil), initialFrontier...)
		frontiers.set(owner, initialFrontier)
		canceledSchedules := append([]string(nil), body.canceledSchedules...)
		if body.raised != nil || body.initializationFailure != nil {
			// Initializer schedules are not installed in the clock kernel until
			// successful module creation. An escaping exception or a nested New
			// that returns no value abandons the initializer, so every accumulated
			// plan is canceled explicitly in the firing audit.
			canceledSchedules = append(canceledSchedules, scheduledActionIDs(body.scheduled)...)
		}
		canceledSchedules = canonicalStrings(canceledSchedules)
		firing := FiringRecord{
			Sequence: uint64(len(*firings) + 1), Transition: "initial", Target: componentID,
			Generated: generated, Scheduled: scheduledPlans(body.scheduled),
			CanceledSchedules: canceledSchedules,
			StateReads:        body.stateReads,
			StateWrites:       body.stateWrites,
		}
		if body.initializationFailure != nil {
			firing.Completion = "exception"
			firing.ExceptionEventID = string(body.initializationFailure.raised.event.ID)
		}
		*firings = append(*firings, firing)
		terminationContext := processTerminationContext{
			modelDigest: model.digest, functionRuntime: functionRuntime,
			frontiers: frontiers, clocks: clocks, poset: poset, depths: depths,
			queue: queue, seenItems: seenItems, moduleState: moduleState,
			stateSnapshots: stateSnapshots, statementSteps: statementSteps, firings: firings,
		}
		if body.initializationFailure != nil {
			failure := body.initializationFailure
			outcome, err := finalizeFailedModuleInitializationChain(failure, terminationContext)
			if err != nil {
				return nil, fmt.Errorf("%w: component %q allocator initialization: %v", ErrInvalidModuleInitial, componentID, err)
			}
			abandonment, err := finalizeStaticModuleAfterFailedCreation(
				componentID, model, outcome, initialFrontier, functionRuntime,
				startupFrontiers, terminationContext,
			)
			if err != nil {
				return nil, fmt.Errorf("%w: component %q failed-creation completion: %v", ErrInvalidModuleInitial, componentID, err)
			}
			return abandonment, nil
		}
		if body.raised != nil {
			if err := finalizeFailedModuleInitialization(
				componentID, body.raised, terminationContext,
			); err != nil {
				return nil, fmt.Errorf("%w: component %q exception: %v", ErrInvalidModuleInitial, componentID, err)
			}
			ownerID := model.componentArchitectures[componentID]
			if ownerID == "" {
				ownerID = ArchitectureInterfaceID
			}
			return &architectureInitializationAbandonment{
				ownerID: ownerID, activeRaised: body.raised,
			}, nil
		}
		clocks.addScheduled(body.scheduled)
		for _, runtime := range processes[componentID] {
			runtime.elaborated = true
			frontiers.set(runtime.causalOwner, initialFrontier)
			runtime.frontier = append([]gorapide.EventID(nil), initialFrontier...)
		}
	}
	return nil, nil
}

// failedModuleInitializationOutcome is the exact structural state left after
// a failed leaf creation and every enclosing fresh generator frame have been
// closed. A nonempty handledFrontier means a fresh ancestor handled the leaf
// and no exception reached the next lexical caller. Otherwise activeRaised was
// delivered from structuralSourceModuleID to that caller (and may be a handler
// replacement occurrence rather than the original leaf occurrence).
type failedModuleInitializationOutcome struct {
	activeRaised             *raisedExceptionOccurrence
	structuralSourceModuleID string
	handledFrontier          []gorapide.EventID
}

// architectureInitializationAbandonment carries an incomplete static
// declaration or architecture-initial outcome to the enclosing architecture
// generator. A nonempty handledFrontier denotes normal abandonment after
// recovery; otherwise activeRaised is the exact occurrence already propagated
// into the architecture module.
type architectureInitializationAbandonment struct {
	ownerID             string
	activeRaised        *raisedExceptionOccurrence
	handledFrontier     []gorapide.EventID
	architectureInitial bool
}

// finalizeStaticModuleAfterFailedCreation closes a static generator frame
// whose initializer cannot return because a nested New produced no value. A
// propagated occurrence exceptionally finalizes the static module. A handled
// occurrence leaves it nonterminated but still unreturned, so it follows the
// ordinary final path before Finish. In both cases the enclosing architecture
// generator must abandon rather than retain a fabricated constituent result.
func finalizeStaticModuleAfterFailedCreation(
	componentID string,
	model *deterministicModel,
	outcome failedModuleInitializationOutcome,
	initialFrontier []gorapide.EventID,
	functionRuntime *functionExecutionRuntime,
	startupFrontiers map[string][]gorapide.EventID,
	context processTerminationContext,
) (*architectureInitializationAbandonment, error) {
	if componentID == "" || model == nil || functionRuntime == nil || functionRuntime.lifecycle == nil ||
		outcome.activeRaised == nil || outcome.activeRaised.event == nil ||
		outcome.structuralSourceModuleID == "" {
		return nil, fmt.Errorf("%w: static module failed-creation outcome is incomplete", ErrUnhandledRapideException)
	}
	ownerID := model.componentArchitectures[componentID]
	if ownerID == "" {
		ownerID = ArchitectureInterfaceID
	}
	moduleID := functionRuntime.modules[componentID].Identity()
	if moduleID == "" {
		return nil, fmt.Errorf("%w: component %q has no generated module allocation",
			ErrUnhandledRapideException, componentID)
	}
	lifecycle := functionRuntime.lifecycle.modules[moduleID]
	if lifecycle == nil {
		return nil, fmt.Errorf("%w: static module %q has no lifecycle", ErrUnhandledRapideException, moduleID)
	}

	if lifecycle.state != ModuleTerminatedState {
		recovery := canonicalEventIDs(outcome.handledFrontier)
		if len(recovery) == 0 && functionRuntime.propagations != nil &&
			functionRuntime.propagations.handledParentTarget(
				outcome.activeRaised.event.ID, outcome.structuralSourceModuleID, moduleID,
			) {
			var err error
			recovery, err = moduleHandlerRecoveryFrontier(
				componentID, outcome.activeRaised.event.ID, context,
			)
			if err != nil {
				return nil, err
			}
		}
		if len(recovery) != 0 {
			if err := finalizeHandledAbandonedStaticModuleInitializationByIdentity(
				moduleID, componentID, recovery, context,
			); err != nil {
				return nil, err
			}
			startupFrontiers[componentID] = append([]gorapide.EventID(nil), recovery...)
			return &architectureInitializationAbandonment{
				ownerID: ownerID, handledFrontier: canonicalEventIDs(recovery),
			}, nil
		}
		return nil, fmt.Errorf(
			"%w: unhandled static module %q has invalid lifecycle state %q",
			ErrUnhandledRapideException, moduleID, lifecycle.state,
		)
	}
	activeRaised := outcome.activeRaised
	if lifecycle.terminationEventID != activeRaised.event.ID {
		disposition, _ := functionRuntime.propagations.parentTargetDisposition(
			activeRaised.event.ID, outcome.structuralSourceModuleID, moduleID,
		)
		if disposition != exceptionHandlerRaisedDisposition {
			return nil, fmt.Errorf(
				"%w: static module %q terminated on unrelated occurrence %q",
				ErrInvalidDeclarativeStatement, moduleID, lifecycle.terminationEventID,
			)
		}
		replacement, err := propagatedExceptionOccurrenceBySource(
			moduleID, lifecycle.terminationEventID, context,
		)
		if err != nil {
			return nil, err
		}
		activeRaised = replacement
	}
	startupFrontiers[componentID] = append([]gorapide.EventID(nil), initialFrontier...)
	if err := finalizeTerminatedModuleInitializationByIdentity(
		moduleID, componentID, activeRaised, context,
	); err != nil {
		return nil, err
	}
	return &architectureInitializationAbandonment{
		ownerID: ownerID, activeRaised: activeRaised,
	}, nil
}

func finalizeFailedModuleInitialization(
	componentID string,
	raised *raisedExceptionOccurrence,
	context processTerminationContext,
) error {
	runtime := context.functionRuntime
	if runtime == nil || runtime.lifecycle == nil || runtime.contexts == nil ||
		raised == nil || raised.event == nil {
		return fmt.Errorf("%w: initialization exception runtime is incomplete", ErrUnhandledRapideException)
	}
	module := runtime.modules[componentID]
	moduleID := module.Identity()
	if moduleID == "" {
		return fmt.Errorf("%w: component %q has no generated module allocation",
			ErrUnhandledRapideException, componentID)
	}
	return finalizeFailedModuleInitializationByIdentity(
		moduleID, componentID, raised, context,
	)
}

func finalizeFailedModuleInitializationByIdentity(
	moduleID, stateOwnerID string,
	raised *raisedExceptionOccurrence,
	context processTerminationContext,
) error {
	runtime := context.functionRuntime
	if runtime == nil || runtime.lifecycle == nil || runtime.contexts == nil ||
		raised == nil || raised.event == nil || moduleID == "" || stateOwnerID == "" {
		return fmt.Errorf("%w: initialization exception runtime is incomplete", ErrUnhandledRapideException)
	}
	if err := runtime.lifecycle.terminate(moduleID, raised.event.ID); err != nil {
		return fmt.Errorf("%w: %v", ErrUnhandledRapideException, err)
	}
	// A module handler catches exceptions propagated by its processes; it is not
	// an enclosing handler for the generator's initialization statement list.
	if err := propagateUnhandledModuleException(moduleID, raised, context); err != nil {
		return fmt.Errorf("%w: %v", ErrUnhandledRapideException, err)
	}
	return finalizeTerminatedModuleInitializationByIdentity(
		moduleID, stateOwnerID, raised, context,
	)
}

func finalizeFailedModuleInitializationChain(
	failure *failedModuleInitialization,
	context processTerminationContext,
) (failedModuleInitializationOutcome, error) {
	var outcome failedModuleInitializationOutcome
	if failure == nil || failure.moduleID == "" || failure.stateOwnerID == "" ||
		failure.raised == nil || failure.raised.event == nil {
		return outcome, fmt.Errorf("%w: failed initialization chain is incomplete", ErrUnhandledRapideException)
	}
	if err := finalizeFailedModuleInitializationByIdentity(
		failure.moduleID, failure.stateOwnerID, failure.raised, context,
	); err != nil {
		return outcome, err
	}
	activeRaised := failure.raised
	structuralSourceModuleID := failure.moduleID
	var handledAbandonmentFrontier []gorapide.EventID
	for _, caller := range failure.abandonedCallers {
		if caller.moduleID == "" || caller.stateOwnerID == "" {
			return outcome, fmt.Errorf("%w: failed initialization caller is incomplete", ErrUnhandledRapideException)
		}
		callerLifecycle := context.functionRuntime.lifecycle.modules[caller.moduleID]
		if callerLifecycle == nil {
			return outcome, fmt.Errorf(
				"%w: recursively abandoned module %q has no lifecycle",
				ErrUnhandledRapideException, caller.moduleID,
			)
		}
		if callerLifecycle.state == ModuleTerminatedState {
			if len(handledAbandonmentFrontier) != 0 {
				return outcome, fmt.Errorf(
					"%w: recursively abandoned module %q mixed handled structural recovery with later exceptional termination",
					ErrInvalidDeclarativeStatement, caller.moduleID,
				)
			}
			if callerLifecycle.terminationEventID != activeRaised.event.ID {
				disposition, _ := context.functionRuntime.propagations.parentTargetDisposition(
					activeRaised.event.ID, structuralSourceModuleID, caller.moduleID,
				)
				if disposition != exceptionHandlerRaisedDisposition {
					return outcome, fmt.Errorf(
						"%w: recursively abandoned module %q terminated on unrelated occurrence %q",
						ErrInvalidDeclarativeStatement, caller.moduleID, callerLifecycle.terminationEventID,
					)
				}
				replacement, err := propagatedExceptionOccurrenceBySource(
					caller.moduleID, callerLifecycle.terminationEventID, context,
				)
				if err != nil {
					return outcome, err
				}
				activeRaised = replacement
			}
			if err := finalizeTerminatedModuleInitializationByIdentity(
				caller.moduleID, caller.stateOwnerID, activeRaised, context,
			); err != nil {
				return outcome, fmt.Errorf(
					"%w: recursively abandoned module %q: %v",
					ErrUnhandledRapideException, caller.moduleID, err,
				)
			}
			structuralSourceModuleID = caller.moduleID
			continue
		}
		if len(handledAbandonmentFrontier) == 0 {
			if context.functionRuntime.propagations == nil ||
				!context.functionRuntime.propagations.handledParentTarget(
					activeRaised.event.ID, structuralSourceModuleID, caller.moduleID,
				) {
				return outcome, fmt.Errorf(
					"%w: recursively abandoned module %q has neither matching termination nor structural parent-handler recovery",
					ErrInvalidDeclarativeStatement, caller.moduleID,
				)
			}
			frontier, err := moduleHandlerRecoveryFrontier(
				caller.moduleID, activeRaised.event.ID, context,
			)
			if err != nil {
				return outcome, err
			}
			handledAbandonmentFrontier = frontier
		}
		if callerLifecycle.state != ModuleCompletedState || !callerLifecycle.initializing ||
			callerLifecycle.terminationEventID != "" {
			return outcome, fmt.Errorf(
				"%w: recursively abandoned module %q has invalid handled-abandonment lifecycle state %q",
				ErrUnhandledRapideException, caller.moduleID, callerLifecycle.state,
			)
		}
		if err := finalizeHandledAbandonedModuleInitializationByIdentity(
			caller.moduleID, caller.stateOwnerID, handledAbandonmentFrontier, context,
		); err != nil {
			return outcome, fmt.Errorf(
				"%w: recursively abandoned module %q: %v",
				ErrInvalidDeclarativeStatement, caller.moduleID, err,
			)
		}
	}
	return failedModuleInitializationOutcome{
		activeRaised: activeRaised, structuralSourceModuleID: structuralSourceModuleID,
		handledFrontier: canonicalEventIDs(handledAbandonmentFrontier),
	}, nil
}

// propagatedExceptionOccurrenceBySource recovers the exact replacement
// occurrence emitted by a module handler from the already materialized
// propagation record and poset event. No new event or declaration is inferred.
func propagatedExceptionOccurrenceBySource(
	sourceModuleID string,
	eventID gorapide.EventID,
	context processTerminationContext,
) (*raisedExceptionOccurrence, error) {
	runtime := context.functionRuntime
	if runtime == nil || runtime.propagations == nil || context.poset == nil ||
		sourceModuleID == "" || eventID == "" {
		return nil, fmt.Errorf("%w: replacement propagation runtime is incomplete", ErrUnhandledRapideException)
	}
	record, exists := runtime.propagations.recordsByKey[exceptionPropagationKey(eventID, sourceModuleID)]
	if !exists || record.Exception == "" || record.ExceptionDeclaration == "" ||
		record.ExceptionEventID != string(eventID) || record.SourceModuleID != sourceModuleID {
		return nil, fmt.Errorf(
			"%w: module %q replacement propagation %q is unavailable",
			ErrUnhandledRapideException, sourceModuleID, eventID,
		)
	}
	event, exists := context.poset.Get(eventID)
	if !exists || event == nil {
		return nil, fmt.Errorf(
			"%w: module %q replacement occurrence %q is absent from the poset",
			ErrUnhandledRapideException, sourceModuleID, eventID,
		)
	}
	return &raisedExceptionOccurrence{
		name: record.Exception, declaration: record.ExceptionDeclaration, event: event,
	}, nil
}

// moduleHandlerRecoveryFrontier returns the observable completion frontier of
// the exact successful module-handler activation. With no generated output the
// handled exception remains the only event frontier; state-only recovery is
// still represented by the canonical module-handler firing and state audit.
func moduleHandlerRecoveryFrontier(
	moduleID string,
	exceptionEventID gorapide.EventID,
	context processTerminationContext,
) ([]gorapide.EventID, error) {
	if moduleID == "" || exceptionEventID == "" || context.firings == nil || context.poset == nil {
		return nil, fmt.Errorf("%w: module-handler recovery frontier is incomplete", ErrUnhandledRapideException)
	}
	frontier := []gorapide.EventID{exceptionEventID}
	found := false
	for _, firing := range *context.firings {
		if firing.Transition != "module-handler" || firing.Target != moduleID {
			continue
		}
		matched := false
		for _, eventID := range firing.MatchedEvents {
			if eventID == string(exceptionEventID) {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		if found {
			return nil, fmt.Errorf(
				"%w: module %q has repeated handler recovery for exception %q",
				ErrUnhandledRapideException, moduleID, exceptionEventID,
			)
		}
		found = true
		for _, generated := range firing.Generated {
			if generated.EventID != "" {
				frontier = append(frontier, gorapide.EventID(generated.EventID))
			}
		}
	}
	if !found {
		return nil, fmt.Errorf(
			"%w: module %q handled exception %q without a recovery firing",
			ErrUnhandledRapideException, moduleID, exceptionEventID,
		)
	}
	return maximalKnownCausalFrontier(context.poset, canonicalEventIDs(frontier)), nil
}

// finalizeHandledAbandonedModuleInitializationByIdentity closes a fresh module
// whose structural child failure was handled by this module, but whose nested
// New still returned no value. Since this module did not propagate an
// exception, Executable LRM 9.9 keeps its ordinary final part eligible. Every
// outer fresh generator frame abandoned by the same missing value uses the same
// handler-completion frontier, so Go stack-unwind order adds no false poset edge.
func finalizeHandledAbandonedModuleInitializationByIdentity(
	moduleID, stateOwnerID string,
	frontier []gorapide.EventID,
	context processTerminationContext,
) error {
	runtime := context.functionRuntime
	if runtime == nil || runtime.lifecycle == nil || runtime.contexts == nil ||
		moduleID == "" || stateOwnerID == "" || len(frontier) == 0 ||
		context.statementSteps == nil || context.firings == nil || context.clocks == nil ||
		context.poset == nil || context.depths == nil || context.queue == nil ||
		context.seenItems == nil || context.moduleState == nil || context.stateSnapshots == nil {
		return fmt.Errorf("%w: handled initialization-abandonment runtime is incomplete", ErrUnhandledRapideException)
	}
	if uint64(len(*context.firings)) >= runtime.maxFirings {
		return fmt.Errorf("%w: max_firings=%d", ErrExecutionLimit, runtime.maxFirings)
	}
	if err := runtime.lifecycle.completeInitialization(moduleID); err != nil {
		return err
	}
	finalization := &statementExecution{
		clocks: context.clocks, owner: "initialization-abandonment:" + moduleID,
		budget: context.statementSteps,
	}
	finalized, err := releaseModuleName(
		context.modelDigest, "self-name:"+moduleID, frontier, runtime, finalization,
	)
	if err != nil {
		return err
	}
	if finalized != moduleID {
		return fmt.Errorf(
			"%w: handled abandoned module %q remained namable",
			ErrUnhandledRapideException, moduleID,
		)
	}
	generated := make([]GeneratedEventRecord, 0, len(finalization.generated))
	for _, output := range finalization.generated {
		if err := context.poset.AddEventWithCause(output.event, output.causes...); err != nil {
			return fmt.Errorf(
				"handled abandoned module %q finalization output %s: %w",
				moduleID, output.localID, err,
			)
		}
		depth := eventDepth(context.poset, output.event, context.depths)
		context.depths[output.event.ID] = depth
		if err := enqueueGeneratedObservationViews(
			output, depth, context.moduleState, context.stateSnapshots,
			context.queue, context.seenItems,
		); err != nil {
			return err
		}
		generated = append(generated, GeneratedEventRecord{
			OutputID: output.localID, EventID: string(output.event.ID), Exception: output.exception,
		})
	}
	*context.firings = append(*context.firings, FiringRecord{
		Sequence:   uint64(len(*context.firings) + 1),
		Transition: "initialization-abandonment-finalization",
		Target:     moduleID, Generated: generated,
	})
	return nil
}

// finalizeHandledAbandonedStaticModuleInitializationByIdentity applies the
// same ordinary-finalization rule to a statically declared generator result.
// Its architecture name and Self name were provisional until the generator
// returned, so the lifecycle transition loses both names atomically.
func finalizeHandledAbandonedStaticModuleInitializationByIdentity(
	moduleID, componentID string,
	frontier []gorapide.EventID,
	context processTerminationContext,
) error {
	runtime := context.functionRuntime
	if runtime == nil || runtime.lifecycle == nil || runtime.contexts == nil ||
		moduleID == "" || componentID == "" || len(frontier) == 0 ||
		context.statementSteps == nil || context.firings == nil || context.clocks == nil ||
		context.poset == nil || context.depths == nil || context.queue == nil ||
		context.seenItems == nil || context.moduleState == nil || context.stateSnapshots == nil {
		return fmt.Errorf("%w: handled static initialization-abandonment runtime is incomplete", ErrUnhandledRapideException)
	}
	if uint64(len(*context.firings)) >= runtime.maxFirings {
		return fmt.Errorf("%w: max_firings=%d", ErrExecutionLimit, runtime.maxFirings)
	}
	for _, process := range runtime.processes[componentID] {
		if process != nil && process.elaborated {
			return fmt.Errorf(
				"%w: handled abandoned static module %q already elaborated process %q",
				ErrInvalidDeclarativeStatement, moduleID, process.declaration.ID,
			)
		}
	}
	lifecycle := runtime.lifecycle.modules[moduleID]
	if lifecycle == nil || lifecycle.terminationEventID != "" ||
		(lifecycle.state != ModuleCompletedState && lifecycle.state != ModuleRunningState) {
		return fmt.Errorf(
			"%w: handled abandoned static module %q has invalid lifecycle state",
			ErrInvalidDeclarativeStatement, moduleID,
		)
	}
	if lifecycle.state == ModuleRunningState {
		if err := runtime.lifecycle.setState(moduleID, ModuleCompletedState); err != nil {
			return err
		}
	}
	causes, err := runtime.lifecycle.finalizeInitializationAbandonment(moduleID, frontier)
	if err != nil {
		return err
	}
	component := runtime.components[componentID]
	module := runtime.modules[componentID]
	if component == nil || module.Identity() != moduleID {
		return fmt.Errorf(
			"%w: handled abandoned static module %q has no component/module binding",
			ErrInvalidDeclarativeStatement, moduleID,
		)
	}
	connectionCount := len(runtime.connections)
	runtime.components[moduleID] = component
	runtime.modules[moduleID] = module
	runtime.state[moduleID] = runtime.state[componentID]
	defer func() {
		delete(runtime.components, moduleID)
		delete(runtime.modules, moduleID)
		delete(runtime.state, moduleID)
		delete(runtime.callables, moduleID)
		runtime.connections = runtime.connections[:connectionCount]
	}()
	if err := registerAllocatedModuleFunctions(componentID, moduleID, runtime); err != nil {
		return err
	}
	if err := registerAllocatedModuleActionConnections(componentID, moduleID, runtime); err != nil {
		return err
	}
	finalization := &statementExecution{
		clocks: context.clocks, owner: "static-initialization-abandonment:" + componentID,
		budget: context.statementSteps,
	}
	if err := materializeModuleFinalization(
		context.modelDigest, moduleID, causes, "component-name:"+componentID,
		runtime, finalization,
	); err != nil {
		return err
	}
	generated := make([]GeneratedEventRecord, 0, len(finalization.generated))
	for _, output := range finalization.generated {
		if err := context.poset.AddEventWithCause(output.event, output.causes...); err != nil {
			return fmt.Errorf(
				"handled abandoned static module %q finalization output %s: %w",
				moduleID, output.localID, err,
			)
		}
		depth := eventDepth(context.poset, output.event, context.depths)
		context.depths[output.event.ID] = depth
		if err := enqueueGeneratedObservationViews(
			output, depth, context.moduleState, context.stateSnapshots,
			context.queue, context.seenItems,
		); err != nil {
			return err
		}
		generated = append(generated, GeneratedEventRecord{
			OutputID: output.localID, EventID: string(output.event.ID), Exception: output.exception,
		})
	}
	*context.firings = append(*context.firings, FiringRecord{
		Sequence:   uint64(len(*context.firings) + 1),
		Transition: "initialization-abandonment-finalization",
		Target:     componentID, Generated: generated,
	})
	return nil
}

// finalizeArchitectureInitializationAbandonment closes the architecture
// generator that first failed to return and every lexically enclosing
// architecture generator. Descendant scopes close before their owner, while
// every architecture in the missing-result chain uses the same exact recovery
// or exceptional occurrence frontier. The caller separately preserves the
// phase distinction: declaration failure suppresses connection elaboration,
// while initial failure retains already-elaborated connection observation
// views.
func finalizeArchitectureInitializationAbandonment(
	model *deterministicModel,
	abandonment *architectureInitializationAbandonment,
	context processTerminationContext,
) error {
	runtime := context.functionRuntime
	if model == nil || abandonment == nil || runtime == nil || runtime.lifecycle == nil ||
		abandonment.ownerID == "" {
		return fmt.Errorf("%w: architecture initialization-abandonment runtime is incomplete", ErrUnhandledRapideException)
	}
	lineage, err := architectureInitializationAbandonmentLineage(model, abandonment.ownerID)
	if err != nil {
		return err
	}
	frontier := canonicalEventIDs(abandonment.handledFrontier)
	if len(frontier) == 0 {
		if abandonment.activeRaised == nil || abandonment.activeRaised.event == nil {
			return fmt.Errorf("%w: architecture exceptional abandonment has no occurrence", ErrUnhandledRapideException)
		}
		frontier = []gorapide.EventID{abandonment.activeRaised.event.ID}
	}
	for _, ownerID := range lineage {
		moduleID := runtime.modules[ownerID].Identity()
		if moduleID == "" {
			return fmt.Errorf(
				"%w: architecture %q has no generated module allocation",
				ErrUnhandledRapideException, ownerID,
			)
		}
		if err := finalizeArchitectureOwnedConstituentScopes(
			model, ownerID, frontier, context,
		); err != nil {
			return err
		}
		boundaryID := architectureBoundaryID(ownerID)
		if abandonment.activeRaised != nil {
			activeRaised := abandonment.activeRaised
			ownerLifecycle := runtime.lifecycle.modules[moduleID]
			if ownerLifecycle == nil || ownerLifecycle.state != ModuleTerminatedState {
				return fmt.Errorf(
					"%w: architecture %q has invalid exceptional-abandonment state",
					ErrUnhandledRapideException, ownerID,
				)
			}
			if ownerLifecycle.terminationEventID != activeRaised.event.ID {
				activeRaised, err = propagatedExceptionOccurrenceDeliveredToTarget(
					moduleID, ownerLifecycle.terminationEventID, context,
				)
				if err != nil {
					return err
				}
			}
			if err := finalizeTerminatedModuleInitializationByIdentity(
				moduleID, boundaryID, activeRaised, context,
			); err != nil {
				return err
			}
			continue
		}
		causes, err := runtime.lifecycle.finalizeInitializationAbandonment(moduleID, frontier)
		if err != nil {
			return err
		}
		if err := materializeArchitectureInitializationAbandonmentFinish(
			moduleID, boundaryID, causes, context,
		); err != nil {
			return err
		}
	}
	return nil
}

// architectureInitializationAbandonmentLineage returns the missing-result
// caller chain from the first failed architecture through the root. The model's
// parent relation is canonical data; no host traversal order selects the chain.
func architectureInitializationAbandonmentLineage(
	model *deterministicModel,
	ownerID string,
) ([]string, error) {
	if model == nil || ownerID == "" {
		return nil, fmt.Errorf("%w: architecture abandonment owner is incomplete", ErrInvalidDeclarativeStatement)
	}
	lineage := make([]string, 0, len(model.architectureInstanceIDs)+1)
	seen := make(map[string]bool, len(model.architectureInstanceIDs)+1)
	for current := ownerID; ; {
		if seen[current] {
			return nil, fmt.Errorf(
				"%w: architecture abandonment parent cycle reaches %q",
				ErrInvalidDeclarativeStatement, current,
			)
		}
		seen[current] = true
		lineage = append(lineage, current)
		if current == ArchitectureInterfaceID {
			return lineage, nil
		}
		declaration, exists := model.architectureInstances[current]
		if !exists || declaration.Parent == "" {
			return nil, fmt.Errorf(
				"%w: architecture abandonment owner %q has no parent declaration",
				ErrInvalidDeclarativeStatement, current,
			)
		}
		current = declaration.Parent
	}
}

// architectureAbandonmentPostScopeProcessComponents returns the exact execution
// components whose elaborated processes remain alive after architecture scope
// loss. Propagation-driven
// process termination is already complete by this point; a remaining Running
// lifecycle therefore denotes the independent lifetime required by Executable
// LRM 9.9. Every such lifecycle must map back to an exact execution component
// and at least one live elaborated process before the scheduler may continue it.
func architectureAbandonmentPostScopeProcessComponents(
	runtime *functionExecutionRuntime,
) ([]string, error) {
	if runtime == nil || runtime.lifecycle == nil || runtime.contexts == nil {
		return nil, fmt.Errorf("%w: architecture abandonment lifecycle is unavailable", ErrInvalidDeclarativeStatement)
	}
	running := make([]string, 0)
	for moduleID, lifecycle := range runtime.lifecycle.modules {
		if lifecycle != nil && lifecycle.state == ModuleRunningState {
			running = append(running, moduleID)
		}
	}
	running = canonicalStrings(running)
	for _, moduleID := range running {
		componentID := runtime.contexts.componentByModule[moduleID]
		if componentID == "" {
			return nil, fmt.Errorf(
				"%w: running architecture constituent %q has no execution component",
				ErrInvalidDeclarativeStatement, moduleID,
			)
		}
		processes := runtime.processes[componentID]
		live := false
		for _, process := range processes {
			if process != nil && process.elaborated && !process.terminated {
				live = true
				break
			}
		}
		if !live {
			return nil, fmt.Errorf(
				"%w: running architecture constituent %q has no live elaborated process",
				ErrInvalidDeclarativeProcess, moduleID,
			)
		}
	}
	components := make([]string, 0, len(running))
	for _, moduleID := range running {
		components = append(components, runtime.contexts.componentByModule[moduleID])
	}
	return canonicalStrings(components), nil
}

// architecturePostScopeComponentIsRunning is the scheduler guard after a
// failed architecture generator has lost all constituent names. Only modules
// whose language lifecycle remains Running may execute rules, processes, or
// module-local connections; completed, terminated, and finalized components
// cannot react merely because their queued observations are drained later.
func architecturePostScopeComponentIsRunning(
	runtime *functionExecutionRuntime,
	componentID string,
) bool {
	if runtime == nil || runtime.lifecycle == nil || componentID == "" {
		return false
	}
	moduleID := runtime.modules[componentID].Identity()
	lifecycle := runtime.lifecycle.modules[moduleID]
	return lifecycle != nil && lifecycle.state == ModuleRunningState
}

// architecturePostScopeModuleConnectionIsOpen distinguishes the two exact
// module-controller lifetimes permitted after an architecture generator has
// returned no value: a module connection owned by a still-Running RSD-0319
// component, or an allocation-rebound connection consuming actions generated
// by that component's final part. Architecture connections never enter either
// set, and a finalized module's rules or processes remain closed.
func architecturePostScopeModuleConnectionIsOpen(
	runtime *functionExecutionRuntime,
	sourceID string,
) bool {
	if runtime == nil || sourceID == "" {
		return false
	}
	if runtime.postScopeFinalConnectionSources[sourceID] {
		return true
	}
	return runtime.postScopeComponents[sourceID] &&
		architecturePostScopeComponentIsRunning(runtime, sourceID)
}

// finalizeArchitectureOwnedConstituentScopes closes every returned descendant
// architecture before releasing the names owned by ownerID. All name losses use
// the same frontier. Per-constituent finalization executions remain independent
// even though their canonical audit records are emitted in stable tree order.
func finalizeArchitectureOwnedConstituentScopes(
	model *deterministicModel,
	ownerID string,
	frontier []gorapide.EventID,
	context processTerminationContext,
) error {
	runtime := context.functionRuntime
	if model == nil || runtime == nil || runtime.lifecycle == nil || runtime.contexts == nil ||
		ownerID == "" || len(frontier) == 0 {
		return fmt.Errorf("%w: architecture scope finalization is incomplete", ErrUnhandledRapideException)
	}
	ownerModuleID := runtime.modules[ownerID].Identity()
	if ownerModuleID == "" {
		return fmt.Errorf("%w: architecture %q has no scope identity", ErrUnhandledRapideException, ownerID)
	}
	for _, childID := range model.architectureInstanceIDs {
		declaration := model.architectureInstances[childID]
		if declaration.Parent != ownerID {
			continue
		}
		childModuleID := runtime.modules[childID].Identity()
		childLifecycle := runtime.lifecycle.modules[childModuleID]
		if childLifecycle == nil {
			return fmt.Errorf(
				"%w: child architecture %q has no lifecycle",
				ErrUnhandledRapideException, childID,
			)
		}
		if childLifecycle.state == ModuleFinalizedState {
			continue
		}
		if childLifecycle.state == ModuleRunningState {
			return fmt.Errorf(
				"%w: child architecture %q unexpectedly remains running during scope closure",
				ErrInvalidDeclarativeStatement, childID,
			)
		}
		if err := finalizeArchitectureOwnedConstituentScopes(
			model, childID, frontier, context,
		); err != nil {
			return err
		}
	}
	constituentFinalizations, err := runtime.lifecycle.releaseOwnedConstituentNames(ownerModuleID, frontier)
	if err != nil {
		return err
	}
	for _, finalization := range constituentFinalizations {
		lifecycle := runtime.lifecycle.modules[finalization.moduleID]
		if lifecycle == nil {
			return fmt.Errorf(
				"%w: architecture constituent %q has no lifecycle",
				ErrInvalidDeclarativeStatement, finalization.moduleID,
			)
		}
		if lifecycle.kind == "architecture" {
			childID := runtime.contexts.componentByModule[finalization.moduleID]
			if childID == "" || childID == ArchitectureInterfaceID {
				return fmt.Errorf(
					"%w: architecture constituent %q has no nested boundary",
					ErrInvalidDeclarativeStatement, finalization.moduleID,
				)
			}
			if err := materializeArchitectureInitializationAbandonmentFinish(
				finalization.moduleID, architectureBoundaryID(childID), finalization.causes, context,
			); err != nil {
				return err
			}
			continue
		}
		componentID := runtime.moduleTemplates[finalization.moduleID]
		if componentID == "" {
			return fmt.Errorf(
				"%w: architecture constituent %q has no static module template",
				ErrInvalidDeclarativeStatement, finalization.moduleID,
			)
		}
		if err := materializeArchitectureConstituentFinalization(
			finalization.moduleID, componentID, finalization.causes, context,
		); err != nil {
			return err
		}
	}
	return nil
}

// materializeArchitectureConstituentFinalization executes the ordinary final
// part and implicit Finish of one completed or terminated static constituent
// made unnamable by architecture scope loss. Every constituent receives a
// fresh statement execution rooted at the same loss frontier, so deterministic
// iteration order cannot invent causal edges between sibling finalizations.
func materializeArchitectureConstituentFinalization(
	moduleID, componentID string,
	causes []gorapide.EventID,
	context processTerminationContext,
) error {
	runtime := context.functionRuntime
	if runtime == nil || runtime.lifecycle == nil || runtime.contexts == nil ||
		moduleID == "" || componentID == "" || len(causes) == 0 ||
		context.statementSteps == nil || context.firings == nil || context.clocks == nil ||
		context.poset == nil || context.depths == nil || context.queue == nil ||
		context.seenItems == nil || context.moduleState == nil || context.stateSnapshots == nil {
		return fmt.Errorf("%w: architecture constituent finalization runtime is incomplete", ErrUnhandledRapideException)
	}
	if uint64(len(*context.firings)) >= runtime.maxFirings {
		return fmt.Errorf("%w: max_firings=%d", ErrExecutionLimit, runtime.maxFirings)
	}
	component := runtime.components[componentID]
	module := runtime.modules[componentID]
	if component == nil || module.Identity() != moduleID || runtime.moduleTemplates[moduleID] != componentID {
		return fmt.Errorf(
			"%w: architecture constituent %q has no component/module binding",
			ErrInvalidDeclarativeStatement, moduleID,
		)
	}
	priorComponent, hadComponent := runtime.components[moduleID]
	priorModule, hadModule := runtime.modules[moduleID]
	priorState, hadState := runtime.state[moduleID]
	priorCallables, hadCallables := runtime.callables[moduleID]
	priorConnections := copyExecutionConnections(runtime.connections)
	retainFinalConnectionAliases := false
	var finalConnectionAliases []*Connection
	runtime.components[moduleID] = component
	runtime.modules[moduleID] = module
	runtime.state[moduleID] = runtime.state[componentID]
	defer func() {
		if hadComponent {
			runtime.components[moduleID] = priorComponent
		} else {
			delete(runtime.components, moduleID)
		}
		if hadModule {
			runtime.modules[moduleID] = priorModule
		} else {
			delete(runtime.modules, moduleID)
		}
		if hadState {
			runtime.state[moduleID] = priorState
		} else {
			delete(runtime.state, moduleID)
		}
		if hadCallables {
			runtime.callables[moduleID] = priorCallables
		} else {
			delete(runtime.callables, moduleID)
		}
		if retainFinalConnectionAliases {
			// The allocation identity is the actor/owner of final-part actions.
			// Retain only the interface descriptor and module-connection copies
			// needed to observe those queued occurrences. State, functions, and
			// module execution aliases remain temporary and are restored above.
			runtime.components[moduleID] = component
			runtime.connections = finalConnectionAliases
		} else {
			runtime.connections = priorConnections
		}
	}()
	if err := registerAllocatedModuleFunctions(componentID, moduleID, runtime); err != nil {
		return err
	}
	if err := registerAllocatedModuleActionConnections(componentID, moduleID, runtime); err != nil {
		return err
	}
	if runtime.architectureScopeClosed && len(runtime.connections) > len(priorConnections) {
		if runtime.postScopeFinalConnectionSources == nil {
			runtime.postScopeFinalConnectionSources = make(map[string]bool)
		}
		runtime.postScopeFinalConnectionSources[moduleID] = true
		retainFinalConnectionAliases = true
		finalConnectionAliases = copyExecutionConnections(runtime.connections)
	}
	finalization := &statementExecution{
		clocks: context.clocks, owner: "architecture-scope-finalization:" + componentID,
		budget: context.statementSteps,
	}
	if err := materializeModuleFinalization(
		context.modelDigest, moduleID, canonicalEventIDs(causes),
		"architecture-scope:"+componentID, runtime, finalization,
	); err != nil {
		return err
	}
	generated := make([]GeneratedEventRecord, 0, len(finalization.generated))
	for _, output := range finalization.generated {
		if err := context.poset.AddEventWithCause(output.event, output.causes...); err != nil {
			return fmt.Errorf(
				"architecture constituent %q finalization output %s: %w",
				moduleID, output.localID, err,
			)
		}
		depth := eventDepth(context.poset, output.event, context.depths)
		context.depths[output.event.ID] = depth
		if err := enqueueGeneratedObservationViews(
			output, depth, context.moduleState, context.stateSnapshots,
			context.queue, context.seenItems,
		); err != nil {
			return err
		}
		generated = append(generated, GeneratedEventRecord{
			OutputID: output.localID, EventID: string(output.event.ID), Exception: output.exception,
		})
	}
	*context.firings = append(*context.firings, FiringRecord{
		Sequence:   uint64(len(*context.firings) + 1),
		Transition: "architecture-constituent-finalization", Target: componentID,
		Generated:  generated,
		StateReads: finalization.reads, StateWrites: finalization.writes,
	})
	return nil
}

func materializeArchitectureInitializationAbandonmentFinish(
	moduleID, boundaryID string,
	causes []gorapide.EventID,
	context processTerminationContext,
) error {
	runtime := context.functionRuntime
	if runtime == nil || runtime.lifecycle == nil || runtime.contexts == nil ||
		moduleID == "" || boundaryID == "" || len(causes) == 0 ||
		context.firings == nil || context.clocks == nil || context.poset == nil ||
		context.depths == nil || context.queue == nil || context.seenItems == nil ||
		context.moduleState == nil || context.stateSnapshots == nil {
		return fmt.Errorf("%w: architecture abandonment Finish runtime is incomplete", ErrUnhandledRapideException)
	}
	if uint64(len(*context.firings)) >= runtime.maxFirings {
		return fmt.Errorf("%w: max_firings=%d", ErrExecutionLimit, runtime.maxFirings)
	}
	causes = canonicalEventIDs(causes)
	finish, err := gorapide.NewDeterministicEvent(gorapide.EventProvenance{
		Profile: CompatibilityProfile, Model: context.modelDigest, Instance: moduleID,
		Action: ModuleFinishAction, Occurrence: "module=" + moduleID + "|finish",
		Causes: causes, Timings: context.clocks.instantTimings(boundaryID),
	}, nil)
	if err != nil {
		return err
	}
	if err := context.poset.AddEventWithCause(finish, causes...); err != nil {
		return err
	}
	snapshot := cloneStateCells(context.moduleState[boundaryID])
	output := generatedRuleOutput{
		localID:       "finish@architecture-abandonment:" + boundaryID,
		event:         finish,
		causes:        causes,
		stateSnapshot: snapshot,
		observationSnapshots: map[string]map[string]*stateCell{
			moduleID: snapshot,
		},
	}
	depth := eventDepth(context.poset, finish, context.depths)
	context.depths[finish.ID] = depth
	if err := enqueueGeneratedObservationViews(
		output, depth, context.moduleState, context.stateSnapshots,
		context.queue, context.seenItems,
	); err != nil {
		return err
	}
	if err := runtime.lifecycle.setFinish(moduleID, finish.ID); err != nil {
		return err
	}
	if err := runtime.contexts.closeFinalizedModule(moduleID, causes); err != nil {
		return err
	}
	*context.firings = append(*context.firings, FiringRecord{
		Sequence:   uint64(len(*context.firings) + 1),
		Transition: "architecture-initialization-abandonment-finalization",
		Target:     boundaryID,
		Generated: []GeneratedEventRecord{{
			OutputID: output.localID, EventID: string(finish.ID),
		}},
	})
	return nil
}

// finalizeTerminatedModuleInitializationByIdentity materializes the exceptional
// creation finish for a module already terminated by propagation of the leaf
// initializer occurrence. It deliberately does not propagate again: the leaf
// broadcast closes the complete structural chain before any sibling Finish is
// inserted, so host unwind order adds no false causal edges.
func finalizeTerminatedModuleInitializationByIdentity(
	moduleID, stateOwnerID string,
	raised *raisedExceptionOccurrence,
	context processTerminationContext,
) error {
	runtime := context.functionRuntime
	if runtime == nil || runtime.lifecycle == nil || runtime.contexts == nil ||
		raised == nil || raised.event == nil || moduleID == "" || stateOwnerID == "" {
		return fmt.Errorf("%w: initialization exception runtime is incomplete", ErrUnhandledRapideException)
	}
	causes, err := runtime.lifecycle.finalizeInitializationFailure(moduleID, raised.event.ID)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnhandledRapideException, err)
	}
	if uint64(len(*context.firings)) >= runtime.maxFirings {
		return fmt.Errorf("%w: max_firings=%d", ErrExecutionLimit, runtime.maxFirings)
	}
	finish, err := gorapide.NewDeterministicEvent(gorapide.EventProvenance{
		Profile: CompatibilityProfile, Model: context.modelDigest, Instance: moduleID,
		Action: ModuleFinishAction, Occurrence: "module=" + moduleID + "|finish",
		Causes: causes, Timings: context.clocks.instantTimings(stateOwnerID),
	}, nil)
	if err != nil {
		return err
	}
	if err := context.poset.AddEventWithCause(finish, causes...); err != nil {
		return err
	}
	snapshot := cloneStateCells(context.moduleState[stateOwnerID])
	output := generatedRuleOutput{
		localID: "finish@initial:" + stateOwnerID, event: finish, causes: causes,
		stateSnapshot: snapshot,
		observationSnapshots: map[string]map[string]*stateCell{
			moduleID: snapshot,
		},
	}
	depth := eventDepth(context.poset, finish, context.depths)
	context.depths[finish.ID] = depth
	if err := enqueueGeneratedObservationViews(
		output, depth, context.moduleState, context.stateSnapshots, context.queue, context.seenItems,
	); err != nil {
		return err
	}
	if err := runtime.lifecycle.setFinish(moduleID, finish.ID); err != nil {
		return err
	}
	if err := runtime.contexts.closeFinalizedModule(moduleID, causes); err != nil {
		return err
	}
	*context.firings = append(*context.firings, FiringRecord{
		Sequence: uint64(len(*context.firings) + 1), Transition: "initialization-finalization",
		Target: stateOwnerID,
		Generated: []GeneratedEventRecord{{
			OutputID: output.localID, EventID: string(finish.ID),
		}},
	})
	return nil
}

func validateModuleInitialStatementSubset(
	component *Component,
	statements []Statement,
	callables map[string]map[string]*FunctionImplementation,
) error {
	if component == nil {
		return fmt.Errorf("%w: component is nil", ErrInvalidModuleInitial)
	}
	return validateModuleInitialStatementList(
		component, statements, callables, make(map[string]bool),
	)
}

func validateModuleInitialStatementList(
	component *Component,
	statements []Statement,
	callables map[string]map[string]*FunctionImplementation,
	visitedFunctions map[string]bool,
) error {
	componentID := component.ID
	for _, statement := range statements {
		switch statement.kind {
		case FunctionCallStatement:
			if err := validateModuleInitialFunctionCall(
				component, statement.functionCall, callables, visitedFunctions,
			); err != nil {
				return err
			}
		case DoBlockStatementKind:
			if err := validateModuleInitialStatementList(
				component, statement.handledBody, callables, visitedFunctions,
			); err != nil {
				return err
			}
		case HandlerBlockStatementKind:
			for _, choice := range statement.handler.Choices {
				if choice.Action == "" {
					continue
				}
				action, exists := handlerActionDeclaration(component, choice.Action)
				if !exists || (action.Kind != OutAction && action.Kind != PrivateAction) {
					return fmt.Errorf("%w: component %q initial external in-action interrupt choice %q requires asynchronous startup semantics",
						ErrInvalidModuleInitial, componentID, choice.Action)
				}
			}
			if err := validateModuleInitialStatementList(
				component, statement.handledBody, callables, visitedFunctions,
			); err != nil {
				return err
			}
			for _, choice := range statement.handler.Choices {
				if err := validateModuleInitialStatementList(
					component, choice.Statements, callables, visitedFunctions,
				); err != nil {
					return err
				}
			}
			if err := validateModuleInitialStatementList(
				component, statement.handler.Else, callables, visitedFunctions,
			); err != nil {
				return err
			}
		case IfStatementKind:
			if err := validateModuleInitialStatementList(
				component, statement.thenBranch, callables, visitedFunctions,
			); err != nil {
				return err
			}
			if err := validateModuleInitialStatementList(
				component, statement.elseBranch, callables, visitedFunctions,
			); err != nil {
				return err
			}
		case LoopStatementKind, ForStatementKind:
			if err := validateModuleInitialStatementList(
				component, statement.loopBody, callables, visitedFunctions,
			); err != nil {
				return err
			}
		case GeneralForStatementKind:
			for _, expression := range []ExecutableObjectExpression{
				statement.forInitial, statement.forTest, statement.forNext,
			} {
				if expression.kind != ObjectFunctionExpression {
					continue
				}
				if err := validateModuleInitialFunctionCall(
					component, expression.call, callables, visitedFunctions,
				); err != nil {
					return err
				}
			}
			if err := validateModuleInitialStatementList(
				component, statement.loopBody, callables, visitedFunctions,
			); err != nil {
				return err
			}
		case CaseStatementKind:
			for _, alternative := range statement.caseAlts {
				if err := validateModuleInitialStatementList(
					component, alternative.body, callables, visitedFunctions,
				); err != nil {
					return err
				}
			}
			if err := validateModuleInitialStatementList(
				component, statement.caseDefault, callables, visitedFunctions,
			); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateModuleInitialFunctionCall(
	component *Component,
	call FunctionCall,
	callables map[string]map[string]*FunctionImplementation,
	visitedFunctions map[string]bool,
) error {
	componentID := component.ID
	implementation := callables[componentID][call.functionKey]
	if implementation == nil {
		return fmt.Errorf(
			"%w: component %q initial call %q resolved to missing function signature %q",
			ErrInvalidModuleInitial, componentID, call.ID, call.functionKey,
		)
	}
	targetComponentID := implementation.targetComponent
	if targetComponentID == "" {
		targetComponentID = componentID
	}
	if targetComponentID != componentID {
		return fmt.Errorf(
			"%w: component %q initial function %q routes to component %q; cross-component calls require source-grounded module creation ordering",
			ErrInvalidModuleInitial, componentID, implementation.Name, targetComponentID,
		)
	}
	visitKey := componentID + "\x00" + call.functionKey
	if visitedFunctions[visitKey] {
		return nil
	}
	visitedFunctions[visitKey] = true
	return validateModuleInitialStatementList(
		component, implementation.Statements, callables, visitedFunctions,
	)
}

package arch

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/pattern"
)

// ProcessExecutionRecord is the final auditable control state and causal
// frontier of one declarative process.
type ProcessExecutionRecord struct {
	ComponentID            string   `json:"component_id"`
	ProcessID              string   `json:"process_id"`
	State                  string   `json:"state"`
	Terminated             bool     `json:"terminated"`
	Completion             string   `json:"completion,omitempty"`
	ExceptionEventID       string   `json:"exception_event_id,omitempty"`
	CanceledSchedules      []string `json:"canceled_schedules,omitempty"`
	CanceledSuspensions    []string `json:"canceled_suspensions,omitempty"`
	CanceledSwitches       []string `json:"canceled_switches,omitempty"`
	Frontier               []string `json:"frontier"`
	StateOperationFrontier []string `json:"state_operation_frontier"`
}

type processRuntime struct {
	componentID         string
	declaration         *DeclarativeProcess
	states              map[string]ProcessState
	state               string
	elaborated          bool
	terminated          bool
	completion          string
	exceptionEventID    gorapide.EventID
	canceledSchedules   []string
	canceledSuspensions []string
	canceledSwitches    []string
	frontier            []gorapide.EventID
	activation          uint64
	causalOwner         string
	continuation        *processBodyContinuation
	suspension          *timedProcessSuspension
	switchYield         *processSwitchYield
	delayWindows        []processDelayWindow
	pendingOperations   []stateOperationReference
}

func initializeProcessRuntimes(model *deterministicModel) (map[string][]*processRuntime, error) {
	result := make(map[string][]*processRuntime)
	for _, componentID := range model.componentIDs {
		declarations := model.processes[componentID]
		for _, declaration := range declarations {
			result[componentID] = append(result[componentID], newProcessRuntime(
				componentID, declaration, nil, nil, false,
			))
		}
	}
	return result, nil
}

func newProcessRuntime(
	componentID string,
	declaration *DeclarativeProcess,
	frontier []gorapide.EventID,
	operations []stateOperationReference,
	elaborated bool,
) *processRuntime {
	states := make(map[string]ProcessState)
	if declaration != nil {
		states = make(map[string]ProcessState, len(declaration.States))
		for _, state := range declaration.States {
			states[state.ID] = copyProcessState(state)
		}
	}
	processID := ""
	initial := ""
	if declaration != nil {
		processID = declaration.ID
		initial = declaration.Initial
	}
	return &processRuntime{
		componentID: componentID, declaration: declaration,
		states: states, state: initial, elaborated: elaborated, activation: 1,
		causalOwner:       "process:" + componentID + "\x00" + processID,
		frontier:          canonicalEventIDs(frontier),
		pendingOperations: canonicalStateOperationReferences(operations),
	}
}

func deterministicProcessComponentIDs(runtimes map[string][]*processRuntime) []string {
	result := make([]string, 0, len(runtimes))
	for componentID, componentRuntimes := range runtimes {
		if componentID != "" && len(componentRuntimes) != 0 {
			result = append(result, componentID)
		}
	}
	sort.Strings(result)
	return result
}

type awaitCandidate struct {
	alternative AwaitAlternative
	match       pattern.MatchResult
	canonical   pattern.CanonicalMatch
	key         string
	guardReads  []StateReadRecord
	guardCauses []gorapide.EventID
	isElse      bool
}

type readyProcess struct {
	runtime    *processRuntime
	state      ProcessState
	candidates []awaitCandidate
	interrupts []processInterruptCandidate
	resume     bool
}

type processInterruptCandidate struct {
	event      *gorapide.Event
	invocation interruptHandlerInvocation
	key        string
}

func hasActiveProcessInterruptHandler(runtimes map[string][]*processRuntime) bool {
	for _, componentRuntimes := range runtimes {
		for _, runtime := range componentRuntimes {
			if runtime == nil || runtime.continuation == nil {
				continue
			}
			if len(activeProcessHandlerControllers(runtime.continuation)) != 0 {
				return true
			}
		}
	}
	return false
}

func eligibleProcessInterruptCandidates(
	runtime *processRuntime,
	observed gorapide.EventSet,
	poset pattern.PosetReader,
) ([]processInterruptCandidate, error) {
	if runtime == nil || runtime.continuation == nil {
		return nil, nil
	}
	handlers := activeProcessHandlerControllers(runtime.continuation)
	if len(handlers) == 0 {
		return nil, nil
	}
	candidates := make([]processInterruptCandidate, 0)
	seen := make(map[string]bool)
	for _, event := range observed {
		if event == nil {
			continue
		}
		if runtime.continuation.handledInterruptEvents[event.ID] {
			continue
		}
		for _, controller := range handlers {
			if controller.activationEvents[event.ID] {
				continue
			}
			invocation, err := selectInterruptHandler(
				[]activeInterruptHandler{{
					id: controller.id, owner: controller.owner, processOwned: true,
					handler:    controller.handler,
					outerMatch: cloneProcessMatch(controller.outerMatch), outerSet: true,
				}},
				event, controller.outerMatch,
			)
			if err != nil {
				return nil, err
			}
			if invocation == nil {
				continue
			}
			canonical, err := pattern.CanonicalizeMatch(invocation.match)
			if err != nil {
				return nil, err
			}
			encoded, err := json.Marshal(canonical)
			if err != nil {
				return nil, err
			}
			key := controller.id + "@" + digestBytes(encoded) + "@" + executionItemKey(event)
			if !seen[key] {
				seen[key] = true
				candidates = append(candidates, processInterruptCandidate{
					event: event.Snapshot(), invocation: *invocation, key: key,
				})
			}
			// For this exact event the most recently activated matching handler
			// wins; older active handlers are not alternative targets.
			break
		}
	}
	if len(candidates) == 0 {
		return nil, nil
	}
	earliest := make([]processInterruptCandidate, 0, len(candidates))
	for index, candidate := range candidates {
		dominated := false
		for otherIndex, other := range candidates {
			if index == otherIndex {
				continue
			}
			earlier, err := pattern.IsEarlierMatch(
				gorapide.EventSet{other.event}, gorapide.EventSet{candidate.event}, poset,
			)
			if err != nil {
				return nil, err
			}
			if earlier {
				dominated = true
				break
			}
		}
		if !dominated {
			earliest = append(earliest, candidate)
		}
	}
	sort.Slice(earliest, func(left, right int) bool {
		return earliest[left].key < earliest[right].key
	})
	return earliest, nil
}

func truncateInterruptedDelayWindow(
	runtime *processRuntime,
	suspension *timedProcessSuspension,
	interrupt *gorapide.Event,
	clocks *deterministicClockKernel,
) {
	if runtime == nil || suspension == nil || suspension.kind != DelayTimingClause || interrupt == nil {
		return
	}
	finish := suspension.start
	if clocks != nil {
		if clock := clocks.clocks[suspension.clock]; clock != nil {
			finish = clock.now
		}
	}
	if timing, related := interrupt.Timing(suspension.clock); related {
		finish = timing.Finish
	}
	if finish < suspension.start {
		finish = suspension.start
	}
	if finish > suspension.deadline {
		finish = suspension.deadline
	}
	for index := len(runtime.delayWindows) - 1; index >= 0; index-- {
		window := &runtime.delayWindows[index]
		if window.clock == suspension.clock && window.start == suspension.start &&
			window.finish == suspension.deadline {
			window.finish = finish
			return
		}
	}
}

func readyProcessContinuations(runtimes map[string][]*processRuntime) []*processRuntime {
	ready := make([]*processRuntime, 0)
	for _, componentID := range deterministicProcessComponentIDs(runtimes) {
		for _, runtime := range runtimes[componentID] {
			if runtime == nil || !runtime.elaborated || runtime.terminated || runtime.continuation == nil {
				continue
			}
			if (runtime.suspension == nil || !runtime.suspension.ready) && runtime.switchYield == nil {
				continue
			}
			ready = append(ready, runtime)
		}
	}
	return ready
}

func processResumeChoiceKey(runtime *processRuntime) string {
	prefix := processChoiceKey(runtime.componentID, runtime.declaration.ID, runtime.continuation.state.ID)
	if runtime.suspension != nil && runtime.suspension.ready {
		return prefix + "/suspension/" + runtime.suspension.id
	}
	if runtime.switchYield != nil {
		return prefix + "/switch/" + runtime.switchYield.id
	}
	return prefix + "/unready"
}

func resumeOneProcessContinuation(
	ready []*processRuntime,
	model *deterministicModel,
	choices *choiceResolver,
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
	firings *[]FiringRecord,
) error {
	if len(ready) == 0 {
		return fmt.Errorf("%w: semantic scheduler selected an empty continuation set", ErrInvalidDeclarativeProcess)
	}
	options := make([]string, len(ready))
	byOption := make(map[string]*processRuntime, len(ready))
	for index, runtime := range ready {
		option := processResumeChoiceKey(runtime)
		options[index] = option
		byOption[option] = runtime
	}
	selected, err := choices.resolve("process-resume-schedule", options)
	if err != nil {
		return err
	}
	runtime := byOption[selected]
	if runtime == nil {
		return fmt.Errorf("%w: semantic scheduler lost selected continuation %q", ErrInvalidDeclarativeProcess, selected)
	}
	_, err = executeReadyProcessContinuation(
		runtime, model.digest, functionRuntime,
		moduleState, statementSteps, clocks, frontiers, poset, depths, queue,
		seenItems, stateSnapshots, firings,
	)
	return err
}

func executeReadyProcessContinuation(
	runtime *processRuntime,
	modelDigest string,
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
	firings *[]FiringRecord,
) (bool, error) {
	completed, generated, err := runProcessBodyContinuation(
		runtime, modelDigest, functionRuntime,
		statementSteps, clocks, frontiers,
		poset, depths, queue, seenItems, moduleState, stateSnapshots, firings,
	)
	if err != nil {
		return false, fmt.Errorf("deterministic process %s.%s continuation: %w",
			runtime.componentID, runtime.declaration.ID, err)
	}
	if completed {
		completeProcessBodyContinuation(runtime, frontiers)
		if err := finalizeCompletedDynamicModuleProcesses(runtime, processTerminationContext{
			modelDigest: modelDigest, functionRuntime: functionRuntime,
			frontiers: frontiers, clocks: clocks, poset: poset, depths: depths,
			queue: queue, seenItems: seenItems, moduleState: moduleState,
			stateSnapshots: stateSnapshots, statementSteps: statementSteps, firings: firings,
		}); err != nil {
			return false, err
		}
	}
	return generated, nil
}

func executeObservedProcessInterrupt(
	runtime *processRuntime,
	candidate processInterruptCandidate,
	modelDigest string,
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
	firings *[]FiringRecord,
) (bool, error) {
	if runtime == nil || runtime.continuation == nil || candidate.event == nil {
		return false, fmt.Errorf("%w: observed process interrupt is incomplete", ErrInvalidExceptionHandler)
	}
	continuation := runtime.continuation
	if runtime.suspension != nil {
		suspension := runtime.suspension
		truncateInterruptedDelayWindow(runtime, suspension, candidate.event, clocks)
		runtime.canceledSuspensions = append(
			runtime.canceledSuspensions, clocks.cancelProcessSuspension(runtime)...,
		)
		runtime.suspension = nil
	}
	if runtime.switchYield != nil {
		runtime.canceledSwitches = append(runtime.canceledSwitches, runtime.switchYield.id)
		runtime.switchYield = nil
	}
	invocation := candidate.invocation
	continuation.execution.pendingInterrupt = &invocation
	continuation.execution.control = canonicalEventIDs(append(
		continuation.execution.control, candidate.event.ID,
	))
	if err := applyProcessContinuationControl(
		continuation, statementHandleInterrupt, candidate.invocation.targetID,
		continuation.componentID, modelDigest, functionRuntime,
	); err != nil {
		return false, err
	}
	completed, generated, err := runProcessBodyContinuation(
		runtime, modelDigest, functionRuntime, statementSteps, clocks, frontiers,
		poset, depths, queue, seenItems, moduleState, stateSnapshots, firings,
	)
	if err != nil {
		return false, err
	}
	if completed {
		completeProcessBodyContinuation(runtime, frontiers)
		if err := finalizeCompletedDynamicModuleProcesses(runtime, processTerminationContext{
			modelDigest: modelDigest, functionRuntime: functionRuntime,
			frontiers: frontiers, clocks: clocks, poset: poset, depths: depths,
			queue: queue, seenItems: seenItems, moduleState: moduleState,
			stateSnapshots: stateSnapshots, statementSteps: statementSteps, firings: firings,
		}); err != nil {
			return false, err
		}
	}
	return generated, nil
}

func fireDeclarativeProcesses(
	componentID string,
	model *deterministicModel,
	runtimes map[string][]*processRuntime,
	poset *gorapide.Poset,
	observed map[string]gorapide.EventSet,
	observationRanks map[string]uint64,
	consumption *RuleConsumption,
	depths map[gorapide.EventID]uint64,
	queue *executionQueue,
	seenItems map[string]bool,
	choices *choiceResolver,
	moduleState moduleStateRuntime,
	functionRuntime *functionExecutionRuntime,
	stateSnapshots stateSnapshotRegistry,
	statementSteps *statementBudget,
	clocks *deterministicClockKernel,
	frontiers *causalFrontierRegistry,
	maxFirings uint64,
	firings *[]FiringRecord,
) error {
	componentRuntimes := runtimes[componentID]
	if len(componentRuntimes) == 0 {
		return nil
	}
	component := functionRuntime.components[componentID]
	if component == nil {
		return fmt.Errorf("%w: process component %q is unavailable", ErrInvalidDeclarativeProcess, componentID)
	}
	for {
		ready := make([]readyProcess, 0, len(componentRuntimes))
		for _, runtime := range componentRuntimes {
			if !runtime.elaborated || runtime.terminated {
				continue
			}
			if runtime.continuation != nil {
				interrupts, err := eligibleProcessInterruptCandidates(
					runtime, observed[componentID], poset,
				)
				if err != nil {
					return err
				}
				if len(interrupts) != 0 {
					ready = append(ready, readyProcess{
						runtime: runtime, state: runtime.continuation.state, interrupts: interrupts,
					})
				} else if ((runtime.suspension != nil && runtime.suspension.ready) || runtime.switchYield != nil) &&
					!(len(activeProcessHandlerControllers(runtime.continuation)) != 0 &&
						queue != nil && queue.Len() != 0) {
					ready = append(ready, readyProcess{
						runtime: runtime, state: runtime.continuation.state, resume: true,
					})
				}
				continue
			}
			state, ok := runtime.states[runtime.state]
			if !ok {
				return fmt.Errorf("%w: process %s.%s is in missing state %q", ErrInvalidDeclarativeProcess, componentID, runtime.declaration.ID, runtime.state)
			}
			scope := processConsumptionScope(componentID, runtime.declaration.ID)
			candidates, err := eligibleAwaitCandidates(
				componentID, runtime.declaration.ID, state, poset,
				filterProcessDelayWindows(runtime, observed[componentID]),
				observationRanks, consumption, scope, moduleState[componentID], stateSnapshots,
				runtime.pendingOperations, functionRuntime,
			)
			if err != nil {
				return err
			}
			if len(candidates) == 0 && state.Else != nil {
				candidate, err := awaitElseCandidate(*state.Else)
				if err != nil {
					return fmt.Errorf("deterministic process %s.%s state %s else alternative %s: %w",
						componentID, runtime.declaration.ID, state.ID, state.Else.ID, err)
				}
				candidates = []awaitCandidate{candidate}
			}
			if len(candidates) != 0 {
				ready = append(ready, readyProcess{runtime: runtime, state: state, candidates: candidates})
			}
		}
		if len(ready) == 0 {
			return nil
		}

		processOptions := make([]string, len(ready))
		byProcessOption := make(map[string]readyProcess, len(ready))
		for i, candidate := range ready {
			option := processChoiceKey(componentID, candidate.runtime.declaration.ID, candidate.state.ID)
			processOptions[i] = option
			byProcessOption[option] = candidate
		}
		mode := "single"
		if len(componentRuntimes) > 1 {
			templateID := functionRuntime.templateComponentID(componentID)
			mode = model.processModes[templateID].String()
		}
		selectedProcess, err := choices.resolve("process-schedule:"+mode+":"+componentID, processOptions)
		if err != nil {
			return err
		}
		selectedReady := byProcessOption[selectedProcess]
		runtime := selectedReady.runtime
		state := selectedReady.state
		candidates := selectedReady.candidates
		if len(selectedReady.interrupts) != 0 {
			interruptOptions := make([]string, len(selectedReady.interrupts))
			byInterruptOption := make(map[string]processInterruptCandidate, len(selectedReady.interrupts))
			for index, interrupt := range selectedReady.interrupts {
				interruptOptions[index] = interrupt.key
				byInterruptOption[interrupt.key] = interrupt
			}
			selectedInterrupt, err := choices.resolve(
				"process-interrupt:"+componentID+"/"+runtime.declaration.ID,
				interruptOptions,
			)
			if err != nil {
				return err
			}
			interrupt, exists := byInterruptOption[selectedInterrupt]
			if !exists {
				return fmt.Errorf(
					"%w: process %s.%s lost selected interrupt %q",
					ErrInvalidExceptionHandler, componentID, runtime.declaration.ID, selectedInterrupt,
				)
			}
			generated, err := executeObservedProcessInterrupt(
				runtime, interrupt, model.digest, functionRuntime, moduleState,
				statementSteps, clocks, frontiers, poset, depths, queue, seenItems,
				stateSnapshots, firings,
			)
			if err != nil {
				return err
			}
			if generated {
				return nil
			}
			continue
		}
		if selectedReady.resume {
			generated, err := executeReadyProcessContinuation(
				runtime, model.digest, functionRuntime, moduleState,
				statementSteps, clocks, frontiers, poset, depths, queue, seenItems,
				stateSnapshots, firings,
			)
			if err != nil {
				return err
			}
			if generated {
				return nil
			}
			continue
		}

		options := make([]string, len(candidates))
		byOption := make(map[string]awaitCandidate, len(candidates))
		for i, candidate := range candidates {
			option := awaitChoiceKey(componentID, runtime.declaration.ID, state.ID, candidate)
			options[i] = option
			byOption[option] = candidate
		}
		selected, err := choices.resolve("await:"+componentID+"/"+runtime.declaration.ID+"/"+state.ID, options)
		if err != nil {
			return err
		}
		candidate := byOption[selected]
		if uint64(len(*firings)) >= maxFirings {
			return fmt.Errorf("%w: max_firings=%d", ErrExecutionLimit, maxFirings)
		}

		bodyID := "process/" + runtime.declaration.ID + "/state/" + state.ID + "/alternative/" + candidate.alternative.ID
		if len(candidate.canonical.Events) == 0 {
			bodyID += "/activation/" + strconv.FormatUint(runtime.activation, 10)
		}
		bodyRule := &DeclarativeRule{
			ID: bodyID, Trigger: candidate.alternative.Trigger,
			Process: RulePipeProcess, Body: candidate.alternative.Body,
			allowProcessDoControl: processStateIsSourceWhen(state), processDoName: state.doName,
		}
		previousFrontier := processFrontierForMatch(
			poset, frontiers.get(runtime.causalOwner), candidate.match.Events,
		)
		if err := validateProcessFunctionSuspensionContexts(
			componentID, bodyRule.Body.Statements, functionRuntime,
		); err != nil {
			return fmt.Errorf(
				"deterministic process %s.%s state %s alternative %s: %w",
				componentID, runtime.declaration.ID, state.ID, candidate.alternative.ID, err,
			)
		}
		if ruleBodyMaySuspendThroughFunctions(componentID, bodyRule.Body, functionRuntime) ||
			processBodyNeedsInterruptConnectionContinuation(
				componentID, bodyRule.Body.Statements, functionRuntime,
			) {
			scope := processConsumptionScope(componentID, runtime.declaration.ID)
			if !candidate.isElse {
				if err := consumption.Consume(scope, candidate.match.Events); err != nil {
					return fmt.Errorf("deterministic process %s.%s: %w", componentID, runtime.declaration.ID, err)
				}
			}
			*firings = append(*firings, FiringRecord{
				Sequence: uint64(len(*firings) + 1), Transition: "process",
				ProcessID: runtime.declaration.ID, ProcessState: state.ID,
				AlternativeID: candidate.alternative.ID,
				MatchedEvents: append([]string(nil), candidate.canonical.Events...),
				Bindings:      append([]pattern.CanonicalBinding(nil), candidate.canonical.Bindings...),
				Target:        componentID,
			})
			continuation, err := beginProcessBodyContinuation(
				runtime, state, candidate, bodyRule, previousFrontier,
				statementSteps, clocks, functionRuntime, moduleState[componentID], len(*firings)-1,
			)
			if err != nil {
				return fmt.Errorf("deterministic process %s.%s state %s alternative %s: %w",
					componentID, runtime.declaration.ID, state.ID, candidate.alternative.ID, err)
			}
			runtime.continuation = continuation
			completed, generated, err := runProcessBodyContinuation(
				runtime, model.digest, functionRuntime,
				statementSteps, clocks, frontiers,
				poset, depths, queue, seenItems, moduleState, stateSnapshots, firings,
			)
			if err != nil {
				return fmt.Errorf("deterministic process %s.%s state %s alternative %s: %w",
					componentID, runtime.declaration.ID, state.ID, candidate.alternative.ID, err)
			}
			if completed {
				completeProcessBodyContinuation(runtime, frontiers)
				if err := finalizeCompletedDynamicModuleProcesses(runtime, processTerminationContext{
					modelDigest: model.digest, functionRuntime: functionRuntime,
					frontiers: frontiers, clocks: clocks, poset: poset, depths: depths,
					queue: queue, seenItems: seenItems, moduleState: moduleState,
					stateSnapshots: stateSnapshots, statementSteps: statementSteps, firings: firings,
				}); err != nil {
					return err
				}
			}
			if generated {
				return nil
			}
			continue
		}
		bodyResult, err := buildDeclarativeRuleBody(
			componentID, component, bodyRule, candidate.match, candidate.canonical,
			model.digest, previousFrontier, moduleState[componentID], functionRuntime,
			candidate.guardCauses, candidate.guardReads, statementSteps, clocks, runtime.causalOwner,
			runtime.pendingOperations,
		)
		if err != nil {
			return fmt.Errorf("deterministic process %s.%s state %s alternative %s: %w",
				componentID, runtime.declaration.ID, state.ID, candidate.alternative.ID, err)
		}
		scope := processConsumptionScope(componentID, runtime.declaration.ID)
		if !candidate.isElse {
			if err := consumption.Consume(scope, candidate.match.Events); err != nil {
				return fmt.Errorf("deterministic process %s.%s: %w", componentID, runtime.declaration.ID, err)
			}
		}
		for _, output := range bodyResult.generated {
			if err := poset.AddEventWithCause(output.event, output.causes...); err != nil {
				return fmt.Errorf("deterministic process %s.%s output %s: %w", componentID, runtime.declaration.ID, output.localID, err)
			}
		}
		if len(bodyResult.frontier) > 0 {
			frontiers.set(runtime.causalOwner, bodyResult.frontier)
		} else {
			// A process that reacts without generating an event still carries that
			// trigger into its later causal history. Otherwise a later generated
			// event could incorrectly appear independent of an earlier activation.
			nextFrontier := previousFrontier
			for _, eventID := range candidate.canonical.Events {
				nextFrontier = append(nextFrontier, gorapide.EventID(eventID))
			}
			frontiers.set(runtime.causalOwner, nextFrontier)
		}
		runtime.frontier = frontiers.get(runtime.causalOwner)
		runtime.pendingOperations = bodyResult.stateOperationFrontier
		clocks.addScheduled(bodyResult.scheduled)
		generatedRecord := make([]GeneratedEventRecord, 0, len(bodyResult.generated))
		for _, output := range bodyResult.generated {
			depth := eventDepth(poset, output.event, depths)
			depths[output.event.ID] = depth
			if err := enqueueGeneratedObservationViews(
				output, depth, moduleState, stateSnapshots, queue, seenItems,
			); err != nil {
				return err
			}
			generatedRecord = append(generatedRecord, GeneratedEventRecord{
				OutputID: output.localID, EventID: string(output.event.ID), Exception: output.exception,
			})
		}
		*firings = append(*firings, FiringRecord{
			Sequence: uint64(len(*firings) + 1), Transition: "process",
			ProcessID: runtime.declaration.ID, ProcessState: state.ID,
			AlternativeID: candidate.alternative.ID,
			MatchedEvents: append([]string(nil), candidate.canonical.Events...),
			Bindings:      append([]pattern.CanonicalBinding(nil), candidate.canonical.Bindings...),
			Target:        componentID, Generated: generatedRecord,
			Scheduled:         scheduledPlans(bodyResult.scheduled),
			CanceledSchedules: append([]string(nil), bodyResult.canceledSchedules...),
			StateReads:        bodyResult.stateReads, StateWrites: bodyResult.stateWrites,
		})
		terminationContext := processTerminationContext{
			modelDigest: model.digest, functionRuntime: functionRuntime,
			frontiers: frontiers, clocks: clocks, poset: poset, depths: depths,
			queue: queue, seenItems: seenItems, moduleState: moduleState,
			stateSnapshots: stateSnapshots, statementSteps: statementSteps, firings: firings,
		}
		if bodyResult.initializationFailure != nil {
			failure := bodyResult.initializationFailure
			if _, err := finalizeFailedModuleInitializationChain(failure, terminationContext); err != nil {
				return fmt.Errorf("deterministic process %s.%s allocator initialization: %w",
					componentID, runtime.declaration.ID, err)
			}
			if !runtime.terminated {
				if err := completeProcessesForModuleException(
					[]*processRuntime{runtime}, runtime, failure.raised.event.ID, terminationContext,
				); err != nil {
					return fmt.Errorf("deterministic process %s.%s allocator call abortion: %w",
						componentID, runtime.declaration.ID, err)
				}
			}
			return nil
		}
		if bodyResult.raised != nil {
			if err := terminateModuleProcesses(
				runtime, componentRuntimes, bodyResult.raised,
				terminationContext,
			); err != nil {
				return fmt.Errorf("deterministic process %s.%s exception: %w",
					componentID, runtime.declaration.ID, err)
			}
			if err := finalizeCompletedDynamicModuleProcesses(runtime, terminationContext); err != nil {
				return fmt.Errorf("deterministic process %s.%s finalization: %w",
					componentID, runtime.declaration.ID, err)
			}
			return nil
		}
		if bodyResult.exitProcess || candidate.alternative.Next == "" {
			runtime.state = ""
			runtime.terminated = true
			runtime.completion = "normal"
		} else {
			runtime.state = candidate.alternative.Next
			runtime.activation++
		}
		if err := finalizeCompletedDynamicModuleProcesses(runtime, terminationContext); err != nil {
			return fmt.Errorf("deterministic process %s.%s finalization: %w",
				componentID, runtime.declaration.ID, err)
		}
		// Generated occurrences become observable through the canonical worklist.
		// Yield here so another process cannot observe an implementation-specific
		// order that bypasses those events. A null body can continue scheduling
		// immediately because it added no observation to the worklist.
		if len(bodyResult.generated) != 0 {
			return nil
		}
	}
}

type processTerminationContext struct {
	modelDigest     string
	functionRuntime *functionExecutionRuntime
	frontiers       *causalFrontierRegistry
	clocks          *deterministicClockKernel
	poset           *gorapide.Poset
	depths          map[gorapide.EventID]uint64
	queue           *executionQueue
	seenItems       map[string]bool
	moduleState     moduleStateRuntime
	stateSnapshots  stateSnapshotRegistry
	statementSteps  *statementBudget
	firings         *[]FiringRecord
	// activeModuleHandlers is the exact set of module-handler activations whose
	// statement lists are currently executing synchronously. Exception delivery
	// caused by a failed generator in one of those bodies must bypass that same
	// handler under Executable LRM 8.3.1. The map is lookup-only semantic state;
	// no result depends on Go map traversal.
	activeModuleHandlers map[string]gorapide.EventID
}

// finalizeCompletedDynamicModuleProcesses closes the lifecycle of an active
// module after its last elaborated process completes. Allocator-created modules
// use their allocation identity as the execution component. A static module is
// normally still named by its architecture and therefore only becomes
// Completed here; if failed architecture scope loss already made it unnamable,
// the same joint process/name-loss frontier executes its ordinary final part
// and Finish. Neither scheduler traversal nor Go reachability chooses that
// boundary.
func finalizeCompletedDynamicModuleProcesses(
	process *processRuntime,
	context processTerminationContext,
) error {
	if process == nil || context.modelDigest == "" || context.functionRuntime == nil ||
		context.functionRuntime.lifecycle == nil || context.frontiers == nil ||
		context.poset == nil || context.firings == nil || context.statementSteps == nil ||
		context.clocks == nil || context.depths == nil || context.queue == nil ||
		context.seenItems == nil || context.moduleState == nil || context.stateSnapshots == nil {
		return fmt.Errorf("%w: dynamic process finalization runtime is incomplete", ErrInvalidDeclarativeProcess)
	}
	runtime := context.functionRuntime
	componentID := process.componentID
	templateID := runtime.templateComponentID(componentID)
	module := runtime.modules[componentID]
	moduleID := module.Identity()
	static := templateID == componentID && moduleID != "" &&
		runtime.moduleTemplates[moduleID] == componentID
	if templateID == componentID && !static {
		// Go-built deterministic components without source module membership
		// remain outside the language-level allocation lifecycle.
		return nil
	}
	if moduleID == "" || (!static && moduleID != componentID) {
		return fmt.Errorf(
			"%w: process component %q has no exact module allocation",
			ErrInvalidDeclarativeProcess, componentID,
		)
	}
	if static {
		lifecycle := runtime.lifecycle.modules[moduleID]
		if lifecycle == nil {
			return fmt.Errorf(
				"%w: static process component %q has no module lifecycle",
				ErrInvalidDeclarativeProcess, componentID,
			)
		}
		if lifecycle.state == ModuleFinalizedState {
			return nil
		}
		if lifecycle.state == ModuleTerminatedState &&
			(!runtime.architectureScopeClosed || !runtime.postScopeComponents[componentID]) {
			return nil
		}
		if lifecycle.state == ModuleRunningState && runtime.architectureScopeClosed &&
			runtime.postScopeComponents[componentID] {
			// The process/controller worklist must close before an unnamable
			// static module is completed. The engine performs that quiescence-
			// gated transition after every ready observation and clock release.
			return nil
		}
	}
	componentProcesses := runtime.processes[componentID]
	if len(componentProcesses) == 0 {
		return fmt.Errorf(
			"%w: dynamic module %q has no registered process set",
			ErrInvalidDeclarativeProcess, componentID,
		)
	}
	completionFrontier := make([]gorapide.EventID, 0)
	for _, candidate := range componentProcesses {
		if candidate == nil || candidate.componentID != componentID || !candidate.elaborated {
			return fmt.Errorf(
				"%w: dynamic module %q has an incomplete process registration",
				ErrInvalidDeclarativeProcess, componentID,
			)
		}
		if !candidate.terminated {
			return nil
		}
		completionFrontier = append(
			completionFrontier, context.frontiers.get(candidate.causalOwner)...,
		)
	}
	completionFrontier = maximalKnownCausalFrontier(
		context.poset, canonicalEventIDs(completionFrontier),
	)
	finalizedModuleID, causes, err := runtime.lifecycle.completeProcesses(
		moduleID, completionFrontier,
	)
	if err != nil || finalizedModuleID == "" {
		return err
	}
	if finalizedModuleID != moduleID {
		return fmt.Errorf(
			"%w: process component %q finalized unexpected module %q",
			ErrInvalidDeclarativeProcess, componentID, finalizedModuleID,
		)
	}
	causes = maximalKnownCausalFrontier(context.poset, causes)
	if static {
		return materializeArchitectureConstituentFinalization(
			moduleID, componentID, causes, context,
		)
	}
	if uint64(len(*context.firings)) >= runtime.maxFirings {
		return fmt.Errorf("%w: max_firings=%d", ErrExecutionLimit, runtime.maxFirings)
	}
	finalization := &statementExecution{
		clocks: context.clocks, owner: "process-finalization:" + componentID,
		budget: context.statementSteps,
	}
	if err := materializeModuleFinalization(
		context.modelDigest, moduleID, causes, "processes-complete:"+componentID,
		runtime, finalization,
	); err != nil {
		return err
	}
	generated := make([]GeneratedEventRecord, 0, len(finalization.generated))
	for _, output := range finalization.generated {
		if err := context.poset.AddEventWithCause(output.event, output.causes...); err != nil {
			return fmt.Errorf(
				"dynamic module %q finalization output %s: %w",
				componentID, output.localID, err,
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
			OutputID: output.localID, EventID: string(output.event.ID),
			Exception: output.exception,
		})
	}
	*context.firings = append(*context.firings, FiringRecord{
		Sequence:   uint64(len(*context.firings) + 1),
		Transition: "process-finalization", Target: moduleID, Generated: generated,
	})
	return nil
}

// finalizePostScopeCompletedStaticModules performs the deferred half of an
// unnamable static module's process completion. It is called only at semantic
// quiescence, after every ready process output, module-local connection, and
// declarative-rule reaction has closed. Finalization therefore depends on the
// maximal component event frontier as well as every process and prior name
// loss, without turning scheduler traversal into causality.
func finalizePostScopeCompletedStaticModules(
	context processTerminationContext,
) (bool, error) {
	runtime := context.functionRuntime
	if runtime == nil || !runtime.architectureScopeClosed ||
		runtime.lifecycle == nil || context.frontiers == nil || context.poset == nil {
		return false, nil
	}
	componentIDs := make([]string, 0, len(runtime.postScopeComponents))
	for componentID := range runtime.postScopeComponents {
		componentIDs = append(componentIDs, componentID)
	}
	componentIDs = canonicalStrings(componentIDs)
	finalized := false
	for _, componentID := range componentIDs {
		moduleID := runtime.modules[componentID].Identity()
		lifecycle := runtime.lifecycle.modules[moduleID]
		if lifecycle == nil {
			return false, fmt.Errorf(
				"%w: post-scope component %q has no module lifecycle",
				ErrInvalidDeclarativeProcess, componentID,
			)
		}
		if lifecycle.state != ModuleRunningState {
			continue
		}
		componentProcesses := runtime.processes[componentID]
		if len(componentProcesses) == 0 {
			return false, fmt.Errorf(
				"%w: post-scope component %q has no process set",
				ErrInvalidDeclarativeProcess, componentID,
			)
		}
		completionFrontier := make([]gorapide.EventID, 0)
		allCompleted := true
		for _, process := range componentProcesses {
			if process == nil || !process.elaborated {
				return false, fmt.Errorf(
					"%w: post-scope component %q has an incomplete process registration",
					ErrInvalidDeclarativeProcess, componentID,
				)
			}
			if !process.terminated {
				allCompleted = false
				break
			}
			completionFrontier = append(
				completionFrontier, context.frontiers.get(process.causalOwner)...,
			)
		}
		if !allCompleted {
			continue
		}
		for _, event := range context.poset.Events() {
			if event != nil && event.Source == componentID {
				completionFrontier = append(completionFrontier, event.ID)
			}
		}
		completionFrontier = maximalKnownCausalFrontier(
			context.poset, canonicalEventIDs(completionFrontier),
		)
		finalizedModuleID, causes, err := runtime.lifecycle.completeProcesses(
			moduleID, completionFrontier,
		)
		if err != nil {
			return false, err
		}
		if finalizedModuleID == "" {
			continue
		}
		if finalizedModuleID != moduleID {
			return false, fmt.Errorf(
				"%w: post-scope component %q finalized unexpected module %q",
				ErrInvalidDeclarativeProcess, componentID, finalizedModuleID,
			)
		}
		if err := materializeArchitectureConstituentFinalization(
			moduleID, componentID,
			maximalKnownCausalFrontier(context.poset, causes), context,
		); err != nil {
			return false, err
		}
		finalized = true
	}
	return finalized, nil
}

// executionComponentState resolves runtime-only allocation aliases used while
// final actions close through a static module's own connections. State remains
// stored once under the sealed component ID so canonical state records do not
// duplicate it under the allocation identity.
func executionComponentState(
	state moduleStateRuntime,
	runtime *functionExecutionRuntime,
	componentID string,
) map[string]*stateCell {
	if state == nil || componentID == "" {
		return nil
	}
	if cells := state[componentID]; cells != nil {
		return cells
	}
	if runtime == nil {
		return nil
	}
	return state[runtime.templateComponentID(componentID)]
}

func terminateModuleProcesses(
	runtime *processRuntime,
	componentRuntimes []*processRuntime,
	raised *raisedExceptionOccurrence,
	context processTerminationContext,
) error {
	if runtime == nil || runtime.declaration == nil || raised == nil || raised.event == nil {
		return fmt.Errorf("%w: incomplete process exception termination", ErrUnhandledRapideException)
	}
	functionRuntime := context.functionRuntime
	if functionRuntime == nil || functionRuntime.model == nil || functionRuntime.lifecycle == nil {
		return fmt.Errorf("%w: process %s has no module lifecycle runtime",
			ErrUnhandledRapideException, runtime.declaration.ID)
	}
	templateID := functionRuntime.templateComponentID(runtime.componentID)
	declaredProcesses := functionRuntime.model.processes[templateID]
	if componentRuntimes == nil {
		componentRuntimes = functionRuntime.processes[runtime.componentID]
	}
	if len(componentRuntimes) != len(declaredProcesses) {
		return fmt.Errorf("%w: module process runtime set is incomplete", ErrUnhandledRapideException)
	}
	for _, candidate := range componentRuntimes {
		if candidate == nil || candidate.componentID != runtime.componentID {
			return fmt.Errorf("%w: module process runtime set is inconsistent", ErrUnhandledRapideException)
		}
	}
	module := functionRuntime.modules[runtime.componentID]
	if module.Identity() == "" {
		return fmt.Errorf("%w: component %q has no generated module allocation",
			ErrUnhandledRapideException, runtime.componentID)
	}
	if handler, exists := functionRuntime.model.moduleHandlers[templateID]; exists {
		// A module handler is owned by the generated module, not by the
		// architecture name that once denoted it. Failed architecture-scope
		// loss therefore leaves this handler live for every still-Running
		// post-scope process; normal selection/escape semantics below decide
		// recovery or termination from the exact raised occurrence.
		invocation, err := selectModuleExceptionHandler(handler, raised, runtime.componentID)
		if err != nil {
			return fmt.Errorf("%w: module handler selection: %v", ErrUnhandledRapideException, err)
		}
		if invocation != nil {
			if err := completeProcessesForModuleException(
				[]*processRuntime{runtime}, runtime, raised.event.ID, context,
			); err != nil {
				return err
			}
			handlerResult, err := executeModuleExceptionHandler(
				runtime.componentID, invocation, raised, context,
			)
			if err != nil {
				return err
			}
			if handlerResult.raised == nil {
				return nil
			}
			if !handlerResult.propagationComplete {
				if err := functionRuntime.lifecycle.terminate(module.Identity(), handlerResult.raised.event.ID); err != nil {
					return fmt.Errorf("%w: %v", ErrUnhandledRapideException, err)
				}
				if err := completeProcessesForModuleException(
					componentRuntimes, nil, handlerResult.raised.event.ID, context,
				); err != nil {
					return err
				}
				if err := propagateUnhandledModuleException(module.Identity(), handlerResult.raised, context); err != nil {
					return fmt.Errorf("%w: %v", ErrUnhandledRapideException, err)
				}
			}
			if functionRuntime.architectureScopeClosed &&
				functionRuntime.postScopeComponents[runtime.componentID] {
				if err := finalizeCompletedDynamicModuleProcesses(runtime, context); err != nil {
					return err
				}
			}
			return nil
		}
	}
	if err := functionRuntime.lifecycle.terminate(module.Identity(), raised.event.ID); err != nil {
		return fmt.Errorf("%w: %v", ErrUnhandledRapideException, err)
	}
	if err := completeProcessesForModuleException(componentRuntimes, runtime, raised.event.ID, context); err != nil {
		return err
	}
	if err := propagateUnhandledModuleException(module.Identity(), raised, context); err != nil {
		return fmt.Errorf("%w: %v", ErrUnhandledRapideException, err)
	}
	if functionRuntime.architectureScopeClosed &&
		functionRuntime.postScopeComponents[runtime.componentID] {
		if err := finalizeCompletedDynamicModuleProcesses(runtime, context); err != nil {
			return err
		}
	}
	return nil
}

func completeProcessesForModuleException(
	componentRuntimes []*processRuntime,
	raisingRuntime *processRuntime,
	exceptionEventID gorapide.EventID,
	context processTerminationContext,
) error {
	if exceptionEventID == "" {
		return fmt.Errorf("%w: module process completion has no exception occurrence", ErrUnhandledRapideException)
	}
	functionRuntime := context.functionRuntime
	for _, candidate := range componentRuntimes {
		if candidate == nil {
			return fmt.Errorf("%w: module process runtime set contains nil", ErrUnhandledRapideException)
		}
		// A process declaration whose module failed during initialization was
		// never elaborated. It has no execution to complete or cancel.
		if !candidate.elaborated {
			continue
		}
		if candidate.terminated && candidate != raisingRuntime {
			continue
		}
		canceledSchedules, canceledSuspensions := context.clocks.cancelProcessWork(candidate)
		candidate.canceledSchedules = append(candidate.canceledSchedules, canceledSchedules...)
		candidate.canceledSuspensions = append(candidate.canceledSuspensions, canceledSuspensions...)
		if candidate.switchYield != nil {
			candidate.canceledSwitches = append(candidate.canceledSwitches, candidate.switchYield.id)
			candidate.switchYield = nil
		}
		if candidate.continuation != nil {
			if err := abortProcessFunctionContinuations(
				candidate, exceptionEventID, context.modelDigest, functionRuntime,
			); err != nil {
				return fmt.Errorf("%w: process %s function cleanup: %v",
					ErrUnhandledRapideException, candidate.declaration.ID, err)
			}
			if err := releasePatternModuleBindings(
				context.modelDigest, candidate.continuation.moduleBindings,
				functionRuntime, &candidate.continuation.execution,
			); err != nil {
				return fmt.Errorf("%w: process %s pattern-name cleanup: %v",
					ErrUnhandledRapideException, candidate.declaration.ID, err)
			}
			candidate.continuation.moduleBindings = nil
			if _, err := flushProcessContinuation(
				candidate, candidate.continuation, context.poset, context.depths,
				context.queue, context.seenItems, context.clocks, context.frontiers,
				context.moduleState, context.stateSnapshots, context.firings,
			); err != nil {
				return fmt.Errorf("%w: process %s cleanup audit: %v",
					ErrUnhandledRapideException, candidate.declaration.ID, err)
			}
			candidate.pendingOperations = candidate.continuation.execution.pendingOperations
			candidate.continuation.frames = nil
			candidate.continuation = nil
		}
		candidate.suspension = nil
		candidate.delayWindows = nil
		candidate.state = ""
		candidate.terminated = true
		candidate.exceptionEventID = exceptionEventID
		candidate.frontier = []gorapide.EventID{exceptionEventID}
		if candidate == raisingRuntime {
			candidate.completion = "exception"
		} else {
			candidate.completion = "module-termination"
		}
		context.frontiers.set(candidate.causalOwner, candidate.frontier)
	}
	return nil
}

func propagateUnhandledModuleException(
	sourceModuleID string,
	raised *raisedExceptionOccurrence,
	context processTerminationContext,
) error {
	runtime := context.functionRuntime
	if runtime == nil || runtime.lifecycle == nil || runtime.contexts == nil || runtime.propagations == nil {
		return fmt.Errorf("exception propagation runtime is unavailable")
	}
	if raised == nil || raised.event == nil || raised.name == "" || raised.declaration == "" || sourceModuleID == "" {
		return fmt.Errorf("exception propagation source is incomplete")
	}
	if runtime.propagations.has(raised.event.ID, sourceModuleID) {
		return nil
	}
	targets, err := runtime.contexts.exceptionPropagationTargets(sourceModuleID)
	if err != nil {
		return err
	}
	record := ExceptionPropagationRecord{
		ExceptionEventID:     string(raised.event.ID),
		Exception:            raised.name,
		ExceptionDeclaration: raised.declaration,
		SourceModuleID:       sourceModuleID,
		SourceComponentID:    runtime.contexts.componentByModule[sourceModuleID],
		Targets:              make([]ExceptionPropagationTargetRecord, 0, len(targets)),
	}
	handlerInvocations := make(map[string]*moduleExceptionHandlerInvocation)
	for _, target := range targets {
		targetRecord := ExceptionPropagationTargetRecord{
			ModuleID: target.moduleID, ComponentID: runtime.contexts.componentByModule[target.moduleID],
			Relations: append([]string(nil), target.relations...),
		}
		switch target.moduleID {
		case moduleEnvironmentRoot:
			targetRecord.Disposition = exceptionEscapedEnvironmentDisposition
		default:
			targetRuntime := runtime.lifecycle.modules[target.moduleID]
			if targetRuntime == nil {
				return fmt.Errorf("exception propagation target module %q is unavailable", target.moduleID)
			}
			switch targetRuntime.state {
			case ModuleFinalizedState:
				targetRecord.Disposition = exceptionIgnoredFinalizedDisposition
			case ModuleTerminatedState:
				if targetRuntime.terminationEventID == raised.event.ID {
					targetRecord.Disposition = exceptionDeliveredDisposition
				} else {
					targetRecord.Disposition = exceptionIgnoredTerminatedDisposition
				}
			default:
				targetRecord.Disposition = exceptionDeliveredDisposition
			}
			if targetRecord.Disposition == exceptionDeliveredDisposition &&
				targetRuntime.state != ModuleTerminatedState {
				if _, active := context.activeModuleHandlers[target.moduleID]; active {
					// Immediate module-handler execution cannot interleave an unrelated
					// observation turn. Any synchronous parent, linked, or coalesced
					// delivery reached here was produced while this handler body was
					// active and therefore bypasses that same handler. The ordinary
					// delivered path below terminates the owner and propagates the exact
					// occurrence without discarding its relation audit.
				} else {
					templateID := runtime.templateComponentID(targetRecord.ComponentID)
					if handler, exists := runtime.model.moduleHandlers[templateID]; exists {
						invocation, err := selectModuleExceptionHandler(handler, raised, targetRecord.ComponentID)
						if err != nil {
							return err
						}
						if invocation != nil {
							targetRecord.Disposition = exceptionHandledDisposition
							handlerInvocations[target.moduleID] = invocation
						}
					}
				}
			}
		}
		record.Targets = append(record.Targets, targetRecord)
	}
	if err := runtime.propagations.add(record); err != nil {
		return err
	}
	newlyTerminated := make(map[string]bool, len(record.Targets))
	for _, target := range record.Targets {
		if target.Disposition != exceptionDeliveredDisposition {
			continue
		}
		targetRuntime := runtime.lifecycle.modules[target.ModuleID]
		alreadyTerminatedByOccurrence := targetRuntime.state == ModuleTerminatedState &&
			targetRuntime.terminationEventID == raised.event.ID
		if !alreadyTerminatedByOccurrence {
			if err := runtime.lifecycle.terminate(target.ModuleID, raised.event.ID); err != nil {
				return err
			}
			newlyTerminated[target.ModuleID] = true
		}
	}
	// Mark every direct unhandled recipient before executing any selected
	// sibling handler. A descendant exception generated by one handler cannot
	// overtake the occurrence that was already broadcast to an independent
	// direct target. Cleanup remains deferred until all handler activations have
	// completed so this state transition adds no traversal-ordered event edge.
	for _, target := range record.Targets {
		if target.Disposition != exceptionHandledDisposition {
			continue
		}
		handlerResult, err := executeModuleExceptionHandler(
			target.ComponentID, handlerInvocations[target.ModuleID], raised, context,
		)
		if err != nil {
			return err
		}
		if handlerResult.raised != nil {
			if err := runtime.propagations.setTargetDisposition(
				raised.event.ID, sourceModuleID, target.ModuleID,
				exceptionHandlerRaisedDisposition,
			); err != nil {
				return err
			}
			if !handlerResult.propagationComplete {
				if err := runtime.lifecycle.terminate(target.ModuleID, handlerResult.raised.event.ID); err != nil {
					return err
				}
				if err := completeProcessesForModuleException(
					runtime.processes[target.ComponentID], nil, handlerResult.raised.event.ID, context,
				); err != nil {
					return err
				}
				if err := propagateUnhandledModuleException(target.ModuleID, handlerResult.raised, context); err != nil {
					return err
				}
			}
			if !handlerResult.propagationComplete {
				if componentProcesses := runtime.processes[target.ComponentID]; len(componentProcesses) != 0 {
					if err := finalizeCompletedDynamicModuleProcesses(componentProcesses[0], context); err != nil {
						return err
					}
				}
			}
		}
	}
	// Clean every direct recipient only after all direct recipient states were
	// committed and selected handler bodies completed. Cleanup of one linked
	// target cannot finalize or otherwise change a sibling's eligibility.
	for _, target := range record.Targets {
		if !newlyTerminated[target.ModuleID] {
			continue
		}
		if err := completeProcessesForModuleException(
			runtime.processes[target.ComponentID], nil, raised.event.ID, context,
		); err != nil {
			return err
		}
	}
	for _, target := range record.Targets {
		if target.Disposition != exceptionDeliveredDisposition {
			continue
		}
		if err := propagateUnhandledModuleException(target.ModuleID, raised, context); err != nil {
			return err
		}
		if componentProcesses := runtime.processes[target.ComponentID]; len(componentProcesses) != 0 {
			if err := finalizeCompletedDynamicModuleProcesses(componentProcesses[0], context); err != nil {
				return err
			}
		}
	}
	return nil
}

func processFrontierForMatch(
	poset *gorapide.Poset,
	frontier []gorapide.EventID,
	matched gorapide.EventSet,
) []gorapide.EventID {
	if poset == nil || len(frontier) == 0 || len(matched) == 0 {
		return canonicalEventIDs(frontier)
	}
	result := make([]gorapide.EventID, 0, len(frontier))
	for _, prior := range frontier {
		dominated := false
		for _, event := range matched {
			if event != nil && (prior == event.ID || poset.IsCausallyBefore(prior, event.ID)) {
				dominated = true
				break
			}
		}
		if !dominated {
			result = append(result, prior)
		}
	}
	return canonicalEventIDs(result)
}

func awaitElseCandidate(alternative AwaitAlternative) (awaitCandidate, error) {
	match := pattern.MatchResult{Events: gorapide.EventSet{}, Bindings: pattern.Bindings{}}
	canonical, err := pattern.CanonicalizeMatch(match)
	if err != nil {
		return awaitCandidate{}, err
	}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return awaitCandidate{}, err
	}
	return awaitCandidate{
		alternative: alternative, match: match, canonical: canonical,
		key: string(encoded), isElse: true,
	}, nil
}

func processConsumptionScope(componentID, processID string) string {
	return "process:" + componentID + "\x00" + processID
}

func processChoiceKey(componentID, processID, stateID string) string {
	return componentID + "/" + processID + "/" + stateID
}

func eligibleAwaitCandidates(
	componentID, processID string,
	state ProcessState,
	poset *gorapide.Poset,
	observed gorapide.EventSet,
	observationRanks map[string]uint64,
	consumption *RuleConsumption,
	scope string,
	moduleCells map[string]*stateCell,
	stateSnapshots stateSnapshotRegistry,
	priorOperations []stateOperationReference,
	functionRuntime *functionExecutionRuntime,
) ([]awaitCandidate, error) {
	available, err := consumption.Available(scope, observed)
	if err != nil {
		return nil, err
	}
	candidates := make([]awaitCandidate, 0)
	for _, alternative := range state.Alternatives {
		alternativeAvailable := available
		if !pattern.HasModuleSourceBinding(alternative.Trigger) {
			alternativeAvailable = make(gorapide.EventSet, 0, len(available))
			for _, event := range available {
				if event != nil && event.Source == componentID {
					alternativeAvailable = append(alternativeAvailable, event)
				}
			}
		}
		trigger := alternative.Trigger
		if pattern.HasModuleSourceBinding(trigger) {
			trigger, err = pattern.ScopeUnqualifiedEventSources(trigger, componentID)
			if err != nil {
				return nil, fmt.Errorf("deterministic process %s.%s state %s alternative %s trigger source scope: %w",
					componentID, processID, state.ID, alternative.ID, err)
			}
		}
		view := newObservationView(alternativeAvailable, poset, functionRuntime)
		matches, err := pattern.MatchWithBindings(trigger, view)
		if err != nil {
			return nil, fmt.Errorf("deterministic process %s.%s state %s alternative %s: %w",
				componentID, processID, state.ID, alternative.ID, err)
		}
		for _, match := range matches {
			guardMatched, guardReads, guardCauses, err := evaluateMatchGuard(
				"process "+componentID+"."+processID+" state "+state.ID+" alternative "+alternative.ID,
				componentID, alternative.Guard, match, observationRanks, stateSnapshots, moduleCells,
				priorOperations,
			)
			if err != nil {
				return nil, err
			}
			if !guardMatched {
				continue
			}
			canonical, err := pattern.CanonicalizeMatch(match)
			if err != nil {
				return nil, err
			}
			encoded, err := json.Marshal(canonical)
			if err != nil {
				return nil, err
			}
			candidates = append(candidates, awaitCandidate{
				alternative: alternative, match: match, canonical: canonical,
				key:        string(encoded),
				guardReads: guardReads, guardCauses: guardCauses,
			})
		}
	}
	if len(candidates) == 0 {
		return nil, nil
	}
	// Stanford Rapide module processes select earliest, then maximal matches.
	// The observation-order "first" relation applies only to synchronized
	// transition rules in an interface behavior, not procedural await/when.
	candidates, err = earliestAwaitCandidates(candidates, poset)
	if err != nil {
		return nil, err
	}
	candidates = maximalAwaitCandidates(candidates)
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].alternative.ID != candidates[j].alternative.ID {
			return candidates[i].alternative.ID < candidates[j].alternative.ID
		}
		return candidates[i].key < candidates[j].key
	})
	return candidates, nil
}

func earliestAwaitCandidates(candidates []awaitCandidate, poset pattern.PosetReader) ([]awaitCandidate, error) {
	result := make([]awaitCandidate, 0, len(candidates))
	for i, candidate := range candidates {
		dominated := false
		for j, other := range candidates {
			if i == j {
				continue
			}
			earlier, err := pattern.IsEarlierMatch(other.match.Events, candidate.match.Events, poset)
			if err != nil {
				return nil, err
			}
			if earlier {
				dominated = true
				break
			}
		}
		if !dominated {
			result = append(result, candidate)
		}
	}
	return result, nil
}

func maximalAwaitCandidates(candidates []awaitCandidate) []awaitCandidate {
	matches := make([]pattern.MatchResult, len(candidates))
	for i, candidate := range candidates {
		matches[i] = candidate.match
	}
	maximal := maximalRuleMatches(matches)
	keys := make(map[string]bool, len(maximal))
	for _, match := range maximal {
		canonical, err := pattern.CanonicalizeMatch(match)
		if err != nil {
			continue
		}
		encoded, err := json.Marshal(canonical)
		if err == nil {
			keys[string(encoded)] = true
		}
	}
	result := make([]awaitCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if keys[candidate.key] {
			result = append(result, candidate)
		}
	}
	return result
}

func awaitChoiceKey(componentID, processID, stateID string, candidate awaitCandidate) string {
	return componentID + "/" + processID + "/" + stateID + "/" + candidate.alternative.ID + "@" + digestBytes([]byte(candidate.key))
}

func canonicalProcessExecutionRecords(runtimes map[string][]*processRuntime, frontiers *causalFrontierRegistry) []ProcessExecutionRecord {
	result := make([]ProcessExecutionRecord, 0)
	for _, componentRuntimes := range runtimes {
		for _, runtime := range componentRuntimes {
			if runtime == nil || !runtime.elaborated {
				continue
			}
			semanticFrontier := frontiers.get(runtime.causalOwner)
			frontier := make([]string, len(semanticFrontier))
			for i, eventID := range semanticFrontier {
				frontier[i] = string(eventID)
			}
			sort.Strings(frontier)
			canceledSchedules := append([]string(nil), runtime.canceledSchedules...)
			canceledSuspensions := append([]string(nil), runtime.canceledSuspensions...)
			canceledSwitches := append([]string(nil), runtime.canceledSwitches...)
			sort.Strings(canceledSchedules)
			sort.Strings(canceledSuspensions)
			sort.Strings(canceledSwitches)
			result = append(result, ProcessExecutionRecord{
				ComponentID: runtime.componentID, ProcessID: runtime.declaration.ID,
				State: runtime.state, Terminated: runtime.terminated,
				Completion: runtime.completion, ExceptionEventID: string(runtime.exceptionEventID),
				CanceledSchedules:      canceledSchedules,
				CanceledSuspensions:    canceledSuspensions,
				CanceledSwitches:       canceledSwitches,
				Frontier:               frontier,
				StateOperationFrontier: stateOperationReferenceIDs(runtime.pendingOperations),
			})
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].ComponentID != result[j].ComponentID {
			return result[i].ComponentID < result[j].ComponentID
		}
		return result[i].ProcessID < result[j].ProcessID
	})
	return result
}

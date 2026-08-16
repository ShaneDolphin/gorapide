package arch

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/pattern"
)

type suspendedProcessAction struct {
	localID         string
	action          string
	occurrence      string
	params          map[string]any
	match           pattern.MatchResult
	matchDigest     string
	stateCauses     []gorapide.EventID
	stateOperations []stateOperationReference
}

type processDelayWindow struct {
	clock  string
	start  uint64
	finish uint64
}

type processBodyContinuation struct {
	state                    ProcessState
	candidate                awaitCandidate
	componentID              string
	rule                     *DeclarativeRule
	matchDigest              string
	moduleBindings           []patternModuleBindingRuntime
	frames                   []processStatementFrame
	execution                statementExecution
	functionCalls            []processFunctionContinuation
	firingIndex              int
	flushedEvents            int
	flushedReads             int
	flushedWrites            int
	flushedActions           int
	flushedCanceledSchedules int
	handledInterruptEvents   map[gorapide.EventID]bool
}

func markProcessInterruptMatch(
	continuation *processBodyContinuation,
	match pattern.MatchResult,
) {
	if continuation == nil {
		return
	}
	if continuation.handledInterruptEvents == nil {
		continuation.handledInterruptEvents = make(map[gorapide.EventID]bool)
	}
	for _, event := range match.Events {
		if event != nil {
			continuation.handledInterruptEvents[event.ID] = true
		}
	}
}

// processFunctionContinuation is one saved synchronous caller. The active
// function body lives in processBodyContinuation itself; this frame retains
// the caller's exact component, bindings, statement frames, and effects until
// the callee either returns or terminates. No Go stack, closure, or callback is
// part of the semantic state.
type processFunctionContinuation struct {
	call                *resumableFunctionCall
	callerComponentID   string
	callerRule          *DeclarativeRule
	callerCandidate     awaitCandidate
	callerMatchDigest   string
	callerFrames        []processStatementFrame
	callerExecution     statementExecution
	callerStatementPath string
	callAuditPath       string
	resultDestination   processFunctionResultDestination
}

type processFunctionResultDestination struct {
	kind            string
	generalForFrame int
	generalForPhase string
}

const processFunctionGeneralForResult = "general-for"

// processSwitchYield is an immediately ready semantic switching point. Rapide
// makes completion of a synchronous function call a module switching point;
// retaining it explicitly lets replay and exploration schedule the caller's
// continuation without depending on the Go call stack.
type processSwitchYield struct {
	id string
}

type processStatementFrame struct {
	statements  []Statement
	path        string
	next        int
	match       pattern.MatchResult
	matchDigest string
	doControl   bool
	loop        bool
	loopPath    string
	doName      string
	iteration   uint64
	bindings    pattern.Bindings
	iterator    *processIteratorContinuation
	generalFor  *processGeneralForContinuation
	handler     *processHandlerContinuation
}

const (
	processHandlerProtectedPhase = "protected"
	processHandlerBodyPhase      = "handler"
)

// processHandlerContinuation retains the semantic lifetime of one active
// handler-bearing block. The activation event set is a generation snapshot,
// not an observation-order shortcut: only occurrences created after block
// entry can interrupt the protected computation, exactly as Executable LRM
// 8.3.2 requires.
type processHandlerContinuation struct {
	id               string
	owner            string
	path             string
	handler          ExceptionHandler
	outerMatch       pattern.MatchResult
	outerMatchDigest string
	phase            string
	handled          *raisedExceptionOccurrence
	activationEvents map[gorapide.EventID]bool
}

type processIteratorContinuation struct {
	value     *finiteRangeIterator
	body      []Statement
	name      string
	path      string
	iteration uint64
}

type processGeneralForContinuation struct {
	initializer ExecutableObjectExpression
	test        ExecutableObjectExpression
	next        ExecutableObjectExpression
	body        []Statement
	path        string
	iteration   uint64
	phase       string
}

func cloneProcessMatch(match pattern.MatchResult) pattern.MatchResult {
	return pattern.MatchResult{
		Events:   append(gorapide.EventSet(nil), match.Events...),
		Bindings: append(pattern.Bindings(nil), match.Bindings...),
	}
}

func processFrameMatch(
	continuation *processBodyContinuation,
	frame *processStatementFrame,
) (pattern.MatchResult, string) {
	if frame != nil && frame.matchDigest != "" {
		match := cloneProcessMatch(frame.match)
		match.Bindings = append(pattern.Bindings(nil), frame.bindings...)
		return match, frame.matchDigest
	}
	match := pattern.MatchResult{}
	digest := ""
	if continuation != nil {
		match = cloneProcessMatch(continuation.candidate.match)
		digest = continuation.matchDigest
	}
	if frame != nil {
		match.Bindings = append(pattern.Bindings(nil), frame.bindings...)
	}
	return match, digest
}

func processKnownEventIDs(
	continuation *processBodyContinuation,
	poset *gorapide.Poset,
) map[gorapide.EventID]bool {
	known := make(map[gorapide.EventID]bool)
	if poset != nil {
		for _, event := range poset.Events() {
			known[event.ID] = true
		}
	}
	include := func(execution *statementExecution) {
		if execution == nil {
			return
		}
		for _, output := range execution.generated {
			if output.event != nil {
				known[output.event.ID] = true
			}
		}
	}
	if continuation != nil {
		for index := range continuation.functionCalls {
			include(&continuation.functionCalls[index].callerExecution)
		}
		include(&continuation.execution)
	}
	return known
}

func handlerHasInterruptChoice(handler ExceptionHandler) bool {
	for _, choice := range handler.Choices {
		if choice.Action != "" || choice.Any {
			return true
		}
	}
	return false
}

func deactivateProcessInterruptHandler(execution *statementExecution, id string) error {
	if execution == nil || id == "" {
		return fmt.Errorf("%w: active process handler is incomplete", ErrInvalidExceptionHandler)
	}
	for index := len(execution.interruptHandlers) - 1; index >= 0; index-- {
		if execution.interruptHandlers[index].id != id {
			continue
		}
		execution.interruptHandlers = append(
			execution.interruptHandlers[:index], execution.interruptHandlers[index+1:]...,
		)
		return nil
	}
	return nil
}

func popProcessHandledOccurrence(
	execution *statementExecution,
	handled *raisedExceptionOccurrence,
) error {
	if handled == nil {
		return nil
	}
	if execution == nil || len(execution.handledExceptions) == 0 ||
		execution.handledExceptions[len(execution.handledExceptions)-1] != handled {
		return fmt.Errorf("%w: process handler lost its active handled occurrence", ErrInvalidExceptionHandler)
	}
	execution.handledExceptions = execution.handledExceptions[:len(execution.handledExceptions)-1]
	return nil
}

func completeProcessHandlerFrame(
	continuation *processBodyContinuation,
	frame *processStatementFrame,
) error {
	if continuation == nil || frame == nil || frame.handler == nil {
		return nil
	}
	controller := frame.handler
	switch controller.phase {
	case processHandlerProtectedPhase:
		return deactivateProcessInterruptHandler(&continuation.execution, controller.id)
	case processHandlerBodyPhase:
		return popProcessHandledOccurrence(&continuation.execution, controller.handled)
	default:
		return fmt.Errorf(
			"%w: process handler %s has phase %q",
			ErrInvalidExceptionHandler, controller.path, controller.phase,
		)
	}
}

func statementSuspendsProcess(statement Statement) bool {
	if statement.kind == TimedStatementKind {
		return true
	}
	return statement.kind == EventCallStatement && statement.timing != nil &&
		(statement.timing.Kind == PauseTimingClause || statement.timing.Kind == DelayTimingClause)
}

func statementTreeSuspendsProcess(statement Statement) bool {
	if statementSuspendsProcess(statement) {
		return true
	}
	contains := func(statements []Statement) bool {
		for _, nested := range statements {
			if statementTreeSuspendsProcess(nested) {
				return true
			}
		}
		return false
	}
	switch statement.kind {
	case DoBlockStatementKind:
		return contains(statement.handledBody)
	case HandlerBlockStatementKind:
		if contains(statement.handledBody) || contains(statement.handler.Else) {
			return true
		}
		for _, choice := range statement.handler.Choices {
			if contains(choice.Statements) {
				return true
			}
		}
	case IfStatementKind:
		return contains(statement.thenBranch) || contains(statement.elseBranch)
	case LoopStatementKind, ForStatementKind, GeneralForStatementKind:
		return contains(statement.loopBody)
	case CaseStatementKind:
		if contains(statement.caseDefault) {
			return true
		}
		for _, alternative := range statement.caseAlts {
			if contains(alternative.body) {
				return true
			}
		}
	}
	return false
}

func processEventUnavailable(runtime *processRuntime, event *gorapide.Event) bool {
	if runtime == nil || event == nil {
		return false
	}
	for _, window := range runtime.delayWindows {
		timing, related := event.Timing(window.clock)
		if related && timing.Finish >= window.start && timing.Finish <= window.finish {
			return true
		}
	}
	return false
}

func filterProcessDelayWindows(runtime *processRuntime, events gorapide.EventSet) gorapide.EventSet {
	result := make(gorapide.EventSet, 0, len(events))
	for _, event := range events {
		if !processEventUnavailable(runtime, event) {
			result = append(result, event)
		}
	}
	return result
}

func beginProcessBodyContinuation(
	runtime *processRuntime,
	state ProcessState,
	candidate awaitCandidate,
	bodyRule *DeclarativeRule,
	previousFrontier []gorapide.EventID,
	budget *statementBudget,
	clocks *deterministicClockKernel,
	functionRuntime *functionExecutionRuntime,
	cells map[string]*stateCell,
	firingIndex int,
) (*processBodyContinuation, error) {
	if runtime == nil || bodyRule == nil || bodyRule.Body == nil || bodyRule.Body.Statements == nil {
		return nil, fmt.Errorf("%w: process continuation has no statement body", ErrInvalidDeclarativeProcess)
	}
	matchDigest, err := pattern.SemanticDigestMatches([]pattern.MatchResult{candidate.match})
	if err != nil {
		return nil, err
	}
	moduleBindings, err := acquirePatternModuleBindings(
		runtime.componentID, bodyRule.ID, matchDigest, candidate.match, functionRuntime,
	)
	if err != nil {
		return nil, err
	}
	triggerCauses := make([]gorapide.EventID, len(candidate.canonical.Events))
	for index, eventID := range candidate.canonical.Events {
		triggerCauses[index] = gorapide.EventID(eventID)
	}
	baseCauses := canonicalEventIDs(append(append(triggerCauses, previousFrontier...), candidate.guardCauses...))
	assignmentReads, stateWrites, err := applyStateAssignments(
		bodyRule.ID, bodyRule.Body.Assignments, candidate.match.Bindings, cells, baseCauses,
		canonicalStateOperationReferences(append(runtime.pendingOperations, stateOperationReferences(candidate.guardReads, nil)...)),
	)
	if err != nil {
		return nil, err
	}
	return &processBodyContinuation{
		state: state, candidate: candidate, componentID: runtime.componentID,
		rule: bodyRule, matchDigest: matchDigest,
		moduleBindings: moduleBindings, firingIndex: firingIndex,
		frames: []processStatementFrame{{
			statements: bodyRule.Body.Statements,
			bindings:   append(pattern.Bindings(nil), candidate.match.Bindings...),
			match:      cloneProcessMatch(candidate.match), matchDigest: matchDigest,
		}},
		execution: statementExecution{
			control: baseCauses,
			reads:   append(append([]StateReadRecord(nil), candidate.guardReads...), assignmentReads...),
			writes:  stateWrites,
			clocks:  clocks, owner: runtime.causalOwner, budget: budget,
			pendingOperations: canonicalStateOperationReferences(append(
				runtime.pendingOperations,
				stateOperationReferences(
					append(append([]StateReadRecord(nil), candidate.guardReads...), assignmentReads...), stateWrites,
				)...,
			)),
		},
	}, nil
}

func (continuation *processBodyContinuation) rootExecution() *statementExecution {
	if continuation == nil {
		return nil
	}
	if len(continuation.functionCalls) == 0 {
		return &continuation.execution
	}
	return &continuation.functionCalls[0].callerExecution
}

func (continuation *processBodyContinuation) drainActiveFunctionEffects() {
	if continuation == nil {
		return
	}
	child := &continuation.execution
	for index := len(continuation.functionCalls) - 1; index >= 0; index-- {
		frame := &continuation.functionCalls[index]
		drainResumableFunctionExecution(frame.call, child, &frame.callerExecution)
		child = &frame.callerExecution
	}
}

func (continuation *processBodyContinuation) auditStatementPath(statementPath string) string {
	if continuation == nil {
		return statementPath
	}
	result := statementPath
	for index := len(continuation.functionCalls) - 1; index >= 0; index-- {
		result = continuation.functionCalls[index].call.prefix + "/body/" + result
	}
	return result
}

func processFunctionSwitchID(owner string, callEventID, returnEventID gorapide.EventID) string {
	payload := "gorapide:process-switch:function-completion:v1\x00" + owner +
		"\x00" + string(callEventID) + "\x00" + string(returnEventID)
	return "swi1-" + strings.TrimPrefix(digestBytes([]byte(payload)), "sha256:")
}

func startProcessSuspension(
	runtime *processRuntime,
	continuation *processBodyContinuation,
	component *Component,
	componentID, modelDigest string,
	statement Statement,
	statementPath string,
	auditPath string,
	match pattern.MatchResult,
	matchDigest string,
	clocks *deterministicClockKernel,
	cells map[string]*stateCell,
	budget *statementBudget,
	firings *[]FiringRecord,
) error {
	if err := budget.consume(); err != nil {
		return err
	}
	clause := statement.timing
	if clause == nil || (clause.Kind != PauseTimingClause && clause.Kind != DelayTimingClause) {
		return fmt.Errorf("%w: statement %s has invalid normalized suspension", ErrInvalidDeclarativeStatement, statementPath)
	}
	clock := clocks.clocks[clause.Clock]
	if clock == nil {
		return fmt.Errorf("%w: missing clock %q", ErrInvalidBasicClock, clause.Clock)
	}
	occurrencePath := continuation.rule.ID + "\x00" + matchDigest + "\x00" + statementPath
	ticks, err := clocks.resolveTimingTicks(
		runtime.causalOwner+"\x00"+occurrencePath+"\x00"+clause.Clock,
		clause,
	)
	if err != nil {
		return err
	}
	deadline, err := clocks.deadline(clause.Clock, ticks)
	if err != nil {
		return err
	}
	suspension := &timedProcessSuspension{
		kind: clause.Kind, clock: clause.Clock, start: clock.now, deadline: deadline,
		statement: statementPath, runtime: runtime,
	}
	suspension.id = processSuspensionID(runtime.causalOwner, occurrencePath, clause.Kind, clause.Clock, deadline)
	if statement.kind == EventCallStatement {
		parameters, reads, stateCauses, err := resolveRuleParameters(
			continuation.rule.ID, statement.output, match.Bindings, cells,
		)
		if err != nil {
			return err
		}
		if !interfaceMatchesGeneratedAction(component, statement.output.Action, parameters) {
			return fmt.Errorf("%w: output action %s.%s", ErrActionTypeMismatch, componentID, statement.output.Action)
		}
		continuation.execution.reads = append(continuation.execution.reads, reads...)
		readOperations := stateOperationReferences(reads, nil)
		dependencies := append(eventIDStrings(continuation.execution.control), stateOperationReferenceIDs(continuation.execution.pendingOperations)...)
		if err := addStateOperationDependencies(readOperations, dependencies...); err != nil {
			return err
		}
		occurrence := continuation.rule.ID + "|match=" + matchDigest + "|statement=" + statementPath + "|output=" + statement.output.ID
		suspension.action = &suspendedProcessAction{
			localID: statement.output.ID, action: statement.output.Action,
			occurrence: occurrence, params: parameters,
			match: cloneProcessMatch(match), matchDigest: matchDigest,
			stateCauses:     canonicalEventIDs(stateCauses),
			stateOperations: canonicalStateOperationReferences(append(continuation.execution.pendingOperations, readOperations...)),
		}
		continuation.execution.pendingOperations = nil
	}
	if clause.Kind == DelayTimingClause {
		runtime.delayWindows = append(runtime.delayWindows, processDelayWindow{
			clock: clause.Clock, start: clock.now, finish: deadline,
		})
	}
	record := ProcessSuspensionRecord{
		SuspensionID: suspension.id, Kind: clause.Kind, Clock: clause.Clock,
		Start: strconv.FormatUint(clock.now, 10), Finish: strconv.FormatUint(deadline, 10),
		Statement: auditPath,
	}
	if suspension.action != nil {
		record.OutputID = suspension.action.localID
	}
	(*firings)[continuation.firingIndex].Suspensions = append(
		(*firings)[continuation.firingIndex].Suspensions, record,
	)
	suspension.record = len((*firings)[continuation.firingIndex].Suspensions) - 1
	runtime.suspension = suspension
	if deadline == clock.now {
		// Zero ticks has a real timing interval but does not advance the clock
		// or yield the executing process to the semantic scheduler.
		suspension.ready = true
	} else {
		clocks.addProcessSuspension(suspension)
	}
	return nil
}

func resumeProcessSuspension(
	runtime *processRuntime,
	continuation *processBodyContinuation,
	componentID, modelDigest string,
	functionRuntime *functionExecutionRuntime,
	clocks *deterministicClockKernel,
	frontiers *causalFrontierRegistry,
	firings *[]FiringRecord,
	cells map[string]*stateCell,
) error {
	suspension := runtime.suspension
	if suspension == nil || !suspension.ready {
		return fmt.Errorf("%w: process %s has no ready suspension", ErrInvalidDeclarativeProcess, runtime.declaration.ID)
	}
	if suspension.deadline != suspension.start {
		// A positive suspension yielded the process; deferred actions may have
		// changed its owner frontier in the meantime. Zero ticks never yielded,
		// so its in-memory statement frontier is already the exact program point.
		continuation.execution.control = frontiers.get(runtime.causalOwner)
	}
	if suspension.action != nil {
		causes := canonicalEventIDs(append(append([]gorapide.EventID(nil), continuation.execution.control...), suspension.action.stateCauses...))
		event, err := gorapide.NewDeterministicEvent(gorapide.EventProvenance{
			Profile: CompatibilityProfile, Model: modelDigest, Instance: componentID,
			Action: suspension.action.action, Occurrence: suspension.action.occurrence,
			Causes:  causes,
			Timings: clocks.intervalTimings(componentID, suspension.clock, suspension.start, suspension.deadline),
		}, suspension.action.params)
		if err != nil {
			return err
		}
		if err := addStateOperationSuccessors(suspension.action.stateOperations, string(event.ID)); err != nil {
			return err
		}
		continuation.execution.generated = append(continuation.execution.generated, generatedRuleOutput{
			localID: suspension.action.localID, event: event, causes: causes,
			stateSnapshot: cloneStateCells(cells),
		})
		continuation.execution.control = []gorapide.EventID{event.ID}
		invocation, err := selectGeneratedInterruptHandler(
			&continuation.execution, event, suspension.action.match, functionRuntime,
		)
		if err != nil {
			return err
		}
		if invocation != nil {
			continuation.execution.pendingInterrupt = invocation
		}
		(*firings)[continuation.firingIndex].Suspensions[suspension.record].EventID = string(event.ID)
	}
	runtime.suspension = nil
	return nil
}

func flushProcessContinuation(
	runtime *processRuntime,
	continuation *processBodyContinuation,
	poset *gorapide.Poset,
	depths map[gorapide.EventID]uint64,
	queue *executionQueue,
	seenItems map[string]bool,
	clocks *deterministicClockKernel,
	frontiers *causalFrontierRegistry,
	moduleState moduleStateRuntime,
	stateSnapshots stateSnapshotRegistry,
	firings *[]FiringRecord,
) (bool, error) {
	continuation.drainActiveFunctionEffects()
	execution := continuation.rootExecution()
	if execution == nil {
		return false, fmt.Errorf("%w: process %s has no root execution", ErrInvalidDeclarativeProcess, runtime.declaration.ID)
	}
	record := &(*firings)[continuation.firingIndex]
	generated := false
	for _, output := range execution.generated[continuation.flushedEvents:] {
		if err := poset.AddEventWithCause(output.event, output.causes...); err != nil {
			return false, fmt.Errorf("deterministic process %s.%s output %s: %w", runtime.componentID, runtime.declaration.ID, output.localID, err)
		}
		depth := eventDepth(poset, output.event, depths)
		depths[output.event.ID] = depth
		if err := enqueueGeneratedObservationViews(
			output, depth, moduleState, stateSnapshots, queue, seenItems,
		); err != nil {
			return false, err
		}
		if !output.connectionOutput {
			record.Generated = append(record.Generated, GeneratedEventRecord{
				OutputID: output.localID, EventID: string(output.event.ID), Exception: output.exception,
			})
		}
		generated = true
	}
	continuation.flushedEvents = len(execution.generated)

	newActions := execution.scheduled[continuation.flushedActions:]
	clocks.addScheduled(newActions)
	record.Scheduled = append(record.Scheduled, scheduledPlans(newActions)...)
	continuation.flushedActions = len(execution.scheduled)
	canceled := append([]string(nil), execution.canceledSchedules...)
	if execution.initializationFailure != nil {
		canceled = append(canceled, execution.initializationFailure.canceledSchedules...)
	}
	newCanceled := canceled[continuation.flushedCanceledSchedules:]
	record.CanceledSchedules = append(record.CanceledSchedules, newCanceled...)
	continuation.flushedCanceledSchedules = len(canceled)

	newReads := execution.reads[continuation.flushedReads:]
	record.StateReads = append(record.StateReads, qualifyStateReads(runtime.componentID, newReads)...)
	continuation.flushedReads = len(execution.reads)
	newWrites := execution.writes[continuation.flushedWrites:]
	record.StateWrites = append(record.StateWrites, qualifyStateWrites(runtime.componentID, newWrites)...)
	continuation.flushedWrites = len(execution.writes)

	if len(execution.control) != 0 {
		frontiers.set(runtime.causalOwner, execution.control)
	}
	runtime.frontier = frontiers.get(runtime.causalOwner)
	return generated, nil
}

type processContinuationStep struct {
	statement     Statement
	pathPrefix    string
	index         int
	statementPath string
	bindings      pattern.Bindings
	match         pattern.MatchResult
	matchDigest   string
	iterator      bool
	generalFor    bool
	ok            bool
}

func nextProcessContinuationStatement(
	continuation *processBodyContinuation,
) (processContinuationStep, error) {
	for len(continuation.frames) != 0 {
		frameIndex := len(continuation.frames) - 1
		frame := &continuation.frames[frameIndex]
		if frame.iterator != nil {
			return processContinuationStep{iterator: true, ok: true}, nil
		}
		if frame.generalFor != nil {
			return processContinuationStep{generalFor: true, ok: true}, nil
		}
		if frame.next < len(frame.statements) {
			index := frame.next
			frame.next++
			prefix := frame.path
			if frame.loop {
				prefix = frame.loopPath + "/iteration/" + strconv.FormatUint(frame.iteration, 10) + "/"
			}
			frameMatch, frameMatchDigest := processFrameMatch(continuation, frame)
			return processContinuationStep{
				statement: frame.statements[index], pathPrefix: prefix, index: index,
				statementPath: prefix + strconv.Itoa(index), bindings: frame.bindings,
				match: frameMatch, matchDigest: frameMatchDigest, ok: true,
			}, nil
		}
		if !frame.loop {
			if err := completeProcessHandlerFrame(continuation, frame); err != nil {
				return processContinuationStep{}, err
			}
			continuation.frames = continuation.frames[:frameIndex]
			continue
		}
		if len(frame.statements) == 0 {
			return processContinuationStep{}, fmt.Errorf(
				"%w: empty process loop %s cannot make bounded progress", ErrExecutionLimit, frame.loopPath,
			)
		}
		if frame.iteration == ^uint64(0) {
			return processContinuationStep{}, fmt.Errorf(
				"%w: process loop %s iteration overflows", ErrExecutionLimit, frame.loopPath,
			)
		}
		frame.iteration++
		frame.next = 0
	}
	return processContinuationStep{}, nil
}

func evaluateProcessContinuationBoolean(
	continuation *processBodyContinuation,
	condition RuleValue,
	statementPath, purpose string,
	bindings pattern.Bindings,
	cells map[string]*stateCell,
) (bool, error) {
	evaluated, err := evaluateClosedRuleValue(
		continuation.rule.ID+" statement "+statementPath+" "+purpose,
		condition, bindings, cells,
	)
	if err != nil {
		return false, err
	}
	selected, ok := evaluated.value.(bool)
	if !ok {
		return false, fmt.Errorf("%w: statement %s %s evaluated to %T",
			ErrInvalidDeclarativeStatement, statementPath, purpose, evaluated.value)
	}
	if err := incorporateEvaluatedStateReads(&continuation.execution, evaluated.reads, evaluated.causes); err != nil {
		return false, err
	}
	return selected, nil
}

func processContinuationCaseAlternatives(
	continuation *processBodyContinuation,
	statement Statement,
	statementPath string,
	bindings pattern.Bindings,
	cells map[string]*stateCell,
) ([]int, error) {
	value, err := evaluateClosedRuleValue(
		continuation.rule.ID+" statement "+statementPath+" case expression",
		statement.caseValue, bindings, cells,
	)
	if err != nil {
		return nil, err
	}
	if err := incorporateEvaluatedStateReads(&continuation.execution, value.reads, value.causes); err != nil {
		return nil, err
	}
	eligible := make([]int, 0, len(statement.caseAlts))
	for alternativeIndex, alternative := range statement.caseAlts {
		matched := false
		for choiceIndex, choice := range alternative.choices {
			choiceOwner := fmt.Sprintf("%s statement %s alternative %d choice %d",
				continuation.rule.ID, statementPath, alternativeIndex, choiceIndex)
			switch choice.kind {
			case caseValueChoiceKind:
				candidate, err := evaluateClosedRuleValue(
					choiceOwner, choice.value, bindings, cells,
				)
				if err != nil {
					return nil, err
				}
				if err := incorporateEvaluatedStateReads(&continuation.execution, candidate.reads, candidate.causes); err != nil {
					return nil, err
				}
				matched, err = gorapide.CanonicalValuesEqual(value.value, candidate.value)
				if err != nil {
					return nil, err
				}
			case caseRangeChoiceKind:
				first, err := evaluateClosedRuleValue(
					choiceOwner+" range first", choice.first, bindings, cells,
				)
				if err != nil {
					return nil, err
				}
				last, err := evaluateClosedRuleValue(
					choiceOwner+" range last", choice.last, bindings, cells,
				)
				if err != nil {
					return nil, err
				}
				if err := incorporateEvaluatedStateReads(&continuation.execution, first.reads, first.causes); err != nil {
					return nil, err
				}
				if err := incorporateEvaluatedStateReads(&continuation.execution, last.reads, last.causes); err != nil {
					return nil, err
				}
				selectorInteger, selectorOK := value.value.(int64)
				firstInteger, firstOK := first.value.(int64)
				lastInteger, lastOK := last.value.(int64)
				if !selectorOK || !firstOK || !lastOK {
					return nil, fmt.Errorf("%w: %s range evaluated to %T, %T, %T",
						ErrInvalidDeclarativeStatement, choiceOwner, value.value, first.value, last.value)
				}
				matched = firstInteger <= lastInteger && selectorInteger >= firstInteger && selectorInteger <= lastInteger
			default:
				return nil, fmt.Errorf("%w: %s has choice kind %q", ErrInvalidDeclarativeStatement, choiceOwner, choice.kind)
			}
			if matched {
				break
			}
		}
		if matched {
			eligible = append(eligible, alternativeIndex)
			if statement.caseMode == CaseElseMode {
				break
			}
		}
	}
	if statement.caseMode == CaseXorMode && len(eligible) > 1 {
		return nil, fmt.Errorf("%w: statement %s alternatives %d and %d",
			ErrCaseChoiceConflict, statementPath, eligible[0], eligible[1])
	}
	return eligible, nil
}

func configureProcessHandlerBody(
	continuation *processBodyContinuation,
	frameIndex int,
	statements []Statement,
	match pattern.MatchResult,
	matchDigest, pathSuffix string,
	handled *raisedExceptionOccurrence,
) error {
	if continuation == nil || frameIndex < 0 || frameIndex >= len(continuation.frames) {
		return fmt.Errorf("%w: process handler frame is unavailable", ErrInvalidExceptionHandler)
	}
	frame := &continuation.frames[frameIndex]
	controller := frame.handler
	if controller == nil || controller.phase != processHandlerProtectedPhase {
		return fmt.Errorf("%w: process handler frame is not protected", ErrInvalidExceptionHandler)
	}
	if err := deactivateProcessInterruptHandler(&continuation.execution, controller.id); err != nil {
		return err
	}
	controller.phase = processHandlerBodyPhase
	controller.handled = handled
	frame.statements = copyStatements(statements)
	frame.path = controller.path + pathSuffix
	frame.next = 0
	frame.loop = false
	frame.loopPath = ""
	frame.iteration = 0
	frame.bindings = append(pattern.Bindings(nil), match.Bindings...)
	frame.match = cloneProcessMatch(match)
	frame.matchDigest = matchDigest
	frame.iterator = nil
	frame.generalFor = nil
	if handled != nil {
		continuation.execution.handledExceptions = append(
			continuation.execution.handledExceptions, handled,
		)
	}
	return nil
}

func transferProcessExceptionToHandler(
	continuation *processBodyContinuation,
	componentID, modelDigest string,
	functionRuntime *functionExecutionRuntime,
) error {
	if continuation == nil || continuation.execution.raised == nil {
		return fmt.Errorf("%w: process exception transfer has no occurrence", ErrInvalidExceptionHandler)
	}
	raised := continuation.execution.raised
	for index := len(continuation.frames) - 1; index >= 0; index-- {
		controller := continuation.frames[index].handler
		if controller == nil || controller.phase != processHandlerProtectedPhase {
			continue
		}
		statements, match, handled, err := selectExceptionHandler(
			controller.handler, raised, controller.outerMatch,
		)
		if err != nil {
			return err
		}
		if !handled {
			continue
		}
		matchDigest, err := pattern.SemanticDigestMatches([]pattern.MatchResult{match})
		if err != nil {
			return err
		}
		if err := releaseProcessIteratorFrames(
			continuation, index+1, componentID, modelDigest, functionRuntime,
		); err != nil {
			return err
		}
		continuation.frames = continuation.frames[:index+1]
		continuation.execution.raised = nil
		return configureProcessHandlerBody(
			continuation, index, statements, match, matchDigest, "h/", raised,
		)
	}
	if err := releaseProcessIteratorFrames(
		continuation, 0, componentID, modelDigest, functionRuntime,
	); err != nil {
		return err
	}
	continuation.frames = nil
	return nil
}

func processFramesContainHandler(frames []processStatementFrame, targetID string) bool {
	for index := len(frames) - 1; index >= 0; index-- {
		controller := frames[index].handler
		if controller != nil && controller.phase == processHandlerProtectedPhase &&
			controller.id == targetID {
			return true
		}
	}
	return false
}

func activeProcessHandlerControllers(
	continuation *processBodyContinuation,
) []*processHandlerContinuation {
	if continuation == nil {
		return nil
	}
	result := make([]*processHandlerContinuation, 0)
	appendFrames := func(frames []processStatementFrame) {
		for index := len(frames) - 1; index >= 0; index-- {
			controller := frames[index].handler
			if controller != nil && controller.phase == processHandlerProtectedPhase &&
				handlerHasInterruptChoice(controller.handler) {
				result = append(result, controller)
			}
		}
	}
	appendFrames(continuation.frames)
	for index := len(continuation.functionCalls) - 1; index >= 0; index-- {
		appendFrames(continuation.functionCalls[index].callerFrames)
	}
	return result
}

func unwindProcessFunctionsForInterrupt(
	continuation *processBodyContinuation,
	targetID, modelDigest string,
	functionRuntime *functionExecutionRuntime,
) error {
	for !processFramesContainHandler(continuation.frames, targetID) {
		if len(continuation.functionCalls) == 0 {
			return fmt.Errorf(
				"%w: interrupt target %q is outside the active process stack",
				ErrInvalidExceptionHandler, targetID,
			)
		}
		if err := releaseProcessIteratorFrames(
			continuation, 0, continuation.componentID, modelDigest, functionRuntime,
		); err != nil {
			return err
		}
		index := len(continuation.functionCalls) - 1
		frame := &continuation.functionCalls[index]
		invocation := continuation.execution.pendingInterrupt
		if invocation == nil {
			return fmt.Errorf("%w: cross-call interrupt invocation is missing", ErrInvalidExceptionHandler)
		}
		if err := releaseFunctionModuleLocals(
			modelDigest, frame.call.localModules, functionRuntime, &continuation.execution,
		); err != nil {
			return err
		}
		drainResumableFunctionExecution(
			frame.call, &continuation.execution, &frame.callerExecution,
		)
		frame.callerExecution.pendingOperations = continuation.execution.pendingOperations
		frame.callerExecution.pendingInterrupt = invocation
		callerComponentID := frame.callerComponentID
		callerRule := frame.callerRule
		callerCandidate := frame.callerCandidate
		callerMatchDigest := frame.callerMatchDigest
		callerFrames := frame.callerFrames
		callerExecution := frame.callerExecution
		continuation.functionCalls = continuation.functionCalls[:index]
		continuation.componentID = callerComponentID
		continuation.rule = callerRule
		continuation.candidate = callerCandidate
		continuation.matchDigest = callerMatchDigest
		continuation.frames = callerFrames
		continuation.execution = callerExecution
	}
	return nil
}

func transferProcessInterruptToHandler(
	continuation *processBodyContinuation,
	componentID, modelDigest string,
	functionRuntime *functionExecutionRuntime,
) error {
	if continuation == nil || continuation.execution.pendingInterrupt == nil {
		return fmt.Errorf("%w: process interrupt transfer has no invocation", ErrInvalidExceptionHandler)
	}
	targetID := continuation.execution.pendingInterrupt.targetID
	if !processFramesContainHandler(continuation.frames, targetID) {
		if err := unwindProcessFunctionsForInterrupt(
			continuation, targetID, modelDigest, functionRuntime,
		); err != nil {
			return err
		}
		componentID = continuation.componentID
	}
	invocation := continuation.execution.pendingInterrupt
	markProcessInterruptMatch(continuation, invocation.match)
	for index := len(continuation.frames) - 1; index >= 0; index-- {
		controller := continuation.frames[index].handler
		if controller == nil || controller.phase != processHandlerProtectedPhase ||
			controller.id != targetID {
			continue
		}
		matchDigest, err := pattern.SemanticDigestMatches([]pattern.MatchResult{invocation.match})
		if err != nil {
			return err
		}
		if err := releaseProcessIteratorFrames(
			continuation, index+1, componentID, modelDigest, functionRuntime,
		); err != nil {
			return err
		}
		continuation.frames = continuation.frames[:index+1]
		continuation.execution.pendingInterrupt = nil
		return configureProcessHandlerBody(
			continuation, index, invocation.statements, invocation.match,
			matchDigest, "i/", nil,
		)
	}
	return fmt.Errorf("%w: active interrupt target %q was lost", ErrInvalidExceptionHandler, targetID)
}

func applyProcessContinuationControl(
	continuation *processBodyContinuation,
	control statementControl,
	statementPath string,
	componentID, modelDigest string,
	functionRuntime *functionExecutionRuntime,
) error {
	switch control {
	case statementContinue:
		return nil
	case statementExitProcess:
		if err := releaseProcessIteratorFrames(
			continuation, 0, componentID, modelDigest, functionRuntime,
		); err != nil {
			return err
		}
		continuation.execution.exitProcess = true
		continuation.frames = nil
		return nil
	case statementReturnFunction:
		if len(continuation.functionCalls) == 0 {
			return fmt.Errorf("%w: function return escaped process statement %s", ErrInvalidDeclarativeStatement, statementPath)
		}
		if err := releaseProcessIteratorFrames(
			continuation, 0, componentID, modelDigest, functionRuntime,
		); err != nil {
			return err
		}
		continuation.frames = nil
		return nil
	case statementRaiseException:
		return transferProcessExceptionToHandler(
			continuation, componentID, modelDigest, functionRuntime,
		)
	case statementHandleInterrupt:
		return transferProcessInterruptToHandler(
			continuation, componentID, modelDigest, functionRuntime,
		)
	case statementExitLoop, statementNextLoop:
		target := continuation.execution.loopControlDoName
		for index := len(continuation.frames) - 1; index >= 0; index-- {
			if !continuation.frames[index].doControl {
				continue
			}
			if target != "" && continuation.frames[index].doName != target {
				continue
			}
			consumeDoControl(&continuation.execution)
			if !continuation.frames[index].loop {
				if err := releaseProcessIteratorFrames(
					continuation, index, componentID, modelDigest, functionRuntime,
				); err != nil {
					return err
				}
				continuation.frames = continuation.frames[:index]
				return nil
			}
			if control == statementExitLoop {
				if err := releaseProcessIteratorFrames(
					continuation, index, componentID, modelDigest, functionRuntime,
				); err != nil {
					return err
				}
				continuation.frames = continuation.frames[:index]
				return nil
			}
			if err := releaseProcessIteratorFrames(
				continuation, index+1, componentID, modelDigest, functionRuntime,
			); err != nil {
				return err
			}
			frame := &continuation.frames[index]
			if frame.iterator != nil {
				continuation.frames = continuation.frames[:index+1]
				return nil
			}
			if frame.generalFor != nil {
				continuation.frames = continuation.frames[:index+1]
				frame = &continuation.frames[index]
				frame.generalFor.phase = "next"
				return nil
			}
			if frame.iteration == ^uint64(0) {
				return fmt.Errorf("%w: process loop %s iteration overflows", ErrExecutionLimit, frame.loopPath)
			}
			continuation.frames = continuation.frames[:index+1]
			frame = &continuation.frames[index]
			frame.iteration++
			frame.next = 0
			return nil
		}
		if continuation.rule != nil && continuation.rule.allowProcessDoControl &&
			(target == "" || target == continuation.rule.processDoName) {
			if err := releaseProcessIteratorFrames(
				continuation, 0, componentID, modelDigest, functionRuntime,
			); err != nil {
				return err
			}
			consumeDoControl(&continuation.execution)
			if control == statementExitLoop {
				continuation.execution.exitProcess = true
			}
			continuation.frames = nil
			return nil
		}
		return fmt.Errorf("%w: do control escaped process statement %s", ErrInvalidDeclarativeStatement, statementPath)
	default:
		return fmt.Errorf("%w: unknown process statement control %d", ErrInvalidDeclarativeStatement, control)
	}
}

func releaseProcessIteratorFrames(
	continuation *processBodyContinuation,
	first int,
	componentID, modelDigest string,
	functionRuntime *functionExecutionRuntime,
) error {
	if continuation == nil {
		return fmt.Errorf("%w: process continuation is missing", ErrInvalidDeclarativeProcess)
	}
	if first < 0 {
		first = 0
	}
	if first > len(continuation.frames) {
		first = len(continuation.frames)
	}
	for index := len(continuation.frames) - 1; index >= first; index-- {
		frame := &continuation.frames[index]
		if err := completeProcessHandlerFrame(continuation, frame); err != nil {
			return err
		}
		controller := frame.iterator
		if controller == nil {
			continue
		}
		if err := releaseStatementIterator(
			componentID, modelDigest, controller.path, controller.value,
			functionRuntime, &continuation.execution,
		); err != nil {
			return err
		}
	}
	return nil
}

func advanceProcessIteratorFrame(
	continuation *processBodyContinuation,
	componentID, modelDigest string,
	functionRuntime *functionExecutionRuntime,
	cells map[string]*stateCell,
	budget *statementBudget,
) error {
	if continuation == nil || len(continuation.frames) == 0 {
		return fmt.Errorf("%w: process iterator continuation is missing", ErrInvalidDeclarativeStatement)
	}
	frameIndex := len(continuation.frames) - 1
	frame := &continuation.frames[frameIndex]
	controller := frame.iterator
	if controller == nil {
		return fmt.Errorf("%w: process iterator controller is missing", ErrInvalidDeclarativeStatement)
	}
	iteration := controller.iteration
	if iteration == 0 {
		iteration = 1
		controller.iteration = iteration
	}
	if err := budget.consume(); err != nil {
		return err
	}
	more := controller.value.more()
	if err := executeFiniteIteratorProtocolCall(
		componentID, modelDigest, controller.path, iteration,
		"More", "Boolean", more, controller.value, cells, &continuation.execution,
	); err != nil {
		return err
	}
	if !more {
		if err := releaseStatementIterator(
			componentID, modelDigest, controller.path, controller.value,
			functionRuntime, &continuation.execution,
		); err != nil {
			return err
		}
		continuation.frames = continuation.frames[:frameIndex]
		return nil
	}
	if err := budget.consume(); err != nil {
		return err
	}
	item, err := controller.value.item()
	if err != nil {
		return err
	}
	if err := executeFiniteIteratorProtocolCall(
		componentID, modelDigest, controller.path, iteration,
		"Item", controller.value.itemType, item, controller.value, cells, &continuation.execution,
	); err != nil {
		return err
	}
	if controller.iteration == ^uint64(0) {
		return fmt.Errorf("%w: process iterator %s iteration overflows", ErrExecutionLimit, controller.path)
	}
	controller.iteration++
	if len(controller.body) == 0 {
		return nil
	}
	bindings := bindingsWithIteratorValue(frame.bindings, controller.name, item)
	continuation.frames = append(continuation.frames, processStatementFrame{
		statements: controller.body,
		path:       controller.path + "/iteration/" + strconv.FormatUint(iteration, 10) + "/",
		bindings:   bindings, match: cloneProcessMatch(frame.match), matchDigest: frame.matchDigest,
	})
	return nil
}

func advanceProcessGeneralForFrame(
	continuation *processBodyContinuation,
	componentID string,
	component *Component,
	modelDigest string,
	functionRuntime *functionExecutionRuntime,
	cells map[string]*stateCell,
	budget *statementBudget,
) error {
	if continuation == nil || len(continuation.frames) == 0 {
		return fmt.Errorf("%w: process general-for continuation is missing", ErrInvalidDeclarativeStatement)
	}
	frameIndex := len(continuation.frames) - 1
	frame := &continuation.frames[frameIndex]
	controller := frame.generalFor
	if controller == nil {
		return fmt.Errorf("%w: process general-for controller is missing", ErrInvalidDeclarativeStatement)
	}
	iterationMatch, frameMatchDigest := processFrameMatch(continuation, frame)
	iterationPath := controller.path + "/iteration/" + strconv.FormatUint(controller.iteration, 10)
	phase := controller.phase
	var expression ExecutableObjectExpression
	var expressionPath string
	switch phase {
	case "initializer":
		expression = controller.initializer
		expressionPath = controller.path + "/initializer"
	case "test":
		expression = controller.test
		expressionPath = iterationPath + "/test"
	case "next":
		expression = controller.next
		expressionPath = iterationPath + "/next"
	default:
		return fmt.Errorf(
			"%w: process general-for %s has phase %q",
			ErrInvalidDeclarativeStatement, controller.path, controller.phase,
		)
	}
	if expression.kind == ObjectFunctionExpression &&
		(functionCallMaySuspend(componentID, expression.call, functionRuntime) ||
			functionCallNeedsInterruptConnectionContinuation(
				componentID, expression.call, functionRuntime,
			)) {
		controller.phase = phase + "-waiting"
		return beginProcessFunctionContinuation(
			continuation, expression.call, expressionPath, iterationMatch, frameMatchDigest,
			processFunctionResultDestination{
				kind: processFunctionGeneralForResult, generalForFrame: frameIndex,
				generalForPhase: phase,
			},
			modelDigest, functionRuntime, cells, budget,
		)
	}
	value, err := executeExecutableObjectExpression(
		componentID, component, continuation.rule, iterationMatch,
		frameMatchDigest, modelDigest, expressionPath,
		expression, functionRuntime, cells, &continuation.execution, budget,
	)
	if err != nil {
		return err
	}
	if continuation.execution.initializationFailure != nil {
		return applyProcessContinuationControl(
			continuation, statementExitProcess, controller.path,
			componentID, modelDigest, functionRuntime,
		)
	}
	if continuation.execution.raised != nil {
		return applyProcessContinuationControl(
			continuation, statementRaiseException, controller.path,
			componentID, modelDigest, functionRuntime,
		)
	}
	return completeProcessGeneralForExpression(continuation, frameIndex, phase, value)
}

func completeProcessGeneralForExpression(
	continuation *processBodyContinuation,
	frameIndex int,
	phase string,
	value any,
) error {
	if continuation == nil || frameIndex < 0 || frameIndex >= len(continuation.frames) {
		return fmt.Errorf("%w: process general-for result frame is unavailable", ErrInvalidDeclarativeStatement)
	}
	frame := &continuation.frames[frameIndex]
	controller := frame.generalFor
	if controller == nil {
		return fmt.Errorf("%w: process general-for result controller is missing", ErrInvalidDeclarativeStatement)
	}
	if controller.phase != phase && controller.phase != phase+"-waiting" {
		return fmt.Errorf(
			"%w: process general-for %s expected phase %q, found %q",
			ErrInvalidDeclarativeStatement, controller.path, phase, controller.phase,
		)
	}
	switch phase {
	case "initializer":
		controller.phase = "test"
		return nil
	case "test":
		selected, ok := value.(bool)
		if !ok {
			return fmt.Errorf(
				"%w: process general-for %s test evaluated to %T",
				ErrInvalidDeclarativeStatement, controller.path, value,
			)
		}
		if !selected {
			continuation.frames = continuation.frames[:frameIndex]
			return nil
		}
		iterationPath := controller.path + "/iteration/" + strconv.FormatUint(controller.iteration, 10)
		controller.phase = "next"
		if len(controller.body) != 0 {
			continuation.frames = append(continuation.frames, processStatementFrame{
				statements: controller.body, path: iterationPath + "/body/", bindings: frame.bindings,
				match: cloneProcessMatch(frame.match), matchDigest: frame.matchDigest,
			})
		}
		return nil
	case "next":
		if controller.iteration == ^uint64(0) {
			return fmt.Errorf("%w: process general-for %s iteration overflows", ErrExecutionLimit, controller.path)
		}
		controller.iteration++
		controller.phase = "test"
		return nil
	default:
		return fmt.Errorf(
			"%w: process general-for %s cannot complete phase %q",
			ErrInvalidDeclarativeStatement, controller.path, phase,
		)
	}
}

func beginProcessFunctionContinuation(
	continuation *processBodyContinuation,
	call FunctionCall,
	statementPath string,
	match pattern.MatchResult,
	matchDigest string,
	resultDestination processFunctionResultDestination,
	modelDigest string,
	functionRuntime *functionExecutionRuntime,
	cells map[string]*stateCell,
	budget *statementBudget,
) error {
	if continuation == nil || call.ID == "" {
		return fmt.Errorf("%w: process function continuation has no function call", ErrInvalidFunctionImplementation)
	}
	if err := budget.consume(); err != nil {
		return err
	}
	state, child, err := beginResumableFunctionCall(
		continuation.componentID, continuation.rule, match,
		matchDigest, modelDigest, statementPath,
		call, functionRuntime, cells,
		&continuation.execution, budget,
	)
	if err != nil {
		return err
	}
	continuation.functionCalls = append(continuation.functionCalls, processFunctionContinuation{
		call:                state,
		callerComponentID:   continuation.componentID,
		callerRule:          continuation.rule,
		callerCandidate:     continuation.candidate,
		callerMatchDigest:   continuation.matchDigest,
		callerFrames:        continuation.frames,
		callerExecution:     continuation.execution,
		callerStatementPath: statementPath,
		callAuditPath:       continuation.auditStatementPath(statementPath),
		resultDestination:   resultDestination,
	})
	continuation.componentID = state.targetComponentID
	continuation.rule = state.functionRule
	continuation.candidate = awaitCandidate{match: state.functionMatch}
	continuation.matchDigest = string(state.callEvent.ID)
	continuation.execution = child
	continuation.frames = nil
	if child.initializationFailure == nil && child.pendingInterrupt == nil {
		continuation.frames = []processStatementFrame{{
			statements: state.implementation.Statements,
			bindings:   append(pattern.Bindings(nil), state.functionMatch.Bindings...),
			match:      cloneProcessMatch(state.functionMatch), matchDigest: string(state.callEvent.ID),
		}}
	}
	if child.pendingInterrupt != nil {
		return applyProcessContinuationControl(
			continuation, statementHandleInterrupt, statementPath,
			state.targetComponentID, modelDigest, functionRuntime,
		)
	}
	return nil
}

func finishProcessFunctionContinuation(
	runtime *processRuntime,
	continuation *processBodyContinuation,
	modelDigest string,
	functionRuntime *functionExecutionRuntime,
	moduleState moduleStateRuntime,
	firings *[]FiringRecord,
) (bool, error) {
	if continuation == nil || len(continuation.functionCalls) == 0 {
		return false, fmt.Errorf("%w: process function stack is empty", ErrInvalidFunctionImplementation)
	}
	index := len(continuation.functionCalls) - 1
	frame := &continuation.functionCalls[index]
	control := statementContinue
	if continuation.execution.initializationFailure != nil {
		control = statementExitProcess
	} else if continuation.execution.raised != nil {
		control = statementRaiseException
	} else if continuation.execution.returned {
		control = statementReturnFunction
	}
	returned, callControl, err := finishResumableFunctionCall(
		frame.call, &continuation.execution, &frame.callerExecution,
		control, modelDigest, functionRuntime,
	)
	if err != nil {
		return false, err
	}
	returnEventID := gorapide.EventID("")
	if callControl == statementContinue {
		if len(frame.callerExecution.control) != 1 {
			return false, fmt.Errorf(
				"%w: function %q completion has %d return-frontier events",
				ErrInvalidFunctionImplementation, frame.call.implementation.Name,
				len(frame.callerExecution.control),
			)
		}
		returnEventID = frame.callerExecution.control[0]
	}

	callerComponentID := frame.callerComponentID
	callerRule := frame.callerRule
	callerCandidate := frame.callerCandidate
	callerMatchDigest := frame.callerMatchDigest
	callerFrames := frame.callerFrames
	callerExecution := frame.callerExecution
	callerStatementPath := frame.callerStatementPath
	callAuditPath := frame.callAuditPath
	callState := frame.call
	resultDestination := frame.resultDestination
	continuation.functionCalls = continuation.functionCalls[:index]
	continuation.componentID = callerComponentID
	continuation.rule = callerRule
	continuation.candidate = callerCandidate
	continuation.matchDigest = callerMatchDigest
	continuation.frames = callerFrames
	continuation.execution = callerExecution

	if callControl != statementContinue {
		if err := applyProcessContinuationControl(
			continuation, callControl, callerStatementPath,
			callerComponentID, modelDigest, functionRuntime,
		); err != nil {
			return false, err
		}
		return false, nil
	}
	if resultDestination.kind == processFunctionGeneralForResult {
		if err := completeProcessGeneralForExpression(
			continuation, resultDestination.generalForFrame,
			resultDestination.generalForPhase, returned,
		); err != nil {
			return false, err
		}
	} else if callState.call.ResultTarget != "" {
		callerCells := moduleState[callerComponentID]
		if callerCells == nil {
			return false, fmt.Errorf(
				"%w: function %q caller component %q has no state",
				ErrInvalidFunctionImplementation, callState.implementation.Name, callerComponentID,
			)
		}
		reads, writes, err := applyStateAssignments(
			callerRule.ID+" statement "+callerStatementPath+" function result",
			[]StateAssignment{AssignState(callState.call.ResultTarget, LiteralValue(returned))},
			nil, callerCells, continuation.execution.control,
			continuation.execution.pendingOperations,
		)
		if err != nil {
			return false, err
		}
		continuation.execution.reads = append(continuation.execution.reads, reads...)
		continuation.execution.writes = append(continuation.execution.writes, writes...)
		continuation.execution.pendingOperations = canonicalStateOperationReferences(append(
			continuation.execution.pendingOperations, stateOperationReferences(reads, writes)...,
		))
		for _, write := range writes {
			for _, cause := range write.Causes {
				continuation.execution.control = append(
					continuation.execution.control, gorapide.EventID(cause),
				)
			}
		}
		continuation.execution.control = canonicalEventIDs(continuation.execution.control)
	}

	switchID := processFunctionSwitchID(
		runtime.causalOwner, callState.callEvent.ID, returnEventID,
	)
	runtime.switchYield = &processSwitchYield{id: switchID}
	record := ProcessSwitchRecord{
		SwitchID: switchID, Kind: "function-completion",
		Statement: callAuditPath, CallEventID: string(callState.callEvent.ID),
		ReturnEventID: string(returnEventID),
	}
	(*firings)[continuation.firingIndex].Switches = append(
		(*firings)[continuation.firingIndex].Switches, record,
	)
	return true, nil
}

func abortProcessFunctionContinuations(
	runtime *processRuntime,
	exceptionEventID gorapide.EventID,
	modelDigest string,
	functionRuntime *functionExecutionRuntime,
) error {
	if runtime == nil || runtime.continuation == nil || exceptionEventID == "" {
		return fmt.Errorf("%w: process function cleanup state is incomplete", ErrInvalidFunctionImplementation)
	}
	continuation := runtime.continuation
	continuation.execution.control = []gorapide.EventID{exceptionEventID}
	for len(continuation.functionCalls) != 0 {
		if err := releaseProcessIteratorFrames(
			continuation, 0, continuation.componentID, modelDigest, functionRuntime,
		); err != nil {
			return err
		}
		index := len(continuation.functionCalls) - 1
		frame := &continuation.functionCalls[index]
		if err := releaseFunctionModuleLocals(
			modelDigest, frame.call.localModules, functionRuntime, &continuation.execution,
		); err != nil {
			return err
		}
		drainResumableFunctionExecution(
			frame.call, &continuation.execution, &frame.callerExecution,
		)
		frame.callerExecution.pendingOperations = continuation.execution.pendingOperations
		callerComponentID := frame.callerComponentID
		callerRule := frame.callerRule
		callerCandidate := frame.callerCandidate
		callerMatchDigest := frame.callerMatchDigest
		callerFrames := frame.callerFrames
		callerExecution := frame.callerExecution
		continuation.functionCalls = continuation.functionCalls[:index]
		continuation.componentID = callerComponentID
		continuation.rule = callerRule
		continuation.candidate = callerCandidate
		continuation.matchDigest = callerMatchDigest
		continuation.frames = callerFrames
		continuation.execution = callerExecution
		continuation.execution.control = []gorapide.EventID{exceptionEventID}
	}
	if err := releaseProcessIteratorFrames(
		continuation, 0, continuation.componentID, modelDigest, functionRuntime,
	); err != nil {
		return err
	}
	continuation.frames = nil
	return nil
}

func runProcessBodyContinuation(
	runtime *processRuntime,
	modelDigest string,
	functionRuntime *functionExecutionRuntime,
	statementSteps *statementBudget,
	clocks *deterministicClockKernel,
	frontiers *causalFrontierRegistry,
	poset *gorapide.Poset,
	depths map[gorapide.EventID]uint64,
	queue *executionQueue,
	seenItems map[string]bool,
	moduleState moduleStateRuntime,
	stateSnapshots stateSnapshotRegistry,
	firings *[]FiringRecord,
) (bool, bool, error) {
	continuation := runtime.continuation
	if continuation == nil {
		return false, false, fmt.Errorf("%w: process %s has no continuation", ErrInvalidDeclarativeProcess, runtime.declaration.ID)
	}
	if runtime.switchYield != nil {
		runtime.switchYield = nil
	}
	for {
		componentID := continuation.componentID
		activeComponent := functionRuntime.components[componentID]
		activeCells := moduleState[componentID]
		if activeComponent == nil || activeCells == nil {
			return false, false, fmt.Errorf(
				"%w: process %s active component %q is unavailable",
				ErrInvalidDeclarativeProcess, runtime.declaration.ID, componentID,
			)
		}
		if runtime.suspension != nil {
			resumedStatement := runtime.suspension.statement
			if err := resumeProcessSuspension(
				runtime, continuation, componentID, modelDigest, functionRuntime, clocks,
				frontiers, firings, activeCells,
			); err != nil {
				return false, false, err
			}
			if continuation.execution.pendingInterrupt != nil {
				if err := applyProcessContinuationControl(
					continuation, statementHandleInterrupt, resumedStatement,
					componentID, modelDigest, functionRuntime,
				); err != nil {
					return false, false, err
				}
				continue
			}
		}

		step, err := nextProcessContinuationStatement(continuation)
		if err != nil {
			return false, false, err
		}
		if !step.ok {
			if len(continuation.functionCalls) != 0 {
				yielded, err := finishProcessFunctionContinuation(
					runtime, continuation, modelDigest, functionRuntime, moduleState, firings,
				)
				if err != nil {
					return false, false, err
				}
				if yielded {
					break
				}
				continue
			}
			break
		}
		if step.iterator {
			if err := advanceProcessIteratorFrame(
				continuation, componentID, modelDigest, functionRuntime, activeCells, statementSteps,
			); err != nil {
				return false, false, err
			}
			continue
		}
		if step.generalFor {
			if err := advanceProcessGeneralForFrame(
				continuation, componentID, activeComponent, modelDigest,
				functionRuntime, activeCells, statementSteps,
			); err != nil {
				return false, false, err
			}
			continue
		}
		statement := step.statement
		statementPath := step.statementPath
		if statementSuspendsProcess(statement) {
			if err := startProcessSuspension(
				runtime, continuation, activeComponent, componentID, modelDigest,
				statement, statementPath, continuation.auditStatementPath(statementPath),
				step.match, step.matchDigest, clocks, activeCells, statementSteps, firings,
			); err != nil {
				return false, false, err
			}
			if runtime.suspension.ready {
				// A zero-duration form completes in this same process turn. This
				// preserves program order without inventing a scheduler yield.
				continue
			}
			break
		}

		switch statement.kind {
		case FunctionCallStatement:
			if functionCallMaySuspend(componentID, statement.functionCall, functionRuntime) ||
				functionCallNeedsInterruptConnectionContinuation(
					componentID, statement.functionCall, functionRuntime,
				) {
				if err := beginProcessFunctionContinuation(
					continuation, statement.functionCall, statementPath,
					step.match, step.matchDigest,
					processFunctionResultDestination{}, modelDigest, functionRuntime,
					activeCells, statementSteps,
				); err != nil {
					return false, false, err
				}
				continue
			}
			control, err := executeRuleStatementListFrom(
				componentID, activeComponent, continuation.rule,
				step.match, step.matchDigest, modelDigest,
				[]Statement{statement}, functionRuntime, activeCells,
				step.pathPrefix, step.index, &continuation.execution, statementSteps,
			)
			if err != nil {
				return false, false, err
			}
			if err := applyProcessContinuationControl(
				continuation, control, statementPath, componentID, modelDigest, functionRuntime,
			); err != nil {
				return false, false, err
			}
		case DoBlockStatementKind:
			if err := statementSteps.consume(); err != nil {
				return false, false, err
			}
			continuation.frames = append(continuation.frames, processStatementFrame{
				statements: statement.handledBody, path: statementPath + "d/",
				doControl: true, doName: statement.doName, bindings: step.bindings,
				match: cloneProcessMatch(step.match), matchDigest: step.matchDigest,
			})
		case HandlerBlockStatementKind:
			if err := statementSteps.consume(); err != nil {
				return false, false, err
			}
			activationID := continuation.execution.owner + "\x00" +
				continuation.auditStatementPath(statementPath)
			controller := &processHandlerContinuation{
				id: activationID, owner: componentID, path: statementPath, handler: statement.handler,
				outerMatch: cloneProcessMatch(step.match), outerMatchDigest: step.matchDigest,
				phase:            processHandlerProtectedPhase,
				activationEvents: processKnownEventIDs(continuation, poset),
			}
			continuation.execution.interruptHandlers = append(
				continuation.execution.interruptHandlers,
				activeInterruptHandler{
					id: activationID, owner: componentID, processOwned: true,
					handler:    statement.handler,
					outerMatch: cloneProcessMatch(step.match), outerSet: true,
				},
			)
			continuation.frames = append(continuation.frames, processStatementFrame{
				statements: statement.handledBody, path: statementPath + "b/",
				doControl: statement.doControl, doName: statement.doName,
				bindings: append(pattern.Bindings(nil), step.bindings...),
				match:    cloneProcessMatch(step.match), matchDigest: step.matchDigest,
				handler: controller,
			})
		case IfStatementKind:
			if err := statementSteps.consume(); err != nil {
				return false, false, err
			}
			selected, err := evaluateProcessContinuationBoolean(
				continuation, statement.condition, statementPath, "condition", step.bindings, activeCells,
			)
			if err != nil {
				return false, false, err
			}
			branch := statement.elseBranch
			branchName := "else/"
			if selected {
				branch = statement.thenBranch
				branchName = "then/"
			}
			if len(branch) != 0 {
				continuation.frames = append(continuation.frames, processStatementFrame{
					statements: branch, path: statementPath + "/" + branchName,
					bindings: step.bindings, match: cloneProcessMatch(step.match), matchDigest: step.matchDigest,
				})
			}
		case LoopStatementKind:
			if err := statementSteps.consume(); err != nil {
				return false, false, err
			}
			continuation.frames = append(continuation.frames, processStatementFrame{
				statements: statement.loopBody, doControl: true,
				loop: true, loopPath: statementPath, iteration: 1,
				doName: statement.doName, bindings: step.bindings,
				match: cloneProcessMatch(step.match), matchDigest: step.matchDigest,
			})
		case ForStatementKind:
			if err := statementSteps.consume(); err != nil {
				return false, false, err
			}
			iterationMatch := cloneProcessMatch(step.match)
			iterator, err := initializeStatementIterator(
				componentID, continuation.rule, iterationMatch,
				step.matchDigest, modelDigest, statementPath,
				statement, functionRuntime, activeCells, &continuation.execution,
			)
			if err != nil {
				return false, false, err
			}
			continuation.frames = append(continuation.frames, processStatementFrame{
				doControl: true, loop: true, loopPath: statementPath,
				doName: statement.doName, bindings: step.bindings,
				match: cloneProcessMatch(step.match), matchDigest: step.matchDigest,
				iterator: &processIteratorContinuation{
					value: iterator, body: statement.loopBody, name: statement.iteratorName,
					path: statementPath, iteration: 1,
				},
			})
		case GeneralForStatementKind:
			if err := statementSteps.consume(); err != nil {
				return false, false, err
			}
			continuation.frames = append(continuation.frames, processStatementFrame{
				doControl: true, loop: true, loopPath: statementPath,
				doName: statement.doName, bindings: step.bindings,
				match: cloneProcessMatch(step.match), matchDigest: step.matchDigest,
				generalFor: &processGeneralForContinuation{
					initializer: statement.forInitial, test: statement.forTest, next: statement.forNext,
					body: statement.loopBody, path: statementPath,
					iteration: 1, phase: "initializer",
				},
			})
		case CaseStatementKind:
			if err := statementSteps.consume(); err != nil {
				return false, false, err
			}
			eligible, err := processContinuationCaseAlternatives(continuation, statement, statementPath, step.bindings, activeCells)
			if err != nil {
				return false, false, err
			}
			if len(eligible) == 0 {
				if len(statement.caseDefault) != 0 {
					continuation.frames = append(continuation.frames, processStatementFrame{
						statements: statement.caseDefault, path: statementPath + "/default/",
						bindings: step.bindings, match: cloneProcessMatch(step.match), matchDigest: step.matchDigest,
					})
				}
				continue
			}
			for eligibleIndex := len(eligible) - 1; eligibleIndex >= 0; eligibleIndex-- {
				alternativeIndex := eligible[eligibleIndex]
				body := statement.caseAlts[alternativeIndex].body
				if len(body) == 0 {
					continue
				}
				continuation.frames = append(continuation.frames, processStatementFrame{
					statements: body, path: fmt.Sprintf("%s/case/%d/", statementPath, alternativeIndex),
					bindings: step.bindings, match: cloneProcessMatch(step.match), matchDigest: step.matchDigest,
				})
			}
		default:
			control, err := executeRuleStatementListFrom(
				componentID, activeComponent, continuation.rule,
				step.match, step.matchDigest, modelDigest,
				[]Statement{statement}, functionRuntime, activeCells, step.pathPrefix, step.index,
				&continuation.execution, statementSteps,
			)
			if err != nil {
				return false, false, err
			}
			if err := applyProcessContinuationControl(
				continuation, control, statementPath, componentID, modelDigest, functionRuntime,
			); err != nil {
				return false, false, err
			}
		}
	}
	completed := len(continuation.frames) == 0 && len(continuation.functionCalls) == 0 &&
		runtime.suspension == nil && runtime.switchYield == nil
	if completed {
		if err := releasePatternModuleBindings(
			modelDigest, continuation.moduleBindings, functionRuntime, &continuation.execution,
		); err != nil {
			return false, false, err
		}
		continuation.moduleBindings = nil
	}
	generated, err := flushProcessContinuation(
		runtime, continuation, poset, depths, queue, seenItems, clocks,
		frontiers, moduleState, stateSnapshots, firings,
	)
	if err != nil {
		return false, false, err
	}
	if completed && continuation.execution.initializationFailure != nil {
		failure := continuation.execution.initializationFailure
		terminationContext := processTerminationContext{
			modelDigest: modelDigest, functionRuntime: functionRuntime,
			frontiers: frontiers, clocks: clocks, poset: poset, depths: depths,
			queue: queue, seenItems: seenItems, moduleState: moduleState,
			stateSnapshots: stateSnapshots, statementSteps: statementSteps, firings: firings,
		}
		if _, err := finalizeFailedModuleInitializationChain(failure, terminationContext); err != nil {
			return false, false, err
		}
		if !runtime.terminated {
			if err := completeProcessesForModuleException(
				[]*processRuntime{runtime}, runtime, failure.raised.event.ID, terminationContext,
			); err != nil {
				return false, false, err
			}
		}
	} else if completed && continuation.execution.raised != nil {
		if err := terminateModuleProcesses(
			runtime, nil, continuation.execution.raised,
			processTerminationContext{
				modelDigest: modelDigest, functionRuntime: functionRuntime,
				frontiers: frontiers, clocks: clocks, poset: poset, depths: depths,
				queue: queue, seenItems: seenItems, moduleState: moduleState,
				stateSnapshots: stateSnapshots, statementSteps: statementSteps, firings: firings,
			},
		); err != nil {
			return false, false, err
		}
	}
	return completed, generated, nil
}

func completeProcessBodyContinuation(runtime *processRuntime, frontiers *causalFrontierRegistry) {
	continuation := runtime.continuation
	if continuation == nil {
		return
	}
	if continuation.execution.initializationFailure != nil {
		if !runtime.terminated {
			runtime.state = ""
			runtime.terminated = true
			runtime.completion = "exception"
			runtime.exceptionEventID = continuation.execution.initializationFailure.raised.event.ID
		}
	} else if continuation.execution.raised != nil {
		runtime.state = ""
		runtime.terminated = true
		runtime.completion = "exception"
		runtime.exceptionEventID = continuation.execution.raised.event.ID
	} else if continuation.execution.exitProcess || continuation.candidate.alternative.Next == "" {
		runtime.state = ""
		runtime.terminated = true
		runtime.completion = "normal"
	} else {
		runtime.state = continuation.candidate.alternative.Next
		runtime.activation++
	}
	runtime.frontier = frontiers.get(runtime.causalOwner)
	runtime.pendingOperations = continuation.execution.pendingOperations
	runtime.continuation = nil
}

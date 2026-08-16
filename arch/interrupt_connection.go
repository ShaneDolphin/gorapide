package arch

import (
	"container/heap"
	"fmt"
	"reflect"
	"sort"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/pattern"
)

// generationTimeConnectionState is the observer-side state retained by one
// active action-handler lifetime while it spans synchronous calls. A process
// continuation may additionally retain the same state across suspension.
// It contains only language events, visible observations, and causal state;
// no host scheduling or arrival order enters the closure.
type generationTimeConnectionState struct {
	outputs             map[gorapide.EventID]generatedRuleOutput
	closedViews         map[string]*gorapide.Event
	observedOccurrences map[gorapide.EventID]bool
	architectureSeen    gorapide.EventSet
	architectureKeys    map[string]bool
	observed            map[string]gorapide.EventSet
	observedKeys        map[string]map[string]bool
}

func newGenerationTimeConnectionState() *generationTimeConnectionState {
	return &generationTimeConnectionState{
		outputs:             make(map[gorapide.EventID]generatedRuleOutput),
		closedViews:         make(map[string]*gorapide.Event),
		observedOccurrences: make(map[gorapide.EventID]bool),
		architectureKeys:    make(map[string]bool),
		observed:            make(map[string]gorapide.EventSet),
		observedKeys:        make(map[string]map[string]bool),
	}
}

func hasGenerationAwareInterruptHandler(handlers []activeInterruptHandler) bool {
	for _, active := range handlers {
		if active.processOwned || active.generationAware {
			return true
		}
	}
	return false
}

func inheritGenerationTimeConnectionState(parent, child *statementExecution) {
	if parent == nil || child == nil || !hasGenerationAwareInterruptHandler(child.interruptHandlers) {
		return
	}
	shareGenerationTimeConnectionState(parent, child)
}

// retainInitializerGenerationTimeConnectionState preserves the synchronous
// caller prefix even when a fresh initializer has not activated a handler yet.
// The initializer may activate one before a nested New; at that later point its
// own Start and every still-unflushed caller occurrence must already be
// available to the generation-time working poset.
func retainInitializerGenerationTimeConnectionState(parent, child *statementExecution) {
	if parent == nil || child == nil || !child.initializationOwned {
		return
	}
	shareGenerationTimeConnectionState(parent, child)
}

// retainSynchronousGenerationTimeConnectionState carries an unflushed caller
// prefix into an ordinary function frame. A later nested initializer may
// activate an action handler even when no handler was active at function entry.
// Retaining this internal state does not make any handler eligible by itself.
func retainSynchronousGenerationTimeConnectionState(parent, child *statementExecution) {
	if parent == nil || child == nil {
		return
	}
	shareGenerationTimeConnectionState(parent, child)
}

func shareGenerationTimeConnectionState(parent, child *statementExecution) {
	if parent.generationConnections == nil {
		parent.generationConnections = newGenerationTimeConnectionState()
	}
	parent.generationConnections.registerExecution(parent)
	child.generationConnections = parent.generationConnections
}

func (state *generationTimeConnectionState) registerExecution(execution *statementExecution) {
	if state == nil || execution == nil {
		return
	}
	for _, output := range execution.generated {
		state.registerOutput(output)
	}
}

func (state *generationTimeConnectionState) registerOutput(output generatedRuleOutput) {
	if state == nil || output.event == nil || output.event.ID == "" {
		return
	}
	state.outputs[output.event.ID] = output
}

func (state *generationTimeConnectionState) addArchitectureView(event *gorapide.Event) {
	if state == nil || event == nil {
		return
	}
	key := generationTimeViewKey(event)
	if state.architectureKeys[key] {
		return
	}
	state.architectureKeys[key] = true
	state.architectureSeen = append(state.architectureSeen, event.Snapshot())
}

func (state *generationTimeConnectionState) addModuleView(owner string, event *gorapide.Event) {
	if state == nil || owner == "" || event == nil {
		return
	}
	if state.observedKeys[owner] == nil {
		state.observedKeys[owner] = make(map[string]bool)
	}
	key := generationTimeViewKey(event)
	if state.observedKeys[owner][key] {
		return
	}
	state.observedKeys[owner][key] = true
	state.observed[owner] = append(state.observed[owner], event.Snapshot())
}

func (state *generationTimeConnectionState) mergeObservedRuntime(runtime *functionExecutionRuntime) {
	if state == nil || runtime == nil {
		return
	}
	if runtime.architectureSeen != nil {
		for _, event := range *runtime.architectureSeen {
			state.addArchitectureView(event)
		}
	}
	for owner, events := range runtime.observed {
		for _, event := range events {
			state.addModuleView(owner, event)
		}
	}
	for eventID, observed := range runtime.observedOccurrences {
		if observed {
			state.observedOccurrences[eventID] = true
		}
	}
}

// selectGeneratedInterruptHandler performs the connection closure required at
// an action's exact generation point while an interrupt handler is active.
// Rapide action generation is not an invented scheduler yield: the
// generated occurrence and its connection closure are materialized before the
// protected computation can continue or return.
func selectGeneratedInterruptHandler(
	execution *statementExecution,
	event *gorapide.Event,
	outer pattern.MatchResult,
	runtime *functionExecutionRuntime,
) (*interruptHandlerInvocation, error) {
	if execution == nil || event == nil {
		return nil, fmt.Errorf("%w: generated interrupt occurrence is incomplete", ErrInvalidExceptionHandler)
	}
	if !hasGenerationAwareInterruptHandler(execution.interruptHandlers) {
		return selectInterruptHandler(execution.interruptHandlers, event, outer)
	}
	if execution.generationConnections == nil {
		execution.generationConnections = newGenerationTimeConnectionState()
	}
	execution.generationConnections.registerExecution(execution)
	views, visiblePoset, err := closeGenerationTimeActionConnections(execution, event, runtime)
	if err != nil {
		return nil, err
	}
	invocation, err := selectInterruptHandlerViewsDeterministic(
		execution.interruptHandlers, views, outer, visiblePoset, runtime.choices,
	)
	if err != nil {
		return nil, err
	}
	if invocation != nil && invocation.eventID != "" {
		execution.control = []gorapide.EventID{invocation.eventID}
	}
	return invocation, nil
}

func closeGenerationTimeActionConnections(
	execution *statementExecution,
	root *gorapide.Event,
	runtime *functionExecutionRuntime,
) (gorapide.EventSet, *gorapide.Poset, error) {
	if runtime == nil || runtime.model == nil || runtime.poset == nil {
		return nil, nil, fmt.Errorf(
			"%w: generation-time connection runtime is unavailable",
			ErrInvalidExceptionHandler,
		)
	}
	if execution == nil || execution.generationConnections == nil || root == nil {
		return nil, nil, fmt.Errorf(
			"%w: generation-time connection state is unavailable",
			ErrInvalidExceptionHandler,
		)
	}
	if runtime.connectionFired == nil {
		runtime.connectionFired = make(map[string]bool)
	}
	if runtime.connectionPipe == nil {
		runtime.connectionPipe = make(map[string]gorapide.EventID)
	}

	state := execution.generationConnections
	state.registerExecution(execution)
	state.mergeObservedRuntime(runtime)
	working, err := generationTimeWorkingPoset(runtime.poset, state.outputs)
	if err != nil {
		return nil, nil, err
	}

	queue := &executionQueue{}
	heap.Init(queue)
	seenQueue := make(map[string]bool, len(state.closedViews))
	for key := range state.closedViews {
		seenQueue[key] = true
	}
	newEventIDs := map[gorapide.EventID]bool{root.ID: true}
	newViews := make(gorapide.EventSet, 0)
	newViewKeys := make(map[string]bool)
	enqueueViews := func(event *gorapide.Event) {
		if event == nil {
			return
		}
		for _, view := range event.ObservationViews() {
			enqueueExecutionItem(queue, seenQueue, view, 0)
		}
	}

	outputIDs := make([]gorapide.EventID, 0, len(state.outputs))
	for eventID := range state.outputs {
		outputIDs = append(outputIDs, eventID)
	}
	sort.Slice(outputIDs, func(left, right int) bool { return outputIDs[left] < outputIDs[right] })
	for _, eventID := range outputIDs {
		output := state.outputs[eventID]
		if output.event == nil {
			continue
		}
		if eventID == root.ID {
			enqueueViews(output.event)
			continue
		}
		// Events generated before this exact action are visible antecedents for
		// compound patterns, but they are not re-fired as if generated after the
		// current handler activation.
		for _, view := range output.event.ObservationViews() {
			key := generationTimeViewKey(view)
			if state.closedViews[key] == nil {
				observeGenerationTimeView(state, view, working, runtime)
			}
			seenQueue[key] = true
		}
	}
	if queue.Len() == 0 {
		return nil, nil, fmt.Errorf(
			"%w: generated occurrence %s has no unobserved view",
			ErrInvalidExceptionHandler, root.ID,
		)
	}

	for queue.Len() != 0 {
		current, err := popReadyExecutionItemDomain(
			queue, working, state.observedOccurrences, runtime.choices,
			"generation-time-event-observation",
		)
		if err != nil {
			return nil, nil, err
		}
		view := current.event
		moduleObservers := observeGenerationTimeView(state, view, working, runtime)
		if newEventIDs[view.ID] {
			key := generationTimeViewKey(view)
			if !newViewKeys[key] {
				newViewKeys[key] = true
				newViews = append(newViews, view.Snapshot())
			}
		}

		for _, connection := range runtime.connections {
			if connection == nil {
				continue
			}
			visiblePool := state.architectureSeen
			if connection.Scope == ArchitectureConnectionScope {
				if !architectureConnectionSourceMatches(connection, runtime.model, view) ||
					(connection.From != "*" && connection.From != view.Source) {
					continue
				}
			} else {
				if !interfaceMatchesModuleAction(runtime.components[view.Source], view.Name, view.Params) ||
					!moduleObservers[connection.From] {
					continue
				}
				visiblePool = state.observed[connection.From]
			}
			visible := connectionVisibleEvents(connection, visiblePool, runtime.model, runtime.components)
			if len(visible) == 0 {
				continue
			}
			matches := []pattern.MatchResult{{Events: gorapide.EventSet{view.Snapshot()}}}
			if connection.Trigger != nil {
				trigger := connection.Trigger
				if connection.Scope == ModuleConnectionScope && pattern.HasModuleSourceBinding(trigger) {
					trigger, err = pattern.ScopeUnqualifiedEventSources(trigger, connection.From)
					if err != nil {
						return nil, nil, fmt.Errorf(
							"connection %q generation-time trigger source scope: %w",
							connection.ID, err,
						)
					}
				}
				matches, err = pattern.MatchWithBindings(
					trigger, newObservationView(visible, working, runtime),
				)
				if err != nil {
					return nil, nil, fmt.Errorf("connection %q generation-time trigger: %w", connection.ID, err)
				}
			}
			candidates, err := canonicalConnectionMatches(matches, view.ID)
			if err != nil {
				return nil, nil, fmt.Errorf("connection %q generation-time matches: %w", connection.ID, err)
			}
			for _, candidate := range candidates {
				anchor := view
				if len(candidate.match.Events) == 1 {
					anchor = candidate.match.Events[0]
				}
				parameters, err := connection.resolveClosedParameters(anchor, candidate.match.Bindings)
				if err != nil {
					return nil, nil, fmt.Errorf("connection %q generation-time parameters: %w", connection.ID, err)
				}
				for _, targetID := range deterministicTargets(connection, anchor, runtime.model) {
					firingKey := connection.ID + "\x00" + targetID + "\x00" + candidate.key
					if runtime.connectionFired[firingKey] {
						continue
					}
					if runtime.firings != nil && uint64(len(*runtime.firings)) >= runtime.maxFirings {
						return nil, nil, fmt.Errorf("%w: max_firings=%d", ErrExecutionLimit, runtime.maxFirings)
					}
					if err := validateConnectionTargetAction(
						runtime.model, runtime.components, connection, targetID,
						connection.outputAction(view), parameters,
					); err != nil {
						return nil, nil, fmt.Errorf("connection %q target %q: %w", connection.ID, targetID, err)
					}

					stateKey := connection.ID + "\x00" + targetID
					previous := runtime.connectionPipe[stateKey]
					timings := runtime.clocks.instantTimings(targetID)
					if connection.Kind == BasicConnection {
						timings = runtime.clocks.missingObservationTimings(anchor, targetID)
					}
					var output *gorapide.Event
					switch connection.Kind {
					case BasicConnection:
						materialized := state.outputs[view.ID]
						if materialized.event == nil {
							return nil, nil, fmt.Errorf(
								"%w: connection %q lost generated occurrence %s",
								ErrInvalidExceptionHandler, connection.ID, view.ID,
							)
						}
						observation := gorapide.EventObservation{
							Name: connection.outputAction(view), Source: targetID,
							Params: parameters,
						}
						output, err = addGeneratedBasicObservation(
							materialized.event, observation, timings,
						)
						if err == nil {
							_, err = working.AddObservationWithTimings(view.ID, observation, timings)
							state.registerOutput(materialized)
						}
					case PipeConnection, AgentConnection:
						var nextPrevious gorapide.EventID
						output, nextPrevious, err = connection.applyResolvedMatch(
							working, candidate.match, anchor, targetID, previous, parameters, timings,
						)
						if err == nil {
							causes := generationTimeDirectCauseIDs(working, output.ID)
							generated := generatedRuleOutput{
								localID: "connection@" + digestBytes([]byte(firingKey)),
								event:   output, causes: causes,
								stateSnapshot:    cloneStateCells(runtime.state[targetID]),
								connectionOutput: true,
							}
							execution.generated = append(execution.generated, generated)
							state.registerOutput(generated)
							if connection.Kind == PipeConnection {
								runtime.connectionPipe[stateKey] = nextPrevious
							}
						}
					default:
						err = fmt.Errorf("unknown connection kind %d", connection.Kind)
					}
					if err != nil {
						return nil, nil, fmt.Errorf(
							"deterministic generation-time firing %q: %w", connection.ID, err,
						)
					}

					runtime.connectionFired[firingKey] = true
					if runtime.firings != nil {
						*runtime.firings = append(*runtime.firings, FiringRecord{
							Sequence: uint64(len(*runtime.firings) + 1), Transition: "connection",
							ConnectionID: connection.ID, ConnectionKind: connection.Kind.String(),
							ConnectionScope: connection.Scope.String(), TriggerID: string(view.ID),
							TriggerSource: view.Source, TriggerAction: view.Name,
							MatchedEvents: append([]string(nil), candidate.canonical.Events...),
							Bindings:      append([]pattern.CanonicalBinding(nil), candidate.canonical.Bindings...),
							Target:        targetID, ResultID: string(output.ID),
						})
					}
					newEventIDs[output.ID] = true
					enqueueViews(output)
				}
			}
		}
	}
	sort.Slice(newViews, func(left, right int) bool {
		return generationTimeViewKey(newViews[left]) < generationTimeViewKey(newViews[right])
	})
	return newViews, working, nil
}

func generationTimeWorkingPoset(
	base *gorapide.Poset,
	outputs map[gorapide.EventID]generatedRuleOutput,
) (*gorapide.Poset, error) {
	if base == nil {
		return nil, fmt.Errorf("%w: generation-time base poset is unavailable", ErrInvalidExceptionHandler)
	}
	result := gorapide.NewPoset()
	for _, event := range base.TopologicalSort() {
		causes := generationTimeDirectCauseIDs(base, event.ID)
		if err := result.AddEventWithCause(event.Snapshot(), causes...); err != nil {
			return nil, fmt.Errorf("generation-time base occurrence %s: %w", event.ID, err)
		}
	}
	remaining := make(map[gorapide.EventID]generatedRuleOutput, len(outputs))
	for eventID, output := range outputs {
		if _, exists := result.Event(eventID); !exists {
			remaining[eventID] = output
		}
	}
	for len(remaining) != 0 {
		ids := make([]gorapide.EventID, 0, len(remaining))
		for eventID := range remaining {
			ids = append(ids, eventID)
		}
		sort.Slice(ids, func(left, right int) bool { return ids[left] < ids[right] })
		progress := false
		for _, eventID := range ids {
			output := remaining[eventID]
			if output.event == nil {
				return nil, fmt.Errorf(
					"%w: pending generation-time occurrence %s is nil",
					ErrInvalidExceptionHandler, eventID,
				)
			}
			ready := true
			for _, cause := range output.causes {
				if _, exists := result.Event(cause); !exists {
					ready = false
					break
				}
			}
			if !ready {
				continue
			}
			if err := result.AddEventWithCause(output.event.Snapshot(), output.causes...); err != nil {
				return nil, fmt.Errorf("generation-time pending occurrence %s: %w", eventID, err)
			}
			delete(remaining, eventID)
			progress = true
		}
		if !progress {
			unresolved := make([]string, 0, len(remaining))
			for _, eventID := range ids {
				unresolved = append(unresolved, string(eventID))
			}
			return nil, fmt.Errorf(
				"%w: generation-time pending causes are unresolved for %v",
				ErrInvalidExceptionHandler, unresolved,
			)
		}
	}
	return result, nil
}

func generationTimeDirectCauseIDs(poset *gorapide.Poset, eventID gorapide.EventID) []gorapide.EventID {
	causes := poset.DirectCauses(eventID)
	result := make([]gorapide.EventID, len(causes))
	for index, cause := range causes {
		result[index] = cause.ID
	}
	return canonicalEventIDs(result)
}

func observeGenerationTimeView(
	state *generationTimeConnectionState,
	event *gorapide.Event,
	poset *gorapide.Poset,
	runtime *functionExecutionRuntime,
) map[string]bool {
	observers := make(map[string]bool)
	if state == nil || event == nil {
		return observers
	}
	state.addArchitectureView(event)
	observers[event.Source] = true
	if runtime != nil && interfaceMatchesAction(
		runtime.components[event.Source], event.Name, OutAction, event.Params,
	) && runtime.contexts != nil {
		for _, recipient := range runtime.contexts.recipientsAt(event.Source, event.ID, poset) {
			observers[recipient] = true
		}
	}
	for observer := range observers {
		state.addModuleView(observer, event)
	}
	state.closedViews[generationTimeViewKey(event)] = event.Snapshot()
	state.observedOccurrences[event.ID] = true
	return observers
}

func addGeneratedBasicObservation(
	event *gorapide.Event,
	observation gorapide.EventObservation,
	timings []gorapide.EventTiming,
) (*gorapide.Event, error) {
	if event == nil || observation.Name == "" || observation.Source == "" {
		return nil, fmt.Errorf("generated basic observation is incomplete")
	}
	mergedTimings, err := gorapide.CanonicalizeEventTimings(append(
		append([]gorapide.EventTiming(nil), event.Timings...), timings...,
	))
	if err != nil {
		return nil, err
	}
	event.Timings = mergedTimings
	observations := event.EventObservations()
	for _, existing := range observations {
		if existing.Source != observation.Source || existing.Name != observation.Name {
			continue
		}
		left, err := gorapide.CanonicalizeParameters(existing.Params)
		if err != nil {
			return nil, err
		}
		right, err := gorapide.CanonicalizeParameters(observation.Params)
		if err != nil {
			return nil, err
		}
		if !reflect.DeepEqual(left, right) {
			return nil, fmt.Errorf(
				"%w: %s.%s on %s", gorapide.ErrObservationConflict,
				observation.Source, observation.Name, event.ID,
			)
		}
		for _, view := range event.ObservationViews() {
			if view.Source == observation.Source && view.Name == observation.Name {
				return view, nil
			}
		}
	}
	params := make(map[string]any, len(observation.Params))
	for key, value := range observation.Params {
		params[key] = value
	}
	observation.Params = params
	observations = append(observations, observation)
	sort.Slice(observations, func(left, right int) bool {
		if observations[left].Source != observations[right].Source {
			return observations[left].Source < observations[right].Source
		}
		return observations[left].Name < observations[right].Name
	})
	event.Observations = observations
	for _, view := range event.ObservationViews() {
		if view.Source == observation.Source && view.Name == observation.Name {
			return view, nil
		}
	}
	return nil, fmt.Errorf("generated basic observation %s.%s was not retained", observation.Source, observation.Name)
}

func generationTimeViewKey(event *gorapide.Event) string {
	return string(event.ID) + "\x00" + event.Source + "\x00" + event.Name
}

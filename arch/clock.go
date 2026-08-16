package arch

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/ShaneDolphin/gorapide"
)

var (
	ErrInvalidBasicClock     = errors.New("invalid Rapide basic clock declaration")
	ErrClockDeadlineOverflow = errors.New("Rapide clock deadline overflows Ticks")
	ErrClockScheduleDeadlock = errors.New("deterministic Rapide clock schedule cannot make progress")
	ErrClockAdvanceDirective = errors.New("explicit Rapide clock advance directive cannot execute")
	ErrInvalidTimingRange    = errors.New("invalid finite Rapide Ticks range timing expression")
)

// MaxTimingRangeCardinality is the current closed-model boundary for a finite
// type-valued timing expression. Larger and non-range Ticks subtypes remain
// explicit unsupported language until their object domains can be enumerated
// without an implicit resource policy.
const MaxTimingRangeCardinality uint64 = 256

// ClockAdvancePolicy identifies the normal deterministic completion of
// Rapide's deliberately nondeterministic basic-clock ticking semantics. The
// engine performs no unconstrained idle ticks unless the journal supplies a
// finite explicit target. Such a target may not pass the selected clock's next
// deadline. Without a directive, one enabled clock advances to its nearest
// deadline; independent clocks use the replayable/explorable choice schedule.
const ClockAdvancePolicy = "gorapide.clock-advance.journal-or-minimum-deadline-canonical.v2"

// BasicClockDeclaration is one MakeClock() object local to a component's
// module implementation. Rapide clock names are scoped; ClockID returns the
// stable model-wide identity used in event timing intervals and artifacts.
type BasicClockDeclaration struct {
	Name string
}

// ClockID returns the model-wide identity of a component-local basic clock.
// Clocked components and local clock names are restricted to Rapide identifier
// spelling so this source-like qualified name is unambiguous.
func ClockID(componentID, localName string) string {
	return componentID + "." + localName
}

// AddBasicClock declares one component-local MakeClock() object. Complete
// validation, including duplicate detection and scoped identity validation,
// occurs while constructing the deterministic model.
func (component *Component) AddBasicClock(name string) error {
	if component == nil || name == "" {
		return fmt.Errorf("%w: component or clock name is empty", ErrInvalidBasicClock)
	}
	component.mu.Lock()
	component.basicClocks = append(component.basicClocks, BasicClockDeclaration{Name: name})
	component.mu.Unlock()
	return nil
}

type canonicalBasicClockDeclaration struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
}

func validRapideClockIdentifier(name string) bool {
	if name == "" {
		return false
	}
	for index, character := range []byte(name) {
		letter := character >= 'A' && character <= 'Z' || character >= 'a' && character <= 'z'
		digit := character >= '0' && character <= '9'
		if index == 0 {
			if !letter {
				return false
			}
			continue
		}
		if !letter && !digit && character != '_' {
			return false
		}
	}
	return true
}

func validDeterministicClockComponentID(componentID, architectureID string) bool {
	if validRapideClockIdentifier(componentID) {
		return true
	}
	if architectureID == "" || architectureID == ArchitectureInterfaceID {
		return false
	}
	prefix := architectureInstanceAuditID(architectureID) + "/"
	localID := strings.TrimPrefix(componentID, prefix)
	return strings.HasPrefix(componentID, prefix) &&
		!strings.Contains(localID, "/") && validRapideClockIdentifier(localID)
}

func canonicalizeActionTimingClause(component *Component, clause *ActionTimingClause, allowSuspension bool) (*ActionTimingClause, *canonicalActionTimingClause, error) {
	if clause == nil {
		return nil, nil, nil
	}
	if clause.Kind != InTimingClause && clause.Kind != PauseTimingClause && clause.Kind != DelayTimingClause {
		return nil, nil, fmt.Errorf("%w: unsupported timing clause kind %q", ErrInvalidDeclarativeStatement, clause.Kind)
	}
	if component == nil || !validRapideClockIdentifier(clause.Clock) {
		return nil, nil, fmt.Errorf("%w: invalid local clock %q", ErrInvalidDeclarativeStatement, clause.Clock)
	}
	component.mu.Lock()
	found := false
	for _, declaration := range component.basicClocks {
		if declaration.Name == clause.Clock {
			found = true
			break
		}
	}
	component.mu.Unlock()
	if !found {
		return nil, nil, fmt.Errorf("%w: component %q has no basic clock %q", ErrInvalidDeclarativeStatement, component.ID, clause.Clock)
	}
	var tickRange *TimingTickRange
	if clause.Range != nil {
		if clause.Ticks != 0 {
			return nil, nil, fmt.Errorf("%w: %w: timing expression has both object %d and range %d..%d",
				ErrInvalidDeclarativeStatement, ErrInvalidTimingRange, clause.Ticks, clause.Range.First, clause.Range.Last)
		}
		if clause.Range.First > clause.Range.Last {
			return nil, nil, fmt.Errorf("%w: %w: empty range %d..%d requires Timing_Error support",
				ErrInvalidDeclarativeStatement, ErrInvalidTimingRange, clause.Range.First, clause.Range.Last)
		}
		if clause.Range.Last-clause.Range.First >= MaxTimingRangeCardinality {
			return nil, nil, fmt.Errorf("%w: %w: range %d..%d exceeds supported cardinality %d",
				ErrInvalidDeclarativeStatement, ErrInvalidTimingRange,
				clause.Range.First, clause.Range.Last, MaxTimingRangeCardinality)
		}
		if clause.Range.First == clause.Range.Last {
			clause = &ActionTimingClause{Kind: clause.Kind, Clock: clause.Clock, Ticks: clause.Range.First}
		} else {
			rangeCopy := *clause.Range
			tickRange = &rangeCopy
		}
	}
	if clause.Kind == InTimingClause && tickRange == nil && clause.Ticks == 0 {
		return nil, nil, nil
	}
	if clause.Kind != InTimingClause {
		if !allowSuspension {
			return nil, nil, fmt.Errorf("%w: %s requires a resumable declarative-process continuation", ErrInvalidDeclarativeStatement, clause.Kind)
		}
	}
	normalized := &ActionTimingClause{
		Kind: clause.Kind, Clock: ClockID(component.ID, clause.Clock), Ticks: clause.Ticks, Range: tickRange,
	}
	canonical := &canonicalActionTimingClause{
		Kind: normalized.Kind, Clock: normalized.Clock,
	}
	if normalized.Range == nil {
		canonical.Ticks = strconv.FormatUint(normalized.Ticks, 10)
	} else {
		canonical.Range = &canonicalTimingTickRange{
			First: strconv.FormatUint(normalized.Range.First, 10),
			Last:  strconv.FormatUint(normalized.Range.Last, 10),
		}
	}
	return normalized, canonical, nil
}

type basicClockRuntime struct {
	id    string
	owner string
	name  string
	now   uint64
}

// ClockStateRecord is the final auditable counter value of one basic clock.
// Now is a decimal string so the complete uint64 Ticks domain has a stable JSON
// representation independent of JSON-number precision.
type ClockStateRecord struct {
	Clock string `json:"clock"`
	Owner string `json:"owner"`
	Name  string `json:"name"`
	Now   string `json:"now"`
}

// ClockAdvanceRecord records every semantic counter change. Released contains
// stable scheduled-action IDs or input event IDs made observable by the step.
type ClockAdvanceRecord struct {
	Sequence uint64   `json:"sequence"`
	Clock    string   `json:"clock"`
	From     string   `json:"from"`
	To       string   `json:"to"`
	Reason   string   `json:"reason"`
	Released []string `json:"released"`
}

// ScheduledEventPlanRecord is attached to the firing that evaluated an
// action's name and parameters. The event itself does not yet exist.
type ScheduledEventPlanRecord struct {
	ScheduleID string `json:"schedule_id"`
	OutputID   string `json:"output_id"`
	Clock      string `json:"clock"`
	Tick       string `json:"tick"`
}

// ScheduledEventRecord connects a deferred in-clause plan to the exact event
// occurrence materialized when its named clock reaches the deadline.
type ScheduledEventRecord struct {
	Sequence   uint64   `json:"sequence"`
	ScheduleID string   `json:"schedule_id"`
	Owner      string   `json:"owner"`
	Component  string   `json:"component"`
	OutputID   string   `json:"output_id"`
	Clock      string   `json:"clock"`
	Tick       string   `json:"tick"`
	EventID    string   `json:"event_id"`
	Causes     []string `json:"causes"`
}

// ProcessSuspensionRecord is the auditable interval of one pause/delay action
// clause or timed statement. EventID is populated only for an action clause.
type ProcessSuspensionRecord struct {
	SuspensionID string           `json:"suspension_id"`
	Kind         TimingClauseKind `json:"kind"`
	Clock        string           `json:"clock"`
	Start        string           `json:"start"`
	Finish       string           `json:"finish"`
	Statement    string           `json:"statement"`
	OutputID     string           `json:"output_id,omitempty"`
	EventID      string           `json:"event_id,omitempty"`
}

// ProcessSwitchRecord is the auditable completion of one synchronous function
// call that yielded its owning process back to the semantic scheduler.
type ProcessSwitchRecord struct {
	SwitchID      string `json:"switch_id"`
	Kind          string `json:"kind"`
	Statement     string `json:"statement"`
	CallEventID   string `json:"call_event_id"`
	ReturnEventID string `json:"return_event_id"`
}

type causalFrontierRegistry struct {
	values map[string][]gorapide.EventID
}

func newCausalFrontierRegistry() *causalFrontierRegistry {
	return &causalFrontierRegistry{values: make(map[string][]gorapide.EventID)}
}

func (registry *causalFrontierRegistry) get(owner string) []gorapide.EventID {
	if registry == nil || owner == "" {
		return nil
	}
	return append([]gorapide.EventID(nil), registry.values[owner]...)
}

func (registry *causalFrontierRegistry) set(owner string, frontier []gorapide.EventID) {
	if registry == nil || owner == "" {
		return
	}
	registry.values[owner] = canonicalEventIDs(frontier)
}

type scheduledAction struct {
	scheduleID      string
	owner           string
	componentID     string
	localID         string
	clock           string
	deadline        uint64
	action          string
	occurrence      string
	params          map[string]any
	stateCauses     []gorapide.EventID
	stateOperations []stateOperationReference
	acquiredAfter   []gorapide.EventID
	retentionNameID string
	order           uint64
}

func (action scheduledAction) plan() ScheduledEventPlanRecord {
	return ScheduledEventPlanRecord{
		ScheduleID: action.scheduleID, OutputID: action.localID,
		Clock: action.clock, Tick: strconv.FormatUint(action.deadline, 10),
	}
}

func scheduledPlans(actions []scheduledAction) []ScheduledEventPlanRecord {
	result := make([]ScheduledEventPlanRecord, len(actions))
	for index, action := range actions {
		result[index] = action.plan()
	}
	return result
}

func scheduledActionIDs(actions []scheduledAction) []string {
	result := make([]string, 0, len(actions))
	for _, action := range actions {
		if action.scheduleID != "" {
			result = append(result, action.scheduleID)
		}
	}
	sort.Strings(result)
	return result
}

type pendingInputOccurrence struct {
	event        *gorapide.Event
	depth        uint64
	requirements map[string]uint64
}

type timedProcessSuspension struct {
	id        string
	kind      TimingClauseKind
	clock     string
	start     uint64
	deadline  uint64
	statement string
	action    *suspendedProcessAction
	ready     bool
	runtime   *processRuntime
	record    int
}

type deterministicClockKernel struct {
	clocks             map[string]*basicClockRuntime
	clockIDs           []string
	componentClockIDs  map[string][]string
	scheduled          []scheduledAction
	pendingInputs      []pendingInputOccurrence
	processSuspensions []*timedProcessSuspension
	directives         []ClockAdvanceDirective
	nextDirective      int
	nextOrderByOwner   map[string]uint64
	advances           []ClockAdvanceRecord
	scheduledEvents    []ScheduledEventRecord
	choices            *choiceResolver
}

func newDeterministicClockKernel(model *deterministicModel, directives []ClockAdvanceDirective, choices *choiceResolver) *deterministicClockKernel {
	kernel := &deterministicClockKernel{
		clocks:            make(map[string]*basicClockRuntime),
		componentClockIDs: make(map[string][]string),
		nextOrderByOwner:  make(map[string]uint64),
		directives:        append([]ClockAdvanceDirective(nil), directives...),
		choices:           choices,
	}
	if model == nil {
		return kernel
	}
	for _, componentID := range model.componentIDs {
		for _, declaration := range model.basicClocks[componentID] {
			id := ClockID(componentID, declaration.Name)
			kernel.clocks[id] = &basicClockRuntime{id: id, owner: componentID, name: declaration.Name}
			kernel.clockIDs = append(kernel.clockIDs, id)
			kernel.componentClockIDs[componentID] = append(kernel.componentClockIDs[componentID], id)
		}
	}
	sort.Strings(kernel.clockIDs)
	for componentID := range kernel.componentClockIDs {
		sort.Strings(kernel.componentClockIDs[componentID])
	}
	return kernel
}

// addAllocatedComponentClocks creates the fresh MakeClock objects belonging to
// one allocator-created module. Each clock begins at zero at allocation; the
// sealed generator declarations remain unchanged and are rebound separately in
// the copied executable statements.
func (kernel *deterministicClockKernel) addAllocatedComponentClocks(
	templateID, componentID string,
	declarations []BasicClockDeclaration,
) error {
	if kernel == nil || templateID == "" || componentID == "" {
		return fmt.Errorf("%w: allocated component clock registration is incomplete", ErrInvalidBasicClock)
	}
	if len(kernel.componentClockIDs[componentID]) != 0 {
		return fmt.Errorf("%w: allocated component %q clocks are already registered", ErrInvalidBasicClock, componentID)
	}
	seen := make(map[string]bool, len(declarations))
	for _, declaration := range declarations {
		if !validRapideClockIdentifier(declaration.Name) || seen[declaration.Name] {
			return fmt.Errorf("%w: allocated component %q has invalid or duplicate clock %q",
				ErrInvalidBasicClock, componentID, declaration.Name)
		}
		seen[declaration.Name] = true
		id := ClockID(componentID, declaration.Name)
		if kernel.clocks[id] != nil {
			return fmt.Errorf("%w: allocated clock %q is already registered", ErrInvalidBasicClock, id)
		}
		kernel.clocks[id] = &basicClockRuntime{id: id, owner: componentID, name: declaration.Name}
		kernel.clockIDs = append(kernel.clockIDs, id)
		kernel.componentClockIDs[componentID] = append(kernel.componentClockIDs[componentID], id)
	}
	sort.Strings(kernel.clockIDs)
	sort.Strings(kernel.componentClockIDs[componentID])
	return nil
}

func timingTickOption(ticks uint64) string {
	return fmt.Sprintf("tick@%020d", ticks)
}

func (kernel *deterministicClockKernel) resolveTimingTicks(selectionID string, clause *ActionTimingClause) (uint64, error) {
	if clause == nil {
		return 0, fmt.Errorf("%w: timing selection %q has no clause", ErrInvalidDeclarativeStatement, selectionID)
	}
	if clause.Range == nil {
		return clause.Ticks, nil
	}
	if kernel == nil || kernel.choices == nil {
		return 0, fmt.Errorf("%w: timing selection %q has no choice resolver", ErrInvalidDeclarativeStatement, selectionID)
	}
	options := make([]string, 0, clause.Range.Last-clause.Range.First+1)
	byOption := make(map[string]uint64, cap(options))
	for ticks := clause.Range.First; ; ticks++ {
		option := timingTickOption(ticks)
		options = append(options, option)
		byOption[option] = ticks
		if ticks == clause.Range.Last {
			break
		}
	}
	selected, err := kernel.choices.resolve("timing-object:"+selectionID, options)
	if err != nil {
		return 0, err
	}
	return byOption[selected], nil
}

func (kernel *deterministicClockKernel) hasComponentClocks(componentID string) bool {
	return kernel != nil && len(kernel.componentClockIDs[componentID]) != 0
}

func (kernel *deterministicClockKernel) instantTimings(componentIDs ...string) []gorapide.EventTiming {
	if kernel == nil {
		return nil
	}
	seen := make(map[string]bool)
	timings := make([]gorapide.EventTiming, 0)
	for _, componentID := range componentIDs {
		for _, clockID := range kernel.componentClockIDs[componentID] {
			if seen[clockID] {
				continue
			}
			seen[clockID] = true
			now := kernel.clocks[clockID].now
			timings = append(timings, gorapide.EventTiming{Clock: clockID, Start: now, Finish: now})
		}
	}
	sort.Slice(timings, func(i, j int) bool { return timings[i].Clock < timings[j].Clock })
	return timings
}

func (kernel *deterministicClockKernel) missingObservationTimings(event *gorapide.Event, componentID string) []gorapide.EventTiming {
	if kernel == nil {
		return nil
	}
	result := make([]gorapide.EventTiming, 0)
	for _, clockID := range kernel.componentClockIDs[componentID] {
		if _, exists := event.Timing(clockID); exists {
			continue
		}
		now := kernel.clocks[clockID].now
		result = append(result, gorapide.EventTiming{Clock: clockID, Start: now, Finish: now})
	}
	return result
}

func (kernel *deterministicClockKernel) deadline(clockID string, ticks uint64) (uint64, error) {
	clock := kernel.clocks[clockID]
	if clock == nil {
		return 0, fmt.Errorf("%w: missing clock %q", ErrInvalidBasicClock, clockID)
	}
	if ticks > ^uint64(0)-clock.now {
		return 0, fmt.Errorf("%w: clock %q now=%d duration=%d", ErrClockDeadlineOverflow, clockID, clock.now, ticks)
	}
	return clock.now + ticks, nil
}

func (kernel *deterministicClockKernel) intervalTimings(componentID, namedClock string, start, finish uint64) []gorapide.EventTiming {
	timings := kernel.instantTimings(componentID)
	for index := range timings {
		if timings[index].Clock == namedClock {
			timings[index].Start = start
			timings[index].Finish = finish
			break
		}
	}
	return timings
}

func scheduledActionID(owner, occurrence, clock string, deadline uint64) string {
	payload := "gorapide:scheduled-action:v1\x00" + owner + "\x00" + occurrence + "\x00" + clock + "\x00" + strconv.FormatUint(deadline, 10)
	digest := sha256.Sum256([]byte(payload))
	return "sch1-" + hex.EncodeToString(digest[:])
}

func processSuspensionID(owner, statement string, kind TimingClauseKind, clock string, deadline uint64) string {
	payload := "gorapide:process-suspension:v1\x00" + owner + "\x00" + statement + "\x00" + string(kind) + "\x00" + clock + "\x00" + strconv.FormatUint(deadline, 10)
	digest := sha256.Sum256([]byte(payload))
	return "sus1-" + hex.EncodeToString(digest[:])
}

func (kernel *deterministicClockKernel) addProcessSuspension(suspension *timedProcessSuspension) {
	if kernel != nil && suspension != nil {
		kernel.processSuspensions = append(kernel.processSuspensions, suspension)
	}
}

func (kernel *deterministicClockKernel) cancelProcessSuspension(
	runtime *processRuntime,
) []string {
	if kernel == nil || runtime == nil || runtime.suspension == nil {
		return nil
	}
	id := runtime.suspension.id
	pending := make([]*timedProcessSuspension, 0, len(kernel.processSuspensions))
	for _, suspension := range kernel.processSuspensions {
		if suspension != nil && suspension.runtime == runtime && suspension.id == id {
			continue
		}
		pending = append(pending, suspension)
	}
	kernel.processSuspensions = pending
	return []string{id}
}

func (kernel *deterministicClockKernel) addScheduled(actions []scheduledAction) {
	for _, action := range actions {
		kernel.nextOrderByOwner[action.owner]++
		action.order = kernel.nextOrderByOwner[action.owner]
		kernel.scheduled = append(kernel.scheduled, action)
	}
}

func (kernel *deterministicClockKernel) cancelProcessWork(runtime *processRuntime) ([]string, []string) {
	if kernel == nil || runtime == nil {
		return nil, nil
	}
	canceledSchedules := make([]string, 0)
	pendingSchedules := make([]scheduledAction, 0, len(kernel.scheduled))
	for _, action := range kernel.scheduled {
		if action.owner == runtime.causalOwner {
			canceledSchedules = append(canceledSchedules, action.scheduleID)
			continue
		}
		pendingSchedules = append(pendingSchedules, action)
	}
	kernel.scheduled = pendingSchedules
	canceledSuspensions := make([]string, 0)
	pendingSuspensions := make([]*timedProcessSuspension, 0, len(kernel.processSuspensions))
	for _, suspension := range kernel.processSuspensions {
		if suspension != nil && suspension.runtime == runtime {
			canceledSuspensions = append(canceledSuspensions, suspension.id)
			continue
		}
		pendingSuspensions = append(pendingSuspensions, suspension)
	}
	kernel.processSuspensions = pendingSuspensions
	if runtime.suspension != nil {
		found := false
		for _, id := range canceledSuspensions {
			if id == runtime.suspension.id {
				found = true
				break
			}
		}
		if !found {
			canceledSuspensions = append(canceledSuspensions, runtime.suspension.id)
		}
	}
	sort.Strings(canceledSchedules)
	sort.Strings(canceledSuspensions)
	return canceledSchedules, canceledSuspensions
}

func (kernel *deterministicClockKernel) completeInputTimings(source string, timings []gorapide.EventTiming, causes ...*gorapide.Event) ([]gorapide.EventTiming, error) {
	if kernel == nil || !kernel.hasComponentClocks(source) {
		return gorapide.CanonicalizeEventTimings(timings)
	}
	result := append([]gorapide.EventTiming(nil), timings...)
	present := make(map[string]bool, len(result))
	for _, timing := range result {
		present[timing.Clock] = true
	}
	for _, clockID := range kernel.componentClockIDs[source] {
		if !present[clockID] {
			now := kernel.clocks[clockID].now
			for _, cause := range causes {
				if timing, related := cause.Timing(clockID); related && timing.Finish > now {
					now = timing.Finish
				}
			}
			result = append(result, gorapide.EventTiming{Clock: clockID, Start: now, Finish: now})
		}
	}
	return gorapide.CanonicalizeEventTimings(result)
}

func (kernel *deterministicClockKernel) deferInput(event *gorapide.Event, depth uint64) bool {
	if kernel == nil || event == nil {
		return false
	}
	requirements := make(map[string]uint64)
	for _, timing := range event.Timings {
		clock := kernel.clocks[timing.Clock]
		if clock != nil && timing.Finish > clock.now {
			requirements[timing.Clock] = timing.Finish
		}
	}
	if len(requirements) == 0 {
		return false
	}
	kernel.pendingInputs = append(kernel.pendingInputs, pendingInputOccurrence{
		event: event, depth: depth, requirements: requirements,
	})
	return true
}

func (kernel *deterministicClockKernel) nextAdvanceTargets() map[string]uint64 {
	targets := make(map[string]uint64)
	consider := func(clockID string, target uint64) {
		clock := kernel.clocks[clockID]
		if clock == nil || target <= clock.now {
			return
		}
		current, exists := targets[clockID]
		if !exists || target < current {
			targets[clockID] = target
		}
	}
	for _, action := range kernel.scheduled {
		consider(action.clock, action.deadline)
	}
	for _, input := range kernel.pendingInputs {
		for clockID, target := range input.requirements {
			consider(clockID, target)
		}
	}
	for _, suspension := range kernel.processSuspensions {
		consider(suspension.clock, suspension.deadline)
	}
	return targets
}

func clockAdvanceOption(clock string, target uint64) string {
	return clock + "@" + strconv.FormatUint(target, 10)
}

func (kernel *deterministicClockKernel) advanceAndRelease(
	profile, modelDigest string,
	choices *choiceResolver,
	frontiers *causalFrontierRegistry,
	poset *gorapide.Poset,
	depths map[gorapide.EventID]uint64,
	queue *executionQueue,
	seenItems map[string]bool,
	moduleState moduleStateRuntime,
	stateSnapshots stateSnapshotRegistry,
	functionRuntime *functionExecutionRuntime,
	statementSteps *statementBudget,
	firings *[]FiringRecord,
) (bool, error) {
	targets := kernel.nextAdvanceTargets()
	var clockID string
	var target uint64
	reason := "deadline"
	if kernel.nextDirective < len(kernel.directives) {
		directiveIndex := kernel.nextDirective
		directive := kernel.directives[directiveIndex]
		clock := kernel.clocks[directive.Clock]
		if clock == nil {
			return false, fmt.Errorf("%w: %w: directive %d names undeclared clock %q",
				ErrInvalidExecutionJournal, ErrClockAdvanceDirective, directiveIndex, directive.Clock)
		}
		if directive.To <= clock.now {
			return false, fmt.Errorf("%w: %w: directive %d advances %s from %d to nonfuture %d",
				ErrInvalidExecutionJournal, ErrClockAdvanceDirective, directiveIndex,
				directive.Clock, clock.now, directive.To)
		}
		if deadline, constrained := targets[directive.Clock]; constrained && directive.To > deadline {
			return false, fmt.Errorf("%w: %w: directive %d advances %s to %d past nearest deadline %d",
				ErrInvalidExecutionJournal, ErrClockAdvanceDirective, directiveIndex,
				directive.Clock, directive.To, deadline)
		}
		clockID = directive.Clock
		target = directive.To
		reason = "explicit"
		kernel.nextDirective++
	} else {
		if len(targets) == 0 {
			if len(kernel.scheduled) != 0 || len(kernel.pendingInputs) != 0 || len(kernel.processSuspensions) != 0 {
				return false, fmt.Errorf("%w: %d actions, %d inputs, and %d process suspensions remain", ErrClockScheduleDeadlock, len(kernel.scheduled), len(kernel.pendingInputs), len(kernel.processSuspensions))
			}
			return false, nil
		}
		options := make([]string, 0, len(targets))
		byOption := make(map[string]string, len(targets))
		for candidateClock, candidateTarget := range targets {
			option := clockAdvanceOption(candidateClock, candidateTarget)
			options = append(options, option)
			byOption[option] = candidateClock
		}
		selected, err := choices.resolve("clock-advance", options)
		if err != nil {
			return false, err
		}
		clockID = byOption[selected]
		target = targets[clockID]
	}
	clock := kernel.clocks[clockID]
	from := clock.now
	clock.now = target
	released := make([]string, 0)

	readyActions := make([]scheduledAction, 0)
	pendingActions := make([]scheduledAction, 0, len(kernel.scheduled))
	for _, action := range kernel.scheduled {
		if action.clock == clockID && action.deadline <= clock.now {
			readyActions = append(readyActions, action)
		} else {
			pendingActions = append(pendingActions, action)
		}
	}
	kernel.scheduled = pendingActions
	sort.Slice(readyActions, func(i, j int) bool {
		if readyActions[i].owner != readyActions[j].owner {
			return readyActions[i].owner < readyActions[j].owner
		}
		return readyActions[i].order < readyActions[j].order
	})
	for _, action := range readyActions {
		causes := canonicalEventIDs(append(frontiers.get(action.owner), action.stateCauses...))
		event, err := gorapide.NewDeterministicEvent(gorapide.EventProvenance{
			Profile: profile, Model: modelDigest, Instance: action.componentID,
			Action: action.action, Occurrence: action.occurrence, Causes: causes,
			Timings: kernel.instantTimings(action.componentID),
		}, action.params)
		if err != nil {
			return false, fmt.Errorf("scheduled action %q: %w", action.scheduleID, err)
		}
		if err := addStateOperationSuccessors(action.stateOperations, string(event.ID)); err != nil {
			return false, err
		}
		if err := poset.AddEventWithCause(event, causes...); err != nil {
			return false, fmt.Errorf("scheduled action %q: %w", action.scheduleID, err)
		}
		frontiers.set(action.owner, []gorapide.EventID{event.ID})
		depth := eventDepth(poset, event, depths)
		depths[event.ID] = depth
		output := generatedRuleOutput{
			localID: action.localID, event: event, causes: causes,
			stateSnapshot: cloneStateCells(moduleState[action.componentID]),
		}
		if err := enqueueGeneratedObservationViews(output, depth, moduleState, stateSnapshots, queue, seenItems); err != nil {
			return false, err
		}
		if action.retentionNameID != "" {
			if functionRuntime == nil || firings == nil {
				return false, fmt.Errorf("%w: scheduled action %q has no lifecycle runtime",
					ErrInvalidDeclarativeStatement, action.scheduleID)
			}
			finalization := &statementExecution{
				clocks: kernel, owner: "scheduled-finalization:" + action.scheduleID,
				budget: statementSteps,
			}
			moduleID, releaseErr := releaseModuleName(
				modelDigest, action.retentionNameID, []gorapide.EventID{event.ID},
				functionRuntime, finalization,
			)
			if releaseErr != nil {
				return false, fmt.Errorf("scheduled action %q lifecycle release: %w", action.scheduleID, releaseErr)
			}
			if moduleID != "" {
				if uint64(len(*firings)) >= functionRuntime.maxFirings {
					return false, fmt.Errorf("%w: max_firings=%d", ErrExecutionLimit, functionRuntime.maxFirings)
				}
				generated := make([]GeneratedEventRecord, 0, len(finalization.generated))
				for _, finalOutput := range finalization.generated {
					if err := poset.AddEventWithCause(finalOutput.event, finalOutput.causes...); err != nil {
						return false, fmt.Errorf("scheduled action %q finalization output %s: %w",
							action.scheduleID, finalOutput.localID, err)
					}
					finalDepth := eventDepth(poset, finalOutput.event, depths)
					depths[finalOutput.event.ID] = finalDepth
					if err := enqueueGeneratedObservationViews(
						finalOutput, finalDepth, moduleState, stateSnapshots, queue, seenItems,
					); err != nil {
						return false, err
					}
					generated = append(generated, GeneratedEventRecord{
						OutputID: finalOutput.localID, EventID: string(finalOutput.event.ID),
						Exception: finalOutput.exception,
					})
				}
				*firings = append(*firings, FiringRecord{
					Sequence: uint64(len(*firings) + 1), Transition: "scheduled-finalization",
					TriggerID: string(event.ID), TriggerSource: event.Source,
					TriggerAction: event.Name, Target: moduleID, Generated: generated,
				})
			}
		}
		causeStrings := make([]string, len(causes))
		for index, cause := range causes {
			causeStrings[index] = string(cause)
		}
		kernel.scheduledEvents = append(kernel.scheduledEvents, ScheduledEventRecord{
			Sequence: uint64(len(kernel.scheduledEvents) + 1), ScheduleID: action.scheduleID,
			Owner: action.owner, Component: action.componentID, OutputID: action.localID,
			Clock: action.clock, Tick: strconv.FormatUint(action.deadline, 10),
			EventID: string(event.ID), Causes: causeStrings,
		})
		released = append(released, "schedule:"+action.scheduleID)
	}

	readyInputs := make([]pendingInputOccurrence, 0)
	pendingInputs := make([]pendingInputOccurrence, 0, len(kernel.pendingInputs))
	for _, input := range kernel.pendingInputs {
		ready := true
		for requiredClock, target := range input.requirements {
			if kernel.clocks[requiredClock].now < target {
				ready = false
				break
			}
		}
		if ready {
			readyInputs = append(readyInputs, input)
		} else {
			pendingInputs = append(pendingInputs, input)
		}
	}
	kernel.pendingInputs = pendingInputs
	sort.Slice(readyInputs, func(i, j int) bool { return readyInputs[i].event.ID < readyInputs[j].event.ID })
	for _, input := range readyInputs {
		enqueueExecutionItem(queue, seenItems, input.event, input.depth)
		released = append(released, "input:"+string(input.event.ID))
	}

	pendingSuspensions := make([]*timedProcessSuspension, 0, len(kernel.processSuspensions))
	readySuspensions := make([]*timedProcessSuspension, 0)
	for _, suspension := range kernel.processSuspensions {
		if suspension.clock == clockID && suspension.deadline <= clock.now {
			readySuspensions = append(readySuspensions, suspension)
		} else {
			pendingSuspensions = append(pendingSuspensions, suspension)
		}
	}
	kernel.processSuspensions = pendingSuspensions
	sort.Slice(readySuspensions, func(i, j int) bool { return readySuspensions[i].id < readySuspensions[j].id })
	for _, suspension := range readySuspensions {
		suspension.ready = true
		released = append(released, "resume:"+suspension.id)
	}

	kernel.advances = append(kernel.advances, ClockAdvanceRecord{
		Sequence: uint64(len(kernel.advances) + 1), Clock: clockID,
		From: strconv.FormatUint(from, 10), To: strconv.FormatUint(clock.now, 10),
		Reason: reason, Released: released,
	})
	return true, nil
}

func (kernel *deterministicClockKernel) stateRecords() []ClockStateRecord {
	result := make([]ClockStateRecord, 0, len(kernel.clockIDs))
	for _, clockID := range kernel.clockIDs {
		clock := kernel.clocks[clockID]
		result = append(result, ClockStateRecord{
			Clock: clock.id, Owner: clock.owner, Name: clock.name,
			Now: strconv.FormatUint(clock.now, 10),
		})
	}
	return result
}

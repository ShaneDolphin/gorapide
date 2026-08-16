package arch

import (
	"errors"
	"fmt"
	"sort"

	"github.com/ShaneDolphin/gorapide/pattern"
)

var ErrInvalidDeclarativeProcess = errors.New("invalid declarative Rapide process")

// ModuleProcessMode distinguishes Rapide's serial and parallel module
// processors. It matters when a component declares more than one process.
type ModuleProcessMode int

const (
	UnspecifiedProcessMode ModuleProcessMode = iota
	SerialProcesses
	ParallelProcesses
)

func (mode ModuleProcessMode) String() string {
	switch mode {
	case SerialProcesses:
		return "serial"
	case ParallelProcesses:
		return "parallel"
	default:
		return "unspecified"
	}
}

// AwaitAlternative is one pattern/body alternative of an await statement.
// Next names the next await state; an empty Next terminates the process.
type AwaitAlternative struct {
	ID      string
	Trigger pattern.Pattern
	Guard   *RuleValue
	Body    *RuleBody
	Next    string
}

// AwaitAlternativeBuilder constructs a closed await alternative.
type AwaitAlternativeBuilder struct {
	alternative AwaitAlternative
}

// Await begins one named await alternative.
func Await(id string) *AwaitAlternativeBuilder {
	return &AwaitAlternativeBuilder{alternative: AwaitAlternative{ID: id}}
}

// On sets the event-pattern trigger.
func (builder *AwaitAlternativeBuilder) On(trigger pattern.Pattern) *AwaitAlternativeBuilder {
	builder.alternative.Trigger = trigger
	return builder
}

// Where adds a closed Boolean guard evaluated at generation of the last event
// in the alternative's selected match.
func (builder *AwaitAlternativeBuilder) Where(guard RuleValue) *AwaitAlternativeBuilder {
	copy := copyRuleValue(guard)
	builder.alternative.Guard = &copy
	return builder
}

// Emit declares a single generated event in the alternative body.
func (builder *AwaitAlternativeBuilder) Emit(action string, parameters ...RuleParameter) *AwaitAlternativeBuilder {
	body := ensureRuleBody(builder.alternative.Body)
	body.Outputs = []RuleOutput{RuleEvent("result", action, parameters...)}
	builder.alternative.Body = body
	return builder
}

// Generate declares a finite generated body poset.
func (builder *AwaitAlternativeBuilder) Generate(outputs ...RuleOutput) *AwaitAlternativeBuilder {
	body := ensureRuleBody(builder.alternative.Body)
	body.Outputs = make([]RuleOutput, len(outputs))
	for i, output := range outputs {
		body.Outputs[i] = copyRuleOutput(output)
	}
	builder.alternative.Body = body
	return builder
}

// NoEvents declares a null alternative body.
func (builder *AwaitAlternativeBuilder) NoEvents() *AwaitAlternativeBuilder {
	body := ensureRuleBody(builder.alternative.Body)
	body.Outputs = []RuleOutput{}
	builder.alternative.Body = body
	return builder
}

// Do declares a sequential ordinary-statement alternative body.
func (builder *AwaitAlternativeBuilder) Do(statements ...Statement) *AwaitAlternativeBuilder {
	builder.alternative.Body = &RuleBody{Statements: copyStatements(statements)}
	return builder
}

// Assign declares sequential module-state assignments before generated events.
func (builder *AwaitAlternativeBuilder) Assign(assignments ...StateAssignment) *AwaitAlternativeBuilder {
	body := ensureRuleBody(builder.alternative.Body)
	for _, assignment := range assignments {
		body.Assignments = append(body.Assignments, StateAssignment{
			Target: assignment.Target, Value: copyRuleValue(assignment.Value),
		})
	}
	builder.alternative.Body = body
	return builder
}

// Then transfers control to the named await state after the body completes.
func (builder *AwaitAlternativeBuilder) Then(stateID string) *AwaitAlternativeBuilder {
	builder.alternative.Next = stateID
	return builder
}

// Terminate makes the alternative terminate its process.
func (builder *AwaitAlternativeBuilder) Terminate() *AwaitAlternativeBuilder {
	builder.alternative.Next = ""
	return builder
}

// Build returns an isolated alternative snapshot.
func (builder *AwaitAlternativeBuilder) Build() AwaitAlternative {
	if builder == nil {
		return AwaitAlternative{}
	}
	result := builder.alternative
	result.Guard = copyRuleValuePointer(builder.alternative.Guard)
	result.Body = copyRuleBody(builder.alternative.Body)
	return result
}

// ProcessState represents one suspended await statement. Alternatives are
// selected from the matches available when the process becomes active.
type ProcessState struct {
	ID           string
	Alternatives []AwaitAlternative
	Else         *AwaitAlternative

	// doName is the optional source label of the do statement represented by a
	// source-equivalent self-looping when state. It remains private so an
	// ordinary await state cannot silently claim do-statement control semantics.
	doName string
}

// AwaitState constructs one await statement with one or more alternatives.
func AwaitState(id string, alternatives ...AwaitAlternative) ProcessState {
	state := ProcessState{ID: id, Alternatives: make([]AwaitAlternative, len(alternatives))}
	for i, alternative := range alternatives {
		state.Alternatives[i] = copyAwaitAlternative(alternative)
	}
	return state
}

// AwaitElse begins the nonblocking else part of an await statement. An else
// part has no trigger or guard and executes only when no triggering match is
// available as the process enters the await state.
func AwaitElse(id string) *AwaitAlternativeBuilder {
	return Await(id)
}

// AwaitStateWithElse constructs one nonblocking await statement. The else
// branch is considered only when none of the patterned alternatives has a
// triggering match.
func AwaitStateWithElse(id string, elseBranch AwaitAlternative, alternatives ...AwaitAlternative) ProcessState {
	state := AwaitState(id, alternatives...)
	copy := copyAwaitAlternative(elseBranch)
	state.Else = &copy
	return state
}

// WhenState constructs the source-equivalent self-looping await used by a
// Rapide when statement.
func WhenState(id string, trigger pattern.Pattern, body *RuleBody) ProcessState {
	builder := Await(id + "/when").On(trigger).Then(id)
	builder.alternative.Body = ensureRuleBody(body)
	return AwaitState(id, builder.Build())
}

// NameWhenState returns a copy whose source-equivalent when statement has the
// given Rapide label. Canonical validation rejects names on states that are not
// the single-alternative self-loop produced by a when statement.
func NameWhenState(name string, state ProcessState) ProcessState {
	result := copyProcessState(state)
	result.doName = name
	return result
}

func processStateIsSourceWhen(state ProcessState) bool {
	return len(state.Alternatives) == 1 && state.Else == nil && state.Alternatives[0].Next == state.ID
}

func bodyOutputs(body *RuleBody) []RuleOutput {
	if body == nil {
		return nil
	}
	result := make([]RuleOutput, len(body.Outputs))
	for i, output := range body.Outputs {
		result[i] = copyRuleOutput(output)
	}
	return result
}

// EventBody constructs a finite process or rule generator body.
func EventBody(outputs ...RuleOutput) *RuleBody {
	return &RuleBody{Outputs: bodyOutputs(&RuleBody{Outputs: outputs})}
}

// StatementBody constructs an ordered ordinary-statement process/rule body.
func StatementBody(statements ...Statement) *RuleBody {
	return &RuleBody{Statements: copyStatements(statements)}
}

// DeclarativeProcess is the closed await/when state-machine representation of
// one Rapide process. The initial subset contains reactive states only.
type DeclarativeProcess struct {
	ID      string
	Initial string
	States  []ProcessState
}

// DeclarativeProcessBuilder constructs a closed process declaration.
type DeclarativeProcessBuilder struct {
	process DeclarativeProcess
}

// Process begins a declarative process.
func Process(id string) *DeclarativeProcessBuilder {
	return &DeclarativeProcessBuilder{process: DeclarativeProcess{ID: id}}
}

// StartAt declares the initial await state.
func (builder *DeclarativeProcessBuilder) StartAt(stateID string) *DeclarativeProcessBuilder {
	builder.process.Initial = stateID
	return builder
}

// States declares the process's reactive control states. Slice order has no
// semantic meaning.
func (builder *DeclarativeProcessBuilder) States(states ...ProcessState) *DeclarativeProcessBuilder {
	builder.process.States = make([]ProcessState, len(states))
	for i, state := range states {
		builder.process.States[i] = copyProcessState(state)
	}
	return builder
}

// Build returns an isolated process snapshot.
func (builder *DeclarativeProcessBuilder) Build() *DeclarativeProcess {
	if builder == nil {
		return nil
	}
	result := builder.process
	result.States = make([]ProcessState, len(builder.process.States))
	for i, state := range builder.process.States {
		result.States[i] = copyProcessState(state)
	}
	return &result
}

// AddDeclarativeProcess registers a closed process. Components with more than
// one process must also declare serial or parallel module processor semantics.
func (component *Component) AddDeclarativeProcess(process *DeclarativeProcess) error {
	if component == nil || process == nil {
		return fmt.Errorf("%w: component or process is nil", ErrInvalidDeclarativeProcess)
	}
	copy := *process
	copy.States = make([]ProcessState, len(process.States))
	for i, state := range process.States {
		copy.States[i] = copyProcessState(state)
	}
	component.mu.Lock()
	component.processes = append(component.processes, &copy)
	component.mu.Unlock()
	return nil
}

// SetModuleProcessMode declares the processor semantics for a multi-process
// module implementation. For one process, serial and parallel are equivalent
// and canonicalize to the same single-process model.
func (component *Component) SetModuleProcessMode(mode ModuleProcessMode) error {
	if component == nil || (mode != SerialProcesses && mode != ParallelProcesses) {
		return fmt.Errorf("%w: invalid module process mode %d", ErrInvalidDeclarativeProcess, mode)
	}
	component.mu.Lock()
	component.processMode = mode
	component.mu.Unlock()
	return nil
}

type canonicalAwaitAlternative struct {
	ID          string                     `json:"id"`
	Trigger     string                     `json:"trigger"`
	Guard       *canonicalRuleValue        `json:"guard,omitempty"`
	Assignments []canonicalStateAssignment `json:"assignments"`
	Outputs     []canonicalRuleOutput      `json:"outputs"`
	Statements  []canonicalRuleStatement   `json:"statements,omitempty"`
	Next        string                     `json:"next"`
}

type canonicalProcessState struct {
	ID           string                      `json:"id"`
	DoControl    bool                        `json:"do_control,omitempty"`
	DoName       string                      `json:"do_name,omitempty"`
	Alternatives []canonicalAwaitAlternative `json:"alternatives"`
	Else         *canonicalAwaitAlternative  `json:"else,omitempty"`
}

type canonicalDeclarativeProcess struct {
	ID      string                  `json:"id"`
	Initial string                  `json:"initial"`
	States  []canonicalProcessState `json:"states"`
}

func canonicalizeDeclarativeProcess(component *Component, declaration *DeclarativeProcess, stateTypes map[string]string, functions map[string]*FunctionImplementation) (*DeclarativeProcess, canonicalDeclarativeProcess, error) {
	if declaration == nil || declaration.ID == "" || declaration.Initial == "" {
		return nil, canonicalDeclarativeProcess{}, fmt.Errorf("%w: process or initial state has no stable identity", ErrInvalidDeclarativeProcess)
	}
	if len(declaration.States) == 0 {
		return nil, canonicalDeclarativeProcess{}, fmt.Errorf("%w: process %q has no await states", ErrInvalidDeclarativeProcess, declaration.ID)
	}
	normalized := &DeclarativeProcess{ID: declaration.ID, Initial: declaration.Initial}
	canonical := canonicalDeclarativeProcess{ID: declaration.ID, Initial: declaration.Initial}
	seenStates := make(map[string]bool, len(declaration.States))
	for _, state := range declaration.States {
		if state.ID == "" || seenStates[state.ID] {
			return nil, canonicalDeclarativeProcess{}, fmt.Errorf("%w: process %q has empty or duplicate state %q", ErrInvalidDeclarativeProcess, declaration.ID, state.ID)
		}
		if len(state.Alternatives) == 0 {
			return nil, canonicalDeclarativeProcess{}, fmt.Errorf("%w: process %q state %q has no alternatives", ErrInvalidDeclarativeProcess, declaration.ID, state.ID)
		}
		seenStates[state.ID] = true
		allowProcessDoControl := processStateIsSourceWhen(state)
		doName, err := canonicalDoName(state.doName)
		if err != nil {
			return nil, canonicalDeclarativeProcess{}, fmt.Errorf("%w: process %q state %q: %v", ErrInvalidDeclarativeProcess, declaration.ID, state.ID, err)
		}
		if doName != "" && !allowProcessDoControl {
			return nil, canonicalDeclarativeProcess{}, fmt.Errorf("%w: process %q state %q names do %q but is not a source-equivalent when", ErrInvalidDeclarativeProcess, declaration.ID, state.ID, doName)
		}
		normalizedState := ProcessState{ID: state.ID, doName: doName}
		canonicalState := canonicalProcessState{ID: state.ID, DoControl: allowProcessDoControl, DoName: doName}
		seenAlternatives := make(map[string]bool, len(state.Alternatives))
		for _, alternative := range state.Alternatives {
			if alternative.ID == "" || seenAlternatives[alternative.ID] {
				return nil, canonicalDeclarativeProcess{}, fmt.Errorf("%w: process %q state %q has empty or duplicate alternative %q", ErrInvalidDeclarativeProcess, declaration.ID, state.ID, alternative.ID)
			}
			if alternative.Trigger == nil {
				return nil, canonicalDeclarativeProcess{}, fmt.Errorf("%w: process %q state %q alternative %q has no trigger", ErrInvalidDeclarativeProcess, declaration.ID, state.ID, alternative.ID)
			}
			seenAlternatives[alternative.ID] = true
			ownerID := declaration.ID + "/" + state.ID + "/" + alternative.ID
			fakeRule := &DeclarativeRule{
				ID: ownerID, Trigger: alternative.Trigger, Guard: alternative.Guard,
				Process: RuleAgentProcess, Body: alternative.Body,
				allowProcessDoControl: allowProcessDoControl, processDoName: doName, allowTimingSuspension: true,
				allowProcessInterruptAllocation: true,
			}
			normalizedRule, canonicalRule, err := canonicalizeDeclarativeRule(component, fakeRule, stateTypes, functions)
			if err != nil {
				return nil, canonicalDeclarativeProcess{}, fmt.Errorf("%w: process %q state %q alternative %q: %w", ErrInvalidDeclarativeProcess, declaration.ID, state.ID, alternative.ID, err)
			}
			if !ruleBodyIsTotalOrder(normalizedRule.Body.Outputs) {
				return nil, canonicalDeclarativeProcess{}, fmt.Errorf("%w: process %q state %q alternative %q body is not a sequential event order", ErrInvalidDeclarativeProcess, declaration.ID, state.ID, alternative.ID)
			}
			normalizedState.Alternatives = append(normalizedState.Alternatives, AwaitAlternative{
				ID: alternative.ID, Trigger: normalizedRule.Trigger, Guard: normalizedRule.Guard,
				Body: normalizedRule.Body, Next: alternative.Next,
			})
			canonicalState.Alternatives = append(canonicalState.Alternatives, canonicalAwaitAlternative{
				ID: alternative.ID, Trigger: canonicalRule.Trigger,
				Guard:       canonicalRule.Guard,
				Assignments: canonicalRule.Assignments, Outputs: canonicalRule.Outputs,
				Statements: canonicalRule.Statements, Next: alternative.Next,
			})
		}
		if state.Else != nil {
			elseBranch := copyAwaitAlternative(*state.Else)
			if elseBranch.ID == "" || seenAlternatives[elseBranch.ID] {
				return nil, canonicalDeclarativeProcess{}, fmt.Errorf("%w: process %q state %q has empty or duplicate else alternative %q", ErrInvalidDeclarativeProcess, declaration.ID, state.ID, elseBranch.ID)
			}
			if elseBranch.Trigger != nil || elseBranch.Guard != nil {
				return nil, canonicalDeclarativeProcess{}, fmt.Errorf("%w: process %q state %q else alternative %q cannot declare a trigger or guard", ErrInvalidDeclarativeProcess, declaration.ID, state.ID, elseBranch.ID)
			}
			ownerID := declaration.ID + "/" + state.ID + "/" + elseBranch.ID
			fakeRule := &DeclarativeRule{
				ID: ownerID, Trigger: pattern.MatchEvent("__gorapide_await_else__"),
				Process: RuleAgentProcess, Body: elseBranch.Body, allowTimingSuspension: true,
				allowProcessInterruptAllocation: true,
			}
			normalizedRule, canonicalRule, err := canonicalizeDeclarativeRule(component, fakeRule, stateTypes, functions)
			if err != nil {
				return nil, canonicalDeclarativeProcess{}, fmt.Errorf("%w: process %q state %q else alternative %q: %w", ErrInvalidDeclarativeProcess, declaration.ID, state.ID, elseBranch.ID, err)
			}
			if !ruleBodyIsTotalOrder(normalizedRule.Body.Outputs) {
				return nil, canonicalDeclarativeProcess{}, fmt.Errorf("%w: process %q state %q else alternative %q body is not a sequential event order", ErrInvalidDeclarativeProcess, declaration.ID, state.ID, elseBranch.ID)
			}
			normalizedState.Else = &AwaitAlternative{
				ID: elseBranch.ID, Body: normalizedRule.Body, Next: elseBranch.Next,
			}
			canonicalState.Else = &canonicalAwaitAlternative{
				ID: elseBranch.ID, Trigger: "",
				Assignments: canonicalRule.Assignments, Outputs: canonicalRule.Outputs,
				Statements: canonicalRule.Statements, Next: elseBranch.Next,
			}
		}
		sort.Slice(normalizedState.Alternatives, func(i, j int) bool {
			return normalizedState.Alternatives[i].ID < normalizedState.Alternatives[j].ID
		})
		sort.Slice(canonicalState.Alternatives, func(i, j int) bool {
			return canonicalState.Alternatives[i].ID < canonicalState.Alternatives[j].ID
		})
		normalized.States = append(normalized.States, normalizedState)
		canonical.States = append(canonical.States, canonicalState)
	}
	if !seenStates[declaration.Initial] {
		return nil, canonicalDeclarativeProcess{}, fmt.Errorf("%w: process %q initial state %q is missing", ErrInvalidDeclarativeProcess, declaration.ID, declaration.Initial)
	}
	for _, state := range normalized.States {
		for _, alternative := range state.Alternatives {
			if alternative.Next != "" && !seenStates[alternative.Next] {
				return nil, canonicalDeclarativeProcess{}, fmt.Errorf("%w: process %q alternative %s.%s references missing next state %q", ErrInvalidDeclarativeProcess, declaration.ID, state.ID, alternative.ID, alternative.Next)
			}
		}
		if state.Else != nil && state.Else.Next != "" && !seenStates[state.Else.Next] {
			return nil, canonicalDeclarativeProcess{}, fmt.Errorf("%w: process %q else alternative %s.%s references missing next state %q", ErrInvalidDeclarativeProcess, declaration.ID, state.ID, state.Else.ID, state.Else.Next)
		}
	}
	sort.Slice(normalized.States, func(i, j int) bool { return normalized.States[i].ID < normalized.States[j].ID })
	sort.Slice(canonical.States, func(i, j int) bool { return canonical.States[i].ID < canonical.States[j].ID })
	return normalized, canonical, nil
}

func copyAwaitAlternative(alternative AwaitAlternative) AwaitAlternative {
	alternative.Guard = copyRuleValuePointer(alternative.Guard)
	alternative.Body = copyRuleBody(alternative.Body)
	return alternative
}

func copyProcessState(state ProcessState) ProcessState {
	state.Alternatives = append([]AwaitAlternative(nil), state.Alternatives...)
	for i := range state.Alternatives {
		state.Alternatives[i] = copyAwaitAlternative(state.Alternatives[i])
	}
	if state.Else != nil {
		copy := copyAwaitAlternative(*state.Else)
		state.Else = &copy
	}
	return state
}

func ruleBodyIsTotalOrder(outputs []RuleOutput) bool {
	children := make(map[string][]string, len(outputs))
	for _, output := range outputs {
		for _, cause := range output.Causes {
			children[cause] = append(children[cause], output.ID)
		}
	}
	reaches := func(from, to string) bool {
		visited := make(map[string]bool)
		queue := append([]string(nil), children[from]...)
		for len(queue) > 0 {
			current := queue[0]
			queue = queue[1:]
			if current == to {
				return true
			}
			if visited[current] {
				continue
			}
			visited[current] = true
			queue = append(queue, children[current]...)
		}
		return false
	}
	for i := range outputs {
		for j := i + 1; j < len(outputs); j++ {
			if !reaches(outputs[i].ID, outputs[j].ID) && !reaches(outputs[j].ID, outputs[i].ID) {
				return false
			}
		}
	}
	return true
}

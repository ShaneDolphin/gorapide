package arch

import (
	"bytes"
	"container/heap"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/constraint"
	"github.com/ShaneDolphin/gorapide/pattern"
)

const (
	CompatibilityProfile       = "stanford-rapide-1.0"
	ExecutionJournalFormat     = "gorapide.execution-journal.v6"
	ExecutionArtifactFormat    = "gorapide.execution-artifact.v70"
	DeterministicEngineVersion = "gorapide.deterministic-engine.v244"
	RuleSelectionPolicy        = "gorapide.rule-selection.first-earliest-maximal-canonical.v1"
	ChoiceResolutionPolicy     = "gorapide.choice-resolution.explicit-or-canonical.v1"
	SemanticStepPolicy         = "gorapide.semantic-step.ready-observation-or-continuation.v2"
)

var (
	ErrUnsupportedDeterministicModel = errors.New("model uses a construct outside the deterministic execution subset")
	ErrInvalidExecutionJournal       = errors.New("invalid deterministic execution journal")
	ErrModelDigestMismatch           = errors.New("execution journal model digest does not match architecture")
	ErrExecutionLimit                = errors.New("deterministic execution firing limit reached")
	ErrReplayMismatch                = errors.New("replayed artifact digest does not match expected digest")
	ErrChoiceScheduleMismatch        = errors.New("explicit choice schedule does not match execution")
	ErrUnsupportedRapideType         = errors.New("Rapide type is outside the deterministic type subset")
	ErrActionTypeMismatch            = errors.New("event values do not match the declared action type")
)

// InputEvent is one explicit environment or component input occurrence. Causes
// refer to other InputEvent keys and define the input partial order. Slice order
// has no semantic meaning.
type InputEvent struct {
	Key     string                 `json:"key"`
	Source  string                 `json:"source"`
	Action  string                 `json:"action"`
	Params  map[string]any         `json:"params"`
	Causes  []string               `json:"causes"`
	Timings []gorapide.EventTiming `json:"timings,omitempty"`
}

// ExecutionLimits makes termination bounds part of the replayable input.
type ExecutionLimits struct {
	MaxFirings                uint64 `json:"max_firings"`
	MaxStatements             uint64 `json:"max_statements"`
	MaxConsistentCuts         uint64 `json:"max_consistent_cuts"`
	MaxOptionalCutOccurrences uint64 `json:"max_optional_cut_occurrences"`
}

// ChoiceDecision selects one stable alternative at a choice point discovered
// during execution. Point and Selection values are obtained from a prior audit
// result or a deterministic exploration result.
type ChoiceDecision struct {
	Point     string `json:"point"`
	Selection string `json:"selection"`
}

// ClockAdvanceDirective selects one finite, absolute counter advance at the
// next semantic quiescence. Directives are ordered inputs: unlike declaration
// slices, their sequence is semantic. The target may not pass that clock's
// nearest pending timing deadline.
type ClockAdvanceDirective struct {
	Clock string `json:"clock"`
	To    uint64 `json:"to"`
}

// ExecutionJournal contains every non-model input used by deterministic
// execution. It is self-identifying and canonically serializable.
type ExecutionJournal struct {
	Format        string                  `json:"format"`
	Profile       string                  `json:"profile"`
	ModelDigest   string                  `json:"model_digest"`
	Limits        ExecutionLimits         `json:"limits"`
	Inputs        []InputEvent            `json:"inputs"`
	Choices       []ChoiceDecision        `json:"choices"`
	ClockAdvances []ClockAdvanceDirective `json:"clock_advances"`
}

type canonicalInputEvent struct {
	Key     string                          `json:"key"`
	Source  string                          `json:"source"`
	Action  string                          `json:"action"`
	Params  []gorapide.CanonicalParameter   `json:"params"`
	Causes  []string                        `json:"causes"`
	Timings []gorapide.CanonicalEventTiming `json:"timings,omitempty"`
}

type canonicalExecutionJournal struct {
	Format        string                           `json:"format"`
	Profile       string                           `json:"profile"`
	ModelDigest   string                           `json:"model_digest"`
	Limits        ExecutionLimits                  `json:"limits"`
	Inputs        []canonicalInputEvent            `json:"inputs"`
	Choices       []ChoiceDecision                 `json:"choices"`
	ClockAdvances []canonicalClockAdvanceDirective `json:"clock_advances"`
}

type canonicalClockAdvanceDirective struct {
	Clock string `json:"clock"`
	To    string `json:"to"`
}

// NewExecutionJournal constructs a journal with the supported format/profile.
func NewExecutionJournal(modelDigest string, maxFirings uint64, inputs ...InputEvent) ExecutionJournal {
	maxStatements := maxFirings * 1024
	if maxFirings > math.MaxUint64/1024 {
		maxStatements = math.MaxUint64
	}
	maxConsistentCuts := maxStatements
	return ExecutionJournal{
		Format:      ExecutionJournalFormat,
		Profile:     CompatibilityProfile,
		ModelDigest: modelDigest,
		Limits: ExecutionLimits{
			MaxFirings: maxFirings, MaxStatements: maxStatements,
			MaxConsistentCuts: maxConsistentCuts, MaxOptionalCutOccurrences: 64,
		},
		Inputs:        append([]InputEvent(nil), inputs...),
		Choices:       []ChoiceDecision{},
		ClockAdvances: []ClockAdvanceDirective{},
	}
}

// NewExecutionJournalWithLimits constructs a journal with both firing and
// ordinary-statement bounds explicitly selected by the caller.
func NewExecutionJournalWithLimits(modelDigest string, limits ExecutionLimits, inputs ...InputEvent) ExecutionJournal {
	journal := NewExecutionJournal(modelDigest, limits.MaxFirings, inputs...)
	if limits.MaxConsistentCuts == 0 {
		limits.MaxConsistentCuts = journal.Limits.MaxConsistentCuts
	}
	if limits.MaxOptionalCutOccurrences == 0 {
		limits.MaxOptionalCutOccurrences = journal.Limits.MaxOptionalCutOccurrences
	}
	journal.Limits = limits
	return journal
}

// MarshalCanonical validates and serializes the journal independently of map,
// input-slice, and cause-slice order.
func (journal ExecutionJournal) MarshalCanonical() ([]byte, error) {
	normalized, err := normalizeJournal(journal)
	if err != nil {
		return nil, err
	}
	canonical := canonicalExecutionJournal{
		Format: normalized.Format, Profile: normalized.Profile,
		ModelDigest: normalized.ModelDigest, Limits: normalized.Limits,
		Inputs:        make([]canonicalInputEvent, 0, len(normalized.Inputs)),
		Choices:       append([]ChoiceDecision{}, normalized.Choices...),
		ClockAdvances: make([]canonicalClockAdvanceDirective, len(normalized.ClockAdvances)),
	}
	for index, directive := range normalized.ClockAdvances {
		canonical.ClockAdvances[index] = canonicalClockAdvanceDirective{
			Clock: directive.Clock, To: strconv.FormatUint(directive.To, 10),
		}
	}
	for _, input := range normalized.Inputs {
		params, err := gorapide.CanonicalizeParameters(input.Params)
		if err != nil {
			return nil, fmt.Errorf("arch.ExecutionJournal.MarshalCanonical: input %q: %w", input.Key, err)
		}
		timings, err := gorapide.EncodeCanonicalEventTimings(input.Timings)
		if err != nil {
			return nil, fmt.Errorf("arch.ExecutionJournal.MarshalCanonical: input %q timing: %w", input.Key, err)
		}
		canonical.Inputs = append(canonical.Inputs, canonicalInputEvent{
			Key: input.Key, Source: input.Source, Action: input.Action,
			Params: params, Causes: append([]string(nil), input.Causes...), Timings: timings,
		})
	}
	data, err := json.Marshal(canonical)
	if err != nil {
		return nil, fmt.Errorf("arch.ExecutionJournal.MarshalCanonical: %w", err)
	}
	return data, nil
}

// ParseExecutionJournal parses only the exact canonical journal encoding.
// This prevents duplicate keys, numeric type erasure, whitespace variants,
// unknown fields, and unordered inputs from acquiring semantic meaning.
func ParseExecutionJournal(data []byte) (ExecutionJournal, error) {
	var canonical canonicalExecutionJournal
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&canonical); err != nil {
		return ExecutionJournal{}, fmt.Errorf("%w: %v", ErrInvalidExecutionJournal, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			err = fmt.Errorf("multiple JSON values")
		}
		return ExecutionJournal{}, fmt.Errorf("%w: %v", ErrInvalidExecutionJournal, err)
	}
	reencoded, err := json.Marshal(canonical)
	if err != nil || !bytes.Equal(reencoded, data) {
		return ExecutionJournal{}, fmt.Errorf("%w: input is not the canonical byte encoding", ErrInvalidExecutionJournal)
	}
	journal := ExecutionJournal{
		Format: canonical.Format, Profile: canonical.Profile,
		ModelDigest: canonical.ModelDigest, Limits: canonical.Limits,
		Inputs:        make([]InputEvent, 0, len(canonical.Inputs)),
		Choices:       append([]ChoiceDecision{}, canonical.Choices...),
		ClockAdvances: make([]ClockAdvanceDirective, len(canonical.ClockAdvances)),
	}
	for index, directive := range canonical.ClockAdvances {
		to, err := strconv.ParseUint(directive.To, 10, 64)
		if err != nil {
			return ExecutionJournal{}, fmt.Errorf("%w: clock advance %d target %q: %v", ErrInvalidExecutionJournal, index, directive.To, err)
		}
		journal.ClockAdvances[index] = ClockAdvanceDirective{Clock: directive.Clock, To: to}
	}
	for _, input := range canonical.Inputs {
		params, err := gorapide.DecodeCanonicalParameters(input.Params)
		if err != nil {
			return ExecutionJournal{}, fmt.Errorf("%w: input %q: %v", ErrInvalidExecutionJournal, input.Key, err)
		}
		timings, err := gorapide.DecodeCanonicalEventTimings(input.Timings)
		if err != nil {
			return ExecutionJournal{}, fmt.Errorf("%w: input %q timing: %v", ErrInvalidExecutionJournal, input.Key, err)
		}
		journal.Inputs = append(journal.Inputs, InputEvent{
			Key: input.Key, Source: input.Source, Action: input.Action,
			Params: params, Causes: append([]string(nil), input.Causes...), Timings: timings,
		})
	}
	normalized, err := normalizeJournal(journal)
	if err != nil {
		return ExecutionJournal{}, err
	}
	roundTrip, err := normalized.MarshalCanonical()
	if err != nil || !bytes.Equal(roundTrip, data) {
		return ExecutionJournal{}, fmt.Errorf("%w: journal content is not canonically ordered", ErrInvalidExecutionJournal)
	}
	return normalized, nil
}

// SemanticDigest returns the SHA-256 digest of the canonical journal.
func (journal ExecutionJournal) SemanticDigest() (string, error) {
	data, err := journal.MarshalCanonical()
	if err != nil {
		return "", err
	}
	return digestBytes(data), nil
}

// GeneratedEventRecord identifies one body-local rule output occurrence.
type GeneratedEventRecord struct {
	OutputID  string `json:"output_id"`
	EventID   string `json:"event_id"`
	Exception bool   `json:"exception,omitempty"`
}

// FiringRecord is an auditable connection or declarative-rule transition
// selected by the deterministic semantic worklist.
type FiringRecord struct {
	Sequence          uint64                     `json:"sequence"`
	Transition        string                     `json:"transition"`
	ConnectionID      string                     `json:"connection_id,omitempty"`
	ConnectionKind    string                     `json:"connection_kind,omitempty"`
	ConnectionScope   string                     `json:"connection_scope,omitempty"`
	RuleID            string                     `json:"rule_id,omitempty"`
	RuleProcess       string                     `json:"rule_process,omitempty"`
	ProcessID         string                     `json:"process_id,omitempty"`
	ProcessState      string                     `json:"process_state,omitempty"`
	AlternativeID     string                     `json:"alternative_id,omitempty"`
	TriggerID         string                     `json:"trigger_id,omitempty"`
	TriggerSource     string                     `json:"trigger_source,omitempty"`
	TriggerAction     string                     `json:"trigger_action,omitempty"`
	MatchedEvents     []string                   `json:"matched_events,omitempty"`
	Bindings          []pattern.CanonicalBinding `json:"bindings,omitempty"`
	Target            string                     `json:"target"`
	ResultID          string                     `json:"result_id,omitempty"`
	Generated         []GeneratedEventRecord     `json:"generated,omitempty"`
	Scheduled         []ScheduledEventPlanRecord `json:"scheduled,omitempty"`
	CanceledSchedules []string                   `json:"canceled_schedules,omitempty"`
	Suspensions       []ProcessSuspensionRecord  `json:"suspensions,omitempty"`
	Switches          []ProcessSwitchRecord      `json:"switches,omitempty"`
	StateReads        []StateReadRecord          `json:"state_reads,omitempty"`
	StateWrites       []StateWriteRecord         `json:"state_writes,omitempty"`
	Completion        string                     `json:"completion,omitempty"`
	ExceptionEventID  string                     `json:"exception_event_id,omitempty"`
}

// ModuleConstraintRecord binds one generated component instance to the closed
// constraint decision over that module's own visible computation.
type ModuleConstraintRecord struct {
	ComponentID string                                  `json:"component_id"`
	Report      constraint.CanonicalConstraintSetReport `json:"report"`
}

// ArchitectureConstraintRecord binds one child architecture instance to the
// decision over that instance's owner-scoped visible computation.
type ArchitectureConstraintRecord struct {
	ArchitectureInstance string                                  `json:"architecture_instance"`
	Report               constraint.CanonicalConstraintSetReport `json:"report"`
}

// ExecutionResult contains the fresh semantic poset and its reproducibility
// envelope. The architecture's legacy runtime state is not read or mutated.
type ExecutionResult struct {
	Poset                   *gorapide.Poset
	Profile                 string
	ModelDigest             string
	JournalDigest           string
	Firings                 []FiringRecord
	Consumption             CanonicalRuleConsumption
	RuleSelection           string
	SemanticStepPolicy      string
	Choices                 []ChoiceResolution
	Processes               []ProcessExecutionRecord
	Modules                 []ModuleLifecycleRecord
	Contexts                []CommunicationContextRecord
	ExceptionPropagations   []ExceptionPropagationRecord
	State                   []StateRecord
	StateOperations         []StateOperationRecord
	Iterators               []IteratorStateRecord
	Constraints             *constraint.CanonicalConstraintSetReport
	ArchitectureConstraints []ArchitectureConstraintRecord
	ModuleConstraints       []ModuleConstraintRecord
	StatementSteps          uint64
	ClockPolicy             string
	Clocks                  []ClockStateRecord
	ClockAdvances           []ClockAdvanceRecord
	ScheduledEvents         []ScheduledEventRecord
}

type canonicalExecutionArtifact struct {
	Format                  string                                   `json:"format"`
	Engine                  string                                   `json:"engine"`
	Profile                 string                                   `json:"profile"`
	ModelDigest             string                                   `json:"model_digest"`
	JournalDigest           string                                   `json:"journal_digest"`
	Poset                   gorapide.CanonicalPoset                  `json:"poset"`
	Firings                 []FiringRecord                           `json:"firings"`
	Consumption             CanonicalRuleConsumption                 `json:"rule_consumption"`
	RuleSelection           string                                   `json:"rule_selection"`
	SemanticStepPolicy      string                                   `json:"semantic_step_policy"`
	ChoicePolicy            string                                   `json:"choice_policy"`
	Choices                 []ChoiceResolution                       `json:"choices"`
	Processes               []ProcessExecutionRecord                 `json:"processes"`
	Modules                 []ModuleLifecycleRecord                  `json:"modules"`
	Contexts                []CommunicationContextRecord             `json:"communication_contexts"`
	ExceptionPropagations   []ExceptionPropagationRecord             `json:"exception_propagations"`
	State                   []StateRecord                            `json:"state"`
	StateOperations         []StateOperationRecord                   `json:"state_operations"`
	Iterators               []IteratorStateRecord                    `json:"iterators"`
	Constraints             *constraint.CanonicalConstraintSetReport `json:"constraint_report"`
	ArchitectureConstraints []ArchitectureConstraintRecord           `json:"architecture_constraint_reports"`
	ModuleConstraints       []ModuleConstraintRecord                 `json:"module_constraint_reports"`
	StatementSteps          uint64                                   `json:"statement_steps"`
	ClockPolicy             string                                   `json:"clock_policy"`
	Clocks                  []ClockStateRecord                       `json:"clocks"`
	ClockAdvances           []ClockAdvanceRecord                     `json:"clock_advances"`
	ScheduledEvents         []ScheduledEventRecord                   `json:"scheduled_events"`
}

// MarshalCanonical returns the byte-identical complete execution artifact.
func (result *ExecutionResult) MarshalCanonical() ([]byte, error) {
	if result == nil || result.Poset == nil {
		return nil, fmt.Errorf("arch.ExecutionResult.MarshalCanonical: result or poset is nil")
	}
	poset, err := result.Poset.Canonical()
	if err != nil {
		return nil, err
	}
	artifact := canonicalExecutionArtifact{
		Format:                  ExecutionArtifactFormat,
		Engine:                  DeterministicEngineVersion,
		Profile:                 result.Profile,
		ModelDigest:             result.ModelDigest,
		JournalDigest:           result.JournalDigest,
		Poset:                   poset,
		Firings:                 append([]FiringRecord(nil), result.Firings...),
		Consumption:             result.Consumption,
		RuleSelection:           result.RuleSelection,
		SemanticStepPolicy:      result.SemanticStepPolicy,
		ChoicePolicy:            ChoiceResolutionPolicy,
		Choices:                 append([]ChoiceResolution{}, result.Choices...),
		Processes:               append([]ProcessExecutionRecord{}, result.Processes...),
		Modules:                 append([]ModuleLifecycleRecord{}, result.Modules...),
		Contexts:                append([]CommunicationContextRecord{}, result.Contexts...),
		ExceptionPropagations:   append([]ExceptionPropagationRecord{}, result.ExceptionPropagations...),
		State:                   append([]StateRecord{}, result.State...),
		StateOperations:         append([]StateOperationRecord{}, result.StateOperations...),
		Iterators:               append([]IteratorStateRecord{}, result.Iterators...),
		Constraints:             result.Constraints,
		ArchitectureConstraints: append([]ArchitectureConstraintRecord{}, result.ArchitectureConstraints...),
		ModuleConstraints:       append([]ModuleConstraintRecord{}, result.ModuleConstraints...),
		StatementSteps:          result.StatementSteps,
		ClockPolicy:             result.ClockPolicy,
		Clocks:                  append([]ClockStateRecord(nil), result.Clocks...),
		ClockAdvances:           append([]ClockAdvanceRecord{}, result.ClockAdvances...),
		ScheduledEvents:         append([]ScheduledEventRecord{}, result.ScheduledEvents...),
	}
	data, err := json.Marshal(artifact)
	if err != nil {
		return nil, fmt.Errorf("arch.ExecutionResult.MarshalCanonical: %w", err)
	}
	return data, nil
}

// ArtifactDigest returns the digest of the full execution artifact.
func (result *ExecutionResult) ArtifactDigest() (string, error) {
	data, err := result.MarshalCanonical()
	if err != nil {
		return "", err
	}
	return digestBytes(data), nil
}

type canonicalParamDecl struct {
	Name           string                   `json:"name"`
	Type           string                   `json:"type"`
	StructuralType json.RawMessage          `json:"structural_type,omitempty"`
	Default        *gorapide.CanonicalValue `json:"default,omitempty"`
}

type canonicalActionDecl struct {
	Name   string               `json:"name"`
	Kind   int                  `json:"kind"`
	Params []canonicalParamDecl `json:"params"`
}

type canonicalFunctionDecl struct {
	Name       string               `json:"name"`
	Kind       int                  `json:"kind"`
	Params     []canonicalParamDecl `json:"params"`
	ReturnType string               `json:"return_type"`
}

type canonicalServiceDecl struct {
	Name      string                  `json:"name"`
	Actions   []canonicalActionDecl   `json:"actions"`
	Functions []canonicalFunctionDecl `json:"functions"`
}

type canonicalInterfaceDecl struct {
	Name           string                  `json:"name"`
	Actions        []canonicalActionDecl   `json:"actions"`
	Functions      []canonicalFunctionDecl `json:"functions"`
	Services       []canonicalServiceDecl  `json:"services"`
	StructuralType json.RawMessage         `json:"structural_type,omitempty"`
}

type canonicalComponentDecl struct {
	ID                       string                                   `json:"id"`
	ArchitectureInstance     string                                   `json:"architecture_instance,omitempty"`
	Interface                canonicalInterfaceDecl                   `json:"interface"`
	Membership               *canonicalModuleMembership               `json:"module_membership,omitempty"`
	Functions                []canonicalFunctionImplementation        `json:"functions"`
	Exceptions               []canonicalExceptionDeclaration          `json:"exceptions"`
	ModuleHandler            *canonicalExceptionHandler               `json:"module_handler,omitempty"`
	InitializationParameters []canonicalModuleInitializationParameter `json:"initialization_parameters,omitempty"`
	Initial                  []canonicalRuleStatement                 `json:"initial,omitempty"`
	Final                    []canonicalRuleStatement                 `json:"final,omitempty"`
	Rules                    []canonicalDeclarativeRule               `json:"rules"`
	Processes                []canonicalDeclarativeProcess            `json:"processes"`
	ProcessMode              string                                   `json:"process_mode"`
	Clocks                   []canonicalBasicClockDeclaration         `json:"clocks"`
	State                    []canonicalStateDeclaration              `json:"state"`
	Constraints              *canonicalConstraintSetDecl              `json:"constraint_set,omitempty"`
}

type canonicalArchitectureInstanceDecl struct {
	ID                 string                          `json:"id"`
	Parent             string                          `json:"parent"`
	Generator          string                          `json:"generator"`
	GeneratorArguments []canonicalArchitectureArgument `json:"generator_arguments"`
	ReturnInterface    canonicalInterfaceDecl          `json:"return_interface"`
	Constraints        *canonicalConstraintSetDecl     `json:"constraint_set,omitempty"`
	Initial            []canonicalRuleStatement        `json:"initial,omitempty"`
}

type canonicalArchitectureInitialExceptionCatalog struct {
	Owner      string                          `json:"owner"`
	Exceptions []canonicalExceptionDeclaration `json:"exceptions"`
}

type canonicalConnectionDecl struct {
	ID                   string                   `json:"id"`
	Kind                 int                      `json:"kind"`
	Scope                int                      `json:"scope"`
	ArchitectureInstance string                   `json:"architecture_instance"`
	From                 string                   `json:"from"`
	To                   string                   `json:"to"`
	Trigger              string                   `json:"trigger"`
	ActionName           string                   `json:"action_name"`
	Parameters           []canonicalRuleParameter `json:"parameters"`
}

type canonicalArchitecture struct {
	Format                   string                                         `json:"format"`
	Profile                  string                                         `json:"profile"`
	Name                     string                                         `json:"name"`
	GeneratorArguments       []canonicalArchitectureArgument                `json:"generator_arguments"`
	ReturnInterface          canonicalInterfaceDecl                         `json:"return_interface"`
	Initial                  []canonicalRuleStatement                       `json:"initial,omitempty"`
	InitialExceptionCatalogs []canonicalArchitectureInitialExceptionCatalog `json:"initial_exception_catalogs,omitempty"`
	ArchitectureInstances    []canonicalArchitectureInstanceDecl            `json:"architecture_instances"`
	Components               []canonicalComponentDecl                       `json:"components"`
	Connections              []canonicalConnectionDecl                      `json:"connections"`
	FunctionConnections      []canonicalFunctionConnection                  `json:"function_connections"`
	FiniteIterators          []canonicalFiniteIteratorModule                `json:"finite_iterators"`
	IteratorGenerators       []canonicalFiniteIteratorGenerator             `json:"finite_iterator_generators"`
	ConstraintSet            *canonicalConstraintSetDecl                    `json:"constraint_set,omitempty"`
}

type canonicalArchitectureArgument struct {
	Name  string                  `json:"name"`
	Type  string                  `json:"type"`
	Value gorapide.CanonicalValue `json:"value"`
}

type canonicalConstraintSetDecl struct {
	Name   string `json:"name"`
	Digest string `json:"digest"`
}

type deterministicModel struct {
	name                          string
	digest                        string
	canonical                     []byte
	returnInterface               *InterfaceDecl
	architectureInstances         map[string]ArchitectureInstanceDeclaration
	architectureInstanceIDs       []string
	architectureConstraints       map[string]*constraint.ConstraintSet
	architectureInitials          map[string][]Statement
	architectureInitialExceptions map[string][]ExceptionDeclaration
	componentArchitectures        map[string]string
	staticModuleGenerators        map[string]string
	staticRecordObjects           map[string][]gorapide.RapideRecordObjectDeclaration
	components                    map[string]*Component
	componentIDs                  []string
	connections                   []*Connection
	rules                         map[string][]*DeclarativeRule
	processes                     map[string][]*DeclarativeProcess
	moduleHandlers                map[string]ExceptionHandler
	processModes                  map[string]ModuleProcessMode
	stateDeclarations             map[string][]StateDeclaration
	initializationParameters      map[string][]ModuleInitializationParameter
	functions                     map[string]map[string]*FunctionImplementation
	callables                     map[string]map[string]*FunctionImplementation
	initialStatements             map[string][]Statement
	finalStatements               map[string][]Statement
	constraintSet                 *constraint.ConstraintSet
	moduleConstraints             map[string]*constraint.ConstraintSet
	basicClocks                   map[string][]BasicClockDeclaration
	finiteIterators               map[string]*FiniteIteratorModule
	iteratorGenerators            map[string]*FiniteIteratorGenerator
}

type functionExecutionRuntime struct {
	model                   *deterministicModel
	poset                   *gorapide.Poset
	components              map[string]*Component
	connections             []*Connection
	callables               map[string]map[string]*FunctionImplementation
	state                   moduleStateRuntime
	startupFrontiers        map[string][]gorapide.EventID
	frontiers               *causalFrontierRegistry
	modules                 map[string]gorapide.RapideModuleValue
	recordObjects           map[string]map[string]gorapide.RapideRecordValue
	clocks                  *deterministicClockKernel
	iterators               map[string]*finiteRangeIterator
	iteratorGenerators      map[string]*FiniteIteratorGenerator
	lifecycle               *moduleLifecycleRegistry
	contexts                *communicationContextRuntime
	processes               map[string][]*processRuntime
	propagations            *exceptionPropagationRuntime
	choices                 *choiceResolver
	observed                map[string]gorapide.EventSet
	architectureSeen        *gorapide.EventSet
	observedOccurrences     map[gorapide.EventID]bool
	firings                 *[]FiringRecord
	connectionFired         map[string]bool
	connectionPipe          map[string]gorapide.EventID
	architectureScopeClosed bool
	postScopeComponents     map[string]bool
	// postScopeFinalConnectionSources is the exact set of allocation identities
	// whose module-owned action connections remain eligible for final-part
	// occurrences after architecture scope loss. It never admits architecture
	// wiring, journal input, processes, or rules for a finalized module.
	postScopeFinalConnectionSources map[string]bool
	maxFirings                      uint64
	moduleParents                   map[string]string
	moduleTemplates                 map[string]string
}

// templateComponentID returns the sealed component declaration instantiated by
// one execution component. Static components retain their architecture ID;
// allocator-created modules execute under their allocation identity and map
// back to the canonical generator specialization stored in the model.
func (runtime *functionExecutionRuntime) templateComponentID(componentID string) string {
	if runtime != nil && runtime.moduleTemplates != nil {
		if templateID := runtime.moduleTemplates[componentID]; templateID != "" {
			return templateID
		}
	}
	return componentID
}

// ArchitectureStartAction is Stanford Rapide's predefined system action for
// the event generated when a fresh architecture instance is created.
const ArchitectureStartAction = "Start"

func newArchitectureStartEvent(
	profile, modelDigest string,
	module gorapide.RapideModuleValue,
) (*gorapide.Event, error) {
	return newArchitectureInstanceStartEvent(profile, modelDigest, ArchitectureInterfaceID, module, nil)
}

func newArchitectureInstanceStartEvent(
	profile string,
	modelDigest string,
	instanceID string,
	module gorapide.RapideModuleValue,
	parent *gorapide.Event,
) (*gorapide.Event, error) {
	if module.Identity() == "" {
		return nil, fmt.Errorf("architecture %q has no allocation identity", instanceID)
	}
	causes := make([]gorapide.EventID, 0, 1)
	if parent != nil {
		causes = append(causes, parent.ID)
	}
	event, err := gorapide.NewDeterministicEvent(gorapide.EventProvenance{
		Profile: profile, Model: modelDigest, Instance: module.Identity(),
		ObservationSource: architectureInstanceAuditID(instanceID),
		Action:            ArchitectureStartAction, Occurrence: "architecture:start", Causes: causes,
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("architecture %q Start: %w", instanceID, err)
	}
	return event, nil
}

func createArchitectureStartEvents(
	profile string,
	model *deterministicModel,
	poset *gorapide.Poset,
	depths map[gorapide.EventID]uint64,
) (map[string]gorapide.RapideModuleValue, map[string]*gorapide.Event, error) {
	rootModule, err := gorapide.NewRapideModuleValue(gorapide.ModuleAllocationProvenance{
		Profile: profile, Model: model.digest, Parent: moduleEnvironmentRoot,
		Generator: model.name, Occurrence: "architecture:root",
	})
	if err != nil {
		return nil, nil, fmt.Errorf("root architecture allocation: %w", err)
	}
	root, err := newArchitectureStartEvent(profile, model.digest, rootModule)
	if err != nil {
		return nil, nil, err
	}
	if err := poset.AddEvent(root); err != nil {
		return nil, nil, fmt.Errorf("architecture Start: %w", err)
	}
	depths[root.ID] = 1
	modules := map[string]gorapide.RapideModuleValue{ArchitectureInterfaceID: rootModule}
	starts := map[string]*gorapide.Event{ArchitectureInterfaceID: root}
	for _, instanceID := range model.architectureInstanceIDs {
		declaration := model.architectureInstances[instanceID]
		parent := starts[declaration.Parent]
		parentModule := modules[declaration.Parent]
		if parent == nil {
			return nil, nil, fmt.Errorf("architecture instance %q has unavailable parent Start %q", instanceID, declaration.Parent)
		}
		module, err := gorapide.NewRapideModuleValue(gorapide.ModuleAllocationProvenance{
			Profile: profile, Model: model.digest, Parent: parentModule.Identity(),
			Generator: declaration.Generator, Occurrence: "architecture-instance:" + instanceID,
			Causes: []gorapide.EventID{parent.ID},
		})
		if err != nil {
			return nil, nil, fmt.Errorf("architecture instance %q allocation: %w", instanceID, err)
		}
		start, err := newArchitectureInstanceStartEvent(profile, model.digest, instanceID, module, parent)
		if err != nil {
			return nil, nil, err
		}
		if err := poset.AddEventWithCause(start, parent.ID); err != nil {
			return nil, nil, fmt.Errorf("architecture instance %q Start: %w", instanceID, err)
		}
		depths[start.ID] = depths[parent.ID] + 1
		modules[instanceID] = module
		starts[instanceID] = start
	}
	return modules, starts, nil
}

const staticModuleAuditPrefix = "$module/"

func staticModuleAuditID(componentID string) string {
	return staticModuleAuditPrefix + componentID
}

func newStaticModuleStartEvent(
	profile string,
	modelDigest string,
	componentID string,
	generator string,
	module gorapide.RapideModuleValue,
	parent *gorapide.Event,
) (*gorapide.Event, error) {
	if componentID == "" || generator == "" || module.Identity() == "" || parent == nil {
		return nil, fmt.Errorf("static module %q has incomplete Start provenance", componentID)
	}
	event, err := gorapide.NewDeterministicEvent(gorapide.EventProvenance{
		Profile: profile, Model: modelDigest, Instance: module.Identity(),
		ObservationSource: staticModuleAuditID(componentID),
		Action:            ArchitectureStartAction, Occurrence: "module-generator:" + generator + ":start",
		Causes: []gorapide.EventID{parent.ID},
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("static module %q Start: %w", componentID, err)
	}
	return event, nil
}

// createStaticModuleStartEvents allocates every statically elaborated
// module-generator result and materializes its predefined Start occurrence.
// The allocation identity is the occurrence generator; $module/<component>
// remains only the qualified audit observation through which the system action
// is currently exposed.
func createStaticModuleStartEvents(
	profile string,
	model *deterministicModel,
	architectureModules map[string]gorapide.RapideModuleValue,
	architectureStarts map[string]*gorapide.Event,
	poset *gorapide.Poset,
	depths map[gorapide.EventID]uint64,
) (map[string]gorapide.RapideModuleValue, map[string]*gorapide.Event, error) {
	modules := make(map[string]gorapide.RapideModuleValue, len(model.staticModuleGenerators))
	starts := make(map[string]*gorapide.Event, len(model.staticModuleGenerators))
	for _, componentID := range model.componentIDs {
		generator := model.staticModuleGenerators[componentID]
		if generator == "" {
			continue
		}
		owner := model.componentArchitectures[componentID]
		if owner == "" {
			owner = ArchitectureInterfaceID
		}
		parent := architectureStarts[owner]
		if parent == nil {
			return nil, nil, fmt.Errorf("static module %q has unavailable architecture Start %q", componentID, owner)
		}
		parentModule := architectureModules[owner]
		if parentModule.Identity() == "" {
			return nil, nil, fmt.Errorf("static module %q has unavailable architecture allocation %q", componentID, owner)
		}
		module, err := gorapide.NewRapideModuleValue(gorapide.ModuleAllocationProvenance{
			Profile: profile, Model: model.digest, Parent: parentModule.Identity(),
			Generator: generator, Occurrence: "component:" + componentID,
			Causes: []gorapide.EventID{parent.ID},
		})
		if err != nil {
			return nil, nil, fmt.Errorf("static module %q allocation: %w", componentID, err)
		}
		start, err := newStaticModuleStartEvent(profile, model.digest, componentID, generator, module, parent)
		if err != nil {
			return nil, nil, err
		}
		if err := poset.AddEventWithCause(start, parent.ID); err != nil {
			return nil, nil, fmt.Errorf("static module %q Start: %w", componentID, err)
		}
		depths[start.ID] = depths[parent.ID] + 1
		modules[componentID] = module
		starts[componentID] = start
	}
	return modules, starts, nil
}

func createStaticRecordObjects(
	profile string,
	model *deterministicModel,
	modules map[string]gorapide.RapideModuleValue,
	moduleStarts map[string]*gorapide.Event,
	poset *gorapide.Poset,
	depths map[gorapide.EventID]uint64,
) (
	map[string]map[string]gorapide.RapideRecordValue,
	map[string]*gorapide.Event,
	[]*gorapide.Event,
	error,
) {
	objects := make(map[string]map[string]gorapide.RapideRecordValue, len(model.staticRecordObjects))
	componentStarts := make(map[string]*gorapide.Event, len(moduleStarts))
	for _, componentID := range model.componentIDs {
		if start := moduleStarts[componentID]; start != nil {
			componentStarts[componentID] = start
		}
	}
	starts := make([]*gorapide.Event, 0)
	for _, componentID := range model.componentIDs {
		declarations := model.staticRecordObjects[componentID]
		if len(declarations) == 0 {
			continue
		}
		frontier := moduleStarts[componentID]
		if frontier == nil {
			return nil, nil, nil, fmt.Errorf("component %q Record objects have no enclosing static module Start", componentID)
		}
		objects[componentID] = make(map[string]gorapide.RapideRecordValue, len(declarations))
		parent := modules[componentID]
		if parent.Identity() == "" {
			return nil, nil, nil, fmt.Errorf("component %q Record objects have no enclosing module allocation", componentID)
		}
		for _, declaration := range declarations {
			causes := []gorapide.EventID{frontier.ID}
			value, err := declaration.Allocate(gorapide.ModuleAllocationProvenance{
				Profile: profile, Model: model.digest, Parent: parent.Identity(),
				Generator: "record-literal", Occurrence: "module-object:" + declaration.Name(),
				Causes: causes,
			})
			if err != nil {
				return nil, nil, nil, fmt.Errorf("component %q Record object %q allocation: %w", componentID, declaration.Name(), err)
			}
			if _, duplicate := objects[componentID][declaration.Name()]; duplicate {
				return nil, nil, nil, fmt.Errorf("component %q has duplicate Record object %q", componentID, declaration.Name())
			}
			start, err := gorapide.NewDeterministicEvent(gorapide.EventProvenance{
				Profile: profile, Model: model.digest, Instance: value.Identity(),
				Action:     ArchitectureStartAction,
				Occurrence: "record-object:" + declaration.Name() + ":start", Causes: causes,
			}, nil)
			if err != nil {
				return nil, nil, nil, fmt.Errorf("component %q Record object %q Start: %w", componentID, declaration.Name(), err)
			}
			if err := poset.AddEventWithCause(start, causes...); err != nil {
				return nil, nil, nil, fmt.Errorf("component %q Record object %q Start: %w", componentID, declaration.Name(), err)
			}
			depths[start.ID] = depths[frontier.ID] + 1
			objects[componentID][declaration.Name()] = value
			starts = append(starts, start)
			frontier = start
		}
		componentStarts[componentID] = frontier
	}
	return objects, componentStarts, starts, nil
}

func componentStartupFrontiers(
	model *deterministicModel,
	architectureStarts map[string]*gorapide.Event,
	componentStarts map[string]*gorapide.Event,
) (map[string][]gorapide.EventID, error) {
	result := make(map[string][]gorapide.EventID, len(model.componentIDs))
	for _, componentID := range model.componentIDs {
		if start := componentStarts[componentID]; start != nil {
			result[componentID] = []gorapide.EventID{start.ID}
			continue
		}
		owner := model.componentArchitectures[componentID]
		if owner == "" {
			owner = ArchitectureInterfaceID
		}
		start := architectureStarts[owner]
		if start == nil {
			return nil, fmt.Errorf("component %q has unavailable architecture Start %q", componentID, owner)
		}
		result[componentID] = []gorapide.EventID{start.ID}
	}
	return result, nil
}

func canonicalizeArchitectureInstances(
	declarations map[string]ArchitectureInstanceDeclaration,
	constraintSets map[string]*constraint.ConstraintSet,
) (map[string]ArchitectureInstanceDeclaration, []string, []canonicalArchitectureInstanceDecl, error) {
	lexicalIDs := make([]string, 0, len(declarations))
	for id := range declarations {
		lexicalIDs = append(lexicalIDs, id)
	}
	sort.Strings(lexicalIDs)
	normalized := make(map[string]ArchitectureInstanceDeclaration, len(lexicalIDs))
	for _, id := range lexicalIDs {
		declaration := declarations[id]
		localID, validID := deterministicArchitectureInstanceLocalID(id)
		if id != declaration.ID || !validID || declaration.Parent == "" ||
			(declaration.Parent != ArchitectureInterfaceID && !validDeterministicArchitectureInstanceID(declaration.Parent)) ||
			id != DeterministicArchitectureInstanceID(declaration.Parent, localID) ||
			!validModuleMembershipIdentifier(declaration.Generator) || declaration.ReturnInterface == nil {
			return nil, nil, nil, fmt.Errorf("invalid recursive architecture instance %q", id)
		}
		arguments, err := normalizeArchitectureGeneratorArguments(declaration.GeneratorArguments)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("architecture instance %q: %w", id, err)
		}
		returnInterface, err := cloneInterfaceDecl(declaration.ReturnInterface)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("architecture instance %q return interface: %w", id, err)
		}
		normalized[id] = ArchitectureInstanceDeclaration{
			ID: id, Parent: declaration.Parent, Generator: declaration.Generator,
			GeneratorArguments: arguments, ReturnInterface: returnInterface,
		}
	}
	for _, id := range lexicalIDs {
		parent := normalized[id].Parent
		if parent == ArchitectureInterfaceID {
			continue
		}
		if _, exists := normalized[parent]; !exists {
			return nil, nil, nil, fmt.Errorf("architecture instance %q references missing parent %q", id, parent)
		}
	}
	ids, err := architectureInstancePreOrder(normalized, lexicalIDs)
	if err != nil {
		return nil, nil, nil, err
	}
	canonical := make([]canonicalArchitectureInstanceDecl, 0, len(ids))
	for _, id := range ids {
		declaration := normalized[id]
		returnInterface, err := canonicalizeInterface(declaration.ReturnInterface)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("architecture instance %q return interface: %w", id, err)
		}
		canonicalArguments := make([]canonicalArchitectureArgument, 0, len(declaration.GeneratorArguments))
		for _, argument := range declaration.GeneratorArguments {
			value, err := gorapide.EncodeCanonicalValue(argument.Value)
			if err != nil {
				return nil, nil, nil, fmt.Errorf("architecture instance %q argument %q: %w", id, argument.Name, err)
			}
			canonicalArguments = append(canonicalArguments, canonicalArchitectureArgument{
				Name: argument.Name, Type: argument.Type, Value: value,
			})
		}
		canonicalDeclaration := canonicalArchitectureInstanceDecl{
			ID: id, Parent: declaration.Parent, Generator: declaration.Generator,
			GeneratorArguments: canonicalArguments, ReturnInterface: returnInterface,
		}
		if set := constraintSets[id]; set != nil {
			digest, err := set.DeterministicDigest()
			if err != nil {
				return nil, nil, nil, fmt.Errorf("architecture instance %q constraints: %w", id, err)
			}
			canonicalDeclaration.Constraints = &canonicalConstraintSetDecl{
				Name: set.Name, Digest: digest,
			}
		}
		canonical = append(canonical, canonicalDeclaration)
	}
	constraintOwners := make([]string, 0, len(constraintSets))
	for id := range constraintSets {
		constraintOwners = append(constraintOwners, id)
	}
	sort.Strings(constraintOwners)
	for _, id := range constraintOwners {
		if _, exists := declarations[id]; !exists {
			return nil, nil, nil, fmt.Errorf("constraints reference missing architecture instance %q", id)
		}
	}
	return normalized, ids, canonical, nil
}

func architectureInstancePreOrder(
	instances map[string]ArchitectureInstanceDeclaration,
	lexicalIDs []string,
) ([]string, error) {
	children := make(map[string][]string, len(instances)+1)
	for _, id := range lexicalIDs {
		children[instances[id].Parent] = append(children[instances[id].Parent], id)
	}
	for parent := range children {
		sort.Strings(children[parent])
	}
	result := make([]string, 0, len(instances))
	visiting := make(map[string]bool, len(instances))
	visited := make(map[string]bool, len(instances))
	var visit func(string) error
	visit = func(parent string) error {
		for _, child := range children[parent] {
			if visiting[child] {
				return fmt.Errorf("architecture instance parent cycle at %q", child)
			}
			if visited[child] {
				continue
			}
			visiting[child] = true
			result = append(result, child)
			if err := visit(child); err != nil {
				return err
			}
			visiting[child] = false
			visited[child] = true
		}
		return nil
	}
	if err := visit(ArchitectureInterfaceID); err != nil {
		return nil, err
	}
	if len(result) != len(instances) {
		for _, id := range lexicalIDs {
			if !visited[id] {
				return nil, fmt.Errorf("architecture instance %q is not reachable from %q", id, ArchitectureInterfaceID)
			}
		}
	}
	return result, nil
}

func architectureInstancePostOrder(
	instances map[string]ArchitectureInstanceDeclaration,
	preOrder []string,
) []string {
	children := make(map[string][]string, len(instances)+1)
	for _, id := range preOrder {
		children[instances[id].Parent] = append(children[instances[id].Parent], id)
	}
	for parent := range children {
		sort.Strings(children[parent])
	}
	result := make([]string, 0, len(instances)+1)
	var visit func(string)
	visit = func(parent string) {
		for _, child := range children[parent] {
			visit(child)
			result = append(result, child)
		}
	}
	visit(ArchitectureInterfaceID)
	return append(result, ArchitectureInterfaceID)
}

func canonicalizeArchitectureInitials(
	returnInterface *InterfaceDecl,
	instances map[string]ArchitectureInstanceDeclaration,
	instanceIDs []string,
	declarations map[string][]Statement,
	exceptionDeclarations map[string][]ExceptionDeclaration,
	callables map[string]map[string]*FunctionImplementation,
	componentArchitectures map[string]string,
) (map[string][]Statement, map[string][]ExceptionDeclaration, []canonicalRuleStatement, map[string][]canonicalRuleStatement, []canonicalArchitectureInitialExceptionCatalog, error) {
	declarationOwners := make([]string, 0, len(declarations))
	for owner := range declarations {
		declarationOwners = append(declarationOwners, owner)
	}
	sort.Strings(declarationOwners)
	for _, owner := range declarationOwners {
		if owner == ArchitectureInterfaceID {
			continue
		}
		if _, exists := instances[owner]; !exists {
			return nil, nil, nil, nil, nil, fmt.Errorf("initial part references missing architecture instance %q", owner)
		}
	}
	for owner := range exceptionDeclarations {
		if _, exists := declarations[owner]; !exists {
			return nil, nil, nil, nil, nil, fmt.Errorf("initial exception catalog references architecture %q without an initial part", owner)
		}
	}
	owners := append([]string(nil), instanceIDs...)
	owners = append(owners, ArchitectureInterfaceID)
	normalized := make(map[string][]Statement, len(declarations))
	normalizedExceptions := make(map[string][]ExceptionDeclaration, len(exceptionDeclarations))
	children := make(map[string][]canonicalRuleStatement, len(instanceIDs))
	canonicalExceptions := make([]canonicalArchitectureInitialExceptionCatalog, 0, len(exceptionDeclarations))
	var root []canonicalRuleStatement
	for _, owner := range owners {
		statements := declarations[owner]
		if statements == nil {
			continue
		}
		if len(statements) == 0 {
			return nil, nil, nil, nil, nil, fmt.Errorf("%w: architecture %q initial part is empty", ErrInvalidArchitectureInitial, owner)
		}
		iface := returnInterface
		if owner != ArchitectureInterfaceID {
			iface = instances[owner].ReturnInterface
		}
		component := NewComponent(architectureBoundaryID(owner), iface, nil)
		exceptions := exceptionDeclarations[owner]
		for _, declaration := range exceptions {
			if err := component.AddExceptionDeclaration(declaration); err != nil {
				return nil, nil, nil, nil, nil, fmt.Errorf("%w: architecture %q initial exception: %v", ErrInvalidArchitectureInitial, owner, err)
			}
		}
		encodedExceptions, err := canonicalizeExceptionDeclarations(component, exceptions)
		if err != nil {
			return nil, nil, nil, nil, nil, fmt.Errorf("%w: architecture %q initial exceptions: %v", ErrInvalidArchitectureInitial, owner, err)
		}
		normalizedStatements, canonicalStatements, err := canonicalizeRuleStatements(
			component, "architecture initial "+owner, statements, nil, nil, callables[component.ID], nil,
		)
		if err != nil {
			return nil, nil, nil, nil, nil, fmt.Errorf("%w: architecture %q: %v", ErrInvalidArchitectureInitial, owner, err)
		}
		if err := validateArchitectureInitialStatementSubset(
			component, normalizedStatements, nil, owner, callables,
			componentArchitectures, instances, make(map[string]bool),
		); err != nil {
			return nil, nil, nil, nil, nil, fmt.Errorf("architecture %q: %w", owner, err)
		}
		normalized[owner] = normalizedStatements
		if len(exceptions) != 0 {
			copied := make([]ExceptionDeclaration, len(exceptions))
			for index, declaration := range exceptions {
				copied[index] = ExceptionDeclaration{
					Declaration: declaration.Declaration, Name: declaration.Name,
					Params: nil,
				}
				params, cloneErr := cloneParamDecls(declaration.Params)
				if cloneErr != nil {
					return nil, nil, nil, nil, nil, fmt.Errorf("%w: architecture %q initial exception %q: %v", ErrInvalidArchitectureInitial, owner, declaration.Name, cloneErr)
				}
				copied[index].Params = params
			}
			normalizedExceptions[owner] = copied
			canonicalExceptions = append(canonicalExceptions, canonicalArchitectureInitialExceptionCatalog{
				Owner: owner, Exceptions: encodedExceptions,
			})
		}
		if owner == ArchitectureInterfaceID {
			root = canonicalStatements
		} else {
			children[owner] = canonicalStatements
		}
	}
	return normalized, normalizedExceptions, root, children, canonicalExceptions, nil
}

// DeterministicModelDigest validates the currently supported architecture
// subset and returns its canonical semantic digest.
func (a *Architecture) DeterministicModelDigest() (string, error) {
	prepared, err := a.PrepareDeterministic()
	if err != nil {
		return "", err
	}
	return prepared.DeterministicModelDigest()
}

func (a *Architecture) deterministicModel() (*deterministicModel, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	if a.running {
		return nil, fmt.Errorf("%w: architecture %q is running", ErrUnsupportedDeterministicModel, a.Name)
	}
	if len(a.subArchitectures) != 0 {
		return nil, fmt.Errorf("%w: subarchitectures are not implemented", ErrUnsupportedDeterministicModel)
	}
	if a.bindings != nil && len(a.bindings.ActiveBindings()) != 0 {
		return nil, fmt.Errorf("%w: dynamic bindings are not implemented", ErrUnsupportedDeterministicModel)
	}
	if a.checkerOpts != nil {
		return nil, fmt.Errorf("%w: constraint checker callbacks are outside the deterministic kernel", ErrUnsupportedDeterministicModel)
	}
	if len(a.onEvent) != 0 {
		return nil, fmt.Errorf("%w: observer callbacks are outside the deterministic kernel", ErrUnsupportedDeterministicModel)
	}

	componentIDs := make([]string, 0, len(a.components))
	sourceComponents := make(map[string]*Component, len(a.components))
	components := make(map[string]*Component, len(a.components))
	componentArchitectures := make(map[string]string, len(a.components))
	staticModuleGenerators := make(map[string]string, len(a.components))
	staticRecordObjects := make(map[string][]gorapide.RapideRecordObjectDeclaration, len(a.components))
	rules := make(map[string][]*DeclarativeRule, len(a.components))
	processes := make(map[string][]*DeclarativeProcess, len(a.components))
	processModes := make(map[string]ModuleProcessMode, len(a.components))
	basicClocks := make(map[string][]BasicClockDeclaration, len(a.components))
	stateDeclarations := make(map[string][]StateDeclaration, len(a.components))
	initializationParameters := make(map[string][]ModuleInitializationParameter, len(a.components))
	functions := make(map[string]map[string]*FunctionImplementation, len(a.components))
	functionSignatures := make(map[string]map[string]canonicalFunctionDecl, len(a.components))
	callables := make(map[string]map[string]*FunctionImplementation, len(a.components))
	stateTypesByComponent := make(map[string]map[string]string, len(a.components))
	rawRules := make(map[string][]*DeclarativeRule, len(a.components))
	rawProcesses := make(map[string][]*DeclarativeProcess, len(a.components))
	rawModuleHandlers := make(map[string]*ExceptionHandler, len(a.components))
	rawInitial := make(map[string][]Statement, len(a.components))
	rawFinal := make(map[string][]Statement, len(a.components))
	moduleConstraints := make(map[string]*constraint.ConstraintSet, len(a.components))
	for id, component := range a.components {
		componentIDs = append(componentIDs, id)
		sourceComponents[id] = component
	}
	sort.Strings(componentIDs)

	finiteIterators, canonicalFiniteIterators, err := canonicalizeFiniteIteratorModules(a.finiteIterators)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnsupportedDeterministicModel, err)
	}
	iteratorGenerators, canonicalIteratorGenerators, err := canonicalizeFiniteIteratorGenerators(a.iteratorGenerators)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnsupportedDeterministicModel, err)
	}
	if a.returnInterface == nil {
		return nil, fmt.Errorf("%w: architecture %q has no return interface", ErrUnsupportedDeterministicModel, a.Name)
	}
	returnInterface, err := cloneInterfaceDecl(a.returnInterface)
	if err != nil {
		return nil, fmt.Errorf("%w: architecture return interface: %w", ErrUnsupportedDeterministicModel, err)
	}
	canonicalReturnInterface, err := canonicalizeInterface(returnInterface)
	if err != nil {
		return nil, fmt.Errorf("%w: architecture return interface: %v", ErrUnsupportedDeterministicModel, err)
	}
	architectureConstraints, err := cloneDeterministicConstraintSets(a.architectureConstraints)
	if err != nil {
		return nil, fmt.Errorf("%w: architecture constraints: %v", ErrUnsupportedDeterministicModel, err)
	}
	constraintSet, err := cloneDeterministicConstraintSet(a.constraintSet)
	if err != nil {
		return nil, fmt.Errorf("%w: constraints: %v", ErrUnsupportedDeterministicModel, err)
	}
	canonical := canonicalArchitecture{
		Format:             "gorapide.architecture.v126",
		Profile:            CompatibilityProfile,
		Name:               a.Name,
		GeneratorArguments: []canonicalArchitectureArgument{},
		ReturnInterface:    canonicalReturnInterface,
		FiniteIterators:    canonicalFiniteIterators,
		IteratorGenerators: canonicalIteratorGenerators,
	}
	architectureInstances, architectureInstanceIDs, canonicalInstances, err :=
		canonicalizeArchitectureInstances(a.architectureInstances, architectureConstraints)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnsupportedDeterministicModel, err)
	}
	canonical.ArchitectureInstances = canonicalInstances
	seenGeneratorArguments := make(map[string]bool, len(a.generatorArguments))
	for index, argument := range a.generatorArguments {
		if argument.Name == "" || argument.Type == "" {
			return nil, fmt.Errorf("%w: architecture generator argument %d has an empty name or type",
				ErrUnsupportedDeterministicModel, index)
		}
		key := strings.ToLower(argument.Name)
		if seenGeneratorArguments[key] {
			return nil, fmt.Errorf("%w: duplicate architecture generator argument %q",
				ErrUnsupportedDeterministicModel, argument.Name)
		}
		if !supportedPredefinedType(argument.Type) || !valueMatchesPredefinedType(argument.Value, argument.Type) {
			return nil, fmt.Errorf("%w: architecture generator argument %q does not match supported type %q",
				ErrUnsupportedDeterministicModel, argument.Name, argument.Type)
		}
		value, err := gorapide.EncodeCanonicalValue(argument.Value)
		if err != nil {
			return nil, fmt.Errorf("%w: architecture generator argument %q: %v",
				ErrUnsupportedDeterministicModel, argument.Name, err)
		}
		seenGeneratorArguments[key] = true
		canonical.GeneratorArguments = append(canonical.GeneratorArguments, canonicalArchitectureArgument{
			Name: argument.Name, Type: argument.Type, Value: value,
		})
	}
	if constraintSet != nil {
		digest, err := constraintSet.DeterministicDigest()
		if err != nil {
			return nil, fmt.Errorf("%w: constraints: %v", ErrUnsupportedDeterministicModel, err)
		}
		canonical.ConstraintSet = &canonicalConstraintSetDecl{Name: constraintSet.Name, Digest: digest}
	}
	for _, id := range componentIDs {
		sourceComponent := sourceComponents[id]
		if sourceComponent == nil || sourceComponent.Interface == nil {
			return nil, fmt.Errorf("%w: component %q has no interface", ErrUnsupportedDeterministicModel, id)
		}
		sourceComponent.mu.Lock()
		sourceInterface := sourceComponent.Interface
		hasBehavior := sourceComponent.behavior != nil || len(sourceComponent.rules) != 0
		declarations := append([]*DeclarativeRule(nil), sourceComponent.transitions...)
		processDeclarations := append([]*DeclarativeProcess(nil), sourceComponent.processes...)
		processMode := sourceComponent.processMode
		componentClocks := append([]BasicClockDeclaration(nil), sourceComponent.basicClocks...)
		componentStateDeclarations := append([]StateDeclaration(nil), sourceComponent.stateDeclarations...)
		componentInitializationParameters := copyModuleInitializationParameters(sourceComponent.initializationParameters)
		functionDeclarations := make([]*FunctionImplementation, len(sourceComponent.functions))
		for index, implementation := range sourceComponent.functions {
			functionDeclarations[index] = copyFunctionImplementation(implementation)
		}
		initialDeclarations := copyStatements(sourceComponent.initialStatements)
		finalDeclarations := copyStatements(sourceComponent.finalStatements)
		exceptionDeclarations := make([]ExceptionDeclaration, len(sourceComponent.exceptions))
		for index, declaration := range sourceComponent.exceptions {
			params, cloneErr := cloneParamDecls(declaration.Params)
			if cloneErr != nil {
				sourceComponent.mu.Unlock()
				return nil, fmt.Errorf("%w: component %q exception %q: %v", ErrUnsupportedDeterministicModel, id, declaration.Name, cloneErr)
			}
			exceptionDeclarations[index] = ExceptionDeclaration{
				Declaration: declaration.Declaration,
				Name:        declaration.Name, Params: params,
			}
		}
		var moduleHandler *ExceptionHandler
		if sourceComponent.moduleHandler != nil {
			copy := copyExceptionHandler(*sourceComponent.moduleHandler)
			moduleHandler = &copy
		}
		componentConstraintSet := sourceComponent.moduleConstraints
		var componentMembership *moduleMembershipDeclaration
		if sourceComponent.moduleMembership != nil {
			componentMembership = &moduleMembershipDeclaration{
				Generator: sourceComponent.moduleMembership.Generator,
				GeneratorArguments: append([]ModuleGeneratorArgument(nil),
					sourceComponent.moduleMembership.GeneratorArguments...),
				TypeDenotations: append([]gorapide.RapideTypeDenotation(nil),
					sourceComponent.moduleMembership.TypeDenotations...),
				ObjectDenotations: append([]gorapide.RapideObjectDenotation(nil),
					sourceComponent.moduleMembership.ObjectDenotations...),
				RecordObjects: append([]gorapide.RapideRecordObjectDeclaration(nil),
					sourceComponent.moduleMembership.RecordObjects...),
			}
		}
		sourceComponent.mu.Unlock()
		interfaceSnapshot, err := cloneInterfaceDecl(sourceInterface)
		if err != nil {
			return nil, fmt.Errorf("%w: component %q interface: %w", ErrUnsupportedDeterministicModel, id, err)
		}
		component := NewComponent(id, interfaceSnapshot, nil)
		component.basicClocks = append([]BasicClockDeclaration(nil), componentClocks...)
		component.exceptions = append([]ExceptionDeclaration(nil), exceptionDeclarations...)
		component.moduleMembership = componentMembership
		components[id] = component
		if hasBehavior {
			return nil, fmt.Errorf("%w: component %q uses Go behavior callbacks", ErrUnsupportedDeterministicModel, id)
		}
		if len(declarations) != 0 && len(processDeclarations) != 0 {
			return nil, fmt.Errorf("%w: component %q mixes interface rules and a process implementation", ErrUnsupportedDeterministicModel, id)
		}
		if len(processDeclarations) > 1 && processMode != SerialProcesses && processMode != ParallelProcesses {
			return nil, fmt.Errorf("%w: component %q has multiple processes but no serial/parallel module mode", ErrUnsupportedDeterministicModel, id)
		}
		if finalDeclarations != nil && componentMembership == nil {
			return nil, fmt.Errorf("%w: component %q: %w: final part requires generated-module membership",
				ErrUnsupportedDeterministicModel, id, ErrInvalidModuleFinal)
		}
		if moduleHandler != nil && componentMembership == nil {
			return nil, fmt.Errorf("%w: component %q: %w: module handler requires generated-module membership",
				ErrUnsupportedDeterministicModel, id, ErrInvalidExceptionHandler)
		}
		if componentInitializationParameters != nil && componentMembership == nil {
			return nil, fmt.Errorf("%w: component %q: %w: initialization parameters require generated-module membership",
				ErrUnsupportedDeterministicModel, id, ErrInvalidModuleInitializationParameter)
		}
		if componentInitializationParameters != nil && initialDeclarations == nil {
			return nil, fmt.Errorf("%w: component %q: %w: initialization parameters require an initial statement list",
				ErrUnsupportedDeterministicModel, id, ErrInvalidModuleInitializationParameter)
		}
		rawRules[id] = declarations
		rawProcesses[id] = processDeclarations
		rawModuleHandlers[id] = moduleHandler
		rawInitial[id] = initialDeclarations
		rawFinal[id] = finalDeclarations
		iface, err := canonicalizeInterface(component.Interface)
		if err != nil {
			return nil, fmt.Errorf("component %q: %w", id, err)
		}
		owner := a.componentArchitectures[id]
		if owner == "" {
			owner = ArchitectureInterfaceID
		}
		if owner != ArchitectureInterfaceID {
			if _, exists := architectureInstances[owner]; !exists {
				return nil, fmt.Errorf("%w: component %q references missing architecture instance %q",
					ErrUnsupportedDeterministicModel, id, owner)
			}
		}
		componentArchitectures[id] = owner
		canonicalComponent := canonicalComponentDecl{
			ID: id, Interface: iface, Clocks: []canonicalBasicClockDeclaration{},
			Exceptions: []canonicalExceptionDeclaration{},
		}
		canonicalComponent.Exceptions, err = canonicalizeExceptionDeclarations(component, exceptionDeclarations)
		if err != nil {
			return nil, fmt.Errorf("%w: component %q: %w: %v",
				ErrUnsupportedDeterministicModel, id, ErrInvalidExceptionDeclaration, err)
		}
		if owner != ArchitectureInterfaceID {
			canonicalComponent.ArchitectureInstance = owner
		}
		canonicalComponent.Membership, err = canonicalizeModuleMembership(componentMembership, component.Interface)
		if err != nil {
			return nil, fmt.Errorf("%w: component %q module membership: %v", ErrUnsupportedDeterministicModel, id, err)
		}
		if canonicalComponent.Membership != nil {
			staticModuleGenerators[id] = canonicalComponent.Membership.Generator
			staticRecordObjects[id] = append([]gorapide.RapideRecordObjectDeclaration(nil), componentMembership.RecordObjects...)
		}
		if componentConstraintSet != nil {
			componentConstraintSet, err = cloneDeterministicConstraintSet(componentConstraintSet)
			if err != nil {
				return nil, fmt.Errorf("%w: component %q module constraints: %v", ErrUnsupportedDeterministicModel, id, err)
			}
			digest, err := componentConstraintSet.DeterministicDigest()
			if err != nil {
				return nil, fmt.Errorf("%w: component %q module constraints: %v", ErrUnsupportedDeterministicModel, id, err)
			}
			moduleConstraints[id] = componentConstraintSet
			canonicalComponent.Constraints = &canonicalConstraintSetDecl{
				Name: componentConstraintSet.Name, Digest: digest,
			}
		}
		if len(componentClocks) != 0 && !validDeterministicClockComponentID(id, owner) {
			return nil, fmt.Errorf("%w: clock owner component %q is not a Rapide identifier", ErrUnsupportedDeterministicModel, id)
		}
		seenClocks := make(map[string]bool, len(componentClocks))
		for _, clock := range componentClocks {
			if !validRapideClockIdentifier(clock.Name) || seenClocks[clock.Name] {
				return nil, fmt.Errorf("%w: component %q has invalid or duplicate basic clock %q", ErrInvalidBasicClock, id, clock.Name)
			}
			seenClocks[clock.Name] = true
			basicClocks[id] = append(basicClocks[id], clock)
			canonicalComponent.Clocks = append(canonicalComponent.Clocks, canonicalBasicClockDeclaration{
				Name: clock.Name, Kind: "basic",
			})
		}
		sort.Slice(basicClocks[id], func(i, j int) bool { return basicClocks[id][i].Name < basicClocks[id][j].Name })
		sort.Slice(canonicalComponent.Clocks, func(i, j int) bool {
			return canonicalComponent.Clocks[i].Name < canonicalComponent.Clocks[j].Name
		})
		normalizedState, canonicalState, stateTypes, err := canonicalizeStateDeclarations(componentStateDeclarations)
		if err != nil {
			return nil, fmt.Errorf("%w: component %q: %w", ErrUnsupportedDeterministicModel, id, err)
		}
		stateDeclarations[id] = normalizedState
		stateTypesByComponent[id] = stateTypes
		canonicalComponent.State = canonicalState
		normalizedInitialization, canonicalInitialization, _, err := canonicalizeModuleInitializationParameters(
			id, componentInitializationParameters, stateTypes,
		)
		if err != nil {
			return nil, fmt.Errorf("%w: component %q: %w", ErrUnsupportedDeterministicModel, id, err)
		}
		initializationParameters[id] = normalizedInitialization
		canonicalComponent.InitializationParameters = canonicalInitialization
		functionCatalog, signatures, err := prepareFunctionCatalog(component, functionDeclarations)
		if err != nil {
			return nil, fmt.Errorf("%w: component %q: %w", ErrUnsupportedDeterministicModel, id, err)
		}
		functions[id] = functionCatalog
		functionSignatures[id] = signatures
		callables[id] = make(map[string]*FunctionImplementation, len(functionCatalog))
		for key, implementation := range functionCatalog {
			copy := copyFunctionImplementation(implementation)
			copy.targetComponent = id
			copy.targetName = copy.Name
			callables[id][key] = copy
		}
		if len(processDeclarations) == 1 {
			canonicalComponent.ProcessMode = "single"
			processModes[id] = UnspecifiedProcessMode
		} else if len(processDeclarations) > 1 {
			canonicalComponent.ProcessMode = processMode.String()
			processModes[id] = processMode
		}
		canonical.Components = append(canonical.Components, canonicalComponent)
	}

	functionConnections := make([]*FunctionConnection, len(a.functionConnections))
	for index, connection := range a.functionConnections {
		functionConnections[index] = copyFunctionConnection(connection)
	}
	// First construct the complete signature-only callable graph. Function
	// bodies can then resolve connected calls without depending on declaration
	// or component traversal order.
	if _, err := buildFunctionRoutes(
		functionConnections, returnInterface, components, architectureInstances, componentArchitectures, functions, callables,
	); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnsupportedDeterministicModel, err)
	}
	for componentIndex, id := range componentIDs {
		normalized, canonicalFunctions, err := canonicalizeFunctionImplementations(
			components[id], functions[id], functionSignatures[id],
			stateTypesByComponent[id], callables[id],
		)
		if err != nil {
			return nil, fmt.Errorf("%w: component %q: %w", ErrUnsupportedDeterministicModel, id, err)
		}
		functions[id] = normalized
		canonical.Components[componentIndex].Functions = canonicalFunctions
	}

	// Rebuild executable routes from canonicalized provider bodies. Route keys
	// depend only on canonical endpoint signatures and are identical in both
	// passes.
	callables = make(map[string]map[string]*FunctionImplementation, len(componentIDs))
	for _, id := range componentIDs {
		callables[id] = make(map[string]*FunctionImplementation, len(functions[id]))
		for key, implementation := range functions[id] {
			copy := copyFunctionImplementation(implementation)
			copy.targetComponent = id
			copy.targetName = copy.Name
			callables[id][key] = copy
		}
	}
	canonicalFunctionConnections, err := buildFunctionRoutes(
		functionConnections, returnInterface, components, architectureInstances, componentArchitectures, functions, callables,
	)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnsupportedDeterministicModel, err)
	}
	canonical.FunctionConnections = canonicalFunctionConnections

	// Architecture initial lists are post-connection executable parts. Delay
	// their canonicalization until the complete executable alias graph exists so
	// an inward returned-interface provides call resolves exactly like the same
	// alias used by an enclosing caller.
	architectureInitials, architectureInitialExceptions, canonicalRootInitial, canonicalChildInitials, canonicalInitialExceptions, err := canonicalizeArchitectureInitials(
		returnInterface, architectureInstances, architectureInstanceIDs,
		a.architectureInitials, a.architectureInitialExceptions, callables,
		componentArchitectures,
	)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnsupportedDeterministicModel, err)
	}
	canonical.Initial = canonicalRootInitial
	canonical.InitialExceptionCatalogs = canonicalInitialExceptions
	for index := range canonical.ArchitectureInstances {
		owner := canonical.ArchitectureInstances[index].ID
		canonical.ArchitectureInstances[index].Initial = canonicalChildInitials[owner]
	}

	// Initial/final parts, rules, and processes are canonicalized only after all local
	// and connected call targets carry their closed executable bodies.
	initialStatements := make(map[string][]Statement, len(componentIDs))
	finalStatements := make(map[string][]Statement, len(componentIDs))
	moduleHandlers := make(map[string]ExceptionHandler, len(componentIDs))
	for componentIndex, id := range componentIDs {
		component := components[id]
		initialDeclarations := rawInitial[id]
		finalDeclarations := rawFinal[id]
		declarations := rawRules[id]
		processDeclarations := rawProcesses[id]
		stateTypes := stateTypesByComponent[id]
		if rawModuleHandlers[id] != nil {
			normalizedHandler, canonicalHandler, err := canonicalizeModuleExceptionHandler(
				component, "module handler "+id, *rawModuleHandlers[id], stateTypes, callables[id], callables,
			)
			if err != nil {
				return nil, fmt.Errorf("%w: component %q: %w: %v",
					ErrUnsupportedDeterministicModel, id, ErrInvalidExceptionHandler, err)
			}
			moduleHandlers[id] = normalizedHandler
			canonical.Components[componentIndex].ModuleHandler = &canonicalHandler
		}
		if initialDeclarations != nil {
			initialParameterTypes := make(map[string]string, len(initializationParameters[id]))
			for _, parameter := range initializationParameters[id] {
				initialParameterTypes[parameter.Name] = parameter.Type
			}
			normalizedInitial, canonicalInitial, err := canonicalizeInitializerRuleStatements(
				component, "module initial "+id, initialDeclarations, stateTypes, initialParameterTypes, callables[id], nil,
			)
			if err != nil {
				return nil, fmt.Errorf("%w: component %q: %w: %v",
					ErrUnsupportedDeterministicModel, id, ErrInvalidModuleInitial, err)
			}
			if err := validateModuleInitialStatementSubset(component, normalizedInitial, callables); err != nil {
				return nil, fmt.Errorf("%w: component %q: %w", ErrUnsupportedDeterministicModel, id, err)
			}
			initialStatements[id] = normalizedInitial
			canonical.Components[componentIndex].Initial = canonicalInitial
		}
		if finalDeclarations != nil {
			normalizedFinal, canonicalFinal, err := canonicalizeRuleStatements(
				component, "module final "+id, finalDeclarations, nil, nil, callables[id], nil,
			)
			if err != nil {
				return nil, fmt.Errorf("%w: component %q: %w: %v",
					ErrUnsupportedDeterministicModel, id, ErrInvalidModuleFinal, err)
			}
			if err := validateModuleFinalStatementSubset(component, normalizedFinal, callables); err != nil {
				return nil, fmt.Errorf("%w: component %q: %w", ErrUnsupportedDeterministicModel, id, err)
			}
			finalStatements[id] = normalizedFinal
			canonical.Components[componentIndex].Final = canonicalFinal
		}
		sort.Slice(declarations, func(i, j int) bool {
			if declarations[i] == nil {
				return declarations[j] != nil
			}
			if declarations[j] == nil {
				return false
			}
			return declarations[i].ID < declarations[j].ID
		})
		seenRules := make(map[string]bool, len(declarations))
		for _, declaration := range declarations {
			normalizedRule, canonicalRule, err := canonicalizeDeclarativeRule(component, declaration, stateTypes, callables[id])
			if err != nil {
				return nil, fmt.Errorf("%w: component %q: %w", ErrUnsupportedDeterministicModel, id, err)
			}
			if seenRules[normalizedRule.ID] {
				return nil, fmt.Errorf("%w: component %q has duplicate rule ID %q", ErrUnsupportedDeterministicModel, id, normalizedRule.ID)
			}
			seenRules[normalizedRule.ID] = true
			rules[id] = append(rules[id], normalizedRule)
			canonical.Components[componentIndex].Rules = append(canonical.Components[componentIndex].Rules, canonicalRule)
		}
		sort.Slice(processDeclarations, func(i, j int) bool {
			if processDeclarations[i] == nil {
				return processDeclarations[j] != nil
			}
			if processDeclarations[j] == nil {
				return false
			}
			return processDeclarations[i].ID < processDeclarations[j].ID
		})
		seenProcesses := make(map[string]bool, len(processDeclarations))
		for _, declaration := range processDeclarations {
			normalizedProcess, canonicalProcess, err := canonicalizeDeclarativeProcess(component, declaration, stateTypes, callables[id])
			if err != nil {
				return nil, fmt.Errorf("%w: component %q: %w", ErrUnsupportedDeterministicModel, id, err)
			}
			if seenProcesses[normalizedProcess.ID] {
				return nil, fmt.Errorf("%w: component %q has duplicate process ID %q", ErrUnsupportedDeterministicModel, id, normalizedProcess.ID)
			}
			seenProcesses[normalizedProcess.ID] = true
			processes[id] = append(processes[id], normalizedProcess)
			canonical.Components[componentIndex].Processes = append(canonical.Components[componentIndex].Processes, canonicalProcess)
		}
	}

	connections := append([]*Connection(nil), a.connections...)
	sort.Slice(connections, func(i, j int) bool { return connections[i].ID < connections[j].ID })
	normalizedConnections := make([]*Connection, 0, len(connections))
	seenConnections := make(map[string]bool, len(connections))
	for _, connection := range connections {
		if connection == nil || connection.ID == "" {
			return nil, fmt.Errorf("%w: connection has no stable identity", ErrUnsupportedDeterministicModel)
		}
		if seenConnections[connection.ID] {
			return nil, fmt.Errorf("%w: duplicate connection ID %q", ErrUnsupportedDeterministicModel, connection.ID)
		}
		seenConnections[connection.ID] = true
		if connection.transform != nil {
			return nil, fmt.Errorf("%w: connection %q uses an opaque parameter transform", ErrUnsupportedDeterministicModel, connection.ID)
		}
		if connection.Kind < BasicConnection || connection.Kind > AgentConnection {
			return nil, fmt.Errorf("%w: connection %q has kind %d", ErrUnsupportedDeterministicModel, connection.ID, connection.Kind)
		}
		if connection.Scope < ArchitectureConnectionScope || connection.Scope > ModuleConnectionScope {
			return nil, fmt.Errorf("%w: connection %q has scope %d", ErrUnsupportedDeterministicModel, connection.ID, connection.Scope)
		}
		owner := connection.ArchitectureInstance
		if owner == "" {
			owner = ArchitectureInterfaceID
		}
		if owner != ArchitectureInterfaceID {
			if _, exists := architectureInstances[owner]; !exists {
				return nil, fmt.Errorf("%w: connection %q references missing architecture instance %q",
					ErrUnsupportedDeterministicModel, connection.ID, owner)
			}
		}
		if connection.Scope == ModuleConnectionScope &&
			(connection.From == "*" || connection.To == "*" || connection.From != connection.To) {
			return nil, fmt.Errorf("%w: module connection %q must use one identical explicit source and target component", ErrUnsupportedDeterministicModel, connection.ID)
		}
		if connection.Scope == ModuleConnectionScope && connection.From == architectureBoundaryID(owner) {
			return nil, fmt.Errorf("%w: module connection %q cannot use its enclosing architecture interface", ErrUnsupportedDeterministicModel, connection.ID)
		}
		if connection.From != "*" {
			if !connectionEndpointVisible(owner, connection.From, architectureInstances, componentArchitectures) {
				return nil, fmt.Errorf("%w: connection %q source %q is not visible in architecture %q",
					ErrUnsupportedDeterministicModel, connection.ID, connection.From, owner)
			}
		}
		if connection.To != "*" {
			if !connectionEndpointVisible(owner, connection.To, architectureInstances, componentArchitectures) {
				return nil, fmt.Errorf("%w: connection %q target %q is not visible in architecture %q",
					ErrUnsupportedDeterministicModel, connection.ID, connection.To, owner)
			}
		}
		patternKey, err := pattern.DeterministicKey(connection.Trigger)
		if err != nil {
			return nil, fmt.Errorf("%w: connection %q trigger: %v", ErrUnsupportedDeterministicModel, connection.ID, err)
		}
		trigger, err := pattern.CloneDeterministic(connection.Trigger)
		if err != nil {
			return nil, fmt.Errorf("%w: connection %q trigger: %v", ErrUnsupportedDeterministicModel, connection.ID, err)
		}
		requiresStateWitnesses, err := pattern.RequiresStateWitnesses(connection.Trigger)
		if err != nil {
			return nil, fmt.Errorf("%w: connection %q trigger: %v", ErrUnsupportedDeterministicModel, connection.ID, err)
		}
		if requiresStateWitnesses {
			return nil, fmt.Errorf("%w: connection %q trigger requires consistent-cut state witnesses", ErrUnsupportedDeterministicModel, connection.ID)
		}
		_, singleEventError := pattern.DeterministicSingleEventKey(connection.Trigger)
		compound := singleEventError != nil
		empty, err := pattern.CanMatchEmpty(connection.Trigger)
		if err != nil {
			return nil, fmt.Errorf("%w: connection %q trigger: %v", ErrUnsupportedDeterministicModel, connection.ID, err)
		}
		if empty {
			return nil, fmt.Errorf("%w: connection %q trigger can match an empty computation", ErrUnsupportedDeterministicModel, connection.ID)
		}
		if compound && connection.Kind == BasicConnection {
			return nil, fmt.Errorf("%w: basic connection %q requires a single-event trigger", ErrUnsupportedDeterministicModel, connection.ID)
		}
		if compound && connection.To == "*" {
			return nil, fmt.Errorf("%w: compound connection %q requires one explicit target component", ErrUnsupportedDeterministicModel, connection.ID)
		}
		if compound && connection.ActionName == "" {
			return nil, fmt.Errorf("%w: compound connection %q requires one explicit target action", ErrUnsupportedDeterministicModel, connection.ID)
		}
		placeholderTypes, err := pattern.BoundPlaceholderTypes(connection.Trigger)
		if err != nil {
			return nil, fmt.Errorf("%w: connection %q trigger: %v", ErrUnsupportedDeterministicModel, connection.ID, err)
		}
		normalizedParameters, canonicalParameters, err := canonicalizeConnectionParameters(connection.ID, connection.Parameters, placeholderTypes)
		if err != nil {
			return nil, fmt.Errorf("%w: connection %q parameters: %v", ErrUnsupportedDeterministicModel, connection.ID, err)
		}
		if len(normalizedParameters) != 0 && connection.ActionName == "" {
			return nil, fmt.Errorf("%w: connection %q has explicit parameters but no target action", ErrUnsupportedDeterministicModel, connection.ID)
		}
		if len(normalizedParameters) != 0 {
			targetIDs := connectionTargetIDs(
				owner, componentIDs, architectureInstances, architectureInstanceIDs, componentArchitectures,
			)
			if connection.To != "*" {
				targetIDs = []string{connection.To}
			}
			for _, targetID := range targetIDs {
				if targetID == connection.From && connection.To == "*" {
					continue
				}
				targetKinds := []ActionKind{InAction}
				if connection.Scope == ModuleConnectionScope {
					targetKinds = []ActionKind{OutAction, PrivateAction}
				} else if targetID == architectureBoundaryID(owner) {
					targetKinds = []ActionKind{OutAction}
				}
				if !connectionOutputShapeMatchesInterface(
					endpointInterface(targetID, returnInterface, architectureInstances, components),
					connection.ActionName, targetKinds, normalizedParameters, placeholderTypes,
				) {
					return nil, fmt.Errorf("%w: connection %q output does not match target action %s.%s", ErrUnsupportedDeterministicModel, connection.ID, targetID, connection.ActionName)
				}
			}
		}
		normalizedConnections = append(normalizedConnections, &Connection{
			ID: connection.ID, Kind: connection.Kind, Scope: connection.Scope, ArchitectureInstance: owner,
			From: connection.From, To: connection.To,
			Trigger: trigger, ActionName: connection.ActionName,
			Parameters: normalizedParameters, transform: connection.transform,
			forward: connection.forward, previousOutput: make(map[string]gorapide.EventID),
		})
		canonical.Connections = append(canonical.Connections, canonicalConnectionDecl{
			ID: connection.ID, Kind: int(connection.Kind), Scope: int(connection.Scope), ArchitectureInstance: owner,
			From: connection.From, To: connection.To,
			Trigger: patternKey, ActionName: connection.ActionName, Parameters: canonicalParameters,
		})
	}

	architectureInitialOwners := append([]string(nil), architectureInstanceIDs...)
	architectureInitialOwners = append(architectureInitialOwners, ArchitectureInterfaceID)
	for _, owner := range architectureInitialOwners {
		statements := architectureInitials[owner]
		if err := validateFiniteIteratorStatementReferences(statements, finiteIterators, iteratorGenerators); err != nil {
			return nil, fmt.Errorf("%w: architecture %q initial part: %v", ErrUnsupportedDeterministicModel, owner, err)
		}
	}
	if err := validateFiniteIteratorModelReferences(
		componentIDs, initialStatements, functions, rules, processes, finiteIterators, iteratorGenerators,
	); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnsupportedDeterministicModel, err)
	}
	data, err := json.Marshal(canonical)
	if err != nil {
		return nil, fmt.Errorf("arch.Architecture.DeterministicModelDigest: %w", err)
	}
	return &deterministicModel{
		name: a.Name, digest: digestBytes(data), canonical: append([]byte(nil), data...),
		returnInterface: returnInterface, components: components,
		architectureInstances: architectureInstances, architectureInstanceIDs: architectureInstanceIDs,
		architectureConstraints:       architectureConstraints,
		architectureInitials:          architectureInitials,
		architectureInitialExceptions: architectureInitialExceptions,
		componentArchitectures:        componentArchitectures,
		staticModuleGenerators:        staticModuleGenerators,
		staticRecordObjects:           staticRecordObjects,
		componentIDs:                  componentIDs, connections: normalizedConnections, rules: rules, processes: processes,
		moduleHandlers: moduleHandlers,
		processModes:   processModes, stateDeclarations: stateDeclarations,
		initializationParameters: initializationParameters, functions: functions, callables: callables,
		initialStatements: initialStatements, finalStatements: finalStatements, constraintSet: constraintSet,
		moduleConstraints: moduleConstraints, basicClocks: basicClocks,
		finiteIterators: finiteIterators, iteratorGenerators: iteratorGenerators,
	}, nil
}

func canonicalizeInterface(iface *InterfaceDecl) (canonicalInterfaceDecl, error) {
	result := canonicalInterfaceDecl{Name: iface.Name}
	if structuralType, ok := iface.StructuralRapideType(); ok {
		encoded, err := structuralType.MarshalCanonical()
		if err != nil {
			return canonicalInterfaceDecl{}, fmt.Errorf("structural Rapide type: %w", err)
		}
		if _, err := gorapide.ParseRapideType(encoded); err != nil {
			return canonicalInterfaceDecl{}, fmt.Errorf("structural Rapide type canonical validation: %w", err)
		}
		result.StructuralType = append(json.RawMessage(nil), encoded...)
	}
	seen := make(map[string]bool)
	for _, action := range iface.Actions {
		canonical, key, err := canonicalizeAction(action)
		if err != nil {
			return canonicalInterfaceDecl{}, err
		}
		if seen[key] {
			return canonicalInterfaceDecl{}, fmt.Errorf("duplicate action declaration %q", key)
		}
		seen[key] = true
		result.Actions = append(result.Actions, canonical)
	}
	sort.Slice(result.Actions, func(i, j int) bool { return actionDeclKey(result.Actions[i]) < actionDeclKey(result.Actions[j]) })

	seenFunctions := make(map[string]bool)
	for _, function := range iface.Functions {
		canonical, key, err := canonicalizeFunction(function)
		if err != nil {
			return canonicalInterfaceDecl{}, err
		}
		if seenFunctions[key] {
			return canonicalInterfaceDecl{}, fmt.Errorf("duplicate function declaration %q", key)
		}
		seenFunctions[key] = true
		result.Functions = append(result.Functions, canonical)
	}
	sort.Slice(result.Functions, func(i, j int) bool {
		return functionDeclKey(result.Functions[i]) < functionDeclKey(result.Functions[j])
	})

	seenServices := make(map[string]bool)
	for _, service := range iface.Services {
		if service.Name == "" || seenServices[service.Name] {
			return canonicalInterfaceDecl{}, fmt.Errorf("empty or duplicate service declaration %q", service.Name)
		}
		seenServices[service.Name] = true
		canonical := canonicalServiceDecl{Name: service.Name}
		serviceSeen := make(map[string]bool)
		for _, action := range service.Actions {
			canonicalAction, key, err := canonicalizeAction(action)
			if err != nil {
				return canonicalInterfaceDecl{}, fmt.Errorf("service %q: %w", service.Name, err)
			}
			if serviceSeen[key] {
				return canonicalInterfaceDecl{}, fmt.Errorf("service %q: duplicate action %q", service.Name, key)
			}
			serviceSeen[key] = true
			canonical.Actions = append(canonical.Actions, canonicalAction)
		}
		sort.Slice(canonical.Actions, func(i, j int) bool { return actionDeclKey(canonical.Actions[i]) < actionDeclKey(canonical.Actions[j]) })
		serviceFunctionsSeen := make(map[string]bool)
		for _, function := range service.Functions {
			canonicalFunction, key, err := canonicalizeFunction(function)
			if err != nil {
				return canonicalInterfaceDecl{}, fmt.Errorf("service %q: %w", service.Name, err)
			}
			if serviceFunctionsSeen[key] {
				return canonicalInterfaceDecl{}, fmt.Errorf("service %q: duplicate function %q", service.Name, key)
			}
			serviceFunctionsSeen[key] = true
			canonical.Functions = append(canonical.Functions, canonicalFunction)
		}
		sort.Slice(canonical.Functions, func(i, j int) bool {
			return functionDeclKey(canonical.Functions[i]) < functionDeclKey(canonical.Functions[j])
		})
		result.Services = append(result.Services, canonical)
	}
	sort.Slice(result.Services, func(i, j int) bool { return result.Services[i].Name < result.Services[j].Name })
	return result, nil
}

func canonicalizeAction(action ActionDecl) (canonicalActionDecl, string, error) {
	if action.Name == "" {
		return canonicalActionDecl{}, "", fmt.Errorf("action name is empty")
	}
	if action.Kind != InAction && action.Kind != OutAction && action.Kind != PrivateAction {
		return canonicalActionDecl{}, "", fmt.Errorf("action %q has invalid kind %d", action.Name, action.Kind)
	}
	result := canonicalActionDecl{Name: action.Name, Kind: int(action.Kind)}
	seenParams := make(map[string]bool)
	for _, param := range action.Params {
		if param.Name == "" || param.Type == "" {
			return canonicalActionDecl{}, "", fmt.Errorf("action %q has incomplete parameter declaration", action.Name)
		}
		if param.Default != nil {
			return canonicalActionDecl{}, "", fmt.Errorf("action %q parameter %q cannot have a function default denotation", action.Name, param.Name)
		}
		if seenParams[param.Name] {
			return canonicalActionDecl{}, "", fmt.Errorf("action %q has duplicate parameter %q", action.Name, param.Name)
		}
		canonicalParam := canonicalParamDecl{Name: param.Name, Type: param.Type}
		if structuralType, structural := param.StructuralRapideType(); structural {
			if action.Kind != OutAction && action.Kind != InAction {
				return canonicalActionDecl{}, "", fmt.Errorf("%w: action %q parameter %q has structural type %q; the current module-value slice supports public in/out actions only",
					ErrUnsupportedRapideType, action.Name, param.Name, param.Type)
			}
			encoded, err := structuralType.MarshalCanonical()
			if err != nil {
				return canonicalActionDecl{}, "", fmt.Errorf("action %q parameter %q structural type: %w", action.Name, param.Name, err)
			}
			if _, err := gorapide.ParseRapideType(encoded); err != nil {
				return canonicalActionDecl{}, "", fmt.Errorf("action %q parameter %q structural type validation: %w", action.Name, param.Name, err)
			}
			canonicalParam.StructuralType = append(json.RawMessage(nil), encoded...)
		} else if !supportedPredefinedType(param.Type) {
			return canonicalActionDecl{}, "", fmt.Errorf("%w: action %q parameter %q has type %q",
				ErrUnsupportedRapideType, action.Name, param.Name, param.Type)
		}
		seenParams[param.Name] = true
		result.Params = append(result.Params, canonicalParam)
	}
	return result, actionDeclKey(result), nil
}

func actionDeclKey(action canonicalActionDecl) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "%d:%s", action.Kind, action.Name)
	for _, param := range action.Params {
		fmt.Fprintf(&builder, "|%s:%s", param.Name, param.Type)
		if len(param.StructuralType) != 0 {
			fmt.Fprintf(&builder, "#%s", param.StructuralType)
		}
	}
	return builder.String()
}

func canonicalizeFunction(function FunctionDecl) (canonicalFunctionDecl, string, error) {
	if function.Name == "" {
		return canonicalFunctionDecl{}, "", fmt.Errorf("function name is empty")
	}
	if function.Kind != ProvidesFunction && function.Kind != RequiresFunction {
		return canonicalFunctionDecl{}, "", fmt.Errorf("function %q has invalid kind %d", function.Name, function.Kind)
	}
	if function.ReturnType != "" && !supportedPredefinedType(function.ReturnType) {
		return canonicalFunctionDecl{}, "", fmt.Errorf("%w: function %q return has type %q",
			ErrUnsupportedRapideType, function.Name, function.ReturnType)
	}
	result := canonicalFunctionDecl{
		Name: function.Name, Kind: int(function.Kind), ReturnType: function.ReturnType,
	}
	seenParams := make(map[string]bool)
	for _, param := range function.Params {
		if param.Name == "" || param.Type == "" {
			return canonicalFunctionDecl{}, "", fmt.Errorf("function %q has incomplete parameter declaration", function.Name)
		}
		if function.ReturnType != "" && strings.EqualFold(param.Name, "Return") {
			return canonicalFunctionDecl{}, "", fmt.Errorf("function %q parameter %q conflicts with the implicit return-event parameter", function.Name, param.Name)
		}
		if seenParams[param.Name] {
			return canonicalFunctionDecl{}, "", fmt.Errorf("function %q has duplicate parameter %q", function.Name, param.Name)
		}
		if _, structural := param.StructuralRapideType(); structural {
			return canonicalFunctionDecl{}, "", fmt.Errorf("%w: function %q structural parameter %q is outside the executable function subset",
				ErrUnsupportedRapideType, function.Name, param.Name)
		}
		if !supportedPredefinedType(param.Type) {
			return canonicalFunctionDecl{}, "", fmt.Errorf("%w: function %q parameter %q has type %q",
				ErrUnsupportedRapideType, function.Name, param.Name, param.Type)
		}
		seenParams[param.Name] = true
		canonicalParam := canonicalParamDecl{Name: param.Name, Type: param.Type}
		if param.Default != nil {
			values, err := gorapide.CanonicalizeParams(map[string]any{"default": param.Default})
			if err != nil {
				return canonicalFunctionDecl{}, "", fmt.Errorf("function %q parameter %q has an invalid default denotation: %w", function.Name, param.Name, err)
			}
			value := values["default"]
			if !valueMatchesPredefinedType(value, param.Type) {
				return canonicalFunctionDecl{}, "", fmt.Errorf("function %q parameter %q default does not match %s", function.Name, param.Name, param.Type)
			}
			encoded, err := gorapide.CanonicalizeParameters(values)
			if err != nil {
				return canonicalFunctionDecl{}, "", err
			}
			canonicalParam.Default = &encoded[0].Value
		}
		result.Params = append(result.Params, canonicalParam)
	}
	return result, functionDeclKey(result), nil
}

// functionDeclKey is the callable overload key. A default denotation changes
// canonical model content and callability, but it does not create a distinct
// overload of an otherwise identical formal signature.
func functionDeclKey(function canonicalFunctionDecl) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "%d:%s", function.Kind, function.Name)
	for _, param := range function.Params {
		fmt.Fprintf(&builder, "|%s:%s", param.Name, param.Type)
	}
	fmt.Fprintf(&builder, "->%s", function.ReturnType)
	return builder.String()
}

// ExecuteDeterministic executes the supported Rapide subset using a fresh
// poset and a stable causal-depth/event-ID worklist. No goroutine, component
// inbox, timer, observer, or legacy architecture poset participates.
func (a *Architecture) ExecuteDeterministic(journal ExecutionJournal) (*ExecutionResult, error) {
	prepared, err := a.PrepareDeterministic()
	if err != nil {
		return nil, err
	}
	return prepared.ExecuteDeterministic(journal)
}

func executeDeterministicModel(model *deterministicModel, journal ExecutionJournal) (*ExecutionResult, error) {
	normalized, err := normalizeJournal(journal)
	if err != nil {
		return nil, err
	}
	if normalized.ModelDigest != model.digest {
		return nil, fmt.Errorf("%w: journal=%s architecture=%s", ErrModelDigestMismatch, normalized.ModelDigest, model.digest)
	}
	journalDigest, err := normalized.SemanticDigest()
	if err != nil {
		return nil, err
	}
	choices := newChoiceResolver(normalized.Choices)
	clocks := newDeterministicClockKernel(model, normalized.ClockAdvances, choices)
	frontiers := newCausalFrontierRegistry()
	processes, err := initializeProcessRuntimes(model)
	if err != nil {
		return nil, err
	}
	state, err := initializeModuleState(model)
	if err != nil {
		return nil, err
	}
	iteratorRuntimes, err := initializeFiniteIteratorRuntimes(model.finiteIterators)
	if err != nil {
		return nil, err
	}
	stateSnapshots := make(stateSnapshotRegistry)
	statementSteps := &statementBudget{limit: normalized.Limits.MaxStatements}

	poset := gorapide.NewPoset()
	queue := &executionQueue{}
	heap.Init(queue)
	depths := make(map[gorapide.EventID]uint64)
	architectureModules, architectureStarts, err := createArchitectureStartEvents(
		normalized.Profile, model, poset, depths,
	)
	if err != nil {
		return nil, err
	}
	staticModules, moduleStarts, err := createStaticModuleStartEvents(
		normalized.Profile, model, architectureModules, architectureStarts, poset, depths,
	)
	if err != nil {
		return nil, err
	}
	recordObjects, componentStarts, recordStarts, err := createStaticRecordObjects(
		normalized.Profile, model, staticModules, moduleStarts, poset, depths,
	)
	if err != nil {
		return nil, err
	}
	lifecycle, err := initializeStaticModuleLifecycles(
		model, architectureModules, architectureStarts, staticModules, moduleStarts, recordObjects, recordStarts,
	)
	if err != nil {
		return nil, err
	}
	moduleParents := make(map[string]string, len(model.componentIDs)+1)
	moduleParents[ArchitectureInterfaceID] = architectureModules[ArchitectureInterfaceID].Identity()
	for _, instanceID := range model.architectureInstanceIDs {
		moduleParents[instanceID] = architectureModules[instanceID].Identity()
	}
	for _, componentID := range model.componentIDs {
		moduleParents[componentID] = componentID
		if module := staticModules[componentID]; module.Identity() != "" {
			moduleParents[componentID] = module.Identity()
		}
	}
	executionModules := make(map[string]gorapide.RapideModuleValue, len(architectureModules)+len(staticModules))
	for instanceID, module := range architectureModules {
		executionModules[instanceID] = module
	}
	for componentID, module := range staticModules {
		executionModules[componentID] = module
	}
	contexts, err := newCommunicationContextRuntime(lifecycle, executionModules)
	if err != nil {
		return nil, err
	}
	propagations := newExceptionPropagationRuntime()
	executionComponents := make(map[string]*Component, len(model.components))
	for componentID, component := range model.components {
		executionComponents[componentID] = component
	}
	moduleTemplates := make(map[string]string, len(staticModules))
	for componentID, module := range staticModules {
		if module.Identity() != "" {
			moduleTemplates[module.Identity()] = componentID
		}
	}
	functionRuntime := &functionExecutionRuntime{
		model: model, poset: poset, components: executionComponents,
		connections: copyExecutionConnections(model.connections),
		callables:   copyExecutionCallables(model.callables), state: state, clocks: clocks,
		modules: executionModules, recordObjects: recordObjects,
		iterators: iteratorRuntimes, iteratorGenerators: model.iteratorGenerators,
		lifecycle: lifecycle, contexts: contexts, processes: processes, propagations: propagations,
		frontiers:     frontiers,
		maxFirings:    normalized.Limits.MaxFirings,
		moduleParents: moduleParents, moduleTemplates: moduleTemplates,
	}
	startupFrontiers, err := componentStartupFrontiers(model, architectureStarts, componentStarts)
	if err != nil {
		return nil, err
	}
	functionRuntime.startupFrontiers = startupFrontiers
	seenItems := make(map[string]bool)
	pipeState := make(map[string]gorapide.EventID)
	fired := make(map[string]bool)
	ruleFired := make(map[string]bool)
	consumption := NewRuleConsumption()
	observed := make(map[string]gorapide.EventSet, len(model.components))
	architectureObserved := make(gorapide.EventSet, 0)
	observedViews := make(map[string]bool)
	observedOccurrences := make(map[gorapide.EventID]bool)
	// Architecture Start occurrences are system events generated and observed as
	// part of instance creation, before component startup or journal observation.
	for _, start := range architectureStarts {
		observedOccurrences[start.ID] = true
	}
	for _, start := range moduleStarts {
		observedOccurrences[start.ID] = true
	}
	for _, start := range recordStarts {
		observedOccurrences[start.ID] = true
	}
	observationRanks := make(map[string]uint64)
	var nextObservationRank uint64
	var firings []FiringRecord
	functionRuntime.choices = choices
	functionRuntime.observed = observed
	functionRuntime.architectureSeen = &architectureObserved
	functionRuntime.observedOccurrences = observedOccurrences
	functionRuntime.firings = &firings
	functionRuntime.connectionFired = fired
	functionRuntime.connectionPipe = pipeState
	postScopeProcessContinuation := false
	postScopeContinuation := false
	startupAbandonment, err := executeModuleInitialParts(
		model, functionRuntime, state, processes, statementSteps, clocks, frontiers,
		poset, depths, queue, seenItems, stateSnapshots,
		startupFrontiers, normalized.Limits.MaxFirings, &firings,
	)
	if err != nil {
		return nil, err
	}
	inputEvents := make(map[string]*gorapide.Event)
	if startupAbandonment != nil {
		terminationContext := processTerminationContext{
			modelDigest: model.digest, functionRuntime: functionRuntime,
			frontiers: frontiers, clocks: clocks, poset: poset, depths: depths,
			queue: queue, seenItems: seenItems, moduleState: state,
			stateSnapshots: stateSnapshots, statementSteps: statementSteps, firings: &firings,
		}
		if err := finalizeArchitectureInitializationAbandonment(
			model, startupAbandonment, terminationContext,
		); err != nil {
			return nil, err
		}
		// The enclosing architecture never reaches connection elaboration or
		// Return. Generated cleanup events remain in the poset and firing audit,
		// but no architecture connection or process may observe them.
		queue = &executionQueue{}
		heap.Init(queue)
		seenItems = make(map[string]bool)
	} else {
		startupAbandonment, err = executeArchitectureInitialParts(
			model, functionRuntime, state, statementSteps, clocks, frontiers,
			poset, depths, queue, seenItems, stateSnapshots, architectureStarts,
			normalized.Limits.MaxFirings, &firings,
		)
		if err != nil {
			return nil, err
		}
		if startupAbandonment != nil {
			// The generator returned no architecture value. Mark that ownership
			// boundary before materializing constituent final parts so only their
			// module-owned connection aliases can survive long enough to observe
			// final actions; architecture routes close immediately.
			functionRuntime.architectureScopeClosed = true
			functionRuntime.postScopeFinalConnectionSources = make(map[string]bool)
			terminationContext := processTerminationContext{
				modelDigest: model.digest, functionRuntime: functionRuntime,
				frontiers: frontiers, clocks: clocks, poset: poset, depths: depths,
				queue: queue, seenItems: seenItems, moduleState: state,
				stateSnapshots: stateSnapshots, statementSteps: statementSteps, firings: &firings,
			}
			if err := finalizeArchitectureInitializationAbandonment(
				model, startupAbandonment, terminationContext,
			); err != nil {
				return nil, err
			}
			postScopeComponents, err := architectureAbandonmentPostScopeProcessComponents(functionRuntime)
			if err != nil {
				return nil, err
			}
			postScopeProcessContinuation = len(postScopeComponents) != 0
			postScopeContinuation = postScopeProcessContinuation ||
				len(functionRuntime.postScopeFinalConnectionSources) != 0
			if postScopeProcessContinuation {
				// The architecture value and its wiring no longer exist, but an
				// independently running module retains its own process, Context,
				// module-local wiring, state, and clocks until process completion.
				// Journal input remains suppressed because no architecture value was
				// returned to the environment.
				functionRuntime.postScopeComponents = make(map[string]bool, len(postScopeComponents))
				for _, componentID := range postScopeComponents {
					functionRuntime.postScopeComponents[componentID] = true
				}
			}
			if !postScopeContinuation {
				queue = &executionQueue{}
				heap.Init(queue)
				seenItems = make(map[string]bool)
			}
		} else {
			inputDepths := make(map[gorapide.EventID]uint64)
			inputEvents, inputDepths, err = buildInputEvents(
				normalized, model, poset, clocks, architectureStarts, componentStarts, depths,
			)
			if err != nil {
				return nil, err
			}
			for id, depth := range inputDepths {
				depths[id] = depth
			}
			for _, event := range inputEvents {
				if err := stateSnapshots.capture(event, state[event.Source]); err != nil {
					return nil, err
				}
			}
		}
	}
	startupComplete := startupAbandonment != nil && !postScopeProcessContinuation
	for {
		if startupAbandonment != nil && !postScopeContinuation {
			break
		}
		readyContinuations := readyProcessContinuations(processes)
		hasReadyObservation := hasReadyExecutionItem(queue, poset, observedOccurrences)
		if queue.Len() != 0 && !hasReadyObservation {
			return nil, fmt.Errorf("deterministic execution queue has no causally ready observation")
		}
		if !startupComplete && !hasReadyObservation {
			startupComplete = true
			// Every immediate event generated by every module initial part has now
			// passed through connections and reactive rules. Processes enter their
			// initial awaits only after that closed startup computation exists.
			for _, componentID := range deterministicProcessComponentIDs(processes) {
				if functionRuntime.architectureScopeClosed &&
					!architecturePostScopeComponentIsRunning(functionRuntime, componentID) {
					continue
				}
				if err := fireDeclarativeProcesses(
					componentID, model, processes, poset, observed, observationRanks,
					consumption, depths, queue, seenItems, choices,
					state, functionRuntime, stateSnapshots, statementSteps, clocks, frontiers,
					normalized.Limits.MaxFirings, &firings,
				); err != nil {
					return nil, err
				}
			}
			// Journal inputs become observable only after startup and initial
			// process activation. Their immutable snapshots were captured after
			// module initial state completed and before any process body ran.
			for _, event := range inputEvents {
				if !clocks.deferInput(event, depths[event.ID]) {
					enqueueExecutionItem(queue, seenItems, event, depths[event.ID])
				}
			}
			continue
		}
		if hasReadyObservation && len(readyContinuations) != 0 {
			if !hasActiveProcessInterruptHandler(processes) {
				selectedStep, err := choices.resolve("semantic-step", []string{"observe", "resume"})
				if err != nil {
					return nil, err
				}
				if selectedStep == "resume" {
					if err := resumeOneProcessContinuation(
						readyContinuations, model, choices, functionRuntime, state,
						statementSteps, clocks, frontiers, poset, depths, queue,
						seenItems, stateSnapshots, &firings,
					); err != nil {
						return nil, err
					}
					continue
				}
			}
		} else if len(readyContinuations) != 0 {
			if err := resumeOneProcessContinuation(
				readyContinuations, model, choices, functionRuntime, state,
				statementSteps, clocks, frontiers, poset, depths, queue,
				seenItems, stateSnapshots, &firings,
			); err != nil {
				return nil, err
			}
			continue
		}
		if hasReadyObservation {
			item, err := popReadyExecutionItem(queue, poset, observedOccurrences, choices)
			if err != nil {
				return nil, err
			}
			current := item.event
			observedOccurrences[current.ID] = true
			viewKey := executionItemKey(current)
			if !observedViews[viewKey] {
				observedViews[viewKey] = true
				rankKey := observationRankKey(current)
				if _, exists := observationRanks[rankKey]; !exists {
					nextObservationRank++
					observationRanks[rankKey] = nextObservationRank
				}
				observed[current.Source] = append(observed[current.Source], current.Snapshot())
				architectureObserved = append(architectureObserved, current.Snapshot())
			}
			moduleObserverSet := map[string]bool{current.Source: true}
			if interfaceMatchesAction(functionRuntime.components[current.Source], current.Name, OutAction, current.Params) {
				for _, recipient := range contexts.recipientsAt(current.Source, current.ID, poset) {
					moduleObserverSet[recipient] = true
					observed[recipient] = append(observed[recipient], current.Snapshot())
					if err := stateSnapshots.captureFor(
						recipient, current,
						executionComponentState(state, functionRuntime, recipient),
					); err != nil {
						return nil, err
					}
				}
			}
			moduleObservers := make([]string, 0, len(moduleObserverSet))
			for observer := range moduleObserverSet {
				moduleObservers = append(moduleObservers, observer)
			}
			sort.Strings(moduleObservers)

			for _, connection := range functionRuntime.connections {
				if functionRuntime.architectureScopeClosed {
					if connection.Scope == ArchitectureConnectionScope ||
						!architecturePostScopeModuleConnectionIsOpen(functionRuntime, connection.From) {
						continue
					}
				}
				if connection.Scope == ArchitectureConnectionScope &&
					!architectureConnectionSourceMatches(connection, model, current) {
					continue
				}
				if connection.Scope == ModuleConnectionScope &&
					!interfaceMatchesModuleAction(functionRuntime.components[current.Source], current.Name, current.Params) {
					continue
				}
				visiblePool := architectureObserved
				if connection.Scope == ModuleConnectionScope {
					if !moduleObserverSet[connection.From] {
						continue
					}
					visiblePool = observed[connection.From]
				} else if connection.From != "*" && connection.From != current.Source {
					continue
				}
				visible := connectionVisibleEvents(connection, visiblePool, model, functionRuntime.components)
				view := newObservationView(visible, poset, functionRuntime)
				matches := []pattern.MatchResult{{Events: gorapide.EventSet{current.Snapshot()}}}
				if connection.Trigger != nil {
					trigger := connection.Trigger
					if connection.Scope == ModuleConnectionScope && pattern.HasModuleSourceBinding(trigger) {
						trigger, err = pattern.ScopeUnqualifiedEventSources(trigger, connection.From)
						if err != nil {
							return nil, fmt.Errorf("connection %q trigger source scope: %w", connection.ID, err)
						}
					}
					matches, err = pattern.MatchWithBindings(trigger, view)
					if err != nil {
						return nil, fmt.Errorf("connection %q trigger: %w", connection.ID, err)
					}
				}
				candidates, err := canonicalConnectionMatches(matches, current.ID)
				if err != nil {
					return nil, fmt.Errorf("connection %q trigger matches: %w", connection.ID, err)
				}
				for _, candidate := range candidates {
					anchor := current
					if len(candidate.match.Events) == 1 {
						anchor = candidate.match.Events[0]
					}
					connectionParameters, err := connection.resolveClosedParameters(anchor, candidate.match.Bindings)
					if err != nil {
						return nil, fmt.Errorf("connection %q parameters: %w", connection.ID, err)
					}
					for _, targetID := range deterministicTargets(connection, anchor, model) {
						firingKey := connection.ID + "\x00" + targetID + "\x00" + candidate.key
						if fired[firingKey] {
							continue
						}
						if uint64(len(firings)) >= normalized.Limits.MaxFirings {
							return nil, fmt.Errorf("%w: max_firings=%d", ErrExecutionLimit, normalized.Limits.MaxFirings)
						}
						if err := validateConnectionTargetAction(model, functionRuntime.components, connection, targetID, connection.outputAction(current), connectionParameters); err != nil {
							return nil, fmt.Errorf("connection %q target %q: %w", connection.ID, targetID, err)
						}
						stateKey := connection.ID + "\x00" + targetID
						connectionTimings := clocks.instantTimings(targetID)
						if connection.Kind == BasicConnection {
							connectionTimings = clocks.missingObservationTimings(anchor, targetID)
						}
						output, nextPrevious, err := connection.applyResolvedMatch(
							poset, candidate.match, anchor, targetID, pipeState[stateKey], connectionParameters,
							connectionTimings,
						)
						if err != nil {
							return nil, fmt.Errorf("deterministic firing %q: %w", connection.ID, err)
						}
						fired[firingKey] = true
						if connection.Kind == PipeConnection {
							pipeState[stateKey] = nextPrevious
						}
						outputDepth := item.depth
						if output.ID != current.ID {
							outputDepth = eventDepth(poset, output, depths)
							depths[output.ID] = outputDepth
						}
						if err := stateSnapshots.capture(
							output,
							executionComponentState(state, functionRuntime, output.Source),
						); err != nil {
							return nil, err
						}
						enqueueExecutionItem(queue, seenItems, output, outputDepth)
						firings = append(firings, FiringRecord{
							Sequence: uint64(len(firings) + 1), Transition: "connection",
							ConnectionID: connection.ID, ConnectionKind: connection.Kind.String(), ConnectionScope: connection.Scope.String(),
							TriggerID: string(current.ID), TriggerSource: current.Source,
							TriggerAction: current.Name, MatchedEvents: append([]string(nil), candidate.canonical.Events...),
							Bindings: append([]pattern.CanonicalBinding(nil), candidate.canonical.Bindings...),
							Target:   targetID, ResultID: string(output.ID),
						})
					}
				}
			}

			for _, observer := range moduleObservers {
				if functionRuntime.architectureScopeClosed &&
					!architecturePostScopeComponentIsRunning(functionRuntime, observer) {
					continue
				}
				if err := fireDeclarativeRules(
					observer, model, poset, observed, observationRanks, consumption,
					ruleFired, frontiers, clocks, depths, queue, seenItems, choices,
					state, functionRuntime, stateSnapshots, statementSteps, normalized.Limits.MaxFirings, &firings,
				); err != nil {
					return nil, err
				}
				if startupComplete {
					if err := fireDeclarativeProcesses(
						observer, model, processes, poset, observed, observationRanks,
						consumption, depths, queue, seenItems, choices,
						state, functionRuntime, stateSnapshots, statementSteps, clocks, frontiers,
						normalized.Limits.MaxFirings, &firings,
					); err != nil {
						return nil, err
					}
				}
			}
			continue
		}
		// No event observation or process continuation is ready. Give
		// ordinary await/when/else activations a canonical component turn before
		// advancing a clock. Ready timed continuations are handled above as global
		// semantic alternatives, never as a side effect of component traversal.
		for _, componentID := range deterministicProcessComponentIDs(processes) {
			if functionRuntime.architectureScopeClosed &&
				!architecturePostScopeComponentIsRunning(functionRuntime, componentID) {
				continue
			}
			if err := fireDeclarativeProcesses(
				componentID, model, processes, poset, observed, observationRanks,
				consumption, depths, queue, seenItems, choices,
				state, functionRuntime, stateSnapshots, statementSteps, clocks, frontiers,
				normalized.Limits.MaxFirings, &firings,
			); err != nil {
				return nil, err
			}
		}
		if queue.Len() != 0 {
			continue
		}
		advanced, err := clocks.advanceAndRelease(
			normalized.Profile, model.digest, choices, frontiers, poset, depths,
			queue, seenItems, state, stateSnapshots, functionRuntime, statementSteps, &firings,
		)
		if err != nil {
			return nil, err
		}
		if !advanced {
			finalized, err := finalizePostScopeCompletedStaticModules(processTerminationContext{
				modelDigest: model.digest, functionRuntime: functionRuntime,
				frontiers: frontiers, clocks: clocks, poset: poset, depths: depths,
				queue: queue, seenItems: seenItems, moduleState: state,
				stateSnapshots: stateSnapshots, statementSteps: statementSteps, firings: &firings,
			})
			if err != nil {
				return nil, err
			}
			if finalized {
				continue
			}
			break
		}
	}

	canonicalConsumption, err := consumption.Canonical()
	if err != nil {
		return nil, err
	}
	if err := choices.finish(); err != nil {
		return nil, err
	}
	processRecords := canonicalProcessExecutionRecords(processes, frontiers)
	if err := updateStaticModuleLifecycleStates(lifecycle, model, staticModules, processes); err != nil {
		return nil, err
	}
	moduleRecords, err := lifecycle.records()
	if err != nil {
		return nil, err
	}
	stateRecords, err := canonicalStateRecords(state)
	if err != nil {
		return nil, err
	}
	stateOperations, err := canonicalStateOperationRecords(state)
	if err != nil {
		return nil, err
	}
	stateWitnesses := newConstraintStateWitnessKernel(poset, stateOperations, normalized.Limits)
	var constraintReport *constraint.CanonicalConstraintSetReport
	if model.constraintSet != nil {
		constraintView, err := architectureConstraintView(model, poset)
		if err != nil {
			return nil, err
		}
		witnesses, err := stateWitnesses.derive(model.constraintSet, constraintView)
		if err != nil {
			return nil, err
		}
		report, err := model.constraintSet.EvaluateCanonicalWithState(constraintView, witnesses)
		if err != nil {
			return nil, err
		}
		constraintReport = &report
	}
	architectureConstraintReports := make([]ArchitectureConstraintRecord, 0, len(model.architectureConstraints))
	for _, instanceID := range model.architectureInstanceIDs {
		set := model.architectureConstraints[instanceID]
		if set == nil {
			continue
		}
		constraintView, err := architectureConstraintViewForOwner(model, poset, instanceID)
		if err != nil {
			return nil, err
		}
		witnesses, err := stateWitnesses.derive(set, constraintView)
		if err != nil {
			return nil, fmt.Errorf("architecture instance %q constraint state: %w", instanceID, err)
		}
		report, err := set.EvaluateCanonicalWithState(constraintView, witnesses)
		if err != nil {
			return nil, fmt.Errorf("architecture instance %q constraints: %w", instanceID, err)
		}
		architectureConstraintReports = append(architectureConstraintReports, ArchitectureConstraintRecord{
			ArchitectureInstance: instanceID, Report: report,
		})
	}
	moduleConstraintReports := make([]ModuleConstraintRecord, 0, len(model.moduleConstraints))
	for _, componentID := range model.componentIDs {
		set := model.moduleConstraints[componentID]
		if set == nil {
			continue
		}
		constraintView, err := moduleConstraintView(componentID, poset)
		if err != nil {
			return nil, err
		}
		witnesses, err := stateWitnesses.derive(set, constraintView)
		if err != nil {
			return nil, fmt.Errorf("component %q module constraint state: %w", componentID, err)
		}
		report, err := set.EvaluateCanonicalWithState(constraintView, witnesses)
		if err != nil {
			return nil, fmt.Errorf("component %q module constraints: %w", componentID, err)
		}
		moduleConstraintReports = append(moduleConstraintReports, ModuleConstraintRecord{
			ComponentID: componentID, Report: report,
		})
	}
	return &ExecutionResult{
		Poset: poset, Profile: normalized.Profile, ModelDigest: model.digest,
		JournalDigest: journalDigest, Firings: firings, Consumption: canonicalConsumption,
		RuleSelection:           RuleSelectionPolicy,
		SemanticStepPolicy:      SemanticStepPolicy,
		Choices:                 choices.canonicalResolutions(),
		Processes:               processRecords,
		Modules:                 moduleRecords,
		Contexts:                contexts.records(),
		ExceptionPropagations:   propagations.records(),
		State:                   stateRecords,
		StateOperations:         stateOperations,
		Iterators:               finiteIteratorStateRecords(iteratorRuntimes),
		Constraints:             constraintReport,
		ArchitectureConstraints: architectureConstraintReports,
		ModuleConstraints:       moduleConstraintReports,
		StatementSteps:          statementSteps.used,
		ClockPolicy:             ClockAdvancePolicy,
		Clocks:                  clocks.stateRecords(),
		ClockAdvances:           append([]ClockAdvanceRecord{}, clocks.advances...),
		ScheduledEvents:         append([]ScheduledEventRecord{}, clocks.scheduledEvents...),
	}, nil
}

type declarativeRuleCandidate struct {
	rule        *DeclarativeRule
	match       pattern.MatchResult
	canonical   pattern.CanonicalMatch
	key         string
	firstRank   uint64
	guardReads  []StateReadRecord
	guardCauses []gorapide.EventID
}

func fireDeclarativeRules(
	componentID string,
	model *deterministicModel,
	poset *gorapide.Poset,
	observed map[string]gorapide.EventSet,
	observationRanks map[string]uint64,
	consumption *RuleConsumption,
	fired map[string]bool,
	frontiers *causalFrontierRegistry,
	clocks *deterministicClockKernel,
	depths map[gorapide.EventID]uint64,
	queue *executionQueue,
	seenItems map[string]bool,
	choices *choiceResolver,
	state moduleStateRuntime,
	functionRuntime *functionExecutionRuntime,
	stateSnapshots stateSnapshotRegistry,
	statementSteps *statementBudget,
	maxFirings uint64,
	firings *[]FiringRecord,
) error {
	component := model.components[componentID]
	if component == nil || len(model.rules[componentID]) == 0 {
		return nil
	}
	for {
		candidates, err := eligibleDeclarativeRuleCandidates(
			componentID, model.rules[componentID], poset, observed[componentID],
			observationRanks, consumption, fired, state[componentID], stateSnapshots, functionRuntime,
		)
		if err != nil {
			return err
		}
		if len(candidates) == 0 {
			return nil
		}
		options := make([]string, len(candidates))
		byOption := make(map[string]declarativeRuleCandidate, len(candidates))
		for i, current := range candidates {
			option := declarativeRuleChoiceKey(componentID, current)
			options[i] = option
			byOption[option] = current
		}
		selected, err := choices.resolve("declarative-rule:"+componentID, options)
		if err != nil {
			return err
		}
		candidate := byOption[selected]
		if uint64(len(*firings)) >= maxFirings {
			return fmt.Errorf("%w: max_firings=%d", ErrExecutionLimit, maxFirings)
		}

		scope := componentID + "\x00" + candidate.rule.ID
		causalOwner := "rule-agent:" + scope + "\x00" + candidate.key
		previousFrontier := []gorapide.EventID(nil)
		if candidate.rule.Process == RulePipeProcess {
			causalOwner = "rule-pipe:" + scope
			previousFrontier = frontiers.get(causalOwner)
		}
		bodyResult, err := buildDeclarativeRuleBody(
			componentID, component, candidate.rule, candidate.match, candidate.canonical,
			model.digest, previousFrontier, state[componentID], functionRuntime,
			candidate.guardCauses, candidate.guardReads, statementSteps, clocks, causalOwner, nil,
		)
		if err != nil {
			return fmt.Errorf("deterministic rule %s.%s: %w", componentID, candidate.rule.ID, err)
		}
		if bodyResult.raised != nil {
			return fmt.Errorf("deterministic rule %s.%s: %w: %s",
				componentID, candidate.rule.ID, ErrUnhandledRapideException, bodyResult.raised.name)
		}
		if err := consumption.Consume(scope, candidate.match.Events); err != nil {
			return fmt.Errorf("deterministic rule %s.%s: %w", componentID, candidate.rule.ID, err)
		}
		for _, output := range bodyResult.generated {
			if err := poset.AddEventWithCause(output.event, output.causes...); err != nil {
				return fmt.Errorf("deterministic rule %s.%s output %s: %w", componentID, candidate.rule.ID, output.localID, err)
			}
		}
		fired[scope+"\x00"+candidate.key] = true
		if len(bodyResult.frontier) > 0 && (len(bodyResult.generated) != 0 || len(bodyResult.scheduled) != 0) {
			frontiers.set(causalOwner, bodyResult.frontier)
		}
		clocks.addScheduled(bodyResult.scheduled)
		generatedRecord := make([]GeneratedEventRecord, 0, len(bodyResult.generated))
		for _, output := range bodyResult.generated {
			depth := eventDepth(poset, output.event, depths)
			depths[output.event.ID] = depth
			if err := enqueueGeneratedObservationViews(
				output, depth, state, stateSnapshots, queue, seenItems,
			); err != nil {
				return err
			}
			generatedRecord = append(generatedRecord, GeneratedEventRecord{
				OutputID: output.localID, EventID: string(output.event.ID), Exception: output.exception,
			})
		}
		firing := FiringRecord{
			Sequence: uint64(len(*firings) + 1), Transition: "rule",
			RuleID: candidate.rule.ID, RuleProcess: candidate.rule.Process.String(),
			MatchedEvents: append([]string(nil), candidate.canonical.Events...),
			Bindings:      append([]pattern.CanonicalBinding(nil), candidate.canonical.Bindings...),
			Target:        componentID, Generated: generatedRecord,
			Scheduled:         scheduledPlans(bodyResult.scheduled),
			CanceledSchedules: append([]string(nil), bodyResult.canceledSchedules...),
			StateReads:        bodyResult.stateReads, StateWrites: bodyResult.stateWrites,
		}
		if bodyResult.initializationFailure != nil {
			firing.Completion = "exception"
			firing.ExceptionEventID = string(bodyResult.initializationFailure.raised.event.ID)
		}
		*firings = append(*firings, firing)
		if bodyResult.initializationFailure != nil {
			failure := bodyResult.initializationFailure
			terminationContext := processTerminationContext{
				modelDigest: model.digest, functionRuntime: functionRuntime,
				frontiers: frontiers, clocks: clocks, poset: poset, depths: depths,
				queue: queue, seenItems: seenItems, moduleState: state,
				stateSnapshots: stateSnapshots, statementSteps: statementSteps, firings: firings,
			}
			if _, err := finalizeFailedModuleInitializationChain(failure, terminationContext); err != nil {
				return fmt.Errorf("deterministic rule %s.%s allocator initialization: %w",
					componentID, candidate.rule.ID, err)
			}
			// A transition-rule body is one behavior activation, not a module
			// process. The nonreturning generator call abandons this activation,
			// while the enclosing behavior remains ready to select later events.
			// Yield so the failed creation's generated and finalization occurrences
			// become observable before another behavior activation is selected.
			return nil
		}
	}
}

type generatedRuleOutput struct {
	localID              string
	event                *gorapide.Event
	causes               []gorapide.EventID
	stateSnapshot        map[string]*stateCell
	observationSnapshots map[string]map[string]*stateCell
	exception            bool
	connectionOutput     bool
}

func enqueueGeneratedObservationViews(
	output generatedRuleOutput,
	depth uint64,
	moduleState moduleStateRuntime,
	stateSnapshots stateSnapshotRegistry,
	queue *executionQueue,
	seenItems map[string]bool,
) error {
	for _, view := range output.event.ObservationViews() {
		snapshot := output.stateSnapshot
		if output.observationSnapshots != nil && output.observationSnapshots[view.Source] != nil {
			snapshot = output.observationSnapshots[view.Source]
		} else if view.Source != output.event.Source && moduleState[view.Source] != nil {
			snapshot = moduleState[view.Source]
		}
		if err := stateSnapshots.capture(view, snapshot); err != nil {
			return err
		}
		enqueueExecutionItem(queue, seenItems, view, depth)
	}
	return nil
}

type ruleBodyExecution struct {
	generated              []generatedRuleOutput
	scheduled              []scheduledAction
	frontier               []gorapide.EventID
	stateReads             []StateReadRecord
	stateWrites            []StateWriteRecord
	exitProcess            bool
	raised                 *raisedExceptionOccurrence
	stateOperationFrontier []stateOperationReference
	initializationFailure  *failedModuleInitialization
	canceledSchedules      []string
}

// buildDeclarativeRuleBody materializes a finite rule-body poset without
// mutating execution state. Topological ties are resolved by stable local IDs.
// Body roots inherit the complete trigger match and, for pipe rules, the
// previous body's causal frontier. Descendants inherit those relationships
// transitively through the body-local edges.
func buildDeclarativeRuleBody(
	componentID string,
	component *Component,
	rule *DeclarativeRule,
	match pattern.MatchResult,
	canonical pattern.CanonicalMatch,
	modelDigest string,
	previousFrontier []gorapide.EventID,
	cells map[string]*stateCell,
	functionRuntime *functionExecutionRuntime,
	activationCauses []gorapide.EventID,
	activationReads []StateReadRecord,
	statementSteps *statementBudget,
	clocks *deterministicClockKernel,
	causalOwner string,
	priorOperations []stateOperationReference,
	handledExceptions ...*raisedExceptionOccurrence,
) (ruleBodyExecution, error) {
	if rule == nil || rule.Body == nil {
		return ruleBodyExecution{}, fmt.Errorf("%w: missing normalized rule body", ErrInvalidDeclarativeRule)
	}
	ordered, err := ruleBodyOrder(rule.Body.Outputs)
	if err != nil {
		return ruleBodyExecution{}, err
	}
	matchDigest, err := pattern.SemanticDigestMatches([]pattern.MatchResult{match})
	if err != nil {
		return ruleBodyExecution{}, err
	}
	triggerCauses := make([]gorapide.EventID, len(canonical.Events))
	for i, eventID := range canonical.Events {
		triggerCauses[i] = gorapide.EventID(eventID)
	}
	baseCauses := append([]gorapide.EventID(nil), triggerCauses...)
	if rule.Process == RulePipeProcess {
		baseCauses = append(baseCauses, previousFrontier...)
	}
	baseCauses = append(baseCauses, activationCauses...)
	baseCauses = canonicalEventIDs(baseCauses)
	stateReads := append([]StateReadRecord(nil), activationReads...)
	assignmentReads, stateWrites, err := applyStateAssignments(
		rule.ID, rule.Body.Assignments, match.Bindings, cells, baseCauses,
		canonicalStateOperationReferences(append(priorOperations, stateOperationReferences(activationReads, nil)...)),
	)
	if err != nil {
		return ruleBodyExecution{}, err
	}
	stateReads = append(stateReads, assignmentReads...)
	baseOperations := canonicalStateOperationReferences(append(priorOperations, stateOperationReferences(stateReads, stateWrites)...))
	if rule.Body.Statements != nil {
		statementResult, err := executeRuleStatements(
			componentID, component, rule, match, matchDigest, modelDigest,
			rule.Body.Statements, functionRuntime, cells, baseCauses,
			statementSteps, clocks, causalOwner,
			stateOperationReferences(stateReads, stateWrites),
			nil,
			handledExceptions...,
		)
		if err != nil {
			return ruleBodyExecution{}, err
		}
		stateReads = append(stateReads, statementResult.reads...)
		stateWrites = append(stateWrites, statementResult.writes...)
		return ruleBodyExecution{
			generated: statementResult.generated, scheduled: statementResult.scheduled,
			frontier:               statementResult.control,
			stateReads:             qualifyStateReads(componentID, stateReads),
			stateWrites:            qualifyStateWrites(componentID, stateWrites),
			exitProcess:            statementResult.exitProcess,
			raised:                 statementResult.raised,
			stateOperationFrontier: statementResult.pendingOperations,
			initializationFailure:  statementResult.initializationFailure,
			canceledSchedules: func() []string {
				canceled := append([]string(nil), statementResult.canceledSchedules...)
				if statementResult.initializationFailure != nil {
					canceled = append(canceled, statementResult.initializationFailure.canceledSchedules...)
				}
				sort.Strings(canceled)
				return canceled
			}(),
		}, nil
	}

	children := make(map[string]bool, len(ordered))
	actual := make(map[string]*gorapide.Event, len(ordered))
	generated := make([]generatedRuleOutput, 0, len(ordered))
	for _, output := range ordered {
		parameters, outputReads, stateCauses, err := resolveRuleParameters(rule.ID, output, match.Bindings, cells)
		if err != nil {
			return ruleBodyExecution{}, err
		}
		stateReads = append(stateReads, outputReads...)
		if !interfaceMatchesGeneratedAction(component, output.Action, parameters) {
			return ruleBodyExecution{}, fmt.Errorf("%w: output action %s.%s", ErrActionTypeMismatch, componentID, output.Action)
		}
		causes := make([]gorapide.EventID, 0, len(output.Causes)+len(triggerCauses)+len(previousFrontier))
		for _, localCause := range output.Causes {
			cause := actual[localCause]
			if cause == nil {
				return ruleBodyExecution{}, fmt.Errorf("%w: output %q has unavailable body cause %q", ErrInvalidDeclarativeRule, output.ID, localCause)
			}
			causes = append(causes, cause.ID)
			children[localCause] = true
		}
		if len(output.Causes) == 0 {
			causes = append(causes, baseCauses...)
		}
		causes = canonicalEventIDs(append(causes, stateCauses...))
		outputReadOperations := stateOperationReferences(outputReads, nil)
		dependencies := append(eventIDStrings(causes), stateOperationReferenceIDs(baseOperations)...)
		if err := addStateOperationDependencies(outputReadOperations, dependencies...); err != nil {
			return ruleBodyExecution{}, err
		}
		event, err := gorapide.NewDeterministicEvent(gorapide.EventProvenance{
			Profile: CompatibilityProfile, Model: modelDigest, Instance: componentID,
			Action:     output.Action,
			Occurrence: rule.ID + "|match=" + matchDigest + "|output=" + output.ID,
			Causes:     causes,
			Timings:    clocks.instantTimings(componentID),
		}, parameters)
		if err != nil {
			return ruleBodyExecution{}, fmt.Errorf("output %q: %w", output.ID, err)
		}
		if err := addStateOperationSuccessors(
			canonicalStateOperationReferences(append(baseOperations, outputReadOperations...)),
			string(event.ID),
		); err != nil {
			return ruleBodyExecution{}, err
		}
		actual[output.ID] = event
		generated = append(generated, generatedRuleOutput{
			localID: output.ID, event: event, causes: causes,
			stateSnapshot: cloneStateCells(cells),
		})
	}

	frontier := make([]gorapide.EventID, 0, len(generated))
	for _, output := range generated {
		if !children[output.localID] {
			frontier = append(frontier, output.event.ID)
		}
	}
	sort.Slice(frontier, func(i, j int) bool { return frontier[i] < frontier[j] })
	return ruleBodyExecution{
		generated: generated, frontier: frontier,
		stateReads:  qualifyStateReads(componentID, stateReads),
		stateWrites: qualifyStateWrites(componentID, stateWrites),
		stateOperationFrontier: func() []stateOperationReference {
			if len(generated) == 0 {
				return baseOperations
			}
			return nil
		}(),
	}, nil
}

func eligibleDeclarativeRuleCandidates(
	componentID string,
	rules []*DeclarativeRule,
	poset *gorapide.Poset,
	observed gorapide.EventSet,
	observationRanks map[string]uint64,
	consumption *RuleConsumption,
	fired map[string]bool,
	state map[string]*stateCell,
	stateSnapshots stateSnapshotRegistry,
	functionRuntime *functionExecutionRuntime,
) ([]declarativeRuleCandidate, error) {
	candidates := make([]declarativeRuleCandidate, 0)
	for _, rule := range rules {
		scope := componentID + "\x00" + rule.ID
		available, err := consumption.Available(scope, observed)
		if err != nil {
			return nil, err
		}
		if !pattern.HasModuleSourceBinding(rule.Trigger) {
			local := make(gorapide.EventSet, 0, len(available))
			for _, event := range available {
				if event != nil && event.Source == componentID {
					local = append(local, event)
				}
			}
			available = local
		}
		trigger := rule.Trigger
		if pattern.HasModuleSourceBinding(trigger) {
			trigger, err = pattern.ScopeUnqualifiedEventSources(trigger, componentID)
			if err != nil {
				return nil, fmt.Errorf("deterministic rule %s.%s trigger source scope: %w", componentID, rule.ID, err)
			}
		}
		view := newObservationView(available, poset, functionRuntime)
		matches, err := pattern.MatchWithBindings(trigger, view)
		if err != nil {
			return nil, fmt.Errorf("deterministic rule %s.%s trigger: %w", componentID, rule.ID, err)
		}
		for _, match := range matches {
			guardMatched, guardReads, guardCauses, err := evaluateMatchGuard(
				"rule "+componentID+"."+rule.ID, componentID, rule.Guard, match,
				observationRanks, stateSnapshots, state, nil,
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
			key := string(encoded)
			if fired[scope+"\x00"+key] {
				continue
			}
			firstRank := ^uint64(0)
			for _, event := range match.Events {
				if rank := observationRanks[observationRankKey(event)]; rank < firstRank {
					firstRank = rank
				}
			}
			if len(match.Events) == 0 {
				firstRank = 0
			}
			candidates = append(candidates, declarativeRuleCandidate{
				rule: rule, match: match, canonical: canonical, key: key, firstRank: firstRank,
				guardReads: guardReads, guardCauses: guardCauses,
			})
		}
	}
	if len(candidates) == 0 {
		return nil, nil
	}
	minimumFirst := candidates[0].firstRank
	for _, candidate := range candidates[1:] {
		if candidate.firstRank < minimumFirst {
			minimumFirst = candidate.firstRank
		}
	}
	write := 0
	for _, candidate := range candidates {
		if candidate.firstRank == minimumFirst {
			candidates[write] = candidate
			write++
		}
	}
	candidates = candidates[:write]
	var err error
	candidates, err = earliestRuleCandidates(candidates, poset)
	if err != nil {
		return nil, err
	}
	candidates = maximalRuleCandidates(candidates)
	// First, earliest, and maximal have now resolved every match they order. A
	// stable declaration/match key resolves only the remaining choice that the
	// Rapide manual explicitly leaves arbitrary.
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].rule.ID != candidates[j].rule.ID {
			return candidates[i].rule.ID < candidates[j].rule.ID
		}
		return candidates[i].key < candidates[j].key
	})
	return candidates, nil
}

// selectDeclarativeRuleCandidate retains the focused selection helper used by
// conformance tests for unguarded, stateless rules.
func selectDeclarativeRuleCandidate(
	componentID string,
	rules []*DeclarativeRule,
	poset *gorapide.Poset,
	observed gorapide.EventSet,
	observationRanks map[string]uint64,
	consumption *RuleConsumption,
	fired map[string]bool,
) (declarativeRuleCandidate, bool, error) {
	candidates, err := eligibleDeclarativeRuleCandidates(
		componentID, rules, poset, observed, observationRanks, consumption, fired,
		nil, nil, nil,
	)
	if err != nil || len(candidates) == 0 {
		return declarativeRuleCandidate{}, false, err
	}
	return candidates[0], true, nil
}

func declarativeRuleChoiceKey(componentID string, candidate declarativeRuleCandidate) string {
	return componentID + "/" + candidate.rule.ID + "@" + digestBytes([]byte(candidate.key))
}

func earliestRuleCandidates(candidates []declarativeRuleCandidate, poset pattern.PosetReader) ([]declarativeRuleCandidate, error) {
	result := make([]declarativeRuleCandidate, 0, len(candidates))
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

func maximalRuleCandidates(candidates []declarativeRuleCandidate) []declarativeRuleCandidate {
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
	result := make([]declarativeRuleCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if keys[candidate.key] {
			result = append(result, candidate)
		}
	}
	return result
}

func maximalRuleMatches(matches []pattern.MatchResult) []pattern.MatchResult {
	result := make([]pattern.MatchResult, 0, len(matches))
	sets := make([]map[gorapide.EventID]bool, len(matches))
	for i, match := range matches {
		sets[i] = make(map[gorapide.EventID]bool, len(match.Events))
		for _, event := range match.Events {
			sets[i][event.ID] = true
		}
	}
	for i, match := range matches {
		maximal := true
		for j := range matches {
			if i == j || len(sets[i]) >= len(sets[j]) {
				continue
			}
			subset := true
			for eventID := range sets[i] {
				if !sets[j][eventID] {
					subset = false
					break
				}
			}
			if subset {
				maximal = false
				break
			}
		}
		if maximal {
			result = append(result, match)
		}
	}
	return result
}

func eventDepth(poset *gorapide.Poset, event *gorapide.Event, depths map[gorapide.EventID]uint64) uint64 {
	depth := uint64(1)
	for _, cause := range poset.DirectCauses(event.ID) {
		if candidate := depths[cause.ID] + 1; candidate > depth {
			depth = candidate
		}
	}
	return depth
}

// ReplayDeterministic reruns a journal and verifies the complete artifact
// digest against a previously recorded value.
func (a *Architecture) ReplayDeterministic(journal ExecutionJournal, expectedArtifactDigest string) (*ExecutionResult, error) {
	prepared, err := a.PrepareDeterministic()
	if err != nil {
		return nil, err
	}
	return prepared.ReplayDeterministic(journal, expectedArtifactDigest)
}

func normalizeJournal(journal ExecutionJournal) (ExecutionJournal, error) {
	if journal.Format != ExecutionJournalFormat {
		return ExecutionJournal{}, fmt.Errorf("%w: format %q", ErrInvalidExecutionJournal, journal.Format)
	}
	if journal.Profile != CompatibilityProfile {
		return ExecutionJournal{}, fmt.Errorf("%w: unsupported profile %q", ErrInvalidExecutionJournal, journal.Profile)
	}
	if journal.ModelDigest == "" {
		return ExecutionJournal{}, fmt.Errorf("%w: model_digest is empty", ErrInvalidExecutionJournal)
	}
	if journal.Limits.MaxFirings == 0 || journal.Limits.MaxStatements == 0 ||
		journal.Limits.MaxConsistentCuts == 0 || journal.Limits.MaxOptionalCutOccurrences == 0 {
		return ExecutionJournal{}, fmt.Errorf("%w: all execution limits must be greater than zero", ErrInvalidExecutionJournal)
	}

	normalized := journal
	normalized.Inputs = make([]InputEvent, len(journal.Inputs))
	normalized.Choices = make([]ChoiceDecision, len(journal.Choices))
	normalized.ClockAdvances = make([]ClockAdvanceDirective, len(journal.ClockAdvances))
	for index, directive := range journal.ClockAdvances {
		if directive.Clock == "" || directive.To == 0 {
			return ExecutionJournal{}, fmt.Errorf("%w: %w: clock advance %d has empty clock or zero target",
				ErrInvalidExecutionJournal, ErrClockAdvanceDirective, index)
		}
		normalized.ClockAdvances[index] = directive
	}
	seenChoices := make(map[string]bool, len(journal.Choices))
	for i, decision := range journal.Choices {
		if decision.Point == "" || decision.Selection == "" {
			return ExecutionJournal{}, fmt.Errorf("%w: choice %d has an empty point or selection", ErrInvalidExecutionJournal, i)
		}
		if seenChoices[decision.Point] {
			return ExecutionJournal{}, fmt.Errorf("%w: duplicate choice point %q", ErrInvalidExecutionJournal, decision.Point)
		}
		seenChoices[decision.Point] = true
		normalized.Choices[i] = decision
	}
	sort.Slice(normalized.Choices, func(i, j int) bool {
		return normalized.Choices[i].Point < normalized.Choices[j].Point
	})
	seen := make(map[string]bool, len(journal.Inputs))
	for i, input := range journal.Inputs {
		if input.Key == "" || input.Source == "" || input.Action == "" {
			return ExecutionJournal{}, fmt.Errorf("%w: input %d has an empty key, source, or action", ErrInvalidExecutionJournal, i)
		}
		if seen[input.Key] {
			return ExecutionJournal{}, fmt.Errorf("%w: duplicate input key %q", ErrInvalidExecutionJournal, input.Key)
		}
		seen[input.Key] = true
		params, err := gorapide.CanonicalizeParams(input.Params)
		if err != nil {
			return ExecutionJournal{}, fmt.Errorf("%w: input %q: %v", ErrInvalidExecutionJournal, input.Key, err)
		}
		timings, err := gorapide.CanonicalizeEventTimings(input.Timings)
		if err != nil {
			return ExecutionJournal{}, fmt.Errorf("%w: input %q timing: %v", ErrInvalidExecutionJournal, input.Key, err)
		}
		causes := append([]string(nil), input.Causes...)
		sort.Strings(causes)
		write := 0
		for _, cause := range causes {
			if cause == "" || cause == input.Key {
				return ExecutionJournal{}, fmt.Errorf("%w: input %q has empty or self cause %q", ErrInvalidExecutionJournal, input.Key, cause)
			}
			if write > 0 && causes[write-1] == cause {
				continue
			}
			causes[write] = cause
			write++
		}
		normalized.Inputs[i] = InputEvent{
			Key: input.Key, Source: input.Source, Action: input.Action,
			Params: params, Causes: causes[:write], Timings: timings,
		}
	}
	sort.Slice(normalized.Inputs, func(i, j int) bool { return normalized.Inputs[i].Key < normalized.Inputs[j].Key })
	for _, input := range normalized.Inputs {
		for _, cause := range input.Causes {
			if !seen[cause] {
				return ExecutionJournal{}, fmt.Errorf("%w: input %q references missing cause %q", ErrInvalidExecutionJournal, input.Key, cause)
			}
		}
	}
	return normalized, nil
}

func buildInputEvents(
	journal ExecutionJournal,
	model *deterministicModel,
	poset *gorapide.Poset,
	clocks *deterministicClockKernel,
	architectureStarts map[string]*gorapide.Event,
	componentStarts map[string]*gorapide.Event,
	existingDepths map[gorapide.EventID]uint64,
) (map[string]*gorapide.Event, map[gorapide.EventID]uint64, error) {
	if architectureStarts[ArchitectureInterfaceID] == nil {
		return nil, nil, fmt.Errorf("root architecture Start event is nil")
	}
	byKey := make(map[string]InputEvent, len(journal.Inputs))
	inDegree := make(map[string]int, len(journal.Inputs))
	children := make(map[string][]string, len(journal.Inputs))
	for _, input := range journal.Inputs {
		if err := validateInputEvent(model, input); err != nil {
			return nil, nil, err
		}
		byKey[input.Key] = input
		inDegree[input.Key] = len(input.Causes)
		for _, cause := range input.Causes {
			children[cause] = append(children[cause], input.Key)
		}
	}
	for key := range children {
		sort.Strings(children[key])
	}
	ready := make([]string, 0)
	for key, degree := range inDegree {
		if degree == 0 {
			ready = append(ready, key)
		}
	}
	sort.Strings(ready)

	events := make(map[string]*gorapide.Event, len(journal.Inputs))
	inputDepths := make(map[gorapide.EventID]uint64, len(journal.Inputs))
	for len(ready) > 0 {
		key := ready[0]
		ready = ready[1:]
		input := byKey[key]
		causeIDs := make([]gorapide.EventID, 0, len(input.Causes)+1)
		causeEvents := make([]*gorapide.Event, 0, len(input.Causes)+1)
		depth := uint64(1)
		if len(input.Causes) == 0 {
			if componentStart := componentStarts[input.Source]; componentStart != nil {
				causeIDs = append(causeIDs, componentStart.ID)
				causeEvents = append(causeEvents, componentStart)
				depth = existingDepths[componentStart.ID] + 1
			} else {
				owner := ArchitectureInterfaceID
				if input.Source != ArchitectureInterfaceID {
					if _, isArchitectureBoundary := model.architectureInstances[input.Source]; isArchitectureBoundary {
						owner = input.Source
					} else {
						owner = model.componentArchitectures[input.Source]
						if owner == "" {
							owner = ArchitectureInterfaceID
						}
					}
				}
				architectureStart := architectureStarts[owner]
				if architectureStart == nil {
					return nil, nil, fmt.Errorf("input %q source %q has unavailable architecture Start %q",
						input.Key, input.Source, owner)
				}
				causeIDs = append(causeIDs, architectureStart.ID)
				causeEvents = append(causeEvents, architectureStart)
				depth = existingDepths[architectureStart.ID] + 1
			}
		}
		for _, causeKey := range input.Causes {
			cause := events[causeKey]
			causeIDs = append(causeIDs, cause.ID)
			causeEvents = append(causeEvents, cause)
			if candidate := inputDepths[cause.ID] + 1; candidate > depth {
				depth = candidate
			}
		}
		if len(input.Causes) != 0 {
			if componentStart := componentStarts[input.Source]; componentStart != nil {
				covered := false
				for _, cause := range causeEvents {
					if cause.ID == componentStart.ID || poset.IsCausallyBefore(componentStart.ID, cause.ID) {
						covered = true
						break
					}
				}
				if !covered {
					causeIDs = append(causeIDs, componentStart.ID)
					causeEvents = append(causeEvents, componentStart)
					if candidate := existingDepths[componentStart.ID] + 1; candidate > depth {
						depth = candidate
					}
				}
			}
		}
		timings, err := clocks.completeInputTimings(input.Source, input.Timings, causeEvents...)
		if err != nil {
			return nil, nil, fmt.Errorf("input %q timing: %w", input.Key, err)
		}
		input.Timings = timings
		event, err := gorapide.NewDeterministicEvent(gorapide.EventProvenance{
			Profile: journal.Profile, Model: model.digest, Instance: input.Source,
			Action: input.Action, Occurrence: "input:" + input.Key, Causes: causeIDs, Timings: input.Timings,
		}, input.Params)
		if err != nil {
			return nil, nil, fmt.Errorf("input %q: %w", key, err)
		}
		if err := poset.AddEventWithCause(event, causeIDs...); err != nil {
			return nil, nil, fmt.Errorf("input %q: %w", key, err)
		}
		events[key] = event
		inputDepths[event.ID] = depth
		for _, child := range children[key] {
			inDegree[child]--
			if inDegree[child] == 0 {
				ready = append(ready, child)
			}
		}
		sort.Strings(ready)
	}
	if len(events) != len(journal.Inputs) {
		return nil, nil, fmt.Errorf("%w: input cause graph contains a cycle", ErrInvalidExecutionJournal)
	}
	return events, inputDepths, nil
}

type connectionMatchCandidate struct {
	match     pattern.MatchResult
	canonical pattern.CanonicalMatch
	key       string
}

func connectionVisibleEvents(
	connection *Connection,
	observed gorapide.EventSet,
	model *deterministicModel,
	components map[string]*Component,
) gorapide.EventSet {
	result := make(gorapide.EventSet, 0, len(observed))
	for _, event := range observed {
		if event == nil {
			continue
		}
		if connection.Scope == ArchitectureConnectionScope && connection.From != "*" && event.Source != connection.From {
			continue
		}
		if connection.Scope == ModuleConnectionScope && !pattern.HasModuleSourceBinding(connection.Trigger) && event.Source != connection.From {
			continue
		}
		contextQualifiedModuleSource := connection.Scope == ModuleConnectionScope &&
			pattern.HasModuleSourceBinding(connection.Trigger)
		if !contextQualifiedModuleSource && !connectionEventSourceVisible(connection, event.Source, model) {
			continue
		}
		component := components[event.Source]
		if connection.Scope == ArchitectureConnectionScope {
			if !architectureConnectionSourceMatches(connection, model, event) {
				continue
			}
		} else if !interfaceMatchesModuleAction(component, event.Name, event.Params) {
			continue
		}
		result = append(result, event.Snapshot())
	}
	return result
}

// architectureConstraintView exposes the root architecture's own components
// and immediate child boundaries while hiding child internals and module-
// private actions. Causality between every remaining occurrence is inherited
// transitively from the complete audit poset.
func architectureConstraintView(model *deterministicModel, poset *gorapide.Poset) (pattern.PosetReader, error) {
	return architectureConstraintViewForOwner(model, poset, ArchitectureInterfaceID)
}

func architectureConstraintViewForOwner(
	model *deterministicModel,
	poset *gorapide.Poset,
	owner string,
) (pattern.PosetReader, error) {
	visible := make(gorapide.EventSet, 0)
	hasHidden := false
	startPoset := gorapide.NewPoset()
	startDepths := make(map[gorapide.EventID]uint64)
	_, starts, err := createArchitectureStartEvents(
		CompatibilityProfile, model, startPoset, startDepths,
	)
	if err != nil {
		return nil, err
	}
	startIDs := make(map[gorapide.EventID]bool, len(starts))
	for _, start := range starts {
		startIDs[start.ID] = true
	}
	for _, event := range poset.All() {
		if startIDs[event.ID] {
			// Start is an architecture-system occurrence, not a constituent of the
			// returned interface's public constraint alphabet in this source slice.
			hasHidden = true
			continue
		}
		for _, view := range event.ObservationViews() {
			if !architectureConstraintSourceVisible(model, owner, view.Source) {
				hasHidden = true
				continue
			}
			component := model.components[view.Source]
			private := interfaceMatchesAction(component, view.Name, PrivateAction, view.Params)
			public := interfaceMatchesAction(component, view.Name, InAction, view.Params) ||
				interfaceMatchesAction(component, view.Name, OutAction, view.Params)
			if private && !public {
				hasHidden = true
				continue
			}
			visible = append(visible, view)
		}
	}
	if !hasHidden {
		return poset, nil
	}
	projection, err := pattern.NewProjection(poset, visible)
	if err != nil {
		return nil, fmt.Errorf("architecture %q constraint visibility: %w", owner, err)
	}
	return projection, nil
}

func architectureConstraintSourceVisible(model *deterministicModel, owner, source string) bool {
	if model == nil {
		return false
	}
	if source == architectureBoundaryID(owner) {
		return true
	}
	if componentOwner, exists := model.componentArchitectures[source]; exists {
		return componentOwner == owner
	}
	if declaration, exists := model.architectureInstances[source]; exists {
		return declaration.Parent == owner
	}
	return false
}

// moduleConstraintView exposes exactly the qualified observations available to
// one generated component instance. Private actions are included; observations
// belonging only to surrounding or peer modules are hidden while their causal
// effect between local endpoints is retained transitively.
func moduleConstraintView(componentID string, poset *gorapide.Poset) (pattern.PosetReader, error) {
	visible := make(gorapide.EventSet, 0)
	for _, event := range poset.All() {
		for _, view := range event.ObservationViews() {
			if view.Source == componentID {
				visible = append(visible, view)
			}
		}
	}
	projection, err := pattern.NewProjection(poset, visible)
	if err != nil {
		return nil, fmt.Errorf("module constraint visibility for %q: %w", componentID, err)
	}
	return projection, nil
}

func canonicalConnectionMatches(matches []pattern.MatchResult, activation gorapide.EventID) ([]connectionMatchCandidate, error) {
	result := make([]connectionMatchCandidate, 0, len(matches))
	seen := make(map[string]bool, len(matches))
	for _, match := range matches {
		containsActivation := false
		for _, event := range match.Events {
			if event.ID == activation {
				containsActivation = true
				break
			}
		}
		if !containsActivation {
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
		key := string(encoded)
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, connectionMatchCandidate{match: match, canonical: canonical, key: key})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].key < result[j].key })
	return result, nil
}

func deterministicTargets(connection *Connection, current *gorapide.Event, model *deterministicModel) []string {
	if connection.To != "*" {
		return []string{connection.To}
	}
	targets := connectionTargetIDs(
		connectionArchitectureID(connection), model.componentIDs,
		model.architectureInstances, model.architectureInstanceIDs, model.componentArchitectures,
	)
	result := make([]string, 0, len(targets))
	for _, id := range targets {
		if id != current.Source {
			result = append(result, id)
		}
	}
	return result
}

func validateInputEvent(model *deterministicModel, input InputEvent) error {
	for parameter, value := range input.Params {
		encoded, err := gorapide.EncodeCanonicalValue(value)
		if err != nil {
			return fmt.Errorf("%w: input %q parameter %q: %v", ErrInvalidExecutionJournal, input.Key, parameter, err)
		}
		if canonicalValueContainsModule(encoded) {
			return fmt.Errorf("%w: input %q parameter %q contains a module allocation identity; module values must originate from registered Rapide execution",
				ErrInvalidExecutionJournal, input.Key, parameter)
		}
	}
	iface := deterministicEndpointInterface(model, input.Source)
	if iface == nil {
		return fmt.Errorf("%w: input source %q is missing", ErrInvalidExecutionJournal, input.Source)
	}
	kind := OutAction
	if input.Source == ArchitectureInterfaceID {
		kind = InAction
	}
	if !interfaceDeclMatchesAction(iface, input.Action, kind, input.Params) {
		return fmt.Errorf("%w: %w: input action %s.%s", ErrInvalidExecutionJournal,
			ErrActionTypeMismatch, input.Source, input.Action)
	}
	return nil
}

func canonicalValueContainsModule(value gorapide.CanonicalValue) bool {
	if value.Kind == "module" || value.Kind == "record" {
		return true
	}
	for _, item := range value.Items {
		if canonicalValueContainsModule(item) {
			return true
		}
	}
	for _, field := range value.Fields {
		if canonicalValueContainsModule(field.Value) {
			return true
		}
	}
	return false
}

func validateConnectionTargetAction(
	model *deterministicModel,
	components map[string]*Component,
	connection *Connection,
	targetID, action string,
	params map[string]any,
) error {
	iface := endpointInterface(targetID, model.returnInterface, model.architectureInstances, components)
	if iface == nil {
		return fmt.Errorf("target %q or its interface is missing", targetID)
	}
	kind := InAction
	direction := "in"
	if connection.Scope == ModuleConnectionScope {
		if interfaceDeclMatchesAction(iface, action, OutAction, params) ||
			interfaceDeclMatchesAction(iface, action, PrivateAction, params) {
			return nil
		}
		return fmt.Errorf("%w: action %s.%s does not match a declared out-action or private-action signature",
			ErrActionTypeMismatch, targetID, action)
	}
	if targetID == architectureBoundaryID(connectionArchitectureID(connection)) {
		kind = OutAction
		direction = "out"
	}
	if !interfaceDeclMatchesAction(iface, action, kind, params) {
		return fmt.Errorf("%w: action %s.%s does not match a declared %s-action signature",
			ErrActionTypeMismatch, targetID, action, direction)
	}
	return nil
}

func endpointInterface(
	id string,
	returnInterface *InterfaceDecl,
	architectureInstances map[string]ArchitectureInstanceDeclaration,
	components map[string]*Component,
) *InterfaceDecl {
	if id == ArchitectureInterfaceID {
		return returnInterface
	}
	if declaration, exists := architectureInstances[id]; exists {
		return declaration.ReturnInterface
	}
	component := components[id]
	if component == nil {
		return nil
	}
	return component.Interface
}

func deterministicEndpointInterface(model *deterministicModel, id string) *InterfaceDecl {
	if model == nil {
		return nil
	}
	return endpointInterface(id, model.returnInterface, model.architectureInstances, model.components)
}

func architectureConnectionSourceMatches(connection *Connection, model *deterministicModel, event *gorapide.Event) bool {
	if connection == nil || model == nil || event == nil ||
		!connectionEventSourceVisible(connection, event.Source, model) {
		return false
	}
	kind := OutAction
	if event.Source == architectureBoundaryID(connectionArchitectureID(connection)) {
		kind = InAction
	}
	return interfaceDeclMatchesAction(
		deterministicEndpointInterface(model, event.Source),
		event.Name, kind, event.Params,
	)
}

func connectionArchitectureID(connection *Connection) string {
	if connection == nil || connection.ArchitectureInstance == "" {
		return ArchitectureInterfaceID
	}
	return connection.ArchitectureInstance
}

func architectureBoundaryID(architectureID string) string {
	if architectureID == "" || architectureID == ArchitectureInterfaceID {
		return ArchitectureInterfaceID
	}
	return architectureID
}

func connectionEndpointVisible(
	architectureID string,
	endpointID string,
	architectureInstances map[string]ArchitectureInstanceDeclaration,
	componentArchitectures map[string]string,
) bool {
	if endpointID == architectureBoundaryID(architectureID) {
		return true
	}
	if owner, exists := componentArchitectures[endpointID]; exists {
		return owner == architectureID
	}
	if declaration, exists := architectureInstances[endpointID]; exists {
		return declaration.Parent == architectureID
	}
	return false
}

func connectionEventSourceVisible(connection *Connection, sourceID string, model *deterministicModel) bool {
	if connection == nil || model == nil {
		return false
	}
	if connection.Scope == ModuleConnectionScope && connection.From == sourceID && connection.To == sourceID {
		return true
	}
	return connectionEndpointVisible(
		connectionArchitectureID(connection), sourceID,
		model.architectureInstances, model.componentArchitectures,
	)
}

func connectionTargetIDs(
	architectureID string,
	componentIDs []string,
	architectureInstances map[string]ArchitectureInstanceDeclaration,
	architectureInstanceIDs []string,
	componentArchitectures map[string]string,
) []string {
	result := make([]string, 0, len(componentIDs)+len(architectureInstanceIDs))
	for _, id := range componentIDs {
		if componentArchitectures[id] == architectureID {
			result = append(result, id)
		}
	}
	for _, id := range architectureInstanceIDs {
		if architectureInstances[id].Parent == architectureID {
			result = append(result, id)
		}
	}
	sort.Strings(result)
	return result
}

func interfaceMatchesGeneratedAction(component *Component, name string, params map[string]any) bool {
	return interfaceMatchesAction(component, name, OutAction, params) ||
		interfaceMatchesAction(component, name, PrivateAction, params)
}

func interfaceMatchesModuleAction(component *Component, name string, params map[string]any) bool {
	return interfaceMatchesAction(component, name, InAction, params) ||
		interfaceMatchesGeneratedAction(component, name, params)
}

func interfaceMatchesAction(component *Component, name string, kind ActionKind, params map[string]any) bool {
	if component == nil || component.Interface == nil {
		return false
	}
	return interfaceDeclMatchesAction(component.Interface, name, kind, params)
}

func interfaceDeclMatchesAction(iface *InterfaceDecl, name string, kind ActionKind, params map[string]any) bool {
	if iface == nil {
		return false
	}
	for _, action := range iface.Actions {
		if action.Name == name && action.Kind == kind && actionParamsMatch(action, params) {
			return true
		}
	}
	for _, service := range iface.Services {
		for _, action := range service.Actions {
			if action.Name == name && action.Kind == kind && actionParamsMatch(action, params) {
				return true
			}
		}
	}
	return false
}

func actionParamsMatch(action ActionDecl, params map[string]any) bool {
	if len(action.Params) != len(params) {
		return false
	}
	for _, declaration := range action.Params {
		value, ok := params[declaration.Name]
		if !ok || !valueMatchesParameter(value, declaration) {
			return false
		}
	}
	return true
}

func supportedPredefinedType(name string) bool {
	return gorapide.IsSupportedPredefinedType(name)
}

func valueMatchesPredefinedType(value any, name string) bool {
	return gorapide.CanonicalValueMatchesPredefinedType(value, name)
}

func valueMatchesParameter(value any, declaration ParamDecl) bool {
	if structuralType, structural := declaration.StructuralRapideType(); structural {
		_, err := gorapide.NewRapideObjectDenotation("parameter", structuralType, value)
		return err == nil
	}
	return valueMatchesPredefinedType(value, declaration.Type)
}

type executionItem struct {
	event *gorapide.Event
	depth uint64
}

type executionQueue []*executionItem

func (queue executionQueue) Len() int { return len(queue) }
func (queue executionQueue) Less(i, j int) bool {
	if queue[i].depth != queue[j].depth {
		return queue[i].depth < queue[j].depth
	}
	return executionItemKey(queue[i].event) < executionItemKey(queue[j].event)
}
func (queue executionQueue) Swap(i, j int)   { queue[i], queue[j] = queue[j], queue[i] }
func (queue *executionQueue) Push(value any) { *queue = append(*queue, value.(*executionItem)) }
func (queue *executionQueue) Pop() any {
	old := *queue
	last := old[len(old)-1]
	*queue = old[:len(old)-1]
	return last
}

func executionItemKey(event *gorapide.Event) string {
	return string(event.ID) + "\x00" + event.Source + "\x00" + event.Name
}

func observationRankKey(event *gorapide.Event) string {
	return event.Source + "\x00" + string(event.ID)
}

func enqueueExecutionItem(queue *executionQueue, seen map[string]bool, event *gorapide.Event, depth uint64) {
	key := executionItemKey(event)
	if seen[key] {
		return
	}
	seen[key] = true
	heap.Push(queue, &executionItem{event: event, depth: depth})
}

func popReadyExecutionItem(
	queue *executionQueue,
	poset *gorapide.Poset,
	observed map[gorapide.EventID]bool,
	choices *choiceResolver,
) (*executionItem, error) {
	return popReadyExecutionItemDomain(queue, poset, observed, choices, "event-observation")
}

func popReadyExecutionItemDomain(
	queue *executionQueue,
	poset *gorapide.Poset,
	observed map[gorapide.EventID]bool,
	choices *choiceResolver,
	domain string,
) (*executionItem, error) {
	// A basic connection adds a qualified view of the same event occurrence.
	// Once any view of an occurrence has been observed, close all currently
	// ready views of that occurrence before selecting a new occurrence. This
	// preserves Stanford's basic-connection identity and orderly-observation
	// rule: a causally later occurrence cannot overtake a pending alias of its
	// causal predecessor merely because content-derived event IDs sort that way.
	closeObservedOccurrence := false
	for _, item := range *queue {
		if executionItemReady(item, poset, observed) && observed[item.event.ID] {
			closeObservedOccurrence = true
			break
		}
	}
	options := make([]string, 0, queue.Len())
	indices := make(map[string]int, queue.Len())
	for index, item := range *queue {
		if !executionItemReady(item, poset, observed) {
			continue
		}
		if closeObservedOccurrence && !observed[item.event.ID] {
			continue
		}
		option := executionItemKey(item.event)
		options = append(options, option)
		indices[option] = index
	}
	if len(options) == 0 {
		return nil, fmt.Errorf("deterministic execution queue has no causally ready observation")
	}
	if choices == nil {
		return nil, fmt.Errorf("deterministic execution queue has no semantic choice resolver")
	}
	selected, err := choices.resolve(domain, options)
	if err != nil {
		return nil, err
	}
	item, ok := heap.Remove(queue, indices[selected]).(*executionItem)
	if !ok || item == nil {
		return nil, fmt.Errorf("deterministic execution queue lost selected observation %q", selected)
	}
	return item, nil
}

func hasReadyExecutionItem(
	queue *executionQueue,
	poset *gorapide.Poset,
	observed map[gorapide.EventID]bool,
) bool {
	for _, item := range *queue {
		if executionItemReady(item, poset, observed) {
			return true
		}
	}
	return false
}

func executionItemReady(
	item *executionItem,
	poset *gorapide.Poset,
	observed map[gorapide.EventID]bool,
) bool {
	if item == nil || item.event == nil {
		return false
	}
	for _, cause := range poset.DirectCauses(item.event.ID) {
		if !observed[cause.ID] {
			return false
		}
	}
	return true
}

func digestBytes(data []byte) string {
	digest := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(digest[:])
}

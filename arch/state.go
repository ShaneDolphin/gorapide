package arch

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/pattern"
)

var ErrInvalidStateReference = errors.New("invalid declarative Rapide state reference")

// StateDeclaration is one module-owned reference object in the initial closed
// predefined-type subset. Initial values are part of canonical model identity.
type StateDeclaration struct {
	Name    string
	Type    string
	Initial any
}

// StateReference declares a module state reference and its initial value.
func StateReference(name, typeName string, initial any) StateDeclaration {
	return StateDeclaration{Name: name, Type: typeName, Initial: initial}
}

// DeclareState registers module-owned reference objects. Declarations are
// canonicalized by name, so call and slice order have no semantic meaning.
func (component *Component) DeclareState(declarations ...StateDeclaration) error {
	if component == nil {
		return fmt.Errorf("%w: component is nil", ErrInvalidStateReference)
	}
	component.mu.Lock()
	component.stateDeclarations = append(component.stateDeclarations, declarations...)
	component.mu.Unlock()
	return nil
}

// StateAssignment is one sequential reference assignment executed before the
// event generator in a closed transition body.
type StateAssignment struct {
	Target string
	Value  RuleValue
}

// AssignState constructs one closed state assignment.
func AssignState(target string, value RuleValue) StateAssignment {
	return StateAssignment{Target: target, Value: copyRuleValue(value)}
}

type canonicalStateDeclaration struct {
	Name    string                  `json:"name"`
	Type    string                  `json:"type"`
	Initial gorapide.CanonicalValue `json:"initial"`
}

type canonicalStateAssignment struct {
	Target                  string                                  `json:"target"`
	Kind                    RuleValueKind                           `json:"kind"`
	Placeholder             string                                  `json:"placeholder,omitempty"`
	State                   string                                  `json:"state,omitempty"`
	Type                    string                                  `json:"type,omitempty"`
	GeneratorArguments      []canonicalArchitectureArgument         `json:"generator_arguments,omitempty"`
	InitializationArguments []canonicalModuleInitializationArgument `json:"initialization_arguments,omitempty"`
	Literal                 *gorapide.CanonicalValue                `json:"literal,omitempty"`
	Operator                RuleValueOperator                       `json:"operator,omitempty"`
	Operands                []canonicalRuleValue                    `json:"operands,omitempty"`
}

// StateReadRecord identifies the exact reference version used by a firing.
type StateReadRecord struct {
	ComponentID string                  `json:"component_id"`
	Name        string                  `json:"name"`
	OperationID string                  `json:"operation_id"`
	Version     uint64                  `json:"version"`
	Value       gorapide.CanonicalValue `json:"value"`
	Causes      []string                `json:"causes"`
	operation   stateOperationReference
}

// StateWriteRecord records a new reference version and its causal provenance.
type StateWriteRecord struct {
	ComponentID string                  `json:"component_id"`
	Name        string                  `json:"name"`
	OperationID string                  `json:"operation_id"`
	Version     uint64                  `json:"version"`
	Value       gorapide.CanonicalValue `json:"value"`
	Causes      []string                `json:"causes"`
	operation   stateOperationReference
}

// StateOperationKind identifies one operation in the semantic sequential order
// required for a single Rapide Ref object.
type StateOperationKind string

const (
	StateOperationCreate      StateOperationKind = "create"
	StateOperationDereference StateOperationKind = "dereference"
	StateOperationAssign      StateOperationKind = "assign"
)

// StateOperationRecord is one canonical Ref operation occurrence. Sequence and
// Predecessor establish the per-Ref semantic order. A dereference additionally
// identifies the creation or assignment operation that supplied its value.
type StateOperationRecord struct {
	ID            string                  `json:"id"`
	ComponentID   string                  `json:"component_id"`
	Name          string                  `json:"name"`
	Sequence      uint64                  `json:"sequence"`
	Kind          StateOperationKind      `json:"kind"`
	Version       uint64                  `json:"version"`
	Value         gorapide.CanonicalValue `json:"value"`
	Predecessor   string                  `json:"predecessor,omitempty"`
	ValueSource   string                  `json:"value_source,omitempty"`
	Owner         string                  `json:"owner"`
	Causes        []string                `json:"causes"`
	Dependencies  []string                `json:"dependencies"`
	Successors    []string                `json:"successors"`
	evaluationRun uint64
}

type stateOperationReference struct {
	id      string
	history *stateReferenceHistory
}

type stateReferenceHistory struct {
	componentID     string
	name            string
	nextEvaluation  uint64
	nextDereference uint64
	operations      []StateOperationRecord
	operationIndex  map[string]int
	sources         map[uint64]string
}

func qualifyStateReads(componentID string, records []StateReadRecord) []StateReadRecord {
	for index := range records {
		if records[index].ComponentID == "" {
			records[index].ComponentID = componentID
		}
	}
	return records
}

func qualifyStateWrites(componentID string, records []StateWriteRecord) []StateWriteRecord {
	for index := range records {
		if records[index].ComponentID == "" {
			records[index].ComponentID = componentID
		}
	}
	return records
}

// StateRecord is the canonical final value and provenance of one module state
// reference after deterministic execution.
type StateRecord struct {
	ComponentID string                  `json:"component_id"`
	Name        string                  `json:"name"`
	Type        string                  `json:"type"`
	Version     uint64                  `json:"version"`
	Value       gorapide.CanonicalValue `json:"value"`
	Causes      []string                `json:"causes"`
}

type stateCell struct {
	declaration StateDeclaration
	value       any
	version     uint64
	causes      []gorapide.EventID
	history     *stateReferenceHistory
}

type moduleStateRuntime map[string]map[string]*stateCell

func newStateReferenceHistory(componentID, name string, initial any) (*stateReferenceHistory, error) {
	encoded, err := gorapide.CanonicalizeParameters(map[string]any{"value": initial})
	if err != nil {
		return nil, err
	}
	history := &stateReferenceHistory{
		componentID: componentID, name: name, sources: make(map[uint64]string),
		operationIndex: make(map[string]int),
	}
	id, err := stateOperationID(componentID, name, StateOperationCreate, 0, 0)
	if err != nil {
		return nil, err
	}
	history.sources[0] = id
	history.operations = append(history.operations, StateOperationRecord{
		ID: id, ComponentID: componentID, Name: name, Kind: StateOperationCreate,
		Version: 0, Value: encoded[0].Value, Owner: "elaboration", Causes: []string{},
	})
	history.operationIndex[id] = 0
	return history, nil
}

func stateOperationID(componentID, name string, kind StateOperationKind, version, ordinal uint64) (string, error) {
	descriptor := struct {
		Format      string             `json:"format"`
		ComponentID string             `json:"component_id"`
		Name        string             `json:"name"`
		Kind        StateOperationKind `json:"kind"`
		Version     uint64             `json:"version"`
		Ordinal     uint64             `json:"ordinal"`
	}{
		Format: "gorapide.ref-operation-id.v1", ComponentID: componentID, Name: name,
		Kind: kind, Version: version, Ordinal: ordinal,
	}
	encoded, err := json.Marshal(descriptor)
	if err != nil {
		return "", fmt.Errorf("%w: Ref operation identity: %v", ErrInvalidStateReference, err)
	}
	return "refop1-" + digestBytes(encoded), nil
}

func (history *stateReferenceHistory) recordDereference(
	owner string,
	version uint64,
	value gorapide.CanonicalValue,
	causes []string,
) (string, error) {
	if history == nil {
		return "", fmt.Errorf("%w: dereference has no Ref history", ErrInvalidStateReference)
	}
	source := history.sources[version]
	if source == "" {
		return "", fmt.Errorf("%w: Ref %s.%s has no value source for version %d", ErrInvalidStateReference, history.componentID, history.name, version)
	}
	if history.nextEvaluation == ^uint64(0) || history.nextDereference == ^uint64(0) {
		return "", fmt.Errorf("%w: Ref %s.%s exhausted the operation identity domain", ErrInvalidStateReference, history.componentID, history.name)
	}
	history.nextEvaluation++
	history.nextDereference++
	id, err := stateOperationID(history.componentID, history.name, StateOperationDereference, version, history.nextDereference)
	if err != nil {
		return "", err
	}
	history.operations = append(history.operations, StateOperationRecord{
		ID: id, ComponentID: history.componentID, Name: history.name,
		Kind: StateOperationDereference, Version: version, Value: value,
		ValueSource: source, Owner: owner, Causes: append([]string(nil), causes...),
		evaluationRun: history.nextEvaluation,
	})
	history.operationIndex[id] = len(history.operations) - 1
	return id, nil
}

func (history *stateReferenceHistory) recordAssignment(
	owner string,
	version uint64,
	value gorapide.CanonicalValue,
	causes []string,
) (string, error) {
	if history == nil {
		return "", fmt.Errorf("%w: assignment has no Ref history", ErrInvalidStateReference)
	}
	if history.sources[version] != "" || history.sources[version-1] == "" {
		return "", fmt.Errorf("%w: Ref %s.%s assignment version %d is not sequential", ErrInvalidStateReference, history.componentID, history.name, version)
	}
	if history.nextEvaluation == ^uint64(0) {
		return "", fmt.Errorf("%w: Ref %s.%s exhausted the operation identity domain", ErrInvalidStateReference, history.componentID, history.name)
	}
	history.nextEvaluation++
	id, err := stateOperationID(history.componentID, history.name, StateOperationAssign, version, 0)
	if err != nil {
		return "", err
	}
	history.sources[version] = id
	history.operations = append(history.operations, StateOperationRecord{
		ID: id, ComponentID: history.componentID, Name: history.name,
		Kind: StateOperationAssign, Version: version, Value: value,
		Owner: owner, Causes: append([]string(nil), causes...),
		evaluationRun: history.nextEvaluation,
	})
	history.operationIndex[id] = len(history.operations) - 1
	return id, nil
}

func (history *stateReferenceHistory) addOperationDependencies(id string, dependencies ...string) error {
	if history == nil {
		return fmt.Errorf("%w: Ref operation %q has no history", ErrInvalidStateReference, id)
	}
	index, exists := history.operationIndex[id]
	if !exists || index < 0 || index >= len(history.operations) {
		return fmt.Errorf("%w: Ref operation %q is unavailable for dependency linkage", ErrInvalidStateReference, id)
	}
	history.operations[index].Dependencies = canonicalStrings(append(history.operations[index].Dependencies, dependencies...))
	return nil
}

func (history *stateReferenceHistory) addOperationSuccessors(id string, successors ...string) error {
	if history == nil {
		return fmt.Errorf("%w: Ref operation %q has no history", ErrInvalidStateReference, id)
	}
	index, exists := history.operationIndex[id]
	if !exists || index < 0 || index >= len(history.operations) {
		return fmt.Errorf("%w: Ref operation %q is unavailable for successor linkage", ErrInvalidStateReference, id)
	}
	history.operations[index].Successors = canonicalStrings(append(history.operations[index].Successors, successors...))
	return nil
}

func stateOperationReferences(reads []StateReadRecord, writes []StateWriteRecord) []stateOperationReference {
	result := make([]stateOperationReference, 0, len(reads)+len(writes))
	for _, read := range reads {
		result = append(result, read.operation)
	}
	for _, write := range writes {
		result = append(result, write.operation)
	}
	return canonicalStateOperationReferences(result)
}

func stateOperationReferenceIDs(values []stateOperationReference) []string {
	canonical := canonicalStateOperationReferences(values)
	result := make([]string, 0, len(canonical))
	for _, value := range canonical {
		result = append(result, value.id)
	}
	return result
}

func canonicalStateOperationReferences(values []stateOperationReference) []stateOperationReference {
	byID := make(map[string]stateOperationReference, len(values))
	for _, value := range values {
		if value.id != "" && value.history != nil {
			byID[value.id] = value
		}
	}
	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	result := make([]stateOperationReference, 0, len(ids))
	for _, id := range ids {
		result = append(result, byID[id])
	}
	return result
}

func addStateOperationDependencies(operations []stateOperationReference, dependencies ...string) error {
	dependencies = canonicalStrings(dependencies)
	for _, operation := range canonicalStateOperationReferences(operations) {
		if err := operation.history.addOperationDependencies(operation.id, dependencies...); err != nil {
			return err
		}
	}
	return nil
}

func addStateOperationSuccessors(operations []stateOperationReference, successors ...string) error {
	successors = canonicalStrings(successors)
	for _, operation := range canonicalStateOperationReferences(operations) {
		if err := operation.history.addOperationSuccessors(operation.id, successors...); err != nil {
			return err
		}
	}
	return nil
}

func (history *stateReferenceHistory) canonicalOperations() ([]StateOperationRecord, error) {
	if history == nil || len(history.operations) == 0 {
		return nil, fmt.Errorf("%w: Ref history is empty", ErrInvalidStateReference)
	}
	result := append([]StateOperationRecord(nil), history.operations...)
	sort.Slice(result, func(i, j int) bool {
		if result[i].Version != result[j].Version {
			return result[i].Version < result[j].Version
		}
		if result[i].Kind != result[j].Kind {
			return result[i].Kind != StateOperationDereference
		}
		if result[i].evaluationRun != result[j].evaluationRun {
			return result[i].evaluationRun < result[j].evaluationRun
		}
		return result[i].ID < result[j].ID
	})
	seen := make(map[string]bool, len(result))
	for index := range result {
		if result[index].ID == "" || seen[result[index].ID] || result[index].Owner == "" ||
			result[index].ComponentID != history.componentID || result[index].Name != history.name {
			return nil, fmt.Errorf("%w: Ref %s.%s has duplicate or empty operation identity", ErrInvalidStateReference, history.componentID, history.name)
		}
		seen[result[index].ID] = true
		result[index].Sequence = uint64(index + 1)
		if index > 0 {
			result[index].Predecessor = result[index-1].ID
		}
		if result[index].Kind == StateOperationDereference {
			if result[index].ValueSource != history.sources[result[index].Version] || !seen[result[index].ValueSource] {
				return nil, fmt.Errorf("%w: Ref %s.%s dereference precedes its value source", ErrInvalidStateReference, history.componentID, history.name)
			}
		} else if result[index].ValueSource != "" {
			return nil, fmt.Errorf("%w: Ref %s.%s non-dereference has a value source", ErrInvalidStateReference, history.componentID, history.name)
		}
		result[index].Causes = append([]string{}, canonicalStrings(result[index].Causes)...)
		result[index].Dependencies = append([]string{}, canonicalStrings(result[index].Dependencies)...)
		result[index].Successors = append([]string{}, canonicalStrings(result[index].Successors)...)
		for _, dependency := range result[index].Dependencies {
			if dependency == result[index].ID {
				return nil, fmt.Errorf("%w: Ref operation %q depends on itself", ErrInvalidStateReference, result[index].ID)
			}
		}
		for _, successor := range result[index].Successors {
			if successor == result[index].ID {
				return nil, fmt.Errorf("%w: Ref operation %q precedes itself", ErrInvalidStateReference, result[index].ID)
			}
		}
		result[index].evaluationRun = 0
	}
	return result, nil
}

// stateSnapshotRegistry stores module state at semantic event generation. The
// key includes the observing component, so a basic connection's target
// observation can carry that module's generation-time state.
type stateSnapshotRegistry map[string]map[string]*stateCell

func canonicalizeStateDeclarations(declarations []StateDeclaration) ([]StateDeclaration, []canonicalStateDeclaration, map[string]string, error) {
	normalized := make([]StateDeclaration, 0, len(declarations))
	canonical := make([]canonicalStateDeclaration, 0, len(declarations))
	types := make(map[string]string, len(declarations))
	for _, declaration := range declarations {
		if declaration.Name == "" || declaration.Type == "" {
			return nil, nil, nil, fmt.Errorf("%w: state declaration has an empty name or type", ErrInvalidStateReference)
		}
		if _, duplicate := types[declaration.Name]; duplicate {
			return nil, nil, nil, fmt.Errorf("%w: duplicate state reference %q", ErrInvalidStateReference, declaration.Name)
		}
		if !supportedPredefinedType(declaration.Type) {
			return nil, nil, nil, fmt.Errorf("%w: state reference %q has unsupported type %q", ErrInvalidStateReference, declaration.Name, declaration.Type)
		}
		values, err := gorapide.CanonicalizeParams(map[string]any{"value": declaration.Initial})
		if err != nil || !valueMatchesPredefinedType(values["value"], declaration.Type) {
			return nil, nil, nil, fmt.Errorf("%w: initial value of %q does not match %s", ErrInvalidStateReference, declaration.Name, declaration.Type)
		}
		encoded, err := gorapide.CanonicalizeParameters(values)
		if err != nil {
			return nil, nil, nil, err
		}
		normalized = append(normalized, StateDeclaration{
			Name: declaration.Name, Type: declaration.Type, Initial: values["value"],
		})
		canonical = append(canonical, canonicalStateDeclaration{
			Name: declaration.Name, Type: declaration.Type, Initial: encoded[0].Value,
		})
		types[declaration.Name] = declaration.Type
	}
	sort.Slice(normalized, func(i, j int) bool { return normalized[i].Name < normalized[j].Name })
	sort.Slice(canonical, func(i, j int) bool { return canonical[i].Name < canonical[j].Name })
	return normalized, canonical, types, nil
}

func canonicalizeStateAssignments(owner string, assignments []StateAssignment, stateTypes, placeholderTypes map[string]string) ([]StateAssignment, []canonicalStateAssignment, error) {
	normalized := make([]StateAssignment, 0, len(assignments))
	canonical := make([]canonicalStateAssignment, 0, len(assignments))
	for index, assignment := range assignments {
		targetType, ok := stateTypes[assignment.Target]
		if assignment.Target == "" || !ok {
			return nil, nil, fmt.Errorf("%w: %s assignment %d targets undeclared state %q", ErrInvalidStateReference, owner, index, assignment.Target)
		}
		normalizedValue, encodedValue, expressionType, err := canonicalizeStateRuleValue(owner, assignment.Value, stateTypes, placeholderTypes)
		if err != nil {
			return nil, nil, err
		}
		if normalizedValue.kind == RuleLiteralValue && !valueMatchesPredefinedType(normalizedValue.literal, targetType) {
			return nil, nil, fmt.Errorf("%w: %s assignment to %q does not match %s", ErrInvalidStateReference, owner, assignment.Target, targetType)
		}
		if normalizedValue.kind != RuleLiteralValue && expressionType != "" &&
			!ruleValueAssignableToPredefined(normalizedValue, expressionType, targetType) {
			return nil, nil, fmt.Errorf("%w: %s assignment to %q has type %s, want %s", ErrInvalidStateReference, owner, assignment.Target, expressionType, targetType)
		}
		normalized = append(normalized, StateAssignment{Target: assignment.Target, Value: normalizedValue})
		encodedValue.Target = assignment.Target
		canonical = append(canonical, encodedValue)
	}
	return normalized, canonical, nil
}

func canonicalizeStateRuleValue(owner string, value RuleValue, stateTypes, placeholderTypes map[string]string) (RuleValue, canonicalStateAssignment, string, error) {
	normalized, expression, typeName, err := canonicalizeClosedRuleValue(owner, value, stateTypes, placeholderTypes)
	if err != nil {
		return RuleValue{}, canonicalStateAssignment{}, "", err
	}
	return normalized, canonicalStateAssignment{
		Kind: expression.Kind, Placeholder: expression.Placeholder, State: expression.State,
		Type: expression.Type, GeneratorArguments: expression.GeneratorArguments,
		InitializationArguments: expression.InitializationArguments,
		Literal:                 expression.Literal, Operator: expression.Operator, Operands: expression.Operands,
	}, typeName, nil
}

func predefinedTypeOfValue(value any) string {
	switch value.(type) {
	case gorapide.RapideTriv:
		return "Triv"
	case bool:
		return "Boolean"
	case int64:
		return "Integer"
	case float64:
		return "Float"
	case gorapide.RapideCharacter:
		return "Character"
	case gorapide.RapideString:
		return "String"
	case string:
		return "String"
	default:
		return ""
	}
}

func predefinedTypeAssignable(source, target string) bool {
	if source == target {
		return true
	}
	switch target {
	case "Integer":
		return source == "Natural" || source == "Positive"
	case "Natural":
		return source == "Positive"
	default:
		return false
	}
}

func ruleValueAssignableToPredefined(value RuleValue, expressionType, target string) bool {
	if predefinedTypeAssignable(expressionType, target) {
		return true
	}
	evaluated, _, err := EvaluateConstant(value)
	return err == nil && valueMatchesPredefinedType(evaluated, target)
}

func initializeModuleState(model *deterministicModel) (moduleStateRuntime, error) {
	result := make(moduleStateRuntime, len(model.stateDeclarations))
	for componentID, declarations := range model.stateDeclarations {
		cells := make(map[string]*stateCell, len(declarations))
		for _, declaration := range declarations {
			values, err := gorapide.CanonicalizeParams(map[string]any{"value": declaration.Initial})
			if err != nil {
				return nil, err
			}
			history, err := newStateReferenceHistory(componentID, declaration.Name, values["value"])
			if err != nil {
				return nil, err
			}
			cells[declaration.Name] = &stateCell{
				declaration: declaration, value: values["value"], history: history,
			}
		}
		result[componentID] = cells
	}
	return result, nil
}

func (registry stateSnapshotRegistry) capture(event *gorapide.Event, cells map[string]*stateCell) error {
	if event == nil {
		return fmt.Errorf("%w: state snapshot event is nil", ErrInvalidStateReference)
	}
	return registry.captureFor(event.Source, event, cells)
}

func (registry stateSnapshotRegistry) captureFor(
	componentID string,
	event *gorapide.Event,
	cells map[string]*stateCell,
) error {
	if registry == nil || event == nil {
		return fmt.Errorf("%w: state snapshot registry or event is nil", ErrInvalidStateReference)
	}
	key := componentID + "\x00" + observationRankKey(event)
	if _, exists := registry[key]; exists {
		return nil
	}
	snapshot, err := cloneStateCellsChecked(cells)
	if err != nil {
		return err
	}
	registry[key] = snapshot
	return nil
}

func cloneStateCells(cells map[string]*stateCell) map[string]*stateCell {
	snapshot, _ := cloneStateCellsChecked(cells)
	return snapshot
}

func cloneStateCellsChecked(cells map[string]*stateCell) (map[string]*stateCell, error) {
	snapshot := make(map[string]*stateCell, len(cells))
	for name, cell := range cells {
		if cell == nil {
			return nil, fmt.Errorf("%w: state snapshot contains nil cell %q", ErrInvalidStateReference, name)
		}
		values, err := gorapide.CanonicalizeParams(map[string]any{"value": cell.value})
		if err != nil {
			return nil, err
		}
		copy := *cell
		copy.value = values["value"]
		copy.causes = append([]gorapide.EventID(nil), cell.causes...)
		snapshot[name] = &copy
	}
	return snapshot, nil
}

func (registry stateSnapshotRegistry) forMatch(
	componentID string,
	match pattern.MatchResult,
	observationRanks map[string]uint64,
	current map[string]*stateCell,
) (map[string]*stateCell, error) {
	if len(match.Events) == 0 {
		return current, nil
	}
	var last *gorapide.Event
	var lastRank uint64
	for _, event := range match.Events {
		rank, ok := observationRanks[observationRankKey(event)]
		if !ok {
			return nil, fmt.Errorf("%w: match event %s has no observation rank", ErrInvalidStateReference, event.ID)
		}
		if last == nil || rank > lastRank || (rank == lastRank && executionItemKey(event) > executionItemKey(last)) {
			last, lastRank = event, rank
		}
	}
	snapshot := registry[componentID+"\x00"+observationRankKey(last)]
	if snapshot == nil {
		return nil, fmt.Errorf("%w: component %s has no generation-time state for event %s", ErrInvalidStateReference, componentID, last.ID)
	}
	return snapshot, nil
}

func evaluateMatchGuard(
	owner, componentID string,
	guard *RuleValue,
	match pattern.MatchResult,
	observationRanks map[string]uint64,
	registry stateSnapshotRegistry,
	current map[string]*stateCell,
	priorOperations []stateOperationReference,
) (bool, []StateReadRecord, []gorapide.EventID, error) {
	if guard == nil {
		return true, nil, nil, nil
	}
	snapshot, err := registry.forMatch(componentID, match, observationRanks, current)
	if err != nil {
		return false, nil, nil, err
	}
	evaluated, err := evaluateClosedRuleValue(owner+" guard", *guard, match.Bindings, snapshot)
	if err != nil {
		return false, nil, nil, err
	}
	result, ok := evaluated.value.(bool)
	if !ok {
		return false, nil, nil, fmt.Errorf("%w: %s guard evaluated to %T, want Boolean", ErrInvalidStateReference, owner, evaluated.value)
	}
	matchEvents := make([]string, 0, len(match.Events))
	for _, event := range match.Events {
		matchEvents = append(matchEvents, string(event.ID))
	}
	matchEvents = append(matchEvents, stateOperationReferenceIDs(priorOperations)...)
	if err := addStateOperationDependencies(stateOperationReferences(evaluated.reads, nil), matchEvents...); err != nil {
		return false, nil, nil, err
	}
	return result, evaluated.reads, evaluated.causes, nil
}

func evaluateRuleValue(owner string, value RuleValue, bindings pattern.Bindings, cells map[string]*stateCell) (any, []gorapide.EventID, []StateReadRecord, error) {
	evaluated, err := evaluateClosedRuleValue(owner, value, bindings, cells)
	return evaluated.value, evaluated.causes, evaluated.reads, err
}

func applyStateAssignments(
	owner string,
	assignments []StateAssignment,
	bindings pattern.Bindings,
	cells map[string]*stateCell,
	baseCauses []gorapide.EventID,
	priorOperations []stateOperationReference,
) ([]StateReadRecord, []StateWriteRecord, error) {
	reads := make([]StateReadRecord, 0)
	writes := make([]StateWriteRecord, 0, len(assignments))
	priorOperations = canonicalStateOperationReferences(priorOperations)
	for _, assignment := range assignments {
		cell := cells[assignment.Target]
		if cell == nil {
			return nil, nil, fmt.Errorf("%w: %s writes missing state %q", ErrInvalidStateReference, owner, assignment.Target)
		}
		value, readCauses, expressionReads, err := evaluateRuleValue(owner, assignment.Value, bindings, cells)
		if err != nil {
			return nil, nil, err
		}
		readOperations := stateOperationReferences(expressionReads, nil)
		dependencies := append(eventIDStrings(baseCauses), stateOperationReferenceIDs(priorOperations)...)
		if err := addStateOperationDependencies(readOperations, dependencies...); err != nil {
			return nil, nil, err
		}
		if !valueMatchesPredefinedType(value, cell.declaration.Type) {
			return nil, nil, fmt.Errorf("%w: %s assignment to %q does not match %s", ErrInvalidStateReference, owner, assignment.Target, cell.declaration.Type)
		}
		reads = append(reads, expressionReads...)
		causes := canonicalEventIDs(append(append([]gorapide.EventID(nil), baseCauses...), readCauses...))
		encoded, err := gorapide.CanonicalizeParameters(map[string]any{"value": value})
		if err != nil {
			return nil, nil, err
		}
		causeStrings := eventIDStrings(causes)
		nextVersion := cell.version + 1
		if nextVersion == 0 {
			return nil, nil, fmt.Errorf("%w: %s assignment to %q exhausted the Ref version domain", ErrInvalidStateReference, owner, assignment.Target)
		}
		operationID, err := cell.history.recordAssignment(owner, nextVersion, encoded[0].Value, causeStrings)
		if err != nil {
			return nil, nil, err
		}
		cell.value = value
		cell.version = nextVersion
		cell.causes = causes
		write := StateWriteRecord{
			Name: assignment.Target, OperationID: operationID, Version: cell.version,
			Value: encoded[0].Value, Causes: causeStrings,
			operation: stateOperationReference{id: operationID, history: cell.history},
		}
		writeOperations := stateOperationReferences(nil, []StateWriteRecord{write})
		dependencies = append(dependencies, stateOperationReferenceIDs(readOperations)...)
		if err := addStateOperationDependencies(writeOperations, dependencies...); err != nil {
			return nil, nil, err
		}
		writes = append(writes, write)
		priorOperations = canonicalStateOperationReferences(append(append(priorOperations, readOperations...), writeOperations...))
	}
	return reads, writes, nil
}

func canonicalStateRecords(states moduleStateRuntime) ([]StateRecord, error) {
	result := make([]StateRecord, 0)
	for componentID, cells := range states {
		for name, cell := range cells {
			encoded, err := gorapide.CanonicalizeParameters(map[string]any{"value": cell.value})
			if err != nil {
				return nil, err
			}
			result = append(result, StateRecord{
				ComponentID: componentID, Name: name, Type: cell.declaration.Type,
				Version: cell.version, Value: encoded[0].Value, Causes: eventIDStrings(cell.causes),
			})
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].ComponentID != result[j].ComponentID {
			return result[i].ComponentID < result[j].ComponentID
		}
		return result[i].Name < result[j].Name
	})
	return result, nil
}

func canonicalStateOperationRecords(states moduleStateRuntime) ([]StateOperationRecord, error) {
	result := make([]StateOperationRecord, 0)
	for _, cells := range states {
		for _, cell := range cells {
			if cell == nil || cell.history == nil {
				return nil, fmt.Errorf("%w: state cell has no Ref history", ErrInvalidStateReference)
			}
			operations, err := cell.history.canonicalOperations()
			if err != nil {
				return nil, err
			}
			result = append(result, operations...)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].ComponentID != result[j].ComponentID {
			return result[i].ComponentID < result[j].ComponentID
		}
		if result[i].Name != result[j].Name {
			return result[i].Name < result[j].Name
		}
		return result[i].Sequence < result[j].Sequence
	})
	return result, nil
}

func canonicalEventIDs(ids []gorapide.EventID) []gorapide.EventID {
	result := append([]gorapide.EventID(nil), ids...)
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	write := 0
	for _, id := range result {
		if id == "" || (write > 0 && result[write-1] == id) {
			continue
		}
		result[write] = id
		write++
	}
	return result[:write]
}

func eventIDStrings(ids []gorapide.EventID) []string {
	canonical := canonicalEventIDs(ids)
	result := make([]string, len(canonical))
	for i, id := range canonical {
		result[i] = string(id)
	}
	return result
}

package arch

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/constraint"
	"github.com/ShaneDolphin/gorapide/pattern"
)

const (
	BehaviorConformanceModelFormat = "gorapide.behavior-conformance-model.v1"

	// These bounds are semantic profile constants and are included in every
	// behavior-conformance model digest. The supported source subset is also
	// statically acyclic, so reaching either limit is an explicit engine error.
	DefaultBehaviorConformanceMaxFirings    uint64 = 65536
	DefaultBehaviorConformanceMaxEmbeddings uint64 = 100000
)

var (
	ErrInvalidBehaviorConformance = errors.New("invalid deterministic behavior-conformance constraint")
	ErrBehaviorConformanceLimit   = errors.New("deterministic behavior-conformance limit reached")
)

const (
	behaviorEnvironmentComponent = "environment"
	behaviorSubjectComponent     = "subject"
)

type behaviorConformanceAction struct {
	Name      string `json:"name"`
	Qualified string `json:"qualified"`
	Input     bool   `json:"input"`
}

type canonicalBehaviorConformanceModel struct {
	Format        string                      `json:"format"`
	Name          string                      `json:"name"`
	ServicePath   string                      `json:"service_path"`
	Specification string                      `json:"specification_model_digest"`
	Actions       []behaviorConformanceAction `json:"actions"`
	MaxFirings    uint64                      `json:"max_firings"`
	MaxEmbeddings uint64                      `json:"max_embeddings"`
}

// BehaviorConformanceConstraint implements the closed behavior-conformance
// subset: the behavior is executed as an isolated shadow specification, and
// its interface poset must embed injectively in the actual subject-visible
// poset with the same causal relation on the common events. The shadow never
// emits into or mutates the production computation.
type BehaviorConformanceConstraint struct {
	Name          string
	ServicePath   string
	Specification string
	Actions       []behaviorConformanceAction
	MaxFirings    uint64
	MaxEmbeddings uint64

	specification         *Architecture
	preparedSpecification *PreparedArchitecture
}

// NewBehaviorConformanceConstraint builds a closed shadow architecture from
// one behavior-bearing interface. The supplied interface must contain only the
// actions visible to that behavior; functions and stateful effects are handled
// by later compatibility slices.
func NewBehaviorConformanceConstraint(
	name string,
	servicePath string,
	iface *InterfaceDecl,
	rules []*DeclarativeRule,
	semanticSpecification string,
) (*BehaviorConformanceConstraint, error) {
	return newBehaviorConformanceConstraint(
		name, servicePath, iface, rules, semanticSpecification, true,
	)
}

// NewComponentBehaviorConformanceConstraint builds the same isolated shadow
// specification for one module-denoted interface object. The surrounding
// module-constraint projection already scopes observations to componentPath,
// so direct action names remain unqualified inside that projection.
func NewComponentBehaviorConformanceConstraint(
	name string,
	componentPath string,
	iface *InterfaceDecl,
	rules []*DeclarativeRule,
	semanticSpecification string,
) (*BehaviorConformanceConstraint, error) {
	return newBehaviorConformanceConstraint(
		name, componentPath, iface, rules, semanticSpecification, false,
	)
}

func newBehaviorConformanceConstraint(
	name string,
	subjectPath string,
	iface *InterfaceDecl,
	rules []*DeclarativeRule,
	semanticSpecification string,
	qualifyActions bool,
) (*BehaviorConformanceConstraint, error) {
	name = strings.TrimSpace(name)
	subjectPath = strings.ToLower(strings.TrimSpace(subjectPath))
	semanticSpecification = strings.TrimSpace(semanticSpecification)
	if name == "" || subjectPath == "" || semanticSpecification == "" || iface == nil || iface.Name == "" {
		return nil, fmt.Errorf("%w: name, subject path, and interface are required", ErrInvalidBehaviorConformance)
	}
	if len(iface.Functions) != 0 || len(iface.Services) != 0 {
		return nil, fmt.Errorf("%w: shadow interface must contain direct actions only", ErrInvalidBehaviorConformance)
	}

	subjectInterface := &InterfaceDecl{
		Name:    iface.Name,
		Actions: cloneBehaviorConformanceActions(iface.Actions),
	}
	environmentBuilder := Interface("BehaviorEnvironment")
	actions := make([]behaviorConformanceAction, 0, len(subjectInterface.Actions))
	seen := make(map[string]bool, len(subjectInterface.Actions))
	for _, action := range subjectInterface.Actions {
		key := strings.ToLower(action.Name)
		if key == "" || seen[key] {
			return nil, fmt.Errorf("%w: duplicate or empty action %q", ErrInvalidBehaviorConformance, action.Name)
		}
		seen[key] = true
		qualified := key
		if qualifyActions {
			qualified = subjectPath + "." + key
		}
		entry := behaviorConformanceAction{Name: action.Name, Qualified: qualified, Input: action.Kind == InAction}
		actions = append(actions, entry)
		if entry.Input {
			environmentBuilder.OutAction(action.Name, append([]ParamDecl(nil), action.Params...)...)
		}
	}
	sort.Slice(actions, func(i, j int) bool { return actions[i].Qualified < actions[j].Qualified })

	specification := NewArchitecture("behavior-conformance:" + subjectPath)
	environment := NewComponent(behaviorEnvironmentComponent, environmentBuilder.Build(), nil)
	subject := NewComponent(behaviorSubjectComponent, subjectInterface, nil)
	for _, rule := range rules {
		if err := subject.AddDeclarativeRule(rule); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidBehaviorConformance, err)
		}
	}
	if err := specification.AddComponent(environment); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidBehaviorConformance, err)
	}
	if err := specification.AddComponent(subject); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidBehaviorConformance, err)
	}
	for _, action := range actions {
		if !action.Input {
			continue
		}
		connection := Connect(behaviorEnvironmentComponent, behaviorSubjectComponent).
			IdentifiedBy("behavior-input:" + strings.ToLower(action.Name)).
			On(pattern.MatchEvent(action.Name)).
			Send(action.Name).
			Build()
		if err := specification.AddConnection(connection); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidBehaviorConformance, err)
		}
	}
	result := &BehaviorConformanceConstraint{
		Name: name, ServicePath: subjectPath, Specification: semanticSpecification, Actions: actions,
		MaxFirings:    DefaultBehaviorConformanceMaxFirings,
		MaxEmbeddings: DefaultBehaviorConformanceMaxEmbeddings,
		specification: specification,
	}
	if _, err := result.DeterministicDigest(); err != nil {
		return nil, err
	}
	return result, nil
}

func cloneBehaviorConformanceActions(source []ActionDecl) []ActionDecl {
	result := make([]ActionDecl, len(source))
	for index, action := range source {
		result[index] = ActionDecl{
			Name: action.Name, Kind: action.Kind,
			Params: append([]ParamDecl(nil), action.Params...),
		}
	}
	return result
}

func (current *BehaviorConformanceConstraint) CanonicalName() string {
	if current == nil {
		return ""
	}
	return current.Name
}

func (current *BehaviorConformanceConstraint) canonicalModel() (canonicalBehaviorConformanceModel, error) {
	if current == nil || current.Name == "" || current.ServicePath == "" || current.Specification == "" ||
		(current.specification == nil && current.preparedSpecification == nil) ||
		current.MaxFirings == 0 || current.MaxEmbeddings == 0 {
		return canonicalBehaviorConformanceModel{}, fmt.Errorf("%w: incomplete declaration", ErrInvalidBehaviorConformance)
	}
	if current.preparedSpecification != nil {
		if _, err := current.preparedSpecification.DeterministicModelDigest(); err != nil {
			return canonicalBehaviorConformanceModel{}, fmt.Errorf("%w: shadow specification: %v", ErrInvalidBehaviorConformance, err)
		}
	} else if _, err := current.specification.DeterministicModelDigest(); err != nil {
		return canonicalBehaviorConformanceModel{}, fmt.Errorf("%w: shadow specification: %v", ErrInvalidBehaviorConformance, err)
	}
	actions := append([]behaviorConformanceAction(nil), current.Actions...)
	for index := range actions {
		actions[index].Name = strings.ToLower(actions[index].Name)
		actions[index].Qualified = strings.ToLower(actions[index].Qualified)
	}
	sort.Slice(actions, func(i, j int) bool { return actions[i].Qualified < actions[j].Qualified })
	previous := ""
	for _, action := range actions {
		if action.Name == "" || action.Qualified == "" || (previous != "" && action.Qualified == previous) {
			return canonicalBehaviorConformanceModel{}, fmt.Errorf("%w: malformed action map", ErrInvalidBehaviorConformance)
		}
		previous = action.Qualified
	}
	return canonicalBehaviorConformanceModel{
		Format: BehaviorConformanceModelFormat, Name: current.Name,
		ServicePath: current.ServicePath, Specification: current.Specification,
		Actions: actions, MaxFirings: current.MaxFirings,
		MaxEmbeddings: current.MaxEmbeddings,
	}, nil
}

func (current *BehaviorConformanceConstraint) DeterministicDigest() (string, error) {
	model, err := current.canonicalModel()
	if err != nil {
		return "", err
	}
	encoded, err := json.Marshal(model)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func (current *BehaviorConformanceConstraint) Check(poset *gorapide.Poset) []constraint.ConstraintViolation {
	violations, err := current.evaluate(poset)
	if err == nil {
		return violations
	}
	return []constraint.ConstraintViolation{{
		Constraint: current.CanonicalName(), Clause: "evaluation", Kind: constraint.MustMatch,
		Message: err.Error(), Severity: "error",
	}}
}

func (current *BehaviorConformanceConstraint) EvaluateCanonical(
	poset pattern.PosetReader,
) (constraint.CanonicalConstraintReport, error) {
	digest, err := current.DeterministicDigest()
	if err != nil {
		return constraint.CanonicalConstraintReport{}, err
	}
	violations, err := current.evaluate(poset)
	if err != nil {
		return constraint.CanonicalConstraintReport{}, err
	}
	return constraint.CanonicalReportFromViolations(digest, poset, violations)
}

func (current *BehaviorConformanceConstraint) evaluate(
	actual pattern.PosetReader,
) ([]constraint.ConstraintViolation, error) {
	if actual == nil {
		return nil, fmt.Errorf("%w: evaluation poset is nil", ErrInvalidBehaviorConformance)
	}
	model, err := current.canonicalModel()
	if err != nil {
		return nil, err
	}
	inputs, err := current.shadowInputs(actual)
	if err != nil {
		return nil, err
	}
	var runtimeSpecificationDigest string
	if current.preparedSpecification != nil {
		runtimeSpecificationDigest, err = current.preparedSpecification.DeterministicModelDigest()
	} else {
		runtimeSpecificationDigest, err = current.specification.DeterministicModelDigest()
	}
	if err != nil {
		return nil, fmt.Errorf("%w: shadow specification: %v", ErrInvalidBehaviorConformance, err)
	}
	journal := NewExecutionJournal(runtimeSpecificationDigest, current.MaxFirings, inputs...)
	var expectedResult *ExecutionResult
	if current.preparedSpecification != nil {
		expectedResult, err = current.preparedSpecification.ExecuteDeterministic(journal)
	} else {
		expectedResult, err = current.specification.ExecuteDeterministic(journal)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: shadow execution: %v", ErrInvalidBehaviorConformance, err)
	}
	expected, err := moduleConstraintView(behaviorSubjectComponent, expectedResult.Poset)
	if err != nil {
		return nil, fmt.Errorf("%w: shadow projection: %v", ErrInvalidBehaviorConformance, err)
	}
	embeds, relevant, err := current.embeds(expected, actual)
	if err != nil {
		return nil, err
	}
	if embeds {
		return nil, nil
	}
	return []constraint.ConstraintViolation{{
		Constraint: current.Name, Clause: "super-poset", Kind: constraint.MustMatch,
		Message: fmt.Sprintf(
			"%s %s visible computation is not a causal-order-preserving super-poset of behavior computation %s",
			current.behaviorSubjectKind(), current.ServicePath, model.Specification,
		),
		MatchedEvents: relevant, Severity: "error",
	}}, nil
}

func (current *BehaviorConformanceConstraint) behaviorSubjectKind() string {
	for _, action := range current.Actions {
		if strings.ToLower(action.Qualified) != strings.ToLower(action.Name) {
			return "service"
		}
	}
	return "component"
}

func cloneBehaviorConformanceConstraint(
	source *BehaviorConformanceConstraint,
) (*BehaviorConformanceConstraint, error) {
	if source == nil {
		return nil, fmt.Errorf("%w: constraint is nil", ErrInvalidBehaviorConformance)
	}
	before, err := source.DeterministicDigest()
	if err != nil {
		return nil, err
	}
	var prepared *PreparedArchitecture
	if source.preparedSpecification != nil {
		model, modelErr := source.preparedSpecification.checkedModel()
		if modelErr != nil {
			return nil, modelErr
		}
		prepared = &PreparedArchitecture{model: model}
	} else {
		prepared, err = source.specification.PrepareDeterministic()
		if err != nil {
			return nil, err
		}
	}
	result := &BehaviorConformanceConstraint{
		Name: source.Name, ServicePath: source.ServicePath, Specification: source.Specification,
		Actions:    append([]behaviorConformanceAction(nil), source.Actions...),
		MaxFirings: source.MaxFirings, MaxEmbeddings: source.MaxEmbeddings,
		preparedSpecification: prepared,
	}
	after, err := result.DeterministicDigest()
	if err != nil {
		return nil, err
	}
	if after != before {
		return nil, fmt.Errorf("%w: constraint %q clone changed digest", ErrInvalidBehaviorConformance, source.Name)
	}
	return result, nil
}

func (current *BehaviorConformanceConstraint) shadowInputs(actual pattern.PosetReader) ([]InputEvent, error) {
	type selectedInput struct {
		event  *gorapide.Event
		action behaviorConformanceAction
		key    string
	}
	inputByQualified := make(map[string]behaviorConformanceAction)
	for _, action := range current.Actions {
		if action.Input {
			inputByQualified[strings.ToLower(action.Qualified)] = action
		}
	}
	selected := make([]selectedInput, 0)
	seenOccurrences := make(map[gorapide.EventID]string)
	for _, event := range actual.All() {
		action, ok := inputByQualified[strings.ToLower(event.Name)]
		if !ok {
			continue
		}
		if previous, exists := seenOccurrences[event.ID]; exists {
			return nil, fmt.Errorf(
				"%w: occurrence %s is visible as multiple service inputs %q and %q",
				ErrInvalidBehaviorConformance, event.ID, previous, event.Name,
			)
		}
		seenOccurrences[event.ID] = event.Name
		selected = append(selected, selectedInput{
			event: event, action: action,
			key: string(event.ID),
		})
	}
	sort.Slice(selected, func(i, j int) bool { return selected[i].key < selected[j].key })
	result := make([]InputEvent, 0, len(selected))
	for _, input := range selected {
		causes := make([]string, 0)
		for _, possibleCause := range selected {
			if actual.IsCausallyBefore(possibleCause.event.ID, input.event.ID) {
				causes = append(causes, possibleCause.key)
			}
		}
		result = append(result, InputEvent{
			Key: input.key, Source: behaviorEnvironmentComponent,
			Action: input.action.Name, Params: cloneBehaviorConformanceParams(input.event.Params),
			Causes: causes,
		})
	}
	return result, nil
}

func cloneBehaviorConformanceParams(source map[string]any) map[string]any {
	result := make(map[string]any, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

type behaviorEmbeddingEvent struct {
	event      *gorapide.Event
	signature  string
	candidates gorapide.EventSet
}

func (current *BehaviorConformanceConstraint) embeds(
	expected pattern.PosetReader,
	actual pattern.PosetReader,
) (bool, gorapide.EventSet, error) {
	qualifiedByName := make(map[string]string, len(current.Actions))
	relevantNames := make(map[string]bool, len(current.Actions))
	for _, action := range current.Actions {
		qualifiedByName[strings.ToLower(action.Name)] = strings.ToLower(action.Qualified)
		relevantNames[strings.ToLower(action.Qualified)] = true
	}
	relevant := make(gorapide.EventSet, 0)
	actualBySignature := make(map[string]gorapide.EventSet)
	for _, event := range actual.All() {
		if !relevantNames[strings.ToLower(event.Name)] {
			continue
		}
		relevant = append(relevant, event)
		signature, err := behaviorEventSignature(strings.ToLower(event.Name), event.Params)
		if err != nil {
			return false, nil, err
		}
		actualBySignature[signature] = append(actualBySignature[signature], event)
	}
	sort.Slice(relevant, func(i, j int) bool { return string(relevant[i].ID) < string(relevant[j].ID) })
	for signature := range actualBySignature {
		sort.Slice(actualBySignature[signature], func(i, j int) bool {
			return string(actualBySignature[signature][i].ID) < string(actualBySignature[signature][j].ID)
		})
	}

	toEmbed := make([]behaviorEmbeddingEvent, 0, expected.Len())
	for _, event := range expected.All() {
		qualified, ok := qualifiedByName[strings.ToLower(event.Name)]
		if !ok {
			return false, nil, fmt.Errorf(
				"%w: shadow event %q has no service action mapping",
				ErrInvalidBehaviorConformance, event.Name,
			)
		}
		signature, err := behaviorEventSignature(qualified, event.Params)
		if err != nil {
			return false, nil, err
		}
		candidates := actualBySignature[signature]
		if len(candidates) == 0 {
			return false, relevant, nil
		}
		toEmbed = append(toEmbed, behaviorEmbeddingEvent{
			event: event, signature: signature, candidates: candidates,
		})
	}
	sort.Slice(toEmbed, func(i, j int) bool {
		if len(toEmbed[i].candidates) != len(toEmbed[j].candidates) {
			return len(toEmbed[i].candidates) < len(toEmbed[j].candidates)
		}
		if toEmbed[i].signature != toEmbed[j].signature {
			return toEmbed[i].signature < toEmbed[j].signature
		}
		return string(toEmbed[i].event.ID) < string(toEmbed[j].event.ID)
	})

	mapped := make(map[gorapide.EventID]gorapide.EventID, len(toEmbed))
	used := make(map[gorapide.EventID]bool, len(toEmbed))
	var attempts uint64
	var search func(int) (bool, error)
	search = func(index int) (bool, error) {
		if index == len(toEmbed) {
			return true, nil
		}
		currentEvent := toEmbed[index]
		for _, candidate := range currentEvent.candidates {
			attempts++
			if attempts > current.MaxEmbeddings {
				return false, fmt.Errorf(
					"%w: %s %s exceeded %d embedding candidates",
					ErrBehaviorConformanceLimit, current.behaviorSubjectKind(), current.ServicePath, current.MaxEmbeddings,
				)
			}
			if used[candidate.ID] || !preservesBehaviorCausality(expected, actual, currentEvent.event.ID, candidate.ID, mapped) {
				continue
			}
			mapped[currentEvent.event.ID] = candidate.ID
			used[candidate.ID] = true
			found, err := search(index + 1)
			if err != nil || found {
				return found, err
			}
			delete(mapped, currentEvent.event.ID)
			delete(used, candidate.ID)
		}
		return false, nil
	}
	found, err := search(0)
	return found, relevant, err
}

func behaviorEventSignature(name string, params map[string]any) (string, error) {
	canonical, err := gorapide.CanonicalizeParameters(params)
	if err != nil {
		return "", fmt.Errorf("%w: event %q values: %v", ErrInvalidBehaviorConformance, name, err)
	}
	encoded, err := json.Marshal(struct {
		Name   string                        `json:"name"`
		Params []gorapide.CanonicalParameter `json:"params"`
	}{Name: name, Params: canonical})
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func preservesBehaviorCausality(
	expected pattern.PosetReader,
	actual pattern.PosetReader,
	expectedID gorapide.EventID,
	actualID gorapide.EventID,
	mapped map[gorapide.EventID]gorapide.EventID,
) bool {
	for previousExpected, previousActual := range mapped {
		if expected.IsCausallyBefore(previousExpected, expectedID) !=
			actual.IsCausallyBefore(previousActual, actualID) {
			return false
		}
		if expected.IsCausallyBefore(expectedID, previousExpected) !=
			actual.IsCausallyBefore(actualID, previousActual) {
			return false
		}
	}
	return true
}

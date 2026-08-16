package arch

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/constraint"
	"github.com/ShaneDolphin/gorapide/pattern"
)

const (
	EventPatternMapModelFormat    = "gorapide.event-pattern-map-model.v4"
	MapExecutionArtifactFormat    = "gorapide.map-execution-artifact.v4"
	DeterministicMapEngineVersion = "gorapide.deterministic-map-engine.v5"
)

// MapInducedDependencyPolicy selects the published Rapide relation used to
// transfer causality between separate map-rule firings. It is semantic model
// content, not an execution-order preference.
type MapInducedDependencyPolicy string

const (
	NoneInducedDependencyPolicy      MapInducedDependencyPolicy = "rapide-none-induced-dependency"
	StrongInducedDependencyPolicy    MapInducedDependencyPolicy = "rapide-strong-induced-dependency"
	MaximaInducedDependencyPolicy    MapInducedDependencyPolicy = "rapide-maxima-induced-dependency"
	DominanceInducedDependencyPolicy MapInducedDependencyPolicy = "rapide-dominance-induced-dependency"
	OverlookInducedDependencyPolicy  MapInducedDependencyPolicy = "rapide-overlook-induced-dependency"
	DiffInducedDependencyPolicy      MapInducedDependencyPolicy = "rapide-diff-induced-dependency"

	// WeakInducedDependencyPolicy is retained as a source-compatible alias for
	// the old Go API. The v2 implementation actually applied Stanford's strong
	// relation, so the corrected canonical identity names that exact policy.
	WeakInducedDependencyPolicy = StrongInducedDependencyPolicy
)

var (
	ErrInvalidEventPatternMap         = errors.New("invalid deterministic Rapide event-pattern map")
	ErrInvalidPreparedEventPatternMap = errors.New("invalid prepared deterministic Rapide event-pattern map")
	ErrMapExecutionLimit              = errors.New("deterministic map execution limit exceeded")
	ErrMapReplayMismatch              = errors.New("deterministic map replay mismatch")
)

// MapExecutionLimits are explicit semantic execution inputs. The evaluator
// fails before silently truncating either selected firings or range events.
type MapExecutionLimits struct {
	MaxFirings     uint64 `json:"max_firings"`
	MaxRangeEvents uint64 `json:"max_range_events"`
}

// MapExecutionRequest is the complete execution policy input beyond the
// canonical model and exact domain poset. Choices select stable generator
// alternative IDs at choice points discovered during execution.
type MapExecutionRequest struct {
	Limits  MapExecutionLimits `json:"limits"`
	Choices []ChoiceDecision   `json:"choices"`
}

// EventPatternMap is the closed deterministic representation of Rapide's map
// construct. This first source-faithful slice maps one named object/interface
// domain to one range interface. It is deliberately separate from legacy Map,
// whose callbacks implement an adapter rather than Rapide map semantics.
type EventPatternMap struct {
	Name                    string
	DomainID                string
	DomainInterface         *InterfaceDecl
	RangeInterface          *InterfaceDecl
	InducedDependencyPolicy MapInducedDependencyPolicy
	Rules                   []*DeclarativeRule
	RangeConstraints        *constraint.ConstraintSet
}

// EventPatternMapBuilder constructs a closed map without callbacks.
type EventPatternMapBuilder struct {
	declaration EventPatternMap
}

// NewEventPatternMap begins a deterministic Rapide event-pattern map.
func NewEventPatternMap(name string) *EventPatternMapBuilder {
	return &EventPatternMapBuilder{declaration: EventPatternMap{Name: name}}
}

// FromObject declares the one named object/interface domain supported by this
// slice. Only qualified event views performed by domainID are observable.
func (builder *EventPatternMapBuilder) FromObject(domainID string, iface *InterfaceDecl) *EventPatternMapBuilder {
	builder.declaration.DomainID = domainID
	builder.declaration.DomainInterface = iface
	return builder
}

// ToInterface declares the range interface whose action events map rules may
// generate.
func (builder *EventPatternMapBuilder) ToInterface(iface *InterfaceDecl) *EventPatternMapBuilder {
	builder.declaration.RangeInterface = iface
	return builder
}

// WithInducedDependencyPolicy selects one of Stanford Rapide's six published
// induced orderings. An omitted policy defaults to strong, which is the exact
// all-trigger-events-before-all-trigger-events relation used by map v2.
func (builder *EventPatternMapBuilder) WithInducedDependencyPolicy(policy MapInducedDependencyPolicy) *EventPatternMapBuilder {
	builder.declaration.InducedDependencyPolicy = policy
	return builder
}

// AddRule adds an isolated snapshot of one agent mapping rule.
func (builder *EventPatternMapBuilder) AddRule(rule *DeclarativeRule) *EventPatternMapBuilder {
	if rule == nil {
		builder.declaration.Rules = append(builder.declaration.Rules, nil)
		return builder
	}
	copy := *rule
	copy.Guard = copyRuleValuePointer(rule.Guard)
	copy.Body = copyRuleBody(rule.Body)
	builder.declaration.Rules = append(builder.declaration.Rules, &copy)
	return builder
}

// WithRangeConstraints adds the constraints that the generated range poset
// must satisfy. Their deterministic digest becomes part of map model identity.
func (builder *EventPatternMapBuilder) WithRangeConstraints(set *constraint.ConstraintSet) *EventPatternMapBuilder {
	builder.declaration.RangeConstraints = set
	return builder
}

// Build returns an isolated map declaration snapshot.
func (builder *EventPatternMapBuilder) Build() *EventPatternMap {
	if builder == nil {
		return nil
	}
	result := builder.declaration
	result.Rules = copyMapRules(builder.declaration.Rules)
	return &result
}

// MappingRule begins an agent state-transition rule for use in a map. Calling
// Pipe afterward is accepted by the builder but rejected by map validation.
func MappingRule(id string) *DeclarativeRuleBuilder {
	return Rule(id).Agent()
}

type canonicalMapGeneratorAlternative struct {
	ID      string                `json:"id"`
	Outputs []canonicalRuleOutput `json:"outputs"`
}

type canonicalMapRule struct {
	Rule       canonicalDeclarativeRule           `json:"rule"`
	Generators []canonicalMapGeneratorAlternative `json:"generators"`
}

type canonicalEventPatternMap struct {
	Format           string                      `json:"format"`
	Profile          string                      `json:"profile"`
	Name             string                      `json:"name"`
	DomainID         string                      `json:"domain_id"`
	DomainInterface  canonicalInterfaceDecl      `json:"domain_interface"`
	RangeInterface   canonicalInterfaceDecl      `json:"range_interface"`
	DependencyPolicy string                      `json:"dependency_policy"`
	Rules            []canonicalMapRule          `json:"rules"`
	ConstraintSet    *canonicalConstraintSetDecl `json:"range_constraint_set,omitempty"`
}

type mapGeneratorAlternative struct {
	id      string
	outputs []RuleOutput
}

type deterministicEventPatternMap struct {
	name                    string
	domainID                string
	domainInterface         *InterfaceDecl
	rangeInterface          *InterfaceDecl
	inducedDependencyPolicy MapInducedDependencyPolicy
	digest                  string
	canonical               []byte
	rules                   []*DeclarativeRule
	generators              map[string][]mapGeneratorAlternative
	rangeConstraints        *constraint.ConstraintSet
}

// PreparedEventPatternMap is a deeply owned executable snapshot of one
// validated Rapide event-pattern map. It can be executed and replayed
// repeatedly or concurrently without rereading the source declaration.
type PreparedEventPatternMap struct {
	model *deterministicEventPatternMap
}

// PrepareDeterministic validates and seals the map's supported semantic
// content. Callers must not mutate the source map concurrently with this call;
// after it returns, later source mutations cannot change the snapshot.
func (mapping *EventPatternMap) PrepareDeterministic() (*PreparedEventPatternMap, error) {
	model, _, err := mapping.deterministicModel()
	if err != nil {
		return nil, err
	}
	return &PreparedEventPatternMap{model: model}, nil
}

func (prepared *PreparedEventPatternMap) checkedModel() (*deterministicEventPatternMap, error) {
	if prepared == nil || prepared.model == nil || prepared.model.digest == "" || len(prepared.model.canonical) == 0 {
		return nil, fmt.Errorf("%w: snapshot is nil or incomplete", ErrInvalidPreparedEventPatternMap)
	}
	return prepared.model, nil
}

// MarshalCanonicalModel returns an isolated copy of the exact canonical model
// bytes frozen at preparation.
func (prepared *PreparedEventPatternMap) MarshalCanonicalModel() ([]byte, error) {
	model, err := prepared.checkedModel()
	if err != nil {
		return nil, err
	}
	return append([]byte(nil), model.canonical...), nil
}

// DeterministicModelDigest returns the frozen map model identity.
func (prepared *PreparedEventPatternMap) DeterministicModelDigest() (string, error) {
	model, err := prepared.checkedModel()
	if err != nil {
		return "", err
	}
	return model.digest, nil
}

// MarshalCanonicalModel validates the supported map subset and returns its
// canonical model bytes.
func (mapping *EventPatternMap) MarshalCanonicalModel() ([]byte, error) {
	prepared, err := mapping.PrepareDeterministic()
	if err != nil {
		return nil, err
	}
	return prepared.MarshalCanonicalModel()
}

// DeterministicModelDigest returns the semantic identity of the closed map.
func (mapping *EventPatternMap) DeterministicModelDigest() (string, error) {
	prepared, err := mapping.PrepareDeterministic()
	if err != nil {
		return "", err
	}
	return prepared.DeterministicModelDigest()
}

func (mapping *EventPatternMap) deterministicModel() (*deterministicEventPatternMap, canonicalEventPatternMap, error) {
	if mapping == nil || mapping.Name == "" {
		return nil, canonicalEventPatternMap{}, fmt.Errorf("%w: map or map name is empty", ErrInvalidEventPatternMap)
	}
	if mapping.DomainID == "" || mapping.DomainInterface == nil || mapping.RangeInterface == nil {
		return nil, canonicalEventPatternMap{}, fmt.Errorf("%w: domain identity and domain/range interfaces are required", ErrInvalidEventPatternMap)
	}
	if mapping.DomainInterface.Name == "" || mapping.RangeInterface.Name == "" {
		return nil, canonicalEventPatternMap{}, fmt.Errorf("%w: domain and range interface names are required", ErrInvalidEventPatternMap)
	}
	domainSnapshot, err := cloneInterfaceDecl(mapping.DomainInterface)
	if err != nil {
		return nil, canonicalEventPatternMap{}, fmt.Errorf("%w: domain interface: %w", ErrInvalidEventPatternMap, err)
	}
	rangeSnapshot, err := cloneInterfaceDecl(mapping.RangeInterface)
	if err != nil {
		return nil, canonicalEventPatternMap{}, fmt.Errorf("%w: range interface: %w", ErrInvalidEventPatternMap, err)
	}
	domainInterface, err := canonicalizeInterface(domainSnapshot)
	if err != nil {
		return nil, canonicalEventPatternMap{}, fmt.Errorf("%w: domain interface: %w", ErrInvalidEventPatternMap, err)
	}
	rangeInterface, err := canonicalizeInterface(rangeSnapshot)
	if err != nil {
		return nil, canonicalEventPatternMap{}, fmt.Errorf("%w: range interface: %w", ErrInvalidEventPatternMap, err)
	}
	dependencyPolicy, err := normalizeMapInducedDependencyPolicy(mapping.InducedDependencyPolicy)
	if err != nil {
		return nil, canonicalEventPatternMap{}, err
	}
	canonical := canonicalEventPatternMap{
		Format: EventPatternMapModelFormat, Profile: CompatibilityProfile,
		Name: mapping.Name, DomainID: mapping.DomainID,
		DomainInterface: domainInterface, RangeInterface: rangeInterface,
		DependencyPolicy: string(dependencyPolicy),
		Rules:            make([]canonicalMapRule, 0, len(mapping.Rules)),
	}
	rangeConstraints, err := cloneDeterministicConstraintSet(mapping.RangeConstraints)
	if err != nil {
		return nil, canonicalEventPatternMap{}, fmt.Errorf("%w: range constraints: %w", ErrInvalidEventPatternMap, err)
	}
	if rangeConstraints != nil {
		digest, err := rangeConstraints.DeterministicDigest()
		if err != nil {
			return nil, canonicalEventPatternMap{}, fmt.Errorf("%w: range constraints: %v", ErrInvalidEventPatternMap, err)
		}
		canonical.ConstraintSet = &canonicalConstraintSetDecl{Name: rangeConstraints.Name, Digest: digest}
	}

	validationInterface := mapRangeValidationInterface(rangeSnapshot)
	validationComponent := &Component{ID: mapping.Name, Interface: validationInterface}
	rules := make([]*DeclarativeRule, 0, len(mapping.Rules))
	generators := make(map[string][]mapGeneratorAlternative, len(mapping.Rules))
	seenRules := make(map[string]bool, len(mapping.Rules))
	for index, declaration := range mapping.Rules {
		if declaration == nil {
			return nil, canonicalEventPatternMap{}, fmt.Errorf("%w: rule %d is nil", ErrInvalidEventPatternMap, index)
		}
		if declaration.Process != RuleAgentProcess {
			return nil, canonicalEventPatternMap{}, fmt.Errorf("%w: mapping rule %q must use the agent operator ||>", ErrInvalidEventPatternMap, declaration.ID)
		}
		if declaration.ID == "" || seenRules[declaration.ID] {
			return nil, canonicalEventPatternMap{}, fmt.Errorf("%w: empty or duplicate mapping rule identity %q", ErrInvalidEventPatternMap, declaration.ID)
		}
		seenRules[declaration.ID] = true
		if declaration.Trigger == nil {
			return nil, canonicalEventPatternMap{}, fmt.Errorf("%w: mapping rule %q has an unnamed MatchAny trigger", ErrInvalidEventPatternMap, declaration.ID)
		}
		empty, err := pattern.CanMatchEmpty(declaration.Trigger)
		if err != nil {
			return nil, canonicalEventPatternMap{}, fmt.Errorf("%w: mapping rule %q trigger: %v", ErrInvalidEventPatternMap, declaration.ID, err)
		}
		if empty {
			return nil, canonicalEventPatternMap{}, fmt.Errorf("%w: mapping rule %q has a zero-event trigger", ErrInvalidEventPatternMap, declaration.ID)
		}
		if err := validateMapTrigger(mapping.DomainID, domainSnapshot, declaration.Trigger); err != nil {
			return nil, canonicalEventPatternMap{}, fmt.Errorf("%w: mapping rule %q trigger: %v", ErrInvalidEventPatternMap, declaration.ID, err)
		}
		if declaration.Body != nil && (len(declaration.Body.Assignments) != 0 || declaration.Body.Statements != nil) {
			return nil, canonicalEventPatternMap{}, fmt.Errorf("%w: mapping rule %q uses map state or procedural statements outside the current compatibility slice", ErrInvalidEventPatternMap, declaration.ID)
		}
		normalized, encoded, alternatives, err := canonicalizeMapRuleGenerators(validationComponent, declaration)
		if err != nil {
			return nil, canonicalEventPatternMap{}, fmt.Errorf("%w: mapping rule %q: %v", ErrInvalidEventPatternMap, declaration.ID, err)
		}
		rules = append(rules, normalized)
		canonical.Rules = append(canonical.Rules, encoded)
		generators[normalized.ID] = alternatives
	}
	sort.Slice(rules, func(i, j int) bool { return rules[i].ID < rules[j].ID })
	sort.Slice(canonical.Rules, func(i, j int) bool { return canonical.Rules[i].Rule.ID < canonical.Rules[j].Rule.ID })
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return nil, canonicalEventPatternMap{}, err
	}
	model := &deterministicEventPatternMap{
		name: mapping.Name, domainID: mapping.DomainID,
		domainInterface: domainSnapshot, rangeInterface: rangeSnapshot,
		inducedDependencyPolicy: dependencyPolicy,
		digest:                  digestMapBytes(encoded), canonical: append([]byte(nil), encoded...),
		rules: rules, generators: generators, rangeConstraints: rangeConstraints,
	}
	return model, canonical, nil
}

func canonicalizeMapRuleGenerators(
	component *Component,
	declaration *DeclarativeRule,
) (*DeclarativeRule, canonicalMapRule, []mapGeneratorAlternative, error) {
	if declaration == nil || declaration.Body == nil {
		return nil, canonicalMapRule{}, nil, fmt.Errorf("%w: mapping rule has no body", ErrInvalidDeclarativeRule)
	}
	raw := declaration.Body.Alternatives
	if raw == nil {
		raw = [][]RuleOutput{declaration.Body.Outputs}
	} else if len(raw) == 0 {
		return nil, canonicalMapRule{}, nil, fmt.Errorf("%w: mapping rule has an empty generator-alternative set", ErrInvalidDeclarativeRule)
	}
	type candidate struct {
		id        string
		rule      *DeclarativeRule
		canonical canonicalDeclarativeRule
	}
	byID := make(map[string]candidate, len(raw))
	for _, outputs := range raw {
		copy := *declaration
		copy.allowGeneratorCausalEquivalence = true
		copy.Guard = copyRuleValuePointer(declaration.Guard)
		copy.Body = copyRuleBody(declaration.Body)
		copy.Body.Alternatives = nil
		copy.Body.Outputs = make([]RuleOutput, len(outputs))
		for index, output := range outputs {
			copy.Body.Outputs[index] = copyRuleOutput(output)
		}
		normalized, encoded, err := canonicalizeDeclarativeRule(component, &copy, nil, nil)
		if err != nil {
			return nil, canonicalMapRule{}, nil, err
		}
		outputBytes, err := json.Marshal(encoded.Outputs)
		if err != nil {
			return nil, canonicalMapRule{}, nil, err
		}
		id := "mapposet2-" + digestMapHex(outputBytes)
		if _, exists := byID[id]; !exists {
			byID[id] = candidate{id: id, rule: normalized, canonical: encoded}
		}
	}
	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if len(ids) == 0 {
		return nil, canonicalMapRule{}, nil, fmt.Errorf("%w: mapping rule has no generator alternatives", ErrInvalidDeclarativeRule)
	}
	base := byID[ids[0]].rule
	result := canonicalMapRule{Rule: byID[ids[0]].canonical}
	alternatives := make([]mapGeneratorAlternative, 0, len(ids))
	for _, id := range ids {
		current := byID[id]
		result.Generators = append(result.Generators, canonicalMapGeneratorAlternative{
			ID: id, Outputs: current.canonical.Outputs,
		})
		outputs := make([]RuleOutput, len(current.rule.Body.Outputs))
		for index, output := range current.rule.Body.Outputs {
			outputs[index] = copyRuleOutput(output)
		}
		alternatives = append(alternatives, mapGeneratorAlternative{id: id, outputs: outputs})
	}
	return base, result, alternatives, nil
}

func normalizeMapInducedDependencyPolicy(policy MapInducedDependencyPolicy) (MapInducedDependencyPolicy, error) {
	if policy == "" {
		return StrongInducedDependencyPolicy, nil
	}
	switch policy {
	case NoneInducedDependencyPolicy, StrongInducedDependencyPolicy,
		MaximaInducedDependencyPolicy, DominanceInducedDependencyPolicy,
		OverlookInducedDependencyPolicy, DiffInducedDependencyPolicy:
		return policy, nil
	default:
		return "", fmt.Errorf("%w: unsupported induced dependency policy %q", ErrInvalidEventPatternMap, policy)
	}
}

func copyMapRules(rules []*DeclarativeRule) []*DeclarativeRule {
	result := make([]*DeclarativeRule, len(rules))
	for i, rule := range rules {
		if rule == nil {
			continue
		}
		copy := *rule
		copy.Guard = copyRuleValuePointer(rule.Guard)
		copy.Body = copyRuleBody(rule.Body)
		result[i] = &copy
	}
	return result
}

func mapRangeValidationInterface(iface *InterfaceDecl) *InterfaceDecl {
	if iface == nil {
		return nil
	}
	result := &InterfaceDecl{Name: iface.Name, Functions: append([]FunctionDecl(nil), iface.Functions...)}
	result.Actions = make([]ActionDecl, len(iface.Actions))
	for index, action := range iface.Actions {
		action.Kind = OutAction
		action.Params = append([]ParamDecl(nil), action.Params...)
		result.Actions[index] = action
	}
	result.Services = make([]ServiceDecl, len(iface.Services))
	for i, service := range iface.Services {
		result.Services[i] = ServiceDecl{Name: service.Name, Functions: append([]FunctionDecl(nil), service.Functions...)}
		result.Services[i].Actions = make([]ActionDecl, len(service.Actions))
		for index, action := range service.Actions {
			action.Kind = OutAction
			action.Params = append([]ParamDecl(nil), action.Params...)
			result.Services[i].Actions[index] = action
		}
	}
	return result
}

func validateMapTrigger(domainID string, iface *InterfaceDecl, trigger pattern.Pattern) error {
	if pattern.HasModuleSourceBinding(trigger) {
		return fmt.Errorf("module-source placeholder bindings require executable communication Context and are outside deterministic map v4")
	}
	references, err := pattern.BasicEventReferences(trigger)
	if err != nil {
		return err
	}
	actions := publicInterfaceActions(iface)
	for _, reference := range references {
		if reference.Action == "" {
			return fmt.Errorf("unnamed MatchAny is not an action or function event")
		}
		for _, source := range reference.Sources {
			if source != domainID {
				return fmt.Errorf("qualified source %q is outside object domain %q", source, domainID)
			}
		}
		matchedShape := false
		for _, action := range actions {
			if action.Name != reference.Action {
				continue
			}
			parameters := make(map[string]string, len(action.Params))
			for _, parameter := range action.Params {
				parameters[parameter.Name] = parameter.Type
			}
			shape := true
			for _, name := range reference.Parameters {
				if _, ok := parameters[name]; !ok {
					shape = false
					break
				}
			}
			for _, binding := range reference.Bindings {
				typeName, ok := parameters[binding.Parameter]
				if !ok || (binding.Type != "" && !predefinedTypeAssignable(typeName, binding.Type)) {
					shape = false
					break
				}
			}
			for _, filter := range reference.Filters {
				typeName, ok := parameters[filter.Parameter]
				if !ok {
					shape = false
					break
				}
				decoded, err := gorapide.DecodeCanonicalParameters([]gorapide.CanonicalParameter{{Name: "value", Value: filter.Value}})
				if err != nil || !valueMatchesPredefinedType(decoded["value"], typeName) {
					shape = false
					break
				}
			}
			if shape {
				matchedShape = true
				break
			}
		}
		if !matchedShape {
			return fmt.Errorf("action %q and referenced parameters do not match the domain interface", reference.Action)
		}
	}
	return nil
}

func interfaceActions(iface *InterfaceDecl) []ActionDecl {
	if iface == nil {
		return nil
	}
	result := append([]ActionDecl(nil), iface.Actions...)
	for _, service := range iface.Services {
		result = append(result, service.Actions...)
	}
	return result
}

func publicInterfaceActions(iface *InterfaceDecl) []ActionDecl {
	result := interfaceActions(iface)
	write := 0
	for _, action := range result {
		if action.Kind == PrivateAction {
			continue
		}
		result[write] = action
		write++
	}
	return result[:write]
}

func interfaceMatchesMapAction(iface *InterfaceDecl, name string, params map[string]any) bool {
	for _, action := range interfaceActions(iface) {
		if action.Name != name || len(action.Params) != len(params) {
			continue
		}
		matched := true
		for _, declaration := range action.Params {
			value, ok := params[declaration.Name]
			if !ok || !valueMatchesPredefinedType(value, declaration.Type) {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

func interfaceMatchesPublicMapAction(iface *InterfaceDecl, name string, params map[string]any) bool {
	for _, action := range publicInterfaceActions(iface) {
		if action.Name != name || len(action.Params) != len(params) {
			continue
		}
		matched := true
		for _, declaration := range action.Params {
			value, ok := params[declaration.Name]
			if !ok || !valueMatchesPredefinedType(value, declaration.Type) {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

// MapGeneratedEventRecord connects a local generator identity to the exact
// range event occurrence it produced.
type MapGeneratedEventRecord struct {
	OutputID string `json:"output_id"`
	EventID  string `json:"event_id"`
}

// MapFiringRecord is the audit witness for one selected mapping-rule match.
// InducedBy contains only other range-producing firings; domain event IDs stay
// in Match and are never represented as range-poset causes.
type MapFiringRecord struct {
	FiringID    string                    `json:"firing_id"`
	RuleID      string                    `json:"rule_id"`
	GeneratorID string                    `json:"generator_id"`
	Match       pattern.CanonicalMatch    `json:"match"`
	MatchDigest string                    `json:"match_digest"`
	InducedBy   []string                  `json:"induced_by"`
	Generated   []MapGeneratedEventRecord `json:"generated"`
}

// MapExecutionArtifact is the complete canonical audit result of applying one
// closed map to one exact domain poset under explicit limits.
type MapExecutionArtifact struct {
	Format            string                                   `json:"format"`
	Engine            string                                   `json:"engine"`
	Profile           string                                   `json:"profile"`
	ModelDigest       string                                   `json:"model_digest"`
	DomainPosetDigest string                                   `json:"domain_poset_digest"`
	DependencyPolicy  MapInducedDependencyPolicy               `json:"dependency_policy"`
	Limits            MapExecutionLimits                       `json:"limits"`
	ChoicePolicy      string                                   `json:"choice_policy"`
	Choices           []ChoiceResolution                       `json:"choices"`
	StartEventID      string                                   `json:"start_event_id"`
	RangePoset        gorapide.CanonicalPoset                  `json:"range_poset"`
	Firings           []MapFiringRecord                        `json:"firings"`
	Constraints       *constraint.CanonicalConstraintSetReport `json:"range_constraint_report,omitempty"`
}

// MarshalCanonical serializes an internally produced map artifact.
func (artifact MapExecutionArtifact) MarshalCanonical() ([]byte, error) {
	normalized, err := normalizeMapExecutionArtifact(artifact)
	if err != nil {
		return nil, err
	}
	return json.Marshal(normalized)
}

// SemanticDigest returns the SHA-256 identity of the canonical artifact.
func (artifact MapExecutionArtifact) SemanticDigest() (string, error) {
	encoded, err := artifact.MarshalCanonical()
	if err != nil {
		return "", err
	}
	return digestMapBytes(encoded), nil
}

// ParseCanonicalMapExecutionArtifact accepts only the byte-exact canonical
// encoding and revalidates all internal identities, references, coverage, and
// range-poset relationships that do not require the original domain poset.
func ParseCanonicalMapExecutionArtifact(data []byte) (MapExecutionArtifact, error) {
	var artifact MapExecutionArtifact
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&artifact); err != nil {
		return MapExecutionArtifact{}, fmt.Errorf("%w: artifact JSON: %v", ErrInvalidEventPatternMap, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return MapExecutionArtifact{}, fmt.Errorf("%w: trailing JSON content", ErrInvalidEventPatternMap)
	}
	normalized, err := normalizeMapExecutionArtifact(artifact)
	if err != nil {
		return MapExecutionArtifact{}, err
	}
	reencoded, err := json.Marshal(normalized)
	if err != nil || !bytes.Equal(reencoded, data) {
		return MapExecutionArtifact{}, fmt.Errorf("%w: input is not the canonical byte encoding", ErrInvalidEventPatternMap)
	}
	return normalized, nil
}

func normalizeMapExecutionArtifact(artifact MapExecutionArtifact) (MapExecutionArtifact, error) {
	if artifact.Format != MapExecutionArtifactFormat || artifact.Engine != DeterministicMapEngineVersion || artifact.Profile != CompatibilityProfile {
		return MapExecutionArtifact{}, fmt.Errorf("%w: artifact version or profile is invalid", ErrInvalidEventPatternMap)
	}
	if !validMapDigest(artifact.ModelDigest) || !validMapDigest(artifact.DomainPosetDigest) {
		return MapExecutionArtifact{}, fmt.Errorf("%w: artifact has a malformed model or domain digest", ErrInvalidEventPatternMap)
	}
	if _, err := normalizeMapInducedDependencyPolicy(artifact.DependencyPolicy); err != nil || artifact.DependencyPolicy == "" {
		return MapExecutionArtifact{}, fmt.Errorf("%w: artifact has an invalid induced dependency policy %q", ErrInvalidEventPatternMap, artifact.DependencyPolicy)
	}
	if artifact.ChoicePolicy != ChoiceResolutionPolicy {
		return MapExecutionArtifact{}, fmt.Errorf("%w: artifact has invalid choice policy %q", ErrInvalidEventPatternMap, artifact.ChoicePolicy)
	}
	choices, err := normalizeMapChoiceResolutions(artifact.Choices)
	if err != nil {
		return MapExecutionArtifact{}, err
	}
	if artifact.Limits.MaxFirings == 0 || artifact.Limits.MaxRangeEvents == 0 || artifact.StartEventID == "" {
		return MapExecutionArtifact{}, fmt.Errorf("%w: artifact has invalid limits or Start identity", ErrInvalidEventPatternMap)
	}
	rangeBytes, err := json.Marshal(artifact.RangePoset)
	if err != nil {
		return MapExecutionArtifact{}, err
	}
	rangePoset, err := gorapide.ParseCanonicalPoset(rangeBytes)
	if err != nil {
		return MapExecutionArtifact{}, fmt.Errorf("%w: range poset: %v", ErrInvalidEventPatternMap, err)
	}
	if uint64(rangePoset.Len()) > artifact.Limits.MaxRangeEvents || uint64(len(artifact.Firings)) > artifact.Limits.MaxFirings {
		return MapExecutionArtifact{}, fmt.Errorf("%w: artifact exceeds its declared limits", ErrInvalidEventPatternMap)
	}
	start, ok := rangeEventByID(rangePoset, gorapide.EventID(artifact.StartEventID))
	if !ok || start.Name != "Start" {
		return MapExecutionArtifact{}, fmt.Errorf("%w: range poset does not contain the declared map Start event", ErrInvalidEventPatternMap)
	}
	for _, event := range rangePoset.All() {
		if event.ID != start.ID && !rangePoset.IsCausallyBefore(start.ID, event.ID) {
			return MapExecutionArtifact{}, fmt.Errorf("%w: map Start does not precede range event %s", ErrInvalidEventPatternMap, event.ID)
		}
	}

	result := artifact
	result.Choices = choices
	result.Firings = append([]MapFiringRecord{}, artifact.Firings...)
	firingIDs := make(map[string]bool, len(result.Firings))
	choiceDomains := make(map[string]string, len(result.Firings))
	generatedEvents := make(map[string]string)
	consumedByRule := make(map[string]map[string]string)
	previousFiring := ""
	for index := range result.Firings {
		firing := &result.Firings[index]
		if firing.FiringID == "" || firing.RuleID == "" || firing.GeneratorID == "" || firingIDs[firing.FiringID] || (index > 0 && firing.FiringID <= previousFiring) {
			return MapExecutionArtifact{}, fmt.Errorf("%w: empty, duplicate, or unsorted map firing identity %q", ErrInvalidEventPatternMap, firing.FiringID)
		}
		firingIDs[firing.FiringID] = true
		choiceDomains[mapGeneratorChoiceDomain(firing.RuleID, firing.FiringID)] = firing.GeneratorID
		previousFiring = firing.FiringID
		canonicalMatch, matchDigest, err := normalizeMapCanonicalMatch(firing.Match)
		if err != nil {
			return MapExecutionArtifact{}, fmt.Errorf("%w: firing %q match: %v", ErrInvalidEventPatternMap, firing.FiringID, err)
		}
		if firing.MatchDigest != matchDigest {
			return MapExecutionArtifact{}, fmt.Errorf("%w: firing %q match digest mismatch", ErrInvalidEventPatternMap, firing.FiringID)
		}
		firing.Match = canonicalMatch
		if consumedByRule[firing.RuleID] == nil {
			consumedByRule[firing.RuleID] = make(map[string]string)
		}
		for _, eventID := range firing.Match.Events {
			if previous, exists := consumedByRule[firing.RuleID][eventID]; exists {
				return MapExecutionArtifact{}, fmt.Errorf("%w: domain event %s is consumed by mapping rule %q in both %s and %s", ErrInvalidEventPatternMap, eventID, firing.RuleID, previous, firing.FiringID)
			}
			consumedByRule[firing.RuleID][eventID] = firing.FiringID
		}
		firingKey, err := json.Marshal(struct {
			Rule  string                 `json:"rule"`
			Match pattern.CanonicalMatch `json:"match"`
		}{Rule: firing.RuleID, Match: firing.Match})
		if err != nil {
			return MapExecutionArtifact{}, err
		}
		if firing.FiringID != "mapfire1-"+digestMapHex(firingKey) {
			return MapExecutionArtifact{}, fmt.Errorf("%w: firing %q identity does not match its rule and domain witness", ErrInvalidEventPatternMap, firing.FiringID)
		}
		firing.InducedBy = append([]string{}, firing.InducedBy...)
		for i, predecessor := range firing.InducedBy {
			if predecessor == "" || predecessor == firing.FiringID || (i > 0 && predecessor <= firing.InducedBy[i-1]) {
				return MapExecutionArtifact{}, fmt.Errorf("%w: firing %q has invalid induced predecessor list", ErrInvalidEventPatternMap, firing.FiringID)
			}
		}
		firing.Generated = append([]MapGeneratedEventRecord{}, firing.Generated...)
		for outputIndex, generated := range firing.Generated {
			if generated.OutputID == "" || generated.EventID == "" || (outputIndex > 0 && generated.OutputID <= firing.Generated[outputIndex-1].OutputID) {
				return MapExecutionArtifact{}, fmt.Errorf("%w: firing %q has empty, duplicate, or unsorted generated output", ErrInvalidEventPatternMap, firing.FiringID)
			}
			if generated.EventID == artifact.StartEventID {
				return MapExecutionArtifact{}, fmt.Errorf("%w: map Start is claimed as a rule output", ErrInvalidEventPatternMap)
			}
			if previous, exists := generatedEvents[generated.EventID]; exists {
				return MapExecutionArtifact{}, fmt.Errorf("%w: range event %s is generated by both %s and %s", ErrInvalidEventPatternMap, generated.EventID, previous, firing.FiringID)
			}
			if _, ok := rangeEventByID(rangePoset, gorapide.EventID(generated.EventID)); !ok {
				return MapExecutionArtifact{}, fmt.Errorf("%w: firing %q references absent range event %s", ErrInvalidEventPatternMap, firing.FiringID, generated.EventID)
			}
			generatedEvents[generated.EventID] = firing.FiringID
		}
	}
	for _, choice := range result.Choices {
		selected, exists := choiceDomains[choice.Domain]
		if !exists || selected != choice.Selected {
			return MapExecutionArtifact{}, fmt.Errorf("%w: generator choice %q does not match a firing witness", ErrInvalidEventPatternMap, choice.Point)
		}
	}
	for _, firing := range result.Firings {
		for _, predecessor := range firing.InducedBy {
			if !firingIDs[predecessor] {
				return MapExecutionArtifact{}, fmt.Errorf("%w: firing %q references absent induced predecessor %q", ErrInvalidEventPatternMap, firing.FiringID, predecessor)
			}
			for _, before := range generatedByMapFiring(result.Firings, predecessor) {
				for _, after := range firing.Generated {
					if !rangePoset.IsCausallyBefore(gorapide.EventID(before.EventID), gorapide.EventID(after.EventID)) {
						return MapExecutionArtifact{}, fmt.Errorf("%w: induced firing %q does not precede firing %q in the range poset", ErrInvalidEventPatternMap, predecessor, firing.FiringID)
					}
				}
			}
		}
		for _, eventID := range firing.Match.Events {
			if _, ok := rangeEventByID(rangePoset, gorapide.EventID(eventID)); ok {
				return MapExecutionArtifact{}, fmt.Errorf("%w: domain witness %s is also present in the range poset", ErrInvalidEventPatternMap, eventID)
			}
		}
	}
	inducedRelation := make([][]bool, len(result.Firings))
	for before := range result.Firings {
		inducedRelation[before] = make([]bool, len(result.Firings))
		if len(result.Firings[before].Generated) == 0 {
			continue
		}
		for after := range result.Firings {
			if before != after && len(result.Firings[after].Generated) != 0 {
				inducedRelation[before][after] = allGeneratedMapEventsBefore(
					result.Firings[before].Generated, result.Firings[after].Generated, rangePoset,
				)
			}
		}
	}
	for index, firing := range result.Firings {
		expected := directArtifactMapPredecessors(index, result.Firings, inducedRelation)
		if !equalMapStrings(expected, firing.InducedBy) {
			return MapExecutionArtifact{}, fmt.Errorf("%w: firing %q induced witness does not match range causality", ErrInvalidEventPatternMap, firing.FiringID)
		}
	}
	for _, event := range rangePoset.All() {
		if event.ID != start.ID {
			if _, ok := generatedEvents[string(event.ID)]; !ok {
				return MapExecutionArtifact{}, fmt.Errorf("%w: range event %s has no generating firing witness", ErrInvalidEventPatternMap, event.ID)
			}
		}
	}
	if result.Constraints != nil {
		constraintBytes, err := json.Marshal(result.Constraints)
		if err != nil {
			return MapExecutionArtifact{}, err
		}
		report, err := constraint.ParseCanonicalConstraintSetReport(constraintBytes)
		if err != nil {
			return MapExecutionArtifact{}, fmt.Errorf("%w: range constraint report: %v", ErrInvalidEventPatternMap, err)
		}
		rangeDigest, err := rangePoset.SemanticDigest()
		if err != nil {
			return MapExecutionArtifact{}, err
		}
		if report.PosetDigest != rangeDigest {
			return MapExecutionArtifact{}, fmt.Errorf("%w: range constraint report refers to another poset", ErrInvalidEventPatternMap)
		}
		result.Constraints = &report
	}
	return result, nil
}

func normalizeMapChoiceResolutions(source []ChoiceResolution) ([]ChoiceResolution, error) {
	result := make([]ChoiceResolution, len(source))
	seenPoints := make(map[string]bool, len(source))
	seenOrdinals := make(map[uint64]bool, len(source))
	for index, resolution := range source {
		if resolution.Point == "" || resolution.Domain == "" || resolution.Selected == "" || seenPoints[resolution.Point] {
			return nil, fmt.Errorf("%w: map artifact has an empty or duplicate choice witness", ErrInvalidEventPatternMap)
		}
		seenPoints[resolution.Point] = true
		options := append([]string(nil), resolution.Options...)
		sort.Strings(options)
		selected := false
		for optionIndex, option := range options {
			if option == "" || (optionIndex > 0 && option == options[optionIndex-1]) {
				return nil, fmt.Errorf("%w: map artifact choice %q has empty or duplicate alternatives", ErrInvalidEventPatternMap, resolution.Point)
			}
			if option == resolution.Selected {
				selected = true
			}
		}
		if len(options) < 2 || !selected {
			return nil, fmt.Errorf("%w: map artifact choice %q has no valid selected alternative", ErrInvalidEventPatternMap, resolution.Point)
		}
		encoded, err := json.Marshal(options)
		if err != nil {
			return nil, err
		}
		prefix := resolution.Domain + "#"
		suffix := "@" + digestBytes(encoded)
		if !strings.HasPrefix(resolution.Point, prefix) || !strings.HasSuffix(resolution.Point, suffix) {
			return nil, fmt.Errorf("%w: map artifact choice %q has an invalid point identity", ErrInvalidEventPatternMap, resolution.Point)
		}
		ordinalText := strings.TrimSuffix(strings.TrimPrefix(resolution.Point, prefix), suffix)
		ordinal, err := strconv.ParseUint(ordinalText, 10, 64)
		if err != nil || ordinal == 0 || seenOrdinals[ordinal] {
			return nil, fmt.Errorf("%w: map artifact choice %q has an invalid ordinal", ErrInvalidEventPatternMap, resolution.Point)
		}
		seenOrdinals[ordinal] = true
		result[index] = resolution
		result[index].Options = options
	}
	for ordinal := uint64(1); ordinal <= uint64(len(result)); ordinal++ {
		if !seenOrdinals[ordinal] {
			return nil, fmt.Errorf("%w: map artifact choice ordinals are not contiguous", ErrInvalidEventPatternMap)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Point < result[j].Point })
	return result, nil
}

func normalizeMapCanonicalMatch(match pattern.CanonicalMatch) (pattern.CanonicalMatch, string, error) {
	if len(match.Events) == 0 {
		return pattern.CanonicalMatch{}, "", fmt.Errorf("mapping match contains no domain event occurrence")
	}
	events := make(gorapide.EventSet, 0, len(match.Events))
	for index, eventID := range match.Events {
		if eventID == "" || (index > 0 && eventID <= match.Events[index-1]) {
			return pattern.CanonicalMatch{}, "", fmt.Errorf("event identities are empty, duplicate, or unsorted")
		}
		events = append(events, &gorapide.Event{ID: gorapide.EventID(eventID)})
	}
	bindings := make(pattern.Bindings, 0, len(match.Bindings))
	for index, binding := range match.Bindings {
		if binding.Placeholder == "" || (index > 0 && binding.Placeholder <= match.Bindings[index-1].Placeholder) {
			return pattern.CanonicalMatch{}, "", fmt.Errorf("binding identities are empty, duplicate, or unsorted")
		}
		decoded, err := gorapide.DecodeCanonicalParameters([]gorapide.CanonicalParameter{{Name: "value", Value: binding.Value}})
		if err != nil {
			return pattern.CanonicalMatch{}, "", err
		}
		bindings = append(bindings, pattern.Binding{Placeholder: binding.Placeholder, Value: decoded["value"]})
	}
	reconstructed := pattern.MatchResult{Events: events, Bindings: bindings}
	canonical, err := pattern.CanonicalizeMatch(reconstructed)
	if err != nil {
		return pattern.CanonicalMatch{}, "", err
	}
	left, _ := json.Marshal(match)
	right, _ := json.Marshal(canonical)
	if !bytes.Equal(left, right) {
		return pattern.CanonicalMatch{}, "", fmt.Errorf("match is not canonical")
	}
	digest, err := pattern.SemanticDigestMatches([]pattern.MatchResult{reconstructed})
	return canonical, digest, err
}

func rangeEventByID(poset *gorapide.Poset, id gorapide.EventID) (*gorapide.Event, bool) {
	for _, event := range poset.All() {
		if event.ID == id {
			return event, true
		}
	}
	return nil, false
}

func generatedByMapFiring(firings []MapFiringRecord, firingID string) []MapGeneratedEventRecord {
	for _, firing := range firings {
		if firing.FiringID == firingID {
			return firing.Generated
		}
	}
	return nil
}

func allGeneratedMapEventsBefore(left, right []MapGeneratedEventRecord, poset *gorapide.Poset) bool {
	if len(left) == 0 || len(right) == 0 {
		return false
	}
	for _, before := range left {
		for _, after := range right {
			if !poset.IsCausallyBefore(gorapide.EventID(before.EventID), gorapide.EventID(after.EventID)) {
				return false
			}
		}
	}
	return true
}

func directArtifactMapPredecessors(index int, firings []MapFiringRecord, relation [][]bool) []string {
	if len(firings[index].Generated) == 0 {
		return []string{}
	}
	result := make([]string, 0)
	for predecessor := range firings {
		if len(firings[predecessor].Generated) == 0 || !relation[predecessor][index] {
			continue
		}
		dominated := false
		for middle := range firings {
			if middle == predecessor || middle == index || len(firings[middle].Generated) == 0 {
				continue
			}
			if relation[predecessor][middle] && relation[middle][index] {
				dominated = true
				break
			}
		}
		if !dominated {
			result = append(result, firings[predecessor].FiringID)
		}
	}
	sort.Strings(result)
	return result
}

func equalMapStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func validMapDigest(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != len("sha256:")+64 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil
}

// MapExecutionResult retains both the queryable range poset and its immutable
// canonical audit representation.
type MapExecutionResult struct {
	Range    *gorapide.Poset
	Artifact MapExecutionArtifact
}

func (result *MapExecutionResult) MarshalCanonical() ([]byte, error) {
	if result == nil {
		return nil, fmt.Errorf("%w: result is nil", ErrInvalidEventPatternMap)
	}
	return result.Artifact.MarshalCanonical()
}

func (result *MapExecutionResult) ArtifactDigest() (string, error) {
	if result == nil {
		return "", fmt.Errorf("%w: result is nil", ErrInvalidEventPatternMap)
	}
	return result.Artifact.SemanticDigest()
}

type selectedMapFiring struct {
	rule          *DeclarativeRule
	generator     mapGeneratorAlternative
	match         pattern.MatchResult
	canonical     pattern.CanonicalMatch
	matchDigest   string
	key           string
	firingID      string
	generated     []MapGeneratedEventRecord
	frontier      []gorapide.EventID
	directInduced []int
}

// ExecuteDeterministic applies the map without mutating the domain poset. It
// returns a distinct range poset whose only causal edges are map Start, body-
// local generator order, and the selected published induced dependency.
func (mapping *EventPatternMap) ExecuteDeterministic(domain *gorapide.Poset, limits MapExecutionLimits) (*MapExecutionResult, error) {
	return mapping.ExecuteDeterministicRequest(domain, MapExecutionRequest{Limits: limits})
}

// ExecuteDeterministicRequest applies the map with an explicit canonical
// generator-choice schedule.
func (mapping *EventPatternMap) ExecuteDeterministicRequest(domain *gorapide.Poset, request MapExecutionRequest) (*MapExecutionResult, error) {
	prepared, err := mapping.PrepareDeterministic()
	if err != nil {
		return nil, err
	}
	return prepared.ExecuteDeterministicRequest(domain, request)
}

// ExecuteDeterministic applies this immutable map snapshot without mutating
// the domain poset.
func (prepared *PreparedEventPatternMap) ExecuteDeterministic(
	domain *gorapide.Poset,
	limits MapExecutionLimits,
) (*MapExecutionResult, error) {
	return prepared.ExecuteDeterministicRequest(domain, MapExecutionRequest{Limits: limits})
}

// ExecuteDeterministicRequest applies this immutable map snapshot with an
// explicit canonical generator-choice schedule.
func (prepared *PreparedEventPatternMap) ExecuteDeterministicRequest(
	domain *gorapide.Poset,
	request MapExecutionRequest,
) (*MapExecutionResult, error) {
	model, err := prepared.checkedModel()
	if err != nil {
		return nil, err
	}
	return executeDeterministicEventPatternMap(model, domain, request)
}

func executeDeterministicEventPatternMap(
	model *deterministicEventPatternMap,
	domain *gorapide.Poset,
	request MapExecutionRequest,
) (*MapExecutionResult, error) {
	if domain == nil {
		return nil, fmt.Errorf("%w: domain poset is nil", ErrInvalidEventPatternMap)
	}
	if request.Limits.MaxFirings == 0 || request.Limits.MaxRangeEvents == 0 {
		return nil, fmt.Errorf("%w: map limits must be greater than zero", ErrInvalidEventPatternMap)
	}
	choices, err := canonicalChoiceSchedule(request.Choices)
	if err != nil {
		return nil, err
	}
	resolver := newChoiceResolver(choices)
	limits := request.Limits
	mapping := &EventPatternMap{
		Name: model.name, DomainID: model.domainID,
		DomainInterface: model.domainInterface, RangeInterface: model.rangeInterface,
		InducedDependencyPolicy: model.inducedDependencyPolicy,
		Rules:                   model.rules, RangeConstraints: model.rangeConstraints,
	}
	domainDigest, err := domain.SemanticDigest()
	if err != nil {
		return nil, fmt.Errorf("%w: domain poset: %v", ErrInvalidEventPatternMap, err)
	}
	visible, err := mapping.visibleDomainEvents(domain)
	if err != nil {
		return nil, err
	}
	firings, err := mapping.selectMapFirings(model.rules, domain, visible, limits.MaxFirings)
	if err != nil {
		return nil, err
	}
	eventCount := uint64(1)
	for index := range firings {
		alternatives := model.generators[firings[index].rule.ID]
		if len(alternatives) == 0 {
			return nil, fmt.Errorf("%w: mapping rule %q has no normalized generator", ErrInvalidEventPatternMap, firings[index].rule.ID)
		}
		options := make([]string, len(alternatives))
		for alternativeIndex, alternative := range alternatives {
			options[alternativeIndex] = alternative.id
		}
		selected, err := resolver.resolve(
			mapGeneratorChoiceDomain(firings[index].rule.ID, firings[index].firingID), options,
		)
		if err != nil {
			return nil, err
		}
		for _, alternative := range alternatives {
			if alternative.id == selected {
				firings[index].generator = alternative
				break
			}
		}
		eventCount += uint64(len(firings[index].generator.outputs))
		if eventCount > limits.MaxRangeEvents {
			return nil, fmt.Errorf("%w: range events exceed %d", ErrMapExecutionLimit, limits.MaxRangeEvents)
		}
	}
	if err := resolver.finish(); err != nil {
		return nil, err
	}

	rangePoset := gorapide.NewPoset()
	start, err := gorapide.NewDeterministicEvent(gorapide.EventProvenance{
		Profile: CompatibilityProfile, Model: model.digest, Instance: mapping.Name,
		Action: "Start", Occurrence: "map-start|domain=" + domainDigest,
	}, nil)
	if err != nil {
		return nil, err
	}
	if err := rangePoset.AddEvent(start); err != nil {
		return nil, err
	}

	relation := mapFiringRelation(firings, domain, model.inducedDependencyPolicy)
	order, err := mapFiringTopologicalOrder(firings, relation)
	if err != nil {
		return nil, err
	}
	for _, index := range order {
		direct := directNonEmptyMapPredecessors(index, firings, relation)
		firings[index].directInduced = direct
		baseCauses := make([]gorapide.EventID, 0)
		for _, predecessor := range direct {
			baseCauses = append(baseCauses, firings[predecessor].frontier...)
		}
		if len(baseCauses) == 0 {
			baseCauses = append(baseCauses, start.ID)
		}
		generated, frontier, err := materializeMapFiring(mapping, model.digest, domainDigest, firings[index], baseCauses, rangePoset)
		if err != nil {
			return nil, err
		}
		firings[index].generated = generated
		firings[index].frontier = frontier
	}

	canonicalPoset, err := rangePoset.Canonical()
	if err != nil {
		return nil, err
	}
	artifact := MapExecutionArtifact{
		Format: MapExecutionArtifactFormat, Engine: DeterministicMapEngineVersion,
		Profile: CompatibilityProfile, ModelDigest: model.digest, DomainPosetDigest: domainDigest,
		DependencyPolicy: model.inducedDependencyPolicy,
		Limits:           limits, ChoicePolicy: ChoiceResolutionPolicy,
		Choices: resolver.canonicalResolutions(), StartEventID: string(start.ID), RangePoset: canonicalPoset,
		Firings: canonicalMapFiringRecords(firings),
	}
	if model.rangeConstraints != nil {
		report, err := model.rangeConstraints.EvaluateCanonical(rangePoset)
		if err != nil {
			return nil, fmt.Errorf("%w: range constraints: %v", ErrInvalidEventPatternMap, err)
		}
		artifact.Constraints = &report
	}
	return &MapExecutionResult{Range: rangePoset, Artifact: artifact}, nil
}

// ReplayDeterministic reapplies the exact model, domain poset, and limits and
// verifies the complete artifact identity.
func (mapping *EventPatternMap) ReplayDeterministic(domain *gorapide.Poset, limits MapExecutionLimits, expectedArtifactDigest string) (*MapExecutionResult, error) {
	return mapping.ReplayDeterministicRequest(domain, MapExecutionRequest{Limits: limits}, expectedArtifactDigest)
}

// ReplayDeterministicRequest reapplies the exact scheduled map request and
// verifies the complete artifact identity.
func (mapping *EventPatternMap) ReplayDeterministicRequest(domain *gorapide.Poset, request MapExecutionRequest, expectedArtifactDigest string) (*MapExecutionResult, error) {
	prepared, err := mapping.PrepareDeterministic()
	if err != nil {
		return nil, err
	}
	return prepared.ReplayDeterministicRequest(domain, request, expectedArtifactDigest)
}

// ReplayDeterministic reapplies this immutable map snapshot and verifies the
// complete artifact identity.
func (prepared *PreparedEventPatternMap) ReplayDeterministic(
	domain *gorapide.Poset,
	limits MapExecutionLimits,
	expectedArtifactDigest string,
) (*MapExecutionResult, error) {
	return prepared.ReplayDeterministicRequest(domain, MapExecutionRequest{Limits: limits}, expectedArtifactDigest)
}

// ReplayDeterministicRequest reapplies this immutable map snapshot with the
// exact scheduled request and verifies the complete artifact identity.
func (prepared *PreparedEventPatternMap) ReplayDeterministicRequest(
	domain *gorapide.Poset,
	request MapExecutionRequest,
	expectedArtifactDigest string,
) (*MapExecutionResult, error) {
	result, err := prepared.ExecuteDeterministicRequest(domain, request)
	if err != nil {
		return nil, err
	}
	digest, err := result.ArtifactDigest()
	if err != nil {
		return nil, err
	}
	if digest != expectedArtifactDigest {
		return nil, fmt.Errorf("%w: expected=%s actual=%s", ErrMapReplayMismatch, expectedArtifactDigest, digest)
	}
	return result, nil
}

func mapGeneratorChoiceDomain(ruleID, firingID string) string {
	return "map-generator:" + ruleID + ":" + firingID
}

func (mapping *EventPatternMap) visibleDomainEvents(domain *gorapide.Poset) (gorapide.EventSet, error) {
	visible := make(gorapide.EventSet, 0)
	for _, event := range domain.All() {
		for _, view := range event.ObservationViews() {
			if view.Source != mapping.DomainID {
				continue
			}
			if !interfaceMatchesPublicMapAction(mapping.DomainInterface, view.Name, view.Params) {
				continue
			}
			visible = append(visible, view)
		}
	}
	projection, err := pattern.NewProjection(domain, visible)
	if err != nil {
		return nil, fmt.Errorf("%w: domain visibility: %v", ErrInvalidEventPatternMap, err)
	}
	result := make(gorapide.EventSet, 0)
	for _, action := range publicInterfaceActions(mapping.DomainInterface) {
		result = append(result, projection.ByName(action.Name)...)
	}
	return result, nil
}

func (mapping *EventPatternMap) selectMapFirings(rules []*DeclarativeRule, domain *gorapide.Poset, visible gorapide.EventSet, maximum uint64) ([]selectedMapFiring, error) {
	result := make([]selectedMapFiring, 0)
	for _, rule := range rules {
		consumed := make(map[gorapide.EventID]bool)
		for {
			available := make(gorapide.EventSet, 0, len(visible))
			for _, event := range visible {
				if event != nil && !consumed[event.ID] {
					available = append(available, event)
				}
			}
			projection, err := pattern.NewProjection(domain, available)
			if err != nil {
				return nil, err
			}
			matches, err := pattern.MatchWithBindings(rule.Trigger, projection)
			if err != nil {
				return nil, fmt.Errorf("%w: mapping rule %q trigger: %v", ErrInvalidEventPatternMap, rule.ID, err)
			}
			matches, err = filterMapGuardMatches(rule, matches)
			if err != nil {
				return nil, err
			}
			matches, err = earliestMapMatches(matches, projection)
			if err != nil {
				return nil, err
			}
			matches = maximalRuleMatches(matches)
			if len(matches) == 0 {
				break
			}
			sort.Slice(matches, func(i, j int) bool {
				return canonicalMapMatchKey(matches[i]) < canonicalMapMatchKey(matches[j])
			})
			selected := matches[0]
			canonical, err := pattern.CanonicalizeMatch(selected)
			if err != nil {
				return nil, err
			}
			matchDigest, err := pattern.SemanticDigestMatches([]pattern.MatchResult{selected})
			if err != nil {
				return nil, err
			}
			keyBytes, err := json.Marshal(struct {
				Rule  string                 `json:"rule"`
				Match pattern.CanonicalMatch `json:"match"`
			}{Rule: rule.ID, Match: canonical})
			if err != nil {
				return nil, err
			}
			for _, event := range selected.Events {
				consumed[event.ID] = true
			}
			result = append(result, selectedMapFiring{
				rule: rule, match: selected, canonical: canonical, matchDigest: matchDigest,
				key: string(keyBytes), firingID: "mapfire1-" + digestMapHex(keyBytes),
			})
			if uint64(len(result)) > maximum {
				return nil, fmt.Errorf("%w: firings exceed %d", ErrMapExecutionLimit, maximum)
			}
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].key < result[j].key })
	return result, nil
}

func filterMapGuardMatches(rule *DeclarativeRule, matches []pattern.MatchResult) ([]pattern.MatchResult, error) {
	if rule.Guard == nil {
		return matches, nil
	}
	result := make([]pattern.MatchResult, 0, len(matches))
	for _, match := range matches {
		value, _, reads, err := evaluateRuleValue("mapping rule "+rule.ID+" guard", *rule.Guard, match.Bindings, nil)
		if err != nil {
			return nil, fmt.Errorf("%w: mapping rule %q guard: %v", ErrInvalidEventPatternMap, rule.ID, err)
		}
		if len(reads) != 0 {
			return nil, fmt.Errorf("%w: mapping rule %q guard read map state", ErrInvalidEventPatternMap, rule.ID)
		}
		matched, ok := value.(bool)
		if !ok {
			return nil, fmt.Errorf("%w: mapping rule %q guard evaluated to %T", ErrInvalidEventPatternMap, rule.ID, value)
		}
		if matched {
			result = append(result, match)
		}
	}
	return result, nil
}

func earliestMapMatches(matches []pattern.MatchResult, poset pattern.PosetReader) ([]pattern.MatchResult, error) {
	result := make([]pattern.MatchResult, 0, len(matches))
	for i, candidate := range matches {
		dominated := false
		for j, other := range matches {
			if i == j {
				continue
			}
			earlier, err := pattern.IsEarlierMatch(other.Events, candidate.Events, poset)
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

func canonicalMapMatchKey(match pattern.MatchResult) string {
	canonical, err := pattern.CanonicalizeMatch(match)
	if err != nil {
		return ""
	}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return ""
	}
	return string(encoded)
}

func mapFiringRelation(
	firings []selectedMapFiring,
	domain *gorapide.Poset,
	policy MapInducedDependencyPolicy,
) [][]bool {
	relation := make([][]bool, len(firings))
	for i := range relation {
		relation[i] = make([]bool, len(firings))
		for j := range firings {
			if i != j {
				relation[i][j] = mapMatchInduces(
					firings[i].match.Events, firings[j].match.Events, domain, policy,
				)
			}
		}
	}
	return relation
}

func mapMatchInduces(
	left, right gorapide.EventSet,
	domain *gorapide.Poset,
	policy MapInducedDependencyPolicy,
) bool {
	if len(left) == 0 || len(right) == 0 || domain == nil {
		return false
	}
	switch policy {
	case NoneInducedDependencyPolicy:
		return false
	case StrongInducedDependencyPolicy:
		return allMapMatchEventsBefore(left, right, domain)
	case MaximaInducedDependencyPolicy:
		return allMapMatchEventsBefore(
			mapMatchMaxima(left, domain), mapMatchMaxima(right, domain), domain,
		)
	case DominanceInducedDependencyPolicy:
		rightOnly := mapEventSetDifference(right, left)
		if len(rightOnly) == 0 {
			return false
		}
		for _, before := range left {
			found := false
			for _, after := range rightOnly {
				if mapEventCausallyBefore(before, after, domain) {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
		return true
	case OverlookInducedDependencyPolicy:
		for _, after := range right {
			found := false
			for _, before := range left {
				if mapEventCausallyBefore(before, after, domain) {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
		return true
	case DiffInducedDependencyPolicy:
		return allMapMatchEventsBefore(left, mapEventSetDifference(right, left), domain)
	default:
		return false
	}
}

// mapMatchMaxima follows the primer's explicit definition: an event is in a
// trigger's maxima when no other event in that trigger causally precedes it.
// This source terminology is retained even though some order-theory texts call
// these events minimal.
func mapMatchMaxima(events gorapide.EventSet, domain *gorapide.Poset) gorapide.EventSet {
	result := make(gorapide.EventSet, 0, len(events))
	for _, candidate := range events {
		preceded := false
		for _, other := range events {
			if mapEventCausallyBefore(other, candidate, domain) {
				preceded = true
				break
			}
		}
		if !preceded {
			result = append(result, candidate)
		}
	}
	return result
}

func mapEventSetDifference(left, right gorapide.EventSet) gorapide.EventSet {
	excluded := make(map[gorapide.EventID]bool, len(right))
	for _, event := range right {
		if event != nil {
			excluded[event.ID] = true
		}
	}
	result := make(gorapide.EventSet, 0, len(left))
	for _, event := range left {
		if event != nil && !excluded[event.ID] {
			result = append(result, event)
		}
	}
	return result
}

func mapEventCausallyBefore(before, after *gorapide.Event, domain *gorapide.Poset) bool {
	return before != nil && after != nil && before.ID != after.ID && domain.IsCausallyBefore(before.ID, after.ID)
}

func allMapMatchEventsBefore(left, right gorapide.EventSet, domain *gorapide.Poset) bool {
	if len(left) == 0 {
		return false
	}
	for _, before := range left {
		for _, after := range right {
			if !mapEventCausallyBefore(before, after, domain) {
				return false
			}
		}
	}
	return true
}

func mapFiringTopologicalOrder(firings []selectedMapFiring, relation [][]bool) ([]int, error) {
	degree := make([]int, len(firings))
	for i := range firings {
		for j := range firings {
			if relation[j][i] {
				degree[i]++
			}
		}
	}
	result := make([]int, 0, len(firings))
	for len(result) < len(firings) {
		ready := make([]int, 0)
		for i := range firings {
			if degree[i] == 0 {
				ready = append(ready, i)
			}
		}
		if len(ready) == 0 {
			return nil, fmt.Errorf("%w: induced firing dependency contains a cycle", ErrInvalidEventPatternMap)
		}
		sort.Slice(ready, func(i, j int) bool { return firings[ready[i]].key < firings[ready[j]].key })
		selected := ready[0]
		degree[selected] = -1
		result = append(result, selected)
		for j := range firings {
			if relation[selected][j] {
				degree[j]--
			}
		}
	}
	return result, nil
}

func directNonEmptyMapPredecessors(index int, firings []selectedMapFiring, relation [][]bool) []int {
	if len(firings[index].generator.outputs) == 0 {
		return nil
	}
	result := make([]int, 0)
	for predecessor := range firings {
		if len(firings[predecessor].generator.outputs) == 0 || !relation[predecessor][index] {
			continue
		}
		dominated := false
		for middle := range firings {
			if middle == predecessor || middle == index || len(firings[middle].generator.outputs) == 0 {
				continue
			}
			if relation[predecessor][middle] && relation[middle][index] {
				dominated = true
				break
			}
		}
		if !dominated {
			result = append(result, predecessor)
		}
	}
	sort.Slice(result, func(i, j int) bool { return firings[result[i]].key < firings[result[j]].key })
	return result
}

func materializeMapFiring(mapping *EventPatternMap, modelDigest, domainDigest string, firing selectedMapFiring, baseCauses []gorapide.EventID, rangePoset *gorapide.Poset) ([]MapGeneratedEventRecord, []gorapide.EventID, error) {
	ordered, err := ruleBodyOrder(firing.generator.outputs)
	if err != nil {
		return nil, nil, err
	}
	actual := make(map[string]*gorapide.Event, len(ordered))
	children := make(map[string]bool, len(ordered))
	rootClasses := ruleBodyRootClasses(ordered)
	records := make([]MapGeneratedEventRecord, 0, len(ordered))
	for _, output := range ordered {
		parameters, reads, stateCauses, err := resolveRuleParameters(firing.rule.ID, output, firing.match.Bindings, nil)
		if err != nil {
			return nil, nil, err
		}
		if len(reads) != 0 || len(stateCauses) != 0 {
			return nil, nil, fmt.Errorf("%w: mapping rule %q output %q read map state", ErrInvalidEventPatternMap, firing.rule.ID, output.ID)
		}
		if !interfaceMatchesMapAction(mapping.RangeInterface, output.Action, parameters) {
			return nil, nil, fmt.Errorf("%w: output action %s does not match the range interface", ErrInvalidEventPatternMap, output.Action)
		}
		causes := make([]gorapide.EventID, 0, len(output.Causes)+len(baseCauses))
		for _, localCause := range output.Causes {
			cause := actual[localCause]
			if cause == nil {
				return nil, nil, fmt.Errorf("%w: output %q has unavailable local cause %q", ErrInvalidEventPatternMap, output.ID, localCause)
			}
			causes = append(causes, cause.ID)
			children[localCause] = true
			for _, peer := range ruleOutputEquivalenceClass(outputByRuleID(ordered, localCause)) {
				children[peer] = true
			}
		}
		if rootClasses[output.ID] {
			causes = append(causes, baseCauses...)
		}
		causes = canonicalEventIDs(causes)
		event, err := gorapide.NewDeterministicEvent(gorapide.EventProvenance{
			Profile: CompatibilityProfile, Model: modelDigest, Instance: mapping.Name,
			Action:     output.Action,
			Occurrence: "mapping-rule=" + firing.rule.ID + "|match=" + firing.matchDigest + "|output=" + output.ID + "|domain=" + domainDigest,
			Causes:     causes,
		}, parameters)
		if err != nil {
			return nil, nil, err
		}
		if err := rangePoset.AddEventWithCause(event, causes...); err != nil {
			return nil, nil, err
		}
		actual[output.ID] = event
		records = append(records, MapGeneratedEventRecord{OutputID: output.ID, EventID: string(event.ID)})
	}
	if err := materializeRuleOutputEquivalences(ordered, actual, rangePoset); err != nil {
		return nil, nil, err
	}
	frontier := make([]gorapide.EventID, 0, len(records))
	for _, output := range ordered {
		if !children[output.ID] {
			frontier = append(frontier, actual[output.ID].ID)
		}
	}
	sort.Slice(records, func(i, j int) bool { return records[i].OutputID < records[j].OutputID })
	return records, canonicalEventIDs(frontier), nil
}

func outputByRuleID(outputs []RuleOutput, id string) RuleOutput {
	for _, output := range outputs {
		if output.ID == id {
			return output
		}
	}
	return RuleOutput{}
}

func ruleOutputEquivalenceClass(output RuleOutput) []string {
	result := append([]string{output.ID}, output.Equivalent...)
	sort.Strings(result)
	return result
}

func ruleBodyRootClasses(outputs []RuleOutput) map[string]bool {
	byID := make(map[string]RuleOutput, len(outputs))
	for _, output := range outputs {
		byID[output.ID] = output
	}
	result := make(map[string]bool, len(outputs))
	for _, output := range outputs {
		root := true
		for _, member := range ruleOutputEquivalenceClass(output) {
			if len(byID[member].Causes) != 0 {
				root = false
				break
			}
		}
		result[output.ID] = root
	}
	return result
}

func materializeRuleOutputEquivalences(outputs []RuleOutput, actual map[string]*gorapide.Event, poset *gorapide.Poset) error {
	seen := make(map[string]bool)
	for _, output := range outputs {
		members := ruleOutputEquivalenceClass(output)
		if len(members) < 2 {
			continue
		}
		key := strings.Join(members, "\x00")
		if seen[key] {
			continue
		}
		seen[key] = true
		ids := make([]gorapide.EventID, len(members))
		for index, member := range members {
			if actual[member] == nil {
				return fmt.Errorf("%w: unavailable causal-equivalence output %q", ErrInvalidEventPatternMap, member)
			}
			ids[index] = actual[member].ID
		}
		if err := poset.AddCausalEquivalenceClass(ids...); err != nil {
			return fmt.Errorf("%w: causal-equivalence class %q: %v", ErrInvalidEventPatternMap, key, err)
		}
	}
	return nil
}

func canonicalMapFiringRecords(firings []selectedMapFiring) []MapFiringRecord {
	result := make([]MapFiringRecord, 0, len(firings))
	for index, firing := range firings {
		induced := make([]string, 0, len(firing.directInduced))
		for _, predecessor := range firing.directInduced {
			induced = append(induced, firings[predecessor].firingID)
		}
		sort.Strings(induced)
		generated := append([]MapGeneratedEventRecord(nil), firing.generated...)
		sort.Slice(generated, func(i, j int) bool { return generated[i].OutputID < generated[j].OutputID })
		result = append(result, MapFiringRecord{
			FiringID: firing.firingID, RuleID: firing.rule.ID, GeneratorID: firing.generator.id,
			Match: firing.canonical, MatchDigest: firing.matchDigest,
			InducedBy: induced, Generated: generated,
		})
		_ = index
	}
	sort.Slice(result, func(i, j int) bool { return result[i].FiringID < result[j].FiringID })
	return result
}

func digestMapBytes(data []byte) string {
	return "sha256:" + digestMapHex(data)
}

func digestMapHex(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

package gorapide

import (
	"bytes"
	"container/heap"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
)

const (
	CanonicalPosetFormat               = "gorapide.canonical-poset.v1"
	CanonicalTimedPosetFormat          = "gorapide.canonical-poset.v2"
	CanonicalCausalPreorderFormat      = "gorapide.canonical-poset.v3"
	CanonicalTimedCausalPreorderFormat = "gorapide.canonical-poset.v4"
)

var ErrInvalidCanonicalPoset = errors.New("invalid canonical poset")

// CanonicalValue is the lossless, typed representation of a value in the
// deterministic value algebra. Numeric values are strings so JSON decoding
// cannot erase signedness, width normalization, or floating-point identity.
type CanonicalValue struct {
	Kind   string           `json:"kind"`
	Text   string           `json:"text,omitempty"`
	Bool   bool             `json:"bool,omitempty"`
	Items  []CanonicalValue `json:"items,omitempty"`
	Fields []CanonicalField `json:"fields,omitempty"`
}

type CanonicalField struct {
	Name  string         `json:"name"`
	Value CanonicalValue `json:"value"`
}

type CanonicalParameter struct {
	Name  string         `json:"name"`
	Value CanonicalValue `json:"value"`
}

type CanonicalObservation struct {
	Name   string               `json:"name"`
	Source string               `json:"source"`
	Params []CanonicalParameter `json:"params"`
}

// CanonicalEvent is the deterministic representation of one event occurrence.
// CausalDepth is derived from the graph and is therefore independent of event
// insertion order. It is not a replacement for Rapide's explicit clock model.
type CanonicalEvent struct {
	ID           string                 `json:"id"`
	CausalDepth  uint64                 `json:"causal_depth"`
	Observations []CanonicalObservation `json:"observations"`
	Timings      []CanonicalEventTiming `json:"timings,omitempty"`
}

// CanonicalEventTiming is one local-clock interval in canonical clock-name
// order. Ticks use canonical unsigned decimal strings so generic JSON tooling
// cannot round values above IEEE-754's exact integer range.
type CanonicalEventTiming struct {
	Clock  string `json:"clock"`
	Start  string `json:"start"`
	Finish string `json:"finish"`
}

// EncodeCanonicalEventTimings validates and losslessly encodes explicit Rapide
// local-clock intervals in canonical clock-name order.
func EncodeCanonicalEventTimings(timings []EventTiming) ([]CanonicalEventTiming, error) {
	normalized, err := CanonicalizeEventTimings(timings)
	if err != nil {
		return nil, err
	}
	result := make([]CanonicalEventTiming, len(normalized))
	for index, timing := range normalized {
		result[index] = CanonicalEventTiming{
			Clock: timing.Clock, Start: strconv.FormatUint(timing.Start, 10), Finish: strconv.FormatUint(timing.Finish, 10),
		}
	}
	return result, nil
}

// DecodeCanonicalEventTimings decodes only strictly ordered canonical clock
// intervals and rejects noncanonical decimal tick strings.
func DecodeCanonicalEventTimings(timings []CanonicalEventTiming) ([]EventTiming, error) {
	result := make([]EventTiming, len(timings))
	for index, timing := range timings {
		start, err := parseCanonicalTick(timing.Start)
		if err != nil {
			return nil, fmt.Errorf("clock %q start: %w", timing.Clock, err)
		}
		finish, err := parseCanonicalTick(timing.Finish)
		if err != nil {
			return nil, fmt.Errorf("clock %q finish: %w", timing.Clock, err)
		}
		result[index] = EventTiming{Clock: timing.Clock, Start: start, Finish: finish}
	}
	normalized, err := CanonicalizeEventTimings(result)
	if err != nil {
		return nil, err
	}
	for index := range result {
		if result[index] != normalized[index] {
			return nil, fmt.Errorf("Rapide event timings are not in canonical order")
		}
	}
	return normalized, nil
}

// CanonicalEdge is one direct causal relation.
type CanonicalEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// CanonicalCausalEquivalenceClass is one non-singleton class in Rapide's
// causal-equivalence relation. Members are strictly ordered by EventID and the
// classes are ordered by their least member.
type CanonicalCausalEquivalenceClass struct {
	Members []string `json:"members"`
}

// CanonicalPoset is the versioned, metadata-free representation used for
// semantic comparison, caching, auditing, and replay checks.
type CanonicalPoset struct {
	Format             string                            `json:"format"`
	Events             []CanonicalEvent                  `json:"events"`
	CausalEquivalences []CanonicalCausalEquivalenceClass `json:"causal_equivalences,omitempty"`
	Edges              []CanonicalEdge                   `json:"edges"`
}

// CanonicalizeParameters validates, normalizes, and losslessly encodes a
// parameter map. Results are ordered by parameter name.
func CanonicalizeParameters(params map[string]any) ([]CanonicalParameter, error) {
	params, err := canonicalParams(params)
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(params))
	for key := range params {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]CanonicalParameter, 0, len(keys))
	for _, key := range keys {
		value, err := toCanonicalValue(params[key])
		if err != nil {
			return nil, fmt.Errorf("parameter %q: %w", key, err)
		}
		result = append(result, CanonicalParameter{Name: key, Value: value})
	}
	return result, nil
}

// DecodeCanonicalParameters decodes a canonical parameter list, rejecting
// duplicate or noncanonical ordering.
func DecodeCanonicalParameters(params []CanonicalParameter) (map[string]any, error) {
	result := make(map[string]any, len(params))
	previous := ""
	for i, param := range params {
		if param.Name == "" {
			return nil, fmt.Errorf("canonical parameter %d has an empty name", i)
		}
		if i > 0 && param.Name <= previous {
			return nil, fmt.Errorf("canonical parameters are not strictly ordered at %q", param.Name)
		}
		value, err := fromCanonicalValue(param.Value)
		if err != nil {
			return nil, fmt.Errorf("parameter %q: %w", param.Name, err)
		}
		result[param.Name] = value
		previous = param.Name
	}
	return result, nil
}

// EncodeCanonicalValue validates and losslessly encodes one value in the
// deterministic Rapide value algebra. It is the single-value counterpart of
// CanonicalizeParameters and is useful for canonical model declarations whose
// denotation is itself a value rather than an event parameter map.
func EncodeCanonicalValue(value any) (CanonicalValue, error) {
	return toCanonicalValue(value)
}

// DecodeCanonicalValue decodes one canonical value and rejects encodings that
// are accepted structurally but are not the unique canonical representation of
// their decoded value.
func DecodeCanonicalValue(value CanonicalValue) (any, error) {
	decoded, err := fromCanonicalValue(value)
	if err != nil {
		return nil, err
	}
	reencoded, err := toCanonicalValue(decoded)
	if err != nil {
		return nil, err
	}
	encodedInput, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	encodedNormalized, err := json.Marshal(reencoded)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(encodedInput, encodedNormalized) {
		return nil, fmt.Errorf("canonical value is not in its unique normalized form")
	}
	return decoded, nil
}

func toCanonicalValue(value any) (CanonicalValue, error) {
	value, err := canonicalValueCopy(value)
	if err != nil {
		return CanonicalValue{}, err
	}
	switch value := value.(type) {
	case nil:
		return CanonicalValue{Kind: "null"}, nil
	case bool:
		return CanonicalValue{Kind: "bool", Bool: value}, nil
	case RapideTriv:
		return CanonicalValue{Kind: "triv"}, nil
	case string:
		return CanonicalValue{Kind: "string", Text: value}, nil
	case RapideCharacter:
		return CanonicalValue{Kind: "character", Text: strconv.FormatInt(value.code, 10)}, nil
	case RapideString:
		result := CanonicalValue{Kind: "string-codes", Items: make([]CanonicalValue, len(value.codes))}
		for index, code := range value.codes {
			result.Items[index] = CanonicalValue{Kind: "character", Text: strconv.FormatInt(code, 10)}
		}
		return result, nil
	case RapideModuleValue:
		return CanonicalValue{Kind: "module", Text: value.identity}, nil
	case RapideRecordValue:
		result := CanonicalValue{
			Kind: "record", Text: value.module.identity,
			Fields: make([]CanonicalField, len(value.fields)),
		}
		for index, field := range value.fields {
			canonical, err := toCanonicalValue(field.value)
			if err != nil {
				return CanonicalValue{}, fmt.Errorf("Record field %q: %w", field.name, err)
			}
			result.Fields[index] = CanonicalField{Name: field.name, Value: canonical}
		}
		return result, nil
	case int64:
		return CanonicalValue{Kind: "integer", Text: strconv.FormatInt(value, 10)}, nil
	case uint64:
		return CanonicalValue{Kind: "unsigned", Text: strconv.FormatUint(value, 10)}, nil
	case float64:
		return CanonicalValue{Kind: "float64", Text: fmt.Sprintf("%016x", math.Float64bits(value))}, nil
	case []byte:
		return CanonicalValue{Kind: "bytes", Text: base64.RawStdEncoding.EncodeToString(value)}, nil
	case []any:
		result := CanonicalValue{Kind: "list", Items: make([]CanonicalValue, 0, len(value))}
		for i, item := range value {
			canonical, err := toCanonicalValue(item)
			if err != nil {
				return CanonicalValue{}, fmt.Errorf("list item %d: %w", i, err)
			}
			result.Items = append(result.Items, canonical)
		}
		return result, nil
	case map[string]any:
		keys := make([]string, 0, len(value))
		for key := range value {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		result := CanonicalValue{Kind: "object", Fields: make([]CanonicalField, 0, len(keys))}
		for _, key := range keys {
			canonical, err := toCanonicalValue(value[key])
			if err != nil {
				return CanonicalValue{}, fmt.Errorf("object field %q: %w", key, err)
			}
			result.Fields = append(result.Fields, CanonicalField{Name: key, Value: canonical})
		}
		return result, nil
	default:
		return CanonicalValue{}, fmt.Errorf("%w: %T", ErrNonCanonicalValue, value)
	}
}

func fromCanonicalValue(value CanonicalValue) (any, error) {
	switch value.Kind {
	case "null":
		return nil, nil
	case "bool":
		return value.Bool, nil
	case "triv":
		return RapideUnit(), nil
	case "string":
		return value.Text, nil
	case "character":
		result, err := strconv.ParseInt(value.Text, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid Character code %q", value.Text)
		}
		return RapideCharacterFromCode(result), nil
	case "string-codes":
		codes := make([]int64, len(value.Items))
		for index, item := range value.Items {
			decoded, err := fromCanonicalValue(item)
			if err != nil {
				return nil, fmt.Errorf("String Character %d: %w", index, err)
			}
			character, ok := decoded.(RapideCharacter)
			if !ok {
				return nil, fmt.Errorf("String item %d is not a Character", index)
			}
			codes[index] = character.code
		}
		return RapideStringFromCodes(codes...), nil
	case "module":
		result, err := ParseRapideModuleValue(value.Text)
		if err != nil {
			return nil, err
		}
		return result, nil
	case "record":
		module, err := ParseRapideModuleValue(value.Text)
		if err != nil {
			return nil, fmt.Errorf("Record allocation: %w", err)
		}
		fields := make([]rapideRecordValueField, len(value.Fields))
		previous := ""
		for index, field := range value.Fields {
			canonical, nameErr := canonicalRapideIdentifier(field.Name)
			if nameErr != nil || canonical != field.Name || (index > 0 && field.Name <= previous) {
				return nil, fmt.Errorf("Record fields are invalid, noncanonical, duplicate, or unordered at %q", field.Name)
			}
			decoded, err := fromCanonicalValue(field.Value)
			if err != nil {
				return nil, fmt.Errorf("Record field %q: %w", field.Name, err)
			}
			fields[index] = rapideRecordValueField{name: field.Name, value: decoded}
			previous = field.Name
		}
		return rapideRecordValueFromCanonical(module, fields)
	case "integer":
		result, err := strconv.ParseInt(value.Text, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid integer %q", value.Text)
		}
		return result, nil
	case "unsigned":
		result, err := strconv.ParseUint(value.Text, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid unsigned integer %q", value.Text)
		}
		return result, nil
	case "float64":
		bits, err := strconv.ParseUint(value.Text, 16, 64)
		if err != nil || len(value.Text) != 16 {
			return nil, fmt.Errorf("invalid float64 bits %q", value.Text)
		}
		result := math.Float64frombits(bits)
		if math.IsNaN(result) || math.IsInf(result, 0) {
			return nil, fmt.Errorf("invalid non-finite float64")
		}
		return result, nil
	case "bytes":
		result, err := base64.RawStdEncoding.DecodeString(value.Text)
		if err != nil {
			return nil, fmt.Errorf("invalid base64 bytes: %w", err)
		}
		return result, nil
	case "list":
		result := make([]any, len(value.Items))
		for i, item := range value.Items {
			decoded, err := fromCanonicalValue(item)
			if err != nil {
				return nil, fmt.Errorf("list item %d: %w", i, err)
			}
			result[i] = decoded
		}
		return result, nil
	case "object":
		result := make(map[string]any, len(value.Fields))
		previous := ""
		for i, field := range value.Fields {
			if field.Name == "" || (i > 0 && field.Name <= previous) {
				return nil, fmt.Errorf("object fields are empty, duplicate, or unordered at %q", field.Name)
			}
			decoded, err := fromCanonicalValue(field.Value)
			if err != nil {
				return nil, fmt.Errorf("object field %q: %w", field.Name, err)
			}
			result[field.Name] = decoded
			previous = field.Name
		}
		return result, nil
	default:
		return nil, fmt.Errorf("unknown canonical value kind %q", value.Kind)
	}
}

// Canonical returns a deterministic representation of the poset. It rejects
// parameter values outside the deterministic value algebra.
func (p *Poset) Canonical() (CanonicalPoset, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	depths, err := p.causalDepthsLocked()
	if err != nil {
		return CanonicalPoset{}, err
	}

	ids := make([]EventID, 0, len(p.events))
	for id := range p.events {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	result := CanonicalPoset{
		Format: CanonicalPosetFormat,
		Events: make([]CanonicalEvent, 0, len(ids)),
	}
	for _, class := range p.nontrivialCausalClassesLocked() {
		canonicalClass := CanonicalCausalEquivalenceClass{Members: make([]string, len(class))}
		for index, member := range class {
			canonicalClass.Members[index] = string(member)
		}
		result.CausalEquivalences = append(result.CausalEquivalences, canonicalClass)
	}
	timed := false
	for _, id := range ids {
		event := p.events[id]
		observations := event.EventObservations()
		canonicalObservations := make([]CanonicalObservation, 0, len(observations))
		for _, observation := range observations {
			params, err := CanonicalizeParameters(observation.Params)
			if err != nil {
				return CanonicalPoset{}, fmt.Errorf("canonical event %s observation %s.%s: %w",
					id, observation.Source, observation.Name, err)
			}
			canonicalObservations = append(canonicalObservations, CanonicalObservation{
				Name: observation.Name, Source: observation.Source, Params: params,
			})
		}
		canonicalTimings, err := EncodeCanonicalEventTimings(event.Timings)
		if err != nil {
			return CanonicalPoset{}, fmt.Errorf("canonical event %s timing: %w", id, err)
		}
		if len(canonicalTimings) != 0 {
			timed = true
		}
		result.Events = append(result.Events, CanonicalEvent{
			ID: idString(id), CausalDepth: depths[id], Observations: canonicalObservations,
			Timings: canonicalTimings,
		})
	}
	if len(result.CausalEquivalences) != 0 && timed {
		result.Format = CanonicalTimedCausalPreorderFormat
	} else if len(result.CausalEquivalences) != 0 {
		result.Format = CanonicalCausalPreorderFormat
	} else if timed {
		result.Format = CanonicalTimedPosetFormat
	}

	edgeKeys := make(map[string]CanonicalEdge)
	for from, successors := range p.causalEdges {
		from = p.causalRepresentativeLocked(from)
		for to := range successors {
			to = p.causalRepresentativeLocked(to)
			if from == to {
				continue
			}
			edge := CanonicalEdge{From: idString(from), To: idString(to)}
			edgeKeys[edge.From+"\x00"+edge.To] = edge
		}
	}
	orderedEdgeKeys := make([]string, 0, len(edgeKeys))
	for key := range edgeKeys {
		orderedEdgeKeys = append(orderedEdgeKeys, key)
	}
	sort.Strings(orderedEdgeKeys)
	for _, key := range orderedEdgeKeys {
		result.Edges = append(result.Edges, edgeKeys[key])
	}

	return result, nil
}

func idString(id EventID) string { return string(id) }

// MarshalCanonical returns byte-identical, typed JSON for semantically
// identical posets constructed from stable event identities.
func (p *Poset) MarshalCanonical() ([]byte, error) {
	canonical, err := p.Canonical()
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(canonical)
	if err != nil {
		return nil, fmt.Errorf("gorapide.Poset.MarshalCanonical: %w", err)
	}
	return data, nil
}

// ParseCanonicalPoset parses only the exact canonical encoding. Whitespace,
// unknown fields, duplicate JSON keys, unsorted elements, inconsistent depth,
// malformed values or timings, timing/causal conflicts, cycles, and dangling
// edges are rejected.
func ParseCanonicalPoset(data []byte) (*Poset, error) {
	var canonical CanonicalPoset
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&canonical); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidCanonicalPoset, err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidCanonicalPoset, err)
	}
	reencoded, err := json.Marshal(canonical)
	if err != nil || !bytes.Equal(reencoded, data) {
		return nil, fmt.Errorf("%w: input is not the canonical byte encoding", ErrInvalidCanonicalPoset)
	}
	if canonical.Format != CanonicalPosetFormat && canonical.Format != CanonicalTimedPosetFormat &&
		canonical.Format != CanonicalCausalPreorderFormat && canonical.Format != CanonicalTimedCausalPreorderFormat {
		return nil, fmt.Errorf("%w: format %q", ErrInvalidCanonicalPoset, canonical.Format)
	}
	causalPreorder := canonical.Format == CanonicalCausalPreorderFormat ||
		canonical.Format == CanonicalTimedCausalPreorderFormat
	timedFormat := canonical.Format == CanonicalTimedPosetFormat ||
		canonical.Format == CanonicalTimedCausalPreorderFormat
	if causalPreorder != (len(canonical.CausalEquivalences) != 0) {
		return nil, fmt.Errorf("%w: format %q and causal-equivalence content disagree", ErrInvalidCanonicalPoset, canonical.Format)
	}

	poset := NewPoset()
	previousID := ""
	for i, canonicalEvent := range canonical.Events {
		if canonicalEvent.ID == "" || (i > 0 && canonicalEvent.ID <= previousID) {
			return nil, fmt.Errorf("%w: event IDs are empty, duplicate, or unordered at %q", ErrInvalidCanonicalPoset, canonicalEvent.ID)
		}
		if len(canonicalEvent.Observations) == 0 {
			return nil, fmt.Errorf("%w: event %q has no observations", ErrInvalidCanonicalPoset, canonicalEvent.ID)
		}
		observations := make([]EventObservation, 0, len(canonicalEvent.Observations))
		previousObservation := ""
		for j, observation := range canonicalEvent.Observations {
			key := observation.Source + "\x00" + observation.Name
			if observation.Name == "" || (j > 0 && key <= previousObservation) {
				return nil, fmt.Errorf("%w: observations for event %q are empty, duplicate, or unordered", ErrInvalidCanonicalPoset, canonicalEvent.ID)
			}
			params, err := DecodeCanonicalParameters(observation.Params)
			if err != nil {
				return nil, fmt.Errorf("%w: event %q observation %q: %v", ErrInvalidCanonicalPoset, canonicalEvent.ID, key, err)
			}
			observations = append(observations, EventObservation{
				Name: observation.Name, Source: observation.Source, Params: params,
			})
			previousObservation = key
		}
		primary := observations[0]
		normalizedTimings, err := DecodeCanonicalEventTimings(canonicalEvent.Timings)
		if err != nil {
			return nil, fmt.Errorf("%w: event %q timing: %v", ErrInvalidCanonicalPoset, canonicalEvent.ID, err)
		}
		if !timedFormat && len(normalizedTimings) != 0 {
			return nil, fmt.Errorf("%w: untimed canonical format contains event timing", ErrInvalidCanonicalPoset)
		}
		event := &Event{
			ID: EventID(canonicalEvent.ID), Name: primary.Name, Source: primary.Source,
			Params: copyParams(primary.Params), Observations: observations, Timings: normalizedTimings,
			deterministic: true,
		}
		if err := poset.AddEvent(event); err != nil {
			return nil, fmt.Errorf("%w: event %q: %v", ErrInvalidCanonicalPoset, canonicalEvent.ID, err)
		}
		previousID = canonicalEvent.ID
	}

	seenEquivalent := make(map[string]bool)
	previousClass := ""
	for classIndex, class := range canonical.CausalEquivalences {
		if len(class.Members) < 2 {
			return nil, fmt.Errorf("%w: causal-equivalence class %d has fewer than two members", ErrInvalidCanonicalPoset, classIndex)
		}
		for memberIndex, member := range class.Members {
			if member == "" || (memberIndex > 0 && member <= class.Members[memberIndex-1]) {
				return nil, fmt.Errorf("%w: causal-equivalence class %d members are empty, duplicate, or unordered", ErrInvalidCanonicalPoset, classIndex)
			}
			if _, exists := poset.events[EventID(member)]; !exists {
				return nil, fmt.Errorf("%w: causal-equivalence member %q is not an event", ErrInvalidCanonicalPoset, member)
			}
			if seenEquivalent[member] {
				return nil, fmt.Errorf("%w: event %q occurs in multiple causal-equivalence classes", ErrInvalidCanonicalPoset, member)
			}
			seenEquivalent[member] = true
		}
		if classIndex > 0 && class.Members[0] <= previousClass {
			return nil, fmt.Errorf("%w: causal-equivalence classes are not strictly ordered", ErrInvalidCanonicalPoset)
		}
		members := make([]EventID, len(class.Members))
		for index, member := range class.Members {
			members[index] = EventID(member)
		}
		if err := poset.AddCausalEquivalenceClass(members...); err != nil {
			return nil, fmt.Errorf("%w: causal-equivalence class %d: %v", ErrInvalidCanonicalPoset, classIndex, err)
		}
		previousClass = class.Members[0]
	}

	previousEdge := ""
	for i, edge := range canonical.Edges {
		key := edge.From + "\x00" + edge.To
		if edge.From == "" || edge.To == "" || (i > 0 && key <= previousEdge) {
			return nil, fmt.Errorf("%w: edges are empty, duplicate, or unordered", ErrInvalidCanonicalPoset)
		}
		if err := poset.AddCausal(EventID(edge.From), EventID(edge.To)); err != nil {
			return nil, fmt.Errorf("%w: edge %s -> %s: %v", ErrInvalidCanonicalPoset, edge.From, edge.To, err)
		}
		previousEdge = key
	}

	roundTrip, err := poset.MarshalCanonical()
	if err != nil || !bytes.Equal(roundTrip, data) {
		return nil, fmt.Errorf("%w: encoded depth or graph semantics do not match content", ErrInvalidCanonicalPoset)
	}
	return poset, nil
}

func parseCanonicalTick(value string) (uint64, error) {
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil || strconv.FormatUint(parsed, 10) != value {
		return 0, fmt.Errorf("invalid canonical Natural tick %q", value)
	}
	return parsed, nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple JSON values")
		}
		return err
	}
	return nil
}

// SemanticDigest returns a versioned SHA-256 digest of MarshalCanonical.
func (p *Poset) SemanticDigest() (string, error) {
	data, err := p.MarshalCanonical()
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func (p *Poset) causalDepthsLocked() (map[EventID]uint64, error) {
	representatives := p.causalClassRepresentativesLocked()
	inDegree := make(map[EventID]int, len(representatives))
	for _, representative := range representatives {
		inDegree[representative] = len(p.causalClassPredecessorsLocked(representative))
	}

	// The ready set is a min-heap keyed by EventID: each step visits the
	// lexicographically least ready representative, exactly the order the
	// previous sort-after-every-pop implementation produced, at O(log n) per
	// operation instead of O(n log n). Longest-path depths are independent of
	// the visiting order in any case; the heap only preserves the traversal.
	ready := &eventIDMinHeap{}
	for _, id := range representatives {
		if inDegree[id] == 0 {
			*ready = append(*ready, id)
		}
	}
	heap.Init(ready)

	classDepths := make(map[EventID]uint64, len(representatives))
	visited := 0
	for ready.Len() > 0 {
		current := heap.Pop(ready).(EventID)
		visited++

		depth := uint64(1)
		for _, predecessor := range p.causalClassPredecessorsLocked(current) {
			if candidate := classDepths[predecessor] + 1; candidate > depth {
				depth = candidate
			}
		}
		classDepths[current] = depth

		for _, successor := range p.causalClassSuccessorsLocked(current) {
			inDegree[successor]--
			if inDegree[successor] == 0 {
				heap.Push(ready, successor)
			}
		}
	}
	if visited != len(representatives) {
		return nil, fmt.Errorf("gorapide.Poset.Canonical: causal graph contains a cycle")
	}
	depths := make(map[EventID]uint64, len(p.events))
	for id := range p.events {
		depths[id] = classDepths[p.causalRepresentativeLocked(id)]
	}
	return depths, nil
}

// eventIDMinHeap orders event IDs lexicographically for causalDepthsLocked.
type eventIDMinHeap []EventID

func (h eventIDMinHeap) Len() int           { return len(h) }
func (h eventIDMinHeap) Less(i, j int) bool { return h[i] < h[j] }
func (h eventIDMinHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *eventIDMinHeap) Push(x any)        { *h = append(*h, x.(EventID)) }
func (h *eventIDMinHeap) Pop() any {
	old := *h
	last := old[len(old)-1]
	*h = old[:len(old)-1]
	return last
}

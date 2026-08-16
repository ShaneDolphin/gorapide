package gorapide

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"sort"
	"unicode/utf8"
)

var ErrNonCanonicalValue = errors.New("value is not in the deterministic value algebra")

// EventProvenance contains the semantic coordinates required to distinguish
// an event occurrence without entropy, wall time, memory addresses, or
// discovery order.
type EventProvenance struct {
	Profile  string
	Model    string
	Instance string
	// ObservationSource is the source qualifier of the occurrence's initial
	// action observation when that language-level name differs from Instance.
	// Instance remains the allocation identity used to derive the occurrence
	// identity. Like an observation added by a basic connection, this qualified
	// view does not create a second occurrence.
	ObservationSource string
	Action            string
	Occurrence        string
	Causes            []EventID
	Timings           []EventTiming
}

// NewDeterministicEvent constructs an event with a content-derived ID. Optional
// Rapide local-clock intervals are explicit provenance; the legacy wall clock
// remains zero. Add the event to a poset with the same direct causes supplied in
// provenance.
func NewDeterministicEvent(provenance EventProvenance, params map[string]any) (*Event, error) {
	if provenance.Profile == "" {
		return nil, fmt.Errorf("gorapide.NewDeterministicEvent: profile is empty")
	}
	if provenance.Instance == "" {
		return nil, fmt.Errorf("gorapide.NewDeterministicEvent: instance is empty")
	}
	if provenance.Action == "" {
		return nil, fmt.Errorf("gorapide.NewDeterministicEvent: action is empty")
	}
	if provenance.Occurrence == "" {
		return nil, fmt.Errorf("gorapide.NewDeterministicEvent: occurrence is empty")
	}

	canonicalParams, err := canonicalParams(params)
	if err != nil {
		return nil, fmt.Errorf("gorapide.NewDeterministicEvent: %w", err)
	}
	causes, err := normalizeCauses(provenance.Causes)
	if err != nil {
		return nil, fmt.Errorf("gorapide.NewDeterministicEvent: %w", err)
	}
	provenance.Causes = causes
	timings, err := CanonicalizeEventTimings(provenance.Timings)
	if err != nil {
		return nil, fmt.Errorf("gorapide.NewDeterministicEvent: %w", err)
	}
	provenance.Timings = timings

	id, err := deriveEventID(provenance, canonicalParams)
	if err != nil {
		return nil, fmt.Errorf("gorapide.NewDeterministicEvent: %w", err)
	}

	observationSource := provenance.ObservationSource
	if observationSource == "" {
		observationSource = provenance.Instance
	}
	return &Event{
		ID:     id,
		Name:   provenance.Action,
		Source: observationSource,
		Params: canonicalParams,
		Observations: []EventObservation{{
			Name: provenance.Action, Source: observationSource,
			Params: copyParams(canonicalParams),
		}},
		Timings:        append([]EventTiming(nil), timings...),
		expectedCauses: append([]EventID(nil), causes...),
		deterministic:  true,
	}, nil
}

func deriveEventID(provenance EventProvenance, params map[string]any) (EventID, error) {
	if len(provenance.Timings) != 0 {
		return deriveTimedEventID(provenance, params)
	}
	var buffer bytes.Buffer
	buffer.WriteString("gorapide:event:v1")
	writeCanonicalString(&buffer, provenance.Profile)
	writeCanonicalString(&buffer, provenance.Model)
	writeCanonicalString(&buffer, provenance.Instance)
	writeCanonicalString(&buffer, provenance.Action)
	writeCanonicalString(&buffer, provenance.Occurrence)

	causes, err := normalizeCauses(provenance.Causes)
	if err != nil {
		return "", err
	}
	writeCanonicalUint64(&buffer, uint64(len(causes)))
	for _, cause := range causes {
		writeCanonicalString(&buffer, string(cause))
	}
	if err := writeCanonicalValue(&buffer, params); err != nil {
		return "", err
	}

	digest := sha256.Sum256(buffer.Bytes())
	return EventID("evt1-" + hex.EncodeToString(digest[:])), nil
}

func deriveTimedEventID(provenance EventProvenance, params map[string]any) (EventID, error) {
	var buffer bytes.Buffer
	buffer.WriteString("gorapide:event:v2")
	writeCanonicalString(&buffer, provenance.Profile)
	writeCanonicalString(&buffer, provenance.Model)
	writeCanonicalString(&buffer, provenance.Instance)
	writeCanonicalString(&buffer, provenance.Action)
	writeCanonicalString(&buffer, provenance.Occurrence)

	causes, err := normalizeCauses(provenance.Causes)
	if err != nil {
		return "", err
	}
	writeCanonicalUint64(&buffer, uint64(len(causes)))
	for _, cause := range causes {
		writeCanonicalString(&buffer, string(cause))
	}
	timings, err := CanonicalizeEventTimings(provenance.Timings)
	if err != nil {
		return "", err
	}
	writeCanonicalUint64(&buffer, uint64(len(timings)))
	for _, timing := range timings {
		writeCanonicalString(&buffer, timing.Clock)
		writeCanonicalUint64(&buffer, timing.Start)
		writeCanonicalUint64(&buffer, timing.Finish)
	}
	if err := writeCanonicalValue(&buffer, params); err != nil {
		return "", err
	}
	digest := sha256.Sum256(buffer.Bytes())
	return EventID("evt2-" + hex.EncodeToString(digest[:])), nil
}

func canonicalParams(params map[string]any) (map[string]any, error) {
	if params == nil {
		return map[string]any{}, nil
	}
	result := make(map[string]any, len(params))
	for key, value := range params {
		if !utf8.ValidString(key) {
			return nil, fmt.Errorf("%w: parameter name is not valid UTF-8", ErrNonCanonicalValue)
		}
		canonical, err := canonicalValueCopy(value)
		if err != nil {
			return nil, fmt.Errorf("parameter %q: %w", key, err)
		}
		result[key] = canonical
	}
	return result, nil
}

// CanonicalizeParams validates and deeply copies parameters into the initial
// deterministic value algebra. Numeric Go widths are normalized so host
// architecture does not change canonical JSON or event identity.
func CanonicalizeParams(params map[string]any) (map[string]any, error) {
	return canonicalParams(params)
}

func canonicalValueCopy(value any) (any, error) {
	switch value := value.(type) {
	case nil, bool, RapideTriv:
		return value, nil
	case string:
		if !utf8.ValidString(value) {
			return nil, fmt.Errorf("%w: string is not valid UTF-8", ErrNonCanonicalValue)
		}
		return value, nil
	case RapideCharacter:
		return value, nil
	case RapideString:
		return canonicalRapideString(value), nil
	case RapideModuleValue:
		return canonicalRapideModuleValue(value)
	case RapideRecordValue:
		return canonicalRapideRecordValue(value)
	case int:
		return int64(value), nil
	case int8:
		return int64(value), nil
	case int16:
		return int64(value), nil
	case int32:
		return int64(value), nil
	case int64:
		return value, nil
	case uint:
		return uint64(value), nil
	case uint8:
		return uint64(value), nil
	case uint16:
		return uint64(value), nil
	case uint32:
		return uint64(value), nil
	case uint64:
		return value, nil
	case float32:
		return canonicalFloat64(float64(value))
	case float64:
		return canonicalFloat64(value)
	case []byte:
		return append([]byte(nil), value...), nil
	case []any:
		result := make([]any, len(value))
		for i, item := range value {
			canonical, err := canonicalValueCopy(item)
			if err != nil {
				return nil, fmt.Errorf("list item %d: %w", i, err)
			}
			result[i] = canonical
		}
		return result, nil
	case map[string]any:
		return canonicalParams(value)
	default:
		return nil, fmt.Errorf("%w: %T", ErrNonCanonicalValue, value)
	}
}

func canonicalFloat64(value float64) (float64, error) {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, fmt.Errorf("%w: non-finite float", ErrNonCanonicalValue)
	}
	if value == 0 {
		return 0, nil
	}
	return value, nil
}

func normalizeCauses(causes []EventID) ([]EventID, error) {
	result := append([]EventID(nil), causes...)
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	write := 0
	for _, cause := range result {
		if cause == "" {
			return nil, fmt.Errorf("deterministic cause ID is empty")
		}
		if write > 0 && result[write-1] == cause {
			continue
		}
		result[write] = cause
		write++
	}
	return result[:write], nil
}

func writeCanonicalValue(buffer *bytes.Buffer, value any) error {
	switch value := value.(type) {
	case nil:
		buffer.WriteByte('0')
	case bool:
		buffer.WriteByte('b')
		if value {
			buffer.WriteByte(1)
		} else {
			buffer.WriteByte(0)
		}
	case RapideTriv:
		buffer.WriteByte('t')
	case string:
		buffer.WriteByte('s')
		writeCanonicalString(buffer, value)
	case RapideCharacter:
		buffer.WriteByte('c')
		writeCanonicalUint64(buffer, uint64(value.code))
	case RapideString:
		buffer.WriteByte('S')
		writeCanonicalUint64(buffer, uint64(len(value.codes)))
		for _, code := range value.codes {
			writeCanonicalUint64(buffer, uint64(code))
		}
	case RapideModuleValue:
		canonical, err := canonicalRapideModuleValue(value)
		if err != nil {
			return err
		}
		buffer.WriteByte('o')
		writeCanonicalString(buffer, canonical.identity)
	case RapideRecordValue:
		canonical, err := canonicalRapideRecordValue(value)
		if err != nil {
			return err
		}
		buffer.WriteByte('r')
		writeCanonicalString(buffer, canonical.module.identity)
		writeCanonicalUint64(buffer, uint64(len(canonical.fields)))
		for _, field := range canonical.fields {
			writeCanonicalString(buffer, field.name)
			if err := writeCanonicalValue(buffer, field.value); err != nil {
				return fmt.Errorf("Record field %q: %w", field.name, err)
			}
		}
	case int:
		writeCanonicalInt64(buffer, int64(value))
	case int8:
		writeCanonicalInt64(buffer, int64(value))
	case int16:
		writeCanonicalInt64(buffer, int64(value))
	case int32:
		writeCanonicalInt64(buffer, int64(value))
	case int64:
		writeCanonicalInt64(buffer, value)
	case uint:
		writeCanonicalUnsigned(buffer, uint64(value))
	case uint8:
		writeCanonicalUnsigned(buffer, uint64(value))
	case uint16:
		writeCanonicalUnsigned(buffer, uint64(value))
	case uint32:
		writeCanonicalUnsigned(buffer, uint64(value))
	case uint64:
		writeCanonicalUnsigned(buffer, value)
	case float32:
		return writeCanonicalFloat64(buffer, float64(value))
	case float64:
		return writeCanonicalFloat64(buffer, value)
	case []byte:
		buffer.WriteByte('x')
		writeCanonicalUint64(buffer, uint64(len(value)))
		buffer.Write(value)
	case []any:
		buffer.WriteByte('l')
		writeCanonicalUint64(buffer, uint64(len(value)))
		for _, item := range value {
			if err := writeCanonicalValue(buffer, item); err != nil {
				return err
			}
		}
	case map[string]any:
		buffer.WriteByte('m')
		keys := make([]string, 0, len(value))
		for key := range value {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		writeCanonicalUint64(buffer, uint64(len(keys)))
		for _, key := range keys {
			writeCanonicalString(buffer, key)
			if err := writeCanonicalValue(buffer, value[key]); err != nil {
				return fmt.Errorf("map key %q: %w", key, err)
			}
		}
	default:
		return fmt.Errorf("%w: %T", ErrNonCanonicalValue, value)
	}
	return nil
}

func writeCanonicalString(buffer *bytes.Buffer, value string) {
	writeCanonicalUint64(buffer, uint64(len(value)))
	buffer.WriteString(value)
}

func writeCanonicalInt64(buffer *bytes.Buffer, value int64) {
	buffer.WriteByte('i')
	writeCanonicalUint64(buffer, uint64(value))
}

func writeCanonicalUnsigned(buffer *bytes.Buffer, value uint64) {
	buffer.WriteByte('u')
	writeCanonicalUint64(buffer, value)
}

func writeCanonicalFloat64(buffer *bytes.Buffer, value float64) error {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return fmt.Errorf("%w: non-finite float", ErrNonCanonicalValue)
	}
	if value == 0 {
		value = 0 // canonicalize negative zero
	}
	buffer.WriteByte('f')
	writeCanonicalUint64(buffer, math.Float64bits(value))
	return nil
}

func writeCanonicalUint64(buffer *bytes.Buffer, value uint64) {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], value)
	buffer.Write(encoded[:])
}

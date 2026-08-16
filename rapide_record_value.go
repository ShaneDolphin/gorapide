package gorapide

import (
	"errors"
	"fmt"
	"sort"
)

// ErrInvalidRapideRecordValue identifies malformed Record-literal modules.
var ErrInvalidRapideRecordValue = errors.New("invalid Rapide Record value")

// RapideRecordField is one field initializer of a Record literal. Fields are
// private so identifier normalization and deep canonical copying cannot be
// bypassed.
type RapideRecordField struct {
	name  string
	value any
}

// RapideRecordObjectField constructs one named Record field initializer.
func RapideRecordObjectField(name string, value any) RapideRecordField {
	return RapideRecordField{name: name, value: value}
}

type rapideRecordValueField struct {
	name  string
	value any
}

func normalizeRapideRecordFields(fields []RapideRecordField) ([]rapideRecordValueField, error) {
	normalized := make([]rapideRecordValueField, len(fields))
	seen := make(map[string]bool, len(fields))
	for index, field := range fields {
		name, err := canonicalRapideIdentifier(field.name)
		if err != nil {
			return nil, fmt.Errorf("field %d: %v", index, err)
		}
		if seen[name] {
			return nil, fmt.Errorf("duplicate field %q", name)
		}
		seen[name] = true
		value, err := canonicalValueCopy(field.value)
		if err != nil {
			return nil, fmt.Errorf("field %q: %v", name, err)
		}
		normalized[index] = rapideRecordValueField{name: name, value: value}
	}
	sort.Slice(normalized, func(left, right int) bool {
		return normalized[left].name < normalized[right].name
	})
	return normalized, nil
}

// RapideRecordValue is the immutable module produced by evaluating one Record
// literal. Identity is the literal evaluation's module allocation identity;
// fields are a canonical case-folded, name-sorted object table. The Predefined
// Types LRM defines selection but leaves Record equality unresolved, so this
// type deliberately supplies identity and selection without a user "=".
type RapideRecordValue struct {
	module RapideModuleValue
	fields []rapideRecordValueField
}

// NewRapideRecordValue constructs the value of a Record literal allocation at
// the given explicit provenance. Field order and identifier case do not alter
// the result. Every field value is copied into the canonical value algebra.
// An execution frontend remains responsible for generating the allocation's
// observable module Start occurrence before publishing this value.
func NewRapideRecordValue(
	provenance ModuleAllocationProvenance,
	fields ...RapideRecordField,
) (RapideRecordValue, error) {
	normalized, err := normalizeRapideRecordFields(fields)
	if err != nil {
		return RapideRecordValue{}, fmt.Errorf("%w: %v", ErrInvalidRapideRecordValue, err)
	}
	module, err := NewRapideModuleValue(provenance)
	if err != nil {
		return RapideRecordValue{}, fmt.Errorf("%w: allocation: %v", ErrInvalidRapideRecordValue, err)
	}
	return RapideRecordValue{module: module, fields: normalized}, nil
}

// RapideRecordObjectDeclaration is the canonical, allocation-free model data
// for one immutable module object initialized by a Record literal. Evaluation
// supplies allocation provenance later; the declaration itself never fabricates
// a module identity.
type RapideRecordObjectDeclaration struct {
	name   string
	typ    RapideType
	fields []rapideRecordValueField
}

// NewRapideRecordObjectDeclaration validates a context-typed Record literal
// without allocating it. The declared structural type and canonical field
// values are sufficient to prove membership; allocation remains per execution.
func NewRapideRecordObjectDeclaration(
	name string,
	typ RapideType,
	fields ...RapideRecordField,
) (RapideRecordObjectDeclaration, error) {
	canonical, err := canonicalRapideIdentifier(name)
	if err != nil {
		return RapideRecordObjectDeclaration{}, fmt.Errorf("%w: object declaration: %v", ErrInvalidRapideRecordValue, err)
	}
	if typ.node == nil || typ.node.kind != rapideInterfaceType {
		return RapideRecordObjectDeclaration{}, fmt.Errorf("%w: object declaration %q type is not an interface", ErrInvalidRapideRecordValue, name)
	}
	if err := validateRapideTypeReferences(typ.node, nil); err != nil {
		return RapideRecordObjectDeclaration{}, fmt.Errorf("%w: object declaration %q type: %v", ErrInvalidRapideRecordValue, name, err)
	}
	normalized, err := normalizeRapideRecordFields(fields)
	if err != nil {
		return RapideRecordObjectDeclaration{}, fmt.Errorf("%w: object declaration %q: %v", ErrInvalidRapideRecordValue, name, err)
	}
	probe := RapideRecordValue{fields: normalized}
	if err := validateRapideRecordValueMembership(probe, typ.node); err != nil {
		return RapideRecordObjectDeclaration{}, fmt.Errorf("%w: object declaration %q: %v", ErrInvalidRapideRecordValue, name, err)
	}
	return RapideRecordObjectDeclaration{name: canonical, typ: typ, fields: normalized}, nil
}

// Name returns the canonical case-folded object identifier.
func (declaration RapideRecordObjectDeclaration) Name() string { return declaration.name }

// Type returns the exact immutable structural type paired with the object.
func (declaration RapideRecordObjectDeclaration) Type() RapideType { return declaration.typ }

// FieldNames returns the canonical field table order.
func (declaration RapideRecordObjectDeclaration) FieldNames() []string {
	result := make([]string, len(declaration.fields))
	for index, field := range declaration.fields {
		result[index] = field.name
	}
	return result
}

// Field returns one declaration-time field value as a defensive canonical copy.
func (declaration RapideRecordObjectDeclaration) Field(name string) (any, bool, error) {
	return (RapideRecordValue{fields: declaration.fields}).Field(name)
}

// Allocate evaluates this declaration as a fresh Record module at the supplied
// deterministic program point.
func (declaration RapideRecordObjectDeclaration) Allocate(
	provenance ModuleAllocationProvenance,
) (RapideRecordValue, error) {
	fields := make([]RapideRecordField, len(declaration.fields))
	for index, field := range declaration.fields {
		fields[index] = RapideRecordObjectField(field.name, field.value)
	}
	return NewRapideRecordValue(provenance, fields...)
}

// NormalizeRapideRecordObjectDeclarations validates duplicates while retaining
// declaration order, which is semantic when successive literal evaluations
// generate successive module Start occurrences.
func NormalizeRapideRecordObjectDeclarations(
	declarations ...RapideRecordObjectDeclaration,
) ([]RapideRecordObjectDeclaration, error) {
	result := make([]RapideRecordObjectDeclaration, len(declarations))
	seen := make(map[string]bool, len(declarations))
	for index, declaration := range declarations {
		fields := make([]RapideRecordField, len(declaration.fields))
		for fieldIndex, field := range declaration.fields {
			fields[fieldIndex] = RapideRecordObjectField(field.name, field.value)
		}
		normalized, err := NewRapideRecordObjectDeclaration(declaration.name, declaration.typ, fields...)
		if err != nil {
			return nil, err
		}
		if seen[normalized.name] {
			return nil, fmt.Errorf("%w: duplicate Record object declaration %q", ErrInvalidRapideRecordValue, normalized.name)
		}
		seen[normalized.name] = true
		result[index] = normalized
	}
	return result, nil
}

// Identity returns the Record literal module's replay-stable allocation ID.
func (value RapideRecordValue) Identity() string { return value.module.Identity() }

// Module returns the generic allocation identity of this Record module.
func (value RapideRecordValue) Module() RapideModuleValue { return value.module }

// SameRapideRecord compares observable module-allocation identity. This is not
// the unresolved user-defined Record equality operation.
func SameRapideRecord(left, right RapideRecordValue) bool {
	return SameRapideModule(left.module, right.module)
}

// FieldNames returns canonical field identifiers in deterministic order.
func (value RapideRecordValue) FieldNames() []string {
	result := make([]string, len(value.fields))
	for index, field := range value.fields {
		result[index] = field.name
	}
	return result
}

// Field selects one field case-insensitively and returns a defensive canonical
// copy. A missing field is reported by ok=false without fabricating a value.
func (value RapideRecordValue) Field(name string) (result any, ok bool, err error) {
	canonical, err := canonicalRapideIdentifier(name)
	if err != nil {
		return nil, false, fmt.Errorf("%w: field selection: %v", ErrInvalidRapideRecordValue, err)
	}
	index := sort.Search(len(value.fields), func(index int) bool {
		return value.fields[index].name >= canonical
	})
	if index == len(value.fields) || value.fields[index].name != canonical {
		return nil, false, nil
	}
	result, err = canonicalValueCopy(value.fields[index].value)
	if err != nil {
		return nil, false, fmt.Errorf("%w: stored field %q: %v", ErrInvalidRapideRecordValue, canonical, err)
	}
	return result, true, nil
}

func canonicalRapideRecordValue(value RapideRecordValue) (RapideRecordValue, error) {
	module, err := canonicalRapideModuleValue(value.module)
	if err != nil {
		return RapideRecordValue{}, fmt.Errorf("%w: allocation: %v", ErrInvalidRapideRecordValue, err)
	}
	fields := make([]rapideRecordValueField, len(value.fields))
	previous := ""
	for index, field := range value.fields {
		name, err := canonicalRapideIdentifier(field.name)
		if err != nil || name != field.name || (index > 0 && name <= previous) {
			return RapideRecordValue{}, fmt.Errorf(
				"%w: fields are invalid, noncanonical, duplicate, or unordered at %q",
				ErrInvalidRapideRecordValue, field.name,
			)
		}
		canonical, err := canonicalValueCopy(field.value)
		if err != nil {
			return RapideRecordValue{}, fmt.Errorf("%w: field %q: %v", ErrInvalidRapideRecordValue, name, err)
		}
		fields[index] = rapideRecordValueField{name: name, value: canonical}
		previous = name
	}
	return RapideRecordValue{module: module, fields: fields}, nil
}

func rapideRecordValueFromCanonical(
	module RapideModuleValue,
	fields []rapideRecordValueField,
) (RapideRecordValue, error) {
	return canonicalRapideRecordValue(RapideRecordValue{module: module, fields: fields})
}

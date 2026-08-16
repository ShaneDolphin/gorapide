package gorapide

import (
	"errors"
	"fmt"
	"sort"
)

// ErrInvalidRapideMembership identifies a malformed or unsatisfied structural
// module-membership obligation.
var ErrInvalidRapideMembership = errors.New("invalid Rapide module membership")

// RapideTypeDenotation binds one interface type-name constituent to the exact
// structural type supplied by a module. Fields remain private so canonical
// identifier and descriptor validation cannot be bypassed.
type RapideTypeDenotation struct {
	name string
	typ  RapideType
}

// RapideObjectDenotation binds one concrete module object name to its exact
// structural type and canonical value. The executable membership subset
// accepts predefined-library values, allocation-identified module values paired
// with an exact structural interface type, and immutable allocation-identified
// Record modules checked field-by-field. References, functions, and other
// structural values require their own membership kernels.
type RapideObjectDenotation struct {
	name  string
	typ   RapideType
	value CanonicalValue
}

// RapideObjectTypeDenotation is an allocation-free proof that a module
// declaration supplies one concrete object with an exact structural type. It
// is used for runtime-allocated immutable objects whose value cannot be part of
// the canonical model without creating an allocation/model-digest cycle.
type RapideObjectTypeDenotation struct {
	name string
	typ  RapideType
}

// NewRapideObjectTypeDenotation constructs one canonical type-only object
// membership proof.
func NewRapideObjectTypeDenotation(name string, typ RapideType) (RapideObjectTypeDenotation, error) {
	canonical, err := canonicalRapideIdentifier(name)
	if err != nil {
		return RapideObjectTypeDenotation{}, fmt.Errorf("%w: object type denotation: %v", ErrInvalidRapideMembership, err)
	}
	if typ.node == nil {
		return RapideObjectTypeDenotation{}, fmt.Errorf("%w: object type denotation %q has a zero type", ErrInvalidRapideMembership, name)
	}
	if err := validateRapideTypeReferences(typ.node, nil); err != nil {
		return RapideObjectTypeDenotation{}, fmt.Errorf("%w: object type denotation %q: %v", ErrInvalidRapideMembership, name, err)
	}
	return RapideObjectTypeDenotation{name: canonical, typ: typ}, nil
}

// Name returns the canonical case-folded object identifier.
func (denotation RapideObjectTypeDenotation) Name() string { return denotation.name }

// Type returns the exact structural type supplied by the declaration.
func (denotation RapideObjectTypeDenotation) Type() RapideType { return denotation.typ }

// NewRapideObjectDenotation constructs one immutable module object
// denotation. Rapide's type system identifies Natural and Positive with
// Integer, so source-level Natural/Positive membership constraints must be
// checked by the source elaborator before this structural boundary.
func NewRapideObjectDenotation(name string, typ RapideType, value any) (RapideObjectDenotation, error) {
	canonical, err := canonicalRapideIdentifier(name)
	if err != nil {
		return RapideObjectDenotation{}, fmt.Errorf("%w: object denotation: %v", ErrInvalidRapideMembership, err)
	}
	if typ.node == nil {
		return RapideObjectDenotation{}, fmt.Errorf("%w: object denotation %q has a zero type", ErrInvalidRapideMembership, name)
	}
	if err := validateRapideTypeReferences(typ.node, nil); err != nil {
		return RapideObjectDenotation{}, fmt.Errorf("%w: object denotation %q: %v", ErrInvalidRapideMembership, name, err)
	}
	encoded, err := EncodeCanonicalValue(value)
	if err != nil {
		return RapideObjectDenotation{}, fmt.Errorf("%w: object denotation %q value: %v", ErrInvalidRapideMembership, name, err)
	}
	decoded, err := DecodeCanonicalValue(encoded)
	if err != nil {
		return RapideObjectDenotation{}, fmt.Errorf("%w: object denotation %q value: %v", ErrInvalidRapideMembership, name, err)
	}
	switch typ.node.kind {
	case rapidePredefinedLibraryType:
		if !CanonicalValueMatchesPredefinedType(decoded, typ.node.predefined) {
			return RapideObjectDenotation{}, fmt.Errorf("%w: object denotation %q value is not a member of %s", ErrInvalidRapideMembership, name, typ.node.predefined)
		}
	case rapideInterfaceType:
		switch value := decoded.(type) {
		case RapideModuleValue:
			// The exact structural type paired with this allocation supplies
			// the static membership proof for a general module value.
		case RapideRecordValue:
			if err := validateRapideRecordValueMembership(value, typ.node); err != nil {
				return RapideObjectDenotation{}, fmt.Errorf(
					"%w: object denotation %q Record value: %v",
					ErrInvalidRapideMembership, name, err,
				)
			}
		default:
			return RapideObjectDenotation{}, fmt.Errorf("%w: object denotation %q value is not an allocation-identified module", ErrInvalidRapideMembership, name)
		}
	default:
		return RapideObjectDenotation{}, fmt.Errorf("%w: object denotation %q type requires an unsupported structural value-membership kernel", ErrInvalidRapideMembership, name)
	}
	return RapideObjectDenotation{name: canonical, typ: typ, value: encoded}, nil
}

func validateRapideRecordValueMembership(value RapideRecordValue, typ *rapideTypeNode) error {
	if typ == nil || typ.kind != rapideInterfaceType {
		return fmt.Errorf("Record target is not an interface")
	}
	for _, member := range typ.members {
		if member.region == rapideRequiresRegion {
			// A Record literal module has no environmental requirements. The
			// ordinary contravariant interface rule therefore permits it where
			// a target interface declares requirements.
			continue
		}
		if member.kind != rapideObjectConstituent || member.typ == nil || member.typ.kind == rapideFunctionType {
			return fmt.Errorf("Record literal cannot supply non-field constituent %q", member.name)
		}
		field, exists, err := value.Field(member.name)
		if err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("Record value does not supply field %q", member.name)
		}
		matches, err := rapideRecordFieldMatchesType(field, member.typ)
		if err != nil {
			return fmt.Errorf("field %q: %v", member.name, err)
		}
		if !matches {
			return fmt.Errorf("field %q is not a member of %s", member.name, rapideMembershipTypeDescription(member.typ))
		}
	}
	return nil
}

func rapideRecordFieldMatchesType(value any, typ *rapideTypeNode) (bool, error) {
	if typ == nil {
		return false, fmt.Errorf("field type is missing")
	}
	switch typ.kind {
	case rapidePredefinedLibraryType:
		return CanonicalValueMatchesPredefinedType(value, typ.predefined), nil
	case rapideInterfaceType:
		switch nested := value.(type) {
		case RapideModuleValue:
			return nested.identity != "", nil
		case RapideRecordValue:
			if err := validateRapideRecordValueMembership(nested, typ); err != nil {
				return false, err
			}
			return true, nil
		default:
			return false, nil
		}
	case rapideTypeNameReferenceType:
		return false, fmt.Errorf("field type-name reference %q requires contextual denotation substitution", typ.reference)
	default:
		return false, fmt.Errorf("field type requires an unsupported structural value-membership kernel")
	}
}

func rapideMembershipTypeDescription(typ *rapideTypeNode) string {
	if typ == nil {
		return "<missing>"
	}
	if typ.kind == rapidePredefinedLibraryType {
		return typ.predefined
	}
	return "its structural field type"
}

// Name returns the canonical case-folded object identifier.
func (denotation RapideObjectDenotation) Name() string { return denotation.name }

// Type returns the exact immutable structural type of the module object.
func (denotation RapideObjectDenotation) Type() RapideType { return denotation.typ }

// Value returns a defensive decoded copy of the module object's canonical
// value.
func (denotation RapideObjectDenotation) Value() (any, error) {
	return DecodeCanonicalValue(denotation.value)
}

// EncodedValue returns the immutable canonical representation of the module
// object's value. The returned composite is a defensive copy.
func (denotation RapideObjectDenotation) EncodedValue() CanonicalValue {
	return copyCanonicalMembershipValue(denotation.value)
}

// NewRapideTypeDenotation constructs one exact module type denotation.
func NewRapideTypeDenotation(name string, typ RapideType) (RapideTypeDenotation, error) {
	canonical, err := canonicalRapideIdentifier(name)
	if err != nil {
		return RapideTypeDenotation{}, fmt.Errorf("%w: type denotation: %v", ErrInvalidRapideMembership, err)
	}
	if typ.node == nil {
		return RapideTypeDenotation{}, fmt.Errorf("%w: type denotation %q has a zero type", ErrInvalidRapideMembership, name)
	}
	if err := validateRapideTypeReferences(typ.node, nil); err != nil {
		return RapideTypeDenotation{}, fmt.Errorf("%w: type denotation %q: %v", ErrInvalidRapideMembership, name, err)
	}
	return RapideTypeDenotation{name: canonical, typ: typ}, nil
}

// Name returns the canonical case-folded constituent identifier.
func (denotation RapideTypeDenotation) Name() string {
	return denotation.name
}

// Type returns the exact immutable structural denotation.
func (denotation RapideTypeDenotation) Type() RapideType {
	return denotation.typ
}

// NormalizeRapideTypeDenotations validates, deduplicates, and name-sorts an
// exact module type table independently of any target interface. Extra names
// are legitimate private module declarations when the target does not expose
// them.
func NormalizeRapideTypeDenotations(
	denotations ...RapideTypeDenotation,
) ([]RapideTypeDenotation, error) {
	normalized := make([]RapideTypeDenotation, len(denotations))
	seen := make(map[string]bool, len(denotations))
	for index, denotation := range denotations {
		validated, err := NewRapideTypeDenotation(denotation.name, denotation.typ)
		if err != nil {
			return nil, err
		}
		if seen[validated.name] {
			return nil, fmt.Errorf("%w: duplicate module type denotation %q", ErrInvalidRapideMembership, validated.name)
		}
		seen[validated.name] = true
		normalized[index] = validated
	}
	sort.Slice(normalized, func(left, right int) bool {
		return normalized[left].name < normalized[right].name
	})
	return normalized, nil
}

// NormalizeRapideObjectDenotations validates, deduplicates, and name-sorts a
// concrete module object table independently of a target interface. Extra
// objects are legitimate internal module declarations.
func NormalizeRapideObjectDenotations(
	denotations ...RapideObjectDenotation,
) ([]RapideObjectDenotation, error) {
	normalized := make([]RapideObjectDenotation, len(denotations))
	seen := make(map[string]bool, len(denotations))
	for index, denotation := range denotations {
		value, err := denotation.Value()
		if err != nil {
			return nil, fmt.Errorf("%w: object denotation %q value: %v", ErrInvalidRapideMembership, denotation.name, err)
		}
		validated, err := NewRapideObjectDenotation(denotation.name, denotation.typ, value)
		if err != nil {
			return nil, err
		}
		if seen[validated.name] {
			return nil, fmt.Errorf("%w: duplicate module object denotation %q", ErrInvalidRapideMembership, validated.name)
		}
		seen[validated.name] = true
		normalized[index] = validated
	}
	sort.Slice(normalized, func(left, right int) bool {
		return normalized[left].name < normalized[right].name
	})
	return normalized, nil
}

// NormalizeRapideObjectTypeDenotations validates, deduplicates, and name-sorts
// allocation-free object membership proofs.
func NormalizeRapideObjectTypeDenotations(
	denotations ...RapideObjectTypeDenotation,
) ([]RapideObjectTypeDenotation, error) {
	normalized := make([]RapideObjectTypeDenotation, len(denotations))
	seen := make(map[string]bool, len(denotations))
	for index, denotation := range denotations {
		validated, err := NewRapideObjectTypeDenotation(denotation.name, denotation.typ)
		if err != nil {
			return nil, err
		}
		if seen[validated.name] {
			return nil, fmt.Errorf("%w: duplicate module object type denotation %q", ErrInvalidRapideMembership, validated.name)
		}
		seen[validated.name] = true
		normalized[index] = validated
	}
	sort.Slice(normalized, func(left, right int) bool {
		return normalized[left].name < normalized[right].name
	})
	return normalized, nil
}

// ValidateRapideInterfaceTypeDenotations checks the Type LRM obligations for
// every non-constructor type name at the root of one interface and returns a
// canonical name-sorted copy. A module declaration supplies an exact type; an
// unbounded target accepts it, a bounded target requires a subtype, and an
// exact target requires mutual-subtype equality.
func ValidateRapideInterfaceTypeDenotations(
	interfaceType RapideType,
	denotations ...RapideTypeDenotation,
) ([]RapideTypeDenotation, error) {
	if interfaceType.node == nil || interfaceType.node.kind != rapideInterfaceType {
		return nil, fmt.Errorf("%w: membership target is not an interface type", ErrInvalidRapideMembership)
	}
	if err := validateRapideTypeReferences(interfaceType.node, nil); err != nil {
		return nil, fmt.Errorf("%w: membership target: %v", ErrInvalidRapideMembership, err)
	}

	normalized, err := NormalizeRapideTypeDenotations(denotations...)
	if err != nil {
		return nil, err
	}
	byName := make(map[string]RapideType, len(denotations))
	for _, denotation := range normalized {
		byName[denotation.name] = denotation.typ
	}

	targetNames := make(map[string]rapideInterfaceConstituent)
	for _, member := range interfaceType.node.members {
		if member.kind == rapideTypeNameConstituent {
			targetNames[member.name] = member
		}
	}
	for _, name := range sortedRapideMembershipNames(targetNames) {
		target := targetNames[name]
		actual, exists := byName[name]
		if !exists {
			return nil, fmt.Errorf("%w: module does not supply type-name %q", ErrInvalidRapideMembership, name)
		}
		switch target.typeSpecification {
		case rapideAnyTypeDenotation:
		case rapideSubtypeTypeDenotation, rapideExactTypeDenotation:
			if target.typ == nil {
				return nil, fmt.Errorf("%w: constrained target type-name %q has no type", ErrInvalidRapideMembership, name)
			}
			constraint := target.typ
			if constraint.kind == rapideTypeNameReferenceType {
				var found bool
				constraintType, exists := byName[constraint.reference]
				if exists {
					constraint = constraintType.node
					found = true
				}
				if !found {
					return nil, fmt.Errorf("%w: type-name %q constraint references unsupplied local type-name %q",
						ErrInvalidRapideMembership, name, target.typ.reference)
				}
			} else if rapideNodeContainsTypeNameReference(constraint, make(map[*rapideTypeNode]bool)) {
				return nil, fmt.Errorf("%w: type-name %q constraint requires nested local type substitution",
					ErrInvalidRapideMembership, name)
			}
			checker := rapideSubtypeChecker{states: make(map[rapideSubtypePair]rapideSubtypeState)}
			compatible, err := checker.subtype(actual.node, constraint)
			if err != nil {
				return nil, fmt.Errorf("%w: type-name %q: %v", ErrInvalidRapideMembership, name, err)
			}
			if compatible && target.typeSpecification == rapideExactTypeDenotation {
				compatible, err = checker.subtype(constraint, actual.node)
				if err != nil {
					return nil, fmt.Errorf("%w: type-name %q: %v", ErrInvalidRapideMembership, name, err)
				}
			}
			if !compatible {
				specification := "bound"
				if target.typeSpecification == rapideExactTypeDenotation {
					specification = "exact type"
				}
				return nil, fmt.Errorf("%w: module type denotation %q does not satisfy target %s", ErrInvalidRapideMembership, name, specification)
			}
		default:
			return nil, fmt.Errorf("%w: target type-name %q has an invalid specification", ErrInvalidRapideMembership, name)
		}
	}
	return normalized, nil
}

// ValidateRapideInterfaceObjectTypes checks the type-level obligation for every
// concrete provides/private non-function object at the root of an interface.
// Required objects are environmental requirements and are therefore not
// supplied by the module. Extra module objects are retained as internal
// declarations. Direct references to local interface type names are
// substituted from the module's already-validated exact type table.
func ValidateRapideInterfaceObjectTypes(
	interfaceType RapideType,
	typeDenotations []RapideTypeDenotation,
	objectDenotations ...RapideObjectTypeDenotation,
) ([]RapideObjectTypeDenotation, error) {
	if interfaceType.node == nil || interfaceType.node.kind != rapideInterfaceType {
		return nil, fmt.Errorf("%w: membership target is not an interface type", ErrInvalidRapideMembership)
	}
	if err := validateRapideTypeReferences(interfaceType.node, nil); err != nil {
		return nil, fmt.Errorf("%w: membership target: %v", ErrInvalidRapideMembership, err)
	}
	validatedTypes, err := ValidateRapideInterfaceTypeDenotations(interfaceType, typeDenotations...)
	if err != nil {
		return nil, err
	}
	typesByName := make(map[string]*rapideTypeNode, len(validatedTypes))
	for _, denotation := range validatedTypes {
		typesByName[denotation.name] = denotation.typ.node
	}
	normalized, err := NormalizeRapideObjectTypeDenotations(objectDenotations...)
	if err != nil {
		return nil, err
	}
	objectsByName := make(map[string]RapideObjectTypeDenotation, len(normalized))
	for _, denotation := range normalized {
		objectsByName[denotation.name] = denotation
	}
	targets := make(map[string]rapideInterfaceConstituent)
	for _, member := range interfaceType.node.members {
		if member.kind != rapideObjectConstituent || member.region == rapideRequiresRegion ||
			member.typ == nil || member.typ.kind == rapideFunctionType {
			continue
		}
		if _, exists := targets[member.name]; exists {
			return nil, fmt.Errorf("%w: overloaded concrete interface object %q requires type-directed module object selection",
				ErrInvalidRapideMembership, member.name)
		}
		targets[member.name] = member
	}
	for _, name := range sortedRapideMembershipNames(targets) {
		target := targets[name]
		actual, exists := objectsByName[name]
		if !exists {
			return nil, fmt.Errorf("%w: module does not supply object %q", ErrInvalidRapideMembership, name)
		}
		constraint, err := resolveRapideMembershipType(target.typ, typesByName, "object "+name)
		if err != nil {
			return nil, err
		}
		checker := rapideSubtypeChecker{states: make(map[rapideSubtypePair]rapideSubtypeState)}
		compatible, err := checker.subtype(actual.typ.node, constraint)
		if err != nil {
			return nil, fmt.Errorf("%w: object %q: %v", ErrInvalidRapideMembership, name, err)
		}
		if !compatible {
			return nil, fmt.Errorf("%w: module object denotation %q does not subtype its interface object type", ErrInvalidRapideMembership, name)
		}
	}
	return normalized, nil
}

// ValidateRapideInterfaceObjectDenotations additionally validates every
// supplied canonical value before applying the shared type-level membership
// proof.
func ValidateRapideInterfaceObjectDenotations(
	interfaceType RapideType,
	typeDenotations []RapideTypeDenotation,
	objectDenotations ...RapideObjectDenotation,
) ([]RapideObjectDenotation, error) {
	normalized, err := NormalizeRapideObjectDenotations(objectDenotations...)
	if err != nil {
		return nil, err
	}
	types := make([]RapideObjectTypeDenotation, len(normalized))
	for index, denotation := range normalized {
		types[index], err = NewRapideObjectTypeDenotation(denotation.name, denotation.typ)
		if err != nil {
			return nil, err
		}
	}
	if _, err := ValidateRapideInterfaceObjectTypes(interfaceType, typeDenotations, types...); err != nil {
		return nil, err
	}
	return normalized, nil
}

func resolveRapideMembershipType(
	constraint *rapideTypeNode,
	typesByName map[string]*rapideTypeNode,
	description string,
) (*rapideTypeNode, error) {
	if constraint == nil {
		return nil, fmt.Errorf("%w: %s has no type", ErrInvalidRapideMembership, description)
	}
	if constraint.kind == rapideTypeNameReferenceType {
		resolved := typesByName[constraint.reference]
		if resolved == nil {
			return nil, fmt.Errorf("%w: %s references unsupplied local type-name %q",
				ErrInvalidRapideMembership, description, constraint.reference)
		}
		return resolved, nil
	}
	if rapideNodeContainsTypeNameReference(constraint, make(map[*rapideTypeNode]bool)) {
		return nil, fmt.Errorf("%w: %s requires nested local type substitution", ErrInvalidRapideMembership, description)
	}
	return constraint, nil
}

func copyCanonicalMembershipValue(value CanonicalValue) CanonicalValue {
	result := value
	if value.Items != nil {
		result.Items = make([]CanonicalValue, len(value.Items))
		for index, item := range value.Items {
			result.Items[index] = copyCanonicalMembershipValue(item)
		}
	}
	if value.Fields != nil {
		result.Fields = make([]CanonicalField, len(value.Fields))
		for index, field := range value.Fields {
			result.Fields[index] = CanonicalField{Name: field.Name, Value: copyCanonicalMembershipValue(field.Value)}
		}
	}
	return result
}

func sortedRapideMembershipNames(values map[string]rapideInterfaceConstituent) []string {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func rapideNodeContainsTypeNameReference(node *rapideTypeNode, visited map[*rapideTypeNode]bool) bool {
	if node == nil || visited[node] {
		return false
	}
	visited[node] = true
	if node.kind == rapideTypeNameReferenceType {
		return true
	}
	for _, member := range node.members {
		if rapideNodeContainsTypeNameReference(member.typ, visited) ||
			rapideNodeContainsTypeNameReference(member.constructorResult, visited) {
			return true
		}
	}
	for _, parameter := range node.parameters {
		if rapideNodeContainsTypeNameReference(parameter.typ, visited) {
			return true
		}
	}
	for _, parameter := range node.eventParams {
		if rapideNodeContainsTypeNameReference(parameter.typ, visited) {
			return true
		}
	}
	return rapideNodeContainsTypeNameReference(node.result, visited)
}

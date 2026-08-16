package gorapide

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

const RapideTypeFormat = "gorapide.rapide-type.v2"

// MaxRapideIntegerServiceSetCardinality is the compatibility-profile bound on
// one closed literal Integer-range service set. Exhausting it fails explicitly;
// the engine never truncates a declared service family.
const MaxRapideIntegerServiceSetCardinality = 256

// MaxRapideComponentArrayCardinality is the compatibility-profile bound on
// one closed Integer-range array of architecture components. Exhausting it
// fails during elaboration; the compiler never truncates an array or lets host
// resources determine how many component instances exist.
const MaxRapideComponentArrayCardinality = 256

const rapideTypeDigestPrefix = "type1-"

var ErrInvalidRapideType = errors.New("invalid Rapide type")

// RapideType is an immutable, normalized descriptor for the closed portion of
// the Stanford Rapide type language implemented by this package. The
// fundamental represented kinds are interface and function types. Predefined
// library types use an opaque implementation node until their complete Rapide
// interface definitions and type constructors can be expanded.
//
// Source type-declaration names are deliberately absent: Rapide type
// declarations alias their denoted types rather than creating nominal types.
type RapideType struct {
	node *rapideTypeNode
}

type rapideTypeKind uint8

const (
	rapideInterfaceType rapideTypeKind = iota + 1
	rapideFunctionType
	rapideEventInterfaceType
	rapidePredefinedLibraryType
	rapideTypeNameReferenceType
)

type rapideTypeNode struct {
	kind        rapideTypeKind
	predefined  string
	reference   string
	members     []rapideInterfaceConstituent
	parameters  []rapideObjectParameter
	eventParams []rapideEventObjectParameter
	result      *rapideTypeNode
}

type rapideInterfaceMemberKind uint8

const (
	rapideObjectConstituent rapideInterfaceMemberKind = iota + 1
	rapideActionConstituent
	rapideTypeNameConstituent
	rapideTypeConstructorConstituent
	rapideModuleGeneratorConstituent
	rapideExceptionConstituent
)

type rapideInterfaceRegion uint8

const (
	rapideProvidesRegion rapideInterfaceRegion = iota + 1
	rapidePrivateRegion
	rapideRequiresRegion
)

// RapideInterfaceMember is one object or action constituent of a closed
// interface descriptor. Its fields are private so constituent kind, region or
// action mode, and type validity are checked by NewRapideInterfaceType.
type RapideInterfaceMember struct {
	kind              rapideInterfaceMemberKind
	region            rapideInterfaceRegion
	actionMode        rapideActionMode
	typeSpecification rapideTypeNameSpecification
	name              string
	typ               RapideType
	constructorParams []RapideFunctionParameter
	constructorResult RapideType
}

type rapideInterfaceConstituent struct {
	kind              rapideInterfaceMemberKind
	region            rapideInterfaceRegion
	actionMode        rapideActionMode
	typeSpecification rapideTypeNameSpecification
	name              string
	typ               *rapideTypeNode
	constructorResult *rapideTypeNode
}

type rapideTypeNameSpecification uint8

const (
	rapideAnyTypeDenotation rapideTypeNameSpecification = iota + 1
	rapideSubtypeTypeDenotation
	rapideExactTypeDenotation
)

type rapideActionMode uint8

const (
	rapideActionIn rapideActionMode = iota + 1
	rapideActionOut
)

// NewRapideTypeNameReference constructs a symbolic reference to a type-name
// declaration in the lexically enclosing interface or function type. It is an
// internal representation node, not a third fundamental Rapide type kind. A
// reference is valid only when an enclosing scope declares the same type name;
// standalone canonicalization and subtyping reject it as unscoped.
func NewRapideTypeNameReference(name string) (RapideType, error) {
	canonical, err := canonicalRapideIdentifier(name)
	if err != nil {
		return RapideType{}, fmt.Errorf("%w: type-name reference: %v", ErrInvalidRapideType, err)
	}
	return RapideType{node: &rapideTypeNode{kind: rapideTypeNameReferenceType, reference: canonical}}, nil
}

// NewRapideTypeParameterReference is the function-formal spelling of the same
// lexical type-reference node. Keeping one canonical node preserves Rapide's
// non-nominal type identity while making the intended scope explicit to API
// callers constructing dependent function descriptors.
func NewRapideTypeParameterReference(name string) (RapideType, error) {
	return NewRapideTypeNameReference(name)
}

// ProvidedRapideMember constructs a publicly provided object constituent.
func ProvidedRapideMember(name string, typ RapideType) RapideInterfaceMember {
	return RapideInterfaceMember{kind: rapideObjectConstituent, region: rapideProvidesRegion, name: name, typ: typ}
}

// PrivateRapideMember constructs a module-private object constituent.
func PrivateRapideMember(name string, typ RapideType) RapideInterfaceMember {
	return RapideInterfaceMember{kind: rapideObjectConstituent, region: rapidePrivateRegion, name: name, typ: typ}
}

// RequiredRapideMember constructs a required object constituent.
func RequiredRapideMember(name string, typ RapideType) RapideInterfaceMember {
	return RapideInterfaceMember{kind: rapideObjectConstituent, region: rapideRequiresRegion, name: name, typ: typ}
}

// ProvidedRapideModuleGenerator constructs a publicly provided module-generator
// constituent. signature must be a function type whose result is an interface;
// the distinct constituent kind preserves the generator's fresh-allocation
// semantics instead of treating it as an ordinary function-valued object.
func ProvidedRapideModuleGenerator(name string, signature RapideType) RapideInterfaceMember {
	return RapideInterfaceMember{kind: rapideModuleGeneratorConstituent, region: rapideProvidesRegion, name: name, typ: signature}
}

// PrivateRapideModuleGenerator constructs a module-private module generator.
func PrivateRapideModuleGenerator(name string, signature RapideType) RapideInterfaceMember {
	return RapideInterfaceMember{kind: rapideModuleGeneratorConstituent, region: rapidePrivateRegion, name: name, typ: signature}
}

// RequiredRapideModuleGenerator constructs a required module generator.
func RequiredRapideModuleGenerator(name string, signature RapideType) RapideInterfaceMember {
	return RapideInterfaceMember{kind: rapideModuleGeneratorConstituent, region: rapideRequiresRegion, name: name, typ: signature}
}

// UnboundedProvidedRapideTypeName constructs Stanford's `type T;`
// constituent in a provides region. A module may denote T by any type.
func UnboundedProvidedRapideTypeName(name string) RapideInterfaceMember {
	return RapideInterfaceMember{
		kind: rapideTypeNameConstituent, region: rapideProvidesRegion,
		typeSpecification: rapideAnyTypeDenotation, name: name,
	}
}

// BoundedProvidedRapideTypeName constructs `type T <: Bound;` in a provides
// region. A module's denotation for T must subtype bound.
func BoundedProvidedRapideTypeName(name string, bound RapideType) RapideInterfaceMember {
	return RapideInterfaceMember{
		kind: rapideTypeNameConstituent, region: rapideProvidesRegion,
		typeSpecification: rapideSubtypeTypeDenotation, name: name, typ: bound,
	}
}

// ExactProvidedRapideTypeName constructs `type T is Exact;` in a provides
// region. A module's denotation for T must equal exact by mutual subtyping.
func ExactProvidedRapideTypeName(name string, exact RapideType) RapideInterfaceMember {
	return RapideInterfaceMember{
		kind: rapideTypeNameConstituent, region: rapideProvidesRegion,
		typeSpecification: rapideExactTypeDenotation, name: name, typ: exact,
	}
}

// UnboundedPrivateRapideTypeName constructs a private `type T;`
// constituent. Type-name declarations are not permitted in requires regions.
func UnboundedPrivateRapideTypeName(name string) RapideInterfaceMember {
	return RapideInterfaceMember{
		kind: rapideTypeNameConstituent, region: rapidePrivateRegion,
		typeSpecification: rapideAnyTypeDenotation, name: name,
	}
}

// BoundedPrivateRapideTypeName constructs a private `type T <: Bound;`
// constituent.
func BoundedPrivateRapideTypeName(name string, bound RapideType) RapideInterfaceMember {
	return RapideInterfaceMember{
		kind: rapideTypeNameConstituent, region: rapidePrivateRegion,
		typeSpecification: rapideSubtypeTypeDenotation, name: name, typ: bound,
	}
}

// ExactPrivateRapideTypeName constructs a private `type T is Exact;`
// constituent.
func ExactPrivateRapideTypeName(name string, exact RapideType) RapideInterfaceMember {
	return RapideInterfaceMember{
		kind: rapideTypeNameConstituent, region: rapidePrivateRegion,
		typeSpecification: rapideExactTypeDenotation, name: name, typ: exact,
	}
}

// UnboundedProvidedRapideTypeConstructor constructs `type T(Formals);` in a
// provides region. Any constructor conforming to the characteristic function
// type may denote T in a module.
func UnboundedProvidedRapideTypeConstructor(name string, parameters ...RapideFunctionParameter) RapideInterfaceMember {
	return rapideTypeConstructorMember(name, rapideProvidesRegion, rapideAnyTypeDenotation, RapideType{}, parameters)
}

// BoundedProvidedRapideTypeConstructor constructs the closed nondependent
// subset of `type T(Formals) <: Bound;`. Every application result must subtype
// bound; parameter-dependent bounds require the future symbolic graph kernel.
func BoundedProvidedRapideTypeConstructor(
	name string,
	bound RapideType,
	parameters ...RapideFunctionParameter,
) RapideInterfaceMember {
	return rapideTypeConstructorMember(name, rapideProvidesRegion, rapideSubtypeTypeDenotation, bound, parameters)
}

// ExactProvidedRapideTypeConstructor constructs the closed nondependent subset
// of `type T(Formals) is Exact;` in a provides region.
func ExactProvidedRapideTypeConstructor(
	name string,
	exact RapideType,
	parameters ...RapideFunctionParameter,
) RapideInterfaceMember {
	return rapideTypeConstructorMember(name, rapideProvidesRegion, rapideExactTypeDenotation, exact, parameters)
}

// UnboundedPrivateRapideTypeConstructor constructs a private
// `type T(Formals);` constituent.
func UnboundedPrivateRapideTypeConstructor(name string, parameters ...RapideFunctionParameter) RapideInterfaceMember {
	return rapideTypeConstructorMember(name, rapidePrivateRegion, rapideAnyTypeDenotation, RapideType{}, parameters)
}

// BoundedPrivateRapideTypeConstructor constructs the closed nondependent
// private subset of `type T(Formals) <: Bound;`.
func BoundedPrivateRapideTypeConstructor(
	name string,
	bound RapideType,
	parameters ...RapideFunctionParameter,
) RapideInterfaceMember {
	return rapideTypeConstructorMember(name, rapidePrivateRegion, rapideSubtypeTypeDenotation, bound, parameters)
}

// ExactPrivateRapideTypeConstructor constructs the closed nondependent private
// subset of `type T(Formals) is Exact;`.
func ExactPrivateRapideTypeConstructor(
	name string,
	exact RapideType,
	parameters ...RapideFunctionParameter,
) RapideInterfaceMember {
	return rapideTypeConstructorMember(name, rapidePrivateRegion, rapideExactTypeDenotation, exact, parameters)
}

func rapideTypeConstructorMember(
	name string,
	region rapideInterfaceRegion,
	specification rapideTypeNameSpecification,
	result RapideType,
	parameters []RapideFunctionParameter,
) RapideInterfaceMember {
	return RapideInterfaceMember{
		kind: rapideTypeConstructorConstituent, region: region,
		typeSpecification: specification, name: name,
		constructorParams: append([]RapideFunctionParameter(nil), parameters...),
		constructorResult: result,
	}
}

// InputRapideAction constructs an incoming action constituent. eventType must
// be an event descriptor produced by NewRapideEventType.
func InputRapideAction(name string, eventType RapideType) RapideInterfaceMember {
	return RapideInterfaceMember{kind: rapideActionConstituent, actionMode: rapideActionIn, name: name, typ: eventType}
}

// OutputRapideAction constructs an outgoing action constituent. eventType must
// be an event descriptor produced by NewRapideEventType.
func OutputRapideAction(name string, eventType RapideType) RapideInterfaceMember {
	return RapideInterfaceMember{kind: rapideActionConstituent, actionMode: rapideActionOut, name: name, typ: eventType}
}

// ProvidedRapideException constructs a publicly provided exception-name
// constituent. eventType must be produced by NewRapideEventType.
func ProvidedRapideException(name string, eventType RapideType) RapideInterfaceMember {
	return RapideInterfaceMember{
		kind: rapideExceptionConstituent, region: rapideProvidesRegion, name: name, typ: eventType,
	}
}

// PrivateRapideException constructs a module-private exception constituent.
func PrivateRapideException(name string, eventType RapideType) RapideInterfaceMember {
	return RapideInterfaceMember{
		kind: rapideExceptionConstituent, region: rapidePrivateRegion, name: name, typ: eventType,
	}
}

// RequiredRapideException constructs a required exception-name constituent.
func RequiredRapideException(name string, eventType RapideType) RapideInterfaceMember {
	return RapideInterfaceMember{
		kind: rapideExceptionConstituent, region: rapideRequiresRegion, name: name, typ: eventType,
	}
}

// RapideEventParameter is one named field of an event's parameter record.
// Event parameters are record fields, not ordered function parameters.
type RapideEventParameter struct {
	name string
	typ  RapideType
}

type rapideEventObjectParameter struct {
	name string
	typ  *rapideTypeNode
}

// RapideEventParam constructs one event parameter-record field.
func RapideEventParam(name string, typ RapideType) RapideEventParameter {
	return RapideEventParameter{name: name, typ: typ}
}

type rapideFunctionParameterKind uint8

const (
	rapideObjectFunctionParameter rapideFunctionParameterKind = iota + 1
	rapideTypeFunctionParameter
)

// RapideFunctionParameter is one ordered formal object or type parameter.
// Its fields are private so kind, bounds, defaults, and type validity are
// checked by NewRapideFunctionType.
type RapideFunctionParameter struct {
	kind       rapideFunctionParameterKind
	name       string
	typ        RapideType
	hasDefault bool
}

type rapideObjectParameter struct {
	kind       rapideFunctionParameterKind
	name       string
	typ        *rapideTypeNode
	hasDefault bool
}

// RapideUnionMember is one distinct tag and associated member type in the
// Predefined Types LRM's Union shorthand. Fields remain private so tag
// canonicalization and member-type validity cannot be bypassed.
type RapideUnionMember struct {
	name string
	typ  RapideType
}

// RapideUnionTag constructs one tag/type association for NewRapideUnionType.
func RapideUnionTag(name string, typ RapideType) RapideUnionMember {
	return RapideUnionMember{name: name, typ: typ}
}

// RapideObjectParameter constructs a required formal object parameter.
func RapideObjectParameter(name string, typ RapideType) RapideFunctionParameter {
	return RapideFunctionParameter{kind: rapideObjectFunctionParameter, name: name, typ: typ}
}

// DefaultedRapideObjectParameter constructs a formal object parameter with a
// default denotation. The default object itself belongs to executable
// elaboration and is not part of function-type subtyping.
func DefaultedRapideObjectParameter(name string, typ RapideType) RapideFunctionParameter {
	return RapideFunctionParameter{kind: rapideObjectFunctionParameter, name: name, typ: typ, hasDefault: true}
}

// RapideTypeParameter constructs an unbounded formal `type T` parameter.
func RapideTypeParameter(name string) RapideFunctionParameter {
	return RapideFunctionParameter{kind: rapideTypeFunctionParameter, name: name}
}

// BoundedRapideTypeParameter constructs a formal `type T <: Bound` parameter.
func BoundedRapideTypeParameter(name string, bound RapideType) RapideFunctionParameter {
	return RapideFunctionParameter{kind: rapideTypeFunctionParameter, name: name, typ: bound}
}

// EmptyRapideInterfaceType returns the empty interface type. Stanford Rapide
// treats a function declaration without a result as returning this type.
func EmptyRapideInterfaceType() RapideType {
	return RapideType{node: &rapideTypeNode{kind: rapideInterfaceType}}
}

// RapidePredefinedType returns the normalized type-system descriptor for one
// currently supported predefined type. The Predefined Types LRM defines
// Natural and Positive through constraints on Integer and explicitly says all
// three are indistinguishable to the type system, so they share one descriptor.
// Their distinct value-membership predicates remain available through
// CanonicalValueMatchesPredefinedType. GST is instead the library's shared
// abstract subtype of Float: it retains distinct type identity and has no
// fabricated runtime values in the current profile.
func RapidePredefinedType(name string) (RapideType, error) {
	var canonical string
	switch strings.ToLower(name) {
	case "triv":
		canonical = "Triv"
	case "boolean":
		canonical = "Boolean"
	case "integer", "natural", "positive":
		canonical = "Integer"
	case "float":
		canonical = "Float"
	case "gst":
		canonical = "GST"
	case "character":
		canonical = "Character"
	case "string":
		canonical = "String"
	default:
		return RapideType{}, fmt.Errorf("%w: predefined type %q is outside the closed library subset", ErrInvalidRapideType, name)
	}
	return RapideType{node: &rapideTypeNode{kind: rapidePredefinedLibraryType, predefined: canonical}}, nil
}

// PredefinedName reports the canonical library name when this descriptor is a
// closed predefined type. Natural and Positive report Integer because the
// Stanford type system declares those constrained sets indistinguishable from
// Integer; value-membership predicates retain their distinction separately.
// GST reports GST because its abstract identity must remain distinct from its
// Float upper bound.
func (typ RapideType) PredefinedName() (string, bool) {
	if typ.node == nil || typ.node.kind != rapidePredefinedLibraryType || typ.node.predefined == "" {
		return "", false
	}
	return typ.node.predefined, true
}

// NewRapideEventType constructs an event interface from its parameter record.
// Parameter order and identifier case are nonsemantic in the current
// compatibility framework. Event identity, owner/performer, causal operations,
// and event generation belong to the value/executable layers; this descriptor
// supplies their published structural type boundary.
func NewRapideEventType(parameters ...RapideEventParameter) (RapideType, error) {
	normalized := make([]rapideEventObjectParameter, len(parameters))
	for index, parameter := range parameters {
		name, err := canonicalRapideIdentifier(parameter.name)
		if err != nil {
			return RapideType{}, fmt.Errorf("%w: event parameter %d: %v", ErrInvalidRapideType, index, err)
		}
		if parameter.typ.node == nil {
			return RapideType{}, fmt.Errorf("%w: event parameter %q has an invalid type", ErrInvalidRapideType, parameter.name)
		}
		normalized[index] = rapideEventObjectParameter{name: name, typ: parameter.typ.node}
	}
	sort.Slice(normalized, func(left, right int) bool {
		if normalized[left].name != normalized[right].name {
			return normalized[left].name < normalized[right].name
		}
		return compareRapideTypeNodes(normalized[left].typ, normalized[right].typ) < 0
	})
	for index := 1; index < len(normalized); index++ {
		if normalized[index-1].name == normalized[index].name {
			return RapideType{}, fmt.Errorf("%w: duplicate event parameter %q", ErrInvalidRapideType, normalized[index].name)
		}
	}
	return RapideType{node: &rapideTypeNode{kind: rapideEventInterfaceType, eventParams: normalized}}, nil
}

// NewRapideInterfaceType constructs a structural interface type from the
// currently supported object, action, exception, module-generator, type-name,
// and type-constructor constituents. Declaration order and identifier case are
// nonsemantic in the current compatibility framework and are normalized.
// Exact duplicate constituents fail explicitly.
func NewRapideInterfaceType(members ...RapideInterfaceMember) (RapideType, error) {
	return newRapideInterfaceTypeWithScope(nil, members...)
}

func newRapideInterfaceTypeWithScope(
	scope map[string]rapideTypeReferenceScopeKind,
	members ...RapideInterfaceMember,
) (RapideType, error) {
	normalized := make([]rapideInterfaceConstituent, len(members))
	for index, member := range members {
		var name string
		var err error
		switch member.kind {
		case rapideObjectConstituent:
			name, err = canonicalRapideConstituentName(member.name, true)
		case rapideActionConstituent, rapideModuleGeneratorConstituent, rapideExceptionConstituent:
			name, err = canonicalRapideConstituentName(member.name, false)
		default:
			name, err = canonicalRapideIdentifier(member.name)
		}
		if err != nil {
			return RapideType{}, fmt.Errorf("%w: interface member %d: %v", ErrInvalidRapideType, index, err)
		}
		memberType := member.typ.node
		constructorResult := member.constructorResult.node
		if member.kind != rapideTypeConstructorConstituent &&
			(len(member.constructorParams) != 0 || constructorResult != nil) {
			return RapideType{}, fmt.Errorf("%w: non-constructor member %q carries type-constructor data", ErrInvalidRapideType, member.name)
		}
		switch member.kind {
		case rapideObjectConstituent:
			if member.typ.node == nil {
				return RapideType{}, fmt.Errorf("%w: interface object member %q has an invalid type", ErrInvalidRapideType, member.name)
			}
			if member.region < rapideProvidesRegion || member.region > rapideRequiresRegion || member.actionMode != 0 || member.typeSpecification != 0 {
				return RapideType{}, fmt.Errorf("%w: object member %d has an invalid region or action mode", ErrInvalidRapideType, index)
			}
		case rapideModuleGeneratorConstituent:
			if member.typ.node == nil || member.typ.node.kind != rapideFunctionType || member.typ.node.result == nil ||
				member.typ.node.result.kind != rapideInterfaceType {
				return RapideType{}, fmt.Errorf("%w: module-generator member %q does not have a function signature returning an interface", ErrInvalidRapideType, member.name)
			}
			if member.region < rapideProvidesRegion || member.region > rapideRequiresRegion || member.actionMode != 0 || member.typeSpecification != 0 {
				return RapideType{}, fmt.Errorf("%w: module-generator member %d has an invalid region or action mode", ErrInvalidRapideType, index)
			}
		case rapideActionConstituent:
			if member.typ.node == nil {
				return RapideType{}, fmt.Errorf("%w: interface action member %q has an invalid event type", ErrInvalidRapideType, member.name)
			}
			if member.actionMode < rapideActionIn || member.actionMode > rapideActionOut || member.region != 0 || member.typeSpecification != 0 {
				return RapideType{}, fmt.Errorf("%w: action member %d has an invalid mode or object region", ErrInvalidRapideType, index)
			}
			if member.typ.node.kind != rapideEventInterfaceType {
				return RapideType{}, fmt.Errorf("%w: action member %q does not denote an event type", ErrInvalidRapideType, member.name)
			}
		case rapideExceptionConstituent:
			if member.typ.node == nil || member.typ.node.kind != rapideEventInterfaceType {
				return RapideType{}, fmt.Errorf("%w: exception member %q does not denote an event type", ErrInvalidRapideType, member.name)
			}
			if member.region < rapideProvidesRegion || member.region > rapideRequiresRegion ||
				member.actionMode != 0 || member.typeSpecification != 0 {
				return RapideType{}, fmt.Errorf("%w: exception member %d has an invalid region or action mode", ErrInvalidRapideType, index)
			}
		case rapideTypeNameConstituent:
			if member.region != rapideProvidesRegion && member.region != rapidePrivateRegion {
				return RapideType{}, fmt.Errorf("%w: type-name member %q is outside provides/private", ErrInvalidRapideType, member.name)
			}
			if member.actionMode != 0 {
				return RapideType{}, fmt.Errorf("%w: type-name member %q has an action mode", ErrInvalidRapideType, member.name)
			}
			switch member.typeSpecification {
			case rapideAnyTypeDenotation:
				if member.typ.node != nil {
					return RapideType{}, fmt.Errorf("%w: unbounded type-name member %q has a bound", ErrInvalidRapideType, member.name)
				}
			case rapideSubtypeTypeDenotation, rapideExactTypeDenotation:
				if member.typ.node == nil {
					return RapideType{}, fmt.Errorf("%w: constrained type-name member %q has no type", ErrInvalidRapideType, member.name)
				}
			default:
				return RapideType{}, fmt.Errorf("%w: type-name member %q has an invalid specification", ErrInvalidRapideType, member.name)
			}
		case rapideTypeConstructorConstituent:
			if member.region != rapideProvidesRegion && member.region != rapidePrivateRegion {
				return RapideType{}, fmt.Errorf("%w: type-constructor member %q is outside provides/private", ErrInvalidRapideType, member.name)
			}
			if member.actionMode != 0 || member.typ.node != nil {
				return RapideType{}, fmt.Errorf("%w: type-constructor member %q has an action mode or object type", ErrInvalidRapideType, member.name)
			}
			characteristic, err := NewRapideFunctionType(member.constructorParams, RapideType{})
			if err != nil {
				return RapideType{}, fmt.Errorf("%w: type-constructor member %q characteristic function: %v", ErrInvalidRapideType, member.name, err)
			}
			memberType = characteristic.node
			switch member.typeSpecification {
			case rapideAnyTypeDenotation:
				if constructorResult != nil {
					return RapideType{}, fmt.Errorf("%w: unbounded type-constructor member %q has a result constraint", ErrInvalidRapideType, member.name)
				}
			case rapideSubtypeTypeDenotation:
				if constructorResult == nil {
					return RapideType{}, fmt.Errorf("%w: bounded type-constructor member %q has no result bound", ErrInvalidRapideType, member.name)
				}
			case rapideExactTypeDenotation:
				if constructorResult == nil {
					return RapideType{}, fmt.Errorf("%w: exact type-constructor member %q has no result type", ErrInvalidRapideType, member.name)
				}
				for _, parameter := range characteristic.node.parameters {
					if parameter.hasDefault {
						return RapideType{}, fmt.Errorf("%w: exact type-constructor member %q has a defaulted object parameter whose denotation is not represented", ErrInvalidRapideType, member.name)
					}
				}
			default:
				return RapideType{}, fmt.Errorf("%w: type-constructor member %q has an invalid specification", ErrInvalidRapideType, member.name)
			}
		default:
			return RapideType{}, fmt.Errorf("%w: interface member %d has an invalid constituent kind", ErrInvalidRapideType, index)
		}
		normalized[index] = rapideInterfaceConstituent{
			kind: member.kind, region: member.region, actionMode: member.actionMode,
			typeSpecification: member.typeSpecification, name: name, typ: memberType,
			constructorResult: constructorResult,
		}
	}
	sort.Slice(normalized, func(left, right int) bool {
		return compareRapideMembers(normalized[left], normalized[right]) < 0
	})
	for index := 1; index < len(normalized); index++ {
		if compareRapideMembers(normalized[index-1], normalized[index]) == 0 {
			return RapideType{}, fmt.Errorf("%w: duplicate %s constituent %q",
				ErrInvalidRapideType, rapideConstituentDescription(normalized[index]), normalized[index].name)
		}
	}
	for index, member := range normalized {
		if member.kind != rapideTypeNameConstituent && member.kind != rapideTypeConstructorConstituent {
			continue
		}
		for otherIndex, other := range normalized {
			if index == otherIndex {
				continue
			}
			if member.name == other.name {
				kindName := "type-name"
				if member.kind == rapideTypeConstructorConstituent {
					kindName = "type-constructor"
				}
				return RapideType{}, fmt.Errorf("%w: %s constituent %q collides with another interface constituent",
					ErrInvalidRapideType, kindName, member.name)
			}
		}
	}
	result := RapideType{node: &rapideTypeNode{kind: rapideInterfaceType, members: normalized}}
	if err := validateRapideTypeReferences(result.node, scope); err != nil {
		return RapideType{}, err
	}
	return result, nil
}

// RapideServiceInterfaceType applies the Architecture LRM's basic non-dual
// service rewrite. Each target constituent is qualified by the service name;
// constituent kind, direction/region, and structural type are preserved. The
// LRM forbids private and type-name/type-constructor constituents in a service
// type. Recursive type-graph qualification remains an explicit boundary; the
// dual rewrite is exposed separately by RapideDualServiceInterfaceType.
func RapideServiceInterfaceType(name string, serviceType RapideType) (RapideType, error) {
	members, err := RapideServiceInterfaceMembers(name, serviceType)
	if err != nil {
		return RapideType{}, err
	}
	return NewRapideInterfaceType(members...)
}

// RapideDualServiceInterfaceType applies the Architecture LRM's dual-service
// rewrite. Provides/requires object and module-generator regions and in/out
// action directions are exchanged while names and structural signatures are
// qualified exactly as for an ordinary basic service.
func RapideDualServiceInterfaceType(name string, serviceType RapideType) (RapideType, error) {
	members, err := RapideDualServiceInterfaceMembers(name, serviceType)
	if err != nil {
		return RapideType{}, err
	}
	return NewRapideInterfaceType(members...)
}

// RapideIntegerServiceSetInterfaceType expands one finite literal Integer
// range into services Name(First) through Name(Last), inclusive. A descending
// range denotes the empty service family.
func RapideIntegerServiceSetInterfaceType(name string, first, last int64, serviceType RapideType) (RapideType, error) {
	members, err := RapideIntegerServiceSetInterfaceMembers(name, first, last, serviceType)
	if err != nil {
		return RapideType{}, err
	}
	return NewRapideInterfaceType(members...)
}

// RapideDualIntegerServiceSetInterfaceType is the dual form of
// RapideIntegerServiceSetInterfaceType.
func RapideDualIntegerServiceSetInterfaceType(name string, first, last int64, serviceType RapideType) (RapideType, error) {
	members, err := RapideDualIntegerServiceSetInterfaceMembers(name, first, last, serviceType)
	if err != nil {
		return RapideType{}, err
	}
	return NewRapideInterfaceType(members...)
}

// RapideServiceInterfaceMembers returns the normalized constituent expansion
// for embedding a basic service in a larger interface descriptor.
func RapideServiceInterfaceMembers(name string, serviceType RapideType) ([]RapideInterfaceMember, error) {
	return rapideServiceInterfaceMembers(name, serviceType, false)
}

// RapideDualServiceInterfaceMembers returns the normalized constituent
// expansion for embedding a dual service in a larger interface descriptor.
func RapideDualServiceInterfaceMembers(name string, serviceType RapideType) ([]RapideInterfaceMember, error) {
	return rapideServiceInterfaceMembers(name, serviceType, true)
}

// RapideIntegerServiceSetInterfaceMembers returns the flattened members of a
// finite ordinary Integer-indexed service family.
func RapideIntegerServiceSetInterfaceMembers(name string, first, last int64, serviceType RapideType) ([]RapideInterfaceMember, error) {
	return rapideIntegerServiceSetInterfaceMembers(name, first, last, serviceType, false)
}

// RapideDualIntegerServiceSetInterfaceMembers returns the flattened members of
// a finite dual Integer-indexed service family.
func RapideDualIntegerServiceSetInterfaceMembers(name string, first, last int64, serviceType RapideType) ([]RapideInterfaceMember, error) {
	return rapideIntegerServiceSetInterfaceMembers(name, first, last, serviceType, true)
}

func rapideIntegerServiceSetInterfaceMembers(
	name string,
	first, last int64,
	serviceType RapideType,
	dual bool,
) ([]RapideInterfaceMember, error) {
	base, err := canonicalRapideIdentifier(name)
	if err != nil {
		return nil, fmt.Errorf("%w: service-set name: %v", ErrInvalidRapideType, err)
	}
	if err := validateRapideServiceInterfaceTarget(name, serviceType); err != nil {
		return nil, err
	}
	if first > last {
		return nil, nil
	}
	members := make([]RapideInterfaceMember, 0)
	for index := first; ; index++ {
		if uint64(index-first) >= MaxRapideIntegerServiceSetCardinality {
			return nil, fmt.Errorf("%w: service set %q cardinality exceeds deterministic limit %d",
				ErrInvalidRapideType, name, MaxRapideIntegerServiceSetCardinality)
		}
		prefix := base + "(" + strconv.FormatInt(index, 10) + ")"
		expanded := rapideServiceInterfaceMembersFromValidatedTarget(prefix, serviceType, dual)
		members = append(members, expanded...)
		if index == last {
			break
		}
	}
	return members, nil
}

func rapideServiceInterfaceMembers(name string, serviceType RapideType, dual bool) ([]RapideInterfaceMember, error) {
	prefix, err := canonicalRapideIdentifier(name)
	if err != nil {
		return nil, fmt.Errorf("%w: service name: %v", ErrInvalidRapideType, err)
	}
	return rapideServiceInterfaceMembersWithPrefix(prefix, name, serviceType, dual)
}

func rapideServiceInterfaceMembersWithPrefix(
	prefix string,
	diagnosticName string,
	serviceType RapideType,
	dual bool,
) ([]RapideInterfaceMember, error) {
	if err := validateRapideServiceInterfaceTarget(diagnosticName, serviceType); err != nil {
		return nil, err
	}
	return rapideServiceInterfaceMembersFromValidatedTarget(prefix, serviceType, dual), nil
}

func validateRapideServiceInterfaceTarget(diagnosticName string, serviceType RapideType) error {
	if serviceType.node == nil || serviceType.node.kind != rapideInterfaceType {
		return fmt.Errorf("%w: service %q type is not an interface", ErrInvalidRapideType, diagnosticName)
	}
	if err := validateRapideTypeReferences(serviceType.node, nil); err != nil {
		return err
	}
	for _, member := range serviceType.node.members {
		switch member.kind {
		case rapideObjectConstituent:
			if member.region == rapidePrivateRegion {
				return fmt.Errorf("%w: service %q target contains private object %q", ErrInvalidRapideType, diagnosticName, member.name)
			}
		case rapideModuleGeneratorConstituent:
			if member.region == rapidePrivateRegion {
				return fmt.Errorf("%w: service %q target contains private module generator %q", ErrInvalidRapideType, diagnosticName, member.name)
			}
		case rapideActionConstituent:
		case rapideExceptionConstituent:
			if member.region == rapidePrivateRegion {
				return fmt.Errorf("%w: service %q target contains private exception %q", ErrInvalidRapideType, diagnosticName, member.name)
			}
		case rapideTypeNameConstituent, rapideTypeConstructorConstituent:
			return fmt.Errorf("%w: service %q target contains forbidden %s %q",
				ErrInvalidRapideType, diagnosticName, rapideConstituentDescription(member), member.name)
		default:
			return fmt.Errorf("%w: service %q target has an invalid constituent kind", ErrInvalidRapideType, diagnosticName)
		}
	}
	return nil
}

func rapideServiceInterfaceMembersFromValidatedTarget(
	prefix string,
	serviceType RapideType,
	dual bool,
) []RapideInterfaceMember {
	members := make([]RapideInterfaceMember, 0, len(serviceType.node.members))
	for _, member := range serviceType.node.members {
		qualified := prefix + "." + member.name
		typ := RapideType{node: member.typ}
		switch member.kind {
		case rapideObjectConstituent:
			provides := member.region == rapideProvidesRegion
			if dual {
				provides = !provides
			}
			if provides {
				members = append(members, ProvidedRapideMember(qualified, typ))
			} else {
				members = append(members, RequiredRapideMember(qualified, typ))
			}
		case rapideModuleGeneratorConstituent:
			provides := member.region == rapideProvidesRegion
			if dual {
				provides = !provides
			}
			if provides {
				members = append(members, ProvidedRapideModuleGenerator(qualified, typ))
			} else {
				members = append(members, RequiredRapideModuleGenerator(qualified, typ))
			}
		case rapideActionConstituent:
			input := member.actionMode == rapideActionIn
			if dual {
				input = !input
			}
			if input {
				members = append(members, InputRapideAction(qualified, typ))
			} else {
				members = append(members, OutputRapideAction(qualified, typ))
			}
		case rapideExceptionConstituent:
			provides := member.region == rapideProvidesRegion
			if dual {
				provides = !provides
			}
			if provides {
				members = append(members, ProvidedRapideException(qualified, typ))
			} else {
				members = append(members, RequiredRapideException(qualified, typ))
			}
		}
	}
	return members
}

// NewSelfRecursiveRapideInterfaceType constructs one equirecursive structural
// interface. The builder is invoked exactly once with the type being defined;
// references to self may be used anywhere a RapideType is accepted. The
// declaration name is intentionally absent: recursion is bound structurally,
// not by nominal source identity.
func NewSelfRecursiveRapideInterfaceType(
	build func(self RapideType) (RapideType, error),
) (RapideType, error) {
	if build == nil {
		return RapideType{}, fmt.Errorf("%w: recursive interface builder is nil", ErrInvalidRapideType)
	}
	placeholder := &rapideTypeNode{kind: rapideInterfaceType}
	constructed, err := build(RapideType{node: placeholder})
	if err != nil {
		return RapideType{}, err
	}
	if constructed.node == nil || constructed.node.kind != rapideInterfaceType {
		return RapideType{}, fmt.Errorf("%w: recursive interface builder did not return an interface type", ErrInvalidRapideType)
	}
	if constructed.node == placeholder {
		return RapideType{}, fmt.Errorf("%w: recursive interface builder returned the unbound self placeholder", ErrInvalidRapideType)
	}
	*placeholder = *constructed.node
	result := RapideType{node: placeholder}
	if err := validateRapideTypeReferences(result.node, nil); err != nil {
		return RapideType{}, err
	}
	return result, nil
}

// NewRapideFunctionType constructs a closed function type containing formal
// object and type parameters. Parameter order remains semantic. A zero result
// descriptor denotes the empty interface type, matching a Rapide declaration
// with no return clause.
func NewRapideFunctionType(parameters []RapideFunctionParameter, result RapideType) (RapideType, error) {
	normalized := make([]rapideObjectParameter, len(parameters))
	seenNames := make(map[string]struct{}, len(parameters))
	for index, parameter := range parameters {
		name, err := canonicalRapideIdentifier(parameter.name)
		if err != nil {
			return RapideType{}, fmt.Errorf("%w: function parameter %d: %v", ErrInvalidRapideType, index, err)
		}
		if _, exists := seenNames[name]; exists {
			return RapideType{}, fmt.Errorf("%w: duplicate function parameter %q", ErrInvalidRapideType, name)
		}
		seenNames[name] = struct{}{}
		switch parameter.kind {
		case rapideObjectFunctionParameter:
			if parameter.typ.node == nil {
				return RapideType{}, fmt.Errorf("%w: function object parameter %q has an invalid type", ErrInvalidRapideType, parameter.name)
			}
		case rapideTypeFunctionParameter:
			if parameter.hasDefault {
				return RapideType{}, fmt.Errorf("%w: function type parameter %q has an object default", ErrInvalidRapideType, parameter.name)
			}
			// A nil type is the published unbounded `type T` form. A nonnil
			// type is the bound in `type T <: Bound`.
		default:
			return RapideType{}, fmt.Errorf("%w: function parameter %q has an invalid kind", ErrInvalidRapideType, parameter.name)
		}
		normalized[index] = rapideObjectParameter{
			kind: parameter.kind, name: name, typ: parameter.typ.node, hasDefault: parameter.hasDefault,
		}
	}
	if result.node == nil {
		result = EmptyRapideInterfaceType()
	}
	return RapideType{node: &rapideTypeNode{
		kind: rapideFunctionType, parameters: normalized, result: result.node,
	}}, nil
}

// NewRapideUnionType lowers Stanford's Union shorthand to its exact Chapter
// 10.3 polymorphic function type. It introduces no Union-specific nominal node:
//
//	function(type T; P : record Tag : function(O : Member) return T; ... end record) return T
//
// The ordinary function/interface subtype rules therefore derive tag-set width
// and member covariance. Union object literals and operations remain a separate
// value/executable-language concern.
func NewRapideUnionType(members ...RapideUnionMember) (RapideType, error) {
	if len(members) == 0 {
		return RapideType{}, fmt.Errorf("%w: a Union type requires at least one tag/member", ErrInvalidRapideType)
	}
	typeResult, err := NewRapideTypeParameterReference("T")
	if err != nil {
		return RapideType{}, err
	}
	scope := map[string]rapideTypeReferenceScopeKind{"t": rapideFunctionTypeParameterScope}
	seen := make(map[string]bool, len(members))
	recordMembers := make([]RapideInterfaceMember, len(members))
	for index, member := range members {
		name, err := canonicalRapideIdentifier(member.name)
		if err != nil {
			return RapideType{}, fmt.Errorf("%w: Union tag %d: %v", ErrInvalidRapideType, index, err)
		}
		if seen[name] {
			return RapideType{}, fmt.Errorf("%w: duplicate Union tag %q", ErrInvalidRapideType, name)
		}
		seen[name] = true
		if member.typ.node == nil {
			return RapideType{}, fmt.Errorf("%w: Union tag %q has an invalid member type", ErrInvalidRapideType, member.name)
		}
		tagFunction, err := NewRapideFunctionType(
			[]RapideFunctionParameter{RapideObjectParameter("O", member.typ)},
			typeResult,
		)
		if err != nil {
			return RapideType{}, fmt.Errorf("%w: Union tag %q function reduction: %v", ErrInvalidRapideType, member.name, err)
		}
		recordMembers[index] = ProvidedRapideMember(name, tagFunction)
	}
	record, err := newRapideInterfaceTypeWithScope(scope, recordMembers...)
	if err != nil {
		return RapideType{}, fmt.Errorf("%w: Union tag record: %v", ErrInvalidRapideType, err)
	}
	result, err := NewRapideFunctionType(
		[]RapideFunctionParameter{
			RapideTypeParameter("T"),
			RapideObjectParameter("P", record),
		},
		typeResult,
	)
	if err != nil {
		return RapideType{}, fmt.Errorf("%w: Union function reduction: %v", ErrInvalidRapideType, err)
	}
	if err := validateRapideTypeReferences(result.node, nil); err != nil {
		return RapideType{}, err
	}
	return result, nil
}

// NewRapideEnumerationType lowers Stanford's Enumeration shorthand to a Union
// whose distinct tags all carry Triv. It adds no Enumeration-specific nominal
// node or ordering; the Chapter 11 subtype rule is therefore the Union tag-set
// rule. Enumeration literals and iteration are separate executable concerns.
func NewRapideEnumerationType(literals ...string) (RapideType, error) {
	if len(literals) == 0 {
		return RapideType{}, fmt.Errorf("%w: an Enumeration type requires at least one literal", ErrInvalidRapideType)
	}
	triv, err := RapidePredefinedType("Triv")
	if err != nil {
		return RapideType{}, err
	}
	members := make([]RapideUnionMember, len(literals))
	for index, literal := range literals {
		members[index] = RapideUnionTag(literal, triv)
	}
	result, err := NewRapideUnionType(members...)
	if err != nil {
		return RapideType{}, fmt.Errorf("%w: Enumeration Union reduction: %v", ErrInvalidRapideType, err)
	}
	return result, nil
}

func publicRapideFunctionParameter(parameter rapideObjectParameter) RapideFunctionParameter {
	switch parameter.kind {
	case rapideObjectFunctionParameter:
		if parameter.hasDefault {
			return DefaultedRapideObjectParameter(parameter.name, RapideType{node: parameter.typ})
		}
		return RapideObjectParameter(parameter.name, RapideType{node: parameter.typ})
	case rapideTypeFunctionParameter:
		if parameter.typ == nil {
			return RapideTypeParameter(parameter.name)
		}
		return BoundedRapideTypeParameter(parameter.name, RapideType{node: parameter.typ})
	default:
		return RapideFunctionParameter{kind: parameter.kind, name: parameter.name}
	}
}

func rapideEmptyFunctionResult(function *rapideTypeNode) bool {
	return function != nil && function.kind == rapideFunctionType && function.result != nil &&
		function.result.kind == rapideInterfaceType && function.result.predefined == "" &&
		len(function.result.members) == 0 && len(function.result.parameters) == 0 &&
		len(function.result.eventParams) == 0 && function.result.result == nil
}

type rapideTypeReferenceScopeKind uint8

const (
	rapideInterfaceTypeNameScope rapideTypeReferenceScopeKind = iota + 1
	rapideFunctionTypeParameterScope
)

func validateRapideTypeReferences(node *rapideTypeNode, scope map[string]rapideTypeReferenceScopeKind) error {
	return validateRapideTypeReferencesOnPath(node, scope, make(map[*rapideTypeNode]bool))
}

func validateRapideTypeReferencesOnPath(
	node *rapideTypeNode,
	scope map[string]rapideTypeReferenceScopeKind,
	onPath map[*rapideTypeNode]bool,
) error {
	if node == nil {
		return fmt.Errorf("%w: nil nested descriptor", ErrInvalidRapideType)
	}
	if onPath[node] {
		return nil
	}
	onPath[node] = true
	defer delete(onPath, node)
	switch node.kind {
	case rapideTypeNameReferenceType:
		if node.reference == "" || node.predefined != "" || len(node.members) != 0 ||
			len(node.parameters) != 0 || len(node.eventParams) != 0 || node.result != nil {
			return fmt.Errorf("%w: malformed type-name reference", ErrInvalidRapideType)
		}
		if scope == nil || scope[node.reference] == 0 {
			return fmt.Errorf("%w: unscoped type-name reference %q", ErrInvalidRapideType, node.reference)
		}
		return nil
	case rapidePredefinedLibraryType:
		if node.reference != "" {
			return fmt.Errorf("%w: malformed predefined library descriptor", ErrInvalidRapideType)
		}
		return nil
	case rapideInterfaceType:
		if node.reference != "" {
			return fmt.Errorf("%w: malformed interface descriptor", ErrInvalidRapideType)
		}
		// An interface type establishes a fresh namespace for its own type-name
		// constituents. Lexically enclosing function type parameters remain
		// visible, but type-name constituents of an outer interface do not leak
		// into an independently nested interface type.
		local := make(map[string]rapideTypeReferenceScopeKind, len(scope)+1)
		for name, kind := range scope {
			if kind == rapideFunctionTypeParameterScope {
				local[name] = kind
			}
		}
		for _, member := range node.members {
			if member.kind == rapideTypeNameConstituent {
				local[member.name] = rapideInterfaceTypeNameScope
			}
		}
		for _, member := range node.members {
			if member.typ != nil {
				if err := validateRapideTypeReferencesOnPath(member.typ, local, onPath); err != nil {
					return fmt.Errorf("%w: interface member %q: %v", ErrInvalidRapideType, member.name, err)
				}
			}
			if member.constructorResult != nil {
				if err := validateRapideTypeReferencesOnPath(member.constructorResult, local, onPath); err != nil {
					return fmt.Errorf("%w: type-constructor member %q result: %v", ErrInvalidRapideType, member.name, err)
				}
			}
		}
		return nil
	case rapideFunctionType:
		if node.reference != "" {
			return fmt.Errorf("%w: malformed function descriptor", ErrInvalidRapideType)
		}
		local := cloneRapideTypeReferenceScope(scope)
		for _, parameter := range node.parameters {
			if parameter.typ != nil {
				if err := validateRapideTypeReferencesOnPath(parameter.typ, local, onPath); err != nil {
					return fmt.Errorf("%w: function parameter %q: %v", ErrInvalidRapideType, parameter.name, err)
				}
			}
			if parameter.kind == rapideTypeFunctionParameter {
				local[parameter.name] = rapideFunctionTypeParameterScope
			}
		}
		return validateRapideTypeReferencesOnPath(node.result, local, onPath)
	case rapideEventInterfaceType:
		if node.reference != "" {
			return fmt.Errorf("%w: malformed event descriptor", ErrInvalidRapideType)
		}
		for _, parameter := range node.eventParams {
			if err := validateRapideTypeReferencesOnPath(parameter.typ, scope, onPath); err != nil {
				return fmt.Errorf("%w: event parameter %q: %v", ErrInvalidRapideType, parameter.name, err)
			}
		}
		return nil
	default:
		return fmt.Errorf("%w: unknown descriptor kind %d", ErrInvalidRapideType, node.kind)
	}
}

func cloneRapideTypeReferenceScope(scope map[string]rapideTypeReferenceScopeKind) map[string]rapideTypeReferenceScopeKind {
	result := make(map[string]rapideTypeReferenceScopeKind, len(scope)+1)
	for name, kind := range scope {
		if kind != 0 {
			result[name] = kind
		}
	}
	return result
}

// RapideIteratorType expands the predefined Iterator(T) interface supported by
// this slice. It has provided Item() returning T and More() returning Boolean.
// Executing an iterator object remains a separate executable-language feature.
func RapideIteratorType(item RapideType) (RapideType, error) {
	if item.node == nil {
		return RapideType{}, fmt.Errorf("%w: Iterator item type is invalid", ErrInvalidRapideType)
	}
	booleanType, err := RapidePredefinedType("Boolean")
	if err != nil {
		return RapideType{}, err
	}
	itemFunction, err := NewRapideFunctionType(nil, item)
	if err != nil {
		return RapideType{}, err
	}
	moreFunction, err := NewRapideFunctionType(nil, booleanType)
	if err != nil {
		return RapideType{}, err
	}
	return NewRapideInterfaceType(
		ProvidedRapideMember("Item", itemFunction),
		ProvidedRapideMember("More", moreFunction),
	)
}

// RapideIterableType expands the predefined Iterable(T) interface from the
// Predefined Types LRM. Its Iterator constituent is a zero-parameter module
// generator returning Iterator(T), not an ordinary function object. The
// generator signature makes Iterable covariant through the structural rules;
// executable allocation remains the module-generator runtime's responsibility.
func RapideIterableType(item RapideType) (RapideType, error) {
	if item.node == nil {
		return RapideType{}, fmt.Errorf("%w: Iterable item type is invalid", ErrInvalidRapideType)
	}
	iterator, err := RapideIteratorType(item)
	if err != nil {
		return RapideType{}, err
	}
	signature, err := NewRapideFunctionType(nil, iterator)
	if err != nil {
		return RapideType{}, err
	}
	return NewRapideInterfaceType(
		ProvidedRapideModuleGenerator("Iterator", signature),
	)
}

// RapideDiscreteType expands the predefined Discrete(T) interface from the
// Predefined Types LRM. The rhs parameters of < and = are contravariant while
// Succ's result is covariant, so the ordinary structural rules make
// Discrete(T1) a subtype of Discrete(T2) exactly when T1 and T2 are equal.
func RapideDiscreteType(item RapideType) (RapideType, error) {
	if item.node == nil {
		return RapideType{}, fmt.Errorf("%w: Discrete item type is invalid", ErrInvalidRapideType)
	}
	booleanType, err := RapidePredefinedType("Boolean")
	if err != nil {
		return RapideType{}, err
	}
	comparison, err := NewRapideFunctionType(
		[]RapideFunctionParameter{RapideObjectParameter("rhs", item)}, booleanType,
	)
	if err != nil {
		return RapideType{}, err
	}
	successor, err := NewRapideFunctionType(nil, item)
	if err != nil {
		return RapideType{}, err
	}
	return NewRapideInterfaceType(
		ProvidedRapideMember("<", comparison),
		ProvidedRapideMember("=", comparison),
		ProvidedRapideMember("Succ", successor),
	)
}

// RapideEqualityType expands the predefined Equality(T) interface from
// Predefined Types LRM Chapter 3. Both functions accept T and return Boolean;
// ordinary function-parameter contravariance derives the structural subtype
// behavior without a nominal Equality node.
func RapideEqualityType(item RapideType) (RapideType, error) {
	if item.node == nil {
		return RapideType{}, fmt.Errorf("%w: Equality item type is invalid", ErrInvalidRapideType)
	}
	booleanType, err := RapidePredefinedType("Boolean")
	if err != nil {
		return RapideType{}, err
	}
	comparison, err := NewRapideFunctionType(
		[]RapideFunctionParameter{RapideObjectParameter("rhs", item)}, booleanType,
	)
	if err != nil {
		return RapideType{}, err
	}
	return NewRapideInterfaceType(
		ProvidedRapideMember("=", comparison),
		ProvidedRapideMember("/=", comparison),
	)
}

// RapideOrderType expands the predefined Order(T) interface from Chapter 4.
// It includes the exact Equality(T) operations plus the four ordinary ordering
// functions. The LRM's algebraic laws remain behavioral obligations rather than
// being inferred from the existence of these structural names.
func RapideOrderType(item RapideType) (RapideType, error) {
	if item.node == nil {
		return RapideType{}, fmt.Errorf("%w: Order item type is invalid", ErrInvalidRapideType)
	}
	booleanType, err := RapidePredefinedType("Boolean")
	if err != nil {
		return RapideType{}, err
	}
	comparison, err := NewRapideFunctionType(
		[]RapideFunctionParameter{RapideObjectParameter("rhs", item)}, booleanType,
	)
	if err != nil {
		return RapideType{}, err
	}
	return NewRapideInterfaceType(
		ProvidedRapideMember("=", comparison),
		ProvidedRapideMember("/=", comparison),
		ProvidedRapideMember("<", comparison),
		ProvidedRapideMember("<=", comparison),
		ProvidedRapideMember(">", comparison),
		ProvidedRapideMember(">=", comparison),
	)
}

// RapideSetType expands the predefined Set(Element) interface from Chapter 15.
// Element must itself provide the published Equality(Element) contract. The
// result is a recursive structural interface because most operations consume
// or return the same Set(Element) type; no host map or collection value is
// introduced by constructing this type descriptor.
func RapideSetType(element RapideType) (RapideType, error) {
	if element.node == nil {
		return RapideType{}, fmt.Errorf("%w: Set element type is invalid", ErrInvalidRapideType)
	}
	supportsEquality, err := rapideTypeSupportsEquality(element)
	if err != nil {
		return RapideType{}, fmt.Errorf("%w: Set element equality constraint: %v", ErrInvalidRapideType, err)
	}
	if !supportsEquality {
		return RapideType{}, fmt.Errorf(
			"%w: Set element type does not subtype Equality(element)", ErrInvalidRapideType,
		)
	}
	booleanType, err := RapidePredefinedType("Boolean")
	if err != nil {
		return RapideType{}, err
	}
	naturalType, err := RapidePredefinedType("Natural")
	if err != nil {
		return RapideType{}, err
	}
	return NewSelfRecursiveRapideInterfaceType(func(self RapideType) (RapideType, error) {
		setComparison, err := NewRapideFunctionType(
			[]RapideFunctionParameter{RapideObjectParameter("rhs", self)}, booleanType,
		)
		if err != nil {
			return RapideType{}, err
		}
		membership, err := NewRapideFunctionType(
			[]RapideFunctionParameter{RapideObjectParameter("E", element)}, booleanType,
		)
		if err != nil {
			return RapideType{}, err
		}
		addElement, err := NewRapideFunctionType(
			[]RapideFunctionParameter{RapideObjectParameter("E", element)}, self,
		)
		if err != nil {
			return RapideType{}, err
		}
		setOperation, err := NewRapideFunctionType(
			[]RapideFunctionParameter{RapideObjectParameter("S", self)}, self,
		)
		if err != nil {
			return RapideType{}, err
		}
		removeElement, err := NewRapideFunctionType(
			[]RapideFunctionParameter{RapideObjectParameter("E", element)}, self,
		)
		if err != nil {
			return RapideType{}, err
		}
		cardinality, err := NewRapideFunctionType(nil, naturalType)
		if err != nil {
			return RapideType{}, err
		}
		return NewRapideInterfaceType(
			ProvidedRapideMember("=", setComparison),
			ProvidedRapideMember("/=", setComparison),
			ProvidedRapideMember("Is_Member", membership),
			ProvidedRapideMember("+", addElement),
			ProvidedRapideMember("&", setOperation),
			ProvidedRapideMember("-", removeElement),
			ProvidedRapideMember("-", setOperation),
			ProvidedRapideMember("<", setComparison),
			ProvidedRapideMember("Cardinality", cardinality),
		)
	})
}

// RapideClockType expands the base predefined Clock interface from Chapter 17.
// Ticks is a local type name bounded by Natural; Now and the three event-query
// functions return that symbolic local type. This is structural type metadata
// only and neither reads wall time nor advances an executable simulation clock.
func RapideClockType() (RapideType, error) {
	members, _, err := rapideClockMembers()
	if err != nil {
		return RapideType{}, err
	}
	return NewRapideInterfaceType(members...)
}

func rapideClockMembers() ([]RapideInterfaceMember, RapideType, error) {
	naturalType, err := RapidePredefinedType("Natural")
	if err != nil {
		return nil, RapideType{}, err
	}
	ticks, err := NewRapideTypeNameReference("Ticks")
	if err != nil {
		return nil, RapideType{}, err
	}
	eventType, err := NewRapideEventType()
	if err != nil {
		return nil, RapideType{}, err
	}
	now, err := NewRapideFunctionType(nil, ticks)
	if err != nil {
		return nil, RapideType{}, err
	}
	eventQuery, err := NewRapideFunctionType(
		[]RapideFunctionParameter{RapideObjectParameter("E", eventType)}, ticks,
	)
	if err != nil {
		return nil, RapideType{}, err
	}
	return []RapideInterfaceMember{
		BoundedProvidedRapideTypeName("Ticks", naturalType),
		ProvidedRapideMember("Now", now),
		ProvidedRapideMember("Start", eventQuery),
		ProvidedRapideMember("Finish", eventQuery),
		ProvidedRapideMember("Length", eventQuery),
	}, ticks, nil
}

// RapideAccuracyType expands the predefined Accuracy Record from Chapter 17.
// Its Measure field has type Float because the LRM explicitly makes value
// constraints type-indistinguishable; the 0.0..1.0 range remains a mandatory
// runtime membership check once Accuracy values are admitted.
func RapideAccuracyType() (RapideType, error) {
	kind, err := NewRapideEnumerationType("Interval", "Ratio")
	if err != nil {
		return RapideType{}, err
	}
	floatType, err := RapidePredefinedType("Float")
	if err != nil {
		return RapideType{}, err
	}
	return NewRapideInterfaceType(
		ProvidedRapideMember("Kind", kind),
		ProvidedRapideMember("Measure", floatType),
	)
}

func rapideSynchronousClockMembers() ([]RapideInterfaceMember, error) {
	members, ticks, err := rapideClockMembers()
	if err != nil {
		return nil, err
	}
	gst, err := RapidePredefinedType("GST")
	if err != nil {
		return nil, err
	}
	gstAtTick, err := NewRapideFunctionType(
		[]RapideFunctionParameter{RapideObjectParameter("T", ticks)}, gst,
	)
	if err != nil {
		return nil, err
	}
	distance, err := NewRapideFunctionType([]RapideFunctionParameter{
		RapideObjectParameter("T1", ticks),
		RapideObjectParameter("T2", ticks),
	}, gst)
	if err != nil {
		return nil, err
	}
	ticksAtGST, err := NewRapideFunctionType(
		[]RapideFunctionParameter{RapideObjectParameter("D", gst)}, ticks,
	)
	if err != nil {
		return nil, err
	}
	return append(members,
		ProvidedRapideMember("GST", gstAtTick),
		ProvidedRapideMember("Distance", distance),
		ProvidedRapideMember("Ticks", ticksAtGST),
	), nil
}

// RapideSynchronousClockType retains the published draft boundary explicitly.
// Synchronous_Clock includes Clock's type-name constituent Ticks and also
// declares a function named Ticks, while the Type LRM forbids type-name
// identifiers from colliding with any other interface constituent. No exact
// structural type can be returned until that primary-source conflict is
// resolved by stronger evidence or a named compatibility profile.
func RapideSynchronousClockType() (RapideType, error) {
	members, err := rapideSynchronousClockMembers()
	if err != nil {
		return RapideType{}, err
	}
	result, err := NewRapideInterfaceType(members...)
	if err == nil {
		return result, nil
	}
	return RapideType{}, fmt.Errorf(
		"%w: published Synchronous_Clock function Ticks collides with inherited type-name Ticks under the Type LRM uniqueness rule: %v",
		ErrInvalidRapideType, err,
	)
}

// RapideRegularClockType remains gated because it includes the contradictory
// Synchronous_Clock declaration.
func RapideRegularClockType() (RapideType, error) {
	_, err := RapideSynchronousClockType()
	return RapideType{}, fmt.Errorf(
		"%w: published Regular_Clock includes unresolved Synchronous_Clock: %v",
		ErrInvalidRapideType, err,
	)
}

// RapideSlavedClockType remains gated because it includes and refers to the
// contradictory Synchronous_Clock declaration.
func RapideSlavedClockType() (RapideType, error) {
	_, err := RapideSynchronousClockType()
	return RapideType{}, fmt.Errorf(
		"%w: published Slaved_Clock includes and refers to unresolved Synchronous_Clock: %v",
		ErrInvalidRapideType, err,
	)
}

func rapideTypeSupportsEquality(item RapideType) (bool, error) {
	if _, predefined := item.PredefinedName(); predefined {
		// Every predefined type admitted by RapidePredefinedType has the
		// Chapter 3 equality operations in the recovered library.
		return true, nil
	}
	equality, err := RapideEqualityType(item)
	if err != nil {
		return false, err
	}
	return IsRapideSubtype(item, equality)
}

// RapideReferenceType expands the predefined Ref(T) interface. Assignment is
// a function on the reference object and returns that same recursive Ref(T)
// type; dereference returns T and Is_Nil returns Boolean. The opposing
// parameter/result variance makes Ref invariant in T, matching Chapter 16.
func RapideReferenceType(item RapideType) (RapideType, error) {
	if item.node == nil {
		return RapideType{}, fmt.Errorf("%w: Ref item type is invalid", ErrInvalidRapideType)
	}
	booleanType, err := RapidePredefinedType("Boolean")
	if err != nil {
		return RapideType{}, err
	}
	return NewSelfRecursiveRapideInterfaceType(func(self RapideType) (RapideType, error) {
		assignment, err := NewRapideFunctionType(
			[]RapideFunctionParameter{RapideObjectParameter("rhs", item)}, self,
		)
		if err != nil {
			return RapideType{}, err
		}
		dereference, err := NewRapideFunctionType(nil, item)
		if err != nil {
			return RapideType{}, err
		}
		isNil, err := NewRapideFunctionType(nil, booleanType)
		if err != nil {
			return RapideType{}, err
		}
		return NewRapideInterfaceType(
			ProvidedRapideMember(":=", assignment),
			ProvidedRapideMember("$", dereference),
			ProvidedRapideMember("Is_Nil", isNil),
		)
	})
}

// ApplyRapideTypeConstructor evaluates one closed predefined type-constructor
// application into its structural denotation. This includes Equality, Order,
// Set, Iterator, Iterable, Discrete, and Ref. Constructor spelling is not
// retained as nominal identity. Range remains an explicit boundary because the draft
// Range chapter conflicts with Iterable's module-generator definition and is
// marked obsolete by its own editor's note.
func ApplyRapideTypeConstructor(name string, arguments ...RapideType) (RapideType, error) {
	canonical, err := canonicalRapideIdentifier(name)
	if err != nil {
		return RapideType{}, fmt.Errorf("%w: type-constructor name: %v", ErrInvalidRapideType, err)
	}
	switch canonical {
	case "iterator", "discrete", "ref", "iterable", "equality", "order", "set", "range":
	default:
		return RapideType{}, fmt.Errorf(
			"%w: unknown closed predefined type constructor %q",
			ErrInvalidRapideType, name,
		)
	}
	if len(arguments) != 1 {
		return RapideType{}, fmt.Errorf(
			"%w: predefined type constructor %s has %d arguments, want 1",
			ErrInvalidRapideType, name, len(arguments),
		)
	}
	if arguments[0].node == nil {
		return RapideType{}, fmt.Errorf(
			"%w: predefined type constructor %s has an invalid argument",
			ErrInvalidRapideType, name,
		)
	}
	switch canonical {
	case "iterator":
		return RapideIteratorType(arguments[0])
	case "discrete":
		return RapideDiscreteType(arguments[0])
	case "ref":
		return RapideReferenceType(arguments[0])
	case "iterable":
		return RapideIterableType(arguments[0])
	case "equality":
		return RapideEqualityType(arguments[0])
	case "order":
		return RapideOrderType(arguments[0])
	case "set":
		return RapideSetType(arguments[0])
	case "range":
		return RapideType{}, fmt.Errorf(
			"%w: predefined type constructor %s is withheld because the published draft denotation is obsolete and conflicts with Iterable's module-generator constituent",
			ErrInvalidRapideType, name,
		)
	}
	return RapideType{}, fmt.Errorf("%w: unreachable type-constructor application %q", ErrInvalidRapideType, name)
}

// IsRapideSubtype applies the published Stanford Rapide subtype rules to the
// supported descriptor subset. Circular subtype subgoals are provisionally
// true as required by Type LRM Section 4.4; recursive interface construction
// exposes those coinductive obligations without nominal source identity.
func IsRapideSubtype(left, right RapideType) (bool, error) {
	if left.node == nil || right.node == nil {
		return false, fmt.Errorf("%w: subtype operands must both be valid", ErrInvalidRapideType)
	}
	if err := validateRapideTypeReferences(left.node, nil); err != nil {
		return false, err
	}
	if err := validateRapideTypeReferences(right.node, nil); err != nil {
		return false, err
	}
	checker := rapideSubtypeChecker{states: make(map[rapideSubtypePair]rapideSubtypeState)}
	return checker.subtype(left.node, right.node)
}

// RapideTypesEqual implements Rapide type equality as mutual subtyping. It is
// intentionally not Go struct or pointer equality.
func RapideTypesEqual(left, right RapideType) (bool, error) {
	leftToRight, err := IsRapideSubtype(left, right)
	if err != nil || !leftToRight {
		return leftToRight, err
	}
	return IsRapideSubtype(right, left)
}

type rapideSubtypePair struct {
	left  *rapideTypeNode
	right *rapideTypeNode
}

type rapideSubtypeState uint8

const (
	rapideSubtypeVisiting rapideSubtypeState = iota + 1
	rapideSubtypeFalse
	rapideSubtypeTrue
)

type rapideSubtypeChecker struct {
	states map[rapideSubtypePair]rapideSubtypeState
}

func (checker *rapideSubtypeChecker) subtype(left, right *rapideTypeNode) (bool, error) {
	pair := rapideSubtypePair{left: left, right: right}
	switch checker.states[pair] {
	case rapideSubtypeVisiting, rapideSubtypeTrue:
		return true, nil
	case rapideSubtypeFalse:
		return false, nil
	}
	checker.states[pair] = rapideSubtypeVisiting

	var result bool
	var err error
	switch {
	case left == nil || right == nil:
		err = fmt.Errorf("%w: malformed nested subtype operand", ErrInvalidRapideType)
	case left.kind == rapideEventInterfaceType && right.kind == rapideInterfaceType:
		result, err = checker.eventInterfaceSubtype(left, right)
	case left.kind == rapidePredefinedLibraryType && right.kind == rapideInterfaceType:
		// Predefined library types denote interfaces even while their complete
		// recursive definitions remain opaque. Every interface subtypes the
		// empty interface; no other structural relation can be proved without
		// expanding the predefined declaration.
		result = len(right.members) == 0 && len(right.parameters) == 0 && right.result == nil && right.predefined == ""
	case left.kind != right.kind:
		result = false
	case left.kind == rapidePredefinedLibraryType:
		if left.predefined == "" || right.predefined == "" {
			err = fmt.Errorf("%w: malformed predefined library descriptor", ErrInvalidRapideType)
		} else {
			result = left.predefined == right.predefined ||
				left.predefined == "GST" && right.predefined == "Float"
		}
	case left.kind == rapideTypeNameReferenceType:
		if left.reference == "" || right.reference == "" {
			err = fmt.Errorf("%w: malformed type-name reference", ErrInvalidRapideType)
		} else {
			result = left.reference == right.reference
		}
	case left.kind == rapideFunctionType:
		result, err = checker.functionSubtype(left, right)
	case left.kind == rapideEventInterfaceType:
		result, err = checker.eventSubtype(left, right)
	case left.kind == rapideInterfaceType:
		result, err = checker.interfaceSubtype(left, right)
	default:
		err = fmt.Errorf("%w: unknown descriptor kind %d", ErrInvalidRapideType, left.kind)
	}
	if err != nil {
		delete(checker.states, pair)
		return false, err
	}
	if result {
		checker.states[pair] = rapideSubtypeTrue
	} else {
		checker.states[pair] = rapideSubtypeFalse
	}
	return result, nil
}

func (checker *rapideSubtypeChecker) functionSubtype(left, right *rapideTypeNode) (bool, error) {
	if left.result == nil || right.result == nil {
		return false, fmt.Errorf("%w: function result descriptor is missing", ErrInvalidRapideType)
	}
	if len(left.parameters) > len(right.parameters) {
		for _, parameter := range left.parameters[len(right.parameters):] {
			if !parameter.hasDefault {
				return false, nil
			}
		}
	}
	shared := len(left.parameters)
	if len(right.parameters) < shared {
		shared = len(right.parameters)
	}
	for index := 0; index < shared; index++ {
		leftParameter := left.parameters[index]
		rightParameter := right.parameters[index]
		if leftParameter.name == "" || rightParameter.name == "" || leftParameter.name != rightParameter.name ||
			leftParameter.kind != rightParameter.kind {
			return false, nil
		}
		switch leftParameter.kind {
		case rapideObjectFunctionParameter:
			// Object parameters are contravariant: F2's parameter type must be
			// a subtype of F1's corresponding parameter type.
			compatible, err := checker.subtype(rightParameter.typ, leftParameter.typ)
			if err != nil || !compatible {
				return compatible, err
			}
		case rapideTypeFunctionParameter:
			// An unbounded F1 parameter accepts every corresponding type
			// denotation. If F1 is bounded by T1, F2 must be bounded by T2
			// with T2 <: T1 (Type LRM Section 5.3 rule 3).
			if leftParameter.typ == nil {
				continue
			}
			if rightParameter.typ == nil {
				return false, nil
			}
			compatible, err := checker.subtype(rightParameter.typ, leftParameter.typ)
			if err != nil || !compatible {
				return compatible, err
			}
		default:
			return false, fmt.Errorf("%w: function parameter %q has an invalid kind", ErrInvalidRapideType, leftParameter.name)
		}
	}
	// Function results are covariant.
	return checker.subtype(left.result, right.result)
}

func (checker *rapideSubtypeChecker) interfaceSubtype(left, right *rapideTypeNode) (bool, error) {
	// Type-name declarations cannot appear in requires. Every target public
	// type name needs a public source specification; every target private type
	// name may be covered publicly or privately. Type names cannot overload,
	// so each search has at most one legal candidate.
	for _, target := range right.members {
		if target.kind != rapideTypeNameConstituent {
			continue
		}
		allowedRegions := []rapideInterfaceRegion{rapideProvidesRegion}
		if target.region == rapidePrivateRegion {
			allowedRegions = append(allowedRegions, rapidePrivateRegion)
		}
		matched := false
		for _, source := range left.members {
			if source.kind != rapideTypeNameConstituent || source.name != target.name ||
				!rapideRegionAllowed(source.region, allowedRegions) {
				continue
			}
			compatible, err := checker.typeNameSpecificationSubtype(source, target)
			if err != nil {
				return false, err
			}
			if compatible {
				matched = true
				break
			}
		}
		if !matched {
			return false, nil
		}
	}
	// Type-constructor names follow the same provides/private region rule,
	// but first require characteristic-function conformance. This public tree
	// subset represents only application-independent result bounds/bodies;
	// parameter-dependent substitution waits for symbolic type graphs.
	for _, target := range right.members {
		if target.kind != rapideTypeConstructorConstituent {
			continue
		}
		allowedRegions := []rapideInterfaceRegion{rapideProvidesRegion}
		if target.region == rapidePrivateRegion {
			allowedRegions = append(allowedRegions, rapidePrivateRegion)
		}
		matched := false
		for _, source := range left.members {
			if source.kind != rapideTypeConstructorConstituent || source.name != target.name ||
				!rapideRegionAllowed(source.region, allowedRegions) {
				continue
			}
			compatible, err := checker.typeConstructorSpecificationSubtype(source, target)
			if err != nil {
				return false, err
			}
			if compatible {
				matched = true
				break
			}
		}
		if !matched {
			return false, nil
		}
	}
	// Every target provided object, module generator, or exception must be
	// covered by a same-kind source provides constituent. Generator conformance
	// uses its function signature while exception conformance uses its event
	// parameter record. Each overload is checked independently.
	for _, target := range right.members {
		if target.kind != rapideObjectConstituent && target.kind != rapideModuleGeneratorConstituent &&
			target.kind != rapideExceptionConstituent ||
			target.region != rapideProvidesRegion {
			continue
		}
		matched, err := checker.matchCovariantMember(left, target, rapideProvidesRegion)
		if err != nil || !matched {
			return matched, err
		}
	}
	// A target private constituent may be implemented publicly or privately
	// by the source.
	for _, target := range right.members {
		if target.kind != rapideObjectConstituent && target.kind != rapideModuleGeneratorConstituent &&
			target.kind != rapideExceptionConstituent ||
			target.region != rapidePrivateRegion {
			continue
		}
		matched, err := checker.matchCovariantMember(left, target, rapideProvidesRegion, rapidePrivateRegion)
		if err != nil || !matched {
			return matched, err
		}
	}
	// Requirements reverse direction: every source requirement must exist in
	// the target, and the target requirement's type is the subtype.
	for _, source := range left.members {
		if source.kind != rapideObjectConstituent && source.kind != rapideModuleGeneratorConstituent &&
			source.kind != rapideExceptionConstituent ||
			source.region != rapideRequiresRegion {
			continue
		}
		matched := false
		for _, target := range right.members {
			if target.kind != source.kind || target.region != rapideRequiresRegion || target.name != source.name {
				continue
			}
			compatible, err := checker.subtype(target.typ, source.typ)
			if err != nil {
				return false, err
			}
			if compatible {
				matched = true
				break
			}
		}
		if !matched {
			return false, nil
		}
	}
	// Every target action must be covered by a same-named, same-mode source
	// action whose event type is a subtype. Action overload matching is
	// existential and does not consume a source declaration.
	for _, target := range right.members {
		if target.kind != rapideActionConstituent {
			continue
		}
		matched := false
		for _, source := range left.members {
			if source.kind != rapideActionConstituent || source.name != target.name || source.actionMode != target.actionMode {
				continue
			}
			compatible, err := checker.subtype(source.typ, target.typ)
			if err != nil {
				return false, err
			}
			if compatible {
				matched = true
				break
			}
		}
		if !matched {
			return false, nil
		}
	}
	return true, nil
}

func (checker *rapideSubtypeChecker) typeConstructorSpecificationSubtype(
	source, target rapideInterfaceConstituent,
) (bool, error) {
	if source.kind != rapideTypeConstructorConstituent || target.kind != rapideTypeConstructorConstituent {
		return false, fmt.Errorf("%w: non-constructor constituent supplied to type-constructor subtype rule", ErrInvalidRapideType)
	}
	if source.typ == nil || target.typ == nil || !rapideEmptyFunctionResult(source.typ) || !rapideEmptyFunctionResult(target.typ) {
		return false, fmt.Errorf("%w: type-constructor characteristic function is malformed", ErrInvalidRapideType)
	}
	conforms, err := checker.subtype(source.typ, target.typ)
	if err != nil || !conforms {
		return conforms, err
	}
	switch target.typeSpecification {
	case rapideAnyTypeDenotation:
		return true, nil
	case rapideSubtypeTypeDenotation:
		if target.constructorResult == nil {
			return false, fmt.Errorf("%w: bounded target type-constructor %q has no result bound", ErrInvalidRapideType, target.name)
		}
		switch source.typeSpecification {
		case rapideSubtypeTypeDenotation, rapideExactTypeDenotation:
			if source.constructorResult == nil {
				return false, fmt.Errorf("%w: constrained source type-constructor %q has no result type", ErrInvalidRapideType, source.name)
			}
			return checker.subtype(source.constructorResult, target.constructorResult)
		case rapideAnyTypeDenotation:
			return false, nil
		default:
			return false, fmt.Errorf("%w: source type-constructor %q has an invalid specification", ErrInvalidRapideType, source.name)
		}
	case rapideExactTypeDenotation:
		if target.constructorResult == nil {
			return false, fmt.Errorf("%w: exact target type-constructor %q has no result type", ErrInvalidRapideType, target.name)
		}
		if source.typeSpecification != rapideExactTypeDenotation || source.constructorResult == nil {
			return false, nil
		}
		return checker.sameClosedTypeConstructors(source, target)
	default:
		return false, fmt.Errorf("%w: target type-constructor %q has an invalid specification", ErrInvalidRapideType, target.name)
	}
}

func (checker *rapideSubtypeChecker) sameClosedTypeConstructors(
	left, right rapideInterfaceConstituent,
) (bool, error) {
	if left.typ == nil || right.typ == nil || left.constructorResult == nil || right.constructorResult == nil ||
		len(left.typ.parameters) != len(right.typ.parameters) {
		return false, nil
	}
	for index := range left.typ.parameters {
		leftParameter := left.typ.parameters[index]
		rightParameter := right.typ.parameters[index]
		if leftParameter.kind != rightParameter.kind || leftParameter.name != rightParameter.name ||
			leftParameter.hasDefault != rightParameter.hasDefault || (leftParameter.typ == nil) != (rightParameter.typ == nil) {
			return false, nil
		}
		if leftParameter.typ != nil {
			equal, err := checker.typesEqual(leftParameter.typ, rightParameter.typ)
			if err != nil || !equal {
				return equal, err
			}
		}
	}
	return checker.typesEqual(left.constructorResult, right.constructorResult)
}

func (checker *rapideSubtypeChecker) typesEqual(left, right *rapideTypeNode) (bool, error) {
	forward, err := checker.subtype(left, right)
	if err != nil || !forward {
		return forward, err
	}
	return checker.subtype(right, left)
}

func (checker *rapideSubtypeChecker) typeNameSpecificationSubtype(
	source, target rapideInterfaceConstituent,
) (bool, error) {
	if source.kind != rapideTypeNameConstituent || target.kind != rapideTypeNameConstituent {
		return false, fmt.Errorf("%w: non-type constituent supplied to type-name subtype rule", ErrInvalidRapideType)
	}
	switch target.typeSpecification {
	case rapideAnyTypeDenotation:
		return true, nil
	case rapideSubtypeTypeDenotation:
		if target.typ == nil {
			return false, fmt.Errorf("%w: bounded target type-name %q has no bound", ErrInvalidRapideType, target.name)
		}
		switch source.typeSpecification {
		case rapideSubtypeTypeDenotation, rapideExactTypeDenotation:
			if source.typ == nil {
				return false, fmt.Errorf("%w: constrained source type-name %q has no type", ErrInvalidRapideType, source.name)
			}
			return checker.subtype(source.typ, target.typ)
		case rapideAnyTypeDenotation:
			return false, nil
		default:
			return false, fmt.Errorf("%w: source type-name %q has an invalid specification", ErrInvalidRapideType, source.name)
		}
	case rapideExactTypeDenotation:
		if target.typ == nil {
			return false, fmt.Errorf("%w: exact target type-name %q has no type", ErrInvalidRapideType, target.name)
		}
		if source.typeSpecification != rapideExactTypeDenotation {
			return false, nil
		}
		if source.typ == nil {
			return false, fmt.Errorf("%w: exact source type-name %q has no type", ErrInvalidRapideType, source.name)
		}
		forward, err := checker.subtype(source.typ, target.typ)
		if err != nil || !forward {
			return forward, err
		}
		return checker.subtype(target.typ, source.typ)
	default:
		return false, fmt.Errorf("%w: target type-name %q has an invalid specification", ErrInvalidRapideType, target.name)
	}
}

func (checker *rapideSubtypeChecker) matchCovariantMember(
	sourceInterface *rapideTypeNode,
	target rapideInterfaceConstituent,
	allowedRegions ...rapideInterfaceRegion,
) (bool, error) {
	for _, source := range sourceInterface.members {
		if source.kind != target.kind || source.name != target.name || !rapideRegionAllowed(source.region, allowedRegions) {
			continue
		}
		compatible, err := checker.subtype(source.typ, target.typ)
		if err != nil {
			return false, err
		}
		if compatible {
			return true, nil
		}
	}
	return false, nil
}

func (checker *rapideSubtypeChecker) eventSubtype(left, right *rapideTypeNode) (bool, error) {
	// Event subtyping is exactly subtyping of the associated parameter
	// record: target fields are a subset and source field types are
	// covariant.
	for _, target := range right.eventParams {
		matched := false
		for _, source := range left.eventParams {
			if source.name != target.name {
				continue
			}
			compatible, err := checker.subtype(source.typ, target.typ)
			if err != nil {
				return false, err
			}
			if compatible {
				matched = true
				break
			}
		}
		if !matched {
			return false, nil
		}
	}
	return true, nil
}

func (checker *rapideSubtypeChecker) eventInterfaceSubtype(event, target *rapideTypeNode) (bool, error) {
	// The parameter record is included in the event interface. Common
	// Event_Operations are intentionally opaque in this slice, so only
	// structural obligations satisfied by parameter fields can be proved.
	for _, member := range target.members {
		if member.kind != rapideObjectConstituent {
			return false, nil
		}
		if member.region == rapideRequiresRegion {
			// The event source has no requirements; target-only requirements
			// do not obstruct I1 <: I2 under the interface rules.
			continue
		}
		matched := false
		for _, parameter := range event.eventParams {
			if parameter.name != member.name {
				continue
			}
			compatible, err := checker.subtype(parameter.typ, member.typ)
			if err != nil {
				return false, err
			}
			if compatible {
				matched = true
				break
			}
		}
		if !matched {
			return false, nil
		}
	}
	return true, nil
}

func rapideRegionAllowed(region rapideInterfaceRegion, allowed []rapideInterfaceRegion) bool {
	for _, candidate := range allowed {
		if region == candidate {
			return true
		}
	}
	return false
}

// MarshalCanonical serializes the normalized descriptor in a versioned,
// byte-stable form. The bytes identify the descriptor representation; semantic
// type equality must still be tested with RapideTypesEqual because Rapide
// defines equality through mutual subtyping.
func (typ RapideType) MarshalCanonical() ([]byte, error) {
	if typ.node == nil {
		return nil, fmt.Errorf("%w: zero type descriptor", ErrInvalidRapideType)
	}
	if err := validateRapideTypeReferences(typ.node, nil); err != nil {
		return nil, err
	}
	node, err := encodeCanonicalRapideTypeNode(typ.node)
	if err != nil {
		return nil, err
	}
	return json.Marshal(canonicalRapideTypeDocument{Format: RapideTypeFormat, Type: node})
}

// DescriptorDigest hashes MarshalCanonical with a domain-specific prefix. It
// is a descriptor/artifact digest, not a replacement for mutual-subtype type
// equality.
func (typ RapideType) DescriptorDigest() (string, error) {
	encoded, err := typ.MarshalCanonical()
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return rapideTypeDigestPrefix + hex.EncodeToString(digest[:]), nil
}

// ParseRapideType accepts only the exact canonical v2 representation and
// rebuilds it through the same validating constructors used by callers.
func ParseRapideType(encoded []byte) (RapideType, error) {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var document canonicalRapideTypeDocument
	if err := decoder.Decode(&document); err != nil {
		return RapideType{}, fmt.Errorf("%w: %v", ErrInvalidRapideType, err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return RapideType{}, fmt.Errorf("%w: %v", ErrInvalidRapideType, err)
	}
	if document.Format != RapideTypeFormat {
		return RapideType{}, fmt.Errorf("%w: format %q is not %q", ErrInvalidRapideType, document.Format, RapideTypeFormat)
	}
	typ, err := rapideTypeFromCanonical(document.Type)
	if err != nil {
		return RapideType{}, err
	}
	canonical, err := typ.MarshalCanonical()
	if err != nil {
		return RapideType{}, err
	}
	if !bytes.Equal(encoded, canonical) {
		return RapideType{}, fmt.Errorf("%w: descriptor bytes are not canonical", ErrInvalidRapideType)
	}
	return typ, nil
}

type canonicalRapideTypeDocument struct {
	Format string                  `json:"format"`
	Type   canonicalRapideTypeNode `json:"type"`
}

type canonicalRapideTypeNode struct {
	Kind            string                              `json:"kind"`
	Name            string                              `json:"name,omitempty"`
	Depth           *uint                               `json:"depth,omitempty"`
	Members         []canonicalRapideTypeMember         `json:"members,omitempty"`
	Parameters      []canonicalRapideTypeParameter      `json:"parameters,omitempty"`
	EventParameters []canonicalRapideEventTypeParameter `json:"event_parameters,omitempty"`
	Result          *canonicalRapideTypeNode            `json:"result,omitempty"`
}

type canonicalRapideTypeMember struct {
	Kind           string                   `json:"kind,omitempty"`
	Region         string                   `json:"region,omitempty"`
	Mode           string                   `json:"mode,omitempty"`
	Specification  string                   `json:"specification,omitempty"`
	Name           string                   `json:"name"`
	Characteristic *canonicalRapideTypeNode `json:"characteristic,omitempty"`
	Type           *canonicalRapideTypeNode `json:"type,omitempty"`
}

type canonicalRapideTypeParameter struct {
	Name       string                   `json:"name"`
	Kind       string                   `json:"kind,omitempty"`
	Type       *canonicalRapideTypeNode `json:"type,omitempty"`
	HasDefault bool                     `json:"has_default,omitempty"`
}

type canonicalRapideEventTypeParameter struct {
	Name string                  `json:"name"`
	Type canonicalRapideTypeNode `json:"type"`
}

func encodeCanonicalRapideTypeNode(node *rapideTypeNode) (canonicalRapideTypeNode, error) {
	return encodeCanonicalRapideTypeNodeWithContext(node, nil)
}

func encodeCanonicalRapideTypeNodeWithContext(
	node *rapideTypeNode,
	binders []*rapideTypeNode,
) (canonicalRapideTypeNode, error) {
	if node == nil {
		return canonicalRapideTypeNode{}, fmt.Errorf("%w: nil nested descriptor", ErrInvalidRapideType)
	}
	for index := len(binders) - 1; index >= 0; index-- {
		if node == binders[index] {
			depth := uint(len(binders) - 1 - index)
			return canonicalRapideTypeNode{Kind: "recursive_reference", Depth: &depth}, nil
		}
	}
	switch node.kind {
	case rapideTypeNameReferenceType:
		if node.reference == "" || node.predefined != "" || len(node.members) != 0 ||
			len(node.parameters) != 0 || len(node.eventParams) != 0 || node.result != nil {
			return canonicalRapideTypeNode{}, fmt.Errorf("%w: malformed type-name reference", ErrInvalidRapideType)
		}
		return canonicalRapideTypeNode{Kind: "type_reference", Name: node.reference}, nil
	case rapidePredefinedLibraryType:
		if !canonicalPredefinedDescriptorName(node.predefined) || node.reference != "" || len(node.members) != 0 || len(node.parameters) != 0 || len(node.eventParams) != 0 || node.result != nil {
			return canonicalRapideTypeNode{}, fmt.Errorf("%w: malformed predefined library descriptor", ErrInvalidRapideType)
		}
		return canonicalRapideTypeNode{Kind: "predefined", Name: node.predefined}, nil
	case rapideInterfaceType:
		if node.predefined != "" || node.reference != "" || len(node.parameters) != 0 || len(node.eventParams) != 0 || node.result != nil {
			return canonicalRapideTypeNode{}, fmt.Errorf("%w: malformed interface descriptor", ErrInvalidRapideType)
		}
		kind := "interface"
		memberBinders := binders
		if rapideInterfaceReachesItself(node, binders) {
			kind = "recursive_interface"
			memberBinders = append(append([]*rapideTypeNode(nil), binders...), node)
		}
		result := canonicalRapideTypeNode{Kind: kind}
		for index, member := range node.members {
			canonicalMember := canonicalRapideTypeMember{Name: member.name}
			if member.typ != nil && member.kind != rapideTypeConstructorConstituent {
				typ, err := encodeCanonicalRapideTypeNodeWithContext(member.typ, memberBinders)
				if err != nil {
					return canonicalRapideTypeNode{}, fmt.Errorf("%w: member %d: %v", ErrInvalidRapideType, index, err)
				}
				canonicalMember.Type = &typ
			}
			switch member.kind {
			case rapideObjectConstituent:
				if member.typ == nil || member.region < rapideProvidesRegion || member.region > rapideRequiresRegion ||
					member.actionMode != 0 || member.typeSpecification != 0 || member.constructorResult != nil {
					return canonicalRapideTypeNode{}, fmt.Errorf("%w: object member %d is malformed", ErrInvalidRapideType, index)
				}
				canonicalMember.Region = rapideRegionName(member.region)
			case rapideModuleGeneratorConstituent:
				if member.typ == nil || member.typ.kind != rapideFunctionType || member.typ.result == nil ||
					member.typ.result.kind != rapideInterfaceType ||
					member.region < rapideProvidesRegion || member.region > rapideRequiresRegion ||
					member.actionMode != 0 || member.typeSpecification != 0 || member.constructorResult != nil {
					return canonicalRapideTypeNode{}, fmt.Errorf("%w: module-generator member %d is malformed", ErrInvalidRapideType, index)
				}
				canonicalMember.Kind = "module_generator"
				canonicalMember.Region = rapideRegionName(member.region)
			case rapideActionConstituent:
				if member.typ == nil || member.typ.kind != rapideEventInterfaceType ||
					(member.actionMode != rapideActionIn && member.actionMode != rapideActionOut) ||
					member.region != 0 || member.typeSpecification != 0 || member.constructorResult != nil {
					return canonicalRapideTypeNode{}, fmt.Errorf("%w: action member %d is malformed", ErrInvalidRapideType, index)
				}
				canonicalMember.Kind = "action"
				canonicalMember.Mode = rapideActionModeName(member.actionMode)
			case rapideExceptionConstituent:
				if member.typ == nil || member.typ.kind != rapideEventInterfaceType ||
					member.region < rapideProvidesRegion || member.region > rapideRequiresRegion ||
					member.actionMode != 0 || member.typeSpecification != 0 || member.constructorResult != nil {
					return canonicalRapideTypeNode{}, fmt.Errorf("%w: exception member %d is malformed", ErrInvalidRapideType, index)
				}
				canonicalMember.Kind = "exception"
				canonicalMember.Region = rapideRegionName(member.region)
			case rapideTypeNameConstituent:
				if member.region != rapideProvidesRegion && member.region != rapidePrivateRegion || member.actionMode != 0 || member.constructorResult != nil {
					return canonicalRapideTypeNode{}, fmt.Errorf("%w: type-name member %d has an invalid region or action mode", ErrInvalidRapideType, index)
				}
				if member.typeSpecification == rapideAnyTypeDenotation && member.typ != nil ||
					(member.typeSpecification == rapideSubtypeTypeDenotation || member.typeSpecification == rapideExactTypeDenotation) && member.typ == nil ||
					member.typeSpecification < rapideAnyTypeDenotation || member.typeSpecification > rapideExactTypeDenotation {
					return canonicalRapideTypeNode{}, fmt.Errorf("%w: type-name member %d has an invalid denotation specification", ErrInvalidRapideType, index)
				}
				canonicalMember.Kind = "type"
				canonicalMember.Region = rapideRegionName(member.region)
				canonicalMember.Specification = rapideTypeNameSpecificationName(member.typeSpecification)
			case rapideTypeConstructorConstituent:
				if member.typ == nil || member.typ.kind != rapideFunctionType || !rapideEmptyFunctionResult(member.typ) ||
					(member.region != rapideProvidesRegion && member.region != rapidePrivateRegion) || member.actionMode != 0 {
					return canonicalRapideTypeNode{}, fmt.Errorf("%w: type-constructor member %d has an invalid characteristic, region, or action mode", ErrInvalidRapideType, index)
				}
				if member.typeSpecification == rapideAnyTypeDenotation && member.constructorResult != nil ||
					(member.typeSpecification == rapideSubtypeTypeDenotation || member.typeSpecification == rapideExactTypeDenotation) && member.constructorResult == nil ||
					member.typeSpecification < rapideAnyTypeDenotation || member.typeSpecification > rapideExactTypeDenotation {
					return canonicalRapideTypeNode{}, fmt.Errorf("%w: type-constructor member %d has an invalid denotation specification", ErrInvalidRapideType, index)
				}
				characteristic, err := encodeCanonicalRapideTypeNodeWithContext(member.typ, memberBinders)
				if err != nil {
					return canonicalRapideTypeNode{}, fmt.Errorf("%w: type-constructor member %d characteristic: %v", ErrInvalidRapideType, index, err)
				}
				canonicalMember.Kind = "type_constructor"
				canonicalMember.Region = rapideRegionName(member.region)
				canonicalMember.Specification = rapideTypeNameSpecificationName(member.typeSpecification)
				canonicalMember.Characteristic = &characteristic
				if member.constructorResult != nil {
					denotation, err := encodeCanonicalRapideTypeNodeWithContext(member.constructorResult, memberBinders)
					if err != nil {
						return canonicalRapideTypeNode{}, fmt.Errorf("%w: type-constructor member %d denotation: %v", ErrInvalidRapideType, index, err)
					}
					canonicalMember.Type = &denotation
				}
			default:
				return canonicalRapideTypeNode{}, fmt.Errorf("%w: member %d has an invalid constituent kind", ErrInvalidRapideType, index)
			}
			result.Members = append(result.Members, canonicalMember)
		}
		return result, nil
	case rapideFunctionType:
		if node.predefined != "" || node.reference != "" || len(node.members) != 0 || len(node.eventParams) != 0 || node.result == nil {
			return canonicalRapideTypeNode{}, fmt.Errorf("%w: malformed function descriptor", ErrInvalidRapideType)
		}
		result := canonicalRapideTypeNode{Kind: "function"}
		for index, parameter := range node.parameters {
			canonicalParameter := canonicalRapideTypeParameter{
				Name: parameter.name, HasDefault: parameter.hasDefault,
			}
			switch parameter.kind {
			case rapideObjectFunctionParameter:
				if parameter.typ == nil {
					return canonicalRapideTypeNode{}, fmt.Errorf("%w: object parameter %d has no type", ErrInvalidRapideType, index)
				}
				typ, err := encodeCanonicalRapideTypeNodeWithContext(parameter.typ, binders)
				if err != nil {
					return canonicalRapideTypeNode{}, fmt.Errorf("%w: parameter %d: %v", ErrInvalidRapideType, index, err)
				}
				canonicalParameter.Type = &typ
			case rapideTypeFunctionParameter:
				if parameter.hasDefault {
					return canonicalRapideTypeNode{}, fmt.Errorf("%w: type parameter %d has an object default", ErrInvalidRapideType, index)
				}
				canonicalParameter.Kind = "type"
				if parameter.typ != nil {
					bound, err := encodeCanonicalRapideTypeNodeWithContext(parameter.typ, binders)
					if err != nil {
						return canonicalRapideTypeNode{}, fmt.Errorf("%w: type parameter %d bound: %v", ErrInvalidRapideType, index, err)
					}
					canonicalParameter.Type = &bound
				}
			default:
				return canonicalRapideTypeNode{}, fmt.Errorf("%w: parameter %d has an invalid kind", ErrInvalidRapideType, index)
			}
			result.Parameters = append(result.Parameters, canonicalParameter)
		}
		resultType, err := encodeCanonicalRapideTypeNodeWithContext(node.result, binders)
		if err != nil {
			return canonicalRapideTypeNode{}, fmt.Errorf("%w: function result: %v", ErrInvalidRapideType, err)
		}
		result.Result = &resultType
		return result, nil
	case rapideEventInterfaceType:
		if node.predefined != "" || node.reference != "" || len(node.members) != 0 || len(node.parameters) != 0 || node.result != nil {
			return canonicalRapideTypeNode{}, fmt.Errorf("%w: malformed event descriptor", ErrInvalidRapideType)
		}
		result := canonicalRapideTypeNode{Kind: "event"}
		for index, parameter := range node.eventParams {
			typ, err := encodeCanonicalRapideTypeNodeWithContext(parameter.typ, binders)
			if err != nil {
				return canonicalRapideTypeNode{}, fmt.Errorf("%w: event parameter %d: %v", ErrInvalidRapideType, index, err)
			}
			result.EventParameters = append(result.EventParameters, canonicalRapideEventTypeParameter{Name: parameter.name, Type: typ})
		}
		return result, nil
	default:
		return canonicalRapideTypeNode{}, fmt.Errorf("%w: unknown descriptor kind %d", ErrInvalidRapideType, node.kind)
	}
}

func rapideInterfaceReachesItself(node *rapideTypeNode, enclosingBinders []*rapideTypeNode) bool {
	if node == nil || node.kind != rapideInterfaceType {
		return false
	}
	blocked := make(map[*rapideTypeNode]bool, len(enclosingBinders))
	for _, binder := range enclosingBinders {
		blocked[binder] = true
	}
	visited := make(map[*rapideTypeNode]bool)
	for _, member := range node.members {
		if rapideTypeReaches(member.typ, node, visited, blocked) ||
			rapideTypeReaches(member.constructorResult, node, visited, blocked) {
			return true
		}
	}
	return false
}

func rapideTypeReaches(
	node, target *rapideTypeNode,
	visited, blocked map[*rapideTypeNode]bool,
) bool {
	if node == nil {
		return false
	}
	if node == target {
		return true
	}
	if blocked[node] {
		return false
	}
	if visited[node] {
		return false
	}
	visited[node] = true
	switch node.kind {
	case rapideInterfaceType:
		for _, member := range node.members {
			if rapideTypeReaches(member.typ, target, visited, blocked) ||
				rapideTypeReaches(member.constructorResult, target, visited, blocked) {
				return true
			}
		}
	case rapideFunctionType:
		for _, parameter := range node.parameters {
			if rapideTypeReaches(parameter.typ, target, visited, blocked) {
				return true
			}
		}
		return rapideTypeReaches(node.result, target, visited, blocked)
	case rapideEventInterfaceType:
		for _, parameter := range node.eventParams {
			if rapideTypeReaches(parameter.typ, target, visited, blocked) {
				return true
			}
		}
	}
	return false
}

func rapideTypeFromCanonical(node canonicalRapideTypeNode) (RapideType, error) {
	return rapideTypeFromCanonicalWithContext(node, nil)
}

func rapideTypeFromCanonicalWithContext(
	node canonicalRapideTypeNode,
	binders []*rapideTypeNode,
) (RapideType, error) {
	return rapideTypeFromCanonicalWithScope(node, binders, nil)
}

func rapideTypeFromCanonicalWithScope(
	node canonicalRapideTypeNode,
	binders []*rapideTypeNode,
	scope map[string]rapideTypeReferenceScopeKind,
) (RapideType, error) {
	switch node.Kind {
	case "recursive_reference":
		if node.Name != "" || node.Depth == nil || len(node.Members) != 0 || len(node.Parameters) != 0 || len(node.EventParameters) != 0 || node.Result != nil {
			return RapideType{}, fmt.Errorf("%w: malformed canonical recursive reference", ErrInvalidRapideType)
		}
		if uint(len(binders)) <= *node.Depth {
			return RapideType{}, fmt.Errorf("%w: recursive reference depth %d is outside %d enclosing binders", ErrInvalidRapideType, *node.Depth, len(binders))
		}
		return RapideType{node: binders[len(binders)-1-int(*node.Depth)]}, nil
	case "type_reference":
		if node.Name == "" || node.Depth != nil || len(node.Members) != 0 || len(node.Parameters) != 0 || len(node.EventParameters) != 0 || node.Result != nil {
			return RapideType{}, fmt.Errorf("%w: malformed canonical type-name reference", ErrInvalidRapideType)
		}
		return NewRapideTypeNameReference(node.Name)
	case "predefined":
		if node.Name == "" || node.Depth != nil || len(node.Members) != 0 || len(node.Parameters) != 0 || len(node.EventParameters) != 0 || node.Result != nil {
			return RapideType{}, fmt.Errorf("%w: malformed canonical predefined descriptor", ErrInvalidRapideType)
		}
		return RapidePredefinedType(node.Name)
	case "interface", "recursive_interface":
		if node.Name != "" || node.Depth != nil || len(node.Parameters) != 0 || len(node.EventParameters) != 0 || node.Result != nil {
			return RapideType{}, fmt.Errorf("%w: malformed canonical interface descriptor", ErrInvalidRapideType)
		}
		memberBinders := binders
		var placeholder *rapideTypeNode
		if node.Kind == "recursive_interface" {
			placeholder = &rapideTypeNode{kind: rapideInterfaceType}
			memberBinders = append(append([]*rapideTypeNode(nil), binders...), placeholder)
		}
		members := make([]RapideInterfaceMember, len(node.Members))
		for index, member := range node.Members {
			var typ RapideType
			if member.Type != nil {
				var err error
				typ, err = rapideTypeFromCanonicalWithScope(*member.Type, memberBinders, scope)
				if err != nil {
					return RapideType{}, fmt.Errorf("%w: member %d: %v", ErrInvalidRapideType, index, err)
				}
			}
			switch member.Kind {
			case "", "object":
				if member.Mode != "" || member.Specification != "" || member.Type == nil || member.Characteristic != nil {
					return RapideType{}, fmt.Errorf("%w: object member %d has action/type-name fields or no type", ErrInvalidRapideType, index)
				}
				switch member.Region {
				case "provides":
					members[index] = ProvidedRapideMember(member.Name, typ)
				case "private":
					members[index] = PrivateRapideMember(member.Name, typ)
				case "requires":
					members[index] = RequiredRapideMember(member.Name, typ)
				default:
					return RapideType{}, fmt.Errorf("%w: member %d has unknown region %q", ErrInvalidRapideType, index, member.Region)
				}
			case "module_generator":
				if member.Mode != "" || member.Specification != "" || member.Type == nil || member.Characteristic != nil ||
					typ.node == nil || typ.node.kind != rapideFunctionType || typ.node.result == nil ||
					typ.node.result.kind != rapideInterfaceType {
					return RapideType{}, fmt.Errorf("%w: module-generator member %d has non-generator fields or no function signature returning an interface", ErrInvalidRapideType, index)
				}
				switch member.Region {
				case "provides":
					members[index] = ProvidedRapideModuleGenerator(member.Name, typ)
				case "private":
					members[index] = PrivateRapideModuleGenerator(member.Name, typ)
				case "requires":
					members[index] = RequiredRapideModuleGenerator(member.Name, typ)
				default:
					return RapideType{}, fmt.Errorf("%w: module-generator member %d has unknown region %q", ErrInvalidRapideType, index, member.Region)
				}
			case "action":
				if member.Region != "" || member.Specification != "" || member.Type == nil || member.Characteristic != nil {
					return RapideType{}, fmt.Errorf("%w: action member %d has object/type-name fields or no event type", ErrInvalidRapideType, index)
				}
				switch member.Mode {
				case "in":
					members[index] = InputRapideAction(member.Name, typ)
				case "out":
					members[index] = OutputRapideAction(member.Name, typ)
				default:
					return RapideType{}, fmt.Errorf("%w: member %d has unknown action mode %q", ErrInvalidRapideType, index, member.Mode)
				}
			case "exception":
				if member.Mode != "" || member.Specification != "" || member.Type == nil || member.Characteristic != nil ||
					typ.node == nil || typ.node.kind != rapideEventInterfaceType {
					return RapideType{}, fmt.Errorf("%w: exception member %d has non-exception fields or no event type", ErrInvalidRapideType, index)
				}
				switch member.Region {
				case "provides":
					members[index] = ProvidedRapideException(member.Name, typ)
				case "private":
					members[index] = PrivateRapideException(member.Name, typ)
				case "requires":
					members[index] = RequiredRapideException(member.Name, typ)
				default:
					return RapideType{}, fmt.Errorf("%w: exception member %d has unknown region %q", ErrInvalidRapideType, index, member.Region)
				}
			case "type":
				if member.Mode != "" || member.Characteristic != nil {
					return RapideType{}, fmt.Errorf("%w: type-name member %d has action mode %q", ErrInvalidRapideType, index, member.Mode)
				}
				private := false
				switch member.Region {
				case "provides":
				case "private":
					private = true
				default:
					return RapideType{}, fmt.Errorf("%w: type-name member %d has illegal region %q", ErrInvalidRapideType, index, member.Region)
				}
				switch member.Specification {
				case "any":
					if member.Type != nil {
						return RapideType{}, fmt.Errorf("%w: unbounded type-name member %d has a type", ErrInvalidRapideType, index)
					}
					if private {
						members[index] = UnboundedPrivateRapideTypeName(member.Name)
					} else {
						members[index] = UnboundedProvidedRapideTypeName(member.Name)
					}
				case "subtype":
					if member.Type == nil {
						return RapideType{}, fmt.Errorf("%w: bounded type-name member %d has no bound", ErrInvalidRapideType, index)
					}
					if private {
						members[index] = BoundedPrivateRapideTypeName(member.Name, typ)
					} else {
						members[index] = BoundedProvidedRapideTypeName(member.Name, typ)
					}
				case "exact":
					if member.Type == nil {
						return RapideType{}, fmt.Errorf("%w: exact type-name member %d has no type", ErrInvalidRapideType, index)
					}
					if private {
						members[index] = ExactPrivateRapideTypeName(member.Name, typ)
					} else {
						members[index] = ExactProvidedRapideTypeName(member.Name, typ)
					}
				default:
					return RapideType{}, fmt.Errorf("%w: type-name member %d has unknown specification %q", ErrInvalidRapideType, index, member.Specification)
				}
			case "type_constructor":
				if member.Mode != "" || member.Characteristic == nil {
					return RapideType{}, fmt.Errorf("%w: type-constructor member %d has an action mode or no characteristic function", ErrInvalidRapideType, index)
				}
				private := false
				switch member.Region {
				case "provides":
				case "private":
					private = true
				default:
					return RapideType{}, fmt.Errorf("%w: type-constructor member %d has illegal region %q", ErrInvalidRapideType, index, member.Region)
				}
				characteristic, err := rapideTypeFromCanonicalWithScope(*member.Characteristic, memberBinders, scope)
				if err != nil {
					return RapideType{}, fmt.Errorf("%w: type-constructor member %d characteristic: %v", ErrInvalidRapideType, index, err)
				}
				if characteristic.node == nil || characteristic.node.kind != rapideFunctionType || !rapideEmptyFunctionResult(characteristic.node) {
					return RapideType{}, fmt.Errorf("%w: type-constructor member %d characteristic is not a resultless function type", ErrInvalidRapideType, index)
				}
				parameters := make([]RapideFunctionParameter, len(characteristic.node.parameters))
				for parameterIndex, parameter := range characteristic.node.parameters {
					parameters[parameterIndex] = publicRapideFunctionParameter(parameter)
				}
				switch member.Specification {
				case "any":
					if member.Type != nil {
						return RapideType{}, fmt.Errorf("%w: unbounded type-constructor member %d has a result constraint", ErrInvalidRapideType, index)
					}
					if private {
						members[index] = UnboundedPrivateRapideTypeConstructor(member.Name, parameters...)
					} else {
						members[index] = UnboundedProvidedRapideTypeConstructor(member.Name, parameters...)
					}
				case "subtype", "exact":
					if member.Type == nil {
						return RapideType{}, fmt.Errorf("%w: constrained type-constructor member %d has no result type", ErrInvalidRapideType, index)
					}
					if member.Specification == "subtype" {
						if private {
							members[index] = BoundedPrivateRapideTypeConstructor(member.Name, typ, parameters...)
						} else {
							members[index] = BoundedProvidedRapideTypeConstructor(member.Name, typ, parameters...)
						}
					} else if private {
						members[index] = ExactPrivateRapideTypeConstructor(member.Name, typ, parameters...)
					} else {
						members[index] = ExactProvidedRapideTypeConstructor(member.Name, typ, parameters...)
					}
				default:
					return RapideType{}, fmt.Errorf("%w: type-constructor member %d has unknown specification %q", ErrInvalidRapideType, index, member.Specification)
				}
			default:
				return RapideType{}, fmt.Errorf("%w: member %d has unknown constituent kind %q", ErrInvalidRapideType, index, member.Kind)
			}
		}
		result, err := newRapideInterfaceTypeWithScope(scope, members...)
		if err != nil {
			return RapideType{}, err
		}
		if placeholder == nil {
			return result, nil
		}
		*placeholder = *result.node
		result = RapideType{node: placeholder}
		if err := validateRapideTypeReferences(result.node, scope); err != nil {
			return RapideType{}, err
		}
		return result, nil
	case "function":
		if node.Name != "" || node.Depth != nil || len(node.Members) != 0 || len(node.EventParameters) != 0 || node.Result == nil {
			return RapideType{}, fmt.Errorf("%w: malformed canonical function descriptor", ErrInvalidRapideType)
		}
		localScope := cloneRapideTypeReferenceScope(scope)
		parameters := make([]RapideFunctionParameter, len(node.Parameters))
		for index, parameter := range node.Parameters {
			switch parameter.Kind {
			case "", "object":
				if parameter.Type == nil {
					return RapideType{}, fmt.Errorf("%w: object parameter %d has no type", ErrInvalidRapideType, index)
				}
				typ, err := rapideTypeFromCanonicalWithScope(*parameter.Type, binders, localScope)
				if err != nil {
					return RapideType{}, fmt.Errorf("%w: parameter %d: %v", ErrInvalidRapideType, index, err)
				}
				if parameter.HasDefault {
					parameters[index] = DefaultedRapideObjectParameter(parameter.Name, typ)
				} else {
					parameters[index] = RapideObjectParameter(parameter.Name, typ)
				}
			case "type":
				if parameter.HasDefault {
					return RapideType{}, fmt.Errorf("%w: type parameter %d has an object default", ErrInvalidRapideType, index)
				}
				if parameter.Type == nil {
					parameters[index] = RapideTypeParameter(parameter.Name)
				} else {
					bound, err := rapideTypeFromCanonicalWithScope(*parameter.Type, binders, localScope)
					if err != nil {
						return RapideType{}, fmt.Errorf("%w: type parameter %d bound: %v", ErrInvalidRapideType, index, err)
					}
					parameters[index] = BoundedRapideTypeParameter(parameter.Name, bound)
				}
				name, err := canonicalRapideIdentifier(parameter.Name)
				if err != nil {
					return RapideType{}, fmt.Errorf("%w: type parameter %d: %v", ErrInvalidRapideType, index, err)
				}
				localScope[name] = rapideFunctionTypeParameterScope
			default:
				return RapideType{}, fmt.Errorf("%w: parameter %d has unknown kind %q", ErrInvalidRapideType, index, parameter.Kind)
			}
		}
		result, err := rapideTypeFromCanonicalWithScope(*node.Result, binders, localScope)
		if err != nil {
			return RapideType{}, fmt.Errorf("%w: result: %v", ErrInvalidRapideType, err)
		}
		return NewRapideFunctionType(parameters, result)
	case "event":
		if node.Name != "" || node.Depth != nil || len(node.Members) != 0 || len(node.Parameters) != 0 || node.Result != nil {
			return RapideType{}, fmt.Errorf("%w: malformed canonical event descriptor", ErrInvalidRapideType)
		}
		parameters := make([]RapideEventParameter, len(node.EventParameters))
		for index, parameter := range node.EventParameters {
			typ, err := rapideTypeFromCanonicalWithScope(parameter.Type, binders, scope)
			if err != nil {
				return RapideType{}, fmt.Errorf("%w: event parameter %d: %v", ErrInvalidRapideType, index, err)
			}
			parameters[index] = RapideEventParam(parameter.Name, typ)
		}
		return NewRapideEventType(parameters...)
	default:
		return RapideType{}, fmt.Errorf("%w: unknown canonical kind %q", ErrInvalidRapideType, node.Kind)
	}
}

func canonicalRapideIdentifier(name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("identifier is empty")
	}
	for index := 0; index < len(name); index++ {
		value := name[index]
		validStart := value == '_' || value >= 'A' && value <= 'Z' || value >= 'a' && value <= 'z'
		valid := validStart || index > 0 && value >= '0' && value <= '9'
		if !valid || index == 0 && !validStart {
			return "", fmt.Errorf("identifier %q is outside the current ASCII lexical profile", name)
		}
	}
	return strings.ToLower(name), nil
}

func canonicalRapideObjectName(name string) (string, error) {
	if identifier, err := canonicalRapideIdentifier(name); err == nil {
		return identifier, nil
	}
	// Function objects may use the predefined symbolic designators that occur
	// in the Stanford library interfaces. They are names, not evaluator hooks.
	switch name {
	case ":=", "$", "=", "/=", "<", "<=", ">", ">=", "+", "-", "*", "**", "/", "%", "&", "[]":
		return name, nil
	default:
		return "", fmt.Errorf("object name %q is outside the current Rapide lexical profile", name)
	}
}

func canonicalRapideConstituentName(name string, object bool) (string, error) {
	parts := strings.Split(name, ".")
	if len(parts) == 0 {
		return "", fmt.Errorf("constituent name is empty")
	}
	for index := range parts {
		var canonical string
		var err error
		if index == len(parts)-1 {
			if object {
				canonical, err = canonicalRapideObjectName(parts[index])
			} else {
				canonical, err = canonicalRapideIdentifier(parts[index])
			}
		} else {
			canonical, err = canonicalRapideServiceQualifier(parts[index])
		}
		if err != nil {
			return "", fmt.Errorf("qualified constituent name %q segment %d: %v", name, index, err)
		}
		parts[index] = canonical
	}
	return strings.Join(parts, "."), nil
}

func canonicalRapideServiceQualifier(name string) (string, error) {
	if identifier, err := canonicalRapideIdentifier(name); err == nil {
		return identifier, nil
	}
	open := strings.LastIndexByte(name, '(')
	if open <= 0 || !strings.HasSuffix(name, ")") {
		return "", fmt.Errorf("service qualifier %q is outside the current lexical profile", name)
	}
	base, err := canonicalRapideIdentifier(name[:open])
	if err != nil {
		return "", fmt.Errorf("service qualifier %q base: %v", name, err)
	}
	indexText := name[open+1 : len(name)-1]
	index, err := strconv.ParseInt(indexText, 10, 64)
	if err != nil || indexText != strconv.FormatInt(index, 10) {
		return "", fmt.Errorf("service qualifier %q does not have a canonical Integer index", name)
	}
	return base + "(" + indexText + ")", nil
}

func compareRapideMembers(left, right rapideInterfaceConstituent) int {
	if left.kind < right.kind {
		return -1
	}
	if left.kind > right.kind {
		return 1
	}
	if left.kind == rapideObjectConstituent || left.kind == rapideTypeNameConstituent ||
		left.kind == rapideTypeConstructorConstituent || left.kind == rapideModuleGeneratorConstituent ||
		left.kind == rapideExceptionConstituent {
		if left.region < right.region {
			return -1
		}
		if left.region > right.region {
			return 1
		}
	} else {
		if left.actionMode < right.actionMode {
			return -1
		}
		if left.actionMode > right.actionMode {
			return 1
		}
	}
	if left.name < right.name {
		return -1
	}
	if left.name > right.name {
		return 1
	}
	if left.kind == rapideTypeNameConstituent || left.kind == rapideTypeConstructorConstituent {
		if left.typeSpecification < right.typeSpecification {
			return -1
		}
		if left.typeSpecification > right.typeSpecification {
			return 1
		}
		if compared := compareRapideOptionalTypeNodes(left.typ, right.typ); compared != 0 {
			return compared
		}
		if left.kind == rapideTypeConstructorConstituent {
			return compareRapideOptionalTypeNodes(left.constructorResult, right.constructorResult)
		}
		return 0
	}
	return compareRapideTypeNodes(left.typ, right.typ)
}

func compareRapideOptionalTypeNodes(left, right *rapideTypeNode) int {
	if left == nil && right == nil {
		return 0
	}
	if left == nil {
		return -1
	}
	if right == nil {
		return 1
	}
	return compareRapideTypeNodes(left, right)
}

func compareRapideTypeNodes(left, right *rapideTypeNode) int {
	leftType, _ := encodeCanonicalRapideTypeNode(left)
	rightType, _ := encodeCanonicalRapideTypeNode(right)
	leftBytes, _ := json.Marshal(leftType)
	rightBytes, _ := json.Marshal(rightType)
	return bytes.Compare(leftBytes, rightBytes)
}

func rapideRegionName(region rapideInterfaceRegion) string {
	switch region {
	case rapideProvidesRegion:
		return "provides"
	case rapidePrivateRegion:
		return "private"
	case rapideRequiresRegion:
		return "requires"
	default:
		return "invalid"
	}
}

func rapideActionModeName(mode rapideActionMode) string {
	switch mode {
	case rapideActionIn:
		return "in"
	case rapideActionOut:
		return "out"
	default:
		return "invalid"
	}
}

func rapideTypeNameSpecificationName(specification rapideTypeNameSpecification) string {
	switch specification {
	case rapideAnyTypeDenotation:
		return "any"
	case rapideSubtypeTypeDenotation:
		return "subtype"
	case rapideExactTypeDenotation:
		return "exact"
	default:
		return "invalid"
	}
}

func rapideConstituentDescription(member rapideInterfaceConstituent) string {
	if member.kind == rapideObjectConstituent {
		return rapideRegionName(member.region) + " object"
	}
	if member.kind == rapideActionConstituent {
		return rapideActionModeName(member.actionMode) + " action"
	}
	if member.kind == rapideTypeNameConstituent {
		return rapideRegionName(member.region) + " type name"
	}
	if member.kind == rapideTypeConstructorConstituent {
		return rapideRegionName(member.region) + " type constructor"
	}
	if member.kind == rapideModuleGeneratorConstituent {
		return rapideRegionName(member.region) + " module generator"
	}
	if member.kind == rapideExceptionConstituent {
		return rapideRegionName(member.region) + " exception"
	}
	return "invalid"
}

func canonicalPredefinedDescriptorName(name string) bool {
	switch name {
	case "Triv", "Boolean", "Integer", "Float", "GST", "Character", "String":
		return true
	default:
		return false
	}
}

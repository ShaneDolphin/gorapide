package arch

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/ShaneDolphin/gorapide"
)

// ErrInvalidModuleMembership identifies an invalid generated-module
// structural membership declaration.
var ErrInvalidModuleMembership = errors.New("invalid generated-module membership")

type moduleMembershipDeclaration struct {
	Generator          string
	GeneratorArguments []ModuleGeneratorArgument
	TypeDenotations    []gorapide.RapideTypeDenotation
	ObjectDenotations  []gorapide.RapideObjectDenotation
	RecordObjects      []gorapide.RapideRecordObjectDeclaration
}

// ModuleGeneratorArgument is one formal/type/actual association captured when
// a module generator creates a component implementation.
type ModuleGeneratorArgument struct {
	Name  string
	Type  string
	Value any
}

// ModuleArgument constructs one module-generator actual association.
func ModuleArgument(name, typ string, value any) ModuleGeneratorArgument {
	return ModuleGeneratorArgument{Name: name, Type: typ, Value: value}
}

func canonicalizeNewGeneratorArguments(
	arguments []ModuleGeneratorArgument,
) ([]ModuleGeneratorArgument, []canonicalArchitectureArgument, error) {
	normalized := make([]ModuleGeneratorArgument, len(arguments))
	canonical := make([]canonicalArchitectureArgument, len(arguments))
	seen := make(map[string]bool, len(arguments))
	for index, argument := range arguments {
		if !validModuleMembershipIdentifier(argument.Name) ||
			!gorapide.IsSupportedPredefinedType(argument.Type) {
			return nil, nil, fmt.Errorf("invalid generator argument %q of type %q", argument.Name, argument.Type)
		}
		key := strings.ToLower(argument.Name)
		if seen[key] {
			return nil, nil, fmt.Errorf("duplicate generator argument %q", argument.Name)
		}
		values, err := gorapide.CanonicalizeParams(map[string]any{"value": argument.Value})
		if err != nil || !gorapide.CanonicalValueMatchesPredefinedType(values["value"], argument.Type) {
			return nil, nil, fmt.Errorf("generator argument %q does not match %s", argument.Name, argument.Type)
		}
		encoded, err := gorapide.EncodeCanonicalValue(values["value"])
		if err != nil {
			return nil, nil, fmt.Errorf("generator argument %q: %v", argument.Name, err)
		}
		seen[key] = true
		normalized[index] = ModuleGeneratorArgument{
			Name: argument.Name, Type: argument.Type, Value: values["value"],
		}
		canonical[index] = canonicalArchitectureArgument{
			Name: argument.Name, Type: argument.Type, Value: encoded,
		}
	}
	return normalized, canonical, nil
}

// SetModuleMembership binds the generator and exact module type denotations to
// this component. The declaration becomes canonical model content and is
// checked against the component's structural return interface.
func (component *Component) SetModuleMembership(
	generator string,
	denotations ...gorapide.RapideTypeDenotation,
) error {
	return component.SetModuleMembershipWithObjects(generator, denotations, nil)
}

// SetModuleMembershipWithObjects binds the generator, exact type denotations,
// and concrete immutable object denotations to this component. Both tables are
// validated against the same structural return interface and become canonical
// model content.
func (component *Component) SetModuleMembershipWithObjects(
	generator string,
	typeDenotations []gorapide.RapideTypeDenotation,
	objectDenotations []gorapide.RapideObjectDenotation,
) error {
	return component.SetModuleMembershipWithArgumentsAndRecordObjects(
		generator, nil, typeDenotations, objectDenotations, nil,
	)
}

// SetModuleMembershipWithArguments additionally binds the ordered formal
// object parameters used by this particular generator call. They are immutable
// canonical model data, separate from public/private interface object
// denotations.
func (component *Component) SetModuleMembershipWithArguments(
	generator string,
	arguments []ModuleGeneratorArgument,
	typeDenotations []gorapide.RapideTypeDenotation,
	objectDenotations []gorapide.RapideObjectDenotation,
) error {
	return component.SetModuleMembershipWithArgumentsAndRecordObjects(
		generator, arguments, typeDenotations, objectDenotations, nil,
	)
}

// SetModuleMembershipWithArgumentsAndRecordObjects additionally binds ordered
// runtime-allocated immutable Record object declarations. Their field values
// and types are canonical model data; allocation identity is created only by an
// execution after the enclosing module Start exists.
func (component *Component) SetModuleMembershipWithArgumentsAndRecordObjects(
	generator string,
	arguments []ModuleGeneratorArgument,
	typeDenotations []gorapide.RapideTypeDenotation,
	objectDenotations []gorapide.RapideObjectDenotation,
	recordObjects []gorapide.RapideRecordObjectDeclaration,
) error {
	if component == nil || component.Interface == nil || !validModuleMembershipIdentifier(generator) {
		return fmt.Errorf("%w: invalid module generator %q", ErrInvalidModuleMembership, generator)
	}
	var normalizedTypes []gorapide.RapideTypeDenotation
	var normalizedObjects []gorapide.RapideObjectDenotation
	var normalizedRecords []gorapide.RapideRecordObjectDeclaration
	normalizedArguments := make([]ModuleGeneratorArgument, len(arguments))
	seenArguments := make(map[string]bool, len(arguments))
	for index, argument := range arguments {
		if !validModuleMembershipIdentifier(argument.Name) || !gorapide.IsSupportedPredefinedType(argument.Type) {
			return fmt.Errorf("%w: invalid generator argument %q of type %q",
				ErrInvalidModuleMembership, argument.Name, argument.Type)
		}
		key := strings.ToLower(argument.Name)
		if seenArguments[key] {
			return fmt.Errorf("%w: duplicate generator argument %q", ErrInvalidModuleMembership, argument.Name)
		}
		canonical, err := gorapide.CanonicalizeParams(map[string]any{"value": argument.Value})
		if err != nil || !gorapide.CanonicalValueMatchesPredefinedType(canonical["value"], argument.Type) {
			return fmt.Errorf("%w: generator argument %q does not match %s",
				ErrInvalidModuleMembership, argument.Name, argument.Type)
		}
		seenArguments[key] = true
		normalizedArguments[index] = ModuleGeneratorArgument{
			Name: argument.Name, Type: argument.Type, Value: canonical["value"],
		}
	}
	var err error
	normalizedRecords, err = gorapide.NormalizeRapideRecordObjectDeclarations(recordObjects...)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidModuleMembership, err)
	}
	if structural, exists := component.Interface.StructuralRapideType(); exists {
		var err error
		normalizedTypes, err = gorapide.ValidateRapideInterfaceTypeDenotations(structural, typeDenotations...)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidModuleMembership, err)
		}
		normalizedObjects, err = gorapide.NormalizeRapideObjectDenotations(objectDenotations...)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidModuleMembership, err)
		}
		objectTypes := make([]gorapide.RapideObjectTypeDenotation, 0, len(normalizedObjects)+len(normalizedRecords))
		for _, denotation := range normalizedObjects {
			actual, typeErr := gorapide.NewRapideObjectTypeDenotation(denotation.Name(), denotation.Type())
			if typeErr != nil {
				return fmt.Errorf("%w: %v", ErrInvalidModuleMembership, typeErr)
			}
			objectTypes = append(objectTypes, actual)
		}
		for _, declaration := range normalizedRecords {
			actual, typeErr := gorapide.NewRapideObjectTypeDenotation(declaration.Name(), declaration.Type())
			if typeErr != nil {
				return fmt.Errorf("%w: %v", ErrInvalidModuleMembership, typeErr)
			}
			objectTypes = append(objectTypes, actual)
		}
		if _, err = gorapide.ValidateRapideInterfaceObjectTypes(structural, normalizedTypes, objectTypes...); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidModuleMembership, err)
		}
	} else {
		normalizedTypes, err = gorapide.NormalizeRapideTypeDenotations(typeDenotations...)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidModuleMembership, err)
		}
		normalizedObjects, err = gorapide.NormalizeRapideObjectDenotations(objectDenotations...)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidModuleMembership, err)
		}
	}
	component.mu.Lock()
	defer component.mu.Unlock()
	if component.moduleMembership != nil {
		return fmt.Errorf("%w: component already has a module membership declaration", ErrInvalidModuleMembership)
	}
	component.moduleMembership = &moduleMembershipDeclaration{
		Generator:          strings.ToLower(generator),
		GeneratorArguments: append([]ModuleGeneratorArgument(nil), normalizedArguments...),
		TypeDenotations:    append([]gorapide.RapideTypeDenotation(nil), normalizedTypes...),
		ObjectDenotations:  append([]gorapide.RapideObjectDenotation(nil), normalizedObjects...),
		RecordObjects:      append([]gorapide.RapideRecordObjectDeclaration(nil), normalizedRecords...),
	}
	return nil
}

type canonicalModuleTypeDenotation struct {
	Name string          `json:"name"`
	Type json.RawMessage `json:"type"`
}

type canonicalModuleMembership struct {
	Generator          string                            `json:"generator"`
	GeneratorArguments []canonicalArchitectureArgument   `json:"generator_arguments"`
	TypeDenotations    []canonicalModuleTypeDenotation   `json:"type_denotations"`
	ObjectDenotations  []canonicalModuleObjectDenotation `json:"object_denotations,omitempty"`
	RecordObjects      []canonicalModuleRecordObject     `json:"record_objects,omitempty"`
}

type canonicalModuleObjectDenotation struct {
	Name  string                  `json:"name"`
	Type  json.RawMessage         `json:"type"`
	Value gorapide.CanonicalValue `json:"value"`
}

type canonicalModuleRecordObject struct {
	Name   string                    `json:"name"`
	Type   json.RawMessage           `json:"type"`
	Fields []gorapide.CanonicalField `json:"fields"`
}

func canonicalizeModuleMembership(
	membership *moduleMembershipDeclaration,
	iface *InterfaceDecl,
) (*canonicalModuleMembership, error) {
	if membership == nil {
		return nil, nil
	}
	if !validModuleMembershipIdentifier(membership.Generator) {
		return nil, fmt.Errorf("%w: invalid generator %q", ErrInvalidModuleMembership, membership.Generator)
	}
	typeDenotations := membership.TypeDenotations
	objectDenotations := membership.ObjectDenotations
	recordObjects := membership.RecordObjects
	var err error
	recordObjects, err = gorapide.NormalizeRapideRecordObjectDeclarations(recordObjects...)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidModuleMembership, err)
	}
	if structural, exists := iface.StructuralRapideType(); exists {
		typeDenotations, err = gorapide.ValidateRapideInterfaceTypeDenotations(structural, typeDenotations...)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidModuleMembership, err)
		}
		objectDenotations, err = gorapide.NormalizeRapideObjectDenotations(objectDenotations...)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidModuleMembership, err)
		}
		objectTypes := make([]gorapide.RapideObjectTypeDenotation, 0, len(objectDenotations)+len(recordObjects))
		for _, denotation := range objectDenotations {
			actual, typeErr := gorapide.NewRapideObjectTypeDenotation(denotation.Name(), denotation.Type())
			if typeErr != nil {
				return nil, fmt.Errorf("%w: %v", ErrInvalidModuleMembership, typeErr)
			}
			objectTypes = append(objectTypes, actual)
		}
		for _, declaration := range recordObjects {
			actual, typeErr := gorapide.NewRapideObjectTypeDenotation(declaration.Name(), declaration.Type())
			if typeErr != nil {
				return nil, fmt.Errorf("%w: %v", ErrInvalidModuleMembership, typeErr)
			}
			objectTypes = append(objectTypes, actual)
		}
		if _, err = gorapide.ValidateRapideInterfaceObjectTypes(structural, typeDenotations, objectTypes...); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidModuleMembership, err)
		}
	} else {
		typeDenotations, err = gorapide.NormalizeRapideTypeDenotations(typeDenotations...)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidModuleMembership, err)
		}
		objectDenotations, err = gorapide.NormalizeRapideObjectDenotations(objectDenotations...)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidModuleMembership, err)
		}
	}
	result := &canonicalModuleMembership{
		Generator:          membership.Generator,
		GeneratorArguments: make([]canonicalArchitectureArgument, 0, len(membership.GeneratorArguments)),
		TypeDenotations:    make([]canonicalModuleTypeDenotation, 0, len(typeDenotations)),
		ObjectDenotations:  make([]canonicalModuleObjectDenotation, 0, len(objectDenotations)),
		RecordObjects:      make([]canonicalModuleRecordObject, 0, len(recordObjects)),
	}
	seenArguments := make(map[string]bool, len(membership.GeneratorArguments))
	for _, argument := range membership.GeneratorArguments {
		key := strings.ToLower(argument.Name)
		if !validModuleMembershipIdentifier(argument.Name) || seenArguments[key] ||
			!gorapide.IsSupportedPredefinedType(argument.Type) ||
			!gorapide.CanonicalValueMatchesPredefinedType(argument.Value, argument.Type) {
			return nil, fmt.Errorf("%w: invalid generator argument %q", ErrInvalidModuleMembership, argument.Name)
		}
		value, err := gorapide.EncodeCanonicalValue(argument.Value)
		if err != nil {
			return nil, fmt.Errorf("%w: generator argument %q: %v", ErrInvalidModuleMembership, argument.Name, err)
		}
		seenArguments[key] = true
		result.GeneratorArguments = append(result.GeneratorArguments, canonicalArchitectureArgument{
			Name: argument.Name, Type: argument.Type, Value: value,
		})
	}
	for _, denotation := range typeDenotations {
		encoded, err := denotation.Type().MarshalCanonical()
		if err != nil {
			return nil, fmt.Errorf("%w: type denotation %q: %v", ErrInvalidModuleMembership, denotation.Name(), err)
		}
		if _, err := gorapide.ParseRapideType(encoded); err != nil {
			return nil, fmt.Errorf("%w: canonical type denotation %q: %v", ErrInvalidModuleMembership, denotation.Name(), err)
		}
		result.TypeDenotations = append(result.TypeDenotations, canonicalModuleTypeDenotation{
			Name: denotation.Name(), Type: append(json.RawMessage(nil), encoded...),
		})
	}
	for _, denotation := range objectDenotations {
		encodedType, err := denotation.Type().MarshalCanonical()
		if err != nil {
			return nil, fmt.Errorf("%w: object denotation %q type: %v", ErrInvalidModuleMembership, denotation.Name(), err)
		}
		if _, err := gorapide.ParseRapideType(encodedType); err != nil {
			return nil, fmt.Errorf("%w: canonical object denotation %q type: %v", ErrInvalidModuleMembership, denotation.Name(), err)
		}
		value := denotation.EncodedValue()
		if _, err := gorapide.DecodeCanonicalValue(value); err != nil {
			return nil, fmt.Errorf("%w: canonical object denotation %q value: %v", ErrInvalidModuleMembership, denotation.Name(), err)
		}
		result.ObjectDenotations = append(result.ObjectDenotations, canonicalModuleObjectDenotation{
			Name: denotation.Name(), Type: append(json.RawMessage(nil), encodedType...), Value: value,
		})
	}
	for _, declaration := range recordObjects {
		encodedType, err := declaration.Type().MarshalCanonical()
		if err != nil {
			return nil, fmt.Errorf("%w: Record object %q type: %v", ErrInvalidModuleMembership, declaration.Name(), err)
		}
		if _, err := gorapide.ParseRapideType(encodedType); err != nil {
			return nil, fmt.Errorf("%w: canonical Record object %q type: %v", ErrInvalidModuleMembership, declaration.Name(), err)
		}
		encoded := canonicalModuleRecordObject{
			Name: declaration.Name(), Type: append(json.RawMessage(nil), encodedType...),
			Fields: make([]gorapide.CanonicalField, 0, len(declaration.FieldNames())),
		}
		for _, name := range declaration.FieldNames() {
			value, exists, fieldErr := declaration.Field(name)
			if fieldErr != nil || !exists {
				return nil, fmt.Errorf("%w: Record object %q field %q is unavailable: %v",
					ErrInvalidModuleMembership, declaration.Name(), name, fieldErr)
			}
			canonical, encodeErr := gorapide.EncodeCanonicalValue(value)
			if encodeErr != nil {
				return nil, fmt.Errorf("%w: Record object %q field %q: %v",
					ErrInvalidModuleMembership, declaration.Name(), name, encodeErr)
			}
			encoded.Fields = append(encoded.Fields, gorapide.CanonicalField{Name: name, Value: canonical})
		}
		result.RecordObjects = append(result.RecordObjects, encoded)
	}
	return result, nil
}

func validModuleMembershipIdentifier(name string) bool {
	if name == "" {
		return false
	}
	for index, character := range []byte(name) {
		letter := character >= 'A' && character <= 'Z' || character >= 'a' && character <= 'z'
		digit := character >= '0' && character <= '9'
		if index == 0 {
			if !letter && character != '_' {
				return false
			}
			continue
		}
		if !letter && !digit && character != '_' {
			return false
		}
	}
	return true
}

package arch

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/pattern"
)

// ErrInvalidModuleInitializationParameter identifies a malformed Stanford
// module initial-part formal or default expression.
var ErrInvalidModuleInitializationParameter = errors.New("invalid declarative Rapide module initialization parameter")

const moduleInitializationBindingPrefix = "\x00gorapide:module-initial:"

// ModuleInitializationFormalBinding returns the reserved binding used when a
// default refers to an earlier initialization formal.
func ModuleInitializationFormalBinding(name string) string {
	return moduleInitializationBindingPrefix + strings.ToLower(strings.TrimSpace(name))
}

// ModuleInitializationParameter is one ordered, non-type formal declared after
// the initial keyword. The Executable LRM requires every such formal to have a
// default. Generator and literal creation evaluate that default in the new
// module; the implicit allocator may instead associate an explicit actual.
type ModuleInitializationParameter struct {
	Name    string
	Type    string
	Default RuleValue
}

// ModuleInitializationArgument is one complete formal-order allocator actual.
// Value is evaluated in the allocating module before the child is created.
type ModuleInitializationArgument struct {
	Name  string
	Type  string
	Value RuleValue
}

// ModuleInitialArgument constructs one allocator initialization association.
func ModuleInitialArgument(name, typeName string, value RuleValue) ModuleInitializationArgument {
	return ModuleInitializationArgument{Name: name, Type: typeName, Value: copyRuleValue(value)}
}

func resolveModuleInitializationDefaults(
	componentID, owner string,
	parameters []ModuleInitializationParameter,
	cells map[string]*stateCell,
	initialControl []gorapide.EventID,
	initialOperations []stateOperationReference,
) (pattern.MatchResult, []StateReadRecord, []gorapide.EventID, []stateOperationReference, error) {
	bindings := make(pattern.Bindings, 0, len(parameters))
	evaluationBindings := make(pattern.Bindings, 0, len(parameters))
	reads := make([]StateReadRecord, 0)
	control := canonicalEventIDs(initialControl)
	operations := canonicalStateOperationReferences(initialOperations)
	for _, parameter := range parameters {
		evaluated, err := evaluateClosedRuleValue(
			owner+" initialization formal "+parameter.Name,
			parameter.Default, evaluationBindings, cells,
		)
		if err != nil {
			return pattern.MatchResult{}, nil, nil, nil, err
		}
		if !valueMatchesPredefinedType(evaluated.value, parameter.Type) {
			return pattern.MatchResult{}, nil, nil, nil, fmt.Errorf(
				"%w: component %q formal %q evaluated as %T, want %s",
				ErrInvalidModuleInitializationParameter, componentID,
				parameter.Name, evaluated.value, parameter.Type,
			)
		}
		readOperations := stateOperationReferences(evaluated.reads, nil)
		dependencies := append(eventIDStrings(control), stateOperationReferenceIDs(operations)...)
		if err := addStateOperationDependencies(readOperations, dependencies...); err != nil {
			return pattern.MatchResult{}, nil, nil, nil, err
		}
		bindings = append(bindings, pattern.Binding{Placeholder: parameter.Name, Value: evaluated.value})
		evaluationBindings = append(evaluationBindings, pattern.Binding{
			Placeholder: ModuleInitializationFormalBinding(parameter.Name), Value: evaluated.value,
		})
		sort.Slice(evaluationBindings, func(left, right int) bool {
			return evaluationBindings[left].Placeholder < evaluationBindings[right].Placeholder
		})
		reads = append(reads, evaluated.reads...)
		control = canonicalEventIDs(append(control, evaluated.causes...))
		operations = canonicalStateOperationReferences(append(operations, readOperations...))
	}
	sort.Slice(bindings, func(left, right int) bool {
		return bindings[left].Placeholder < bindings[right].Placeholder
	})
	return pattern.MatchResult{Bindings: bindings}, reads, control, operations, nil
}

// ModuleInitialParameter constructs one closed initialization formal.
func ModuleInitialParameter(name, typeName string, defaultValue RuleValue) ModuleInitializationParameter {
	return ModuleInitializationParameter{
		Name: name, Type: typeName, Default: copyRuleValue(defaultValue),
	}
}

// SetModuleInitializationParameters installs the ordered initial-part formals.
// They are snapshotted and become canonical model data.
func (component *Component) SetModuleInitializationParameters(parameters ...ModuleInitializationParameter) error {
	if component == nil {
		return fmt.Errorf("%w: component is nil", ErrInvalidModuleInitializationParameter)
	}
	if len(parameters) == 0 {
		return fmt.Errorf("%w: component %q parameter list is empty", ErrInvalidModuleInitializationParameter, component.ID)
	}
	component.mu.Lock()
	defer component.mu.Unlock()
	if component.initializationParameters != nil {
		return fmt.Errorf("%w: component %q already has initialization parameters", ErrInvalidModuleInitializationParameter, component.ID)
	}
	component.initializationParameters = copyModuleInitializationParameters(parameters)
	return nil
}

func copyModuleInitializationParameters(parameters []ModuleInitializationParameter) []ModuleInitializationParameter {
	if parameters == nil {
		return nil
	}
	result := make([]ModuleInitializationParameter, len(parameters))
	for index, parameter := range parameters {
		result[index] = ModuleInitializationParameter{
			Name: parameter.Name, Type: parameter.Type, Default: copyRuleValue(parameter.Default),
		}
	}
	return result
}

type canonicalModuleInitializationParameter struct {
	Name    string             `json:"name"`
	Type    string             `json:"type"`
	Default canonicalRuleValue `json:"default"`
}

type canonicalModuleInitializationArgument struct {
	Name  string             `json:"name"`
	Type  string             `json:"type"`
	Value canonicalRuleValue `json:"value"`
}

func copyModuleInitializationArguments(arguments []ModuleInitializationArgument) []ModuleInitializationArgument {
	if arguments == nil {
		return nil
	}
	result := make([]ModuleInitializationArgument, len(arguments))
	for index, argument := range arguments {
		result[index] = ModuleInitializationArgument{
			Name: argument.Name, Type: argument.Type, Value: copyRuleValue(argument.Value),
		}
	}
	return result
}

func canonicalizeNewInitializationArguments(
	owner string,
	arguments []ModuleInitializationArgument,
	stateTypes, placeholderTypes map[string]string,
) ([]ModuleInitializationArgument, []canonicalModuleInitializationArgument, error) {
	normalized := make([]ModuleInitializationArgument, 0, len(arguments))
	canonical := make([]canonicalModuleInitializationArgument, 0, len(arguments))
	seen := make(map[string]bool, len(arguments))
	availableTypes := make(map[string]string, len(placeholderTypes)+len(arguments))
	for name, typeName := range placeholderTypes {
		availableTypes[name] = typeName
	}
	for index, argument := range arguments {
		name := strings.TrimSpace(argument.Name)
		typeName := strings.TrimSpace(argument.Type)
		key := strings.ToLower(name)
		if name == "" || typeName == "" || seen[key] {
			return nil, nil, fmt.Errorf(
				"%w: %s actual %d has an empty or duplicate name/type",
				ErrInvalidModuleInitializationParameter, owner, index+1,
			)
		}
		if !supportedPredefinedType(typeName) {
			return nil, nil, fmt.Errorf(
				"%w: %s actual %q has unsupported type %q",
				ErrInvalidModuleInitializationParameter, owner, name, typeName,
			)
		}
		value, encodedValue, expressionType, err := canonicalizeClosedRuleValue(
			owner+" actual "+name, argument.Value, stateTypes, availableTypes,
		)
		if err != nil {
			return nil, nil, err
		}
		if !ruleValueAssignableToPredefined(value, expressionType, typeName) {
			return nil, nil, fmt.Errorf(
				"%w: %s actual %q has type %s, want %s",
				ErrInvalidModuleInitializationParameter, owner, name, expressionType, typeName,
			)
		}
		normalized = append(normalized, ModuleInitializationArgument{
			Name: name, Type: typeName, Value: value,
		})
		canonical = append(canonical, canonicalModuleInitializationArgument{
			Name: name, Type: typeName, Value: encodedValue,
		})
		availableTypes[ModuleInitializationFormalBinding(name)] = typeName
		seen[key] = true
	}
	return normalized, canonical, nil
}

func canonicalizeModuleInitializationParameters(
	componentID string,
	parameters []ModuleInitializationParameter,
	stateTypes map[string]string,
) ([]ModuleInitializationParameter, []canonicalModuleInitializationParameter, map[string]string, error) {
	if parameters == nil {
		return nil, nil, nil, nil
	}
	normalized := make([]ModuleInitializationParameter, 0, len(parameters))
	canonical := make([]canonicalModuleInitializationParameter, 0, len(parameters))
	formalTypes := make(map[string]string, len(parameters))
	seen := make(map[string]bool, len(parameters))
	for index, parameter := range parameters {
		name := strings.TrimSpace(parameter.Name)
		typeName := strings.TrimSpace(parameter.Type)
		key := strings.ToLower(name)
		if name == "" || typeName == "" || seen[key] {
			return nil, nil, nil, fmt.Errorf(
				"%w: component %q formal %d has an empty or duplicate name/type",
				ErrInvalidModuleInitializationParameter, componentID, index+1,
			)
		}
		if !supportedPredefinedType(typeName) {
			return nil, nil, nil, fmt.Errorf(
				"%w: component %q formal %q has unsupported type %q",
				ErrInvalidModuleInitializationParameter, componentID, name, typeName,
			)
		}
		if parameter.Default.kind == "" {
			return nil, nil, nil, fmt.Errorf(
				"%w: component %q formal %q has no default",
				ErrInvalidModuleInitializationParameter, componentID, name,
			)
		}
		defaultValue, encodedDefault, expressionType, err := canonicalizeClosedRuleValue(
			"module initialization formal "+componentID+"."+name,
			parameter.Default, stateTypes, formalTypes,
		)
		if err != nil {
			return nil, nil, nil, err
		}
		if !ruleValueAssignableToPredefined(defaultValue, expressionType, typeName) {
			return nil, nil, nil, fmt.Errorf(
				"%w: component %q formal %q default has type %s, want %s",
				ErrInvalidModuleInitializationParameter, componentID, name, expressionType, typeName,
			)
		}
		normalized = append(normalized, ModuleInitializationParameter{
			Name: name, Type: typeName, Default: defaultValue,
		})
		canonical = append(canonical, canonicalModuleInitializationParameter{
			Name: name, Type: typeName, Default: encodedDefault,
		})
		formalTypes[ModuleInitializationFormalBinding(name)] = typeName
		seen[key] = true
	}
	return normalized, canonical, formalTypes, nil
}

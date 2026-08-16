package arch

import (
	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/pattern"
)

// ConnectionParameter maps one target action parameter to a closed literal or
// a placeholder established by the source connection pattern.
type ConnectionParameter struct {
	Name  string
	Value RuleValue
}

// ConnectionLiteralParam declares a constant target parameter.
func ConnectionLiteralParam(name string, value any) ConnectionParameter {
	return ConnectionParameter{Name: name, Value: LiteralValue(value)}
}

// ConnectionBindingParam declares a target parameter substituted from a
// connection-trigger placeholder.
func ConnectionBindingParam(name, placeholder string) ConnectionParameter {
	return ConnectionParameter{Name: name, Value: BoundValue(placeholder)}
}

// ConnectionExpressionParam declares a closed target expression over literals
// and trigger bindings. Module-state reads are rejected in architecture scope.
func ConnectionExpressionParam(name string, value RuleValue) ConnectionParameter {
	return ConnectionParameter{Name: name, Value: copyRuleValue(value)}
}

func canonicalizeConnectionParameters(connectionID string, parameters []ConnectionParameter, placeholderTypes map[string]string) ([]ConnectionParameter, []canonicalRuleParameter, error) {
	normalized := make([]ConnectionParameter, 0, len(parameters))
	canonical := make([]canonicalRuleParameter, 0, len(parameters))
	seen := make(map[string]bool, len(parameters))
	bound := make(map[string]bool, len(placeholderTypes))
	for name := range placeholderTypes {
		bound[name] = true
	}
	for _, parameter := range parameters {
		normalizedRule, canonicalRule, err := canonicalizeRuleParameter(
			"connection/"+connectionID, "target", RuleParameter(parameter), bound, seen,
			nil, placeholderTypes,
		)
		if err != nil {
			return nil, nil, err
		}
		normalized = append(normalized, ConnectionParameter(normalizedRule))
		canonical = append(canonical, canonicalRule)
	}
	sortRuleParameters := func(i, j int) bool { return normalized[i].Name < normalized[j].Name }
	for i := 1; i < len(normalized); i++ {
		for j := i; j > 0 && sortRuleParameters(j, j-1); j-- {
			normalized[j], normalized[j-1] = normalized[j-1], normalized[j]
			canonical[j], canonical[j-1] = canonical[j-1], canonical[j]
		}
	}
	return normalized, canonical, nil
}

func (connection *Connection) resolveClosedParameters(trigger *gorapide.Event, bindings pattern.Bindings) (map[string]any, error) {
	if len(connection.Parameters) == 0 {
		return connection.resolveParams(trigger), nil
	}
	parameters := make(map[string]any, len(connection.Parameters))
	for _, parameter := range connection.Parameters {
		value, _, _, err := evaluateRuleValue("connection "+connection.ID+" parameter "+parameter.Name, parameter.Value, bindings, nil)
		if err != nil {
			return nil, err
		}
		parameters[parameter.Name] = value
	}
	return gorapide.CanonicalizeParams(parameters)
}

func connectionOutputShapeMatches(component *Component, action string, kinds []ActionKind, parameters []ConnectionParameter, placeholderTypes map[string]string) bool {
	if component == nil || component.Interface == nil {
		return false
	}
	return connectionOutputShapeMatchesInterface(component.Interface, action, kinds, parameters, placeholderTypes)
}

func connectionOutputShapeMatchesInterface(iface *InterfaceDecl, action string, kinds []ActionKind, parameters []ConnectionParameter, placeholderTypes map[string]string) bool {
	if iface == nil {
		return false
	}
	actions := append([]ActionDecl(nil), iface.Actions...)
	for _, service := range iface.Services {
		actions = append(actions, service.Actions...)
	}
	for _, declaration := range actions {
		kindMatches := false
		for _, kind := range kinds {
			if declaration.Kind == kind {
				kindMatches = true
				break
			}
		}
		if declaration.Name != action || !kindMatches || len(declaration.Params) != len(parameters) {
			continue
		}
		types := make(map[string]string, len(declaration.Params))
		for _, parameter := range declaration.Params {
			types[parameter.Name] = parameter.Type
		}
		matches := true
		for _, parameter := range parameters {
			typeName, ok := types[parameter.Name]
			effectivePlaceholderTypes := make(map[string]string, len(placeholderTypes))
			for name, typeName := range placeholderTypes {
				effectivePlaceholderTypes[name] = typeName
			}
			collectRuleValuePlaceholderTypes(parameter.Value, effectivePlaceholderTypes)
			_, _, expressionType, err := canonicalizeClosedRuleValue(
				"connection output parameter "+parameter.Name, parameter.Value, nil, effectivePlaceholderTypes,
			)
			if !ok || err != nil || (expressionType != "" && !ruleValueAssignableToPredefined(parameter.Value, expressionType, typeName)) ||
				(parameter.Value.kind == RuleLiteralValue && !valueMatchesPredefinedType(parameter.Value.literal, typeName)) {
				matches = false
				break
			}
		}
		if matches {
			return true
		}
	}
	return false
}

func collectRuleValuePlaceholderTypes(value RuleValue, types map[string]string) {
	if value.kind == RuleBindingValue && value.placeholder != "" {
		if _, exists := types[value.placeholder]; !exists {
			types[value.placeholder] = ""
		}
	}
	for _, operand := range value.operands {
		collectRuleValuePlaceholderTypes(operand, types)
	}
}

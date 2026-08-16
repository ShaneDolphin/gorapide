package arch

import (
	"fmt"
	"math"
	"strings"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/pattern"
)

const moduleSelfBindingName = "\x00gorapide:self"

// RuleValueOperator is one closed, side-effect-free Rapide expression
// operator. Operators are serialized by semantic name, never by a Go function.
type RuleValueOperator string

const (
	OperatorNegate            RuleValueOperator = "negate"
	OperatorPositive          RuleValueOperator = "positive"
	OperatorAbs               RuleValueOperator = "abs"
	OperatorIntegerPred       RuleValueOperator = "integer-pred"
	OperatorIntegerSucc       RuleValueOperator = "integer-succ"
	OperatorIntegerFloat      RuleValueOperator = "integer-float"
	OperatorNot               RuleValueOperator = "not"
	OperatorCharacterCode     RuleValueOperator = "character-code"
	OperatorCodeToCharacter   RuleValueOperator = "code-to-character"
	OperatorStringIsNull      RuleValueOperator = "string-is-null"
	OperatorStringLength      RuleValueOperator = "string-length"
	OperatorFloatFloor        RuleValueOperator = "float-floor"
	OperatorStringAppend      RuleValueOperator = "string-append"
	OperatorStringPrepend     RuleValueOperator = "string-prepend"
	OperatorStringConcatenate RuleValueOperator = "string-concatenate"
	OperatorStringIndex       RuleValueOperator = "string-index"
	OperatorStringSlice       RuleValueOperator = "string-slice"
	OperatorAdd               RuleValueOperator = "add"
	OperatorSubtract          RuleValueOperator = "subtract"
	OperatorMultiply          RuleValueOperator = "multiply"
	OperatorDivide            RuleValueOperator = "divide"
	OperatorEqual             RuleValueOperator = "equal"
	OperatorNotEqual          RuleValueOperator = "not-equal"
	OperatorLess              RuleValueOperator = "less"
	OperatorLessOrEqual       RuleValueOperator = "less-or-equal"
	OperatorGreater           RuleValueOperator = "greater"
	OperatorGreaterOrEqual    RuleValueOperator = "greater-or-equal"
	OperatorAnd               RuleValueOperator = "and"
	OperatorOr                RuleValueOperator = "or"
	OperatorXor               RuleValueOperator = "xor"
	OperatorNand              RuleValueOperator = "nand"
	OperatorNor               RuleValueOperator = "nor"
	OperatorAndThen           RuleValueOperator = "andthen"
	OperatorOrElse            RuleValueOperator = "orelse"
	OperatorIf                RuleValueOperator = "if"
	OperatorQualifyPrefix     RuleValueOperator = "qualify:"
)

type canonicalRuleValue struct {
	Kind                    RuleValueKind                           `json:"kind"`
	Placeholder             string                                  `json:"placeholder,omitempty"`
	State                   string                                  `json:"state,omitempty"`
	Type                    string                                  `json:"type,omitempty"`
	GeneratorArguments      []canonicalArchitectureArgument         `json:"generator_arguments,omitempty"`
	InitializationArguments []canonicalModuleInitializationArgument `json:"initialization_arguments,omitempty"`
	Literal                 *gorapide.CanonicalValue                `json:"literal,omitempty"`
	Operator                RuleValueOperator                       `json:"operator,omitempty"`
	Operands                []canonicalRuleValue                    `json:"operands,omitempty"`
}

// UnaryValue and BinaryValue construct closed expression nodes. Prefer the
// named helpers below for readable models.
func UnaryValue(operator RuleValueOperator, operand RuleValue) RuleValue {
	return RuleValue{kind: RuleUnaryValue, operator: operator, operands: []RuleValue{copyRuleValue(operand)}}
}

func BinaryValue(operator RuleValueOperator, left, right RuleValue) RuleValue {
	return RuleValue{kind: RuleBinaryValue, operator: operator, operands: []RuleValue{copyRuleValue(left), copyRuleValue(right)}}
}

func TernaryValue(operator RuleValueOperator, first, second, third RuleValue) RuleValue {
	return RuleValue{
		kind: RuleTernaryValue, operator: operator,
		operands: []RuleValue{copyRuleValue(first), copyRuleValue(second), copyRuleValue(third)},
	}
}

func NegateValue(value RuleValue) RuleValue { return UnaryValue(OperatorNegate, value) }
func PositiveValue(value RuleValue) RuleValue {
	return UnaryValue(OperatorPositive, value)
}
func AbsValue(value RuleValue) RuleValue { return UnaryValue(OperatorAbs, value) }
func IntegerPredValue(value RuleValue) RuleValue {
	return UnaryValue(OperatorIntegerPred, value)
}
func IntegerSuccValue(value RuleValue) RuleValue {
	return UnaryValue(OperatorIntegerSucc, value)
}
func IntegerFloatValue(value RuleValue) RuleValue {
	return UnaryValue(OperatorIntegerFloat, value)
}
func NotValue(value RuleValue) RuleValue { return UnaryValue(OperatorNot, value) }
func CharacterCodeValue(value RuleValue) RuleValue {
	return UnaryValue(OperatorCharacterCode, value)
}
func CodeToCharacterValue(value RuleValue) RuleValue {
	return UnaryValue(OperatorCodeToCharacter, value)
}
func StringIsNullValue(value RuleValue) RuleValue {
	return UnaryValue(OperatorStringIsNull, value)
}
func StringLengthValue(value RuleValue) RuleValue {
	return UnaryValue(OperatorStringLength, value)
}
func FloatFloorValue(value RuleValue) RuleValue {
	return UnaryValue(OperatorFloatFloor, value)
}
func StringAppendValue(value, character RuleValue) RuleValue {
	return BinaryValue(OperatorStringAppend, value, character)
}
func StringPrependValue(value, character RuleValue) RuleValue {
	return BinaryValue(OperatorStringPrepend, value, character)
}
func StringConcatenateValues(left, right RuleValue) RuleValue {
	return BinaryValue(OperatorStringConcatenate, left, right)
}
func StringIndexValue(value, position RuleValue) RuleValue {
	return BinaryValue(OperatorStringIndex, value, position)
}
func StringSliceValue(value, lower, upper RuleValue) RuleValue {
	return TernaryValue(OperatorStringSlice, value, lower, upper)
}
func AddValues(left, right RuleValue) RuleValue {
	return BinaryValue(OperatorAdd, left, right)
}
func SubtractValues(left, right RuleValue) RuleValue {
	return BinaryValue(OperatorSubtract, left, right)
}
func MultiplyValues(left, right RuleValue) RuleValue {
	return BinaryValue(OperatorMultiply, left, right)
}
func DivideValues(left, right RuleValue) RuleValue {
	return BinaryValue(OperatorDivide, left, right)
}
func EqualValues(left, right RuleValue) RuleValue {
	return BinaryValue(OperatorEqual, left, right)
}
func NotEqualValues(left, right RuleValue) RuleValue {
	return BinaryValue(OperatorNotEqual, left, right)
}
func LessValues(left, right RuleValue) RuleValue {
	return BinaryValue(OperatorLess, left, right)
}
func LessOrEqualValues(left, right RuleValue) RuleValue {
	return BinaryValue(OperatorLessOrEqual, left, right)
}
func GreaterValues(left, right RuleValue) RuleValue {
	return BinaryValue(OperatorGreater, left, right)
}
func GreaterOrEqualValues(left, right RuleValue) RuleValue {
	return BinaryValue(OperatorGreaterOrEqual, left, right)
}
func AndValues(left, right RuleValue) RuleValue {
	return BinaryValue(OperatorAnd, left, right)
}
func OrValues(left, right RuleValue) RuleValue {
	return BinaryValue(OperatorOr, left, right)
}
func XorValues(left, right RuleValue) RuleValue {
	return BinaryValue(OperatorXor, left, right)
}
func NandValues(left, right RuleValue) RuleValue {
	return BinaryValue(OperatorNand, left, right)
}
func NorValues(left, right RuleValue) RuleValue {
	return BinaryValue(OperatorNor, left, right)
}
func AndThenValues(left, right RuleValue) RuleValue {
	return BinaryValue(OperatorAndThen, left, right)
}
func OrElseValues(left, right RuleValue) RuleValue {
	return BinaryValue(OperatorOrElse, left, right)
}
func IfValue(condition, thenValue, elseValue RuleValue) RuleValue {
	return TernaryValue(OperatorIf, condition, thenValue, elseValue)
}

// QualifyValue constructs Stanford Rapide's T'(E) type-ascription node. It is
// an identity operation over the canonical value; its target type remains
// canonical model content because it can affect overload and visibility rules.
func QualifyValue(typeName string, value RuleValue) RuleValue {
	if canonical, ok := canonicalQualificationType(typeName); ok {
		typeName = canonical
	}
	return UnaryValue(RuleValueOperator(string(OperatorQualifyPrefix)+typeName), value)
}

// EvaluateConstant validates and evaluates a closed expression that contains
// no placeholder or state references. It is used by source front ends to make
// declaration-time values explicit model data through the same checked
// expression semantics used during execution.
func EvaluateConstant(value RuleValue) (any, string, error) {
	normalized, _, typeName, err := canonicalizeClosedRuleValue(
		"constant expression", value, map[string]string{}, map[string]string{},
	)
	if err != nil {
		return nil, "", err
	}
	evaluated, err := evaluateClosedRuleValue("constant expression", normalized, nil, nil)
	if err != nil {
		return nil, "", err
	}
	return evaluated.value, typeName, nil
}

func copyRuleValue(value RuleValue) RuleValue {
	value.newArguments = append([]ModuleGeneratorArgument(nil), value.newArguments...)
	value.newInitializationArguments = copyModuleInitializationArguments(value.newInitializationArguments)
	value.operands = append([]RuleValue(nil), value.operands...)
	for i := range value.operands {
		value.operands[i] = copyRuleValue(value.operands[i])
	}
	return value
}

func copyRuleValuePointer(value *RuleValue) *RuleValue {
	if value == nil {
		return nil
	}
	copy := copyRuleValue(*value)
	return &copy
}

func canonicalizeClosedRuleValue(owner string, value RuleValue, stateTypes, placeholderTypes map[string]string) (RuleValue, canonicalRuleValue, string, error) {
	encoded := canonicalRuleValue{Kind: value.kind}
	switch value.kind {
	case RuleLiteralValue:
		values, err := gorapide.CanonicalizeParams(map[string]any{"value": value.literal})
		if err != nil {
			return RuleValue{}, canonicalRuleValue{}, "", fmt.Errorf("%w: %s literal: %v", ErrInvalidStateReference, owner, err)
		}
		canonical, err := gorapide.CanonicalizeParameters(values)
		if err != nil {
			return RuleValue{}, canonicalRuleValue{}, "", err
		}
		value.literal = values["value"]
		encoded.Literal = &canonical[0].Value
		return value, encoded, predefinedTypeOfValue(value.literal), nil
	case RuleBindingValue:
		typeName, ok := placeholderTypes[value.placeholder]
		if value.placeholder == "" || !ok {
			return RuleValue{}, canonicalRuleValue{}, "", fmt.Errorf("%w: %s references unbound placeholder %q", ErrInvalidStateReference, owner, value.placeholder)
		}
		encoded.Placeholder = value.placeholder
		return value, encoded, typeName, nil
	case RuleStateValue:
		typeName, ok := stateTypes[value.state]
		if value.state == "" || !ok {
			return RuleValue{}, canonicalRuleValue{}, "", fmt.Errorf("%w: %s reads undeclared state %q", ErrInvalidStateReference, owner, value.state)
		}
		encoded.State = value.state
		return value, encoded, typeName, nil
	case RuleSelfValue:
		if strings.TrimSpace(value.selfType) == "" {
			return RuleValue{}, canonicalRuleValue{}, "", fmt.Errorf("%w: %s Self has no enclosing interface type", ErrInvalidStateReference, owner)
		}
		encoded.Type = value.selfType
		return value, encoded, value.selfType, nil
	case RuleNewValue:
		if strings.TrimSpace(value.newType) == "" {
			return RuleValue{}, canonicalRuleValue{}, "", fmt.Errorf("%w: %s New has no enclosing module result type", ErrInvalidStateReference, owner)
		}
		arguments, canonicalArguments, err := canonicalizeNewGeneratorArguments(value.newArguments)
		if err != nil {
			return RuleValue{}, canonicalRuleValue{}, "", fmt.Errorf("%w: %s New generator arguments: %v", ErrInvalidStateReference, owner, err)
		}
		value.newArguments = arguments
		initializationArguments, canonicalInitializationArguments, err := canonicalizeNewInitializationArguments(
			owner+" New initialization arguments", value.newInitializationArguments,
			stateTypes, placeholderTypes,
		)
		if err != nil {
			return RuleValue{}, canonicalRuleValue{}, "", err
		}
		value.newInitializationArguments = initializationArguments
		encoded.Type = value.newType
		encoded.GeneratorArguments = canonicalArguments
		encoded.InitializationArguments = canonicalInitializationArguments
		return value, encoded, value.newType, nil
	case RuleUnaryValue, RuleBinaryValue, RuleTernaryValue:
		expected := 1
		if value.kind == RuleBinaryValue {
			expected = 2
		} else if value.kind == RuleTernaryValue {
			expected = 3
		}
		if len(value.operands) != expected {
			return RuleValue{}, canonicalRuleValue{}, "", fmt.Errorf("%w: %s %s expression has %d operands, want %d", ErrInvalidStateReference, owner, value.operator, len(value.operands), expected)
		}
		normalized := value
		normalized.operands = make([]RuleValue, len(value.operands))
		encoded.Operator = value.operator
		encoded.Operands = make([]canonicalRuleValue, len(value.operands))
		types := make([]string, len(value.operands))
		for i, operand := range value.operands {
			operandValue, operandCanonical, operandType, err := canonicalizeClosedRuleValue(owner, operand, stateTypes, placeholderTypes)
			if err != nil {
				return RuleValue{}, canonicalRuleValue{}, "", err
			}
			normalized.operands[i] = operandValue
			encoded.Operands[i] = operandCanonical
			types[i] = operandType
		}
		if target, ok := qualifiedRuleType(value.operator); ok &&
			!predefinedRuleTypeAssignable(types[0], target) {
			// Source compilation already enforces the subtype rule. During
			// lowering, however, a closed Positive/Natural module constant is
			// represented by its canonical Integer value. Recover that erased
			// constraint only when the operand is independently closed and its
			// exact canonical value proves membership in the target.
			evaluated, evaluateErr := evaluateClosedRuleValue(owner, normalized.operands[0], nil, nil)
			if evaluateErr == nil && gorapide.CanonicalValueMatchesPredefinedType(evaluated.value, target) {
				return normalized, encoded, target, nil
			}
		}
		resultType, err := expressionResultType(value.kind, value.operator, types)
		if err != nil {
			return RuleValue{}, canonicalRuleValue{}, "", fmt.Errorf("%w: %s: %v", ErrInvalidStateReference, owner, err)
		}
		return normalized, encoded, resultType, nil
	default:
		return RuleValue{}, canonicalRuleValue{}, "", fmt.Errorf("%w: %s has expression kind %q", ErrInvalidStateReference, owner, value.kind)
	}
}

func expressionResultType(kind RuleValueKind, operator RuleValueOperator, types []string) (string, error) {
	unknown := false
	for _, typeName := range types {
		unknown = unknown || typeName == ""
	}
	if kind == RuleUnaryValue {
		if target, ok := qualifiedRuleType(operator); ok {
			if unknown || predefinedRuleTypeAssignable(types[0], target) {
				return target, nil
			}
			return "", fmt.Errorf("qualification operand has type %s, which is not a subtype of %s", types[0], target)
		}
		switch operator {
		case OperatorNegate:
			if unknown {
				return "", nil
			}
			if isIntegerType(types[0]) {
				return "Integer", nil
			}
			if types[0] == "Float" {
				return "Float", nil
			}
		case OperatorPositive:
			if unknown {
				return "", nil
			}
			if isIntegerType(types[0]) {
				return "Integer", nil
			}
			if types[0] == "Float" {
				return "Float", nil
			}
		case OperatorAbs:
			if unknown {
				return "", nil
			}
			if isIntegerType(types[0]) {
				return "Integer", nil
			}
			if types[0] == "Float" {
				return "Float", nil
			}
		case OperatorIntegerPred, OperatorIntegerSucc:
			if unknown {
				return "", nil
			}
			if isIntegerType(types[0]) {
				return "Integer", nil
			}
		case OperatorIntegerFloat:
			if unknown {
				return "", nil
			}
			if isIntegerType(types[0]) {
				return "Float", nil
			}
		case OperatorNot:
			if unknown {
				return "", nil
			}
			if types[0] == "Boolean" {
				return "Boolean", nil
			}
		case OperatorCharacterCode:
			if unknown {
				return "", nil
			}
			if types[0] == "Character" {
				return "Integer", nil
			}
		case OperatorCodeToCharacter:
			if unknown {
				return "", nil
			}
			if isIntegerType(types[0]) {
				return "Character", nil
			}
		case OperatorStringIsNull:
			if unknown {
				return "", nil
			}
			if types[0] == "String" {
				return "Boolean", nil
			}
		case OperatorStringLength:
			if unknown {
				return "", nil
			}
			if types[0] == "String" {
				return "Integer", nil
			}
		case OperatorFloatFloor:
			if unknown {
				return "", nil
			}
			if types[0] == "Float" {
				return "Integer", nil
			}
		default:
			return "", fmt.Errorf("unsupported unary operator %q", operator)
		}
		return "", fmt.Errorf("operator %s is not defined for %s", operator, types[0])
	}
	if kind == RuleTernaryValue {
		if operator == OperatorIf {
			if unknown {
				return "", nil
			}
			if types[0] != "Boolean" {
				return "", fmt.Errorf("operator if requires Boolean condition, got %s", types[0])
			}
			if resultType, ok := conditionalResultType(types[1], types[2]); ok {
				return resultType, nil
			}
			return "", fmt.Errorf("operator if has incompatible result types %s and %s", types[1], types[2])
		}
		if operator != OperatorStringSlice {
			return "", fmt.Errorf("unsupported ternary operator %q", operator)
		}
		if unknown {
			return "", nil
		}
		if types[0] == "String" && isIntegerType(types[1]) && isIntegerType(types[2]) {
			return "String", nil
		}
		return "", fmt.Errorf("operator %s is not defined for %s, %s, and %s", operator, types[0], types[1], types[2])
	}

	if kind != RuleBinaryValue {
		return "", fmt.Errorf("unsupported expression kind %q", kind)
	}
	if !isRuleBinaryOperator(operator) {
		return "", fmt.Errorf("unsupported binary operator %q", operator)
	}
	if unknown {
		return "", nil
	}
	left, right := types[0], types[1]
	switch operator {
	case OperatorStringIndex:
		if left == "String" && isIntegerType(right) {
			return "Character", nil
		}
	case OperatorStringConcatenate:
		if left == "String" && right == "String" {
			return "String", nil
		}
	case OperatorStringAppend, OperatorStringPrepend:
		if left == "String" && right == "Character" {
			return "String", nil
		}
	case OperatorAdd, OperatorSubtract, OperatorMultiply, OperatorDivide:
		if isIntegerType(left) && isIntegerType(right) {
			switch operator {
			case OperatorAdd:
				if left == "Positive" || right == "Positive" {
					if left != "Integer" && right != "Integer" {
						return "Positive", nil
					}
				}
				if left != "Integer" && right != "Integer" {
					return "Natural", nil
				}
			case OperatorMultiply:
				if left == "Positive" && right == "Positive" {
					return "Positive", nil
				}
				if left != "Integer" && right != "Integer" {
					return "Natural", nil
				}
			}
			return "Integer", nil
		}
		if left == "Float" && right == "Float" {
			return "Float", nil
		}
	case OperatorEqual, OperatorNotEqual:
		if predefinedTypesComparable(left, right) {
			return "Boolean", nil
		}
	case OperatorLess, OperatorLessOrEqual, OperatorGreater, OperatorGreaterOrEqual:
		if (isIntegerType(left) && isIntegerType(right)) ||
			(left == "Float" && right == "Float") ||
			(left == "Character" && right == "Character") {
			return "Boolean", nil
		}
	case OperatorAnd, OperatorOr, OperatorXor, OperatorNand, OperatorNor, OperatorAndThen, OperatorOrElse:
		if left == "Boolean" && right == "Boolean" {
			return "Boolean", nil
		}
	}
	return "", fmt.Errorf("operator %s is not defined for %s and %s", operator, left, right)
}

func conditionalResultType(left, right string) (string, bool) {
	if left == right && left != "" {
		return left, true
	}
	if !isIntegerType(left) || !isIntegerType(right) {
		return "", false
	}
	if left == "Integer" || right == "Integer" {
		return "Integer", true
	}
	if left == "Natural" || right == "Natural" {
		return "Natural", true
	}
	return "Positive", true
}

func isRuleBinaryOperator(operator RuleValueOperator) bool {
	switch operator {
	case OperatorStringAppend, OperatorStringPrepend, OperatorStringConcatenate, OperatorStringIndex,
		OperatorAdd, OperatorSubtract, OperatorMultiply, OperatorDivide,
		OperatorEqual, OperatorNotEqual, OperatorLess, OperatorLessOrEqual,
		OperatorGreater, OperatorGreaterOrEqual, OperatorAnd, OperatorOr, OperatorXor,
		OperatorNand, OperatorNor, OperatorAndThen, OperatorOrElse:
		return true
	default:
		return false
	}
}

func isIntegerType(typeName string) bool {
	return typeName == "Integer" || typeName == "Natural" || typeName == "Positive"
}

func canonicalQualificationType(typeName string) (string, bool) {
	switch {
	case strings.EqualFold(typeName, "Triv"):
		return "Triv", true
	case strings.EqualFold(typeName, "Boolean"):
		return "Boolean", true
	case strings.EqualFold(typeName, "Integer"):
		return "Integer", true
	case strings.EqualFold(typeName, "Natural"):
		return "Natural", true
	case strings.EqualFold(typeName, "Positive"):
		return "Positive", true
	case strings.EqualFold(typeName, "Float"):
		return "Float", true
	case strings.EqualFold(typeName, "Character"):
		return "Character", true
	case strings.EqualFold(typeName, "String"):
		return "String", true
	default:
		return "", false
	}
}

func qualifiedRuleType(operator RuleValueOperator) (string, bool) {
	spelling := string(operator)
	prefix := string(OperatorQualifyPrefix)
	if !strings.HasPrefix(strings.ToLower(spelling), prefix) {
		return "", false
	}
	return canonicalQualificationType(spelling[len(prefix):])
}

func predefinedRuleTypeAssignable(source, target string) bool {
	if source == target {
		return true
	}
	switch target {
	case "Integer":
		return source == "Natural" || source == "Positive"
	case "Natural":
		return source == "Positive"
	default:
		return false
	}
}

func predefinedTypesComparable(left, right string) bool {
	return left == right && gorapide.IsSupportedPredefinedType(left) ||
		(isIntegerType(left) && isIntegerType(right))
}

type evaluatedRuleValue struct {
	value  any
	causes []gorapide.EventID
	reads  []StateReadRecord
}

func evaluateClosedRuleValue(owner string, value RuleValue, bindings pattern.Bindings, cells map[string]*stateCell) (evaluatedRuleValue, error) {
	switch value.kind {
	case RuleLiteralValue:
		values, err := gorapide.CanonicalizeParams(map[string]any{"value": value.literal})
		if err != nil {
			return evaluatedRuleValue{}, err
		}
		return evaluatedRuleValue{value: values["value"]}, nil
	case RuleBindingValue:
		bound, ok := bindings.Lookup(value.placeholder)
		if !ok {
			return evaluatedRuleValue{}, fmt.Errorf("%w: %s has no binding for %q", ErrInvalidStateReference, owner, value.placeholder)
		}
		values, err := gorapide.CanonicalizeParams(map[string]any{"value": bound})
		if err != nil {
			return evaluatedRuleValue{}, err
		}
		return evaluatedRuleValue{value: values["value"]}, nil
	case RuleStateValue:
		cell := cells[value.state]
		if cell == nil {
			return evaluatedRuleValue{}, fmt.Errorf("%w: %s reads missing state %q", ErrInvalidStateReference, owner, value.state)
		}
		values, err := gorapide.CanonicalizeParams(map[string]any{"value": cell.value})
		if err != nil {
			return evaluatedRuleValue{}, err
		}
		encoded, err := gorapide.CanonicalizeParameters(values)
		if err != nil {
			return evaluatedRuleValue{}, err
		}
		causes := eventIDStrings(cell.causes)
		operationID, err := cell.history.recordDereference(owner, cell.version, encoded[0].Value, causes)
		if err != nil {
			return evaluatedRuleValue{}, err
		}
		return evaluatedRuleValue{
			value: values["value"], causes: append([]gorapide.EventID(nil), cell.causes...),
			reads: []StateReadRecord{{
				Name: value.state, OperationID: operationID, Version: cell.version,
				Value: encoded[0].Value, Causes: causes,
				operation: stateOperationReference{id: operationID, history: cell.history},
			}},
		}, nil
	case RuleSelfValue:
		bound, ok := bindings.Lookup(moduleSelfBindingName)
		if !ok {
			return evaluatedRuleValue{}, fmt.Errorf("%w: %s has no executing module for Self", ErrInvalidStateReference, owner)
		}
		module, ok := bound.(gorapide.RapideModuleValue)
		if !ok || module.Identity() == "" {
			return evaluatedRuleValue{}, fmt.Errorf("%w: %s has an invalid Self module value", ErrInvalidStateReference, owner)
		}
		return evaluatedRuleValue{value: module}, nil
	case RuleNewValue:
		return evaluatedRuleValue{}, fmt.Errorf("%w: %s New requires an immediate generated-action allocation context", ErrInvalidStateReference, owner)
	case RuleUnaryValue, RuleBinaryValue, RuleTernaryValue:
		if value.kind == RuleTernaryValue && value.operator == OperatorIf {
			condition, err := evaluateClosedRuleValue(owner, value.operands[0], bindings, cells)
			if err != nil {
				return evaluatedRuleValue{}, err
			}
			conditionBoolean, ok := condition.value.(bool)
			if !ok {
				return evaluatedRuleValue{}, fmt.Errorf("%w: %s: operator if requires Boolean condition", ErrInvalidStateReference, owner)
			}
			branch := 2
			if conditionBoolean {
				branch = 1
			}
			selected, err := evaluateClosedRuleValue(owner, value.operands[branch], bindings, cells)
			if err != nil {
				return evaluatedRuleValue{}, err
			}
			return evaluatedRuleValue{
				value: selected.value, causes: canonicalEventIDs(append(condition.causes, selected.causes...)),
				reads: append(condition.reads, selected.reads...),
			}, nil
		}
		if value.kind == RuleBinaryValue && (value.operator == OperatorAndThen || value.operator == OperatorOrElse) {
			left, err := evaluateClosedRuleValue(owner, value.operands[0], bindings, cells)
			if err != nil {
				return evaluatedRuleValue{}, err
			}
			leftBoolean, ok := left.value.(bool)
			if !ok {
				return evaluatedRuleValue{}, fmt.Errorf("%w: %s: operator %s requires Boolean left operand", ErrInvalidStateReference, owner, value.operator)
			}
			if value.operator == OperatorAndThen && !leftBoolean || value.operator == OperatorOrElse && leftBoolean {
				return evaluatedRuleValue{value: leftBoolean, causes: canonicalEventIDs(left.causes), reads: left.reads}, nil
			}
			right, err := evaluateClosedRuleValue(owner, value.operands[1], bindings, cells)
			if err != nil {
				return evaluatedRuleValue{}, err
			}
			rightBoolean, ok := right.value.(bool)
			if !ok {
				return evaluatedRuleValue{}, fmt.Errorf("%w: %s: operator %s requires Boolean right operand", ErrInvalidStateReference, owner, value.operator)
			}
			return evaluatedRuleValue{
				value: rightBoolean, causes: canonicalEventIDs(append(left.causes, right.causes...)),
				reads: append(left.reads, right.reads...),
			}, nil
		}
		values := make([]any, len(value.operands))
		var causes []gorapide.EventID
		var reads []StateReadRecord
		for i, operand := range value.operands {
			evaluated, err := evaluateClosedRuleValue(owner, operand, bindings, cells)
			if err != nil {
				return evaluatedRuleValue{}, err
			}
			values[i] = evaluated.value
			causes = append(causes, evaluated.causes...)
			reads = append(reads, evaluated.reads...)
		}
		result, err := applyRuleValueOperator(value.kind, value.operator, values)
		if err != nil {
			return evaluatedRuleValue{}, fmt.Errorf("%w: %s: %v", ErrInvalidStateReference, owner, err)
		}
		return evaluatedRuleValue{value: result, causes: canonicalEventIDs(causes), reads: reads}, nil
	default:
		return evaluatedRuleValue{}, fmt.Errorf("%w: %s has expression kind %q", ErrInvalidStateReference, owner, value.kind)
	}
}

func applyRuleValueOperator(kind RuleValueKind, operator RuleValueOperator, operands []any) (any, error) {
	if kind == RuleUnaryValue {
		if len(operands) != 1 {
			return nil, fmt.Errorf("unary operator %s has %d operands", operator, len(operands))
		}
		if target, ok := qualifiedRuleType(operator); ok {
			if !gorapide.CanonicalValueMatchesPredefinedType(operands[0], target) {
				return nil, fmt.Errorf("qualification value does not match %s", target)
			}
			return operands[0], nil
		}
		switch operator {
		case OperatorNegate:
			switch value := operands[0].(type) {
			case int64:
				if value == math.MinInt64 {
					return nil, fmt.Errorf("integer overflow")
				}
				return -value, nil
			case float64:
				return canonicalExpressionValue(-value)
			}
		case OperatorPositive:
			switch value := operands[0].(type) {
			case int64:
				return value, nil
			case float64:
				return canonicalExpressionValue(value)
			}
		case OperatorAbs:
			switch value := operands[0].(type) {
			case int64:
				if value == math.MinInt64 {
					return nil, fmt.Errorf("integer overflow")
				}
				if value < 0 {
					return -value, nil
				}
				return value, nil
			case float64:
				return gorapide.RapideFloat64Abs(value)
			}
		case OperatorIntegerPred:
			if value, ok := operands[0].(int64); ok {
				if value == math.MinInt64 {
					return nil, fmt.Errorf("integer overflow")
				}
				return value - 1, nil
			}
		case OperatorIntegerSucc:
			if value, ok := operands[0].(int64); ok {
				if value == math.MaxInt64 {
					return nil, fmt.Errorf("integer overflow")
				}
				return value + 1, nil
			}
		case OperatorIntegerFloat:
			if value, ok := operands[0].(int64); ok {
				return gorapide.RapideIntegerToFloat(value)
			}
		case OperatorNot:
			if value, ok := operands[0].(bool); ok {
				return !value, nil
			}
		case OperatorCharacterCode:
			if value, ok := operands[0].(gorapide.RapideCharacter); ok {
				return value.Code(), nil
			}
		case OperatorCodeToCharacter:
			if value, ok := operands[0].(int64); ok {
				return gorapide.RapideCharacterFromCode(value), nil
			}
		case OperatorStringIsNull, OperatorStringLength:
			codes, err := gorapide.CanonicalRapideStringCodes(operands[0])
			if err != nil {
				return nil, err
			}
			if operator == OperatorStringIsNull {
				return len(codes) == 0, nil
			}
			return int64(len(codes)), nil
		case OperatorFloatFloor:
			if value, ok := operands[0].(float64); ok {
				return gorapide.RapideFloat64Floor(value)
			}
		}
		return nil, fmt.Errorf("operator %s is not defined for %T", operator, operands[0])
	}
	if kind == RuleTernaryValue {
		if operator != OperatorStringSlice || len(operands) != 3 {
			return nil, fmt.Errorf("ternary operator %s has %d operands", operator, len(operands))
		}
		codes, err := gorapide.CanonicalRapideStringCodes(operands[0])
		if err != nil {
			return nil, err
		}
		lower, lowerOK := operands[1].(int64)
		upper, upperOK := operands[2].(int64)
		if !lowerOK || !upperOK {
			return nil, fmt.Errorf("operator %s has incompatible operands %T, %T, and %T", operator, operands[0], operands[1], operands[2])
		}
		if err := validateStringSliceBounds(len(codes), lower, upper); err != nil {
			return nil, err
		}
		if lower > upper {
			return "", nil
		}
		return canonicalExpressionValue(gorapide.RapideStringFromCodes(codes[lower-1 : upper]...))
	}
	if kind != RuleBinaryValue || len(operands) != 2 {
		return nil, fmt.Errorf("binary operator %s has %d operands", operator, len(operands))
	}
	left, right := operands[0], operands[1]
	if operator == OperatorStringIndex {
		codes, err := gorapide.CanonicalRapideStringCodes(left)
		if err != nil {
			return nil, err
		}
		position, ok := right.(int64)
		if !ok {
			return nil, fmt.Errorf("operator %s has incompatible operands %T and %T", operator, left, right)
		}
		if position < 1 || position > int64(len(codes)) {
			return nil, fmt.Errorf("String index %d is outside 1..%d", position, len(codes))
		}
		return gorapide.RapideCharacterFromCode(codes[position-1]), nil
	}
	if operator == OperatorStringConcatenate {
		leftCodes, err := gorapide.CanonicalRapideStringCodes(left)
		if err != nil {
			return nil, err
		}
		rightCodes, err := gorapide.CanonicalRapideStringCodes(right)
		if err != nil {
			return nil, err
		}
		codes := make([]int64, 0, len(leftCodes)+len(rightCodes))
		codes = append(codes, leftCodes...)
		codes = append(codes, rightCodes...)
		return canonicalExpressionValue(gorapide.RapideStringFromCodes(codes...))
	}
	if operator == OperatorStringAppend || operator == OperatorStringPrepend {
		codes, err := gorapide.CanonicalRapideStringCodes(left)
		if err != nil {
			return nil, err
		}
		character, ok := right.(gorapide.RapideCharacter)
		if !ok {
			return nil, fmt.Errorf("operator %s has incompatible operands %T and %T", operator, left, right)
		}
		if operator == OperatorStringAppend {
			codes = append(codes, character.Code())
		} else {
			codes = append([]int64{character.Code()}, codes...)
		}
		return canonicalExpressionValue(gorapide.RapideStringFromCodes(codes...))
	}
	if leftInteger, ok := left.(int64); ok {
		rightInteger, compatible := right.(int64)
		if !compatible {
			return nil, fmt.Errorf("operator %s has incompatible operands %T and %T", operator, left, right)
		}
		return applyIntegerOperator(operator, leftInteger, rightInteger)
	}
	if leftFloat, ok := left.(float64); ok {
		rightFloat, compatible := right.(float64)
		if !compatible {
			return nil, fmt.Errorf("operator %s has incompatible operands %T and %T", operator, left, right)
		}
		return applyFloatOperator(operator, leftFloat, rightFloat)
	}
	if leftBoolean, ok := left.(bool); ok {
		rightBoolean, compatible := right.(bool)
		if !compatible {
			return nil, fmt.Errorf("operator %s has incompatible operands %T and %T", operator, left, right)
		}
		switch operator {
		case OperatorEqual:
			return leftBoolean == rightBoolean, nil
		case OperatorNotEqual:
			return leftBoolean != rightBoolean, nil
		case OperatorAnd:
			return leftBoolean && rightBoolean, nil
		case OperatorOr:
			return leftBoolean || rightBoolean, nil
		case OperatorXor:
			return leftBoolean != rightBoolean, nil
		case OperatorNand:
			return !(leftBoolean && rightBoolean), nil
		case OperatorNor:
			return !(leftBoolean || rightBoolean), nil
		case OperatorAndThen:
			return leftBoolean && rightBoolean, nil
		case OperatorOrElse:
			return leftBoolean || rightBoolean, nil
		}
	}
	if leftCharacter, ok := left.(gorapide.RapideCharacter); ok {
		rightCharacter, compatible := right.(gorapide.RapideCharacter)
		if !compatible {
			return nil, fmt.Errorf("operator %s has incompatible operands %T and %T", operator, left, right)
		}
		leftCode, rightCode := leftCharacter.Code(), rightCharacter.Code()
		switch operator {
		case OperatorEqual:
			return leftCode == rightCode, nil
		case OperatorNotEqual:
			return leftCode != rightCode, nil
		case OperatorLess:
			return leftCode < rightCode, nil
		case OperatorLessOrEqual:
			return leftCode <= rightCode, nil
		case OperatorGreater:
			return leftCode > rightCode, nil
		case OperatorGreaterOrEqual:
			return leftCode >= rightCode, nil
		}
	}
	if leftString, ok := left.(string); ok {
		rightString, compatible := right.(string)
		if !compatible {
			return nil, fmt.Errorf("operator %s has incompatible operands %T and %T", operator, left, right)
		}
		switch operator {
		case OperatorEqual:
			return leftString == rightString, nil
		case OperatorNotEqual:
			return leftString != rightString, nil
		}
	}
	if operator == OperatorEqual || operator == OperatorNotEqual {
		equal, err := gorapide.CanonicalValuesEqual(left, right)
		if err != nil {
			return nil, err
		}
		if operator == OperatorNotEqual {
			equal = !equal
		}
		return equal, nil
	}
	return nil, fmt.Errorf("operator %s is not defined for %T and %T", operator, left, right)
}

func validateStringSliceBounds(length int, lower, upper int64) error {
	if lower < 1 || lower > int64(length)+1 || upper < 0 || upper > int64(length) || lower <= upper && lower > int64(length) {
		return fmt.Errorf("String slice %d..%d is outside length %d", lower, upper, length)
	}
	return nil
}

func applyIntegerOperator(operator RuleValueOperator, left, right int64) (any, error) {
	switch operator {
	case OperatorAdd:
		if (right > 0 && left > math.MaxInt64-right) || (right < 0 && left < math.MinInt64-right) {
			return nil, fmt.Errorf("integer overflow")
		}
		return left + right, nil
	case OperatorSubtract:
		if (right < 0 && left > math.MaxInt64+right) || (right > 0 && left < math.MinInt64+right) {
			return nil, fmt.Errorf("integer overflow")
		}
		return left - right, nil
	case OperatorMultiply:
		if left == 0 || right == 0 {
			return int64(0), nil
		}
		if (left == math.MinInt64 && right == -1) || (right == math.MinInt64 && left == -1) {
			return nil, fmt.Errorf("integer overflow")
		}
		result := left * right
		if result/right != left {
			return nil, fmt.Errorf("integer overflow")
		}
		return result, nil
	case OperatorDivide:
		if right == 0 {
			return nil, fmt.Errorf("division by zero")
		}
		if left == math.MinInt64 && right == -1 {
			return nil, fmt.Errorf("integer overflow")
		}
		return left / right, nil
	case OperatorEqual:
		return left == right, nil
	case OperatorNotEqual:
		return left != right, nil
	case OperatorLess:
		return left < right, nil
	case OperatorLessOrEqual:
		return left <= right, nil
	case OperatorGreater:
		return left > right, nil
	case OperatorGreaterOrEqual:
		return left >= right, nil
	default:
		return nil, fmt.Errorf("operator %s is not defined for integers", operator)
	}
}

func applyFloatOperator(operator RuleValueOperator, left, right float64) (any, error) {
	switch operator {
	case OperatorAdd:
		return gorapide.RapideFloat64Arithmetic("+", left, right)
	case OperatorSubtract:
		return gorapide.RapideFloat64Arithmetic("-", left, right)
	case OperatorMultiply:
		return gorapide.RapideFloat64Arithmetic("*", left, right)
	case OperatorDivide:
		return gorapide.RapideFloat64Arithmetic("/", left, right)
	case OperatorEqual:
		return gorapide.RapideFloat64Compare("=", left, right)
	case OperatorNotEqual:
		return gorapide.RapideFloat64Compare("/=", left, right)
	case OperatorLess:
		return gorapide.RapideFloat64Compare("<", left, right)
	case OperatorLessOrEqual:
		return gorapide.RapideFloat64Compare("<=", left, right)
	case OperatorGreater:
		return gorapide.RapideFloat64Compare(">", left, right)
	case OperatorGreaterOrEqual:
		return gorapide.RapideFloat64Compare(">=", left, right)
	default:
		return nil, fmt.Errorf("operator %s is not defined for floats", operator)
	}
}

func canonicalExpressionValue(value any) (any, error) {
	canonical, err := gorapide.CanonicalizeParams(map[string]any{"value": value})
	if err != nil {
		return nil, err
	}
	return canonical["value"], nil
}

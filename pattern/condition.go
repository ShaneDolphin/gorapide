package pattern

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/ShaneDolphin/gorapide"
)

// ErrInvalidCondition identifies a malformed or ill-typed expression in the
// closed, deterministic subset of Rapide pattern guards.
var ErrInvalidCondition = errors.New("invalid deterministic pattern guard")

type conditionKind string

const (
	conditionLiteral conditionKind = "literal"
	conditionBinding conditionKind = "binding"
	conditionState   conditionKind = "state"
	conditionUnary   conditionKind = "unary"
	conditionBinary  conditionKind = "binary"
	conditionTernary conditionKind = "ternary"
)

// ConditionOperator is one side-effect-free algebraic operator supported by
// the first closed pattern-guard subset. The spellings follow Rapide source.
type ConditionOperator string

const (
	ConditionNegate            ConditionOperator = "-"
	ConditionPositive          ConditionOperator = "+"
	ConditionAbs               ConditionOperator = "abs"
	ConditionIntegerPred       ConditionOperator = "integer-pred"
	ConditionIntegerSucc       ConditionOperator = "integer-succ"
	ConditionIntegerFloat      ConditionOperator = "integer-float"
	ConditionNot               ConditionOperator = "not"
	ConditionCharacterCode     ConditionOperator = "character-code"
	ConditionCodeToCharacter   ConditionOperator = "code-to-character"
	ConditionStringIsNull      ConditionOperator = "string-is-null"
	ConditionStringLength      ConditionOperator = "string-length"
	ConditionFloatFloor        ConditionOperator = "float-floor"
	ConditionStringAppend      ConditionOperator = "string-append"
	ConditionStringPrepend     ConditionOperator = "string-prepend"
	ConditionStringConcatenate ConditionOperator = "string-concatenate"
	ConditionStringIndex       ConditionOperator = "string-index"
	ConditionStringSlice       ConditionOperator = "string-slice"
	ConditionAdd               ConditionOperator = "+"
	ConditionSubtract          ConditionOperator = "-"
	ConditionMultiply          ConditionOperator = "*"
	ConditionDivide            ConditionOperator = "/"
	ConditionEqual             ConditionOperator = "="
	ConditionNotEqual          ConditionOperator = "/="
	ConditionLess              ConditionOperator = "<"
	ConditionLessOrEqual       ConditionOperator = "<="
	ConditionGreater           ConditionOperator = ">"
	ConditionGreaterEqual      ConditionOperator = ">="
	ConditionAnd               ConditionOperator = "and"
	ConditionOr                ConditionOperator = "or"
	ConditionXor               ConditionOperator = "xor"
	ConditionNand              ConditionOperator = "nand"
	ConditionNor               ConditionOperator = "nor"
	ConditionAndThen           ConditionOperator = "andthen"
	ConditionOrElse            ConditionOperator = "orelse"
	ConditionIf                ConditionOperator = "if"
	ConditionQualifyPrefix     ConditionOperator = "qualify:"
)

// Condition is a closed, copyable Rapide algebraic expression. It deliberately
// has no callback or host-language evaluation hook. State dereferences are
// evaluated only from explicit consistent-cut witness data.
type Condition struct {
	kind        conditionKind
	literal     any
	placeholder *Placeholder
	state       string
	stateType   string
	operator    ConditionOperator
	operands    []Condition
}

// LiteralCondition constructs a canonical literal guard operand.
func LiteralCondition(value any) Condition {
	return Condition{kind: conditionLiteral, literal: value}
}

// BindingCondition reads the substitution value of a pattern placeholder.
func BindingCondition(placeholder *Placeholder) Condition {
	var copy *Placeholder
	if placeholder != nil {
		value := *placeholder
		copy = &value
	}
	return Condition{kind: conditionBinding, placeholder: copy}
}

// StateCondition reads one statically resolved state-witness key. It can only
// be evaluated by MatchWithStateWitnesses; ordinary matching fails explicitly.
func StateCondition(key, typeName string) Condition {
	return Condition{kind: conditionState, state: key, stateType: typeName}
}

// UnaryCondition constructs a side-effect-free unary guard expression.
func UnaryCondition(operator ConditionOperator, operand Condition) Condition {
	return Condition{kind: conditionUnary, operator: operator, operands: []Condition{copyCondition(operand)}}
}

// BinaryCondition constructs a side-effect-free binary guard expression.
func BinaryCondition(operator ConditionOperator, left, right Condition) Condition {
	return Condition{kind: conditionBinary, operator: operator, operands: []Condition{copyCondition(left), copyCondition(right)}}
}

func TernaryCondition(operator ConditionOperator, first, second, third Condition) Condition {
	return Condition{
		kind: conditionTernary, operator: operator,
		operands: []Condition{copyCondition(first), copyCondition(second), copyCondition(third)},
	}
}

// QualifiedCondition constructs the supported scalar form of Stanford
// Rapide's T'(E) qualified expression. Evaluation preserves the value and
// revalidates membership in the explicitly stated type.
func QualifiedCondition(typeName string, operand Condition) Condition {
	if canonical, ok := canonicalConditionQualificationType(typeName); ok {
		typeName = canonical
	}
	return UnaryCondition(ConditionOperator(string(ConditionQualifyPrefix)+typeName), operand)
}

func copyCondition(condition Condition) Condition {
	copy := condition
	if condition.placeholder != nil {
		placeholder := *condition.placeholder
		copy.placeholder = &placeholder
	}
	copy.operands = make([]Condition, len(condition.operands))
	for index := range condition.operands {
		copy.operands[index] = copyCondition(condition.operands[index])
	}
	return copy
}

// wherePattern is Stanford Rapide's P where B pattern node. Unlike the legacy
// Guard helper, this node is closed, canonical, binding-aware, and auditable.
type wherePattern struct {
	pattern   Pattern
	condition Condition
}

// Where creates a deterministic guarded pattern. B is evaluated separately
// for every match of P using that match's complete placeholder environment.
func Where(expression Pattern, condition Condition) Pattern {
	if expression == nil {
		panic("pattern.Where: requires a non-nil sub-pattern")
	}
	return &wherePattern{pattern: expression, condition: copyCondition(condition)}
}

func (guard *wherePattern) Match(poset PosetReader) []gorapide.EventSet {
	matches, err := MatchWithBindings(guard, poset)
	if err != nil {
		return []gorapide.EventSet{}
	}
	result := make([]gorapide.EventSet, 0, len(matches))
	for _, match := range matches {
		result = append(result, append(gorapide.EventSet(nil), match.Events...))
	}
	return result
}

func (guard *wherePattern) String() string {
	if guard == nil || guard.pattern == nil {
		return "Where(<invalid>)"
	}
	return fmt.Sprintf("Where(%s, %s)", guard.pattern.String(), conditionString(guard.condition))
}

type conditionEvaluation struct {
	value    any
	typeName string
}

func evaluateCondition(condition Condition, bindings Bindings) (conditionEvaluation, error) {
	return evaluateConditionWithState(condition, bindings, nil)
}

func evaluateConditionWithState(condition Condition, bindings Bindings, state map[string]any) (conditionEvaluation, error) {
	switch condition.kind {
	case conditionLiteral:
		value, err := canonicalConditionValue(condition.literal)
		if err != nil {
			return conditionEvaluation{}, err
		}
		return conditionEvaluation{value: value, typeName: conditionValueType(value)}, nil
	case conditionBinding:
		if condition.placeholder == nil || condition.placeholder.name == "" {
			return conditionEvaluation{}, fmt.Errorf("%w: binding has no placeholder", ErrInvalidCondition)
		}
		value, exists := bindings.Lookup(condition.placeholder.name)
		if !exists {
			return conditionEvaluation{}, fmt.Errorf("%w: placeholder ?%s has no substitution value", ErrInvalidCondition, condition.placeholder.name)
		}
		if condition.placeholder.typ != "" && !gorapide.CanonicalValueMatchesPredefinedType(value, condition.placeholder.typ) {
			return conditionEvaluation{}, fmt.Errorf("%w: placeholder ?%s value does not match %s", ErrInvalidCondition, condition.placeholder.name, condition.placeholder.typ)
		}
		canonical, err := canonicalConditionValue(value)
		if err != nil {
			return conditionEvaluation{}, err
		}
		return conditionEvaluation{value: canonical, typeName: conditionValueType(canonical)}, nil
	case conditionState:
		if condition.state == "" || condition.stateType == "" {
			return conditionEvaluation{}, fmt.Errorf("%w: state operand has an empty key or type", ErrInvalidCondition)
		}
		if state == nil {
			return conditionEvaluation{}, fmt.Errorf("%w: state operand %q requires a consistent-cut witness", ErrInvalidCondition, condition.state)
		}
		value, exists := state[condition.state]
		if !exists {
			return conditionEvaluation{}, fmt.Errorf("%w: state witness has no value for %q", ErrInvalidCondition, condition.state)
		}
		if !gorapide.CanonicalValueMatchesPredefinedType(value, condition.stateType) {
			return conditionEvaluation{}, fmt.Errorf("%w: state witness value %q does not match %s", ErrInvalidCondition, condition.state, condition.stateType)
		}
		canonical, err := canonicalConditionValue(value)
		if err != nil {
			return conditionEvaluation{}, err
		}
		return conditionEvaluation{value: canonical, typeName: conditionValueType(canonical)}, nil
	case conditionUnary:
		if len(condition.operands) != 1 {
			return conditionEvaluation{}, fmt.Errorf("%w: unary operator %q has %d operands", ErrInvalidCondition, condition.operator, len(condition.operands))
		}
		operand, err := evaluateConditionWithState(condition.operands[0], bindings, state)
		if err != nil {
			return conditionEvaluation{}, err
		}
		if target, ok := qualifiedConditionType(condition.operator); ok {
			if !gorapide.CanonicalValueMatchesPredefinedType(operand.value, target) {
				return conditionEvaluation{}, fmt.Errorf("%w: qualification value does not match %s", ErrInvalidCondition, target)
			}
			return conditionEvaluation{value: operand.value, typeName: conditionAlgebraType(target)}, nil
		}
		switch condition.operator {
		case ConditionNegate:
			switch value := operand.value.(type) {
			case int64:
				if value == math.MinInt64 {
					return conditionEvaluation{}, conditionOperatorError(condition.operator, operand.typeName)
				}
				return conditionEvaluation{value: -value, typeName: "Integer"}, nil
			case float64:
				canonical, err := canonicalConditionValue(-value)
				if err != nil {
					return conditionEvaluation{}, err
				}
				return conditionEvaluation{value: canonical, typeName: "Float"}, nil
			default:
				return conditionEvaluation{}, conditionOperatorError(condition.operator, operand.typeName)
			}
		case ConditionPositive:
			switch value := operand.value.(type) {
			case int64:
				return conditionEvaluation{value: value, typeName: "Integer"}, nil
			case float64:
				canonical, err := canonicalConditionValue(value)
				if err != nil {
					return conditionEvaluation{}, err
				}
				return conditionEvaluation{value: canonical, typeName: "Float"}, nil
			default:
				return conditionEvaluation{}, conditionOperatorError(condition.operator, operand.typeName)
			}
		case ConditionAbs:
			switch value := operand.value.(type) {
			case int64:
				if value == math.MinInt64 {
					return conditionEvaluation{}, fmt.Errorf("%w: integer overflow in %q", ErrInvalidCondition, condition.operator)
				}
				if value < 0 {
					value = -value
				}
				return conditionEvaluation{value: value, typeName: "Integer"}, nil
			case float64:
				absolute, err := gorapide.RapideFloat64Abs(value)
				if err != nil {
					return conditionEvaluation{}, fmt.Errorf("%w: %v", ErrInvalidCondition, err)
				}
				return conditionEvaluation{value: absolute, typeName: "Float"}, nil
			default:
				return conditionEvaluation{}, conditionOperatorError(condition.operator, operand.typeName)
			}
		case ConditionIntegerPred:
			value, ok := operand.value.(int64)
			if !ok {
				return conditionEvaluation{}, conditionOperatorError(condition.operator, operand.typeName)
			}
			if value == math.MinInt64 {
				return conditionEvaluation{}, fmt.Errorf("%w: integer overflow in %q", ErrInvalidCondition, condition.operator)
			}
			return conditionEvaluation{value: value - 1, typeName: "Integer"}, nil
		case ConditionIntegerSucc:
			value, ok := operand.value.(int64)
			if !ok {
				return conditionEvaluation{}, conditionOperatorError(condition.operator, operand.typeName)
			}
			if value == math.MaxInt64 {
				return conditionEvaluation{}, fmt.Errorf("%w: integer overflow in %q", ErrInvalidCondition, condition.operator)
			}
			return conditionEvaluation{value: value + 1, typeName: "Integer"}, nil
		case ConditionIntegerFloat:
			value, ok := operand.value.(int64)
			if !ok {
				return conditionEvaluation{}, conditionOperatorError(condition.operator, operand.typeName)
			}
			converted, err := gorapide.RapideIntegerToFloat(value)
			if err != nil {
				return conditionEvaluation{}, fmt.Errorf("%w: %v", ErrInvalidCondition, err)
			}
			return conditionEvaluation{value: converted, typeName: "Float"}, nil
		case ConditionNot:
			boolean, ok := operand.value.(bool)
			if !ok {
				return conditionEvaluation{}, conditionOperatorError(condition.operator, operand.typeName)
			}
			return conditionEvaluation{value: !boolean, typeName: "Boolean"}, nil
		case ConditionCharacterCode:
			character, ok := operand.value.(gorapide.RapideCharacter)
			if !ok {
				return conditionEvaluation{}, conditionOperatorError(condition.operator, operand.typeName)
			}
			return conditionEvaluation{value: character.Code(), typeName: "Integer"}, nil
		case ConditionCodeToCharacter:
			code, ok := operand.value.(int64)
			if !ok {
				return conditionEvaluation{}, conditionOperatorError(condition.operator, operand.typeName)
			}
			return conditionEvaluation{value: gorapide.RapideCharacterFromCode(code), typeName: "Character"}, nil
		case ConditionStringIsNull, ConditionStringLength:
			codes, err := gorapide.CanonicalRapideStringCodes(operand.value)
			if err != nil {
				return conditionEvaluation{}, conditionOperatorError(condition.operator, operand.typeName)
			}
			if condition.operator == ConditionStringIsNull {
				return conditionEvaluation{value: len(codes) == 0, typeName: "Boolean"}, nil
			}
			return conditionEvaluation{value: int64(len(codes)), typeName: "Integer"}, nil
		case ConditionFloatFloor:
			value, ok := operand.value.(float64)
			if !ok {
				return conditionEvaluation{}, conditionOperatorError(condition.operator, operand.typeName)
			}
			floored, err := gorapide.RapideFloat64Floor(value)
			if err != nil {
				return conditionEvaluation{}, fmt.Errorf("%w: %v", ErrInvalidCondition, err)
			}
			return conditionEvaluation{value: floored, typeName: "Integer"}, nil
		default:
			return conditionEvaluation{}, fmt.Errorf("%w: unsupported unary operator %q", ErrInvalidCondition, condition.operator)
		}
	case conditionBinary:
		if len(condition.operands) != 2 {
			return conditionEvaluation{}, fmt.Errorf("%w: binary operator %q has %d operands", ErrInvalidCondition, condition.operator, len(condition.operands))
		}
		left, err := evaluateConditionWithState(condition.operands[0], bindings, state)
		if err != nil {
			return conditionEvaluation{}, err
		}
		if condition.operator == ConditionAndThen || condition.operator == ConditionOrElse {
			leftBoolean, ok := left.value.(bool)
			if !ok {
				return conditionEvaluation{}, conditionOperatorError(condition.operator, left.typeName, "Boolean")
			}
			if condition.operator == ConditionAndThen && !leftBoolean || condition.operator == ConditionOrElse && leftBoolean {
				return conditionEvaluation{value: leftBoolean, typeName: "Boolean"}, nil
			}
		}
		right, err := evaluateConditionWithState(condition.operands[1], bindings, state)
		if err != nil {
			return conditionEvaluation{}, err
		}
		return evaluateBinaryCondition(condition.operator, left, right)
	case conditionTernary:
		if len(condition.operands) != 3 {
			return conditionEvaluation{}, fmt.Errorf("%w: ternary operator %q has %d operands", ErrInvalidCondition, condition.operator, len(condition.operands))
		}
		if condition.operator == ConditionIf {
			conditionValue, err := evaluateConditionWithState(condition.operands[0], bindings, state)
			if err != nil {
				return conditionEvaluation{}, err
			}
			conditionBoolean, ok := conditionValue.value.(bool)
			if !ok {
				return conditionEvaluation{}, conditionOperatorError(condition.operator, conditionValue.typeName)
			}
			branch := 2
			if conditionBoolean {
				branch = 1
			}
			return evaluateConditionWithState(condition.operands[branch], bindings, state)
		}
		values := make([]conditionEvaluation, 3)
		for index := range condition.operands {
			value, err := evaluateConditionWithState(condition.operands[index], bindings, state)
			if err != nil {
				return conditionEvaluation{}, err
			}
			values[index] = value
		}
		return evaluateTernaryCondition(condition.operator, values)
	default:
		return conditionEvaluation{}, fmt.Errorf("%w: condition kind %q", ErrInvalidCondition, condition.kind)
	}
}

func evaluateTernaryCondition(operator ConditionOperator, operands []conditionEvaluation) (conditionEvaluation, error) {
	if operator != ConditionStringSlice || len(operands) != 3 ||
		operands[0].typeName != "String" || operands[1].typeName != "Integer" || operands[2].typeName != "Integer" {
		types := make([]string, len(operands))
		for index := range operands {
			types[index] = operands[index].typeName
		}
		return conditionEvaluation{}, conditionOperatorError(operator, types...)
	}
	codes, err := gorapide.CanonicalRapideStringCodes(operands[0].value)
	if err != nil {
		return conditionEvaluation{}, fmt.Errorf("%w: %v", ErrInvalidCondition, err)
	}
	lower := operands[1].value.(int64)
	upper := operands[2].value.(int64)
	if err := validateConditionStringSliceBounds(len(codes), lower, upper); err != nil {
		return conditionEvaluation{}, err
	}
	if lower > upper {
		return conditionEvaluation{value: "", typeName: "String"}, nil
	}
	value, err := canonicalConditionValue(gorapide.RapideStringFromCodes(codes[lower-1 : upper]...))
	if err != nil {
		return conditionEvaluation{}, err
	}
	return conditionEvaluation{value: value, typeName: "String"}, nil
}

func validateConditionStringSliceBounds(length int, lower, upper int64) error {
	if lower < 1 || lower > int64(length)+1 || upper < 0 || upper > int64(length) || lower <= upper && lower > int64(length) {
		return fmt.Errorf("%w: String slice %d..%d is outside length %d", ErrInvalidCondition, lower, upper, length)
	}
	return nil
}

func evaluateBinaryCondition(operator ConditionOperator, left, right conditionEvaluation) (conditionEvaluation, error) {
	if operator == ConditionStringIndex {
		if left.typeName != "String" || right.typeName != "Integer" {
			return conditionEvaluation{}, conditionOperatorError(operator, left.typeName, right.typeName)
		}
		codes, err := gorapide.CanonicalRapideStringCodes(left.value)
		if err != nil {
			return conditionEvaluation{}, fmt.Errorf("%w: %v", ErrInvalidCondition, err)
		}
		position, ok := right.value.(int64)
		if !ok {
			return conditionEvaluation{}, conditionOperatorError(operator, left.typeName, right.typeName)
		}
		if position < 1 || position > int64(len(codes)) {
			return conditionEvaluation{}, fmt.Errorf("%w: String index %d is outside 1..%d", ErrInvalidCondition, position, len(codes))
		}
		return conditionEvaluation{value: gorapide.RapideCharacterFromCode(codes[position-1]), typeName: "Character"}, nil
	}
	if operator == ConditionStringConcatenate {
		if left.typeName != "String" || right.typeName != "String" {
			return conditionEvaluation{}, conditionOperatorError(operator, left.typeName, right.typeName)
		}
		leftCodes, err := gorapide.CanonicalRapideStringCodes(left.value)
		if err != nil {
			return conditionEvaluation{}, fmt.Errorf("%w: %v", ErrInvalidCondition, err)
		}
		rightCodes, err := gorapide.CanonicalRapideStringCodes(right.value)
		if err != nil {
			return conditionEvaluation{}, fmt.Errorf("%w: %v", ErrInvalidCondition, err)
		}
		codes := make([]int64, 0, len(leftCodes)+len(rightCodes))
		codes = append(codes, leftCodes...)
		codes = append(codes, rightCodes...)
		value, err := canonicalConditionValue(gorapide.RapideStringFromCodes(codes...))
		if err != nil {
			return conditionEvaluation{}, err
		}
		return conditionEvaluation{value: value, typeName: "String"}, nil
	}
	if operator == ConditionStringAppend || operator == ConditionStringPrepend {
		if left.typeName != "String" || right.typeName != "Character" {
			return conditionEvaluation{}, conditionOperatorError(operator, left.typeName, right.typeName)
		}
		codes, err := gorapide.CanonicalRapideStringCodes(left.value)
		if err != nil {
			return conditionEvaluation{}, fmt.Errorf("%w: %v", ErrInvalidCondition, err)
		}
		character, ok := right.value.(gorapide.RapideCharacter)
		if !ok {
			return conditionEvaluation{}, conditionOperatorError(operator, left.typeName, right.typeName)
		}
		if operator == ConditionStringAppend {
			codes = append(codes, character.Code())
		} else {
			codes = append([]int64{character.Code()}, codes...)
		}
		value, err := canonicalConditionValue(gorapide.RapideStringFromCodes(codes...))
		if err != nil {
			return conditionEvaluation{}, err
		}
		return conditionEvaluation{value: value, typeName: "String"}, nil
	}
	switch operator {
	case ConditionEqual, ConditionNotEqual:
		if left.typeName == "" || left.typeName != right.typeName {
			return conditionEvaluation{}, conditionOperatorError(operator, left.typeName, right.typeName)
		}
		equal, err := gorapide.CanonicalValuesEqual(left.value, right.value)
		if err != nil {
			return conditionEvaluation{}, fmt.Errorf("%w: equality: %v", ErrInvalidCondition, err)
		}
		if operator == ConditionNotEqual {
			equal = !equal
		}
		return conditionEvaluation{value: equal, typeName: "Boolean"}, nil
	case ConditionAnd, ConditionOr, ConditionXor, ConditionNand, ConditionNor, ConditionAndThen, ConditionOrElse:
		leftBoolean, leftOK := left.value.(bool)
		rightBoolean, rightOK := right.value.(bool)
		if !leftOK || !rightOK {
			return conditionEvaluation{}, conditionOperatorError(operator, left.typeName, right.typeName)
		}
		if operator == ConditionAnd {
			return conditionEvaluation{value: leftBoolean && rightBoolean, typeName: "Boolean"}, nil
		}
		if operator == ConditionOr {
			return conditionEvaluation{value: leftBoolean || rightBoolean, typeName: "Boolean"}, nil
		}
		if operator == ConditionXor {
			return conditionEvaluation{value: leftBoolean != rightBoolean, typeName: "Boolean"}, nil
		}
		if operator == ConditionNand {
			return conditionEvaluation{value: !(leftBoolean && rightBoolean), typeName: "Boolean"}, nil
		}
		if operator == ConditionNor {
			return conditionEvaluation{value: !(leftBoolean || rightBoolean), typeName: "Boolean"}, nil
		}
		if operator == ConditionAndThen {
			return conditionEvaluation{value: leftBoolean && rightBoolean, typeName: "Boolean"}, nil
		}
		return conditionEvaluation{value: leftBoolean || rightBoolean, typeName: "Boolean"}, nil
	}
	leftFloat, leftIsFloat := left.value.(float64)
	rightFloat, rightIsFloat := right.value.(float64)
	if leftIsFloat || rightIsFloat {
		if !leftIsFloat || !rightIsFloat {
			return conditionEvaluation{}, conditionOperatorError(operator, left.typeName, right.typeName)
		}
		switch operator {
		case ConditionAdd, ConditionSubtract, ConditionMultiply, ConditionDivide:
			value, err := gorapide.RapideFloat64Arithmetic(string(operator), leftFloat, rightFloat)
			if err != nil {
				return conditionEvaluation{}, fmt.Errorf("%w: %v", ErrInvalidCondition, err)
			}
			return conditionEvaluation{value: value, typeName: "Float"}, nil
		case ConditionLess, ConditionLessOrEqual, ConditionGreater, ConditionGreaterEqual:
			value, err := gorapide.RapideFloat64Compare(string(operator), leftFloat, rightFloat)
			if err != nil {
				return conditionEvaluation{}, fmt.Errorf("%w: %v", ErrInvalidCondition, err)
			}
			return conditionEvaluation{value: value, typeName: "Boolean"}, nil
		default:
			return conditionEvaluation{}, fmt.Errorf("%w: unsupported binary operator %q", ErrInvalidCondition, operator)
		}
	}

	leftCharacter, leftIsCharacter := left.value.(gorapide.RapideCharacter)
	rightCharacter, rightIsCharacter := right.value.(gorapide.RapideCharacter)
	if leftIsCharacter || rightIsCharacter {
		if !leftIsCharacter || !rightIsCharacter {
			return conditionEvaluation{}, conditionOperatorError(operator, left.typeName, right.typeName)
		}
		leftCode, rightCode := leftCharacter.Code(), rightCharacter.Code()
		switch operator {
		case ConditionLess:
			return conditionEvaluation{value: leftCode < rightCode, typeName: "Boolean"}, nil
		case ConditionLessOrEqual:
			return conditionEvaluation{value: leftCode <= rightCode, typeName: "Boolean"}, nil
		case ConditionGreater:
			return conditionEvaluation{value: leftCode > rightCode, typeName: "Boolean"}, nil
		case ConditionGreaterEqual:
			return conditionEvaluation{value: leftCode >= rightCode, typeName: "Boolean"}, nil
		default:
			return conditionEvaluation{}, conditionOperatorError(operator, left.typeName, right.typeName)
		}
	}

	leftInteger, leftOK := left.value.(int64)
	rightInteger, rightOK := right.value.(int64)
	if !leftOK || !rightOK {
		return conditionEvaluation{}, conditionOperatorError(operator, left.typeName, right.typeName)
	}
	switch operator {
	case ConditionAdd:
		if (rightInteger > 0 && leftInteger > math.MaxInt64-rightInteger) ||
			(rightInteger < 0 && leftInteger < math.MinInt64-rightInteger) {
			return conditionEvaluation{}, fmt.Errorf("%w: integer overflow in %q", ErrInvalidCondition, operator)
		}
		return conditionEvaluation{value: leftInteger + rightInteger, typeName: "Integer"}, nil
	case ConditionSubtract:
		if (rightInteger < 0 && leftInteger > math.MaxInt64+rightInteger) ||
			(rightInteger > 0 && leftInteger < math.MinInt64+rightInteger) {
			return conditionEvaluation{}, fmt.Errorf("%w: integer overflow in %q", ErrInvalidCondition, operator)
		}
		return conditionEvaluation{value: leftInteger - rightInteger, typeName: "Integer"}, nil
	case ConditionMultiply:
		if leftInteger == 0 || rightInteger == 0 {
			return conditionEvaluation{value: int64(0), typeName: "Integer"}, nil
		}
		if leftInteger == math.MinInt64 && rightInteger == -1 ||
			rightInteger == math.MinInt64 && leftInteger == -1 {
			return conditionEvaluation{}, fmt.Errorf("%w: integer overflow in %q", ErrInvalidCondition, operator)
		}
		product := leftInteger * rightInteger
		if product/rightInteger != leftInteger {
			return conditionEvaluation{}, fmt.Errorf("%w: integer overflow in %q", ErrInvalidCondition, operator)
		}
		return conditionEvaluation{value: product, typeName: "Integer"}, nil
	case ConditionDivide:
		if rightInteger == 0 {
			return conditionEvaluation{}, fmt.Errorf("%w: division by zero", ErrInvalidCondition)
		}
		if leftInteger == math.MinInt64 && rightInteger == -1 {
			return conditionEvaluation{}, fmt.Errorf("%w: integer overflow in %q", ErrInvalidCondition, operator)
		}
		return conditionEvaluation{value: leftInteger / rightInteger, typeName: "Integer"}, nil
	case ConditionLess:
		return conditionEvaluation{value: leftInteger < rightInteger, typeName: "Boolean"}, nil
	case ConditionLessOrEqual:
		return conditionEvaluation{value: leftInteger <= rightInteger, typeName: "Boolean"}, nil
	case ConditionGreater:
		return conditionEvaluation{value: leftInteger > rightInteger, typeName: "Boolean"}, nil
	case ConditionGreaterEqual:
		return conditionEvaluation{value: leftInteger >= rightInteger, typeName: "Boolean"}, nil
	default:
		return conditionEvaluation{}, fmt.Errorf("%w: unsupported binary operator %q", ErrInvalidCondition, operator)
	}
}

func canonicalConditionValue(value any) (any, error) {
	values, err := gorapide.CanonicalizeParams(map[string]any{"value": value})
	if err != nil {
		return nil, fmt.Errorf("%w: literal or binding: %v", ErrInvalidCondition, err)
	}
	return values["value"], nil
}

func conditionValueType(value any) string {
	switch value.(type) {
	case gorapide.RapideTriv:
		return "Triv"
	case bool:
		return "Boolean"
	case int64:
		return "Integer"
	case float64:
		return "Float"
	case gorapide.RapideCharacter:
		return "Character"
	case gorapide.RapideString:
		return "String"
	case string:
		return "String"
	default:
		return ""
	}
}

func conditionOperatorError(operator ConditionOperator, types ...string) error {
	return fmt.Errorf("%w: operator %q is not defined for %s", ErrInvalidCondition, operator, strings.Join(types, " and "))
}

type canonicalCondition struct {
	Kind        string                   `json:"kind"`
	Placeholder string                   `json:"placeholder,omitempty"`
	Type        string                   `json:"type,omitempty"`
	State       string                   `json:"state,omitempty"`
	Literal     *gorapide.CanonicalValue `json:"literal,omitempty"`
	Operator    ConditionOperator        `json:"operator,omitempty"`
	Operands    []canonicalCondition     `json:"operands,omitempty"`
}

func deterministicConditionKey(condition Condition) (string, error) {
	canonical, err := canonicalizeCondition(condition)
	if err != nil {
		return "", err
	}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return "", fmt.Errorf("%w: canonical encoding: %v", ErrInvalidCondition, err)
	}
	return string(encoded), nil
}

func validateConditionType(condition Condition, boundTypes map[string]string) (string, error) {
	switch condition.kind {
	case conditionLiteral:
		value, err := canonicalConditionValue(condition.literal)
		if err != nil {
			return "", err
		}
		return conditionValueType(value), nil
	case conditionBinding:
		if condition.placeholder == nil || condition.placeholder.name == "" {
			return "", fmt.Errorf("%w: binding has no placeholder", ErrInvalidCondition)
		}
		boundType, exists := boundTypes[condition.placeholder.name]
		if !exists {
			return "", fmt.Errorf("%w: placeholder ?%s is not bound by the guarded pattern", ErrInvalidCondition, condition.placeholder.name)
		}
		conditionType := condition.placeholder.typ
		if boundType != "" && conditionType != "" && boundType != conditionType {
			return "", fmt.Errorf("%w: placeholder ?%s has guard type %s but pattern type %s", ErrInvalidCondition, condition.placeholder.name, conditionType, boundType)
		}
		if conditionType == "" {
			conditionType = boundType
		}
		return conditionAlgebraType(conditionType), nil
	case conditionState:
		if condition.state == "" || !supportedPlaceholderType(condition.stateType) {
			return "", fmt.Errorf("%w: state operand has invalid key %q or type %q", ErrInvalidCondition, condition.state, condition.stateType)
		}
		return conditionAlgebraType(condition.stateType), nil
	case conditionUnary:
		if len(condition.operands) != 1 || !validConditionOperator(conditionUnary, condition.operator) {
			return "", fmt.Errorf("%w: unary operator %q has invalid arity or spelling", ErrInvalidCondition, condition.operator)
		}
		operandType, err := validateConditionType(condition.operands[0], boundTypes)
		if err != nil {
			return "", err
		}
		if target, ok := qualifiedConditionType(condition.operator); ok {
			if target == "Natural" || target == "Positive" {
				return "", fmt.Errorf("%w: qualification target %s requires constrained source-type preservation", ErrInvalidCondition, target)
			}
			target = conditionAlgebraType(target)
			if operandType != "" && !conditionTypeAssignable(operandType, target) {
				return "", conditionOperatorError(condition.operator, operandType)
			}
			return target, nil
		}
		switch condition.operator {
		case ConditionNegate:
			if operandType != "" && operandType != "Integer" && operandType != "Float" {
				return "", conditionOperatorError(condition.operator, operandType)
			}
			return operandType, nil
		case ConditionPositive:
			if operandType != "" && operandType != "Integer" && operandType != "Float" {
				return "", conditionOperatorError(condition.operator, operandType)
			}
			return operandType, nil
		case ConditionAbs:
			if operandType != "" && operandType != "Integer" && operandType != "Float" {
				return "", conditionOperatorError(condition.operator, operandType)
			}
			return operandType, nil
		case ConditionIntegerPred, ConditionIntegerSucc:
			if operandType != "" && operandType != "Integer" {
				return "", conditionOperatorError(condition.operator, operandType)
			}
			return "Integer", nil
		case ConditionIntegerFloat:
			if operandType != "" && operandType != "Integer" {
				return "", conditionOperatorError(condition.operator, operandType)
			}
			return "Float", nil
		case ConditionNot:
			if operandType != "" && operandType != "Boolean" {
				return "", conditionOperatorError(condition.operator, operandType)
			}
			return "Boolean", nil
		case ConditionCharacterCode:
			if operandType != "" && operandType != "Character" {
				return "", conditionOperatorError(condition.operator, operandType)
			}
			return "Integer", nil
		case ConditionCodeToCharacter:
			if operandType != "" && operandType != "Integer" {
				return "", conditionOperatorError(condition.operator, operandType)
			}
			return "Character", nil
		case ConditionStringIsNull:
			if operandType != "" && operandType != "String" {
				return "", conditionOperatorError(condition.operator, operandType)
			}
			return "Boolean", nil
		case ConditionStringLength:
			if operandType != "" && operandType != "String" {
				return "", conditionOperatorError(condition.operator, operandType)
			}
			return "Integer", nil
		case ConditionFloatFloor:
			if operandType != "" && operandType != "Float" {
				return "", conditionOperatorError(condition.operator, operandType)
			}
			return "Integer", nil
		}
	case conditionBinary:
		if len(condition.operands) != 2 || !validConditionOperator(conditionBinary, condition.operator) {
			return "", fmt.Errorf("%w: binary operator %q has invalid arity or spelling", ErrInvalidCondition, condition.operator)
		}
		leftType, err := validateConditionType(condition.operands[0], boundTypes)
		if err != nil {
			return "", err
		}
		rightType, err := validateConditionType(condition.operands[1], boundTypes)
		if err != nil {
			return "", err
		}
		switch condition.operator {
		case ConditionStringIndex:
			if leftType != "" && leftType != "String" || rightType != "" && rightType != "Integer" {
				return "", conditionOperatorError(condition.operator, leftType, rightType)
			}
			return "Character", nil
		case ConditionStringConcatenate:
			if leftType != "" && leftType != "String" || rightType != "" && rightType != "String" {
				return "", conditionOperatorError(condition.operator, leftType, rightType)
			}
			return "String", nil
		case ConditionStringAppend, ConditionStringPrepend:
			if leftType != "" && leftType != "String" || rightType != "" && rightType != "Character" {
				return "", conditionOperatorError(condition.operator, leftType, rightType)
			}
			return "String", nil
		case ConditionEqual, ConditionNotEqual:
			if leftType != "" && rightType != "" && leftType != rightType {
				return "", conditionOperatorError(condition.operator, leftType, rightType)
			}
			return "Boolean", nil
		case ConditionAnd, ConditionOr, ConditionXor, ConditionNand, ConditionNor, ConditionAndThen, ConditionOrElse:
			if leftType != "" && leftType != "Boolean" || rightType != "" && rightType != "Boolean" {
				return "", conditionOperatorError(condition.operator, leftType, rightType)
			}
			return "Boolean", nil
		case ConditionLess, ConditionLessOrEqual, ConditionGreater, ConditionGreaterEqual:
			if leftType != "" && rightType != "" &&
				(leftType != rightType || leftType != "Integer" && leftType != "Float" && leftType != "Character") {
				return "", conditionOperatorError(condition.operator, leftType, rightType)
			}
			return "Boolean", nil
		case ConditionAdd, ConditionSubtract, ConditionMultiply, ConditionDivide:
			if leftType != "" && rightType != "" &&
				(leftType != rightType || leftType != "Integer" && leftType != "Float") {
				return "", conditionOperatorError(condition.operator, leftType, rightType)
			}
			if leftType != "" {
				return leftType, nil
			}
			return rightType, nil
		}
	case conditionTernary:
		if len(condition.operands) != 3 || !validConditionOperator(conditionTernary, condition.operator) {
			return "", fmt.Errorf("%w: ternary operator %q has invalid arity or spelling", ErrInvalidCondition, condition.operator)
		}
		types := make([]string, 3)
		for index := range condition.operands {
			valueType, err := validateConditionType(condition.operands[index], boundTypes)
			if err != nil {
				return "", err
			}
			types[index] = valueType
		}
		if condition.operator == ConditionIf {
			if types[0] != "" && types[0] != "Boolean" {
				return "", conditionOperatorError(condition.operator, types...)
			}
			if resultType, ok := conditionConditionalResultType(types[1], types[2]); ok {
				return resultType, nil
			}
			return "", conditionOperatorError(condition.operator, types...)
		}
		if types[0] != "" && types[0] != "String" || types[1] != "" && types[1] != "Integer" || types[2] != "" && types[2] != "Integer" {
			return "", conditionOperatorError(condition.operator, types...)
		}
		return "String", nil
	default:
		return "", fmt.Errorf("%w: condition kind %q", ErrInvalidCondition, condition.kind)
	}
	return "", fmt.Errorf("%w: unsupported %s operator %q", ErrInvalidCondition, condition.kind, condition.operator)
}

func conditionAlgebraType(typeName string) string {
	switch typeName {
	case "Natural", "Positive":
		return "Integer"
	default:
		return typeName
	}
}

func canonicalConditionQualificationType(typeName string) (string, bool) {
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

func qualifiedConditionType(operator ConditionOperator) (string, bool) {
	spelling := string(operator)
	prefix := string(ConditionQualifyPrefix)
	if !strings.HasPrefix(strings.ToLower(spelling), prefix) {
		return "", false
	}
	return canonicalConditionQualificationType(spelling[len(prefix):])
}

func conditionTypeAssignable(source, target string) bool {
	return source == target
}

func conditionConditionalResultType(left, right string) (string, bool) {
	if left == "" {
		return right, true
	}
	if right == "" {
		return left, true
	}
	if left == right {
		return left, true
	}
	return "", false
}

func canonicalizeCondition(condition Condition) (canonicalCondition, error) {
	switch condition.kind {
	case conditionLiteral:
		parameters, err := gorapide.CanonicalizeParameters(map[string]any{"value": condition.literal})
		if err != nil || len(parameters) != 1 || conditionValueTypeFromCanonical(parameters[0].Value) == "" {
			return canonicalCondition{}, fmt.Errorf("%w: unsupported literal", ErrInvalidCondition)
		}
		value := parameters[0].Value
		return canonicalCondition{Kind: string(conditionLiteral), Literal: &value}, nil
	case conditionBinding:
		if condition.placeholder == nil || condition.placeholder.name == "" {
			return canonicalCondition{}, fmt.Errorf("%w: binding has no placeholder", ErrInvalidCondition)
		}
		if condition.placeholder.typ != "" && !supportedPlaceholderType(condition.placeholder.typ) {
			return canonicalCondition{}, fmt.Errorf("%w: placeholder ?%s has unsupported type %q", ErrInvalidCondition, condition.placeholder.name, condition.placeholder.typ)
		}
		return canonicalCondition{
			Kind: string(conditionBinding), Placeholder: condition.placeholder.name, Type: condition.placeholder.typ,
		}, nil
	case conditionState:
		if condition.state == "" || !supportedPlaceholderType(condition.stateType) {
			return canonicalCondition{}, fmt.Errorf("%w: state operand has invalid key %q or type %q", ErrInvalidCondition, condition.state, condition.stateType)
		}
		return canonicalCondition{Kind: string(conditionState), State: condition.state, Type: condition.stateType}, nil
	case conditionUnary, conditionBinary, conditionTernary:
		expected := 1
		if condition.kind == conditionBinary {
			expected = 2
		} else if condition.kind == conditionTernary {
			expected = 3
		}
		if len(condition.operands) != expected || !validConditionOperator(condition.kind, condition.operator) {
			return canonicalCondition{}, fmt.Errorf("%w: %s operator %q has invalid arity or spelling", ErrInvalidCondition, condition.kind, condition.operator)
		}
		operands := make([]canonicalCondition, len(condition.operands))
		for index, operand := range condition.operands {
			canonical, err := canonicalizeCondition(operand)
			if err != nil {
				return canonicalCondition{}, err
			}
			operands[index] = canonical
		}
		return canonicalCondition{Kind: string(condition.kind), Operator: condition.operator, Operands: operands}, nil
	default:
		return canonicalCondition{}, fmt.Errorf("%w: condition kind %q", ErrInvalidCondition, condition.kind)
	}
}

func validConditionOperator(kind conditionKind, operator ConditionOperator) bool {
	if kind == conditionUnary {
		if _, ok := qualifiedConditionType(operator); ok {
			return true
		}
		return operator == ConditionNegate || operator == ConditionPositive || operator == ConditionAbs ||
			operator == ConditionIntegerPred || operator == ConditionIntegerSucc || operator == ConditionIntegerFloat || operator == ConditionNot ||
			operator == ConditionCharacterCode || operator == ConditionCodeToCharacter ||
			operator == ConditionStringIsNull || operator == ConditionStringLength ||
			operator == ConditionFloatFloor
	}
	if kind == conditionTernary {
		return operator == ConditionStringSlice || operator == ConditionIf
	}
	switch operator {
	case ConditionStringAppend, ConditionStringPrepend, ConditionStringConcatenate, ConditionStringIndex,
		ConditionAdd, ConditionSubtract, ConditionMultiply, ConditionDivide,
		ConditionEqual, ConditionNotEqual, ConditionLess, ConditionLessOrEqual,
		ConditionGreater, ConditionGreaterEqual, ConditionAnd, ConditionOr, ConditionXor,
		ConditionNand, ConditionNor, ConditionAndThen, ConditionOrElse:
		return true
	default:
		return false
	}
}

func supportedPlaceholderType(typeName string) bool {
	switch typeName {
	case "Triv", "Boolean", "Integer", "Natural", "Positive", "Float", "Character", "String":
		return true
	default:
		return false
	}
}

func conditionValueTypeFromCanonical(value gorapide.CanonicalValue) string {
	switch value.Kind {
	case "triv":
		return "Triv"
	case "bool":
		return "Boolean"
	case "integer":
		return "Integer"
	case "float64":
		return "Float"
	case "character":
		return "Character"
	case "string-codes":
		return "String"
	case "string":
		return "String"
	default:
		return ""
	}
}

func conditionString(condition Condition) string {
	switch condition.kind {
	case conditionLiteral:
		return fmt.Sprint(condition.literal)
	case conditionBinding:
		if condition.placeholder == nil {
			return "?<invalid>"
		}
		return "?" + condition.placeholder.name
	case conditionState:
		return "$" + condition.state
	case conditionUnary:
		if len(condition.operands) != 1 {
			return "<invalid-unary>"
		}
		return string(condition.operator) + " " + conditionString(condition.operands[0])
	case conditionBinary:
		if len(condition.operands) != 2 {
			return "<invalid-binary>"
		}
		return "(" + conditionString(condition.operands[0]) + " " + string(condition.operator) + " " + conditionString(condition.operands[1]) + ")"
	case conditionTernary:
		if len(condition.operands) != 3 {
			return "<invalid-ternary>"
		}
		return string(condition.operator) + "(" + conditionString(condition.operands[0]) + ", " +
			conditionString(condition.operands[1]) + ", " + conditionString(condition.operands[2]) + ")"
	default:
		return "<invalid-condition>"
	}
}

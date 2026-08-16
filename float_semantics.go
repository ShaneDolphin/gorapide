package gorapide

import (
	"fmt"
	"math"
	"math/big"
)

// RapideFloat64Arithmetic evaluates one supported Float operation exactly as a
// rational number, then rounds once to the nearest IEEE-754 binary64 value.
// This explicit semantic boundary prevents host FMA, extended precision, and
// compiler expression fusion from changing a Rapide result.
func RapideFloat64Arithmetic(operator string, left, right float64) (float64, error) {
	canonicalLeft, err := canonicalFloat64(left)
	if err != nil {
		return 0, fmt.Errorf("left Float operand: %w", err)
	}
	canonicalRight, err := canonicalFloat64(right)
	if err != nil {
		return 0, fmt.Errorf("right Float operand: %w", err)
	}
	leftRational := new(big.Rat).SetFloat64(canonicalLeft)
	rightRational := new(big.Rat).SetFloat64(canonicalRight)
	result := new(big.Rat)
	switch operator {
	case "+":
		result.Add(leftRational, rightRational)
	case "-":
		result.Sub(leftRational, rightRational)
	case "*":
		result.Mul(leftRational, rightRational)
	case "/":
		if rightRational.Sign() == 0 {
			return 0, fmt.Errorf("division by zero")
		}
		result.Quo(leftRational, rightRational)
	default:
		return 0, fmt.Errorf("unsupported Float arithmetic operator %q", operator)
	}
	rounded, _ := result.Float64()
	canonical, err := canonicalFloat64(rounded)
	if err != nil {
		return 0, fmt.Errorf("Float %s result: %w", operator, err)
	}
	return canonical, nil
}

// RapideFloat64Compare evaluates supported Float equality and order over the
// exact rational values of two finite binary64 operands.
func RapideFloat64Compare(operator string, left, right float64) (bool, error) {
	canonicalLeft, err := canonicalFloat64(left)
	if err != nil {
		return false, fmt.Errorf("left Float operand: %w", err)
	}
	canonicalRight, err := canonicalFloat64(right)
	if err != nil {
		return false, fmt.Errorf("right Float operand: %w", err)
	}
	comparison := new(big.Rat).SetFloat64(canonicalLeft).Cmp(new(big.Rat).SetFloat64(canonicalRight))
	switch operator {
	case "=":
		return comparison == 0, nil
	case "/=":
		return comparison != 0, nil
	case "<":
		return comparison < 0, nil
	case "<=":
		return comparison <= 0, nil
	case ">":
		return comparison > 0, nil
	case ">=":
		return comparison >= 0, nil
	default:
		return false, fmt.Errorf("unsupported Float comparison operator %q", operator)
	}
}

// RapideFloat64Floor returns the greatest Rapide Integer not greater than one
// finite binary64 Float. The operation is performed on the exact rational
// denotation, not by host truncation, so negative fractions round downward.
func RapideFloat64Floor(value float64) (int64, error) {
	canonical, err := canonicalFloat64(value)
	if err != nil {
		return 0, fmt.Errorf("Float Floor operand: %w", err)
	}
	rational := new(big.Rat).SetFloat64(canonical)
	result := new(big.Int).Div(rational.Num(), rational.Denom())
	if !result.IsInt64() {
		return 0, fmt.Errorf("Float Floor result %s is outside the signed 64-bit Integer range", result.String())
	}
	return result.Int64(), nil
}

// RapideFloat64Abs returns the exact nonnegative binary64 denotation of one
// canonical finite Float. Clearing the sign bit is exact and also preserves the
// canonical single-zero rule.
func RapideFloat64Abs(value float64) (float64, error) {
	canonical, err := canonicalFloat64(value)
	if err != nil {
		return 0, fmt.Errorf("Float Abs operand: %w", err)
	}
	return math.Float64frombits(math.Float64bits(canonical) &^ (uint64(1) << 63)), nil
}

// RapideIntegerToFloat converts one exact signed Rapide Integer to the nearest
// IEEE-754 binary64 value, with halfway cases rounded to even. The conversion
// starts from an exact rational denotation rather than a host conversion
// instruction, so every platform observes the same rounded value.
func RapideIntegerToFloat(value int64) (float64, error) {
	rounded, _ := new(big.Rat).SetInt64(value).Float64()
	canonical, err := canonicalFloat64(rounded)
	if err != nil {
		return 0, fmt.Errorf("Integer Float result: %w", err)
	}
	return canonical, nil
}

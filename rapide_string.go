package gorapide

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// RapideString is the immutable representation used when a Stanford Rapide
// String contains Character codes that cannot be represented by a Go UTF-8
// string. Use RapideStringFromCodes to construct one. Values whose complete
// code sequence consists of Unicode scalar values normalize to an ordinary Go
// string at the canonical boundary for compatibility with the existing API.
type RapideString struct {
	codes []int64
}

// RapideStringFromCodes constructs a String as an exact sequence of Rapide
// Character codes. The input is copied. Every signed Rapide Integer is a valid
// Character code, so construction cannot fail.
func RapideStringFromCodes(codes ...int64) RapideString {
	return RapideString{codes: append([]int64(nil), codes...)}
}

// Codes returns a defensive copy of the exact Character-code sequence.
func (value RapideString) Codes() []int64 {
	return append([]int64(nil), value.codes...)
}

// String returns an unambiguous diagnostic representation without pretending
// that every Rapide Character code is a Unicode scalar value.
func (value RapideString) String() string {
	var builder strings.Builder
	builder.WriteString("String[")
	for index, code := range value.codes {
		if index != 0 {
			builder.WriteByte(',')
		}
		fmt.Fprintf(&builder, "%d", code)
	}
	builder.WriteByte(']')
	return builder.String()
}

// CanonicalRapideStringCodes returns the exact Character-code sequence of a
// canonical Go string or RapideString value. Go strings map each decoded
// Unicode scalar value to the Character having the same numeric code.
func CanonicalRapideStringCodes(value any) ([]int64, error) {
	switch value := value.(type) {
	case string:
		if !utf8.ValidString(value) {
			return nil, fmt.Errorf("%w: string is not valid UTF-8", ErrNonCanonicalValue)
		}
		result := make([]int64, 0, utf8.RuneCountInString(value))
		for _, character := range value {
			result = append(result, int64(character))
		}
		return result, nil
	case RapideString:
		return append([]int64(nil), value.codes...), nil
	default:
		return nil, fmt.Errorf("%w: %T is not a Rapide String value", ErrNonCanonicalValue, value)
	}
}

func canonicalRapideString(value RapideString) any {
	runes := make([]rune, len(value.codes))
	for index, code := range value.codes {
		if code < 0 || code > utf8.MaxRune || code >= 0xd800 && code <= 0xdfff {
			return RapideStringFromCodes(value.codes...)
		}
		runes[index] = rune(code)
	}
	return string(runes)
}

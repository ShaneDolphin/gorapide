package gorapide

import "reflect"

// CanonicalValuesEqual compares two values after normalization into the
// deterministic value algebra. Numeric representations remain distinct where
// Rapide type semantics distinguish them (for example Integer and Float).
func CanonicalValuesEqual(left, right any) (bool, error) {
	canonicalLeft, err := canonicalValueCopy(left)
	if err != nil {
		return false, err
	}
	canonicalRight, err := canonicalValueCopy(right)
	if err != nil {
		return false, err
	}
	return reflect.DeepEqual(canonicalLeft, canonicalRight), nil
}

// IsSupportedPredefinedType reports whether name is part of the initial,
// explicitly documented Rapide predefined-type compatibility subset.
func IsSupportedPredefinedType(name string) bool {
	switch name {
	case "Triv", "Boolean", "Integer", "Natural", "Positive", "Float", "Character", "String":
		return true
	default:
		return false
	}
}

// CanonicalValueMatchesPredefinedType checks a value against the initial
// predefined-type subset. The value is normalized before checking so Go integer
// widths do not leak into Rapide semantics. Unsupported types return false.
func CanonicalValueMatchesPredefinedType(value any, name string) bool {
	canonical, err := canonicalValueCopy(value)
	if err != nil {
		return false
	}
	switch name {
	case "Triv":
		_, ok := canonical.(RapideTriv)
		return ok
	case "Boolean":
		_, ok := canonical.(bool)
		return ok
	case "Integer":
		_, ok := canonical.(int64)
		return ok
	case "Natural":
		integer, ok := canonical.(int64)
		return ok && integer >= 0
	case "Positive":
		integer, ok := canonical.(int64)
		return ok && integer >= 1
	case "Float":
		_, ok := canonical.(float64)
		return ok
	case "Character":
		_, ok := canonical.(RapideCharacter)
		return ok
	case "String":
		switch canonical.(type) {
		case string, RapideString:
			return true
		default:
			return false
		}
	default:
		return false
	}
}

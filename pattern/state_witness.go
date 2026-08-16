package pattern

import (
	"encoding/hex"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/ShaneDolphin/gorapide"
)

// StateWitnessValue is one statically resolved state value in a consistent-cut
// witness. Key is model-defined (the source compiler uses component NUL name).
type StateWitnessValue struct {
	Key   string
	Value gorapide.CanonicalValue
}

// MatchStateWitness binds one complete canonical pattern match to one audited
// consistent-cut state.
type MatchStateWitness struct {
	MatchDigest   string
	WitnessDigest string
	State         []StateWitnessValue
}

// StatefulMatchResult retains every cut witness for which the guard was True.
type StatefulMatchResult struct {
	Match          MatchResult
	WitnessDigests []string
}

// StateWitnessEvaluation is one deterministic Boolean guard result for one
// complete inner match at one consistent-cut witness.
type StateWitnessEvaluation struct {
	MatchDigest   string
	WitnessDigest string
	Matched       bool
}

// RequiresStateWitnesses reports whether a closed deterministic pattern reads
// match-relative state.
func RequiresStateWitnesses(expression Pattern) (bool, error) {
	if _, err := DeterministicKey(expression); err != nil {
		return false, err
	}
	guard, ok := expression.(*wherePattern)
	return ok && conditionHasState(guard.condition), nil
}

// StateWitnessCandidates returns the complete inner matches for a top-level
// state-dependent where pattern. It is used by the deterministic engine to
// construct cut witnesses before evaluating the guard.
func StateWitnessCandidates(expression Pattern, poset PosetReader) ([]MatchResult, error) {
	guard, ok := expression.(*wherePattern)
	if !ok || !conditionHasState(guard.condition) {
		return nil, fmt.Errorf("%w: pattern does not have a top-level state guard", ErrInvalidCondition)
	}
	if _, err := DeterministicKey(expression); err != nil {
		return nil, err
	}
	return MatchWithBindings(guard.pattern, poset)
}

// MatchWithStateWitnesses evaluates a top-level closed P where B whose B reads
// state. Witness input is canonical data rather than a callback. State-free
// patterns continue through ordinary MatchWithBindings.
func MatchWithStateWitnesses(
	expression Pattern,
	poset PosetReader,
	witnesses []MatchStateWitness,
) ([]StatefulMatchResult, error) {
	matches, _, err := MatchWithStateWitnessAudit(expression, poset, witnesses)
	return matches, err
}

// MatchWithStateWitnessAudit returns both existential matches and every
// individual cut-relative Boolean evaluation used to reach that result.
func MatchWithStateWitnessAudit(
	expression Pattern,
	poset PosetReader,
	witnesses []MatchStateWitness,
) ([]StatefulMatchResult, []StateWitnessEvaluation, error) {
	guard, guarded := expression.(*wherePattern)
	if !guarded || !conditionHasState(guard.condition) {
		if len(witnesses) != 0 {
			return nil, nil, fmt.Errorf("%w: state witnesses were supplied for a state-free pattern", ErrInvalidCondition)
		}
		matches, err := MatchWithBindings(expression, poset)
		if err != nil {
			return nil, nil, err
		}
		result := make([]StatefulMatchResult, len(matches))
		for index, match := range matches {
			result[index] = StatefulMatchResult{Match: match, WitnessDigests: []string{}}
		}
		return result, []StateWitnessEvaluation{}, nil
	}
	if poset == nil {
		return nil, nil, fmt.Errorf("%w: poset is nil", ErrInvalidCondition)
	}
	if _, err := DeterministicKey(expression); err != nil {
		return nil, nil, err
	}
	inner, err := MatchWithBindings(guard.pattern, poset)
	if err != nil {
		return nil, nil, err
	}
	table := make(map[string][]MatchStateWitness)
	seenWitness := make(map[string]bool, len(witnesses))
	for _, witness := range witnesses {
		if !validStateDigest(witness.MatchDigest, false) || !validStateDigest(witness.WitnessDigest, true) {
			return nil, nil, fmt.Errorf("%w: state witness has a malformed match or cut digest", ErrInvalidCondition)
		}
		key := witness.MatchDigest + "\x00" + witness.WitnessDigest
		if seenWitness[key] {
			return nil, nil, fmt.Errorf("%w: duplicate state witness %q", ErrInvalidCondition, witness.WitnessDigest)
		}
		seenWitness[key] = true
		copy := witness
		copy.State = append([]StateWitnessValue(nil), witness.State...)
		sort.Slice(copy.State, func(i, j int) bool { return copy.State[i].Key < copy.State[j].Key })
		for index, value := range copy.State {
			if value.Key == "" || index > 0 && copy.State[index-1].Key == value.Key {
				return nil, nil, fmt.Errorf("%w: state witness has an empty or duplicate key %q", ErrInvalidCondition, value.Key)
			}
			decoded, err := gorapide.DecodeCanonicalParameters([]gorapide.CanonicalParameter{{Name: "value", Value: value.Value}})
			if err != nil || len(decoded) != 1 {
				return nil, nil, fmt.Errorf("%w: state witness key %q has a noncanonical value", ErrInvalidCondition, value.Key)
			}
			reencoded, err := gorapide.CanonicalizeParameters(decoded)
			if err != nil || len(reencoded) != 1 || !reflect.DeepEqual(reencoded[0].Value, value.Value) {
				return nil, nil, fmt.Errorf("%w: state witness key %q value is not in canonical normal form", ErrInvalidCondition, value.Key)
			}
		}
		table[copy.MatchDigest] = append(table[copy.MatchDigest], copy)
	}
	for digest := range table {
		sort.Slice(table[digest], func(i, j int) bool {
			return table[digest][i].WitnessDigest < table[digest][j].WitnessDigest
		})
	}

	used := make(map[string]bool)
	result := make([]StatefulMatchResult, 0, len(inner))
	evaluations := make([]StateWitnessEvaluation, 0, len(witnesses))
	for _, match := range inner {
		matchDigest, err := SemanticDigestMatches([]MatchResult{match})
		if err != nil {
			return nil, nil, err
		}
		available := table[matchDigest]
		if len(available) == 0 {
			return nil, nil, fmt.Errorf("%w: match %s has no consistent-cut state witnesses", ErrInvalidCondition, matchDigest)
		}
		trueWitnesses := make([]string, 0, len(available))
		for _, witness := range available {
			used[matchDigest+"\x00"+witness.WitnessDigest] = true
			state := make(map[string]any, len(witness.State))
			for _, value := range witness.State {
				decoded, err := gorapide.DecodeCanonicalParameters([]gorapide.CanonicalParameter{{Name: value.Key, Value: value.Value}})
				if err != nil {
					return nil, nil, err
				}
				state[value.Key] = decoded[value.Key]
			}
			evaluated, err := evaluateConditionWithState(guard.condition, match.Bindings, state)
			if err != nil {
				return nil, nil, err
			}
			matched, ok := evaluated.value.(bool)
			if !ok {
				return nil, nil, fmt.Errorf("%w: state guard evaluated to %s, want Boolean", ErrInvalidCondition, evaluated.typeName)
			}
			evaluations = append(evaluations, StateWitnessEvaluation{
				MatchDigest: matchDigest, WitnessDigest: witness.WitnessDigest, Matched: matched,
			})
			if matched {
				trueWitnesses = append(trueWitnesses, witness.WitnessDigest)
			}
		}
		if len(trueWitnesses) != 0 {
			result = append(result, StatefulMatchResult{Match: match, WitnessDigests: trueWitnesses})
		}
	}
	for key := range seenWitness {
		if !used[key] {
			return nil, nil, fmt.Errorf("%w: state witness %q does not correspond to an inner match", ErrInvalidCondition, key)
		}
	}
	sort.Slice(evaluations, func(i, j int) bool {
		if evaluations[i].MatchDigest != evaluations[j].MatchDigest {
			return evaluations[i].MatchDigest < evaluations[j].MatchDigest
		}
		return evaluations[i].WitnessDigest < evaluations[j].WitnessDigest
	})
	return result, evaluations, nil
}

func validStateDigest(value string, prefixed bool) bool {
	if prefixed {
		if !strings.HasPrefix(value, "sha256:") {
			return false
		}
		value = strings.TrimPrefix(value, "sha256:")
	}
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32
}

func conditionHasState(condition Condition) bool {
	if condition.kind == conditionState {
		return true
	}
	for _, operand := range condition.operands {
		if conditionHasState(operand) {
			return true
		}
	}
	return false
}

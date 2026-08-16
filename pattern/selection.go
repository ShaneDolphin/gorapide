package pattern

import (
	"errors"
	"fmt"

	"github.com/ShaneDolphin/gorapide"
)

var ErrInvalidMatchSelection = errors.New("invalid Rapide triggering-match selection")

// IsEarlierMatch implements the Rapide executable-language manual's recursive
// earlier ordering C1 << C2 over two matching computations. Shared occurrences
// and occurrences independent of the complete opposing computation are removed
// to a fixed point. C1 is then earlier exactly when every remaining event of C2
// has a causal predecessor in the remaining C1.
func IsEarlierMatch(left, right gorapide.EventSet, poset PosetReader) (bool, error) {
	if poset == nil {
		return false, fmt.Errorf("%w: poset is nil", ErrInvalidMatchSelection)
	}
	visible := make(map[gorapide.EventID]bool)
	for _, event := range poset.All() {
		if event != nil && event.ID != "" {
			visible[event.ID] = true
		}
	}
	leftIDs, err := selectionEventIDs(left, visible, "left")
	if err != nil {
		return false, err
	}
	rightIDs, err := selectionEventIDs(right, visible, "right")
	if err != nil {
		return false, err
	}
	if len(leftIDs) == 0 && len(rightIDs) == 0 {
		return false, nil
	}

	for {
		changed := false
		for eventID := range leftIDs {
			if rightIDs[eventID] {
				delete(leftIDs, eventID)
				delete(rightIDs, eventID)
				changed = true
			}
		}
		removeLeft := selectionIndependentIDs(leftIDs, rightIDs, poset)
		removeRight := selectionIndependentIDs(rightIDs, leftIDs, poset)
		for _, eventID := range removeLeft {
			delete(leftIDs, eventID)
			changed = true
		}
		for _, eventID := range removeRight {
			delete(rightIDs, eventID)
			changed = true
		}
		if !changed {
			break
		}
	}
	if len(leftIDs) == 0 || len(rightIDs) == 0 {
		return false, nil
	}
	for rightID := range rightIDs {
		preceded := false
		for leftID := range leftIDs {
			if poset.IsCausallyBefore(leftID, rightID) {
				preceded = true
				break
			}
		}
		if !preceded {
			return false, nil
		}
	}
	return true, nil
}

func selectionEventIDs(events gorapide.EventSet, visible map[gorapide.EventID]bool, side string) (map[gorapide.EventID]bool, error) {
	result := make(map[gorapide.EventID]bool, len(events))
	for index, event := range events {
		if event == nil || event.ID == "" {
			return nil, fmt.Errorf("%w: %s match event %d is nil or unidentified", ErrInvalidMatchSelection, side, index)
		}
		if !visible[event.ID] {
			return nil, fmt.Errorf("%w: %s match event %s is not visible", ErrInvalidMatchSelection, side, event.ID)
		}
		result[event.ID] = true
	}
	return result, nil
}

func selectionIndependentIDs(events, other map[gorapide.EventID]bool, poset PosetReader) []gorapide.EventID {
	result := make([]gorapide.EventID, 0)
	for eventID := range events {
		independent := true
		for otherID := range other {
			if !poset.IsCausallyIndependent(eventID, otherID) {
				independent = false
				break
			}
		}
		if independent {
			result = append(result, eventID)
		}
	}
	return result
}

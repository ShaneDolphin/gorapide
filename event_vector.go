package gorapide

// NodeID identifies a node in a distributed GoRapide cluster.
type NodeID string

// VectorClock tracks logical time across multiple nodes.
// A nil VectorClock indicates single-node mode (backward compatible).
type VectorClock map[NodeID]uint64

// Increment returns a NEW VectorClock with the given node's counter incremented
// by one. It does not mutate the original.
func (vc VectorClock) Increment(node NodeID) VectorClock {
	result := make(VectorClock, len(vc)+1)
	for k, v := range vc {
		result[k] = v
	}
	result[node] = result[node] + 1
	return result
}

// Merge returns a NEW VectorClock containing the pointwise maximum of vc and
// other. Neither receiver nor argument is mutated.
func (vc VectorClock) Merge(other VectorClock) VectorClock {
	result := make(VectorClock, len(vc)+len(other))
	for k, v := range vc {
		result[k] = v
	}
	for k, v := range other {
		if v > result[k] {
			result[k] = v
		}
	}
	return result
}

// Before reports whether vc is strictly causally before other.
// This is true iff for every node k, vc[k] <= other[k], and there exists at
// least one node k where vc[k] < other[k].
// A nil receiver is treated as having all entries equal to 0.
func (vc VectorClock) Before(other VectorClock) bool {
	// Two nil/empty vectors are equal, not "before".
	if len(vc) == 0 && len(other) == 0 {
		return false
	}

	hasStrict := false

	// Check all keys in vc.
	for k, v := range vc {
		ov := other[k]
		if v > ov {
			return false
		}
		if v < ov {
			hasStrict = true
		}
	}

	// Check keys in other that are not in vc (vc[k] is implicitly 0).
	for k, ov := range other {
		if _, exists := vc[k]; !exists {
			// vc[k] == 0
			if ov > 0 {
				hasStrict = true
			}
			// ov < 0 is impossible for uint64
		}
	}

	return hasStrict
}

// Concurrent reports whether vc and other are causally concurrent, meaning
// neither is before the other and they are not equal.
// Two nil/empty vectors are considered equal, not concurrent (returns false).
func (vc VectorClock) Concurrent(other VectorClock) bool {
	if vc.equal(other) {
		return false
	}
	return !vc.Before(other) && !other.Before(vc)
}

// equal reports whether two VectorClocks have identical entries.
func (vc VectorClock) equal(other VectorClock) bool {
	if len(vc) == 0 && len(other) == 0 {
		return true
	}
	if len(vc) != len(other) {
		return false
	}
	for k, v := range vc {
		if other[k] != v {
			return false
		}
	}
	return true
}

// Clone returns a deep copy of the VectorClock. A nil receiver returns nil.
func (vc VectorClock) Clone() VectorClock {
	if vc == nil {
		return nil
	}
	result := make(VectorClock, len(vc))
	for k, v := range vc {
		result[k] = v
	}
	return result
}

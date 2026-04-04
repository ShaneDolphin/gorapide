package gorapide

import (
	"testing"
	"time"
)

func TestVectorClockIncrement(t *testing.T) {
	vc := VectorClock{NodeID("a"): 2, NodeID("b"): 5}
	result := vc.Increment(NodeID("a"))

	// Result should have incremented value.
	if result[NodeID("a")] != 3 {
		t.Fatalf("expected a=3, got a=%d", result[NodeID("a")])
	}
	if result[NodeID("b")] != 5 {
		t.Fatalf("expected b=5, got b=%d", result[NodeID("b")])
	}

	// Original must be unchanged.
	if vc[NodeID("a")] != 2 {
		t.Fatalf("original mutated: expected a=2, got a=%d", vc[NodeID("a")])
	}
}

func TestVectorClockIncrementNew(t *testing.T) {
	var vc VectorClock // nil
	result := vc.Increment(NodeID("x"))

	if result[NodeID("x")] != 1 {
		t.Fatalf("expected x=1, got x=%d", result[NodeID("x")])
	}
	if len(result) != 1 {
		t.Fatalf("expected length 1, got %d", len(result))
	}
}

func TestVectorClockMerge(t *testing.T) {
	a := VectorClock{NodeID("n1"): 3, NodeID("n2"): 1}
	b := VectorClock{NodeID("n1"): 1, NodeID("n2"): 4, NodeID("n3"): 7}

	merged := a.Merge(b)

	expect := map[NodeID]uint64{
		NodeID("n1"): 3,
		NodeID("n2"): 4,
		NodeID("n3"): 7,
	}
	for k, v := range expect {
		if merged[k] != v {
			t.Fatalf("merged[%s] = %d, want %d", k, merged[k], v)
		}
	}
	if len(merged) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(merged))
	}
}

func TestVectorClockBefore(t *testing.T) {
	a := VectorClock{NodeID("n1"): 1, NodeID("n2"): 2}
	b := VectorClock{NodeID("n1"): 2, NodeID("n2"): 3}

	if !a.Before(b) {
		t.Fatal("expected a before b")
	}
	if b.Before(a) {
		t.Fatal("b should not be before a")
	}
}

func TestVectorClockBeforeEqual(t *testing.T) {
	a := VectorClock{NodeID("n1"): 3, NodeID("n2"): 5}
	b := VectorClock{NodeID("n1"): 3, NodeID("n2"): 5}

	if a.Before(b) {
		t.Fatal("equal vectors: Before should return false")
	}
	if b.Before(a) {
		t.Fatal("equal vectors: Before should return false (reverse)")
	}
}

func TestVectorClockConcurrent(t *testing.T) {
	a := VectorClock{NodeID("node1"): 3, NodeID("node2"): 1}
	b := VectorClock{NodeID("node1"): 1, NodeID("node2"): 3}

	if !a.Concurrent(b) {
		t.Fatal("expected a and b to be concurrent")
	}
	if !b.Concurrent(a) {
		t.Fatal("expected b and a to be concurrent (symmetric)")
	}
}

func TestVectorClockClone(t *testing.T) {
	original := VectorClock{NodeID("x"): 10, NodeID("y"): 20}
	cloned := original.Clone()

	// Values must match.
	if cloned[NodeID("x")] != 10 || cloned[NodeID("y")] != 20 {
		t.Fatal("cloned values do not match original")
	}

	// Mutating clone must not affect original.
	cloned[NodeID("x")] = 999
	if original[NodeID("x")] != 10 {
		t.Fatal("mutating clone affected original")
	}
}

func TestVectorClockCloneNil(t *testing.T) {
	var vc VectorClock
	cloned := vc.Clone()
	if cloned != nil {
		t.Fatal("clone of nil should be nil")
	}
}

func TestNilVectorClockBefore(t *testing.T) {
	var nilVC VectorClock
	other := VectorClock{NodeID("a"): 1}

	if !nilVC.Before(other) {
		t.Fatal("nil (all 0s) should be before non-nil with positive entries")
	}
}

func TestNilVectorClockConcurrent(t *testing.T) {
	var a VectorClock
	var b VectorClock

	if a.Concurrent(b) {
		t.Fatal("two nil vectors are equal, not concurrent")
	}
}

func TestClockStampBackwardCompat(t *testing.T) {
	// Verify that a zero-value ClockStamp has nil Vector.
	var cs ClockStamp
	if cs.Vector != nil {
		t.Fatal("default ClockStamp should have nil Vector")
	}

	// Verify Before() still uses Lamport (unchanged behavior).
	cs1 := ClockStamp{Lamport: 1, WallTime: time.Now()}
	cs2 := ClockStamp{Lamport: 2, WallTime: time.Now()}
	if !cs1.Before(cs2) {
		t.Fatal("ClockStamp.Before should still use Lamport ordering")
	}
	if cs2.Before(cs1) {
		t.Fatal("ClockStamp.Before should still use Lamport ordering (reverse)")
	}
}

package rapide

import (
	"reflect"
	"testing"
)

func TestSortedMissingNamesReportsLexicographicOrder(t *testing.T) {
	left := map[string]int{"shared": 1}
	right := map[string]string{"zeta": "", "alpha": "", "shared": "", "mid": ""}
	got := sortedMissingNames(left, right)
	want := []string{"alpha", "mid", "zeta"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sortedMissingNames = %v, want %v", got, want)
	}
}

func TestSortedMissingNamesNilWhenCovered(t *testing.T) {
	left := map[string]int{"a": 1, "b": 2}
	right := map[string]bool{"a": true, "b": false}
	if got := sortedMissingNames(left, right); got != nil {
		t.Fatalf("expected nil, got %v", got)
	}
}

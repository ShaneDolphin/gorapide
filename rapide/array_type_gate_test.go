package rapide

import (
	"strings"
	"testing"
)

func TestSourceArrayTypeRemainsExplicitContradictionGate(t *testing.T) {
	_, err := Parse([]byte(`type Matrix is array [Integer, Integer] of Float;`))
	if err == nil || !strings.Contains(err.Error(),
		"rich type-expression form array is outside the current closed name/application subset") {
		t.Fatalf("Array contradiction gate error = %v", err)
	}
}

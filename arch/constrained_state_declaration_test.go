package arch

import (
	"strings"
	"testing"
)

func TestStateDeclarationsEnforceConstrainedInitialMembership(t *testing.T) {
	valid := NewArchitecture("constrained-state-initials")
	component := NewComponent("c", Interface("I").Build(), nil)
	if err := component.DeclareState(
		StateReference("natural", "Natural", 0),
		StateReference("positive", "Positive", 1),
	); err != nil {
		t.Fatal(err)
	}
	if err := valid.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	if _, err := valid.DeterministicModelDigest(); err != nil {
		t.Fatalf("valid constrained state initials failed: %v", err)
	}

	for _, test := range []struct {
		typeName string
		value    any
	}{
		{typeName: "Positive", value: 0},
		{typeName: "Natural", value: -1},
	} {
		invalid := NewArchitecture("invalid-constrained-state-initial")
		component := NewComponent("c", Interface("I").Build(), nil)
		if err := component.DeclareState(StateReference("value", test.typeName, test.value)); err != nil {
			t.Fatal(err)
		}
		if err := invalid.AddComponent(component); err != nil {
			t.Fatal(err)
		}
		if _, err := invalid.DeterministicModelDigest(); err == nil ||
			!strings.Contains(err.Error(), "initial value of \"value\" does not match "+test.typeName) {
			t.Fatalf("invalid %s initial diagnostic=%v", test.typeName, err)
		}
	}
}

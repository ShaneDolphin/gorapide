package arch

import (
	"errors"
	"testing"
)

func TestGeneratorArgumentsEnforceConstrainedMembership(t *testing.T) {
	architecture := NewArchitecture("constrained-generator-arguments")
	if err := architecture.SetGeneratorArguments(
		ArchitectureArgument("positive", "Positive", 1),
		ArchitectureArgument("natural", "Natural", 0),
	); err != nil {
		t.Fatalf("valid architecture arguments failed: %v", err)
	}
	if err := architecture.SetGeneratorArguments(
		ArchitectureArgument("positive", "Positive", 0),
	); !errors.Is(err, ErrActionTypeMismatch) {
		t.Fatalf("invalid Positive architecture argument error=%v", err)
	}

	component := NewComponent("c", Interface("I").Build(), nil)
	if err := component.SetModuleMembershipWithArguments(
		"M",
		[]ModuleGeneratorArgument{
			ModuleArgument("positive", "Positive", 1),
			ModuleArgument("natural", "Natural", 0),
		},
		nil,
		nil,
	); err != nil {
		t.Fatalf("valid module arguments failed: %v", err)
	}
	invalid := NewComponent("invalid", Interface("I").Build(), nil)
	if err := invalid.SetModuleMembershipWithArguments(
		"M",
		[]ModuleGeneratorArgument{ModuleArgument("natural", "Natural", -1)},
		nil,
		nil,
	); !errors.Is(err, ErrInvalidModuleMembership) {
		t.Fatalf("invalid Natural module argument error=%v", err)
	}
}

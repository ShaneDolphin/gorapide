package arch

import (
	"fmt"
	"strings"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/constraint"
)

// ArchitectureInstanceDeclaration is one statically elaborated architecture-
// generator result. Parent is ArchitectureInterfaceID for a direct child and
// the canonical instance ID of the immediately enclosing architecture for a
// deeper descendant.
type ArchitectureInstanceDeclaration struct {
	ID                 string
	Parent             string
	Generator          string
	GeneratorArguments []ArchitectureGeneratorArgument
	ReturnInterface    *InterfaceDecl
}

// ArchitectureInstance constructs a direct deterministic child architecture.
// AddDeterministicArchitectureInstance validates and snapshots it before it
// becomes model data.
func ArchitectureInstance(
	id string,
	generator string,
	returnInterface *InterfaceDecl,
	arguments ...ArchitectureGeneratorArgument,
) ArchitectureInstanceDeclaration {
	return ArchitectureInstanceDeclaration{
		ID: id, Parent: ArchitectureInterfaceID, Generator: generator,
		GeneratorArguments: append([]ArchitectureGeneratorArgument(nil), arguments...),
		ReturnInterface:    returnInterface,
	}
}

// ArchitectureInstanceWithin constructs a deterministic architecture child in
// the scope of another static architecture instance. The reserved path segment
// in its ID cannot collide with an ordinary Rapide component identifier.
func ArchitectureInstanceWithin(
	parentID string,
	localID string,
	generator string,
	returnInterface *InterfaceDecl,
	arguments ...ArchitectureGeneratorArgument,
) ArchitectureInstanceDeclaration {
	return ArchitectureInstanceDeclaration{
		ID: DeterministicArchitectureInstanceID(parentID, localID), Parent: parentID,
		Generator:          generator,
		GeneratorArguments: append([]ArchitectureGeneratorArgument(nil), arguments...),
		ReturnInterface:    returnInterface,
	}
}

// DeterministicArchitectureInstanceID returns the collision-free global ID of
// localID in parentID. The root's direct children retain their Rapide names for
// compatibility; deeper children insert a reserved $architecture path segment.
// Invalid source identifiers are still rejected when the declaration is added.
func DeterministicArchitectureInstanceID(parentID, localID string) string {
	if parentID == "" || parentID == ArchitectureInterfaceID {
		return localID
	}
	return architectureInstanceAuditID(parentID) + "/" + ArchitectureInterfaceID + "/" + localID
}

// DeterministicArchitectureComponentID returns the collision-free global ID
// used for an ordinary component declared directly inside ownerID.
func DeterministicArchitectureComponentID(ownerID, localID string) string {
	if ownerID == "" || ownerID == ArchitectureInterfaceID {
		return localID
	}
	return architectureInstanceAuditID(ownerID) + "/" + localID
}

// AddDeterministicArchitectureInstance registers a static nested architecture
// value for guaranteed execution. It is deliberately separate from the legacy
// goroutine/channel-backed SubArchitecture adapter.
func (a *Architecture) AddDeterministicArchitectureInstance(declaration ArchitectureInstanceDeclaration) error {
	if a == nil {
		return fmt.Errorf("arch: architecture is nil")
	}
	if declaration.Parent == "" {
		declaration.Parent = ArchitectureInterfaceID
	}
	localID, validID := deterministicArchitectureInstanceLocalID(declaration.ID)
	if !validID || declaration.ID == ArchitectureInterfaceID {
		return fmt.Errorf("arch: invalid deterministic architecture instance %q", declaration.ID)
	}
	if declaration.Parent != ArchitectureInterfaceID && !validDeterministicArchitectureInstanceID(declaration.Parent) {
		return fmt.Errorf("arch: deterministic architecture instance %q has invalid parent %q", declaration.ID, declaration.Parent)
	}
	expectedID := DeterministicArchitectureInstanceID(declaration.Parent, localID)
	if declaration.ID != expectedID {
		return fmt.Errorf("arch: deterministic architecture instance %q has non-canonical ID for parent %q; want %q",
			declaration.ID, declaration.Parent, expectedID)
	}
	if !validModuleMembershipIdentifier(declaration.Generator) {
		return fmt.Errorf("arch: architecture instance %q has invalid generator %q", declaration.ID, declaration.Generator)
	}
	if declaration.ReturnInterface == nil {
		return fmt.Errorf("arch: architecture instance %q has no return interface", declaration.ID)
	}
	arguments, err := normalizeArchitectureGeneratorArguments(declaration.GeneratorArguments)
	if err != nil {
		return fmt.Errorf("arch: architecture instance %q: %w", declaration.ID, err)
	}
	declaration.GeneratorArguments = arguments
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.running {
		return fmt.Errorf("arch: architecture %q is running", a.Name)
	}
	if _, exists := a.architectureInstances[declaration.ID]; exists {
		return fmt.Errorf("arch: deterministic architecture instance %q already exists", declaration.ID)
	}
	if _, exists := a.components[declaration.ID]; exists {
		return fmt.Errorf("arch: deterministic architecture instance %q conflicts with a component", declaration.ID)
	}
	if _, exists := a.subArchitectures[declaration.ID]; exists {
		return fmt.Errorf("arch: deterministic architecture instance %q conflicts with a legacy subarchitecture", declaration.ID)
	}
	a.architectureInstances[declaration.ID] = ArchitectureInstanceDeclaration{
		ID: declaration.ID, Parent: declaration.Parent, Generator: declaration.Generator,
		GeneratorArguments: append([]ArchitectureGeneratorArgument(nil), arguments...),
		ReturnInterface:    declaration.ReturnInterface,
	}
	return nil
}

// SetDeterministicArchitectureInstanceConstraints attaches the constraint part
// evaluated over one child architecture's own visible computation. The set is
// immutable by convention, like the root architecture constraint set.
func (a *Architecture) SetDeterministicArchitectureInstanceConstraints(
	instanceID string,
	set *constraint.ConstraintSet,
) error {
	if a == nil {
		return fmt.Errorf("arch: architecture is nil")
	}
	if set == nil {
		return fmt.Errorf("arch: architecture instance %q constraint set is nil", instanceID)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.running {
		return fmt.Errorf("arch: architecture %q is running", a.Name)
	}
	if _, exists := a.architectureInstances[instanceID]; !exists {
		return fmt.Errorf("arch: deterministic architecture instance %q is not declared", instanceID)
	}
	if _, exists := a.architectureConstraints[instanceID]; exists {
		return fmt.Errorf("arch: deterministic architecture instance %q already has constraints", instanceID)
	}
	a.architectureConstraints[instanceID] = set
	return nil
}

func normalizeArchitectureGeneratorArguments(
	arguments []ArchitectureGeneratorArgument,
) ([]ArchitectureGeneratorArgument, error) {
	normalized := make([]ArchitectureGeneratorArgument, len(arguments))
	seen := make(map[string]bool, len(arguments))
	for index, argument := range arguments {
		if argument.Name == "" || argument.Type == "" {
			return nil, fmt.Errorf("architecture generator argument %d has an empty name or type", index)
		}
		key := strings.ToLower(argument.Name)
		if seen[key] {
			return nil, fmt.Errorf("duplicate architecture generator argument %q", argument.Name)
		}
		if !gorapide.IsSupportedPredefinedType(argument.Type) {
			return nil, fmt.Errorf("%w: architecture generator argument %q has type %q",
				ErrUnsupportedRapideType, argument.Name, argument.Type)
		}
		value, err := gorapide.CanonicalizeParams(map[string]any{"value": argument.Value})
		if err != nil {
			return nil, fmt.Errorf("architecture generator argument %q: %w", argument.Name, err)
		}
		if !gorapide.CanonicalValueMatchesPredefinedType(value["value"], argument.Type) {
			return nil, fmt.Errorf("%w: architecture generator argument %q does not match %s",
				ErrActionTypeMismatch, argument.Name, argument.Type)
		}
		seen[key] = true
		normalized[index] = ArchitectureGeneratorArgument{
			Name: argument.Name, Type: argument.Type, Value: value["value"],
		}
	}
	return normalized, nil
}

func architectureInstanceAuditID(instanceID string) string {
	if instanceID == "" || instanceID == ArchitectureInterfaceID {
		return ArchitectureInterfaceID
	}
	if strings.HasPrefix(instanceID, ArchitectureInterfaceID+"/") {
		return instanceID
	}
	return ArchitectureInterfaceID + "/" + instanceID
}

func deterministicArchitectureInstanceLocalID(instanceID string) (string, bool) {
	if validModuleMembershipIdentifier(instanceID) {
		return instanceID, true
	}
	prefix := ArchitectureInterfaceID + "/"
	if !strings.HasPrefix(instanceID, prefix) {
		return "", false
	}
	parts := strings.Split(strings.TrimPrefix(instanceID, prefix), "/")
	if len(parts) < 3 || len(parts)%2 == 0 {
		return "", false
	}
	for index, part := range parts {
		if index%2 == 0 {
			if !validModuleMembershipIdentifier(part) {
				return "", false
			}
			continue
		}
		if part != ArchitectureInterfaceID {
			return "", false
		}
	}
	return parts[len(parts)-1], true
}

func validDeterministicArchitectureInstanceID(instanceID string) bool {
	_, valid := deterministicArchitectureInstanceLocalID(instanceID)
	return valid
}

package studio

import "fmt"

// ArchitectureSchema is the JSON-serializable representation of an architecture
// definition used by the visual editor.
type ArchitectureSchema struct {
	Name        string              `json:"name"`
	Components  []ComponentSchema   `json:"components"`
	Connections []ConnectionSchema  `json:"connections"`
	Constraints []ConstraintSchema  `json:"constraints,omitempty"`
	Layout      map[string]Position `json:"layout,omitempty"`
}

// ComponentSchema describes a single component in the architecture.
type ComponentSchema struct {
	ID        string          `json:"id"`
	Interface InterfaceSchema `json:"interface"`
}

// InterfaceSchema describes the public interface of a component.
type InterfaceSchema struct {
	Name     string          `json:"name"`
	Actions  []ActionSchema  `json:"actions,omitempty"`
	Services []ServiceSchema `json:"services,omitempty"`
}

// ActionSchema describes an individual action on an interface.
type ActionSchema struct {
	Name   string        `json:"name"`
	Kind   string        `json:"kind"` // "in" or "out"
	Params []ParamSchema `json:"params,omitempty"`
}

// ParamSchema describes a single parameter of an action.
type ParamSchema struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// ServiceSchema groups a set of actions under a named service.
type ServiceSchema struct {
	Name    string         `json:"name"`
	Actions []ActionSchema `json:"actions"`
}

// ConnectionSchema describes a directed connection between two components.
type ConnectionSchema struct {
	From       string `json:"from"`
	To         string `json:"to"`
	Kind       string `json:"kind"`              // "basic", "pipe", "agent"
	Trigger    string `json:"trigger,omitempty"`
	ActionName string `json:"action_name"`
}

// ConstraintSchema describes a named constraint applied to the architecture.
type ConstraintSchema struct {
	Name     string         `json:"name"`
	Kind     string         `json:"kind"`
	Severity string         `json:"severity"`
	Args     map[string]any `json:"args,omitempty"`
}

// Position holds the 2-D coordinates of a component in the visual layout.
type Position struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// validConnectionKinds is the set of accepted connection kinds.
var validConnectionKinds = map[string]bool{
	"basic": true,
	"pipe":  true,
	"agent": true,
}

// Validate checks the schema for structural correctness:
//   - Name must be non-empty.
//   - Component IDs must be non-empty and unique.
//   - Connection Kind must be one of "basic", "pipe", "agent".
//   - Connection From/To must reference an existing component ID or be "*".
func (a *ArchitectureSchema) Validate() error {
	if a.Name == "" {
		return fmt.Errorf("studio: architecture name must not be empty")
	}

	// Build component ID set and check for duplicates / empty IDs.
	ids := make(map[string]bool, len(a.Components))
	for i, c := range a.Components {
		if c.ID == "" {
			return fmt.Errorf("studio: component at index %d has an empty ID", i)
		}
		if ids[c.ID] {
			return fmt.Errorf("studio: duplicate component ID %q", c.ID)
		}
		ids[c.ID] = true
	}

	// Validate each connection.
	for i, conn := range a.Connections {
		if !validConnectionKinds[conn.Kind] {
			return fmt.Errorf("studio: connection at index %d has invalid kind %q (must be \"basic\", \"pipe\", or \"agent\")", i, conn.Kind)
		}
		if conn.From != "*" && !ids[conn.From] {
			return fmt.Errorf("studio: connection at index %d references unknown source component %q", i, conn.From)
		}
		if conn.To != "*" && !ids[conn.To] {
			return fmt.Errorf("studio: connection at index %d references unknown target component %q", i, conn.To)
		}
	}

	return nil
}

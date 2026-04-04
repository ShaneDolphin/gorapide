package studio

import (
	"encoding/json"
	"testing"
)

// sampleSchema returns a fully-populated ArchitectureSchema for reuse in tests.
func sampleSchema() ArchitectureSchema {
	return ArchitectureSchema{
		Name: "test-arch",
		Components: []ComponentSchema{
			{
				ID: "alpha",
				Interface: InterfaceSchema{
					Name: "AlphaInterface",
					Actions: []ActionSchema{
						{
							Name: "send",
							Kind: "out",
							Params: []ParamSchema{
								{Name: "payload", Type: "string"},
							},
						},
					},
					Services: []ServiceSchema{
						{
							Name: "core",
							Actions: []ActionSchema{
								{Name: "ping", Kind: "in"},
							},
						},
					},
				},
			},
			{
				ID: "beta",
				Interface: InterfaceSchema{
					Name: "BetaInterface",
					Actions: []ActionSchema{
						{Name: "receive", Kind: "in"},
					},
				},
			},
		},
		Connections: []ConnectionSchema{
			{
				From:       "alpha",
				To:         "beta",
				Kind:       "basic",
				ActionName: "receive",
			},
		},
		Layout: map[string]Position{
			"alpha": {X: 100, Y: 200},
			"beta":  {X: 300, Y: 200},
		},
	}
}

// TestSchemaJSONRoundTrip verifies that marshalling and unmarshalling an
// ArchitectureSchema (including the Layout map) produces an identical value.
func TestSchemaJSONRoundTrip(t *testing.T) {
	original := sampleSchema()

	data, err := json.Marshal(&original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var restored ArchitectureSchema
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Name
	if restored.Name != original.Name {
		t.Errorf("Name: got %q, want %q", restored.Name, original.Name)
	}

	// Components count
	if len(restored.Components) != len(original.Components) {
		t.Fatalf("Components len: got %d, want %d", len(restored.Components), len(original.Components))
	}
	for i, orig := range original.Components {
		got := restored.Components[i]
		if got.ID != orig.ID {
			t.Errorf("Components[%d].ID: got %q, want %q", i, got.ID, orig.ID)
		}
		if got.Interface.Name != orig.Interface.Name {
			t.Errorf("Components[%d].Interface.Name: got %q, want %q", i, got.Interface.Name, orig.Interface.Name)
		}
		if len(got.Interface.Actions) != len(orig.Interface.Actions) {
			t.Errorf("Components[%d].Interface.Actions len: got %d, want %d", i, len(got.Interface.Actions), len(orig.Interface.Actions))
		}
		if len(got.Interface.Services) != len(orig.Interface.Services) {
			t.Errorf("Components[%d].Interface.Services len: got %d, want %d", i, len(got.Interface.Services), len(orig.Interface.Services))
		}
	}

	// Params inside first component's first action
	if len(restored.Components) > 0 && len(restored.Components[0].Interface.Actions) > 0 {
		gotParams := restored.Components[0].Interface.Actions[0].Params
		origParams := original.Components[0].Interface.Actions[0].Params
		if len(gotParams) != len(origParams) {
			t.Fatalf("Params len: got %d, want %d", len(gotParams), len(origParams))
		}
		if gotParams[0].Name != origParams[0].Name || gotParams[0].Type != origParams[0].Type {
			t.Errorf("Params[0]: got {%q,%q}, want {%q,%q}",
				gotParams[0].Name, gotParams[0].Type,
				origParams[0].Name, origParams[0].Type)
		}
	}

	// Connections
	if len(restored.Connections) != len(original.Connections) {
		t.Fatalf("Connections len: got %d, want %d", len(restored.Connections), len(original.Connections))
	}
	for i, orig := range original.Connections {
		got := restored.Connections[i]
		if got.From != orig.From || got.To != orig.To || got.Kind != orig.Kind || got.ActionName != orig.ActionName {
			t.Errorf("Connections[%d]: got {%q,%q,%q,%q}, want {%q,%q,%q,%q}",
				i, got.From, got.To, got.Kind, got.ActionName,
				orig.From, orig.To, orig.Kind, orig.ActionName)
		}
	}

	// Layout
	if len(restored.Layout) != len(original.Layout) {
		t.Fatalf("Layout len: got %d, want %d", len(restored.Layout), len(original.Layout))
	}
	for key, origPos := range original.Layout {
		gotPos, ok := restored.Layout[key]
		if !ok {
			t.Errorf("Layout[%q]: missing after round-trip", key)
			continue
		}
		if gotPos.X != origPos.X || gotPos.Y != origPos.Y {
			t.Errorf("Layout[%q]: got {%v,%v}, want {%v,%v}", key, gotPos.X, gotPos.Y, origPos.X, origPos.Y)
		}
	}
}

// TestSchemaValidateSuccess checks that a well-formed schema passes validation.
func TestSchemaValidateSuccess(t *testing.T) {
	s := sampleSchema()
	if err := s.Validate(); err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

// TestSchemaValidateEmptyName checks that an empty architecture name is rejected.
func TestSchemaValidateEmptyName(t *testing.T) {
	s := sampleSchema()
	s.Name = ""
	if err := s.Validate(); err == nil {
		t.Error("expected error for empty name, got nil")
	}
}

// TestSchemaValidateDuplicateComponentID checks that two components with the
// same ID are rejected.
func TestSchemaValidateDuplicateComponentID(t *testing.T) {
	s := sampleSchema()
	// Make both components share the same ID.
	s.Components[1].ID = s.Components[0].ID
	if err := s.Validate(); err == nil {
		t.Error("expected error for duplicate component ID, got nil")
	}
}

// TestSchemaValidateBadConnectionRef checks that a connection referencing a
// non-existent component ID (and not the wildcard "*") is rejected.
func TestSchemaValidateBadConnectionRef(t *testing.T) {
	s := sampleSchema()
	s.Connections = []ConnectionSchema{
		{
			From:       "alpha",
			To:         "nonexistent",
			Kind:       "basic",
			ActionName: "receive",
		},
	}
	if err := s.Validate(); err == nil {
		t.Error("expected error for connection to missing component, got nil")
	}
}

// TestSchemaValidateBadConnectionKind checks that an unrecognised connection
// kind is rejected.
func TestSchemaValidateBadConnectionKind(t *testing.T) {
	s := sampleSchema()
	s.Connections = []ConnectionSchema{
		{
			From:       "alpha",
			To:         "beta",
			Kind:       "unknown",
			ActionName: "receive",
		},
	}
	if err := s.Validate(); err == nil {
		t.Error("expected error for invalid connection kind, got nil")
	}
}

// TestSchemaWithConstraints verifies that constraints survive a JSON round-trip
// and that the resulting schema is still valid.
func TestSchemaWithConstraints(t *testing.T) {
	s := sampleSchema()
	s.Constraints = []ConstraintSchema{
		{
			Name:     "no-cycles",
			Kind:     "acyclic",
			Severity: "error",
			Args:     map[string]any{"strict": true},
		},
		{
			Name:     "max-latency",
			Kind:     "latency",
			Severity: "warning",
			Args:     map[string]any{"ms": float64(100)},
		},
	}

	data, err := json.Marshal(&s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var restored ArchitectureSchema
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(restored.Constraints) != len(s.Constraints) {
		t.Fatalf("Constraints len: got %d, want %d", len(restored.Constraints), len(s.Constraints))
	}
	for i, orig := range s.Constraints {
		got := restored.Constraints[i]
		if got.Name != orig.Name {
			t.Errorf("Constraints[%d].Name: got %q, want %q", i, got.Name, orig.Name)
		}
		if got.Kind != orig.Kind {
			t.Errorf("Constraints[%d].Kind: got %q, want %q", i, got.Kind, orig.Kind)
		}
		if got.Severity != orig.Severity {
			t.Errorf("Constraints[%d].Severity: got %q, want %q", i, got.Severity, orig.Severity)
		}
		if len(got.Args) != len(orig.Args) {
			t.Errorf("Constraints[%d].Args len: got %d, want %d", i, len(got.Args), len(orig.Args))
		}
	}

	if err := restored.Validate(); err != nil {
		t.Errorf("Validate on schema with constraints: %v", err)
	}
}

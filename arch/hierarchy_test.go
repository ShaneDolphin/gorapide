package arch

import (
	"testing"

	"github.com/beautiful-majestic-dolphin/gorapide"
)

func TestSubArchitectureBuilder(t *testing.T) {
	inner := NewArchitecture("inner")
	iface := Interface("InnerFace").
		InAction("Request").
		OutAction("Response").
		Build()

	sa := WrapArchitecture("sub1", inner).
		WithInterface(iface).
		Export("worker", "Result", "Response").
		Import("Request", "worker", "Task").
		Build()

	if sa.ParticipantID() != "sub1" {
		t.Errorf("ID: want sub1, got %s", sa.ParticipantID())
	}
	if sa.ParticipantInterface().Name != "InnerFace" {
		t.Errorf("Interface: want InnerFace, got %s", sa.ParticipantInterface().Name)
	}
	if len(sa.exportRules) != 1 {
		t.Errorf("exportRules: want 1, got %d", len(sa.exportRules))
	}
	if len(sa.importRules) != 1 {
		t.Errorf("importRules: want 1, got %d", len(sa.importRules))
	}
}

func TestSubArchitectureSatisfiesParticipant(t *testing.T) {
	inner := NewArchitecture("inner")
	iface := Interface("I").Build()
	sa := WrapArchitecture("sub", inner).WithInterface(iface).Build()
	var p Participant = sa
	if p.ParticipantID() != "sub" {
		t.Errorf("ParticipantID: want sub, got %s", p.ParticipantID())
	}
}

func TestExportRuleWithTransform(t *testing.T) {
	inner := NewArchitecture("inner")
	iface := Interface("I").OutAction("Out").Build()

	sa := WrapArchitecture("sub", inner).
		WithInterface(iface).
		ExportWith("worker", "Result", "Out", func(e *gorapide.Event) map[string]any {
			return map[string]any{"mapped": true}
		}).
		Build()

	if sa.exportRules[0].Transform == nil {
		t.Error("ExportWith should set transform")
	}
}

func TestImportRuleWithTransform(t *testing.T) {
	inner := NewArchitecture("inner")
	iface := Interface("I").InAction("In").Build()

	sa := WrapArchitecture("sub", inner).
		WithInterface(iface).
		ImportWith("In", "worker", "Task", func(e *gorapide.Event) map[string]any {
			return map[string]any{"mapped": true}
		}).
		Build()

	if sa.importRules[0].Transform == nil {
		t.Error("ImportWith should set transform")
	}
}

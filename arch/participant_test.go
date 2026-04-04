package arch

import (
	"testing"

	"github.com/beautiful-majestic-dolphin/gorapide"
)

func TestComponentSatisfiesParticipant(t *testing.T) {
	var p Participant = NewComponent("test", Interface("I").Build(), gorapide.NewPoset())
	if p.ParticipantID() != "test" {
		t.Errorf("ParticipantID: want test, got %s", p.ParticipantID())
	}
	if p.ParticipantInterface().Name != "I" {
		t.Errorf("ParticipantInterface: want I, got %s", p.ParticipantInterface().Name)
	}
}

func TestParticipantSend(t *testing.T) {
	c := NewComponent("test", Interface("I").Build(), gorapide.NewPoset())
	var p Participant = c
	e := gorapide.NewEvent("X", "src", nil)
	ok := p.Send(e)
	if !ok {
		t.Error("Send should succeed")
	}
}

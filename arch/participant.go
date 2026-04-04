package arch

import (
	"context"

	"github.com/ShaneDolphin/gorapide"
)

// Participant is the common interface for anything that can participate
// in an architecture: plain components and nested sub-architectures.
type Participant interface {
	ParticipantID() string
	ParticipantInterface() *InterfaceDecl
	Send(e *gorapide.Event) bool
	Start(ctx context.Context)
	Stop()
	Wait()
}

// ParticipantID returns the component's ID. Satisfies Participant.
func (c *Component) ParticipantID() string {
	return c.ID
}

// ParticipantInterface returns the component's interface declaration. Satisfies Participant.
func (c *Component) ParticipantInterface() *InterfaceDecl {
	return c.Interface
}

// Compile-time assertion that *Component satisfies Participant.
var _ Participant = (*Component)(nil)

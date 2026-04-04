package studio

import (
	"fmt"

	"github.com/beautiful-majestic-dolphin/gorapide"
	"github.com/beautiful-majestic-dolphin/gorapide/arch"
	"github.com/beautiful-majestic-dolphin/gorapide/pattern"
)

// Reconstruct converts an ArchitectureSchema into a live arch.Architecture.
func Reconstruct(schema *ArchitectureSchema) (*arch.Architecture, error) {
	if err := schema.Validate(); err != nil {
		return nil, fmt.Errorf("studio.Reconstruct: %w", err)
	}
	a := arch.NewArchitecture(schema.Name)
	// Build components from schema
	for _, cs := range schema.Components {
		iface := buildInterface(cs.Interface)
		comp := arch.NewComponent(cs.ID, iface, nil)
		if err := a.AddComponent(comp); err != nil {
			return nil, fmt.Errorf("studio.Reconstruct: %w", err)
		}
	}
	// Build connections from schema
	for i, cs := range schema.Connections {
		conn, err := buildConnection(cs)
		if err != nil {
			return nil, fmt.Errorf("studio.Reconstruct: connection %d: %w", i, err)
		}
		if err := a.AddConnection(conn); err != nil {
			return nil, fmt.Errorf("studio.Reconstruct: %w", err)
		}
	}
	return a, nil
}

// ReconstructWithObserver is like Reconstruct but registers a global event observer.
func ReconstructWithObserver(schema *ArchitectureSchema, observer func(*gorapide.Event)) (*arch.Architecture, error) {
	if err := schema.Validate(); err != nil {
		return nil, fmt.Errorf("studio.Reconstruct: %w", err)
	}
	a := arch.NewArchitecture(schema.Name, arch.WithObserver(observer))
	for _, cs := range schema.Components {
		iface := buildInterface(cs.Interface)
		comp := arch.NewComponent(cs.ID, iface, nil)
		if err := a.AddComponent(comp); err != nil {
			return nil, fmt.Errorf("studio.Reconstruct: %w", err)
		}
	}
	for i, cs := range schema.Connections {
		conn, err := buildConnection(cs)
		if err != nil {
			return nil, fmt.Errorf("studio.Reconstruct: connection %d: %w", i, err)
		}
		if err := a.AddConnection(conn); err != nil {
			return nil, fmt.Errorf("studio.Reconstruct: %w", err)
		}
	}
	return a, nil
}

// buildInterface converts an InterfaceSchema into an *arch.InterfaceDecl.
func buildInterface(is InterfaceSchema) *arch.InterfaceDecl {
	b := arch.Interface(is.Name)
	for _, as := range is.Actions {
		params := buildParams(as.Params)
		if as.Kind == "in" {
			b = b.InAction(as.Name, params...)
		} else {
			b = b.OutAction(as.Name, params...)
		}
	}
	for _, svc := range is.Services {
		svc := svc // capture loop variable
		b = b.Service(svc.Name, func(sb *arch.ServiceBuilder) {
			for _, as := range svc.Actions {
				params := buildParams(as.Params)
				if as.Kind == "in" {
					sb.InAction(as.Name, params...)
				} else {
					sb.OutAction(as.Name, params...)
				}
			}
		})
	}
	return b.Build()
}

// buildParams converts a slice of ParamSchema into a slice of arch.ParamDecl.
func buildParams(ps []ParamSchema) []arch.ParamDecl {
	decls := make([]arch.ParamDecl, len(ps))
	for i, p := range ps {
		decls[i] = arch.P(p.Name, p.Type)
	}
	return decls
}

// buildConnection converts a ConnectionSchema into an *arch.Connection.
func buildConnection(cs ConnectionSchema) (*arch.Connection, error) {
	b := arch.Connect(cs.From, cs.To)

	if cs.Trigger != "" {
		b = b.On(pattern.MatchEvent(cs.Trigger))
	}

	switch cs.Kind {
	case "basic":
		// default kind — no extra call needed
	case "pipe":
		b = b.Pipe()
	case "agent":
		b = b.Agent()
	default:
		return nil, fmt.Errorf("unknown connection kind %q", cs.Kind)
	}

	b = b.Send(cs.ActionName)
	return b.Build(), nil
}

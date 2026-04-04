package arch

import (
	"fmt"
	"strings"

	"github.com/beautiful-majestic-dolphin/gorapide"
)

// EventTranslation defines how a single source action maps to a target action.
// Guard (nil = always match) filters which events qualify for translation.
// Transform (nil = copy params) produces the parameter map for the target event.
type EventTranslation struct {
	SourceAction string
	TargetAction string
	Transform    func(*gorapide.Event) map[string]any
	Guard        func(*gorapide.Event) bool
}

// Map defines a translation between two interface vocabularies.
// It implements gorapide.MapTarget, allowing a source event to be translated
// into zero or more target events according to its Translations rules.
type Map struct {
	Name            string
	SourceInterface *InterfaceDecl
	TargetInterface *InterfaceDecl
	Translations    []EventTranslation
}

// MapEvent translates a source event into zero or more target events.
// For each translation rule: the source name must match, the guard (if any)
// must pass, and the transform (or param copy) produces the target params.
func (m *Map) MapEvent(source *gorapide.Event) ([]*gorapide.Event, error) {
	var results []*gorapide.Event
	for _, tr := range m.Translations {
		if source.Name != tr.SourceAction {
			continue
		}
		if tr.Guard != nil && !tr.Guard(source) {
			continue
		}
		var params map[string]any
		if tr.Transform != nil {
			params = tr.Transform(source)
		} else {
			params = copyParams(source)
		}
		results = append(results, gorapide.NewEvent(tr.TargetAction, "", params))
	}
	return results, nil
}

// Validate checks that all SourceAction names exist in SourceInterface and
// all TargetAction names exist in TargetInterface (including Service actions).
// If an interface is nil, its check is skipped.
func (m *Map) Validate() error {
	var errs []string

	if m.SourceInterface != nil {
		srcActions := collectActionNames(m.SourceInterface)
		for _, tr := range m.Translations {
			if !srcActions[tr.SourceAction] {
				errs = append(errs, fmt.Sprintf("source action %q not found in interface %q", tr.SourceAction, m.SourceInterface.Name))
			}
		}
	}

	if m.TargetInterface != nil {
		tgtActions := collectActionNames(m.TargetInterface)
		for _, tr := range m.Translations {
			if !tgtActions[tr.TargetAction] {
				errs = append(errs, fmt.Sprintf("target action %q not found in interface %q", tr.TargetAction, m.TargetInterface.Name))
			}
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("map %q validation: %s", m.Name, strings.Join(errs, "; "))
	}
	return nil
}

// String returns a human-readable description of the Map.
func (m *Map) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Map(%s)", m.Name)
	if m.SourceInterface != nil {
		fmt.Fprintf(&b, " from=%s", m.SourceInterface.Name)
	}
	if m.TargetInterface != nil {
		fmt.Fprintf(&b, " to=%s", m.TargetInterface.Name)
	}
	fmt.Fprintf(&b, " translations=%d", len(m.Translations))
	for _, tr := range m.Translations {
		fmt.Fprintf(&b, " [%s->%s]", tr.SourceAction, tr.TargetAction)
	}
	return b.String()
}

// ---------------------------------------------------------------------------
// MapBuilder — fluent API for constructing a Map
// ---------------------------------------------------------------------------

// MapBuilder builds a Map using a fluent API.
type MapBuilder struct {
	name        string
	source      *InterfaceDecl
	target      *InterfaceDecl
	translations []EventTranslation
}

// NewMap starts building a new Map with the given name.
func NewMap(name string) *MapBuilder {
	return &MapBuilder{name: name}
}

// From sets the source interface for the map.
func (b *MapBuilder) From(iface *InterfaceDecl) *MapBuilder {
	b.source = iface
	return b
}

// To sets the target interface for the map.
func (b *MapBuilder) To(iface *InterfaceDecl) *MapBuilder {
	b.target = iface
	return b
}

// Translate adds a simple translation from sourceAction to targetAction
// with no guard and no transform (params are copied).
func (b *MapBuilder) Translate(sourceAction, targetAction string) *MapBuilder {
	b.translations = append(b.translations, EventTranslation{
		SourceAction: sourceAction,
		TargetAction: targetAction,
	})
	return b
}

// TranslateWith adds a translation with a custom transform function.
func (b *MapBuilder) TranslateWith(sourceAction, targetAction string, transform func(*gorapide.Event) map[string]any) *MapBuilder {
	b.translations = append(b.translations, EventTranslation{
		SourceAction: sourceAction,
		TargetAction: targetAction,
		Transform:    transform,
	})
	return b
}

// TranslateGuarded adds a translation with a guard and optional transform.
func (b *MapBuilder) TranslateGuarded(sourceAction, targetAction string, guard func(*gorapide.Event) bool, transform func(*gorapide.Event) map[string]any) *MapBuilder {
	b.translations = append(b.translations, EventTranslation{
		SourceAction: sourceAction,
		TargetAction: targetAction,
		Guard:        guard,
		Transform:    transform,
	})
	return b
}

// Build finalizes and returns the Map.
func (b *MapBuilder) Build() *Map {
	return &Map{
		Name:            b.name,
		SourceInterface: b.source,
		TargetInterface: b.target,
		Translations:    b.translations,
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// copyParams creates a deep copy of the event's Params map.
func copyParams(e *gorapide.Event) map[string]any {
	if e.Params == nil {
		return nil
	}
	cp := make(map[string]any, len(e.Params))
	for k, v := range e.Params {
		cp[k] = v
	}
	return cp
}

// collectActionNames returns a set of all action names declared on an
// InterfaceDecl, including those within Services.
func collectActionNames(iface *InterfaceDecl) map[string]bool {
	names := make(map[string]bool)
	for _, a := range iface.Actions {
		names[a.Name] = true
	}
	for _, svc := range iface.Services {
		for _, a := range svc.Actions {
			names[a.Name] = true
		}
	}
	return names
}

// Compile-time assertion that *Map satisfies gorapide.MapTarget.
var _ gorapide.MapTarget = (*Map)(nil)

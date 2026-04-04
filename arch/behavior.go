package arch

import (
	"fmt"
	"sort"
	"strings"

	"github.com/beautiful-majestic-dolphin/gorapide"
	"github.com/beautiful-majestic-dolphin/gorapide/pattern"
)

// BehaviorRule defines a reactive transition rule: when the trigger pattern
// matches against observed events, execute the action.
type BehaviorRule struct {
	Name    string
	Trigger pattern.Pattern
	Action  func(ctx BehaviorContext)
	Once    bool // if true, fire only once then deactivate

	active    bool
	firedKeys map[string]bool // tracks which match keys have already fired
}

// BehaviorContext is passed to behavior actions when a trigger pattern matches.
type BehaviorContext struct {
	Component *Component
	Matched   gorapide.EventSet
	Poset     *gorapide.Poset
}

// Emit creates a new event sourced from the component, caused by ALL
// events in the Matched set.
func (bc BehaviorContext) Emit(name string, params map[string]any) *gorapide.Event {
	causes := make([]gorapide.EventID, len(bc.Matched))
	for i, e := range bc.Matched {
		causes[i] = e.ID
	}
	event, err := bc.Component.Emit(name, params, causes...)
	if err != nil {
		panic(fmt.Sprintf("arch.BehaviorContext.Emit: %v", err))
	}
	return event
}

// EmitCausedBy creates a new event with explicit causal parents instead
// of all matched events.
func (bc BehaviorContext) EmitCausedBy(name string, params map[string]any, causes ...gorapide.EventID) *gorapide.Event {
	event, err := bc.Component.Emit(name, params, causes...)
	if err != nil {
		panic(fmt.Sprintf("arch.BehaviorContext.EmitCausedBy: %v", err))
	}
	return event
}

// ParamFrom finds the first matched event with the given name and returns
// the value of paramKey. Returns nil if no such event or param exists.
func (bc BehaviorContext) ParamFrom(eventName, paramKey string) any {
	for _, e := range bc.Matched {
		if e.Name == eventName {
			v, ok := e.Param(paramKey)
			if ok {
				return v
			}
		}
	}
	return nil
}

// --- Component behavior registration ---

// OnEvent registers a behavior that triggers when an event with the given
// name is observed. Shorthand for OnPattern with MatchEvent(name).
func (c *Component) OnEvent(name string, action func(BehaviorContext)) *Component {
	return c.OnPattern(name, pattern.MatchEvent(name), action)
}

// OnPattern registers a behavior rule with the given trigger pattern.
func (c *Component) OnPattern(name string, trigger pattern.Pattern, action func(BehaviorContext)) *Component {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.rules = append(c.rules, &BehaviorRule{
		Name:      name,
		Trigger:   trigger,
		Action:    action,
		active:    true,
		firedKeys: make(map[string]bool),
	})
	return c
}

// OnPatternOnce registers a behavior rule that fires only once.
func (c *Component) OnPatternOnce(name string, trigger pattern.Pattern, action func(BehaviorContext)) *Component {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.rules = append(c.rules, &BehaviorRule{
		Name:      name,
		Trigger:   trigger,
		Action:    action,
		Once:      true,
		active:    true,
		firedKeys: make(map[string]bool),
	})
	return c
}

// observe adds an event to the observation buffer and checks behavior rules.
// Called from the component's run loop (single goroutine).
func (c *Component) observe(e *gorapide.Event) {
	// Deduplicate: each event observed exactly once.
	for _, obs := range c.observed {
		if obs.ID == e.ID {
			return
		}
	}
	c.observed = append(c.observed, e)

	// Snapshot rules under lock.
	c.mu.Lock()
	rules := make([]*BehaviorRule, len(c.rules))
	copy(rules, c.rules)
	c.mu.Unlock()

	view := &observationView{
		observed: c.observed,
		poset:    c.poset,
	}

	for _, rule := range rules {
		if !rule.active {
			continue
		}
		matches := rule.Trigger.Match(view)
		for _, matched := range matches {
			key := matchKey(matched)
			if rule.firedKeys[key] {
				continue
			}
			rule.firedKeys[key] = true
			rule.Action(BehaviorContext{
				Component: c,
				Matched:   matched,
				Poset:     c.poset,
			})
			if rule.Once {
				rule.active = false
				break
			}
		}
	}
}

// matchKey returns a canonical string key for an EventSet, used to
// deduplicate firings.
func matchKey(es gorapide.EventSet) string {
	ids := make([]string, len(es))
	for i, e := range es {
		ids[i] = string(e.ID)
	}
	sort.Strings(ids)
	return strings.Join(ids, ",")
}

// --- observationView implements pattern.PosetReader ---

// observationView wraps a poset but scopes All/ByName/Len to the
// component's observed events. Causal queries delegate to the real poset.
type observationView struct {
	observed gorapide.EventSet
	poset    *gorapide.Poset
}

func (v *observationView) All() gorapide.EventSet {
	return v.observed
}

func (v *observationView) ByName(name string) gorapide.EventSet {
	var result gorapide.EventSet
	for _, e := range v.observed {
		if e.Name == name {
			result = append(result, e)
		}
	}
	return result
}

func (v *observationView) Len() int {
	return len(v.observed)
}

func (v *observationView) IsCausallyBefore(a, b gorapide.EventID) bool {
	return v.poset.IsCausallyBefore(a, b)
}

func (v *observationView) IsCausallyIndependent(a, b gorapide.EventID) bool {
	return v.poset.IsCausallyIndependent(a, b)
}

func (v *observationView) CausalAncestors(id gorapide.EventID) gorapide.EventSet {
	return v.poset.CausalAncestors(id)
}

func (v *observationView) CausalDescendants(id gorapide.EventID) gorapide.EventSet {
	return v.poset.CausalDescendants(id)
}

func (v *observationView) CausalChain(from, to gorapide.EventID) (gorapide.EventSet, error) {
	return v.poset.CausalChain(from, to)
}

func (v *observationView) Roots() gorapide.EventSet {
	return v.poset.Roots()
}

func (v *observationView) Leaves() gorapide.EventSet {
	return v.poset.Leaves()
}

func (v *observationView) TopologicalSort() []*gorapide.Event {
	return v.poset.TopologicalSort()
}

package arch

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/pattern"
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
//
// Deprecated: callback behavior is outside the deterministic trusted core.
// Use AddDeclarativeRule or AddDeclarativeProcess with closed model data.
func (c *Component) OnEvent(name string, action func(BehaviorContext)) *Component {
	return c.OnPattern(name, pattern.MatchEvent(name), action)
}

// OnPattern registers a behavior rule with the given trigger pattern.
//
// Deprecated: callback behavior is outside the deterministic trusted core.
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
//
// Deprecated: callback behavior is outside the deterministic trusted core.
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

	for _, rule := range rules {
		if !rule.active {
			continue
		}
		available, err := c.consumed.Available(rule.Name, c.observed)
		if err != nil {
			panic(fmt.Sprintf("arch.Component.observe: %v", err))
		}
		ruleView := newObservationView(available, c.poset)
		matches := rule.Trigger.Match(ruleView)
		for _, matched := range matches {
			key := matchKey(matched)
			if rule.firedKeys[key] {
				continue
			}
			if err := c.consumed.Consume(rule.Name, matched); err != nil {
				if errors.Is(err, ErrEventConsumedByRule) {
					continue
				}
				panic(fmt.Sprintf("arch.Component.observe: %v", err))
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

// observationView applies Rapide visibility projection to a component's
// observed event pool.
type observationView struct {
	*pattern.Projection
	runtime *functionExecutionRuntime
}

func newObservationView(
	observed gorapide.EventSet,
	poset *gorapide.Poset,
	runtimes ...*functionExecutionRuntime,
) *observationView {
	// Legacy Component.Send permits an observed event that was not first added
	// to the shared poset. Keep that compatibility path matchable while causal
	// queries continue to come only from the actual poset. Guaranteed execution
	// always supplies events already present in the source computation.
	source := &observationProjectionSource{Poset: poset, extra: observed}
	projection, err := pattern.NewProjection(source, observed)
	if err != nil {
		panic(fmt.Sprintf("arch.newObservationView: %v", err))
	}
	view := &observationView{Projection: projection}
	if len(runtimes) != 0 {
		view.runtime = runtimes[0]
	}
	return view
}

func (view *observationView) ModuleValueForSource(source, expectedType string) (gorapide.RapideModuleValue, bool) {
	if view == nil || view.runtime == nil {
		return gorapide.RapideModuleValue{}, false
	}
	component := view.runtime.components[source]
	if component == nil || component.Interface == nil || !strings.EqualFold(component.Interface.Name, expectedType) {
		return gorapide.RapideModuleValue{}, false
	}
	module, exists := view.runtime.modules[source]
	return module, exists && module.Identity() != ""
}

type observationProjectionSource struct {
	*gorapide.Poset
	extra gorapide.EventSet
}

func (source *observationProjectionSource) All() gorapide.EventSet {
	result := source.Poset.All()
	seen := make(map[gorapide.EventID]bool, len(result)+len(source.extra))
	for _, event := range result {
		seen[event.ID] = true
	}
	for _, event := range source.extra {
		if event != nil && !seen[event.ID] {
			seen[event.ID] = true
			result = append(result, event)
		}
	}
	return result
}

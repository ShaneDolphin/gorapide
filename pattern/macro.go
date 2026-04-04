package pattern

import (
	"fmt"
	"sync"
)

// PatternMacro is a reusable, parameterized pattern template.
type PatternMacro struct {
	Name  string
	Desc  string
	Build func(args ...any) Pattern
}

// MacroRegistry is a named collection of pattern macros.
// It is safe for concurrent use.
type MacroRegistry struct {
	mu     sync.RWMutex
	macros map[string]*PatternMacro
}

// NewMacroRegistry creates an empty MacroRegistry with built-in macros
// pre-registered.
func NewMacroRegistry() *MacroRegistry {
	r := &MacroRegistry{
		macros: make(map[string]*PatternMacro),
	}
	r.registerBuiltins()
	return r
}

// Register adds a named pattern macro to the registry.
func (r *MacroRegistry) Register(name string, desc string, build func(args ...any) Pattern) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.macros[name] = &PatternMacro{
		Name:  name,
		Desc:  desc,
		Build: build,
	}
}

// Get retrieves a macro by name.
func (r *MacroRegistry) Get(name string) (*PatternMacro, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	m, ok := r.macros[name]
	return m, ok
}

// Apply looks up a macro by name and builds a pattern with the given args.
func (r *MacroRegistry) Apply(name string, args ...any) (Pattern, error) {
	r.mu.RLock()
	m, ok := r.macros[name]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("macro %q not found", name)
	}
	return m.Build(args...), nil
}

// Names returns all registered macro names.
func (r *MacroRegistry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.macros))
	for name := range r.macros {
		names = append(names, name)
	}
	return names
}

func (r *MacroRegistry) registerBuiltins() {
	r.macros["causal_chain"] = &PatternMacro{
		Name: "causal_chain",
		Desc: "Creates a Seq pattern from a list of event names. Args: event name strings.",
		Build: func(args ...any) Pattern {
			if len(args) < 2 {
				panic("causal_chain macro requires at least 2 event names")
			}
			patterns := make([]Pattern, len(args))
			for i, arg := range args {
				name, ok := arg.(string)
				if !ok {
					panic(fmt.Sprintf("causal_chain: argument %d must be a string, got %T", i, arg))
				}
				patterns[i] = MatchEvent(name)
			}
			return Seq(patterns...)
		},
	}

	r.macros["fan_in"] = &PatternMacro{
		Name: "fan_in",
		Desc: "One target event caused by multiple source events. Args: target name, then source names.",
		Build: func(args ...any) Pattern {
			if len(args) < 2 {
				panic("fan_in macro requires at least 2 arguments (target + sources)")
			}
			target, ok := args[0].(string)
			if !ok {
				panic(fmt.Sprintf("fan_in: target (arg 0) must be a string, got %T", args[0]))
			}
			sources := make([]Pattern, len(args)-1)
			for i, arg := range args[1:] {
				name, ok := arg.(string)
				if !ok {
					panic(fmt.Sprintf("fan_in: source (arg %d) must be a string, got %T", i+1, arg))
				}
				sources[i] = MatchEvent(name)
			}
			// Join all sources, then Seq into target.
			var joined Pattern
			if len(sources) == 1 {
				joined = sources[0]
			} else {
				joined = Join(sources[0], sources[1])
				for _, s := range sources[2:] {
					joined = Join(joined, s)
				}
			}
			return Seq(joined, MatchEvent(target))
		},
	}

	r.macros["fan_out"] = &PatternMacro{
		Name: "fan_out",
		Desc: "One source event causing multiple independent targets. Args: source name, then target names.",
		Build: func(args ...any) Pattern {
			if len(args) < 2 {
				panic("fan_out macro requires at least 2 arguments (source + targets)")
			}
			source, ok := args[0].(string)
			if !ok {
				panic(fmt.Sprintf("fan_out: source (arg 0) must be a string, got %T", args[0]))
			}
			targets := make([]Pattern, len(args)-1)
			for i, arg := range args[1:] {
				name, ok := arg.(string)
				if !ok {
					panic(fmt.Sprintf("fan_out: target (arg %d) must be a string, got %T", i+1, arg))
				}
				targets[i] = MatchEvent(name)
			}
			var indep Pattern
			if len(targets) == 1 {
				indep = targets[0]
			} else {
				indep = Independent(targets[0], targets[1])
				for _, t := range targets[2:] {
					indep = Independent(indep, t)
				}
			}
			return Seq(MatchEvent(source), indep)
		},
	}
}

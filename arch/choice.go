package arch

import (
	"encoding/json"
	"fmt"
	"sort"
)

// ChoiceResolution is the complete witness for one language-permitted choice.
// Options are stable semantic alternative IDs, not declaration positions.
type ChoiceResolution struct {
	Point     string   `json:"point"`
	Domain    string   `json:"domain"`
	Options   []string `json:"options"`
	Selected  string   `json:"selected"`
	Scheduled bool     `json:"scheduled"`
}

// Decision returns the explicit schedule entry that reproduces this choice.
func (resolution ChoiceResolution) Decision() ChoiceDecision {
	return ChoiceDecision{Point: resolution.Point, Selection: resolution.Selected}
}

type choiceResolver struct {
	scheduled   map[string]string
	used        map[string]bool
	nextOrdinal uint64
	resolutions []ChoiceResolution
}

func newChoiceResolver(schedule []ChoiceDecision) *choiceResolver {
	scheduled := make(map[string]string, len(schedule))
	for _, decision := range schedule {
		scheduled[decision.Point] = decision.Selection
	}
	return &choiceResolver{scheduled: scheduled, used: make(map[string]bool, len(schedule))}
}

func (resolver *choiceResolver) resolve(domain string, options []string) (string, error) {
	if len(options) == 0 {
		return "", fmt.Errorf("arch.choiceResolver.resolve: choice has no alternatives")
	}
	canonical := append([]string(nil), options...)
	sort.Strings(canonical)
	for i := 1; i < len(canonical); i++ {
		if canonical[i-1] == canonical[i] {
			return "", fmt.Errorf("arch.choiceResolver.resolve: duplicate alternative %q", canonical[i])
		}
	}
	if len(canonical) == 1 {
		return canonical[0], nil
	}

	resolver.nextOrdinal++
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	point := fmt.Sprintf("%s#%d@%s", domain, resolver.nextOrdinal, digestBytes(encoded))
	selected := canonical[0]
	scheduled := false
	if requested, ok := resolver.scheduled[point]; ok {
		scheduled = true
		selected = requested
		found := false
		for _, option := range canonical {
			if option == requested {
				found = true
				break
			}
		}
		if !found {
			return "", fmt.Errorf("%w: point %q selects unavailable alternative %q", ErrChoiceScheduleMismatch, point, requested)
		}
		resolver.used[point] = true
	}
	resolver.resolutions = append(resolver.resolutions, ChoiceResolution{
		Point: point, Domain: domain, Options: canonical,
		Selected: selected, Scheduled: scheduled,
	})
	return selected, nil
}

func (resolver *choiceResolver) finish() error {
	unused := make([]string, 0)
	for point := range resolver.scheduled {
		if !resolver.used[point] {
			unused = append(unused, point)
		}
	}
	if len(unused) == 0 {
		return nil
	}
	sort.Strings(unused)
	return fmt.Errorf("%w: unused choice points %v", ErrChoiceScheduleMismatch, unused)
}

func (resolver *choiceResolver) canonicalResolutions() []ChoiceResolution {
	result := make([]ChoiceResolution, len(resolver.resolutions))
	for i, resolution := range resolver.resolutions {
		result[i] = resolution
		result[i].Options = append([]string(nil), resolution.Options...)
	}
	return result
}

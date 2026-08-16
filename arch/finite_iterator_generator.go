package arch

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/ShaneDolphin/gorapide"
)

// ErrInvalidFiniteIteratorGenerator identifies malformed closed Iterator(T)
// module-generator model data.
var ErrInvalidFiniteIteratorGenerator = errors.New("invalid deterministic finite Iterator(T) generator")

// FiniteIteratorGenerator is a zero-parameter closed module generator whose
// every evaluation allocates a fresh Iterator(T) module over the same immutable
// ordered model domain.
type FiniteIteratorGenerator struct {
	name     string
	itemType gorapide.RapideType
	items    []gorapide.CanonicalValue
}

// NewFiniteIteratorGenerator constructs a deterministic zero-parameter module
// generator. The first kernel supports predefined item types and at most 256
// canonical items; it does not substitute a host callback for a Rapide body.
func NewFiniteIteratorGenerator(
	name string,
	itemType gorapide.RapideType,
	items ...any,
) (*FiniteIteratorGenerator, error) {
	if !validModuleMembershipIdentifier(name) {
		return nil, fmt.Errorf("%w: invalid generator name %q", ErrInvalidFiniteIteratorGenerator, name)
	}
	_, encoded, err := encodeFiniteIteratorItems(itemType, items)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidFiniteIteratorGenerator, err)
	}
	return &FiniteIteratorGenerator{
		name: strings.ToLower(name), itemType: itemType,
		items: append([]gorapide.CanonicalValue(nil), encoded...),
	}, nil
}

// Name returns the compatibility-profile normalized generator name.
func (generator *FiniteIteratorGenerator) Name() string {
	if generator == nil {
		return ""
	}
	return generator.name
}

// ItemType returns the exact immutable T supplied to Iterator(T).
func (generator *FiniteIteratorGenerator) ItemType() gorapide.RapideType {
	if generator == nil {
		return gorapide.RapideType{}
	}
	return generator.itemType
}

// Items returns defensive decoded copies in generator-defined semantic order.
func (generator *FiniteIteratorGenerator) Items() ([]any, error) {
	if generator == nil {
		return nil, fmt.Errorf("%w: generator is nil", ErrInvalidFiniteIteratorGenerator)
	}
	result := make([]any, len(generator.items))
	for index, item := range generator.items {
		decoded, err := gorapide.DecodeCanonicalValue(item)
		if err != nil {
			return nil, fmt.Errorf("%w: item %d: %v", ErrInvalidFiniteIteratorGenerator, index, err)
		}
		result[index] = decoded
	}
	return result, nil
}

func normalizeFiniteIteratorGenerator(
	generator *FiniteIteratorGenerator,
) (*FiniteIteratorGenerator, error) {
	if generator == nil {
		return nil, fmt.Errorf("%w: generator is nil", ErrInvalidFiniteIteratorGenerator)
	}
	items, err := generator.Items()
	if err != nil {
		return nil, err
	}
	return NewFiniteIteratorGenerator(generator.name, generator.itemType, items...)
}

func (generator *FiniteIteratorGenerator) instantiate(
	module gorapide.RapideModuleValue,
) (*finiteRangeIterator, error) {
	normalized, err := normalizeFiniteIteratorGenerator(generator)
	if err != nil {
		return nil, err
	}
	items, err := normalized.Items()
	if err != nil {
		return nil, err
	}
	typeName, _ := normalized.itemType.PredefinedName()
	return &finiteRangeIterator{module: module, itemType: typeName, items: items}, nil
}

type canonicalFiniteIteratorGenerator struct {
	Name     string                    `json:"name"`
	ItemType json.RawMessage           `json:"item_type"`
	Items    []gorapide.CanonicalValue `json:"items"`
}

func canonicalizeFiniteIteratorGenerators(
	generators map[string]*FiniteIteratorGenerator,
) (map[string]*FiniteIteratorGenerator, []canonicalFiniteIteratorGenerator, error) {
	names := make([]string, 0, len(generators))
	for name := range generators {
		names = append(names, name)
	}
	sort.Strings(names)
	normalized := make(map[string]*FiniteIteratorGenerator, len(generators))
	canonical := make([]canonicalFiniteIteratorGenerator, 0, len(generators))
	for _, name := range names {
		generator, err := normalizeFiniteIteratorGenerator(generators[name])
		if err != nil {
			return nil, nil, err
		}
		if generator.name != name {
			return nil, nil, fmt.Errorf("%w: registry key %q does not match generator %q",
				ErrInvalidFiniteIteratorGenerator, name, generator.name)
		}
		itemType, err := generator.itemType.MarshalCanonical()
		if err != nil {
			return nil, nil, fmt.Errorf("%w: generator %s item type: %v",
				ErrInvalidFiniteIteratorGenerator, name, err)
		}
		if _, err := gorapide.ParseRapideType(itemType); err != nil {
			return nil, nil, fmt.Errorf("%w: generator %s item type: %v",
				ErrInvalidFiniteIteratorGenerator, name, err)
		}
		normalized[name] = generator
		canonical = append(canonical, canonicalFiniteIteratorGenerator{
			Name: name, ItemType: append(json.RawMessage(nil), itemType...),
			Items: append([]gorapide.CanonicalValue(nil), generator.items...),
		})
	}
	return normalized, canonical, nil
}

package arch

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"

	"github.com/ShaneDolphin/gorapide"
)

var ErrInvalidFiniteIteratorModule = errors.New("invalid deterministic finite Iterator(T) module")

// FiniteIteratorModule is one allocation-identified, closed implementation of
// the published Iterator(T) interface. Its ordered immutable item domain is
// model data; its cursor is fresh execution-local module state.
type FiniteIteratorModule struct {
	module   gorapide.RapideModuleValue
	itemType gorapide.RapideType
	items    []gorapide.CanonicalValue
}

// NewFiniteIteratorModule constructs a finite deterministic Iterator(T)
// implementation. This first implementation kernel requires a predefined item
// type because general structural value membership is not yet available.
func NewFiniteIteratorModule(
	module gorapide.RapideModuleValue,
	itemType gorapide.RapideType,
	items ...any,
) (*FiniteIteratorModule, error) {
	parsed, err := gorapide.ParseRapideModuleValue(module.Identity())
	if err != nil {
		return nil, fmt.Errorf("%w: module: %v", ErrInvalidFiniteIteratorModule, err)
	}
	_, encodedItems, err := encodeFiniteIteratorItems(itemType, items)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidFiniteIteratorModule, err)
	}
	return &FiniteIteratorModule{module: parsed, itemType: itemType, items: encodedItems}, nil
}

func encodeFiniteIteratorItems(
	itemType gorapide.RapideType,
	items []any,
) (string, []gorapide.CanonicalValue, error) {
	typeName, ok := itemType.PredefinedName()
	if !ok {
		return "", nil, fmt.Errorf("item type requires an unsupported structural value-membership kernel")
	}
	if uint64(len(items)) > MaxFiniteRangeIteratorCardinality {
		return "", nil, fmt.Errorf("item cardinality %d exceeds deterministic bound %d",
			len(items), MaxFiniteRangeIteratorCardinality)
	}
	encodedItems := make([]gorapide.CanonicalValue, len(items))
	for index, item := range items {
		encoded, err := gorapide.EncodeCanonicalValue(item)
		if err != nil {
			return "", nil, fmt.Errorf("item %d: %v", index, err)
		}
		decoded, err := gorapide.DecodeCanonicalValue(encoded)
		if err != nil {
			return "", nil, fmt.Errorf("item %d: %v", index, err)
		}
		if !gorapide.CanonicalValueMatchesPredefinedType(decoded, typeName) {
			return "", nil, fmt.Errorf("item %d is not a member of %s", index, typeName)
		}
		encodedItems[index] = encoded
	}
	return typeName, encodedItems, nil
}

// Module returns the allocation identity of this Iterator(T) object.
func (module *FiniteIteratorModule) Module() gorapide.RapideModuleValue {
	if module == nil {
		return gorapide.RapideModuleValue{}
	}
	return module.module
}

// ItemType returns the exact immutable T supplied to Iterator(T).
func (module *FiniteIteratorModule) ItemType() gorapide.RapideType {
	if module == nil {
		return gorapide.RapideType{}
	}
	return module.itemType
}

// Items returns defensive decoded copies in the iterator's semantic order.
func (module *FiniteIteratorModule) Items() ([]any, error) {
	if module == nil {
		return nil, fmt.Errorf("%w: module is nil", ErrInvalidFiniteIteratorModule)
	}
	result := make([]any, len(module.items))
	for index, item := range module.items {
		decoded, err := gorapide.DecodeCanonicalValue(item)
		if err != nil {
			return nil, fmt.Errorf("%w: item %d: %v", ErrInvalidFiniteIteratorModule, index, err)
		}
		result[index] = decoded
	}
	return result, nil
}

func normalizeFiniteIteratorModule(module *FiniteIteratorModule) (*FiniteIteratorModule, error) {
	if module == nil {
		return nil, fmt.Errorf("%w: module is nil", ErrInvalidFiniteIteratorModule)
	}
	items, err := module.Items()
	if err != nil {
		return nil, err
	}
	return NewFiniteIteratorModule(module.module, module.itemType, items...)
}

func (module *FiniteIteratorModule) runtime() (*finiteRangeIterator, error) {
	normalized, err := normalizeFiniteIteratorModule(module)
	if err != nil {
		return nil, err
	}
	items, err := normalized.Items()
	if err != nil {
		return nil, err
	}
	typeName, _ := normalized.itemType.PredefinedName()
	return &finiteRangeIterator{
		module: normalized.module, itemType: typeName, items: items,
	}, nil
}

type canonicalFiniteIteratorModule struct {
	Module   string                    `json:"module"`
	ItemType json.RawMessage           `json:"item_type"`
	Items    []gorapide.CanonicalValue `json:"items"`
}

func canonicalizeFiniteIteratorModules(
	modules map[string]*FiniteIteratorModule,
) (map[string]*FiniteIteratorModule, []canonicalFiniteIteratorModule, error) {
	identities := make([]string, 0, len(modules))
	for identity := range modules {
		identities = append(identities, identity)
	}
	sort.Strings(identities)
	normalized := make(map[string]*FiniteIteratorModule, len(modules))
	canonical := make([]canonicalFiniteIteratorModule, 0, len(modules))
	for _, identity := range identities {
		module, err := normalizeFiniteIteratorModule(modules[identity])
		if err != nil {
			return nil, nil, err
		}
		if module.module.Identity() != identity {
			return nil, nil, fmt.Errorf("%w: registry key %q does not match module %q",
				ErrInvalidFiniteIteratorModule, identity, module.module.Identity())
		}
		itemType, err := module.itemType.MarshalCanonical()
		if err != nil {
			return nil, nil, fmt.Errorf("%w: module %s item type: %v", ErrInvalidFiniteIteratorModule, identity, err)
		}
		if _, err := gorapide.ParseRapideType(itemType); err != nil {
			return nil, nil, fmt.Errorf("%w: module %s item type: %v", ErrInvalidFiniteIteratorModule, identity, err)
		}
		normalized[identity] = module
		canonical = append(canonical, canonicalFiniteIteratorModule{
			Module: identity, ItemType: append(json.RawMessage(nil), itemType...),
			Items: append([]gorapide.CanonicalValue(nil), module.items...),
		})
	}
	return normalized, canonical, nil
}

func initializeFiniteIteratorRuntimes(
	modules map[string]*FiniteIteratorModule,
) (map[string]*finiteRangeIterator, error) {
	result := make(map[string]*finiteRangeIterator, len(modules))
	for identity, module := range modules {
		runtime, err := module.runtime()
		if err != nil {
			return nil, err
		}
		result[identity] = runtime
	}
	return result, nil
}

// IteratorStateRecord is the final cursor state of one declared finite
// Iterator(T) module. Counts are canonical decimal strings.
type IteratorStateRecord struct {
	Module      string `json:"module"`
	ItemType    string `json:"item_type"`
	Cardinality string `json:"cardinality"`
	Next        string `json:"next"`
	Exhausted   bool   `json:"exhausted"`
}

func finiteIteratorStateRecords(iterators map[string]*finiteRangeIterator) []IteratorStateRecord {
	identities := make([]string, 0, len(iterators))
	for identity := range iterators {
		identities = append(identities, identity)
	}
	sort.Strings(identities)
	result := make([]IteratorStateRecord, 0, len(identities))
	for _, identity := range identities {
		iterator := iterators[identity]
		if iterator == nil {
			continue
		}
		result = append(result, IteratorStateRecord{
			Module: identity, ItemType: iterator.itemType,
			Cardinality: strconv.FormatUint(uint64(len(iterator.items)), 10),
			Next:        strconv.FormatUint(iterator.next, 10), Exhausted: !iterator.more(),
		})
	}
	return result
}

func validateFiniteIteratorStatementReferences(
	statements []Statement,
	modules map[string]*FiniteIteratorModule,
	generators map[string]*FiniteIteratorGenerator,
) error {
	for _, statement := range statements {
		switch statement.kind {
		case DoBlockStatementKind:
			if err := validateFiniteIteratorStatementReferences(statement.handledBody, modules, generators); err != nil {
				return err
			}
		case HandlerBlockStatementKind:
			if err := validateFiniteIteratorStatementReferences(statement.handledBody, modules, generators); err != nil {
				return err
			}
			for _, choice := range statement.handler.Choices {
				if err := validateFiniteIteratorStatementReferences(choice.Statements, modules, generators); err != nil {
					return err
				}
			}
			if err := validateFiniteIteratorStatementReferences(statement.handler.Else, modules, generators); err != nil {
				return err
			}
		case ForStatementKind:
			if statement.iteratorKind == moduleStatementIteratorKind {
				module, ok := statement.iteratorValue.literal.(gorapide.RapideModuleValue)
				if statement.iteratorValue.kind != RuleLiteralValue || !ok {
					return fmt.Errorf("%w: for statement has no closed module iterator expression", ErrInvalidFiniteIteratorModule)
				}
				implementation := modules[module.Identity()]
				if implementation == nil {
					return fmt.Errorf("%w: module %s has no declared implementation",
						ErrInvalidFiniteIteratorModule, module.Identity())
				}
				expected, err := gorapide.RapidePredefinedType(statement.iteratorType)
				if err != nil {
					return fmt.Errorf("%w: iterator item type %q: %v", ErrInvalidFiniteIteratorModule, statement.iteratorType, err)
				}
				equal, err := gorapide.RapideTypesEqual(implementation.itemType, expected)
				if err != nil {
					return fmt.Errorf("%w: module %s item type: %v", ErrInvalidFiniteIteratorModule, module.Identity(), err)
				}
				if !equal {
					actualName, _ := implementation.itemType.PredefinedName()
					return fmt.Errorf("%w: module %s supplies %s items, statement expects %s",
						ErrInvalidFiniteIteratorModule, module.Identity(), actualName, statement.iteratorType)
				}
			}
			if statement.iteratorKind == generatorStatementIteratorKind {
				generator := generators[statement.iteratorGenerator]
				if generator == nil {
					return fmt.Errorf("%w: generator %q has no declared implementation",
						ErrInvalidFiniteIteratorGenerator, statement.iteratorGenerator)
				}
				expected, err := gorapide.RapidePredefinedType(statement.iteratorType)
				if err != nil {
					return fmt.Errorf("%w: iterator item type %q: %v",
						ErrInvalidFiniteIteratorGenerator, statement.iteratorType, err)
				}
				equal, err := gorapide.RapideTypesEqual(generator.itemType, expected)
				if err != nil {
					return fmt.Errorf("%w: generator %s item type: %v",
						ErrInvalidFiniteIteratorGenerator, statement.iteratorGenerator, err)
				}
				if !equal {
					actualName, _ := generator.itemType.PredefinedName()
					return fmt.Errorf("%w: generator %s supplies %s items, statement expects %s",
						ErrInvalidFiniteIteratorGenerator, statement.iteratorGenerator,
						actualName, statement.iteratorType)
				}
			}
			if err := validateFiniteIteratorStatementReferences(statement.loopBody, modules, generators); err != nil {
				return err
			}
		case LoopStatementKind:
			if err := validateFiniteIteratorStatementReferences(statement.loopBody, modules, generators); err != nil {
				return err
			}
		case IfStatementKind:
			if err := validateFiniteIteratorStatementReferences(statement.thenBranch, modules, generators); err != nil {
				return err
			}
			if err := validateFiniteIteratorStatementReferences(statement.elseBranch, modules, generators); err != nil {
				return err
			}
		case CaseStatementKind:
			for _, alternative := range statement.caseAlts {
				if err := validateFiniteIteratorStatementReferences(alternative.body, modules, generators); err != nil {
					return err
				}
			}
			if err := validateFiniteIteratorStatementReferences(statement.caseDefault, modules, generators); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateFiniteIteratorModelReferences(
	componentIDs []string,
	initial map[string][]Statement,
	functions map[string]map[string]*FunctionImplementation,
	rules map[string][]*DeclarativeRule,
	processes map[string][]*DeclarativeProcess,
	modules map[string]*FiniteIteratorModule,
	generators map[string]*FiniteIteratorGenerator,
) error {
	for _, componentID := range componentIDs {
		statements := initial[componentID]
		if err := validateFiniteIteratorStatementReferences(statements, modules, generators); err != nil {
			return err
		}
		implementations := functions[componentID]
		functionKeys := make([]string, 0, len(implementations))
		for key := range implementations {
			functionKeys = append(functionKeys, key)
		}
		sort.Strings(functionKeys)
		for _, key := range functionKeys {
			implementation := implementations[key]
			if implementation != nil {
				if err := validateFiniteIteratorStatementReferences(implementation.Statements, modules, generators); err != nil {
					return err
				}
			}
		}
		declarations := rules[componentID]
		for _, rule := range declarations {
			if rule != nil && rule.Body != nil {
				if err := validateFiniteIteratorStatementReferences(rule.Body.Statements, modules, generators); err != nil {
					return err
				}
			}
		}
		processDeclarations := processes[componentID]
		for _, process := range processDeclarations {
			if process == nil {
				continue
			}
			for _, state := range process.States {
				alternatives := append([]AwaitAlternative(nil), state.Alternatives...)
				if state.Else != nil {
					alternatives = append(alternatives, *state.Else)
				}
				for _, alternative := range alternatives {
					if alternative.Body != nil {
						if err := validateFiniteIteratorStatementReferences(alternative.Body.Statements, modules, generators); err != nil {
							return err
						}
					}
				}
			}
		}
	}
	return nil
}

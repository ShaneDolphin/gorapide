package rapide

import (
	"sort"
	"strconv"
	"strings"

	"github.com/ShaneDolphin/gorapide"
)

var sourceReservedStructuralTypeNames = map[string]string{
	"clock":             "Clock",
	"synchronous_clock": "Synchronous_Clock",
	"regular_clock":     "Regular_Clock",
	"slaved_clock":      "Slaved_Clock",
	"gst":               "GST",
	"accuracy":          "Accuracy",
}

func sourceReservedTypeName(key string) (string, bool) {
	if canonical, ok := predefinedTypes[key]; ok {
		return canonical, true
	}
	canonical, ok := sourceReservedStructuralTypeNames[key]
	return canonical, ok
}

type sourceTypeElaborationState uint8

const (
	sourceTypeVisiting sourceTypeElaborationState = iota + 1
	sourceTypeComplete
)

// sourceTypeElaborator resolves the finite source type namespace without adding
// nominal identity. Global aliases, direct finite ranges, and named interface
// declarations share one namespace; cached structural results are their
// acyclic or recursively bound denotations. Range bounds remain separate until
// their constrained value semantics are supported outside finite domains.
type sourceTypeElaborator struct {
	interfaces    map[string]InterfaceDecl
	unions        map[string]UnionDecl
	enumerations  map[string]EnumerationDecl
	aliases       map[string]TypeAliasDecl
	integerRanges map[string]TypeAliasDecl
	states        map[string]sourceTypeElaborationState
	resolved      map[string]gorapide.RapideType
	inProgress    map[string]gorapide.RapideType
}

func newSourceTypeElaborator(
	interfaces map[string]InterfaceDecl,
	declarations []TypeAliasDecl,
) (*sourceTypeElaborator, error) {
	return newSourceTypeElaboratorWithUnionsAndEnumerations(interfaces, declarations, nil, nil)
}

func newSourceTypeElaboratorWithUnions(
	interfaces map[string]InterfaceDecl,
	declarations []TypeAliasDecl,
	unionDeclarations []UnionDecl,
) (*sourceTypeElaborator, error) {
	return newSourceTypeElaboratorWithUnionsAndEnumerations(
		interfaces, declarations, unionDeclarations, nil,
	)
}

func newSourceTypeElaboratorWithUnionsAndEnumerations(
	interfaces map[string]InterfaceDecl,
	declarations []TypeAliasDecl,
	unionDeclarations []UnionDecl,
	enumerationDeclarations []EnumerationDecl,
) (*sourceTypeElaborator, error) {
	result := &sourceTypeElaborator{
		interfaces:    interfaces,
		unions:        make(map[string]UnionDecl, len(unionDeclarations)),
		enumerations:  make(map[string]EnumerationDecl, len(enumerationDeclarations)),
		aliases:       make(map[string]TypeAliasDecl, len(declarations)),
		integerRanges: make(map[string]TypeAliasDecl),
		states:        make(map[string]sourceTypeElaborationState, len(interfaces)+len(declarations)+len(unionDeclarations)+len(enumerationDeclarations)),
		resolved:      make(map[string]gorapide.RapideType, len(interfaces)+len(declarations)+len(unionDeclarations)+len(enumerationDeclarations)),
		inProgress:    make(map[string]gorapide.RapideType),
	}
	interfaceKeys := make([]string, 0, len(interfaces))
	for key := range interfaces {
		interfaceKeys = append(interfaceKeys, key)
	}
	sort.Strings(interfaceKeys)
	for _, key := range interfaceKeys {
		declaration := interfaces[key]
		if canonical, predefined := sourceReservedTypeName(key); predefined {
			return nil, typeError(declaration.Position,
				"interface type %q collides with predefined type %q in the current source profile", declaration.Name, canonical)
		}
	}
	for _, declaration := range unionDeclarations {
		key := folded(declaration.Name)
		if key == "" {
			return nil, typeError(declaration.Position, "Union type has no name")
		}
		if canonical, predefined := sourceReservedTypeName(key); predefined {
			return nil, typeError(declaration.Position,
				"Union type %q collides with predefined type %q in the current source profile", declaration.Name, canonical)
		}
		if previous, exists := result.unions[key]; exists {
			return nil, typeError(declaration.Position,
				"duplicate Union type %q (previous spelling %q)", declaration.Name, previous.Name)
		}
		if existing, exists := interfaces[key]; exists {
			return nil, typeError(declaration.Position,
				"Union type %q collides with interface type %q", declaration.Name, existing.Name)
		}
		result.unions[key] = declaration
	}
	for _, declaration := range enumerationDeclarations {
		key := folded(declaration.Name)
		if key == "" {
			return nil, typeError(declaration.Position, "Enumeration type has no name")
		}
		if canonical, predefined := sourceReservedTypeName(key); predefined {
			return nil, typeError(declaration.Position,
				"Enumeration type %q collides with predefined type %q in the current source profile", declaration.Name, canonical)
		}
		if previous, exists := result.enumerations[key]; exists {
			return nil, typeError(declaration.Position,
				"duplicate Enumeration type %q (previous spelling %q)", declaration.Name, previous.Name)
		}
		if existing, exists := interfaces[key]; exists {
			return nil, typeError(declaration.Position,
				"Enumeration type %q collides with interface type %q", declaration.Name, existing.Name)
		}
		if existing, exists := result.unions[key]; exists {
			return nil, typeError(declaration.Position,
				"Enumeration type %q collides with Union type %q", declaration.Name, existing.Name)
		}
		result.enumerations[key] = declaration
	}
	for _, declaration := range declarations {
		key := folded(declaration.Name)
		if key == "" {
			return nil, typeError(declaration.Position, "type alias has no name")
		}
		if canonical, predefined := sourceReservedTypeName(key); predefined {
			return nil, typeError(declaration.Position,
				"type alias %q collides with predefined type %q in the current source profile", declaration.Name, canonical)
		}
		if existing, exists := result.aliases[key]; exists {
			return nil, typeError(declaration.Position,
				"duplicate type alias %q (previous spelling %q)", declaration.Name, existing.Name)
		}
		if existing, exists := result.integerRanges[key]; exists {
			return nil, typeError(declaration.Position,
				"duplicate type alias %q (previous spelling %q)", declaration.Name, existing.Name)
		}
		if existing, exists := interfaces[key]; exists {
			return nil, typeError(declaration.Position,
				"type alias %q collides with interface type %q", declaration.Name, existing.Name)
		}
		if existing, exists := result.unions[key]; exists {
			return nil, typeError(declaration.Position,
				"type alias %q collides with Union type %q", declaration.Name, existing.Name)
		}
		if existing, exists := result.enumerations[key]; exists {
			return nil, typeError(declaration.Position,
				"type alias %q collides with Enumeration type %q", declaration.Name, existing.Name)
		}
		if declaration.IntegerRange {
			result.integerRanges[key] = declaration
		} else {
			result.aliases[key] = declaration
		}
	}
	if err := result.validateServiceExpansionAcyclic(); err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(result.aliases)+len(result.unions)+len(result.enumerations))
	for key := range result.aliases {
		keys = append(keys, key)
	}
	for key := range result.unions {
		keys = append(keys, key)
	}
	for key := range result.enumerations {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		position := Position{Line: 1, Column: 1}
		name := key
		if alias, exists := result.aliases[key]; exists {
			position, name = alias.Position, alias.Name
		} else if union, exists := result.unions[key]; exists {
			position, name = union.Position, union.Name
		} else if enumeration, exists := result.enumerations[key]; exists {
			position, name = enumeration.Position, enumeration.Name
		}
		if _, err := result.resolveNamed(position, name, nil); err != nil {
			return nil, err
		}
	}
	return result, nil
}

type sourceServiceExpansionEdge struct {
	target  string
	service InterfaceServiceDecl
}

func (current *sourceTypeElaborator) validateServiceExpansionAcyclic() error {
	edges := make(map[string][]sourceServiceExpansionEdge, len(current.interfaces))
	keys := make([]string, 0, len(current.interfaces))
	for key, declaration := range current.interfaces {
		keys = append(keys, key)
		for _, service := range declaration.Services {
			if service.IntegerSet && service.FirstIndex > service.LastIndex {
				continue
			}
			expression := declaredTypeExpression(service.Position, service.Type, service.TypeExpression)
			target, named := typeExpressionNamedTarget(expression)
			if !named {
				continue
			}
			targetKey, _, err := current.interfaceDeclaration(service.Position, target)
			if err != nil {
				continue
			}
			edges[key] = append(edges[key], sourceServiceExpansionEdge{target: targetKey, service: service})
		}
	}
	sort.Strings(keys)
	for key := range edges {
		sort.Slice(edges[key], func(left, right int) bool {
			if edges[key][left].target != edges[key][right].target {
				return edges[key][left].target < edges[key][right].target
			}
			return serviceDeclarationKey(edges[key][left].service) < serviceDeclarationKey(edges[key][right].service)
		})
	}

	states := make(map[string]sourceTypeElaborationState, len(current.interfaces))
	stack := make([]string, 0, len(current.interfaces))
	var visit func(string) error
	visit = func(key string) error {
		states[key] = sourceTypeVisiting
		stack = append(stack, key)
		defer func() { stack = stack[:len(stack)-1] }()
		for _, edge := range edges[key] {
			if states[edge.target] == sourceTypeComplete {
				continue
			}
			if states[edge.target] == sourceTypeVisiting {
				start := 0
				for index, candidate := range stack {
					if candidate == edge.target {
						start = index
						break
					}
				}
				cycle := make([]string, 0, len(stack)-start+1)
				for _, candidate := range stack[start:] {
					cycle = append(cycle, current.interfaces[candidate].Name)
				}
				cycle = append(cycle, current.interfaces[edge.target].Name)
				return typeError(edge.service.Position,
					"service expansion cycle %s has no finite service-free rewrite", strings.Join(cycle, " -> "))
			}
			if err := visit(edge.target); err != nil {
				return err
			}
		}
		states[key] = sourceTypeComplete
		return nil
	}
	for _, key := range keys {
		if states[key] == 0 {
			if err := visit(key); err != nil {
				return err
			}
		}
	}
	return nil
}

type sourceExecutionInterfaceExpansion struct {
	actions     []ActionDecl
	functions   []FunctionDecl
	exceptions  []ExceptionDecl
	constraints []ConstraintDecl
}

func (current *sourceTypeElaborator) executionInterfaceExpansion(
	declaration InterfaceDecl,
) (sourceExecutionInterfaceExpansion, error) {
	cache := make(map[string]sourceExecutionInterfaceExpansion, len(current.interfaces))
	var expand func(InterfaceDecl) (sourceExecutionInterfaceExpansion, error)
	expand = func(source InterfaceDecl) (sourceExecutionInterfaceExpansion, error) {
		key := folded(source.Name)
		if cached, exists := cache[key]; exists {
			return sourceExecutionInterfaceExpansion{
				actions:     cloneActionDeclarations(cached.actions),
				functions:   cloneFunctionDeclarations(cached.functions),
				exceptions:  cloneExceptionDeclarations(cached.exceptions),
				constraints: cloneConstraintDeclarations(cached.constraints),
			}, nil
		}
		result := sourceExecutionInterfaceExpansion{
			actions:     cloneActionDeclarations(source.Actions),
			functions:   cloneFunctionDeclarations(source.Functions),
			exceptions:  cloneExceptionDeclarations(source.Exceptions),
			constraints: cloneConstraintDeclarations(source.Constraints),
		}
		for _, service := range source.Services {
			prefixes, err := sourceServicePrefixes(service)
			if err != nil {
				return sourceExecutionInterfaceExpansion{}, err
			}
			if len(prefixes) == 0 {
				continue
			}
			expression := declaredTypeExpression(service.Position, service.Type, service.TypeExpression)
			targetName, named := typeExpressionNamedTarget(expression)
			if !named {
				// Closed constructor applications remain valid structural service
				// types. Their executable member adapter requires public structural
				// type introspection and remains a separate explicit boundary.
				continue
			}
			_, target, err := current.interfaceDeclaration(service.Position, targetName)
			if err != nil {
				// Structural elaboration remains authoritative for invalid or
				// non-interface service targets, including empty service sets.
				// Deferring that diagnostic preserves the source type error and
				// keeps this adapter limited to execution-facing declarations.
				continue
			}
			targetExpansion, err := expand(target)
			if err != nil {
				return sourceExecutionInterfaceExpansion{}, err
			}
			for _, action := range targetExpansion.actions {
				if action.Mode == ActionPrivate {
					return sourceExecutionInterfaceExpansion{}, typeError(service.Position,
						"service %q target %q contains a forbidden private action %q",
						service.Name, target.Name, action.Name)
				}
			}
			for _, function := range targetExpansion.functions {
				if function.Mode == FunctionPrivate {
					return sourceExecutionInterfaceExpansion{}, typeError(service.Position,
						"service %q target %q contains a forbidden private function %q",
						service.Name, target.Name, function.Name)
				}
			}
			for _, exception := range targetExpansion.exceptions {
				if exception.Constituent && exception.Region == InterfaceNamePrivate {
					return sourceExecutionInterfaceExpansion{}, typeError(service.Position,
						"service %q target %q contains a forbidden private exception %q",
						service.Name, target.Name, exception.Name)
				}
			}
			for _, prefix := range prefixes {
				for _, action := range targetExpansion.actions {
					copy := cloneActionDecl(action)
					copy.Position = service.Position
					copy.Name = prefix + "." + folded(action.Name)
					for index := range copy.Parameters {
						copy.Parameters[index].Position = service.Position
						copy.Parameters[index].Name = folded(copy.Parameters[index].Name)
					}
					if service.Dual {
						if copy.Mode == ActionIn {
							copy.Mode = ActionOut
						} else {
							copy.Mode = ActionIn
						}
					}
					result.actions = append(result.actions, copy)
				}
				for _, function := range targetExpansion.functions {
					copy := cloneFunctionDecl(function)
					copy.Position = service.Position
					copy.Name = prefix + "." + folded(function.Name)
					for index := range copy.Parameters {
						copy.Parameters[index].Position = service.Position
						copy.Parameters[index].Name = folded(copy.Parameters[index].Name)
					}
					if service.Dual {
						if copy.Mode == FunctionProvides {
							copy.Mode = FunctionRequires
						} else {
							copy.Mode = FunctionProvides
						}
					}
					result.functions = append(result.functions, copy)
				}
				for _, exception := range targetExpansion.exceptions {
					if !exception.Constituent {
						continue
					}
					copy := exception
					copy.Position = service.Position
					copy.Name = prefix + "." + folded(exception.Name)
					copy.Parameters = append([]ParameterDecl(nil), exception.Parameters...)
					for index := range copy.Parameters {
						copy.Parameters[index].Position = service.Position
						copy.Parameters[index].Name = folded(copy.Parameters[index].Name)
					}
					if service.Dual {
						if copy.Region == InterfaceNameProvides {
							copy.Region = InterfaceNameRequires
						} else if copy.Region == InterfaceNameRequires {
							copy.Region = InterfaceNameProvides
						}
					}
					copy.Declaration = interfaceExceptionDeclarationIdentity(
						source.Name, copy.Region, copy.Name,
					)
					result.exceptions = append(result.exceptions, copy)
				}
				for _, constraint := range targetExpansion.constraints {
					copy, err := qualifyServiceConstraintDeclaration(constraint, prefix, service.Position)
					if err != nil {
						return sourceExecutionInterfaceExpansion{}, err
					}
					result.constraints = append(result.constraints, copy)
				}
			}
		}
		cache[key] = sourceExecutionInterfaceExpansion{
			actions:     cloneActionDeclarations(result.actions),
			functions:   cloneFunctionDeclarations(result.functions),
			exceptions:  cloneExceptionDeclarations(result.exceptions),
			constraints: cloneConstraintDeclarations(result.constraints),
		}
		return result, nil
	}
	return expand(declaration)
}

func sourceServicePrefixes(service InterfaceServiceDecl) ([]string, error) {
	base := folded(service.Name)
	if !service.IntegerSet {
		return []string{base}, nil
	}
	if service.FirstIndex > service.LastIndex {
		return nil, nil
	}
	result := make([]string, 0)
	for index := service.FirstIndex; ; index++ {
		if uint64(index-service.FirstIndex) >= gorapide.MaxRapideIntegerServiceSetCardinality {
			return nil, typeError(service.Position,
				"service set %q cardinality exceeds deterministic limit %d",
				service.Name, gorapide.MaxRapideIntegerServiceSetCardinality)
		}
		result = append(result, base+"("+strconv.FormatInt(index, 10)+")")
		if index == service.LastIndex {
			break
		}
	}
	return result, nil
}

type sourceServiceInstance struct {
	position   Position
	path       string
	targetType gorapide.RapideType
	target     InterfaceDecl
	dual       bool
}

// serviceInstances returns every finite service name introduced by a source
// interface, including recursively nested and Integer-indexed services. Dual
// parity accumulates through nesting exactly as the Architecture LRM rewrite.
func (current *sourceTypeElaborator) serviceInstances(
	declaration InterfaceDecl,
) (map[string]sourceServiceInstance, error) {
	result := make(map[string]sourceServiceInstance)
	var expand func(string, bool, InterfaceDecl) error
	expand = func(parent string, inheritedDual bool, source InterfaceDecl) error {
		for _, service := range source.Services {
			prefixes, err := sourceServicePrefixes(service)
			if err != nil {
				return err
			}
			if len(prefixes) == 0 {
				continue
			}
			expression := declaredTypeExpression(service.Position, service.Type, service.TypeExpression)
			targetType, err := current.resolveTypeExpression(expression, nil)
			if err != nil {
				return err
			}
			targetName, named := typeExpressionNamedTarget(expression)
			if !named {
				for _, prefix := range prefixes {
					path := prefix
					if parent != "" {
						path = parent + "." + prefix
					}
					key := folded(path)
					if previous, exists := result[key]; exists {
						return typeError(service.Position,
							"service path %q collides with service declared at %d:%d",
							path, previous.position.Line, previous.position.Column)
					}
					result[key] = sourceServiceInstance{
						position: service.Position, path: path, targetType: targetType,
						dual: inheritedDual != service.Dual,
					}
				}
				continue
			}
			_, target, err := current.interfaceDeclaration(service.Position, targetName)
			if err != nil {
				return err
			}
			effectiveDual := inheritedDual != service.Dual
			for _, prefix := range prefixes {
				path := prefix
				if parent != "" {
					path = parent + "." + prefix
				}
				key := folded(path)
				if previous, exists := result[key]; exists {
					return typeError(service.Position,
						"service path %q collides with service declared at %d:%d",
						path, previous.position.Line, previous.position.Column)
				}
				result[key] = sourceServiceInstance{
					position: service.Position, path: path, targetType: targetType,
					target: target, dual: effectiveDual,
				}
				if err := expand(path, effectiveDual, target); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := expand("", false, declaration); err != nil {
		return nil, err
	}
	return result, nil
}

func cloneActionDeclarations(source []ActionDecl) []ActionDecl {
	result := make([]ActionDecl, len(source))
	for index, declaration := range source {
		result[index] = cloneActionDecl(declaration)
	}
	return result
}

func cloneFunctionDeclarations(source []FunctionDecl) []FunctionDecl {
	result := make([]FunctionDecl, len(source))
	for index, declaration := range source {
		result[index] = cloneFunctionDecl(declaration)
	}
	return result
}

func cloneExceptionDeclarations(source []ExceptionDecl) []ExceptionDecl {
	result := make([]ExceptionDecl, len(source))
	for index, declaration := range source {
		result[index] = declaration
		result[index].Parameters = append([]ParameterDecl(nil), declaration.Parameters...)
	}
	return result
}

func cloneConstraintDeclarations(source []ConstraintDecl) []ConstraintDecl {
	result := make([]ConstraintDecl, len(source))
	for index, declaration := range source {
		result[index] = cloneConstraintDeclaration(declaration)
	}
	return result
}

func cloneConstraintDeclaration(source ConstraintDecl) ConstraintDecl {
	result := source
	result.Alphabet = make([]BehaviorPatternDecl, len(source.Alphabet))
	for index, declaration := range source.Alphabet {
		result.Alphabet[index] = cloneBehaviorPatternDeclaration(declaration)
	}
	result.Components = make([]ConstraintComponentDecl, len(source.Components))
	for index, component := range source.Components {
		copy := component
		copy.Placeholders = append([]ParameterDecl(nil), component.Placeholders...)
		copy.Pattern = cloneBehaviorPatternDeclaration(component.Pattern)
		copy.Guard = cloneExpressionDeclarationPointer(component.Guard)
		result.Components[index] = copy
	}
	return result
}

func cloneBehaviorPatternDeclaration(source BehaviorPatternDecl) BehaviorPatternDecl {
	result := source
	result.Event.ComponentIndex = cloneExpressionDeclarationPointer(source.Event.ComponentIndex)
	result.Event.Path = append([]QualifiedMemberSegmentDecl(nil), source.Event.Path...)
	for index := range result.Event.Path {
		result.Event.Path[index].Index = cloneExpressionDeclarationPointer(source.Event.Path[index].Index)
	}
	result.Event.Arguments = make([]PatternParameterAssociationDecl, len(source.Event.Arguments))
	for index, association := range source.Event.Arguments {
		copy := association
		copy.Actual = cloneExpressionDeclaration(association.Actual)
		result.Event.Arguments[index] = copy
	}
	if source.Left != nil {
		copy := cloneBehaviorPatternDeclaration(*source.Left)
		result.Left = &copy
	}
	if source.Right != nil {
		copy := cloneBehaviorPatternDeclaration(*source.Right)
		result.Right = &copy
	}
	if source.Inner != nil {
		copy := cloneBehaviorPatternDeclaration(*source.Inner)
		result.Inner = &copy
	}
	return result
}

func cloneExpressionDeclaration(source ExpressionDecl) ExpressionDecl {
	result := source
	result.StringCodes = append([]int64(nil), source.StringCodes...)
	result.Arguments = make([]ExpressionDecl, len(source.Arguments))
	for index, argument := range source.Arguments {
		result.Arguments[index] = cloneExpressionDeclaration(argument)
	}
	result.RecordFields = make([]RecordFieldExpressionDecl, len(source.RecordFields))
	for index, field := range source.RecordFields {
		result.RecordFields[index] = field
		result.RecordFields[index].Value = cloneExpressionDeclaration(field.Value)
	}
	result.Left = cloneExpressionDeclarationPointer(source.Left)
	result.Right = cloneExpressionDeclarationPointer(source.Right)
	return result
}

func cloneExpressionDeclarationPointer(source *ExpressionDecl) *ExpressionDecl {
	if source == nil {
		return nil
	}
	copy := cloneExpressionDeclaration(*source)
	return &copy
}

func qualifyServiceConstraintDeclaration(
	source ConstraintDecl,
	prefix string,
	position Position,
) (ConstraintDecl, error) {
	result := cloneConstraintDeclaration(source)
	result.Position = position
	if result.Label != "" {
		result.Label = prefix + "." + folded(result.Label)
	}
	for index, declaration := range result.Alphabet {
		qualified, err := qualifyServiceConstraintPattern(declaration, prefix)
		if err != nil {
			return ConstraintDecl{}, err
		}
		result.Alphabet[index] = qualified
	}
	for index, component := range result.Components {
		if component.Label != "" {
			component.Label = prefix + "." + folded(component.Label)
		}
		qualified, err := qualifyServiceConstraintPattern(component.Pattern, prefix)
		if err != nil {
			return ConstraintDecl{}, err
		}
		component.Pattern = qualified
		result.Components[index] = component
	}
	return result, nil
}

func qualifyServiceConstraintPattern(
	source BehaviorPatternDecl,
	prefix string,
) (BehaviorPatternDecl, error) {
	result := cloneBehaviorPatternDeclaration(source)
	switch source.Kind {
	case BehaviorBasicPattern:
		if source.Event.Component != "" {
			return BehaviorPatternDecl{}, typeError(source.Event.Position,
				"service target constraint action %q cannot be component-qualified before service rewriting",
				source.Event.Component+"."+source.Event.Name)
		}
		result.Event.Name = prefix + "." + folded(source.Event.Name)
	case BehaviorBinaryPattern:
		if source.Left != nil {
			left, err := qualifyServiceConstraintPattern(*source.Left, prefix)
			if err != nil {
				return BehaviorPatternDecl{}, err
			}
			result.Left = &left
		}
		if source.Right != nil {
			right, err := qualifyServiceConstraintPattern(*source.Right, prefix)
			if err != nil {
				return BehaviorPatternDecl{}, err
			}
			result.Right = &right
		}
	case BehaviorIterationPattern:
		if source.Inner != nil {
			inner, err := qualifyServiceConstraintPattern(*source.Inner, prefix)
			if err != nil {
				return BehaviorPatternDecl{}, err
			}
			result.Inner = &inner
		}
	default:
		return BehaviorPatternDecl{}, typeError(source.Position,
			"service target constraint has unsupported pattern kind %q", source.Kind)
	}
	return result, nil
}

func (current *sourceTypeElaborator) resolveNamed(
	position Position,
	name string,
	stack []string,
) (gorapide.RapideType, error) {
	if typ, ok := structuralDirectPredefinedType(name); ok {
		return typ, nil
	}
	key := folded(name)
	switch key {
	case "synchronous_clock":
		_, err := gorapide.RapideSynchronousClockType()
		return gorapide.RapideType{}, typeError(position, "%v", err)
	case "regular_clock":
		_, err := gorapide.RapideRegularClockType()
		return gorapide.RapideType{}, typeError(position, "%v", err)
	case "slaved_clock":
		_, err := gorapide.RapideSlavedClockType()
		return gorapide.RapideType{}, typeError(position, "%v", err)
	}
	if declaration, isRange := current.integerRanges[key]; isRange {
		return gorapide.RapideType{}, typeError(position,
			"finite Integer range type %q is currently supported only as a finite component-array or connection-generator domain", declaration.Name)
	}
	if current.states[key] == sourceTypeComplete {
		return current.resolved[key], nil
	}
	if current.states[key] == sourceTypeVisiting {
		if recursive, ok := current.recursiveInterfaceDenotation(key); ok {
			return recursive, nil
		}
		if current.sourceTypeCycleContainsUnion(stack, key) {
			return gorapide.RapideType{}, typeError(position,
				"recursive Union/function type cycle %s requires general recursive function-type binders",
				sourceTypeCyclePath(stack, key, current.interfaces, current.aliases, current.unions))
		}
		return gorapide.RapideType{}, typeError(position,
			"recursive type alias cycle %s has no interface constructor to bind",
			sourceTypeCyclePath(stack, key, current.interfaces, current.aliases, current.unions))
	}
	alias, isAlias := current.aliases[key]
	declaration, isInterface := current.interfaces[key]
	union, isUnion := current.unions[key]
	enumeration, isEnumeration := current.enumerations[key]
	if !isAlias && !isInterface && !isUnion && !isEnumeration {
		return gorapide.RapideType{}, typeError(position,
			"type %q is outside the current deterministic Rapide type-expression subset", name)
	}
	current.states[key] = sourceTypeVisiting
	stack = append(stack, key)
	var result gorapide.RapideType
	var err error
	if isAlias {
		result, err = current.resolveTypeExpression(typeAliasExpression(alias), stack)
	} else if isUnion {
		result, err = current.compileUnion(union, stack)
	} else if isEnumeration {
		result, err = current.compileEnumeration(enumeration)
	} else {
		result, err = current.compileInterface(declaration, stack)
	}
	if err != nil {
		delete(current.states, key)
		return gorapide.RapideType{}, err
	}
	current.resolved[key] = result
	current.states[key] = sourceTypeComplete
	return result, nil
}

func (current *sourceTypeElaborator) sourceTypeCycleContainsUnion(stack []string, key string) bool {
	start := 0
	for index, candidate := range stack {
		if candidate == key {
			start = index
			break
		}
	}
	for _, candidate := range append(append([]string(nil), stack[start:]...), key) {
		if _, exists := current.unions[candidate]; exists {
			return true
		}
	}
	return false
}

func typeAliasExpression(declaration TypeAliasDecl) TypeExpressionDecl {
	return declaredTypeExpression(declaration.Position, declaration.Target, declaration.Expression)
}

func (current *sourceTypeElaborator) finiteIntegerRange(
	position Position,
	name string,
	context string,
) (TypeAliasDecl, error) {
	declaration, ok := current.integerRanges[folded(name)]
	if !ok {
		return TypeAliasDecl{}, typeError(position,
			"%s index type %q is not a declared closed Integer range", context, name)
	}
	return declaration, nil
}

func declaredTypeExpression(
	position Position,
	spelling string,
	parsed TypeExpressionDecl,
) TypeExpressionDecl {
	if parsed.Kind != "" {
		return parsed
	}
	return TypeExpressionDecl{Position: position, Kind: TypeExpressionName, Name: spelling}
}

func typeExpressionNamedTarget(expression TypeExpressionDecl) (string, bool) {
	return expression.Name, expression.Kind == TypeExpressionName && expression.Name != ""
}

func typeExpressionReferencesAny(expression TypeExpressionDecl, names map[string]bool) bool {
	if names[folded(expression.Name)] {
		return true
	}
	for _, argument := range expression.Arguments {
		if typeExpressionReferencesAny(argument, names) {
			return true
		}
	}
	return false
}

func elaborateSourceTypeExpression(
	expression TypeExpressionDecl,
	resolveName func(Position, string) (gorapide.RapideType, error),
) (gorapide.RapideType, error) {
	switch expression.Kind {
	case TypeExpressionName:
		return resolveName(expression.Position, expression.Name)
	case TypeExpressionApplication:
		arguments := make([]gorapide.RapideType, len(expression.Arguments))
		for index, argument := range expression.Arguments {
			resolved, err := elaborateSourceTypeExpression(argument, resolveName)
			if err != nil {
				return gorapide.RapideType{}, err
			}
			arguments[index] = resolved
		}
		result, err := gorapide.ApplyRapideTypeConstructor(expression.Name, arguments...)
		if err != nil {
			return gorapide.RapideType{}, typeError(expression.Position,
				"type-constructor application %s: %v", typeExpressionSpelling(expression), err)
		}
		return result, nil
	default:
		return gorapide.RapideType{}, typeError(expression.Position,
			"type expression has unsupported kind %q", expression.Kind)
	}
}

func (current *sourceTypeElaborator) resolveTypeExpression(
	expression TypeExpressionDecl,
	stack []string,
) (gorapide.RapideType, error) {
	return elaborateSourceTypeExpression(expression, func(position Position, name string) (gorapide.RapideType, error) {
		return current.resolveNamed(position, name, stack)
	})
}

func (current *sourceTypeElaborator) recursiveInterfaceDenotation(key string) (gorapide.RapideType, bool) {
	seen := make(map[string]bool)
	for {
		if seen[key] {
			return gorapide.RapideType{}, false
		}
		seen[key] = true
		if _, isInterface := current.interfaces[key]; isInterface {
			typ, exists := current.inProgress[key]
			return typ, exists
		}
		alias, isAlias := current.aliases[key]
		if !isAlias {
			return gorapide.RapideType{}, false
		}
		target, named := typeExpressionNamedTarget(typeAliasExpression(alias))
		if !named {
			return gorapide.RapideType{}, false
		}
		key = folded(target)
	}
}

func (current *sourceTypeElaborator) interfaceType(key string) (gorapide.RapideType, error) {
	declaration, exists := current.interfaces[key]
	if !exists {
		return gorapide.RapideType{}, typeError(Position{Line: 1, Column: 1}, "interface type %q is undeclared", key)
	}
	return current.resolveNamed(declaration.Position, declaration.Name, nil)
}

func (current *sourceTypeElaborator) interfaceDeclaration(
	position Position,
	name string,
) (string, InterfaceDecl, error) {
	seen := make(map[string]bool)
	path := make([]string, 0)
	for {
		key := folded(name)
		if declaration, ok := current.interfaces[key]; ok {
			return key, declaration, nil
		}
		if canonical, ok := predefinedTypes[key]; ok {
			return "", InterfaceDecl{}, typeError(position,
				"type %q denotes predefined value type %q, not an interface declaration in the current source subset", name, canonical)
		}
		if canonical, ok := sourceReservedStructuralTypeNames[key]; ok {
			return "", InterfaceDecl{}, typeError(position,
				"type %q denotes predefined structural type %q, which cannot be copied by the current named-interface derivation normalizer",
				name, canonical)
		}
		if union, ok := current.unions[key]; ok {
			return "", InterfaceDecl{}, typeError(position,
				"type %q denotes Union function type %q, not an interface declaration", name, union.Name)
		}
		if enumeration, ok := current.enumerations[key]; ok {
			return "", InterfaceDecl{}, typeError(position,
				"type %q denotes Enumeration function type %q, not an interface declaration", name, enumeration.Name)
		}
		if seen[key] {
			path = append(path, sourceTypeSpelling(key, current.interfaces, current.aliases, current.unions))
			return "", InterfaceDecl{}, typeError(position,
				"type alias cycle %s", strings.Join(path, " -> "))
		}
		seen[key] = true
		path = append(path, sourceTypeSpelling(key, current.interfaces, current.aliases, current.unions))
		alias, ok := current.aliases[key]
		if !ok {
			return "", InterfaceDecl{}, typeError(position,
				"type %q is not a declared interface or interface alias", name)
		}
		target, named := typeExpressionNamedTarget(typeAliasExpression(alias))
		if !named {
			return "", InterfaceDecl{}, typeError(position,
				"type alias %q denotes structural application %s, not a named interface declaration",
				alias.Name, alias.Target)
		}
		name = target
	}
}

func (current *sourceTypeElaborator) executionPredefinedType(position Position, name string) (string, error) {
	seen := make(map[string]bool)
	path := make([]string, 0)
	for {
		key := folded(name)
		if canonical, ok := predefinedTypes[key]; ok {
			return canonical, nil
		}
		if declaration, isRange := current.integerRanges[key]; isRange {
			return "", typeError(position,
				"finite Integer range type %q is currently supported only as a finite component-array or connection-generator domain", declaration.Name)
		}
		if seen[key] {
			path = append(path, name)
			return "", typeError(position, "type alias cycle %s", strings.Join(path, " -> "))
		}
		seen[key] = true
		path = append(path, name)
		if alias, ok := current.aliases[key]; ok {
			target, named := typeExpressionNamedTarget(typeAliasExpression(alias))
			if !named {
				return "", typeError(position,
					"type alias %q denotes structural application %s and cannot be used in an execution-facing predefined value slot",
					alias.Name, alias.Target)
			}
			name = target
			continue
		}
		if declaration, ok := current.interfaces[key]; ok {
			return "", typeError(position,
				"interface type %q cannot yet be adapted to an execution-facing predefined value slot", declaration.Name)
		}
		return "", typeError(position,
			"type %q is outside the current deterministic Rapide type subset", name)
	}
}

func (current *sourceTypeElaborator) executionPredefinedTypeExpression(
	position Position,
	name string,
	expression TypeExpressionDecl,
) (string, error) {
	parsed := declaredTypeExpression(position, name, expression)
	if parsed.Kind == TypeExpressionApplication {
		return "", typeError(position,
			"type-constructor application %s cannot yet be used in an execution-facing predefined value slot",
			typeExpressionSpelling(parsed))
	}
	return current.executionPredefinedType(position, parsed.Name)
}

func (current *sourceTypeElaborator) moduleTypeDenotations(
	module ModuleDecl,
	target gorapide.RapideType,
) ([]gorapide.RapideTypeDenotation, error) {
	declarations := make(map[string]TypeAliasDecl, len(module.Types))
	for _, declaration := range module.Types {
		key := folded(declaration.Name)
		if existing, exists := declarations[key]; exists {
			return nil, typeError(declaration.Position,
				"duplicate module type denotation %q (previous spelling %q)", declaration.Name, existing.Name)
		}
		declarations[key] = declaration
	}
	states := make(map[string]sourceTypeElaborationState, len(declarations))
	resolved := make(map[string]gorapide.RapideType, len(declarations))
	var resolve func(string, []string) (gorapide.RapideType, error)
	resolve = func(key string, stack []string) (gorapide.RapideType, error) {
		if states[key] == sourceTypeComplete {
			return resolved[key], nil
		}
		declaration, exists := declarations[key]
		if !exists {
			return gorapide.RapideType{}, typeError(module.Position,
				"module %q has no type denotation %q", module.Name, key)
		}
		if states[key] == sourceTypeVisiting {
			start := 0
			for index, candidate := range stack {
				if candidate == key {
					start = index
					break
				}
			}
			cycle := make([]string, 0, len(stack)-start+1)
			for _, candidate := range stack[start:] {
				cycle = append(cycle, declarations[candidate].Name)
			}
			cycle = append(cycle, declaration.Name)
			return gorapide.RapideType{}, typeError(declaration.Position,
				"recursive module type denotation %s has no type-constructor body", strings.Join(cycle, " -> "))
		}
		states[key] = sourceTypeVisiting
		stack = append(stack, key)
		typ, err := elaborateSourceTypeExpression(
			typeAliasExpression(declaration),
			func(position Position, name string) (gorapide.RapideType, error) {
				targetKey := folded(name)
				if _, local := declarations[targetKey]; local {
					return resolve(targetKey, stack)
				}
				return current.resolveNamed(position, name, nil)
			},
		)
		if err != nil {
			delete(states, key)
			return gorapide.RapideType{}, err
		}
		resolved[key] = typ
		states[key] = sourceTypeComplete
		return typ, nil
	}

	keys := make([]string, 0, len(declarations))
	for key := range declarations {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	denotations := make([]gorapide.RapideTypeDenotation, 0, len(keys))
	for _, key := range keys {
		typ, err := resolve(key, nil)
		if err != nil {
			return nil, err
		}
		denotation, err := gorapide.NewRapideTypeDenotation(declarations[key].Name, typ)
		if err != nil {
			return nil, typeError(declarations[key].Position,
				"module %q type denotation %q: %v", module.Name, declarations[key].Name, err)
		}
		denotations = append(denotations, denotation)
	}
	normalized, err := gorapide.ValidateRapideInterfaceTypeDenotations(target, denotations...)
	if err != nil {
		return nil, typeError(module.Position, "module %q type membership: %v", module.Name, err)
	}
	return normalized, nil
}

func (current *sourceTypeElaborator) compileInterface(
	declaration InterfaceDecl,
	stack []string,
) (gorapide.RapideType, error) {
	key := folded(declaration.Name)
	return gorapide.NewSelfRecursiveRapideInterfaceType(func(self gorapide.RapideType) (gorapide.RapideType, error) {
		current.inProgress[key] = self
		defer delete(current.inProgress, key)
		return current.compileInterfaceBody(declaration, stack)
	})
}

func (current *sourceTypeElaborator) compileUnion(
	declaration UnionDecl,
	stack []string,
) (gorapide.RapideType, error) {
	members := make([]gorapide.RapideUnionMember, len(declaration.Tags))
	for index, tag := range declaration.Tags {
		typ, err := current.resolveTypeExpression(
			declaredTypeExpression(tag.Position, tag.Type, tag.TypeExpression), stack,
		)
		if err != nil {
			return gorapide.RapideType{}, err
		}
		members[index] = gorapide.RapideUnionTag(tag.Name, typ)
	}
	result, err := gorapide.NewRapideUnionType(members...)
	if err != nil {
		return gorapide.RapideType{}, typeError(declaration.Position,
			"Union %q structural function reduction: %v", declaration.Name, err)
	}
	return result, nil
}

func (current *sourceTypeElaborator) compileEnumeration(
	declaration EnumerationDecl,
) (gorapide.RapideType, error) {
	literals := make([]string, len(declaration.Literals))
	for index, literal := range declaration.Literals {
		literals[index] = literal.Name
	}
	result, err := gorapide.NewRapideEnumerationType(literals...)
	if err != nil {
		return gorapide.RapideType{}, typeError(declaration.Position,
			"Enumeration %q structural Union reduction: %v", declaration.Name, err)
	}
	return result, nil
}

func (current *sourceTypeElaborator) compileInterfaceBody(
	declaration InterfaceDecl,
	stack []string,
) (gorapide.RapideType, error) {
	members := make([]gorapide.RapideInterfaceMember, 0,
		len(declaration.Actions)+len(declaration.Exceptions)+len(declaration.Functions)+len(declaration.ModuleGenerators)+
			len(declaration.Services)+len(declaration.Objects)+len(declaration.TypeNames)+len(declaration.TypeConstructors))
	localTypeNames := make(map[string]bool, len(declaration.TypeNames))
	for _, typeName := range declaration.TypeNames {
		localTypeNames[folded(typeName.Name)] = true
	}
	localConstructorNames := make(map[string]bool, len(declaration.TypeConstructors))
	for _, constructor := range declaration.TypeConstructors {
		localConstructorNames[folded(constructor.Name)] = true
	}
	resolve := func(position Position, name string) (gorapide.RapideType, error) {
		if localTypeNames[folded(name)] {
			reference, err := gorapide.NewRapideTypeNameReference(name)
			if err != nil {
				return gorapide.RapideType{}, typeError(position,
					"local type-name reference %q: %v", name, err)
			}
			return reference, nil
		}
		if localConstructorNames[folded(name)] {
			return gorapide.RapideType{}, typeError(position,
				"reference to local type-constructor constituent %q requires an application and symbolic structural type graphs", name)
		}
		return current.resolveNamed(position, name, stack)
	}
	resolveExpression := func(
		position Position,
		spelling string,
		expression TypeExpressionDecl,
	) (gorapide.RapideType, error) {
		return elaborateSourceTypeExpression(
			declaredTypeExpression(position, spelling, expression), resolve,
		)
	}
	for _, action := range declaration.Actions {
		if action.Mode == ActionPrivate {
			continue
		}
		parameters := make([]gorapide.RapideEventParameter, len(action.Parameters))
		for index, parameter := range action.Parameters {
			typ, err := resolveExpression(parameter.Position, parameter.Type, parameter.TypeExpression)
			if err != nil {
				return gorapide.RapideType{}, err
			}
			parameters[index] = gorapide.RapideEventParam(parameter.Name, typ)
		}
		eventType, err := gorapide.NewRapideEventType(parameters...)
		if err != nil {
			return gorapide.RapideType{}, typeError(action.Position,
				"action %q structural event type: %v", action.Name, err)
		}
		if action.Mode == ActionIn {
			members = append(members, gorapide.InputRapideAction(action.Name, eventType))
		} else if action.Mode == ActionOut {
			members = append(members, gorapide.OutputRapideAction(action.Name, eventType))
		} else {
			return gorapide.RapideType{}, typeError(action.Position, "action %q has unsupported mode %q", action.Name, action.Mode)
		}
	}
	for _, exception := range declaration.Exceptions {
		if !exception.Constituent {
			continue
		}
		parameters := make([]gorapide.RapideEventParameter, len(exception.Parameters))
		for index, parameter := range exception.Parameters {
			typ, err := resolveExpression(parameter.Position, parameter.Type, parameter.TypeExpression)
			if err != nil {
				return gorapide.RapideType{}, err
			}
			parameters[index] = gorapide.RapideEventParam(parameter.Name, typ)
		}
		eventType, err := gorapide.NewRapideEventType(parameters...)
		if err != nil {
			return gorapide.RapideType{}, typeError(exception.Position,
				"exception %q structural event type: %v", exception.Name, err)
		}
		switch exception.Region {
		case InterfaceNameProvides:
			members = append(members, gorapide.ProvidedRapideException(exception.Name, eventType))
		case InterfaceNamePrivate:
			members = append(members, gorapide.PrivateRapideException(exception.Name, eventType))
		case InterfaceNameRequires:
			members = append(members, gorapide.RequiredRapideException(exception.Name, eventType))
		default:
			return gorapide.RapideType{}, typeError(exception.Position,
				"interface exception %q has unsupported structural region %q", exception.Name, exception.Region)
		}
	}
	for _, function := range declaration.Functions {
		parameters := make([]gorapide.RapideFunctionParameter, len(function.Parameters))
		for index, parameter := range function.Parameters {
			typ, err := resolveExpression(parameter.Position, parameter.Type, parameter.TypeExpression)
			if err != nil {
				return gorapide.RapideType{}, err
			}
			if parameter.Default != nil {
				parameters[index] = gorapide.DefaultedRapideObjectParameter(parameter.Name, typ)
			} else {
				parameters[index] = gorapide.RapideObjectParameter(parameter.Name, typ)
			}
		}
		var resultType gorapide.RapideType
		if function.ReturnType != "" {
			var err error
			resultType, err = resolveExpression(
				function.Position, function.ReturnType, function.ReturnTypeExpression,
			)
			if err != nil {
				return gorapide.RapideType{}, err
			}
		}
		functionType, err := gorapide.NewRapideFunctionType(parameters, resultType)
		if err != nil {
			return gorapide.RapideType{}, typeError(function.Position,
				"function %q structural type: %v", function.Name, err)
		}
		switch function.Mode {
		case FunctionProvides:
			members = append(members, gorapide.ProvidedRapideMember(function.Name, functionType))
		case FunctionRequires:
			members = append(members, gorapide.RequiredRapideMember(function.Name, functionType))
		case FunctionPrivate:
			members = append(members, gorapide.PrivateRapideMember(function.Name, functionType))
		default:
			return gorapide.RapideType{}, typeError(function.Position,
				"function %q has unsupported structural region %q", function.Name, function.Mode)
		}
	}
	for _, generator := range declaration.ModuleGenerators {
		parameters := make([]gorapide.RapideFunctionParameter, len(generator.Parameters))
		formalNames := make(map[string]bool, len(generator.Parameters))
		for _, parameter := range generator.Parameters {
			parameterKey := folded(parameter.Name)
			if formalNames[parameterKey] {
				return gorapide.RapideType{}, typeError(parameter.Position,
					"duplicate formal parameter %q on module-generator name %q", parameter.Name, generator.Name)
			}
			formalNames[parameterKey] = true
		}
		for index, parameter := range generator.Parameters {
			switch parameter.Kind {
			case InterfaceFormalTypeParameter:
				if parameter.Type == "" {
					parameters[index] = gorapide.RapideTypeParameter(parameter.Name)
					continue
				}
				expression := declaredTypeExpression(parameter.Position, parameter.Type, parameter.TypeExpression)
				if typeExpressionReferencesAny(expression, formalNames) {
					return gorapide.RapideType{}, typeError(parameter.Position,
						"formal type-parameter bound %q on module-generator name %q requires symbolic parameter references",
						parameter.Type, generator.Name)
				}
				bound, err := resolveExpression(parameter.Position, parameter.Type, parameter.TypeExpression)
				if err != nil {
					return gorapide.RapideType{}, err
				}
				parameters[index] = gorapide.BoundedRapideTypeParameter(parameter.Name, bound)
			case InterfaceFormalObjectParameter:
				expression := declaredTypeExpression(parameter.Position, parameter.Type, parameter.TypeExpression)
				if typeExpressionReferencesAny(expression, formalNames) {
					return gorapide.RapideType{}, typeError(parameter.Position,
						"formal object-parameter type %q on module-generator name %q requires symbolic parameter references",
						parameter.Type, generator.Name)
				}
				typ, err := resolveExpression(parameter.Position, parameter.Type, parameter.TypeExpression)
				if err != nil {
					return gorapide.RapideType{}, err
				}
				parameters[index] = gorapide.RapideObjectParameter(parameter.Name, typ)
			default:
				return gorapide.RapideType{}, typeError(parameter.Position,
					"module-generator name %q has unsupported formal parameter kind %q", generator.Name, parameter.Kind)
			}
		}
		resultExpression := declaredTypeExpression(
			generator.Position, generator.ReturnType, generator.ReturnTypeExpression,
		)
		if typeExpressionReferencesAny(resultExpression, formalNames) {
			return gorapide.RapideType{}, typeError(generator.Position,
				"module-generator return expression %q requires symbolic parameter substitution", generator.ReturnType)
		}
		resultType, err := resolveExpression(
			generator.Position, generator.ReturnType, generator.ReturnTypeExpression,
		)
		if err != nil {
			return gorapide.RapideType{}, err
		}
		signature, err := gorapide.NewRapideFunctionType(parameters, resultType)
		if err != nil {
			return gorapide.RapideType{}, typeError(generator.Position,
				"module-generator name %q structural signature: %v", generator.Name, err)
		}
		var member gorapide.RapideInterfaceMember
		switch generator.Region {
		case InterfaceNameProvides:
			member = gorapide.ProvidedRapideModuleGenerator(generator.Name, signature)
		case InterfaceNameRequires:
			member = gorapide.RequiredRapideModuleGenerator(generator.Name, signature)
		case InterfaceNamePrivate:
			member = gorapide.PrivateRapideModuleGenerator(generator.Name, signature)
		default:
			return gorapide.RapideType{}, typeError(generator.Position,
				"module-generator name %q has unsupported interface region %q", generator.Name, generator.Region)
		}
		members = append(members, member)
	}
	for _, service := range declaration.Services {
		expression := declaredTypeExpression(service.Position, service.Type, service.TypeExpression)
		if target, named := typeExpressionNamedTarget(expression); named {
			_, targetDeclaration, err := current.interfaceDeclaration(service.Position, target)
			if err == nil {
				for _, action := range targetDeclaration.Actions {
					if action.Mode == ActionPrivate {
						return gorapide.RapideType{}, typeError(service.Position,
							"service %q target %q contains a forbidden private action %q",
							service.Name, targetDeclaration.Name, action.Name)
					}
				}
			}
		}
		targetType, err := resolveExpression(service.Position, service.Type, service.TypeExpression)
		if err != nil {
			return gorapide.RapideType{}, err
		}
		var expanded []gorapide.RapideInterfaceMember
		if service.IntegerSet && service.Dual {
			expanded, err = gorapide.RapideDualIntegerServiceSetInterfaceMembers(
				service.Name, service.FirstIndex, service.LastIndex, targetType,
			)
		} else if service.IntegerSet {
			expanded, err = gorapide.RapideIntegerServiceSetInterfaceMembers(
				service.Name, service.FirstIndex, service.LastIndex, targetType,
			)
		} else if service.Dual {
			expanded, err = gorapide.RapideDualServiceInterfaceMembers(service.Name, targetType)
		} else {
			expanded, err = gorapide.RapideServiceInterfaceMembers(service.Name, targetType)
		}
		if err != nil {
			return gorapide.RapideType{}, typeError(service.Position,
				"service %q structural rewrite: %v", service.Name, err)
		}
		members = append(members, expanded...)
	}
	for _, object := range declaration.Objects {
		typ, err := resolveExpression(object.Position, object.Type, object.TypeExpression)
		if err != nil {
			return gorapide.RapideType{}, err
		}
		switch object.Region {
		case InterfaceNameProvides:
			members = append(members, gorapide.ProvidedRapideMember(object.Name, typ))
		case InterfaceNameRequires:
			members = append(members, gorapide.RequiredRapideMember(object.Name, typ))
		case InterfaceNamePrivate:
			members = append(members, gorapide.PrivateRapideMember(object.Name, typ))
		default:
			return gorapide.RapideType{}, typeError(object.Position,
				"object %q has unsupported interface region %q", object.Name, object.Region)
		}
	}
	for _, typeName := range declaration.TypeNames {
		var constrained gorapide.RapideType
		if typeName.Specification != InterfaceTypeNameAny {
			var err error
			constrained, err = resolveExpression(typeName.Position, typeName.Type, typeName.TypeExpression)
			if err != nil {
				return gorapide.RapideType{}, err
			}
		}
		var member gorapide.RapideInterfaceMember
		switch {
		case typeName.Region == InterfaceNameProvides && typeName.Specification == InterfaceTypeNameAny:
			member = gorapide.UnboundedProvidedRapideTypeName(typeName.Name)
		case typeName.Region == InterfaceNameProvides && typeName.Specification == InterfaceTypeNameSubtype:
			member = gorapide.BoundedProvidedRapideTypeName(typeName.Name, constrained)
		case typeName.Region == InterfaceNameProvides && typeName.Specification == InterfaceTypeNameExact:
			member = gorapide.ExactProvidedRapideTypeName(typeName.Name, constrained)
		case typeName.Region == InterfaceNamePrivate && typeName.Specification == InterfaceTypeNameAny:
			member = gorapide.UnboundedPrivateRapideTypeName(typeName.Name)
		case typeName.Region == InterfaceNamePrivate && typeName.Specification == InterfaceTypeNameSubtype:
			member = gorapide.BoundedPrivateRapideTypeName(typeName.Name, constrained)
		case typeName.Region == InterfaceNamePrivate && typeName.Specification == InterfaceTypeNameExact:
			member = gorapide.ExactPrivateRapideTypeName(typeName.Name, constrained)
		default:
			return gorapide.RapideType{}, typeError(typeName.Position,
				"type-name %q has unsupported region/specification %q/%q", typeName.Name, typeName.Region, typeName.Specification)
		}
		members = append(members, member)
	}
	for _, constructor := range declaration.TypeConstructors {
		formalNames := make(map[string]bool, len(constructor.Parameters))
		for _, parameter := range constructor.Parameters {
			parameterKey := folded(parameter.Name)
			if formalNames[parameterKey] {
				return gorapide.RapideType{}, typeError(parameter.Position,
					"duplicate formal parameter %q on type constructor %q", parameter.Name, constructor.Name)
			}
			formalNames[parameterKey] = true
		}
		parameters := make([]gorapide.RapideFunctionParameter, len(constructor.Parameters))
		for index, parameter := range constructor.Parameters {
			switch parameter.Kind {
			case InterfaceFormalTypeParameter:
				if parameter.Type == "" {
					parameters[index] = gorapide.RapideTypeParameter(parameter.Name)
					continue
				}
				parameterExpression := declaredTypeExpression(
					parameter.Position, parameter.Type, parameter.TypeExpression,
				)
				if typeExpressionReferencesAny(parameterExpression, formalNames) {
					return gorapide.RapideType{}, typeError(parameter.Position,
						"formal type-parameter bound %q on constructor %q requires symbolic parameter references", parameter.Type, constructor.Name)
				}
				bound, err := resolveExpression(parameter.Position, parameter.Type, parameter.TypeExpression)
				if err != nil {
					return gorapide.RapideType{}, err
				}
				parameters[index] = gorapide.BoundedRapideTypeParameter(parameter.Name, bound)
			case InterfaceFormalObjectParameter:
				parameterExpression := declaredTypeExpression(
					parameter.Position, parameter.Type, parameter.TypeExpression,
				)
				if typeExpressionReferencesAny(parameterExpression, formalNames) {
					return gorapide.RapideType{}, typeError(parameter.Position,
						"formal object-parameter type %q on constructor %q requires symbolic parameter references", parameter.Type, constructor.Name)
				}
				typ, err := resolveExpression(parameter.Position, parameter.Type, parameter.TypeExpression)
				if err != nil {
					return gorapide.RapideType{}, err
				}
				parameters[index] = gorapide.RapideObjectParameter(parameter.Name, typ)
			default:
				return gorapide.RapideType{}, typeError(parameter.Position,
					"type constructor %q has unsupported formal parameter kind %q", constructor.Name, parameter.Kind)
			}
		}
		var constrained gorapide.RapideType
		if constructor.Specification != InterfaceTypeNameAny {
			constructorExpression := declaredTypeExpression(
				constructor.Position, constructor.Type, constructor.TypeExpression,
			)
			if typeExpressionReferencesAny(constructorExpression, formalNames) {
				return gorapide.RapideType{}, typeError(constructor.Position,
					"type-constructor result expression %q requires symbolic parameter substitution", constructor.Type)
			}
			var err error
			constrained, err = resolveExpression(
				constructor.Position, constructor.Type, constructor.TypeExpression,
			)
			if err != nil {
				return gorapide.RapideType{}, err
			}
		}
		var member gorapide.RapideInterfaceMember
		switch {
		case constructor.Region == InterfaceNameProvides && constructor.Specification == InterfaceTypeNameAny:
			member = gorapide.UnboundedProvidedRapideTypeConstructor(constructor.Name, parameters...)
		case constructor.Region == InterfaceNameProvides && constructor.Specification == InterfaceTypeNameSubtype:
			member = gorapide.BoundedProvidedRapideTypeConstructor(constructor.Name, constrained, parameters...)
		case constructor.Region == InterfaceNameProvides && constructor.Specification == InterfaceTypeNameExact:
			member = gorapide.ExactProvidedRapideTypeConstructor(constructor.Name, constrained, parameters...)
		case constructor.Region == InterfaceNamePrivate && constructor.Specification == InterfaceTypeNameAny:
			member = gorapide.UnboundedPrivateRapideTypeConstructor(constructor.Name, parameters...)
		case constructor.Region == InterfaceNamePrivate && constructor.Specification == InterfaceTypeNameSubtype:
			member = gorapide.BoundedPrivateRapideTypeConstructor(constructor.Name, constrained, parameters...)
		case constructor.Region == InterfaceNamePrivate && constructor.Specification == InterfaceTypeNameExact:
			member = gorapide.ExactPrivateRapideTypeConstructor(constructor.Name, constrained, parameters...)
		default:
			return gorapide.RapideType{}, typeError(constructor.Position,
				"type-constructor %q has unsupported region/specification %q/%q", constructor.Name, constructor.Region, constructor.Specification)
		}
		members = append(members, member)
	}
	result, err := gorapide.NewRapideInterfaceType(members...)
	if err != nil {
		return gorapide.RapideType{}, typeError(declaration.Position,
			"interface %q structural type: %v", declaration.Name, err)
	}
	return result, nil
}

func structuralDirectPredefinedType(name string) (gorapide.RapideType, bool) {
	switch folded(name) {
	case "clock":
		typ, err := gorapide.RapideClockType()
		return typ, err == nil
	case "accuracy":
		typ, err := gorapide.RapideAccuracyType()
		return typ, err == nil
	case "gst":
		typ, err := gorapide.RapidePredefinedType("GST")
		return typ, err == nil
	}
	if _, ok := predefinedTypes[folded(name)]; !ok {
		return gorapide.RapideType{}, false
	}
	typ, err := gorapide.RapidePredefinedType(name)
	return typ, err == nil
}

func sourceTypeCyclePath(
	stack []string,
	key string,
	interfaces map[string]InterfaceDecl,
	aliases map[string]TypeAliasDecl,
	unions map[string]UnionDecl,
) string {
	start := 0
	for index, candidate := range stack {
		if candidate == key {
			start = index
			break
		}
	}
	result := make([]string, 0, len(stack)-start+1)
	for _, candidate := range stack[start:] {
		result = append(result, sourceTypeSpelling(candidate, interfaces, aliases, unions))
	}
	result = append(result, sourceTypeSpelling(key, interfaces, aliases, unions))
	return strings.Join(result, " -> ")
}

func sourceTypeSpelling(
	key string,
	interfaces map[string]InterfaceDecl,
	aliases map[string]TypeAliasDecl,
	unions map[string]UnionDecl,
) string {
	if declaration, ok := interfaces[key]; ok {
		return declaration.Name
	}
	if declaration, ok := aliases[key]; ok {
		return declaration.Name
	}
	if declaration, ok := unions[key]; ok {
		return declaration.Name
	}
	return key
}

package rapide

import (
	"fmt"
	"sort"
	"strings"
)

type interfaceNormalizationState uint8

const (
	interfaceUnvisited interfaceNormalizationState = iota
	interfaceVisiting
	interfaceNormalized
)

// normalizeInterfaceDeclarations implements the Stanford Type LRM's
// interface-derivation normalization step for the name-declaration regions
// represented by the current source subset. The returned declarations contain
// no derivations, so execution and artifact generation never perform dynamic
// inheritance lookup.
func normalizeInterfaceDeclarations(declarations []InterfaceDecl) (map[string]InterfaceDecl, error) {
	return normalizeInterfaceDeclarationsWithAliases(declarations, nil)
}

func normalizeInterfaceDeclarationsWithAliases(
	declarations []InterfaceDecl,
	typeAliases []TypeAliasDecl,
) (map[string]InterfaceDecl, error) {
	raw := make(map[string]InterfaceDecl, len(declarations))
	for _, declaration := range declarations {
		key := folded(declaration.Name)
		if key == "" {
			return nil, typeError(declaration.Position, "interface type has no name")
		}
		if previous, exists := raw[key]; exists {
			return nil, typeError(declaration.Position,
				"duplicate interface type %q (previous spelling %q)", declaration.Name, previous.Name)
		}
		raw[key] = declaration
	}
	aliases := make(map[string]TypeAliasDecl, len(typeAliases))
	for _, alias := range typeAliases {
		key := folded(alias.Name)
		if key == "" {
			return nil, typeError(alias.Position, "type alias has no name")
		}
		if canonical, predefined := predefinedTypes[key]; predefined {
			return nil, typeError(alias.Position,
				"type alias %q collides with predefined type %q in the current source profile", alias.Name, canonical)
		}
		if previous, exists := aliases[key]; exists {
			return nil, typeError(alias.Position,
				"duplicate type alias %q (previous spelling %q)", alias.Name, previous.Name)
		}
		if declaration, exists := raw[key]; exists {
			return nil, typeError(alias.Position,
				"type alias %q collides with interface type %q", alias.Name, declaration.Name)
		}
		aliases[key] = alias
	}

	resolveDerivationSource := func(position Position, owner, name string) (string, InterfaceDecl, error) {
		seen := make(map[string]bool)
		path := make([]string, 0)
		for {
			key := folded(name)
			if declaration, exists := raw[key]; exists {
				return key, declaration, nil
			}
			alias, exists := aliases[key]
			if !exists {
				if canonical, predefined := predefinedTypes[key]; predefined {
					return "", InterfaceDecl{}, typeError(position,
						"interface derivation source %q denotes predefined type %q, not an interface type", name, canonical)
				}
				return "", InterfaceDecl{}, typeError(position,
					"interface %q derives from undeclared interface type %q", owner, name)
			}
			if seen[key] {
				path = append(path, alias.Name)
				return "", InterfaceDecl{}, typeError(position,
					"interface derivation type alias cycle %s", strings.Join(path, " -> "))
			}
			seen[key] = true
			path = append(path, alias.Name)
			if alias.IntegerRange {
				return "", InterfaceDecl{}, typeError(position,
					"interface derivation source alias %q denotes a finite Integer range, not an interface type", alias.Name)
			}
			target, named := typeExpressionNamedTarget(typeAliasExpression(alias))
			if !named {
				return "", InterfaceDecl{}, typeError(position,
					"interface derivation source alias %q denotes structural application %s, not a named interface type",
					alias.Name, alias.Target)
			}
			name = target
		}
	}

	keys := make([]string, 0, len(raw))
	for key := range raw {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	result := make(map[string]InterfaceDecl, len(raw))
	states := make(map[string]interfaceNormalizationState, len(raw))
	var normalize func(string, []string) (InterfaceDecl, error)
	normalize = func(key string, stack []string) (InterfaceDecl, error) {
		if states[key] == interfaceNormalized {
			return result[key], nil
		}
		declaration := raw[key]
		states[key] = interfaceVisiting
		stack = append(stack, key)

		normalized := cloneInterfaceForNormalization(declaration)
		normalized.Derivations = nil
		for _, derivation := range declaration.Derivations {
			sourceKey, source, err := resolveDerivationSource(
				derivation.Position, declaration.Name, derivation.Source,
			)
			if err != nil {
				return InterfaceDecl{}, err
			}
			if declaration.Record && !source.Record {
				return InterfaceDecl{}, typeError(derivation.Position,
					"record type %q derives from non-record interface type %q", declaration.Name, source.Name)
			}
			if states[sourceKey] == interfaceVisiting {
				return InterfaceDecl{}, typeError(derivation.Position,
					"interface derivation cycle %s", interfaceCyclePath(stack, sourceKey, raw))
			}
			if states[sourceKey] != interfaceNormalized {
				var err error
				source, err = normalize(sourceKey, stack)
				if err != nil {
					return InterfaceDecl{}, err
				}
			} else {
				source = result[sourceKey]
			}
			actions, exceptions, functions, generators, services, objects, typeNames, typeConstructors, err := deriveInterfaceNames(source, derivation)
			if err != nil {
				return InterfaceDecl{}, err
			}
			normalized.Actions = append(normalized.Actions, actions...)
			normalized.Exceptions = append(normalized.Exceptions, exceptions...)
			normalized.Functions = append(normalized.Functions, functions...)
			normalized.ModuleGenerators = append(normalized.ModuleGenerators, generators...)
			normalized.Services = append(normalized.Services, services...)
			normalized.Objects = append(normalized.Objects, objects...)
			normalized.TypeNames = append(normalized.TypeNames, typeNames...)
			normalized.TypeConstructors = append(normalized.TypeConstructors, typeConstructors...)
		}
		sortNormalizedActions(normalized.Actions)
		for index := range normalized.Exceptions {
			if normalized.Exceptions[index].Constituent {
				normalized.Exceptions[index].Declaration = interfaceExceptionDeclarationIdentity(
					declaration.Name, normalized.Exceptions[index].Region, normalized.Exceptions[index].Name,
				)
			}
		}
		sortNormalizedFunctions(normalized.Functions)
		sortNormalizedModuleGenerators(normalized.ModuleGenerators)
		sortNormalizedServices(normalized.Services)
		sortNormalizedObjects(normalized.Objects)
		sortNormalizedTypeNames(normalized.TypeNames)
		sortNormalizedTypeConstructors(normalized.TypeConstructors)
		if err := validateNormalizedRecordDeclaration(normalized); err != nil {
			return InterfaceDecl{}, err
		}
		result[key] = normalized
		states[key] = interfaceNormalized
		return normalized, nil
	}

	for _, key := range keys {
		if _, err := normalize(key, nil); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func validateNormalizedRecordDeclaration(declaration InterfaceDecl) error {
	if !declaration.Record {
		return nil
	}
	if len(declaration.Actions) != 0 || len(interfaceConstituentExceptions(declaration.Exceptions)) != 0 || len(declaration.Functions) != 0 ||
		len(declaration.ModuleGenerators) != 0 || len(declaration.Services) != 0 ||
		len(declaration.TypeNames) != 0 || len(declaration.TypeConstructors) != 0 ||
		len(declaration.Constraints) != 0 || declaration.Behavior != nil {
		return typeError(declaration.Position,
			"record type %q contains a non-field interface declaration", declaration.Name)
	}
	seen := make(map[string]InterfaceObjectDecl, len(declaration.Objects))
	for _, field := range declaration.Objects {
		if field.Region != InterfaceNameProvides {
			return typeError(field.Position,
				"record field %q in %q is not an immutable provided field", field.Name, declaration.Name)
		}
		key := folded(field.Name)
		if previous, exists := seen[key]; exists {
			return typeError(field.Position,
				"duplicate record field %q in %q (previous spelling %q)", field.Name, declaration.Name, previous.Name)
		}
		seen[key] = field
	}
	return nil
}

func cloneInterfaceForNormalization(source InterfaceDecl) InterfaceDecl {
	result := source
	result.Derivations = append([]InterfaceDerivationDecl(nil), source.Derivations...)
	result.Actions = make([]ActionDecl, len(source.Actions))
	for index, action := range source.Actions {
		result.Actions[index] = cloneActionDecl(action)
	}
	result.Exceptions = make([]ExceptionDecl, len(source.Exceptions))
	for index, exception := range source.Exceptions {
		result.Exceptions[index] = exception
		result.Exceptions[index].Parameters = append([]ParameterDecl(nil), exception.Parameters...)
	}
	result.SelectedExceptions = make([]ExceptionDecl, len(source.SelectedExceptions))
	for index, exception := range source.SelectedExceptions {
		result.SelectedExceptions[index] = exception
		result.SelectedExceptions[index].Parameters = append([]ParameterDecl(nil), exception.Parameters...)
	}
	result.ExceptionScopes = make([]ExceptionScopeDecl, len(source.ExceptionScopes))
	for index, scope := range source.ExceptionScopes {
		result.ExceptionScopes[index] = ExceptionScopeDecl{
			Path:       append([]string(nil), scope.Path...),
			Exceptions: cloneExceptionDeclarations(scope.Exceptions),
		}
	}
	result.Functions = make([]FunctionDecl, len(source.Functions))
	for index, function := range source.Functions {
		result.Functions[index] = cloneFunctionDecl(function)
	}
	result.ModuleGenerators = make([]InterfaceModuleGeneratorDecl, len(source.ModuleGenerators))
	for index, generator := range source.ModuleGenerators {
		result.ModuleGenerators[index] = cloneInterfaceModuleGeneratorDecl(generator)
	}
	result.Services = append([]InterfaceServiceDecl(nil), source.Services...)
	result.Objects = append([]InterfaceObjectDecl(nil), source.Objects...)
	result.TypeNames = append([]InterfaceTypeNameDecl(nil), source.TypeNames...)
	result.TypeConstructors = make([]InterfaceTypeConstructorDecl, len(source.TypeConstructors))
	for index, constructor := range source.TypeConstructors {
		result.TypeConstructors[index] = constructor
		result.TypeConstructors[index].Parameters = append([]InterfaceFormalParameterDecl(nil), constructor.Parameters...)
	}
	result.Constraints = append([]ConstraintDecl(nil), source.Constraints...)
	return result
}

func cloneActionDecl(source ActionDecl) ActionDecl {
	result := source
	result.Parameters = append([]ParameterDecl(nil), source.Parameters...)
	return result
}

func cloneFunctionDecl(source FunctionDecl) FunctionDecl {
	result := source
	result.Parameters = append([]ParameterDecl(nil), source.Parameters...)
	return result
}

func cloneInterfaceModuleGeneratorDecl(source InterfaceModuleGeneratorDecl) InterfaceModuleGeneratorDecl {
	result := source
	result.Parameters = append([]InterfaceFormalParameterDecl(nil), source.Parameters...)
	return result
}

func deriveInterfaceNames(source InterfaceDecl, derivation InterfaceDerivationDecl) (
	[]ActionDecl,
	[]ExceptionDecl,
	[]FunctionDecl,
	[]InterfaceModuleGeneratorDecl,
	[]InterfaceServiceDecl,
	[]InterfaceObjectDecl,
	[]InterfaceTypeNameDecl,
	[]InterfaceTypeConstructorDecl,
	error,
) {
	if derivation.Region != InterfaceDerivationAll && derivation.Region != InterfaceDerivationProvides &&
		derivation.Region != InterfaceDerivationRequires && derivation.Region != InterfaceDerivationPrivate {
		return nil, nil, nil, nil, nil, nil, nil, nil, typeError(derivation.Position,
			"interface derivation from %q has unsupported declarative region %q", derivation.Source, derivation.Region)
	}
	if derivation.Modifier != InterfaceDerivationUnmodified && derivation.Modifier != InterfaceDerivationOnly &&
		derivation.Modifier != InterfaceDerivationExcept {
		return nil, nil, nil, nil, nil, nil, nil, nil, typeError(derivation.Position,
			"interface derivation from %q has unsupported modifier %q", derivation.Source, derivation.Modifier)
	}
	selectedNames := make(map[string]bool, len(derivation.Names))
	for _, name := range derivation.Names {
		key := folded(name)
		if key == "" || selectedNames[key] {
			return nil, nil, nil, nil, nil, nil, nil, nil, typeError(derivation.Position,
				"interface derivation from %q has an empty or duplicate modifier name %q", derivation.Source, name)
		}
		selectedNames[key] = true
	}
	if derivation.Modifier != InterfaceDerivationUnmodified && len(selectedNames) == 0 {
		return nil, nil, nil, nil, nil, nil, nil, nil, typeError(derivation.Position,
			"interface derivation from %q has an empty %s identifier list", derivation.Source, derivation.Modifier)
	}
	if derivation.Modifier == InterfaceDerivationUnmodified && len(selectedNames) != 0 {
		return nil, nil, nil, nil, nil, nil, nil, nil, typeError(derivation.Position,
			"unmodified interface derivation from %q cannot have selected names", derivation.Source)
	}

	actions := make([]ActionDecl, 0)
	exceptions := make([]ExceptionDecl, 0)
	functions := make([]FunctionDecl, 0)
	generators := make([]InterfaceModuleGeneratorDecl, 0)
	services := make([]InterfaceServiceDecl, 0)
	objects := make([]InterfaceObjectDecl, 0)
	typeNames := make([]InterfaceTypeNameDecl, 0)
	typeConstructors := make([]InterfaceTypeConstructorDecl, 0)
	if derivation.Region == InterfaceDerivationAll {
		for _, action := range source.Actions {
			if interfaceDerivationSelects(action.Name, derivation.Modifier, selectedNames) {
				actions = append(actions, cloneActionDecl(action))
			}
		}
	}
	for _, exception := range source.Exceptions {
		if !exception.Constituent || !interfaceDerivationContainsNameRegion(derivation.Region, exception.Region) ||
			!interfaceDerivationSelects(exception.Name, derivation.Modifier, selectedNames) {
			continue
		}
		copy := exception
		copy.Parameters = append([]ParameterDecl(nil), exception.Parameters...)
		exceptions = append(exceptions, copy)
	}
	for _, function := range source.Functions {
		inRegion := derivation.Region == InterfaceDerivationAll ||
			(derivation.Region == InterfaceDerivationProvides && function.Mode == FunctionProvides) ||
			(derivation.Region == InterfaceDerivationRequires && function.Mode == FunctionRequires) ||
			(derivation.Region == InterfaceDerivationPrivate && function.Mode == FunctionPrivate)
		if inRegion && interfaceDerivationSelects(function.Name, derivation.Modifier, selectedNames) {
			functions = append(functions, cloneFunctionDecl(function))
		}
	}
	for _, generator := range source.ModuleGenerators {
		if interfaceDerivationContainsNameRegion(derivation.Region, generator.Region) &&
			interfaceDerivationSelects(generator.Name, derivation.Modifier, selectedNames) {
			generators = append(generators, cloneInterfaceModuleGeneratorDecl(generator))
		}
	}
	if derivation.Region == InterfaceDerivationAll {
		for _, service := range source.Services {
			if interfaceDerivationSelects(service.Name, derivation.Modifier, selectedNames) {
				services = append(services, service)
			}
		}
	}
	for _, object := range source.Objects {
		if interfaceDerivationContainsNameRegion(derivation.Region, object.Region) &&
			interfaceDerivationSelects(object.Name, derivation.Modifier, selectedNames) {
			objects = append(objects, object)
		}
	}
	for _, typeName := range source.TypeNames {
		if interfaceDerivationContainsNameRegion(derivation.Region, typeName.Region) &&
			interfaceDerivationSelects(typeName.Name, derivation.Modifier, selectedNames) {
			typeNames = append(typeNames, typeName)
		}
	}
	for _, constructor := range source.TypeConstructors {
		if interfaceDerivationContainsNameRegion(derivation.Region, constructor.Region) &&
			interfaceDerivationSelects(constructor.Name, derivation.Modifier, selectedNames) {
			copy := constructor
			copy.Parameters = append([]InterfaceFormalParameterDecl(nil), constructor.Parameters...)
			typeConstructors = append(typeConstructors, copy)
		}
	}

	introduced := make(map[string]bool, len(actions)+len(exceptions)+len(functions)+len(generators)+len(services)+len(objects)+len(typeNames)+len(typeConstructors))
	for _, action := range actions {
		introduced[folded(action.Name)] = true
	}
	for _, exception := range exceptions {
		introduced[folded(exception.Name)] = true
	}
	for _, function := range functions {
		introduced[folded(function.Name)] = true
	}
	for _, generator := range generators {
		introduced[folded(generator.Name)] = true
	}
	for _, service := range services {
		introduced[folded(service.Name)] = true
	}
	for _, object := range objects {
		introduced[folded(object.Name)] = true
	}
	for _, typeName := range typeNames {
		introduced[folded(typeName.Name)] = true
	}
	for _, constructor := range typeConstructors {
		introduced[folded(constructor.Name)] = true
	}
	replacements := make(map[string]string, len(derivation.Replacements))
	replacementSources := make(map[string]bool, len(derivation.Replacements))
	for _, replacement := range derivation.Replacements {
		key := folded(replacement.From)
		if key == "" || folded(replacement.To) == "" || replacementSources[key] {
			return nil, nil, nil, nil, nil, nil, nil, nil, typeError(replacement.Position,
				"interface derivation replacement %q to %q has an empty name or repeated source", replacement.From, replacement.To)
		}
		replacementSources[key] = true
		if introduced[key] {
			replacements[key] = replacement.To
		}
	}
	for index := range actions {
		relocateAndReplaceAction(&actions[index], derivation.Position, replacements)
	}
	for index := range exceptions {
		exception := &exceptions[index]
		exception.Position = derivation.Position
		if replacement := replacements[folded(exception.Name)]; replacement != "" {
			exception.Name = replacement
		}
		for parameterIndex := range exception.Parameters {
			exception.Parameters[parameterIndex].Position = derivation.Position
			parameter := &exception.Parameters[parameterIndex]
			parameter.Type, parameter.TypeExpression = relocateAndReplaceTypeExpression(
				derivation.Position, parameter.Type, parameter.TypeExpression, replacements,
			)
		}
	}
	for index := range functions {
		relocateAndReplaceFunction(&functions[index], derivation.Position, replacements)
	}
	for index := range generators {
		generator := &generators[index]
		generator.Position = derivation.Position
		if replacement := replacements[folded(generator.Name)]; replacement != "" {
			generator.Name = replacement
		}
		for parameterIndex := range generator.Parameters {
			parameter := &generator.Parameters[parameterIndex]
			parameter.Position = derivation.Position
			parameter.Type, parameter.TypeExpression = relocateAndReplaceTypeExpression(
				derivation.Position, parameter.Type, parameter.TypeExpression, replacements,
			)
		}
		generator.ReturnType, generator.ReturnTypeExpression = relocateAndReplaceTypeExpression(
			derivation.Position, generator.ReturnType, generator.ReturnTypeExpression, replacements,
		)
	}
	for index := range services {
		service := &services[index]
		service.Position = derivation.Position
		if replacement := replacements[folded(service.Name)]; replacement != "" {
			service.Name = replacement
		}
		service.Type, service.TypeExpression = relocateAndReplaceTypeExpression(
			derivation.Position, service.Type, service.TypeExpression, replacements,
		)
	}
	for index := range objects {
		objects[index].Position = derivation.Position
		if replacement := replacements[folded(objects[index].Name)]; replacement != "" {
			objects[index].Name = replacement
		}
		objects[index].Type, objects[index].TypeExpression = relocateAndReplaceTypeExpression(
			derivation.Position, objects[index].Type, objects[index].TypeExpression, replacements,
		)
	}
	for index := range typeNames {
		typeNames[index].Position = derivation.Position
		if replacement := replacements[folded(typeNames[index].Name)]; replacement != "" {
			typeNames[index].Name = replacement
		}
		typeNames[index].Type, typeNames[index].TypeExpression = relocateAndReplaceTypeExpression(
			derivation.Position, typeNames[index].Type, typeNames[index].TypeExpression, replacements,
		)
	}
	for index := range typeConstructors {
		typeConstructors[index].Position = derivation.Position
		if replacement := replacements[folded(typeConstructors[index].Name)]; replacement != "" {
			typeConstructors[index].Name = replacement
		}
		typeConstructors[index].Type, typeConstructors[index].TypeExpression = relocateAndReplaceTypeExpression(
			derivation.Position, typeConstructors[index].Type,
			typeConstructors[index].TypeExpression, replacements,
		)
		for parameterIndex := range typeConstructors[index].Parameters {
			typeConstructors[index].Parameters[parameterIndex].Position = derivation.Position
			parameter := &typeConstructors[index].Parameters[parameterIndex]
			parameter.Type, parameter.TypeExpression = relocateAndReplaceTypeExpression(
				derivation.Position, parameter.Type, parameter.TypeExpression, replacements,
			)
		}
	}
	return actions, exceptions, functions, generators, services, objects, typeNames, typeConstructors, nil
}

func interfaceDerivationContainsNameRegion(derivation InterfaceDerivationRegion, region InterfaceNameRegion) bool {
	return derivation == InterfaceDerivationAll ||
		derivation == InterfaceDerivationProvides && region == InterfaceNameProvides ||
		derivation == InterfaceDerivationRequires && region == InterfaceNameRequires ||
		derivation == InterfaceDerivationPrivate && region == InterfaceNamePrivate
}

func interfaceDerivationSelects(name string, modifier InterfaceDerivationModifier, names map[string]bool) bool {
	present := names[folded(name)]
	switch modifier {
	case InterfaceDerivationOnly:
		return present
	case InterfaceDerivationExcept:
		return !present
	default:
		return true
	}
}

func relocateAndReplaceAction(action *ActionDecl, position Position, replacements map[string]string) {
	action.Position = position
	if replacement := replacements[folded(action.Name)]; replacement != "" {
		action.Name = replacement
	}
	for index := range action.Parameters {
		action.Parameters[index].Position = position
		parameter := &action.Parameters[index]
		parameter.Type, parameter.TypeExpression = relocateAndReplaceTypeExpression(
			position, parameter.Type, parameter.TypeExpression, replacements,
		)
	}
}

func relocateAndReplaceFunction(function *FunctionDecl, position Position, replacements map[string]string) {
	function.Position = position
	if replacement := replacements[folded(function.Name)]; replacement != "" {
		function.Name = replacement
	}
	for index := range function.Parameters {
		function.Parameters[index].Position = position
		parameter := &function.Parameters[index]
		parameter.Type, parameter.TypeExpression = relocateAndReplaceTypeExpression(
			position, parameter.Type, parameter.TypeExpression, replacements,
		)
	}
	function.ReturnType, function.ReturnTypeExpression = relocateAndReplaceTypeExpression(
		position, function.ReturnType, function.ReturnTypeExpression, replacements,
	)
}

func relocateAndReplaceTypeExpression(
	position Position,
	spelling string,
	expression TypeExpressionDecl,
	replacements map[string]string,
) (string, TypeExpressionDecl) {
	if expression.Kind == "" {
		if replacement := replacements[folded(spelling)]; replacement != "" {
			spelling = replacement
		}
		return spelling, expression
	}
	result := TypeExpressionDecl{
		Position: position,
		Kind:     expression.Kind,
		Name:     expression.Name,
	}
	if replacement := replacements[folded(result.Name)]; replacement != "" {
		result.Name = replacement
	}
	result.Arguments = make([]TypeExpressionDecl, len(expression.Arguments))
	for index, argument := range expression.Arguments {
		_, result.Arguments[index] = relocateAndReplaceTypeExpression(
			position, typeExpressionSpelling(argument), argument, replacements,
		)
	}
	return typeExpressionSpelling(result), result
}

func interfaceCyclePath(stack []string, sourceKey string, declarations map[string]InterfaceDecl) string {
	start := 0
	for index, key := range stack {
		if key == sourceKey {
			start = index
			break
		}
	}
	path := make([]string, 0, len(stack)-start+1)
	for _, key := range stack[start:] {
		path = append(path, declarations[key].Name)
	}
	path = append(path, declarations[sourceKey].Name)
	return strings.Join(path, " -> ")
}

func sortNormalizedActions(actions []ActionDecl) {
	sort.SliceStable(actions, func(left, right int) bool {
		if comparison := comparePosition(actions[left].Position, actions[right].Position); comparison != 0 {
			return comparison < 0
		}
		return actionDeclarationKey(actions[left]) < actionDeclarationKey(actions[right])
	})
}

func sortNormalizedFunctions(functions []FunctionDecl) {
	sort.SliceStable(functions, func(left, right int) bool {
		if comparison := comparePosition(functions[left].Position, functions[right].Position); comparison != 0 {
			return comparison < 0
		}
		return functionDeclarationKey(functions[left]) < functionDeclarationKey(functions[right])
	})
}

func sortNormalizedModuleGenerators(generators []InterfaceModuleGeneratorDecl) {
	sort.SliceStable(generators, func(left, right int) bool {
		if comparison := comparePosition(generators[left].Position, generators[right].Position); comparison != 0 {
			return comparison < 0
		}
		return moduleGeneratorDeclarationKey(generators[left]) < moduleGeneratorDeclarationKey(generators[right])
	})
}

func sortNormalizedServices(services []InterfaceServiceDecl) {
	sort.SliceStable(services, func(left, right int) bool {
		if comparison := comparePosition(services[left].Position, services[right].Position); comparison != 0 {
			return comparison < 0
		}
		return serviceDeclarationKey(services[left]) < serviceDeclarationKey(services[right])
	})
}

func sortNormalizedObjects(objects []InterfaceObjectDecl) {
	sort.SliceStable(objects, func(left, right int) bool {
		if comparison := comparePosition(objects[left].Position, objects[right].Position); comparison != 0 {
			return comparison < 0
		}
		return objectDeclarationKey(objects[left]) < objectDeclarationKey(objects[right])
	})
}

func sortNormalizedTypeNames(typeNames []InterfaceTypeNameDecl) {
	sort.SliceStable(typeNames, func(left, right int) bool {
		if comparison := comparePosition(typeNames[left].Position, typeNames[right].Position); comparison != 0 {
			return comparison < 0
		}
		return typeNameDeclarationKey(typeNames[left]) < typeNameDeclarationKey(typeNames[right])
	})
}

func sortNormalizedTypeConstructors(constructors []InterfaceTypeConstructorDecl) {
	sort.SliceStable(constructors, func(left, right int) bool {
		if comparison := comparePosition(constructors[left].Position, constructors[right].Position); comparison != 0 {
			return comparison < 0
		}
		return typeConstructorDeclarationKey(constructors[left]) < typeConstructorDeclarationKey(constructors[right])
	})
}

func comparePosition(left, right Position) int {
	if left.Offset != right.Offset {
		if left.Offset < right.Offset {
			return -1
		}
		return 1
	}
	if left.Line != right.Line {
		if left.Line < right.Line {
			return -1
		}
		return 1
	}
	if left.Column < right.Column {
		return -1
	}
	if left.Column > right.Column {
		return 1
	}
	return 0
}

func actionDeclarationKey(action ActionDecl) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "%s:%s", action.Mode, folded(action.Name))
	for _, parameter := range action.Parameters {
		fmt.Fprintf(&builder, "|%s:%s", folded(parameter.Name), folded(parameter.Type))
	}
	return builder.String()
}

func functionDeclarationKey(function FunctionDecl) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "%s:%s", function.Mode, folded(function.Name))
	for _, parameter := range function.Parameters {
		fmt.Fprintf(&builder, "|%s:%s", folded(parameter.Name), folded(parameter.Type))
	}
	fmt.Fprintf(&builder, "->%s", folded(function.ReturnType))
	return builder.String()
}

func moduleGeneratorDeclarationKey(generator InterfaceModuleGeneratorDecl) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "%s:%s", generator.Region, folded(generator.Name))
	for _, parameter := range generator.Parameters {
		fmt.Fprintf(&builder, "|%s:%s:%s", parameter.Kind, folded(parameter.Name), folded(parameter.Type))
	}
	fmt.Fprintf(&builder, "->%s", folded(generator.ReturnType))
	return builder.String()
}

func serviceDeclarationKey(service InterfaceServiceDecl) string {
	return fmt.Sprintf("%t:%t:%d:%d:%s:%s", service.Dual, service.IntegerSet,
		service.FirstIndex, service.LastIndex, folded(service.Name), folded(service.Type))
}

func objectDeclarationKey(object InterfaceObjectDecl) string {
	return string(object.Region) + ":" + folded(object.Name) + ":" + folded(object.Type)
}

func typeNameDeclarationKey(typeName InterfaceTypeNameDecl) string {
	return string(typeName.Region) + ":" + folded(typeName.Name) + ":" +
		string(typeName.Specification) + ":" + folded(typeName.Type)
}

func typeConstructorDeclarationKey(constructor InterfaceTypeConstructorDecl) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "%s:%s:%s:%s", constructor.Region, folded(constructor.Name), constructor.Specification, folded(constructor.Type))
	for _, parameter := range constructor.Parameters {
		fmt.Fprintf(&builder, "|%s:%s:%s", parameter.Kind, folded(parameter.Name), folded(parameter.Type))
	}
	return builder.String()
}

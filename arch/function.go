package arch

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/pattern"
)

var ErrInvalidFunctionImplementation = errors.New("invalid declarative Rapide function implementation")

// FunctionImplementation is the initial closed implementation subset for one
// local provided function. Statements execute sequentially. Return is nil for
// a function with no returned object; typed functions require one tail return
// expression so the executable kernel never invents Rapide's otherwise
// arbitrary fall-through value.
type FunctionImplementation struct {
	Name       string
	Params     []ParamDecl
	ReturnType string
	Locals     []FunctionModuleLocal
	Statements []Statement
	Return     *RuleValue

	key              string
	targetComponent  string
	targetName       string
	targetParams     []ParamDecl
	connectionID     string
	connectionScopes []ConnectionScope
	routeAliases     []functionRouteAlias
}

// FunctionModuleLocal is one declaration-ordered function-local module name.
// The first supported initializer is the enclosing module's implicit New
// allocator; Value remains explicit so canonical model identity does not hide
// the allocation expression behind a host constructor.
type FunctionModuleLocal struct {
	Name  string
	Value RuleValue
}

// ModuleLocal constructs one function-local module name and initializer.
func ModuleLocal(name string, value RuleValue) FunctionModuleLocal {
	return FunctionModuleLocal{Name: name, Value: copyRuleValue(value)}
}

// functionRouteAlias is one additional function observation on a static
// synchronous alias path. The first function in a call remains the local
// callable declaration; routeAliases records every subsequent architecture
// boundary and final provider in semantic path order.
type functionRouteAlias struct {
	ComponentID string
	Name        string
	Params      []ParamDecl
}

// FunctionImplementationBuilder constructs a closed local function body.
type FunctionImplementationBuilder struct {
	implementation FunctionImplementation
}

// Function begins an implementation whose signature must exactly match one
// provided function in the component interface.
func Function(name, returnType string, params ...ParamDecl) *FunctionImplementationBuilder {
	return &FunctionImplementationBuilder{implementation: FunctionImplementation{
		Name: name, ReturnType: returnType, Params: append([]ParamDecl(nil), params...),
	}}
}

// Do replaces the sequential body of the function.
func (builder *FunctionImplementationBuilder) Do(statements ...Statement) *FunctionImplementationBuilder {
	builder.implementation.Statements = copyStatements(statements)
	return builder
}

// WithLocals replaces the declaration-ordered function-local module names.
func (builder *FunctionImplementationBuilder) WithLocals(locals ...FunctionModuleLocal) *FunctionImplementationBuilder {
	builder.implementation.Locals = copyFunctionModuleLocals(locals)
	return builder
}

// Returns sets the tail return expression of a typed function.
func (builder *FunctionImplementationBuilder) Returns(value RuleValue) *FunctionImplementationBuilder {
	copy := copyRuleValue(value)
	builder.implementation.Return = &copy
	return builder
}

// Build returns an isolated function implementation snapshot.
func (builder *FunctionImplementationBuilder) Build() *FunctionImplementation {
	if builder == nil {
		return nil
	}
	return copyFunctionImplementation(&builder.implementation)
}

// AddFunctionImplementation registers a closed local implementation. Complete
// signature, body, and return validation occurs during canonical model build.
func (component *Component) AddFunctionImplementation(implementation *FunctionImplementation) error {
	if component == nil || implementation == nil {
		return fmt.Errorf("%w: component or implementation is nil", ErrInvalidFunctionImplementation)
	}
	component.mu.Lock()
	component.functions = append(component.functions, copyFunctionImplementation(implementation))
	component.mu.Unlock()
	return nil
}

type canonicalFunctionImplementation struct {
	Signature  canonicalFunctionDecl          `json:"signature"`
	Locals     []canonicalFunctionModuleLocal `json:"locals,omitempty"`
	Statements []canonicalRuleStatement       `json:"statements"`
	Return     *canonicalRuleValue            `json:"return,omitempty"`
}

type canonicalFunctionModuleLocal struct {
	Name  string             `json:"name"`
	Value canonicalRuleValue `json:"value"`
}

func copyFunctionModuleLocals(locals []FunctionModuleLocal) []FunctionModuleLocal {
	result := make([]FunctionModuleLocal, len(locals))
	for index, local := range locals {
		result[index] = FunctionModuleLocal{Name: local.Name, Value: copyRuleValue(local.Value)}
	}
	return result
}

func copyExecutionCallables(
	callables map[string]map[string]*FunctionImplementation,
) map[string]map[string]*FunctionImplementation {
	result := make(map[string]map[string]*FunctionImplementation, len(callables))
	for componentID, functions := range callables {
		copied := make(map[string]*FunctionImplementation, len(functions))
		for key, implementation := range functions {
			copied[key] = implementation
		}
		result[componentID] = copied
	}
	return result
}

func isDynamicModuleFunctionRoute(
	templateID string,
	implementation *FunctionImplementation,
) bool {
	if templateID == "" || implementation == nil || implementation.connectionID == "" ||
		implementation.targetComponent != templateID || len(implementation.connectionScopes) == 0 ||
		len(implementation.connectionScopes) != len(implementation.routeAliases) {
		return false
	}
	for index, scope := range implementation.connectionScopes {
		if scope != ModuleConnectionScope || implementation.routeAliases[index].ComponentID != templateID {
			return false
		}
	}
	return true
}

func copyFunctionImplementation(implementation *FunctionImplementation) *FunctionImplementation {
	if implementation == nil {
		return nil
	}
	result := *implementation
	result.Params = append([]ParamDecl(nil), implementation.Params...)
	result.Locals = copyFunctionModuleLocals(implementation.Locals)
	result.targetParams = append([]ParamDecl(nil), implementation.targetParams...)
	result.connectionScopes = append([]ConnectionScope(nil), implementation.connectionScopes...)
	result.routeAliases = make([]functionRouteAlias, len(implementation.routeAliases))
	for index, alias := range implementation.routeAliases {
		result.routeAliases[index] = functionRouteAlias{
			ComponentID: alias.ComponentID,
			Name:        alias.Name,
			Params:      append([]ParamDecl(nil), alias.Params...),
		}
	}
	result.Statements = copyStatements(implementation.Statements)
	result.Return = copyRuleValuePointer(implementation.Return)
	return &result
}

// prepareFunctionCatalog validates implementation signatures without resolving
// their bodies. Architecture routes are constructed from this catalog before a
// second pass closes every local and connected call site.
func prepareFunctionCatalog(
	component *Component,
	declarations []*FunctionImplementation,
) (map[string]*FunctionImplementation, map[string]canonicalFunctionDecl, error) {
	provided := make(map[string]FunctionDecl)
	providedCanonical := make(map[string]canonicalFunctionDecl)
	// Service qualification and interface-type-expression semantics are not yet
	// executable, so only direct interface functions may receive local bodies.
	for _, declaration := range component.Interface.Functions {
		canonical, key, err := canonicalizeFunction(declaration)
		if err != nil {
			return nil, nil, err
		}
		if declaration.Kind == ProvidesFunction {
			provided[key] = declaration
			providedCanonical[key] = canonical
		}
	}

	catalog := make(map[string]*FunctionImplementation, len(declarations))
	signatures := make(map[string]canonicalFunctionDecl, len(declarations))
	for _, declaration := range declarations {
		if declaration == nil {
			return nil, nil, fmt.Errorf("%w: implementation is nil", ErrInvalidFunctionImplementation)
		}
		_, key, err := canonicalizeFunction(FunctionDecl{
			Name: declaration.Name, Kind: ProvidesFunction,
			Params: declaration.Params, ReturnType: declaration.ReturnType,
		})
		if err != nil {
			return nil, nil, fmt.Errorf("%w: %v", ErrInvalidFunctionImplementation, err)
		}
		interfaceDeclaration, exists := provided[key]
		if !exists {
			return nil, nil, fmt.Errorf("%w: function %q does not exactly match a provided interface signature", ErrInvalidFunctionImplementation, declaration.Name)
		}
		if catalog[key] != nil {
			return nil, nil, fmt.Errorf("%w: duplicate implementation for %q", ErrInvalidFunctionImplementation, key)
		}
		copy := copyFunctionImplementation(declaration)
		// Defaults belong to the declared function object, not its body. Copy
		// the interface formals onto the callable so omitted actuals are closed
		// before F'Call even when an embedding API did not repeat defaults on
		// the implementation declaration.
		copy.Params = append([]ParamDecl(nil), interfaceDeclaration.Params...)
		copy.key = key
		catalog[key] = copy
		signatures[key] = providedCanonical[key]
	}
	return catalog, signatures, nil
}

// canonicalizeFunctionImplementations closes function bodies after the full
// architecture callable catalog exists. This permits deterministic synchronous
// chains and recursion across component boundaries.
func canonicalizeFunctionImplementations(
	component *Component,
	catalog map[string]*FunctionImplementation,
	signatures map[string]canonicalFunctionDecl,
	stateTypes map[string]string,
	callables map[string]*FunctionImplementation,
) (map[string]*FunctionImplementation, []canonicalFunctionImplementation, error) {
	keys := make([]string, 0, len(catalog))
	for key := range catalog {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	normalized := make(map[string]*FunctionImplementation, len(catalog))
	canonical := make([]canonicalFunctionImplementation, 0, len(keys))
	for _, key := range keys {
		implementation := copyFunctionImplementation(catalog[key])
		placeholderTypes := make(map[string]string, len(implementation.Params)+len(implementation.Locals))
		localNames := make(map[string]bool, len(implementation.Params)+len(implementation.Locals))
		for _, parameter := range implementation.Params {
			placeholderTypes[parameter.Name] = parameter.Type
			localNames[strings.ToLower(parameter.Name)] = true
		}
		encoded := canonicalFunctionImplementation{Signature: signatures[key]}
		for index, local := range implementation.Locals {
			name := strings.ToLower(strings.TrimSpace(local.Name))
			if name == "" || localNames[name] {
				return nil, nil, fmt.Errorf("%w: function %q local %d has an empty or duplicate name %q", ErrInvalidFunctionImplementation, implementation.Name, index, local.Name)
			}
			value, canonicalValue, valueType, err := canonicalizeClosedRuleValue(
				"function "+implementation.Name+" local "+name, local.Value,
				stateTypes, placeholderTypes,
			)
			if err != nil {
				return nil, nil, fmt.Errorf("%w: function %q local %q: %w", ErrInvalidFunctionImplementation, implementation.Name, name, err)
			}
			if value.kind != RuleNewValue {
				return nil, nil, fmt.Errorf("%w: function %q local %q requires the current owner-only New initializer", ErrInvalidFunctionImplementation, implementation.Name, name)
			}
			interfaceName := ""
			if component.Interface != nil {
				interfaceName = component.Interface.Name
			}
			if interfaceName == "" || !strings.EqualFold(valueType, interfaceName) {
				return nil, nil, fmt.Errorf("%w: function %q local %q has module type %s, want enclosing interface %s", ErrInvalidFunctionImplementation, implementation.Name, name, valueType, interfaceName)
			}
			implementation.Locals[index] = FunctionModuleLocal{Name: name, Value: value}
			encoded.Locals = append(encoded.Locals, canonicalFunctionModuleLocal{Name: name, Value: canonicalValue})
			placeholderTypes[name] = valueType
			localNames[name] = true
		}
		// Function bodies are canonical independently of their eventual caller.
		// Admit the protected-allocation syntax here, then require its semantic
		// owner at the exact call boundary. This mirrors suspending functions:
		// one canonical body may be valid from an initializer-owned stack while
		// remaining an explicit pre-Call error from an unowned context.
		statements, encodedStatements, err := canonicalizeRuleStatementsWithProcessExit(
			component, "function "+implementation.Name, implementation.Statements,
			stateTypes, placeholderTypes, callables, &implementation.ReturnType,
			false, "", true, true,
		)
		if err != nil {
			return nil, nil, fmt.Errorf("%w: function %q: %w", ErrInvalidFunctionImplementation, implementation.Name, err)
		}
		implementation.Statements = statements
		encoded.Statements = encodedStatements
		if implementation.ReturnType == "" {
			if implementation.Return != nil {
				return nil, nil, fmt.Errorf("%w: function %q has no return type but declares a return value", ErrInvalidFunctionImplementation, implementation.Name)
			}
		} else {
			if implementation.Return == nil {
				return nil, nil, fmt.Errorf("%w: typed function %q requires an explicit deterministic return value", ErrInvalidFunctionImplementation, implementation.Name)
			}
			value, encodedValue, valueType, err := canonicalizeClosedRuleValue(
				"function "+implementation.Name+" return", *implementation.Return,
				stateTypes, placeholderTypes,
			)
			if err != nil {
				return nil, nil, fmt.Errorf("%w: %w", ErrInvalidFunctionImplementation, err)
			}
			if value.kind == RuleLiteralValue {
				if !valueMatchesPredefinedType(value.literal, implementation.ReturnType) {
					return nil, nil, fmt.Errorf("%w: function %q return does not match %s", ErrInvalidFunctionImplementation, implementation.Name, implementation.ReturnType)
				}
			} else if valueType != "" && !ruleValueAssignableToPredefined(value, valueType, implementation.ReturnType) {
				return nil, nil, fmt.Errorf("%w: function %q return has type %s, want %s", ErrInvalidFunctionImplementation, implementation.Name, valueType, implementation.ReturnType)
			}
			implementation.Return = &value
			encoded.Return = &encodedValue
		}
		normalized[key] = implementation
		canonical = append(canonical, encoded)
	}
	return normalized, canonical, nil
}

func functionBindings(params []ParamDecl, parameters map[string]any) pattern.Bindings {
	names := make([]string, 0, len(params))
	for _, parameter := range params {
		names = append(names, parameter.Name)
	}
	sort.Strings(names)
	result := make(pattern.Bindings, 0, len(names))
	for _, name := range names {
		result = append(result, pattern.Binding{Placeholder: name, Value: parameters[name]})
	}
	return result
}

type functionModuleLocalRuntime struct {
	nameID string
}

func bindFunctionModuleLocal(
	match pattern.MatchResult,
	name string,
	value gorapide.RapideModuleValue,
) (pattern.MatchResult, error) {
	for _, binding := range match.Bindings {
		if binding.Placeholder != name {
			continue
		}
		equal, err := gorapide.CanonicalValuesEqual(binding.Value, value)
		if err != nil || !equal {
			return pattern.MatchResult{}, fmt.Errorf("%w: function local %q conflicts with an existing binding", ErrInvalidFunctionImplementation, name)
		}
		return match, nil
	}
	match.Bindings = append(append(pattern.Bindings(nil), match.Bindings...), pattern.Binding{
		Placeholder: name,
		Value:       value,
	})
	sort.Slice(match.Bindings, func(left, right int) bool {
		return match.Bindings[left].Placeholder < match.Bindings[right].Placeholder
	})
	return match, nil
}

func allocateFunctionModuleLocals(
	componentID, modelDigest, occurrence string,
	implementation *FunctionImplementation,
	match pattern.MatchResult,
	runtime *functionExecutionRuntime,
	execution *statementExecution,
) (pattern.MatchResult, []functionModuleLocalRuntime, error) {
	allocated := make([]functionModuleLocalRuntime, 0, len(implementation.Locals))
	for index, local := range implementation.Locals {
		localOccurrence := fmt.Sprintf("%s|local=%06d:%s", occurrence, index, local.Name)
		module, actual, err := allocateModuleNewActual(
			componentID, modelDigest, localOccurrence, local.Value.newType,
			local.Value.newArguments, local.Value.newInitializationArguments,
			match.Bindings, runtime.state[componentID], runtime, execution,
		)
		if err != nil {
			return pattern.MatchResult{}, nil, err
		}
		if execution.initializationFailure != nil {
			return match, allocated, nil
		}
		if execution.pendingInterrupt != nil {
			return match, allocated, nil
		}
		nameID := "function-local:" + module.Identity()
		if err := runtime.lifecycle.addName(moduleNameRuntime{
			nameID: nameID, moduleID: module.Identity(), owner: runtime.modules[componentID].Identity(),
			name: local.Name, kind: "function-local", acquiredAfter: actual.frontier,
		}); err != nil {
			return pattern.MatchResult{}, nil, err
		}
		finalized, _, err := runtime.lifecycle.releaseName(actual.nameID, actual.frontier)
		if err != nil {
			return pattern.MatchResult{}, nil, err
		}
		if finalized != "" {
			return pattern.MatchResult{}, nil, fmt.Errorf("%w: function local %q did not retain allocated module %q", ErrInvalidFunctionImplementation, local.Name, module.Identity())
		}
		match, err = bindFunctionModuleLocal(match, local.Name, module)
		if err != nil {
			return pattern.MatchResult{}, nil, err
		}
		allocated = append(allocated, functionModuleLocalRuntime{nameID: nameID})
		// Local declarations elaborate sequentially. The next allocator and the
		// function body therefore descend from this allocation's Start.
		execution.control = append([]gorapide.EventID(nil), actual.frontier...)
		execution.pendingOperations = canonicalStateOperationReferences(append(
			execution.pendingOperations, actual.operations...,
		))
	}
	return match, allocated, nil
}

func releaseFunctionModuleLocals(
	modelDigest string,
	locals []functionModuleLocalRuntime,
	runtime *functionExecutionRuntime,
	execution *statementExecution,
) error {
	frontier := canonicalEventIDs(execution.control)
	if len(locals) != 0 && len(frontier) == 0 {
		return fmt.Errorf("%w: function-local scope has no causal exit frontier", ErrInvalidFunctionImplementation)
	}
	for _, local := range locals {
		_, err := releaseModuleName(modelDigest, local.nameID, frontier, runtime, execution)
		if err != nil {
			return err
		}
		// A successful deferred initializer action may retain the module after
		// its lexical function-local name is lost. The scheduler's own audited
		// name releases and finalizes it when the last future action occurs.
	}
	return nil
}

func canonicalFunctionReturnParameters(returnType string, actuals map[string]any, returned any) (map[string]any, error) {
	parameters := make(map[string]any, len(actuals)+1)
	for name, value := range actuals {
		parameters[name] = value
	}
	if returnType != "" {
		parameters["Return"] = returned
	}
	return gorapide.CanonicalizeParams(parameters)
}

func functionRouteActuals(
	sourceParams []ParamDecl,
	sourceActuals map[string]any,
	targetParams []ParamDecl,
) (map[string]any, error) {
	result := make(map[string]any, len(targetParams))
	shared := len(sourceParams)
	if len(targetParams) < shared {
		shared = len(targetParams)
	}
	for index := 0; index < shared; index++ {
		target := targetParams[index]
		source := sourceParams[index]
		value, exists := sourceActuals[source.Name]
		if !exists {
			return nil, fmt.Errorf("%w: connected function route is missing source argument %q",
				ErrInvalidFunctionImplementation, source.Name)
		}
		result[target.Name] = value
	}
	for _, target := range targetParams[shared:] {
		if target.Default == nil {
			return nil, fmt.Errorf("%w: connected function route cannot supply target argument %q without a default",
				ErrInvalidFunctionImplementation, target.Name)
		}
		result[target.Name] = target.Default
	}
	return gorapide.CanonicalizeParams(result)
}

func canonicalizeFunctionCall(
	owner string,
	call FunctionCall,
	stateTypes, placeholderTypes map[string]string,
	functions map[string]*FunctionImplementation,
) (FunctionCall, canonicalFunctionCall, error) {
	if call.ID == "" || call.Name == "" {
		return FunctionCall{}, canonicalFunctionCall{}, fmt.Errorf("%w: %s has an empty function call ID or name", ErrInvalidDeclarativeStatement, owner)
	}
	resultType := ""
	if call.ResultTarget != "" {
		var ok bool
		resultType, ok = stateTypes[call.ResultTarget]
		if !ok {
			return FunctionCall{}, canonicalFunctionCall{}, fmt.Errorf("%w: %s function call %q writes undeclared state %q", ErrInvalidDeclarativeStatement, owner, call.ID, call.ResultTarget)
		}
	}
	type candidateArgument struct {
		normalized RuleParameter
		canonical  canonicalRuleParameter
		typeName   string
	}
	arguments := make(map[string]candidateArgument, len(call.Arguments))
	for _, argument := range call.Arguments {
		if argument.Name == "" || arguments[argument.Name].normalized.Name != "" {
			return FunctionCall{}, canonicalFunctionCall{}, fmt.Errorf("%w: %s function call %q has empty or duplicate argument %q", ErrInvalidDeclarativeStatement, owner, call.ID, argument.Name)
		}
		value, encoded, typeName, err := canonicalizeClosedRuleValue(
			owner+" function call "+call.ID+" argument "+argument.Name,
			argument.Value, stateTypes, placeholderTypes,
		)
		if err != nil {
			return FunctionCall{}, canonicalFunctionCall{}, err
		}
		arguments[argument.Name] = candidateArgument{
			normalized: RuleParameter{Name: argument.Name, Value: value},
			canonical: canonicalRuleParameter{
				Name: argument.Name, Kind: encoded.Kind, Placeholder: encoded.Placeholder,
				State: encoded.State, Type: encoded.Type,
				GeneratorArguments:      encoded.GeneratorArguments,
				InitializationArguments: encoded.InitializationArguments, Literal: encoded.Literal,
				Operator: encoded.Operator, Operands: encoded.Operands,
			},
			typeName: typeName,
		}
	}
	keys := make([]string, 0, len(functions))
	for key := range functions {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	type candidateMatch struct {
		implementation *FunctionImplementation
		arguments      map[string]candidateArgument
	}
	matches := make([]candidateMatch, 0)
	for _, key := range keys {
		candidate := functions[key]
		if candidate.Name != call.Name || len(arguments) > len(candidate.Params) {
			continue
		}
		if call.ResultTarget != "" && (candidate.ReturnType == "" || !predefinedTypeAssignable(candidate.ReturnType, resultType)) {
			continue
		}
		completed := make(map[string]candidateArgument, len(candidate.Params))
		for name, argument := range arguments {
			completed[name] = argument
		}
		compatible := true
		for _, formal := range candidate.Params {
			argument, ok := completed[formal.Name]
			if !ok {
				if formal.Default == nil {
					compatible = false
					break
				}
				value, encoded, typeName, err := canonicalizeClosedRuleValue(
					owner+" function call "+call.ID+" default argument "+formal.Name,
					LiteralValue(formal.Default), stateTypes, placeholderTypes,
				)
				if err != nil {
					return FunctionCall{}, canonicalFunctionCall{}, err
				}
				argument = candidateArgument{
					normalized: RuleParameter{Name: formal.Name, Value: value},
					canonical: canonicalRuleParameter{
						Name: formal.Name, Kind: encoded.Kind, Placeholder: encoded.Placeholder,
						State: encoded.State, Type: encoded.Type,
						GeneratorArguments:      encoded.GeneratorArguments,
						InitializationArguments: encoded.InitializationArguments, Literal: encoded.Literal,
						Operator: encoded.Operator, Operands: encoded.Operands,
					},
					typeName: typeName,
				}
				completed[formal.Name] = argument
			}
			if argument.normalized.Value.kind == RuleLiteralValue {
				compatible = valueMatchesPredefinedType(argument.normalized.Value.literal, formal.Type)
			} else if argument.typeName != "" {
				compatible = ruleValueAssignableToPredefined(argument.normalized.Value, argument.typeName, formal.Type)
			}
			if !compatible {
				break
			}
		}
		if compatible && len(completed) != len(candidate.Params) {
			compatible = false
		}
		if compatible {
			matches = append(matches, candidateMatch{implementation: candidate, arguments: completed})
		}
	}
	if len(matches) == 0 {
		return FunctionCall{}, canonicalFunctionCall{}, fmt.Errorf("%w: %s call %q does not match an implemented local or connected function %q", ErrInvalidDeclarativeStatement, owner, call.ID, call.Name)
	}
	if len(matches) > 1 {
		return FunctionCall{}, canonicalFunctionCall{}, fmt.Errorf("%w: %s call %q is ambiguous across %d overloads of %q", ErrInvalidDeclarativeStatement, owner, call.ID, len(matches), call.Name)
	}
	selected := matches[0].implementation
	arguments = matches[0].arguments
	normalized := FunctionCall{
		ID: call.ID, Name: call.Name, ResultTarget: call.ResultTarget, functionKey: selected.key,
	}
	encoded := canonicalFunctionCall{
		ID: call.ID, Name: call.Name, Signature: selected.key, ResultTarget: call.ResultTarget,
	}
	for _, formal := range selected.Params {
		argument := arguments[formal.Name]
		normalized.Arguments = append(normalized.Arguments, argument.normalized)
		encoded.Arguments = append(encoded.Arguments, argument.canonical)
	}
	return normalized, encoded, nil
}

func executeFunctionCall(
	componentID string,
	component *Component,
	rule *DeclarativeRule,
	match pattern.MatchResult,
	matchDigest, modelDigest, statementPath string,
	call FunctionCall,
	functionRuntime *functionExecutionRuntime,
	cells map[string]*stateCell,
	execution *statementExecution,
	budget *statementBudget,
) (any, error) {
	if functionRuntime == nil {
		return nil, fmt.Errorf("%w: function execution runtime is missing", ErrInvalidFunctionImplementation)
	}
	if functionCallMaySuspend(componentID, call, functionRuntime) {
		return nil, fmt.Errorf(
			"%w: function call %q may pause or delay and requires a process-owned resumable call stack",
			ErrInvalidFunctionImplementation, call.ID,
		)
	}
	implementation := functionRuntime.callables[componentID][call.functionKey]
	if implementation == nil {
		return nil, fmt.Errorf("%w: call %q resolved to missing function signature %q", ErrInvalidFunctionImplementation, call.ID, call.functionKey)
	}
	if functionRuntime.architectureScopeClosed && functionImplementationUsesArchitectureRoute(implementation) {
		return nil, fmt.Errorf(
			"%w: function call %q requires an architecture route after its owning architecture scope closed",
			ErrInvalidFunctionImplementation, call.ID,
		)
	}
	targetComponentID := implementation.targetComponent
	if targetComponentID == "" {
		targetComponentID = componentID
	}
	// A module initializer owns only same-component synchronous allocation.
	// An architecture initializer already waits for its connected provider's
	// startup frontier, so the selected provider may own the same immediate
	// handler/New computation without inventing a process continuation.
	interruptAllocationOwned := execution.initializationOwned && targetComponentID == componentID
	architectureOwner, architectureInitial := strings.CutPrefix(execution.owner, "architecture-initial:")
	if architectureInitial && componentID == architectureBoundaryID(architectureOwner) {
		interruptAllocationOwned = true
	}
	if functionImplementationRequiresInitializationInterruptOwner(
		targetComponentID, implementation, functionRuntime,
	) && !interruptAllocationOwned {
		return nil, fmt.Errorf(
			"%w: function call %q contains function-owned generation-time interrupt allocation and requires an initializer-owned same-component call stack, a direct architecture-initial connected-provider activation, or a supported process-owned continuation",
			ErrInvalidFunctionImplementation, call.ID,
		)
	}
	targetName := implementation.targetName
	if targetName == "" {
		targetName = implementation.Name
	}
	targetComponent := functionRuntime.components[targetComponentID]
	targetCells := functionRuntime.state[targetComponentID]
	if targetComponent == nil || targetCells == nil {
		return nil, fmt.Errorf("%w: function %q target component %q is unavailable", ErrInvalidFunctionImplementation, implementation.Name, targetComponentID)
	}
	aliases := append([]functionRouteAlias(nil), implementation.routeAliases...)
	if len(aliases) == 0 && implementation.connectionID != "" {
		aliases = append(aliases, functionRouteAlias{
			ComponentID: targetComponentID,
			Name:        targetName,
			Params:      append([]ParamDecl(nil), implementation.targetParams...),
		})
	}
	remote := len(aliases) != 0
	actuals := make(map[string]any, len(call.Arguments))
	argumentCauses := make([]gorapide.EventID, 0)
	for _, argument := range call.Arguments {
		evaluated, err := evaluateClosedRuleValue(
			rule.ID+" statement "+statementPath+" function argument "+argument.Name,
			argument.Value, match.Bindings, cells,
		)
		if err != nil {
			return nil, err
		}
		actuals[argument.Name] = evaluated.value
		argumentCauses = append(argumentCauses, evaluated.causes...)
		if err := incorporateEvaluatedStateReads(execution, evaluated.reads, evaluated.causes); err != nil {
			return nil, err
		}
	}
	actuals, err := gorapide.CanonicalizeParams(actuals)
	if err != nil {
		return nil, err
	}
	for _, formal := range implementation.Params {
		if !valueMatchesPredefinedType(actuals[formal.Name], formal.Type) {
			return nil, fmt.Errorf("%w: function %q argument %q does not match %s", ErrInvalidFunctionImplementation, implementation.Name, formal.Name, formal.Type)
		}
	}
	targetParams := implementation.targetParams
	if len(targetParams) == 0 && len(implementation.Params) != 0 {
		targetParams = implementation.Params
	}
	aliasActuals := make([]map[string]any, len(aliases))
	for index, alias := range aliases {
		aliasActuals[index], err = functionRouteActuals(implementation.Params, actuals, alias.Params)
		if err != nil {
			return nil, err
		}
	}
	providerActuals := actuals
	if len(aliasActuals) != 0 {
		providerActuals = aliasActuals[len(aliasActuals)-1]
	}
	controlCauses := append([]gorapide.EventID(nil), execution.control...)
	if strings.HasPrefix(execution.owner, "architecture-initial:") {
		startup, err := architectureInitialFunctionTargetFrontier(
			functionRuntime, targetComponentID,
		)
		if err != nil {
			return nil, err
		}
		controlCauses = maximalKnownCausalFrontier(
			functionRuntime.poset, append(controlCauses, startup...),
		)
	}
	callCauses := canonicalEventIDs(append(controlCauses, argumentCauses...))
	occurrence := rule.ID + "|match=" + matchDigest + "|statement=" + statementPath + "|function=" + call.ID + "|signature=" + call.functionKey
	callEvent, err := gorapide.NewDeterministicEvent(gorapide.EventProvenance{
		Profile: CompatibilityProfile, Model: modelDigest, Instance: componentID,
		Action: implementation.Name + "'Call", Occurrence: occurrence + "|call",
		Causes: callCauses, Timings: functionRuntime.clocks.instantTimings(componentID, targetComponentID),
	}, actuals)
	if err != nil {
		return nil, err
	}
	if err := addStateOperationSuccessors(execution.pendingOperations, string(callEvent.ID)); err != nil {
		return nil, err
	}
	execution.pendingOperations = nil
	if remote {
		for index, alias := range aliases {
			callEvent.Observations = append(callEvent.Observations, gorapide.EventObservation{
				Name: alias.Name + "'Call", Source: alias.ComponentID, Params: aliasActuals[index],
			})
		}
	}
	prefix := "function@" + statementPath + "/" + call.ID
	callSnapshots := map[string]map[string]*stateCell{
		componentID: cloneStateCells(cells), targetComponentID: cloneStateCells(targetCells),
	}
	execution.generated = append(execution.generated, generatedRuleOutput{
		localID: prefix + "/call", event: callEvent, causes: callCauses,
		stateSnapshot: cloneStateCells(cells), observationSnapshots: callSnapshots,
	})

	functionMatch := pattern.MatchResult{Bindings: functionBindings(targetParams, providerActuals)}
	functionMatch, err = bindModuleSelf(functionMatch, targetComponentID, functionRuntime)
	if err != nil {
		return nil, err
	}
	functionRule := &DeclarativeRule{ID: "function/" + call.functionKey}
	child := statementExecution{
		control: []gorapide.EventID{callEvent.ID}, clocks: execution.clocks, owner: execution.owner,
		budget:              budget,
		initializationOwned: interruptAllocationOwned,
	}
	for _, active := range execution.interruptHandlers {
		if targetComponentID == componentID || active.processOwned {
			child.interruptHandlers = append(child.interruptHandlers, active)
		}
	}
	retainSynchronousGenerationTimeConnectionState(execution, &child)
	inheritGenerationTimeConnectionState(execution, &child)
	functionMatch, localModules, err := allocateFunctionModuleLocals(
		targetComponentID, modelDigest, occurrence, implementation,
		functionMatch, functionRuntime, &child,
	)
	if err != nil {
		return nil, err
	}
	if child.initializationFailure != nil {
		if err := releaseFunctionModuleLocals(modelDigest, localModules, functionRuntime, &child); err != nil {
			return nil, err
		}
		transferAbandonedFunctionExecution(prefix, targetComponentID, &child, execution)
		return nil, nil
	}
	if child.pendingInterrupt != nil {
		if err := releaseFunctionModuleLocals(modelDigest, localModules, functionRuntime, &child); err != nil {
			return nil, err
		}
		transferAbandonedFunctionExecution(prefix, targetComponentID, &child, execution)
		return nil, nil
	}
	control, err := executeRuleStatementList(
		targetComponentID, targetComponent, functionRule, functionMatch, string(callEvent.ID), modelDigest,
		implementation.Statements, functionRuntime, targetCells, "", &child, budget,
	)
	if err != nil {
		return nil, err
	}
	if child.initializationFailure != nil {
		if err := releaseFunctionModuleLocals(modelDigest, localModules, functionRuntime, &child); err != nil {
			return nil, err
		}
		transferAbandonedFunctionExecution(prefix, targetComponentID, &child, execution)
		return nil, nil
	}
	if control == statementRaiseException {
		if err := releaseFunctionModuleLocals(modelDigest, localModules, functionRuntime, &child); err != nil {
			return nil, err
		}
		for _, output := range child.generated {
			output.localID = prefix + "/body/" + output.localID
			execution.generated = append(execution.generated, output)
		}
		execution.scheduled = append(execution.scheduled, child.scheduled...)
		execution.reads = append(execution.reads, qualifyStateReads(targetComponentID, child.reads)...)
		execution.writes = append(execution.writes, qualifyStateWrites(targetComponentID, child.writes)...)
		execution.control = canonicalEventIDs(child.control)
		execution.pendingOperations = child.pendingOperations
		execution.raised = child.raised
		return nil, nil
	}
	if control == statementHandleInterrupt {
		if err := releaseFunctionModuleLocals(modelDigest, localModules, functionRuntime, &child); err != nil {
			return nil, err
		}
		transferAbandonedFunctionExecution(prefix, targetComponentID, &child, execution)
		execution.pendingInterrupt = child.pendingInterrupt
		return nil, nil
	}
	if control != statementContinue && control != statementReturnFunction {
		return nil, fmt.Errorf("%w: loop control escaped function %q", ErrInvalidFunctionImplementation, implementation.Name)
	}
	var returned any
	if child.returned {
		returned = child.returnValue
	} else if implementation.Return != nil {
		evaluated, err := evaluateClosedRuleValue(
			"function "+targetName+" return", *implementation.Return,
			functionMatch.Bindings, targetCells,
		)
		if err != nil {
			return nil, err
		}
		returned = evaluated.value
		if err := incorporateEvaluatedStateReads(&child, evaluated.reads, evaluated.causes); err != nil {
			return nil, err
		}
	}
	if err := releaseFunctionModuleLocals(modelDigest, localModules, functionRuntime, &child); err != nil {
		return nil, err
	}
	for _, output := range child.generated {
		output.localID = prefix + "/body/" + output.localID
		execution.generated = append(execution.generated, output)
	}
	execution.scheduled = append(execution.scheduled, child.scheduled...)
	execution.reads = append(execution.reads, qualifyStateReads(targetComponentID, child.reads)...)
	execution.writes = append(execution.writes, qualifyStateWrites(targetComponentID, child.writes)...)
	execution.control = canonicalEventIDs(child.control)
	if implementation.ReturnType != "" && !valueMatchesPredefinedType(returned, implementation.ReturnType) {
		return nil, fmt.Errorf("%w: function %q returned %T, want %s", ErrInvalidFunctionImplementation, targetName, returned, implementation.ReturnType)
	}
	providerReturnParameters, err := canonicalFunctionReturnParameters(implementation.ReturnType, providerActuals, returned)
	if err != nil {
		return nil, err
	}
	callerReturnParameters, err := canonicalFunctionReturnParameters(implementation.ReturnType, actuals, returned)
	if err != nil {
		return nil, err
	}
	returnCauses := canonicalEventIDs(execution.control)
	returnEvent, err := gorapide.NewDeterministicEvent(gorapide.EventProvenance{
		Profile: CompatibilityProfile, Model: modelDigest, Instance: targetComponentID,
		Action: targetName + "'Return", Occurrence: occurrence + "|return",
		Causes: returnCauses, Timings: functionRuntime.clocks.instantTimings(targetComponentID, componentID),
	}, providerReturnParameters)
	if err != nil {
		return nil, err
	}
	if err := addStateOperationSuccessors(child.pendingOperations, string(returnEvent.ID)); err != nil {
		return nil, err
	}
	if remote {
		returnEvent.Observations = append(returnEvent.Observations, gorapide.EventObservation{
			Name: implementation.Name + "'Return", Source: componentID, Params: callerReturnParameters,
		})
		for index := 0; index+1 < len(aliases); index++ {
			parameters, err := canonicalFunctionReturnParameters(
				implementation.ReturnType, aliasActuals[index], returned,
			)
			if err != nil {
				return nil, err
			}
			returnEvent.Observations = append(returnEvent.Observations, gorapide.EventObservation{
				Name: aliases[index].Name + "'Return", Source: aliases[index].ComponentID, Params: parameters,
			})
		}
	}
	returnSnapshots := map[string]map[string]*stateCell{
		targetComponentID: cloneStateCells(targetCells), componentID: cloneStateCells(cells),
	}
	execution.generated = append(execution.generated, generatedRuleOutput{
		localID: prefix + "/return", event: returnEvent, causes: returnCauses,
		stateSnapshot: cloneStateCells(targetCells), observationSnapshots: returnSnapshots,
	})
	execution.control = []gorapide.EventID{returnEvent.ID}
	execution.pendingOperations = nil
	return returned, nil
}

func functionImplementationUsesArchitectureRoute(implementation *FunctionImplementation) bool {
	if implementation == nil {
		return false
	}
	for _, scope := range implementation.connectionScopes {
		if scope == ArchitectureConnectionScope {
			return true
		}
	}
	return false
}

func transferAbandonedFunctionExecution(
	prefix, targetComponentID string,
	child, parent *statementExecution,
) {
	if child == nil || parent == nil {
		return
	}
	for _, output := range child.generated {
		output.localID = prefix + "/body/" + output.localID
		parent.generated = append(parent.generated, output)
	}
	parent.scheduled = append(parent.scheduled, child.scheduled...)
	parent.reads = append(parent.reads, qualifyStateReads(targetComponentID, child.reads)...)
	parent.writes = append(parent.writes, qualifyStateWrites(targetComponentID, child.writes)...)
	parent.control = canonicalEventIDs(child.control)
	parent.pendingOperations = child.pendingOperations
	parent.initializationFailure = child.initializationFailure
	parent.pendingInterrupt = child.pendingInterrupt
	parent.canceledSchedules = append(parent.canceledSchedules, child.canceledSchedules...)
}

func architectureInitialFunctionTargetFrontier(
	runtime *functionExecutionRuntime,
	targetComponentID string,
) ([]gorapide.EventID, error) {
	if runtime == nil || runtime.lifecycle == nil {
		return nil, fmt.Errorf("%w: architecture initial function lifecycle runtime is unavailable", ErrInvalidFunctionImplementation)
	}
	module := runtime.modules[targetComponentID]
	moduleID := module.Identity()
	if moduleID == "" {
		return nil, fmt.Errorf(
			"%w: architecture initial function target component %q has no module allocation",
			ErrInvalidFunctionImplementation, targetComponentID,
		)
	}
	lifecycle := runtime.lifecycle.modules[moduleID]
	if lifecycle == nil {
		return nil, fmt.Errorf(
			"%w: architecture initial function target component %q has no lifecycle",
			ErrInvalidFunctionImplementation, targetComponentID,
		)
	}
	if lifecycle.state == ModuleTerminatedState || lifecycle.state == ModuleFinalizedState {
		return nil, fmt.Errorf(
			"%w: architecture initial function target component %q is %s",
			ErrInvalidFunctionImplementation, targetComponentID, lifecycle.state,
		)
	}
	frontier := canonicalEventIDs(runtime.startupFrontiers[targetComponentID])
	if len(frontier) == 0 {
		return nil, fmt.Errorf(
			"%w: architecture initial function target component %q has no closed startup frontier",
			ErrInvalidFunctionImplementation, targetComponentID,
		)
	}
	return frontier, nil
}

func maximalKnownCausalFrontier(
	poset *gorapide.Poset,
	ids []gorapide.EventID,
) []gorapide.EventID {
	ids = canonicalEventIDs(ids)
	if poset == nil || len(ids) < 2 {
		return ids
	}
	result := make([]gorapide.EventID, 0, len(ids))
	for _, candidate := range ids {
		dominated := false
		for _, other := range ids {
			if candidate != other && poset.IsCausallyBefore(candidate, other) {
				dominated = true
				break
			}
		}
		if !dominated {
			result = append(result, candidate)
		}
	}
	return canonicalEventIDs(result)
}

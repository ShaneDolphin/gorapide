package rapide

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/arch"
	"github.com/ShaneDolphin/gorapide/constraint"
	"github.com/ShaneDolphin/gorapide/pattern"
)

// TypeError is a deterministic static-semantic diagnostic.
type TypeError struct {
	Position Position
	Message  string
}

func (err *TypeError) Error() string {
	return fmt.Sprintf("Rapide type error at %d:%d: %s", err.Position.Line, err.Position.Column, err.Message)
}

var predefinedTypes = map[string]string{
	"triv":      "Triv",
	"boolean":   "Boolean",
	"integer":   "Integer",
	"natural":   "Natural",
	"positive":  "Positive",
	"float":     "Float",
	"character": "Character",
	"string":    "String",
}

type compiledModuleDecl struct {
	declaration        ModuleDecl
	generatorArguments []arch.ModuleGeneratorArgument
	typeDenotations    []gorapide.RapideTypeDenotation
	objectDenotations  []gorapide.RapideObjectDenotation
	recordObjects      []gorapide.RapideRecordObjectDeclaration
	objectBindings     map[string]behaviorBinding
	states             []arch.StateDeclaration
	stateBindings      map[string]behaviorBinding
	initialParameters  []arch.ModuleInitializationParameter
	functions          []*arch.FunctionImplementation
	exceptions         []arch.ExceptionDeclaration
	clocks             []ClockDecl
	connections        []compiledModuleConnection
	initial            []arch.Statement
	mode               arch.ModuleProcessMode
	processes          []*arch.DeclarativeProcess
	handler            *arch.ExceptionHandler
	final              []arch.Statement
}

type compiledModuleConnection struct {
	position         Position
	semanticKey      string
	function         bool
	requiredFunction string
	providedFunction string
	kind             arch.ConnectionKind
	trigger          pattern.Pattern
	targetAction     string
	outputParameters []arch.ConnectionParameter
}

// Compile parses, type-checks, and lowers one named architecture into the
// deterministic GoRapide kernel. An empty architectureName is permitted only
// when the file declares exactly one architecture.
func Compile(source []byte, architectureName string) (*arch.Architecture, error) {
	return CompileWithArguments(source, architectureName, nil)
}

// CompileWithArguments parses, type-checks, and lowers one architecture
// generator application. arguments are the complete explicit actual-object
// environment for the selected generator; host map order is nonsemantic.
func CompileWithArguments(source []byte, architectureName string, arguments map[string]any) (*arch.Architecture, error) {
	file, err := Parse(source)
	if err != nil {
		return nil, err
	}
	return CompileFileWithArguments(file, architectureName, arguments)
}

// CompileFile type-checks and lowers a parsed file.
func CompileFile(file *File, architectureName string) (*arch.Architecture, error) {
	return CompileFileWithArguments(file, architectureName, nil)
}

// CompileFileWithArguments type-checks and lowers a parsed architecture
// generator application with a complete explicit actual-object environment.
func CompileFileWithArguments(file *File, architectureName string, arguments map[string]any) (*arch.Architecture, error) {
	if file == nil {
		return nil, &TypeError{Position: Position{Line: 1, Column: 1}, Message: "source file is nil"}
	}
	if _, err := compileExceptionDeclarations("outermost", file.Exceptions); err != nil {
		return nil, err
	}
	sourceInterfaces := make([]InterfaceDecl, len(file.Interfaces))
	for index, declaration := range file.Interfaces {
		sourceInterfaces[index] = cloneInterfaceForNormalization(declaration)
		sourceInterfaces[index].Exceptions = mergeVisibleExceptionDeclarations(
			visibleOutermostExceptions(file.Exceptions, declaration.Position),
			sourceInterfaces[index].Exceptions...,
		)
	}
	interfaces, err := normalizeInterfaceDeclarationsWithAliases(sourceInterfaces, file.TypeAliases)
	if err != nil {
		return nil, err
	}
	typeElaborator, err := newSourceTypeElaboratorWithUnionsAndEnumerations(
		interfaces, file.TypeAliases, file.Unions, file.Enumerations,
	)
	if err != nil {
		return nil, err
	}
	compiledInterfaces := make(map[string]*arch.InterfaceDecl, len(file.Interfaces))
	compiledBehaviorStates := make(map[string][]arch.StateDeclaration, len(file.Interfaces))
	compiledBehaviors := make(map[string][]*arch.FunctionImplementation, len(file.Interfaces))
	compiledBehaviorRules := make(map[string][]*arch.DeclarativeRule, len(file.Interfaces))
	compiledInterfaceExceptions := make(map[string][]arch.ExceptionDeclaration, len(file.Interfaces))
	interfaceKeys := make([]string, 0, len(interfaces))
	for key := range interfaces {
		interfaceKeys = append(interfaceKeys, key)
	}
	sort.Strings(interfaceKeys)
	for _, key := range interfaceKeys {
		declaration := interfaces[key]
		execution, err := typeElaborator.executionInterfaceExpansion(declaration)
		if err != nil {
			return nil, err
		}
		builder := arch.Interface(declaration.Name)
		interfaceExceptions, err := compileExceptionDeclarations("interface", execution.exceptions)
		if err != nil {
			return nil, err
		}
		compiledInterfaceExceptions[key] = interfaceExceptions
		seenActions := make(map[string]bool, len(execution.actions))
		for _, action := range execution.actions {
			actionKey := folded(action.Name)
			if seenActions[actionKey] {
				return nil, typeError(action.Position, "duplicate action %q in interface %q", action.Name, declaration.Name)
			}
			seenActions[actionKey] = true
			parameters := make([]arch.ParamDecl, 0, len(action.Parameters))
			seenParameters := make(map[string]bool, len(action.Parameters))
			for _, parameter := range action.Parameters {
				parameterKey := folded(parameter.Name)
				if seenParameters[parameterKey] {
					return nil, typeError(parameter.Position, "duplicate parameter %q on action %q", parameter.Name, action.Name)
				}
				seenParameters[parameterKey] = true
				typeName, err := typeElaborator.executionPredefinedTypeExpression(
					parameter.Position, parameter.Type, parameter.TypeExpression,
				)
				if err != nil {
					if action.Mode != ActionOut && action.Mode != ActionIn {
						return nil, err
					}
					if parameter.TypeExpression.Kind != TypeExpressionName {
						return nil, err
					}
					interfaceKey, interfaceDeclaration, interfaceErr := typeElaborator.interfaceDeclaration(
						parameter.Position, parameter.Type,
					)
					if interfaceErr != nil {
						return nil, err
					}
					structuralType, interfaceErr := typeElaborator.interfaceType(interfaceKey)
					if interfaceErr != nil {
						return nil, interfaceErr
					}
					parameters = append(parameters, arch.PStructural(
						parameter.Name, interfaceDeclaration.Name, structuralType,
					))
					continue
				}
				parameters = append(parameters, arch.P(parameter.Name, typeName))
			}
			switch action.Mode {
			case ActionIn:
				builder.InAction(action.Name, parameters...)
			case ActionOut:
				builder.OutAction(action.Name, parameters...)
			case ActionPrivate:
				builder.PrivateAction(action.Name, parameters...)
			default:
				return nil, typeError(action.Position, "action %q has unsupported mode %q", action.Name, action.Mode)
			}
		}
		seenFunctions := make(map[string]bool, len(execution.functions))
		for _, function := range execution.functions {
			if keyword(function.Name, "New") {
				return nil, typeError(function.Position,
					"interface %q may not declare allocator function New; the tool-supplied owner-only function is always used",
					declaration.Name)
			}
			parameters := make([]arch.ParamDecl, 0, len(function.Parameters))
			seenParameters := make(map[string]bool, len(function.Parameters))
			for _, parameter := range function.Parameters {
				parameterKey := folded(parameter.Name)
				if seenParameters[parameterKey] {
					return nil, typeError(parameter.Position, "duplicate parameter %q on function %q", parameter.Name, function.Name)
				}
				seenParameters[parameterKey] = true
				typeName, err := typeElaborator.executionPredefinedTypeExpression(
					parameter.Position, parameter.Type, parameter.TypeExpression,
				)
				if err != nil {
					return nil, err
				}
				compiledParameter := arch.P(parameter.Name, typeName)
				if parameter.Default != nil {
					if !sourceClosedScalarType(typeName) {
						return nil, typeError(parameter.Default.Position,
							"function parameter %q default has type %s; the current closed default-denotation subset supports predefined scalar types",
							parameter.Name, typeName)
					}
					value, _, err := evaluateClosedGeneratorDefault(parameter, typeName, "function")
					if err != nil {
						return nil, err
					}
					compiledParameter = arch.PDefault(parameter.Name, typeName, value)
				}
				parameters = append(parameters, compiledParameter)
			}
			returnType := ""
			if function.ReturnType != "" {
				returnType, err = typeElaborator.executionPredefinedTypeExpression(
					function.Position, function.ReturnType, function.ReturnTypeExpression,
				)
				if err != nil {
					if elaborationError, ok := err.(*TypeError); ok {
						return nil, typeError(function.Position, "function %q return type %q: %s",
							function.Name, function.ReturnType, elaborationError.Message)
					}
					return nil, typeError(function.Position, "function %q return type %q: %v",
						function.Name, function.ReturnType, err)
				}
			}
			functionKey := compiledFunctionKey(function, parameters, returnType)
			if seenFunctions[functionKey] {
				return nil, typeError(function.Position, "duplicate %s function signature %q in interface %q", function.Mode, function.Name, declaration.Name)
			}
			seenFunctions[functionKey] = true
			switch function.Mode {
			case FunctionProvides:
				builder.ProvidesFunction(function.Name, returnType, parameters...)
			case FunctionRequires:
				builder.RequiresFunction(function.Name, returnType, parameters...)
			case FunctionPrivate:
				// Private function objects are represented in the attached
				// structural type but are not executable through arch.FunctionDecl
				// until private function selection and module membership land.
			default:
				return nil, typeError(function.Position, "function %q has unsupported interface region %q", function.Name, function.Mode)
			}
		}
		if interfaceNeedsStructuralDescriptor(declaration) {
			structuralType, err := typeElaborator.interfaceType(key)
			if err != nil {
				return nil, err
			}
			builder.StructuralType(structuralType)
		}
		compiledInterface := builder.Build()
		states, stateBindings, err := compileInterfaceBehaviorStates(declaration)
		if err != nil {
			return nil, err
		}
		behavior, err := compileInterfaceBehavior(declaration, stateBindings)
		if err != nil {
			return nil, err
		}
		compiledInterfaces[key] = compiledInterface
		compiledBehaviorStates[key] = states
		compiledBehaviors[key] = behavior
		rules, err := compileInterfaceBehaviorRules(declaration, stateBindings, typeElaborator)
		if err != nil {
			return nil, err
		}
		compiledBehaviorRules[key] = rules
		executionDeclaration := declaration
		executionDeclaration.Actions = execution.actions
		executionDeclaration.Functions = execution.functions
		executionDeclaration.Exceptions = execution.exceptions
		executionDeclaration.Constraints = execution.constraints
		if err := validateInterfaceConstraints(executionDeclaration); err != nil {
			return nil, err
		}
	}

	moduleDeclarations := make(map[string]ModuleDecl, len(file.Modules))
	parameterlessModules := make(map[string]compiledModuleDecl, len(file.Modules))
	for _, declaration := range file.Modules {
		declaration.Exceptions = mergeVisibleExceptionDeclarations(
			visibleOutermostExceptions(file.Exceptions, declaration.Position),
			declaration.Exceptions...,
		)
		key := folded(declaration.Name)
		if key == "" {
			return nil, typeError(declaration.Position, "module generator has no name")
		}
		if _, exists := moduleDeclarations[key]; exists {
			return nil, typeError(declaration.Position, "duplicate module generator %q", declaration.Name)
		}
		if err := validateModuleGeneratorParameters(declaration, typeElaborator); err != nil {
			return nil, err
		}
		_, returnDeclaration, err := typeElaborator.interfaceDeclaration(declaration.Position, declaration.ReturnType)
		if err != nil {
			return nil, typeError(declaration.Position, "module %q return type %q: %v",
				declaration.Name, declaration.ReturnType, err)
		}
		if len(declaration.Parameters) != 0 {
			if err := validateModuleObjectNameConflicts(declaration); err != nil {
				return nil, err
			}
			expansion, err := typeElaborator.executionInterfaceExpansion(returnDeclaration)
			if err != nil {
				return nil, err
			}
			executionReturn := expandedExecutionInterfaceDeclaration(returnDeclaration, expansion)
			if err := validateSupportedModuleMembership(declaration, returnDeclaration, executionReturn); err != nil {
				return nil, err
			}
		}
		moduleDeclarations[key] = declaration
		if len(declaration.Parameters) == 0 {
			compiled, err := compileModuleDeclaration(declaration, nil, nil, typeElaborator)
			if err != nil {
				return nil, err
			}
			parameterlessModules[key] = compiled
		}
	}

	architectures := make(map[string]ArchitectureDecl, len(file.Architectures))
	for _, declaration := range file.Architectures {
		key := folded(declaration.Name)
		if key == "" || architectures[key].Name != "" {
			return nil, typeError(declaration.Position, "duplicate architecture %q", declaration.Name)
		}
		architectures[key] = declaration
	}
	if architectureName == "" {
		if len(file.Architectures) != 1 {
			return nil, typeError(Position{Line: 1, Column: 1}, "architecture name is required when the file declares %d architectures", len(file.Architectures))
		}
		architectureName = file.Architectures[0].Name
	}
	declaration, ok := architectures[folded(architectureName)]
	if !ok {
		return nil, typeError(Position{Line: 1, Column: 1}, "architecture %q is not declared", architectureName)
	}

	architectureBindings, generatorArguments, err := compileArchitectureGeneratorArguments(
		declaration.Position, declaration.Parameters, arguments, typeElaborator,
	)
	if err != nil {
		return nil, err
	}
	result := arch.NewArchitecture(declaration.Name)
	if err := result.SetGeneratorArguments(generatorArguments...); err != nil {
		return nil, typeError(declaration.Position, "architecture %q generator arguments: %v", declaration.Name, err)
	}
	returnExecutionDeclaration := InterfaceDecl{Name: "Root"}
	returnInterface := arch.Interface("Root").Build()
	returnServices := map[string]sourceServiceInstance(nil)
	if !keyword(declaration.ReturnType, "Root") {
		returnInterfaceKey, declaredReturn, err := typeElaborator.interfaceDeclaration(
			declaration.ReturnTypeExpression.Position, declaration.ReturnType,
		)
		if err != nil {
			return nil, typeError(declaration.ReturnTypeExpression.Position,
				"architecture %q return type %q: %v", declaration.Name, declaration.ReturnType, err)
		}
		returnExecution, err := typeElaborator.executionInterfaceExpansion(declaredReturn)
		if err != nil {
			return nil, err
		}
		returnExecutionDeclaration = declaredReturn
		returnExecutionDeclaration.Actions = returnExecution.actions
		returnExecutionDeclaration.Functions = returnExecution.functions
		returnExecutionDeclaration.Exceptions = returnExecution.exceptions
		returnExecutionDeclaration.Constraints = returnExecution.constraints
		returnInterface = compiledInterfaces[returnInterfaceKey]
		returnServices, err = typeElaborator.serviceInstances(declaredReturn)
		if err != nil {
			return nil, err
		}
	}
	if err := result.SetReturnInterface(returnInterface); err != nil {
		return nil, typeError(declaration.Position, "%v", err)
	}
	componentDeclarations, err := elaborateClosedComponentArrays(
		declaration.Components, architectureBindings, typeElaborator,
	)
	if err != nil {
		return nil, err
	}
	components := make(map[string]ComponentDecl, len(componentDeclarations))
	componentInterfaces := make(map[string]InterfaceDecl, len(componentDeclarations)+1)
	componentServices := make(map[string]map[string]sourceServiceInstance, len(componentDeclarations)+1)
	componentSpellings := make(map[string]string, len(componentDeclarations)+1)
	componentInterfaces[""] = returnExecutionDeclaration
	componentServices[""] = returnServices
	componentSpellings[""] = arch.ArchitectureInterfaceID
	activeArchitectureGenerators := map[string]bool{folded(declaration.Name): true}
	for _, component := range componentDeclarations {
		key := folded(component.Name)
		if components[key].Name != "" {
			return nil, typeError(component.Position, "duplicate component %q", component.Name)
		}
		declaredInterfaceKey := folded(component.InterfaceType)
		if _, isInterface := interfaces[declaredInterfaceKey]; !isInterface {
			if _, isAlias := typeElaborator.aliases[declaredInterfaceKey]; !isAlias {
				return nil, typeError(component.Position,
					"component %q uses undeclared interface type %q", component.Name, component.InterfaceType)
			}
		}
		interfaceKey, interfaceDecl, err := typeElaborator.interfaceDeclaration(component.Position, component.InterfaceType)
		if err != nil {
			return nil, typeError(component.Position, "component %q interface type %q: %v",
				component.Name, component.InterfaceType, err)
		}
		execution, err := typeElaborator.executionInterfaceExpansion(interfaceDecl)
		if err != nil {
			return nil, err
		}
		executionInterfaceDecl := interfaceDecl
		executionInterfaceDecl.Actions = execution.actions
		executionInterfaceDecl.Functions = execution.functions
		executionInterfaceDecl.Exceptions = execution.exceptions
		executionInterfaceDecl.Constraints = execution.constraints
		serviceInstances, err := typeElaborator.serviceInstances(interfaceDecl)
		if err != nil {
			return nil, err
		}
		if component.ArchitectureLiteral != nil {
			if component.Module != "" || len(component.ModuleArguments) != 0 {
				return nil, typeError(component.Position,
					"component %q mixes an architecture literal with a generator application", component.Name)
			}
			if err := lowerNestedArchitectureInstance(
				result, component, *component.ArchitectureLiteral, interfaceKey,
				arch.ArchitectureInterfaceID, "", activeArchitectureGenerators,
				architectureBindings, architectures, interfaces, compiledInterfaces,
				compiledBehaviorStates, compiledBehaviors, compiledBehaviorRules, compiledInterfaceExceptions,
				moduleDeclarations, parameterlessModules, typeElaborator,
			); err != nil {
				return nil, err
			}
			components[key] = component
			componentInterfaces[key] = executionInterfaceDecl
			componentServices[key] = serviceInstances
			componentSpellings[key] = component.Name
			continue
		}
		if component.Module != "" {
			generatorKey := folded(component.Module)
			_, moduleExists := moduleDeclarations[generatorKey]
			childArchitecture, architectureExists := architectures[generatorKey]
			if moduleExists && architectureExists {
				return nil, typeError(component.Position,
					"component %q generator name %q is ambiguous between a module and an architecture",
					component.Name, component.Module)
			}
			if architectureExists {
				if err := lowerNestedArchitectureInstance(
					result, component, childArchitecture, interfaceKey,
					arch.ArchitectureInterfaceID, generatorKey, activeArchitectureGenerators,
					architectureBindings, architectures, interfaces, compiledInterfaces,
					compiledBehaviorStates, compiledBehaviors, compiledBehaviorRules, compiledInterfaceExceptions,
					moduleDeclarations, parameterlessModules, typeElaborator,
				); err != nil {
					return nil, err
				}
				components[key] = component
				componentInterfaces[key] = executionInterfaceDecl
				componentServices[key] = serviceInstances
				componentSpellings[key] = component.Name
				continue
			}
		}
		instance := arch.NewComponent(component.Name, compiledInterfaces[interfaceKey], nil)
		var moduleSource *ModuleDecl
		var moduleStateBindings map[string]behaviorBinding
		var moduleInstance *compiledModuleDecl
		if component.Module == "" {
			if len(component.ModuleArguments) != 0 {
				return nil, typeError(component.Position,
					"component %q has module actuals without a module generator", component.Name)
			}
			if err := instance.DeclareState(compiledBehaviorStates[interfaceKey]...); err != nil {
				return nil, typeError(component.Position, "component %q behavior state: %v", component.Name, err)
			}
			for _, implementation := range compiledBehaviors[interfaceKey] {
				if err := instance.AddFunctionImplementation(implementation); err != nil {
					return nil, typeError(component.Position, "component %q behavior: %v", component.Name, err)
				}
			}
			for _, rule := range compiledBehaviorRules[interfaceKey] {
				if err := instance.AddDeclarativeRule(rule); err != nil {
					return nil, typeError(component.Position, "component %q behavior rule: %v", component.Name, err)
				}
			}
			for _, exception := range compiledInterfaceExceptions[interfaceKey] {
				if err := instance.AddExceptionDeclaration(exception); err != nil {
					return nil, typeError(component.Position,
						"component %q behavior exception: %v", component.Name, err)
				}
			}
		} else {
			moduleDeclaration, exists := moduleDeclarations[folded(component.Module)]
			if !exists {
				return nil, typeError(component.Position, "component %q instantiates undeclared module generator %q", component.Name, component.Module)
			}
			formalBindings, moduleArguments, err := compileModuleGeneratorArguments(
				component, moduleDeclaration, architectureBindings, typeElaborator,
			)
			if err != nil {
				return nil, err
			}
			module, cached := parameterlessModules[folded(component.Module)]
			if !cached {
				module, err = compileModuleDeclaration(
					moduleDeclaration, formalBindings, moduleArguments, typeElaborator,
				)
				if err != nil {
					return nil, err
				}
			}
			moduleInterfaceKey, _, err := typeElaborator.interfaceDeclaration(module.declaration.Position, module.declaration.ReturnType)
			if err != nil {
				return nil, err
			}
			if moduleInterfaceKey != interfaceKey {
				return nil, typeError(component.Position,
					"component %q interface %q does not exactly match module %q return interface %q; structural conformance is outside the current source subset",
					component.Name, component.InterfaceType, module.declaration.Name, module.declaration.ReturnType)
			}
			moduleSourceDeclaration := module.declaration
			moduleSource = &moduleSourceDeclaration
			moduleInstance = &module
			moduleStateBindings = module.stateBindings
			if err := instance.SetModuleMembershipWithArgumentsAndRecordObjects(
				module.declaration.Name, module.generatorArguments,
				module.typeDenotations, module.objectDenotations, module.recordObjects,
			); err != nil {
				return nil, typeError(component.Position,
					"component %q module membership: %v", component.Name, err)
			}
			if err := instance.DeclareState(module.states...); err != nil {
				return nil, typeError(component.Position, "component %q module state: %v", component.Name, err)
			}
			if len(module.initialParameters) != 0 {
				if err := instance.SetModuleInitializationParameters(module.initialParameters...); err != nil {
					return nil, typeError(component.Position,
						"component %q module initialization parameters: %v", component.Name, err)
				}
			}
			for _, implementation := range module.functions {
				if err := instance.AddFunctionImplementation(implementation); err != nil {
					return nil, typeError(component.Position, "component %q module function: %v", component.Name, err)
				}
			}
			for _, exception := range module.exceptions {
				if err := instance.AddExceptionDeclaration(exception); err != nil {
					return nil, typeError(component.Position, "component %q module exception: %v", component.Name, err)
				}
			}
			if module.handler != nil {
				if err := instance.SetModuleExceptionHandler(*module.handler); err != nil {
					return nil, typeError(component.Position, "component %q module handler: %v", component.Name, err)
				}
			}
			if len(module.initial) != 0 {
				if err := instance.SetInitialStatements(module.initial...); err != nil {
					return nil, typeError(component.Position, "component %q module initial part: %v", component.Name, err)
				}
			}
			if len(module.final) != 0 {
				if err := instance.SetFinalStatements(module.final...); err != nil {
					return nil, typeError(component.Position, "component %q module final part: %v", component.Name, err)
				}
			}
			for _, clock := range module.clocks {
				if err := instance.AddBasicClock(clock.Name); err != nil {
					return nil, typeError(clock.Position, "component %q module clock: %v", component.Name, err)
				}
			}
			if len(module.processes) != 0 {
				if err := instance.SetModuleProcessMode(module.mode); err != nil {
					return nil, typeError(component.Position, "component %q module process mode: %v", component.Name, err)
				}
			}
			for _, process := range module.processes {
				if err := instance.AddDeclarativeProcess(process); err != nil {
					return nil, typeError(component.Position, "component %q module process: %v", component.Name, err)
				}
			}
		}
		componentConstraints, err := compileModuleInstanceConstraints(component.Name, executionInterfaceDecl, moduleSource, moduleStateBindings)
		if err != nil {
			return nil, err
		}
		var componentBehaviorConstraint *arch.BehaviorConformanceConstraint
		if moduleSource != nil {
			componentBehaviorConstraint, err = compileComponentBehaviorConstraint(
				component.Position, component.Name, interfaceDecl, compiledInterfaces[interfaceKey],
			)
			if err != nil {
				return nil, err
			}
		}
		serviceBehaviorConstraints, err := compileServiceBehaviorConstraints(
			serviceInstances, compiledInterfaces,
		)
		if err != nil {
			return nil, err
		}
		if (componentBehaviorConstraint != nil || len(serviceBehaviorConstraints) != 0) && componentConstraints == nil {
			componentConstraints = constraint.NewConstraintSet(
				"rpd:component:" + folded(component.Name) + ":constraints",
			)
		}
		if componentBehaviorConstraint != nil {
			componentConstraints.Add(componentBehaviorConstraint)
		}
		for _, checker := range serviceBehaviorConstraints {
			componentConstraints.Add(checker)
		}
		if componentConstraints != nil {
			instance.SetModuleConstraints(componentConstraints)
		}
		if err := result.AddComponent(instance); err != nil {
			return nil, typeError(component.Position, "%v", err)
		}
		if component.Module != "" {
			for _, connection := range moduleInstance.connections {
				connectionID := "rpd:module:" + folded(moduleInstance.declaration.Name) + ":" + folded(component.Name) + ":" + connection.semanticKey
				if connection.function {
					declaration := arch.ConnectFunction(
						component.Name, connection.requiredFunction,
						component.Name, connection.providedFunction,
					).WithinModule().WithinArchitecture(arch.ArchitectureInterfaceID).IdentifiedBy(connectionID).Build()
					if err := result.AddFunctionConnection(declaration); err != nil {
						return nil, typeError(connection.position,
							"component %q module function connection: %v", component.Name, err)
					}
					continue
				}
				builder := arch.Connect(component.Name, component.Name).WithinModule().
					IdentifiedBy(connectionID).
					On(connection.trigger)
				if len(connection.outputParameters) == 0 {
					builder.Send(connection.targetAction)
				} else {
					builder.SendParameters(connection.targetAction, connection.outputParameters...)
				}
				if connection.kind == arch.PipeConnection {
					builder.Pipe()
				} else if connection.kind == arch.AgentConnection {
					builder.Agent()
				}
				if err := result.AddConnection(builder.Build()); err != nil {
					return nil, typeError(connection.position, "component %q module connection: %v", component.Name, err)
				}
			}
		}
		components[key] = component
		componentInterfaces[key] = executionInterfaceDecl
		componentServices[key] = serviceInstances
		componentSpellings[key] = component.Name
	}
	constraints, err := compileArchitectureConstraints(
		declaration, componentInterfaces, componentSpellings, architectureBindings,
	)
	if err != nil {
		return nil, err
	}
	if constraints != nil {
		result.WithConstraints(constraints, constraint.CheckAfter)
	}

	if err := lowerArchitectureConnections(
		result, declaration.Connections, declaration.ConnectionGenerators,
		"architecture connection generator", architectureBindings,
		componentInterfaces, componentServices, componentSpellings,
		typeElaborator, arch.ArchitectureInterfaceID,
	); err != nil {
		return nil, err
	}
	initial, initialExceptions, err := compileArchitectureInitial(
		declaration, returnExecutionDeclaration, architectureBindings,
	)
	if err != nil {
		return nil, err
	}
	if len(initial) != 0 {
		if len(initialExceptions) != 0 {
			if err := result.SetDeterministicArchitectureInitialExceptionDeclarations(
				arch.ArchitectureInterfaceID, initialExceptions...,
			); err != nil {
				return nil, typeError(declaration.Position,
					"architecture %q initial exceptions: %v", declaration.Name, err)
			}
		}
		if err := result.SetDeterministicArchitectureInitialStatements(
			arch.ArchitectureInterfaceID, initial...,
		); err != nil {
			return nil, typeError(declaration.Position,
				"architecture %q initial part: %v", declaration.Name, err)
		}
	}
	if _, err := result.DeterministicModelDigest(); err != nil {
		return nil, typeError(declaration.Position, "lowered deterministic model is invalid: %v", err)
	}
	return result, nil
}

func lowerArchitectureConnections(
	result *arch.Architecture,
	declarations []ConnectionDecl,
	generators []ConnectionGeneratorDecl,
	generatorContext string,
	architectureBindings map[string]behaviorBinding,
	componentInterfaces map[string]InterfaceDecl,
	componentServices map[string]map[string]sourceServiceInstance,
	componentSpellings map[string]string,
	typeElaborator *sourceTypeElaborator,
	owner string,
) error {
	connectionDeclarations, err := elaborateClosedConnectionGenerators(
		declarations, generators, generatorContext, architectureBindings, typeElaborator,
	)
	if err != nil {
		return err
	}
	seenConnections := make(map[string]bool, len(connectionDeclarations))
	connections, err := elaborateArchitectureServiceConnections(
		connectionDeclarations, componentInterfaces, componentServices, typeElaborator,
	)
	if err != nil {
		return err
	}
	sort.Slice(connections, func(i, j int) bool {
		return connectionSemanticKey(connections[i]) < connectionSemanticKey(connections[j])
	})
	for _, connection := range connections {
		sourceComponentKey := folded(connection.Source.Component)
		targetComponentKey := folded(connection.Target.Component)
		targetInterface, targetExists := componentInterfaces[targetComponentKey]
		if !targetExists {
			return typeError(connection.Target.Position, "connection target component %q is not declared", connection.Target.Component)
		}
		if connection.SourcePattern != nil &&
			(connection.SourcePattern.Kind != BehaviorBasicPattern || hasUniversalPlaceholder(connection.Placeholders)) {
			if err := lowerCompoundActionConnection(
				result, connection, targetInterface, componentInterfaces, componentSpellings,
				seenConnections, owner,
			); err != nil {
				return err
			}
			continue
		}
		if connection.SourcePattern != nil && connection.SourcePattern.Event.Attribute != "" {
			return typeError(connection.SourcePattern.Event.Position,
				"function Call/Return attribute connection triggers are outside the current source subset")
		}
		sourceInterface, sourceExists := componentInterfaces[sourceComponentKey]
		if !sourceExists {
			return typeError(connection.Source.Position, "connection source component %q is not declared", connection.Source.Component)
		}
		sourceAction, sourceActionExists := findAction(sourceInterface, connection.Source.Action)
		targetAction, targetActionExists := findAction(targetInterface, connection.Target.Action)
		sourceFunctions := findFunctions(sourceInterface, connection.Source.Action)
		targetFunctions := findFunctions(targetInterface, connection.Target.Action)
		if connection.Constituent == ConnectionActionConstituent {
			sourceFunctions, targetFunctions = nil, nil
		} else if connection.Constituent == ConnectionFunctionConstituent {
			sourceActionExists, targetActionExists = false, false
		}
		if sourceActionExists && len(sourceFunctions) != 0 {
			return typeError(connection.Source.Position, "connection source %s.%s is ambiguous between an action and a function", connection.Source.Component, connection.Source.Action)
		}
		if targetActionExists && len(targetFunctions) != 0 {
			return typeError(connection.Target.Position, "connection target %s.%s is ambiguous between an action and a function", connection.Target.Component, connection.Target.Action)
		}
		if len(sourceFunctions) != 0 || len(targetFunctions) != 0 {
			if len(sourceFunctions) == 0 {
				return typeError(connection.Source.Position, "function connection source %s.%s is not declared", connection.Source.Component, connection.Source.Action)
			}
			if len(targetFunctions) == 0 {
				return typeError(connection.Target.Position, "function connection target %s.%s is not declared", connection.Target.Component, connection.Target.Action)
			}
			if err := lowerFunctionConnection(
				result, connection, sourceFunctions, targetFunctions,
				componentSpellings[sourceComponentKey], componentSpellings[targetComponentKey],
				seenConnections, owner,
			); err != nil {
				return err
			}
			continue
		}
		if !sourceActionExists {
			return typeError(connection.Source.Position, "source action %s.%s is not declared", connection.Source.Component, connection.Source.Action)
		}
		if !targetActionExists {
			return typeError(connection.Target.Position, "target action %s.%s is not declared", connection.Target.Component, connection.Target.Action)
		}
		sourceMode := ActionOut
		sourceDirection := "out"
		if connection.Source.Component == "" {
			sourceMode = ActionIn
			sourceDirection = "in"
		}
		if sourceAction.Mode != sourceMode {
			return typeError(connection.Source.Position, "connection source %s.%s is not an %s action", connection.Source.Component, sourceAction.Name, sourceDirection)
		}
		targetMode := ActionIn
		targetDirection := "in"
		if connection.Target.Component == "" {
			targetMode = ActionOut
			targetDirection = "out"
		}
		if targetAction.Mode != targetMode {
			return typeError(connection.Target.Position, "connection target %s.%s is not an %s action", connection.Target.Component, targetAction.Name, targetDirection)
		}
		bindings, err := compileConnectionBindings(connection.Placeholders, "connection")
		if err != nil {
			return err
		}
		bound := make(map[string]bool, len(bindings))
		sourcePattern := connectionSourcePattern(connection)
		trigger, err := compileSourcePattern(sourcePattern, bindings, bound, "connection", func(event BehaviorEventDecl) (ActionDecl, string, error) {
			if !keyword(event.Component, connection.Source.Component) || !keyword(event.Name, sourceAction.Name) {
				return ActionDecl{}, "", typeError(event.Position, "basic connection source pattern must name %s.%s", connection.Source.Component, sourceAction.Name)
			}
			return sourceAction, componentSpellings[sourceComponentKey], nil
		})
		if err != nil {
			return err
		}
		trigger, err = compileUniversalQualifications(trigger, connection.Placeholders, bound, "connection")
		if err != nil {
			return err
		}
		for _, placeholder := range connection.Placeholders {
			if !bound[folded(placeholder.Name)] {
				return typeError(placeholder.Position, "placeholder %s is never bound by the source pattern", patternPlaceholderDisplay(placeholder))
			}
		}
		trigger, err = compileConnectionGuard(trigger, connection, bindings, "connection")
		if err != nil {
			return err
		}
		var outputParameters []arch.ConnectionParameter
		targetExpressions := actionRefArgumentExpressions(connection.Target)
		if len(targetExpressions) == 0 {
			if !compatiblePassthrough(sourceAction, targetAction) {
				if len(sourcePattern.Event.Arguments) == 0 && len(connection.Placeholders) == 0 {
					return typeError(connection.Position, "connection %s.%s %s %s.%s requires identical parameter names and types when argument lists are omitted",
						connection.Source.Component, sourceAction.Name, connection.Connector,
						connection.Target.Component, targetAction.Name)
				}
				return typeError(connection.Target.Position, "an omitted target argument list requires identical source and target parameter names and types")
			}
		} else {
			if len(targetExpressions) != len(targetAction.Parameters) {
				return typeError(connection.Target.Position, "target action %s.%s has %d parameters but the pattern supplies %d arguments",
					connection.Target.Component, targetAction.Name, len(targetAction.Parameters), len(targetExpressions))
			}
			outputParameters, err = compileConnectionTargetExpressions(
				connection.Target, targetAction, bindings, "connection target",
			)
			if err != nil {
				return err
			}
		}
		semanticKey := connectionSemanticKey(connection)
		if seenConnections[semanticKey] {
			return typeError(connection.Position, "duplicate connection %s", semanticKey)
		}
		seenConnections[semanticKey] = true
		connectionID := "rpd:" + semanticKey
		if owner != arch.ArchitectureInterfaceID {
			connectionID = "rpd:architecture:" + folded(owner) + ":" + semanticKey
		}
		builder := arch.Connect(componentSpellings[sourceComponentKey], componentSpellings[targetComponentKey]).
			WithinArchitecture(owner).
			IdentifiedBy(connectionID).
			On(trigger)
		if len(outputParameters) == 0 {
			builder.Send(targetAction.Name)
		} else {
			builder.SendParameters(targetAction.Name, outputParameters...)
		}
		switch connection.Connector {
		case ConnectPipe:
			builder.Pipe()
		case ConnectAgent:
			builder.Agent()
		}
		if err := result.AddConnection(builder.Build()); err != nil {
			return typeError(connection.Position, "%v", err)
		}
	}
	return nil
}

func lowerNestedArchitectureInstance(
	result *arch.Architecture,
	component ComponentDecl,
	declaration ArchitectureDecl,
	expectedInterfaceKey string,
	parentOwner string,
	generatorKey string,
	activeArchitectureGenerators map[string]bool,
	parentBindings map[string]behaviorBinding,
	architectures map[string]ArchitectureDecl,
	interfaces map[string]InterfaceDecl,
	compiledInterfaces map[string]*arch.InterfaceDecl,
	compiledBehaviorStates map[string][]arch.StateDeclaration,
	compiledBehaviors map[string][]*arch.FunctionImplementation,
	compiledBehaviorRules map[string][]*arch.DeclarativeRule,
	compiledInterfaceExceptions map[string][]arch.ExceptionDeclaration,
	moduleDeclarations map[string]ModuleDecl,
	parameterlessModules map[string]compiledModuleDecl,
	typeElaborator *sourceTypeElaborator,
) error {
	if generatorKey != "" {
		if activeArchitectureGenerators[generatorKey] {
			return typeError(component.Position,
				"recursive static architecture generator application %q at component %q has no finite elaboration",
				declaration.Name, component.Name)
		}
		activeArchitectureGenerators[generatorKey] = true
		defer delete(activeArchitectureGenerators, generatorKey)
	}
	ownerID := arch.DeterministicArchitectureInstanceID(parentOwner, component.Name)
	bindings, arguments, err := compileArchitectureComponentArguments(
		component, declaration, parentBindings, typeElaborator,
	)
	if err != nil {
		return err
	}
	if keyword(declaration.ReturnType, "Root") {
		return typeError(component.Position,
			"nested architecture %q returns Root and cannot implement component %q of interface %q",
			declaration.Name, component.Name, component.InterfaceType)
	}
	returnInterfaceKey, returnDeclaration, err := typeElaborator.interfaceDeclaration(
		declaration.ReturnTypeExpression.Position, declaration.ReturnType,
	)
	if err != nil {
		return typeError(declaration.ReturnTypeExpression.Position,
			"architecture %q return type %q: %v", declaration.Name, declaration.ReturnType, err)
	}
	if returnInterfaceKey != expectedInterfaceKey {
		return typeError(component.Position,
			"component %q interface %q does not exactly match architecture %q return interface %q; structural conformance is outside the current source subset",
			component.Name, component.InterfaceType, declaration.Name, declaration.ReturnType)
	}
	returnExecution, err := typeElaborator.executionInterfaceExpansion(returnDeclaration)
	if err != nil {
		return err
	}
	returnExecutionDeclaration := returnDeclaration
	returnExecutionDeclaration.Actions = returnExecution.actions
	returnExecutionDeclaration.Functions = returnExecution.functions
	returnExecutionDeclaration.Exceptions = returnExecution.exceptions
	returnExecutionDeclaration.Constraints = returnExecution.constraints
	returnServices, err := typeElaborator.serviceInstances(returnDeclaration)
	if err != nil {
		return err
	}
	if err := result.AddDeterministicArchitectureInstance(arch.ArchitectureInstanceWithin(
		parentOwner, component.Name, declaration.Name, compiledInterfaces[returnInterfaceKey], arguments...,
	)); err != nil {
		return typeError(component.Position, "component %q architecture instance: %v", component.Name, err)
	}

	componentDeclarations, err := elaborateClosedComponentArrays(
		declaration.Components, bindings, typeElaborator,
	)
	if err != nil {
		return err
	}
	componentInterfaces := make(map[string]InterfaceDecl, len(componentDeclarations)+1)
	componentServices := make(map[string]map[string]sourceServiceInstance, len(componentDeclarations)+1)
	componentSpellings := make(map[string]string, len(componentDeclarations)+1)
	componentInterfaces[""] = returnExecutionDeclaration
	componentServices[""] = returnServices
	componentSpellings[""] = ownerID
	seenComponents := make(map[string]bool, len(componentDeclarations))
	for _, childComponent := range componentDeclarations {
		localName := childComponent.Name
		localKey := folded(localName)
		if seenComponents[localKey] {
			return typeError(childComponent.Position,
				"duplicate component %q in nested architecture %q", localName, declaration.Name)
		}
		seenComponents[localKey] = true
		declaredInterfaceKey := folded(childComponent.InterfaceType)
		if _, isInterface := interfaces[declaredInterfaceKey]; !isInterface {
			if _, isAlias := typeElaborator.aliases[declaredInterfaceKey]; !isAlias {
				return typeError(childComponent.Position,
					"component %q uses undeclared interface type %q", localName, childComponent.InterfaceType)
			}
		}
		childInterfaceKey, childInterfaceDeclaration, err := typeElaborator.interfaceDeclaration(
			childComponent.Position, childComponent.InterfaceType,
		)
		if err != nil {
			return typeError(childComponent.Position,
				"component %q interface type %q: %v", localName, childComponent.InterfaceType, err)
		}
		childExecution, err := typeElaborator.executionInterfaceExpansion(childInterfaceDeclaration)
		if err != nil {
			return err
		}
		childExecutionInterface := childInterfaceDeclaration
		childExecutionInterface.Actions = childExecution.actions
		childExecutionInterface.Functions = childExecution.functions
		childExecutionInterface.Exceptions = childExecution.exceptions
		childExecutionInterface.Constraints = childExecution.constraints
		childServices, err := typeElaborator.serviceInstances(childInterfaceDeclaration)
		if err != nil {
			return err
		}
		if childComponent.ArchitectureLiteral != nil {
			if childComponent.Module != "" || len(childComponent.ModuleArguments) != 0 {
				return typeError(childComponent.Position,
					"component %q mixes an architecture literal with a generator application", localName)
			}
			if err := lowerNestedArchitectureInstance(
				result, childComponent, *childComponent.ArchitectureLiteral, childInterfaceKey,
				ownerID, "", activeArchitectureGenerators,
				bindings, architectures, interfaces, compiledInterfaces,
				compiledBehaviorStates, compiledBehaviors, compiledBehaviorRules, compiledInterfaceExceptions,
				moduleDeclarations, parameterlessModules, typeElaborator,
			); err != nil {
				return err
			}
			componentInterfaces[localKey] = childExecutionInterface
			componentServices[localKey] = childServices
			componentSpellings[localKey] = arch.DeterministicArchitectureInstanceID(ownerID, localName)
			continue
		}
		if childComponent.Module != "" {
			generatorKey := folded(childComponent.Module)
			_, moduleExists := moduleDeclarations[generatorKey]
			childArchitecture, architectureExists := architectures[generatorKey]
			if moduleExists && architectureExists {
				return typeError(childComponent.Position,
					"component %q generator name %q is ambiguous between a module and an architecture",
					localName, childComponent.Module)
			}
			if architectureExists {
				if err := lowerNestedArchitectureInstance(
					result, childComponent, childArchitecture, childInterfaceKey,
					ownerID, generatorKey, activeArchitectureGenerators,
					bindings, architectures, interfaces, compiledInterfaces,
					compiledBehaviorStates, compiledBehaviors, compiledBehaviorRules, compiledInterfaceExceptions,
					moduleDeclarations, parameterlessModules, typeElaborator,
				); err != nil {
					return err
				}
				componentInterfaces[localKey] = childExecutionInterface
				componentServices[localKey] = childServices
				componentSpellings[localKey] = arch.DeterministicArchitectureInstanceID(ownerID, localName)
				continue
			}
		}
		globalComponent := childComponent
		globalComponent.Name = arch.DeterministicArchitectureComponentID(ownerID, localName)
		executionInterface, services, err := lowerNestedOrdinaryComponent(
			result, globalComponent, ownerID, bindings,
			interfaces, compiledInterfaces, compiledBehaviorStates, compiledBehaviors,
			compiledBehaviorRules, compiledInterfaceExceptions,
			moduleDeclarations, parameterlessModules, typeElaborator,
		)
		if err != nil {
			return err
		}
		componentInterfaces[localKey] = executionInterface
		componentServices[localKey] = services
		componentSpellings[localKey] = globalComponent.Name
	}
	constraints, err := compileArchitectureConstraints(
		declaration, componentInterfaces, componentSpellings, bindings,
	)
	if err != nil {
		return err
	}
	if constraints != nil {
		if err := result.SetDeterministicArchitectureInstanceConstraints(ownerID, constraints); err != nil {
			return typeError(component.Position,
				"component %q architecture constraints: %v", component.Name, err)
		}
	}
	if err := lowerArchitectureConnections(
		result, declaration.Connections, declaration.ConnectionGenerators,
		"nested architecture connection generator", bindings,
		componentInterfaces, componentServices, componentSpellings,
		typeElaborator, ownerID,
	); err != nil {
		return err
	}
	initial, initialExceptions, err := compileArchitectureInitial(declaration, returnExecutionDeclaration, bindings)
	if err != nil {
		return err
	}
	if len(initial) != 0 {
		if len(initialExceptions) != 0 {
			if err := result.SetDeterministicArchitectureInitialExceptionDeclarations(ownerID, initialExceptions...); err != nil {
				return typeError(component.Position,
					"component %q architecture initial exceptions: %v", component.Name, err)
			}
		}
		if err := result.SetDeterministicArchitectureInitialStatements(ownerID, initial...); err != nil {
			return typeError(component.Position,
				"component %q architecture initial part: %v", component.Name, err)
		}
	}
	return nil
}

func nestedComponentID(instanceID, localID string) string {
	return arch.DeterministicArchitectureComponentID(instanceID, localID)
}

func lowerNestedOrdinaryComponent(
	result *arch.Architecture,
	component ComponentDecl,
	owner string,
	architectureBindings map[string]behaviorBinding,
	interfaces map[string]InterfaceDecl,
	compiledInterfaces map[string]*arch.InterfaceDecl,
	compiledBehaviorStates map[string][]arch.StateDeclaration,
	compiledBehaviors map[string][]*arch.FunctionImplementation,
	compiledBehaviorRules map[string][]*arch.DeclarativeRule,
	compiledInterfaceExceptions map[string][]arch.ExceptionDeclaration,
	moduleDeclarations map[string]ModuleDecl,
	parameterlessModules map[string]compiledModuleDecl,
	typeElaborator *sourceTypeElaborator,
) (InterfaceDecl, map[string]sourceServiceInstance, error) {
	declaredInterfaceKey := folded(component.InterfaceType)
	if _, isInterface := interfaces[declaredInterfaceKey]; !isInterface {
		if _, isAlias := typeElaborator.aliases[declaredInterfaceKey]; !isAlias {
			return InterfaceDecl{}, nil, typeError(component.Position,
				"component %q uses undeclared interface type %q", component.Name, component.InterfaceType)
		}
	}
	interfaceKey, interfaceDeclaration, err := typeElaborator.interfaceDeclaration(
		component.Position, component.InterfaceType,
	)
	if err != nil {
		return InterfaceDecl{}, nil, typeError(component.Position,
			"component %q interface type %q: %v", component.Name, component.InterfaceType, err)
	}
	execution, err := typeElaborator.executionInterfaceExpansion(interfaceDeclaration)
	if err != nil {
		return InterfaceDecl{}, nil, err
	}
	executionInterface := interfaceDeclaration
	executionInterface.Actions = execution.actions
	executionInterface.Functions = execution.functions
	executionInterface.Exceptions = execution.exceptions
	executionInterface.Constraints = execution.constraints
	services, err := typeElaborator.serviceInstances(interfaceDeclaration)
	if err != nil {
		return InterfaceDecl{}, nil, err
	}
	instance := arch.NewComponent(component.Name, compiledInterfaces[interfaceKey], nil)
	var moduleSource *ModuleDecl
	var moduleStateBindings map[string]behaviorBinding
	var moduleInstance *compiledModuleDecl
	if component.Module == "" {
		if len(component.ModuleArguments) != 0 {
			return InterfaceDecl{}, nil, typeError(component.Position,
				"component %q has module actuals without a module generator", component.Name)
		}
		if err := instance.DeclareState(compiledBehaviorStates[interfaceKey]...); err != nil {
			return InterfaceDecl{}, nil, typeError(component.Position,
				"component %q behavior state: %v", component.Name, err)
		}
		for _, implementation := range compiledBehaviors[interfaceKey] {
			if err := instance.AddFunctionImplementation(implementation); err != nil {
				return InterfaceDecl{}, nil, typeError(component.Position,
					"component %q behavior: %v", component.Name, err)
			}
		}
		for _, rule := range compiledBehaviorRules[interfaceKey] {
			if err := instance.AddDeclarativeRule(rule); err != nil {
				return InterfaceDecl{}, nil, typeError(component.Position,
					"component %q behavior rule: %v", component.Name, err)
			}
		}
		for _, exception := range compiledInterfaceExceptions[interfaceKey] {
			if err := instance.AddExceptionDeclaration(exception); err != nil {
				return InterfaceDecl{}, nil, typeError(component.Position,
					"component %q behavior exception: %v", component.Name, err)
			}
		}
	} else {
		moduleDeclaration, exists := moduleDeclarations[folded(component.Module)]
		if !exists {
			return InterfaceDecl{}, nil, typeError(component.Position,
				"component %q instantiates undeclared module generator %q", component.Name, component.Module)
		}
		formalBindings, moduleArguments, err := compileModuleGeneratorArguments(
			component, moduleDeclaration, architectureBindings, typeElaborator,
		)
		if err != nil {
			return InterfaceDecl{}, nil, err
		}
		module, cached := parameterlessModules[folded(component.Module)]
		if !cached {
			module, err = compileModuleDeclaration(
				moduleDeclaration, formalBindings, moduleArguments, typeElaborator,
			)
			if err != nil {
				return InterfaceDecl{}, nil, err
			}
		}
		moduleInterfaceKey, _, err := typeElaborator.interfaceDeclaration(
			module.declaration.Position, module.declaration.ReturnType,
		)
		if err != nil {
			return InterfaceDecl{}, nil, err
		}
		if moduleInterfaceKey != interfaceKey {
			return InterfaceDecl{}, nil, typeError(component.Position,
				"component %q interface %q does not exactly match module %q return interface %q; structural conformance is outside the current source subset",
				component.Name, component.InterfaceType, module.declaration.Name, module.declaration.ReturnType)
		}
		moduleSourceDeclaration := module.declaration
		moduleSource = &moduleSourceDeclaration
		moduleInstance = &module
		moduleStateBindings = module.stateBindings
		if err := instance.SetModuleMembershipWithArgumentsAndRecordObjects(
			module.declaration.Name, module.generatorArguments,
			module.typeDenotations, module.objectDenotations, module.recordObjects,
		); err != nil {
			return InterfaceDecl{}, nil, typeError(component.Position,
				"component %q module membership: %v", component.Name, err)
		}
		if err := instance.DeclareState(module.states...); err != nil {
			return InterfaceDecl{}, nil, typeError(component.Position,
				"component %q module state: %v", component.Name, err)
		}
		if len(module.initialParameters) != 0 {
			if err := instance.SetModuleInitializationParameters(module.initialParameters...); err != nil {
				return InterfaceDecl{}, nil, typeError(component.Position,
					"component %q module initialization parameters: %v", component.Name, err)
			}
		}
		for _, implementation := range module.functions {
			if err := instance.AddFunctionImplementation(implementation); err != nil {
				return InterfaceDecl{}, nil, typeError(component.Position,
					"component %q module function: %v", component.Name, err)
			}
		}
		for _, exception := range module.exceptions {
			if err := instance.AddExceptionDeclaration(exception); err != nil {
				return InterfaceDecl{}, nil, typeError(component.Position,
					"component %q module exception: %v", component.Name, err)
			}
		}
		if module.handler != nil {
			if err := instance.SetModuleExceptionHandler(*module.handler); err != nil {
				return InterfaceDecl{}, nil, typeError(component.Position,
					"component %q module handler: %v", component.Name, err)
			}
		}
		if len(module.initial) != 0 {
			if err := instance.SetInitialStatements(module.initial...); err != nil {
				return InterfaceDecl{}, nil, typeError(component.Position,
					"component %q module initial part: %v", component.Name, err)
			}
		}
		if len(module.final) != 0 {
			if err := instance.SetFinalStatements(module.final...); err != nil {
				return InterfaceDecl{}, nil, typeError(component.Position,
					"component %q module final part: %v", component.Name, err)
			}
		}
		for _, clock := range module.clocks {
			if err := instance.AddBasicClock(clock.Name); err != nil {
				return InterfaceDecl{}, nil, typeError(clock.Position,
					"component %q module clock: %v", component.Name, err)
			}
		}
		if len(module.processes) != 0 {
			if err := instance.SetModuleProcessMode(module.mode); err != nil {
				return InterfaceDecl{}, nil, typeError(component.Position,
					"component %q module process mode: %v", component.Name, err)
			}
		}
		for _, process := range module.processes {
			if err := instance.AddDeclarativeProcess(process); err != nil {
				return InterfaceDecl{}, nil, typeError(component.Position,
					"component %q module process: %v", component.Name, err)
			}
		}
	}
	componentConstraints, err := compileModuleInstanceConstraints(
		component.Name, executionInterface, moduleSource, moduleStateBindings,
	)
	if err != nil {
		return InterfaceDecl{}, nil, err
	}
	var componentBehaviorConstraint *arch.BehaviorConformanceConstraint
	if moduleSource != nil {
		componentBehaviorConstraint, err = compileComponentBehaviorConstraint(
			component.Position, component.Name, interfaceDeclaration, compiledInterfaces[interfaceKey],
		)
		if err != nil {
			return InterfaceDecl{}, nil, err
		}
	}
	serviceBehaviorConstraints, err := compileServiceBehaviorConstraints(services, compiledInterfaces)
	if err != nil {
		return InterfaceDecl{}, nil, err
	}
	if (componentBehaviorConstraint != nil || len(serviceBehaviorConstraints) != 0) && componentConstraints == nil {
		componentConstraints = constraint.NewConstraintSet(
			"rpd:component:" + folded(component.Name) + ":constraints",
		)
	}
	if componentBehaviorConstraint != nil {
		componentConstraints.Add(componentBehaviorConstraint)
	}
	for _, checker := range serviceBehaviorConstraints {
		componentConstraints.Add(checker)
	}
	if componentConstraints != nil {
		instance.SetModuleConstraints(componentConstraints)
	}
	if err := result.AddComponentInArchitecture(instance, owner); err != nil {
		return InterfaceDecl{}, nil, typeError(component.Position, "%v", err)
	}
	if moduleInstance != nil {
		for _, connection := range moduleInstance.connections {
			connectionID := "rpd:module:" + folded(moduleInstance.declaration.Name) + ":" + folded(component.Name) + ":" + connection.semanticKey
			if connection.function {
				declaration := arch.ConnectFunction(
					component.Name, connection.requiredFunction,
					component.Name, connection.providedFunction,
				).WithinModule().WithinArchitecture(owner).IdentifiedBy(connectionID).Build()
				if err := result.AddFunctionConnection(declaration); err != nil {
					return InterfaceDecl{}, nil, typeError(connection.position,
						"component %q module function connection: %v", component.Name, err)
				}
				continue
			}
			builder := arch.Connect(component.Name, component.Name).
				WithinModule().WithinArchitecture(owner).
				IdentifiedBy(connectionID).
				On(connection.trigger)
			if len(connection.outputParameters) == 0 {
				builder.Send(connection.targetAction)
			} else {
				builder.SendParameters(connection.targetAction, connection.outputParameters...)
			}
			if connection.kind == arch.PipeConnection {
				builder.Pipe()
			} else if connection.kind == arch.AgentConnection {
				builder.Agent()
			}
			if err := result.AddConnection(builder.Build()); err != nil {
				return InterfaceDecl{}, nil, typeError(connection.position,
					"component %q module connection: %v", component.Name, err)
			}
		}
	}
	return executionInterface, services, nil
}

func compileArchitectureComponentArguments(
	component ComponentDecl,
	declaration ArchitectureDecl,
	parentBindings map[string]behaviorBinding,
	typeElaborator *sourceTypeElaborator,
) (map[string]behaviorBinding, []arch.ArchitectureGeneratorArgument, error) {
	formals := component.ModuleArgumentFormals
	if len(formals) == 0 && len(component.ModuleArguments) != 0 {
		formals = make([]string, len(component.ModuleArguments))
	}
	if len(formals) != len(component.ModuleArguments) {
		return nil, nil, typeError(component.Position,
			"component %q has %d architecture arguments but %d association descriptors",
			component.Name, len(component.ModuleArguments), len(formals))
	}
	parameterIndices := make(map[string]int, len(declaration.Parameters))
	for index, parameter := range declaration.Parameters {
		key := folded(parameter.Name)
		if key == "" {
			return nil, nil, typeError(parameter.Position,
				"architecture generator %q has an empty parameter name", declaration.Name)
		}
		if _, duplicate := parameterIndices[key]; duplicate {
			return nil, nil, typeError(parameter.Position,
				"architecture generator %q has duplicate parameter %q", declaration.Name, parameter.Name)
		}
		parameterIndices[key] = index
	}
	actuals := make(map[int]ExpressionDecl, len(component.ModuleArguments))
	named := false
	nextPositional := 0
	for index, expression := range component.ModuleArguments {
		formal := formals[index]
		if formal == "" {
			if named {
				return nil, nil, typeError(expression.Position,
					"component %q positional architecture arguments must precede named associations", component.Name)
			}
			if nextPositional >= len(declaration.Parameters) {
				return nil, nil, typeError(expression.Position,
					"architecture generator %q expects at most %d actual parameters but component %q supplies more",
					declaration.Name, len(declaration.Parameters), component.Name)
			}
			actuals[nextPositional] = expression
			nextPositional++
			continue
		}
		named = true
		parameterIndex, exists := parameterIndices[folded(formal)]
		if !exists {
			return nil, nil, typeError(expression.Position,
				"architecture generator %q has no formal parameter named %q", declaration.Name, formal)
		}
		if _, duplicate := actuals[parameterIndex]; duplicate {
			return nil, nil, typeError(expression.Position,
				"component %q supplies architecture parameter %q more than once",
				component.Name, declaration.Parameters[parameterIndex].Name)
		}
		actuals[parameterIndex] = expression
	}
	bindings := make(map[string]behaviorBinding, len(declaration.Parameters))
	arguments := make([]arch.ArchitectureGeneratorArgument, len(declaration.Parameters))
	for index, parameter := range declaration.Parameters {
		typeName, err := typeElaborator.executionPredefinedTypeExpression(
			parameter.Position, parameter.Type, parameter.TypeExpression,
		)
		if err != nil {
			return nil, nil, err
		}
		if !sourceClosedScalarType(typeName) {
			return nil, nil, typeError(parameter.Position,
				"architecture generator parameter %q has type %s; the explicit elaboration-object subset supports predefined scalar types",
				parameter.Name, typeName)
		}
		defaultValue, hasDefault, err := evaluateClosedGeneratorDefault(
			parameter, typeName, "architecture generator",
		)
		if err != nil {
			return nil, nil, err
		}
		value := defaultValue
		if expression, supplied := actuals[index]; supplied {
			compiled, err := compileBehaviorExpression(expression, parentBindings, nil)
			if err != nil {
				return nil, nil, typeError(expression.Position,
					"component %q architecture argument %d for %q: %v",
					component.Name, index+1, parameter.Name, err)
			}
			if !sourceBehaviorExpressionAssignable(compiled, typeName) {
				return nil, nil, typeError(expression.Position,
					"component %q architecture argument %d has type %s, want %s for %q",
					component.Name, index+1, compiled.typeName, typeName, parameter.Name)
			}
			var evaluatedType string
			value, evaluatedType, err = arch.EvaluateConstant(compiled.value)
			if err != nil {
				return nil, nil, typeError(expression.Position,
					"component %q architecture argument %d is not a closed deterministic constant: %v",
					component.Name, index+1, err)
			}
			if !gorapide.CanonicalValueMatchesPredefinedType(value, typeName) {
				return nil, nil, typeError(expression.Position,
					"component %q architecture argument %d evaluates as %s, want %s",
					component.Name, index+1, evaluatedType, typeName)
			}
		} else if !hasDefault {
			return nil, nil, typeError(parameter.Position,
				"architecture generator %q parameter %q requires an explicit %s argument for component %q",
				declaration.Name, parameter.Name, typeName, component.Name)
		}
		constant := arch.LiteralValue(value)
		bindings[folded(parameter.Name)] = behaviorBinding{
			name: parameter.Name, typeName: typeName, constant: &constant,
		}
		arguments[index] = arch.ArchitectureArgument(parameter.Name, typeName, value)
	}
	return bindings, arguments, nil
}

func compileArchitectureConstraints(
	declaration ArchitectureDecl,
	componentInterfaces map[string]InterfaceDecl,
	componentSpellings map[string]string,
	architectureBindings map[string]behaviorBinding,
) (*constraint.ConstraintSet, error) {
	type scopedConstraint struct {
		source ConstraintDecl
		key    string
	}
	declarations := make([]scopedConstraint, 0, len(declaration.Constraints))
	for _, source := range declaration.Constraints {
		declarations = append(declarations, scopedConstraint{
			source: source, key: "architecture:" + constraintDeclarationIdentity(source),
		})
	}
	if len(declarations) == 0 {
		return nil, nil
	}
	sort.Slice(declarations, func(i, j int) bool {
		return declarations[i].key < declarations[j].key
	})
	set := constraint.NewConstraintSet("rpd:" + folded(declaration.Name) + ":constraints")
	seen := make(map[string]bool, len(declarations))
	for _, scoped := range declarations {
		source, key := scoped.source, scoped.key
		if seen[key] {
			return nil, typeError(source.Position, "duplicate architecture constraint %s", key)
		}
		seen[key] = true
		resolve := func(event BehaviorEventDecl) (ActionDecl, string, error) {
			componentName, err := substituteComponentArraySelection(
				event.Component, event.ComponentIndex, architectureBindings, "architecture constraint",
			)
			if err != nil {
				return ActionDecl{}, "", err
			}
			componentKey := folded(componentName)
			interfaceDeclaration, ok := componentInterfaces[componentKey]
			if !ok {
				return ActionDecl{}, "", typeError(event.Position, "architecture constraint component %q is not declared", componentName)
			}
			action, ok, err := findPatternAction(interfaceDeclaration, event)
			if err != nil {
				return ActionDecl{}, "", typeError(event.Position, "architecture constraint: %v", err)
			}
			if !ok {
				return ActionDecl{}, "", typeError(event.Position, "architecture constraint action %s.%s is not declared", componentName, event.Name)
			}
			if action.Mode == ActionPrivate {
				return ActionDecl{}, "", typeError(event.Position, "architecture constraint cannot observe private action %s.%s", componentName, action.Name)
			}
			return action, componentSpellings[componentKey], nil
		}
		compiled, err := compileConstraintDeclaration(
			source, "rpd:"+key, "architecture constraint", nil, "", resolve,
		)
		if err != nil {
			return nil, err
		}
		set.Add(compiled)
	}
	return set, nil
}

func compileModuleInstanceConstraints(
	componentName string,
	declaration InterfaceDecl,
	module *ModuleDecl,
	moduleStates map[string]behaviorBinding,
) (*constraint.ConstraintSet, error) {
	type scopedConstraint struct {
		source ConstraintDecl
		key    string
		states map[string]behaviorBinding
	}
	declarations := make([]scopedConstraint, 0, len(declaration.Constraints))
	for _, source := range declaration.Constraints {
		declarations = append(declarations, scopedConstraint{
			source: source,
			key:    "interface:" + folded(declaration.Name) + ":" + constraintDeclarationIdentity(source),
		})
	}
	if module != nil {
		for _, source := range module.Constraints {
			declarations = append(declarations, scopedConstraint{
				source: source,
				key:    "module:" + folded(module.Name) + ":" + constraintDeclarationIdentity(source),
				states: moduleStates,
			})
		}
	}
	if len(declarations) == 0 {
		return nil, nil
	}
	sort.Slice(declarations, func(i, j int) bool { return declarations[i].key < declarations[j].key })
	set := constraint.NewConstraintSet("rpd:component:" + folded(componentName) + ":constraints")
	seen := make(map[string]bool, len(declarations))
	for _, scoped := range declarations {
		source, key := scoped.source, scoped.key
		if seen[key] {
			return nil, typeError(source.Position, "duplicate module-visible constraint %s", key)
		}
		seen[key] = true
		resolve := func(event BehaviorEventDecl) (ActionDecl, string, error) {
			if event.Component != "" {
				return ActionDecl{}, "", typeError(event.Position, "module-visible constraint action %q cannot be component-qualified", event.Component+"."+event.Name)
			}
			action, ok, err := findPatternAction(declaration, event)
			if err != nil {
				return ActionDecl{}, "", typeError(event.Position, "module-visible constraint: %v", err)
			}
			if !ok {
				return ActionDecl{}, "", typeError(event.Position, "module-visible constraint action %q is not declared", event.Name)
			}
			return action, componentName, nil
		}
		compiled, err := compileConstraintDeclaration(
			source, "rpd:"+key, "module-visible constraint", scoped.states, componentName, resolve,
		)
		if err != nil {
			return nil, err
		}
		set.Add(compiled)
	}
	return set, nil
}

func compileServiceBehaviorConstraints(
	instances map[string]sourceServiceInstance,
	compiledInterfaces map[string]*arch.InterfaceDecl,
) ([]*arch.BehaviorConformanceConstraint, error) {
	paths := make([]string, 0, len(instances))
	for path, instance := range instances {
		if instance.target.Behavior != nil {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	result := make([]*arch.BehaviorConformanceConstraint, 0, len(paths))
	for _, path := range paths {
		instance := instances[path]
		target := instance.target
		if err := validateServiceBehaviorConstraintSubset(instance.position, path, target); err != nil {
			return nil, err
		}
		canonicalTarget := canonicalServiceBehaviorDeclaration(target)
		shadowRules, err := compileInterfaceBehaviorRules(canonicalTarget, nil, nil)
		if err != nil {
			return nil, err
		}
		compiled := compiledInterfaces[folded(target.Name)]
		if compiled == nil {
			return nil, typeError(instance.position,
				"service %q behavior target %q has no compiled interface", path, target.Name)
		}
		behaviorInterface, err := directBehaviorInterface(target, compiled)
		if err != nil {
			return nil, typeError(instance.position, "service %q behavior target %q: %v", path, target.Name, err)
		}
		checker, err := arch.NewBehaviorConformanceConstraint(
			"rpd:service:"+folded(path)+":behavior:"+folded(target.Name),
			path, behaviorInterface, shadowRules,
			serviceBehaviorSemanticDigest(target, behaviorInterface),
		)
		if err != nil {
			return nil, typeError(instance.position,
				"service %q target %q behavior constraint: %v", path, target.Name, err)
		}
		result = append(result, checker)
	}
	return result, nil
}

// compileComponentBehaviorConstraint implements Architecture LRM sections
// 2.6.1 and 2.7 for a module-denoted interface object. Its behavior part is a
// constraint on the concrete module computation, not a second executable
// controller. The bounded shadow used here is isolated from production.
func compileComponentBehaviorConstraint(
	position Position,
	componentName string,
	declaration InterfaceDecl,
	compiled *arch.InterfaceDecl,
) (*arch.BehaviorConformanceConstraint, error) {
	if declaration.Behavior == nil {
		return nil, nil
	}
	if err := validateBehaviorConstraintSubset(position, "component", componentName, declaration); err != nil {
		return nil, err
	}
	canonicalDeclaration := canonicalServiceBehaviorDeclaration(declaration)
	shadowRules, err := compileInterfaceBehaviorRules(canonicalDeclaration, nil, nil)
	if err != nil {
		return nil, err
	}
	if compiled == nil {
		return nil, typeError(position,
			"component %q behavior target %q has no compiled interface", componentName, declaration.Name)
	}
	behaviorInterface, err := directBehaviorInterface(declaration, compiled)
	if err != nil {
		return nil, typeError(position,
			"component %q behavior target %q: %v", componentName, declaration.Name, err)
	}
	checker, err := arch.NewComponentBehaviorConformanceConstraint(
		"rpd:component:"+folded(componentName)+":behavior:"+folded(declaration.Name),
		componentName, behaviorInterface, shadowRules,
		serviceBehaviorSemanticDigest(declaration, behaviorInterface),
	)
	if err != nil {
		return nil, typeError(position,
			"component %q target %q behavior constraint: %v", componentName, declaration.Name, err)
	}
	return checker, nil
}

func canonicalServiceBehaviorDeclaration(source InterfaceDecl) InterfaceDecl {
	result := cloneInterfaceForNormalization(source)
	result.Name = folded(result.Name)
	for actionIndex := range result.Actions {
		action := &result.Actions[actionIndex]
		action.Name = folded(action.Name)
		for parameterIndex := range action.Parameters {
			action.Parameters[parameterIndex].Name = folded(action.Parameters[parameterIndex].Name)
		}
	}
	if source.Behavior == nil {
		result.Behavior = nil
		return result
	}
	behavior := &BehaviorDecl{
		Position: source.Behavior.Position,
		Rules:    make([]BehaviorRuleDecl, len(source.Behavior.Rules)),
	}
	for ruleIndex, sourceRule := range source.Behavior.Rules {
		rule := sourceRule
		rule.Placeholders = append([]ParameterDecl(nil), sourceRule.Placeholders...)
		for index := range rule.Placeholders {
			rule.Placeholders[index].Name = folded(rule.Placeholders[index].Name)
		}
		rule.Trigger = cloneBehaviorPatternDeclaration(sourceRule.Trigger)
		canonicalizeServiceBehaviorPattern(&rule.Trigger)
		rule.Guard = cloneExpressionDeclarationPointer(sourceRule.Guard)
		canonicalizeServiceBehaviorExpression(rule.Guard)
		rule.Statements = make([]BehaviorStatementDecl, len(sourceRule.Statements))
		for index, statement := range sourceRule.Statements {
			copy := statement
			copy.Call.Name = folded(copy.Call.Name)
			copy.Call.ArgumentFormals = append([]string(nil), statement.Call.ArgumentFormals...)
			for formalIndex := range copy.Call.ArgumentFormals {
				copy.Call.ArgumentFormals[formalIndex] = folded(copy.Call.ArgumentFormals[formalIndex])
			}
			copy.Call.Arguments = make([]ExpressionDecl, len(statement.Call.Arguments))
			for argumentIndex, argument := range statement.Call.Arguments {
				copy.Call.Arguments[argumentIndex] = cloneExpressionDeclaration(argument)
				canonicalizeServiceBehaviorExpression(&copy.Call.Arguments[argumentIndex])
			}
			rule.Statements[index] = copy
		}
		behavior.Rules[ruleIndex] = rule
	}
	result.Behavior = behavior
	return result
}

func canonicalizeServiceBehaviorPattern(source *BehaviorPatternDecl) {
	if source == nil {
		return
	}
	source.Event.Component = folded(source.Event.Component)
	canonicalizeServiceBehaviorExpression(source.Event.ComponentIndex)
	source.Event.Name = folded(source.Event.Name)
	source.Event.Attribute = folded(source.Event.Attribute)
	for index := range source.Event.Arguments {
		source.Event.Arguments[index].Formal = folded(source.Event.Arguments[index].Formal)
		canonicalizeServiceBehaviorExpression(&source.Event.Arguments[index].Actual)
	}
	canonicalizeServiceBehaviorPattern(source.Left)
	canonicalizeServiceBehaviorPattern(source.Right)
	canonicalizeServiceBehaviorPattern(source.Inner)
}

func canonicalizeServiceBehaviorExpression(source *ExpressionDecl) {
	if source == nil {
		return
	}
	source.Name = folded(source.Name)
	canonicalizeServiceBehaviorExpression(source.Left)
	canonicalizeServiceBehaviorExpression(source.Right)
}

func serviceBehaviorSemanticDigest(
	declaration InterfaceDecl,
	compiled *arch.InterfaceDecl,
) string {
	actionKeys := make([]string, 0, len(compiled.Actions))
	for _, action := range compiled.Actions {
		var builder strings.Builder
		builder.WriteString(strconv.Itoa(int(action.Kind)))
		builder.WriteByte(':')
		builder.WriteString(folded(action.Name))
		builder.WriteByte('(')
		for index, parameter := range action.Params {
			if index != 0 {
				builder.WriteByte(',')
			}
			builder.WriteString(folded(parameter.Name))
			builder.WriteByte(':')
			builder.WriteString(folded(parameter.Type))
		}
		builder.WriteByte(')')
		actionKeys = append(actionKeys, builder.String())
	}
	sort.Strings(actionKeys)
	ruleKeys := make([]string, 0, len(declaration.Behavior.Rules))
	for _, rule := range declaration.Behavior.Rules {
		ruleKeys = append(ruleKeys, behaviorRuleSemanticKey(rule))
	}
	sort.Strings(ruleKeys)
	encoded := strings.Join(actionKeys, ";") + "|" + strings.Join(ruleKeys, ";")
	digest := sha256.Sum256([]byte(encoded))
	return "sha256:" + hex.EncodeToString(digest[:])
}

func directBehaviorInterface(
	declaration InterfaceDecl,
	compiled *arch.InterfaceDecl,
) (*arch.InterfaceDecl, error) {
	actions := make([]arch.ActionDecl, 0, len(declaration.Actions))
	seen := make(map[string]bool, len(declaration.Actions))
	for _, source := range declaration.Actions {
		key := folded(source.Name)
		if seen[key] {
			return nil, fmt.Errorf("duplicate direct action %q", source.Name)
		}
		seen[key] = true
		found := false
		for _, candidate := range compiled.Actions {
			if folded(candidate.Name) != key {
				continue
			}
			copy := candidate
			copy.Name = key
			copy.Params = append([]arch.ParamDecl(nil), candidate.Params...)
			for parameterIndex := range copy.Params {
				copy.Params[parameterIndex].Name = folded(copy.Params[parameterIndex].Name)
			}
			actions = append(actions, copy)
			found = true
			break
		}
		if !found {
			return nil, fmt.Errorf("direct action %q is missing from execution interface", source.Name)
		}
	}
	return &arch.InterfaceDecl{Name: folded(declaration.Name), Actions: actions}, nil
}

func validateServiceBehaviorConstraintSubset(
	position Position,
	path string,
	declaration InterfaceDecl,
) error {
	return validateBehaviorConstraintSubset(position, "service", path, declaration)
}

func validateBehaviorConstraintSubset(
	position Position,
	subject string,
	path string,
	declaration InterfaceDecl,
) error {
	behavior := declaration.Behavior
	if behavior == nil {
		return nil
	}
	if len(behavior.States) != 0 {
		return typeError(position,
			"%s %q target %q behavior state is outside the current finite stateless conformance subset",
			subject, path, declaration.Name)
	}
	if len(behavior.Functions) != 0 {
		return typeError(position,
			"%s %q target %q behavior functions are outside the current action-only conformance subset",
			subject, path, declaration.Name)
	}
	edges := make(map[string]map[string]bool)
	ruleTriggers := make(map[string]bool)
	for _, rule := range behavior.Rules {
		if rule.Trigger.Kind != BehaviorBasicPattern {
			return typeError(rule.Position,
				"%s %q target %q behavior compound triggers are outside the current finite conformance subset",
				subject, path, declaration.Name)
		}
		trigger := folded(rule.Trigger.Event.Name)
		if trigger == "" {
			return typeError(rule.Position, "%s %q target %q behavior has an empty trigger", subject, path, declaration.Name)
		}
		ruleTriggers[trigger] = true
		for _, statement := range rule.Statements {
			switch statement.Kind {
			case BehaviorNullStatement:
				continue
			case BehaviorCallStatement:
				if statement.Call.Timing != nil {
					return typeError(statement.Position,
						"%s %q target %q behavior timing is outside the current conformance subset",
						subject, path, declaration.Name)
				}
				action, exists := findAction(declaration, statement.Call.Name)
				if !exists {
					if len(findFunctions(declaration, statement.Call.Name)) != 0 {
						return typeError(statement.Position,
							"%s %q target %q behavior function calls are outside the current action-only conformance subset",
							subject, path, declaration.Name)
					}
					return typeError(statement.Position,
						"%s %q target %q behavior call %q is not a direct action",
						subject, path, declaration.Name, statement.Call.Name)
				}
				if action.Mode != ActionOut {
					return typeError(statement.Position,
						"%s %q target %q behavior cannot generate non-out action %q",
						subject, path, declaration.Name, action.Name)
				}
				if edges[trigger] == nil {
					edges[trigger] = make(map[string]bool)
				}
				edges[trigger][folded(action.Name)] = true
			default:
				return typeError(statement.Position,
					"%s %q target %q behavior statement %q is outside the current action-only conformance subset",
					subject, path, declaration.Name, statement.Kind)
			}
		}
	}
	if cycle := behaviorActionCycle(edges); len(cycle) != 0 {
		return typeError(position,
			"%s %q target %q behavior action cycle %s is outside the finite conformance subset",
			subject, path, declaration.Name, strings.Join(cycle, " -> "))
	}
	reachable := make(map[string]bool)
	frontier := make([]string, 0)
	for _, action := range declaration.Actions {
		if action.Mode != ActionIn {
			continue
		}
		name := folded(action.Name)
		if !reachable[name] {
			reachable[name] = true
			frontier = append(frontier, name)
		}
	}
	sort.Strings(frontier)
	for len(frontier) != 0 {
		current := frontier[0]
		frontier = frontier[1:]
		targets := make([]string, 0, len(edges[current]))
		for target := range edges[current] {
			targets = append(targets, target)
		}
		sort.Strings(targets)
		for _, target := range targets {
			if reachable[target] {
				continue
			}
			reachable[target] = true
			frontier = append(frontier, target)
		}
	}
	unreachable := make([]string, 0)
	for trigger := range ruleTriggers {
		if !reachable[trigger] {
			unreachable = append(unreachable, trigger)
		}
	}
	if len(unreachable) != 0 {
		sort.Strings(unreachable)
		return typeError(position,
			"%s %q target %q behavior trigger %q is not reachable from a direct in action in the current closed conformance subset",
			subject, path, declaration.Name, unreachable[0])
	}
	return nil
}

func behaviorActionCycle(edges map[string]map[string]bool) []string {
	const (
		unvisited = iota
		visiting
		complete
	)
	states := make(map[string]int)
	stack := make([]string, 0)
	var visit func(string) []string
	visit = func(current string) []string {
		states[current] = visiting
		stack = append(stack, current)
		targets := make([]string, 0, len(edges[current]))
		for target := range edges[current] {
			targets = append(targets, target)
		}
		sort.Strings(targets)
		for _, target := range targets {
			switch states[target] {
			case visiting:
				start := 0
				for index, name := range stack {
					if name == target {
						start = index
						break
					}
				}
				cycle := append([]string(nil), stack[start:]...)
				return append(cycle, target)
			case unvisited:
				if cycle := visit(target); len(cycle) != 0 {
					return cycle
				}
			}
		}
		stack = stack[:len(stack)-1]
		states[current] = complete
		return nil
	}
	nodes := make([]string, 0, len(edges))
	for source, targets := range edges {
		nodes = append(nodes, source)
		for target := range targets {
			if _, exists := states[target]; !exists {
				states[target] = unvisited
			}
		}
	}
	sort.Strings(nodes)
	for _, node := range nodes {
		if states[node] == unvisited {
			if cycle := visit(node); len(cycle) != 0 {
				return cycle
			}
		}
	}
	return nil
}

func constraintDeclarationIdentity(source ConstraintDecl) string {
	if source.Label != "" {
		return "label:" + folded(source.Label)
	}
	return "source:" + constraintSemanticKey(source)
}

func constraintSemanticKey(source ConstraintDecl) string {
	alphabet := make([]string, 0, len(source.Alphabet))
	for _, filter := range source.Alphabet {
		alphabet = append(alphabet, behaviorPatternKey(filter))
	}
	sort.Strings(alphabet)
	alphabet = uniqueFoldedStrings(alphabet)
	components := make([]string, 0, len(source.Components))
	for _, component := range source.Components {
		components = append(components, constraintComponentOrderingKey(component))
	}
	sort.Strings(components)
	return "from:" + strings.Join(alphabet, ",") + ":components:" + strings.Join(components, "|")
}

func constraintComponentSemanticKey(source ConstraintComponentDecl) string {
	placeholders := make([]string, 0, len(source.Placeholders))
	for _, placeholder := range source.Placeholders {
		placeholders = append(placeholders, patternPlaceholderSemanticKey(placeholder))
	}
	sort.Strings(placeholders)
	guard := ""
	if source.Guard != nil {
		guard = ":where:" + behaviorExpressionKey(*source.Guard)
	}
	return string(source.Kind) + ":" + strings.Join(placeholders, ";") + ":" + behaviorPatternKey(source.Pattern) + guard
}

func patternPlaceholderSemanticKey(placeholder ParameterDecl) string {
	if placeholder.Qualification == PlaceholderUniversal {
		return fmt.Sprintf("!%s:%s range %d..%d by %s",
			folded(placeholder.Name), folded(placeholder.Type),
			placeholder.RangeFirst, placeholder.RangeLast, folded(placeholder.Relation))
	}
	return "?" + folded(placeholder.Name) + ":" + folded(placeholder.Type)
}

func patternPlaceholderDisplay(placeholder ParameterDecl) string {
	prefix := "?"
	if placeholder.Qualification == PlaceholderUniversal {
		prefix = "!"
	}
	return prefix + placeholder.Name
}

func constraintComponentOrderingKey(source ConstraintComponentDecl) string {
	label := "source"
	if source.Label != "" {
		label = "label:" + folded(source.Label)
	}
	return label + ":" + constraintComponentSemanticKey(source)
}

func uniqueFoldedStrings(values []string) []string {
	write := 0
	for _, value := range values {
		if write > 0 && values[write-1] == value {
			continue
		}
		values[write] = value
		write++
	}
	return values[:write]
}

func compileConstraintAlphabet(
	source []BehaviorPatternDecl,
	context string,
	resolve sourcePatternActionResolver,
) ([]pattern.Pattern, error) {
	result := make([]pattern.Pattern, 0, len(source))
	for _, filter := range source {
		compiled, err := compileSourcePattern(filter, map[string]behaviorBinding{}, map[string]bool{}, context+" alphabet filter", resolve)
		if err != nil {
			return nil, err
		}
		if _, err := pattern.DeterministicSingleEventKey(compiled); err != nil {
			return nil, typeError(filter.Position, "%s requires a closed basic pattern: %v", context, err)
		}
		result = append(result, compiled)
	}
	return result, nil
}

func validateInterfaceConstraints(declaration InterfaceDecl) error {
	seen := make(map[string]bool, len(declaration.Constraints))
	for _, source := range declaration.Constraints {
		key := constraintDeclarationIdentity(source)
		if seen[key] {
			return typeError(source.Position, "duplicate interface constraint %s", key)
		}
		seen[key] = true
		resolve := func(event BehaviorEventDecl) (ActionDecl, string, error) {
			if event.Component != "" {
				return ActionDecl{}, "", typeError(event.Position, "interface constraint action %q cannot be component-qualified", event.Component+"."+event.Name)
			}
			action, ok, err := findPatternAction(declaration, event)
			if err != nil {
				return ActionDecl{}, "", typeError(event.Position, "interface constraint: %v", err)
			}
			if !ok {
				return ActionDecl{}, "", typeError(event.Position, "interface constraint action %q is not declared", event.Name)
			}
			return action, "validation", nil
		}
		if _, err := compileConstraintDeclaration(
			source, "rpd:validation:"+key, "interface constraint", nil, "", resolve,
		); err != nil {
			return err
		}
	}
	return nil
}

func compileConstraintDeclaration(
	source ConstraintDecl,
	name string,
	context string,
	states map[string]behaviorBinding,
	stateOwner string,
	resolve sourcePatternActionResolver,
) (*constraint.Constraint, error) {
	if len(source.Components) == 0 {
		return nil, typeError(source.Position, "%s has no body components", context)
	}
	alphabet, err := compileConstraintAlphabet(source.Alphabet, context, resolve)
	if err != nil {
		return nil, err
	}
	builder := constraint.NewConstraint(name)
	if len(alphabet) != 0 {
		builder.FilterFrom(alphabet...)
	}
	components := append([]ConstraintComponentDecl(nil), source.Components...)
	sort.Slice(components, func(i, j int) bool {
		return constraintComponentOrderingKey(components[i]) < constraintComponentOrderingKey(components[j])
	})
	seenLabels := make(map[string]bool, len(components))
	seenClauses := make(map[string]bool, len(components))
	for _, component := range components {
		if component.Label != "" {
			label := folded(component.Label)
			if seenLabels[label] {
				return nil, typeError(component.Position, "duplicate %s body label %q", context, component.Label)
			}
			seenLabels[label] = true
		}
		clauseName := "source"
		if len(components) != 1 || component.Label != "" {
			if component.Label != "" {
				clauseName = "label:" + folded(component.Label)
			} else {
				clauseName = "source:" + constraintComponentSemanticKey(component)
			}
		}
		if seenClauses[clauseName] {
			return nil, typeError(component.Position, "duplicate %s body component %s", context, clauseName)
		}
		seenClauses[clauseName] = true
		bindings, err := compilePatternBindings(component.Placeholders, "constraint")
		if err != nil {
			return nil, err
		}
		bound := make(map[string]bool, len(bindings))
		compiled, err := compileConstraintPattern(component, bindings, states, stateOwner, bound, "constraint", resolve)
		if err != nil {
			return nil, err
		}
		for _, placeholder := range component.Placeholders {
			if !bound[folded(placeholder.Name)] {
				return nil, typeError(placeholder.Position, "constraint placeholder %s is never bound by the pattern", patternPlaceholderDisplay(placeholder))
			}
		}
		switch component.Kind {
		case ConstraintNever:
			builder.MustNever(clauseName, compiled, "source never constraint matched")
		case ConstraintNotMatch:
			builder.MustNotMatch(clauseName, compiled, "source negative match constraint exactly matched the associated computation")
		case ConstraintMatch:
			builder.Must(clauseName, compiled, "source match constraint did not match the whole associated computation")
		default:
			return nil, typeError(component.Position, "%s has unsupported body kind %q", context, component.Kind)
		}
	}
	return builder.Build(), nil
}

type behaviorBinding struct {
	name                              string
	typeName                          string
	timingClock                       string
	constant                          *arch.RuleValue
	placeholder                       bool
	universal                         bool
	state                             bool
	moduleSelf                        bool
	moduleNew                         bool
	moduleNewParameters               []ParameterDecl
	moduleNewInitialParameters        []ParameterDecl
	moduleNewInitializationParameters []arch.ModuleInitializationParameter
	moduleNewArguments                []arch.ModuleGeneratorArgument
	moduleValue                       bool
	recordFields                      map[string]behaviorBinding
	structural                        bool
}

func evaluateClosedGeneratorDefault(
	parameter ParameterDecl,
	typeName string,
	context string,
) (any, bool, error) {
	if parameter.Default == nil {
		return nil, false, nil
	}
	compiled, err := compileBehaviorExpression(*parameter.Default, nil, nil)
	if err != nil {
		return nil, false, typeError(parameter.Default.Position,
			"%s parameter %q default is not a closed deterministic expression: %v",
			context, parameter.Name, err)
	}
	if !sourceBehaviorExpressionAssignable(compiled, typeName) {
		return nil, false, typeError(parameter.Default.Position,
			"%s parameter %q default has type %s, want %s",
			context, parameter.Name, compiled.typeName, typeName)
	}
	value, evaluatedType, err := arch.EvaluateConstant(compiled.value)
	if err != nil {
		return nil, false, typeError(parameter.Default.Position,
			"%s parameter %q default is not a closed deterministic constant: %v",
			context, parameter.Name, err)
	}
	if !gorapide.CanonicalValueMatchesPredefinedType(value, canonicalPredefinedType(typeName)) {
		return nil, false, typeError(parameter.Default.Position,
			"%s parameter %q default evaluates as %s, want %s",
			context, parameter.Name, evaluatedType, typeName)
	}
	return value, true, nil
}

func compileArchitectureGeneratorArguments(
	position Position,
	parameters []ParameterDecl,
	actuals map[string]any,
	typeElaborator *sourceTypeElaborator,
) (map[string]behaviorBinding, []arch.ArchitectureGeneratorArgument, error) {
	type suppliedArgument struct {
		name  string
		value any
	}
	rawNames := make([]string, 0, len(actuals))
	for name := range actuals {
		rawNames = append(rawNames, name)
	}
	sort.Slice(rawNames, func(i, j int) bool {
		left, right := folded(rawNames[i]), folded(rawNames[j])
		if left != right {
			return left < right
		}
		return rawNames[i] < rawNames[j]
	})
	supplied := make(map[string]suppliedArgument, len(rawNames))
	for _, name := range rawNames {
		key := folded(name)
		if key == "" {
			return nil, nil, typeError(position, "architecture generator argument has an empty name")
		}
		if prior, exists := supplied[key]; exists {
			return nil, nil, typeError(position,
				"architecture generator arguments %q and %q differ only by case", prior.name, name)
		}
		normalized, err := gorapide.CanonicalizeParams(map[string]any{"value": actuals[name]})
		if err != nil {
			return nil, nil, typeError(position,
				"architecture generator argument %q is not a canonical deterministic value: %v", name, err)
		}
		supplied[key] = suppliedArgument{name: name, value: normalized["value"]}
	}

	bindings := make(map[string]behaviorBinding, len(parameters))
	arguments := make([]arch.ArchitectureGeneratorArgument, 0, len(parameters))
	seenParameters := make(map[string]bool, len(parameters))
	for _, parameter := range parameters {
		key := folded(parameter.Name)
		if key == "" || seenParameters[key] {
			return nil, nil, typeError(parameter.Position,
				"duplicate or empty architecture generator parameter %q", parameter.Name)
		}
		seenParameters[key] = true
		typeName, err := typeElaborator.executionPredefinedTypeExpression(
			parameter.Position, parameter.Type, parameter.TypeExpression,
		)
		if err != nil {
			return nil, nil, err
		}
		if !sourceClosedScalarType(typeName) {
			return nil, nil, typeError(parameter.Position,
				"architecture generator parameter %q has type %s; the explicit elaboration-object subset supports predefined scalar types",
				parameter.Name, typeName)
		}
		defaultValue, hasDefault, err := evaluateClosedGeneratorDefault(
			parameter, typeName, "architecture generator",
		)
		if err != nil {
			return nil, nil, err
		}
		actual, exists := supplied[key]
		if !exists {
			if !hasDefault {
				return nil, nil, typeError(parameter.Position,
					"architecture generator parameter %q requires an explicit %s argument", parameter.Name, typeName)
			}
			actual = suppliedArgument{name: parameter.Name, value: defaultValue}
		}
		if !gorapide.CanonicalValueMatchesPredefinedType(actual.value, typeName) {
			return nil, nil, typeError(parameter.Position,
				"architecture generator argument %q does not match %s", actual.name, typeName)
		}
		constant := arch.LiteralValue(actual.value)
		bindings[key] = behaviorBinding{name: parameter.Name, typeName: typeName, constant: &constant}
		arguments = append(arguments, arch.ArchitectureArgument(parameter.Name, typeName, actual.value))
		delete(supplied, key)
	}
	if len(supplied) != 0 {
		extra := make([]string, 0, len(supplied))
		for _, actual := range supplied {
			extra = append(extra, actual.name)
		}
		sort.Slice(extra, func(i, j int) bool {
			left, right := folded(extra[i]), folded(extra[j])
			if left != right {
				return left < right
			}
			return extra[i] < extra[j]
		})
		return nil, nil, typeError(position,
			"architecture generator received undeclared argument %q", extra[0])
	}
	return bindings, arguments, nil
}

func validateModuleGeneratorParameters(
	module ModuleDecl,
	typeElaborator *sourceTypeElaborator,
) error {
	seen := make(map[string]bool, len(module.Parameters))
	for _, parameter := range module.Parameters {
		key := folded(parameter.Name)
		if key == "" || seen[key] {
			return typeError(parameter.Position,
				"duplicate or empty module generator parameter %q", parameter.Name)
		}
		seen[key] = true
		typeName, err := typeElaborator.executionPredefinedTypeExpression(
			parameter.Position, parameter.Type, parameter.TypeExpression,
		)
		if err != nil {
			return err
		}
		if !sourceClosedScalarType(typeName) {
			return typeError(parameter.Position,
				"module generator parameter %q has type %s; the explicit object-formal subset supports predefined scalar types",
				parameter.Name, typeName)
		}
		if _, _, err := evaluateClosedGeneratorDefault(
			parameter, typeName, "module generator",
		); err != nil {
			return err
		}
	}
	return nil
}

func compileModuleGeneratorArguments(
	component ComponentDecl,
	module ModuleDecl,
	architectureBindings map[string]behaviorBinding,
	typeElaborator *sourceTypeElaborator,
) (map[string]behaviorBinding, []arch.ModuleGeneratorArgument, error) {
	formals := component.ModuleArgumentFormals
	if len(formals) == 0 && len(component.ModuleArguments) != 0 {
		formals = make([]string, len(component.ModuleArguments))
	}
	if len(formals) != len(component.ModuleArguments) {
		return nil, nil, typeError(component.Position,
			"component %q has %d module arguments but %d association descriptors",
			component.Name, len(component.ModuleArguments), len(formals))
	}
	allPositional := true
	for _, formal := range formals {
		if formal != "" {
			allPositional = false
			break
		}
	}
	hasDefault := false
	for _, parameter := range module.Parameters {
		if parameter.Default != nil {
			hasDefault = true
			break
		}
	}
	if allPositional && !hasDefault && len(component.ModuleArguments) != len(module.Parameters) {
		return nil, nil, typeError(component.Position,
			"module generator %q expects %d actual parameters but component %q supplies %d",
			module.Name, len(module.Parameters), component.Name, len(component.ModuleArguments))
	}
	parameterIndices := make(map[string]int, len(module.Parameters))
	for index, parameter := range module.Parameters {
		parameterIndices[folded(parameter.Name)] = index
	}
	actuals := make(map[int]ExpressionDecl, len(component.ModuleArguments))
	named := false
	nextPositional := 0
	for index, expression := range component.ModuleArguments {
		formal := formals[index]
		if formal == "" {
			if named {
				return nil, nil, typeError(expression.Position,
					"component %q positional module arguments must precede named associations", component.Name)
			}
			if nextPositional >= len(module.Parameters) {
				return nil, nil, typeError(expression.Position,
					"module generator %q expects at most %d actual parameters but component %q supplies more",
					module.Name, len(module.Parameters), component.Name)
			}
			actuals[nextPositional] = expression
			nextPositional++
			continue
		}
		named = true
		parameterIndex, exists := parameterIndices[folded(formal)]
		if !exists {
			return nil, nil, typeError(expression.Position,
				"module generator %q has no formal parameter named %q", module.Name, formal)
		}
		if _, duplicate := actuals[parameterIndex]; duplicate {
			return nil, nil, typeError(expression.Position,
				"component %q supplies module parameter %q more than once", component.Name, module.Parameters[parameterIndex].Name)
		}
		actuals[parameterIndex] = expression
	}
	bindings := make(map[string]behaviorBinding, len(module.Parameters))
	arguments := make([]arch.ModuleGeneratorArgument, len(module.Parameters))
	for index, parameter := range module.Parameters {
		typeName, err := typeElaborator.executionPredefinedTypeExpression(
			parameter.Position, parameter.Type, parameter.TypeExpression,
		)
		if err != nil {
			return nil, nil, err
		}
		expression, explicitlySupplied := actuals[index]
		var value any
		if explicitlySupplied {
			compiled, err := compileBehaviorExpression(expression, architectureBindings, nil)
			if err != nil {
				return nil, nil, typeError(expression.Position,
					"component %q module argument %d for %q: %v",
					component.Name, index+1, parameter.Name, err)
			}
			if !sourceBehaviorExpressionAssignable(compiled, typeName) {
				return nil, nil, typeError(expression.Position,
					"component %q module argument %d has type %s, want %s for %q",
					component.Name, index+1, compiled.typeName, typeName, parameter.Name)
			}
			var evaluatedType string
			value, evaluatedType, err = arch.EvaluateConstant(compiled.value)
			if err != nil {
				return nil, nil, typeError(expression.Position,
					"component %q module argument %d is not a closed deterministic constant: %v",
					component.Name, index+1, err)
			}
			if !gorapide.CanonicalValueMatchesPredefinedType(value, typeName) {
				return nil, nil, typeError(expression.Position,
					"component %q module argument %d evaluates as %s, want %s",
					component.Name, index+1, evaluatedType, typeName)
			}
		} else {
			var exists bool
			value, exists, err = evaluateClosedGeneratorDefault(parameter, typeName, "module generator")
			if err != nil {
				return nil, nil, err
			}
			if !exists {
				return nil, nil, typeError(parameter.Position,
					"module generator %q parameter %q requires an explicit %s argument for component %q",
					module.Name, parameter.Name, typeName, component.Name)
			}
		}
		constant := arch.LiteralValue(value)
		bindings[folded(parameter.Name)] = behaviorBinding{
			name: parameter.Name, typeName: typeName, constant: &constant,
		}
		arguments[index] = arch.ModuleArgument(parameter.Name, typeName, value)
	}
	return bindings, arguments, nil
}

func compileModuleDeclaration(
	declaration ModuleDecl,
	formalBindings map[string]behaviorBinding,
	generatorArguments []arch.ModuleGeneratorArgument,
	typeElaborator *sourceTypeElaborator,
) (compiledModuleDecl, error) {
	declaration = assignModuleBehaviorDoExceptionIdentities(declaration)
	interfaceKey, interfaceDecl, err := typeElaborator.interfaceDeclaration(
		declaration.Position, declaration.ReturnType,
	)
	if err != nil {
		return compiledModuleDecl{}, typeError(declaration.Position,
			"module %q return type %q: %v", declaration.Name, declaration.ReturnType, err)
	}
	interfaceType, err := typeElaborator.interfaceType(interfaceKey)
	if err != nil {
		return compiledModuleDecl{}, err
	}
	interfaceExecution, err := typeElaborator.executionInterfaceExpansion(interfaceDecl)
	if err != nil {
		return compiledModuleDecl{}, err
	}
	executionInterfaceDecl := expandedExecutionInterfaceDeclaration(interfaceDecl, interfaceExecution)
	interfaceExceptions := interfaceConstituentExceptions(interfaceExecution.exceptions)
	executionInterfaceDecl.SelectedExceptions = cloneExceptionDeclarations(interfaceExceptions)
	executionInterfaceDecl.ExceptionScopes = []ExceptionScopeDecl{{
		Path: []string{declaration.Name}, Exceptions: cloneExceptionDeclarations(declaration.Exceptions),
	}}
	interfaceScopes, err := visibleInterfaceExceptionScopes(typeElaborator)
	if err != nil {
		return compiledModuleDecl{}, err
	}
	executionInterfaceDecl.ExceptionScopes = append(
		executionInterfaceDecl.ExceptionScopes, interfaceScopes...,
	)
	executionInterfaceDecl.Exceptions = mergeVisibleExceptionDeclarations(
		interfaceExceptions, declaration.Exceptions...,
	)
	exceptions, err := compileExceptionDeclarations("module", executionInterfaceDecl.Exceptions)
	if err != nil {
		return compiledModuleDecl{}, err
	}
	selectedExceptions, err := compileExceptionDeclarations("selected interface", interfaceExceptions)
	if err != nil {
		return compiledModuleDecl{}, err
	}
	exceptions = mergeCompiledExceptionCatalogs(exceptions, selectedExceptions)
	for _, scope := range interfaceScopes {
		scopeExceptions, err := compileExceptionDeclarations(
			"interface scope "+strings.Join(scope.Path, "::"), scope.Exceptions,
		)
		if err != nil {
			return compiledModuleDecl{}, err
		}
		exceptions = mergeCompiledExceptionCatalogs(exceptions, scopeExceptions)
	}
	doScopes, err := moduleBehaviorDoExceptionScopes(declaration)
	if err != nil {
		return compiledModuleDecl{}, err
	}
	for _, scope := range doScopes {
		scopeExceptions, err := compileExceptionDeclarations(
			"declaration-bearing do "+strings.Join(scope.Path, "::"), scope.Exceptions,
		)
		if err != nil {
			return compiledModuleDecl{}, err
		}
		exceptions = mergeCompiledExceptionCatalogs(exceptions, scopeExceptions)
	}
	iterationScopes, err := moduleProcessWhenIterationExceptionScopes(declaration)
	if err != nil {
		return compiledModuleDecl{}, err
	}
	for _, scope := range iterationScopes {
		scopeExceptions, err := compileExceptionDeclarations(
			"per-match declaration-bearing when "+strings.Join(scope.Path, "::"), scope.Exceptions,
		)
		if err != nil {
			return compiledModuleDecl{}, err
		}
		exceptions = mergeCompiledExceptionCatalogs(exceptions, scopeExceptions)
	}
	for _, exception := range executionInterfaceDecl.Exceptions {
		if _, exists := findAction(interfaceDecl, exception.Name); exists ||
			len(findFunctions(interfaceDecl, exception.Name)) != 0 {
			return compiledModuleDecl{}, typeError(exception.Position,
				"module exception %q conflicts with a returned-interface action or function", exception.Name)
		}
	}
	typeDenotations, err := typeElaborator.moduleTypeDenotations(declaration, interfaceType)
	if err != nil {
		return compiledModuleDecl{}, err
	}
	if err := validateModuleObjectNameConflicts(declaration); err != nil {
		return compiledModuleDecl{}, err
	}
	objectDenotations, recordObjects, localObjects, err := compileModuleObjectDenotations(
		declaration, typeElaborator, typeDenotations, formalBindings,
	)
	if err != nil {
		return compiledModuleDecl{}, err
	}
	objectTypes := make([]gorapide.RapideObjectTypeDenotation, 0, len(objectDenotations)+len(recordObjects))
	for _, denotation := range objectDenotations {
		actual, typeErr := gorapide.NewRapideObjectTypeDenotation(denotation.Name(), denotation.Type())
		if typeErr != nil {
			return compiledModuleDecl{}, typeError(declaration.Position, "module %q object membership: %v", declaration.Name, typeErr)
		}
		objectTypes = append(objectTypes, actual)
	}
	for _, record := range recordObjects {
		actual, typeErr := gorapide.NewRapideObjectTypeDenotation(record.Name(), record.Type())
		if typeErr != nil {
			return compiledModuleDecl{}, typeError(declaration.Position, "module %q object membership: %v", declaration.Name, typeErr)
		}
		objectTypes = append(objectTypes, actual)
	}
	_, err = gorapide.ValidateRapideInterfaceObjectTypes(interfaceType, typeDenotations, objectTypes...)
	if err != nil {
		return compiledModuleDecl{}, typeError(declaration.Position,
			"module %q object membership: %v", declaration.Name, err)
	}
	objects := copyBehaviorBindings(formalBindings, len(localObjects))
	for key, object := range localObjects {
		objects[key] = object
	}
	if existing := objects[folded("Self")]; existing.name != "" {
		return compiledModuleDecl{}, typeError(declaration.Position,
			"module %q declaration %q conflicts with tool-supplied Self", declaration.Name, existing.name)
	}
	objects[folded("Self")] = behaviorBinding{
		name: "Self", typeName: interfaceDecl.Name, moduleSelf: true, moduleValue: true,
		moduleNew: sourceModuleAllocatorSubset(
			declaration, interfaceDecl, executionInterfaceDecl, objects, typeElaborator,
		),
		moduleNewParameters:        append([]ParameterDecl(nil), declaration.Parameters...),
		moduleNewInitialParameters: append([]ParameterDecl(nil), declaration.InitialParameters...),
		moduleNewArguments:         append([]arch.ModuleGeneratorArgument(nil), generatorArguments...),
	}
	seenClocks := make(map[string]bool, len(declaration.Clocks))
	for _, clock := range declaration.Clocks {
		clockKey := folded(clock.Name)
		if seenClocks[clockKey] {
			return compiledModuleDecl{}, typeError(clock.Position,
				"duplicate basic clock %q in module %q", clock.Name, declaration.Name)
		}
		seenClocks[clockKey] = true
	}
	timingObjects, err := compileModuleTimingObjectBindings(declaration, objects, seenClocks)
	if err != nil {
		return compiledModuleDecl{}, err
	}
	for key, object := range timingObjects {
		objects[key] = object
	}
	states, stateBindings, err := compileStateDeclarations("module state", declaration.States, objects)
	if err != nil {
		return compiledModuleDecl{}, err
	}
	initialParameters, initialBindings, err := compileModuleInitializationParameters(
		declaration, objects, stateBindings, typeElaborator,
	)
	if err != nil {
		return compiledModuleDecl{}, err
	}
	selfKey := folded("Self")
	selfBinding := objects[selfKey]
	selfBinding.moduleNewInitializationParameters = append(
		[]arch.ModuleInitializationParameter(nil), initialParameters...,
	)
	objects[selfKey] = selfBinding
	initialBindings[selfKey] = selfBinding
	normalizedFunctions, err := normalizeModuleFunctionTimings(
		declaration, declaration.Functions, objects,
	)
	if err != nil {
		return compiledModuleDecl{}, err
	}
	functions, err := compileFunctionBodies(
		executionInterfaceDecl, normalizedFunctions, stateBindings, objects, "module function",
	)
	if err != nil {
		return compiledModuleDecl{}, err
	}
	if err := validateSupportedModuleMembership(declaration, interfaceDecl, executionInterfaceDecl); err != nil {
		return compiledModuleDecl{}, err
	}
	if _, err := compileModuleInstanceConstraints(
		"validation", interfaceDecl, &declaration, stateBindings,
	); err != nil {
		return compiledModuleDecl{}, err
	}
	connections, err := compileModuleConnections(declaration, interfaceDecl, objects, typeElaborator)
	if err != nil {
		return compiledModuleDecl{}, err
	}
	initial, err := compileModuleInitial(declaration, executionInterfaceDecl, initialBindings, stateBindings)
	if err != nil {
		return compiledModuleDecl{}, err
	}
	final, err := compileModuleFinal(declaration, executionInterfaceDecl, objects, stateBindings)
	if err != nil {
		return compiledModuleDecl{}, err
	}
	mode := arch.UnspecifiedProcessMode
	switch strings.ToLower(declaration.Mode) {
	case "":
		if len(declaration.Processes) != 0 {
			return compiledModuleDecl{}, typeError(declaration.Position,
				"module %q processes require serial or parallel mode", declaration.Name)
		}
	case "serial":
		mode = arch.SerialProcesses
	case "parallel":
		mode = arch.ParallelProcesses
	default:
		return compiledModuleDecl{}, typeError(declaration.Position,
			"module %q has unsupported process mode %q", declaration.Name, declaration.Mode)
	}
	processes, err := compileModuleProcesses(declaration, executionInterfaceDecl, objects, stateBindings, typeElaborator)
	if err != nil {
		return compiledModuleDecl{}, err
	}
	var moduleHandler *arch.ExceptionHandler
	if declaration.Handler != nil {
		compiled, err := compileProcessExceptionHandler(
			executionInterfaceDecl, *declaration.Handler,
			"module "+declaration.Name, objects, stateBindings, nil, false,
		)
		if err != nil {
			return compiledModuleDecl{}, err
		}
		moduleHandler = &compiled
	}
	return compiledModuleDecl{
		declaration: declaration, generatorArguments: append([]arch.ModuleGeneratorArgument(nil), generatorArguments...),
		typeDenotations: typeDenotations, objectDenotations: objectDenotations, recordObjects: recordObjects,
		objectBindings: objects, states: states, stateBindings: stateBindings,
		initialParameters: initialParameters,
		functions:         functions, exceptions: exceptions, clocks: append([]ClockDecl(nil), declaration.Clocks...),
		connections: connections, initial: initial, mode: mode, processes: processes,
		handler: moduleHandler, final: final,
	}, nil
}

func visibleInterfaceExceptionScopes(typeElaborator *sourceTypeElaborator) ([]ExceptionScopeDecl, error) {
	keys := make([]string, 0, len(typeElaborator.interfaces))
	for key := range typeElaborator.interfaces {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]ExceptionScopeDecl, 0, len(keys))
	for _, key := range keys {
		declaration := typeElaborator.interfaces[key]
		expansion, err := typeElaborator.executionInterfaceExpansion(declaration)
		if err != nil {
			return nil, err
		}
		exceptions := interfaceConstituentExceptions(expansion.exceptions)
		if len(exceptions) == 0 {
			continue
		}
		result = append(result, ExceptionScopeDecl{
			Path: []string{declaration.Name}, Exceptions: cloneExceptionDeclarations(exceptions),
		})
	}
	return result, nil
}

func assignModuleBehaviorDoExceptionIdentities(module ModuleDecl) ModuleDecl {
	result := module
	result.Initial = assignInitializerBehaviorDoExceptionIdentities(
		module.Name, "initial", module.Initial,
	)
	result.Final = assignFinalizerBehaviorDoExceptionIdentities(
		module.Name, "final", module.Final,
	)
	result.Functions = append([]FunctionBodyDecl(nil), module.Functions...)
	result.Processes = append([]ModuleProcessDecl(nil), module.Processes...)
	type rankedProcess struct {
		index int
		key   string
	}
	order := make([]rankedProcess, len(module.Processes))
	for index, process := range module.Processes {
		order[index] = rankedProcess{index: index, key: moduleProcessSemanticKey(process)}
	}
	sort.SliceStable(order, func(i, j int) bool { return order[i].key < order[j].key })
	for rank, candidate := range order {
		process := cloneModuleProcessForExceptionIdentity(module.Processes[candidate.index])
		path := fmt.Sprintf("process:%06d", rank)
		for index := range process.OuterExceptions {
			exception := &process.OuterExceptions[index]
			if process.Label != "" {
				exception.Declaration = doExceptionDeclarationIdentity(
					module.Name, process.Label, exception.Name,
				)
			} else {
				exception.Declaration = lexicalWhenExceptionDeclarationIdentity(
					module.Name, path, "outer", exception.Name,
				)
			}
		}
		for index := range process.IterationExceptions {
			exception := &process.IterationExceptions[index]
			if process.Label != "" {
				exception.Declaration = whenIterationExceptionDeclarationIdentity(
					module.Name, process.Label, exception.Name,
				)
			} else {
				exception.Declaration = lexicalWhenExceptionDeclarationIdentity(
					module.Name, path, "iteration", exception.Name,
				)
			}
		}
		process.Statements = assignBehaviorDoExceptionIdentities(
			module.Name, path+":body", process.Statements,
		)

		type rankedAlternative struct {
			index int
			key   string
		}
		alternatives := make([]rankedAlternative, len(process.Alternatives))
		for index, alternative := range process.Alternatives {
			alternatives[index] = rankedAlternative{
				index: index, key: moduleAwaitAlternativeSemanticKey(alternative),
			}
		}
		sort.SliceStable(alternatives, func(i, j int) bool {
			return alternatives[i].key < alternatives[j].key
		})
		for alternativeRank, alternative := range alternatives {
			process.Alternatives[alternative.index].Statements = assignBehaviorDoExceptionIdentities(
				module.Name,
				fmt.Sprintf("%s:await:%06d", path, alternativeRank),
				process.Alternatives[alternative.index].Statements,
			)
		}
		process.Else = assignBehaviorDoExceptionIdentities(module.Name, path+":else", process.Else)
		process.Handler = assignBehaviorHandlerDoExceptionIdentities(
			module.Name, path+":handler", process.Handler,
		)
		result.Processes[candidate.index] = process
	}
	for index, source := range module.Functions {
		function := cloneFunctionBodyForExceptionIdentity(source)
		path := functionBodyExceptionIdentityPath(source)
		function.Statements = assignFunctionBehaviorDoExceptionIdentities(
			module.Name, path+":body", source.Statements,
		)
		function.Handler = assignFunctionBehaviorHandlerDoExceptionIdentities(
			module.Name, path+":handler", source.Handler,
		)
		result.Functions[index] = function
	}
	result.Handler = assignBehaviorHandlerDoExceptionIdentities(
		module.Name, "module-handler", module.Handler,
	)
	return result
}

func cloneFunctionBodyForExceptionIdentity(source FunctionBodyDecl) FunctionBodyDecl {
	result := source
	result.Statements = append([]BehaviorStatementDecl(nil), source.Statements...)
	result.Handler = cloneBehaviorHandlerForExceptionIdentity(source.Handler)
	return result
}

func functionBodyExceptionIdentityPath(body FunctionBodyDecl) string {
	var builder strings.Builder
	builder.WriteString(folded(body.Name))
	for _, parameter := range body.Parameters {
		builder.WriteByte('|')
		builder.WriteString(folded(parameter.Name))
		builder.WriteByte(':')
		builder.WriteString(folded(parameter.Type))
	}
	builder.WriteString("->")
	builder.WriteString(folded(body.ReturnType))
	digest := sha256.Sum256([]byte(builder.String()))
	return "function:" + hex.EncodeToString(digest[:])
}

func cloneModuleProcessForExceptionIdentity(source ModuleProcessDecl) ModuleProcessDecl {
	result := source
	result.OuterExceptions = cloneExceptionDeclarations(source.OuterExceptions)
	result.IterationExceptions = cloneExceptionDeclarations(source.IterationExceptions)
	result.Statements = append([]BehaviorStatementDecl(nil), source.Statements...)
	result.Alternatives = append([]ModuleAwaitAlternativeDecl(nil), source.Alternatives...)
	for index := range result.Alternatives {
		result.Alternatives[index].Statements = append(
			[]BehaviorStatementDecl(nil), source.Alternatives[index].Statements...,
		)
	}
	result.Else = append([]BehaviorStatementDecl(nil), source.Else...)
	result.Handler = cloneBehaviorHandlerForExceptionIdentity(source.Handler)
	return result
}

func cloneBehaviorHandlerForExceptionIdentity(source *BehaviorHandlerDecl) *BehaviorHandlerDecl {
	if source == nil {
		return nil
	}
	result := *source
	result.Choices = append([]BehaviorHandlerChoiceDecl(nil), source.Choices...)
	for index := range result.Choices {
		result.Choices[index].Statements = append(
			[]BehaviorStatementDecl(nil), source.Choices[index].Statements...,
		)
	}
	result.Else = append([]BehaviorStatementDecl(nil), source.Else...)
	return &result
}

func assignBehaviorDoExceptionIdentities(
	moduleName string,
	path string,
	statements []BehaviorStatementDecl,
) []BehaviorStatementDecl {
	return assignBehaviorDoExceptionIdentitiesWithPolicy(moduleName, path, statements, "")
}

func assignFunctionBehaviorDoExceptionIdentities(
	moduleName string,
	path string,
	statements []BehaviorStatementDecl,
) []BehaviorStatementDecl {
	return assignBehaviorDoExceptionIdentitiesWithPolicy(moduleName, path, statements, "function")
}

func assignInitializerBehaviorDoExceptionIdentities(
	moduleName string,
	path string,
	statements []BehaviorStatementDecl,
) []BehaviorStatementDecl {
	return assignBehaviorDoExceptionIdentitiesWithPolicy(moduleName, path, statements, "initial")
}

func assignFinalizerBehaviorDoExceptionIdentities(
	moduleName string,
	path string,
	statements []BehaviorStatementDecl,
) []BehaviorStatementDecl {
	return assignBehaviorDoExceptionIdentitiesWithPolicy(moduleName, path, statements, "final")
}

func assignArchitectureInitializerBehaviorDoExceptionIdentities(
	architectureName string,
	path string,
	statements []BehaviorStatementDecl,
) []BehaviorStatementDecl {
	return assignBehaviorDoExceptionIdentitiesWithPolicy(architectureName, path, statements, "architecture-initial")
}

func assignBehaviorDoExceptionIdentitiesWithPolicy(
	moduleName string,
	path string,
	statements []BehaviorStatementDecl,
	scopedLabels string,
) []BehaviorStatementDecl {
	if statements == nil {
		return nil
	}
	result := append([]BehaviorStatementDecl(nil), statements...)
	for index := range result {
		statementPath := fmt.Sprintf("%s:statement:%06d", path, index)
		result[index].Exceptions = cloneExceptionDeclarations(statements[index].Exceptions)
		for exceptionIndex := range result[index].Exceptions {
			exception := &result[index].Exceptions[exceptionIndex]
			if result[index].Label != "" {
				switch scopedLabels {
				case "function":
					exception.Declaration = functionDoExceptionDeclarationIdentity(
						moduleName, statementPath, result[index].Label, exception.Name,
					)
				case "initial":
					exception.Declaration = initializerDoExceptionDeclarationIdentity(
						moduleName, statementPath, result[index].Label, exception.Name,
					)
				case "final":
					exception.Declaration = finalizerDoExceptionDeclarationIdentity(
						moduleName, statementPath, result[index].Label, exception.Name,
					)
				case "architecture-initial":
					exception.Declaration = architectureInitializerDoExceptionDeclarationIdentity(
						moduleName, statementPath, result[index].Label, exception.Name,
					)
				default:
					exception.Declaration = doExceptionDeclarationIdentity(
						moduleName, result[index].Label, exception.Name,
					)
				}
			} else {
				exception.Declaration = lexicalDoExceptionDeclarationIdentity(
					moduleName, statementPath, exception.Name,
				)
			}
		}
		result[index].Then = assignBehaviorDoExceptionIdentitiesWithPolicy(
			moduleName, statementPath+":then", statements[index].Then, scopedLabels,
		)
		result[index].Else = assignBehaviorDoExceptionIdentitiesWithPolicy(
			moduleName, statementPath+":else", statements[index].Else, scopedLabels,
		)
		result[index].Body = assignBehaviorDoExceptionIdentitiesWithPolicy(
			moduleName, statementPath+":body", statements[index].Body, scopedLabels,
		)
		result[index].Default = assignBehaviorDoExceptionIdentitiesWithPolicy(
			moduleName, statementPath+":default", statements[index].Default, scopedLabels,
		)
		result[index].Cases = append([]BehaviorCaseAlternativeDecl(nil), statements[index].Cases...)
		for caseIndex := range result[index].Cases {
			result[index].Cases[caseIndex].Body = assignBehaviorDoExceptionIdentitiesWithPolicy(
				moduleName,
				fmt.Sprintf("%s:case:%06d", statementPath, caseIndex),
				statements[index].Cases[caseIndex].Body,
				scopedLabels,
			)
		}
		result[index].Handler = assignBehaviorHandlerDoExceptionIdentitiesWithPolicy(
			moduleName, statementPath+":handler", statements[index].Handler, scopedLabels,
		)
	}
	return result
}

func assignBehaviorHandlerDoExceptionIdentities(
	moduleName string,
	path string,
	source *BehaviorHandlerDecl,
) *BehaviorHandlerDecl {
	return assignBehaviorHandlerDoExceptionIdentitiesWithPolicy(moduleName, path, source, "")
}

func assignFunctionBehaviorHandlerDoExceptionIdentities(
	moduleName string,
	path string,
	source *BehaviorHandlerDecl,
) *BehaviorHandlerDecl {
	return assignBehaviorHandlerDoExceptionIdentitiesWithPolicy(moduleName, path, source, "function")
}

func assignBehaviorHandlerDoExceptionIdentitiesWithPolicy(
	moduleName string,
	path string,
	source *BehaviorHandlerDecl,
	scopedLabels string,
) *BehaviorHandlerDecl {
	result := cloneBehaviorHandlerForExceptionIdentity(source)
	if result == nil {
		return nil
	}
	type rankedChoice struct {
		index int
		key   string
	}
	order := make([]rankedChoice, len(source.Choices))
	for index, choice := range source.Choices {
		order[index] = rankedChoice{index: index, key: behaviorHandlerChoiceSemanticKey(choice)}
	}
	sort.SliceStable(order, func(i, j int) bool { return order[i].key < order[j].key })
	for rank, candidate := range order {
		result.Choices[candidate.index].Statements = assignBehaviorDoExceptionIdentitiesWithPolicy(
			moduleName,
			fmt.Sprintf("%s:choice:%06d", path, rank),
			source.Choices[candidate.index].Statements,
			scopedLabels,
		)
	}
	result.Else = assignBehaviorDoExceptionIdentitiesWithPolicy(
		moduleName, path+":else", source.Else, scopedLabels,
	)
	return result
}

func behaviorHandlerChoiceSemanticKey(choice BehaviorHandlerChoiceDecl) string {
	var builder strings.Builder
	builder.WriteString(behaviorPatternKey(choice.Pattern))
	builder.WriteString("=>{")
	for _, statement := range choice.Statements {
		builder.WriteString(behaviorStatementKey(statement))
	}
	builder.WriteByte('}')
	return builder.String()
}

func sourceModuleAllocatorSubset(
	declaration ModuleDecl,
	iface InterfaceDecl,
	executionIface InterfaceDecl,
	objectBindings map[string]behaviorBinding,
	typeElaborator *sourceTypeElaborator,
) bool {
	return sourceModuleAllocatorObjectsSubset(declaration.Objects, objectBindings) &&
		len(declaration.Constraints) == 0 &&
		sourceModuleAllocatorConnectionsSubset(declaration, iface, objectBindings, typeElaborator) &&
		sourceModuleAllocatorInitialSubset(
			declaration.Initial, declaration, executionIface, objectBindings,
			false, make(map[string]bool), nil, sourceAllocatorHandledException{},
		) && (len(declaration.Processes) == 0 || declaration.Handler == nil) &&
		len(iface.Constraints) == 0 && iface.Behavior == nil
}

type sourceAllocatorExceptionHandlerScope struct {
	declarations []string
	catchAll     bool
}

type sourceAllocatorHandledException struct {
	declaration string
	active      bool
	unknown     bool
}

func sourceAllocatorExceptionChoice(
	declaration InterfaceDecl,
	choice BehaviorHandlerChoiceDecl,
	bindings map[string]behaviorBinding,
) (ExceptionDecl, bool) {
	pattern := choice.Pattern
	if pattern.Kind != BehaviorBasicPattern || pattern.Event.ComponentPlaceholder ||
		pattern.Event.ComponentIndex != nil || pattern.Event.Attribute != "" {
		return ExceptionDecl{}, false
	}
	event := pattern.Event
	if event.Component == "" && keyword(event.Name, "any") {
		return ExceptionDecl{}, false
	}
	name := event.Name
	if event.Component != "" {
		name = event.Component + "." + event.Name
	}
	exception, exists := findExceptionReference(declaration, name, bindings)
	return exception, exists && exception.Declaration != ""
}

type sourceAllocatorHandlerChoiceKind uint8

const (
	sourceAllocatorExceptionChoiceKind sourceAllocatorHandlerChoiceKind = iota + 1
	sourceAllocatorActionChoiceKind
	sourceAllocatorAnyChoiceKind
)

func sourceAllocatorHandlerChoice(
	declaration InterfaceDecl,
	choice BehaviorHandlerChoiceDecl,
	bindings map[string]behaviorBinding,
) (sourceAllocatorHandlerChoiceKind, ExceptionDecl, bool) {
	pattern := choice.Pattern
	if pattern.Kind != BehaviorBasicPattern || pattern.Event.ComponentPlaceholder ||
		pattern.Event.ComponentIndex != nil || pattern.Event.Attribute != "" {
		return 0, ExceptionDecl{}, false
	}
	event := pattern.Event
	if event.Component == "" && keyword(event.Name, "any") {
		return sourceAllocatorAnyChoiceKind, ExceptionDecl{}, len(event.Arguments) == 0
	}
	if exception, exists := sourceAllocatorExceptionChoice(declaration, choice, bindings); exists {
		return sourceAllocatorExceptionChoiceKind, exception, true
	}
	if event.Component != "" {
		return 0, ExceptionDecl{}, false
	}
	action, exists := findAction(declaration, event.Name)
	if !exists || (action.Mode != ActionOut && action.Mode != ActionPrivate) {
		return 0, ExceptionDecl{}, false
	}
	return sourceAllocatorActionChoiceKind, ExceptionDecl{}, true
}

func sourceAllocatorHandlerScope(
	declaration InterfaceDecl,
	handler BehaviorHandlerDecl,
	bindings map[string]behaviorBinding,
) (sourceAllocatorExceptionHandlerScope, bool, bool) {
	scope := sourceAllocatorExceptionHandlerScope{catchAll: handler.Else != nil}
	hasInterrupt := false
	hasAny := false
	for _, choice := range handler.Choices {
		kind, exception, exists := sourceAllocatorHandlerChoice(declaration, choice, bindings)
		if !exists {
			return sourceAllocatorExceptionHandlerScope{}, false, false
		}
		switch kind {
		case sourceAllocatorExceptionChoiceKind:
			scope.declarations = append(scope.declarations, exception.Declaration)
		case sourceAllocatorActionChoiceKind:
			hasInterrupt = true
		case sourceAllocatorAnyChoiceKind:
			hasInterrupt = true
			hasAny = true
			scope.catchAll = true
		default:
			return sourceAllocatorExceptionHandlerScope{}, false, false
		}
	}
	if hasAny && (len(handler.Choices) != 1 || handler.Else != nil) {
		return sourceAllocatorExceptionHandlerScope{}, false, false
	}
	sort.Strings(scope.declarations)
	return scope, hasInterrupt, len(handler.Choices) != 0 || handler.Else != nil
}

func sourceModuleAllocatorInitialSubset(
	statements []BehaviorStatementDecl,
	module ModuleDecl,
	iface InterfaceDecl,
	objectBindings map[string]behaviorBinding,
	inFunction bool,
	visitedFunctions map[string]bool,
	handlers []sourceAllocatorExceptionHandlerScope,
	handled sourceAllocatorHandledException,
) bool {
	for _, statement := range statements {
		if statement.Timing != nil ||
			(len(statement.Exceptions) != 0 && statement.Kind != BehaviorDoStatement) ||
			(statement.Handler != nil && statement.Kind != BehaviorDoStatement) {
			return false
		}
		switch statement.Kind {
		case BehaviorAssignmentStatement, BehaviorNullStatement, BehaviorAssertStatement:
			continue
		case BehaviorRaiseStatement:
			exception, exists := findExceptionReference(iface, statement.Call.Name, objectBindings)
			if !exists || exception.Declaration == "" {
				return false
			}
		case BehaviorReraiseStatement:
			if !handled.active {
				return false
			}
		case BehaviorCallStatement:
			if statement.Call.Timing != nil && statement.Call.Timing.Kind != TimingIn {
				return false
			}
			action, exists := findAction(iface, statement.Call.Name)
			if exists {
				if action.Mode != ActionOut && action.Mode != ActionPrivate {
					return false
				}
				continue
			}
			if !sourceModuleAllocatorInitialFunctionSubset(
				statement.Call.Name, module, iface, objectBindings, visitedFunctions,
				handlers,
			) {
				return false
			}
		case BehaviorIfStatement:
			if !sourceModuleAllocatorInitialSubset(
				statement.Then, module, iface, objectBindings, inFunction,
				visitedFunctions, handlers, handled,
			) || !sourceModuleAllocatorInitialSubset(
				statement.Else, module, iface, objectBindings, inFunction,
				visitedFunctions, handlers, handled,
			) {
				return false
			}
		case BehaviorCaseStatement:
			for _, alternative := range statement.Cases {
				if !sourceModuleAllocatorInitialSubset(
					alternative.Body, module, iface, objectBindings, inFunction,
					visitedFunctions, handlers, handled,
				) {
					return false
				}
			}
			if !sourceModuleAllocatorInitialSubset(
				statement.Default, module, iface, objectBindings, inFunction,
				visitedFunctions, handlers, handled,
			) {
				return false
			}
		case BehaviorLoopStatement:
			if !sourceModuleAllocatorInitialSubset(
				statement.Body, module, iface, objectBindings, inFunction,
				visitedFunctions, handlers, handled,
			) {
				return false
			}
		case BehaviorDoStatement:
			doInterface, err := qualifyBehaviorDoExceptionScope(iface, statement)
			if err != nil {
				return false
			}
			if statement.Handler == nil {
				if !sourceModuleAllocatorInitialSubset(
					statement.Body, module, doInterface, objectBindings, inFunction,
					visitedFunctions, handlers, handled,
				) {
					return false
				}
				continue
			}
			scope, _, valid := sourceAllocatorHandlerScope(
				doInterface, *statement.Handler, objectBindings,
			)
			if !valid {
				return false
			}
			protectedHandlers := append(
				append([]sourceAllocatorExceptionHandlerScope(nil), handlers...), scope,
			)
			if !sourceModuleAllocatorInitialSubset(
				statement.Body, module, doInterface, objectBindings, inFunction,
				visitedFunctions, protectedHandlers, handled,
			) {
				return false
			}
			for _, choice := range statement.Handler.Choices {
				kind, exception, exists := sourceAllocatorHandlerChoice(
					doInterface, choice, objectBindings,
				)
				choiceHandled := sourceAllocatorHandledException{}
				if kind == sourceAllocatorExceptionChoiceKind {
					choiceHandled = sourceAllocatorHandledException{
						declaration: exception.Declaration, active: true,
					}
				}
				if !exists || !sourceModuleAllocatorInitialSubset(
					choice.Statements, module, doInterface, objectBindings, inFunction,
					visitedFunctions, handlers, choiceHandled,
				) {
					return false
				}
			}
			if statement.Handler.Else != nil && !sourceModuleAllocatorInitialSubset(
				statement.Handler.Else, module, doInterface, objectBindings, inFunction,
				visitedFunctions, handlers, sourceAllocatorHandledException{
					active: true, unknown: true,
				},
			) {
				return false
			}
		case BehaviorForStatement:
			general := statement.ForInitial != nil || statement.ForTest != nil || statement.ForNext != nil
			if general {
				if statement.ForInitial == nil || statement.ForTest == nil || statement.ForNext == nil {
					return false
				}
				for _, expression := range []*BehaviorObjectExpressionDecl{
					statement.ForInitial, statement.ForTest, statement.ForNext,
				} {
					if !sourceModuleAllocatorObjectExpressionSubset(
						expression, module, iface, objectBindings, visitedFunctions, handlers,
					) {
						return false
					}
				}
			} else if canonicalPredefinedType(statement.IteratorType) != "Integer" {
				return false
			}
			if !sourceModuleAllocatorInitialSubset(
				statement.Body, module, iface, objectBindings, inFunction,
				visitedFunctions, handlers, handled,
			) {
				return false
			}
		case BehaviorExitStatement, BehaviorNextStatement:
		case BehaviorReturnStatement:
			if !inFunction {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func sourceModuleAllocatorObjectExpressionSubset(
	expression *BehaviorObjectExpressionDecl,
	module ModuleDecl,
	iface InterfaceDecl,
	objectBindings map[string]behaviorBinding,
	visited map[string]bool,
	handlers []sourceAllocatorExceptionHandlerScope,
) bool {
	if expression == nil {
		return false
	}
	switch expression.Kind {
	case BehaviorObjectValue, BehaviorObjectAssignment:
		return true
	case BehaviorObjectFunction:
		return sourceModuleAllocatorInitialFunctionSubset(
			expression.Call.Name, module, iface, objectBindings, visited, handlers,
		)
	default:
		return false
	}
}

func sourceModuleAllocatorInitialFunctionSubset(
	name string,
	module ModuleDecl,
	iface InterfaceDecl,
	objectBindings map[string]behaviorBinding,
	visited map[string]bool,
	handlers []sourceAllocatorExceptionHandlerScope,
) bool {
	callable := false
	for _, function := range findFunctions(iface, name) {
		if function.Mode != FunctionProvides && function.Mode != FunctionRequires {
			continue
		}
		callable = true
		if function.Mode != FunctionProvides {
			continue
		}
		// An exception escaping this ordinary function is re-raised at the
		// call (Executable LRM 8.3.1), where the initializer's active lexical
		// handlers may contain it. Canonical handler catchability remains part
		// of the visit identity so recursive validation retains the exact
		// call-site context without declaration-order dependence.
		visitKey := sourceAllocatorFunctionVisitKey(function) + "|handlers=" +
			sourceAllocatorExceptionHandlerStackKey(handlers)
		if visited[visitKey] {
			continue
		}
		visited[visitKey] = true
		matched := false
		for _, body := range module.Functions {
			if !functionBodyMatchesDeclaration(body, function) {
				continue
			}
			matched = true
			if !sourceModuleAllocatorFunctionLocalsSubset(body.Objects, iface) ||
				!sourceModuleAllocatorFunctionBodySubset(
					body, module, iface, objectBindings, visited, handlers,
				) {
				return false
			}
		}
		if !matched {
			return false
		}
	}
	return callable
}

func sourceModuleAllocatorFunctionLocalsSubset(
	objects []ModuleObjectDecl,
	iface InterfaceDecl,
) bool {
	for _, object := range objects {
		if object.TypeExpression.Kind != TypeExpressionName ||
			!keyword(object.Type, iface.Name) ||
			object.Initial.Kind != ExpressionCall || object.Initial.Left != nil ||
			!keyword(object.Initial.Name, "New") {
			return false
		}
	}
	return true
}

func sourceModuleAllocatorFunctionBodySubset(
	body FunctionBodyDecl,
	module ModuleDecl,
	iface InterfaceDecl,
	objectBindings map[string]behaviorBinding,
	visited map[string]bool,
	callHandlers []sourceAllocatorExceptionHandlerScope,
) bool {
	if body.Handler == nil {
		return sourceModuleAllocatorInitialSubset(
			body.Statements, module, iface, objectBindings, true, visited,
			callHandlers, sourceAllocatorHandledException{},
		)
	}
	scope, _, valid := sourceAllocatorHandlerScope(
		iface, *body.Handler, objectBindings,
	)
	if !valid {
		return false
	}
	if !sourceModuleAllocatorInitialSubset(
		body.Statements, module, iface, objectBindings, true, visited,
		append(append([]sourceAllocatorExceptionHandlerScope(nil), callHandlers...), scope),
		sourceAllocatorHandledException{},
	) {
		return false
	}
	for _, choice := range body.Handler.Choices {
		kind, exception, exists := sourceAllocatorHandlerChoice(iface, choice, objectBindings)
		choiceHandled := sourceAllocatorHandledException{}
		if kind == sourceAllocatorExceptionChoiceKind {
			choiceHandled = sourceAllocatorHandledException{
				declaration: exception.Declaration, active: true,
			}
		}
		if !exists || !sourceModuleAllocatorInitialSubset(
			choice.Statements, module, iface, objectBindings, true, visited, callHandlers,
			choiceHandled,
		) {
			return false
		}
	}
	return body.Handler.Else == nil || sourceModuleAllocatorInitialSubset(
		body.Handler.Else, module, iface, objectBindings, true, visited, callHandlers,
		sourceAllocatorHandledException{active: true, unknown: true},
	)
}

func sourceAllocatorExceptionHandlerStackKey(handlers []sourceAllocatorExceptionHandlerScope) string {
	declarations := make(map[string]bool)
	catchAll := false
	for _, handler := range handlers {
		catchAll = catchAll || handler.catchAll
		for _, declaration := range handler.declarations {
			declarations[declaration] = true
		}
	}
	ordered := make([]string, 0, len(declarations))
	for declaration := range declarations {
		ordered = append(ordered, declaration)
	}
	sort.Strings(ordered)
	var builder strings.Builder
	if catchAll {
		builder.WriteByte('*')
	}
	for _, declaration := range ordered {
		builder.WriteByte('|')
		builder.WriteString(declaration)
	}
	return builder.String()
}

func sourceAllocatorFunctionVisitKey(function FunctionDecl) string {
	var builder strings.Builder
	builder.WriteString(folded(function.Name))
	for _, parameter := range function.Parameters {
		builder.WriteByte('|')
		builder.WriteString(folded(parameter.Type))
	}
	builder.WriteString("->")
	builder.WriteString(folded(function.ReturnType))
	return builder.String()
}

func sourceModuleAllocatorObjectsSubset(
	declarations []ModuleObjectDecl,
	bindings map[string]behaviorBinding,
) bool {
	for _, declaration := range declarations {
		binding, exists := bindings[folded(declaration.Name)]
		if !exists || binding.name == "" || binding.constant == nil ||
			!sourceClosedScalarType(binding.typeName) || binding.moduleValue ||
			binding.structural || len(binding.recordFields) != 0 {
			return false
		}
	}
	return true
}

func sourceModuleAllocatorConnectionsSubset(
	declaration ModuleDecl,
	iface InterfaceDecl,
	objectBindings map[string]behaviorBinding,
	typeElaborator *sourceTypeElaborator,
) bool {
	connections, err := elaborateClosedConnectionGenerators(
		declaration.Connections,
		declaration.ConnectionGenerators,
		"passive module connection generator",
		objectBindings,
		typeElaborator,
	)
	if err != nil {
		return false
	}
	for _, connection := range connections {
		if connection.SourcePattern == nil ||
			(connection.Connector != ConnectBasic && connection.Connector != ConnectPipe && connection.Connector != ConnectAgent) ||
			connection.Target.Component != "" {
			return false
		}
		source := connectionSourcePattern(connection)
		sourceActionRoute := sourceModuleAllocatorActionPatternSubset(
			source, iface, connection.Placeholders, typeElaborator,
		)
		targetAction, targetIsAction := findAction(iface, connection.Target.Action)
		sourceFunctions := findFunctions(iface, connection.Source.Action)
		targetFunctions := findFunctions(iface, connection.Target.Action)
		if connection.Constituent == ConnectionActionConstituent {
			sourceFunctions, targetFunctions = nil, nil
		} else if connection.Constituent == ConnectionFunctionConstituent {
			sourceActionRoute, targetIsAction = false, false
		}
		actionRoute := sourceActionRoute && targetIsAction && targetAction.Name != ""
		functionRoute := len(sourceFunctions) != 0 && len(targetFunctions) != 0
		if !actionRoute && !functionRoute {
			return false
		}
		if functionRoute && !actionRoute && (source.Kind != BehaviorBasicPattern || source.Event.ComponentPlaceholder ||
			source.Event.Component != "" || source.Event.Attribute != "") {
			return false
		}
		if functionRoute && !actionRoute &&
			connection.Connector != ConnectBasic {
			return false
		}
		if functionRoute && !actionRoute &&
			(len(connection.Placeholders) != 0 || connection.Guard != nil ||
				len(source.Event.Arguments) != 0 || len(actionRefArgumentExpressions(connection.Target)) != 0) {
			return false
		}
	}
	return true
}

func sourceModuleAllocatorActionPatternSubset(
	source BehaviorPatternDecl,
	iface InterfaceDecl,
	placeholders []ParameterDecl,
	typeElaborator *sourceTypeElaborator,
) bool {
	switch source.Kind {
	case BehaviorBasicPattern:
		if source.Event.ComponentPlaceholder {
			return sourceModuleAllocatorQualifiedActionPatternSubset(source, placeholders, typeElaborator)
		}
		if source.Event.Name == "" || source.Event.Component != "" || source.Event.Attribute != "" {
			return false
		}
		action, exists := findAction(iface, source.Event.Name)
		return exists && action.Name != "" && len(findFunctions(iface, source.Event.Name)) == 0
	case BehaviorBinaryPattern:
		return source.Left != nil && source.Right != nil &&
			sourceModuleAllocatorActionPatternSubset(*source.Left, iface, placeholders, typeElaborator) &&
			sourceModuleAllocatorActionPatternSubset(*source.Right, iface, placeholders, typeElaborator)
	case BehaviorIterationPattern:
		return source.Inner != nil &&
			sourceModuleAllocatorActionPatternSubset(*source.Inner, iface, placeholders, typeElaborator)
	default:
		return false
	}
}

func sourceModuleAllocatorQualifiedActionPatternSubset(
	source BehaviorPatternDecl,
	placeholders []ParameterDecl,
	typeElaborator *sourceTypeElaborator,
) bool {
	if source.Kind != BehaviorBasicPattern || source.Event.Name == "" ||
		!source.Event.ComponentPlaceholder || source.Event.Component == "" ||
		source.Event.Attribute != "" {
		return false
	}
	placeholder, exists := findParameter(placeholders, source.Event.Component)
	if !exists || placeholder.Qualification == PlaceholderUniversal || typeElaborator == nil {
		return false
	}
	_, sourceInterface, err := typeElaborator.interfaceDeclaration(source.Event.Position, placeholder.Type)
	if err != nil {
		return false
	}
	action, exists := findAction(sourceInterface, source.Event.Name)
	return exists && action.Name != "" && action.Mode == ActionOut &&
		len(findFunctions(sourceInterface, source.Event.Name)) == 0
}

func moduleBehaviorDoExceptionScopes(module ModuleDecl) ([]ExceptionScopeDecl, error) {
	if err := validateModuleBehaviorDoLabels(module); err != nil {
		return nil, err
	}
	result := make([]ExceptionScopeDecl, 0)
	var visitStatements func([]BehaviorStatementDecl, []string) error
	var visitHandler func(*BehaviorHandlerDecl, []string) error
	visitHandler = func(handler *BehaviorHandlerDecl, path []string) error {
		if handler == nil {
			return nil
		}
		for _, choice := range handler.Choices {
			if err := visitStatements(choice.Statements, path); err != nil {
				return err
			}
		}
		return visitStatements(handler.Else, path)
	}
	visitStatements = func(statements []BehaviorStatementDecl, path []string) error {
		for _, statement := range statements {
			statementPath := path
			if statement.Label != "" {
				statementPath = append(append([]string(nil), path...), statement.Label)
			}
			if len(statement.Exceptions) != 0 {
				if statement.Kind != BehaviorDoStatement {
					return typeError(statement.Position,
						"exception declarations require a declaration-bearing do")
				}
				exceptions := cloneExceptionDeclarations(statement.Exceptions)
				for index := range exceptions {
					if exceptions[index].Declaration == "" {
						return typeError(exceptions[index].Position,
							"declaration-bearing do exception %q has no canonical lexical identity",
							exceptions[index].Name)
					}
				}
				result = append(result, ExceptionScopeDecl{
					Path: append([]string(nil), statementPath...), Exceptions: exceptions,
				})
			}
			for _, group := range [][]BehaviorStatementDecl{
				statement.Then, statement.Else, statement.Body, statement.Default,
			} {
				if err := visitStatements(group, statementPath); err != nil {
					return err
				}
			}
			for _, alternative := range statement.Cases {
				if err := visitStatements(alternative.Body, statementPath); err != nil {
					return err
				}
			}
			if err := visitHandler(statement.Handler, statementPath); err != nil {
				return err
			}
		}
		return nil
	}
	root := []string{module.Name}
	if err := visitStatements(module.Initial, root); err != nil {
		return nil, err
	}
	if err := visitStatements(module.Final, root); err != nil {
		return nil, err
	}
	for _, function := range module.Functions {
		if err := visitStatements(function.Statements, root); err != nil {
			return nil, err
		}
		if err := visitHandler(function.Handler, root); err != nil {
			return nil, err
		}
	}
	for _, process := range module.Processes {
		path := root
		if process.Label != "" {
			path = append(append([]string(nil), root...), process.Label)
		}
		if len(process.OuterExceptions) != 0 {
			exceptions := cloneExceptionDeclarations(process.OuterExceptions)
			for _, exception := range exceptions {
				if exception.Declaration == "" {
					return nil, typeError(exception.Position,
						"outer declaration-bearing when exception %q has no canonical lexical identity",
						exception.Name)
				}
			}
			result = append(result, ExceptionScopeDecl{
				Path: append([]string(nil), path...), Exceptions: exceptions,
			})
		}
		if err := visitStatements(process.Statements, path); err != nil {
			return nil, err
		}
		for _, alternative := range process.Alternatives {
			if err := visitStatements(alternative.Statements, path); err != nil {
				return nil, err
			}
		}
		if err := visitStatements(process.Else, path); err != nil {
			return nil, err
		}
		if err := visitHandler(process.Handler, path); err != nil {
			return nil, err
		}
	}
	if err := visitHandler(module.Handler, root); err != nil {
		return nil, err
	}
	return result, nil
}

func architectureInitialDoExceptionScopes(architecture ArchitectureDecl) ([]ExceptionScopeDecl, error) {
	result := make([]ExceptionScopeDecl, 0)
	var visitStatements func([]BehaviorStatementDecl, []string) error
	var visitHandler func(*BehaviorHandlerDecl, []string) error
	visitHandler = func(handler *BehaviorHandlerDecl, path []string) error {
		if handler == nil {
			return nil
		}
		for _, choice := range handler.Choices {
			if err := visitStatements(choice.Statements, path); err != nil {
				return err
			}
		}
		return visitStatements(handler.Else, path)
	}
	visitStatements = func(statements []BehaviorStatementDecl, path []string) error {
		for _, statement := range statements {
			statementPath := path
			if statement.Label != "" {
				statementPath = append(append([]string(nil), path...), statement.Label)
			}
			if len(statement.Exceptions) != 0 {
				if statement.Kind != BehaviorDoStatement {
					return typeError(statement.Position,
						"exception declarations require a declaration-bearing do")
				}
				exceptions := cloneExceptionDeclarations(statement.Exceptions)
				for index := range exceptions {
					if exceptions[index].Declaration == "" {
						return typeError(exceptions[index].Position,
							"architecture declaration-bearing do exception %q has no canonical lexical identity",
							exceptions[index].Name)
					}
				}
				result = append(result, ExceptionScopeDecl{
					Path: append([]string(nil), statementPath...), Exceptions: exceptions,
				})
			}
			for _, group := range [][]BehaviorStatementDecl{
				statement.Then, statement.Else, statement.Body, statement.Default,
			} {
				if err := visitStatements(group, statementPath); err != nil {
					return err
				}
			}
			for _, alternative := range statement.Cases {
				if err := visitStatements(alternative.Body, statementPath); err != nil {
					return err
				}
			}
			if err := visitHandler(statement.Handler, statementPath); err != nil {
				return err
			}
		}
		return nil
	}
	if err := visitStatements(architecture.Initial, []string{architecture.Name}); err != nil {
		return nil, err
	}
	return result, nil
}

func moduleProcessWhenIterationExceptionScopes(module ModuleDecl) ([]ExceptionScopeDecl, error) {
	result := make([]ExceptionScopeDecl, 0)
	for _, process := range module.Processes {
		if len(process.IterationExceptions) == 0 {
			continue
		}
		if process.Entry || process.Await {
			return nil, typeError(process.Position,
				"per-match exception declarations require a when process")
		}
		exceptions := cloneExceptionDeclarations(process.IterationExceptions)
		for _, exception := range exceptions {
			if exception.Declaration == "" {
				return nil, typeError(exception.Position,
					"per-match declaration-bearing when exception %q has no canonical lexical identity",
					exception.Name)
			}
		}
		result = append(result, ExceptionScopeDecl{
			Path: []string{module.Name, process.Label}, Exceptions: exceptions,
		})
	}
	return result, nil
}

func declarationBearingDoPosition(statements []BehaviorStatementDecl) (Position, bool) {
	for _, statement := range statements {
		if len(statement.Exceptions) != 0 {
			return statement.Position, true
		}
		for _, group := range [][]BehaviorStatementDecl{
			statement.Then, statement.Else, statement.Body, statement.Default,
		} {
			if position, exists := declarationBearingDoPosition(group); exists {
				return position, true
			}
		}
		for _, alternative := range statement.Cases {
			if position, exists := declarationBearingDoPosition(alternative.Body); exists {
				return position, true
			}
		}
		if position, exists := declarationBearingDoHandlerPosition(statement.Handler); exists {
			return position, true
		}
	}
	return Position{}, false
}

func declarationBearingDoHandlerPosition(handler *BehaviorHandlerDecl) (Position, bool) {
	if handler == nil {
		return Position{}, false
	}
	for _, choice := range handler.Choices {
		if position, exists := declarationBearingDoPosition(choice.Statements); exists {
			return position, true
		}
	}
	return declarationBearingDoPosition(handler.Else)
}

func unidentifiedDeclarationBearingDoPosition(statements []BehaviorStatementDecl) (Position, bool) {
	for _, statement := range statements {
		for _, exception := range statement.Exceptions {
			if exception.Declaration == "" {
				return statement.Position, true
			}
		}
		for _, group := range [][]BehaviorStatementDecl{
			statement.Then, statement.Else, statement.Body, statement.Default,
		} {
			if position, exists := unidentifiedDeclarationBearingDoPosition(group); exists {
				return position, true
			}
		}
		for _, alternative := range statement.Cases {
			if position, exists := unidentifiedDeclarationBearingDoPosition(alternative.Body); exists {
				return position, true
			}
		}
		if position, exists := unidentifiedDeclarationBearingDoHandlerPosition(statement.Handler); exists {
			return position, true
		}
	}
	return Position{}, false
}

func unidentifiedDeclarationBearingDoHandlerPosition(handler *BehaviorHandlerDecl) (Position, bool) {
	if handler == nil {
		return Position{}, false
	}
	for _, choice := range handler.Choices {
		if position, exists := unidentifiedDeclarationBearingDoPosition(choice.Statements); exists {
			return position, true
		}
	}
	return unidentifiedDeclarationBearingDoPosition(handler.Else)
}

func qualifyBehaviorDoExceptionScope(
	declaration InterfaceDecl,
	statement BehaviorStatementDecl,
) (InterfaceDecl, error) {
	if statement.Kind != BehaviorDoStatement {
		return InterfaceDecl{}, typeError(statement.Position,
			"exception declarations are only valid on a declaration-bearing do")
	}
	result := declaration
	result.ExceptionScopes = cloneExceptionScopeDeclarations(declaration.ExceptionScopes)
	path := activeBehaviorExceptionScopePath(result.ExceptionScopes)
	if statement.Label != "" {
		path = append(path, statement.Label)
		local := cloneExceptionDeclarations(statement.Exceptions)
		if len(path) == 1 {
			return InterfaceDecl{}, typeError(statement.Position,
				"named do scope %q has no enclosing module scope", statement.Label)
		}
		for index := range local {
			if local[index].Declaration == "" {
				local[index].Declaration = doExceptionDeclarationIdentity(path[0], statement.Label, local[index].Name)
			}
		}
		result.ExceptionScopes = append(result.ExceptionScopes, ExceptionScopeDecl{
			Path: append([]string(nil), path...), Exceptions: cloneExceptionDeclarations(local),
		})
		if len(local) != 0 {
			result.Exceptions = mergeVisibleExceptionDeclarations(result.Exceptions, local...)
		}
	} else if len(statement.Exceptions) != 0 {
		local := cloneExceptionDeclarations(statement.Exceptions)
		for _, exception := range local {
			if exception.Declaration == "" {
				return InterfaceDecl{}, typeError(exception.Position,
					"declaration-bearing do exception %q has no canonical lexical identity",
					exception.Name)
			}
		}
		result.Exceptions = mergeVisibleExceptionDeclarations(result.Exceptions, local...)
	}
	return result, nil
}

func activeBehaviorExceptionScopePath(scopes []ExceptionScopeDecl) []string {
	if len(scopes) == 0 || len(scopes[0].Path) == 0 {
		return nil
	}
	root := scopes[0].Path
	active := append([]string(nil), root...)
	for _, scope := range scopes[1:] {
		if len(scope.Path) <= len(active) || len(scope.Path) < len(root) {
			continue
		}
		belongs := true
		for index := range root {
			if !keyword(scope.Path[index], root[index]) {
				belongs = false
				break
			}
		}
		if belongs {
			active = append([]string(nil), scope.Path...)
		}
	}
	return active
}

func qualifyBehaviorWhenIterationExceptionScope(
	declaration InterfaceDecl,
	process ModuleProcessDecl,
) (InterfaceDecl, error) {
	if len(process.IterationExceptions) == 0 {
		return declaration, nil
	}
	if process.Entry || process.Await {
		return InterfaceDecl{}, typeError(process.Position,
			"per-match exception declarations require a when process")
	}
	result := declaration
	result.ExceptionScopes = cloneExceptionScopeDeclarations(declaration.ExceptionScopes)
	local := cloneExceptionDeclarations(process.IterationExceptions)
	for _, exception := range local {
		if exception.Declaration == "" {
			return InterfaceDecl{}, typeError(exception.Position,
				"per-match declaration-bearing when exception %q has no canonical lexical identity",
				exception.Name)
		}
	}
	result.Exceptions = mergeVisibleExceptionDeclarations(result.Exceptions, local...)
	return result, nil
}

func cloneExceptionScopeDeclarations(source []ExceptionScopeDecl) []ExceptionScopeDecl {
	result := make([]ExceptionScopeDecl, len(source))
	for index, scope := range source {
		result[index] = ExceptionScopeDecl{
			Path: append([]string(nil), scope.Path...), Exceptions: cloneExceptionDeclarations(scope.Exceptions),
		}
	}
	return result
}

func compileExceptionDeclarations(context string, declarations []ExceptionDecl) ([]arch.ExceptionDeclaration, error) {
	result := make([]arch.ExceptionDeclaration, 0, len(declarations))
	seenNames := make(map[string]bool, len(declarations))
	seenDeclarations := make(map[string]bool, len(declarations))
	for _, declaration := range declarations {
		key := folded(declaration.Name)
		if declaration.Name == "" || declaration.Declaration == "" || seenNames[key] ||
			seenDeclarations[declaration.Declaration] {
			return nil, typeError(declaration.Position,
				"empty or duplicate %s exception %q declaration %q",
				context, declaration.Name, declaration.Declaration)
		}
		seenNames[key] = true
		seenDeclarations[declaration.Declaration] = true
		parameters := make([]arch.ParamDecl, 0, len(declaration.Parameters))
		seenParameters := make(map[string]bool, len(declaration.Parameters))
		for _, parameter := range declaration.Parameters {
			parameterKey := folded(parameter.Name)
			typeName, predefined := predefinedTypes[folded(parameter.Type)]
			if parameter.Name == "" || seenParameters[parameterKey] || !predefined {
				return nil, typeError(parameter.Position,
					"%s exception %q parameter %q must have a unique predefined-scalar type, got %q",
					context, declaration.Name, parameter.Name, parameter.Type)
			}
			seenParameters[parameterKey] = true
			parameters = append(parameters, arch.P(parameter.Name, typeName))
		}
		result = append(result, arch.DeclaredException(
			declaration.Declaration, declaration.Name, parameters...,
		))
	}
	return result, nil
}

func expandedExecutionInterfaceDeclaration(
	declaration InterfaceDecl,
	expansion sourceExecutionInterfaceExpansion,
) InterfaceDecl {
	result := declaration
	result.Actions = cloneActionDeclarations(expansion.actions)
	result.Functions = cloneFunctionDeclarations(expansion.functions)
	result.Exceptions = cloneExceptionDeclarations(expansion.exceptions)
	result.Constraints = append([]ConstraintDecl(nil), expansion.constraints...)
	return result
}

func mergeCompiledExceptionCatalogs(
	visible, selected []arch.ExceptionDeclaration,
) []arch.ExceptionDeclaration {
	result := append([]arch.ExceptionDeclaration(nil), visible...)
	seen := make(map[string]bool, len(visible)+len(selected))
	for _, declaration := range visible {
		seen[declaration.Declaration] = true
	}
	for _, declaration := range selected {
		if !seen[declaration.Declaration] {
			seen[declaration.Declaration] = true
			result = append(result, declaration)
		}
	}
	return result
}

func visibleOutermostExceptions(declarations []ExceptionDecl, position Position) []ExceptionDecl {
	result := make([]ExceptionDecl, 0, len(declarations))
	for _, declaration := range declarations {
		if declaration.Position.Offset >= position.Offset {
			continue
		}
		copy := declaration
		copy.Parameters = append([]ParameterDecl(nil), declaration.Parameters...)
		result = append(result, copy)
	}
	return result
}

// mergeVisibleExceptionDeclarations applies the ordinary lexical hiding rule:
// declarations in the later/local region hide same-spelling declarations from
// the earlier/extended region. Exact identities are otherwise preserved.
func mergeVisibleExceptionDeclarations(earlier []ExceptionDecl, later ...ExceptionDecl) []ExceptionDecl {
	hidden := make(map[string]bool, len(later))
	for _, declaration := range later {
		hidden[folded(declaration.Name)] = true
	}
	result := make([]ExceptionDecl, 0, len(earlier)+len(later))
	for _, declaration := range earlier {
		if !hidden[folded(declaration.Name)] {
			result = append(result, declaration)
		}
	}
	result = append(result, later...)
	return result
}

func interfaceConstituentExceptions(declarations []ExceptionDecl) []ExceptionDecl {
	result := make([]ExceptionDecl, 0, len(declarations))
	for _, declaration := range declarations {
		if declaration.Constituent {
			result = append(result, declaration)
		}
	}
	return result
}

func copyBehaviorBindings(source map[string]behaviorBinding, extra int) map[string]behaviorBinding {
	result := make(map[string]behaviorBinding, len(source)+extra)
	for key, binding := range source {
		binding.moduleNewParameters = append([]ParameterDecl(nil), binding.moduleNewParameters...)
		binding.moduleNewInitialParameters = append([]ParameterDecl(nil), binding.moduleNewInitialParameters...)
		binding.moduleNewInitializationParameters = append(
			[]arch.ModuleInitializationParameter(nil), binding.moduleNewInitializationParameters...,
		)
		binding.moduleNewArguments = append([]arch.ModuleGeneratorArgument(nil), binding.moduleNewArguments...)
		result[key] = binding
	}
	return result
}

type compiledBehaviorExpression struct {
	value    arch.RuleValue
	typeName string
}

func compileInterfaceBehaviorStates(declaration InterfaceDecl) ([]arch.StateDeclaration, map[string]behaviorBinding, error) {
	if declaration.Behavior == nil {
		return nil, nil, nil
	}
	return compileStateDeclarations("behavior state", declaration.Behavior.States, nil)
}

func compileModuleObjectDenotations(
	module ModuleDecl,
	typeElaborator *sourceTypeElaborator,
	typeDenotations []gorapide.RapideTypeDenotation,
	formalBindings map[string]behaviorBinding,
) ([]gorapide.RapideObjectDenotation, []gorapide.RapideRecordObjectDeclaration, map[string]behaviorBinding, error) {
	result := make([]gorapide.RapideObjectDenotation, 0, len(module.Objects))
	records := make([]gorapide.RapideRecordObjectDeclaration, 0)
	bindings := make(map[string]behaviorBinding, len(module.Objects))
	localTypes := make(map[string]gorapide.RapideType, len(typeDenotations))
	for _, denotation := range typeDenotations {
		localTypes[folded(denotation.Name())] = denotation.Type()
	}
	for _, object := range module.Objects {
		key := folded(object.Name)
		if bindings[key].name != "" || formalBindings[key].name != "" {
			return nil, nil, nil, typeError(object.Position, "duplicate module object %q", object.Name)
		}
		if recordKey, recordTypeDeclaration, recordErr := typeElaborator.interfaceDeclaration(object.Position, object.Type); recordErr == nil && recordTypeDeclaration.Record {
			if object.Initial.Kind != ExpressionRecord {
				return nil, nil, nil, typeError(object.Initial.Position,
					"module Record object %q requires a context-typed Record literal initializer", object.Name)
			}
			structuralType, err := typeElaborator.interfaceType(recordKey)
			if err != nil {
				return nil, nil, nil, err
			}
			declaration, binding, err := compileModuleRecordObject(
				object, recordTypeDeclaration, structuralType, formalBindings, typeElaborator,
			)
			if err != nil {
				return nil, nil, nil, err
			}
			records = append(records, declaration)
			bindings[key] = binding
			continue
		}
		if object.Initial.Kind == ExpressionRecord {
			return nil, nil, nil, typeError(object.Initial.Position,
				"module object %q Record literal requires a direct named Record type context", object.Name)
		}
		var structuralType gorapide.RapideType
		var executionType string
		if local, exists := localTypes[folded(object.Type)]; exists {
			structuralType = local
			var err error
			executionType, err = moduleExecutionPredefinedType(object.Position, object.Type, module, typeElaborator)
			if err != nil {
				return nil, nil, nil, err
			}
		} else {
			var err error
			executionType, err = typeElaborator.executionPredefinedTypeExpression(
				object.Position, object.Type, object.TypeExpression,
			)
			if err != nil {
				return nil, nil, nil, err
			}
			structuralType, err = gorapide.RapidePredefinedType(executionType)
			if err != nil {
				return nil, nil, nil, typeError(object.Position, "module object %q type %q: %v", object.Name, object.Type, err)
			}
		}
		if !sourceClosedScalarType(executionType) {
			return nil, nil, nil, typeError(object.Position,
				"module object %q type %s is outside the immutable predefined-scalar object subset", object.Name, executionType)
		}
		compiled, err := compileBehaviorExpression(object.Initial, formalBindings, nil)
		if err != nil {
			return nil, nil, nil, err
		}
		if !sourceBehaviorExpressionAssignable(compiled, executionType) {
			return nil, nil, nil, typeError(object.Initial.Position,
				"module object %q initializer has type %s, want %s", object.Name, compiled.typeName, executionType)
		}
		value, evaluatedType, err := arch.EvaluateConstant(compiled.value)
		if err != nil {
			return nil, nil, nil, typeError(object.Initial.Position,
				"module object %q initializer is not a closed deterministic constant: %v", object.Name, err)
		}
		if !gorapide.CanonicalValueMatchesPredefinedType(value, executionType) {
			return nil, nil, nil, typeError(object.Initial.Position,
				"module object %q initializer evaluates as %s, want %s", object.Name, evaluatedType, executionType)
		}
		denotation, err := gorapide.NewRapideObjectDenotation(object.Name, structuralType, value)
		if err != nil {
			return nil, nil, nil, typeError(object.Position, "module object %q: %v", object.Name, err)
		}
		constant := arch.LiteralValue(value)
		bindings[key] = behaviorBinding{name: object.Name, typeName: executionType, constant: &constant}
		result = append(result, denotation)
	}
	return result, records, bindings, nil
}

func compileModuleRecordObject(
	object ModuleObjectDecl,
	recordType InterfaceDecl,
	structuralType gorapide.RapideType,
	formalBindings map[string]behaviorBinding,
	typeElaborator *sourceTypeElaborator,
) (gorapide.RapideRecordObjectDeclaration, behaviorBinding, error) {
	literals := make(map[string]RecordFieldExpressionDecl, len(object.Initial.RecordFields))
	for _, field := range object.Initial.RecordFields {
		key := folded(field.Name)
		if _, duplicate := literals[key]; duplicate {
			return gorapide.RapideRecordObjectDeclaration{}, behaviorBinding{},
				typeError(field.Position, "module Record object %q has duplicate literal field %q", object.Name, field.Name)
		}
		literals[key] = field
	}
	expected := make(map[string]bool, len(recordType.Objects))
	fields := make([]gorapide.RapideRecordField, 0, len(recordType.Objects))
	fieldBindings := make(map[string]behaviorBinding, len(recordType.Objects))
	fieldDeclarations := append([]InterfaceObjectDecl(nil), recordType.Objects...)
	sort.Slice(fieldDeclarations, func(left, right int) bool {
		return folded(fieldDeclarations[left].Name) < folded(fieldDeclarations[right].Name)
	})
	for _, declaration := range fieldDeclarations {
		key := folded(declaration.Name)
		expected[key] = true
		literal, exists := literals[key]
		if !exists {
			return gorapide.RapideRecordObjectDeclaration{}, behaviorBinding{},
				typeError(object.Initial.Position, "module Record object %q does not initialize field %q", object.Name, declaration.Name)
		}
		typeName, err := typeElaborator.executionPredefinedTypeExpression(
			declaration.Position, declaration.Type, declaration.TypeExpression,
		)
		if err != nil || !sourceClosedScalarType(typeName) {
			return gorapide.RapideRecordObjectDeclaration{}, behaviorBinding{},
				typeError(declaration.Position,
					"module Record object %q field %q type %s is outside the closed predefined-scalar field subset",
					object.Name, declaration.Name, declaration.Type)
		}
		compiled, err := compileBehaviorExpression(literal.Value, formalBindings, nil)
		if err != nil {
			return gorapide.RapideRecordObjectDeclaration{}, behaviorBinding{}, err
		}
		if !sourceBehaviorExpressionAssignable(compiled, typeName) {
			return gorapide.RapideRecordObjectDeclaration{}, behaviorBinding{},
				typeError(literal.Value.Position,
					"module Record object %q field %q initializer has type %s, want %s",
					object.Name, declaration.Name, compiled.typeName, typeName)
		}
		value, evaluatedType, err := arch.EvaluateConstant(compiled.value)
		if err != nil {
			return gorapide.RapideRecordObjectDeclaration{}, behaviorBinding{},
				typeError(literal.Value.Position,
					"module Record object %q field %q is not a closed deterministic constant: %v",
					object.Name, declaration.Name, err)
		}
		if !gorapide.CanonicalValueMatchesPredefinedType(value, typeName) {
			return gorapide.RapideRecordObjectDeclaration{}, behaviorBinding{},
				typeError(literal.Value.Position,
					"module Record object %q field %q evaluates as %s, want %s",
					object.Name, declaration.Name, evaluatedType, typeName)
		}
		fields = append(fields, gorapide.RapideRecordObjectField(declaration.Name, value))
		constant := arch.LiteralValue(value)
		fieldBindings[key] = behaviorBinding{
			name: declaration.Name, typeName: typeName, constant: &constant,
		}
	}
	extraLiterals := append([]RecordFieldExpressionDecl(nil), object.Initial.RecordFields...)
	sort.Slice(extraLiterals, func(left, right int) bool {
		return folded(extraLiterals[left].Name) < folded(extraLiterals[right].Name)
	})
	for _, literal := range extraLiterals {
		if !expected[folded(literal.Name)] {
			return gorapide.RapideRecordObjectDeclaration{}, behaviorBinding{},
				typeError(literal.Position,
					"module Record object %q literal field %q is not declared by contextual Record type %q",
					object.Name, literal.Name, recordType.Name)
		}
	}
	declaration, err := gorapide.NewRapideRecordObjectDeclaration(object.Name, structuralType, fields...)
	if err != nil {
		return gorapide.RapideRecordObjectDeclaration{}, behaviorBinding{},
			typeError(object.Position, "module Record object %q: %v", object.Name, err)
	}
	return declaration, behaviorBinding{
		name: object.Name, typeName: recordType.Name, recordFields: fieldBindings, structural: true,
	}, nil
}

func compileModuleTimingObjectBindings(
	module ModuleDecl,
	objects map[string]behaviorBinding,
	clocks map[string]bool,
) (map[string]behaviorBinding, error) {
	result := make(map[string]behaviorBinding, len(module.TimingObjects))
	clockNames := make(map[string]string, len(module.Clocks))
	for _, clock := range module.Clocks {
		clockNames[folded(clock.Name)] = clock.Name
	}
	if err := validateModuleBehaviorDoLabels(module); err != nil {
		return nil, err
	}
	for _, object := range module.TimingObjects {
		key := folded(object.Name)
		if objects[key].name != "" || result[key].name != "" {
			return nil, typeError(object.Position, "duplicate module timing object %q", object.Name)
		}
		clockKey := folded(object.Clock)
		clock, exists := clockNames[clockKey]
		if !exists || !clocks[clockKey] {
			return nil, typeError(object.Position,
				"module timing object %q refers to undeclared clock %q", object.Name, object.Clock)
		}
		value, err := evaluateClosedTimingExpression(
			object.Initial, objects, clock, "module timing object "+object.Name,
		)
		if err != nil {
			return nil, err
		}
		constant := arch.LiteralValue(int64(value))
		result[key] = behaviorBinding{
			name: object.Name, typeName: "Ticks", timingClock: clock, constant: &constant,
		}
	}
	return result, nil
}

func moduleExecutionPredefinedType(
	position Position,
	name string,
	module ModuleDecl,
	typeElaborator *sourceTypeElaborator,
) (string, error) {
	local := make(map[string]TypeAliasDecl, len(module.Types))
	for _, declaration := range module.Types {
		local[folded(declaration.Name)] = declaration
	}
	seen := make(map[string]bool, len(local))
	path := make([]string, 0, len(local)+1)
	for {
		key := folded(name)
		declaration, exists := local[key]
		if !exists {
			return typeElaborator.executionPredefinedType(position, name)
		}
		if seen[key] {
			path = append(path, declaration.Name)
			return "", typeError(position, "module type alias cycle %s", strings.Join(path, " -> "))
		}
		seen[key] = true
		path = append(path, declaration.Name)
		if declaration.Expression.Kind == TypeExpressionApplication {
			return typeElaborator.executionPredefinedTypeExpression(
				declaration.Position, declaration.Target, declaration.Expression,
			)
		}
		name = declaration.Target
	}
}

func validateModuleObjectNameConflicts(module ModuleDecl) error {
	objectNames := make(map[string]bool, len(module.Parameters)+len(module.Objects)+len(module.TimingObjects))
	for _, parameter := range module.Parameters {
		objectNames[folded(parameter.Name)] = true
	}
	for _, object := range module.Objects {
		key := folded(object.Name)
		if objectNames[key] {
			return typeError(object.Position, "duplicate module object %q", object.Name)
		}
		objectNames[key] = true
	}
	for _, object := range module.TimingObjects {
		key := folded(object.Name)
		if objectNames[key] {
			return typeError(object.Position, "duplicate module object %q", object.Name)
		}
		objectNames[key] = true
	}
	conflicts := make(map[string]bool)
	record := func(kind, name string) {
		key := folded(name)
		if objectNames[key] {
			conflicts[fmt.Sprintf("object %s conflicts with %s %s", key, kind, key)] = true
		}
	}
	for _, declaration := range module.Types {
		record("type", declaration.Name)
	}
	for _, declaration := range module.States {
		record("state", declaration.Name)
	}
	for _, declaration := range module.Functions {
		record("function", declaration.Name)
	}
	for _, declaration := range module.Clocks {
		record("clock", declaration.Name)
	}
	if len(conflicts) == 0 {
		return nil
	}
	diagnostics := make([]string, 0, len(conflicts))
	for diagnostic := range conflicts {
		diagnostics = append(diagnostics, diagnostic)
	}
	sort.Strings(diagnostics)
	return typeError(module.Position, "module %q has conflicting declarations: %s",
		module.Name, strings.Join(diagnostics, ", "))
}

func compileStateDeclarations(
	owner string,
	declarations []StateDecl,
	objects map[string]behaviorBinding,
) ([]arch.StateDeclaration, map[string]behaviorBinding, error) {
	result := make([]arch.StateDeclaration, 0, len(declarations))
	bindings := make(map[string]behaviorBinding, len(declarations))
	for _, state := range declarations {
		key := folded(state.Name)
		if bindings[key].name != "" || objects[key].name != "" {
			return nil, nil, typeError(state.Position, "duplicate %s %q", owner, state.Name)
		}
		if state.TypeExpression.Kind == TypeExpressionApplication {
			return nil, nil, typeError(state.Position,
				"%s %q type-constructor application %s cannot yet be used in the predefined-scalar state value kernel",
				owner, state.Name, typeExpressionSpelling(state.TypeExpression))
		}
		typeName, ok := predefinedTypes[folded(state.Type)]
		if !ok {
			return nil, nil, typeError(state.Position, "%s %q has unsupported type %q", owner, state.Name, state.Type)
		}
		if !sourceClosedScalarType(typeName) {
			return nil, nil, typeError(state.Position, "%s %q type %s is outside the predefined-scalar state subset", owner, state.Name, typeName)
		}
		if state.Initial == nil {
			return nil, nil, typeError(state.Position, "%s %q requires an explicit initializer in the deterministic profile", owner, state.Name)
		}
		compiled, err := compileBehaviorExpression(*state.Initial, objects, nil)
		if err != nil {
			return nil, nil, err
		}
		if !sourceBehaviorExpressionAssignable(compiled, typeName) {
			return nil, nil, typeError(state.Initial.Position, "%s %q initializer has type %s, want %s", owner, state.Name, compiled.typeName, typeName)
		}
		initial, evaluatedType, err := arch.EvaluateConstant(compiled.value)
		if err != nil {
			return nil, nil, typeError(state.Initial.Position, "%s %q initializer is not a closed deterministic constant: %v", owner, state.Name, err)
		}
		if !gorapide.CanonicalValueMatchesPredefinedType(initial, typeName) {
			return nil, nil, typeError(state.Initial.Position, "%s %q initializer evaluates as %s, want %s", owner, state.Name, evaluatedType, typeName)
		}
		result = append(result, arch.StateReference(state.Name, typeName, initial))
		bindings[key] = behaviorBinding{name: state.Name, typeName: typeName, state: true}
	}
	return result, bindings, nil
}

func compileInterfaceBehavior(declaration InterfaceDecl, states map[string]behaviorBinding) ([]*arch.FunctionImplementation, error) {
	if declaration.Behavior == nil {
		return nil, nil
	}
	return compileFunctionBodies(declaration, declaration.Behavior.Functions, states, nil, "behavior function")
}

func compileFunctionBodies(
	declaration InterfaceDecl,
	bodies []FunctionBodyDecl,
	states map[string]behaviorBinding,
	objects map[string]behaviorBinding,
	owner string,
) ([]*arch.FunctionImplementation, error) {
	result := make([]*arch.FunctionImplementation, 0, len(bodies))
	seen := make(map[string]bool, len(bodies))
	for _, body := range bodies {
		if position, exists := unidentifiedDeclarationBearingDoPosition(body.Statements); exists {
			return nil, typeError(position,
				"declaration-bearing do in a function currently requires a concrete module generator")
		}
		if position, exists := unidentifiedDeclarationBearingDoHandlerPosition(body.Handler); exists {
			return nil, typeError(position,
				"declaration-bearing do in a function currently requires a concrete module generator")
		}
		signature, err := matchProvidedFunctionBody(declaration, body, owner)
		if err != nil {
			return nil, err
		}
		for index, bodyParameter := range body.Parameters {
			if bodyParameter.Default == nil {
				continue
			}
			declaredParameter := signature.Parameters[index]
			if declaredParameter.Default == nil {
				return nil, typeError(bodyParameter.Default.Position,
					"%s %q parameter %q repeats a default absent from its provided interface declaration",
					owner, body.Name, bodyParameter.Name)
			}
			typeName := canonicalPredefinedType(declaredParameter.Type)
			bodyDefault, _, err := evaluateClosedGeneratorDefault(bodyParameter, typeName, "function body")
			if err != nil {
				return nil, err
			}
			declaredDefault, _, err := evaluateClosedGeneratorDefault(declaredParameter, typeName, "function")
			if err != nil {
				return nil, err
			}
			if fmt.Sprintf("%T:%v", bodyDefault, bodyDefault) != fmt.Sprintf("%T:%v", declaredDefault, declaredDefault) {
				return nil, typeError(bodyParameter.Default.Position,
					"%s %q parameter %q default does not denote the provided interface default",
					owner, body.Name, bodyParameter.Name)
			}
		}
		parameters := make([]arch.ParamDecl, len(signature.Parameters))
		bindings := copyBehaviorBindings(objects, len(body.Parameters))
		for index, parameter := range signature.Parameters {
			typeName := predefinedTypes[folded(parameter.Type)]
			parameters[index] = arch.P(parameter.Name, typeName)
			if parameter.Default != nil {
				value, _, err := evaluateClosedGeneratorDefault(parameter, typeName, "function")
				if err != nil {
					return nil, err
				}
				parameters[index] = arch.PDefault(parameter.Name, typeName, value)
			}
			key := folded(body.Parameters[index].Name)
			if bindings[key].name != "" {
				return nil, typeError(body.Parameters[index].Position, "%s parameter %q conflicts with a module object", owner, body.Parameters[index].Name)
			}
			bindings[key] = behaviorBinding{name: parameter.Name, typeName: typeName}
		}
		locals := make([]arch.FunctionModuleLocal, 0, len(body.Objects))
		for _, object := range body.Objects {
			key := folded(object.Name)
			if bindings[key].name != "" || states[key].name != "" {
				return nil, typeError(object.Position, "%s local object %q conflicts with an enclosing declaration", owner, object.Name)
			}
			if object.TypeExpression.Kind != TypeExpressionName || !keyword(object.Type, declaration.Name) {
				return nil, typeError(object.Position,
					"%s local object %q requires the enclosing interface type %s in the current function-local module slice",
					owner, object.Name, declaration.Name)
			}
			if object.Initial.Kind != ExpressionCall || object.Initial.Left != nil ||
				!keyword(object.Initial.Name, "New") {
				return nil, typeError(object.Initial.Position,
					"%s local module %q requires a direct owner allocator New initializer",
					owner, object.Name)
			}
			initial, err := compileBehaviorExpression(object.Initial, bindings, states)
			if err != nil {
				return nil, err
			}
			if !keyword(initial.typeName, declaration.Name) {
				return nil, typeError(object.Initial.Position,
					"%s local module %q initializer has type %s, want %s",
					owner, object.Name, initial.typeName, declaration.Name)
			}
			locals = append(locals, arch.ModuleLocal(key, initial.value))
			bindings[key] = behaviorBinding{name: key, typeName: declaration.Name, moduleValue: true}
		}
		returnType := ""
		if signature.ReturnType != "" {
			returnType = predefinedTypes[folded(signature.ReturnType)]
		}
		key := compiledFunctionKey(signature, parameters, returnType)
		if seen[key] {
			return nil, typeError(body.Position, "duplicate %s implementation for function %q", owner, body.Name)
		}
		seen[key] = true
		statements := make([]arch.Statement, 0, len(body.Statements))
		for index, sourceStatement := range body.Statements {
			statement, err := compileBehaviorStatement(declaration, body.Name, index, sourceStatement, bindings, states, &returnType)
			if err != nil {
				return nil, err
			}
			statements = append(statements, statement)
		}
		builder := arch.Function(signature.Name, returnType, parameters...).WithLocals(locals...)
		if returnType != "" {
			if body.Return == nil {
				return nil, typeError(body.Position, "typed %s %q requires a final return expression", owner, body.Name)
			}
			returned, err := compileBehaviorExpression(*body.Return, bindings, states)
			if err != nil {
				return nil, err
			}
			if !sourceBehaviorExpressionAssignable(returned, returnType) {
				return nil, typeError(body.Return.Position, "%s %q returns %s, want %s", owner, body.Name, returned.typeName, returnType)
			}
			if body.Handler != nil {
				statements = append(statements, arch.ReturnFromFunction(returned.value))
			}
			builder.Returns(returned.value)
		} else if body.Return != nil {
			return nil, typeError(body.Return.Position, "void %s %q cannot return a value", owner, body.Name)
		}
		if body.Handler != nil {
			if len(statements) == 0 {
				return nil, typeError(body.Handler.Position,
					"%s %q handler requires a nonempty protected statement list", owner, body.Name)
			}
			handler, err := compileProcessExceptionHandler(
				declaration, *body.Handler, owner+" "+body.Name,
				bindings, states, &returnType, true,
			)
			if err != nil {
				return nil, err
			}
			statements = []arch.Statement{arch.HandleExceptions(statements, handler)}
		}
		builder.Do(statements...)
		result = append(result, builder.Build())
	}
	return result, nil
}

func compileInterfaceBehaviorRules(
	declaration InterfaceDecl,
	states map[string]behaviorBinding,
	typeElaborator *sourceTypeElaborator,
) ([]*arch.DeclarativeRule, error) {
	if declaration.Behavior == nil {
		return nil, nil
	}
	result := make([]*arch.DeclarativeRule, 0, len(declaration.Behavior.Rules))
	seen := make(map[string]bool, len(declaration.Behavior.Rules))
	for _, rule := range declaration.Behavior.Rules {
		key := behaviorRuleSemanticKey(rule)
		if seen[key] {
			return nil, typeError(rule.Position, "duplicate behavior rule %s", key)
		}
		seen[key] = true
		bindings, err := compilePatternBindings(rule.Placeholders, "behavior")
		if err != nil {
			return nil, err
		}
		bound := make(map[string]bool, len(rule.Placeholders))
		trigger, err := compileBehaviorPattern(declaration, rule.Trigger, rule.Placeholders, bindings, bound, typeElaborator)
		if err != nil {
			return nil, err
		}
		for _, placeholder := range rule.Placeholders {
			if !bound[folded(placeholder.Name)] {
				return nil, typeError(placeholder.Position, "behavior placeholder %s is never bound by the trigger", patternPlaceholderDisplay(placeholder))
			}
		}
		var guard *arch.RuleValue
		if rule.Guard != nil {
			compiled, err := compileBehaviorExpression(*rule.Guard, bindings, states)
			if err != nil {
				return nil, err
			}
			if compiled.typeName != "Boolean" {
				return nil, typeError(rule.Guard.Position, "behavior rule guard has type %s, want Boolean", compiled.typeName)
			}
			guardValue := compiled.value
			guard = &guardValue
		}
		statements := make([]arch.Statement, 0, len(rule.Statements))
		for index, sourceStatement := range rule.Statements {
			statement, err := compileBehaviorStatement(declaration, "rule:"+key, index, sourceStatement, bindings, states, nil)
			if err != nil {
				return nil, err
			}
			statements = append(statements, statement)
		}
		builder := arch.Rule("rpd:behavior:" + key).On(trigger)
		if guard != nil {
			builder.Where(*guard)
		}
		if rule.Connector == ConnectAgent {
			builder.Agent()
		} else {
			builder.Pipe()
		}
		if len(statements) == 0 {
			builder.NoEvents()
		} else {
			builder.Do(statements...)
		}
		result = append(result, builder.Build())
	}
	return result, nil
}

func compileModuleInitial(
	module ModuleDecl,
	declaration InterfaceDecl,
	objects map[string]behaviorBinding,
	states map[string]behaviorBinding,
) ([]arch.Statement, error) {
	if len(module.Initial) == 0 {
		return nil, nil
	}
	clockNames := make(map[string]string, len(module.Clocks))
	for _, clock := range module.Clocks {
		clockNames[folded(clock.Name)] = clock.Name
	}
	normalized, err := normalizeModuleTimingStatements(module, module.Initial, clockNames, objects)
	if err != nil {
		return nil, err
	}
	owner := "module:" + folded(module.Name) + ":initial"
	result := make([]arch.Statement, 0, len(normalized))
	for index, sourceStatement := range normalized {
		if err := validateModuleInitialStatement(declaration, sourceStatement); err != nil {
			return nil, err
		}
		statement, err := compileBehaviorStatement(
			declaration, owner, index, sourceStatement, objects, states, nil,
		)
		if err != nil {
			return nil, err
		}
		result = append(result, statement)
	}
	return result, nil
}

func compileModuleInitializationParameters(
	module ModuleDecl,
	objects map[string]behaviorBinding,
	states map[string]behaviorBinding,
	typeElaborator *sourceTypeElaborator,
) ([]arch.ModuleInitializationParameter, map[string]behaviorBinding, error) {
	bindings := copyBehaviorBindings(objects, len(module.InitialParameters))
	defaultBindings := copyBehaviorBindings(objects, len(module.InitialParameters))
	if len(module.InitialParameters) == 0 {
		return nil, bindings, nil
	}
	result := make([]arch.ModuleInitializationParameter, 0, len(module.InitialParameters))
	seen := make(map[string]bool, len(module.InitialParameters))
	for _, parameter := range module.InitialParameters {
		key := folded(parameter.Name)
		if key == "" || seen[key] || bindings[key].name != "" || states[key].name != "" {
			return nil, nil, typeError(parameter.Position,
				"module %q initialization parameter %q is empty, duplicate, or conflicts with a module constituent",
				module.Name, parameter.Name)
		}
		typeName, err := typeElaborator.executionPredefinedTypeExpression(
			parameter.Position, parameter.Type, parameter.TypeExpression,
		)
		if err != nil {
			return nil, nil, err
		}
		if !sourceClosedScalarType(typeName) {
			return nil, nil, typeError(parameter.Position,
				"module %q initialization parameter %q type %s is outside the predefined-scalar subset",
				module.Name, parameter.Name, typeName)
		}
		if parameter.Default == nil {
			return nil, nil, typeError(parameter.Position,
				"module %q initialization parameter %q requires a default association",
				module.Name, parameter.Name)
		}
		compiled, err := compileBehaviorExpression(*parameter.Default, defaultBindings, states)
		if err != nil {
			return nil, nil, typeError(parameter.Default.Position,
				"module %q initialization parameter %q default: %v",
				module.Name, parameter.Name, err)
		}
		if !sourceBehaviorExpressionAssignable(compiled, typeName) {
			return nil, nil, typeError(parameter.Default.Position,
				"module %q initialization parameter %q default has type %s, want %s",
				module.Name, parameter.Name, compiled.typeName, typeName)
		}
		result = append(result, arch.ModuleInitialParameter(parameter.Name, typeName, compiled.value))
		bindings[key] = behaviorBinding{name: parameter.Name, typeName: typeName}
		defaultBindings[key] = behaviorBinding{
			name: arch.ModuleInitializationFormalBinding(parameter.Name), typeName: typeName,
		}
		seen[key] = true
	}
	return result, bindings, nil
}

// compileModuleFinal lowers the closed immediate Stanford final-part slice.
// Finalization is not ordinary process continuation: the supported source
// form is an ordered, non-timed tree of closed out/private actions, direct
// unconnected local provided-function calls, null/do blocks, local exception
// or self-generated action interruption, if/case selection, assertions,
// immediate indefinite do/while control, and finite closed Integer range
// iteration. The
// deterministic kernel separately rejects open/stateful values and executes the
// tree after the complete name-loss frontier and before implicit Finish.
func compileModuleFinal(
	module ModuleDecl,
	declaration InterfaceDecl,
	objects map[string]behaviorBinding,
	states map[string]behaviorBinding,
) ([]arch.Statement, error) {
	if len(module.Final) == 0 {
		return nil, nil
	}
	owner := "module:" + folded(module.Name) + ":final"
	result := make([]arch.Statement, 0, len(module.Final))
	for index, sourceStatement := range module.Final {
		if err := validateModuleFinalStatement(declaration, sourceStatement); err != nil {
			return nil, err
		}
		statement, err := compileBehaviorStatement(
			declaration, owner, index, sourceStatement, objects, states, nil,
		)
		if err != nil {
			return nil, err
		}
		result = append(result, statement)
	}
	return result, nil
}

func validateModuleFinalStatement(declaration InterfaceDecl, statement BehaviorStatementDecl) error {
	switch statement.Kind {
	case BehaviorCallStatement:
		if statement.Call.Timing != nil {
			return typeError(statement.Call.Timing.Position,
				"module final action timing requires resumable finalization outside the current source subset")
		}
		action, exists := findAction(declaration, statement.Call.Name)
		if exists && action.Mode != ActionOut && action.Mode != ActionPrivate {
			return typeError(statement.Call.Position,
				"module final call %q must name a declared out- or private-action or a local provided function in the current source subset",
				statement.Call.Name)
		}
		if exists {
			break
		}
		functions := findFunctions(declaration, statement.Call.Name)
		callable := false
		for _, function := range functions {
			callable = callable || function.Mode == FunctionProvides || function.Mode == FunctionRequires
		}
		if !callable {
			return typeError(statement.Call.Position,
				"module final function %q must be a declared provided function or a required function resolved by a generator-owned self connection",
				statement.Call.Name)
		}
	case BehaviorNullStatement, BehaviorRaiseStatement, BehaviorReraiseStatement:
	case BehaviorAssertStatement:
	case BehaviorIfStatement:
		for _, child := range statement.Then {
			if err := validateModuleFinalStatement(declaration, child); err != nil {
				return err
			}
		}
		for _, child := range statement.Else {
			if err := validateModuleFinalStatement(declaration, child); err != nil {
				return err
			}
		}
	case BehaviorCaseStatement:
		for _, alternative := range statement.Cases {
			for _, child := range alternative.Body {
				if err := validateModuleFinalStatement(declaration, child); err != nil {
					return err
				}
			}
		}
		for _, child := range statement.Default {
			if err := validateModuleFinalStatement(declaration, child); err != nil {
				return err
			}
		}
	case BehaviorLoopStatement:
		for _, child := range statement.Body {
			if err := validateModuleFinalStatement(declaration, child); err != nil {
				return err
			}
		}
	case BehaviorForStatement:
		if statement.ForInitial != nil || statement.ForTest != nil || statement.ForNext != nil {
			return typeError(statement.Position,
				"module final general for requires the general finalization-control slice")
		}
		for _, child := range statement.Body {
			if err := validateModuleFinalStatement(declaration, child); err != nil {
				return err
			}
		}
	case BehaviorExitStatement, BehaviorNextStatement:
	case BehaviorDoStatement:
		for _, child := range statement.Body {
			if err := validateModuleFinalStatement(declaration, child); err != nil {
				return err
			}
		}
		if statement.Handler != nil {
			for _, choice := range statement.Handler.Choices {
				for _, child := range choice.Statements {
					if err := validateModuleFinalStatement(declaration, child); err != nil {
						return err
					}
				}
			}
			for _, child := range statement.Handler.Else {
				if err := validateModuleFinalStatement(declaration, child); err != nil {
					return err
				}
			}
		}
	default:
		return typeError(statement.Position,
			"module final part currently supports only the immediate action/local-function/null/do/raise/if/case/assert/loop/range-for/exit/next subset; %s requires the general finalization-control slice",
			statement.Kind)
	}
	return nil
}

func compileArchitectureInitial(
	architecture ArchitectureDecl,
	declaration InterfaceDecl,
	bindings map[string]behaviorBinding,
) ([]arch.Statement, []arch.ExceptionDeclaration, error) {
	if len(architecture.Initial) == 0 {
		return nil, nil, nil
	}
	architecture.Initial = assignArchitectureInitializerBehaviorDoExceptionIdentities(
		architecture.Name, "initial", architecture.Initial,
	)
	declaration.ExceptionScopes = append(
		[]ExceptionScopeDecl{{Path: []string{architecture.Name}}},
		cloneExceptionScopeDeclarations(declaration.ExceptionScopes)...,
	)
	scopes, err := architectureInitialDoExceptionScopes(architecture)
	if err != nil {
		return nil, nil, err
	}
	exceptions := make([]arch.ExceptionDeclaration, 0)
	for _, scope := range scopes {
		compiled, err := compileExceptionDeclarations(
			"architecture initial declaration-bearing do "+strings.Join(scope.Path, "::"),
			scope.Exceptions,
		)
		if err != nil {
			return nil, nil, err
		}
		exceptions = mergeCompiledExceptionCatalogs(exceptions, compiled)
	}
	owner := "architecture:" + folded(architecture.Name) + ":initial"
	callDeclaration := declaration
	callDeclaration.Functions = make([]FunctionDecl, 0, len(declaration.Functions))
	for _, function := range declaration.Functions {
		if function.Mode == FunctionProvides {
			callDeclaration.Functions = append(callDeclaration.Functions, function)
		}
	}
	result := make([]arch.Statement, 0, len(architecture.Initial))
	for index, sourceStatement := range architecture.Initial {
		if err := validateArchitectureInitialStatement(declaration, sourceStatement); err != nil {
			return nil, nil, err
		}
		statement, err := compileBehaviorStatement(
			callDeclaration, owner, index, sourceStatement, bindings, nil, nil,
		)
		if err != nil {
			return nil, nil, err
		}
		result = append(result, statement)
	}
	return result, exceptions, nil
}

func validateArchitectureInitialStatement(declaration InterfaceDecl, statement BehaviorStatementDecl) error {
	if statement.Kind == BehaviorAssignmentStatement {
		return typeError(statement.Position,
			"architecture initial assignments require architecture state outside the current source subset")
	}
	if statement.Kind == BehaviorCallStatement {
		action, actionExists := findAction(declaration, statement.Call.Name)
		if actionExists && action.Mode == ActionPrivate {
			return typeError(statement.Call.Position,
				"architecture initial cannot generate private action %q in the current returned-interface subset", statement.Call.Name)
		}
		if !actionExists {
			functions := findFunctions(declaration, statement.Call.Name)
			if len(functions) != 0 {
				provided := false
				for _, function := range functions {
					provided = provided || function.Mode == FunctionProvides
				}
				if !provided {
					return typeError(statement.Call.Position,
						"architecture initial function %q is not a returned-interface provides function connected inward", statement.Call.Name)
				}
			}
		}
	}
	if statement.Kind == BehaviorTimedStatement || statement.Call.Timing != nil {
		return typeError(statement.Position,
			"architecture initial timing requires architecture clocks outside the current source subset")
	}
	if statement.Kind == BehaviorForStatement &&
		(statement.ForInitial != nil || statement.ForTest != nil || statement.ForNext != nil) {
		return typeError(statement.Position,
			"architecture initial general for is outside the current deterministic startup subset")
	}
	groups := [][]BehaviorStatementDecl{statement.Then, statement.Else, statement.Body, statement.Default}
	for _, group := range groups {
		for _, child := range group {
			if err := validateArchitectureInitialStatement(declaration, child); err != nil {
				return err
			}
		}
	}
	for _, alternative := range statement.Cases {
		for _, child := range alternative.Body {
			if err := validateArchitectureInitialStatement(declaration, child); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateModuleInitialStatement(declaration InterfaceDecl, statement BehaviorStatementDecl) error {
	if statement.Kind == BehaviorTimedStatement {
		return typeError(statement.Position,
			"module initial pause/delay requires startup continuation semantics outside the current source subset")
	}
	if statement.Call.Timing != nil && statement.Call.Timing.Kind != TimingIn {
		return typeError(statement.Call.Timing.Position,
			"module initial action %s timing requires startup continuation semantics outside the current source subset",
			statement.Call.Timing.Kind)
	}
	groups := [][]BehaviorStatementDecl{statement.Then, statement.Else, statement.Body, statement.Default}
	for _, group := range groups {
		for _, child := range group {
			if err := validateModuleInitialStatement(declaration, child); err != nil {
				return err
			}
		}
	}
	for _, alternative := range statement.Cases {
		for _, child := range alternative.Body {
			if err := validateModuleInitialStatement(declaration, child); err != nil {
				return err
			}
		}
	}
	if statement.Handler != nil {
		for _, choice := range statement.Handler.Choices {
			for _, child := range choice.Statements {
				if err := validateModuleInitialStatement(declaration, child); err != nil {
					return err
				}
			}
		}
		for _, child := range statement.Handler.Else {
			if err := validateModuleInitialStatement(declaration, child); err != nil {
				return err
			}
		}
	}
	return nil
}

func compileModuleProcesses(
	module ModuleDecl,
	declaration InterfaceDecl,
	objects map[string]behaviorBinding,
	states map[string]behaviorBinding,
	typeElaborator *sourceTypeElaborator,
) ([]*arch.DeclarativeProcess, error) {
	type sourceProcess struct {
		declaration ModuleProcessDecl
		key         string
	}
	clockNames := make(map[string]string, len(module.Clocks))
	for _, clock := range module.Clocks {
		clockNames[folded(clock.Name)] = clock.Name
	}
	sources := make([]sourceProcess, len(module.Processes))
	for index, process := range module.Processes {
		normalized := process
		var err error
		normalized.Statements, err = normalizeModuleTimingStatements(module, process.Statements, clockNames, objects)
		if err != nil {
			return nil, err
		}
		normalized.Alternatives = make([]ModuleAwaitAlternativeDecl, len(process.Alternatives))
		for alternativeIndex, alternative := range process.Alternatives {
			normalized.Alternatives[alternativeIndex] = alternative
			normalized.Alternatives[alternativeIndex].Statements, err = normalizeModuleTimingStatements(
				module, alternative.Statements, clockNames, objects,
			)
			if err != nil {
				return nil, err
			}
		}
		normalized.Else, err = normalizeModuleTimingStatements(module, process.Else, clockNames, objects)
		if err != nil {
			return nil, err
		}
		normalized.Handler, err = normalizeModuleTimingHandler(module, process.Handler, clockNames, objects)
		if err != nil {
			return nil, err
		}
		sources[index] = sourceProcess{declaration: normalized, key: moduleProcessSemanticKey(normalized)}
	}
	sort.SliceStable(sources, func(i, j int) bool { return sources[i].key < sources[j].key })
	result := make([]*arch.DeclarativeProcess, 0, len(sources))
	for index, source := range sources {
		process := source.declaration
		owner := fmt.Sprintf("module:%s:process:%06d", folded(module.Name), index)
		processDeclaration, err := qualifyBehaviorDoExceptionScope(declaration, BehaviorStatementDecl{
			Position: process.Position, Kind: BehaviorDoStatement, Label: process.Label,
			Exceptions: process.OuterExceptions,
		})
		if err != nil {
			return nil, err
		}
		iterationDeclaration, err := qualifyBehaviorWhenIterationExceptionScope(
			processDeclaration, process,
		)
		if err != nil {
			return nil, err
		}
		if process.Await {
			type sourceAlternative struct {
				declaration ModuleAwaitAlternativeDecl
				key         string
			}
			sourceAlternatives := make([]sourceAlternative, len(process.Alternatives))
			for alternativeIndex, alternative := range process.Alternatives {
				sourceAlternatives[alternativeIndex] = sourceAlternative{
					declaration: alternative, key: moduleAwaitAlternativeSemanticKey(alternative),
				}
			}
			sort.SliceStable(sourceAlternatives, func(i, j int) bool {
				return sourceAlternatives[i].key < sourceAlternatives[j].key
			})
			alternatives := make([]arch.AwaitAlternative, 0, len(sourceAlternatives))
			for alternativeIndex, sourceAlternative := range sourceAlternatives {
				alternative := sourceAlternative.declaration
				bindings, err := compilePatternBindings(alternative.Placeholders, "module-await alternative")
				if err != nil {
					return nil, err
				}
				for key, object := range objects {
					if bindings[key].name != "" {
						return nil, typeError(alternative.Position,
							"module-await placeholder %q conflicts with module object %q", bindings[key].name, object.name)
					}
					bindings[key] = object
				}
				bound := make(map[string]bool, len(alternative.Placeholders))
				trigger, err := compileBehaviorPattern(
					processDeclaration, alternative.Trigger, alternative.Placeholders, bindings, bound, typeElaborator,
				)
				if err != nil {
					return nil, err
				}
				for _, placeholder := range alternative.Placeholders {
					if !bound[folded(placeholder.Name)] {
						return nil, typeError(placeholder.Position,
							"module-await placeholder %s is never bound by its alternative trigger",
							patternPlaceholderDisplay(placeholder))
					}
				}
				var guard *arch.RuleValue
				if alternative.Guard != nil {
					compiled, err := compileBehaviorExpression(*alternative.Guard, bindings, states)
					if err != nil {
						return nil, err
					}
					if compiled.typeName != "Boolean" {
						return nil, typeError(alternative.Guard.Position,
							"module-await alternative guard has type %s, want Boolean", compiled.typeName)
					}
					value := compiled.value
					guard = &value
				}
				statements := make([]arch.Statement, 0, len(alternative.Statements))
				alternativeOwner := fmt.Sprintf("%s:await:%06d", owner, alternativeIndex)
				for statementIndex, sourceStatement := range alternative.Statements {
					statement, err := compileBehaviorStatement(
						processDeclaration, alternativeOwner, statementIndex, sourceStatement, bindings, states, nil,
					)
					if err != nil {
						return nil, err
					}
					statements = append(statements, statement)
				}
				builder := arch.Await(fmt.Sprintf("alternative:%06d", alternativeIndex)).
					On(trigger).Do(statements...).Terminate()
				if guard != nil {
					builder.Where(*guard)
				}
				alternatives = append(alternatives, builder.Build())
			}
			var state arch.ProcessState
			if process.ElsePresent {
				bindings := make(map[string]behaviorBinding, len(objects))
				for key, object := range objects {
					bindings[key] = object
				}
				statements := make([]arch.Statement, 0, len(process.Else))
				for statementIndex, sourceStatement := range process.Else {
					statement, err := compileBehaviorStatement(
						processDeclaration, owner+":await:else", statementIndex, sourceStatement, bindings, states, nil,
					)
					if err != nil {
						return nil, err
					}
					statements = append(statements, statement)
				}
				elseBranch := arch.AwaitElse("else").Do(statements...).Terminate().Build()
				state = arch.AwaitStateWithElse("await", elseBranch, alternatives...)
			} else {
				state = arch.AwaitState("await", alternatives...)
			}
			result = append(result, arch.Process("rpd:"+owner).StartAt("await").States(state).Build())
			continue
		}
		bindings, err := compilePatternBindings(process.Placeholders, "module-process")
		if err != nil {
			return nil, err
		}
		for key, object := range objects {
			if bindings[key].name != "" {
				return nil, typeError(process.Position,
					"module-process placeholder %q conflicts with module object %q", bindings[key].name, object.name)
			}
			bindings[key] = object
		}
		var trigger pattern.Pattern
		if process.Entry {
			if len(process.Placeholders) != 0 || process.Guard != nil {
				return nil, typeError(process.Position,
					"ordinary module process entry cannot declare reactive placeholders or a guard")
			}
			trigger = pattern.IterateRange(
				pattern.MatchEvent("__gorapide_process_entry__"), pattern.RelationDisjoint, 0, 0,
			)
		} else {
			bound := make(map[string]bool, len(process.Placeholders))
			trigger, err = compileBehaviorPattern(processDeclaration, process.Trigger, process.Placeholders, bindings, bound, typeElaborator)
			if err != nil {
				return nil, err
			}
			for _, placeholder := range process.Placeholders {
				if !bound[folded(placeholder.Name)] {
					return nil, typeError(placeholder.Position, "module-process placeholder %s is never bound by the trigger", patternPlaceholderDisplay(placeholder))
				}
			}
		}
		var guard *arch.RuleValue
		if process.Guard != nil {
			compiled, err := compileBehaviorExpression(*process.Guard, bindings, states)
			if err != nil {
				return nil, err
			}
			if compiled.typeName != "Boolean" {
				return nil, typeError(process.Guard.Position, "module-process guard has type %s, want Boolean", compiled.typeName)
			}
			value := compiled.value
			guard = &value
		}
		statements := make([]arch.Statement, 0, len(process.Statements))
		for statementIndex, sourceStatement := range process.Statements {
			statement, err := compileBehaviorStatement(
				iterationDeclaration, owner, statementIndex, sourceStatement, bindings, states, nil,
			)
			if err != nil {
				return nil, err
			}
			statements = append(statements, statement)
		}
		if process.Handler != nil {
			handler, err := compileProcessExceptionHandler(
				iterationDeclaration, *process.Handler, owner, bindings, states, nil, true,
			)
			if err != nil {
				return nil, err
			}
			statements = []arch.Statement{arch.HandleExceptions(statements, handler)}
		}
		if process.Entry {
			result = append(result, arch.Process("rpd:"+owner).StartAt("entry").States(
				arch.AwaitState("entry", arch.Await("entry").On(trigger).Do(statements...).Terminate().Build()),
			).Build())
			continue
		}
		alternative := arch.Await("when").On(trigger).Do(statements...).Then("when")
		if guard != nil {
			alternative.Where(*guard)
		}
		state := arch.AwaitState("when", alternative.Build())
		if process.Label != "" {
			state = arch.NameWhenState(process.Label, state)
		}
		result = append(result, arch.Process("rpd:"+owner).StartAt("when").States(state).Build())
	}
	return result, nil
}

func validateModuleBehaviorDoLabels(module ModuleDecl) error {
	seen := make(map[string]bool)
	var visitStatements func([]BehaviorStatementDecl) error
	var visitHandler func(*BehaviorHandlerDecl) error
	add := func(position Position, label string) error {
		if label == "" {
			return nil
		}
		name := folded(label)
		if seen[name] {
			return typeError(position, "module process part overloads do label %q", name)
		}
		seen[name] = true
		return nil
	}
	visitHandler = func(handler *BehaviorHandlerDecl) error {
		if handler == nil {
			return nil
		}
		for _, choice := range handler.Choices {
			if err := visitStatements(choice.Statements); err != nil {
				return err
			}
		}
		return visitStatements(handler.Else)
	}
	visitStatements = func(statements []BehaviorStatementDecl) error {
		for _, statement := range statements {
			if err := add(statement.Position, statement.Label); err != nil {
				return err
			}
			for _, group := range [][]BehaviorStatementDecl{
				statement.Then, statement.Else, statement.Body, statement.Default,
			} {
				if err := visitStatements(group); err != nil {
					return err
				}
			}
			for _, alternative := range statement.Cases {
				if err := visitStatements(alternative.Body); err != nil {
					return err
				}
			}
			if err := visitHandler(statement.Handler); err != nil {
				return err
			}
		}
		return nil
	}
	for _, process := range module.Processes {
		if err := add(process.Position, process.Label); err != nil {
			return err
		}
		if err := visitStatements(process.Statements); err != nil {
			return err
		}
		for _, alternative := range process.Alternatives {
			if err := visitStatements(alternative.Statements); err != nil {
				return err
			}
		}
		if err := visitStatements(process.Else); err != nil {
			return err
		}
		if err := visitHandler(process.Handler); err != nil {
			return err
		}
	}
	if err := visitHandler(module.Handler); err != nil {
		return err
	}
	return nil
}

func compileProcessExceptionHandler(
	declaration InterfaceDecl,
	source BehaviorHandlerDecl,
	owner string,
	outerBindings, states map[string]behaviorBinding,
	functionReturnType *string,
	allowInterrupt bool,
) (arch.ExceptionHandler, error) {
	result := arch.ExceptionHandler{}
	seenChoices := make(map[string]bool, len(source.Choices))
	hasAnyChoice := false
	for _, choice := range source.Choices {
		if choice.Pattern.Kind != BehaviorBasicPattern || choice.Pattern.Event.ComponentPlaceholder ||
			choice.Pattern.Event.ComponentIndex != nil || choice.Pattern.Event.Attribute != "" {
			return arch.ExceptionHandler{}, typeError(choice.Position,
				"the current handler slice requires one unqualified basic exception or action pattern per choice, except a selected visible exception constituent")
		}
		event := choice.Pattern.Event
		choiceKind := "exception"
		choiceName := ""
		var parameters []ParameterDecl
		exceptionName := event.Name
		if event.Component != "" {
			exceptionName = event.Component + "." + event.Name
		}
		exception, exceptionExists := findExceptionReference(declaration, exceptionName, outerBindings)
		action, actionExists := ActionDecl{}, false
		if event.Component == "" {
			action, actionExists = findAction(declaration, event.Name)
		}
		switch {
		case event.Component == "" && keyword(event.Name, "any"):
			if !allowInterrupt {
				return arch.ExceptionHandler{}, typeError(event.Position,
					"module handler any pattern requires an enclosing active procedural block")
			}
			if len(event.Arguments) != 0 {
				return arch.ExceptionHandler{}, typeError(event.Position,
					"predefined any handler pattern cannot have parameter associations")
			}
			choiceKind = "any"
			choiceName = "any"
			hasAnyChoice = true
		case exceptionExists:
			choiceName = exception.Name
			parameters = exception.Parameters
		case actionExists && allowInterrupt:
			choiceKind = "action"
			choiceName = action.Name
			parameters = action.Parameters
		case actionExists:
			return arch.ExceptionHandler{}, typeError(event.Position,
				"module handler action %q requires an enclosing active procedural block", event.Name)
		case event.Component != "":
			return arch.ExceptionHandler{}, typeError(choice.Position,
				"the current handler slice requires one unqualified basic exception or action pattern per choice, except a selected visible exception constituent; %q is not such an exception",
				exceptionName)
		default:
			return arch.ExceptionHandler{}, typeError(event.Position,
				"handler choice names missing exception or visible action %q", event.Name)
		}
		choiceIdentity := folded(choiceName)
		if choiceKind == "exception" {
			choiceIdentity = exception.Declaration
		}
		choiceKey := choiceKind + "\x00" + choiceIdentity
		if seenChoices[choiceKey] {
			return arch.ExceptionHandler{}, typeError(event.Position,
				"handler choice names duplicate %s %q", choiceKind, event.Name)
		}
		seenChoices[choiceKey] = true
		formals := make(map[string]ParameterDecl, len(parameters))
		for _, formal := range parameters {
			formals[folded(formal.Name)] = formal
		}
		choiceBindings := copyBehaviorBindings(outerBindings, len(event.Arguments))
		seenFormals := make(map[string]bool, len(event.Arguments))
		seenPlaceholders := make(map[string]bool, len(event.Arguments))
		bindings := make([]arch.ExceptionHandlerBinding, 0, len(event.Arguments))
		for associationIndex, association := range event.Arguments {
			var formal ParameterDecl
			if association.Formal == "" {
				if associationIndex >= len(parameters) {
					return arch.ExceptionHandler{}, typeError(association.Position,
						"%s %q handler has too many positional associations", choiceKind, choiceName)
				}
				formal = parameters[associationIndex]
			} else {
				var ok bool
				formal, ok = formals[folded(association.Formal)]
				if !ok {
					return arch.ExceptionHandler{}, typeError(association.Position,
						"%s %q handler names unknown formal %q", choiceKind, choiceName, association.Formal)
				}
			}
			formalKey := folded(formal.Name)
			if seenFormals[formalKey] || association.Actual.Kind != ExpressionPlaceholder {
				return arch.ExceptionHandler{}, typeError(association.Position,
					"%s %q handler associations must uniquely bind direct existential placeholders", choiceKind, choiceName)
			}
			placeholderKey := folded(association.Actual.Name)
			if placeholderKey == "" || seenPlaceholders[placeholderKey] || choiceBindings[placeholderKey].name != "" {
				return arch.ExceptionHandler{}, typeError(association.Actual.Position,
					"handler placeholder ?%s is empty, duplicate, or conflicts with an outer declaration",
					association.Actual.Name)
			}
			seenFormals[formalKey] = true
			seenPlaceholders[placeholderKey] = true
			choiceBindings[placeholderKey] = behaviorBinding{
				name: association.Actual.Name, typeName: canonicalPredefinedType(formal.Type), placeholder: true,
			}
			bindings = append(bindings, arch.ExceptionHandlerBinding{
				Formal: formal.Name, Placeholder: association.Actual.Name,
				Type: canonicalPredefinedType(formal.Type),
			})
		}
		statements := make([]arch.Statement, 0, len(choice.Statements))
		for statementIndex, statement := range choice.Statements {
			compiled, err := compileBehaviorStatementWithHandlerContext(
				declaration, fmt.Sprintf("%s:handler:%s", owner, folded(choiceIdentity)),
				statementIndex, statement, choiceBindings, states, functionReturnType, choiceKind == "exception",
			)
			if err != nil {
				return arch.ExceptionHandler{}, err
			}
			statements = append(statements, compiled)
		}
		if choiceKind == "exception" {
			result.Choices = append(result.Choices, arch.HandleDeclaredException(
				exception.Declaration, choiceName, bindings, statements...,
			))
		} else if choiceKind == "action" {
			result.Choices = append(result.Choices, arch.HandleInterrupt(choiceName, bindings, statements...))
		} else {
			result.Choices = append(result.Choices, arch.HandleAnyEvent(statements...))
		}
	}
	if hasAnyChoice && (len(source.Choices) != 1 || source.Else != nil) {
		return arch.ExceptionHandler{}, typeError(source.Position,
			"predefined any pattern must be the handler's sole choice")
	}
	if source.Else != nil {
		result.Else = make([]arch.Statement, 0, len(source.Else))
		for statementIndex, statement := range source.Else {
			compiled, err := compileBehaviorStatementWithHandlerContext(
				declaration, owner+":handler:else", statementIndex,
				statement, outerBindings, states, functionReturnType, true,
			)
			if err != nil {
				return arch.ExceptionHandler{}, err
			}
			result.Else = append(result.Else, compiled)
		}
	}
	return result, nil
}

func moduleProcessSemanticKey(process ModuleProcessDecl) string {
	var builder strings.Builder
	if process.Entry {
		builder.WriteString("entry:")
	} else if process.Await {
		builder.WriteString("await:")
	} else {
		builder.WriteString("when:")
		builder.WriteString(folded(process.Label))
		builder.WriteByte(':')
		if len(process.OuterExceptions) != 0 {
			builder.WriteString("declare{")
			builder.WriteString(exceptionDeclarationsSemanticKey(process.OuterExceptions))
			builder.WriteString("}:")
		}
		if len(process.IterationExceptions) != 0 {
			builder.WriteString("iteration-declare{")
			builder.WriteString(exceptionDeclarationsSemanticKey(process.IterationExceptions))
			builder.WriteString("}:")
		}
	}
	if process.Await {
		alternatives := make([]string, 0, len(process.Alternatives))
		for _, alternative := range process.Alternatives {
			alternatives = append(alternatives, moduleAwaitAlternativeSemanticKey(alternative))
		}
		sort.Strings(alternatives)
		for _, alternative := range alternatives {
			builder.WriteByte('{')
			builder.WriteString(alternative)
			builder.WriteByte('}')
		}
		if process.ElsePresent {
			builder.WriteString(":else{")
			for _, statement := range process.Else {
				builder.WriteString(behaviorStatementKey(statement))
			}
			builder.WriteByte('}')
		}
		return builder.String()
	}
	placeholders := make([]string, 0, len(process.Placeholders))
	for _, placeholder := range process.Placeholders {
		placeholders = append(placeholders, patternPlaceholderSemanticKey(placeholder))
	}
	sort.Strings(placeholders)
	for _, placeholder := range placeholders {
		builder.WriteString(placeholder)
		builder.WriteByte(';')
	}
	if !process.Entry {
		builder.WriteString(behaviorPatternKey(process.Trigger))
	}
	if !process.Entry && process.Guard != nil {
		builder.WriteString(":where:")
		builder.WriteString(behaviorExpressionKey(*process.Guard))
	}
	for _, statement := range process.Statements {
		builder.WriteString(behaviorStatementKey(statement))
	}
	if process.Handler != nil {
		builder.WriteString(":handler{")
		choices := make([]string, 0, len(process.Handler.Choices))
		for _, choice := range process.Handler.Choices {
			var choiceKey strings.Builder
			choiceKey.WriteString(behaviorPatternKey(choice.Pattern))
			choiceKey.WriteString("=>{")
			for _, statement := range choice.Statements {
				choiceKey.WriteString(behaviorStatementKey(statement))
			}
			choiceKey.WriteByte('}')
			choices = append(choices, choiceKey.String())
		}
		sort.Strings(choices)
		for _, choice := range choices {
			builder.WriteString(choice)
		}
		if process.Handler.Else != nil {
			builder.WriteString(":else{")
			for _, statement := range process.Handler.Else {
				builder.WriteString(behaviorStatementKey(statement))
			}
			builder.WriteByte('}')
		}
		builder.WriteByte('}')
	}
	return builder.String()
}

func moduleAwaitAlternativeSemanticKey(alternative ModuleAwaitAlternativeDecl) string {
	var builder strings.Builder
	placeholders := make([]string, 0, len(alternative.Placeholders))
	for _, placeholder := range alternative.Placeholders {
		placeholders = append(placeholders, patternPlaceholderSemanticKey(placeholder))
	}
	sort.Strings(placeholders)
	for _, placeholder := range placeholders {
		builder.WriteString(placeholder)
		builder.WriteByte(';')
	}
	builder.WriteString(behaviorPatternKey(alternative.Trigger))
	if alternative.Guard != nil {
		builder.WriteString(":where:")
		builder.WriteString(behaviorExpressionKey(*alternative.Guard))
	}
	builder.WriteString(":do{")
	for _, statement := range alternative.Statements {
		builder.WriteString(behaviorStatementKey(statement))
	}
	builder.WriteByte('}')
	return builder.String()
}

func normalizeModuleTimingStatement(
	module ModuleDecl,
	statement BehaviorStatementDecl,
	clocks map[string]string,
	objects map[string]behaviorBinding,
) (BehaviorStatementDecl, error) {
	result := statement
	if statement.Call.Timing != nil {
		timing, err := normalizeModuleTiming(module, *statement.Call.Timing, clocks, objects)
		if err != nil {
			return BehaviorStatementDecl{}, err
		}
		result.Call.Timing = &timing
	}
	if statement.Timing != nil {
		timing, err := normalizeModuleTiming(module, *statement.Timing, clocks, objects)
		if err != nil {
			return BehaviorStatementDecl{}, err
		}
		result.Timing = &timing
	}
	var err error
	result.Then, err = normalizeModuleTimingStatements(module, statement.Then, clocks, objects)
	if err != nil {
		return BehaviorStatementDecl{}, err
	}
	result.Else, err = normalizeModuleTimingStatements(module, statement.Else, clocks, objects)
	if err != nil {
		return BehaviorStatementDecl{}, err
	}
	result.Body, err = normalizeModuleTimingStatements(module, statement.Body, clocks, objects)
	if err != nil {
		return BehaviorStatementDecl{}, err
	}
	result.Default, err = normalizeModuleTimingStatements(module, statement.Default, clocks, objects)
	if err != nil {
		return BehaviorStatementDecl{}, err
	}
	result.Cases = append([]BehaviorCaseAlternativeDecl(nil), statement.Cases...)
	for index := range result.Cases {
		result.Cases[index].Body, err = normalizeModuleTimingStatements(module, statement.Cases[index].Body, clocks, objects)
		if err != nil {
			return BehaviorStatementDecl{}, err
		}
	}
	result.Handler, err = normalizeModuleTimingHandler(module, statement.Handler, clocks, objects)
	if err != nil {
		return BehaviorStatementDecl{}, err
	}
	return result, nil
}

func normalizeModuleFunctionTimings(
	module ModuleDecl,
	functions []FunctionBodyDecl,
	objects map[string]behaviorBinding,
) ([]FunctionBodyDecl, error) {
	if functions == nil {
		return nil, nil
	}
	clockNames := make(map[string]string, len(module.Clocks))
	for _, clock := range module.Clocks {
		clockNames[folded(clock.Name)] = clock.Name
	}
	result := append([]FunctionBodyDecl(nil), functions...)
	for index := range result {
		statements, err := normalizeModuleTimingStatements(
			module, functions[index].Statements, clockNames, objects,
		)
		if err != nil {
			return nil, err
		}
		result[index].Statements = statements
		handler, err := normalizeModuleTimingHandler(
			module, functions[index].Handler, clockNames, objects,
		)
		if err != nil {
			return nil, err
		}
		result[index].Handler = handler
	}
	return result, nil
}

func normalizeModuleTimingHandler(
	module ModuleDecl,
	handler *BehaviorHandlerDecl,
	clocks map[string]string,
	objects map[string]behaviorBinding,
) (*BehaviorHandlerDecl, error) {
	if handler == nil {
		return nil, nil
	}
	result := *handler
	result.Choices = append([]BehaviorHandlerChoiceDecl(nil), handler.Choices...)
	for index := range result.Choices {
		statements, err := normalizeModuleTimingStatements(
			module, handler.Choices[index].Statements, clocks, objects,
		)
		if err != nil {
			return nil, err
		}
		result.Choices[index].Statements = statements
	}
	var err error
	result.Else, err = normalizeModuleTimingStatements(module, handler.Else, clocks, objects)
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func normalizeModuleTimingStatements(
	module ModuleDecl,
	statements []BehaviorStatementDecl,
	clocks map[string]string,
	objects map[string]behaviorBinding,
) ([]BehaviorStatementDecl, error) {
	if statements == nil {
		return nil, nil
	}
	result := make([]BehaviorStatementDecl, len(statements))
	for index, statement := range statements {
		normalized, err := normalizeModuleTimingStatement(module, statement, clocks, objects)
		if err != nil {
			return nil, err
		}
		result[index] = normalized
	}
	return result, nil
}

func normalizeModuleTiming(
	module ModuleDecl,
	timing TimingDecl,
	clocks map[string]string,
	objects map[string]behaviorBinding,
) (TimingDecl, error) {
	if timing.Name != "" {
		binding, exists := objects[folded(timing.Name)]
		if !exists || binding.name == "" {
			return TimingDecl{}, typeError(timing.Position,
				"named timing object %q is not declared in module %q", timing.Name, module.Name)
		}
		if binding.timingClock == "" || binding.constant == nil {
			return TimingDecl{}, typeError(timing.Position,
				"timing expression name %q has type %s, want an object of a declared clock's Ticks type",
				binding.name, binding.typeName)
		}
		value, evaluatedType, err := arch.EvaluateConstant(*binding.constant)
		if err != nil {
			return TimingDecl{}, typeError(timing.Position,
				"named timing object %q is not a closed deterministic constant: %v", binding.name, err)
		}
		integer, ok := value.(int64)
		if !ok || evaluatedType != "Integer" || integer < 0 {
			return TimingDecl{}, typeError(timing.Position,
				"named timing object %q evaluates to %#v, want a nonnegative clock tick", binding.name, value)
		}
		timing.Clock = binding.timingClock
		timing.First = uint64(integer)
		timing.Last = uint64(integer)
		timing.Name = ""
	}
	clock, exists := clocks[folded(timing.Clock)]
	if !exists {
		return TimingDecl{}, typeError(timing.Position, "module %q has no basic clock %q", module.Name, timing.Clock)
	}
	if timing.Value != nil {
		value, err := evaluateClosedTimingExpression(*timing.Value, objects, clock, "fixed timing")
		if err != nil {
			return TimingDecl{}, err
		}
		timing.First = value
		timing.Last = value
		timing.Value = nil
	}
	if timing.RangeFirst != nil {
		first, err := evaluateClosedTimingExpression(*timing.RangeFirst, objects, clock, "timing range lower-bound")
		if err != nil {
			return TimingDecl{}, err
		}
		timing.First = first
		timing.RangeFirst = nil
	}
	if timing.RangeLast != nil {
		last, err := evaluateClosedTimingExpression(*timing.RangeLast, objects, clock, "timing range upper-bound")
		if err != nil {
			return TimingDecl{}, err
		}
		timing.Last = last
		timing.RangeLast = nil
	}
	if timing.Last < timing.First {
		return TimingDecl{}, typeError(timing.Position,
			"empty timing range %d..%d requires Timing_Error support", timing.First, timing.Last)
	}
	if timing.Last-timing.First >= arch.MaxTimingRangeCardinality {
		return TimingDecl{}, typeError(timing.Position,
			"timing range %d..%d exceeds supported cardinality %d", timing.First, timing.Last, arch.MaxTimingRangeCardinality)
	}
	timing.Clock = clock
	return timing, nil
}

func evaluateClosedTimingExpression(
	expression ExpressionDecl,
	objects map[string]behaviorBinding,
	clock string,
	description string,
) (uint64, error) {
	compiled, err := compileBehaviorExpression(expression, objects, nil)
	if err != nil {
		return 0, err
	}
	if compiled.typeName != "Integer" {
		return 0, typeError(expression.Position,
			"%s expression has type %s, want a nonnegative Integer compatible with %s.Ticks",
			description, compiled.typeName, clock)
	}
	value, evaluatedType, err := arch.EvaluateConstant(compiled.value)
	if err != nil {
		return 0, typeError(expression.Position,
			"%s expression is not a closed deterministic constant: %v", description, err)
	}
	integer, ok := value.(int64)
	if !ok || evaluatedType != "Integer" || integer < 0 {
		return 0, typeError(expression.Position,
			"%s expression evaluates to %#v, want a nonnegative Integer compatible with %s.Ticks",
			description, value, clock)
	}
	return uint64(integer), nil
}

func behaviorRuleSemanticKey(rule BehaviorRuleDecl) string {
	var builder strings.Builder
	placeholderKeys := make([]string, 0, len(rule.Placeholders))
	for _, placeholder := range rule.Placeholders {
		placeholderKeys = append(placeholderKeys, patternPlaceholderSemanticKey(placeholder))
	}
	sort.Strings(placeholderKeys)
	for _, placeholder := range placeholderKeys {
		builder.WriteString(placeholder)
		builder.WriteByte(';')
	}
	builder.WriteString(behaviorPatternKey(rule.Trigger))
	if rule.Guard != nil {
		builder.WriteString("where")
		builder.WriteString(behaviorExpressionKey(*rule.Guard))
	}
	builder.WriteString(string(rule.Connector))
	for _, statement := range rule.Statements {
		builder.WriteString(behaviorStatementKey(statement))
	}
	return builder.String()
}

func behaviorPatternKey(source BehaviorPatternDecl) string {
	switch source.Kind {
	case BehaviorBasicPattern:
		var builder strings.Builder
		builder.WriteString("event:")
		if source.Event.Component != "" {
			if source.Event.ComponentPlaceholder {
				builder.WriteByte('?')
			}
			builder.WriteString(componentSelectionSemanticKey(source.Event.Component, source.Event.ComponentIndex))
			builder.WriteByte('.')
		}
		builder.WriteString(folded(source.Event.Name))
		if source.Event.Attribute != "" {
			builder.WriteByte('\'')
			builder.WriteString(folded(source.Event.Attribute))
		}
		builder.WriteByte('(')
		positional := make([]string, 0, len(source.Event.Arguments))
		named := make([]string, 0, len(source.Event.Arguments))
		for _, argument := range source.Event.Arguments {
			key := behaviorExpressionKey(argument.Actual)
			if argument.Formal == "" {
				positional = append(positional, key)
			} else {
				named = append(named, folded(argument.Formal)+" is "+key)
			}
		}
		sort.Strings(named)
		arguments := append(positional, named...)
		for index, argument := range arguments {
			if index != 0 {
				builder.WriteByte(',')
			}
			builder.WriteString(argument)
		}
		builder.WriteByte(')')
		return builder.String()
	case BehaviorBinaryPattern:
		if source.Left == nil || source.Right == nil {
			return "invalid-binary-pattern"
		}
		left, right := behaviorPatternKey(*source.Left), behaviorPatternKey(*source.Right)
		operator := strings.ToLower(source.Operator)
		switch operator {
		case "||", "~", "or", "and", "<=>":
			if right < left {
				left, right = right, left
			}
		}
		return "(" + left + operator + right + ")"
	case BehaviorIterationPattern:
		if source.Inner == nil {
			return "invalid-iteration-pattern"
		}
		if source.Iterator != "" {
			return fmt.Sprintf("iterate:%s:Integer:%d:%d:%s:{%s}", folded(source.Iterator),
				source.First, source.Last, strings.ToLower(source.Relation), behaviorPatternKey(*source.Inner))
		}
		return fmt.Sprintf("iterate:%d:%d:%s:{%s}", source.Minimum, source.Maximum,
			strings.ToLower(source.Relation), behaviorPatternKey(*source.Inner))
	default:
		return "invalid-pattern:" + string(source.Kind)
	}
}

func compileBehaviorPattern(
	declaration InterfaceDecl,
	source BehaviorPatternDecl,
	placeholders []ParameterDecl,
	bindings map[string]behaviorBinding,
	bound map[string]bool,
	typeElaborator *sourceTypeElaborator,
) (pattern.Pattern, error) {
	compiled, err := compileSourcePattern(source, bindings, bound, "behavior", func(event BehaviorEventDecl) (ActionDecl, string, error) {
		if event.ComponentPlaceholder {
			binding, exists := bindings[folded(event.Component)]
			if !exists {
				return ActionDecl{}, "", typeError(event.Position,
					"behavior module qualifier ?%s is not declared", event.Component)
			}
			if typeElaborator == nil {
				return ActionDecl{}, "", typeError(event.Position,
					"behavior module qualifier ?%s has no structural type environment", event.Component)
			}
			_, sourceInterface, resolveErr := typeElaborator.interfaceDeclaration(event.Position, binding.typeName)
			if resolveErr != nil {
				return ActionDecl{}, "", resolveErr
			}
			action, exists := findAction(sourceInterface, event.Name)
			if !exists {
				return ActionDecl{}, "", typeError(event.Position,
					"behavior module qualifier ?%s of type %s has no action %q",
					event.Component, sourceInterface.Name, event.Name)
			}
			if action.Mode != ActionOut {
				return ActionDecl{}, "", typeError(event.Position,
					"behavior module-qualified action ?%s.%s is not an out action broadcast",
					event.Component, action.Name)
			}
			return action, "", nil
		}
		if event.Component != "" {
			return ActionDecl{}, "", typeError(event.Position, "behavior trigger action %q cannot be component-qualified", event.Component+"."+event.Name)
		}
		action, exists, err := findPatternAction(declaration, event)
		if err != nil {
			return ActionDecl{}, "", typeError(event.Position, "behavior: %v", err)
		}
		if !exists {
			return ActionDecl{}, "", typeError(event.Position, "behavior trigger action %q is not declared", event.Name)
		}
		return action, "", nil
	})
	if err != nil {
		return nil, err
	}
	return compileUniversalQualifications(compiled, placeholders, bound, "behavior")
}

type sourcePatternActionResolver func(BehaviorEventDecl) (ActionDecl, string, error)

func compileSourcePattern(
	source BehaviorPatternDecl,
	bindings map[string]behaviorBinding,
	bound map[string]bool,
	context string,
	resolve sourcePatternActionResolver,
) (pattern.Pattern, error) {
	return compileSourcePatternWithIterators(source, bindings, bound, nil, context, resolve)
}

func compileSourcePatternWithIterators(
	source BehaviorPatternDecl,
	bindings map[string]behaviorBinding,
	bound map[string]bool,
	iterators map[string]behaviorBinding,
	context string,
	resolve sourcePatternActionResolver,
) (pattern.Pattern, error) {
	switch source.Kind {
	case BehaviorBasicPattern:
		action, component, err := resolve(source.Event)
		if err != nil {
			return nil, err
		}
		result := pattern.MatchEvent(action.Name)
		if source.Event.ComponentPlaceholder {
			binding, exists := bindings[folded(source.Event.Component)]
			if !exists || !binding.placeholder || binding.universal {
				return nil, typeError(source.Event.Position,
					"%s module qualifier ?%s is not a declared existential placeholder",
					context, source.Event.Component)
			}
			if _, predefined := predefinedTypes[folded(binding.typeName)]; predefined {
				return nil, typeError(source.Event.Position,
					"%s module qualifier ?%s has non-structural type %s",
					context, source.Event.Component, binding.typeName)
			}
			result.BindModuleSource(pattern.Var(binding.name).WithType(binding.typeName))
			bound[folded(source.Event.Component)] = true
		} else if component != "" {
			result.WhereSource(component)
		}
		if len(source.Event.Arguments) == 0 {
			return result, nil
		}
		positionalIndex := 0
		named := false
		associated := make(map[string]bool, len(source.Event.Arguments))
		for _, association := range source.Event.Arguments {
			var parameter ParameterDecl
			if association.Formal == "" {
				if named {
					return nil, typeError(association.Position, "%s positional basic-pattern associations must precede named associations", context)
				}
				if positionalIndex >= len(action.Parameters) {
					return nil, typeError(association.Position, "%s pattern action %q has %d parameters but supplies positional association %d", context, action.Name, len(action.Parameters), positionalIndex+1)
				}
				parameter = action.Parameters[positionalIndex]
				positionalIndex++
			} else {
				named = true
				var exists bool
				parameter, exists = findParameter(action.Parameters, association.Formal)
				if !exists {
					return nil, typeError(association.Position, "%s pattern action %q has no parameter named %q", context, action.Name, association.Formal)
				}
			}
			parameterKey := folded(parameter.Name)
			if associated[parameterKey] {
				return nil, typeError(association.Position, "%s pattern parameter %q is associated more than once", context, parameter.Name)
			}
			associated[parameterKey] = true
			if err := applyPatternParameterAssociation(result, association, parameter, bindings, bound, iterators, context); err != nil {
				return nil, err
			}
		}
		return result, nil
	case BehaviorBinaryPattern:
		if source.Left == nil || source.Right == nil {
			return nil, typeError(source.Position, "binary behavior pattern has a missing operand")
		}
		left, err := compileSourcePatternWithIterators(*source.Left, bindings, bound, iterators, context, resolve)
		if err != nil {
			return nil, err
		}
		right, err := compileSourcePatternWithIterators(*source.Right, bindings, bound, iterators, context, resolve)
		if err != nil {
			return nil, err
		}
		switch strings.ToLower(source.Operator) {
		case "->":
			return pattern.Seq(left, right), nil
		case "|>":
			return pattern.ImmSeq(left, right), nil
		case "||":
			return pattern.Independent(left, right), nil
		case "~":
			return pattern.Disjoint(left, right), nil
		case "or":
			return pattern.Or(left, right), nil
		case "and":
			return pattern.Union(left, right), nil
		case "<=>":
			return pattern.And(left, right), nil
		default:
			return nil, typeError(source.Position, "unsupported behavior pattern operator %q", source.Operator)
		}
	case BehaviorIterationPattern:
		if source.Inner == nil {
			return nil, typeError(source.Position, "behavior iteration pattern has a missing operand")
		}
		relation, err := compileSourcePatternRelation(source.Position, source.Relation, "behavior iteration")
		if err != nil {
			return nil, err
		}
		if source.Iterator != "" {
			if _, valid := sourceNamedRangeCardinality(source.First, source.Last); !valid {
				return nil, typeError(source.Position,
					"named Integer iterator %s range %d..%d must contain at most %d values",
					source.Iterator, source.First, source.Last, pattern.MaxNamedRangeIterationCardinality)
			}
			key := folded(source.Iterator)
			if _, exists := bindings[key]; exists {
				return nil, typeError(source.Position, "named iterator %s conflicts with a placeholder in the enclosing qualification", source.Iterator)
			}
			if _, exists := iterators[key]; exists {
				return nil, typeError(source.Position, "named iterator %s conflicts with an enclosing iterator", source.Iterator)
			}
			local := make(map[string]behaviorBinding, len(iterators)+1)
			for name, binding := range iterators {
				local[name] = binding
			}
			local[key] = behaviorBinding{name: source.Iterator, typeName: "Integer", placeholder: true}
			inner, err := compileSourcePatternWithIterators(*source.Inner, bindings, bound, local, context, resolve)
			if err != nil {
				return nil, err
			}
			return pattern.IterateIntegerRange(
				pattern.Var(source.Iterator).WithType("Integer"), source.First, source.Last, relation, inner,
			), nil
		}
		if source.Minimum < 0 || (source.Maximum >= 0 && source.Maximum < source.Minimum) {
			return nil, typeError(source.Position, "behavior iteration pattern has invalid cardinality %d..%d", source.Minimum, source.Maximum)
		}
		if source.Maximum > int(pattern.MaxNamedRangeIterationCardinality) {
			return nil, typeError(source.Position, "finite iteration cardinality %d exceeds the deterministic bound of %d",
				source.Maximum, pattern.MaxNamedRangeIterationCardinality)
		}
		inner, err := compileSourcePatternWithIterators(*source.Inner, bindings, bound, iterators, context, resolve)
		if err != nil {
			return nil, err
		}
		if source.Maximum < 0 && source.Minimum == 0 {
			return pattern.IterateZeroOrMore(inner, relation), nil
		}
		if source.Maximum < 0 && source.Minimum == 1 {
			return pattern.IterateOneOrMore(inner, relation), nil
		}
		return pattern.IterateRange(inner, relation, source.Minimum, source.Maximum), nil
	default:
		return nil, typeError(source.Position, "unsupported behavior pattern kind %q", source.Kind)
	}
}

func sourceNamedRangeCardinality(first, last int64) (uint64, bool) {
	if last < first {
		return 0, true
	}
	difference := uint64(last) - uint64(first)
	if difference >= pattern.MaxNamedRangeIterationCardinality {
		return 0, false
	}
	return difference + 1, true
}

func compileSourcePatternRelation(position Position, source, context string) (pattern.IterationRelation, error) {
	switch strings.ToLower(source) {
	case "->":
		return pattern.RelationFollows, nil
	case "|>":
		return pattern.RelationImmediateFollows, nil
	case "||":
		return pattern.RelationIndependent, nil
	case "~":
		return pattern.RelationDisjoint, nil
	case "and":
		return pattern.RelationUnion, nil
	case "or":
		return pattern.RelationOr, nil
	case "<=>":
		return pattern.RelationAnd, nil
	default:
		return "", typeError(position, "%s has unsupported relation %q", context, source)
	}
}

func compileUniversalQualifications(
	compiled pattern.Pattern,
	placeholders []ParameterDecl,
	bound map[string]bool,
	context string,
) (pattern.Pattern, error) {
	universalCount := 0
	for _, placeholder := range placeholders {
		if placeholder.Qualification != PlaceholderUniversal {
			continue
		}
		universalCount++
		if universalCount > 1 {
			return nil, typeError(placeholder.Position,
				"%s currently supports one universal placeholder per qualification; multiple-universal nesting order remains source-ambiguous", context)
		}
		if !bound[folded(placeholder.Name)] {
			return nil, typeError(placeholder.Position, "%s universal placeholder !%s does not occur in the pattern", context, placeholder.Name)
		}
		if canonicalPredefinedType(placeholder.Type) != "Integer" {
			return nil, typeError(placeholder.Position,
				"%s universal placeholder !%s requires the supported Integer range domain, got %q", context, placeholder.Name, placeholder.Type)
		}
		if placeholder.RangeLast < placeholder.RangeFirst ||
			uint64(placeholder.RangeLast)-uint64(placeholder.RangeFirst) >= pattern.MaxUniversalRangeCardinality {
			return nil, typeError(placeholder.Position,
				"%s universal range %d..%d must be nonempty and contain at most %d values",
				context, placeholder.RangeFirst, placeholder.RangeLast, pattern.MaxUniversalRangeCardinality)
		}
		relation, err := compileSourcePatternRelation(placeholder.Position, placeholder.Relation, context+" universal qualification")
		if err != nil {
			return nil, err
		}
		compiled = pattern.ForAllIntegerRange(
			pattern.Var(placeholder.Name).WithType("Integer"),
			placeholder.RangeFirst, placeholder.RangeLast, relation, compiled,
		)
	}
	return compiled, nil
}

func compileConstraintPattern(
	source ConstraintComponentDecl,
	bindings map[string]behaviorBinding,
	states map[string]behaviorBinding,
	stateOwner string,
	bound map[string]bool,
	context string,
	resolve sourcePatternActionResolver,
) (pattern.Pattern, error) {
	compiled, err := compileSourcePattern(source.Pattern, bindings, bound, context, resolve)
	if err != nil {
		return nil, err
	}
	compiled, err = compileUniversalQualifications(compiled, source.Placeholders, bound, context)
	if err != nil || source.Guard == nil {
		return compiled, err
	}
	condition, typeName, err := compilePatternCondition(*source.Guard, bindings, states, stateOwner, context)
	if err != nil {
		return nil, err
	}
	if typeName != "Boolean" {
		return nil, typeError(source.Guard.Position, "%s pattern guard has type %s, want Boolean", context, typeName)
	}
	return pattern.Where(compiled, condition), nil
}

func compileConnectionGuard(
	trigger pattern.Pattern,
	connection ConnectionDecl,
	bindings map[string]behaviorBinding,
	context string,
) (pattern.Pattern, error) {
	if connection.Guard == nil {
		return trigger, nil
	}
	condition, typeName, err := compilePatternCondition(
		*connection.Guard, bindings, nil, "", context,
	)
	if err != nil {
		return nil, err
	}
	if typeName != "Boolean" {
		return nil, typeError(connection.Guard.Position,
			"%s pattern guard has type %s, want Boolean", context, typeName)
	}
	return pattern.Where(trigger, condition), nil
}

func compilePatternCondition(
	expression ExpressionDecl,
	bindings map[string]behaviorBinding,
	states map[string]behaviorBinding,
	stateOwner string,
	context string,
) (pattern.Condition, string, error) {
	switch expression.Kind {
	case ExpressionPlaceholder:
		binding, ok := bindings[folded(expression.Name)]
		if !ok || !binding.placeholder {
			return pattern.Condition{}, "", typeError(expression.Position, "%s pattern guard placeholder ?%s is not declared", context, expression.Name)
		}
		if binding.universal {
			return pattern.Condition{}, "", typeError(expression.Position,
				"%s universal placeholder !%s is a qualification domain, not one scalar guard binding", context, binding.name)
		}
		return pattern.BindingCondition(pattern.Var(binding.name).WithType(binding.typeName)), patternConditionType(binding.typeName), nil
	case ExpressionUniversal:
		return pattern.Condition{}, "", typeError(expression.Position,
			"%s universal placeholder !%s is substituted inside its qualified pattern and is unavailable to a whole-match guard", context, expression.Name)
	case ExpressionName:
		if binding, ok := bindings[folded(expression.Name)]; ok && binding.placeholder {
			return pattern.Condition{}, "", typeError(expression.Position, "%s pattern guard placeholder %q must be referenced with '?'", context, expression.Name)
		}
		return pattern.Condition{}, "", typeError(expression.Position, "%s pattern guard name %q is not declared in the closed guard subset", context, expression.Name)
	case ExpressionState:
		binding, ok := states[folded(expression.Name)]
		if !ok || !binding.state || stateOwner == "" {
			return pattern.Condition{}, "", typeError(expression.Position,
				"%s pattern guard state dereference $%s is not declared in this constraint scope",
				context, expression.Name)
		}
		return pattern.StateCondition(stateOwner+"\x00"+binding.name, binding.typeName), patternConditionType(binding.typeName), nil
	case ExpressionInteger:
		return pattern.LiteralCondition(expression.Integer), "Integer", nil
	case ExpressionFloat:
		return pattern.LiteralCondition(expression.Float), "Float", nil
	case ExpressionCharacter:
		return pattern.LiteralCondition(gorapide.RapideCharacterFromCode(expression.Character)), "Character", nil
	case ExpressionString:
		return pattern.LiteralCondition(sourceStringLiteralValue(expression)), "String", nil
	case ExpressionUnit:
		return pattern.LiteralCondition(gorapide.RapideUnit()), "Triv", nil
	case ExpressionBoolean:
		return pattern.LiteralCondition(expression.Boolean), "Boolean", nil
	case ExpressionQualified:
		if expression.Left == nil {
			return pattern.Condition{}, "", typeError(expression.Position, "%s qualified expression has no operand", context)
		}
		target := canonicalPredefinedType(expression.Name)
		if !sourceClosedScalarType(target) {
			return pattern.Condition{}, "", typeError(expression.Position,
				"%s qualified-expression type %q is outside the direct predefined-scalar subset", context, expression.Name)
		}
		if target == "Natural" || target == "Positive" {
			return pattern.Condition{}, "", typeError(expression.Position,
				"%s qualified-expression target %s requires constrained source-type preservation outside the current pattern-guard algebra", context, target)
		}
		operand, operandType, err := compilePatternCondition(*expression.Left, bindings, states, stateOwner, context)
		if err != nil {
			return pattern.Condition{}, "", err
		}
		if !sourcePredefinedTypeAssignable(operandType, target) {
			return pattern.Condition{}, "", typeError(expression.Position,
				"%s qualified-expression operand has type %s, which is not a subtype of %s", context, operandType, target)
		}
		return pattern.QualifiedCondition(target, operand), patternConditionType(target), nil
	case ExpressionCall:
		if expression.Left != nil && len(expression.Arguments) == 1 &&
			(expression.Name == "<" || expression.Name == "<=" || expression.Name == ">" || expression.Name == ">=") {
			left, leftType, err := compilePatternCondition(*expression.Left, bindings, states, stateOwner, context)
			if err != nil {
				return pattern.Condition{}, "", err
			}
			right, rightType, err := compilePatternCondition(expression.Arguments[0], bindings, states, stateOwner, context)
			if err != nil {
				return pattern.Condition{}, "", err
			}
			compatible := leftType == rightType &&
				(leftType == "Integer" || leftType == "Float" || leftType == "Character")
			if !compatible {
				return pattern.Condition{}, "", typeError(expression.Position,
					"%s Order.%s is not defined for %s and %s", context, expression.Name, leftType, rightType)
			}
			operators := map[string]pattern.ConditionOperator{
				"<": pattern.ConditionLess, "<=": pattern.ConditionLessOrEqual,
				">": pattern.ConditionGreater, ">=": pattern.ConditionGreaterEqual,
			}
			return pattern.BinaryCondition(operators[expression.Name], left, right), "Boolean", nil
		}
		if expression.Left != nil && len(expression.Arguments) == 1 &&
			(expression.Name == "=" || expression.Name == "/=") {
			left, leftType, err := compilePatternCondition(*expression.Left, bindings, states, stateOwner, context)
			if err != nil {
				return pattern.Condition{}, "", err
			}
			right, rightType, err := compilePatternCondition(expression.Arguments[0], bindings, states, stateOwner, context)
			if err != nil {
				return pattern.Condition{}, "", err
			}
			if leftType != rightType {
				return pattern.Condition{}, "", typeError(expression.Position,
					"%s Equality.%s requires equal operand types, got %s and %s", context, expression.Name, leftType, rightType)
			}
			operator := pattern.ConditionEqual
			if expression.Name == "/=" {
				operator = pattern.ConditionNotEqual
			}
			return pattern.BinaryCondition(operator, left, right), "Boolean", nil
		}
		if expression.Left != nil && keyword(expression.Name, "Not") && len(expression.Arguments) == 0 {
			receiver, receiverType, err := compilePatternCondition(*expression.Left, bindings, states, stateOwner, context)
			if err != nil {
				return pattern.Condition{}, "", err
			}
			if receiverType != "Boolean" {
				return pattern.Condition{}, "", typeError(expression.Position,
					"%s Boolean.Not receiver has type %s, want Boolean", context, receiverType)
			}
			return pattern.UnaryCondition(pattern.ConditionNot, receiver), "Boolean", nil
		}
		if expression.Left != nil && len(expression.Arguments) == 1 &&
			(keyword(expression.Name, "And") || keyword(expression.Name, "Or")) {
			left, leftType, err := compilePatternCondition(*expression.Left, bindings, states, stateOwner, context)
			if err != nil {
				return pattern.Condition{}, "", err
			}
			right, rightType, err := compilePatternCondition(expression.Arguments[0], bindings, states, stateOwner, context)
			if err != nil {
				return pattern.Condition{}, "", err
			}
			if leftType != "Boolean" || rightType != "Boolean" {
				return pattern.Condition{}, "", typeError(expression.Position,
					"%s Boolean.%s requires Boolean and Boolean, got %s and %s", context, expression.Name, leftType, rightType)
			}
			operator := pattern.ConditionAnd
			if keyword(expression.Name, "Or") {
				operator = pattern.ConditionOr
			}
			return pattern.BinaryCondition(operator, left, right), "Boolean", nil
		}
		if expression.Left != nil && keyword(expression.Name, "Xor") && len(expression.Arguments) == 1 {
			left, leftType, err := compilePatternCondition(*expression.Left, bindings, states, stateOwner, context)
			if err != nil {
				return pattern.Condition{}, "", err
			}
			right, rightType, err := compilePatternCondition(expression.Arguments[0], bindings, states, stateOwner, context)
			if err != nil {
				return pattern.Condition{}, "", err
			}
			if leftType != "Boolean" || rightType != "Boolean" {
				return pattern.Condition{}, "", typeError(expression.Position,
					"%s Boolean.Xor requires Boolean and Boolean, got %s and %s", context, leftType, rightType)
			}
			return pattern.BinaryCondition(pattern.ConditionXor, left, right), "Boolean", nil
		}
		if expression.Left != nil && (expression.Name == "+" || expression.Name == "-") && len(expression.Arguments) == 0 {
			receiver, receiverType, err := compilePatternCondition(*expression.Left, bindings, states, stateOwner, context)
			if err != nil {
				return pattern.Condition{}, "", err
			}
			if receiverType != "Integer" && receiverType != "Float" {
				return pattern.Condition{}, "", typeError(expression.Position,
					"%s Numeric.%s receiver has type %s, want Integer or Float", context, expression.Name, receiverType)
			}
			operator := pattern.ConditionPositive
			if expression.Name == "-" {
				operator = pattern.ConditionNegate
			}
			return pattern.UnaryCondition(operator, receiver), receiverType, nil
		}
		if expression.Left != nil && len(expression.Arguments) == 1 &&
			(expression.Name == "+" || expression.Name == "-" || expression.Name == "*") {
			left, leftType, err := compilePatternCondition(*expression.Left, bindings, states, stateOwner, context)
			if err != nil {
				return pattern.Condition{}, "", err
			}
			right, rightType, err := compilePatternCondition(expression.Arguments[0], bindings, states, stateOwner, context)
			if err != nil {
				return pattern.Condition{}, "", err
			}
			compatible := leftType == rightType && (leftType == "Integer" || leftType == "Float")
			if !compatible {
				return pattern.Condition{}, "", typeError(expression.Position,
					"%s Numeric.%s is not defined for %s and %s", context, expression.Name, leftType, rightType)
			}
			operators := map[string]pattern.ConditionOperator{
				"+": pattern.ConditionAdd, "-": pattern.ConditionSubtract, "*": pattern.ConditionMultiply,
			}
			return pattern.BinaryCondition(operators[expression.Name], left, right), leftType, nil
		}
		if expression.Left != nil && keyword(expression.Name, "Float") && len(expression.Arguments) == 0 {
			receiver, receiverType, err := compilePatternCondition(*expression.Left, bindings, states, stateOwner, context)
			if err != nil {
				return pattern.Condition{}, "", err
			}
			if receiverType != "Integer" {
				return pattern.Condition{}, "", typeError(expression.Position,
					"%s Integer.Float receiver has type %s, want Integer", context, receiverType)
			}
			return pattern.UnaryCondition(pattern.ConditionIntegerFloat, receiver), "Float", nil
		}
		if expression.Left != nil && len(expression.Arguments) == 0 &&
			(keyword(expression.Name, "Pred") || keyword(expression.Name, "Succ")) {
			receiver, receiverType, err := compilePatternCondition(*expression.Left, bindings, states, stateOwner, context)
			if err != nil {
				return pattern.Condition{}, "", err
			}
			if receiverType != "Integer" {
				return pattern.Condition{}, "", typeError(expression.Position,
					"%s Integer.%s receiver has type %s, want Integer", context, expression.Name, receiverType)
			}
			operator := pattern.ConditionIntegerPred
			if keyword(expression.Name, "Succ") {
				operator = pattern.ConditionIntegerSucc
			}
			return pattern.UnaryCondition(operator, receiver), "Integer", nil
		}
		if expression.Left != nil && keyword(expression.Name, "Abs") && len(expression.Arguments) == 0 {
			receiver, receiverType, err := compilePatternCondition(*expression.Left, bindings, states, stateOwner, context)
			if err != nil {
				return pattern.Condition{}, "", err
			}
			if receiverType != "Integer" && receiverType != "Float" {
				return pattern.Condition{}, "", typeError(expression.Position,
					"%s Numeric.Abs receiver has type %s, want Integer or Float", context, receiverType)
			}
			return pattern.UnaryCondition(pattern.ConditionAbs, receiver), receiverType, nil
		}
		if expression.Left != nil && keyword(expression.Name, "Floor") && len(expression.Arguments) == 0 {
			receiver, receiverType, err := compilePatternCondition(*expression.Left, bindings, states, stateOwner, context)
			if err != nil {
				return pattern.Condition{}, "", err
			}
			if receiverType != "Float" {
				return pattern.Condition{}, "", typeError(expression.Position,
					"%s Float.Floor receiver has type %s, want Float", context, receiverType)
			}
			return pattern.UnaryCondition(pattern.ConditionFloatFloor, receiver), "Integer", nil
		}
		if expression.Left != nil && keyword(expression.Name, "Slice") && len(expression.Arguments) == 2 {
			receiver, receiverType, err := compilePatternCondition(*expression.Left, bindings, states, stateOwner, context)
			if err != nil {
				return pattern.Condition{}, "", err
			}
			if receiverType != "String" {
				return pattern.Condition{}, "", typeError(expression.Position,
					"%s String Slice receiver has type %s, want String", context, receiverType)
			}
			bounds := make([]pattern.Condition, 2)
			for index := range expression.Arguments {
				bound, boundType, err := compilePatternCondition(expression.Arguments[index], bindings, states, stateOwner, context)
				if err != nil {
					return pattern.Condition{}, "", err
				}
				if boundType != "Integer" {
					return pattern.Condition{}, "", typeError(expression.Position,
						"%s String Slice bound %d has type %s, want Integer", context, index+1, boundType)
				}
				bounds[index] = bound
			}
			return pattern.TernaryCondition(pattern.ConditionStringSlice, receiver, bounds[0], bounds[1]), "String", nil
		}
		if expression.Left != nil && keyword(expression.Name, "[]") && len(expression.Arguments) == 1 {
			receiver, receiverType, err := compilePatternCondition(*expression.Left, bindings, states, stateOwner, context)
			if err != nil {
				return pattern.Condition{}, "", err
			}
			if receiverType != "String" {
				return pattern.Condition{}, "", typeError(expression.Position,
					"%s String index receiver has type %s, want String", context, receiverType)
			}
			position, positionType, err := compilePatternCondition(expression.Arguments[0], bindings, states, stateOwner, context)
			if err != nil {
				return pattern.Condition{}, "", err
			}
			if positionType != "Integer" {
				return pattern.Condition{}, "", typeError(expression.Position,
					"%s String index has type %s, want Integer", context, positionType)
			}
			return pattern.BinaryCondition(pattern.ConditionStringIndex, receiver, position), "Character", nil
		}
		if expression.Left != nil && len(expression.Arguments) == 1 &&
			(keyword(expression.Name, "Append") || keyword(expression.Name, "Prepend")) {
			receiver, receiverType, err := compilePatternCondition(*expression.Left, bindings, states, stateOwner, context)
			if err != nil {
				return pattern.Condition{}, "", err
			}
			if receiverType != "String" {
				return pattern.Condition{}, "", typeError(expression.Position,
					"%s String.%s receiver has type %s, want String", context, expression.Name, receiverType)
			}
			argument, argumentType, err := compilePatternCondition(expression.Arguments[0], bindings, states, stateOwner, context)
			if err != nil {
				return pattern.Condition{}, "", err
			}
			if argumentType != "Character" {
				return pattern.Condition{}, "", typeError(expression.Position,
					"%s String.%s argument has type %s, want Character", context, expression.Name, argumentType)
			}
			if keyword(expression.Name, "Append") {
				return pattern.BinaryCondition(pattern.ConditionStringAppend, receiver, argument), "String", nil
			}
			return pattern.BinaryCondition(pattern.ConditionStringPrepend, receiver, argument), "String", nil
		}
		if expression.Left != nil && len(expression.Arguments) == 0 &&
			(keyword(expression.Name, "Is_Null") || keyword(expression.Name, "Length")) {
			receiver, receiverType, err := compilePatternCondition(*expression.Left, bindings, states, stateOwner, context)
			if err != nil {
				return pattern.Condition{}, "", err
			}
			if receiverType != "String" {
				return pattern.Condition{}, "", typeError(expression.Position,
					"%s String.%s receiver has type %s, want String", context, expression.Name, receiverType)
			}
			if keyword(expression.Name, "Is_Null") {
				return pattern.UnaryCondition(pattern.ConditionStringIsNull, receiver), "Boolean", nil
			}
			return pattern.UnaryCondition(pattern.ConditionStringLength, receiver), "Integer", nil
		}
		if expression.Left != nil && keyword(expression.Name, "Code") && len(expression.Arguments) == 0 {
			receiver, receiverType, err := compilePatternCondition(*expression.Left, bindings, states, stateOwner, context)
			if err != nil {
				return pattern.Condition{}, "", err
			}
			if receiverType != "Character" {
				return pattern.Condition{}, "", typeError(expression.Position,
					"%s Character.Code receiver has type %s, want Character", context, receiverType)
			}
			return pattern.UnaryCondition(pattern.ConditionCharacterCode, receiver), "Integer", nil
		}
		if expression.Left == nil && keyword(expression.Name, "Code_To_Char") && len(expression.Arguments) == 1 {
			argument, argumentType, err := compilePatternCondition(expression.Arguments[0], bindings, states, stateOwner, context)
			if err != nil {
				return pattern.Condition{}, "", err
			}
			if argumentType != "Integer" {
				return pattern.Condition{}, "", typeError(expression.Position,
					"%s Code_To_Char argument has type %s, want Integer", context, argumentType)
			}
			return pattern.UnaryCondition(pattern.ConditionCodeToCharacter, argument), "Character", nil
		}
		return pattern.Condition{}, "", typeError(expression.Position,
			"%s unsupported closed expression call %s", context, behaviorExpressionKey(expression))
	case ExpressionConditional:
		if expression.Left == nil || expression.Right == nil || len(expression.Arguments) != 1 {
			return pattern.Condition{}, "", typeError(expression.Position, "%s if-expression is incomplete", context)
		}
		condition, conditionType, err := compilePatternCondition(*expression.Left, bindings, states, stateOwner, context)
		if err != nil {
			return pattern.Condition{}, "", err
		}
		if conditionType != "Boolean" {
			return pattern.Condition{}, "", typeError(expression.Position,
				"%s if-expression condition has type %s, want Boolean", context, conditionType)
		}
		thenValue, thenType, err := compilePatternCondition(*expression.Right, bindings, states, stateOwner, context)
		if err != nil {
			return pattern.Condition{}, "", err
		}
		elseValue, elseType, err := compilePatternCondition(expression.Arguments[0], bindings, states, stateOwner, context)
		if err != nil {
			return pattern.Condition{}, "", err
		}
		if thenType != elseType {
			return pattern.Condition{}, "", typeError(expression.Position,
				"%s if-expression branches have incompatible types %s and %s", context, thenType, elseType)
		}
		return pattern.TernaryCondition(pattern.ConditionIf, condition, thenValue, elseValue), thenType, nil
	case ExpressionUnary:
		if expression.Left == nil {
			return pattern.Condition{}, "", typeError(expression.Position, "%s unary pattern guard expression has a missing operand", context)
		}
		operand, operandType, err := compilePatternCondition(*expression.Left, bindings, states, stateOwner, context)
		if err != nil {
			return pattern.Condition{}, "", err
		}
		switch strings.ToLower(expression.Operator) {
		case "+":
			if operandType != "Integer" && operandType != "Float" {
				return pattern.Condition{}, "", typeError(expression.Position, "%s pattern guard unary '+' requires Integer or Float, got %s", context, operandType)
			}
			return pattern.UnaryCondition(pattern.ConditionPositive, operand), operandType, nil
		case "-":
			if operandType != "Integer" && operandType != "Float" {
				return pattern.Condition{}, "", typeError(expression.Position, "%s pattern guard unary '-' requires Integer or Float, got %s", context, operandType)
			}
			return pattern.UnaryCondition(pattern.ConditionNegate, operand), operandType, nil
		case "abs":
			if operandType != "Integer" && operandType != "Float" {
				return pattern.Condition{}, "", typeError(expression.Position, "%s pattern guard 'abs' requires Integer or Float, got %s", context, operandType)
			}
			return pattern.UnaryCondition(pattern.ConditionAbs, operand), operandType, nil
		case "not":
			if operandType != "Boolean" {
				return pattern.Condition{}, "", typeError(expression.Position, "%s pattern guard 'not' requires Boolean, got %s", context, operandType)
			}
			return pattern.UnaryCondition(pattern.ConditionNot, operand), "Boolean", nil
		default:
			return pattern.Condition{}, "", typeError(expression.Position, "%s pattern guard has unsupported unary operator %q", context, expression.Operator)
		}
	case ExpressionBinary:
		if expression.Left == nil || expression.Right == nil {
			return pattern.Condition{}, "", typeError(expression.Position, "%s binary pattern guard expression has a missing operand", context)
		}
		left, leftType, err := compilePatternCondition(*expression.Left, bindings, states, stateOwner, context)
		if err != nil {
			return pattern.Condition{}, "", err
		}
		right, rightType, err := compilePatternCondition(*expression.Right, bindings, states, stateOwner, context)
		if err != nil {
			return pattern.Condition{}, "", err
		}
		operator := strings.ToLower(expression.Operator)
		var conditionOperator pattern.ConditionOperator
		resultType := ""
		switch operator {
		case "&":
			switch {
			case leftType == "String" && rightType == "String":
				conditionOperator = pattern.ConditionStringConcatenate
				resultType = "String"
			case leftType == "String" && rightType == "Character":
				conditionOperator = pattern.ConditionStringAppend
				resultType = "String"
				return pattern.BinaryCondition(conditionOperator, left, right), resultType, nil
			case leftType == "Character" && rightType == "String":
				conditionOperator = pattern.ConditionStringPrepend
				resultType = "String"
				return pattern.BinaryCondition(conditionOperator, right, left), resultType, nil
			}
		case "+", "-", "*", "/":
			if leftType == "Integer" && rightType == "Integer" {
				resultType = "Integer"
			} else if leftType == "Float" && rightType == "Float" {
				resultType = "Float"
			}
			switch operator {
			case "+":
				conditionOperator = pattern.ConditionAdd
			case "-":
				conditionOperator = pattern.ConditionSubtract
			case "*":
				conditionOperator = pattern.ConditionMultiply
			case "/":
				conditionOperator = pattern.ConditionDivide
			}
		case "=", "/=":
			if leftType == rightType {
				resultType = "Boolean"
			}
			if operator == "=" {
				conditionOperator = pattern.ConditionEqual
			} else {
				conditionOperator = pattern.ConditionNotEqual
			}
		case "<", "<=", ">", ">=":
			if leftType == "Integer" && rightType == "Integer" ||
				leftType == "Float" && rightType == "Float" ||
				leftType == "Character" && rightType == "Character" {
				resultType = "Boolean"
			}
			switch operator {
			case "<":
				conditionOperator = pattern.ConditionLess
			case "<=":
				conditionOperator = pattern.ConditionLessOrEqual
			case ">":
				conditionOperator = pattern.ConditionGreater
			case ">=":
				conditionOperator = pattern.ConditionGreaterEqual
			}
		case "and", "or", "xor", "nand", "nor", "andthen", "orelse":
			if leftType == "Boolean" && rightType == "Boolean" {
				resultType = "Boolean"
			}
			if operator == "and" {
				conditionOperator = pattern.ConditionAnd
			} else if operator == "or" {
				conditionOperator = pattern.ConditionOr
			} else if operator == "xor" {
				conditionOperator = pattern.ConditionXor
			} else if operator == "nand" {
				conditionOperator = pattern.ConditionNand
			} else if operator == "nor" {
				conditionOperator = pattern.ConditionNor
			} else if operator == "andthen" {
				conditionOperator = pattern.ConditionAndThen
			} else {
				conditionOperator = pattern.ConditionOrElse
			}
		default:
			return pattern.Condition{}, "", typeError(expression.Position, "%s pattern guard has unsupported binary operator %q", context, expression.Operator)
		}
		if resultType == "" {
			return pattern.Condition{}, "", typeError(expression.Position,
				"%s pattern guard operator %q is not defined for %s and %s", context, expression.Operator, leftType, rightType)
		}
		return pattern.BinaryCondition(conditionOperator, left, right), resultType, nil
	default:
		return pattern.Condition{}, "", typeError(expression.Position, "%s pattern guard has unsupported expression kind %q", context, expression.Kind)
	}
}

func patternConditionType(typeName string) string {
	switch canonicalPredefinedType(typeName) {
	case "Natural", "Positive":
		return "Integer"
	default:
		return canonicalPredefinedType(typeName)
	}
}

func findParameter(parameters []ParameterDecl, name string) (ParameterDecl, bool) {
	for _, parameter := range parameters {
		if keyword(parameter.Name, name) {
			return parameter, true
		}
	}
	return ParameterDecl{}, false
}

func applyPatternParameterAssociation(
	result *pattern.BasicPattern,
	association PatternParameterAssociationDecl,
	parameter ParameterDecl,
	bindings map[string]behaviorBinding,
	bound map[string]bool,
	iterators map[string]behaviorBinding,
	context string,
) error {
	expected, expectedPredefined := predefinedTypes[folded(parameter.Type)]
	if !expectedPredefined {
		expected = parameter.Type
	}
	if association.Actual.Kind == ExpressionName {
		if iterator, ok := iterators[folded(association.Actual.Name)]; ok {
			if !sourcePredefinedTypeAssignable(expected, iterator.typeName) {
				return typeError(association.Actual.Position,
					"%s named iterator %s has type %s but action parameter %s has type %s",
					context, iterator.name, iterator.typeName, parameter.Name, expected)
			}
			result.BindParam(parameter.Name, pattern.Var(iterator.name).WithType(iterator.typeName))
			return nil
		}
	}
	if association.Actual.Kind == ExpressionPlaceholder || association.Actual.Kind == ExpressionUniversal {
		name := association.Actual.Name
		prefix := "?"
		if association.Actual.Kind == ExpressionUniversal {
			prefix = "!"
		}
		binding, ok := bindings[folded(name)]
		if !ok {
			return typeError(association.Actual.Position, "%s pattern placeholder %s%s is not declared", context, prefix, name)
		}
		if (association.Actual.Kind == ExpressionUniversal) != binding.universal {
			expected, actual := "?", "!"
			if binding.universal {
				expected, actual = "!", "?"
			}
			return typeError(association.Actual.Position,
				"%s placeholder %s%s must be referenced as %s%s", context, actual, name, expected, binding.name)
		}
		compatible := false
		if expectedPredefined {
			if _, bindingPredefined := predefinedTypes[folded(binding.typeName)]; !bindingPredefined {
				return typeError(association.Actual.Position,
					"%s placeholder %s%s has unsupported type %q", context, prefix, name, binding.typeName)
			}
			compatible = sourcePredefinedTypeAssignable(expected, binding.typeName)
		} else {
			compatible = keyword(parameter.Type, binding.typeName)
		}
		if !compatible {
			return typeError(association.Actual.Position, "%s placeholder %s%s has type %s but action parameter %s has type %s", context,
				prefix, name, binding.typeName, parameter.Name, expected)
		}
		placeholder := pattern.Var(binding.name)
		if expectedPredefined {
			placeholder.WithType(binding.typeName)
		}
		result.BindParam(parameter.Name, placeholder)
		bound[folded(name)] = true
		return nil
	}
	if expressionContainsPlaceholder(association.Actual) {
		return typeError(association.Actual.Position, "%s pattern association for parameter %q may use a placeholder only as the entire actual parameter", context, parameter.Name)
	}
	if !closedPatternAssociationExpression(association.Actual) {
		return typeError(association.Actual.Position, "%s pattern association for parameter %q requires an unsupported object or state-dependent expression", context, parameter.Name)
	}
	compiled, err := compileBehaviorExpression(association.Actual, map[string]behaviorBinding{}, map[string]behaviorBinding{})
	if err != nil {
		return err
	}
	value, actualType, err := arch.EvaluateConstant(compiled.value)
	if err != nil {
		return typeError(association.Actual.Position, "%s pattern association for parameter %q is not a closed deterministic value: %v", context, parameter.Name, err)
	}
	if !gorapide.CanonicalValueMatchesPredefinedType(value, expected) {
		return typeError(association.Actual.Position, "%s pattern value for parameter %s has type %s but the action parameter has type %s", context, parameter.Name, actualType, expected)
	}
	result.WhereParam(parameter.Name, value)
	return nil
}

func closedPatternAssociationExpression(expression ExpressionDecl) bool {
	switch expression.Kind {
	case ExpressionInteger, ExpressionFloat, ExpressionCharacter, ExpressionString, ExpressionUnit, ExpressionBoolean:
		return true
	case ExpressionCall:
		if expression.Left != nil && !closedPatternAssociationExpression(*expression.Left) {
			return false
		}
		for _, argument := range expression.Arguments {
			if !closedPatternAssociationExpression(argument) {
				return false
			}
		}
		return true
	case ExpressionUnary:
		return expression.Left != nil && closedPatternAssociationExpression(*expression.Left)
	case ExpressionQualified:
		return expression.Left != nil && closedPatternAssociationExpression(*expression.Left)
	case ExpressionBinary:
		return expression.Left != nil && expression.Right != nil &&
			closedPatternAssociationExpression(*expression.Left) &&
			closedPatternAssociationExpression(*expression.Right)
	case ExpressionConditional:
		return expression.Left != nil && expression.Right != nil && len(expression.Arguments) == 1 &&
			closedPatternAssociationExpression(*expression.Left) &&
			closedPatternAssociationExpression(*expression.Right) &&
			closedPatternAssociationExpression(expression.Arguments[0])
	default:
		return false
	}
}

func callArgumentsSemanticKey(call CallStatementDecl) string {
	positional := make([]string, 0, len(call.Arguments))
	named := make([]string, 0, len(call.Arguments))
	for index, argument := range call.Arguments {
		key := behaviorExpressionKey(argument)
		formal := ""
		if index < len(call.ArgumentFormals) {
			formal = call.ArgumentFormals[index]
		}
		if formal == "" {
			positional = append(positional, key)
		} else {
			named = append(named, folded(formal)+" is "+key)
		}
	}
	sort.Strings(named)
	return strings.Join(append(positional, named...), ",")
}

func behaviorStatementKey(statement BehaviorStatementDecl) string {
	switch statement.Kind {
	case BehaviorCallStatement:
		var builder strings.Builder
		builder.WriteString("call:")
		builder.WriteString(folded(statement.Call.Name))
		builder.WriteByte('(')
		builder.WriteString(callArgumentsSemanticKey(statement.Call))
		builder.WriteByte(')')
		builder.WriteString(timingSemanticKey(statement.Call.Timing))
		builder.WriteByte(';')
		return builder.String()
	case BehaviorRaiseStatement:
		var builder strings.Builder
		builder.WriteString("raise:")
		builder.WriteString(folded(statement.Call.Name))
		builder.WriteByte('(')
		builder.WriteString(callArgumentsSemanticKey(statement.Call))
		builder.WriteByte(')')
		if statement.Condition != nil {
			builder.WriteString(":where:")
			builder.WriteString(behaviorExpressionKey(*statement.Condition))
		}
		builder.WriteByte(';')
		return builder.String()
	case BehaviorReraiseStatement:
		if statement.Condition == nil {
			return "reraise;"
		}
		return "reraise:where:" + behaviorExpressionKey(*statement.Condition) + ";"
	case BehaviorAssignmentStatement:
		if statement.Function != nil {
			var builder strings.Builder
			builder.WriteString("assign:")
			builder.WriteString(folded(statement.Target))
			builder.WriteString(":=call:")
			builder.WriteString(folded(statement.Function.Name))
			builder.WriteByte('(')
			builder.WriteString(callArgumentsSemanticKey(*statement.Function))
			builder.WriteByte(')')
			builder.WriteString(timingSemanticKey(statement.Function.Timing))
			builder.WriteByte(';')
			return builder.String()
		}
		return "assign:" + folded(statement.Target) + ":=" + behaviorExpressionKey(statement.Expression) + ";"
	case BehaviorIfStatement:
		if statement.Condition == nil {
			return "invalid-if"
		}
		var builder strings.Builder
		builder.WriteString("if:")
		builder.WriteString(behaviorExpressionKey(*statement.Condition))
		builder.WriteString(":then{")
		for _, child := range statement.Then {
			builder.WriteString(behaviorStatementKey(child))
		}
		builder.WriteString("}:else{")
		for _, child := range statement.Else {
			builder.WriteString(behaviorStatementKey(child))
		}
		builder.WriteString("};")
		return builder.String()
	case BehaviorDoStatement:
		var builder strings.Builder
		builder.WriteString("do:")
		builder.WriteString(folded(statement.Label))
		builder.WriteString(":declare{")
		builder.WriteString(exceptionDeclarationsSemanticKey(statement.Exceptions))
		builder.WriteString("}{")
		for _, child := range statement.Body {
			builder.WriteString(behaviorStatementKey(child))
		}
		builder.WriteString("}:handler{")
		if statement.Handler != nil {
			choices := make([]string, 0, len(statement.Handler.Choices))
			for _, choice := range statement.Handler.Choices {
				var choiceKey strings.Builder
				choiceKey.WriteString(behaviorPatternKey(choice.Pattern))
				choiceKey.WriteString("=>{")
				for _, child := range choice.Statements {
					choiceKey.WriteString(behaviorStatementKey(child))
				}
				choiceKey.WriteByte('}')
				choices = append(choices, choiceKey.String())
			}
			sort.Strings(choices)
			for _, choice := range choices {
				builder.WriteString(choice)
			}
			if statement.Handler.Else != nil {
				builder.WriteString(":else{")
				for _, child := range statement.Handler.Else {
					builder.WriteString(behaviorStatementKey(child))
				}
				builder.WriteByte('}')
			}
		}
		builder.WriteString("};")
		return builder.String()
	case BehaviorLoopStatement:
		var builder strings.Builder
		builder.WriteString("loop:")
		builder.WriteString(folded(statement.Label))
		builder.WriteByte(':')
		if statement.Condition != nil {
			builder.WriteString("while:")
			builder.WriteString(behaviorExpressionKey(*statement.Condition))
		}
		builder.WriteString("{")
		for _, child := range statement.Body {
			builder.WriteString(behaviorStatementKey(child))
		}
		builder.WriteString("};")
		return builder.String()
	case BehaviorForStatement:
		var builder strings.Builder
		builder.WriteString("for:")
		builder.WriteString(folded(statement.Label))
		builder.WriteByte(':')
		if statement.ForInitial != nil || statement.ForTest != nil || statement.ForNext != nil {
			builder.WriteString("general:")
			builder.WriteString(behaviorObjectExpressionKey(statement.ForInitial))
			builder.WriteString(":in:")
			builder.WriteString(behaviorObjectExpressionKey(statement.ForTest))
			builder.WriteString(":next:")
			builder.WriteString(behaviorObjectExpressionKey(statement.ForNext))
		} else {
			builder.WriteString(folded(statement.Iterator))
			builder.WriteByte(':')
			builder.WriteString(folded(canonicalPredefinedType(statement.IteratorType)))
			builder.WriteString(":in:")
			builder.WriteString(behaviorExpressionKey(statement.RangeFirst))
			builder.WriteString("..")
			builder.WriteString(behaviorExpressionKey(statement.RangeLast))
		}
		builder.WriteString("{")
		for _, child := range statement.Body {
			builder.WriteString(behaviorStatementKey(child))
		}
		builder.WriteString("};")
		return builder.String()
	case BehaviorExitStatement, BehaviorNextStatement:
		key := string(statement.Kind) + ":" + folded(statement.ControlDo)
		if statement.Condition != nil {
			key += ":where:" + behaviorExpressionKey(*statement.Condition)
		}
		return key + ";"
	case BehaviorReturnStatement:
		if statement.Expression.Kind == "" {
			return "return;"
		}
		return "return:" + behaviorExpressionKey(statement.Expression) + ";"
	case BehaviorCaseStatement:
		var builder strings.Builder
		builder.WriteString("case:")
		builder.WriteString(behaviorExpressionKey(statement.Expression))
		builder.WriteString(":")
		builder.WriteString(strings.ToLower(statement.CaseMode))
		builder.WriteString("{")
		for _, alternative := range statement.Cases {
			builder.WriteString("choices[")
			for _, choice := range alternative.Choices {
				builder.WriteString(behaviorExpressionKey(choice.First))
				if choice.Last != nil {
					builder.WriteString("..")
					builder.WriteString(behaviorExpressionKey(*choice.Last))
				}
				builder.WriteByte(',')
			}
			builder.WriteString("]=>{")
			for _, child := range alternative.Body {
				builder.WriteString(behaviorStatementKey(child))
			}
			builder.WriteString("};")
		}
		builder.WriteString("}:default{")
		for _, child := range statement.Default {
			builder.WriteString(behaviorStatementKey(child))
		}
		builder.WriteString("};")
		return builder.String()
	case BehaviorAssertStatement:
		if statement.Condition == nil {
			return "invalid-assert"
		}
		return "assert:" + behaviorExpressionKey(*statement.Condition) + ";"
	case BehaviorNullStatement:
		return "null;"
	case BehaviorTimedStatement:
		return "timed:" + timingSemanticKey(statement.Timing) + ";"
	default:
		return "invalid-statement:" + string(statement.Kind)
	}
}

func exceptionDeclarationsSemanticKey(exceptions []ExceptionDecl) string {
	declarations := make([]string, 0, len(exceptions))
	for _, exception := range exceptions {
		parameters := make([]string, 0, len(exception.Parameters))
		for _, parameter := range exception.Parameters {
			parameters = append(parameters, folded(parameter.Name)+":"+folded(parameter.Type))
		}
		sort.Strings(parameters)
		declarations = append(declarations,
			folded(exception.Name)+"("+strings.Join(parameters, ",")+")")
	}
	sort.Strings(declarations)
	return strings.Join(declarations, ";")
}

func behaviorObjectExpressionKey(expression *BehaviorObjectExpressionDecl) string {
	if expression == nil {
		return "missing"
	}
	switch expression.Kind {
	case BehaviorObjectValue:
		return "value:" + behaviorExpressionKey(expression.Expression)
	case BehaviorObjectAssignment:
		return "assign:" + folded(expression.Target) + ":=" + behaviorExpressionKey(expression.Expression)
	case BehaviorObjectFunction:
		var builder strings.Builder
		builder.WriteString("call:")
		builder.WriteString(folded(expression.Call.Name))
		builder.WriteByte('(')
		builder.WriteString(callArgumentsSemanticKey(expression.Call))
		builder.WriteByte(')')
		return builder.String()
	default:
		return "invalid:" + string(expression.Kind)
	}
}

func timingSemanticKey(timing *TimingDecl) string {
	if timing == nil {
		return ""
	}
	if timing.Name != "" {
		return fmt.Sprintf(":%s:named-ticks:%s",
			strings.ToLower(string(timing.Kind)), folded(timing.Name))
	}
	if timing.Value != nil {
		return fmt.Sprintf(":%s:%s.ticks:object:%s",
			strings.ToLower(string(timing.Kind)), folded(timing.Clock), behaviorExpressionKey(*timing.Value))
	}
	if timing.RangeFirst != nil || timing.RangeLast != nil {
		first := "missing"
		last := "missing"
		if timing.RangeFirst != nil {
			first = behaviorExpressionKey(*timing.RangeFirst)
		}
		if timing.RangeLast != nil {
			last = behaviorExpressionKey(*timing.RangeLast)
		}
		return fmt.Sprintf(":%s:%s.ticks:range-expression:%s..%s",
			strings.ToLower(string(timing.Kind)), folded(timing.Clock), first, last)
	}
	return fmt.Sprintf(":%s:%s.ticks:range:%020d..%020d",
		strings.ToLower(string(timing.Kind)), folded(timing.Clock), timing.First, timing.Last)
}

func behaviorExpressionKey(expression ExpressionDecl) string {
	switch expression.Kind {
	case ExpressionName:
		return "name:" + folded(expression.Name)
	case ExpressionPlaceholder:
		return "placeholder:" + folded(expression.Name)
	case ExpressionUniversal:
		return "universal-placeholder:" + folded(expression.Name)
	case ExpressionState:
		return "state:" + folded(expression.Name)
	case ExpressionInteger:
		return fmt.Sprintf("integer:%d", expression.Integer)
	case ExpressionFloat:
		return fmt.Sprintf("float:%016x", math.Float64bits(expression.Float))
	case ExpressionCharacter:
		return fmt.Sprintf("character:%d", expression.Character)
	case ExpressionString:
		codes := sourceStringLiteralCodes(expression)
		var builder strings.Builder
		builder.WriteString("string:")
		for index, code := range codes {
			if index != 0 {
				builder.WriteByte(',')
			}
			fmt.Fprintf(&builder, "%d", code)
		}
		return builder.String()
	case ExpressionUnit:
		return "unit"
	case ExpressionBoolean:
		return fmt.Sprintf("boolean:%t", expression.Boolean)
	case ExpressionCall:
		var builder strings.Builder
		builder.WriteString("call:")
		if expression.Left != nil {
			builder.WriteString(behaviorExpressionKey(*expression.Left))
			builder.WriteByte('.')
		}
		builder.WriteString(folded(expression.Name))
		builder.WriteByte('(')
		argumentKeys := make([]string, 0, len(expression.Arguments))
		namedKeys := make([]string, 0, len(expression.Arguments))
		for index, argument := range expression.Arguments {
			key := behaviorExpressionKey(argument)
			formal := ""
			if index < len(expression.ArgumentFormals) {
				formal = expression.ArgumentFormals[index]
			}
			if formal == "" {
				argumentKeys = append(argumentKeys, key)
			} else {
				namedKeys = append(namedKeys, folded(formal)+"="+key)
			}
		}
		sort.Strings(namedKeys)
		argumentKeys = append(argumentKeys, namedKeys...)
		for index, key := range argumentKeys {
			if index != 0 {
				builder.WriteByte(',')
			}
			builder.WriteString(key)
		}
		builder.WriteByte(')')
		return builder.String()
	case ExpressionUnary:
		if expression.Left == nil {
			return "invalid-unary"
		}
		return "(" + strings.ToLower(expression.Operator) + behaviorExpressionKey(*expression.Left) + ")"
	case ExpressionBinary:
		if expression.Left == nil || expression.Right == nil {
			return "invalid-binary"
		}
		return "(" + behaviorExpressionKey(*expression.Left) + expression.Operator + behaviorExpressionKey(*expression.Right) + ")"
	case ExpressionConditional:
		if expression.Left == nil || expression.Right == nil || len(expression.Arguments) != 1 {
			return "invalid-conditional"
		}
		return "(if:" + behaviorExpressionKey(*expression.Left) + ":" +
			behaviorExpressionKey(*expression.Right) + ":" + behaviorExpressionKey(expression.Arguments[0]) + ")"
	case ExpressionQualified:
		if expression.Left == nil {
			return "invalid-qualified"
		}
		return "qualified:" + folded(expression.Name) + "'(" + behaviorExpressionKey(*expression.Left) + ")"
	case ExpressionSelection:
		if expression.Left == nil {
			return "invalid-selection"
		}
		return "selection:" + behaviorExpressionKey(*expression.Left) + "." + folded(expression.Name)
	case ExpressionRecord:
		fields := append([]RecordFieldExpressionDecl(nil), expression.RecordFields...)
		sort.Slice(fields, func(left, right int) bool {
			return folded(fields[left].Name) < folded(fields[right].Name)
		})
		var builder strings.Builder
		builder.WriteString("record:(")
		for index, field := range fields {
			if index != 0 {
				builder.WriteByte(',')
			}
			builder.WriteString(folded(field.Name))
			builder.WriteString(" is ")
			builder.WriteString(behaviorExpressionKey(field.Value))
		}
		builder.WriteByte(')')
		return builder.String()
	default:
		return "invalid:" + string(expression.Kind)
	}
}

func sourceStringLiteralValue(expression ExpressionDecl) any {
	if expression.StringCodes != nil {
		return gorapide.RapideStringFromCodes(expression.StringCodes...)
	}
	return expression.String
}

func sourceStringLiteralCodes(expression ExpressionDecl) []int64 {
	if expression.StringCodes != nil {
		return append([]int64(nil), expression.StringCodes...)
	}
	codes, err := gorapide.CanonicalRapideStringCodes(expression.String)
	if err != nil {
		return nil
	}
	return codes
}

func compileBehaviorStatement(
	declaration InterfaceDecl,
	owner string,
	index int,
	statement BehaviorStatementDecl,
	bindings, states map[string]behaviorBinding,
	functionReturnType *string,
) (arch.Statement, error) {
	return compileBehaviorStatementWithHandlerContext(
		declaration, owner, index, statement, bindings, states, functionReturnType, false,
	)
}

func compileBehaviorStatementWithHandlerContext(
	declaration InterfaceDecl,
	owner string,
	index int,
	statement BehaviorStatementDecl,
	bindings, states map[string]behaviorBinding,
	functionReturnType *string,
	handlerActive bool,
) (arch.Statement, error) {
	switch statement.Kind {
	case BehaviorCallStatement:
		return compileBehaviorCall(declaration, owner, index, statement.Call, bindings, states)
	case BehaviorRaiseStatement:
		exception, exists := findExceptionReference(declaration, statement.Call.Name, bindings)
		if !exists {
			return arch.Statement{}, typeError(statement.Call.Position,
				"raise names undeclared exception %q", statement.Call.Name)
		}
		arguments, err := compileCallActualExpressions(statement.Call, bindings, states)
		if err != nil {
			return arch.Statement{}, err
		}
		parameters, compatible, reason, err := associateCallActuals(
			statement.Call, exception.Parameters, arguments, false,
		)
		if err != nil {
			return arch.Statement{}, err
		}
		if !compatible {
			return arch.Statement{}, typeError(statement.Call.Position,
				"exception %q raise association is invalid: %s", exception.Name, reason)
		}
		id := fmt.Sprintf("rpd:%s:%06d:raise:%s", folded(owner), index, folded(exception.Name))
		if statement.Condition == nil {
			return arch.RaiseDeclaredException(
				id, exception.Declaration, exception.Name, parameters...,
			), nil
		}
		condition, err := compileBehaviorExpression(*statement.Condition, bindings, states)
		if err != nil {
			return arch.Statement{}, err
		}
		if condition.typeName != "Boolean" {
			return arch.Statement{}, typeError(statement.Condition.Position,
				"raise where condition has type %s, want Boolean", condition.typeName)
		}
		return arch.RaiseDeclaredExceptionWhere(
			id, exception.Declaration, exception.Name, condition.value, parameters...,
		), nil
	case BehaviorReraiseStatement:
		if !handlerActive {
			return arch.Statement{}, typeError(statement.Position,
				"unnamed raise requires a lexically enclosing active handler")
		}
		if statement.Condition == nil {
			return arch.ReraiseException(), nil
		}
		condition, err := compileBehaviorExpression(*statement.Condition, bindings, states)
		if err != nil {
			return arch.Statement{}, err
		}
		if condition.typeName != "Boolean" {
			return arch.Statement{}, typeError(statement.Condition.Position,
				"unnamed raise where condition has type %s, want Boolean", condition.typeName)
		}
		return arch.ReraiseExceptionWhere(condition.value), nil
	case BehaviorAssignmentStatement:
		state, ok := states[folded(statement.Target)]
		if !ok {
			return arch.Statement{}, typeError(statement.Position, "behavior assignment targets undeclared state %q", statement.Target)
		}
		if statement.Function != nil {
			return compileBehaviorFunctionAssignment(
				declaration, owner, index, state, *statement.Function, bindings, states,
			)
		}
		value, err := compileBehaviorExpression(statement.Expression, bindings, states)
		if err != nil {
			return arch.Statement{}, err
		}
		if !sourceBehaviorExpressionAssignable(value, state.typeName) {
			return arch.Statement{}, typeError(statement.Expression.Position, "behavior assignment to %q has type %s, want %s", state.name, value.typeName, state.typeName)
		}
		return arch.SetState(state.name, value.value), nil
	case BehaviorIfStatement:
		if statement.Condition == nil {
			return arch.Statement{}, typeError(statement.Position, "behavior if statement has no condition")
		}
		condition, err := compileBehaviorExpression(*statement.Condition, bindings, states)
		if err != nil {
			return arch.Statement{}, err
		}
		if condition.typeName != "Boolean" {
			return arch.Statement{}, typeError(statement.Condition.Position, "behavior if condition has type %s, want Boolean", condition.typeName)
		}
		thenBranch := make([]arch.Statement, 0, len(statement.Then))
		for childIndex, child := range statement.Then {
			compiled, err := compileBehaviorStatementWithHandlerContext(
				declaration, fmt.Sprintf("%s:if:%06d:then", owner, index), childIndex,
				child, bindings, states, functionReturnType, handlerActive,
			)
			if err != nil {
				return arch.Statement{}, err
			}
			thenBranch = append(thenBranch, compiled)
		}
		elseBranch := make([]arch.Statement, 0, len(statement.Else))
		for childIndex, child := range statement.Else {
			compiled, err := compileBehaviorStatementWithHandlerContext(
				declaration, fmt.Sprintf("%s:if:%06d:else", owner, index), childIndex,
				child, bindings, states, functionReturnType, handlerActive,
			)
			if err != nil {
				return arch.Statement{}, err
			}
			elseBranch = append(elseBranch, compiled)
		}
		return arch.IfThen(condition.value, thenBranch, elseBranch), nil
	case BehaviorDoStatement:
		doDeclaration, err := qualifyBehaviorDoExceptionScope(declaration, statement)
		if err != nil {
			return arch.Statement{}, err
		}
		body := make([]arch.Statement, 0, len(statement.Body))
		for childIndex, child := range statement.Body {
			compiled, err := compileBehaviorStatementWithHandlerContext(
				doDeclaration, fmt.Sprintf("%s:do:%06d", owner, index), childIndex,
				child, bindings, states, functionReturnType, handlerActive,
			)
			if err != nil {
				return arch.Statement{}, err
			}
			body = append(body, compiled)
		}
		if statement.Handler == nil {
			compiled := arch.DoBlock(body...)
			if statement.Label != "" {
				compiled = arch.NameDo(statement.Label, compiled)
			}
			return compiled, nil
		}
		handler, err := compileProcessExceptionHandler(
			doDeclaration, *statement.Handler, fmt.Sprintf("%s:do:%06d", owner, index),
			bindings, states, functionReturnType, true,
		)
		if err != nil {
			return arch.Statement{}, err
		}
		compiled := arch.HandleDo(body, handler)
		if statement.Label != "" {
			compiled = arch.NameDo(statement.Label, compiled)
		}
		return compiled, nil
	case BehaviorLoopStatement:
		body := make([]arch.Statement, 0, len(statement.Body)+1)
		if statement.Condition != nil {
			condition, err := compileBehaviorExpression(*statement.Condition, bindings, states)
			if err != nil {
				return arch.Statement{}, err
			}
			if condition.typeName != "Boolean" {
				return arch.Statement{}, typeError(statement.Condition.Position, "behavior while condition has type %s, want Boolean", condition.typeName)
			}
			body = append(body, arch.ExitWhen(arch.NotValue(condition.value)))
		}
		for childIndex, child := range statement.Body {
			compiled, err := compileBehaviorStatementWithHandlerContext(
				declaration, fmt.Sprintf("%s:loop:%06d", owner, index), childIndex,
				child, bindings, states, functionReturnType, handlerActive,
			)
			if err != nil {
				return arch.Statement{}, err
			}
			body = append(body, compiled)
		}
		compiled := arch.LoopDo(body...)
		if statement.Label != "" {
			compiled = arch.NameDo(statement.Label, compiled)
		}
		return compiled, nil
	case BehaviorForStatement:
		if statement.ForInitial != nil || statement.ForTest != nil || statement.ForNext != nil {
			if statement.ForInitial == nil || statement.ForTest == nil || statement.ForNext == nil {
				return arch.Statement{}, typeError(statement.Position, "general for statement requires initializer, test, and next object expressions")
			}
			initial, err := compileBehaviorObjectExpression(
				declaration, owner, index, "initializer", *statement.ForInitial, bindings, states,
			)
			if err != nil {
				return arch.Statement{}, err
			}
			test, err := compileBehaviorObjectExpression(
				declaration, owner, index, "test", *statement.ForTest, bindings, states,
			)
			if err != nil {
				return arch.Statement{}, err
			}
			if test.typeName != "Boolean" {
				return arch.Statement{}, typeError(statement.ForTest.Position,
					"general for test has type %s, want Boolean", test.typeName)
			}
			next, err := compileBehaviorObjectExpression(
				declaration, owner, index, "next", *statement.ForNext, bindings, states,
			)
			if err != nil {
				return arch.Statement{}, err
			}
			body := make([]arch.Statement, 0, len(statement.Body))
			for childIndex, child := range statement.Body {
				compiled, err := compileBehaviorStatementWithHandlerContext(
					declaration, fmt.Sprintf("%s:general-for:%06d", owner, index), childIndex,
					child, bindings, states, functionReturnType, handlerActive,
				)
				if err != nil {
					return arch.Statement{}, err
				}
				body = append(body, compiled)
			}
			compiled := arch.ForObjectExpressions(initial.value, test.value, next.value, body...)
			if statement.Label != "" {
				compiled = arch.NameDo(statement.Label, compiled)
			}
			return compiled, nil
		}
		if canonicalPredefinedType(statement.IteratorType) != "Integer" {
			return arch.Statement{}, typeError(statement.Position,
				"procedural range iterator %q has type %s, want Integer",
				statement.Iterator, statement.IteratorType,
			)
		}
		first, err := compileBehaviorExpression(statement.RangeFirst, bindings, states)
		if err != nil {
			return arch.Statement{}, err
		}
		last, err := compileBehaviorExpression(statement.RangeLast, bindings, states)
		if err != nil {
			return arch.Statement{}, err
		}
		if first.typeName != "Integer" || last.typeName != "Integer" {
			return arch.Statement{}, typeError(statement.Position,
				"procedural iterator range endpoints have types %s and %s, want Integer",
				first.typeName, last.typeName,
			)
		}
		iteratorName := folded(statement.Iterator)
		bodyBindings := copyBehaviorBindings(bindings, 1)
		if iteratorName != "" {
			bodyBindings[iteratorName] = behaviorBinding{name: iteratorName, typeName: "Integer"}
		}
		body := make([]arch.Statement, 0, len(statement.Body))
		for childIndex, child := range statement.Body {
			compiled, err := compileBehaviorStatementWithHandlerContext(
				declaration, fmt.Sprintf("%s:for:%06d", owner, index), childIndex,
				child, bodyBindings, states, functionReturnType, handlerActive,
			)
			if err != nil {
				return arch.Statement{}, err
			}
			body = append(body, compiled)
		}
		compiled := arch.ForEachIntegerRange(iteratorName, first.value, last.value, body...)
		if statement.Label != "" {
			compiled = arch.NameDo(statement.Label, compiled)
		}
		return compiled, nil
	case BehaviorExitStatement, BehaviorNextStatement:
		condition := arch.LiteralValue(true)
		if statement.Condition != nil {
			compiled, err := compileBehaviorExpression(*statement.Condition, bindings, states)
			if err != nil {
				return arch.Statement{}, err
			}
			if compiled.typeName != "Boolean" {
				return arch.Statement{}, typeError(statement.Condition.Position, "behavior %s condition has type %s, want Boolean", statement.Kind, compiled.typeName)
			}
			condition = compiled.value
		}
		if statement.Kind == BehaviorExitStatement {
			if statement.ControlDo != "" {
				return arch.ExitNamedWhen(statement.ControlDo, condition), nil
			}
			return arch.ExitWhen(condition), nil
		}
		if statement.ControlDo != "" {
			return arch.NextNamedWhen(statement.ControlDo, condition), nil
		}
		return arch.NextWhen(condition), nil
	case BehaviorReturnStatement:
		if functionReturnType == nil {
			return arch.Statement{}, typeError(statement.Position, "behavior return is only allowed in a function body")
		}
		if *functionReturnType == "" {
			if statement.Expression.Kind != "" {
				return arch.Statement{}, typeError(statement.Expression.Position, "void behavior function cannot return a value")
			}
			return arch.ReturnFromFunctionVoid(), nil
		}
		if statement.Expression.Kind == "" {
			return arch.Statement{}, typeError(statement.Position, "typed behavior function return requires a %s value", *functionReturnType)
		}
		returned, err := compileBehaviorExpression(statement.Expression, bindings, states)
		if err != nil {
			return arch.Statement{}, err
		}
		if !sourceBehaviorExpressionAssignable(returned, *functionReturnType) {
			return arch.Statement{}, typeError(statement.Expression.Position, "behavior function returns %s, want %s", returned.typeName, *functionReturnType)
		}
		return arch.ReturnFromFunction(returned.value), nil
	case BehaviorCaseStatement:
		selector, err := compileBehaviorExpression(statement.Expression, bindings, states)
		if err != nil {
			return arch.Statement{}, err
		}
		mode := arch.CaseXorMode
		switch strings.ToLower(statement.CaseMode) {
		case "", "xor":
			mode = arch.CaseXorMode
		case "or":
			mode = arch.CaseOrMode
		case "else":
			mode = arch.CaseElseMode
		default:
			return arch.Statement{}, typeError(statement.Position, "unsupported behavior case separator %q", statement.CaseMode)
		}
		alternatives := make([]arch.CaseAlternative, 0, len(statement.Cases))
		for alternativeIndex, alternative := range statement.Cases {
			choices := make([]arch.CaseChoice, 0, len(alternative.Choices))
			for _, choice := range alternative.Choices {
				if choice.Last == nil && choice.First.Kind == ExpressionName {
					if _, isType := predefinedTypes[folded(choice.First.Name)]; isType {
						return arch.Statement{}, typeError(choice.Position, "behavior case type choices are outside the current source subset")
					}
				}
				first, err := compileBehaviorExpression(choice.First, bindings, states)
				if err != nil {
					return arch.Statement{}, err
				}
				if !sourceBehaviorExpressionAssignable(first, selector.typeName) {
					return arch.Statement{}, typeError(choice.Position, "behavior case choice has type %s, want %s", first.typeName, selector.typeName)
				}
				if choice.Last == nil {
					choices = append(choices, arch.CaseValueChoice(first.value))
					continue
				}
				last, err := compileBehaviorExpression(*choice.Last, bindings, states)
				if err != nil {
					return arch.Statement{}, err
				}
				if !sourceIntegerType(first.typeName) || !sourceIntegerType(last.typeName) || !sourceIntegerType(selector.typeName) {
					return arch.Statement{}, typeError(choice.Position, "behavior case ranges require an Integer selector and Integer endpoints")
				}
				if !sourceBehaviorExpressionAssignable(last, selector.typeName) {
					return arch.Statement{}, typeError(choice.Position, "behavior case range endpoint has type %s, want %s", last.typeName, selector.typeName)
				}
				choices = append(choices, arch.CaseRangeChoice(first.value, last.value))
			}
			body := make([]arch.Statement, 0, len(alternative.Body))
			for childIndex, child := range alternative.Body {
				compiled, err := compileBehaviorStatementWithHandlerContext(
					declaration, fmt.Sprintf("%s:case:%06d:alternative:%06d", owner, index, alternativeIndex),
					childIndex, child, bindings, states, functionReturnType, handlerActive,
				)
				if err != nil {
					return arch.Statement{}, err
				}
				body = append(body, compiled)
			}
			alternatives = append(alternatives, arch.CaseWhenChoices(choices, body...))
		}
		defaultBody := make([]arch.Statement, 0, len(statement.Default))
		for childIndex, child := range statement.Default {
			compiled, err := compileBehaviorStatementWithHandlerContext(
				declaration, fmt.Sprintf("%s:case:%06d:default", owner, index),
				childIndex, child, bindings, states, functionReturnType, handlerActive,
			)
			if err != nil {
				return arch.Statement{}, err
			}
			defaultBody = append(defaultBody, compiled)
		}
		if statement.Default != nil {
			return arch.CaseOfDefault(selector.value, mode, defaultBody, alternatives...), nil
		}
		return arch.CaseOf(selector.value, mode, alternatives...), nil
	case BehaviorAssertStatement:
		if statement.Condition == nil {
			return arch.Statement{}, typeError(statement.Position, "behavior assertion has no condition")
		}
		condition, err := compileBehaviorExpression(*statement.Condition, bindings, states)
		if err != nil {
			return arch.Statement{}, err
		}
		if condition.typeName != "Boolean" {
			return arch.Statement{}, typeError(statement.Condition.Position, "behavior assertion has type %s, want Boolean", condition.typeName)
		}
		return arch.AssertThat(condition.value), nil
	case BehaviorNullStatement:
		return arch.NullStatement(), nil
	case BehaviorTimedStatement:
		if statement.Timing == nil {
			return arch.Statement{}, typeError(statement.Position, "timed statement has no timing expression")
		}
		switch statement.Timing.Kind {
		case TimingPause:
			return arch.PauseForRange(statement.Timing.Clock, statement.Timing.First, statement.Timing.Last), nil
		case TimingDelay:
			return arch.DelayForRange(statement.Timing.Clock, statement.Timing.First, statement.Timing.Last), nil
		default:
			return arch.Statement{}, typeError(statement.Timing.Position, "standalone %s timing statement is invalid", statement.Timing.Kind)
		}
	default:
		return arch.Statement{}, typeError(statement.Position, "unsupported behavior statement kind %q", statement.Kind)
	}
}

func compileCallActualExpressions(
	call CallStatementDecl,
	bindings, states map[string]behaviorBinding,
) ([]compiledBehaviorExpression, error) {
	if len(call.ArgumentFormals) != 0 && len(call.ArgumentFormals) != len(call.Arguments) {
		return nil, typeError(call.Position, "call %q has inconsistent argument-association metadata", call.Name)
	}
	result := make([]compiledBehaviorExpression, len(call.Arguments))
	for index, argument := range call.Arguments {
		compiled, err := compileBehaviorExpression(argument, bindings, states)
		if err != nil {
			return nil, err
		}
		result[index] = compiled
	}
	return result, nil
}

// associateCallActuals applies the published positional-prefix/named-suffix
// rule to one candidate declaration and returns arguments in formal declaration
// order. Function defaults are closed at compile time because the current
// source subset admits only closed predefined-scalar default denotations.
func associateCallActuals(
	call CallStatementDecl,
	formals []ParameterDecl,
	actuals []compiledBehaviorExpression,
	allowDefaults bool,
) ([]arch.RuleParameter, bool, string, error) {
	assigned := make([]*compiledBehaviorExpression, len(formals))
	named := false
	for actualIndex := range actuals {
		formalIndex := actualIndex
		formalName := ""
		if len(call.ArgumentFormals) != 0 {
			formalName = call.ArgumentFormals[actualIndex]
		}
		if formalName != "" {
			named = true
			formalIndex = -1
			for index, formal := range formals {
				if keyword(formal.Name, formalName) {
					formalIndex = index
					break
				}
			}
			if formalIndex < 0 {
				return nil, false, fmt.Sprintf("unknown formal %q", formalName), nil
			}
		} else if named {
			return nil, false, "positional arguments must precede named associations", nil
		}
		if formalIndex >= len(formals) {
			return nil, false, "too many positional arguments", nil
		}
		if assigned[formalIndex] != nil {
			return nil, false, fmt.Sprintf("formal %q is associated more than once", formals[formalIndex].Name), nil
		}
		actual := actuals[actualIndex]
		assigned[formalIndex] = &actual
	}

	parameters := make([]arch.RuleParameter, len(formals))
	for index, formal := range formals {
		actual := assigned[index]
		if actual == nil {
			if !allowDefaults || formal.Default == nil {
				return nil, false, fmt.Sprintf("formal %q has no actual or default", formal.Name), nil
			}
			typeName := canonicalPredefinedType(formal.Type)
			value, _, err := evaluateClosedGeneratorDefault(formal, typeName, "function")
			if err != nil {
				return nil, false, "", err
			}
			parameters[index] = arch.LiteralParam(formal.Name, value)
			continue
		}
		expected, expectedPredefined := predefinedTypes[folded(formal.Type)]
		if !expectedPredefined {
			expected = formal.Type
		}
		compatible := false
		if expectedPredefined {
			compatible = sourceBehaviorExpressionAssignable(*actual, expected)
		} else {
			compatible = keyword(actual.typeName, formal.Type)
		}
		if !compatible {
			return nil, false, fmt.Sprintf("formal %q has actual type %s, want %s", formal.Name, actual.typeName, expected), nil
		}
		parameters[index] = arch.ExpressionParam(formal.Name, actual.value)
	}
	return parameters, true, "", nil
}

func compileBehaviorFunctionAssignment(
	declaration InterfaceDecl,
	owner string,
	index int,
	target behaviorBinding,
	call CallStatementDecl,
	bindings, states map[string]behaviorBinding,
) (arch.Statement, error) {
	if call.Timing != nil {
		return arch.Statement{}, typeError(call.Timing.Position, "timing clauses may only be applied to action calls")
	}
	if action, exists := findAction(declaration, call.Name); exists {
		return arch.Statement{}, typeError(call.Position, "action %q cannot supply a function assignment result", action.Name)
	}
	functions := findFunctions(declaration, call.Name)
	if len(functions) == 0 {
		return arch.Statement{}, typeError(call.Position, "behavior function assignment calls undeclared function %q", call.Name)
	}
	arguments, err := compileCallActualExpressions(call, bindings, states)
	if err != nil {
		return arch.Statement{}, err
	}
	type functionCallMatch struct {
		declaration FunctionDecl
		parameters  []arch.RuleParameter
	}
	matches := make([]functionCallMatch, 0)
	for _, function := range functions {
		if function.ReturnType == "" ||
			!sourcePredefinedTypeAssignable(canonicalPredefinedType(function.ReturnType), target.typeName) {
			continue
		}
		parameters, compatible, _, err := associateCallActuals(call, function.Parameters, arguments, true)
		if err != nil {
			return arch.Statement{}, err
		}
		if compatible {
			matches = append(matches, functionCallMatch{declaration: function, parameters: parameters})
		}
	}
	if len(matches) != 1 {
		return arch.Statement{}, typeError(call.Position,
			"behavior function assignment %q to %s state %q matches %d compatible typed signatures, want 1",
			call.Name, target.typeName, target.name, len(matches))
	}
	selected := matches[0]
	id := fmt.Sprintf("rpd:%s:%06d:%s", folded(owner), index, folded(call.Name))
	return arch.CallFunctionInto(id, target.name, selected.declaration.Name, selected.parameters...), nil
}

type compiledBehaviorObjectExpression struct {
	value    arch.ExecutableObjectExpression
	typeName string
}

func compileBehaviorObjectExpression(
	declaration InterfaceDecl,
	owner string,
	index int,
	role string,
	expression BehaviorObjectExpressionDecl,
	bindings, states map[string]behaviorBinding,
) (compiledBehaviorObjectExpression, error) {
	switch expression.Kind {
	case BehaviorObjectValue:
		compiled, err := compileBehaviorExpression(expression.Expression, bindings, states)
		if err != nil {
			return compiledBehaviorObjectExpression{}, err
		}
		return compiledBehaviorObjectExpression{
			value: arch.ObjectValue(compiled.value), typeName: compiled.typeName,
		}, nil
	case BehaviorObjectAssignment:
		state, ok := states[folded(expression.Target)]
		if !ok {
			return compiledBehaviorObjectExpression{}, typeError(
				expression.Position, "general for %s assignment targets undeclared state %q", role, expression.Target,
			)
		}
		compiled, err := compileBehaviorExpression(expression.Expression, bindings, states)
		if err != nil {
			return compiledBehaviorObjectExpression{}, err
		}
		if !sourceBehaviorExpressionAssignable(compiled, state.typeName) {
			return compiledBehaviorObjectExpression{}, typeError(
				expression.Expression.Position,
				"general for %s assignment to %q has type %s, want %s",
				role, state.name, compiled.typeName, state.typeName,
			)
		}
		return compiledBehaviorObjectExpression{
			value:    arch.ObjectAssignment(state.name, compiled.value),
			typeName: "Ref(" + state.typeName + ")",
		}, nil
	case BehaviorObjectFunction:
		call := expression.Call
		if call.Timing != nil {
			return compiledBehaviorObjectExpression{}, typeError(call.Timing.Position,
				"timing clauses may only be applied to action-call statements")
		}
		if action, exists := findAction(declaration, call.Name); exists {
			return compiledBehaviorObjectExpression{}, typeError(call.Position,
				"action %q cannot be used as a general for object expression", action.Name)
		}
		functions := findFunctions(declaration, call.Name)
		if len(functions) == 0 {
			return compiledBehaviorObjectExpression{}, typeError(call.Position,
				"general for %s calls undeclared function %q", role, call.Name)
		}
		arguments, err := compileCallActualExpressions(call, bindings, states)
		if err != nil {
			return compiledBehaviorObjectExpression{}, err
		}
		type functionCallMatch struct {
			declaration FunctionDecl
			parameters  []arch.RuleParameter
		}
		matches := make([]functionCallMatch, 0)
		for _, function := range functions {
			parameters, compatible, _, err := associateCallActuals(call, function.Parameters, arguments, true)
			if err != nil {
				return compiledBehaviorObjectExpression{}, err
			}
			if compatible {
				matches = append(matches, functionCallMatch{declaration: function, parameters: parameters})
			}
		}
		if len(matches) != 1 {
			return compiledBehaviorObjectExpression{}, typeError(call.Position,
				"general for %s function call %q matches %d interface signatures, want 1",
				role, call.Name, len(matches))
		}
		selected := matches[0]
		id := fmt.Sprintf(
			"rpd:%s:%06d:general-for:%s:%s", folded(owner), index, folded(role), folded(call.Name),
		)
		returnType := canonicalPredefinedType(selected.declaration.ReturnType)
		if returnType == "" {
			returnType = "Root"
		}
		return compiledBehaviorObjectExpression{
			value: arch.ObjectFunctionCall(id, selected.declaration.Name, selected.parameters...), typeName: returnType,
		}, nil
	default:
		return compiledBehaviorObjectExpression{}, typeError(
			expression.Position, "unsupported general for %s object expression %q", role, expression.Kind,
		)
	}
}

func matchProvidedFunctionBody(declaration InterfaceDecl, body FunctionBodyDecl, owner string) (FunctionDecl, error) {
	matches := make([]FunctionDecl, 0)
	for _, function := range declaration.Functions {
		if function.Mode != FunctionProvides || !keyword(function.Name, body.Name) ||
			len(function.Parameters) != len(body.Parameters) || !keyword(function.ReturnType, body.ReturnType) {
			continue
		}
		compatible := true
		for index := range function.Parameters {
			if !keyword(function.Parameters[index].Name, body.Parameters[index].Name) ||
				!keyword(function.Parameters[index].Type, body.Parameters[index].Type) {
				compatible = false
				break
			}
		}
		if compatible {
			matches = append(matches, function)
		}
	}
	if len(matches) != 1 {
		return FunctionDecl{}, typeError(body.Position, "%s %q matches %d provided interface signatures, want 1", owner, body.Name, len(matches))
	}
	return matches[0], nil
}

func validateSupportedModuleMembership(
	module ModuleDecl,
	declaration InterfaceDecl,
	executionDeclaration InterfaceDecl,
) error {
	unsupported := make([]string, 0,
		len(declaration.TypeNames)+len(declaration.TypeConstructors)+
			len(declaration.ModuleGenerators)+len(executionDeclaration.Functions))
	for _, constructor := range declaration.TypeConstructors {
		unsupported = append(unsupported, fmt.Sprintf("%s type-constructor %s", constructor.Region, folded(constructor.Name)))
	}
	for _, function := range executionDeclaration.Functions {
		if function.Mode == FunctionPrivate {
			unsupported = append(unsupported, "private function "+folded(function.Name))
		}
	}
	for _, generator := range declaration.ModuleGenerators {
		unsupported = append(unsupported,
			fmt.Sprintf("%s module-generator %s", generator.Region, folded(generator.Name)))
	}
	if len(unsupported) != 0 {
		sort.Strings(unsupported)
		return typeError(module.Position,
			"module %q membership in interface %q requires unsupported concrete declarations: %s",
			module.Name, declaration.Name, strings.Join(unsupported, ", "))
	}
	for _, function := range executionDeclaration.Functions {
		if function.Mode != FunctionProvides {
			continue
		}
		matches := 0
		for _, body := range module.Functions {
			if functionBodyMatchesDeclaration(body, function) {
				matches++
			}
		}
		if matches != 1 {
			return typeError(module.Position,
				"module %q provides function %q with %d matching implementations, want 1",
				module.Name, function.Name, matches)
		}
	}
	return nil
}

func functionBodyMatchesDeclaration(body FunctionBodyDecl, declaration FunctionDecl) bool {
	if !keyword(body.Name, declaration.Name) || !keyword(body.ReturnType, declaration.ReturnType) ||
		len(body.Parameters) != len(declaration.Parameters) {
		return false
	}
	for index := range body.Parameters {
		if !keyword(body.Parameters[index].Name, declaration.Parameters[index].Name) ||
			!keyword(body.Parameters[index].Type, declaration.Parameters[index].Type) {
			return false
		}
	}
	return true
}

func compileBehaviorCall(
	declaration InterfaceDecl,
	functionName string,
	index int,
	call CallStatementDecl,
	bindings, states map[string]behaviorBinding,
) (arch.Statement, error) {
	if keyword(call.Name, "Link") || keyword(call.Name, "Unlink") {
		if call.Timing != nil {
			return arch.Statement{}, typeError(call.Timing.Position,
				"predefined %s cannot carry an action timing clause", call.Name)
		}
		if len(call.Arguments) != 1 ||
			(len(call.ArgumentFormals) != 0 && (len(call.ArgumentFormals) != 1 || call.ArgumentFormals[0] != "")) {
			return arch.Statement{}, typeError(call.Position,
				"predefined %s currently requires one positional module actual", call.Name)
		}
		argument, err := compileBehaviorExpression(call.Arguments[0], bindings, states)
		if err != nil {
			return arch.Statement{}, err
		}
		if argument.typeName == "" {
			return arch.Statement{}, typeError(call.Arguments[0].Position,
				"predefined %s actual has no structural interface type", call.Name)
		}
		if _, predefined := predefinedTypes[folded(argument.typeName)]; predefined {
			return arch.Statement{}, typeError(call.Arguments[0].Position,
				"predefined %s actual has scalar type %s, want a module interface", call.Name, argument.typeName)
		}
		if keyword(call.Name, "Link") {
			return arch.LinkModule(argument.value), nil
		}
		return arch.UnlinkModule(argument.value), nil
	}
	action, actionExists := findAction(declaration, call.Name)
	functions := findFunctions(declaration, call.Name)
	if actionExists && len(functions) != 0 {
		return arch.Statement{}, typeError(call.Position, "behavior call %q is ambiguous between an action and a function", call.Name)
	}
	id := fmt.Sprintf("rpd:%s:%06d:%s", folded(functionName), index, folded(call.Name))
	if actionExists {
		if action.Mode != ActionOut && action.Mode != ActionPrivate {
			return arch.Statement{}, typeError(call.Position, "behavior cannot generate in-action %q", action.Name)
		}
		arguments, err := compileCallActualExpressions(call, bindings, states)
		if err != nil {
			return arch.Statement{}, err
		}
		parameters, compatible, reason, err := associateCallActuals(call, action.Parameters, arguments, false)
		if err != nil {
			return arch.Statement{}, err
		}
		if !compatible {
			return arch.Statement{}, typeError(call.Position, "action %q call association is invalid: %s", action.Name, reason)
		}
		if call.Timing == nil {
			return arch.CallAction(id, action.Name, parameters...), nil
		}
		switch call.Timing.Kind {
		case TimingIn:
			return arch.CallActionInRange(id, action.Name, call.Timing.Clock, call.Timing.First, call.Timing.Last, parameters...), nil
		case TimingPause:
			return arch.CallActionPauseRange(id, action.Name, call.Timing.Clock, call.Timing.First, call.Timing.Last, parameters...), nil
		case TimingDelay:
			return arch.CallActionDelayRange(id, action.Name, call.Timing.Clock, call.Timing.First, call.Timing.Last, parameters...), nil
		default:
			return arch.Statement{}, typeError(call.Timing.Position, "unsupported timing clause %q", call.Timing.Kind)
		}
	}
	if len(functions) == 0 {
		return arch.Statement{}, typeError(call.Position, "behavior call %q is not a declared action or function", call.Name)
	}
	if call.Timing != nil {
		return arch.Statement{}, typeError(call.Timing.Position, "timing clauses may only be applied to action calls")
	}
	arguments, err := compileCallActualExpressions(call, bindings, states)
	if err != nil {
		return arch.Statement{}, err
	}
	type functionCallMatch struct {
		declaration FunctionDecl
		parameters  []arch.RuleParameter
	}
	matches := make([]functionCallMatch, 0)
	for _, function := range functions {
		parameters, compatible, _, err := associateCallActuals(call, function.Parameters, arguments, true)
		if err != nil {
			return arch.Statement{}, err
		}
		if compatible {
			matches = append(matches, functionCallMatch{declaration: function, parameters: parameters})
		}
	}
	if len(matches) != 1 {
		return arch.Statement{}, typeError(call.Position, "behavior function call %q matches %d interface signatures, want 1", call.Name, len(matches))
	}
	selected := matches[0]
	return arch.CallFunction(id, selected.declaration.Name, selected.parameters...), nil
}

func compileModuleNewExpression(
	expression ExpressionDecl,
	self behaviorBinding,
	bindings, states map[string]behaviorBinding,
) (compiledBehaviorExpression, error) {
	parameters := self.moduleNewParameters
	initialParameters := self.moduleNewInitialParameters
	specialization := self.moduleNewArguments
	if len(parameters) != len(specialization) {
		return compiledBehaviorExpression{}, typeError(expression.Position,
			"allocator New has inconsistent owning module-generator metadata")
	}
	if len(initialParameters) != len(self.moduleNewInitializationParameters) {
		return compiledBehaviorExpression{}, typeError(expression.Position,
			"allocator New has inconsistent owning module-initialization metadata")
	}
	allParameters := append(append([]ParameterDecl(nil), parameters...), initialParameters...)
	if len(expression.Arguments) > len(allParameters) {
		if len(initialParameters) == 0 {
			return compiledBehaviorExpression{}, typeError(expression.Position,
				"allocator New supplies %d generator actuals, but %d are declared",
				len(expression.Arguments), len(parameters))
		}
		return compiledBehaviorExpression{}, typeError(expression.Position,
			"allocator New supplies %d actuals, but %d generator-plus-initialization formals are declared",
			len(expression.Arguments), len(allParameters))
	}
	if len(expression.ArgumentFormals) != 0 &&
		len(expression.ArgumentFormals) != len(expression.Arguments) {
		return compiledBehaviorExpression{}, typeError(expression.Position,
			"allocator New has inconsistent argument-association metadata")
	}
	parameterIndices := make(map[string]int, len(allParameters))
	for index, parameter := range allParameters {
		parameterIndices[folded(parameter.Name)] = index
	}
	actuals := make(map[int]ExpressionDecl, len(expression.Arguments))
	named := false
	nextPositional := 0
	for actualIndex, actual := range expression.Arguments {
		formal := ""
		if len(expression.ArgumentFormals) != 0 {
			formal = expression.ArgumentFormals[actualIndex]
		}
		formalIndex := nextPositional
		if formal == "" {
			if named {
				return compiledBehaviorExpression{}, typeError(actual.Position,
					"allocator New positional actuals must precede named associations")
			}
			nextPositional++
		} else {
			named = true
			var exists bool
			formalIndex, exists = parameterIndices[folded(formal)]
			if !exists {
				kind := "formal"
				if len(initialParameters) == 0 {
					kind = "generator formal"
				}
				return compiledBehaviorExpression{}, typeError(actual.Position,
					"allocator New has no %s named %q", kind, formal)
			}
		}
		if formalIndex >= len(allParameters) {
			return compiledBehaviorExpression{}, typeError(actual.Position,
				"allocator New supplies too many positional actuals")
		}
		if _, duplicate := actuals[formalIndex]; duplicate {
			kind := "formal"
			if formalIndex < len(parameters) && len(initialParameters) == 0 {
				kind = "generator formal"
			}
			return compiledBehaviorExpression{}, typeError(actual.Position,
				"allocator New supplies %s %q more than once",
				kind, allParameters[formalIndex].Name)
		}
		actuals[formalIndex] = actual
	}
	arguments := make([]arch.ModuleGeneratorArgument, len(parameters))
	for index, parameter := range parameters {
		expected := specialization[index]
		if !keyword(parameter.Name, expected.Name) ||
			!sourceClosedScalarType(expected.Type) {
			return compiledBehaviorExpression{}, typeError(expression.Position,
				"allocator New has inconsistent generator formal %d", index+1)
		}
		var value any
		if actualExpression, explicitlySupplied := actuals[index]; explicitlySupplied {
			actual, err := compileBehaviorExpression(actualExpression, bindings, states)
			if err != nil {
				return compiledBehaviorExpression{}, err
			}
			if !sourceBehaviorExpressionAssignable(actual, expected.Type) {
				return compiledBehaviorExpression{}, typeError(actualExpression.Position,
					"allocator New formal %q has actual type %s, want %s",
					parameter.Name, actual.typeName, expected.Type)
			}
			value, _, err = arch.EvaluateConstant(actual.value)
			if err != nil {
				return compiledBehaviorExpression{}, typeError(actualExpression.Position,
					"allocator New formal %q requires a closed deterministic actual: %v",
					parameter.Name, err)
			}
		} else {
			var exists bool
			var err error
			value, exists, err = evaluateClosedGeneratorDefault(
				parameter, expected.Type, "allocator New",
			)
			if err != nil {
				return compiledBehaviorExpression{}, err
			}
			if !exists {
				return compiledBehaviorExpression{}, typeError(expression.Position,
					"allocator New formal %q has no actual or default", parameter.Name)
			}
		}
		values, err := gorapide.CanonicalizeParams(map[string]any{
			"actual": value, "expected": expected.Value,
		})
		if err != nil || !gorapide.CanonicalValueMatchesPredefinedType(values["actual"], expected.Type) {
			return compiledBehaviorExpression{}, typeError(expression.Position,
				"allocator New formal %q does not denote canonical %s data", parameter.Name, expected.Type)
		}
		if !reflect.DeepEqual(values["actual"], values["expected"]) {
			return compiledBehaviorExpression{}, typeError(expression.Position,
				"allocator New actual for formal %q selects a different module specialization",
				parameter.Name)
		}
		arguments[index] = arch.ModuleArgument(expected.Name, expected.Type, values["actual"])
	}
	initializationArguments := make([]arch.ModuleInitializationArgument, len(initialParameters))
	for index, parameter := range initialParameters {
		expected := self.moduleNewInitializationParameters[index]
		if !keyword(parameter.Name, expected.Name) || !sourceClosedScalarType(expected.Type) {
			return compiledBehaviorExpression{}, typeError(expression.Position,
				"allocator New has inconsistent initialization formal %d", index+1)
		}
		actualExpression, explicitlySupplied := actuals[len(parameters)+index]
		value := expected.Default
		if explicitlySupplied {
			actual, err := compileBehaviorExpression(actualExpression, bindings, states)
			if err != nil {
				return compiledBehaviorExpression{}, err
			}
			if !sourceBehaviorExpressionAssignable(actual, expected.Type) {
				return compiledBehaviorExpression{}, typeError(actualExpression.Position,
					"allocator New initialization formal %q has actual type %s, want %s",
					parameter.Name, actual.typeName, expected.Type)
			}
			value = actual.value
		}
		initializationArguments[index] = arch.ModuleInitialArgument(
			expected.Name, expected.Type, value,
		)
	}
	return compiledBehaviorExpression{
		value: arch.ModuleNewValueWithInitializationArguments(
			self.typeName, arguments, initializationArguments...,
		),
		typeName: self.typeName,
	}, nil
}

func compileBehaviorExpression(expression ExpressionDecl, bindings, states map[string]behaviorBinding) (compiledBehaviorExpression, error) {
	switch expression.Kind {
	case ExpressionName, ExpressionPlaceholder:
		binding, ok := bindings[folded(expression.Name)]
		if !ok {
			if state, exists := states[folded(expression.Name)]; exists {
				return compiledBehaviorExpression{}, typeError(expression.Position, "behavior state %q must be dereferenced with '$'", state.name)
			}
			return compiledBehaviorExpression{}, typeError(expression.Position, "behavior expression name %q is not declared in this body", expression.Name)
		}
		if expression.Kind == ExpressionPlaceholder && !binding.placeholder {
			return compiledBehaviorExpression{}, typeError(expression.Position, "?%s is not a behavior placeholder", expression.Name)
		}
		if expression.Kind == ExpressionName && binding.moduleSelf {
			return compiledBehaviorExpression{
				value: arch.ModuleSelfValue(binding.typeName), typeName: binding.typeName,
			}, nil
		}
		if expression.Kind == ExpressionName && binding.constant != nil {
			return compiledBehaviorExpression{value: *binding.constant, typeName: binding.typeName}, nil
		}
		if binding.structural {
			return compiledBehaviorExpression{}, typeError(expression.Position,
				"Record object %q is a structural module value; select one of its fields", binding.name)
		}
		if binding.universal {
			return compiledBehaviorExpression{}, typeError(expression.Position,
				"universal placeholder !%s is a qualification domain, not one scalar body binding", binding.name)
		}
		if expression.Kind == ExpressionName && binding.placeholder {
			return compiledBehaviorExpression{}, typeError(expression.Position, "behavior placeholder %q must be referenced with '?'", expression.Name)
		}
		return compiledBehaviorExpression{value: arch.BoundValue(binding.name), typeName: binding.typeName}, nil
	case ExpressionUniversal:
		return compiledBehaviorExpression{}, typeError(expression.Position,
			"universal placeholder !%s is substituted only inside its qualified pattern", expression.Name)
	case ExpressionState:
		state, ok := states[folded(expression.Name)]
		if !ok {
			return compiledBehaviorExpression{}, typeError(expression.Position, "behavior state $%s is not declared", expression.Name)
		}
		return compiledBehaviorExpression{value: arch.ReadState(state.name), typeName: state.typeName}, nil
	case ExpressionInteger:
		return compiledBehaviorExpression{value: arch.LiteralValue(expression.Integer), typeName: "Integer"}, nil
	case ExpressionFloat:
		return compiledBehaviorExpression{value: arch.LiteralValue(expression.Float), typeName: "Float"}, nil
	case ExpressionCharacter:
		return compiledBehaviorExpression{value: arch.LiteralValue(gorapide.RapideCharacterFromCode(expression.Character)), typeName: "Character"}, nil
	case ExpressionString:
		return compiledBehaviorExpression{value: arch.LiteralValue(sourceStringLiteralValue(expression)), typeName: "String"}, nil
	case ExpressionUnit:
		return compiledBehaviorExpression{value: arch.LiteralValue(gorapide.RapideUnit()), typeName: "Triv"}, nil
	case ExpressionBoolean:
		return compiledBehaviorExpression{value: arch.LiteralValue(expression.Boolean), typeName: "Boolean"}, nil
	case ExpressionSelection:
		if expression.Left == nil {
			return compiledBehaviorExpression{}, typeError(expression.Position, "component selection has no receiver")
		}
		if expression.Left.Kind != ExpressionName {
			return compiledBehaviorExpression{}, typeError(expression.Position,
				"Record component selection currently requires a directly named module Record object")
		}
		receiver, exists := bindings[folded(expression.Left.Name)]
		if !exists {
			return compiledBehaviorExpression{}, typeError(expression.Left.Position,
				"component selection receiver %q is not declared in this body", expression.Left.Name)
		}
		if !receiver.structural {
			return compiledBehaviorExpression{}, typeError(expression.Position,
				"component selection %s.%s has non-Record receiver %q; Union extraction and general structural qualification remain unsupported",
				expression.Left.Name, expression.Name, receiver.name)
		}
		field, exists := receiver.recordFields[folded(expression.Name)]
		if !exists {
			return compiledBehaviorExpression{}, typeError(expression.Position,
				"Record object %q of type %s has no field %q", receiver.name, receiver.typeName, expression.Name)
		}
		if field.constant == nil {
			return compiledBehaviorExpression{}, typeError(expression.Position,
				"Record field %s.%s is outside the immutable closed-scalar selection subset", receiver.name, field.name)
		}
		return compiledBehaviorExpression{value: *field.constant, typeName: field.typeName}, nil
	case ExpressionRecord:
		return compiledBehaviorExpression{}, typeError(expression.Position,
			"Record literal requires the contextual type of a named module Record object declaration")
	case ExpressionQualified:
		if expression.Left == nil {
			return compiledBehaviorExpression{}, typeError(expression.Position, "qualified expression has no operand")
		}
		target := canonicalPredefinedType(expression.Name)
		if !sourceClosedScalarType(target) {
			return compiledBehaviorExpression{}, typeError(expression.Position,
				"qualified-expression type %q is outside the direct predefined-scalar subset", expression.Name)
		}
		operand, err := compileBehaviorExpression(*expression.Left, bindings, states)
		if err != nil {
			return compiledBehaviorExpression{}, err
		}
		if !sourcePredefinedTypeAssignable(operand.typeName, target) {
			return compiledBehaviorExpression{}, typeError(expression.Position,
				"qualified-expression operand has type %s, which is not a subtype of %s", operand.typeName, target)
		}
		return compiledBehaviorExpression{value: arch.QualifyValue(target, operand.value), typeName: target}, nil
	case ExpressionCall:
		if expression.Left == nil && keyword(expression.Name, "New") {
			self, ok := bindings[folded("Self")]
			if !ok || !self.moduleSelf || self.typeName == "" {
				return compiledBehaviorExpression{}, typeError(expression.Position,
					"allocator New is callable only from its owning module")
			}
			if !self.moduleNew {
				return compiledBehaviorExpression{}, typeError(expression.Position,
					"allocator New requires the current deterministic dynamic-module specialization slice")
			}
			return compileModuleNewExpression(expression, self, bindings, states)
		}
		for _, formal := range expression.ArgumentFormals {
			if formal != "" {
				return compiledBehaviorExpression{}, typeError(expression.Position,
					"named association %q requires a supported declared function expression",
					formal)
			}
		}
		if expression.Left != nil && len(expression.Arguments) == 1 &&
			(expression.Name == "<" || expression.Name == "<=" || expression.Name == ">" || expression.Name == ">=") {
			left, err := compileBehaviorExpression(*expression.Left, bindings, states)
			if err != nil {
				return compiledBehaviorExpression{}, err
			}
			right, err := compileBehaviorExpression(expression.Arguments[0], bindings, states)
			if err != nil {
				return compiledBehaviorExpression{}, err
			}
			compatible := sourceIntegerType(left.typeName) && sourceIntegerType(right.typeName) ||
				left.typeName == right.typeName && (left.typeName == "Float" || left.typeName == "Character")
			if !compatible {
				return compiledBehaviorExpression{}, typeError(expression.Position,
					"Order.%s is not defined for %s and %s", expression.Name, left.typeName, right.typeName)
			}
			operators := map[string]func(arch.RuleValue, arch.RuleValue) arch.RuleValue{
				"<": arch.LessValues, "<=": arch.LessOrEqualValues,
				">": arch.GreaterValues, ">=": arch.GreaterOrEqualValues,
			}
			return compiledBehaviorExpression{value: operators[expression.Name](left.value, right.value), typeName: "Boolean"}, nil
		}
		if expression.Left != nil && len(expression.Arguments) == 1 &&
			(expression.Name == "=" || expression.Name == "/=") {
			left, err := compileBehaviorExpression(*expression.Left, bindings, states)
			if err != nil {
				return compiledBehaviorExpression{}, err
			}
			right, err := compileBehaviorExpression(expression.Arguments[0], bindings, states)
			if err != nil {
				return compiledBehaviorExpression{}, err
			}
			compatible := left.typeName == right.typeName ||
				sourceIntegerType(left.typeName) && sourceIntegerType(right.typeName)
			if !compatible {
				return compiledBehaviorExpression{}, typeError(expression.Position,
					"Equality.%s requires equal operand types, got %s and %s", expression.Name, left.typeName, right.typeName)
			}
			value := arch.EqualValues(left.value, right.value)
			if expression.Name == "/=" {
				value = arch.NotEqualValues(left.value, right.value)
			}
			return compiledBehaviorExpression{value: value, typeName: "Boolean"}, nil
		}
		if expression.Left != nil && keyword(expression.Name, "Not") && len(expression.Arguments) == 0 {
			receiver, err := compileBehaviorExpression(*expression.Left, bindings, states)
			if err != nil {
				return compiledBehaviorExpression{}, err
			}
			if receiver.typeName != "Boolean" {
				return compiledBehaviorExpression{}, typeError(expression.Position,
					"Boolean.Not receiver has type %s, want Boolean", receiver.typeName)
			}
			return compiledBehaviorExpression{value: arch.NotValue(receiver.value), typeName: "Boolean"}, nil
		}
		if expression.Left != nil && len(expression.Arguments) == 1 &&
			(keyword(expression.Name, "And") || keyword(expression.Name, "Or")) {
			left, err := compileBehaviorExpression(*expression.Left, bindings, states)
			if err != nil {
				return compiledBehaviorExpression{}, err
			}
			right, err := compileBehaviorExpression(expression.Arguments[0], bindings, states)
			if err != nil {
				return compiledBehaviorExpression{}, err
			}
			if left.typeName != "Boolean" || right.typeName != "Boolean" {
				return compiledBehaviorExpression{}, typeError(expression.Position,
					"Boolean.%s requires Boolean and Boolean, got %s and %s", expression.Name, left.typeName, right.typeName)
			}
			value := arch.AndValues(left.value, right.value)
			if keyword(expression.Name, "Or") {
				value = arch.OrValues(left.value, right.value)
			}
			return compiledBehaviorExpression{value: value, typeName: "Boolean"}, nil
		}
		if expression.Left != nil && keyword(expression.Name, "Xor") && len(expression.Arguments) == 1 {
			left, err := compileBehaviorExpression(*expression.Left, bindings, states)
			if err != nil {
				return compiledBehaviorExpression{}, err
			}
			right, err := compileBehaviorExpression(expression.Arguments[0], bindings, states)
			if err != nil {
				return compiledBehaviorExpression{}, err
			}
			if left.typeName != "Boolean" || right.typeName != "Boolean" {
				return compiledBehaviorExpression{}, typeError(expression.Position,
					"Boolean.Xor requires Boolean and Boolean, got %s and %s", left.typeName, right.typeName)
			}
			return compiledBehaviorExpression{value: arch.XorValues(left.value, right.value), typeName: "Boolean"}, nil
		}
		if expression.Left != nil && (expression.Name == "+" || expression.Name == "-") && len(expression.Arguments) == 0 {
			receiver, err := compileBehaviorExpression(*expression.Left, bindings, states)
			if err != nil {
				return compiledBehaviorExpression{}, err
			}
			if !sourceIntegerType(receiver.typeName) && receiver.typeName != "Float" {
				return compiledBehaviorExpression{}, typeError(expression.Position,
					"Numeric.%s receiver has type %s, want Integer or Float", expression.Name, receiver.typeName)
			}
			resultType := "Integer"
			if receiver.typeName == "Float" {
				resultType = "Float"
			}
			value := arch.PositiveValue(receiver.value)
			if expression.Name == "-" {
				value = arch.NegateValue(receiver.value)
			}
			return compiledBehaviorExpression{value: value, typeName: resultType}, nil
		}
		if expression.Left != nil && len(expression.Arguments) == 1 &&
			(expression.Name == "+" || expression.Name == "-" || expression.Name == "*") {
			left, err := compileBehaviorExpression(*expression.Left, bindings, states)
			if err != nil {
				return compiledBehaviorExpression{}, err
			}
			right, err := compileBehaviorExpression(expression.Arguments[0], bindings, states)
			if err != nil {
				return compiledBehaviorExpression{}, err
			}
			integerOperands := sourceIntegerType(left.typeName) && sourceIntegerType(right.typeName)
			floatOperands := left.typeName == "Float" && right.typeName == "Float"
			if !integerOperands && !floatOperands {
				return compiledBehaviorExpression{}, typeError(expression.Position,
					"Numeric.%s is not defined for %s and %s", expression.Name, left.typeName, right.typeName)
			}
			operators := map[string]func(arch.RuleValue, arch.RuleValue) arch.RuleValue{
				"+": arch.AddValues, "-": arch.SubtractValues, "*": arch.MultiplyValues,
			}
			resultType := "Integer"
			if floatOperands {
				resultType = "Float"
			}
			return compiledBehaviorExpression{value: operators[expression.Name](left.value, right.value), typeName: resultType}, nil
		}
		if expression.Left != nil && keyword(expression.Name, "Float") && len(expression.Arguments) == 0 {
			receiver, err := compileBehaviorExpression(*expression.Left, bindings, states)
			if err != nil {
				return compiledBehaviorExpression{}, err
			}
			if !sourceIntegerType(receiver.typeName) {
				return compiledBehaviorExpression{}, typeError(expression.Position,
					"Integer.Float receiver has type %s, want Integer", receiver.typeName)
			}
			return compiledBehaviorExpression{value: arch.IntegerFloatValue(receiver.value), typeName: "Float"}, nil
		}
		if expression.Left != nil && len(expression.Arguments) == 0 &&
			(keyword(expression.Name, "Pred") || keyword(expression.Name, "Succ")) {
			receiver, err := compileBehaviorExpression(*expression.Left, bindings, states)
			if err != nil {
				return compiledBehaviorExpression{}, err
			}
			if !sourceIntegerType(receiver.typeName) {
				return compiledBehaviorExpression{}, typeError(expression.Position,
					"Integer.%s receiver has type %s, want Integer", expression.Name, receiver.typeName)
			}
			value := arch.IntegerPredValue(receiver.value)
			if keyword(expression.Name, "Succ") {
				value = arch.IntegerSuccValue(receiver.value)
			}
			return compiledBehaviorExpression{value: value, typeName: "Integer"}, nil
		}
		if expression.Left != nil && keyword(expression.Name, "Abs") && len(expression.Arguments) == 0 {
			receiver, err := compileBehaviorExpression(*expression.Left, bindings, states)
			if err != nil {
				return compiledBehaviorExpression{}, err
			}
			if !sourceIntegerType(receiver.typeName) && receiver.typeName != "Float" {
				return compiledBehaviorExpression{}, typeError(expression.Position,
					"Numeric.Abs receiver has type %s, want Integer or Float", receiver.typeName)
			}
			resultType := "Integer"
			if receiver.typeName == "Float" {
				resultType = "Float"
			}
			return compiledBehaviorExpression{value: arch.AbsValue(receiver.value), typeName: resultType}, nil
		}
		if expression.Left != nil && keyword(expression.Name, "Floor") && len(expression.Arguments) == 0 {
			receiver, err := compileBehaviorExpression(*expression.Left, bindings, states)
			if err != nil {
				return compiledBehaviorExpression{}, err
			}
			if receiver.typeName != "Float" {
				return compiledBehaviorExpression{}, typeError(expression.Position,
					"Float.Floor receiver has type %s, want Float", receiver.typeName)
			}
			return compiledBehaviorExpression{value: arch.FloatFloorValue(receiver.value), typeName: "Integer"}, nil
		}
		if expression.Left != nil && keyword(expression.Name, "Slice") && len(expression.Arguments) == 2 {
			receiver, err := compileBehaviorExpression(*expression.Left, bindings, states)
			if err != nil {
				return compiledBehaviorExpression{}, err
			}
			if receiver.typeName != "String" {
				return compiledBehaviorExpression{}, typeError(expression.Position,
					"String Slice receiver has type %s, want String", receiver.typeName)
			}
			bounds := make([]compiledBehaviorExpression, 2)
			for index := range expression.Arguments {
				bound, err := compileBehaviorExpression(expression.Arguments[index], bindings, states)
				if err != nil {
					return compiledBehaviorExpression{}, err
				}
				if !sourceIntegerType(bound.typeName) {
					return compiledBehaviorExpression{}, typeError(expression.Position,
						"String Slice bound %d has type %s, want Integer", index+1, bound.typeName)
				}
				bounds[index] = bound
			}
			return compiledBehaviorExpression{
				value: arch.StringSliceValue(receiver.value, bounds[0].value, bounds[1].value), typeName: "String",
			}, nil
		}
		if expression.Left != nil && keyword(expression.Name, "[]") && len(expression.Arguments) == 1 {
			receiver, err := compileBehaviorExpression(*expression.Left, bindings, states)
			if err != nil {
				return compiledBehaviorExpression{}, err
			}
			if receiver.typeName != "String" {
				return compiledBehaviorExpression{}, typeError(expression.Position,
					"String index receiver has type %s, want String", receiver.typeName)
			}
			position, err := compileBehaviorExpression(expression.Arguments[0], bindings, states)
			if err != nil {
				return compiledBehaviorExpression{}, err
			}
			if !sourceIntegerType(position.typeName) {
				return compiledBehaviorExpression{}, typeError(expression.Position,
					"String index has type %s, want Integer", position.typeName)
			}
			return compiledBehaviorExpression{value: arch.StringIndexValue(receiver.value, position.value), typeName: "Character"}, nil
		}
		if expression.Left != nil && len(expression.Arguments) == 1 &&
			(keyword(expression.Name, "Append") || keyword(expression.Name, "Prepend")) {
			receiver, err := compileBehaviorExpression(*expression.Left, bindings, states)
			if err != nil {
				return compiledBehaviorExpression{}, err
			}
			if receiver.typeName != "String" {
				return compiledBehaviorExpression{}, typeError(expression.Position,
					"String.%s receiver has type %s, want String", expression.Name, receiver.typeName)
			}
			argument, err := compileBehaviorExpression(expression.Arguments[0], bindings, states)
			if err != nil {
				return compiledBehaviorExpression{}, err
			}
			if argument.typeName != "Character" {
				return compiledBehaviorExpression{}, typeError(expression.Position,
					"String.%s argument has type %s, want Character", expression.Name, argument.typeName)
			}
			if keyword(expression.Name, "Append") {
				return compiledBehaviorExpression{value: arch.StringAppendValue(receiver.value, argument.value), typeName: "String"}, nil
			}
			return compiledBehaviorExpression{value: arch.StringPrependValue(receiver.value, argument.value), typeName: "String"}, nil
		}
		if expression.Left != nil && len(expression.Arguments) == 0 &&
			(keyword(expression.Name, "Is_Null") || keyword(expression.Name, "Length")) {
			receiver, err := compileBehaviorExpression(*expression.Left, bindings, states)
			if err != nil {
				return compiledBehaviorExpression{}, err
			}
			if receiver.typeName != "String" {
				return compiledBehaviorExpression{}, typeError(expression.Position,
					"String.%s receiver has type %s, want String", expression.Name, receiver.typeName)
			}
			if keyword(expression.Name, "Is_Null") {
				return compiledBehaviorExpression{value: arch.StringIsNullValue(receiver.value), typeName: "Boolean"}, nil
			}
			return compiledBehaviorExpression{value: arch.StringLengthValue(receiver.value), typeName: "Integer"}, nil
		}
		if expression.Left != nil && keyword(expression.Name, "Code") && len(expression.Arguments) == 0 {
			receiver, err := compileBehaviorExpression(*expression.Left, bindings, states)
			if err != nil {
				return compiledBehaviorExpression{}, err
			}
			if receiver.typeName != "Character" {
				return compiledBehaviorExpression{}, typeError(expression.Position,
					"Character.Code receiver has type %s, want Character", receiver.typeName)
			}
			return compiledBehaviorExpression{value: arch.CharacterCodeValue(receiver.value), typeName: "Integer"}, nil
		}
		if expression.Left == nil && keyword(expression.Name, "Code_To_Char") && len(expression.Arguments) == 1 {
			argument, err := compileBehaviorExpression(expression.Arguments[0], bindings, states)
			if err != nil {
				return compiledBehaviorExpression{}, err
			}
			if !sourceIntegerType(argument.typeName) {
				return compiledBehaviorExpression{}, typeError(expression.Position,
					"Code_To_Char argument has type %s, want Integer", argument.typeName)
			}
			return compiledBehaviorExpression{value: arch.CodeToCharacterValue(argument.value), typeName: "Character"}, nil
		}
		return compiledBehaviorExpression{}, typeError(expression.Position,
			"unsupported closed behavior expression call %s", behaviorExpressionKey(expression))
	case ExpressionUnary:
		if expression.Left == nil {
			return compiledBehaviorExpression{}, typeError(expression.Position, "unary behavior expression has a missing operand")
		}
		operand, err := compileBehaviorExpression(*expression.Left, bindings, states)
		if err != nil {
			return compiledBehaviorExpression{}, err
		}
		switch strings.ToLower(expression.Operator) {
		case "+":
			if !sourceIntegerType(operand.typeName) && operand.typeName != "Float" {
				return compiledBehaviorExpression{}, typeError(expression.Position, "unary '+' requires Integer or Float, got %s", operand.typeName)
			}
			resultType := "Integer"
			if operand.typeName == "Float" {
				resultType = "Float"
			}
			return compiledBehaviorExpression{value: arch.PositiveValue(operand.value), typeName: resultType}, nil
		case "-":
			if !sourceIntegerType(operand.typeName) && operand.typeName != "Float" {
				return compiledBehaviorExpression{}, typeError(expression.Position, "unary '-' requires Integer or Float, got %s", operand.typeName)
			}
			resultType := "Integer"
			if operand.typeName == "Float" {
				resultType = "Float"
			}
			return compiledBehaviorExpression{value: arch.NegateValue(operand.value), typeName: resultType}, nil
		case "abs":
			if !sourceIntegerType(operand.typeName) && operand.typeName != "Float" {
				return compiledBehaviorExpression{}, typeError(expression.Position, "'abs' requires Integer or Float, got %s", operand.typeName)
			}
			resultType := "Integer"
			if operand.typeName == "Float" {
				resultType = "Float"
			}
			return compiledBehaviorExpression{value: arch.AbsValue(operand.value), typeName: resultType}, nil
		case "not":
			if operand.typeName != "Boolean" {
				return compiledBehaviorExpression{}, typeError(expression.Position, "'not' requires Boolean, got %s", operand.typeName)
			}
			return compiledBehaviorExpression{value: arch.NotValue(operand.value), typeName: "Boolean"}, nil
		default:
			return compiledBehaviorExpression{}, typeError(expression.Position, "unsupported unary behavior expression operator %q", expression.Operator)
		}
	case ExpressionBinary:
		if expression.Left == nil || expression.Right == nil {
			return compiledBehaviorExpression{}, typeError(expression.Position, "binary behavior expression has a missing operand")
		}
		left, err := compileBehaviorExpression(*expression.Left, bindings, states)
		if err != nil {
			return compiledBehaviorExpression{}, err
		}
		right, err := compileBehaviorExpression(*expression.Right, bindings, states)
		if err != nil {
			return compiledBehaviorExpression{}, err
		}
		var value arch.RuleValue
		resultType := ""
		switch strings.ToLower(expression.Operator) {
		case "&":
			switch {
			case left.typeName == "String" && right.typeName == "String":
				value = arch.StringConcatenateValues(left.value, right.value)
				resultType = "String"
			case left.typeName == "String" && right.typeName == "Character":
				value = arch.StringAppendValue(left.value, right.value)
				resultType = "String"
			case left.typeName == "Character" && right.typeName == "String":
				value = arch.StringPrependValue(right.value, left.value)
				resultType = "String"
			}
		case "+":
			if sourceIntegerType(left.typeName) && sourceIntegerType(right.typeName) {
				value = arch.AddValues(left.value, right.value)
				resultType = "Integer"
			} else if left.typeName == "Float" && right.typeName == "Float" {
				value = arch.AddValues(left.value, right.value)
				resultType = "Float"
			}
		case "-":
			if sourceIntegerType(left.typeName) && sourceIntegerType(right.typeName) {
				value = arch.SubtractValues(left.value, right.value)
				resultType = "Integer"
			} else if left.typeName == "Float" && right.typeName == "Float" {
				value = arch.SubtractValues(left.value, right.value)
				resultType = "Float"
			}
		case "*":
			if sourceIntegerType(left.typeName) && sourceIntegerType(right.typeName) {
				value = arch.MultiplyValues(left.value, right.value)
				resultType = "Integer"
			} else if left.typeName == "Float" && right.typeName == "Float" {
				value = arch.MultiplyValues(left.value, right.value)
				resultType = "Float"
			}
		case "/":
			if sourceIntegerType(left.typeName) && sourceIntegerType(right.typeName) {
				value = arch.DivideValues(left.value, right.value)
				resultType = "Integer"
			} else if left.typeName == "Float" && right.typeName == "Float" {
				value = arch.DivideValues(left.value, right.value)
				resultType = "Float"
			}
		case "=", "/=":
			compatible := sourceIntegerType(left.typeName) && sourceIntegerType(right.typeName) ||
				left.typeName == right.typeName && sourceClosedScalarType(left.typeName)
			if !compatible {
				break
			}
			if expression.Operator == "=" {
				value = arch.EqualValues(left.value, right.value)
			} else {
				value = arch.NotEqualValues(left.value, right.value)
			}
			resultType = "Boolean"
		case "<", "<=", ">", ">=":
			integerOperands := sourceIntegerType(left.typeName) && sourceIntegerType(right.typeName)
			floatOperands := left.typeName == "Float" && right.typeName == "Float"
			characterOperands := left.typeName == "Character" && right.typeName == "Character"
			if !integerOperands && !floatOperands && !characterOperands {
				break
			}
			switch expression.Operator {
			case "<":
				value = arch.LessValues(left.value, right.value)
			case "<=":
				value = arch.LessOrEqualValues(left.value, right.value)
			case ">":
				value = arch.GreaterValues(left.value, right.value)
			case ">=":
				value = arch.GreaterOrEqualValues(left.value, right.value)
			}
			resultType = "Boolean"
		case "and", "or", "xor", "nand", "nor", "andthen", "orelse":
			if left.typeName != "Boolean" || right.typeName != "Boolean" {
				break
			}
			if strings.EqualFold(expression.Operator, "and") {
				value = arch.AndValues(left.value, right.value)
			} else if strings.EqualFold(expression.Operator, "or") {
				value = arch.OrValues(left.value, right.value)
			} else if strings.EqualFold(expression.Operator, "xor") {
				value = arch.XorValues(left.value, right.value)
			} else if strings.EqualFold(expression.Operator, "nand") {
				value = arch.NandValues(left.value, right.value)
			} else if strings.EqualFold(expression.Operator, "nor") {
				value = arch.NorValues(left.value, right.value)
			} else if strings.EqualFold(expression.Operator, "andthen") {
				value = arch.AndThenValues(left.value, right.value)
			} else {
				value = arch.OrElseValues(left.value, right.value)
			}
			resultType = "Boolean"
		default:
			return compiledBehaviorExpression{}, typeError(expression.Position, "unsupported behavior expression operator %q", expression.Operator)
		}
		if resultType == "" {
			return compiledBehaviorExpression{}, typeError(expression.Position, "operator %q is not defined for %s and %s", expression.Operator, left.typeName, right.typeName)
		}
		return compiledBehaviorExpression{value: value, typeName: resultType}, nil
	case ExpressionConditional:
		if expression.Left == nil || expression.Right == nil || len(expression.Arguments) != 1 {
			return compiledBehaviorExpression{}, typeError(expression.Position, "if-expression is incomplete")
		}
		condition, err := compileBehaviorExpression(*expression.Left, bindings, states)
		if err != nil {
			return compiledBehaviorExpression{}, err
		}
		if condition.typeName != "Boolean" {
			return compiledBehaviorExpression{}, typeError(expression.Position,
				"if-expression condition has type %s, want Boolean", condition.typeName)
		}
		thenValue, err := compileBehaviorExpression(*expression.Right, bindings, states)
		if err != nil {
			return compiledBehaviorExpression{}, err
		}
		elseValue, err := compileBehaviorExpression(expression.Arguments[0], bindings, states)
		if err != nil {
			return compiledBehaviorExpression{}, err
		}
		resultType, ok := sourceConditionalResultType(thenValue.typeName, elseValue.typeName)
		if !ok {
			return compiledBehaviorExpression{}, typeError(expression.Position,
				"if-expression branches have incompatible types %s and %s", thenValue.typeName, elseValue.typeName)
		}
		return compiledBehaviorExpression{
			value: arch.IfValue(condition.value, thenValue.value, elseValue.value), typeName: resultType,
		}, nil
	default:
		return compiledBehaviorExpression{}, typeError(expression.Position, "unsupported behavior expression kind %q", expression.Kind)
	}
}

func lowerCompoundActionConnection(
	architecture *arch.Architecture,
	connection ConnectionDecl,
	targetInterface InterfaceDecl,
	componentInterfaces map[string]InterfaceDecl,
	componentSpellings map[string]string,
	seenConnections map[string]bool,
	owner string,
) error {
	if connection.Connector == ConnectBasic {
		return typeError(connection.Position, "compound source patterns require '=>' or '||>'; basic 'to' preserves one event identity")
	}
	targetAction, targetActionExists := findAction(targetInterface, connection.Target.Action)
	targetFunctions := findFunctions(targetInterface, connection.Target.Action)
	if targetActionExists && len(targetFunctions) != 0 {
		return typeError(connection.Target.Position, "connection target %s.%s is ambiguous between an action and a function", connection.Target.Component, connection.Target.Action)
	}
	if !targetActionExists {
		if len(targetFunctions) != 0 {
			return typeError(connection.Target.Position, "compound source patterns cannot target function %s.%s", connection.Target.Component, connection.Target.Action)
		}
		return typeError(connection.Target.Position, "target action %s.%s is not declared", connection.Target.Component, connection.Target.Action)
	}
	targetMode := ActionIn
	targetDirection := "in"
	if connection.Target.Component == "" {
		targetMode = ActionOut
		targetDirection = "out"
	}
	if targetAction.Mode != targetMode {
		return typeError(connection.Target.Position, "connection target %s.%s is not an %s action", connection.Target.Component, targetAction.Name, targetDirection)
	}

	bindings, err := compilePatternBindings(connection.Placeholders, "connection")
	if err != nil {
		return err
	}
	bound := make(map[string]bool, len(bindings))
	trigger, err := compileSourcePattern(*connection.SourcePattern, bindings, bound, "connection", func(event BehaviorEventDecl) (ActionDecl, string, error) {
		componentKey := folded(event.Component)
		interfaceDeclaration, ok := componentInterfaces[componentKey]
		if !ok {
			return ActionDecl{}, "", typeError(event.Position, "connection pattern component %q is not declared", event.Component)
		}
		action, ok := findAction(interfaceDeclaration, event.Name)
		if !ok {
			if len(findFunctions(interfaceDeclaration, event.Name)) != 0 {
				return ActionDecl{}, "", typeError(event.Position, "compound connection patterns over function call/return events are outside the current source subset")
			}
			return ActionDecl{}, "", typeError(event.Position, "connection pattern action %s.%s is not declared", event.Component, event.Name)
		}
		sourceMode := ActionOut
		sourceDirection := "out"
		if event.Component == "" {
			sourceMode = ActionIn
			sourceDirection = "in"
		}
		if action.Mode != sourceMode {
			return ActionDecl{}, "", typeError(event.Position, "connection pattern action %s.%s is not an %s action", event.Component, action.Name, sourceDirection)
		}
		return action, componentSpellings[componentKey], nil
	})
	if err != nil {
		return err
	}
	trigger, err = compileUniversalQualifications(trigger, connection.Placeholders, bound, "connection")
	if err != nil {
		return err
	}
	for _, placeholder := range connection.Placeholders {
		if !bound[folded(placeholder.Name)] {
			return typeError(placeholder.Position, "connection placeholder %s is never bound by the source pattern", patternPlaceholderDisplay(placeholder))
		}
	}
	trigger, err = compileConnectionGuard(trigger, connection, bindings, "connection")
	if err != nil {
		return err
	}
	empty, err := pattern.CanMatchEmpty(trigger)
	if err != nil {
		return typeError(connection.Position, "compound connection trigger: %v", err)
	}
	if empty {
		return typeError(connection.Position, "compound connection trigger can match an empty computation")
	}
	targetExpressions := actionRefArgumentExpressions(connection.Target)
	if len(targetExpressions) != len(targetAction.Parameters) {
		return typeError(connection.Target.Position, "target action %s.%s has %d parameters but the result pattern supplies %d arguments",
			connection.Target.Component, targetAction.Name, len(targetAction.Parameters), len(targetExpressions))
	}
	outputParameters, err := compileConnectionTargetExpressions(
		connection.Target, targetAction, bindings, "compound connection result",
	)
	if err != nil {
		return err
	}
	semanticKey := connectionSemanticKey(connection)
	if seenConnections[semanticKey] {
		return typeError(connection.Position, "duplicate connection %s", semanticKey)
	}
	seenConnections[semanticKey] = true
	targetComponent := componentSpellings[folded(connection.Target.Component)]
	connectionID := "rpd:" + semanticKey
	if owner != arch.ArchitectureInterfaceID {
		connectionID = "rpd:architecture:" + folded(owner) + ":" + semanticKey
	}
	builder := arch.Connect("*", targetComponent).
		WithinArchitecture(owner).
		IdentifiedBy(connectionID).
		On(trigger)
	if len(outputParameters) == 0 {
		builder.Send(targetAction.Name)
	} else {
		builder.SendParameters(targetAction.Name, outputParameters...)
	}
	if connection.Connector == ConnectPipe {
		builder.Pipe()
	} else {
		builder.Agent()
	}
	if err := architecture.AddConnection(builder.Build()); err != nil {
		return typeError(connection.Position, "%v", err)
	}
	return nil
}

func compileModuleConnections(
	module ModuleDecl,
	interfaceDecl InterfaceDecl,
	objectBindings map[string]behaviorBinding,
	typeElaborator *sourceTypeElaborator,
) ([]compiledModuleConnection, error) {
	connections, err := elaborateClosedConnectionGenerators(
		module.Connections, module.ConnectionGenerators, "module connection generator", objectBindings, typeElaborator,
	)
	if err != nil {
		return nil, err
	}
	sort.Slice(connections, func(i, j int) bool {
		return connectionSemanticKey(connections[i]) < connectionSemanticKey(connections[j])
	})
	result := make([]compiledModuleConnection, 0, len(connections))
	seen := make(map[string]bool, len(connections))
	for _, connection := range connections {
		semanticKey := connectionSemanticKey(connection)
		if seen[semanticKey] {
			return nil, typeError(connection.Position, "duplicate module connection %s", semanticKey)
		}
		seen[semanticKey] = true
		if hasUniversalPlaceholder(connection.Placeholders) {
			return nil, typeError(connection.Position,
				"universal module-connection sources require the remaining qualified result-generator semantics")
		}
		if connection.SourcePattern == nil {
			return nil, typeError(connection.Position,
				"module connection has no source pattern")
		}
		sourcePattern := connectionSourcePattern(connection)
		compound := sourcePattern.Kind != BehaviorBasicPattern
		dynamicSource := sourcePattern.Kind == BehaviorBasicPattern && sourcePattern.Event.ComponentPlaceholder
		if compound && connection.Connector == ConnectBasic {
			return nil, typeError(connection.Position,
				"compound module connection source patterns require '=>' or '||>'; basic 'to' preserves one event identity")
		}
		if !compound && connection.Source.Component != "" && !dynamicSource {
			return nil, typeError(connection.Source.Position,
				"module connection source %s.%s must be an unqualified action of Self or function of Self in the current source subset",
				connection.Source.Component, connection.Source.Action)
		}
		if sourcePattern.Kind == BehaviorBasicPattern && sourcePattern.Event.Attribute != "" {
			return nil, typeError(sourcePattern.Event.Position,
				"function Call/Return attribute module-connection triggers are outside the current source subset")
		}
		if connection.Target.Component != "" {
			return nil, typeError(connection.Target.Position,
				"module connection target %s.%s must be an unqualified action of Self or function of Self in the current source subset",
				connection.Target.Component, connection.Target.Action)
		}
		var sourceAction ActionDecl
		sourceExists := false
		var sourceFunctions []FunctionDecl
		if !compound && !dynamicSource {
			sourceAction, sourceExists = findAction(interfaceDecl, connection.Source.Action)
			sourceFunctions = findFunctions(interfaceDecl, connection.Source.Action)
		}
		targetAction, targetExists := findAction(interfaceDecl, connection.Target.Action)
		targetFunctions := findFunctions(interfaceDecl, connection.Target.Action)
		if connection.Constituent == ConnectionActionConstituent {
			sourceFunctions, targetFunctions = nil, nil
		} else if connection.Constituent == ConnectionFunctionConstituent {
			sourceExists, targetExists = false, false
		}
		if sourceExists && len(sourceFunctions) != 0 {
			return nil, typeError(connection.Source.Position,
				"module connection source %q is ambiguous between an action and a function", connection.Source.Action)
		}
		if targetExists && len(targetFunctions) != 0 {
			return nil, typeError(connection.Target.Position,
				"module connection target %q is ambiguous between an action and a function", connection.Target.Action)
		}
		if len(sourceFunctions) != 0 || len(targetFunctions) != 0 {
			if compound {
				return nil, typeError(connection.Target.Position,
					"compound module connection patterns cannot target function %q", connection.Target.Action)
			}
			compiled, err := compileModuleFunctionConnection(
				connection, semanticKey, sourceFunctions, targetFunctions,
			)
			if err != nil {
				return nil, err
			}
			result = append(result, compiled)
			continue
		}
		if !compound && !dynamicSource && !sourceExists {
			return nil, typeError(connection.Source.Position,
				"module connection source action %q is not declared by return interface %q",
				connection.Source.Action, interfaceDecl.Name)
		}
		if !targetExists {
			return nil, typeError(connection.Target.Position,
				"module connection target action %q is not declared by return interface %q",
				connection.Target.Action, interfaceDecl.Name)
		}
		if targetAction.Mode != ActionOut && targetAction.Mode != ActionPrivate {
			return nil, typeError(connection.Target.Position,
				"module connection target %q is not an out action or private action", targetAction.Name)
		}

		bindings, err := compileConnectionBindings(connection.Placeholders, "module connection")
		if err != nil {
			return nil, err
		}
		bound := make(map[string]bool, len(bindings))
		trigger, err := compileSourcePattern(sourcePattern, bindings, bound, "module connection", func(event BehaviorEventDecl) (ActionDecl, string, error) {
			if event.ComponentPlaceholder {
				binding, exists := bindings[folded(event.Component)]
				if !exists {
					return ActionDecl{}, "", typeError(event.Position,
						"module connection qualifier ?%s is not declared", event.Component)
				}
				_, sourceInterface, resolveErr := typeElaborator.interfaceDeclaration(event.Position, binding.typeName)
				if resolveErr != nil {
					return ActionDecl{}, "", resolveErr
				}
				action, exists := findAction(sourceInterface, event.Name)
				if !exists {
					return ActionDecl{}, "", typeError(event.Position,
						"module connection qualifier ?%s of type %s has no action %q",
						event.Component, sourceInterface.Name, event.Name)
				}
				if action.Mode != ActionOut {
					return ActionDecl{}, "", typeError(event.Position,
						"module-qualified source ?%s.%s is not an out action broadcast",
						event.Component, action.Name)
				}
				return action, "", nil
			}
			if event.Component != "" {
				return ActionDecl{}, "", typeError(event.Position,
					"module connection source action %s.%s must be unqualified", event.Component, event.Name)
			}
			action, actionExists := findAction(interfaceDecl, event.Name)
			functions := findFunctions(interfaceDecl, event.Name)
			if actionExists && len(functions) != 0 {
				return ActionDecl{}, "", typeError(event.Position,
					"module connection source %q is ambiguous between an action and a function", event.Name)
			}
			if !actionExists {
				if len(functions) != 0 {
					return ActionDecl{}, "", typeError(event.Position,
						"compound module connection patterns over function call/return events are outside the current source subset")
				}
				return ActionDecl{}, "", typeError(event.Position,
					"module connection source action %q is not declared by return interface %q",
					event.Name, interfaceDecl.Name)
			}
			return action, "", nil
		})
		if err != nil {
			return nil, err
		}
		trigger, err = compileUniversalQualifications(
			trigger, connection.Placeholders, bound, "module connection",
		)
		if err != nil {
			return nil, err
		}
		for _, placeholder := range connection.Placeholders {
			if !bound[folded(placeholder.Name)] {
				return nil, typeError(placeholder.Position,
					"placeholder ?%s is never bound by the module source pattern", placeholder.Name)
			}
		}
		trigger, err = compileConnectionGuard(trigger, connection, bindings, "module connection")
		if err != nil {
			return nil, err
		}
		var outputParameters []arch.ConnectionParameter
		targetExpressions := actionRefArgumentExpressions(connection.Target)
		if !compound && !dynamicSource && len(targetExpressions) == 0 {
			if !compatiblePassthrough(sourceAction, targetAction) {
				if len(sourcePattern.Event.Arguments) == 0 && len(connection.Placeholders) == 0 {
					return nil, typeError(connection.Position,
						"module connection %s %s %s requires identical parameter names and types when argument lists are omitted",
						sourceAction.Name, connection.Connector, targetAction.Name)
				}
				return nil, typeError(connection.Target.Position,
					"an omitted module target argument list requires identical source and target parameter names and types")
			}
		} else {
			if len(targetExpressions) != len(targetAction.Parameters) {
				return nil, typeError(connection.Target.Position,
					"module target action %s has %d parameters but the result supplies %d arguments",
					targetAction.Name, len(targetAction.Parameters), len(targetExpressions))
			}
			outputParameters, err = compileConnectionTargetExpressions(
				connection.Target, targetAction, bindings, "module connection target",
			)
			if err != nil {
				return nil, err
			}
		}
		if compound {
			empty, err := pattern.CanMatchEmpty(trigger)
			if err != nil {
				return nil, typeError(connection.Position, "compound module connection trigger: %v", err)
			}
			if empty {
				return nil, typeError(connection.Position, "compound module connection trigger can match an empty computation")
			}
		}
		kind := arch.BasicConnection
		if connection.Connector == ConnectPipe {
			kind = arch.PipeConnection
		} else if connection.Connector == ConnectAgent {
			kind = arch.AgentConnection
		}
		result = append(result, compiledModuleConnection{
			position: connection.Position, semanticKey: semanticKey, kind: kind, trigger: trigger,
			targetAction: targetAction.Name, outputParameters: outputParameters,
		})
	}
	return result, nil
}

func compileModuleFunctionConnection(
	connection ConnectionDecl,
	semanticKey string,
	sourceFunctions, targetFunctions []FunctionDecl,
) (compiledModuleConnection, error) {
	if connection.Connector != ConnectBasic {
		return compiledModuleConnection{}, typeError(connection.Position,
			"module function connection %s uses %s; the current static subset requires 'to'",
			semanticKey, connection.Connector)
	}
	if len(connection.Placeholders) != 0 ||
		len(connectionSourcePattern(connection).Event.Arguments) != 0 ||
		len(actionRefArgumentExpressions(connection.Target)) != 0 {
		return compiledModuleConnection{}, typeError(connection.Position,
			"module function connections do not accept pattern placeholders or call argument lists")
	}
	required := make([]FunctionDecl, 0, len(sourceFunctions))
	for _, function := range sourceFunctions {
		if function.Mode == FunctionRequires {
			required = append(required, function)
		}
	}
	if len(required) == 0 {
		return compiledModuleConnection{}, typeError(connection.Source.Position,
			"module function connection source %q is not a requires function", connection.Source.Action)
	}
	provided := make([]FunctionDecl, 0, len(targetFunctions))
	for _, function := range targetFunctions {
		if function.Mode == FunctionProvides {
			provided = append(provided, function)
		}
	}
	if len(provided) == 0 {
		return compiledModuleConnection{}, typeError(connection.Target.Position,
			"module function connection target %q is not a provides function", connection.Target.Action)
	}
	for _, requirement := range required {
		matches := 0
		for _, offer := range provided {
			if sourceFunctionConnectionCompatible(requirement, offer) {
				matches++
			}
		}
		if matches != 1 {
			return compiledModuleConnection{}, typeError(connection.Position,
				"module required function signature %s has %d type-compatible provided signatures at %s, want 1",
				requirement.Name, matches, connection.Target.Action)
		}
	}
	return compiledModuleConnection{
		position: connection.Position, semanticKey: semanticKey, function: true,
		requiredFunction: required[0].Name,
		providedFunction: provided[0].Name,
	}, nil
}

func elaborateClosedConnectionGenerators(
	connections []ConnectionDecl,
	generators []ConnectionGeneratorDecl,
	context string,
	bindings map[string]behaviorBinding,
	typeElaborator *sourceTypeElaborator,
) ([]ConnectionDecl, error) {
	initial := make(map[string]behaviorBinding, len(bindings))
	for name, binding := range bindings {
		initial[name] = binding
	}
	return elaborateClosedConnectionGeneratorsWithBindings(
		connections, generators, context, initial, typeElaborator,
	)
}

func elaborateClosedComponentArrays(
	declarations []ComponentDecl,
	bindings map[string]behaviorBinding,
	typeElaborator *sourceTypeElaborator,
) ([]ComponentDecl, error) {
	result := make([]ComponentDecl, 0, len(declarations))
	seen := make(map[string]ComponentDecl, len(declarations))
	for _, declaration := range declarations {
		key := folded(declaration.Name)
		if prior, exists := seen[key]; exists {
			return nil, typeError(declaration.Position,
				"duplicate component or component array %q (previous spelling %q)", declaration.Name, prior.Name)
		}
		seen[key] = declaration
		if !declaration.IntegerArray {
			result = append(result, declaration)
			continue
		}
		if declaration.Module != "" {
			return nil, typeError(declaration.Position,
				"component array %q has a denotation expression outside the current finite source subset", declaration.Name)
		}
		if declaration.IndexType != "" {
			domain, err := typeElaborator.finiteIntegerRange(
				declaration.Position, declaration.IndexType, "component array "+strconv.Quote(declaration.Name),
			)
			if err != nil {
				return nil, err
			}
			declaration.FirstIndex = domain.FirstIndex
			declaration.LastIndex = domain.LastIndex
		} else if declaration.RangeFirst.Kind != "" || declaration.RangeLast.Kind != "" {
			if declaration.RangeFirst.Kind == "" || declaration.RangeLast.Kind == "" {
				return nil, typeError(declaration.Position,
					"component array %q has an incomplete range expression", declaration.Name)
			}
			first, err := evaluateConnectionGeneratorInteger(
				declaration.RangeFirst, bindings, "component array "+strconv.Quote(declaration.Name)+" lower bound",
			)
			if err != nil {
				return nil, err
			}
			last, err := evaluateConnectionGeneratorInteger(
				declaration.RangeLast, bindings, "component array "+strconv.Quote(declaration.Name)+" upper bound",
			)
			if err != nil {
				return nil, err
			}
			declaration.FirstIndex = first
			declaration.LastIndex = last
		}
		if declaration.LastIndex < declaration.FirstIndex {
			continue
		}
		difference := uint64(declaration.LastIndex) - uint64(declaration.FirstIndex)
		if difference >= gorapide.MaxRapideComponentArrayCardinality {
			return nil, typeError(declaration.Position,
				"component array %q range %d..%d exceeds deterministic cardinality limit %d",
				declaration.Name, declaration.FirstIndex, declaration.LastIndex,
				gorapide.MaxRapideComponentArrayCardinality)
		}
		for index := declaration.FirstIndex; ; index++ {
			element := declaration
			element.Name = componentArrayElementSpelling(declaration.Name, index)
			element.IntegerArray = false
			element.IndexType = ""
			element.RangeFirst = ExpressionDecl{}
			element.RangeLast = ExpressionDecl{}
			element.FirstIndex = 0
			element.LastIndex = 0
			result = append(result, element)
			if index == declaration.LastIndex {
				break
			}
		}
	}
	return result, nil
}

func componentArrayElementSpelling(name string, index int64) string {
	return name + "[" + strconv.FormatInt(index, 10) + "]"
}

func elaborateClosedConnectionGeneratorsWithBindings(
	connections []ConnectionDecl,
	generators []ConnectionGeneratorDecl,
	context string,
	bindings map[string]behaviorBinding,
	typeElaborator *sourceTypeElaborator,
) ([]ConnectionDecl, error) {
	result := make([]ConnectionDecl, 0, len(connections))
	for _, connection := range connections {
		if connection.TargetGenerator != nil {
			generated, err := elaborateConnectionSetGenerator(
				connection, *connection.TargetGenerator, bindings, context+" result set", typeElaborator,
			)
			if err != nil {
				return nil, err
			}
			result = append(result, generated...)
			continue
		}
		substituted, err := substituteConnectionGeneratorBindings(connection, bindings, context)
		if err != nil {
			return nil, err
		}
		result = append(result, substituted)
	}
	for _, generator := range generators {
		switch generator.Kind {
		case ConnectionGeneratorIf:
			selected, err := evaluateConnectionGeneratorBoolean(generator.Condition, bindings, context)
			if err != nil {
				return nil, err
			}
			if !selected {
				continue
			}
			generated, err := elaborateClosedConnectionGeneratorsWithBindings(
				generator.Connections, generator.Generators, context, bindings, typeElaborator,
			)
			if err != nil {
				return nil, err
			}
			result = append(result, generated...)
		case ConnectionGeneratorForRange:
			iteratorKey := folded(generator.Iterator)
			if iteratorKey == "" {
				return nil, typeError(generator.Position, "%s has an empty iterator name", context)
			}
			if _, exists := bindings[iteratorKey]; exists {
				return nil, typeError(generator.Position,
					"%s iterator %q conflicts with an enclosing object or iterator", context, generator.Iterator)
			}
			first, err := evaluateConnectionGeneratorInteger(generator.RangeFirst, bindings, context+" lower bound")
			if err != nil {
				return nil, err
			}
			last, err := evaluateConnectionGeneratorInteger(generator.RangeLast, bindings, context+" upper bound")
			if err != nil {
				return nil, err
			}
			if err := validateConnectionGeneratorIntegerDomain(
				generator.Position, generator.Iterator, generator.IteratorType,
				first, last, context, typeElaborator,
			); err != nil {
				return nil, err
			}
			if last < first {
				continue
			}
			if uint64(last)-uint64(first) >= gorapide.MaxRapideIntegerServiceSetCardinality {
				return nil, typeError(generator.Position,
					"%s range %d..%d exceeds deterministic cardinality limit %d",
					context, first, last, gorapide.MaxRapideIntegerServiceSetCardinality)
			}
			for value := first; ; value++ {
				local := make(map[string]behaviorBinding, len(bindings)+1)
				for name, binding := range bindings {
					local[name] = binding
				}
				constant := arch.LiteralValue(value)
				local[iteratorKey] = behaviorBinding{
					name: generator.Iterator, typeName: "Integer", constant: &constant,
				}
				generated, err := elaborateClosedConnectionGeneratorsWithBindings(
					generator.Connections, generator.Generators, context, local, typeElaborator,
				)
				if err != nil {
					return nil, err
				}
				result = append(result, generated...)
				if value == last {
					break
				}
			}
		default:
			return nil, typeError(generator.Position, "%s has unsupported generation scheme %q", context, generator.Kind)
		}
	}
	return result, nil
}

func elaborateConnectionSetGenerator(
	template ConnectionDecl,
	generator ConnectionSetGeneratorDecl,
	bindings map[string]behaviorBinding,
	context string,
	typeElaborator *sourceTypeElaborator,
) ([]ConnectionDecl, error) {
	emit := func(local map[string]behaviorBinding) ([]ConnectionDecl, error) {
		result := make([]ConnectionDecl, 0, len(generator.Targets))
		for _, target := range generator.Targets {
			connection := template
			connection.Target = target
			connection.TargetGenerator = nil
			substituted, err := substituteConnectionGeneratorBindings(connection, local, context)
			if err != nil {
				return nil, err
			}
			result = append(result, substituted)
		}
		for _, nested := range generator.Generators {
			generated, err := elaborateConnectionSetGenerator(template, nested, local, context, typeElaborator)
			if err != nil {
				return nil, err
			}
			result = append(result, generated...)
		}
		return result, nil
	}

	switch generator.Kind {
	case ConnectionGeneratorIf:
		selected, err := evaluateConnectionGeneratorBoolean(generator.Condition, bindings, context)
		if err != nil {
			return nil, err
		}
		if !selected {
			return nil, nil
		}
		return emit(bindings)
	case ConnectionGeneratorForRange:
		iteratorKey := folded(generator.Iterator)
		if iteratorKey == "" {
			return nil, typeError(generator.Position, "%s has an empty iterator name", context)
		}
		if _, exists := bindings[iteratorKey]; exists {
			return nil, typeError(generator.Position,
				"%s iterator %q conflicts with an enclosing object or iterator", context, generator.Iterator)
		}
		first, err := evaluateConnectionGeneratorInteger(generator.RangeFirst, bindings, context+" lower bound")
		if err != nil {
			return nil, err
		}
		last, err := evaluateConnectionGeneratorInteger(generator.RangeLast, bindings, context+" upper bound")
		if err != nil {
			return nil, err
		}
		if err := validateConnectionGeneratorIntegerDomain(
			generator.Position, generator.Iterator, generator.IteratorType,
			first, last, context, typeElaborator,
		); err != nil {
			return nil, err
		}
		if last < first {
			return nil, nil
		}
		if uint64(last)-uint64(first) >= gorapide.MaxRapideIntegerServiceSetCardinality {
			return nil, typeError(generator.Position,
				"%s range %d..%d exceeds deterministic cardinality limit %d",
				context, first, last, gorapide.MaxRapideIntegerServiceSetCardinality)
		}
		result := make([]ConnectionDecl, 0)
		for value := first; ; value++ {
			local := make(map[string]behaviorBinding, len(bindings)+1)
			for name, binding := range bindings {
				local[name] = binding
			}
			constant := arch.LiteralValue(value)
			local[iteratorKey] = behaviorBinding{
				name: generator.Iterator, typeName: "Integer", constant: &constant,
			}
			generated, err := emit(local)
			if err != nil {
				return nil, err
			}
			result = append(result, generated...)
			if value == last {
				break
			}
		}
		return result, nil
	default:
		return nil, typeError(generator.Position, "%s has unsupported generation scheme %q", context, generator.Kind)
	}
}

func validateConnectionGeneratorIntegerDomain(
	position Position,
	iterator string,
	iteratorType string,
	first int64,
	last int64,
	context string,
	typeElaborator *sourceTypeElaborator,
) error {
	if keyword(iteratorType, "Integer") {
		return nil
	}
	if _, predefined := predefinedTypes[folded(iteratorType)]; predefined {
		return typeError(position,
			"%s iterator %q has type %s, want Integer in the current finite range subset",
			context, iterator, iteratorType)
	}
	domain, err := typeElaborator.finiteIntegerRange(position, iteratorType, context+" iterator "+strconv.Quote(iterator))
	if err != nil {
		return err
	}
	if last < first {
		return nil
	}
	if first < domain.FirstIndex || first > domain.LastIndex ||
		last < domain.FirstIndex || last > domain.LastIndex {
		return typeError(position,
			"%s iterator %q range %d..%d is outside declared type %s range %d..%d",
			context, iterator, first, last, domain.Name, domain.FirstIndex, domain.LastIndex)
	}
	return nil
}

func evaluateConnectionGeneratorBoolean(
	expression ExpressionDecl,
	bindings map[string]behaviorBinding,
	context string,
) (bool, error) {
	compiled, err := compileBehaviorExpression(expression, bindings, map[string]behaviorBinding{})
	if err != nil {
		return false, typeError(expression.Position,
			"%s condition must be a closed deterministic Boolean expression: %v", context, err)
	}
	if compiled.typeName != "Boolean" {
		return false, typeError(expression.Position,
			"%s condition has type %s, want Boolean", context, compiled.typeName)
	}
	value, evaluatedType, err := arch.EvaluateConstant(compiled.value)
	if err != nil {
		return false, typeError(expression.Position,
			"%s condition is not a closed deterministic constant: %v", context, err)
	}
	selected, ok := value.(bool)
	if !ok || evaluatedType != "Boolean" {
		return false, typeError(expression.Position,
			"%s condition evaluated as %s, want Boolean", context, evaluatedType)
	}
	return selected, nil
}

func evaluateConnectionGeneratorInteger(
	expression ExpressionDecl,
	bindings map[string]behaviorBinding,
	context string,
) (int64, error) {
	compiled, err := compileBehaviorExpression(expression, bindings, map[string]behaviorBinding{})
	if err != nil {
		return 0, typeError(expression.Position,
			"%s must be a closed deterministic Integer expression: %v", context, err)
	}
	if compiled.typeName != "Integer" {
		return 0, typeError(expression.Position, "%s has type %s, want Integer", context, compiled.typeName)
	}
	value, evaluatedType, err := arch.EvaluateConstant(compiled.value)
	if err != nil {
		return 0, typeError(expression.Position,
			"%s is not a closed deterministic constant: %v", context, err)
	}
	integer, ok := value.(int64)
	if !ok || evaluatedType != "Integer" {
		return 0, typeError(expression.Position, "%s evaluated as %s, want Integer", context, evaluatedType)
	}
	return integer, nil
}

func substituteConnectionGeneratorBindings(
	connection ConnectionDecl,
	bindings map[string]behaviorBinding,
	context string,
) (ConnectionDecl, error) {
	result := connection
	var err error
	result.Source, err = substituteConnectionActionRef(connection.Source, bindings, context)
	if err != nil {
		return ConnectionDecl{}, err
	}
	result.Target, err = substituteConnectionActionRef(connection.Target, bindings, context)
	if err != nil {
		return ConnectionDecl{}, err
	}
	if connection.SourcePattern != nil {
		patternCopy, patternErr := substituteConnectionPatternBindings(*connection.SourcePattern, bindings, context)
		if patternErr != nil {
			return ConnectionDecl{}, patternErr
		}
		result.SourcePattern = &patternCopy
		if patternCopy.Kind == BehaviorBasicPattern {
			result.Source.Action = patternCopy.Event.Name
			result.Source.Path = append([]QualifiedMemberSegmentDecl(nil), patternCopy.Event.Path...)
		}
	}
	if connection.Guard != nil {
		guard := substituteGeneratorExpression(*connection.Guard, bindings)
		result.Guard = &guard
	}
	return result, nil
}

func substituteConnectionActionRef(
	reference ActionRef,
	bindings map[string]behaviorBinding,
	context string,
) (ActionRef, error) {
	result := reference
	component, err := substituteComponentArraySelection(
		reference.Component, reference.ComponentIndex, bindings, context,
	)
	if err != nil {
		return ActionRef{}, err
	}
	result.Component = component
	result.ComponentIndex = nil
	path, err := substituteQualifiedMemberPath(reference.Path, bindings, context)
	if err != nil {
		return ActionRef{}, err
	}
	result.Path = path
	if len(path) != 0 {
		result.Action = qualifiedMemberPathSpelling(path)
	}
	if len(reference.ArgumentExpressions) != 0 {
		result.ArgumentExpressions = append([]ExpressionDecl(nil), reference.ArgumentExpressions...)
		result.ArgumentFormals = append([]string(nil), reference.ArgumentFormals...)
		for index := range result.ArgumentExpressions {
			result.ArgumentExpressions[index] = substituteGeneratorExpression(result.ArgumentExpressions[index], bindings)
		}
		result.Arguments = positionalPlaceholderRefs(result.ArgumentExpressions)
	}
	return result, nil
}

func substituteConnectionPatternBindings(
	source BehaviorPatternDecl,
	bindings map[string]behaviorBinding,
	context string,
) (BehaviorPatternDecl, error) {
	result := cloneBehaviorPatternDeclaration(source)
	switch result.Kind {
	case BehaviorBasicPattern:
		component, err := substituteComponentArraySelection(
			result.Event.Component, result.Event.ComponentIndex, bindings, context,
		)
		if err != nil {
			return BehaviorPatternDecl{}, err
		}
		result.Event.Component = component
		result.Event.ComponentIndex = nil
		result.Event.Path, err = substituteQualifiedMemberPath(result.Event.Path, bindings, context)
		if err != nil {
			return BehaviorPatternDecl{}, err
		}
		if len(result.Event.Path) != 0 {
			result.Event.Name = qualifiedMemberPathSpelling(result.Event.Path)
		}
		for index := range result.Event.Arguments {
			result.Event.Arguments[index].Actual = substituteGeneratorExpression(
				result.Event.Arguments[index].Actual, bindings,
			)
		}
	case BehaviorBinaryPattern:
		if result.Left != nil {
			left, err := substituteConnectionPatternBindings(*result.Left, bindings, context)
			if err != nil {
				return BehaviorPatternDecl{}, err
			}
			result.Left = &left
		}
		if result.Right != nil {
			right, err := substituteConnectionPatternBindings(*result.Right, bindings, context)
			if err != nil {
				return BehaviorPatternDecl{}, err
			}
			result.Right = &right
		}
	case BehaviorIterationPattern:
		if result.Inner != nil {
			inner, err := substituteConnectionPatternBindings(*result.Inner, bindings, context)
			if err != nil {
				return BehaviorPatternDecl{}, err
			}
			result.Inner = &inner
		}
	}
	return result, nil
}

func substituteComponentArraySelection(
	component string,
	index *ExpressionDecl,
	bindings map[string]behaviorBinding,
	context string,
) (string, error) {
	if index == nil {
		return component, nil
	}
	if component == "" {
		return "", typeError(index.Position, "%s has a component-array index without an array name", context)
	}
	value, err := evaluateConnectionGeneratorInteger(*index, bindings, context+" component-array index")
	if err != nil {
		return "", err
	}
	return componentArrayElementSpelling(component, value), nil
}

func substituteQualifiedMemberPath(
	path []QualifiedMemberSegmentDecl,
	bindings map[string]behaviorBinding,
	context string,
) ([]QualifiedMemberSegmentDecl, error) {
	result := append([]QualifiedMemberSegmentDecl(nil), path...)
	for index := range result {
		if result[index].Index == nil {
			continue
		}
		value, err := evaluateConnectionGeneratorInteger(*result[index].Index, bindings, context+" service index")
		if err != nil {
			return nil, err
		}
		expression := ExpressionDecl{Position: result[index].Index.Position, Kind: ExpressionInteger, Integer: value}
		result[index].Index = &expression
	}
	return result, nil
}

func substituteGeneratorExpression(expression ExpressionDecl, bindings map[string]behaviorBinding) ExpressionDecl {
	expression.StringCodes = append([]int64(nil), expression.StringCodes...)
	if expression.Kind == ExpressionName {
		if binding, exists := bindings[folded(expression.Name)]; exists && binding.constant != nil {
			value, typeName, err := arch.EvaluateConstant(*binding.constant)
			if err == nil && typeName == "Integer" {
				if integer, ok := value.(int64); ok {
					expression.Kind = ExpressionInteger
					expression.Name = ""
					expression.Integer = integer
				}
			}
		}
	}
	if expression.Left != nil {
		left := substituteGeneratorExpression(*expression.Left, bindings)
		expression.Left = &left
	}
	if expression.Right != nil {
		right := substituteGeneratorExpression(*expression.Right, bindings)
		expression.Right = &right
	}
	expression.Arguments = append([]ExpressionDecl(nil), expression.Arguments...)
	for index, argument := range expression.Arguments {
		expression.Arguments[index] = substituteGeneratorExpression(argument, bindings)
	}
	expression.RecordFields = append([]RecordFieldExpressionDecl(nil), expression.RecordFields...)
	for index, field := range expression.RecordFields {
		expression.RecordFields[index] = field
		expression.RecordFields[index].Value = substituteGeneratorExpression(field.Value, bindings)
	}
	return expression
}

func findAction(declaration InterfaceDecl, name string) (ActionDecl, bool) {
	for _, action := range declaration.Actions {
		if keyword(action.Name, name) {
			return action, true
		}
	}
	return ActionDecl{}, false
}

func findException(declaration InterfaceDecl, name string) (ExceptionDecl, bool) {
	for _, exception := range declaration.Exceptions {
		if keyword(exception.Name, name) {
			return exception, true
		}
	}
	return ExceptionDecl{}, false
}

// findExceptionReference resolves direct names, the exact enclosing-module
// scope-name slice, and same-interface object selection. Scope::N consults only
// declarations owned by that named module region. Self and supported
// function-local New values are typed modules of the generated module's return
// interface, so O.N denotes that interface's exact constituent; neither form
// creates an instance-specific or string-matched exception.
func findExceptionReference(
	declaration InterfaceDecl,
	name string,
	bindings map[string]behaviorBinding,
) (ExceptionDecl, bool) {
	if strings.Contains(name, "::") {
		parts := strings.Split(name, "::")
		if len(parts) < 2 {
			return ExceptionDecl{}, false
		}
		scopePath := parts[:len(parts)-1]
		member := parts[len(parts)-1]
		var result ExceptionDecl
		matches := 0
		for _, scope := range declaration.ExceptionScopes {
			if !scopePathSuffixMatches(scope.Path, scopePath) {
				continue
			}
			scoped := declaration
			scoped.Exceptions = scope.Exceptions
			if exception, exists := findException(scoped, member); exists {
				result = exception
				matches++
			}
		}
		return result, matches == 1
	}
	if separator := strings.IndexByte(name, '.'); separator >= 0 {
		qualifier := name[:separator]
		binding, bound := bindings[folded(qualifier)]
		if keyword(qualifier, "Self") ||
			(bound && binding.moduleValue && keyword(binding.typeName, declaration.Name)) {
			selected := declaration
			selected.Exceptions = declaration.SelectedExceptions
			return findException(selected, name[separator+1:])
		}
	}
	return findException(declaration, name)
}

func scopePathSuffixMatches(full, suffix []string) bool {
	if len(suffix) == 0 || len(suffix) > len(full) {
		return false
	}
	offset := len(full) - len(suffix)
	for index := range suffix {
		if !keyword(full[offset+index], suffix[index]) {
			return false
		}
	}
	return true
}

func findFunctions(declaration InterfaceDecl, name string) []FunctionDecl {
	result := make([]FunctionDecl, 0)
	for _, function := range declaration.Functions {
		if keyword(function.Name, name) {
			result = append(result, function)
		}
	}
	return result
}

// findPatternAction resolves an explicitly declared action or the two
// event-valued predefined attributes of one function. The latter are actions
// only for pattern matching; they do not provide a second way to invoke the
// synchronous function.
func findPatternAction(declaration InterfaceDecl, event BehaviorEventDecl) (ActionDecl, bool, error) {
	if event.Attribute == "" {
		action, ok := findAction(declaration, event.Name)
		return action, ok, nil
	}
	attribute := ""
	switch {
	case keyword(event.Attribute, "Call"):
		attribute = "Call"
	case keyword(event.Attribute, "Return"):
		attribute = "Return"
	default:
		return ActionDecl{}, false, fmt.Errorf(
			"attribute %s'%s is not an event-valued predefined function attribute; module lifecycle attributes require the deferred lifecycle model",
			event.Name, event.Attribute,
		)
	}
	functions := findFunctions(declaration, event.Name)
	if len(functions) == 0 {
		return ActionDecl{}, false, fmt.Errorf("function %q is not declared", event.Name)
	}
	if len(functions) != 1 {
		return ActionDecl{}, false, fmt.Errorf(
			"function attribute %s'%s resolves to %d overloads; overloaded attribute-event selection is outside the current source subset",
			event.Name, attribute, len(functions),
		)
	}
	function := functions[0]
	if attribute == "Return" && function.ReturnType == "" {
		return ActionDecl{}, false, fmt.Errorf(
			"void function attribute %s'Return requires Stanford's implicit Return : Root parameter, but canonical Root values are not yet represented",
			function.Name,
		)
	}
	parameters := append([]ParameterDecl(nil), function.Parameters...)
	if attribute == "Return" {
		parameters = append(parameters, ParameterDecl{
			Position: function.Position, Name: "Return", Type: function.ReturnType,
			TypeExpression: TypeExpressionDecl{
				Position: function.Position, Kind: TypeExpressionName, Name: function.ReturnType,
			},
		})
	}
	mode := ActionPrivate
	if function.Mode != FunctionPrivate {
		if function.Mode == FunctionRequires && attribute == "Call" ||
			function.Mode == FunctionProvides && attribute == "Return" {
			mode = ActionOut
		} else {
			mode = ActionIn
		}
	}
	return ActionDecl{
		Position: function.Position, Mode: mode,
		Name: function.Name + "'" + attribute, Parameters: parameters,
	}, true, nil
}

func elaborateArchitectureServiceConnections(
	declarations []ConnectionDecl,
	componentInterfaces map[string]InterfaceDecl,
	componentServices map[string]map[string]sourceServiceInstance,
	types *sourceTypeElaborator,
) ([]ConnectionDecl, error) {
	result := make([]ConnectionDecl, 0, len(declarations))
	for _, declaration := range declarations {
		sourceInterface, sourceComponentExists := componentInterfaces[folded(declaration.Source.Component)]
		targetInterface, targetComponentExists := componentInterfaces[folded(declaration.Target.Component)]
		if !sourceComponentExists || !targetComponentExists {
			// Preserve the existing component diagnostic and its source position.
			result = append(result, declaration)
			continue
		}
		sourcePath, sourceReferenceClean := sourceServiceConnectionReference(declaration)
		targetPath := folded(declaration.Target.Action)
		sourceService, sourceIsService := componentServices[folded(declaration.Source.Component)][sourcePath]
		targetService, targetIsService := componentServices[folded(declaration.Target.Component)][targetPath]
		sourceMemberName := declaration.Source.Action
		if declaration.SourcePattern != nil && declaration.SourcePattern.Kind == BehaviorBasicPattern {
			sourceMemberName = declaration.SourcePattern.Event.Name
		}
		sourceIsDirect := false
		if _, exists := findAction(sourceInterface, sourceMemberName); exists {
			sourceIsDirect = true
		}
		if len(findFunctions(sourceInterface, sourceMemberName)) != 0 {
			sourceIsDirect = true
		}
		targetIsDirect := false
		if _, exists := findAction(targetInterface, declaration.Target.Action); exists {
			targetIsDirect = true
		}
		if len(findFunctions(targetInterface, declaration.Target.Action)) != 0 {
			targetIsDirect = true
		}
		fallbackTarget, hasIndexedActionFallback := indexedTargetActionFallback(declaration.Target)
		if hasIndexedActionFallback {
			if _, exists := findAction(targetInterface, fallbackTarget.Action); exists {
				targetIsDirect = true
			}
			if len(findFunctions(targetInterface, fallbackTarget.Action)) != 0 {
				targetIsDirect = true
			}
		}
		if sourceIsService && sourceIsDirect {
			return nil, typeError(declaration.Source.Position,
				"connection source %s.%s is ambiguous between a service and an action or function",
				declaration.Source.Component, sourcePath)
		}
		if targetIsService && targetIsDirect {
			return nil, typeError(declaration.Target.Position,
				"connection target %s.%s is ambiguous between a service and an action or function",
				declaration.Target.Component, targetPath)
		}
		if !sourceIsService && !targetIsService {
			if hasIndexedActionFallback {
				if _, exists := findAction(targetInterface, fallbackTarget.Action); exists {
					declaration.Target = fallbackTarget
				} else if len(findFunctions(targetInterface, fallbackTarget.Action)) != 0 {
					declaration.Target = fallbackTarget
				}
			}
			result = append(result, declaration)
			continue
		}
		if !sourceIsService || !targetIsService {
			return nil, typeError(declaration.Position,
				"service connection requires service names on both sides; got %s.%s to %s.%s",
				declaration.Source.Component, sourcePath,
				declaration.Target.Component, targetPath)
		}
		if !sourceReferenceClean || len(declaration.Placeholders) != 0 ||
			len(declaration.Source.Arguments) != 0 || len(actionRefArgumentExpressions(declaration.Target)) != 0 {
			return nil, typeError(declaration.Position,
				"service connections do not accept action arguments or placeholder declarations")
		}
		if declaration.Connector != ConnectBasic {
			return nil, typeError(declaration.Position,
				"service connection %s.%s %s %s.%s must use Stanford basic 'to' elaboration",
				declaration.Source.Component, sourcePath, declaration.Connector,
				declaration.Target.Component, targetPath)
		}
		equal, err := gorapide.RapideTypesEqual(sourceService.targetType, targetService.targetType)
		if err != nil {
			return nil, typeError(declaration.Position, "service connection type comparison: %v", err)
		}
		architectureBoundaryPair := (declaration.Source.Component == "") != (declaration.Target.Component == "")
		if architectureBoundaryPair {
			if !equal || sourceService.dual != targetService.dual {
				return nil, typeError(declaration.Position,
					"service connection %s.%s to %s.%s requires exact same service types when exactly one endpoint is the architecture interface",
					declaration.Source.Component, sourcePath,
					declaration.Target.Component, targetPath)
			}
		} else if !equal || sourceService.dual == targetService.dual {
			return nil, typeError(declaration.Position,
				"service connection %s.%s to %s.%s requires exact dual service types",
				declaration.Source.Component, sourcePath,
				declaration.Target.Component, targetPath)
		}
		generated, err := types.elaborateServiceConnection(
			declaration, sourceService, targetService,
		)
		if err != nil {
			return nil, err
		}
		result = append(result, generated...)
	}
	return result, nil
}

func indexedTargetActionFallback(reference ActionRef) (ActionRef, bool) {
	if len(reference.Path) == 0 || len(reference.ArgumentExpressions) != 0 {
		return ActionRef{}, false
	}
	path := append([]QualifiedMemberSegmentDecl(nil), reference.Path...)
	last := &path[len(path)-1]
	if last.Index == nil {
		return ActionRef{}, false
	}
	argument := *last.Index
	last.Index = nil
	result := reference
	result.Path = path
	result.Action = qualifiedMemberPathSpelling(path)
	result.ArgumentExpressions = []ExpressionDecl{argument}
	result.Arguments = positionalPlaceholderRefs(result.ArgumentExpressions)
	return result, true
}

func sourceServiceConnectionReference(connection ConnectionDecl) (string, bool) {
	if connection.SourcePattern == nil || connection.SourcePattern.Kind != BehaviorBasicPattern {
		return folded(connection.Source.Action), false
	}
	event := connection.SourcePattern.Event
	if len(event.Arguments) == 0 {
		return folded(event.Name), true
	}
	if len(event.Arguments) != 1 || event.Arguments[0].Formal != "" {
		return folded(event.Name), false
	}
	index, ok := closedSignedInteger(event.Arguments[0].Actual)
	if !ok {
		return folded(event.Name), false
	}
	return folded(event.Name) + "(" + strconv.FormatInt(index, 10) + ")", true
}

func closedSignedInteger(expression ExpressionDecl) (int64, bool) {
	switch expression.Kind {
	case ExpressionInteger:
		return expression.Integer, true
	case ExpressionUnary:
		if expression.Operator != "-" || expression.Left == nil || expression.Left.Kind != ExpressionInteger {
			return 0, false
		}
		if expression.Left.Integer == math.MinInt64 {
			return 0, false
		}
		return -expression.Left.Integer, true
	default:
		return 0, false
	}
}

func (current *sourceTypeElaborator) elaborateServiceConnection(
	source ConnectionDecl,
	left sourceServiceInstance,
	right sourceServiceInstance,
) ([]ConnectionDecl, error) {
	if left.target.Name == "" || right.target.Name == "" {
		return nil, typeError(source.Position,
			"service connection through a structural type-constructor application is outside the current execution adapter")
	}
	if err := current.validateServiceConnectionConstituents(left.target); err != nil {
		return nil, typeError(source.Position, "service connection %s.%s: %v",
			source.Source.Component, left.path, err)
	}
	if err := current.validateServiceConnectionConstituents(right.target); err != nil {
		return nil, typeError(source.Position, "service connection %s.%s: %v",
			source.Target.Component, right.path, err)
	}
	leftExpansion, err := current.executionInterfaceExpansion(left.target)
	if err != nil {
		return nil, err
	}
	rightExpansion, err := current.executionInterfaceExpansion(right.target)
	if err != nil {
		return nil, err
	}
	if source.Guard != nil && (len(leftExpansion.functions) != 0 || len(rightExpansion.functions) != 0) {
		return nil, typeError(source.Guard.Position,
			"guarded service connections containing function objects require conditional object-alias semantics outside the current source subset")
	}
	leftActions := make(map[string]ActionDecl, len(leftExpansion.actions))
	rightActions := make(map[string]ActionDecl, len(rightExpansion.actions))
	for _, action := range leftExpansion.actions {
		leftActions[folded(action.Name)] = action
	}
	for _, action := range rightExpansion.actions {
		rightActions[folded(action.Name)] = action
	}
	actionNames := make([]string, 0, len(leftActions))
	for name := range leftActions {
		actionNames = append(actionNames, name)
	}
	sort.Strings(actionNames)
	result := make([]ConnectionDecl, 0, len(actionNames))
	for _, name := range actionNames {
		leftAction := leftActions[name]
		rightAction, exists := rightActions[name]
		if !exists || !compatiblePassthrough(leftAction, rightAction) {
			return nil, typeError(source.Position,
				"dual service connection action %q has no exact same-named signature", name)
		}
		leftMode := dualActionMode(leftAction.Mode, left.dual)
		rightMode := dualActionMode(rightAction.Mode, right.dual)
		// The enclosing architecture interface reverses connection polarity:
		// its in actions are triggers and its out actions are bodies. This is
		// why Stanford permits equal (rather than dual) service types when
		// exactly one service belongs to the architecture interface.
		if source.Source.Component == "" {
			leftMode = dualActionMode(leftMode, true)
		}
		if source.Target.Component == "" {
			rightMode = dualActionMode(rightMode, true)
		}
		var fromComponent, fromAction, toComponent, toAction string
		switch {
		case leftMode == ActionOut && rightMode == ActionIn:
			fromComponent, fromAction = source.Source.Component, left.path+"."+folded(leftAction.Name)
			toComponent, toAction = source.Target.Component, right.path+"."+folded(rightAction.Name)
		case rightMode == ActionOut && leftMode == ActionIn:
			fromComponent, fromAction = source.Target.Component, right.path+"."+folded(rightAction.Name)
			toComponent, toAction = source.Source.Component, left.path+"."+folded(leftAction.Name)
		default:
			return nil, typeError(source.Position,
				"dual service connection action %q does not produce one out-to-in pair", name)
		}
		result = append(result, basicGeneratedConnection(source.Position, source.Guard,
			fromComponent, fromAction, toComponent, toAction, ConnectionActionConstituent))
	}
	if missing := sortedMissingNames(leftActions, rightActions); len(missing) > 0 {
		return nil, typeError(source.Position,
			"dual service connection action %q exists only in the target service", missing[0])
	}

	leftFunctions := groupFunctionsByName(leftExpansion.functions)
	rightFunctions := groupFunctionsByName(rightExpansion.functions)
	functionNames := make([]string, 0, len(leftFunctions))
	for name := range leftFunctions {
		functionNames = append(functionNames, name)
	}
	sort.Strings(functionNames)
	for _, name := range functionNames {
		rightGroup, exists := rightFunctions[name]
		if !exists {
			return nil, typeError(source.Position,
				"dual service connection function %q exists only in the source service", name)
		}
		leftRequired, leftProvided := effectiveFunctionRegions(leftFunctions[name], left.dual)
		rightRequired, rightProvided := effectiveFunctionRegions(rightGroup, right.dual)
		// As for action constituents, the architecture's own returned service
		// reverses provides/requires polarity relative to directly visible
		// component and child services.
		if source.Source.Component == "" {
			leftRequired, leftProvided = leftProvided, leftRequired
		}
		if source.Target.Component == "" {
			rightRequired, rightProvided = rightProvided, rightRequired
		}
		if leftRequired && rightProvided {
			result = append(result, basicGeneratedConnection(source.Position, nil,
				source.Source.Component, left.path+"."+name,
				source.Target.Component, right.path+"."+name, ConnectionFunctionConstituent))
		}
		if rightRequired && leftProvided {
			result = append(result, basicGeneratedConnection(source.Position, nil,
				source.Target.Component, right.path+"."+name,
				source.Source.Component, left.path+"."+name, ConnectionFunctionConstituent))
		}
		if leftRequired != rightProvided || rightRequired != leftProvided {
			return nil, typeError(source.Position,
				"dual service connection function %q does not produce complementary requires/provides regions", name)
		}
	}
	if missing := sortedMissingNames(leftFunctions, rightFunctions); len(missing) > 0 {
		return nil, typeError(source.Position,
			"dual service connection function %q exists only in the target service", missing[0])
	}
	return result, nil
}

func (current *sourceTypeElaborator) validateServiceConnectionConstituents(
	declaration InterfaceDecl,
) error {
	if len(declaration.Objects) != 0 {
		return typeError(declaration.Objects[0].Position,
			"object constituent %q requires deterministic object-alias connection semantics",
			declaration.Objects[0].Name)
	}
	if len(declaration.ModuleGenerators) != 0 {
		return typeError(declaration.ModuleGenerators[0].Position,
			"module-generator constituent %q cannot be silently omitted from service connection elaboration",
			declaration.ModuleGenerators[0].Name)
	}
	for _, service := range declaration.Services {
		prefixes, err := sourceServicePrefixes(service)
		if err != nil {
			return err
		}
		if len(prefixes) == 0 {
			continue
		}
		expression := declaredTypeExpression(service.Position, service.Type, service.TypeExpression)
		targetName, named := typeExpressionNamedTarget(expression)
		if !named {
			return typeError(service.Position,
				"nested structural service target %s has no execution-facing object adapter",
				typeExpressionSpelling(expression))
		}
		_, target, err := current.interfaceDeclaration(service.Position, targetName)
		if err != nil {
			return err
		}
		if err := current.validateServiceConnectionConstituents(target); err != nil {
			return err
		}
	}
	return nil
}

func basicGeneratedConnection(
	position Position,
	guard *ExpressionDecl,
	fromComponent string,
	fromAction string,
	toComponent string,
	toAction string,
	constituent ConnectionConstituentKind,
) ConnectionDecl {
	event := BehaviorEventDecl{Position: position, Component: fromComponent, Name: fromAction}
	var guardCopy *ExpressionDecl
	if guard != nil {
		copy := *guard
		guardCopy = &copy
	}
	return ConnectionDecl{
		Position: position,
		Source:   ActionRef{Position: position, Component: fromComponent, Action: fromAction},
		SourcePattern: &BehaviorPatternDecl{
			Position: position, Kind: BehaviorBasicPattern, Event: event,
		},
		Guard:       guardCopy,
		Connector:   ConnectBasic,
		Target:      ActionRef{Position: position, Component: toComponent, Action: toAction},
		Constituent: constituent,
	}
}

func dualActionMode(mode ActionMode, dual bool) ActionMode {
	if !dual {
		return mode
	}
	if mode == ActionIn {
		return ActionOut
	}
	if mode == ActionOut {
		return ActionIn
	}
	return mode
}

func groupFunctionsByName(functions []FunctionDecl) map[string][]FunctionDecl {
	result := make(map[string][]FunctionDecl)
	for _, function := range functions {
		name := folded(function.Name)
		result[name] = append(result[name], function)
	}
	return result
}

func effectiveFunctionRegions(functions []FunctionDecl, dual bool) (requires bool, provides bool) {
	for _, function := range functions {
		mode := function.Mode
		if dual {
			if mode == FunctionProvides {
				mode = FunctionRequires
			} else if mode == FunctionRequires {
				mode = FunctionProvides
			}
		}
		requires = requires || mode == FunctionRequires
		provides = provides || mode == FunctionProvides
	}
	return requires, provides
}

func lowerFunctionConnection(
	architecture *arch.Architecture,
	connection ConnectionDecl,
	sourceFunctions, targetFunctions []FunctionDecl,
	sourceComponent, targetComponent string,
	seenConnections map[string]bool,
	owner string,
) error {
	if connection.Connector != ConnectBasic {
		return typeError(connection.Position, "function connection %s.%s %s %s.%s uses an unsupported connector; the current static subset requires 'to'",
			connection.Source.Component, connection.Source.Action, connection.Connector,
			connection.Target.Component, connection.Target.Action)
	}
	if len(connection.Placeholders) != 0 || len(connectionSourcePattern(connection).Event.Arguments) != 0 || len(actionRefArgumentExpressions(connection.Target)) != 0 {
		return typeError(connection.Position, "static function connections do not accept pattern placeholders or call argument lists")
	}
	sourceMode := FunctionRequires
	sourceRole := "requires"
	if sourceComponent == owner {
		sourceMode = FunctionProvides
		sourceRole = "provides"
	}
	targetMode := FunctionProvides
	targetRole := "provides"
	if targetComponent == owner {
		targetMode = FunctionRequires
		targetRole = "requires"
	}
	sources := make([]FunctionDecl, 0, len(sourceFunctions))
	for _, function := range sourceFunctions {
		if function.Mode == sourceMode {
			sources = append(sources, function)
		}
	}
	if len(sources) == 0 {
		return typeError(connection.Source.Position, "function connection source %s.%s is not a %s function",
			connection.Source.Component, connection.Source.Action, sourceRole)
	}
	targets := make([]FunctionDecl, 0, len(targetFunctions))
	for _, function := range targetFunctions {
		if function.Mode == targetMode {
			targets = append(targets, function)
		}
	}
	if len(targets) == 0 {
		return typeError(connection.Target.Position, "function connection target %s.%s is not a %s function",
			connection.Target.Component, connection.Target.Action, targetRole)
	}
	for _, source := range sources {
		matches := 0
		for _, target := range targets {
			if sourceFunctionConnectionCompatible(source, target) {
				matches++
			}
		}
		if matches != 1 {
			targetSignatureRole := "required"
			if targetMode == FunctionProvides {
				targetSignatureRole = "provided"
			}
			return typeError(connection.Position, "function source signature %s.%s has %d type-compatible %s signatures at %s.%s, want 1",
				connection.Source.Component, source.Name, matches, targetSignatureRole,
				connection.Target.Component, connection.Target.Action)
		}
	}
	semanticKey := connectionSemanticKey(connection)
	if seenConnections[semanticKey] {
		return typeError(connection.Position, "duplicate connection %s", semanticKey)
	}
	seenConnections[semanticKey] = true
	// Route the resolved declaration names, not the reference spelling. This is
	// required for case-insensitive source references and for service members,
	// whose rewrite has one canonical qualified execution name shared by every
	// overload.
	connectionID := "rpd:" + semanticKey
	if owner != arch.ArchitectureInterfaceID {
		connectionID = "rpd:architecture:" + folded(owner) + ":" + semanticKey
	}
	declaration := arch.ConnectFunction(sourceComponent, sources[0].Name, targetComponent, targets[0].Name).
		WithinArchitecture(owner).IdentifiedBy(connectionID).Build()
	if err := architecture.AddFunctionConnection(declaration); err != nil {
		return typeError(connection.Position, "%v", err)
	}
	return nil
}

func sourceFunctionConnectionCompatible(required, provided FunctionDecl) bool {
	requiredReturn := canonicalPredefinedType(required.ReturnType)
	providedReturn := canonicalPredefinedType(provided.ReturnType)
	if requiredReturn == "" || providedReturn == "" {
		if requiredReturn != providedReturn {
			return false
		}
	} else if !sourcePredefinedTypeAssignable(providedReturn, requiredReturn) {
		return false
	}
	shared := len(required.Parameters)
	if len(provided.Parameters) < shared {
		shared = len(provided.Parameters)
	}
	for index := 0; index < shared; index++ {
		requiredType := canonicalPredefinedType(required.Parameters[index].Type)
		providedType := canonicalPredefinedType(provided.Parameters[index].Type)
		if !sourcePredefinedTypeAssignable(requiredType, providedType) {
			return false
		}
	}
	for _, parameter := range provided.Parameters[shared:] {
		if parameter.Default == nil {
			return false
		}
	}
	return true
}

func sourcePredefinedTypeAssignable(source, target string) bool {
	source = canonicalPredefinedType(source)
	target = canonicalPredefinedType(target)
	if source == target {
		return true
	}
	switch target {
	case "Integer":
		return source == "Natural" || source == "Positive"
	case "Natural":
		return source == "Positive"
	default:
		return false
	}
}

func sourceIntegerType(typeName string) bool {
	return sourcePredefinedTypeAssignable(typeName, "Integer")
}

func sourceConditionalResultType(left, right string) (string, bool) {
	left = canonicalPredefinedType(left)
	right = canonicalPredefinedType(right)
	if sourcePredefinedTypeAssignable(left, right) {
		return right, true
	}
	if sourcePredefinedTypeAssignable(right, left) {
		return left, true
	}
	return "", false
}

func sourceClosedScalarType(typeName string) bool {
	typeName = canonicalPredefinedType(typeName)
	return typeName == "Triv" || typeName == "Boolean" || sourceIntegerType(typeName) || typeName == "Float" || typeName == "Character" || typeName == "String"
}

func sourceBehaviorExpressionAssignable(expression compiledBehaviorExpression, target string) bool {
	if sourcePredefinedTypeAssignable(expression.typeName, target) {
		return true
	}
	value, _, err := arch.EvaluateConstant(expression.value)
	return err == nil && gorapide.CanonicalValueMatchesPredefinedType(value, canonicalPredefinedType(target))
}

func compiledFunctionKey(function FunctionDecl, parameters []arch.ParamDecl, returnType string) string {
	var builder strings.Builder
	builder.WriteString(string(function.Mode))
	builder.WriteByte(':')
	builder.WriteString(folded(function.Name))
	for _, parameter := range parameters {
		builder.WriteByte('|')
		builder.WriteString(folded(parameter.Name))
		builder.WriteByte(':')
		builder.WriteString(folded(parameter.Type))
	}
	builder.WriteString("->")
	builder.WriteString(folded(returnType))
	return builder.String()
}

func interfaceNeedsStructuralDescriptor(declaration InterfaceDecl) bool {
	if len(declaration.Objects) != 0 || len(declaration.TypeNames) != 0 ||
		len(declaration.TypeConstructors) != 0 || len(declaration.ModuleGenerators) != 0 ||
		len(declaration.Services) != 0 {
		return true
	}
	for _, function := range declaration.Functions {
		if function.Mode == FunctionPrivate {
			return true
		}
		for _, parameter := range function.Parameters {
			if parameter.Default != nil {
				return true
			}
		}
	}
	return false
}

func compatiblePassthrough(source, target ActionDecl) bool {
	if len(source.Parameters) != len(target.Parameters) {
		return false
	}
	for i := range source.Parameters {
		if !keyword(source.Parameters[i].Name, target.Parameters[i].Name) ||
			!keyword(source.Parameters[i].Type, target.Parameters[i].Type) {
			return false
		}
	}
	return true
}

func connectionSemanticKey(connection ConnectionDecl) string {
	var builder strings.Builder
	if connection.Constituent != "" {
		builder.WriteString("service-")
		builder.WriteString(string(connection.Constituent))
		builder.WriteByte(':')
	}
	placeholderKeys := make([]string, 0, len(connection.Placeholders))
	for _, placeholder := range connection.Placeholders {
		placeholderKeys = append(placeholderKeys, patternPlaceholderSemanticKey(placeholder)+";")
	}
	sort.Strings(placeholderKeys)
	for _, placeholderKey := range placeholderKeys {
		builder.WriteString(placeholderKey)
	}
	if connection.SourcePattern != nil &&
		(connection.SourcePattern.Kind != BehaviorBasicPattern || behaviorPatternHasExtendedAssociations(*connection.SourcePattern)) {
		builder.WriteString(behaviorPatternKey(*connection.SourcePattern))
	} else {
		builder.WriteString(componentSelectionSemanticKey(connection.Source.Component, connection.Source.ComponentIndex) + "." + folded(connection.Source.Action))
		writeArguments(&builder, connection.Source.Arguments)
	}
	if connection.Guard != nil {
		builder.WriteString(":where:")
		builder.WriteString(behaviorExpressionKey(*connection.Guard))
	}
	builder.WriteString(string(connection.Connector))
	builder.WriteString(componentSelectionSemanticKey(connection.Target.Component, connection.Target.ComponentIndex) + "." + folded(connection.Target.Action))
	writeActionRefArguments(&builder, connection.Target)
	return builder.String()
}

func behaviorPatternHasExtendedAssociations(source BehaviorPatternDecl) bool {
	if source.Kind != BehaviorBasicPattern {
		return true
	}
	for _, association := range source.Event.Arguments {
		if association.Formal != "" || association.Actual.Kind != ExpressionPlaceholder {
			return true
		}
	}
	return false
}

func hasUniversalPlaceholder(placeholders []ParameterDecl) bool {
	for _, placeholder := range placeholders {
		if placeholder.Qualification == PlaceholderUniversal {
			return true
		}
	}
	return false
}

func connectionSourcePattern(connection ConnectionDecl) BehaviorPatternDecl {
	if connection.SourcePattern != nil {
		return *connection.SourcePattern
	}
	event := BehaviorEventDecl{
		Position: connection.Source.Position, Component: connection.Source.Component,
		ComponentIndex: cloneExpressionDeclarationPointer(connection.Source.ComponentIndex),
		Name:           connection.Source.Action,
	}
	for _, argument := range connection.Source.Arguments {
		event.Arguments = append(event.Arguments, PatternParameterAssociationDecl{
			Position: argument.Position,
			Actual:   ExpressionDecl{Position: argument.Position, Kind: ExpressionPlaceholder, Name: argument.Name},
		})
	}
	return BehaviorPatternDecl{Position: connection.Source.Position, Kind: BehaviorBasicPattern, Event: event}
}

func componentSelectionSemanticKey(component string, index *ExpressionDecl) string {
	result := folded(component)
	if index != nil {
		result += "[" + behaviorExpressionKey(*index) + "]"
	}
	return result
}

func compileConnectionBindings(placeholders []ParameterDecl, owner string) (map[string]behaviorBinding, error) {
	return compilePatternBindings(placeholders, owner)
}

func actionRefArgumentExpressions(reference ActionRef) []ExpressionDecl {
	if len(reference.ArgumentExpressions) != 0 {
		return append([]ExpressionDecl(nil), reference.ArgumentExpressions...)
	}
	result := make([]ExpressionDecl, 0, len(reference.Arguments))
	for _, argument := range reference.Arguments {
		result = append(result, ExpressionDecl{
			Position: argument.Position, Kind: ExpressionPlaceholder, Name: argument.Name,
		})
	}
	return result
}

func compileConnectionTargetExpressions(
	reference ActionRef,
	target ActionDecl,
	bindings map[string]behaviorBinding,
	context string,
) ([]arch.ConnectionParameter, error) {
	expressions := actionRefArgumentExpressions(reference)
	if len(expressions) != len(target.Parameters) {
		return nil, typeError(reference.Position, "%s action %s has %d parameters but supplies %d arguments",
			context, target.Name, len(target.Parameters), len(expressions))
	}
	ordered := make([]ExpressionDecl, len(target.Parameters))
	assigned := make([]bool, len(target.Parameters))
	nextPositional := 0
	for index, expression := range expressions {
		formal := ""
		if index < len(reference.ArgumentFormals) {
			formal = reference.ArgumentFormals[index]
		}
		selected := -1
		if formal == "" {
			for nextPositional < len(target.Parameters) && assigned[nextPositional] {
				nextPositional++
			}
			selected = nextPositional
			nextPositional++
		} else {
			for parameterIndex, parameter := range target.Parameters {
				if keyword(parameter.Name, formal) {
					selected = parameterIndex
					break
				}
			}
			if selected < 0 {
				return nil, typeError(expression.Position,
					"%s names undeclared formal parameter %q of action %s", context, formal, target.Name)
			}
		}
		if selected < 0 || selected >= len(target.Parameters) {
			return nil, typeError(expression.Position, "%s has too many positional arguments", context)
		}
		if assigned[selected] {
			return nil, typeError(expression.Position,
				"%s supplies target parameter %q more than once", context, target.Parameters[selected].Name)
		}
		assigned[selected] = true
		ordered[selected] = expression
	}
	result := make([]arch.ConnectionParameter, 0, len(expressions))
	for index, expression := range ordered {
		if !assigned[index] {
			return nil, typeError(reference.Position,
				"%s omits target parameter %q", context, target.Parameters[index].Name)
		}
		compiled, err := compileBehaviorExpression(expression, bindings, map[string]behaviorBinding{})
		if err != nil {
			return nil, typeError(expression.Position, "%s argument %d: %v", context, index+1, err)
		}
		expected, expectedPredefined := predefinedTypes[folded(target.Parameters[index].Type)]
		if !expectedPredefined {
			expected = target.Parameters[index].Type
		}
		compatible := sourceBehaviorExpressionAssignable(compiled, expected)
		if !expectedPredefined {
			compatible = keyword(compiled.typeName, target.Parameters[index].Type)
		}
		if !compatible {
			return nil, typeError(expression.Position,
				"%s argument %d has type %s but parameter %s has type %s",
				context, index+1, compiled.typeName, target.Parameters[index].Name, expected)
		}
		result = append(result, arch.ConnectionExpressionParam(target.Parameters[index].Name, compiled.value))
	}
	return result, nil
}

func compilePatternBindings(placeholders []ParameterDecl, owner string) (map[string]behaviorBinding, error) {
	result := make(map[string]behaviorBinding, len(placeholders))
	universalCount := 0
	for _, placeholder := range placeholders {
		key := folded(placeholder.Name)
		if result[key].name != "" {
			return nil, typeError(placeholder.Position, "duplicate %s placeholder %s", owner, patternPlaceholderDisplay(placeholder))
		}
		typeName, predefined := predefinedTypes[folded(placeholder.Type)]
		if !predefined {
			if placeholder.TypeExpression.Kind != TypeExpressionName || strings.TrimSpace(placeholder.Type) == "" {
				return nil, typeError(placeholder.Position, "%s placeholder %s has unsupported structural type %q", owner, patternPlaceholderDisplay(placeholder), placeholder.Type)
			}
			typeName = placeholder.Type
		}
		universal := placeholder.Qualification == PlaceholderUniversal
		if placeholder.Qualification != "" && placeholder.Qualification != PlaceholderExistential && !universal {
			return nil, typeError(placeholder.Position, "%s placeholder %q has unsupported qualification %q", owner, placeholder.Name, placeholder.Qualification)
		}
		if universal {
			universalCount++
			if universalCount > 1 {
				return nil, typeError(placeholder.Position,
					"%s currently supports one universal placeholder per qualification; multiple-universal nesting order remains source-ambiguous", owner)
			}
			if !predefined || typeName != "Integer" {
				return nil, typeError(placeholder.Position,
					"%s universal placeholder !%s requires Integer range, got %s", owner, placeholder.Name, typeName)
			}
		}
		result[key] = behaviorBinding{
			name: placeholder.Name, typeName: typeName, placeholder: true, universal: universal,
		}
	}
	return result, nil
}

func writeArguments(builder *strings.Builder, arguments []PlaceholderRef) {
	if len(arguments) == 0 {
		return
	}
	builder.WriteByte('(')
	for i, argument := range arguments {
		if i > 0 {
			builder.WriteByte(',')
		}
		builder.WriteByte('?')
		builder.WriteString(folded(argument.Name))
	}
	builder.WriteByte(')')
}

func writeActionRefArguments(builder *strings.Builder, reference ActionRef) {
	if len(reference.ArgumentExpressions) == 0 {
		writeArguments(builder, reference.Arguments)
		return
	}
	if len(reference.ArgumentFormals) == 0 {
		if placeholders := positionalPlaceholderRefs(reference.ArgumentExpressions); len(placeholders) == len(reference.ArgumentExpressions) {
			writeArguments(builder, placeholders)
			return
		}
	}
	type association struct {
		formal     string
		expression ExpressionDecl
	}
	associations := make([]association, len(reference.ArgumentExpressions))
	firstNamed := len(associations)
	for index, expression := range reference.ArgumentExpressions {
		formal := ""
		if index < len(reference.ArgumentFormals) {
			formal = folded(reference.ArgumentFormals[index])
		}
		associations[index] = association{formal: formal, expression: expression}
		if formal != "" && firstNamed == len(associations) {
			firstNamed = index
		}
	}
	if firstNamed < len(associations) {
		sort.Slice(associations[firstNamed:], func(i, j int) bool {
			return associations[firstNamed+i].formal < associations[firstNamed+j].formal
		})
	}
	builder.WriteByte('(')
	for index, association := range associations {
		if index > 0 {
			builder.WriteByte(',')
		}
		if association.formal != "" {
			builder.WriteString(association.formal)
			builder.WriteString(" is ")
		}
		builder.WriteString(behaviorExpressionKey(association.expression))
	}
	builder.WriteByte(')')
}

func canonicalPredefinedType(name string) string {
	if canonical, ok := predefinedTypes[folded(name)]; ok {
		return canonical
	}
	return name
}

func folded(value string) string {
	return strings.ToLower(value)
}

func typeError(position Position, format string, arguments ...any) error {
	return &TypeError{Position: position, Message: fmt.Sprintf(format, arguments...)}
}

// sortedMissingNames returns, in lexicographic order, the keys of right that
// are absent from left, so diagnostics never depend on map iteration order.
func sortedMissingNames[L, R any](left map[string]L, right map[string]R) []string {
	var missing []string
	for name := range right {
		if _, exists := left[name]; !exists {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	return missing
}

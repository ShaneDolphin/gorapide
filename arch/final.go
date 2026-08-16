package arch

import (
	"errors"
	"fmt"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/pattern"
)

// ErrInvalidModuleFinal identifies a malformed closed module final part.
var ErrInvalidModuleFinal = errors.New("invalid declarative Rapide module final part")

// SetFinalStatements installs the ordered statement list executed when this
// module generator's result becomes finalized and before its implicit Finish
// occurrence. Canonical-model validation defines the currently executable
// bounded subset; storing a defensive copy prevents caller mutation.
func (component *Component) SetFinalStatements(statements ...Statement) error {
	if component == nil {
		return fmt.Errorf("%w: component is nil", ErrInvalidModuleFinal)
	}
	if len(statements) == 0 {
		return fmt.Errorf("%w: component %q final part is empty", ErrInvalidModuleFinal, component.ID)
	}
	component.mu.Lock()
	defer component.mu.Unlock()
	if component.finalStatements != nil {
		return fmt.Errorf("%w: component %q already has a final part", ErrInvalidModuleFinal, component.ID)
	}
	component.finalStatements = copyStatements(statements)
	return nil
}

// validateModuleFinalStatementSubset admits the closed immediate statement tree
// restored for finalization, including closed if/case selection, assertions,
// immediate indefinite do/while control, finite closed Integer ranges, and
// direct calls to unconnected local provided functions whose complete call
// graph remains inside this same immediate subset. General/object iteration,
// state, connected functions, timing, allocation, Link/Unlink, Self, and
// external/asynchronous action interrupts require additional lifecycle
// semantics and remain explicit failures. A handler may catch an out/private
// action generated synchronously by its own protected block, or use any for
// such an immediate occurrence.
func validateModuleFinalStatementSubset(
	component *Component,
	statements []Statement,
	callables map[string]map[string]*FunctionImplementation,
) error {
	if component == nil {
		return fmt.Errorf("%w: component is nil", ErrInvalidModuleFinal)
	}
	if len(statements) == 0 {
		return fmt.Errorf("%w: component %q final part is empty", ErrInvalidModuleFinal, component.ID)
	}
	return validateModuleFinalStatementList(
		component, statements, false, false, callables, make(map[string]bool),
	)
}

func validateModuleFinalStatementList(
	component *Component,
	statements []Statement,
	allowBindings, inFunction bool,
	callables map[string]map[string]*FunctionImplementation,
	visitedFunctions map[string]bool,
) error {
	componentID := component.ID
	for index, statement := range statements {
		switch statement.kind {
		case EventCallStatement:
			if statement.timing != nil {
				return fmt.Errorf("%w: component %q final action %q is timed",
					ErrInvalidModuleFinal, componentID, statement.output.ID)
			}
			for _, parameter := range statement.output.Parameters {
				if !closedFinalRuleValue(parameter.Value, allowBindings) {
					return fmt.Errorf("%w: component %q final action %q parameter %q is not a closed scalar expression",
						ErrInvalidModuleFinal, componentID, statement.output.ID, parameter.Name)
				}
			}
		case FunctionCallStatement:
			if statement.functionCall.ResultTarget != "" {
				return fmt.Errorf("%w: component %q final function %q assigns result state %q",
					ErrInvalidModuleFinal, componentID, statement.functionCall.Name, statement.functionCall.ResultTarget)
			}
			for _, argument := range statement.functionCall.Arguments {
				if !closedFinalRuleValue(argument.Value, allowBindings) {
					return fmt.Errorf("%w: component %q final function %q argument %q is not a closed scalar expression",
						ErrInvalidModuleFinal, componentID, statement.functionCall.Name, argument.Name)
				}
			}
			if err := validateModuleFinalFunctionCall(
				component, statement.functionCall, callables, visitedFunctions,
			); err != nil {
				return err
			}
		case ReturnStatementKind:
			if !inFunction {
				return fmt.Errorf("%w: component %q final part contains return outside a local function",
					ErrInvalidModuleFinal, componentID)
			}
			if statement.returnValue != nil && !closedFinalRuleValue(*statement.returnValue, allowBindings) {
				return fmt.Errorf("%w: component %q final function return is not a closed scalar expression",
					ErrInvalidModuleFinal, componentID)
			}
		case NullStatementKind, RaiseStatementKind, ReraiseStatementKind:
		case DoBlockStatementKind:
			if err := validateModuleFinalStatementList(
				component, statement.handledBody, allowBindings, inFunction, callables, visitedFunctions,
			); err != nil {
				return err
			}
		case HandlerBlockStatementKind:
			for _, choice := range statement.handler.Choices {
				if choice.Action == "" {
					continue
				}
				action, exists := handlerActionDeclaration(component, choice.Action)
				if !exists || (action.Kind != OutAction && action.Kind != PrivateAction) {
					return fmt.Errorf("%w: component %q final external in-action interrupt choice %q requires asynchronous finalization semantics",
						ErrInvalidModuleFinal, componentID, choice.Action)
				}
			}
			if err := validateModuleFinalStatementList(
				component, statement.handledBody, allowBindings, inFunction, callables, visitedFunctions,
			); err != nil {
				return err
			}
			for _, choice := range statement.handler.Choices {
				if err := validateModuleFinalStatementList(
					component, choice.Statements, true, inFunction, callables, visitedFunctions,
				); err != nil {
					return err
				}
			}
			if err := validateModuleFinalStatementList(
				component, statement.handler.Else, allowBindings, inFunction, callables, visitedFunctions,
			); err != nil {
				return err
			}
		case IfStatementKind:
			if !closedFinalRuleValue(statement.condition, allowBindings) {
				return fmt.Errorf("%w: component %q final if condition is not a closed scalar expression",
					ErrInvalidModuleFinal, componentID)
			}
			if err := validateModuleFinalStatementList(
				component, statement.thenBranch, allowBindings, inFunction, callables, visitedFunctions,
			); err != nil {
				return err
			}
			if err := validateModuleFinalStatementList(
				component, statement.elseBranch, allowBindings, inFunction, callables, visitedFunctions,
			); err != nil {
				return err
			}
		case CaseStatementKind:
			if !closedFinalRuleValue(statement.caseValue, allowBindings) {
				return fmt.Errorf("%w: component %q final case expression is not a closed scalar expression",
					ErrInvalidModuleFinal, componentID)
			}
			for _, alternative := range statement.caseAlts {
				for _, choice := range alternative.choices {
					if !closedFinalCaseChoice(choice, allowBindings) {
						return fmt.Errorf("%w: component %q final case choice is not a closed scalar expression",
							ErrInvalidModuleFinal, componentID)
					}
				}
				if err := validateModuleFinalStatementList(
					component, alternative.body, allowBindings, inFunction, callables, visitedFunctions,
				); err != nil {
					return err
				}
			}
			if err := validateModuleFinalStatementList(
				component, statement.caseDefault, allowBindings, inFunction, callables, visitedFunctions,
			); err != nil {
				return err
			}
		case AssertStatementKind:
			if !closedFinalRuleValue(statement.condition, allowBindings) {
				return fmt.Errorf("%w: component %q final assertion is not a closed Boolean expression",
					ErrInvalidModuleFinal, componentID)
			}
		case LoopStatementKind:
			if err := validateModuleFinalStatementList(
				component, statement.loopBody, allowBindings, inFunction, callables, visitedFunctions,
			); err != nil {
				return err
			}
		case ForStatementKind:
			if statement.iteratorKind != rangeStatementIteratorKind || statement.iteratorType != "Integer" {
				return fmt.Errorf("%w: component %q final for requires a finite Range(Integer) iterator",
					ErrInvalidModuleFinal, componentID)
			}
			if !closedFinalRuleValue(statement.iteratorFirst, allowBindings) ||
				!closedFinalRuleValue(statement.iteratorLast, allowBindings) {
				return fmt.Errorf("%w: component %q final range endpoints are not closed scalar expressions",
					ErrInvalidModuleFinal, componentID)
			}
			bodyBindings := allowBindings || statement.iteratorName != ""
			if err := validateModuleFinalStatementList(
				component, statement.loopBody, bodyBindings, inFunction, callables, visitedFunctions,
			); err != nil {
				return err
			}
		case ExitStatementKind, NextStatementKind:
			if !closedFinalRuleValue(statement.condition, allowBindings) {
				return fmt.Errorf("%w: component %q final %s condition is not a closed Boolean expression",
					ErrInvalidModuleFinal, componentID, statement.kind)
			}
		default:
			return fmt.Errorf("%w: component %q final statement %d has unsupported kind %q; current subset requires immediate action/local-function/null/do/raise/if/case/assert/loop/range-for/exit/next statements",
				ErrInvalidModuleFinal, componentID, index, statement.kind)
		}
	}
	return nil
}

func validateModuleFinalFunctionCall(
	component *Component,
	call FunctionCall,
	callables map[string]map[string]*FunctionImplementation,
	visitedFunctions map[string]bool,
) error {
	componentID := component.ID
	implementation := callables[componentID][call.functionKey]
	if implementation == nil {
		return fmt.Errorf(
			"%w: component %q final call %q resolved to missing function signature %q",
			ErrInvalidModuleFinal, componentID, call.ID, call.functionKey,
		)
	}
	targetComponentID := implementation.targetComponent
	if targetComponentID == "" {
		targetComponentID = componentID
	}
	localProvided := implementation.connectionID == "" && targetComponentID == componentID && len(implementation.routeAliases) == 0
	moduleRoute := isDynamicModuleFunctionRoute(componentID, implementation)
	if !localProvided && !moduleRoute {
		return fmt.Errorf(
			"%w: component %q final function %q uses a non-module route to component %q; passive New finalization can reevaluate only generator-owned self connections",
			ErrInvalidModuleFinal, componentID, implementation.Name, targetComponentID,
		)
	}
	if len(implementation.Locals) != 0 {
		return fmt.Errorf(
			"%w: component %q final function %q declares module locals; allocation during finalization is outside the passive subset",
			ErrInvalidModuleFinal, componentID, implementation.Name,
		)
	}
	visitKey := componentID + "\x00" + call.functionKey
	if visitedFunctions[visitKey] {
		return nil
	}
	visitedFunctions[visitKey] = true
	if implementation.Return != nil && !closedFinalRuleValue(*implementation.Return, true) {
		return fmt.Errorf(
			"%w: component %q final function %q tail return is not a closed scalar expression",
			ErrInvalidModuleFinal, componentID, implementation.Name,
		)
	}
	return validateModuleFinalStatementList(
		component, implementation.Statements, true, true, callables, visitedFunctions,
	)
}

func closedFinalCaseChoice(choice CaseChoice, allowBindings bool) bool {
	switch choice.kind {
	case caseValueChoiceKind:
		return closedFinalRuleValue(choice.value, allowBindings)
	case caseRangeChoiceKind:
		return closedFinalRuleValue(choice.first, allowBindings) &&
			closedFinalRuleValue(choice.last, allowBindings)
	default:
		return false
	}
}

func closedFinalRuleValue(value RuleValue, allowBindings bool) bool {
	switch value.kind {
	case RuleLiteralValue:
		return predefinedTypeOfValue(value.literal) != ""
	case RuleBindingValue:
		return allowBindings
	case RuleUnaryValue, RuleBinaryValue, RuleTernaryValue:
		for _, operand := range value.operands {
			if !closedFinalRuleValue(operand, allowBindings) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

// executeModuleFinalPart materializes the final-part action sequence at the
// newly finalized dynamic module identity. It appends those occurrences to the
// enclosing semantic step, but intentionally leaves that step's control
// frontier unchanged: Stanford finalization is caused by name loss while the
// enclosing process continues independently after relinquishing the name.
// The returned frontier is used only to cause the implicit Finish occurrence.
func executeModuleFinalPart(
	moduleID string,
	nameLossFrontier []gorapide.EventID,
	modelDigest string,
	runtime *functionExecutionRuntime,
	execution *statementExecution,
) ([]gorapide.EventID, error) {
	if runtime == nil || runtime.model == nil || execution == nil {
		return nil, fmt.Errorf("%w: finalized module %q has no execution runtime", ErrInvalidModuleFinal, moduleID)
	}
	templateID := runtime.moduleTemplates[moduleID]
	if templateID == "" {
		return nil, fmt.Errorf("%w: finalized module %q has no generator template", ErrInvalidModuleFinal, moduleID)
	}
	statements := runtime.model.finalStatements[templateID]
	if len(statements) == 0 {
		return canonicalEventIDs(nameLossFrontier), nil
	}
	component := runtime.components[moduleID]
	if component == nil || runtime.modules[moduleID].Identity() != moduleID {
		return nil, fmt.Errorf("%w: finalized module %q has no allocated component/module identity", ErrInvalidModuleFinal, moduleID)
	}
	emptyMatch := pattern.MatchResult{Events: gorapide.EventSet{}, Bindings: pattern.Bindings{}}
	matchDigest, err := pattern.SemanticDigestMatches([]pattern.MatchResult{emptyMatch})
	if err != nil {
		return nil, fmt.Errorf("%w: empty final-part binding environment: %v", ErrInvalidModuleFinal, err)
	}
	rule := &DeclarativeRule{
		ID:      "module-final/" + templateID,
		Process: RulePipeProcess,
		Body:    &RuleBody{Statements: statements},
	}
	result, err := executeRuleStatements(
		moduleID, component, rule, emptyMatch, matchDigest, modelDigest,
		statements, runtime, runtime.state[moduleID], nameLossFrontier,
		execution.budget, execution.clocks, "final:"+moduleID, nil, nil,
	)
	if err != nil {
		return nil, fmt.Errorf("%w: module %q: %v", ErrInvalidModuleFinal, moduleID, err)
	}
	if result.raised != nil {
		return nil, fmt.Errorf("%w: module %q final part: %s",
			ErrUnhandledRapideException, moduleID, result.raised.name)
	}
	if len(result.scheduled) != 0 || len(result.reads) != 0 || len(result.writes) != 0 || result.exitProcess {
		return nil, fmt.Errorf("%w: module %q final part escaped the immediate finalization subset", ErrInvalidModuleFinal, moduleID)
	}
	execution.generated = append(execution.generated, result.generated...)
	// Finish must follow every event generated by the final part. A temporary
	// iterator's own Finish is intentionally independent of the following final
	// statement, so include every generated event before reducing to the exact
	// maximal frontier used only by the finalized module's implicit Finish.
	frontier := append([]gorapide.EventID(nil), result.control...)
	for _, output := range result.generated {
		if output.event != nil {
			frontier = append(frontier, output.event.ID)
		}
	}
	return maximalGeneratedEventFrontier(frontier, result.generated), nil
}

package arch

import (
	"fmt"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/pattern"
)

// ExecutableObjectExpressionKind identifies the closed top-level object-
// expression forms that can currently preserve executable Rapide effects.
// Object expressions are not statement lists: selection and nested function
// application remain later compatibility work.
type ExecutableObjectExpressionKind string

const (
	ObjectValueExpression      ExecutableObjectExpressionKind = "value"
	ObjectFunctionExpression   ExecutableObjectExpressionKind = "function-call"
	ObjectAssignmentExpression ExecutableObjectExpressionKind = "assignment"
)

// ExecutableObjectExpression is one closed, serializable Rapide object
// expression. It is initially used by the three-expression for statement from
// Executable LRM section 5.3.
type ExecutableObjectExpression struct {
	kind       ExecutableObjectExpressionKind
	value      RuleValue
	call       FunctionCall
	assignment StateAssignment
}

// ObjectValue constructs a side-effect-free object expression.
func ObjectValue(value RuleValue) ExecutableObjectExpression {
	return ExecutableObjectExpression{kind: ObjectValueExpression, value: copyRuleValue(value)}
}

// ObjectFunctionCall constructs a function-call object expression. Its return
// object may be ignored by an initializer or increment and may be consumed as
// the Boolean test of a general for statement.
func ObjectFunctionCall(id, name string, arguments ...RuleParameter) ExecutableObjectExpression {
	call := FunctionCall{ID: id, Name: name, Arguments: make([]RuleParameter, len(arguments))}
	for index, argument := range arguments {
		call.Arguments[index] = RuleParameter{Name: argument.Name, Value: copyRuleValue(argument.Value)}
	}
	return ExecutableObjectExpression{kind: ObjectFunctionExpression, call: call}
}

// ObjectAssignment constructs the closed Ref-style assignment object
// expression supported by procedural source bodies. Rapide defines := on
// Ref(T) as returning Ref(T). The current closed loop subset type-checks that
// result exactly, while initializer and next positions intentionally ignore it.
func ObjectAssignment(target string, value RuleValue) ExecutableObjectExpression {
	return ExecutableObjectExpression{
		kind:       ObjectAssignmentExpression,
		assignment: StateAssignment{Target: target, Value: copyRuleValue(value)},
	}
}

type canonicalExecutableObjectExpression struct {
	Kind       ExecutableObjectExpressionKind `json:"kind"`
	Value      *canonicalRuleValue            `json:"value,omitempty"`
	Call       *canonicalFunctionCall         `json:"call,omitempty"`
	Assignment *canonicalStateAssignment      `json:"assignment,omitempty"`
}

func copyExecutableObjectExpression(expression ExecutableObjectExpression) ExecutableObjectExpression {
	result := expression
	result.value = copyRuleValue(expression.value)
	result.call = copyFunctionCall(expression.call)
	result.assignment.Value = copyRuleValue(expression.assignment.Value)
	return result
}

func canonicalizeExecutableObjectExpression(
	owner string,
	expression ExecutableObjectExpression,
	stateTypes, placeholderTypes map[string]string,
	functions map[string]*FunctionImplementation,
	seenCalls map[string]bool,
) (ExecutableObjectExpression, canonicalExecutableObjectExpression, string, error) {
	canonical := canonicalExecutableObjectExpression{Kind: expression.kind}
	switch expression.kind {
	case ObjectValueExpression:
		value, encoded, typeName, err := canonicalizeClosedRuleValue(
			owner, expression.value, stateTypes, placeholderTypes,
		)
		if err != nil {
			return ExecutableObjectExpression{}, canonicalExecutableObjectExpression{}, "", err
		}
		canonical.Value = &encoded
		return ExecutableObjectExpression{kind: ObjectValueExpression, value: value}, canonical, typeName, nil
	case ObjectFunctionExpression:
		if expression.call.ResultTarget != "" {
			return ExecutableObjectExpression{}, canonicalExecutableObjectExpression{}, "", fmt.Errorf(
				"%w: %s function-call object expression cannot assign its result to %q",
				ErrInvalidDeclarativeStatement, owner, expression.call.ResultTarget,
			)
		}
		if expression.call.ID == "" || seenCalls[expression.call.ID] {
			return ExecutableObjectExpression{}, canonicalExecutableObjectExpression{}, "", fmt.Errorf(
				"%w: %s has empty or duplicate object-expression call ID %q",
				ErrInvalidDeclarativeStatement, owner, expression.call.ID,
			)
		}
		call, encoded, err := canonicalizeFunctionCall(
			owner, expression.call, stateTypes, placeholderTypes, functions,
		)
		if err != nil {
			return ExecutableObjectExpression{}, canonicalExecutableObjectExpression{}, "", err
		}
		implementation := functions[call.functionKey]
		if implementation == nil {
			return ExecutableObjectExpression{}, canonicalExecutableObjectExpression{}, "", fmt.Errorf(
				"%w: %s resolved missing function signature %q",
				ErrInvalidDeclarativeStatement, owner, call.functionKey,
			)
		}
		seenCalls[expression.call.ID] = true
		canonical.Call = &encoded
		resultType := implementation.ReturnType
		if resultType == "" {
			resultType = "Root"
		}
		return ExecutableObjectExpression{kind: ObjectFunctionExpression, call: call}, canonical, resultType, nil
	case ObjectAssignmentExpression:
		targetType, ok := stateTypes[expression.assignment.Target]
		if !ok {
			return ExecutableObjectExpression{}, canonicalExecutableObjectExpression{}, "", fmt.Errorf(
				"%w: %s assignment targets undeclared state %q",
				ErrInvalidStateReference, owner, expression.assignment.Target,
			)
		}
		assignments, encoded, err := canonicalizeStateAssignments(
			owner, []StateAssignment{expression.assignment}, stateTypes, placeholderTypes,
		)
		if err != nil {
			return ExecutableObjectExpression{}, canonicalExecutableObjectExpression{}, "", err
		}
		canonical.Assignment = &encoded[0]
		return ExecutableObjectExpression{
			kind: ObjectAssignmentExpression, assignment: assignments[0],
		}, canonical, referenceObjectExpressionType(targetType), nil
	default:
		return ExecutableObjectExpression{}, canonicalExecutableObjectExpression{}, "", fmt.Errorf(
			"%w: %s has object-expression kind %q",
			ErrInvalidDeclarativeStatement, owner, expression.kind,
		)
	}
}

func referenceObjectExpressionType(elementType string) string {
	return "Ref(" + elementType + ")"
}

func executeExecutableObjectExpression(
	componentID string,
	component *Component,
	rule *DeclarativeRule,
	match pattern.MatchResult,
	matchDigest, modelDigest, expressionPath string,
	expression ExecutableObjectExpression,
	functionRuntime *functionExecutionRuntime,
	cells map[string]*stateCell,
	execution *statementExecution,
	budget *statementBudget,
) (any, error) {
	if err := budget.consume(); err != nil {
		return nil, err
	}
	switch expression.kind {
	case ObjectValueExpression:
		evaluated, err := evaluateClosedRuleValue(
			rule.ID+" object expression "+expressionPath,
			expression.value, match.Bindings, cells,
		)
		if err != nil {
			return nil, err
		}
		if err := incorporateEvaluatedStateReads(execution, evaluated.reads, evaluated.causes); err != nil {
			return nil, err
		}
		return evaluated.value, nil
	case ObjectFunctionExpression:
		return executeFunctionCall(
			componentID, component, rule, match, matchDigest, modelDigest,
			expressionPath, expression.call, functionRuntime, cells, execution, budget,
		)
	case ObjectAssignmentExpression:
		reads, writes, err := applyStateAssignments(
			rule.ID+" object expression "+expressionPath,
			[]StateAssignment{expression.assignment}, match.Bindings, cells,
			execution.control, execution.pendingOperations,
		)
		if err != nil {
			return nil, err
		}
		execution.reads = append(execution.reads, reads...)
		execution.writes = append(execution.writes, writes...)
		execution.pendingOperations = canonicalStateOperationReferences(append(
			execution.pendingOperations, stateOperationReferences(reads, writes)...,
		))
		for _, write := range writes {
			for _, cause := range write.Causes {
				execution.control = append(execution.control, gorapide.EventID(cause))
			}
		}
		execution.control = canonicalEventIDs(execution.control)
		return nil, nil
	default:
		return nil, fmt.Errorf(
			"%w: object expression %s has kind %q",
			ErrInvalidDeclarativeStatement, expressionPath, expression.kind,
		)
	}
}

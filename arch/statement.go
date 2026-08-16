package arch

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/pattern"
)

var (
	ErrInvalidDeclarativeStatement = errors.New("invalid declarative Rapide statement")
	ErrCaseChoiceConflict          = errors.New("multiple xor case alternatives are eligible")
)

type statementBudget struct {
	limit uint64
	used  uint64
}

func (budget *statementBudget) consume() error {
	if budget == nil || budget.limit == 0 {
		return fmt.Errorf("%w: ordinary statement budget is missing", ErrExecutionLimit)
	}
	if budget.used >= budget.limit {
		return fmt.Errorf("%w: max_statements=%d", ErrExecutionLimit, budget.limit)
	}
	budget.used++
	return nil
}

// StatementKind identifies one closed ordinary statement form in the initial
// procedural subset.
type StatementKind string

const (
	AssignmentStatement       StatementKind = "assignment"
	EventCallStatement        StatementKind = "event-call"
	IfStatementKind           StatementKind = "if"
	NullStatementKind         StatementKind = "null"
	LoopStatementKind         StatementKind = "loop"
	ForStatementKind          StatementKind = "for"
	GeneralForStatementKind   StatementKind = "general-for"
	ExitStatementKind         StatementKind = "exit"
	NextStatementKind         StatementKind = "next"
	ExitWhenStatementKind     StatementKind = "exit-enclosing-when"
	CaseStatementKind         StatementKind = "case"
	AssertStatementKind       StatementKind = "assert"
	FunctionCallStatement     StatementKind = "function-call"
	ReturnStatementKind       StatementKind = "return"
	TimedStatementKind        StatementKind = "timed-statement"
	LinkStatementKind         StatementKind = "link"
	UnlinkStatementKind       StatementKind = "unlink"
	RaiseStatementKind        StatementKind = "raise"
	ReraiseStatementKind      StatementKind = "reraise"
	DoBlockStatementKind      StatementKind = "do-block"
	HandlerBlockStatementKind StatementKind = "handler-block"
)

// TimingClauseKind identifies an executable action-call or timed-statement
// timing form. The closed subset supports object-valued in clauses everywhere
// ordinary action calls are supported and resumable pause/delay forms in the
// supported nested control flow of declarative-process statement bodies.
type TimingClauseKind string

const (
	InTimingClause    TimingClauseKind = "in"
	PauseTimingClause TimingClauseKind = "pause"
	DelayTimingClause TimingClauseKind = "delay"
)

type ActionTimingClause struct {
	Kind  TimingClauseKind
	Clock string
	Ticks uint64
	Range *TimingTickRange
}

// TimingTickRange is the initial finite subtype-valued timing expression:
// C.Ticks range First..Last. Execution chooses one member through the ordinary
// deterministic choice journal rather than host randomness.
type TimingTickRange struct {
	First uint64
	Last  uint64
}

// FunctionCall is one local synchronous invocation. Arguments are named closed
// expressions and ID is the stable lexical occurrence name used for event
// provenance.
type FunctionCall struct {
	ID           string
	Name         string
	Arguments    []RuleParameter
	ResultTarget string

	functionKey string
}

// CaseMode identifies the separator between Rapide case alternatives. A case
// body uses one separator consistently: xor, or, or else.
type CaseMode string

const (
	CaseXorMode  CaseMode = "xor"
	CaseOrMode   CaseMode = "or"
	CaseElseMode CaseMode = "else"
)

// CaseAlternative is one choice list and its ordered statement body.
// Type choices remain outside this closed subset.
type CaseAlternative struct {
	choices []CaseChoice
	body    []Statement
}

// CaseChoice is one closed value or inclusive integer-range case choice.
type CaseChoice struct {
	kind  string
	value RuleValue
	first RuleValue
	last  RuleValue
}

const (
	caseValueChoiceKind = "value"
	caseRangeChoiceKind = "range"
)

// Statement is a closed, serializable procedural statement. Fields remain
// private so malformed nodes can only be constructed through the generic
// operator escape hatches used by negative conformance tests.
type Statement struct {
	kind                 StatementKind
	doName               string
	controlDoName        string
	doControl            bool
	assignment           StateAssignment
	output               RuleOutput
	timing               *ActionTimingClause
	condition            RuleValue
	thenBranch           []Statement
	elseBranch           []Statement
	loopBody             []Statement
	iteratorKind         string
	iteratorName         string
	iteratorType         string
	iteratorGenerator    string
	iteratorValue        RuleValue
	iteratorFirst        RuleValue
	iteratorLast         RuleValue
	forInitial           ExecutableObjectExpression
	forTest              ExecutableObjectExpression
	forNext              ExecutableObjectExpression
	caseValue            RuleValue
	caseMode             CaseMode
	caseAlts             []CaseAlternative
	caseDefault          []Statement
	functionCall         FunctionCall
	returnValue          *RuleValue
	contextValue         RuleValue
	exceptionDeclaration string
	raiseCondition       *RuleValue
	handledBody          []Statement
	handler              ExceptionHandler
}

const (
	rangeStatementIteratorKind     = "range"
	moduleStatementIteratorKind    = "module"
	generatorStatementIteratorKind = "generator"
)

func SetState(target string, value RuleValue) Statement {
	return Statement{kind: AssignmentStatement, assignment: AssignState(target, value)}
}

// CallAction generates one sequential process/rule-body event.
func CallAction(id, action string, parameters ...RuleParameter) Statement {
	return Statement{kind: EventCallStatement, output: RuleEvent(id, action, parameters...)}
}

// RaiseException generates one exception event and immediately transfers the
// current statement control to the nearest active handler.
func RaiseException(id, name string, parameters ...RuleParameter) Statement {
	return Statement{kind: RaiseStatementKind, output: RuleEvent(id, name, parameters...)}
}

// RaiseDeclaredException raises one exact lexical exception declaration.
func RaiseDeclaredException(id, declaration, name string, parameters ...RuleParameter) Statement {
	return Statement{
		kind: RaiseStatementKind, exceptionDeclaration: declaration,
		output: RuleEvent(id, name, parameters...),
	}
}

// RaiseExceptionWhere evaluates the Boolean guard first. False continues
// normally without generating an exception; true has ordinary raise semantics.
func RaiseExceptionWhere(id, name string, condition RuleValue, parameters ...RuleParameter) Statement {
	copy := copyRuleValue(condition)
	return Statement{
		kind: RaiseStatementKind, output: RuleEvent(id, name, parameters...), raiseCondition: &copy,
	}
}

// RaiseDeclaredExceptionWhere conditionally raises one exact lexical
// exception declaration.
func RaiseDeclaredExceptionWhere(
	id, declaration, name string,
	condition RuleValue,
	parameters ...RuleParameter,
) Statement {
	copy := copyRuleValue(condition)
	return Statement{
		kind: RaiseStatementKind, exceptionDeclaration: declaration,
		output: RuleEvent(id, name, parameters...), raiseCondition: &copy,
	}
}

// ReraiseException transfers the exact exception occurrence currently being
// handled to the next enclosing handler. It generates no replacement event.
func ReraiseException() Statement {
	return Statement{kind: ReraiseStatementKind}
}

// ReraiseExceptionWhere conditionally transfers the exact exception occurrence
// currently being handled. False continues the active handler normally.
func ReraiseExceptionWhere(condition RuleValue) Statement {
	copy := copyRuleValue(condition)
	return Statement{kind: ReraiseStatementKind, raiseCondition: &copy}
}

// CallActionIn evaluates the action and parameters immediately, takes zero
// clock time, and schedules the event for generation after ticks on the named
// component-local basic clock. A zero duration canonicalizes to CallAction, as
// required by the Executable LRM.
func CallActionIn(id, action, clock string, ticks uint64, parameters ...RuleParameter) Statement {
	clause := &ActionTimingClause{Kind: InTimingClause, Clock: clock, Ticks: ticks}
	return Statement{kind: EventCallStatement, output: RuleEvent(id, action, parameters...), timing: clause}
}

// CallActionInRange uses the finite subtype C.Ticks range first..last. One
// member is selected explicitly when the call statement executes.
func CallActionInRange(id, action, clock string, first, last uint64, parameters ...RuleParameter) Statement {
	clause := &ActionTimingClause{
		Kind: InTimingClause, Clock: clock, Range: &TimingTickRange{First: first, Last: last},
	}
	return Statement{kind: EventCallStatement, output: RuleEvent(id, action, parameters...), timing: clause}
}

// CallActionPause begins the event at the current named-clock tick, suspends
// the enclosing process, and completes/generates the event after ticks.
func CallActionPause(id, action, clock string, ticks uint64, parameters ...RuleParameter) Statement {
	clause := &ActionTimingClause{Kind: PauseTimingClause, Clock: clock, Ticks: ticks}
	return Statement{kind: EventCallStatement, output: RuleEvent(id, action, parameters...), timing: clause}
}

// CallActionPauseRange selects one member of C.Ticks range first..last before
// beginning the corresponding pause interval.
func CallActionPauseRange(id, action, clock string, first, last uint64, parameters ...RuleParameter) Statement {
	clause := &ActionTimingClause{
		Kind: PauseTimingClause, Clock: clock, Range: &TimingTickRange{First: first, Last: last},
	}
	return Statement{kind: EventCallStatement, output: RuleEvent(id, action, parameters...), timing: clause}
}

// CallActionDelay has pause timing and additionally makes events observable to
// the enclosing process during the closed interval unavailable to that process.
func CallActionDelay(id, action, clock string, ticks uint64, parameters ...RuleParameter) Statement {
	clause := &ActionTimingClause{Kind: DelayTimingClause, Clock: clock, Ticks: ticks}
	return Statement{kind: EventCallStatement, output: RuleEvent(id, action, parameters...), timing: clause}
}

// CallActionDelayRange selects one member of C.Ticks range first..last before
// beginning the corresponding delay interval.
func CallActionDelayRange(id, action, clock string, first, last uint64, parameters ...RuleParameter) Statement {
	clause := &ActionTimingClause{
		Kind: DelayTimingClause, Clock: clock, Range: &TimingTickRange{First: first, Last: last},
	}
	return Statement{kind: EventCallStatement, output: RuleEvent(id, action, parameters...), timing: clause}
}

// PauseFor suspends a process for ticks without generating an event.
func PauseFor(clock string, ticks uint64) Statement {
	return Statement{kind: TimedStatementKind, timing: &ActionTimingClause{Kind: PauseTimingClause, Clock: clock, Ticks: ticks}}
}

// PauseForRange selects one member of C.Ticks range first..last and suspends
// the process for the selected duration.
func PauseForRange(clock string, first, last uint64) Statement {
	return Statement{kind: TimedStatementKind, timing: &ActionTimingClause{
		Kind: PauseTimingClause, Clock: clock, Range: &TimingTickRange{First: first, Last: last},
	}}
}

// DelayFor suspends a process for ticks without generating an event and makes
// events observable during the closed interval unavailable to that process.
func DelayFor(clock string, ticks uint64) Statement {
	return Statement{kind: TimedStatementKind, timing: &ActionTimingClause{Kind: DelayTimingClause, Clock: clock, Ticks: ticks}}
}

// DelayForRange selects one member of C.Ticks range first..last and applies the
// corresponding process-local closed delay window.
func DelayForRange(clock string, first, last uint64) Statement {
	return Statement{kind: TimedStatementKind, timing: &ActionTimingClause{
		Kind: DelayTimingClause, Clock: clock, Range: &TimingTickRange{First: first, Last: last},
	}}
}

// CallFunction invokes one statically resolved local or connected function and
// ignores its returned object, if any. Function calls used as expressions are
// a later compatibility slice.
func CallFunction(id, name string, arguments ...RuleParameter) Statement {
	result := FunctionCall{ID: id, Name: name, Arguments: make([]RuleParameter, len(arguments))}
	for i, argument := range arguments {
		result.Arguments[i] = RuleParameter{Name: argument.Name, Value: copyRuleValue(argument.Value)}
	}
	return Statement{kind: FunctionCallStatement, functionCall: result}
}

// CallFunctionInto invokes a typed local or connected function and writes its returned
// object to target after F'Return is generated. This is the initial ordered
// lowering for assignment from a function call; general call expressions are
// not yet part of RuleValue.
func CallFunctionInto(id, target, name string, arguments ...RuleParameter) Statement {
	statement := CallFunction(id, name, arguments...)
	statement.functionCall.ResultTarget = target
	return statement
}

// ReturnFromFunction terminates the enclosing typed function with value.
func ReturnFromFunction(value RuleValue) Statement {
	copy := copyRuleValue(value)
	return Statement{kind: ReturnStatementKind, returnValue: &copy}
}

// ReturnFromFunctionVoid terminates a function with no returned object.
func ReturnFromFunctionVoid() Statement {
	return Statement{kind: ReturnStatementKind}
}

func IfThen(condition RuleValue, thenBranch []Statement, elseBranch []Statement) Statement {
	return Statement{
		kind: IfStatementKind, condition: copyRuleValue(condition),
		thenBranch: copyStatements(thenBranch), elseBranch: copyStatements(elseBranch),
	}
}

func NullStatement() Statement { return Statement{kind: NullStatementKind} }

// DoBlock groups one nonempty sequential statement list without installing a
// handler. Control transfers propagate unchanged to the enclosing construct.
func DoBlock(statements ...Statement) Statement {
	return Statement{
		kind: DoBlockStatementKind, doControl: true,
		handledBody: copyStatements(statements),
	}
}

// NameDo gives an executable do form its Rapide label. The label is part of
// canonical model identity and may be named by ExitNamedWhen or NextNamedWhen.
// Canonical validation rejects labels on statements that are not do forms.
func NameDo(name string, statement Statement) Statement {
	statement.doName = name
	return statement
}

// LinkModule links source into the communication Context of the executing
// module, matching predefined Link(Src). The returned Src value is discarded
// by this statement form.
func LinkModule(source RuleValue) Statement {
	return Statement{kind: LinkStatementKind, contextValue: copyRuleValue(source)}
}

// UnlinkModule removes source from the communication Context of the executing
// module, matching predefined Unlink(Src).
func UnlinkModule(source RuleValue) Statement {
	return Statement{kind: UnlinkStatementKind, contextValue: copyRuleValue(source)}
}

// AssertThat constructs an unlabeled Rapide assertion. A false condition
// generates the predefined parameterless Inconsistent event.
func AssertThat(condition RuleValue) Statement {
	return Statement{kind: AssertStatementKind, condition: copyRuleValue(condition)}
}

// LoopDo constructs Rapide's indefinite do loop. ExitWhen/ExitLoop complete
// the nearest loop; NextWhen/NextLoop skip to its next iteration.
func LoopDo(body ...Statement) Statement {
	return Statement{kind: LoopStatementKind, doControl: true, loopBody: copyStatements(body)}
}

// ForEachIntegerRange constructs the published finite Range(Integer) form of
// Rapide's first for statement. Execution evaluates both range endpoints once,
// allocates one replay-stable iterator object, and invokes its More and Item
// functions in the order specified by Executable LRM section 5.3.
func ForEachIntegerRange(identifier string, first, last RuleValue, body ...Statement) Statement {
	return Statement{
		kind: ForStatementKind, doControl: true, iteratorKind: rangeStatementIteratorKind,
		iteratorName: identifier, iteratorType: "Integer",
		iteratorFirst: copyRuleValue(first), iteratorLast: copyRuleValue(last),
		loopBody: copyStatements(body),
	}
}

// ForEachIterator constructs the first closed object-expression subset of the
// published for statement. The finite iterator module must also be declared on
// the owning Architecture so its implementation and shared cursor are model
// content rather than host state.
func ForEachIterator(identifier string, iterator *FiniteIteratorModule, body ...Statement) Statement {
	statement := Statement{
		kind: ForStatementKind, doControl: true, iteratorKind: moduleStatementIteratorKind,
		iteratorName: identifier, loopBody: copyStatements(body),
	}
	if iterator != nil {
		statement.iteratorValue = LiteralValue(iterator.Module())
		statement.iteratorType, _ = iterator.ItemType().PredefinedName()
	}
	return statement
}

// ForEachGeneratedIterator constructs a zero-parameter Iterator(T) module-
// generator call expression. Every evaluation allocates a fresh module and
// then invokes its More and Item functions in the published order.
func ForEachGeneratedIterator(
	identifier string,
	generator *FiniteIteratorGenerator,
	body ...Statement,
) Statement {
	statement := Statement{
		kind: ForStatementKind, doControl: true, iteratorKind: generatorStatementIteratorKind,
		iteratorName: identifier, loopBody: copyStatements(body),
	}
	if generator != nil {
		statement.iteratorGenerator = generator.Name()
		statement.iteratorType, _ = generator.ItemType().PredefinedName()
	}
	return statement
}

// ForObjectExpressions constructs the initializer/test/next form of Rapide's
// for statement. Each control is exactly one object expression. The initializer
// executes once, then the Boolean test, body, and next expression repeat in
// that order. A next statement in the body still executes the next expression;
// an exit statement does not.
func ForObjectExpressions(
	initial, test, next ExecutableObjectExpression,
	body ...Statement,
) Statement {
	return Statement{
		kind:       GeneralForStatementKind,
		doControl:  true,
		forInitial: copyExecutableObjectExpression(initial),
		forTest:    copyExecutableObjectExpression(test),
		forNext:    copyExecutableObjectExpression(next),
		loopBody:   copyStatements(body),
	}
}

func ExitWhen(condition RuleValue) Statement {
	return Statement{kind: ExitStatementKind, condition: copyRuleValue(condition)}
}

func ExitLoop() Statement { return ExitWhen(LiteralValue(true)) }

// ExitNamedWhen completes the lexically enclosing do named by name when the
// Boolean condition is true.
func ExitNamedWhen(name string, condition RuleValue) Statement {
	return Statement{
		kind: ExitStatementKind, controlDoName: name,
		condition: copyRuleValue(condition),
	}
}

func ExitNamed(name string) Statement { return ExitNamedWhen(name, LiteralValue(true)) }

func NextWhen(condition RuleValue) Statement {
	return Statement{kind: NextStatementKind, condition: copyRuleValue(condition)}
}

func NextLoop() Statement { return NextWhen(LiteralValue(true)) }

// NextNamedWhen completes the current iteration of the lexically enclosing
// do named by name when the Boolean condition is true.
func NextNamedWhen(name string, condition RuleValue) Statement {
	return Statement{
		kind: NextStatementKind, controlDoName: name,
		condition: copyRuleValue(condition),
	}
}

func NextNamed(name string) Statement { return NextNamedWhen(name, LiteralValue(true)) }

// ExitEnclosingWhen completes the source-equivalent implicit do loop of the
// enclosing WhenState. It remains as a compatibility constructor; an ordinary
// unnamed ExitLoop has the same target when no nested do statement encloses it.
func ExitEnclosingWhen() Statement {
	return ExitEnclosingWhenWhere(LiteralValue(true))
}

// ExitEnclosingWhenWhere conditionally completes the enclosing WhenState.
func ExitEnclosingWhenWhere(condition RuleValue) Statement {
	return Statement{kind: ExitWhenStatementKind, condition: copyRuleValue(condition)}
}

// CaseWhen constructs an alternative with one value choice.
func CaseWhen(choice RuleValue, body ...Statement) CaseAlternative {
	return CaseWhenChoices([]CaseChoice{CaseValueChoice(choice)}, body...)
}

// CaseWhenAny constructs an alternative eligible when any choice equals the
// case expression.
func CaseWhenAny(choices []RuleValue, body ...Statement) CaseAlternative {
	converted := make([]CaseChoice, len(choices))
	for i, choice := range choices {
		converted[i] = CaseValueChoice(choice)
	}
	return CaseWhenChoices(converted, body...)
}

// CaseWhenRange constructs one alternative for an inclusive integer range.
func CaseWhenRange(first, last RuleValue, body ...Statement) CaseAlternative {
	return CaseWhenChoices([]CaseChoice{CaseRangeChoice(first, last)}, body...)
}

// CaseWhenChoices constructs an alternative from a source-ordered mix of
// value and integer-range choices.
func CaseWhenChoices(choices []CaseChoice, body ...Statement) CaseAlternative {
	return CaseAlternative{choices: copyCaseChoices(choices), body: copyStatements(body)}
}

func CaseValueChoice(value RuleValue) CaseChoice {
	return CaseChoice{kind: caseValueChoiceKind, value: copyRuleValue(value)}
}

func CaseRangeChoice(first, last RuleValue) CaseChoice {
	return CaseChoice{
		kind: caseRangeChoiceKind, first: copyRuleValue(first), last: copyRuleValue(last),
	}
}

// CaseOf constructs a case without a default part.
func CaseOf(value RuleValue, mode CaseMode, alternatives ...CaseAlternative) Statement {
	return caseStatement(value, mode, nil, alternatives)
}

// CaseOfDefault constructs a case with an ordered default statement body.
func CaseOfDefault(value RuleValue, mode CaseMode, defaultBody []Statement, alternatives ...CaseAlternative) Statement {
	return caseStatement(value, mode, defaultBody, alternatives)
}

func caseStatement(value RuleValue, mode CaseMode, defaultBody []Statement, alternatives []CaseAlternative) Statement {
	return Statement{
		kind: CaseStatementKind, caseValue: copyRuleValue(value), caseMode: mode,
		caseAlts: copyCaseAlternatives(alternatives), caseDefault: copyStatements(defaultBody),
	}
}

type canonicalCaseChoice struct {
	Kind  string              `json:"kind"`
	Value *canonicalRuleValue `json:"value,omitempty"`
	First *canonicalRuleValue `json:"first,omitempty"`
	Last  *canonicalRuleValue `json:"last,omitempty"`
}

type canonicalCaseAlternative struct {
	Choices []canonicalCaseChoice    `json:"choices"`
	Body    []canonicalRuleStatement `json:"body,omitempty"`
}

func copyCaseChoices(choices []CaseChoice) []CaseChoice {
	if choices == nil {
		return nil
	}
	result := make([]CaseChoice, len(choices))
	for i, choice := range choices {
		result[i] = CaseChoice{
			kind: choice.kind, value: copyRuleValue(choice.value),
			first: copyRuleValue(choice.first), last: copyRuleValue(choice.last),
		}
	}
	return result
}

type canonicalRuleStatement struct {
	Kind                 StatementKind                        `json:"kind"`
	DoName               string                               `json:"do_name,omitempty"`
	ControlDo            string                               `json:"control_do,omitempty"`
	DoControl            bool                                 `json:"do_control,omitempty"`
	Assignment           *canonicalStateAssignment            `json:"assignment,omitempty"`
	Output               *canonicalRuleOutput                 `json:"output,omitempty"`
	Timing               *canonicalActionTimingClause         `json:"timing,omitempty"`
	Condition            *canonicalRuleValue                  `json:"condition,omitempty"`
	Then                 []canonicalRuleStatement             `json:"then,omitempty"`
	Else                 []canonicalRuleStatement             `json:"else,omitempty"`
	Body                 []canonicalRuleStatement             `json:"body,omitempty"`
	Iterator             *canonicalForIterator                `json:"iterator,omitempty"`
	Expression           *canonicalRuleValue                  `json:"expression,omitempty"`
	CaseMode             CaseMode                             `json:"case_mode,omitempty"`
	Alternatives         []canonicalCaseAlternative           `json:"alternatives,omitempty"`
	Default              []canonicalRuleStatement             `json:"default,omitempty"`
	FunctionCall         *canonicalFunctionCall               `json:"function_call,omitempty"`
	Return               *canonicalRuleValue                  `json:"return,omitempty"`
	Initializer          *canonicalExecutableObjectExpression `json:"initializer,omitempty"`
	Test                 *canonicalExecutableObjectExpression `json:"test,omitempty"`
	Next                 *canonicalExecutableObjectExpression `json:"next,omitempty"`
	ContextValue         *canonicalRuleValue                  `json:"context_value,omitempty"`
	ExceptionDeclaration string                               `json:"exception_declaration,omitempty"`
	Handler              *canonicalExceptionHandler           `json:"handler,omitempty"`
}

type canonicalForIterator struct {
	Kind       string              `json:"kind"`
	Identifier string              `json:"identifier"`
	Type       string              `json:"type"`
	Generator  string              `json:"generator,omitempty"`
	Expression *canonicalRuleValue `json:"expression,omitempty"`
	First      *canonicalRuleValue `json:"first,omitempty"`
	Last       *canonicalRuleValue `json:"last,omitempty"`
}

type canonicalActionTimingClause struct {
	Kind  TimingClauseKind          `json:"kind"`
	Clock string                    `json:"clock"`
	Ticks string                    `json:"ticks,omitempty"`
	Range *canonicalTimingTickRange `json:"range,omitempty"`
}

type canonicalTimingTickRange struct {
	First string `json:"first"`
	Last  string `json:"last"`
}

type canonicalFunctionCall struct {
	ID           string                   `json:"id"`
	Name         string                   `json:"name"`
	Signature    string                   `json:"signature"`
	Arguments    []canonicalRuleParameter `json:"arguments"`
	ResultTarget string                   `json:"result_target,omitempty"`
}

func copyCaseAlternatives(alternatives []CaseAlternative) []CaseAlternative {
	if alternatives == nil {
		return nil
	}
	result := make([]CaseAlternative, len(alternatives))
	for i, alternative := range alternatives {
		result[i].choices = copyCaseChoices(alternative.choices)
		result[i].body = copyStatements(alternative.body)
	}
	return result
}

func copyStatements(statements []Statement) []Statement {
	if statements == nil {
		return nil
	}
	result := make([]Statement, len(statements))
	for i, statement := range statements {
		result[i] = statement
		result[i].doName = statement.doName
		result[i].controlDoName = statement.controlDoName
		result[i].assignment.Value = copyRuleValue(statement.assignment.Value)
		result[i].contextValue = copyRuleValue(statement.contextValue)
		result[i].output = copyRuleOutput(statement.output)
		if statement.timing != nil {
			copy := *statement.timing
			if statement.timing.Range != nil {
				rangeCopy := *statement.timing.Range
				copy.Range = &rangeCopy
			}
			result[i].timing = &copy
		}
		result[i].condition = copyRuleValue(statement.condition)
		result[i].thenBranch = copyStatements(statement.thenBranch)
		result[i].elseBranch = copyStatements(statement.elseBranch)
		result[i].loopBody = copyStatements(statement.loopBody)
		result[i].iteratorFirst = copyRuleValue(statement.iteratorFirst)
		result[i].iteratorLast = copyRuleValue(statement.iteratorLast)
		result[i].iteratorValue = copyRuleValue(statement.iteratorValue)
		result[i].forInitial = copyExecutableObjectExpression(statement.forInitial)
		result[i].forTest = copyExecutableObjectExpression(statement.forTest)
		result[i].forNext = copyExecutableObjectExpression(statement.forNext)
		result[i].caseValue = copyRuleValue(statement.caseValue)
		result[i].caseAlts = copyCaseAlternatives(statement.caseAlts)
		result[i].caseDefault = copyStatements(statement.caseDefault)
		result[i].functionCall = copyFunctionCall(statement.functionCall)
		result[i].returnValue = copyRuleValuePointer(statement.returnValue)
		result[i].raiseCondition = copyRuleValuePointer(statement.raiseCondition)
		result[i].handledBody = copyStatements(statement.handledBody)
		result[i].handler = copyExceptionHandler(statement.handler)
	}
	return result
}

func copyFunctionCall(call FunctionCall) FunctionCall {
	call.Arguments = append([]RuleParameter(nil), call.Arguments...)
	for i := range call.Arguments {
		call.Arguments[i].Value = copyRuleValue(call.Arguments[i].Value)
	}
	return call
}

func canonicalizeRuleStatements(
	component *Component,
	owner string,
	statements []Statement,
	stateTypes, placeholderTypes map[string]string,
	functions map[string]*FunctionImplementation,
	functionReturnType *string,
) ([]Statement, []canonicalRuleStatement, error) {
	return canonicalizeRuleStatementsWithProcessExit(
		component, owner, statements, stateTypes, placeholderTypes, functions, functionReturnType, false, "", false, false,
	)
}

// canonicalizeInitializerRuleStatements grants only a module initializer the
// authority to keep its synchronous action handler active while a protected
// expression allocates a fresh module. Initializers cannot suspend, so this
// does not grant the resumable process-continuation authority used by process
// bodies.
func canonicalizeInitializerRuleStatements(
	component *Component,
	owner string,
	statements []Statement,
	stateTypes, placeholderTypes map[string]string,
	functions map[string]*FunctionImplementation,
	functionReturnType *string,
) ([]Statement, []canonicalRuleStatement, error) {
	return canonicalizeRuleStatementsWithProcessExit(
		component, owner, statements, stateTypes, placeholderTypes, functions,
		functionReturnType, false, "", false, true,
	)
}

func canonicalizeRuleStatementsWithProcessExit(
	component *Component,
	owner string,
	statements []Statement,
	stateTypes, placeholderTypes map[string]string,
	functions map[string]*FunctionImplementation,
	functionReturnType *string,
	allowProcessDoControl bool,
	processDoName string,
	allowTimingSuspension bool,
	allowProcessInterruptAllocation bool,
) ([]Statement, []canonicalRuleStatement, error) {
	seenOutputs := make(map[string]bool)
	if err := validateStatementDoLabels(owner, statements, processDoName); err != nil {
		return nil, nil, err
	}
	loopDepth := 0
	var doStack []string
	if allowProcessDoControl {
		loopDepth = 1
		doStack = []string{processDoName}
	}
	return canonicalizeRuleStatementList(component, owner, statements, stateTypes, placeholderTypes, functions, functionReturnType, seenOutputs, "", loopDepth, doStack, allowProcessDoControl, allowTimingSuspension, false, allowProcessInterruptAllocation)
}

func validateStatementDoLabels(owner string, statements []Statement, processDoName string) error {
	seen := make(map[string]bool)
	if processDoName != "" {
		seen[processDoName] = true
	}
	var visitList func([]Statement) error
	visitList = func(list []Statement) error {
		for _, statement := range list {
			if statement.doName != "" {
				name := strings.ToLower(strings.TrimSpace(statement.doName))
				if !statement.doControl || !validModuleMembershipIdentifier(name) {
					return fmt.Errorf("%w: %s has invalid do label %q", ErrInvalidDeclarativeStatement, owner, statement.doName)
				}
				if seen[name] {
					return fmt.Errorf("%w: %s overloads do label %q", ErrInvalidDeclarativeStatement, owner, name)
				}
				seen[name] = true
			}
			groups := [][]Statement{
				statement.thenBranch, statement.elseBranch, statement.loopBody,
				statement.caseDefault, statement.handledBody, statement.handler.Else,
			}
			for _, alternative := range statement.caseAlts {
				groups = append(groups, alternative.body)
			}
			for _, choice := range statement.handler.Choices {
				groups = append(groups, choice.Statements)
			}
			for _, group := range groups {
				if err := visitList(group); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return visitList(statements)
}

func canonicalDoName(name string) (string, error) {
	if name == "" {
		return "", nil
	}
	normalized := strings.ToLower(strings.TrimSpace(name))
	if !validModuleMembershipIdentifier(normalized) {
		return "", fmt.Errorf("invalid do name %q", name)
	}
	return normalized, nil
}

func pushDoName(stack []string, name string) []string {
	result := append([]string(nil), stack...)
	return append(result, name)
}

func doNameIsEnclosing(stack []string, name string) bool {
	for index := len(stack) - 1; index >= 0; index-- {
		if stack[index] == name {
			return true
		}
	}
	return false
}

func canonicalizeRuleStatementList(
	component *Component,
	owner string,
	statements []Statement,
	stateTypes, placeholderTypes map[string]string,
	functions map[string]*FunctionImplementation,
	functionReturnType *string,
	seenOutputs map[string]bool,
	path string,
	loopDepth int,
	doStack []string,
	allowProcessDoControl bool,
	allowTimingSuspension bool,
	allowReraise bool,
	allowProcessInterruptAllocation bool,
) ([]Statement, []canonicalRuleStatement, error) {
	normalized := make([]Statement, 0, len(statements))
	canonical := make([]canonicalRuleStatement, 0, len(statements))
	for index, statement := range statements {
		statementPath := path + strconv.Itoa(index)
		switch statement.kind {
		case AssignmentStatement:
			normalizedAssignments, encodedAssignments, err := canonicalizeStateAssignments(
				owner+" statement "+statementPath,
				[]StateAssignment{statement.assignment}, stateTypes, placeholderTypes,
			)
			if err != nil {
				return nil, nil, fmt.Errorf("%w: %w", ErrInvalidDeclarativeStatement, err)
			}
			normalized = append(normalized, Statement{kind: AssignmentStatement, assignment: normalizedAssignments[0]})
			encoded := encodedAssignments[0]
			canonical = append(canonical, canonicalRuleStatement{Kind: AssignmentStatement, Assignment: &encoded})
		case EventCallStatement:
			output := copyRuleOutput(statement.output)
			if output.ID == "" || seenOutputs[output.ID] {
				return nil, nil, fmt.Errorf("%w: %s has empty or duplicate statement output ID %q", ErrInvalidDeclarativeStatement, owner, output.ID)
			}
			if output.Action == "" || len(output.Causes) != 0 {
				return nil, nil, fmt.Errorf("%w: %s statement output %q has no action or explicit causes", ErrInvalidDeclarativeStatement, owner, output.ID)
			}
			seenOutputs[output.ID] = true
			normalizedOutput := RuleOutput{ID: output.ID, Action: output.Action}
			canonicalOutput := canonicalRuleOutput{ID: output.ID, Action: output.Action}
			seenParameters := make(map[string]bool, len(output.Parameters))
			bound := make(map[string]bool, len(placeholderTypes))
			for name := range placeholderTypes {
				bound[name] = true
			}
			for _, parameter := range output.Parameters {
				normalizedParameter, canonicalParameter, err := canonicalizeRuleParameter(
					owner, output.ID, parameter, bound, seenParameters, stateTypes, placeholderTypes,
				)
				if err != nil {
					return nil, nil, fmt.Errorf("%w: %w", ErrInvalidDeclarativeStatement, err)
				}
				normalizedOutput.Parameters = append(normalizedOutput.Parameters, normalizedParameter)
				canonicalOutput.Parameters = append(canonicalOutput.Parameters, canonicalParameter)
			}
			sort.Slice(normalizedOutput.Parameters, func(i, j int) bool {
				return normalizedOutput.Parameters[i].Name < normalizedOutput.Parameters[j].Name
			})
			sort.Slice(canonicalOutput.Parameters, func(i, j int) bool {
				return canonicalOutput.Parameters[i].Name < canonicalOutput.Parameters[j].Name
			})
			if !ruleOutputShapeMatches(component, normalizedOutput, stateTypes, placeholderTypes) {
				return nil, nil, fmt.Errorf("%w: %s statement output %q action %s does not match a declared out- or private-action shape", ErrInvalidDeclarativeStatement, owner, output.ID, output.Action)
			}
			normalizedTiming, canonicalTiming, err := canonicalizeActionTimingClause(
				component, statement.timing, allowTimingSuspension,
			)
			if err != nil {
				return nil, nil, fmt.Errorf("%w: %s statement output %q: %w", ErrInvalidDeclarativeStatement, owner, output.ID, err)
			}
			normalized = append(normalized, Statement{kind: EventCallStatement, output: normalizedOutput, timing: normalizedTiming})
			canonical = append(canonical, canonicalRuleStatement{Kind: EventCallStatement, Output: &canonicalOutput, Timing: canonicalTiming})
		case RaiseStatementKind:
			output := copyRuleOutput(statement.output)
			if output.ID == "" || seenOutputs[output.ID] || output.Action == "" || len(output.Causes) != 0 || statement.timing != nil {
				return nil, nil, fmt.Errorf("%w: %s has malformed or duplicate raise %q",
					ErrInvalidDeclarativeStatement, owner, output.ID)
			}
			seenOutputs[output.ID] = true
			normalizedOutput := RuleOutput{ID: output.ID, Action: output.Action}
			canonicalOutput := canonicalRuleOutput{ID: output.ID, Action: output.Action}
			seenParameters := make(map[string]bool, len(output.Parameters))
			bound := make(map[string]bool, len(placeholderTypes))
			for name := range placeholderTypes {
				bound[name] = true
			}
			for _, parameter := range output.Parameters {
				normalizedParameter, canonicalParameter, err := canonicalizeRuleParameter(
					owner, output.ID, parameter, bound, seenParameters, stateTypes, placeholderTypes,
				)
				if err != nil {
					return nil, nil, fmt.Errorf("%w: %w", ErrInvalidDeclarativeStatement, err)
				}
				normalizedOutput.Parameters = append(normalizedOutput.Parameters, normalizedParameter)
				canonicalOutput.Parameters = append(canonicalOutput.Parameters, canonicalParameter)
			}
			sort.Slice(normalizedOutput.Parameters, func(i, j int) bool {
				return normalizedOutput.Parameters[i].Name < normalizedOutput.Parameters[j].Name
			})
			sort.Slice(canonicalOutput.Parameters, func(i, j int) bool {
				return canonicalOutput.Parameters[i].Name < canonicalOutput.Parameters[j].Name
			})
			declaration, exists := exceptionDeclaration(
				component, statement.exceptionDeclaration, normalizedOutput.Action,
			)
			if !exists || !exceptionOutputShapeMatches(
				component, declaration.Declaration, normalizedOutput, stateTypes, placeholderTypes,
			) {
				return nil, nil, fmt.Errorf("%w: %s raise %q does not match declared exception %s",
					ErrInvalidDeclarativeStatement, owner, output.ID, output.Action)
			}
			normalizedRaise := Statement{
				kind: RaiseStatementKind, output: normalizedOutput,
				exceptionDeclaration: declaration.Declaration,
			}
			encodedRaise := canonicalRuleStatement{
				Kind: RaiseStatementKind, Output: &canonicalOutput,
				ExceptionDeclaration: declaration.Declaration,
			}
			if statement.raiseCondition != nil {
				condition, encodedCondition, conditionType, err := canonicalizeClosedRuleValue(
					owner+" raise "+output.ID+" where", *statement.raiseCondition, stateTypes, placeholderTypes,
				)
				if err != nil || conditionType != "Boolean" {
					return nil, nil, fmt.Errorf("%w: %s raise %q where condition must be Boolean: %v",
						ErrInvalidDeclarativeStatement, owner, output.ID, err)
				}
				normalizedRaise.raiseCondition = &condition
				encodedRaise.Condition = &encodedCondition
			}
			normalized = append(normalized, normalizedRaise)
			canonical = append(canonical, encodedRaise)
		case ReraiseStatementKind:
			if !allowReraise || statement.output.ID != "" || statement.output.Action != "" ||
				len(statement.output.Parameters) != 0 || statement.timing != nil {
				return nil, nil, fmt.Errorf("%w: %s unnamed re-raise %s requires an active handler",
					ErrInvalidDeclarativeStatement, owner, statementPath)
			}
			normalizedReraise := Statement{kind: ReraiseStatementKind}
			encodedReraise := canonicalRuleStatement{Kind: ReraiseStatementKind}
			if statement.raiseCondition != nil {
				condition, encodedCondition, conditionType, err := canonicalizeClosedRuleValue(
					owner+" unnamed re-raise "+statementPath+" where",
					*statement.raiseCondition, stateTypes, placeholderTypes,
				)
				if err != nil || conditionType != "Boolean" {
					return nil, nil, fmt.Errorf("%w: %s unnamed re-raise where condition must be Boolean: %v",
						ErrInvalidDeclarativeStatement, owner, err)
				}
				normalizedReraise.raiseCondition = &condition
				encodedReraise.Condition = &encodedCondition
			}
			normalized = append(normalized, normalizedReraise)
			canonical = append(canonical, encodedReraise)
		case DoBlockStatementKind:
			if !statement.doControl {
				return nil, nil, fmt.Errorf("%w: %s plain do block %s is not a control scope",
					ErrInvalidDeclarativeStatement, owner, statementPath)
			}
			doName, err := canonicalDoName(statement.doName)
			if err != nil {
				return nil, nil, fmt.Errorf("%w: %s plain do block %s: %v",
					ErrInvalidDeclarativeStatement, owner, statementPath, err)
			}
			if len(statement.handledBody) == 0 {
				return nil, nil, fmt.Errorf("%w: %s plain do block %s is empty",
					ErrInvalidDeclarativeStatement, owner, statementPath)
			}
			body, canonicalBody, err := canonicalizeRuleStatementList(
				component, owner+" plain do", statement.handledBody,
				stateTypes, placeholderTypes, functions, functionReturnType, seenOutputs,
				statementPath+"/do/", loopDepth+1, pushDoName(doStack, doName),
				allowProcessDoControl, allowTimingSuspension, allowReraise,
				allowProcessInterruptAllocation,
			)
			if err != nil {
				return nil, nil, err
			}
			normalized = append(normalized, Statement{
				kind: DoBlockStatementKind, doControl: true, doName: doName, handledBody: body,
			})
			canonical = append(canonical, canonicalRuleStatement{
				Kind: DoBlockStatementKind, DoControl: true, DoName: doName, Body: canonicalBody,
			})
		case HandlerBlockStatementKind:
			normalizedHandler, canonicalHandler, err := canonicalizeExceptionHandlerBlock(
				component, owner, statement, stateTypes, placeholderTypes, functions,
				functionReturnType, seenOutputs, statementPath, loopDepth, doStack,
				allowProcessDoControl, allowTimingSuspension, allowReraise, true,
				allowProcessInterruptAllocation,
			)
			if err != nil {
				return nil, nil, fmt.Errorf("%w: %w", ErrInvalidDeclarativeStatement, err)
			}
			normalized = append(normalized, normalizedHandler)
			canonical = append(canonical, canonicalHandler)
		case TimedStatementKind:
			normalizedTiming, canonicalTiming, err := canonicalizeActionTimingClause(
				component, statement.timing, allowTimingSuspension,
			)
			if err != nil {
				return nil, nil, fmt.Errorf("%w: %s timed statement %s: %w", ErrInvalidDeclarativeStatement, owner, statementPath, err)
			}
			if normalizedTiming == nil || normalizedTiming.Kind == InTimingClause {
				return nil, nil, fmt.Errorf("%w: %s timed statement %s has no suspending timing form", ErrInvalidDeclarativeStatement, owner, statementPath)
			}
			normalized = append(normalized, Statement{kind: TimedStatementKind, timing: normalizedTiming})
			canonical = append(canonical, canonicalRuleStatement{Kind: TimedStatementKind, Timing: canonicalTiming})
		case LinkStatementKind, UnlinkStatementKind:
			value, encodedValue, valueType, err := canonicalizeClosedRuleValue(
				owner+" statement "+statementPath+" communication Context operand",
				statement.contextValue, stateTypes, placeholderTypes,
			)
			if err != nil {
				return nil, nil, fmt.Errorf("%w: %w", ErrInvalidDeclarativeStatement, err)
			}
			if valueType != "" && supportedPredefinedType(valueType) {
				return nil, nil, fmt.Errorf(
					"%w: %s statement %s communication Context operand has predefined type %s",
					ErrInvalidDeclarativeStatement, owner, statementPath, valueType,
				)
			}
			normalized = append(normalized, Statement{kind: statement.kind, contextValue: value})
			canonical = append(canonical, canonicalRuleStatement{Kind: statement.kind, ContextValue: &encodedValue})
		case FunctionCallStatement:
			if statement.functionCall.ID == "" || seenOutputs[statement.functionCall.ID] {
				return nil, nil, fmt.Errorf("%w: %s has empty or duplicate statement call ID %q", ErrInvalidDeclarativeStatement, owner, statement.functionCall.ID)
			}
			functionCall, encodedCall, err := canonicalizeFunctionCall(
				owner, statement.functionCall, stateTypes, placeholderTypes, functions,
			)
			if err != nil {
				return nil, nil, err
			}
			seenOutputs[statement.functionCall.ID] = true
			normalized = append(normalized, Statement{kind: FunctionCallStatement, functionCall: functionCall})
			canonical = append(canonical, canonicalRuleStatement{Kind: FunctionCallStatement, FunctionCall: &encodedCall})
		case ReturnStatementKind:
			if functionReturnType == nil {
				return nil, nil, fmt.Errorf("%w: %s statement %s uses return outside a function", ErrInvalidDeclarativeStatement, owner, statementPath)
			}
			if *functionReturnType == "" {
				if statement.returnValue != nil {
					return nil, nil, fmt.Errorf("%w: %s statement %s returns a value from a function with no return type", ErrInvalidDeclarativeStatement, owner, statementPath)
				}
				normalized = append(normalized, Statement{kind: ReturnStatementKind})
				canonical = append(canonical, canonicalRuleStatement{Kind: ReturnStatementKind})
				break
			}
			if statement.returnValue == nil {
				return nil, nil, fmt.Errorf("%w: %s statement %s omits the %s return value", ErrInvalidDeclarativeStatement, owner, statementPath, *functionReturnType)
			}
			value, encodedValue, valueType, err := canonicalizeClosedRuleValue(
				owner+" statement "+statementPath+" return", *statement.returnValue,
				stateTypes, placeholderTypes,
			)
			if err != nil {
				return nil, nil, fmt.Errorf("%w: %w", ErrInvalidDeclarativeStatement, err)
			}
			if value.kind == RuleLiteralValue {
				if !valueMatchesPredefinedType(value.literal, *functionReturnType) {
					return nil, nil, fmt.Errorf("%w: %s statement %s return does not match %s", ErrInvalidDeclarativeStatement, owner, statementPath, *functionReturnType)
				}
			} else if valueType != "" && !ruleValueAssignableToPredefined(value, valueType, *functionReturnType) {
				return nil, nil, fmt.Errorf("%w: %s statement %s return has type %s, want %s", ErrInvalidDeclarativeStatement, owner, statementPath, valueType, *functionReturnType)
			}
			normalized = append(normalized, Statement{kind: ReturnStatementKind, returnValue: &value})
			canonical = append(canonical, canonicalRuleStatement{Kind: ReturnStatementKind, Return: &encodedValue})
		case IfStatementKind:
			normalizedCondition, canonicalCondition, conditionType, err := canonicalizeClosedRuleValue(
				owner+" statement "+statementPath+" condition", statement.condition, stateTypes, placeholderTypes,
			)
			if err != nil {
				return nil, nil, fmt.Errorf("%w: %w", ErrInvalidDeclarativeStatement, err)
			}
			if conditionType != "" && conditionType != "Boolean" {
				return nil, nil, fmt.Errorf("%w: %s statement %s condition has type %s, want Boolean", ErrInvalidDeclarativeStatement, owner, statementPath, conditionType)
			}
			thenStatements, canonicalThen, err := canonicalizeRuleStatementList(
				component, owner, statement.thenBranch, stateTypes, placeholderTypes, functions, functionReturnType, seenOutputs, statementPath+"/then/",
				loopDepth, doStack, allowProcessDoControl, allowTimingSuspension, allowReraise,
				allowProcessInterruptAllocation,
			)
			if err != nil {
				return nil, nil, err
			}
			elseStatements, canonicalElse, err := canonicalizeRuleStatementList(
				component, owner, statement.elseBranch, stateTypes, placeholderTypes, functions, functionReturnType, seenOutputs, statementPath+"/else/",
				loopDepth, doStack, allowProcessDoControl, allowTimingSuspension, allowReraise,
				allowProcessInterruptAllocation,
			)
			if err != nil {
				return nil, nil, err
			}
			normalized = append(normalized, Statement{
				kind: IfStatementKind, condition: normalizedCondition,
				thenBranch: thenStatements, elseBranch: elseStatements,
			})
			canonical = append(canonical, canonicalRuleStatement{
				Kind: IfStatementKind, Condition: &canonicalCondition,
				Then: canonicalThen, Else: canonicalElse,
			})
		case NullStatementKind:
			normalized = append(normalized, Statement{kind: NullStatementKind})
			canonical = append(canonical, canonicalRuleStatement{Kind: NullStatementKind})
		case LoopStatementKind:
			if !statement.doControl {
				return nil, nil, fmt.Errorf("%w: %s loop statement %s is not a do control scope",
					ErrInvalidDeclarativeStatement, owner, statementPath)
			}
			doName, err := canonicalDoName(statement.doName)
			if err != nil {
				return nil, nil, fmt.Errorf("%w: %s loop statement %s: %v",
					ErrInvalidDeclarativeStatement, owner, statementPath, err)
			}
			body, canonicalBody, err := canonicalizeRuleStatementList(
				component, owner, statement.loopBody, stateTypes, placeholderTypes,
				functions, functionReturnType, seenOutputs, statementPath+"/loop/", loopDepth+1,
				pushDoName(doStack, doName), allowProcessDoControl, allowTimingSuspension, allowReraise,
				allowProcessInterruptAllocation,
			)
			if err != nil {
				return nil, nil, err
			}
			normalized = append(normalized, Statement{
				kind: LoopStatementKind, doControl: true, doName: doName, loopBody: body,
			})
			canonical = append(canonical, canonicalRuleStatement{
				Kind: LoopStatementKind, DoControl: true, DoName: doName, Body: canonicalBody,
			})
		case ForStatementKind:
			if !statement.doControl {
				return nil, nil, fmt.Errorf("%w: %s for statement %s is not a do control scope",
					ErrInvalidDeclarativeStatement, owner, statementPath)
			}
			doName, err := canonicalDoName(statement.doName)
			if err != nil {
				return nil, nil, fmt.Errorf("%w: %s for statement %s: %v",
					ErrInvalidDeclarativeStatement, owner, statementPath, err)
			}
			if statement.iteratorType == "" {
				return nil, nil, fmt.Errorf("%w: %s statement %s has an invalid iterator declaration", ErrInvalidDeclarativeStatement, owner, statementPath)
			}
			if _, err := gorapide.RapidePredefinedType(statement.iteratorType); err != nil {
				return nil, nil, fmt.Errorf("%w: %s statement %s iterator item type %q is unsupported", ErrInvalidDeclarativeStatement, owner, statementPath, statement.iteratorType)
			}
			var normalizedIterator canonicalForIterator
			normalizedIterator.Kind = statement.iteratorKind
			normalizedIterator.Identifier = statement.iteratorName
			normalizedIterator.Type = statement.iteratorType
			normalizedStatement := Statement{
				kind: ForStatementKind, doControl: true, doName: doName,
				iteratorKind: statement.iteratorKind,
				iteratorName: statement.iteratorName, iteratorType: statement.iteratorType,
				iteratorGenerator: statement.iteratorGenerator,
			}
			switch statement.iteratorKind {
			case rangeStatementIteratorKind:
				if statement.iteratorType != "Integer" {
					return nil, nil, fmt.Errorf("%w: %s statement %s range iterator has type %s, want Integer", ErrInvalidDeclarativeStatement, owner, statementPath, statement.iteratorType)
				}
				first, encodedFirst, firstType, err := canonicalizeClosedRuleValue(
					owner+" statement "+statementPath+" iterator first", statement.iteratorFirst,
					stateTypes, placeholderTypes,
				)
				if err != nil {
					return nil, nil, fmt.Errorf("%w: %w", ErrInvalidDeclarativeStatement, err)
				}
				last, encodedLast, lastType, err := canonicalizeClosedRuleValue(
					owner+" statement "+statementPath+" iterator last", statement.iteratorLast,
					stateTypes, placeholderTypes,
				)
				if err != nil {
					return nil, nil, fmt.Errorf("%w: %w", ErrInvalidDeclarativeStatement, err)
				}
				if !isIntegerType(firstType) || !isIntegerType(lastType) {
					return nil, nil, fmt.Errorf("%w: %s statement %s range endpoints have types %s and %s, want Integer", ErrInvalidDeclarativeStatement, owner, statementPath, firstType, lastType)
				}
				normalizedStatement.iteratorFirst = first
				normalizedStatement.iteratorLast = last
				normalizedIterator.First = &encodedFirst
				normalizedIterator.Last = &encodedLast
			case moduleStatementIteratorKind:
				value, encodedValue, _, err := canonicalizeClosedRuleValue(
					owner+" statement "+statementPath+" iterator expression", statement.iteratorValue,
					stateTypes, placeholderTypes,
				)
				if err != nil {
					return nil, nil, fmt.Errorf("%w: %w", ErrInvalidDeclarativeStatement, err)
				}
				if value.kind != RuleLiteralValue {
					return nil, nil, fmt.Errorf("%w: %s statement %s iterator expression is not a closed module value", ErrInvalidDeclarativeStatement, owner, statementPath)
				}
				if _, ok := value.literal.(gorapide.RapideModuleValue); !ok {
					return nil, nil, fmt.Errorf("%w: %s statement %s iterator expression is not an allocation-identified module", ErrInvalidDeclarativeStatement, owner, statementPath)
				}
				normalizedStatement.iteratorValue = value
				normalizedIterator.Expression = &encodedValue
			case generatorStatementIteratorKind:
				if !validModuleMembershipIdentifier(statement.iteratorGenerator) ||
					statement.iteratorGenerator != strings.ToLower(statement.iteratorGenerator) {
					return nil, nil, fmt.Errorf("%w: %s statement %s has invalid iterator generator %q",
						ErrInvalidDeclarativeStatement, owner, statementPath, statement.iteratorGenerator)
				}
				normalizedIterator.Generator = statement.iteratorGenerator
			default:
				return nil, nil, fmt.Errorf("%w: %s statement %s has iterator kind %q", ErrInvalidDeclarativeStatement, owner, statementPath, statement.iteratorKind)
			}
			bodyTypes := make(map[string]string, len(placeholderTypes)+1)
			for name, typeName := range placeholderTypes {
				bodyTypes[name] = typeName
			}
			if statement.iteratorName != "" {
				bodyTypes[statement.iteratorName] = statement.iteratorType
			}
			body, canonicalBody, err := canonicalizeRuleStatementList(
				component, owner, statement.loopBody, stateTypes, bodyTypes,
				functions, functionReturnType, seenOutputs, statementPath+"/for/", loopDepth+1,
				pushDoName(doStack, doName), allowProcessDoControl, allowTimingSuspension, allowReraise,
				allowProcessInterruptAllocation,
			)
			if err != nil {
				return nil, nil, err
			}
			normalizedStatement.loopBody = body
			normalized = append(normalized, normalizedStatement)
			canonical = append(canonical, canonicalRuleStatement{
				Kind: ForStatementKind, DoControl: true, DoName: doName, Body: canonicalBody,
				Iterator: &normalizedIterator,
			})
		case GeneralForStatementKind:
			if !statement.doControl {
				return nil, nil, fmt.Errorf("%w: %s general for statement %s is not a do control scope",
					ErrInvalidDeclarativeStatement, owner, statementPath)
			}
			doName, err := canonicalDoName(statement.doName)
			if err != nil {
				return nil, nil, fmt.Errorf("%w: %s general for statement %s: %v",
					ErrInvalidDeclarativeStatement, owner, statementPath, err)
			}
			initial, canonicalInitial, _, err := canonicalizeExecutableObjectExpression(
				owner+" statement "+statementPath+" initializer",
				statement.forInitial, stateTypes, placeholderTypes, functions, seenOutputs,
			)
			if err != nil {
				return nil, nil, fmt.Errorf("%w: %w", ErrInvalidDeclarativeStatement, err)
			}
			test, canonicalTest, testType, err := canonicalizeExecutableObjectExpression(
				owner+" statement "+statementPath+" test",
				statement.forTest, stateTypes, placeholderTypes, functions, seenOutputs,
			)
			if err != nil {
				return nil, nil, fmt.Errorf("%w: %w", ErrInvalidDeclarativeStatement, err)
			}
			if testType != "Boolean" {
				return nil, nil, fmt.Errorf(
					"%w: %s statement %s test has type %s, want Boolean",
					ErrInvalidDeclarativeStatement, owner, statementPath, testType,
				)
			}
			next, canonicalNext, _, err := canonicalizeExecutableObjectExpression(
				owner+" statement "+statementPath+" next",
				statement.forNext, stateTypes, placeholderTypes, functions, seenOutputs,
			)
			if err != nil {
				return nil, nil, fmt.Errorf("%w: %w", ErrInvalidDeclarativeStatement, err)
			}
			body, canonicalBody, err := canonicalizeRuleStatementList(
				component, owner, statement.loopBody, stateTypes, placeholderTypes,
				functions, functionReturnType, seenOutputs, statementPath+"/for/",
				loopDepth+1, pushDoName(doStack, doName),
				allowProcessDoControl, allowTimingSuspension, allowReraise,
				allowProcessInterruptAllocation,
			)
			if err != nil {
				return nil, nil, err
			}
			normalized = append(normalized, Statement{
				kind: GeneralForStatementKind, doControl: true, doName: doName,
				forInitial: initial, forTest: test,
				forNext: next, loopBody: body,
			})
			canonical = append(canonical, canonicalRuleStatement{
				Kind: GeneralForStatementKind, DoControl: true, DoName: doName,
				Initializer: &canonicalInitial,
				Test:        &canonicalTest, Next: &canonicalNext, Body: canonicalBody,
			})
		case ExitStatementKind, NextStatementKind:
			if loopDepth == 0 {
				return nil, nil, fmt.Errorf("%w: %s statement %s uses %s outside a do statement", ErrInvalidDeclarativeStatement, owner, statementPath, statement.kind)
			}
			normalizedCondition, canonicalCondition, conditionType, err := canonicalizeClosedRuleValue(
				owner+" statement "+statementPath+" condition", statement.condition, stateTypes, placeholderTypes,
			)
			if err != nil {
				return nil, nil, fmt.Errorf("%w: %w", ErrInvalidDeclarativeStatement, err)
			}
			if conditionType != "" && conditionType != "Boolean" {
				return nil, nil, fmt.Errorf("%w: %s statement %s condition has type %s, want Boolean", ErrInvalidDeclarativeStatement, owner, statementPath, conditionType)
			}
			controlDoName, err := canonicalDoName(statement.controlDoName)
			if err != nil {
				return nil, nil, fmt.Errorf("%w: %s statement %s: %v",
					ErrInvalidDeclarativeStatement, owner, statementPath, err)
			}
			if controlDoName != "" && !doNameIsEnclosing(doStack, controlDoName) {
				return nil, nil, fmt.Errorf("%w: %s statement %s names non-enclosing do %q",
					ErrInvalidDeclarativeStatement, owner, statementPath, controlDoName)
			}
			normalized = append(normalized, Statement{
				kind: statement.kind, controlDoName: controlDoName, condition: normalizedCondition,
			})
			canonical = append(canonical, canonicalRuleStatement{
				Kind: statement.kind, ControlDo: controlDoName, Condition: &canonicalCondition,
			})
		case ExitWhenStatementKind:
			if !allowProcessDoControl {
				return nil, nil, fmt.Errorf("%w: %s statement %s uses %s outside a source-equivalent when", ErrInvalidDeclarativeStatement, owner, statementPath, statement.kind)
			}
			normalizedCondition, canonicalCondition, conditionType, err := canonicalizeClosedRuleValue(
				owner+" statement "+statementPath+" condition", statement.condition, stateTypes, placeholderTypes,
			)
			if err != nil {
				return nil, nil, fmt.Errorf("%w: %w", ErrInvalidDeclarativeStatement, err)
			}
			if conditionType != "" && conditionType != "Boolean" {
				return nil, nil, fmt.Errorf("%w: %s statement %s condition has type %s, want Boolean", ErrInvalidDeclarativeStatement, owner, statementPath, conditionType)
			}
			normalized = append(normalized, Statement{kind: ExitWhenStatementKind, condition: normalizedCondition})
			canonical = append(canonical, canonicalRuleStatement{Kind: ExitWhenStatementKind, Condition: &canonicalCondition})
		case CaseStatementKind:
			if statement.caseMode != CaseXorMode && statement.caseMode != CaseOrMode && statement.caseMode != CaseElseMode {
				return nil, nil, fmt.Errorf("%w: %s statement %s has case mode %q", ErrInvalidDeclarativeStatement, owner, statementPath, statement.caseMode)
			}
			if len(statement.caseAlts) == 0 {
				return nil, nil, fmt.Errorf("%w: %s statement %s has no case alternatives", ErrInvalidDeclarativeStatement, owner, statementPath)
			}
			value, encodedValue, valueType, err := canonicalizeClosedRuleValue(
				owner+" statement "+statementPath+" case expression", statement.caseValue, stateTypes, placeholderTypes,
			)
			if err != nil {
				return nil, nil, fmt.Errorf("%w: %w", ErrInvalidDeclarativeStatement, err)
			}
			alternatives := make([]CaseAlternative, len(statement.caseAlts))
			encodedAlternatives := make([]canonicalCaseAlternative, len(statement.caseAlts))
			for alternativeIndex, alternative := range statement.caseAlts {
				if len(alternative.choices) == 0 {
					return nil, nil, fmt.Errorf("%w: %s statement %s alternative %d has no choices", ErrInvalidDeclarativeStatement, owner, statementPath, alternativeIndex)
				}
				alternatives[alternativeIndex].choices = make([]CaseChoice, len(alternative.choices))
				encodedAlternatives[alternativeIndex].Choices = make([]canonicalCaseChoice, len(alternative.choices))
				for choiceIndex, choice := range alternative.choices {
					choiceOwner := fmt.Sprintf("%s statement %s alternative %d choice %d", owner, statementPath, alternativeIndex, choiceIndex)
					switch choice.kind {
					case caseValueChoiceKind:
						normalizedChoice, encodedChoice, choiceType, err := canonicalizeClosedRuleValue(
							choiceOwner, choice.value, stateTypes, placeholderTypes,
						)
						if err != nil {
							return nil, nil, fmt.Errorf("%w: %w", ErrInvalidDeclarativeStatement, err)
						}
						if valueType != "" && choiceType != "" && !ruleValueAssignableToPredefined(normalizedChoice, choiceType, valueType) {
							return nil, nil, fmt.Errorf("%w: %s compares case type %s with choice type %s", ErrInvalidDeclarativeStatement, choiceOwner, valueType, choiceType)
						}
						alternatives[alternativeIndex].choices[choiceIndex] = CaseValueChoice(normalizedChoice)
						encodedAlternatives[alternativeIndex].Choices[choiceIndex] = canonicalCaseChoice{
							Kind: caseValueChoiceKind, Value: &encodedChoice,
						}
					case caseRangeChoiceKind:
						first, encodedFirst, firstType, err := canonicalizeClosedRuleValue(
							choiceOwner+" range first", choice.first, stateTypes, placeholderTypes,
						)
						if err != nil {
							return nil, nil, fmt.Errorf("%w: %w", ErrInvalidDeclarativeStatement, err)
						}
						last, encodedLast, lastType, err := canonicalizeClosedRuleValue(
							choiceOwner+" range last", choice.last, stateTypes, placeholderTypes,
						)
						if err != nil {
							return nil, nil, fmt.Errorf("%w: %w", ErrInvalidDeclarativeStatement, err)
						}
						if !isIntegerType(valueType) || !isIntegerType(firstType) || !isIntegerType(lastType) {
							return nil, nil, fmt.Errorf("%w: %s integer range has case/endpoint types %s, %s, %s", ErrInvalidDeclarativeStatement, choiceOwner, valueType, firstType, lastType)
						}
						if !ruleValueAssignableToPredefined(first, firstType, valueType) ||
							!ruleValueAssignableToPredefined(last, lastType, valueType) {
							return nil, nil, fmt.Errorf("%w: %s integer range endpoints are not objects of case type %s", ErrInvalidDeclarativeStatement, choiceOwner, valueType)
						}
						alternatives[alternativeIndex].choices[choiceIndex] = CaseRangeChoice(first, last)
						encodedAlternatives[alternativeIndex].Choices[choiceIndex] = canonicalCaseChoice{
							Kind: caseRangeChoiceKind, First: &encodedFirst, Last: &encodedLast,
						}
					default:
						return nil, nil, fmt.Errorf("%w: %s has choice kind %q", ErrInvalidDeclarativeStatement, choiceOwner, choice.kind)
					}
				}
				body, encodedBody, err := canonicalizeRuleStatementList(
					component, owner, alternative.body, stateTypes, placeholderTypes, functions, functionReturnType, seenOutputs,
					fmt.Sprintf("%s/case/%d/", statementPath, alternativeIndex), loopDepth, doStack,
					allowProcessDoControl, allowTimingSuspension, allowReraise,
					allowProcessInterruptAllocation,
				)
				if err != nil {
					return nil, nil, err
				}
				alternatives[alternativeIndex].body = body
				encodedAlternatives[alternativeIndex].Body = encodedBody
			}
			defaultBody, encodedDefault, err := canonicalizeRuleStatementList(
				component, owner, statement.caseDefault, stateTypes, placeholderTypes, functions, functionReturnType, seenOutputs,
				statementPath+"/default/", loopDepth, doStack,
				allowProcessDoControl, allowTimingSuspension, allowReraise,
				allowProcessInterruptAllocation,
			)
			if err != nil {
				return nil, nil, err
			}
			normalized = append(normalized, Statement{
				kind: CaseStatementKind, caseValue: value, caseMode: statement.caseMode,
				caseAlts: alternatives, caseDefault: defaultBody,
			})
			canonical = append(canonical, canonicalRuleStatement{
				Kind: CaseStatementKind, Expression: &encodedValue, CaseMode: statement.caseMode,
				Alternatives: encodedAlternatives, Default: encodedDefault,
			})
		case AssertStatementKind:
			condition, encodedCondition, conditionType, err := canonicalizeClosedRuleValue(
				owner+" statement "+statementPath+" assertion", statement.condition, stateTypes, placeholderTypes,
			)
			if err != nil {
				return nil, nil, fmt.Errorf("%w: %w", ErrInvalidDeclarativeStatement, err)
			}
			if conditionType != "" && conditionType != "Boolean" {
				return nil, nil, fmt.Errorf("%w: %s statement %s assertion has type %s, want Boolean", ErrInvalidDeclarativeStatement, owner, statementPath, conditionType)
			}
			normalized = append(normalized, Statement{kind: AssertStatementKind, condition: condition})
			canonical = append(canonical, canonicalRuleStatement{Kind: AssertStatementKind, Condition: &encodedCondition})
		default:
			return nil, nil, fmt.Errorf("%w: %s statement %s has kind %q", ErrInvalidDeclarativeStatement, owner, statementPath, statement.kind)
		}
	}
	return normalized, canonical, nil
}

type statementExecution struct {
	generated             []generatedRuleOutput
	scheduled             []scheduledAction
	control               []gorapide.EventID
	reads                 []StateReadRecord
	writes                []StateWriteRecord
	returned              bool
	returnValue           any
	exitProcess           bool
	raised                *raisedExceptionOccurrence
	handledExceptions     []*raisedExceptionOccurrence
	interruptHandlers     []activeInterruptHandler
	pendingInterrupt      *interruptHandlerInvocation
	generationConnections *generationTimeConnectionState
	loopControlDoName     string
	clocks                *deterministicClockKernel
	owner                 string
	budget                *statementBudget
	pendingOperations     []stateOperationReference
	initializationFailure *failedModuleInitialization
	canceledSchedules     []string
	initializationOwned   bool
}

func incorporateEvaluatedStateReads(
	execution *statementExecution,
	reads []StateReadRecord,
	causes []gorapide.EventID,
) error {
	if execution == nil {
		return fmt.Errorf("%w: statement execution is nil", ErrInvalidDeclarativeStatement)
	}
	priorControl := append([]gorapide.EventID(nil), execution.control...)
	operations := stateOperationReferences(reads, nil)
	dependencies := append(eventIDStrings(priorControl), stateOperationReferenceIDs(execution.pendingOperations)...)
	if err := addStateOperationDependencies(operations, dependencies...); err != nil {
		return err
	}
	execution.control = canonicalEventIDs(append(execution.control, causes...))
	execution.reads = append(execution.reads, reads...)
	execution.pendingOperations = canonicalStateOperationReferences(append(execution.pendingOperations, operations...))
	return nil
}

type statementControl int

const (
	statementContinue statementControl = iota
	statementExitLoop
	statementNextLoop
	statementReturnFunction
	statementExitProcess
	statementRaiseException
	statementHandleInterrupt
)

func doConsumesControl(statement Statement, execution *statementExecution) bool {
	if !statement.doControl || execution == nil {
		return false
	}
	return execution.loopControlDoName == "" || execution.loopControlDoName == statement.doName
}

func consumeDoControl(execution *statementExecution) {
	if execution != nil {
		execution.loopControlDoName = ""
	}
}

type patternModuleBindingRuntime struct {
	nameID string
}

func acquirePatternModuleBindings(
	componentID, ruleID, matchDigest string,
	match pattern.MatchResult,
	runtime *functionExecutionRuntime,
) ([]patternModuleBindingRuntime, error) {
	if runtime == nil || runtime.lifecycle == nil {
		return nil, nil
	}
	acquiredAfter := make([]gorapide.EventID, 0, len(match.Events))
	for _, event := range match.Events {
		if event != nil {
			acquiredAfter = append(acquiredAfter, event.ID)
		}
	}
	acquiredAfter = canonicalEventIDs(acquiredAfter)
	result := make([]patternModuleBindingRuntime, 0)
	for _, binding := range match.Bindings {
		module, ok := binding.Value.(gorapide.RapideModuleValue)
		if !ok {
			continue
		}
		owner := runtime.modules[componentID]
		if owner.Identity() == "" {
			return nil, fmt.Errorf("%w: component %q has no module allocation for pattern binding %q", ErrInvalidDeclarativeStatement, componentID, binding.Placeholder)
		}
		if module.Identity() == "" || len(acquiredAfter) == 0 {
			return nil, fmt.Errorf("%w: pattern binding %q has an invalid module value or acquisition frontier", ErrInvalidDeclarativeStatement, binding.Placeholder)
		}
		nameID := "pattern-binding:" + componentID + "/" + ruleID + "/" + matchDigest + "/" + binding.Placeholder + "/" + module.Identity()
		if err := runtime.lifecycle.addName(moduleNameRuntime{
			nameID: nameID, moduleID: module.Identity(), owner: owner.Identity(),
			name: "?" + binding.Placeholder, kind: "pattern-binding", acquiredAfter: acquiredAfter,
		}); err != nil {
			return nil, err
		}
		result = append(result, patternModuleBindingRuntime{nameID: nameID})
	}
	return result, nil
}

func releasePatternModuleBindings(
	modelDigest string,
	bindings []patternModuleBindingRuntime,
	runtime *functionExecutionRuntime,
	execution *statementExecution,
) error {
	frontier := canonicalEventIDs(execution.control)
	if len(bindings) != 0 && len(frontier) == 0 {
		return fmt.Errorf("%w: pattern module-binding scope has no causal exit frontier", ErrInvalidDeclarativeStatement)
	}
	for _, binding := range bindings {
		if _, err := releaseModuleName(modelDigest, binding.nameID, frontier, runtime, execution); err != nil {
			return err
		}
	}
	return nil
}

func bindModuleSelf(
	match pattern.MatchResult,
	componentID string,
	runtime *functionExecutionRuntime,
) (pattern.MatchResult, error) {
	if runtime == nil {
		return match, nil
	}
	module := runtime.modules[componentID]
	if module.Identity() == "" {
		return match, nil
	}
	for _, binding := range match.Bindings {
		if binding.Placeholder != moduleSelfBindingName {
			continue
		}
		equal, err := gorapide.CanonicalValuesEqual(binding.Value, module)
		if err != nil || !equal {
			return pattern.MatchResult{}, fmt.Errorf("%w: component %q has conflicting Self binding", ErrInvalidStateReference, componentID)
		}
		return match, nil
	}
	match.Bindings = append(append(pattern.Bindings(nil), match.Bindings...), pattern.Binding{
		Placeholder: moduleSelfBindingName,
		Value:       module,
	})
	sort.Slice(match.Bindings, func(left, right int) bool {
		return match.Bindings[left].Placeholder < match.Bindings[right].Placeholder
	})
	return match, nil
}

func resolveStatementRuleParameters(
	componentID, ruleID, statementPath, modelDigest string,
	output RuleOutput,
	bindings pattern.Bindings,
	cells map[string]*stateCell,
	runtime *functionExecutionRuntime,
	execution *statementExecution,
) (map[string]any, []StateReadRecord, []gorapide.EventID, []allocatedModuleActual, error) {
	parameters := make(map[string]any, len(output.Parameters))
	reads := make([]StateReadRecord, 0)
	var readCauses []gorapide.EventID
	allocations := make([]allocatedModuleActual, 0)
	for index, parameter := range output.Parameters {
		if parameter.Value.kind == RuleNewValue {
			occurrence := "component=" + componentID + "|rule=" + ruleID +
				"|statement=" + statementPath + "|output=" + output.ID +
				"|parameter=" + strconv.Itoa(index) + ":" + parameter.Name
			module, allocation, err := allocateModuleNewActual(
				componentID, modelDigest, occurrence, parameter.Value.newType,
				parameter.Value.newArguments, parameter.Value.newInitializationArguments,
				bindings, cells, runtime, execution,
			)
			if err != nil {
				return nil, nil, nil, nil, err
			}
			if execution.initializationFailure != nil {
				after := execution.initializationFailure.raised.event.ID
				for _, prior := range allocations {
					if err := releaseAllocatedModuleActual(
						modelDigest, prior, after, runtime, execution,
					); err != nil {
						return nil, nil, nil, nil, err
					}
				}
				return nil, nil, nil, nil, nil
			}
			if execution.pendingInterrupt != nil {
				after := execution.pendingInterrupt.eventID
				for _, prior := range allocations {
					if err := releaseAllocatedModuleActual(
						modelDigest, prior, after, runtime, execution,
					); err != nil {
						return nil, nil, nil, nil, err
					}
				}
				return nil, nil, nil, nil, nil
			}
			parameters[parameter.Name] = module
			allocations = append(allocations, allocation)
			continue
		}
		value, causes, expressionReads, err := evaluateRuleValue(
			"rule "+ruleID+" output "+output.ID, parameter.Value, bindings, cells,
		)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		parameters[parameter.Name] = value
		readCauses = append(readCauses, causes...)
		reads = append(reads, expressionReads...)
	}
	canonical, err := gorapide.CanonicalizeParams(parameters)
	return canonical, reads, canonicalEventIDs(readCauses), allocations, err
}

func executeRuleStatements(
	componentID string,
	component *Component,
	rule *DeclarativeRule,
	match pattern.MatchResult,
	matchDigest, modelDigest string,
	statements []Statement,
	functionRuntime *functionExecutionRuntime,
	cells map[string]*stateCell,
	initialControl []gorapide.EventID,
	budget *statementBudget,
	clocks *deterministicClockKernel,
	causalOwner string,
	initialOperations []stateOperationReference,
	outerExecution *statementExecution,
	handledExceptions ...*raisedExceptionOccurrence,
) (statementExecution, error) {
	var err error
	moduleBindings, err := acquirePatternModuleBindings(
		componentID, rule.ID, matchDigest, match, functionRuntime,
	)
	if err != nil {
		return statementExecution{}, err
	}
	match, err = bindModuleSelf(match, componentID, functionRuntime)
	if err != nil {
		return statementExecution{}, err
	}
	execution := statementExecution{
		control: canonicalEventIDs(initialControl), clocks: clocks, owner: causalOwner,
		budget:              budget,
		pendingOperations:   canonicalStateOperationReferences(initialOperations),
		initializationOwned: rule != nil && rule.initializationOwned,
	}
	if outerExecution != nil {
		for index := range outerExecution.interruptHandlers {
			active := outerExecution.interruptHandlers[index]
			// New executes its fresh initializer synchronously inside the caller's
			// protected computation. A process handler or an initializer-owned
			// handler crosses this exact boundary. Initializer ownership is upgraded
			// to generation-aware only here, after the parent execution contains the
			// fresh Start occurrence needed by connection closure. Process ownership
			// remains the separate authority to survive scheduler suspension.
			if active.initializationOwned && handlerHasInterruptChoice(active.handler) {
				active.generationAware = true
				outerExecution.interruptHandlers[index].generationAware = true
			}
			if active.processOwned || (active.initializationOwned && handlerHasInterruptChoice(active.handler)) {
				execution.interruptHandlers = append(execution.interruptHandlers, active)
			}
		}
		retainInitializerGenerationTimeConnectionState(outerExecution, &execution)
		inheritGenerationTimeConnectionState(outerExecution, &execution)
	}
	for _, handled := range handledExceptions {
		if handled != nil && handled.event != nil {
			execution.handledExceptions = append(execution.handledExceptions, handled)
		}
	}
	control, err := executeRuleStatementList(
		componentID, component, rule, match, matchDigest, modelDigest,
		statements, functionRuntime, cells, "", &execution, budget,
	)
	if err != nil {
		return statementExecution{}, err
	}
	if err := releasePatternModuleBindings(modelDigest, moduleBindings, functionRuntime, &execution); err != nil {
		return statementExecution{}, err
	}
	if control == statementExitProcess {
		execution.exitProcess = true
		return execution, nil
	}
	if (control == statementExitLoop || control == statementNextLoop) && rule.allowProcessDoControl &&
		(execution.loopControlDoName == "" || execution.loopControlDoName == rule.processDoName) {
		consumeDoControl(&execution)
		if control == statementExitLoop {
			execution.exitProcess = true
		}
		return execution, nil
	}
	if control == statementRaiseException {
		if execution.raised == nil || execution.raised.event == nil {
			return statementExecution{}, fmt.Errorf("%w: missing exception occurrence", ErrUnhandledRapideException)
		}
		return execution, nil
	}
	if control == statementHandleInterrupt {
		if outerExecution != nil && execution.pendingInterrupt != nil {
			return execution, nil
		}
		return statementExecution{}, fmt.Errorf("%w: interrupt escaped every active handler",
			ErrInvalidExceptionHandler)
	}
	if control != statementContinue {
		return statementExecution{}, fmt.Errorf("%w: loop control escaped the statement body", ErrInvalidDeclarativeStatement)
	}
	return execution, nil
}

func executeRuleStatementList(
	componentID string,
	component *Component,
	rule *DeclarativeRule,
	match pattern.MatchResult,
	matchDigest, modelDigest string,
	statements []Statement,
	functionRuntime *functionExecutionRuntime,
	cells map[string]*stateCell,
	path string,
	execution *statementExecution,
	budget *statementBudget,
) (statementControl, error) {
	return executeRuleStatementListFrom(
		componentID, component, rule, match, matchDigest, modelDigest,
		statements, functionRuntime, cells, path, 0, execution, budget,
	)
}

func executeRuleStatementListFrom(
	componentID string,
	component *Component,
	rule *DeclarativeRule,
	match pattern.MatchResult,
	matchDigest, modelDigest string,
	statements []Statement,
	functionRuntime *functionExecutionRuntime,
	cells map[string]*stateCell,
	path string,
	firstIndex int,
	execution *statementExecution,
	budget *statementBudget,
) (statementControl, error) {
	for index, statement := range statements {
		if err := budget.consume(); err != nil {
			return statementContinue, err
		}
		statementPath := path + strconv.Itoa(firstIndex+index)
		switch statement.kind {
		case AssignmentStatement:
			reads, writes, err := applyStateAssignments(
				rule.ID+" statement "+statementPath,
				[]StateAssignment{statement.assignment}, match.Bindings, cells, execution.control,
				execution.pendingOperations,
			)
			if err != nil {
				return statementContinue, err
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
		case EventCallStatement:
			for _, parameter := range statement.output.Parameters {
				if parameter.Value.kind == RuleNewValue && statement.timing != nil {
					return statementContinue, fmt.Errorf(
						"%w: allocator New cannot cross a timed action boundary in the current slice",
						ErrInvalidDeclarativeStatement)
				}
			}
			parameters, reads, stateCauses, allocations, err := resolveStatementRuleParameters(
				componentID, rule.ID, statementPath, modelDigest, statement.output,
				match.Bindings, cells, functionRuntime, execution,
			)
			if err != nil {
				return statementContinue, err
			}
			if execution.initializationFailure != nil {
				return statementExitProcess, nil
			}
			if execution.pendingInterrupt != nil {
				return statementHandleInterrupt, nil
			}
			readOperations := stateOperationReferences(reads, nil)
			dependencies := append(eventIDStrings(execution.control), stateOperationReferenceIDs(execution.pendingOperations)...)
			if err := addStateOperationDependencies(readOperations, dependencies...); err != nil {
				return statementContinue, err
			}
			eventOperations := canonicalStateOperationReferences(append(execution.pendingOperations, readOperations...))
			for _, allocation := range allocations {
				eventOperations = canonicalStateOperationReferences(append(
					eventOperations, allocation.operations...,
				))
			}
			if !interfaceMatchesGeneratedAction(component, statement.output.Action, parameters) {
				return statementContinue, fmt.Errorf("%w: output action %s.%s", ErrActionTypeMismatch, componentID, statement.output.Action)
			}
			controlCauses := execution.control
			if len(allocations) != 0 {
				controlCauses = nil
				for _, allocation := range allocations {
					controlCauses = append(controlCauses, allocation.frontier...)
				}
			}
			causes := canonicalEventIDs(append(append([]gorapide.EventID(nil), controlCauses...), stateCauses...))
			occurrence := rule.ID + "|match=" + matchDigest + "|statement=" + statementPath + "|output=" + statement.output.ID
			if statement.timing != nil {
				if statement.timing.Kind != InTimingClause || execution.clocks == nil {
					return statementContinue, fmt.Errorf("%w: statement %s has an invalid normalized timing clause", ErrInvalidDeclarativeStatement, statementPath)
				}
				ticks, err := execution.clocks.resolveTimingTicks(
					execution.owner+"\x00"+occurrence+"\x00"+statement.timing.Clock,
					statement.timing,
				)
				if err != nil {
					return statementContinue, err
				}
				if ticks != 0 {
					deadline, err := execution.clocks.deadline(statement.timing.Clock, ticks)
					if err != nil {
						return statementContinue, err
					}
					execution.scheduled = append(execution.scheduled, scheduledAction{
						scheduleID: scheduledActionID(execution.owner, occurrence, statement.timing.Clock, deadline),
						owner:      execution.owner, componentID: componentID, localID: statement.output.ID,
						clock: statement.timing.Clock, deadline: deadline, action: statement.output.Action,
						occurrence: occurrence, params: parameters, stateCauses: canonicalEventIDs(stateCauses),
						stateOperations: eventOperations, acquiredAfter: causes,
					})
					execution.reads = append(execution.reads, reads...)
					execution.pendingOperations = nil
					break
				}
			}
			event, err := gorapide.NewDeterministicEvent(gorapide.EventProvenance{
				Profile: CompatibilityProfile, Model: modelDigest, Instance: componentID,
				Action:     statement.output.Action,
				Occurrence: occurrence,
				Causes:     causes,
				Timings:    execution.clocks.instantTimings(componentID),
			}, parameters)
			if err != nil {
				return statementContinue, err
			}
			if err := addStateOperationSuccessors(eventOperations, string(event.ID)); err != nil {
				return statementContinue, err
			}
			execution.generated = append(execution.generated, generatedRuleOutput{
				localID: statement.output.ID, event: event, causes: causes,
				stateSnapshot: cloneStateCells(cells),
			})
			for _, allocation := range allocations {
				if err := releaseAllocatedModuleActual(
					modelDigest, allocation, event.ID, functionRuntime, execution,
				); err != nil {
					return statementContinue, err
				}
			}
			execution.control = []gorapide.EventID{event.ID}
			execution.pendingOperations = nil
			execution.reads = append(execution.reads, reads...)
			invocation, err := selectGeneratedInterruptHandler(
				execution, event, match, functionRuntime,
			)
			if err != nil {
				return statementContinue, err
			}
			if invocation != nil {
				execution.pendingInterrupt = invocation
				return statementHandleInterrupt, nil
			}
		case RaiseStatementKind:
			if statement.raiseCondition != nil {
				evaluated, err := evaluateClosedRuleValue(
					rule.ID+" statement "+statementPath+" raise condition",
					*statement.raiseCondition, match.Bindings, cells,
				)
				if err != nil {
					return statementContinue, err
				}
				if err := incorporateEvaluatedStateReads(execution, evaluated.reads, evaluated.causes); err != nil {
					return statementContinue, err
				}
				raise, ok := evaluated.value.(bool)
				if !ok {
					return statementContinue, fmt.Errorf("%w: raise where condition evaluated to %T",
						ErrInvalidDeclarativeStatement, evaluated.value)
				}
				if !raise {
					break
				}
			}
			parameters, reads, readCauses, allocations, err := resolveStatementRuleParameters(
				componentID, rule.ID, statementPath, modelDigest,
				statement.output, match.Bindings, cells, functionRuntime, execution,
			)
			if err != nil {
				return statementContinue, err
			}
			if len(allocations) != 0 {
				return statementContinue, fmt.Errorf("%w: exception actual allocated a module",
					ErrInvalidDeclarativeStatement)
			}
			causes := canonicalEventIDs(append(append([]gorapide.EventID(nil), execution.control...), readCauses...))
			occurrence := rule.ID + "|match=" + matchDigest + "|statement=" + statementPath +
				"|raise=" + statement.output.ID
			event, err := gorapide.NewDeterministicEvent(gorapide.EventProvenance{
				Profile: CompatibilityProfile, Model: modelDigest, Instance: componentID,
				Action: statement.output.Action, Occurrence: occurrence, Causes: causes,
				Timings: execution.clocks.instantTimings(componentID),
			}, parameters)
			if err != nil {
				return statementContinue, err
			}
			if err := addStateOperationSuccessors(execution.pendingOperations, string(event.ID)); err != nil {
				return statementContinue, err
			}
			execution.generated = append(execution.generated, generatedRuleOutput{
				localID: "raise@" + statement.output.ID, event: event, causes: causes,
				stateSnapshot: cloneStateCells(cells), exception: true,
			})
			execution.control = []gorapide.EventID{event.ID}
			execution.pendingOperations = nil
			execution.reads = append(execution.reads, reads...)
			execution.raised = &raisedExceptionOccurrence{
				name: statement.output.Action, declaration: statement.exceptionDeclaration, event: event,
			}
			return statementRaiseException, nil
		case ReraiseStatementKind:
			if statement.raiseCondition != nil {
				evaluated, err := evaluateClosedRuleValue(
					rule.ID+" statement "+statementPath+" unnamed re-raise condition",
					*statement.raiseCondition, match.Bindings, cells,
				)
				if err != nil {
					return statementContinue, err
				}
				if err := incorporateEvaluatedStateReads(execution, evaluated.reads, evaluated.causes); err != nil {
					return statementContinue, err
				}
				reraise, ok := evaluated.value.(bool)
				if !ok {
					return statementContinue, fmt.Errorf("%w: unnamed re-raise where condition evaluated to %T",
						ErrInvalidDeclarativeStatement, evaluated.value)
				}
				if !reraise {
					break
				}
			}
			if len(execution.handledExceptions) == 0 {
				return statementContinue, fmt.Errorf("%w: unnamed re-raise has no active handled occurrence",
					ErrInvalidExceptionHandler)
			}
			execution.raised = execution.handledExceptions[len(execution.handledExceptions)-1]
			return statementRaiseException, nil
		case DoBlockStatementKind:
			control, err := executeRuleStatementList(
				componentID, component, rule, match, matchDigest, modelDigest,
				statement.handledBody, functionRuntime, cells, statementPath+"d/", execution, budget,
			)
			if err != nil {
				return statementContinue, err
			}
			if (control == statementExitLoop || control == statementNextLoop) &&
				doConsumesControl(statement, execution) {
				consumeDoControl(execution)
				break
			}
			if control != statementContinue {
				return control, err
			}
		case HandlerBlockStatementKind:
			activationID := execution.owner + "\x00" + statementPath
			execution.interruptHandlers = append(execution.interruptHandlers, activeInterruptHandler{
				id: activationID, owner: componentID,
				initializationOwned: execution.initializationOwned, handler: statement.handler,
				outerMatch: cloneProcessMatch(match), outerSet: true,
			})
			control, err := executeRuleStatementList(
				componentID, component, rule, match, matchDigest, modelDigest,
				statement.handledBody, functionRuntime, cells, statementPath+"b/", execution, budget,
			)
			execution.interruptHandlers = execution.interruptHandlers[:len(execution.interruptHandlers)-1]
			if err != nil {
				return statementContinue, err
			}
			if control == statementHandleInterrupt {
				invocation := execution.pendingInterrupt
				if invocation == nil {
					return statementContinue, fmt.Errorf("%w: interrupt transfer has no selected handler",
						ErrInvalidExceptionHandler)
				}
				if invocation.targetID != activationID {
					return statementHandleInterrupt, nil
				}
				handlerMatchDigest, err := pattern.SemanticDigestMatches([]pattern.MatchResult{invocation.match})
				if err != nil {
					return statementContinue, err
				}
				execution.pendingInterrupt = nil
				control, err = executeRuleStatementList(
					componentID, component, rule, invocation.match, handlerMatchDigest, modelDigest,
					invocation.statements, functionRuntime, cells, statementPath+"i/", execution, budget,
				)
				if err != nil {
					return statementContinue, err
				}
				if control != statementContinue {
					if (control == statementExitLoop || control == statementNextLoop) &&
						doConsumesControl(statement, execution) {
						consumeDoControl(execution)
						break
					}
					return control, nil
				}
				break
			}
			if control != statementRaiseException {
				if control == statementContinue {
					break
				}
				if (control == statementExitLoop || control == statementNextLoop) &&
					doConsumesControl(statement, execution) {
					consumeDoControl(execution)
					break
				}
				return control, nil
			}
			handlerStatements, handlerMatch, handled, err := selectExceptionHandler(
				statement.handler, execution.raised, match,
			)
			if err != nil {
				return statementContinue, err
			}
			if !handled {
				return statementRaiseException, nil
			}
			handlerMatchDigest, err := pattern.SemanticDigestMatches([]pattern.MatchResult{handlerMatch})
			if err != nil {
				return statementContinue, err
			}
			handledOccurrence := execution.raised
			execution.raised = nil
			execution.handledExceptions = append(execution.handledExceptions, handledOccurrence)
			control, err = executeRuleStatementList(
				componentID, component, rule, handlerMatch, handlerMatchDigest, modelDigest,
				handlerStatements, functionRuntime, cells, statementPath+"h/", execution, budget,
			)
			execution.handledExceptions = execution.handledExceptions[:len(execution.handledExceptions)-1]
			if err != nil {
				return statementContinue, err
			}
			if control != statementContinue {
				if (control == statementExitLoop || control == statementNextLoop) &&
					doConsumesControl(statement, execution) {
					consumeDoControl(execution)
					break
				}
				return control, nil
			}
		case LinkStatementKind, UnlinkStatementKind:
			evaluated, err := evaluateClosedRuleValue(
				rule.ID+" statement "+statementPath+" communication Context operand",
				statement.contextValue, match.Bindings, cells,
			)
			if err != nil {
				return statementContinue, err
			}
			if err := incorporateEvaluatedStateReads(execution, evaluated.reads, evaluated.causes); err != nil {
				return statementContinue, err
			}
			source, ok := evaluated.value.(gorapide.RapideModuleValue)
			if !ok || source.Identity() == "" {
				return statementContinue, fmt.Errorf(
					"%w: statement %s communication Context operand evaluated to %T",
					ErrInvalidDeclarativeStatement, statementPath, evaluated.value,
				)
			}
			if functionRuntime == nil || functionRuntime.contexts == nil {
				return statementContinue, fmt.Errorf("%w: communication Context runtime is unavailable", ErrInvalidDeclarativeStatement)
			}
			destination := functionRuntime.modules[componentID]
			if destination.Identity() == "" {
				return statementContinue, fmt.Errorf(
					"%w: component %q has no module allocation for communication Context",
					ErrInvalidDeclarativeStatement, componentID,
				)
			}
			operationID := componentID + "/" + rule.ID + "/" + matchDigest + "/" + statementPath
			if statement.kind == LinkStatementKind {
				if err := functionRuntime.contexts.link(
					"link:"+operationID, source.Identity(), destination.Identity(), execution.control,
				); err != nil {
					return statementContinue, err
				}
				break
			}
			nameID, err := functionRuntime.contexts.unlink(
				source.Identity(), destination.Identity(), execution.control,
			)
			if err != nil {
				return statementContinue, err
			}
			if nameID != "" {
				if _, err := releaseModuleName(
					modelDigest, nameID, execution.control, functionRuntime, execution,
				); err != nil {
					return statementContinue, err
				}
			}
		case FunctionCallStatement:
			returned, err := executeFunctionCall(
				componentID, component, rule, match, matchDigest, modelDigest, statementPath,
				statement.functionCall, functionRuntime, cells, execution, budget,
			)
			if err != nil {
				return statementContinue, err
			}
			if execution.initializationFailure != nil {
				return statementExitProcess, nil
			}
			if execution.raised != nil {
				return statementRaiseException, nil
			}
			if execution.pendingInterrupt != nil {
				return statementHandleInterrupt, nil
			}
			if statement.functionCall.ResultTarget != "" {
				reads, writes, err := applyStateAssignments(
					rule.ID+" statement "+statementPath+" function result",
					[]StateAssignment{AssignState(statement.functionCall.ResultTarget, LiteralValue(returned))},
					nil, cells, execution.control, execution.pendingOperations,
				)
				if err != nil {
					return statementContinue, err
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
			}
		case ReturnStatementKind:
			if statement.returnValue != nil {
				evaluated, err := evaluateClosedRuleValue(
					rule.ID+" statement "+statementPath+" return",
					*statement.returnValue, match.Bindings, cells,
				)
				if err != nil {
					return statementContinue, err
				}
				execution.returnValue = evaluated.value
				if err := incorporateEvaluatedStateReads(execution, evaluated.reads, evaluated.causes); err != nil {
					return statementContinue, err
				}
			}
			execution.returned = true
			return statementReturnFunction, nil
		case IfStatementKind:
			condition, err := evaluateClosedRuleValue(
				rule.ID+" statement "+statementPath+" condition",
				statement.condition, match.Bindings, cells,
			)
			if err != nil {
				return statementContinue, err
			}
			selected, ok := condition.value.(bool)
			if !ok {
				return statementContinue, fmt.Errorf("%w: statement %s condition evaluated to %T", ErrInvalidDeclarativeStatement, statementPath, condition.value)
			}
			if err := incorporateEvaluatedStateReads(execution, condition.reads, condition.causes); err != nil {
				return statementContinue, err
			}
			branch := statement.elseBranch
			branchName := "else/"
			if selected {
				branch = statement.thenBranch
				branchName = "then/"
			}
			control, err := executeRuleStatementList(
				componentID, component, rule, match, matchDigest, modelDigest,
				branch, functionRuntime, cells, statementPath+"/"+branchName, execution,
				budget,
			)
			if err != nil || control != statementContinue {
				return control, err
			}
		case NullStatementKind:
			// Intentionally no effect.
		case LoopStatementKind:
			for iteration := uint64(1); ; iteration++ {
				iterationPath := statementPath + "/iteration/" + strconv.FormatUint(iteration, 10) + "/"
				control, err := executeRuleStatementList(
					componentID, component, rule, match, matchDigest, modelDigest,
					statement.loopBody, functionRuntime, cells, iterationPath, execution, budget,
				)
				if err != nil {
					return statementContinue, err
				}
				if control == statementExitLoop || control == statementNextLoop {
					if !doConsumesControl(statement, execution) {
						return control, nil
					}
					consumeDoControl(execution)
					if control == statementExitLoop {
						break
					}
					continue
				}
				if control == statementReturnFunction || control == statementExitProcess ||
					control == statementRaiseException || control == statementHandleInterrupt {
					return control, nil
				}
				// Both normal completion and next restart the nearest loop.
			}
		case ForStatementKind:
			iterator, err := initializeStatementIterator(
				componentID, rule, match, matchDigest, modelDigest, statementPath,
				statement, functionRuntime, cells, execution,
			)
			if err != nil {
				return statementContinue, err
			}
			for iteration := uint64(1); ; iteration++ {
				if err := budget.consume(); err != nil {
					return statementContinue, err
				}
				more := iterator.more()
				if err := executeFiniteIteratorProtocolCall(
					componentID, modelDigest, statementPath, iteration,
					"More", "Boolean", more, iterator, cells, execution,
				); err != nil {
					return statementContinue, err
				}
				if !more {
					break
				}
				if err := budget.consume(); err != nil {
					return statementContinue, err
				}
				item, err := iterator.item()
				if err != nil {
					return statementContinue, err
				}
				if err := executeFiniteIteratorProtocolCall(
					componentID, modelDigest, statementPath, iteration,
					"Item", iterator.itemType, item, iterator, cells, execution,
				); err != nil {
					return statementContinue, err
				}
				iterationMatch := match
				iterationMatch.Bindings = bindingsWithIteratorValue(match.Bindings, statement.iteratorName, item)
				control, err := executeRuleStatementList(
					componentID, component, rule, iterationMatch, matchDigest, modelDigest,
					statement.loopBody, functionRuntime, cells,
					statementPath+"/iteration/"+strconv.FormatUint(iteration, 10)+"/",
					execution, budget,
				)
				if err != nil {
					return statementContinue, err
				}
				if control == statementExitLoop || control == statementNextLoop {
					if !doConsumesControl(statement, execution) {
						if err := releaseStatementIterator(
							componentID, modelDigest, statementPath, iterator, functionRuntime, execution,
						); err != nil {
							return statementContinue, err
						}
						return control, nil
					}
					consumeDoControl(execution)
					if control == statementExitLoop {
						break
					}
					continue
				}
				if control == statementReturnFunction || control == statementExitProcess ||
					control == statementRaiseException || control == statementHandleInterrupt {
					if err := releaseStatementIterator(
						componentID, modelDigest, statementPath, iterator, functionRuntime, execution,
					); err != nil {
						return statementContinue, err
					}
					return control, nil
				}
				// Both normal completion and next request the next More/Item pair.
			}
			if err := releaseStatementIterator(
				componentID, modelDigest, statementPath, iterator, functionRuntime, execution,
			); err != nil {
				return statementContinue, err
			}
		case GeneralForStatementKind:
			if _, err := executeExecutableObjectExpression(
				componentID, component, rule, match, matchDigest, modelDigest,
				statementPath+"/initializer", statement.forInitial, functionRuntime,
				cells, execution, budget,
			); err != nil {
				return statementContinue, err
			}
			if execution.initializationFailure != nil {
				return statementExitProcess, nil
			}
			if execution.raised != nil {
				return statementRaiseException, nil
			}
			for iteration := uint64(1); ; iteration++ {
				iterationPath := statementPath + "/iteration/" + strconv.FormatUint(iteration, 10)
				test, err := executeExecutableObjectExpression(
					componentID, component, rule, match, matchDigest, modelDigest,
					iterationPath+"/test", statement.forTest, functionRuntime,
					cells, execution, budget,
				)
				if err != nil {
					return statementContinue, err
				}
				if execution.initializationFailure != nil {
					return statementExitProcess, nil
				}
				if execution.raised != nil {
					return statementRaiseException, nil
				}
				selected, ok := test.(bool)
				if !ok {
					return statementContinue, fmt.Errorf(
						"%w: statement %s test evaluated to %T",
						ErrInvalidDeclarativeStatement, statementPath, test,
					)
				}
				if !selected {
					break
				}
				control, err := executeRuleStatementList(
					componentID, component, rule, match, matchDigest, modelDigest,
					statement.loopBody, functionRuntime, cells, iterationPath+"/body/",
					execution, budget,
				)
				if err != nil {
					return statementContinue, err
				}
				if control == statementExitLoop || control == statementNextLoop {
					if !doConsumesControl(statement, execution) {
						return control, nil
					}
					consumeDoControl(execution)
					if control == statementExitLoop {
						break
					}
					// A next in the body still executes the next expression.
				}
				if control == statementReturnFunction || control == statementExitProcess ||
					control == statementRaiseException || control == statementHandleInterrupt {
					return control, nil
				}
				if _, err := executeExecutableObjectExpression(
					componentID, component, rule, match, matchDigest, modelDigest,
					iterationPath+"/next", statement.forNext, functionRuntime,
					cells, execution, budget,
				); err != nil {
					return statementContinue, err
				}
				if execution.initializationFailure != nil {
					return statementExitProcess, nil
				}
				if execution.raised != nil {
					return statementRaiseException, nil
				}
				if iteration == ^uint64(0) {
					return statementContinue, fmt.Errorf(
						"%w: statement %s iteration overflows", ErrExecutionLimit, statementPath,
					)
				}
				// Both normal completion and next execute the next expression.
			}
		case ExitStatementKind, NextStatementKind:
			condition, err := evaluateClosedRuleValue(
				rule.ID+" statement "+statementPath+" condition",
				statement.condition, match.Bindings, cells,
			)
			if err != nil {
				return statementContinue, err
			}
			selected, ok := condition.value.(bool)
			if !ok {
				return statementContinue, fmt.Errorf("%w: statement %s condition evaluated to %T", ErrInvalidDeclarativeStatement, statementPath, condition.value)
			}
			if err := incorporateEvaluatedStateReads(execution, condition.reads, condition.causes); err != nil {
				return statementContinue, err
			}
			if selected && statement.kind == ExitStatementKind {
				execution.loopControlDoName = statement.controlDoName
				return statementExitLoop, nil
			}
			if selected && statement.kind == NextStatementKind {
				execution.loopControlDoName = statement.controlDoName
				return statementNextLoop, nil
			}
		case ExitWhenStatementKind:
			condition, err := evaluateClosedRuleValue(
				rule.ID+" statement "+statementPath+" condition",
				statement.condition, match.Bindings, cells,
			)
			if err != nil {
				return statementContinue, err
			}
			selected, ok := condition.value.(bool)
			if !ok {
				return statementContinue, fmt.Errorf("%w: statement %s condition evaluated to %T", ErrInvalidDeclarativeStatement, statementPath, condition.value)
			}
			if err := incorporateEvaluatedStateReads(execution, condition.reads, condition.causes); err != nil {
				return statementContinue, err
			}
			if selected {
				return statementExitProcess, nil
			}
		case CaseStatementKind:
			value, err := evaluateClosedRuleValue(
				rule.ID+" statement "+statementPath+" case expression",
				statement.caseValue, match.Bindings, cells,
			)
			if err != nil {
				return statementContinue, err
			}
			if err := incorporateEvaluatedStateReads(execution, value.reads, value.causes); err != nil {
				return statementContinue, err
			}
			eligible := make([]int, 0, len(statement.caseAlts))
			for alternativeIndex, alternative := range statement.caseAlts {
				matched := false
				for choiceIndex, choice := range alternative.choices {
					choiceOwner := fmt.Sprintf("%s statement %s alternative %d choice %d", rule.ID, statementPath, alternativeIndex, choiceIndex)
					switch choice.kind {
					case caseValueChoiceKind:
						candidate, err := evaluateClosedRuleValue(choiceOwner, choice.value, match.Bindings, cells)
						if err != nil {
							return statementContinue, err
						}
						if err := incorporateEvaluatedStateReads(execution, candidate.reads, candidate.causes); err != nil {
							return statementContinue, err
						}
						equal, err := gorapide.CanonicalValuesEqual(value.value, candidate.value)
						if err != nil {
							return statementContinue, err
						}
						matched = equal
					case caseRangeChoiceKind:
						first, err := evaluateClosedRuleValue(choiceOwner+" range first", choice.first, match.Bindings, cells)
						if err != nil {
							return statementContinue, err
						}
						last, err := evaluateClosedRuleValue(choiceOwner+" range last", choice.last, match.Bindings, cells)
						if err != nil {
							return statementContinue, err
						}
						if err := incorporateEvaluatedStateReads(execution, first.reads, first.causes); err != nil {
							return statementContinue, err
						}
						if err := incorporateEvaluatedStateReads(execution, last.reads, last.causes); err != nil {
							return statementContinue, err
						}
						selectorInteger, selectorOK := value.value.(int64)
						firstInteger, firstOK := first.value.(int64)
						lastInteger, lastOK := last.value.(int64)
						if !selectorOK || !firstOK || !lastOK {
							return statementContinue, fmt.Errorf("%w: %s range evaluated to %T, %T, %T", ErrInvalidDeclarativeStatement, choiceOwner, value.value, first.value, last.value)
						}
						matched = firstInteger <= lastInteger && selectorInteger >= firstInteger && selectorInteger <= lastInteger
					default:
						return statementContinue, fmt.Errorf("%w: %s has choice kind %q", ErrInvalidDeclarativeStatement, choiceOwner, choice.kind)
					}
					if matched {
						break
					}
				}
				if matched {
					eligible = append(eligible, alternativeIndex)
					if statement.caseMode == CaseElseMode {
						break
					}
				}
			}
			if statement.caseMode == CaseXorMode && len(eligible) > 1 {
				return statementContinue, fmt.Errorf("%w: statement %s alternatives %d and %d", ErrCaseChoiceConflict, statementPath, eligible[0], eligible[1])
			}
			if len(eligible) == 0 {
				control, err := executeRuleStatementList(
					componentID, component, rule, match, matchDigest, modelDigest,
					statement.caseDefault, functionRuntime, cells, statementPath+"/default/", execution, budget,
				)
				if err != nil || control != statementContinue {
					return control, err
				}
				break
			}
			for _, alternativeIndex := range eligible {
				control, err := executeRuleStatementList(
					componentID, component, rule, match, matchDigest, modelDigest,
					statement.caseAlts[alternativeIndex].body, functionRuntime, cells,
					fmt.Sprintf("%s/case/%d/", statementPath, alternativeIndex), execution, budget,
				)
				if err != nil || control != statementContinue {
					return control, err
				}
			}
		case AssertStatementKind:
			condition, err := evaluateClosedRuleValue(
				rule.ID+" statement "+statementPath+" assertion",
				statement.condition, match.Bindings, cells,
			)
			if err != nil {
				return statementContinue, err
			}
			consistent, ok := condition.value.(bool)
			if !ok {
				return statementContinue, fmt.Errorf("%w: statement %s assertion evaluated to %T", ErrInvalidDeclarativeStatement, statementPath, condition.value)
			}
			if err := incorporateEvaluatedStateReads(execution, condition.reads, condition.causes); err != nil {
				return statementContinue, err
			}
			if consistent {
				break
			}
			causes := canonicalEventIDs(execution.control)
			event, err := gorapide.NewDeterministicEvent(gorapide.EventProvenance{
				Profile: CompatibilityProfile, Model: modelDigest, Instance: componentID,
				Action: "Inconsistent", Occurrence: rule.ID + "|match=" + matchDigest + "|statement=" + statementPath + "|assert",
				Causes: causes, Timings: execution.clocks.instantTimings(componentID),
			}, nil)
			if err != nil {
				return statementContinue, err
			}
			if err := addStateOperationSuccessors(execution.pendingOperations, string(event.ID)); err != nil {
				return statementContinue, err
			}
			execution.generated = append(execution.generated, generatedRuleOutput{
				localID: "assert@" + statementPath, event: event, causes: causes,
				stateSnapshot: cloneStateCells(cells),
			})
			execution.control = []gorapide.EventID{event.ID}
			execution.pendingOperations = nil
		case TimedStatementKind:
			return statementContinue, fmt.Errorf("%w: timed statement %s requires a resumable process", ErrInvalidDeclarativeStatement, statementPath)
		default:
			return statementContinue, fmt.Errorf("%w: statement %s has kind %q", ErrInvalidDeclarativeStatement, statementPath, statement.kind)
		}
	}
	return statementContinue, nil
}

// Package rapide parses and type-checks Stanford Rapide source before lowering
// supported constructs into the deterministic GoRapide kernel.
package rapide

// Position is a one-based source coordinate.
type Position struct {
	Offset int `json:"offset"`
	Line   int `json:"line"`
	Column int `json:"column"`
}

// File is the source-order-independent declaration model produced by Parse.
type File struct {
	Interfaces    []InterfaceDecl
	Exceptions    []ExceptionDecl
	Unions        []UnionDecl
	Enumerations  []EnumerationDecl
	TypeAliases   []TypeAliasDecl
	Modules       []ModuleDecl
	Maps          []MapDecl
	Architectures []ArchitectureDecl
}

// UnionDecl is the named source shorthand from Predefined Types LRM Chapter
// 10. Tags are distinct labeled member types. The declaration elaborates to
// the manual's dependent polymorphic function type; this AST does not create a
// nominal Union kind.
type UnionDecl struct {
	Position Position
	Name     string
	Tags     []UnionTagDecl
}

type UnionTagDecl struct {
	Position       Position
	Name           string
	Type           string
	TypeExpression TypeExpressionDecl
}

// EnumerationDecl is the named shorthand from Predefined Types LRM Chapter
// 11. Its distinct identifiers are the exhaustive tags of a Union whose member
// type is Triv; source order is retained only for diagnostics.
type EnumerationDecl struct {
	Position Position
	Name     string
	Literals []EnumerationLiteralDecl
}

type EnumerationLiteralDecl struct {
	Position Position
	Name     string
}

type TypeExpressionKind string

const (
	TypeExpressionName        TypeExpressionKind = "name"
	TypeExpressionApplication TypeExpressionKind = "application"
)

// TypeExpressionDecl is the closed name/application portion of Stanford's
// structural type-expression grammar. Applications retain their tree until
// elaboration; no source spelling becomes nominal type identity.
type TypeExpressionDecl struct {
	Position  Position
	Kind      TypeExpressionKind
	Name      string
	Arguments []TypeExpressionDecl
}

// TypeAliasDecl represents Stanford's `type Identifier is type_expression;`
// plus the closed finite `type Identifier is range First..Last;` restoration.
// Target is retained for compatibility and diagnostics; Expression is the
// authoritative parsed tree for ordinary aliases. The declaration name is
// absent from structural RapideType identity.
type TypeAliasDecl struct {
	Position     Position
	Name         string
	Target       string
	Expression   TypeExpressionDecl
	IntegerRange bool
	FirstIndex   int64
	LastIndex    int64
}

type InterfaceDecl struct {
	Position Position
	Name     string
	// Record marks the restricted Record type-expression surface form. Record
	// types elaborate to the same structural interface denotation as their
	// fields; the marker exists only to enforce that Record derivations include
	// Record types rather than arbitrary interfaces.
	Record      bool
	Derivations []InterfaceDerivationDecl
	Actions     []ActionDecl
	// Exceptions contains both source interface exception constituents and
	// lexically visible outer declarations on compiler-private copies. The
	// Constituent marker distinguishes structural members from visibility-only
	// declarations without losing their exact lexical identities.
	Exceptions []ExceptionDecl
	// SelectedExceptions is a compiler-private catalog of exact exception
	// constituents denoted through selection on the generated module object.
	// It remains separate from Exceptions because a module-local declaration
	// may hide an interface constituent only for unqualified lookup.
	SelectedExceptions []ExceptionDecl
	// ExceptionScopes is compiler-private metadata for Stanford's scope-name
	// notation. Each path names one enclosing declarative region and contains
	// only exceptions owned by that region; suffix paths such as Inner::E and
	// full paths such as Module::Inner::E therefore stay exact.
	ExceptionScopes  []ExceptionScopeDecl
	Functions        []FunctionDecl
	ModuleGenerators []InterfaceModuleGeneratorDecl
	Services         []InterfaceServiceDecl
	Objects          []InterfaceObjectDecl
	TypeNames        []InterfaceTypeNameDecl
	TypeConstructors []InterfaceTypeConstructorDecl
	Constraints      []ConstraintDecl
	Behavior         *BehaviorDecl
}

type ExceptionScopeDecl struct {
	Path       []string
	Exceptions []ExceptionDecl
}

type InterfaceNameRegion string

const (
	InterfaceNameProvides InterfaceNameRegion = "provides"
	InterfaceNameRequires InterfaceNameRegion = "requires"
	InterfaceNamePrivate  InterfaceNameRegion = "private"
)

// InterfaceObjectDecl is one normalized object name declaration. A source
// declaration with an identifier list expands into one entry per identifier,
// matching Type LRM normalization step 3.
type InterfaceObjectDecl struct {
	Position       Position
	Region         InterfaceNameRegion
	Name           string
	Type           string
	TypeExpression TypeExpressionDecl
}

// InterfaceModuleGeneratorDecl is a module-generator name constituent in an
// interface declarative region. Parameters retains the ordered closed object
// and unbounded/bounded type formals used for structural conformance.
type InterfaceModuleGeneratorDecl struct {
	Position             Position
	Region               InterfaceNameRegion
	Name                 string
	Parameters           []InterfaceFormalParameterDecl
	ReturnType           string
	ReturnTypeExpression TypeExpressionDecl
}

// InterfaceServiceDecl is a basic scalar or closed Integer-range service-set
// declaration whose target constituents are structurally qualified by Name.
// Dual records the published provides/requires and in/out reversal.
type InterfaceServiceDecl struct {
	Position       Position
	Name           string
	Dual           bool
	IntegerSet     bool
	FirstIndex     int64
	LastIndex      int64
	Type           string
	TypeExpression TypeExpressionDecl
}

type InterfaceTypeNameSpecification string

const (
	InterfaceTypeNameAny     InterfaceTypeNameSpecification = "any"
	InterfaceTypeNameSubtype InterfaceTypeNameSpecification = "subtype"
	InterfaceTypeNameExact   InterfaceTypeNameSpecification = "exact"
)

type InterfaceTypeNameDecl struct {
	Position       Position
	Region         InterfaceNameRegion
	Name           string
	Specification  InterfaceTypeNameSpecification
	Type           string
	TypeExpression TypeExpressionDecl
}

type InterfaceFormalParameterKind string

const (
	InterfaceFormalObjectParameter InterfaceFormalParameterKind = "object"
	InterfaceFormalTypeParameter   InterfaceFormalParameterKind = "type"
)

// InterfaceFormalParameterDecl is one normalized formal object or type
// parameter of a type-constructor name declaration. Type is an object type or
// optional formal-type bound. Default object denotations are not yet part of
// the closed source subset.
type InterfaceFormalParameterDecl struct {
	Position       Position
	Kind           InterfaceFormalParameterKind
	Name           string
	Type           string
	TypeExpression TypeExpressionDecl
}

// InterfaceTypeConstructorDecl is the closed named-expression subset of the
// Type LRM's three type-constructor denotation specifications.
type InterfaceTypeConstructorDecl struct {
	Position       Position
	Region         InterfaceNameRegion
	Name           string
	Parameters     []InterfaceFormalParameterDecl
	Specification  InterfaceTypeNameSpecification
	Type           string
	TypeExpression TypeExpressionDecl
}

// InterfaceDerivationRegion records which Stanford interface declarative
// region contains an include declaration. A derivation before the interface
// keyword copies every currently represented name declaration; a derivation
// within the default/provides or requires region copies only that region.
type InterfaceDerivationRegion string

const (
	InterfaceDerivationAll      InterfaceDerivationRegion = "interface"
	InterfaceDerivationProvides InterfaceDerivationRegion = "provides"
	InterfaceDerivationRequires InterfaceDerivationRegion = "requires"
	InterfaceDerivationPrivate  InterfaceDerivationRegion = "private"
)

type InterfaceDerivationModifier string

const (
	InterfaceDerivationUnmodified InterfaceDerivationModifier = ""
	InterfaceDerivationOnly       InterfaceDerivationModifier = "only"
	InterfaceDerivationExcept     InterfaceDerivationModifier = "except"
)

// InterfaceDerivationDecl is the immutable parsed form of Stanford's
// `include T [only|except (...)] [replace (...)]` declaration. Source is
// deliberately a single interface identifier in the current compatibility
// subset; richer type expressions fail explicitly in the parser.
type InterfaceDerivationDecl struct {
	Position     Position
	Source       string
	Region       InterfaceDerivationRegion
	Modifier     InterfaceDerivationModifier
	Names        []string
	Replacements []InterfaceReplacementDecl
}

type InterfaceReplacementDecl struct {
	Position Position
	From     string
	To       string
}

type ActionMode string

const (
	ActionIn      ActionMode = "in"
	ActionOut     ActionMode = "out"
	ActionPrivate ActionMode = "private"
)

type ActionDecl struct {
	Position   Position
	Mode       ActionMode
	Name       string
	Parameters []ParameterDecl
}

// ExceptionDecl is one lexically visible exception-event declaration. A
// constituent declaration belongs to an interface provides/requires/private
// region; outermost and module-local declarations leave Region empty.
type ExceptionDecl struct {
	Position    Position
	Declaration string
	Region      InterfaceNameRegion
	Constituent bool
	Name        string
	Parameters  []ParameterDecl
}

type FunctionMode string

const (
	FunctionProvides FunctionMode = "provides"
	FunctionRequires FunctionMode = "requires"
	FunctionPrivate  FunctionMode = "private"
)

type FunctionDecl struct {
	Position             Position
	Mode                 FunctionMode
	Name                 string
	Parameters           []ParameterDecl
	ReturnType           string
	ReturnTypeExpression TypeExpressionDecl
}

// BehaviorDecl is the initial source-compatible interface behavior subset.
// Function bodies are declarations before the behavior's begin marker; basic
// transition rules follow it. A bounded binary compound-pattern and procedural
// statement subset is represented explicitly; unsupported forms are rejected.
type BehaviorDecl struct {
	Position  Position
	States    []StateDecl
	Functions []FunctionBodyDecl
	Rules     []BehaviorRuleDecl
}

type StateDecl struct {
	Position       Position
	Name           string
	Type           string
	TypeExpression TypeExpressionDecl
	Initial        *ExpressionDecl
}

// ModuleObjectDecl is one normalized executable object declaration. A source
// identifier list expands to one entry per name because the Executable LRM
// requires the initializer expression to be evaluated separately for each
// name.
type ModuleObjectDecl struct {
	Position       Position
	Name           string
	Type           string
	TypeExpression TypeExpressionDecl
	Initial        ExpressionDecl
}

type FunctionBodyDecl struct {
	Position             Position
	Name                 string
	Parameters           []ParameterDecl
	ReturnType           string
	ReturnTypeExpression TypeExpressionDecl
	Objects              []ModuleObjectDecl
	Statements           []BehaviorStatementDecl
	Handler              *BehaviorHandlerDecl
	Return               *ExpressionDecl
}

type BehaviorStatementKind string

const (
	BehaviorCallStatement       BehaviorStatementKind = "call"
	BehaviorAssignmentStatement BehaviorStatementKind = "assignment"
	BehaviorIfStatement         BehaviorStatementKind = "if"
	BehaviorDoStatement         BehaviorStatementKind = "do"
	BehaviorLoopStatement       BehaviorStatementKind = "loop"
	BehaviorForStatement        BehaviorStatementKind = "for"
	BehaviorExitStatement       BehaviorStatementKind = "exit"
	BehaviorNextStatement       BehaviorStatementKind = "next"
	BehaviorReturnStatement     BehaviorStatementKind = "return"
	BehaviorCaseStatement       BehaviorStatementKind = "case"
	BehaviorAssertStatement     BehaviorStatementKind = "assert"
	BehaviorNullStatement       BehaviorStatementKind = "null"
	BehaviorTimedStatement      BehaviorStatementKind = "timed"
	BehaviorRaiseStatement      BehaviorStatementKind = "raise"
	BehaviorReraiseStatement    BehaviorStatementKind = "reraise"
)

type BehaviorStatementDecl struct {
	Position     Position
	Kind         BehaviorStatementKind
	Label        string
	Terminator   string
	ControlDo    string
	Call         CallStatementDecl
	Target       string
	Expression   ExpressionDecl
	Function     *CallStatementDecl
	Condition    *ExpressionDecl
	Then         []BehaviorStatementDecl
	Else         []BehaviorStatementDecl
	Body         []BehaviorStatementDecl
	Handler      *BehaviorHandlerDecl
	Exceptions   []ExceptionDecl
	Iterator     string
	IteratorType string
	RangeFirst   ExpressionDecl
	RangeLast    ExpressionDecl
	ForInitial   *BehaviorObjectExpressionDecl
	ForTest      *BehaviorObjectExpressionDecl
	ForNext      *BehaviorObjectExpressionDecl
	CaseMode     string
	Cases        []BehaviorCaseAlternativeDecl
	Default      []BehaviorStatementDecl
	Timing       *TimingDecl
}

type BehaviorObjectExpressionKind string

const (
	BehaviorObjectValue      BehaviorObjectExpressionKind = "value"
	BehaviorObjectFunction   BehaviorObjectExpressionKind = "function-call"
	BehaviorObjectAssignment BehaviorObjectExpressionKind = "assignment"
)

// BehaviorObjectExpressionDecl is one top-level object expression in the
// initializer/test/next for form. Function application and Ref-style
// assignment are kept distinct from pure expressions so their effects survive
// lowering.
type BehaviorObjectExpressionDecl struct {
	Position   Position
	Kind       BehaviorObjectExpressionKind
	Expression ExpressionDecl
	Call       CallStatementDecl
	Target     string
}

type BehaviorCaseAlternativeDecl struct {
	Position Position
	Choices  []BehaviorCaseChoiceDecl
	Body     []BehaviorStatementDecl
}

type BehaviorCaseChoiceDecl struct {
	Position Position
	First    ExpressionDecl
	Last     *ExpressionDecl
}

type CallStatementDecl struct {
	Position        Position
	Name            string
	Arguments       []ExpressionDecl
	ArgumentFormals []string
	Timing          *TimingDecl
}

type TimingKind string

const (
	TimingIn    TimingKind = "in"
	TimingPause TimingKind = "pause"
	TimingDelay TimingKind = "delay"
)

// TimingDecl is the closed source-level timing-expression subset. Value is a
// fixed C.Ticks(expression) awaiting elaboration. RangeFirst and RangeLast are
// closed bounds of C.Ticks range First..Last awaiting the same elaboration.
// Name holds a local named object of one exact clock's dependent Ticks type.
// Equal resolved bounds preserve the canonical tick object; distinct bounds
// preserve the finite type-valued form. Exported/general dependent objects and
// general subtypes remain explicit future work.
type TimingDecl struct {
	Position   Position
	Kind       TimingKind
	Clock      string
	Name       string
	First      uint64
	Last       uint64
	Value      *ExpressionDecl
	RangeFirst *ExpressionDecl
	RangeLast  *ExpressionDecl
}

type BehaviorRuleDecl struct {
	Position     Position
	Placeholders []ParameterDecl
	Trigger      BehaviorPatternDecl
	Guard        *ExpressionDecl
	Connector    Connector
	Statements   []BehaviorStatementDecl
}

// MapDecl is a Stanford Rapide map generator declaration. Domains and Range
// retain the declared indicators; the identity of each active actual domain
// belongs to a map-generator application and is therefore supplied separately
// to compilation. The current executable source slice accepts one named
// interface domain and a finite restricted poset generator composed from
// action calls, causal sequence, immediate sequence, causal independence,
// bounded full-causal-preorder join, finite disjunction, finite iteration, and
// parentheses.
type MapDecl struct {
	Position   Position
	Name       string
	Parameters []ParameterDecl
	Domains    []TypeExpressionDecl
	Range      TypeExpressionDecl
	Rules      []MapRuleDecl
}

// MapRuleDecl keeps the map grammar's restricted poset generator separate
// from an ordinary procedural statement list. Generator is nil for the
// published null body.
type MapRuleDecl struct {
	Position     Position
	Placeholders []ParameterDecl
	Trigger      BehaviorPatternDecl
	Guard        *ExpressionDecl
	Connector    Connector
	Generator    *PosetGeneratorDecl
}

type PosetGeneratorKind string

const (
	PosetGeneratorEmpty     PosetGeneratorKind = "empty"
	PosetGeneratorEvent     PosetGeneratorKind = "event"
	PosetGeneratorBinary    PosetGeneratorKind = "binary"
	PosetGeneratorIteration PosetGeneratorKind = "iteration"
)

// PosetGeneratorDecl is the source form of a finite restricted pattern used
// to generate events. It is deliberately distinct from BehaviorPatternDecl:
// event arguments here are output expressions, not match associations.
type PosetGeneratorDecl struct {
	Position Position
	Kind     PosetGeneratorKind
	Call     CallStatementDecl
	Operator string
	Left     *PosetGeneratorDecl
	Right    *PosetGeneratorDecl
	Iterator string
	First    int64
	Last     int64
	Minimum  int
	Maximum  int
	Relation string
	Inner    *PosetGeneratorDecl
}

type BehaviorPatternKind string

const (
	BehaviorBasicPattern     BehaviorPatternKind = "basic"
	BehaviorBinaryPattern    BehaviorPatternKind = "binary"
	BehaviorIterationPattern BehaviorPatternKind = "iteration"
)

type BehaviorPatternDecl struct {
	Position Position
	Kind     BehaviorPatternKind
	Event    BehaviorEventDecl
	Operator string
	Left     *BehaviorPatternDecl
	Right    *BehaviorPatternDecl
	Iterator string
	First    int64
	Last     int64
	Minimum  int
	Maximum  int
	Relation string
	Inner    *BehaviorPatternDecl
}

type BehaviorEventDecl struct {
	Position             Position
	Component            string
	ComponentPlaceholder bool
	ComponentIndex       *ExpressionDecl
	Name                 string
	Attribute            string
	Path                 []QualifiedMemberSegmentDecl
	Arguments            []PatternParameterAssociationDecl
}

// QualifiedMemberSegmentDecl preserves an indexed service path structurally
// until connection-generator iterators have been substituted. Name remains the
// flattened execution-facing adapter after elaboration.
type QualifiedMemberSegmentDecl struct {
	Position Position
	Name     string
	Index    *ExpressionDecl
}

// PatternParameterAssociationDecl preserves Stanford Rapide's positional and
// named basic-pattern associations. An empty Formal denotes a positional
// association; a nonempty Formal denotes "formal is actual". Basic patterns
// may omit associations, making those event parameters wildcards.
type PatternParameterAssociationDecl struct {
	Position Position
	Formal   string
	Actual   ExpressionDecl
}

type ExpressionKind string

const (
	ExpressionName        ExpressionKind = "name"
	ExpressionPlaceholder ExpressionKind = "placeholder"
	ExpressionUniversal   ExpressionKind = "universal-placeholder"
	ExpressionState       ExpressionKind = "state"
	ExpressionInteger     ExpressionKind = "integer"
	ExpressionFloat       ExpressionKind = "float"
	ExpressionCharacter   ExpressionKind = "character"
	ExpressionString      ExpressionKind = "string"
	ExpressionUnit        ExpressionKind = "unit"
	ExpressionBoolean     ExpressionKind = "boolean"
	ExpressionCall        ExpressionKind = "call"
	ExpressionUnary       ExpressionKind = "unary"
	ExpressionBinary      ExpressionKind = "binary"
	ExpressionConditional ExpressionKind = "conditional"
	ExpressionQualified   ExpressionKind = "qualified"
	ExpressionRecord      ExpressionKind = "record"
	ExpressionSelection   ExpressionKind = "selection"
)

// RecordFieldExpressionDecl is one named initializer in a context-typed Record
// literal. The compiler retains the name independently of field list order.
type RecordFieldExpressionDecl struct {
	Position Position
	Name     string
	Value    ExpressionDecl
}

type ExpressionDecl struct {
	Position        Position
	Kind            ExpressionKind
	Name            string
	Integer         int64
	Float           float64
	Character       int64
	String          string
	StringCodes     []int64
	Boolean         bool
	Operator        string
	Left            *ExpressionDecl
	Right           *ExpressionDecl
	Arguments       []ExpressionDecl
	ArgumentFormals []string
	RecordFields    []RecordFieldExpressionDecl
}

type ParameterDecl struct {
	Position       Position
	Name           string
	Type           string
	TypeExpression TypeExpressionDecl
	Default        *ExpressionDecl
	Qualification  PlaceholderQualification
	RangeFirst     int64
	RangeLast      int64
	Relation       string
}

type PlaceholderQualification string

const (
	PlaceholderExistential PlaceholderQualification = "existential"
	PlaceholderUniversal   PlaceholderQualification = "universal"
)

// ModuleDecl is the initial faithful module-generator subset: closed predefined
// scalar object parameters with optional closed defaults, an exact return
// interface, exact named type denotations, closed
// immutable predefined scalar objects, exact local C.Ticks object declarations,
// initialized predefined scalar state, provided-function bodies, component-local
// Make_Clock objects, module-local basic action connections, and serial or
// parallel one-shot ordinary or repeating when processes. InitialParameters
// contains the defaulted object formals introduced by the initial part, and
// Initial contains its ordered startup statement list before process
// activation. Final contains the ordered statement list run when the generated
// module becomes finalized and before its implicit Finish action.
type ModuleDecl struct {
	Position             Position
	Name                 string
	Parameters           []ParameterDecl
	ReturnType           string
	Types                []TypeAliasDecl
	Exceptions           []ExceptionDecl
	Objects              []ModuleObjectDecl
	TimingObjects        []ModuleTimingObjectDecl
	States               []StateDecl
	Functions            []FunctionBodyDecl
	Clocks               []ClockDecl
	Constraints          []ConstraintDecl
	Connections          []ConnectionDecl
	ConnectionGenerators []ConnectionGeneratorDecl
	InitialParameters    []ParameterDecl
	Initial              []BehaviorStatementDecl
	Mode                 string
	Processes            []ModuleProcessDecl
	Handler              *BehaviorHandlerDecl
	Final                []BehaviorStatementDecl
}

type ClockDecl struct {
	Position Position
	Name     string
}

// ModuleTimingObjectDecl retains the exact dependent clock type of a local
// Name : C.Ticks is Expression declaration until module elaboration resolves
// it to a canonical fixed tick. It is intentionally distinct from an ordinary
// Integer/Natural object denotation.
type ModuleTimingObjectDecl struct {
	Position Position
	Name     string
	Clock    string
	Initial  ExpressionDecl
}

type ModuleProcessDecl struct {
	Position            Position
	Entry               bool
	Await               bool
	Label               string
	Terminator          string
	OuterExceptions     []ExceptionDecl
	IterationExceptions []ExceptionDecl
	Placeholders        []ParameterDecl
	Trigger             BehaviorPatternDecl
	Guard               *ExpressionDecl
	Statements          []BehaviorStatementDecl
	Alternatives        []ModuleAwaitAlternativeDecl
	ElsePresent         bool
	Else                []BehaviorStatementDecl
	Handler             *BehaviorHandlerDecl
}

// BehaviorHandlerDecl is a block handler. It may protect one top-level when
// process or serve as the handler part of its enclosing module definition.
type BehaviorHandlerDecl struct {
	Position Position
	Choices  []BehaviorHandlerChoiceDecl
	Else     []BehaviorStatementDecl
}

type BehaviorHandlerChoiceDecl struct {
	Position   Position
	Pattern    BehaviorPatternDecl
	Statements []BehaviorStatementDecl
}

// ModuleAwaitAlternativeDecl is one independently qualified trigger/body
// alternative of a source await statement. Placeholder declarations belong
// only to this alternative; they are not visible to sibling alternatives or
// the optional else part.
type ModuleAwaitAlternativeDecl struct {
	Position     Position
	Placeholders []ParameterDecl
	Trigger      BehaviorPatternDecl
	Guard        *ExpressionDecl
	Statements   []BehaviorStatementDecl
}

type ArchitectureDecl struct {
	Position             Position
	Name                 string
	Parameters           []ParameterDecl
	ReturnType           string
	ReturnTypeExpression TypeExpressionDecl
	Components           []ComponentDecl
	Constraints          []ConstraintDecl
	Connections          []ConnectionDecl
	ConnectionGenerators []ConnectionGeneratorDecl
	Initial              []BehaviorStatementDecl
}

type ConstraintKind string

const (
	ConstraintMatch    ConstraintKind = "match"
	ConstraintNotMatch ConstraintKind = "not match"
	ConstraintNever    ConstraintKind = "never"
)

type ConstraintDecl struct {
	Position   Position
	Label      string
	Alphabet   []BehaviorPatternDecl
	Components []ConstraintComponentDecl
}

type ConstraintComponentDecl struct {
	Position     Position
	Label        string
	Kind         ConstraintKind
	Placeholders []ParameterDecl
	Pattern      BehaviorPatternDecl
	Guard        *ExpressionDecl
}

type ComponentDecl struct {
	Position              Position
	Name                  string
	InterfaceType         string
	Module                string
	ModuleArguments       []ExpressionDecl
	ModuleArgumentFormals []string
	ArchitectureLiteral   *ArchitectureDecl
	IntegerArray          bool
	IndexType             string
	RangeFirst            ExpressionDecl
	RangeLast             ExpressionDecl
	FirstIndex            int64
	LastIndex             int64
}

type Connector string

const (
	ConnectBasic Connector = "to"
	ConnectPipe  Connector = "=>"
	ConnectAgent Connector = "||>"
)

type ActionRef struct {
	Position            Position
	Component           string
	ComponentIndex      *ExpressionDecl
	Action              string
	Path                []QualifiedMemberSegmentDecl
	Arguments           []PlaceholderRef
	ArgumentExpressions []ExpressionDecl
	ArgumentFormals     []string
}

type PlaceholderRef struct {
	Position Position
	Name     string
}

type ConnectionDecl struct {
	Position        Position
	Placeholders    []ParameterDecl
	Source          ActionRef
	SourcePattern   *BehaviorPatternDecl
	Guard           *ExpressionDecl
	Connector       Connector
	Target          ActionRef
	TargetGenerator *ConnectionSetGeneratorDecl
	// Constituent is populated only by deterministic service-connection
	// elaboration, where same-named actions and function objects remain distinct.
	Constituent ConnectionConstituentKind
}

// ConnectionSetGeneratorDecl is a generation scheme nested in the right-hand
// connection set. Every elaborated target shares the enclosing rule's source,
// placeholders, guard, and connector.
type ConnectionSetGeneratorDecl struct {
	Position     Position
	Kind         ConnectionGeneratorKind
	Condition    ExpressionDecl
	Iterator     string
	IteratorType string
	RangeFirst   ExpressionDecl
	RangeLast    ExpressionDecl
	Targets      []ActionRef
	Generators   []ConnectionSetGeneratorDecl
}

type ConnectionGeneratorKind string

const (
	ConnectionGeneratorIf       ConnectionGeneratorKind = "if"
	ConnectionGeneratorForRange ConnectionGeneratorKind = "for-integer-range"
)

// ConnectionGeneratorDecl is an elaboration-time topology generator from the
// Architecture LRM. It is deliberately distinct from runtime behavior and
// process iteration. The current source subset implements closed Boolean `if`
// schemes; the recursive shape preserves nested generators and provides the
// finite `for` schemes over closed Integer ranges.
type ConnectionGeneratorDecl struct {
	Position     Position
	Kind         ConnectionGeneratorKind
	Condition    ExpressionDecl
	Iterator     string
	IteratorType string
	RangeFirst   ExpressionDecl
	RangeLast    ExpressionDecl
	Connections  []ConnectionDecl
	Generators   []ConnectionGeneratorDecl
}

type ConnectionConstituentKind string

const (
	ConnectionActionConstituent   ConnectionConstituentKind = "action"
	ConnectionFunctionConstituent ConnectionConstituentKind = "function"
)

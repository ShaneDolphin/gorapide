package rapide

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// SyntaxError is a deterministic source diagnostic.
type SyntaxError struct {
	Position Position
	Message  string
}

func (err *SyntaxError) Error() string {
	return fmt.Sprintf("Rapide syntax error at %d:%d: %s", err.Position.Line, err.Position.Column, err.Message)
}

type parser struct {
	tokens                   []token
	index                    int
	connectionIteratorScopes []string
}

// Parse parses the initial source-compatible Rapide declaration subset.
// Unsupported syntax fails at its exact source coordinate.
func Parse(source []byte) (*File, error) {
	tokens, err := lex(source)
	if err != nil {
		return nil, err
	}
	current := &parser{tokens: tokens}
	file := &File{}
	for current.peek().kind != tokenEOF {
		switch {
		case current.peekKeyword("exception"):
			declaration, err := current.parseModuleExceptionDeclaration()
			if err != nil {
				return nil, err
			}
			declaration.Declaration = outermostExceptionDeclarationIdentity(declaration.Name)
			file.Exceptions = append(file.Exceptions, declaration)
		case current.peekKeyword("type"):
			if current.startsRecordType() {
				declaration, err := current.parseRecordType()
				if err != nil {
					return nil, err
				}
				file.Interfaces = append(file.Interfaces, declaration)
				continue
			}
			if current.startsUnionType() {
				declaration, err := current.parseUnionType()
				if err != nil {
					return nil, err
				}
				file.Unions = append(file.Unions, declaration)
				continue
			}
			if current.startsEnumerationType() {
				declaration, err := current.parseEnumerationType()
				if err != nil {
					return nil, err
				}
				file.Enumerations = append(file.Enumerations, declaration)
				continue
			}
			if current.startsClosedTypeAlias() {
				declaration, err := current.parseClosedTypeAlias()
				if err != nil {
					return nil, err
				}
				file.TypeAliases = append(file.TypeAliases, declaration)
				continue
			}
			declaration, err := current.parseInterface()
			if err != nil {
				return nil, err
			}
			file.Interfaces = append(file.Interfaces, declaration)
		case current.peekKeyword("module"):
			declaration, err := current.parseModule()
			if err != nil {
				return nil, err
			}
			file.Modules = append(file.Modules, declaration)
		case current.peekKeyword("map"):
			declaration, err := current.parseMap()
			if err != nil {
				return nil, err
			}
			file.Maps = append(file.Maps, declaration)
		case current.peekKeyword("architecture"):
			declaration, err := current.parseArchitecture()
			if err != nil {
				return nil, err
			}
			file.Architectures = append(file.Architectures, declaration)
		default:
			return nil, current.unexpected("'exception', 'type', 'module', 'map', or 'architecture'")
		}
	}
	return file, nil
}

// parseMap implements the Rapide 1.0 map-generator shell. Declarations,
// state assignments, module-generator indicators, multiple active domains,
// and the not-yet-lowered restricted-pattern operators remain explicit
// compiler/parser boundaries; no legacy pre-1.0 `rules` spelling is accepted
// implicitly.
func (current *parser) parseMap() (MapDecl, error) {
	start, _ := current.expectKeyword("map")
	name, err := current.expectIdentifier("map generator name")
	if err != nil {
		return MapDecl{}, err
	}
	if _, err := current.expect(tokenLParen, "'('"); err != nil {
		return MapDecl{}, err
	}
	parameters := make([]ParameterDecl, 0)
	if current.peek().kind != tokenRParen {
		for {
			parameterNames, err := current.parseFormalIdentifierList("map generator parameter name")
			if err != nil {
				return MapDecl{}, err
			}
			if _, err := current.expect(tokenColon, "':'"); err != nil {
				return MapDecl{}, err
			}
			parameterType, err := current.parseClosedTypeExpression("map generator parameter type")
			if err != nil {
				return MapDecl{}, err
			}
			var defaultExpression *ExpressionDecl
			if current.peekKeyword("is") {
				current.advance()
				value, err := current.parseExpression()
				if err != nil {
					return MapDecl{}, err
				}
				defaultExpression = &value
			}
			for _, parameterName := range parameterNames {
				parameters = append(parameters, ParameterDecl{
					Position: parameterName.position, Name: parameterName.lexeme,
					Type: typeExpressionSpelling(parameterType), TypeExpression: parameterType,
					Default: cloneExpressionDeclarationPointer(defaultExpression),
				})
			}
			if current.peek().kind != tokenComma && current.peek().kind != tokenSemicolon {
				break
			}
			current.advance()
		}
	}
	if _, err := current.expect(tokenRParen, "')'"); err != nil {
		return MapDecl{}, err
	}
	if _, err := current.expectKeyword("from"); err != nil {
		return MapDecl{}, err
	}
	domains := make([]TypeExpressionDecl, 0, 1)
	for {
		domain, err := current.parseClosedTypeExpression("map domain indicator")
		if err != nil {
			return MapDecl{}, err
		}
		domains = append(domains, domain)
		if current.peek().kind != tokenComma {
			break
		}
		current.advance()
	}
	if _, err := current.expectKeyword("to"); err != nil {
		return MapDecl{}, err
	}
	rangeIndicator, err := current.parseClosedTypeExpression("map range indicator")
	if err != nil {
		return MapDecl{}, err
	}
	if _, err := current.expectKeyword("is"); err != nil {
		return MapDecl{}, err
	}
	if current.peekKeyword("rules") {
		return MapDecl{}, &SyntaxError{
			Position: current.peek().position,
			Message:  "legacy pre-1.0 map section 'rules' is not the published Rapide 1.0 'rule' form",
		}
	}
	if !current.peekKeyword("rule") {
		return MapDecl{}, &SyntaxError{
			Position: current.peek().position,
			Message:  "map declarations and explicit map constraints are outside the current source-map subset; expected 'rule'",
		}
	}
	current.advance()
	result := MapDecl{
		Position: start.position, Name: name.lexeme, Parameters: parameters,
		Domains: domains, Range: rangeIndicator,
	}
	for !current.peekKeyword("end") {
		if current.peek().kind == tokenEOF {
			return MapDecl{}, current.unexpected("map agent rule or 'end'")
		}
		rule, err := current.parseMapRule()
		if err != nil {
			return MapDecl{}, err
		}
		result.Rules = append(result.Rules, rule)
	}
	current.advance()
	if current.peekKeyword("map") {
		current.advance()
	}
	if current.peek().kind == tokenIdentifier && keyword(current.peek().lexeme, result.Name) {
		current.advance()
	}
	if _, err := current.expect(tokenSemicolon, "';'"); err != nil {
		return MapDecl{}, err
	}
	return result, nil
}

func (current *parser) parseMapRule() (MapRuleDecl, error) {
	result := MapRuleDecl{Position: current.peek().position}
	if placeholders, present, err := current.parsePatternPlaceholderDeclarations(); err != nil {
		return MapRuleDecl{}, err
	} else if present {
		result.Placeholders = placeholders
	}
	trigger, err := current.parseBehaviorPattern()
	if err != nil {
		return MapRuleDecl{}, err
	}
	result.Trigger = trigger
	if isBehaviorPatternOperator(current.peek()) {
		return MapRuleDecl{}, &SyntaxError{
			Position: current.peek().position,
			Message:  "chained map trigger operators require explicit parentheses in the current source subset",
		}
	}
	if current.peekKeyword("where") {
		current.advance()
		guard, err := current.parseExpression()
		if err != nil {
			return MapRuleDecl{}, err
		}
		result.Guard = &guard
	}
	operator := current.peek()
	switch operator.kind {
	case tokenPipe:
		result.Connector = ConnectPipe
	case tokenAgent:
		result.Connector = ConnectAgent
	default:
		return MapRuleDecl{}, current.unexpected("map agent rule operator '||>'")
	}
	current.advance()
	if current.peek().kind == tokenSemicolon {
		current.advance()
		return result, nil
	}
	if current.peek().kind == tokenIdentifier && current.peekAt(1).kind == tokenAssign {
		return MapRuleDecl{}, &SyntaxError{
			Position: current.peek().position,
			Message:  "map state assignments are outside the current deterministic source-map subset",
		}
	}
	generator, err := current.parseMapPosetGenerator()
	if err != nil {
		return MapRuleDecl{}, err
	}
	result.Generator = &generator
	if current.peek().kind != tokenSemicolon {
		return MapRuleDecl{}, &SyntaxError{
			Position: current.peek().position,
			Message:  "expected ';' after map poset generator",
		}
	}
	current.advance()
	return result, nil
}

func (current *parser) parseMapPosetGenerator() (PosetGeneratorDecl, error) {
	left, err := current.parseMapPosetGeneratorPrimary()
	if err != nil {
		return PosetGeneratorDecl{}, err
	}
	for isBehaviorPatternOperator(current.peek()) {
		operator := current.advance()
		if operator.kind == tokenEquivalent {
			return PosetGeneratorDecl{}, &SyntaxError{
				Position: operator.position,
				Message:  "map poset generators are restricted patterns and may not contain '<=>'",
			}
		}
		right, err := current.parseMapPosetGeneratorPrimary()
		if err != nil {
			return PosetGeneratorDecl{}, err
		}
		leftCopy, rightCopy := left, right
		left = PosetGeneratorDecl{
			Position: operator.position, Kind: PosetGeneratorBinary,
			Operator: strings.ToLower(operator.lexeme), Left: &leftCopy, Right: &rightCopy,
		}
	}
	return left, nil
}

func (current *parser) parseMapPosetGeneratorPrimary() (PosetGeneratorDecl, error) {
	if current.peek().kind == tokenLBracket {
		return current.parseMapPosetGeneratorIteration()
	}
	if current.peek().kind == tokenLParen {
		current.advance()
		result, err := current.parseMapPosetGenerator()
		if err != nil {
			return PosetGeneratorDecl{}, err
		}
		if _, err := current.expect(tokenRParen, "')'"); err != nil {
			return PosetGeneratorDecl{}, err
		}
		return result, nil
	}
	name, err := current.expectIdentifier("range action in map poset generator")
	if err != nil {
		return PosetGeneratorDecl{}, err
	}
	call := CallStatementDecl{Position: name.position, Name: name.lexeme}
	hasParentheses := current.peek().kind == tokenLParen
	if hasParentheses {
		call, err = current.parseCallArguments(call)
		if err != nil {
			return PosetGeneratorDecl{}, err
		}
	}
	if keyword(call.Name, "empty") {
		if !hasParentheses {
			return PosetGeneratorDecl{}, &SyntaxError{
				Position: call.Position, Message: "map empty poset generator requires the published empty() spelling",
			}
		}
		if len(call.Arguments) != 0 {
			return PosetGeneratorDecl{}, &SyntaxError{
				Position: call.Position, Message: "map poset generator empty() does not accept arguments",
			}
		}
		return PosetGeneratorDecl{Position: name.position, Kind: PosetGeneratorEmpty}, nil
	}
	return PosetGeneratorDecl{Position: name.position, Kind: PosetGeneratorEvent, Call: call}, nil
}

func (current *parser) parseMapPosetGeneratorIteration() (PosetGeneratorDecl, error) {
	start, _ := current.expect(tokenLBracket, "'['")
	iterator := ""
	if current.peek().kind == tokenIdentifier && current.peekAt(1).kind == tokenColon {
		iterator = current.advance().lexeme
		current.advance()
		first, err := current.parseSignedIntegerLiteral("named map-generator iterator range lower bound")
		if err != nil {
			return PosetGeneratorDecl{}, err
		}
		if _, err := current.expect(tokenDot, "first '.' in named map-generator iterator range '..'"); err != nil {
			return PosetGeneratorDecl{}, err
		}
		if _, err := current.expect(tokenDot, "second '.' in named map-generator iterator range '..'"); err != nil {
			return PosetGeneratorDecl{}, err
		}
		last, err := current.parseSignedIntegerLiteral("named map-generator iterator range upper bound")
		if err != nil {
			return PosetGeneratorDecl{}, err
		}
		relation, err := current.parseMapPosetGeneratorIterationRelation()
		if err != nil {
			return PosetGeneratorDecl{}, err
		}
		inner, err := current.parseMapPosetGeneratorPrimary()
		if err != nil {
			return PosetGeneratorDecl{}, err
		}
		innerCopy := inner
		return PosetGeneratorDecl{
			Position: start.position, Kind: PosetGeneratorIteration,
			Iterator: iterator, First: first, Last: last,
			Relation: strings.ToLower(relation.lexeme), Inner: &innerCopy,
		}, nil
	}

	minimum, maximum := 0, -1
	switch cardinality := current.peek(); cardinality.kind {
	case tokenStar:
		current.advance()
	case tokenPlus:
		current.advance()
		minimum = 1
	case tokenInteger, tokenMinus:
		first, err := current.parseSignedIntegerLiteral("map-generator iteration cardinality or range lower bound")
		if err != nil {
			return PosetGeneratorDecl{}, &SyntaxError{
				Position: cardinality.position,
				Message:  "map-generator iteration cardinality is outside the host-independent integer subset",
			}
		}
		if current.peek().kind == tokenDot {
			current.advance()
			if _, err := current.expect(tokenDot, "second '.' in map-generator iterator range '..'"); err != nil {
				return PosetGeneratorDecl{}, err
			}
			last, err := current.parseSignedIntegerLiteral("map-generator iterator range upper bound")
			if err != nil {
				return PosetGeneratorDecl{}, err
			}
			count := uint64(0)
			if last >= first {
				difference := uint64(last) - uint64(first)
				if difference >= uint64(1<<31-1) {
					return PosetGeneratorDecl{}, &SyntaxError{
						Position: cardinality.position,
						Message:  "map-generator iteration range cardinality is outside the host-independent integer subset",
					}
				}
				count = difference + 1
			}
			minimum, maximum = int(count), int(count)
		} else {
			if first < 0 {
				return PosetGeneratorDecl{}, &SyntaxError{
					Position: cardinality.position,
					Message:  "expected map-generator iteration cardinality '*', '+', or nonnegative integer",
				}
			}
			if first > int64(1<<31-1) {
				return PosetGeneratorDecl{}, &SyntaxError{
					Position: cardinality.position,
					Message:  "map-generator iteration cardinality is outside the host-independent integer subset",
				}
			}
			minimum, maximum = int(first), int(first)
		}
	default:
		return PosetGeneratorDecl{}, current.unexpected(
			"map-generator iteration cardinality '*', '+', nonnegative integer, Integer range, or named Integer range",
		)
	}
	relation, err := current.parseMapPosetGeneratorIterationRelation()
	if err != nil {
		return PosetGeneratorDecl{}, err
	}
	inner, err := current.parseMapPosetGeneratorPrimary()
	if err != nil {
		return PosetGeneratorDecl{}, err
	}
	innerCopy := inner
	return PosetGeneratorDecl{
		Position: start.position, Kind: PosetGeneratorIteration,
		Minimum: minimum, Maximum: maximum,
		Relation: strings.ToLower(relation.lexeme), Inner: &innerCopy,
	}, nil
}

func (current *parser) parseMapPosetGeneratorIterationRelation() (token, error) {
	relation, err := current.parseBehaviorIterationRelation()
	if err != nil {
		return token{}, err
	}
	if relation.kind == tokenEquivalent {
		return token{}, &SyntaxError{
			Position: relation.position,
			Message:  "map poset generators are restricted patterns and may not contain '<=>'",
		}
	}
	return relation, nil
}

func (current *parser) startsRecordType() bool {
	return current.peekKeyword("type") && current.peekAt(1).kind == tokenIdentifier &&
		current.peekAtKeyword(2, "is") && current.peekAtKeyword(3, "record")
}

func (current *parser) startsUnionType() bool {
	return current.peekKeyword("type") && current.peekAt(1).kind == tokenIdentifier &&
		current.peekAtKeyword(2, "is") && current.peekAtKeyword(3, "union")
}

func (current *parser) startsEnumerationType() bool {
	return current.peekKeyword("type") && current.peekAt(1).kind == tokenIdentifier &&
		current.peekAtKeyword(2, "is") && current.peekAtKeyword(3, "enum")
}

func (current *parser) startsClosedTypeAlias() bool {
	return current.peekKeyword("type") && current.peekAt(1).kind == tokenIdentifier &&
		current.peekAtKeyword(2, "is") && current.peekAt(3).kind == tokenIdentifier &&
		!current.peekAtKeyword(3, "interface") && !current.peekAtKeyword(3, "include")
}

func (current *parser) parseClosedTypeAlias() (TypeAliasDecl, error) {
	start, _ := current.expectKeyword("type")
	name, err := current.expectIdentifier("type alias name")
	if err != nil {
		return TypeAliasDecl{}, err
	}
	if _, err := current.expectKeyword("is"); err != nil {
		return TypeAliasDecl{}, err
	}
	if current.peekKeyword("range") && current.peekAt(1).kind != tokenLParen {
		current.advance()
		first, err := current.parseSignedIntegerLiteral("finite Integer range lower bound")
		if err != nil {
			return TypeAliasDecl{}, err
		}
		if _, err := current.expect(tokenDot, "first '.' in finite Integer range '..'"); err != nil {
			return TypeAliasDecl{}, err
		}
		if _, err := current.expect(tokenDot, "second '.' in finite Integer range '..'"); err != nil {
			return TypeAliasDecl{}, err
		}
		last, err := current.parseSignedIntegerLiteral("finite Integer range upper bound")
		if err != nil {
			return TypeAliasDecl{}, err
		}
		if _, err := current.expect(tokenSemicolon, "';'"); err != nil {
			return TypeAliasDecl{}, err
		}
		return TypeAliasDecl{
			Position: start.position, Name: name.lexeme,
			Target:       fmt.Sprintf("range %d..%d", first, last),
			IntegerRange: true, FirstIndex: first, LastIndex: last,
		}, nil
	}
	target, err := current.parseClosedTypeExpression("type alias target")
	if err != nil {
		return TypeAliasDecl{}, err
	}
	if _, err := current.expect(tokenSemicolon, "';'"); err != nil {
		return TypeAliasDecl{}, &SyntaxError{
			Position: current.peek().position,
			Message:  "the current type-alias subset requires one closed name/application type expression",
		}
	}
	return TypeAliasDecl{
		Position: start.position, Name: name.lexeme,
		Target: typeExpressionSpelling(target), Expression: target,
	}, nil
}

func (current *parser) parseClosedTypeExpression(context string) (TypeExpressionDecl, error) {
	name, err := current.expectIdentifier(context)
	if err != nil {
		return TypeExpressionDecl{}, err
	}
	if keyword(name.lexeme, "function") || keyword(name.lexeme, "interface") ||
		keyword(name.lexeme, "record") || keyword(name.lexeme, "union") ||
		keyword(name.lexeme, "enum") || keyword(name.lexeme, "array") {
		return TypeExpressionDecl{}, &SyntaxError{
			Position: name.position,
			Message:  "rich type-expression form " + name.lexeme + " is outside the current closed name/application subset",
		}
	}
	result := TypeExpressionDecl{Position: name.position, Kind: TypeExpressionName, Name: name.lexeme}
	if current.peek().kind != tokenLParen {
		return result, nil
	}
	current.advance()
	result.Kind = TypeExpressionApplication
	for current.peek().kind != tokenRParen {
		if current.peek().kind == tokenEOF {
			return TypeExpressionDecl{}, current.unexpected("type-constructor argument or ')'")
		}
		argument, err := current.parseClosedTypeExpression("type-constructor argument")
		if err != nil {
			return TypeExpressionDecl{}, err
		}
		result.Arguments = append(result.Arguments, argument)
		if current.peek().kind == tokenRParen {
			break
		}
		if _, err := current.expect(tokenComma, "',' between type-constructor arguments"); err != nil {
			return TypeExpressionDecl{}, err
		}
	}
	current.advance()
	return result, nil
}

func (current *parser) parseRecordType() (InterfaceDecl, error) {
	start, _ := current.expectKeyword("type")
	name, err := current.expectIdentifier("record type name")
	if err != nil {
		return InterfaceDecl{}, err
	}
	if _, err := current.expectKeyword("is"); err != nil {
		return InterfaceDecl{}, err
	}
	if _, err := current.expectKeyword("record"); err != nil {
		return InterfaceDecl{}, err
	}
	result := InterfaceDecl{Position: start.position, Name: name.lexeme, Record: true}
	for !current.peekKeyword("end") {
		if current.peek().kind == tokenEOF {
			return InterfaceDecl{}, current.unexpected("record field, derivation, or 'end'")
		}
		if current.peekKeyword("include") {
			derivation, err := current.parseInterfaceDerivation(InterfaceDerivationAll)
			if err != nil {
				return InterfaceDecl{}, err
			}
			result.Derivations = append(result.Derivations, derivation)
			continue
		}
		if current.peek().kind != tokenIdentifier {
			return InterfaceDecl{}, current.unexpected("record field declaration, derivation, or 'end'")
		}
		objects, functions, generators, err := current.parseInterfaceNameDeclaration(InterfaceNameProvides)
		if err != nil {
			return InterfaceDecl{}, err
		}
		if len(functions) != 0 || len(generators) != 0 || len(objects) == 0 {
			position := start.position
			if len(functions) != 0 {
				position = functions[0].Position
			} else if len(generators) != 0 {
				position = generators[0].Position
			}
			return InterfaceDecl{}, &SyntaxError{
				Position: position,
				Message:  "record fields must be object name declarations; functions and module generators are not record fields",
			}
		}
		result.Objects = append(result.Objects, objects...)
	}
	current.advance()
	if current.peekKeyword("record") {
		current.advance()
	}
	if current.peek().kind == tokenIdentifier && keyword(current.peek().lexeme, result.Name) {
		current.advance()
	}
	if _, err := current.expect(tokenSemicolon, "';'"); err != nil {
		return InterfaceDecl{}, err
	}
	return result, nil
}

func (current *parser) parseUnionType() (UnionDecl, error) {
	start, _ := current.expectKeyword("type")
	name, err := current.expectIdentifier("Union type name")
	if err != nil {
		return UnionDecl{}, err
	}
	if _, err := current.expectKeyword("is"); err != nil {
		return UnionDecl{}, err
	}
	if _, err := current.expectKeyword("union"); err != nil {
		return UnionDecl{}, err
	}
	result := UnionDecl{Position: start.position, Name: name.lexeme}
	seen := make(map[string]bool)
	for !current.peekKeyword("end") {
		if current.peek().kind == tokenEOF {
			return UnionDecl{}, current.unexpected("Union tag declaration or 'end'")
		}
		tags, err := current.parseFormalIdentifierList("Union tag")
		if err != nil {
			return UnionDecl{}, err
		}
		if _, err := current.expect(tokenColon, "':'"); err != nil {
			return UnionDecl{}, err
		}
		memberType, err := current.parseClosedTypeExpression("Union member type")
		if err != nil {
			return UnionDecl{}, err
		}
		if _, err := current.expect(tokenSemicolon, "';'"); err != nil {
			return UnionDecl{}, err
		}
		for _, tag := range tags {
			key := folded(tag.lexeme)
			if seen[key] {
				return UnionDecl{}, &SyntaxError{
					Position: tag.position,
					Message:  fmt.Sprintf("Union tag %q is declared more than once", tag.lexeme),
				}
			}
			seen[key] = true
			result.Tags = append(result.Tags, UnionTagDecl{
				Position: tag.position, Name: tag.lexeme,
				Type: typeExpressionSpelling(memberType), TypeExpression: memberType,
			})
		}
	}
	if len(result.Tags) == 0 {
		return UnionDecl{}, &SyntaxError{
			Position: current.peek().position,
			Message:  "a Union type requires at least one tag/member declaration",
		}
	}
	current.advance()
	if current.peekKeyword("union") {
		current.advance()
	}
	if current.peek().kind == tokenIdentifier && keyword(current.peek().lexeme, result.Name) {
		current.advance()
	}
	if _, err := current.expect(tokenSemicolon, "';'"); err != nil {
		return UnionDecl{}, err
	}
	return result, nil
}

func (current *parser) parseEnumerationType() (EnumerationDecl, error) {
	start, _ := current.expectKeyword("type")
	name, err := current.expectIdentifier("Enumeration type name")
	if err != nil {
		return EnumerationDecl{}, err
	}
	if _, err := current.expectKeyword("is"); err != nil {
		return EnumerationDecl{}, err
	}
	if _, err := current.expectKeyword("enum"); err != nil {
		return EnumerationDecl{}, err
	}
	result := EnumerationDecl{Position: start.position, Name: name.lexeme}
	seen := make(map[string]bool)
	if current.peekKeyword("end") {
		return EnumerationDecl{}, &SyntaxError{
			Position: current.peek().position,
			Message:  "an Enumeration type requires at least one literal identifier",
		}
	}
	for {
		literal, err := current.expectIdentifier("Enumeration literal")
		if err != nil {
			return EnumerationDecl{}, err
		}
		key := folded(literal.lexeme)
		if seen[key] {
			return EnumerationDecl{}, &SyntaxError{
				Position: literal.position,
				Message:  fmt.Sprintf("Enumeration literal %q is declared more than once", literal.lexeme),
			}
		}
		seen[key] = true
		result.Literals = append(result.Literals, EnumerationLiteralDecl{
			Position: literal.position, Name: literal.lexeme,
		})
		if current.peek().kind != tokenComma {
			break
		}
		current.advance()
		if current.peekKeyword("end") {
			return EnumerationDecl{}, current.unexpected("Enumeration literal after ','")
		}
	}
	if _, err := current.expectKeyword("end"); err != nil {
		return EnumerationDecl{}, err
	}
	if current.peekKeyword("enum") {
		current.advance()
	}
	if current.peek().kind == tokenIdentifier && keyword(current.peek().lexeme, result.Name) {
		current.advance()
	}
	if _, err := current.expect(tokenSemicolon, "';'"); err != nil {
		return EnumerationDecl{}, err
	}
	return result, nil
}

// parseFormalIdentifierList implements the Type LRM's formal object-parameter
// identifier_list. The caller parses the shared type and optional default, then
// expands one declaration per returned identifier in this source order.
func (current *parser) parseFormalIdentifierList(description string) ([]token, error) {
	result := make([]token, 0, 1)
	for {
		name, err := current.expectIdentifier(description)
		if err != nil {
			return nil, err
		}
		result = append(result, name)
		if current.peek().kind != tokenComma {
			return result, nil
		}
		current.advance()
	}
}

func typeExpressionSpelling(expression TypeExpressionDecl) string {
	if expression.Kind != TypeExpressionApplication {
		return expression.Name
	}
	var builder strings.Builder
	builder.WriteString(expression.Name)
	builder.WriteByte('(')
	for index, argument := range expression.Arguments {
		if index != 0 {
			builder.WriteByte(',')
		}
		builder.WriteString(typeExpressionSpelling(argument))
	}
	builder.WriteByte(')')
	return builder.String()
}

func (current *parser) parseInterface() (InterfaceDecl, error) {
	start, _ := current.expectKeyword("type")
	name, err := current.expectIdentifier("interface type name")
	if err != nil {
		return InterfaceDecl{}, err
	}
	if _, err := current.expectKeyword("is"); err != nil {
		return InterfaceDecl{}, err
	}
	result := InterfaceDecl{Position: start.position, Name: name.lexeme}
	for current.peekKeyword("include") {
		derivation, err := current.parseInterfaceDerivation(InterfaceDerivationAll)
		if err != nil {
			return InterfaceDecl{}, err
		}
		result.Derivations = append(result.Derivations, derivation)
	}
	if _, err := current.expectKeyword("interface"); err != nil {
		return InterfaceDecl{}, err
	}
	region := InterfaceNameProvides
	regionSupported := true
	privateRegion := false
	for !current.peekKeyword("end") {
		if current.peek().kind == tokenEOF {
			return InterfaceDecl{}, current.unexpected("interface constituent or 'end'")
		}
		switch {
		case current.peekKeyword("include"):
			if !regionSupported {
				return InterfaceDecl{}, &SyntaxError{
					Position: current.peek().position,
					Message:  "interface derivation is not permitted in the current action, behavior, or constraint part",
				}
			}
			derivation, err := current.parseInterfaceDerivation(interfaceDerivationRegionForNameRegion(region))
			if err != nil {
				return InterfaceDecl{}, err
			}
			result.Derivations = append(result.Derivations, derivation)
		case current.peekKeyword("action"):
			if privateRegion {
				return InterfaceDecl{}, current.unexpected("a private name declaration or 'end'")
			}
			regionSupported = false
			action, err := current.parseAction()
			if err != nil {
				return InterfaceDecl{}, err
			}
			result.Actions = append(result.Actions, action)
		case current.peekKeyword("private"):
			if current.peekAtKeyword(1, "action") {
				regionSupported = false
				actions, err := current.parsePrivateActionPart()
				if err != nil {
					return InterfaceDecl{}, err
				}
				result.Actions = append(result.Actions, actions...)
				break
			}
			current.advance()
			region = InterfaceNamePrivate
			regionSupported = true
			privateRegion = true
		case current.peekKeyword("provides"):
			if privateRegion {
				return InterfaceDecl{}, current.unexpected("a private name declaration or 'end'")
			}
			current.advance()
			region = InterfaceNameProvides
			regionSupported = true
		case current.peekKeyword("requires"):
			if privateRegion {
				return InterfaceDecl{}, current.unexpected("a private name declaration or 'end'")
			}
			current.advance()
			region = InterfaceNameRequires
			regionSupported = true
		case current.peekKeyword("type"):
			if !regionSupported {
				return InterfaceDecl{}, current.unexpected("a declarative-part keyword before a type-name declaration")
			}
			if current.peekAt(1).kind == tokenIdentifier && current.peekAt(2).kind == tokenLParen {
				declaration, err := current.parseInterfaceTypeConstructor(region)
				if err != nil {
					return InterfaceDecl{}, err
				}
				result.TypeConstructors = append(result.TypeConstructors, declaration)
				break
			}
			declaration, err := current.parseInterfaceTypeName(region)
			if err != nil {
				return InterfaceDecl{}, err
			}
			result.TypeNames = append(result.TypeNames, declaration)
		case current.peekKeyword("exception"):
			if !regionSupported {
				return InterfaceDecl{}, current.unexpected("a declarative-part keyword before an exception declaration")
			}
			declaration, err := current.parseModuleExceptionDeclaration()
			if err != nil {
				return InterfaceDecl{}, err
			}
			declaration.Region = region
			declaration.Constituent = true
			declaration.Declaration = interfaceExceptionDeclarationIdentity(
				result.Name, region, declaration.Name,
			)
			result.Exceptions = append(result.Exceptions, declaration)
		case current.peekKeyword("service"):
			if privateRegion {
				return InterfaceDecl{}, current.unexpected("a private name declaration or 'end'")
			}
			regionSupported = false
			services, err := current.parseInterfaceServicePart()
			if err != nil {
				return InterfaceDecl{}, err
			}
			result.Services = append(result.Services, services...)
		case current.peek().kind == tokenIdentifier && !current.interfacePartBoundary():
			if !regionSupported {
				return InterfaceDecl{}, current.unexpected("a declarative-part keyword before an interface name declaration")
			}
			objects, functions, generators, err := current.parseInterfaceNameDeclaration(region)
			if err != nil {
				return InterfaceDecl{}, err
			}
			result.Objects = append(result.Objects, objects...)
			result.Functions = append(result.Functions, functions...)
			result.ModuleGenerators = append(result.ModuleGenerators, generators...)
		case current.peekKeyword("behavior"):
			regionSupported = false
			if result.Behavior != nil {
				return InterfaceDecl{}, current.unexpected("only one interface behavior part")
			}
			behavior, err := current.parseBehavior()
			if err != nil {
				return InterfaceDecl{}, err
			}
			result.Behavior = &behavior
		case current.peekKeyword("constraint"):
			regionSupported = false
			current.advance()
			if !current.startsPatternConstraintGroup() {
				return InterfaceDecl{}, current.unexpected("interface 'match', 'never', or 'observe' constraint")
			}
			for current.startsPatternConstraintGroup() {
				declarations, err := current.parsePatternConstraintGroup()
				if err != nil {
					return InterfaceDecl{}, err
				}
				result.Constraints = append(result.Constraints, declarations...)
			}
		default:
			return InterfaceDecl{}, current.unexpected("supported interface derivation, object, function, module-generator name, exception, action, private action, provides, requires, behavior, or 'end'")
		}
	}
	current.advance()
	if current.peekKeyword("interface") {
		current.advance()
	}
	if current.peek().kind == tokenIdentifier && keyword(current.peek().lexeme, result.Name) {
		current.advance()
	}
	if _, err := current.expect(tokenSemicolon, "';'"); err != nil {
		return InterfaceDecl{}, err
	}
	return result, nil
}

func (current *parser) parseInterfaceServicePart() ([]InterfaceServiceDecl, error) {
	start, _ := current.expectKeyword("service")
	if current.interfacePartBoundary() {
		return nil, &SyntaxError{Position: start.position, Message: "interface service part requires a service declaration"}
	}
	result := make([]InterfaceServiceDecl, 0)
	for !current.interfacePartBoundary() {
		names := make([]token, 0, 1)
		for {
			name, err := current.expectIdentifier("service name")
			if err != nil {
				return nil, err
			}
			names = append(names, name)
			if current.peek().kind != tokenComma {
				break
			}
			current.advance()
		}
		integerSet := false
		var firstIndex, lastIndex int64
		if current.peek().kind == tokenLParen {
			integerSet = true
			current.advance()
			if current.peek().kind != tokenInteger && current.peek().kind != tokenMinus {
				return nil, &SyntaxError{
					Position: current.peek().position,
					Message:  "enumeration-indexed service sets are outside the current source subset",
				}
			}
			var err error
			firstIndex, err = current.parseSignedIntegerLiteral("service-set Integer range lower bound")
			if err != nil {
				return nil, err
			}
			if _, err := current.expect(tokenDot, "first '.' in service-set range '..'"); err != nil {
				return nil, err
			}
			if _, err := current.expect(tokenDot, "second '.' in service-set range '..'"); err != nil {
				return nil, err
			}
			lastIndex, err = current.parseSignedIntegerLiteral("service-set Integer range upper bound")
			if err != nil {
				return nil, err
			}
			if _, err := current.expect(tokenRParen, "')'"); err != nil {
				return nil, err
			}
		}
		if _, err := current.expect(tokenColon, "':'"); err != nil {
			return nil, err
		}
		dual := false
		if current.peekKeyword("dual") {
			dual = true
			current.advance()
		}
		typ, err := current.parseClosedTypeExpression("service interface type")
		if err != nil {
			return nil, err
		}
		if _, err := current.expect(tokenSemicolon, "';'"); err != nil {
			return nil, err
		}
		for _, name := range names {
			result = append(result, InterfaceServiceDecl{
				Position: name.position, Name: name.lexeme,
				Dual: dual, IntegerSet: integerSet, FirstIndex: firstIndex, LastIndex: lastIndex,
				Type: typeExpressionSpelling(typ), TypeExpression: typ,
			})
		}
	}
	return result, nil
}

func interfaceDerivationRegionForNameRegion(region InterfaceNameRegion) InterfaceDerivationRegion {
	switch region {
	case InterfaceNameRequires:
		return InterfaceDerivationRequires
	case InterfaceNamePrivate:
		return InterfaceDerivationPrivate
	default:
		return InterfaceDerivationProvides
	}
}

func (current *parser) parseInterfaceTypeName(region InterfaceNameRegion) (InterfaceTypeNameDecl, error) {
	start, _ := current.expectKeyword("type")
	name, err := current.expectIdentifier("interface type-name constituent")
	if err != nil {
		return InterfaceTypeNameDecl{}, err
	}
	if region == InterfaceNameRequires {
		return InterfaceTypeNameDecl{}, &SyntaxError{
			Position: start.position, Message: "type-name declarations are not permitted in a requires region",
		}
	}
	result := InterfaceTypeNameDecl{
		Position: start.position, Region: region, Name: name.lexeme, Specification: InterfaceTypeNameAny,
	}
	switch {
	case current.peek().kind == tokenSemicolon:
		current.advance()
		return result, nil
	case current.peek().kind == tokenLess && current.peekAt(1).kind == tokenColon:
		current.advance()
		current.advance()
		result.Specification = InterfaceTypeNameSubtype
	case current.peekKeyword("is"):
		current.advance()
		result.Specification = InterfaceTypeNameExact
	default:
		return InterfaceTypeNameDecl{}, current.unexpected("';', '<:', or 'is' in a type-name declaration")
	}
	typeExpression, err := current.parseClosedTypeExpression("type expression in interface type-name declaration")
	if err != nil {
		return InterfaceTypeNameDecl{}, err
	}
	result.Type = typeExpressionSpelling(typeExpression)
	result.TypeExpression = typeExpression
	if _, err := current.expect(tokenSemicolon, "';'"); err != nil {
		return InterfaceTypeNameDecl{}, &SyntaxError{
			Position: current.peek().position,
			Message:  "the current interface type-name subset requires one closed name/application bound or exact type",
		}
	}
	return result, nil
}

func (current *parser) parseInterfaceTypeConstructor(region InterfaceNameRegion) (InterfaceTypeConstructorDecl, error) {
	start, _ := current.expectKeyword("type")
	name, err := current.expectIdentifier("interface type-constructor constituent")
	if err != nil {
		return InterfaceTypeConstructorDecl{}, err
	}
	if region == InterfaceNameRequires {
		return InterfaceTypeConstructorDecl{}, &SyntaxError{
			Position: start.position, Message: "type-constructor declarations are not permitted in a requires region",
		}
	}
	if _, err := current.expect(tokenLParen, "'('"); err != nil {
		return InterfaceTypeConstructorDecl{}, err
	}
	result := InterfaceTypeConstructorDecl{
		Position: start.position, Region: region, Name: name.lexeme, Specification: InterfaceTypeNameAny,
	}
	for current.peek().kind != tokenRParen {
		if current.peek().kind == tokenEOF {
			return InterfaceTypeConstructorDecl{}, current.unexpected("type-constructor formal parameter or ')'")
		}
		if current.peekKeyword("type") {
			parameterStart := current.advance()
			parameterName, err := current.expectIdentifier("formal type-parameter name")
			if err != nil {
				return InterfaceTypeConstructorDecl{}, err
			}
			parameter := InterfaceFormalParameterDecl{
				Position: parameterStart.position, Kind: InterfaceFormalTypeParameter, Name: parameterName.lexeme,
			}
			if current.peek().kind == tokenLess && current.peekAt(1).kind == tokenColon {
				current.advance()
				current.advance()
				bound, err := current.parseClosedTypeExpression("formal type-parameter bound")
				if err != nil {
					return InterfaceTypeConstructorDecl{}, err
				}
				parameter.Type = typeExpressionSpelling(bound)
				parameter.TypeExpression = bound
			}
			result.Parameters = append(result.Parameters, parameter)
		} else {
			names := make([]token, 0, 1)
			for {
				parameterName, err := current.expectIdentifier("formal object-parameter name")
				if err != nil {
					return InterfaceTypeConstructorDecl{}, err
				}
				names = append(names, parameterName)
				if current.peek().kind != tokenComma {
					break
				}
				current.advance()
			}
			if _, err := current.expect(tokenColon, "':'"); err != nil {
				return InterfaceTypeConstructorDecl{}, err
			}
			parameterType, err := current.parseClosedTypeExpression("formal object-parameter type")
			if err != nil {
				return InterfaceTypeConstructorDecl{}, err
			}
			if current.peekKeyword("is") {
				return InterfaceTypeConstructorDecl{}, &SyntaxError{
					Position: current.peek().position,
					Message:  "formal object-parameter defaults require canonical object denotations outside the current type-constructor subset",
				}
			}
			for _, parameterName := range names {
				result.Parameters = append(result.Parameters, InterfaceFormalParameterDecl{
					Position: parameterName.position, Kind: InterfaceFormalObjectParameter,
					Name: parameterName.lexeme, Type: typeExpressionSpelling(parameterType),
					TypeExpression: parameterType,
				})
			}
		}
		if current.peek().kind == tokenRParen {
			break
		}
		if _, err := current.expect(tokenSemicolon, "';' between type-constructor formal parameter declarations"); err != nil {
			return InterfaceTypeConstructorDecl{}, err
		}
	}
	current.advance()
	switch {
	case current.peek().kind == tokenSemicolon:
		current.advance()
		return result, nil
	case current.peek().kind == tokenLess && current.peekAt(1).kind == tokenColon:
		current.advance()
		current.advance()
		result.Specification = InterfaceTypeNameSubtype
	case current.peekKeyword("is"):
		current.advance()
		result.Specification = InterfaceTypeNameExact
	default:
		return InterfaceTypeConstructorDecl{}, current.unexpected("';', '<:', or 'is' in a type-constructor declaration")
	}
	typeExpression, err := current.parseClosedTypeExpression("type-constructor result type expression")
	if err != nil {
		return InterfaceTypeConstructorDecl{}, err
	}
	result.Type = typeExpressionSpelling(typeExpression)
	result.TypeExpression = typeExpression
	if _, err := current.expect(tokenSemicolon, "';'"); err != nil {
		return InterfaceTypeConstructorDecl{}, &SyntaxError{
			Position: current.peek().position,
			Message:  "the current type-constructor subset requires one closed name/application result bound or exact type",
		}
	}
	return result, nil
}

func (current *parser) parseInterfaceNameDeclaration(region InterfaceNameRegion) (
	[]InterfaceObjectDecl,
	[]FunctionDecl,
	[]InterfaceModuleGeneratorDecl,
	error,
) {
	names := make([]token, 0, 1)
	for {
		name, err := current.expectIdentifier("interface constituent name")
		if err != nil {
			return nil, nil, nil, err
		}
		names = append(names, name)
		if current.peek().kind != tokenComma {
			break
		}
		current.advance()
	}
	if _, err := current.expect(tokenColon, "':'"); err != nil {
		return nil, nil, nil, err
	}
	if current.peekKeyword("function") {
		mode := FunctionProvides
		switch region {
		case InterfaceNameRequires:
			mode = FunctionRequires
		case InterfaceNamePrivate:
			mode = FunctionPrivate
		}
		functions, err := current.parseInterfaceFunctionType(names, mode)
		return nil, functions, nil, err
	}
	if current.peekKeyword("module") {
		generators, err := current.parseInterfaceModuleGeneratorType(names, region)
		return nil, nil, generators, err
	}
	typeExpression, err := current.parseClosedTypeExpression("object constituent type")
	if err != nil {
		return nil, nil, nil, err
	}
	if _, err := current.expect(tokenSemicolon, "';'"); err != nil {
		return nil, nil, nil, &SyntaxError{
			Position: current.peek().position,
			Message:  "the current object-constituent subset requires one closed name/application type expression",
		}
	}
	objects := make([]InterfaceObjectDecl, len(names))
	for index, name := range names {
		objects[index] = InterfaceObjectDecl{
			Position: name.position, Region: region, Name: name.lexeme,
			Type: typeExpressionSpelling(typeExpression), TypeExpression: typeExpression,
		}
	}
	return objects, nil, nil, nil
}

func (current *parser) parseInterfaceModuleGeneratorType(
	names []token,
	region InterfaceNameRegion,
) ([]InterfaceModuleGeneratorDecl, error) {
	if _, err := current.expectKeyword("module"); err != nil {
		return nil, err
	}
	if _, err := current.expect(tokenLParen, "'('"); err != nil {
		return nil, err
	}
	parameters := make([]InterfaceFormalParameterDecl, 0)
	if current.peek().kind != tokenRParen {
		for {
			if current.peekKeyword("type") {
				start := current.advance()
				parameterName, err := current.expectIdentifier("module-generator type parameter")
				if err != nil {
					return nil, err
				}
				parameter := InterfaceFormalParameterDecl{
					Position: start.position, Kind: InterfaceFormalTypeParameter, Name: parameterName.lexeme,
				}
				if current.peek().kind == tokenLess && current.peekAt(1).kind == tokenColon {
					current.advance()
					current.advance()
					bound, err := current.parseClosedTypeExpression("module-generator type-parameter bound")
					if err != nil {
						return nil, err
					}
					parameter.Type = typeExpressionSpelling(bound)
					parameter.TypeExpression = bound
				} else if current.peekKeyword("is") {
					return nil, &SyntaxError{
						Position: current.peek().position,
						Message:  "defaulted interface module-generator type parameters are outside the current source subset",
					}
				}
				parameters = append(parameters, parameter)
			} else {
				parameterNames, err := current.parseFormalIdentifierList("module-generator object parameter")
				if err != nil {
					return nil, err
				}
				if _, err := current.expect(tokenColon, "':'"); err != nil {
					return nil, err
				}
				parameterType, err := current.parseClosedTypeExpression("module-generator parameter type")
				if err != nil {
					return nil, err
				}
				if current.peek().kind == tokenAssign {
					return nil, &SyntaxError{
						Position: current.peek().position,
						Message:  "defaulted interface module-generator parameters are outside the current source subset",
					}
				}
				for _, parameterName := range parameterNames {
					parameters = append(parameters, InterfaceFormalParameterDecl{
						Position: parameterName.position, Kind: InterfaceFormalObjectParameter, Name: parameterName.lexeme,
						Type: typeExpressionSpelling(parameterType), TypeExpression: parameterType,
					})
				}
			}
			if current.peek().kind != tokenComma && current.peek().kind != tokenSemicolon {
				break
			}
			current.advance()
		}
	}
	if _, err := current.expect(tokenRParen, "')'"); err != nil {
		return nil, err
	}
	if _, err := current.expectKeyword("return"); err != nil {
		return nil, &SyntaxError{
			Position: current.peek().position,
			Message:  "interface module-generator name requires 'return Interface_Expression'",
		}
	}
	returnType, err := current.parseClosedTypeExpression("module-generator return interface")
	if err != nil {
		return nil, err
	}
	if _, err := current.expect(tokenSemicolon, "';'"); err != nil {
		return nil, err
	}
	result := make([]InterfaceModuleGeneratorDecl, len(names))
	for index, name := range names {
		result[index] = InterfaceModuleGeneratorDecl{
			Position: name.position, Region: region, Name: name.lexeme,
			Parameters: append([]InterfaceFormalParameterDecl(nil), parameters...),
			ReturnType: typeExpressionSpelling(returnType), ReturnTypeExpression: returnType,
		}
	}
	return result, nil
}

func (current *parser) parseInterfaceFunctionType(names []token, mode FunctionMode) ([]FunctionDecl, error) {
	if _, err := current.expectKeyword("function"); err != nil {
		return nil, err
	}
	if _, err := current.expect(tokenLParen, "'('"); err != nil {
		return nil, err
	}
	parameters := make([]ParameterDecl, 0)
	if current.peek().kind != tokenRParen {
		for {
			parameterNames, err := current.parseFormalIdentifierList("parameter name")
			if err != nil {
				return nil, err
			}
			if _, err := current.expect(tokenColon, "':'"); err != nil {
				return nil, err
			}
			parameterType, err := current.parseClosedTypeExpression("function parameter type")
			if err != nil {
				return nil, err
			}
			var defaultExpression *ExpressionDecl
			if current.peekKeyword("is") {
				current.advance()
				value, err := current.parseExpression()
				if err != nil {
					return nil, err
				}
				defaultExpression = &value
			}
			for _, parameterName := range parameterNames {
				parameters = append(parameters, ParameterDecl{
					Position: parameterName.position, Name: parameterName.lexeme,
					Type: typeExpressionSpelling(parameterType), TypeExpression: parameterType,
					Default: cloneExpressionDeclarationPointer(defaultExpression),
				})
			}
			if current.peek().kind != tokenComma && current.peek().kind != tokenSemicolon {
				break
			}
			current.advance()
		}
	}
	if _, err := current.expect(tokenRParen, "')'"); err != nil {
		return nil, err
	}
	returnType := ""
	var returnTypeExpression TypeExpressionDecl
	if current.peekKeyword("return") {
		current.advance()
		result, err := current.parseClosedTypeExpression("function return type")
		if err != nil {
			return nil, err
		}
		returnType = typeExpressionSpelling(result)
		returnTypeExpression = result
	}
	if _, err := current.expect(tokenSemicolon, "';'"); err != nil {
		return nil, err
	}
	result := make([]FunctionDecl, len(names))
	for index, name := range names {
		result[index] = FunctionDecl{
			Position: name.position, Mode: mode, Name: name.lexeme,
			Parameters: append([]ParameterDecl(nil), parameters...), ReturnType: returnType,
			ReturnTypeExpression: returnTypeExpression,
		}
	}
	return result, nil
}

func (current *parser) parseInterfaceDerivation(region InterfaceDerivationRegion) (InterfaceDerivationDecl, error) {
	start, _ := current.expectKeyword("include")
	source, err := current.expectIdentifier("included interface type name")
	if err != nil {
		return InterfaceDerivationDecl{}, err
	}
	result := InterfaceDerivationDecl{Position: start.position, Source: source.lexeme, Region: region}
	if current.peekKeyword("only") || current.peekKeyword("except") {
		modifier := current.advance()
		if keyword(modifier.lexeme, "only") {
			result.Modifier = InterfaceDerivationOnly
		} else {
			result.Modifier = InterfaceDerivationExcept
		}
		names, err := current.parseInterfaceIdentifierList("interface derivation name")
		if err != nil {
			return InterfaceDerivationDecl{}, err
		}
		result.Names = names
	}
	if current.peekKeyword("replace") {
		current.advance()
		if _, err := current.expect(tokenLParen, "'('"); err != nil {
			return InterfaceDerivationDecl{}, err
		}
		seen := make(map[string]bool)
		for {
			from, err := current.expectIdentifier("replaced interface constituent name")
			if err != nil {
				return InterfaceDerivationDecl{}, err
			}
			if seen[folded(from.lexeme)] {
				return InterfaceDerivationDecl{}, &SyntaxError{
					Position: from.position,
					Message:  fmt.Sprintf("replacement source %q is named more than once", from.lexeme),
				}
			}
			seen[folded(from.lexeme)] = true
			if _, err := current.expectKeyword("to"); err != nil {
				return InterfaceDerivationDecl{}, err
			}
			to, err := current.expectIdentifier("replacement interface constituent name")
			if err != nil {
				return InterfaceDerivationDecl{}, err
			}
			result.Replacements = append(result.Replacements, InterfaceReplacementDecl{
				Position: from.position, From: from.lexeme, To: to.lexeme,
			})
			if current.peek().kind != tokenComma {
				break
			}
			current.advance()
		}
		if _, err := current.expect(tokenRParen, "')'"); err != nil {
			return InterfaceDerivationDecl{}, err
		}
	}
	if current.peekKeyword("only") || current.peekKeyword("except") || current.peekKeyword("replace") {
		return InterfaceDerivationDecl{}, &SyntaxError{
			Position: current.peek().position,
			Message:  "interface derivation modifiers must occur at most once in only/except then replace order",
		}
	}
	if _, err := current.expect(tokenSemicolon, "';'"); err != nil {
		return InterfaceDerivationDecl{}, &SyntaxError{
			Position: current.peek().position,
			Message:  "the current interface-derivation subset requires a named interface followed by only/except, replace, or ';'",
		}
	}
	return result, nil
}

func (current *parser) parseInterfaceIdentifierList(description string) ([]string, error) {
	if _, err := current.expect(tokenLParen, "'('"); err != nil {
		return nil, err
	}
	result := make([]string, 0, 1)
	seen := make(map[string]bool)
	for {
		name, err := current.expectIdentifier(description)
		if err != nil {
			return nil, err
		}
		if seen[folded(name.lexeme)] {
			return nil, &SyntaxError{Position: name.position, Message: fmt.Sprintf("%s %q is named more than once", description, name.lexeme)}
		}
		seen[folded(name.lexeme)] = true
		result = append(result, name.lexeme)
		if current.peek().kind != tokenComma {
			break
		}
		current.advance()
	}
	if _, err := current.expect(tokenRParen, "')'"); err != nil {
		return nil, err
	}
	return result, nil
}

func (current *parser) parseBehavior() (BehaviorDecl, error) {
	start, _ := current.expectKeyword("behavior")
	result := BehaviorDecl{Position: start.position}
	for !current.peekKeyword("begin") {
		if current.peek().kind == tokenEOF || current.peekKeyword("end") {
			return BehaviorDecl{}, current.unexpected("behavior declaration or 'begin'")
		}
		if current.peekAt(2).kind == tokenIdentifier && keyword(current.peekAt(2).lexeme, "var") {
			state, err := current.parseBehaviorState()
			if err != nil {
				return BehaviorDecl{}, err
			}
			result.States = append(result.States, state)
			continue
		}
		function, err := current.parseBehaviorFunction()
		if err != nil {
			return BehaviorDecl{}, err
		}
		result.Functions = append(result.Functions, function)
	}
	current.advance()
	for !current.peekKeyword("end") {
		rule, err := current.parseBehaviorRule()
		if err != nil {
			return BehaviorDecl{}, err
		}
		result.Rules = append(result.Rules, rule)
	}
	return result, nil
}

func (current *parser) parseBehaviorState() (StateDecl, error) {
	name, err := current.expectIdentifier("behavior state name")
	if err != nil {
		return StateDecl{}, err
	}
	if _, err := current.expect(tokenColon, "':'"); err != nil {
		return StateDecl{}, err
	}
	if _, err := current.expectKeyword("var"); err != nil {
		return StateDecl{}, err
	}
	typeExpression, err := current.parseClosedTypeExpression("behavior state type")
	if err != nil {
		return StateDecl{}, err
	}
	result := StateDecl{
		Position: name.position, Name: name.lexeme,
		Type: typeExpressionSpelling(typeExpression), TypeExpression: typeExpression,
	}
	if current.peek().kind == tokenAssign {
		current.advance()
		initial, err := current.parseExpression()
		if err != nil {
			return StateDecl{}, err
		}
		result.Initial = &initial
	}
	if _, err := current.expect(tokenSemicolon, "';'"); err != nil {
		return StateDecl{}, err
	}
	return result, nil
}

func (current *parser) parseBehaviorRule() (BehaviorRuleDecl, error) {
	result := BehaviorRuleDecl{Position: current.peek().position}
	if placeholders, present, err := current.parsePatternPlaceholderDeclarations(); err != nil {
		return BehaviorRuleDecl{}, err
	} else if present {
		result.Placeholders = placeholders
	}
	trigger, err := current.parseBehaviorPattern()
	if err != nil {
		return BehaviorRuleDecl{}, err
	}
	result.Trigger = trigger
	if isBehaviorPatternOperator(current.peek()) {
		return BehaviorRuleDecl{}, &SyntaxError{
			Position: current.peek().position,
			Message:  "chained behavior pattern operators require explicit parentheses in the current source subset",
		}
	}
	if current.peekKeyword("where") {
		current.advance()
		guard, err := current.parseExpression()
		if err != nil {
			return BehaviorRuleDecl{}, err
		}
		result.Guard = &guard
	}
	operator := current.peek()
	switch operator.kind {
	case tokenPipe:
		result.Connector = ConnectPipe
	case tokenAgent:
		result.Connector = ConnectAgent
	default:
		return BehaviorRuleDecl{}, current.unexpected("behavior rule operator '=>' or '||>'")
	}
	current.advance()
	for current.peek().kind != tokenSemicolon {
		if current.peek().kind == tokenEOF || current.peekKeyword("end") {
			return BehaviorRuleDecl{}, current.unexpected("behavior statement or terminating ';'")
		}
		statement, err := current.parseBehaviorStatement()
		if err != nil {
			return BehaviorRuleDecl{}, err
		}
		result.Statements = append(result.Statements, statement)
	}
	current.advance()
	return result, nil
}

// parsePatternPlaceholderDeclarations parses the outer qualification form used
// by transition rules, module when processes, constraints, and connections.
// The current universal subset is Stanford's explicit finite form
// `!i : Integer range first..last by op`; semantic validation and the fixed
// compatibility-profile cardinality bound are applied by the compiler.
func (current *parser) parsePatternPlaceholderDeclarations() ([]ParameterDecl, bool, error) {
	if current.peek().kind != tokenLParen ||
		(current.peekAt(1).kind != tokenQuestion && current.peekAt(1).kind != tokenBang) {
		return nil, false, nil
	}
	current.advance()
	result := make([]ParameterDecl, 0)
	for {
		marker := current.peek()
		if marker.kind != tokenQuestion && marker.kind != tokenBang {
			return nil, true, current.unexpected("'?' or '!' placeholder declaration")
		}
		qualification := PlaceholderExistential
		if marker.kind == tokenBang {
			qualification = PlaceholderUniversal
		}
		names := make([]token, 0, 1)
		positions := make([]Position, 0, 1)
		for {
			position := current.advance().position
			name, err := current.expectIdentifier("placeholder name")
			if err != nil {
				return nil, true, err
			}
			names = append(names, name)
			positions = append(positions, position)
			if current.peek().kind != tokenComma || current.peekAt(1).kind != marker.kind {
				break
			}
			current.advance()
		}
		if _, err := current.expect(tokenColon, "':'"); err != nil {
			return nil, true, err
		}
		typeExpression, err := current.parseClosedTypeExpression("placeholder type")
		if err != nil {
			return nil, true, err
		}
		declarations := make([]ParameterDecl, len(names))
		for index, name := range names {
			declarations[index] = ParameterDecl{
				Position: positions[index], Name: name.lexeme,
				Type: typeExpressionSpelling(typeExpression), TypeExpression: typeExpression,
				Qualification: qualification,
			}
		}
		if qualification == PlaceholderUniversal {
			if len(declarations) != 1 {
				return nil, true, &SyntaxError{
					Position: marker.position,
					Message:  "a universal placeholder declaration names exactly one placeholder",
				}
			}
			if _, err := current.expectKeyword("range"); err != nil {
				return nil, true, &SyntaxError{
					Position: current.peek().position,
					Message:  "the current universal domain subset requires 'Integer range first..last'",
				}
			}
			first, err := current.parseSignedIntegerLiteral("universal range lower bound")
			if err != nil {
				return nil, true, err
			}
			if _, err := current.expect(tokenDot, "first '.' in '..'"); err != nil {
				return nil, true, err
			}
			if _, err := current.expect(tokenDot, "second '.' in '..'"); err != nil {
				return nil, true, err
			}
			last, err := current.parseSignedIntegerLiteral("universal range upper bound")
			if err != nil {
				return nil, true, err
			}
			if _, err := current.expectKeyword("by"); err != nil {
				return nil, true, err
			}
			relation := current.peek()
			if !isBehaviorPatternOperator(relation) {
				return nil, true, current.unexpected("universal relation '->', '|>', '||', '~', 'and', 'or', or '<=>'")
			}
			current.advance()
			declarations[0].RangeFirst = first
			declarations[0].RangeLast = last
			declarations[0].Relation = strings.ToLower(relation.lexeme)
		}
		result = append(result, declarations...)
		if current.peek().kind != tokenSemicolon && current.peek().kind != tokenComma {
			break
		}
		current.advance()
		if current.peek().kind != tokenQuestion && current.peek().kind != tokenBang {
			return nil, true, current.unexpected("placeholder declaration after separator")
		}
	}
	if _, err := current.expect(tokenRParen, "')'"); err != nil {
		return nil, true, err
	}
	return result, true, nil
}

func (current *parser) parseSignedIntegerLiteral(description string) (int64, error) {
	negative := false
	position := current.peek().position
	if current.peek().kind == tokenMinus {
		negative = true
		position = current.advance().position
	}
	literal, err := current.expect(tokenInteger, description)
	if err != nil {
		return 0, err
	}
	text := literal.lexeme
	if negative {
		text = "-" + text
	}
	value, parseErr := strconv.ParseInt(text, 10, 64)
	if parseErr != nil {
		return 0, &SyntaxError{Position: position, Message: description + " is outside the signed 64-bit deterministic subset"}
	}
	return value, nil
}

func (current *parser) parseBehaviorPattern() (BehaviorPatternDecl, error) {
	left, err := current.parseBehaviorPatternPrimary()
	if err != nil {
		return BehaviorPatternDecl{}, err
	}
	if !isBehaviorPatternOperator(current.peek()) {
		return left, nil
	}
	operator := current.advance()
	right, err := current.parseBehaviorPatternPrimary()
	if err != nil {
		return BehaviorPatternDecl{}, err
	}
	leftCopy, rightCopy := left, right
	return BehaviorPatternDecl{
		Position: operator.position, Kind: BehaviorBinaryPattern, Operator: strings.ToLower(operator.lexeme),
		Left: &leftCopy, Right: &rightCopy,
	}, nil
}

func (current *parser) parseBehaviorPatternPrimary() (BehaviorPatternDecl, error) {
	if current.peek().kind == tokenLBracket {
		return current.parseBehaviorIterationPattern()
	}
	if current.peek().kind == tokenLParen {
		if current.peekAt(1).kind == tokenQuestion || current.peekAt(1).kind == tokenBang {
			return BehaviorPatternDecl{}, &SyntaxError{
				Position: current.peek().position,
				Message:  "nested placeholder qualification is outside the current source subset",
			}
		}
		current.advance()
		result, err := current.parseBehaviorPattern()
		if err != nil {
			return BehaviorPatternDecl{}, err
		}
		if isBehaviorPatternOperator(current.peek()) {
			return BehaviorPatternDecl{}, &SyntaxError{
				Position: current.peek().position,
				Message:  "chained behavior pattern operators require explicit parentheses in the current source subset",
			}
		}
		if _, err := current.expect(tokenRParen, "')'"); err != nil {
			return BehaviorPatternDecl{}, err
		}
		return result, nil
	}
	if current.peek().kind == tokenQuestion {
		start := current.advance()
		module, err := current.expectIdentifier("qualified module placeholder")
		if err != nil {
			return BehaviorPatternDecl{}, err
		}
		if _, err := current.expect(tokenDot, "'.'"); err != nil {
			return BehaviorPatternDecl{}, err
		}
		action, path, err := current.parseQualifiedMemberPath("module-placeholder pattern action name")
		if err != nil {
			return BehaviorPatternDecl{}, err
		}
		event := BehaviorEventDecl{
			Position: start.position, Component: module.lexeme, ComponentPlaceholder: true,
			Name: action, Path: path,
		}
		return current.parseBehaviorPatternEventSuffix(event)
	}
	name, err := current.expectIdentifier("basic behavior trigger action")
	if err != nil {
		return BehaviorPatternDecl{}, err
	}
	event := BehaviorEventDecl{Position: name.position, Name: name.lexeme}
	if current.hasScopeNameSeparator() {
		event.Name, err = current.parseScopeNamePath(name)
		if err != nil {
			return BehaviorPatternDecl{}, err
		}
	} else if current.peek().kind == tokenLBracket {
		index, err := current.parseComponentSelection()
		if err != nil {
			return BehaviorPatternDecl{}, err
		}
		action, path, err := current.parseQualifiedMemberPath("selected-component pattern action name")
		if err != nil {
			return BehaviorPatternDecl{}, err
		}
		event.Component, event.ComponentIndex, event.Name, event.Path = name.lexeme, &index, action, path
	} else if current.peek().kind == tokenDot {
		current.advance()
		action, path, err := current.parseQualifiedMemberPath("qualified pattern action name")
		if err != nil {
			return BehaviorPatternDecl{}, err
		}
		event.Component, event.Name, event.Path = name.lexeme, action, path
	}
	return current.parseBehaviorPatternEventSuffix(event)
}

func (current *parser) parseBehaviorPatternEventSuffix(event BehaviorEventDecl) (BehaviorPatternDecl, error) {
	if current.peek().kind == tokenApostrophe {
		current.advance()
		attribute, err := current.expectIdentifier("behavior event attribute")
		if err != nil {
			return BehaviorPatternDecl{}, err
		}
		event.Attribute = attribute.lexeme
	}
	if current.peek().kind == tokenLParen {
		current.advance()
		if current.peek().kind != tokenRParen {
			named := false
			for {
				association, err := current.parsePatternParameterAssociation()
				if err != nil {
					return BehaviorPatternDecl{}, err
				}
				if association.Formal != "" {
					named = true
				} else if named {
					return BehaviorPatternDecl{}, &SyntaxError{
						Position: association.Position,
						Message:  "positional basic-pattern associations must precede named associations",
					}
				}
				event.Arguments = append(event.Arguments, association)
				if current.peek().kind != tokenComma {
					break
				}
				current.advance()
			}
		}
		if _, err := current.expect(tokenRParen, "')'"); err != nil {
			return BehaviorPatternDecl{}, err
		}
	}
	return BehaviorPatternDecl{Position: event.Position, Kind: BehaviorBasicPattern, Event: event}, nil
}

func (current *parser) hasScopeNameSeparator() bool {
	return current.peek().kind == tokenColon && current.peekAt(1).kind == tokenColon
}

// parseScopeNamePath parses Stanford's S::N notation after the first basic
// name. It preserves the declarative-region path distinctly from object
// selection and service-rewritten dotted names.
func (current *parser) parseScopeNamePath(first token) (string, error) {
	parts := []string{first.lexeme}
	for current.hasScopeNameSeparator() {
		current.advance()
		current.advance()
		name, err := current.expectIdentifier("name after scope-name separator '::'")
		if err != nil {
			return "", err
		}
		parts = append(parts, name.lexeme)
	}
	return strings.Join(parts, "::"), nil
}

func (current *parser) parsePatternParameterAssociation() (PatternParameterAssociationDecl, error) {
	start := current.peek().position
	formal := ""
	if current.peek().kind == tokenIdentifier && current.peekAt(1).kind == tokenIdentifier &&
		keyword(current.peekAt(1).lexeme, "is") {
		formal = current.advance().lexeme
		current.advance()
	}
	actual, err := current.parseExpression()
	if err != nil {
		return PatternParameterAssociationDecl{}, err
	}
	return PatternParameterAssociationDecl{Position: start, Formal: formal, Actual: actual}, nil
}

func (current *parser) parseBehaviorIterationPattern() (BehaviorPatternDecl, error) {
	start, _ := current.expect(tokenLBracket, "'['")
	iterator := ""
	if current.peek().kind == tokenIdentifier && current.peekAt(1).kind == tokenColon {
		iterator = current.advance().lexeme
		current.advance()
		first, err := current.parseSignedIntegerLiteral("named iterator range lower bound")
		if err != nil {
			return BehaviorPatternDecl{}, err
		}
		if _, err := current.expect(tokenDot, "first '.' in named iterator range '..'"); err != nil {
			return BehaviorPatternDecl{}, err
		}
		if _, err := current.expect(tokenDot, "second '.' in named iterator range '..'"); err != nil {
			return BehaviorPatternDecl{}, err
		}
		last, err := current.parseSignedIntegerLiteral("named iterator range upper bound")
		if err != nil {
			return BehaviorPatternDecl{}, err
		}
		relation, err := current.parseBehaviorIterationRelation()
		if err != nil {
			return BehaviorPatternDecl{}, err
		}
		inner, err := current.parseBehaviorPatternPrimary()
		if err != nil {
			return BehaviorPatternDecl{}, err
		}
		innerCopy := inner
		return BehaviorPatternDecl{
			Position: start.position, Kind: BehaviorIterationPattern,
			Iterator: iterator, First: first, Last: last,
			Relation: strings.ToLower(relation.lexeme), Inner: &innerCopy,
		}, nil
	}
	minimum, maximum := 0, -1
	switch cardinality := current.peek(); cardinality.kind {
	case tokenStar:
		current.advance()
	case tokenPlus:
		current.advance()
		minimum = 1
	case tokenInteger, tokenMinus:
		first, err := current.parseSignedIntegerLiteral("iteration cardinality or range lower bound")
		if err != nil {
			return BehaviorPatternDecl{}, &SyntaxError{Position: cardinality.position, Message: "iteration cardinality is outside the host-independent integer subset"}
		}
		if current.peek().kind == tokenDot {
			current.advance()
			if _, err := current.expect(tokenDot, "second '.' in iterator range '..'"); err != nil {
				return BehaviorPatternDecl{}, err
			}
			last, err := current.parseSignedIntegerLiteral("iterator range upper bound")
			if err != nil {
				return BehaviorPatternDecl{}, err
			}
			count := uint64(0)
			if last >= first {
				difference := uint64(last) - uint64(first)
				if difference >= uint64(1<<31-1) {
					return BehaviorPatternDecl{}, &SyntaxError{Position: cardinality.position, Message: "iteration range cardinality is outside the host-independent integer subset"}
				}
				count = difference + 1
			}
			minimum, maximum = int(count), int(count)
		} else {
			if first < 0 {
				return BehaviorPatternDecl{}, &SyntaxError{Position: cardinality.position, Message: "expected iteration cardinality '*', '+', or nonnegative integer"}
			}
			if first > int64(1<<31-1) {
				return BehaviorPatternDecl{}, &SyntaxError{Position: cardinality.position, Message: "iteration cardinality is outside the host-independent integer subset"}
			}
			minimum, maximum = int(first), int(first)
		}
	default:
		return BehaviorPatternDecl{}, current.unexpected("iteration cardinality '*', '+', nonnegative integer, Integer range, or named Integer range")
	}
	relation, err := current.parseBehaviorIterationRelation()
	if err != nil {
		return BehaviorPatternDecl{}, err
	}
	inner, err := current.parseBehaviorPatternPrimary()
	if err != nil {
		return BehaviorPatternDecl{}, err
	}
	innerCopy := inner
	return BehaviorPatternDecl{
		Position: start.position, Kind: BehaviorIterationPattern,
		Minimum: minimum, Maximum: maximum, Relation: strings.ToLower(relation.lexeme), Inner: &innerCopy,
	}, nil
}

func (current *parser) parseBehaviorIterationRelation() (token, error) {
	if _, err := current.expectKeyword("rel"); err != nil {
		return token{}, err
	}
	relation := current.peek()
	if !isBehaviorPatternOperator(relation) {
		return token{}, current.unexpected("iteration relation '->', '|>', '||', '~', 'and', 'or', or '<=>' ")
	}
	current.advance()
	if _, err := current.expect(tokenRBracket, "']'"); err != nil {
		return token{}, err
	}
	return relation, nil
}

func isBehaviorPatternOperator(candidate token) bool {
	switch candidate.kind {
	case tokenSequence, tokenImmediateSequence, tokenIndependent, tokenDisjoint, tokenEquivalent:
		return true
	case tokenIdentifier:
		return keyword(candidate.lexeme, "and") || keyword(candidate.lexeme, "or")
	default:
		return false
	}
}

func (current *parser) parseBehaviorFunction() (FunctionBodyDecl, error) {
	name, err := current.expectIdentifier("behavior function name")
	if err != nil {
		return FunctionBodyDecl{}, err
	}
	if _, err := current.expect(tokenColon, "':'"); err != nil {
		return FunctionBodyDecl{}, err
	}
	if _, err := current.expectKeyword("function"); err != nil {
		return FunctionBodyDecl{}, err
	}
	if _, err := current.expect(tokenLParen, "'('"); err != nil {
		return FunctionBodyDecl{}, err
	}
	result := FunctionBodyDecl{Position: name.position, Name: name.lexeme}
	if current.peek().kind != tokenRParen {
		for {
			parameterNames, err := current.parseFormalIdentifierList("function parameter name")
			if err != nil {
				return FunctionBodyDecl{}, err
			}
			if _, err := current.expect(tokenColon, "':'"); err != nil {
				return FunctionBodyDecl{}, err
			}
			parameterType, err := current.parseClosedTypeExpression("function parameter type")
			if err != nil {
				return FunctionBodyDecl{}, err
			}
			var defaultExpression *ExpressionDecl
			if current.peekKeyword("is") {
				current.advance()
				value, err := current.parseExpression()
				if err != nil {
					return FunctionBodyDecl{}, err
				}
				defaultExpression = &value
			}
			for _, parameterName := range parameterNames {
				result.Parameters = append(result.Parameters, ParameterDecl{
					Position: parameterName.position, Name: parameterName.lexeme,
					Type: typeExpressionSpelling(parameterType), TypeExpression: parameterType,
					Default: cloneExpressionDeclarationPointer(defaultExpression),
				})
			}
			if current.peek().kind != tokenComma && current.peek().kind != tokenSemicolon {
				break
			}
			current.advance()
		}
	}
	if _, err := current.expect(tokenRParen, "')'"); err != nil {
		return FunctionBodyDecl{}, err
	}
	if current.peekKeyword("return") {
		current.advance()
		returnType, err := current.parseClosedTypeExpression("function return type")
		if err != nil {
			return FunctionBodyDecl{}, err
		}
		result.ReturnType = typeExpressionSpelling(returnType)
		result.ReturnTypeExpression = returnType
	}
	if _, err := current.expectKeyword("is"); err != nil {
		return FunctionBodyDecl{}, err
	}
	for !current.peekKeyword("begin") {
		if current.peek().kind == tokenEOF {
			return FunctionBodyDecl{}, current.unexpected("function local object declaration or 'begin'")
		}
		objects, err := current.parseModuleObjectDeclaration()
		if err != nil {
			return FunctionBodyDecl{}, err
		}
		result.Objects = append(result.Objects, objects...)
	}
	if _, err := current.expectKeyword("begin"); err != nil {
		return FunctionBodyDecl{}, err
	}
	for !current.peekKeyword("handler") && !current.peekKeyword("end") {
		statement, err := current.parseBehaviorStatement()
		if err != nil {
			return FunctionBodyDecl{}, err
		}
		result.Statements = append(result.Statements, statement)
	}
	// A typed function's final top-level return is retained as its deterministic
	// fall-through value. Other returns remain ordinary control-flow statements.
	if len(result.Statements) != 0 {
		last := result.Statements[len(result.Statements)-1]
		if last.Kind == BehaviorReturnStatement && last.Expression.Kind != "" {
			value := last.Expression
			result.Return = &value
			result.Statements = result.Statements[:len(result.Statements)-1]
		}
	}
	if current.peekKeyword("handler") {
		handler, err := current.parseBehaviorHandler()
		if err != nil {
			return FunctionBodyDecl{}, err
		}
		result.Handler = &handler
	}
	current.advance()
	if current.peekKeyword("function") {
		current.advance()
	}
	if current.peek().kind == tokenIdentifier && keyword(current.peek().lexeme, result.Name) {
		current.advance()
	}
	if _, err := current.expect(tokenSemicolon, "';'"); err != nil {
		return FunctionBodyDecl{}, err
	}
	return result, nil
}

func (current *parser) parseBehaviorStatement() (BehaviorStatementDecl, error) {
	if current.peek().kind == tokenIdentifier && current.peekAt(1).kind == tokenColon &&
		!current.peekKeyword("for") {
		label := current.advance()
		current.advance()
		switch {
		case current.peekKeyword("declare"):
			return current.parseBehaviorDeclaredDoStatement(label.lexeme)
		case current.peekKeyword("do"):
			return current.parseBehaviorDoStatement(label.lexeme)
		case current.peekKeyword("for"):
			return current.parseBehaviorForStatement(label.lexeme)
		case current.peekKeyword("loop") || current.peekKeyword("while"):
			return current.parseBehaviorLoopStatement(label.lexeme)
		default:
			return BehaviorStatementDecl{}, &SyntaxError{
				Position: label.position,
				Message:  "labels on non-do statements are outside the current source subset",
			}
		}
	}
	if current.peekKeyword("declare") {
		return current.parseBehaviorDeclaredDoStatement("")
	}
	if current.peekKeyword("await") {
		return BehaviorStatementDecl{}, &SyntaxError{
			Position: current.peek().position,
			Message:  "nested await statements are outside the current top-level module process subset",
		}
	}
	if current.peekKeyword("raise") {
		start := current.advance()
		unnamed := current.peekKeyword("where") || current.peek().kind == tokenSemicolon
		result := BehaviorStatementDecl{Position: start.position, Kind: BehaviorReraiseStatement}
		var call CallStatementDecl
		var err error
		if !unnamed {
			if current.peek().kind != tokenIdentifier {
				return BehaviorStatementDecl{}, current.unexpected("exception name, 'where', or ';' after raise")
			}
			name := current.advance()
			call = CallStatementDecl{Position: name.position, Name: name.lexeme}
			if current.hasScopeNameSeparator() {
				call.Name, err = current.parseScopeNamePath(name)
			} else if current.peek().kind == tokenDot {
				current.advance()
				member, _, pathErr := current.parseQualifiedMemberPath("selected exception name")
				if pathErr != nil {
					return BehaviorStatementDecl{}, pathErr
				}
				call.Name += "." + member
			}
			if current.peek().kind == tokenLParen {
				call, err = current.parseCallArguments(call)
			}
			result.Kind = BehaviorRaiseStatement
			result.Call = call
		}
		if err != nil {
			return BehaviorStatementDecl{}, err
		}
		if current.peekKeyword("where") {
			current.advance()
			condition, err := current.parseExpression()
			if err != nil {
				return BehaviorStatementDecl{}, err
			}
			result.Condition = &condition
		}
		if _, err := current.expect(tokenSemicolon, "';'"); err != nil {
			return BehaviorStatementDecl{}, err
		}
		return result, nil
	}
	if current.peekKeyword("pause") || current.peekKeyword("delay") {
		start := current.advance()
		kind := TimingPause
		if keyword(start.lexeme, "delay") {
			kind = TimingDelay
		}
		timing, err := current.parseTimingExpression(start.position, kind)
		if err != nil {
			return BehaviorStatementDecl{}, err
		}
		if _, err := current.expect(tokenSemicolon, "';'"); err != nil {
			return BehaviorStatementDecl{}, err
		}
		return BehaviorStatementDecl{Position: start.position, Kind: BehaviorTimedStatement, Timing: &timing}, nil
	}
	if current.peekKeyword("if") {
		return current.parseBehaviorIfStatement()
	}
	if current.peekKeyword("do") {
		return current.parseBehaviorDoStatement("")
	}
	if current.peekKeyword("for") {
		return current.parseBehaviorForStatement("")
	}
	if current.peekKeyword("loop") || current.peekKeyword("while") {
		return current.parseBehaviorLoopStatement("")
	}
	if current.peekKeyword("case") {
		return current.parseBehaviorCaseStatement()
	}
	if (current.peekKeyword("exit") || current.peekKeyword("next")) && current.peekAt(1).kind != tokenLParen {
		return current.parseBehaviorLoopControlStatement()
	}
	if current.peekKeyword("return") {
		start := current.advance()
		result := BehaviorStatementDecl{Position: start.position, Kind: BehaviorReturnStatement}
		if current.peek().kind != tokenSemicolon {
			value, err := current.parseExpression()
			if err != nil {
				return BehaviorStatementDecl{}, err
			}
			result.Expression = value
		}
		if _, err := current.expect(tokenSemicolon, "';'"); err != nil {
			return BehaviorStatementDecl{}, err
		}
		return result, nil
	}
	if current.peekKeyword("assert") {
		start := current.advance()
		condition, err := current.parseExpression()
		if err != nil {
			return BehaviorStatementDecl{}, err
		}
		if _, err := current.expect(tokenSemicolon, "';'"); err != nil {
			return BehaviorStatementDecl{}, err
		}
		return BehaviorStatementDecl{
			Position: start.position, Kind: BehaviorAssertStatement, Condition: &condition,
		}, nil
	}
	if current.peekKeyword("null") {
		start := current.advance()
		if _, err := current.expect(tokenSemicolon, "';'"); err != nil {
			return BehaviorStatementDecl{}, err
		}
		return BehaviorStatementDecl{Position: start.position, Kind: BehaviorNullStatement}, nil
	}
	if current.peek().kind == tokenIdentifier && current.peekAt(1).kind == tokenAssign {
		target := current.advance()
		current.advance()
		if current.peek().kind == tokenIdentifier && current.peekAt(1).kind == tokenLParen {
			call, err := current.parseCall()
			if err != nil {
				return BehaviorStatementDecl{}, err
			}
			if _, err := current.expect(tokenSemicolon, "';'"); err != nil {
				return BehaviorStatementDecl{}, err
			}
			return BehaviorStatementDecl{
				Position: target.position, Kind: BehaviorAssignmentStatement,
				Target: target.lexeme, Function: &call,
			}, nil
		}
		value, err := current.parseExpression()
		if err != nil {
			return BehaviorStatementDecl{}, err
		}
		if _, err := current.expect(tokenSemicolon, "';'"); err != nil {
			return BehaviorStatementDecl{}, err
		}
		return BehaviorStatementDecl{
			Position: target.position, Kind: BehaviorAssignmentStatement,
			Target: target.lexeme, Expression: value,
		}, nil
	}
	call, err := current.parseCallStatement()
	if err != nil {
		return BehaviorStatementDecl{}, err
	}
	return BehaviorStatementDecl{Position: call.Position, Kind: BehaviorCallStatement, Call: call}, nil
}

func (current *parser) parseBehaviorDoStatement(label string) (BehaviorStatementDecl, error) {
	start := current.advance()
	result := BehaviorStatementDecl{Position: start.position, Kind: BehaviorDoStatement, Label: label}
	return current.parseBehaviorDoBody(result)
}

func (current *parser) parseBehaviorDeclaredDoStatement(label string) (BehaviorStatementDecl, error) {
	start, err := current.expectKeyword("declare")
	if err != nil {
		return BehaviorStatementDecl{}, err
	}
	result := BehaviorStatementDecl{Position: start.position, Kind: BehaviorDoStatement, Label: label}
	for current.peekKeyword("exception") {
		exception, err := current.parseModuleExceptionDeclaration()
		if err != nil {
			return BehaviorStatementDecl{}, err
		}
		result.Exceptions = append(result.Exceptions, exception)
	}
	if len(result.Exceptions) == 0 {
		return BehaviorStatementDecl{}, &SyntaxError{
			Position: start.position,
			Message:  "the current declaration-bearing do subset requires one or more exception declarations",
		}
	}
	if _, err := current.expectKeyword("do"); err != nil {
		return BehaviorStatementDecl{}, &SyntaxError{
			Position: current.peek().position,
			Message:  "the current declaration-bearing do subset supports exception declarations followed directly by 'do'",
		}
	}
	return current.parseBehaviorDoBody(result)
}

func (current *parser) parseBehaviorDoBody(result BehaviorStatementDecl) (BehaviorStatementDecl, error) {
	for !current.peekKeyword("handler") && !current.peekKeyword("end") {
		if current.peek().kind == tokenEOF {
			return BehaviorStatementDecl{}, current.unexpected("do statement, handler, or 'end do'")
		}
		statement, err := current.parseBehaviorStatement()
		if err != nil {
			return BehaviorStatementDecl{}, err
		}
		result.Body = append(result.Body, statement)
	}
	if len(result.Body) == 0 {
		return BehaviorStatementDecl{}, current.unexpected("do statement body")
	}
	if current.peekKeyword("handler") {
		handler, err := current.parseBehaviorHandler()
		if err != nil {
			return BehaviorStatementDecl{}, err
		}
		result.Handler = &handler
	}
	if _, err := current.expectKeyword("end"); err != nil {
		return BehaviorStatementDecl{}, err
	}
	if current.peekKeyword("do") {
		current.advance()
	}
	terminator, err := current.parseBehaviorDoTerminator(result.Label)
	if err != nil {
		return BehaviorStatementDecl{}, err
	}
	result.Terminator = terminator
	if _, err := current.expect(tokenSemicolon, "';'"); err != nil {
		return BehaviorStatementDecl{}, err
	}
	return result, nil
}

func (current *parser) parseBehaviorDoTerminator(label string) (string, error) {
	if current.peek().kind != tokenIdentifier {
		return "", nil
	}
	terminator := current.advance()
	if label == "" {
		return "", &SyntaxError{
			Position: terminator.position,
			Message:  "a named do terminator requires a statement label",
		}
	}
	if !keyword(terminator.lexeme, label) {
		return "", &SyntaxError{
			Position: terminator.position,
			Message: fmt.Sprintf(
				"do terminator %q does not match statement label %q",
				terminator.lexeme, label,
			),
		}
	}
	return terminator.lexeme, nil
}

func (current *parser) parseBehaviorForStatement(label string) (BehaviorStatementDecl, error) {
	start := current.advance()
	result := BehaviorStatementDecl{
		Position: start.position, Kind: BehaviorForStatement, Label: label,
		IteratorType: "Integer",
	}
	if current.behaviorForHasNextClause() {
		initial, err := current.parseBehaviorObjectExpression()
		if err != nil {
			return BehaviorStatementDecl{}, err
		}
		if _, err := current.expectKeyword("in"); err != nil {
			return BehaviorStatementDecl{}, err
		}
		test, err := current.parseBehaviorObjectExpression()
		if err != nil {
			return BehaviorStatementDecl{}, err
		}
		if _, err := current.expectKeyword("next"); err != nil {
			return BehaviorStatementDecl{}, err
		}
		next, err := current.parseBehaviorObjectExpression()
		if err != nil {
			return BehaviorStatementDecl{}, err
		}
		result.ForInitial = &initial
		result.ForTest = &test
		result.ForNext = &next
	} else {
		if current.peek().kind == tokenIdentifier &&
			(current.peekAt(1).kind == tokenColon || keyword(current.peekAt(1).lexeme, "in")) {
			result.Iterator = current.advance().lexeme
		}
		if current.peek().kind == tokenColon {
			current.advance()
			typeName, err := current.expect(tokenIdentifier, "iterator type expression")
			if err != nil {
				return BehaviorStatementDecl{}, err
			}
			result.IteratorType = typeName.lexeme
		}
		if current.peekKeyword("in") {
			current.advance()
		}
		if current.peek().kind == tokenIdentifier && current.peekAt(1).kind == tokenLParen {
			return BehaviorStatementDecl{}, &SyntaxError{
				Position: current.peek().position,
				Message:  "source Iterator(T) module-generator call expressions are outside the current source subset",
			}
		}
		first, err := current.parseExpression()
		if err != nil {
			return BehaviorStatementDecl{}, err
		}
		if _, err := current.expect(tokenDot, "first '.' in procedural iterator range '..'"); err != nil {
			return BehaviorStatementDecl{}, err
		}
		if _, err := current.expect(tokenDot, "second '.' in procedural iterator range '..'"); err != nil {
			return BehaviorStatementDecl{}, err
		}
		last, err := current.parseExpression()
		if err != nil {
			return BehaviorStatementDecl{}, err
		}
		result.RangeFirst = first
		result.RangeLast = last
	}
	if _, err := current.expectKeyword("do"); err != nil {
		return BehaviorStatementDecl{}, err
	}
	for !current.peekKeyword("end") {
		if current.peek().kind == tokenEOF {
			return BehaviorStatementDecl{}, current.unexpected("behavior statement or 'end'")
		}
		statement, err := current.parseBehaviorStatement()
		if err != nil {
			return BehaviorStatementDecl{}, err
		}
		result.Body = append(result.Body, statement)
	}
	current.advance()
	if current.peekKeyword("for") || current.peekKeyword("do") {
		current.advance()
	}
	terminator, err := current.parseBehaviorDoTerminator(label)
	if err != nil {
		return BehaviorStatementDecl{}, err
	}
	result.Terminator = terminator
	if _, err := current.expect(tokenSemicolon, "';'"); err != nil {
		return BehaviorStatementDecl{}, err
	}
	return result, nil
}

func (current *parser) behaviorForHasNextClause() bool {
	depth := 0
	for index := current.index; index < len(current.tokens); index++ {
		token := current.tokens[index]
		switch token.kind {
		case tokenLParen, tokenLBracket:
			depth++
		case tokenRParen, tokenRBracket:
			if depth > 0 {
				depth--
			}
		case tokenEOF, tokenSemicolon:
			return false
		}
		if depth == 0 && keyword(token.lexeme, "next") {
			return true
		}
		if depth == 0 && keyword(token.lexeme, "do") {
			return false
		}
	}
	return false
}

func (current *parser) parseBehaviorObjectExpression() (BehaviorObjectExpressionDecl, error) {
	start := current.peek()
	if start.kind == tokenIdentifier && current.peekAt(1).kind == tokenAssign {
		target := current.advance()
		current.advance()
		if current.peek().kind == tokenIdentifier && current.peekAt(1).kind == tokenLParen {
			return BehaviorObjectExpressionDecl{}, &SyntaxError{
				Position: current.peek().position,
				Message:  "nested function calls in assignment object expressions are outside the current source subset",
			}
		}
		value, err := current.parseExpression()
		if err != nil {
			return BehaviorObjectExpressionDecl{}, err
		}
		return BehaviorObjectExpressionDecl{
			Position: target.position, Kind: BehaviorObjectAssignment,
			Target: target.lexeme, Expression: value,
		}, nil
	}
	if start.kind == tokenIdentifier && current.peekAt(1).kind == tokenLParen {
		call, err := current.parseCall()
		if err != nil {
			return BehaviorObjectExpressionDecl{}, err
		}
		return BehaviorObjectExpressionDecl{
			Position: call.Position, Kind: BehaviorObjectFunction, Call: call,
		}, nil
	}
	value, err := current.parseExpression()
	if err != nil {
		return BehaviorObjectExpressionDecl{}, err
	}
	return BehaviorObjectExpressionDecl{
		Position: start.position, Kind: BehaviorObjectValue, Expression: value,
	}, nil
}

func (current *parser) parseBehaviorLoopStatement(label string) (BehaviorStatementDecl, error) {
	start := current.advance()
	result := BehaviorStatementDecl{Position: start.position, Kind: BehaviorLoopStatement, Label: label}
	if keyword(start.lexeme, "while") {
		condition, err := current.parseExpression()
		if err != nil {
			return BehaviorStatementDecl{}, err
		}
		result.Condition = &condition
	}
	if _, err := current.expectKeyword("do"); err != nil {
		return BehaviorStatementDecl{}, err
	}
	for !current.peekKeyword("end") {
		if current.peek().kind == tokenEOF {
			return BehaviorStatementDecl{}, current.unexpected("behavior statement or 'end do'")
		}
		statement, err := current.parseBehaviorStatement()
		if err != nil {
			return BehaviorStatementDecl{}, err
		}
		result.Body = append(result.Body, statement)
	}
	current.advance()
	if current.peekKeyword("do") || current.peekKeyword("loop") || current.peekKeyword("while") {
		current.advance()
	}
	terminator, err := current.parseBehaviorDoTerminator(label)
	if err != nil {
		return BehaviorStatementDecl{}, err
	}
	result.Terminator = terminator
	if _, err := current.expect(tokenSemicolon, "';'"); err != nil {
		return BehaviorStatementDecl{}, err
	}
	return result, nil
}

func (current *parser) parseBehaviorLoopControlStatement() (BehaviorStatementDecl, error) {
	start := current.advance()
	kind := BehaviorExitStatement
	if keyword(start.lexeme, "next") {
		kind = BehaviorNextStatement
	}
	result := BehaviorStatementDecl{Position: start.position, Kind: kind}
	if current.peek().kind == tokenIdentifier && !current.peekKeyword("where") {
		result.ControlDo = current.advance().lexeme
	}
	if current.peekKeyword("where") {
		current.advance()
		condition, err := current.parseExpression()
		if err != nil {
			return BehaviorStatementDecl{}, err
		}
		result.Condition = &condition
	}
	if _, err := current.expect(tokenSemicolon, "';'"); err != nil {
		return BehaviorStatementDecl{}, err
	}
	return result, nil
}

func (current *parser) parseBehaviorCaseStatement() (BehaviorStatementDecl, error) {
	start := current.advance()
	selector, err := current.parseExpression()
	if err != nil {
		return BehaviorStatementDecl{}, err
	}
	if _, err := current.expectKeyword("of"); err != nil {
		return BehaviorStatementDecl{}, err
	}
	result := BehaviorStatementDecl{
		Position: start.position, Kind: BehaviorCaseStatement, Expression: selector,
	}
	for {
		if current.peekKeyword("end") {
			if len(result.Cases) == 0 {
				return BehaviorStatementDecl{}, current.unexpected("case alternative")
			}
			break
		}
		if current.peekKeyword("default") && current.peekAt(1).kind == tokenPipe {
			current.advance()
			current.advance()
			body, err := current.parseBehaviorCaseBody()
			if err != nil {
				return BehaviorStatementDecl{}, err
			}
			result.Default = body
			if !current.peekKeyword("end") {
				return BehaviorStatementDecl{}, current.unexpected("'end case'")
			}
			break
		}
		alternative, err := current.parseBehaviorCaseAlternative()
		if err != nil {
			return BehaviorStatementDecl{}, err
		}
		result.Cases = append(result.Cases, alternative)
		if (current.peekKeyword("xor") || current.peekKeyword("or") || current.peekKeyword("else")) && current.peekAt(1).kind != tokenLParen {
			separator := current.advance()
			if result.CaseMode == "" {
				result.CaseMode = strings.ToLower(separator.lexeme)
			} else if !keyword(result.CaseMode, separator.lexeme) {
				return BehaviorStatementDecl{}, &SyntaxError{
					Position: separator.position,
					Message:  fmt.Sprintf("case alternatives cannot mix %q and %q separators", result.CaseMode, strings.ToLower(separator.lexeme)),
				}
			}
			continue
		}
		if !(current.peekKeyword("default") && current.peekAt(1).kind == tokenPipe) && !current.peekKeyword("end") {
			return BehaviorStatementDecl{}, current.unexpected("case separator, 'default', or 'end case'")
		}
	}
	current.advance()
	if current.peekKeyword("case") {
		current.advance()
	}
	if _, err := current.expect(tokenSemicolon, "';'"); err != nil {
		return BehaviorStatementDecl{}, err
	}
	return result, nil
}

func (current *parser) parseBehaviorCaseAlternative() (BehaviorCaseAlternativeDecl, error) {
	result := BehaviorCaseAlternativeDecl{Position: current.peek().position}
	for {
		first, err := current.parseExpression()
		if err != nil {
			return BehaviorCaseAlternativeDecl{}, err
		}
		choice := BehaviorCaseChoiceDecl{Position: first.Position, First: first}
		if current.peek().kind == tokenDot && current.peekAt(1).kind == tokenDot {
			current.advance()
			current.advance()
			last, err := current.parseExpression()
			if err != nil {
				return BehaviorCaseAlternativeDecl{}, err
			}
			choice.Last = &last
		}
		result.Choices = append(result.Choices, choice)
		if current.peek().kind != tokenComma {
			break
		}
		current.advance()
	}
	if _, err := current.expect(tokenPipe, "'=>'"); err != nil {
		return BehaviorCaseAlternativeDecl{}, err
	}
	body, err := current.parseBehaviorCaseBody()
	if err != nil {
		return BehaviorCaseAlternativeDecl{}, err
	}
	result.Body = body
	return result, nil
}

func (current *parser) parseBehaviorCaseBody() ([]BehaviorStatementDecl, error) {
	result := make([]BehaviorStatementDecl, 0)
	for !current.peekKeyword("end") &&
		!((current.peekKeyword("xor") || current.peekKeyword("or") || current.peekKeyword("else")) && current.peekAt(1).kind != tokenLParen) &&
		!(current.peekKeyword("default") && current.peekAt(1).kind == tokenPipe) {
		if current.peek().kind == tokenEOF {
			return nil, current.unexpected("case statement or 'end case'")
		}
		statement, err := current.parseBehaviorStatement()
		if err != nil {
			return nil, err
		}
		result = append(result, statement)
	}
	if len(result) == 0 {
		return nil, current.unexpected("case statement")
	}
	return result, nil
}

func (current *parser) parseBehaviorIfStatement() (BehaviorStatementDecl, error) {
	start, _ := current.expectKeyword("if")
	condition, err := current.parseExpression()
	if err != nil {
		return BehaviorStatementDecl{}, err
	}
	if _, err := current.expectKeyword("then"); err != nil {
		return BehaviorStatementDecl{}, err
	}
	result := BehaviorStatementDecl{
		Position: start.position, Kind: BehaviorIfStatement, Condition: &condition,
	}
	for !current.peekKeyword("elsif") && !current.peekKeyword("else") && !current.peekKeyword("end") && !current.peekKeyword("endif") {
		if current.peek().kind == tokenEOF {
			return BehaviorStatementDecl{}, current.unexpected("behavior statement, 'elsif', 'else', or 'end if'")
		}
		statement, err := current.parseBehaviorStatement()
		if err != nil {
			return BehaviorStatementDecl{}, err
		}
		result.Then = append(result.Then, statement)
	}
	if current.peekKeyword("elsif") {
		nested, err := current.parseBehaviorElsifTail()
		if err != nil {
			return BehaviorStatementDecl{}, err
		}
		result.Else = append(result.Else, nested)
	} else if current.peekKeyword("else") {
		current.advance()
		for !current.peekKeyword("end") && !current.peekKeyword("endif") {
			if current.peek().kind == tokenEOF {
				return BehaviorStatementDecl{}, current.unexpected("behavior statement or 'end if'")
			}
			statement, err := current.parseBehaviorStatement()
			if err != nil {
				return BehaviorStatementDecl{}, err
			}
			result.Else = append(result.Else, statement)
		}
	}
	if current.peekKeyword("endif") {
		current.advance()
	} else {
		if _, err := current.expectKeyword("end"); err != nil {
			return BehaviorStatementDecl{}, err
		}
		if current.peekKeyword("if") {
			current.advance()
		}
	}
	if _, err := current.expect(tokenSemicolon, "';'"); err != nil {
		return BehaviorStatementDecl{}, err
	}
	return result, nil
}

func (current *parser) parseBehaviorElsifTail() (BehaviorStatementDecl, error) {
	start, _ := current.expectKeyword("elsif")
	condition, err := current.parseExpression()
	if err != nil {
		return BehaviorStatementDecl{}, err
	}
	if _, err := current.expectKeyword("then"); err != nil {
		return BehaviorStatementDecl{}, err
	}
	result := BehaviorStatementDecl{
		Position: start.position, Kind: BehaviorIfStatement, Condition: &condition,
	}
	for !current.peekKeyword("elsif") && !current.peekKeyword("else") && !current.peekKeyword("end") && !current.peekKeyword("endif") {
		if current.peek().kind == tokenEOF {
			return BehaviorStatementDecl{}, current.unexpected("behavior statement, 'elsif', 'else', or 'end if'")
		}
		statement, err := current.parseBehaviorStatement()
		if err != nil {
			return BehaviorStatementDecl{}, err
		}
		result.Then = append(result.Then, statement)
	}
	if current.peekKeyword("elsif") {
		nested, err := current.parseBehaviorElsifTail()
		if err != nil {
			return BehaviorStatementDecl{}, err
		}
		result.Else = append(result.Else, nested)
	} else if current.peekKeyword("else") {
		current.advance()
		for !current.peekKeyword("end") && !current.peekKeyword("endif") {
			if current.peek().kind == tokenEOF {
				return BehaviorStatementDecl{}, current.unexpected("behavior statement or 'end if'")
			}
			statement, err := current.parseBehaviorStatement()
			if err != nil {
				return BehaviorStatementDecl{}, err
			}
			result.Else = append(result.Else, statement)
		}
	}
	return result, nil
}

func (current *parser) parseCallStatement() (CallStatementDecl, error) {
	result, err := current.parseCall()
	if err != nil {
		return CallStatementDecl{}, err
	}
	if current.peekKeyword("in") || current.peekKeyword("pause") || current.peekKeyword("delay") {
		operator := current.advance()
		kind := TimingKind(strings.ToLower(operator.lexeme))
		timing, err := current.parseTimingExpression(operator.position, kind)
		if err != nil {
			return CallStatementDecl{}, err
		}
		result.Timing = &timing
	}
	if _, err := current.expect(tokenSemicolon, "';'"); err != nil {
		return CallStatementDecl{}, err
	}
	return result, nil
}

func (current *parser) parseTimingExpression(position Position, kind TimingKind) (TimingDecl, error) {
	clock, err := current.expectIdentifier("timing clock name")
	if err != nil {
		return TimingDecl{}, err
	}
	if current.peek().kind != tokenDot {
		return TimingDecl{Position: position, Kind: kind, Name: clock.lexeme}, nil
	}
	if _, err := current.expect(tokenDot, "'.'"); err != nil {
		return TimingDecl{}, err
	}
	if _, err := current.expectKeyword("Ticks"); err != nil {
		return TimingDecl{}, err
	}
	if current.peek().kind == tokenLParen {
		current.advance()
		if current.peek().kind == tokenInteger && current.peekAt(1).kind == tokenRParen {
			value := current.advance()
			current.advance()
			tick, err := strconv.ParseUint(value.lexeme, 10, 64)
			if err != nil {
				return TimingDecl{}, &SyntaxError{Position: value.position, Message: "fixed timing tick is outside C.Ticks"}
			}
			return TimingDecl{Position: position, Kind: kind, Clock: clock.lexeme, First: tick, Last: tick}, nil
		}
		value, err := current.parseExpression()
		if err != nil {
			return TimingDecl{}, err
		}
		if _, err := current.expect(tokenRParen, "')'"); err != nil {
			return TimingDecl{}, err
		}
		return TimingDecl{Position: position, Kind: kind, Clock: clock.lexeme, Value: &value}, nil
	}
	if _, err := current.expectKeyword("range"); err != nil {
		return TimingDecl{}, &SyntaxError{
			Position: current.peek().position,
			Message:  "named and general subtype timing expressions are outside the current source subset; expected '(tick)' or 'range'",
		}
	}
	result := TimingDecl{Position: position, Kind: kind, Clock: clock.lexeme}
	if current.peek().kind == tokenInteger && current.peekAt(1).kind == tokenDot && current.peekAt(2).kind == tokenDot {
		first := current.advance()
		firstValue, err := strconv.ParseUint(first.lexeme, 10, 64)
		if err != nil {
			return TimingDecl{}, &SyntaxError{Position: first.position, Message: "timing range lower bound is outside C.Ticks"}
		}
		result.First = firstValue
	} else {
		first, err := current.parseExpression()
		if err != nil {
			return TimingDecl{}, err
		}
		result.RangeFirst = &first
	}
	if _, err := current.expect(tokenDot, "first '.' in timing range '..'"); err != nil {
		return TimingDecl{}, err
	}
	if _, err := current.expect(tokenDot, "second '.' in timing range '..'"); err != nil {
		return TimingDecl{}, err
	}
	if current.peek().kind == tokenInteger && current.peekAt(1).kind == tokenSemicolon {
		last := current.advance()
		lastValue, err := strconv.ParseUint(last.lexeme, 10, 64)
		if err != nil {
			return TimingDecl{}, &SyntaxError{Position: last.position, Message: "timing range upper bound is outside C.Ticks"}
		}
		result.Last = lastValue
	} else {
		last, err := current.parseExpression()
		if err != nil {
			return TimingDecl{}, err
		}
		result.RangeLast = &last
	}
	return result, nil
}

func (current *parser) parseCall() (CallStatementDecl, error) {
	name, err := current.expectIdentifier("action or function call")
	if err != nil {
		return CallStatementDecl{}, err
	}
	return current.parseCallArguments(CallStatementDecl{Position: name.position, Name: name.lexeme})
}

func (current *parser) parseCallArguments(result CallStatementDecl) (CallStatementDecl, error) {
	if _, err := current.expect(tokenLParen, "'('"); err != nil {
		return CallStatementDecl{}, err
	}
	if current.peek().kind != tokenRParen {
		named := false
		seenNamed := make(map[string]bool)
		for {
			formal := ""
			if current.peek().kind == tokenIdentifier && current.peekAt(1).kind == tokenIdentifier &&
				keyword(current.peekAt(1).lexeme, "is") {
				name := current.advance()
				current.advance()
				formal = name.lexeme
				named = true
				key := folded(formal)
				if seenNamed[key] {
					return CallStatementDecl{}, &SyntaxError{
						Position: name.position,
						Message:  fmt.Sprintf("duplicate named call association %q", formal),
					}
				}
				seenNamed[key] = true
			} else if named {
				return CallStatementDecl{}, &SyntaxError{
					Position: current.peek().position,
					Message:  "positional call arguments must precede named associations",
				}
			}
			argument, err := current.parseExpression()
			if err != nil {
				return CallStatementDecl{}, err
			}
			result.Arguments = append(result.Arguments, argument)
			result.ArgumentFormals = append(result.ArgumentFormals, formal)
			if current.peek().kind != tokenComma {
				break
			}
			current.advance()
		}
	}
	if _, err := current.expect(tokenRParen, "')'"); err != nil {
		return CallStatementDecl{}, err
	}
	return result, nil
}

func (current *parser) parseExpression() (ExpressionDecl, error) {
	return current.parseLogicalExpression()
}

func (current *parser) parseLogicalExpression() (ExpressionDecl, error) {
	left, err := current.parseComparisonExpression()
	if err != nil {
		return ExpressionDecl{}, err
	}
	for current.peekKeyword("nand") || current.peekKeyword("nor") ||
		current.peekKeyword("and") || current.peekKeyword("andthen") ||
		current.peekKeyword("or") || current.peekKeyword("orelse") || current.peekKeyword("xor") {
		operator := current.advance()
		right, err := current.parseComparisonExpression()
		if err != nil {
			return ExpressionDecl{}, err
		}
		leftCopy, rightCopy := left, right
		left = ExpressionDecl{Position: operator.position, Kind: ExpressionBinary, Operator: strings.ToLower(operator.lexeme), Left: &leftCopy, Right: &rightCopy}
	}
	return left, nil
}

func (current *parser) parseComparisonExpression() (ExpressionDecl, error) {
	left, err := current.parseAdditiveExpression()
	if err != nil {
		return ExpressionDecl{}, err
	}
	for isComparisonToken(current.peek().kind) {
		operator := current.advance()
		right, err := current.parseAdditiveExpression()
		if err != nil {
			return ExpressionDecl{}, err
		}
		leftCopy, rightCopy := left, right
		left = ExpressionDecl{Position: operator.position, Kind: ExpressionBinary, Operator: operator.lexeme, Left: &leftCopy, Right: &rightCopy}
	}
	return left, nil
}

func (current *parser) parseAdditiveExpression() (ExpressionDecl, error) {
	left, err := current.parseMultiplicativeExpression()
	if err != nil {
		return ExpressionDecl{}, err
	}
	for current.peek().kind == tokenPlus || current.peek().kind == tokenMinus || current.peek().kind == tokenAmpersand {
		operator := current.advance()
		right, err := current.parseMultiplicativeExpression()
		if err != nil {
			return ExpressionDecl{}, err
		}
		leftCopy, rightCopy := left, right
		left = ExpressionDecl{
			Position: operator.position, Kind: ExpressionBinary, Operator: operator.lexeme,
			Left: &leftCopy, Right: &rightCopy,
		}
	}
	return left, nil
}

func (current *parser) parseMultiplicativeExpression() (ExpressionDecl, error) {
	left, err := current.parseUnaryExpression()
	if err != nil {
		return ExpressionDecl{}, err
	}
	for current.peek().kind == tokenStar || current.peek().kind == tokenSlash {
		operator := current.advance()
		right, err := current.parseUnaryExpression()
		if err != nil {
			return ExpressionDecl{}, err
		}
		leftCopy, rightCopy := left, right
		left = ExpressionDecl{
			Position: operator.position, Kind: ExpressionBinary, Operator: operator.lexeme,
			Left: &leftCopy, Right: &rightCopy,
		}
	}
	return left, nil
}

func (current *parser) parseUnaryExpression() (ExpressionDecl, error) {
	if current.peek().kind == tokenPlus || current.peek().kind == tokenMinus || current.peekKeyword("not") || current.peekKeyword("abs") {
		operator := current.advance()
		operand, err := current.parseUnaryExpression()
		if err != nil {
			return ExpressionDecl{}, err
		}
		operandCopy := operand
		return ExpressionDecl{
			Position: operator.position, Kind: ExpressionUnary,
			Operator: strings.ToLower(operator.lexeme), Left: &operandCopy,
		}, nil
	}
	return current.parsePrimaryExpression()
}

func isComparisonToken(kind tokenKind) bool {
	switch kind {
	case tokenEqual, tokenNotEqual, tokenLess, tokenLessOrEqual, tokenGreater, tokenGreaterOrEqual:
		return true
	default:
		return false
	}
}

func (current *parser) parsePrimaryExpression() (ExpressionDecl, error) {
	result, err := current.parsePrimaryExpressionAtom()
	if err != nil {
		return ExpressionDecl{}, err
	}
	for {
		if current.peek().kind == tokenDot && current.peekAt(1).kind == tokenIdentifier && current.peekAt(2).kind == tokenLParen {
			current.advance()
			member := current.advance()
			arguments, formals, err := current.parseExpressionCallArguments()
			if err != nil {
				return ExpressionDecl{}, err
			}
			receiver := result
			result = ExpressionDecl{
				Position: member.position, Kind: ExpressionCall, Name: member.lexeme,
				Left: &receiver, Arguments: arguments, ArgumentFormals: formals,
			}
			continue
		}
		if current.peek().kind == tokenDot && current.peekAt(1).kind == tokenLBracket &&
			current.peekAt(2).kind == tokenRBracket && current.peekAt(3).kind == tokenLParen {
			current.advance()
			member := current.advance()
			current.advance()
			arguments, formals, err := current.parseExpressionCallArguments()
			if err != nil {
				return ExpressionDecl{}, err
			}
			receiver := result
			result = ExpressionDecl{
				Position: member.position, Kind: ExpressionCall, Name: "[]",
				Left: &receiver, Arguments: arguments, ArgumentFormals: formals,
			}
			continue
		}
		if current.peek().kind == tokenDot &&
			(current.peekAt(1).kind == tokenPlus || current.peekAt(1).kind == tokenMinus || current.peekAt(1).kind == tokenStar) &&
			current.peekAt(2).kind == tokenLParen {
			current.advance()
			member := current.advance()
			arguments, formals, err := current.parseExpressionCallArguments()
			if err != nil {
				return ExpressionDecl{}, err
			}
			receiver := result
			result = ExpressionDecl{
				Position: member.position, Kind: ExpressionCall, Name: member.lexeme,
				Left: &receiver, Arguments: arguments, ArgumentFormals: formals,
			}
			continue
		}
		if current.peek().kind == tokenDot &&
			(current.peekAt(1).kind == tokenEqual || current.peekAt(1).kind == tokenNotEqual) &&
			current.peekAt(2).kind == tokenLParen {
			current.advance()
			member := current.advance()
			arguments, formals, err := current.parseExpressionCallArguments()
			if err != nil {
				return ExpressionDecl{}, err
			}
			receiver := result
			result = ExpressionDecl{
				Position: member.position, Kind: ExpressionCall, Name: member.lexeme,
				Left: &receiver, Arguments: arguments, ArgumentFormals: formals,
			}
			continue
		}
		if current.peek().kind == tokenDot &&
			(current.peekAt(1).kind == tokenLess || current.peekAt(1).kind == tokenLessOrEqual ||
				current.peekAt(1).kind == tokenGreater || current.peekAt(1).kind == tokenGreaterOrEqual) &&
			current.peekAt(2).kind == tokenLParen {
			current.advance()
			member := current.advance()
			arguments, formals, err := current.parseExpressionCallArguments()
			if err != nil {
				return ExpressionDecl{}, err
			}
			receiver := result
			result = ExpressionDecl{
				Position: member.position, Kind: ExpressionCall, Name: member.lexeme,
				Left: &receiver, Arguments: arguments, ArgumentFormals: formals,
			}
			continue
		}
		if current.peek().kind == tokenLBracket {
			member := current.advance()
			first, err := current.parseExpression()
			if err != nil {
				return ExpressionDecl{}, err
			}
			name := "[]"
			arguments := []ExpressionDecl{first}
			if current.peek().kind == tokenDot && current.peekAt(1).kind == tokenDot {
				current.advance()
				current.advance()
				last, err := current.parseExpression()
				if err != nil {
					return ExpressionDecl{}, err
				}
				name = "Slice"
				arguments = append(arguments, last)
			}
			if _, err := current.expect(tokenRBracket, "']'"); err != nil {
				return ExpressionDecl{}, err
			}
			receiver := result
			result = ExpressionDecl{
				Position: member.position, Kind: ExpressionCall, Name: name,
				Left: &receiver, Arguments: arguments,
			}
			continue
		}
		if current.peek().kind == tokenApostrophe {
			if current.peekAt(1).kind == tokenIdentifier {
				return ExpressionDecl{}, &SyntaxError{
					Position: current.peek().position,
					Message:  "attributes are outside the current source subset",
				}
			}
			return ExpressionDecl{}, &SyntaxError{
				Position: current.peek().position,
				Message:  "qualified expressions over general type expressions are outside the direct named-type subset",
			}
		}
		if current.peek().kind == tokenDot && current.peekAt(1).kind == tokenIdentifier {
			current.advance()
			member := current.advance()
			receiver := result
			result = ExpressionDecl{
				Position: member.position, Kind: ExpressionSelection, Name: member.lexeme,
				Left: &receiver,
			}
			continue
		}
		break
	}
	return result, nil
}

func (current *parser) parsePrimaryExpressionAtom() (ExpressionDecl, error) {
	token := current.peek()
	switch token.kind {
	case tokenInteger:
		current.advance()
		value, err := strconv.ParseInt(token.lexeme, 10, 64)
		if err != nil {
			return ExpressionDecl{}, &SyntaxError{Position: token.position, Message: "integer literal is outside the signed 64-bit deterministic subset"}
		}
		return ExpressionDecl{Position: token.position, Kind: ExpressionInteger, Integer: value}, nil
	case tokenFloat:
		current.advance()
		value, err := strconv.ParseFloat(token.lexeme, 64)
		if err != nil {
			return ExpressionDecl{}, &SyntaxError{Position: token.position, Message: "floating-point literal is outside the finite IEEE-754 binary64 deterministic subset"}
		}
		return ExpressionDecl{Position: token.position, Kind: ExpressionFloat, Float: value}, nil
	case tokenCharacter:
		current.advance()
		value, err := parseCharacterLiteralCode(token.lexeme)
		if err != nil {
			return ExpressionDecl{}, &SyntaxError{Position: token.position, Message: err.Error()}
		}
		return ExpressionDecl{Position: token.position, Kind: ExpressionCharacter, Character: value}, nil
	case tokenString:
		current.advance()
		codes, decoded, err := parseStringLiteralCodes(token.lexeme)
		if err != nil {
			return ExpressionDecl{}, &SyntaxError{Position: token.position, Message: err.Error()}
		}
		return ExpressionDecl{Position: token.position, Kind: ExpressionString, String: decoded, StringCodes: codes}, nil
	case tokenIdentifier:
		if keyword(token.lexeme, "if") {
			return current.parseIfExpression()
		}
		if keyword(token.lexeme, "case") && current.peekAt(1).kind == tokenLParen {
			return ExpressionDecl{}, &SyntaxError{
				Position: token.position,
				Message:  "Union discrimination requires first-class tagged values outside the current expression subset",
			}
		}
		if (keyword(token.lexeme, "tagof") || keyword(token.lexeme, "tags")) && current.peekAt(1).kind == tokenLParen {
			return ExpressionDecl{}, &SyntaxError{
				Position: token.position,
				Message:  "Union tag extraction requires first-class tagged values and Enumeration types outside the current expression subset",
			}
		}
		if keyword(token.lexeme, "empty_set") && current.peekAt(1).kind == tokenLParen {
			return ExpressionDecl{}, &SyntaxError{
				Position: token.position,
				Message:  "Empty_Set requires first-class Set values outside the current expression subset",
			}
		}
		if current.peekAt(1).kind == tokenApostrophe && current.peekAt(2).kind == tokenLParen {
			current.advance()
			current.advance()
			if _, err := current.expect(tokenLParen, "'('"); err != nil {
				return ExpressionDecl{}, err
			}
			value, err := current.parseExpression()
			if err != nil {
				return ExpressionDecl{}, err
			}
			if _, err := current.expect(tokenRParen, "')'"); err != nil {
				return ExpressionDecl{}, err
			}
			valueCopy := value
			return ExpressionDecl{
				Position: token.position, Kind: ExpressionQualified, Name: token.lexeme, Left: &valueCopy,
			}, nil
		}
		current.advance()
		if keyword(token.lexeme, "unit") {
			return ExpressionDecl{Position: token.position, Kind: ExpressionUnit}, nil
		}
		if keyword(token.lexeme, "true") || keyword(token.lexeme, "false") {
			return ExpressionDecl{Position: token.position, Kind: ExpressionBoolean, Boolean: keyword(token.lexeme, "true")}, nil
		}
		if current.peek().kind == tokenLParen {
			arguments, formals, err := current.parseExpressionCallArguments()
			if err != nil {
				return ExpressionDecl{}, err
			}
			return ExpressionDecl{
				Position: token.position, Kind: ExpressionCall, Name: token.lexeme,
				Arguments: arguments, ArgumentFormals: formals,
			}, nil
		}
		return ExpressionDecl{Position: token.position, Kind: ExpressionName, Name: token.lexeme}, nil
	case tokenQuestion:
		current.advance()
		name, err := current.expectIdentifier("placeholder name")
		if err != nil {
			return ExpressionDecl{}, err
		}
		return ExpressionDecl{Position: token.position, Kind: ExpressionPlaceholder, Name: name.lexeme}, nil
	case tokenBang:
		current.advance()
		name, err := current.expectIdentifier("universal placeholder name")
		if err != nil {
			return ExpressionDecl{}, err
		}
		return ExpressionDecl{Position: token.position, Kind: ExpressionUniversal, Name: name.lexeme}, nil
	case tokenDollar:
		current.advance()
		name, err := current.expectIdentifier("state name")
		if err != nil {
			return ExpressionDecl{}, err
		}
		return ExpressionDecl{Position: token.position, Kind: ExpressionState, Name: name.lexeme}, nil
	case tokenLParen:
		if current.peekAt(1).kind == tokenIdentifier && current.peekAt(2).kind == tokenComma {
			return ExpressionDecl{}, &SyntaxError{
				Position: token.position,
				Message:  "Union literals require contextual tagged-value typing outside the current expression subset",
			}
		}
		if current.peekAt(1).kind == tokenIdentifier && current.peekAtKeyword(2, "is") {
			return current.parseRecordLiteral()
		}
		current.advance()
		value, err := current.parseExpression()
		if err != nil {
			return ExpressionDecl{}, err
		}
		if _, err := current.expect(tokenRParen, "')'"); err != nil {
			return ExpressionDecl{}, err
		}
		return value, nil
	default:
		return ExpressionDecl{}, current.unexpected("closed behavior expression")
	}
}

func (current *parser) parseRecordLiteral() (ExpressionDecl, error) {
	start := current.advance()
	fields := make([]RecordFieldExpressionDecl, 0)
	for {
		name, err := current.expectIdentifier("Record field name")
		if err != nil {
			return ExpressionDecl{}, err
		}
		if _, err := current.expectKeyword("is"); err != nil {
			return ExpressionDecl{}, err
		}
		value, err := current.parseExpression()
		if err != nil {
			return ExpressionDecl{}, err
		}
		fields = append(fields, RecordFieldExpressionDecl{
			Position: name.position, Name: name.lexeme, Value: value,
		})
		if current.peek().kind != tokenComma {
			break
		}
		current.advance()
	}
	if _, err := current.expect(tokenRParen, "')' after Record literal"); err != nil {
		return ExpressionDecl{}, err
	}
	return ExpressionDecl{Position: start.position, Kind: ExpressionRecord, RecordFields: fields}, nil
}

func (current *parser) parseIfExpression() (ExpressionDecl, error) {
	start := current.advance()
	condition, err := current.parseExpression()
	if err != nil {
		return ExpressionDecl{}, err
	}
	if _, err := current.expectKeyword("then"); err != nil {
		return ExpressionDecl{}, err
	}
	thenExpression, err := current.parseExpression()
	if err != nil {
		return ExpressionDecl{}, err
	}
	if _, err := current.expectKeyword("else"); err != nil {
		return ExpressionDecl{}, err
	}
	elseExpression, err := current.parseExpression()
	if err != nil {
		return ExpressionDecl{}, err
	}
	if _, err := current.expectKeyword("end"); err != nil {
		return ExpressionDecl{}, err
	}
	if _, err := current.expectKeyword("if"); err != nil {
		return ExpressionDecl{}, err
	}
	conditionCopy, thenCopy := condition, thenExpression
	return ExpressionDecl{
		Position: start.position, Kind: ExpressionConditional,
		Left: &conditionCopy, Right: &thenCopy, Arguments: []ExpressionDecl{elseExpression},
	}, nil
}

func (current *parser) parseExpressionCallArguments() ([]ExpressionDecl, []string, error) {
	if _, err := current.expect(tokenLParen, "'('"); err != nil {
		return nil, nil, err
	}
	arguments := make([]ExpressionDecl, 0)
	formals := make([]string, 0)
	if current.peek().kind != tokenRParen {
		named := false
		for {
			formal := ""
			if current.peek().kind == tokenIdentifier && current.peekAtKeyword(1, "is") {
				formal = current.advance().lexeme
				current.advance()
				named = true
			} else if named {
				return nil, nil, &SyntaxError{
					Position: current.peek().position,
					Message:  "positional expression-call arguments must precede named associations",
				}
			}
			argument, err := current.parseExpression()
			if err != nil {
				return nil, nil, err
			}
			arguments = append(arguments, argument)
			formals = append(formals, formal)
			if current.peek().kind != tokenComma {
				break
			}
			current.advance()
		}
	}
	if _, err := current.expect(tokenRParen, "')'"); err != nil {
		return nil, nil, err
	}
	return arguments, formals, nil
}

func parseCharacterLiteralCode(lexeme string) (int64, error) {
	if len(lexeme) == 1 && isCharacterLiteralLetter(lexeme[0]) {
		return int64(lexeme[0]), nil
	}
	if len(lexeme) < 2 || lexeme[0] != '\\' {
		return 0, fmt.Errorf("invalid Character literal")
	}
	switch lexeme {
	case `\n`:
		return int64('\n'), nil
	case `\\`:
		return int64('\\'), nil
	case `\'`:
		return int64('\''), nil
	}
	value, err := strconv.ParseInt(lexeme[1:], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("Character code is outside the signed 64-bit deterministic Integer subset")
	}
	return value, nil
}

func parseStringLiteralCodes(lexeme string) ([]int64, string, error) {
	codes := make([]int64, 0, len(lexeme))
	for index := 0; index < len(lexeme); {
		if lexeme[index] != '\\' {
			codes = append(codes, int64(lexeme[index]))
			index++
			continue
		}
		index++
		if index >= len(lexeme) {
			return nil, "", fmt.Errorf("unterminated String escape")
		}
		switch lexeme[index] {
		case 'n':
			codes = append(codes, int64('\n'))
			index++
		case '\\':
			codes = append(codes, int64('\\'))
			index++
		case '\'':
			codes = append(codes, int64('\''))
			index++
		default:
			start := index
			for index < len(lexeme) && lexeme[index] >= '0' && lexeme[index] <= '9' {
				index++
			}
			value, err := strconv.ParseInt(lexeme[start:index], 10, 64)
			if err != nil {
				return nil, "", fmt.Errorf("String Character code is outside the signed 64-bit deterministic Integer subset")
			}
			codes = append(codes, value)
		}
	}
	runes := make([]rune, len(codes))
	for index, code := range codes {
		if code < 0 || code > utf8.MaxRune || code >= 0xd800 && code <= 0xdfff {
			return codes, "", nil
		}
		runes[index] = rune(code)
	}
	return codes, string(runes), nil
}

func (current *parser) interfacePartBoundary() bool {
	return current.peekKeyword("provides") || current.peekKeyword("requires") ||
		current.peekKeyword("action") || current.peekKeyword("private") ||
		current.peekKeyword("service") || current.peekKeyword("exception") || current.peekKeyword("constraint") ||
		current.peekKeyword("behavior") || current.peekKeyword("include") || current.peekKeyword("end")
}

func (current *parser) parseAction() (ActionDecl, error) {
	start, _ := current.expectKeyword("action")
	modeToken, err := current.expectIdentifier("action mode 'in' or 'out'")
	if err != nil {
		return ActionDecl{}, err
	}
	var mode ActionMode
	switch {
	case keyword(modeToken.lexeme, "in"):
		mode = ActionIn
	case keyword(modeToken.lexeme, "out"):
		mode = ActionOut
	default:
		return ActionDecl{}, &SyntaxError{Position: modeToken.position, Message: "expected action mode 'in' or 'out'"}
	}
	return current.parseActionSignature(start.position, mode)
}

func (current *parser) parsePrivateActionPart() ([]ActionDecl, error) {
	start, _ := current.expectKeyword("private")
	if _, err := current.expectKeyword("action"); err != nil {
		return nil, &SyntaxError{
			Position: start.position,
			Message:  "the current deterministic private-part subset requires an 'action' declarative part",
		}
	}
	if current.interfacePartBoundary() {
		return nil, current.unexpected("private action declaration")
	}
	result := make([]ActionDecl, 0)
	for !current.interfacePartBoundary() {
		if current.peek().kind != tokenIdentifier {
			return nil, current.unexpected("private action declaration or interface constituent")
		}
		action, err := current.parseActionSignature(current.peek().position, ActionPrivate)
		if err != nil {
			return nil, err
		}
		result = append(result, action)
	}
	return result, nil
}

func (current *parser) parseActionSignature(position Position, mode ActionMode) (ActionDecl, error) {
	name, err := current.expectIdentifier("action name")
	if err != nil {
		return ActionDecl{}, err
	}
	if _, err := current.expect(tokenLParen, "'('"); err != nil {
		return ActionDecl{}, err
	}
	result := ActionDecl{Position: position, Mode: mode, Name: name.lexeme}
	if current.peek().kind != tokenRParen {
		for {
			parameterNames, err := current.parseFormalIdentifierList("parameter name")
			if err != nil {
				return ActionDecl{}, err
			}
			if _, err := current.expect(tokenColon, "':'"); err != nil {
				return ActionDecl{}, err
			}
			parameterType, err := current.parseClosedTypeExpression("action parameter type")
			if err != nil {
				return ActionDecl{}, err
			}
			for _, parameterName := range parameterNames {
				result.Parameters = append(result.Parameters, ParameterDecl{
					Position: parameterName.position, Name: parameterName.lexeme,
					Type: typeExpressionSpelling(parameterType), TypeExpression: parameterType,
				})
			}
			if current.peek().kind != tokenComma && current.peek().kind != tokenSemicolon {
				break
			}
			current.advance()
		}
	}
	if _, err := current.expect(tokenRParen, "')'"); err != nil {
		return ActionDecl{}, err
	}
	if _, err := current.expect(tokenSemicolon, "';'"); err != nil {
		return ActionDecl{}, err
	}
	return result, nil
}

func (current *parser) parseModule() (ModuleDecl, error) {
	start, _ := current.expectKeyword("module")
	name, err := current.expectIdentifier("module generator name")
	if err != nil {
		return ModuleDecl{}, err
	}
	if _, err := current.expect(tokenLParen, "'('"); err != nil {
		return ModuleDecl{}, err
	}
	parameters := make([]ParameterDecl, 0)
	if current.peek().kind != tokenRParen {
		for {
			if current.peekKeyword("type") {
				return ModuleDecl{}, &SyntaxError{
					Position: current.peek().position,
					Message:  "module-generator type formals are outside the current predefined-scalar object-formal subset",
				}
			}
			parameterNames, err := current.parseFormalIdentifierList("module generator parameter name")
			if err != nil {
				return ModuleDecl{}, err
			}
			if _, err := current.expect(tokenColon, "':'"); err != nil {
				return ModuleDecl{}, err
			}
			parameterType, err := current.parseClosedTypeExpression("module generator parameter type")
			if err != nil {
				return ModuleDecl{}, err
			}
			var defaultExpression *ExpressionDecl
			if current.peekKeyword("is") {
				current.advance()
				value, err := current.parseExpression()
				if err != nil {
					return ModuleDecl{}, err
				}
				defaultExpression = &value
			}
			for _, parameterName := range parameterNames {
				parameters = append(parameters, ParameterDecl{
					Position: parameterName.position, Name: parameterName.lexeme,
					Type: typeExpressionSpelling(parameterType), TypeExpression: parameterType,
					Default: cloneExpressionDeclarationPointer(defaultExpression),
				})
			}
			if current.peek().kind != tokenComma && current.peek().kind != tokenSemicolon {
				break
			}
			current.advance()
		}
	}
	if _, err := current.expect(tokenRParen, "')'"); err != nil {
		return ModuleDecl{}, err
	}
	if _, err := current.expectKeyword("return"); err != nil {
		return ModuleDecl{}, err
	}
	returnType, err := current.expectIdentifier("module return interface type")
	if err != nil {
		return ModuleDecl{}, err
	}
	if _, err := current.expectKeyword("is"); err != nil {
		return ModuleDecl{}, err
	}
	result := ModuleDecl{
		Position: start.position, Name: name.lexeme, Parameters: parameters, ReturnType: returnType.lexeme,
	}
	for !current.peekKeyword("constraint") && !current.peekKeyword("connect") &&
		!current.peekKeyword("initial") && !current.peekKeyword("parallel") && !current.peekKeyword("serial") &&
		!current.peekKeyword("handler") && !current.peekKeyword("final") && !current.peekKeyword("end") {
		if current.peekKeyword("exception") {
			exception, err := current.parseModuleExceptionDeclaration()
			if err != nil {
				return ModuleDecl{}, err
			}
			exception.Declaration = moduleExceptionDeclarationIdentity(result.Name, exception.Name)
			result.Exceptions = append(result.Exceptions, exception)
			continue
		}
		if current.peekKeyword("type") {
			if !current.startsClosedTypeAlias() {
				return ModuleDecl{}, &SyntaxError{
					Position: current.peek().position,
					Message:  "the current module type-denotation subset requires 'type Name is Named_Type;'",
				}
			}
			denotation, err := current.parseClosedTypeAlias()
			if err != nil {
				return ModuleDecl{}, err
			}
			if denotation.IntegerRange {
				return ModuleDecl{}, &SyntaxError{
					Position: denotation.Position,
					Message:  "module-local finite range declarations are outside the current named component-array/generator subset; declare the range at top level",
				}
			}
			result.Types = append(result.Types, denotation)
			continue
		}
		if current.peekAt(2).kind == tokenIdentifier && keyword(current.peekAt(2).lexeme, "var") {
			state, err := current.parseBehaviorState()
			if err != nil {
				return ModuleDecl{}, err
			}
			result.States = append(result.States, state)
			continue
		}
		if current.peekAt(2).kind == tokenIdentifier && keyword(current.peekAt(2).lexeme, "function") {
			function, err := current.parseBehaviorFunction()
			if err != nil {
				return ModuleDecl{}, err
			}
			result.Functions = append(result.Functions, function)
			continue
		}
		if current.startsModuleTimingObjectDeclaration() {
			object, err := current.parseModuleTimingObjectDeclaration()
			if err != nil {
				return ModuleDecl{}, err
			}
			result.TimingObjects = append(result.TimingObjects, object)
			continue
		}
		if !current.startsBasicClockDeclaration() {
			objects, err := current.parseModuleObjectDeclaration()
			if err != nil {
				return ModuleDecl{}, err
			}
			result.Objects = append(result.Objects, objects...)
			continue
		}
		clock, err := current.parseBasicClockDeclaration()
		if err != nil {
			return ModuleDecl{}, err
		}
		result.Clocks = append(result.Clocks, clock)
	}
	if current.peekKeyword("constraint") {
		start := current.advance()
		if !current.startsPatternConstraintGroup() {
			return ModuleDecl{}, current.unexpected("module 'match', 'never', or 'observe' constraint")
		}
		for current.startsPatternConstraintGroup() {
			declarations, err := current.parsePatternConstraintGroup()
			if err != nil {
				return ModuleDecl{}, err
			}
			result.Constraints = append(result.Constraints, declarations...)
		}
		if len(result.Constraints) == 0 {
			return ModuleDecl{}, &SyntaxError{
				Position: start.position, Message: "module constraint part requires a constraint",
			}
		}
	}
	if current.peekKeyword("connect") {
		start := current.advance()
		for !current.peekKeyword("initial") && !current.peekKeyword("parallel") &&
			!current.peekKeyword("serial") && !current.peekKeyword("handler") &&
			!current.peekKeyword("final") && !current.peekKeyword("end") {
			if current.startsConnectionGenerator() {
				generator, err := current.parseConnectionGenerator(current.parseModuleActionRef)
				if err != nil {
					return ModuleDecl{}, err
				}
				result.ConnectionGenerators = append(result.ConnectionGenerators, generator)
				continue
			}
			connections, err := current.parseModuleConnection()
			if err != nil {
				return ModuleDecl{}, err
			}
			result.Connections = append(result.Connections, connections...)
		}
		if len(result.Connections) == 0 && len(result.ConnectionGenerators) == 0 {
			return ModuleDecl{}, &SyntaxError{Position: start.position, Message: "module connect part requires a connection"}
		}
	}
	if current.peekKeyword("initial") {
		current.advance()
		if current.peek().kind == tokenLParen {
			parameters, err := current.parseModuleInitializationParameters()
			if err != nil {
				return ModuleDecl{}, err
			}
			result.InitialParameters = parameters
		}
		for !current.peekKeyword("parallel") && !current.peekKeyword("serial") &&
			!current.peekKeyword("handler") && !current.peekKeyword("final") && !current.peekKeyword("end") {
			statement, err := current.parseBehaviorStatement()
			if err != nil {
				return ModuleDecl{}, err
			}
			result.Initial = append(result.Initial, statement)
		}
		if len(result.Initial) == 0 {
			return ModuleDecl{}, &SyntaxError{
				Position: current.peek().position,
				Message:  "module initial part requires at least one statement",
			}
		}
	}
	if current.peekKeyword("parallel") || current.peekKeyword("serial") {
		mode := current.advance()
		result.Mode = strings.ToLower(mode.lexeme)
		for {
			var process ModuleProcessDecl
			var err error
			whenLabel := ""
			var whenOuterExceptions []ExceptionDecl
			if current.peek().kind == tokenIdentifier && current.peekAt(1).kind == tokenColon &&
				(current.peekAtKeyword(2, "when") || current.labeledDeclareIntroducesWhen()) {
				whenLabel = current.advance().lexeme
				current.advance()
				if current.peekKeyword("declare") {
					whenOuterExceptions, err = current.parseModuleWhenOuterExceptionDeclarations()
					if err != nil {
						return ModuleDecl{}, err
					}
				}
			} else if current.peekKeyword("declare") && current.declareIntroducesWhen(0) {
				whenOuterExceptions, err = current.parseModuleWhenOuterExceptionDeclarations()
				if err != nil {
					return ModuleDecl{}, err
				}
			}
			if current.peekKeyword("await") {
				process, err = current.parseModuleAwaitProcess()
			} else if current.peekKeyword("when") {
				process, err = current.parseModuleWhenProcess(whenLabel, whenOuterExceptions)
			} else {
				process, err = current.parseModuleEntryProcess()
			}
			if err != nil {
				return ModuleDecl{}, err
			}
			result.Processes = append(result.Processes, process)
			if current.peek().kind != tokenIndependent {
				break
			}
			current.advance()
		}
	}
	if current.peekKeyword("handler") {
		handler, err := current.parseBehaviorHandlerUntil(true)
		if err != nil {
			return ModuleDecl{}, err
		}
		result.Handler = &handler
	}
	if current.peekKeyword("final") {
		current.advance()
		for !current.peekKeyword("end") {
			if current.peek().kind == tokenEOF {
				return ModuleDecl{}, current.unexpected("module final statement or module end")
			}
			statement, err := current.parseBehaviorStatement()
			if err != nil {
				return ModuleDecl{}, err
			}
			result.Final = append(result.Final, statement)
		}
		if len(result.Final) == 0 {
			return ModuleDecl{}, &SyntaxError{
				Position: current.peek().position,
				Message:  "module final part requires at least one statement",
			}
		}
	}
	if _, err := current.expectKeyword("end"); err != nil {
		return ModuleDecl{}, err
	}
	if current.peekKeyword("module") {
		current.advance()
	}
	if current.peek().kind == tokenIdentifier && keyword(current.peek().lexeme, result.Name) {
		current.advance()
	}
	if _, err := current.expect(tokenSemicolon, "';'"); err != nil {
		return ModuleDecl{}, err
	}
	return result, nil
}

func (current *parser) parseModuleInitializationParameters() ([]ParameterDecl, error) {
	start, err := current.expect(tokenLParen, "'('")
	if err != nil {
		return nil, err
	}
	if current.peek().kind == tokenRParen {
		return nil, &SyntaxError{
			Position: start.position,
			Message:  "module initial parameter list may not be empty",
		}
	}
	parameters := make([]ParameterDecl, 0)
	for {
		if current.peekKeyword("type") {
			return nil, &SyntaxError{
				Position: current.peek().position,
				Message:  "module initialization parameters may not include type formals",
			}
		}
		names, err := current.parseFormalIdentifierList("module initialization parameter name")
		if err != nil {
			return nil, err
		}
		if _, err := current.expect(tokenColon, "':'"); err != nil {
			return nil, err
		}
		typeExpression, err := current.parseClosedTypeExpression("module initialization parameter type")
		if err != nil {
			return nil, err
		}
		if !current.peekKeyword("is") {
			return nil, &SyntaxError{
				Position: current.peek().position,
				Message:  "every module initialization parameter requires a default association",
			}
		}
		current.advance()
		defaultExpression, err := current.parseExpression()
		if err != nil {
			return nil, err
		}
		for _, name := range names {
			value := defaultExpression
			parameters = append(parameters, ParameterDecl{
				Position: name.position, Name: name.lexeme,
				Type: typeExpressionSpelling(typeExpression), TypeExpression: typeExpression,
				Default: &value,
			})
		}
		if current.peek().kind != tokenComma && current.peek().kind != tokenSemicolon {
			break
		}
		current.advance()
	}
	if _, err := current.expect(tokenRParen, "')'"); err != nil {
		return nil, err
	}
	return parameters, nil
}

func (current *parser) parseModuleEntryProcess() (ModuleProcessDecl, error) {
	result := ModuleProcessDecl{Position: current.peek().position, Entry: true}
	for current.peek().kind != tokenIndependent && !current.peekKeyword("handler") && !current.peekKeyword("final") &&
		!current.peekKeyword("end") {
		if current.peek().kind == tokenEOF {
			return ModuleProcessDecl{}, current.unexpected("module process statement, '||', or module end")
		}
		statement, err := current.parseBehaviorStatement()
		if err != nil {
			return ModuleProcessDecl{}, err
		}
		result.Statements = append(result.Statements, statement)
	}
	if len(result.Statements) == 0 {
		return ModuleProcessDecl{}, &SyntaxError{
			Position: result.Position, Message: "ordinary module process requires at least one statement",
		}
	}
	return result, nil
}

func (current *parser) parseModuleAwaitProcess() (ModuleProcessDecl, error) {
	start, err := current.expectKeyword("await")
	if err != nil {
		return ModuleProcessDecl{}, err
	}
	result := ModuleProcessDecl{Position: start.position, Await: true}
	for {
		alternative := ModuleAwaitAlternativeDecl{Position: current.peek().position}
		if placeholders, present, err := current.parsePatternPlaceholderDeclarations(); err != nil {
			return ModuleProcessDecl{}, err
		} else if present {
			alternative.Placeholders = placeholders
		}
		trigger, err := current.parseBehaviorPattern()
		if err != nil {
			return ModuleProcessDecl{}, err
		}
		alternative.Trigger = trigger
		if isBehaviorPatternOperator(current.peek()) {
			return ModuleProcessDecl{}, &SyntaxError{
				Position: current.peek().position,
				Message:  "chained await pattern operators require explicit parentheses in the current source subset",
			}
		}
		if current.peekKeyword("where") {
			current.advance()
			guard, err := current.parseExpression()
			if err != nil {
				return ModuleProcessDecl{}, err
			}
			alternative.Guard = &guard
		}
		if len(result.Alternatives) == 0 && current.peek().kind == tokenSemicolon {
			current.advance()
			result.Alternatives = append(result.Alternatives, alternative)
			return result, nil
		}
		if _, err := current.expect(tokenPipe, "'=>'"); err != nil {
			return ModuleProcessDecl{}, err
		}
		for !current.peekKeyword("or") && !current.peekKeyword("else") && !current.peekKeyword("end") {
			if current.peek().kind == tokenEOF {
				return ModuleProcessDecl{}, current.unexpected("await statement, 'or', 'else', or 'end await'")
			}
			statement, err := current.parseBehaviorStatement()
			if err != nil {
				return ModuleProcessDecl{}, err
			}
			alternative.Statements = append(alternative.Statements, statement)
		}
		if len(alternative.Statements) == 0 {
			return ModuleProcessDecl{}, current.unexpected("await alternative statement")
		}
		result.Alternatives = append(result.Alternatives, alternative)
		if current.peekKeyword("or") {
			current.advance()
			continue
		}
		break
	}
	if current.peekKeyword("else") {
		result.ElsePresent = true
		current.advance()
		for !current.peekKeyword("end") {
			if current.peek().kind == tokenEOF {
				return ModuleProcessDecl{}, current.unexpected("await else statement or 'end await'")
			}
			statement, err := current.parseBehaviorStatement()
			if err != nil {
				return ModuleProcessDecl{}, err
			}
			result.Else = append(result.Else, statement)
		}
		if len(result.Else) == 0 {
			return ModuleProcessDecl{}, current.unexpected("await else statement")
		}
	}
	if _, err := current.expectKeyword("end"); err != nil {
		return ModuleProcessDecl{}, err
	}
	if current.peekKeyword("await") {
		current.advance()
	}
	if current.peek().kind == tokenIdentifier {
		return ModuleProcessDecl{}, &SyntaxError{
			Position: current.peek().position,
			Message:  "named await terminators are outside the current top-level module process subset",
		}
	}
	if _, err := current.expect(tokenSemicolon, "';'"); err != nil {
		return ModuleProcessDecl{}, err
	}
	return result, nil
}

func (current *parser) startsBasicClockDeclaration() bool {
	return current.peek().kind == tokenIdentifier && current.peekAt(1).kind == tokenColon &&
		current.peekAtKeyword(2, "Clock") && current.peekAtKeyword(3, "is") &&
		isMakeClockConstructor(current.peekAt(4))
}

func (current *parser) startsModuleTimingObjectDeclaration() bool {
	return current.peek().kind == tokenIdentifier && current.peekAt(1).kind == tokenColon &&
		current.peekAt(2).kind == tokenIdentifier && current.peekAt(3).kind == tokenDot &&
		current.peekAtKeyword(4, "Ticks") && current.peekAtKeyword(5, "is")
}

func (current *parser) parseModuleTimingObjectDeclaration() (ModuleTimingObjectDecl, error) {
	name, err := current.expectIdentifier("module timing object name")
	if err != nil {
		return ModuleTimingObjectDecl{}, err
	}
	if _, err := current.expect(tokenColon, "':'"); err != nil {
		return ModuleTimingObjectDecl{}, err
	}
	clock, err := current.expectIdentifier("timing object's clock name")
	if err != nil {
		return ModuleTimingObjectDecl{}, err
	}
	if _, err := current.expect(tokenDot, "'.'"); err != nil {
		return ModuleTimingObjectDecl{}, err
	}
	if _, err := current.expectKeyword("Ticks"); err != nil {
		return ModuleTimingObjectDecl{}, err
	}
	if _, err := current.expectKeyword("is"); err != nil {
		return ModuleTimingObjectDecl{}, err
	}
	initial, err := current.parseExpression()
	if err != nil {
		return ModuleTimingObjectDecl{}, err
	}
	if _, err := current.expect(tokenSemicolon, "';'"); err != nil {
		return ModuleTimingObjectDecl{}, err
	}
	return ModuleTimingObjectDecl{
		Position: name.position, Name: name.lexeme, Clock: clock.lexeme, Initial: initial,
	}, nil
}

func isMakeClockConstructor(candidate token) bool {
	return candidate.kind == tokenIdentifier &&
		(keyword(candidate.lexeme, "Make_Clock") || keyword(candidate.lexeme, "MakeClock"))
}

func (current *parser) parseModuleObjectDeclaration() ([]ModuleObjectDecl, error) {
	names := make([]token, 0, 1)
	for {
		name, err := current.expectIdentifier("module object name")
		if err != nil {
			return nil, err
		}
		names = append(names, name)
		if current.peek().kind != tokenComma {
			break
		}
		current.advance()
	}
	if _, err := current.expect(tokenColon, "':'"); err != nil {
		return nil, &SyntaxError{
			Position: current.peek().position,
			Message:  "the current module object subset requires 'Name : Boolean|Integer is closed_expression;'",
		}
	}
	typeExpression, err := current.parseClosedTypeExpression("module object type")
	if err != nil {
		return nil, err
	}
	if _, err := current.expectKeyword("is"); err != nil {
		return nil, &SyntaxError{
			Position: current.peek().position,
			Message:  "module objects require an explicit initializer in the deterministic profile",
		}
	}
	initial, err := current.parseExpression()
	if err != nil {
		return nil, err
	}
	if _, err := current.expect(tokenSemicolon, "';'"); err != nil {
		return nil, err
	}
	result := make([]ModuleObjectDecl, len(names))
	for index, name := range names {
		result[index] = ModuleObjectDecl{
			Position: name.position, Name: name.lexeme,
			Type: typeExpressionSpelling(typeExpression), TypeExpression: typeExpression, Initial: initial,
		}
	}
	return result, nil
}

func (current *parser) parseBasicClockDeclaration() (ClockDecl, error) {
	name, err := current.expectIdentifier("basic clock declaration or module process part")
	if err != nil {
		return ClockDecl{}, err
	}
	if _, err := current.expect(tokenColon, "':'"); err != nil {
		return ClockDecl{}, err
	}
	if _, err := current.expectKeyword("Clock"); err != nil {
		return ClockDecl{}, &SyntaxError{
			Position: current.peek().position,
			Message:  "only C : Clock is Make_Clock() module declarations are in the current source subset",
		}
	}
	if _, err := current.expectKeyword("is"); err != nil {
		return ClockDecl{}, err
	}
	if !isMakeClockConstructor(current.peek()) {
		return ClockDecl{}, &SyntaxError{
			Position: current.peek().position,
			Message:  "expected published clock constructor 'Make_Clock'",
		}
	}
	current.advance()
	if _, err := current.expect(tokenLParen, "'('"); err != nil {
		return ClockDecl{}, err
	}
	if _, err := current.expect(tokenRParen, "')'"); err != nil {
		return ClockDecl{}, err
	}
	if _, err := current.expect(tokenSemicolon, "';'"); err != nil {
		return ClockDecl{}, err
	}
	return ClockDecl{Position: name.position, Name: name.lexeme}, nil
}

func (current *parser) labeledDeclareIntroducesWhen() bool {
	return current.declareIntroducesWhen(2)
}

func (current *parser) declareIntroducesWhen(declareOffset int) bool {
	if !current.peekAtKeyword(declareOffset, "declare") ||
		!current.peekAtKeyword(declareOffset+1, "exception") {
		return false
	}
	offset := declareOffset + 1
	for current.peekAtKeyword(offset, "exception") {
		depth := 0
		for {
			tok := current.peekAt(offset)
			if tok.kind == tokenEOF {
				return false
			}
			switch tok.kind {
			case tokenLParen:
				depth++
			case tokenRParen:
				if depth > 0 {
					depth--
				}
			case tokenSemicolon:
				if depth == 0 {
					offset++
					goto nextDeclaration
				}
			}
			offset++
		}
	nextDeclaration:
	}
	return current.peekAtKeyword(offset, "when")
}

func (current *parser) parseModuleWhenOuterExceptionDeclarations() ([]ExceptionDecl, error) {
	start, err := current.expectKeyword("declare")
	if err != nil {
		return nil, err
	}
	result := make([]ExceptionDecl, 0)
	for current.peekKeyword("exception") {
		exception, err := current.parseModuleExceptionDeclaration()
		if err != nil {
			return nil, err
		}
		result = append(result, exception)
	}
	if len(result) == 0 {
		return nil, &SyntaxError{
			Position: start.position,
			Message:  "the current outer declaration-bearing when subset requires one or more exception declarations",
		}
	}
	if !current.peekKeyword("when") {
		return nil, &SyntaxError{
			Position: current.peek().position,
			Message:  "the current outer declaration-bearing when subset supports exception declarations followed directly by 'when'",
		}
	}
	return result, nil
}

func (current *parser) parseModuleWhenProcess(label string, outerExceptions []ExceptionDecl) (ModuleProcessDecl, error) {
	start, err := current.expectKeyword("when")
	if err != nil {
		return ModuleProcessDecl{}, &SyntaxError{
			Position: current.peek().position,
			Message:  "the current module process subset requires one top-level 'when' statement per process",
		}
	}
	result := ModuleProcessDecl{
		Position: start.position, Label: label,
		OuterExceptions: cloneExceptionDeclarations(outerExceptions),
	}
	if placeholders, present, err := current.parsePatternPlaceholderDeclarations(); err != nil {
		return ModuleProcessDecl{}, err
	} else if present {
		result.Placeholders = placeholders
	}
	trigger, err := current.parseBehaviorPattern()
	if err != nil {
		return ModuleProcessDecl{}, err
	}
	result.Trigger = trigger
	if isBehaviorPatternOperator(current.peek()) {
		return ModuleProcessDecl{}, &SyntaxError{
			Position: current.peek().position,
			Message:  "chained module-process pattern operators require explicit parentheses in the current source subset",
		}
	}
	if current.peekKeyword("where") {
		current.advance()
		guard, err := current.parseExpression()
		if err != nil {
			return ModuleProcessDecl{}, err
		}
		result.Guard = &guard
	}
	if current.peekKeyword("declare") {
		result.IterationExceptions, err = current.parseModuleWhenIterationExceptionDeclarations()
		if err != nil {
			return ModuleProcessDecl{}, err
		}
	}
	if _, err := current.expectKeyword("do"); err != nil {
		return ModuleProcessDecl{}, err
	}
	for !current.peekKeyword("handler") && !current.peekKeyword("end") {
		if current.peek().kind == tokenEOF {
			return ModuleProcessDecl{}, current.unexpected("module process statement or 'end when'")
		}
		statement, err := current.parseBehaviorStatement()
		if err != nil {
			return ModuleProcessDecl{}, err
		}
		result.Statements = append(result.Statements, statement)
	}
	if len(result.Statements) == 0 {
		return ModuleProcessDecl{}, current.unexpected("module process statement")
	}
	if current.peekKeyword("handler") {
		handler, err := current.parseBehaviorHandler()
		if err != nil {
			return ModuleProcessDecl{}, err
		}
		result.Handler = &handler
	}
	if _, err := current.expectKeyword("end"); err != nil {
		return ModuleProcessDecl{}, err
	}
	if current.peekKeyword("while") || current.peekKeyword("for") ||
		current.peekKeyword("when") || current.peekKeyword("loop") ||
		current.peekKeyword("do") {
		current.advance()
	}
	terminator, err := current.parseBehaviorDoTerminator(label)
	if err != nil {
		return ModuleProcessDecl{}, err
	}
	result.Terminator = terminator
	if _, err := current.expect(tokenSemicolon, "';'"); err != nil {
		return ModuleProcessDecl{}, err
	}
	return result, nil
}

func (current *parser) parseModuleWhenIterationExceptionDeclarations() ([]ExceptionDecl, error) {
	start, err := current.expectKeyword("declare")
	if err != nil {
		return nil, err
	}
	result := make([]ExceptionDecl, 0)
	for current.peekKeyword("exception") {
		exception, err := current.parseModuleExceptionDeclaration()
		if err != nil {
			return nil, err
		}
		result = append(result, exception)
	}
	if len(result) == 0 {
		return nil, &SyntaxError{
			Position: start.position,
			Message:  "the current per-match declaration-bearing when subset requires one or more exception declarations",
		}
	}
	if !current.peekKeyword("do") {
		return nil, &SyntaxError{
			Position: current.peek().position,
			Message:  "the current per-match declaration-bearing when subset supports exception declarations followed directly by 'do'",
		}
	}
	return result, nil
}

func (current *parser) parseModuleExceptionDeclaration() (ExceptionDecl, error) {
	start, err := current.expectKeyword("exception")
	if err != nil {
		return ExceptionDecl{}, err
	}
	name, err := current.expectIdentifier("exception name")
	if err != nil {
		return ExceptionDecl{}, err
	}
	result := ExceptionDecl{Position: start.position, Name: name.lexeme}
	if current.peek().kind == tokenLParen {
		current.advance()
		if current.peek().kind != tokenRParen {
			for {
				parameterNames, err := current.parseFormalIdentifierList("exception parameter name")
				if err != nil {
					return ExceptionDecl{}, err
				}
				if _, err := current.expect(tokenColon, "':'"); err != nil {
					return ExceptionDecl{}, err
				}
				parameterType, err := current.parseClosedTypeExpression("exception parameter type")
				if err != nil {
					return ExceptionDecl{}, err
				}
				for _, parameterName := range parameterNames {
					result.Parameters = append(result.Parameters, ParameterDecl{
						Position: parameterName.position, Name: parameterName.lexeme,
						Type: typeExpressionSpelling(parameterType), TypeExpression: parameterType,
					})
				}
				if current.peek().kind != tokenComma && current.peek().kind != tokenSemicolon {
					break
				}
				current.advance()
			}
		}
		if _, err := current.expect(tokenRParen, "')'"); err != nil {
			return ExceptionDecl{}, err
		}
	}
	if _, err := current.expect(tokenSemicolon, "';'"); err != nil {
		return ExceptionDecl{}, err
	}
	return result, nil
}

func outermostExceptionDeclarationIdentity(name string) string {
	return "rapide:outermost:exception:" + folded(name)
}

func moduleExceptionDeclarationIdentity(module, name string) string {
	return "rapide:module:" + folded(module) + ":exception:" + folded(name)
}

func doExceptionDeclarationIdentity(module, label, name string) string {
	return "rapide:module:" + folded(module) + ":do:" + folded(label) + ":exception:" + folded(name)
}

func lexicalDoExceptionDeclarationIdentity(module, path, name string) string {
	return "rapide:module:" + folded(module) + ":lexical-do:" + path + ":exception:" + folded(name)
}

func functionDoExceptionDeclarationIdentity(module, path, label, name string) string {
	return "rapide:module:" + folded(module) + ":function-do:" + path + ":label:" + folded(label) + ":exception:" + folded(name)
}

func initializerDoExceptionDeclarationIdentity(module, path, label, name string) string {
	return "rapide:module:" + folded(module) + ":initializer-do:" + path + ":label:" + folded(label) + ":exception:" + folded(name)
}

func finalizerDoExceptionDeclarationIdentity(module, path, label, name string) string {
	return "rapide:module:" + folded(module) + ":finalizer-do:" + path + ":label:" + folded(label) + ":exception:" + folded(name)
}

func architectureInitializerDoExceptionDeclarationIdentity(architecture, path, label, name string) string {
	return "rapide:architecture:" + folded(architecture) + ":initializer-do:" + path + ":label:" + folded(label) + ":exception:" + folded(name)
}

func lexicalWhenExceptionDeclarationIdentity(module, path, region, name string) string {
	return "rapide:module:" + folded(module) + ":lexical-when:" + path + ":" + region + ":exception:" + folded(name)
}

func whenIterationExceptionDeclarationIdentity(module, label, name string) string {
	return "rapide:module:" + folded(module) + ":when:" + folded(label) + ":iteration:exception:" + folded(name)
}

func interfaceExceptionDeclarationIdentity(interfaceName string, region InterfaceNameRegion, name string) string {
	return "rapide:interface:" + folded(interfaceName) + ":" + string(region) + ":exception:" + folded(name)
}

func (current *parser) parseBehaviorHandler() (BehaviorHandlerDecl, error) {
	return current.parseBehaviorHandlerUntil(false)
}

func (current *parser) parseBehaviorHandlerUntil(stopAtFinal bool) (BehaviorHandlerDecl, error) {
	start, err := current.expectKeyword("handler")
	if err != nil {
		return BehaviorHandlerDecl{}, err
	}
	result := BehaviorHandlerDecl{Position: start.position}
	for current.peekKeyword("is") {
		choiceStart := current.advance()
		patternDecl, err := current.parseBehaviorPattern()
		if err != nil {
			return BehaviorHandlerDecl{}, err
		}
		if _, err := current.expect(tokenPipe, "'=>'"); err != nil {
			return BehaviorHandlerDecl{}, err
		}
		choice := BehaviorHandlerChoiceDecl{Position: choiceStart.position, Pattern: patternDecl}
		for !current.peekKeyword("is") && !current.peekKeyword("else") && !current.peekKeyword("end") &&
			!(stopAtFinal && current.peekKeyword("final")) {
			if current.peek().kind == tokenEOF {
				return BehaviorHandlerDecl{}, current.unexpected("handler statement, choice, else, or block end")
			}
			statement, err := current.parseBehaviorStatement()
			if err != nil {
				return BehaviorHandlerDecl{}, err
			}
			choice.Statements = append(choice.Statements, statement)
		}
		if len(choice.Statements) == 0 {
			return BehaviorHandlerDecl{}, current.unexpected("handler choice statement")
		}
		result.Choices = append(result.Choices, choice)
	}
	if current.peekKeyword("else") {
		current.advance()
		for !current.peekKeyword("end") && !(stopAtFinal && current.peekKeyword("final")) {
			if current.peek().kind == tokenEOF {
				return BehaviorHandlerDecl{}, current.unexpected("handler else statement or block end")
			}
			statement, err := current.parseBehaviorStatement()
			if err != nil {
				return BehaviorHandlerDecl{}, err
			}
			result.Else = append(result.Else, statement)
		}
		if len(result.Else) == 0 {
			return BehaviorHandlerDecl{}, current.unexpected("handler else statement")
		}
	}
	if len(result.Choices) == 0 && len(result.Else) == 0 {
		return BehaviorHandlerDecl{}, &SyntaxError{Position: start.position, Message: "handler requires a choice or else part"}
	}
	return result, nil
}

func (current *parser) parseArchitecture() (ArchitectureDecl, error) {
	start, _ := current.expectKeyword("architecture")
	name, err := current.expectIdentifier("architecture name")
	if err != nil {
		return ArchitectureDecl{}, err
	}
	if _, err := current.expect(tokenLParen, "'('"); err != nil {
		return ArchitectureDecl{}, err
	}
	parameters := make([]ParameterDecl, 0)
	if current.peek().kind != tokenRParen {
		for {
			parameterNames, err := current.parseFormalIdentifierList("architecture generator parameter name")
			if err != nil {
				return ArchitectureDecl{}, err
			}
			if _, err := current.expect(tokenColon, "':'"); err != nil {
				return ArchitectureDecl{}, err
			}
			parameterType, err := current.parseClosedTypeExpression("architecture generator parameter type")
			if err != nil {
				return ArchitectureDecl{}, err
			}
			var defaultExpression *ExpressionDecl
			if current.peekKeyword("is") {
				current.advance()
				value, err := current.parseExpression()
				if err != nil {
					return ArchitectureDecl{}, err
				}
				defaultExpression = &value
			}
			for _, parameterName := range parameterNames {
				parameters = append(parameters, ParameterDecl{
					Position: parameterName.position, Name: parameterName.lexeme,
					Type: typeExpressionSpelling(parameterType), TypeExpression: parameterType,
					Default: cloneExpressionDeclarationPointer(defaultExpression),
				})
			}
			if current.peek().kind != tokenComma && current.peek().kind != tokenSemicolon {
				break
			}
			current.advance()
		}
	}
	if _, err := current.expect(tokenRParen, "')'"); err != nil {
		return ArchitectureDecl{}, err
	}
	returnType := TypeExpressionDecl{
		Position: start.position, Kind: TypeExpressionName, Name: "Root",
	}
	if current.peekKeyword("return") {
		current.advance()
		returnType, err = current.parseClosedTypeExpression("architecture return interface type")
		if err != nil {
			return ArchitectureDecl{}, err
		}
	}
	if _, err := current.expectKeyword("is"); err != nil {
		return ArchitectureDecl{}, err
	}
	result := ArchitectureDecl{
		Position: start.position, Name: name.lexeme, Parameters: parameters,
		ReturnType: typeExpressionSpelling(returnType), ReturnTypeExpression: returnType,
	}
	for !current.peekKeyword("constraint") && !current.peekKeyword("connect") &&
		!current.peekKeyword("initial") && !current.peekKeyword("end") {
		component, err := current.parseArchitectureComponent()
		if err != nil {
			return ArchitectureDecl{}, err
		}
		result.Components = append(result.Components, component)
	}
	if current.peekKeyword("constraint") {
		current.advance()
		for !current.peekKeyword("connect") && !current.peekKeyword("initial") && !current.peekKeyword("end") {
			declarations, err := current.parsePatternConstraintGroup()
			if err != nil {
				return ArchitectureDecl{}, err
			}
			result.Constraints = append(result.Constraints, declarations...)
		}
	}
	if current.peekKeyword("connect") {
		current.advance()
		for !current.peekKeyword("constraint") && !current.peekKeyword("initial") && !current.peekKeyword("end") {
			if current.startsConnectionGenerator() {
				generator, err := current.parseConnectionGenerator(current.parseActionRef)
				if err != nil {
					return ArchitectureDecl{}, err
				}
				result.ConnectionGenerators = append(result.ConnectionGenerators, generator)
				continue
			}
			connections, err := current.parseConnection()
			if err != nil {
				return ArchitectureDecl{}, err
			}
			result.Connections = append(result.Connections, connections...)
		}
	}
	if current.peekKeyword("constraint") {
		current.advance()
		for !current.peekKeyword("initial") && !current.peekKeyword("end") {
			declarations, err := current.parsePatternConstraintGroup()
			if err != nil {
				return ArchitectureDecl{}, err
			}
			result.Constraints = append(result.Constraints, declarations...)
		}
	}
	if current.peekKeyword("initial") {
		current.advance()
		for !current.peekKeyword("connect") && !current.peekKeyword("end") {
			statement, err := current.parseBehaviorStatement()
			if err != nil {
				return ArchitectureDecl{}, err
			}
			result.Initial = append(result.Initial, statement)
		}
		if current.peekKeyword("connect") {
			return ArchitectureDecl{}, &SyntaxError{
				Position: current.peek().position,
				Message:  "architecture initial before connect contradicts the Architecture LRM grammar and is outside the current compatibility subset",
			}
		}
	}
	if _, err := current.expectKeyword("end"); err != nil {
		return ArchitectureDecl{}, err
	}
	if current.peekKeyword("architecture") {
		current.advance()
	}
	if current.peek().kind == tokenIdentifier && keyword(current.peek().lexeme, result.Name) {
		current.advance()
	}
	if _, err := current.expect(tokenSemicolon, "';'"); err != nil {
		return ArchitectureDecl{}, err
	}
	return result, nil
}

func (current *parser) parseArchitectureComponent() (ComponentDecl, error) {
	componentName, err := current.expectIdentifier("component name")
	if err != nil {
		return ComponentDecl{}, err
	}
	if _, err := current.expect(tokenColon, "':'"); err != nil {
		return ComponentDecl{}, err
	}
	integerArray := false
	indexType := ""
	var firstIndex, lastIndex int64
	var resultRangeFirst, resultRangeLast ExpressionDecl
	var interfaceType token
	if current.peekKeyword("array") {
		integerArray = true
		current.advance()
		if _, err := current.expect(tokenLBracket, "'[' after component array"); err != nil {
			return ComponentDecl{}, err
		}
		if current.peek().kind == tokenIdentifier && current.peekAt(1).kind == tokenRBracket {
			indexType = current.advance().lexeme
			if _, err := current.expect(tokenRBracket, "']' after component-array index type"); err != nil {
				return ComponentDecl{}, err
			}
		} else {
			firstExpression, expressionErr := current.parseExpression()
			if expressionErr != nil {
				return ComponentDecl{}, expressionErr
			}
			if _, err := current.expect(tokenDot, "first '.' in component-array range '..'"); err != nil {
				return ComponentDecl{}, &SyntaxError{
					Position: current.peek().position,
					Message:  "the current finite component-array subset requires an Integer range First..Last or a named finite range type",
				}
			}
			if _, err := current.expect(tokenDot, "second '.' in component-array range '..'"); err != nil {
				return ComponentDecl{}, err
			}
			lastExpression, expressionErr := current.parseExpression()
			if expressionErr != nil {
				return ComponentDecl{}, expressionErr
			}
			if literal, ok := closedSignedInteger(firstExpression); ok {
				firstIndex = literal
			}
			if literal, ok := closedSignedInteger(lastExpression); ok {
				lastIndex = literal
			}
			resultRangeFirst = firstExpression
			resultRangeLast = lastExpression
			if _, err := current.expect(tokenRBracket, "']'"); err != nil {
				return ComponentDecl{}, err
			}
		}
		if _, err := current.expectKeyword("of"); err != nil {
			return ComponentDecl{}, err
		}
		interfaceType, err = current.expectIdentifier("component-array element interface type")
	} else {
		interfaceType, err = current.expectIdentifier("component interface type")
	}
	if err != nil {
		return ComponentDecl{}, err
	}
	moduleName := ""
	moduleArguments := make([]ExpressionDecl, 0)
	moduleArgumentFormals := make([]string, 0)
	var architectureLiteral *ArchitectureDecl
	if current.peekKeyword("is") {
		if integerArray {
			return ComponentDecl{}, &SyntaxError{
				Position: current.peek().position,
				Message:  "component-array denotation expressions are outside the current finite source subset",
			}
		}
		current.advance()
		if current.peekKeyword("architecture") {
			literal, err := current.parseArchitectureLiteral(interfaceType)
			if err != nil {
				return ComponentDecl{}, err
			}
			architectureLiteral = &literal
		} else {
			module, err := current.expectIdentifier("component module or architecture generator")
			if err != nil {
				return ComponentDecl{}, err
			}
			moduleName = module.lexeme
			if _, err := current.expect(tokenLParen, "'('"); err != nil {
				return ComponentDecl{}, err
			}
			if current.peek().kind != tokenRParen {
				named := false
				for {
					formal := ""
					if current.peek().kind == tokenIdentifier && current.peekAtKeyword(1, "is") {
						formal = current.advance().lexeme
						current.advance()
						named = true
					} else if named {
						return ComponentDecl{}, &SyntaxError{
							Position: current.peek().position,
							Message:  "positional module arguments must precede named associations",
						}
					}
					argument, err := current.parseExpression()
					if err != nil {
						return ComponentDecl{}, err
					}
					moduleArguments = append(moduleArguments, argument)
					moduleArgumentFormals = append(moduleArgumentFormals, formal)
					if current.peek().kind != tokenComma {
						break
					}
					current.advance()
				}
			}
			if _, err := current.expect(tokenRParen, "')'"); err != nil {
				return ComponentDecl{}, err
			}
		}
	}
	if _, err := current.expect(tokenSemicolon, "';'"); err != nil {
		return ComponentDecl{}, err
	}
	return ComponentDecl{
		Position: componentName.position, Name: componentName.lexeme, InterfaceType: interfaceType.lexeme,
		Module: moduleName, ModuleArguments: moduleArguments, ModuleArgumentFormals: moduleArgumentFormals,
		ArchitectureLiteral: architectureLiteral,
		IntegerArray:        integerArray, IndexType: indexType,
		RangeFirst: resultRangeFirst, RangeLast: resultRangeLast,
		FirstIndex: firstIndex, LastIndex: lastIndex,
	}, nil
}

func (current *parser) parseArchitectureLiteral(contextType token) (ArchitectureDecl, error) {
	start, err := current.expectKeyword("architecture")
	if err != nil {
		return ArchitectureDecl{}, err
	}
	returnType := TypeExpressionDecl{
		Position: contextType.position, Kind: TypeExpressionName, Name: contextType.lexeme,
	}
	result := ArchitectureDecl{
		Position: start.position, Name: "ArchitectureLiteral",
		ReturnType: contextType.lexeme, ReturnTypeExpression: returnType,
	}
	for !current.peekKeyword("constraint") && !current.peekKeyword("connect") &&
		!current.peekKeyword("initial") && !current.peekKeyword("end") {
		component, err := current.parseArchitectureComponent()
		if err != nil {
			return ArchitectureDecl{}, err
		}
		result.Components = append(result.Components, component)
	}
	if current.peekKeyword("constraint") {
		current.advance()
		for !current.peekKeyword("connect") && !current.peekKeyword("initial") && !current.peekKeyword("end") {
			declarations, err := current.parsePatternConstraintGroup()
			if err != nil {
				return ArchitectureDecl{}, err
			}
			result.Constraints = append(result.Constraints, declarations...)
		}
	}
	if current.peekKeyword("connect") {
		current.advance()
		for !current.peekKeyword("constraint") && !current.peekKeyword("initial") && !current.peekKeyword("end") {
			if current.startsConnectionGenerator() {
				generator, err := current.parseConnectionGenerator(current.parseActionRef)
				if err != nil {
					return ArchitectureDecl{}, err
				}
				result.ConnectionGenerators = append(result.ConnectionGenerators, generator)
				continue
			}
			connections, err := current.parseConnection()
			if err != nil {
				return ArchitectureDecl{}, err
			}
			result.Connections = append(result.Connections, connections...)
		}
	}
	if current.peekKeyword("constraint") {
		current.advance()
		for !current.peekKeyword("initial") && !current.peekKeyword("end") {
			declarations, err := current.parsePatternConstraintGroup()
			if err != nil {
				return ArchitectureDecl{}, err
			}
			result.Constraints = append(result.Constraints, declarations...)
		}
	}
	if current.peekKeyword("initial") {
		current.advance()
		for !current.peekKeyword("connect") && !current.peekKeyword("end") {
			statement, err := current.parseBehaviorStatement()
			if err != nil {
				return ArchitectureDecl{}, err
			}
			result.Initial = append(result.Initial, statement)
		}
		if current.peekKeyword("connect") {
			return ArchitectureDecl{}, &SyntaxError{
				Position: current.peek().position,
				Message:  "architecture-literal initial before connect contradicts the Architecture LRM grammar and is outside the current compatibility subset",
			}
		}
	}
	if _, err := current.expectKeyword("end"); err != nil {
		return ArchitectureDecl{}, err
	}
	if current.peekKeyword("architecture") {
		current.advance()
	}
	return result, nil
}

func (current *parser) startsPatternConstraintGroup() bool {
	if current.peekKeyword("match") || current.peekKeyword("not") || current.peekKeyword("never") || current.peekKeyword("observe") {
		return true
	}
	return current.peek().kind == tokenIdentifier && current.peekAt(1).kind == tokenColon &&
		(current.peekAtKeyword(2, "match") || current.peekAtKeyword(2, "not") || current.peekAtKeyword(2, "never") || current.peekAtKeyword(2, "observe"))
}

func (current *parser) startsPatternConstraintBody() bool {
	if current.peekKeyword("match") || current.peekKeyword("not") || current.peekKeyword("never") {
		return true
	}
	return current.peek().kind == tokenIdentifier && current.peekAt(1).kind == tokenColon &&
		(current.peekAtKeyword(2, "match") || current.peekAtKeyword(2, "not") || current.peekAtKeyword(2, "never"))
}

func (current *parser) peekAtKeyword(offset int, expected string) bool {
	tok := current.peekAt(offset)
	return tok.kind == tokenIdentifier && keyword(tok.lexeme, expected)
}

func (current *parser) parseOptionalConstraintLabel() (string, Position, bool) {
	if current.peek().kind != tokenIdentifier || current.peekAt(1).kind != tokenColon {
		return "", Position{}, false
	}
	label := current.advance()
	current.advance()
	return label.lexeme, label.position, true
}

func (current *parser) parsePatternConstraint() (ConstraintComponentDecl, error) {
	label, labelPosition, labeled := current.parseOptionalConstraintLabel()
	negated := false
	if current.peekKeyword("not") {
		current.advance()
		negated = true
	}
	kindToken, err := current.expectIdentifier("constraint kind 'match' or 'never'")
	if err != nil {
		return ConstraintComponentDecl{}, err
	}
	result := ConstraintComponentDecl{Position: kindToken.position, Label: label}
	if labeled {
		result.Position = labelPosition
	}
	switch {
	case keyword(kindToken.lexeme, "match"):
		if negated {
			result.Kind = ConstraintNotMatch
		} else {
			result.Kind = ConstraintMatch
		}
	case keyword(kindToken.lexeme, "never"):
		if negated {
			return ConstraintComponentDecl{}, &SyntaxError{Position: kindToken.position, Message: "'not' may only qualify a 'match' constraint"}
		}
		result.Kind = ConstraintNever
	default:
		return ConstraintComponentDecl{}, &SyntaxError{Position: kindToken.position, Message: "expected constraint kind 'match' or 'never'"}
	}
	if placeholders, present, err := current.parsePatternPlaceholderDeclarations(); err != nil {
		return ConstraintComponentDecl{}, err
	} else if present {
		result.Placeholders = placeholders
	}
	result.Pattern, err = current.parseBehaviorPattern()
	if err != nil {
		return ConstraintComponentDecl{}, err
	}
	if isBehaviorPatternOperator(current.peek()) {
		return ConstraintComponentDecl{}, &SyntaxError{
			Position: current.peek().position,
			Message:  "chained constraint pattern operators require explicit parentheses in the current source subset",
		}
	}
	if current.peekKeyword("where") {
		current.advance()
		guard, err := current.parseExpression()
		if err != nil {
			return ConstraintComponentDecl{}, err
		}
		result.Guard = &guard
	}
	if _, err := current.expect(tokenSemicolon, "';'"); err != nil {
		return ConstraintComponentDecl{}, err
	}
	return result, nil
}

func (current *parser) parsePatternConstraintGroup() ([]ConstraintDecl, error) {
	label, labelPosition, labeled := current.parseOptionalConstraintLabel()
	if !current.peekKeyword("observe") {
		component, err := current.parsePatternConstraint()
		if err != nil {
			return nil, err
		}
		position := component.Position
		if labeled {
			position = labelPosition
		}
		return []ConstraintDecl{{Position: position, Label: label, Components: []ConstraintComponentDecl{component}}}, nil
	}
	start := current.advance()
	if labeled {
		start.position = labelPosition
	}
	if _, err := current.expectKeyword("from"); err != nil {
		return nil, err
	}
	alphabet := make([]BehaviorPatternDecl, 0)
	for {
		filter, err := current.parseBehaviorPatternPrimary()
		if err != nil {
			return nil, err
		}
		if filter.Kind != BehaviorBasicPattern {
			return nil, &SyntaxError{Position: filter.Position, Message: "alphabet filter requires basic patterns"}
		}
		if patternAssociationsContainPlaceholder(filter.Event.Arguments) {
			return nil, &SyntaxError{Position: filter.Position, Message: "alphabet filter placeholders are not defined by Stanford Rapide"}
		}
		alphabet = append(alphabet, filter)
		if current.peek().kind != tokenComma {
			break
		}
		current.advance()
	}
	if !current.startsPatternConstraintBody() {
		return nil, current.unexpected("filtered 'match' or 'never' constraint")
	}
	components := make([]ConstraintComponentDecl, 0)
	for current.startsPatternConstraintBody() {
		component, err := current.parsePatternConstraint()
		if err != nil {
			return nil, err
		}
		components = append(components, component)
	}
	if _, err := current.expectKeyword("end"); err != nil {
		return nil, err
	}
	if current.peekKeyword("observe") {
		current.advance()
	}
	if _, err := current.expect(tokenSemicolon, "';'"); err != nil {
		return nil, err
	}
	return []ConstraintDecl{{
		Position: start.position, Label: label,
		Alphabet: append([]BehaviorPatternDecl(nil), alphabet...), Components: components,
	}}, nil
}

func (current *parser) parseConnection() ([]ConnectionDecl, error) {
	return current.parseConnectionWithTarget(current.parseActionRef)
}

func (current *parser) parseModuleConnection() ([]ConnectionDecl, error) {
	return current.parseConnectionWithTarget(current.parseModuleActionRef)
}

func (current *parser) startsConnectionGenerator() bool {
	return current.peekKeyword("if") || current.peekKeyword("for")
}

// parseConnectionGenerator parses Stanford's elaboration-time connection
// generator construct. Closed Boolean `if` schemes and closed finite Integer
// range schemes elaborate topology; they are never runtime process iteration.
func (current *parser) parseConnectionGenerator(targetParser func() (ActionRef, error)) (ConnectionGeneratorDecl, error) {
	start := current.peek()
	result := ConnectionGeneratorDecl{Position: start.position}
	closingKeyword := ""
	switch {
	case current.peekKeyword("if"):
		current.advance()
		condition, err := current.parseExpression()
		if err != nil {
			return ConnectionGeneratorDecl{}, err
		}
		result.Kind = ConnectionGeneratorIf
		result.Condition = condition
		closingKeyword = "if"
	case current.peekKeyword("for"):
		current.advance()
		iterator, err := current.expectIdentifier("connection generator iterator name")
		if err != nil {
			return ConnectionGeneratorDecl{}, &SyntaxError{
				Position: start.position,
				Message:  "the current connection generator subset requires 'for I : Integer in First..Last'",
			}
		}
		if _, err := current.expect(tokenColon, "':'"); err != nil {
			return ConnectionGeneratorDecl{}, &SyntaxError{
				Position: iterator.position,
				Message:  "the current connection generator subset requires 'for I : Integer in First..Last'",
			}
		}
		iteratorType, err := current.parseClosedTypeExpression("connection generator iterator type")
		if err != nil {
			return ConnectionGeneratorDecl{}, err
		}
		if _, err := current.expectKeyword("in"); err != nil {
			return ConnectionGeneratorDecl{}, err
		}
		first, err := current.parseExpression()
		if err != nil {
			return ConnectionGeneratorDecl{}, err
		}
		if _, err := current.expect(tokenDot, "first '.' in connection generator range '..'"); err != nil {
			return ConnectionGeneratorDecl{}, err
		}
		if _, err := current.expect(tokenDot, "second '.' in connection generator range '..'"); err != nil {
			return ConnectionGeneratorDecl{}, err
		}
		last, err := current.parseExpression()
		if err != nil {
			return ConnectionGeneratorDecl{}, err
		}
		result.Kind = ConnectionGeneratorForRange
		result.Iterator = iterator.lexeme
		result.IteratorType = typeExpressionSpelling(iteratorType)
		result.RangeFirst = first
		result.RangeLast = last
		closingKeyword = "for"
	default:
		return ConnectionGeneratorDecl{}, current.unexpected("connection generation scheme 'if' or 'for'")
	}
	if _, err := current.expectKeyword("generate"); err != nil {
		return ConnectionGeneratorDecl{}, err
	}
	if result.Kind == ConnectionGeneratorForRange {
		current.connectionIteratorScopes = append(current.connectionIteratorScopes, result.Iterator)
	}
	for !current.peekKeyword("end") {
		if current.peek().kind == tokenEOF {
			return ConnectionGeneratorDecl{}, current.unexpected("connection or 'end generate'")
		}
		if current.startsConnectionGenerator() {
			nested, err := current.parseConnectionGenerator(targetParser)
			if err != nil {
				return ConnectionGeneratorDecl{}, err
			}
			result.Generators = append(result.Generators, nested)
			continue
		}
		connections, err := current.parseConnectionWithTarget(targetParser)
		if err != nil {
			return ConnectionGeneratorDecl{}, err
		}
		result.Connections = append(result.Connections, connections...)
	}
	if result.Kind == ConnectionGeneratorForRange {
		current.connectionIteratorScopes = current.connectionIteratorScopes[:len(current.connectionIteratorScopes)-1]
	}
	if len(result.Connections) == 0 && len(result.Generators) == 0 {
		return ConnectionGeneratorDecl{}, &SyntaxError{
			Position: start.position, Message: "connection generator requires at least one connection",
		}
	}
	current.advance()
	if current.peekKeyword("generate") {
		current.advance()
		if current.peekKeyword(closingKeyword) {
			current.advance()
		}
	} else if current.peekKeyword(closingKeyword) {
		current.advance()
	}
	if _, err := current.expect(tokenSemicolon, "';'"); err != nil {
		return ConnectionGeneratorDecl{}, err
	}
	return result, nil
}

// parseConnectionWithTarget expands the Architecture LRM's connection-rule
// list shorthand into its Cartesian product. The expansion is syntactic: each
// result retains the same outer placeholder declarations and connector, so a
// comma list and the corresponding explicit rules compile to one canonical
// semantic model.
func (current *parser) parseConnectionWithTarget(targetParser func() (ActionRef, error)) ([]ConnectionDecl, error) {
	start := current.peek().position
	placeholders := make([]ParameterDecl, 0)
	if parsed, present, err := current.parsePatternPlaceholderDeclarations(); err != nil {
		return nil, err
	} else if present {
		placeholders = parsed
	}
	type guardedSourcePattern struct {
		pattern BehaviorPatternDecl
		guard   *ExpressionDecl
	}
	sourcePatterns := make([]guardedSourcePattern, 0, 1)
	for {
		sourcePattern, err := current.parseBehaviorPattern()
		if err != nil {
			return nil, err
		}
		if isBehaviorPatternOperator(current.peek()) {
			return nil, &SyntaxError{
				Position: current.peek().position,
				Message:  "chained connection pattern operators require explicit parentheses in the current source subset",
			}
		}
		var guard *ExpressionDecl
		if current.peekKeyword("where") {
			current.advance()
			expression, err := current.parseExpression()
			if err != nil {
				return nil, err
			}
			guard = &expression
		}
		sourcePatterns = append(sourcePatterns, guardedSourcePattern{pattern: sourcePattern, guard: guard})
		if current.peek().kind != tokenComma {
			break
		}
		current.advance()
	}
	connectorToken := current.peek()
	var connector Connector
	switch {
	case connectorToken.kind == tokenPipe:
		connector = ConnectPipe
		current.advance()
	case connectorToken.kind == tokenAgent:
		connector = ConnectAgent
		current.advance()
	case connectorToken.kind == tokenIdentifier && keyword(connectorToken.lexeme, "to"):
		connector = ConnectBasic
		current.advance()
	default:
		return nil, current.unexpected("connection operator 'to', '=>', or '||>'")
	}
	targets := make([]ActionRef, 0, 1)
	targetGenerators := make([]ConnectionSetGeneratorDecl, 0)
	for {
		if current.startsConnectionGenerator() {
			generator, err := current.parseConnectionSetGenerator(targetParser)
			if err != nil {
				return nil, err
			}
			targetGenerators = append(targetGenerators, generator)
		} else {
			target, err := targetParser()
			if err != nil {
				return nil, err
			}
			targets = append(targets, target)
		}
		if current.peek().kind != tokenComma {
			break
		}
		current.advance()
	}
	if _, err := current.expect(tokenSemicolon, "';'"); err != nil {
		return nil, err
	}
	result := make([]ConnectionDecl, 0, len(sourcePatterns)*(len(targets)+len(targetGenerators)))
	for _, guardedSource := range sourcePatterns {
		sourcePattern := guardedSource.pattern
		var source ActionRef
		if sourcePattern.Kind == BehaviorBasicPattern {
			var componentIndex *ExpressionDecl
			if sourcePattern.Event.ComponentIndex != nil {
				copy := cloneExpressionDeclaration(*sourcePattern.Event.ComponentIndex)
				componentIndex = &copy
			}
			source = ActionRef{
				Position: sourcePattern.Event.Position, Component: sourcePattern.Event.Component,
				ComponentIndex: componentIndex, Action: sourcePattern.Event.Name,
				Path: append([]QualifiedMemberSegmentDecl(nil), sourcePattern.Event.Path...),
			}
			if arguments, ok := positionalPlaceholderArguments(sourcePattern.Event.Arguments); ok {
				source.Arguments = arguments
			}
		}
		for _, target := range targets {
			pattern := sourcePattern
			var guard *ExpressionDecl
			if guardedSource.guard != nil {
				guardCopy := *guardedSource.guard
				guard = &guardCopy
			}
			result = append(result, ConnectionDecl{
				Position: start, Placeholders: append([]ParameterDecl(nil), placeholders...), Source: source,
				SourcePattern: &pattern, Guard: guard, Connector: connector, Target: target,
			})
		}
		for index := range targetGenerators {
			pattern := sourcePattern
			var guard *ExpressionDecl
			if guardedSource.guard != nil {
				guardCopy := *guardedSource.guard
				guard = &guardCopy
			}
			generatorCopy := targetGenerators[index]
			result = append(result, ConnectionDecl{
				Position: start, Placeholders: append([]ParameterDecl(nil), placeholders...), Source: source,
				SourcePattern: &pattern, Guard: guard, Connector: connector, TargetGenerator: &generatorCopy,
			})
		}
	}
	return result, nil
}

// parseConnectionSetGenerator parses a generation scheme nested in the body
// set of one connection rule. It intentionally has no trailing semicolon: the
// enclosing rule owns that terminator.
func (current *parser) parseConnectionSetGenerator(targetParser func() (ActionRef, error)) (ConnectionSetGeneratorDecl, error) {
	start := current.peek()
	result := ConnectionSetGeneratorDecl{Position: start.position}
	closingKeyword := ""
	switch {
	case current.peekKeyword("if"):
		current.advance()
		condition, err := current.parseExpression()
		if err != nil {
			return ConnectionSetGeneratorDecl{}, err
		}
		result.Kind = ConnectionGeneratorIf
		result.Condition = condition
		closingKeyword = "if"
	case current.peekKeyword("for"):
		current.advance()
		iterator, err := current.expectIdentifier("connection-set generator iterator name")
		if err != nil {
			return ConnectionSetGeneratorDecl{}, &SyntaxError{
				Position: start.position,
				Message:  "the current connection-set generator subset requires 'for I : Integer in First..Last'",
			}
		}
		if _, err := current.expect(tokenColon, "':'"); err != nil {
			return ConnectionSetGeneratorDecl{}, &SyntaxError{
				Position: iterator.position,
				Message:  "the current connection-set generator subset requires 'for I : Integer in First..Last'",
			}
		}
		iteratorType, err := current.parseClosedTypeExpression("connection-set generator iterator type")
		if err != nil {
			return ConnectionSetGeneratorDecl{}, err
		}
		if _, err := current.expectKeyword("in"); err != nil {
			return ConnectionSetGeneratorDecl{}, err
		}
		first, err := current.parseExpression()
		if err != nil {
			return ConnectionSetGeneratorDecl{}, err
		}
		if _, err := current.expect(tokenDot, "first '.' in connection-set generator range '..'"); err != nil {
			return ConnectionSetGeneratorDecl{}, err
		}
		if _, err := current.expect(tokenDot, "second '.' in connection-set generator range '..'"); err != nil {
			return ConnectionSetGeneratorDecl{}, err
		}
		last, err := current.parseExpression()
		if err != nil {
			return ConnectionSetGeneratorDecl{}, err
		}
		result.Kind = ConnectionGeneratorForRange
		result.Iterator = iterator.lexeme
		result.IteratorType = typeExpressionSpelling(iteratorType)
		result.RangeFirst = first
		result.RangeLast = last
		closingKeyword = "for"
	default:
		return ConnectionSetGeneratorDecl{}, current.unexpected("connection-set generation scheme 'if' or 'for'")
	}
	if _, err := current.expectKeyword("generate"); err != nil {
		return ConnectionSetGeneratorDecl{}, err
	}
	if result.Kind == ConnectionGeneratorForRange {
		current.connectionIteratorScopes = append(current.connectionIteratorScopes, result.Iterator)
	}
	for !current.peekKeyword("end") {
		if current.peek().kind == tokenEOF {
			return ConnectionSetGeneratorDecl{}, current.unexpected("connection target or 'end generate'")
		}
		if current.startsConnectionGenerator() {
			nested, err := current.parseConnectionSetGenerator(targetParser)
			if err != nil {
				return ConnectionSetGeneratorDecl{}, err
			}
			result.Generators = append(result.Generators, nested)
		} else {
			target, err := targetParser()
			if err != nil {
				return ConnectionSetGeneratorDecl{}, err
			}
			result.Targets = append(result.Targets, target)
		}
		if current.peek().kind == tokenComma {
			current.advance()
			continue
		}
		if !current.peekKeyword("end") {
			return ConnectionSetGeneratorDecl{}, current.unexpected("',' or 'end generate' in connection set")
		}
	}
	if result.Kind == ConnectionGeneratorForRange {
		current.connectionIteratorScopes = current.connectionIteratorScopes[:len(current.connectionIteratorScopes)-1]
	}
	if len(result.Targets) == 0 && len(result.Generators) == 0 {
		return ConnectionSetGeneratorDecl{}, &SyntaxError{
			Position: start.position, Message: "connection-set generator requires at least one target",
		}
	}
	current.advance()
	if current.peekKeyword("generate") {
		current.advance()
		if current.peekKeyword(closingKeyword) {
			current.advance()
		}
	} else if current.peekKeyword(closingKeyword) {
		current.advance()
	}
	return result, nil
}

func positionalPlaceholderArguments(associations []PatternParameterAssociationDecl) ([]PlaceholderRef, bool) {
	result := make([]PlaceholderRef, 0, len(associations))
	for _, association := range associations {
		if association.Formal != "" || association.Actual.Kind != ExpressionPlaceholder {
			return nil, false
		}
		result = append(result, PlaceholderRef{Position: association.Position, Name: association.Actual.Name})
	}
	return result, true
}

func patternAssociationsContainPlaceholder(associations []PatternParameterAssociationDecl) bool {
	for _, association := range associations {
		if expressionContainsPlaceholder(association.Actual) {
			return true
		}
	}
	return false
}

func expressionContainsPlaceholder(expression ExpressionDecl) bool {
	if expression.Kind == ExpressionPlaceholder || expression.Kind == ExpressionUniversal {
		return true
	}
	if expression.Left != nil && expressionContainsPlaceholder(*expression.Left) ||
		expression.Right != nil && expressionContainsPlaceholder(*expression.Right) {
		return true
	}
	for _, argument := range expression.Arguments {
		if expressionContainsPlaceholder(argument) {
			return true
		}
	}
	return false
}

func (current *parser) parseModuleActionRef() (ActionRef, error) {
	action, err := current.expectIdentifier("module connection target action")
	if err != nil {
		return ActionRef{}, err
	}
	result := ActionRef{Position: action.position, Action: action.lexeme}
	if current.peek().kind != tokenLParen {
		return result, nil
	}
	current.advance()
	if current.peek().kind != tokenRParen {
		named := false
		for {
			formal := ""
			if current.peek().kind == tokenIdentifier && current.peekAtKeyword(1, "is") {
				formal = current.advance().lexeme
				current.advance()
				named = true
			} else if named {
				return ActionRef{}, &SyntaxError{
					Position: current.peek().position,
					Message:  "positional target arguments must precede named associations",
				}
			}
			expression, err := current.parseExpression()
			if err != nil {
				return ActionRef{}, err
			}
			result.ArgumentExpressions = append(result.ArgumentExpressions, expression)
			result.ArgumentFormals = append(result.ArgumentFormals, formal)
			if current.peek().kind != tokenComma {
				break
			}
			current.advance()
		}
	}
	if _, err := current.expect(tokenRParen, "')'"); err != nil {
		return ActionRef{}, err
	}
	result.Arguments = positionalPlaceholderRefs(result.ArgumentExpressions)
	return result, nil
}

func (current *parser) parseActionRef() (ActionRef, error) {
	result := ActionRef{}
	first, err := current.expectIdentifier("component or architecture-interface action name")
	if err != nil {
		return ActionRef{}, err
	}
	component := ""
	var componentIndex *ExpressionDecl
	action := first.lexeme
	var path []QualifiedMemberSegmentDecl
	if current.peek().kind == tokenLBracket {
		index, err := current.parseComponentSelection()
		if err != nil {
			return ActionRef{}, err
		}
		component = first.lexeme
		componentIndex = &index
		action, path, err = current.parseQualifiedMemberPath("selected-component action name")
		if err != nil {
			return ActionRef{}, err
		}
	} else if current.peek().kind == tokenDot {
		current.advance()
		component = first.lexeme
		action, path, err = current.parseQualifiedMemberPath("action name")
		if err != nil {
			return ActionRef{}, err
		}
	}
	if current.hasFinalIndexedServiceSuffix() {
		current.advance()
		index, err := current.parseConnectionPathIndex("service index")
		if err != nil {
			return ActionRef{}, err
		}
		if _, err := current.expect(tokenRParen, "')'"); err != nil {
			return ActionRef{}, err
		}
		path[len(path)-1].Index = &index
		action = qualifiedMemberPathSpelling(path)
	} else if current.peek().kind == tokenLParen {
		current.advance()
		if current.peek().kind != tokenRParen {
			named := false
			for {
				formal := ""
				if current.peek().kind == tokenIdentifier && current.peekAtKeyword(1, "is") {
					formal = current.advance().lexeme
					current.advance()
					named = true
				} else if named {
					return ActionRef{}, &SyntaxError{
						Position: current.peek().position,
						Message:  "positional target arguments must precede named associations",
					}
				}
				expression, err := current.parseExpression()
				if err != nil {
					return ActionRef{}, err
				}
				result.ArgumentExpressions = append(result.ArgumentExpressions, expression)
				result.ArgumentFormals = append(result.ArgumentFormals, formal)
				if current.peek().kind != tokenComma {
					break
				}
				current.advance()
			}
		}
		if _, err := current.expect(tokenRParen, "')'"); err != nil {
			return ActionRef{}, err
		}
		result.Arguments = positionalPlaceholderRefs(result.ArgumentExpressions)
	}
	result.Position = first.position
	result.Component = component
	result.ComponentIndex = componentIndex
	result.Action = action
	result.Path = path
	return result, nil
}

func positionalPlaceholderRefs(expressions []ExpressionDecl) []PlaceholderRef {
	if len(expressions) == 0 {
		return nil
	}
	result := make([]PlaceholderRef, 0, len(expressions))
	for _, expression := range expressions {
		if expression.Kind != ExpressionPlaceholder {
			return nil
		}
		result = append(result, PlaceholderRef{Position: expression.Position, Name: expression.Name})
	}
	return result
}

// parseQualifiedMemberPath parses the service-free name produced by the
// Architecture LRM's service rewrite. The caller has already consumed the
// component separator. A parenthesized signed Integer is an indexed service
// segment only when another '.' follows it; the final parentheses remain the
// action/function argument list.
func (current *parser) parseQualifiedMemberPath(description string) (string, []QualifiedMemberSegmentDecl, error) {
	segments := make([]QualifiedMemberSegmentDecl, 0, 2)
	for {
		member, err := current.expectIdentifier(description)
		if err != nil {
			return "", nil, err
		}
		segment := QualifiedMemberSegmentDecl{Position: member.position, Name: member.lexeme}
		if current.hasIndexedQualifiedMemberSuffix() {
			current.advance()
			index, err := current.parseConnectionPathIndex("service member index")
			if err != nil {
				return "", nil, err
			}
			if _, err := current.expect(tokenRParen, "')'"); err != nil {
				return "", nil, err
			}
			segment.Index = &index
		}
		segments = append(segments, segment)
		if current.peek().kind != tokenDot {
			return qualifiedMemberPathSpelling(segments), segments, nil
		}
		current.advance()
	}
}

func qualifiedMemberPathSpelling(segments []QualifiedMemberSegmentDecl) string {
	spellings := make([]string, len(segments))
	for index, segment := range segments {
		spellings[index] = segment.Name
		if segment.Index == nil {
			continue
		}
		switch segment.Index.Kind {
		case ExpressionInteger:
			spellings[index] += "(" + strconv.FormatInt(segment.Index.Integer, 10) + ")"
		case ExpressionName:
			spellings[index] += "(" + segment.Index.Name + ")"
		}
	}
	return strings.Join(spellings, ".")
}

func (current *parser) parseConnectionPathIndex(description string) (ExpressionDecl, error) {
	position := current.peek().position
	if current.peek().kind == tokenIdentifier && current.isConnectionIterator(current.peek().lexeme) {
		name := current.advance()
		return ExpressionDecl{Position: name.position, Kind: ExpressionName, Name: name.lexeme}, nil
	}
	value, err := current.parseSignedIntegerLiteral(description)
	if err != nil {
		return ExpressionDecl{}, err
	}
	return ExpressionDecl{Position: position, Kind: ExpressionInteger, Integer: value}, nil
}

// parseComponentSelection parses Stanford's bracket selection between an
// array-valued component name and one of that component's interface members.
// Parentheses remain reserved for service sets and action arguments.
func (current *parser) parseComponentSelection() (ExpressionDecl, error) {
	if _, err := current.expect(tokenLBracket, "'['"); err != nil {
		return ExpressionDecl{}, err
	}
	index, err := current.parseExpression()
	if err != nil {
		return ExpressionDecl{}, err
	}
	if _, err := current.expect(tokenRBracket, "']'"); err != nil {
		return ExpressionDecl{}, err
	}
	if _, err := current.expect(tokenDot, "'.' after component-array selection"); err != nil {
		return ExpressionDecl{}, err
	}
	return index, nil
}

func (current *parser) isConnectionIterator(name string) bool {
	for index := len(current.connectionIteratorScopes) - 1; index >= 0; index-- {
		if keyword(current.connectionIteratorScopes[index], name) {
			return true
		}
	}
	return false
}

func (current *parser) hasIndexedQualifiedMemberSuffix() bool {
	if current.peek().kind != tokenLParen {
		return false
	}
	distance := 1
	if current.peekAt(distance).kind == tokenMinus {
		distance++
	}
	if current.peekAt(distance).kind != tokenInteger &&
		!(current.peekAt(distance).kind == tokenIdentifier && current.isConnectionIterator(current.peekAt(distance).lexeme)) {
		return false
	}
	distance++
	return current.peekAt(distance).kind == tokenRParen &&
		current.peekAt(distance+1).kind == tokenDot
}

func (current *parser) hasFinalIndexedServiceSuffix() bool {
	if current.peek().kind != tokenLParen {
		return false
	}
	distance := 1
	if current.peekAt(distance).kind == tokenMinus {
		distance++
	}
	if current.peekAt(distance).kind != tokenInteger &&
		!(current.peekAt(distance).kind == tokenIdentifier && current.isConnectionIterator(current.peekAt(distance).lexeme)) {
		return false
	}
	return current.peekAt(distance+1).kind == tokenRParen
}

func (current *parser) peek() token {
	if current.index >= len(current.tokens) {
		return token{kind: tokenEOF}
	}
	return current.tokens[current.index]
}

func (current *parser) peekAt(distance int) token {
	index := current.index + distance
	if index < 0 || index >= len(current.tokens) {
		return token{kind: tokenEOF}
	}
	return current.tokens[index]
}

func (current *parser) advance() token {
	result := current.peek()
	if current.index < len(current.tokens) {
		current.index++
	}
	return result
}

func (current *parser) peekKeyword(expected string) bool {
	return current.peek().kind == tokenIdentifier && keyword(current.peek().lexeme, expected)
}

func (current *parser) expectKeyword(expected string) (token, error) {
	if !current.peekKeyword(expected) {
		return token{}, current.unexpected("'" + expected + "'")
	}
	return current.advance(), nil
}

func (current *parser) expectIdentifier(description string) (token, error) {
	if current.peek().kind != tokenIdentifier {
		return token{}, current.unexpected(description)
	}
	return current.advance(), nil
}

func (current *parser) expect(kind tokenKind, description string) (token, error) {
	if current.peek().kind != kind {
		return token{}, current.unexpected(description)
	}
	return current.advance(), nil
}

func (current *parser) unexpected(expected string) error {
	actual := current.peek().lexeme
	if current.peek().kind == tokenEOF {
		actual = "end of input"
	} else {
		actual = fmt.Sprintf("%q", actual)
	}
	return &SyntaxError{Position: current.peek().position, Message: fmt.Sprintf("expected %s, found %s", expected, actual)}
}

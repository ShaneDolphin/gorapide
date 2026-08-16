package rapide

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

type tokenKind int

const (
	tokenEOF tokenKind = iota
	tokenIdentifier
	tokenLParen
	tokenRParen
	tokenColon
	tokenSemicolon
	tokenComma
	tokenDot
	tokenPipe
	tokenAgent
	tokenQuestion
	tokenBang
	tokenInteger
	tokenFloat
	tokenCharacter
	tokenString
	tokenPlus
	tokenMinus
	tokenAmpersand
	tokenStar
	tokenSlash
	tokenAssign
	tokenDollar
	tokenEqual
	tokenNotEqual
	tokenLess
	tokenLessOrEqual
	tokenGreater
	tokenGreaterOrEqual
	tokenSequence
	tokenImmediateSequence
	tokenIndependent
	tokenDisjoint
	tokenEquivalent
	tokenLBracket
	tokenRBracket
	tokenApostrophe
)

type token struct {
	kind     tokenKind
	lexeme   string
	position Position
}

type lexer struct {
	source []byte
	offset int
	line   int
	column int
}

func lex(source []byte) ([]token, error) {
	current := &lexer{source: source, line: 1, column: 1}
	result := make([]token, 0)
	for {
		current.skipSpaceAndComments()
		position := current.position()
		if current.offset >= len(current.source) {
			result = append(result, token{kind: tokenEOF, position: position})
			return result, nil
		}
		start := current.offset
		value := current.source[current.offset]
		if isIdentifierStart(value) {
			current.advanceByte()
			for current.offset < len(current.source) && isIdentifierContinue(current.source[current.offset]) {
				current.advanceByte()
			}
			result = append(result, token{kind: tokenIdentifier, lexeme: string(current.source[start:current.offset]), position: position})
			continue
		}
		if value >= '0' && value <= '9' {
			current.advanceByte()
			for current.offset < len(current.source) && current.source[current.offset] >= '0' && current.source[current.offset] <= '9' {
				current.advanceByte()
			}
			kind := tokenInteger
			if current.peekByte(0) == '.' && current.peekByte(1) >= '0' && current.peekByte(1) <= '9' {
				kind = tokenFloat
				current.advanceByte()
				for current.peekByte(0) >= '0' && current.peekByte(0) <= '9' {
					current.advanceByte()
				}
				if current.peekByte(0) == 'e' || current.peekByte(0) == 'E' {
					current.advanceByte()
					if current.peekByte(0) == '+' || current.peekByte(0) == '-' {
						current.advanceByte()
					}
					if current.peekByte(0) < '0' || current.peekByte(0) > '9' {
						return nil, current.errorAt(position, "floating-point exponent requires at least one decimal digit")
					}
					for current.peekByte(0) >= '0' && current.peekByte(0) <= '9' {
						current.advanceByte()
					}
				}
			}
			result = append(result, token{kind: kind, lexeme: string(current.source[start:current.offset]), position: position})
			continue
		}
		switch value {
		case '\'':
			if current.apostropheIntroducesDelimiter() {
				current.advanceByte()
				result = append(result, token{kind: tokenApostrophe, lexeme: "'", position: position})
				break
			}
			current.advanceByte()
			contentStart := current.offset
			if current.offset >= len(current.source) || current.peekByte(0) == '\r' || current.peekByte(0) == '\n' {
				return nil, current.errorAt(position, "unterminated character literal")
			}
			if current.peekByte(0) == '\\' {
				current.advanceByte()
				escape := current.peekByte(0)
				switch {
				case escape == 'n' || escape == '\\' || escape == '\'':
					current.advanceByte()
				case escape >= '0' && escape <= '9':
					for current.peekByte(0) >= '0' && current.peekByte(0) <= '9' {
						current.advanceByte()
					}
				default:
					return nil, current.errorAt(current.position(), "character literal has an unsupported escape; expected \\n, \\\\, \\', or \\N")
				}
			} else if isCharacterLiteralLetter(current.peekByte(0)) {
				current.advanceByte()
			} else {
				return nil, current.errorAt(current.position(), "direct character literal must be an ASCII English letter; use \\N for another code")
			}
			if current.peekByte(0) != '\'' {
				return nil, current.errorAt(current.position(), "character literal must contain exactly one published character form")
			}
			lexeme := string(current.source[contentStart:current.offset])
			current.advanceByte()
			result = append(result, token{kind: tokenCharacter, lexeme: lexeme, position: position})
		case '"':
			current.advanceByte()
			contentStart := current.offset
			for current.offset < len(current.source) && current.peekByte(0) != '"' {
				character := current.peekByte(0)
				if character == '\r' || character == '\n' {
					return nil, current.errorAt(position, "unterminated string literal")
				}
				if character == '\\' {
					current.advanceByte()
					escape := current.peekByte(0)
					switch {
					case escape == 'n' || escape == '\\' || escape == '\'':
						current.advanceByte()
					case escape >= '0' && escape <= '9':
						for current.peekByte(0) >= '0' && current.peekByte(0) <= '9' {
							current.advanceByte()
						}
					default:
						return nil, current.errorAt(current.position(), "string literal has an unsupported escape; expected \\n, \\\\, \\', or \\N")
					}
					continue
				}
				if character < 0x20 || character >= utf8.RuneSelf {
					return nil, current.errorAt(current.position(), "string literal contains a character outside the current printable ASCII subset")
				}
				current.advanceByte()
			}
			if current.offset >= len(current.source) {
				return nil, current.errorAt(position, "unterminated string literal")
			}
			lexeme := string(current.source[contentStart:current.offset])
			current.advanceByte()
			result = append(result, token{kind: tokenString, lexeme: lexeme, position: position})
		case '(':
			current.advanceByte()
			result = append(result, token{kind: tokenLParen, lexeme: "(", position: position})
		case ')':
			current.advanceByte()
			result = append(result, token{kind: tokenRParen, lexeme: ")", position: position})
		case '[':
			current.advanceByte()
			result = append(result, token{kind: tokenLBracket, lexeme: "[", position: position})
		case ']':
			current.advanceByte()
			result = append(result, token{kind: tokenRBracket, lexeme: "]", position: position})
		case ':':
			current.advanceByte()
			if current.peekByte(0) == '=' {
				current.advanceByte()
				result = append(result, token{kind: tokenAssign, lexeme: ":=", position: position})
			} else {
				result = append(result, token{kind: tokenColon, lexeme: ":", position: position})
			}
		case ';':
			current.advanceByte()
			result = append(result, token{kind: tokenSemicolon, lexeme: ";", position: position})
		case ',':
			current.advanceByte()
			result = append(result, token{kind: tokenComma, lexeme: ",", position: position})
		case '.':
			current.advanceByte()
			result = append(result, token{kind: tokenDot, lexeme: ".", position: position})
		case '?':
			current.advanceByte()
			result = append(result, token{kind: tokenQuestion, lexeme: "?", position: position})
		case '!':
			current.advanceByte()
			result = append(result, token{kind: tokenBang, lexeme: "!", position: position})
		case '$':
			current.advanceByte()
			result = append(result, token{kind: tokenDollar, lexeme: "$", position: position})
		case '+':
			current.advanceByte()
			result = append(result, token{kind: tokenPlus, lexeme: "+", position: position})
		case '&':
			current.advanceByte()
			result = append(result, token{kind: tokenAmpersand, lexeme: "&", position: position})
		case '-':
			current.advanceByte()
			if current.peekByte(0) == '>' {
				current.advanceByte()
				result = append(result, token{kind: tokenSequence, lexeme: "->", position: position})
			} else {
				result = append(result, token{kind: tokenMinus, lexeme: "-", position: position})
			}
		case '*':
			current.advanceByte()
			result = append(result, token{kind: tokenStar, lexeme: "*", position: position})
		case '/':
			current.advanceByte()
			if current.peekByte(0) == '=' {
				current.advanceByte()
				result = append(result, token{kind: tokenNotEqual, lexeme: "/=", position: position})
			} else {
				result = append(result, token{kind: tokenSlash, lexeme: "/", position: position})
			}
		case '=':
			current.advanceByte()
			if current.peekByte(0) == '>' {
				current.advanceByte()
				result = append(result, token{kind: tokenPipe, lexeme: "=>", position: position})
			} else {
				result = append(result, token{kind: tokenEqual, lexeme: "=", position: position})
			}
		case '<':
			current.advanceByte()
			if current.peekByte(0) == '=' && current.peekByte(1) == '>' {
				current.advanceByte()
				current.advanceByte()
				result = append(result, token{kind: tokenEquivalent, lexeme: "<=>", position: position})
			} else if current.peekByte(0) == '=' {
				current.advanceByte()
				result = append(result, token{kind: tokenLessOrEqual, lexeme: "<=", position: position})
			} else {
				result = append(result, token{kind: tokenLess, lexeme: "<", position: position})
			}
		case '>':
			current.advanceByte()
			if current.peekByte(0) == '=' {
				current.advanceByte()
				result = append(result, token{kind: tokenGreaterOrEqual, lexeme: ">=", position: position})
			} else {
				result = append(result, token{kind: tokenGreater, lexeme: ">", position: position})
			}
		case '|':
			if current.peekByte(1) == '|' {
				current.advanceByte()
				current.advanceByte()
				if current.peekByte(0) == '>' {
					current.advanceByte()
					result = append(result, token{kind: tokenAgent, lexeme: "||>", position: position})
				} else {
					result = append(result, token{kind: tokenIndependent, lexeme: "||", position: position})
				}
			} else if current.peekByte(1) == '>' {
				current.advanceByte()
				current.advanceByte()
				result = append(result, token{kind: tokenImmediateSequence, lexeme: "|>", position: position})
			} else {
				return nil, current.errorAt(position, "unexpected '|'; expected '|>', '||', or '||>'")
			}
		case '~':
			current.advanceByte()
			result = append(result, token{kind: tokenDisjoint, lexeme: "~", position: position})
		default:
			if value >= utf8.RuneSelf {
				return nil, current.errorAt(position, "non-ASCII identifiers are outside the current Rapide lexical subset")
			}
			return nil, current.errorAt(position, fmt.Sprintf("unexpected character %q", value))
		}
	}
}

// apostropheIntroducesDelimiter distinguishes Stanford Rapide's T'(E) and
// E'Attribute delimiters from a Character literal without weakening the
// latter's exact diagnostics. Lexical whitespace may separate tokens.
func (current *lexer) apostropheIntroducesDelimiter() bool {
	for offset := current.offset + 1; offset < len(current.source); offset++ {
		switch current.source[offset] {
		case ' ', '\t', '\r', '\n':
			continue
		default:
			if current.source[offset] == '(' {
				return true
			}
			offset = len(current.source)
		}
	}
	if current.offset == 0 {
		return false
	}
	previous := current.source[current.offset-1]
	switch {
	case isIdentifierContinue(previous), previous == ')', previous == ']':
		return true
	default:
		return false
	}
}

func (current *lexer) skipSpaceAndComments() {
	for current.offset < len(current.source) {
		value := current.source[current.offset]
		if value == '-' && current.peekByte(1) == '-' {
			for current.offset < len(current.source) && current.source[current.offset] != '\n' {
				current.advanceByte()
			}
			continue
		}
		if value == ' ' || value == '\t' || value == '\r' || value == '\n' {
			current.advanceByte()
			continue
		}
		return
	}
}

func (current *lexer) position() Position {
	return Position{Offset: current.offset, Line: current.line, Column: current.column}
}

func (current *lexer) advanceByte() {
	if current.offset >= len(current.source) {
		return
	}
	if current.source[current.offset] == '\n' {
		current.line++
		current.column = 1
	} else {
		current.column++
	}
	current.offset++
}

func (current *lexer) peekByte(distance int) byte {
	index := current.offset + distance
	if index < 0 || index >= len(current.source) {
		return 0
	}
	return current.source[index]
}

func (current *lexer) errorAt(position Position, message string) error {
	return &SyntaxError{Position: position, Message: message}
}

func isIdentifierStart(value byte) bool {
	return value == '_' || value >= 'A' && value <= 'Z' || value >= 'a' && value <= 'z'
}

func isIdentifierContinue(value byte) bool {
	return isIdentifierStart(value) || value >= '0' && value <= '9'
}

func isCharacterLiteralLetter(value byte) bool {
	return value >= 'A' && value <= 'Z' || value >= 'a' && value <= 'z'
}

func keyword(value, expected string) bool {
	return strings.EqualFold(value, expected)
}

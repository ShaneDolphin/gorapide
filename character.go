package gorapide

import "fmt"

// RapideCharacter is one value of Stanford Rapide's predefined Character
// type. Character is intentionally distinct from both Integer and String.
//
// The Predefined Types LRM makes Code and Code_To_Char one-to-one inverse
// functions between Character and Integer. Keeping the complete signed
// Integer code therefore avoids imposing an undocumented byte, ASCII,
// Unicode, or host-rune bound on the language value.
type RapideCharacter struct {
	code int64
}

// RapideCharacterFromCode denotes the Character returned by the published
// Code_To_Char(code) operation. Every deterministic Rapide Integer code has
// exactly one Character denotation.
func RapideCharacterFromCode(code int64) RapideCharacter {
	return RapideCharacter{code: code}
}

// Code returns the exact Rapide Integer code for character.
func (character RapideCharacter) Code() int64 {
	return character.code
}

// String returns an unambiguous diagnostic representation. It deliberately
// does not coerce Character into a Go string because not every Rapide Integer
// code is a Unicode scalar value.
func (character RapideCharacter) String() string {
	return fmt.Sprintf("Character(%d)", character.code)
}

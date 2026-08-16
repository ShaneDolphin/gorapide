package gorapide

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

const rapideModuleIdentityPrefix = "mod1-"

var ErrInvalidRapideModuleValue = errors.New("invalid Rapide module value")

// ModuleAllocationProvenance is the closed semantic identity of one Rapide
// module allocation. Occurrence must be a stable language-level coordinate,
// such as a generator-call program point plus its deterministic firing
// identity; it must not be a host discovery counter.
//
// Causes identify the event occurrences whose execution enabled the
// allocation. They are treated as a set and canonicalized before hashing.
type ModuleAllocationProvenance struct {
	Profile    string
	Model      string
	Parent     string
	Generator  string
	Occurrence string
	Causes     []EventID
}

// RapideModuleValue is the value denoted by a name bound to a Rapide module.
// Its identity is allocation identity, not the module's current state or the
// structural content of its interface. The fields are deliberately private so
// malformed or host-derived identities cannot enter deterministic events
// without passing validation.
type RapideModuleValue struct {
	identity string
}

// NewRapideModuleValue derives a replay-stable module identity from explicit
// semantic allocation provenance. Equal provenance denotes the same replayed
// allocation; every distinct generator or literal evaluation must have a
// distinct Occurrence and/or causal context.
func NewRapideModuleValue(provenance ModuleAllocationProvenance) (RapideModuleValue, error) {
	if provenance.Profile == "" {
		return RapideModuleValue{}, fmt.Errorf("%w: profile is empty", ErrInvalidRapideModuleValue)
	}
	if provenance.Model == "" {
		return RapideModuleValue{}, fmt.Errorf("%w: model is empty", ErrInvalidRapideModuleValue)
	}
	if provenance.Parent == "" {
		return RapideModuleValue{}, fmt.Errorf("%w: parent is empty", ErrInvalidRapideModuleValue)
	}
	if provenance.Generator == "" {
		return RapideModuleValue{}, fmt.Errorf("%w: generator is empty", ErrInvalidRapideModuleValue)
	}
	if provenance.Occurrence == "" {
		return RapideModuleValue{}, fmt.Errorf("%w: occurrence is empty", ErrInvalidRapideModuleValue)
	}
	causes, err := normalizeCauses(provenance.Causes)
	if err != nil {
		return RapideModuleValue{}, fmt.Errorf("%w: %v", ErrInvalidRapideModuleValue, err)
	}

	var encoded bytes.Buffer
	encoded.WriteString("gorapide:rapide-module-allocation:v1")
	writeCanonicalString(&encoded, provenance.Profile)
	writeCanonicalString(&encoded, provenance.Model)
	writeCanonicalString(&encoded, provenance.Parent)
	writeCanonicalString(&encoded, provenance.Generator)
	writeCanonicalString(&encoded, provenance.Occurrence)
	writeCanonicalUint64(&encoded, uint64(len(causes)))
	for _, cause := range causes {
		writeCanonicalString(&encoded, string(cause))
	}
	digest := sha256.Sum256(encoded.Bytes())
	return RapideModuleValue{identity: rapideModuleIdentityPrefix + hex.EncodeToString(digest[:])}, nil
}

// ParseRapideModuleValue validates and restores the canonical identity carried
// by an execution artifact. It does not allocate a new module.
func ParseRapideModuleValue(identity string) (RapideModuleValue, error) {
	if !strings.HasPrefix(identity, rapideModuleIdentityPrefix) {
		return RapideModuleValue{}, fmt.Errorf("%w: identity %q has no %q prefix",
			ErrInvalidRapideModuleValue, identity, rapideModuleIdentityPrefix)
	}
	hexDigest := strings.TrimPrefix(identity, rapideModuleIdentityPrefix)
	decoded, err := hex.DecodeString(hexDigest)
	if err != nil || len(decoded) != sha256.Size || hex.EncodeToString(decoded) != hexDigest {
		return RapideModuleValue{}, fmt.Errorf("%w: identity %q is not canonical", ErrInvalidRapideModuleValue, identity)
	}
	return RapideModuleValue{identity: identity}, nil
}

// Identity returns the canonical tool-supplied module identity.
func (value RapideModuleValue) Identity() string {
	return value.identity
}

// SameRapideModule implements the observable identity relation supplied for
// interface values. It is intentionally separate from any user-defined "="
// function, which Rapide requires only to be an equivalence relation.
func SameRapideModule(left, right RapideModuleValue) bool {
	return left.identity != "" && left.identity == right.identity
}

func canonicalRapideModuleValue(value RapideModuleValue) (RapideModuleValue, error) {
	parsed, err := ParseRapideModuleValue(value.identity)
	if err != nil {
		return RapideModuleValue{}, err
	}
	return parsed, nil
}

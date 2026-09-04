package constraint

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"

	"github.com/ShaneDolphin/gorapide/pattern"
)

const (
	CanonicalConstraintSetReportFormat         = "gorapide.constraint-set-report.v3"
	legacyCanonicalConstraintSetReportFormatV2 = "gorapide.constraint-set-report.v2"
	legacyCanonicalConstraintSetReportFormatV1 = "gorapide.constraint-set-report.v1"
	legacyCanonicalConstraintSetReportFormat   = legacyCanonicalConstraintSetReportFormatV1
)

var ErrInvalidConstraintSet = errors.New("invalid deterministic constraint set")

// CanonicalCheckable is a closed, deterministic constraint whose model and
// decision both have canonical identities. Pattern constraints implement this
// interface directly; architecture-level semantic constraints may implement it
// without falling back to an opaque host predicate.
type CanonicalCheckable interface {
	Checkable
	CanonicalName() string
	DeterministicDigest() (string, error)
	EvaluateCanonical(pattern.PosetReader) (CanonicalConstraintReport, error)
}

type deterministicConstraintSetMember struct {
	checker CanonicalCheckable
	name    string
	digest  string
}

type canonicalConstraintSetMember struct {
	Name   string `json:"name"`
	Digest string `json:"digest"`
}

type canonicalConstraintSetModel struct {
	Format      string                         `json:"format"`
	Name        string                         `json:"name"`
	Constraints []canonicalConstraintSetMember `json:"constraints"`
}

// CanonicalConstraintSetReport is the complete deterministic decision for a
// named set of closed pattern constraints.
type CanonicalConstraintSetReport struct {
	Format      string                      `json:"format"`
	SetDigest   string                      `json:"set_digest"`
	PosetDigest string                      `json:"poset_digest"`
	Passed      bool                        `json:"passed"`
	Reports     []CanonicalConstraintReport `json:"reports"`
}

// DeterministicConstraints returns the closed pattern constraints in canonical
// identity order. It remains the pattern-specific API; a set containing a
// different canonical semantic checker is valid for set evaluation but cannot
// be represented by this narrower return type. PredicateConstraint and other
// opaque checkers always fail.
func (set *ConstraintSet) DeterministicConstraints() ([]*Constraint, error) {
	members, err := set.deterministicMembers()
	if err != nil {
		return nil, err
	}
	result := make([]*Constraint, 0, len(members))
	for index, member := range members {
		current, ok := member.checker.(*Constraint)
		if !ok || current == nil {
			return nil, fmt.Errorf("%w: checker %d has canonical non-pattern type %T", ErrInvalidConstraintSet, index, member.checker)
		}
		result = append(result, current)
	}
	return result, nil
}

func (set *ConstraintSet) deterministicMembers() ([]deterministicConstraintSetMember, error) {
	if set == nil || set.Name == "" {
		return nil, fmt.Errorf("%w: set or set name is empty", ErrInvalidConstraintSet)
	}
	members := make([]deterministicConstraintSetMember, 0, len(set.checkers))
	seenNames := make(map[string]bool, len(set.checkers))
	for i, checker := range set.checkers {
		canonical, ok := checker.(CanonicalCheckable)
		if !ok || canonical == nil {
			return nil, fmt.Errorf("%w: checker %d has unsupported type %T", ErrInvalidConstraintSet, i, checker)
		}
		name := canonical.CanonicalName()
		if name == "" {
			return nil, fmt.Errorf("%w: checker %d has an empty canonical name", ErrInvalidConstraintSet, i)
		}
		if seenNames[name] {
			return nil, fmt.Errorf("%w: duplicate constraint name %q", ErrInvalidConstraintSet, name)
		}
		seenNames[name] = true
		digest, err := canonical.DeterministicDigest()
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidConstraintSet, err)
		}
		members = append(members, deterministicConstraintSetMember{
			checker: canonical, name: name, digest: digest,
		})
	}
	sort.Slice(members, func(i, j int) bool {
		if members[i].digest != members[j].digest {
			return members[i].digest < members[j].digest
		}
		return members[i].name < members[j].name
	})
	return members, nil
}

// DeterministicDigest returns the canonical identity of the complete set.
func (set *ConstraintSet) DeterministicDigest() (string, error) {
	members, err := set.deterministicMembers()
	if err != nil {
		return "", err
	}
	model := canonicalConstraintSetModel{
		Format: "gorapide.constraint-set-model.v3", Name: set.Name,
		Constraints: make([]canonicalConstraintSetMember, 0, len(members)),
	}
	for _, member := range members {
		model.Constraints = append(model.Constraints, canonicalConstraintSetMember{
			Name: member.name, Digest: member.digest,
		})
	}
	encoded, err := json.Marshal(model)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

// EvaluateCanonical evaluates every closed member against one exact poset.
func (set *ConstraintSet) EvaluateCanonical(poset pattern.PosetReader) (CanonicalConstraintSetReport, error) {
	return set.EvaluateCanonicalWithState(poset, nil)
}

// EvaluateCanonicalWithState evaluates every member using canonical cut data
// partitioned by declared constraint/clause identity.
func (set *ConstraintSet) EvaluateCanonicalWithState(
	poset pattern.PosetReader,
	stateWitnesses []ClauseStateWitnesses,
) (CanonicalConstraintSetReport, error) {
	setDigest, err := set.DeterministicDigest()
	if err != nil {
		return CanonicalConstraintSetReport{}, err
	}
	if poset == nil {
		return CanonicalConstraintSetReport{}, fmt.Errorf("%w: poset is nil", ErrInvalidConstraintSet)
	}
	posetDigest, err := semanticPosetDigest(poset)
	if err != nil {
		return CanonicalConstraintSetReport{}, err
	}
	members, err := set.deterministicMembers()
	if err != nil {
		return CanonicalConstraintSetReport{}, err
	}
	result := CanonicalConstraintSetReport{
		Format: CanonicalConstraintSetReportFormat, SetDigest: setDigest,
		PosetDigest: posetDigest, Passed: true,
		Reports: make([]CanonicalConstraintReport, 0, len(members)),
	}
	for _, member := range members {
		memberWitnesses := make([]ClauseStateWitnesses, 0)
		for _, entry := range stateWitnesses {
			if entry.Constraint == member.name {
				memberWitnesses = append(memberWitnesses, entry)
			}
		}
		var report CanonicalConstraintReport
		if current, ok := member.checker.(*Constraint); ok {
			report, err = current.evaluateCanonicalWithPosetDigest(poset, posetDigest, memberWitnesses)
		} else {
			if len(memberWitnesses) != 0 {
				return CanonicalConstraintSetReport{}, fmt.Errorf(
					"%w: canonical checker %q does not accept pattern-state witnesses",
					ErrInvalidConstraintSet, member.name,
				)
			}
			report, err = member.checker.EvaluateCanonical(poset)
		}
		if err != nil {
			return CanonicalConstraintSetReport{}, err
		}
		if !report.Passed {
			result.Passed = false
		}
		result.Reports = append(result.Reports, report)
	}
	for _, entry := range stateWitnesses {
		found := false
		for _, member := range members {
			if entry.Constraint == member.name {
				found = true
				break
			}
		}
		if !found {
			return CanonicalConstraintSetReport{}, fmt.Errorf("%w: state witness references missing constraint %q", ErrInvalidConstraintSet, entry.Constraint)
		}
	}
	return normalizeConstraintSetReport(result)
}

func (report CanonicalConstraintSetReport) MarshalCanonical() ([]byte, error) {
	normalized, err := normalizeConstraintSetReport(report)
	if err != nil {
		return nil, err
	}
	return json.Marshal(normalized)
}

func (report CanonicalConstraintSetReport) SemanticDigest() (string, error) {
	encoded, err := report.MarshalCanonical()
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func ParseCanonicalConstraintSetReport(data []byte) (CanonicalConstraintSetReport, error) {
	var report CanonicalConstraintSetReport
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&report); err != nil {
		return CanonicalConstraintSetReport{}, fmt.Errorf("%w: %v", ErrInvalidConstraintSet, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return CanonicalConstraintSetReport{}, fmt.Errorf("%w: trailing JSON content", ErrInvalidConstraintSet)
	}
	normalized, err := normalizeConstraintSetReport(report)
	if err != nil {
		return CanonicalConstraintSetReport{}, err
	}
	reencoded, err := json.Marshal(normalized)
	if err != nil || !bytes.Equal(reencoded, data) {
		return CanonicalConstraintSetReport{}, fmt.Errorf("%w: input is not the canonical byte encoding", ErrInvalidConstraintSet)
	}
	return normalized, nil
}

func normalizeConstraintSetReport(report CanonicalConstraintSetReport) (CanonicalConstraintSetReport, error) {
	if (report.Format != CanonicalConstraintSetReportFormat && report.Format != legacyCanonicalConstraintSetReportFormatV2 &&
		report.Format != legacyCanonicalConstraintSetReportFormatV1) ||
		!validDigest(report.SetDigest) || !validDigest(report.PosetDigest) {
		return CanonicalConstraintSetReport{}, fmt.Errorf("%w: malformed format or digest", ErrInvalidConstraintSet)
	}
	result := report
	result.Reports = append([]CanonicalConstraintReport{}, report.Reports...)
	for i := range result.Reports {
		if report.Format != CanonicalConstraintSetReportFormat && result.Reports[i].Format == CanonicalConstraintReportFormat {
			return CanonicalConstraintSetReport{}, fmt.Errorf("%w: legacy set report contains a current member report", ErrInvalidConstraintSet)
		}
		if report.Format == legacyCanonicalConstraintSetReportFormatV1 &&
			result.Reports[i].Format == legacyCanonicalConstraintReportFormatV4 {
			return CanonicalConstraintSetReport{}, fmt.Errorf("%w: v1 set report contains a v4 member report", ErrInvalidConstraintSet)
		}
		normalized, err := normalizeConstraintReport(result.Reports[i])
		if err != nil {
			return CanonicalConstraintSetReport{}, err
		}
		if normalized.PosetDigest != result.PosetDigest {
			return CanonicalConstraintSetReport{}, fmt.Errorf("%w: member poset digest mismatch", ErrInvalidConstraintSet)
		}
		result.Reports[i] = normalized
	}
	sort.Slice(result.Reports, func(i, j int) bool {
		return result.Reports[i].ConstraintDigest < result.Reports[j].ConstraintDigest
	})
	for i := 1; i < len(result.Reports); i++ {
		if result.Reports[i-1].ConstraintDigest == result.Reports[i].ConstraintDigest {
			return CanonicalConstraintSetReport{}, fmt.Errorf("%w: duplicate member report", ErrInvalidConstraintSet)
		}
	}
	passed := true
	for _, member := range result.Reports {
		if !member.Passed {
			passed = false
		}
	}
	if result.Passed != passed {
		return CanonicalConstraintSetReport{}, fmt.Errorf("%w: passed flag conflicts with member reports", ErrInvalidConstraintSet)
	}
	return result, nil
}

package constraint

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/pattern"
)

const (
	CanonicalConstraintReportFormat         = "gorapide.constraint-report.v5"
	legacyCanonicalConstraintReportFormatV4 = "gorapide.constraint-report.v4"
	legacyCanonicalConstraintReportFormatV3 = "gorapide.constraint-report.v3"
	legacyCanonicalConstraintReportFormatV2 = "gorapide.constraint-report.v2"
	legacyCanonicalConstraintReportFormatV1 = "gorapide.constraint-report.v1"
	legacyCanonicalConstraintReportFormat   = legacyCanonicalConstraintReportFormatV1
)

var (
	ErrInvalidConstraintModel  = errors.New("invalid deterministic constraint model")
	ErrInvalidConstraintReport = errors.New("invalid canonical constraint report")
)

type canonicalConstraintClause struct {
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Pattern string `json:"pattern"`
	Message string `json:"message"`
}

type canonicalConstraintModel struct {
	Format      string                      `json:"format"`
	Name        string                      `json:"name"`
	Description string                      `json:"description"`
	Severity    string                      `json:"severity"`
	Filter      string                      `json:"filter,omitempty"`
	Clauses     []canonicalConstraintClause `json:"clauses"`
}

// DeterministicDigest returns the canonical identity of a supported constraint
// declaration. Clause declaration order is not semantic and is normalized.
func (c *Constraint) DeterministicDigest() (string, error) {
	if c == nil {
		return "", fmt.Errorf("%w: constraint is nil", ErrInvalidConstraintModel)
	}
	if c.Name == "" {
		return "", fmt.Errorf("%w: constraint name is empty", ErrInvalidConstraintModel)
	}
	if c.Severity != "error" && c.Severity != "warning" && c.Severity != "info" {
		return "", fmt.Errorf("%w: constraint %q has severity %q", ErrInvalidConstraintModel, c.Name, c.Severity)
	}
	model := canonicalConstraintModel{
		Format: "gorapide.constraint-model.v5", Name: c.Name,
		Description: c.Desc, Severity: c.Severity,
	}
	if c.Filter != nil && len(c.Alphabet) != 0 {
		return "", fmt.Errorf("%w: constraint %q combines pattern and alphabet filters", ErrInvalidConstraintModel, c.Name)
	}
	if c.Filter != nil {
		key, err := pattern.DeterministicKey(c.Filter)
		if err != nil {
			return "", fmt.Errorf("%w: constraint %q filter: %v", ErrInvalidConstraintModel, c.Name, err)
		}
		model.Filter = key
	}
	if len(c.Alphabet) != 0 {
		keys := make([]string, 0, len(c.Alphabet))
		for index, filter := range c.Alphabet {
			key, err := pattern.DeterministicSingleEventKey(filter)
			if err != nil {
				return "", fmt.Errorf("%w: constraint %q alphabet filter %d: %v", ErrInvalidConstraintModel, c.Name, index, err)
			}
			keys = append(keys, key)
		}
		sort.Strings(keys)
		write := 0
		for _, key := range keys {
			if write > 0 && keys[write-1] == key {
				continue
			}
			keys[write] = key
			write++
		}
		keys = keys[:write]
		encoded, err := json.Marshal(struct {
			Kind     string   `json:"kind"`
			Patterns []string `json:"patterns"`
		}{Kind: "alphabet", Patterns: keys})
		if err != nil {
			return "", err
		}
		model.Filter = string(encoded)
	}
	seenNames := make(map[string]bool, len(c.Clauses))
	for _, clause := range c.Clauses {
		if clause.Name == "" || seenNames[clause.Name] {
			return "", fmt.Errorf("%w: constraint %q has empty or duplicate clause %q", ErrInvalidConstraintModel, c.Name, clause.Name)
		}
		seenNames[clause.Name] = true
		if clause.Kind != MustMatch && clause.Kind != MustNotMatch && clause.Kind != MustNever {
			return "", fmt.Errorf("%w: constraint %q clause %q has kind %d", ErrInvalidConstraintModel, c.Name, clause.Name, clause.Kind)
		}
		key, err := pattern.DeterministicKey(clause.Pattern)
		if err != nil {
			return "", fmt.Errorf("%w: constraint %q clause %q: %v", ErrInvalidConstraintModel, c.Name, clause.Name, err)
		}
		model.Clauses = append(model.Clauses, canonicalConstraintClause{
			Name: clause.Name, Kind: clause.Kind.String(), Pattern: key, Message: clause.Message,
		})
	}
	sort.Slice(model.Clauses, func(i, j int) bool {
		return constraintClauseKey(model.Clauses[i]) < constraintClauseKey(model.Clauses[j])
	})
	encoded, err := json.Marshal(model)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func constraintClauseKey(clause canonicalConstraintClause) string {
	encoded, _ := json.Marshal(clause)
	return string(encoded)
}

// CanonicalCausalRelation is a transitive causal fact among witness events.
type CanonicalCausalRelation struct {
	Before string `json:"before"`
	After  string `json:"after"`
}

// CanonicalConstraintViolation is an auditable constraint decision with the
// associated events, placeholder environment, and causal witness.
type CanonicalConstraintViolation struct {
	Constraint     string                     `json:"constraint"`
	Clause         string                     `json:"clause"`
	Kind           string                     `json:"kind"`
	Message        string                     `json:"message"`
	Severity       string                     `json:"severity"`
	Events         []string                   `json:"events"`
	Bindings       []pattern.CanonicalBinding `json:"bindings"`
	Causality      []CanonicalCausalRelation  `json:"causality"`
	StateWitnesses []string                   `json:"state_witnesses,omitempty"`
}

// CanonicalConstraintStateEvaluation records one Boolean result for one
// match/cut pair, including false results that make a state-guarded constraint
// pass.
type CanonicalConstraintStateEvaluation struct {
	Clause        string `json:"clause"`
	MatchDigest   string `json:"match_digest"`
	WitnessDigest string `json:"witness_digest"`
	GuardResult   bool   `json:"guard_result"`
}

// CanonicalConstraintReport is a versioned, replay-comparable constraint
// result tied to exact constraint and poset digests.
type CanonicalConstraintReport struct {
	Format                string                               `json:"format"`
	ConstraintDigest      string                               `json:"constraint_digest"`
	PosetDigest           string                               `json:"poset_digest"`
	EvaluationPosetDigest string                               `json:"evaluation_poset_digest,omitempty"`
	Passed                bool                                 `json:"passed"`
	Violations            []CanonicalConstraintViolation       `json:"violations"`
	StateEvaluations      []CanonicalConstraintStateEvaluation `json:"state_evaluations,omitempty"`
}

// EvaluateCanonical evaluates a constraint and constructs its canonical audit
// report. No wall time, host scheduling, or map iteration enters the result.
func (c *Constraint) EvaluateCanonical(poset pattern.PosetReader) (CanonicalConstraintReport, error) {
	return c.EvaluateCanonicalWithState(poset, nil)
}

// EvaluateCanonicalWithState binds state-dependent decisions to supplied cut
// witnesses while retaining the same exact poset and constraint identities.
func (c *Constraint) EvaluateCanonicalWithState(
	poset pattern.PosetReader,
	stateWitnesses []ClauseStateWitnesses,
) (CanonicalConstraintReport, error) {
	return c.evaluateCanonicalWithPosetDigest(poset, "", stateWitnesses)
}

// evaluateCanonicalWithPosetDigest is EvaluateCanonicalWithState for a caller
// that already holds the poset's semantic digest (a constraint set evaluating
// many members against one poset). An empty posetDigest is computed here. The
// digest is a pure function of the poset, so reusing it cannot change the
// report; it only avoids re-encoding the whole poset once per constraint.
// Likewise, when the constraint declares no filter and no alphabet, the
// evaluation view is the poset itself and EvaluationPosetDigest equals
// PosetDigest by construction, so it is not recomputed.
func (c *Constraint) evaluateCanonicalWithPosetDigest(
	poset pattern.PosetReader,
	posetDigest string,
	stateWitnesses []ClauseStateWitnesses,
) (CanonicalConstraintReport, error) {
	constraintDigest, err := c.DeterministicDigest()
	if err != nil {
		return CanonicalConstraintReport{}, err
	}
	if poset == nil {
		return CanonicalConstraintReport{}, fmt.Errorf("%w: poset is nil", ErrConstraintEvaluation)
	}
	if posetDigest == "" {
		posetDigest, err = semanticPosetDigest(poset)
		if err != nil {
			return CanonicalConstraintReport{}, err
		}
	}
	view, err := c.evaluationView(poset)
	if err != nil {
		return CanonicalConstraintReport{}, err
	}
	evaluationDigest := posetDigest
	if c.hasEvaluationFilter() {
		evaluationDigest, err = semanticPosetDigest(view)
		if err != nil {
			return CanonicalConstraintReport{}, err
		}
	}
	violations, err := c.checkDeterministicView(view, stateWitnesses)
	if err != nil {
		return CanonicalConstraintReport{}, err
	}
	stateEvaluations, err := c.stateEvaluations(view, stateWitnesses)
	if err != nil {
		return CanonicalConstraintReport{}, err
	}
	report := CanonicalConstraintReport{
		Format: CanonicalConstraintReportFormat, ConstraintDigest: constraintDigest,
		PosetDigest: posetDigest, EvaluationPosetDigest: evaluationDigest,
		Passed:           len(violations) == 0,
		Violations:       make([]CanonicalConstraintViolation, 0, len(violations)),
		StateEvaluations: make([]CanonicalConstraintStateEvaluation, 0, len(stateEvaluations)),
	}
	for _, violation := range violations {
		canonical, err := canonicalizeViolation(violation, view)
		if err != nil {
			return CanonicalConstraintReport{}, err
		}
		report.Violations = append(report.Violations, canonical)
	}
	for _, evaluation := range stateEvaluations {
		report.StateEvaluations = append(report.StateEvaluations, CanonicalConstraintStateEvaluation{
			Clause: evaluation.Clause, MatchDigest: "sha256:" + evaluation.MatchDigest,
			WitnessDigest: evaluation.WitnessDigest, GuardResult: evaluation.GuardResult,
		})
	}
	return normalizeConstraintReport(report)
}

// CanonicalReportFromViolations constructs the same replayable report envelope
// used by pattern constraints for a different closed semantic checker. The
// caller supplies its canonical model digest and violations over the exact
// evaluation poset; no host callback or unrecorded state enters the result.
func CanonicalReportFromViolations(
	constraintDigest string,
	poset pattern.PosetReader,
	violations []ConstraintViolation,
) (CanonicalConstraintReport, error) {
	if !validDigest(constraintDigest) {
		return CanonicalConstraintReport{}, fmt.Errorf("%w: malformed constraint digest", ErrInvalidConstraintReport)
	}
	if poset == nil {
		return CanonicalConstraintReport{}, fmt.Errorf("%w: poset is nil", ErrConstraintEvaluation)
	}
	posetDigest, err := semanticPosetDigest(poset)
	if err != nil {
		return CanonicalConstraintReport{}, err
	}
	report := CanonicalConstraintReport{
		Format: CanonicalConstraintReportFormat, ConstraintDigest: constraintDigest,
		PosetDigest: posetDigest, EvaluationPosetDigest: posetDigest,
		Passed:           len(violations) == 0,
		Violations:       make([]CanonicalConstraintViolation, 0, len(violations)),
		StateEvaluations: []CanonicalConstraintStateEvaluation{},
	}
	for _, violation := range violations {
		canonical, err := canonicalizeViolation(violation, poset)
		if err != nil {
			return CanonicalConstraintReport{}, err
		}
		report.Violations = append(report.Violations, canonical)
	}
	return normalizeConstraintReport(report)
}

func canonicalizeViolation(violation ConstraintViolation, poset pattern.PosetReader) (CanonicalConstraintViolation, error) {
	eventIDs := make([]string, 0, len(violation.MatchedEvents))
	seen := make(map[gorapide.EventID]bool, len(violation.MatchedEvents))
	for _, event := range violation.MatchedEvents {
		if event == nil || event.ID == "" {
			return CanonicalConstraintViolation{}, fmt.Errorf("%w: violation contains nil or unidentified event", ErrInvalidConstraintReport)
		}
		if !seen[event.ID] {
			seen[event.ID] = true
			eventIDs = append(eventIDs, string(event.ID))
		}
	}
	sort.Strings(eventIDs)
	bindings, err := canonicalizeViolationBindings(violation.Bindings)
	if err != nil {
		return CanonicalConstraintViolation{}, err
	}
	relations := make([]CanonicalCausalRelation, 0)
	for _, before := range eventIDs {
		for _, after := range eventIDs {
			if poset.IsCausallyBefore(gorapide.EventID(before), gorapide.EventID(after)) {
				relations = append(relations, CanonicalCausalRelation{Before: before, After: after})
			}
		}
	}
	return CanonicalConstraintViolation{
		Constraint: violation.Constraint, Clause: violation.Clause,
		Kind: violation.Kind.String(), Message: violation.Message, Severity: violation.Severity,
		Events: eventIDs, Bindings: bindings, Causality: relations,
		StateWitnesses: append([]string{}, violation.StateWitnesses...),
	}, nil
}

type semanticPosetReader interface {
	SemanticDigest() (string, error)
}

func semanticPosetDigest(poset pattern.PosetReader) (string, error) {
	digester, ok := poset.(semanticPosetReader)
	if !ok {
		return "", fmt.Errorf("%w: poset reader %T has no semantic digest", ErrConstraintEvaluation, poset)
	}
	return digester.SemanticDigest()
}

func canonicalizeViolationBindings(bindings pattern.Bindings) ([]pattern.CanonicalBinding, error) {
	encoded, err := pattern.MarshalCanonicalMatches([]pattern.MatchResult{{Bindings: bindings}})
	if err != nil {
		return nil, err
	}
	var matchSet pattern.CanonicalMatchSet
	if err := json.Unmarshal(encoded, &matchSet); err != nil {
		return nil, err
	}
	return matchSet.Matches[0].Bindings, nil
}

// MarshalCanonical returns the exact canonical JSON encoding of the report.
func (report CanonicalConstraintReport) MarshalCanonical() ([]byte, error) {
	normalized, err := normalizeConstraintReport(report)
	if err != nil {
		return nil, err
	}
	return json.Marshal(normalized)
}

// SemanticDigest returns the SHA-256 identity of the canonical report.
func (report CanonicalConstraintReport) SemanticDigest() (string, error) {
	encoded, err := report.MarshalCanonical()
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

// ParseCanonicalConstraintReport accepts only the exact canonical byte form.
func ParseCanonicalConstraintReport(data []byte) (CanonicalConstraintReport, error) {
	var report CanonicalConstraintReport
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&report); err != nil {
		return CanonicalConstraintReport{}, fmt.Errorf("%w: %v", ErrInvalidConstraintReport, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return CanonicalConstraintReport{}, fmt.Errorf("%w: trailing JSON content", ErrInvalidConstraintReport)
	}
	normalized, err := normalizeConstraintReport(report)
	if err != nil {
		return CanonicalConstraintReport{}, err
	}
	reencoded, err := json.Marshal(normalized)
	if err != nil || !bytes.Equal(reencoded, data) {
		return CanonicalConstraintReport{}, fmt.Errorf("%w: input is not the canonical byte encoding", ErrInvalidConstraintReport)
	}
	return normalized, nil
}

func normalizeConstraintReport(report CanonicalConstraintReport) (CanonicalConstraintReport, error) {
	if report.Format != CanonicalConstraintReportFormat &&
		report.Format != legacyCanonicalConstraintReportFormatV4 &&
		report.Format != legacyCanonicalConstraintReportFormatV3 &&
		report.Format != legacyCanonicalConstraintReportFormatV2 &&
		report.Format != legacyCanonicalConstraintReportFormatV1 {
		return CanonicalConstraintReport{}, fmt.Errorf("%w: format %q", ErrInvalidConstraintReport, report.Format)
	}
	if !validDigest(report.ConstraintDigest) || !validDigest(report.PosetDigest) {
		return CanonicalConstraintReport{}, fmt.Errorf("%w: malformed semantic digest", ErrInvalidConstraintReport)
	}
	if report.Format == CanonicalConstraintReportFormat || report.Format == legacyCanonicalConstraintReportFormatV4 ||
		report.Format == legacyCanonicalConstraintReportFormatV3 ||
		report.Format == legacyCanonicalConstraintReportFormatV2 {
		if !validDigest(report.EvaluationPosetDigest) {
			return CanonicalConstraintReport{}, fmt.Errorf("%w: malformed evaluation-poset digest", ErrInvalidConstraintReport)
		}
	} else if report.EvaluationPosetDigest != "" {
		return CanonicalConstraintReport{}, fmt.Errorf("%w: legacy report contains an evaluation-poset digest", ErrInvalidConstraintReport)
	}
	result := report
	result.StateEvaluations = append([]CanonicalConstraintStateEvaluation{}, report.StateEvaluations...)
	if report.Format != CanonicalConstraintReportFormat && report.Format != legacyCanonicalConstraintReportFormatV4 &&
		len(result.StateEvaluations) != 0 {
		return CanonicalConstraintReport{}, fmt.Errorf("%w: legacy report contains state evaluations", ErrInvalidConstraintReport)
	}
	sort.Slice(result.StateEvaluations, func(i, j int) bool {
		left, _ := json.Marshal(result.StateEvaluations[i])
		right, _ := json.Marshal(result.StateEvaluations[j])
		return string(left) < string(right)
	})
	evaluationWitnesses := make(map[string]bool, len(result.StateEvaluations))
	evaluationIdentities := make(map[string]bool, len(result.StateEvaluations))
	previousEvaluation := ""
	for index, evaluation := range result.StateEvaluations {
		if evaluation.Clause == "" || !validDigest(evaluation.MatchDigest) || !validDigest(evaluation.WitnessDigest) {
			return CanonicalConstraintReport{}, fmt.Errorf("%w: malformed state evaluation %d", ErrInvalidConstraintReport, index)
		}
		encoded, _ := json.Marshal(evaluation)
		key := string(encoded)
		if index > 0 && key == previousEvaluation {
			return CanonicalConstraintReport{}, fmt.Errorf("%w: duplicate state evaluation", ErrInvalidConstraintReport)
		}
		previousEvaluation = key
		evaluationKey := evaluation.Clause + "\x00" + evaluation.MatchDigest + "\x00" + evaluation.WitnessDigest
		if evaluationIdentities[evaluationKey] {
			return CanonicalConstraintReport{}, fmt.Errorf("%w: duplicate state evaluation identity", ErrInvalidConstraintReport)
		}
		evaluationIdentities[evaluationKey] = true
		evaluationWitnesses[evaluationKey] = evaluationWitnesses[evaluationKey] || evaluation.GuardResult
	}
	result.Violations = make([]CanonicalConstraintViolation, len(report.Violations))
	copy(result.Violations, report.Violations)
	for i := range result.Violations {
		violation := &result.Violations[i]
		if violation.Constraint == "" || violation.Clause == "" ||
			(violation.Kind != MustMatch.String() && violation.Kind != MustNotMatch.String() && violation.Kind != MustNever.String()) {
			return CanonicalConstraintReport{}, fmt.Errorf("%w: malformed violation %d", ErrInvalidConstraintReport, i)
		}
		if report.Format != CanonicalConstraintReportFormat && violation.Kind == MustNotMatch.String() {
			return CanonicalConstraintReport{}, fmt.Errorf("%w: legacy report contains a negative match violation", ErrInvalidConstraintReport)
		}
		violation.Events = append([]string{}, violation.Events...)
		sort.Strings(violation.Events)
		violation.Events = uniqueStrings(violation.Events)
		bindings := append([]pattern.CanonicalBinding{}, violation.Bindings...)
		for j := range bindings {
			if bindings[j].Placeholder == "" {
				return CanonicalConstraintReport{}, fmt.Errorf("%w: empty placeholder", ErrInvalidConstraintReport)
			}
			normalized, err := normalizeCanonicalValue(bindings[j].Value)
			if err != nil {
				return CanonicalConstraintReport{}, err
			}
			bindings[j].Value = normalized
		}
		sort.Slice(bindings, func(a, b int) bool { return bindings[a].Placeholder < bindings[b].Placeholder })
		for j := 1; j < len(bindings); j++ {
			if bindings[j-1].Placeholder == bindings[j].Placeholder {
				return CanonicalConstraintReport{}, fmt.Errorf("%w: duplicate placeholder %q", ErrInvalidConstraintReport, bindings[j].Placeholder)
			}
		}
		violation.Bindings = bindings
		violation.StateWitnesses = append([]string{}, violation.StateWitnesses...)
		sort.Strings(violation.StateWitnesses)
		violation.StateWitnesses = uniqueStrings(violation.StateWitnesses)
		for _, digest := range violation.StateWitnesses {
			if !validDigest(digest) {
				return CanonicalConstraintReport{}, fmt.Errorf("%w: malformed state witness digest", ErrInvalidConstraintReport)
			}
		}
		if report.Format != CanonicalConstraintReportFormat && report.Format != legacyCanonicalConstraintReportFormatV4 &&
			report.Format != legacyCanonicalConstraintReportFormatV3 &&
			len(violation.StateWitnesses) != 0 {
			return CanonicalConstraintReport{}, fmt.Errorf("%w: legacy report contains state witnesses", ErrInvalidConstraintReport)
		}
		if report.Format == CanonicalConstraintReportFormat || report.Format == legacyCanonicalConstraintReportFormatV4 {
			matchDigest, err := canonicalViolationMatchDigest(*violation)
			if err != nil {
				return CanonicalConstraintReport{}, err
			}
			for _, digest := range violation.StateWitnesses {
				guardTrue, exists := evaluationWitnesses[violation.Clause+"\x00"+matchDigest+"\x00"+digest]
				if !exists || (violation.Kind == MustNever.String() || violation.Kind == MustNotMatch.String()) && !guardTrue {
					return CanonicalConstraintReport{}, fmt.Errorf("%w: violation references an unrelated state evaluation", ErrInvalidConstraintReport)
				}
			}
		}
		eventSet := make(map[string]bool, len(violation.Events))
		for _, id := range violation.Events {
			if id == "" {
				return CanonicalConstraintReport{}, fmt.Errorf("%w: empty witness event ID", ErrInvalidConstraintReport)
			}
			eventSet[id] = true
		}
		violation.Causality = append([]CanonicalCausalRelation{}, violation.Causality...)
		sort.Slice(violation.Causality, func(a, b int) bool {
			return causalRelationKey(violation.Causality[a]) < causalRelationKey(violation.Causality[b])
		})
		previous := ""
		for j, relation := range violation.Causality {
			key := causalRelationKey(relation)
			if relation.Before == relation.After || !eventSet[relation.Before] || !eventSet[relation.After] || (j > 0 && key == previous) {
				return CanonicalConstraintReport{}, fmt.Errorf("%w: malformed causal witness", ErrInvalidConstraintReport)
			}
			previous = key
		}
	}
	sort.Slice(result.Violations, func(i, j int) bool {
		left, _ := json.Marshal(result.Violations[i])
		right, _ := json.Marshal(result.Violations[j])
		return string(left) < string(right)
	})
	if report.Passed != (len(result.Violations) == 0) {
		return CanonicalConstraintReport{}, fmt.Errorf("%w: passed flag conflicts with violations", ErrInvalidConstraintReport)
	}
	return result, nil
}

func canonicalViolationMatchDigest(violation CanonicalConstraintViolation) (string, error) {
	encoded, err := json.Marshal(pattern.CanonicalMatchSet{
		Format: pattern.CanonicalMatchSetFormat,
		Matches: []pattern.CanonicalMatch{{
			Events:   append([]string{}, violation.Events...),
			Bindings: append([]pattern.CanonicalBinding{}, violation.Bindings...),
		}},
	})
	if err != nil {
		return "", fmt.Errorf("%w: match digest: %v", ErrInvalidConstraintReport, err)
	}
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func normalizeCanonicalValue(value gorapide.CanonicalValue) (gorapide.CanonicalValue, error) {
	decoded, err := gorapide.DecodeCanonicalParameters([]gorapide.CanonicalParameter{{Name: "value", Value: value}})
	if err != nil {
		return gorapide.CanonicalValue{}, fmt.Errorf("%w: %v", ErrInvalidConstraintReport, err)
	}
	encoded, err := gorapide.CanonicalizeParameters(decoded)
	if err != nil || len(encoded) != 1 || !reflect.DeepEqual(encoded[0].Value, value) {
		return gorapide.CanonicalValue{}, fmt.Errorf("%w: noncanonical binding value", ErrInvalidConstraintReport)
	}
	return encoded[0].Value, nil
}

func validDigest(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != len("sha256:")+64 {
		return false
	}
	decoded, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil && len(decoded) == sha256.Size && value == strings.ToLower(value)
}

func uniqueStrings(values []string) []string {
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

func causalRelationKey(relation CanonicalCausalRelation) string {
	return fmt.Sprintf("%d:%s%d:%s", len(relation.Before), relation.Before, len(relation.After), relation.After)
}

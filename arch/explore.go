package arch

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

const ExplorationArtifactFormat = "gorapide.exploration-artifact.v1"

var ErrInvalidExplorationLimits = errors.New("invalid deterministic exploration limits")

// ExplorationLimits are explicit semantic search bounds. MaxChoiceDepth counts
// choices beyond any fixed decisions already present in the base journal.
type ExplorationLimits struct {
	MaxExecutions  uint64 `json:"max_executions"`
	MaxChoiceDepth uint64 `json:"max_choice_depth"`
}

// ExploredComputation is one unique permitted semantic poset and a schedule
// witness that reproduces it. Result is excluded from canonical serialization.
type ExploredComputation struct {
	PosetDigest    string           `json:"poset_digest"`
	ArtifactDigest string           `json:"artifact_digest"`
	Schedule       []ChoiceDecision `json:"schedule"`
	Result         *ExecutionResult `json:"-"`
}

// ExplorationResult is a canonically ordered, poset-deduplicated search result.
// Complete is false when an explicit execution or choice-depth bound truncated
// a reachable branch.
type ExplorationResult struct {
	Format            string                `json:"format"`
	Engine            string                `json:"engine"`
	Profile           string                `json:"profile"`
	ModelDigest       string                `json:"model_digest"`
	BaseJournalDigest string                `json:"base_journal_digest"`
	Limits            ExplorationLimits     `json:"limits"`
	Complete          bool                  `json:"complete"`
	Executions        uint64                `json:"executions"`
	Computations      []ExploredComputation `json:"computations"`
}

type canonicalExploredComputation struct {
	PosetDigest    string           `json:"poset_digest"`
	ArtifactDigest string           `json:"artifact_digest"`
	Schedule       []ChoiceDecision `json:"schedule"`
}

type canonicalExplorationResult struct {
	Format            string                         `json:"format"`
	Engine            string                         `json:"engine"`
	Profile           string                         `json:"profile"`
	ModelDigest       string                         `json:"model_digest"`
	BaseJournalDigest string                         `json:"base_journal_digest"`
	Limits            ExplorationLimits              `json:"limits"`
	Complete          bool                           `json:"complete"`
	Executions        uint64                         `json:"executions"`
	Computations      []canonicalExploredComputation `json:"computations"`
}

// MarshalCanonical returns byte-identical exploration metadata. Semantic posets
// remain available through each computation's Result field.
func (result *ExplorationResult) MarshalCanonical() ([]byte, error) {
	if result == nil {
		return nil, fmt.Errorf("arch.ExplorationResult.MarshalCanonical: result is nil")
	}
	canonical := canonicalExplorationResult{
		Format: result.Format, Engine: result.Engine, Profile: result.Profile,
		ModelDigest: result.ModelDigest, BaseJournalDigest: result.BaseJournalDigest,
		Limits: result.Limits, Complete: result.Complete, Executions: result.Executions,
		Computations: make([]canonicalExploredComputation, 0, len(result.Computations)),
	}
	for _, computation := range result.Computations {
		schedule, err := canonicalChoiceSchedule(computation.Schedule)
		if err != nil {
			return nil, err
		}
		canonical.Computations = append(canonical.Computations, canonicalExploredComputation{
			PosetDigest: computation.PosetDigest, ArtifactDigest: computation.ArtifactDigest,
			Schedule: schedule,
		})
	}
	sort.Slice(canonical.Computations, func(i, j int) bool {
		if canonical.Computations[i].PosetDigest != canonical.Computations[j].PosetDigest {
			return canonical.Computations[i].PosetDigest < canonical.Computations[j].PosetDigest
		}
		return canonical.Computations[i].ArtifactDigest < canonical.Computations[j].ArtifactDigest
	})
	return json.Marshal(canonical)
}

// SemanticDigest returns the digest of the canonical exploration artifact.
func (result *ExplorationResult) SemanticDigest() (string, error) {
	encoded, err := result.MarshalCanonical()
	if err != nil {
		return "", err
	}
	return digestBytes(encoded), nil
}

type explorationNode struct {
	schedule []ChoiceDecision
	depth    uint64
}

// ExploreDeterministic enumerates permitted choices by rerunning the pure
// deterministic kernel with explicit schedules. Search order is canonical;
// computations that produce the same semantic poset are collapsed.
func (a *Architecture) ExploreDeterministic(journal ExecutionJournal, limits ExplorationLimits) (*ExplorationResult, error) {
	prepared, err := a.PrepareDeterministic()
	if err != nil {
		return nil, err
	}
	return prepared.ExploreDeterministic(journal, limits)
}

func exploreDeterministicModel(
	model *deterministicModel,
	journal ExecutionJournal,
	limits ExplorationLimits,
) (*ExplorationResult, error) {
	if limits.MaxExecutions == 0 || limits.MaxChoiceDepth == 0 {
		return nil, fmt.Errorf("%w: both limits must be greater than zero", ErrInvalidExplorationLimits)
	}
	normalized, err := normalizeJournal(journal)
	if err != nil {
		return nil, err
	}
	if normalized.ModelDigest != model.digest {
		return nil, fmt.Errorf("%w: journal=%s architecture=%s", ErrModelDigestMismatch, normalized.ModelDigest, model.digest)
	}
	baseDigest, err := normalized.SemanticDigest()
	if err != nil {
		return nil, err
	}

	queue := []explorationNode{{schedule: append([]ChoiceDecision{}, normalized.Choices...)}}
	queued := make(map[string]bool)
	baseKey, err := choiceScheduleKey(queue[0].schedule)
	if err != nil {
		return nil, err
	}
	queued[baseKey] = true
	byPoset := make(map[string]ExploredComputation)
	complete := true
	var executions uint64

	for len(queue) > 0 {
		if executions >= limits.MaxExecutions {
			complete = false
			break
		}
		node := queue[0]
		queue = queue[1:]
		runJournal := normalized
		runJournal.Choices = append([]ChoiceDecision{}, node.schedule...)
		run, err := executeDeterministicModel(model, runJournal)
		if err != nil {
			return nil, err
		}
		executions++

		scheduled := make(map[string]bool, len(node.schedule))
		for _, decision := range node.schedule {
			scheduled[decision.Point] = true
		}
		var unresolved *ChoiceResolution
		for i := range run.Choices {
			if !scheduled[run.Choices[i].Point] {
				unresolved = &run.Choices[i]
				break
			}
		}
		if unresolved != nil && node.depth < limits.MaxChoiceDepth {
			for _, option := range unresolved.Options {
				next, err := addChoiceDecision(node.schedule, ChoiceDecision{
					Point: unresolved.Point, Selection: option,
				})
				if err != nil {
					return nil, err
				}
				key, err := choiceScheduleKey(next)
				if err != nil {
					return nil, err
				}
				if queued[key] {
					continue
				}
				queued[key] = true
				queue = append(queue, explorationNode{schedule: next, depth: node.depth + 1})
			}
			continue
		}
		if unresolved != nil {
			complete = false
		}
		posetDigest, err := run.Poset.SemanticDigest()
		if err != nil {
			return nil, err
		}
		artifactDigest, err := run.ArtifactDigest()
		if err != nil {
			return nil, err
		}
		schedule, err := canonicalChoiceSchedule(node.schedule)
		if err != nil {
			return nil, err
		}
		current := ExploredComputation{
			PosetDigest: posetDigest, ArtifactDigest: artifactDigest,
			Schedule: schedule, Result: run,
		}
		prior, exists := byPoset[posetDigest]
		if !exists || choiceScheduleLess(current.Schedule, prior.Schedule) {
			byPoset[posetDigest] = current
		}
	}

	computations := make([]ExploredComputation, 0, len(byPoset))
	for _, computation := range byPoset {
		computations = append(computations, computation)
	}
	sort.Slice(computations, func(i, j int) bool {
		return computations[i].PosetDigest < computations[j].PosetDigest
	})
	return &ExplorationResult{
		Format: ExplorationArtifactFormat, Engine: DeterministicEngineVersion,
		Profile: normalized.Profile, ModelDigest: model.digest,
		BaseJournalDigest: baseDigest, Limits: limits,
		Complete: complete, Executions: executions, Computations: computations,
	}, nil
}

func canonicalChoiceSchedule(schedule []ChoiceDecision) ([]ChoiceDecision, error) {
	result := append([]ChoiceDecision{}, schedule...)
	seen := make(map[string]bool, len(result))
	for _, decision := range result {
		if decision.Point == "" || decision.Selection == "" {
			return nil, fmt.Errorf("%w: empty choice point or selection", ErrInvalidExecutionJournal)
		}
		if seen[decision.Point] {
			return nil, fmt.Errorf("%w: duplicate choice point %q", ErrInvalidExecutionJournal, decision.Point)
		}
		seen[decision.Point] = true
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Point < result[j].Point })
	return result, nil
}

func addChoiceDecision(schedule []ChoiceDecision, decision ChoiceDecision) ([]ChoiceDecision, error) {
	next := append(append([]ChoiceDecision{}, schedule...), decision)
	return canonicalChoiceSchedule(next)
}

func choiceScheduleKey(schedule []ChoiceDecision) (string, error) {
	canonical, err := canonicalChoiceSchedule(schedule)
	if err != nil {
		return "", err
	}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func choiceScheduleLess(left, right []ChoiceDecision) bool {
	leftEncoded, _ := json.Marshal(left)
	rightEncoded, _ := json.Marshal(right)
	return bytes.Compare(leftEncoded, rightEncoded) < 0
}

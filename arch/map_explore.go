package arch

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/ShaneDolphin/gorapide"
)

const MapExplorationArtifactFormat = "gorapide.map-exploration-artifact.v1"

// MapExploredComputation is one unique permitted range poset and the exact
// generator-choice schedule that reproduces it. Result is queryable runtime
// state and is excluded from canonical serialization.
type MapExploredComputation struct {
	RangePosetDigest string              `json:"range_poset_digest"`
	ArtifactDigest   string              `json:"artifact_digest"`
	Schedule         []ChoiceDecision    `json:"schedule"`
	Result           *MapExecutionResult `json:"-"`
}

// MapExplorationResult is a canonical, range-poset-deduplicated enumeration
// of finite language-permitted generator choices.
type MapExplorationResult struct {
	Format            string                   `json:"format"`
	Engine            string                   `json:"engine"`
	Profile           string                   `json:"profile"`
	ModelDigest       string                   `json:"model_digest"`
	DomainPosetDigest string                   `json:"domain_poset_digest"`
	BaseRequestDigest string                   `json:"base_request_digest"`
	Limits            ExplorationLimits        `json:"limits"`
	Complete          bool                     `json:"complete"`
	Executions        uint64                   `json:"executions"`
	Computations      []MapExploredComputation `json:"computations"`
}

type canonicalMapExploredComputation struct {
	RangePosetDigest string           `json:"range_poset_digest"`
	ArtifactDigest   string           `json:"artifact_digest"`
	Schedule         []ChoiceDecision `json:"schedule"`
}

type canonicalMapExplorationResult struct {
	Format            string                            `json:"format"`
	Engine            string                            `json:"engine"`
	Profile           string                            `json:"profile"`
	ModelDigest       string                            `json:"model_digest"`
	DomainPosetDigest string                            `json:"domain_poset_digest"`
	BaseRequestDigest string                            `json:"base_request_digest"`
	Limits            ExplorationLimits                 `json:"limits"`
	Complete          bool                              `json:"complete"`
	Executions        uint64                            `json:"executions"`
	Computations      []canonicalMapExploredComputation `json:"computations"`
}

// MarshalCanonical returns byte-identical bounded exploration metadata.
func (result *MapExplorationResult) MarshalCanonical() ([]byte, error) {
	if result == nil {
		return nil, fmt.Errorf("arch.MapExplorationResult.MarshalCanonical: result is nil")
	}
	canonical := canonicalMapExplorationResult{
		Format: result.Format, Engine: result.Engine, Profile: result.Profile,
		ModelDigest: result.ModelDigest, DomainPosetDigest: result.DomainPosetDigest,
		BaseRequestDigest: result.BaseRequestDigest, Limits: result.Limits,
		Complete: result.Complete, Executions: result.Executions,
		Computations: make([]canonicalMapExploredComputation, 0, len(result.Computations)),
	}
	for _, computation := range result.Computations {
		schedule, err := canonicalChoiceSchedule(computation.Schedule)
		if err != nil {
			return nil, err
		}
		canonical.Computations = append(canonical.Computations, canonicalMapExploredComputation{
			RangePosetDigest: computation.RangePosetDigest,
			ArtifactDigest:   computation.ArtifactDigest, Schedule: schedule,
		})
	}
	sort.Slice(canonical.Computations, func(i, j int) bool {
		if canonical.Computations[i].RangePosetDigest != canonical.Computations[j].RangePosetDigest {
			return canonical.Computations[i].RangePosetDigest < canonical.Computations[j].RangePosetDigest
		}
		return canonical.Computations[i].ArtifactDigest < canonical.Computations[j].ArtifactDigest
	})
	return json.Marshal(canonical)
}

// SemanticDigest returns the identity of the canonical map exploration
// artifact.
func (result *MapExplorationResult) SemanticDigest() (string, error) {
	encoded, err := result.MarshalCanonical()
	if err != nil {
		return "", err
	}
	return digestMapBytes(encoded), nil
}

// ExploreDeterministic enumerates finite permitted map-generator choices by
// rerunning the immutable kernel with explicit schedules.
func (mapping *EventPatternMap) ExploreDeterministic(
	domain *gorapide.Poset,
	request MapExecutionRequest,
	limits ExplorationLimits,
) (*MapExplorationResult, error) {
	prepared, err := mapping.PrepareDeterministic()
	if err != nil {
		return nil, err
	}
	return prepared.ExploreDeterministic(domain, request, limits)
}

// ExploreDeterministic enumerates choices against this prepared map snapshot.
func (prepared *PreparedEventPatternMap) ExploreDeterministic(
	domain *gorapide.Poset,
	request MapExecutionRequest,
	limits ExplorationLimits,
) (*MapExplorationResult, error) {
	model, err := prepared.checkedModel()
	if err != nil {
		return nil, err
	}
	return exploreDeterministicEventPatternMap(model, domain, request, limits)
}

func exploreDeterministicEventPatternMap(
	model *deterministicEventPatternMap,
	domain *gorapide.Poset,
	request MapExecutionRequest,
	limits ExplorationLimits,
) (*MapExplorationResult, error) {
	if domain == nil {
		return nil, fmt.Errorf("%w: domain poset is nil", ErrInvalidEventPatternMap)
	}
	if request.Limits.MaxFirings == 0 || request.Limits.MaxRangeEvents == 0 {
		return nil, fmt.Errorf("%w: map limits must be greater than zero", ErrInvalidEventPatternMap)
	}
	if limits.MaxExecutions == 0 || limits.MaxChoiceDepth == 0 {
		return nil, fmt.Errorf("%w: both limits must be greater than zero", ErrInvalidExplorationLimits)
	}
	baseSchedule, err := canonicalChoiceSchedule(request.Choices)
	if err != nil {
		return nil, err
	}
	domainDigest, err := domain.SemanticDigest()
	if err != nil {
		return nil, err
	}
	baseBytes, err := json.Marshal(struct {
		Model     string             `json:"model"`
		Domain    string             `json:"domain"`
		Execution MapExecutionLimits `json:"execution"`
		Choices   []ChoiceDecision   `json:"choices"`
	}{Model: model.digest, Domain: domainDigest, Execution: request.Limits, Choices: baseSchedule})
	if err != nil {
		return nil, err
	}

	queue := []explorationNode{{schedule: baseSchedule}}
	queued := make(map[string]bool)
	baseKey, err := choiceScheduleKey(baseSchedule)
	if err != nil {
		return nil, err
	}
	queued[baseKey] = true
	byPoset := make(map[string]MapExploredComputation)
	complete := true
	var executions uint64

	for len(queue) > 0 {
		if executions >= limits.MaxExecutions {
			complete = false
			break
		}
		node := queue[0]
		queue = queue[1:]
		runRequest := request
		runRequest.Choices = append([]ChoiceDecision{}, node.schedule...)
		run, err := executeDeterministicEventPatternMap(model, domain, runRequest)
		if err != nil {
			return nil, err
		}
		executions++

		scheduled := make(map[string]bool, len(node.schedule))
		for _, decision := range node.schedule {
			scheduled[decision.Point] = true
		}
		var unresolved *ChoiceResolution
		for index := range run.Artifact.Choices {
			if !scheduled[run.Artifact.Choices[index].Point] {
				unresolved = &run.Artifact.Choices[index]
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
		posetDigest, err := run.Range.SemanticDigest()
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
		current := MapExploredComputation{
			RangePosetDigest: posetDigest, ArtifactDigest: artifactDigest,
			Schedule: schedule, Result: run,
		}
		prior, exists := byPoset[posetDigest]
		if !exists || choiceScheduleLess(current.Schedule, prior.Schedule) {
			byPoset[posetDigest] = current
		}
	}

	computations := make([]MapExploredComputation, 0, len(byPoset))
	for _, computation := range byPoset {
		computations = append(computations, computation)
	}
	sort.Slice(computations, func(i, j int) bool {
		return computations[i].RangePosetDigest < computations[j].RangePosetDigest
	})
	return &MapExplorationResult{
		Format: MapExplorationArtifactFormat, Engine: DeterministicMapEngineVersion,
		Profile: CompatibilityProfile, ModelDigest: model.digest,
		DomainPosetDigest: domainDigest, BaseRequestDigest: digestMapBytes(baseBytes),
		Limits: limits, Complete: complete, Executions: executions,
		Computations: computations,
	}, nil
}

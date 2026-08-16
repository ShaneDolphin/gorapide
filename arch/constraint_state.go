package arch

import (
	"fmt"
	"sort"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/constraint"
	"github.com/ShaneDolphin/gorapide/pattern"
)

// constraintStateWitnessKernel derives all state observations from the final
// augmented computation. The journal-bound budget is shared across every
// architecture and module constraint in one execution.
type constraintStateWitnessKernel struct {
	poset                  *gorapide.Poset
	operations             []StateOperationRecord
	computation            *AugmentedComputation
	remainingCuts          uint64
	maxOptionalOccurrences uint64
}

func newConstraintStateWitnessKernel(
	poset *gorapide.Poset,
	operations []StateOperationRecord,
	limits ExecutionLimits,
) *constraintStateWitnessKernel {
	return &constraintStateWitnessKernel{
		poset: poset, operations: append([]StateOperationRecord(nil), operations...),
		remainingCuts:          limits.MaxConsistentCuts,
		maxOptionalOccurrences: limits.MaxOptionalCutOccurrences,
	}
}

func (kernel *constraintStateWitnessKernel) derive(
	set *constraint.ConstraintSet,
	view pattern.PosetReader,
) ([]constraint.ClauseStateWitnesses, error) {
	candidates, err := set.StateWitnessCandidates(view)
	if err != nil {
		return nil, err
	}
	result := make([]constraint.ClauseStateWitnesses, 0, len(candidates))
	for _, clause := range candidates {
		entry := constraint.ClauseStateWitnesses{
			Constraint: clause.Constraint, Clause: clause.Clause,
			Witnesses: []pattern.MatchStateWitness{},
		}
		for _, match := range clause.Matches {
			canonical, err := pattern.CanonicalizeMatch(match)
			if err != nil {
				return nil, err
			}
			if len(canonical.Events) == 0 {
				return nil, fmt.Errorf("%w: constraint %q clause %q has a state guard over an empty match", ErrUnsupportedDeterministicModel, clause.Constraint, clause.Clause)
			}
			matchDigest, err := pattern.SemanticDigestMatches([]pattern.MatchResult{match})
			if err != nil {
				return nil, err
			}
			computation, err := kernel.augmentedComputation()
			if err != nil {
				return nil, err
			}
			if kernel.remainingCuts == 0 {
				return nil, fmt.Errorf("%w: max_consistent_cuts exhausted", ErrConsistentCutLimit)
			}
			cuts, err := computation.ConsistentCutStateWitnesses(canonical.Events, ConsistentCutLimits{
				MaxCuts: kernel.remainingCuts, MaxOptionalOccurrences: kernel.maxOptionalOccurrences,
			})
			if err != nil {
				return nil, fmt.Errorf("constraint %q clause %q match %s: %w", clause.Constraint, clause.Clause, matchDigest, err)
			}
			if len(cuts) == 0 {
				return nil, fmt.Errorf("%w: constraint %q clause %q match %s has no anchored consistent cut", ErrInvalidAugmentedComputation, clause.Constraint, clause.Clause, matchDigest)
			}
			kernel.remainingCuts -= uint64(len(cuts))
			for _, cut := range cuts {
				state := make([]pattern.StateWitnessValue, 0, len(cut.State))
				for _, value := range cut.State {
					state = append(state, pattern.StateWitnessValue{
						Key: value.ComponentID + "\x00" + value.Name, Value: value.Value,
					})
				}
				entry.Witnesses = append(entry.Witnesses, pattern.MatchStateWitness{
					MatchDigest: matchDigest, WitnessDigest: cut.Digest, State: state,
				})
			}
		}
		sort.Slice(entry.Witnesses, func(i, j int) bool {
			if entry.Witnesses[i].MatchDigest != entry.Witnesses[j].MatchDigest {
				return entry.Witnesses[i].MatchDigest < entry.Witnesses[j].MatchDigest
			}
			return entry.Witnesses[i].WitnessDigest < entry.Witnesses[j].WitnessDigest
		})
		result = append(result, entry)
	}
	return result, nil
}

func (kernel *constraintStateWitnessKernel) augmentedComputation() (*AugmentedComputation, error) {
	if kernel == nil || kernel.poset == nil {
		return nil, fmt.Errorf("%w: constraint state witness kernel has no poset", ErrInvalidAugmentedComputation)
	}
	if kernel.computation != nil {
		return kernel.computation, nil
	}
	result := &ExecutionResult{Poset: kernel.poset, StateOperations: kernel.operations}
	computation, err := result.AugmentedComputation()
	if err != nil {
		return nil, err
	}
	kernel.computation = computation
	return computation, nil
}

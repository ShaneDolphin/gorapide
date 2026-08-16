package arch

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/constraint"
)

// ErrInvalidPreparedArchitecture identifies a nil or incomplete immutable
// deterministic architecture snapshot.
var ErrInvalidPreparedArchitecture = errors.New("invalid prepared deterministic architecture")

// PreparedArchitecture is a deeply owned executable snapshot of one validated
// architecture. It contains no caller-owned mutable model pointers and may be
// executed, replayed, or explored repeatedly and concurrently.
//
// Preparing is the only operation that reads the source Architecture. Callers
// must not mutate the source concurrently with PrepareDeterministic. After a
// successful return, the source may be changed or discarded without changing
// this snapshot.
type PreparedArchitecture struct {
	model *deterministicModel
}

// PrepareDeterministic validates and seals the currently supported Rapide
// architecture subset into a deeply owned executable snapshot.
func (a *Architecture) PrepareDeterministic() (*PreparedArchitecture, error) {
	if a == nil {
		return nil, fmt.Errorf("%w: architecture is nil", ErrInvalidPreparedArchitecture)
	}
	model, err := a.deterministicModel()
	if err != nil {
		return nil, err
	}
	return &PreparedArchitecture{model: model}, nil
}

func (prepared *PreparedArchitecture) checkedModel() (*deterministicModel, error) {
	if prepared == nil || prepared.model == nil || prepared.model.digest == "" || len(prepared.model.canonical) == 0 {
		return nil, fmt.Errorf("%w: snapshot is nil or incomplete", ErrInvalidPreparedArchitecture)
	}
	return prepared.model, nil
}

// MarshalCanonicalModel returns an isolated copy of the canonical architecture
// bytes frozen at preparation.
func (prepared *PreparedArchitecture) MarshalCanonicalModel() ([]byte, error) {
	model, err := prepared.checkedModel()
	if err != nil {
		return nil, err
	}
	return append([]byte(nil), model.canonical...), nil
}

// MarshalCanonicalModel validates and snapshots the supported architecture,
// then returns its canonical model bytes.
func (a *Architecture) MarshalCanonicalModel() ([]byte, error) {
	prepared, err := a.PrepareDeterministic()
	if err != nil {
		return nil, err
	}
	return prepared.MarshalCanonicalModel()
}

// DeterministicModelDigest returns the canonical semantic identity frozen into
// this snapshot.
func (prepared *PreparedArchitecture) DeterministicModelDigest() (string, error) {
	model, err := prepared.checkedModel()
	if err != nil {
		return "", err
	}
	return model.digest, nil
}

// ExecuteDeterministic executes this immutable snapshot with explicit journal
// input. Each call creates fresh runtime state.
func (prepared *PreparedArchitecture) ExecuteDeterministic(journal ExecutionJournal) (*ExecutionResult, error) {
	model, err := prepared.checkedModel()
	if err != nil {
		return nil, err
	}
	return executeDeterministicModel(model, journal)
}

// ReplayDeterministic executes this snapshot and verifies the complete
// execution-artifact digest.
func (prepared *PreparedArchitecture) ReplayDeterministic(
	journal ExecutionJournal,
	expectedArtifactDigest string,
) (*ExecutionResult, error) {
	result, err := prepared.ExecuteDeterministic(journal)
	if err != nil {
		return nil, err
	}
	digest, err := result.ArtifactDigest()
	if err != nil {
		return nil, err
	}
	if digest != expectedArtifactDigest {
		return nil, fmt.Errorf("%w: expected=%s actual=%s", ErrReplayMismatch, expectedArtifactDigest, digest)
	}
	return result, nil
}

// ExploreDeterministic enumerates supported choices against this one immutable
// snapshot. Every branch reuses the same sealed model and fresh runtime state.
func (prepared *PreparedArchitecture) ExploreDeterministic(
	journal ExecutionJournal,
	limits ExplorationLimits,
) (*ExplorationResult, error) {
	model, err := prepared.checkedModel()
	if err != nil {
		return nil, err
	}
	return exploreDeterministicModel(model, journal, limits)
}

func cloneInterfaceDecl(source *InterfaceDecl) (*InterfaceDecl, error) {
	if source == nil {
		return nil, fmt.Errorf("interface is nil")
	}
	result := &InterfaceDecl{Name: source.Name}
	if source.structuralType != nil {
		encoded, err := source.structuralType.MarshalCanonical()
		if err != nil {
			return nil, fmt.Errorf("structural Rapide type: %w", err)
		}
		cloned, err := gorapide.ParseRapideType(encoded)
		if err != nil {
			return nil, fmt.Errorf("structural Rapide type: %w", err)
		}
		result.structuralType = &cloned
	}
	var err error
	result.Actions, err = cloneActionDecls(source.Actions)
	if err != nil {
		return nil, err
	}
	result.Functions, err = cloneFunctionDecls(source.Functions)
	if err != nil {
		return nil, err
	}
	result.Services = make([]ServiceDecl, len(source.Services))
	for index, service := range source.Services {
		result.Services[index].Name = service.Name
		result.Services[index].Actions, err = cloneActionDecls(service.Actions)
		if err != nil {
			return nil, err
		}
		result.Services[index].Functions, err = cloneFunctionDecls(service.Functions)
		if err != nil {
			return nil, err
		}
	}
	before, err := canonicalizeInterface(source)
	if err != nil {
		return nil, err
	}
	after, err := canonicalizeInterface(result)
	if err != nil {
		return nil, err
	}
	beforeBytes, err := canonicalJSON(before)
	if err != nil {
		return nil, err
	}
	afterBytes, err := canonicalJSON(after)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(beforeBytes, afterBytes) {
		return nil, fmt.Errorf("interface %q clone changed canonical identity", source.Name)
	}
	return result, nil
}

func cloneActionDecls(source []ActionDecl) ([]ActionDecl, error) {
	result := make([]ActionDecl, len(source))
	for index, declaration := range source {
		parameters, err := cloneParamDecls(declaration.Params)
		if err != nil {
			return nil, fmt.Errorf("action %q: %w", declaration.Name, err)
		}
		result[index] = ActionDecl{Name: declaration.Name, Kind: declaration.Kind, Params: parameters}
	}
	return result, nil
}

func cloneFunctionDecls(source []FunctionDecl) ([]FunctionDecl, error) {
	result := make([]FunctionDecl, len(source))
	for index, declaration := range source {
		parameters, err := cloneParamDecls(declaration.Params)
		if err != nil {
			return nil, fmt.Errorf("function %q: %w", declaration.Name, err)
		}
		result[index] = FunctionDecl{
			Name: declaration.Name, Kind: declaration.Kind,
			Params: parameters, ReturnType: declaration.ReturnType,
		}
	}
	return result, nil
}

func cloneParamDecls(source []ParamDecl) ([]ParamDecl, error) {
	result := make([]ParamDecl, len(source))
	for index, declaration := range source {
		result[index] = ParamDecl{Name: declaration.Name, Type: declaration.Type}
		if declaration.Default != nil {
			values, err := gorapide.CanonicalizeParams(map[string]any{"value": declaration.Default})
			if err != nil {
				return nil, fmt.Errorf("parameter %q default: %w", declaration.Name, err)
			}
			result[index].Default = values["value"]
		}
		if declaration.structuralType != nil {
			encoded, err := declaration.structuralType.MarshalCanonical()
			if err != nil {
				return nil, fmt.Errorf("parameter %q structural type: %w", declaration.Name, err)
			}
			cloned, err := gorapide.ParseRapideType(encoded)
			if err != nil {
				return nil, fmt.Errorf("parameter %q structural type: %w", declaration.Name, err)
			}
			result[index].structuralType = &cloned
		}
	}
	return result, nil
}

func canonicalJSON(value any) ([]byte, error) {
	return json.Marshal(value)
}

func cloneDeterministicConstraintSet(source *constraint.ConstraintSet) (*constraint.ConstraintSet, error) {
	if source == nil {
		return nil, nil
	}
	before, err := source.DeterministicDigest()
	if err != nil {
		return nil, err
	}
	checkers, err := source.DeterministicCheckers()
	if err != nil {
		return nil, err
	}
	result := constraint.NewConstraintSet(source.Name)
	for index, checker := range checkers {
		var cloned constraint.CanonicalCheckable
		switch current := checker.(type) {
		case *constraint.Constraint:
			cloned, err = constraint.CloneDeterministicConstraint(current)
		case *BehaviorConformanceConstraint:
			cloned, err = cloneBehaviorConformanceConstraint(current)
		default:
			err = fmt.Errorf("unsupported canonical checker type %T", checker)
		}
		if err != nil {
			return nil, fmt.Errorf("constraint checker %d: %w", index, err)
		}
		result.Add(cloned)
	}
	after, err := result.DeterministicDigest()
	if err != nil {
		return nil, err
	}
	if after != before {
		return nil, fmt.Errorf("constraint set %q clone changed digest", source.Name)
	}
	return result, nil
}

func cloneDeterministicConstraintSets(
	source map[string]*constraint.ConstraintSet,
) (map[string]*constraint.ConstraintSet, error) {
	result := make(map[string]*constraint.ConstraintSet, len(source))
	for owner, set := range source {
		clone, err := cloneDeterministicConstraintSet(set)
		if err != nil {
			return nil, fmt.Errorf("owner %q constraints: %w", owner, err)
		}
		result[owner] = clone
	}
	return result, nil
}

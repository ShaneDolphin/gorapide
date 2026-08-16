package arch

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/constraint"
)

func TestDeterministicTrustedEntryPointsAdmitClosedModel(t *testing.T) {
	architecture := NewArchitecture("closed-boundary")
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := NewExecutionJournal(digest, 1)
	executed, err := architecture.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	artifactDigest, err := executed.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := architecture.ReplayDeterministic(journal, artifactDigest)
	if err != nil {
		t.Fatal(err)
	}
	explored, err := architecture.ExploreDeterministic(journal, ExplorationLimits{
		MaxExecutions: 1, MaxChoiceDepth: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !explored.Complete || explored.Executions != 1 || len(explored.Computations) != 1 {
		t.Fatalf("closed-model exploration=%#v", explored)
	}
	executedBytes, err := executed.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	replayedBytes, err := replayed.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	exploredBytes, err := explored.Computations[0].Result.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(executedBytes, replayedBytes) || !bytes.Equal(executedBytes, exploredBytes) {
		t.Fatal("closed model did not retain byte-identical execute, replay, and exploration artifacts")
	}
}

func TestDeterministicTrustedEntryPointsRejectLegacyExecutionState(t *testing.T) {
	type boundaryCase struct {
		name  string
		want  string
		build func(*testing.T) (*Architecture, func())
	}

	closedComponent := func(id string) *Component {
		return NewComponent(id, Interface(id+"Interface").Build(), nil)
	}
	cases := []boundaryCase{
		{
			name: "running architecture",
			want: "is running",
			build: func(t *testing.T) (*Architecture, func()) {
				architecture := NewArchitecture("running")
				if err := architecture.Start(context.Background()); err != nil {
					t.Fatal(err)
				}
				return architecture, func() {
					if err := architecture.Stop(); err != nil {
						t.Error(err)
					}
					architecture.Wait()
				}
			},
		},
		{
			name: "observer callback",
			want: "observer callbacks",
			build: func(*testing.T) (*Architecture, func()) {
				return NewArchitecture("observer", WithObserver(func(*gorapide.Event) {})), func() {}
			},
		},
		{
			name: "constraint checker callback",
			want: "constraint checker callbacks",
			build: func(*testing.T) (*Architecture, func()) {
				architecture := NewArchitecture("checker")
				architecture.WithConstraintsOpts(
					constraint.NewConstraintSet("closed"), constraint.CheckAfter,
					func(*constraint.Checker) {},
				)
				return architecture, func() {}
			},
		},
		{
			name: "component receive callback",
			want: "Go behavior callbacks",
			build: func(t *testing.T) (*Architecture, func()) {
				architecture := NewArchitecture("receive-callback")
				component := closedComponent("component")
				component.OnReceive(func(*Component, *gorapide.Event) {})
				if err := architecture.AddComponent(component); err != nil {
					t.Fatal(err)
				}
				return architecture, func() {}
			},
		},
		{
			name: "component behavior rule callback",
			want: "Go behavior callbacks",
			build: func(t *testing.T) (*Architecture, func()) {
				architecture := NewArchitecture("behavior-rule")
				component := closedComponent("component")
				component.OnEvent("Input", func(BehaviorContext) {})
				if err := architecture.AddComponent(component); err != nil {
					t.Fatal(err)
				}
				return architecture, func() {}
			},
		},
		{
			name: "dynamic binding",
			want: "dynamic bindings",
			build: func(t *testing.T) (*Architecture, func()) {
				architecture := NewArchitecture("binding")
				for _, id := range []string{"source", "target"} {
					if err := architecture.AddComponent(closedComponent(id)); err != nil {
						t.Fatal(err)
					}
				}
				if err := architecture.Bind("source", "target"); err != nil {
					t.Fatal(err)
				}
				return architecture, func() {}
			},
		},
		{
			name: "goroutine subarchitecture adapter",
			want: "subarchitectures",
			build: func(t *testing.T) (*Architecture, func()) {
				architecture := NewArchitecture("parent")
				adapter := WrapArchitecture("child", NewArchitecture("inner")).
					WithInterface(Interface("Child").Build()).Build()
				if err := architecture.AddSubArchitecture(adapter); err != nil {
					t.Fatal(err)
				}
				return architecture, func() {}
			},
		},
	}

	entryPoints := []struct {
		name string
		run  func(*Architecture) error
	}{
		{
			name: "model digest",
			run: func(architecture *Architecture) error {
				_, err := architecture.DeterministicModelDigest()
				return err
			},
		},
		{
			name: "execute",
			run: func(architecture *Architecture) error {
				_, err := architecture.ExecuteDeterministic(boundaryTestJournal())
				return err
			},
		},
		{
			name: "replay",
			run: func(architecture *Architecture) error {
				_, err := architecture.ReplayDeterministic(boundaryTestJournal(), "sha256:unused")
				return err
			},
		},
		{
			name: "explore",
			run: func(architecture *Architecture) error {
				_, err := architecture.ExploreDeterministic(boundaryTestJournal(), ExplorationLimits{
					MaxExecutions: 1, MaxChoiceDepth: 1,
				})
				return err
			},
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			architecture, cleanup := test.build(t)
			defer cleanup()
			for _, entryPoint := range entryPoints {
				t.Run(entryPoint.name, func(t *testing.T) {
					err := entryPoint.run(architecture)
					if !errors.Is(err, ErrUnsupportedDeterministicModel) ||
						!strings.Contains(err.Error(), test.want) {
						t.Fatalf("error=%v, want ErrUnsupportedDeterministicModel containing %q", err, test.want)
					}
				})
			}
		})
	}
}

func boundaryTestJournal() ExecutionJournal {
	return NewExecutionJournal("sha256:boundary-test", 1)
}

package rapide

import (
	"bytes"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/arch"
)

const sourceMapProgram = `
type Domain is interface
  action out Observed(value : Integer);
  action out Completed(value : Integer);
end interface Domain;

type Range is interface
  action out Accepted(value : Integer);
  private action Abstracted(value : Integer);
  constraint
    never Abstracted(value is 0);
end interface Range;

map AuditView() from Domain to Range is
rule
  (?Value : Integer) Observed(value is ?Value) ||>
    Abstracted(value is ?Value);
  (?Value : Integer) Completed(?Value) ||> Accepted(?Value);
end map AuditView;
`

const sourceMapProgramReordered = `
type Range is interface
  action out Accepted(value : Integer);
  private action Abstracted(value : Integer);
  constraint
    never Abstracted(value is 0);
end interface Range;

type Domain is interface
  action out Completed(value : Integer);
  action out Observed(value : Integer);
end interface Domain;

map AuditView() from Domain to Range is
rule
	(?value : Integer) completed(?value) ||> accepted(?value);
	(?value : Integer) observed(value is ?value) ||>
		abstracted(value is ?value);
end AuditView;
`

func TestParseRapideMapGeneratorKeepsRestrictedGeneratorDistinct(t *testing.T) {
	file, err := Parse([]byte(sourceMapProgram))
	if err != nil {
		t.Fatal(err)
	}
	if len(file.Maps) != 1 {
		t.Fatalf("maps=%d, want 1", len(file.Maps))
	}
	mapping := file.Maps[0]
	if mapping.Name != "AuditView" || len(mapping.Domains) != 1 || mapping.Domains[0].Name != "Domain" ||
		mapping.Range.Name != "Range" || len(mapping.Rules) != 2 {
		t.Fatalf("parsed map=%#v", mapping)
	}
	first := mapping.Rules[0]
	if first.Connector != ConnectAgent || first.Generator == nil ||
		first.Generator.Kind != PosetGeneratorEvent || first.Generator.Call.Name != "Abstracted" ||
		len(first.Generator.Call.Arguments) != 1 || len(first.Placeholders) != 1 {
		t.Fatalf("parsed first map rule=%#v", first)
	}
	if len(file.Architectures) != 0 || len(file.Modules) != 0 {
		t.Fatalf("map was conflated with another generator kind: %#v", file)
	}
}

const sourceMapPosetProgram = `
type Domain is interface
  action out Trigger(value : Integer);
end interface Domain;

type Range is interface
  action out Begin(value : Integer);
  action out Left(value : Integer);
  action out Right(value : Integer);
  action out Done(value : Integer);
end interface Range;

map PosetView() from Domain to Range is
rule
  (?Value : Integer) Trigger(?Value) ||>
    Begin(?Value) -> (Left(?Value) || Right(?Value)) -> Done(?Value);
end map PosetView;
`

const sourceMapPosetProgramEquivalent = `
type Range is interface
  action out Done(value : Integer);
  action out Right(value : Integer);
  action out Begin(value : Integer);
  action out Left(value : Integer);
end interface Range;
type Domain is interface action out Trigger(value : Integer); end interface Domain;
map PosetView() from domain to range is rule
  (?value : Integer) trigger(?value) ||>
    begin(?value) -> ((right(?value) || left(?value)) -> done(?value));
end PosetView;
`

func TestParseRapideMapFiniteGeneratorRetainsRestrictedPatternTree(t *testing.T) {
	file, err := Parse([]byte(sourceMapPosetProgram))
	if err != nil {
		t.Fatal(err)
	}
	if len(file.Maps) != 1 || len(file.Maps[0].Rules) != 1 || file.Maps[0].Rules[0].Generator == nil {
		t.Fatalf("parsed finite generator=%#v", file.Maps)
	}
	generator := file.Maps[0].Rules[0].Generator
	if generator.Kind != PosetGeneratorBinary || generator.Operator != "->" ||
		generator.Left == nil || generator.Right == nil ||
		generator.Left.Kind != PosetGeneratorBinary || generator.Left.Operator != "->" ||
		generator.Left.Right == nil || generator.Left.Right.Kind != PosetGeneratorBinary ||
		generator.Left.Right.Operator != "||" || generator.Right.Kind != PosetGeneratorEvent ||
		generator.Right.Call.Name != "Done" {
		t.Fatalf("restricted generator tree=%#v", generator)
	}
}

func TestCompileRapideMapFiniteGeneratorPreservesCausalityAndIndependence(t *testing.T) {
	left, err := CompileMap([]byte(sourceMapPosetProgram), "PosetView", "domain")
	if err != nil {
		t.Fatal(err)
	}
	right, err := CompileMap([]byte(sourceMapPosetProgramEquivalent), "POSETVIEW", "domain")
	if err != nil {
		t.Fatal(err)
	}
	leftDigest, err := left.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	rightDigest, err := right.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if leftDigest != rightDigest {
		leftModel, _ := left.MarshalCanonicalModel()
		rightModel, _ := right.MarshalCanonicalModel()
		t.Fatalf("associative/commutative equivalent generators differ: %s != %s\nleft=%s\nright=%s",
			leftDigest, rightDigest, leftModel, rightModel)
	}

	domain := gorapide.NewPoset()
	if err := domain.AddEvent(sourceMapDomainEvent(t, "Trigger", "trigger", nil, 17)); err != nil {
		t.Fatal(err)
	}
	limits := arch.MapExecutionLimits{MaxFirings: 4, MaxRangeEvents: 8}
	previous := runtime.GOMAXPROCS(1)
	first, err := left.ExecuteDeterministic(domain, limits)
	runtime.GOMAXPROCS(8)
	second, secondErr := right.ExecuteDeterministic(domain, limits)
	runtime.GOMAXPROCS(previous)
	if err != nil {
		t.Fatal(err)
	}
	if secondErr != nil {
		t.Fatal(secondErr)
	}
	begin := onlySourceMapEvent(t, first.Range.ByName("Begin"))
	leftEvent := onlySourceMapEvent(t, first.Range.ByName("Left"))
	rightEvent := onlySourceMapEvent(t, first.Range.ByName("Right"))
	done := onlySourceMapEvent(t, first.Range.ByName("Done"))
	if !first.Range.IsCausallyBefore(begin.ID, leftEvent.ID) ||
		!first.Range.IsCausallyBefore(begin.ID, rightEvent.ID) ||
		!first.Range.IsCausallyBefore(leftEvent.ID, done.ID) ||
		!first.Range.IsCausallyBefore(rightEvent.ID, done.ID) {
		t.Fatalf("generated causal diamond is incomplete: %#v", first.Range.All())
	}
	if !first.Range.IsCausallyIndependent(leftEvent.ID, rightEvent.ID) {
		t.Fatal("'||' generator branches were serialized")
	}
	assertSourceMapDirectCauses(t, first.Range, leftEvent, begin.ID)
	assertSourceMapDirectCauses(t, first.Range, rightEvent, begin.ID)
	assertSourceMapDirectCauses(t, first.Range, done, leftEvent.ID, rightEvent.ID)
	for _, event := range []*gorapide.Event{begin, leftEvent, rightEvent, done} {
		if value, ok := event.Param("value"); !ok || value != int64(17) {
			t.Fatalf("%s value=%#v, %t", event.Name, value, ok)
		}
	}
	firstBytes, err := first.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	secondBytes, err := second.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatalf("generator spelling/GOMAXPROCS changed artifact:\nleft=%s\nright=%s", firstBytes, secondBytes)
	}
	artifactDigest, err := first.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := left.ReplayDeterministic(domain, limits, artifactDigest)
	if err != nil {
		t.Fatal(err)
	}
	replayedBytes, err := replayed.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstBytes, replayedBytes) {
		t.Fatalf("finite source-map generator replay differs:\nfirst=%s\nreplay=%s", firstBytes, replayedBytes)
	}
}

func TestCompileRapideMapFiniteGeneratorComposesWithInducedPolicy(t *testing.T) {
	strong, err := CompileMap([]byte(sourceMapPosetProgram), "PosetView", "domain")
	if err != nil {
		t.Fatal(err)
	}
	none, err := CompileMapWithOptions([]byte(sourceMapPosetProgram), "PosetView", MapCompileOptions{
		DomainID: "domain", InducedDependencyPolicy: arch.NoneInducedDependencyPolicy,
	})
	if err != nil {
		t.Fatal(err)
	}
	domain := gorapide.NewPoset()
	firstTrigger := sourceMapDomainEvent(t, "Trigger", "first", nil, 1)
	secondTrigger := sourceMapDomainEvent(t, "Trigger", "second", []gorapide.EventID{firstTrigger.ID}, 2)
	if err := domain.AddEvent(firstTrigger); err != nil {
		t.Fatal(err)
	}
	if err := domain.AddEventWithCause(secondTrigger, firstTrigger.ID); err != nil {
		t.Fatal(err)
	}
	limits := arch.MapExecutionLimits{MaxFirings: 4, MaxRangeEvents: 12}
	strongResult, err := strong.ExecuteDeterministic(domain, limits)
	if err != nil {
		t.Fatal(err)
	}
	noneResult, err := none.ExecuteDeterministic(domain, limits)
	if err != nil {
		t.Fatal(err)
	}
	strongBeginOne := sourceMapEventByInteger(t, strongResult.Range.ByName("Begin"), 1)
	strongDoneOne := sourceMapEventByInteger(t, strongResult.Range.ByName("Done"), 1)
	strongBeginTwo := sourceMapEventByInteger(t, strongResult.Range.ByName("Begin"), 2)
	strongDoneTwo := sourceMapEventByInteger(t, strongResult.Range.ByName("Done"), 2)
	if !strongResult.Range.IsCausallyBefore(strongBeginOne.ID, strongDoneOne.ID) ||
		!strongResult.Range.IsCausallyBefore(strongDoneOne.ID, strongBeginTwo.ID) ||
		!strongResult.Range.IsCausallyBefore(strongBeginTwo.ID, strongDoneTwo.ID) {
		t.Fatal("strong induced policy did not compose firing order with both local generator posets")
	}
	noneBeginOne := sourceMapEventByInteger(t, noneResult.Range.ByName("Begin"), 1)
	noneDoneOne := sourceMapEventByInteger(t, noneResult.Range.ByName("Done"), 1)
	noneBeginTwo := sourceMapEventByInteger(t, noneResult.Range.ByName("Begin"), 2)
	noneDoneTwo := sourceMapEventByInteger(t, noneResult.Range.ByName("Done"), 2)
	if !noneResult.Range.IsCausallyBefore(noneBeginOne.ID, noneDoneOne.ID) ||
		!noneResult.Range.IsCausallyBefore(noneBeginTwo.ID, noneDoneTwo.ID) {
		t.Fatal("none induced policy erased generator-local causality")
	}
	if !noneResult.Range.IsCausallyIndependent(noneDoneOne.ID, noneBeginTwo.ID) {
		t.Fatal("none induced policy invented a dependency between distinct firings")
	}
}

func TestCompileRapideMapRepeatedIndependentActionsRemainDistinct(t *testing.T) {
	source := []byte(`
type Domain is interface action out A(); end interface Domain;
type Range is interface action out X(); end interface Range;
map M() from Domain to Range is rule A ||> X || X; end map M;
`)
	mapping, err := CompileMap(source, "M", "domain")
	if err != nil {
		t.Fatal(err)
	}
	domain := gorapide.NewPoset()
	if err := domain.AddEvent(sourceMapDomainEvent(t, "A", "a", nil, nil)); err != nil {
		t.Fatal(err)
	}
	result, err := mapping.ExecuteDeterministic(domain, arch.MapExecutionLimits{MaxFirings: 2, MaxRangeEvents: 4})
	if err != nil {
		t.Fatal(err)
	}
	events := result.Range.ByName("X")
	if len(events) != 2 || events[0].ID == events[1].ID || !result.Range.IsCausallyIndependent(events[0].ID, events[1].ID) {
		t.Fatalf("repeated independent outputs=%#v", events)
	}
}

const sourceMapImmediatePosetProgram = `
type Domain is interface action out Trigger(value : Integer); end interface Domain;
type Range is interface
  action out Begin(value : Integer);
  action out Left(value : Integer);
  action out Right(value : Integer);
  action out Done(value : Integer);
end interface Range;
map ImmediateView() from Domain to Range is rule
  (?Value : Integer) Trigger(?Value) ||>
    Begin(?Value) |> (Left(?Value) || Right(?Value)) |> Done(?Value);
end map ImmediateView;
`

const sourceMapImmediatePosetProgramEquivalent = `
type Range is interface
  action out Right(value : Integer);
  action out Done(value : Integer);
  action out Left(value : Integer);
  action out Begin(value : Integer);
end interface Range;
type Domain is interface action out Trigger(value : Integer); end interface Domain;
map ImmediateView() from domain to range is rule
  (?value : Integer) trigger(?value) ||>
    (begin(?value) |> (right(?value) || left(?value))) |> done(?value);
end ImmediateView;
`

func TestCompileRapideMapImmediateSequencePreservesDirectAdjacency(t *testing.T) {
	left, err := CompileMap([]byte(sourceMapImmediatePosetProgram), "ImmediateView", "domain")
	if err != nil {
		t.Fatal(err)
	}
	right, err := CompileMap([]byte(sourceMapImmediatePosetProgramEquivalent), "immediateview", "domain")
	if err != nil {
		t.Fatal(err)
	}
	leftDigest, _ := left.DeterministicModelDigest()
	rightDigest, _ := right.DeterministicModelDigest()
	if leftDigest != rightDigest {
		t.Fatalf("immediate generator order/case variants differ: %s != %s", leftDigest, rightDigest)
	}
	ordinarySource := strings.ReplaceAll(sourceMapImmediatePosetProgram, " |> ", " -> ")
	ordinary, err := CompileMap([]byte(ordinarySource), "ImmediateView", "domain")
	if err != nil {
		t.Fatal(err)
	}
	ordinaryDigest, _ := ordinary.DeterministicModelDigest()
	if leftDigest == ordinaryDigest {
		t.Fatal("immediate and ordinary sequence lost their distinct model identities")
	}

	domain := gorapide.NewPoset()
	firstTrigger := sourceMapDomainEvent(t, "Trigger", "first", nil, 23)
	secondTrigger := sourceMapDomainEvent(t, "Trigger", "second", []gorapide.EventID{firstTrigger.ID}, 24)
	if err := domain.AddEvent(firstTrigger); err != nil {
		t.Fatal(err)
	}
	if err := domain.AddEventWithCause(secondTrigger, firstTrigger.ID); err != nil {
		t.Fatal(err)
	}
	limits := arch.MapExecutionLimits{MaxFirings: 4, MaxRangeEvents: 12}
	previous := runtime.GOMAXPROCS(1)
	first, err := left.ExecuteDeterministic(domain, limits)
	runtime.GOMAXPROCS(8)
	second, secondErr := right.ExecuteDeterministic(domain, limits)
	runtime.GOMAXPROCS(previous)
	if err != nil {
		t.Fatal(err)
	}
	if secondErr != nil {
		t.Fatal(secondErr)
	}
	begin := sourceMapEventByInteger(t, first.Range.ByName("Begin"), 23)
	leftEvent := sourceMapEventByInteger(t, first.Range.ByName("Left"), 23)
	rightEvent := sourceMapEventByInteger(t, first.Range.ByName("Right"), 23)
	done := sourceMapEventByInteger(t, first.Range.ByName("Done"), 23)
	assertSourceMapDirectCauses(t, first.Range, leftEvent, begin.ID)
	assertSourceMapDirectCauses(t, first.Range, rightEvent, begin.ID)
	assertSourceMapDirectCauses(t, first.Range, done, leftEvent.ID, rightEvent.ID)
	if !first.Range.IsCausallyIndependent(leftEvent.ID, rightEvent.ID) {
		t.Fatal("immediate-sequence lowering serialized independent middle branches")
	}
	secondBegin := sourceMapEventByInteger(t, first.Range.ByName("Begin"), 24)
	secondLeft := sourceMapEventByInteger(t, first.Range.ByName("Left"), 24)
	secondRight := sourceMapEventByInteger(t, first.Range.ByName("Right"), 24)
	secondDone := sourceMapEventByInteger(t, first.Range.ByName("Done"), 24)
	assertSourceMapDirectCauses(t, first.Range, secondLeft, secondBegin.ID)
	assertSourceMapDirectCauses(t, first.Range, secondRight, secondBegin.ID)
	assertSourceMapDirectCauses(t, first.Range, secondDone, secondLeft.ID, secondRight.ID)
	if !first.Range.IsCausallyBefore(done.ID, secondBegin.ID) {
		t.Fatal("strong induced order did not connect immediate-generator firing frontiers")
	}
	firstBytes, _ := first.MarshalCanonical()
	secondBytes, _ := second.MarshalCanonical()
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatalf("immediate generator order/case/GOMAXPROCS changed artifact:\nleft=%s\nright=%s", firstBytes, secondBytes)
	}
	artifactDigest, err := first.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := left.ReplayDeterministic(domain, limits, artifactDigest)
	if err != nil {
		t.Fatal(err)
	}
	replayedBytes, _ := replayed.MarshalCanonical()
	if !bytes.Equal(firstBytes, replayedBytes) {
		t.Fatalf("immediate generator replay differs:\nfirst=%s\nreplay=%s", firstBytes, replayedBytes)
	}
}

const sourceMapJoinProgram = `
type Domain is interface action out Trigger(); end interface Domain;
type Range is interface
  action out Left();
  action out Right();
end interface Range;
map JoinView() from Domain to Range is rule
  Trigger ||> Left ~ Right;
end map JoinView;
`

const sourceMapJoinProgramEquivalent = `
type Range is interface action out Right(); action out Left(); end interface Range;
type Domain is interface action out Trigger(); end interface Domain;
map JoinView() from domain to range is rule
  trigger ||> right ~ left;
end JoinView;
`

func TestCompileRapideMapJoinSchedulesAndExploresAllSupportedCausalPreorders(t *testing.T) {
	mapping, err := CompileMap([]byte(sourceMapJoinProgram), "JoinView", "domain")
	if err != nil {
		t.Fatal(err)
	}
	equivalent, err := CompileMap([]byte(sourceMapJoinProgramEquivalent), "joinview", "domain")
	if err != nil {
		t.Fatal(err)
	}
	mappingDigest, _ := mapping.DeterministicModelDigest()
	equivalentDigest, _ := equivalent.DeterministicModelDigest()
	if mappingDigest != equivalentDigest {
		t.Fatalf("commutative join/source-order/case variants differ: %s != %s", mappingDigest, equivalentDigest)
	}
	domain := gorapide.NewPoset()
	if err := domain.AddEvent(sourceMapDomainEvent(t, "Trigger", "trigger", nil, nil)); err != nil {
		t.Fatal(err)
	}
	request := arch.MapExecutionRequest{
		Limits: arch.MapExecutionLimits{MaxFirings: 2, MaxRangeEvents: 4},
	}
	previous := runtime.GOMAXPROCS(1)
	defaultResult, err := mapping.ExecuteDeterministicRequest(domain, request)
	runtime.GOMAXPROCS(8)
	repeated, repeatedErr := equivalent.ExecuteDeterministicRequest(domain, request)
	runtime.GOMAXPROCS(previous)
	if err != nil {
		t.Fatal(err)
	}
	if repeatedErr != nil {
		t.Fatal(repeatedErr)
	}
	if len(defaultResult.Artifact.Choices) != 1 || defaultResult.Artifact.Choices[0].Scheduled ||
		len(defaultResult.Artifact.Choices[0].Options) != 4 || len(defaultResult.Artifact.Firings) != 1 ||
		defaultResult.Artifact.Firings[0].GeneratorID != defaultResult.Artifact.Choices[0].Selected {
		t.Fatalf("join choice witness=%#v firings=%#v", defaultResult.Artifact.Choices, defaultResult.Artifact.Firings)
	}
	defaultBytes, err := defaultResult.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	repeatedBytes, err := repeated.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(defaultBytes, repeatedBytes) {
		t.Fatalf("canonical join selection changed with GOMAXPROCS:\nfirst=%s\nsecond=%s", defaultBytes, repeatedBytes)
	}
	if _, err := arch.ParseCanonicalMapExecutionArtifact(defaultBytes); err != nil {
		t.Fatalf("canonical join artifact did not parse: %v", err)
	}
	tamperedChoice := defaultResult.Artifact
	tamperedChoice.Choices = append([]arch.ChoiceResolution{}, defaultResult.Artifact.Choices...)
	tamperedChoice.Choices[0].Point += "-tampered"
	if _, err := tamperedChoice.MarshalCanonical(); !errors.Is(err, arch.ErrInvalidEventPatternMap) {
		t.Fatalf("tampered choice point: expected ErrInvalidEventPatternMap, got %v", err)
	}
	tamperedGenerator := defaultResult.Artifact
	tamperedGenerator.Firings = append([]arch.MapFiringRecord{}, defaultResult.Artifact.Firings...)
	tamperedGenerator.Firings[0].GeneratorID = "mapposet1-tampered"
	if _, err := tamperedGenerator.MarshalCanonical(); !errors.Is(err, arch.ErrInvalidEventPatternMap) {
		t.Fatalf("tampered firing generator: expected ErrInvalidEventPatternMap, got %v", err)
	}

	explorationLimits := arch.ExplorationLimits{MaxExecutions: 8, MaxChoiceDepth: 1}
	first, err := mapping.ExploreDeterministic(domain, request, explorationLimits)
	if err != nil {
		t.Fatal(err)
	}
	second, err := mapping.ExploreDeterministic(domain, request, explorationLimits)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Complete || first.Executions != 5 || len(first.Computations) != 4 {
		t.Fatalf("join exploration=%#v", first)
	}
	firstBytes, _ := first.MarshalCanonical()
	secondBytes, _ := second.MarshalCanonical()
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatalf("repeated join exploration differs:\nfirst=%s\nsecond=%s", firstBytes, secondBytes)
	}

	relations := make(map[string]bool)
	for _, computation := range first.Computations {
		if computation.Result == nil || len(computation.Schedule) != 1 ||
			len(computation.Result.Artifact.Choices) != 1 || !computation.Result.Artifact.Choices[0].Scheduled {
			t.Fatalf("incomplete explored join witness=%#v", computation)
		}
		left := onlySourceMapEvent(t, computation.Result.Range.ByName("Left"))
		right := onlySourceMapEvent(t, computation.Result.Range.ByName("Right"))
		switch {
		case computation.Result.Range.IsCausallyBefore(left.ID, right.ID):
			relations["left-before-right"] = true
		case computation.Result.Range.IsCausallyBefore(right.ID, left.ID):
			relations["right-before-left"] = true
		case computation.Result.Range.IsCausallyEquivalent(left.ID, right.ID):
			relations["equivalent"] = true
		case computation.Result.Range.IsCausallyIndependent(left.ID, right.ID):
			relations["independent"] = true
		default:
			t.Fatal("join exploration produced an invalid cross-event relation")
		}
		scheduled := request
		scheduled.Choices = append([]arch.ChoiceDecision{}, computation.Schedule...)
		digest, err := computation.Result.ArtifactDigest()
		if err != nil {
			t.Fatal(err)
		}
		replayed, err := mapping.ReplayDeterministicRequest(domain, scheduled, digest)
		if err != nil {
			t.Fatal(err)
		}
		replayedBytes, _ := replayed.MarshalCanonical()
		computationBytes, _ := computation.Result.MarshalCanonical()
		if !bytes.Equal(replayedBytes, computationBytes) {
			t.Fatalf("scheduled join replay differs:\nresult=%s\nreplay=%s", computationBytes, replayedBytes)
		}
	}
	if len(relations) != 4 || !relations["left-before-right"] || !relations["right-before-left"] ||
		!relations["equivalent"] || !relations["independent"] {
		t.Fatalf("join relations=%#v", relations)
	}
}

func TestRapideSourceMapJoinChoiceScheduleRejectsMismatch(t *testing.T) {
	mapping, err := CompileMap([]byte(sourceMapJoinProgram), "JoinView", "domain")
	if err != nil {
		t.Fatal(err)
	}
	domain := gorapide.NewPoset()
	if err := domain.AddEvent(sourceMapDomainEvent(t, "Trigger", "trigger", nil, nil)); err != nil {
		t.Fatal(err)
	}
	request := arch.MapExecutionRequest{Limits: arch.MapExecutionLimits{MaxFirings: 2, MaxRangeEvents: 4}}
	result, err := mapping.ExecuteDeterministicRequest(domain, request)
	if err != nil {
		t.Fatal(err)
	}
	unavailable := request
	unavailable.Choices = []arch.ChoiceDecision{{
		Point: result.Artifact.Choices[0].Point, Selection: "not-a-generator",
	}}
	if _, err := mapping.ExecuteDeterministicRequest(domain, unavailable); !errors.Is(err, arch.ErrChoiceScheduleMismatch) {
		t.Fatalf("unavailable generator selection: expected ErrChoiceScheduleMismatch, got %v", err)
	}
	unused := request
	unused.Choices = []arch.ChoiceDecision{{Point: "unused", Selection: "unused"}}
	if _, err := mapping.ExecuteDeterministicRequest(domain, unused); !errors.Is(err, arch.ErrChoiceScheduleMismatch) {
		t.Fatalf("unused generator selection: expected ErrChoiceScheduleMismatch, got %v", err)
	}
}

func TestCompileRapideMapJoinEquivalenceComposesWithSequence(t *testing.T) {
	source := `
type Domain is interface action out Trigger(); end interface Domain;
type Range is interface
  action out Left(); action out Right(); action out Done();
end interface Range;
map JoinSequence() from Domain to Range is rule
  Trigger ||> (Left ~ Right) -> Done;
end map JoinSequence;
`
	mapping, err := CompileMap([]byte(source), "JoinSequence", "domain")
	if err != nil {
		t.Fatal(err)
	}
	domain := gorapide.NewPoset()
	if err := domain.AddEvent(sourceMapDomainEvent(t, "Trigger", "trigger", nil, nil)); err != nil {
		t.Fatal(err)
	}
	request := arch.MapExecutionRequest{Limits: arch.MapExecutionLimits{MaxFirings: 2, MaxRangeEvents: 6}}
	explored, err := mapping.ExploreDeterministic(domain, request, arch.ExplorationLimits{MaxExecutions: 8, MaxChoiceDepth: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !explored.Complete || len(explored.Computations) != 4 {
		t.Fatalf("join-sequence exploration=%#v", explored)
	}
	foundEquivalent := false
	for _, computation := range explored.Computations {
		left := onlySourceMapEvent(t, computation.Result.Range.ByName("Left"))
		right := onlySourceMapEvent(t, computation.Result.Range.ByName("Right"))
		done := onlySourceMapEvent(t, computation.Result.Range.ByName("Done"))
		if !computation.Result.Range.IsCausallyBefore(left.ID, done.ID) ||
			!computation.Result.Range.IsCausallyBefore(right.ID, done.ID) {
			t.Fatal("sequence did not order every join maximum before Done")
		}
		if computation.Result.Range.IsCausallyEquivalent(left.ID, right.ID) {
			foundEquivalent = true
			if causes := computation.Result.Range.DirectCauses(done.ID); len(causes) != 2 {
				t.Fatalf("equivalent quotient predecessor did not substitute to both occurrences: %v", causes.IDs())
			}
		}
	}
	if !foundEquivalent {
		t.Fatal("join exploration omitted the causal-equivalence branch")
	}
}

func TestCompileRapideMapJoinPreservesOperandSubposets(t *testing.T) {
	source := []byte(`
type Domain is interface action out Trigger(); end interface Domain;
type Range is interface action out A(); action out B(); action out C(); end interface Range;
map M() from Domain to Range is rule Trigger ||> (A -> B) ~ C; end map M;
`)
	mapping, err := CompileMap(source, "M", "domain")
	if err != nil {
		t.Fatal(err)
	}
	domain := gorapide.NewPoset()
	if err := domain.AddEvent(sourceMapDomainEvent(t, "Trigger", "trigger", nil, nil)); err != nil {
		t.Fatal(err)
	}
	explored, err := mapping.ExploreDeterministic(domain, arch.MapExecutionRequest{
		Limits: arch.MapExecutionLimits{MaxFirings: 2, MaxRangeEvents: 5},
	}, arch.ExplorationLimits{MaxExecutions: 32, MaxChoiceDepth: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !explored.Complete || len(explored.Computations) < 3 {
		t.Fatalf("composite join exploration=%#v", explored)
	}
	for _, computation := range explored.Computations {
		a := onlySourceMapEvent(t, computation.Result.Range.ByName("A"))
		b := onlySourceMapEvent(t, computation.Result.Range.ByName("B"))
		if !computation.Result.Range.IsCausallyBefore(a.ID, b.ID) {
			t.Fatal("join alternative changed the left operand's required A -> B relation")
		}
	}
}

const sourceMapDisjunctionProgram = `
type Domain is interface action out Trigger(value : Integer); end interface Domain;
type Range is interface
  action out A(value : Integer);
  action out B(value : Integer);
  action out C(value : Integer);
end interface Range;
map ChoiceView() from Domain to Range is rule
  (?Value : Integer) Trigger(?Value) ||> (A(?Value) -> C(?Value)) or B(?Value);
end map ChoiceView;
`

const sourceMapDisjunctionProgramEquivalent = `
type Range is interface
  action out C(value : Integer);
  action out B(value : Integer);
  action out A(value : Integer);
end interface Range;
type Domain is interface action out Trigger(value : Integer); end interface Domain;
map ChoiceView() from domain to range is rule
  (?value : Integer) trigger(?value) ||> b(?value) or (a(?value) -> c(?value));
end ChoiceView;
`

func TestParseRapideMapDisjunctionRetainsAlternativeTree(t *testing.T) {
	file, err := Parse([]byte(sourceMapDisjunctionProgram))
	if err != nil {
		t.Fatal(err)
	}
	if len(file.Maps) != 1 || len(file.Maps[0].Rules) != 1 || file.Maps[0].Rules[0].Generator == nil {
		t.Fatalf("parsed map disjunction=%#v", file.Maps)
	}
	generator := file.Maps[0].Rules[0].Generator
	if generator.Kind != PosetGeneratorBinary || generator.Operator != "or" ||
		generator.Left == nil || generator.Left.Kind != PosetGeneratorBinary || generator.Left.Operator != "->" ||
		generator.Right == nil || generator.Right.Kind != PosetGeneratorEvent || generator.Right.Call.Name != "B" {
		t.Fatalf("disjunction generator tree=%#v", generator)
	}
}

func TestCompileRapideMapDisjunctionSchedulesExploresAndCanonicalizes(t *testing.T) {
	left, err := CompileMap([]byte(sourceMapDisjunctionProgram), "ChoiceView", "domain")
	if err != nil {
		t.Fatal(err)
	}
	right, err := CompileMap([]byte(sourceMapDisjunctionProgramEquivalent), "choiceview", "domain")
	if err != nil {
		t.Fatal(err)
	}
	leftDigest, _ := left.DeterministicModelDigest()
	rightDigest, _ := right.DeterministicModelDigest()
	if leftDigest != rightDigest {
		t.Fatalf("associative/commutative disjunction variants differ: %s != %s", leftDigest, rightDigest)
	}
	domain := gorapide.NewPoset()
	if err := domain.AddEvent(sourceMapDomainEvent(t, "Trigger", "trigger", nil, 9)); err != nil {
		t.Fatal(err)
	}
	request := arch.MapExecutionRequest{Limits: arch.MapExecutionLimits{MaxFirings: 2, MaxRangeEvents: 5}}
	previous := runtime.GOMAXPROCS(1)
	defaultResult, err := left.ExecuteDeterministicRequest(domain, request)
	runtime.GOMAXPROCS(8)
	repeated, repeatedErr := right.ExecuteDeterministicRequest(domain, request)
	runtime.GOMAXPROCS(previous)
	if err != nil {
		t.Fatal(err)
	}
	if repeatedErr != nil {
		t.Fatal(repeatedErr)
	}
	defaultBytes, _ := defaultResult.MarshalCanonical()
	repeatedBytes, _ := repeated.MarshalCanonical()
	if !bytes.Equal(defaultBytes, repeatedBytes) || len(defaultResult.Artifact.Choices) != 1 ||
		len(defaultResult.Artifact.Choices[0].Options) != 2 {
		t.Fatalf("disjunction default is not canonical: choices=%#v", defaultResult.Artifact.Choices)
	}
	explored, err := left.ExploreDeterministic(domain, request,
		arch.ExplorationLimits{MaxExecutions: 6, MaxChoiceDepth: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !explored.Complete || explored.Executions != 3 || len(explored.Computations) != 2 {
		t.Fatalf("disjunction exploration=%#v", explored)
	}
	seenSingle, seenSequence := false, false
	for _, computation := range explored.Computations {
		a := computation.Result.Range.ByName("A")
		b := computation.Result.Range.ByName("B")
		c := computation.Result.Range.ByName("C")
		switch {
		case len(a) == 0 && len(b) == 1 && len(c) == 0:
			seenSingle = true
			if value, ok := b[0].Param("value"); !ok || value != int64(9) {
				t.Fatalf("B value=%#v, %t", value, ok)
			}
		case len(a) == 1 && len(b) == 0 && len(c) == 1:
			seenSequence = true
			if !computation.Result.Range.IsCausallyBefore(a[0].ID, c[0].ID) {
				t.Fatal("disjunction changed the selected A -> C operand")
			}
		default:
			t.Fatalf("disjunction mixed alternatives: A=%d B=%d C=%d", len(a), len(b), len(c))
		}
		scheduled := request
		scheduled.Choices = computation.Schedule
		replayed, err := left.ReplayDeterministicRequest(domain, scheduled, computation.ArtifactDigest)
		if err != nil {
			t.Fatal(err)
		}
		replayedBytes, _ := replayed.MarshalCanonical()
		resultBytes, _ := computation.Result.MarshalCanonical()
		if !bytes.Equal(replayedBytes, resultBytes) {
			t.Fatal("scheduled disjunction replay differs")
		}
	}
	if !seenSingle || !seenSequence {
		t.Fatalf("disjunction alternatives single=%t sequence=%t", seenSingle, seenSequence)
	}
}

func TestCompileRapideMapFiniteDisjunctionIterationMatchesExplicitExpansion(t *testing.T) {
	iteratedSource := []byte(`
type Domain is interface action out Trigger(); end interface Domain;
type Range is interface action out Step(value : Integer); end interface Range;
map M() from Domain to Range is rule Trigger ||> [I : 1..3 rel or] Step(I); end map M;
`)
	expandedSource := []byte(`
type Range is interface action out Step(value : Integer); end interface Range;
type Domain is interface action out Trigger(); end interface Domain;
map M() from domain to range is rule Trigger ||> Step(3) or Step(1) or Step(2); end M;
`)
	iterated, err := CompileMap(iteratedSource, "M", "domain")
	if err != nil {
		t.Fatal(err)
	}
	expanded, err := CompileMap(expandedSource, "m", "domain")
	if err != nil {
		t.Fatal(err)
	}
	iteratedDigest, _ := iterated.DeterministicModelDigest()
	expandedDigest, _ := expanded.DeterministicModelDigest()
	if iteratedDigest != expandedDigest {
		t.Fatalf("finite disjunction iteration differs from expansion: %s != %s", iteratedDigest, expandedDigest)
	}
	domain := gorapide.NewPoset()
	if err := domain.AddEvent(sourceMapDomainEvent(t, "Trigger", "trigger", nil, nil)); err != nil {
		t.Fatal(err)
	}
	request := arch.MapExecutionRequest{Limits: arch.MapExecutionLimits{MaxFirings: 2, MaxRangeEvents: 3}}
	explored, err := iterated.ExploreDeterministic(domain, request,
		arch.ExplorationLimits{MaxExecutions: 8, MaxChoiceDepth: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !explored.Complete || len(explored.Computations) != 3 {
		t.Fatalf("finite disjunction iteration exploration=%#v", explored)
	}
	values := make(map[int64]bool)
	for _, computation := range explored.Computations {
		step := onlySourceMapEvent(t, computation.Result.Range.ByName("Step"))
		value, ok := step.Param("value")
		if !ok {
			t.Fatal("iterated disjunction output has no value")
		}
		values[value.(int64)] = true
	}
	if len(values) != 3 || !values[1] || !values[2] || !values[3] {
		t.Fatalf("finite disjunction values=%#v", values)
	}

	duplicateSource := bytes.ReplaceAll(iteratedSource,
		[]byte("[I : 1..3 rel or] Step(I)"), []byte("[3 rel or] Step(7)"))
	singleSource := bytes.ReplaceAll(iteratedSource,
		[]byte("[I : 1..3 rel or] Step(I)"), []byte("Step(7)"))
	duplicate, err := CompileMap(duplicateSource, "M", "domain")
	if err != nil {
		t.Fatal(err)
	}
	single, err := CompileMap(singleSource, "M", "domain")
	if err != nil {
		t.Fatal(err)
	}
	duplicateDigest, _ := duplicate.DeterministicModelDigest()
	singleDigest, _ := single.DeterministicModelDigest()
	if duplicateDigest != singleDigest {
		t.Fatalf("duplicate disjunction alternatives did not collapse: %s != %s", duplicateDigest, singleDigest)
	}
	result, err := duplicate.ExecuteDeterministicRequest(domain, request)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Artifact.Choices) != 0 || len(result.Range.ByName("Step")) != 1 {
		t.Fatalf("duplicate disjunction retained a false choice: %#v", result.Artifact.Choices)
	}
}

func TestCompileRapideMapDisjunctionRetainsEmptyAlternative(t *testing.T) {
	source := []byte(`
type Domain is interface action out Trigger(); end interface Domain;
type Range is interface action out X(); end interface Range;
map M() from Domain to Range is rule Trigger ||> empty() or X; end map M;
`)
	mapping, err := CompileMap(source, "M", "domain")
	if err != nil {
		t.Fatal(err)
	}
	domain := gorapide.NewPoset()
	if err := domain.AddEvent(sourceMapDomainEvent(t, "Trigger", "trigger", nil, nil)); err != nil {
		t.Fatal(err)
	}
	explored, err := mapping.ExploreDeterministic(domain, arch.MapExecutionRequest{
		Limits: arch.MapExecutionLimits{MaxFirings: 2, MaxRangeEvents: 3},
	}, arch.ExplorationLimits{MaxExecutions: 6, MaxChoiceDepth: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !explored.Complete || len(explored.Computations) != 2 {
		t.Fatalf("empty disjunction exploration=%#v", explored)
	}
	seenEmpty, seenX := false, false
	for _, computation := range explored.Computations {
		switch len(computation.Result.Range.ByName("X")) {
		case 0:
			seenEmpty = true
			if len(computation.Result.Range.All()) != 1 {
				t.Fatalf("empty disjunct generated events: %#v", computation.Result.Range.All())
			}
		case 1:
			seenX = true
		default:
			t.Fatal("disjunction generated more than one X")
		}
	}
	if !seenEmpty || !seenX {
		t.Fatalf("empty disjunction branches empty=%t X=%t", seenEmpty, seenX)
	}
}

func TestRapideSourceMapDisjunctionAlternativeBoundIsStable(t *testing.T) {
	var generator strings.Builder
	for index := 0; index < 257; index++ {
		if index != 0 {
			generator.WriteString(" or ")
		}
		fmt.Fprintf(&generator, "X(%d)", index)
	}
	source := []byte(`
type Domain is interface action out Trigger(); end interface Domain;
type Range is interface action out X(value : Integer); end interface Range;
map M() from Domain to Range is rule Trigger ||> ` + generator.String() + `; end map M;
`)
	_, err := CompileMap(source, "M", "domain")
	if err == nil || !strings.Contains(err.Error(), "more than the deterministic bound of 256 alternatives") {
		t.Fatalf("disjunction alternative bound error=%v", err)
	}
}

const sourceMapNamedIterationProgram = `
type Domain is interface action out Trigger(value : Integer); end interface Domain;
type Range is interface action out Step(value : Integer); end interface Range;
map IteratedView() from Domain to Range is rule
  (?Base : Integer) Trigger(?Base) ||>
    [I : 1..3 rel ->] Step(value is ?Base + I);
end map IteratedView;
`

const sourceMapNamedIterationProgramExpanded = `
type Range is interface action out Step(value : Integer); end interface Range;
type Domain is interface action out Trigger(value : Integer); end interface Domain;
map IteratedView() from domain to range is rule
  (?base : Integer) trigger(?base) ||>
    step(value is ?base + 1) -> step(value is ?base + 2) -> step(value is ?base + 3);
end IteratedView;
`

func TestParseRapideMapFiniteIterationRetainsIteratorAndOperand(t *testing.T) {
	file, err := Parse([]byte(sourceMapNamedIterationProgram))
	if err != nil {
		t.Fatal(err)
	}
	if len(file.Maps) != 1 || len(file.Maps[0].Rules) != 1 || file.Maps[0].Rules[0].Generator == nil {
		t.Fatalf("parsed map iteration=%#v", file.Maps)
	}
	generator := file.Maps[0].Rules[0].Generator
	if generator.Kind != PosetGeneratorIteration || generator.Iterator != "I" ||
		generator.First != 1 || generator.Last != 3 || generator.Relation != "->" ||
		generator.Inner == nil || generator.Inner.Kind != PosetGeneratorEvent ||
		generator.Inner.Call.Name != "Step" || len(generator.Inner.Call.Arguments) != 1 {
		t.Fatalf("parsed map iteration=%#v", generator)
	}
}

func TestCompileRapideMapNamedIterationCanonicalizesToExplicitExpansion(t *testing.T) {
	iterated, err := CompileMap([]byte(sourceMapNamedIterationProgram), "IteratedView", "domain")
	if err != nil {
		t.Fatal(err)
	}
	expanded, err := CompileMap([]byte(sourceMapNamedIterationProgramExpanded), "ITERATEDVIEW", "domain")
	if err != nil {
		t.Fatal(err)
	}
	iteratedDigest, err := iterated.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	expandedDigest, err := expanded.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if iteratedDigest != expandedDigest {
		iteratedModel, _ := iterated.MarshalCanonicalModel()
		expandedModel, _ := expanded.MarshalCanonicalModel()
		t.Fatalf("finite iterator and explicit expansion differ: %s != %s\niterated=%s\nexpanded=%s",
			iteratedDigest, expandedDigest, iteratedModel, expandedModel)
	}

	domain := gorapide.NewPoset()
	if err := domain.AddEvent(sourceMapDomainEvent(t, "Trigger", "trigger", nil, 10)); err != nil {
		t.Fatal(err)
	}
	limits := arch.MapExecutionLimits{MaxFirings: 2, MaxRangeEvents: 6}
	previous := runtime.GOMAXPROCS(1)
	first, err := iterated.ExecuteDeterministic(domain, limits)
	runtime.GOMAXPROCS(8)
	second, secondErr := expanded.ExecuteDeterministic(domain, limits)
	runtime.GOMAXPROCS(previous)
	if err != nil {
		t.Fatal(err)
	}
	if secondErr != nil {
		t.Fatal(secondErr)
	}
	one := sourceMapEventByInteger(t, first.Range.ByName("Step"), 11)
	two := sourceMapEventByInteger(t, first.Range.ByName("Step"), 12)
	three := sourceMapEventByInteger(t, first.Range.ByName("Step"), 13)
	assertSourceMapDirectCauses(t, first.Range, two, one.ID)
	assertSourceMapDirectCauses(t, first.Range, three, two.ID)
	firstBytes, _ := first.MarshalCanonical()
	secondBytes, _ := second.MarshalCanonical()
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatalf("finite iterator spelling/GOMAXPROCS changed artifact:\niterated=%s\nexpanded=%s",
			firstBytes, secondBytes)
	}
	artifactDigest, err := first.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := iterated.ReplayDeterministic(domain, limits, artifactDigest)
	if err != nil {
		t.Fatal(err)
	}
	replayedBytes, _ := replayed.MarshalCanonical()
	if !bytes.Equal(firstBytes, replayedBytes) {
		t.Fatalf("finite iterator replay differs:\nfirst=%s\nreplay=%s", firstBytes, replayedBytes)
	}
}

func TestCompileRapideMapFiniteIterationPreservesImmediateAndIndependentRelations(t *testing.T) {
	source := []byte(`
type Domain is interface
  action out Immediate();
  action out Parallel();
end interface Domain;
type Range is interface
  action out Step(value : Integer);
  action out Tick(value : Integer);
end interface Range;
map M() from Domain to Range is rule
  Immediate ||> [I : 1..3 rel |>] Step(I);
  Parallel ||> [3 rel ||] Tick(7);
end map M;
`)
	expandedSource := []byte(`
type Range is interface
  action out Tick(value : Integer);
  action out Step(value : Integer);
end interface Range;
type Domain is interface
  action out Parallel();
  action out Immediate();
end interface Domain;
map M() from domain to range is rule
  Parallel ||> Tick(7) || Tick(7) || Tick(7);
  Immediate ||> (Step(1) |> Step(2)) |> Step(3);
end M;
`)
	iterated, err := CompileMap(source, "M", "domain")
	if err != nil {
		t.Fatal(err)
	}
	expanded, err := CompileMap(expandedSource, "m", "domain")
	if err != nil {
		t.Fatal(err)
	}
	iteratedDigest, _ := iterated.DeterministicModelDigest()
	expandedDigest, _ := expanded.DeterministicModelDigest()
	if iteratedDigest != expandedDigest {
		t.Fatalf("finite immediate/independent iterations differ from expansion: %s != %s",
			iteratedDigest, expandedDigest)
	}
	ordinary, err := CompileMap([]byte(strings.ReplaceAll(string(source), "rel |>", "rel ->")), "M", "domain")
	if err != nil {
		t.Fatal(err)
	}
	ordinaryDigest, _ := ordinary.DeterministicModelDigest()
	if iteratedDigest == ordinaryDigest {
		t.Fatal("iterated immediate sequence lost its distinct model identity")
	}

	domain := gorapide.NewPoset()
	if err := domain.AddEvent(sourceMapDomainEvent(t, "Immediate", "immediate", nil, nil)); err != nil {
		t.Fatal(err)
	}
	if err := domain.AddEvent(sourceMapDomainEvent(t, "Parallel", "parallel", nil, nil)); err != nil {
		t.Fatal(err)
	}
	result, err := iterated.ExecuteDeterministic(domain, arch.MapExecutionLimits{MaxFirings: 4, MaxRangeEvents: 10})
	if err != nil {
		t.Fatal(err)
	}
	one := sourceMapEventByInteger(t, result.Range.ByName("Step"), 1)
	two := sourceMapEventByInteger(t, result.Range.ByName("Step"), 2)
	three := sourceMapEventByInteger(t, result.Range.ByName("Step"), 3)
	assertSourceMapDirectCauses(t, result.Range, two, one.ID)
	assertSourceMapDirectCauses(t, result.Range, three, two.ID)
	ticks := result.Range.ByName("Tick")
	if len(ticks) != 3 || ticks[0].ID == ticks[1].ID || ticks[0].ID == ticks[2].ID || ticks[1].ID == ticks[2].ID {
		t.Fatalf("anonymous repeated iterator outputs=%#v", ticks)
	}
	for left := range ticks {
		for right := left + 1; right < len(ticks); right++ {
			if !result.Range.IsCausallyIndependent(ticks[left].ID, ticks[right].ID) {
				t.Fatalf("anonymous independent iterator serialized %s and %s", ticks[left].ID, ticks[right].ID)
			}
		}
	}
}

func TestCompileRapideMapEmptyFiniteIterationsCanonicalizeToNullGenerator(t *testing.T) {
	base := `
type Domain is interface action out A(); end interface Domain;
type Range is interface action out X(value : Integer); end interface Range;
map M() from Domain to Range is rule A ||> %s; end map M;
`
	sources := []string{
		fmt.Sprintf(base, ""),
		fmt.Sprintf(base, "[I : 3..1 rel ->] X(I)"),
		fmt.Sprintf(base, "[0 rel ||] X(9)"),
		fmt.Sprintf(base, "[4..2 rel |>] X(9)"),
	}
	var wanted string
	for index, source := range sources {
		mapping, err := CompileMap([]byte(source), "M", "domain")
		if err != nil {
			t.Fatalf("source %d: %v", index, err)
		}
		digest, err := mapping.DeterministicModelDigest()
		if err != nil {
			t.Fatal(err)
		}
		if index == 0 {
			wanted = digest
		} else if digest != wanted {
			t.Fatalf("empty iterator source %d digest=%s, want %s", index, digest, wanted)
		}
	}
}

func TestRapideSourceMapFiniteIterationBoundariesAreStable(t *testing.T) {
	interfaces := `
type Domain is interface action out A(); end interface Domain;
type Range is interface action out X(value : Integer); end interface Range;
`
	tests := []struct {
		name   string
		body   string
		parse  bool
		wanted string
	}{
		{"zero still type checks operand", "[0 rel ->] Missing(1)", false, "not declared"},
		{"star is unbounded", "[* rel ->] X(1)", false, "unbounded map poset-generator iteration"},
		{"plus is unbounded", "[+ rel ->] X(1)", false, "unbounded map poset-generator iteration"},
		{"fixed cardinality bound", "[257 rel ->] X(1)", false, "exceeds the deterministic bound"},
		{"named range bound", "[I : 1..257 rel ->] X(I)", false, "must contain at most 256 values"},
		{"disjoint relation", "[2 rel ~] X(1)", false, "outside the finite sequence"},
		{"conjunction relation", "[2 rel and] X(1)", false, "outside the finite sequence"},
		{"equivalence remains nonrestricted", "[2 rel <=>] X(1)", true, "may not contain '<=>'"},
		{"nested expansion bound", "[17 rel ->] ([16 rel ->] X(1))", false, "more than the deterministic bound of 256 event occurrences"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := []byte(interfaces + "map M() from Domain to Range is rule A ||> " + test.body + "; end map M;")
			var err error
			if test.parse {
				_, err = Parse(source)
			} else {
				_, err = CompileMap(source, "M", "domain")
			}
			if err == nil || !strings.Contains(err.Error(), test.wanted) {
				t.Fatalf("error=%v, want substring %q", err, test.wanted)
			}
		})
	}
}

func TestCompileRapideMapExecutesReplaysAndInheritsRangeConstraints(t *testing.T) {
	mapping, err := CompileMap([]byte(sourceMapProgram), "auditview", "domain")
	if err != nil {
		t.Fatal(err)
	}
	domain := gorapide.NewPoset()
	observed := sourceMapDomainEvent(t, "Observed", "observed", nil, 7)
	completed := sourceMapDomainEvent(t, "Completed", "completed", []gorapide.EventID{observed.ID}, 7)
	if err := domain.AddEvent(observed); err != nil {
		t.Fatal(err)
	}
	if err := domain.AddEventWithCause(completed, observed.ID); err != nil {
		t.Fatal(err)
	}
	limits := arch.MapExecutionLimits{MaxFirings: 8, MaxRangeEvents: 8}
	result, err := mapping.ExecuteDeterministic(domain, limits)
	if err != nil {
		t.Fatal(err)
	}
	abstracted := onlySourceMapEvent(t, result.Range.ByName("Abstracted"))
	accepted := onlySourceMapEvent(t, result.Range.ByName("Accepted"))
	if value, ok := abstracted.Param("value"); !ok || value != int64(7) {
		t.Fatalf("Abstracted value=%#v, %t", value, ok)
	}
	if !result.Range.IsCausallyBefore(abstracted.ID, accepted.ID) {
		t.Fatal("strong source-map policy did not transfer domain causality")
	}
	if result.Artifact.Constraints == nil || !result.Artifact.Constraints.Passed {
		t.Fatalf("inherited range constraint report=%#v", result.Artifact.Constraints)
	}
	canonical, err := result.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := result.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := mapping.ReplayDeterministic(domain, limits, digest)
	if err != nil {
		t.Fatal(err)
	}
	replayedCanonical, err := replayed.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(canonical, replayedCanonical) {
		t.Fatalf("source-map replay differs:\nfirst=%s\nreplay=%s", canonical, replayedCanonical)
	}
}

func TestCompileRapideMapIsSourceOrderCaseAndGOMAXPROCSInvariant(t *testing.T) {
	left, err := CompileMap([]byte(sourceMapProgram), "AuditView", "domain")
	if err != nil {
		t.Fatal(err)
	}
	right, err := CompileMap([]byte(sourceMapProgramReordered), "AUDITVIEW", "domain")
	if err != nil {
		t.Fatal(err)
	}
	leftDigest, err := left.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	rightDigest, err := right.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if leftDigest != rightDigest {
		t.Fatalf("equivalent source maps differ: %s != %s", leftDigest, rightDigest)
	}

	domain := gorapide.NewPoset()
	if err := domain.AddEvent(sourceMapDomainEvent(t, "Observed", "one", nil, 4)); err != nil {
		t.Fatal(err)
	}
	limits := arch.MapExecutionLimits{MaxFirings: 4, MaxRangeEvents: 4}
	previous := runtime.GOMAXPROCS(1)
	first, err := left.ExecuteDeterministic(domain, limits)
	runtime.GOMAXPROCS(8)
	second, secondErr := right.ExecuteDeterministic(domain, limits)
	runtime.GOMAXPROCS(previous)
	if err != nil {
		t.Fatal(err)
	}
	if secondErr != nil {
		t.Fatal(secondErr)
	}
	firstBytes, _ := first.MarshalCanonical()
	secondBytes, _ := second.MarshalCanonical()
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatalf("source-map GOMAXPROCS/source ordering changed artifact:\nleft=%s\nright=%s", firstBytes, secondBytes)
	}
}

func TestCompileRapideMapPolicyAndActualDomainAreCanonicalInputs(t *testing.T) {
	strong, err := CompileMap([]byte(sourceMapProgram), "AuditView", "domain")
	if err != nil {
		t.Fatal(err)
	}
	none, err := CompileMapWithOptions([]byte(sourceMapProgram), "AuditView", MapCompileOptions{
		DomainID: "domain", InducedDependencyPolicy: arch.NoneInducedDependencyPolicy,
	})
	if err != nil {
		t.Fatal(err)
	}
	otherDomain, err := CompileMap([]byte(sourceMapProgram), "AuditView", "other-domain")
	if err != nil {
		t.Fatal(err)
	}
	strongDigest, _ := strong.DeterministicModelDigest()
	noneDigest, _ := none.DeterministicModelDigest()
	otherDigest, _ := otherDomain.DeterministicModelDigest()
	if strongDigest == noneDigest || strongDigest == otherDigest || noneDigest == otherDigest {
		t.Fatalf("policy/domain binding missing from model identity: %s %s %s", strongDigest, noneDigest, otherDigest)
	}

	domain := gorapide.NewPoset()
	observed := sourceMapDomainEvent(t, "Observed", "observed", nil, 1)
	completed := sourceMapDomainEvent(t, "Completed", "completed", []gorapide.EventID{observed.ID}, 1)
	if err := domain.AddEvent(observed); err != nil {
		t.Fatal(err)
	}
	if err := domain.AddEventWithCause(completed, observed.ID); err != nil {
		t.Fatal(err)
	}
	limits := arch.MapExecutionLimits{MaxFirings: 8, MaxRangeEvents: 8}
	strongResult, err := strong.ExecuteDeterministic(domain, limits)
	if err != nil {
		t.Fatal(err)
	}
	noneResult, err := none.ExecuteDeterministic(domain, limits)
	if err != nil {
		t.Fatal(err)
	}
	strongAbstracted := onlySourceMapEvent(t, strongResult.Range.ByName("Abstracted"))
	strongAccepted := onlySourceMapEvent(t, strongResult.Range.ByName("Accepted"))
	noneAbstracted := onlySourceMapEvent(t, noneResult.Range.ByName("Abstracted"))
	noneAccepted := onlySourceMapEvent(t, noneResult.Range.ByName("Accepted"))
	if !strongResult.Range.IsCausallyBefore(strongAbstracted.ID, strongAccepted.ID) {
		t.Fatal("strong compiled source map lacks induced dependency")
	}
	if !noneResult.Range.IsCausallyIndependent(noneAbstracted.ID, noneAccepted.ID) {
		t.Fatal("none compiled source map invented an induced dependency")
	}
}

func TestCompileRapideMapNullGeneratorProducesNoRangeOccurrence(t *testing.T) {
	source := []byte(`
type Domain is interface action out Ignore(); end interface Domain;
type Range is interface action out Impossible(); end interface Range;
map Filter() from Domain to Range is rule Ignore ||> ; end map Filter;
`)
	explicitEmptySource := []byte(`
type Range is interface action out Impossible(); end interface Range;
type Domain is interface action out Ignore(); end interface Domain;
map Filter() from Domain to Range is rule Ignore ||> empty(); end map Filter;
`)
	mapping, err := CompileMap(source, "", "domain")
	if err != nil {
		t.Fatal(err)
	}
	explicitEmpty, err := CompileMap(explicitEmptySource, "Filter", "domain")
	if err != nil {
		t.Fatal(err)
	}
	modelDigest, err := mapping.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	explicitModelDigest, err := explicitEmpty.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if modelDigest != explicitModelDigest {
		t.Fatalf("omitted and explicit empty generators differ: %s != %s", modelDigest, explicitModelDigest)
	}
	domain := gorapide.NewPoset()
	if err := domain.AddEvent(sourceMapDomainEvent(t, "Ignore", "ignore", nil, nil)); err != nil {
		t.Fatal(err)
	}
	result, err := mapping.ExecuteDeterministic(domain, arch.MapExecutionLimits{MaxFirings: 2, MaxRangeEvents: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Range.All()) != 1 || len(result.Range.ByName("Start")) != 1 || len(result.Artifact.Firings) != 1 {
		t.Fatalf("null source-map result=%#v, events=%#v", result.Artifact, result.Range.All())
	}
	explicitResult, err := explicitEmpty.ExecuteDeterministic(domain, arch.MapExecutionLimits{MaxFirings: 2, MaxRangeEvents: 2})
	if err != nil {
		t.Fatal(err)
	}
	resultBytes, _ := result.MarshalCanonical()
	explicitBytes, _ := explicitResult.MarshalCanonical()
	if !bytes.Equal(resultBytes, explicitBytes) {
		t.Fatalf("omitted and explicit empty artifacts differ:\nomitted=%s\nexplicit=%s", resultBytes, explicitBytes)
	}
}

func TestCompileRapideMapRangeConstraintViolationIsAuditable(t *testing.T) {
	mapping, err := CompileMap([]byte(sourceMapProgram), "AuditView", "domain")
	if err != nil {
		t.Fatal(err)
	}
	domain := gorapide.NewPoset()
	if err := domain.AddEvent(sourceMapDomainEvent(t, "Observed", "zero", nil, 0)); err != nil {
		t.Fatal(err)
	}
	result, err := mapping.ExecuteDeterministic(domain, arch.MapExecutionLimits{MaxFirings: 4, MaxRangeEvents: 4})
	if err != nil {
		t.Fatal(err)
	}
	if result.Artifact.Constraints == nil || result.Artifact.Constraints.Passed ||
		len(result.Artifact.Constraints.Reports) == 0 {
		t.Fatalf("range constraint violation was not retained: %#v", result.Artifact.Constraints)
	}
}

func TestRapideSourceMapUnsupportedBoundariesAreStable(t *testing.T) {
	interfaces := `
type Domain is interface action out A(); private action Hidden(); end interface Domain;
type Range is interface action out X(); end interface Range;
`
	tests := []struct {
		name    string
		source  string
		compile bool
		want    string
	}{
		{"legacy rules", interfaces + `map M() from Domain to Range is rules A ||> X; end map M;`, false, "legacy pre-1.0"},
		{"declaration", interfaces + `map M() from Domain to Range is state : Integer; rule A ||> X; end map M;`, false, "map declarations and explicit map constraints"},
		{"state assignment", interfaces + `map M() from Domain to Range is rule A ||> state := 1; X; end map M;`, false, "map state assignments"},
		{"equivalence generator", interfaces + `map M() from Domain to Range is rule A ||> X <=> X; end map M;`, false, "may not contain '<=>'"},
		{"join cross-product bound", interfaces + `map M() from Domain to Range is rule A ||> (X || X || X) ~ (X || X); end map M;`, true, "exceeding the deterministic exploration bound"},
		{"nested join", interfaces + `map M() from Domain to Range is rule A ||> X ~ X ~ X; end map M;`, true, "permits one finite arbitrary-choice site"},
		{"join immediate operand", interfaces + `map M() from Domain to Range is rule A ||> (X |> X) ~ X; end map M;`, true, "operands containing immediate sequence"},
		{"conjunction generator", interfaces + `map M() from Domain to Range is rule A ||> X and X; end map M;`, true, "outside the current finite sequence"},
		{"formal parameter", interfaces + `map M(limit : Integer) from Domain to Range is rule A ||> X; end map M;`, true, "formal parameters for non-active module values"},
		{"multiple domains", interfaces + `map M() from Domain, Domain to Range is rule A ||> X; end map M;`, true, "requires exactly one"},
		{"pipe rule", interfaces + `map M() from Domain to Range is rule A => X; end map M;`, true, "require the published '||>'"},
		{"private trigger", interfaces + `map M() from Domain to Range is rule Hidden ||> X; end map M;`, true, "cannot observe private action"},
		{"type application", interfaces + `map M() from Domain(Integer) to Range is rule A ||> X; end map M;`, true, "must be one named interface type"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var err error
			if test.compile {
				_, err = CompileMap([]byte(test.source), "M", "domain")
			} else {
				_, err = Parse([]byte(test.source))
			}
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v, want substring %q", err, test.want)
			}
		})
	}
}

func TestCompileRapideMapRejectsModuleGeneratorIndicatorExplicitly(t *testing.T) {
	source := []byte(`
type Domain is interface action out A(); end interface Domain;
type Range is interface action out X(); end interface Range;
module DomainModule() return Domain is end module DomainModule;
map M() from DomainModule to Range is rule A ||> X; end map M;
`)
	_, err := CompileMap(source, "M", "domain")
	if err == nil || !strings.Contains(err.Error(), "module-generator domains are outside") {
		t.Fatalf("module-generator map domain boundary=%v", err)
	}
}

func sourceMapDomainEvent(
	t *testing.T,
	action, occurrence string,
	causes []gorapide.EventID,
	value any,
) *gorapide.Event {
	t.Helper()
	parameters := map[string]any(nil)
	if value != nil {
		parameters = map[string]any{"value": value}
	}
	event, err := gorapide.NewDeterministicEvent(gorapide.EventProvenance{
		Profile: arch.CompatibilityProfile,
		Model:   "source-map-domain-model", Instance: "domain",
		Action: action, Occurrence: occurrence, Causes: causes,
	}, parameters)
	if err != nil {
		t.Fatal(err)
	}
	return event
}

func onlySourceMapEvent(t *testing.T, events gorapide.EventSet) *gorapide.Event {
	t.Helper()
	if len(events) != 1 {
		t.Fatalf("events=%d, want 1", len(events))
	}
	return events[0]
}

func sourceMapEventByInteger(t *testing.T, events gorapide.EventSet, wanted int64) *gorapide.Event {
	t.Helper()
	for _, event := range events {
		value, exists := event.Param("value")
		if exists && value == wanted {
			return event
		}
	}
	t.Fatalf("no event with value %d in %#v", wanted, events)
	return nil
}

func assertSourceMapDirectCauses(
	t *testing.T,
	poset *gorapide.Poset,
	event *gorapide.Event,
	wanted ...gorapide.EventID,
) {
	t.Helper()
	causes := poset.DirectCauses(event.ID)
	if len(causes) != len(wanted) {
		t.Fatalf("%s direct causes=%#v, want %#v", event.Name, causes, wanted)
	}
	remaining := make(map[gorapide.EventID]bool, len(wanted))
	for _, id := range wanted {
		remaining[id] = true
	}
	for _, cause := range causes {
		if !remaining[cause.ID] {
			t.Fatalf("%s direct causes=%#v, want %#v", event.Name, causes, wanted)
		}
		delete(remaining, cause.ID)
	}
}

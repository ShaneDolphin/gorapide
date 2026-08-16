package rapide

import (
	"bytes"
	"runtime"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide/arch"
)

func functionDefaultSource(call string, actionCall ...string) []byte {
	output := "Pair(Right is $total, Left is 3)"
	if len(actionCall) != 0 {
		output = actionCall[0]
	}
	return []byte(`
type Worker is interface
  action out Pair(Left, Right : Integer);
  provides Sum : function(Left : Integer; Right : Integer is 2) return Integer;
end interface Worker;
module Pairer() return Worker is
  total : var Integer := 0;
  Sum : function(Left : Integer; Right : Integer) return Integer is
  begin
    return Left + Right;
  end function Sum;
initial
  total := ` + call + `;
  ` + output + `;
end module Pairer;
architecture System() is
  worker : Worker is Pairer();
end architecture System;
`)
}

func TestFunctionDefaultsAndNamedCallsNormalizeToOneComputation(t *testing.T) {
	variants := []struct {
		function string
		action   string
	}{
		{function: "Sum(3)", action: "Pair(3, $total)"},
		{function: "Sum(Left is 3, Right is 2)", action: "Pair(Left is 3, Right is $total)"},
		{function: "Sum(RIGHT is 2, LEFT is 3)", action: "Pair(RIGHT is $total, LEFT is 3)"},
	}
	models := make([]*arch.Architecture, len(variants))
	digests := make([]string, len(variants))
	for index, variant := range variants {
		model, err := Compile(functionDefaultSource(variant.function, variant.action), "System")
		if err != nil {
			t.Fatalf("compile %s / %s: %v", variant.function, variant.action, err)
		}
		models[index] = model
		digests[index], err = model.DeterministicModelDigest()
		if err != nil {
			t.Fatal(err)
		}
		if digests[index] != digests[0] {
			t.Fatalf("call spelling %s / %s changed model identity: %s != %s", variant.function, variant.action, digests[index], digests[0])
		}
	}

	previous := runtime.GOMAXPROCS(1)
	first, err := models[0].ExecuteDeterministic(arch.NewExecutionJournal(digests[0], 20))
	if err != nil {
		runtime.GOMAXPROCS(previous)
		t.Fatal(err)
	}
	runtime.GOMAXPROCS(8)
	third, err := models[2].ExecuteDeterministic(arch.NewExecutionJournal(digests[2], 20))
	runtime.GOMAXPROCS(previous)
	if err != nil {
		t.Fatal(err)
	}
	pair := first.Poset.ByName("Pair")
	if len(pair) != 1 || pair[0].ParamInt("Left") != 3 || pair[0].ParamInt("Right") != 5 {
		t.Fatalf("defaulted/named call output=%#v", pair)
	}
	callEvents := first.Poset.ByName("Sum'Call")
	if len(callEvents) != 1 || callEvents[0].ParamInt("Left") != 3 || callEvents[0].ParamInt("Right") != 2 {
		t.Fatalf("default was not materialized before F'Call: %#v", callEvents)
	}
	firstBytes, err := first.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	thirdBytes, err := third.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstBytes, thirdBytes) {
		t.Fatal("equivalent named/defaulted calls or GOMAXPROCS changed artifact bytes")
	}
	digest, err := first.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := models[0].ReplayDeterministic(arch.NewExecutionJournal(digests[0], 20), digest)
	if err != nil {
		t.Fatal(err)
	}
	replayedBytes, err := replayed.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstBytes, replayedBytes) {
		t.Fatal("defaulted function call replay changed canonical artifact bytes")
	}
}

func TestFunctionDefaultIsCanonicalSignatureData(t *testing.T) {
	digest := func(value string) string {
		t.Helper()
		source := strings.ReplaceAll(string(functionDefaultSource("Sum(3, 2)")), "Right : Integer is 2", "Right : Integer is "+value)
		model, err := Compile([]byte(source), "System")
		if err != nil {
			t.Fatal(err)
		}
		result, err := model.DeterministicModelDigest()
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	if digest("2") == digest("4") {
		t.Fatal("changing an unused function default did not change canonical model identity")
	}
}

func TestFunctionDefaultAndCallAssociationDiagnosticsAreExplicit(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "wrong default type", body: "Left : Integer; Right : Integer is True", want: "default has type Boolean, want Integer"},
		{name: "unknown formal", body: "Left : Integer; Right : Integer is 2", want: "matches 0 compatible typed signatures"},
		{name: "missing required", body: "Left : Integer; Right : Integer", want: "matches 0 compatible typed signatures"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			call := "Sum(Unknown is 3)"
			if test.name == "wrong default type" {
				call = "Sum(3)"
			}
			if test.name == "missing required" {
				call = "Sum()"
			}
			source := strings.ReplaceAll(string(functionDefaultSource(call)), "Left : Integer; Right : Integer is 2", test.body)
			_, err := Compile([]byte(source), "System")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("diagnostic=%v, want %q", err, test.want)
			}
		})
	}

	for _, test := range []struct {
		call string
		want string
	}{
		{call: "Sum(Left is 3, 2)", want: "positional call arguments must precede named associations"},
		{call: "Sum(Left is 3, LEFT is 2)", want: "duplicate named call association"},
	} {
		_, err := Parse(functionDefaultSource(test.call))
		if err == nil || !strings.Contains(err.Error(), test.want) {
			t.Fatalf("parse diagnostic for %s=%v, want %q", test.call, err, test.want)
		}
	}

	bodySignature := "Sum : function(Left : Integer; Right : Integer) return Integer"
	equivalentBodyDefault := strings.ReplaceAll(
		string(functionDefaultSource("Sum(3)")), bodySignature,
		"Sum : function(Left : Integer; Right : Integer is 1 + 1) return Integer",
	)
	if _, err := Compile([]byte(equivalentBodyDefault), "System"); err != nil {
		t.Fatalf("equivalent repeated body default failed: %v", err)
	}
	mismatchedBodyDefault := strings.ReplaceAll(
		string(functionDefaultSource("Sum(3)")), bodySignature,
		"Sum : function(Left : Integer; Right : Integer is 3) return Integer",
	)
	if _, err := Compile([]byte(mismatchedBodyDefault), "System"); err == nil ||
		!strings.Contains(err.Error(), "does not denote the provided interface default") {
		t.Fatalf("mismatched body default diagnostic=%v", err)
	}
}

func TestSourceFunctionConnectionAdaptsProviderExtraDefault(t *testing.T) {
	source := []byte(`
type Client is interface
  action out Start(n : Integer);
  action out Done(n : Integer);
  requires Add : function(n : Integer) return Integer;
  behavior
    result : var Integer := 0;
  begin
    (?N : Integer) Start(?N) =>
      result := Add(?N);
      Done($result);
    ;
end interface Client;
type Provider is interface
  provides Sum : function(value : Integer; bonus : Integer is 2) return Integer;
  behavior
    Sum : function(value : Integer; bonus : Integer) return Integer is
    begin
      return value + bonus;
    end function Sum;
  begin
end interface Provider;
architecture System() is
  client : Client;
  provider : Provider;
connect
  client.Add to provider.Sum;
end architecture System;
`)
	model, err := Compile(source, "System")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 20, arch.InputEvent{
		Key: "start", Source: "client", Action: "Start", Params: map[string]any{"n": 3},
	}))
	if err != nil {
		t.Fatal(err)
	}
	providedCall := result.Poset.ByName("Sum'Call")
	requiredCall := result.Poset.ByName("Add'Call")
	done := result.Poset.ByName("Done")
	if len(providedCall) != 1 || len(requiredCall) != 1 || len(done) != 1 ||
		providedCall[0].ParamInt("bonus") != 2 || done[0].ParamInt("n") != 5 {
		t.Fatalf("source default-adapting route call/done=%#v/%#v/%#v", requiredCall, providedCall, done)
	}
	if _, exists := requiredCall[0].Param("bonus"); exists {
		t.Fatalf("provider-only default leaked into required view: %#v", requiredCall[0].Params)
	}

	invalid := strings.ReplaceAll(string(source), "bonus : Integer is 2", "bonus : Integer")
	if _, err := Compile([]byte(invalid), "System"); err == nil ||
		!strings.Contains(err.Error(), "0 type-compatible provided signatures") {
		t.Fatalf("source nondefaulted provider-extra diagnostic=%v", err)
	}
}

func TestSourceFunctionConnectionsUsePredefinedVariance(t *testing.T) {
	build := func(providerParameter, providerReturn string) []byte {
		return []byte(`
type Client is interface
  action out Start(n : Positive);
  action out Done(n : Integer);
  requires Convert : function(n : Positive) return Integer;
  behavior
    result : var Integer := 0;
  begin
    (?N : Positive) Start(?N) =>
      result := Convert(?N);
      Done($result);
    ;
end interface Client;
type Provider is interface
  provides Adapt : function(value : ` + providerParameter + `) return ` + providerReturn + `;
  behavior
    Adapt : function(value : ` + providerParameter + `) return ` + providerReturn + ` is
    begin
      return value;
    end function Adapt;
  begin
end interface Provider;
architecture System() is client : Client; provider : Provider;
connect client.Convert to provider.Adapt;
end architecture System;
`)
	}
	for _, test := range []struct {
		name              string
		providerParameter string
		providerReturn    string
	}{
		{name: "contravariant parameter", providerParameter: "Integer", providerReturn: "Integer"},
		{name: "covariant result", providerParameter: "Positive", providerReturn: "Positive"},
	} {
		t.Run(test.name, func(t *testing.T) {
			model, err := Compile(build(test.providerParameter, test.providerReturn), "System")
			if err != nil {
				t.Fatal(err)
			}
			digest, err := model.DeterministicModelDigest()
			if err != nil {
				t.Fatal(err)
			}
			result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 20, arch.InputEvent{
				Key: "start", Source: "client", Action: "Start", Params: map[string]any{"n": 3},
			}))
			if err != nil {
				t.Fatal(err)
			}
			done := result.Poset.ByName("Done")
			if len(done) != 1 || done[0].ParamInt("n") != 3 {
				t.Fatalf("variant source result=%#v", done)
			}
		})
	}

	invalid := strings.ReplaceAll(string(build("Integer", "Integer")),
		"requires Convert : function(n : Positive)", "requires Convert : function(n : Integer)")
	invalid = strings.ReplaceAll(invalid,
		"action out Start(n : Positive)", "action out Start(n : Integer)")
	invalid = strings.ReplaceAll(invalid,
		"(?N : Positive) Start(?N)", "(?N : Integer) Start(?N)")
	invalid = strings.ReplaceAll(invalid,
		"provides Adapt : function(value : Integer)", "provides Adapt : function(value : Positive)")
	invalid = strings.ReplaceAll(invalid,
		"Adapt : function(value : Integer)", "Adapt : function(value : Positive)")
	invalid = strings.ReplaceAll(invalid, "return value;", "return 1;")
	if _, err := Compile([]byte(invalid), "System"); err == nil ||
		!strings.Contains(err.Error(), "0 type-compatible provided signatures") {
		t.Fatalf("reversed source parameter variance diagnostic=%v", err)
	}
}

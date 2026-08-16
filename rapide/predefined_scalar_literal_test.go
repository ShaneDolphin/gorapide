package rapide

import (
	"bytes"
	"math"
	"runtime"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide/arch"
)

func predefinedScalarLiteralSource(floatSpelling, stringSpelling string) []byte {
	return []byte(`
type Driver is interface action out Set(rate : Float; label : String); end interface Driver;
type Worker is interface
  Rate : Float;
  Label : String;
  action in Set(rate : Float; label : String);
  action out Values(
    generator_rate : Float; generator_label : String;
    object_rate : Float; object_label : String;
    state_rate : Float; state_label : String;
    default_rate : Float; default_label : String
  );
  action out Changed(rate : Float; label : String);
  provides Echo : function(input_rate : Float is ` + floatSpelling + `; input_label : String is ` + stringSpelling + `) return String;
end interface Worker;
module Store(Rate_Arg : Float is ` + floatSpelling + `; Label_Arg : String is ` + stringSpelling + `) return Worker is
  Rate : Float is ` + floatSpelling + `;
  Label : String is ` + stringSpelling + `;
  last_rate : var Float := -0.5;
  last_label : var String := "";
  Echo : function(input_rate : Float; input_label : String) return String is
  begin
    return input_label;
  end function Echo;
initial
  last_label := Echo();
  Values(Rate_Arg, Label_Arg, Rate, Label, $last_rate, $last_label, ` + floatSpelling + `, ` + stringSpelling + `);
serial
  when Set(` + floatSpelling + `, ` + stringSpelling + `)
    where $last_label = ` + stringSpelling + ` and $last_rate = -0.5 do
    last_rate := -` + floatSpelling + `;
    last_label := "changed";
    Changed($last_rate, $last_label);
  end when;
end module Store;
architecture System(Rate : Float is ` + floatSpelling + `; Label : String is ` + stringSpelling + `) is
  defaulted_driver : Driver;
  explicit_driver : Driver;
  defaulted : Worker is Store();
  explicit : Worker is Store(Rate, Label);
connect
  defaulted_driver.Set => defaulted.Set;
  explicit_driver.Set => explicit.Set;
end architecture System;
`)
}

func TestPredefinedScalarLiteralsExecuteAndReplayCanonically(t *testing.T) {
	source := predefinedScalarLiteralSource("1.25", `"alpha value"`)
	implicit, err := Compile(source, "System")
	if err != nil {
		t.Fatal(err)
	}
	explicit, err := CompileWithArguments(source, "system", map[string]any{
		"rate": float32(1.25), "LABEL": "alpha value",
	})
	if err != nil {
		t.Fatal(err)
	}
	implicitDigest, err := implicit.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	explicitDigest, err := explicit.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if implicitDigest != explicitDigest {
		t.Fatalf("scalar defaults and explicit actuals differ: %s != %s", implicitDigest, explicitDigest)
	}
	journal := arch.NewExecutionJournal(implicitDigest, 40,
		arch.InputEvent{Key: "defaulted-set", Source: "defaulted_driver", Action: "Set", Params: map[string]any{"rate": 1.25, "label": "alpha value"}},
		arch.InputEvent{Key: "explicit-set", Source: "explicit_driver", Action: "Set", Params: map[string]any{"rate": float32(1.25), "label": "alpha value"}},
	)

	previous := runtime.GOMAXPROCS(1)
	first, err := implicit.ExecuteDeterministic(journal)
	if err != nil {
		runtime.GOMAXPROCS(previous)
		t.Fatal(err)
	}
	runtime.GOMAXPROCS(8)
	second, err := explicit.ExecuteDeterministic(journal)
	runtime.GOMAXPROCS(previous)
	if err != nil {
		t.Fatal(err)
	}

	values := first.Poset.ByName("Values")
	if len(values) != 2 {
		t.Fatalf("Values events=%#v, want two", values)
	}
	for _, event := range values {
		for _, parameter := range []string{"generator_rate", "object_rate", "default_rate"} {
			value, ok := event.Param(parameter)
			if !ok || math.Float64bits(value.(float64)) != math.Float64bits(1.25) {
				t.Fatalf("Values.%s=%#v, want canonical 1.25", parameter, value)
			}
		}
		for _, parameter := range []string{"generator_label", "object_label", "state_label", "default_label"} {
			value, ok := event.Param(parameter)
			if !ok || value != "alpha value" {
				t.Fatalf("Values.%s=%#v, want alpha value", parameter, value)
			}
		}
		stateRate, _ := event.Param("state_rate")
		if math.Float64bits(stateRate.(float64)) != math.Float64bits(-0.5) {
			t.Fatalf("Values.state_rate=%#v, want -0.5", stateRate)
		}
	}
	changed := first.Poset.ByName("Changed")
	if len(changed) != 2 {
		t.Fatalf("Changed events=%#v, want two literal-filter matches", changed)
	}
	for _, event := range changed {
		rate, _ := event.Param("rate")
		label, _ := event.Param("label")
		if math.Float64bits(rate.(float64)) != math.Float64bits(-1.25) || label != "changed" {
			t.Fatalf("Changed event=%#v", event)
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
		t.Fatal("float width, argument spelling, or GOMAXPROCS changed scalar artifact bytes")
	}
	artifactDigest, err := first.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := implicit.ReplayDeterministic(journal, artifactDigest)
	if err != nil {
		t.Fatal(err)
	}
	replayedBytes, err := replayed.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstBytes, replayedBytes) {
		t.Fatal("predefined scalar replay changed canonical artifact bytes")
	}
}

func TestEquivalentScalarLiteralSpellingsHaveOneModelIdentity(t *testing.T) {
	first, err := Compile(predefinedScalarLiteralSource("1.25", `"alpha value"`), "System")
	if err != nil {
		t.Fatal(err)
	}
	second, err := Compile(predefinedScalarLiteralSource("1.250e+0", `"alpha value"`), "System")
	if err != nil {
		t.Fatal(err)
	}
	firstDigest, err := first.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	secondDigest, err := second.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if firstDigest != secondDigest {
		t.Fatalf("equivalent decimal Float spellings changed model identity: %s != %s", firstDigest, secondDigest)
	}
}

func TestScalarLiteralsSelectExactCaseChoices(t *testing.T) {
	source := []byte(`
type Selector is interface
  action out Choose_Float(value : Float);
  action out Choose_String(value : String);
  action out Selected(kind : String);
  behavior begin
    (?F : Float) Choose_Float(?F) =>
      case ?F of
        1.25 => Selected("float");
        default => Selected("other");
      end case;
    ;
    (?S : String) Choose_String(?S) =>
      case ?S of
        "alpha" => Selected("string");
        default => Selected("other");
      end case;
    ;
  end interface Selector;
architecture System() is selector : Selector; end architecture System;
`)
	model, err := Compile(source, "System")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 20,
		arch.InputEvent{Key: "float", Source: "selector", Action: "Choose_Float", Params: map[string]any{"value": 1.25}},
		arch.InputEvent{Key: "string", Source: "selector", Action: "Choose_String", Params: map[string]any{"value": "alpha"}},
	))
	if err != nil {
		t.Fatal(err)
	}
	selected := result.Poset.ByName("Selected")
	if len(selected) != 2 {
		t.Fatalf("Selected events=%#v, want two", selected)
	}
	seen := map[string]bool{}
	for _, event := range selected {
		kind, _ := event.Param("kind")
		seen[kind.(string)] = true
	}
	if !seen["float"] || !seen["string"] || len(seen) != 2 {
		t.Fatalf("scalar case choices selected=%#v", seen)
	}
}

func TestScalarLiteralDiagnosticsAreExplicit(t *testing.T) {
	for _, test := range []struct {
		name   string
		source string
		want   string
	}{
		{name: "missing exponent", source: `architecture System(F : Float is 1.0e) is end architecture System;`, want: "exponent requires at least one decimal digit"},
		{name: "non-finite exponent", source: `architecture System(F : Float is 1.0e400) is end architecture System;`, want: "finite IEEE-754 binary64"},
		{name: "unterminated string", source: "architecture System(S : String is \"open) is end architecture System;", want: "unterminated string literal"},
		{name: "unsupported string escape", source: `architecture System(S : String is "a\t") is end architecture System;`, want: "string literal has an unsupported escape"},
		{name: "float default mismatch", source: `architecture System(S : String is 1.0) is end architecture System;`, want: "default has type Float, want String"},
		{name: "string default mismatch", source: `architecture System(F : Float is "one") is end architecture System;`, want: "default has type String, want Float"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := Compile([]byte(test.source), "System")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("diagnostic=%v, want %q", err, test.want)
			}
		})
	}
}

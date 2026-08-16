package gorapide

import (
	"bytes"
	"strings"
	"testing"
)

func TestRapideInterfaceExceptionIsCanonicalStructuralConstituent(t *testing.T) {
	integer := mustRapidePredefinedType(t, "Integer")
	event := mustRapideEventType(t, RapideEventParam("Code", integer))
	typ := mustRapideInterfaceType(t,
		RequiredRapideException("Retry", event),
		ProvidedRapideException("Failure", event),
		PrivateRapideException("Hidden", event),
	)

	encoded := mustMarshalRapideType(t, typ)
	text := string(encoded)
	for _, fragment := range []string{
		`"format":"gorapide.rapide-type.v2"`,
		`"kind":"exception","region":"provides","name":"failure"`,
		`"kind":"exception","region":"private","name":"hidden"`,
		`"kind":"exception","region":"requires","name":"retry"`,
	} {
		if !strings.Contains(text, fragment) {
			t.Fatalf("canonical exception descriptor lacks %s: %s", fragment, text)
		}
	}
	roundTrip, err := ParseRapideType(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if repeated := mustMarshalRapideType(t, roundTrip); !bytes.Equal(encoded, repeated) {
		t.Fatalf("exception type round trip changed bytes:\n%s\n%s", encoded, repeated)
	}
}

func TestRapideInterfaceExceptionUsesRegionAndEventSubtypeRules(t *testing.T) {
	integer := mustRapidePredefinedType(t, "Integer")
	narrow := mustRapideEventType(t, RapideEventParam("Code", integer))
	wide := mustRapideEventType(t,
		RapideEventParam("Code", integer),
		RapideEventParam("Detail", integer),
	)

	providedWide := mustRapideInterfaceType(t, ProvidedRapideException("Failure", wide))
	providedNarrow := mustRapideInterfaceType(t, ProvidedRapideException("Failure", narrow))
	if !mustRapideSubtype(t, providedWide, providedNarrow) {
		t.Fatal("provided exception did not use covariant event-record subtyping")
	}
	if mustRapideSubtype(t, providedNarrow, providedWide) {
		t.Fatal("narrow provided exception unexpectedly supplies the wider event record")
	}

	privateTarget := mustRapideInterfaceType(t, PrivateRapideException("Failure", narrow))
	if !mustRapideSubtype(t, providedWide, privateTarget) {
		t.Fatal("provided exception did not satisfy a private exception constituent")
	}

	requiredNarrow := mustRapideInterfaceType(t, RequiredRapideException("Failure", narrow))
	requiredWide := mustRapideInterfaceType(t, RequiredRapideException("Failure", wide))
	if !mustRapideSubtype(t, requiredNarrow, requiredWide) {
		t.Fatal("required exception did not apply the reversed interface-requirement rule")
	}
}

func TestRapideServiceQualifiesAndDualizesExceptionConstituents(t *testing.T) {
	integer := mustRapidePredefinedType(t, "Integer")
	event := mustRapideEventType(t, RapideEventParam("Code", integer))
	service := mustRapideInterfaceType(t,
		ProvidedRapideException("Failure", event),
		RequiredRapideException("Retry", event),
	)

	ordinary, err := RapideServiceInterfaceType("API", service)
	if err != nil {
		t.Fatal(err)
	}
	dual, err := RapideDualServiceInterfaceType("API", service)
	if err != nil {
		t.Fatal(err)
	}
	ordinaryText := string(mustMarshalRapideType(t, ordinary))
	dualText := string(mustMarshalRapideType(t, dual))
	if !strings.Contains(ordinaryText, `"region":"provides","name":"api.failure"`) ||
		!strings.Contains(ordinaryText, `"region":"requires","name":"api.retry"`) {
		t.Fatalf("ordinary service exception rewrite=%s", ordinaryText)
	}
	if !strings.Contains(dualText, `"region":"requires","name":"api.failure"`) ||
		!strings.Contains(dualText, `"region":"provides","name":"api.retry"`) {
		t.Fatalf("dual service exception rewrite=%s", dualText)
	}
}

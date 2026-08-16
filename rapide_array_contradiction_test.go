package gorapide

import "testing"

func TestPublishedArrayDefinitionConflictsWithPublishedIndexVariance(t *testing.T) {
	stringType := mustRapidePredefinedType(t, "String")
	floatType := mustRapidePredefinedType(t, "Float")
	baseIndex := mustRapideInterfaceType(t,
		ProvidedRapideMember("Name", stringType),
	)
	narrowIndex := mustRapideInterfaceType(t,
		ProvidedRapideMember("Name", stringType),
		ProvidedRapideMember("Department", stringType),
	)
	if !mustRapideSubtype(t, narrowIndex, baseIndex) {
		t.Fatal("test prerequisite NarrowIndex <: BaseIndex failed")
	}
	baseArray := mustPublishedDraftArrayType(t, baseIndex, floatType)
	narrowArray := mustPublishedDraftArrayType(t, narrowIndex, floatType)

	// Section 14.1.4 says Array(BaseIndex, T) <: Array(NarrowIndex, T)
	// because NarrowIndex <: BaseIndex. The exact Section 14.1.2 interface
	// expansion says otherwise: [] supports that contravariant direction, but
	// included Iterable(Index) simultaneously requires BaseIndex <: NarrowIndex.
	if subtype, err := IsRapideSubtype(baseArray, narrowArray); err != nil || subtype {
		t.Fatalf("draft Array definition unexpectedly satisfied the contradictory stated variance: %v, %v", subtype, err)
	}
	if subtype, err := IsRapideSubtype(narrowArray, baseArray); err != nil || subtype {
		t.Fatalf("reverse draft Array direction unexpectedly subtyped: %v, %v", subtype, err)
	}
}

func mustPublishedDraftArrayType(t *testing.T, index, element RapideType) RapideType {
	t.Helper()
	iterator, err := RapideIteratorType(index)
	if err != nil {
		t.Fatal(err)
	}
	iteratorGenerator, err := NewRapideFunctionType(nil, iterator)
	if err != nil {
		t.Fatal(err)
	}
	lookup, err := NewRapideFunctionType(
		[]RapideFunctionParameter{RapideObjectParameter("I", index)}, element,
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := NewRapideInterfaceType(
		ProvidedRapideModuleGenerator("Iterator", iteratorGenerator),
		ProvidedRapideMember("[]", lookup),
	)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

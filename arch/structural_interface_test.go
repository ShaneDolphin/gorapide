package arch

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide"
)

func TestStructuralRapideTypeIsCanonicalModelContent(t *testing.T) {
	stringType, err := gorapide.RapidePredefinedType("String")
	if err != nil {
		t.Fatal(err)
	}
	structural, err := gorapide.NewRapideInterfaceType(
		gorapide.ExactProvidedRapideTypeName("Element", stringType),
		gorapide.ProvidedRapideMember("Name", stringType),
	)
	if err != nil {
		t.Fatal(err)
	}
	build := func(withStructural bool) *Architecture {
		model := NewArchitecture("System")
		builder := Interface("Schema")
		if withStructural {
			builder.StructuralType(structural)
		}
		if err := model.AddComponent(NewComponent("schema", builder.Build(), nil)); err != nil {
			t.Fatal(err)
		}
		return model
	}
	with := build(true)
	without := build(false)
	withDigest, err := with.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	withoutDigest, err := without.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if withDigest == withoutDigest {
		t.Fatal("structural type did not change canonical architecture identity")
	}
	got, ok := with.Component("schema")
	if !ok {
		t.Fatal("component absent")
	}
	attached, ok := got.Interface.StructuralRapideType()
	if !ok {
		t.Fatal("structural type absent")
	}
	left, _ := structural.MarshalCanonical()
	right, _ := attached.MarshalCanonical()
	if !bytes.Equal(left, right) {
		t.Fatal("attached immutable structural descriptor changed")
	}
}

func TestStructuralRapideTypeRejectsZeroDescriptor(t *testing.T) {
	model := NewArchitecture("System")
	iface := Interface("Schema").StructuralType(gorapide.RapideType{}).Build()
	if err := model.AddComponent(NewComponent("schema", iface, nil)); err != nil {
		t.Fatal(err)
	}
	_, err := model.DeterministicModelDigest()
	if err == nil || !strings.Contains(err.Error(), "zero type descriptor") {
		t.Fatalf("got %v, want zero structural type error", err)
	}
}

func TestStructuralActionParametersSupportPublicTransportOnly(t *testing.T) {
	stringType, err := gorapide.RapidePredefinedType("String")
	if err != nil {
		t.Fatal(err)
	}
	structural, err := gorapide.NewRapideInterfaceType(
		gorapide.ProvidedRapideMember("Name", stringType),
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name  string
		build func(*InterfaceDeclBuilder)
		ok    bool
	}{
		{name: "out", build: func(builder *InterfaceDeclBuilder) {
			builder.OutAction("Send", PStructural("value", "Value", structural))
		}, ok: true},
		{name: "in", build: func(builder *InterfaceDeclBuilder) {
			builder.InAction("Receive", PStructural("value", "Value", structural))
		}, ok: true},
		{name: "private", build: func(builder *InterfaceDeclBuilder) {
			builder.PrivateAction("Keep", PStructural("value", "Value", structural))
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			builder := Interface("Carrier")
			test.build(builder)
			model := NewArchitecture("System")
			if err := model.AddComponent(NewComponent("carrier", builder.Build(), nil)); err != nil {
				t.Fatal(err)
			}
			_, err := model.DeterministicModelDigest()
			if test.ok {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), "supports public in/out actions only") {
				t.Fatalf("got %v, want explicit structural action direction boundary", err)
			}
		})
	}
}

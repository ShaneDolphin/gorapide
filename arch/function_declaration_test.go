package arch

import (
	"errors"
	"strings"
	"testing"
)

func functionDeclarationArchitecture(t *testing.T, reverse bool) *Architecture {
	t.Helper()
	builder := Interface("Storage")
	if reverse {
		builder.
			RequiresFunction("Write", "", P("key", "String"), P("value", "Integer")).
			ProvidesFunction("Read", "Integer", P("key", "String"))
	} else {
		builder.
			ProvidesFunction("Read", "Integer", P("key", "String")).
			RequiresFunction("Write", "", P("key", "String"), P("value", "Integer"))
	}
	builder.Service("Transactions", func(service *ServiceBuilder) {
		if reverse {
			service.RequiresFunction("Rollback", "", P("id", "String"))
			service.ProvidesFunction("Commit", "Boolean", P("id", "String"))
		} else {
			service.ProvidesFunction("Commit", "Boolean", P("id", "String"))
			service.RequiresFunction("Rollback", "", P("id", "String"))
		}
	})
	architecture := NewArchitecture("function-declarations")
	if err := architecture.AddComponent(NewComponent("storage", builder.Build(), nil)); err != nil {
		t.Fatal(err)
	}
	return architecture
}

func TestFunctionDeclarationsAreCanonicalAndOrderIndependent(t *testing.T) {
	forward, err := functionDeclarationArchitecture(t, false).DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	reverse, err := functionDeclarationArchitecture(t, true).DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if forward != reverse {
		t.Fatalf("function declaration order changed model digest: %s != %s", forward, reverse)
	}
}

func TestFunctionDirectionParametersAndReturnTypeAffectModelIdentity(t *testing.T) {
	digest := func(function FunctionDecl) string {
		t.Helper()
		architecture := NewArchitecture("function-identity")
		iface := &InterfaceDecl{Name: "API", Functions: []FunctionDecl{function}}
		if err := architecture.AddComponent(NewComponent("api", iface, nil)); err != nil {
			t.Fatal(err)
		}
		value, err := architecture.DeterministicModelDigest()
		if err != nil {
			t.Fatal(err)
		}
		return value
	}

	base := FunctionDecl{Name: "Lookup", Kind: ProvidesFunction, Params: []ParamDecl{P("key", "String")}, ReturnType: "Integer"}
	variants := []FunctionDecl{
		{Name: "Lookup", Kind: RequiresFunction, Params: []ParamDecl{P("key", "String")}, ReturnType: "Integer"},
		{Name: "Lookup", Kind: ProvidesFunction, Params: []ParamDecl{P("key", "Integer")}, ReturnType: "Integer"},
		{Name: "Lookup", Kind: ProvidesFunction, Params: []ParamDecl{P("key", "String")}, ReturnType: "Boolean"},
		{Name: "Lookup", Kind: ProvidesFunction, Params: []ParamDecl{P("renamed", "String")}, ReturnType: "Integer"},
	}
	baseDigest := digest(base)
	for _, variant := range variants {
		if got := digest(variant); got == baseDigest {
			t.Fatalf("function declaration distinction was erased: %#v", variant)
		}
	}
}

func TestFunctionDeclarationsAllowOverloadsAndNoReturnObject(t *testing.T) {
	architecture := NewArchitecture("function-overloads")
	iface := Interface("API").
		ProvidesFunction("Convert", "String", P("value", "Integer")).
		ProvidesFunction("Convert", "Integer", P("value", "String")).
		RequiresFunction("Notify", "", P("message", "String")).
		Build()
	if err := architecture.AddComponent(NewComponent("api", iface, nil)); err != nil {
		t.Fatal(err)
	}
	if _, err := architecture.DeterministicModelDigest(); err != nil {
		t.Fatal(err)
	}
}

func TestFunctionDeclarationValidationIsDeterministic(t *testing.T) {
	tests := []struct {
		name      string
		functions []FunctionDecl
		want      string
		isTypeErr bool
	}{
		{name: "empty name", functions: []FunctionDecl{{Kind: ProvidesFunction}}, want: "function name is empty"},
		{name: "invalid kind", functions: []FunctionDecl{{Name: "F", Kind: FunctionKind(99)}}, want: "invalid kind 99"},
		{name: "empty parameter name", functions: []FunctionDecl{{Name: "F", Params: []ParamDecl{P("", "Integer")}}}, want: "incomplete parameter"},
		{name: "empty parameter type", functions: []FunctionDecl{{Name: "F", Params: []ParamDecl{P("n", "")}}}, want: "incomplete parameter"},
		{name: "duplicate parameter", functions: []FunctionDecl{{Name: "F", Params: []ParamDecl{P("n", "Integer"), P("n", "Integer")}}}, want: "duplicate parameter"},
		{name: "implicit return collision", functions: []FunctionDecl{{Name: "F", Params: []ParamDecl{P("return", "Integer")}, ReturnType: "Integer"}}, want: "implicit return-event parameter"},
		{name: "unsupported parameter", functions: []FunctionDecl{{Name: "F", Params: []ParamDecl{P("n", "HostPointer")}}}, want: "HostPointer", isTypeErr: true},
		{name: "unsupported return", functions: []FunctionDecl{{Name: "F", ReturnType: "HostPointer"}}, want: "HostPointer", isTypeErr: true},
		{name: "exact duplicate", functions: []FunctionDecl{{Name: "F"}, {Name: "F"}}, want: "duplicate function declaration"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			architecture := NewArchitecture("invalid-function")
			iface := &InterfaceDecl{Name: "API", Functions: test.functions}
			if err := architecture.AddComponent(NewComponent("api", iface, nil)); err != nil {
				t.Fatal(err)
			}
			_, err := architecture.DeterministicModelDigest()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v, want error containing %q", err, test.want)
			}
			if test.isTypeErr && !errors.Is(err, ErrUnsupportedRapideType) {
				t.Fatalf("got %v, want ErrUnsupportedRapideType", err)
			}
		})
	}
}

func TestServiceFunctionDeclarationValidation(t *testing.T) {
	architecture := NewArchitecture("invalid-service-function")
	iface := &InterfaceDecl{Name: "API", Services: []ServiceDecl{{
		Name: "S",
		Functions: []FunctionDecl{
			{Name: "F", Kind: RequiresFunction, ReturnType: "Integer"},
			{Name: "F", Kind: RequiresFunction, ReturnType: "Integer"},
		},
	}}}
	if err := architecture.AddComponent(NewComponent("api", iface, nil)); err != nil {
		t.Fatal(err)
	}
	_, err := architecture.DeterministicModelDigest()
	if err == nil || !strings.Contains(err.Error(), `service "S": duplicate function`) {
		t.Fatalf("got %v, want duplicate service function error", err)
	}
}

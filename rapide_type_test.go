package gorapide

import (
	"bytes"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"testing"
)

func TestRapidePredefinedTypeSeparatesTypeEquivalenceFromValueConstraints(t *testing.T) {
	integer := mustRapidePredefinedType(t, "Integer")
	natural := mustRapidePredefinedType(t, "natural")
	positive := mustRapidePredefinedType(t, "POSITIVE")
	floatType := mustRapidePredefinedType(t, "Float")

	integerBytes := mustMarshalRapideType(t, integer)
	for name, typ := range map[string]RapideType{"Natural": natural, "Positive": positive} {
		equal, err := RapideTypesEqual(integer, typ)
		if err != nil || !equal {
			t.Fatalf("Integer and %s type equivalence = %v, %v", name, equal, err)
		}
		if got := mustMarshalRapideType(t, typ); !bytes.Equal(got, integerBytes) {
			t.Fatalf("Integer and %s descriptors differ:\n%s\n%s", name, integerBytes, got)
		}
	}

	if CanonicalValueMatchesPredefinedType(int64(-1), "Natural") {
		t.Fatal("Natural object constraint accepted -1")
	}
	if CanonicalValueMatchesPredefinedType(int64(0), "Positive") {
		t.Fatal("Positive object constraint accepted 0")
	}
	if !CanonicalValueMatchesPredefinedType(int64(1), "Positive") {
		t.Fatal("Positive object constraint rejected 1")
	}
	if subtype := mustRapideSubtype(t, integer, floatType); subtype {
		t.Fatal("Integer unexpectedly subtypes Float")
	}
	emptyInterface := EmptyRapideInterfaceType()
	if !mustRapideSubtype(t, integer, emptyInterface) {
		t.Fatal("opaque predefined interface does not subtype the empty interface")
	}
	if mustRapideSubtype(t, emptyInterface, integer) {
		t.Fatal("empty interface unexpectedly subtypes opaque Integer")
	}
	valueFunction := mustRapideFunctionType(t, nil, integer)
	voidFunction := mustRapideFunctionType(t, nil, RapideType{})
	if !mustRapideSubtype(t, valueFunction, voidFunction) {
		t.Fatal("Integer result did not subtype an omitted empty-interface result")
	}
}

func TestRapideInterfaceSubtypeUsesWidthAndObjectCovariance(t *testing.T) {
	stringType := mustRapidePredefinedType(t, "String")
	floatType := mustRapidePredefinedType(t, "Float")

	salaryInfo := mustRapideInterfaceType(t,
		ProvidedRapideMember("Salary", floatType),
	)
	employee := mustRapideInterfaceType(t,
		ProvidedRapideMember("Name", stringType),
		ProvidedRapideMember("Salary", floatType),
	)
	if !mustRapideSubtype(t, employee, salaryInfo) {
		t.Fatal("wider Employee interface does not subtype SalaryInfo")
	}
	if mustRapideSubtype(t, salaryInfo, employee) {
		t.Fatal("SalaryInfo unexpectedly subtypes wider Employee interface")
	}

	wrappedEmployee := mustRapideInterfaceType(t, ProvidedRapideMember("Value", employee))
	wrappedSalary := mustRapideInterfaceType(t, ProvidedRapideMember("Value", salaryInfo))
	if !mustRapideSubtype(t, wrappedEmployee, wrappedSalary) {
		t.Fatal("provided object constituent was not covariant")
	}
}

func TestRapideInterfaceSubtypeAppliesPrivateAndRequiresRules(t *testing.T) {
	stringType := mustRapidePredefinedType(t, "String")
	floatType := mustRapidePredefinedType(t, "Float")
	employee := mustRapideInterfaceType(t,
		ProvidedRapideMember("Name", stringType),
		ProvidedRapideMember("Salary", floatType),
	)
	salaryInfo := mustRapideInterfaceType(t, ProvidedRapideMember("Salary", floatType))

	publicSource := mustRapideInterfaceType(t, ProvidedRapideMember("Reset", stringType))
	privateTarget := mustRapideInterfaceType(t, PrivateRapideMember("reset", stringType))
	if !mustRapideSubtype(t, publicSource, privateTarget) {
		t.Fatal("provided source constituent did not cover target private constituent")
	}
	privateSource := mustRapideInterfaceType(t, PrivateRapideMember("Reset", stringType))
	publicTarget := mustRapideInterfaceType(t, ProvidedRapideMember("reset", stringType))
	if mustRapideSubtype(t, privateSource, publicTarget) {
		t.Fatal("private source constituent covered target provides constituent")
	}

	// I1 requirements are contravariant: I2 must require the same name with
	// a subtype of I1's required object type.
	liberalRequirement := mustRapideInterfaceType(t, RequiredRapideMember("Person", salaryInfo))
	narrowRequirement := mustRapideInterfaceType(t, RequiredRapideMember("person", employee))
	if !mustRapideSubtype(t, liberalRequirement, narrowRequirement) {
		t.Fatal("requires constituent did not reverse object subtype direction")
	}
	if mustRapideSubtype(t, narrowRequirement, liberalRequirement) {
		t.Fatal("requires constituent accepted the wrong variance direction")
	}
}

func TestRapideFunctionSubtypeUsesNamesDefaultsAndVariance(t *testing.T) {
	stringType := mustRapidePredefinedType(t, "String")
	floatType := mustRapidePredefinedType(t, "Float")
	integerType := mustRapidePredefinedType(t, "Integer")
	salaryInfo := mustRapideInterfaceType(t, ProvidedRapideMember("Salary", floatType))
	employee := mustRapideInterfaceType(t,
		ProvidedRapideMember("Name", stringType),
		ProvidedRapideMember("Salary", floatType),
	)

	left := mustRapideFunctionType(t,
		[]RapideFunctionParameter{RapideObjectParameter("Person", salaryInfo)}, employee)
	right := mustRapideFunctionType(t,
		[]RapideFunctionParameter{RapideObjectParameter("person", employee)}, salaryInfo)
	if !mustRapideSubtype(t, left, right) {
		t.Fatal("function object-parameter contravariance and result covariance failed")
	}
	if mustRapideSubtype(t, right, left) {
		t.Fatal("function accepted reversed parameter/result variance")
	}

	differentName := mustRapideFunctionType(t,
		[]RapideFunctionParameter{RapideObjectParameter("Employee", employee)}, salaryInfo)
	if mustRapideSubtype(t, left, differentName) {
		t.Fatal("function subtyping ignored formal parameter identifiers")
	}

	base := mustRapideFunctionType(t,
		[]RapideFunctionParameter{RapideObjectParameter("Person", salaryInfo)}, salaryInfo)
	withDefault := mustRapideFunctionType(t, []RapideFunctionParameter{
		RapideObjectParameter("person", salaryInfo),
		DefaultedRapideObjectParameter("scale", integerType),
	}, salaryInfo)
	if !mustRapideSubtype(t, withDefault, base) || !mustRapideSubtype(t, base, withDefault) {
		t.Fatal("published fewer/defaulted-extra parameter rules did not produce mutual subtyping")
	}
	equal, err := RapideTypesEqual(base, withDefault)
	if err != nil || !equal {
		t.Fatalf("function equality through mutual subtyping = %v, %v", equal, err)
	}
	if bytes.Equal(mustMarshalRapideType(t, base), mustMarshalRapideType(t, withDefault)) {
		t.Fatal("different canonical descriptors unexpectedly collapsed into one artifact")
	}

	withRequiredExtra := mustRapideFunctionType(t, []RapideFunctionParameter{
		RapideObjectParameter("person", salaryInfo),
		RapideObjectParameter("scale", integerType),
	}, salaryInfo)
	if mustRapideSubtype(t, withRequiredExtra, base) {
		t.Fatal("source function's nondefaulted extra parameter was accepted")
	}
	// Type LRM 5.3 compares through the shorter parameter list; when F1 is
	// shorter, no default condition is imposed on F2's remaining parameters.
	if !mustRapideSubtype(t, base, withRequiredExtra) {
		t.Fatal("shorter source function did not follow the published asymmetric rule")
	}
}

func TestRapideFunctionTypeParametersUsePublishedBoundContravariance(t *testing.T) {
	stringType := mustRapidePredefinedType(t, "String")
	floatType := mustRapidePredefinedType(t, "Float")
	salaryInfo := mustRapideInterfaceType(t, ProvidedRapideMember("Salary", floatType))
	employee := mustRapideInterfaceType(t,
		ProvidedRapideMember("Name", stringType),
		ProvidedRapideMember("Salary", floatType),
	)
	empty := RapideType{}

	unbounded := mustRapideFunctionType(t,
		[]RapideFunctionParameter{RapideTypeParameter("T")}, empty)
	salaryBound := mustRapideFunctionType(t,
		[]RapideFunctionParameter{BoundedRapideTypeParameter("t", salaryInfo)}, empty)
	employeeBound := mustRapideFunctionType(t,
		[]RapideFunctionParameter{BoundedRapideTypeParameter("T", employee)}, empty)

	if !mustRapideSubtype(t, unbounded, salaryBound) || !mustRapideSubtype(t, unbounded, employeeBound) {
		t.Fatal("unbounded formal type parameter did not accept bounded target domains")
	}
	if mustRapideSubtype(t, salaryBound, unbounded) {
		t.Fatal("bounded formal type parameter incorrectly covered an unbounded target domain")
	}
	if !mustRapideSubtype(t, salaryBound, employeeBound) {
		t.Fatal("T2 <: T1 did not establish formal type-parameter contravariance")
	}
	if mustRapideSubtype(t, employeeBound, salaryBound) {
		t.Fatal("formal type-parameter bound variance was reversed")
	}

	objectParameter := mustRapideFunctionType(t,
		[]RapideFunctionParameter{RapideObjectParameter("T", employee)}, empty)
	if mustRapideSubtype(t, employeeBound, objectParameter) || mustRapideSubtype(t, objectParameter, employeeBound) {
		t.Fatal("formal type and object parameter kinds were conflated")
	}
	differentName := mustRapideFunctionType(t,
		[]RapideFunctionParameter{BoundedRapideTypeParameter("Element", employee)}, empty)
	if mustRapideSubtype(t, employeeBound, differentName) {
		t.Fatal("formal type-parameter identifiers were ignored")
	}
	extraTypeParameter := mustRapideFunctionType(t, []RapideFunctionParameter{
		RapideTypeParameter("T"), RapideTypeParameter("U"),
	}, empty)
	if mustRapideSubtype(t, extraTypeParameter, unbounded) {
		t.Fatal("extra nondefaultable formal type parameter was accepted")
	}
}

func TestRapideFunctionTypeParametersRoundTripCanonically(t *testing.T) {
	stringType := mustRapidePredefinedType(t, "String")
	functionType := mustRapideFunctionType(t, []RapideFunctionParameter{
		RapideTypeParameter("Element"),
		BoundedRapideTypeParameter("Record", mustRapideInterfaceType(t, ProvidedRapideMember("Name", stringType))),
		RapideObjectParameter("Fallback", stringType),
	}, RapideType{})
	encoded := mustMarshalRapideType(t, functionType)
	want := `{"format":"gorapide.rapide-type.v2","type":{"kind":"function","parameters":[` +
		`{"name":"element","kind":"type"},` +
		`{"name":"record","kind":"type","type":{"kind":"interface","members":[{"region":"provides","name":"name","type":{"kind":"predefined","name":"String"}}]}},` +
		`{"name":"fallback","type":{"kind":"predefined","name":"String"}}],"result":{"kind":"interface"}}}`
	if string(encoded) != want {
		t.Fatalf("formal type-parameter bytes:\nwant %s\n got %s", want, encoded)
	}
	parsed, err := ParseRapideType(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if got := mustMarshalRapideType(t, parsed); !bytes.Equal(got, encoded) {
		t.Fatalf("formal type-parameter round trip changed bytes:\n%s\n%s", encoded, got)
	}
}

func TestRapideInterfaceOverloadMayCoverSeveralTargetOverloads(t *testing.T) {
	stringType := mustRapidePredefinedType(t, "String")
	floatType := mustRapidePredefinedType(t, "Float")
	displayable := mustRapideInterfaceType(t)
	textType := mustRapideInterfaceType(t, ProvidedRapideMember("Text", stringType))
	figureType := mustRapideInterfaceType(t, ProvidedRapideMember("Figure", floatType))

	generalDisplay := mustRapideFunctionType(t,
		[]RapideFunctionParameter{RapideObjectParameter("D", displayable)}, RapideType{})
	textDisplay := mustRapideFunctionType(t,
		[]RapideFunctionParameter{RapideObjectParameter("d", textType)}, RapideType{})
	figureDisplay := mustRapideFunctionType(t,
		[]RapideFunctionParameter{RapideObjectParameter("D", figureType)}, RapideType{})

	source := mustRapideInterfaceType(t, ProvidedRapideMember("Display", generalDisplay))
	target := mustRapideInterfaceType(t,
		ProvidedRapideMember("display", textDisplay),
		ProvidedRapideMember("DISPLAY", figureDisplay),
	)
	if !mustRapideSubtype(t, source, target) {
		t.Fatal("one general source overload did not cover both target overloads")
	}
	if mustRapideSubtype(t, target, source) {
		t.Fatal("narrow target overloads unexpectedly covered the general source overload")
	}
}

func TestRapideIteratorTypeIsCovariantThroughStructuralRules(t *testing.T) {
	stringType := mustRapidePredefinedType(t, "String")
	floatType := mustRapidePredefinedType(t, "Float")
	salaryInfo := mustRapideInterfaceType(t, ProvidedRapideMember("Salary", floatType))
	employee := mustRapideInterfaceType(t,
		ProvidedRapideMember("Name", stringType),
		ProvidedRapideMember("Salary", floatType),
	)
	employeeIterator, err := RapideIteratorType(employee)
	if err != nil {
		t.Fatal(err)
	}
	salaryIterator, err := RapideIteratorType(salaryInfo)
	if err != nil {
		t.Fatal(err)
	}
	if !mustRapideSubtype(t, employeeIterator, salaryIterator) {
		t.Fatal("Iterator(Employee) does not subtype Iterator(SalaryInfo)")
	}
	if mustRapideSubtype(t, salaryIterator, employeeIterator) {
		t.Fatal("Iterator covariance ran in the wrong direction")
	}
}

func TestRapideModuleGeneratorConstituentUsesFunctionConformanceAndCanonicalKind(t *testing.T) {
	stringType := mustRapidePredefinedType(t, "String")
	floatType := mustRapidePredefinedType(t, "Float")
	salaryInfo := mustRapideInterfaceType(t, ProvidedRapideMember("Salary", floatType))
	employee := mustRapideInterfaceType(t,
		ProvidedRapideMember("Name", stringType),
		ProvidedRapideMember("Salary", floatType),
	)
	employeeSignature := mustRapideFunctionType(t, nil, employee)
	salarySignature := mustRapideFunctionType(t, nil, salaryInfo)

	source := mustRapideInterfaceType(t,
		ProvidedRapideModuleGenerator("Build", employeeSignature),
	)
	target := mustRapideInterfaceType(t,
		ProvidedRapideModuleGenerator("build", salarySignature),
	)
	if !mustRapideSubtype(t, source, target) {
		t.Fatal("module-generator result did not use function covariance")
	}
	if mustRapideSubtype(t, target, source) {
		t.Fatal("module-generator result covariance ran in the wrong direction")
	}
	generalSignature := mustRapideFunctionType(t,
		[]RapideFunctionParameter{RapideObjectParameter("Initial", salaryInfo)}, employee)
	narrowSignature := mustRapideFunctionType(t,
		[]RapideFunctionParameter{RapideObjectParameter("initial", employee)}, salaryInfo)
	generalGenerator := mustRapideInterfaceType(t,
		ProvidedRapideModuleGenerator("Configure", generalSignature),
	)
	narrowGenerator := mustRapideInterfaceType(t,
		ProvidedRapideModuleGenerator("configure", narrowSignature),
	)
	if !mustRapideSubtype(t, generalGenerator, narrowGenerator) {
		t.Fatal("module-generator object parameter did not use function contravariance")
	}
	if mustRapideSubtype(t, narrowGenerator, generalGenerator) {
		t.Fatal("module-generator object-parameter contravariance ran in the wrong direction")
	}
	unboundedTypeSignature := mustRapideFunctionType(t,
		[]RapideFunctionParameter{RapideTypeParameter("Item")}, employee)
	boundedTypeSignature := mustRapideFunctionType(t,
		[]RapideFunctionParameter{BoundedRapideTypeParameter("item", employee)}, salaryInfo)
	unboundedTypeGenerator := mustRapideInterfaceType(t,
		ProvidedRapideModuleGenerator("Generic", unboundedTypeSignature),
	)
	boundedTypeGenerator := mustRapideInterfaceType(t,
		ProvidedRapideModuleGenerator("generic", boundedTypeSignature),
	)
	if !mustRapideSubtype(t, unboundedTypeGenerator, boundedTypeGenerator) {
		t.Fatal("module-generator type-parameter bound did not use function contravariance")
	}
	if mustRapideSubtype(t, boundedTypeGenerator, unboundedTypeGenerator) {
		t.Fatal("module-generator type-parameter bound contravariance ran in the wrong direction")
	}

	ordinaryFunctionObject := mustRapideInterfaceType(t,
		ProvidedRapideMember("Build", employeeSignature),
	)
	if mustRapideSubtype(t, source, ordinaryFunctionObject) ||
		mustRapideSubtype(t, ordinaryFunctionObject, source) {
		t.Fatal("module generator was conflated with an ordinary function-valued object")
	}

	encoded := mustMarshalRapideType(t, source)
	if !strings.Contains(string(encoded), `"kind":"module_generator"`) {
		t.Fatalf("canonical descriptor lost module-generator constituent kind: %s", encoded)
	}
	parsed, err := ParseRapideType(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if got := mustMarshalRapideType(t, parsed); !bytes.Equal(got, encoded) {
		t.Fatalf("module-generator round trip changed bytes:\n%s\n%s", encoded, got)
	}
}

func TestRapideIterableTypeIsCovariantAndDistinctFromFunctionObject(t *testing.T) {
	stringType := mustRapidePredefinedType(t, "String")
	floatType := mustRapidePredefinedType(t, "Float")
	salaryInfo := mustRapideInterfaceType(t, ProvidedRapideMember("Salary", floatType))
	employee := mustRapideInterfaceType(t,
		ProvidedRapideMember("Name", stringType),
		ProvidedRapideMember("Salary", floatType),
	)
	employeeIterable, err := RapideIterableType(employee)
	if err != nil {
		t.Fatal(err)
	}
	salaryIterable, err := RapideIterableType(salaryInfo)
	if err != nil {
		t.Fatal(err)
	}
	if !mustRapideSubtype(t, employeeIterable, salaryIterable) {
		t.Fatal("Iterable(Employee) does not subtype Iterable(SalaryInfo)")
	}
	if mustRapideSubtype(t, salaryIterable, employeeIterable) {
		t.Fatal("Iterable covariance ran in the wrong direction")
	}

	iterator, err := RapideIteratorType(employee)
	if err != nil {
		t.Fatal(err)
	}
	signature := mustRapideFunctionType(t, nil, iterator)
	ordinaryFunctionObject := mustRapideInterfaceType(t,
		ProvidedRapideMember("Iterator", signature),
	)
	if mustRapideSubtype(t, employeeIterable, ordinaryFunctionObject) ||
		mustRapideSubtype(t, ordinaryFunctionObject, employeeIterable) {
		t.Fatal("Iterable's module generator was conflated with a function-valued object")
	}

	encoded := mustMarshalRapideType(t, employeeIterable)
	previous := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(previous)
	for _, processors := range []int{1, 4} {
		runtime.GOMAXPROCS(processors)
		rebuilt, err := RapideIterableType(employee)
		if err != nil {
			t.Fatal(err)
		}
		if got := mustMarshalRapideType(t, rebuilt); !bytes.Equal(got, encoded) {
			t.Fatalf("GOMAXPROCS=%d changed Iterable(T) bytes:\n%s\n%s", processors, encoded, got)
		}
	}
}

func TestRapideServiceInterfaceTypeQualifiesAndFlattensConstituents(t *testing.T) {
	stringType := mustRapidePredefinedType(t, "String")
	integerType := mustRapidePredefinedType(t, "Integer")
	eventType := mustRapideEventType(t, RapideEventParam("Value", integerType))
	functionType := mustRapideFunctionType(t, nil, stringType)
	worker := mustRapideInterfaceType(t, OutputRapideAction("Done", eventType))
	generatorSignature := mustRapideFunctionType(t, nil, worker)
	serviceTarget := mustRapideInterfaceType(t,
		ProvidedRapideMember("Name", stringType),
		RequiredRapideMember("Lookup", functionType),
		InputRapideAction("Request", eventType),
		OutputRapideAction("Reply", eventType),
		ProvidedRapideModuleGenerator("Worker", generatorSignature),
	)
	expanded, err := RapideServiceInterfaceType("API", serviceTarget)
	if err != nil {
		t.Fatal(err)
	}
	want := mustRapideInterfaceType(t,
		ProvidedRapideMember("api.name", stringType),
		RequiredRapideMember("api.lookup", functionType),
		InputRapideAction("api.request", eventType),
		OutputRapideAction("api.reply", eventType),
		ProvidedRapideModuleGenerator("api.worker", generatorSignature),
	)
	gotBytes := mustMarshalRapideType(t, expanded)
	wantBytes := mustMarshalRapideType(t, want)
	if !bytes.Equal(gotBytes, wantBytes) {
		t.Fatalf("service rewrite:\nwant %s\n got %s", wantBytes, gotBytes)
	}
	nested, err := RapideServiceInterfaceType("Outer", expanded)
	if err != nil {
		t.Fatal(err)
	}
	nestedWant := mustRapideInterfaceType(t,
		ProvidedRapideMember("outer.api.name", stringType),
		RequiredRapideMember("outer.api.lookup", functionType),
		InputRapideAction("outer.api.request", eventType),
		OutputRapideAction("outer.api.reply", eventType),
		ProvidedRapideModuleGenerator("outer.api.worker", generatorSignature),
	)
	if got, want := mustMarshalRapideType(t, nested), mustMarshalRapideType(t, nestedWant); !bytes.Equal(got, want) {
		t.Fatalf("nested service rewrite:\nwant %s\n got %s", want, got)
	}
}

func TestRapideDualServiceInterfaceTypeReversesRegionsAndDirections(t *testing.T) {
	stringType := mustRapidePredefinedType(t, "String")
	eventType := mustRapideEventType(t)
	worker := mustRapideInterfaceType(t, OutputRapideAction("Done", eventType))
	generatorSignature := mustRapideFunctionType(t, nil, worker)
	target := mustRapideInterfaceType(t,
		ProvidedRapideMember("Source", stringType),
		RequiredRapideMember("Sink", stringType),
		InputRapideAction("Take", eventType),
		OutputRapideAction("Give", eventType),
		ProvidedRapideModuleGenerator("Create", generatorSignature),
		RequiredRapideModuleGenerator("Obtain", generatorSignature),
	)
	dual, err := RapideDualServiceInterfaceType("Socket", target)
	if err != nil {
		t.Fatal(err)
	}
	want := mustRapideInterfaceType(t,
		RequiredRapideMember("socket.source", stringType),
		ProvidedRapideMember("socket.sink", stringType),
		OutputRapideAction("socket.take", eventType),
		InputRapideAction("socket.give", eventType),
		RequiredRapideModuleGenerator("socket.create", generatorSignature),
		ProvidedRapideModuleGenerator("socket.obtain", generatorSignature),
	)
	if gotBytes, wantBytes := mustMarshalRapideType(t, dual), mustMarshalRapideType(t, want); !bytes.Equal(gotBytes, wantBytes) {
		t.Fatalf("dual service rewrite:\nwant %s\n got %s", wantBytes, gotBytes)
	}

	inner, err := RapideDualServiceInterfaceType("Inner", target)
	if err != nil {
		t.Fatal(err)
	}
	double, err := RapideDualServiceInterfaceType("Outer", inner)
	if err != nil {
		t.Fatal(err)
	}
	doubleWant := mustRapideInterfaceType(t,
		ProvidedRapideMember("outer.inner.source", stringType),
		RequiredRapideMember("outer.inner.sink", stringType),
		InputRapideAction("outer.inner.take", eventType),
		OutputRapideAction("outer.inner.give", eventType),
		ProvidedRapideModuleGenerator("outer.inner.create", generatorSignature),
		RequiredRapideModuleGenerator("outer.inner.obtain", generatorSignature),
	)
	if gotBytes, wantBytes := mustMarshalRapideType(t, double), mustMarshalRapideType(t, doubleWant); !bytes.Equal(gotBytes, wantBytes) {
		t.Fatalf("double-dual service rewrite:\nwant %s\n got %s", wantBytes, gotBytes)
	}
}

func TestRapideIntegerServiceSetExpandsFiniteIndexedQualifiedNames(t *testing.T) {
	integerType := mustRapidePredefinedType(t, "Integer")
	eventType := mustRapideEventType(t)
	target := mustRapideInterfaceType(t,
		ProvidedRapideMember("Source", integerType),
		RequiredRapideMember("Sink", integerType),
		InputRapideAction("Take", eventType),
		OutputRapideAction("Give", eventType),
	)
	set, err := RapideIntegerServiceSetInterfaceType("Port", -1, 1, target)
	if err != nil {
		t.Fatal(err)
	}
	wantMembers := make([]RapideInterfaceMember, 0, 12)
	for _, index := range []int{-1, 0, 1} {
		prefix := fmt.Sprintf("port(%d).", index)
		wantMembers = append(wantMembers,
			ProvidedRapideMember(prefix+"source", integerType),
			RequiredRapideMember(prefix+"sink", integerType),
			InputRapideAction(prefix+"take", eventType),
			OutputRapideAction(prefix+"give", eventType),
		)
	}
	want := mustRapideInterfaceType(t, wantMembers...)
	if gotBytes, wantBytes := mustMarshalRapideType(t, set), mustMarshalRapideType(t, want); !bytes.Equal(gotBytes, wantBytes) {
		t.Fatalf("Integer service-set rewrite:\nwant %s\n got %s", wantBytes, gotBytes)
	}
	encoded := mustMarshalRapideType(t, set)
	if _, err := ParseRapideType(encoded); err != nil {
		t.Fatalf("indexed qualified canonical type did not round trip: %v", err)
	}

	dual, err := RapideDualIntegerServiceSetInterfaceType("Socket", 2, 2, target)
	if err != nil {
		t.Fatal(err)
	}
	dualWant := mustRapideInterfaceType(t,
		RequiredRapideMember("socket(2).source", integerType),
		ProvidedRapideMember("socket(2).sink", integerType),
		OutputRapideAction("socket(2).take", eventType),
		InputRapideAction("socket(2).give", eventType),
	)
	if gotBytes, wantBytes := mustMarshalRapideType(t, dual), mustMarshalRapideType(t, dualWant); !bytes.Equal(gotBytes, wantBytes) {
		t.Fatalf("dual Integer service-set rewrite:\nwant %s\n got %s", wantBytes, gotBytes)
	}

	nested, err := RapideServiceInterfaceType("Outer", set)
	if err != nil {
		t.Fatal(err)
	}
	nestedMembers := make([]RapideInterfaceMember, 0, len(wantMembers))
	for _, index := range []int{-1, 0, 1} {
		prefix := fmt.Sprintf("outer.port(%d).", index)
		nestedMembers = append(nestedMembers,
			ProvidedRapideMember(prefix+"source", integerType),
			RequiredRapideMember(prefix+"sink", integerType),
			InputRapideAction(prefix+"take", eventType),
			OutputRapideAction(prefix+"give", eventType),
		)
	}
	if gotBytes, wantBytes := mustMarshalRapideType(t, nested), mustMarshalRapideType(t, mustRapideInterfaceType(t, nestedMembers...)); !bytes.Equal(gotBytes, wantBytes) {
		t.Fatalf("nested Integer service-set rewrite:\nwant %s\n got %s", wantBytes, gotBytes)
	}

	empty, err := RapideIntegerServiceSetInterfaceType("None", 1, 0, target)
	if err != nil {
		t.Fatal(err)
	}
	if gotBytes, wantBytes := mustMarshalRapideType(t, empty), mustMarshalRapideType(t, EmptyRapideInterfaceType()); !bytes.Equal(gotBytes, wantBytes) {
		t.Fatalf("descending service set is not empty:\nwant %s\n got %s", wantBytes, gotBytes)
	}
	if _, err := RapideIntegerServiceSetInterfaceType("None", 1, 0, integerType); !errors.Is(err, ErrInvalidRapideType) || !strings.Contains(err.Error(), "type is not an interface") {
		t.Fatalf("descending service-set invalid target error=%v", err)
	}
	if _, err := RapideIntegerServiceSetInterfaceType("TooMany", 0, MaxRapideIntegerServiceSetCardinality, target); !errors.Is(err, ErrInvalidRapideType) || !strings.Contains(err.Error(), "cardinality exceeds deterministic limit 256") {
		t.Fatalf("oversized service-set error=%v", err)
	}
	const minInt64 = int64(-1 << 63)
	const maxInt64 = int64(1<<63 - 1)
	for _, bounds := range [][2]int64{{minInt64, minInt64 + 255}, {maxInt64 - 255, maxInt64}} {
		if _, err := RapideIntegerServiceSetInterfaceType("Edge", bounds[0], bounds[1], target); err != nil {
			t.Fatalf("edge Integer service-set range %d..%d: %v", bounds[0], bounds[1], err)
		}
	}
	for _, name := range []string{"port(01).take", "port(+1).take", "port(item).take", "port(1)"} {
		if _, err := NewRapideInterfaceType(InputRapideAction(name, eventType)); !errors.Is(err, ErrInvalidRapideType) {
			t.Fatalf("noncanonical indexed constituent %q error=%v", name, err)
		}
	}
}

func TestRapideServiceInterfaceTypeRejectsForbiddenOrUnrepresentableTargets(t *testing.T) {
	stringType := mustRapidePredefinedType(t, "String")
	if _, err := NewRapideInterfaceType(UnboundedProvidedRapideTypeName("api.Item")); !errors.Is(err, ErrInvalidRapideType) || !strings.Contains(err.Error(), "outside the current ASCII lexical profile") {
		t.Fatalf("qualified type-name constituent error=%v", err)
	}
	tests := []struct {
		name string
		typ  RapideType
		want string
	}{
		{name: "non-interface", typ: stringType, want: "type is not an interface"},
		{name: "private", typ: mustRapideInterfaceType(t, PrivateRapideMember("Secret", stringType)), want: "contains private object"},
		{name: "type name", typ: mustRapideInterfaceType(t, UnboundedProvidedRapideTypeName("Item")), want: "contains forbidden provides type name"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := RapideServiceInterfaceType("API", test.typ)
			if !errors.Is(err, ErrInvalidRapideType) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v, want ErrInvalidRapideType containing %q", err, test.want)
			}
		})
	}
}

func TestRapideServicePreservesRecursiveMemberTypes(t *testing.T) {
	recursive, err := NewSelfRecursiveRapideInterfaceType(func(self RapideType) (RapideType, error) {
		return NewRapideInterfaceType(
			ProvidedRapideMember("Next", self),
			OutputRapideAction("Done", mustRapideEventType(t)),
		)
	})
	if err != nil {
		t.Fatal(err)
	}
	expanded, err := RapideServiceInterfaceType("API", recursive)
	if err != nil {
		t.Fatal(err)
	}
	want := mustRapideInterfaceType(t,
		ProvidedRapideMember("api.next", recursive),
		OutputRapideAction("api.done", mustRapideEventType(t)),
	)
	if gotBytes, wantBytes := mustMarshalRapideType(t, expanded), mustMarshalRapideType(t, want); !bytes.Equal(gotBytes, wantBytes) {
		t.Fatalf("recursive service member type:\nwant %s\n got %s", wantBytes, gotBytes)
	}
	if _, err := ParseRapideType(mustMarshalRapideType(t, expanded)); err != nil {
		t.Fatalf("recursive service type did not round trip: %v", err)
	}
}

func TestRapideDiscreteTypeIsInvariantThroughPublishedFunctions(t *testing.T) {
	stringType := mustRapidePredefinedType(t, "String")
	floatType := mustRapidePredefinedType(t, "Float")
	salaryInfo := mustRapideInterfaceType(t, ProvidedRapideMember("Salary", floatType))
	employee := mustRapideInterfaceType(t,
		ProvidedRapideMember("Name", stringType),
		ProvidedRapideMember("Salary", floatType),
	)
	employeeDiscrete, err := RapideDiscreteType(employee)
	if err != nil {
		t.Fatal(err)
	}
	salaryDiscrete, err := RapideDiscreteType(salaryInfo)
	if err != nil {
		t.Fatal(err)
	}
	if mustRapideSubtype(t, employeeDiscrete, salaryDiscrete) ||
		mustRapideSubtype(t, salaryDiscrete, employeeDiscrete) {
		t.Fatal("Discrete(T) did not preserve the published invariant subtype rule")
	}
	rebuilt, err := RapideDiscreteType(employee)
	if err != nil {
		t.Fatal(err)
	}
	if !mustRapideSubtype(t, employeeDiscrete, rebuilt) ||
		!mustRapideSubtype(t, rebuilt, employeeDiscrete) {
		t.Fatal("equal Discrete(T) descriptors were not mutually subtyping")
	}
	encoded := mustMarshalRapideType(t, employeeDiscrete)
	if !strings.Contains(string(encoded), `"name":"\u003c"`) ||
		!strings.Contains(string(encoded), `"name":"="`) ||
		!strings.Contains(string(encoded), `"name":"succ"`) {
		t.Fatalf("Discrete(T) canonical descriptor omitted a published operation: %s", encoded)
	}
}

func TestRapideReferenceTypeIsInvariantRecursiveAndCanonical(t *testing.T) {
	stringType := mustRapidePredefinedType(t, "String")
	floatType := mustRapidePredefinedType(t, "Float")
	salaryInfo := mustRapideInterfaceType(t, ProvidedRapideMember("Salary", floatType))
	employee := mustRapideInterfaceType(t,
		ProvidedRapideMember("Name", stringType),
		ProvidedRapideMember("Salary", floatType),
	)
	employeeRef, err := RapideReferenceType(employee)
	if err != nil {
		t.Fatal(err)
	}
	salaryRef, err := RapideReferenceType(salaryInfo)
	if err != nil {
		t.Fatal(err)
	}
	if mustRapideSubtype(t, employeeRef, salaryRef) || mustRapideSubtype(t, salaryRef, employeeRef) {
		t.Fatal("Ref(T) did not preserve the published invariant subtype rule")
	}
	encoded := mustMarshalRapideType(t, employeeRef)
	if !strings.Contains(string(encoded), `"name":"$"`) ||
		!strings.Contains(string(encoded), `"name":":="`) ||
		!strings.Contains(string(encoded), `"name":"is_nil"`) ||
		!strings.Contains(string(encoded), `"kind":"recursive_interface"`) ||
		!strings.Contains(string(encoded), `"kind":"recursive_reference"`) {
		t.Fatalf("Ref(T) canonical descriptor omitted a published operation or recursion: %s", encoded)
	}
	parsed, err := ParseRapideType(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if got := mustMarshalRapideType(t, parsed); !bytes.Equal(got, encoded) {
		t.Fatalf("Ref(T) canonical round trip changed bytes:\n%s\n%s", encoded, got)
	}
	equal, err := RapideTypesEqual(employeeRef, parsed)
	if err != nil || !equal {
		t.Fatalf("parsed Ref(T) equality = %v, %v", equal, err)
	}
	previous := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(previous)
	for _, processors := range []int{1, 4} {
		runtime.GOMAXPROCS(processors)
		rebuilt, err := RapideReferenceType(employee)
		if err != nil {
			t.Fatal(err)
		}
		if got := mustMarshalRapideType(t, rebuilt); !bytes.Equal(got, encoded) {
			t.Fatalf("GOMAXPROCS=%d changed Ref(T) bytes:\n%s\n%s", processors, encoded, got)
		}
	}
}

func TestRapideSymbolicFunctionNamesDoNotBroadenOtherNameClasses(t *testing.T) {
	integer := mustRapidePredefinedType(t, "Integer")
	function, err := NewRapideFunctionType(nil, integer)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewRapideInterfaceType(ProvidedRapideMember("$", function)); err != nil {
		t.Fatalf("published symbolic function object name was rejected: %v", err)
	}
	if _, err := NewRapideInterfaceType(ProvidedRapideMember("=>", function)); !errors.Is(err, ErrInvalidRapideType) {
		t.Fatalf("non-function connector designator error = %v", err)
	}
	if _, err := NewRapideInterfaceType(UnboundedProvidedRapideTypeName("=")); !errors.Is(err, ErrInvalidRapideType) {
		t.Fatalf("symbolic type-name constituent error = %v", err)
	}
}

func TestApplyRapideTypeConstructorErasesApplicationSpellingStructurally(t *testing.T) {
	integer := mustRapidePredefinedType(t, "Integer")
	directIterator, err := RapideIteratorType(integer)
	if err != nil {
		t.Fatal(err)
	}
	appliedIterator, err := ApplyRapideTypeConstructor("iTeRaToR", integer)
	if err != nil {
		t.Fatal(err)
	}
	directBytes := mustMarshalRapideType(t, directIterator)
	if appliedBytes := mustMarshalRapideType(t, appliedIterator); !bytes.Equal(appliedBytes, directBytes) {
		t.Fatalf("Iterator application retained nominal spelling:\n%s\n%s", directBytes, appliedBytes)
	}

	nested, err := ApplyRapideTypeConstructor("Ref", appliedIterator)
	if err != nil {
		t.Fatal(err)
	}
	directNested, err := RapideReferenceType(directIterator)
	if err != nil {
		t.Fatal(err)
	}
	nestedBytes := mustMarshalRapideType(t, nested)
	if directNestedBytes := mustMarshalRapideType(t, directNested); !bytes.Equal(nestedBytes, directNestedBytes) {
		t.Fatalf("nested constructor application changed structural denotation:\n%s\n%s", nestedBytes, directNestedBytes)
	}

	previous := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(previous)
	for _, processors := range []int{1, 4} {
		runtime.GOMAXPROCS(processors)
		rebuilt, err := ApplyRapideTypeConstructor("REF", appliedIterator)
		if err != nil {
			t.Fatal(err)
		}
		if got := mustMarshalRapideType(t, rebuilt); !bytes.Equal(got, nestedBytes) {
			t.Fatalf("GOMAXPROCS=%d changed constructor application bytes:\n%s\n%s", processors, nestedBytes, got)
		}
	}

	directIterable, err := RapideIterableType(integer)
	if err != nil {
		t.Fatal(err)
	}
	appliedIterable, err := ApplyRapideTypeConstructor("ItErAbLe", integer)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := mustMarshalRapideType(t, appliedIterable), mustMarshalRapideType(t, directIterable); !bytes.Equal(got, want) {
		t.Fatalf("Iterable application retained nominal spelling:\n%s\n%s", want, got)
	}
}

func TestApplyRapideTypeConstructorRejectsObsoleteOrMalformedApplications(t *testing.T) {
	integer := mustRapidePredefinedType(t, "Integer")
	tests := []struct {
		name      string
		arguments []RapideType
		want      string
	}{
		{name: "Iterator", want: "has 0 arguments, want 1"},
		{name: "Ref", arguments: []RapideType{{}}, want: "has an invalid argument"},
		{name: "Range", arguments: []RapideType{integer}, want: "withheld because the published draft denotation is obsolete"},
		{name: "Collection", arguments: []RapideType{integer}, want: "unknown closed predefined type constructor"},
	}
	for _, test := range tests {
		t.Run(test.name+test.want, func(t *testing.T) {
			_, err := ApplyRapideTypeConstructor(test.name, test.arguments...)
			if err == nil || !errors.Is(err, ErrInvalidRapideType) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v, want ErrInvalidRapideType containing %q", err, test.want)
			}
		})
	}
}

func TestRapideEventTypeUsesParameterRecordWidthAndCovariance(t *testing.T) {
	integerType := mustRapidePredefinedType(t, "Integer")
	stringType := mustRapidePredefinedType(t, "String")
	floatType := mustRapidePredefinedType(t, "Float")
	salaryInfo := mustRapideInterfaceType(t, ProvidedRapideMember("Salary", floatType))
	employee := mustRapideInterfaceType(t,
		ProvidedRapideMember("Name", stringType),
		ProvidedRapideMember("Salary", floatType),
	)

	wide := mustRapideEventType(t,
		RapideEventParam("ID", integerType),
		RapideEventParam("Person", employee),
	)
	narrow := mustRapideEventType(t, RapideEventParam("person", salaryInfo))
	if !mustRapideSubtype(t, wide, narrow) {
		t.Fatal("event parameter-record width/covariance failed")
	}
	if mustRapideSubtype(t, narrow, wide) {
		t.Fatal("narrow event unexpectedly subtypes wider parameter record")
	}

	reordered := mustRapideEventType(t,
		RapideEventParam("PERSON", employee),
		RapideEventParam("id", integerType),
	)
	equal, err := RapideTypesEqual(wide, reordered)
	if err != nil || !equal {
		t.Fatalf("reordered event type equality = %v, %v", equal, err)
	}
	if !bytes.Equal(mustMarshalRapideType(t, wide), mustMarshalRapideType(t, reordered)) {
		t.Fatal("event parameter order/case changed canonical descriptor")
	}

	parameterRecord := mustRapideInterfaceType(t,
		ProvidedRapideMember("person", salaryInfo),
	)
	if !mustRapideSubtype(t, wide, parameterRecord) {
		t.Fatal("event type does not subtype its parameter record")
	}
	if mustRapideSubtype(t, parameterRecord, wide) {
		t.Fatal("parameter record unexpectedly subtypes the event type with common operations")
	}
	if !mustRapideSubtype(t, wide, EmptyRapideInterfaceType()) {
		t.Fatal("event interface does not subtype the empty interface")
	}
}

func TestRapideActionConstituentRequiresNameModeAndEventSubtype(t *testing.T) {
	integerType := mustRapidePredefinedType(t, "Integer")
	stringType := mustRapidePredefinedType(t, "String")
	wideEvent := mustRapideEventType(t,
		RapideEventParam("id", integerType),
		RapideEventParam("message", stringType),
	)
	idEvent := mustRapideEventType(t, RapideEventParam("ID", integerType))
	messageEvent := mustRapideEventType(t, RapideEventParam("Message", stringType))

	source := mustRapideInterfaceType(t, OutputRapideAction("Notify", wideEvent))
	target := mustRapideInterfaceType(t, OutputRapideAction("notify", idEvent))
	if !mustRapideSubtype(t, source, target) {
		t.Fatal("same-name/mode action did not use covariant event subtyping")
	}
	if mustRapideSubtype(t, target, source) {
		t.Fatal("narrow event action unexpectedly subtypes wider action event")
	}
	wrongMode := mustRapideInterfaceType(t, InputRapideAction("Notify", idEvent))
	if mustRapideSubtype(t, source, wrongMode) {
		t.Fatal("out action satisfied an in action")
	}
	wrongName := mustRapideInterfaceType(t, OutputRapideAction("Report", idEvent))
	if mustRapideSubtype(t, source, wrongName) {
		t.Fatal("action subtyping ignored the action identifier")
	}

	// One wider event overload may independently cover several target
	// overloads; candidate matching is existential, not consumptive.
	overloadedTarget := mustRapideInterfaceType(t,
		OutputRapideAction("Notify", idEvent),
		OutputRapideAction("Notify", messageEvent),
	)
	if !mustRapideSubtype(t, source, overloadedTarget) {
		t.Fatal("one general action overload did not cover two target overloads")
	}
}

func TestRapideTypeNameConstituentNarrowsDenotationSpecifications(t *testing.T) {
	stringType := mustRapidePredefinedType(t, "String")
	floatType := mustRapidePredefinedType(t, "Float")
	salaryInfo := mustRapideInterfaceType(t, ProvidedRapideMember("Salary", floatType))
	employee := mustRapideInterfaceType(t,
		ProvidedRapideMember("Name", stringType),
		ProvidedRapideMember("Salary", floatType),
	)

	anyEmployee := mustRapideInterfaceType(t, UnboundedProvidedRapideTypeName("Employee"))
	boundedSalary := mustRapideInterfaceType(t, BoundedProvidedRapideTypeName("Employee", salaryInfo))
	boundedEmployee := mustRapideInterfaceType(t, BoundedProvidedRapideTypeName("Employee", employee))
	exactEmployee := mustRapideInterfaceType(t, ExactProvidedRapideTypeName("Employee", employee))
	exactSalary := mustRapideInterfaceType(t, ExactProvidedRapideTypeName("Employee", salaryInfo))

	for name, source := range map[string]RapideType{
		"unbounded": anyEmployee, "bounded": boundedSalary, "narrower bound": boundedEmployee,
		"exact": exactEmployee,
	} {
		if !mustRapideSubtype(t, source, anyEmployee) {
			t.Fatalf("%s source did not satisfy an unconstrained type-name specification", name)
		}
	}
	if mustRapideSubtype(t, anyEmployee, boundedSalary) {
		t.Fatal("unconstrained source type name satisfied a bounded target")
	}
	if !mustRapideSubtype(t, boundedEmployee, boundedSalary) {
		t.Fatal("narrower source bound did not subtype wider target bound")
	}
	if mustRapideSubtype(t, boundedSalary, boundedEmployee) {
		t.Fatal("wider source bound subtyped narrower target bound")
	}
	if !mustRapideSubtype(t, exactEmployee, boundedSalary) {
		t.Fatal("exact source denotation satisfying a target bound was rejected")
	}
	if mustRapideSubtype(t, boundedEmployee, exactEmployee) {
		t.Fatal("bounded source specification satisfied an exact target")
	}
	if mustRapideSubtype(t, exactEmployee, exactSalary) {
		t.Fatal("one-way subtype denotations were treated as the same exact type")
	}
	if !mustRapideSubtype(t, exactEmployee, exactEmployee) {
		t.Fatal("the same exact type-name specification did not subtype itself")
	}
	if equal, err := RapideTypesEqual(anyEmployee, exactEmployee); err != nil || equal {
		t.Fatalf("unbounded and exact type-name interfaces equality=%v, %v", equal, err)
	}
}

func TestRapideTypeNameConstituentUsesPublishedRegionRules(t *testing.T) {
	stringType := mustRapidePredefinedType(t, "String")
	privateTarget := mustRapideInterfaceType(t, UnboundedPrivateRapideTypeName("Element"))
	providedSource := mustRapideInterfaceType(t, ExactProvidedRapideTypeName("Element", stringType))
	privateSource := mustRapideInterfaceType(t, ExactPrivateRapideTypeName("Element", stringType))
	providedTarget := mustRapideInterfaceType(t, UnboundedProvidedRapideTypeName("Element"))

	if !mustRapideSubtype(t, providedSource, privateTarget) || !mustRapideSubtype(t, privateSource, privateTarget) {
		t.Fatal("target private type name was not satisfiable by source provides/private")
	}
	if mustRapideSubtype(t, privateSource, providedTarget) {
		t.Fatal("source private type name satisfied a target provided type name")
	}
	wrongName := mustRapideInterfaceType(t, UnboundedProvidedRapideTypeName("Other"))
	if mustRapideSubtype(t, providedSource, wrongName) {
		t.Fatal("type-name subtyping ignored the constituent identifier")
	}
	objectInstead := mustRapideInterfaceType(t, ProvidedRapideMember("Element", stringType))
	if mustRapideSubtype(t, objectInstead, providedTarget) {
		t.Fatal("object constituent satisfied a type-name constituent")
	}
}

func TestRapideTypeConstructorConformanceAndResultSpecifications(t *testing.T) {
	stringType := mustRapidePredefinedType(t, "String")
	floatType := mustRapidePredefinedType(t, "Float")
	salaryInfo := mustRapideInterfaceType(t, ProvidedRapideMember("Salary", floatType))
	employee := mustRapideInterfaceType(t,
		ProvidedRapideMember("Name", stringType),
		ProvidedRapideMember("Salary", floatType),
	)
	unboundedParameter := []RapideFunctionParameter{RapideTypeParameter("Element")}
	employeeParameter := []RapideFunctionParameter{BoundedRapideTypeParameter("element", employee)}

	generalCharacteristic := mustRapideInterfaceType(t,
		UnboundedProvidedRapideTypeConstructor("Collection", unboundedParameter...),
	)
	narrowCharacteristic := mustRapideInterfaceType(t,
		UnboundedProvidedRapideTypeConstructor("collection", employeeParameter...),
	)
	if !mustRapideSubtype(t, generalCharacteristic, narrowCharacteristic) {
		t.Fatal("constructor characteristic function did not use function-type conformance")
	}
	if mustRapideSubtype(t, narrowCharacteristic, generalCharacteristic) {
		t.Fatal("constructor characteristic-function parameter variance was reversed")
	}

	boundedEmployee := mustRapideInterfaceType(t,
		BoundedProvidedRapideTypeConstructor("Collection", employee, unboundedParameter...),
	)
	boundedSalary := mustRapideInterfaceType(t,
		BoundedProvidedRapideTypeConstructor("collection", salaryInfo, unboundedParameter...),
	)
	if !mustRapideSubtype(t, boundedEmployee, boundedSalary) {
		t.Fatal("constructor application-result bounds did not narrow covariantly")
	}
	if mustRapideSubtype(t, boundedSalary, boundedEmployee) {
		t.Fatal("constructor result-bound variance was reversed")
	}
	if mustRapideSubtype(t, generalCharacteristic, boundedSalary) {
		t.Fatal("unbounded constructor denotation satisfied a bounded target")
	}

	exactEmployee := mustRapideInterfaceType(t,
		ExactProvidedRapideTypeConstructor("Collection", employee, unboundedParameter...),
	)
	if !mustRapideSubtype(t, exactEmployee, boundedSalary) {
		t.Fatal("exact constructor declaration did not satisfy a wider application bound")
	}
	exactEmployeeAgain := mustRapideInterfaceType(t,
		ExactProvidedRapideTypeConstructor("collection", employee, RapideTypeParameter("element")),
	)
	if !mustRapideSubtype(t, exactEmployee, exactEmployeeAgain) || !mustRapideSubtype(t, exactEmployeeAgain, exactEmployee) {
		t.Fatal("equal closed constructor declarations were not mutually subtyping")
	}
	exactSalary := mustRapideInterfaceType(t,
		ExactProvidedRapideTypeConstructor("Collection", salaryInfo, unboundedParameter...),
	)
	if mustRapideSubtype(t, exactEmployee, exactSalary) {
		t.Fatal("distinct exact constructor application results were treated as the same constructor")
	}
}

func TestRapideTypeConstructorUsesPublishedProvidesPrivateRegions(t *testing.T) {
	parameters := []RapideFunctionParameter{RapideTypeParameter("Element")}
	publicSource := mustRapideInterfaceType(t,
		UnboundedProvidedRapideTypeConstructor("Sequence", parameters...),
	)
	privateTarget := mustRapideInterfaceType(t,
		UnboundedPrivateRapideTypeConstructor("sequence", parameters...),
	)
	if !mustRapideSubtype(t, publicSource, privateTarget) {
		t.Fatal("public type constructor did not cover target private constituent")
	}
	if mustRapideSubtype(t, privateTarget, publicSource) {
		t.Fatal("private type constructor incorrectly covered target public constituent")
	}
}

func TestRapideTypeConstructorCanonicalArtifactAndOrdering(t *testing.T) {
	previous := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(previous)
	stringType := mustRapidePredefinedType(t, "String")
	integerType := mustRapidePredefinedType(t, "Integer")
	resultType := mustRapideInterfaceType(t, ProvidedRapideMember("Name", stringType))
	parameters := []RapideFunctionParameter{
		RapideTypeParameter("Element"),
		RapideObjectParameter("Size", integerType),
	}
	member := BoundedProvidedRapideTypeConstructor("Collection", resultType, parameters...)
	typ := mustRapideInterfaceType(t, member)
	encoded := mustMarshalRapideType(t, typ)
	want := `{"format":"gorapide.rapide-type.v2","type":{"kind":"interface","members":[` +
		`{"kind":"type_constructor","region":"provides","specification":"subtype","name":"collection",` +
		`"characteristic":{"kind":"function","parameters":[{"name":"element","kind":"type"},` +
		`{"name":"size","type":{"kind":"predefined","name":"Integer"}}],"result":{"kind":"interface"}},` +
		`"type":{"kind":"interface","members":[{"region":"provides","name":"name","type":{"kind":"predefined","name":"String"}}]}}]}}`
	if string(encoded) != want {
		t.Fatalf("type-constructor canonical bytes:\nwant %s\n got %s", want, encoded)
	}
	parsed, err := ParseRapideType(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if got := mustMarshalRapideType(t, parsed); !bytes.Equal(got, encoded) {
		t.Fatalf("type-constructor round trip changed bytes:\n%s\n%s", encoded, got)
	}
	for _, processors := range []int{1, 4} {
		runtime.GOMAXPROCS(processors)
		rebuilt := mustRapideInterfaceType(t,
			BoundedProvidedRapideTypeConstructor("COLLECTION", resultType,
				RapideTypeParameter("ELEMENT"), RapideObjectParameter("SIZE", integerType)),
		)
		if got := mustMarshalRapideType(t, rebuilt); !bytes.Equal(got, encoded) {
			t.Fatalf("GOMAXPROCS=%d changed type-constructor bytes:\n%s\n%s", processors, encoded, got)
		}
	}
}

func TestRapideTypeNameIdentifiersCannotOverloadOrCollide(t *testing.T) {
	stringType := mustRapidePredefinedType(t, "String")
	eventType := mustRapideEventType(t)
	tests := []struct {
		name    string
		members []RapideInterfaceMember
	}{
		{
			name: "two type specifications",
			members: []RapideInterfaceMember{
				UnboundedProvidedRapideTypeName("Element"),
				ExactPrivateRapideTypeName("element", stringType),
			},
		},
		{
			name: "object collision",
			members: []RapideInterfaceMember{
				UnboundedProvidedRapideTypeName("Element"),
				ProvidedRapideMember("ELEMENT", stringType),
			},
		},
		{
			name: "action collision",
			members: []RapideInterfaceMember{
				UnboundedProvidedRapideTypeName("Element"),
				OutputRapideAction("element", eventType),
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewRapideInterfaceType(test.members...); !errors.Is(err, ErrInvalidRapideType) {
				t.Fatalf("error=%v, want type-name collision", err)
			}
		})
	}
	if _, err := NewRapideInterfaceType(
		ProvidedRapideMember("Value", stringType),
		ProvidedRapideMember("value", mustRapideInterfaceType(t)),
	); err != nil {
		t.Fatalf("ordinary object overloading was incorrectly prohibited: %v", err)
	}
}

func TestRapideTypeNameCanonicalArtifactsAreStrictAndStable(t *testing.T) {
	previous := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(previous)
	stringType := mustRapidePredefinedType(t, "String")
	floatType := mustRapidePredefinedType(t, "Float")
	salaryInfo := mustRapideInterfaceType(t, ProvidedRapideMember("Salary", floatType))
	orders := [][]RapideInterfaceMember{
		{
			UnboundedPrivateRapideTypeName("Cache_Type"),
			ExactProvidedRapideTypeName("Name_Type", stringType),
			BoundedProvidedRapideTypeName("Employee", salaryInfo),
		},
		{
			BoundedProvidedRapideTypeName("employee", salaryInfo),
			UnboundedPrivateRapideTypeName("CACHE_TYPE"),
			ExactProvidedRapideTypeName("name_type", stringType),
		},
	}
	var baseline []byte
	for _, processors := range []int{1, 4} {
		runtime.GOMAXPROCS(processors)
		for _, order := range orders {
			typ := mustRapideInterfaceType(t, order...)
			encoded := mustMarshalRapideType(t, typ)
			if baseline == nil {
				baseline = encoded
			} else if !bytes.Equal(encoded, baseline) {
				t.Fatalf("GOMAXPROCS=%d/order changed type-name bytes:\n%s\n%s", processors, baseline, encoded)
			}
			parsed, err := ParseRapideType(encoded)
			if err != nil {
				t.Fatal(err)
			}
			if roundTrip := mustMarshalRapideType(t, parsed); !bytes.Equal(roundTrip, encoded) {
				t.Fatalf("type-name round trip changed bytes:\n%s\n%s", encoded, roundTrip)
			}
		}
	}
	want := `{"format":"gorapide.rapide-type.v2","type":{"kind":"interface","members":[` +
		`{"kind":"type","region":"provides","specification":"subtype","name":"employee","type":{"kind":"interface","members":[{"region":"provides","name":"salary","type":{"kind":"predefined","name":"Float"}}]}},` +
		`{"kind":"type","region":"provides","specification":"exact","name":"name_type","type":{"kind":"predefined","name":"String"}},` +
		`{"kind":"type","region":"private","specification":"any","name":"cache_type"}]}}`
	if string(baseline) != want {
		t.Fatalf("type-name canonical bytes:\n%s\n%s", want, baseline)
	}
}

func TestRapideEventAndActionCanonicalArtifactsAreStrictAndStable(t *testing.T) {
	previous := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(previous)
	integerType := mustRapidePredefinedType(t, "Integer")
	stringType := mustRapidePredefinedType(t, "String")
	firstEvent := mustRapideEventType(t,
		RapideEventParam("Message", stringType),
		RapideEventParam("ID", integerType),
	)
	secondEvent := mustRapideEventType(t,
		RapideEventParam("id", integerType),
		RapideEventParam("message", stringType),
	)
	first := mustRapideInterfaceType(t,
		OutputRapideAction("Published", firstEvent),
		ProvidedRapideMember("Name", stringType),
		InputRapideAction("Receive", firstEvent),
	)
	second := mustRapideInterfaceType(t,
		InputRapideAction("receive", secondEvent),
		ProvidedRapideMember("name", stringType),
		OutputRapideAction("published", secondEvent),
	)
	firstBytes := mustMarshalRapideType(t, first)
	secondBytes := mustMarshalRapideType(t, second)
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatalf("event/action order or case changed bytes:\n%s\n%s", firstBytes, secondBytes)
	}
	parsed, err := ParseRapideType(firstBytes)
	if err != nil {
		t.Fatal(err)
	}
	if got := mustMarshalRapideType(t, parsed); !bytes.Equal(got, firstBytes) {
		t.Fatalf("event/action round trip changed bytes:\n%s\n%s", firstBytes, got)
	}
	for _, processors := range []int{1, 4} {
		runtime.GOMAXPROCS(processors)
		rebuiltEvent := mustRapideEventType(t,
			RapideEventParam("MESSAGE", stringType),
			RapideEventParam("id", integerType),
		)
		rebuilt := mustRapideInterfaceType(t,
			InputRapideAction("RECEIVE", rebuiltEvent),
			OutputRapideAction("PUBLISHED", rebuiltEvent),
			ProvidedRapideMember("NAME", stringType),
		)
		if got := mustMarshalRapideType(t, rebuilt); !bytes.Equal(got, firstBytes) {
			t.Fatalf("GOMAXPROCS=%d changed event/action bytes:\n%s\n%s", processors, firstBytes, got)
		}
	}
}

func TestRapideTypeV2KeepsExistingMemberDescriptorShapesStable(t *testing.T) {
	stringType := mustRapidePredefinedType(t, "String")
	interfaceType := mustRapideInterfaceType(t, ProvidedRapideMember("Name", stringType))
	wantInterface := `{"format":"gorapide.rapide-type.v2","type":{"kind":"interface","members":[{"region":"provides","name":"name","type":{"kind":"predefined","name":"String"}}]}}`
	if got := string(mustMarshalRapideType(t, interfaceType)); got != wantInterface {
		t.Fatalf("object-interface v2 bytes changed:\n%s\n%s", wantInterface, got)
	}
	functionType := mustRapideFunctionType(t,
		[]RapideFunctionParameter{RapideObjectParameter("Value", stringType)}, stringType)
	wantFunction := `{"format":"gorapide.rapide-type.v2","type":{"kind":"function","parameters":[{"name":"value","type":{"kind":"predefined","name":"String"}}],"result":{"kind":"predefined","name":"String"}}}`
	if got := string(mustMarshalRapideType(t, functionType)); got != wantFunction {
		t.Fatalf("function v2 bytes changed:\n%s\n%s", wantFunction, got)
	}
}

func TestRapideTypeCanonicalRoundTripAndOrdering(t *testing.T) {
	stringType := mustRapidePredefinedType(t, "String")
	integerType := mustRapidePredefinedType(t, "Integer")
	firstMembers := []RapideInterfaceMember{
		RequiredRapideMember("Clock", integerType),
		ProvidedRapideMember("Name", stringType),
		PrivateRapideMember("Cache", stringType),
	}
	secondMembers := []RapideInterfaceMember{
		PrivateRapideMember("CACHE", stringType),
		ProvidedRapideMember("name", stringType),
		RequiredRapideMember("clock", integerType),
	}
	first := mustRapideInterfaceType(t, firstMembers...)
	second := mustRapideInterfaceType(t, secondMembers...)

	firstBytes := mustMarshalRapideType(t, first)
	secondBytes := mustMarshalRapideType(t, second)
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatalf("member order/case changed canonical descriptor:\n%s\n%s", firstBytes, secondBytes)
	}
	firstDigest, err := first.DescriptorDigest()
	if err != nil {
		t.Fatal(err)
	}
	secondDigest, err := second.DescriptorDigest()
	if err != nil {
		t.Fatal(err)
	}
	if firstDigest != secondDigest {
		t.Fatalf("descriptor digest changed: %q != %q", firstDigest, secondDigest)
	}

	parsed, err := ParseRapideType(firstBytes)
	if err != nil {
		t.Fatal(err)
	}
	if got := mustMarshalRapideType(t, parsed); !bytes.Equal(got, firstBytes) {
		t.Fatalf("round trip changed bytes:\n%s\n%s", firstBytes, got)
	}
	equal, err := RapideTypesEqual(first, parsed)
	if err != nil || !equal {
		t.Fatalf("round-trip type equality = %v, %v", equal, err)
	}

	// Constructors copy caller-owned slices into private immutable storage.
	parameters := []RapideFunctionParameter{RapideObjectParameter("value", stringType)}
	function := mustRapideFunctionType(t, parameters, stringType)
	before := mustMarshalRapideType(t, function)
	parameters[0] = RapideObjectParameter("changed", integerType)
	after := mustMarshalRapideType(t, function)
	if !bytes.Equal(before, after) {
		t.Fatal("caller mutation changed an immutable function descriptor")
	}
}

func TestParseRapideTypeRejectsMalformedAndNoncanonicalArtifacts(t *testing.T) {
	validEmpty := `{"format":"gorapide.rapide-type.v2","type":{"kind":"interface"}}`
	cases := map[string]string{
		"wrong format":       `{"format":"gorapide.rapide-type.v0","type":{"kind":"interface"}}`,
		"unknown field":      `{"format":"gorapide.rapide-type.v2","type":{"kind":"interface","extra":true}}`,
		"unknown kind":       `{"format":"gorapide.rapide-type.v2","type":{"kind":"record"}}`,
		"noncanonical alias": `{"format":"gorapide.rapide-type.v2","type":{"kind":"predefined","name":"Natural"}}`,
		"noncanonical space": validEmpty + "\n",
		"multiple values":    validEmpty + validEmpty,
		"event parameter order": `{"format":"gorapide.rapide-type.v2","type":{"kind":"event","event_parameters":[` +
			`{"name":"z","type":{"kind":"predefined","name":"String"}},` +
			`{"name":"a","type":{"kind":"predefined","name":"String"}}]}}`,
		"action object region": `{"format":"gorapide.rapide-type.v2","type":{"kind":"interface","members":[` +
			`{"kind":"action","region":"provides","mode":"out","name":"x","type":{"kind":"event"}}]}}`,
		"type name requires region": `{"format":"gorapide.rapide-type.v2","type":{"kind":"interface","members":[` +
			`{"kind":"type","region":"requires","specification":"any","name":"t"}]}}`,
		"unbounded type name with type": `{"format":"gorapide.rapide-type.v2","type":{"kind":"interface","members":[` +
			`{"kind":"type","region":"provides","specification":"any","name":"t","type":{"kind":"predefined","name":"String"}}]}}`,
		"bounded type name without bound": `{"format":"gorapide.rapide-type.v2","type":{"kind":"interface","members":[` +
			`{"kind":"type","region":"provides","specification":"subtype","name":"t"}]}}`,
		"type name action mode": `{"format":"gorapide.rapide-type.v2","type":{"kind":"interface","members":[` +
			`{"kind":"type","region":"provides","mode":"out","specification":"any","name":"t"}]}}`,
		"unknown type name specification": `{"format":"gorapide.rapide-type.v2","type":{"kind":"interface","members":[` +
			`{"kind":"type","region":"provides","specification":"compatible","name":"t"}]}}`,
		"type constructor requires region": `{"format":"gorapide.rapide-type.v2","type":{"kind":"interface","members":[` +
			`{"kind":"type_constructor","region":"requires","specification":"any","name":"t",` +
			`"characteristic":{"kind":"function","result":{"kind":"interface"}}}]}}`,
		"type constructor missing characteristic": `{"format":"gorapide.rapide-type.v2","type":{"kind":"interface","members":[` +
			`{"kind":"type_constructor","region":"provides","specification":"any","name":"t"}]}}`,
		"type constructor characteristic has result": `{"format":"gorapide.rapide-type.v2","type":{"kind":"interface","members":[` +
			`{"kind":"type_constructor","region":"provides","specification":"any","name":"t",` +
			`"characteristic":{"kind":"function","result":{"kind":"predefined","name":"String"}}}]}}`,
		"module generator missing signature": `{"format":"gorapide.rapide-type.v2","type":{"kind":"interface","members":[` +
			`{"kind":"module_generator","region":"provides","name":"iterator"}]}}`,
		"module generator has object type": `{"format":"gorapide.rapide-type.v2","type":{"kind":"interface","members":[` +
			`{"kind":"module_generator","region":"provides","name":"iterator",` +
			`"type":{"kind":"predefined","name":"String"}}]}}`,
		"module generator has noninterface result": `{"format":"gorapide.rapide-type.v2","type":{"kind":"interface","members":[` +
			`{"kind":"module_generator","region":"provides","name":"iterator",` +
			`"type":{"kind":"function","result":{"kind":"predefined","name":"String"}}}]}}`,
		"unbounded type constructor with result bound": `{"format":"gorapide.rapide-type.v2","type":{"kind":"interface","members":[` +
			`{"kind":"type_constructor","region":"provides","specification":"any","name":"t",` +
			`"characteristic":{"kind":"function","result":{"kind":"interface"}},` +
			`"type":{"kind":"predefined","name":"String"}}]}}`,
		"bounded type constructor without result bound": `{"format":"gorapide.rapide-type.v2","type":{"kind":"interface","members":[` +
			`{"kind":"type_constructor","region":"provides","specification":"subtype","name":"t",` +
			`"characteristic":{"kind":"function","result":{"kind":"interface"}}}]}}`,
		"formal type parameter with default": `{"format":"gorapide.rapide-type.v2","type":{"kind":"function","parameters":[` +
			`{"name":"t","kind":"type","has_default":true}],"result":{"kind":"interface"}}}`,
		"standalone type reference": `{"format":"gorapide.rapide-type.v2","type":` +
			`{"kind":"type_reference","name":"element"}}`,
		"type reference without name": `{"format":"gorapide.rapide-type.v2","type":` +
			`{"kind":"type_reference"}}`,
		"type reference with members": `{"format":"gorapide.rapide-type.v2","type":` +
			`{"kind":"type_reference","name":"element","members":[]}}`,
		"standalone recursive reference": `{"format":"gorapide.rapide-type.v2","type":` +
			`{"kind":"recursive_reference","depth":0}}`,
		"recursive reference without depth": `{"format":"gorapide.rapide-type.v2","type":` +
			`{"kind":"recursive_reference"}}`,
		"recursive reference with name": `{"format":"gorapide.rapide-type.v2","type":` +
			`{"kind":"recursive_reference","name":"node","depth":0}}`,
		"ordinary interface with recursive reference": `{"format":"gorapide.rapide-type.v2","type":{"kind":"interface","members":[` +
			`{"region":"provides","name":"next","type":{"kind":"recursive_reference","depth":0}}]}}`,
		"unused recursive binder": `{"format":"gorapide.rapide-type.v2","type":` +
			`{"kind":"recursive_interface"}}`,
		"recursive reference beyond binders": `{"format":"gorapide.rapide-type.v2","type":{"kind":"recursive_interface","members":[` +
			`{"region":"provides","name":"next","type":{"kind":"recursive_reference","depth":1}}]}}`,
		"duplicate member": `{"format":"gorapide.rapide-type.v2","type":{"kind":"interface","members":[` +
			`{"kind":"object","region":"provides","name":"x","type":{"kind":"predefined","name":"String"}},` +
			`{"kind":"object","region":"provides","name":"x","type":{"kind":"predefined","name":"String"}}]}}`,
	}
	for name, encoded := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ParseRapideType([]byte(encoded))
			if !errors.Is(err, ErrInvalidRapideType) {
				t.Fatalf("error = %v, want ErrInvalidRapideType", err)
			}
		})
	}
}

func TestRapideTypeNameReferencesAreScopedStructuralNodes(t *testing.T) {
	reference, err := NewRapideTypeNameReference("Element")
	if err != nil {
		t.Fatal(err)
	}
	read := mustRapideFunctionType(t, nil, reference)
	schema := mustRapideInterfaceType(t,
		UnboundedProvidedRapideTypeName("ELEMENT"),
		ProvidedRapideMember("Item", reference),
		ProvidedRapideMember("Read", read),
	)
	want := `{"format":"gorapide.rapide-type.v2","type":{"kind":"interface","members":[` +
		`{"region":"provides","name":"item","type":{"kind":"type_reference","name":"element"}},` +
		`{"region":"provides","name":"read","type":{"kind":"function","result":{"kind":"type_reference","name":"element"}}},` +
		`{"kind":"type","region":"provides","specification":"any","name":"element"}]}}`
	encoded := mustMarshalRapideType(t, schema)
	if string(encoded) != want {
		t.Fatalf("scoped type-name artifact:\n%s\nwant:\n%s", encoded, want)
	}
	parsed, err := ParseRapideType(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if roundTrip := mustMarshalRapideType(t, parsed); !bytes.Equal(roundTrip, encoded) {
		t.Fatalf("type-name reference round trip changed bytes:\n%s\n%s", encoded, roundTrip)
	}

	if _, err := reference.MarshalCanonical(); !errors.Is(err, ErrInvalidRapideType) ||
		!strings.Contains(err.Error(), "unscoped type-name reference") {
		t.Fatalf("standalone reference marshal error = %v", err)
	}
	if _, err := IsRapideSubtype(reference, reference); !errors.Is(err, ErrInvalidRapideType) ||
		!strings.Contains(err.Error(), "unscoped type-name reference") {
		t.Fatalf("standalone reference subtype error = %v", err)
	}
	unknown, err := NewRapideTypeNameReference("Missing")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewRapideInterfaceType(
		UnboundedProvidedRapideTypeName("Element"),
		ProvidedRapideMember("Item", unknown),
	); !errors.Is(err, ErrInvalidRapideType) || !strings.Contains(err.Error(), `unscoped type-name reference "missing"`) {
		t.Fatalf("unknown interface reference error = %v", err)
	}
}

func TestRapideTypeNameReferencesFollowEnclosingSpecificationConformance(t *testing.T) {
	stringType := mustRapidePredefinedType(t, "String")
	element := mustRapideTypeNameReference(t, "Element")
	source := mustRapideInterfaceType(t,
		ExactProvidedRapideTypeName("Element", stringType),
		ProvidedRapideMember("Item", element),
	)
	target := mustRapideInterfaceType(t,
		UnboundedProvidedRapideTypeName("element"),
		ProvidedRapideMember("item", element),
	)
	if !mustRapideSubtype(t, source, target) {
		t.Fatal("exact source denotation did not conform to an unbounded target with the same symbolic object type")
	}
	if mustRapideSubtype(t, target, source) {
		t.Fatal("unbounded source denotation unexpectedly conformed to an exact target")
	}

	value := mustRapideTypeNameReference(t, "Value")
	different := mustRapideInterfaceType(t,
		UnboundedProvidedRapideTypeName("Value"),
		ProvidedRapideMember("Item", value),
	)
	if mustRapideSubtype(t, source, different) {
		t.Fatal("different type-name constituents were treated as the same symbolic denotation")
	}
}

func TestNestedRapideInterfaceTypeNameReferencesUseTheirOwnScope(t *testing.T) {
	innerReference := mustRapideTypeNameReference(t, "Inner")
	inner := mustRapideInterfaceType(t,
		UnboundedProvidedRapideTypeName("Inner"),
		ProvidedRapideMember("Value", innerReference),
	)
	outer := mustRapideInterfaceType(t,
		UnboundedProvidedRapideTypeName("Outer"),
		ProvidedRapideMember("Nested", inner),
	)
	if _, err := outer.MarshalCanonical(); err != nil {
		t.Fatal(err)
	}

	outerReference := mustRapideTypeNameReference(t, "Outer")
	leakyInner := RapideType{node: &rapideTypeNode{
		kind: rapideInterfaceType,
		members: []rapideInterfaceConstituent{{
			kind: rapideObjectConstituent, region: rapideProvidesRegion,
			name: "leaked", typ: outerReference.node,
		}},
	}}
	if _, err := NewRapideInterfaceType(
		UnboundedProvidedRapideTypeName("Outer"),
		ProvidedRapideMember("Nested", leakyInner),
	); !errors.Is(err, ErrInvalidRapideType) || !strings.Contains(err.Error(), `unscoped type-name reference "outer"`) {
		t.Fatalf("nested scope leak error = %v", err)
	}
}

func TestSelfRecursiveRapideInterfaceIsStructuralAndCanonical(t *testing.T) {
	stringType := mustRapidePredefinedType(t, "String")
	node, err := NewSelfRecursiveRapideInterfaceType(func(self RapideType) (RapideType, error) {
		return NewRapideInterfaceType(
			ProvidedRapideMember("Name", stringType),
			ProvidedRapideMember("Child", self),
		)
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"format":"gorapide.rapide-type.v2","type":{"kind":"recursive_interface","members":[` +
		`{"region":"provides","name":"child","type":{"kind":"recursive_reference","depth":0}},` +
		`{"region":"provides","name":"name","type":{"kind":"predefined","name":"String"}}]}}`
	encoded := mustMarshalRapideType(t, node)
	if string(encoded) != want {
		t.Fatalf("recursive type artifact:\n%s\nwant:\n%s", encoded, want)
	}
	parsed, err := ParseRapideType(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if roundTrip := mustMarshalRapideType(t, parsed); !bytes.Equal(roundTrip, encoded) {
		t.Fatalf("recursive type round trip changed bytes:\n%s\n%s", encoded, roundTrip)
	}
	if equal, err := RapideTypesEqual(node, parsed); err != nil || !equal {
		t.Fatalf("recursive round-trip equality = %v, %v", equal, err)
	}
}

func TestRecursiveRapideInterfaceSubtypeUsesCoinductiveAssumption(t *testing.T) {
	stringType := mustRapidePredefinedType(t, "String")
	wide, err := NewSelfRecursiveRapideInterfaceType(func(self RapideType) (RapideType, error) {
		return NewRapideInterfaceType(
			ProvidedRapideMember("Next", self),
			ProvidedRapideMember("Name", stringType),
		)
	})
	if err != nil {
		t.Fatal(err)
	}
	narrow, err := NewSelfRecursiveRapideInterfaceType(func(self RapideType) (RapideType, error) {
		return NewRapideInterfaceType(ProvidedRapideMember("Next", self))
	})
	if err != nil {
		t.Fatal(err)
	}
	if !mustRapideSubtype(t, wide, narrow) {
		t.Fatal("wider recursive interface did not subtype its recursive projection")
	}
	if mustRapideSubtype(t, narrow, wide) {
		t.Fatal("recursive projection unexpectedly supplied the additional Name member")
	}
}

func TestMutuallyRecursiveRapideInterfacesUseLexicalBinderDepth(t *testing.T) {
	left, err := NewSelfRecursiveRapideInterfaceType(func(leftSelf RapideType) (RapideType, error) {
		right, err := NewSelfRecursiveRapideInterfaceType(func(_ RapideType) (RapideType, error) {
			return NewRapideInterfaceType(ProvidedRapideMember("Left", leftSelf))
		})
		if err != nil {
			return RapideType{}, err
		}
		return NewRapideInterfaceType(ProvidedRapideMember("Right", right))
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"format":"gorapide.rapide-type.v2","type":{"kind":"recursive_interface","members":[` +
		`{"region":"provides","name":"right","type":{"kind":"interface","members":[` +
		`{"region":"provides","name":"left","type":{"kind":"recursive_reference","depth":0}}]}}]}}`
	encoded := mustMarshalRapideType(t, left)
	if string(encoded) != want {
		t.Fatalf("mutually recursive binder artifact:\n%s\nwant:\n%s", encoded, want)
	}
	parsed, err := ParseRapideType(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if equal, err := RapideTypesEqual(left, parsed); err != nil || !equal {
		t.Fatalf("mutually recursive round-trip equality = %v, %v", equal, err)
	}
}

func TestRecursiveRapideTypeCanonicalBytesIgnoreOrderCaseAndGOMAXPROCS(t *testing.T) {
	previous := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(previous)
	stringType := mustRapidePredefinedType(t, "String")
	var baseline []byte
	for iteration := 0; iteration < 20; iteration++ {
		if iteration == 10 {
			runtime.GOMAXPROCS(4)
		}
		typ, err := NewSelfRecursiveRapideInterfaceType(func(self RapideType) (RapideType, error) {
			if iteration%2 == 0 {
				return NewRapideInterfaceType(
					ProvidedRapideMember("Next", self),
					ProvidedRapideMember("Name", stringType),
				)
			}
			return NewRapideInterfaceType(
				ProvidedRapideMember("NAME", stringType),
				ProvidedRapideMember("NEXT", self),
			)
		})
		if err != nil {
			t.Fatal(err)
		}
		encoded := mustMarshalRapideType(t, typ)
		if baseline == nil {
			baseline = encoded
		} else if !bytes.Equal(encoded, baseline) {
			t.Fatalf("order/case/GOMAXPROCS changed recursive bytes:\n%s\n%s", baseline, encoded)
		}
	}
}

func TestRapideTypeConstructorsRejectInvalidDescriptors(t *testing.T) {
	stringType := mustRapidePredefinedType(t, "String")
	if _, err := NewSelfRecursiveRapideInterfaceType(nil); !errors.Is(err, ErrInvalidRapideType) {
		t.Fatalf("nil recursive builder error = %v", err)
	}
	if _, err := NewSelfRecursiveRapideInterfaceType(func(RapideType) (RapideType, error) {
		return stringType, nil
	}); !errors.Is(err, ErrInvalidRapideType) {
		t.Fatalf("non-interface recursive builder result error = %v", err)
	}
	if _, err := NewSelfRecursiveRapideInterfaceType(func(self RapideType) (RapideType, error) {
		return self, nil
	}); !errors.Is(err, ErrInvalidRapideType) {
		t.Fatalf("unbound recursive self result error = %v", err)
	}
	if _, err := RapidePredefinedType("Real"); !errors.Is(err, ErrInvalidRapideType) {
		t.Fatalf("unsupported predefined type error = %v", err)
	}
	if _, err := NewRapideInterfaceType(ProvidedRapideMember("9bad", stringType)); !errors.Is(err, ErrInvalidRapideType) {
		t.Fatalf("bad member identifier error = %v", err)
	}
	if _, err := NewRapideInterfaceType(ProvidedRapideMember("Value", RapideType{})); !errors.Is(err, ErrInvalidRapideType) {
		t.Fatalf("zero member type error = %v", err)
	}
	if _, err := NewRapideInterfaceType(
		ProvidedRapideMember("Value", stringType),
		ProvidedRapideMember("value", stringType),
	); !errors.Is(err, ErrInvalidRapideType) {
		t.Fatalf("duplicate member error = %v", err)
	}
	if _, err := NewRapideFunctionType([]RapideFunctionParameter{
		RapideObjectParameter("Value", stringType),
		RapideObjectParameter("value", stringType),
	}, stringType); !errors.Is(err, ErrInvalidRapideType) {
		t.Fatalf("duplicate parameter error = %v", err)
	}
	if _, err := NewRapideFunctionType([]RapideFunctionParameter{{
		kind: rapideTypeFunctionParameter, name: "T", hasDefault: true,
	}}, stringType); !errors.Is(err, ErrInvalidRapideType) {
		t.Fatalf("defaulted type parameter error = %v", err)
	}
	if _, err := NewRapideFunctionType([]RapideFunctionParameter{{name: "T"}}, stringType); !errors.Is(err, ErrInvalidRapideType) {
		t.Fatalf("unknown parameter kind error = %v", err)
	}
	if _, err := IsRapideSubtype(RapideType{}, stringType); !errors.Is(err, ErrInvalidRapideType) {
		t.Fatalf("zero subtype operand error = %v", err)
	}
	malformedReferenceField := RapideType{node: &rapideTypeNode{
		kind: rapidePredefinedLibraryType, predefined: "String", reference: "element",
	}}
	if _, err := IsRapideSubtype(malformedReferenceField, stringType); !errors.Is(err, ErrInvalidRapideType) {
		t.Fatalf("reference field on predefined subtype error = %v", err)
	}
	if _, err := malformedReferenceField.MarshalCanonical(); !errors.Is(err, ErrInvalidRapideType) {
		t.Fatalf("reference field on predefined marshal error = %v", err)
	}
	if _, err := NewRapideEventType(RapideEventParam("9bad", stringType)); !errors.Is(err, ErrInvalidRapideType) {
		t.Fatalf("bad event parameter identifier error = %v", err)
	}
	if _, err := NewRapideEventType(RapideEventParam("value", RapideType{})); !errors.Is(err, ErrInvalidRapideType) {
		t.Fatalf("zero event parameter type error = %v", err)
	}
	if _, err := NewRapideEventType(
		RapideEventParam("Value", stringType),
		RapideEventParam("value", stringType),
	); !errors.Is(err, ErrInvalidRapideType) {
		t.Fatalf("duplicate event parameter error = %v", err)
	}
	if _, err := NewRapideInterfaceType(OutputRapideAction("Published", stringType)); !errors.Is(err, ErrInvalidRapideType) {
		t.Fatalf("non-event action type error = %v", err)
	}
	if _, err := NewRapideInterfaceType(BoundedProvidedRapideTypeName("Element", RapideType{})); !errors.Is(err, ErrInvalidRapideType) {
		t.Fatalf("zero type-name bound error = %v", err)
	}
	if _, err := NewRapideInterfaceType(RapideInterfaceMember{
		kind: rapideTypeNameConstituent, region: rapideRequiresRegion,
		typeSpecification: rapideAnyTypeDenotation, name: "Element",
	}); !errors.Is(err, ErrInvalidRapideType) {
		t.Fatalf("requires type-name error = %v", err)
	}
	if _, err := NewRapideInterfaceType(RapideInterfaceMember{
		kind: rapideTypeNameConstituent, region: rapideProvidesRegion,
		typeSpecification: rapideAnyTypeDenotation, name: "Element", typ: stringType,
	}); !errors.Is(err, ErrInvalidRapideType) {
		t.Fatalf("unbounded type-name carrying bound error = %v", err)
	}
	if _, err := NewRapideInterfaceType(BoundedProvidedRapideTypeConstructor(
		"Collection", RapideType{}, RapideTypeParameter("Element"),
	)); !errors.Is(err, ErrInvalidRapideType) {
		t.Fatalf("zero type-constructor bound error = %v", err)
	}
	if _, err := NewRapideInterfaceType(ExactProvidedRapideTypeConstructor(
		"Collection", stringType, DefaultedRapideObjectParameter("Size", stringType),
	)); !errors.Is(err, ErrInvalidRapideType) {
		t.Fatalf("exact constructor with unrepresented default error = %v", err)
	}
	if _, err := NewRapideInterfaceType(RapideInterfaceMember{
		kind: rapideTypeConstructorConstituent, region: rapideRequiresRegion,
		typeSpecification: rapideAnyTypeDenotation, name: "Collection",
	}); !errors.Is(err, ErrInvalidRapideType) {
		t.Fatalf("requires type-constructor error = %v", err)
	}
	if _, err := NewRapideInterfaceType(
		UnboundedProvidedRapideTypeConstructor("Collection", RapideTypeParameter("Element")),
		ProvidedRapideMember("collection", stringType),
	); !errors.Is(err, ErrInvalidRapideType) {
		t.Fatalf("type-constructor/object collision error = %v", err)
	}
}

func TestRapideTypeCanonicalBytesIgnoreGOMAXPROCSAndInsertionOrder(t *testing.T) {
	previous := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(previous)
	stringType := mustRapidePredefinedType(t, "String")
	integerType := mustRapidePredefinedType(t, "Integer")
	orders := [][]RapideInterfaceMember{
		{
			ProvidedRapideMember("Zed", stringType),
			RequiredRapideMember("Source", integerType),
			ProvidedRapideMember("Alpha", integerType),
		},
		{
			ProvidedRapideMember("alpha", integerType),
			ProvidedRapideMember("zed", stringType),
			RequiredRapideMember("source", integerType),
		},
		{
			RequiredRapideMember("SOURCE", integerType),
			ProvidedRapideMember("ZED", stringType),
			ProvidedRapideMember("ALPHA", integerType),
		},
	}
	var baseline []byte
	for _, processors := range []int{1, 4} {
		runtime.GOMAXPROCS(processors)
		for _, order := range orders {
			typ := mustRapideInterfaceType(t, order...)
			encoded := mustMarshalRapideType(t, typ)
			if baseline == nil {
				baseline = encoded
				continue
			}
			if !bytes.Equal(encoded, baseline) {
				t.Fatalf("GOMAXPROCS=%d/order changed bytes:\n%s\n%s", processors, baseline, encoded)
			}
		}
	}
}

func mustRapidePredefinedType(t *testing.T, name string) RapideType {
	t.Helper()
	typ, err := RapidePredefinedType(name)
	if err != nil {
		t.Fatal(err)
	}
	return typ
}

func mustRapideTypeNameReference(t *testing.T, name string) RapideType {
	t.Helper()
	typ, err := NewRapideTypeNameReference(name)
	if err != nil {
		t.Fatal(err)
	}
	return typ
}

func mustRapideInterfaceType(t *testing.T, members ...RapideInterfaceMember) RapideType {
	t.Helper()
	typ, err := NewRapideInterfaceType(members...)
	if err != nil {
		t.Fatal(err)
	}
	return typ
}

func mustRapideFunctionType(t *testing.T, parameters []RapideFunctionParameter, result RapideType) RapideType {
	t.Helper()
	typ, err := NewRapideFunctionType(parameters, result)
	if err != nil {
		t.Fatal(err)
	}
	return typ
}

func mustRapideEventType(t *testing.T, parameters ...RapideEventParameter) RapideType {
	t.Helper()
	typ, err := NewRapideEventType(parameters...)
	if err != nil {
		t.Fatal(err)
	}
	return typ
}

func mustRapideSubtype(t *testing.T, left, right RapideType) bool {
	t.Helper()
	result, err := IsRapideSubtype(left, right)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func mustMarshalRapideType(t *testing.T, typ RapideType) []byte {
	t.Helper()
	encoded, err := typ.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

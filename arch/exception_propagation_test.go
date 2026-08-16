package arch

import (
	"reflect"
	"testing"
)

func TestExceptionPropagationTargetsDeduplicateAndCanonicalizeRelations(t *testing.T) {
	lifecycle := newModuleLifecycleRegistry()
	lifecycle.modules["source"] = &moduleLifecycleRuntime{moduleID: "source", parent: "parent"}
	lifecycle.modules["parent"] = &moduleLifecycleRuntime{moduleID: "parent"}
	lifecycle.modules["linked"] = &moduleLifecycleRuntime{moduleID: "linked"}
	runtime := &communicationContextRuntime{
		lifecycle: lifecycle,
		edges: map[string]*communicationContextEdge{
			"z-linked": {
				edgeID: "z-linked", source: "source", destination: "linked",
				kind: "explicit-link", live: true,
			},
			// A closed initial edge followed by an explicit Link to the parent
			// is legal. Parent propagation remains structural, so this target
			// must retain both relations without appearing twice.
			"a-parent-link": {
				edgeID: "a-parent-link", source: "source", destination: "parent",
				kind: "explicit-link", live: true,
			},
		},
	}

	targets, err := runtime.exceptionPropagationTargets("source")
	if err != nil {
		t.Fatal(err)
	}
	want := []exceptionPropagationTarget{
		{moduleID: "linked", relations: []string{"linked"}},
		{moduleID: "parent", relations: []string{"linked", "parent"}},
	}
	if !reflect.DeepEqual(targets, want) {
		t.Fatalf("exception targets=%#v, want %#v", targets, want)
	}
}

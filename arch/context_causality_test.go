package arch

import (
	"testing"

	"github.com/ShaneDolphin/gorapide"
)

func TestClosedCommunicationContextUsesCausalIntervalInsteadOfObservationTime(t *testing.T) {
	poset := gorapide.NewPoset()
	acquired := &gorapide.Event{ID: "acquired", Name: "Start", Source: "child"}
	middle := &gorapide.Event{ID: "middle", Name: "Broadcast", Source: "child"}
	lost := &gorapide.Event{ID: "lost", Name: "Terminal", Source: "child"}
	after := &gorapide.Event{ID: "after", Name: "TooLate", Source: "child"}
	independent := &gorapide.Event{ID: "independent", Name: "Concurrent", Source: "child"}
	if err := poset.AddEvent(acquired); err != nil {
		t.Fatal(err)
	}
	if err := poset.AddEventWithCause(middle, acquired.ID); err != nil {
		t.Fatal(err)
	}
	if err := poset.AddEventWithCause(lost, middle.ID); err != nil {
		t.Fatal(err)
	}
	if err := poset.AddEventWithCause(after, lost.ID); err != nil {
		t.Fatal(err)
	}
	if err := poset.AddEvent(independent); err != nil {
		t.Fatal(err)
	}
	runtime := &communicationContextRuntime{
		edges: map[string]*communicationContextEdge{
			"edge": {
				edgeID: "edge", source: "child-module", destination: "parent-module",
				kind: "initial-parent", live: false,
				acquiredAfter: []gorapide.EventID{acquired.ID},
				lostAfter:     []gorapide.EventID{lost.ID},
			},
		},
		moduleByComponent: map[string]string{"child": "child-module"},
		componentByModule: map[string]string{"parent-module": "parent"},
	}
	assertRecipients := func(event *gorapide.Event, want bool) {
		t.Helper()
		recipients := runtime.recipientsAt("child", event.ID, poset)
		if got := len(recipients) == 1 && recipients[0] == "parent"; got != want {
			t.Fatalf("event %s recipients=%v, want parent=%v", event.ID, recipients, want)
		}
	}
	assertRecipients(acquired, false)
	assertRecipients(middle, true)
	assertRecipients(lost, true)
	assertRecipients(after, false)
	assertRecipients(independent, false)
}

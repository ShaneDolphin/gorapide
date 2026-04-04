package arch

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/beautiful-majestic-dolphin/gorapide"
	"github.com/beautiful-majestic-dolphin/gorapide/pattern"
)

// --- ConnectionKind Tests ---

func TestConnectionKindConstants(t *testing.T) {
	if BasicConnection == PipeConnection {
		t.Error("BasicConnection and PipeConnection must be distinct")
	}
	if PipeConnection == AgentConnection {
		t.Error("PipeConnection and AgentConnection must be distinct")
	}
	if BasicConnection == AgentConnection {
		t.Error("BasicConnection and AgentConnection must be distinct")
	}
}

// --- Builder Tests ---

func TestConnectBuilderDefaults(t *testing.T) {
	conn := Connect("A", "B").
		On(pattern.MatchEvent("Ping")).
		Send("Pong").
		Build()

	if conn.From != "A" {
		t.Errorf("From: want A, got %s", conn.From)
	}
	if conn.To != "B" {
		t.Errorf("To: want B, got %s", conn.To)
	}
	if conn.Kind != BasicConnection {
		t.Errorf("Kind: want BasicConnection, got %d", conn.Kind)
	}
}

func TestConnectBuilderPipe(t *testing.T) {
	conn := Connect("A", "B").
		On(pattern.MatchEvent("Ping")).
		Pipe().
		Send("Pong").
		Build()

	if conn.Kind != PipeConnection {
		t.Errorf("Kind: want PipeConnection, got %d", conn.Kind)
	}
}

func TestConnectBuilderAgent(t *testing.T) {
	conn := Connect("A", "B").
		On(pattern.MatchEvent("Ping")).
		Agent().
		Forward().
		Build()

	if conn.Kind != AgentConnection {
		t.Errorf("Kind: want AgentConnection, got %d", conn.Kind)
	}
}

func TestConnectBuilderSendWith(t *testing.T) {
	conn := Connect("A", "B").
		On(pattern.MatchEvent("Req")).
		Pipe().
		SendWith("Resp", func(e *gorapide.Event) map[string]any {
			return map[string]any{"echo": e.ParamString("msg")}
		}).
		Build()

	if conn.Kind != PipeConnection {
		t.Errorf("Kind: want PipeConnection, got %d", conn.Kind)
	}
	if conn.ActionName != "Resp" {
		t.Errorf("ActionName: want Resp, got %s", conn.ActionName)
	}
}

// --- PipeConnection: creates causal edge ---

func TestPipeConnectionCreatesCausalEdge(t *testing.T) {
	p := gorapide.NewPoset()
	ifaceA := Interface("Sender").OutAction("Ping").Build()
	ifaceB := Interface("Receiver").InAction("Pong").Build()
	compA := NewComponent("A", ifaceA, p)
	compB := NewComponent("B", ifaceB, p)

	conn := Connect("A", "B").
		On(pattern.MatchEvent("Ping")).
		Pipe().
		Send("Pong").
		Build()

	// Emit an event from A.
	trigger, err := compA.Emit("Ping", map[string]any{"seq": 1})
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}

	// Execute the connection.
	err = conn.Execute(trigger, compA, compB)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// Verify new event in poset.
	pongs := p.ByName("Pong")
	if len(pongs) != 1 {
		t.Fatalf("expected 1 Pong event, got %d", len(pongs))
	}
	pong := pongs[0]

	// Verify causal link: trigger -> pong.
	if !p.IsCausallyBefore(trigger.ID, pong.ID) {
		t.Error("pipe connection should create causal edge from trigger to new event")
	}

	// Verify source is target component.
	if pong.Source != "B" {
		t.Errorf("Source: want B, got %s", pong.Source)
	}
}

// --- BasicConnection: NO causal edge ---

func TestBasicConnectionNoCausalEdge(t *testing.T) {
	p := gorapide.NewPoset()
	ifaceA := Interface("Sender").OutAction("Ping").Build()
	ifaceB := Interface("Receiver").InAction("Pong").Build()
	compA := NewComponent("A", ifaceA, p)
	compB := NewComponent("B", ifaceB, p)

	conn := Connect("A", "B").
		On(pattern.MatchEvent("Ping")).
		Send("Pong").
		Build()

	trigger, err := compA.Emit("Ping", nil)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}

	err = conn.Execute(trigger, compA, compB)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	pongs := p.ByName("Pong")
	if len(pongs) != 1 {
		t.Fatalf("expected 1 Pong event, got %d", len(pongs))
	}
	pong := pongs[0]

	// Basic connection should NOT create a causal edge.
	if p.IsCausallyBefore(trigger.ID, pong.ID) {
		t.Error("basic connection should NOT create causal edge")
	}

	// But both events should exist independently in the poset.
	if pong.Source != "B" {
		t.Errorf("Source: want B, got %s", pong.Source)
	}
}

// --- AgentConnection: same EventID ---

func TestAgentConnectionSameEventID(t *testing.T) {
	p := gorapide.NewPoset()
	ifaceA := Interface("Sender").OutAction("Ping").Build()
	ifaceB := Interface("Observer").InAction("Ping").Build()
	compA := NewComponent("A", ifaceA, p)
	compB := NewComponent("B", ifaceB, p, WithBufferSize(4))

	conn := Connect("A", "B").
		On(pattern.MatchEvent("Ping")).
		Agent().
		Forward().
		Build()

	trigger, err := compA.Emit("Ping", map[string]any{"val": 42})
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}

	err = conn.Execute(trigger, compA, compB)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// Agent connection should have sent the SAME event to B's inbox.
	select {
	case received := <-compB.inbox:
		if received.ID != trigger.ID {
			t.Errorf("agent connection should deliver same EventID: want %s, got %s",
				trigger.ID, received.ID)
		}
		if received.ParamInt("val") != 42 {
			t.Errorf("params should be preserved: want 42, got %d", received.ParamInt("val"))
		}
	default:
		t.Fatal("agent connection should have sent event to target inbox")
	}
}

// --- Transform function ---

func TestPipeConnectionWithTransform(t *testing.T) {
	p := gorapide.NewPoset()
	ifaceA := Interface("Src").OutAction("Req").Build()
	ifaceB := Interface("Dst").InAction("Resp").Build()
	compA := NewComponent("A", ifaceA, p)
	compB := NewComponent("B", ifaceB, p)

	conn := Connect("A", "B").
		On(pattern.MatchEvent("Req")).
		Pipe().
		SendWith("Resp", func(e *gorapide.Event) map[string]any {
			return map[string]any{
				"echo":   e.ParamString("msg"),
				"status": 200,
			}
		}).
		Build()

	trigger, err := compA.Emit("Req", map[string]any{"msg": "hello"})
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}

	err = conn.Execute(trigger, compA, compB)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	resps := p.ByName("Resp")
	if len(resps) != 1 {
		t.Fatalf("expected 1 Resp event, got %d", len(resps))
	}
	resp := resps[0]

	if resp.ParamString("echo") != "hello" {
		t.Errorf("echo param: want hello, got %s", resp.ParamString("echo"))
	}
	if resp.ParamInt("status") != 200 {
		t.Errorf("status param: want 200, got %d", resp.ParamInt("status"))
	}

	// Pipe: should have causal edge.
	if !p.IsCausallyBefore(trigger.ID, resp.ID) {
		t.Error("pipe with transform should still create causal edge")
	}
}

// --- Lamport timestamp ordering through pipe chain ---

func TestPipeConnectionLamportOrdering(t *testing.T) {
	p := gorapide.NewPoset()
	ifaceA := Interface("A").OutAction("Step1").Build()
	ifaceB := Interface("B").InAction("Step1").OutAction("Step2").Build()
	compA := NewComponent("A", ifaceA, p)
	compB := NewComponent("B", ifaceB, p)

	conn := Connect("A", "B").
		On(pattern.MatchEvent("Step1")).
		Pipe().
		Send("Step2").
		Build()

	trigger, err := compA.Emit("Step1", nil)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}

	err = conn.Execute(trigger, compA, compB)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	step2s := p.ByName("Step2")
	if len(step2s) != 1 {
		t.Fatalf("expected 1 Step2 event, got %d", len(step2s))
	}

	if step2s[0].Clock.Lamport <= trigger.Clock.Lamport {
		t.Errorf("Lamport should increase: trigger=%d, effect=%d",
			trigger.Clock.Lamport, step2s[0].Clock.Lamport)
	}
}

// --- 3-component pipeline: A ->pipe-> B ->pipe-> C ---

func TestThreeComponentPipeline(t *testing.T) {
	p := gorapide.NewPoset()
	ifaceA := Interface("Producer").OutAction("Data").Build()
	ifaceB := Interface("Filter").InAction("Data").OutAction("Filtered").Build()
	ifaceC := Interface("Consumer").InAction("Filtered").Build()

	compA := NewComponent("A", ifaceA, p)
	compB := NewComponent("B", ifaceB, p)
	compC := NewComponent("C", ifaceC, p)

	connAB := Connect("A", "B").
		On(pattern.MatchEvent("Data")).
		Pipe().
		Send("Data").
		Build()

	connBC := Connect("B", "C").
		On(pattern.MatchEvent("Filtered")).
		Pipe().
		Send("Filtered").
		Build()

	// Step 1: A emits Data.
	dataEvent, err := compA.Emit("Data", map[string]any{"val": "raw"})
	if err != nil {
		t.Fatalf("Emit Data: %v", err)
	}

	// Step 2: Connection A->B fires, creating a Data event on B.
	err = connAB.Execute(dataEvent, compA, compB)
	if err != nil {
		t.Fatalf("Execute A->B: %v", err)
	}

	// Step 3: B processes and emits Filtered (simulating behavior).
	bData := p.ByName("Data")
	var bReceived *gorapide.Event
	for _, e := range bData {
		if e.Source == "B" {
			bReceived = e
			break
		}
	}
	if bReceived == nil {
		t.Fatal("B should have received a Data event via pipe")
	}

	filtered, err := compB.Emit("Filtered", map[string]any{"val": "clean"}, bReceived.ID)
	if err != nil {
		t.Fatalf("Emit Filtered: %v", err)
	}

	// Step 4: Connection B->C fires.
	err = connBC.Execute(filtered, compB, compC)
	if err != nil {
		t.Fatalf("Execute B->C: %v", err)
	}

	// Verify full causal chain exists: A's Data -> B's Data -> B's Filtered -> C's Filtered.
	cFiltered := p.ByName("Filtered")
	var cEvent *gorapide.Event
	for _, e := range cFiltered {
		if e.Source == "C" {
			cEvent = e
			break
		}
	}
	if cEvent == nil {
		t.Fatal("C should have a Filtered event")
	}

	if !p.IsCausallyBefore(dataEvent.ID, cEvent.ID) {
		t.Error("A's original Data should be causally before C's Filtered (transitive)")
	}

	// Verify Lamport timestamps are monotonically increasing through the chain.
	if dataEvent.Clock.Lamport >= bReceived.Clock.Lamport {
		t.Errorf("Lamport: A.Data(%d) should be < B.Data(%d)",
			dataEvent.Clock.Lamport, bReceived.Clock.Lamport)
	}
	if bReceived.Clock.Lamport >= filtered.Clock.Lamport {
		t.Errorf("Lamport: B.Data(%d) should be < B.Filtered(%d)",
			bReceived.Clock.Lamport, filtered.Clock.Lamport)
	}
	if filtered.Clock.Lamport >= cEvent.Clock.Lamport {
		t.Errorf("Lamport: B.Filtered(%d) should be < C.Filtered(%d)",
			filtered.Clock.Lamport, cEvent.Clock.Lamport)
	}
}

// --- BasicConnection also sends to inbox ---

func TestBasicConnectionSendsToInbox(t *testing.T) {
	p := gorapide.NewPoset()
	ifaceA := Interface("Src").OutAction("Ping").Build()
	ifaceB := Interface("Dst").InAction("Pong").Build()
	compA := NewComponent("A", ifaceA, p, WithBufferSize(4))
	compB := NewComponent("B", ifaceB, p, WithBufferSize(4))

	var received []*gorapide.Event
	var mu sync.Mutex
	compB.OnReceive(func(comp *Component, e *gorapide.Event) {
		mu.Lock()
		defer mu.Unlock()
		received = append(received, e)
	})

	conn := Connect("A", "B").
		On(pattern.MatchEvent("Ping")).
		Send("Pong").
		Build()

	ctx := context.Background()
	compB.Start(ctx)

	trigger, _ := compA.Emit("Ping", nil)
	conn.Execute(trigger, compA, compB)

	time.Sleep(50 * time.Millisecond)
	compB.Stop()
	compB.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(received) != 1 {
		t.Fatalf("expected 1 received event, got %d", len(received))
	}
	if received[0].Name != "Pong" {
		t.Errorf("received event name: want Pong, got %s", received[0].Name)
	}
}

// --- PipeConnection also sends to inbox ---

func TestPipeConnectionSendsToInbox(t *testing.T) {
	p := gorapide.NewPoset()
	ifaceA := Interface("Src").OutAction("X").Build()
	ifaceB := Interface("Dst").InAction("Y").Build()
	compA := NewComponent("A", ifaceA, p, WithBufferSize(4))
	compB := NewComponent("B", ifaceB, p, WithBufferSize(4))

	var received []*gorapide.Event
	var mu sync.Mutex
	compB.OnReceive(func(comp *Component, e *gorapide.Event) {
		mu.Lock()
		defer mu.Unlock()
		received = append(received, e)
	})

	conn := Connect("A", "B").
		On(pattern.MatchEvent("X")).
		Pipe().
		Send("Y").
		Build()

	ctx := context.Background()
	compB.Start(ctx)

	trigger, _ := compA.Emit("X", nil)
	conn.Execute(trigger, compA, compB)

	time.Sleep(50 * time.Millisecond)
	compB.Stop()
	compB.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(received) != 1 {
		t.Fatalf("expected 1 received event, got %d", len(received))
	}
	if received[0].Name != "Y" {
		t.Errorf("received event name: want Y, got %s", received[0].Name)
	}
}

// --- Connection String ---

func TestConnectionString(t *testing.T) {
	conn := Connect("A", "B").
		On(pattern.MatchEvent("Ping")).
		Pipe().
		Send("Pong").
		Build()

	s := conn.String()
	if len(s) == 0 {
		t.Error("String() should not be empty")
	}
	if !containsStr(s, "A") || !containsStr(s, "B") {
		t.Errorf("String() should contain component IDs, got %s", s)
	}
	if !containsStr(s, "Pipe") {
		t.Errorf("String() should contain connection kind, got %s", s)
	}
}

// --- Wildcard connection ---

func TestConnectWildcard(t *testing.T) {
	conn := Connect("*", "B").
		On(pattern.MatchAny()).
		Send("Notify").
		Build()

	if conn.From != "*" {
		t.Errorf("From: want *, got %s", conn.From)
	}
}

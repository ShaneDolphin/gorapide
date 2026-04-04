package gorapide

import (
	"strings"
	"testing"
	"time"
)

func TestNewEventGeneratesUniqueIDs(t *testing.T) {
	const count = 1000
	seen := make(map[EventID]bool, count)
	for i := 0; i < count; i++ {
		e := NewEvent("Test", "src", nil)
		if seen[e.ID] {
			t.Fatalf("duplicate EventID detected: %s (at iteration %d)", e.ID, i)
		}
		seen[e.ID] = true
	}
}

func TestNewEventSetsWallTime(t *testing.T) {
	before := time.Now()
	e := NewEvent("Ping", "sensor", nil)
	after := time.Now()

	if e.Clock.WallTime.Before(before) {
		t.Errorf("WallTime %v is before test start %v", e.Clock.WallTime, before)
	}
	if e.Clock.WallTime.After(after) {
		t.Errorf("WallTime %v is after test end %v", e.Clock.WallTime, after)
	}
}

func TestNewEventLamportStartsAtZero(t *testing.T) {
	e := NewEvent("Init", "core", nil)
	if e.Clock.Lamport != 0 {
		t.Errorf("expected Lamport=0, got %d", e.Clock.Lamport)
	}
}

func TestNewEventCopiesParams(t *testing.T) {
	params := map[string]any{"key": "value"}
	e := NewEvent("Test", "src", params)
	params["key"] = "modified"
	if e.ParamString("key") != "value" {
		t.Error("NewEvent did not defensively copy params")
	}
}

func TestNewEventWithVariousParamTypes(t *testing.T) {
	params := map[string]any{
		"name":    "vuln-scan",
		"count":   42,
		"ratio":   3.14,
		"enabled": true,
		"tags":    []string{"critical", "network"},
	}
	e := NewEvent("ScanComplete", "scanner-1", params)

	if e.Name != "ScanComplete" {
		t.Errorf("expected Name=ScanComplete, got %s", e.Name)
	}
	if e.Source != "scanner-1" {
		t.Errorf("expected Source=scanner-1, got %s", e.Source)
	}
	if e.ParamString("name") != "vuln-scan" {
		t.Errorf("expected name=vuln-scan, got %s", e.ParamString("name"))
	}
	if e.ParamInt("count") != 42 {
		t.Errorf("expected count=42, got %d", e.ParamInt("count"))
	}

	v, ok := e.Param("ratio")
	if !ok || v.(float64) != 3.14 {
		t.Errorf("expected ratio=3.14, got %v", v)
	}
	v, ok = e.Param("enabled")
	if !ok || v.(bool) != true {
		t.Errorf("expected enabled=true, got %v", v)
	}
}

func TestParamAccessorsMissingKeys(t *testing.T) {
	e := NewEvent("Empty", "src", nil)

	v, ok := e.Param("nonexistent")
	if ok {
		t.Error("expected ok=false for missing key")
	}
	if v != nil {
		t.Errorf("expected nil for missing key, got %v", v)
	}
	if e.ParamString("missing") != "" {
		t.Errorf("expected empty string for missing key, got %q", e.ParamString("missing"))
	}
	if e.ParamInt("missing") != 0 {
		t.Errorf("expected 0 for missing key, got %d", e.ParamInt("missing"))
	}
}

func TestParamAccessorsWrongType(t *testing.T) {
	params := map[string]any{
		"number": 42,
		"text":   "hello",
	}
	e := NewEvent("TypeTest", "src", params)

	if e.ParamString("number") != "" {
		t.Error("ParamString should return empty for non-string value")
	}
	if e.ParamInt("text") != 0 {
		t.Error("ParamInt should return 0 for non-int value")
	}
}

func TestEventString(t *testing.T) {
	params := map[string]any{
		"severity": "high",
		"port":     443,
	}
	e := NewEvent("VulnFound", "scanner", params)
	s := e.String()

	if !strings.HasPrefix(s, "VulnFound(") {
		t.Errorf("String() should start with event name, got %q", s)
	}
	if !strings.Contains(s, "@scanner") {
		t.Errorf("String() should contain @source, got %q", s)
	}
	if !strings.Contains(s, "port=443") {
		t.Errorf("String() should contain params, got %q", s)
	}
	if !strings.Contains(s, "severity=high") {
		t.Errorf("String() should contain params, got %q", s)
	}
	if !strings.Contains(s, "[id:") {
		t.Errorf("String() should contain [id:...], got %q", s)
	}
}

func TestEventStringNoParams(t *testing.T) {
	e := NewEvent("Heartbeat", "monitor", nil)
	s := e.String()
	if !strings.Contains(s, "Heartbeat()") {
		t.Errorf("expected Heartbeat() in output, got %q", s)
	}
}

func TestFreeze(t *testing.T) {
	e := NewEvent("Test", "src", map[string]any{"a": 1})
	if e.Immutable {
		t.Error("event should not be immutable before Freeze")
	}

	e.Freeze()

	if !e.Immutable {
		t.Error("event should be immutable after Freeze")
	}
	// Params should still be accessible after freeze.
	if e.ParamInt("a") != 1 {
		t.Error("params should be readable after Freeze")
	}
}

func TestClockStampOrdering(t *testing.T) {
	c1 := ClockStamp{Lamport: 1, WallTime: time.Now()}
	c2 := ClockStamp{Lamport: 2, WallTime: time.Now()}
	c3 := ClockStamp{Lamport: 1, WallTime: time.Now()}

	if !c1.Before(c2) {
		t.Error("c1 (Lamport=1) should be before c2 (Lamport=2)")
	}
	if c2.Before(c1) {
		t.Error("c2 (Lamport=2) should not be before c1 (Lamport=1)")
	}
	if c1.Before(c3) {
		t.Error("equal Lamport values should not satisfy Before")
	}
}

func TestEventSetContains(t *testing.T) {
	e1 := NewEvent("A", "src", nil)
	e2 := NewEvent("B", "src", nil)
	e3 := NewEvent("C", "src", nil)
	set := EventSet{e1, e2}

	if !set.Contains(e1.ID) {
		t.Error("set should contain e1")
	}
	if !set.Contains(e2.ID) {
		t.Error("set should contain e2")
	}
	if set.Contains(e3.ID) {
		t.Error("set should not contain e3")
	}
}

func TestEventSetIDs(t *testing.T) {
	e1 := NewEvent("A", "src", nil)
	e2 := NewEvent("B", "src", nil)
	set := EventSet{e1, e2}
	ids := set.IDs()

	if len(ids) != 2 {
		t.Fatalf("expected 2 IDs, got %d", len(ids))
	}
	if ids[0] != e1.ID || ids[1] != e2.ID {
		t.Error("IDs do not match expected order")
	}
}

func TestEventSetFilter(t *testing.T) {
	events := EventSet{
		NewEvent("ScanComplete", "scanner", map[string]any{"severity": "high"}),
		NewEvent("ScanComplete", "scanner", map[string]any{"severity": "low"}),
		NewEvent("VulnFound", "scanner", map[string]any{"severity": "high"}),
		NewEvent("Heartbeat", "monitor", nil),
	}

	// Filter by name.
	scans := events.Filter(func(e *Event) bool {
		return e.Name == "ScanComplete"
	})
	if len(scans) != 2 {
		t.Errorf("expected 2 ScanComplete events, got %d", len(scans))
	}

	// Filter by param.
	high := events.Filter(func(e *Event) bool {
		return e.ParamString("severity") == "high"
	})
	if len(high) != 2 {
		t.Errorf("expected 2 high-severity events, got %d", len(high))
	}

	// Filter returns empty set when nothing matches.
	none := events.Filter(func(e *Event) bool {
		return e.Name == "NonExistent"
	})
	if len(none) != 0 {
		t.Errorf("expected 0 events, got %d", len(none))
	}
}

func TestEventSetNames(t *testing.T) {
	events := EventSet{
		NewEvent("Alpha", "src", nil),
		NewEvent("Beta", "src", nil),
		NewEvent("Alpha", "src", nil),
	}
	names := events.Names()
	if len(names) != 3 {
		t.Fatalf("expected 3 names, got %d", len(names))
	}
	if names[0] != "Alpha" || names[1] != "Beta" || names[2] != "Alpha" {
		t.Errorf("unexpected names: %v", names)
	}
}

func TestEventIDShort(t *testing.T) {
	id := NewEventID()
	short := id.Short()
	if len(short) != 8 {
		t.Errorf("expected Short() length of 8, got %d (%q)", len(short), short)
	}
}

func TestEventSetEmpty(t *testing.T) {
	var set EventSet
	if set.Contains(EventID("nonexistent")) {
		t.Error("empty set should not contain anything")
	}
	if len(set.IDs()) != 0 {
		t.Error("empty set IDs should be empty")
	}
	if len(set.Names()) != 0 {
		t.Error("empty set Names should be empty")
	}
	filtered := set.Filter(func(e *Event) bool { return true })
	if len(filtered) != 0 {
		t.Error("filtering empty set should return empty")
	}
}

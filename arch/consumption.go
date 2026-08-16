package arch

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"sync"

	"github.com/ShaneDolphin/gorapide"
)

const RuleConsumptionFormat = "gorapide.rule-consumption.v1"

var (
	ErrInvalidRuleConsumption = errors.New("invalid rule-consumption state")
	ErrEventConsumedByRule    = errors.New("event was already consumed by this rule")
)

// RuleConsumption tracks event availability independently for each Rapide
// rule. Consuming an event for one rule never removes it from another rule's
// pool.
type RuleConsumption struct {
	mu     sync.RWMutex
	byRule map[string]map[gorapide.EventID]bool
}

type CanonicalRuleConsumption struct {
	Format string                    `json:"format"`
	Rules  []CanonicalRuleEventUsage `json:"rules"`
}

type CanonicalRuleEventUsage struct {
	RuleID string   `json:"rule_id"`
	Events []string `json:"events"`
}

func NewRuleConsumption() *RuleConsumption {
	return &RuleConsumption{byRule: make(map[string]map[gorapide.EventID]bool)}
}

// Available returns stable defensive snapshots not yet consumed by ruleID.
func (state *RuleConsumption) Available(ruleID string, events gorapide.EventSet) (gorapide.EventSet, error) {
	if state == nil || ruleID == "" {
		return nil, fmt.Errorf("%w: state is nil or rule ID is empty", ErrInvalidRuleConsumption)
	}
	state.mu.RLock()
	consumed := state.byRule[ruleID]
	state.mu.RUnlock()
	result := make(gorapide.EventSet, 0, len(events))
	seen := make(map[string]bool, len(events))
	for index, event := range events {
		if event == nil || event.ID == "" {
			return nil, fmt.Errorf("%w: event %d is nil or unidentified", ErrInvalidRuleConsumption, index)
		}
		viewKey := fmt.Sprintf("%d:%s%d:%s%d:%s", len(event.ID), event.ID, len(event.Source), event.Source, len(event.Name), event.Name)
		if seen[viewKey] {
			continue
		}
		seen[viewKey] = true
		if !consumed[event.ID] {
			result = append(result, event.Snapshot())
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].ID != result[j].ID {
			return result[i].ID < result[j].ID
		}
		if result[i].Source != result[j].Source {
			return result[i].Source < result[j].Source
		}
		return result[i].Name < result[j].Name
	})
	return result, nil
}

// Consume atomically marks every event in match unavailable to ruleID. It
// rejects partial updates when any event was already consumed by that rule.
func (state *RuleConsumption) Consume(ruleID string, match gorapide.EventSet) error {
	if state == nil || ruleID == "" {
		return fmt.Errorf("%w: state is nil or rule ID is empty", ErrInvalidRuleConsumption)
	}
	ids := make([]gorapide.EventID, 0, len(match))
	seen := make(map[gorapide.EventID]bool, len(match))
	for index, event := range match {
		if event == nil || event.ID == "" {
			return fmt.Errorf("%w: match event %d is nil or unidentified", ErrInvalidRuleConsumption, index)
		}
		if seen[event.ID] {
			return fmt.Errorf("%w: duplicate event %s in match", ErrInvalidRuleConsumption, event.ID)
		}
		seen[event.ID] = true
		ids = append(ids, event.ID)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	state.mu.Lock()
	defer state.mu.Unlock()
	if state.byRule == nil {
		state.byRule = make(map[string]map[gorapide.EventID]bool)
	}
	consumed := state.byRule[ruleID]
	if consumed == nil {
		consumed = make(map[gorapide.EventID]bool)
	}
	for _, id := range ids {
		if consumed[id] {
			return fmt.Errorf("%w: rule=%s event=%s", ErrEventConsumedByRule, ruleID, id)
		}
	}
	for _, id := range ids {
		consumed[id] = true
	}
	state.byRule[ruleID] = consumed
	return nil
}

func (state *RuleConsumption) IsConsumed(ruleID string, eventID gorapide.EventID) bool {
	if state == nil {
		return false
	}
	state.mu.RLock()
	defer state.mu.RUnlock()
	return state.byRule[ruleID][eventID]
}

func (state *RuleConsumption) Canonical() (CanonicalRuleConsumption, error) {
	if state == nil {
		return CanonicalRuleConsumption{}, fmt.Errorf("%w: state is nil", ErrInvalidRuleConsumption)
	}
	state.mu.RLock()
	defer state.mu.RUnlock()
	result := CanonicalRuleConsumption{Format: RuleConsumptionFormat, Rules: make([]CanonicalRuleEventUsage, 0, len(state.byRule))}
	ruleIDs := make([]string, 0, len(state.byRule))
	for ruleID := range state.byRule {
		if ruleID == "" {
			return CanonicalRuleConsumption{}, fmt.Errorf("%w: empty rule ID", ErrInvalidRuleConsumption)
		}
		ruleIDs = append(ruleIDs, ruleID)
	}
	sort.Strings(ruleIDs)
	for _, ruleID := range ruleIDs {
		events := make([]string, 0, len(state.byRule[ruleID]))
		for eventID := range state.byRule[ruleID] {
			if eventID == "" {
				return CanonicalRuleConsumption{}, fmt.Errorf("%w: rule %q has empty event ID", ErrInvalidRuleConsumption, ruleID)
			}
			events = append(events, string(eventID))
		}
		sort.Strings(events)
		result.Rules = append(result.Rules, CanonicalRuleEventUsage{RuleID: ruleID, Events: events})
	}
	return result, nil
}

func (state *RuleConsumption) MarshalCanonical() ([]byte, error) {
	canonical, err := state.Canonical()
	if err != nil {
		return nil, err
	}
	return json.Marshal(canonical)
}

func (state *RuleConsumption) SemanticDigest() (string, error) {
	encoded, err := state.MarshalCanonical()
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func ParseRuleConsumption(data []byte) (*RuleConsumption, error) {
	var canonical CanonicalRuleConsumption
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&canonical); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidRuleConsumption, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("%w: trailing JSON content", ErrInvalidRuleConsumption)
	}
	if canonical.Format != RuleConsumptionFormat {
		return nil, fmt.Errorf("%w: format %q", ErrInvalidRuleConsumption, canonical.Format)
	}
	state := NewRuleConsumption()
	previousRule := ""
	for i, rule := range canonical.Rules {
		if rule.RuleID == "" || (i > 0 && rule.RuleID <= previousRule) {
			return nil, fmt.Errorf("%w: rule IDs are empty, duplicate, or unordered", ErrInvalidRuleConsumption)
		}
		previousEvent := ""
		for j, eventID := range rule.Events {
			if eventID == "" || (j > 0 && eventID <= previousEvent) {
				return nil, fmt.Errorf("%w: events for rule %q are empty, duplicate, or unordered", ErrInvalidRuleConsumption, rule.RuleID)
			}
			if err := state.Consume(rule.RuleID, gorapide.EventSet{{ID: gorapide.EventID(eventID)}}); err != nil {
				return nil, err
			}
			previousEvent = eventID
		}
		previousRule = rule.RuleID
	}
	reencoded, err := state.MarshalCanonical()
	if err != nil || !bytes.Equal(reencoded, data) {
		return nil, fmt.Errorf("%w: input is not the canonical byte encoding", ErrInvalidRuleConsumption)
	}
	return state, nil
}

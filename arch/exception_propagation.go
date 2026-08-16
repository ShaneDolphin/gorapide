package arch

import (
	"fmt"
	"sort"

	"github.com/ShaneDolphin/gorapide"
)

const (
	exceptionParentRelation = "parent"
	exceptionLinkedRelation = "linked"

	exceptionDeliveredDisposition          = "delivered"
	exceptionHandledDisposition            = "handled"
	exceptionHandlerRaisedDisposition      = "handler-raised"
	exceptionEscapedEnvironmentDisposition = "escaped-environment"
	exceptionIgnoredTerminatedDisposition  = "ignored-already-terminated"
	exceptionIgnoredFinalizedDisposition   = "ignored-finalized"
)

// ExceptionPropagationTargetRecord is one canonical destination of a
// propagated exception occurrence. Relations is a set because the same module
// can be both the parent and an explicit Context destination. Disposition
// distinguishes delivery from the published already-terminated-parent rule.
type ExceptionPropagationTargetRecord struct {
	ModuleID    string   `json:"module_id"`
	ComponentID string   `json:"component_id,omitempty"`
	Relations   []string `json:"relations"`
	Disposition string   `json:"disposition"`
}

// ExceptionPropagationRecord proves one module-level broadcast of one
// exception occurrence. Propagation never creates a second event: every source
// and target refers to ExceptionEventID in the execution poset.
type ExceptionPropagationRecord struct {
	ExceptionEventID     string                             `json:"exception_event_id"`
	Exception            string                             `json:"exception"`
	ExceptionDeclaration string                             `json:"exception_declaration"`
	SourceModuleID       string                             `json:"source_module_id"`
	SourceComponentID    string                             `json:"source_component_id,omitempty"`
	Targets              []ExceptionPropagationTargetRecord `json:"targets"`
}

type exceptionPropagationTarget struct {
	moduleID  string
	relations []string
}

type exceptionPropagationRuntime struct {
	recordsByKey map[string]ExceptionPropagationRecord
}

func newExceptionPropagationRuntime() *exceptionPropagationRuntime {
	return &exceptionPropagationRuntime{recordsByKey: make(map[string]ExceptionPropagationRecord)}
}

func exceptionPropagationKey(eventID gorapide.EventID, sourceModuleID string) string {
	return string(eventID) + "\x00" + sourceModuleID
}

func (runtime *exceptionPropagationRuntime) has(eventID gorapide.EventID, sourceModuleID string) bool {
	if runtime == nil {
		return false
	}
	_, exists := runtime.recordsByKey[exceptionPropagationKey(eventID, sourceModuleID)]
	return exists
}

func (runtime *exceptionPropagationRuntime) add(record ExceptionPropagationRecord) error {
	if runtime == nil {
		return fmt.Errorf("exception propagation runtime is unavailable")
	}
	if record.ExceptionEventID == "" || record.Exception == "" || record.SourceModuleID == "" {
		return fmt.Errorf("exception propagation record is incomplete")
	}
	key := record.ExceptionEventID + "\x00" + record.SourceModuleID
	if _, exists := runtime.recordsByKey[key]; exists {
		return fmt.Errorf("exception propagation record %q is repeated", key)
	}
	copyRecord := record
	copyRecord.Targets = make([]ExceptionPropagationTargetRecord, len(record.Targets))
	for index, target := range record.Targets {
		copyRecord.Targets[index] = target
		copyRecord.Targets[index].Relations = append([]string(nil), target.Relations...)
	}
	runtime.recordsByKey[key] = copyRecord
	return nil
}

func (runtime *exceptionPropagationRuntime) setTargetDisposition(
	eventID gorapide.EventID,
	sourceModuleID, targetModuleID, disposition string,
) error {
	if runtime == nil {
		return fmt.Errorf("exception propagation runtime is unavailable")
	}
	key := exceptionPropagationKey(eventID, sourceModuleID)
	record, exists := runtime.recordsByKey[key]
	if !exists {
		return fmt.Errorf("exception propagation record %q is unavailable", key)
	}
	for index := range record.Targets {
		if record.Targets[index].ModuleID == targetModuleID {
			record.Targets[index].Disposition = disposition
			runtime.recordsByKey[key] = record
			return nil
		}
	}
	return fmt.Errorf("exception propagation target %q is unavailable in %q", targetModuleID, key)
}

// handledParentTarget reports whether the exact exception occurrence reached
// targetModuleID from its structural child and was selected by the target's
// module handler. The parent relation matters here: linked delivery may be an
// independent propagation branch and cannot by itself resolve a suspended
// nested generator-call chain.
func (runtime *exceptionPropagationRuntime) handledParentTarget(
	eventID gorapide.EventID,
	sourceModuleID, targetModuleID string,
) bool {
	disposition, exists := runtime.parentTargetDisposition(eventID, sourceModuleID, targetModuleID)
	return exists && disposition == exceptionHandledDisposition
}

func (runtime *exceptionPropagationRuntime) parentTargetDisposition(
	eventID gorapide.EventID,
	sourceModuleID, targetModuleID string,
) (string, bool) {
	if runtime == nil || eventID == "" || sourceModuleID == "" || targetModuleID == "" {
		return "", false
	}
	record, exists := runtime.recordsByKey[exceptionPropagationKey(eventID, sourceModuleID)]
	if !exists {
		return "", false
	}
	for _, target := range record.Targets {
		if target.ModuleID != targetModuleID {
			continue
		}
		for _, relation := range target.Relations {
			if relation == exceptionParentRelation {
				return target.Disposition, true
			}
		}
	}
	return "", false
}

func (runtime *exceptionPropagationRuntime) records() []ExceptionPropagationRecord {
	if runtime == nil {
		return []ExceptionPropagationRecord{}
	}
	keys := make([]string, 0, len(runtime.recordsByKey))
	for key := range runtime.recordsByKey {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]ExceptionPropagationRecord, 0, len(keys))
	for _, key := range keys {
		record := runtime.recordsByKey[key]
		record.Targets = append([]ExceptionPropagationTargetRecord(nil), record.Targets...)
		for index := range record.Targets {
			record.Targets[index].Relations = append([]string(nil), record.Targets[index].Relations...)
		}
		result = append(result, record)
	}
	return result
}

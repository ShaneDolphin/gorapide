package arch

import (
	"fmt"
	"sort"

	"github.com/ShaneDolphin/gorapide"
)

// CommunicationContextRecord is one interval during which Source belongs to
// Destination's Rapide communication Context. Initial parent membership and
// explicit Link intervals are distinct even though Context itself is a set.
type CommunicationContextRecord struct {
	EdgeID        string   `json:"edge_id"`
	Source        string   `json:"source_module"`
	Destination   string   `json:"destination_module"`
	Kind          string   `json:"kind"`
	Live          bool     `json:"live"`
	AcquiredAfter []string `json:"acquired_after"`
	LostAfter     []string `json:"lost_after"`
}

type communicationContextEdge struct {
	edgeID        string
	source        string
	destination   string
	kind          string
	live          bool
	acquiredAfter []gorapide.EventID
	lostAfter     []gorapide.EventID
	nameID        string
}

type communicationContextRuntime struct {
	edges             map[string]*communicationContextEdge
	componentByModule map[string]string
	moduleByComponent map[string]string
	lifecycle         *moduleLifecycleRegistry
}

func newCommunicationContextRuntime(
	lifecycle *moduleLifecycleRegistry,
	modules map[string]gorapide.RapideModuleValue,
) (*communicationContextRuntime, error) {
	if lifecycle == nil {
		return nil, fmt.Errorf("communication Context has no module lifecycle registry")
	}
	runtime := &communicationContextRuntime{
		edges:             make(map[string]*communicationContextEdge),
		componentByModule: make(map[string]string, len(modules)),
		moduleByComponent: make(map[string]string, len(modules)),
		lifecycle:         lifecycle,
	}
	for component, module := range modules {
		if module.Identity() != "" {
			runtime.componentByModule[module.Identity()] = component
			runtime.moduleByComponent[component] = module.Identity()
		}
	}
	moduleIDs := make([]string, 0, len(lifecycle.modules))
	for moduleID := range lifecycle.modules {
		moduleIDs = append(moduleIDs, moduleID)
	}
	sort.Strings(moduleIDs)
	for _, moduleID := range moduleIDs {
		module := lifecycle.modules[moduleID]
		if module.parent == "" {
			continue
		}
		edgeID := "initial-context:" + module.parent + "/" + moduleID
		runtime.edges[edgeID] = &communicationContextEdge{
			edgeID: edgeID, source: moduleID, destination: module.parent,
			kind: "initial-parent", live: true,
			acquiredAfter: []gorapide.EventID{module.startEventID},
		}
	}
	return runtime, nil
}

// addInitialModule records the published rule that every created module begins
// in its parent's Context. Static modules are installed by the constructor;
// executable generators call this method immediately after lifecycle
// registration so allocation order cannot be recovered from host iteration.
func (runtime *communicationContextRuntime) addInitialModule(
	moduleID, parentID string,
	start gorapide.EventID,
) error {
	if runtime == nil || runtime.lifecycle == nil {
		return fmt.Errorf("communication Context runtime is unavailable")
	}
	if runtime.lifecycle.modules[moduleID] == nil {
		return fmt.Errorf("initial communication Context (%q,%q) references an unavailable module", moduleID, parentID)
	}
	if runtime.lifecycle.modules[parentID] == nil {
		// Legacy Go-built components have a stable component identifier but no
		// Rapide module value. They therefore have no language-level Context to
		// fabricate; their deterministic behavior remains outside this record.
		return nil
	}
	if start == "" {
		return fmt.Errorf("initial communication Context for %q has no Start frontier", moduleID)
	}
	edgeID := "initial-context:" + parentID + "/" + moduleID
	if runtime.edges[edgeID] != nil {
		return fmt.Errorf("initial communication Context edge %q is already registered", edgeID)
	}
	// Dynamically allocated modules use their allocation identity as their
	// execution component identity. Retain that bidirectional lookup after
	// finalization as well: delayed observation and replay still have to resolve
	// the event's original broadcaster exactly.
	if runtime.componentByModule[moduleID] == "" {
		runtime.componentByModule[moduleID] = moduleID
	}
	if runtime.moduleByComponent[moduleID] == "" {
		runtime.moduleByComponent[moduleID] = moduleID
	}
	runtime.edges[edgeID] = &communicationContextEdge{
		edgeID: edgeID, source: moduleID, destination: parentID,
		kind: "initial-parent", live: true,
		acquiredAfter: []gorapide.EventID{start},
	}
	return nil
}

// closeFinalizedModule removes a finalized module from every communication
// Context and closes the finalized module's own Context. The supplied frontier
// is the exact Finish-enabling loss frontier, not a host cleanup timestamp.
func (runtime *communicationContextRuntime) closeFinalizedModule(
	moduleID string,
	after []gorapide.EventID,
) error {
	if runtime == nil {
		return fmt.Errorf("communication Context runtime is unavailable")
	}
	after = canonicalEventIDs(after)
	if moduleID == "" || len(after) == 0 {
		return fmt.Errorf("finalized communication Context module %q has no loss frontier", moduleID)
	}
	ids := make([]string, 0)
	for edgeID, edge := range runtime.edges {
		if edge.live && (edge.source == moduleID || edge.destination == moduleID) {
			ids = append(ids, edgeID)
		}
	}
	sort.Strings(ids)
	for _, edgeID := range ids {
		edge := runtime.edges[edgeID]
		edge.live = false
		edge.lostAfter = append([]gorapide.EventID(nil), after...)
	}
	return nil
}

func (runtime *communicationContextRuntime) liveEdge(source, destination string) *communicationContextEdge {
	if runtime == nil {
		return nil
	}
	ids := make([]string, 0)
	for id, edge := range runtime.edges {
		if edge.live && edge.source == source && edge.destination == destination {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	sort.Strings(ids)
	return runtime.edges[ids[0]]
}

func (runtime *communicationContextRuntime) link(
	edgeID, source, destination string,
	after []gorapide.EventID,
) error {
	if runtime == nil || runtime.lifecycle == nil {
		return fmt.Errorf("communication Context runtime is unavailable")
	}
	if runtime.lifecycle.modules[source] == nil || runtime.lifecycle.modules[destination] == nil {
		return fmt.Errorf("communication Context Link(%q,%q) references an unavailable module", source, destination)
	}
	if runtime.liveEdge(source, destination) != nil {
		return nil
	}
	if edgeID == "" || runtime.edges[edgeID] != nil {
		return fmt.Errorf("communication Context edge %q is empty or repeated", edgeID)
	}
	after = canonicalEventIDs(after)
	if len(after) == 0 {
		return fmt.Errorf("communication Context edge %q has no acquisition frontier", edgeID)
	}
	nameID := "context-name:" + edgeID
	if err := runtime.lifecycle.addName(moduleNameRuntime{
		nameID: nameID, moduleID: source, owner: destination,
		name: "Context", kind: "context-link", acquiredAfter: after,
	}); err != nil {
		return err
	}
	runtime.edges[edgeID] = &communicationContextEdge{
		edgeID: edgeID, source: source, destination: destination,
		kind: "explicit-link", live: true, acquiredAfter: after, nameID: nameID,
	}
	return nil
}

// unlink removes the current set membership. It returns the lifecycle name
// owned by an explicit Link, if any; initial parent membership has no separate
// name because its constituent edge already carries nameability.
func (runtime *communicationContextRuntime) unlink(
	source, destination string,
	after []gorapide.EventID,
) (string, error) {
	if runtime == nil {
		return "", fmt.Errorf("communication Context runtime is unavailable")
	}
	edge := runtime.liveEdge(source, destination)
	if edge == nil {
		return "", nil
	}
	after = canonicalEventIDs(after)
	if len(after) == 0 {
		return "", fmt.Errorf("communication Context edge %q has no loss frontier", edge.edgeID)
	}
	edge.live = false
	edge.lostAfter = after
	return edge.nameID, nil
}

// recipientsAt returns the Context destinations that observe one exact source
// occurrence. Closed intervals are evaluated from their causal acquisition and
// loss frontiers rather than the host worklist's current mutation state. An
// event at the loss frontier is still inside the interval; an event caused by
// that frontier is not. Concurrent-to-loss delivery remains outside this
// bounded interval rule and is conservatively absent.
func (runtime *communicationContextRuntime) recipientsAt(
	sourceComponent string,
	eventID gorapide.EventID,
	poset *gorapide.Poset,
) []string {
	if runtime == nil || eventID == "" || poset == nil {
		return nil
	}
	module := runtime.moduleByComponent[sourceComponent]
	if module == "" {
		return nil
	}
	seen := make(map[string]bool)
	for _, edge := range runtime.edges {
		if edge.source != module || !contextEdgeContainsEvent(edge, eventID, poset) {
			continue
		}
		if component := runtime.componentByModule[edge.destination]; component != "" && component != sourceComponent {
			seen[component] = true
		}
	}
	result := make([]string, 0, len(seen))
	for component := range seen {
		result = append(result, component)
	}
	sort.Strings(result)
	return result
}

func contextEdgeContainsEvent(
	edge *communicationContextEdge,
	eventID gorapide.EventID,
	poset *gorapide.Poset,
) bool {
	if edge == nil || eventID == "" || poset == nil || len(edge.acquiredAfter) == 0 {
		return false
	}
	for _, acquired := range edge.acquiredAfter {
		if acquired == "" || acquired == eventID || !poset.IsCausallyBefore(acquired, eventID) {
			return false
		}
	}
	if edge.live {
		return true
	}
	for _, lost := range edge.lostAfter {
		if lost == eventID || poset.IsCausallyBefore(eventID, lost) {
			return true
		}
	}
	return false
}

// exceptionPropagationTargets returns the module's parent plus every live
// explicit Context destination. Parent propagation is structural and remains
// required even if an Unlink closed the initial Context edge. Duplicate paths
// are collapsed while retaining their canonical relation set.
func (runtime *communicationContextRuntime) exceptionPropagationTargets(sourceModule string) ([]exceptionPropagationTarget, error) {
	if runtime == nil || runtime.lifecycle == nil {
		return nil, fmt.Errorf("communication Context runtime is unavailable")
	}
	source := runtime.lifecycle.modules[sourceModule]
	if source == nil {
		return nil, fmt.Errorf("exception propagation source module %q is unavailable", sourceModule)
	}
	relationsByTarget := make(map[string]map[string]bool)
	add := func(target, relation string) {
		if target == "" {
			return
		}
		if relationsByTarget[target] == nil {
			relationsByTarget[target] = make(map[string]bool)
		}
		relationsByTarget[target][relation] = true
	}
	add(source.parent, exceptionParentRelation)
	for _, edge := range runtime.edges {
		if edge.live && edge.kind == "explicit-link" && edge.source == sourceModule {
			add(edge.destination, exceptionLinkedRelation)
		}
	}
	targetIDs := make([]string, 0, len(relationsByTarget))
	for targetID := range relationsByTarget {
		targetIDs = append(targetIDs, targetID)
	}
	sort.Strings(targetIDs)
	result := make([]exceptionPropagationTarget, 0, len(targetIDs))
	for _, targetID := range targetIDs {
		relations := make([]string, 0, len(relationsByTarget[targetID]))
		for relation := range relationsByTarget[targetID] {
			relations = append(relations, relation)
		}
		sort.Strings(relations)
		result = append(result, exceptionPropagationTarget{moduleID: targetID, relations: relations})
	}
	return result, nil
}

func (runtime *communicationContextRuntime) records() []CommunicationContextRecord {
	if runtime == nil {
		return []CommunicationContextRecord{}
	}
	ids := make([]string, 0, len(runtime.edges))
	for id := range runtime.edges {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	result := make([]CommunicationContextRecord, 0, len(ids))
	for _, id := range ids {
		edge := runtime.edges[id]
		result = append(result, CommunicationContextRecord{
			EdgeID: edge.edgeID, Source: edge.source, Destination: edge.destination,
			Kind: edge.kind, Live: edge.live,
			AcquiredAfter: eventIDStrings(edge.acquiredAfter), LostAfter: eventIDStrings(edge.lostAfter),
		})
	}
	return result
}

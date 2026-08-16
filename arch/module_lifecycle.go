package arch

import (
	"fmt"
	"sort"

	"github.com/ShaneDolphin/gorapide"
)

// ModuleLifecycleState is one of the four mutually exclusive module states in
// Executable LRM Section 9.9. Terminated means a propagated module exception;
// it is deliberately not inferred from a process reaching the end of its body.
type ModuleLifecycleState string

const (
	ModuleRunningState    ModuleLifecycleState = "running"
	ModuleCompletedState  ModuleLifecycleState = "completed"
	ModuleTerminatedState ModuleLifecycleState = "terminated"
	ModuleFinalizedState  ModuleLifecycleState = "finalized"
)

// ModuleFinishAction is the predefined system action implicitly called after a
// module's final part when that module becomes finalized.
const ModuleFinishAction = "Finish"

const moduleEnvironmentRoot = "$environment"

// ModuleNameRecord is one language-level name edge retained for lifecycle
// audit. AcquiredAfter and LostAfter are causal frontiers, not host timestamps.
type ModuleNameRecord struct {
	NameID        string   `json:"name_id"`
	Owner         string   `json:"owner"`
	Name          string   `json:"name"`
	Kind          string   `json:"kind"`
	Live          bool     `json:"live"`
	AcquiredAfter []string `json:"acquired_after"`
	LostAfter     []string `json:"lost_after"`
}

// ModuleLifecycleRecord is the canonical final lifecycle evidence for one
// module whose allocation/elaboration belongs to this execution.
type ModuleLifecycleRecord struct {
	ModuleID           string               `json:"module_id"`
	Kind               string               `json:"kind"`
	Parent             string               `json:"parent"`
	Generator          string               `json:"generator"`
	Occurrence         string               `json:"occurrence"`
	StartEventID       string               `json:"start_event_id"`
	FinishEventID      string               `json:"finish_event_id"`
	TerminationEventID string               `json:"termination_event_id,omitempty"`
	State              ModuleLifecycleState `json:"state"`
	Namable            bool                 `json:"namable"`
	Names              []ModuleNameRecord   `json:"names"`
}

type moduleLifecycleRuntime struct {
	moduleID           string
	kind               string
	parent             string
	generator          string
	occurrence         string
	startEventID       gorapide.EventID
	finishEventID      gorapide.EventID
	terminationEventID gorapide.EventID
	completedAfter     []gorapide.EventID
	state              ModuleLifecycleState
	initializing       bool
}

type moduleNameRuntime struct {
	nameID        string
	moduleID      string
	owner         string
	name          string
	kind          string
	live          bool
	acquiredAfter []gorapide.EventID
	lostAfter     []gorapide.EventID
}

type moduleFinalizationTransition struct {
	moduleID string
	causes   []gorapide.EventID
}

// moduleLifecycleRegistry is a deterministic language-level name graph. Root
// owners and running modules are graph roots; live names are directed edges
// from their owner to the module they denote. Go reachability and garbage
// collection never participate.
type moduleLifecycleRegistry struct {
	modules       map[string]*moduleLifecycleRuntime
	names         map[string]*moduleNameRuntime
	externalRoots map[string]bool
}

const scheduledActionNameOwner = "$scheduler"

func newModuleLifecycleRegistry() *moduleLifecycleRegistry {
	return &moduleLifecycleRegistry{
		modules:       make(map[string]*moduleLifecycleRuntime),
		names:         make(map[string]*moduleNameRuntime),
		externalRoots: make(map[string]bool),
	}
}

func (runtime *functionExecutionRuntime) moduleParent(componentID string) string {
	if runtime != nil && runtime.moduleParents != nil && runtime.moduleParents[componentID] != "" {
		return runtime.moduleParents[componentID]
	}
	return componentID
}

// executingModuleIdentity resolves the concrete module whose statement list is
// running. Static components are keyed by their architecture name while
// allocator-created modules are keyed directly by allocation identity; both
// must own module-generator calls evaluated in their executable parts.
func (runtime *functionExecutionRuntime) executingModuleIdentity(componentID string) string {
	if runtime != nil && runtime.modules != nil {
		if module := runtime.modules[componentID]; module.Identity() != "" {
			return module.Identity()
		}
	}
	return runtime.moduleParent(componentID)
}

func (registry *moduleLifecycleRegistry) addExternalRoot(owner string) error {
	if registry == nil || owner == "" {
		return fmt.Errorf("invalid module lifecycle root %q", owner)
	}
	registry.externalRoots[owner] = true
	return nil
}

func (registry *moduleLifecycleRegistry) addModule(runtime moduleLifecycleRuntime) error {
	if registry == nil {
		return fmt.Errorf("module lifecycle registry is nil")
	}
	if runtime.moduleID == "" || runtime.kind == "" || runtime.startEventID == "" {
		return fmt.Errorf("module lifecycle registration is incomplete for %q", runtime.moduleID)
	}
	if runtime.state != ModuleRunningState && runtime.state != ModuleCompletedState &&
		runtime.state != ModuleTerminatedState && runtime.state != ModuleFinalizedState {
		return fmt.Errorf("module %q has invalid lifecycle state %q", runtime.moduleID, runtime.state)
	}
	if _, exists := registry.modules[runtime.moduleID]; exists {
		return fmt.Errorf("module lifecycle %q is already registered", runtime.moduleID)
	}
	copyRuntime := runtime
	registry.modules[runtime.moduleID] = &copyRuntime
	if err := registry.addName(moduleNameRuntime{
		nameID:   "self-name:" + runtime.moduleID,
		moduleID: runtime.moduleID, owner: runtime.moduleID,
		name: "Self", kind: "implicit-self",
		acquiredAfter: []gorapide.EventID{runtime.startEventID},
	}); err != nil {
		delete(registry.modules, runtime.moduleID)
		return err
	}
	return nil
}

func (registry *moduleLifecycleRegistry) addName(name moduleNameRuntime) error {
	if registry == nil {
		return fmt.Errorf("module lifecycle registry is nil")
	}
	if name.nameID == "" || name.moduleID == "" || name.owner == "" || name.name == "" || name.kind == "" {
		return fmt.Errorf("module name registration is incomplete for %q", name.nameID)
	}
	if registry.modules[name.moduleID] == nil {
		return fmt.Errorf("module name %q denotes unavailable module %q", name.nameID, name.moduleID)
	}
	if _, exists := registry.names[name.nameID]; exists {
		return fmt.Errorf("module name %q is already registered", name.nameID)
	}
	name.live = true
	name.acquiredAfter = canonicalEventIDs(name.acquiredAfter)
	copyName := name
	registry.names[name.nameID] = &copyName
	return nil
}

// completeInitialization removes the temporary reachability root held while a
// fresh module's constituents and initial part execute. The root is semantic
// execution ownership, not a Rapide name and therefore does not enter the
// canonical name artifact. Names lexically owned by the fresh module remain
// reachable during initialization and are evaluated normally afterward.
func (registry *moduleLifecycleRegistry) completeInitialization(moduleID string) error {
	if registry == nil {
		return fmt.Errorf("module lifecycle registry is nil")
	}
	runtime := registry.modules[moduleID]
	if runtime == nil {
		return fmt.Errorf("module lifecycle %q is unavailable", moduleID)
	}
	if !runtime.initializing {
		return fmt.Errorf("module %q is not initializing", moduleID)
	}
	runtime.initializing = false
	return nil
}

func (registry *moduleLifecycleRegistry) setState(moduleID string, state ModuleLifecycleState) error {
	runtime := registry.modules[moduleID]
	if runtime == nil {
		return fmt.Errorf("module lifecycle %q is unavailable", moduleID)
	}
	if runtime.state == ModuleFinalizedState && state != ModuleFinalizedState {
		return fmt.Errorf("finalized module %q cannot return to %q", moduleID, state)
	}
	if state != ModuleRunningState && state != ModuleCompletedState && state != ModuleTerminatedState && state != ModuleFinalizedState {
		return fmt.Errorf("module %q has invalid lifecycle state %q", moduleID, state)
	}
	runtime.state = state
	return nil
}

func (registry *moduleLifecycleRegistry) terminate(moduleID string, exceptionEventID gorapide.EventID) error {
	runtime := registry.modules[moduleID]
	if runtime == nil {
		return fmt.Errorf("module lifecycle %q is unavailable", moduleID)
	}
	if exceptionEventID == "" {
		return fmt.Errorf("module %q termination has no exception occurrence", moduleID)
	}
	if runtime.state == ModuleFinalizedState {
		return fmt.Errorf("finalized module %q cannot terminate", moduleID)
	}
	if runtime.state == ModuleTerminatedState {
		if runtime.terminationEventID == exceptionEventID {
			return nil
		}
		return fmt.Errorf("module %q has conflicting termination occurrences", moduleID)
	}
	runtime.state = ModuleTerminatedState
	runtime.terminationEventID = exceptionEventID
	return nil
}

// finalizeInitializationFailure performs the exceptional creation transition
// required by Executable LRM Section 9.9. A module whose initialization
// propagates an exception was never returned, so every provisional name is
// lost at that occurrence and the module becomes finalized. The caller skips
// the user final part and materializes only the implicit Finish occurrence.
func (registry *moduleLifecycleRegistry) finalizeInitializationFailure(
	moduleID string,
	exceptionEventID gorapide.EventID,
) ([]gorapide.EventID, error) {
	if registry == nil {
		return nil, fmt.Errorf("module lifecycle registry is nil")
	}
	runtime := registry.modules[moduleID]
	if runtime == nil {
		return nil, fmt.Errorf("module lifecycle %q is unavailable", moduleID)
	}
	if exceptionEventID == "" || runtime.state != ModuleTerminatedState ||
		runtime.terminationEventID != exceptionEventID {
		return nil, fmt.Errorf("module %q has no matching initialization exception", moduleID)
	}
	for _, name := range registry.names {
		if name.moduleID != moduleID || !name.live {
			continue
		}
		name.live = false
		name.lostAfter = []gorapide.EventID{exceptionEventID}
	}
	runtime.initializing = false
	runtime.state = ModuleFinalizedState
	return []gorapide.EventID{exceptionEventID}, nil
}

// finalizeInitializationAbandonment closes a not-yet-returned module whose
// suspended generator call produced no value after an inner exception was
// handled. The module did not propagate an exception, so it remains on the
// ordinary finalization path. All names acquired provisionally for the pending
// generator result are lost atomically at the recovery frontier.
func (registry *moduleLifecycleRegistry) finalizeInitializationAbandonment(
	moduleID string,
	frontier []gorapide.EventID,
) ([]gorapide.EventID, error) {
	if registry == nil {
		return nil, fmt.Errorf("module lifecycle registry is nil")
	}
	runtime := registry.modules[moduleID]
	if runtime == nil {
		return nil, fmt.Errorf("module lifecycle %q is unavailable", moduleID)
	}
	frontier = canonicalEventIDs(frontier)
	if len(frontier) == 0 || runtime.state != ModuleCompletedState ||
		runtime.terminationEventID != "" {
		return nil, fmt.Errorf("module %q has no matching handled initialization abandonment", moduleID)
	}
	for _, name := range registry.names {
		if name.moduleID != moduleID || !name.live {
			continue
		}
		name.live = false
		name.lostAfter = append([]gorapide.EventID(nil), frontier...)
	}
	runtime.initializing = false
	runtime.state = ModuleFinalizedState
	if registry.namable()[moduleID] {
		return nil, fmt.Errorf("handled abandoned module %q remained namable", moduleID)
	}
	return frontier, nil
}

// releaseOwnedConstituentNames closes every live name in one disappearing
// architecture declaration scope at the same causal frontier. Completed and
// terminated constituents that thereby become unnamable transition to
// finalized atomically; the caller materializes their ordinary final parts and
// Finish occurrences. Running constituents lose the architecture name but
// remain running roots until their own process lifetime is closed.
func (registry *moduleLifecycleRegistry) releaseOwnedConstituentNames(
	ownerModuleID string,
	frontier []gorapide.EventID,
) ([]moduleFinalizationTransition, error) {
	if registry == nil {
		return nil, fmt.Errorf("module lifecycle registry is nil")
	}
	frontier = canonicalEventIDs(frontier)
	if ownerModuleID == "" || len(frontier) == 0 {
		return nil, fmt.Errorf("architecture constituent-name loss is incomplete")
	}
	if registry.modules[ownerModuleID] == nil {
		return nil, fmt.Errorf("architecture lifecycle %q is unavailable", ownerModuleID)
	}

	nameIDs := make([]string, 0)
	moduleSet := make(map[string]bool)
	for nameID, name := range registry.names {
		if !name.live || name.owner != ownerModuleID || name.moduleID == ownerModuleID {
			continue
		}
		target := registry.modules[name.moduleID]
		if target == nil {
			return nil, fmt.Errorf(
				"architecture constituent name %q denotes unavailable module %q",
				nameID, name.moduleID,
			)
		}
		if target.state == ModuleFinalizedState {
			return nil, fmt.Errorf(
				"finalized architecture constituent %q retained live name %q",
				name.moduleID, nameID,
			)
		}
		nameIDs = append(nameIDs, nameID)
		moduleSet[name.moduleID] = true
	}
	sort.Strings(nameIDs)
	for _, nameID := range nameIDs {
		name := registry.names[nameID]
		name.live = false
		name.lostAfter = append([]gorapide.EventID(nil), frontier...)
	}

	reachable := registry.namable()
	moduleIDs := make([]string, 0, len(moduleSet))
	for moduleID := range moduleSet {
		moduleIDs = append(moduleIDs, moduleID)
	}
	sort.Strings(moduleIDs)
	transitions := make([]moduleFinalizationTransition, 0, len(moduleIDs))
	for _, moduleID := range moduleIDs {
		target := registry.modules[moduleID]
		if target.state != ModuleCompletedState && target.state != ModuleTerminatedState {
			continue
		}
		if reachable[moduleID] {
			continue
		}
		causes := append([]gorapide.EventID(nil), target.completedAfter...)
		if target.terminationEventID != "" {
			causes = append(causes, target.terminationEventID)
		}
		for _, name := range registry.names {
			if name.moduleID == moduleID && !name.live {
				causes = append(causes, name.lostAfter...)
			}
		}
		target.state = ModuleFinalizedState
		transitions = append(transitions, moduleFinalizationTransition{
			moduleID: moduleID,
			causes:   canonicalEventIDs(causes),
		})
	}
	return transitions, nil
}

// releaseName records a semantic name loss and returns the newly finalized
// module, if any, plus the complete causal frontier of stored process
// completion, termination, and every recorded name loss. The caller
// materializes the final part and implicit Finish occurrence.
func (registry *moduleLifecycleRegistry) releaseName(
	nameID string,
	lostAfter []gorapide.EventID,
) (string, []gorapide.EventID, error) {
	if registry == nil {
		return "", nil, fmt.Errorf("module lifecycle registry is nil")
	}
	name := registry.names[nameID]
	if name == nil {
		return "", nil, fmt.Errorf("module name %q is unavailable", nameID)
	}
	if !name.live {
		return "", nil, fmt.Errorf("module name %q was already lost", nameID)
	}
	name.live = false
	name.lostAfter = canonicalEventIDs(lostAfter)
	runtime := registry.modules[name.moduleID]
	if runtime == nil {
		return "", nil, fmt.Errorf("module lifecycle %q is unavailable", name.moduleID)
	}
	if runtime.state != ModuleCompletedState && runtime.state != ModuleTerminatedState {
		return "", nil, nil
	}
	if registry.namable()[runtime.moduleID] {
		return "", nil, nil
	}
	losses := append([]gorapide.EventID(nil), runtime.completedAfter...)
	if runtime.terminationEventID != "" {
		losses = append(losses, runtime.terminationEventID)
	}
	for _, candidate := range registry.names {
		if candidate.moduleID == runtime.moduleID && !candidate.live {
			losses = append(losses, candidate.lostAfter...)
		}
	}
	runtime.state = ModuleFinalizedState
	return runtime.moduleID, canonicalEventIDs(losses), nil
}

// completeProcesses applies the other half of Rapide finalization eligibility.
// A running module becomes completed when all of its processes have completed;
// a module already terminated by a process exception retains that distinct
// state until finalization. If the module is now unnamable, finalization follows
// both the complete process frontier and every prior name-loss frontier. This is
// deliberately separate from Go object reachability and scheduler order.
func (registry *moduleLifecycleRegistry) completeProcesses(
	moduleID string,
	completedAfter []gorapide.EventID,
) (string, []gorapide.EventID, error) {
	if registry == nil {
		return "", nil, fmt.Errorf("module lifecycle registry is nil")
	}
	runtime := registry.modules[moduleID]
	if runtime == nil {
		return "", nil, fmt.Errorf("module lifecycle %q is unavailable", moduleID)
	}
	completedAfter = canonicalEventIDs(completedAfter)
	if len(completedAfter) == 0 {
		return "", nil, fmt.Errorf("module %q process completion has no causal frontier", moduleID)
	}
	runtime.completedAfter = canonicalEventIDs(append(runtime.completedAfter, completedAfter...))
	switch runtime.state {
	case ModuleRunningState:
		runtime.state = ModuleCompletedState
	case ModuleCompletedState, ModuleTerminatedState:
	case ModuleFinalizedState:
		return "", nil, nil
	default:
		return "", nil, fmt.Errorf("module %q has invalid process-completion state %q", moduleID, runtime.state)
	}
	if registry.namable()[moduleID] {
		return "", nil, nil
	}
	causes := append([]gorapide.EventID(nil), runtime.completedAfter...)
	if runtime.terminationEventID != "" {
		causes = append(causes, runtime.terminationEventID)
	}
	for _, name := range registry.names {
		if name.moduleID == moduleID && !name.live {
			causes = append(causes, name.lostAfter...)
		}
	}
	runtime.state = ModuleFinalizedState
	return moduleID, canonicalEventIDs(causes), nil
}

func (registry *moduleLifecycleRegistry) setFinish(moduleID string, eventID gorapide.EventID) error {
	runtime := registry.modules[moduleID]
	if runtime == nil || runtime.state != ModuleFinalizedState {
		return fmt.Errorf("module %q is not finalized", moduleID)
	}
	if eventID == "" || runtime.finishEventID != "" {
		return fmt.Errorf("module %q has invalid repeated Finish %q", moduleID, eventID)
	}
	runtime.finishEventID = eventID
	return nil
}

func (registry *moduleLifecycleRegistry) namable() map[string]bool {
	reachable := make(map[string]bool, len(registry.modules))
	owners := make([]string, 0, len(registry.externalRoots)+len(registry.modules))
	for owner := range registry.externalRoots {
		owners = append(owners, owner)
	}
	for moduleID, runtime := range registry.modules {
		if runtime.state == ModuleRunningState || runtime.initializing {
			owners = append(owners, moduleID)
		}
	}
	sort.Strings(owners)
	for len(owners) != 0 {
		owner := owners[0]
		owners = owners[1:]
		targets := make([]string, 0)
		for _, name := range registry.names {
			// Self is usable inside an executing module but cannot make that
			// module externally namable or prevent finalization by itself.
			if name.live && name.kind != "implicit-self" &&
				name.owner == owner && !reachable[name.moduleID] {
				targets = append(targets, name.moduleID)
			}
		}
		sort.Strings(targets)
		for _, target := range targets {
			if reachable[target] {
				continue
			}
			reachable[target] = true
			owners = append(owners, target)
		}
	}
	return reachable
}

func (registry *moduleLifecycleRegistry) records() ([]ModuleLifecycleRecord, error) {
	if registry == nil {
		return nil, fmt.Errorf("module lifecycle registry is nil")
	}
	reachable := registry.namable()
	moduleIDs := make([]string, 0, len(registry.modules))
	for moduleID := range registry.modules {
		moduleIDs = append(moduleIDs, moduleID)
	}
	sort.Strings(moduleIDs)
	result := make([]ModuleLifecycleRecord, 0, len(moduleIDs))
	for _, moduleID := range moduleIDs {
		runtime := registry.modules[moduleID]
		if runtime.state == ModuleFinalizedState && runtime.finishEventID == "" {
			return nil, fmt.Errorf("finalized module %q has no Finish occurrence", moduleID)
		}
		nameIDs := make([]string, 0)
		for nameID, name := range registry.names {
			if name.moduleID == moduleID {
				nameIDs = append(nameIDs, nameID)
			}
		}
		sort.Strings(nameIDs)
		names := make([]ModuleNameRecord, 0, len(nameIDs))
		for _, nameID := range nameIDs {
			name := registry.names[nameID]
			names = append(names, ModuleNameRecord{
				NameID: name.nameID, Owner: name.owner, Name: name.name, Kind: name.kind, Live: name.live,
				AcquiredAfter: eventIDStrings(name.acquiredAfter), LostAfter: eventIDStrings(name.lostAfter),
			})
		}
		result = append(result, ModuleLifecycleRecord{
			ModuleID: moduleID, Kind: runtime.kind, Parent: runtime.parent,
			Generator: runtime.generator, Occurrence: runtime.occurrence,
			StartEventID: string(runtime.startEventID), FinishEventID: string(runtime.finishEventID),
			TerminationEventID: string(runtime.terminationEventID),
			State:              runtime.state, Namable: reachable[moduleID], Names: names,
		})
	}
	return result, nil
}

func initializeStaticModuleLifecycles(
	model *deterministicModel,
	architectureModules map[string]gorapide.RapideModuleValue,
	architectureStarts map[string]*gorapide.Event,
	staticModules map[string]gorapide.RapideModuleValue,
	moduleStarts map[string]*gorapide.Event,
	recordObjects map[string]map[string]gorapide.RapideRecordValue,
	recordStarts []*gorapide.Event,
) (*moduleLifecycleRegistry, error) {
	registry := newModuleLifecycleRegistry()
	if err := registry.addExternalRoot(moduleEnvironmentRoot); err != nil {
		return nil, err
	}
	for _, instanceID := range append([]string{ArchitectureInterfaceID}, model.architectureInstanceIDs...) {
		start := architectureStarts[instanceID]
		if start == nil {
			return nil, fmt.Errorf("architecture lifecycle %q has no Start", instanceID)
		}
		parent := moduleEnvironmentRoot
		generator := model.name
		occurrence := "architecture:root"
		name := ArchitectureInterfaceID
		if instanceID != ArchitectureInterfaceID {
			declaration := model.architectureInstances[instanceID]
			parent = architectureModules[declaration.Parent].Identity()
			generator = declaration.Generator
			occurrence = "architecture-instance:" + instanceID
			name = deterministicArchitectureInstanceLocalIDOrSelf(instanceID)
		}
		moduleID := architectureModules[instanceID].Identity()
		if moduleID == "" || parent == "" {
			return nil, fmt.Errorf("architecture lifecycle %q has incomplete allocation identity", instanceID)
		}
		if err := registry.addModule(moduleLifecycleRuntime{
			moduleID: moduleID, kind: "architecture", parent: parent, generator: generator,
			occurrence: occurrence, startEventID: start.ID, state: ModuleCompletedState,
		}); err != nil {
			return nil, err
		}
		if err := registry.addName(moduleNameRuntime{
			nameID: "architecture-name:" + architectureInstanceAuditID(instanceID), moduleID: moduleID, owner: parent,
			name: name, kind: "architecture-constituent", acquiredAfter: []gorapide.EventID{start.ID},
		}); err != nil {
			return nil, err
		}
	}
	for _, componentID := range model.componentIDs {
		start := moduleStarts[componentID]
		generator := model.staticModuleGenerators[componentID]
		if start == nil || generator == "" {
			continue
		}
		ownerID := model.componentArchitectures[componentID]
		if ownerID == "" {
			ownerID = ArchitectureInterfaceID
		}
		owner := architectureModules[ownerID].Identity()
		if owner == "" {
			return nil, fmt.Errorf("static module lifecycle %q has no owning architecture allocation", componentID)
		}
		module := staticModules[componentID]
		moduleID := module.Identity()
		if moduleID == "" {
			return nil, fmt.Errorf("static module lifecycle %q has no allocation identity", componentID)
		}
		state := ModuleCompletedState
		if len(model.processes[componentID]) != 0 {
			state = ModuleRunningState
		}
		if err := registry.addModule(moduleLifecycleRuntime{
			moduleID: moduleID, kind: "module-generator-result", parent: owner,
			generator: generator, occurrence: "component:" + componentID,
			startEventID: start.ID, state: state,
		}); err != nil {
			return nil, err
		}
		if err := registry.addName(moduleNameRuntime{
			nameID: "component-name:" + componentID, moduleID: moduleID, owner: owner,
			name: componentID, kind: "architecture-constituent", acquiredAfter: []gorapide.EventID{start.ID},
		}); err != nil {
			return nil, err
		}
	}
	recordStartByModule := make(map[string]*gorapide.Event, len(recordStarts))
	for _, start := range recordStarts {
		if start != nil {
			recordStartByModule[start.Source] = start
		}
	}
	for _, componentID := range model.componentIDs {
		for _, declaration := range model.staticRecordObjects[componentID] {
			value := recordObjects[componentID][declaration.Name()]
			start := recordStartByModule[value.Identity()]
			if start == nil {
				return nil, fmt.Errorf("Record object lifecycle %s.%s has no Start", componentID, declaration.Name())
			}
			moduleID := value.Identity()
			owner := staticModules[componentID].Identity()
			if owner == "" {
				return nil, fmt.Errorf("Record object lifecycle %s.%s has no owning module allocation", componentID, declaration.Name())
			}
			if err := registry.addModule(moduleLifecycleRuntime{
				moduleID: moduleID, kind: "record-module", parent: owner,
				generator: "record-literal", occurrence: "module-object:" + declaration.Name(),
				startEventID: start.ID, state: ModuleCompletedState,
			}); err != nil {
				return nil, err
			}
			if err := registry.addName(moduleNameRuntime{
				nameID:   "record-name:" + componentID + "/" + declaration.Name(),
				moduleID: moduleID, owner: owner, name: declaration.Name(), kind: "module-object",
				acquiredAfter: []gorapide.EventID{start.ID},
			}); err != nil {
				return nil, err
			}
		}
	}
	return registry, nil
}

func deterministicArchitectureInstanceLocalIDOrSelf(instanceID string) string {
	if local, ok := deterministicArchitectureInstanceLocalID(instanceID); ok {
		return local
	}
	return instanceID
}

func updateStaticModuleLifecycleStates(
	registry *moduleLifecycleRegistry,
	model *deterministicModel,
	staticModules map[string]gorapide.RapideModuleValue,
	processes map[string][]*processRuntime,
) error {
	for _, componentID := range model.componentIDs {
		if model.staticModuleGenerators[componentID] == "" {
			continue
		}
		state := ModuleCompletedState
		for _, process := range processes[componentID] {
			if process != nil && !process.terminated {
				state = ModuleRunningState
				break
			}
		}
		moduleID := staticModules[componentID].Identity()
		if moduleID == "" {
			return fmt.Errorf("static module lifecycle %q has no allocation identity", componentID)
		}
		if current := registry.modules[moduleID]; current != nil &&
			(current.state == ModuleTerminatedState || current.state == ModuleFinalizedState) {
			continue
		}
		if err := registry.setState(moduleID, state); err != nil {
			return err
		}
	}
	return nil
}

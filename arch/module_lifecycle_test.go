package arch

import (
	"bytes"
	"runtime"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/pattern"
)

func lifecycleRecordByKind(t *testing.T, result *ExecutionResult, kind string) ModuleLifecycleRecord {
	t.Helper()
	matches := make([]ModuleLifecycleRecord, 0, 1)
	for _, record := range result.Modules {
		if record.Kind == kind {
			matches = append(matches, record)
		}
	}
	if len(matches) != 1 {
		t.Fatalf("module lifecycle kind %q records=%#v", kind, matches)
	}
	return matches[0]
}

func lifecycleRecordByNameID(t *testing.T, result *ExecutionResult, nameID string) ModuleLifecycleRecord {
	t.Helper()
	for _, record := range result.Modules {
		for _, name := range record.Names {
			if name.NameID == nameID {
				return record
			}
		}
	}
	t.Fatalf("module lifecycle name %q is absent", nameID)
	return ModuleLifecycleRecord{}
}

func eventByAuditID(t *testing.T, poset *gorapide.Poset, eventID string) *gorapide.Event {
	t.Helper()
	event, exists := poset.Event(gorapide.EventID(eventID))
	if !exists {
		t.Fatalf("event %q is absent", eventID)
	}
	return event
}

func TestModuleLifecycleInitializingOwnerRetainsLexicalChildOnlyUntilCompletion(t *testing.T) {
	registry := newModuleLifecycleRegistry()
	if err := registry.addModule(moduleLifecycleRuntime{
		moduleID: "parent", kind: "allocator-module", startEventID: "parent-start",
		state: ModuleCompletedState, initializing: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := registry.addModule(moduleLifecycleRuntime{
		moduleID: "child", kind: "allocator-module", parent: "parent",
		startEventID: "child-start", state: ModuleCompletedState,
	}); err != nil {
		t.Fatal(err)
	}
	if err := registry.addName(moduleNameRuntime{
		nameID: "function-local:child", moduleID: "child", owner: "parent",
		name: "Child", kind: "function-local", acquiredAfter: []gorapide.EventID{"child-start"},
	}); err != nil {
		t.Fatal(err)
	}
	finalized, _, err := registry.releaseName(
		"self-name:child", []gorapide.EventID{"child-start"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if finalized != "" || !registry.namable()["child"] {
		t.Fatalf("initializing parent did not retain lexical child: finalized=%q namable=%#v",
			finalized, registry.namable())
	}
	if err := registry.completeInitialization("parent"); err != nil {
		t.Fatal(err)
	}
	finalized, causes, err := registry.releaseName(
		"function-local:child", []gorapide.EventID{"function-return"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if finalized != "child" || len(causes) != 2 ||
		causes[0] != "child-start" || causes[1] != "function-return" {
		t.Fatalf("completed parent lexical release=%q causes=%#v", finalized, causes)
	}
	if err := registry.completeInitialization("parent"); err == nil {
		t.Fatal("completed initialization root was removed twice")
	}
}

func TestModuleLifecycleHandledInitializationAbandonmentLosesProvisionalNamesAtomically(t *testing.T) {
	registry := newModuleLifecycleRegistry()
	if err := registry.addExternalRoot("environment"); err != nil {
		t.Fatal(err)
	}
	if err := registry.addModule(moduleLifecycleRuntime{
		moduleID: "abandoned", kind: "module-generator-result", parent: "environment",
		startEventID: "start", state: ModuleCompletedState,
	}); err != nil {
		t.Fatal(err)
	}
	if err := registry.addName(moduleNameRuntime{
		nameID: "result-name", moduleID: "abandoned", owner: "environment",
		name: "Result", kind: "architecture-constituent",
		acquiredAfter: []gorapide.EventID{"start"},
	}); err != nil {
		t.Fatal(err)
	}
	causes, err := registry.finalizeInitializationAbandonment(
		"abandoned", []gorapide.EventID{"recovered", "recovered"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(causes) != 1 || causes[0] != "recovered" {
		t.Fatalf("handled abandonment causes=%#v", causes)
	}
	if err := registry.setFinish("abandoned", "finish"); err != nil {
		t.Fatal(err)
	}
	records, err := registry.records()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].State != ModuleFinalizedState ||
		records[0].Namable || records[0].TerminationEventID != "" ||
		records[0].FinishEventID != "finish" {
		t.Fatalf("handled abandonment lifecycle=%#v", records)
	}
	for _, name := range records[0].Names {
		if name.Live || len(name.LostAfter) != 1 || name.LostAfter[0] != "recovered" {
			t.Fatalf("handled abandonment name=%#v", name)
		}
	}
	if _, err := registry.finalizeInitializationAbandonment(
		"abandoned", []gorapide.EventID{"again"},
	); err == nil {
		t.Fatal("finalized module accepted repeated initialization abandonment")
	}
}

func TestModuleLifecycleArchitectureScopeLossFinalizesCompletedConstituentsAtomically(t *testing.T) {
	registry := newModuleLifecycleRegistry()
	if err := registry.addExternalRoot("environment"); err != nil {
		t.Fatal(err)
	}
	for _, module := range []moduleLifecycleRuntime{
		{moduleID: "root", kind: "architecture", parent: "environment", startEventID: "root-start", state: ModuleCompletedState},
		{moduleID: "completed", kind: "module-generator-result", parent: "root", startEventID: "completed-start", state: ModuleCompletedState},
		{moduleID: "terminated", kind: "module-generator-result", parent: "root", startEventID: "terminated-start", state: ModuleCompletedState},
		{moduleID: "running", kind: "module-generator-result", parent: "root", startEventID: "running-start", state: ModuleRunningState},
	} {
		if err := registry.addModule(module); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []moduleNameRuntime{
		{nameID: "architecture-name", moduleID: "root", owner: "environment", name: "Root", kind: "architecture-constituent", acquiredAfter: []gorapide.EventID{"root-start"}},
		{nameID: "component-name:completed", moduleID: "completed", owner: "root", name: "completed", kind: "architecture-constituent", acquiredAfter: []gorapide.EventID{"completed-start"}},
		{nameID: "component-name:terminated", moduleID: "terminated", owner: "root", name: "terminated", kind: "architecture-constituent", acquiredAfter: []gorapide.EventID{"terminated-start"}},
		{nameID: "component-name:running", moduleID: "running", owner: "root", name: "running", kind: "architecture-constituent", acquiredAfter: []gorapide.EventID{"running-start"}},
	} {
		if err := registry.addName(name); err != nil {
			t.Fatal(err)
		}
	}
	if err := registry.terminate("terminated", "failure"); err != nil {
		t.Fatal(err)
	}
	transitions, err := registry.releaseOwnedConstituentNames(
		"root", []gorapide.EventID{"recovery", "recovery"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(transitions) != 2 || transitions[0].moduleID != "completed" ||
		len(transitions[0].causes) != 1 || transitions[0].causes[0] != "recovery" ||
		transitions[1].moduleID != "terminated" || len(transitions[1].causes) != 2 ||
		transitions[1].causes[0] != "failure" || transitions[1].causes[1] != "recovery" {
		t.Fatalf("architecture scope-loss transitions=%#v", transitions)
	}
	if err := registry.setFinish("completed", "completed-finish"); err != nil {
		t.Fatal(err)
	}
	if err := registry.setFinish("terminated", "terminated-finish"); err != nil {
		t.Fatal(err)
	}
	records, err := registry.records()
	if err != nil {
		t.Fatal(err)
	}
	byID := make(map[string]ModuleLifecycleRecord, len(records))
	for _, record := range records {
		byID[record.ModuleID] = record
	}
	if byID["completed"].State != ModuleFinalizedState || byID["completed"].Namable ||
		byID["terminated"].State != ModuleFinalizedState || byID["terminated"].Namable ||
		byID["terminated"].TerminationEventID != "failure" ||
		byID["running"].State != ModuleRunningState || byID["running"].Namable ||
		byID["root"].State != ModuleCompletedState || !byID["root"].Namable {
		t.Fatalf("architecture scope-loss lifecycles=%#v", byID)
	}
	for _, moduleID := range []string{"completed", "terminated", "running"} {
		var constituent ModuleNameRecord
		for _, name := range byID[moduleID].Names {
			if name.Kind == "architecture-constituent" {
				constituent = name
			}
		}
		if constituent.NameID == "" || constituent.Live || len(constituent.LostAfter) != 1 ||
			constituent.LostAfter[0] != "recovery" {
			t.Fatalf("architecture scope-loss name %q=%#v", moduleID, constituent)
		}
	}
	moduleID, causes, err := registry.completeProcesses(
		"running", []gorapide.EventID{"process-done"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if moduleID != "running" || len(causes) != 2 ||
		causes[0] != "process-done" || causes[1] != "recovery" {
		t.Fatalf("post-scope running completion=%q causes=%#v", moduleID, causes)
	}
	if err := registry.setFinish("running", "running-finish"); err != nil {
		t.Fatal(err)
	}
	records, err = registry.records()
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		if record.ModuleID == "running" &&
			(record.State != ModuleFinalizedState || record.Namable || record.FinishEventID != "running-finish") {
			t.Fatalf("post-scope running finalization=%#v", record)
		}
	}
}

func TestTemporaryRangeModuleFinalizesWithoutOrderingContinuation(t *testing.T) {
	architecture := NewArchitecture("range-lifecycle")
	component := NewComponent("worker", Interface("Worker").
		OutAction("Go").OutAction("Tick").OutAction("Done").Build(), nil)
	if err := component.SetModuleMembership("Worker_Module"); err != nil {
		t.Fatal(err)
	}
	if err := component.AddDeclarativeRule(
		Rule("iterate").On(pattern.MatchEvent("Go")).Do(
			ForEachIntegerRange("I", LiteralValue(1), LiteralValue(1), CallAction("tick", "Tick")),
			CallAction("done", "Done"),
		).Build(),
	); err != nil {
		t.Fatal(err)
	}
	if err := architecture.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := NewExecutionJournalWithLimits(digest,
		ExecutionLimits{MaxFirings: 20, MaxStatements: 32},
		InputEvent{Key: "go", Source: "worker", Action: "Go"},
	)
	result, err := architecture.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	record := lifecycleRecordByKind(t, result, "predefined-range-iterator")
	staticRecord := lifecycleRecordByNameID(t, result, "component-name:worker")
	var lexicalName, selfName ModuleNameRecord
	for _, name := range record.Names {
		if name.Kind == "implicit-self" {
			selfName = name
		} else {
			lexicalName = name
		}
	}
	if record.State != ModuleFinalizedState || record.Namable || record.FinishEventID == "" ||
		len(record.Names) != 2 || lexicalName.Live || len(lexicalName.LostAfter) != 1 ||
		!selfName.Live || selfName.Owner != record.ModuleID || selfName.Name != "Self" ||
		record.Parent != staticRecord.ModuleID || !strings.HasPrefix(staticRecord.ModuleID, "mod1-") {
		t.Fatalf("range module lifecycle=%#v", record)
	}
	start := eventByAuditID(t, result.Poset, record.StartEventID)
	finish := eventByAuditID(t, result.Poset, record.FinishEventID)
	tick := onlyNamedEvent(t, result.Poset, "Tick")
	done := onlyNamedEvent(t, result.Poset, "Done")
	if start.Source != record.ModuleID || start.Name != ArchitectureStartAction ||
		finish.Source != record.ModuleID || finish.Name != ModuleFinishAction {
		t.Fatalf("range lifecycle events start=%#v finish=%#v", start, finish)
	}
	if !result.Poset.IsCausallyBefore(start.ID, finish.ID) ||
		!result.Poset.IsCausallyBefore(tick.ID, finish.ID) {
		t.Fatal("range Start/body does not causally precede finalization")
	}
	if !result.Poset.IsCausallyIndependent(finish.ID, done.ID) {
		t.Fatal("implicit Finish incorrectly became enclosing statement control")
	}
	direct := result.Poset.DirectCauses(finish.ID)
	if len(direct) != 1 || string(direct[0].ID) != lexicalName.LostAfter[0] {
		t.Fatalf("Finish direct causes/name-loss frontier=%#v/%#v", direct, lexicalName.LostAfter)
	}
	if len(result.Iterators) != 1 || result.Iterators[0].Module != record.ModuleID || !result.Iterators[0].Exhausted {
		t.Fatalf("range iterator audit=%#v", result.Iterators)
	}

	replayed, err := architecture.ReplayDeterministic(journal, mustArtifactDigest(t, result))
	if err != nil {
		t.Fatal(err)
	}
	firstBytes, _ := result.MarshalCanonical()
	replayBytes, _ := replayed.MarshalCanonical()
	if !bytes.Equal(firstBytes, replayBytes) {
		t.Fatal("range lifecycle replay changed canonical artifact")
	}
	prior := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(prior)
	single, err := architecture.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	runtime.GOMAXPROCS(4)
	multi, err := architecture.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	singleBytes, _ := single.MarshalCanonical()
	multiBytes, _ := multi.MarshalCanonical()
	if !bytes.Equal(firstBytes, singleBytes) || !bytes.Equal(singleBytes, multiBytes) {
		t.Fatal("range lifecycle changed with GOMAXPROCS")
	}
}

func TestGeneratedIteratorNameSurvivesSuspensionAndFinalizesAfterExhaustion(t *testing.T) {
	integerType, _ := gorapide.RapidePredefinedType("Integer")
	generator := testFiniteIteratorGenerator(t, "Resumable_Lifecycle", integerType, int64(7), int64(8))
	architecture := NewArchitecture("resumable-lifecycle")
	if err := architecture.AddFiniteIteratorGenerator(generator); err != nil {
		t.Fatal(err)
	}
	component := NewComponent("worker", Interface("Worker").
		OutAction("Go").OutAction("Tick", P("value", "Integer")).Build(), nil)
	if err := component.AddBasicClock("C"); err != nil {
		t.Fatal(err)
	}
	if err := component.AddDeclarativeProcess(Process("p").StartAt("wait").States(
		AwaitState("wait", Await("go").On(pattern.MatchEvent("Go")).Do(
			ForEachGeneratedIterator("I", generator,
				PauseFor("C", 1), CallAction("tick", "Tick", BindingParam("value", "I"))),
		).Terminate().Build()),
	).Build()); err != nil {
		t.Fatal(err)
	}
	if err := architecture.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	result, err := architecture.ExecuteDeterministic(NewExecutionJournalWithLimits(digest,
		ExecutionLimits{MaxFirings: 20, MaxStatements: 64},
		InputEvent{Key: "go", Source: "worker", Action: "Go"},
	))
	if err != nil {
		t.Fatal(err)
	}
	record := lifecycleRecordByKind(t, result, "iterator-generator-result")
	finish := eventByAuditID(t, result.Poset, record.FinishEventID)
	ticks := result.Poset.ByName("Tick")
	if record.State != ModuleFinalizedState || record.Namable || len(ticks) != 2 || len(result.ClockAdvances) != 2 {
		t.Fatalf("resumable lifecycle/ticks/clocks=%#v/%d/%#v", record, len(ticks), result.ClockAdvances)
	}
	for _, tick := range ticks {
		if !result.Poset.IsCausallyBefore(tick.ID, finish.ID) {
			t.Fatalf("iterator finalized before resumed Tick %s", tick.ID)
		}
	}
}

func TestProcessExitReleasesNestedGeneratedIteratorName(t *testing.T) {
	integerType, _ := gorapide.RapidePredefinedType("Integer")
	generator := testFiniteIteratorGenerator(t, "Exit_Lifecycle", integerType, int64(1), int64(2))
	architecture := NewArchitecture("exit-lifecycle")
	if err := architecture.AddFiniteIteratorGenerator(generator); err != nil {
		t.Fatal(err)
	}
	component := NewComponent("worker", Interface("Worker").
		OutAction("Go").OutAction("Tick").OutAction("Unreachable").Build(), nil)
	if err := component.AddDeclarativeProcess(Process("p").StartAt("wait").States(
		WhenState("wait", pattern.MatchEvent("Go"), StatementBody(
			ForEachGeneratedIterator("I", generator,
				CallAction("tick", "Tick"), ExitEnclosingWhen()),
			CallAction("unreachable", "Unreachable"),
		)),
	).Build()); err != nil {
		t.Fatal(err)
	}
	if err := architecture.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	result, err := architecture.ExecuteDeterministic(NewExecutionJournalWithLimits(digest,
		ExecutionLimits{MaxFirings: 20, MaxStatements: 32},
		InputEvent{Key: "go", Source: "worker", Action: "Go"},
	))
	if err != nil {
		t.Fatal(err)
	}
	record := lifecycleRecordByKind(t, result, "iterator-generator-result")
	if record.State != ModuleFinalizedState || record.Namable || record.FinishEventID == "" ||
		len(result.Poset.ByName("Tick")) != 1 || len(result.Poset.ByName("Unreachable")) != 0 ||
		len(result.Processes) != 1 || !result.Processes[0].Terminated {
		t.Fatalf("process-exit lifecycle/result=%#v/%#v", record, result.Processes)
	}
}

func TestStaticGeneratedModulesRemainNamedAcrossCompletedAndRunningStates(t *testing.T) {
	architecture := NewArchitecture("static-lifecycle")
	passive := NewComponent("passive", Interface("Passive").Build(), nil)
	if err := passive.SetModuleMembership("Passive_Module"); err != nil {
		t.Fatal(err)
	}
	active := NewComponent("active", Interface("Active").OutAction("Go").Build(), nil)
	if err := active.SetModuleMembership("Active_Module"); err != nil {
		t.Fatal(err)
	}
	if err := active.AddDeclarativeProcess(Process("p").StartAt("wait").States(
		AwaitState("wait", Await("go").On(pattern.MatchEvent("Go")).Do(NullStatement()).Terminate().Build()),
	).Build()); err != nil {
		t.Fatal(err)
	}
	if err := architecture.AddComponent(passive); err != nil {
		t.Fatal(err)
	}
	if err := architecture.AddComponent(active); err != nil {
		t.Fatal(err)
	}
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	result, err := architecture.ExecuteDeterministic(NewExecutionJournalWithLimits(digest,
		ExecutionLimits{MaxFirings: 20, MaxStatements: 32},
	))
	if err != nil {
		t.Fatal(err)
	}
	root := lifecycleRecordByNameID(t, result, "architecture-name:"+ArchitectureInterfaceID)
	passiveRecord := lifecycleRecordByNameID(t, result, "component-name:passive")
	activeRecord := lifecycleRecordByNameID(t, result, "component-name:active")
	if root.State != ModuleCompletedState || !root.Namable || !strings.HasPrefix(root.ModuleID, "mod1-") ||
		passiveRecord.State != ModuleCompletedState || !passiveRecord.Namable || passiveRecord.FinishEventID != "" ||
		activeRecord.State != ModuleRunningState || !activeRecord.Namable || activeRecord.FinishEventID != "" ||
		passiveRecord.Parent != root.ModuleID || activeRecord.Parent != root.ModuleID {
		t.Fatalf("static lifecycle root/passive/active=%#v/%#v/%#v", root, passiveRecord, activeRecord)
	}
	for componentID, record := range map[string]ModuleLifecycleRecord{
		"passive": passiveRecord,
		"active":  activeRecord,
	} {
		if !strings.HasPrefix(record.ModuleID, "mod1-") || record.ModuleID == staticModuleAuditID(componentID) {
			t.Fatalf("static module %q has no allocation identity: %#v", componentID, record)
		}
		start := eventByAuditID(t, result.Poset, record.StartEventID)
		if start.Source != staticModuleAuditID(componentID) ||
			!start.HasObservation(staticModuleAuditID(componentID), ArchitectureStartAction) {
			t.Fatalf("static module %q Start audit view=%#v", componentID, start)
		}
	}
}

func TestModuleLifecycleFinalizationRetainsEarlierProcessCompletionFrontier(t *testing.T) {
	registry := newModuleLifecycleRegistry()
	if err := registry.addExternalRoot("root"); err != nil {
		t.Fatal(err)
	}
	if err := registry.addModule(moduleLifecycleRuntime{
		moduleID: "dynamic", kind: "allocator-module", parent: "root",
		generator: "Worker", occurrence: "test", startEventID: "start",
		state: ModuleRunningState,
	}); err != nil {
		t.Fatal(err)
	}
	if err := registry.addName(moduleNameRuntime{
		nameID: "allocator-result:dynamic", moduleID: "dynamic", owner: "root",
		name: "New", kind: "allocator-result", acquiredAfter: []gorapide.EventID{"start"},
	}); err != nil {
		t.Fatal(err)
	}
	moduleID, causes, err := registry.completeProcesses("dynamic", []gorapide.EventID{"process-done"})
	if err != nil {
		t.Fatal(err)
	}
	if moduleID != "" || causes != nil {
		t.Fatalf("named completed module finalized early: module=%q causes=%v", moduleID, causes)
	}
	moduleID, causes, err = registry.releaseName(
		"allocator-result:dynamic", []gorapide.EventID{"result-lost"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if moduleID != "dynamic" || len(causes) != 2 ||
		causes[0] != "process-done" || causes[1] != "result-lost" {
		t.Fatalf("late name loss omitted process completion: module=%q causes=%v", moduleID, causes)
	}
}

func mustArtifactDigest(t *testing.T, result *ExecutionResult) string {
	t.Helper()
	digest, err := result.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

package rapide

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/ShaneDolphin/gorapide/arch"
	"github.com/ShaneDolphin/gorapide/pattern"
)

// MapCompileOptions are the explicit semantic inputs to one supported map
// generator application. DomainID names the active actual object whose poset
// will be supplied at execution. An omitted dependency policy selects the
// Stanford strong relation through the deterministic map kernel.
type MapCompileOptions struct {
	DomainID                string
	InducedDependencyPolicy arch.MapInducedDependencyPolicy
}

// CompileMap parses, type-checks, and lowers one named map generator applied
// to one exact-interface active domain object. An empty mapName is permitted
// only when the file declares exactly one map.
func CompileMap(source []byte, mapName, domainID string) (*arch.EventPatternMap, error) {
	return CompileMapWithOptions(source, mapName, MapCompileOptions{DomainID: domainID})
}

// CompileMapWithOptions is CompileMap with an explicit published induced-
// dependency policy. Policy is canonical semantic model content.
func CompileMapWithOptions(
	source []byte,
	mapName string,
	options MapCompileOptions,
) (*arch.EventPatternMap, error) {
	file, err := Parse(source)
	if err != nil {
		return nil, err
	}
	return CompileMapFileWithOptions(file, mapName, options)
}

// CompileMapFile lowers a parsed map generator with its explicit actual-domain
// identity and the default strong induced-dependency policy.
func CompileMapFile(file *File, mapName, domainID string) (*arch.EventPatternMap, error) {
	return CompileMapFileWithOptions(file, mapName, MapCompileOptions{DomainID: domainID})
}

// CompileMapFileWithOptions lowers the current closed source-map slice. The
// actual domain has exactly the declared interface indicator in this slice;
// binding a richer structural subtype and module-generator/map indicators are
// retained as explicit future compatibility boundaries.
func CompileMapFileWithOptions(
	file *File,
	mapName string,
	options MapCompileOptions,
) (*arch.EventPatternMap, error) {
	if file == nil {
		return nil, &TypeError{Position: Position{Line: 1, Column: 1}, Message: "source file is nil"}
	}
	if strings.TrimSpace(options.DomainID) == "" {
		return nil, typeError(Position{Line: 1, Column: 1}, "map actual domain identity is required")
	}

	maps := make(map[string]MapDecl, len(file.Maps))
	for _, declaration := range file.Maps {
		key := folded(declaration.Name)
		if key == "" || maps[key].Name != "" {
			return nil, typeError(declaration.Position, "duplicate map %q", declaration.Name)
		}
		maps[key] = declaration
	}
	if mapName == "" {
		if len(file.Maps) != 1 {
			return nil, typeError(Position{Line: 1, Column: 1},
				"map name is required when the file declares %d maps", len(file.Maps))
		}
		mapName = file.Maps[0].Name
	}
	declaration, exists := maps[folded(mapName)]
	if !exists {
		return nil, typeError(Position{Line: 1, Column: 1}, "map %q is not declared", mapName)
	}
	if len(declaration.Parameters) != 0 {
		return nil, typeError(declaration.Parameters[0].Position,
			"map %q formal parameters for non-active module values are outside the current source-map subset", declaration.Name)
	}
	if len(declaration.Domains) != 1 {
		return nil, typeError(declaration.Position,
			"map %q has %d domain indicators; the current deterministic source-map subset requires exactly one",
			declaration.Name, len(declaration.Domains))
	}
	if declaration.Domains[0].Kind != TypeExpressionName {
		return nil, typeError(declaration.Domains[0].Position,
			"map %q domain indicator %s must be one named interface type in the current source-map subset",
			declaration.Name, typeExpressionSpelling(declaration.Domains[0]))
	}
	if declaration.Range.Kind != TypeExpressionName {
		return nil, typeError(declaration.Range.Position,
			"map %q range indicator %s must be one named interface type in the current source-map subset",
			declaration.Name, typeExpressionSpelling(declaration.Range))
	}
	if module, ok := sourceModuleByName(file.Modules, declaration.Domains[0].Name); ok {
		return nil, typeError(declaration.Domains[0].Position,
			"map %q domain indicator %q names module generator %q; module-generator domains are outside the current source-map subset",
			declaration.Name, declaration.Domains[0].Name, module.Name)
	}
	if module, ok := sourceModuleByName(file.Modules, declaration.Range.Name); ok {
		return nil, typeError(declaration.Range.Position,
			"map %q range indicator %q names module generator %q; module-generator ranges are outside the current source-map subset",
			declaration.Name, declaration.Range.Name, module.Name)
	}

	interfaces, typeElaborator, err := mapSourceTypeEnvironment(file)
	if err != nil {
		return nil, err
	}
	domainKey, domainSource, err := typeElaborator.interfaceDeclaration(
		declaration.Domains[0].Position, declaration.Domains[0].Name,
	)
	if err != nil {
		return nil, typeError(declaration.Domains[0].Position,
			"map %q domain indicator %q: %v", declaration.Name, declaration.Domains[0].Name, err)
	}
	rangeKey, rangeSource, err := typeElaborator.interfaceDeclaration(
		declaration.Range.Position, declaration.Range.Name,
	)
	if err != nil {
		return nil, typeError(declaration.Range.Position,
			"map %q range indicator %q: %v", declaration.Name, declaration.Range.Name, err)
	}
	domainSource = interfaces[domainKey]
	rangeSource = interfaces[rangeKey]
	domainExpansion, err := typeElaborator.executionInterfaceExpansion(domainSource)
	if err != nil {
		return nil, err
	}
	rangeExpansion, err := typeElaborator.executionInterfaceExpansion(rangeSource)
	if err != nil {
		return nil, err
	}
	domainExecution := expandedExecutionInterfaceDeclaration(domainSource, domainExpansion)
	rangeExecution := expandedExecutionInterfaceDeclaration(rangeSource, rangeExpansion)

	domainInterface, rangeInterface, err := compileMapExecutionInterfaces(
		file, declaration, domainSource.Name, rangeSource.Name,
	)
	if err != nil {
		return nil, err
	}

	builder := arch.NewEventPatternMap(declaration.Name).
		FromObject(options.DomainID, domainInterface).
		ToInterface(rangeInterface).
		WithInducedDependencyPolicy(options.InducedDependencyPolicy)
	seenRules := make(map[string]bool, len(declaration.Rules))
	for _, source := range declaration.Rules {
		key := mapRuleSemanticKey(source)
		if seenRules[key] {
			return nil, typeError(source.Position, "duplicate map rule %s", key)
		}
		seenRules[key] = true
		rule, err := compileSourceMapRule(
			declaration.Name, key, source, domainExecution, rangeExecution,
		)
		if err != nil {
			return nil, err
		}
		builder.AddRule(rule)
	}
	rangeConstraints, err := compileModuleInstanceConstraints(
		declaration.Name, rangeExecution, nil, nil,
	)
	if err != nil {
		return nil, err
	}
	if rangeConstraints != nil {
		builder.WithRangeConstraints(rangeConstraints)
	}
	mapping := builder.Build()
	if _, err := mapping.PrepareDeterministic(); err != nil {
		return nil, typeError(declaration.Position, "map %q: %v", declaration.Name, err)
	}
	return mapping, nil
}

func sourceModuleByName(modules []ModuleDecl, name string) (ModuleDecl, bool) {
	for _, module := range modules {
		if keyword(module.Name, name) {
			return module, true
		}
	}
	return ModuleDecl{}, false
}

func mapSourceTypeEnvironment(
	file *File,
) (map[string]InterfaceDecl, *sourceTypeElaborator, error) {
	if _, err := compileExceptionDeclarations("outermost", file.Exceptions); err != nil {
		return nil, nil, err
	}
	sourceInterfaces := make([]InterfaceDecl, len(file.Interfaces))
	for index, declaration := range file.Interfaces {
		sourceInterfaces[index] = cloneInterfaceForNormalization(declaration)
		sourceInterfaces[index].Exceptions = mergeVisibleExceptionDeclarations(
			visibleOutermostExceptions(file.Exceptions, declaration.Position),
			sourceInterfaces[index].Exceptions...,
		)
	}
	interfaces, err := normalizeInterfaceDeclarationsWithAliases(sourceInterfaces, file.TypeAliases)
	if err != nil {
		return nil, nil, err
	}
	typeElaborator, err := newSourceTypeElaboratorWithUnionsAndEnumerations(
		interfaces, file.TypeAliases, file.Unions, file.Enumerations,
	)
	if err != nil {
		return nil, nil, err
	}
	return interfaces, typeElaborator, nil
}

// compileMapExecutionInterfaces reuses the ordinary source interface lowering
// without executing interface behavior or validating domain constraints, both
// of which the published type-expression map domain deliberately cannot see.
func compileMapExecutionInterfaces(
	file *File,
	declaration MapDecl,
	domainType, rangeType string,
) (*arch.InterfaceDecl, *arch.InterfaceDecl, error) {
	interfaces := make([]InterfaceDecl, len(file.Interfaces))
	for index, source := range file.Interfaces {
		interfaces[index] = cloneInterfaceForNormalization(source)
		interfaces[index].Behavior = nil
		interfaces[index].Constraints = nil
	}
	const (
		architectureName = "__gorapide_source_map_interface_lowering"
		domainComponent  = "__gorapide_source_map_domain"
		rangeComponent   = "__gorapide_source_map_range"
	)
	synthetic := &File{
		Interfaces:   interfaces,
		Exceptions:   append([]ExceptionDecl(nil), file.Exceptions...),
		Unions:       append([]UnionDecl(nil), file.Unions...),
		Enumerations: append([]EnumerationDecl(nil), file.Enumerations...),
		TypeAliases:  append([]TypeAliasDecl(nil), file.TypeAliases...),
		Architectures: []ArchitectureDecl{{
			Position:   declaration.Position,
			Name:       architectureName,
			ReturnType: "Root",
			ReturnTypeExpression: TypeExpressionDecl{
				Position: declaration.Position, Kind: TypeExpressionName, Name: "Root",
			},
			Components: []ComponentDecl{
				{Position: declaration.Domains[0].Position, Name: domainComponent, InterfaceType: domainType},
				{Position: declaration.Range.Position, Name: rangeComponent, InterfaceType: rangeType},
			},
		}},
	}
	compiled, err := CompileFile(synthetic, architectureName)
	if err != nil {
		return nil, nil, err
	}
	domain, domainExists := compiled.Component(domainComponent)
	rangeObject, rangeExists := compiled.Component(rangeComponent)
	if !domainExists || !rangeExists || domain.Interface == nil || rangeObject.Interface == nil {
		return nil, nil, typeError(declaration.Position,
			"map %q interface lowering did not produce both domain and range interfaces", declaration.Name)
	}
	return domain.Interface, rangeObject.Interface, nil
}

func compileSourceMapRule(
	mapName, semanticKey string,
	source MapRuleDecl,
	domain, rangeInterface InterfaceDecl,
) (*arch.DeclarativeRule, error) {
	if source.Connector != ConnectAgent {
		return nil, typeError(source.Position,
			"map agent state-transition rules require the published '||>' operator")
	}
	placeholders := canonicalMapPlaceholders(source.Placeholders)
	bindings, err := compilePatternBindings(placeholders, "map")
	if err != nil {
		return nil, err
	}
	bound := make(map[string]bool, len(placeholders))
	trigger, err := compileSourceMapPattern(domain, source.Trigger, placeholders, bindings, bound)
	if err != nil {
		return nil, err
	}
	for index, placeholder := range placeholders {
		if !bound[folded(placeholder.Name)] {
			return nil, typeError(source.Placeholders[index].Position,
				"map placeholder %s is never bound by the trigger", patternPlaceholderDisplay(source.Placeholders[index]))
		}
	}
	builder := arch.MappingRule("rpd:map:" + folded(mapName) + ":rule:" + semanticKey).On(trigger)
	if source.Guard != nil {
		guard, err := compileBehaviorExpression(*source.Guard, bindings, nil)
		if err != nil {
			return nil, err
		}
		if guard.typeName != "Boolean" {
			return nil, typeError(source.Guard.Position,
				"map rule guard has type %s, want Boolean", guard.typeName)
		}
		builder.Where(guard.value)
	}
	if source.Generator == nil {
		return builder.NoEvents().Build(), nil
	}
	alternatives, err := compileSourceMapGenerator(*source.Generator, rangeInterface, bindings)
	if err != nil {
		return nil, err
	}
	if len(alternatives) == 1 {
		return builder.Generate(alternatives[0]...).Build(), nil
	}
	return builder.GenerateOneOf(alternatives...).Build(), nil
}

type compiledMapGenerator struct {
	outputs []arch.RuleOutput
	roots   []string
	maxima  []string
}

const maxSourceMapIterationOccurrences uint64 = pattern.MaxNamedRangeIterationCardinality

const (
	maxSourceMapJoinCrossPairs        = 5
	maxSourceMapGeneratorAlternatives = 256
)

func compileSourceMapGenerator(
	source PosetGeneratorDecl,
	rangeInterface InterfaceDecl,
	bindings map[string]behaviorBinding,
) ([][]arch.RuleOutput, error) {
	if err := validateSourceMapGenerator(source, rangeInterface, bindings); err != nil {
		return nil, err
	}
	normalized, err := normalizeMapPosetGenerator(source)
	if err != nil {
		return nil, err
	}
	if joins := mapPosetGeneratorJoinCount(normalized); joins > 1 {
		return nil, typeError(source.Position,
			"map poset-generator join currently permits one finite arbitrary-choice site per rule, got %d", joins)
	}
	nextID := 0
	compiled, err := compileSourceMapGeneratorAlternativesNode(normalized, rangeInterface, bindings, &nextID)
	if err != nil {
		return nil, err
	}
	result := make([][]arch.RuleOutput, len(compiled))
	if len(compiled) > maxSourceMapGeneratorAlternatives {
		return nil, typeError(source.Position,
			"map poset generator produces more than the deterministic bound of %d alternatives",
			maxSourceMapGeneratorAlternatives)
	}
	for index, alternative := range compiled {
		result[index] = alternative.outputs
	}
	return result, nil
}

func validateSourceMapGenerator(
	source PosetGeneratorDecl,
	rangeInterface InterfaceDecl,
	bindings map[string]behaviorBinding,
) error {
	switch source.Kind {
	case PosetGeneratorEmpty:
		return nil
	case PosetGeneratorEvent:
		nextID := 0
		_, err := compileSourceMapGeneratorNode(source, rangeInterface, bindings, &nextID)
		return err
	case PosetGeneratorBinary:
		if source.Left == nil || source.Right == nil {
			return typeError(source.Position, "map poset generator has an incomplete binary operator")
		}
		operator := strings.ToLower(source.Operator)
		if operator != "->" && operator != "|>" && operator != "||" && operator != "~" && operator != "or" {
			return nil
		}
		if err := validateSourceMapGenerator(*source.Left, rangeInterface, bindings); err != nil {
			return err
		}
		return validateSourceMapGenerator(*source.Right, rangeInterface, bindings)
	case PosetGeneratorIteration:
		if source.Inner == nil {
			return typeError(source.Position, "map poset-generator iteration has a missing operand")
		}
		if _, err := mapPosetGeneratorIterationCardinality(source); err != nil {
			return err
		}
		inner := cloneMapPosetGenerator(*source.Inner)
		if source.Iterator != "" {
			inner = substituteMapPosetGeneratorIterator(inner, source.Iterator, source.First)
		}
		return validateSourceMapGenerator(inner, rangeInterface, bindings)
	default:
		return typeError(source.Position, "unsupported map poset-generator node kind %q", source.Kind)
	}
}

func compileSourceMapGeneratorAlternativesNode(
	source PosetGeneratorDecl,
	rangeInterface InterfaceDecl,
	bindings map[string]behaviorBinding,
	nextID *int,
) ([]compiledMapGenerator, error) {
	if source.Kind != PosetGeneratorBinary {
		compiled, err := compileSourceMapGeneratorNode(source, rangeInterface, bindings, nextID)
		if err != nil {
			return nil, err
		}
		return []compiledMapGenerator{compiled}, nil
	}
	if source.Left == nil || source.Right == nil {
		return nil, typeError(source.Position, "map poset generator has an incomplete binary operator")
	}
	operator := strings.ToLower(source.Operator)
	var left, right []compiledMapGenerator
	var err error
	if operator == "or" {
		start := *nextID
		leftNext, rightNext := start, start
		left, err = compileSourceMapGeneratorAlternativesNode(*source.Left, rangeInterface, bindings, &leftNext)
		if err != nil {
			return nil, err
		}
		right, err = compileSourceMapGeneratorAlternativesNode(*source.Right, rangeInterface, bindings, &rightNext)
		if err != nil {
			return nil, err
		}
		if rightNext > leftNext {
			leftNext = rightNext
		}
		*nextID = leftNext
	} else {
		left, err = compileSourceMapGeneratorAlternativesNode(*source.Left, rangeInterface, bindings, nextID)
		if err != nil {
			return nil, err
		}
		right, err = compileSourceMapGeneratorAlternativesNode(*source.Right, rangeInterface, bindings, nextID)
		if err != nil {
			return nil, err
		}
	}
	if operator == "or" {
		return boundedCompiledMapGeneratorAlternatives(source.Position, append(append([]compiledMapGenerator{}, left...), right...))
	}
	if operator == "~" {
		if mapPosetGeneratorContainsOperator(*source.Left, "|>") ||
			mapPosetGeneratorContainsOperator(*source.Right, "|>") {
			return nil, typeError(source.Position,
				"map poset-generator join operands containing immediate sequence are outside the current bounded choice slice")
		}
		if len(left) != 1 || len(right) != 1 {
			return nil, typeError(source.Position,
				"nested arbitrary map poset-generator joins are outside the current bounded choice slice")
		}
		return enumerateMapGeneratorJoin(source.Position, left[0], right[0])
	}
	if operator != "->" && operator != "|>" && operator != "||" {
		return nil, typeError(source.Position,
			"map poset-generator operator %q is published restricted-pattern syntax but is outside the current finite sequence/immediate-sequence/independence/join/disjunction subset",
			source.Operator)
	}
	result := make([]compiledMapGenerator, 0, len(left)*len(right))
	for _, leftAlternative := range left {
		for _, rightAlternative := range right {
			combined := combineCompiledMapGenerators(leftAlternative, rightAlternative, operator)
			result = append(result, combined)
		}
	}
	return boundedCompiledMapGeneratorAlternatives(source.Position, result)
}

func boundedCompiledMapGeneratorAlternatives(position Position, source []compiledMapGenerator) ([]compiledMapGenerator, error) {
	if len(source) > maxSourceMapGeneratorAlternatives {
		return nil, typeError(position,
			"map poset generator produces more than the deterministic bound of %d alternatives",
			maxSourceMapGeneratorAlternatives)
	}
	return source, nil
}

func combineCompiledMapGenerators(left, right compiledMapGenerator, operator string) compiledMapGenerator {
	result := compiledMapGenerator{outputs: appendCopiedRuleOutputs(left.outputs, right.outputs...)}
	if operator == "||" {
		result.roots = canonicalGeneratorIDs(append(append([]string{}, left.roots...), right.roots...))
		result.maxima = canonicalGeneratorIDs(append(append([]string{}, left.maxima...), right.maxima...))
		return result
	}
	causes := canonicalGeneratorIDs(left.maxima)
	roots := make(map[string]bool, len(right.roots))
	for _, id := range right.roots {
		roots[id] = true
	}
	for index := range result.outputs {
		if roots[result.outputs[index].ID] {
			result.outputs[index] = result.outputs[index].After(causes...)
		}
	}
	result.roots = canonicalGeneratorIDs(left.roots)
	result.maxima = canonicalGeneratorIDs(right.maxima)
	return result
}

func appendCopiedRuleOutputs(left []arch.RuleOutput, right ...arch.RuleOutput) []arch.RuleOutput {
	result := make([]arch.RuleOutput, 0, len(left)+len(right))
	for _, output := range append(append([]arch.RuleOutput{}, left...), right...) {
		copy := output
		copy.Parameters = append([]arch.RuleParameter(nil), output.Parameters...)
		copy.Causes = append([]string(nil), output.Causes...)
		copy.Equivalent = append([]string(nil), output.Equivalent...)
		result = append(result, copy)
	}
	return result
}

func compiledMapGeneratorKey(generator compiledMapGenerator) (string, error) {
	encoded, err := json.Marshal(generator.outputs)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func enumerateMapGeneratorJoin(position Position, left, right compiledMapGenerator) ([]compiledMapGenerator, error) {
	crossPairs := len(left.outputs) * len(right.outputs)
	if crossPairs > maxSourceMapJoinCrossPairs {
		return nil, typeError(position,
			"map poset-generator join has %d cross-event pairs, exceeding the deterministic exploration bound of %d",
			crossPairs, maxSourceMapJoinCrossPairs)
	}
	base := appendCopiedRuleOutputs(left.outputs, right.outputs...)
	ids := make([]string, len(base))
	for index, output := range base {
		ids[index] = output.ID
	}
	sort.Strings(ids)
	indexByID := make(map[string]int, len(ids))
	for index, id := range ids {
		indexByID[id] = index
	}
	baseEquivalence := newMapGeneratorEquivalence(len(ids))
	for _, output := range base {
		for _, peer := range output.Equivalent {
			baseEquivalence.union(indexByID[output.ID], indexByID[peer])
		}
	}
	basePreorder, valid := buildMapGeneratorPreorder(base, indexByID, baseEquivalence, nil)
	if !valid {
		return nil, typeError(position, "map poset-generator join has an invalid operand causal preorder")
	}
	pairs := make([][2]int, 0, crossPairs)
	for _, leftOutput := range left.outputs {
		for _, rightOutput := range right.outputs {
			pairs = append(pairs, [2]int{indexByID[leftOutput.ID], indexByID[rightOutput.ID]})
		}
	}
	assignments := 1
	for range pairs {
		assignments *= 4
	}
	seen := make(map[string]compiledMapGenerator)
	for code := 0; code < assignments; code++ {
		equivalence := baseEquivalence.clone()
		edges := make([][2]int, 0, len(pairs))
		current := code
		for _, pair := range pairs {
			switch current % 4 {
			case 1:
				edges = append(edges, pair)
			case 2:
				edges = append(edges, [2]int{pair[1], pair[0]})
			case 3:
				equivalence.union(pair[0], pair[1])
			}
			current /= 4
		}
		preorder, valid := buildMapGeneratorPreorder(base, indexByID, equivalence, edges)
		if !valid ||
			!mapGeneratorOperandPreorderPreserved(preorder, basePreorder, left.outputs, indexByID) ||
			!mapGeneratorOperandPreorderPreserved(preorder, basePreorder, right.outputs, indexByID) {
			continue
		}
		generator := compiledMapGeneratorFromPreorder(base, ids, indexByID, preorder)
		key, err := compiledMapGeneratorKey(generator)
		if err != nil {
			return nil, err
		}
		seen[key] = generator
		if len(seen) > maxSourceMapGeneratorAlternatives {
			return nil, typeError(position,
				"map poset-generator join produces more than %d deterministic alternatives",
				maxSourceMapGeneratorAlternatives)
		}
	}
	keys := make([]string, 0, len(seen))
	for key := range seen {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]compiledMapGenerator, 0, len(keys))
	for _, key := range keys {
		result = append(result, seen[key])
	}
	return result, nil
}

type mapGeneratorEquivalence struct {
	parent []int
}

func newMapGeneratorEquivalence(size int) *mapGeneratorEquivalence {
	result := &mapGeneratorEquivalence{parent: make([]int, size)}
	for index := range result.parent {
		result.parent[index] = index
	}
	return result
}

func (equivalence *mapGeneratorEquivalence) clone() *mapGeneratorEquivalence {
	return &mapGeneratorEquivalence{parent: append([]int(nil), equivalence.parent...)}
}

func (equivalence *mapGeneratorEquivalence) find(index int) int {
	if equivalence.parent[index] != index {
		equivalence.parent[index] = equivalence.find(equivalence.parent[index])
	}
	return equivalence.parent[index]
}

func (equivalence *mapGeneratorEquivalence) union(left, right int) {
	left, right = equivalence.find(left), equivalence.find(right)
	if left == right {
		return
	}
	if right < left {
		left, right = right, left
	}
	equivalence.parent[right] = left
}

type mapGeneratorPreorder struct {
	equivalence *mapGeneratorEquivalence
	strict      [][]bool
}

func buildMapGeneratorPreorder(
	outputs []arch.RuleOutput,
	indexByID map[string]int,
	equivalence *mapGeneratorEquivalence,
	extraEdges [][2]int,
) (mapGeneratorPreorder, bool) {
	equivalence = equivalence.clone()
	strict := make([][]bool, len(indexByID))
	for index := range strict {
		strict[index] = make([]bool, len(indexByID))
	}
	addEdge := func(before, after int) bool {
		before, after = equivalence.find(before), equivalence.find(after)
		if before == after {
			return false
		}
		strict[before][after] = true
		return true
	}
	for _, output := range outputs {
		for _, cause := range output.Causes {
			if !addEdge(indexByID[cause], indexByID[output.ID]) {
				return mapGeneratorPreorder{}, false
			}
		}
	}
	for _, edge := range extraEdges {
		if !addEdge(edge[0], edge[1]) {
			return mapGeneratorPreorder{}, false
		}
	}
	mapGeneratorTransitiveClosure(strict)
	if mapGeneratorRelationCyclic(strict) {
		return mapGeneratorPreorder{}, false
	}
	return mapGeneratorPreorder{equivalence: equivalence, strict: strict}, true
}

func (preorder mapGeneratorPreorder) equivalent(left, right int) bool {
	return preorder.equivalence.find(left) == preorder.equivalence.find(right)
}

func (preorder mapGeneratorPreorder) before(left, right int) bool {
	left, right = preorder.equivalence.find(left), preorder.equivalence.find(right)
	return left != right && preorder.strict[left][right]
}

func mapGeneratorOperandPreorderPreserved(
	candidate, wanted mapGeneratorPreorder,
	outputs []arch.RuleOutput,
	indexByID map[string]int,
) bool {
	for _, left := range outputs {
		for _, right := range outputs {
			leftIndex, rightIndex := indexByID[left.ID], indexByID[right.ID]
			if candidate.equivalent(leftIndex, rightIndex) != wanted.equivalent(leftIndex, rightIndex) ||
				candidate.before(leftIndex, rightIndex) != wanted.before(leftIndex, rightIndex) {
				return false
			}
		}
	}
	return true
}

func compiledMapGeneratorFromPreorder(
	base []arch.RuleOutput,
	ids []string,
	indexByID map[string]int,
	preorder mapGeneratorPreorder,
) compiledMapGenerator {
	byID := make(map[string]arch.RuleOutput, len(base))
	classes := make(map[int][]string)
	for _, output := range base {
		copy := output
		copy.Parameters = append([]arch.RuleParameter(nil), output.Parameters...)
		copy.Causes = nil
		copy.Equivalent = nil
		byID[output.ID] = copy
		root := preorder.equivalence.find(indexByID[output.ID])
		classes[root] = append(classes[root], output.ID)
	}
	representatives := make([]int, 0, len(classes))
	for representative := range classes {
		representatives = append(representatives, representative)
		sort.Strings(classes[representative])
	}
	sort.Ints(representatives)
	for _, members := range classes {
		for _, id := range members {
			output := byID[id]
			for _, peer := range members {
				if peer != id {
					output.Equivalent = append(output.Equivalent, peer)
				}
			}
			byID[id] = output
		}
	}
	for _, after := range representatives {
		causes := make([]string, 0)
		for _, before := range representatives {
			if !preorder.strict[before][after] {
				continue
			}
			direct := true
			for _, middle := range representatives {
				if middle != before && middle != after && preorder.strict[before][middle] && preorder.strict[middle][after] {
					direct = false
					break
				}
			}
			if direct {
				causes = append(causes, ids[before])
			}
		}
		sort.Strings(causes)
		output := byID[ids[after]]
		output.Causes = causes
		byID[ids[after]] = output
	}
	result := compiledMapGenerator{outputs: make([]arch.RuleOutput, 0, len(ids))}
	for _, id := range ids {
		result.outputs = append(result.outputs, byID[id])
	}
	for _, representative := range representatives {
		hasBefore, hasAfter := false, false
		for _, other := range representatives {
			hasBefore = hasBefore || preorder.strict[other][representative]
			hasAfter = hasAfter || preorder.strict[representative][other]
		}
		if !hasBefore {
			result.roots = append(result.roots, classes[representative]...)
		}
		if !hasAfter {
			result.maxima = append(result.maxima, classes[representative]...)
		}
	}
	result.roots = canonicalGeneratorIDs(result.roots)
	result.maxima = canonicalGeneratorIDs(result.maxima)
	return result
}

func mapGeneratorReachability(outputs []arch.RuleOutput, indexByID map[string]int) [][]bool {
	relation := make([][]bool, len(indexByID))
	for index := range relation {
		relation[index] = make([]bool, len(indexByID))
	}
	for _, output := range outputs {
		after := indexByID[output.ID]
		for _, cause := range output.Causes {
			relation[indexByID[cause]][after] = true
		}
	}
	mapGeneratorTransitiveClosure(relation)
	return relation
}

func cloneMapGeneratorRelation(source [][]bool) [][]bool {
	result := make([][]bool, len(source))
	for index := range source {
		result[index] = append([]bool(nil), source[index]...)
	}
	return result
}

func mapGeneratorTransitiveClosure(relation [][]bool) {
	for middle := range relation {
		for before := range relation {
			if !relation[before][middle] {
				continue
			}
			for after := range relation {
				if relation[middle][after] {
					relation[before][after] = true
				}
			}
		}
	}
}

func mapGeneratorRelationCyclic(relation [][]bool) bool {
	for index := range relation {
		if relation[index][index] {
			return true
		}
	}
	return false
}

func mapGeneratorRestrictedRelation(relation [][]bool, outputs []arch.RuleOutput, indexByID map[string]int) [][]bool {
	result := make([][]bool, len(outputs))
	for leftIndex, left := range outputs {
		result[leftIndex] = make([]bool, len(outputs))
		for rightIndex, right := range outputs {
			result[leftIndex][rightIndex] = relation[indexByID[left.ID]][indexByID[right.ID]]
		}
	}
	return result
}

func mapGeneratorRestrictionEqual(relation [][]bool, outputs []arch.RuleOutput, indexByID map[string]int, wanted [][]bool) bool {
	for leftIndex, left := range outputs {
		for rightIndex, right := range outputs {
			if relation[indexByID[left.ID]][indexByID[right.ID]] != wanted[leftIndex][rightIndex] {
				return false
			}
		}
	}
	return true
}

func mapGeneratorOutputsFromRelation(
	base []arch.RuleOutput,
	ids []string,
	indexByID map[string]int,
	relation [][]bool,
) []arch.RuleOutput {
	byID := make(map[string]arch.RuleOutput, len(base))
	for _, output := range base {
		copy := output
		copy.Parameters = append([]arch.RuleParameter(nil), output.Parameters...)
		copy.Causes = nil
		byID[output.ID] = copy
	}
	for _, afterID := range ids {
		after := indexByID[afterID]
		causes := make([]string, 0)
		for _, beforeID := range ids {
			before := indexByID[beforeID]
			if !relation[before][after] {
				continue
			}
			direct := true
			for middle := range ids {
				if middle != before && middle != after && relation[before][middle] && relation[middle][after] {
					direct = false
					break
				}
			}
			if direct {
				causes = append(causes, beforeID)
			}
		}
		sort.Strings(causes)
		output := byID[afterID]
		output.Causes = causes
		byID[afterID] = output
	}
	result := make([]arch.RuleOutput, 0, len(ids))
	for _, id := range ids {
		result = append(result, byID[id])
	}
	return result
}

func compiledMapGeneratorFromOutputs(outputs []arch.RuleOutput, ids []string, indexByID map[string]int, relation [][]bool) compiledMapGenerator {
	result := compiledMapGenerator{outputs: outputs}
	for _, id := range ids {
		index := indexByID[id]
		hasBefore, hasAfter := false, false
		for other := range ids {
			hasBefore = hasBefore || relation[other][index]
			hasAfter = hasAfter || relation[index][other]
		}
		if !hasBefore {
			result.roots = append(result.roots, id)
		}
		if !hasAfter {
			result.maxima = append(result.maxima, id)
		}
	}
	result.roots = canonicalGeneratorIDs(result.roots)
	result.maxima = canonicalGeneratorIDs(result.maxima)
	return result
}

func mapPosetGeneratorJoinCount(source PosetGeneratorDecl) int {
	count := 0
	if source.Kind == PosetGeneratorBinary {
		if strings.EqualFold(source.Operator, "~") {
			count++
		}
		if source.Left != nil {
			count += mapPosetGeneratorJoinCount(*source.Left)
		}
		if source.Right != nil {
			count += mapPosetGeneratorJoinCount(*source.Right)
		}
	}
	return count
}

func mapPosetGeneratorContainsOperator(source PosetGeneratorDecl, wanted string) bool {
	if source.Kind != PosetGeneratorBinary {
		return false
	}
	if strings.EqualFold(source.Operator, wanted) {
		return true
	}
	return source.Left != nil && mapPosetGeneratorContainsOperator(*source.Left, wanted) ||
		source.Right != nil && mapPosetGeneratorContainsOperator(*source.Right, wanted)
}

func compileSourceMapGeneratorNode(
	source PosetGeneratorDecl,
	rangeInterface InterfaceDecl,
	bindings map[string]behaviorBinding,
	nextID *int,
) (compiledMapGenerator, error) {
	switch source.Kind {
	case PosetGeneratorEmpty:
		return compiledMapGenerator{outputs: []arch.RuleOutput{}}, nil
	case PosetGeneratorEvent:
		call := source.Call
		action, exists := findAction(rangeInterface, call.Name)
		if !exists {
			if len(findFunctions(rangeInterface, call.Name)) != 0 {
				return compiledMapGenerator{}, typeError(call.Position,
					"map range function call %q is outside the current action-event poset-generator subset",
					call.Name)
			}
			return compiledMapGenerator{}, typeError(call.Position,
				"map range action %q is not declared by interface %q",
				call.Name, rangeInterface.Name)
		}
		actuals, err := compileCallActualExpressions(call, bindings, nil)
		if err != nil {
			return compiledMapGenerator{}, err
		}
		parameters, matched, reason, err := associateCallActuals(call, action.Parameters, actuals, false)
		if err != nil {
			return compiledMapGenerator{}, err
		}
		if !matched {
			return compiledMapGenerator{}, typeError(call.Position,
				"map range action %q arguments do not match: %s", action.Name, reason)
		}
		id := fmt.Sprintf("event:%06d", *nextID)
		(*nextID)++
		return compiledMapGenerator{
			outputs: []arch.RuleOutput{arch.RuleEvent(id, action.Name, parameters...)},
			roots:   []string{id}, maxima: []string{id},
		}, nil
	case PosetGeneratorBinary:
		if source.Left == nil || source.Right == nil {
			return compiledMapGenerator{}, typeError(source.Position, "map poset generator has an incomplete binary operator")
		}
		if source.Operator != "->" && source.Operator != "|>" && source.Operator != "||" {
			return compiledMapGenerator{}, typeError(source.Position,
				"map poset-generator operator %q is published restricted-pattern syntax but is outside the current finite sequence/immediate-sequence/independence/join/disjunction subset",
				source.Operator)
		}
		left, err := compileSourceMapGeneratorNode(*source.Left, rangeInterface, bindings, nextID)
		if err != nil {
			return compiledMapGenerator{}, err
		}
		right, err := compileSourceMapGeneratorNode(*source.Right, rangeInterface, bindings, nextID)
		if err != nil {
			return compiledMapGenerator{}, err
		}
		result := compiledMapGenerator{outputs: append(left.outputs, right.outputs...)}
		if source.Operator == "||" {
			result.roots = canonicalGeneratorIDs(append(left.roots, right.roots...))
			result.maxima = canonicalGeneratorIDs(append(left.maxima, right.maxima...))
			return result, nil
		}
		causes := canonicalGeneratorIDs(left.maxima)
		roots := make(map[string]bool, len(right.roots))
		for _, id := range right.roots {
			roots[id] = true
		}
		for index := range result.outputs {
			if roots[result.outputs[index].ID] {
				result.outputs[index] = result.outputs[index].After(causes...)
			}
		}
		result.roots = canonicalGeneratorIDs(left.roots)
		result.maxima = canonicalGeneratorIDs(right.maxima)
		return result, nil
	case PosetGeneratorIteration:
		return compiledMapGenerator{}, typeError(source.Position,
			"map poset-generator iteration was not normalized before lowering")
	default:
		return compiledMapGenerator{}, typeError(source.Position,
			"unsupported map poset-generator node kind %q", source.Kind)
	}
}

func canonicalGeneratorIDs(source []string) []string {
	result := append([]string(nil), source...)
	sort.Strings(result)
	write := 0
	for _, id := range result {
		if write != 0 && result[write-1] == id {
			continue
		}
		result[write] = id
		write++
	}
	return result[:write]
}

func canonicalMapPlaceholders(source []ParameterDecl) []ParameterDecl {
	result := append([]ParameterDecl(nil), source...)
	for index := range result {
		result[index].Name = folded(result[index].Name)
	}
	return result
}

func compileSourceMapPattern(
	declaration InterfaceDecl,
	source BehaviorPatternDecl,
	placeholders []ParameterDecl,
	bindings map[string]behaviorBinding,
	bound map[string]bool,
) (pattern.Pattern, error) {
	compiled, err := compileSourcePattern(source, bindings, bound, "map", func(event BehaviorEventDecl) (ActionDecl, string, error) {
		if event.Component != "" {
			return ActionDecl{}, "", typeError(event.Position,
				"map type-expression domain trigger action %q cannot be component-qualified in the current exact-domain subset",
				event.Component+"."+event.Name)
		}
		if event.Attribute != "" {
			return ActionDecl{}, "", typeError(event.Position,
				"map function attribute event %s'%s is outside the current action-only source-map subset",
				event.Name, event.Attribute)
		}
		action, exists, err := findPatternAction(declaration, event)
		if err != nil {
			return ActionDecl{}, "", typeError(event.Position, "map: %v", err)
		}
		if !exists {
			return ActionDecl{}, "", typeError(event.Position,
				"map domain action %q is not declared by interface %q", event.Name, declaration.Name)
		}
		if action.Mode == ActionPrivate {
			return ActionDecl{}, "", typeError(event.Position,
				"map type-expression domain cannot observe private action %q", action.Name)
		}
		return action, "", nil
	})
	if err != nil {
		return nil, err
	}
	return compileUniversalQualifications(compiled, placeholders, bound, "map")
}

func mapRuleSemanticKey(rule MapRuleDecl) string {
	key := behaviorRuleSemanticKey(BehaviorRuleDecl{
		Position: rule.Position, Placeholders: rule.Placeholders,
		Trigger: rule.Trigger, Guard: rule.Guard, Connector: rule.Connector,
	})
	if rule.Generator == nil {
		return key + ":generator:null"
	}
	normalized, err := normalizeMapPosetGenerator(*rule.Generator)
	if err != nil {
		return key + ":generator:invalid"
	}
	if normalized.Kind == PosetGeneratorEmpty {
		return key + ":generator:null"
	}
	return key + ":generator:" + mapPosetGeneratorSemanticKey(normalized)
}

func normalizeMapPosetGenerator(source PosetGeneratorDecl) (PosetGeneratorDecl, error) {
	if source.Kind == PosetGeneratorIteration {
		return normalizeMapPosetGeneratorIteration(source)
	}
	if source.Kind != PosetGeneratorBinary {
		return source, nil
	}
	if source.Left == nil || source.Right == nil {
		return PosetGeneratorDecl{}, typeError(source.Position, "map poset generator has an incomplete binary operator")
	}
	left, err := normalizeMapPosetGenerator(*source.Left)
	if err != nil {
		return PosetGeneratorDecl{}, err
	}
	right, err := normalizeMapPosetGenerator(*source.Right)
	if err != nil {
		return PosetGeneratorDecl{}, err
	}
	operator := strings.ToLower(source.Operator)
	if operator == "->" || operator == "|>" || operator == "||" {
		if left.Kind == PosetGeneratorEmpty {
			return right, nil
		}
		if right.Kind == PosetGeneratorEmpty {
			return left, nil
		}
	}
	if operator == "~" && mapPosetGeneratorSemanticKey(left) > mapPosetGeneratorSemanticKey(right) {
		left, right = right, left
	}
	if operator != "->" && operator != "||" && operator != "or" {
		leftCopy, rightCopy := left, right
		return PosetGeneratorDecl{
			Position: source.Position, Kind: PosetGeneratorBinary, Operator: operator,
			Left: &leftCopy, Right: &rightCopy,
		}, nil
	}
	children := make([]PosetGeneratorDecl, 0, 2)
	var collect func(PosetGeneratorDecl)
	collect = func(child PosetGeneratorDecl) {
		if child.Kind == PosetGeneratorBinary && strings.EqualFold(child.Operator, operator) {
			collect(*child.Left)
			collect(*child.Right)
			return
		}
		children = append(children, child)
	}
	collect(left)
	collect(right)
	if operator == "||" || operator == "or" {
		sort.SliceStable(children, func(i, j int) bool {
			return mapPosetGeneratorSemanticKey(children[i]) < mapPosetGeneratorSemanticKey(children[j])
		})
	}
	if operator == "or" {
		write := 0
		for _, child := range children {
			if write != 0 && mapPosetGeneratorSemanticKey(children[write-1]) == mapPosetGeneratorSemanticKey(child) {
				continue
			}
			children[write] = child
			write++
		}
		children = children[:write]
		if len(children) == 1 {
			return children[0], nil
		}
	}
	result := children[0]
	for _, child := range children[1:] {
		leftCopy, rightCopy := result, child
		result = PosetGeneratorDecl{
			Position: source.Position, Kind: PosetGeneratorBinary, Operator: operator,
			Left: &leftCopy, Right: &rightCopy,
		}
	}
	return result, nil
}

func normalizeMapPosetGeneratorIteration(source PosetGeneratorDecl) (PosetGeneratorDecl, error) {
	if source.Inner == nil {
		return PosetGeneratorDecl{}, typeError(source.Position,
			"map poset-generator iteration has a missing operand")
	}
	cardinality, err := mapPosetGeneratorIterationCardinality(source)
	if err != nil {
		return PosetGeneratorDecl{}, err
	}
	if cardinality == 0 {
		return PosetGeneratorDecl{Position: source.Position, Kind: PosetGeneratorEmpty}, nil
	}

	instances := make([]PosetGeneratorDecl, 0, cardinality)
	totalOccurrences := uint64(0)
	for offset := uint64(0); offset < cardinality; offset++ {
		instance := cloneMapPosetGenerator(*source.Inner)
		if source.Iterator != "" {
			instance = substituteMapPosetGeneratorIterator(
				instance, source.Iterator, source.First+int64(offset),
			)
		}
		normalized, err := normalizeMapPosetGenerator(instance)
		if err != nil {
			return PosetGeneratorDecl{}, err
		}
		occurrences := mapPosetGeneratorOccurrenceCount(normalized)
		if occurrences > maxSourceMapIterationOccurrences-totalOccurrences {
			return PosetGeneratorDecl{}, typeError(source.Position,
				"map poset-generator iteration expands to more than the deterministic bound of %d event occurrences",
				maxSourceMapIterationOccurrences)
		}
		totalOccurrences += occurrences
		instances = append(instances, normalized)
	}

	result := instances[0]
	operator := strings.ToLower(source.Relation)
	for _, instance := range instances[1:] {
		leftCopy, rightCopy := result, instance
		result = PosetGeneratorDecl{
			Position: source.Position, Kind: PosetGeneratorBinary, Operator: operator,
			Left: &leftCopy, Right: &rightCopy,
		}
	}
	return normalizeMapPosetGenerator(result)
}

func mapPosetGeneratorIterationCardinality(source PosetGeneratorDecl) (uint64, error) {
	relation := strings.ToLower(source.Relation)
	if relation != "->" && relation != "|>" && relation != "||" && relation != "or" {
		return 0, typeError(source.Position,
			"map poset-generator iteration relation %q is outside the finite sequence/immediate-sequence/independence/disjunction subset",
			source.Relation)
	}
	if source.Iterator != "" {
		cardinality, valid := sourceNamedRangeCardinality(source.First, source.Last)
		if !valid {
			return 0, typeError(source.Position,
				"named map poset-generator iterator %s range %d..%d must contain at most %d values",
				source.Iterator, source.First, source.Last, pattern.MaxNamedRangeIterationCardinality)
		}
		return cardinality, nil
	}
	if source.Minimum < 0 {
		return 0, typeError(source.Position,
			"map poset-generator iteration has invalid cardinality %d..%d", source.Minimum, source.Maximum)
	}
	if source.Maximum < 0 {
		return 0, typeError(source.Position,
			"unbounded map poset-generator iteration is outside the finite deterministic source-map subset")
	}
	if source.Minimum != source.Maximum {
		return 0, typeError(source.Position,
			"map poset-generator iteration requires one exact finite cardinality, got %d..%d",
			source.Minimum, source.Maximum)
	}
	if uint64(source.Maximum) > pattern.MaxNamedRangeIterationCardinality {
		return 0, typeError(source.Position,
			"map poset-generator iteration cardinality %d exceeds the deterministic bound of %d",
			source.Maximum, pattern.MaxNamedRangeIterationCardinality)
	}
	return uint64(source.Maximum), nil
}

func mapPosetGeneratorOccurrenceCount(source PosetGeneratorDecl) uint64 {
	switch source.Kind {
	case PosetGeneratorEvent:
		return 1
	case PosetGeneratorBinary:
		if source.Left == nil || source.Right == nil {
			return 0
		}
		return mapPosetGeneratorOccurrenceCount(*source.Left) + mapPosetGeneratorOccurrenceCount(*source.Right)
	default:
		return 0
	}
}

func cloneMapPosetGenerator(source PosetGeneratorDecl) PosetGeneratorDecl {
	result := source
	result.Call.Arguments = make([]ExpressionDecl, len(source.Call.Arguments))
	for index, argument := range source.Call.Arguments {
		result.Call.Arguments[index] = cloneExpressionDeclaration(argument)
	}
	result.Call.ArgumentFormals = append([]string(nil), source.Call.ArgumentFormals...)
	if source.Left != nil {
		left := cloneMapPosetGenerator(*source.Left)
		result.Left = &left
	}
	if source.Right != nil {
		right := cloneMapPosetGenerator(*source.Right)
		result.Right = &right
	}
	if source.Inner != nil {
		inner := cloneMapPosetGenerator(*source.Inner)
		result.Inner = &inner
	}
	return result
}

func substituteMapPosetGeneratorIterator(
	source PosetGeneratorDecl,
	iterator string,
	value int64,
) PosetGeneratorDecl {
	result := cloneMapPosetGenerator(source)
	switch result.Kind {
	case PosetGeneratorEvent:
		for index, argument := range result.Call.Arguments {
			result.Call.Arguments[index] = substituteMapGeneratorIteratorExpression(argument, iterator, value)
		}
	case PosetGeneratorBinary:
		if result.Left != nil {
			left := substituteMapPosetGeneratorIterator(*result.Left, iterator, value)
			result.Left = &left
		}
		if result.Right != nil {
			right := substituteMapPosetGeneratorIterator(*result.Right, iterator, value)
			result.Right = &right
		}
	case PosetGeneratorIteration:
		if result.Iterator != "" && folded(result.Iterator) == folded(iterator) {
			return result
		}
		if result.Inner != nil {
			inner := substituteMapPosetGeneratorIterator(*result.Inner, iterator, value)
			result.Inner = &inner
		}
	}
	return result
}

func substituteMapGeneratorIteratorExpression(
	source ExpressionDecl,
	iterator string,
	value int64,
) ExpressionDecl {
	if source.Kind == ExpressionName && folded(source.Name) == folded(iterator) {
		return ExpressionDecl{Position: source.Position, Kind: ExpressionInteger, Integer: value}
	}
	result := cloneExpressionDeclaration(source)
	if result.Left != nil {
		left := substituteMapGeneratorIteratorExpression(*result.Left, iterator, value)
		result.Left = &left
	}
	if result.Right != nil {
		right := substituteMapGeneratorIteratorExpression(*result.Right, iterator, value)
		result.Right = &right
	}
	for index, argument := range result.Arguments {
		result.Arguments[index] = substituteMapGeneratorIteratorExpression(argument, iterator, value)
	}
	for index, field := range result.RecordFields {
		result.RecordFields[index].Value = substituteMapGeneratorIteratorExpression(field.Value, iterator, value)
	}
	return result
}

func mapPosetGeneratorSemanticKey(source PosetGeneratorDecl) string {
	switch source.Kind {
	case PosetGeneratorEmpty:
		return "empty"
	case PosetGeneratorEvent:
		return "event:" + folded(source.Call.Name) + "(" + callArgumentsSemanticKey(source.Call) + ")"
	case PosetGeneratorBinary:
		if source.Left == nil || source.Right == nil {
			return "invalid-binary-generator"
		}
		return "(" + mapPosetGeneratorSemanticKey(*source.Left) + strings.ToLower(source.Operator) +
			mapPosetGeneratorSemanticKey(*source.Right) + ")"
	case PosetGeneratorIteration:
		if source.Inner == nil {
			return "invalid-iteration-generator"
		}
		if source.Iterator != "" {
			return fmt.Sprintf("iterate:%s:Integer:%d:%d:%s:{%s}", folded(source.Iterator),
				source.First, source.Last, strings.ToLower(source.Relation),
				mapPosetGeneratorSemanticKey(*source.Inner))
		}
		return fmt.Sprintf("iterate:%d:%d:%s:{%s}", source.Minimum, source.Maximum,
			strings.ToLower(source.Relation), mapPosetGeneratorSemanticKey(*source.Inner))
	default:
		return "invalid-generator:" + string(source.Kind)
	}
}

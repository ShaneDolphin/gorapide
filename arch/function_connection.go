package arch

import (
	"fmt"
	"sort"
	"strings"
)

// FunctionConnection aliases a required function of From to a type-compatible
// provided function of To. The deterministic subset supports one-to-one basic
// routes at explicit architecture or same-component module scope.
type FunctionConnection struct {
	ID                   string
	Scope                ConnectionScope
	ArchitectureInstance string
	From                 string
	Required             string
	To                   string
	Provided             string
}

// FunctionConnectionBuilder constructs one statically identified function
// connection.
type FunctionConnectionBuilder struct {
	connection FunctionConnection
}

// ConnectFunction begins a required-to-provided synchronous connection.
func ConnectFunction(from, required, to, provided string) *FunctionConnectionBuilder {
	return &FunctionConnectionBuilder{connection: FunctionConnection{
		From: from, Required: required, To: to, Provided: provided,
	}}
}

// IdentifiedBy assigns stable semantic identity to the connection.
func (builder *FunctionConnectionBuilder) IdentifiedBy(id string) *FunctionConnectionBuilder {
	builder.connection.ID = id
	return builder
}

// WithinArchitecture records the static architecture scope that owns this
// synchronous alias. An empty value denotes the root architecture.
func (builder *FunctionConnectionBuilder) WithinArchitecture(instanceID string) *FunctionConnectionBuilder {
	if builder != nil {
		builder.connection.ArchitectureInstance = instanceID
	}
	return builder
}

// WithinModule marks this alias as a constituent of the generated module rather
// than architecture wiring. Both endpoints must then name that same module
// instance, and generator reevaluation may instantiate the route per allocation.
func (builder *FunctionConnectionBuilder) WithinModule() *FunctionConnectionBuilder {
	if builder != nil {
		builder.connection.Scope = ModuleConnectionScope
	}
	return builder
}

// Build returns an isolated declaration snapshot.
func (builder *FunctionConnectionBuilder) Build() *FunctionConnection {
	if builder == nil {
		return nil
	}
	return copyFunctionConnection(&builder.connection)
}

func copyFunctionConnection(connection *FunctionConnection) *FunctionConnection {
	if connection == nil {
		return nil
	}
	copy := *connection
	return &copy
}

type canonicalFunctionConnection struct {
	ID                   string `json:"id"`
	Scope                int    `json:"scope"`
	ArchitectureInstance string `json:"architecture_instance,omitempty"`
	From                 string `json:"from"`
	SourceFunction       string `json:"source_function"`
	SourceSignature      string `json:"source_signature"`
	To                   string `json:"to"`
	TargetFunction       string `json:"target_function"`
	TargetSignature      string `json:"target_signature"`
}

type functionRouteNode struct {
	endpoint  string
	function  FunctionDecl
	signature string
}

func (node functionRouteNode) key() string {
	return node.endpoint + "\x00" + node.signature
}

type functionRouteEdge struct {
	id     string
	scope  ConnectionScope
	source functionRouteNode
	target functionRouteNode
}

func buildFunctionRoutes(
	connections []*FunctionConnection,
	returnInterface *InterfaceDecl,
	components map[string]*Component,
	architectureInstances map[string]ArchitectureInstanceDeclaration,
	componentArchitectures map[string]string,
	functions, callables map[string]map[string]*FunctionImplementation,
) ([]canonicalFunctionConnection, error) {
	ordered := make([]*FunctionConnection, len(connections))
	for index, connection := range connections {
		ordered[index] = copyFunctionConnection(connection)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i] == nil {
			return ordered[j] != nil
		}
		if ordered[j] == nil {
			return false
		}
		return ordered[i].ID < ordered[j].ID
	})
	seenIDs := make(map[string]bool, len(ordered))
	routed := make(map[string]bool)
	edges := make([]functionRouteEdge, 0, len(ordered))
	edgeBySource := make(map[string]functionRouteEdge, len(ordered))
	result := make([]canonicalFunctionConnection, 0)
	for _, connection := range ordered {
		if connection == nil || connection.ID == "" || connection.From == "" ||
			connection.Required == "" || connection.To == "" || connection.Provided == "" {
			return nil, fmt.Errorf("function connection has an empty identity, endpoint, or function name")
		}
		if seenIDs[connection.ID] {
			return nil, fmt.Errorf("duplicate function connection ID %q", connection.ID)
		}
		seenIDs[connection.ID] = true
		if connection.Scope != ArchitectureConnectionScope && connection.Scope != ModuleConnectionScope {
			return nil, fmt.Errorf("function connection %q has invalid scope %d", connection.ID, connection.Scope)
		}
		owner := connection.ArchitectureInstance
		if owner == "" {
			owner = ArchitectureInterfaceID
		}
		if owner != ArchitectureInterfaceID {
			if _, exists := architectureInstances[owner]; !exists {
				return nil, fmt.Errorf("function connection %q references missing architecture instance %q", connection.ID, owner)
			}
		}
		if connection.Scope == ModuleConnectionScope {
			if connection.From != connection.To || components[connection.From] == nil {
				return nil, fmt.Errorf("module function connection %q must use one identical component source and target", connection.ID)
			}
			if connection.From == architectureBoundaryID(owner) {
				return nil, fmt.Errorf("module function connection %q cannot use its enclosing architecture interface", connection.ID)
			}
		}
		if !connectionEndpointVisible(owner, connection.From, architectureInstances, componentArchitectures) {
			return nil, fmt.Errorf("function connection %q source %q is not visible in architecture %q", connection.ID, connection.From, owner)
		}
		if !connectionEndpointVisible(owner, connection.To, architectureInstances, componentArchitectures) {
			return nil, fmt.Errorf("function connection %q target %q is not visible in architecture %q", connection.ID, connection.To, owner)
		}
		sourceInterface := endpointInterface(connection.From, returnInterface, architectureInstances, components)
		targetInterface := endpointInterface(connection.To, returnInterface, architectureInstances, components)
		if sourceInterface == nil || targetInterface == nil {
			return nil, fmt.Errorf("function connection %q references a missing endpoint", connection.ID)
		}
		sourceKind := RequiresFunction
		if connection.From == architectureBoundaryID(owner) {
			sourceKind = ProvidesFunction
		}
		targetKind := ProvidesFunction
		if connection.To == architectureBoundaryID(owner) {
			targetKind = RequiresFunction
		}
		sources := directFunctionsNamed(sourceInterface, sourceKind, connection.Required)
		targets := directFunctionsNamed(targetInterface, targetKind, connection.Provided)
		if len(sources) == 0 {
			return nil, fmt.Errorf("function connection %q source %s.%s is not a direct %s function",
				connection.ID, connection.From, connection.Required, functionConnectionRole(sourceKind))
		}
		if len(targets) == 0 {
			return nil, fmt.Errorf("function connection %q target %s.%s is not a direct %s function",
				connection.ID, connection.To, connection.Provided, functionConnectionRole(targetKind))
		}
		for _, source := range sources {
			sourceCanonical, sourceKey, err := canonicalizeFunction(source)
			if err != nil {
				return nil, err
			}
			matches := make([]FunctionDecl, 0, len(targets))
			for _, target := range targets {
				if functionConnectionSignaturesCompatible(source, target) {
					matches = append(matches, target)
				}
			}
			if len(matches) != 1 {
				return nil, fmt.Errorf("function connection %q source signature %q has %d compatible %s signatures, want 1",
					connection.ID, sourceKey, len(matches), functionConnectionRole(targetKind))
			}
			target := matches[0]
			targetCanonical, targetKey, err := canonicalizeFunction(target)
			if err != nil {
				return nil, err
			}
			routeIdentity := connection.From + "\x00" + sourceKey
			if routed[routeIdentity] {
				return nil, fmt.Errorf("required function route %q is connected more than once", routeIdentity)
			}
			routed[routeIdentity] = true
			edge := functionRouteEdge{
				id:     connection.ID,
				scope:  connection.Scope,
				source: functionRouteNode{endpoint: connection.From, function: source, signature: sourceKey},
				target: functionRouteNode{endpoint: connection.To, function: target, signature: targetKey},
			}
			edges = append(edges, edge)
			edgeBySource[edge.source.key()] = edge
			result = append(result, canonicalFunctionConnection{
				ID: connection.ID, Scope: int(connection.Scope), From: connection.From, SourceFunction: source.Name,
				SourceSignature: functionDeclKey(sourceCanonical),
				To:              connection.To, TargetFunction: target.Name,
				TargetSignature: functionDeclKey(targetCanonical),
			})
			if owner != ArchitectureInterfaceID {
				result[len(result)-1].ArchitectureInstance = owner
			}
		}
	}
	for _, edge := range edges {
		if _, _, err := resolveFunctionRoute(edge, edgeBySource, components); err != nil {
			return nil, err
		}
	}
	for _, edge := range edges {
		componentRequirement := components[edge.source.endpoint] != nil &&
			edge.source.function.Kind == RequiresFunction
		architectureProvision := components[edge.source.endpoint] == nil &&
			edge.source.function.Kind == ProvidesFunction &&
			(edge.source.endpoint == ArchitectureInterfaceID ||
				architectureInstances[edge.source.endpoint].ID != "")
		if !componentRequirement && !architectureProvision {
			continue
		}
		path, terminal, err := resolveFunctionRoute(edge, edgeBySource, components)
		if err != nil {
			return nil, err
		}
		implementation := functions[terminal.endpoint][terminal.signature]
		if implementation == nil {
			continue
		}
		route := copyFunctionImplementation(implementation)
		route.Name = edge.source.function.Name
		route.Params = append([]ParamDecl(nil), edge.source.function.Params...)
		route.ReturnType = edge.source.function.ReturnType
		var routeKey strings.Builder
		routeKey.WriteString("function-connection:")
		connectionIDs := make([]string, 0, len(path)-1)
		for index := 1; index < len(path); index++ {
			segment := edgeBySource[path[index-1].key()]
			appendFunctionRouteKeyPart(&routeKey, segment.id)
			appendFunctionRouteKeyPart(&routeKey, path[index-1].signature)
			appendFunctionRouteKeyPart(&routeKey, path[index].signature)
			connectionIDs = append(connectionIDs, segment.id)
			route.connectionScopes = append(route.connectionScopes, segment.scope)
			route.routeAliases = append(route.routeAliases, functionRouteAlias{
				ComponentID: path[index].endpoint,
				Name:        path[index].function.Name,
				Params:      append([]ParamDecl(nil), path[index].function.Params...),
			})
		}
		route.key = routeKey.String()
		route.targetComponent = terminal.endpoint
		route.targetName = terminal.function.Name
		route.targetParams = append([]ParamDecl(nil), terminal.function.Params...)
		route.connectionID = strings.Join(connectionIDs, "|")
		if callables[edge.source.endpoint] == nil {
			callables[edge.source.endpoint] = make(map[string]*FunctionImplementation)
		}
		callables[edge.source.endpoint][route.key] = route
	}
	sort.Slice(result, func(i, j int) bool {
		left := result[i].ID + "\x00" + result[i].SourceSignature + "\x00" + result[i].TargetSignature
		right := result[j].ID + "\x00" + result[j].SourceSignature + "\x00" + result[j].TargetSignature
		return left < right
	})
	return result, nil
}

func appendFunctionRouteKeyPart(builder *strings.Builder, value string) {
	fmt.Fprintf(builder, "%d:", len(value))
	builder.WriteString(value)
	builder.WriteByte(';')
}

func functionConnectionRole(kind FunctionKind) string {
	if kind == RequiresFunction {
		return "required"
	}
	return "provided"
}

func resolveFunctionRoute(
	first functionRouteEdge,
	edgeBySource map[string]functionRouteEdge,
	components map[string]*Component,
) ([]functionRouteNode, functionRouteNode, error) {
	path := []functionRouteNode{first.source, first.target}
	seen := map[string]bool{first.source.key(): true}
	current := first.target
	for {
		if seen[current.key()] {
			return nil, functionRouteNode{}, fmt.Errorf("function connection route from %s.%s contains a boundary cycle at %s.%s",
				first.source.endpoint, first.source.function.Name, current.endpoint, current.function.Name)
		}
		seen[current.key()] = true
		if components[current.endpoint] != nil && current.function.Kind == ProvidesFunction {
			return path, current, nil
		}
		next, exists := edgeBySource[current.key()]
		if !exists {
			return path, current, nil
		}
		path = append(path, next.target)
		current = next.target
	}
}

func directFunctionsNamed(iface *InterfaceDecl, kind FunctionKind, name string) []FunctionDecl {
	if iface == nil {
		return nil
	}
	result := make([]FunctionDecl, 0)
	for _, function := range iface.Functions {
		if function.Kind == kind && function.Name == name {
			result = append(result, function)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		_, left, _ := canonicalizeFunction(result[i])
		_, right, _ := canonicalizeFunction(result[j])
		return left < right
	})
	return result
}

func functionConnectionSignaturesCompatible(required, provided FunctionDecl) bool {
	if !functionConnectionReturnCompatible(required.ReturnType, provided.ReturnType) {
		return false
	}
	shared := len(required.Params)
	if len(provided.Params) < shared {
		shared = len(provided.Params)
	}
	for index := 0; index < shared; index++ {
		// Function parameters are contravariant: every value admitted by the
		// required/caller formal must also be admitted by the provider formal.
		if !predefinedTypeAssignable(required.Params[index].Type, provided.Params[index].Type) {
			return false
		}
	}
	// Type LRM Section 5.3 tests the provided/source function as the
	// subtype. Its formals beyond the required/target arity must therefore
	// have defaults. Extra required formals are boundary-local call data and
	// are omitted from the provider observation.
	for _, parameter := range provided.Params[shared:] {
		if parameter.Default == nil {
			return false
		}
	}
	return true
}

func functionConnectionReturnCompatible(required, provided string) bool {
	if required == "" || provided == "" {
		return required == provided
	}
	// Function results are covariant: the provider result must be assignable
	// to the result promised by the required/caller view.
	return predefinedTypeAssignable(provided, required)
}

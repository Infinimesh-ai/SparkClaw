package capability

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

const (
	DefaultCatalogRevision = "2026-07-20.v4"
	RootID                 = app.CapabilityID("capability")
)

type NodeKind string

const (
	NodeBranch NodeKind = "branch"
	NodeLeaf   NodeKind = "leaf"
)

type Node struct {
	ID          app.CapabilityID         `json:"id"`
	ParentID    app.CapabilityID         `json:"parent_id,omitempty"`
	Kind        NodeKind                 `json:"kind"`
	Description string                   `json:"description"`
	Workflow    *app.WorkflowContractRef `json:"workflow,omitempty"`
	Route       *RouteContract           `json:"route,omitempty"`
}

type RouteContract struct {
	Operations      []app.RouteOperation `json:"operations"`
	FactScopes      []app.RouteFactScope `json:"fact_scopes,omitempty"`
	TargetKinds     []string             `json:"target_kinds,omitempty"`
	RequireQuery    bool                 `json:"require_query,omitempty"`
	RequireLocation bool                 `json:"require_location,omitempty"`
	RequireTarget   bool                 `json:"require_target,omitempty"`
	RequiredFacts   []string             `json:"required_facts,omitempty"`
}

type RouteOption struct {
	Path        []app.CapabilityID `json:"path"`
	Description string             `json:"description"`
	Contract    RouteContract      `json:"contract"`
}

type Catalog struct {
	revision string
	nodes    map[app.CapabilityID]Node
	children map[app.CapabilityID][]app.CapabilityID
}

func NewCatalog(revision string, nodes []Node) (Catalog, error) {
	revision = strings.TrimSpace(revision)
	if revision == "" {
		return Catalog{}, errors.New("capability catalog revision is required")
	}
	catalog := Catalog{
		revision: revision,
		nodes:    make(map[app.CapabilityID]Node, len(nodes)),
		children: make(map[app.CapabilityID][]app.CapabilityID),
	}
	for _, node := range nodes {
		if err := validateNodeShape(node); err != nil {
			return Catalog{}, err
		}
		if _, exists := catalog.nodes[node.ID]; exists {
			return Catalog{}, fmt.Errorf("capability %q is registered more than once", node.ID)
		}
		catalog.nodes[node.ID] = cloneNode(node)
	}
	root, ok := catalog.nodes[RootID]
	if !ok {
		return Catalog{}, fmt.Errorf("capability catalog root %q is missing", RootID)
	}
	if root.ParentID != "" || root.Kind != NodeBranch {
		return Catalog{}, errors.New("capability catalog root must be a parentless branch")
	}
	for _, node := range catalog.nodes {
		if node.ID == RootID {
			continue
		}
		parent, ok := catalog.nodes[node.ParentID]
		if !ok {
			return Catalog{}, fmt.Errorf("capability %q references unknown parent %q", node.ID, node.ParentID)
		}
		if parent.Kind != NodeBranch {
			return Catalog{}, fmt.Errorf("capability %q cannot use leaf %q as its parent", node.ID, node.ParentID)
		}
		catalog.children[node.ParentID] = append(catalog.children[node.ParentID], node.ID)
	}
	for parentID := range catalog.children {
		slices.Sort(catalog.children[parentID])
	}
	for _, node := range catalog.nodes {
		if node.Kind == NodeBranch && len(catalog.children[node.ID]) == 0 {
			return Catalog{}, fmt.Errorf("capability branch %q has no children", node.ID)
		}
		if err := catalog.validateConnected(node.ID); err != nil {
			return Catalog{}, err
		}
	}
	return catalog, nil
}

func DefaultCatalog() (Catalog, error) {
	branch := func(id, parent, description string) Node {
		return Node{ID: app.CapabilityID(id), ParentID: app.CapabilityID(parent), Kind: NodeBranch, Description: description}
	}
	leaf := func(id, parent, description string, route RouteContract) Node {
		workflow := app.WorkflowContractRef{ID: app.WorkflowID(id), Revision: 1}
		return Node{ID: app.CapabilityID(id), ParentID: app.CapabilityID(parent), Kind: NodeLeaf, Description: description, Workflow: &workflow, Route: &route}
	}
	return NewCatalog(DefaultCatalogRevision, []Node{
		branch(string(RootID), "", "Registered user-visible product capabilities."),
		branch("browser", string(RootID), "Use current Internet facts, a single-location weather card, or a managed browser session."),
		leaf(string(app.CapabilityBrowserInternetSearch), "browser", "Retrieve read-only facts that depend on current Internet state, including gold prices, exchange rates, stock or index quotes, immediate news, current sports results, schedules, and weather alerts, news, or comparisons. Stable common knowledge that does not depend on current external state is not Internet search.", RouteContract{
			Operations: []app.RouteOperation{app.RouteOperationSearch}, FactScopes: []app.RouteFactScope{app.RouteFactScopeCurrentInternet}, RequireQuery: true,
		}),
		leaf(string(app.CapabilityBrowserWeather), "browser", "Render one weather card for a single explicit location's current conditions or short forecast. Weather alerts, news, historical research, and multi-location comparisons belong to Internet search.", RouteContract{
			Operations: []app.RouteOperation{app.RouteOperationRender}, FactScopes: []app.RouteFactScope{app.RouteFactScopeWeatherSnapshot}, RequireLocation: true,
		}),
		leaf(string(app.CapabilityBrowserAutomation), "browser", "Open or focus an explicitly known URL in the managed browser.", RouteContract{
			Operations: []app.RouteOperation{app.RouteOperationOpen}, TargetKinds: []string{"url"}, RequireTarget: true, RequiredFacts: []string{"url"},
		}),
		branch("document", string(RootID), "Read or edit one explicitly identified governed document."),
		leaf(string(app.CapabilityDocumentRead), "document", "Read one explicitly identified governed file by its detected type.", RouteContract{
			Operations: []app.RouteOperation{app.RouteOperationRead}, TargetKinds: []string{"workspace_path"}, RequireTarget: true, RequiredFacts: []string{"path"},
		}),
		leaf(string(app.CapabilityDocumentEdit), "document", "Edit a copy of one explicitly identified governed document.", RouteContract{
			Operations: []app.RouteOperation{app.RouteOperationEdit, app.RouteOperationTransform}, TargetKinds: []string{"workspace_path"}, RequireTarget: true, RequiredFacts: []string{"path"},
		}),
	})
}

func MustDefaultCatalog() Catalog {
	catalog, err := DefaultCatalog()
	if err != nil {
		panic(err)
	}
	return catalog
}

func (c Catalog) Revision() string {
	return c.revision
}

func (c Catalog) Node(id app.CapabilityID) (Node, bool) {
	node, ok := c.nodes[id]
	return cloneNode(node), ok
}

func (c Catalog) Children(id app.CapabilityID) []Node {
	ids := c.children[id]
	children := make([]Node, 0, len(ids))
	for _, childID := range ids {
		children = append(children, cloneNode(c.nodes[childID]))
	}
	return children
}

func (c Catalog) RouteOptions() []RouteOption {
	options := make([]RouteOption, 0)
	for _, node := range c.nodes {
		if node.Kind != NodeLeaf || node.Route == nil {
			continue
		}
		path, err := c.PathTo(node.ID)
		if err != nil {
			continue
		}
		options = append(options, RouteOption{Path: path, Description: node.Description, Contract: cloneRouteContract(*node.Route)})
	}
	slices.SortFunc(options, func(left, right RouteOption) int {
		return strings.Compare(pathKey(left.Path), pathKey(right.Path))
	})
	return options
}

func (c Catalog) PathTo(id app.CapabilityID) ([]app.CapabilityID, error) {
	node, ok := c.nodes[id]
	if !ok {
		return nil, fmt.Errorf("capability %q is not registered", id)
	}
	path := []app.CapabilityID{}
	for node.ID != RootID {
		path = append(path, node.ID)
		parent, ok := c.nodes[node.ParentID]
		if !ok {
			return nil, fmt.Errorf("capability %q is not connected to root %q", id, RootID)
		}
		node = parent
	}
	slices.Reverse(path)
	return path, nil
}

func (c Catalog) ResolveLeaf(path []app.CapabilityID) (Node, error) {
	node, err := c.resolvePath(path)
	if err != nil {
		return Node{}, err
	}
	if node.Kind != NodeLeaf {
		return Node{}, fmt.Errorf("capability path ends at branch %q", node.ID)
	}
	return cloneNode(node), nil
}

func (c Catalog) ValidateDecision(decision app.RouteDecision) error {
	if decision.SchemaVersion != app.RouteDecisionSchemaVersion {
		return fmt.Errorf("unsupported route decision schema version %d", decision.SchemaVersion)
	}
	if decision.CatalogRevision != c.revision {
		return fmt.Errorf("route decision catalog revision %q does not match %q", decision.CatalogRevision, c.revision)
	}
	if decision.Confidence < 0 || decision.Confidence > 1 {
		return fmt.Errorf("route confidence %.4f is outside [0,1]", decision.Confidence)
	}
	switch decision.Status {
	case app.RouteMatched:
		leaf, err := c.ResolveLeaf(decision.CapabilityPath)
		if err != nil {
			return err
		}
		return validateMatchedSlots(leaf, decision)
	case app.RouteClarify, app.RouteBlocked:
		if len(decision.CapabilityPath) == 0 {
			return nil
		}
		_, err := c.resolvePath(decision.CapabilityPath)
		return err
	case app.RouteUnmatched:
		if len(decision.CapabilityPath) != 0 {
			return errors.New("unmatched route decision cannot contain a capability path")
		}
		if !decision.Slots.Empty() || len(decision.Facts) != 0 {
			return errors.New("unmatched route decision cannot contain semantic slots or facts")
		}
		return nil
	default:
		return fmt.Errorf("unsupported route status %q", decision.Status)
	}
}

func validateMatchedSlots(leaf Node, decision app.RouteDecision) error {
	if leaf.Route == nil {
		return fmt.Errorf("capability %q has no typed slot contract", leaf.ID)
	}
	contract := leaf.Route
	slots := decision.Slots
	if !slices.Contains(contract.Operations, slots.Operation) {
		return fmt.Errorf("operation %q is not valid for capability %q", slots.Operation, leaf.ID)
	}
	if (slots.FactScope != "" || len(contract.FactScopes) > 0) && !slices.Contains(contract.FactScopes, slots.FactScope) {
		return fmt.Errorf("fact scope %q is not valid for capability %q", slots.FactScope, leaf.ID)
	}
	if slots.Location != "" && !contract.RequireLocation {
		return fmt.Errorf("location is not valid for capability %q", leaf.ID)
	}
	if slots.TargetKind != "" && !slices.Contains(contract.TargetKinds, slots.TargetKind) {
		return fmt.Errorf("target kind %q is not valid for capability %q", slots.TargetKind, leaf.ID)
	}
	if contract.RequireQuery && strings.TrimSpace(slots.Query) == "" {
		return fmt.Errorf("capability %q requires a query", leaf.ID)
	}
	if contract.RequireLocation && strings.TrimSpace(slots.Location) == "" {
		return fmt.Errorf("capability %q requires a location", leaf.ID)
	}
	if contract.RequireTarget && (strings.TrimSpace(slots.TargetKind) == "" || strings.TrimSpace(slots.TargetRef) == "") {
		return fmt.Errorf("capability %q requires a deterministic target", leaf.ID)
	}
	for _, fact := range contract.RequiredFacts {
		if strings.TrimSpace(decision.Facts[fact]) == "" {
			return fmt.Errorf("capability %q requires deterministic fact %q", leaf.ID, fact)
		}
	}
	return nil
}

func (c Catalog) resolvePath(path []app.CapabilityID) (Node, error) {
	if len(path) == 0 {
		return Node{}, errors.New("capability path is empty")
	}
	parentID := RootID
	for index, id := range path {
		node, ok := c.nodes[id]
		if !ok {
			return Node{}, fmt.Errorf("capability path contains unknown node %q", id)
		}
		if node.ParentID != parentID {
			return Node{}, fmt.Errorf("capability path edge %q -> %q is not registered", parentID, id)
		}
		if index < len(path)-1 && node.Kind != NodeBranch {
			return Node{}, fmt.Errorf("capability path continues after leaf %q", id)
		}
		parentID = id
	}
	return cloneNode(c.nodes[parentID]), nil
}

func validateNodeShape(node Node) error {
	if strings.TrimSpace(string(node.ID)) == "" {
		return errors.New("capability id is required")
	}
	if strings.TrimSpace(node.Description) == "" {
		return fmt.Errorf("capability %q description is required", node.ID)
	}
	switch node.Kind {
	case NodeBranch:
		if node.Workflow != nil || node.Route != nil {
			return fmt.Errorf("capability branch %q cannot select a workflow", node.ID)
		}
	case NodeLeaf:
		if node.ParentID == "" {
			return fmt.Errorf("capability leaf %q requires a parent", node.ID)
		}
		if node.Workflow == nil || strings.TrimSpace(string(node.Workflow.ID)) == "" || node.Workflow.Revision < 1 {
			return fmt.Errorf("capability leaf %q requires a versioned workflow contract", node.ID)
		}
		if node.Route == nil || len(node.Route.Operations) == 0 {
			return fmt.Errorf("capability leaf %q requires a typed route contract", node.ID)
		}
		if node.Route.RequireTarget && len(node.Route.TargetKinds) == 0 {
			return fmt.Errorf("capability leaf %q requires registered target kinds", node.ID)
		}
	default:
		return fmt.Errorf("capability %q has invalid kind %q", node.ID, node.Kind)
	}
	return nil
}

func (c Catalog) validateConnected(id app.CapabilityID) error {
	seen := map[app.CapabilityID]bool{}
	current := id
	for current != RootID {
		if seen[current] {
			return fmt.Errorf("capability catalog contains a cycle at %q", current)
		}
		seen[current] = true
		node, ok := c.nodes[current]
		if !ok || node.ParentID == "" {
			return fmt.Errorf("capability %q is not connected to root %q", id, RootID)
		}
		current = node.ParentID
	}
	return nil
}

func cloneNode(node Node) Node {
	if node.Workflow != nil {
		workflow := *node.Workflow
		node.Workflow = &workflow
	}
	if node.Route != nil {
		route := cloneRouteContract(*node.Route)
		node.Route = &route
	}
	return node
}

func cloneRouteContract(route RouteContract) RouteContract {
	route.Operations = append([]app.RouteOperation(nil), route.Operations...)
	route.FactScopes = append([]app.RouteFactScope(nil), route.FactScopes...)
	route.TargetKinds = append([]string(nil), route.TargetKinds...)
	route.RequiredFacts = append([]string(nil), route.RequiredFacts...)
	return route
}

func pathKey(path []app.CapabilityID) string {
	parts := make([]string, len(path))
	for index, id := range path {
		parts[index] = string(id)
	}
	return strings.Join(parts, "/")
}

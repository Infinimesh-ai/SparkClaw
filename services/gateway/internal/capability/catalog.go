package capability

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

const (
	DefaultCatalogRevision = "2026-07-16.v1"
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
	leaf := func(id, parent, description string) Node {
		workflow := app.WorkflowContractRef{ID: app.WorkflowID(id), Revision: 1}
		return Node{ID: app.CapabilityID(id), ParentID: app.CapabilityID(parent), Kind: NodeLeaf, Description: description, Workflow: &workflow}
	}
	return NewCatalog(DefaultCatalogRevision, []Node{
		branch(string(RootID), "", "Registered user-visible product capabilities."),
		branch("conversation", string(RootID), "Answer or discuss without a more specific product operation."),
		leaf("conversation.answer", "conversation", "Answer a user message."),
		branch("browser", string(RootID), "Work with public or interactive browser resources."),
		leaf("browser.search", "browser", "Search public information sources."),
		leaf("browser.automation", "browser", "Interact with pages and browser sessions."),
		branch("file", string(RootID), "Work with governed files in the user's workspace."),
		leaf("file.discover", "file", "Discover files and workspace structure."),
		leaf("file.read", "file", "Read or inspect file content."),
		leaf("file.create", "file", "Create a new file or document."),
		leaf("file.edit", "file", "Edit an existing file or document."),
		leaf("file.transform", "file", "Convert or transform file content."),
		leaf("file.delete", "file", "Delete a file subject to policy."),
		branch("message", string(RootID), "Send content now or arrange future message creation."),
		leaf("message.send", "message", "Send multimodal content to an endpoint."),
		leaf("message.schedule", "message", "Create or change a scheduled message."),
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
		_, err := c.ResolveLeaf(decision.CapabilityPath)
		return err
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
		return nil
	default:
		return fmt.Errorf("unsupported route status %q", decision.Status)
	}
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
		if node.Workflow != nil {
			return fmt.Errorf("capability branch %q cannot select a workflow", node.ID)
		}
	case NodeLeaf:
		if node.ParentID == "" {
			return fmt.Errorf("capability leaf %q requires a parent", node.ID)
		}
		if node.Workflow == nil || strings.TrimSpace(string(node.Workflow.ID)) == "" || node.Workflow.Revision < 1 {
			return fmt.Errorf("capability leaf %q requires a versioned workflow contract", node.ID)
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
	return node
}

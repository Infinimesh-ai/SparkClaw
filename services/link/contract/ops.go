package contract

import (
	"encoding/json"
	"sort"
)

// Core operations (design section 4.2).
const (
	OpSyncChanges     = "sync.changes"
	OpSessionCreate   = "session.create"
	OpSessionRename   = "session.rename"
	OpSessionArchive  = "session.archive"
	OpSessionDelete   = "session.delete"
	OpSessionMessages = "session.messages"
	OpMessageSend     = "message.send"
	OpRunSubscribe    = "run.subscribe"
	OpRunUnsubscribe  = "run.unsubscribe"
	OpRunStatus       = "run.status"
	OpRunCancel       = "run.cancel"
	OpApprovalList    = "approval.list"
	OpApprovalResolve = "approval.resolve"
	OpDeviceDescribe  = "device.describe"
	OpPing            = "ping"

	// Large objects (design 3.10).
	OpObjectUploadBegin  = "object.upload.begin"
	OpObjectUploadChunk  = "object.upload.chunk"
	OpObjectUploadCommit = "object.upload.commit"
	OpObjectDescribe     = "object.describe"
	OpObjectRead         = "object.read"

	// Business domains (design 3.1, section 5).
	OpDomainCall = "domain.call"

	// Projections kept from the phone operations (section 5).
	OpProjectionSnapshot = "projection.snapshot"
	OpProjectionActivity = "projection.activity"
)

// Device conditions a client declares at handshake (design 3.9).
type Device struct {
	Class string `json:"class"`
	// Name is a human label, for example the console device name.
	Name string `json:"name,omitempty"`
	// App identifies the client build, for diagnostics only.
	App        string     `json:"app,omitempty"`
	Conditions Conditions `json:"conditions"`
}

// Conditions are what the device itself can do. Features are closed only
// where a condition is missing.
type Conditions struct {
	// BrowserSurface: the client has a built-in browser the backend may
	// drive (SparkX only).
	BrowserSurface bool `json:"browser_surface,omitempty"`
	// Attachments: the client can show, pick, upload and download files.
	Attachments bool `json:"attachments,omitempty"`
	// Microphone: the client can record audio.
	Microphone bool `json:"microphone,omitempty"`
	// Screen is large (desktop), small (phone) or tiny (watch).
	Screen string `json:"screen,omitempty"`
}

// Screen classes.
const (
	ScreenLarge = "large"
	ScreenSmall = "small"
	ScreenTiny  = "tiny"
)

// Requirement lists the conditions an operation needs. Most need none.
type Requirement struct {
	BrowserSurface bool
	Attachments    bool
	Microphone     bool
}

// Satisfied reports whether c meets r.
func (r Requirement) Satisfied(c Conditions) bool {
	if r.BrowserSurface && !c.BrowserSurface {
		return false
	}
	if r.Attachments && !c.Attachments {
		return false
	}
	if r.Microphone && !c.Microphone {
		return false
	}
	return true
}

// Operation describes one forward operation.
type Operation struct {
	Name     string
	Group    string // core | object | domain | projection
	Mutation bool
	// Requires names device conditions.
	Requires Requirement
	// DuplexOnly operations need a long-lived connection (subscriptions).
	// Everything they deliver must also be reachable in the pull profile.
	DuplexOnly bool
}

var operations = map[string]Operation{
	OpPing:            {Name: OpPing, Group: "core"},
	OpDeviceDescribe:  {Name: OpDeviceDescribe, Group: "core"},
	OpSyncChanges:     {Name: OpSyncChanges, Group: "core"},
	OpSessionCreate:   {Name: OpSessionCreate, Group: "core", Mutation: true},
	OpSessionRename:   {Name: OpSessionRename, Group: "core", Mutation: true},
	OpSessionArchive:  {Name: OpSessionArchive, Group: "core", Mutation: true},
	OpSessionDelete:   {Name: OpSessionDelete, Group: "core", Mutation: true},
	OpSessionMessages: {Name: OpSessionMessages, Group: "core"},
	OpMessageSend:     {Name: OpMessageSend, Group: "core", Mutation: true},
	OpRunSubscribe:    {Name: OpRunSubscribe, Group: "core", DuplexOnly: true},
	OpRunUnsubscribe:  {Name: OpRunUnsubscribe, Group: "core", DuplexOnly: true},
	OpRunStatus:       {Name: OpRunStatus, Group: "core"},
	OpRunCancel:       {Name: OpRunCancel, Group: "core", Mutation: true},
	OpApprovalList:    {Name: OpApprovalList, Group: "core"},
	OpApprovalResolve: {Name: OpApprovalResolve, Group: "core", Mutation: true},

	OpObjectUploadBegin:  {Name: OpObjectUploadBegin, Group: "object", Mutation: true, Requires: Requirement{Attachments: true}},
	OpObjectUploadChunk:  {Name: OpObjectUploadChunk, Group: "object", Mutation: true, Requires: Requirement{Attachments: true}},
	OpObjectUploadCommit: {Name: OpObjectUploadCommit, Group: "object", Mutation: true, Requires: Requirement{Attachments: true}},
	OpObjectDescribe:     {Name: OpObjectDescribe, Group: "object"},
	OpObjectRead:         {Name: OpObjectRead, Group: "object", Requires: Requirement{Attachments: true}},

	OpDomainCall: {Name: OpDomainCall, Group: "domain"},

	OpProjectionSnapshot: {Name: OpProjectionSnapshot, Group: "projection"},
	OpProjectionActivity: {Name: OpProjectionActivity, Group: "projection"},
}

// LookupOperation returns the operation definition.
func LookupOperation(name string) (Operation, bool) {
	op, ok := operations[name]
	return op, ok
}

// Operations returns every forward operation sorted by name.
func Operations() []Operation {
	out := make([]Operation, 0, len(operations))
	for _, op := range operations {
		out = append(out, op)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Available returns the operations this device may use. The same rule runs
// at every entry point, so LAN and ISCP give the same result.
func Available(c Conditions, profile string) []string {
	var out []string
	for _, op := range Operations() {
		if !op.Requires.Satisfied(c) {
			continue
		}
		if op.DuplexOnly && profile == ProfilePull {
			continue
		}
		out = append(out, op.Name)
	}
	return out
}

// Reverse operations a desktop with a built-in browser serves. Their bodies
// are opaque to the link layer and unchanged from the previous transport.
var reverseOperations = map[string]Requirement{
	"browser.controller.status":  {BrowserSurface: true},
	"browser.controller.acquire": {BrowserSurface: true},
	"browser.controller.execute": {BrowserSurface: true},
	"browser.controller.release": {BrowserSurface: true},
	"browser.controller.chunk":   {BrowserSurface: true},
	"mcd.account.ensure":         {BrowserSurface: true},
	"mcd.checkout.prepare":       {BrowserSurface: true},
	"mcd.checkout.commit":        {BrowserSurface: true},
	"mcd.checkout.abort":         {BrowserSurface: true},
	"mcd.operation.status":       {BrowserSurface: true},
	"mcd.orders.query":           {BrowserSurface: true},
}

// ReverseAllowed reports whether a device with conditions c may declare the
// reverse operation op.
func ReverseAllowed(op string, c Conditions) bool {
	req, ok := reverseOperations[op]
	return ok && req.Satisfied(c)
}

// ReverseOperations lists every reverse operation name.
func ReverseOperations() []string {
	out := make([]string, 0, len(reverseOperations))
	for k := range reverseOperations {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// RegisterReverseOperation lets the Gateway register the exact reverse
// operation names it calls (kept equal to the previous operation table).
func RegisterReverseOperation(op string, req Requirement) {
	reverseOperations[op] = req
}

// LAN connector constants (shared by the Gateway and clients).
const (
	LANPath            = "/link/v1"
	LANProtocol        = "sparkclaw.link.lan.v1"
	LANChallengePrefix = "sparkclaw.link.lan:"
)

// LANControl is the pre-link exchange on a LAN connection: the server sends
// a challenge; the device answers with a proof (audience: the backend's
// device id; challenge: LANChallengePrefix + nonce); the server replies
// authenticated or error. Link frames follow.
type LANControl struct {
	Type      string          `json:"type"`
	Version   string          `json:"version,omitempty"`
	Host      string          `json:"host,omitempty"`
	Nonce     string          `json:"nonce,omitempty"`
	DeviceID  string          `json:"device_id,omitempty"`
	Proof     json.RawMessage `json:"proof,omitempty"`
	Code      string          `json:"code,omitempty"`
	Message   string          `json:"message,omitempty"`
	Principal *Principal      `json:"principal,omitempty"`
}

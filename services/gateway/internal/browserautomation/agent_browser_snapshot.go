package browserautomation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

const agentBrowserSnapshotControlLimit = 24

var agentBrowserTreeRefPattern = regexp.MustCompile(`ref=(e[0-9]+)`)
var agentBrowserTreeControlPattern = regexp.MustCompile(`^-\s+([^\s]+)(?:\s+("(?:[^"\\]|\\.)*"))?.*\[[^\]]*ref=(e[0-9]+)[^\]]*\]`)
var agentBrowserTreeTextPattern = regexp.MustCompile(`^-\s+(?:StaticText|text|heading|paragraph)\s+("(?:[^"\\]|\\.)*")`)
var agentBrowserAuthTitlePattern = regexp.MustCompile(`(?i)(?:登录|登陆|sign[[:space:]-]*in|log[[:space:]-]*in|login)`)
var agentBrowserAuthRoutePattern = regexp.MustCompile(`(?i)(?:[/#?&=._-])(?:login|signin|sign-in|logon|auth|oauth|sso|verify|verification|captcha)(?:[/#?&=._-]|$)`)
var agentBrowserAuthControlPattern = regexp.MustCompile(`(?i)(?:登录|登陆|sign[[:space:]-]*in|log[[:space:]-]*in|login|password|passcode|验证码|verification[[:space:]-]*code|one[[:space:]-]*time[[:space:]-]*code|captcha)`)
var agentBrowserSignOutControlPattern = regexp.MustCompile(`(?i)(?:退出登录|安全退出|注销登录|sign[[:space:]-]*out|log[[:space:]-]*out|logout)`)
var agentBrowserAccountControlPattern = regexp.MustCompile(`(?i)(?:个人中心|我的账户|账户设置|账号设置|用户菜单|个人资料|my[[:space:]-]*account|account[[:space:]-]*settings|profile|user[[:space:]-]*menu|avatar)`)
var agentBrowserCredentialControlPattern = regexp.MustCompile(`(?i)(?:账号|帐号|用户名|邮箱|手机号|username|email|phone|password|passcode)`)
var agentBrowserVerificationControlPattern = regexp.MustCompile(`(?i)(?:验证码|短信验证|verification[[:space:]-]*code|one[[:space:]-]*time[[:space:]-]*code|captcha|\botp\b)`)
var agentBrowserIdentityPattern = regexp.MustCompile(`(?i)[a-z0-9.!#$%&'*+/=?^_{|}~-]+@[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)+`)
var agentBrowserLoginPromptPattern = regexp.MustCompile(`(?i)(?:请(?:先)?登录|登录后(?:查看|访问|继续)|未登录|重新登录|账号登录|密码登录|扫码登录|please[[:space:]]+sign[[:space:]]+in|sign[[:space:]]+in[[:space:]]+to[[:space:]]+continue|login[[:space:]]+required|log[[:space:]]+in[[:space:]]+to[[:space:]]+(?:view|continue)|enter[[:space:]]+(?:your[[:space:]]+)?password)`)
var agentBrowserDocumentationPattern = regexp.MustCompile(`(?i)(?:documentation|developer[[:space:]]+guide|api[[:space:]]+reference|文档|开发指南|接口说明)`)
var agentBrowserApplicationLandmarkPattern = regexp.MustCompile(`(?im)^\s*-\s+(?:main|navigation|menubar|tree|complementary)(?:\s|$)`)

type agentBrowserSnapshotRef struct {
	RawRef      string
	ExternalRef string
	Fingerprint string
	Role        string
	Name        string
	Clickable   bool
	Ordinal     int
	Score       int
	Index       int
}

type agentBrowserSnapshotState struct {
	SnapshotID    string
	PageID        string
	URL           string
	Digest        string
	ContentDigest string
	ActionTaken   bool
	Refs          map[string]*agentBrowserSnapshotRef
}

func agentBrowserSnapshotRawArgs() map[string]any {
	return map[string]any{"compact": true, "includeUrls": true}
}

func (e *agentBrowserSessionEntry) takeSnapshotLocked(ctx context.Context, args, rawArgs map[string]any) (map[string]any, error) {
	result, err := e.callAgentToolLocked(ctx, "agent_browser_snapshot", rawArgs)
	if err != nil {
		return nil, err
	}
	data := mapValue(result.Data)
	rawTree := firstStringValue(data, "snapshot", "tree", "text")
	if rawTree == "" {
		rawTree = contentText(agentBrowserOutput(result))
	}
	refs := mapValue(data["refs"])
	if refs == nil {
		return nil, errorsForSnapshot("agent-browser omitted the structured refs map")
	}
	enrichAgentBrowserRefsFromTree(refs, rawTree)
	pageID := e.currentPageIDLocked(ctx)
	if pageID == "" {
		return nil, errorsForSnapshot("agent-browser did not report an active tab")
	}
	url, _ := e.currentURLLocked(ctx)
	title, _ := e.currentTitleLocked(ctx)
	metadata := e.snapshotVisibleTextLocked(ctx)

	e.nextSnapshotID++
	snapshotID := agentBrowserSnapshotID(e.generation, pageID, e.nextSnapshotID)
	goal := strings.TrimSpace(stringArg(args, "interaction_goal"))
	allRefs := buildAgentBrowserSnapshotRefs(refs, goal)
	metadata["tree"] = rawTree
	metadata = inferAgentBrowserSnapshotAuth(metadata, title, url, allRefs)
	pageText, pageTextObserved := metadata["text"].(string)
	contentDigest := ""
	if pageTextObserved {
		contentDigest = digestBrowserStableContent(title, pageText)
	}
	digest := digestAgentBrowserSnapshot(url, title, rawTree, pageText, allRefs)
	previous := e.snapshots[pageID]
	repeated := previous != nil && previous.ActionTaken && contentDigest != "" && previous.ContentDigest == contentDigest
	previousID := ""
	if previous != nil && previous.ActionTaken {
		previousID = previous.SnapshotID
	}

	ranked := append([]*agentBrowserSnapshotRef{}, allRefs...)
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].Score == ranked[j].Score {
			return ranked[i].Index < ranked[j].Index
		}
		return ranked[i].Score > ranked[j].Score
	})
	if len(ranked) > agentBrowserSnapshotControlLimit {
		ranked = ranked[:agentBrowserSnapshotControlLimit]
	}
	state := &agentBrowserSnapshotState{
		SnapshotID:    snapshotID,
		PageID:        pageID,
		URL:           url,
		Digest:        digest,
		ContentDigest: contentDigest,
		Refs:          map[string]*agentBrowserSnapshotRef{},
	}
	controls := make([]any, 0, len(ranked))
	actionRefs := make([]string, 0, len(ranked))
	for _, descriptor := range ranked {
		descriptor.ExternalRef = snapshotID + ":" + descriptor.RawRef + ":" + descriptor.Fingerprint[:16]
		state.Refs[descriptor.ExternalRef] = descriptor
		controls = append(controls, agentBrowserSnapshotControl(descriptor))
		if descriptor.Clickable {
			actionRefs = append(actionRefs, descriptor.ExternalRef)
		}
	}
	safeTree := projectAgentBrowserTreeRefs(ranked)
	text := strings.Join(nonEmptyStrings("Page: "+url, safeTree), "\n")
	e.snapshots[pageID] = state
	e.activeSnapshotPage = pageID

	authState := firstStringValue(metadata, "authState", "auth_state")
	authConfidence := firstStringValue(metadata, "authConfidence", "auth_confidence")
	authSignals := firstStringSliceValue(metadata["authSignals"], metadata["auth_signals"])
	snapshot := map[string]any{
		"schema_version":               "browser_interaction_snapshot_v1",
		"snapshot_id":                  snapshotID,
		"previous_snapshot_id":         previousID,
		"page_id":                      pageID,
		"url":                          url,
		"title":                        title,
		"interaction_goal":             goal,
		"digest":                       digest,
		"content_digest":               contentDigest,
		"repeated":                     repeated,
		"controls_total":               len(allRefs),
		"controls_returned":            len(controls),
		"truncated":                    len(allRefs) > len(controls),
		"browser_page_auth_state":      authState,
		"browser_page_auth_confidence": authConfidence,
		"browser_page_auth_signals":    authSignals,
		"aria":                         safeTree,
		"controls":                     controls,
		"refs":                         controls,
		"action_refs":                  actionRefs,
	}
	return map[string]any{
		"text":                         text,
		"snapshot_id":                  snapshotID,
		"page_id":                      pageID,
		"digest":                       digest,
		"content_digest":               contentDigest,
		"repeated":                     repeated,
		"snapshot":                     snapshot,
		"browser_page_auth_state":      authState,
		"browser_page_auth_confidence": authConfidence,
		"browser_page_auth_signals":    authSignals,
		"auth_challenge_detected":      boolValue(metadata["authChallengeDetected"]),
		"content":                      []any{map[string]any{"type": "text", "text": text}},
	}, nil
}

func agentBrowserSnapshotID(generation uint64, pageID string, sequence uint64) string {
	pageNumber := strings.TrimPrefix(strings.TrimSpace(pageID), "page_")
	return fmt.Sprintf("snapshot_%d_%s_%d", generation, pageNumber, sequence)
}

func agentBrowserSnapshotControl(descriptor *agentBrowserSnapshotRef) map[string]any {
	control := map[string]any{
		"ref":             descriptor.ExternalRef,
		"role":            descriptor.Role,
		"accessible_name": descriptor.Name,
		"fingerprint":     descriptor.Fingerprint[:16],
	}
	if descriptor.Ordinal > 1 {
		control["ordinal"] = descriptor.Ordinal
	}
	return control
}

func inferAgentBrowserSnapshotAuth(metadata map[string]any, title, pageURL string, refs []*agentBrowserSnapshotRef) map[string]any {
	result := cloneArgs(metadata)
	state := strings.ToLower(strings.TrimSpace(firstStringValue(result, "authState", "auth_state")))
	confidence := strings.ToLower(strings.TrimSpace(firstStringValue(result, "authConfidence", "auth_confidence")))
	if state == "challenged" || state == "authenticated" || confidence == "conflicting" {
		return result
	}

	titleGate := agentBrowserAuthTitlePattern.MatchString(strings.TrimSpace(title))
	routeGate := agentBrowserAuthRoutePattern.MatchString(strings.ToLower(strings.TrimSpace(pageURL)))
	authControls := 0
	credentialControls := 0
	verificationControls := 0
	interactiveControls := 0
	applicationLandmarks := 0
	authFrame := false
	signOutControl := false
	accountControl := false
	identityControl := false
	for _, ref := range refs {
		if ref == nil {
			continue
		}
		name := strings.TrimSpace(ref.Name)
		if name == "" {
			continue
		}
		role := strings.ToLower(strings.TrimSpace(ref.Role))
		if ref.Clickable || agentBrowserInteractiveRole(role) {
			interactiveControls++
		}
		if role == "main" || role == "navigation" || role == "menubar" || role == "tree" || role == "complementary" {
			applicationLandmarks++
		}
		if agentBrowserSignOutControlPattern.MatchString(name) {
			signOutControl = true
			continue
		}
		if agentBrowserAccountControlPattern.MatchString(name) {
			accountControl = true
		}
		if len(name) <= 200 && agentBrowserIdentityPattern.MatchString(name) {
			identityControl = true
		}
		if agentBrowserAuthControlPattern.MatchString(name) {
			authControls++
			if role == "iframe" {
				authFrame = true
			}
		}
		if (role == "textbox" || role == "input") && agentBrowserCredentialControlPattern.MatchString(name) {
			credentialControls++
		}
		if agentBrowserVerificationControlPattern.MatchString(name) {
			verificationControls++
		}
	}

	pageText := firstStringValue(result, "text")
	if agentBrowserApplicationLandmarkPattern.MatchString(firstStringValue(result, "tree")) {
		applicationLandmarks++
	}
	if agentBrowserDocumentationPattern.MatchString(title) && (titleGate || routeGate) {
		result["authState"] = "unknown"
		result["authConfidence"] = "insufficient"
		result["authSignals"] = []string{}
		result["authChallengeDetected"] = false
		return result
	}
	explicitLoginPrompt := agentBrowserLoginPromptPattern.MatchString(title + "\n" + pageText)
	challenge := verificationControls > 0 && (titleGate || routeGate || authControls > 0) ||
		credentialControls > 0 && (titleGate || routeGate || authControls > 0) ||
		authFrame && (titleGate || routeGate) ||
		authControls >= 2 && (titleGate || routeGate) ||
		explicitLoginPrompt && (titleGate || routeGate)

	signals := firstStringSliceValue(result["authSignals"], result["auth_signals"])
	usableApplicationShell := !titleGate && !routeGate && len([]rune(strings.TrimSpace(pageText))) >= 80 &&
		(applicationLandmarks > 0 && interactiveControls >= 4 || interactiveControls >= 6 || identityControl && interactiveControls >= 4)
	if usableApplicationShell {
		signals = appendUniqueString(signals, "usable_application_shell")
	}
	if signOutControl {
		signals = appendUniqueString(signals, "visible_sign_out_control")
	}
	if accountControl {
		signals = appendUniqueString(signals, "visible_account_control")
	}
	if identityControl && usableApplicationShell {
		signals = appendUniqueString(signals, "visible_identity_control")
	}
	authenticated := signOutControl || usableApplicationShell && (accountControl || identityControl)
	if challenge && authenticated {
		result["authState"] = "unknown"
		result["authConfidence"] = "conflicting"
		result["authSignals"] = signals
		result["authChallengeDetected"] = false
		return result
	}
	if authenticated {
		result["authState"] = "authenticated"
		if identityControl && usableApplicationShell {
			result["authConfidence"] = "application_continuity"
		} else {
			result["authConfidence"] = "explicit_ui"
		}
		result["authSignals"] = signals
		result["authChallengeDetected"] = false
		return result
	}
	if !challenge {
		result["authState"] = "unknown"
		if usableApplicationShell {
			result["authConfidence"] = "application_shell"
		} else if confidence == "" {
			result["authConfidence"] = "insufficient"
		}
		result["authSignals"] = signals
		result["authChallengeDetected"] = false
		return result
	}
	if titleGate {
		signals = appendUniqueString(signals, "snapshot_auth_title")
	}
	if routeGate {
		signals = appendUniqueString(signals, "snapshot_auth_route")
	}
	if authControls > 0 {
		signals = appendUniqueString(signals, "snapshot_auth_controls")
	}
	if authFrame {
		signals = appendUniqueString(signals, "snapshot_auth_frame")
	}
	if credentialControls > 0 {
		signals = appendUniqueString(signals, "snapshot_credential_controls")
	}
	if verificationControls > 0 {
		signals = appendUniqueString(signals, "snapshot_verification_controls")
	}
	result["authState"] = "challenged"
	result["authConfidence"] = "accessibility_tree"
	result["authSignals"] = signals
	result["authChallengeDetected"] = true
	return result
}

func appendUniqueString(values []string, value string) []string {
	for _, current := range values {
		if current == value {
			return values
		}
	}
	return append(values, value)
}

func (e *agentBrowserSessionEntry) snapshotVisibleTextLocked(ctx context.Context) map[string]any {
	metadata := map[string]any{}
	if result, err := e.callAgentToolLocked(ctx, "agent_browser_get_text", map[string]any{"selector": "body"}); err == nil {
		metadata["text"] = firstStringValue(mapValue(result.Data), "text", "value", "result")
	}
	return metadata
}

func agentBrowserInteractiveRole(role string) bool {
	switch role {
	case "button", "link", "textbox", "input", "combobox", "checkbox", "radio", "menuitem", "tab", "option", "switch", "slider", "spinbutton":
		return true
	default:
		return false
	}
}

func buildAgentBrowserSnapshotRefs(refs map[string]any, goal string) []*agentBrowserSnapshotRef {
	keys := make([]string, 0, len(refs))
	for rawRef := range refs {
		if agentBrowserRefNumber(rawRef) > 0 {
			keys = append(keys, rawRef)
		}
	}
	sort.Slice(keys, func(i, j int) bool { return agentBrowserRefNumber(keys[i]) < agentBrowserRefNumber(keys[j]) })
	ordinals := map[string]int{}
	result := make([]*agentBrowserSnapshotRef, 0, len(keys))
	for index, rawRef := range keys {
		values := mapValue(refs[rawRef])
		role := firstStringValue(values, "role")
		if role == "" {
			role = "control"
		}
		name := firstStringValue(values, "name", "accessible_name")
		clickable := boolValue(values["clickable"])
		ordinalKey := role + "\x00" + name
		ordinals[ordinalKey]++
		ordinal := ordinals[ordinalKey]
		fingerprint := snapshotRefFingerprint(role, name, ordinal)
		result = append(result, &agentBrowserSnapshotRef{
			RawRef: rawRef, Fingerprint: fingerprint, Role: role, Name: name, Clickable: clickable,
			Ordinal: ordinal, Score: scoreAgentBrowserRef(role, name, goal, clickable), Index: index,
		})
	}
	return result
}

func enrichAgentBrowserRefsFromTree(refs map[string]any, tree string) {
	lines := strings.Split(tree, "\n")
	for index, line := range lines {
		match := agentBrowserTreeControlPattern.FindStringSubmatch(strings.TrimSpace(line))
		if len(match) != 4 {
			continue
		}
		rawRef := match[3]
		values := mapValue(refs[rawRef])
		if values == nil {
			values = map[string]any{}
			refs[rawRef] = values
		}
		if firstStringValue(values, "role") == "" {
			values["role"] = match[1]
		}
		if strings.Contains(line, " clickable") || strings.Contains(line, "cursor:pointer") || strings.Contains(line, "onclick") {
			values["clickable"] = true
		}
		if firstStringValue(values, "name", "accessible_name") == "" && match[2] != "" {
			name, err := strconv.Unquote(match[2])
			if err == nil {
				values["name"] = name
			}
		}
		if firstStringValue(values, "name", "accessible_name") == "" {
			values["name"] = agentBrowserDescendantText(lines, index)
		}
	}
}

func agentBrowserDescendantText(lines []string, parentIndex int) string {
	parentIndent := agentBrowserTreeIndent(lines[parentIndex])
	labels := make([]string, 0, 3)
	nestedRefIndents := []int{}
	for index := parentIndex + 1; index < len(lines); index++ {
		line := lines[index]
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		indent := agentBrowserTreeIndent(line)
		if strings.HasPrefix(trimmed, "-") && indent <= parentIndent {
			break
		}
		for len(nestedRefIndents) > 0 && indent <= nestedRefIndents[len(nestedRefIndents)-1] {
			nestedRefIndents = nestedRefIndents[:len(nestedRefIndents)-1]
		}
		if agentBrowserTreeRefPattern.MatchString(trimmed) {
			nestedRefIndents = append(nestedRefIndents, indent)
			continue
		}
		if len(nestedRefIndents) > 0 {
			continue
		}
		match := agentBrowserTreeTextPattern.FindStringSubmatch(trimmed)
		if len(match) != 2 {
			continue
		}
		label, err := strconv.Unquote(match[1])
		if err != nil || strings.TrimSpace(label) == "" {
			continue
		}
		labels = append(labels, strings.TrimSpace(label))
		if len(labels) == 3 || len(strings.Join(labels, " ")) >= 160 {
			break
		}
	}
	return truncateAgentBrowserText(strings.Join(labels, " "), 160)
}

func truncateAgentBrowserText(value string, limit int) string {
	characters := []rune(value)
	if limit <= 0 || len(characters) <= limit {
		return value
	}
	return string(characters[:limit])
}

func agentBrowserTreeIndent(line string) int {
	indent := 0
	for _, value := range line {
		switch value {
		case ' ':
			indent++
		case '\t':
			indent += 2
		default:
			return indent
		}
	}
	return indent
}

func projectAgentBrowserTreeRefs(refs []*agentBrowserSnapshotRef) string {
	if len(refs) == 0 {
		return ""
	}
	lines := make([]string, 0, len(refs))
	for _, descriptor := range refs {
		role := strings.TrimSpace(descriptor.Role)
		if role == "" {
			role = "control"
		}
		line := "- " + role
		if descriptor.Name != "" {
			encoded, _ := json.Marshal(descriptor.Name)
			line += " " + string(encoded)
		}
		line += " [ref=" + descriptor.ExternalRef + "]"
		if descriptor.Clickable {
			line += " clickable"
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func scoreAgentBrowserRef(role, name, goal string, clickable bool) int {
	role = strings.ToLower(strings.TrimSpace(role))
	name = strings.ToLower(strings.TrimSpace(name))
	goal = strings.ToLower(strings.TrimSpace(goal))
	score := 0
	if clickable || role == "button" || role == "link" || role == "textbox" || role == "input" || role == "combobox" || role == "checkbox" || role == "radio" || role == "menuitem" || role == "tab" || role == "option" || role == "switch" {
		score++
	}
	if goal == "" {
		return score
	}
	if name != "" && strings.Contains(goal, name) {
		score += 200
	}
	if name != "" && strings.Contains(name, goal) {
		score += 100
	}
	for _, token := range strings.Fields(goal) {
		if len([]rune(token)) > 1 && strings.Contains(name, token) {
			score += 30
		}
	}
	return score
}

func snapshotRefFingerprint(role, name string, ordinal int) string {
	digest := sha256.Sum256([]byte(role + "\x00" + name + "\x00" + strconv.Itoa(ordinal)))
	return hex.EncodeToString(digest[:])
}

func digestAgentBrowserSnapshot(url, title, tree, pageText string, refs []*agentBrowserSnapshotRef) string {
	semantic := make([]map[string]any, 0, len(refs))
	for _, ref := range refs {
		semantic = append(semantic, map[string]any{"role": ref.Role, "name": ref.Name, "ordinal": ref.Ordinal})
	}
	raw, _ := json.Marshal(map[string]any{"url": url, "title": title, "tree": tree, "page_text": pageText, "refs": semantic})
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func (e *agentBrowserSessionEntry) resolveSnapshotRefLocked(args map[string]any) (string, string, *agentBrowserSnapshotRef, *agentBrowserSnapshotState, error) {
	external := strings.TrimSpace(stringArg(args, "uid"))
	if external == "" {
		external = strings.TrimSpace(stringArg(args, "ref"))
	}
	if external == "" {
		return "", "", nil, nil, errorsForSnapshot("browser interaction requires a snapshot ref")
	}
	pageID := strings.TrimSpace(stringArg(args, "page_id"))
	if pageID == "" {
		for candidatePage, state := range e.snapshots {
			if state.Refs[external] != nil {
				pageID = candidatePage
				break
			}
		}
	}
	state := e.snapshots[pageID]
	if state == nil || state.ActionTaken {
		return "", "", nil, nil, errorsForSnapshot("stale or unknown snapshot; take a new browser.snapshot")
	}
	if requested := strings.TrimSpace(stringArg(args, "snapshot_id")); requested != "" && requested != state.SnapshotID {
		return "", "", nil, nil, errorsForSnapshot("stale or mismatched snapshot_id; take a new browser.snapshot")
	}
	descriptor := state.Refs[external]
	if descriptor == nil {
		return "", "", nil, nil, errorsForSnapshot("stale or unknown snapshot ref; take a new browser.snapshot")
	}
	return pageID, descriptor.RawRef, descriptor, state, nil
}

func (e *agentBrowserSessionEntry) refreshSnapshotRefLocked(ctx context.Context, pageID string, state *agentBrowserSnapshotState, descriptor *agentBrowserSnapshotRef) (string, error) {
	if err := e.ensureSnapshotPageActiveLocked(pageID); err != nil {
		return "", err
	}
	currentURL, err := e.currentURLLocked(ctx)
	if err != nil {
		return "", err
	}
	if currentURL != state.URL {
		return "", errorsForSnapshot("active page URL changed; take a new browser.snapshot")
	}
	result, err := e.callAgentToolLocked(ctx, "agent_browser_snapshot", agentBrowserSnapshotRawArgs())
	if err != nil {
		return "", err
	}
	refs := mapValue(mapValue(result.Data)["refs"])
	if refs == nil {
		return "", errorsForSnapshot("agent-browser omitted the structured refs map")
	}
	enrichAgentBrowserRefsFromTree(refs, firstStringValue(mapValue(result.Data), "snapshot", "tree", "text"))
	matches := []string{}
	for _, current := range buildAgentBrowserSnapshotRefs(refs, "") {
		if current.Fingerprint == descriptor.Fingerprint {
			matches = append(matches, current.RawRef)
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) > 1 {
		return "", errorsForSnapshot("snapshot ref became ambiguous; take a new browser.snapshot")
	}
	return "", errorsForSnapshot("snapshot ref changed or is unavailable; take a new browser.snapshot")
}

func (e *agentBrowserSessionEntry) invalidateSnapshotsLocked() {
	e.snapshots = map[string]*agentBrowserSnapshotState{}
	e.activeSnapshotPage = ""
}

func agentBrowserRefNumber(value string) int {
	number, err := strconv.Atoi(strings.TrimPrefix(strings.ToLower(strings.TrimSpace(value)), "e"))
	if err != nil {
		return 0
	}
	return number
}

// errorsForSnapshot classifies every snapshot-binding failure as
// app.ToolErrorSnapshotStale: whatever the specific cause, the caller's
// remedy is the same — take a fresh browser.snapshot.
func errorsForSnapshot(message string) error {
	return &app.CodedToolError{
		Code: app.ToolErrorSnapshotStale,
		Err:  fmt.Errorf("agent-browser snapshot: %s", message),
	}
}

func nonEmptyStrings(values ...string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	return out
}

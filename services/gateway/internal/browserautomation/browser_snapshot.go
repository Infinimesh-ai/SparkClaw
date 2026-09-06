package browserautomation

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const browserSnapshotControlLimit = 24

var browserAuthTitlePattern = regexp.MustCompile(`(?i)(?:登录|登陆|sign[[:space:]-]*in|log[[:space:]-]*in|login)`)
var browserAuthRoutePattern = regexp.MustCompile(`(?i)(?:[/#?&=._-])(?:login|signin|sign-in|logon|auth|oauth|sso|verify|verification|captcha)(?:[/#?&=._-]|$)`)
var browserAuthControlPattern = regexp.MustCompile(`(?i)(?:登录|登陆|sign[[:space:]-]*in|log[[:space:]-]*in|login|password|passcode|验证码|verification[[:space:]-]*code|one[[:space:]-]*time[[:space:]-]*code|captcha)`)
var browserSignOutControlPattern = regexp.MustCompile(`(?i)(?:退出登录|安全退出|注销登录|sign[[:space:]-]*out|log[[:space:]-]*out|logout)`)
var browserAccountControlPattern = regexp.MustCompile(`(?i)(?:个人中心|我的账户|账户设置|账号设置|用户菜单|个人资料|my[[:space:]-]*account|account[[:space:]-]*settings|profile|user[[:space:]-]*menu|avatar)`)
var browserCredentialControlPattern = regexp.MustCompile(`(?i)(?:账号|帐号|用户名|邮箱|手机号|username|email|phone|password|passcode)`)
var browserVerificationControlPattern = regexp.MustCompile(`(?i)(?:验证码|短信验证|verification[[:space:]-]*code|one[[:space:]-]*time[[:space:]-]*code|captcha|\botp\b)`)
var browserIdentityPattern = regexp.MustCompile(`(?i)[a-z0-9.!#$%&'*+/=?^_{|}~-]+@[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)+`)
var browserLoginPromptPattern = regexp.MustCompile(`(?i)(?:请(?:先)?登录|登录后(?:查看|访问|继续)|未登录|重新登录|账号登录|密码登录|扫码登录|please[[:space:]]+sign[[:space:]]+in|sign[[:space:]]+in[[:space:]]+to[[:space:]]+continue|login[[:space:]]+required|log[[:space:]]+in[[:space:]]+to[[:space:]]+(?:view|continue)|enter[[:space:]]+(?:your[[:space:]]+)?password)`)
var browserDocumentationPattern = regexp.MustCompile(`(?i)(?:documentation|developer[[:space:]]+guide|api[[:space:]]+reference|文档|开发指南|接口说明)`)
var browserApplicationLandmarkPattern = regexp.MustCompile(`(?im)^\s*-\s+(?:main|navigation|menubar|tree|complementary)(?:\s|$)`)

type browserSnapshotRef struct {
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

type browserSnapshotState struct {
	SnapshotID    string
	PageID        string
	URL           string
	Digest        string
	ContentDigest string
	ActionTaken   bool
	Refs          map[string]*browserSnapshotRef
}

func browserSnapshotID(generation uint64, pageID string, sequence uint64) string {
	pageNumber := strings.TrimPrefix(strings.TrimSpace(pageID), "page_")
	return fmt.Sprintf("snapshot_%d_%s_%d", generation, pageNumber, sequence)
}

func browserSnapshotControl(descriptor *browserSnapshotRef) map[string]any {
	control := map[string]any{
		"ref":             descriptor.ExternalRef,
		"short_ref":       descriptor.RawRef,
		"role":            descriptor.Role,
		"accessible_name": descriptor.Name,
		"fingerprint":     descriptor.Fingerprint[:16],
	}
	if descriptor.Ordinal > 1 {
		control["ordinal"] = descriptor.Ordinal
	}
	return control
}

func inferBrowserSnapshotAuth(metadata map[string]any, title, pageURL string, refs []*browserSnapshotRef) map[string]any {
	result := cloneArgs(metadata)
	state := strings.ToLower(strings.TrimSpace(firstStringValue(result, "authState", "auth_state")))
	confidence := strings.ToLower(strings.TrimSpace(firstStringValue(result, "authConfidence", "auth_confidence")))
	if state == "challenged" || state == "authenticated" || confidence == "conflicting" {
		return result
	}

	titleGate := browserAuthTitlePattern.MatchString(strings.TrimSpace(title))
	routeGate := browserAuthRoutePattern.MatchString(strings.ToLower(strings.TrimSpace(pageURL)))
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
		if ref.Clickable || browserInteractiveRole(role) {
			interactiveControls++
		}
		if role == "main" || role == "navigation" || role == "menubar" || role == "tree" || role == "complementary" {
			applicationLandmarks++
		}
		if browserSignOutControlPattern.MatchString(name) {
			signOutControl = true
			continue
		}
		if browserAccountControlPattern.MatchString(name) {
			accountControl = true
		}
		if len(name) <= 200 && browserIdentityPattern.MatchString(name) {
			identityControl = true
		}
		if browserAuthControlPattern.MatchString(name) {
			authControls++
			if role == "iframe" {
				authFrame = true
			}
		}
		if (role == "textbox" || role == "input") && browserCredentialControlPattern.MatchString(name) {
			credentialControls++
		}
		if browserVerificationControlPattern.MatchString(name) {
			verificationControls++
		}
	}

	pageText := firstStringValue(result, "text")
	if browserApplicationLandmarkPattern.MatchString(firstStringValue(result, "tree")) {
		applicationLandmarks++
	}
	if browserDocumentationPattern.MatchString(title) && (titleGate || routeGate) {
		result["authState"] = "unknown"
		result["authConfidence"] = "insufficient"
		result["authSignals"] = []string{}
		result["authChallengeDetected"] = false
		return result
	}
	explicitLoginPrompt := browserLoginPromptPattern.MatchString(title + "\n" + pageText)
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

func browserInteractiveRole(role string) bool {
	switch role {
	case "button", "link", "textbox", "input", "combobox", "checkbox", "radio", "menuitem", "tab", "option", "switch", "slider", "spinbutton":
		return true
	default:
		return false
	}
}

func buildBrowserSnapshotRefs(refs map[string]any, goal string) []*browserSnapshotRef {
	keys := make([]string, 0, len(refs))
	for rawRef := range refs {
		if browserRefNumber(rawRef) > 0 {
			keys = append(keys, rawRef)
		}
	}
	sort.Slice(keys, func(i, j int) bool { return browserRefNumber(keys[i]) < browserRefNumber(keys[j]) })
	ordinals := map[string]int{}
	result := make([]*browserSnapshotRef, 0, len(keys))
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
		result = append(result, &browserSnapshotRef{
			RawRef: rawRef, Fingerprint: fingerprint, Role: role, Name: name, Clickable: clickable,
			Ordinal: ordinal, Score: scoreBrowserRef(role, name, goal, clickable), Index: index,
		})
	}
	return result
}

func projectBrowserTreeRefs(refs []*browserSnapshotRef) string {
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

func scoreBrowserRef(role, name, goal string, clickable bool) int {
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

func digestBrowserSnapshot(url, title, tree, pageText string, refs []*browserSnapshotRef) string {
	semantic := make([]map[string]any, 0, len(refs))
	for _, ref := range refs {
		semantic = append(semantic, map[string]any{"role": ref.Role, "name": ref.Name, "ordinal": ref.Ordinal})
	}
	raw, _ := json.Marshal(map[string]any{"url": url, "title": title, "tree": tree, "page_text": pageText, "refs": semantic})
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func browserRefNumber(value string) int {
	number, err := strconv.Atoi(strings.TrimPrefix(strings.ToLower(strings.TrimSpace(value)), "e"))
	if err != nil {
		return 0
	}
	return number
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

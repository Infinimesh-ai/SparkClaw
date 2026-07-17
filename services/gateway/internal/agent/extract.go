package agent

import (
	"path/filepath"
	"regexp"
	"strings"
)

func searchQuery(content string) string {
	content = strings.TrimSpace(content)
	for _, prefix := range []string{"search for", "find", "搜索", "查找", "找"} {
		if idx := strings.Index(strings.ToLower(content), strings.ToLower(prefix)); idx >= 0 {
			rest := strings.TrimSpace(content[idx+len(prefix):])
			if rest != "" {
				return trimSearchScope(trimSentence(rest))
			}
		}
	}
	words := strings.Fields(content)
	if len(words) > 8 {
		words = words[:8]
	}
	if len(words) == 0 {
		return "sparkclaw"
	}
	return strings.Join(words, " ")
}

func trimSearchScope(value string) string {
	lower := strings.ToLower(value)
	for _, marker := range []string{" in the workspace", " in workspace", " in files", " in local files", " inside the workspace", " 在工作区", " 在文件"} {
		if idx := strings.Index(lower, marker); idx > 0 {
			return strings.TrimSpace(value[:idx])
		}
	}
	return strings.TrimSpace(value)
}

func codeSearchQuery(content string) string {
	lower := strings.ToLower(content)
	for _, marker := range []string{"failing test", "failed test", "test failure", "failing tests", "failed tests"} {
		if strings.Contains(lower, marker) {
			return "test"
		}
	}
	for _, marker := range []string{"read repo", "inspect repo", "explain repo", "repo", "repository", "codebase"} {
		if strings.Contains(lower, marker) {
			return "go.mod"
		}
	}
	return searchQuery(content)
}

func memoryContent(content string) string {
	replacements := []string{"remember that", "remember", "请记住", "记住"}
	out := strings.TrimSpace(content)
	lower := strings.ToLower(out)
	for _, prefix := range replacements {
		if strings.HasPrefix(lower, strings.ToLower(prefix)) {
			return strings.TrimSpace(out[len(prefix):])
		}
	}
	return out
}

func extractPath(content string) string {
	paths := extractPaths(content)
	if len(paths) > 0 {
		return paths[0]
	}
	return ""
}

func extractPaths(content string) []string {
	patterns := []*regexp.Regexp{
		regexp.MustCompile("`([^`]+)`"),
		regexp.MustCompile(`(?i)(?:read|open|summarize|读取|打开|总结)\s+([A-Za-z0-9_./\\-]+\.[A-Za-z0-9]+)`),
		regexp.MustCompile(`(?i)(?:delete|remove|删除|移除)\s+([A-Za-z0-9_./\\-]+\.[A-Za-z0-9]+)`),
		regexp.MustCompile(`[A-Za-z0-9_./\\-]+\.[A-Za-z0-9]+`),
	}
	seen := map[string]bool{}
	out := []string{}
	for _, pattern := range patterns {
		matches := pattern.FindAllStringSubmatchIndex(content, -1)
		for _, match := range matches {
			if len(match) < 2 {
				continue
			}
			start, end := match[0], match[1]
			if len(match) >= 4 && match[2] >= 0 && match[3] >= 0 {
				start, end = match[2], match[3]
			}
			value := content[start:end]
			value = strings.Trim(value, "`'\".,;:()[]{}")
			if value == "" || !looksLikeLocalPathToken(content, start, end, value) {
				continue
			}
			clean := filepath.Clean(value)
			if clean == "." || clean == "/" || clean == `\` || seen[clean] {
				continue
			}
			seen[clean] = true
			out = append(out, clean)
		}
	}
	return out
}

func looksLikeLocalPathToken(content string, start, end int, value string) bool {
	lower := strings.ToLower(value)
	if strings.Contains(lower, "://") || strings.Contains(value, "@") {
		return false
	}
	for _, prefix := range []string{"browser.", "files.", "memory.", "code.", "shell.", "notify."} {
		if strings.HasPrefix(lower, prefix) {
			return false
		}
	}
	tokenStart := strings.LastIndexAny(content[:start], " \t\r\n\"'`<>") + 1
	tokenEndRel := strings.IndexAny(content[end:], " \t\r\n\"'`<>")
	tokenEnd := len(content)
	if tokenEndRel >= 0 {
		tokenEnd = end + tokenEndRel
	}
	wholeToken := strings.ToLower(content[tokenStart:tokenEnd])
	if strings.Contains(wholeToken, "://") || strings.Contains(wholeToken, "@") {
		return false
	}
	prefixStart := start - 8
	if prefixStart < 0 {
		prefixStart = 0
	}
	if strings.Contains(strings.ToLower(content[prefixStart:start]), "://") {
		return false
	}
	if start > 0 && content[start-1] == '@' {
		return false
	}
	onlyNumericHost := true
	for _, ch := range value {
		if !(ch >= '0' && ch <= '9' || ch == '.' || ch == ':' || ch == '-') {
			onlyNumericHost = false
			break
		}
	}
	if onlyNumericHost {
		return false
	}
	return strings.Contains(value, ".")
}

func extractURL(content string) string {
	urls := extractURLs(content)
	if len(urls) > 0 {
		return urls[0]
	}
	return ""
}

func extractURLs(content string) []string {
	matches := regexp.MustCompile(`(?i)(?:https?://|www\.)[^\s<>"')，。！？；、]+`).FindAllString(content, -1)
	seen := map[string]bool{}
	out := []string{}
	for _, match := range matches {
		value := strings.TrimRight(match, ".,;!?，。！？")
		if strings.HasPrefix(strings.ToLower(value), "www.") {
			value = "https://" + value
		}
		if value != "" && !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	return out
}

func shellCommand(content string) string {
	if match := regexp.MustCompile("`([^`]+)`").FindStringSubmatch(content); len(match) > 1 {
		return match[1]
	}
	lower := strings.ToLower(content)
	if containsAny(lower, "go test") {
		return "go test ./..."
	}
	if containsAny(lower, "npm test", "run tests", "run test", "tests", "test failure", "failed test", "failing test", "测试") {
		return "npm test"
	}
	return strings.TrimSpace(content)
}

func extractPatch(content string) string {
	if match := regexp.MustCompile("(?s)```(?:diff|patch)?\\s*\\n(.*?)```").FindStringSubmatch(content); len(match) > 1 {
		return strings.TrimSpace(match[1])
	}
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "--- ") {
			return strings.TrimSpace(strings.Join(lines[i:], "\n"))
		}
	}
	return strings.TrimSpace(content)
}

func extractLabeledValue(content, label string) string {
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(label) + `[_ -]?id\s*[:=]\s*`),
		regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(label) + `\s*[:=]\s*`),
	}
	for _, pattern := range patterns {
		if match := pattern.FindStringIndex(content); len(match) == 2 {
			return parseLabeledValue(content[match[1]:])
		}
	}
	return ""
}

func parseLabeledValue(rest string) string {
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return ""
	}
	if rest[0] == '"' || rest[0] == '\'' {
		quote := rest[0]
		for i := 1; i < len(rest); i++ {
			if rest[i] == quote {
				return strings.TrimSpace(rest[1:i])
			}
		}
		return strings.TrimSpace(rest[1:])
	}
	if next := regexp.MustCompile(`\s+[A-Za-z][A-Za-z0-9_-]*(?:[_ -]?id)?\s*[:=]`).FindStringIndex(rest); len(next) == 2 {
		return strings.TrimSpace(rest[:next[0]])
	}
	return strings.TrimSpace(rest)
}

func isCodeTask(content string) bool {
	return containsAny(content,
		"code", "patch", "diff", "repo", "repository", "codebase",
		"failing test", "failed test", "test failure", "run tests", "run test",
		"go test", "npm test", "pytest", "cargo test", "代码", "补丁", "测试",
	)
}

func isCodeInspectionTask(content string) bool {
	return containsAny(content,
		"inspect repo", "read repo", "explain repo", "repo", "repository", "codebase",
		"failing test", "failed test", "test failure", "解释代码", "读代码",
	)
}

func isTerminalTask(content string) bool {
	return containsAny(content,
		"shell", "terminal", "exec", "run command", "sandbox command",
		"run tests", "run test", "sandboxed test", "failing test", "failed test",
		"go test", "npm test", "pytest", "cargo test", "命令", "终端", "测试",
	)
}

func containsAny(content string, needles ...string) bool {
	lower := strings.ToLower(content)
	for _, needle := range needles {
		if strings.Contains(lower, strings.ToLower(needle)) {
			return true
		}
	}
	return false
}

func domainSpecificSearch(content string) bool {
	return containsEnglishSemanticTerm(content,
		"browser", "web",
	) || containsAny(content, "网页", "网址")
}

func trimSentence(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, ".。?？!！")
	return value
}

func trimForEpisode(value string, limit int) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "..."
}

package skills

import (
	"bufio"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
)

type Skill struct {
	Name         string         `json:"name"`
	Description  string         `json:"description"`
	RiskLevel    string         `json:"risk_level"`
	InputSchema  map[string]any `json:"input_schema,omitempty"`
	Dependencies []string       `json:"dependencies"`
	EvalCases    []string       `json:"eval_cases"`
	AllowedTools []string       `json:"allowed_tools"`
	DeniedTools  []string       `json:"denied_tools"`
	Keywords     []string       `json:"keywords"`
	Path         string         `json:"path"`
	BodyPreview  string         `json:"body_preview"`
}

type Registry struct {
	cfg config.Config
}

func NewRegistry(cfg config.Config) Registry {
	return Registry{cfg: cfg}
}

func (r Registry) Enabled() bool {
	return len(r.cfg.Skills.Dirs) > 0
}

func (r Registry) List() ([]Skill, error) {
	out := []Skill{}
	for _, dir := range r.cfg.Skills.Dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			path := filepath.Join(dir, entry.Name(), "SKILL.md")
			skill, err := Load(path)
			if err != nil {
				continue
			}
			out = append(out, skill)
		}
	}
	slices.SortFunc(out, func(a, b Skill) int {
		return strings.Compare(a.Name, b.Name)
	})
	return out, nil
}

func (r Registry) Relevant(query string, limit int) ([]Skill, error) {
	if limit <= 0 {
		limit = 3
	}
	found, err := r.List()
	if err != nil {
		return nil, err
	}
	query = strings.ToLower(query)
	type scoredSkill struct {
		skill Skill
		score int
		index int
	}
	scored := []scoredSkill{}
	for i, skill := range found {
		score := skillScore(skill, query)
		if score > 0 {
			scored = append(scored, scoredSkill{skill: skill, score: score, index: i})
		}
	}
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].score == scored[j].score {
			return scored[i].index < scored[j].index
		}
		return scored[i].score > scored[j].score
	})
	if len(scored) > limit {
		scored = scored[:limit]
	}
	out := make([]Skill, 0, len(scored))
	for _, item := range scored {
		out = append(out, item.skill)
	}
	return out, nil
}

func Load(path string) (Skill, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Skill{}, err
	}
	text := strings.ReplaceAll(string(raw), "\r\n", "\n")
	meta, body := splitFrontMatter(text)
	skill := Skill{
		Name:        filepath.Base(filepath.Dir(path)),
		RiskLevel:   "medium",
		Path:        path,
		BodyPreview: previewBody(body),
	}
	parseMeta(meta, &skill)
	if skill.Name == "" {
		skill.Name = filepath.Base(filepath.Dir(path))
	}
	skill.Dependencies = ensureStringSlice(skill.Dependencies)
	skill.EvalCases = ensureStringSlice(skill.EvalCases)
	skill.AllowedTools = ensureStringSlice(skill.AllowedTools)
	skill.DeniedTools = ensureStringSlice(skill.DeniedTools)
	skill.Keywords = ensureStringSlice(skill.Keywords)
	return skill, nil
}

func splitFrontMatter(text string) (string, string) {
	if !strings.HasPrefix(text, "---\n") {
		return "", text
	}
	rest := strings.TrimPrefix(text, "---\n")
	if idx := strings.Index(rest, "\n---"); idx >= 0 {
		return rest[:idx], strings.TrimSpace(rest[idx+4:])
	}
	return "", text
}

func parseMeta(meta string, skill *Skill) {
	scanner := bufio.NewScanner(strings.NewReader(meta))
	var listKey string
	var activation bool
	var schemaLines []string
	var schemaIndent = -1
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if schemaIndent >= 0 {
			if strings.TrimSpace(line) == "" {
				schemaLines = append(schemaLines, "")
				continue
			}
			indent := leadingSpaces(line)
			if indent > schemaIndent {
				schemaLines = append(schemaLines, strings.TrimPrefix(line, strings.Repeat(" ", schemaIndent+2)))
				continue
			}
			skill.InputSchema = parseSimpleYAMLObject(schemaLines)
			schemaLines = nil
			schemaIndent = -1
		}
		if trimmed == "" {
			continue
		}
		if strings.HasSuffix(trimmed, ":") {
			key := strings.TrimSuffix(trimmed, ":")
			if key == "input_schema" {
				listKey = ""
				activation = false
				schemaIndent = leadingSpaces(line)
				continue
			}
			listKey = key
			activation = listKey == "activation"
			continue
		}
		if strings.HasPrefix(trimmed, "- ") {
			value := strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))
			switch listKey {
			case "allowed_tools":
				skill.AllowedTools = append(skill.AllowedTools, value)
			case "denied_tools":
				skill.DeniedTools = append(skill.DeniedTools, value)
			case "keywords":
				skill.Keywords = append(skill.Keywords, trimQuotes(value))
			case "dependencies":
				skill.Dependencies = append(skill.Dependencies, trimQuotes(value))
			case "eval_cases":
				skill.EvalCases = append(skill.EvalCases, trimQuotes(value))
			}
			continue
		}
		key, value, ok := strings.Cut(trimmed, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if activation && key == "keywords" {
			skill.Keywords = append(skill.Keywords, parseInlineList(value)...)
			continue
		}
		activation = false
		listKey = ""
		switch key {
		case "name":
			skill.Name = trimQuotes(value)
		case "description":
			skill.Description = trimQuotes(value)
		case "risk_level":
			skill.RiskLevel = trimQuotes(value)
		case "input_schema":
			if strings.TrimSpace(value) != "" {
				skill.InputSchema = parseSimpleYAMLObject([]string{strings.TrimSpace(value)})
			} else {
				schemaIndent = leadingSpaces(line)
			}
		case "allowed_tools":
			skill.AllowedTools = parseInlineList(value)
		case "denied_tools":
			skill.DeniedTools = parseInlineList(value)
		case "keywords":
			skill.Keywords = parseInlineList(value)
		case "dependencies":
			skill.Dependencies = parseInlineList(value)
		case "eval_cases":
			skill.EvalCases = parseInlineList(value)
		}
	}
	if schemaIndent >= 0 {
		skill.InputSchema = parseSimpleYAMLObject(schemaLines)
	}
}

func parseInlineList(value string) []string {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "[") || !strings.HasSuffix(value, "]") {
		if value == "" {
			return nil
		}
		return []string{trimQuotes(value)}
	}
	value = strings.TrimSuffix(strings.TrimPrefix(value, "["), "]")
	parts := strings.Split(value, ",")
	out := []string{}
	for _, part := range parts {
		part = trimQuotes(strings.TrimSpace(part))
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func trimQuotes(value string) string {
	return strings.Trim(value, `"'`)
}

func leadingSpaces(value string) int {
	return len(value) - len(strings.TrimLeft(value, " "))
}

func parseSimpleYAMLObject(lines []string) map[string]any {
	root := map[string]any{}
	type frame struct {
		indent int
		value  map[string]any
	}
	stack := []frame{{indent: -1, value: root}}
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		indent := leadingSpaces(line)
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "- ") {
			continue
		}
		key, rawValue, ok := strings.Cut(trimmed, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		rawValue = strings.TrimSpace(rawValue)
		for len(stack) > 1 && indent <= stack[len(stack)-1].indent {
			stack = stack[:len(stack)-1]
		}
		parent := stack[len(stack)-1].value
		if rawValue == "" {
			child := map[string]any{}
			parent[key] = child
			stack = append(stack, frame{indent: indent, value: child})
			continue
		}
		parent[key] = parseScalar(rawValue)
	}
	if len(root) == 0 {
		return nil
	}
	return root
}

func parseScalar(value string) any {
	value = strings.TrimSpace(value)
	switch value {
	case "true":
		return true
	case "false":
		return false
	}
	if strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]") {
		return parseInlineList(value)
	}
	return trimQuotes(value)
}

func ensureStringSlice(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func previewBody(body string) string {
	body = strings.TrimSpace(body)
	body = strings.Join(strings.Fields(body), " ")
	if utf8.RuneCountInString(body) > 240 {
		return string([]rune(body)[:240]) + "..."
	}
	return body
}

func skillScore(skill Skill, query string) int {
	score := 0
	for _, keyword := range skill.Keywords {
		keyword = strings.ToLower(strings.TrimSpace(keyword))
		if keyword != "" && strings.Contains(query, keyword) {
			score += 10
		}
	}
	if skill.Name != "" && strings.Contains(query, strings.ToLower(skill.Name)) {
		score += 6
	}
	for _, token := range strings.Fields(strings.ToLower(skill.Description)) {
		token = strings.Trim(token, ".,:;!?()[]{}\"'")
		if len(token) >= 4 && strings.Contains(query, token) {
			score += 2
		}
	}
	for _, tool := range skill.AllowedTools {
		if tool != "" && strings.Contains(query, strings.ToLower(tool)) {
			score += 4
		}
	}
	return score
}

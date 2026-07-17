package skills

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
)

func TestLoadSkillFrontMatter(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "coding_helper", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`---
name: coding_helper
description: Inspect workspace code and propose reviewable patches.
risk_level: medium
input_schema:
  type: object
  properties:
    query:
      type: string
  required: [query]
dependencies:
  - workspace.allowlist
eval_cases:
  - code_inspection
allowed_tools:
  - files.search
  - files.read
denied_tools: ["shell.exec_sandboxed"]
activation:
  keywords: ["code", "repo", "代码"]
---

Inspect observed code before proposing changes.
`), 0o644); err != nil {
		t.Fatal(err)
	}

	skill, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if skill.Name != "coding_helper" || skill.Description == "" {
		t.Fatalf("unexpected skill: %#v", skill)
	}
	if len(skill.AllowedTools) != 2 || skill.AllowedTools[0] != "files.search" {
		t.Fatalf("allowed tools did not parse: %#v", skill.AllowedTools)
	}
	if len(skill.DeniedTools) != 1 || skill.DeniedTools[0] != "shell.exec_sandboxed" {
		t.Fatalf("denied tools did not parse: %#v", skill.DeniedTools)
	}
	if len(skill.Keywords) != 3 || skill.Keywords[2] != "代码" {
		t.Fatalf("keywords did not parse: %#v", skill.Keywords)
	}
	if skill.InputSchema["type"] != "object" || len(skill.Dependencies) != 1 || skill.Dependencies[0] != "workspace.allowlist" {
		t.Fatalf("skill contract metadata did not parse: %#v", skill)
	}
	if len(skill.EvalCases) != 1 || skill.EvalCases[0] != "code_inspection" {
		t.Fatalf("skill eval cases did not parse: %#v", skill.EvalCases)
	}
	if skill.BodyPreview == "" {
		t.Fatal("body preview was empty")
	}
}

func TestRegistrySkipsMissingDirs(t *testing.T) {
	cfg := config.Default()
	cfg.Skills.Dirs = []string{filepath.Join(t.TempDir(), "missing")}
	found, err := NewRegistry(cfg).List()
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 0 {
		t.Fatalf("expected no skills, got %#v", found)
	}
}

func TestRegistryReturnsRelevantSkills(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "coding_helper", `---
name: coding_helper
description: Inspect workspace code.
allowed_tools: ["files.search"]
activation:
  keywords: ["code", "repo", "代码"]
---
Code workflow.`)
	writeSkill(t, root, "local_files", `---
name: local_files
description: Search workspace files.
allowed_tools: ["files.search"]
activation:
  keywords: ["file", "workspace"]
---
File workflow.`)
	cfg := config.Default()
	cfg.Skills.Dirs = []string{root}

	found, err := NewRegistry(cfg).Relevant("Please inspect repo code for deployment", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].Name != "coding_helper" {
		t.Fatalf("unexpected relevant skills: %#v", found)
	}
}

func TestRegistryRoutesAdmissionLookupToWebSearch(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "web_search", `---
name: web_search
description: Search public information without opening result pages.
allowed_tools: ["web.search"]
activation:
  keywords: ["搜索", "最新", "招生简章"]
---
Search-only workflow.`)
	writeSkill(t, root, "browser_automation", `---
name: browser_automation
description: Read supplied pages and operate a live browser.
allowed_tools: ["browser.read", "browser.open"]
activation:
  keywords: ["浏览器", "打开网页", "页面操作"]
---
Explicit page-access workflow.`)
	cfg := config.Default()
	cfg.Skills.Dirs = []string{root}

	found, err := NewRegistry(cfg).Relevant("浙江大学最新招生简章", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].Name != "web_search" {
		t.Fatalf("admission lookup should load only web_search, got %#v", found)
	}
}

func writeSkill(t *testing.T, root, name, body string) {
	t.Helper()
	path := filepath.Join(root, name, "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

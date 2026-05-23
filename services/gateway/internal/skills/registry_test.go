package skills

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
)

func TestLoadSkillFrontMatter(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "email_triage", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`---
name: email_triage
description: Summarize inbox and draft replies.
risk_level: medium
input_schema:
  type: object
  properties:
    query:
      type: string
  required: [query]
dependencies:
  - email_adapter
eval_cases:
  - email_search_and_draft
allowed_tools:
  - email.search
  - email.draft_reply
denied_tools: ["email.send"]
activation:
  keywords: ["email", "inbox", "邮件"]
---

When handling email, summarize facts before drafting.
`), 0o644); err != nil {
		t.Fatal(err)
	}

	skill, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if skill.Name != "email_triage" || skill.Description == "" {
		t.Fatalf("unexpected skill: %#v", skill)
	}
	if len(skill.AllowedTools) != 2 || skill.AllowedTools[0] != "email.search" {
		t.Fatalf("allowed tools did not parse: %#v", skill.AllowedTools)
	}
	if len(skill.DeniedTools) != 1 || skill.DeniedTools[0] != "email.send" {
		t.Fatalf("denied tools did not parse: %#v", skill.DeniedTools)
	}
	if len(skill.Keywords) != 3 || skill.Keywords[2] != "邮件" {
		t.Fatalf("keywords did not parse: %#v", skill.Keywords)
	}
	if skill.InputSchema["type"] != "object" || len(skill.Dependencies) != 1 || skill.Dependencies[0] != "email_adapter" {
		t.Fatalf("skill contract metadata did not parse: %#v", skill)
	}
	if len(skill.EvalCases) != 1 || skill.EvalCases[0] != "email_search_and_draft" {
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
	writeSkill(t, root, "email_triage", `---
name: email_triage
description: Summarize inbox and draft replies.
allowed_tools: ["email.search"]
activation:
  keywords: ["email", "inbox", "邮件"]
---
Email workflow.`)
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

	found, err := NewRegistry(cfg).Relevant("Please search email inbox for deployment", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].Name != "email_triage" {
		t.Fatalf("unexpected relevant skills: %#v", found)
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

package agent

import (
	"sort"
	"strings"
)

type contextSectionVariant struct {
	Name string
	Text string
}

type contextSection struct {
	Kind     string
	Priority int
	Variants []contextSectionVariant
	level    int
}

type contextBuilder struct {
	Sections []contextSection
	Joiner   string
}

func (builder contextBuilder) Render(maxTokens int) string {
	sections := append([]contextSection(nil), builder.Sections...)
	joiner := builder.Joiner
	if joiner == "" {
		joiner = "\n\n"
	}
	render := func() string {
		parts := make([]string, 0, len(sections))
		for _, section := range sections {
			if len(section.Variants) == 0 {
				continue
			}
			level := section.level
			if level >= len(section.Variants) {
				level = len(section.Variants) - 1
			}
			if text := strings.TrimSpace(section.Variants[level].Text); text != "" {
				parts = append(parts, section.Variants[level].Text)
			}
		}
		return strings.Join(parts, joiner)
	}
	text := render()
	if maxTokens <= 0 || estimatePromptTokens("", text) <= maxTokens {
		return text
	}
	indices := make([]int, len(sections))
	for index := range sections {
		indices[index] = index
	}
	sort.SliceStable(indices, func(i, j int) bool {
		return sections[indices[i]].Priority < sections[indices[j]].Priority
	})
	for estimatePromptTokens("", text) > maxTokens {
		degraded := false
		for _, index := range indices {
			if sections[index].level+1 >= len(sections[index].Variants) {
				continue
			}
			sections[index].level++
			degraded = true
			text = render()
			break
		}
		if !degraded {
			break
		}
	}
	return text
}

func staticContextSection(kind string, priority int, text string) contextSection {
	return contextSection{Kind: kind, Priority: priority, Variants: []contextSectionVariant{{Name: "full", Text: text}}}
}

func degradingContextSection(kind string, priority int, full, compact string, drop bool) contextSection {
	variants := []contextSectionVariant{{Name: "full", Text: full}, {Name: "compact", Text: compact}}
	if drop {
		variants = append(variants, contextSectionVariant{Name: "drop"})
	}
	return contextSection{Kind: kind, Priority: priority, Variants: variants}
}

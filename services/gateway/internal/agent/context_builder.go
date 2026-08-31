package agent

import (
	"errors"
	"sort"
	"strings"
)

type contextSectionVariant struct {
	Name string
	Text string
}

type contextSectionChannel string

const (
	contextChannelSystem contextSectionChannel = "system"
	contextChannelUser   contextSectionChannel = "user"
)

type contextSectionPolicy string

const (
	contextPolicyFixed      contextSectionPolicy = "fixed"
	contextPolicyDegradable contextSectionPolicy = "degradable"
)

type contextSection struct {
	Kind     string
	Priority int
	Channel  contextSectionChannel
	Policy   contextSectionPolicy
	Variants []contextSectionVariant
	level    int
}

type contextSectionDecision struct {
	Kind        string `json:"kind"`
	FromVariant string `json:"from_variant"`
	ToVariant   string `json:"to_variant"`
	BytesBefore int    `json:"bytes_before"`
	BytesAfter  int    `json:"bytes_after"`
}

type contextAdmission struct {
	System           string
	User             string
	EstimatedTokens  int
	InitialTokens    int
	SectionDecisions []contextSectionDecision
	SelectedVariants map[string]contextSectionVariant
}

type contextTokenCounter func(system, user string) (int, error)

var errPromptFixedSectionsOversized = errors.New("workflow_prompt_fixed_sections_oversized")

type contextBuilder struct {
	Sections     []contextSection
	SystemJoiner string
	UserJoiner   string
}

// Render admits the sections under maxTokens and joins the result. It fails
// closed: when even the fixed sections exceed the budget the caller gets the
// error instead of a silently unbounded prompt.
func (builder contextBuilder) Render(maxTokens int) (string, error) {
	admission, err := builder.Admit(maxTokens)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(admission.System) == "" {
		return admission.User, nil
	}
	if strings.TrimSpace(admission.User) == "" {
		return admission.System, nil
	}
	return admission.System + builder.joiner(contextChannelSystem) + admission.User, nil
}

func (builder contextBuilder) Admit(maxTokens int) (contextAdmission, error) {
	return builder.AdmitWithCounter(maxTokens, func(system, user string) (int, error) {
		return estimatePromptTokens(system, user), nil
	})
}

func (builder contextBuilder) AdmitWithCounter(maxTokens int, counter contextTokenCounter) (contextAdmission, error) {
	if counter == nil {
		return contextAdmission{}, errors.New("context token counter is required")
	}
	sections := builder.normalizedSections()
	initial, err := builder.renderAdmission(sections, nil, 0, counter)
	if err != nil {
		return contextAdmission{}, err
	}
	initial.InitialTokens = initial.EstimatedTokens
	if maxTokens <= 0 {
		return initial, errPromptFixedSectionsOversized
	}
	if initial.EstimatedTokens <= maxTokens {
		return initial, nil
	}

	fixed := make([]contextSection, 0, len(sections))
	for _, section := range sections {
		if section.Policy == contextPolicyFixed {
			fixed = append(fixed, section)
		}
	}
	fixedTokens, err := counter(builder.renderChannel(fixed, contextChannelSystem), builder.renderChannel(fixed, contextChannelUser))
	if err != nil {
		return contextAdmission{}, err
	}
	if fixedTokens > maxTokens {
		return initial, errPromptFixedSectionsOversized
	}

	indices := make([]int, len(sections))
	for index := range sections {
		indices[index] = index
	}
	sort.SliceStable(indices, func(i, j int) bool {
		return sections[indices[i]].Priority < sections[indices[j]].Priority
	})
	decisions := []contextSectionDecision{}
	current := initial
	for current.EstimatedTokens > maxTokens {
		degraded := false
		for _, index := range indices {
			section := &sections[index]
			if section.Policy == contextPolicyFixed || section.level+1 >= len(section.Variants) {
				continue
			}
			before := section.Variants[section.level]
			section.level++
			after := section.Variants[section.level]
			decisions = append(decisions, contextSectionDecision{
				Kind: section.Kind, FromVariant: before.Name, ToVariant: after.Name,
				BytesBefore: len([]byte(before.Text)), BytesAfter: len([]byte(after.Text)),
			})
			current, err = builder.renderAdmission(sections, decisions, initial.EstimatedTokens, counter)
			if err != nil {
				return contextAdmission{}, err
			}
			degraded = true
			break
		}
		if !degraded {
			break
		}
	}
	if current.EstimatedTokens <= maxTokens {
		return current, nil
	}
	return current, errPromptFixedSectionsOversized
}

func (builder contextBuilder) normalizedSections() []contextSection {
	sections := append([]contextSection(nil), builder.Sections...)
	for index := range sections {
		sections[index].Variants = append([]contextSectionVariant(nil), sections[index].Variants...)
		if sections[index].Channel == "" {
			sections[index].Channel = contextChannelUser
		}
		if sections[index].Policy == "" {
			if len(sections[index].Variants) > 1 {
				sections[index].Policy = contextPolicyDegradable
			} else {
				sections[index].Policy = contextPolicyFixed
			}
		}
	}
	return sections
}

func (builder contextBuilder) renderAdmission(sections []contextSection, decisions []contextSectionDecision, initialTokens int, counter contextTokenCounter) (contextAdmission, error) {
	system := builder.renderChannel(sections, contextChannelSystem)
	user := builder.renderChannel(sections, contextChannelUser)
	tokens, err := counter(system, user)
	if err != nil {
		return contextAdmission{}, err
	}
	selected := make(map[string]contextSectionVariant, len(sections))
	for _, section := range sections {
		if len(section.Variants) == 0 {
			continue
		}
		level := section.level
		if level >= len(section.Variants) {
			level = len(section.Variants) - 1
		}
		selected[section.Kind] = section.Variants[level]
	}
	return contextAdmission{
		System: system, User: user, EstimatedTokens: tokens, InitialTokens: initialTokens,
		SectionDecisions: append([]contextSectionDecision(nil), decisions...), SelectedVariants: selected,
	}, nil
}

func (builder contextBuilder) renderChannel(sections []contextSection, channel contextSectionChannel) string {
	parts := make([]string, 0, len(sections))
	for _, section := range sections {
		if section.Channel != channel || len(section.Variants) == 0 {
			continue
		}
		level := section.level
		if level >= len(section.Variants) {
			level = len(section.Variants) - 1
		}
		if value := strings.TrimSpace(section.Variants[level].Text); value != "" {
			parts = append(parts, value)
		}
	}
	return strings.Join(parts, builder.joiner(channel))
}

func (builder contextBuilder) joiner(channel contextSectionChannel) string {
	joiner := builder.UserJoiner
	if channel == contextChannelSystem {
		joiner = builder.SystemJoiner
	}
	if joiner == "" {
		joiner = "\n\n"
	}
	return joiner
}

func fixedContextSection(kind string, priority int, channel contextSectionChannel, text string) contextSection {
	return contextSection{Kind: kind, Priority: priority, Channel: channel, Policy: contextPolicyFixed, Variants: []contextSectionVariant{{Name: "full", Text: text}}}
}

func degradingContextSection(kind string, priority int, full, compact string, drop bool) contextSection {
	variants := []contextSectionVariant{{Name: "full", Text: full}}
	if strings.TrimSpace(compact) != "" && compact != full && len([]byte(compact)) <= len([]byte(full)) {
		variants = append(variants, contextSectionVariant{Name: "compact", Text: compact})
	}
	if drop {
		variants = append(variants, contextSectionVariant{Name: "drop"})
	}
	return contextSection{Kind: kind, Priority: priority, Policy: contextPolicyDegradable, Variants: variants}
}

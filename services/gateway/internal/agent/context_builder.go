package agent

import (
	"errors"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
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
	contextPolicyFixed       contextSectionPolicy = "fixed"
	contextPolicyDegradable  contextSectionPolicy = "degradable"
	contextPolicyTruncatable contextSectionPolicy = "truncatable"
)

type contextTruncationMode string

const (
	contextTruncateHead     contextTruncationMode = "head"
	contextTruncateHeadTail contextTruncationMode = "head_tail"
)

type contextSection struct {
	Kind           string
	Priority       int
	Channel        contextSectionChannel
	Policy         contextSectionPolicy
	TruncationMode contextTruncationMode
	Variants       []contextSectionVariant
	level          int
}

type contextSectionDecision struct {
	Kind          string `json:"kind"`
	FromVariant   string `json:"from_variant"`
	ToVariant     string `json:"to_variant"`
	BytesBefore   int    `json:"bytes_before"`
	BytesAfter    int    `json:"bytes_after"`
	HardTruncated bool   `json:"hard_truncated,omitempty"`
}

type contextAdmission struct {
	System           string
	User             string
	EstimatedTokens  int
	InitialTokens    int
	SectionDecisions []contextSectionDecision
	SelectedVariants map[string]contextSectionVariant
	HardTruncated    bool
}

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
	sections := builder.normalizedSections()
	initial := builder.renderAdmission(sections, nil, 0)
	initial.InitialTokens = initial.EstimatedTokens
	if maxTokens <= 0 || initial.EstimatedTokens <= maxTokens {
		return initial, nil
	}

	fixed := make([]contextSection, 0, len(sections))
	for _, section := range sections {
		if section.Policy == contextPolicyFixed {
			fixed = append(fixed, section)
		}
	}
	if estimatePromptTokens(builder.renderChannel(fixed, contextChannelSystem), builder.renderChannel(fixed, contextChannelUser)) > maxTokens {
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
			current = builder.renderAdmission(sections, decisions, initial.EstimatedTokens)
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

	for _, index := range indices {
		section := &sections[index]
		if section.Policy != contextPolicyTruncatable || len(section.Variants) == 0 {
			continue
		}
		before := section.Variants[section.level]
		original := before.Text
		best := hardTruncateContextSection(original, 0, section.Kind, section.TruncationMode)
		low, high := 0, len([]byte(original))
		for low <= high {
			keep := low + (high-low)/2
			candidate := hardTruncateContextSection(original, keep, section.Kind, section.TruncationMode)
			section.Variants[section.level].Text = candidate
			admitted := builder.renderAdmission(sections, decisions, initial.EstimatedTokens)
			if admitted.EstimatedTokens <= maxTokens {
				best = candidate
				low = keep + 1
			} else {
				high = keep - 1
			}
		}
		section.Variants[section.level].Text = best
		decisions = append(decisions, contextSectionDecision{
			Kind: section.Kind, FromVariant: before.Name, ToVariant: "truncated",
			BytesBefore: len([]byte(original)), BytesAfter: len([]byte(best)), HardTruncated: true,
		})
		current = builder.renderAdmission(sections, decisions, initial.EstimatedTokens)
		current.HardTruncated = true
		if current.EstimatedTokens <= maxTokens {
			return current, nil
		}
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

func (builder contextBuilder) renderAdmission(sections []contextSection, decisions []contextSectionDecision, initialTokens int) contextAdmission {
	system := builder.renderChannel(sections, contextChannelSystem)
	user := builder.renderChannel(sections, contextChannelUser)
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
		System: system, User: user, EstimatedTokens: estimatePromptTokens(system, user), InitialTokens: initialTokens,
		SectionDecisions: append([]contextSectionDecision(nil), decisions...), SelectedVariants: selected,
	}
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

func truncatableContextSection(kind string, priority int, channel contextSectionChannel, text string, mode contextTruncationMode) contextSection {
	return contextSection{
		Kind: kind, Priority: priority, Channel: channel, Policy: contextPolicyTruncatable, TruncationMode: mode,
		Variants: []contextSectionVariant{{Name: "full", Text: text}},
	}
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

func hardTruncateContextSection(value string, keepBytes int, kind string, mode contextTruncationMode) string {
	raw := []byte(value)
	if keepBytes >= len(raw) {
		return value
	}
	if keepBytes < 0 {
		keepBytes = 0
	}
	var retained string
	retainedBytes := 0
	if mode == contextTruncateHeadTail && keepBytes > 1 {
		headBytes := keepBytes * 2 / 3
		tailBytes := keepBytes - headBytes
		head := utf8SafePrefix(value, headBytes)
		tail := utf8SafeSuffix(value, tailBytes)
		retainedBytes = len([]byte(head)) + len([]byte(tail))
		retained = head
		if head != "" && tail != "" {
			retained += "\n"
		}
		retained += tail
	} else {
		retained = utf8SafePrefix(value, keepBytes)
		retainedBytes = len([]byte(retained))
	}
	omitted := len(raw) - retainedBytes
	if omitted < 0 {
		omitted = 0
	}
	marker := "[prompt_truncated=true kind=" + kind + " omitted_bytes=" + strconv.Itoa(omitted) + "]"
	if retained == "" {
		return marker
	}
	if mode == contextTruncateHeadTail {
		parts := strings.SplitN(retained, "\n", 2)
		if len(parts) == 2 {
			return parts[0] + "\n" + marker + "\n" + parts[1]
		}
	}
	return retained + "\n" + marker
}

func utf8SafePrefix(value string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(value) <= maxBytes {
		return value
	}
	cut := maxBytes
	for cut > 0 && !utf8.ValidString(value[:cut]) {
		cut--
	}
	return value[:cut]
}

func utf8SafeSuffix(value string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(value) <= maxBytes {
		return value
	}
	start := len(value) - maxBytes
	for start < len(value) && !utf8.ValidString(value[start:]) {
		start++
	}
	return value[start:]
}

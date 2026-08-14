package websearch

import (
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	InfoProjectionSchemaVersion = 4
	MaxInfoProjectionBytes      = 8192
	defaultInfoProjectionBytes  = 4096
	minInfoProjectionBytes      = 512
)

const (
	InfoProjectionComplete  = "complete"
	InfoProjectionPartial   = "partial"
	InfoProjectionFailed    = "failed"
	InfoProjectionNoResults = "no_results"
)

type InfoEvidenceProjection struct {
	SchemaVersion      int                    `json:"schema_version"`
	Status             string                 `json:"status"`
	RequestID          string                 `json:"request_id,omitempty"`
	Query              string                 `json:"query,omitempty"`
	Summary            *InfoEvidenceText      `json:"summary,omitempty"`
	Facts              []InfoEvidenceFact     `json:"facts,omitempty"`
	Conflicts          []InfoEvidenceConflict `json:"conflicts,omitempty"`
	Freshness          Freshness              `json:"freshness"`
	Uncertainty        []string               `json:"uncertainty,omitempty"`
	Sources            []InfoEvidenceSource   `json:"sources,omitempty"`
	Omissions          []InfoOmission         `json:"omissions,omitempty"`
	Findings           []InfoFinding          `json:"findings,omitempty"`
	LimitationRequired bool                   `json:"limitation_required"`
	FailureCode        string                 `json:"failure_code,omitempty"`
	Untrusted          bool                   `json:"untrusted"`
}

type InfoEvidenceText struct {
	Ref  string `json:"ref"`
	Text string `json:"text"`
}

type InfoEvidenceFact struct {
	Ref        string   `json:"ref"`
	Claim      string   `json:"claim"`
	Confidence string   `json:"confidence,omitempty"`
	SourceIDs  []string `json:"source_ids"`
}

type InfoEvidenceConflict struct {
	Ref        string                  `json:"ref"`
	Topic      string                  `json:"topic"`
	Viewpoints []InfoEvidenceViewpoint `json:"viewpoints"`
}

type InfoEvidenceViewpoint struct {
	Ref       string   `json:"ref"`
	Claim     string   `json:"claim"`
	SourceIDs []string `json:"source_ids"`
}

type InfoEvidenceSource struct {
	Index       int     `json:"index"`
	ID          string  `json:"id"`
	Title       string  `json:"title,omitempty"`
	URL         string  `json:"url,omitempty"`
	Linkable    bool    `json:"linkable"`
	SourceType  string  `json:"source_type,omitempty"`
	PublishedAt *string `json:"published_at,omitempty"`
	RetrievedAt string  `json:"retrieved_at,omitempty"`
}

type InfoOmission struct {
	Component string `json:"component"`
	Reason    string `json:"reason"`
	Count     int    `json:"count"`
}

type InfoFinding struct {
	Component string `json:"component"`
	Code      string `json:"code"`
}

type validatedInfoAggregate struct {
	result         Result
	facts          []InfoEvidenceFact
	conflicts      []InfoEvidenceConflict
	sources        []InfoEvidenceSource
	sourceByID     map[string]InfoEvidenceSource
	freshness      Freshness
	uncertainty    []string
	omissions      []InfoOmission
	findings       []InfoFinding
	projectionLoss bool
	failureCode    string
}

// InfoBrowserSource is the browser consumer's read-only view of Info's final
// source sequence. Answer projection capacity never participates in this view.
type InfoBrowserSource struct {
	Index    int
	ID       string
	URL      string
	Linkable bool
}

func OrderedInfoBrowserSources(result Result, frozenQuery string) ([]InfoBrowserSource, error) {
	directory := validateInfoAggregate(result, frozenQuery)
	if directory.failureCode != "" {
		return nil, errors.New(directory.failureCode)
	}
	out := make([]InfoBrowserSource, 0, len(directory.sources))
	for _, source := range directory.sources {
		out = append(out, InfoBrowserSource{Index: source.Index, ID: source.ID, URL: source.URL, Linkable: source.Linkable})
	}
	return out, nil
}

func CompleteInfoEvidenceDirectory(result Result) InfoEvidenceProjection {
	return ProjectInfoEvidence(result, result.Query, MaxInfoProjectionBytes)
}

func InfoEvidenceProjectionHasEvidence(projection InfoEvidenceProjection) bool {
	return len(projection.Facts) > 0 || len(projection.Conflicts) > 0
}

func InfoEvidenceTextIndex(projection InfoEvidenceProjection) map[string]string {
	index := map[string]string{}
	if projection.Summary != nil {
		index[projection.Summary.Ref] = projection.Summary.Text
	}
	for _, fact := range projection.Facts {
		index[fact.Ref] = fact.Claim
	}
	for _, conflict := range projection.Conflicts {
		index[conflict.Ref] = conflict.Topic
		for _, viewpoint := range conflict.Viewpoints {
			index[viewpoint.Ref] = viewpoint.Claim
		}
	}
	return index
}

// ProjectInfoEvidence validates the aggregate and admits complete semantic
// units in Info order. Query terms, snippets, and upstream actions are never
// inputs to this projection.
func ProjectInfoEvidence(result Result, frozenQuery string, maxBytes int) InfoEvidenceProjection {
	maxBytes = normalizeProjectionLimit(maxBytes)
	directory := validateInfoAggregate(result, frozenQuery)
	projection := InfoEvidenceProjection{
		SchemaVersion: InfoProjectionSchemaVersion,
		RequestID:     strings.TrimSpace(result.RequestID),
		Query:         strings.TrimSpace(frozenQuery),
		Freshness:     directory.freshness,
		Findings:      append([]InfoFinding(nil), directory.findings...),
		Untrusted:     true,
	}
	if directory.failureCode != "" {
		projection.Status = InfoProjectionFailed
		projection.FailureCode = directory.failureCode
		projection.Omissions = appendOmission(projection.Omissions, "aggregate", directory.failureCode, 1)
		projection.LimitationRequired = true
		return fitFailedInfoProjection(projection, maxBytes)
	}
	projection.Omissions = append([]InfoOmission(nil), directory.omissions...)
	projection.LimitationRequired = len(directory.findings) > 0 || len(directory.omissions) > 0 ||
		strings.EqualFold(directory.freshness.StalenessRisk, "high")

	for index, uncertainty := range directory.uncertainty {
		candidate := projection
		candidate.Uncertainty = append(append([]string(nil), projection.Uncertainty...), uncertainty)
		candidate.LimitationRequired = true
		if infoProjectionFits(candidate, maxBytes) {
			projection = candidate
			continue
		}
		projection.Omissions = appendOmission(projection.Omissions, "aggregate.uncertainty", "projection_capacity", len(directory.uncertainty)-index)
		projection.LimitationRequired = true
		directory.projectionLoss = true
		break
	}

	validUnitCount := len(directory.facts) + len(directory.conflicts)
	if validUnitCount == 0 {
		projection.Status = InfoProjectionNoResults
		projection.LimitationRequired = projection.LimitationRequired || len(projection.Omissions) > 0
		return fitFinalInfoProjection(projection, maxBytes)
	}

	if summary := strings.TrimSpace(result.Aggregate.Summary); summary != "" {
		candidate := projection
		candidate.Summary = &InfoEvidenceText{Ref: "summary:0", Text: summary}
		if infoProjectionFits(candidate, maxBytes) {
			projection = candidate
		} else {
			projection.Omissions = appendOmission(projection.Omissions, "aggregate.summary", "projection_capacity", 1)
			projection.LimitationRequired = true
			directory.projectionLoss = true
		}
	}

	capacityExhausted := false
	for index, fact := range directory.facts {
		candidate := projection
		candidate.Facts = append(append([]InfoEvidenceFact(nil), projection.Facts...), fact)
		candidate.Sources = referencedProjectionSources(directory.sources, candidate.Facts, candidate.Conflicts)
		if infoProjectionFits(candidate, maxBytes) {
			projection = candidate
			continue
		}
		projection.Omissions = appendOmission(projection.Omissions, "aggregate.facts", "projection_capacity", len(directory.facts)-index)
		if len(directory.conflicts) > 0 {
			projection.Omissions = appendOmission(projection.Omissions, "aggregate.conflicts", "projection_capacity", len(directory.conflicts))
		}
		projection.LimitationRequired = true
		directory.projectionLoss = true
		capacityExhausted = true
		break
	}
	if !capacityExhausted {
		for index, conflict := range directory.conflicts {
			candidate := projection
			candidate.Conflicts = append(append([]InfoEvidenceConflict(nil), projection.Conflicts...), conflict)
			candidate.Sources = referencedProjectionSources(directory.sources, candidate.Facts, candidate.Conflicts)
			if infoProjectionFits(candidate, maxBytes) {
				projection = candidate
				continue
			}
			projection.Omissions = appendOmission(projection.Omissions, "aggregate.conflicts", "projection_capacity", len(directory.conflicts)-index)
			projection.LimitationRequired = true
			directory.projectionLoss = true
			break
		}
	}
	if !InfoEvidenceProjectionHasEvidence(projection) {
		projection.Summary = nil
	}
	if directory.projectionLoss || len(projection.Facts)+len(projection.Conflicts) < validUnitCount {
		projection.Status = InfoProjectionPartial
		projection.LimitationRequired = true
	} else {
		projection.Status = InfoProjectionComplete
	}
	return fitFinalInfoProjection(projection, maxBytes)
}

func validateInfoAggregate(result Result, frozenQuery string) validatedInfoAggregate {
	directory := validatedInfoAggregate{
		result: result, sourceByID: map[string]InfoEvidenceSource{}, freshness: result.Aggregate.Freshness,
	}
	frozenQuery = strings.TrimSpace(frozenQuery)
	switch {
	case frozenQuery == "":
		directory.failureCode = "frozen_query_missing"
	case strings.TrimSpace(result.Query) != frozenQuery:
		directory.failureCode = "query_mismatch"
	case result.SchemaVersion != InfoResultSchemaVersion:
		directory.failureCode = "result_schema_unsupported"
	case strings.TrimSpace(result.Provider) != InfoProviderName:
		directory.failureCode = "unsupported_provider"
	case strings.TrimSpace(result.RequestID) == "":
		directory.failureCode = "request_id_missing"
	case strings.TrimSpace(result.Status) != "ok":
		directory.failureCode = "provider_status_invalid"
	case !result.Untrusted:
		directory.failureCode = "trust_boundary_missing"
	}
	if directory.failureCode != "" {
		return directory
	}

	idCounts := map[string]int{}
	for _, source := range result.Sources {
		idCounts[strings.TrimSpace(source.ID)]++
	}
	for index, source := range result.Sources {
		id := strings.TrimSpace(source.ID)
		switch {
		case id == "":
			directory.omissions = appendOmission(directory.omissions, "sources", "source_id_missing", 1)
			directory.projectionLoss = true
			continue
		case idCounts[id] > 1:
			directory.omissions = appendOmission(directory.omissions, "sources", "source_id_duplicate", 1)
			directory.projectionLoss = true
			continue
		}
		evidenceIndex := index
		if source.evidenceIndexSet {
			evidenceIndex = source.evidenceIndex
		}
		projected := InfoEvidenceSource{
			Index: evidenceIndex, ID: id, Title: strings.TrimSpace(source.Title), URL: strings.TrimSpace(source.URL),
			Linkable: publicHTTPURL(source.URL), SourceType: strings.TrimSpace(source.SourceType),
			PublishedAt: copyOptionalString(source.PublishedAt), RetrievedAt: strings.TrimSpace(source.RetrievedAt),
		}
		if projected.PublishedAt != nil && !validInfoDate(*projected.PublishedAt) {
			projected.PublishedAt = nil
			directory.omissions = appendOmission(directory.omissions, "sources.published_at", "invalid_date", 1)
			directory.projectionLoss = true
		}
		if projected.RetrievedAt != "" && !validInfoDate(projected.RetrievedAt) {
			projected.RetrievedAt = ""
			directory.omissions = appendOmission(directory.omissions, "sources.retrieved_at", "invalid_date", 1)
			directory.projectionLoss = true
		}
		directory.sources = append(directory.sources, projected)
		directory.sourceByID[id] = projected
	}

	for index, fact := range result.Aggregate.Facts {
		claim := strings.TrimSpace(fact.Claim)
		if claim == "" || !validSourceEdges(fact.Sources, directory.sourceByID) {
			directory.omissions = appendOmission(directory.omissions, "aggregate.facts", invalidClaimReason(claim, fact.Sources), 1)
			directory.projectionLoss = true
			continue
		}
		directory.facts = append(directory.facts, InfoEvidenceFact{
			Ref: "fact:" + strconv.Itoa(index), Claim: claim, Confidence: strings.TrimSpace(fact.Confidence),
			SourceIDs: cleanedSourceIDs(fact.Sources),
		})
	}
	for conflictIndex, conflict := range result.Aggregate.Conflicts {
		projected := InfoEvidenceConflict{Ref: "conflict:" + strconv.Itoa(conflictIndex), Topic: strings.TrimSpace(conflict.Topic)}
		for viewpointIndex, viewpoint := range conflict.Viewpoints {
			claim := strings.TrimSpace(viewpoint.Claim)
			if claim == "" || !validSourceEdges(viewpoint.Sources, directory.sourceByID) {
				directory.omissions = appendOmission(directory.omissions, "aggregate.conflicts.viewpoints", invalidClaimReason(claim, viewpoint.Sources), 1)
				directory.projectionLoss = true
				continue
			}
			projected.Viewpoints = append(projected.Viewpoints, InfoEvidenceViewpoint{
				Ref: projected.Ref + ":viewpoint:" + strconv.Itoa(viewpointIndex), Claim: claim,
				SourceIDs: cleanedSourceIDs(viewpoint.Sources),
			})
		}
		if projected.Topic == "" || len(projected.Viewpoints) < 2 {
			directory.omissions = appendOmission(directory.omissions, "aggregate.conflicts", "invalid_conflict", 1)
			directory.projectionLoss = true
			continue
		}
		directory.conflicts = append(directory.conflicts, projected)
	}

	if directory.freshness.LatestSourceDate != nil && !validInfoDate(*directory.freshness.LatestSourceDate) {
		directory.freshness.LatestSourceDate = nil
		directory.omissions = appendOmission(directory.omissions, "aggregate.freshness.latest_source_date", "invalid_date", 1)
		directory.projectionLoss = true
	}
	if value := strings.TrimSpace(directory.freshness.Status); value != "" && value != "current" {
		directory.findings = append(directory.findings, InfoFinding{Component: "aggregate.freshness.status", Code: "unknown_value"})
	}
	if value := strings.TrimSpace(directory.freshness.StalenessRisk); value != "" && value != "low" && value != "medium" && value != "high" {
		directory.findings = append(directory.findings, InfoFinding{Component: "aggregate.freshness.staleness_risk", Code: "unknown_value"})
	}
	for _, uncertainty := range result.Aggregate.Uncertainty {
		if uncertainty = strings.TrimSpace(uncertainty); uncertainty != "" {
			directory.uncertainty = append(directory.uncertainty, uncertainty)
		} else {
			directory.omissions = appendOmission(directory.omissions, "aggregate.uncertainty", "empty_unit", 1)
			directory.projectionLoss = true
		}
	}
	if result.Legacy() {
		directory.omissions = appendOmission(directory.omissions, "aggregate", "legacy_typed_fields_unavailable", 1)
		directory.projectionLoss = true
	}
	return directory
}

func validSourceEdges(sourceIDs []string, sources map[string]InfoEvidenceSource) bool {
	if len(sourceIDs) == 0 {
		return false
	}
	for _, sourceID := range sourceIDs {
		if _, ok := sources[strings.TrimSpace(sourceID)]; !ok {
			return false
		}
	}
	return true
}

func cleanedSourceIDs(sourceIDs []string) []string {
	out := make([]string, 0, len(sourceIDs))
	for _, sourceID := range sourceIDs {
		out = append(out, strings.TrimSpace(sourceID))
	}
	return out
}

func invalidClaimReason(claim string, sourceIDs []string) string {
	if claim == "" {
		return "empty_claim"
	}
	if len(sourceIDs) == 0 {
		return "source_edge_missing"
	}
	return "source_edge_invalid"
}

func referencedProjectionSources(sources []InfoEvidenceSource, facts []InfoEvidenceFact, conflicts []InfoEvidenceConflict) []InfoEvidenceSource {
	referenced := map[string]bool{}
	for _, fact := range facts {
		for _, sourceID := range fact.SourceIDs {
			referenced[sourceID] = true
		}
	}
	for _, conflict := range conflicts {
		for _, viewpoint := range conflict.Viewpoints {
			for _, sourceID := range viewpoint.SourceIDs {
				referenced[sourceID] = true
			}
		}
	}
	out := make([]InfoEvidenceSource, 0, len(referenced))
	for _, source := range sources {
		if referenced[source.ID] {
			out = append(out, source)
		}
	}
	return out
}

func appendOmission(omissions []InfoOmission, component, reason string, count int) []InfoOmission {
	if count <= 0 {
		return omissions
	}
	for index := range omissions {
		if omissions[index].Component == component && omissions[index].Reason == reason {
			omissions[index].Count += count
			return omissions
		}
	}
	return append(omissions, InfoOmission{Component: component, Reason: reason, Count: count})
}

func normalizeProjectionLimit(maxBytes int) int {
	if maxBytes <= 0 {
		return defaultInfoProjectionBytes
	}
	if maxBytes < minInfoProjectionBytes {
		return minInfoProjectionBytes
	}
	if maxBytes > MaxInfoProjectionBytes {
		return MaxInfoProjectionBytes
	}
	return maxBytes
}

func infoProjectionFits(projection InfoEvidenceProjection, maxBytes int) bool {
	raw, err := json.Marshal(projection)
	return err == nil && len(raw) <= maxBytes
}

func fitFinalInfoProjection(projection InfoEvidenceProjection, maxBytes int) InfoEvidenceProjection {
	if infoProjectionFits(projection, maxBytes) {
		return projection
	}
	failed := InfoEvidenceProjection{
		SchemaVersion: InfoProjectionSchemaVersion, Status: InfoProjectionFailed,
		RequestID: projection.RequestID, Query: projection.Query, FailureCode: "projection_size_exceeded",
		Omissions:          []InfoOmission{{Component: "projection", Reason: "projection_capacity", Count: 1}},
		LimitationRequired: true, Untrusted: true,
	}
	return fitFailedInfoProjection(failed, maxBytes)
}

func fitFailedInfoProjection(projection InfoEvidenceProjection, maxBytes int) InfoEvidenceProjection {
	if infoProjectionFits(projection, maxBytes) {
		return projection
	}
	projection.Query = ""
	projection.RequestID = ""
	projection.Omissions = nil
	projection.Findings = nil
	projection.Freshness = Freshness{}
	return projection
}

func validInfoDate(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	if _, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return true
	}
	_, err := time.Parse("2006-01-02", value)
	return err == nil
}

func publicHTTPURL(value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Host == "" || parsed.User != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return false
	}
	host := strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
	if host == "" || host == "localhost" {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return !ip.IsLoopback() && !ip.IsPrivate() && !ip.IsUnspecified() && !ip.IsLinkLocalUnicast() && !ip.IsLinkLocalMulticast() && !ip.IsMulticast()
	}
	return true
}

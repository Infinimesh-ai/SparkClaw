package document

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"path"
	"slices"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

const (
	xlsxPackagePartMaxBytes  = 16 << 20
	xlsxPackageTotalMaxBytes = 64 << 20
)

type XLSXPackagePart struct {
	Name         string
	ContentType  string
	SHA256       string
	SemanticHash string
	Size         int64
}

type XLSXPackageRelationship struct {
	Source     string
	ID         string
	Type       string
	Target     string
	TargetMode string
}

type XLSXPackageManifest struct {
	Parts                 map[string]XLSXPackagePart
	Relationships         []XLSXPackageRelationship
	SheetParts            map[string]string
	FeatureParts          map[string][]string
	FeatureClasses        []string
	ContentTypesHash      string
	RelationshipGraphHash string
	StylesSemanticHash    string
}

type XLSXPackageReport struct {
	CheckedFeatureClasses []string
	CoverageNotes         []string
}

type xlsxPackageScan struct {
	parts         map[string][]byte
	relationships []XLSXPackageRelationship
}

type xlsxFeatureRegistration struct {
	class      string
	detect     func(xlsxPackageScan) []string
	operations map[string]bool
}

var xlsxPackageFeatureRegistry = []xlsxFeatureRegistration{
	{class: "formulas", detect: xlsxWorksheetElementDetector("f"), operations: xlsxVerifiedOperations()},
	{class: "styles", detect: xlsxPartPrefixDetector("xl/styles.xml"), operations: xlsxVerifiedOperations()},
	{class: "themes", detect: xlsxPartPrefixDetector("xl/theme/"), operations: xlsxVerifiedOperations()},
	{class: "workbook_properties", detect: xlsxPartPrefixDetector("docProps/"), operations: xlsxVerifiedOperations()},
	{class: "merged_ranges", detect: xlsxWorksheetElementDetector("mergeCell"), operations: xlsxVerifiedOperations()},
	{class: "comments", detect: xlsxCommentsDetector, operations: xlsxVerifiedOperations()},
	{class: "hyperlinks", detect: xlsxHyperlinksDetector, operations: xlsxVerifiedOperations()},
	{class: "images", detect: xlsxImagesDetector},
	{class: "calc_chain", detect: xlsxPartPrefixDetector("xl/calcChain.xml")},
	{class: "tables", detect: xlsxTablesDetector},
	{class: "charts", detect: xlsxPartPrefixDetector("xl/charts/")},
	{class: "conditional_formatting", detect: xlsxWorksheetElementDetector("conditionalFormatting")},
	{class: "data_validation", detect: xlsxWorksheetElementDetector("dataValidations")},
	{class: "pivots", detect: xlsxPartContainsDetector("pivot")},
	{class: "slicers", detect: xlsxPartContainsDetector("slicer")},
	{class: "external_links", detect: xlsxExternalLinksDetector},
	{class: "connections", detect: xlsxPartPrefixDetector("xl/connections.xml")},
	{class: "embedded_objects", detect: xlsxPartPrefixDetector("xl/embeddings/")},
	{class: "custom_xml", detect: xlsxPartPrefixDetector("customXml/")},
	{class: "macros", detect: xlsxMacrosDetector},
	{class: "signatures", detect: xlsxSignaturesDetector},
	{class: "protection", detect: xlsxProtectionDetector},
	{class: "encryption", detect: xlsxEncryptionDetector},
}

func InspectXLSXPackage(packagePath string) (XLSXPackageManifest, error) {
	parts, err := readXLSXPackageParts(packagePath)
	if err != nil {
		return XLSXPackageManifest{}, xlsxPackageError(CodeParseFailed, err.Error())
	}
	for _, required := range []string{"[Content_Types].xml", "_rels/.rels", "xl/workbook.xml", "xl/_rels/workbook.xml.rels"} {
		if _, ok := parts[required]; !ok {
			return XLSXPackageManifest{}, xlsxPackageError(CodeParseFailed, "required OOXML part is missing: "+required)
		}
	}
	contentTypes, contentTypesHash, err := parseXLSXContentTypes(parts["[Content_Types].xml"])
	if err != nil {
		return XLSXPackageManifest{}, xlsxPackageError(CodeParseFailed, "content types are malformed: "+err.Error())
	}
	relationships, err := parseXLSXRelationships(parts)
	if err != nil {
		return XLSXPackageManifest{}, xlsxPackageError(CodeParseFailed, err.Error())
	}
	sheetParts, err := parseXLSXSheetParts(parts["xl/workbook.xml"], relationships, parts)
	if err != nil {
		return XLSXPackageManifest{}, xlsxPackageError(CodeParseFailed, err.Error())
	}
	manifest := XLSXPackageManifest{
		Parts:                 map[string]XLSXPackagePart{},
		Relationships:         relationships,
		SheetParts:            sheetParts,
		FeatureParts:          map[string][]string{},
		ContentTypesHash:      contentTypesHash,
		RelationshipGraphHash: xlsxRelationshipGraphHash(relationships),
	}
	for name, raw := range parts {
		part := XLSXPackagePart{Name: name, ContentType: xlsxPartContentType(name, contentTypes), SHA256: xlsxRawHash(raw), Size: int64(len(raw))}
		if strings.HasPrefix(name, "xl/worksheets/") && strings.HasSuffix(name, ".xml") || name == "xl/styles.xml" {
			part.SemanticHash = xlsxXMLSemanticHash(raw)
		}
		manifest.Parts[name] = part
		if name == "xl/styles.xml" {
			manifest.StylesSemanticHash = part.SemanticHash
		}
	}
	scan := xlsxPackageScan{parts: parts, relationships: relationships}
	knownFeatureParts := map[string]bool{}
	for _, registration := range xlsxPackageFeatureRegistry {
		detected := uniqueSortedStrings(registration.detect(scan))
		if len(detected) == 0 {
			continue
		}
		manifest.FeatureClasses = append(manifest.FeatureClasses, registration.class)
		manifest.FeatureParts[registration.class] = detected
		for _, name := range detected {
			knownFeatureParts[name] = true
		}
	}
	unknown := []string{}
	for name := range parts {
		if !xlsxKnownCorePart(name) && !knownFeatureParts[name] {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) > 0 {
		manifest.FeatureClasses = append(manifest.FeatureClasses, "unknown_parts")
		manifest.FeatureParts["unknown_parts"] = uniqueSortedStrings(unknown)
	}
	slices.Sort(manifest.FeatureClasses)
	return manifest, nil
}

func ValidateXLSXPackageForOperation(packagePath, operation string, arguments ...map[string]any) (XLSXPackageManifest, error) {
	manifest, err := InspectXLSXPackage(packagePath)
	if err != nil {
		return XLSXPackageManifest{}, err
	}
	if unsupported := unsupportedXLSXPackageFeatures(manifest, operation); len(unsupported) > 0 {
		return XLSXPackageManifest{}, xlsxPackageError(CodeMutationUnsupported,
			fmt.Sprintf("XLSX package features are not verified for %s: %s", strings.TrimSpace(operation), strings.Join(unsupported, ", ")))
	}
	if len(arguments) > 0 {
		if err := validateXLSXTargetedPackageFeatures(manifest, operation, arguments[0]); err != nil {
			return XLSXPackageManifest{}, err
		}
	}
	return manifest, nil
}

func validateXLSXTargetedPackageFeatures(manifest XLSXPackageManifest, operation string, arguments map[string]any) error {
	operation = strings.ToLower(strings.TrimSpace(operation))
	if operation != "insert_row" && operation != "delete_row" {
		return nil
	}
	sheetPart, ok := caseInsensitiveMapValue(manifest.SheetParts, stringValue(arguments["sheet"]))
	if !ok {
		return xlsxPackageError(CodeMutationUnsupported, "XLSX structural edit has no package-bound worksheet")
	}
	blocked := []string{}
	for _, class := range []string{"formulas", "merged_ranges", "comments", "hyperlinks", "images"} {
		if slices.Contains(manifest.FeatureParts[class], sheetPart) {
			blocked = append(blocked, class)
		}
	}
	if len(blocked) > 0 {
		return xlsxPackageError(CodeMutationUnsupported,
			fmt.Sprintf("XLSX target worksheet features are not verified for %s: %s", operation, strings.Join(blocked, ", ")))
	}
	return nil
}

func VerifyXLSXPackagePreservation(before, after XLSXPackageManifest, edit EditRequest, matches []Match) (XLSXPackageReport, error) {
	operation := strings.ToLower(strings.TrimSpace(edit.Operation))
	if unsupported := unsupportedXLSXPackageFeatures(after, operation); len(unsupported) > 0 {
		return XLSXPackageReport{}, preservationError(app.DocumentFormatXLSX, "output introduced unsupported XLSX package features: "+strings.Join(unsupported, ", "))
	}
	if before.ContentTypesHash != after.ContentTypesHash {
		return XLSXPackageReport{}, preservationError(app.DocumentFormatXLSX, "OOXML content types changed outside the operation allowlist")
	}
	if before.RelationshipGraphHash != after.RelationshipGraphHash {
		return XLSXPackageReport{}, preservationError(app.DocumentFormatXLSX, "OOXML relationship graph changed outside the operation allowlist")
	}
	if !sameStringSet(mapKeys(before.Parts), mapKeys(after.Parts)) {
		return XLSXPackageReport{}, preservationError(app.DocumentFormatXLSX, "OOXML package parts were added or removed")
	}
	if !sameStringMap(before.SheetParts, after.SheetParts) {
		return XLSXPackageReport{}, preservationError(app.DocumentFormatXLSX, "worksheet package bindings changed")
	}
	allowed, err := xlsxMutablePackageParts(before, edit, matches)
	if err != nil {
		return XLSXPackageReport{}, preservationError(app.DocumentFormatXLSX, err.Error())
	}
	for name, beforePart := range before.Parts {
		afterPart := after.Parts[name]
		if beforePart.SHA256 != afterPart.SHA256 && !allowed[name] {
			return XLSXPackageReport{}, preservationError(app.DocumentFormatXLSX, "OOXML part changed outside the operation allowlist: "+name)
		}
	}
	return XLSXPackageReport{
		CheckedFeatureClasses: append([]string{"base_package", "content_types", "relationships", "opaque_parts"}, before.FeatureClasses...),
	}, nil
}

func XLSXPackageReadCoverage(manifest XLSXPackageManifest) map[string]any {
	unsupported := unsupportedXLSXPackageFeatures(manifest, "")
	status := "complete"
	if len(unsupported) > 0 {
		status = "partial"
	}
	return map[string]any{
		"status": status, "mutation_supported": len(unsupported) == 0,
		"feature_classes": append([]string(nil), manifest.FeatureClasses...), "unsupported_feature_classes": unsupported,
	}
}

func unsupportedXLSXPackageFeatures(manifest XLSXPackageManifest, operation string) []string {
	operation = strings.ToLower(strings.TrimSpace(operation))
	unsupported := []string{}
	for _, class := range manifest.FeatureClasses {
		if class == "unknown_parts" {
			unsupported = append(unsupported, class)
			continue
		}
		registration, ok := xlsxFeatureByClass(class)
		if !ok || operation == "" && len(registration.operations) == 0 || operation != "" && !registration.operations[operation] {
			unsupported = append(unsupported, class)
		}
	}
	return uniqueSortedStrings(unsupported)
}

func xlsxFeatureByClass(class string) (xlsxFeatureRegistration, bool) {
	for _, registration := range xlsxPackageFeatureRegistry {
		if registration.class == class {
			return registration, true
		}
	}
	return xlsxFeatureRegistration{}, false
}

func xlsxVerifiedOperations() map[string]bool {
	return map[string]bool{
		"replace_text": true, "update_cell": true, "update_row": true,
		"insert_row": true, "append_row": true, "delete_row": true,
	}
}

func xlsxMutablePackageParts(manifest XLSXPackageManifest, edit EditRequest, matches []Match) (map[string]bool, error) {
	allowed := map[string]bool{"xl/sharedStrings.xml": true}
	sheets := map[string]bool{}
	if strings.EqualFold(strings.TrimSpace(edit.Operation), "replace_text") {
		for _, match := range matches {
			if name := strings.TrimSpace(stringValue(match.Location["sheet"])); name != "" {
				sheets[name] = true
			}
		}
	} else if name := strings.TrimSpace(stringValue(edit.Arguments["sheet"])); name != "" {
		sheets[name] = true
	}
	if len(sheets) == 0 {
		return nil, fmt.Errorf("XLSX package delta has no evidence-bound worksheet")
	}
	for sheet := range sheets {
		part, ok := caseInsensitiveMapValue(manifest.SheetParts, sheet)
		if !ok {
			return nil, fmt.Errorf("XLSX package delta worksheet is missing: %s", sheet)
		}
		allowed[part] = true
	}
	return allowed, nil
}

func readXLSXPackageParts(packagePath string) (map[string][]byte, error) {
	archive, err := zip.OpenReader(packagePath)
	if err != nil {
		return nil, fmt.Errorf("OOXML ZIP cannot be opened")
	}
	defer archive.Close()
	parts := map[string][]byte{}
	var total int64
	for _, file := range archive.File {
		if file.FileInfo().IsDir() {
			continue
		}
		name := strings.TrimPrefix(path.Clean("/"+file.Name), "/")
		if name == "" || name == "." || strings.HasPrefix(name, "../") || file.Name != name {
			return nil, fmt.Errorf("OOXML part name is invalid: %s", file.Name)
		}
		if _, exists := parts[name]; exists {
			return nil, fmt.Errorf("OOXML package contains duplicate part: %s", name)
		}
		if file.UncompressedSize64 > xlsxPackagePartMaxBytes {
			return nil, fmt.Errorf("OOXML part exceeds inspection limit: %s", name)
		}
		reader, openErr := file.Open()
		if openErr != nil {
			return nil, fmt.Errorf("OOXML part cannot be opened: %s", name)
		}
		raw, readErr := io.ReadAll(io.LimitReader(reader, xlsxPackagePartMaxBytes+1))
		closeErr := reader.Close()
		if readErr != nil || closeErr != nil || len(raw) > xlsxPackagePartMaxBytes {
			return nil, fmt.Errorf("OOXML part cannot be read safely: %s", name)
		}
		total += int64(len(raw))
		if total > xlsxPackageTotalMaxBytes {
			return nil, fmt.Errorf("OOXML package exceeds inspection limit")
		}
		parts[name] = raw
	}
	return parts, nil
}

type xlsxContentTypesXML struct {
	Defaults []struct {
		Extension   string `xml:"Extension,attr"`
		ContentType string `xml:"ContentType,attr"`
	} `xml:"Default"`
	Overrides []struct {
		PartName    string `xml:"PartName,attr"`
		ContentType string `xml:"ContentType,attr"`
	} `xml:"Override"`
}

func parseXLSXContentTypes(raw []byte) (map[string]string, string, error) {
	var parsed xlsxContentTypesXML
	if err := xml.Unmarshal(raw, &parsed); err != nil {
		return nil, "", err
	}
	values := map[string]string{}
	canonical := map[string]string{}
	for _, item := range parsed.Defaults {
		extension := strings.ToLower(strings.TrimSpace(item.Extension))
		if extension == "" || strings.TrimSpace(item.ContentType) == "" {
			return nil, "", fmt.Errorf("empty default content type")
		}
		values["*."+extension] = strings.TrimSpace(item.ContentType)
		canonical["default:"+extension] = strings.TrimSpace(item.ContentType)
	}
	for _, item := range parsed.Overrides {
		name := strings.TrimPrefix(path.Clean(item.PartName), "/")
		if name == "" || strings.TrimSpace(item.ContentType) == "" {
			return nil, "", fmt.Errorf("empty override content type")
		}
		values[name] = strings.TrimSpace(item.ContentType)
		canonical["override:"+name] = strings.TrimSpace(item.ContentType)
	}
	return values, xlsxJSONHash(canonical), nil
}

type xlsxRelationshipsXML struct {
	Relationships []struct {
		ID         string `xml:"Id,attr"`
		Type       string `xml:"Type,attr"`
		Target     string `xml:"Target,attr"`
		TargetMode string `xml:"TargetMode,attr"`
	} `xml:"Relationship"`
}

func parseXLSXRelationships(parts map[string][]byte) ([]XLSXPackageRelationship, error) {
	relationships := []XLSXPackageRelationship{}
	for name, raw := range parts {
		if !strings.HasSuffix(name, ".rels") {
			continue
		}
		var parsed xlsxRelationshipsXML
		if err := xml.Unmarshal(raw, &parsed); err != nil {
			return nil, fmt.Errorf("relationship part is malformed: %s", name)
		}
		source, err := xlsxRelationshipSource(name)
		if err != nil {
			return nil, err
		}
		seenIDs := map[string]bool{}
		for _, item := range parsed.Relationships {
			id := strings.TrimSpace(item.ID)
			target := strings.TrimSpace(item.Target)
			if id == "" || target == "" || seenIDs[id] {
				return nil, fmt.Errorf("relationship part has an empty or duplicate relationship: %s", name)
			}
			seenIDs[id] = true
			mode := strings.TrimSpace(item.TargetMode)
			resolved := target
			if !strings.EqualFold(mode, "External") {
				resolved, err = xlsxResolveRelationshipTarget(source, target)
				if err != nil {
					return nil, err
				}
				if _, ok := parts[resolved]; !ok {
					return nil, fmt.Errorf("relationship target is missing: %s -> %s", source, resolved)
				}
			}
			relationships = append(relationships, XLSXPackageRelationship{
				Source: source, ID: id, Type: strings.TrimSpace(item.Type), Target: resolved, TargetMode: mode,
			})
		}
	}
	slices.SortFunc(relationships, func(left, right XLSXPackageRelationship) int {
		return strings.Compare(xlsxRelationshipKey(left), xlsxRelationshipKey(right))
	})
	return relationships, nil
}

func xlsxRelationshipSource(name string) (string, error) {
	if name == "_rels/.rels" {
		return "", nil
	}
	marker := "/_rels/"
	index := strings.LastIndex(name, marker)
	if index < 0 || !strings.HasSuffix(name, ".rels") {
		return "", fmt.Errorf("relationship part path is invalid: %s", name)
	}
	return name[:index+1] + strings.TrimSuffix(name[index+len(marker):], ".rels"), nil
}

func xlsxResolveRelationshipTarget(source, target string) (string, error) {
	if strings.HasPrefix(target, "/") {
		target = strings.TrimPrefix(path.Clean(target), "/")
	} else {
		target = path.Clean(path.Join(path.Dir(source), target))
	}
	if target == "" || target == "." || strings.HasPrefix(target, "../") {
		return "", fmt.Errorf("relationship target escapes the OOXML package")
	}
	return target, nil
}

type xlsxWorkbookXML struct {
	Sheets []struct {
		Name  string `xml:"name,attr"`
		RelID string `xml:"id,attr"`
	} `xml:"sheets>sheet"`
}

func parseXLSXSheetParts(raw []byte, relationships []XLSXPackageRelationship, parts map[string][]byte) (map[string]string, error) {
	var workbook xlsxWorkbookXML
	if err := xml.Unmarshal(raw, &workbook); err != nil {
		return nil, fmt.Errorf("workbook XML is malformed")
	}
	byID := map[string]XLSXPackageRelationship{}
	for _, relationship := range relationships {
		if relationship.Source == "xl/workbook.xml" {
			byID[relationship.ID] = relationship
		}
	}
	sheets := map[string]string{}
	for _, sheet := range workbook.Sheets {
		name := strings.TrimSpace(sheet.Name)
		relationship, ok := byID[strings.TrimSpace(sheet.RelID)]
		if name == "" || !ok || !strings.Contains(strings.ToLower(relationship.Type), "/worksheet") {
			return nil, fmt.Errorf("workbook worksheet relationship is missing or malformed")
		}
		if _, exists := parts[relationship.Target]; !exists {
			return nil, fmt.Errorf("workbook worksheet part is missing: %s", relationship.Target)
		}
		if _, exists := sheets[name]; exists {
			return nil, fmt.Errorf("workbook has duplicate worksheet name: %s", name)
		}
		sheets[name] = relationship.Target
	}
	if len(sheets) == 0 {
		return nil, fmt.Errorf("workbook has no worksheets")
	}
	return sheets, nil
}

func xlsxPartContentType(name string, values map[string]string) string {
	if value := values[name]; value != "" {
		return value
	}
	return values["*."+strings.ToLower(strings.TrimPrefix(path.Ext(name), "."))]
}

func xlsxRelationshipGraphHash(values []XLSXPackageRelationship) string {
	canonical := make([]string, 0, len(values))
	for _, value := range values {
		canonical = append(canonical, xlsxRelationshipKey(value))
	}
	return xlsxJSONHash(canonical)
}

func xlsxRelationshipKey(value XLSXPackageRelationship) string {
	return strings.Join([]string{value.Source, value.ID, value.Type, value.Target, strings.ToLower(value.TargetMode)}, "\x00")
}

func xlsxXMLSemanticHash(raw []byte) string {
	decoder := xml.NewDecoder(bytes.NewReader(raw))
	tokens := []any{}
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return ""
		}
		switch current := token.(type) {
		case xml.StartElement:
			attributes := make([]string, 0, len(current.Attr))
			for _, attribute := range current.Attr {
				attributes = append(attributes, attribute.Name.Space+":"+attribute.Name.Local+"="+attribute.Value)
			}
			slices.Sort(attributes)
			tokens = append(tokens, []any{"start", current.Name.Space, current.Name.Local, attributes})
		case xml.EndElement:
			tokens = append(tokens, []any{"end", current.Name.Space, current.Name.Local})
		case xml.CharData:
			if value := strings.TrimSpace(string(current)); value != "" {
				tokens = append(tokens, []any{"text", value})
			}
		}
	}
	return xlsxJSONHash(tokens)
}

func xlsxRawHash(raw []byte) string {
	digest := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func xlsxJSONHash(value any) string {
	raw, _ := json.Marshal(value)
	return xlsxRawHash(raw)
}

func xlsxWorksheetElementDetector(localName string) func(xlsxPackageScan) []string {
	return func(scan xlsxPackageScan) []string {
		matches := []string{}
		for name, raw := range scan.parts {
			if strings.HasPrefix(name, "xl/worksheets/") && strings.HasSuffix(name, ".xml") && xlsxXMLContainsElement(raw, localName) {
				matches = append(matches, name)
			}
		}
		return matches
	}
}

func xlsxPartPrefixDetector(prefix string) func(xlsxPackageScan) []string {
	return func(scan xlsxPackageScan) []string {
		matches := []string{}
		for name := range scan.parts {
			if name == prefix || strings.HasSuffix(prefix, "/") && strings.HasPrefix(name, prefix) {
				matches = append(matches, name)
			}
		}
		return matches
	}
}

func xlsxPartContainsDetector(fragment string) func(xlsxPackageScan) []string {
	return func(scan xlsxPackageScan) []string {
		matches := []string{}
		for name := range scan.parts {
			if strings.Contains(strings.ToLower(name), strings.ToLower(fragment)) {
				matches = append(matches, name)
			}
		}
		return matches
	}
}

func xlsxCommentsDetector(scan xlsxPackageScan) []string {
	matches := []string{}
	for name := range scan.parts {
		if strings.HasPrefix(name, "xl/comments") || strings.HasPrefix(name, "xl/threadedComments/") || strings.Contains(name, "vmlDrawing") {
			matches = append(matches, name)
		}
	}
	for _, relationship := range scan.relationships {
		kind := strings.ToLower(relationship.Type)
		if strings.HasSuffix(kind, "/comments") || strings.HasSuffix(kind, "/vmldrawing") || strings.HasSuffix(kind, "/threadedcomment") {
			matches = append(matches, relationship.Source)
		}
	}
	return matches
}

func xlsxHyperlinksDetector(scan xlsxPackageScan) []string {
	matches := xlsxWorksheetElementDetector("hyperlink")(scan)
	for _, relationship := range scan.relationships {
		if strings.HasSuffix(strings.ToLower(relationship.Type), "/hyperlink") {
			matches = append(matches, relationship.Source)
		}
	}
	return matches
}

func xlsxImagesDetector(scan xlsxPackageScan) []string {
	matches := []string{}
	for name := range scan.parts {
		if strings.HasPrefix(name, "xl/media/") || strings.HasPrefix(name, "xl/drawings/drawing") {
			matches = append(matches, name)
		}
	}
	return matches
}

func xlsxTablesDetector(scan xlsxPackageScan) []string {
	matches := xlsxPartPrefixDetector("xl/tables/")(scan)
	return append(matches, xlsxWorksheetElementDetector("tableParts")(scan)...)
}

func xlsxExternalLinksDetector(scan xlsxPackageScan) []string {
	matches := xlsxPartPrefixDetector("xl/externalLinks/")(scan)
	for _, relationship := range scan.relationships {
		if strings.EqualFold(relationship.TargetMode, "External") && !strings.HasSuffix(strings.ToLower(relationship.Type), "/hyperlink") {
			matches = append(matches, relationship.Source)
		}
	}
	return matches
}

func xlsxMacrosDetector(scan xlsxPackageScan) []string {
	matches := []string{}
	for name := range scan.parts {
		lower := strings.ToLower(name)
		if strings.Contains(lower, "vbaproject") || strings.HasSuffix(lower, ".bin") {
			matches = append(matches, name)
		}
	}
	return matches
}

func xlsxSignaturesDetector(scan xlsxPackageScan) []string {
	matches := []string{}
	for name := range scan.parts {
		if strings.HasPrefix(strings.ToLower(name), "_xmlsignatures/") || strings.Contains(strings.ToLower(name), "signature") {
			matches = append(matches, name)
		}
	}
	return matches
}

func xlsxProtectionDetector(scan xlsxPackageScan) []string {
	matches := []string{}
	for name, raw := range scan.parts {
		if (name == "xl/workbook.xml" && xlsxXMLContainsElement(raw, "workbookProtection")) ||
			(strings.HasPrefix(name, "xl/worksheets/") && xlsxXMLContainsElement(raw, "sheetProtection")) {
			matches = append(matches, name)
		}
	}
	return matches
}

func xlsxEncryptionDetector(scan xlsxPackageScan) []string {
	matches := []string{}
	for name := range scan.parts {
		if strings.EqualFold(name, "EncryptionInfo") || strings.EqualFold(name, "EncryptedPackage") {
			matches = append(matches, name)
		}
	}
	return matches
}

func xlsxXMLContainsElement(raw []byte, localName string) bool {
	decoder := xml.NewDecoder(bytes.NewReader(raw))
	for {
		token, err := decoder.Token()
		if err != nil {
			return false
		}
		if start, ok := token.(xml.StartElement); ok && start.Name.Local == localName {
			return true
		}
	}
}

func xlsxKnownCorePart(name string) bool {
	if name == "[Content_Types].xml" || name == "_rels/.rels" || name == "xl/workbook.xml" || name == "xl/_rels/workbook.xml.rels" ||
		name == "xl/sharedStrings.xml" || name == "xl/styles.xml" {
		return true
	}
	for _, prefix := range []string{"docProps/", "xl/worksheets/", "xl/theme/"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func xlsxPackageError(code ErrorCode, detail string) error {
	return &PipelineError{Code: code, Stage: StageConstrain, Format: app.DocumentFormatXLSX, Detail: detail}
}

func uniqueSortedStrings(values []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" && !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	slices.Sort(out)
	return out
}

func mapKeys[T any](values map[string]T) []string {
	out := make([]string, 0, len(values))
	for key := range values {
		out = append(out, key)
	}
	return out
}

func sameStringSet(left, right []string) bool {
	left = uniqueSortedStrings(left)
	right = uniqueSortedStrings(right)
	return slices.Equal(left, right)
}

func sameStringMap(left, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		other, ok := right[key]
		if !ok || other != value {
			return false
		}
	}
	return true
}

func caseInsensitiveMapValue(values map[string]string, key string) (string, bool) {
	var matched string
	found := false
	for candidate, value := range values {
		if !strings.EqualFold(strings.TrimSpace(candidate), strings.TrimSpace(key)) {
			continue
		}
		if found {
			return "", false
		}
		matched, found = value, true
	}
	return matched, found
}

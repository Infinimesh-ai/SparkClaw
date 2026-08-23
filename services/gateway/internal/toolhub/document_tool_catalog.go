package toolhub

import (
	"fmt"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

type documentToolProvider struct {
	Definition   app.ToolDefinition
	Registration toolRegistration
}

type documentFormatCatalogEntry struct {
	Provider documentFormatProvider
	Tools    func() []documentToolProvider
}

func toolhubDocumentFormatCatalog() []documentFormatCatalogEntry {
	entries := map[string]documentFormatCatalogEntry{
		app.DocumentFormatText: {Provider: textDocumentFormatProvider(), Tools: textDocumentToolProviders},
		app.DocumentFormatDOCX: {Provider: docxDocumentFormatProvider(), Tools: func() []documentToolProvider {
			return formatDocumentToolProviders(docxDocumentFormatProvider(), docxToolDefinitions(), nil)
		}},
		app.DocumentFormatXLSX: {Provider: xlsxDocumentFormatProvider(), Tools: func() []documentToolProvider {
			return formatDocumentToolProviders(xlsxDocumentFormatProvider(), xlsxToolDefinitions(), nil)
		}},
		app.DocumentFormatPPTX: {Provider: pptxDocumentFormatProvider(), Tools: func() []documentToolProvider {
			return formatDocumentToolProviders(pptxDocumentFormatProvider(), pptxToolDefinitions(), nil)
		}},
		app.DocumentFormatPDF: {Provider: pdfDocumentFormatProvider(), Tools: func() []documentToolProvider {
			return formatDocumentToolProviders(pdfDocumentFormatProvider(), pdfToolDefinitions(), map[string]toolRegistration{
				"pdf.extract_text": documentReadRegistration(
					ctxArgsSessionRun((*ToolHub).pdfExtractText), []string{app.DocumentFormatPDF},
					"Extract bounded text and stable page evidence from a workspace PDF, using configured OCR for scanned pages.",
				),
			})
		}},
	}
	catalog := make([]documentFormatCatalogEntry, 0, len(entries))
	for _, spec := range app.DocumentFormatOperationSpecs() {
		entry, ok := entries[spec.Format]
		if !ok {
			panic(fmt.Sprintf("toolhub: document format catalog entry %q is missing", spec.Format))
		}
		catalog = append(catalog, entry)
		delete(entries, spec.Format)
	}
	if len(entries) != 0 {
		panic("toolhub: document format catalog has entries absent from the canonical operation catalog")
	}
	return catalog
}

func documentFormatProvidersFromCatalog() []documentFormatProvider {
	catalog := toolhubDocumentFormatCatalog()
	providers := make([]documentFormatProvider, 0, len(catalog))
	for _, entry := range catalog {
		providers = append(providers, entry.Provider)
	}
	return providers
}

func documentToolProviders() []documentToolProvider {
	providers := sharedDocumentToolProviders()
	seen := map[string]bool{}
	for _, provider := range providers {
		seen[provider.Definition.Name] = true
	}
	for _, entry := range toolhubDocumentFormatCatalog() {
		for _, provider := range entry.Tools() {
			name := provider.Definition.Name
			if name == "" || seen[name] {
				panic(fmt.Sprintf("toolhub: duplicate or empty document tool provider %q", name))
			}
			seen[name] = true
			providers = append(providers, provider)
		}
	}
	return providers
}

func formatDocumentToolProviders(provider documentFormatProvider, definitions []app.ToolDefinition, readRegistrations map[string]toolRegistration) []documentToolProvider {
	providers := make([]documentToolProvider, 0, len(definitions))
	for _, definition := range definitions {
		operations := []string{}
		for _, operation := range provider.OperationOrder {
			candidate, ok := provider.Operations[operation]
			if ok && candidate.ToolName == definition.Name {
				operations = append(operations, operation)
			}
		}
		registration, ok := readRegistrations[definition.Name]
		if len(operations) > 0 {
			first := provider.Operations[operations[0]]
			boundOperation := operations[0]
			if len(operations) > 1 {
				boundOperation = ""
			}
			registration = documentEditRegistration(
				documentEditExecutor(boundOperation), provider.Format, operations[0], first.Summary,
			)
			if first.WhenToUse != "" {
				registration.directory.WhenToUse = first.WhenToUse
			}
			if first.WhenNotToUse != "" {
				registration.directory.WhenNotToUse = first.WhenNotToUse
			}
			registration.capabilities = make([]app.CapabilityDescriptor, 0, len(operations))
			for _, operation := range operations {
				registration.capabilities = append(registration.capabilities, app.CapabilityDescriptor{
					Name: app.ToolCapabilityDocumentEdit,
					Qualifiers: map[string]string{
						app.CapabilityQualifierFormat: provider.Format, app.CapabilityQualifierOperation: operation,
					},
				})
			}
			ok = true
		}
		if !ok {
			panic(fmt.Sprintf("toolhub: document definition %q has no format operation or read registration", definition.Name))
		}
		providers = append(providers, documentToolProvider{Definition: definition, Registration: registration})
	}
	return providers
}

func documentToolDefinitions() []app.ToolDefinition {
	providers := documentToolProviders()
	definitions := make([]app.ToolDefinition, 0, len(providers))
	for _, provider := range providers {
		definitions = append(definitions, provider.Definition)
	}
	return definitions
}

func documentToolRegistrations() map[string]toolRegistration {
	providers := documentToolProviders()
	registrations := make(map[string]toolRegistration, len(providers))
	for _, provider := range providers {
		registrations[provider.Definition.Name] = provider.Registration
	}
	return registrations
}

func sharedDocumentToolProviders() []documentToolProvider {
	filesRead := documentReadRegistration(
		ctxArgsSessionRun((*ToolHub).filesRead),
		documentReadFormats("files.read"),
		"Read one explicitly identified file inside the configured workspace.",
	)
	officeReplace := documentEditRegistration(
		documentEditExecutor(app.DocumentOperationReplaceText),
		app.DocumentFormatDOCX,
		app.DocumentOperationReplaceText,
		"Replace bounded text and write an Office output copy.",
	)
	applyDocumentToolBoundary(&officeReplace, "office.replace_text")
	officeReplace.capabilities = documentEditCapabilities("office.replace_text")
	return []documentToolProvider{
		{Definition: filesReadDefinition(), Registration: filesRead},
		{Definition: officeReplaceTextDefinition(), Registration: officeReplace},
	}
}

func applyDocumentToolBoundary(registration *toolRegistration, toolName string) {
	if registration == nil {
		return
	}
	for _, entry := range toolhubDocumentFormatCatalog() {
		for _, operation := range entry.Provider.OperationOrder {
			candidate, ok := entry.Provider.Operations[operation]
			if !ok || candidate.ToolName != toolName || (candidate.WhenToUse == "" && candidate.WhenNotToUse == "") {
				continue
			}
			registration.directory.WhenToUse = candidate.WhenToUse
			registration.directory.WhenNotToUse = candidate.WhenNotToUse
		}
	}
}

func textDocumentToolProviders() []documentToolProvider {
	return formatDocumentToolProviders(
		textDocumentFormatProvider(), []app.ToolDefinition{textReplaceTextDefinition()}, nil,
	)
}

func documentReadFormats(toolName string) []string {
	formats := []string{}
	for _, entry := range toolhubDocumentFormatCatalog() {
		for _, candidate := range entry.Provider.ReadToolNames {
			if candidate == toolName {
				formats = append(formats, entry.Provider.Format)
				break
			}
		}
	}
	return formats
}

func documentEditCapabilities(toolName string) []app.CapabilityDescriptor {
	capabilities := []app.CapabilityDescriptor{}
	for _, entry := range toolhubDocumentFormatCatalog() {
		provider := entry.Provider
		for _, operation := range provider.OperationOrder {
			candidate, ok := provider.Operations[operation]
			if !ok || candidate.ToolName != toolName {
				continue
			}
			capabilities = append(capabilities, app.CapabilityDescriptor{
				Name: app.ToolCapabilityDocumentEdit,
				Qualifiers: map[string]string{
					app.CapabilityQualifierFormat: provider.Format, app.CapabilityQualifierOperation: operation,
				},
			})
		}
	}
	return capabilities
}

func filesReadDefinition() app.ToolDefinition {
	return app.ToolDefinition{
		Name:        "files.read",
		Description: "Inspect and completely parse one small workspace document into stable blocks, format-specific locations, and categorized high-level evidence. Optional OvisOCR2 page parsing augments explicitly selected images and scanned PDF pages; Fast remains responsible for visual semantics.",
		InputSchema: schema("object", []string{"path"}, map[string]any{
			"path":               map[string]any{"type": "string"},
			"max_bytes":          map[string]any{"type": "number"},
			"image_analysis":     map[string]any{"type": "string", "enum": []string{"none", "targeted", "all"}},
			"image_target_paths": stringArraySchema(),
			"image_question":     stringSchema(),
			"image_required":     booleanSchema(),
		}),
		OutputSchema: objectSchema([]string{"path", "kind", "content", "bytes", "source_bytes", "max_bytes", "truncated", "untrusted", "document"}, map[string]any{
			"path": stringSchema(), "kind": stringSchema(), "content": stringSchema(), "bytes": integerSchema(),
			"source_bytes": integerSchema(), "max_bytes": integerSchema(), "truncated": booleanSchema(),
			"untrusted": booleanSchema(), "document": objectValueSchema(),
		}),
		Risk: app.RiskRead, RequiresApproval: false, Idempotent: true, TimeoutMS: 125000, Sandbox: "forbidden", Audit: "always",
	}
}

func textReplaceTextDefinition() app.ToolDefinition {
	return app.ToolDefinition{
		Name:        "text.replace_text",
		Description: "Replace explicit text pairs in a governed plain-text file and write a new file without overwriting the original.",
		InputSchema: schema("object", []string{"path", "replacements", "output_path"}, map[string]any{
			"path": stringSchema(), "output_path": stringSchema(),
			"replacements": arraySchema(map[string]any{
				"type": "object", "required": []string{"find", "replace"},
				"properties": map[string]any{"find": stringSchema(), "replace": stringSchema()},
			}),
			"expected_replacements": integerSchema(),
		}),
		OutputSchema: objectSchema([]string{"status", "path", "output_path", "replacements", "bytes", "change_summary", "untrusted"}, map[string]any{
			"status": stringSchema(), "path": stringSchema(), "output_path": stringSchema(), "replacements": integerSchema(),
			"bytes": integerSchema(), "change_summary": objectValueSchema(), "untrusted": booleanSchema(),
		}),
		Risk: app.RiskReversible, RequiresApproval: true, Idempotent: false, TimeoutMS: 5000, Sandbox: "optional", Audit: "always",
	}
}

func officeReplaceTextDefinition() app.ToolDefinition {
	return app.ToolDefinition{
		Name:        "office.replace_text",
		Description: "Replace explicit text pairs in a workspace docx/xlsx/pptx and write a new Office file without overwriting the original.",
		InputSchema: schema("object", []string{"path", app.DocumentSourceSHA256Argument, "replacements", "output_path"}, map[string]any{
			"path": stringSchema(), "output_path": stringSchema(), app.DocumentSourceSHA256Argument: stringSchema(),
			"replacements": arraySchema(map[string]any{
				"type": "object", "required": []string{"find", "replace"},
				"properties": map[string]any{"find": stringSchema(), "replace": stringSchema()},
			}),
			"expected_replacements": map[string]any{"type": "number"},
		}),
		OutputSchema: objectSchema([]string{"status", "path", "output_path", "replacements", "bytes", "change_summary", "untrusted"}, map[string]any{
			"status": stringSchema(), "path": stringSchema(), "output_path": stringSchema(), "replacements": integerSchema(),
			"bytes": integerSchema(), "details": arraySchema(objectValueSchema()), "change_summary": objectValueSchema(), "untrusted": booleanSchema(),
		}),
		Risk: app.RiskReversible, RequiresApproval: true, Idempotent: false, TimeoutMS: 5000, Sandbox: "optional", Audit: "always",
	}
}

package agent

import (
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

// The five browser URL helpers are policy bindings over one canonicalizer.
// This matrix pins the deliberate differences between the modes so a future
// edit to the shared core cannot silently change one caller's contract.
func TestCanonicalBrowserURLModes(t *testing.T) {
	ownerTarget := app.BrowserTargetDescriptor{
		TargetKind:      app.BrowserTargetExplicitURL,
		CanonicalURL:    "https://example.com/frozen?owner=1",
		QueryProvenance: app.BrowserQueryOwnerSupplied,
	}
	volatileTarget := app.BrowserTargetDescriptor{
		TargetKind:      app.BrowserTargetExplicitURL,
		CanonicalURL:    "https://example.com/frozen",
		QueryProvenance: app.BrowserQueryProviderVolatile,
	}

	cases := []struct {
		name string
		got  string
		want string
	}{
		{
			// Route freezing drops the fragment but keeps the owner-typed query.
			name: "normalize keeps query drops fragment",
			got:  normalizeBrowserURL("HTTPS://Example.COM/Path?q=1#section"),
			want: "https://example.com/Path?q=1",
		},
		{
			name: "normalize falls back to trimmed input",
			got:  normalizeBrowserURL("  not a url  "),
			want: "not a url",
		},
		{
			// Evidence comparison keeps the fragment: "#/inbox" style in-page
			// routes distinguish document states.
			name: "evidence keeps query and fragment",
			got:  normalizeBrowserEvidenceURL("HTTPS://Example.COM/Path?q=1#section"),
			want: "https://example.com/Path?q=1#section",
		},
		{
			// Result recording drops the provider-volatile live query.
			name: "safe result drops volatile query",
			got:  browserSafeResultURL(volatileTarget, "https://example.com/live?sid=secret#part"),
			want: "https://example.com/live#part",
		},
		{
			// ...but merges the frozen owner query into the live URL.
			name: "safe result merges owner query into live path",
			got:  browserSafeResultURL(ownerTarget, "https://example.com/live?sid=secret"),
			want: "https://example.com/live?owner=1",
		},
		{
			name: "safe result falls back to frozen target",
			got:  browserSafeResultURL(ownerTarget, "not a url"),
			want: "https://example.com/frozen?owner=1",
		},
		{
			// Handoff short-circuits owner-supplied explicit targets to the
			// frozen URL wholesale — live path and all — unlike safe result.
			name: "safe handoff returns frozen owner target wholesale",
			got:  browserSafeHandoffURL(ownerTarget, "https://example.com/live?sid=secret"),
			want: "https://example.com/frozen?owner=1",
		},
		{
			// For non-owner targets handoff always drops the query, never merges.
			name: "safe handoff drops query for volatile target",
			got:  browserSafeHandoffURL(volatileTarget, "https://example.com/live?sid=secret"),
			want: "https://example.com/live",
		},
		{
			// Persistence redaction returns non-http(s) input verbatim: it may
			// be rewriting a URL embedded in free text.
			name: "safe persistence returns non-http input verbatim",
			got:  browserSafePersistenceURL(volatileTarget, "ftp://example.com/file"),
			want: "ftp://example.com/file",
		},
		{
			name: "safe persistence drops volatile query keeps fragment",
			got:  browserSafePersistenceURL(volatileTarget, "https://example.com/live?sid=secret#part"),
			want: "https://example.com/live#part",
		},
		{
			name: "safe persistence merges owner query",
			got:  browserSafePersistenceURL(ownerTarget, "https://example.com/live?sid=secret"),
			want: "https://example.com/live?owner=1",
		},
		{
			// Credentials must never survive into persisted records.
			name: "safe persistence clears URL userinfo",
			got:  browserSafePersistenceURL(volatileTarget, "https://user:pass@example.com/live"),
			want: "https://example.com/live",
		},
	}
	for _, testCase := range cases {
		if testCase.got != testCase.want {
			t.Errorf("%s: got %q want %q", testCase.name, testCase.got, testCase.want)
		}
	}
}

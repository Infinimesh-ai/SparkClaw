package store

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func TestS0PostgresReconciliationManifest(t *testing.T) {
	rootSQL := readS0RootMigration(t)
	goSQL := readS0PostgresSchema(t)
	root := parseS0DDLManifest(t, rootSQL)
	current := parseS0DDLManifest(t, goSQL)

	wantRootTables := strings.Fields(`
		agent_runs approvals artifact_objects audit_events clients episode_summaries eval_runs events memories memory_candidates
		messages model_calls owners pairing_codes reminders run_feedback sessions tool_calls
	`)
	wantCurrentTables := strings.Fields(`
		agent_runs approvals artifact_objects audit_events browser_auth_records browser_login_blocks channel_inbox_updates clients
		connector_settings credential_secrets document_records episode_summaries eval_runs events external_chat_messages external_chat_sessions
		iscp_onboardings mcp_access_tickets mcp_bindings mcp_operations memories memory_candidates message_delivery_records
		message_receive_records messages model_calls notification_bindings owners pairing_codes passive_notifications reminder_deliveries
		reminders run_feedback sessions tool_calls weixin_chat_messages weixin_chat_sessions
	`)
	wantRootIndexes := strings.Fields(`
		approvals_external_ref_idx idx_approvals_status idx_artifact_objects_created idx_artifact_objects_run idx_audit_session_time
		idx_clients_token_hash idx_episode_summaries_session_created idx_eval_runs_started idx_events_session_seq idx_messages_session_created
		idx_model_calls_session_run_started idx_pairing_codes_status_expires idx_run_feedback_run_updated idx_tool_calls_run
		memories_created_at_idx reminders_status_due_time_idx
	`)
	wantCurrentIndexes := strings.Fields(`
		approvals_external_ref_idx browser_auth_lookup_idx browser_login_blocks_active_idx channel_inbox_updates_ready_idx
		document_records_owner_session_activity_idx document_records_session_path_idx external_chat_messages_chat_created_idx
		external_chat_messages_external_idx external_chat_sessions_binding_chat_idx external_chat_sessions_linked_session_idx
		idx_approvals_status idx_artifact_objects_created idx_artifact_objects_run idx_artifact_objects_uri idx_audit_session_time
		idx_clients_token_hash idx_episode_summaries_session_created idx_eval_runs_started idx_events_session_seq idx_messages_session_created
		idx_model_calls_session_run_started idx_pairing_codes_status_expires idx_run_feedback_run_updated idx_tool_calls_run
		iscp_onboardings_owner_created_idx mcp_access_tickets_owner_status_idx mcp_bindings_active_peer_idx mcp_bindings_owner_status_idx
		mcp_operations_binding_updated_idx memories_created_at_idx message_delivery_owner_actor_idx message_receive_owner_actor_idx
		notification_bindings_channel_status_idx owners_external_ref_idx passive_notifications_owner_created_idx
		passive_notifications_owner_unread_idx reminder_deliveries_reminder_id_idx reminders_status_due_time_idx
		weixin_chat_messages_chat_created_idx weixin_chat_messages_external_idx weixin_chat_sessions_binding_user_idx
		weixin_chat_sessions_linked_session_idx
	`)
	assertS0StringSet(t, "root migration tables", mapKeys(root.tables), wantRootTables)
	assertS0StringSet(t, "postgresSchema tables", mapKeys(current.tables), wantCurrentTables)
	assertS0StringSet(t, "root migration indexes", mapKeys(root.indexes), wantRootIndexes)
	assertS0StringSet(t, "postgresSchema indexes", mapKeys(current.indexes), wantCurrentIndexes)

	wantGoOnlyColumnDefinitions := map[string]map[string]string{
		"approvals": {"policy_context": "jsonb"},
		"clients": {
			"actor_id": "text not null default 'owner'",
			"owner_id": "text not null default 'owner'",
		},
		"messages": {
			"attachments":     "jsonb not null default '[]'",
			"requested_media": "jsonb not null default '[]'",
		},
		"owners": {
			"default_binding_id": "text not null default ''",
			"default_channel":    "text not null default ''",
			"external_ref":       "text not null default ''",
			"source":             "text not null default ''",
			"workspace_root":     "text not null default ''",
		},
		"sessions": {
			"hidden":         "boolean not null default false",
			"owner_id":       "text not null default 'owner'",
			"source":         "text not null default 'webchat'",
			"workspace_root": "text not null default ''",
		},
		"tool_calls": {
			"capability":       "text not null default ''",
			"error_code":       "text",
			"policy_context":   "jsonb",
			"scope_revision":   "integer not null default 0",
			"workflow_id":      "text not null default ''",
			"workflow_node_id": "text not null default ''",
		},
	}
	gotGoOnlyColumnDefinitions := map[string]map[string]string{}
	for tableName, rootTable := range root.tables {
		currentTable, exists := current.tables[tableName]
		if !exists {
			t.Errorf("postgresSchema dropped root table %s", tableName)
			continue
		}
		for columnName, rootColumn := range rootTable.columns {
			currentColumn, exists := currentTable.columns[columnName]
			if !exists {
				t.Errorf("postgresSchema table %s dropped root column %s", tableName, columnName)
				continue
			}
			if !reflect.DeepEqual(currentColumn, rootColumn) {
				t.Errorf("shared column definition changed for %s.%s\n root: %#v\n   go: %#v", tableName, columnName, rootColumn, currentColumn)
			}
		}
		for columnName, currentColumn := range currentTable.columns {
			if _, exists := rootTable.columns[columnName]; exists {
				continue
			}
			if gotGoOnlyColumnDefinitions[tableName] == nil {
				gotGoOnlyColumnDefinitions[tableName] = map[string]string{}
			}
			gotGoOnlyColumnDefinitions[tableName][columnName] = currentColumn.definition
		}
		assertS0StringSet(t, "shared table constraints for "+tableName, mapKeys(currentTable.constraints), mapKeys(rootTable.constraints))
	}
	if !reflect.DeepEqual(gotGoOnlyColumnDefinitions, wantGoOnlyColumnDefinitions) {
		t.Fatalf("common-table reconciliation column definitions changed\n got: %#v\nwant: %#v", gotGoOnlyColumnDefinitions, wantGoOnlyColumnDefinitions)
	}

	for indexName, rootDefinition := range root.indexes {
		currentDefinition, exists := current.indexes[indexName]
		if !exists {
			t.Errorf("postgresSchema dropped root index %s", indexName)
			continue
		}
		if currentDefinition != rootDefinition {
			t.Errorf("shared index definition changed for %s\n root: %s\n   go: %s", indexName, rootDefinition, currentDefinition)
		}
	}
	wantGoOnlyIndexDefinitions := map[string]string{
		"browser_auth_lookup_idx":                     "on browser_auth_records(owner_id,browser_profile_id,site_origin,site_realm,account_hint,status,updated_at desc)",
		"browser_login_blocks_active_idx":             "on browser_login_blocks(session_id,status,updated_at desc)",
		"channel_inbox_updates_ready_idx":             "on channel_inbox_updates(channel,status,available_at,created_at)",
		"document_records_owner_session_activity_idx": "on document_records(owner_id,session_id,last_activity_at desc)",
		"document_records_session_path_idx":           "on document_records(session_id,governed_path)",
		"external_chat_messages_chat_created_idx":     "on external_chat_messages(chat_session_id,created_at)",
		"external_chat_messages_external_idx":         "on external_chat_messages(chat_session_id,external_message_id)",
		"external_chat_sessions_binding_chat_idx":     "on external_chat_sessions(binding_id,external_chat_id,external_thread_id)",
		"external_chat_sessions_linked_session_idx":   "on external_chat_sessions(linked_session_id)",
		"idx_artifact_objects_uri":                    "on artifact_objects(uri)",
		"iscp_onboardings_owner_created_idx":          "on iscp_onboardings(owner_id,created_at desc)",
		"mcp_access_tickets_owner_status_idx":         "on mcp_access_tickets(owner_id,status,expires_at desc)",
		"mcp_bindings_active_peer_idx":                "unique on mcp_bindings(domain_id,requester_device_id,requester_key_thumbprint) where status = 'active'",
		"mcp_bindings_owner_status_idx":               "on mcp_bindings(owner_id,status,updated_at desc)",
		"mcp_operations_binding_updated_idx":          "on mcp_operations(binding_id,updated_at desc)",
		"message_delivery_owner_actor_idx":            "on message_delivery_records(owner_id,actor_id,updated_at desc)",
		"message_receive_owner_actor_idx":             "on message_receive_records(owner_id,actor_id,updated_at desc)",
		"notification_bindings_channel_status_idx":    "on notification_bindings(channel,status)",
		"owners_external_ref_idx":                     "on owners(source,external_ref)",
		"passive_notifications_owner_created_idx":     "on passive_notifications(owner_id,created_at desc,id desc)",
		"passive_notifications_owner_unread_idx":      "on passive_notifications(owner_id,created_at desc) where read_at is null",
		"reminder_deliveries_reminder_id_idx":         "on reminder_deliveries(reminder_id)",
		"weixin_chat_messages_chat_created_idx":       "on weixin_chat_messages(chat_session_id,created_at)",
		"weixin_chat_messages_external_idx":           "on weixin_chat_messages(chat_session_id,external_message_id)",
		"weixin_chat_sessions_binding_user_idx":       "on weixin_chat_sessions(binding_id,external_user_id)",
		"weixin_chat_sessions_linked_session_idx":     "on weixin_chat_sessions(linked_session_id)",
	}
	gotGoOnlyIndexDefinitions := mapDifference(current.indexes, root.indexes)
	if !reflect.DeepEqual(gotGoOnlyIndexDefinitions, wantGoOnlyIndexDefinitions) {
		t.Fatalf("Go-only index definitions changed\n got: %#v\nwant: %#v", gotGoOnlyIndexDefinitions, wantGoOnlyIndexDefinitions)
	}

	wantGoOnlyTableConstraints := map[string][]string{
		"channel_inbox_updates":    {"unique(binding_id,external_id)"},
		"connector_settings":       {"primary key(owner_id,channel)"},
		"mcp_operations":           {"unique(binding_id,idempotency_key)"},
		"message_delivery_records": {"unique(owner_id,actor_id,idempotency_key)"},
		"message_receive_records":  {"unique(source_endpoint_id,native_message_id)"},
		"passive_notifications":    {"unique(endpoint_id,idempotency_key)"},
	}
	gotGoOnlyTableConstraints := map[string][]string{}
	for tableName, table := range current.tables {
		if _, shared := root.tables[tableName]; shared || len(table.constraints) == 0 {
			continue
		}
		gotGoOnlyTableConstraints[tableName] = sortedKeys(table.constraints)
	}
	if !reflect.DeepEqual(gotGoOnlyTableConstraints, wantGoOnlyTableConstraints) {
		t.Fatalf("Go-only table constraints changed\n got: %#v\nwant: %#v", gotGoOnlyTableConstraints, wantGoOnlyTableConstraints)
	}
	wantGoOnlyTableDefinitions := map[string]string{
		"browser_auth_records":     "column account_hint text not null default '' | column auth_strategy text not null | column browser_profile_id text not null | column cookie_jar_ref text not null default '' | column created_at timestamptz not null default now() | column credential_ref text not null default '' | column expires_at timestamptz | column id text primary key | column last_error text not null default '' | column last_verified_at timestamptz | column owner_id text not null | column revoked_at timestamptz | column session_ref text not null default '' | column site_origin text not null | column site_realm text not null default '' | column status text not null | column updated_at timestamptz not null default now()",
		"browser_login_blocks":     "column account_hint text not null default '' | column browser_auth_status text not null default '' | column browser_profile_id text not null default 'default' | column created_at timestamptz not null default now() | column id text primary key | column last_error text not null default '' | column last_tool_call_id text not null default '' | column last_user_reply text not null default '' | column last_visible_page_id text not null default '' | column login_handoff_page_id text not null default '' | column login_handoff_url text not null default '' | column original_goal text not null default '' | column owner_id text not null default 'owner' | column resolved_at timestamptz | column resume_args jsonb not null default '{}' | column resume_tool text not null default 'browser.read' | column run_id text not null references agent_runs(id) | column schema_version integer not null default 2 | column session_generation bigint not null default 0 | column session_id text not null references sessions(id) | column site_origin text not null default '' | column site_realm text not null default '' | column status text not null | column target jsonb not null default '{}' | column transition_lease_until timestamptz | column transition_owner_id text not null default '' | column updated_at timestamptz not null default now() | column version bigint not null default 1 | column visible_evidence jsonb not null default 'null' | column workflow_id text not null default '' | column workflow_node_id text not null default '' | column workflow_revision integer not null default 0",
		"channel_inbox_updates":    "column attempts integer not null default 0 | column available_at timestamptz not null default now() | column binding_id text not null | column channel text not null | column chat_key text not null default '' | column created_at timestamptz not null default now() | column external_id text not null | column id text primary key | column last_error text not null default '' | column payload jsonb not null default '{}' | column status text not null | column updated_at timestamptz not null default now() | constraint unique(binding_id,external_id)",
		"connector_settings":       "column channel text not null | column enabled boolean not null default false | column iscp_enabled boolean not null default false | column lan_access_enabled boolean not null default false | column owner_id text not null | column updated_at timestamptz not null default now() | column updated_by text not null default '' | column version bigint not null default 1 | constraint primary key(owner_id,channel)",
		"credential_secrets":       "column created_at timestamptz not null default now() | column kind text not null | column ref text primary key | column updated_at timestamptz not null default now() | column value text not null",
		"document_records":         "column content_type text not null default '' | column created_at timestamptz not null default now() | column format text not null default '' | column governed_path text not null | column id text primary key | column last_activity text not null | column last_activity_at timestamptz not null | column last_activity_id text not null | column name text not null | column owner_id text not null default 'owner' | column parent_document_id text not null default '' | column session_id text not null references sessions(id) | column sha256 text not null default '' | column size_bytes bigint not null default 0 | column source text not null | column source_message_id text not null default '' | column source_run_id text not null default '' | column source_tool_call_id text not null default '' | column status text not null | column updated_at timestamptz not null default now()",
		"external_chat_messages":   "column binding_id text not null | column channel text not null | column chat_session_id text not null | column content text not null | column context_token text not null default '' | column created_at timestamptz not null default now() | column direction text not null | column dispatch_attempts integer not null default 0 | column error text not null default '' | column external_message_id text not null default '' | column id text primary key | column linked_run_id text not null default '' | column pending_reply text not null default '' | column pending_reply_kind text not null default '' | column role text not null | column status text not null | column updated_at timestamptz not null default now()",
		"external_chat_sessions":   "column authorized_actor_id text not null default '' | column authorized_owner_id text not null default '' | column binding_id text not null | column channel text not null | column created_at timestamptz not null default now() | column display_name text not null default '' | column external_chat_id text not null default '' | column external_thread_id text not null default '' | column external_user_id text not null default '' | column id text primary key | column last_context_token text not null default '' | column linked_session_id text not null default '' | column owner_id text not null default '' | column provider text not null default '' | column provider_cursor text not null default '' | column status text not null | column updated_at timestamptz not null default now() | column workspace_root text not null default ''",
		"iscp_onboardings":         "column authority_ref text not null | column created_at timestamptz not null | column domain_id text not null | column id text primary key | column owner_id text not null | column payload jsonb not null | column status text not null | column ticket_id text not null",
		"mcp_access_tickets":       "column domain_id text not null | column expires_at timestamptz not null | column id text primary key | column owner_id text not null | column payload jsonb not null | column secret_hash text not null unique | column status text not null",
		"mcp_bindings":             "column domain_id text not null | column id text primary key | column owner_id text not null | column payload jsonb not null | column requester_device_id text not null | column requester_key_thumbprint text not null | column status text not null | column updated_at timestamptz not null",
		"mcp_operations":           "column binding_id text not null references mcp_bindings(id) | column fingerprint text not null | column id text primary key | column idempotency_key text not null | column payload jsonb not null | column updated_at timestamptz not null | column version bigint not null | constraint unique(binding_id,idempotency_key)",
		"message_delivery_records": "column actor_id text not null | column content_digest text not null | column id text primary key | column idempotency_key text not null | column owner_id text not null | column record jsonb not null | column status text not null | column updated_at timestamptz not null default now() | constraint unique(owner_id,actor_id,idempotency_key)",
		"message_receive_records":  "column actor_id text not null | column id text primary key | column native_message_id text not null | column owner_id text not null | column record jsonb not null | column source_endpoint_id text not null default '' | column status text not null | column updated_at timestamptz not null default now() | constraint unique(source_endpoint_id,native_message_id)",
		"notification_bindings":    "column account_id text not null default '' | column actor_id text not null default '' | column base_url text not null default '' | column channel text not null | column context_token text not null default '' | column created_at timestamptz not null default now() | column credential_ref text not null default '' | column default_for_channel boolean not null default false | column display_name text not null default '' | column expires_at timestamptz | column external_chat_id text not null default '' | column external_thread_id text not null default '' | column external_user_id text not null default '' | column id text primary key | column last_error text not null default '' | column owner_id text not null | column provider text not null | column provider_cursor text not null default '' | column provider_session_id text not null default '' | column provider_state text not null default '' | column qr_code_image text not null default '' | column qr_code_url text not null default '' | column revoked_at timestamptz | column scopes jsonb not null default '[]' | column status text not null | column updated_at timestamptz not null default now()",
		"passive_notifications":    "column created_at timestamptz not null default now() | column deep_link text not null | column endpoint_id text not null | column fingerprint text not null | column id text primary key | column idempotency_key text not null | column kind text not null | column notification_id text not null | column occurred_at timestamptz not null | column owner_id text not null | column read_at timestamptz | column source text not null | column updated_at timestamptz not null default now() | constraint unique(endpoint_id,idempotency_key)",
		"reminder_deliveries":      "column attempt integer not null default 0 | column channel text not null | column created_at timestamptz not null default now() | column error text not null default '' | column id text primary key | column provider text not null | column provider_status text not null default '' | column recipient text not null default '' | column reminder_id text not null references reminders(id) | column retry_state text not null default '' | column sent_at timestamptz | column status text not null",
		"weixin_chat_messages":     "column binding_id text not null | column chat_session_id text not null | column content text not null | column context_token text not null default '' | column created_at timestamptz not null default now() | column direction text not null | column error text not null default '' | column external_message_id text not null default '' | column id text primary key | column linked_run_id text not null default '' | column role text not null | column status text not null | column updated_at timestamptz not null default now()",
		"weixin_chat_sessions":     "column binding_id text not null | column channel text not null default 'weixin' | column created_at timestamptz not null default now() | column display_name text not null default '' | column external_user_id text not null default '' | column id text primary key | column last_context_token text not null default '' | column linked_session_id text not null default '' | column owner_id text not null default '' | column provider text not null default '' | column provider_cursor text not null default '' | column status text not null | column updated_at timestamptz not null default now() | column workspace_root text not null default ''",
	}
	gotGoOnlyTableDefinitions := map[string]string{}
	for tableName, table := range current.tables {
		if _, shared := root.tables[tableName]; !shared {
			gotGoOnlyTableDefinitions[tableName] = canonicalS0TableDefinition(table)
		}
	}
	if !reflect.DeepEqual(gotGoOnlyTableDefinitions, wantGoOnlyTableDefinitions) {
		t.Fatalf("Go-only full table definitions changed\n got: %#v\nwant: %#v", gotGoOnlyTableDefinitions, wantGoOnlyTableDefinitions)
	}
	assertS0GoOnlyTableDefinitionsDocumented(t, wantGoOnlyTableDefinitions)

	// These categories are parsed, rather than silently discarded. Their empty
	// sets are therefore evidence that the current sources contain none.
	assertS0StringSet(t, "root CHECK constraints", root.syntax.checks, nil)
	assertS0StringSet(t, "postgresSchema CHECK constraints", current.syntax.checks, nil)
	assertS0StringSet(t, "root named constraints", root.syntax.namedConstraints, nil)
	assertS0StringSet(t, "postgresSchema named constraints", current.syntax.namedConstraints, nil)
	assertS0StringSet(t, "root table-level foreign keys", root.syntax.tableForeignKeys, nil)
	assertS0StringSet(t, "postgresSchema table-level foreign keys", current.syntax.tableForeignKeys, nil)
	assertS0StringSet(t, "root ALTER constraint statements", root.syntax.alterConstraints, nil)
	assertS0StringSet(t, "postgresSchema ALTER constraint statements", current.syntax.alterConstraints, nil)
	assertS0StringSet(t, "root unparsed DDL", root.syntax.unparsedDDL, nil)
	assertS0StringSet(t, "postgresSchema unparsed DDL", current.syntax.unparsedDDL, nil)

	// DML is deliberately counted but not interpreted as schema. The five Go
	// statements are two compatibility copies and three data normalizations.
	if root.syntax.dmlStatements != 0 || current.syntax.dmlStatements != 5 {
		t.Fatalf("non-schema DML inventory changed: root=%d postgresSchema=%d", root.syntax.dmlStatements, current.syntax.dmlStatements)
	}
}

func assertS0GoOnlyTableDefinitionsDocumented(t *testing.T, definitions map[string]string) {
	t.Helper()
	paths := []string{
		filepath.Join("..", "..", "..", "..", "docs", "store-s0-postgresql-reconciliation-manifest.md"),
		filepath.Join("..", "..", "..", "..", "zh-cn", "docs", "store-s0-postgresql-reconciliation-manifest.md"),
	}
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		text := string(raw)
		for table, definition := range definitions {
			entry := table + ": " + definition
			if strings.Count(text, entry) != 1 {
				t.Errorf("%s must contain exactly one full definition for %s", path, table)
			}
		}
	}
}

func canonicalS0TableDefinition(table *s0DDLTable) string {
	parts := make([]string, 0, len(table.columns)+len(table.constraints))
	for _, columnName := range sortedKeys(table.columns) {
		parts = append(parts, "column "+columnName+" "+table.columns[columnName].definition)
	}
	for _, constraint := range sortedKeys(table.constraints) {
		parts = append(parts, "constraint "+constraint)
	}
	return strings.Join(parts, " | ")
}

type s0DDLManifest struct {
	tables  map[string]*s0DDLTable
	indexes map[string]string
	syntax  s0DDLSyntaxInventory
}

type s0DDLTable struct {
	columns     map[string]s0DDLColumn
	constraints map[string]s0DDLConstraint
}

type s0DDLColumn struct {
	definition string
	dataType   string
	primaryKey bool
	foreignKey string
	unique     bool
	notNull    bool
	defaultSQL string
	checkSQL   string
}

type s0DDLConstraint struct {
	kind       string
	definition string
	name       string
}

type s0DDLSyntaxInventory struct {
	checks           []string
	namedConstraints []string
	tableForeignKeys []string
	alterConstraints []string
	unparsedDDL      []string
	dmlStatements    int
}

var (
	s0CreateTablePattern     = regexp.MustCompile(`(?is)^create\s+table\s+if\s+not\s+exists\s+([a-zA-Z0-9_]+)\s*\((.*)\)$`)
	s0AddColumnPattern       = regexp.MustCompile(`(?is)^alter\s+table\s+([a-zA-Z0-9_]+)\s+add\s+column\s+if\s+not\s+exists\s+([a-zA-Z0-9_]+)\s+(.+)$`)
	s0DropNotNullPattern     = regexp.MustCompile(`(?is)^alter\s+table\s+([a-zA-Z0-9_]+)\s+alter\s+column\s+([a-zA-Z0-9_]+)\s+drop\s+not\s+null$`)
	s0CreateIndexPattern     = regexp.MustCompile(`(?is)^create\s+(unique\s+)?index\s+if\s+not\s+exists\s+([a-zA-Z0-9_]+)\s+(on\s+.+)$`)
	s0ConstraintPattern      = regexp.MustCompile(`(?is)^(?:constraint\s+([a-zA-Z0-9_]+)\s+)?(primary\s+key|foreign\s+key|unique|check)\b(.*)$`)
	s0ReferencePattern       = regexp.MustCompile(`(?i)\breferences\s+([a-zA-Z0-9_]+\s*\([^)]*\))`)
	s0DefaultPattern         = regexp.MustCompile(`(?i)\bdefault\s+(.+)$`)
	s0InlineCheckPattern     = regexp.MustCompile(`(?i)\bcheck\s*(\(.*\))`)
	s0AlterConstraintPattern = regexp.MustCompile(
		`(?is)^alter\s+table\s+.+\b(?:add|drop)\s+(?:constraint|primary\s+key|foreign\s+key|unique|check)\b`,
	)
)

func parseS0DDLManifest(t *testing.T, sql string) s0DDLManifest {
	t.Helper()
	manifest := s0DDLManifest{
		tables:  map[string]*s0DDLTable{},
		indexes: map[string]string{},
	}
	for _, statement := range splitS0SQLStatements(stripS0SQLComments(sql)) {
		canonical := normalizeS0SQL(statement)
		if canonical == "" {
			continue
		}
		if match := s0CreateTablePattern.FindStringSubmatch(canonical); match != nil {
			parseS0CreateTable(t, &manifest, match[1], match[2])
			continue
		}
		if match := s0AddColumnPattern.FindStringSubmatch(canonical); match != nil {
			applyS0AddColumn(t, &manifest, match[1], match[2], match[3])
			continue
		}
		if match := s0DropNotNullPattern.FindStringSubmatch(canonical); match != nil {
			applyS0DropNotNull(t, &manifest, match[1], match[2])
			continue
		}
		if match := s0CreateIndexPattern.FindStringSubmatch(canonical); match != nil {
			name := strings.ToLower(match[2])
			definition := normalizeS0SQL(match[3])
			if strings.TrimSpace(match[1]) != "" {
				definition = "unique " + definition
			}
			manifest.indexes[name] = definition
			continue
		}
		if s0AlterConstraintPattern.MatchString(canonical) {
			manifest.syntax.alterConstraints = append(manifest.syntax.alterConstraints, canonical)
			continue
		}
		if strings.HasPrefix(canonical, "insert ") || strings.HasPrefix(canonical, "update ") || strings.HasPrefix(canonical, "delete ") {
			manifest.syntax.dmlStatements++
			continue
		}
		if strings.HasPrefix(canonical, "create ") || strings.HasPrefix(canonical, "alter ") || strings.HasPrefix(canonical, "drop ") {
			manifest.syntax.unparsedDDL = append(manifest.syntax.unparsedDDL, canonical)
		}
	}
	return manifest
}

func parseS0CreateTable(t *testing.T, manifest *s0DDLManifest, rawName, body string) {
	t.Helper()
	tableName := strings.ToLower(rawName)
	if _, exists := manifest.tables[tableName]; exists {
		t.Fatalf("duplicate CREATE TABLE for %s", tableName)
	}
	table := &s0DDLTable{
		columns:     map[string]s0DDLColumn{},
		constraints: map[string]s0DDLConstraint{},
	}
	for _, element := range splitS0TopLevel(body, ',') {
		canonical := normalizeS0SQL(element)
		if canonical == "" {
			continue
		}
		if match := s0ConstraintPattern.FindStringSubmatch(canonical); match != nil {
			constraint := s0DDLConstraint{
				kind:       normalizeS0SQL(match[2]),
				definition: canonical,
				name:       strings.ToLower(match[1]),
			}
			if constraint.name != "" {
				manifest.syntax.namedConstraints = append(manifest.syntax.namedConstraints, tableName+"."+constraint.name)
				constraint.definition = normalizeS0SQL(strings.TrimSpace(match[2] + match[3]))
			}
			table.constraints[constraint.definition] = constraint
			switch constraint.kind {
			case "check":
				manifest.syntax.checks = append(manifest.syntax.checks, tableName+"."+constraint.definition)
			case "foreign key":
				manifest.syntax.tableForeignKeys = append(manifest.syntax.tableForeignKeys, tableName+"."+constraint.definition)
			}
			continue
		}
		fields := strings.Fields(canonical)
		if len(fields) < 2 {
			t.Fatalf("cannot parse column in %s: %q", tableName, canonical)
		}
		columnName := strings.ToLower(fields[0])
		definition := strings.TrimSpace(strings.TrimPrefix(canonical, fields[0]))
		column := parseS0Column(definition)
		if column.checkSQL != "" {
			manifest.syntax.checks = append(manifest.syntax.checks, tableName+"."+columnName+" "+column.checkSQL)
		}
		table.columns[columnName] = column
	}
	manifest.tables[tableName] = table
}

func applyS0AddColumn(t *testing.T, manifest *s0DDLManifest, rawTable, rawColumn, rawDefinition string) {
	t.Helper()
	tableName := strings.ToLower(rawTable)
	columnName := strings.ToLower(rawColumn)
	table := manifest.tables[tableName]
	if table == nil {
		t.Fatalf("ALTER TABLE references unknown table %s", tableName)
	}
	column := parseS0Column(rawDefinition)
	if existing, exists := table.columns[columnName]; exists {
		if !reflect.DeepEqual(existing, column) {
			t.Fatalf("ALTER ADD COLUMN definition differs from CREATE TABLE for %s.%s\ncreate: %#v\n alter: %#v", tableName, columnName, existing, column)
		}
		return
	}
	table.columns[columnName] = column
}

func applyS0DropNotNull(t *testing.T, manifest *s0DDLManifest, rawTable, rawColumn string) {
	t.Helper()
	tableName := strings.ToLower(rawTable)
	columnName := strings.ToLower(rawColumn)
	table := manifest.tables[tableName]
	if table == nil {
		t.Fatalf("ALTER TABLE references unknown table %s", tableName)
	}
	column, exists := table.columns[columnName]
	if !exists {
		t.Fatalf("ALTER COLUMN references unknown column %s.%s", tableName, columnName)
	}
	column.definition = normalizeS0SQL(strings.Replace(column.definition, " not null", "", 1))
	column.notNull = false
	table.columns[columnName] = column
}

func parseS0Column(rawDefinition string) s0DDLColumn {
	definition := normalizeS0SQL(rawDefinition)
	fields := strings.Fields(definition)
	column := s0DDLColumn{definition: definition}
	if len(fields) != 0 {
		column.dataType = fields[0]
	}
	column.primaryKey = strings.Contains(" "+definition+" ", " primary key ")
	column.unique = strings.Contains(" "+definition+" ", " unique ")
	column.notNull = strings.Contains(" "+definition+" ", " not null ")
	if match := s0ReferencePattern.FindStringSubmatch(definition); match != nil {
		column.foreignKey = normalizeS0SQL(match[1])
	}
	if match := s0DefaultPattern.FindStringSubmatch(definition); match != nil {
		column.defaultSQL = normalizeS0SQL(match[1])
	}
	if match := s0InlineCheckPattern.FindStringSubmatch(definition); match != nil {
		column.checkSQL = "check" + normalizeS0SQL(match[1])
	}
	return column
}

func stripS0SQLComments(sql string) string {
	lines := strings.Split(sql, "\n")
	for i, line := range lines {
		if comment := strings.Index(line, "--"); comment >= 0 {
			lines[i] = line[:comment]
		}
	}
	return strings.Join(lines, "\n")
}

func splitS0SQLStatements(sql string) []string {
	return splitS0SQL(sql, ';', false)
}

func splitS0TopLevel(sql string, separator rune) []string {
	return splitS0SQL(sql, separator, true)
}

func splitS0SQL(sql string, separator rune, topLevelOnly bool) []string {
	parts := make([]string, 0)
	start := 0
	depth := 0
	inQuote := false
	for index, char := range sql {
		switch char {
		case '\'':
			inQuote = !inQuote
		case '(':
			if !inQuote {
				depth++
			}
		case ')':
			if !inQuote {
				depth--
			}
		}
		if char == separator && !inQuote && (!topLevelOnly || depth == 0) {
			parts = append(parts, sql[start:index])
			start = index + 1
		}
	}
	parts = append(parts, sql[start:])
	return parts
}

func normalizeS0SQL(value string) string {
	normalized := strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(value)), " "))
	replacer := strings.NewReplacer(" (", "(", "( ", "(", " )", ")", " ,", ",", ", ", ",")
	return replacer.Replace(normalized)
}

func readS0RootMigration(t *testing.T) string {
	t.Helper()
	path := filepath.Join("..", "..", "..", "..", "migrations", "0001_core.sql")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func readS0PostgresSchema(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("postgres.go")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := parser.ParseFile(token.NewFileSet(), "postgres.go", raw, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, declaration := range parsed.Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, spec := range general.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok || len(value.Names) != 1 || value.Names[0].Name != "postgresSchema" || len(value.Values) != 1 {
				continue
			}
			literal, ok := value.Values[0].(*ast.BasicLit)
			if !ok {
				t.Fatal("postgresSchema is no longer a string literal")
			}
			out, err := strconv.Unquote(literal.Value)
			if err != nil {
				t.Fatal(err)
			}
			return out
		}
	}
	t.Fatal("postgresSchema constant not found")
	return ""
}

func assertS0StringSet(t *testing.T, label string, got, want []string) {
	t.Helper()
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s changed\n got: %v\nwant: %v", label, got, want)
	}
}

func mapKeys[V any](values map[string]V) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	return out
}

func sortedKeys[V any](values map[string]V) []string {
	out := mapKeys(values)
	sort.Strings(out)
	return out
}

func mapDifference[V comparable](left, right map[string]V) map[string]V {
	out := map[string]V{}
	for key, value := range left {
		if _, exists := right[key]; !exists {
			out[key] = value
		}
	}
	return out
}

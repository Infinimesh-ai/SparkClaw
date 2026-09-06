package config

import (
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/weixinproto"
)

func Default() Config {
	cfg := Config{
		Gateway: GatewayConfig{
			Bind:            "127.0.0.1",
			Port:            18789,
			PairingRequired: false,
			RemoteAccess:    "disabled",
			RateLimit: RateLimitConfig{
				Enabled:           true,
				RequestsPerMinute: 600,
				Burst:             120,
			},
		},
		JingSiLAN: JingSiLANConfig{
			Enabled:         false,
			MaxMessageBytes: 64 << 10,
		},
		JingSiRuntime: JingSiRuntimeConfig{
			Enabled: false, StateDir: "./data/jingsi-runtime-v1", MaxConcurrent: 4, RetentionDays: 30, EnforceEffectScopes: true,
		},
		Model: ModelConfig{
			CapacityProfile:    "dgx-spark-dual-light-v1",
			CapacityCatalog:    defaultModelCapacityCatalog,
			Mock:               false,
			HTTPTimeoutSeconds: 300,
			// Matches configs/sparkclaw.default.json: the local model
			// lanes run without thinking so bounded max_tokens (e.g. the
			// guard lane's 128) are spent on the answer, not reasoning.
			DisableThinking: true,
			Fast: ModelProfile{
				Name:    "sparkclaw-fast",
				BaseURL: "http://127.0.0.1:8001/v1",
				Model:   "nvidia/Qwen3.6-35B-A3B-NVFP4",
			},
			Deep: ModelProfile{
				Name:    "sparkclaw-deep",
				BaseURL: "http://127.0.0.1:8002/v1",
				Model:   "nvidia/Qwen3.6-35B-A3B-NVFP4",
			},
			Embedding: ModelProfile{
				Name:    "sparkclaw-embedding",
				BaseURL: "http://127.0.0.1:8003/v1",
				Model:   "Qwen/Qwen3-Embedding-0.6B",
			},
			Guard: ModelProfile{
				Name:    "sparkclaw-guard",
				BaseURL: "http://127.0.0.1:8005/v1",
				Model:   "Qwen/Qwen3Guard-Gen-0.6B",
			},
		},
		Speech: SpeechConfig{
			Enabled:         false,
			Backend:         "openai-http",
			BaseURL:         "",
			AllowedHosts:    nil,
			Model:           "sparkclaw-asr",
			DefaultLanguage: "auto",
			TimeoutSeconds:  120,
			MaxAudioSeconds: 60,
			MaxUploadBytes:  3 << 20,
			MaxConcurrency:  1,
			MaxPending:      1,
			RetainAudio:     false,
		},
		ISCPPairing: ISCPPairingConfig{
			Enabled: false, RequestTimeoutSeconds: 15, ResponseBodyMaxBytes: 64 << 10,
			TicketTTLSeconds: 600, ExpectedTicketType: "iscp.pairing_ticket.v2",
		},
		MCPAccess: MCPAccessConfig{
			LocalDomainID: "sparkclaw-local",
		},
		Plugins: PluginsConfig{
			Entries: PluginEntriesConfig{
				InfinimeshInfo: InfinimeshInfoPluginConfig{
					Config: InfinimeshInfoConfig{
						BaseURL:               "https://info.infinimesh.cloud",
						TokenBatchSize:        10,
						MaxAttempts:           3,
						RetryBaseDelayMS:      200,
						RequestTimeoutSeconds: 30,
						ResponseBodyMaxBytes:  4 << 20,
						Language:              "zh-CN",
						MaxSources:            8,
					},
				},
			},
		},
		Tools: ToolsConfig{
			Web: WebToolsConfig{
				Search: WebSearchToolConfig{
					Enabled:  false,
					Provider: "infinimesh-info",
				},
			},
			BrowserAutomation: BrowserAutomationToolConfig{
				Enabled:  false,
				Provider: "playwright-extension",
				Profile:  "default",
			},
			Reminders: RemindersToolConfig{
				Enabled:             true,
				DefaultChannel:      "web",
				MaxDeliveryAttempts: 8,
			},
			Notifications: NotificationsToolConfig{
				Channels: map[string]NotificationChannelConfig{
					"telegram": {
						Enabled:            false,
						Provider:           "telegram-bot-api",
						BaseURL:            "https://api.telegram.org",
						UpdateMode:         "long-polling",
						PollTimeoutSeconds: 30,
						PrivateChatsOnly:   true,
						MaxDownloadBytes:   20 << 20,
						MaxAttachments:     5,
						MaxVoiceSeconds:    120,
						MaxConcurrency:     4,
						MaxPending:         32,
					},
					"weixin": {
						Enabled:    false,
						Provider:   weixinproto.QRProvider,
						BaseURL:    "https://ilinkai.weixin.qq.com",
						CDNBaseURL: "https://novac2c.cdn.weixin.qq.com/c2c",
					},
					"mcp": {
						Enabled:  false,
						Provider: "iscp-mcp",
					},
				},
			},
		},
		MCPServers: map[string]MCPServerConfig{},
		Security: SecurityConfig{
			ExternalContentUntrusted:              true,
			ApprovalRequiredForDangerousTools:     true,
			SandboxRequiredForMutatingTools:       true,
			DangerousToolsRequireDeepVerification: true,
			DeniedTools: []string{
				"host_shell.exec",
				"file.delete.permanent",
				"browser.submit_form.auto",
			},
			ApprovalRequiredTools: []string{},
			ToolPolicyPath:        "./configs/tools.policy.json",
			BrowserReadAllowHosts: []string{},
		},
		Sandbox: SandboxConfig{
			Enabled:         true,
			Backend:         "local-docker",
			RunnerURL:       "",
			Image:           "alpine:3.22",
			Network:         "none",
			WorkspaceAccess: "rw",
			HostAccess:      "forbidden",
		},
		Adapters: AdapterConfig{
			BrowserAutomation: BrowserAutomationAdapterConfig{
				TimeoutMS:            30000,
				StartupTimeoutMS:     10000,
				SettleTimeoutMS:      15000,
				SettleQuietPeriodMS:  500,
				SettlePollIntervalMS: 100,
				RouteRebindLimit:     2,
				PlaywrightExtension: PlaywrightExtensionConfig{
					ControllerSocket: "/run/sparkclaw/browser-controller/controller.sock",
					ProfileID:        "default",
					ConnectTimeoutMS: 20000,
				},
			},
			DocumentOCR: DocumentOCRAdapterConfig{
				Enabled:        false,
				Provider:       "openai-http",
				Model:          "sparkclaw-ocr",
				TimeoutSeconds: 120,
				MaxUploadBytes: 12 << 20,
				MaxOutputBytes: 1 << 20,
				MaxConcurrency: 2,
				MaxPending:     2,
			},
			PPTXVisualQA: PPTXVisualQAAdapterConfig{
				Phase:                     "disabled",
				RepairQualifiedClasses:    []string{},
				RepairQualifiedOperations: []string{},
				BlockingQualifiedClasses:  []string{},
				MaxRepairAttempts:         2,
				TimeoutSeconds:            120,
				MaxInputBytes:             64 << 20,
				MaxPDFBytes:               64 << 20,
				MaxPages:                  100,
				MaxChangedPages:           20,
				RasterScale:               1.5,
				MaxPagePixels:             20_000_000,
				MaxPNGBytes:               12 << 20,
				DiagnosticToleranceMilli:  2,
				ReadinessTTLSeconds:       300,
				GotenbergVersion:          "8.36.0",
				LibreOfficeVersion:        "26.2.5.2",
				PDFiumVersion:             "5.12.1",
			},
		},
		Memory: MemoryConfig{
			Enabled:              true,
			WritePolicy:          "candidate_then_confirm",
			AllowSensitiveMemory: false,
			RetentionDays:        180,
			RedactPatterns:       []string{"api_key", "password", "token", "ssh_key"},
		},
		PassiveNotifications: PassiveNotificationsConfig{
			MaxPerOwner:   500,
			RetentionDays: 90,
		},
		Workspaces: WorkspaceConfig{
			DefaultRoot: "./data/workspaces",
			Allowlist:   []string{"./data/workspaces"},
		},
		Storage: StorageConfig{
			TraceDir:        "./data/traces",
			LogDir:          "./data/logs",
			ArtifactBackend: "filesystem",
			ArtifactDir:     "./data/artifacts",
			ArtifactBucket:  "sparkclaw",
			S3Endpoint:      "",
			S3Region:        "us-east-1",
		},
		State: StateConfig{
			Backend:                   "file",
			Path:                      "./data/memory/gateway-state.json",
			DSN:                       "",
			StartupTimeoutSeconds:     180,
			ReadTimeoutSeconds:        10,
			WriteTimeoutSeconds:       30,
			TransactionTimeoutSeconds: 60,
			EncryptAtRest:             false,
			EncryptionKey:             "",
			EncryptionKeyFile:         "",
			CredentialKey:             "",
			CredentialKeyFile:         "./data/memory/gateway-credentials.key",
		},
		Runtime: RuntimeConfig{
			ObservationSummaryMaxBytes: 2400,
			// 8000 fits the local 32K-context profile. The hosted product
			// profile (docker/env/sparkclaw.product.env) raises it to the
			// extracted-document contract; defaults_contract_test.go pins
			// that override so the two cannot drift silently.
			StageEvidenceMaxBytes:         8000,
			StageMaxDurationSeconds:       180,
			StageMaxNoProgressActions:     3,
			StageMaxObservationReads:      2,
			RunMaxDurationSeconds:         1800,
			RunMaxToolCalls:               32,
			RunObservationCompactionBytes: 36000,
			RunMaxObservationBytes:        48000,
			RunMaxRepeatedToolCalls:       3,
		},
		Logging: LoggingConfig{
			Level:          "info",
			RedactPatterns: []string{"api_key", "password", "token", "ssh_key"},
		},
	}
	return cfg
}

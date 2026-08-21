package gateway

import "github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"

type runtimeTestRepository interface {
	Repository
	agent.Repository
}

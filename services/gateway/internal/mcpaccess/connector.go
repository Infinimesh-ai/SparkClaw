package mcpaccess

import (
	"context"
	"errors"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/binding"
)

// ConnectorAdapter participates only in the shared connector availability
// contract. MCP bindings are created by Access Ticket redemption over ISCP.
type ConnectorAdapter struct{}

func (ConnectorAdapter) Availability() error           { return nil }
func (ConnectorAdapter) Policy() binding.AdapterPolicy { return binding.AdapterPolicy{} }
func (ConnectorAdapter) Start(context.Context, app.NotificationBinding, binding.StartOptions) (app.NotificationBinding, error) {
	return app.NotificationBinding{}, errors.New("MCP binding is managed through ISCP and MCP Access Tickets")
}
func (ConnectorAdapter) Poll(context.Context, app.NotificationBinding) (binding.PollResult, error) {
	return binding.PollResult{}, errors.New("MCP binding is managed through ISCP")
}
func (ConnectorAdapter) Cancel(context.Context, app.NotificationBinding) error { return nil }

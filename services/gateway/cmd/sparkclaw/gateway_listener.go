package main

import (
	"net/http"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
)

func serveGatewayHTTP(server *http.Server, gateway config.GatewayConfig) error {
	tlsConfig, err := config.GatewayTLSConfig(gateway)
	if err != nil {
		return err
	}
	if tlsConfig != nil {
		server.TLSConfig = tlsConfig
		// Certificates were loaded and checked before opening the listener.
		return server.ListenAndServeTLS("", "")
	}
	return server.ListenAndServe()
}

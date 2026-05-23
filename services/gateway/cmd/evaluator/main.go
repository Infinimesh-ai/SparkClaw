package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/evaluator"
)

func main() {
	gatewayURL := flag.String("gateway-url", getenv("SPARKCLAW_EVALUATOR_GATEWAY_URL", "http://127.0.0.1:18789"), "Gateway base URL")
	profile := flag.String("profile", getenv("SPARKCLAW_EVALUATOR_PROFILE", "api-smoke"), "Evaluator profile")
	output := flag.String("output", getenv("SPARKCLAW_EVALUATOR_OUTPUT", ""), "Optional JSON report path")
	evalConfig := flag.String("eval-config", getenv("SPARKCLAW_EVALUATOR_CONFIG", ""), "Eval profiles JSON path")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	runner := evaluator.New(evaluator.Config{
		GatewayURL:     *gatewayURL,
		APIToken:       os.Getenv("SPARKCLAW_API_TOKEN"),
		Profile:        *profile,
		OutputPath:     *output,
		EvalConfigPath: *evalConfig,
	})
	report, err := runner.Run(ctx)
	if writeErr := runner.WriteReport(report); writeErr != nil {
		slog.Error("failed to write evaluator report", "error", writeErr)
		os.Exit(1)
	}
	if err != nil {
		slog.Error("evaluator failed", "error", err)
		os.Exit(1)
	}
}

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

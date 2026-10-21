// iscp-relay-origin-migrate moves one stopped node-managed service to a new
// Relay ingress port. It rewrites only the Relay URLs in the enrollment file
// and its sealed credential lease; identity, binding, credentials and CAS
// history are retained, so restoring the original files is a full rollback.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpbridge"
)

func main() {
	enrollment := flag.String("enrollment", "", "enrollment file on this host")
	runtimeEnrollment := flag.String("runtime-enrollment", "", "enrollment path as opened by the service (for example inside its container); defaults to -enrollment")
	identityFile := flag.String("identity", "", "service device identity file")
	keyFile := flag.String("key", "", "service device private key file")
	keyBackend := flag.String("key-backend", "file", "device key backend")
	from := flag.String("from-base", "", "current Relay HTTPS origin")
	to := flag.String("base", "", "new Relay HTTPS origin")
	dryRun := flag.Bool("dry-run", false, "open and verify every object without writing")
	flag.Parse()

	device, err := iscpbridge.LoadDeviceWithKeyBackend(*identityFile, *keyFile, *keyBackend, "")
	if err != nil {
		fail(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result, err := iscpbridge.MigrateRelayOrigin(ctx, iscpbridge.RelayOriginMigration{
		EnrollmentFile: *enrollment, RuntimeEnrollmentFile: *runtimeEnrollment, Device: device,
		FromBaseURL: *from, BaseURL: *to, DryRun: *dryRun,
	})
	if err != nil {
		fail(err)
	}
	_ = json.NewEncoder(os.Stdout).Encode(struct {
		iscpbridge.RelayOriginMigrationResult
		DeviceID string `json:"device_id"`
		DryRun   bool   `json:"dry_run"`
	}{result, device.Identity.DeviceID, *dryRun})
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "relay origin migration failed:", err)
	os.Exit(1)
}

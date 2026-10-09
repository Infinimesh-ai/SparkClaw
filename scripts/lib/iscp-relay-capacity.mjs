import fs from "node:fs/promises";
import path from "node:path";
import crypto from "node:crypto";

// This optional local build changes scheduling only. The reference protocol,
// signed descriptors, PoP, envelopes and drained/message frames are unchanged.
// Keep the checksum-verified module cache untouched and record the exact patch.
export async function prepareCapacityRelay(moduleDirectory, destination) {
  await fs.cp(moduleDirectory, destination, { recursive: true });
  const filename = path.join(destination, "services/relay-reference/internal/relay/server.go");
  let source = await fs.readFile(filename, "utf8");
  const before = crypto.createHash("sha256").update(source).digest("hex");
  const replace = (old, next) => {
    if (!source.includes(old) || source.indexOf(old) !== source.lastIndexOf(old)) throw new Error("Pinned Relay capacity patch no longer matches upstream source");
    source = source.replace(old, next);
  };
  replace('\t"net/url"', '\t"net/url"\n\t"os"');
  replace("\ts := &Server{", "\trequestLimit := 120\n\tif cfg.ProfileGate.Profile == config.ProfileLocalLab && os.Getenv(\"SPARKCLAW_ISCP_CAPACITY\") == \"1\" { requestLimit = 12000 }\n\ts := &Server{");
  replace("ratelimit.New(120, time.Minute)", "ratelimit.New(requestLimit, time.Minute)");
  const start = "\tmessages, err := s.dequeueMessages(r.Context(), id.DomainID, id.DeviceID, time.Now().UTC(), 100)";
  const end = '\t_ = c.WriteJSON(map[string]any{"state": "drained", "delivered": delivered})';
  const begin = source.indexOf(start), finish = source.indexOf(end, begin);
  if (begin < 0 || finish < 0 || source.indexOf(start, begin + 1) >= 0) throw new Error("Pinned Relay drain implementation changed");
  const old = source.slice(begin, finish + end.length);
  let loop = old.replace("\tdelivered := 0\n", "").replace(end, "");
  // A bounded stream sends existing message frames continuously. Old drain
  // clients already read frames until drained; a reference Relay still works.
  const replacement = `\tdelivered := 0
\tpeerClosed := make(chan struct{})
\tgo func() { defer close(peerClosed); for { if _, _, err := c.ReadMessage(); err != nil { return } } }()
\tduration := time.Duration(0)
\tif s.cfg.ProfileGate.Profile == config.ProfileLocalLab && os.Getenv("SPARKCLAW_ISCP_CAPACITY") == "1" { duration = 10 * time.Minute }
\tdeadline := time.Now().Add(duration)
\tticker := time.NewTicker(10 * time.Millisecond)
\tdefer ticker.Stop()
\tfor {
\t\tselect { case <-peerClosed: return; default: }
\t\ts.mu.RLock()
\t\t_, revokedNow := s.revoked[id.DeviceID]
\t\ts.mu.RUnlock()
\t\tif revokedNow { return }
\t\tif err := c.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil { return }
${loop}
\t\tif duration == 0 || time.Now().After(deadline) { break }
\t\tselect { case <-peerClosed: return; case <-r.Context().Done(): return; case <-ticker.C: }
\t}
${end}`;
  replace(old, replacement);
  await fs.chmod(filename, 0o600);
  await fs.writeFile(filename, source, { mode: 0o600 });
  return { mode: "compatible-capacity-v1", reference_server_sha256: before, patched_server_sha256: crypto.createHash("sha256").update(source).digest("hex"), local_requests_per_minute: 12000, stream_lifetime_seconds: 600, drain_tick_ms: 10 };
}

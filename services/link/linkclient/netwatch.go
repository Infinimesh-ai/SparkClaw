package linkclient

import (
	"context"
	"net"
	"sort"
	"strings"
	"time"
)

// networkPoll is how often the device's addresses are compared.
const networkPoll = 3 * time.Second

// watchNetwork reports network changes the platform did not: a different
// set of addresses (another Wi-Fi, cellular, a cable), or a gap in time
// that means the device slept. Where the addresses cannot be read (recent
// Android versions) only the platform's own callback reports changes.
func (c *Client) watchNetwork(ctx context.Context) {
	last, known := addressSnapshot()
	seen := time.Now().Round(0) // wall clock: it keeps running through sleep
	tick := time.NewTicker(networkPoll)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		now := time.Now().Round(0)
		slept := now.Sub(seen) > 4*networkPoll
		seen = now
		cur, ok := addressSnapshot()
		changed := ok && known && cur != last
		if ok {
			last, known = cur, true
		}
		if slept || changed {
			c.NetworkChanged()
		}
	}
}

// addressSnapshot is the device's routable addresses in a stable order.
func addressSnapshot() (string, bool) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "", false
	}
	var out []string
	for _, a := range addrs {
		n, ok := a.(*net.IPNet)
		if !ok || n.IP.IsLoopback() || n.IP.IsLinkLocalUnicast() || n.IP.IsLinkLocalMulticast() || n.IP.IsUnspecified() {
			continue
		}
		out = append(out, n.String())
	}
	sort.Strings(out)
	return strings.Join(out, ","), true
}

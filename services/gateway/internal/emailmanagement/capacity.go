package emailmanagement

// Workspace capacity thresholds. Crossing one only warns the user: originals are
// retained permanently and are never deleted automatically, so the only remedy
// the system offers is to surface the pressure and let the user clean up.
const (
	capacityWarnPercent     = 85
	capacityCriticalPercent = 95
	capacityWarnFree        = 2 << 30
	capacityCriticalFree    = 512 << 20
)

type CapacityView struct {
	State       string `json:"state"` // ok | warning | critical | unknown
	TotalBytes  int64  `json:"total_bytes"`
	FreeBytes   int64  `json:"free_bytes"`
	UsedPercent int    `json:"used_percent"`
}

func capacityState(total, free int64) CapacityView {
	out := CapacityView{State: "unknown", TotalBytes: total, FreeBytes: free}
	if total <= 0 || free < 0 || free > total {
		return out
	}
	out.UsedPercent = int((total - free) * 100 / total)
	switch {
	case out.UsedPercent >= capacityCriticalPercent || free < capacityCriticalFree:
		out.State = "critical"
	case out.UsedPercent >= capacityWarnPercent || free < capacityWarnFree:
		out.State = "warning"
	default:
		out.State = "ok"
	}
	return out
}

// capacity reports workspace pressure. A probe failure is non-fatal: the field
// is simply reported as unknown rather than failing the status projection.
func (s *Service) capacity() CapacityView {
	total, free, err := statfs(s.opts.WorkspaceRoot)
	if err != nil {
		return CapacityView{State: "unknown"}
	}
	return capacityState(total, free)
}

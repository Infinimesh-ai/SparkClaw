package execution

import "errors"

const maxExecutionApprovals = 32

func approvalReceipts(f Fence) []ApprovalRecord {
	rows := []ApprovalRecord(nil)
	for _, row := range f.Approvals {
		if row.State != ApprovalDecided && row.State != ApprovalDecisionUnknown {
			continue
		}
		// Do not expose the durable timestamp pointer to mutable callers.
		if row.DecidedAt != nil {
			at := *row.DecidedAt
			row.DecidedAt = &at
		}
		rows = append(rows, row)
	}
	return rows
}

func invalidateApprovals(f *Fence) {
	f.Approvals = append([]ApprovalRecord(nil), f.Approvals...)
	for index := range f.Approvals {
		if f.Approvals[index].State == ApprovalPending {
			f.Approvals[index].State = ApprovalInvalidated
			f.Approvals[index].Revision = f.Revision
		}
	}
}

// Interruption is terminal for the execution, regardless of whether previous
// external effects can be determined. A committed approval must never become
// evidence that its operation did not happen or permission to replay it.
func interruptApprovalExecution(f *Fence) {
	pending, approved := false, false
	for _, row := range f.Approvals {
		pending = pending || row.State == ApprovalPending
		approved = approved || row.Decision == "approve"
	}
	f.State = "unknown"
	f.Revision++
	if pending {
		f.TerminationReason = TerminationGatewayRestartedAwaitingApproval
		if !approved {
			f.State = "failed"
		}
	} else if len(f.Approvals) > 0 {
		f.TerminationReason = TerminationGatewayRestartedAfterApproval
	}
	invalidateApprovals(f)
}

func validateApprovalControl(f Fence) error {
	if f.Revision == 0 || len(f.Approvals) > maxExecutionApprovals {
		return errors.New("invalid approval control revision or capacity")
	}
	switch f.TerminationReason {
	case "":
	case TerminationGatewayRestartedAwaitingApproval, TerminationGatewayRestartedAfterApproval:
		if f.State != "failed" && f.State != "unknown" {
			return errors.New("invalid approval termination state")
		}
	default:
		return errors.New("invalid approval termination reason")
	}
	seen := map[string]bool{}
	for _, row := range f.Approvals {
		if !identityPattern.MatchString(row.ApprovalID) || !digestPattern.MatchString(row.Digest) || row.Revision == 0 || row.Revision > f.Revision || seen[row.ApprovalID] {
			return errors.New("invalid approval control binding")
		}
		seen[row.ApprovalID] = true
		switch row.State {
		case ApprovalPending:
			if f.State != "running" {
				return errors.New("pending approval on terminal execution")
			}
		case ApprovalInvalidated:
		case ApprovalDecided, ApprovalDecisionUnknown:
			if (row.Decision != "approve" && row.Decision != "reject") || row.DecidedAt == nil || row.DecidedAt.IsZero() {
				return errors.New("invalid approval decision receipt")
			}
		default:
			return errors.New("invalid approval control state")
		}
		if row.State != ApprovalDecided && row.State != ApprovalDecisionUnknown && (row.Decision != "" || row.DecidedAt != nil) {
			return errors.New("uncommitted approval decision")
		}
	}
	return nil
}

package medicine

import (
	"fmt"
	"regexp"
)

var digestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

// ValidateState refuses a malformed persisted inventory instead of treating it
// as an empty box. Only the complete file/memory partition uses this function;
// PostgreSQL loads bounded receipt subsets inside its transaction.
func ValidateState(owner string, state State) error {
	if ValidateOwner(owner) != nil || state.Epoch != EmptyState(owner).Epoch || state.Sequence < 0 || state.Floor < 0 || state.Floor > state.Sequence || len(state.Items) > MaxItems || len(state.Events) > MaxEvents || int64(len(state.Events)) != state.Sequence-state.Floor {
		return ErrInvalid
	}
	ids := map[string]bool{}
	validateItem := func(item Item) error {
		fields, err := ValidateFields(item.Fields, true)
		if err != nil || fields != item.Fields || !itemPattern.MatchString(item.ID) || item.Version < 1 || item.CreatedAt.IsZero() || item.UpdatedAt.IsZero() {
			return ErrInvalid
		}
		return nil
	}
	for _, item := range state.Items {
		if ids[item.ID] || validateItem(item) != nil {
			return ErrInvalid
		}
		ids[item.ID] = true
	}
	for i, event := range state.Events {
		if event.Sequence != state.Floor+int64(i)+1 || event.ID != event.Item.ID || validateItem(event.Item) != nil {
			return ErrInvalid
		}
	}
	if state.ReceiptCount != len(state.Receipts)+len(state.Imports) || state.ReceiptCount > MaxReceipts {
		return ErrInvalid
	}
	for id, receipt := range state.Receipts {
		if ValidateOwner(id) != nil || receipt.OperationID != id || !digestPattern.MatchString(receipt.Digest) || receipt.Sequence < 1 || receipt.Sequence > state.Sequence || len(receipt.Items) < 1 || len(receipt.Items) > MaxBatch {
			return ErrInvalid
		}
		for _, change := range receipt.Items {
			if change.Before == nil && change.After == nil {
				return ErrInvalid
			}
			if change.Before != nil && validateItem(*change.Before) != nil {
				return ErrInvalid
			}
			if change.After != nil && validateItem(*change.After) != nil {
				return ErrInvalid
			}
		}
	}
	for id, receipt := range state.Imports {
		if _, exists := state.Receipts[id]; exists {
			return ErrInvalid
		}
		if ValidateOwner(id) != nil || receipt.Result.OperationID != id || !digestPattern.MatchString(receipt.Digest) || receipt.Result.Sequence < 0 || receipt.Result.Sequence > state.Sequence || len(receipt.Result.Items) < 1 || len(receipt.Result.Items) > MaxImport || receipt.Result.Imported+receipt.Result.Skipped != len(receipt.Result.Items) {
			return ErrInvalid
		}
		for _, item := range receipt.Result.Items {
			if validateItem(item) != nil {
				return ErrInvalid
			}
		}
	}
	return nil
}
func ValidateStates(states map[string]State) error {
	for owner, state := range states {
		if err := ValidateState(owner, state); err != nil {
			return fmt.Errorf("medicine state: %w", err)
		}
	}
	return nil
}

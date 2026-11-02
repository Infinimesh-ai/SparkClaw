package medicine

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"golang.org/x/text/unicode/norm"
	"math"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

var identityPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:@-]{0,254}$`)
var itemPattern = regexp.MustCompile(`^med_[a-f0-9]{32}$`)

func ValidateOwner(owner string) error {
	if !identityPattern.MatchString(owner) {
		return ErrInvalid
	}
	return nil
}
func NormalizeName(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return unicode.ToLower(r)
	}, norm.NFKC.String(value))
}
func cleanText(value string, max int, required, multiline bool) (string, error) {
	if !utf8.ValidString(value) {
		return "", ErrInvalid
	}
	if multiline {
		value = strings.ReplaceAll(value, "\r\n", "\n")
	}
	value = strings.TrimSpace(value)
	if (required && value == "") || len(utf16.Encode([]rune(value))) > max {
		return "", ErrInvalid
	}
	for _, r := range value {
		if (r < 32 || r == 127) && !(multiline && r == '\n') {
			return "", ErrInvalid
		}
	}
	return value, nil
}
func ValidateFields(value Fields, allowLegacyExpiry bool) (Fields, error) {
	var err error
	if value.Name, err = cleanText(value.Name, 80, true, false); err != nil {
		return Fields{}, fmt.Errorf("%w: name", err)
	}
	if !slices.Contains(Categories, value.Category) || !slices.Contains(Units, value.Unit) {
		return Fields{}, fmt.Errorf("%w: category/unit", ErrInvalid)
	}
	if math.IsNaN(value.Quantity) || math.IsInf(value.Quantity, 0) || value.Quantity < 0 || value.Quantity > 100000 || math.Abs(value.Quantity*100-math.Round(value.Quantity*100)) > 0.000001 {
		return Fields{}, fmt.Errorf("%w: quantity", ErrInvalid)
	}
	if value.NoExpiry {
		if value.ExpiresOn != "" {
			return Fields{}, fmt.Errorf("%w: expiry", ErrInvalid)
		}
	} else if value.ExpiresOn == "" {
		if !allowLegacyExpiry {
			return Fields{}, fmt.Errorf("%w: expiry", ErrInvalid)
		}
	} else {
		date, e := time.Parse("2006-01-02", value.ExpiresOn)
		if e != nil || date.Year() < 1 || date.Format("2006-01-02") != value.ExpiresOn {
			return Fields{}, fmt.Errorf("%w: expiry", ErrInvalid)
		}
	}
	if value.Location, err = cleanText(value.Location, 80, false, false); err != nil {
		return Fields{}, fmt.Errorf("%w: location", err)
	}
	if value.Usage, err = cleanText(value.Usage, 80, false, false); err != nil {
		return Fields{}, fmt.Errorf("%w: usage", err)
	}
	if value.Notes, err = cleanText(value.Notes, 500, false, true); err != nil {
		return Fields{}, fmt.Errorf("%w: notes", err)
	}
	return value, nil
}
func Digest(value any) string {
	raw, _ := json.Marshal(value)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func EmptyState(owner string) State {
	return State{Box: Box{Epoch: "medicine_" + Digest(owner)[:32], Items: []Item{}, Events: []Event{}}, Receipts: map[string]Receipt{}, Imports: map[string]ImportReceipt{}}
}
func CloneState(in State) State {
	raw, _ := json.Marshal(in)
	var out State
	_ = json.Unmarshal(raw, &out)
	if out.Items == nil {
		out.Items = []Item{}
	}
	if out.Events == nil {
		out.Events = []Event{}
	}
	if out.Receipts == nil {
		out.Receipts = map[string]Receipt{}
	}
	if out.Imports == nil {
		out.Imports = map[string]ImportReceipt{}
	}
	return out
}
func newID() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return "med_" + hex.EncodeToString(bytes[:]), nil
}
func sortedItems(items []Item) {
	slices.SortFunc(items, func(a, b Item) int { return strings.Compare(a.ID, b.ID) })
}
func appendEvent(state *State, item Item, deleted bool) {
	state.Sequence++
	state.Events = append(state.Events, Event{Sequence: state.Sequence, ID: item.ID, Deleted: deleted, Item: item})
	if len(state.Events) > MaxEvents {
		drop := len(state.Events) - MaxEvents
		state.Floor = state.Events[drop-1].Sequence
		state.Events = append([]Event{}, state.Events[drop:]...)
	}
}

// ApplyBatch is pure with respect to current: a failure never exposes partial
// records or consumes an idempotency key. Backends commit next plus its receipt.
func ApplyBatch(owner string, current State, batch Batch, now time.Time) (State, Receipt, error) {
	if ValidateOwner(owner) != nil || !identityPattern.MatchString(batch.OperationID) || len(batch.Items) < 1 || len(batch.Items) > MaxBatch {
		return State{}, Receipt{}, ErrInvalid
	}
	if batch.Operation != OperationCreate && batch.Operation != OperationUpdate && batch.Operation != OperationDelete {
		return State{}, Receipt{}, ErrInvalid
	}
	if batch.Operation == OperationDelete && len(batch.Items) != 1 {
		return State{}, Receipt{}, ErrInvalid
	}
	digest := Digest(struct {
		Owner string
		Batch Batch
	}{owner, batch})
	if receipt, ok := current.Receipts[batch.OperationID]; ok {
		if receipt.Digest != digest {
			return State{}, Receipt{}, ErrConflict
		}
		return CloneState(current), receipt, nil
	}
	if _, ok := current.Imports[batch.OperationID]; ok {
		return State{}, Receipt{}, ErrConflict
	}
	if current.ReceiptCount >= MaxReceipts || len(current.Receipts)+len(current.Imports) >= MaxReceipts {
		return State{}, Receipt{}, ErrCapacity
	}
	if batch.Operation == OperationCreate && len(current.Items)+len(batch.Items) > MaxItems {
		return State{}, Receipt{}, ErrCapacity
	}
	next := CloneState(current)
	if next.Epoch == "" {
		next = EmptyState(owner)
	}
	receipt := Receipt{OperationID: batch.OperationID, Digest: digest, Items: []Change{}}
	seenEntries, seenIDs := map[string]bool{}, map[string]bool{}
	for _, entry := range batch.Items {
		if !identityPattern.MatchString(entry.EntryID) || seenEntries[entry.EntryID] {
			return State{}, Receipt{}, ErrInvalid
		}
		seenEntries[entry.EntryID] = true
		change := Change{EntryID: entry.EntryID}
		if batch.Operation == OperationCreate {
			if entry.ID != "" || entry.BaseVersion != 0 {
				return State{}, Receipt{}, ErrInvalid
			}
			fields, err := ValidateFields(entry.Fields, false)
			if err != nil {
				return State{}, Receipt{}, err
			}
			id, err := newID()
			if err != nil {
				return State{}, Receipt{}, err
			}
			item := Item{Fields: fields, ID: id, Version: 1, CreatedAt: now.UTC(), UpdatedAt: now.UTC()}
			next.Items = append(next.Items, item)
			change.After = &item
			appendEvent(&next, item, false)
		} else {
			if !itemPattern.MatchString(entry.ID) || entry.BaseVersion < 1 || seenIDs[entry.ID] {
				return State{}, Receipt{}, ErrInvalid
			}
			seenIDs[entry.ID] = true
			index := slices.IndexFunc(next.Items, func(item Item) bool { return item.ID == entry.ID })
			if index < 0 {
				return State{}, Receipt{}, ErrNotFound
			}
			before := next.Items[index]
			if before.Version != entry.BaseVersion {
				return State{}, Receipt{}, fmt.Errorf("%w: %s", ErrConflict, before.Name)
			}
			change.Before = &before
			if batch.Operation == OperationDelete {
				if entry.Fields != (Fields{}) {
					return State{}, Receipt{}, ErrInvalid
				}
				next.Items = append(next.Items[:index:index], next.Items[index+1:]...)
				change.Deleted = true
				appendEvent(&next, before, true)
			} else {
				fields, err := ValidateFields(entry.Fields, before.ExpiresOn == "" && !before.NoExpiry && entry.Fields.ExpiresOn == "" && !entry.Fields.NoExpiry)
				if err != nil {
					return State{}, Receipt{}, err
				}
				item := before
				item.Fields = fields
				item.Version++
				item.UpdatedAt = now.UTC()
				if !item.UpdatedAt.After(before.UpdatedAt) {
					item.UpdatedAt = before.UpdatedAt.Add(time.Microsecond)
				}
				next.Items[index] = item
				change.After = &item
				appendEvent(&next, item, false)
			}
		}
		receipt.Items = append(receipt.Items, change)
	}
	sortedItems(next.Items)
	receipt.Sequence = next.Sequence
	next.Receipts[batch.OperationID] = receipt
	next.ReceiptCount++
	return next, receipt, nil
}
func ApplyImport(owner string, current State, request ImportRequest, now time.Time) (State, ImportResult, error) {
	if ValidateOwner(owner) != nil || !identityPattern.MatchString(request.OperationID) || !identityPattern.MatchString(request.InstallationID) || len(request.Items) < 1 || len(request.Items) > MaxImport {
		return State{}, ImportResult{}, ErrInvalid
	}
	digest := Digest(struct {
		Owner   string
		Request ImportRequest
	}{owner, request})
	if receipt, ok := current.Imports[request.OperationID]; ok {
		if receipt.Digest != digest {
			return State{}, ImportResult{}, ErrConflict
		}
		return CloneState(current), receipt.Result, nil
	}
	if _, ok := current.Receipts[request.OperationID]; ok {
		return State{}, ImportResult{}, ErrConflict
	}
	if current.ReceiptCount >= MaxReceipts || len(current.Imports)+len(current.Receipts) >= MaxReceipts {
		return State{}, ImportResult{}, ErrCapacity
	}
	next := CloneState(current)
	if next.Epoch == "" {
		next = EmptyState(owner)
	}
	result := ImportResult{OperationID: request.OperationID, Items: []Item{}}
	seen := map[string]bool{}
	for _, item := range request.Items {
		if !itemPattern.MatchString(item.ID) || seen[item.ID] || item.CreatedAt.IsZero() || item.CreatedAt.Year() < 1 || item.CreatedAt.Year() > 9999 {
			return State{}, ImportResult{}, ErrInvalid
		}
		seen[item.ID] = true
		fields, err := ValidateFields(item.Fields, true)
		if err != nil {
			return State{}, ImportResult{}, err
		}
		index := slices.IndexFunc(next.Items, func(row Item) bool { return row.ID == item.ID })
		if index >= 0 {
			result.Skipped++
			result.Items = append(result.Items, next.Items[index])
			continue
		}
		if len(next.Items) >= MaxItems {
			return State{}, ImportResult{}, ErrCapacity
		}
		item.Fields = fields
		item.Version = 1
		item.CreatedAt = item.CreatedAt.UTC()
		item.UpdatedAt = now.UTC()
		next.Items = append(next.Items, item)
		appendEvent(&next, item, false)
		result.Imported++
		result.Items = append(result.Items, item)
	}
	sortedItems(next.Items)
	result.Sequence = next.Sequence
	next.Imports[request.OperationID] = ImportReceipt{Digest: digest, Result: result}
	next.ReceiptCount++
	return next, result, nil
}

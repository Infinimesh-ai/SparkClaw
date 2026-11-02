// Package medicine owns the authoritative inventory and its atomic change contract.
// It has no transport, model, or storage implementation dependencies.
package medicine

import (
	"context"
	"errors"
	"time"
)

const (
	MaxItems        = 2000
	MaxBatch        = 20
	MaxImport       = 100
	MaxEvents       = 4096
	MaxPage         = 100
	MaxReceipts     = 65536
	OperationCreate = "create"
	OperationUpdate = "update"
	OperationDelete = "delete"
)

var Categories = []string{"cold", "fever", "pain", "cough", "digestive", "allergy", "wound", "other"}
var Units = []string{"tablet", "capsule", "sachet", "bottle", "tube", "patch", "box", "ml", "g"}
var (
	ErrInvalid  = errors.New("invalid medicine input")
	ErrConflict = errors.New("medicine record or operation changed")
	ErrNotFound = errors.New("medicine record not found")
	ErrCapacity = errors.New("medicine capacity exceeded")
	ErrReset    = errors.New("medicine sync snapshot required")
)

type Fields struct {
	Name      string  `json:"name"`
	Category  string  `json:"category"`
	Quantity  float64 `json:"quantity"`
	Unit      string  `json:"unit"`
	ExpiresOn string  `json:"expires_on"`
	NoExpiry  bool    `json:"no_expiry"`
	Location  string  `json:"location"`
	Usage     string  `json:"usage"`
	Notes     string  `json:"notes"`
}
type Item struct {
	Fields
	ID        string    `json:"id"`
	Version   int64     `json:"version"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
type Entry struct {
	EntryID     string `json:"entry_id"`
	ID          string `json:"id,omitempty"`
	BaseVersion int64  `json:"base_version,omitempty"`
	Fields      Fields `json:"fields"`
}
type Batch struct {
	OperationID string  `json:"operation_id"`
	Operation   string  `json:"operation"`
	Items       []Entry `json:"items"`
}
type Change struct {
	EntryID string `json:"entry_id"`
	Before  *Item  `json:"before,omitempty"`
	After   *Item  `json:"after,omitempty"`
	Deleted bool   `json:"deleted"`
}
type Receipt struct {
	OperationID string   `json:"operation_id"`
	Digest      string   `json:"digest"`
	Items       []Change `json:"items"`
	Sequence    int64    `json:"sequence"`
}
type Event struct {
	Sequence int64  `json:"sequence"`
	ID       string `json:"id"`
	Deleted  bool   `json:"deleted"`
	Item     Item   `json:"item"`
}
type Box struct {
	Epoch    string  `json:"epoch"`
	Sequence int64   `json:"sequence"`
	Floor    int64   `json:"floor"`
	Items    []Item  `json:"items"`
	Events   []Event `json:"events"`
}
type ImportRequest struct {
	OperationID    string `json:"operation_id"`
	InstallationID string `json:"installation_id"`
	Items          []Item `json:"items"`
}
type ImportResult struct {
	OperationID string `json:"operation_id"`
	Items       []Item `json:"items"`
	Imported    int    `json:"imported"`
	Skipped     int    `json:"skipped"`
	Sequence    int64  `json:"sequence"`
}
type OperationResult struct {
	OperationID string        `json:"operation_id"`
	Batch       *Receipt      `json:"batch,omitempty"`
	Import      *ImportResult `json:"import,omitempty"`
}

type Repository interface {
	GetMedicineOperation(context.Context, string, string) (OperationResult, bool, error)
	ReadMedicineBox(context.Context, string) (Box, error)
	ApplyMedicineBatch(context.Context, string, Batch) (Receipt, error)
	ImportMedicines(context.Context, string, ImportRequest) (ImportResult, error)
}

// State is the single persistable owner partition, including receipts. A mutation
// replaces it only after every entry has been checked. It is never sent to a client.
type State struct {
	Box
	ReceiptCount int                      `json:"receipt_count"`
	Receipts     map[string]Receipt       `json:"receipts"`
	Imports      map[string]ImportReceipt `json:"imports"`
}
type ImportReceipt struct {
	Digest string       `json:"digest"`
	Result ImportResult `json:"result"`
}

type Service struct{ repo Repository }

func New(repo Repository) *Service { return &Service{repo: repo} }
func (s *Service) Read(ctx context.Context, owner string) (Box, error) {
	return s.repo.ReadMedicineBox(ctx, owner)
}
func (s *Service) Apply(ctx context.Context, owner string, batch Batch) (Receipt, error) {
	return s.repo.ApplyMedicineBatch(ctx, owner, batch)
}
func (s *Service) Import(ctx context.Context, owner string, req ImportRequest) (ImportResult, error) {
	return s.repo.ImportMedicines(ctx, owner, req)
}

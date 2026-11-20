package gateway

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/link"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/medicine"
	"github.com/Chiiz0/SparkClaw/services/link/contract"
)

// The medicine domain over the link layer (link contract section 4,
// Business domains): the medicine box's inventory, its operation receipts,
// photo recognition and reading a typed sentence. Each method calls the
// same business function as the host WebChat's HTTP handler.

type linkMedicineItemArgs struct {
	ItemID      string          `json:"item_id,omitempty"`
	OperationID string          `json:"operation_id"`
	BaseVersion *int64          `json:"base_version,omitempty"`
	Item        json.RawMessage `json:"item,omitempty"`
}

type linkMedicineOperationArgs struct {
	OperationID string `json:"operation_id"`
}

func init() {
	item := func(operation string) func(context.Context, *Server, *link.Request, json.RawMessage) (any, error) {
		return func(ctx context.Context, s *Server, req *link.Request, raw json.RawMessage) (any, error) {
			args, err := linkArgs[linkMedicineItemArgs](raw)
			if err != nil {
				return nil, err
			}
			out, err := s.medicineWriteItem(ctx, req.Principal.OwnerID, operation, medicineItemInput{OperationID: args.OperationID, BaseVersion: args.BaseVersion, Item: args.Item, ItemID: args.ItemID})
			return out, linkMedicineError(err)
		}
	}
	registerLinkDomain("medicine", map[string]linkDomainMethod{
		"medicine.sync": {Call: func(ctx context.Context, s *Server, req *link.Request, raw json.RawMessage) (any, error) {
			args, err := linkArgs[medicineSyncInput](raw)
			if err != nil {
				return nil, err
			}
			out, err := s.medicineSync(ctx, req.Principal.OwnerID, args)
			return out, linkMedicineError(err)
		}},
		"medicine.operations.get": {Call: func(ctx context.Context, s *Server, req *link.Request, raw json.RawMessage) (any, error) {
			args, err := linkArgs[linkMedicineOperationArgs](raw)
			if err != nil {
				return nil, err
			}
			out, err := s.medicineOperation(ctx, req.Principal.OwnerID, args.OperationID)
			return out, linkMedicineError(err)
		}},
		"medicine.items.create": {Mutation: true, Call: item(medicine.OperationCreate)},
		"medicine.items.update": {Mutation: true, Call: item(medicine.OperationUpdate)},
		"medicine.items.delete": {Mutation: true, Call: item(medicine.OperationDelete)},
		"medicine.items.batch": {Mutation: true, Call: func(ctx context.Context, s *Server, req *link.Request, raw json.RawMessage) (any, error) {
			args, err := linkArgs[medicineBatchInput](raw)
			if err != nil {
				return nil, err
			}
			out, err := s.medicineBatch(ctx, req.Principal.OwnerID, args)
			return out, linkMedicineError(err)
		}},
		"medicine.import": {Mutation: true, Call: func(ctx context.Context, s *Server, req *link.Request, raw json.RawMessage) (any, error) {
			args, err := linkArgs[medicineImportInput](raw)
			if err != nil {
				return nil, err
			}
			out, err := s.medicineImport(ctx, req.Principal.OwnerID, args)
			return out, linkMedicineError(err)
		}},
		"medicine.recognize": {Call: func(ctx context.Context, s *Server, _ *link.Request, raw json.RawMessage) (any, error) {
			args, err := linkArgs[medicineRecognizeInput](raw)
			if err != nil {
				return nil, err
			}
			out, err := s.medicineRecognize(ctx, args)
			return out, linkMedicineModelError(err, "medicine_recognition_unavailable", "medicine_photo_invalid")
		}},
		"medicine.parse": {Call: func(ctx context.Context, s *Server, _ *link.Request, raw json.RawMessage) (any, error) {
			args, err := linkArgs[medicineParseInput](raw)
			if err != nil {
				return nil, err
			}
			out, err := s.medicineParse(ctx, args)
			return out, linkMedicineModelError(err, "medicine_parse_unavailable", "medicine_text_invalid")
		}},
	})
}

func linkMedicineError(err error) error {
	if err == nil {
		return nil
	}
	var cerr *contract.Error
	if errors.As(err, &cerr) {
		return cerr
	}
	_, reason, code := medicineInventoryFailure(err)
	return linkDomainError(code, reason, reason, code == contract.CodeBackendUnavailable)
}

// A model outage is the service being unavailable; anything else is the
// input the owner gave.
func linkMedicineModelError(err error, unavailable, invalid string) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, errMedicineModelUnavailable):
		return linkDomainError(contract.CodeBackendUnavailable, unavailable, "模型暂时不可用", true)
	default:
		return linkDomainError(contract.CodeInvalidRequest, invalid, err.Error(), false)
	}
}

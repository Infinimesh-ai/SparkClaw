package gateway

import (
	"context"
	"net/http"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
)

func (a *iscpDomainAdapter) medicineInventory(ctx context.Context, request iscpworkbench.Request) iscpDomainResult {
	if _, _, err := a.executionIdentity(ctx, request); err != nil {
		return domainError(403, "installation_required")
	}
	switch request.Operation {
	case iscpworkbench.OperationMedicineSync:
		return domainHTTP(ctx, request, http.MethodPost, a.server.syncMedicine, nil)
	case iscpworkbench.OperationMedicineItemsCreate:
		return domainHTTP(ctx, request, http.MethodPost, a.server.createMedicine, nil)
	case iscpworkbench.OperationMedicineItemsUpdate:
		return domainHTTP(ctx, request, http.MethodPut, a.server.updateMedicine, map[string]string{"item": request.Params["item"]})
	case iscpworkbench.OperationMedicineItemsDelete:
		return domainHTTP(ctx, request, http.MethodDelete, a.server.deleteMedicine, map[string]string{"item": request.Params["item"]})
	case iscpworkbench.OperationMedicineItemsBatch:
		return domainHTTP(ctx, request, http.MethodPost, a.server.batchMedicine, nil)
	case iscpworkbench.OperationMedicineImport:
		return domainHTTP(ctx, request, http.MethodPost, a.server.importMedicine, nil)
	case iscpworkbench.OperationMedicineOperationsGet:
		if !domainEmpty(request.Body) {
			return domainError(400, "invalid_input")
		}
		return domainHTTP(ctx, request, http.MethodGet, a.server.getMedicineOperation, map[string]string{"operation": request.Params["operation"]})
	}
	return domainError(501, "capability_unavailable")
}

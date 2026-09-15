package request

import "agent-desk/internal/pms/sandbox"

type PMSSandboxStoreRequest struct {
	StoreID int64 `json:"storeId" form:"storeId"`
}

type PMSSandboxResetRequest struct {
	StoreID   int64 `json:"storeId"`
	DatasetID int64 `json:"datasetId"`
	Version   int64 `json:"version"`
}

type PMSSandboxSaveRequest struct {
	StoreID int64 `json:"storeId"`
	sandbox.SaveRequest
}

type PMSSandboxBindingRequest struct {
	StoreID int64 `json:"storeId"`
	sandbox.BindingRequest
}

type PMSSandboxOperationRequest struct {
	StoreID     int64 `json:"storeId"`
	OperationID int64 `json:"operationId"`
}

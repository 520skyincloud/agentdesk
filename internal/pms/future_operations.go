package pms

import "context"

// DeferredOperations is a compile-time boundary for HPMS capabilities whose
// formal endpoint and request payload have not been supplied. It is not wired
// to the customer tool or any HTTP client action.
type DeferredOperations interface {
	QueryOrderByChannelNumber(context.Context, ChannelOrderLookupRequest) (QueryResult, error)
	ChangeRoomType(context.Context, ChangeRoomTypeRequest) (DeferredOperationResult, error)
	AssignRoom(context.Context, AssignRoomRequest) (DeferredOperationResult, error)
	ChangeOccupiedRoom(context.Context, ChangeOccupiedRoomRequest) (DeferredOperationResult, error)
	ExtendCheckout(context.Context, ExtendCheckoutRequest) (DeferredOperationResult, error)
}

// The fields below are limited to identifiers and facts already present in the
// three supplied HPMS documents. They are operation context, not guessed HPMS
// request payloads. Endpoint-specific fields must be added only after HPMS
// supplies the formal contract.
type ChannelOrderLookupRequest struct {
	HotelID            string
	ChannelOrderNumber string
}

type PMSOrderOperationContext struct {
	HotelID           string
	ReserveOrderID    string
	ReceptOrderID     string
	CurrentRoomTypeID string
	CurrentHomeID     string
	CheckInTime       string
	CheckOutTime      string
	OrderStatus       string
}

type ChangeRoomTypeRequest struct {
	Order            PMSOrderOperationContext
	TargetRoomTypeID string
}

type AssignRoomRequest struct {
	Order            PMSOrderOperationContext
	TargetRoomTypeID string
	TargetHomeID     string
	HomeStatus       string
	ControlStatus    string
}

type ChangeOccupiedRoomRequest struct {
	Order               PMSOrderOperationContext
	TargetRoomTypeID    string
	TargetHomeID        string
	TargetHomeStatus    string
	TargetControlStatus string
}

type ExtendCheckoutRequest struct {
	Order              PMSOrderOperationContext
	TargetCheckOutTime string
}

type DeferredOperationResult struct {
	ReserveOrderID string
	ReceptOrderID  string
	RoomTypeID     string
	HomeID         string
	CheckInTime    string
	CheckOutTime   string
	OrderStatus    string
}

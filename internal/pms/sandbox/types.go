package sandbox

import (
	"context"
	"time"
)

const (
	Provider = "sandbox"
	Label    = "测试 PMS"
)

type Scope struct {
	StoreID         int64
	ConversationID  int64
	CustomerID      int64
	SourceMessageID int64
}

type Dataset struct {
	ID        int64     `json:"id"`
	StoreID   int64     `json:"storeId"`
	Version   int64     `json:"version"`
	CreatedAt time.Time `json:"createdAt"`
}

type RoomType struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Rank       int    `json:"rank"`
	PriceCents int64  `json:"priceCents"`
	Enabled    bool   `json:"enabled"`
}

type Room struct {
	ID          int64  `json:"id"`
	RoomTypeID  int64  `json:"roomTypeId"`
	Number      string `json:"number"`
	Floor       string `json:"floor"`
	CleanStatus string `json:"cleanStatus"`
	Description string `json:"description"`
	Enabled     bool   `json:"enabled"`
}

type Order struct {
	ID                int64     `json:"id"`
	Number            string    `json:"number"`
	GuestName         string    `json:"guestName"`
	Phone             string    `json:"phone"`
	RoomTypeID        int64     `json:"roomTypeId"`
	RoomTypeName      string    `json:"roomTypeName"`
	RoomID            int64     `json:"roomId"`
	RoomNumber        string    `json:"roomNumber"`
	CheckIn           time.Time `json:"checkIn"`
	CheckOut          time.Time `json:"checkOut"`
	PayableCents      int64     `json:"payableCents"`
	IncludesBreakfast bool      `json:"includesBreakfast"`
	Status            string    `json:"status"`
	Version           int64     `json:"version"`
}

type Grade struct {
	ID                 int64    `json:"id"`
	Name               string   `json:"name"`
	Benefits           []string `json:"benefits"`
	BirthdayBenefits   []string `json:"birthdayBenefits"`
	FreeUpgradeMaxRank int      `json:"freeUpgradeMaxRank"`
	Enabled            bool     `json:"enabled"`
}

type Member struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name"`
	Phone      string    `json:"phone"`
	GradeID    int64     `json:"gradeId"`
	GradeName  string    `json:"gradeName"`
	Birthday   string    `json:"birthday"`
	ValidUntil time.Time `json:"validUntil"`
	Enabled    bool      `json:"enabled"`
}

type Rule struct {
	ID             int64      `json:"id"`
	Code           string     `json:"code"`
	Name           string     `json:"name"`
	Text           string     `json:"text"`
	Action         string     `json:"action"`
	AmountCents    int64      `json:"amountCents"`
	CheckoutTime   string     `json:"checkoutTime"`
	MinimumGradeID int64      `json:"minimumGradeId"`
	Enabled        bool       `json:"enabled"`
	ValidFrom      *time.Time `json:"validFrom,omitempty"`
	ValidUntil     *time.Time `json:"validUntil,omitempty"`
}

type Resource struct {
	ID              int64  `json:"id"`
	Code            string `json:"code"`
	Name            string `json:"name"`
	Token           string `json:"token"`
	CardPayload     string `json:"cardPayload,omitempty"`
	MessageType     string `json:"messageType"`
	SourceMessageID int64  `json:"sourceMessageId"`
	Enabled         bool   `json:"enabled"`
}

type Binding struct {
	ID         int64 `json:"id"`
	CustomerID int64 `json:"customerId"`
	OrderID    int64 `json:"orderId"`
	MemberID   int64 `json:"memberId"`
}

type RoomAvailability struct {
	RoomType  RoomType `json:"roomType"`
	Rooms     []Room   `json:"rooms"`
	Available int      `json:"available"`
}

type CustomerState struct {
	Label        string             `json:"label"`
	Dataset      Dataset            `json:"dataset"`
	Order        *Order             `json:"order,omitempty"`
	Member       *Member            `json:"member,omitempty"`
	Grade        *Grade             `json:"grade,omitempty"`
	Rules        []Rule             `json:"rules"`
	Availability []RoomAvailability `json:"availability"`
	Resource     *Resource          `json:"resource,omitempty"`
}

type ChangeRequest struct {
	OrderID          int64      `json:"orderId"`
	TargetRoomTypeID int64      `json:"targetRoomTypeId"`
	TargetRoomID     int64      `json:"targetRoomId"`
	CheckOut         *time.Time `json:"checkOut,omitempty"`
	RuleID           int64      `json:"ruleId"`
	Upgrade          bool       `json:"upgrade"`
	ChangeRoom       bool       `json:"changeRoom"`
	LateCheckout     bool       `json:"lateCheckout"`
	Recovery         bool       `json:"recovery"`
	Reason           string     `json:"reason"`
}

type Plan struct {
	Before          Order         `json:"before"`
	After           Order         `json:"after"`
	Request         ChangeRequest `json:"request"`
	AddedCents      int64         `json:"addedCents"`
	Commitment      string        `json:"commitment"`
	DatasetVersion  int64         `json:"datasetVersion"`
	MemberID        int64         `json:"memberId"`
	RoomDescription string        `json:"roomDescription"`
}

type Operation struct {
	ID                    int64      `json:"id"`
	Provider              string     `json:"provider"`
	StoreID               int64      `json:"storeId"`
	DatasetID             int64      `json:"datasetId"`
	ConversationID        int64      `json:"conversationId"`
	SourceMessageID       int64      `json:"sourceMessageId"`
	PreviewMessageID      int64      `json:"previewMessageId"`
	ConfirmationMessageID int64      `json:"confirmationMessageId"`
	OperatorID            int64      `json:"operatorId"`
	OperationType         string     `json:"operationType"`
	Status                string     `json:"status"`
	PreviewText           string     `json:"previewText"`
	ResultText            string     `json:"resultText"`
	ErrorMessage          string     `json:"errorMessage"`
	Plan                  *Plan      `json:"plan,omitempty"`
	ExpiresAt             *time.Time `json:"expiresAt,omitempty"`
	CreatedAt             time.Time  `json:"createdAt"`
	DeliveryStatus        string     `json:"deliveryStatus"`
}

type Snapshot struct {
	Label      string      `json:"label"`
	Dataset    *Dataset    `json:"dataset,omitempty"`
	RoomTypes  []RoomType  `json:"roomTypes"`
	Rooms      []Room      `json:"rooms"`
	Orders     []Order     `json:"orders"`
	Grades     []Grade     `json:"grades"`
	Members    []Member    `json:"members"`
	Rules      []Rule      `json:"rules"`
	Resources  []Resource  `json:"resources"`
	Bindings   []Binding   `json:"bindings"`
	Operations []Operation `json:"operations"`
}

type SaveRequest struct {
	DatasetID int64     `json:"datasetId"`
	Version   int64     `json:"version"`
	RoomType  *RoomType `json:"roomType,omitempty"`
	Room      *Room     `json:"room,omitempty"`
	Order     *Order    `json:"order,omitempty"`
	Grade     *Grade    `json:"grade,omitempty"`
	Member    *Member   `json:"member,omitempty"`
	Rule      *Rule     `json:"rule,omitempty"`
	Resource  *Resource `json:"resource,omitempty"`
}

type BindingRequest struct {
	DatasetID  int64 `json:"datasetId"`
	Version    int64 `json:"version"`
	CustomerID int64 `json:"customerId"`
	OrderID    int64 `json:"orderId"`
	MemberID   int64 `json:"memberId"`
}

type SceneInput struct {
	Scene       string         `json:"scene"`
	Question    string         `json:"question"`
	Topics      []string       `json:"topics"`
	Phone       string         `json:"phone"`
	OrderNumber string         `json:"orderNumber"`
	Change      *ChangeRequest `json:"change,omitempty"`
}

type SceneResult struct {
	Scene      string         `json:"scene"`
	Reply      string         `json:"reply"`
	State      *CustomerState `json:"state,omitempty"`
	Operation  *Operation     `json:"operation,omitempty"`
	Resource   *Resource      `json:"resource,omitempty"`
	NeedsInput []string       `json:"needsInput,omitempty"`
	Completed  bool           `json:"completed"`
}

// Service is implemented by services.PMSSandboxService. Dashboard callers must
// enforce their existing store permissions before invoking administrative methods.
type Service interface {
	Snapshot(context.Context, int64) (*Snapshot, error)
	Initialize(context.Context, int64, int64, bool) (*Snapshot, error)
	Reset(context.Context, int64, int64, int64, int64) (*Snapshot, error)
	Save(context.Context, int64, int64, SaveRequest) (*Snapshot, error)
	Bind(context.Context, int64, int64, BindingRequest) (*Snapshot, error)
	Query(context.Context, Scope) (*CustomerState, error)
	ExecuteScene(context.Context, Scope, SceneInput) (*SceneResult, error)
	Prepare(context.Context, Scope, ChangeRequest) (*Operation, error)
	Confirm(context.Context, Scope, int64) (*Operation, error)
	Cancel(context.Context, Scope, int64) (*Operation, error)
	Pending(context.Context, Scope) (*Operation, error)
	Latest(context.Context, Scope) (*Operation, error)
	Result(context.Context, Scope, int64) (*Operation, error)
	MarkPreviewMessage(context.Context, Scope, int64, int64) error
}

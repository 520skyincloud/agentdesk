package models

import (
	"agent-desk/internal/pkg/enums"
	"time"
)

// Models 注册所有需要迁移和代码生成的模型。
var Models = []any{
	&Migration{},
	&User{},
	&UserIdentity{},
	&TenantIndustryChangeLog{},
	&Tenant{},
	&TenantInvitation{},
	&TenantRegistrationLog{},
	&Customer{},
	&CustomerIdentity{},
	&StoreCustomerRelation{},
	&WxWorkCustomerHandoffSetting{},
	&CustomerContact{},
	&Role{},
	&Permission{},
	&UserRole{},
	&UserRoleChangeLog{},
	&RolePermission{},
	&RolePermissionChangeLog{},
	&LoginSession{},
	&LoginCredentialLog{},
	&EmailVerificationCode{},
	&Asset{},
	&IndustryTagDefinition{},
	&Tag{},
	&TenantCustomerTagPolicy{},
	&StoreCustomerTagRuntimePolicy{},
	&CustomerTagRelation{},
	&CustomerTagChangeLog{},
	&StoreCustomerTagDecision{},
	&ConversationEvolutionState{},
	&ConversationEvolutionRun{},
	&Conversation{},
	&ConversationChannelSession{},
	&ConversationContinuityLink{},
	&Store{},
	&StoreStaffBinding{},
	&WxWorkProtocolDevicePoolInstance{},
	&WxWorkProtocolInstance{},
	&MiniProgramIdentity{},
	&WeComSuiteCredential{},
	&WeComTenantAuthorization{},
	&StoreArrivalConnection{},
	&StoreArrivalInvitation{},
	&WeComAuthorizationAttempt{},
	&ArrivalScanEvent{},
	&ArrivalSession{},
	&ArrivalContactWay{},
	&ArrivalAcquisitionLink{},
	&ArrivalStoreBinding{},
	&ArrivalBindingTicket{},
	&WeComProviderCallbackEvent{},
	&ArrivalAuditLog{},
	&ConversationParticipant{},
	&ConversationReadState{},
	&Message{},
	&MessageAnalysis{},
	&WxWorkKFSyncState{},
	&WxWorkKFConversation{},
	&WxWorkKFMessageRef{},
	&ChannelMessageOutbox{},
	&ConversationRouteState{},
	&AIReplyTurn{},
	&AIReplyTurnTask{},
	&AIReplyTurnAction{},
	&AIReplyJob{},
	&AIManualResumeTask{},
	&ConversationSessionSummary{},
	&ConversationDialogueState{},
	&ConversationServiceSession{},
	&ConversationResponseSpan{},
	&AgentPresenceSession{},
	&QualityTemplate{},
	&QualityTemplateItem{},
	&QualityInspection{},
	&QualityInspectionItem{},
	&QualitySamplingBatch{},
	&QualitySamplingItem{},
	&DispatchDecisionLog{},
	&ServiceAnalyticsPolicy{},
	&ConversationEvaluation{},
	&ReportViewPreset{},
	&MessageSyncLog{},
	&ConversationAssignment{},
	&ConversationTakeoverRequest{},
	&QuickReply{},
	&ConversationEventLog{},
	&Ticket{},
	&TicketTag{},
	&TicketProgress{},
	&TicketView{},
	&TicketNoSequence{},
	&Notification{},
	&AIAgent{},
	&Channel{},
	&AgentProfile{},
	&AgentTeam{},
	&AgentTeamSquad{},
	&AgentTeamSquadMember{},
	&AgentTeamSchedule{},
	&ModelProfileTemplate{},
	&ModelProfileSlot{},
	&ModelProfileTestRun{},
	&StoreModelProfileAssignment{},
	&StoreModelCredential{},
	&StoreCredentialPolicy{},
	&StoreModelCredentialAuditLog{},
	&KnowledgeBase{},
	&KnowledgeRetrieveLog{},
	&KnowledgeRetrieveHit{},
	&KnowledgeFeedback{},
	&KnowledgeCandidate{},
	&KnowledgeResourceGroup{},
	&KnowledgeResourceItem{},
	&FastGPTStoreTenant{},
	&FastGPTUsageSyncState{},
	&FastGPTDatasetJob{},
	&SkillDefinition{},
	&SkillRunLog{},
	&AgentRunLog{},
	&AIUsageEvent{},
	&AIUsageGatewayCall{},
	&ReplyIntentProfile{},
	&ReplyIntentConfig{},
	&ConversationInterrupt{},
	&SystemConfig{},
}

type Migration struct {
	ID         int64     `gorm:"primaryKey;autoIncrement"`
	Version    int64     `gorm:"type:bigint;not null;uniqueIndex"`
	Remark     string    `gorm:"type:text"`
	Success    bool      `gorm:"not null;default:false"`
	ErrorInfo  string    `gorm:"type:text"`
	RetryCount int       `gorm:"type:int;not null;default:0"`
	CreatedAt  time.Time `gorm:"type:datetime"`
	UpdatedAt  time.Time `gorm:"type:datetime"`
}

// WxWorkProtocolDevicePoolInstance 记录聚合智能后台同步到本地的真实 XBot 实例池。
type WxWorkProtocolDevicePoolInstance struct {
	ID                            int64        `gorm:"primaryKey;autoIncrement"`
	ProviderInstanceID            int64        `gorm:"type:bigint;not null;default:0;index"`
	Guid                          string       `gorm:"type:varchar(128);not null;default:'';uniqueIndex"`
	Uin                           string       `gorm:"type:varchar(128);not null;default:'';index"`
	ProviderUserID                int64        `gorm:"type:bigint;not null;default:0;index"`
	ClientType                    int          `gorm:"type:int;not null;default:0;index"`
	SeatName                      string       `gorm:"type:varchar(120);not null;default:''"`
	BridgeID                      string       `gorm:"type:varchar(128);not null;default:'';index"`
	State                         string       `gorm:"type:varchar(80);not null;default:'';index"`
	ExpiredAt                     *time.Time   `gorm:"type:datetime;index"`
	SyncStatus                    string       `gorm:"type:varchar(40);not null;default:'unknown';index"`
	LastSyncedAt                  *time.Time   `gorm:"type:datetime;index"`
	BoundWxWorkProtocolInstanceID int64        `gorm:"type:bigint;not null;default:0;index"`
	RawJSON                       string       `gorm:"type:text"`
	Status                        enums.Status `gorm:"type:int;not null;default:0;index"`
	Remark                        string       `gorm:"type:text"`
	AuditFields
}

// SystemConfig 运营侧系统配置项；具体有哪些 config_key 由业务代码约定，表内一行一项。
type SystemConfig struct {
	ID          int64        `gorm:"primaryKey;autoIncrement"`
	ConfigKey   string       `gorm:"column:config_key;type:varchar(128);not null;uniqueIndex"`
	ConfigValue string       `gorm:"column:config_value;type:text;not null"`
	GroupCode   string       `gorm:"column:group_code;type:varchar(64);not null;default:'';index"`
	Title       string       `gorm:"type:varchar(200);not null;default:''"`
	Description string       `gorm:"type:text"`
	Status      enums.Status `gorm:"type:int;not null;default:0;index"`
	AuditFields
}

// TicketNoSequence 工单号日序列表。
//
// 每天一条记录，NextSeq 表示当日下一次可分配的序号。
type TicketNoSequence struct {
	ID        int64     `gorm:"primaryKey;autoIncrement"`
	DateKey   string    `gorm:"column:date_key;type:varchar(8);not null;uniqueIndex"`
	NextSeq   int64     `gorm:"column:next_seq;type:bigint;not null;default:1"`
	CreatedAt time.Time `gorm:"type:datetime;not null;index"`
	UpdatedAt time.Time `gorm:"type:datetime;not null;index"`
}

// TicketView 工单工作台个人保存视图。
type TicketView struct {
	ID          int64  `gorm:"primaryKey;autoIncrement"`
	TenantID    int64  `gorm:"type:bigint;not null;default:0;index"`
	UserID      int64  `gorm:"column:user_id;type:bigint;not null;index"`
	Name        string `gorm:"column:name;type:varchar(100);not null;default:'';index"`
	FiltersJSON string `gorm:"column:filters_json;type:text;not null"`
	SortNo      int    `gorm:"column:sort_no;type:int;not null;default:0;index"`
	AuditFields
}

// Notification 站内通知。
type Notification struct {
	ID               int64        `gorm:"primaryKey;autoIncrement"`
	TenantID         int64        `gorm:"type:bigint;not null;default:0;index"`
	RecipientUserID  int64        `gorm:"type:bigint;not null;default:0;index"`
	Title            string       `gorm:"type:varchar(255);not null;default:''"`
	Content          string       `gorm:"type:text"`
	NotificationType string       `gorm:"type:varchar(50);not null;default:'';index"`
	BizType          string       `gorm:"type:varchar(50);not null;default:'';index"`
	BizID            int64        `gorm:"type:bigint;not null;default:0;index"`
	ActionURL        string       `gorm:"type:varchar(255);not null;default:''"`
	ReadAt           *time.Time   `gorm:"type:datetime;index"`
	Status           enums.Status `gorm:"type:int;not null;default:0;index"`
	CreatedAt        time.Time    `gorm:"type:datetime;not null;index"`
}

// AuditFields 定义涉及用户操作数据的统一审计字段。
// 该结构记录数据创建与更新的时间、操作者ID和操作者名称。
type AuditFields struct {
	CreatedAt      time.Time `gorm:"type:datetime;not null;index"`          // CreatedAt 记录数据创建时间。
	CreateUserID   int64     `gorm:"type:bigint;not null;default:0;index"`  // CreateUserID 记录创建人用户ID；系统任务写0。
	CreateUserName string    `gorm:"type:varchar(100);not null;default:''"` // CreateUserName 记录创建人名称；系统任务写system。
	UpdatedAt      time.Time `gorm:"type:datetime;not null;index"`          // UpdatedAt 记录数据最近更新时间。
	UpdateUserID   int64     `gorm:"type:bigint;not null;default:0;index"`  // UpdateUserID 记录最后更新人用户ID；系统任务写0。
	UpdateUserName string    `gorm:"type:varchar(100);not null;default:''"` // UpdateUserName 记录最后更新人名称；系统任务写system。
}

// User 后台用户账号。
type User struct {
	ID                 int64                        `gorm:"primaryKey;autoIncrement"`
	TenantID           int64                        `gorm:"type:bigint;not null;default:0;index"`
	Username           string                       `gorm:"type:varchar(100);not null;uniqueIndex"`
	Nickname           string                       `gorm:"type:varchar(100);not null;default:'';index"`
	Avatar             string                       `gorm:"type:varchar(255);not null;default:''"`
	Mobile             *string                      `gorm:"type:varchar(32);uniqueIndex"`
	Email              *string                      `gorm:"type:varchar(100);uniqueIndex"`
	EmailVerifiedAt    *time.Time                   `gorm:"type:datetime;index"`
	Password           string                       `gorm:"type:varchar(255);not null;default:''"`
	PasswordSalt       string                       `gorm:"type:varchar(64);not null;default:''"`
	RegistrationSource enums.UserRegistrationSource `gorm:"type:varchar(30);not null;default:'platform_created';index"`
	ApprovalStatus     enums.UserApprovalStatus     `gorm:"type:varchar(20);not null;default:'approved';index"`
	ApprovedAt         *time.Time                   `gorm:"type:datetime;index"`
	ApprovedBy         int64                        `gorm:"type:bigint;not null;default:0;index"`
	ApprovalRemark     string                       `gorm:"type:varchar(500);not null;default:''"`
	MustChangePassword bool                         `gorm:"not null;default:false;index"`
	Status             enums.Status                 `gorm:"type:int;not null;default:0;index"`
	LastLoginAt        *time.Time                   `gorm:"type:datetime"`
	LastLoginIP        string                       `gorm:"type:varchar(64);not null;default:''"`
	Remark             string                       `gorm:"type:text"`
	DeletedAt          *time.Time                   `gorm:"type:datetime;index"`
	AuditFields
}

// Tenant 是平台接入的独立客户公司，也是所有租户业务数据的隔离根。
type Tenant struct {
	ID                 int64                          `gorm:"primaryKey;autoIncrement"`
	IntentProfileID    int64                          `gorm:"type:bigint;not null;default:0;index"`
	TenantCode         string                         `gorm:"type:varchar(64);not null;uniqueIndex"`
	LegalName          string                         `gorm:"type:varchar(200);not null;default:'';index"`
	ShortName          string                         `gorm:"type:varchar(100);not null;default:'';index"`
	RegistrationType   string                         `gorm:"type:varchar(30);not null;default:'';uniqueIndex:uk_tenant_registration"`
	RegistrationNo     string                         `gorm:"type:varchar(64);not null;default:'';uniqueIndex:uk_tenant_registration"`
	ContactName        string                         `gorm:"type:varchar(100);not null;default:''"`
	ContactMobile      string                         `gorm:"type:varchar(32);not null;default:''"`
	ContactEmail       string                         `gorm:"type:varchar(100);not null;default:''"`
	Address            string                         `gorm:"type:varchar(500);not null;default:''"`
	VerificationStatus enums.TenantVerificationStatus `gorm:"type:varchar(30);not null;default:'pending';index"`
	VerifiedAt         *time.Time                     `gorm:"type:datetime;index"`
	VerifiedBy         int64                          `gorm:"type:bigint;not null;default:0;index"`
	Status             enums.Status                   `gorm:"type:int;not null;default:0;index"`
	Remark             string                         `gorm:"type:text"`
	AuditFields
}

// TenantInvitation 保存租户唯一有效邀请码的哈希和可受控展示的密文。
type TenantInvitation struct {
	ID             int64        `gorm:"primaryKey;autoIncrement"`
	TenantID       int64        `gorm:"type:bigint;not null;index;uniqueIndex:uk_tenant_invitation_version"`
	CodeHash       string       `gorm:"type:varchar(64);not null;uniqueIndex"`
	CodeCiphertext string       `gorm:"type:text;not null"`
	CodeLast4      string       `gorm:"type:varchar(4);not null;default:''"`
	Version        int          `gorm:"type:int;not null;default:1;uniqueIndex:uk_tenant_invitation_version"`
	UsedCount      int64        `gorm:"type:bigint;not null;default:0"`
	LastUsedAt     *time.Time   `gorm:"type:datetime;index"`
	ExpiresAt      *time.Time   `gorm:"type:datetime;index"`
	RotatedAt      *time.Time   `gorm:"type:datetime;index"`
	Status         enums.Status `gorm:"type:int;not null;default:0;index"`
	AuditFields
}

// TenantRegistrationLog 是邀请校验和注册结果的不可变安全日志。
type TenantRegistrationLog struct {
	ID                 int64                          `gorm:"primaryKey;autoIncrement"`
	RequestID          string                         `gorm:"type:varchar(128);not null;default:'';uniqueIndex"`
	RequestFingerprint string                         `gorm:"type:varchar(64);not null;default:'';index"`
	Action             enums.TenantRegistrationAction `gorm:"type:varchar(30);not null;default:'';index"`
	TenantID           int64                          `gorm:"type:bigint;not null;default:0;index"`
	InvitationID       int64                          `gorm:"type:bigint;not null;default:0;index"`
	InviteHash         string                         `gorm:"type:varchar(64);not null;default:'';index"`
	UserID             int64                          `gorm:"type:bigint;not null;default:0;index"`
	Principal          string                         `gorm:"type:varchar(150);not null;default:'';index"`
	Success            bool                           `gorm:"not null;default:false;index"`
	Reason             string                         `gorm:"type:varchar(255);not null;default:''"`
	ClientIP           string                         `gorm:"type:varchar(64);not null;default:'';index"`
	UserAgent          string                         `gorm:"type:varchar(255);not null;default:''"`
	OperatorID         int64                          `gorm:"type:bigint;not null;default:0;index"`
	OperatorName       string                         `gorm:"type:varchar(100);not null;default:''"`
	CreatedAt          time.Time                      `gorm:"type:datetime;not null;index"`
}

// UserIdentity 第三方身份绑定信息。
type UserIdentity struct {
	ID              int64               `gorm:"primaryKey;autoIncrement"`
	UserID          int64               `gorm:"type:bigint;not null;index;uniqueIndex:uk_provider_user"`
	Provider        enums.ThirdProvider `gorm:"type:varchar(50);not null;default:'';index;uniqueIndex:uk_provider_user;uniqueIndex:uk_provider_union"`
	ProviderUserID  string              `gorm:"type:varchar(128);not null;default:'';uniqueIndex:uk_provider_user"`
	ProviderUnionID *string             `gorm:"type:varchar(128);uniqueIndex:uk_provider_union"`
	ProviderCorpID  string              `gorm:"type:varchar(128);not null;default:'';index"`
	ProviderName    string              `gorm:"type:varchar(100);not null;default:''"`
	RawProfile      string              `gorm:"type:text"`
	Status          enums.Status        `gorm:"type:int;not null;default:0;index"`
	LastAuthAt      *time.Time          `gorm:"type:datetime"`
	AuditFields
}

// Customer 客户主表。
//
//	用于存储客户稳定画像信息，不包含平台身份映射和多联系方式明细。
type Customer struct {
	ID            int64        `gorm:"primaryKey;autoIncrement"`                    // ID 为客户主键。
	TenantID      int64        `gorm:"type:bigint;not null;default:0;index"`        // TenantID 为客户所属接入公司。
	Name          string       `gorm:"type:varchar(100);not null;default:'';index"` // Name 为客户姓名或展示名称。
	Avatar        string       `gorm:"type:varchar(1024);not null;default:''"`      // Avatar 为客户头像 URL，可由企微协议联系人资料同步。
	Gender        enums.Gender `gorm:"type:int;not null;default:0;"`                // Gender 为性别：0未知 1男 2女。
	LastActiveAt  *time.Time   `gorm:"type:datetime;"`                              // LastActiveAt 为最近活跃时间。
	PrimaryMobile string       `gorm:"type:varchar(32);not null;default:'';index"`  // PrimaryMobile 为主手机号（冗余展示字段）。
	PrimaryEmail  string       `gorm:"type:varchar(100);not null;default:'';index"` // PrimaryEmail 为主邮箱（冗余展示字段）。
	Status        enums.Status `gorm:"type:int;not null;default:0;"`                // Status 为客户状态。
	Remark        string       `gorm:"type:text"`                                   // Remark 为备注。
	AuditFields
}

// CustomerIdentity 客户第三方身份映射表。
type CustomerIdentity struct {
	ID             int64                `gorm:"primaryKey;autoIncrement"`
	TenantID       int64                `gorm:"type:bigint;not null;default:0;index;uniqueIndex:uk_customer_external,priority:1"`
	CustomerID     int64                `gorm:"type:bigint;not null;index"`                                                          // 为所属客户ID。
	ExternalSource enums.ExternalSource `gorm:"type:varchar(30);uniqueIndex:uk_customer_external,priority:2"`                        // 为外部身份来源
	ExternalID     string               `gorm:"type:varchar(128);index:idx_external_id;uniqueIndex:uk_customer_external,priority:3"` // 为平台侧用户唯一ID，与访客 ExternalID 对齐。
	RawProfile     string               `gorm:"type:text"`                                                                           // 为第三方原始资料JSON。
	Status         enums.Status         `gorm:"type:int;not null;default:0;index"`                                                   // 为映射状态。
	AuditFields
}

// StoreCustomerRelation 记录同一自然客户在不同门店下的独立业务关系。
type StoreCustomerRelation struct {
	ID                 int64        `gorm:"primaryKey;autoIncrement"`
	TenantID           int64        `gorm:"type:bigint;not null;default:0;index"`
	CustomerID         int64        `gorm:"type:bigint;not null;index;uniqueIndex:uk_store_customer_relation"`
	StoreID            int64        `gorm:"type:bigint;not null;index;uniqueIndex:uk_store_customer_relation"`
	WxWorkInstanceID   int64        `gorm:"type:bigint;not null;default:0;index"`
	LastConversationID int64        `gorm:"type:bigint;not null;default:0;index"`
	LastActiveAt       *time.Time   `gorm:"type:datetime;index"`
	VisitCount         int          `gorm:"type:int;not null;default:0"`
	Tags               string       `gorm:"type:varchar(500);not null;default:''"`
	StableNotes        string       `gorm:"type:text"`
	Status             enums.Status `gorm:"type:int;not null;default:0;index"`
	AuditFields
}

// WxWorkCustomerHandoffSetting 保存客户在单个门店员工号绑定下的自动转人工偏好。
// 企微实例替换不改变该偏好，不同门店员工号之间仍保持独立。
type WxWorkCustomerHandoffSetting struct {
	ID                  int64  `gorm:"primaryKey;autoIncrement"`
	TenantID            int64  `gorm:"type:bigint;not null;default:0;index;uniqueIndex:uk_customer_store_staff_handoff_setting,priority:1"`
	CustomerID          int64  `gorm:"type:bigint;not null;index;uniqueIndex:uk_customer_store_staff_handoff_setting,priority:2"`
	StoreStaffBindingID *int64 `gorm:"type:bigint;index;uniqueIndex:uk_customer_store_staff_handoff_setting,priority:3"`
	AutoHandoffEnabled  bool   `gorm:"not null;default:true"`
	Remark              string `gorm:"type:varchar(255);not null;default:''"`
	AuditFields
}

// CustomerContact 客户联系方式表。
//
//	用于维护客户的一对多联系方式，支持主联系方式、验证状态与失效标记。
type CustomerContact struct {
	ID           int64             `gorm:"primaryKey;autoIncrement"`
	TenantID     int64             `gorm:"type:bigint;not null;default:0;index"`
	CustomerID   int64             `gorm:"type:bigint;not null;index;uniqueIndex:uk_customer_contact"`                  // CustomerID 为所属客户ID。
	ContactType  enums.ContactType `gorm:"type:varchar(30);not null;default:'';index;uniqueIndex:uk_customer_contact"`  // ContactType 为联系方式类型：mobile/email/wechat/other。
	ContactValue string            `gorm:"type:varchar(200);not null;default:'';index;uniqueIndex:uk_customer_contact"` // ContactValue 为联系方式值。
	IsPrimary    bool              `gorm:"not null;default:false;index"`                                                // IsPrimary 表示是否主联系方式。
	IsVerified   bool              `gorm:"not null;default:false;index"`                                                // IsVerified 表示是否已验证。
	VerifiedAt   *time.Time        `gorm:"type:datetime"`                                                               // VerifiedAt 为验证时间。
	Source       string            `gorm:"type:varchar(30);not null;default:'';index"`                                  // Source 为来源：manual/import/system。
	Status       enums.Status      `gorm:"type:int;not null;default:0;index"`                                           // Status 为联系方式状态。
	Remark       string            `gorm:"type:varchar(255);not null;default:''"`                                       // Remark 为备注。
	AuditFields
}

// Role 角色定义。
type Role struct {
	ID             int64        `gorm:"primaryKey;autoIncrement"`
	Name           string       `gorm:"type:varchar(100);not null;default:'';index"`
	Code           string       `gorm:"type:varchar(100);not null;uniqueIndex"`
	Scope          string       `gorm:"type:varchar(20);not null;default:'tenant';index"`
	AuthorityLevel int          `gorm:"type:int;not null;default:20;index"`
	Status         enums.Status `gorm:"type:int;not null;default:0;index"`
	IsSystem       bool         `gorm:"not null;default:false;index"`
	SortNo         int          `gorm:"type:int;not null;default:0;index"`
	Remark         string       `gorm:"type:text"`
	AuditFields
}

// Permission 权限点定义。
type Permission struct {
	ID        int64        `gorm:"primaryKey;autoIncrement"`
	Name      string       `gorm:"type:varchar(100);not null;default:''"`
	Code      string       `gorm:"type:varchar(150);not null;uniqueIndex"`
	Type      string       `gorm:"type:varchar(20);not null;default:'';index"`
	Scope     string       `gorm:"type:varchar(20);not null;default:'tenant';index"`
	GroupName string       `gorm:"type:varchar(100);not null;default:'';index"`
	ParentID  int64        `gorm:"type:bigint;not null;default:0;index"`
	Path      string       `gorm:"type:varchar(255);not null;default:''"`
	Method    string       `gorm:"type:varchar(20);not null;default:''"`
	APIPath   string       `gorm:"type:varchar(255);not null;default:''"`
	SortNo    int          `gorm:"type:int;not null;default:0;index"`
	Status    enums.Status `gorm:"type:int;not null;default:0;index"`
	IsBuiltin bool         `gorm:"not null;default:true;index"`
	Remark    string       `gorm:"type:text"`
	AuditFields
}

// UserRole 用户和角色关联。
type UserRole struct {
	ID     int64 `gorm:"primaryKey;autoIncrement"`
	UserID int64 `gorm:"type:bigint;not null;index;uniqueIndex:uk_user_role"`
	RoleID int64 `gorm:"type:bigint;not null;index;uniqueIndex:uk_user_role"`
	AuditFields
}

// RolePermission 角色和权限关联。
type RolePermission struct {
	ID           int64 `gorm:"primaryKey;autoIncrement"`
	RoleID       int64 `gorm:"type:bigint;not null;index;uniqueIndex:uk_role_permission"`
	PermissionID int64 `gorm:"type:bigint;not null;index;uniqueIndex:uk_role_permission"`
	AuditFields
}

// LoginSession 表示一次后台登录会话。
type LoginSession struct {
	ID         int64      `gorm:"primaryKey;autoIncrement"`                   // ID 为登录会话主键。
	UserID     int64      `gorm:"type:bigint;not null;index"`                 // UserID 为登录用户 ID。
	Token      string     `gorm:"type:varchar(128);not null;uniqueIndex"`     // Token 为随机不透明登录凭证，使用 ak_ 前缀。
	ClientType string     `gorm:"type:varchar(50);not null;default:'';index"` // ClientType 为客户端类型，后台 Web 端固定为 admin_web。
	ClientIP   string     `gorm:"type:varchar(64);not null;default:''"`       // ClientIP 为登录请求来源 IP。
	UserAgent  string     `gorm:"type:varchar(255);not null;default:''"`      // UserAgent 为登录请求浏览器或客户端 UA。
	ExpiredAt  time.Time  `gorm:"type:datetime;not null;index"`               // ExpiredAt 为 token 过期时间。
	RevokedAt  *time.Time `gorm:"type:datetime;index"`                        // RevokedAt 为主动注销或踢下线时间，非空表示已失效。
	LastSeenAt *time.Time `gorm:"type:datetime"`                              // LastSeenAt 为最近一次成功鉴权时间。
	AuditFields
}

// LoginCredentialLog 记录一次后台登录凭证校验结果。
type LoginCredentialLog struct {
	ID        int64     `gorm:"primaryKey;autoIncrement"`                    // ID 为登录凭证日志主键。
	Principal string    `gorm:"type:varchar(100);not null;default:'';index"` // Principal 为用户输入的登录名。
	UserID    int64     `gorm:"type:bigint;not null;default:0;index"`        // UserID 为匹配到的用户 ID，未匹配时为 0。
	Success   bool      `gorm:"not null;default:false;index"`                // Success 表示本次凭证校验是否成功。
	ClientIP  string    `gorm:"type:varchar(64);not null;default:''"`        // ClientIP 为登录请求来源 IP。
	UserAgent string    `gorm:"type:varchar(255);not null;default:''"`       // UserAgent 为登录请求浏览器或客户端 UA。
	Reason    string    `gorm:"type:varchar(255);not null;default:''"`       // Reason 为校验结果原因。
	CreatedAt time.Time `gorm:"type:datetime;not null;index"`                // CreatedAt 为日志创建时间。
}

// Asset 存储的文件资源，如上传的附件等。
type Asset struct {
	ID         int64               `gorm:"primaryKey;autoIncrement"`
	TenantID   int64               `gorm:"type:bigint;not null;default:0;index"`
	AssetID    string              `gorm:"type:varchar(64);not null;uniqueIndex"`
	Provider   enums.AssetProvider `gorm:"type:varchar(50);not null;default:'';index"`
	StorageKey string              `gorm:"type:varchar(255);not null;default:'';uniqueIndex:uk_storage_key"`
	Filename   string              `gorm:"type:varchar(255);not null;default:''"`
	FileSize   int64               `gorm:"type:bigint;not null;default:0"`
	MimeType   string              `gorm:"type:varchar(100);not null;default:''"`
	Status     enums.AssetStatus   `gorm:"type:int;not null;default:1;index"`
	AuditFields
}

type Tag struct {
	ID                   int64        `gorm:"primaryKey;autoIncrement"`
	TenantID             int64        `gorm:"type:bigint;not null;default:0;index;uniqueIndex:uk_tenant_tag_template,priority:1"`
	IntentProfileID      int64        `gorm:"type:bigint;not null;default:0;index"`
	TemplateDefinitionID *int64       `gorm:"type:bigint;index;uniqueIndex:uk_tenant_tag_template,priority:2"`
	ParentID             int64        `gorm:"type:bigint;not null;default:0;index"`
	Name                 string       `gorm:"type:varchar(80);not null;default:''"`
	DisplayAlias         string       `gorm:"type:varchar(80);not null;default:''"`
	SemanticKey          string       `gorm:"type:varchar(128);not null;default:'';index"`
	Aliases              string       `gorm:"type:text"`
	ConflictGroup        string       `gorm:"type:varchar(80);not null;default:'';index"`
	ApplicableScene      string       `gorm:"type:varchar(255);not null;default:''"`
	AIEnabled            bool         `gorm:"not null;default:false;index"`
	ReplyEnabled         bool         `gorm:"not null;default:false;index"`
	SystemDefined        bool         `gorm:"not null;default:false;index"`
	Remark               string       `gorm:"type:text"`
	SortNo               int          `gorm:"type:int;not null;default:0;index"`
	Status               enums.Status `gorm:"type:int;not null;default:0;index"`
	AuditFields
}

// Conversation 客服会话。
type Conversation struct {
	ID                   int64                           `gorm:"primaryKey;autoIncrement"`                                          // ID 为会话主键。
	TenantID             int64                           `gorm:"type:bigint;not null;default:0;index"`                              // TenantID 为会话所属接入公司，从 Channel 与 Customer 共同确定。
	StoreID              int64                           `gorm:"type:bigint;not null;default:0;index"`                              // StoreID 为会话不可跨越的稳定门店范围。
	StoreStaffBindingID  int64                           `gorm:"type:bigint;not null;default:0;index"`                              // StoreStaffBindingID 为当前承接该会话的门店员工号绑定。
	ThreadKey            *string                         `gorm:"type:varchar(191);uniqueIndex:uk_conversation_thread_key" json:"-"` // ThreadKey 保证同一门店员工号下客户会话唯一；非门店渠道保持 NULL。
	AIAgentID            int64                           `gorm:"type:bigint;not null;default:0;index"`                              // AIAgentID 为当前会话绑定的 AI Agent ID。
	ChannelID            int64                           `gorm:"type:bigint;not null;default:0;index"`                              // ChannelID 为该会话来源接入渠道ID。
	CustomerID           int64                           `gorm:"type:bigint;not null;default:0;index"`                              // CustomerID 为会话所属客户 ID。
	CustomerName         string                          `gorm:"type:varchar(100);not null;default:'';index"`                       // CustomerName 为客户名称冗余字段，用于列表展示和搜索。
	Status               enums.IMConversationStatus      `gorm:"type:int;not null;default:1;index"`                                 // Status 为会话状态，如待接入、处理中、已关闭。
	ServiceMode          enums.IMConversationServiceMode `gorm:"type:int;not null;default:3;index"`                                 // ServiceMode 为服务模式，如仅AI、仅人工、AI优先人工接管。
	Priority             int                             `gorm:"type:int;not null;default:0;index"`                                 // Priority 为会话优先级。
	DispatchWeight       int                             `gorm:"type:int;not null;default:1"`                                       // DispatchWeight 为规则派单使用的当前人工工作量权重，1 表示普通任务。
	CurrentAssigneeID    int64                           `gorm:"type:bigint;not null;default:0;index"`                              // CurrentAssigneeID 为当前接待客服ID。
	CurrentTeamID        int64                           `gorm:"type:bigint;not null;default:0;index"`                              // CurrentTeamID 为当前处理客服组ID。
	LastMessageID        int64                           `gorm:"type:bigint;not null;default:0;index"`                              // LastMessageID 为最后一条消息ID。
	LastMessageAt        time.Time                       `gorm:"type:datetime;index"`                                               // LastMessageAt 为最后消息时间。
	LastActiveAt         time.Time                       `gorm:"type:datetime;index"`                                               // LastActiveAt 为会话最近活跃时间。
	LastMessageSummary   string                          `gorm:"type:varchar(255);not null;default:''"`                             // LastMessageSummary 为最后一条消息摘要。
	CustomerUnreadCount  int                             `gorm:"type:int;not null;default:0"`                                       // CustomerUnreadCount 为用户侧未读数。
	AgentUnreadCount     int                             `gorm:"type:int;not null;default:0"`                                       // AgentUnreadCount 为客服侧未读数。
	HandoffAt            *time.Time                      `gorm:"type:datetime;index"`                                               // HandoffAt 为最近一次转人工时间。
	HandoffReason        string                          `gorm:"type:varchar(255);not null;default:''"`                             // HandoffReason 为最近一次转人工原因。
	AIReplyRounds        int                             `gorm:"type:int;not null;default:0"`                                       // AIReplyRounds 为当前会话内 AI 已成功回复次数。
	CurrentAIReplyTurnID int64                           `gorm:"type:bigint;not null;default:0;index"`                              // CurrentAIReplyTurnID 为当前可接收客户消息的持久回复轮次，仅供内部协调。
	ClosedAt             *time.Time                      `gorm:"type:datetime;index"`                                               // ClosedAt 为会话关闭时间。
	ClosedBy             int64                           `gorm:"type:bigint;not null;default:0;index"`                              // ClosedBy 为关闭人用户ID，访客关闭时写0。
	CloseReason          string                          `gorm:"type:varchar(255);not null;default:''"`                             // CloseReason 为关闭原因。
	AuditFields
}

// ConversationTakeoverRequest records one active request to take over an
// unassigned conversation from AI or the human pool. Assigned conversations
// continue through the existing transfer flow. ActiveKey is nullable so
// completed requests remain immutable history while a unique key prevents
// duplicate pending requests for the same conversation session.
type ConversationTakeoverRequest struct {
	ID                int64                                   `gorm:"primaryKey;autoIncrement"`
	TenantID          int64                                   `gorm:"type:bigint;not null;default:0;index"`
	ConversationID    int64                                   `gorm:"type:bigint;not null;default:0;index"`
	SessionNo         int                                     `gorm:"type:int;not null;default:1;index"`
	TeamID            int64                                   `gorm:"type:bigint;not null;default:0;index"`
	RequesterUserID   int64                                   `gorm:"type:bigint;not null;default:0;index"`
	RequesterName     string                                  `gorm:"type:varchar(100);not null;default:''"`
	SourceAssigneeID  int64                                   `gorm:"type:bigint;not null;default:0;index"`
	SourceRouteStatus enums.ConversationRouteStatus           `gorm:"type:varchar(40);not null;default:'';index"`
	Reason            string                                  `gorm:"type:varchar(500);not null;default:''"`
	Status            enums.ConversationTakeoverRequestStatus `gorm:"type:varchar(20);not null;default:'pending';index"`
	ReviewerUserID    int64                                   `gorm:"type:bigint;not null;default:0;index"`
	ReviewerName      string                                  `gorm:"type:varchar(100);not null;default:''"`
	ReviewRemark      string                                  `gorm:"type:varchar(500);not null;default:''"`
	ReviewedAt        *time.Time                              `gorm:"type:datetime;index"`
	TerminalReason    string                                  `gorm:"type:varchar(100);not null;default:'';index"`
	ActiveKey         *string                                 `gorm:"type:varchar(191);uniqueIndex:uk_conversation_takeover_active"`
	AuditFields
}

// ConversationChannelSession 固化一次会话轮次使用的门店员工号与企微实例。
// 当前路由可以变化，历史消息来源不得通过当前实例反推。
type ConversationChannelSession struct {
	ID                        int64        `gorm:"primaryKey;autoIncrement"`
	TenantID                  int64        `gorm:"type:bigint;not null;default:0;index"`
	ConversationID            int64        `gorm:"type:bigint;not null;default:0;index;uniqueIndex:uk_conversation_channel_session,priority:1"`
	SessionNo                 int          `gorm:"type:int;not null;default:1;index;uniqueIndex:uk_conversation_channel_session,priority:2"`
	StoreID                   int64        `gorm:"type:bigint;not null;default:0;index"`
	StoreStaffBindingID       int64        `gorm:"type:bigint;not null;default:0;index"`
	WxWorkInstanceID          int64        `gorm:"type:bigint;not null;default:0;index"`
	ChannelID                 int64        `gorm:"type:bigint;not null;default:0;index"`
	StartReason               string       `gorm:"type:varchar(50);not null;default:'';index"`
	StoreStaffDisplayName     string       `gorm:"type:varchar(120);not null;default:''"`
	WxWorkEmployeeDisplayName string       `gorm:"type:varchar(120);not null;default:''"`
	StartedAt                 time.Time    `gorm:"type:datetime;not null;index"`
	EndedAt                   *time.Time   `gorm:"type:datetime;index"`
	Status                    enums.Status `gorm:"type:int;not null;default:0;index"`
	AuditFields
}

// ConversationContinuityLink preserves an explicit handoff between two
// physical conversations. Messages stay on their original conversation; the
// link only defines the predecessor/successor reading lineage.
type ConversationContinuityLink struct {
	ID                        int64        `gorm:"primaryKey;autoIncrement"`
	TenantID                  int64        `gorm:"type:bigint;not null;default:0;index;uniqueIndex:uk_conversation_continuity_predecessor,priority:1;uniqueIndex:uk_conversation_continuity_successor,priority:1"`
	StoreID                   int64        `gorm:"type:bigint;not null;default:0;index"`
	CustomerID                int64        `gorm:"type:bigint;not null;default:0;index"`
	PredecessorConversationID int64        `gorm:"type:bigint;not null;default:0;index;uniqueIndex:uk_conversation_continuity_predecessor,priority:2"`
	SuccessorConversationID   int64        `gorm:"type:bigint;not null;default:0;index;uniqueIndex:uk_conversation_continuity_successor,priority:2"`
	Reason                    string       `gorm:"type:varchar(255);not null;default:''"`
	Status                    enums.Status `gorm:"type:int;not null;default:0;index"`
	AuditFields
}

// Store 是租户内稳定、独立的门店业务身份；多个门店员工号可以绑定同一门店。
type Store struct {
	ID              int64        `gorm:"primaryKey;autoIncrement"`
	TenantID        int64        `gorm:"type:bigint;not null;default:0;index;uniqueIndex:uk_store_tenant_code,priority:1"`
	StoreCode       string       `gorm:"type:varchar(64);not null;default:'';uniqueIndex:uk_store_tenant_code,priority:2"`
	Name            string       `gorm:"type:varchar(120);not null;default:'';index"`
	BrandName       string       `gorm:"type:varchar(120);not null;default:'';index"`
	Address         string       `gorm:"type:varchar(500);not null;default:''"`
	NavigationName  string       `gorm:"type:varchar(200);not null;default:''"`
	Longitude       string       `gorm:"type:varchar(50);not null;default:''"`
	Latitude        string       `gorm:"type:varchar(50);not null;default:''"`
	MapProvider     string       `gorm:"type:varchar(50);not null;default:''"`
	ContactPhone    string       `gorm:"type:varchar(120);not null;default:''"`
	KnowledgeBaseID int64        `gorm:"type:bigint;not null;default:0;index"`
	Status          enums.Status `gorm:"type:int;not null;default:0;index"`
	Remark          string       `gorm:"type:text"`
	AuditFields
}

// StoreStaffBinding 把一个已有门店员工号角色账号绑定到稳定门店身份。
type StoreStaffBinding struct {
	ID                      int64        `gorm:"primaryKey;autoIncrement"`
	TenantID                int64        `gorm:"type:bigint;not null;default:0;index;index:idx_store_staff_tenant_store,priority:1"`
	UserID                  int64        `gorm:"type:bigint;not null;default:0;index"`
	ActiveUserID            *int64       `gorm:"type:bigint;uniqueIndex:uk_store_staff_active_user" json:"-"` // ActiveUserID 仅在启用绑定中等于 UserID，用可空唯一键保证一账号一门店。
	AgentTeamID             int64        `gorm:"type:bigint;not null;default:0;index"`                        // AgentTeamID 为门店员工所属客服组，0 表示暂未分配。
	StoreID                 int64        `gorm:"type:bigint;not null;default:0;index;index:idx_store_staff_tenant_store,priority:2"`
	ManagedMode             string       `gorm:"type:varchar(20);not null;default:'semi';index"`
	ServiceHours            string       `gorm:"type:varchar(200);not null;default:''"`
	StoreRoomConversationID string       `gorm:"type:varchar(128);not null;default:'';index"`
	StoreRoomNotifyEnabled  bool         `gorm:"not null;default:false"`
	StoreRoomAtList         string       `gorm:"type:varchar(500);not null;default:''"`
	FallbackToHQ            bool         `gorm:"not null;default:true"`
	ManualTimeoutMinutes    int          `gorm:"type:int;not null;default:10"`
	Status                  enums.Status `gorm:"type:int;not null;default:0;index"`
	Remark                  string       `gorm:"type:text"`
	AuditFields
}

// WxWorkProtocolInstance 记录一次企微员工号协议登录身份及运行状态。
type WxWorkProtocolInstance struct {
	ID                             int64        `gorm:"primaryKey;autoIncrement"`
	TenantID                       int64        `gorm:"type:bigint;not null;default:0;index"`
	AgentTeamID                    int64        `gorm:"type:bigint;not null;default:0;index"` // AgentTeamID 同步自门店员工绑定，供派单运行时快速查询。
	Guid                           string       `gorm:"type:varchar(128);not null;default:'';uniqueIndex"`
	ChannelID                      int64        `gorm:"type:bigint;not null;default:0;index"`
	EmployeeUserID                 string       `gorm:"type:varchar(128);not null;default:'';index"`
	EmployeeName                   string       `gorm:"type:varchar(120);not null;default:''"`
	EmployeeAvatar                 string       `gorm:"type:varchar(1024);not null;default:''"`
	StoreID                        int64        `gorm:"type:bigint;not null;default:0;index"`
	StoreStaffBindingID            int64        `gorm:"type:bigint;not null;default:0;index"`
	StoreAddress                   string       `gorm:"type:varchar(500);not null;default:''"`
	StoreNavigationName            string       `gorm:"type:varchar(200);not null;default:''"`
	StoreLongitude                 string       `gorm:"type:varchar(50);not null;default:''"`
	StoreLatitude                  string       `gorm:"type:varchar(50);not null;default:''"`
	StoreMapProvider               string       `gorm:"type:varchar(50);not null;default:''"`
	StoreContactPhone              string       `gorm:"type:varchar(120);not null;default:''"`
	DefaultMiniProgramPayload      string       `gorm:"type:text"`
	WelcomeEnabled                 bool         `gorm:"not null;default:true"`
	WelcomeMessage                 string       `gorm:"type:varchar(500);not null;default:''"`
	WelcomeImageAssetID            string       `gorm:"type:varchar(64);not null;default:'';index"`
	WelcomeSendMiniProgram         bool         `gorm:"not null;default:true"`
	WelcomeAskLocation             bool         `gorm:"not null;default:true"`
	KnowledgeBaseID                int64        `gorm:"type:bigint;not null;default:0;index"`
	NotifyURL                      string       `gorm:"type:varchar(500);not null;default:''"`
	Proxy                          string       `gorm:"type:varchar(500);not null;default:''"`
	BridgeID                       string       `gorm:"type:varchar(128);not null;default:'';index"`
	StaffUserIDs                   string       `gorm:"type:varchar(500);not null;default:''"`
	ServiceHours                   string       `gorm:"type:varchar(200);not null;default:''"`
	FrontDeskMode                  string       `gorm:"type:varchar(30);not null;default:'unmanned';index"`
	FrontDeskHours                 string       `gorm:"type:varchar(200);not null;default:''"`
	StoreRoomConversationID        string       `gorm:"type:varchar(128);not null;default:'';index"`
	StoreRoomNotifyEnabled         bool         `gorm:"not null;default:false"`
	StoreRoomAtList                string       `gorm:"type:varchar(500);not null;default:''"`
	FallbackToHQ                   bool         `gorm:"not null;default:true"`
	ManualTimeoutMinutes           int          `gorm:"type:int;not null;default:10"`
	AIReplyEnabled                 bool         `gorm:"not null;default:true"`
	PersonaPrompt                  string       `gorm:"type:text"`
	AutoAcceptFriendRequest        bool         `gorm:"not null;default:false"`
	AutoAcceptFriendRemarkTemplate string       `gorm:"type:varchar(500);not null;default:''"`
	FriendRequestSyncSeq           string       `gorm:"type:varchar(64);not null;default:''"`
	ContactSyncSeq                 string       `gorm:"type:varchar(64);not null;default:''"`
	MessageSyncSeq                 string       `gorm:"type:varchar(64);not null;default:'';index"`
	MessageSyncUpdatedAt           *time.Time   `gorm:"type:datetime;index"`
	MessageGapFromSeq              string       `gorm:"type:varchar(64);not null;default:'';index"`
	MessageGapToSeq                string       `gorm:"type:varchar(64);not null;default:''"`
	MessageGapDetectedAt           *time.Time   `gorm:"type:datetime;index"`
	MessageRepairLastAt            *time.Time   `gorm:"type:datetime;index"`
	MessageRepairLastError         string       `gorm:"type:varchar(500);not null;default:''"`
	ContactAutomationLastAt        *time.Time   `gorm:"type:datetime;index"`
	ContactAutomationLastError     string       `gorm:"type:varchar(500);not null;default:''"`
	ContextMaxMessages             int          `gorm:"type:int;not null;default:30"`
	ContextMaxTokens               int          `gorm:"type:int;not null;default:8000"`
	ContextCompressionEnabled      bool         `gorm:"not null;default:true"`
	RemoteSetupToken               string       `gorm:"type:varchar(80);not null;default:'';index"`
	RemoteSetupExpiresAt           *time.Time   `gorm:"type:datetime;index"`
	RemoteSetupSubmittedAt         *time.Time   `gorm:"type:datetime;index"`
	ReplacesInstanceID             int64        `gorm:"type:bigint;not null;default:0;index"`
	ReplacedByInstanceID           int64        `gorm:"type:bigint;not null;default:0;index"`
	ReplacedAt                     *time.Time   `gorm:"type:datetime;index"`
	HealthStatus                   string       `gorm:"type:varchar(30);not null;default:'unknown';index"`
	LastHeartbeatAt                *time.Time   `gorm:"type:datetime;index"`
	Status                         enums.Status `gorm:"type:int;not null;default:0;index"`
	Remark                         string       `gorm:"type:text"`
	AuditFields
}

// ConversationRouteState 保存 AgentDesk 本地判定的当前接待路由。
type ConversationRouteState struct {
	ID                    int64                         `gorm:"primaryKey;autoIncrement"`
	TenantID              int64                         `gorm:"type:bigint;not null;default:0;index"`
	ConversationID        int64                         `gorm:"type:bigint;not null;uniqueIndex"`
	StoreID               int64                         `gorm:"type:bigint;not null;default:0;index"`
	StoreStaffBindingID   int64                         `gorm:"type:bigint;not null;default:0;index"`
	KnowledgeBaseID       int64                         `gorm:"type:bigint;not null;default:0;index"`
	WxWorkInstanceID      int64                         `gorm:"type:bigint;not null;default:0;index"`
	RouteStatus           enums.ConversationRouteStatus `gorm:"type:varchar(40);not null;default:'AI_SERVING';index"`
	RouteTarget           string                        `gorm:"type:varchar(60);not null;default:'';index"`
	SessionNo             int                           `gorm:"type:int;not null;default:1;index"`
	SessionStartedAt      *time.Time                    `gorm:"type:datetime;index"`
	LastManualHandoffAt   *time.Time                    `gorm:"type:datetime;index"`
	ManualExpireAt        *time.Time                    `gorm:"type:datetime;index"`
	LastCustomerMessageAt *time.Time                    `gorm:"type:datetime;index"`
	PendingAction         string                        `gorm:"type:varchar(60);not null;default:'';index"`
	PendingActionPayload  string                        `gorm:"type:text"`
	PendingActionExpireAt *time.Time                    `gorm:"type:datetime;index"`
	NeedHumanFollowUp     bool                          `gorm:"not null;default:false;index"`
	HandoffReason         string                        `gorm:"type:varchar(500);not null;default:''"`
	Remark                string                        `gorm:"type:text"`
	AuditFields
}

// AIReplyTurn coordinates consecutive customer messages as one durable reply
// turn. It stores only scope, version and delivery evidence; customer content,
// prompts and model output remain in their existing stores.
type AIReplyTurn struct {
	ID                     int64                   `gorm:"primaryKey;autoIncrement"`
	TenantID               int64                   `gorm:"type:bigint;not null;default:0;index"`
	ConversationID         int64                   `gorm:"type:bigint;not null;default:0;index"`
	SessionNo              int                     `gorm:"type:int;not null;default:1;index"`
	StoreID                int64                   `gorm:"type:bigint;not null;default:0;index"`
	StoreStaffBindingID    int64                   `gorm:"type:bigint;not null;default:0;index"`
	Version                int                     `gorm:"type:int;not null;default:1;index"`
	Status                 enums.AIReplyTurnStatus `gorm:"type:varchar(30);not null;default:'open';index"`
	TerminalReason         string                  `gorm:"type:varchar(80);not null;default:'';index"`
	FirstCustomerMessageID int64                   `gorm:"type:bigint;not null;default:0;index"`
	LastCustomerMessageID  int64                   `gorm:"type:bigint;not null;default:0;index"`
	FirstCustomerSentAt    time.Time               `gorm:"type:datetime;not null;index"`
	LastCustomerSentAt     time.Time               `gorm:"type:datetime;not null;index"`
	LastCommittedVersion   int                     `gorm:"type:int;not null;default:0;index"`
	LastDeliveredVersion   int                     `gorm:"type:int;not null;default:0;index"`
	LastCommittedRequestID string                  `gorm:"type:varchar(128);not null;default:'';index"`
	LastDeliveredRequestID string                  `gorm:"type:varchar(128);not null;default:'';index"`
	LastDeliveredAt        *time.Time              `gorm:"type:datetime;index"`
	ActiveJobID            int64                   `gorm:"type:bigint;not null;default:0;index"`
	LeaseOwner             string                  `gorm:"type:varchar(128);not null;default:'';index"`
	LeaseExpiresAt         *time.Time              `gorm:"type:datetime;index"`
	CompletedAt            *time.Time              `gorm:"type:datetime;index"`
	AuditFields
}

// AIReplyTurnTask persists one independently answerable item in a reply turn.
// It deliberately stores no customer text, prompt, retrieved context or model output.
type AIReplyTurnTask struct {
	ID                  int64                                `gorm:"primaryKey;autoIncrement"`
	TenantID            int64                                `gorm:"type:bigint;not null;default:0;index;uniqueIndex:uk_ai_reply_turn_task,priority:1"`
	ConversationID      int64                                `gorm:"type:bigint;not null;default:0;index"`
	SessionNo           int                                  `gorm:"type:int;not null;default:1;index"`
	TurnID              int64                                `gorm:"type:bigint;not null;default:0;index;uniqueIndex:uk_ai_reply_turn_task,priority:2"`
	IntroducedVersion   int                                  `gorm:"type:int;not null;default:1;index"`
	SourceMessageID     int64                                `gorm:"type:bigint;not null;default:0;index"`
	TaskKey             string                               `gorm:"type:varchar(128);not null;default:'';uniqueIndex:uk_ai_reply_turn_task,priority:3"`
	SequenceNo          int                                  `gorm:"type:int;not null;default:1;index"`
	TaskType            enums.AIReplyTurnTaskType            `gorm:"type:varchar(24);not null;default:'text';index"`
	Intent              string                               `gorm:"type:varchar(80);not null;default:'';index"`
	SubIntent           string                               `gorm:"type:varchar(120);not null;default:'';index"`
	ResourceAction      string                               `gorm:"type:varchar(80);not null;default:'';index"`
	QuestionFingerprint string                               `gorm:"type:varchar(64);not null;default:'';index"`
	RelationType        string                               `gorm:"type:varchar(24);not null;default:'';index"`
	RelatedTaskID       int64                                `gorm:"type:bigint;not null;default:0;index"`
	Stage               enums.AIReplyTurnTaskStage           `gorm:"type:varchar(24);not null;default:'intent';index"`
	Status              enums.AIReplyTurnTaskStatus          `gorm:"type:varchar(24);not null;default:'pending';index;index:idx_ai_reply_turn_task_due,priority:1"`
	KnowledgeStatus     enums.AIReplyTurnTaskKnowledgeStatus `gorm:"type:varchar(24);not null;default:'none';index"`
	ClaimedByJobID      int64                                `gorm:"type:bigint;not null;default:0;index"`
	ClaimedVersion      int                                  `gorm:"type:int;not null;default:0;index"`
	CoveredByTaskID     int64                                `gorm:"type:bigint;not null;default:0;index"`
	AttemptCount        int                                  `gorm:"type:int;not null;default:0"`
	KnowledgeHitCount   int                                  `gorm:"type:int;not null;default:0"`
	ResultCode          string                               `gorm:"type:varchar(80);not null;default:'';index"`
	CommittedMessageID  int64                                `gorm:"type:bigint;not null;default:0;index"`
	NextRetryAt         *time.Time                           `gorm:"type:datetime;index;index:idx_ai_reply_turn_task_due,priority:2"`
	CompletedAt         *time.Time                           `gorm:"type:datetime;index"`
	AuditFields
}

// AIReplyJob persists one recoverable AI reply trigger for a committed
// customer message. It stores references and controlled execution state only.
type AIReplyJob struct {
	ID                  int64                       `gorm:"primaryKey;autoIncrement"`
	TenantID            int64                       `gorm:"type:bigint;not null;default:0;index;uniqueIndex:uk_ai_reply_job_message,priority:1"`
	ConversationID      int64                       `gorm:"type:bigint;not null;default:0;index;uniqueIndex:uk_ai_reply_job_message,priority:2"`
	MessageID           int64                       `gorm:"type:bigint;not null;default:0;index;uniqueIndex:uk_ai_reply_job_message,priority:3"`
	SessionNo           int                         `gorm:"type:int;not null;default:1;index"`
	StoreID             int64                       `gorm:"type:bigint;not null;default:0;index"`
	StoreStaffBindingID int64                       `gorm:"type:bigint;not null;default:0;index"`
	TurnID              int64                       `gorm:"type:bigint;not null;default:0;index"`
	TurnVersion         int                         `gorm:"type:int;not null;default:0;index"`
	CoveredByMessageID  int64                       `gorm:"type:bigint;not null;default:0;index"`
	CoveredByTaskID     int64                       `gorm:"type:bigint;not null;default:0;index"`
	RequestID           string                      `gorm:"type:varchar(128);not null;default:'';index"`
	TriggerKind         enums.AIReplyJobTriggerKind `gorm:"type:varchar(20);not null;default:'text';index"`
	Status              enums.AIReplyJobStatus      `gorm:"type:varchar(30);not null;default:'pending';index"`
	AttemptCount        int                         `gorm:"type:int;not null;default:0"`
	NextRetryAt         *time.Time                  `gorm:"type:datetime;index"`
	ExpiresAt           time.Time                   `gorm:"type:datetime;not null;index"`
	LeaseOwner          string                      `gorm:"type:varchar(128);not null;default:'';index"`
	LeaseExpiresAt      *time.Time                  `gorm:"type:datetime;index"`
	ResultCode          string                      `gorm:"type:varchar(80);not null;default:'';index"`
	LastErrorClass      string                      `gorm:"type:varchar(80);not null;default:'';index"`
	StartedAt           *time.Time                  `gorm:"type:datetime;index"`
	CompletedAt         *time.Time                  `gorm:"type:datetime;index"`
	AuditFields
}

// AIManualResumeTask persists the AI continuation that must run after an
// unanswered manual handoff times out. It is deliberately separate from the
// conversation route state so retries never change route semantics.
type AIManualResumeTask struct {
	ID                     int64      `gorm:"primaryKey;autoIncrement"`
	TenantID               int64      `gorm:"type:bigint;not null;default:0;index"`
	TaskKey                string     `gorm:"type:varchar(128);not null;default:'';uniqueIndex"`
	HandoffToken           string     `gorm:"type:varchar(64);not null;default:'';index"`
	ConversationID         int64      `gorm:"type:bigint;not null;default:0;index"`
	WxWorkInstanceID       int64      `gorm:"type:bigint;not null;default:0;index"`
	OriginMessageID        int64      `gorm:"type:bigint;not null;default:0;index"`
	LatestWaitingMessageID int64      `gorm:"type:bigint;not null;default:0;index"`
	RouteStatus            string     `gorm:"type:varchar(40);not null;default:'';index"`
	TaskStatus             string     `gorm:"type:varchar(30);not null;default:'waiting';index"`
	ReadyAt                *time.Time `gorm:"type:datetime;index"`
	NextRetryAt            *time.Time `gorm:"type:datetime;index"`
	RetryCount             int        `gorm:"type:int;not null;default:0"`
	ReminderCount          int        `gorm:"type:int;not null;default:0"`
	LastReminderAt         *time.Time `gorm:"type:datetime;index"`
	NextReminderAt         *time.Time `gorm:"type:datetime;index"`
	NoticeSentAt           *time.Time `gorm:"type:datetime;index"`
	CompletedAt            *time.Time `gorm:"type:datetime;index"`
	LastError              string     `gorm:"type:text"`
	AuditFields
}

// ConversationSessionSummary 保存单个会话轮次的 AI 压缩摘要。
//
// 原始消息仍永久保留；摘要只服务于 AI 超长上下文压缩，不作为审计原文。
type ConversationSessionSummary struct {
	ID                  int64        `gorm:"primaryKey;autoIncrement"`
	TenantID            int64        `gorm:"type:bigint;not null;default:0;index"`
	ConversationID      int64        `gorm:"type:bigint;not null;default:0;index:idx_conversation_session_summary,unique"`
	SessionNo           int          `gorm:"type:int;not null;default:1;index:idx_conversation_session_summary,unique"`
	WxWorkInstanceID    int64        `gorm:"type:bigint;not null;default:0;index"`
	StoreID             int64        `gorm:"type:bigint;not null;default:0;index"`
	CustomerID          int64        `gorm:"type:bigint;not null;default:0;index"`
	StableFacts         string       `gorm:"type:text"`
	OpenIssues          string       `gorm:"type:text"`
	CustomerPreferences string       `gorm:"type:text"`
	MediaSummary        string       `gorm:"type:text"`
	MessageCount        int          `gorm:"type:int;not null;default:0"`
	TokenEstimate       int          `gorm:"type:int;not null;default:0"`
	LastMessageID       int64        `gorm:"type:bigint;not null;default:0;index"`
	Status              enums.Status `gorm:"type:int;not null;default:0;index"`
	AuditFields
}

// MessageSyncLog 记录外部渠道与 AgentDesk 之间消息同步是否成功及跳过原因。
type MessageSyncLog struct {
	ID             int64                      `gorm:"primaryKey;autoIncrement"`
	TenantID       int64                      `gorm:"type:bigint;not null;default:0;index"`
	ConversationID int64                      `gorm:"type:bigint;not null;default:0;index"`
	MessageID      int64                      `gorm:"type:bigint;not null;default:0;index"`
	Direction      enums.MessageSyncDirection `gorm:"type:varchar(40);not null;default:'';index"`
	Source         string                     `gorm:"type:varchar(60);not null;default:'';index"`
	Target         string                     `gorm:"type:varchar(60);not null;default:'';index"`
	ExternalMsgID  string                     `gorm:"type:varchar(128);not null;default:'';index"`
	SyncStatus     enums.MessageSyncStatus    `gorm:"type:varchar(30);not null;default:'pending';index"`
	RetryCount     int                        `gorm:"type:int;not null;default:0"`
	ErrorMessage   string                     `gorm:"type:text"`
	Payload        string                     `gorm:"type:text"`
	AuditFields
}

// ConversationParticipant 会话参与方。
type ConversationParticipant struct {
	ID                    int64        `gorm:"primaryKey;autoIncrement"`
	TenantID              int64        `gorm:"type:bigint;not null;default:0;index"`
	ConversationID        int64        `gorm:"type:bigint;not null;index;uniqueIndex:uk_conversation_participant"`
	ParticipantType       string       `gorm:"type:varchar(30);not null;default:'';index;uniqueIndex:uk_conversation_participant"`
	ParticipantID         int64        `gorm:"type:bigint;not null;default:0;uniqueIndex:uk_conversation_participant"`
	ExternalParticipantID string       `gorm:"type:varchar(128);not null;default:''"`
	JoinedAt              *time.Time   `gorm:"type:datetime"`
	LeftAt                *time.Time   `gorm:"type:datetime"`
	Status                enums.Status `gorm:"type:int;not null;default:0;index"`
	AuditFields
}

// ConversationReadState 会话读游标。
type ConversationReadState struct {
	ID                int64              `gorm:"primaryKey;autoIncrement"`
	TenantID          int64              `gorm:"type:bigint;not null;default:0;index"`
	ConversationID    int64              `gorm:"type:bigint;not null;index;uniqueIndex:uk_conversation_reader"`
	ReaderType        enums.IMSenderType `gorm:"type:varchar(30);not null;default:'';index;uniqueIndex:uk_conversation_reader"`
	ReaderID          int64              `gorm:"type:bigint;not null;default:0;uniqueIndex:uk_conversation_reader"`
	ExternalReaderID  string             `gorm:"type:varchar(128);not null;default:'';uniqueIndex:uk_conversation_reader"`
	LastReadMessageID int64              `gorm:"type:bigint;not null;default:0;index"`
	LastReadSeqNo     int64              `gorm:"type:bigint;not null;default:0;index"`
	LastReadAt        *time.Time         `gorm:"type:datetime"`
	AuditFields
}

// Message 会话消息。
type Message struct {
	ID                  int64                 `gorm:"primaryKey;autoIncrement"`
	TenantID            int64                 `gorm:"type:bigint;not null;default:0;index"`
	ConversationID      int64                 `gorm:"type:bigint;not null;index;uniqueIndex:uk_conversation_seq;uniqueIndex:uk_conversation_client_msg"`
	SessionNo           int                   `gorm:"type:int;not null;default:1;index"`
	HistoricalOnly      bool                  `gorm:"not null;default:false;index"`
	RequestID           string                `gorm:"type:varchar(128);not null;default:'';index"`
	ClientMsgID         string                `gorm:"type:varchar(128);not null;default:'';uniqueIndex:uk_conversation_client_msg"`
	SenderType          enums.IMSenderType    `gorm:"type:varchar(30);not null;default:'';index"`
	SenderID            int64                 `gorm:"type:bigint;not null;default:0;index"`
	ReceiverType        string                `gorm:"type:varchar(30);not null;default:'';index"`
	MessageType         enums.IMMessageType   `gorm:"type:varchar(30);not null;default:'';index"`
	Content             string                `gorm:"type:text"`
	Payload             string                `gorm:"type:text"`
	SeqNo               int64                 `gorm:"type:bigint;not null;default:0;uniqueIndex:uk_conversation_seq"`
	SendStatus          enums.IMMessageStatus `gorm:"type:int;not null;default:2;index"`
	OutboundChannelType string                `gorm:"type:varchar(30);not null;default:'';index"` // OutboundChannelType 持久化本条消息应投递的渠道；空值表示不应创建 Outbox。
	AIReplyTurnID       int64                 `gorm:"type:bigint;not null;default:0;index"`
	AIReplyTurnVersion  int                   `gorm:"type:int;not null;default:0;index"`
	SentAt              *time.Time            `gorm:"type:datetime;index"`
	DeliveredAt         *time.Time            `gorm:"type:datetime"`
	ReadAt              *time.Time            `gorm:"type:datetime"`
	RecalledAt          *time.Time            `gorm:"type:datetime"`
	QuotedMessageID     int64                 `gorm:"type:bigint;not null;default:0;index"`
	AuditFields
}

// WxWorkKFSyncState 企业微信客服消息同步状态表。
//
//	按 open_kfid 记录企业微信客服消息同步游标，用于 SyncMsg 增量拉取。
type WxWorkKFSyncState struct {
	ID         int64        `gorm:"primaryKey;autoIncrement"`                         // ID 为同步状态主键。
	TenantID   int64        `gorm:"type:bigint;not null;default:0;index"`             // TenantID 继承 openKfID 对应渠道所属接入公司。
	OpenKfID   string       `gorm:"type:varchar(64);not null;default:'';uniqueIndex"` // OpenKfID 为企业微信客服账号ID。
	NextCursor string       `gorm:"type:varchar(128);not null;default:''"`            // NextCursor 为下一次增量同步使用的游标。
	LastSyncAt *time.Time   `gorm:"type:datetime;index"`                              // LastSyncAt 为最近一次成功同步时间。
	Status     enums.Status `gorm:"type:int;not null;default:0;index"`                // Status 为同步状态记录状态。
	Remark     string       `gorm:"type:text"`                                        // Remark 为同步异常、人工备注等补充信息。
	AuditFields
}

// WxWorkKFConversation 企业微信客服渠道会话映射表。
//
//	维护平台会话与企业微信客服会话上下文的对应关系，供入站同步和下行发送复用。
type WxWorkKFConversation struct {
	ID             int64        `gorm:"primaryKey;autoIncrement"`                                                         // ID 为渠道会话映射主键。
	TenantID       int64        `gorm:"type:bigint;not null;default:0;index"`                                             // TenantID 继承平台会话所属接入公司。
	ConversationID int64        `gorm:"type:bigint;not null;uniqueIndex"`                                                 // ConversationID 为平台会话ID，一条平台会话仅对应一条当前有效渠道映射。
	ChannelID      int64        `gorm:"type:bigint;not null;default:0;index;index:idx_channel_ext"`                       // ChannelID 为所属接入渠道ID，用于标识该会话来自哪个企业微信渠道配置。
	OpenKfID       string       `gorm:"type:varchar(191);not null;default:'';index:idx_openkf_ext"`                       // OpenKfID 为企业微信客服账号ID。
	ExternalUserID string       `gorm:"type:varchar(128);not null;default:'';index:idx_openkf_ext;index:idx_channel_ext"` // ExternalUserID 为企业微信客户ID。
	ServicerUserID string       `gorm:"type:varchar(128);not null;default:'';index"`                                      // ServicerUserID 为企业微信当前接待客服成员UserID。
	SessionStatus  string       `gorm:"type:varchar(30);not null;default:'';index"`                                       // SessionStatus 为微信侧会话状态快照，如接入中、转接中、已结束。
	LastWxMsgID    string       `gorm:"type:varchar(64);not null;default:'';index"`                                       // LastWxMsgID 为最近一次同步到的微信消息ID。
	LastWxMsgTime  *time.Time   `gorm:"type:datetime;index"`                                                              // LastWxMsgTime 为最近一次微信消息时间。
	RawProfile     string       `gorm:"type:text"`                                                                        // RawProfile 为微信侧原始会话补充信息JSON。
	Status         enums.Status `gorm:"type:int;not null;default:0;index"`                                                // Status 为渠道会话映射状态。
	AuditFields
}

// WxWorkKFMessageRef 企业微信客服消息映射表。
//
//	用于实现微信消息幂等消费，并保存平台消息与微信消息的双向映射关系。
type WxWorkKFMessageRef struct {
	ID             int64        `gorm:"primaryKey;autoIncrement"`                          // ID 为消息映射主键。
	TenantID       int64        `gorm:"type:bigint;not null;default:0;index"`              // TenantID 继承平台会话所属接入公司。
	ConversationID int64        `gorm:"type:bigint;not null;default:0;index"`              // ConversationID 为所属平台会话ID。
	MessageID      int64        `gorm:"type:bigint;not null;default:0;index"`              // MessageID 为所属平台消息ID；仅渠道消息尚未生成平台消息时可暂为0。
	WxMsgID        string       `gorm:"type:varchar(191);not null;default:'';uniqueIndex"` // WxMsgID 为企业微信消息ID，用于幂等去重。
	Direction      string       `gorm:"type:varchar(20);not null;default:'';index"`        // Direction 为消息方向，如 in/out。
	Origin         int          `gorm:"type:int;not null;default:0;index"`                 // Origin 为企业微信消息来源值，如客户发送、系统事件、企微客户端发送。
	OpenKfID       string       `gorm:"type:varchar(64);not null;default:'';index"`        // OpenKfID 为发送或接收该消息的客服账号ID。
	ExternalUserID string       `gorm:"type:varchar(128);not null;default:'';index"`       // ExternalUserID 为消息对应的企业微信客户ID。
	SendStatus     string       `gorm:"type:varchar(30);not null;default:'';index"`        // SendStatus 为渠道发送状态快照，如 sent、failed。
	FailReason     string       `gorm:"type:text"`                                         // FailReason 为渠道发送失败原因或补偿说明。
	RawPayload     string       `gorm:"type:text"`                                         // RawPayload 为企业微信原始消息JSON。
	Status         enums.Status `gorm:"type:int;not null;default:0;index"`                 // Status 为消息映射状态。
	AuditFields
}

// ChannelMessageOutbox 外部渠道消息投递任务表。
//
//	用于记录平台消息提交后的渠道发送任务，保证第三方发送动作与主事务解耦。
type ChannelMessageOutbox struct {
	ID             int64      `gorm:"primaryKey;autoIncrement"`                                                  // ID 为投递任务主键。
	TenantID       int64      `gorm:"type:bigint;not null;default:0;index"`                                      // TenantID 继承平台会话所属接入公司。
	ChannelType    string     `gorm:"type:varchar(30);not null;default:'';index;uniqueIndex:uk_channel_message"` // ChannelType 为目标渠道类型，如 wxwork_kf。
	ConversationID int64      `gorm:"type:bigint;not null;default:0;index"`                                      // ConversationID 为所属平台会话ID。
	MessageID      int64      `gorm:"type:bigint;not null;default:0;uniqueIndex:uk_channel_message"`             // MessageID 为待投递的平台消息ID。
	Payload        string     `gorm:"type:text"`                                                                 // Payload 为渠道发送所需的标准化请求数据JSON。
	SendStatus     string     `gorm:"type:varchar(30);not null;default:'';index"`                                // SendStatus 为当前投递状态，如 pending、sending、sent、failed。
	RetryCount     int        `gorm:"type:int;not null;default:0"`                                               // RetryCount 为已重试次数。
	NextRetryAt    *time.Time `gorm:"type:datetime;index"`                                                       // NextRetryAt 为下一次允许重试时间。
	LastError      string     `gorm:"type:text"`                                                                 // LastError 为最近一次发送失败信息。
	SentAt         *time.Time `gorm:"type:datetime;index"`                                                       // SentAt 为最终发送成功时间。
	AuditFields
}

// ConversationAssignment 会话接待关系。
type ConversationAssignment struct {
	ID                 int64                       `gorm:"primaryKey;autoIncrement"`
	TenantID           int64                       `gorm:"type:bigint;not null;default:0;index"`
	ConversationID     int64                       `gorm:"type:bigint;not null;index"`
	SessionNo          int                         `gorm:"type:int;not null;default:1;index"`    // SessionNo 固定本次分配所属会话轮次，历史报表和质检不得按当前路由反推。
	SquadID            int64                       `gorm:"type:bigint;not null;default:0;index"` // SquadID 记录派发时采用的客服小组快照，0 表示全组或未指定。
	FromUserID         int64                       `gorm:"type:bigint;not null;default:0;index"`
	ToUserID           int64                       `gorm:"type:bigint;not null;default:0;index"`
	AssignType         string                      `gorm:"type:varchar(30);not null;default:'';index"`
	Reason             string                      `gorm:"type:varchar(255);not null;default:''"`
	DispatchMode       enums.AgentTeamDispatchMode `gorm:"type:varchar(30);not null;default:'';index"` // DispatchMode 记录本次人工、规则或历史智能派单方式。
	DecisionConfidence int                         `gorm:"type:int;not null;default:0"`                // DecisionConfidence 仅保留历史模型派单置信度，新派单固定为 0。
	WorkloadWeight     int                         `gorm:"type:int;not null;default:1"`                // WorkloadWeight 为本次派单使用的任务工作量权重快照。
	Status             enums.IMAssignmentStatus    `gorm:"type:int;not null;index"`
	CreatedAt          time.Time                   `gorm:"type:datetime;not null;index"`
	FinishedAt         *time.Time                  `gorm:"type:datetime"`
	OperatorID         int64                       `gorm:"type:bigint;not null;default:0;index"`
}

// QuickReply 快捷回复。
type QuickReply struct {
	ID        int64        `gorm:"primaryKey;autoIncrement"`
	TenantID  int64        `gorm:"type:bigint;not null;default:0;index"`
	GroupName string       `gorm:"type:varchar(50);not null;default:'';index"`
	Title     string       `gorm:"type:varchar(100);not null;default:'';index"`
	Content   string       `gorm:"type:text"`
	Status    enums.Status `gorm:"type:int;not null;index"`
	SortNo    int          `gorm:"type:int;not null;index"`
	AuditFields
}

// AIAgent is the internal runtime strategy identity shared by channels and
// conversations. It is not a tenant-facing model connection or model selector.
type AIAgent struct {
	ID                  int64                           `gorm:"primaryKey;autoIncrement"`                    // ID 为运行策略主键。
	TenantID            int64                           `gorm:"type:bigint;not null;default:0;index"`        // TenantID 为运行策略所属接入公司。
	Name                string                          `gorm:"type:varchar(100);not null;default:'';index"` // Name 为内部接待策略名称。
	Description         string                          `gorm:"type:varchar(255);not null;default:''"`       // Description 为内部接待策略描述。
	Status              enums.Status                    `gorm:"type:int;not null;index"`                     // Status 为运行策略状态。
	ServiceMode         enums.IMConversationServiceMode `gorm:"type:int;not null;default:3;index"`           // ServiceMode 为服务模式，如仅AI、仅人工、AI优先人工接管。
	SystemPrompt        string                          `gorm:"type:text"`                                   // SystemPrompt 为该 Agent 的系统提示词。
	WelcomeMessage      string                          `gorm:"type:text"`                                   // WelcomeMessage 为该 Agent 的欢迎语或首响模板。
	ReplyTimeoutSeconds int                             `gorm:"type:int;not null;default:180"`               // ReplyTimeoutSeconds 为异步自动回复超时秒数。
	TeamIDs             string                          `gorm:"type:varchar(500);not null;default:''"`       // TeamIDs 为转人工时可路由的客服组ID列表，多个之间使用逗号分隔。
	HandoffMode         enums.AIAgentHandoffMode        `gorm:"type:int;not null;default:1"`                 // HandoffMode 为转人工模式，如进入待接入池、进入默认客服组待接入池。
	FallbackMode        enums.AIAgentFallbackMode       `gorm:"type:int;not null;default:1"`                 // FallbackMode 为知识库未命中时的兜底策略。
	FallbackMessage     string                          `gorm:"type:text"`                                   // FallbackMessage 为兜底回复文案。
	KnowledgeIDs        string                          `gorm:"type:varchar(500);not null;default:''"`       // KnowledgeIDs 为绑定的知识库ID列表，按顺序表示优先级。
	SkillIDs            string                          `gorm:"type:varchar(500);not null;default:''"`       // SkillIDs 为绑定的技能ID列表，按顺序表示允许路由的范围。
	AllowedMCPTools     string                          `gorm:"type:text"`                                   // AllowedMCPTools 为允许 direct tool 路由的 MCP 工具白名单配置JSON。
	AllowedGraphTools   string                          `gorm:"type:text"`                                   // AllowedGraphTools 为允许 Graph Tool 的白名单配置JSON。
	SortNo              int                             `gorm:"type:int;not null;default:0;index"`           // SortNo 为后台展示排序号。
	AuditFields
}

// Channel 接入渠道配置。
//
//	用于统一描述系统的外部接入入口。不同渠道类型共享统一的接入配置骨架，
//	例如网页客服渠道（web）和企业微信客服渠道（wxwork_kf）。
//	渠道本身负责定义“入口如何识别、默认接入哪个 AI Agent、渠道专属配置是什么”，
//	而具体消息收发、会话映射等运行时数据由各自的渠道业务表承载。
type Channel struct {
	ID          int64  `gorm:"primaryKey;autoIncrement"`                         // ID 为渠道主键。
	TenantID    int64  `gorm:"type:bigint;not null;default:0;index"`             // TenantID 为接入渠道所属公司。
	Name        string `gorm:"type:varchar(100);not null;default:'';index"`      // Name 为渠道名称，用于后台展示和业务识别，例如“官网客服”“企业微信主客服”。
	ChannelType string `gorm:"type:varchar(30);not null;default:'';index"`       // ChannelType 为渠道类型，决定该渠道的接入方式和配置解释规则。当前规划的典型取值包括：web、wxwork_kf。
	ChannelID   string `gorm:"type:varchar(64);not null;default:'';uniqueIndex"` // ChannelID 为渠道入口标识，由系统自动生成。对 web 渠道，该字段用于前端通过 X-Channel-Id 标识接入来源；对其他渠道，作为统一的系统内稳定渠道标识保留。
	AIAgentID   int64  `gorm:"type:bigint;not null;default:0;index"`             // AIAgentID 为该渠道默认接入的 AI Agent。 当外部客户通过该渠道首次进入系统且尚未命中现有未结束会话时，系统会使用该 AI Agent 作为会话默认接待实例。
	// ConfigJSON 为渠道专属扩展配置，使用 JSON 存储。
	// 例如：
	// 1. web 渠道可记录允许域名、品牌配置等；
	// 2. wxwork_kf 渠道可记录 openKfId、欢迎语策略等。
	// 该字段只存储渠道类型私有配置，不承载通用主字段。
	ConfigJSON string       `gorm:"type:text"`
	Status     enums.Status `gorm:"type:int;not null;default:0;index"` // Status 为渠道状态。禁用后，该渠道不再允许新会话接入；删除时采用软删除状态保留历史关联数据。
	Remark     string       `gorm:"type:text"`                         // Remark 为渠道备注，用于记录接入说明、维护说明和内部运维信息。
	AuditFields
}

// ConversationEventLog 会话事件日志。
type ConversationEventLog struct {
	ID             int64              `gorm:"primaryKey;autoIncrement"`
	TenantID       int64              `gorm:"type:bigint;not null;default:0;index"`
	ConversationID int64              `gorm:"type:bigint;not null;index"`
	RequestID      string             `gorm:"type:varchar(128);not null;default:'';index"`
	EventType      enums.IMEventType  `gorm:"type:varchar(50);not null;default:'';index"`
	OperatorType   enums.IMSenderType `gorm:"type:varchar(30);not null;default:'';index"`
	OperatorID     int64              `gorm:"type:bigint;not null;default:0;index"`
	Content        string             `gorm:"type:text"`
	Payload        string             `gorm:"type:text"`
	CreatedAt      time.Time          `gorm:"type:datetime;not null;index"`
}

// Ticket 客服问题记录。
type Ticket struct {
	ID                int64              `gorm:"primaryKey;autoIncrement"`
	TenantID          int64              `gorm:"type:bigint;not null;default:0;index"`
	TicketNo          string             `gorm:"type:varchar(64);not null;default:'';uniqueIndex"`
	Title             string             `gorm:"type:varchar(255);not null;default:'';index"`
	Description       string             `gorm:"type:text"`
	Category          string             `gorm:"type:varchar(50);not null;default:'';index"`
	Priority          string             `gorm:"type:varchar(30);not null;default:'normal';index"`
	RoomNo            string             `gorm:"type:varchar(50);not null;default:'';index"`
	Source            enums.TicketSource `gorm:"type:varchar(50);not null;default:'';index"`
	Channel           string             `gorm:"type:varchar(50);not null;default:'';index"`
	CustomerID        int64              `gorm:"type:bigint;not null;default:0;index"`
	ConversationID    int64              `gorm:"type:bigint;not null;default:0;index"`
	Status            enums.TicketStatus `gorm:"type:varchar(50);not null;default:'pending';index"`
	CurrentAssigneeID int64              `gorm:"type:bigint;not null;default:0;index"`
	HandledAt         *time.Time         `gorm:"type:datetime;index"`
	AuditFields
}

// TicketTag 工单标签关联。
type TicketTag struct {
	ID       int64 `gorm:"primaryKey;autoIncrement"`
	TenantID int64 `gorm:"type:bigint;not null;default:0;index"`
	TicketID int64 `gorm:"type:bigint;not null;index;uniqueIndex:uk_ticket_tag"`
	TagID    int64 `gorm:"type:bigint;not null;index;uniqueIndex:uk_ticket_tag"`
	AuditFields
}

// TicketProgress 工单处理进展。
type TicketProgress struct {
	ID        int64     `gorm:"primaryKey;autoIncrement"`
	TenantID  int64     `gorm:"type:bigint;not null;default:0;index"`
	TicketID  int64     `gorm:"type:bigint;not null;index"`
	Content   string    `gorm:"type:text"`
	AuthorID  int64     `gorm:"type:bigint;not null;default:0;index"`
	CreatedAt time.Time `gorm:"type:datetime;not null;index"`
}

// AgentProfile 客服档案。
type AgentProfile struct {
	ID                     int64        `gorm:"primaryKey;autoIncrement"`                                                                 // ID 为客服档案主键。
	TenantID               int64        `gorm:"type:bigint;not null;default:0;index;uniqueIndex:uk_agent_profile_tenant_code,priority:1"` // TenantID 为客服档案所属接入公司，从客服组和账号归属确定。
	UserID                 int64        `gorm:"type:bigint;not null;uniqueIndex"`                                                         // UserID 关联后台用户，一名用户只允许一份客服档案。
	TeamID                 int64        `gorm:"type:bigint;not null;default:0;index"`                                                     // TeamID 为客服所属客服组。
	StoreScopeIDs          string       `gorm:"type:varchar(500);not null;default:''"`                                                    // StoreScopeIDs 为客服可服务的门店ID，逗号分隔；为空时继承客服组范围。
	WxWorkInstanceScopeIDs string       `gorm:"type:varchar(500);not null;default:''"`                                                    // WxWorkInstanceScopeIDs 为客服可服务的企微员工号实例ID，逗号分隔；为空时继承客服组范围。
	AgentCode              string       `gorm:"type:varchar(64);not null;default:'';uniqueIndex:uk_agent_profile_tenant_code,priority:2"` // AgentCode 为租户内唯一客服工号，用于业务侧识别客服。
	DisplayName            string       `gorm:"type:varchar(100);not null;default:'';index"`                                              // DisplayName 为客服展示名，可区别于后台昵称。
	Avatar                 string       `gorm:"type:varchar(1024);not null;default:''"`                                                   // Avatar 为客服头像 URL。
	MaxConcurrentCount     int          `gorm:"type:int;not null;default:0"`                                                              // MaxConcurrentCount 表示客服最大并发接待数。
	PriorityLevel          int          `gorm:"type:int;not null;default:0;index"`                                                        // PriorityLevel 表示自动分配优先级，值越大越优先。
	AutoAssignEnabled      bool         `gorm:"not null;default:true;index"`                                                              // AutoAssignEnabled 表示是否参与自动分配。
	LastOnlineAt           *time.Time   `gorm:"type:datetime;index"`                                                                      // LastOnlineAt 记录最近一次在线时间。
	Status                 enums.Status `gorm:"type:int;not null;default:0;index"`                                                        // Status 表示客服档案状态
	Remark                 string       `gorm:"type:text"`                                                                                // Remark 记录客服备注信息。
	AuditFields
}

// AgentTeam 客服组。
type AgentTeam struct {
	ID                     int64                       `gorm:"primaryKey;autoIncrement"`                       // ID 为客服组主键。
	TenantID               int64                       `gorm:"type:bigint;not null;default:0;index"`           // TenantID 为客服组所属接入公司。
	Name                   string                      `gorm:"type:varchar(100);not null;default:'';index"`    // Name 为客服组名称。
	IsDefault              bool                        `gorm:"not null;default:false;index"`                   // IsDefault 表示该组是否为租户创建时生成的默认综合客服组。
	LeaderUserID           int64                       `gorm:"type:bigint;not null;default:0;index"`           // LeaderUserID 为组长用户ID，0 表示暂未设置。
	StoreScopeIDs          string                      `gorm:"type:varchar(500);not null;default:''"`          // StoreScopeIDs 为客服组可服务的门店ID，逗号分隔；为空表示不限制。
	WxWorkInstanceScopeIDs string                      `gorm:"type:varchar(500);not null;default:''"`          // WxWorkInstanceScopeIDs 为客服组可服务的企微员工号实例ID，逗号分隔；为空表示不限制。
	DispatchMode           enums.AgentTeamDispatchMode `gorm:"type:varchar(30);not null;default:'rule';index"` // DispatchMode 为客服组自动派单策略，默认保持规则均衡。
	Status                 enums.Status                `gorm:"type:int;not null;default:0;index"`              // Status 表示客服组状态
	Description            string                      `gorm:"type:varchar(255);not null;default:''"`          // Description 为客服组简介，用于说明职责边界。
	Remark                 string                      `gorm:"type:text"`                                      // Remark 记录客服组内部备注。
	AuditFields
}

// AgentTeamSquad 是综合客服组内用于排班和调度的客服小组，不独立承接会话。
type AgentTeamSquad struct {
	ID           int64        `gorm:"primaryKey;autoIncrement"`
	TenantID     int64        `gorm:"type:bigint;not null;default:0;index"`
	TeamID       int64        `gorm:"type:bigint;not null;index"`
	Name         string       `gorm:"type:varchar(100);not null;default:'';index"`
	LeaderUserID int64        `gorm:"type:bigint;not null;default:0;index"`
	Status       enums.Status `gorm:"type:int;not null;default:0;index"`
	Remark       string       `gorm:"type:text"`
	AuditFields
}

// AgentTeamSquadMember 记录客服档案与客服小组的多对多关系。
type AgentTeamSquadMember struct {
	ID             int64        `gorm:"primaryKey;autoIncrement"`
	TenantID       int64        `gorm:"type:bigint;not null;default:0;index"`
	SquadID        int64        `gorm:"type:bigint;not null;index;uniqueIndex:uk_agent_team_squad_member"`
	AgentProfileID int64        `gorm:"type:bigint;not null;index;uniqueIndex:uk_agent_team_squad_member"`
	Status         enums.Status `gorm:"type:int;not null;default:0;index"`
	AuditFields
}

// AgentTeamSchedule 客服组排班。
type AgentTeamSchedule struct {
	ID                      int64        `gorm:"primaryKey;autoIncrement"`               // ID 为组排班主键。
	TenantID                int64        `gorm:"type:bigint;not null;default:0;index"`   // TenantID 为排班所属接入公司，从客服组继承。
	TeamID                  int64        `gorm:"type:bigint;not null;index"`             // TeamID 为被排班的客服组ID。
	SquadID                 int64        `gorm:"type:bigint;not null;default:0;index"`   // SquadID 为值班客服小组ID，0 表示全组值班。
	IncludedAgentProfileIDs string       `gorm:"type:varchar(1000);not null;default:''"` // IncludedAgentProfileIDs 为本班临时加入的客服档案ID，逗号分隔。
	ExcludedAgentProfileIDs string       `gorm:"type:varchar(1000);not null;default:''"` // ExcludedAgentProfileIDs 为本班请假或临时移出的客服档案ID，逗号分隔。
	StartAt                 time.Time    `gorm:"type:datetime;not null;index"`           // StartAt 为班次开始时间，可与 EndAt 跨自然日。
	EndAt                   time.Time    `gorm:"type:datetime;not null;index"`           // EndAt 为班次结束时间，最长不超过24小时。
	Remark                  string       `gorm:"type:varchar(255);not null;default:''"`  // Remark 记录排班备注。
	Status                  enums.Status `gorm:"type:int;not null;default:0;index"`      // Status 表示组排班记录状态。
	AuditFields
}

// KnowledgeBase 知识库主表。
type KnowledgeBase struct {
	ID                                int64        `gorm:"primaryKey;autoIncrement"`                                // ID 为知识库主键。
	TenantID                          int64        `gorm:"type:bigint;not null;default:0;index"`                    // TenantID 为知识库所属接入公司。
	StoreID                           int64        `gorm:"type:bigint;not null;default:0;index"`                    // StoreID 为知识库所属内部稳定门店身份。
	DatasetID                         string       `gorm:"type:varchar(128);not null;default:'';index"`             // DatasetID 为 FastGPT 数据集 ID。
	DatasetName                       string       `gorm:"type:varchar(200);not null;default:''"`                   // DatasetName 为 FastGPT 数据集名称。
	ConnectionID                      string       `gorm:"type:varchar(64);not null;default:'platform'"`            // ConnectionID 为平台 FastGPT 连接标识。
	FastGPTProfileID                  string       `gorm:"type:varchar(128);not null;default:'';index"`             // FastGPTProfileID 为 FastGPT Dataset Model Profile 的非敏感标识。
	FastGPTProfileName                string       `gorm:"type:varchar(200);not null;default:''"`                   // FastGPTProfileName 仅供门店侧展示。
	FastGPTProfileRevision            string       `gorm:"type:varchar(80);not null;default:''"`                    // FastGPTProfileRevision 为 FastGPT 侧配置版本。
	FastGPTProfileFingerprint         string       `gorm:"type:varchar(128);not null;default:''"`                   // FastGPTProfileFingerprint 用于判断配置是否变更，不保存密钥。
	FastGPTProfileStatus              string       `gorm:"type:varchar(30);not null;default:'pending';index"`       // FastGPTProfileStatus 为 pending/ready/failed 等同步状态。
	FastGPTProfileSyncedAt            *time.Time   `gorm:"type:datetime;index"`                                     // FastGPTProfileSyncedAt 为最后一次成功同步时间。
	FastGPTAppliedProfileID           int64        `gorm:"type:bigint;not null;default:0;index"`                    // FastGPTAppliedProfileID 为该 Dataset 已实际应用的平台模型方案。
	FastGPTAppliedProfileRevision     int64        `gorm:"type:bigint;not null;default:0;index"`                    // FastGPTAppliedProfileRevision 为该 Dataset 已实际应用的平台方案 revision。
	FastGPTAppliedStoreStaffBindingID int64        `gorm:"type:bigint;not null;default:0;index"`                    // FastGPTAppliedStoreStaffBindingID 为该 Dataset 模型调用归属的门店员工号绑定。
	FastGPTAppliedCredentialRevision  int64        `gorm:"type:bigint;not null;default:0;index"`                    // FastGPTAppliedCredentialRevision 为该 Dataset 已实际应用的门店凭据 revision。
	Name                              string       `gorm:"type:varchar(100);not null;default:'';index"`             // Name 为知识库名称。
	Description                       string       `gorm:"type:text"`                                               // Description 为知识库描述。
	KnowledgeType                     string       `gorm:"type:varchar(20);not null;default:'fastgpt_cloud';index"` // KnowledgeType 固定为托管 FastGPT。
	Status                            enums.Status `gorm:"type:int;not null;index"`                                 // Status 为状态
	DefaultTopK                       int          `gorm:"type:int;not null;default:10"`                            // DefaultTopK 为默认召回数量。
	DefaultScoreThreshold             float64      `gorm:"type:decimal(5,4);not null;default:0.5"`                  // DefaultScoreThreshold 为默认相似度阈值。
	DefaultRerankLimit                int          `gorm:"type:int;not null;default:5"`                             // DefaultRerankLimit 为默认重排后保留数量。
	AnswerMode                        int          `gorm:"type:int;not null;default:1"`                             // AnswerMode 为回答模式：1严格知识库模式 2辅助解释模式。
	SortNo                            int          `gorm:"type:int;not null;default:0;index"`                       // SortNo 为排序号，用于后台展示和知识库的人工排序管理。
	Remark                            string       `gorm:"type:text"`                                               // Remark 为备注。
	AuditFields
}

// KnowledgeResourceGroup maps one stable cloud knowledge source record to reusable Agent Desk assets.
// Resources stay outside the language-model context and are sent only after a matching knowledge hit.
type KnowledgeResourceGroup struct {
	ID              int64        `gorm:"primaryKey;autoIncrement"`
	TenantID        int64        `gorm:"type:bigint;not null;default:0;index;uniqueIndex:uk_knowledge_resource_store_source,priority:1"`
	StoreID         int64        `gorm:"type:bigint;not null;default:0;index;uniqueIndex:uk_knowledge_resource_store_source,priority:2"`
	KnowledgeBaseID int64        `gorm:"type:bigint;not null;default:0;index;uniqueIndex:uk_knowledge_resource_store_source,priority:3"`
	SourceProvider  string       `gorm:"type:varchar(40);not null;default:'fastgpt_cloud';index;uniqueIndex:uk_knowledge_resource_store_source,priority:4"`
	SourceRecordID  string       `gorm:"type:varchar(255);not null;default:'';index;uniqueIndex:uk_knowledge_resource_store_source,priority:5"`
	Title           string       `gorm:"type:varchar(255);not null;default:''"`
	Description     string       `gorm:"type:text"`
	SourceHash      string       `gorm:"type:varchar(64);not null;default:'';index"`
	Status          enums.Status `gorm:"type:int;not null;default:0;index"`
	SortNo          int          `gorm:"type:int;not null;default:0;index"`
	Remark          string       `gorm:"type:text"`
	AuditFields
}

// KnowledgeResourceItem is one ordered, reusable image asset in a knowledge resource group.
type KnowledgeResourceItem struct {
	ID                       int64        `gorm:"primaryKey;autoIncrement"`
	TenantID                 int64        `gorm:"type:bigint;not null;default:0;index"`
	KnowledgeResourceGroupID int64        `gorm:"type:bigint;not null;default:0;index"`
	AssetID                  string       `gorm:"type:varchar(64);not null;default:'';index"`
	SourceURL                string       `gorm:"type:text"`
	SourceChecksum           string       `gorm:"type:varchar(64);not null;default:'';index"`
	Title                    string       `gorm:"type:varchar(255);not null;default:''"`
	Description              string       `gorm:"type:text"`
	SortNo                   int          `gorm:"type:int;not null;default:0;index"`
	Status                   enums.Status `gorm:"type:int;not null;default:0;index"`
	Remark                   string       `gorm:"type:text"`
	AuditFields
}

// KnowledgeCandidate 是从人工解决和 AI 未解答会话中提取的待审核知识条目。
type KnowledgeCandidate struct {
	ID              int64                          `gorm:"primaryKey;autoIncrement"`
	TenantID        int64                          `gorm:"type:bigint;not null;default:0;index"`
	StoreID         int64                          `gorm:"type:bigint;not null;default:0;index"`
	KnowledgeBaseID int64                          `gorm:"type:bigint;not null;default:0;index"`
	ConversationID  int64                          `gorm:"type:bigint;not null;default:0;index"`
	MessageIDs      string                         `gorm:"type:text"`
	Source          enums.KnowledgeCandidateSource `gorm:"type:varchar(40);not null;default:'';index"`
	Question        string                         `gorm:"type:text"`
	Answer          string                         `gorm:"type:text"`
	Summary         string                         `gorm:"type:text"`
	EvidenceText    string                         `gorm:"type:text"`
	Frequency       int                            `gorm:"type:int;not null;default:1;index"`
	SimilarityKey   string                         `gorm:"type:varchar(128);not null;default:'';index:idx_kc_similarity"`
	Status          enums.KnowledgeCandidateStatus `gorm:"type:varchar(30);not null;default:'pending';index"`
	Confidence      float64                        `gorm:"type:decimal(5,4);not null;default:0"`
	CreatedBy       string                         `gorm:"type:varchar(100);not null;default:''"`
	ReviewUserID    int64                          `gorm:"type:bigint;not null;default:0;index"`
	ReviewUserName  string                         `gorm:"type:varchar(100);not null;default:''"`
	ReviewedAt      *time.Time                     `gorm:"type:datetime;index"`
	ExportedAt      *time.Time                     `gorm:"type:datetime;index"`
	ImportedAt      *time.Time                     `gorm:"type:datetime;index"`
	AuditFields
}

// KnowledgeRetrieveLog 检索日志表。
type KnowledgeRetrieveLog struct {
	ID               int64     `gorm:"primaryKey;autoIncrement"`                          // ID 为日志主键。
	TenantID         int64     `gorm:"type:bigint;not null;default:0;index"`              // TenantID 为本次检索所属接入公司。
	KnowledgeBaseID  int64     `gorm:"type:bigint;not null;index"`                        // KnowledgeBaseID 为知识库ID。
	SourceType       string    `gorm:"type:varchar(30);not null;default:'fastgpt';index"` // SourceType 为检索来源；新记录固定为 fastgpt，历史值只读保留。
	Channel          string    `gorm:"type:varchar(30);not null;default:'';index"`        // Channel 为渠道：im会话, agent_assist坐席辅助, api开放接口, debug调试。
	Scene            string    `gorm:"type:varchar(50);not null;default:'';index"`        // Scene 为场景：first_response首响, assist辅助, qa问答。
	SessionID        string    `gorm:"type:varchar(64);not null;default:'';index"`        // SessionID 为会话ID。
	ConversationID   int64     `gorm:"type:bigint;not null;default:0;index"`              // ConversationID 为会话ID。
	RequestID        string    `gorm:"type:varchar(64);not null;default:'';index"`        // RequestID 为请求ID。
	Question         string    `gorm:"type:text"`                                         // Question 为原始问题。
	RewriteQuestion  string    `gorm:"type:text"`                                         // RewriteQuestion 为改写后问题。
	Answer           string    `gorm:"type:text"`                                         // Answer 为生成的答案。
	AnswerStatus     int       `gorm:"type:int;not null;default:1;index"`                 // AnswerStatus 为答案状态：1正常 2无答案 3兜底 4风控拦截。
	HitCount         int       `gorm:"type:int;not null;default:0"`                       // HitCount 为命中数量。
	TopScore         float64   `gorm:"type:decimal(5,4);not null;default:0"`              // TopScore 为最高相似度分数。
	ChunkProvider    string    `gorm:"type:varchar(30);not null;default:'';index"`        // ChunkProvider 为检索提供方；新记录固定为托管 FastGPT。
	RerankEnabled    bool      `gorm:"not null;default:false;index"`                      // RerankEnabled 是否启用 rerank。
	RerankLimit      int       `gorm:"type:int;not null;default:0"`                       // RerankLimit 为 rerank 条数。
	CitationCount    int       `gorm:"type:int;not null;default:0"`                       // CitationCount 为最终引用条数。
	UsedChunkCount   int       `gorm:"type:int;not null;default:0"`                       // UsedChunkCount 为进入上下文的 chunk 数。
	LatencyMs        int64     `gorm:"type:bigint;not null;default:0"`                    // LatencyMs 为总耗时毫秒。
	RetrieveMs       int64     `gorm:"type:bigint;not null;default:0"`                    // RetrieveMs 为检索耗时毫秒。
	GenerateMs       int64     `gorm:"type:bigint;not null;default:0"`                    // GenerateMs 为生成耗时毫秒。
	PromptTokens     int       `gorm:"type:int;not null;default:0"`                       // PromptTokens 为prompt token数。
	CompletionTokens int       `gorm:"type:int;not null;default:0"`                       // CompletionTokens 为completion token数。
	ModelName        string    `gorm:"type:varchar(100);not null;default:''"`             // ModelName 为使用的模型名称。
	TraceData        string    `gorm:"type:text"`                                         // TraceData 为链路追踪数据JSON。
	CreatedAt        time.Time `gorm:"type:datetime;not null;index"`
}

// KnowledgeRetrieveHit 检索命中详情表。
type KnowledgeRetrieveHit struct {
	ID              int64     `gorm:"primaryKey;autoIncrement"`                    // ID 为命中记录主键。
	TenantID        int64     `gorm:"type:bigint;not null;default:0;index"`        // TenantID 继承所属检索日志。
	RetrieveLogID   int64     `gorm:"type:bigint;not null;index"`                  // RetrieveLogID 为检索日志ID。
	KnowledgeBaseID int64     `gorm:"type:bigint;not null;default:0;index"`        // KnowledgeBaseID 为命中来源知识库ID。
	SourceRecordID  string    `gorm:"type:varchar(255);not null;default:'';index"` // SourceRecordID 为 FastGPT 命中记录的稳定标识。
	DocumentTitle   string    `gorm:"type:varchar(255);not null;default:''"`       // DocumentTitle 为文档标题。
	Title           string    `gorm:"type:varchar(255);not null;default:''"`       // Title 为切片标题。
	SectionPath     string    `gorm:"type:text"`                                   // SectionPath 为章节路径。
	Provider        string    `gorm:"type:varchar(30);not null;default:''"`        // Provider 为分块 provider。
	RankNo          int       `gorm:"type:int;not null;default:0"`                 // RankNo 为排名。
	Score           float64   `gorm:"type:decimal(5,4);not null;default:0"`        // Score 为相似度分数。
	RerankScore     float64   `gorm:"type:decimal(5,4);not null;default:0"`        // RerankScore 为重排分数。
	UsedInAnswer    bool      `gorm:"not null;default:false"`                      // UsedInAnswer 是否用于生成答案。
	IsCitation      bool      `gorm:"not null;default:false"`                      // IsCitation 是否作为引用返回。
	Snippet         string    `gorm:"type:text"`                                   // Snippet 为内容片段。
	CreatedAt       time.Time `gorm:"type:datetime;not null;index"`
}

// KnowledgeFeedback 问答反馈表。
type KnowledgeFeedback struct {
	ID             int64     `gorm:"primaryKey;autoIncrement"`              // ID 为反馈主键。
	TenantID       int64     `gorm:"type:bigint;not null;default:0;index"`  // TenantID 继承所属检索日志。
	RetrieveLogID  int64     `gorm:"type:bigint;not null;index"`            // RetrieveLogID 为检索日志ID。
	FeedbackType   int       `gorm:"type:int;not null;default:1;index"`     // FeedbackType 为反馈类型：1点赞 2点踩 3无帮助 4引用错误 5其他。
	FeedbackReason string    `gorm:"type:varchar(500);not null;default:''"` // FeedbackReason 为反馈原因。
	UserID         int64     `gorm:"type:bigint;not null;default:0;index"`  // UserID 为用户ID。
	AgentID        int64     `gorm:"type:bigint;not null;default:0;index"`  // AgentID 为坐席ID。
	Remark         string    `gorm:"type:text"`                             // Remark 为备注。
	CreatedAt      time.Time `gorm:"type:datetime;not null;index"`
}

// SkillDefinition 表示可由后台配置并参与运行时路由的 Skill 定义。
type SkillDefinition struct {
	ID            int64        `gorm:"primaryKey;autoIncrement"`                          // ID 为 Skill 主键。
	Code          string       `gorm:"type:varchar(100);not null;default:'';uniqueIndex"` // Code 为 Skill 的稳定唯一编码，供程序内部引用和路由判断使用，例如 refund_skill。
	Name          string       `gorm:"type:varchar(100);not null;default:'';index"`       // Name 为 Skill 的展示名称，用于后台列表、配置页和人工选择场景。
	Description   string       `gorm:"type:varchar(255);not null;default:''"`             // Description 为 Skill 的简要说明，用于描述该 Skill 的适用场景和职责边界。
	Instruction   string       `gorm:"type:longtext"`                                     // Instruction 为 Skill 的主体说明文档存储字段，使用 Markdown 编写，供 Agent 理解任务目标、步骤和工具使用要求。
	Examples      string       `gorm:"type:text"`                                         // Examples 为示例问法 JSON 数组字符串。
	ToolWhitelist string       `gorm:"type:text"`                                         // ToolWhitelist 为允许使用的工具编码 JSON 数组字符串。
	Status        enums.Status `gorm:"type:int;not null;default:0;index"`                 // Status 为 Skill 当前状态，使用全局通用状态：0启用 1禁用 2删除。
	Remark        string       `gorm:"type:text"`                                         // Remark 为后台备注，用于记录配置说明、维护信息或内部协作信息。
	AuditFields
}

// SkillRunLog 表示一次 Skill 运行过程的审计日志。
type SkillRunLog struct {
	ID                int64            `gorm:"primaryKey;autoIncrement"`                    // ID 为 Skill 运行日志主键。
	TenantID          int64            `gorm:"type:bigint;not null;default:0;index"`        // TenantID 继承会话或运行时 AI Agent 的租户。
	ConversationID    int64            `gorm:"type:bigint;not null;default:0;index"`        // ConversationID 为关联会话ID，无会话上下文时为0。
	AIAgentID         int64            `gorm:"type:bigint;not null;default:0;index"`        // AIAgentID 为本次运行所属的 AI Agent ID。
	SkillDefinitionID int64            `gorm:"type:bigint;not null;default:0;index"`        // SkillDefinitionID 为最终命中的 Skill 定义ID，未命中时为0。
	SkillCode         string           `gorm:"type:varchar(100);not null;default:'';index"` // SkillCode 为最终命中的 Skill 编码，未命中时为空。
	ManualSkillCode   string           `gorm:"type:varchar(100);not null;default:'';index"` // ManualSkillCode 为本次请求显式指定的 Skill 编码。
	IntentCode        string           `gorm:"type:varchar(100);not null;default:'';index"` // IntentCode 为上游传入的意图编码。
	UserMessage       string           `gorm:"type:longtext"`                               // UserMessage 为本次请求的用户输入内容。
	Matched           bool             `gorm:"not null;default:false;index"`                // Matched 表示本次请求是否命中了 Skill。
	MatchReason       string           `gorm:"type:varchar(500);not null;default:''"`       // MatchReason 为命中或未命中的原因说明。
	FinalSelected     bool             `gorm:"not null;default:false;index"`                // FinalSelected 表示该日志记录的 Skill 是否为最终选中的执行 Skill。
	UsedModel         string           `gorm:"type:varchar(100);not null;default:''"`       // UsedModel 为本次实际调用的模型名称。
	UsedProvider      enums.AIProvider `gorm:"type:varchar(50);not null;default:''"`        // UsedProvider 为本次实际调用的模型供应商。
	ErrorMessage      string           `gorm:"type:text"`                                   // ErrorMessage 为运行过程中的错误信息。
	TraceData         string           `gorm:"type:text"`                                   // TraceData 为 Skill 执行链路追踪数据JSON。
	CreatedAt         time.Time        `gorm:"type:datetime;not null;index"`                // CreatedAt 为运行日志创建时间。
}

// AgentRunLog 表示一次客服 Agent 自动运行的总链路日志。
type AgentRunLog struct {
	ID               int64     `gorm:"primaryKey;autoIncrement"`
	TenantID         int64     `gorm:"type:bigint;not null;default:0;index"`
	ConversationID   int64     `gorm:"type:bigint;not null;default:0;index"`
	MessageID        int64     `gorm:"type:bigint;not null;default:0;index"`
	RequestID        string    `gorm:"type:varchar(128);not null;default:'';index"`
	AIAgentID        int64     `gorm:"type:bigint;not null;default:0;index"`
	UserMessage      string    `gorm:"type:longtext"`
	PlannedAction    string    `gorm:"type:varchar(30);not null;default:'';index"`
	PlannedSkillCode string    `gorm:"type:varchar(100);not null;default:'';index"`
	PlannedSkillName string    `gorm:"type:varchar(100);not null;default:''"`
	SkillRouteTrace  string    `gorm:"type:text"`
	ToolSearchTrace  string    `gorm:"type:text"`
	GraphToolTrace   string    `gorm:"type:text"`
	GraphToolCode    string    `gorm:"type:varchar(200);not null;default:'';index"`
	HandoffReason    string    `gorm:"type:varchar(500);not null;default:''"`
	PlannedToolCode  string    `gorm:"type:varchar(200);not null;default:'';index"`
	PlanReason       string    `gorm:"type:varchar(500);not null;default:''"`
	InterruptType    string    `gorm:"type:varchar(50);not null;default:'';index"`
	ResumeSource     string    `gorm:"type:varchar(50);not null;default:'';index"`
	FinalAction      string    `gorm:"type:varchar(30);not null;default:'';index"`
	FinalStatus      string    `gorm:"type:varchar(30);not null;default:'';index"`
	ReplyText        string    `gorm:"type:longtext"`
	ErrorMessage     string    `gorm:"type:text"`
	LatencyMs        int64     `gorm:"type:bigint;not null;default:0"`
	TraceData        string    `gorm:"type:text"`
	CreatedAt        time.Time `gorm:"type:datetime;not null;index"`
}

// ReplyIntentProfile stores industry-level intent detection prompt and output schema.
type ReplyIntentProfile struct {
	ID                 int64        `gorm:"primaryKey;autoIncrement"`
	Code               string       `gorm:"type:varchar(100);not null;uniqueIndex"`
	Name               string       `gorm:"type:varchar(120);not null;default:''"`
	IndustryCode       string       `gorm:"type:varchar(100);not null;default:'';index"`
	Description        string       `gorm:"type:text"`
	IntentDetectPrompt string       `gorm:"type:text"`
	IntentJSONSchema   string       `gorm:"type:text"`
	Revision           int64        `gorm:"type:bigint;not null;default:1;index"`
	PublishedAt        *time.Time   `gorm:"type:datetime;index"`
	PublishedBy        int64        `gorm:"type:bigint;not null;default:0;index"`
	Status             enums.Status `gorm:"type:int;not null;default:0;index"`
	SortNo             int          `gorm:"type:int;not null;default:0;index"`
	Remark             string       `gorm:"type:text"`
	AuditFields
}

// ReplyIntentConfig stores editable intent detection and prompt-pack rules for the reply runtime.
type ReplyIntentConfig struct {
	ID                 int64        `gorm:"primaryKey;autoIncrement"`
	Code               string       `gorm:"type:varchar(100);not null;uniqueIndex:uk_reply_intent_profile_code,priority:2"`
	Name               string       `gorm:"type:varchar(120);not null;default:''"`
	Description        string       `gorm:"type:text"`
	IntentProfileID    int64        `gorm:"type:bigint;not null;uniqueIndex:uk_reply_intent_profile_code,priority:1;index"`
	Priority           int          `gorm:"type:int;not null;default:100;index"`
	MatchMode          string       `gorm:"type:varchar(30);not null;default:'hybrid';index"`
	Keywords           string       `gorm:"type:text"`
	PositiveExamples   string       `gorm:"type:text"`
	NegativeExamples   string       `gorm:"type:text"`
	RequiredContext    string       `gorm:"type:text"`
	NeedsKnowledge     bool         `gorm:"type:bool;not null;default:false;index"`
	NeedsResource      bool         `gorm:"type:bool;not null;default:false;index"`
	ResourceType       string       `gorm:"type:varchar(50);not null;default:'';index"`
	NeedsTool          bool         `gorm:"type:bool;not null;default:false;index"`
	ToolCodes          string       `gorm:"type:text"`
	NeedsHumanRoute    bool         `gorm:"type:bool;not null;default:false;index"`
	HumanRoutePolicy   string       `gorm:"type:varchar(50);not null;default:'';index"`
	PromptPack         string       `gorm:"type:text"`
	ReplyPlanTemplate  string       `gorm:"type:text"`
	ValidationRules    string       `gorm:"type:text"`
	NoReplyWhenMatched bool         `gorm:"type:bool;not null;default:false;index"`
	Status             enums.Status `gorm:"type:int;not null;default:0;index"`
	SortNo             int          `gorm:"type:int;not null;default:0;index"`
	Remark             string       `gorm:"type:text"`
	AuditFields
}

// ConversationInterrupt 表示会话级待恢复中断记录。
type ConversationInterrupt struct {
	ID                  int64      `gorm:"primaryKey;autoIncrement"`
	TenantID            int64      `gorm:"type:bigint;not null;default:0;index"`
	ConversationID      int64      `gorm:"type:bigint;not null;default:0;index"`
	AIAgentID           int64      `gorm:"type:bigint;not null;default:0;index"`
	SourceMessageID     int64      `gorm:"type:bigint;not null;default:0;index"`
	LastResumeMessageID int64      `gorm:"type:bigint;not null;default:0;index"`
	CheckPointID        string     `gorm:"type:varchar(128);not null;default:'';uniqueIndex"`
	InterruptID         string     `gorm:"type:varchar(255);not null;default:'';index"`
	InterruptType       string     `gorm:"type:varchar(50);not null;default:'';index"`
	Status              string     `gorm:"type:varchar(30);not null;default:'';index"`
	PromptText          string     `gorm:"type:text"`
	RequestData         string     `gorm:"type:text"`
	CheckPointData      string     `gorm:"type:longtext"`
	ResumeCount         int        `gorm:"type:int;not null;default:0"`
	ExpiresAt           *time.Time `gorm:"type:datetime;index"`
	CreatedAt           time.Time  `gorm:"type:datetime;not null;index"`
	UpdatedAt           time.Time  `gorm:"type:datetime;not null;index"`
}

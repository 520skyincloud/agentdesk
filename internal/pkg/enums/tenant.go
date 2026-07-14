package enums

type TenantVerificationStatus string

const (
	TenantVerificationStatusPending  TenantVerificationStatus = "pending"
	TenantVerificationStatusVerified TenantVerificationStatus = "verified"
	TenantVerificationStatusRejected TenantVerificationStatus = "rejected"
)

var tenantVerificationStatusLabelMap = map[TenantVerificationStatus]string{
	TenantVerificationStatusPending:  "待核验",
	TenantVerificationStatusVerified: "已核验",
	TenantVerificationStatusRejected: "已驳回",
}

type UserRegistrationSource string

const (
	UserRegistrationSourcePlatform        UserRegistrationSource = "platform_created"
	UserRegistrationSourceTenant          UserRegistrationSource = "tenant_created"
	UserRegistrationSourceInvitation      UserRegistrationSource = "invitation"
	UserRegistrationSourceLegacyMigration UserRegistrationSource = "legacy_migration"
	UserRegistrationSourceWxWork          UserRegistrationSource = "wxwork"
	UserRegistrationSourceOIDC            UserRegistrationSource = "oidc"
)

var userRegistrationSourceLabelMap = map[UserRegistrationSource]string{
	UserRegistrationSourcePlatform:        "平台创建",
	UserRegistrationSourceTenant:          "公司创建",
	UserRegistrationSourceInvitation:      "邀请注册",
	UserRegistrationSourceLegacyMigration: "历史迁移",
	UserRegistrationSourceWxWork:          "企业微信登录",
	UserRegistrationSourceOIDC:            "OIDC 登录",
}

type UserApprovalStatus string

const (
	UserApprovalStatusPending  UserApprovalStatus = "pending"
	UserApprovalStatusApproved UserApprovalStatus = "approved"
	UserApprovalStatusRejected UserApprovalStatus = "rejected"
)

var userApprovalStatusLabelMap = map[UserApprovalStatus]string{
	UserApprovalStatusPending:  "待审核",
	UserApprovalStatusApproved: "已通过",
	UserApprovalStatusRejected: "已拒绝",
}

type TenantRegistrationAction string

const (
	TenantRegistrationActionValidateInvite TenantRegistrationAction = "validate_invite"
	TenantRegistrationActionRegister       TenantRegistrationAction = "register"
	TenantRegistrationActionReview         TenantRegistrationAction = "review"
)

var tenantRegistrationActionLabelMap = map[TenantRegistrationAction]string{
	TenantRegistrationActionValidateInvite: "校验邀请码",
	TenantRegistrationActionRegister:       "邀请注册",
	TenantRegistrationActionReview:         "审核注册",
}

type TenantRegistrationReviewDecision string

const (
	TenantRegistrationReviewDecisionApprove TenantRegistrationReviewDecision = "approve"
	TenantRegistrationReviewDecisionReject  TenantRegistrationReviewDecision = "reject"
)

var tenantRegistrationReviewDecisionLabelMap = map[TenantRegistrationReviewDecision]string{
	TenantRegistrationReviewDecisionApprove: "通过",
	TenantRegistrationReviewDecisionReject:  "拒绝",
}

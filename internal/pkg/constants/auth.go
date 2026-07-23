package constants

const (
	RoleCodeSuperAdmin   = "super_admin"    // 超管
	RoleCodeAdmin        = "admin"          // 管理员
	RoleCodeTenantAdmin  = "tenant_admin"   // 公司主管
	RoleCodeCsTeamLeader = "cs_team_leader" // 客服组长
	RoleCodeCsUser       = "cs_user"        // 客服
	RoleCodeStoreStaff   = "store_staff"    // 门店员工
)

const (
	RoleScopePlatform = "platform"
	RoleScopeTenant   = "tenant"

	PermissionScopePlatform = "platform"
	PermissionScopeTenant   = "tenant"
)

const (
	RoleAuthoritySuperAdmin  = 100
	RoleAuthorityAdmin       = 80
	RoleAuthorityTenantAdmin = 60
	RoleAuthorityTeamLeader  = 40
	RoleAuthorityMember      = 20
)

// TenantInvitationValidityDays is the first-version validity window for a
// newly created or rotated company invitation.
const TenantInvitationValidityDays = 90

const (
	StoreManagedModeFull = "full"
	StoreManagedModeSemi = "semi"
	StoreManagedModeNone = "none"
)

const (
	AuthTokenPrefix         = "ak_"
	TenantHeaderName        = "X-Tenant-ID"
	LegacyDefaultTenantCode = "legacy-default"
)

const (
	ClientTypeAdminWeb = "admin_web"
)

const (
	BootstrapAdminUsername = "admin"
	BootstrapAdminPassword = "ChangeMe123!"
	BootstrapAdminNickname = "超级管理员"
)

// Permission 权限结构体
type Permission struct {
	Name      string
	Code      string
	Type      string
	Scope     string
	GroupName string
	Method    string
	APIPath   string
	SortNo    int
}

// 权限常量定义
var (
	// 用户相关权限
	PermissionUserView       = Permission{Name: "查看用户", Code: "user.view", Type: "api", GroupName: "user", Method: "ANY", APIPath: "/api/dashboard/user/list", SortNo: 10}
	PermissionUserCreate     = Permission{Name: "创建用户", Code: "user.create", Type: "api", GroupName: "user", Method: "POST", APIPath: "/api/dashboard/user/create", SortNo: 20}
	PermissionUserUpdate     = Permission{Name: "更新用户", Code: "user.update", Type: "api", GroupName: "user", Method: "POST", APIPath: "/api/dashboard/user/update", SortNo: 30}
	PermissionUserDelete     = Permission{Name: "删除用户", Code: "user.delete", Type: "api", GroupName: "user", Method: "POST", APIPath: "/api/dashboard/user/delete", SortNo: 40}
	PermissionUserAssignRole = Permission{Name: "分配用户角色", Code: "user.assignRole", Type: "api", GroupName: "user", Method: "POST", APIPath: "/api/dashboard/user/assign_role", SortNo: 50}

	// 角色相关权限
	PermissionRoleView             = Permission{Name: "查看角色", Code: "role.view", Type: "api", GroupName: "role", Method: "ANY", APIPath: "/api/dashboard/role/list", SortNo: 110}
	PermissionRoleCreate           = Permission{Name: "创建角色", Code: "role.create", Type: "api", Scope: PermissionScopePlatform, GroupName: "role", Method: "POST", APIPath: "/api/dashboard/role/create", SortNo: 120}
	PermissionRoleUpdate           = Permission{Name: "更新角色", Code: "role.update", Type: "api", Scope: PermissionScopePlatform, GroupName: "role", Method: "POST", APIPath: "/api/dashboard/role/update", SortNo: 130}
	PermissionRoleDelete           = Permission{Name: "删除角色", Code: "role.delete", Type: "api", Scope: PermissionScopePlatform, GroupName: "role", Method: "POST", APIPath: "/api/dashboard/role/delete", SortNo: 140}
	PermissionRoleAssignPermission = Permission{Name: "分配角色权限", Code: "role.assignPermission", Type: "api", Scope: PermissionScopePlatform, GroupName: "role", Method: "POST", APIPath: "/api/dashboard/role/assign_permission", SortNo: 150}

	// 权限相关权限
	PermissionPermissionView = Permission{Name: "查看权限", Code: "permission.view", Type: "api", GroupName: "permission", Method: "ANY", APIPath: "/api/dashboard/permission/list", SortNo: 210}

	// 会话相关权限
	PermissionSessionView   = Permission{Name: "查看登录会话", Code: "session.view", Type: "api", Scope: PermissionScopePlatform, GroupName: "session", Method: "ANY", APIPath: "/api/dashboard/session/list", SortNo: 310}
	PermissionSessionRevoke = Permission{Name: "踢除登录会话", Code: "session.revoke", Type: "api", Scope: PermissionScopePlatform, GroupName: "session", Method: "POST", APIPath: "/api/dashboard/session/revoke", SortNo: 320}

	// 接入公司与邀请注册相关权限
	PermissionTenantView               = Permission{Name: "查看接入公司", Code: "tenant.view", Type: "api", Scope: PermissionScopePlatform, GroupName: "tenant", Method: "ANY", APIPath: "/api/dashboard/tenant/list", SortNo: 330}
	PermissionTenantCreate             = Permission{Name: "创建接入公司", Code: "tenant.create", Type: "api", Scope: PermissionScopePlatform, GroupName: "tenant", Method: "POST", APIPath: "/api/dashboard/tenant/create", SortNo: 340}
	PermissionTenantUpdate             = Permission{Name: "更新接入公司", Code: "tenant.update", Type: "api", Scope: PermissionScopePlatform, GroupName: "tenant", Method: "POST", APIPath: "/api/dashboard/tenant/update", SortNo: 350}
	PermissionTenantUpdateStatus       = Permission{Name: "启停接入公司", Code: "tenant.updateStatus", Type: "api", Scope: PermissionScopePlatform, GroupName: "tenant", Method: "POST", APIPath: "/api/dashboard/tenant/update_status", SortNo: 360}
	PermissionTenantSwitch             = Permission{Name: "切换接入公司", Code: "tenant.switch", Type: "api", Scope: PermissionScopePlatform, GroupName: "tenant", Method: "ANY", APIPath: "/api/dashboard/tenant/list", SortNo: 370}
	PermissionTenantInviteView         = Permission{Name: "查看公司邀请码", Code: "tenantInvite.view", Type: "api", GroupName: "tenantInvite", Method: "GET", APIPath: "/api/dashboard/tenant-invitation/current", SortNo: 380}
	PermissionTenantInviteRotate       = Permission{Name: "重置公司邀请码", Code: "tenantInvite.rotate", Type: "api", GroupName: "tenantInvite", Method: "POST", APIPath: "/api/dashboard/tenant-invitation/rotate", SortNo: 390}
	PermissionTenantRegistrationView   = Permission{Name: "查看邀请注册账号", Code: "tenantRegistration.view", Type: "api", GroupName: "tenantRegistration", Method: "ANY", APIPath: "/api/dashboard/tenant-registration/list", SortNo: 400}
	PermissionTenantRegistrationReview = Permission{Name: "审核邀请注册账号", Code: "tenantRegistration.review", Type: "api", GroupName: "tenantRegistration", Method: "POST", APIPath: "/api/dashboard/tenant-registration/review", SortNo: 405}

	// 运营总览相关权限
	PermissionDashboardView                = Permission{Name: "查看运营总览", Code: "dashboard.view", Type: "api", GroupName: "dashboard", Method: "GET", APIPath: "/api/dashboard/dashboard/overview", SortNo: 406}
	PermissionServiceAnalyticsView         = Permission{Name: "查看客服运营分析", Code: "serviceAnalytics.view", Type: "api", GroupName: "serviceAnalytics", Method: "GET", APIPath: "/api/dashboard/service-analytics/overview", SortNo: 407}
	PermissionServiceAnalyticsExport       = Permission{Name: "导出客服运营分析", Code: "serviceAnalytics.export", Type: "api", GroupName: "serviceAnalytics", Method: "GET", APIPath: "/api/dashboard/service-analytics/export", SortNo: 408}
	PermissionServiceAnalyticsManagePolicy = Permission{Name: "配置客服统计口径", Code: "serviceAnalytics.managePolicy", Type: "api", GroupName: "serviceAnalytics", Method: "POST", APIPath: "/api/dashboard/service-analytics/policy/update", SortNo: 409}
	PermissionConversationRecordView       = Permission{Name: "查看会话记录", Code: "conversationRecord.view", Type: "api", GroupName: "conversationRecord", Method: "ANY", APIPath: "/api/dashboard/service-session/list", SortNo: 410}
	PermissionConversationRecordAnnotate   = Permission{Name: "编辑会话服务小记", Code: "conversationRecord.annotate", Type: "api", GroupName: "conversationRecord", Method: "POST", APIPath: "/api/dashboard/service-session/annotate", SortNo: 411}
	PermissionConversationRecordExport     = Permission{Name: "导出会话记录", Code: "conversationRecord.export", Type: "api", GroupName: "conversationRecord", Method: "GET", APIPath: "/api/dashboard/service-session/export", SortNo: 412}
	PermissionQualityInspectionView        = Permission{Name: "查看人工回复质检", Code: "qualityInspection.view", Type: "api", GroupName: "qualityInspection", Method: "ANY", APIPath: "/api/dashboard/quality-inspection/pool", SortNo: 413}
	PermissionQualityInspectionManage      = Permission{Name: "执行人工回复质检", Code: "qualityInspection.manage", Type: "api", GroupName: "qualityInspection", Method: "POST", APIPath: "/api/dashboard/quality-inspection/save", SortNo: 414}
	PermissionQualitySamplingCreate        = Permission{Name: "创建质检抽样批次", Code: "qualitySampling.create", Type: "api", GroupName: "qualityInspection", Method: "POST", APIPath: "/api/dashboard/quality-sampling/create", SortNo: 415}
	PermissionQualityTemplateManage        = Permission{Name: "管理人工质检模板", Code: "qualityTemplate.manage", Type: "api", GroupName: "qualityInspection", Method: "POST", APIPath: "/api/dashboard/quality-template/save", SortNo: 416}
	PermissionConversationEvaluationView   = Permission{Name: "查看客户评价", Code: "conversationEvaluation.view", Type: "api", GroupName: "conversationEvaluation", Method: "ANY", APIPath: "/api/dashboard/conversation-evaluation/list", SortNo: 417}
	PermissionConversationEvaluationInvite = Permission{Name: "邀请客户评价", Code: "conversationEvaluation.invite", Type: "api", GroupName: "conversationEvaluation", Method: "POST", APIPath: "/api/dashboard/conversation-evaluation/invite", SortNo: 418}
	PermissionReportViewPresetManage       = Permission{Name: "管理个人报表视图", Code: "reportViewPreset.manage", Type: "api", GroupName: "serviceAnalytics", Method: "POST", APIPath: "/api/dashboard/report-view-preset/save", SortNo: 419}
	PermissionAgentPresenceUpdate          = Permission{Name: "更新客服在线状态", Code: "agentPresence.update", Type: "api", GroupName: "agent", Method: "POST", APIPath: "/api/dashboard/agent-presence/update", SortNo: 420}

	// 门店工作台相关权限
	PermissionStoreWorkbenchView   = Permission{Name: "查看门店工作台", Code: "storeWorkbench.view", Type: "api", GroupName: "storeWorkbench", Method: "GET", APIPath: "/api/dashboard/store-workbench/current", SortNo: 407}
	PermissionStoreWorkbenchUpdate = Permission{Name: "更新门店工作台", Code: "storeWorkbench.update", Type: "api", GroupName: "storeWorkbench", Method: "POST", APIPath: "/api/dashboard/store-workbench/update", SortNo: 408}

	// 客服会话相关权限
	PermissionConversationView         = Permission{Name: "查看会话", Code: "conversation.view", Type: "api", GroupName: "conversation", Method: "ANY", APIPath: "/api/dashboard/conversation/list", SortNo: 410}
	PermissionConversationAssign       = Permission{Name: "分配会话", Code: "conversation.assign", Type: "api", GroupName: "conversation", Method: "POST", APIPath: "/api/dashboard/conversation-dispatch/assign", SortNo: 430}
	PermissionConversationTransfer     = Permission{Name: "转接会话", Code: "conversation.transfer", Type: "api", GroupName: "conversation", Method: "POST", APIPath: "/api/dashboard/conversation-dispatch/transfer", SortNo: 440}
	PermissionConversationClose        = Permission{Name: "关闭会话", Code: "conversation.close", Type: "api", GroupName: "conversation", Method: "POST", APIPath: "/api/dashboard/conversation/close", SortNo: 450}
	PermissionConversationSend         = Permission{Name: "发送会话消息", Code: "conversation.send", Type: "api", GroupName: "conversation", Method: "POST", APIPath: "/api/dashboard/conversation/send_message", SortNo: 460}
	PermissionConversationTag          = Permission{Name: "管理门店客户标签", Code: "conversation.tag", Type: "api", GroupName: "conversation", Method: "POST", APIPath: "/api/dashboard/conversation/customer_tag/add", SortNo: 470}
	PermissionConversationHandover     = Permission{Name: "处理会话交接", Code: "conversation.handover", Type: "api", GroupName: "conversation", Method: "ANY", APIPath: "/api/dashboard/conversation-dispatch/list", SortNo: 480}
	PermissionConversationRecycle      = Permission{Name: "回收会话", Code: "conversation.recycle", Type: "api", GroupName: "conversation", Method: "POST", APIPath: "/api/dashboard/conversation-dispatch/release", SortNo: 490}
	PermissionConversationLinkCustomer = Permission{Name: "关联会话客户", Code: "conversation.linkCustomer", Type: "api", GroupName: "conversation", Method: "POST", APIPath: "/api/dashboard/conversation/link_customer", SortNo: 495}

	// 工单相关权限
	PermissionTicketView         = Permission{Name: "查看工单", Code: "ticket.view", Type: "api", GroupName: "ticket", Method: "ANY", APIPath: "/api/dashboard/ticket/list", SortNo: 500}
	PermissionTicketCreate       = Permission{Name: "创建工单", Code: "ticket.create", Type: "api", GroupName: "ticket", Method: "POST", APIPath: "/api/dashboard/ticket/create", SortNo: 510}
	PermissionTicketUpdate       = Permission{Name: "更新工单", Code: "ticket.update", Type: "api", GroupName: "ticket", Method: "POST", APIPath: "/api/dashboard/ticket/update", SortNo: 520}
	PermissionTicketAssign       = Permission{Name: "指派工单", Code: "ticket.assign", Type: "api", GroupName: "ticket", Method: "POST", APIPath: "/api/dashboard/ticket/assign", SortNo: 530}
	PermissionTicketChangeStatus = Permission{Name: "变更工单状态", Code: "ticket.changeStatus", Type: "api", GroupName: "ticket", Method: "POST", APIPath: "/api/dashboard/ticket/change_status", SortNo: 540}
	PermissionTicketProgress     = Permission{Name: "更新工单进展", Code: "ticket.progress", Type: "api", GroupName: "ticket", Method: "POST", APIPath: "/api/dashboard/ticket/progress/create", SortNo: 550}

	// 通知相关权限
	PermissionNotificationView   = Permission{Name: "查看通知", Code: "notification.view", Type: "api", GroupName: "notification", Method: "ANY", APIPath: "/api/dashboard/notification/list", SortNo: 680}
	PermissionNotificationUpdate = Permission{Name: "更新通知", Code: "notification.update", Type: "api", GroupName: "notification", Method: "POST", APIPath: "/api/dashboard/notification/mark_read", SortNo: 690}

	// 快捷回复相关权限
	PermissionQuickReplyView   = Permission{Name: "查看快捷回复", Code: "quickReply.view", Type: "api", GroupName: "quickReply", Method: "ANY", APIPath: "/api/dashboard/quick-reply/list", SortNo: 610}
	PermissionQuickReplyCreate = Permission{Name: "创建快捷回复", Code: "quickReply.create", Type: "api", GroupName: "quickReply", Method: "POST", APIPath: "/api/dashboard/quick-reply/create", SortNo: 620}
	PermissionQuickReplyUpdate = Permission{Name: "更新快捷回复", Code: "quickReply.update", Type: "api", GroupName: "quickReply", Method: "POST", APIPath: "/api/dashboard/quick-reply/update", SortNo: 630}
	PermissionQuickReplyDelete = Permission{Name: "删除快捷回复", Code: "quickReply.delete", Type: "api", GroupName: "quickReply", Method: "POST", APIPath: "/api/dashboard/quick-reply/delete", SortNo: 640}

	// 标签相关权限
	PermissionTagView   = Permission{Name: "查看行业标签目录与策略", Code: "tag.view", Type: "api", GroupName: "tag", Method: "ANY", APIPath: "/api/dashboard/tag/list", SortNo: 550}
	PermissionTagCreate = Permission{Name: "创建标签", Code: "tag.create", Type: "api", GroupName: "tag", Method: "POST", APIPath: "/api/dashboard/tag/create", SortNo: 560}
	PermissionTagUpdate = Permission{Name: "配置行业标签与演化策略", Code: "tag.update", Type: "api", GroupName: "tag", Method: "POST", APIPath: "/api/dashboard/customer-tag/policy/update", SortNo: 570}
	PermissionTagDelete = Permission{Name: "删除标签", Code: "tag.delete", Type: "api", GroupName: "tag", Method: "POST", APIPath: "/api/dashboard/tag/delete", SortNo: 580}

	// 接入渠道相关权限
	PermissionChannelView   = Permission{Name: "查看接入渠道", Code: "channel.view", Type: "api", GroupName: "channel", Method: "ANY", APIPath: "/api/dashboard/channel/list", SortNo: 625}
	PermissionChannelCreate = Permission{Name: "创建接入渠道", Code: "channel.create", Type: "api", GroupName: "channel", Method: "POST", APIPath: "/api/dashboard/channel/create", SortNo: 626}
	PermissionChannelUpdate = Permission{Name: "更新接入渠道", Code: "channel.update", Type: "api", GroupName: "channel", Method: "POST", APIPath: "/api/dashboard/channel/update", SortNo: 627}
	PermissionChannelDelete = Permission{Name: "删除接入渠道", Code: "channel.delete", Type: "api", GroupName: "channel", Method: "POST", APIPath: "/api/dashboard/channel/delete", SortNo: 628}

	// 客户相关权限
	PermissionCustomerView   = Permission{Name: "查看客户", Code: "customer.view", Type: "api", GroupName: "customer", Method: "POST", APIPath: "/api/dashboard/customer/list", SortNo: 630}
	PermissionCustomerCreate = Permission{Name: "创建客户", Code: "customer.create", Type: "api", GroupName: "customer", Method: "POST", APIPath: "/api/dashboard/customer/create", SortNo: 640}
	PermissionCustomerUpdate = Permission{Name: "更新客户", Code: "customer.update", Type: "api", GroupName: "customer", Method: "POST", APIPath: "/api/dashboard/customer/update", SortNo: 650}
	PermissionCustomerDelete = Permission{Name: "删除客户", Code: "customer.delete", Type: "api", GroupName: "customer", Method: "POST", APIPath: "/api/dashboard/customer/delete", SortNo: 660}

	// 客服相关权限
	PermissionAgentView         = Permission{Name: "查看客服", Code: "agent.view", Type: "api", GroupName: "agent", Method: "ANY", APIPath: "/api/dashboard/agent/list", SortNo: 610}
	PermissionAgentCreate       = Permission{Name: "创建客服", Code: "agent.create", Type: "api", GroupName: "agent", Method: "POST", APIPath: "/api/dashboard/agent/create", SortNo: 620}
	PermissionAgentUpdate       = Permission{Name: "更新客服", Code: "agent.update", Type: "api", GroupName: "agent", Method: "POST", APIPath: "/api/dashboard/agent/update", SortNo: 630}
	PermissionAgentDelete       = Permission{Name: "删除客服", Code: "agent.delete", Type: "api", GroupName: "agent", Method: "POST", APIPath: "/api/dashboard/agent/delete", SortNo: 640}
	PermissionAgentUpdateStatus = Permission{Name: "更新客服状态", Code: "agent.updateStatus", Type: "api", GroupName: "agent", Method: "POST", APIPath: "/api/dashboard/agent/update_status", SortNo: 650}
	PermissionAgentConfig       = Permission{Name: "配置客服服务规则", Code: "agent.config", Type: "api", GroupName: "agent", Method: "POST", APIPath: "/api/dashboard/agent/update_service_config", SortNo: 660}

	// 客服组相关权限
	PermissionAgentTeamView   = Permission{Name: "查看客服组", Code: "agentTeam.view", Type: "api", GroupName: "agentTeam", Method: "ANY", APIPath: "/api/dashboard/agent-team/list", SortNo: 710}
	PermissionAgentTeamCreate = Permission{Name: "创建客服组", Code: "agentTeam.create", Type: "api", GroupName: "agentTeam", Method: "POST", APIPath: "/api/dashboard/agent-team/create", SortNo: 720}
	PermissionAgentTeamUpdate = Permission{Name: "更新客服组", Code: "agentTeam.update", Type: "api", GroupName: "agentTeam", Method: "POST", APIPath: "/api/dashboard/agent-team/update", SortNo: 730}
	PermissionAgentTeamDelete = Permission{Name: "删除客服组", Code: "agentTeam.delete", Type: "api", GroupName: "agentTeam", Method: "POST", APIPath: "/api/dashboard/agent-team/delete", SortNo: 740}

	// 客服组排班相关权限
	PermissionAgentTeamScheduleView          = Permission{Name: "查看客服组排班", Code: "agentTeamSchedule.view", Type: "api", GroupName: "agentTeamSchedule", Method: "ANY", APIPath: "/api/dashboard/agent-team-schedule/list", SortNo: 810}
	PermissionAgentTeamScheduleCreate        = Permission{Name: "创建客服组排班", Code: "agentTeamSchedule.create", Type: "api", GroupName: "agentTeamSchedule", Method: "POST", APIPath: "/api/dashboard/agent-team-schedule/create", SortNo: 820}
	PermissionAgentTeamScheduleUpdate        = Permission{Name: "更新客服组排班", Code: "agentTeamSchedule.update", Type: "api", GroupName: "agentTeamSchedule", Method: "POST", APIPath: "/api/dashboard/agent-team-schedule/update", SortNo: 830}
	PermissionAgentTeamScheduleDelete        = Permission{Name: "删除客服组排班", Code: "agentTeamSchedule.delete", Type: "api", GroupName: "agentTeamSchedule", Method: "POST", APIPath: "/api/dashboard/agent-team-schedule/delete", SortNo: 840}
	PermissionAgentTeamScheduleBatchGenerate = Permission{Name: "批量生成客服组排班", Code: "agentTeamSchedule.batchGenerate", Type: "api", GroupName: "agentTeamSchedule", Method: "POST", APIPath: "/api/dashboard/agent-team-schedule/batch_generate", SortNo: 850}

	// 文件资源相关权限
	PermissionAssetView   = Permission{Name: "查看文件资源", Code: "asset.view", Type: "api", GroupName: "asset", Method: "ANY", APIPath: "/api/dashboard/asset/list", SortNo: 1210}
	PermissionAssetCreate = Permission{Name: "上传文件资源", Code: "asset.create", Type: "api", GroupName: "asset", Method: "POST", APIPath: "/api/dashboard/asset/create", SortNo: 1220}
	PermissionAssetDelete = Permission{Name: "删除文件资源", Code: "asset.delete", Type: "api", GroupName: "asset", Method: "POST", APIPath: "/api/dashboard/asset/delete", SortNo: 1230}

	// 平台存储设置相关权限
	PermissionStorageSettingView   = Permission{Name: "查看平台存储设置", Code: "storageSetting.view", Type: "api", Scope: PermissionScopePlatform, GroupName: "storageSetting", Method: "GET", APIPath: "/api/dashboard/storage-setting/get", SortNo: 1240}
	PermissionStorageSettingUpdate = Permission{Name: "修改平台存储设置", Code: "storageSetting.update", Type: "api", Scope: PermissionScopePlatform, GroupName: "storageSetting", Method: "POST", APIPath: "/api/dashboard/storage-setting/update", SortNo: 1250}

	// 平台企微设备池相关权限
	PermissionWxWorkDevicePoolView   = Permission{Name: "查看平台企微设备池", Code: "wxworkDevicePool.view", Type: "api", Scope: PermissionScopePlatform, GroupName: "wxworkDevicePool", Method: "ANY", APIPath: "/api/dashboard/wxwork-protocol-device-pool/list", SortNo: 1260}
	PermissionWxWorkDevicePoolUpdate = Permission{Name: "管理平台企微设备池", Code: "wxworkDevicePool.update", Type: "api", Scope: PermissionScopePlatform, GroupName: "wxworkDevicePool", Method: "POST", APIPath: "/api/dashboard/wxwork-protocol-device-pool/update_settings", SortNo: 1270}
	PermissionWxWorkDevicePoolSync   = Permission{Name: "同步平台企微设备池", Code: "wxworkDevicePool.sync", Type: "api", Scope: PermissionScopePlatform, GroupName: "wxworkDevicePool", Method: "POST", APIPath: "/api/dashboard/wxwork-protocol-device-pool/sync", SortNo: 1280}

	// 接待策略只读选项权限。保留 aiAgent.view 编码以兼容历史角色和已签发 token。
	PermissionAIAgentView                 = Permission{Name: "查看接待策略选项", Code: "aiAgent.view", Type: "api", GroupName: "runtimeStrategy", Method: "GET", APIPath: "/api/dashboard/ai-agent/list_all", SortNo: 1310}
	PermissionTenantModelGrantView        = Permission{Name: "查看租户模型授权", Code: "tenantModelGrant.view", Type: "api", Scope: PermissionScopePlatform, GroupName: "tenantModelGrant", Method: "POST", APIPath: "/api/dashboard/tenant/model_access", SortNo: 1350}
	PermissionTenantModelGrantUpdate      = Permission{Name: "更新租户模型授权", Code: "tenantModelGrant.update", Type: "api", Scope: PermissionScopePlatform, GroupName: "tenantModelGrant", Method: "POST", APIPath: "/api/dashboard/tenant/update_model_access", SortNo: 1360}
	PermissionTenantModelAssignmentView   = Permission{Name: "查看租户账号模型分配", Code: "tenantModelAssignment.view", Type: "api", Scope: PermissionScopePlatform, GroupName: "tenantModelAssignment", Method: "POST", APIPath: "/api/dashboard/wxwork-protocol-instance/model_assignments", SortNo: 1370}
	PermissionTenantModelAssignmentUpdate = Permission{Name: "更新租户账号模型分配", Code: "tenantModelAssignment.update", Type: "api", Scope: PermissionScopePlatform, GroupName: "tenantModelAssignment", Method: "POST", APIPath: "/api/dashboard/wxwork-protocol-instance/update_model_assignments", SortNo: 1380}
	PermissionAgentRunLogView             = Permission{Name: "查看 AI 运行诊断", Code: "agentRunLog.view", Type: "api", Scope: PermissionScopePlatform, GroupName: "agentRunLog", Method: "ANY", APIPath: "/api/dashboard/agent-run-log/list", SortNo: 1385}

	// AI 配置相关权限
	PermissionAIConfigView   = Permission{Name: "查看模型方案与门店模型状态", Code: "aiConfig.view", Type: "api", Scope: PermissionScopeTenant, GroupName: "aiConfig", Method: "POST", APIPath: "/api/dashboard/model-profile-template/get", SortNo: 1390}
	PermissionAIConfigCreate = Permission{Name: "创建 AI 配置", Code: "aiConfig.create", Type: "api", Scope: PermissionScopePlatform, GroupName: "aiConfig", Method: "POST", APIPath: "/api/dashboard/ai-config/create", SortNo: 1400}
	PermissionAIConfigUpdate = Permission{Name: "管理模型方案、门店指派与凭据", Code: "aiConfig.update", Type: "api", Scope: PermissionScopeTenant, GroupName: "aiConfig", Method: "POST", APIPath: "/api/dashboard/model-profile-template/update", SortNo: 1410}
	PermissionAIConfigDelete = Permission{Name: "删除 AI 配置", Code: "aiConfig.delete", Type: "api", Scope: PermissionScopePlatform, GroupName: "aiConfig", Method: "POST", APIPath: "/api/dashboard/ai-config/delete", SortNo: 1420}
	PermissionBillingView    = Permission{Name: "查看门店模型账单与用量", Code: "billing.view", Type: "api", Scope: PermissionScopeTenant, GroupName: "billing", Method: "POST", APIPath: "/api/dashboard/billing-query/get", SortNo: 1430}
	PermissionBillingExport  = Permission{Name: "导出门店模型账单与用量", Code: "billing.export", Type: "api", Scope: PermissionScopeTenant, GroupName: "billing", Method: "POST", APIPath: "/api/dashboard/billing-query/export", SortNo: 1440}

	// 知识库相关权限
	PermissionKnowledgeBaseView   = Permission{Name: "查看知识库", Code: "knowledgeBase.view", Type: "api", GroupName: "knowledgeBase", Method: "ANY", APIPath: "/api/dashboard/knowledge-base/list", SortNo: 1410}
	PermissionKnowledgeBaseCreate = Permission{Name: "创建知识库", Code: "knowledgeBase.create", Type: "api", GroupName: "knowledgeBase", Method: "POST", APIPath: "/api/dashboard/knowledge-base/create", SortNo: 1420}
	PermissionKnowledgeBaseUpdate = Permission{Name: "更新知识库", Code: "knowledgeBase.update", Type: "api", GroupName: "knowledgeBase", Method: "POST", APIPath: "/api/dashboard/knowledge-base/update", SortNo: 1430}
	PermissionKnowledgeBaseDelete = Permission{Name: "删除知识库", Code: "knowledgeBase.delete", Type: "api", GroupName: "knowledgeBase", Method: "POST", APIPath: "/api/dashboard/knowledge-base/delete", SortNo: 1440}

	// 知识文档相关权限
	PermissionKnowledgeDocumentView   = Permission{Name: "查看知识文档", Code: "knowledgeDocument.view", Type: "api", GroupName: "knowledgeDocument", Method: "ANY", APIPath: "/api/dashboard/knowledge-document/list", SortNo: 1510}
	PermissionKnowledgeDocumentCreate = Permission{Name: "创建知识文档", Code: "knowledgeDocument.create", Type: "api", GroupName: "knowledgeDocument", Method: "POST", APIPath: "/api/dashboard/knowledge-document/create", SortNo: 1520}
	PermissionKnowledgeDocumentUpdate = Permission{Name: "更新知识文档", Code: "knowledgeDocument.update", Type: "api", GroupName: "knowledgeDocument", Method: "POST", APIPath: "/api/dashboard/knowledge-document/update", SortNo: 1530}
	PermissionKnowledgeDocumentDelete = Permission{Name: "删除知识文档", Code: "knowledgeDocument.delete", Type: "api", GroupName: "knowledgeDocument", Method: "POST", APIPath: "/api/dashboard/knowledge-document/delete", SortNo: 1540}
	PermissionKnowledgeFAQView        = Permission{Name: "查看知识FAQ", Code: "knowledgeFAQ.view", Type: "api", GroupName: "knowledgeFAQ", Method: "ANY", APIPath: "/api/dashboard/knowledge-faq/list", SortNo: 1550}
	PermissionKnowledgeFAQCreate      = Permission{Name: "创建知识FAQ", Code: "knowledgeFAQ.create", Type: "api", GroupName: "knowledgeFAQ", Method: "POST", APIPath: "/api/dashboard/knowledge-faq/create", SortNo: 1560}
	PermissionKnowledgeFAQUpdate      = Permission{Name: "更新知识FAQ", Code: "knowledgeFAQ.update", Type: "api", GroupName: "knowledgeFAQ", Method: "POST", APIPath: "/api/dashboard/knowledge-faq/update", SortNo: 1570}
	PermissionKnowledgeFAQDelete      = Permission{Name: "删除知识FAQ", Code: "knowledgeFAQ.delete", Type: "api", GroupName: "knowledgeFAQ", Method: "POST", APIPath: "/api/dashboard/knowledge-faq/delete", SortNo: 1580}

	// Skill 定义相关权限
	PermissionSkillDefinitionView   = Permission{Name: "查看技能定义", Code: "skillDefinition.view", Type: "api", GroupName: "skillDefinition", Method: "ANY", APIPath: "/api/dashboard/skill-definition/list", SortNo: 1610}
	PermissionSkillDefinitionCreate = Permission{Name: "创建技能定义", Code: "skillDefinition.create", Type: "api", Scope: PermissionScopePlatform, GroupName: "skillDefinition", Method: "POST", APIPath: "/api/dashboard/skill-definition/create", SortNo: 1620}
	PermissionSkillDefinitionUpdate = Permission{Name: "更新技能定义", Code: "skillDefinition.update", Type: "api", Scope: PermissionScopePlatform, GroupName: "skillDefinition", Method: "POST", APIPath: "/api/dashboard/skill-definition/update", SortNo: 1630}
	PermissionSkillDefinitionDelete = Permission{Name: "删除技能定义", Code: "skillDefinition.delete", Type: "api", Scope: PermissionScopePlatform, GroupName: "skillDefinition", Method: "POST", APIPath: "/api/dashboard/skill-definition/delete", SortNo: 1640}

	// MCP 调试相关权限
	PermissionMCPView = Permission{Name: "查看MCP调试信息", Code: "mcp.view", Type: "api", Scope: PermissionScopePlatform, GroupName: "mcp", Method: "POST", APIPath: "/api/dashboard/mcp/list_tools", SortNo: 1710}
	PermissionMCPCall = Permission{Name: "调用MCP工具", Code: "mcp.call", Type: "api", Scope: PermissionScopePlatform, GroupName: "mcp", Method: "POST", APIPath: "/api/dashboard/mcp/call_tool", SortNo: 1720}
)

// Permissions 内置权限列表
var Permissions = []Permission{
	PermissionUserView,
	PermissionUserCreate,
	PermissionUserUpdate,
	PermissionUserDelete,
	PermissionUserAssignRole,
	PermissionRoleView,
	PermissionRoleCreate,
	PermissionRoleUpdate,
	PermissionRoleDelete,
	PermissionRoleAssignPermission,
	PermissionPermissionView,
	PermissionSessionView,
	PermissionSessionRevoke,
	PermissionTenantView,
	PermissionTenantCreate,
	PermissionTenantUpdate,
	PermissionTenantUpdateStatus,
	PermissionTenantSwitch,
	PermissionTenantInviteView,
	PermissionTenantInviteRotate,
	PermissionTenantRegistrationView,
	PermissionTenantRegistrationReview,
	PermissionDashboardView,
	PermissionServiceAnalyticsView,
	PermissionServiceAnalyticsExport,
	PermissionServiceAnalyticsManagePolicy,
	PermissionConversationRecordView,
	PermissionConversationRecordAnnotate,
	PermissionConversationRecordExport,
	PermissionQualityInspectionView,
	PermissionQualityInspectionManage,
	PermissionQualitySamplingCreate,
	PermissionQualityTemplateManage,
	PermissionConversationEvaluationView,
	PermissionConversationEvaluationInvite,
	PermissionReportViewPresetManage,
	PermissionAgentPresenceUpdate,
	PermissionStoreWorkbenchView,
	PermissionStoreWorkbenchUpdate,
	PermissionConversationView,
	PermissionConversationAssign,
	PermissionConversationTransfer,
	PermissionConversationClose,
	PermissionConversationSend,
	PermissionConversationTag,
	PermissionConversationHandover,
	PermissionConversationRecycle,
	PermissionConversationLinkCustomer,
	PermissionTicketView,
	PermissionTicketCreate,
	PermissionTicketUpdate,
	PermissionTicketAssign,
	PermissionTicketChangeStatus,
	PermissionTicketProgress,
	PermissionNotificationView,
	PermissionNotificationUpdate,
	PermissionQuickReplyView,
	PermissionQuickReplyCreate,
	PermissionQuickReplyUpdate,
	PermissionQuickReplyDelete,
	PermissionTagView,
	PermissionTagUpdate,
	PermissionChannelView,
	PermissionChannelCreate,
	PermissionChannelUpdate,
	PermissionChannelDelete,
	PermissionCustomerView,
	PermissionCustomerCreate,
	PermissionCustomerUpdate,
	PermissionCustomerDelete,
	PermissionAgentView,
	PermissionAgentCreate,
	PermissionAgentUpdate,
	PermissionAgentDelete,
	PermissionAgentUpdateStatus,
	PermissionAgentConfig,
	PermissionAgentTeamView,
	PermissionAgentTeamCreate,
	PermissionAgentTeamUpdate,
	PermissionAgentTeamDelete,
	PermissionAgentTeamScheduleView,
	PermissionAgentTeamScheduleCreate,
	PermissionAgentTeamScheduleUpdate,
	PermissionAgentTeamScheduleDelete,
	PermissionAgentTeamScheduleBatchGenerate,
	PermissionAssetView,
	PermissionAssetCreate,
	PermissionAssetDelete,
	PermissionStorageSettingView,
	PermissionStorageSettingUpdate,
	PermissionWxWorkDevicePoolView,
	PermissionWxWorkDevicePoolUpdate,
	PermissionWxWorkDevicePoolSync,
	PermissionAIAgentView,
	PermissionAgentRunLogView,
	PermissionAIConfigView,
	PermissionAIConfigUpdate,
	PermissionBillingView,
	PermissionBillingExport,
	PermissionKnowledgeBaseView,
	PermissionKnowledgeBaseCreate,
	PermissionKnowledgeBaseUpdate,
	PermissionKnowledgeBaseDelete,
	PermissionKnowledgeDocumentView,
	PermissionKnowledgeDocumentCreate,
	PermissionKnowledgeDocumentUpdate,
	PermissionKnowledgeDocumentDelete,
	PermissionKnowledgeFAQView,
	PermissionKnowledgeFAQCreate,
	PermissionKnowledgeFAQUpdate,
	PermissionKnowledgeFAQDelete,
	PermissionSkillDefinitionView,
	PermissionSkillDefinitionCreate,
	PermissionSkillDefinitionUpdate,
	PermissionSkillDefinitionDelete,
	PermissionMCPView,
	PermissionMCPCall,
}

// PermissionMap 权限映射，用于通过 Code 查找 Permission
var PermissionMap = make(map[string]Permission)

// init 初始化 PermissionMap
func init() {
	for _, permission := range Permissions {
		PermissionMap[permission.Code] = permission
	}
}

type RoleSpec struct {
	Name           string
	Code           string
	Scope          string
	AuthorityLevel int
	SortNo         int
}

var Roles = []RoleSpec{
	{Name: "超级管理员", Code: RoleCodeSuperAdmin, Scope: RoleScopePlatform, AuthorityLevel: RoleAuthoritySuperAdmin, SortNo: 1},
	{Name: "管理员", Code: RoleCodeAdmin, Scope: RoleScopePlatform, AuthorityLevel: RoleAuthorityAdmin, SortNo: 2},
	{Name: "公司主管", Code: RoleCodeTenantAdmin, Scope: RoleScopeTenant, AuthorityLevel: RoleAuthorityTenantAdmin, SortNo: 3},
	{Name: "客服组长", Code: RoleCodeCsTeamLeader, Scope: RoleScopeTenant, AuthorityLevel: RoleAuthorityTeamLeader, SortNo: 4},
	{Name: "客服", Code: RoleCodeCsUser, Scope: RoleScopeTenant, AuthorityLevel: RoleAuthorityMember, SortNo: 5},
	{Name: "门店员工", Code: RoleCodeStoreStaff, Scope: RoleScopeTenant, AuthorityLevel: RoleAuthorityMember, SortNo: 6},
}

var RolePermissions = map[string][]Permission{
	RoleCodeSuperAdmin: Permissions,
	RoleCodeAdmin: {
		PermissionUserView, PermissionUserCreate, PermissionUserUpdate, PermissionUserAssignRole,
		PermissionRoleView, PermissionRoleCreate, PermissionRoleUpdate, PermissionRoleAssignPermission,
		PermissionPermissionView,
		PermissionSessionView, PermissionSessionRevoke,
		PermissionTenantView, PermissionTenantUpdate, PermissionTenantUpdateStatus, PermissionTenantSwitch,
		PermissionTenantInviteView, PermissionTenantInviteRotate, PermissionTenantRegistrationView, PermissionTenantRegistrationReview,
		PermissionDashboardView,
		PermissionServiceAnalyticsView, PermissionServiceAnalyticsExport, PermissionServiceAnalyticsManagePolicy,
		PermissionConversationRecordView, PermissionConversationRecordAnnotate, PermissionConversationRecordExport,
		PermissionQualityInspectionView, PermissionQualityInspectionManage, PermissionQualitySamplingCreate, PermissionQualityTemplateManage,
		PermissionConversationEvaluationView, PermissionConversationEvaluationInvite, PermissionReportViewPresetManage, PermissionAgentPresenceUpdate,
		PermissionConversationView, PermissionConversationAssign, PermissionConversationTransfer, PermissionConversationClose, PermissionConversationSend, PermissionConversationTag, PermissionConversationHandover, PermissionConversationRecycle, PermissionConversationLinkCustomer,
		PermissionTicketView, PermissionTicketCreate, PermissionTicketUpdate, PermissionTicketAssign, PermissionTicketChangeStatus, PermissionTicketProgress,
		PermissionNotificationView, PermissionNotificationUpdate,
		PermissionQuickReplyView, PermissionQuickReplyCreate, PermissionQuickReplyUpdate, PermissionQuickReplyDelete,
		PermissionTagView, PermissionTagUpdate,
		PermissionChannelView, PermissionChannelCreate, PermissionChannelUpdate, PermissionChannelDelete,
		PermissionCustomerView, PermissionCustomerCreate, PermissionCustomerUpdate, PermissionCustomerDelete,
		PermissionAgentView, PermissionAgentCreate, PermissionAgentUpdate, PermissionAgentDelete, PermissionAgentUpdateStatus, PermissionAgentConfig,
		PermissionAgentTeamView, PermissionAgentTeamCreate, PermissionAgentTeamUpdate, PermissionAgentTeamDelete,
		PermissionAgentTeamScheduleView, PermissionAgentTeamScheduleCreate, PermissionAgentTeamScheduleUpdate, PermissionAgentTeamScheduleDelete, PermissionAgentTeamScheduleBatchGenerate,
		PermissionAssetView, PermissionAssetCreate, PermissionAssetDelete,
		PermissionStorageSettingView, PermissionStorageSettingUpdate,
		PermissionWxWorkDevicePoolView, PermissionWxWorkDevicePoolUpdate, PermissionWxWorkDevicePoolSync,
		PermissionAIAgentView,
		PermissionAgentRunLogView,
		PermissionAIConfigView, PermissionAIConfigUpdate,
		PermissionBillingView, PermissionBillingExport,
		PermissionKnowledgeBaseView, PermissionKnowledgeBaseCreate, PermissionKnowledgeBaseUpdate, PermissionKnowledgeBaseDelete,
		PermissionKnowledgeDocumentView, PermissionKnowledgeDocumentCreate, PermissionKnowledgeDocumentUpdate, PermissionKnowledgeDocumentDelete,
		PermissionKnowledgeFAQView, PermissionKnowledgeFAQCreate, PermissionKnowledgeFAQUpdate, PermissionKnowledgeFAQDelete,
		PermissionSkillDefinitionView, PermissionSkillDefinitionCreate, PermissionSkillDefinitionUpdate, PermissionSkillDefinitionDelete,
		PermissionMCPView, PermissionMCPCall,
	},
	RoleCodeTenantAdmin: {
		PermissionUserView, PermissionUserCreate, PermissionUserUpdate, PermissionUserDelete, PermissionUserAssignRole,
		PermissionRoleView, PermissionPermissionView,
		PermissionTenantInviteView, PermissionTenantInviteRotate, PermissionTenantRegistrationView, PermissionTenantRegistrationReview,
		PermissionDashboardView,
		PermissionServiceAnalyticsView, PermissionServiceAnalyticsExport, PermissionServiceAnalyticsManagePolicy,
		PermissionConversationRecordView, PermissionConversationRecordAnnotate, PermissionConversationRecordExport,
		PermissionQualityInspectionView, PermissionQualityInspectionManage, PermissionQualitySamplingCreate, PermissionQualityTemplateManage,
		PermissionConversationEvaluationView, PermissionConversationEvaluationInvite, PermissionReportViewPresetManage, PermissionAgentPresenceUpdate,
		PermissionConversationView, PermissionConversationAssign, PermissionConversationTransfer, PermissionConversationClose, PermissionConversationSend, PermissionConversationTag, PermissionConversationHandover, PermissionConversationRecycle, PermissionConversationLinkCustomer,
		PermissionTicketView, PermissionTicketCreate, PermissionTicketUpdate, PermissionTicketAssign, PermissionTicketChangeStatus, PermissionTicketProgress,
		PermissionNotificationView, PermissionNotificationUpdate,
		PermissionQuickReplyView, PermissionQuickReplyCreate, PermissionQuickReplyUpdate, PermissionQuickReplyDelete,
		PermissionTagView, PermissionTagUpdate,
		PermissionChannelView, PermissionChannelCreate, PermissionChannelUpdate, PermissionChannelDelete,
		PermissionCustomerView, PermissionCustomerCreate, PermissionCustomerUpdate, PermissionCustomerDelete,
		PermissionAgentView, PermissionAgentCreate, PermissionAgentUpdate, PermissionAgentDelete, PermissionAgentUpdateStatus, PermissionAgentConfig,
		PermissionAgentTeamView, PermissionAgentTeamCreate, PermissionAgentTeamUpdate, PermissionAgentTeamDelete,
		PermissionAgentTeamScheduleView, PermissionAgentTeamScheduleCreate, PermissionAgentTeamScheduleUpdate, PermissionAgentTeamScheduleDelete, PermissionAgentTeamScheduleBatchGenerate,
		PermissionAssetView, PermissionAssetCreate, PermissionAssetDelete,
		PermissionAIAgentView,
		PermissionAIConfigView, PermissionAIConfigUpdate,
		PermissionBillingView, PermissionBillingExport,
		PermissionKnowledgeBaseView, PermissionKnowledgeBaseCreate, PermissionKnowledgeBaseUpdate, PermissionKnowledgeBaseDelete,
		PermissionKnowledgeDocumentView, PermissionKnowledgeDocumentCreate, PermissionKnowledgeDocumentUpdate, PermissionKnowledgeDocumentDelete,
		PermissionKnowledgeFAQView, PermissionKnowledgeFAQCreate, PermissionKnowledgeFAQUpdate, PermissionKnowledgeFAQDelete,
		PermissionSkillDefinitionView,
	},
	RoleCodeCsTeamLeader: {
		PermissionUserView,
		PermissionRoleView,
		PermissionPermissionView,
		PermissionDashboardView,
		PermissionServiceAnalyticsView, PermissionServiceAnalyticsExport,
		PermissionConversationRecordView, PermissionConversationRecordAnnotate, PermissionConversationRecordExport,
		PermissionQualityInspectionView, PermissionQualityInspectionManage, PermissionQualitySamplingCreate, PermissionQualityTemplateManage,
		PermissionConversationEvaluationView, PermissionConversationEvaluationInvite, PermissionReportViewPresetManage, PermissionAgentPresenceUpdate,
		PermissionConversationView, PermissionConversationAssign, PermissionConversationTransfer, PermissionConversationClose, PermissionConversationSend, PermissionConversationTag, PermissionConversationHandover, PermissionConversationRecycle, PermissionConversationLinkCustomer,
		PermissionTicketView, PermissionTicketCreate, PermissionTicketUpdate, PermissionTicketAssign, PermissionTicketChangeStatus, PermissionTicketProgress,
		PermissionNotificationView, PermissionNotificationUpdate,
		PermissionQuickReplyView, PermissionQuickReplyCreate, PermissionQuickReplyUpdate, PermissionQuickReplyDelete,
		PermissionTagView,
		PermissionChannelView, PermissionChannelCreate, PermissionChannelUpdate,
		PermissionCustomerView, PermissionCustomerCreate, PermissionCustomerUpdate,
		PermissionAgentView, PermissionAgentUpdate,
		PermissionAgentTeamView,
		PermissionAgentTeamScheduleView, PermissionAgentTeamScheduleCreate, PermissionAgentTeamScheduleUpdate, PermissionAgentTeamScheduleDelete, PermissionAgentTeamScheduleBatchGenerate,
		PermissionAssetView, PermissionAssetCreate, PermissionAssetDelete,
		PermissionAIAgentView,
		PermissionSkillDefinitionView,
	},
	RoleCodeCsUser: {
		PermissionUserView,
		PermissionRoleView,
		PermissionPermissionView,
		PermissionDashboardView,
		PermissionServiceAnalyticsView, PermissionServiceAnalyticsExport,
		PermissionConversationRecordView, PermissionConversationRecordExport,
		PermissionQualityInspectionView,
		PermissionConversationEvaluationInvite, PermissionReportViewPresetManage, PermissionAgentPresenceUpdate,
		PermissionConversationView, PermissionConversationSend,
		PermissionTicketView, PermissionTicketCreate, PermissionTicketAssign, PermissionTicketChangeStatus, PermissionTicketProgress,
		PermissionNotificationView, PermissionNotificationUpdate,
		PermissionQuickReplyView,
		PermissionTagView,
		PermissionChannelView,
		PermissionCustomerView,
		PermissionAssetView,
		PermissionAgentView,
		PermissionAgentTeamView,
		PermissionAgentTeamScheduleView,
		PermissionAIAgentView,
		PermissionSkillDefinitionView,
	},
	RoleCodeStoreStaff: {
		PermissionStoreWorkbenchView, PermissionStoreWorkbenchUpdate,
		PermissionBillingView, PermissionBillingExport,
	},
}

func NormalizePermissionScope(scope string) string {
	if scope == PermissionScopePlatform {
		return PermissionScopePlatform
	}
	return PermissionScopeTenant
}

func PermissionCodes() []string {
	ret := make([]string, 0, len(Permissions))
	for _, permission := range Permissions {
		ret = append(ret, permission.Code)
	}
	return ret
}

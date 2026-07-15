# 租户公司与注册体系验收证据

本文件记录 `docs/design/multi-tenant-company-registration.md` 第 16、17 节的逐条验收证据。产品逻辑仍以真实代码和设计文档为准；本文件不替代代码，也不记录临时密码、邀请码或签名密钥。

## 1. 验收基线

- 集成分支：`codex/tenant-ai-integration`
- 集成基线：`codex/customer-audit@c706815` + `origin/codex/ai-billing@f2d2da4`
- 数据审计基线：59 个 TenantID 模型、73 张必需表、154 条租户关系
- SQLite 验收库：`/tmp/agentdesk-integration.db`
- MySQL：独立临时 MySQL 8.4 实例，使用 tmpfs，不接触现有 Docker 数据卷
- 浏览器：本地 `http://127.0.0.1:8084`，桌面和 412x915 移动视口
- 公开注册：只在注册闭环验收期间于 `/tmp` 配置临时开启；验收后恢复 `tenantRegistration.enabled: false`

## 2. 第 16 节测试方案证据

### 2.1 权限测试

| 要求 | 直接证据 | 结果 |
| --- | --- | --- |
| 账号不能直接获得 UserPermission 例外权限 | `TestAuthServiceIgnoresLegacyAccountPermissionOverrides`；migration 34 在发现历史 override 时拒绝并保留原数据；用户页面源码契约不包含账号权限分配 | 通过 |
| 公司主管只能分配下级租户角色 | `TestTenantRegistrationReviewEnforcesTenantAndRoleAuthority`、`TestUserServiceAssignRolesEnforcesAuthority`；浏览器审核弹窗只显示客服组长、客服、门店员工 | 通过 |
| 管理员不能修改超级管理员角色 | `TestRoleAuthorityAssignmentMatrix`、`TestUserServicePrivilegedMutationsEnforceAuthority`、`TestRoleServiceDeleteRolePreservesSystemRole` | 通过 |
| 无 role.update 不能排序 | `TestRoleUpdateSortRequiresUpdatePermission`；客服账号直接请求 `/api/dashboard/role/update_sort` 返回 3001 | 通过 |
| 无动作权限时前端隐藏、后端拒绝 | 客服账号用户页无邀请、添加和操作列；直接请求创建账号返回 3001；`TestDashboardHandlersHaveExplicitPermissionContract` | 通过 |
| 仅有 view 时页面和必要信息可见 | 公司主管角色页显示完整层级但隐藏修改、排序和分配权限；客服用户页显示本公司账号但隐藏写操作 | 通过 |

### 2.2 租户隔离测试

| 要求 | 直接证据 | 结果 |
| --- | --- | --- |
| A 的账号 CRUD 不能触达 B | `TestUserTenantScopeAndCrossTenantManagement`；浏览器甲公司仅显示甲主管/甲客服，乙公司仅显示乙主管 | 通过 |
| A 不能给 B 分配会话、工单、门店或客服组 | `TestConversationRuntimeRejectsCrossTenantOperations`、`TestConversationDispatchAndFinalWritesStayInTenant`、`TestTicketAndTagRuntimeTenantIsolation`、`TestStoreStaffAssignmentsAndWxWorkScopeStayInActiveTenant`、`TestAgentOrganizationTenantIsolationRejectsIDTampering` | 通过 |
| A 不能绑定 B 的知识库、渠道和企微员工号 | `TestKnowledgeRuntimeTenantIsolation`、`TestCompanyAndChannelServicesRequireActiveTenant`、`TestAIAgentServiceRejectsCrossTenantReferencesAndBindings`、`TestWxWorkProtocolInstanceCRUDStaysInActiveTenant` | 通过 |
| URL、body ID、query ID、请求头不能越权 | `TestResolveAuthPrincipalEnforcesTenantContext`、`TestAuthenticateUsesPerRequestTenantHeader`；主管直接访问平台 URL 被退回；客服伪造 X-Tenant-ID 返回 3001 | 通过 |
| 超管切换 A/B 只见当前租户数据 | 浏览器分别切换甲、乙上下文并读取用户列表；甲为 2 条、乙为 1 条且无交叉 | 通过 |
| 两个标签页选择不同公司互不覆盖 | 标签页 A 选择甲、标签页 B 选择乙；B 切换后 A 重载仍显示甲上下文和甲数据 | 通过 |

### 2.3 注册测试

| 要求 | 直接证据 | 结果 |
| --- | --- | --- |
| 有效邀请码创建正确租户的待审核账号 | `TestTenantRegistrationCreatesPendingRolelessAccountAndReplaysExactly`；浏览器邀请链接识别甲公司并生成待审核账号 | 通过 |
| 无效、过期、重置旧码和停用公司邀请码不能注册 | `TestTenantRegistrationValidateInvitationTracksCurrentLifecycle`；浏览器重置邀请码后旧链接立即显示无效 | 通过 |
| tenantId、roleIds 不能由注册者控制 | `TestTenantRegistrationRejectsCallerControlledScope` | 通过 |
| 重复提交只创建一个账号 | `TestTenantRegistrationConcurrentReplayCreatesOneAccount` 在 race 下通过 | 通过 |
| 未审核账号不能访问后台 | `TestResolveAuthPrincipalRejectsPendingAndInconsistentAccounts`；浏览器审核前登录返回统一认证失败 | 通过 |
| 审核和分配角色后权限生效 | 浏览器主管分配客服角色并通过审核，账号随后可登录；写操作隐藏且后端返回 3001 | 通过 |

### 2.4 运行链路测试

| 要求 | 直接证据 | 结果 |
| --- | --- | --- |
| WebSocket 不跨租户推送 | `TestWsTenantTopicsAndConversationSubscriptionIsolation` | 通过 |
| 第三方回调从 Channel/企微实例反查租户 | `TestWxWorkKFRuntimeTenantIsolation`、`TestWxWorkProtocolActionsRejectCrossTenantInstanceBeforeProtocolCall`、`TestExternalCustomerIdentityIsSeparatedByChannelTenant` | 通过 |
| Outbox worker 不跨租户读取或投递 | `TestWxWorkKFRuntimeTenantIsolation` 覆盖创建、claim 和脏 outbox fail closed；`TestWxWorkCLIBridgePollsOnlyChannelTenant` 覆盖轮询和完成 | 通过 |
| 知识检索同时按 tenantId 和 knowledgeBaseId 过滤 | `TestKnowledgeRetrieverFiltersKnowledgeBasesByAgentTenant`、`TestKnowledgeRuntimeTenantIsolation` | 通过 |
| 文件 URL 和缓存/对象 key 不跨租户复用 | `TestAssetRuntimeTenantIsolation`、`TestAssetStoragePrefixIncludesTenant`、`TestAssetAccessURLRefreshPreservesTenantBoundary`、`TestConversationMemoryUpdatePersistsAndReadsWithinTenant` | 通过 |

### 2.5 工程验证

| 项目 | 证据 | 结果 |
| --- | --- | --- |
| Go 单元与集成测试 | `go test ./... -count=1` | 通过 |
| Go 静态检查 | `go vet ./...` | 通过 |
| 关键并发路径 | 注册、角色、公司、客服小组/派单、租户运行链、AI Runtime 使用 `go test -race ... -p 1` | 通过 |
| 前端类型 | `pnpm --dir web typecheck` | 通过 |
| 前端 lint | `pnpm --dir web lint`，0 error，保留 33 个 warning | 通过 |
| 前端生产构建 | `pnpm --dir web build`，46 个页面生成完成 | 通过 |
| SQLite | 真实验收库只读 tenant-integrity-audit 为 59/73/154、0 违规 | 通过 |
| MySQL | MySQL 8.4 首次/重复 migration、复合唯一索引和只读审计通过；同租户重复键被 1062 拒绝，跨租户同键允许 | 通过 |
| 浏览器桌面/移动端 | 公司创建、编辑、启停、主管、邀请、注册、审核、权限、双标签、412x915 布局 | 通过 |

## 3. 第 17 节验收标准证据

| 验收标准 | 直接证据 | 结果 |
| --- | --- | --- |
| 接入公司可创建、编辑、启停并生成主管 | 浏览器创建甲/乙公司、编辑甲地址、停用后恢复启用；创建结果同时返回主管、默认综合客服组和邀请码 | 通过 |
| 公司主管角色在角色和权限管理完整可见 | 角色页显示 tenant_admin，权限页可检索 tenantInvite 与 tenantRegistration 权限 | 通过 |
| 公司主管管理本租户但不能跨租户或配置平台权限 | 主管完成邀请审核；平台公司页直达被退回；角色页只读且无权限分配 | 通过 |
| 邀请码可查看、复制、重置并完成链接注册 | 邀请弹窗显示邀请码/链接/版本；重置后版本递增且旧码失效；新链接完成注册 | 通过 |
| 邀请账号归属正确租户、默认无角色并待审核 | 浏览器注册后不可登录，主管审核页显示待审核；service 测试断言 roleless 和租户归属 | 通过 |
| 账号页面只分配角色，不能直接分配权限 | 用户页和表单无 permissionIds/账号权限入口；`action-permissions.test.mjs` 提供源码契约 | 通过 |
| 所有业务权限在权限管理可见，无 URL 白名单或账号例外 | 权限页展示 111 个权限；`TestDashboardHandlersHaveExplicitPermissionContract` 和 override 忽略测试 | 通过 |
| 查看权限保守显示，动作权限隐藏 | 公司主管角色页、客服用户页浏览器实测 | 通过 |
| 列表、详情、写、导出、WebSocket、回调、任务、向量和文件通过双租户隔离 | service/handler/race 套件与 59/73/154 审计组合覆盖；具体测试见第 2 节 | 通过 |
| 旧渠道页被替换，Channel 消息链保持可用 | `/dashboard/channels` 展示接入公司；租户 Channel、Conversation、WxWork 和 Outbox 测试继续通过 | 通过 |
| 历史数据完成租户映射，无未确认账号开放到多租户 | migration 34-57 回填测试；浏览器写入后真实 SQLite 审计 0 违规 | 通过 |
| SQLite、MySQL、Go、前端类型和关键浏览器流程通过 | 第 2.5 节全部通过 | 通过 |

## 4. 本批修复与风险

- 修复 React 19 全量 lint 的 5 个 error：编辑器 actions 改为不可变构造；全屏不再用同步 mounted effect；主题和语言改为 `useSyncExternalStore`。存储键和产品交互不变。
- 修复 `fakeKnowledgeContextRetriever` 在并发检索测试中的记录切片数据竞争。仅修改测试替身，不改变 AI 检索并发、模型调用、回复逻辑、token 或计费。
- 全量 lint 仍有 33 个 warning，主要是既有图片优化、Hook dependency 和 minified SDK 告警；不阻断本次验收，但后续应单独治理。
- 本地验收数据保留在 `/tmp/agentdesk-integration.db` 供复查，不进入 Git。公开注册已恢复关闭。

## 5. 并行分支与回滚

- `internal/ai/runtime/executor/answerability_gate_test.go` 来自 AI 分支范围，本批只增加测试互斥；合并 AI 分支后应保留该测试修复。
- 四个前端 lint 文件属于共享前端基础设施；不涉及 models、migration、DTO、enum、API、路由、WebSocket 或权限码。
- 回滚前端修复会恢复全量 lint 失败；回滚测试互斥会恢复 race 失败。两类回滚都不需要数据库操作。

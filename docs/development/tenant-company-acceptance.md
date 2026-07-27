# 租户公司与注册体系验收证据

本文件记录 `docs/design/multi-tenant-company-registration.md` 第 16、17 节的逐条验收证据。产品逻辑仍以真实代码和设计文档为准；本文件不替代代码，也不记录临时密码、邀请码或签名密钥。

## 1. 验收基线

- 集成分支：`codex/tenant-ai-integration`
- 集成基线：`codex/customer-audit@c706815` + `origin/codex/ai-billing@f2d2da4`
- 数据审计基线：61 个 TenantID 模型、74 张必需表、153 条租户关系
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
| 无效、过期、重置旧码和停用公司邀请码不能注册 | `TestTenantRegistrationValidateInvitationTracksCurrentLifecycle` 现在分别覆盖过期码的 Validate/Register 拒绝、旧码和停用公司；`TestTenantInvitationUsableAtFailsClosedForMissingOrExpiredDeadline` 覆盖缺失/过去/未来边界；浏览器确认过期码不可复制且重置后恢复 | 通过 |
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
| 关键并发路径 | 注册、角色、公司、客服小组/派单、租户运行链、AI Runtime 使用 `go test -race ... -p 1`；同请求并发注册修复后定向连续 20 次通过 | 通过 |
| 前端类型 | `pnpm --dir web typecheck` | 通过 |
| 前端 lint | `pnpm --dir web lint`，0 error，保留 33 个 warning | 通过 |
| 前端生产构建与契约 | `pnpm --dir web build` 生成 45 个页面，不再生成 `/dashboard/ai-agents`；50 个 `.test.mjs` 文件共 130 个用例通过 | 通过 |
| SQLite | 真实验收库只读 tenant-integrity-audit 为 61/74/153、0 违规；migration 59 和 seed 重跑后仍保持仿真业务基线 | 通过 |
| MySQL | MySQL 8.4 首次/重复 migration、复合唯一索引和 61/74/153 只读审计通过；migration 58 的邀请码回填与 migration 59 的模型授权迁移均完成首次、重复执行验证 | 通过 |
| 浏览器桌面/移动端 | 公司创建、编辑、启停、主管、邀请、注册、审核、权限、双标签、412x915 布局；过期邀请码提示/禁用复制/重置恢复均实测 | 通过 |

## 3. 第 17 节验收标准证据

| 验收标准 | 直接证据 | 结果 |
| --- | --- | --- |
| 接入公司可创建、编辑、启停并生成主管 | 浏览器创建甲/乙公司、编辑甲地址、停用后恢复启用；创建结果同时返回主管、默认综合客服组和邀请码 | 通过 |
| 公司主管角色在角色和权限管理完整可见 | 角色页显示 tenant_admin，权限页可检索 tenantInvite 与 tenantRegistration 权限 | 通过 |
| 公司主管管理本租户但不能跨租户或配置平台权限 | 主管完成邀请审核；平台公司页直达被退回；角色页只读且无权限分配 | 通过 |
| 邀请码可查看、复制、重置并完成链接注册 | 邀请弹窗显示邀请码/链接/版本；重置后版本递增且旧码失效；新链接完成注册 | 通过 |
| 邀请账号归属正确租户、默认无角色并待审核 | 浏览器注册后不可登录，主管审核页显示待审核；service 测试断言 roleless 和租户归属 | 通过 |
| 账号页面只分配角色，不能直接分配权限 | 用户页和表单无 permissionIds/账号权限入口；`action-permissions.test.mjs` 提供源码契约 | 通过 |
| 所有业务权限在权限管理可见，无 URL 白名单或账号例外 | 权限页展示 113 个权限；59 号迁移已删除 3 项退役的 AIAgent 写权限；`TestDashboardHandlersHaveExplicitPermissionContract` 和 override 忽略测试 | 通过 |
| 查看权限保守显示，动作权限隐藏 | 公司主管角色页、客服用户页浏览器实测 | 通过 |
| 列表、详情、写、导出、WebSocket、回调、任务、向量和文件通过双租户隔离 | service/handler/race 套件与 61/74/153 审计组合覆盖；具体测试见第 2 节 | 通过 |
| 旧渠道页被替换，Channel 消息链保持可用 | `/dashboard/channels` 展示接入公司；租户 Channel、Conversation、WxWork 和 Outbox 测试继续通过 | 通过 |
| 历史数据完成租户映射，无未确认账号开放到多租户 | migration 34-59 回填测试；浏览器写入后真实 SQLite 审计 0 违规 | 通过 |
| SQLite、MySQL、Go、前端类型和关键浏览器流程通过 | 第 2.5 节全部通过 | 通过 |

## 4. 本批修复与风险

- 复核发现此前第 2.3 节把“过期邀请码”并入生命周期测试，但当时模型没有 `ExpiresAt`，测试也未构造过期条件，属于验收假阳性。现已增加 `ExpiresAt`、90 天策略、migration 58、Validate/Register 双重拒绝测试和真实浏览器证据；不再沿用旧结论。
- 历史启用邀请码从 migration 58 执行时起获得 90 天有效期；缺失到期时间在运行时 fail closed。已失效记录不回填，既有到期值不覆盖，避免迁移把旧码重新激活或延长。
- 过期邀请码仍可由有 `tenantInvite.view` 的公司主管查看，用于确认原因；复制入口禁用，只有既有 `tenantInvite.rotate` 可生成新码。没有新增隐藏权限或账号级权限。
- 宽范围 race 首次暴露 SQLite 的六路并发注册可能全部发生锁升级死锁。注册/审核写事务现仅在 SQLite 驱动下进程内串行，MySQL 行锁路径不变；`TestTenantRegistrationConcurrentReplayCreatesOneAccount` 需以 race 多次复跑作为门禁。
- 修复 React 19 全量 lint 的 5 个 error：编辑器 actions 改为不可变构造；全屏不再用同步 mounted effect；主题和语言改为 `useSyncExternalStore`。存储键和产品交互不变。
- 修复 `fakeKnowledgeContextRetriever` 在并发检索测试中的记录切片数据竞争。仅修改测试替身，不改变 AI 检索并发、模型调用、回复逻辑、token 或计费。
- 全量 lint 仍有 33 个 warning，主要是既有图片优化、Hook dependency 和 minified SDK 告警；不阻断本次验收，但后续应单独治理。
- 本地验收数据保留在 `/tmp/agentdesk-integration.db` 供复查，不进入 Git。公开注册已恢复关闭。

## 5. 并行分支与回滚

- `internal/ai/runtime/executor/answerability_gate_test.go` 来自 AI 分支范围，本批只增加测试互斥；合并 AI 分支后应保留该测试修复。
- `internal/models/models.go` 是 AI 分支共享文件；合并时同时保留 `TenantInvitation.ExpiresAt`、`TenantAIModelGrant` 及双方模型注册。响应 DTO 和 `web/lib/api/tenant.ts` 的兼容字段不能被整文件覆盖。
- migration 58、59 已在 SQLite 和独立 MySQL 8.4 验证。回滚代码时保留 `expires_at`、模型授权表与已迁移关系，不删除 migration 记录或恢复旧明文模型凭据；公开注册继续关闭。
- 四个前端 lint 文件属于共享前端基础设施；不涉及 models、migration、DTO、enum、API、路由、WebSocket 或权限码。
- 回滚前端修复会恢复全量 lint 失败；回滚测试互斥会恢复 race 失败。两类回滚都不需要数据库操作。

## 6. 合成验收酒店测试租户验收（2026-07-15）

### 6.1 数据与模型绑定

| 验收项 | 直接证据 | 结果 |
| --- | --- | --- |
| 合成验收是独立测试租户 | seed report 中 tenant=1；接入公司页面显示“合成验收测试 / 合成验收酒店” | 通过 |
| 公司主管与邀请基础存在 | tenantSupervisor=1、tenantInvitation=1、defaultAgentTeam=1；主管账号为 `test_customer_audit_tenant_admin` | 通过 |
| 全部测试数据归属同租户 | 61/74/153 tenant-integrity-audit 0 违规；公司、账号、客户、门店、企微、会话、派单和模型授权均按 TenantID 查询 | 通过 |
| 原模型配置被复用 | 本地隔离库安全复制原环境启用 `deepseek / deepseek-v4-flash`；seed 建立 TenantAIModelGrant 和租户用途默认，内部 AIAgent 的 `AIConfigID=0` | 通过 |
| 模型密钥不进入仓库 | seed 只接受已有配置 ID/名称；report 不返回 API Key；源码和文档不包含密钥 | 通过 |
| 全部主体明确为测试 | 租户、主管、内部接待策略、客服组、员工、门店、客户、渠道和企微备注包含测试/仿真测试标记 | 通过 |

### 6.2 仿真业务基线

| 数据 | 数量/状态 | 浏览器证据 |
| --- | --- | --- |
| 租户账号 | 116 | 用户管理显示共 116 条，门店员工账号及企微/客服组归属可见 |
| 客服组织 | 默认综合组 1、业务组 3、客服 12 | 客服档案显示 34/33/33 个员工号覆盖，12 个客服负载可见 |
| 门店与企微 | 100/100 | 接入公司资源统计门店 100；用户与客服组页面显示企微员工号来源 |
| 客户 | 500 | 客户管理显示测试顾客和合成验收酒店/门店关系 |
| 会话与消息 | 36/135 | seed report 基线稳定，Conversation 均写入租户级测试 AIAgentID |
| 派单 | assignment 21、需人工 27 | 派单页显示待派发 9、待首响 12、处理中 6、可接单客服 12 |
| 内部接待策略 | 1 | 渠道和 Skill 调试选择器显示“合成验收酒店仿真测试接待策略”；租户不再看到独立智能客服管理页或平台模型参数 |

### 6.3 安全、幂等与回滚

- 原 8083 Docker MySQL 只读，旧合成验收 `t_company` 未删除或迁移；8084 SQLite 写入前备份为 `/tmp/agentdesk-integration.pre-lissi-20260715.db`。
- fresh SQLite 生命周期测试覆盖 seed、report、重复 seed 和 cleanup。重复执行数量不变；cleanup 后测试租户及子数据为 0，平台复用 AIConfig 仍为 1。
- 当前 8084 验收库执行 seed 两次后仍保持 36/135/21 和 0 租户违规。公开注册保持 `tenantRegistration.enabled=false`。
- 本地数据库、邀请码、测试密码和 API Key 不进入 Git。生产合并前必须按交接文档在原 MySQL 的临时恢复副本处理 migration 39 冲突，禁止直接在原数据卷试迁移。

## 7. 平台模型授权与原功能回归（2026-07-16）

### 7.1 模型边界

- 平台“模型配置”继续管理 AIConfig 和凭据；接入公司三点菜单可为租户多选授权模型并设置用途默认。租户主管看不到平台模型配置、模型授权、运行日志、token 或 trace。
- 平台管理员进入租户后，企微员工号“模型分配”只列出该租户已授权模型。在线解析顺序为员工号覆盖、租户用途默认、同类型授权兜底，运行时不再读取 `AIAgent.AIConfigID`。
- `/dashboard/ai-agents` 页面、导航和写接口已经退役；静态服务器对未知旧路径会回落公共首页，但构建产物、后台入口和对应管理 API 均不存在。渠道所需的内部身份只通过 `/api/dashboard/ai-agent/list_all` 只读选择。
- 租户总览删除了与“会话管理”重复的“员工号智能客服”快捷项；原平台“接入渠道”快捷项改为当前租户的“接入设置”，避免公司主管进入无权限的平台页面。

### 7.2 原有业务回归

| 原功能 | 浏览器与数据证据 | 结果 |
| --- | --- | --- |
| 渠道设置 | 编辑渠道仍可加载内部接待策略选择器，保存契约未改 | 通过 |
| 客户会话 | 加载 36 个仿真会话和 135 条消息；“全部账号”中选中客户后保持全部会话视图，并高亮来源企微员工号 | 通过 |
| 人工派单 | 9 条待派发、12 条待首响、6 条处理中；12 名可用客服和手动派发候选正常加载 | 通过 |
| 自动派单 | 当前无生效排班时明确提示无可用排班客服，不产生错误指派 | 通过 |
| 客服档案与小组 | 3 个业务组各 4 名客服，负载、34/33/33 服务范围、小组编排和拖拽入口正常 | 通过 |
| 客服排班 | 月历、班次和批量排班弹窗正常加载 | 通过 |
| Skill 与知识库 | 调试弹窗继续解析内部接待策略；知识检索和 RAG 调试正常加载，租户诊断信息按权限脱敏 | 通过 |

- 权限页中 `aiAgent.view` 只显示“查看接待策略选项”，API 为 `/api/dashboard/ai-agent/list_all`；已删除的三项 AIAgent 写权限不再存在。
- 本轮不修改模型供应商适配器、AI usage、token 统计或计费口径。原有会话、派单、客服组、排班、知识和 Skill 状态机不增加平行模型或权限分支。

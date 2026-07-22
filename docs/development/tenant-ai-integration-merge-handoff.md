# Tenant AI Integration 唯一合并交接

> 状态日期：2026-07-22
>
> 唯一工作分支：codex/tenant-ai-integration
>
> 唯一目标：通过 PR #2 合并到 main
>
> PR：https://github.com/520skyincloud/agentdesk/pull/2
>
> 冻结来源：codex/customer-audit
>
> 并行参考：codex/ai-billing

本文只记录当前可执行事实。历史阶段、旧失败、旧测试数字和已经关闭的阻断不再按时间堆叠；需要追溯时使用 Git 历史。代码与本文冲突时先追踪真实运行链，再更新本文。

## 1. 合并决策

1. 租户、账号、客服、派单、运营分析、人工质检、企微员工号、知识库和 AI 集成统一在 codex/tenant-ai-integration 完成。
2. codex/customer-audit 已冻结，当前提交 c706815 是 integration 的祖先。不得继续在该分支修复后再二次搬运。
3. codex/ai-billing 保留为并行语义来源，但其当前提交不是 integration 的祖先；不得整分支 merge、整文件覆盖或重复 cherry-pick。
4. 主线只合并 PR #2。旧 customer-audit PR 不作为第二合并入口。
5. cmd/customer_audit_seed 只是历史命名的仿真命令，不表示仍有 customer-audit 开发分支。
6. 不提交 docs/generated/、.codex/audits/、截图、临时数据库、密钥或本地配置。

2026-07-22 本轮交接更新前 fetch 的拓扑：

    origin/main                         e67e207  -> integration 祖先
    origin/codex/customer-audit         c706815  -> integration 祖先
    origin/codex/tenant-ai-integration  886dea7  -> 本轮 push 前远端基线
    origin/codex/ai-billing             4db7993  -> 与 integration 在 f2d2da4 后分叉
    integration 最后一个代码提交        886dea7

SHA 只是 push 前快照，不表示当前 PR 头仍停在这些提交。每次后续 push 或 merge 前仍必须重新执行 `git fetch origin --prune` 并复核。

## 2. 权威文档

| 领域 | 当前依据 |
| --- | --- |
| AI 回复运行时 | 真实代码、docs/design/reply-runtime-engine.md |
| 租户、注册、权限和隔离 | docs/design/multi-tenant-company-registration.md |
| 门店身份、企微绑定和客服组范围 | docs/design/wxwork-managed-store-scope-implementation.md |
| 规则派单与班次恢复 | docs/design/conversation-dispatch-engine.md |
| 运营分析和人工质检 | docs/design/service-analytics-and-quality.md |
| FastGPT 托管门店知识 | docs/design/fastgpt-managed-store-knowledge.md |
| 本次主线合并 | 本文件 |

docs/development/customer-audit-merge-handoff.md、docs/wecom-hook-bridge.md 和 docs/generated/ 是历史资料，不能恢复旧 FAQ、七鱼、hook bridge、独立 Agent、旧企微字段或旧转人工逻辑。

## 3. 最终产品语义

### 3.1 租户和账号

    平台管理员
      -> 创建 Tenant
      -> 创建首个 tenant_admin 公司主管

    公司主管
      -> 直接创建本公司 User
      或发送邀请注册链接
      -> 审核注册 User
      -> 给 User 分配已有 Role

- 邀请码只确定 TenantID，不携带角色、权限、门店或客服组。
- 平台管理员只负责 Tenant 和首个公司主管；租户后续账号由公司主管管理。
- User 只绑定 Role，Role 绑定 Permission。不存在账号级隐藏权限。
- 公开注册默认关闭；启用前必须配置独立邀请码加密密钥并完成隔离验收。
- “邀请开户、远程开户、门店开户注册”均不是产品概念。

### 3.2 门店和企微员工号

    User
      -> store_staff 角色
      -> StoreStaffBinding
      -> Store
      -> WxWorkProtocolInstance

- 不存在独立“门店账号”实体。
- 一个启用的 store_staff User 在当前产品阶段代表一家门店。
- 首次分配角色必须填写门店名称，并在角色事务中创建唯一 Store + Binding。
- 重新分配角色复用原 Store + Binding，不创建第二套身份。
- 移除角色或停用账号会停用 Store、Binding、相关企微实例和 AI 自动回复。
- 企微扫码、企微 OAuth 和绑定链接只绑定已有账号，不创建 User、不赋角色。
- 异地页面叫“企微员工号绑定链接”；兼容 URL 仍为 /wxwork-remote-setup，URL 名称不代表开户。

### 3.3 租户内组织

- Tenant 下直接管理多家 Store，不存在活跃的“客户企业 -> 门店”中间层。
- StoreStaffBinding.AgentTeamID 是门店员工归属综合客服组的事实源。
- 用户管理支持单个反向绑定客服组；客服组编辑支持双列筛选和批量绑定。
- 门店员工不固定分给某个客服个人；人工任务进入客服组，再由小组、排班和派单规则分配。

### 3.4 历史 Company

Company 已从产品运行时退役：

- 无 Dashboard API、页面、选择器、导航和 company.* 权限。
- /dashboard/companies 与 /dashboard/company-detail/* 服务端 307 跳转到 /dashboard/。
- Customer、Knowledge、ReplyIntent 和门店运行 DTO 不再暴露 Company。
- 新写入的 Store、Binding、WxWork、门店知识和门店模型设置使用 CompanyID=0。
- AI 回复、路由、派单、知识选择和模型授权不再读取 Company。
- model/repository 仅为历史 migration、审计和旧 usage 证据保留。

## 4. 当前实现范围

### 4.1 多租户和权限

- Tenant 创建、核验、启停、平台切换和租户统计。
- 首个公司主管、邀请码、公开注册、审核和角色分配。
- 平台/租户上下文、后端租户过滤、异步、WebSocket 和外部入口隔离。
- 角色权限审计、角色层级、依赖保护和登录会话撤销。
- 权限驱动导航；页面隐藏不替代 Handler 权限和 Service 数据范围。
- 只读租户完整性审计覆盖注册模型、必需表和父子关系。

### 4.2 客服组织和派单

- 综合客服组、客服小组、拖拽成员、排班及双向门店员工范围。
- 人工任务池、待派、已派、处理中、超时、手动派单、转派和释放。
- 派单模式只保留 `manual/rule`；模型选择客服已从运行时、配置页和模型用途删除。
- 规则候选过滤、Presence、容量、实时压力、本班公平债务、排班小组、替班/请假和历史连续性。
- 800ms 实时防抖、30 秒周期补偿、组队列连续消化和事务内状态复核。
- 首响前支持班次/在线/权限/容量失效恢复；人工回复后仅在客户追问超过 Response SLA 且原客服硬不可用时有界接力。两阶段均包含事务复核、90 秒冷却和最多三次自动重派。
- Queue SLA 与 First Response SLA 分离；路由 `ManualExpireAt` 不再作为派单 SLA。
- 派单结果、候选证据、规则理由、人工覆盖、恢复和失败原因可审计。
- 历史 `intelligent` Assignment、置信度、DecisionLog 和 usage 只读保留，不能恢复为当前配置能力。

### 4.3 运营分析和人工质检

- 总览区分当前快照与今日累计。
- 运营分析包含服务总览、响应效率、客服表现、质检与满意度、派单质量和来源分析。
- 会话事实按 conversationId + sessionNo 固化，响应等待使用 ResponseSpan。
- 会话记录统一筛选、详情、导出、保存视图和服务小记。
- 人工质检只评价目标 Assignment 分段内的人工回复；AI 和客户消息仅作上下文。
- Presence、满意度邀请/提交、随机抽样、模板版本和禁忌项均有独立事实。
- 公司主管看全租户，组长看负责客服组，客服看本人。

### 4.4 模型和知识边界

    AIConfig（平台凭证）
      -> TenantAIModelGrant（租户授权集合）
      -> StoreAIModelSetting（租户默认用途）
      -> 员工号覆盖（仅能选择已授权模型）

- 租户用户看不到平台供应商密钥。
- 平台管理员进入活动 Tenant 后按权限配置租户模型用途。
- FastGPT 门店 Team、Dataset、同步状态和门店模型 Profile 已接入 Store 身份。
- 本分支不修改 Token、usage、价格、余额和计费公式。

### 4.5 企微协议

- 企微员工号协议唯一依据 https://wework.apifox.cn/llms.txt 及其具体接口页。
- 登录二维码、状态轮询、确认码、资料同步和更换员工号已形成闭环。
- 新实例必须使用协议平台真实 GUID，不生成 pending_* 或本地假 GUID。
- 消息发送使用 conversation_id；单聊 S:，群聊 R:。
- 文档未提供群列表接口，因此当前不提供假群选择器。

## 5. 关键代码位置

| 能力 | 主要文件 |
| --- | --- |
| Tenant 创建、邀请、注册 | internal/services/tenant_management_service.go、tenant_registration_business_service.go |
| User 与角色事务 | internal/services/user_service.go |
| 门店身份 | internal/services/store_staff_binding_service.go、store_identity_lifecycle_service.go |
| 企微绑定 | internal/services/wx_work_protocol_instance_service.go、wxwork_login_service.go |
| 用户管理页面 | web/app/dashboard/users/* |
| 企微绑定页面 | web/components/wxwork-protocol/wxwork-protocol-binding-dialog.tsx |
| Company 退役 | internal/migration/000063_retire_legacy_company_store_scopes.go、internal/bootstrap/server.go |
| 租户完整性 | internal/services/tenant_integrity_audit_service.go、cmd/tenant_integrity_audit |
| 规则派单与恢复 | internal/services/conversation_dispatch_*_service.go |
| 排班与临时人员 | internal/services/agent_team_schedule_service.go、web/app/dashboard/agent-team-schedules/* |
| 运营事实、质检 | internal/models/service_analytics.go、internal/services/service_analytics_*_service.go |
| 丽斯未来仿真 | cmd/customer_audit_seed/* |

## 6. Migration

### 6.1 权威版本

| 版本 | integration 语义 |
| --- | --- |
| 60 | 同步运营分析、质检、满意度和 Presence 权限 |
| 61 | 回填服务轮次与分析事实 |
| 62 | 迁移企微接待人设 |
| 63 | 退役旧 Company 运行范围并修复门店身份 |
| 64 | 退役模型派单、历史智能组转规则、关闭无容量自动接单 |
| 65 | 同步客服回复与派单权限、更新现行 API 路径并补齐客服组长派发/转派 |

DDL 继续由 AutoMigrate 完成；以上 DML 由 migration runner 幂等执行。

### 6.2 并行冲突

codex/ai-billing 当前使用 migration 60-67，integration 已使用 60-65。两边相同编号的语义并不相同：

| 版本 | integration | ai-billing |
| --- | --- | --- |
| 60 | 运营分析与质检权限 | 企微接待人设 |
| 61 | 运营分析事实回填 | FastGPT Chat/ASR 模板回填 |
| 62 | 企微接待人设 | FastGPT gateway 规范化 |
| 63 | 退役 Company 运行范围 | 动态模型 Profile |
| 64 | 退役模型派单 | 酒店客户标签种子 |
| 65 | 客服回复与派单权限 | 修复酒店标签分类 |
| 66 | 未使用 | 生产化客户标签 |
| 67 | 未使用 | 退役旧会话标签 |

integration 已按当前 Tenant、Store 和平台模型授权语义承接 ai-billing 60-63 的业务结果，不能再次复制这些 migration。最新标签工作若后续选择性吸收，必须在重新 fetch 全部活跃分支后使用未占用的新版本，并以 integration 现有表和数据为输入重写 64-67；不得重编号或删除已经进入 integration 的 60-65。

integration 的 migration definition mismatch 校验、历史 migration archive/remap 支持、命令失败退出码和测试必须保留。不得用 ai-billing 当前 migration runner 整文件覆盖。

创建任何后续 migration 前必须重新核对 origin/main、origin/codex/ai-billing 和其他活动分支。

### 6.3 Migration 63 风险

Migration 63 会：

- 清空活跃门店链路上的旧 CompanyID。
- 禁用旧 Company 范围意图。
- 删除 company.* 权限和角色关系。
- 停用无有效 User/角色、跨租户或重复的 Binding。
- 为已有有效 store_staff User 回填缺失 Store + Binding。

发布前必须备份数据库并在副本演练。简单回滚应用代码不能恢复已清理的旧 Company 权限和范围。

### 6.4 Migration 64/65 边界

Migration 64 只调整派单配置：

- `AgentTeam.DispatchMode=intelligent` 幂等改为 `rule`。
- `StoreAIModelSetting.usage_code=dispatch_decision_llm` 软删除，不删除平台 AIConfig、租户授权、回复/意图/媒体模型配置和历史 usage。
- `AutoAssignEnabled=true` 但 `MaxConcurrentCount<=0` 的客服关闭自动接单，避免不可执行配置。

历史 Assignment、DecisionLog、置信度和 usage 不改写。Migration 65 复用现有权限定义和角色关系，补齐客服实际回复及客服组长编排所需权限并更新权限管理中的现行派单路径，不建立隐藏授权。

## 7. ai-billing 语义映射

origin/codex/ai-billing 在共同基点 f2d2da4 后，前七个提交已按当前租户和门店身份语义重做进入 integration：

| ai-billing | integration |
| --- | --- |
| f43a873 handoff/persona contract | f404be4 |
| 86a5cdf manual timeout recovery | 9a5a68e |
| 377597a reception persona | 2fbd948 |
| a89115d QR verification | 7026969 |
| c997f5c multi-task replies | 14cb208 |
| 9175e05 managed FastGPT workspaces | d2e58ae |
| 33b6d14 store dataset model profiles | 3ff376e |

这些不是 patch-equivalent 提交，因为 integration 同时加入 Tenant、StoreStaffBinding、权限、migration 62/63 和 Company 退役调整。后续审查应比较行为和测试，不应再次 cherry-pick 右侧已经承接的左侧提交。

最新两个提交尚未吸收：

| ai-billing | 状态 | 决策 |
| --- | --- | --- |
| 3538c8d store-scoped model credentials and tag evolution | 124 个文件，新增约 1.2 万行 | 禁止直接合并；模型凭据所有权与当前平台统一管理设计冲突 |
| 4db7993 productionize customer tag workflows | 51 个文件，新增约 3600 行 | 禁止直接合并；需在 Tenant 标签契约上选择性重做 |

完整冲突审计和后续吸收顺序见第 20 节。在该工作完成前，ai-billing 只作为代码与测试语义来源，不是可合并分支。

## 8. 丽斯未来仿真

### 8.1 静态 Seed 基线

停止后台定时任务后执行 cleanup -> seed -> report：

    Tenant                         1
    Tenant supervisor              1
    Tenant invitation              1
    Legacy Company rows            0
    Legacy Company references      0
    Store                          100
    store_staff User               100
    StoreStaffBinding              100
    WxWorkProtocolInstance         100
    Customer                       500
    StoreCustomerRelation          801
    Conversation                   36
    Message                        135
    Assignment                     21
    Pending                        9
    ServiceSession                 36
    ResponseSpan                   39
    QualityInspection              9
    Evaluation                     9
    DispatchDecisionLog            30

expectedCoreComplete=true、expectedSimulationComplete=true、simulationBaselineIntact=true。

### 8.2 运行后动态状态

启动服务且测试客服 Presence 仍有效时，规则派单会继续消费 9 条待派任务。历史观察基线为：

    Assignment                     30
    Currently assigned             27
    Pending                        0
    DispatchDecisionLog            39

此时静态基线标志变为 false 是预期行为，表示后台任务已经推进业务状态，不表示 Seed 损坏。文档和测试必须分别写“静态造数基线”和“运行后动态状态”。

### 8.3 清理修复

82abb92 修复 Seed cleanup 遗漏 ReportViewPreset 的问题，并新增：

- 删除目标租户个人报表视图。
- 删除必须发生在 User/Tenant 之前。
- 其他租户报表视图保持不变。
- 生命周期重复 Seed 与 cleanup 回归。

修复前本地库的两条旧孤儿已经在确认 Tenant/User 父记录均不存在后定点清理；没有加入全库删除孤儿的产品规则。

## 9. 当前验收证据

### 9.1 数据和后端

    PASS cmd/customer_audit_seed 全包测试
    PASS TestFreshDatabaseSeedLifecycle
    PASS TenantIntegrityAudit 定向测试
    PASS 干净 MySQL cleanup -> seed -> report
    PASS MySQL TenantIntegrityAudit

    registeredTenantModels  76
    policyCount             76
    requiredTables          89
    checkedTables           89
    configuredRelations     207
    checkedRelations        207
    violations              0

### 9.2 浏览器

平台管理员进入丽斯未来租户后已验证：

- 接入公司显示客服 12、门店 100、客服组 4。
- 用户管理共 116 个本租户账号；100 个门店员工均显示门店、企微和客服组。
- 添加用户选择“门店员工”后出现必填门店名称。
- 邀请注册明确“注册后仍需审核并分配角色”；注册关闭时明确禁止发送链接。
- 企微页显示 100 个实例及其系统账号。
- 绑定企微弹窗只选择已有 store_staff 账号，不出现创建账号或赋角色。
- /dashboard/companies 带随机查询参数后稳定跳转 /dashboard/。
- 用户管理和企微页面在 390x844 下 scrollWidth == clientWidth == 390。
- 浏览器 console 无 error/warn。

内置浏览器如果仍持有旧静态 chunk，旧 URL 可能先请求已删除资源；随机查询参数或刷新后由服务端重定向。该现象是浏览器缓存，不是 Company 路由仍存在。

### 9.3 全量发布门禁

2026-07-20 在正式 integration 工作树完成最后一轮全量门禁：

    PASS GOCACHE=/private/tmp/zhixiweibao-go-cache go test ./... -count=1
    PASS GOCACHE=/private/tmp/zhixiweibao-go-cache go vet ./...
    PASS pnpm --dir web typecheck
    PASS pnpm --dir web lint（0 error，32 warning，退出码 0）
    PASS cd web && node --test 所有 *.test.mjs（135/135）
    PASS pnpm --dir web build（45 个静态页面，生产构建成功）
    PASS git diff --check

Go 测试中的 `httptest` 需要监听本地随机端口，TypeScript/Next.js 需要在正式工作树写入缓存和构建产物；首次受限沙箱运行分别被操作系统拒绝。允许上述本地测试能力后相同命令完整通过，最终判定以上述成功结果为准。ESLint warning 是现有非阻断技术债，本次没有为清零 warning 扩大租户/门店语义收口的改动范围。

## 10. 运行前提

- AGENT_DESK_INVITATION_ENCRYPTION_KEY 必须是独立的有效 AES-256-GCM 密钥；不得提交到 Git。
- tenantRegistration.enabled 默认 false。启用前完成验证码、限流、邮箱/手机和隔离验收。
- 丽斯未来 100 个企微实例是仿真数据，未连接真实协议设备时在线状态为 unknown 属于预期。
- FastGPT 托管能力未配置时必须明确不可用，不能生成假 Dataset 或假模型测试结果。
- 模型、Token 和计费配置由平台能力负责；规则派单不读取模型配置、不产生派单模型 usage，也不修改回复/意图模型契约。

## 11. 合并步骤

1. git fetch origin --prune。
2. 确认 origin/main 和 origin/codex/customer-audit 仍是 integration 祖先。
3. 对 origin/codex/ai-billing 执行提交和行为对照；前七个映射提交不再吸收，最新 3538c8d/4db7993 按第 20 节选择性重做，禁止整分支 merge。
4. 核对 integration 60-65 和 ai-billing 60-67 的语义及远端新编号；任何新 migration 必须使用重新核对后的未占用版本。
5. 确认正式工作树只包含预期提交，无 docs/generated/、密钥和临时产物。
6. 运行第 9.3 节全部门禁。
7. push codex/tenant-ai-integration 并更新 PR #2。
8. 在 GitHub 确认 PR diff、checks 和目标分支。
9. 只合并 PR #2，不再合并 customer-audit 或 ai-billing 整分支。
10. 主线部署前备份数据库，在副本执行 migration 和 TenantIntegrityAudit。
11. 部署后验证登录、租户切换、用户管理、企微绑定、会话、派单、运营分析和人工质检。

## 12. 共享高风险文件

合并冲突必须按业务语义手工解决，禁止接受任一分支整文件：

    internal/models/models.go
    internal/bootstrap/server.go
    internal/bootstrap/routes.go
    internal/bootstrap/migration.go
    internal/pkg/constants/auth.go
    internal/pkg/dto/*
    internal/pkg/enums/*
    internal/services/user_service.go
    internal/services/message_service.go
    internal/services/conversation_*
    internal/services/wx_work_protocol_instance_service.go
    internal/services/fastgpt_dataset_service.go
    internal/services/store_ai_model_setting_service.go
    internal/services/tag_service.go
    internal/services/conversation_tag_service.go
    internal/ai/runtime/executor/reply_tag_context.go
    web/lib/api/admin.ts
    web/lib/navigation.tsx
    web/messages/zh-CN.json
    web/messages/en-US.json

解决后至少检查：

- TenantID 来源和范围是否仍可信。
- store_staff -> Store + Binding 是否仍原子。
- 企微绑定是否仍只使用已有 User。
- CompanyID 是否被重新带回运行时。
- ai-billing migration 60 是否误覆盖 integration 60。
- ai-billing migration 61-67 是否被原编号复制，或 migration runner 是否被降级。
- 模型和计费字段语义是否被客服侧改写；只允许退役 `dispatch_decision_llm`，不能误删回复和意图模型用途。
- StoreModelCredential 或门店 API Key 编辑是否绕过平台 AIConfig -> TenantAIModelGrant 授权链。
- CustomerTagRelation、ChangeLog 和 Evolution 是否都带可信 TenantID，是否错误依赖已退役 CompanyID。
- 现有 ConversationTag、TicketTag、租户完整性审计和历史筛选是否被无迁移删除。

## 13. 回滚边界

| 范围 | 回滚方式 | 注意 |
| --- | --- | --- |
| 页面与路由 | 回滚对应应用提交 | 旧 Company 页面文件已删除，不能只移除重定向 |
| Store 身份事务 | 回滚 service/UI 提交 | 已生成的 Store/Binding 需保留审计，不物理删除 |
| Migration 60-65 | 数据库备份恢复或专门修复 migration | 不支持仅靠应用代码逆转 |
| 分析事实 | 停止捕获并保留事实表 | 不反向控制会话运行时 |
| 规则派单 | 把客服组改为 `manual` 并使用人工池 | 保留 Assignment/DecisionLog；不恢复模型派单 |
| FastGPT/模型 | 撤销租户授权或禁用托管连接 | 不修改历史 usage 和计费证据 |
| 仿真数据 | customer_audit_seed --action cleanup | 仅清理带专用标识的丽斯未来测试数据 |

## 14. 不属于本次阻断

- 公开注册当前关闭：这是安全配置，不是账号管理链路缺失。
- 仿真企微在线状态 unknown：没有真实协议设备，不伪造在线。
- 服务运行后 Seed 静态标志变化：自动派单的正常结果。
- Company model/repository 仍存在：只用于历史 migration、审计和 usage 证据。
- cmd/customer_audit_seed 名称未改：避免无业务收益的大范围机械改名。
- PR 尚未进入 main：代码完成和主线发布状态必须分开描述。

## 15. 完成判定

只有同时满足以下条件才能写“本次目标完成”：

- 最终身份语义、账号入口和 Company 退役与真实代码一致。
- 丽斯未来静态 Seed、动态运行和完整性审计均有证据。
- 权威设计文档和本文件无旧开户/客户企业运行时语义。
- 权威派单文档、页面和运行时只存在人工/规则两种模式，旧模型派单只作为历史审计说明。
- 全量 Go、vet、前端 typecheck/lint/Node/build 和 diff 检查通过。
- 正式 integration 工作树干净。
- 分支成功 push，PR #2 指向最新提交。
- 远端并行分支和 migration 再次复核，无遗漏提交需要吸收。

main 实际合并由仓库负责人决定；在合并前只能写“PR 已就绪”，不能写“已进入 main”。

## 16. 2026-07-21 规则派单核心正确性加固（已完成）

### 16.1 本步目标与结果

- 待派会话读取改为优先级窗口与最老窗口合并，实际处理预算也固定保留最老任务名额，防止持续高优先级流量造成饥饿。
- 30 秒补偿由“租户全局待派列表”改为“租户轮转 -> 规则客服组轮转”；人工组积压不再占用规则派单预算，未解析客服组使用独立小窗口检查。
- 首响、恢复、负载和历史连续性统一要求消息发送者等于 Assignment 的 `ToUserID`；其他客服消息不能替当前客服计入回复。
- 连续性只认可真实成功回复，并支持复用同一 Conversation 的旧 `SessionNo`；同来源记录优先于跨来源记录。
- 活动会话负载不再把全部人工/客户消息加载到服务内存，改为 Assignment 维度聚合，再按最老未回复消息 ID 批量取时间；同时去掉逐会话读取 RouteState 的 N+1 查询。

### 16.2 文件与契约

本步修改：

    internal/repositories/conversation_repository.go
    internal/repositories/conversation_assignment_repository.go
    internal/repositories/message_repository.go
    internal/services/conversation_dispatch_load_service.go
    internal/services/conversation_dispatch_decision_service.go
    internal/services/conversation_dispatch_service.go
    internal/services/conversation_dispatch_rule_test.go
    docs/design/conversation-dispatch-engine.md
    docs/development/tenant-ai-integration-merge-handoff.md

没有新增表、字段、枚举、接口、权限或 WebSocket payload；SQLite/MySQL 共用标准 CASE、聚合和子查询。`Conversation`、`ConversationAssignment`、`DispatchDecisionLog` 的既有语义不变。

### 16.3 验证

    PASS go test ./internal/services -run 'TestPendingRepositoryWindows|TestPendingProcessingWindow|TestDispatchReplyFacts|TestDispatchContinuity|TestDispatchLoad|TestConversationDispatchWorkbenchBatch|TestRuleAssignmentRecovery' -count=1
    PASS go test ./internal/services -run 'TestPendingDispatchCompensation|TestPendingRepositoryWindows|TestPendingProcessingWindow|TestDispatchReplyFacts|TestDispatchContinuity|TestDispatchLoad' -count=1
    PASS git diff --check

新增回归明确覆盖：租户/客服组双窗口、处理预算最老任务保留、人工队列不挤占规则组、错误客服回复不计首响、旧会话轮次真实连续性。

### 16.4 并行分支、合并与回滚

- 已在本步开始前 fetch；本地 `codex/tenant-ai-integration` 与远端同为 `42dd560`。
- `codex/ai-billing` 没有修改本步三个 repository、派单 decision/load/service 或规则测试；本步未触碰模型调用、AI 回复、Token 与计费语义。
- `conversation_dispatch_service.go` 属于共享会话范围，但 ai-billing 当前未改该文件；后续提交前仍需再次 fetch 对照。
- 本步为兼容查询和 service 排序修改，可按对应提交整体回滚；没有 DDL/DML，不需要数据库逆向操作。

## 17. 2026-07-21 规则恢复、权限与实时工作台收口（已完成）

### 17.1 目标与运行结果

- 规则恢复分为 `first_response` 与 `follow_up` 两个阶段。首响前处理硬失效和 First Response SLA；已回复后只处理“客户仍等待超过 Response SLA + 原客服硬不可用”。
- `busy`、负载偏高、关闭自动接单或容量调整不会打断已回复会话。离线/Presence 过期、休息、离班、账号/档案/客服组停用、失去回复权限或来源范围失效才属于追问阶段硬失效。
- 恢复事务重新核验 Assignment、SessionNo、LastMessageID、当前回复/追问事实、SLA、原客服失效原因、候选资格、容量和负载。判断期间客服已回复时按冲突退出。
- 客服组、小组、当前排班、客服档案、账号状态、用户角色和角色权限变更提交后，立即触发本租户恢复及受影响客服组队列补派；30 秒任务只作补偿。
- 停用门店员工身份时同步重算原客服组来源范围，避免已停用门店/企微实例继续参与路由。
- 会话记录页统一调用 `/api/dashboard/conversation-dispatch/auto_assign`；旧 `/api/dashboard/conversation/dispatch`、DTO、Handler、前端 API 和无人调用 service 包装已删除。
- 派单工作台复用 Dashboard WebSocket，新增只向具备 `conversation.handover` 的用户订阅的 `dispatch:tenant:{tenantId}` 内部主题。可见页事件防抖刷新，隐藏页 60 秒补偿，payload 未改变。
- 权限编码继续复用 `conversation.assign/transfer/handover/recycle`。权限管理 API 路径更新为 `/conversation-dispatch/*`，默认客服组长补齐派发和转派。

### 17.2 本步文件与契约

核心新增/修改：

    internal/repositories/message_repository.go
    internal/repositories/conversation_assignment_repository.go
    internal/services/conversation_dispatch_recovery_service.go
    internal/services/conversation_dispatch_recovery_test.go
    internal/services/agent_team_service.go
    internal/services/agent_team_squad_service.go
    internal/services/agent_team_schedule_service.go
    internal/services/store_staff_binding_service.go
    internal/services/user_service.go
    internal/services/role_service.go
    internal/pkg/constants/auth.go
    internal/migration/000065_sync_agent_reply_permission.go
    internal/services/ws_service.go
    internal/services/ws_realtime_types.go
    internal/bootstrap/routes.go
    internal/handlers/dashboard/conversation_handler.go
    internal/pkg/dto/request/conversation_request.go
    internal/services/conversation_service.go
    internal/services/conversation_human_dispatch_service.go
    web/lib/api/admin.ts
    web/app/dashboard/conversation-monitor/page.tsx
    web/app/dashboard/conversation-dispatch/page.tsx

没有新增表、模型字段、派单模式、模型用途、Token 或计费语义。`ConversationAssignment.DecisionConfidence` 和旧模型决策事实继续只读保留。接口变更只有删除已被现行派单接口替代的 `/api/dashboard/conversation/dispatch`；调用方已经迁移。WebSocket 只新增服务端主题路由，不修改事件 payload。

### 17.3 验证证据

    PASS go test ./internal/services -count=1
    PASS go test ./internal/migration -count=1
    PASS go test ./internal/services -run 'TestRuleAssignmentRecovery|TestAgentBreakImmediatelyRecovers|TestRuleDispatchRetryCooldown' -count=1
    PASS git diff --check

新增测试明确覆盖：追问超时且休息/离班时转派、未超时不转、`busy` 不转、关闭自动接单/容量不打断已服务会话、无候选回原组池，以及判断后客服刚好回复时事务拒绝转派。最终全量 Go、前端 typecheck/lint/Node/build 和浏览器回归仍以本节之后的最新门禁记录为准。

### 17.4 并行分支、合并顺序与回滚

- 本步开始前已 fetch，权威工作树仍为 `codex/tenant-ai-integration`；`codex/customer-audit` 冻结，不再形成第二套 PR。
- `origin/codex/ai-billing` 与 integration 同时涉及 `conversation_human_dispatch_service.go`、`conversation_route_service.go`、`models.go`、`cron.go` 和 `web/lib/api/admin.ts` 等共享文件。合并时必须保留本分支删除的旧 dispatch 包装，同时保留 ai-billing 的模型调用、AI 回复、Token 与计费实现，禁止整文件覆盖。
- 推荐先合入共同依赖的 integration 契约，再按行为差异吸收 ai-billing；不要重新 cherry-pick 历史 customer-audit 派单提交。
- 恢复和实时刷新代码可按应用提交回滚。Migration 65 是幂等权限补齐；若回滚页面，不应删除已赋予角色的权限关系。Migration 64 的模型用途软删除仍遵循数据库备份/修复迁移边界，不能靠恢复旧代码重新启用模型派单。

## 18. 2026-07-21 最终门禁与真实运行验收（已完成）

### 18.1 全量门禁

    PASS go test ./... -count=1
    PASS go vet ./...
    PASS node --test **/*.test.mjs（136/136）
    PASS pnpm typecheck
    PASS pnpm lint（0 error；32 个既有 warning）
    PASS npm run build（Next.js 45/45 静态页面）
    PASS git diff --check

第一次全仓测试和构建只因受限环境无法访问 Go 缓存、监听 `httptest` 本地端口和写入 `.next` 失败；使用相同命令在正常项目权限下重跑后全部通过，不属于代码失败。

### 18.2 丽斯未来规则派单仿真

为避免把旧服务进程和动态运行后的历史仿真误当成新规则证据，先停止 09:12 启动的旧 `8084` 进程，再使用同一专用批次和既有平台模型配置 `deepseek`（AIConfig ID 7）幂等重建丽斯未来测试租户。重建后的静态报告为：

- 3 个业务客服组全部为 `rule`，3 个当前有效班次，12 名客服，0 个活跃派单模型设置。
- 36 个会话、135 条消息、21 条历史/当前 Assignment；9 条待派、18 条当前已派、覆盖 12 名客服。
- `expectedCoreComplete=true`、`expectedSimulationComplete=true`、`simulationBaselineIntact=true`。
- 回复和意图模型授权继续保留；仿真未创建派单模型 usage。

最新服务启动后，补偿扫描将 9 条待派任务全部以 `dispatch_mode=rule` 处理。每个客服组只从 2 名在线/空闲且在班客服中选择，忙碌和休息客服未接收新任务；日志同时出现首响 SLA 恢复和“客户追问超时 + 原客服硬不可用”的接力证据。页面通过 Dashboard WebSocket 自动刷新，无需用户手动刷新。

### 18.3 浏览器验收

- `/dashboard/conversation-dispatch`：只显示“规则均衡/人工派单”，无当前“智能均衡”；来源、客服组、状态、指派、SLA、负载、转派和释放均可见。可接单人数与 Presence 一致，在线/空闲为可接，忙碌/休息为不可接。
- `/dashboard/agent-team-schedules`：月/周/列表、当前班次、批量排班、新建和客服组筛选正常，桌面无重叠。
- `/dashboard/agents`：综合组、客服小组、服务范围、自动接单、并发、待首响/处理中负载正常，桌面无横向溢出。
- `/dashboard/roles`、`/dashboard/permissions`：角色分配权限入口和派单权限 API 显示正常，无页面溢出。
- `/dashboard/conversations`：全部账号保持选中时可同时高亮当前来源企微员工号；客户列表、人工待回复提示和聊天详情正常。
- 浏览器控制台未发现 error 或 warning。

### 18.4 运行数据说明

静态 Seed 报告只证明可重复基线。服务启动后，自动派单、恢复和真实页面读取会自然增加 Assignment/DecisionLog 并改变待派数，因此运行后的报告不应继续要求等于静态基线。历史 `intelligent` Assignment 只在旧审计记录中保留；新 Seed、新补偿扫描和新人工操作只写 `rule/manual`。

### 18.5 提交前远端复核

最终 fetch 后远端引用为：`origin/main@e67e207`、`origin/codex/tenant-ai-integration@42dd560`、`origin/codex/ai-billing@33b6d14`、冻结的 `origin/codex/customer-audit@c706815`。Migration 64/65 未被其他活跃分支占用；ai-billing 的旧 Migration 60 接待语义已在 integration 以 Migration 62 映射，不能再次整提交吸收。

ai-billing 与本批同时修改的共享文件仍为：

    internal/bootstrap/routes.go
    internal/models/models.go
    internal/services/conversation_human_dispatch_service.go
    internal/services/conversation_human_dispatch_service_test.go
    internal/services/conversation_route_service.go
    internal/services/cronx/cron.go
    web/lib/api/admin.ts

后续冲突解决必须保留本批的规则派单、恢复、旧 dispatch 删除和实时工作台，同时保留 ai-billing 的 AI 回复、FastGPT、模型 usage、Token 与计费行为。禁止整文件选边，禁止重新合并 `customer-audit`。

## 19. 2026-07-22 丽斯未来测试客服在线保活（已完成）

### 19.1 目标、诊断与边界

- 用户完成排班后，12 名丽斯未来测试客服仍全部显示“当前未在线”。真实链路复核确认账号、客服档案、自动接单、并发、角色权限、客服组、班次和班次成员均已通过，唯一阻断是 Seed 的 `last_seen_at` 超过生产派单 3 分钟新鲜度。
- 生产规则保持不变：真实客服仍需工作台/WebSocket 心跳，排班不能自动等同在线，也不为 `simulation_seed` 增加生产特判。
- 本批只让丽斯未来的 12 个固定仿真客服持续在线。作用范围必须同时满足固定测试租户注册身份、启用租户、当前 batch 标记、12 个固定测试用户名和同 batch 客服档案标记。

### 19.2 实现与运行方式

- `cmd/customer_audit_seed` 新增 `--action keepalive` 和 `--keepalive-interval`，默认每分钟立即并持续刷新测试 Presence，最短间隔 10 秒；SIGINT/SIGTERM 可正常停止。
- 初始 Seed 的 12 条 Presence 全部改为 `online`。保活将过期、busy、break 或已结束的合成时段恢复为新鲜 online；缺失时补建，并删除同一测试客服的重复合成行、关闭其他重复活动行，重复执行仍保持每人一个活动时段和总计 12 条合成 Presence。
- 标准命令：

      go run ./cmd/customer_audit_seed --config config/config.yaml --action keepalive --batch customer-audit-v1

- 保活进程停止后不伪造未来时间；3 分钟后测试客服会按真实派单规则重新变为离线。30 秒后端补偿扫描负责消费恢复后的可派候选，无需新增主服务 cron 或测试专属生产逻辑。

### 19.3 文件、契约与验证

本批文件：

    cmd/customer_audit_seed/main.go
    cmd/customer_audit_seed/simulation.go
    cmd/customer_audit_seed/presence_keepalive.go
    cmd/customer_audit_seed/presence_keepalive_test.go
    docs/design/service-analytics-and-quality.md
    docs/development/tenant-ai-integration-merge-handoff.md

- 没有 model、AutoMigrate、DML migration、DTO、enum、API、Gin 路由、WebSocket payload、权限、AI 回复、模型调用、Token、usage 或计费变化。
- 测试覆盖 12 人初始全在线、过期/休息恢复、缺失行补建、重复合成/活动行收口、连续刷新幂等以及同租户无关账号不变。
- 已通过：`go test ./cmd/customer_audit_seed -count=1`、`go test ./internal/services -run 'Presence|Dispatch' -count=1`、`go test ./... -count=1`、`go vet ./...` 和 `git diff --check`。
- 当前 8084 同库进程已连续按分钟记录 `agents=12`。派单页由 `可接单客服 0` 恢复为 `12`，12 名客服均显示“在线且在班，可自动接单”；原 9 条规则待派任务已由补偿扫描派完，页面为待派 0、共 27 条人工任务，控制台无 error/warn。
- 全仓 Go 与 vet 门禁已经通过；保活进程属于本地测试运行态，不进入 Git。提交前差异检查与远端复核均已完成。

### 19.4 并行分支、合并与回滚

- 开始前基线为 `codex/tenant-ai-integration@971c338`、`origin/codex/ai-billing@3538c8d`；提交前再次 fetch 后，`origin/codex/ai-billing` 已前进到 `4db7993`。本批四个命令代码文件未与该分支同文件修改；文档只更新唯一 integration 设计与交接，不恢复冻结的 customer-audit PR。
- `ai-billing@4db7993` 全分支使用 migration `60-67`；前七个提交的 60-63 语义已经在 integration 重做，最新客户标签范围使用 64-67，其中 64/65 与 integration 已有的“退役模型派单 / 同步客服回复权限”直接撞号。同时涉及 Tag model、权限、会话 DTO/API、AI 回复上下文和前端标签页面。该新增范围尚未完成语义吸收，不能整分支 merge 或直接 cherry-pick，PR #2 必须先完成逐项映射、迁移重写和回归验证再合并 main。
- 本批可在规则派单提交之后独立合并。回滚只需停止保活进程并回滚上述命令代码；无需回退数据库结构或删除历史业务数据。若需恢复静态仿真基线，可重新执行同 batch Seed。

## 20. 2026-07-22 最新 ai-billing 合并审计（仅记录，未合并）

### 20.1 审计范围与结论

- 审计基线：`codex/tenant-ai-integration@886dea7`、`origin/codex/ai-billing@4db7993`，共同祖先为 `f2d2da4`。
- integration 相对共同祖先有 138 个提交，ai-billing 有 9 个提交；前七个已按第 7 节建立语义对应，当前只需继续审计 `3538c8d` 和 `4db7993`。
- 结论是“禁止直接 merge、禁止整文件覆盖、禁止直接 cherry-pick”。本节只固定未来的选择性吸收契约，本次没有把任何 ai-billing 代码带入 PR #2。
- 冻结的 `codex/customer-audit` 仍是 integration 祖先。该旧工作区遗留的未提交运营分析文件是 integration 已提交版本的旧态，不得再搬运覆盖；其中 `.codex/audits/` 仍是本地历史截图，不进入 Git。

### 20.2 核心冲突矩阵

| 范围 | ai-billing 当前实现 | integration 权威语义 | 处理决定 |
| --- | --- | --- | --- |
| 模型凭据 | `StoreModelCredential` 允许 store_staff 维护门店 API Key，并让运行时围绕门店凭据解析 | `AIConfig -> TenantAIModelGrant -> StoreAIModelSetting -> 企微员工号覆盖`；供应商密钥只由平台管理 | 不吸收门店凭据表、接口和租户页面；仅评估加密、指纹、候选测试/激活/回滚机制能否复用到平台 AIConfig |
| 租户隔离 | 新模型主要使用 CompanyID/StoreID，缺少 TenantID | TenantID 是所有租户事实与查询的第一边界；Company 已退出运行时 | 新事实必须补 TenantID、租户唯一索引和完整性审计，禁止恢复 Company 路由 |
| 权限 | 多处直接检查 superadmin/store_staff 角色 | 所有管理动作必须进入权限管理；账号只分配角色 | 新接口先定义可见 Permission，再由 Handler 校验权限、Service 校验数据范围；删除隐藏角色授权 |
| Migration | 60-67 与 integration 60-65 发生编号和语义冲突 | integration 60-65 已成为权威且可能已执行 | 旧编号不复制；按当前数据契约重写新 migration，提交前再次核对全部远端 |
| Migration 运行器 | 会移除 definition mismatch、历史 archive/remap、失败退出和相关测试 | integration runner 是当前发布安全底座 | 完整保留 integration 实现，只移植独立且幂等的数据变换 |
| 客户标签 | 新建 CustomerTagRelation/ChangeLog/Evolution，并把 conversation.tag 改成客户标签；退役 ConversationTag | 已有 Tenant 范围 Tag、ConversationTag、TicketTag、筛选和完整性规则 | 先统一“标签目录、客户关系、会话快照”三种语义；兼容迁移完成前不删除 ConversationTag/TicketTag |
| AI 回复上下文 | 将持久化客户标签接入回复上下文 | 回复运行时以真实代码和 reply-runtime-engine.md 为准 | 只选择性复用经知识可回答性约束的只读标签上下文，不改变回复提交、usage、Token 或计费公式 |
| 使用证据 | 增加 credential revision 和通用模型 usage 槽位 | 现有 usage 由平台模型授权链和计费分支负责 | 可追加证据字段，但必须先与计费语义对齐；不得改价格、余额、聚合口径或历史事件 |

明确缺少 TenantID 的新事实包括 `StoreModelCredential`、`CustomerTagRelation`、`CustomerTagChangeLog`、`ConversationEvolutionState`、`ConversationEvolutionRun`，以及该分支版本的 `AIUsageEvent`。仅依赖 StoreID 或写 `CompanyID=0` 不能替代租户边界。

### 20.3 可复用与拒绝项

可作为实现参考、但必须重写接线的部分：

- AES-256-GCM、密钥指纹和不回显明文的测试。
- 凭据候选 -> 测试 -> 激活 -> 回滚工作流及 credential revision 证据，但所有权应落在平台模型配置。
- append-only 客户标签变更日志、确定性互斥组、证据消息和人工保护语义。
- 受知识可回答性约束的标签上下文、迁移幂等测试和标签工作流测试。
- 通用 usage 槽位与 revision 证据，但不改变计费公式和租户归集口径。

明确拒绝直接吸收：

- 门店员工编辑供应商 API Key、租户页面展示模型凭据或运行时绕过 TenantAIModelGrant。
- 任何基于角色名的隐藏授权。
- 新写 CompanyID 运行范围或用全局 `CompanyID=0` 标签目录混用多个租户。
- 用 ai-billing 的 migration runner 覆盖 integration。
- 无兼容映射删除 ConversationTag、TicketTag、历史标签关系或现有筛选接口。
- 恢复模型派单、修改规则派单、修改 Token/usage/价格/余额和计费公式。

### 20.4 后续选择性吸收顺序

1. 先冻结标签产品语义：Tenant 标签目录、CustomerTagRelation 持久客户画像、ConversationTag 当次会话快照、TicketTag 工单分类各自承担什么职责。
2. 定义 TenantID、唯一索引、父子关系、权限码、DTO 和兼容 API；这些共享契约先单独提交并通过 SQLite/MySQL 与 TenantIntegrityAudit。
3. 模型凭据继续由平台 AIConfig 管理。需要候选测试/激活/回滚时升级现有 AIConfig，不新建租户或门店明文入口。
4. 在现有 Tag/ConversationTag/TicketTag 上做可回滚迁移；CustomerTagRelation 与 ChangeLog 仅在现有模型无法表达稳定客户画像和审计历史时新增。
5. 接入客户标签人工管理、冲突组和历史日志，再接会话 UI；所有接口进入权限管理并按 TenantID、客服组范围和当前操作者复核。
6. 最后接只读 AI 回复标签上下文和 conversation evolution。模型输出先经过 schema、租户、标签目录、证据消息和人工保护校验，失败不能阻断正常回复。
7. 计费负责人确认 usage/revision 字段后才追加证据；客服分支不改计费计算。
8. 完成后更新本文件和 PR #2，再重新判断是否具备合并 main 条件。

### 20.5 验证与回滚门禁

选择性吸收完成后至少执行：

    go test ./... -count=1
    go vet ./...
    pnpm --dir web typecheck
    pnpm --dir web lint
    cd web && node --test 所有 *.test.mjs
    pnpm --dir web build
    SQLite/MySQL 首次 migration + 重复 migration + 历史数据回填
    TenantIntegrityAudit（双租户、跨租户负例、CompanyID=0 负例）
    标签目录/关系/冲突组/人工保护/历史兼容回归
    AI 回复、FastGPT、usage/Token/计费证据回归
    规则派单、人工接管、会话记录、工单标签和运营分析回归
    git diff --check

回滚顺序必须是 UI/运行时接线 -> 新 API/Service -> 新事实写入。兼容表和历史标签关系在数据核验、导出和明确废弃窗口前不物理删除；已经执行的 migration 不通过删除记录或改旧版本号回滚。

### 20.6 本次文档提交边界

- 本次只更新唯一 integration 合并交接和 PR #2 中文说明，不改代码、model、AutoMigrate、DML migration、DTO、enum、API、路由、权限、WebSocket、AI 回复、usage、Token 或计费。
- 已执行 `git fetch origin --prune`、分支祖先/提交映射、两项新增提交统计、migration 60-67、模型/权限/标签/前端共享文件和现有 PR 状态核对。
- 推送前只需执行文档差异审查与 `git diff --check`；代码全量门禁仍以第 18、19 节最近成功记录为准，未来吸收 ai-billing 后必须全部重跑。

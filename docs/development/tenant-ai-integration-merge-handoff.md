# Tenant AI Integration 唯一合并交接

> 状态日期：2026-07-20
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

2026-07-20 最后一轮 push 前 fetch 的拓扑：

    origin/main                         e67e207  -> integration 祖先
    origin/codex/customer-audit         c706815  -> integration 祖先
    origin/codex/tenant-ai-integration  926129f  -> 本轮 push 前远端基线
    origin/codex/ai-billing             33b6d14  -> 与 integration 在 f2d2da4 后分叉
    integration 最后一个代码提交        82abb92

SHA 只是 push 前快照，不表示当前 PR 头仍停在这些提交。每次后续 push 或 merge 前仍必须重新执行 `git fetch origin --prune` 并复核。

## 2. 权威文档

| 领域 | 当前依据 |
| --- | --- |
| AI 回复运行时 | 真实代码、docs/design/reply-runtime-engine.md |
| 租户、注册、权限和隔离 | docs/design/multi-tenant-company-registration.md |
| 门店身份、企微绑定和客服组范围 | docs/design/wxwork-managed-store-scope-implementation.md |
| 自动派单 | docs/design/conversation-dispatch-engine.md |
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
- 规则候选过滤、实时负载、公平性、排班小组、历史连续性和模型建议。
- 模型失败、低置信度或非法候选时回退到确定性规则。
- 派单结果、候选证据、模型理由、人工覆盖和失败原因可审计。

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
| 自动派单 | internal/services/conversation_dispatch_*_service.go |
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

DDL 继续由 AutoMigrate 完成；以上 DML 由 migration runner 幂等执行。

### 6.2 并行冲突

codex/ai-billing 的历史变体使用 migration 60 处理企微人设。integration 已把该语义迁移为 62，为 60/61 的运营分析让位。合并时不得复制 ai-billing 的 000060_migrate_wxwork_reception_persona.go，也不得重编号已经进入 integration 的 60-63。

创建任何后续 migration 前必须重新核对 origin/main、origin/codex/ai-billing 和其他活动分支。

### 6.3 Migration 63 风险

Migration 63 会：

- 清空活跃门店链路上的旧 CompanyID。
- 禁用旧 Company 范围意图。
- 删除 company.* 权限和角色关系。
- 停用无有效 User/角色、跨租户或重复的 Binding。
- 为已有有效 store_staff User 回填缺失 Store + Binding。

发布前必须备份数据库并在副本演练。简单回滚应用代码不能恢复已清理的旧 Company 权限和范围。

## 7. ai-billing 语义映射

origin/codex/ai-billing 在共同基点 f2d2da4 后的提交已按当前租户和门店身份语义重做进入 integration：

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

启动服务后自动派单会继续消费 9 条待派任务，当前观察到：

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
- 模型、Token 和计费配置由平台能力负责，本次只使用现有授权契约。

## 11. 合并步骤

1. git fetch origin --prune。
2. 确认 origin/main 和 origin/codex/customer-audit 仍是 integration 祖先。
3. 对 origin/codex/ai-billing 执行提交和行为对照；若只有第 7 节映射提交，不再吸收。
4. 核对 migration 60-63 没有远端新冲突。
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
- 模型和计费字段语义是否被客服侧改写。

## 13. 回滚边界

| 范围 | 回滚方式 | 注意 |
| --- | --- | --- |
| 页面与路由 | 回滚对应应用提交 | 旧 Company 页面文件已删除，不能只移除重定向 |
| Store 身份事务 | 回滚 service/UI 提交 | 已生成的 Store/Binding 需保留审计，不物理删除 |
| Migration 60-63 | 数据库备份恢复或专门修复 migration | 不支持仅靠应用代码逆转 |
| 分析事实 | 停止捕获并保留事实表 | 不反向控制会话运行时 |
| 自动派单 | 关闭自动策略并使用人工池 | 保留 Assignment/DecisionLog |
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
- 全量 Go、vet、前端 typecheck/lint/Node/build 和 diff 检查通过。
- 正式 integration 工作树干净。
- 分支成功 push，PR #2 指向最新提交。
- 远端并行分支和 migration 再次复核，无遗漏提交需要吸收。

main 实际合并由仓库负责人决定；在合并前只能写“PR 已就绪”，不能写“已进入 main”。

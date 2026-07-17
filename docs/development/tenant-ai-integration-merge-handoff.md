# Tenant AI Integration 唯一合并交接

> 状态日期：2026-07-17
>
> 唯一工作分支：`codex/tenant-ai-integration`
>
> 唯一主线目标：`codex/tenant-ai-integration -> main`，PR #2
>
> 只读迁移来源：`codex/customer-audit`
>
> 并行参考来源：`codex/ai-billing`

> 阅读规则：第 15 至 19 节是按时间保留的验收快照，其中的旧阻断已由后续批次逐项关闭；当前状态、剩余工作和发布顺序以第 3、5、7、14、20 节为准。

## 1. 强制合并决策

从本文件生效起：

1. 客服、会话、派单、运营分析、人工质检和满意度全部只在 `codex/tenant-ai-integration` 开发。
2. `codex/customer-audit` 冻结为只读历史与迁移来源，不再增加功能、修复、测试或交接批次。
3. 主线只评审并合并一个 integration PR。不得再把 customer-audit 或 ai-billing 分别直接合入 main。
4. 旧 customer-audit PR 若仍存在，只能标记为 superseded，不得作为第二条合并入口。
5. 从源分支吸收能力时按语义手工迁移到 integration，禁止整分支合并、整文件覆盖或把未提交工作树伪装成已合并提交。
6. 本文件是后续唯一合并交接。`docs/development/customer-audit-merge-handoff.md` 只保留历史记录。

这样做不是把多个 PR 串起来，而是让租户、AI、计费、客服和审计在同一最终代码树中先完成冲突处理、测试和验收，再一次进入 main。

## 2. 当前基线

2026-07-17 已执行 `git fetch origin --prune`：

```text
origin/main                        e67e207
origin/codex/ai-billing            f2d2da4
origin/codex/customer-audit        c706815
origin/codex/tenant-ai-integration 2ea04c8
```

工作树：

```text
/Users/qifeng/Documents/zhixiweibao-integration
branch: codex/tenant-ai-integration
runtime: http://127.0.0.1:8084
```

只读来源：

```text
/Users/qifeng/Documents/zhixiweibao
branch: codex/customer-audit
```

当前 Git 祖先关系已核对：`origin/main`、`origin/codex/ai-billing` 和 `origin/codex/customer-audit` 都是 integration HEAD 的祖先，没有三方分叉提交需要再合并。租户/AI 集成和 customer-audit 已进入同一提交历史，六个客服语义提交和最终门禁也已经完成；后续只需 push 和一个 PR。当前不需要 rebase；push 前若远端变化，必须重新判断。

上述 SHA 只是本次审计快照。每次任务开始、创建 migration、提交和 push 前都必须重新 fetch，不能把本文 SHA 当永久基线。

## 3. 当前 integration 提交状态

2026-07-17 已把自动派单、运营事实、总览、运营分析、会话记录、质检、仿真和文档按 `B -> A -> C -> D -> E -> F` 拆为六个语义提交。它们全部位于同一 `codex/tenant-ai-integration` 分支，只用于 review、回滚和依赖边界，不代表多个分支或多个 PR。

自动派单主要范围：

```text
cmd/customer_audit_seed/*
internal/models/models.go
internal/pkg/constants/ai_model.go
internal/pkg/enums/agent.go
internal/pkg/dto/request/agent_request.go
internal/pkg/dto/response/agent_response.go
internal/pkg/dto/response/conversation_dispatch_response.go
internal/repositories/conversation_repository.go
internal/services/agent_team_service.go
internal/services/conversation_assignment_service.go
internal/services/conversation_dispatch_service.go
internal/services/conversation_dispatch_decision_service.go
internal/services/conversation_dispatch_load_service.go
internal/services/conversation_dispatch_workbench_service.go
internal/services/conversation_human_dispatch_service.go
internal/services/conversation_route_service.go
internal/services/message_service.go
internal/services/store_ai_model_setting_service.go
web/app/dashboard/agents/_components/team-edit.tsx
web/app/dashboard/conversation-dispatch/page.tsx
web/lib/api/admin.ts
web/lib/generated/enums.ts
web/messages/en-US.json
web/messages/zh-CN.json
docs/design/conversation-dispatch-engine.md
```

运营分析与质检主要新增范围：

```text
docs/design/service-analytics-and-quality.md
internal/models/service_analytics.go
internal/pkg/enums/service_analytics.go
internal/pkg/dto/request/service_analytics_request.go
internal/pkg/dto/response/service_analytics_response.go
internal/repositories/service_analytics_repository.go
internal/builders/service_analytics_*.go
internal/services/service_analytics_*.go
internal/services/service_session_management_service.go
internal/services/quality_inspection_service.go
internal/services/quality_sampling_service.go
internal/services/report_view_preset_service.go
internal/services/conversation_evaluation_service.go
internal/handlers/dashboard/service_analytics_*.go
internal/handlers/api/conversation_evaluation_handler.go
internal/migration/000060_sync_service_analytics_permissions.go
internal/migration/000061_backfill_service_analytics_facts.go
web/lib/api/service-analytics.ts
web/app/dashboard/service-analytics/page.tsx
web/app/dashboard/conversation-monitor/page.tsx
web/app/dashboard/conversation-monitor/_components/session-workflow.tsx
web/app/dashboard/_components/dashboard-home.tsx
```

共享接线涉及 `models.go`、routes/server、Conversation/Message/Assignment/RouteState、WebSocket、权限、导航和多语言资源，已经按语义拆分提交，未使用 `git add .`。

当前验证证据：

```text
PASS  go test ./... -count=1
PASS  go vet ./...
PASS  go test ./internal/services ./internal/builders ./internal/handlers/dashboard ./internal/bootstrap -count=1
PASS  go test ./cmd/customer_audit_seed -count=1
PASS  go test -race ./internal/services -run '<评价/保存视图/质检完成/范围安全>' -count=1 -p 1
PASS  pnpm --dir web typecheck
PASS  pnpm --dir web lint（0 error，32 个历史 warning）
PASS  cd web && node --test $(rg --files -g '*.test.mjs')（137/137）
PASS  pnpm --dir web build（47 个静态页面）
PASS  git diff --check
PASS  隔离 MySQL 8.4 首次 migration、丽斯未来 Seed/report、租户完整性审计
PASS  MySQL 并发：评价幂等、Completed 质检单成功者、Presence 单活动时段
PASS  1440x900、390x844 主流程浏览器与页面级无横向溢出
PASS  公司主管、客服组长、普通客服三档真实账号 API 与浏览器范围专项
PASS  平台租户切换、空租户/单日零值与旧详情/维度清空专项
PASS  Presence 同用户双连接、部分/最终断开、重复关闭、心跳超时和休息恢复专项
PASS  原 8083 历史库副本重复升级至 61，TenantIntegrityAudit 74/87/202、0 违规
PASS  最新增量全仓 Go/vet/race、前端 lint/typecheck/Node/build、全新 SQLite/MySQL 重复 migration、隔离重复 Seed/report
PASS  B/A/C/D/E/F 六个语义提交
PASS  最终 fetch、完整发布门禁和 migration 60/61 远端冲突核对
PASS  push 与唯一 integration PR #2；旧 customer-audit PR #1 已关闭
```

MySQL 仿真稳定产出 36 个 ServiceSession、39 个 ResponseSpan、12 个 PresenceSession、9 个 QualityInspection、9 个 Evaluation、30 个 DispatchDecisionLog 和 1 个策略。重复 Seed 结果一致，cleanup 后租户业务事实归零。仿真本身不调用真实模型；测试交互产生的 usage 只按专用仿真租户和仿真会话清理，不改变记录、Token、价格或计费语义，平台与其他租户 usage 保留。生命周期测试覆盖正常关联、历史孤儿和平台无关证据。修复后租户完整性审计检查 74 个租户模型、87 张必需表和 202 条关系，0 违规，36 个 ServiceSession 均为 exact。

浏览器已验证总览、运营分析六 Tab、会话记录、派单、实时会话和公开评价页。仅人工质检详情只展示目标 Assignment 内人工回复证据；“全部账号”选择客户时保持全部账号上下文并次级高亮来源企微；公开评价完成真实 5 星提交并在刷新后保持已提交。`1440x900` 和 `390x844` 下上述后台页面的 `documentElement.scrollWidth == clientWidth`，公开评价错误态也无溢出，控制台无 warning/error。

三档角色专项已经完成：公司主管可见本租户 36 个 ServiceSession、9 条质检和 9 条评价；客服组长 001 只见负责客服组 2 的 10 个 ServiceSession、3 条质检和 3 条评价；客服 003 只见本人 4 个 ServiceSession 和 2 条质检，评价后台接口按权限拒绝。浏览器中普通客服已完成质检显示 `95 / 100` 且所有评分、证据和评语只读，没有保存/完成动作；组长和公司主管的草稿质检字段可编辑并显示保存草稿、完成质检动作。导航按账号实际角色权限显示，没有依赖前端隐藏替代后端范围。

此前记录的会话操作缺失、筛选契约分裂、Seed 缺分析事实、测试夹具缺表、前端枚举/typecheck 和历史小组快照问题均已处理，不能继续把旧失败当当前阻断。当前实现已形成六个本地语义提交，但在 push 和唯一 PR 完成前不得写成已进入 main。

本轮继续完成：待派 ServiceSession 保存目标客服组快照并在释放至全局池时清零；ServiceSession 小记、质检详情/保存和抽样批次同时校验来源与组/本人范围；Presence 写入串行化并完成同用户多连接生命周期；评价 Token、保存视图和 Completed 质检补齐并发、幂等、所有权和跨租户门禁；平台租户切换、空租户和原 8083 历史副本升级均已验收。上述结论均来自 integration 当前代码、测试和隔离数据库，不引用 customer-audit 旧验证替代。

## 4. customer-audit 冻结状态

`codex/customer-audit` 只保留历史提交和旧样板用于追溯。它不再接受提交、不再补测试、不再更新业务文档，也不再建立或更新独立 PR。旧 Draft PR 应标记 superseded。

运营分析独立文件和共享接线已经手工迁入并提交到 integration。后续发现遗漏时，只能在 integration 里依据当前代码修复，禁止回到 customer-audit 修改再二次搬运。customer-audit 的历史验证只能证明旧样板方向可行，不能替代 integration 当前门禁。

`cmd/customer_audit_seed` 只是既有仿真命令的历史目录名，不是工作分支或第二个 PR。为避免本批扩大机械改名和脚本路径变更，当前保留该命令名；它的所有新增事实、测试和文档仍只在 `codex/tenant-ai-integration` 提交。后续若要产品化改名，应作为单独的纯重命名提交处理。

禁止迁移 `.codex/audits/`、临时数据库、密钥、截图和 `docs/generated/`。

## 5. 功能完成状态

| 能力 | integration 当前状态 | GitHub 状态 |
| --- | --- | --- |
| 多租户公司、邀请注册、角色权限 | 已提交并运行 | 回归，不重建 |
| 平台模型授权、租户默认、员工号覆盖 | 已提交并运行 | 回归，不改授权和计费语义 |
| AI 回复、usage、token、计费契约 | 已集成 | 客服分析不得修改 |
| 客服组、小组、排班 | 已提交并运行 | 作为组织范围和统计维度 |
| 规则与模型协同派单 | 已提交并通过独立全仓回归 | PR #2 |
| 服务轮次、响应事实、数据质量 | 已提交；新鲜及历史 MySQL 已通过，历史 36 轮均标 estimated | PR #2 |
| 服务小记、范围导出 | 已提交；导出超限会显式失败 | PR #2 |
| 总览精确口径和角色范围 | 已提交并完成三档范围、租户切换和 Presence 多连接验证 | PR #2 |
| 运营分析六 Tab | 已提交并完成 P50/P90、四类客服视图、来源质量、钻取和导出验证 | PR #2 |
| 会话记录 | 已提交并完成原操作、统一筛选、标签、导出、人工证据和三档范围验证 | PR #2 |
| 仅人工回复质检 | 已提交；抽样、评分、证据、模板、不可变完成和三档动作均通过 | PR #2 |
| 保存视图与范围化导出 | 已提交；所有权、租户切换、恢复和超限门禁通过 | PR #2 |
| Presence | 已提交；状态控件、多连接、断线、超时和休息恢复通过 | PR #2 |
| 真实满意度 | 已提交；Token/API、公开评价、聚合、并发和跨租户通过 | PR #2 |
| 权限管理显示与默认角色 | 已提交；权限目录、路由、页面动作和直接请求通过 | PR #2 |
| 派单质量证据 | 已提交；成功、失败、降级、过期和人工覆盖证据通过 | PR #2 |
| 丽斯未来运营仿真 | analytics 事实、report/cleanup、usage 关联清理和幂等测试已完成 | 只作验收数据，不改变计费事实语义 |

完成度判断统一使用四档：已提交运行、已写待验证、后端完成前端未接、未完成。禁止用“文件已存在”替代产品验收。

权威产品方案见 `docs/design/service-analytics-and-quality.md`。

## 6. 共享冲突文件

以下文件禁止从 customer-audit 整文件复制：

| 文件 | integration 必须保留 | 迁移时增加 |
| --- | --- | --- |
| `internal/models/models.go` | TenantGrant、自动派单字段、AI/计费模型注册 | analytics 模型注册、Assignment.SessionNo、MigrationDefinitionArchive |
| `internal/bootstrap/routes.go` | 当前租户/模型/派单路由 | analytics、质检、评价路由 |
| `internal/bootstrap/server.go` | 当前中间件、静态资源、后台路由 | 公共评价页 API 接线 |
| `conversation_assignment_service.go` | 公平负载、事务、路由提交 | SessionNo 和事实捕获 |
| `conversation_route_service.go` | 人工窗口、AI 恢复、智能派单触发 | queue/session 事实捕获 |
| `message_service.go` | 连续消息、AI 回复、usage/计费边界 | 响应分段与首响捕获 |
| `dashboard_service.go` | 当前租户总览 | 精确事实和组织范围 |
| `ws_*` | 当前连接/事件语义 | PresenceSession 捕获 |
| `tenant_integrity_audit_service.go` | integration 现有租户隔离规则 | 当前未提交基线为 74 个租户模型、87 张必需表、202 条关系 |
| `web/lib/navigation.tsx` | 平台/租户上下文导航 | 运营分析入口和会话记录命名 |
| locale/enums/API | integration 当前字段 | 兼容新增，禁止删除另一侧键 |

合并共享文件时逐字段、逐方法核对，不使用 `git checkout --theirs/--ours` 作为最终解决方案。

## 7. integration 内部提交与合并顺序

以下都是同一个分支内的可回滚提交，不是多个分支或多个 PR。实际提交顺序固定为 `B -> A -> C -> D -> E -> F`：`models.go` 同时注册运营事实模型并承载派单共享字段，必须先提交 B 的事实契约，A 才不会引用尚不存在的模型类型。

### 提交 B：分析事实与权限契约

1. 提交 analytics model/enum/repository、Assignment.SessionNo、数据质量和租户完整性关系。
2. 提交 migration 60/61 及 SQLite/MySQL 幂等测试。
3. 提交权限常量、默认角色映射和前端生成枚举。

### 提交 A：稳定自动派单

1. 保留已修复的历史小组快照语义，确保分析代码不改变自动派单和当前组织语义。
2. 跑自动派单、AI Runtime、usage、租户和关键统计回归。
3. 只暂存派单服务、DTO、UI、测试和派单设计文档；共享事实契约已经由 B 提供，不在 A 重复提交。

### 提交 C：运行时捕获与工作流 API

1. 提交会话、消息、人工池、Assignment、转派、关闭和 WebSocket/Presence 接线；待派 ServiceSession 的 `AssignedTeamID` 目标组快照及全局池清零语义已修复并有测试。
2. 提交服务小记、质检、抽样、保存视图、导出和评价 API。
3. 质检保存事务内已复核 Completed 状态；跨租户、来源+分配双范围、并发评价、保存视图所有权和人工证据门禁已补齐，MySQL 并发、三档角色与主浏览器流程已验证。

### 提交 D：总览与运营分析页面

1. 提交已经改造的 Dashboard 精确口径和三档组织范围，保留当前快照/今日累计边界。
2. 提交已经完成的中位数/P90、四类客服视图、来源质量、钻取和汇总导出，以及已经加固的派单失败证据范围。
3. 保留已统一的 `serviceAnalytics.managePolicy` 权限码，完成权限显示、直接请求和页面动作测试。

### 提交 E：会话记录、质检、Presence 与评价页面

1. 提交已经升级的 Conversation Monitor、服务小记、保存视图、自定义列、范围导出、固定抽样和人工质检；旧监控页分配、重新派单、转派、标记已读和关闭操作已经恢复并继续使用原状态机。
2. 提交已经接入的抽样批次列表、质检模板管理、评价后台列表、Presence 控件和 `/support/evaluation`。
3. typed `ServiceSessionQuery`、会话 SLA/渠道/解决状态/分类/标签筛选及标签编辑已经完成；公开评价过期/无 Token/已提交/重复提交、三档角色和 Presence 多连接断线恢复均已验收。

### 提交 F：验收证据与文档

1. 丽斯未来租户的 ServiceSession、ResponseSpan、Presence、质检、评价和派单证据，以及 report/cleanup/幂等测试已经完成。
2. 全仓 Go、vet、关键 race、typecheck、lint、137 个 Node 测试、47 页面静态 build、全新 SQLite/MySQL 重复 migration、隔离重复 Seed/report、MySQL 并发、三档角色和桌面/移动主流程均已通过。
3. 更新本文件每个提交 SHA、验证、风险和回滚边界后，才创建唯一 integration PR。

每个提交都必须保留 integration 当前模型授权、AI 回复、智能派单和 usage/计费语义。禁止恢复 customer-audit 的旧模型选择、旧转人工或旧 AI 回复链路。

## 8. 权限与范围检查

每个新增权限必须同时出现在：

- 后端权限常量。
- migration 权限目录和默认角色关系。
- 权限管理页面国际化。
- handler `RequirePermission`。
- 前端导航/按钮可见性。
- 权限与直接请求测试。

权限只控制能力，数据范围由 service 统一添加：平台管理员需活动租户，公司主管看本租户，客服组长看负责组，客服看本人。禁止 handler 只传 TenantID 后让低级角色看到全租户数据。

## 9. Migration 与数据库要求

integration 已提交 migration 60/61。2026-07-17 提交前 fetch 后与 main、ai-billing、customer-audit 无编号冲突，新鲜 MySQL 8.4 首次 migration、Seed 和租户完整性审计已通过；原 8083 历史库副本也已重复升级到 61 并通过完整性审计。push 前若远端分支占用相同编号，必须先处理，不能重复。

生产升级前：

1. 对原 MySQL 做可恢复备份。
2. 先恢复到独立临时 MySQL 8.4。
3. 列出并确认 migration 版本复用记录；只允许代码内登记的历史定义逐字段归档，未知冲突必须中止。
4. 处理原 8083 数据 migration 39 的已知组长租户冲突，形成明确人工映射记录。
5. 列出无父引用记录；只有确认为已删除测试会话残留且经业务确认后才能在副本清理，migration 本身继续严格拒绝孤儿。
6. 从断点继续执行到最新 migration，再重复执行一次验证幂等。
7. 执行租户完整性审计和关键业务抽样，要求 0 违规。
8. 确认后才安排生产维护窗口。

不得直接删除、改 remark 或伪造 migration 已执行。已确认的旧定义由 `MigrationDefinitionArchive` 原样保存后才释放版本号执行当前定义；未知定义继续硬失败。不得把冲突数据强行归入 legacy tenant。AutoMigrate 新列和 DML 回填不会随代码回滚自动消失。

## 10. AI 与计费边界

客服分析与质检实现不得改变：

- 模型供应商配置和平台 API Key。
- TenantAIModelGrant 和员工号模型覆盖解析顺序。
- AI 回复、Intent、Media、ASR 和 Knowledge Runtime。
- token、usage、价格、余额和计费公式。
- `dispatch_decision_llm` 的 usage 证据语义。

质检首版不调用模型自动评分。满意度和运营事实不写入 AI usage。

## 11. 每批交接模板

每个可提交步骤完成后在本文件追加：

```text
批次与日期：
目标：
Commit：
文件：
Model/AutoMigrate：
DML migration：
DTO/enum/API/WebSocket：
权限：
运行语义：
验证命令与结果：
SQLite/MySQL：
浏览器桌面/移动：
与 main/ai-billing/customer-audit 同文件：
是否需要 rebase：
建议合并顺序：
回滚边界：
已知风险：
```

未完成或未运行的项目必须明确写“未完成/未运行”，不能用设计结论代替验证证据。

## 12. 最终合并门禁

```text
git fetch origin --prune
go test ./... -count=1
go vet ./...
go test -race <客服/租户/AI Runtime 关键包> -count=1 -p 1
pnpm --dir web lint
pnpm --dir web typecheck
pnpm --dir web build
cd web && node --test $(rg --files -g '*.test.mjs')
go run ./cmd/customer_audit_seed --config <isolated-config> --action seed
go run ./cmd/customer_audit_seed --config <isolated-config> --action report
go run ./cmd/tenant_integrity_audit --config <isolated-config> --pretty
git diff --check
```

还必须完成：

- SQLite 首次/重复 migration。
- 临时 MySQL 8.4 首次/重复 migration。
- 双租户隔离和平台切换租户。
- 公司主管、组长、客服三种数据范围。
- 多 sessionNo、转派、AI-only、人工质检和评价 Token。
- 1440x900、390x844 浏览器截图与无重叠/无页面横向溢出检查。
- 原会话回复、自动派单、模型授权、企微员工号和计费证据回归。

## 13. 回滚边界

- 页面、API 和统计捕获可按独立提交回滚。
- 事实表可从业务主表重建，但已完成人工质检和客户评价属于运营记录，删除前必须导出并获得明确确认。
- Assignment.SessionNo、事实质量标记和 migration 回填不能通过删除列或删除 migration 记录回滚。
- 紧急回滚应先关闭新入口和捕获任务，保留数据，再回滚服务/UI。
- 回滚 analytics 不得回滚 Tenant、TenantGrant、AI Runtime、自动派单 Assignment 字段、usage 或计费契约。

## 14. 当前下一步

1. **最终发布门禁**：关键 race、前端 build、SQLite/MySQL 重复 migration、隔离 Seed/report/cleanup 和租户完整性审计已通过。
2. **唯一发布路径**：PR #2 是唯一 `codex/tenant-ai-integration -> main` 合并入口，等待评审与合并。
3. `customer-audit` 保持冻结，旧 Draft PR #1 已关闭；主线不得再分别合入 customer-audit 或 ai-billing。

## 15. 2026-07-17 本轮复核记录

```text
批次与日期：客服运营分析交付复核，2026-07-17
目标：统一到 tenant-ai-integration；复核客服需求 1-6；修复重复 Seed 的仿真 usage 孤儿关系；更新唯一合并方案
Commit：未提交工作树
文件：cmd/customer_audit_seed/simulation.go、cmd/customer_audit_seed/lifecycle_test.go、本文和 service-analytics-and-quality.md
Model/AutoMigrate：无新增、无变更
DML migration：无新增；60/61 保持原语义
DTO/enum/API/WebSocket：无变更
权限：无新增；沿用已同步的运营分析、会话记录、质检、评价、保存视图和 Presence 权限
运行语义：只清理专用仿真租户/会话关联 usage，平台和其他租户 usage 保留；不改变 usage 记录、Token、价格或计费
验证命令与结果：go test ./... PASS；go vet ./... PASS；关键 service race PASS；lint 0 error/32 historical warnings；typecheck PASS；Node 135/135 PASS；Next build 47 pages PASS；git diff --check PASS；定向 Seed 生命周期 PASS；重复 MySQL Seed/report PASS；tenant-integrity-audit 74/87/202、0 违规；exact=36
SQLite/MySQL：SQLite 生命周期 PASS；MySQL Seed/report、并发评价/质检/Presence PASS
浏览器桌面/移动：1440x900 与 390x844 主流程无页面横向溢出；人工证据、企微来源高亮、真实评价提交已验收
与 main/ai-billing/customer-audit 同文件：Seed 目录和本交接文档；未修改 AI/计费运行文件
是否需要 rebase：2026-07-17 fetch 后不需要；提交和 push 前重新 fetch
建议合并顺序：仍按 A-F，同一分支、一个 PR
回滚边界：可单独回滚仿真 cleanup/test/docs；不得回滚 TenantGrant、AIUsageEvent 或计费契约
已知风险：本批记录时三档角色尚未完成，后续已在第 18 节完成；租户切换、Presence 多连接和原 8083 历史库副本 migration 39 人工映射仍未完成
```

## 16. 2026-07-17 派单角色范围收口

```text
批次与日期：派单角色与组织范围专项，2026-07-17
目标：普通客服不进入派单编排；客服组长只能读取自己负责客服组的任务、统计和客服负载；公司主管保留全租户视角
Commit：未提交工作树
文件：internal/handlers/dashboard/conversation_dispatch_handler.go、internal/services/agent_team_scope_service.go、internal/services/conversation_dispatch_workbench_service.go、internal/services/conversation_dispatch_workbench_scope_test.go、web/lib/navigation.tsx、web/app/dashboard/_components/dashboard-home.tsx、相关权限测试和本文
Model/AutoMigrate：无新增、无变更
DML migration：无新增；60/61 保持原语义
DTO/enum/API/WebSocket：无字段、枚举或路由变化；派单读接口权限从 conversation.view 收紧为现有 conversation.handover
权限：不新增隐藏权限；普通客服沿用会话工作台，组长/公司主管沿用 conversation.handover
运行语义：新增统一 ManageableTeamIDs 组织作用域；未归属全局池仅公司主管/平台管理员可见；组长可同时管理自己负责的多个综合客服组
验证命令与结果：定向 service 测试 PASS；go test ./internal/services ./internal/handlers/dashboard -count=1 PASS；git diff --check PASS
SQLite/MySQL：SQLite 服务层覆盖主管全租户、A 组组长、B 组越权筛选、普通客服直接 service、统计和负载一致性；运行中 MySQL API 复核 PASS
浏览器/API：普通客服派单导航隐藏且 list/stats/agent_loads 返回 3001；组长 001 从跨组三组 21 条收紧为本组 7 条，stats.total=7，负载仅本组 4 人，teamId=3 越权查询返回 0；公司主管仍保留全租户范围
与 main/ai-billing/customer-audit 同文件：仅 integration 当前派单/权限共享文件；未修改 AI Runtime、模型供应商、usage、Token、价格、余额或计费语义
是否需要 rebase：2026-07-17 fetch 后不需要；提交和 push 前重新 fetch
建议合并顺序：归入提交 A（稳定自动派单），仍为同一 integration 分支和一个 PR
回滚边界：可回滚派单读范围和导航权限接线；不得回滚原会话发送、自动派单决策、模型授权或计费契约
已知风险：本批记录时公司主管/组长质检动作尚未完成，后续已在第 18 节完成；租户切换、Presence 多连接和原 8083 历史库副本 migration 39 人工映射仍未完成
```

## 17. 2026-07-17 单分支与客服需求 1-6 再复核

### 17.1 分支和 PR 的唯一关系

2026-07-17 再次执行 `git fetch origin --prune` 后确认：

```text
origin/main                         e67e207
origin/codex/customer-audit         c706815
origin/codex/ai-billing             f2d2da4
origin/codex/tenant-ai-integration  2ea04c8
```

`git merge-base --is-ancestor origin/codex/customer-audit HEAD` 返回成功，且 `git log HEAD..origin/codex/customer-audit` 为空。这表示 customer-audit 没有 integration 缺失的独立提交，后续不需要 merge、rebase、cherry-pick 或双向同步该分支。

唯一发布动作固定为：

1. 所有客服、派单、运营分析、会话记录、Presence、满意度和人工回复质检的修复、测试与文档只写入 `codex/tenant-ai-integration`。
2. `codex/customer-audit` 保留只读历史，旧 Draft PR 标记 `superseded by tenant-ai-integration` 并关闭，不再追加提交或作为合并前置。
3. 当前未提交工作树按 A-F 拆成同一分支内的多个语义提交；这些提交只用于 review、回滚和 cherry-pick 边界，不创建多个分支或多个 PR。
4. 最终只 push `codex/tenant-ai-integration`，只建立一个 `codex/tenant-ai-integration -> main` PR。主线不得再单独合并 customer-audit 或 ai-billing。
5. integration PR 合并前保留历史远端分支用于追溯；合并后是否删除旧分支不影响代码，但必须先确认旧 PR 已关闭、提交 SHA 已能从 main 追溯。

### 17.2 客服原始要求的产品结论

重新阅读 Word 文件第 1 至第 6 部分后，当前设计仍成立：不复制七鱼的实时、总览、响应、坐席、会话和质检六套平行页面，而是在现有租户架构内形成三类职责清楚的入口。

| 客服要求 | 唯一产品落点 | 复核结论 |
| --- | --- | --- |
| 实时数据 | `/dashboard` | 当前快照与今日累计分开，使用真实服务轮次、响应、Presence 和派单事实 |
| 历史总览、响应、坐席 | `/dashboard/service-analytics` 六个 Tab | 共用一套筛选、指标口径、组织范围和钻取，不复制宽表和重复报表 |
| 会话记录 | `/dashboard/conversation-monitor` | 每个 `conversationId + sessionNo` 一条记录，保留原分配/转派/关闭状态机 |
| 在线质检 | 会话记录内的人工回复质检 | 只评分目标 Assignment 的人工消息，AI/客户消息仅作上下文 |
| 满意度 | 一次性评价 Token + 运营分析 | 邀评率、参评率、满意率分开，不使用模糊“相对满意度” |
| 留言报表 | 未回复视图与来源分析 | 当前无独立留言业务动作，不建立重复状态机 |

第 7、8 部分继续不在本任务范围。答问比、设备在线时长、模糊综合排名和未经真实数据支持的 FCR 不照搬；当前使用“24 小时无重复咨询率”，待问题分类和解决状态稳定后再评估是否升级为 FCR。

### 17.3 最新增量完成与剩余阻断

本次复核前的最新增量已经完成：

- 组长派单任务、统计和客服负载只使用其负责客服组；全局未归属任务仅公司主管/平台管理员可见。
- 会话导出先统计完整匹配范围，超过单次上限返回明确错误，不再静默截断。
- 普通客服有 `qualityInspection.view` 时只能查看本人质检结果，评分证据和完成操作保持只读。
- 趋势按自然日补零；空范围和跨日响应仍保留连续日期轴。
- 保存视图已验证本人保存/恢复、默认唯一、跨用户不可见且不能更新或删除。
- 三档真实账号范围已验收：公司主管为本租户全量，组长只见负责客服组，普通客服只见本人；质检动作与评价后台权限同时通过浏览器和直接 API 复核。

仍阻断“产品完成/可合并”结论的事项只有以下四类：

1. 平台管理员切换租户后的旧结果、详情、筛选维度、保存视图和导出状态清空，并完成空日期范围浏览器验收。
2. Presence 多浏览器连接、部分断开、全部断开、超时、休息恢复与单活动数据库行。
3. 原 8083 数据库可恢复副本上的 migration 39 逐账号租户映射，以及继续重放至 61 后的 exact/estimated/incomplete 审计。
4. 最新增量之后的全量 Go/vet/race/lint/typecheck/Node/build/MySQL/Seed/完整性/桌面移动门禁，以及 A-F 提交和唯一 integration PR。

最近一次完整全量门禁曾通过；上述最新导出、趋势、只读质检和范围增量只完成了聚焦测试、`pnpm typecheck`、`pnpm build` 和 `git diff --check`，尚未再次重跑完整门禁。合并说明必须保留这个时间顺序，不能用旧全量结果替代最新工作树验收。

```text
批次与日期：单分支与客服需求 1-6 再复核，2026-07-17
目标：确认 customer-audit 不再产生独立 PR；按最新未完成能力冻结完整收口方案；修正全量门禁表述
Commit：未提交工作树
文件：docs/design/service-analytics-and-quality.md、docs/development/tenant-ai-integration-merge-handoff.md
Model/AutoMigrate：无新增、无变更
DML migration：无新增；60/61 保持原语义
DTO/enum/API/WebSocket：无变更
权限：无新增；只记录现有权限与角色范围的验收状态
运行语义：无代码改动；不触碰 AI Runtime、模型、usage、Token、价格、余额或计费
验证命令与结果：git fetch PASS；customer-audit 是 integration 祖先且无独立提交；Word 第 1-6 部分逐图复核 PASS；文档一致性检查 PASS；git diff --check PASS
SQLite/MySQL：本批无数据库改动、未重跑；沿用最近基线，最新增量最终门禁仍待执行
浏览器桌面/移动：本批为方案与交接复核，未新增浏览器验收
与 main/ai-billing/customer-audit 同文件：仅 integration 权威文档；customer-audit 历史交接不再继续更新
是否需要 rebase：当前不需要；提交和 push 前再次 fetch
建议合并顺序：A-F，同一 integration 分支、一个 PR
回滚边界：可单独回滚本批文档；无运行时或数据回滚
已知风险：本批记录时三档角色尚未完成，后续已在第 18 节完成；租户切换、Presence 多连接、历史库副本、最新全量门禁和 A-F 提交仍未完成
```

## 18. 2026-07-17 三档角色专项验收

```text
批次与日期：公司主管、客服组长、普通客服三档范围与质检动作专项，2026-07-17
目标：验证角色权限显示、后端数据范围和质检动作一致；完成后从剩余阻断中移除三档角色专项
Commit：未提交工作树
文件：本次只更新 docs/design/service-analytics-and-quality.md、docs/development/tenant-ai-integration-merge-handoff.md；运行代码沿用当前 integration 未提交工作树
Model/AutoMigrate：无新增、无变更
DML migration：无新增；60/61 保持原语义
DTO/enum/API/WebSocket：无新增、无变更
权限：无隐藏权限；公司主管按本租户全量，组长按负责客服组，普通客服按本人 Assignment；评价后台继续要求既有查看权限
运行语义：不改变会话、派单、AI Runtime、模型授权、usage、Token、价格、余额或计费
直接 API：公司主管 36 ServiceSession / 9 QualityInspection / 9 Evaluation；组长 001 仅客服组 2，10 / 3 / 3；客服 003 仅 agentId=8，4 ServiceSession / 2 QualityInspection，Evaluation 列表按权限拒绝
浏览器：客服只见本人 4 条会话；已完成质检显示 95/100，评分、证据、评语全部只读且无保存/完成动作；组长与公司主管草稿质检可编辑并有保存草稿/完成质检动作；导航按账号已授角色权限显示
验证命令与结果：三角色登录、导航、列表数量、质检详情动作和直接 API 组合 PASS；本批未重跑全量门禁
SQLite/MySQL：使用隔离 MySQL 8.4 丽斯未来测试租户现有仿真数据；本批无 schema 或 DML 改动
浏览器桌面/移动：本批完成桌面三角色动作专项；移动主流程沿用最近 390x844 通过基线
与 main/ai-billing/customer-audit 同文件：仅 integration 权威文档；customer-audit 保持只读，不产生第二个 PR
是否需要 rebase：2026-07-17 fetch 后不需要；提交和 push 前再次 fetch
建议合并顺序：仍按 A-F，同一 codex/tenant-ai-integration 分支、一个 PR
回滚边界：仅文档状态更新；无运行时或数据回滚
已知风险：平台租户切换清状态、Presence 多连接、原 8083 历史库副本、最新全量门禁、A-F 提交和唯一 integration PR 尚未完成
```

## 19. 2026-07-17 租户切换、Presence 与历史库升级收口

### 19.1 租户切换与空数据

- 在丽斯未来测试租户设置“今日”并进入质检待处理钻取，打开会话详情后切换到默认公司。
- 切换后旧客户名称、详情弹窗、日期筛选和租户维度立即消失；默认公司 Dashboard、运营分析和会话记录均为 0，不含丽斯未来客服、门店或来源。
- 默认公司点击“今日”后保留 `2026-07-17` 单日日期轴且全部指标为 0；切回丽斯未来恢复 36 条 ServiceSession。
- 该专项未发现需要修复的前端缺口，现有租户切换和页面卸载逻辑满足要求。

### 19.2 Presence 多连接

新增 `internal/services/ws_presence_test.go`，覆盖：

- 同一租户/用户两个 WebSocket session 只统计一个在线客服。
- 断开一个 session 不结束 Presence，最后一个 session 断开才结束；重复关闭幂等。
- 手工 break 状态及原因不会被多连接 heartbeat 覆盖，恢复 idle 后 Dashboard 只统计一个空闲客服。
- 心跳超时滚动到新 Presence 行后仍只有一个活动行和一个实时客服。

通过命令：

```text
go test ./internal/services -run 'TestDashboardPresence|TestAgentPresence' -count=1
go test -race ./internal/services -run 'TestDashboardPresence|TestAgentPresenceConcurrentHeartbeatAndBreakKeepSingleActiveSession' -count=1 -p 1
```

### 19.3 原 8083 历史库副本

升级证据：

```text
dump: /tmp/agentdesk_legacy_8083_20260717.sql
sha256: aa02cfcc4f4e514c3ca0ee1ffd6c401cb255cf328390dabf7467cc6d23d98a05
copy database: agentdesk_legacy_8083_upgrade_20260717
source counts: users=117, migrations=28, messages=154, conversations=36
```

原 `8083` 数据库未写入。副本上的明确人工映射只有一项：

```text
platform admin remains tenant 0;
legacy agent team 1 remains tenant 1;
team 1 leader_user_id is cleared because the team has no member profiles and a platform account cannot lead a tenant team.
```

migration 历史审计发现 13、21、22、25、26 曾被并行分支改写或复用。处理规则：

1. migration 13 的历史 `normalize reply intent configs to seven categories` 是当前五类归一化之前的已知兼容前身，保留原记录并允许继续；migration 21 负责当前五类语义。
2. 21/22/25/26 的客服组绑定旧定义逐字段归档到 `t_migration_definition_archive`，保留 source id、version、remark、success、error、retry 和原时间，再释放版本号执行当前定义。
3. 兼容清单来自 Git 历史；未知 version/remark 冲突继续由 `validateMigrationDefinition` 拒绝。
4. 新增 SQLite 测试验证归档幂等、证据不丢失、未知冲突拒绝和 migration 21 最终五类意图。

副本还存在一组已删除测试会话 `1` 的孤儿记录，现存业务会话从 `219` 开始。经逐行确认后只在副本事务清理：

```text
1 message: request_id=manual_timeout, sender_type=ai
2 conversation_read_state
2 conversation_event_log
1 message_sync_log
```

migration 仍严格拒绝无父引用，不把该清理做成自动删数据逻辑。生产升级必须先重新列出孤儿并由业务确认，不能照抄副本清理结果。

最终副本结果：

```text
migration 1..61: all success
repeat migration: success, no duplicate facts
ServiceSession: 36, all estimated
ResponseSpan: 12
DispatchDecisionLog: 21
ServiceAnalyticsPolicy: 1
QualityTemplate: 1
TenantIntegrityAudit: 74 models / 87 tables / 202 relations / 0 violations
conversation_id orphan audit: 0
```

临时含凭据配置 `config/config.legacy-upgrade.local.yaml` 已删除，不进入提交。

### 19.4 最新全量门禁

2026-07-17 在上述增量之后重新执行：

```text
PASS go test ./... -count=1
PASS go vet ./...
PASS critical service race: analytics scope / evaluation / saved view / quality completion / intelligent dispatch / Presence
PASS pnpm typecheck
PASS pnpm lint: 0 error, 32 historical warnings
PASS Node: 137/137
PASS Next build: 47 static pages
PASS fresh SQLite: first + repeat migration
PASS fresh MySQL 8.4: first + repeat migration
PASS isolated MySQL seed + repeat seed + report:
     36 sessions / 39 spans / 30 decisions / 9 quality / 9 evaluations
     expectedCoreComplete=true
     expectedSimulationComplete=true
     simulationBaselineIntact=true
PASS fresh SQLite, fresh MySQL and runtime MySQL TenantIntegrityAudit: 74/87/202, 0 violations
PASS git diff --check
```

在运行中的 `8084` 数据库直接 Seed 后曾观察到 9 条额外 `assignment:*` 决策、pending 从 9 降到 0。这不是 Seed 泄漏，而是运行中自动派单 cron 真实接走了 9 条待派会话；隔离库没有 server/cron，首次和重复 Seed 均稳定为 30 条决策。后续静态仿真门禁必须使用隔离数据库，运行库的动态派单结果用于功能验收，不能拿来判定 Seed 幂等。

```text
批次与日期：租户切换、Presence 多连接与历史库升级收口，2026-07-17
目标：完成客服需求 1-6 的剩余运行验收；让真实旧库可审计地升级到 migration 61
Commit：未提交工作树
文件：internal/models/models.go、internal/migration/migration.go、internal/migration/migration_test.go、internal/services/ws_presence_test.go、docs/design/service-analytics-and-quality.md、本文
Model/AutoMigrate：新增 MigrationDefinitionArchive，只保存被复用 migration 的原始执行证据；不属于业务租户数据
DML migration：60/61 语义不变；migration runner 在执行当前定义前事务归档已知旧定义，未知冲突仍硬失败
DTO/enum/API/WebSocket：无契约变化；Presence 测试复用现有 CountUserSessions 和状态服务
权限：无新增、无隐藏权限
运行语义：不改变会话、派单、AI Runtime、模型授权、usage、Token、价格、余额或计费
验证命令与结果：migration 聚焦测试 PASS；Presence 普通/race PASS；旧库副本 migration 首次/重复 PASS；最新全仓 Go/vet/race、前端 typecheck/lint/137 Node/build、隔离 Seed/report 和 git diff --check PASS；TenantIntegrityAudit 74/87/202、0 违规；租户切换和空租户浏览器专项 PASS
SQLite/MySQL：全新 SQLite、全新 MySQL 首次/重复 migration PASS；隔离 MySQL 重复 Seed/report 三项 complete=true；MySQL 8.4 历史副本升级至 61 PASS；36 个历史轮次全部 estimated
浏览器桌面/移动：平台切租户旧状态清空和空租户单日零值 PASS；主页面桌面/移动沿用最近通过基线
与 main/ai-billing/customer-audit 同文件：models.go 和 migration runner 为共享高风险文件；只增加历史兼容与证据归档，不修改 AI/计费字段或语义；customer-audit 不更新
是否需要 rebase：2026-07-17 fetch 后不需要；提交和 push 前重新 fetch
建议合并顺序：migration 兼容模型/runner 归入提交 B，Presence 测试归入提交 C，验收与文档归入提交 F；仍是同一分支和一个 PR
回滚边界：代码回滚不自动删除 archive 表或 analytics 回填；生产尚未执行。副本可直接删除重建，原库不受影响
已知风险：A-F 提交、push 和唯一 integration PR 尚未完成
```

## 20. 2026-07-17 最终方案冻结与交付判定

### 20.1 产品方案冻结

客服需求文件第 1 至第 6 部分统一落在当前租户架构，不再创建平行系统：

| 业务职责 | 唯一入口 | 完整能力 |
| --- | --- | --- |
| 实时运营 | `/dashboard` | 当前排队、最长等待、人工/AI 接待、待回复、SLA、真实 Presence、客服组/小组容量与今日累计 |
| 历史分析 | `/dashboard/service-analytics` | 服务总览、响应效率、客服表现、质检与满意度、派单质量、来源分析六个 Tab，共用时间、组织和来源筛选 |
| 会话追溯 | `/dashboard/conversation-monitor` | `conversationId + sessionNo` 粒度的原文、来源、标签、小记、解决状态、分类、保存视图、范围导出和原会话操作 |
| 人工质检 | 会话记录内质检工作流 | 只评分目标 Assignment 的人工回复，客户和 AI 消息只作上下文；支持固定抽样、模板版本、禁忌项、评语和不可变完成结果 |
| 客户评价 | `/support/evaluation` 与运营分析 | 一次性 Token、邀评/参评/满意率分离、过期和重复提交幂等 |
| 实时回复 | `/dashboard/conversations` | 继续承担客服处理会话和 Presence 状态，不承载历史报表或质检配置 |

第 7、8 部分继续排除。当前没有独立留言业务状态机，留言类诉求通过未回复会话和来源分析表达；没有真实渠道动作前不新增重复报表。质检首版不调用大模型评分，不触碰 AI Runtime、模型授权、usage、Token 或计费。

### 20.2 权限、范围和租户边界

1. 平台管理员必须先选择活动租户；公司主管看本租户全量；客服组长只看负责客服组；普通客服只看本人服务轮次和本人已完成质检结果。
2. 权限控制能否进入和执行动作，service 负责强制数据范围；前端隐藏不能代替后端鉴权。
3. 新权限必须同时进入权限管理目录、默认角色、handler、前端导航/按钮和直接请求测试，不存在隐藏授权。
4. 所有运营事实、质检、评价、保存视图和导出均按 TenantID 隔离；切换租户时清空旧列表、详情、筛选维度和导出状态。

### 20.3 当前完成度

- **产品功能：已完成并验收。** 客服需求 1-6、三档角色、租户切换、Presence 多连接、历史库副本升级、SQLite/MySQL、并发、仿真、桌面/移动和最新全量门禁均已通过。
- **本地代码：已实现并形成六个语义提交。** `356b755 -> 14d2589 -> 6e58a88 -> edb0c31 -> acfa5f0 -> 3d65f5a` 全部位于 `codex/tenant-ai-integration`。
- **GitHub 交付：已进入唯一 PR。** `codex/tenant-ai-integration` 已推送并创建 PR #2；旧 customer-audit PR #1 已关闭，等待 PR #2 评审与合并。

不再新增产品功能作为本批前置。后续门禁发现问题时，在上述现有模型、service、API 和页面职责内修复；只有业务事实确实无法表达时，才允许新增 model/migration，并先更新设计与本交接。

### 20.4 唯一发布与主线合并

1. `codex/customer-audit` 永久冻结为只读历史；不再提交、不再同步、不再开 PR。
2. `codex/ai-billing` 已在 integration 历史中吸收，仅用于语义回归参考，不再单独进入 main。
3. B/A/C/D/E/F 已按共享事实、派单、运行捕获、运营页面、质检页面、仿真文档顺序提交；每个提交的 SHA、验证和回滚边界见第 21 节。
4. push 前重新 fetch，核对 migration 60/61、共享文件和祖先关系。
5. 最终只允许 `codex/tenant-ai-integration -> main` 一个 PR；旧 customer-audit PR 关闭并标记 `superseded by tenant-ai-integration`。

```text
批次与日期：客服需求 1-6 最终方案冻结，2026-07-17
目标：冻结 customer-audit；在 tenant-ai-integration 统一产品、权限、租户和唯一 PR 交付方案
Commit：未提交工作树
文件：docs/design/service-analytics-and-quality.md、docs/development/tenant-ai-integration-merge-handoff.md
Model/AutoMigrate：无新增；沿用已验证的 analytics 模型与 MigrationDefinitionArchive
DML migration：无新增；60/61 保持现有语义，远端仍无编号冲突
DTO/enum/API/WebSocket：无运行时代码改动；沿用已验收契约
权限：无新增；冻结平台管理员/公司主管/组长/客服的数据范围和显式权限原则
运行语义：不触碰 AI Runtime、模型授权、usage、Token、价格、余额或计费
验证命令与结果：git fetch PASS；main/ai-billing/customer-audit 均为 integration 祖先；migration 60/61 无远端冲突；文档一致性与 git diff --check PASS
SQLite/MySQL：本批无数据库改动；沿用第 19.4 节最新全量通过基线
浏览器桌面/移动：本批无页面改动；沿用已通过基线
与 main/ai-billing/customer-audit 同文件：仅 integration 权威文档；历史 customer-audit 交接不更新
是否需要 rebase：当前不需要；push 前重新 fetch
建议合并顺序：B -> A -> C -> D -> E -> F，同一分支、一个 PR
回滚边界：本批仅文档；可独立回滚，不影响运行和数据
已知风险：代码仍未提交、未 push、未创建唯一 PR
```

## 21. integration 语义提交记录

### 21.1 提交 B：分析事实与权限契约

```text
批次与日期：提交 B，2026-07-17
目标：提供运营事实、质检、评价、Presence、保存视图、权限和历史 migration 兼容的共享契约
Commit：356b755 feat(analytics): add tenant service fact contracts
文件：models/service_analytics、analytics enums/DTO/repository、权限常量与 migration 60、migration archive runner、租户完整性审计、前端生成枚举
Model/AutoMigrate：新增 analytics 事实模型和 MigrationDefinitionArchive；Conversation/Assignment/AgentTeam 增加向后兼容字段
DML migration：提交 60 权限同步；61 留在提交 C；已知历史 migration 定义先归档再执行当前定义，未知冲突继续硬失败
DTO/enum/API/WebSocket：新增 DTO/enum 契约；本提交不挂载新路由或 WebSocket 行为
权限：新增运营分析、会话记录、人工质检、评价、保存视图和 Presence 显式权限
运行语义：不改变 AI Runtime、模型授权、usage、Token、价格、余额或计费
验证命令与结果：当前工作树 migration/service/typecheck PASS；独立 356b755 worktree go test ./... PASS、go vet ./... PASS
SQLite/MySQL：沿用第 19.4 节新鲜与历史库门禁；本提交 migration 聚焦测试 PASS
浏览器桌面/移动：无页面行为提交
与 main/ai-billing/customer-audit 同文件：models.go、migration runner、权限和生成枚举；采用兼容新增并保留 integration 既有 AI/租户契约
是否需要 rebase：提交时不需要；push 前重新 fetch
建议合并顺序：本提交为第一条，随后 A -> C -> D -> E -> F
回滚边界：代码回滚不会自动删除已 AutoMigrate 的列或 MigrationDefinitionArchive 数据；未在生产执行
已知风险：其余 A/C/D/E/F 尚未提交；新 API 尚未挂载
```

### 21.2 提交 A：稳定自动派单

```text
批次与日期：提交 A，2026-07-17
目标：在当前客服组、排班和租户边界内完成公平、连续、可解释的自动派单
Commit：14d2589 feat(dispatch): balance automatic team assignments
文件：客服组派单配置、派单 DTO/handler/service/workbench、负载与模型决策、实时调度、派单页、测试和设计文档
Model/AutoMigrate：使用 B 已提交的 DispatchMode、DispatchWeight、Assignment 快照字段和 DispatchDecisionLog
DML migration：无新增
DTO/enum/API/WebSocket：扩展客服组派单模式、任务权重/优先级/置信度和客服加权负载；沿用现有派单路由与事件
权限：派单读写统一要求 conversation.handover；组长仅管理负责组
运行语义：人工、规则均衡、智能均衡三模式；模型只能在公平候选集内选择，失败或低置信度降级规则；值班、禁用、最大并发和租户边界始终硬校验
验证命令与结果：聚焦 dispatch/handoff/team Go 测试 PASS；typecheck PASS；派单展示 Node 测试 PASS；独立暂存快照全仓 go test ./... PASS、go vet ./... PASS
SQLite/MySQL：SQLite service 测试 PASS；完整 MySQL 门禁留待 F 再跑
浏览器桌面/移动：沿用已验收派单页基线；本提交布局约束测试 PASS
与 main/ai-billing/customer-audit 同文件：模型 usage 常量、Assignment/Route/Message、客服组 API 和 locale；保持当前模型授权与 usage 证据语义
是否需要 rebase：提交时不需要；push 前重新 fetch
建议合并顺序：B -> A 已完成，下一步 C
回滚边界：可关闭实时调度并回滚 service/UI；已写 Assignment 和决策证据保留，不删除 usage
已知风险：C/D/E/F 尚未提交；完整 MySQL 和浏览器终验在 F
```

### 21.3 提交 C：运行时捕获与工作流 API

```text
批次与日期：提交 C，2026-07-17
目标：把会话生命周期、响应、Presence、人工质检、评价和保存视图接入租户范围 API
Commit：6e58a88 feat(analytics): capture service lifecycle workflows
文件：analytics builders/services/handlers/routes、事件订阅、会话/消息/路由捕获、WebSocket Presence、migration 61、统一前端 API client
Model/AutoMigrate：使用 B 已提交事实模型；无新增模型
DML migration：61 回填服务轮次、响应、派单证据、策略与质检模板；历史数据标记 estimated
DTO/enum/API/WebSocket：挂载运营分析、服务轮次、质检、抽样、保存视图、Presence、评价和公开评价 API；WebSocket 多连接维护单一 Presence
权限：handler 显式权限，service 强制平台/主管/组长/客服数据范围和 TenantID
运行语义：事实写入不替代业务主表；仅人工 Assignment 回复进入质检；完成质检不可变；评价 Token 幂等
验证命令与结果：analytics/quality/evaluation/presence 聚焦 Go PASS；关键 race PASS；typecheck PASS；独立 B+A+C 快照全仓 go test ./... PASS、go vet ./... PASS
SQLite/MySQL：SQLite 全仓 PASS；完整 MySQL 与历史副本证据沿用第 19.4 节并在 F 终验
浏览器桌面/移动：本提交无页面入口；API 范围沿用已验收基线
与 main/ai-billing/customer-audit 同文件：routes/server、Conversation/Message/Route、WebSocket 和 API client；只兼容新增，不改变 AI Runtime 或计费
是否需要 rebase：提交时不需要；push 前重新 fetch
建议合并顺序：B -> A -> C 已完成，下一步 D
回滚边界：可关闭新路由和捕获；已生成事实、质检和评价数据保留，migration 不删除
已知风险：D/E/F 尚未提交；最终页面与数据库终验在 F
```

### 21.4 提交 D：总览与运营分析页面

```text
批次与日期：提交 D，2026-07-17
目标：用精确事实升级实时总览，并提供一个六 Tab 运营分析入口
Commit：edb0c31 feat(analytics): add operations dashboards
文件：dashboard-home、service-analytics 页面/质量运营组件、dashboard API 类型、导航与权限测试；删除四个旧近似总览组件
Model/AutoMigrate：无
DML migration：无
DTO/enum/API/WebSocket：只消费 C 的 AnalyticsOverview 和工作流 API，不在页面重算指标
权限：总览、运营分析、会话记录和派单入口分别使用显式权限
运行语义：实时快照与今日累计分开；历史、响应、客服、质量、派单和来源共用筛选、范围与钻取
验证命令与结果：当前工作树 typecheck PASS；导航/权限 Node PASS；独立 B+A+C+D 快照 Node 135/135 PASS、tsc PASS、Next build 46 pages PASS
SQLite/MySQL：无数据库改动
浏览器桌面/移动：沿用已验收无横向溢出基线；最终截图复核留在 F
与 main/ai-billing/customer-audit 同文件：dashboard 组件、导航和 API 类型；不修改 AI/计费页面或语义
是否需要 rebase：提交时不需要；push 前重新 fetch
建议合并顺序：B -> A -> C -> D 已完成，下一步 E
回滚边界：可独立恢复旧总览组件和移除分析入口，不删除事实数据
已知风险：E/F 尚未提交；公开评价和会话质检页面待 E
```

### 21.5 提交 E：会话记录、人工质检、Presence 与评价页面

```text
批次与日期：提交 E，2026-07-17
目标：完成主管/组长的会话追溯与人工质检，以及客服 Presence 和客户评价体验
Commit：acfa5f0 feat(quality): add human service review workflows
文件：conversation-monitor、session-workflow、conversations Presence、support/evaluation 和权限测试
Model/AutoMigrate：无
DML migration：无
DTO/enum/API/WebSocket：消费 C 已提交的服务轮次、质检、抽样、保存视图、Presence 和评价 API
权限：会话记录、导出、小记、质检查看/执行、抽样和 Presence 动作独立；普通客服质检结果只读
运行语义：原分配/转派/已读/关闭状态机保留；质量证据只含目标 Assignment 的人工消息；评价 Token 一次性且幂等
验证命令与结果：当前工作树 typecheck PASS；会话权限/评价 Node PASS；独立 B+A+C+D+E 快照 tsc PASS、Node 137/137 PASS、Next build 47 pages PASS
SQLite/MySQL：无数据库改动
浏览器桌面/移动：沿用已验收主流程和无溢出基线；最终运行截图复核留在 F
与 main/ai-billing/customer-audit 同文件：会话监控和实时会话页；保留 integration 已有全部账号入口与来源企微次级高亮
是否需要 rebase：提交时不需要；push 前重新 fetch
建议合并顺序：B -> A -> C -> D -> E 已完成，下一步 F
回滚边界：可独立回滚页面；服务事实、质检和评价数据不删除
已知风险：完整数据库终验、push 和唯一 PR 尚未完成
```

### 21.6 提交 F：租户仿真与单分支权威文档

```text
批次与日期：提交 F，2026-07-17
目标：补齐丽斯未来租户运营事实仿真、重复 Seed/report/cleanup 证据，并冻结唯一分支和唯一 PR 规则
Commit：3d65f5a test(seed): complete tenant service simulation
文件：cmd/customer_audit_seed、AGENTS.md、客服组织/租户/运营分析设计文档、customer-audit 历史交接标废头、integration 唯一合并交接
Model/AutoMigrate：无新增模型；仿真覆盖 B/C 已提交事实模型
DML migration：无新增；沿用 60/61，不修改历史定义或 remark
DTO/enum/API/WebSocket：无新增运行契约
权限：无新增；仿真验证现有三档角色和 TenantID 范围
运行语义：重复 Seed 清理专用仿真租户及仿真会话 usage 关联，不删除平台或其他租户记录，不改变 Token、价格、余额或计费语义
验证命令与结果：go test ./cmd/customer_audit_seed PASS；提交前全仓 go test ./...、go vet、typecheck、lint（0 error）、Node 137/137 PASS；git diff --check PASS
SQLite/MySQL：最终发布门禁已复跑；全新 SQLite/MySQL 首次与重复 migration、隔离 Seed/repeat/report/cleanup 和 TenantIntegrityAudit 74/87/202、0 违规均通过
浏览器桌面/移动：沿用 1440x900、390x844 主流程验收基线
与 main/ai-billing/customer-audit 同文件：只更新现有 Seed 和权威文档；customer-audit 保持只读，不修改 AI Runtime 或计费链路
是否需要 rebase：提交时不需要；push 前重新 fetch
建议合并顺序：B -> A -> C -> D -> E -> F 已全部完成，同一分支、一个 PR
回滚边界：可独立回滚仿真和文档；不删除已产生的生产运营记录或 usage 证据
已知风险：push 和唯一 PR 尚未完成
```

## 22. 2026-07-17 最终发布门禁

最终 fetch 后远端基线未变化：

```text
origin/main                         e67e207
origin/codex/ai-billing             f2d2da4
origin/codex/customer-audit         c706815
origin/codex/tenant-ai-integration  2ea04c8
```

四个远端引用均为 integration HEAD 的祖先，远端 integration 没有本地缺失提交；四个远端引用均未占用 migration 60/61，不需要 rebase 或改 migration 编号。

```text
PASS go test ./... -count=1
PASS go vet ./...
PASS critical service race
PASS pnpm --dir web typecheck
PASS pnpm --dir web lint: 0 error, 32 historical warnings
PASS Node tests: 137/137
PASS Next build: 47 static pages
PASS fresh SQLite: first + repeat migration
PASS fresh MySQL 8.4: first + repeat migration
PASS fresh SQLite TenantIntegrityAudit: 74/87/202, 0 violations
PASS fresh MySQL TenantIntegrityAudit: 74/87/202, 0 violations
PASS isolated MySQL seed + repeat seed + report:
     36 sessions / 39 spans / 30 decisions / 9 quality / 9 evaluations
     expectedCoreComplete=true
     expectedSimulationComplete=true
     simulationBaselineIntact=true
PASS isolated cleanup: simulation tenant facts all return to 0
PASS post-cleanup MySQL TenantIntegrityAudit: 74/87/202, 0 violations
PASS git diff --check
```

隔离 Seed 复用同一测试容器已有的启用 LLM 测试配置，只建立授权和引用，不调用真实模型，不新增 usage/Token/价格/余额事实。邀请码加密密钥只通过单次进程环境变量注入；临时数据库与 `/tmp` 配置不提交。

当前只允许维护、评审和合并 PR #2。禁止 push `codex/customer-audit`，禁止重新打开 PR #1，禁止创建第二个客服 PR。

## 23. GitHub 发布记录

```text
2026-07-17
push: codex/tenant-ai-integration 2ea04c8..6f371c5
PR: https://github.com/520skyincloud/agentdesk/pull/2
base: main
head: codex/tenant-ai-integration
state at creation: OPEN, ready for review
old PR: https://github.com/520skyincloud/agentdesk/pull/1
old PR action: commented as superseded and CLOSED
GitHub account: Archi8848
```

PR #2 正文已列出需求范围、仅人工回复质检边界、migration 60/61、租户数据范围、验证证据和 AI/计费不变约束。后续修复只允许继续推到 `codex/tenant-ai-integration` 并更新 PR #2，不得恢复多分支同步。

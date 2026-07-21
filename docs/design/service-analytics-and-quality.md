# 租户客服运营分析、会话记录与人工回复质检

> 状态：客服需求 1-6 已完整实现并通过最终门禁；唯一 PR：`#2 codex/tenant-ai-integration -> main`
>
> 更新日期：2026-07-17
>
> 唯一实施分支：`codex/tenant-ai-integration`
>
> 目标运行环境：`http://127.0.0.1:8084`
>
> 合并交接：`docs/development/tenant-ai-integration-merge-handoff.md`

## 1. 决策摘要

本方案把客服需求文件第 1 至第 6 部分融入当前租户架构，不照搬七鱼的多套平行报表页面：

1. `/dashboard` 保留为实时运营决策总览。
2. 新增 `/dashboard/service-analytics`，集中承载历史运营分析。
3. `/dashboard/conversation-monitor` 升级并改名为“会话记录”，负责检索、钻取、服务小记和人工质检。
4. `/dashboard/conversations` 继续只负责客服实时回复。
5. 质检只分析人工客服回复；客户和 AI 消息只能作为上下文，不能进入评分证据或质检分母。
6. 所有事实、筛选、权限和导出均受 `TenantID` 与组织数据范围约束。
7. `codex/customer-audit` 从本方案起只作为只读迁移来源，不再开发、不再单独合并。所有实现、修复、测试和文档只更新到 `codex/tenant-ai-integration`。

客服需求文件第 7、8 部分为知识库常用语和在线文档，不属于本方案范围。

## 2. 复核后的真实状态

本节描述 2026-07-17 `codex/tenant-ai-integration` 的已提交实现。六个语义提交已经按依赖顺序形成，`codex/customer-audit` 已冻结为历史只读来源，不再作为完成度或发布来源。

### 2.1 已在 integration 形成的底座

| 能力 | 当前状态 | 结论 |
| --- | --- | --- |
| Tenant、公司切换、邀请注册、角色权限 | 已提交并在 integration 真实运行 | 直接复用，不另建租户或权限系统 |
| 客服组、小组、排班、客服档案 | 已提交并具备组织范围 | 作为统计维度、数据范围和 Presence 主体 |
| 人工、规则、智能自动派单 | 已提交并通过全量回归 | 公平候选集、模型择优和规则降级共用现有派单状态机 |
| 会话、消息、RouteState、Assignment、事件日志 | 已有业务主表和状态机 | 继续作为业务真相，运营事实不得反向控制它们 |
| 服务轮次、响应分段、数据质量 | 已提交 | 已有 model/repository/capture/backfill/API 和定向测试 |
| 服务小记与会话导出 | 已提交 | 会话记录可编辑小记、自定义列、保存视图并按当前范围导出 |
| 人工回复质检 | 已提交 | 已有模板版本、系统指标、禁忌项、人工证据校验、固定抽样和评分界面 |
| Presence | 已提交 | 客服工作台已有 online/idle/busy/break 控件，休息必须填写原因；SQLite/race、真实 MySQL 并发以及同一用户多连接、部分断开、最终断开、心跳超时和休息恢复专项均已通过 |
| 客户评价 | 已提交 | 已有一次性 Token、公开评价页、后台评价列表和满意度聚合；并发幂等、过期、跨租户和真实公开提交/重复访问已通过 |
| 实时总览 | 已提交 | 已区分当前快照和今日累计，并复用运营事实与角色范围；历史小组和待派目标组快照、三档直接请求范围已修复，1440x900/390x844 无页面横向溢出 |
| 运营分析 | 六 Tab 页面与 API 已形成可操作初版 | P50/P90、四类客服视图、URL 钻取、来源质检/满意度、派单证据细分和汇总导出已接入 |
| 会话记录 | 已切换服务轮次 API | 原分配/重派/转派/已读/关闭已恢复；统一查询、高级筛选、标签、自定义列、保存视图、导出、抽样、小记、评价邀请和人工质检已接入；人工证据、三档角色范围、租户切换与桌面/移动布局已验收 |
| 丽斯未来测试租户与仿真数据 | 运营仿真已补齐并通过幂等/清理测试 | 已生成服务轮次、响应分段、Presence、人工质检、评价和完整派单证据；重复 Seed 会同时清理仿真租户遗留 usage 关联，且不触碰平台 usage |

### 2.2 当前代码门禁

最近一次完整代码门禁基线已通过：

```text
go test ./... -count=1
go vet ./...
go test ./internal/services ./internal/builders ./internal/handlers/dashboard ./internal/bootstrap -count=1
go test ./cmd/customer_audit_seed -count=1
go test -race ./internal/services -run 'TestConversationEvaluationConcurrentSubmitIsIdempotent|TestReportViewPresetOwnershipAndDefaultSelection|TestConcurrentQualityCompletionCreatesOneImmutableResult|TestAnalyticsDirectAccessRequiresSourceAndAssignmentScope' -count=1 -p 1
pnpm --dir web typecheck
pnpm --dir web lint
cd web && node --test $(rg --files -g '*.test.mjs')
pnpm --dir web build
git diff --check
```

`pnpm lint` 无 error，当前全仓有 32 个历史 warning；会话记录页本批新增的两个表达式 warning 已修复。全量前端 Node 测试为 137/137 通过，静态导出构建 47 个页面通过；`/support/evaluation` 已按现有静态导出模式使用 `Suspense` 承载客户端查询参数。

隔离 MySQL 8.4 数据库已经完成首次 migration、丽斯未来 Seed、report 和租户完整性审计。仿真稳定产出：

```text
36 ServiceSessions
39 ResponseSpans（27 waiting / 12 replied）
12 PresenceSessions
9 QualityInspections（6 completed）
54 QualityInspectionItems
9 Evaluations（6 submitted）
30 DispatchDecisionLogs（12 selected / 6 fallback / 9 failed / 3 override）
1 ServiceAnalyticsPolicy
```

重复 Seed 结果一致，cleanup 后测试租户的上述业务事实归零；平台默认模板和策略按设计保留。仿真本身不调用真实模型，不产生新的 AIUsageEvent、Token、价格或计费数据。2026-07-21 起派单只使用人工或确定性规则，不再产生派单模型 usage；历史仿真或旧版本留下的模型派单 usage 仍按租户和会话保留为只读审计证据。生命周期测试继续覆盖正常关联、历史孤儿和平台无关记录。2026-07-17 修复时租户完整性审计结果为 74 个租户模型、87 张必需表、202 条关系、0 违规，36 个 ServiceSession 均为 exact；当前发布数字以唯一合并交接的最新门禁为准。

真实 MySQL 8.4 并发门禁已通过：评价 Token 幂等提交、Completed 质检只有一个成功完成者、Presence 心跳与休息并发后只保留一个活动时段。

浏览器已经完成 `1440x900` 与 `390x844` 主流程验收：`/dashboard`、运营分析、会话记录、派单页和实时会话页均无页面级横向溢出；运营分析六个 Tab、仅人工质检证据、全部账号下来源企微次级高亮、公开评价真实提交和已提交状态均已验证。公开评价无 Token 的错误状态同样无溢出，控制台无 warning/error。

该完整基线之后又完成了组长派单范围、导出超限显式失败、普通客服质检结果只读、连续自然日趋势补零、保存视图所有权与恢复、三档真实账号、租户切换/空租户、Presence 多连接和历史库副本升级专项。2026-07-17 已在最新增量后重新执行本节全量门禁：全仓 Go、vet、关键 race、typecheck、lint、137 个 Node 测试、47 页面 build、全新 SQLite/MySQL 首次与重复 migration、隔离 MySQL 重复 Seed/report 和 74/87/202 完整性审计全部通过。

已修复阻断：

1. 历史查询不再预加载当前客服花名册；只有实时 Dashboard 显式设置 `IncludeCurrentAgentRoster` 时才补当前人员和负载。
2. Presence 历史时长按保存的 TeamID 快照归属；缺少小组、门店或企微账号快照时，只补充已经由服务事实选中的客服，不用当前成员关系猜测。
3. `serviceAnalytics.export`、`conversationEvaluation.view` 和质检抽样列表已经有真实路由、service 范围和前端 API；`serviceAnalytics.managePolicy` 权限码已统一。
4. P50/P90、四类客服视图、来源质检/满意度、URL 钻取、评价页、Presence 控件、抽样批次和模板管理已完成定向测试或 TypeScript 检查。
5. Completed 质检已经增加事务内锁定、事务内状态复核和条件更新保护；派单失败证据也已按可解析客服组补归属，无法归属的记录不再向组长或普通客服暴露。
6. 会话记录已经恢复原业务操作，列表和导出使用 typed `ServiceSessionQuery`，并补齐 SLA、渠道、解决状态、分类和标签筛选及标签编辑。

本轮新增完成：待派服务轮次会保存目标客服组快照，释放到全局池时清除组归属；会话小记、质检详情/保存和抽样批次同时校验来源范围与客服组/本人范围；Presence 写入已串行化并完成同用户多连接生命周期；质检完成、评价 Token、保存视图均补齐并发、幂等、所有权和跨租户测试。平台切换租户后旧列表、详情、维度和筛选状态已清空，空租户今日范围保持连续单日零值。

三档角色专项已经完成：公司主管可见本租户 36 个服务轮次、9 条质检和 9 条评价；客服组长 001 只见其负责的客服组 2 下 10 个服务轮次、3 条质检和 3 条评价；客服 003 只见本人 4 个服务轮次和 2 条质检，评价后台接口按权限拒绝。浏览器中客服已完成质检以 `95 / 100` 只读展示且没有保存/完成动作，组长和公司主管对草稿质检可编辑并可保存或完成；导航按角色实际权限显示。

发布状态：`codex/tenant-ai-integration` 已推送，唯一 integration PR #2 已创建；旧 customer-audit Draft PR #1 已标记 superseded 并关闭。

因此当前结论是：**客服需求第 1 至第 6 部分的产品主流程、角色与数据范围、安全并发、仿真、租户切换、Presence 多连接、新鲜及历史数据库、桌面/移动主流程和最新全量门禁已经完成并已提交到唯一 integration 分支；进入唯一 integration PR 后才算 GitHub 交付完成。**

### 2.3 客服需求第 1 至第 6 部分完成矩阵

| 客服需求板块 | 当前达到程度 | 已有能力 | GitHub 状态 |
| --- | --- | --- | --- |
| 1. 实时数据平台 | 主流程、目标组范围、三档角色、仿真、租户切换、多连接、布局和全量门禁已验收 | 当前排队、SLA、人工/AI 接待、待回复、在线/空闲/忙碌/休息、组/小组负载、今日累计 | PR #2 |
| 2. 历史数据平台 | 主流程、新鲜及原 8083 副本 MySQL、全量门禁已验证 | 六 Tab、趋势、来源、客服、质检、满意度、派单、导出和历史数据质量 | PR #2 |
| 3. 响应度报表 | 指标、页面、分位数、仿真、钻取、连续日期、空样本和全量门禁已验收 | 排队、首响、客户总等待、连续响应、SLA、平均/P50/P90、分布钻取和连续自然日补零 | PR #2 |
| 4. 坐席报表 | 页面、三档范围、Presence 仿真、多连接、MySQL 并发、布局和全量门禁已验收 | 工作量、响应、质量、出勤四视图，Presence 与排班/小组维度 | PR #2 |
| 5. 会话记录 | 功能、三档范围门禁、人工证据、租户切换、布局和全量门禁已验收 | 服务轮次列表、原文、原业务操作、来源、客服、SLA、标签、数据质量、小记、解决状态、分类、自定义列、保存视图恢复、统一导出、超限提示和钻取 | PR #2 |
| 6. 人工回复质检 | 主流程、三档角色、仿真、不可变、越权、MySQL 并发、历史库、浏览器评分和全量门禁已验收 | Assignment 粒度、仅人工证据、模板版本、评分/系统指标/禁忌项、抽样批次、事务内已完成锁定、评价列表、普通客服本人结果只读 | PR #2 |

### 2.4 发布状态

1. 只维护并合并 PR #2；不得重新打开或单独合并 customer-audit PR #1，不得为本批再创建第二个 PR。
2. 客服截图中的“留言报表”不单独复制。当前系统没有独立留言状态机，未回复/离线进入的客户消息统一进入会话记录和未回复分析；未来渠道出现真实留言动作时，只增加来源类型和筛选，不新建平行报表。

派单失败证据范围和 Completed 质检事务内不可变性已于本次复核中完成代码加固，不再列为待设计项；它们保留在最终安全与数据库回归中，未通过全量门禁前仍不能标为交付完成。

### 2.5 从当前状态到客服要求完整交付的收口方案

三档角色、租户切换、Presence 多连接、历史库副本、最新全量门禁、语义提交、push 和唯一 PR 已经完成。不再新增平行页面、重复报表、隐藏权限或第二套状态机：

1. **单分支发布**：最终 fetch、migration 60/61 核对和发布门禁已经通过；唯一 PR 为 `https://github.com/520skyincloud/agentdesk/pull/2`，旧 customer-audit PR #1 已关闭。

最终门禁若发现验收缺口，应在现有 service、API 和页面职责内修复；只有真实业务事实无法表达时才允许继续修改 model/migration，并必须先更新本方案与合并交接。唯一 PR 建立前，不得写成“已进入 main”。

### 2.6 客服原始截图逐项复核结论

本表按 Word 文件第 1 至第 6 部分逐项复核。第 7、8 部分继续排除，不进入代码、页面或验收范围。

| 原始要求 | 本项目落点 | 当前判断 | GitHub 状态 |
| --- | --- | --- | --- |
| 实时数据平台：正在咨询、排队、今日会话、排队失败、满意度、客服活动 | `/dashboard` 当前快照与今日累计 | 已用 ServiceSession、ResponseSpan、Presence 和派单事实重建；不再用 15 分钟活跃近似在线；三档角色、租户切换、多连接和全量门禁已验收 | PR #2 |
| 历史总览：会话、接入、访客、转派、首响、时长、满意度、24 小时一次解决、排队 | 运营分析“服务总览”与“质检与满意度” | 主体已接入；“相对满意度”拆为邀评率、参评率、满意率；当前只称“24 小时无重复咨询率”；原 8083 副本已回填且全部历史轮次明确标为 estimated | PR #2 |
| 响应度：首响、平均响应、排队、回复/接入、趋势与分布 | 运营分析“响应效率” | 已有平均/P50/P90、SLA、趋势、分布和会话钻取；连续日期、零样本和全量门禁已验收；答问比不作为绩效指标 | PR #2 |
| 坐席报表：工作量、质量、质检、考勤、服务绩效 | 运营分析“客服表现”四分组视图 | 已按工作量、响应、质量、出勤拆分，避免三十多列宽表和不可解释综合排名；三档数据范围、Presence 多连接和全量门禁已验收 | PR #2 |
| 会话记录：多条件筛选、会话字段、消息详情、服务小记 | 升级后的 `/dashboard/conversation-monitor` | 已统一为服务轮次列表、消息原文、来源、标签、小记、分类、解决状态、自定义列、保存视图和同条件导出；原业务操作、三档角色、租户切换、历史估算标识和全量门禁已验收 | PR #2 |
| 在线质检：筛选/随机抽样、服务小记、五项评分、禁忌项、评语 | 会话记录内的人工回复质检工作流 | 已实现固定抽样批次、模板版本、客观响应指标、人工证据、评分和不可变完成结果；只质检被分配客服的人工回复；主管/组长可操作、客服本人只读和全量门禁已验收 | PR #2 |

“GitHub 交付完成”不采用文件存在定义：页面能操作、接口有权限和数据范围、指标口径可追溯、SQLite/MySQL 可迁移、历史数据质量可识别、原会话/派单/AI/计费链路回归通过，且已经进入唯一 integration PR，以上条件缺一不可。当前上述条件均已满足，下一步是评审并合并 PR #2。

## 3. 页面与职责

### 3.1 实时总览 `/dashboard`

面向公司主管和客服组长的当前态势，明确分成“当前快照”和“今日累计”：

- 当前排队数、最长排队时长、即将超 SLA 数。
- 人工处理中、AI 接待中、待客服回复数。
- 实际在线、空闲、忙碌、休息客服数。
- 当前客服组/小组负载、可用容量和无排班风险。
- 今日进入人工池、成功分配、人工首响、未回复、转派数。
- 点击指标跳转到带 URL 筛选的派单页、会话记录或运营分析。

总览不放完整历史报表筛选，也不重复质检和满意度管理。

### 3.2 运营分析 `/dashboard/service-analytics`

使用一套全局筛选：时间、客服组、小组、客服、门店、企微员工号、渠道、数据质量。包含六个 Tab：

1. **服务总览**：会话量、客户量、人工接入、AI/人工构成、未回复、转派、重复咨询、趋势和状态分布。
2. **响应效率**：排队、人工首响、客户总等待、连续响应、SLA 达标率和时长分布。
3. **客服表现**：任务量、人工消息、服务时长、在线/忙碌/休息时长、响应与质检；不生成模糊综合排名。
4. **质检与满意度**：可质检数、覆盖率、通过率、平均分、禁忌项、邀评、参评、满意率和评分分布。
5. **派单质量**：分配成功、智能选择、规则降级、失败、人工覆盖、过期决策、任务权重和班次负载离散度。
6. **来源分析**：按门店、企微员工号和渠道对比会话、人工率、响应、未回复、质检和满意度。

每个图表都必须能钻取到会话记录；空数据统一返回 `[]`，切换租户时先清空旧数据再加载。

“客服表现”内部使用四个分组视图，避免复制七鱼三十多列的横向大表：

- **工作量**：分配数、有效人工接入数、处理轮次、人工消息数、未回复数、转派数和服务时长。
- **响应**：排队、首响、连续响应的平均值/中位数/P90、各 SLA 达标率和待首响压力。
- **质量**：可质检数、覆盖率、通过率、均分、禁忌项、邀评、参评和满意率。
- **出勤**：计划排班、首次上线、最后在线、在线/空闲/忙碌/休息时长和排班覆盖率。

不展示没有可靠来源的“电脑在线/手机在线/设备在线时长”，不把答问比当绩效，也不生成一个不可解释的综合排名。需要横向比较时允许用户选择列组并保存个人视图。

### 3.3 会话记录 `/dashboard/conversation-monitor`

保留现有页面入口和消息详情，升级为每个 `conversationId + sessionNo` 一条记录：

- 快捷视图：全部、进行中、待人工、待回复、待质检、已质检、SLA 超时。
- 高级筛选：客户、状态、日期、组/小组/客服、门店、企微员工号、渠道、标签、解决状态、数据质量。
- 自定义列和个人保存视图。
- 范围化 CSV 导出，导出条件与页面当前查询完全一致。
- 详情分为会话信息、聊天原文、服务小记/质检三部分。
- 支持运营分析通过 URL 查询参数直接钻取到同一数据集合。
- 保留原会话监控的分配、重新派单、转派、标记已读和关闭能力；ServiceSession 只负责定位历史轮次，业务动作仍作用于原 Conversation/Assignment/RouteState。

### 3.4 实时会话 `/dashboard/conversations`

该页面继续只处理当前分配给客服的会话，不加入历史报表、质检模板或统计策略配置。

### 3.5 客户评价 `/support/evaluation`

使用一次性 Token 打开轻量评价页，只显示当前会话必要信息。提交后 Token 失效，不能查看其他客户、会话或租户数据。

### 3.6 客服截图需求的融入方式

| 客服截图能力 | 本项目落点 | 处理原则 |
| --- | --- | --- |
| 实时数据 | `/dashboard` | 当前快照与今日累计分开，不复制实时页面 |
| 历史总览 | 运营分析“服务总览” | 保留会话、接入、转派、响应、时长、复询和满意度 |
| 响应度报表 | 运营分析“响应效率” | 用响应分段事实；答问比降为诊断项且首版不展示 |
| 坐席报表 | 运营分析“客服表现”四个分组视图 | 复用客服组、小组、排班、Assignment、Presence 和质检 |
| 会话记录 | 升级现有会话监控 | 保留聊天详情，增加轮次、服务小记、筛选、导出和钻取 |
| 在线质检 | 会话记录中的人工质检工作流 | 只质检人工回复，支持筛选抽样和固定批次 |
| 留言报表 | 未回复视图与来源分析 | 当前无独立留言状态机，不新增重复业务模型 |

## 4. 统一指标口径

所有历史指标以 `conversationId + sessionNo` 为会话轮次，不按 Conversation 主表行数统计。

| 指标 | 口径 |
| --- | --- |
| 会话量 | 时间范围内开始的服务轮次数 |
| 独立客户数 | 服务轮次中去重 CustomerID |
| 进入人工池数 | 存在 `queueEnteredAt` 的服务轮次数 |
| 成功分配数 | 进入人工池后产生有效 Assignment 的轮次数 |
| 派单接入率 | 成功分配数 / 进入人工池数 |
| 有效人工接入率 | 产生至少一条有效人工回复的轮次数 / 进入人工池数 |
| 排队时长 | `assignedAt - queueEnteredAt` |
| 人工首响 | `firstHumanReplyAt - assignedAt` |
| 客户总等待 | `firstHumanReplyAt - queueEnteredAt` |
| 连续响应时长 | 一批连续客户消息开始到下一条同 Assignment 人工回复 |
| 未回复 | 人工轮次结束时仍有未闭合客户等待分段 |
| 转派率 | 有效转派轮次数 / 人工接入轮次数 |
| AI 接待率 | 存在 AI 回复的轮次数 / 全部轮次数 |
| 纯 AI 解决率 | 已关闭、存在 AI 回复、没有进入人工池的轮次数 / 已关闭轮次数 |
| 24 小时无重复咨询率 | 客户关闭轮次后 24 小时内没有同来源重复开启轮次的比例 |
| 质检覆盖率 | Completed 质检 Assignment 数 / 含人工回复的可质检 Assignment 数 |
| 质检通过率 | Completed 且通过数 / Completed 质检数 |
| 邀评率 | 成功发出评价邀请的轮次数 / 满足邀评条件的轮次数 |
| 参评率 | 已提交评价数 / 成功发出邀请数 |
| 满意率 | 达到租户满意阈值的评价数 / 已提交评价数 |

“24 小时无重复咨询率”不能在问题分类和解决状态稳定前命名为一次解决率。平均值必须同时提供中位数和 P90，防止少量长尾掩盖真实服务体验。

## 5. 数据设计

### 5.1 业务主表保持不变

Conversation、Message、ConversationRouteState、ConversationAssignment 和 ConversationEventLog 继续是业务状态真相。运营事实是可重建读模型，不反向控制回复、路由、派单或 AI Runtime。

`ConversationAssignment` 兼容新增 `SessionNo`，用于把分配、转派和质检准确归属到服务轮次。`SquadID`、`DispatchMode` 和 `WorkloadWeight` 继续作为当前派单事实；`DecisionConfidence` 只保留旧模型派单历史值，新人工/规则派单不再写入。

### 5.2 会话与响应事实

`ConversationServiceSession`：

- 唯一键：`TenantID + ConversationID + SessionNo`。
- 保存客户、渠道、门店、企微员工号和服务模式快照。
- 保存开始、进人工池、分配、人工首响、最后人工回复、关闭时间。
- 保存首次/最后 Assignment、客服组、小组、客服快照。
- 保存客户、AI、人工、系统消息数和转派数。
- 保存排队、首响、客户总等待、解决状态、咨询分类、标签快照和服务小记。

`ConversationResponseSpan`：

- 一批连续客户消息形成一个等待分段。
- AI 回复不能关闭该分段。
- 只有当前 Assignment 的人工客服回复可以关闭并归属该分段。
- 关闭、转派或会话重开时，旧分段必须明确标记 replied、abandoned 或 transferred。

### 5.3 数据质量标记

所有会话和响应事实增加：

- `FactOrigin`：`runtime`、`backfill`、`repair`。
- `DataQuality`：`exact`、`estimated`、`incomplete`。
- `EstimatedFieldsJSON`：列出无法从历史主表精确还原的字段。

新链路实时捕获的数据标为 exact。历史回填只有在时间点和 Assignment 轮次可证明时才标 exact，其余必须标 estimated 或 incomplete。页面默认展示全部，但对估算数据显示标记并支持筛选；导出必须包含质量字段。

### 5.4 客服在线事实

`AgentPresenceSession` 保存 online、idle、busy、break 时段，并增加：

- `BreakReason`：休息、培训、会议、离席等租户可选原因。
- `Source`：WebSocket、手动切换、系统超时、管理员修正。
- `ChangedBy`：人工切换或修正的操作人。
- `StartedAt / LastSeenAt / EndedAt / DurationSeconds`。

WebSocket 连接和心跳只能证明在线，不应把 15 分钟内活动直接等同于在线。忙碌取实际服务状态和当前任务；休息必须由显式状态切换产生。

### 5.5 派单决策事实

`ConversationAssignment` 记录最终派单结果；`DispatchDecisionLog` 记录一次决策尝试的候选快照、模型/规则结果、降级、失败、过期拒绝和人工覆盖原因。它不是第二张派单任务表，也不拥有会话状态。

成功结果统计以 Assignment 为准，决策过程和失败率以 DecisionLog 为准，避免重复累计。

### 5.6 服务小记与审计

`ResolutionCode`、`CategoryCode`、`SessionSummary` 和标签快照保存在服务轮次事实中。修改接口必须：

1. 校验租户和组织数据范围。
2. 记录旧值、新值和操作者到 ConversationEventLog。
3. 不改写原始消息和历史 Assignment。

### 5.7 质检模型

- `QualityTemplate / QualityTemplateItem`：租户模板和版本化评分项。
- `QualityInspection / QualityInspectionItem`：按 Assignment 分段保存草稿、完成结果、得分、评语和证据消息。
- `QualitySamplingBatch / QualitySamplingItem`：保存筛选条件、抽样种子、样本数、生成时间和固定样本，保证抽样可复核。

模板项支持三种规则：

- `score`：人工评分项。
- `metric`：由系统响应指标自动填充，不允许质检员随意改写。
- `prohibited`：禁忌项，命中后按模板策略一票否决。

默认模板为 100 分：服务礼貌 20、需求理解 25、信息准确 25、解决推进 20、合规安全 10，默认合格线 80。模板变更创建新版本，历史结果继续引用原版本。

### 5.8 满意度模型

`ConversationEvaluation` 同时承载邀请生命周期和提交结果：

- 关联 Tenant、服务轮次和目标 Assignment。
- 只保存 Token 哈希，不保存明文 Token。
- 保存邀请渠道、邀请时间、失效时间、提交时间、评分、标签和文本评价。
- Token 一次使用、到期失效，重复提交返回统一结果，不能泄露会话存在性。
- 转派场景默认按服务轮次评价；只有明确绑定目标 Assignment 时才进入客服个人满意度。

网页渠道可以直接发评价入口。企微员工号发送评价链接前必须重新核对 `wework.apifox.cn` 对应消息接口；协议没有明确支持时，只保留后台手动邀请或网页入口，不实现假发送。

### 5.9 保存视图

`ReportViewPreset` 按 TenantID、UserID 和页面保存筛选、列顺序、列显隐和排序。默认私有，不允许普通用户创建全租户公共视图。

## 6. 人工回复质检边界

质检单位是一次 ConversationAssignment 接待分段：

1. 质检池只纳入该 Assignment 时间区间内至少一条人工客服消息的分段。
2. 客户和 AI 消息只展示上下文，不可选为评分证据。
3. 转派前后由不同 Assignment 分别质检，不能把前一客服回复计入后一客服。
4. 保存时后端重新校验 Tenant、SessionNo、Assignment、Agent、模板项和每个证据 MessageID。
5. Draft 只保存进度；Completed 才进入覆盖率、通过率和平均分。
6. 响应速度来自 ConversationResponseSpan，质检员不能手填与系统事实冲突的时长。
7. 禁忌项命中时保留原始证据并按模板版本执行 hard fail。
8. 不调用大模型给 AI 回复打分，也不把知识候选“质检”混入会话质检。

## 7. 权限和数据范围

权限控制页面和动作，组织范围控制可见数据行；前端隐藏不能替代后端鉴权。

计划同步到原权限管理的权限：

```text
serviceAnalytics.view
serviceAnalytics.export
serviceAnalytics.managePolicy
conversationRecord.view
conversationRecord.annotate
conversationRecord.export
qualityInspection.view
qualityInspection.manage
qualitySampling.create
qualityTemplate.manage
conversationEvaluation.view
conversationEvaluation.invite
reportViewPreset.manage
agentPresence.update
```

角色范围：

- 超级管理员/平台管理员：必须先切换活动租户，再查看该租户；禁止跨租户聚合。
- 公司主管：查看和管理本租户全部数据。
- 客服组长：只查看自己负责客服组及其小组、客服、门店员工号和会话。
- 普通客服：只查看本人 Assignment、回复、在线和质检结果；不能查看其他客服绩效。
- 门店员工：不默认获得运营分析和会话质检权限。

账号仍只分配角色；只有管理员及以上可为角色配置权限。新增权限必须通过幂等 DML migration 进入全局权限管理，不允许 handler 内隐藏赋予。

## 8. API 规划

遵循现有 dashboard 平铺路由和统一 JsonResult：

```text
GET  /api/dashboard/service-analytics/overview
GET  /api/dashboard/service-analytics/dimensions
GET  /api/dashboard/service-analytics/policy
POST /api/dashboard/service-analytics/policy/update
GET  /api/dashboard/service-analytics/export

ANY  /api/dashboard/service-session/list
GET  /api/dashboard/service-session/:id
ANY  /api/dashboard/service-session/message_list
POST /api/dashboard/service-session/annotate
GET  /api/dashboard/service-session/export

ANY  /api/dashboard/quality-inspection/pool
GET  /api/dashboard/quality-inspection/:id
POST /api/dashboard/quality-inspection/save
ANY  /api/dashboard/quality-template/list
POST /api/dashboard/quality-template/save
ANY  /api/dashboard/quality-sampling/list
GET  /api/dashboard/quality-sampling/:id
POST /api/dashboard/quality-sampling/create

POST /api/dashboard/conversation-evaluation/invite
ANY  /api/dashboard/conversation-evaluation/list
GET  /api/evaluation/validate
POST /api/evaluation/submit

ANY  /api/dashboard/report-view-preset/list
POST /api/dashboard/report-view-preset/save
POST /api/dashboard/report-view-preset/delete
GET  /api/dashboard/agent-presence/current
POST /api/dashboard/agent-presence/update
```

运营分析汇总导出、质检抽样列表和评价后台列表已经接入真实路由和 service；三档组织范围及共享门店下的来源+分配双范围已通过直接请求、跨租户测试和三角色浏览器验收，空租户与平台租户切换专项也已通过。权限目录不允许保留“有权限但无接口”的空配置。

会话记录已经使用 typed `ServiceSessionQuery`，由 service 统一构建租户、组织范围、日期、状态、渠道、来源、标签、解决状态、分类、SLA、待回复和质检状态条件；列表、保存视图、URL 钻取、抽样候选和导出复用同一语义。禁止前端把当前页数据拼成“全量导出”。大数据导出达到阈值后再复用现有异步任务/通知能力，不在首版新增另一套任务中心。

## 9. 捕获与事务

运营事实接线遵循“业务事务成功后同事务记录关键事实，非关键聚合异步重算”：

1. 创建/重开会话时确保服务轮次事实。
2. 客户消息写入时更新消息计数并打开或延长响应分段。
3. 进入人工池时记录 queueEnteredAt。
4. Assignment 在同一事务写 SessionNo、分配时间和历史客服快照。
5. 人工消息写入时记录首响并关闭对应响应分段。
6. 转派、释放和关闭时结束或迁移未闭合分段。
7. WebSocket 和手动状态切换维护 PresenceSession。
8. DecisionLog 只读取 integration 已确定的派单候选与结果，不再运行一套派单算法。

事实捕获失败不能静默伪装成功。对不影响客户消息落库的统计异常，记录结构化错误并进入可重建修复任务；Assignment、SessionNo 和首响等强一致字段必须与对应业务写入同一事务。

## 10. 历史回填

integration 已提交：

- `000060_sync_service_analytics_permissions.go`：一次性同步本方案权限和默认角色关系。
- `000061_backfill_service_analytics_facts.go`：回填服务轮次、响应分段、默认质检模板和数据质量标记。

编号在提交时与 `origin/main@e67e207`、`origin/codex/ai-billing@f2d2da4`、`origin/codex/customer-audit@c706815` 不冲突；push 前仍必须再次 fetch 核对。

回填原则：

- 可证明的 Message.SessionNo、RouteState.SessionNo 和明确时间点才写 exact。
- 无 SessionNo 的历史 Assignment 只能按时间窗口近似归属，并标 estimated。
- 旧 Conversation 关闭后重开导致被清空的字段不能猜测，标 incomplete。
- 回填幂等，不覆盖 runtime exact 事实。
- 不删除、改写原消息或原 Assignment。已确认的并行分支 migration 版本复用记录必须先逐字段归档，再释放版本号执行当前定义；未知定义冲突继续硬失败，禁止直接改数据库 remark 冒充已执行。

2026-07-17 已在原 `8083` 数据库的只读 dump 副本完成真实升级验证：

- dump：`/tmp/agentdesk_legacy_8083_20260717.sql`，SHA-256 `aa02cfcc4f4e514c3ca0ee1ffd6c401cb255cf328390dabf7467cc6d23d98a05`；原库保持只读。
- 副本：MySQL 8.4 `agentdesk_legacy_8083_upgrade_20260717`；原始计数为 117 个用户、28 条 migration、154 条消息、36 个会话。
- migration 13 的历史“seven categories”定义被识别为当前五类归一化之前的兼容前身；21/22/25/26 的客服组绑定旧定义原样归档到 `MigrationDefinitionArchive`，随后执行当前同版本定义；任何未登记冲突仍拒绝升级。
- migration 39 的唯一冲突为 tenant 1 空客服组错误引用平台 `admin`。处理结果是 `admin` 继续属于平台 tenant 0，仅清空该空组的错误组长引用，不把平台账号强行迁入租户。
- 清理 1 组已删除会话 `1` 遗留的测试孤儿：1 条 `manual_timeout` AI 消息、2 条 read state、2 条 event、1 条 sync log；现存业务会话从 219 开始。该清理只发生在副本，生产执行前必须重新列出并确认同样证据。
- migration 61 产出 36 个 `estimated` ServiceSession、12 个 ResponseSpan、21 个 DispatchDecisionLog、1 套策略和 1 套质检模板；未把历史估算伪装成 exact。
- 重复 migration 无新增或重复事实；TenantIntegrityAudit 为 74 个租户模型、87 张必需表、202 条关系、0 违规。

## 11. 从当前工作树到完整交付的实施批次

所有批次只在 `codex/tenant-ai-integration` 形成提交，最终只建立一个 integration PR。这里的“已写”不等于“已完成”，必须达到对应门禁后才能标记完成。

### 批次 0：冻结单分支和统计基线（已提交）

- `codex/customer-audit` 已冻结，Git 已确认它是 integration 的祖先；后续只更新 `codex/tenant-ai-integration` 并建立一个 PR。
- 历史客服/小组聚合与当前 Presence/容量聚合已经分开，Assignment 小组快照回归已覆盖。
- 前端 API、权限码、枚举与页面类型已对齐，当前定向 Go、Seed、TypeScript、权限路由和 diff 检查通过。
- 自动派单与运营分析只在同一分支内拆成可评审提交，不再创建第二个开发分支或 PR。

### 批次 1：冻结事实契约（已提交，SQLite、新鲜及历史 MySQL 已验证）

- 固定 models、enums、repository、Assignment.SessionNo、FactOrigin/DataQuality/EstimatedFields。
- 固定权限 migration 60 和历史回填 migration 61；再次 fetch 后确认编号。
- SQLite 全仓测试、新鲜 MySQL 首次 migration、Seed、report 和租户完整性审计已通过；原 8083 历史库副本已重复升级到 61 并通过完整性审计。
- 通过后先提交独立 schema/permission 契约，后续页面提交只依赖该契约。

### 批次 2：稳定运行时捕获（已提交，范围与并发门禁已通过）

- 验证会话创建/重开、客户消息、人工池、Assignment、人工回复、转派、释放、关闭和 WebSocket/Presence 全链路。
- 对 `conversationId + sessionNo`、AI-only、转派前后 Assignment 和并发消息建立定向测试。
- 统计失败不得破坏客户回复；强一致的 Assignment.SessionNo 和首响事实必须跟随业务事务。
- 完整记录派单失败、降级、过期和人工覆盖决策，避免只统计成功 Assignment。
- 在服务轮次创建/进入人工池时已写入 `Conversation.CurrentTeamID` 快照；进入全局池会清零，并已验证本组待派可见、他组和全局池不可见。

### 批次 3：完成实时总览（主流程与三档角色已验收）

- Dashboard handler/service 已接收完整 AuthPrincipal，并复用 ServiceAnalyticsService 的租户与角色范围。
- 页面已用 ServiceSession、ResponseSpan 和 Presence 区分当前快照、今日累计及选定历史范围。
- 小组快照回归和主要跳转参数已处理；主管、组长、客服三档浏览器与直接 API 范围已验收。

### 批次 4：完成运营分析（六 Tab、三档范围与桌面/移动主流程已验收）

- 满意度、exact/estimated/incomplete、Presence 汇总、平均/P50/P90、派单指标和数据质量筛选已接入。
- 工作量/响应/质量/出勤四个客服视图、来源质检/满意度、失败/降级/过期/覆盖细分、URL 钻取和汇总导出已接入。
- 无归属派单失败日志已限制为平台/公司主管可见，单一可解析客服组已写入证据归属；角色范围、移动端表现、导出上限、空租户和租户切换清空状态已验收。

### 批次 5：完成会话记录与人工质检（主流程、三档角色、浏览器与 MySQL 并发已验收）

- Conversation Monitor 已改用 ServiceSession 列表，并保留原完整消息详情和 WebSocket 刷新。
- 服务小记、解决状态、咨询分类、保存视图、自定义列、范围化 CSV 导出、固定抽样和人工质检已接入。
- 抽样批次列表、模板管理入口和 Completed 质检不可修改回归已接入。
- 原监控页已有的分配、重新派单、转派、标记已读和关闭操作已经恢复，继续按原权限和原 Conversation 状态机执行，没有新增平行接口。
- typed `ServiceSessionQuery` 已统一列表、保存视图、钻取、抽样候选和导出，并补齐 SLA、渠道、解决状态、分类和标签筛选及标签编辑/展示。
- Completed 质检已在保存事务内锁定、复核并使用条件更新；race 和真实 MySQL 并发下只有一个成功完成者，来源范围与组/本人范围的直接访问均已加固，浏览器评分和仅人工证据已验收。公司主管和组长可编辑草稿并保存/完成，普通客服仅能只读本人已完成结果，三档动作与直接请求均已验收。

### 批次 6：完成 Presence 与真实满意度（已完成专项验收）

- 客服工作台已经有紧凑状态控制，支持在线、空闲、忙碌、休息并要求休息原因。
- `/support/evaluation` 已完成加载、过期、已提交、评分、标签、评价和重复提交页面状态。
- 后台评价列表已接入；邀评率、参评率、满意率分别计算。
- Token 并发提交、过期边界、跨租户、Presence 并发心跳与休息状态保持已通过 service/race/MySQL 测试；同一用户双连接、部分断开、最终断开、重复关闭、心跳超时和休息恢复均保持单一实时客服计数；公开评价真实提交、重复访问、无 Token 错误态和桌面/移动布局已验收。
- 企微评价链接发送必须先核对 `wework.apifox.cn`，未确认协议前只生成链接，不显示“已发送”。

### 批次 7：仿真、权限、性能和完整验收（已提交，待唯一 PR）

- 把所有新增权限同步到权限管理显示、默认角色、页面动作和直接请求测试；账号仍只赋角色，不直接赋隐藏权限。
- 为租户、时间、组、小组、客服、门店、企微员工号、状态和数据质量补必要复合索引。
- 丽斯未来测试租户已经生成 ServiceSession、ResponseSpan、Presence、质检、评价和派单证据；report、cleanup 和重复 Seed 测试已通过，新鲜 MySQL 租户完整性为 0 违规。
- 全仓 Go、vet、lint/typecheck、关键 race、137 个 Node 测试、47 页面静态 build、SQLite/MySQL 首次与重复 migration、隔离重复 Seed/report、MySQL 并发、桌面/移动主流程和三档角色专项均已通过；`B -> A -> C -> D -> E -> F` 已在同一分支提交，当前只需最终远端核对、push 和唯一 PR。

## 12. 验收标准

1. 同一 Conversation 的多个 sessionNo 独立统计，关闭后重开不覆盖历史。
2. AI 回复永远不关闭人工响应分段，AI-only 会话不进入人工质检分母。
3. 转派前后回复分别归属正确 Assignment 和客服。
4. 公司主管、组长、客服看到的数据范围分别正确；直接调用接口也不能越权。
5. 切换租户后无上一租户残留，导出和保存视图同样隔离。
6. 总览不再使用 LastOnlineAt 15 分钟、ServiceMode AI 率和 Conversation.created_at 排队近似。
7. 历史估算字段在页面和导出中可识别，不能伪装成精确数据。
8. 质检证据只能选择被质检 Assignment 内的人工客服消息。
9. 满意度只有真实邀请和提交后才出现，重复或过期 Token 不可再次提交。
10. 运营事实故障不破坏原回复、路由、派单、模型授权、usage 和计费链路。
11. 会话记录保留原分配、重新派单、转派、已读和关闭操作；列表、导出、保存视图和 URL 钻取在相同筛选下得到同一数据集合。
12. `go test ./... -count=1`、`go vet ./...`、前端 lint/typecheck/build、SQLite/MySQL migration、tenant-integrity-audit 和桌面/移动浏览器验收通过。

## 13. 明确不做

- 不实现客服需求文件第 7、8 部分。
- 不复制七鱼每个指标一张页面的导航结构。
- 不把 `ConversationSessionSummary` AI 压缩摘要当运营事实。
- 不用排班推算实际在线，不做无可靠来源的设备在线时长。
- 不做答问比、模糊综合客服排名或一项总分式绩效榜。
- 不让大模型自动给人工质检定分；首版质检由人工完成，系统只提供客观响应指标。
- 不修改模型供应商、AI 回复、token、价格、余额或计费公式。
- 不恢复 FAQ、七鱼、旧 hook bridge、旧独立 Agent 或旧企微字段。
- 不向 `docs/generated/` 写产品设计或验收依据。

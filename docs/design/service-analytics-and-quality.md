# 客服运营分析、会话记录与人工回复质检

> 状态：当前统一项目权威设计
>
> 更新时间：2026-07-27
>
> 唯一实施分支：`codex/tenant-ai-unified-integration`
>
> 对应 PR：`#3 codex/tenant-ai-unified-integration -> main`
>
> 范围：客服需求文件第 1-6 部分；第 7、8 部分不属于本项目

## 1. 产品结论

客服统计不复制外部产品的多套平行报表。当前信息架构是：

1. `/dashboard`：实时决策总览。
2. `/dashboard/service-analytics`：统一“运营分析”，六个分析 Tab。
3. `/dashboard/conversation-monitor`：统一“会话记录”，承担检索、钻取、服务小记、
   人工回复质检和报表反查。
4. `/dashboard/conversations`：实时回复工作台，不承载历史报表或质检配置。

质检只分析人工客服在一个 `ConversationAssignment` 分段内实际发送的回复，不给 AI
回复打人工绩效分，也不把知识候选质检混入会话质检。

## 2. 需求落点

| 客服需求 | 当前落点 | 实现原则 |
| --- | --- | --- |
| 实时数据平台 | `/dashboard` | 当前快照与今日累计分区展示 |
| 历史数据平台 | 运营分析“服务总览” | 按服务轮次统计，不按 Conversation 主表行数 |
| 响应度报表 | 运营分析“响应效率” | 排队、首响、响应分段和 SLA |
| 坐席报表 | 运营分析“客服表现” | 工作量、响应、质量、出勤四种视图 |
| 会话记录 | `/dashboard/conversation-monitor` | 列表、详情、保存视图、导出和钻取复用一套事实 |
| 在线质检 | 会话记录 + 运营分析 | 更名“会话质检”，只评人工回复分段 |

文件第 7、8 部分不实现，也不能以“未来兼容”为名建立空表、空页面或占位接口。

## 3. 当前页面

### 3.1 实时总览

总览区分：

- 当前排队、最长等待、SLA 告警。
- 人工处理中、AI 接待中。
- 实际在线/空闲/忙碌/休息客服。
- 今日进入人工池、成功分配、有效人工回复、失败接入。
- 今日平均排队和平均首响。

排班表示计划，Presence 表示实际在线状态，两者不能互相替代。

### 3.2 运营分析

六个 Tab：

- 服务总览。
- 响应效率。
- 客服表现。
- 质检与满意度。
- 派单质量。
- 来源分析。

页面支持日期、客服组、客服、Store 和企微来源等维度，图表与表格使用同一查询条件；
指标可钻取到会话记录，导出复用相同 Service 范围。

客服表现不做一张难以阅读的超宽大表，而是切换：

- 工作量。
- 响应。
- 质量。
- 出勤。

### 3.3 会话记录

每个 `conversationId + sessionNo` 一条服务轮次，支持：

- 全部、进行中、含人工回复、待人工回复、SLA 超时、待质检、已质检快捷视图。
- 客户、状态、来源、客服、消息量、响应、解决状态、数据质量和开始时间自定义列。
- 个人保存视图、默认视图、删除和恢复。
- 同条件 CSV 导出。
- 聊天原文、服务小记、咨询分类和解决状态。
- 当前人工 Assignment 分段的质检详情。

报表钻取、页面筛选和导出必须使用同一 Tenant/组织/客服范围。

## 4. 数据事实

### 4.1 服务轮次

`ConversationServiceSession` 是 `conversationId + sessionNo` 的运营读模型，记录：

- Tenant、Customer、Channel、Store、企微实例。
- 开始、进人工池、分配、首个人工回复、最后人工回复和结束时间。
- 首次/最后 Assignment，客服组、小组和客服。
- 客户、AI、人工、系统消息数量。
- 排队、首响、累计人工等待。
- 转派、解决状态、咨询分类、服务小记和数据质量。

它只服务统计和会话记录，不能反向控制回复、路由或派单状态。

### 4.2 响应分段

`ConversationResponseSpan` 从一批连续客户消息开始，到下一条人工回复结束：

- AI 回复不能关闭人工等待分段。
- 转派和会话关闭要明确结束旧等待状态。
- 分段保存当前 Assignment、客服组、小组和客服归因。
- 平均响应、P50/P90 和 SLA 分布只从这些分段计算。

### 4.3 Presence

`AgentPresenceSession` 记录 online/idle/busy/break 时段、来源、休息原因、最后心跳和时长。

- 排班只决定候选资格。
- Presence 决定当前是否可接单和实际出勤统计。
- 测试账号在线行为必须通过测试夹具或真实心跳表达，不能改变生产在线判定。

### 4.4 质检、评价与视图

- `QualityTemplate/QualityTemplateItem`：Tenant 内版本化评分模板。
- `QualityInspection/QualityInspectionItem`：一个人工 Assignment 分段的一次模板评分。
- `QualitySamplingBatch/Item`：可复现的筛选与抽样批次。
- `ConversationEvaluation`：邀评、到期、提交、评分、评价标签和内容。
- `ServiceAnalyticsPolicy`：Tenant 的 SLA、重复咨询、满意阈值和抽样默认值。
- `ReportViewPreset`：用户个人筛选、列和排序。
- `DispatchDecisionLog`：规则派单候选快照、结果、失败、降级和人工覆盖证据。

## 5. 历史数据与采集边界

当前统一项目支持从已知 Migration 基线受控升级，并完整保留 Customer、Conversation、
Message、Assignment 和 Presence 等已有业务记录；但不会根据缺失事件猜测生成运营事实：

- `FactOrigin` 只允许 `runtime` 或明确的人工 `repair`。
- 新消息、进人工池、分配、转派、人工回复、关闭和 Presence 变化在当前运行链实时采集。
- Migration 72 只负责确定性的 Store、Binding、实例、会话连续性与凭据归属，不根据姓名、
  时间接近或其他弱证据补造历史运营事件。
- `estimated/incomplete` 数据质量值保留用于明确修复或采集异常标识，不代表系统会猜测
  旧历史事实。
- fresh 数据库上线前没有历史报表；受控升级库只展示已经真实采集或明确标记为人工 repair
  的事实，缺少事实的历史区间保持 incomplete。

未知 Migration 历史的旧备份仍需配套旧源码在隔离环境只读恢复，不能直接连接当前应用后
补写事实。受支持历史库必须先完成加密备份、独立恢复和新镜像迁移演练。

## 6. 指标口径

- 会话量：服务轮次数，即 `conversationId + sessionNo`。
- 进入人工池数：`QueueEnteredAt` 非空的轮次。
- 成功分配数：进入人工池且存在 `AssignedAt`。
- 派单接入率：成功分配数 / 进入人工池数。
- 有效接入率：产生人工回复的轮次数 / 进入人工池数。
- 排队时长：进入人工池到完成分配。
- 人工首响：完成分配到该轮第一条人工消息。
- 客户总等待：进入人工池到第一条人工消息。
- 人工响应：一个客户消息批次到下一条人工回复。
- 重复咨询率：策略窗口内同客户再次开始服务轮次的数量 / 服务轮次数。
- 参评率：已提交评价数 / 已邀请评价数。
- 满意率：达到 Tenant 满意阈值的评价数 / 已提交评价数。
- 质检覆盖率：已完成质检分段数 / 可质检人工分段数。
- 质检通过率：通过分段数 / 已完成质检分段数。

重复咨询指标不直接命名 FCR；只有问题分类、解决状态和跨轮次归因足够稳定后，才能另行
定义一次解决率。

## 7. 人工回复质检

### 7.1 质检对象

质检单位是当前 Tenant 内一个已产生人工消息的 ConversationAssignment 分段。AI 消息、
系统消息、其他客服分段和其他 Tenant 消息都不进入本次评分。

### 7.2 模板

模板支持：

- 人工评分项。
- 系统指标项。
- 禁忌项/一票否决。
- 必填、最大分、通过分和版本。

评分可覆盖响应速度、针对性、简洁性、方案有效性、礼貌和禁忌项。保存时快照项目名称、
规则和证据，后续模板升级不改写历史质检。

### 7.3 工作流

```text
会话筛选
  -> 可选随机抽样
  -> 打开人工 Assignment 原文
  -> 填写逐项分数、证据和评语
  -> 完成质检
  -> 汇总到客服、团队、来源和 Tenant 分析
```

普通客服即使能看本人会话，也不能因此获得模板管理或质检执行权限。

## 8. 权限和数据范围

当前权限：

- `serviceAnalytics.view`
- `serviceAnalytics.export`
- `serviceAnalytics.managePolicy`
- `qualityInspection.view`
- `qualityInspection.manage`
- `qualitySampling.create`
- `qualityTemplate.manage`
- `conversationEvaluation.view`
- `conversationEvaluation.invite`
- `reportViewPreset.manage`
- `agentPresence.update`

范围上限：

- 平台管理员必须先选择活动 Tenant。
- 公司主管查看本 Tenant。
- 客服组长查看负责客服组。
- 客服查看本人。
- Store 员工只查看自己 Store 被授权的业务信息。

Handler 权限、Service 范围、导出查询和页面显隐必须一致。任何新增分析动作都先进入权限
管理，不允许角色名特判或隐藏权限。

## 9. API

主要显式路由：

```text
GET  /api/dashboard/service-analytics/overview
GET  /api/dashboard/service-analytics/dimensions
GET  /api/dashboard/service-analytics/export
GET  /api/dashboard/service-analytics/policy
POST /api/dashboard/service-analytics/policy/update

ANY  /api/dashboard/service-session/list
GET  /api/dashboard/service-session/dimensions
ANY  /api/dashboard/service-session/message_list
GET  /api/dashboard/service-session/export
POST /api/dashboard/service-session/annotate
GET  /api/dashboard/service-session/:id

ANY  /api/dashboard/quality-inspection/pool
GET  /api/dashboard/quality-inspection/:id
POST /api/dashboard/quality-inspection/save
ANY  /api/dashboard/quality-template/list
POST /api/dashboard/quality-template/save
ANY  /api/dashboard/quality-sampling/list
POST /api/dashboard/quality-sampling/create

ANY  /api/dashboard/report-view-preset/list
POST /api/dashboard/report-view-preset/save
POST /api/dashboard/report-view-preset/delete
GET  /api/dashboard/agent-presence/current
POST /api/dashboard/agent-presence/update
ANY  /api/dashboard/conversation-evaluation/list
POST /api/dashboard/conversation-evaluation/invite
```

公开评价 Token 只访问该 Token 对应的一次评价，不能枚举 Tenant 或会话。

## 10. 验收

- 总览当前快照与今日累计不混算。
- 服务轮次按 sessionNo 分开，重新咨询不会覆盖旧轮次。
- AI 回复不关闭人工响应等待。
- 质检只读取目标 Assignment 的人工消息。
- 筛选、图表、表格、钻取和导出范围一致。
- 保存视图只能由所有者读取和修改。
- Tenant、客服组、客服和 Store 范围在 SQLite/MySQL 等价。
- fresh 数据库不创建 backfill migration，不预置真实 Tenant/Store/会话/评价/质检数据。
- 全量测试、前端契约、typecheck 和生产构建通过。

关键验证：

```bash
go test ./internal/services -run 'ServiceAnalytics|Quality|Evaluation|Presence' -count=1
go test ./internal/bootstrap ./internal/models ./internal/migration -count=1
go test ./... -count=1
go vet ./...
cd web && node --test $(rg --files -g '*.test.mjs')
pnpm --dir web typecheck
pnpm --dir web build
```

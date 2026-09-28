# 客户目标与有界上下文实施记录

日期：2026-09-28。范围：PMS 开发工作树；未部署、未提交、未改数据库配置。

## 内部契约

`IntentTaskTraceData`、`ReplyTaskPlanTraceData`及人工恢复快照兼容新增：

- `subjectScope`：`public_policy`、`public_membership`、`personal_membership`、
  `current_order`、`historical_order`、`current_stay`、`room_candidates`。
- `requestedAspects`：沿用 broad objective，同时保留 `checkout_time`、`checkin_time`、
  `stay_dates`、`member_level_names`、`member_level`、`member_benefits`、
  `member_upgrade_conditions`、`member_retention_conditions`、`member_validity`、
  `order_amount`、`price_difference`、`room_type`、`room_number`等具体所求。
- `selectionSource`：当前由明确客户选择产生 `customer`，不能把它说成客服推荐。
- `selectionRef`：该选择所在当前客户来源引用，如 `U1`。它不是 PMS 房型 ID；
  真实房型绑定仍需以已查目录/候选匹配。

`pmsReadPlanInput`新增 `SubjectScope`、`RequestedAspects`、`MemberPhone`。
调用方需要从 ReplyTaskPlan 传入前两项，会员号从 scoped entity/session 传入；
公开会员查询不再顺带查询个人会员。当前/历史查询由明确 SubjectScope 决定。
`runtimePMSSessionLocator.MemberPhone`与预订 `Phone/OrderLocator`分离；
`member_phone`作为内部实体保留，查询定位不代表身份已验证。

## 行为变化

- JEV 基础仍为每 Task 七个 typed choice/noul 问题；当前消息出现手机号时，
  在同一分类批次按实际号码增加用途选择，区分订房、会员、两者共用和非查询号码。
  没有增加模型阶段或独立 Agent，也不使用本地会员措辞词表推断用途。
- 原有 route 增加 `order_history`选择，落地映射仍复用 `order_query`。
- 具体方面由原有 objective 问题输出，再映射为兼容 broad objective。
- 手机号补问保留原始目标，不再把“退房时间”等所求改为身份。
- 最新混合轮次最多保留八个独立业务上下文，模型按对象选择；
  不再因为混合轮次没有“唯一主题”而回退旧订单。
- 个人会员问题不继承历史订单的文字和订单 ID。
- 已发送的任务结果才作为确认事实；旧日志无发送信息时只保留问题背景。
- 客户取消停止查询；客户选择和要求推荐分别记录。
- 已有 SubjectScope 的模型任务不再经过旧本地业务词表重复改写。

## 文件与边界

主要改动：`jev_intent_detector.go`、`intent_model_detector.go`、
`intent_runlog_context.go`、`pms_read_planner.go`、`intent_goal_contract.go`。
兼容映射：`intent_pipeline.go`、`trace_callback.go`、`manual_resume_plan.go`、
`internal/pkg/replyruntime/manual_resume_context.go`。
旧词表兼容出口：`intent_customer_scenarios.go`。

没有 model、migration、HTTP DTO、路由、WebSocket、计费口径或企微协议变更。
只读 HPMS 边界和原 Commit/Outbox 保持不变。回滚仅回退代码，不恢复数据库。
共享 trace/reply plan 与 PMS facts 分支需同批合并；不得单独部署字段定义而漏接调用方。

## 验证

新增 `intent_goal_contract_test.go`覆盖每类至少两种表达的契约、上下文和调用计划。
它们使用受控 JEV typed 响应验证映射，不冒充真实模型的语义验收。

- `go build ./internal/ai/runtime/executor`通过。
- 新增目标/范围/会员手机号聚焦测试通过。
- JEV 直接在 provider 分支返回，不读取旧 `ReplyIntentProfile.IntentDetectPrompt`；
  不应把该数据库旧 JSON 提示词误认为当前 JEV 生效提示词。

真实对话与字段调用接线由主任务统一验收。当前文档不宣称实聊已通过。

## 发布前边界复核

- `CommitMessages.taskIds` 是消息归属，不代表该 Task 的全部查询事实都已告知。
  生成修复耗尽后仍会提交同 Task 的失败通知，因此上下文读取遇到
  `Validate.status=failed` 或非空 `Generate.fallbackMode` 时，保留原目标、
  具体所求和客户定位，但移除 SupportedFacts、候选展示/选择标记及预设答案。
  原审计记录不被修改，没有新增状态或数据表。
- 当前任务的手机号按 JEV 输出的 `customer_phone/member_phone` 进入补槽、
  PMS 参数构建、同轮拆题和后续 trace 恢复。新的会员号码不清除已有订房定位；
  订房号码确实更换时仍会清除旧订单，不能把另一个会员号码当作订房号码。
- 号码用途选择与实际 TaskSpan 绑定，不按整条消息的 `sourceRef` 广播；
  同一消息内不同订单或会员各自持有号码。仅由模型明确选中的前置任务继承
  已有角色实体。同一 Task、同一角色存在多个有效号码时走现有
  `ambiguous/clarify` 合同，不默认取最后一个；被明确替换的旧号码由同批
  用途选择标为 `not_for_lookup`，有效新号码正常更新。合并任务时同时检查
  订房号码、会员号码和订单定位，防止合并不同对象。
- 新增 `intent_runlog_context_test.go` 两个回归和 `intent_phone_role_test.go`，
  覆盖失败通知、旧 fallback 记录、正常成功上下文、两种换房会员另号表达、
  同轮两个号码、下一轮同订单以及仅提供会员号时不能定位未知订单。
- 以上均为内部 trace 与工具参数行为修复；无数据库、HTTP DTO、权限、
  企微协议或计费语义变化；需与当前 PMS 执行改动同批发布。

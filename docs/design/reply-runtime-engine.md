# Reply Runtime Engine

本文只描述当前生产基线 `18b19997fe1c5663e0fdecbb4b80d26775abd993`
之上的现行回复链路和本轮工作区实现。真实代码优先于历史交接材料；旧 FAQ、
旧 Hook Bridge 和旧独立 Agent 设计不属于本文架构依据。

## 1. 运行边界

正常文本回复链路保持为：

```text
120ms 消息收敛
-> 1 次 Intent
-> 原子任务并行知识检索
-> 1 次现有 Knowledge Evidence Judge
-> 1 次 Generate
-> 本地逐题校验与最多三条消息合并
-> Commit
-> Outbox
```

本轮没有增加新的模型阶段。只有 Generate 出现协议错误、429、可重试 5xx、
超时或连接中断时，才使用同一批已冻结任务和证据额外重试一次 Generate。
Intent、检索和 Judge 不会因此重跑。

Resource 与 Handoff 仍由现有 Action Ledger、结构化资源提交和接待服务执行，
不伪装成 Generate 文本。数据库 Task、稳定发送 ID、Outbox、人工状态机和房号
追问规则不在本轮变更范围内。

## 2. Active Answer Task

Intent 后生成本轮唯一的活跃任务清单。代码使用运行时
`ReplyPlan.TaskPlans` 承载这一结构，不新增持久化模型：

```text
TaskID
Intent / SubIntent
Objective / RelationToPrevious / ResolutionState
Entities
Text / OriginalText / ResolvedText
SourceRefs
OutputKind / ReplyRequired
Output / ResourceAction
SelectedLayer / SelectedCandidateIDs
SupportedFacts / MissingAspects
```

`OutputKind` 只有四类：

- `text`：必须进入 Generate，并且每题恰好产生一个回复 part。
- `resource`：由 Commit 发送真实结构化资源，不进入文本生成。
- `handoff`：由现有接待流程处理，不进入文本生成。
- `context_only`：只帮助理解相邻业务问题，不单独强制回复。

当同一轮同时包含业务问题和感谢、语气纠正等普通互动时，互动任务降为
`context_only`，其 `sourceRefs` 合并到最近的业务任务。纯互动消息仍保留一个
正常 `text` 任务。旧 Profile 或旧测试数据未提供新字段时，代码继续从既有
`Output`、`Intent` 和 `Text` 推导，支持渐进上线。

## 3. 自包含问题与来源

Intent V2 的每个任务同时保留：

- `objective`：客户当前真正要获得的信息或动作，例如
  `availability/quantity/method/action_request`。
- `relationToPrevious`：只描述与紧邻上一轮的关系，例如
  `independent/follow_up/clarification_answer/reference_previous/answer_rejected`。
- `resolutionState`：`clear/resolved_from_context/ambiguous/unresolved`，只有真正歧义
  的任务才进入澄清；同轮其他清晰任务继续执行。
- `entities`：只保存当前任务明确出现或由紧邻上下文可靠补全的房型、设施、地点等
  轻量实体，不建立第二套持久化 Task Memory。
- `text` / `OriginalText`：客户原表达，用于来源覆盖和审计。
- `resolvedText`：补全明确回指、比较或省略后的自包含问题，用于检索、Judge
  和 Generate。
- `sourceRefs`：按 `U1`、`U2` 等引用当前短消息组；首项是主要问题来源，其余
  是该任务共同消化的上下文来源。

例如“那麦田呢”可解析为“麦田房型有没有办公桌”，但只有紧邻业务对象明确时
才允许继承。新主题不得从更早历史强行继承房型、地址或媒体对象。
知识检索优先使用 `resolvedText`；原省略问法只参与来源覆盖和去重，不会再作为
第二条知识查询。`resolvedText` 缺失时回退到原 `text`。

Intent 返回后还会经过纯本地 Semantic Consistency Gate，不增加模型调用。Gate 会
阻止“有没有空调”这类信息咨询被错误执行成现实服务请求，校验
`answer_rejected` 与真实接待分类必须双向一致，并确保
`resolved_from_context` 只在确有紧邻上下文时生效。旧 Profile 未声明全部轻量语义
字段时继续走兼容模式；低置信结果会整体收敛为一个澄清任务，不能被旧业务 Task
重新恢复，也不能残留定位、小程序、工具或人工动作。

## 4. Judge V2 与知识层级

现有 Judge 使用 `knowledge_evidence_judge.v2`，对每个原子任务分别裁决
`store` 和 `general` 两层：

```text
direct_single
direct_combined
partial
insufficient
```

结果携带：

```text
selectedCandidateIds
supportedFacts[] { factId, aspect, statement, criticalValues }
missingAspects[]
```

事实维度限定为 `existence`、`quantity`、`price`、`time`、`location`、
`method`、`scope`、`condition` 和 `other`。一个维度不能推导另一个维度：
“有外卖机器人”只证明存在，不能推出能送到房门；地点名称不能推出距离、
步行时间或路线。

同一知识层内可以用 `direct_combined` 组合多条属于同一门店、同一对象和同一
适用范围的证据。不得跨门店库和通用库拼接，也不得组合不同房型、时间条件或
相互冲突的内容。FAQ 的问题与答案按一个完整语义单元理解；“问题中写两瓶、
答案肯定免费”可以形成数量和价格事实，但答案为“转接”时不能把问题文字当作
已确认事实。

代码的胜出顺序固定为：

```text
门店转接指令
-> 门店完整答案
-> 通用完整答案
-> 门店部分答案
-> 通用部分答案
-> 现有接待路由
```

`partial` 不再丢掉整题。已确认的 `supportedFacts` 继续进入 Generate，只有
`missingAspects` 交给现有 deferred handoff 逻辑；是否实际接待仍遵循既有接待
策略。Generate 只能看到胜出层的选中证据，不能看到被淘汰层或冲突候选。

Judge 不可用、超时、调用失败或协议非法时不重试，也不降级为直接使用原始召回。
相关任务按 `insufficient` 处理，未经选择的 Hits/Context 会被清空；同轮独立
Resource 仍可先真实提交，知识任务随后按现有 deferred handoff 处理。

## 5. 逐题生成与本地合并

所有 `ReplyRequired=true` 的文本任务使用统一协议：

```json
{
  "replyParts": [
    {
      "taskId": "task-1",
      "content": "房间内有两瓶矿泉水，都是免费的。",
      "coveredFactIds": ["F1", "F2"]
    }
  ]
}
```

本地校验要求：

- 每个文本 Task 恰好出现一次，顺序与活跃任务清单一致。
- 未知、重复、缺失、空 `taskId/content` 均判为协议错误。
- `coveredFactIds` 只能引用该 Task 的事实，且必须覆盖全部必答事实。
- `criticalValues` 中的数量、价格、电话、地址、日期、房型名等必须真实出现
  在对应回复文本中。
- Markdown JSON 代码块、字符串包裹 JSON 和常见外层包装会先在本地拆包；
  无法可靠解析时不得把原协议发送给客户。

全部 Task 校验通过后才在本地合并。合并只连接连续 Task，不重新总结或删除
内容，最终最多形成三条客户文本消息。Resource 和 Handoff 仍按原路径独立提交。

## 6. Generate 单阶段恢复

外层 Trigger 不再因协议失败从 Intent 开始重跑整条 Executor。恢复流程为：

1. 冻结本轮 Active Answer Task、选中证据和事实清单。
2. 第一次 Generate 正常执行。
3. 只有协议错误或可重试的模型/网络错误才额外重试一次 Generate；修复提示会
   指出缺失的 Task、Fact 或关键值，但不改变问题和证据。
4. 重试前再次检查当前来源消息是否仍允许 AI 回复；员工接管或消息失效时停止。
5. 两次均失败时，用 Judge 已确认的 `SupportedFacts.Statement` 生成确定性文本；
   纯互动只使用已有安全短答。
6. 没有可用事实的 Resource/Handoff 继续走真实动作路径；协议或模型错误原因
   绝不能显示给客户。

正常任务仍是一次 Generate，异常任务最多两次 Generate。Graph Tool 已执行时
不会盲目重试，避免重复真实动作。

## 7. Generate 上下文隔离

Intent 仍可读取带角色的必要历史来做回指识别。进入 Generate 前会移除长期
记忆和整段原始历史，并构建只包含以下内容的任务上下文：

- 当前文本 Task 的 primary/context 来源。
- `resolvedText`。
- Judge 选中的 `supportedFacts` 与必要值。
- `missingAspects` 的禁止补全说明。
- 必要的最近媒体对象。

原始知识上下文始终从 Generate 消息中移除。Generate 只读取 ReplyPlan 中 Judge
选中的结构化事实，避免从未选中候选扩写能力或重新回答旧问题。

语音、图片和普通文字进入同一 Active Answer Task 链路。连续客户消息使用共享
机器标记保存真实物理消息边界，一条多行文字或多行语音仍只对应一个 `URef`。
只有状态为 `understood` 的语音可以进入 Intent；优先使用完整 `mediaText`，仅在
为空时回退 `mediaSummary`。已经包含在当前 Burst 的语音不会再从当前 payload、
媒体上下文或原始历史重复加入 Prompt。本轮不修改 ASR、OCR 或媒体回调。

本地 `POSSIBLE_ATOMIC_TASKS` 只用于单条长文字/长语音内部的逐题检查，或候选数
确实多于物理消息数的情况；多个已经独立编号的短消息不重复抄写候选。顿号只有在
两侧都像完整问题时才作为拆分边界，因此“早餐几点、停车免费吗、发票怎么开”可
拆成三题，“办公桌、沙发都有吗”仍保持一个组合设施问题。

## 8. 输出与 Commit 防线

Generate 事件消费阶段先解析 `replyParts`，再执行客户可见文本清理。以下精确
内部头部只允许出现在消息开头，并会安全移除：

```text
[历史消息]
[AI客服]
[人工客服]
[人工作答]
```

标记出现在正文中、清理后为空或结构不明确时，输出判为协议错误并进入 Generate
恢复或事实兜底。普通客户可见的“人工、同事、转接”等词不会因此被拦截。

Commit 会再次调用相同清理函数，并拒绝仍含 `replyParts`、`taskId` 或
`coveredFactIds` 协议外形的文本。只有完成逐题校验的客户文本和已登记的真实
结构化资源才允许提交。

## 9. Trace

运行 Trace 是内部排障数据，不进入客户消息。本轮重点字段包括：

- `pipeline.replyPlan.activeTaskCount`
- `pipeline.replyPlan.replyRequiredTaskCount`
- `pipeline.replyPlan.taskPlans[].resolvedText/sourceRefs/outputKind`
- `pipeline.replyPlan.taskPlans[].selectedLayer/selectedCandidateIds`
- `pipeline.replyPlan.taskPlans[].supportedFacts/missingAspects`
- `pipeline.evidenceJudge.tasks[].layers[]`
- `pipeline.generate.attemptCount`
- `pipeline.generate.fallbackMode`
- `pipeline.generate.composedMessageCount`
- `pipeline.generate.blockedInternalMarker`

这些字段用于定位漏题、事实缺失、错误外推、协议恢复和内部标记拦截；应用日志
仍应只打印必要预览和 ID，不另行输出客户敏感全文。Trace 结构不改变外部 API
或客户消息结构。

## 10. 验证、发布与回滚

聚焦自动测试范围：

```bash
go test -p=1 \
  ./internal/ai/application/runtime \
  ./internal/ai/runtime/executor \
  ./internal/ai/runtime \
  ./internal/services \
  ./cmd/reply-runtime-eval \
  -count=1
```

自动测试必须覆盖多题逐题输出、事实与关键值完整性、回指补全、知识层级、
`partial`、Generate 单阶段恢复、事实兜底、内部标记拦截、媒体文本优先、失败
语音门禁、真实 Burst 边界、无效 `sourceRefs` 修复，以及
Resource/Handoff/Outbox 既有行为不变。

代码测试不能替代真实模型和企微出站验收。固定场景重复运行、150 轮评测、
隔离企微客户最终投递和生产观察必须单独记录真实结果；未执行时不得写成已通过。

本轮没有数据库表、Migration、外部 API、DTO、枚举、WebSocket、前端、权限、
模型供应商、计费或 Token 统计口径变更。代码可直接回滚到基线 `18b1999`；
无数据结构需要反向迁移。生产 Intent Profile 或运行配置若在部署阶段另行更新，
必须独立备份和恢复。

## 11. 并行分支

本轮工作区与 `codex/customer-audit` 的代码文件交集目前只有
`internal/ai/runtime/reply_trigger_service.go`：本分支删除外层整链路协议重跑并
增加真实 Burst 边界，审计分支在同文件加强租户范围内的 Agent 读取。另有共同
修改的交接文档 `docs/development-handoff.md`。合并时必须同时保留两边代码语义并
合并文档记录，不能用整文件覆盖解决冲突。

当前工作区与 `codex/ai-billing` 没有同文件交集，也没有修改计费语义。每次提交
和 push 前仍需重新 `git fetch origin` 并核对交集，因为并行分支会继续变化。

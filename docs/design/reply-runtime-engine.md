# Reply Runtime Engine

本文只描述当前生产基线 `40cc24be3972ab341af7f0ef83a4732e9630ad87`
之上的现行回复链路和本轮工作区实现。真实代码优先于历史交接材料；旧 FAQ、
旧 Hook Bridge 和旧独立 Agent 设计不属于本文架构依据。

截至 2026-08-31，当前工作区已通过定向测试、完整普通测试、Race、Vet 与 Linux amd64
构建；最终复审、提交、推送、分阶段部署、真实模型与企微出站验收仍待本轮后续步骤，
本文不把尚未执行的步骤写成已完成。

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
不伪装成 Generate 文本。数据库 Task、人工状态机和房号追问规则不在本轮变更范围内。
普通 `ai_reply` 与 AI 服务通知继续沿用既有 ClientMsgID、Commit、Outbox、发送重试和
幂等行为；本轮只为 `manual_resume` 增加第 8 节所述的请求绑定投递修复，不扩展到
普通回复或服务通知。

“客户已经看到业务答案”不以 RunLog 或 Commit Trace 的 `sent` 字样单独判断。站内
消息要求真实 Message 存在且未撤回；企微客服、企微 CLI 和企微员工号等外部渠道还
要求对应 Outbox 已经是 `sent`。转接成功、人工恢复提示等 AI 服务通知不绑定业务
`taskIds[]`，也不能结算文本或资源 Task。

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
- `text` / `OriginalText`：Intent 模型给出的当前 Task 表达；客户原始物理文本以
  `input.currentTurnSources` 为准，V2 不做字面 span 校验。
- `resolvedText`：补全明确回指、比较或省略后的自包含问题，用于检索、Judge
  和 Generate。
- `sourceRefs`：按 `U1`、`U2` 等引用当前短消息组；首项是主要问题来源，其余
  是该任务共同消化的上下文来源。

同一轮包含多少原子问题、每个问题的语义边界在哪里，只由一次 Intent 模型判断。
Intent Prompt 每轮都要求从 `U1` 到 `Un` 逐条扫描，且不能依赖标点、换行、空格
或固定连接词；代码不再向模型披露本地猜测的候选，也不会在模型返回后重新拆分、
合并或补造 Task。检索层严格使用模型给出的知识 Task，不再从客户原文二次拆题。

V2 本地协议只验证来源真实性与顺序：`sourceRefs` 必须指向本轮真实 URef，主要来源
按 `U1 -> Un` 单调排列，每个当前来源至少被一个 Task 作为 primary 或 context 引用。
代码不要求 `text` 是来源中的字面片段，也不根据标点重新计算问题数量、检查本地
原子题覆盖或重写模型 Task；只折叠字段和动作完全相同的重复 Task。Intent 返回被
完整单层 Markdown JSON fence 包裹时可以安全拆包，JSON 之外仍禁止带解释文字。

同一收敛窗口内的连续短句按各 Task 的 primary `sourceRefs` 独立恢复和校验，例如
`U1=有早餐吗 / U2=几点 / U3=在哪吃` 中，后两题的 `text` 仍分别保留 U2、U3 原话，
`resolvedText` 必须能由其声明的更早 URef 证明。跨轮回指还必须同时存在紧邻客户问题
和紧邻 AI 答复；显式替换对象必须声明当前实体，不能保留旧对象或增加上下文没有的实体。

例如“那麦田呢”可解析为“麦田房型有没有办公桌”，但只有紧邻业务对象明确时
才允许继承。新主题不得从更早历史强行继承房型、地址或媒体对象。
知识检索优先使用 `resolvedText`；原省略问法只参与来源覆盖和去重，不会再作为
第二条知识查询。`resolvedText` 缺失时回退到原 `text`。

进入 Generate 前，知识文本 Task 必须已经有明确胜出知识层和至少一条 Judge
确认的 `supportedFacts`。只有一个无证据知识 Task 时直接使用本地安全兜底；与天气、
资源或其他可执行 Task 同轮出现时，只把该知识 Task 约束为固定安全短答，其他 Task
继续执行，禁止一题失败吞掉整轮。客户可见文本中的存在性、能力、政策、距离、方法、
位置、时间、数量和价格声明即使与已有事实属于同一维度，也必须能回到已选事实，
不能在正确答案旁追加新的对象、能力、规则或数值。

V2 Intent 返回后只执行动作权限收窄：歧义或未解析 Task 进入澄清，其他 Task 只保留
其 intent 类别允许的知识、资源、工具和接待动作；`answer_rejected` 仍要求紧邻真实
AI 答复。V2 不再让完整 Semantic Consistency Gate 重判模型的业务语义或 Task
边界。旧 Profile 未声明轻量语义契约时继续走 legacy Gate 与低置信澄清兼容路径。

## 4. Judge V2 与知识层级

现有 Judge 使用 `knowledge_evidence_judge.v2`，对每个原子任务分别裁决
`store` 和 `general` 两层。模型协议只允许四种业务裁决：

```text
direct_single
direct_combined
partial
insufficient
```

以下三种是运行时记录的调用或协议失败状态，不是模型对“资料是否足够”的业务判断：

```text
protocol_invalid
timeout
malformed
```

结果携带：

```text
decisionSource
candidateCount
selectedCandidateIds
supportedFacts[] { factId, aspect, statement, criticalValues }
missingAspects[]
```

`decisionSource` 记录每个 Task、每个知识层的真实裁决来源：正常 Judge 输出为
`model`；模型已经选定 Candidate、但事实结构需要从该 FAQ 原文机械重建时为
`model_selected_repair`；FAQ 问法或显式 alias 与当前 Task 机械相等时的严格恢复为
`exact_faq_fallback`；同样满足严格相等且答案仅为“转接/转人工”时为
`deterministic_handoff`。同一批次中的不同 Task 可以有不同来源，因此该字段不能
只记录在 Judge 批次顶层。`candidateCount` 同时记录批次、Task 和知识层候选数量；
`supportedFacts/missingAspects` 也按 Task 和知识层分别保存，禁止把门店层已覆盖事实
与通用层缺失方面混成一份结果。

事实维度限定为 `existence`、`quantity`、`price`、`time`、`location`、
`method`、`scope`、`condition` 和 `other`。一个维度不能推导另一个维度：
“有外卖机器人”只证明存在，不能推出能送到房门；地点名称不能推出距离、
步行时间或路线。

Judge 只输出完整回答当前 Task 所需的最小事实集合。同一完整事实句已经包含短句时，
可以让多个 FactID 共同引用该完整句，不能再把摘要短句作为第二段客户内容。
`criticalValues` 只保存不能自然改写的精确值，例如数量、金额、时间、电话、地址、
房型、账号密码、收费状态或固定选项；“建议、选择、联系、回复、比较”等普通措辞
保留在 `statement` 的语义中，但不要求 Generate 逐字复述。

同一知识层内可以用 `direct_combined` 组合多条属于同一门店、同一对象和同一
适用范围的证据。不得跨门店库和通用库拼接，也不得组合不同房型、时间条件或
相互冲突的内容。用品自取 FAQ 的地点冲突按答案中的明确地点短语判断，不依赖
有限地点名称清单；完整地点与其简称可以兼容，两个不同地点则禁止本地救援。
FAQ 的问题与答案按一个完整语义单元理解；“问题中写两瓶、
答案肯定免费”可以形成数量和价格事实，但答案为“转接”时不能把问题文字当作
已确认事实。对“有没有”这类存在性问句，只有答案明确肯定且问题、答案包含同一
结构化实体时，才把问句规范为肯定事实；问句里的“没有”不能被误当成答案的否定。

严格 FAQ 恢复按知识层独立执行，不读取向量分数，也不使用字符相似度或语义近似
改判。只有 FAQ 问法或显式 alias 与当前 Task 在去除标点、礼貌前后缀后机械相等，
同层所有相同问法答案不冲突，并且单条 FAQ 能机械重建完整事实时才允许恢复。
答案仅为“转接/转人工”时只形成该层的确定性知识转接；不得把 FAQ 问题文字当作
事实，也不得跨 FAQ、跨知识层拼接对象、范围或条件。同层 `RawCandidates` 中只要
还存在一条可信、值得交给 Judge 复核的竞争正文 FAQ，精确转接就不能在
Judge 异常时自动恢复为转接；正文即使只存在于 `RawCandidates`、未进入本轮 Judge
预算，也必须参与该否决。紧预算只有一个槽位时优先保留可信待复核正文；至少两个槽位
时优先把同层正文和精确转接一起交给 Judge，再使用剩余槽位放通用兜底。Judge 已经
真实看见两项并明确选择精确转接时，可以执行该模型裁决；本地只阻止模型未见正文时
由异常 fallback 抢先转接。
模型若在非精确问法下选择
转接候选并把它包装成普通事实，或者让转接候选参与 `partial/direct_combined`，该
知识层直接记为 `protocol_invalid`，候选正文不得进入 Generate。

源码中仍保留一组只供历史隔离测试使用的 `highConfidence*` / score-rescue helper，
当前 `JudgeBatch -> applyKnowledgeEvidenceJudgeOutcome` 生产路径没有调用它们。后续
清理应单独进行；禁止为解决线上个案把这些按分数或相似度改判的旧入口重新接回。

房型成员问题会把实体中的“房型/客房”后缀规范化后再与肯定枚举成员对齐。例如
“部分房型配备办公桌，如合柴、麦田和艺林”可以确认“合柴房型有办公桌”，但仍不能
把该部分枚举冒充完整房型名单。

代码的胜出顺序固定为：

```text
无竞争正文的门店精确转接指令
-> 门店完整答案
-> 通用完整答案
-> 门店部分答案
-> 通用部分答案
-> 现有接待路由
```

`partial` 不再丢掉整题。已确认的 `supportedFacts` 继续进入 Generate，只有
`missingAspects` 交给现有 deferred handoff 逻辑；是否实际接待仍遵循既有接待
策略。Generate 只能看到胜出层的选中证据，不能看到被淘汰层或冲突候选。

模型返回的 Candidate ID 和事实 JSON 采用非破坏式校验：本地代码不会改选另一个
Candidate，也不会再次按分数或字符相似度做语义裁决。Fact JSON/aspect/
criticalValues 无法使用时，只允许从模型已经选中的 FAQ 问题与答案机械重建事实；
只有所选 FAQ 与当前 Task 存在可机械证明的明确房型、区域或配置范围冲突时，该知识层
才记为 `protocol_invalid`。普通实体同义关系由 Judge 负责，本地不维护有限同义词表
二次否决。`partial` 只需覆盖同一主体下已经确认的部分事实，不要求把仍然
缺失的实体伪装成已覆盖；`direct_combined` 则不能跨房型、对象或范围拼接。
完整性同时按客户问题中的主体与事实维度配对检查。例如同时询问矿泉水和枕头的
数量、费用时，不能用“矿泉水免费 + 枕头两个”冒充四个槽位都完整；多房型、多设施
也只接受明确分句中的配对，不做笛卡尔积。“不确定、待确认、资料未写明”等边界
可以作为条件说明，但不能充当存在性、数量、费用、位置或方法的确定证据。

Judge 批次候选总预算固定为 28。配额只有一条时优先保留门店中可信、值得 Judge
复核的正文；配额至少
两条且门店、通用均有候选时，通常各保留一条最佳候选，不能让本地“完整性”启发式
在 Judge 前删除整个通用层。唯一例外是：门店没有单条完整答案，但两条同层候选能
机械覆盖当前 Task 明确要求的多个事实维度、主体或配置字段，此时先保留这两条必要
证据；配额仍有剩余时再补通用兜底。泛泛的“同时询问”或两条近重复 FAQ 不构成该
例外。普通问题与 `compound_information` 共用这条规则，语义多样性只填充剩余槽位。

房型等枚举证据区分 `complete/partial/invalid`。“等、部分、例如、比如、诸如、但不限于”
必须在清洗成员前保留并判为 `partial`；只有完整枚举才能生成排他式交集结论。
不同事实 aspect 不通过文本相似度改写为同一句；数量、价格、位置、存在性、时间、
方法、范围和条件分别保留，肯定与否定事实永远不能合并。

每轮只调用一次 Judge。Judge 不可用、超时、调用失败或出现会影响最终选择的
`protocol_invalid/malformed` 时，不重跑 Judge、Intent 或 Retriever，也不降级为使用原始召回；
失败 Task 本身不新增人工接待，并在 Generate 前进入确定性安全短答。未经选择的
`Hits/ContextResults/ContextText` 会被清空，`RawHits` 只保留在内部 Trace 中用于
排障。协议失败只隔离对应 Task/Layer；同轮已经合法的选择和事实会被冻结保留，
不能被失败题清空。某层协议失败时，另一层已经合法的完整或部分答案仍按固定优先级
生效；只有所有候选层均无合法结果且至少一层协议失败时，Task 才进入协议恢复。
同轮已有合法门店完整答案时，不会因为无关通用层协议问题增加模型调用。若同轮另一个 Task 已经得到部分事实或严格知识转接，
其已确认事实、缺失方面接待动作和转接顺序继续保留；协议失败 Task 不能吞掉这些
兄弟任务，也不能把局部异常描述成“整个知识库不可用”。

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
- `criticalValues` 中的数量、金额、电话、地址、日期、时间、房型名等必须真实出现
  在对应回复文本中。
- Markdown JSON 代码块、字符串包裹 JSON 和常见外层包装会先在本地拆包；
  无法可靠解析时不得把原协议发送给客户。

全部 Task 校验通过后才在本地合并。合并只连接连续 Task，不重新总结或删除
内容，最终最多形成三条客户文本消息。同一个 Task 内会删除完全重复句，或删除已经
被另一完整句明确包含的短句；不同 Task、不同对象、不同条件、不同肯否定或互补事实
绝不跨题删除。Resource 和 Handoff 仍按原路径独立提交。

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

正常 Generate 的事实提示与确定性兜底共用同一套客户展示事实收敛：完整句可以承载
其已明确包含的短句 FactID，但数量与费用、位置与方法等互补事实仍分别保留。这样模型
恢复与本地兜底不会产生两套不同的重复或遗漏行为。

## 7. Generate 上下文隔离

Intent 仍可读取带角色的必要历史来做回指识别。进入 Generate 前默认移除长期
记忆和整段原始历史，并构建只包含以下内容的任务上下文：

- 当前文本 Task 的 primary/context 来源。
- `resolvedText`。
- Judge 选中的 `supportedFacts` 与必要值。
- `missingAspects` 的禁止补全说明。
- 必要的最近媒体对象。

明确依赖上一轮的任务使用单独的有界会话上下文，不恢复无约束整段历史：

- `follow_up`、`clarification_answer`、`reference_previous`、纠正或
  `resolved_from_context` 任务读取紧邻的一条客户问题，以及其后最多三条连续、同一
  发送方类型的 AI 或人工客服答复。
- `interaction/conversation_recap` 最多读取最近八条当前会话消息。
- 有界历史只用于解释当前 Task，不会重新创建、激活或补答旧 Task。
- 普通 `independent + clear` 任务仍完全看不到旧业务问题。

相邻上下文必须是紧邻的真实“客户 -> 同类型客服答复组”：AI 与人工不能混成一组，
空消息和已注册的 AI 服务通知不切断正文组；历史末尾不是客服答复时不建立相邻组。
四条以上连续答复只保留最新三条，并分别截断，不能让较早长文本挤掉最后一条纠正。
Intent、Judge 和 Generate 使用同一组、同一顺序。两条连续客户消息不能被拼成历史
问答；当前 burst 内的省略关系由 `sourceRefs + resolvedText` 表达，不会错误挂接更早历史。

原始知识上下文始终从 Generate 消息中移除。Generate 只读取 ReplyPlan 中 Judge
选中的结构化事实，避免从未选中候选扩写能力或重新回答旧问题。

语音、图片和普通文字进入同一 Active Answer Task 链路。连续客户消息使用共享
机器标记保存真实物理消息边界，一条多行文字或多行语音仍只对应一个 `URef`。
只有状态为 `understood` 的语音可以进入 Intent；优先使用完整 `mediaText`，仅在
为空时回退 `mediaSummary`。已经包含在当前 Burst 的语音不会再从当前 payload、
媒体上下文或原始历史重复加入 Prompt。本轮不修改 ASR、OCR 或媒体回调。

本地不生成 `POSSIBLE_ATOMIC_TASKS`，也不按问号、顿号、连接词或物理消息数量猜测
题目数。单条长文字、完整语音转写和连续短消息都由同一次 Intent 模型确定 Task 数量
与边界；V2 代码只校验真实 URef、来源覆盖、来源顺序和完全重复 Task，然后按模型
给出的 Task 检索。

## 8. 人工恢复边界

人工超时恢复复用现有 RunLog，不新增跨运行“已答题目”状态。恢复器只在 Trace
结构完整且来源可验证时读取 `ReplyPlan.TaskPlans` 与
`EvidenceJudge.DeferredTaskIDs`，冻结真正延后的 Task；同轮已经正常回答的兄弟 Task
不会重新进入 Intent、Retriever 或 Generate。人工期间新增的客户消息形成新的 URef，
整次恢复最多为这些新来源调用一次 Intent，再与冻结 Task 按来源顺序合并。

正常轮不会为了隐藏待转接问题而删除 Task。完整无答案或明确转接的 Task 以
`output=deferred_knowledge_handoff`、`outputKind=handoff`、`replyRequired=false`
保留在 ReplyPlan，因此 Generate 只看到可回答的文本 Task，人工恢复仍能通过稳定
TaskID 找回真实未完成项。恢复执行时该临时 handoff 输出标记不会被带回新 ReplyPlan；
Task 会重新成为 `knowledge_text_reply`，按原 `text/resolvedText/sourceRefs` 再走知识
检索和裁决。这个标记不是新的持久状态机，只是现有 RunLog 内的执行边界。

知识库未配置、Retriever 不可用等 Judge 前来源不可用路径同样使用显式逐题契约：
`disposition=no_evidence_handoff`、`decision=insufficient`、
`decisionSource=source_unavailable`。新版 V2 Trace 不允许依靠空 disposition 被恢复；
空值只保留给有界 legacy 兼容。

`no_evidence_handoff` 表示仍未完成、可以在原人工超时点恢复的知识 Task。
`knowledge_direct_handoff`、明确转人工，以及答案已真实到达客户的
`answer_then_handoff` 表示转接已完成，原超时点只静默恢复 AI，不重新回答原题。
系统沿用现有超时矩阵：总部网页待接入 3 分钟、门店待跟进 5 分钟、员工真实接管后
空闲 10 分钟；不会在任一超时之后再启动第二段十分钟等待。

V2 Trace 与 legacy Trace 使用不同契约。V2 要求每个来源都能回到真实客户消息 ID；
legacy 只做受限兼容，不能被误标为 V2。Trace 缺失、来源不可信或冻结 Task 无法验证
时，恢复器回到现有重新识别路径，不伪造 Deferred Task，也不建立新数据库表、Task
状态或恢复账本。恢复执行前后仍使用现有请求有效性和人工路由检查。

`manual_resume` 使用请求绑定的投递复核：恢复请求 ID 绑定来源消息 ID，ClientMsgID 使用
`ai_manual_resume_` 加请求 ID 的 SHA-256 前 24 字节十六进制。稳定的 Task/资源归属
ClientMsgID 同时编码请求、来源消息和 Task 集；命中该稳定 ID 时，已落库 Message 是客户
可见内容的权威版本，恢复器只校验 request ID、消息类型和稳定归属，不再拿重跑后的正文或
资源 payload 覆盖它。旧格式或非稳定 ID 仍要求正文或资源身份匹配。通过上述校验后才允许
补建缺失的 Outbox；符合恢复条件且尚未开始外部发送的 `cancelled` Outbox 可以原子恢复为
`pending`。`pending`、仍在
重试期的 `failed` 以及五分钟内的 `sending` 只标记为 `delivery_pending`，不重跑模型，
也不增加人工恢复 Task 的 `RetryCount`；Outbox 变为 `sent` 后，下一次复核完成恢复 Task。

`failed` 且 `next_retry_at=nil` 是终态 `delivery_failed`，与陈旧 `sending` 的
`delivery_uncertain` 分开处理：两者都不重放、不重跑模型，但终态失败使用明确失败事件，
恢复 Task 直接失败且不再排期，会话保持或恢复门店人工、清空自动过期并要求人工跟进。
即使恢复 Message 和终态 Outbox 已落库、对应 RunLog 尚未来得及写入，请求绑定 Message
也会作为崩溃恢复屏障；已经提交但缺少可验证 Trace 的其他投递只进入人工复核，不能再次
运行模型。

任何 `sending` 都不会由 `ListPending` 自动重放，因为企微协议、客服和 CLI 发送接口没有
可供本服务复用的外部幂等键。`sending` 超过五分钟，或员工接管取消了已经 claim 的普通
AI Outbox 时，投递结果记为 `delivery_uncertain`：不重放消息、不重跑模型，人工恢复 Task
终止为失败，会话保持需要人工复核且不设置自动过期时间。员工接管会取消 `pending`、
`failed` 和已 claim 的 `sending` 普通 AI Outbox；协议与客服发送器在外部调用前重新校验
路由，CLI 在把 claimed 项返回给桥接端前重新校验，迟到回执只能更新仍为 `sending` 的行。
AI 服务通知继续按原旁路规则发送。

该校验不能把外部网络调用变成数据库原子操作。特别是 CLI Poll 已把消息返回给外部桥接
端后、桥接端真正发送前的窗口，服务器无法撤回已返回的数据；要关闭这一窗口，需要在
桥接 API 增加 attempt token 与发送前 CAS 契约，本轮不修改外部接口。

## 9. 输出与 Commit 防线

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
结构化资源才允许提交。内部 Commit Trace 会为每条真实消息记录 `taskIds[]`，并校验
持久化 Message 的 request ID、消息类型和 Task/资源归属；非稳定 ID 继续校验正文或
结构化资源身份。稳定 `manual_resume` ID 命中时保留第一次已落库内容，只修复它的外部
投递，供人工恢复判断哪些 Task 已经真实提交。普通 `ai_reply` 与 AI 服务通知的发送和
幂等行为保持不变；`manual_resume` 的请求绑定 ClientMsgID 与 Outbox 修复只按第 8 节的
严格条件执行。

## 10. Trace

运行 Trace 是内部排障数据，不进入客户消息。本轮重点字段包括：

- `pipeline.replyPlan.activeTaskCount`
- `pipeline.replyPlan.replyRequiredTaskCount`
- `pipeline.replyPlan.taskPlans[].resolvedText/sourceRefs/outputKind`
- `pipeline.replyPlan.taskPlans[].selectedLayer/selectedCandidateIds`
- `pipeline.replyPlan.taskPlans[].supportedFacts/missingAspects`
- `pipeline.evidenceJudge.tasks[].layers[]`
- `pipeline.evidenceJudge.candidateCount`
- `pipeline.evidenceJudge.attemptCount`
- `pipeline.evidenceJudge.tasks[].candidateCount/decisionSource`
- `pipeline.evidenceJudge.tasks[].layers[].candidateCount/decisionSource`
- `pipeline.generate.attemptCount`
- `pipeline.generate.fallbackMode`
- `pipeline.generate.composedMessageCount`
- `pipeline.generate.blockedInternalMarker`
- `input.currentTurnSources[] { ref, messageId, messageType, text }`

这些字段用于定位漏题、事实缺失、错误外推、协议恢复和内部标记拦截；应用日志
仍应只打印必要预览和 ID，不另行输出客户敏感全文。Trace 结构不改变外部 API
或客户消息结构。

Retriever 的原始候选摘要在 Judge 前写入 Trace；Judge 裁决以及 deferred、retry、
handoff 等最终 Task 清理完成后，条目列表会用最终 `EffectiveHits/ContextResults`
覆盖其 `UsedInContext`、上下文排名和淘汰原因。即使
Judge 将所有 Task 判为 `insufficient`、清空 Generate 可见上下文并提前进入接待，
Trace 仍保留真实召回数量、原始排名、知识库和全部候选，同时明确没有候选被最终
授权使用。`pipeline.retriever.count` 表示合并去重后的 `RawHits` 数，不表示进入
上下文的条数。持久化 Retriever 日志只记录原始候选，不再把 Judge 前的预选上下文
标记成最终 UsedHits；最终授权证据以 Runtime Trace 和 EvidenceJudge Trace 为准。

## 11. 验证、发布与回滚

聚焦自动测试范围：

```bash
go test -p=1 \
  ./internal/ai/application/runtime \
  ./internal/ai/runtime/executor \
  ./internal/ai/runtime \
  ./internal/services \
  ./cmd/reply-runtime-eval \
  -count=1

go test -race -p=1 \
  ./internal/ai/application/runtime \
  ./internal/ai/runtime/executor \
  ./internal/ai/runtime \
  ./internal/services \
  ./cmd/reply-runtime-eval \
  -count=1
```

截至 2026-08-31，最新定向回归、上述普通测试、完整 Race、Vet 与 Linux amd64 构建
已在当前差异上通过；这些结果不能替代后续最终复审、提交、部署、真实模型和企微
最终投递验收。

自动测试必须覆盖多题逐题输出、事实与关键值完整性、回指补全、知识层级、
`partial`、Generate 单阶段恢复、事实兜底、内部标记拦截、媒体文本优先、失败
语音门禁、真实 Burst 边界、无效 `sourceRefs` 拒绝，以及普通 Resource/Handoff/
Outbox 稳定发送 ID。人工恢复还必须覆盖请求绑定 ClientMsgID、缺失 Outbox 补建、合法
`cancelled -> pending`、`pending/可重试 failed/新鲜 sending -> delivery_pending` 不重跑
模型且不增加 `RetryCount`、`sent` 后复核完成，终态 `failed + next_retry_at=nil` 进入
`delivery_failed`，以及陈旧 `sending` 或 claim 后取消进入 `delivery_uncertain` 并保留人工
复核。测试还覆盖 request-bound Message 已提交但 RunLog 缺失的崩溃窗口，保证不再次运行
模型；任何 `sending` 不会被扫描重放，员工
接管能取消普通 AI 的已 claim Outbox，迟到 CLI 成功/失败回执不能覆盖终态。

代码测试不能替代真实模型和企微出站验收。本轮只执行计划内 10 至 15 个代表场景，
再观察首批自然生产回复；隔离企微客户最终投递和生产观察必须单独记录真实结果，
未执行时不得写成已通过。未经用户明确同意不运行 50 轮或更大批量主动评测。

本轮没有数据库表、Migration、外部 API、DTO、枚举、WebSocket、前端、权限、
模型供应商、计价公式或 Token 统计字段变更。每轮只有一条 Judge usage/费用事件，
沿用现有稳定事件键；协议异常不会额外产生第二次 Judge 用量。Release B 异常时
优先回滚到已验证的 Release A，Release A 异常再回滚到基线 `40cc24b`；无数据结构
需要反向迁移。生产 Intent Profile 或运行配置若在部署阶段另行更新，必须独立备份
和恢复。

当前普通异步回复没有独立持久化的全链路 Job 重试器；因此 Judge
`protocol_invalid/timeout/malformed` 在严格 exact FAQ 无法恢复时，最终采用不含酒店
事实的安全短答，并在 Trace 中保留协议失败状态，不伪装成 `insufficient`、不转人工，
也不重复调用 Judge。这里是对原计划中 `judge_protocol_retry` 名称的实施收口：在没有
安全重试基础设施和独立 usage 事件键之前，不以整链路重跑换取表面上的自动重试。

## 12. 并行分支

2026-08-31 的统计口径为：先执行 `git fetch origin`；本工作区取
`git diff --name-only 40cc24b --` 的已跟踪路径，并行分支分别取从其与 `40cc24b`
的 merge-base 到远端 tip 的路径；两组路径排序、去重后求交集。

按上述口径，本轮工作区与 `codex/customer-audit` 的完整同路径交集共 12 个：

- `docs/development-handoff.md`
- `internal/ai/runtime/reply_trigger_service.go`
- `internal/services/channel_message_outbox_service.go`
- `internal/services/conversation_human_dispatch_service_test.go`
- `internal/services/conversation_route_service.go`
- `internal/services/message_service.go`
- `internal/services/message_service_test.go`
- `internal/services/wxwork_cli_bridge_service.go`
- `internal/services/wxwork_kf_inbound_service.go`
- `internal/services/wxwork_kf_outbound_service.go`
- `internal/services/wxwork_protocol_service.go`
- `internal/services/wxwork_protocol_service_test.go`

审计分支在这些路径增加租户隔离与发送约束，本分支增加真实 Burst/URef、统一
`ClaimForDispatch` 和人工恢复投递闭环。合并时必须同时保留两边语义，再合并测试和
文档记录，不能用整文件覆盖解决冲突。

按同一口径，当前工作区与 `codex/ai-billing` 的同路径交集为 0，也没有修改计价公式
或 usage 字段语义；Judge 仍维持每轮一次真实调用和一条稳定 usage 记录。每次提交和
push 前仍需重新 `git fetch origin` 并核对交集，因为并行分支会继续变化。

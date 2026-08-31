# 回复引擎上下文与知识裁决最小根治计划

## 文档信息

- 文档状态：Release A/B 工作区实现及自动验证已收口；定向测试、完整普通测试、Race、Vet 与 Linux amd64 构建已通过，待最终复审、提交、推送、分阶段生产部署与真实验收
- 修订日期：2026-08-31
- 当前生产基线：`40cc24be3972ab341af7f0ef83a4732e9630ad87`
- 集成分支：`codex/reply-runtime-context-judge-closure`
- Release A 基础提交：`2af6d3c`、`59942ff`
- Release B 基础提交：`9e6af05`、`8b1d76a`
- 主要行为参考：`4db799363040a4478a5585e101d119de11a26f8e`
  - 提交时间：2026-07-22 16:23:43 +0800
  - 所属历史：`origin/codex/ai-billing`
- 次要行为参考：`003dfb771579bd289f4ea9ab3139a069925009b6`（“原神”）
- 本轮性质：局部修复现有回复链路，不整版回滚，不新建回复状态机

本文以真实生产代码、生产 Trace 和真实会话为依据。目标是删除已经证明会误伤模型判断的约束，保留后来确实需要的事实边界、资源提交和人工接管能力，让系统尽快恢复自然、完整、可控的回复表现。

## 1. 最终结论

### 1.1 不新增“已答题目状态系统”

上一版方案中的以下设计全部移出本轮：

- 跨运行 `TaskClosure`。
- 持久化 `TaskState`。
- `TextActionState / ResourceActionState / HandoffActionState`。
- 新的 TaskKey、SourceSpan 哈希和 ReplyBatchKey。
- `ConversationReplyCoordinator`。
- RunLog `Begin / Checkpoint / Finish` 状态机。
- stale-run 自动恢复器。
- 将 ActionLedger 从审计 Trace 升级为业务状态。

当前系统已经存在真实状态：

```text
Message
ClientMsgID
Commit
Outbox
ConversationRouteState
AIManualResumeTask
```

再增加一套“这道题是否已经答过”的语义账本会形成双写：

- AI 已生成但没有 Commit，却被错误标记为已答。
- 客户继续追问旧答案，却被已关闭 Task 错误跳过。
- 人工接管、Outbox 重试或进程重启后，Task 状态与客户真实可见消息不一致。
- 为防重复而删除历史，最终失去指代、否定和连续对话上下文。

本轮只保留**单次运行内部的临时 Task 和已有 DeferredTaskID**。是否已经真实回复，
以真实 Message 与投递状态共同决定，不建立第二套持久状态：站内消息以已落库且未撤回
为准；企微客服、企微 CLI 和企微员工号等外部渠道必须同时存在匹配 Message，且对应
Outbox 已经是 `sent`。RunLog、Commit Trace 或 Message 自身写成 `sent` 都不能替代真实
外部投递；转接成功、人工恢复提示等 AI 服务通知也不能结算任何业务 Task。

### 1.2 7 月 22 日版本值得恢复的不是旧代码，而是职责边界

`4db7993` 的主路径是：

```text
近期真实历史
-> 1 次 Intent 模型理解并拆题
-> 按 Task 并行检索
-> Generate 使用知识和上下文自然回答
-> Commit / Outbox
```

该版本的重要行为：

1. 当前 Session 的近期真实消息一直存在，默认约 15 条。
2. Intent 能看到当前消息、最近约 8 条带角色历史、必要媒体文本和低优先级摘要。
3. 多问题由 Intent 模型输出 `intentTasks`，本地不按标点再拆一遍。
4. 本地主要守 JSON、来源和输出协议，不用大量本地语义规则否定模型 Task。
5. 知识召回结果不会因为后置 Fact 协议错误直接伪装成“知识库没答案”。

当前生产相对 `4db7993`，Executor 相关范围增加约 2.5 万行代码，其中：

- `knowledge_evidence_judge.go`：约 4059 行，旧版本不存在。
- `intent_protocol_validation.go`：约 1392 行，旧版本不存在。
- `answerability_gate.go`：由约 898 行增长到约 2828 行。
- `multi_reply_output.go`：由约 120 行增长到约 1029 行。

代码量本身不能证明某个阶段应该删除，但它说明当前问题不能再靠继续叠加状态、提示词和本地判断解决。本轮只恢复上述职责边界，不整文件复制旧代码，也不回退后来已经稳定的人工、资源和输出安全能力。

### 1.3 最小根治方向

最终方向固定为：

```text
真实消息边界不丢
+ Intent 模型独占语义拆题权
+ Judge 保留一次，但变成非破坏性裁决
+ Judge 协议错误不能冒充资料不足
+ Generate 只看当前 Task、必要上下文和有效证据
+ 普通 ai_reply、服务通知和人工状态机保持既有行为
+ manual_resume 只增加请求绑定的投递修复
```

不新增模型调用，正常路径仍为：

```text
1 Intent + 并行知识检索 + 1 Judge + 1 Generate
```

## 2. 已确认的真实问题

### 2.1 正确知识在 Judge 后处理阶段被清空

当前链路实际存在：

```text
Retriever 得到 RawHits
-> Judge 选择 Candidate
-> 本地校验 Fact、aspect、范围、主体和必要字段
-> 用有效选择重建 Hits / ContextResults / ContextText
-> 未通过的原始知识被删除
-> Hits 为空
-> 被解释为 no-context
-> 转人工
```

已确认案例：

| Run | 客户问题 | 已召回知识 | 分数 | 实际失败点 |
| --- | --- | --- | ---: | --- |
| `7501` | 你们老板是谁啊 | 通用 FAQ 明确回答董事长信息 | `0.848863` | Judge/本地归一化清空正确知识后转人工 |
| `7504` | 附近有啥好玩的 | 门店 FAQ 明确列出附近地点 | `0.894273` | 正确门店知识被判为不足并延后转人工 |
| `7439` | 拖鞋没了 | 门店 FAQ 有领取地点和方法 | 约 `0.8834` | 历史 Judge 路径错杀后转人工 |

这些案例证明：根因不是 FastGPT 没召回，也不是知识答案本身写成“转接”，而是 `RawHits` 经过后置裁决后被破坏性重建。

### 2.2 本地代码在和 Intent 模型争夺拆题权

当前 Intent 模型输出 Task 后，本地仍会重新判断：

- Task 文本是否是原消息的字面 span。
- 本地标点和疑问结构切出了几道题。
- Task 数量是否覆盖本地原子候选。
- `objective / relationToPrevious / resolutionState` 是否满足本地组合规则。
- `resolvedText` 是否符合本地自包含判断。

这会把模型合理的合并、改写和回指补全判成协议错误。例如：

```text
客户：有早餐吗？几点？在哪吃？
```

模型可以输出一个“早餐完整信息”Task，也可以输出三个同对象 Task。只要来源真实、顺序正确且最终完整回答，本地不应仅因 Task 数量与标点切分结果不同而拒绝整个 Intent。

### 2.3 当前题目和历史上下文被混成同一个概念

正确结构必须分成：

```text
ActiveTurnSources
= 本轮需要处理的真实客户消息

HistoryMessages
= 仅用于理解当前问题的近期客户、AI 和人工消息
```

避免重复回答只需要保证旧客户消息不重新成为 Active Task，不等于从 HistoryMessages 删除它们。

当前人工恢复还会把多条物理消息用普通换行拼成一条假消息，导致：

- U1/U2 等真实来源边界消失。
- `sourceRefs` 无法映射真实 message ID。
- 合法 Task 被本地来源协议拒绝。
- 回指关系和消息先后关系失真。

运行 `7505` 就在 Intent 校验阶段失败，Retriever、Judge 和 Generate 都未执行。

### 2.4 协议错误被伪装成“资料不足”

当前以下情况都可能最终变成 `insufficient` 或空 Hits：

- Judge 真正判断证据不足。
- Judge 缺 Task 或缺 Layer。
- Candidate ID 未知或重复。
- Fact aspect 不被本地枚举接受。
- Fact grounding 格式失败。
- 范围或主体归一化失败。
- Judge 超时或返回非标准 JSON。

这些情况不是同一种业务结论。程序协议错误不能触发“知识库无答案”或直接转人工。

## 3. 目标链路

本轮链路保持：

```text
120ms 消息收敛
-> 建立本轮真实消息来源
-> 读取有界近期历史
-> 1 次 Intent
-> 按 Task 并行检索门店库和通用库
-> 1 次非破坏性 Judge
-> 每个 Task 独立形成显式处置结果
-> 1 次 Generate
-> 本地输出协议校验
-> Commit
-> Outbox
```

本轮不新增 Intent、Judge、摘要、情绪或事实核验模型。

## 4. 修改一：恢复真实上下文边界

### 4.1 ActiveTurnSources

继续复用现有消息收敛和最近出站边界：

- 当前单条客户消息就是一个来源。
- 短时间内连续客户消息按真实顺序组成当前 burst。
- 最近一次真实 AI/人工出站之前的客户消息不重新进入当前 burst。
- 每条来源在 Trace 中保留 message ID、消息类型和文本；顺序及时间通过现有
  Message 的 `seqNo/sentAt` 回查，不重复写入 Trace。

这只决定“本轮答什么”，不删除数据库历史，也不标记语义题目已完成。

来源结构使用轻量 URef：

```text
U1 -> messageId / messageType / text
U2 -> messageId / messageType / text
```

不增加字符偏移、UTF-8 SourceSpan 或新数据库字段。

### 4.2 HistoryMessages

继续保留当前 Session 的有界历史池：

- 默认最近约 15 条真实消息。
- 保留客户、AI 客服、人工客服角色。
- Intent Prompt 最多展示最近 8 条必要历史。
- 更早的压缩摘要保持最低优先级。
- 房号、入住状态和现场情况等一次性事实不能仅凭旧摘要继续沿用。

允许过滤：

- 有明确内部标记的系统服务通知。
- 已明确注册为控制指令的单独“1”消息及对应系统欢迎通知。
- 没有有效转写内容的语音占位。

不能因为消息很短、已经回答或本地认为无关，就删除其他真实客户、AI 或人工消息。

### 4.3 BoundContext

Generate 和 Judge 不恢复整段历史，而是使用关系驱动的必要上下文：

```text
independent + clear
-> 不携带历史问答

follow_up / reference_previous / clarification_answer
correction / modify_previous / answer_rejected
resolved_from_context
-> 携带紧邻且真实相关的一组客户/客服问答

conversation_recap
-> 使用更宽但仍有界的近期历史
```

`relationToPrevious / resolutionState` 不再作为本地语义正确性的硬门槛，但继续作为上下文选择信号。

相邻问答组固定为：紧邻的一条客户问题，加其后最多三条连续、同一发送方类型的 AI
或人工客服答复。AI 与人工不能混成一组，空消息和已注册服务通知跳过；历史最后一条
有效正文不是客服回复时不建立相邻组。四条以上连续答复只保留最新三条，并对每条分别
截断，保证最新纠正不会因整组统一截断而丢失。Intent、Judge、Generate 必须复用同一组、
同一顺序；独立新题仍不携带该组。

兼容旧 Profile 或字段缺失时，只使用结构事实兜底：

- `resolvedText` 与原始 `text` 不同且实体来自相邻历史。
- `sourceRefs` 明确引用同一当前 burst 的前一来源。
- 紧邻 AI 正在追问必要字段，当前消息是该字段的直接回答。

不得再由 Judge 自己根据“这、那、刚才”等局部词表重新猜关系。

### 4.4 已回答和需要上下文的区别

不新增已答状态表，使用现有事实：

```text
已 Commit 的 AI/人工 Message
-> 旧题不再成为 Active Task，但可以作为 BoundContext

尚未 Commit、因新消息取消
-> 下一轮可以重新处理，不算重复发送

DeferredTaskIDs 中的 Task
-> 尚待接待或恢复处理

当前完整新问题
-> 新 Active Task，不继承旧业务对象
```

## 5. 修改二：Intent 模型重新拥有语义拆题权

### 5.1 模型职责

Intent 模型负责：

- 当前有几个业务问题。
- 多个短句属于一个问题还是多个问题。
- 一句话中是否包含多个独立问题。
- “那麦田呢”“几点”“在哪吃”的指代对象。
- 当前是新主题、正常追问、槽位回答、否定上一答复还是明确转人工。
- 每个 Task 是否需要知识、资源、工具或接待。

### 5.2 本地只做协议校验

本地允许校验：

- 输出能否解析为 JSON。
- `intentTasks` 是否为数组。
- intent 是否属于支持枚举。
- Task 文本是否为空。
- `sourceRefs` 是否引用本轮真实来源。
- primary 来源是否属于当前客户消息。
- Task 顺序是否与来源顺序一致。
- 是否存在完全相同的重复 Task。
- `resolvedText` 是否凭空引入当前来源和相邻上下文都不存在的实体。

本地不得再校验：

- 标点切出了几道题。
- Task 数量是否等于本地候选数量。
- Task 文本是否必须是原消息的字面子串。
- “和、以及、只要、几点、哪里”必须按本地规则拆分。
- 模型是否漏掉了本地关键词拆题器认为存在的问题。
- `resolvedText` 是否符合一套本地语义模板。

### 5.3 Task 契约

核心字段保持：

```text
intent
subIntent
text
resolvedText
sourceRefs
needsKnowledge
needsResource
needsTool
needsHumanRoute
resourceAction
relationToPrevious
resolutionState
```

字段职责：

- `text`：Intent 模型给出的当前 Task 表达，不作为字面 span 硬校验；客户原始物理文本
  以 `input.currentTurnSources` 为准。
- `resolvedText`：仅在回指或省略时补全为检索问题。
- `sourceRefs`：真实当前轮来源，不是字符位置。
- `relationToPrevious / resolutionState`：用于上下文选择，不用于本地重判语义。

`objective / entities` 可继续用于 Trace 和 Judge 提示，但不得因为缺失或枚举不完整直接拒绝整个 Intent。

### 5.4 Intent 异常恢复

- 第一次不是合法 JSON 时，允许一次只修 JSON/字段协议的重试。
- 不因本地语义意见与模型不同而重跑 Intent。
- 第二次仍无法解析时走现有 Job 重试，不提交残缺 Task。
- 不新增第二个 Intent 模型或关键词拆题器。

## 6. 修改三：Judge 收窄为非破坏性裁决

### 6.1 Judge 保留的职责

Judge 只负责：

- 候选是否直接回答当前 Task。
- 同一知识层是否存在冲突。
- 多条同层证据能否共同覆盖问题。
- 当前证据是完整、部分还是不足。
- 门店层与通用层的有效答案优先级。

Judge 不负责：

- 重新拆题。
- 重新理解整段会话。
- 修改 `text / resolvedText / sourceRefs`。
- 因自身协议错误删除 Retriever 原始证据。
- 用本地字符相似度重新做一套语义 Judge。

### 6.2 RawHits 与 EffectiveHits 分离

内部结果固定为：

```text
RawHits
= Retriever 的原始结果，只读、不可变，用于 Trace 和故障排查

EffectiveHits
= Judge 当前 Task/Layer 合法选择的候选，只提供给 Generate
```

禁止再使用空选择直接覆盖 RawHits。`RebuildKnowledgeRetrieveSelection` 只能写 EffectiveHits，不得让程序失去原始召回证据。

### 6.3 每个 Task、每个 Layer 独立裁决

Judge 状态必须区分：

```text
direct_single
direct_combined
partial
insufficient
protocol_invalid
timeout
malformed
```

规则：

- 一个 Task 的协议错误不能清空其他 Task。
- 门店层协议错误不能直接清空通用层。
- `protocol_invalid / timeout / malformed` 不能被改写成 `insufficient`。
- Candidate ID 不属于该 Task/Layer 时，该 Layer 判 `protocol_invalid`。
- `direct_combined` 包含未知 Candidate ID 时，整个 Layer 无效，不能删除未知项后继续保持 direct。
- Fact aspect 主要用于说明事实维度，不能仅因模型输出了未列举但已 grounded 的标签而删除合法 Candidate；无法识别时归一为 `other`。

### 6.4 非破坏性修复路径

仅保留两个通用修复，不增加分数救援和类别特判。

#### A. model_selected_repair

适用条件：

- Judge 已明确选择当前 Task/Layer 中存在的 Candidate。
- direct/partial 决定本身合法。
- 失败只发生在 Fact JSON、aspect 命名或 CriticalValues 格式。

处理：

- Candidate 选择保持不变。
- 只从该 Candidate 的 FAQ 问题、答案和原文逐字重建 Fact。
- 不增加新 Candidate，不跨层组合，不改变 direct/partial。
- 无法从原文机械重建时保持 `protocol_invalid`。

#### B. exact_faq_fallback

适用条件：

- Judge 为 `insufficient / protocol_invalid / timeout / malformed`。
- 当前层存在显式 FAQ 问法或 alias。
- 当前问题仅做大小写、空白、标点、礼貌语气词规范化后与 alias 完全相等。
- FAQ 正文不是转接指令。
- 同层不存在明确冲突正文。

明确禁止：

- 不使用 `0.70 / 0.85` 分数单独决定答案。
- 不使用 n-gram、字符覆盖、实体重合或 Intent 类型证明语义等价。
- 不处理 `partial`，不能把 partial 升级为 direct。
- 不从长文章中自行制造虚拟 FAQ。

### 6.5 冲突判断边界

本地冲突判断只能使用机械事实：

- 同一显式 FAQ alias。
- 同一明确门店、房型、区域或对象 scope。
- 字面肯定与否定相反。
- 电话、地址、数量、价格、时间等关键值字面不同。

不能使用字符相似度或关键词集合推断两个答案语义冲突。

### 6.6 知识层优先级

只对 Judge 的**有效结果**执行固定优先级：

```text
门店明确转接
-> 门店完整正文
-> 通用完整正文
-> 门店部分正文
-> 通用部分正文
-> no_evidence_handoff
```

这样同时保证：

- 门店正确正文覆盖通用转接。
- 门店明确转接覆盖通用正文。
- 门店噪声候选不能仅因存在就压掉通用完整答案。
- 门店与通用证据不得跨层拼接。

候选总预算仍为 28。同一知识层同时存在精确转接 FAQ 和可信、值得 Judge 复核的正文 FAQ 时：配额只有
一个槽位先保留正文；至少两个槽位先把正文与精确转接同时交给 Judge，再使用剩余槽位
补其他层。正文只存在于 `RawCandidates`、位于 Judge 预算外时，仍必须阻止
`deterministic_handoff`；但 Judge 已经真实看见两项并明确选择精确转接时允许执行该模型
裁决。范围、房型、对象或条件明确不一致的正文不是竞争答案，不能阻止真实转接。

### 6.7 明确转接知识

知识转接必须同时满足：

```text
FAQ 显式问法/alias 与当前问题精确匹配
+ 答案规范化后严格等于“转接”或“转人工”
+ 同层不存在冲突正文
```

不能仅因为 top1 正文里出现“转接”二字就进入人工。

### 6.8 显式处置结果

每个 Task 最终只能形成：

```text
answer
answer_then_handoff
knowledge_direct_handoff
no_evidence_handoff
judge_protocol_retry
```

含义：

- `answer`：使用 EffectiveHits 生成答案。
- `answer_then_handoff`：先回答已确认部分，再处理真正缺失部分。
- `knowledge_direct_handoff`：精确命中明确转接知识。
- `no_evidence_handoff`：Retriever 成功但两层均无可用证据，或知识库未配置、Retriever
  不可用等 Judge 前知识来源不可用。后一种路径仍必须为每个知识 Task 写入显式
  `disposition=no_evidence_handoff`、`decision=insufficient`、
  `decisionSource=source_unavailable`，不得依赖空 disposition 的 legacy 兼容恢复。
- `judge_protocol_retry`：保留的历史内部名称，表示 Judge 协议或调用失败的逐题隔离。
  当前普通异步回复没有独立持久化 Job；严格 exact FAQ 也无法恢复时，该题生成不含
  酒店事实的安全短答，保留失败 Trace，不转人工、不发成功转接话术，也不重复调用
  Judge。第二次 Judge 仍按第 16 节明确延期。

禁止继续使用：

```text
Hits == 0 -> 默认转人工
```

## 7. 修改四：Generate 保持有界且不承担隐式 Judge

### 7.1 Generate 输入

每个 Task 只接收：

- 当前 `text / resolvedText`。
- 当前 Task 的真实 `sourceRefs`。
- 关系驱动的 BoundContext。
- Judge 有效选中的 EffectiveHits。
- Judge 从选中原文得到的 SupportedFacts 和 MissingAspects。
- 当前 Task 的资源或接待边界。

Generate 不接收：

- 未选中的冲突候选。
- 整段无关历史。
- 其他 Task 的知识证据。
- 跨层拼接事实。
- 跨运行“已答题目状态”。

### 7.2 Generate 规则

提示词保持短而明确：

1. 只回答当前 Task。
2. 只能使用 EffectiveHits 和 SupportedFacts 中的信息。
3. 存在性不能扩写成配送范围，地点名称不能扩写距离、路线和时间。
4. 多问题按客户顺序逐项回答。
5. BoundContext 只用于承接，不补答旧问题。
6. 做不到或缺失的部分自然致歉并进入既有处置，不伪造已通知、已安排或已处理。

### 7.3 多问题输出

保留当前 `replyParts`，它只是单次运行内的输出协议，不是持久题目状态：

```json
{
  "replyParts": [
    {
      "taskId": "task-1",
      "content": "给客户的回复",
      "coveredFactIds": ["F1"]
    }
  ]
}
```

本地只检查当前执行需要的结构：

- 每个当前文本 Task 恰好出现一次。
- 没有未知、重复或空 Task。
- `coveredFactIds` 只引用当前 Task 已确认 Fact。
- 必要 Fact 没有被遗漏。
- 内部 JSON 不得原样发送给客户。

不使用本地关键词重新判断回答语义。协议异常时继续复用现有一次 Generate 恢复，不重跑 Intent、Retriever 和 Judge。

## 8. 修改五：人工恢复只恢复未完成 Task

### 8.1 修复消息来源

`resolveResumeMessage` 不再使用：

```go
strings.Join(parts, "\n")
```

恢复输入继续使用和普通 burst 相同的 URef envelope，每条物理消息保留真实 message ID、顺序、类型和文本。

### 8.2 复用现有 Trace，不新建状态表

优先复用：

```text
ReplyPlan.TaskPlans
EvidenceJudge.DeferredTaskIDs
```

恢复时：

- 已回答兄弟 Task 不重新进入 Intent、Retriever 或 Generate。
- Deferred Task 使用原 `text / resolvedText / sourceRefs / MissingAspects`。
- 正常轮把完整无答案或明确转接的 Deferred Task 以
  `output=deferred_knowledge_handoff`、`outputKind=handoff`、
  `replyRequired=false` 保留在 ReplyPlan，Generate 看不到它，但 Trace 和人工恢复仍能
  按稳定 TaskID 找到它。
- 人工恢复时只去掉上述临时执行标记，将 Deferred Task 重新激活为
  `knowledge_text_reply`，再走 Retriever、Judge 和 Generate；已回答兄弟 Task 不恢复。
- 人工期间新增客户消息使用新的 URef，再由 Intent 模型判断新 Task。
- 找不到旧 Trace 或 Task 信息时，才走兼容的重新识别路径。

处置恢复语义固定为：

- `no_evidence_handoff` 是仍可恢复的未完成知识 Task。
- `knowledge_direct_handoff`、明确转人工，以及已真实提交可见答案的
  `answer_then_handoff` 属于已完成转接，不在超时点重新激活原业务题。
- 新版 V2 Trace 的每个 Deferred Task 必须有显式 disposition；空 disposition 仅用于
  有界 legacy 兼容，不能作为新版 Trace 的正常输入。

人工恢复沿用既有超时矩阵，不追加第二段等待窗口：总部网页待接入为 3 分钟，门店待
跟进为 5 分钟，员工已真实接管后的空闲期为 10 分钟。到达原 `manual_expire_at` 后，
已完成转接静默恢复 AI；仍可恢复的 `no_evidence_handoff` 才准备一次真实 AI 续答。

如果现有 Trace 缺少恢复所需 source message IDs，只向现有 Trace 增加该字段，不新增数据库表、Task 状态枚举或独立恢复账本。

## 9. 保持边界与人工恢复例外

本轮不修改：

- 消息收敛时长和现有最新消息检查。
- 数据库表、Migration 和外部 API。
- 普通 `ai_reply` 与 AI 服务通知的 ClientMsgID 和 Commit；Outbox 只增加 claim 后人工
  路由复核与终态保护，不改变数据库结构或外部 payload。
- 企微回调和员工本地消息识别。
- 既有人工超时矩阵与恢复状态机，不追加第二段等待窗口。
- 房号追问规则。
- 直接转人工状态机。
- 小程序、定位、电话等结构化资源提交。
- 知识库内容和 FastGPT 配置。
- Intent、Judge、Generate 模型及 API 地址。
- 计费、Token 统计、DTO、枚举和 WebSocket。

`manual_resume` 的恢复 request ID 绑定来源消息 ID；ClientMsgID 使用 `ai_manual_resume_`
加 request ID 的 SHA-256 前 24 字节十六进制。严格匹配恢复 Message 时可以补建缺失
Outbox，符合条件且未开始外部发送的 `cancelled` Outbox 可以原子恢复为 `pending`。
`pending`、仍可重试的 `failed` 和五分钟内的 `sending` 进入 `delivery_pending`，不重跑
模型且不增加 `RetryCount`；`sent` 后复核完成。陈旧 `sending` 或 claim 后被员工接管
取消的投递进入 `delivery_uncertain`，不重放、不重跑模型，恢复 Task 终止为失败，会话
保持人工复核且不自动过期。

`failed` 且 `next_retry_at=nil` 单独进入终态 `delivery_failed`：不重放、不补发、不重跑
模型、不消耗恢复重试次数，恢复 Task 直接失败并停止排期，会话保持或恢复门店人工、
清空自动过期并标记需要人工跟进。即使恢复 Message 和终态 Outbox 已落库、完成 RunLog
尚未来得及写入，请求绑定 Message 也必须成为崩溃恢复屏障；其他已有提交但缺少权威
Trace 的情况进入 `delivery_uncertain` 人工复核，不能再次运行模型。

发送端统一使用 claim，并在协议/KF 外部调用前以及 CLI 返回 claimed 项前复核人工路由；
迟到 CLI 成功或失败回执只允许更新仍为 `sending` 的行。任何 `sending` 都不进入
`ListPending`，因为外部接口没有幂等键，重放会产生重复消息。CLI Poll 返回之后到桥接端
真正发送之前仍存在无法由服务器撤回的窗口；彻底关闭需要增加 attempt token 和发送前
CAS 外部契约，不属于本轮接口不变的实施范围。

## 10. 预计代码范围

### 10.1 Intent 与上下文

```text
internal/ai/runtime/reply_trigger_service.go
internal/ai/runtime/executor/intent_protocol_validation.go
internal/ai/runtime/executor/intent_model_detector.go
internal/ai/runtime/executor/intent_pipeline.go
internal/ai/runtime/executor/intent_config_matcher.go
internal/ai/runtime/executor/context_builders.go
internal/ai/runtime/executor/manual_resume_plan.go
internal/ai/runtime/executor/reply_tag_context.go
internal/ai/runtime/internal/impl/adapter/message_adapter.go
internal/pkg/replyruntime/manual_resume_context.go
internal/services/ai_manual_resume_task_service.go
```

### 10.2 Judge 与处置

```text
internal/ai/runtime/executor/knowledge_evidence_judge.go
internal/ai/runtime/executor/answerability_gate.go
internal/ai/runtime/executor/intent_human_route.go
internal/ai/runtime/executor/event_consumer.go
internal/ai/runtime/internal/impl/callbacks/trace_callback.go
internal/ai/runtime/internal/impl/callbacks/runlog_callback.go
```

### 10.3 Generate 与输出

```text
internal/ai/runtime/executor/multi_reply_output.go
internal/ai/runtime/executor/generate_recovery.go
internal/ai/runtime/executor/generated_reply_validator.go
internal/ai/runtime/reply_commit_service.go
internal/services/message_service.go
internal/services/channel_message_outbox_service.go
```

`reply_commit_service.go` 增强内部 Commit Trace 与恢复判定契约：每条真实提交消息记录
`taskIds[]`。非稳定 ID 核对持久化消息的 request ID、消息类型、正文和结构化资源身份；
稳定的 `manual_resume` Task/资源归属 ID 命中时，第一次落库的 Message 是权威内容，后续
只修复其 Outbox，不用重跑后的措辞或 payload 覆盖它。`message_service.go` 与
`channel_message_outbox_service.go` 只承载上述
`manual_resume` 投递修复；不得借机重构普通 MessageService、普通 Outbox、计费、
数据库模型或整套 Executor。

## 11. 实施顺序

### 阶段一：建立基线

1. 从生产提交 `40cc24b` 创建独立干净 worktree。
2. 保存当前未提交 diff 快照，绝不触碰当前脏工作区。
3. 备份当前 release、运行配置、Intent Profile 和实例人设。
4. 固定 `7501 / 7504 / 7505 / 7439` 的 Trace 作为回归样本。
5. 对照 `4db7993` 的职责边界，但不复制旧文件覆盖后来功能。

### 阶段二：修复来源和 Intent

1. 统一普通 burst 与人工恢复的 URef envelope。
2. 删除本地原子候选数量和字面 span 硬校验。
3. 保留来源真实性、顺序、重复和实体凭空引入检查。
4. 让关系字段只决定 BoundContext，不再成为本地语义否决条件。

### 阶段三：修复 Judge 的破坏性应用

1. 分离 RawHits 和 EffectiveHits。
2. 增加 `protocol_invalid / timeout / malformed` 独立状态。
3. 按 Task/Layer 隔离无效结果。
4. 实现受限 `model_selected_repair`。
5. 实现严格 `exact_faq_fallback`。
6. 将 Fact aspect 从决定 Candidate 生死的硬门槛降为可归一化元数据。
7. 用显式 disposition 替换 `Hits == 0 -> handoff`。

### 阶段四：收口 Generate 和恢复

1. Generate 只读取 EffectiveHits、SupportedFacts 和 BoundContext。
2. 保留 `replyParts`、事实覆盖和内部协议防线。
3. 人工恢复只处理现有 DeferredTaskIDs。
4. 不创建 AnswerTask、TaskClosure 或新状态机。

### 阶段五：测试、提交和部署

1. 最新定向测试、普通测试、完整 Race、Vet 与 Linux amd64 构建已在最终实现差异上通过。
2. 完成最终复审。
3. 分两个可独立回滚的提交：
   - Judge 非破坏性裁决、显式 disposition 和输出联动。
   - Intent、URef、BoundContext 和人工恢复。
4. 推送独立修复分支。
5. 分两次发布，避免 Judge 和上下文问题互相掩盖：
   - 发布 A：只上线 Judge 非破坏性修复，验证正确知识不再被清空。
   - 发布 B：再上线 Intent、URef、BoundContext 和人工恢复修复。
6. 每次都构建独立 Linux amd64 release，原子部署，并保留各自上一版回滚点。
7. 部署后再做有限真实模型和企微出站验收。

## 12. 自动测试

截至 2026-08-31，最新定向回归、以下普通测试、完整 Race、Vet 与 Linux amd64 构建
已在当前实现差异上通过。这些结果只证明代码级回归和目标平台可构建，不代表最终复审、
提交、推送、部署或真实模型验收已经完成。

### 12.1 上下文和 Intent

- 最近已发送答复仍可作为 BoundContext，但不会重新成为 Active Task。
- “那这个呢”“几点”“不是这个意思”能绑定紧邻问答。
- “早餐几点”是完整新问题时不继承上一轮房型或停车对象。
- 除明确控制消息外，其他真实短消息不会被历史构建器误删。
- 人工恢复保留每条物理消息的 URef 和顺序。
- “有早餐吗？几点？在哪吃？”不因本地候选题数与模型 Task 数不同而失败。
- “办公桌和沙发都有吗”允许模型输出一个 compound Task。
- 合理 `resolvedText` 不因不是原文字面 span 被拒绝。
- 完全重复 Task 可去重，凭空来源和逆序 Task 仍被拒绝。

### 12.2 Judge

- RawHits 在所有 Judge 结果下都保持不变。
- 一个 Task 协议错误不影响其他 Task。
- 一个 Layer 协议错误不被改写成 `insufficient`。
- `direct_combined` 含未知 Candidate ID 时整个 Layer 为 `protocol_invalid`。
- Judge 已选 Candidate、Fact 格式坏时只修该 Candidate。
- 无法逐字重建 Fact 时不做语义猜测。
- exact FAQ 只在严格 alias 相等且无冲突时兜底。
- `partial` 不被 exact fallback 升级为 direct。
- 门店噪声候选不能覆盖通用完整答案。
- 门店正文覆盖通用转接，门店明确转接覆盖通用正文。
- 无 alias、无 entities 的老板/董事长与附近/周边正文在紧预算下仍与精确转接正确竞争。
- 配额一条保留正文；配额两条时同层正文和精确转接同时进入 Judge。
- 预算外 `RawCandidates` 正文阻止异常时确定性转接；Judge 已看见两项并明确选转接时允许。
- 不同对象、房型、范围或条件的正文不能错误阻止精确转接。
- Fact aspect 未知但事实已 grounded 时归一为 `other`，不清空 Candidate。

### 12.3 知识回答

- 老板信息召回后不再因 Fact/协议格式错误转人工。
- 附近游玩知识召回后不再被后处理清空。
- 拖鞋领取地点和方法正确回答。
- 矿泉水数量与免费事实均进入回复。
- 机器人存在性不能扩写成送房能力。
- 地点名称不能扩写距离、时间或路线。
- 两层真实无证据时才进入 `no_evidence_handoff`。
- Judge 协议错误进入确定性安全短答，不重跑 Judge，也不发送虚假转接成功话术。

### 12.4 多问题和输出

- 一题协议错误不吞掉其他已确认答案。
- 多 Task 按客户顺序生成 `replyParts`。
- 单任务、多任务、代码块和字符串化 JSON 都不会泄漏内部协议。
- Generate 第一次协议失败时只重试 Generate。
- 普通 `ai_reply` 与 AI 服务通知的 Commit 和稳定消息 ID 保持不变；内部
  Commit Trace 必须保留 `taskIds[]`，外部渠道只有 Outbox `sent` 才算业务 Task 客户
  可见，服务通知不能结算业务 Task。
- `manual_resume` 使用 request-bound ClientMsgID；严格匹配时补建缺失 Outbox，合法
  `cancelled` 原子恢复为 `pending`，`pending/failed/新鲜 sending` 仅为
  `delivery_pending` 且不重跑模型、不增加 `RetryCount`，`sent` 后复核完成；陈旧
  `sending` 或 claim 后取消进入 `delivery_uncertain`，不会重试或补发，并保留人工复核。
- `failed + next_retry_at=nil` 进入 `delivery_failed`；没有 RunLog 但已有 request-bound
  Message 和终态 Outbox 时也不重跑模型，并保持人工跟进。
- 员工接管能取消普通 AI 的 `pending/failed/sending` Outbox；协议/KF 发送错误与 CLI
  迟到回执不能覆盖取消终态。CLI 已 Poll 返回后的外部发送窗口作为已记录残余边界。
- 一题已答、一题 Deferred 时，人工恢复只继续 Deferred Task。

聚焦命令：

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

## 13. 有限真实模型验证

未经用户明确同意，不运行 50 轮测试。本轮只执行 10 至 15 个代表场景，每个场景一次；仅不稳定场景允许最多重复三次。

当前尚未执行本节的真实模型、企微最终投递或生产观察，必须在 A、A+B 分阶段部署后
据实记录，不能用已通过的自动测试替代。

固定场景：

1. 你们老板是谁。
2. 附近有什么好玩的。
3. 拖鞋没了怎么办。
4. 房间里有几瓶矿泉水，免费吗。
5. 单独问办公桌、单独问沙发、一起问办公桌和沙发。
6. 有早餐吗；几点；在哪吃的连续短消息。
7. 一条消息同时问停车和拖鞋。
8. “那麦田呢”后切换到新的早餐问题。
9. 人工恢复期间的三条客户消息。
10. 明确转人工知识与普通正文知识冲突。
11. 外卖机器人只回答存在性。
12. 地点知识不扩写距离和步行时间。

真实 Trace 必须确认：

```text
Intent 调用 1 次
Judge 调用 1 次
Generate 正常路径调用 1 次
每个 Task 均有独立 Retriever 和 Judge 结果
RawHits 始终保留
EffectiveHits 只来自合法选择
协议错误与资料不足使用不同状态
客户消息真实入库并发出
```

## 14. 验收标准

- `7501 / 7504 / 7439` 同类正确知识不再被后置协议清空。
- `7505` 同类多消息不再因来源扁平化或本地题数失败。
- 历史能支持回指、否定、反问和槽位回答，完整新题不继承旧对象。
- 单句多问题和连续短消息由 Intent 模型拆分，不由本地标点规则决定。
- 普通知识正文不是转接时，不会因 Judge 协议错误进入人工。
- Judge 仍能阻止存在性扩写、距离外推和跨房型拼接。
- 一题失败不影响其他题回答。
- 无空回复、内部协议泄漏、重复发送或历史消息批量补发。
- 正常模型调用次数不增加。
- 没有新增数据库、全局状态机或外部共享契约。

## 15. 发布与回滚

### 15.1 发布前

- `git fetch origin` 并检查 `codex/customer-audit`、`codex/ai-billing` 对目标文件的同文件修改。
- 2026-08-31 的统计口径为：先执行 `git fetch origin`；本工作区取
  `git diff --name-only 40cc24b --` 的已跟踪路径，并行分支分别取从其与 `40cc24b`
  的 merge-base 到远端 tip 的路径；两组路径排序、去重后求交集。
- 按上述口径，当前与 `codex/customer-audit` 的完整同路径交集为 12 个文件：
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
- 合并必须同时保留审计分支的租户隔离与发送约束，以及本分支的真实 Burst/URef、
  发送原子认领和人工恢复语义；禁止用整文件覆盖解决冲突。
- 按同一口径，当前与 `codex/ai-billing` 的同路径交集为 0；提交和 push 后仍须重新
  fetch 核对。
- 使用独立 worktree，不触碰当前脏工作区。
- 备份当前 release、运行配置、Intent Profile 和实例人设。
- 本轮无 Migration，不导入或恢复数据库。

### 15.2 发布

以下步骤尚未执行：

1. 先部署发布 A，只包含 Judge 非破坏性修复。
2. 检查 systemd、`8083`、进程重启次数和模型错误。
3. 使用隔离会话验证老板、附近游玩、拖鞋、矿泉水和转接冲突。
4. 发布 A 稳定后再部署发布 B，只加入 Intent、URef、BoundContext 和人工恢复修复。
5. 使用隔离会话验证连续短句、回指、新主题和人工恢复。
6. 每次发布后观察首批 50 个自然生产回复或 1 小时，只观察，不主动制造 50 轮测试。
7. 重点监控：
   - Judge `protocol_invalid/timeout/malformed` 与历史 `judge_protocol_retry` disposition 次数。
   - exact FAQ fallback 次数。
   - RawHits 非空但 EffectiveHits 为空。
   - 知识回答转人工。
   - Intent 协议失败。
   - Generate 重试和内部 JSON 拦截。

Release A 必须从生产基线加最终 A-only 修复独立形成，不能从已经包含 Release B 的线性
tip 反向声称得到。Release A+B 必须以该最终 A commit 为父提交，再叠加 B 修复。发布前
分别记录两者最终 commit、release 目录和 Linux 二进制 SHA-256。

### 15.3 回滚

发布 A 异常时立即切回 `40cc24b`。发布 B 异常时优先切回已验证的发布 A；若问题仍在，再切回 `40cc24b`。

出现以下任一情况立即回滚：

- 新消息无法入库或发送。
- 多问题明显漏答或顺序混乱。
- 普通知识明显错误扩写。
- 人工状态、房号追问或资源消息异常。
- Outbox 重复或持续堆积。
- Judge `protocol_invalid/timeout/malformed` 持续升高并导致明显空回复或延迟。

数据库、知识库和运行配置保持当前状态，不做反向 Migration。

## 16. 明确延期

以下内容只有取得独立、可重复的生产证据后再单独立项：

- 持久化 Task Ledger。
- 跨运行“已答题目”状态。
- 同会话全局 Coordinator。
- Message 与 Outbox 新事务协议。
- RunLog Checkpoint 和崩溃恢复状态机。
- SourceSpan 哈希和新的 TaskKey。
- 新的事实核验模型。
- 第二次 Judge 或额外情绪模型。
- 基于关键词的本地语义拆题器。
- 全面移除 Judge。

## 17. 最终完成定义

本轮完成不以新增多少机制为标准，而以现有链路是否重新做到以下行为为标准：

```text
看得懂当前对话
-> Intent 模型正确拆题
-> Retriever 有知识就保留原始证据
-> Judge 只做非破坏性、可解释的证据裁决
-> Generate 自然完整回答
-> 没知识或明确规则才转人工
-> 多问题不漏答
-> 旧问题不重复答
-> 不虚构知识和动作
-> 回复真实发出
```

如果完成后仍出现具体问题，必须先从 Trace 判断属于 Intent、Retriever、Judge、Generate、Commit 还是知识数据；不得在没有生产证据的情况下继续增加全局状态、判断模型或本地语义规则。

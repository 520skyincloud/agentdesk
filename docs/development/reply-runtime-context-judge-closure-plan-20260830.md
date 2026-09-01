# 回复引擎上下文与知识裁决最小根治计划

## 文档信息

- 文档状态：截至 2026-08-31，当前工作树已完成 Intent/Judge 收口并通过代码级验证；此前
  Release A+B 的部署与首轮定向真实模型验证仅作历史记录，本轮不声明已提交、已推送、
  已生成新 release、已完成新的真实模型复测或企微最终出站验收
- 修订日期：2026-08-31
- 原始修复基线：`40cc24be3972ab341af7f0ef83a4732e9630ad87`
- 历史 A+B 运行提交：`39e8656a4e8d9bf25cd2df5e8619592af2ad5c67`
- 历史 A+B release 参考：`/opt/agentdesk/releases/20260831-142758-context-judge-ab-39e8656`
- 集成分支：`codex/reply-runtime-context-judge-ab-final`
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
- 紧邻 AI 或人工客服正在追问必要字段，当前消息是该字段的直接回答；两类客服发送方
  使用同一套紧邻槽位上下文规则。

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
- 每个包含自包含业务问题的 URef 是否至少由一个可执行 Task 以 `sourceRefs[0]` 主认领；
  context ref 不能代替该 URef 自己的问题归属。
- Task 顺序是否与来源顺序一致。
- 是否存在相同来源、原文、意图、目标、实体和动作所有权的重复 Task；仅
  `resolvedText` 措辞不同不能重复执行。
- 保守原子候选能否机械证明漏题、重题、串题、额外 Task 或非法 compound；本地只拒绝
  协议并触发现有一次 Intent 修复，不创建或改写 Task。
- `text` 是否能结合 objective、主题、实体和文本锚点归属于 primary 原子问题。
- `clear` 的 `resolvedText` 是否由当前问题支撑，以及 `resolved_from_context` 是否只使用
  已声明的更早 URef 或 BoundContext 补全。

本地不得再校验：

- 标点切出了几道题。
- Task 文本是否必须是原消息的字面子串。
- “和、以及、只要、几点、哪里”必须按本地规则拆分。
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

- 不使用 `0.70 / 0.85` 分数单独决定答案；`0.70` 只控制候选是否进入 Judge 复核，
  `0.85` 只参与 Judge 前的高置信候选保留。生产链路没有 Judge 失败后的语义分数救援。
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
2. 保留保守原子候选协议验收，删除把候选当成第二套 Intent 的语义猜测和逐字 span 要求。
3. 保留 primary 所有权、来源真实性、顺序、重复、漏题/串题和上下文越界检查。
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

### 阶段五：历史发布计划

以下步骤是原计划，保留用于回滚和审计边界，不代表本轮已经完成提交、推送或部署：

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

2026-08-31 的上一轮定向回归、普通测试、完整 Race、Vet、`git diff --check` 与
Linux amd64 构建已在当时冻结的实现差异上通过。该历史 Server SHA-256 为
`e9bcd0e551f40ffbfa57fd899000baa7d305443035cfc92618e3eafde8ed3d59`，评测器
SHA-256 为 `5fa0c9c34f374f4e24d63e092521c4274c15547717ffbb6a4611dbc1b502e068`。
2026-08-31 三遍审查后的冻结差异重新通过相同验证，当时 Server SHA-256 为
`3f4c3c9f8cdb56265ea198a909a6334981bbf9b6ebd36d2be307c8769bf1fe5a`，评测器
SHA-256 为 `693e69cec620eebb919fc7edc19ff9ca23a54ea387e1e47668c71306c9e1185e`。
以上 SHA 都属于较早冻结差异，不对应当前未提交源码。最终提交后必须从干净 detached
worktree 重新验证和构建，再记录 commit、Server/Eval SHA、release 和部署结果。

### 12.1 上下文和 Intent

- 最近已发送答复仍可作为 BoundContext，但不会重新成为 Active Task。
- “那这个呢”“几点”“不是这个意思”能绑定紧邻问答。
- “早餐几点”是完整新问题时不继承上一轮房型或停车对象。
- 除明确控制消息外，其他真实短消息不会被历史构建器误删。
- 人工恢复保留每条物理消息的 URef 和顺序。
- “有早餐吗？几点？在哪吃？”由 Intent 创建三个 Task，本地以 primary 所有权和保守
  原子覆盖验收，context ref 不能吞掉更早问题。
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

截至本次文档修订，未新增本节真实模型、企微最终投递或生产观察结论；此前 A+B 定向
验证记录见第 18 节，仍不能用自动测试替代真实验收。

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
- 2026-08-31 提交前按当前 13 个未提交文件重新 `fetch` 并核对：与
  `codex/customer-audit` 的实际待提交同路径交集只剩
  `docs/development-handoff.md`，与 `codex/ai-billing` 仍为 0。上述 12 个文件属于从
  `40cc24b` 统计整个历史分支差异的旧口径，不代表这一次提交会触碰 12 个共享文件。
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

## 18. Release A+B 定向验收后的最终收口

截至 2026-08-31，此前 A+B 运行记录为：

```text
commit  39e8656a4e8d9bf25cd2df5e8619592af2ad5c67
release /opt/agentdesk/releases/20260831-142758-context-judge-ab-39e8656
```

定向真实模型场景确认了六类剩余问题：8 Task/28 Candidate 的 Judge 在固定 15 秒内
超时；外卖地址的正确 Candidate 因局部 Fact 类型错误被判协议失败；“那麦田呢”和
“外卖地址再说一遍”偶发被降为 `interaction/clarify`；“两瓶是否都免费”漏掉数量；
办公桌与沙发分别询问被错误合成一个交集 Task。

当前工作树只在现有 Intent/Judge 边界内收口：

- `clarification_answer` 限定为回答紧邻 AI 或人工客服正在进行的必要字段追问；已完成
  业务答复后的细节、比较、复述和重新回答使用 `follow_up/reference_previous`。中性复述
  不再误判 `answer_rejected`。
- 紧邻 AI 或人工客服用“有没有/是否/您是问……吗”等是非式问题确认必要字段时，客户的
  “是的啊”必须触发现有一次 Intent 协议修复并回到原业务 Task；姓名、房号、偏好、条件、
  范围和选项等直接回答使用同一套紧邻上下文规则，泛化帮助询问不受影响。
- “外卖地址再说一遍”等名词式业务目标，只要表达明确的信息或动作目标，就触发一次
  Intent 协议修复，不能因没有问号而降为普通闲聊或泛化澄清。
- 上一轮有多个问题时，只有当前复述锚点唯一命中其中一题才强制回业务 Task；裸
  “再说一遍”继续保持歧义。客服追问槽位后，“谢谢、好的、不用了”等明确纯互动正常
  收尾，“是的/不是”、房号、姓名等真实槽位值仍继承原业务任务。
- `objective=action_request` 且原话已经是自包含执行请求时，包括发资源、换房、派人维修
  和配送用品，即使模型漏掉 Resource/Tool 标记，也触发现有一次 Intent 协议修复；
  房号等紧邻槽位回答仍按上下文处理。
- 动作请求以模型已经声明的 `action_request` 为前提，只校验当前原话是否存在具体目标，
  不建立有限业务动词表；“预约早餐、申请发票、配送矿泉水”属于完整目标，“帮我、我要、
  需要”和“我想要一份、给我送一个”等只有动作框架或数量量词的表达保持澄清。
- 肯定 FAQ 前缀只有在后续没有否定、限制、数量改口或当前必要事实不确定时才确认
  前提；“是的，但具体数量不确定”不能继承问题里的数量，无关细节不确定性不会抹掉
  已经明确确认的事实。
- 多主体问句的肯定前缀不能覆盖答案显式遗漏的主体；答案只写 A 时不得把事实扩给 B。
  “都是/都有/均为”只有在当前子句显式覆盖全部主体，或答案开头仍保持 FAQ 整体主语时
  才能继承问题主体，无关对象或已缩窄后的整体谓词不能重新扩大范围；整体结论后的
  单主体补充不撤销前面的整体覆盖。FAQ 问题已完整列出同类型主体时，允许受控的全称
  同类总称继承，部分、其他、某个及混合类型表达继续拒绝。
- `interaction/clarify + resolved_from_context` 作为字段自相矛盾触发现有一次 Intent
  协议修复；不增加文本关键词分类器。
- 不同答案结果和同一对象的紧密答案目标如何拆分仍由 Intent 模型负责，不要求 Intent
  在检索前知道证据分组。本地只使用保守原子候选验收可机械证明的漏题、重题、串题、
  额外 Task 和非法 compound；“分别/各自/逐项”等明确逐题要求可作为覆盖证据，但本地
  不创建 Task。同一 URef 内只在 Task 文本能唯一映射原文位置时校验顺序，无法唯一定位
  时不做语义猜测。
- Judge 继续单次调用，保留普通单题 15 秒基线，仅按 Task/Candidate 向上扩到 28 秒；
  父级回复 deadline 始终为 Generate、Commit 等下游保留 12 秒并裁剪阶段预算，无可用
  预算时不调用模型。
- `supportedFacts/missingAspects` 局部类型错误按 Task/Layer 修复；Candidate、decision、
  cardinality 和对象主题仍严格校验。
- direct 答案只闭环所选 FAQ 机械确认的查询数量；`partial` 保留已确认事实。中文和
  阿拉伯数字同单位数量机械等价。
- 所选 FAQ 已机械补齐全部必要事实时，清理同主体、同维度的陈旧
  `missingAspects`，将 `partial` 晋升为 direct；配送范围和条件等真实缺失项不清理。
- “一间房、三间房、两位客人入住”等数量只作为查询范围；“两瓶矿泉水、两个枕头”
  等物品数量继续作为关键值。
- “加一条浴巾、帮我拿两瓶水、推荐一个房型”中的数量属于服务参数或结果基数，不要求
  FAQ 重复；价格、存在性、数量确认等事实问题中的同一数量继续严格绑定。
- 单一明确主体下，客户数量与所选 FAQ 唯一明确数量冲突时严格判为协议无效；多主体
  compound 按“主体 × 数量”绑定，每个主体都必须由对应 FAQ 分句或问答单元支持，
  模型 Fact 不能交换主体和值；合计数量只接受明确总量，或全部命名主体的同单位唯一
  分项机械求和。不能拿另一个主体的数量补齐。Intent 未输出实体时，只在单一查询数量和单一所选
  FAQ 的保守边界内继续检查同单位冲突。
- 同一 `time` aspect 内继续区分开始、结束和时长；客户明确询问多个条件时按
  “主体 × 工作日/周末/节假日 × 时间槽”逐格覆盖，不能用工作日开始时间消除周末或
  结束时间的缺失项。
- 配送地址的“怎么填/填哪些”统一为 address/delivery 问题签名，真实地址冲突继续从
  FAQ 正文和关键值判断。
- 完整业务问句及“早餐时间告诉我”等没有问号的自包含信息请求，不能以
  `interaction/clarify` 绕过业务链路；判断使用信息型 `objective`、当前原话和结构化
  未决候选，不增加业务关键词分类器。已声明知识、资源、工具或接待动作的 Task 同样
  受此约束，协议异常只触发现有一次 Intent 修复。
- 全部所选 FAQ 中同主体、同范围、同单位的冲突数量都会使当前层失效，不能由另一条
  匹配数量掩盖；无实体的单一数量问题同样执行该保护，明确不同空间范围不互相污染；
  明确写成“四瓶饮料”的其他物品分句不会污染“两瓶矿泉水”，无主体数量仍继续检查。
- 单主体价格或时间的省略表达只在同一 FAQ 问题或答案明确包含当前 Task 主体、答案分句
  同范围且没有其他显式业务主体时成立；“早餐不收费”不能支撑“停车免费”，“晚餐六点”
  不能支撑“早餐几点”。
- Intent 缺少实体时，从数量相邻文本机械恢复可唯一配对的主体；单一数量和多数量问题
  均按“主体 × 数量”校验，禁止其他物品污染或主体间交换数量。共享谓词只分配其后
  的数量；无标点并列句在查询和候选答案中都按连接词位于数量前后的结构绑定相邻主体。
  无法唯一恢复时保守拒绝，不猜测多主体语义。
- 时间槽支持 `HH:MM` 和“早上七点/九点半”等明确中文时刻；“几点开始和结束”要求
  start/end 两槽，FAQ 问题中的唯一主体可以绑定答案里的纯时间范围；两个条件化开始
  时间不冒充完整范围，“下午”等时段前缀在明确范围表达中同时约束两端，
  “七点开始到九点”等相连范围保留起止端点，不同表达的同一时刻按机械等价处理；
  FAQ 主体过滤首个无主体时间分句，工作日与周末条件互补；“晚上八点到两点”和
  “晚上十点到次日两点”均保留跨午夜边界。
- 多个时间主体按“主体 × 时间槽”分别闭环；时间值按分句提取，主体只传播给随后
  无主体时间分句，出现午餐、入住、退房等新主体后停止传播。事实已经明确写出其他主体
  时不得重绑；“营业/供应/开放时间”可以绑定 FAQ 唯一主体，入住、退房、开门、关门
  不可被当成通用时间。“从什么时候到什么时候”“几点至几点”均要求 start/end 两槽。
- malformed `missingAspects` 仅在所选 FAQ 重建后确实完整时晋升 direct，真实缺失继续
  保留 partial。
- 被同时选中的配送地址候选必须检查具体地址值；不同门店或不同街道地址禁止进入
  Generate，同一地址的楼层/房号补充不视为冲突；“南七店/东七店”短门店名也参与
  冲突判断，“本店/门店/到店”等泛称不作为地址值，冲突时沿用严格拒答防线。
- 人物姓名比较忽略常见称谓后缀，锚定裸姓名同样参与判断；未知、保密和角色词不作为
  人名，保留真实不同姓名的冲突检测；不同身份答案触发严格拒答防线，不能进入
  Generate。

最终工作树已通过：

```bash
go test -p=1 ./... -count=1
go test -race -p=1 ./... -count=1
go vet ./...
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath \
  -o .codex-build/context-judge-ab-final/agent-desk-linux-amd64 ./cmd/server
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath \
  -o .codex-build/context-judge-ab-final/reply-runtime-eval-linux-amd64 ./cmd/reply-runtime-eval
```

2026-08-31 带显式输出路径重建后的 SHA-256：Server
`3f4c3c9f8cdb56265ea198a909a6334981bbf9b6ebd36d2be307c8769bf1fe5a`，评测器
`693e69cec620eebb919fc7edc19ff9ca23a54ea387e1e47668c71306c9e1185e`。

本节没有新增数据库、知识库、人工状态机、Outbox、计费、模型配置或外部接口变更。
最终 release 只定向复测 AA01、AA03、AA04、AA05、AA06、AA08；未经用户明确同意不
运行 50 轮测试。

## 19. 2026-08-31 三遍完整审查后的最小修复

最终审查没有引入新的业务判断层，只补齐五个本地契约：

1. 不同主 `sourceRef` 的相同短句不得去重，避免连续消息跨上下文漏题。
2. 槽位追问后只正向识别可机械确认的槽位值；混有房号等值时修复 Intent 协议，
   `哈哈`、`晚点再说` 等互动不误伤，开放帮助询问不冒充槽位追问；明确取消上一任务时
   必须使用 `cancel_previous`。
3. 只有裸“再说一遍”可绑定唯一上一题；带名字、地址等锚点时仍需匹配上一题。
4. 混合动作请求里的数量保持为动作参数，裸 `是否/是不是/有没有` 不会改变其语义。
5. Intent 未给出 `entities` 时，单主体价格问题从自包含 Query 保守恢复唯一主体，防止
   早餐、停车等跨主体证据污染；比较题和歧义主体不救援。

对应回归覆盖跨来源同文 Task、`谢谢，1208`、`1208，你烦不烦`、`哈哈`、
`晚点再说`、开放帮助询问、取消关系、显式复述锚点、`帮我拿两瓶水，是否可以送到房间`
和 `停车免费吗 + entities=nil`。本轮继续维持
门店层 `protocol_invalid` 时允许合法通用层胜出的现行产品契约，没有擅自改变知识层级。

2026-09-01 重新同步远端后，当前 16 个已跟踪待提交文件与
`origin/codex/customer-audit` 仅重叠 `docs/development-handoff.md`，与
`origin/codex/ai-billing` 重叠为 0。本轮仍无数据库、知识库、模型调用次数、人工状态机、
Outbox、计费、运行配置或外部接口变化。

## 20. 2026-09-01 最终发布阻断收口

三遍完整审查后追加以下结构边界，未改变主调用链：

1. `interaction` 的业务矛盾校验只作用于 `clarify`。天气工具、AI 身份和普通社交问句
   继续按模型协议执行；其他 interaction 仍不得携带未经声明的知识、人工或非天气工具。
2. 紧邻 AI 已完成答复后的明确否定若被模型降为 frustration，协议只触发已有一次
   Intent 修复；不由本地代码直接造接待 Task，人工答复和普通情绪不触发。
3. `entities` 为空的单主体 existence 问题从 Query 保守恢复唯一主体，贯穿 Candidate、
   FAQ 问答单元和 Fact grounding，阻断早餐/晚餐等跨主体证据；多主体仍交给 Judge。
4. 单主体多条件数量按“主体 × 条件 × 单位”比较，工作日和周末等不同条件互不冲突。
5. FAQ 答案明确写“每天/每日/全年”时只扩宽日期类型维度，写“全天/全时段/所有时段”
   时只扩宽昼夜时段维度；一个维度的全称表达不能抹掉另一维度的限制，也不能把分别满足
   两个条件的不同 FAQ 拼成条件交集。

新增用例已覆盖天气、AI 身份、混合天气与知识、明确答复否定、人工答复边界、普通抱怨、
无实体早餐/晚餐、书桌/办公桌同义词、多主体不推断，以及工作日/周末数量一致与冲突。

以下验证和产物只对应较早冻结差异，不对应当前未提交源码：全仓普通测试、`go vet`、
完整 Race、`git diff --check` 和 Linux amd64 双构建。当时 Server SHA-256 为
`a2e6e8f637da06f269790253b36075323cea49d8ab9ab952bc957ce5868d7694`；
当时评测器 SHA-256 为
`5678ee6ab36a739030c5a4c1a02a076787dfa894c634a0783ed23375da7fb083`。

最终提交后必须从干净 detached worktree 重新验证和构建。本节仍不声明已提交、推送、
部署或完成真实模型验收。生产发布后仅执行 AA01、AA03、AA04、AA05、AA06、AA08，
不运行 50 轮。

# AI 回复 Runtime V3 完整链路优化实施方案

> 文档状态：核心运行时修复已实施，尚未部署
>
> 编写基线：独立 worktree 分支 codex/v3-runtime-correctness-fix
>
> 代码基线：96e13d39bd81065c8dce727136c6501b30c1f3f6

## 0. 先说结论

当前问题不是某一个词没有写进提示词，也不是只需要把模型换成 GPT 或 DeepSeek。
真正需要修复的是三个边界：

1. 知识库命中内容只是证据，不是人工动作，也不是可以原样发送给客户的答案。
2. 每一个客户问题必须有自己的 Task、证据、动作和提交结果，不能让一个任务的失败或命中污染同一轮的其他问题。
3. 模型只负责在服务器已经确定的事实和计划上组织语言；它不能重新决定知识来源、门店地址、是否派人工或发送哪种资源。

本方案不针对入住、咖啡、停车场等具体词做特判，而是修复所有同类问题：

- 任意知识问题命中后都必须经过证据质量判断和答案编排。
- 任意连续消息都按来源片段拆成可追踪任务。
- 任意图片、语音、定位、小程序或外卖地址问题都必须绑定自己的来源和能力。
- 任意失败都必须落到明确的技术失败、澄清、人工任务或已覆盖状态，禁止用“暂时没有处理成功”掩盖链路缺口。

本文件只定义正确的实现方式和验收标准。后续智能体必须先按本文检查真实代码，再分阶段实施。

## 0.3 本次已实施的核心修复

本次在独立分支 `codex/v3-runtime-correctness-fix` 完成以下运行时改动：

- 知识正文不再通过“转接/人工”等普通文字自动生成 `human_handoff`；人工动作只能来自当前租户、门店和知识记录的显式绑定。
- 知识绑定动作只修改对应的 Reply Task，不再把单个 Task 的人工动作提升为整轮 Intent，避免同一轮其他问题被提前终止。
- Validator 的服务端知识投影改为短答案边界：每个证据只保留少量完整句，单组有总长度上限，不再把 FAQ、手册或攻略原文整段发送给客户。
- 已执行 `gofmt`，并通过 `go test -tags dev ./internal/ai/runtime/executor -count=1`。

本次没有修改公开协议、Intent Schema、模型调用次数、计费归因、数据库结构或部署服务器。部署前必须将本分支提交并在目标服务器确认运行版本。

## 1. 范围、原则与非目标

### 1.1 目标

- 同一客户一轮中提出多个问题时，逐题识别、逐题检索、逐题生成、逐题验收。
- 知识库有资料时使用知识资料回答；知识库没有资料时不编造。
- 知识资料很长时只抽取回答客户所需的事实，不发送整篇文章。
- 知识片段里出现“转接”“人工”“联系客服”等说明性文字时，不自动升级人工。
- 图片和语音先完成媒体理解，再进入统一上下文；理解未完成时延迟任务，不提前判定人工。
- 地址、门店事实、资源动作必须使用当前 Store 和当前 Conversation 的权威事实。
- 新消息到达、旧任务重试、进程重启、Outbox 重投都不能造成重复回答或串线。
- 所有阶段都能在 Message、Turn、Task、Job、RunLog、KnowledgeRetrieveLog 和 Outbox 之间关联。

### 1.2 必须保持不变的外部契约

- 不修改公开 HTTP API、WebSocket payload 和前端消息展示协议。
- 不修改已发布 ReplyIntentProfile 的字段和行业 Intent JSON Schema。
- 不改变九槽模型选择、Credential 归属和 Usage/计费归因。
- 不增加第二个模型做事实核验。
- 不增加固定等待作为正确性保证。
- 不把客户明文、Prompt、完整上游响应或密钥写入新的持久化字段。
- 不恢复历史独立 Agent、旧 AIConfig、旧 hook bridge 或旧 fallback 作为运行入口。

### 1.3 非目标

- 本文不通过增加入住、咖啡、停车关键词白名单解决问题。
- 本文不把所有知识库内容复制到客户消息。
- 本文不让模型自行决定是否转人工。
- 本文不把所有失败都改成成功。
- 本文不在当前脏的 codex/customer-audit 工作区改动。

## 2. 已确认的真实问题

以下事实来自服务器当前运行版本和生产链路记录，而不是从最终聊天文本反推。

### 2.1 生产基线

- 服务器：test-2，36.138.68.47:2301
- 运行版本：96e13d39bd81065c8dce727136c6501b30c1f3f6
- Release：20260817-ai-v3-evidence-96e13d3
- agentdesk：active
- Runtime V3：已启用
- 本文对应独立 worktree：/Users/qifeng/Documents/zhixiweibao-v3-correctness

部署、重启或灰度时，以上信息必须重新以服务器 release 目录中的 REVISION 和服务状态确认；
不能仅凭仓库分支名称判断已经运行新代码。

### 2.2 问题 A：知识命中被错误升级为人工

已确认的链路：

1. 客户提出办理入住类问题。
2. KnowledgeRetrieveLog 显示检索成功，有多个命中，最高分达到有效阈值。
3. 某一条相似 FAQ 的正文包含“另一间房办不了入住，请转接”等处理说明。
4. task_knowledge.go 按知识正文是否包含“转接”等文字设置 human_handoff。
5. intent_pipeline.go 又把任务改成 human_complaint_risk，并清空知识回答路径。
6. 后续出现人工提示和“暂时没有处理成功”的双重结果。
7. AgentRunLog 记录了 knowledge action binding promoted to human handoff。

根因不是没检索到，也不是知识库崩溃，而是把“知识文档中的说明性文本”错误解释成
“当前客户要求人工”。这是事实来源边界错误和动作授权错误。

### 2.3 问题 B：知识证据被整段发送

已确认的链路：

1. 附近游玩类问题检索成功，来源为知识库 FAQ，命中内容是一整篇攻略。
2. deterministicGroundedKnowledgeContent 直接把 supporting evidence 投影为客户内容。
3. 函数只做长度截断，没有按客户问题抽取事实、去掉内部说明、控制回答结构。
4. Validator 只证明内容与证据存在重合，未证明内容适合直接发送。
5. 最终客户收到知识片段原文或接近原文，而不是客服口吻的短答案。

根因不是知识库返回过多，也不是 FastGPT 没有工作，而是 Evidence 到 Answer 的中间
编排层缺失，Validator 把“有依据”错误等同于“可直接发送”。

### 2.4 问题 C：连续问题之间会互相污染

当前 V3 已经有 Task、AnswerGroup 和 SourceSpan 结构，但仍需确认以下不变量在所有路径都成立：

- 一个 Task 只能引用自己的 SourceSpan、Knowledge evidence 和 Action refs。
- 一个任务的 human route 不能覆盖同一 Turn 的其他任务。
- 一个任务的 no_hit 不能清空其他任务的 hit。
- 闲聊任务不能因为同一轮存在知识任务而携带酒店知识。
- 地址任务不能使用设施图片或另一门店事实。
- 旧 Job、旧版本或旧 Outbox 不能覆盖新 Turn。

本文把这些不变量变成代码层、JSON 层和数据库层的硬约束，并要求测试逐一证明。

### 2.5 问题 D：失败状态表达不准确

技术失败、知识无命中、需要澄清、业务人工交接、人工派单失败、Outbox 发送失败是不同状态。
如果它们都被转换成“暂时没有处理成功”或直接转人工，运营人员无法判断真正失败位置，
客户也会收到错误语义。

必须拆分：

~~~
knowledge_no_hit
knowledge_unavailable
generation_failed
structured_output_invalid
media_understanding_pending
clarification_required
business_handoff_required
human_dispatch_failed
outbox_delivery_failed
covered_by_previous_reply
~~~

每个状态都要定义客户可见结果、是否重试、是否计费、是否派人工和最终终态。

## 3. 当前链路与目标链路

### 3.1 当前生产链路的职责

~~~
企微入站
  -> Message 持久化
  -> AIReplyJob
  -> Turn 聚合
  -> Normalize
  -> IntentDetect
  -> ContextBuild
  -> Knowledge Retrieve
  -> ReplyPlan
  -> Generate
  -> Validator
  -> Commit
  -> Outbox
  -> 企微发送
~~~

当前链路的问题集中在阶段之间的契约不够严格：

- Intent 产生的任务和后端持久化 Task 可能不完全一致。
- Knowledge 结果既承载证据，又被当作动作来源。
- ReplyPlan 允许缺少明确的 Answerability、FactSource 和 ActionAuthorization。
- Generate 的内容验证偏重“是否命中证据”，轻视“是否是合适的客服答案”。
- Commit 前后虽然有版本校验，但必须把 Task 终态和输出证据一起纳入 CAS。

### 3.2 目标链路

~~~
Inbound
  -> NormalizeMessage
  -> MediaObservation
  -> TurnCoordinator
  -> IntentDetect
  -> TaskLedger
  -> PerTaskContext
  -> ParallelKnowledgeRetrieve
  -> EvidenceBoundary
  -> PerTaskReplyPlan
  -> DeterministicActionPlan
  -> GenerateTextOnly
  -> StrictOutputValidate
  -> PerTaskCoverageValidate
  -> TurnVersionCAS
  -> AtomicCommit
  -> OutboxEligibility
  -> Delivery
  -> DeliveryEvidence
  -> Task/Turn Finalize
~~~

阶段职责必须单向：

| 阶段 | 可以做什么 | 禁止做什么 |
| --- | --- | --- |
| Normalize | 统一消息类型、时间、媒体状态、来源引用 | 判断业务意图 |
| MediaObservation | 产出图片/语音理解观察结果 | 直接生成客户答案或派人工 |
| TurnCoordinator | 聚合连续消息、版本和幂等 | 修改知识结论 |
| IntentDetect | 识别问题单元、能力需求和来源范围 | 决定事实、地址、人工选人 |
| TaskLedger | 持久化每题任务和状态 | 复制客户全文到日志 |
| Knowledge | 查询、重排、判断可支持性 | 把证据正文变成动作 |
| EvidenceBoundary | 清洗、分类、绑定证据 | 添加知识库不存在的事实 |
| ReplyPlan | 定义每题输出目标、依据和限制 | 自由发挥新事实 |
| ActionPlanner | 生成已授权资源/人工动作 | 从自然语言猜动作 |
| Generate | 在计划内组织自然语言 | 决定是否检索或转人工 |
| Validator | 验证结构、覆盖、来源和安全 | 通过不完整或越权输出 |
| Commit | 原子落库和生成 Outbox | 绕过 CAS 直接发消息 |
| Outbox | 只负责投递已批准消息 | 重跑模型或改变答案 |

## 4. 核心领域模型

### 4.1 Turn

Turn 表示客户连续输入形成的一次待处理上下文，不等同于一条消息。

建议字段：

~~~
id
tenant_id
conversation_id
session_no
store_id
store_staff_binding_id
version
status
first_customer_message_id
last_customer_message_id
first_customer_sent_at
last_customer_sent_at
last_committed_version
last_delivered_version
last_committed_request_id
last_delivered_request_id
completed_at
created_at
updated_at
~~~

Turn 状态：

~~~
open
running
waiting_media
waiting_knowledge
waiting_generation
committed
delivered
interrupted
closed
failed
~~~

Turn 版本只由客户新消息、人工接管、Session 变化、会话关闭和明确的系统中断推进。
AI 重试不增加 Turn 版本。

### 4.2 Task

Task 是最小可回答单元。一个客户消息可生成多个 Task，一次客户连续消息也可共同形成多个 Task。

建议字段：

~~~
id
tenant_id
turn_id
task_key
source_message_id
source_span_start
source_span_end
sequence_no
intent
sub_intent
task_type
output_mode
needs_knowledge
status
answer_group_key
knowledge_attempts
generation_attempts
handoff_key
covered_by_task_id
committed_message_id
committed_outbox_id
result_code
next_retry_at
created_at
updated_at
~~~

task_key 必须稳定，由 Turn、来源消息、来源范围、任务类型和顺序经过确定性哈希生成。
同一客户问题重试时 task_key 不变；新问题必须生成新 key。

### 4.3 事实来源

所有可进入客户答案的事实必须带 FactSource：

~~~
store_fact
knowledge_evidence
customer_observation
validated_tool_result
conversation_fact
~~~

以下来源不能直接成为事实：

~~~
model_prior
old_answer_text
unscoped_history
knowledge_document_title_only
knowledge_document_instruction_text
unknown
~~~

一个答案 claim 必须至少绑定一个允许来源；没有来源的 claim 必须被删除、改为澄清，
或转为明确的技术/业务失败。

### 4.4 Action 与 Handoff

人工交接必须由结构化业务条件触发，而不是由知识正文中的词触发。

允许触发人工的来源：

- Intent 明确识别客户要求人工。
- 当前任务需要人工审批、投诉处理、退款、特殊承诺或超出 AI 能力。
- Store policy 明确要求人工，并且该 policy 以结构化规则发布。
- 连续技术失败达到阈值，且产品策略明确要求派单。
- 客户明确拒绝继续 AI 服务。

禁止触发人工的来源：

- 知识片段正文包含“人工”“转接”“联系客服”等词。
- 相似 FAQ 的标题或示例句包含人工处理。
- FastGPT 返回内部 action 字段但没有绑定当前 task 和当前证据。
- Generate 文本自行写出“已为您转人工”。

Handoff 必须有：

~~~
handoff_key
task_key
reason_code
reason_source
customer_visible_message
assignment_policy
created_at
~~~

reason_source 必须指向 Intent、结构化 Store policy、人工动作或技术状态；
不得指向未经分类的 knowledge content。

## 5. 输入规范化和媒体链路

### 5.1 Message Normalize

入站时同时保留：

~~~
message_id
client_msg_id
request_id
conversation_id
session_no
tenant_id
store_id
sender_type
message_type
created_at
sent_at
content_fingerprint
media_ref
payload_schema_version
~~~

CreatedAt 表示平台收到时间，SentAt 表示企微真实发送时间；缺失或明显异常才回退，
并记录 inbound_delay_ms 和 fallback_reason。

规范化不能把不同消息类型都转成一段文本。必须保留：

~~~
text
image
voice
file
location
mini_program
event
system
~~~

### 5.2 图片

图片处理分成三个阶段：

1. 下载并验证媒体引用。
2. 视觉模型或媒体解析生成 Observation。
3. 将 Observation 绑定到引用该图片的 Task。

Observation 示例：

~~~json
{
  "schemaVersion": "media_observation.v1",
  "sourceMessageId": 1701,
  "mediaType": "image",
  "status": "understood",
  "observations": [
    {
      "key": "visible_text",
      "value": "无法确认",
      "confidence": 0.0,
      "source": "vision"
    }
  ],
  "usableForAnswer": false,
  "expiresAt": "2026-08-17T00:00:00Z"
}
~~~

图片没有识别结果时，不能直接把任务设为 human_handoff。应返回：

- 仍可根据文字问题回答：继续处理文字任务。
- 图片是必要输入：clarification，要求客户重新发送清晰图片。
- 图片涉及人工审核：建立业务 handoff。

### 5.3 语音

语音链路必须区分：

~~~
audio_received
transcription_pending
transcribed
transcription_failed
~~~

转写结果必须保存最小必要的引用和摘要，不把完整音频或原始 ASR 响应写入 TraceData。
ASR 失败时不要把整轮问题丢失，也不要把失败伪装成客户要求人工：

- 同轮还有可处理文字：先处理文字。
- 只有语音且无法转写：给出重试/文字输入澄清。
- Store policy 要求人工：按结构化 policy 派单。

语音转写文本进入 IntentDetect 时必须带 sourceMessageId 和 rune span；不能把整段转写
复制到每一个 Task。

## 6. IntentDetect 设计

### 6.1 输入边界

IntentDetect 可以看到：

- 当前 Turn 的客户消息和已完成媒体 Observation。
- 当前 Session 的必要近期上下文。
- 结构化 Store Facts 的摘要。
- 尚未完成 Task 的键、状态和已回答摘要标签。
- 发布中的 Industry Intent Profile。

IntentDetect 不可以看到或使用：

- 未绑定当前 Conversation 的其他租户事实。
- 另一门店的地址、资源或旧答案。
- 未经摘要的长历史知识正文。
- Generate 的待发送文本作为事实。
- 从知识正文推断的人工动作。

### 6.2 输出职责

Intent 输出的是任务和能力需求，不是最终答案：

~~~json
{
  "schemaVersion": "intent_tasks.v3",
  "turnVersion": 12,
  "utteranceCoverage": [
    {
      "sourceRef": "m1701",
      "coveredTaskKeys": ["task_1", "task_2"]
    }
  ],
  "tasks": [
    {
      "taskKey": "task_1",
      "sequence": 1,
      "sourceRefs": ["m1701"],
      "sourceSpans": [
        {
          "sourceRef": "m1701",
          "start": 0,
          "end": 6,
          "quote": "客户问题片段"
        }
      ],
      "intent": "hotel_info",
      "subIntent": "service_information",
      "needsKnowledge": true,
      "requestedCapabilities": ["knowledge"],
      "dialogueAct": "question",
      "requiresHuman": false
    }
  ]
}
~~~

### 6.3 Intent 验证不变量

- schemaVersion 必须精确匹配。
- taskKey 在本 Turn 内唯一。
- sequence 连续且按来源顺序。
- 每个 Task 有至少一个 sourceRef 和有效 SourceSpan。
- 所有客户语义片段都被 coverage 覆盖。
- requiresHuman=true 必须有明确 reasonCode 和 reasonSource。
- requestedCapabilities 只能来自发布的能力枚举。
- Intent 不得直接输出知识事实、地址、图片 URL 或人工客服 ID。
- 不能因为任务数量多而把后续问题丢弃；超过批次上限必须续批。

### 6.4 Intent 失败策略

Intent 严格 JSON 解析失败时，只允许一次协议修复：

- 修复请求只带错误 code/path 和结构化任务要求。
- 不把完整模型原文、客户全文或知识正文再次拼入 Prompt。
- 修复后仍失败：记录 structured_output_invalid，创建澄清或技术失败结果；
  不默认转人工，不静默完成。

确定性 HTTP 400（如上游拒绝 schema）不能重复发送相同请求。
网络、超时、429、5xx 才由九槽客户端按配置重试。

## 7. 知识库链路

### 7.1 每题独立检索

对每个 needsKnowledge=true 的 Task 单独执行：

~~~
Task query
  -> tenant/store scoped retrieval
  -> optional rerank
  -> answerability
  -> evidence normalization
  -> task evidence bundle
~~~

并发上限建议为 4；每题独立记录 hit、no_hit、failed。
一个 Task 的失败不得清空或改变其他 Task 的结果。

查询必须至少带：

~~~
tenant_id
store_id
knowledge_base_id
industry_profile_revision
task_key
query_fingerprint
~~~

严禁跨 Tenant、Store、Conversation 或 Profile revision 复用知识答案。
可以复用同一次请求内完全相同的查询指纹，但复用结果仍必须重新绑定当前 Task。

### 7.2 Evidence 三层结构

知识返回结果不能直接进入 Generate。先转换为：

~~~
RawHit
  -> NormalizedEvidence
  -> AnswerableFact
~~~

NormalizedEvidence 只包含当前任务必要的最小片段、来源标识和质量字段：

~~~json
{
  "evidenceRef": "ev_1",
  "taskKey": "task_1",
  "sourceRef": "kb_42",
  "topicMatch": "exact",
  "answerability": "supporting",
  "claimType": "store_policy",
  "facts": [
    {
      "factKey": "fact_1",
      "text": "当前门店已发布的事实",
      "source": "knowledge_evidence",
      "confidence": 0.91
    }
  ],
  "excludedTextReasons": [
    "internal_instruction",
    "historical_example",
    "unrelated_section",
    "handoff_instruction_not_current_policy"
  ],
  "contentFingerprint": "sha256:..."
}
~~~

### 7.3 知识文本中的人工词处理

知识正文必须先分段分类：

~~~
fact
procedure
example
historical_case
internal_instruction
handoff_policy
warning
unrelated
~~~

只有结构化、发布有效、scope 与当前 Store 匹配的 handoff_policy 才能产生人工动作。
正文中出现“转人工”只作为普通文本或内部说明，不得改变 Task route。

伪代码：

~~~go
func deriveHandoff(task TaskView, evidence EvidenceBundle, intent IntentTask) HandoffDecision {
    if intent.RequiresHuman && intent.HumanReason != "" {
        return BusinessHandoff(intent.HumanReason, "intent")
    }
    if policy := evidence.StructuredPolicyFor(task); policy.Active && policy.RequiresHuman {
        return BusinessHandoff(policy.ReasonCode, "store_policy")
    }
    return NoHandoff()
}
~~~

### 7.4 hit、no_hit、failed

#### hit

有 exact topic match、supporting answerability 和至少一个可引用事实：

- 生成知识回答。
- Answer 必须只使用当前 Task 的 facts。
- 没有必要事实的命中不能算 hit。

#### no_hit

检索正常但没有可靠事实：

- 不编造答案。
- 可以说明“当前资料未写明”，并最多追问一个关键澄清点。
- 如果该问题要求实时或人工确认，则创建对应业务人工任务。

#### failed

检索、Embedding、Rerank 或 FastGPT 网关技术失败：

- Gateway 自己负责初次调用加两次重试。
- 任务层不重复放大网络调用。
- 已成功的其他 Task 继续处理。
- 耗尽后产生明确技术失败状态，按产品策略澄清或转人工；
  不能把技术失败伪装成知识无命中。

### 7.5 知识库长文答案策略

禁止使用以下逻辑：

~~~
evidence.Content -> trim(maxLength) -> customerReply
~~~

必须改成：

~~~
evidence.Content
  -> section extraction
  -> claim selection
  -> answer outline
  -> Generate
  -> claim/source validation
~~~

默认回答模板：

1. 先给直接结论。
2. 再给最多 3 个客户真正需要的要点。
3. 需要行动时给下一步。
4. 不把知识标题、内部编号、整篇攻略、历史案例和无关段落发给客户。

推荐约束：

~~~
普通问题 1-4 句；
列表问题最多 5 项；
每项最多 2 句；
除非客户要求详细攻略，否则不超过 180 个中文字符；
客户明确要求完整攻略时，按段落分页或分多条发送，仍不发送内部字段。
~~~

字符上限是输出体验约束，不是安全校验的唯一依据。超过上限必须按内容结构压缩，
不能简单截断造成半句话或丢失关键限定。

## 8. ReplyPlan 设计

### 8.1 ReplyPlan 是服务器事实计划

ReplyPlan 必须由后端根据 Intent、Evidence、Store Facts 和 ActionLedger 构造，
而不是完全由模型自由生成。

~~~json
{
  "schemaVersion": "reply_plan.v4",
  "turnId": 448,
  "turnVersion": 12,
  "tasks": [
    {
      "taskKey": "task_1",
      "sequence": 1,
      "taskType": "knowledge",
      "outputMode": "text",
      "answerGroupKey": "group_1",
      "evidenceRefs": ["ev_1"],
      "requiredFacts": ["fact_1"],
      "allowedClaims": ["fact_1"],
      "forbiddenClaims": ["unverified_price", "other_store_address"],
      "handoff": {
        "required": false,
        "reasonCode": "",
        "source": ""
      },
      "status": "ready"
    }
  ],
  "groups": [
    {
      "groupKey": "group_1",
      "taskKeys": ["task_1"],
      "outputMode": "text",
      "required": true,
      "maxParts": 1
    }
  ]
}
~~~

### 8.2 计划生成规则

- Task 只能引用自己的 EvidenceRefs。
- requiredFacts 为空时，知识回答不得生成确定性事实。
- handoff.required=true 时必须有结构化 reasonCode/source。
- resource task 不得通过普通文本替代资源发送。
- conversation task 不得引用旧业务知识，除非计划明确绑定当前证据。
- 所有 groups 必须覆盖全部可回答 Task。
- 未准备好的 Task 不能被标记为 committed。

### 8.3 AnswerGroup

AnswerGroup 只是输出分组，不是知识范围，也不是权限范围。

同组条件：

- 任务顺序相邻。
- 输出模式相同。
- 事实来源 scope 相同。
- 语气和长度可以自然合并。
- 合并后每个 Task 仍可从 parts 反向追踪。

不同门店、不同地址、不同知识 scope、不同人工动作必须拆组。
最多三条文本消息不是强制把所有问题合并成一条；应优先可读性和任务隔离。

## 9. Generate 设计

### 9.1 Generate 的唯一职责

Generate 只把已经批准的 ReplyPlan 组织成客服口吻文本或结构化资源说明。
它不能：

- 自行追加知识事实。
- 将另一 Task 的证据用于当前 Task。
- 把知识正文原样复制。
- 识别出人工就自发派单。
- 改变地址、门店名称、营业时间或资源类型。
- 把未理解图片当作已理解。

### 9.2 Prompt 来源分层

Prompt 必须在代码中以明确段落组装，且每段有来源标签：

~~~
[SYSTEM_POLICY]
  安全、来源边界、输出职责、禁止事项

[INDUSTRY_PROFILE]
  已发布 ReplyIntentProfile 的分类规则

[PERSONA]
  当前有效 WxWorkProtocolInstance.PersonaPrompt
  只影响语气，不改变事实和路由

[STORE_FACTS]
  当前 Store 的结构化事实

[CURRENT_TASKS]
  当前未完成 Task、SourceSpan、Task type、OutputMode

[ADJACENT_CONTEXT]
  仅必要的相邻问答摘要，不作为新事实

[KNOWLEDGE_EVIDENCE]
  当前 Task 的最小 AnswerableFact

[RUNTIME_STATE]
  TurnVersion、已提交 Task 标签、允许动作和失败状态
~~~

每个来源都必须在编译器中有独立类型或字段，禁止把全部字符串拼成无标签长 Prompt。

### 9.3 Generate 输入示例

~~~json
{
  "schemaVersion": "generate_task_input.v2",
  "turnVersion": 12,
  "groups": [
    {
      "groupKey": "group_1",
      "taskKeys": ["task_1"],
      "tasks": [
        {
          "taskKey": "task_1",
          "sourceSpan": "当前问题片段",
          "intent": "hotel_info",
          "outputMode": "text",
          "allowedFacts": [
            {
              "factRef": "fact_1",
              "content": "当前门店已确认事实"
            }
          ],
          "forbiddenClaims": [
            "不得引用其他门店地址",
            "不得承诺知识中没有的服务"
          ]
        }
      ]
    }
  ]
}
~~~

### 9.4 Generate 输出

单个文本 Task 可以使用普通文本内部路径；两个及以上文本 Task 使用严格结构化输出：

~~~json
{
  "schemaVersion": "reply_output.v3",
  "parts": [
    {
      "groupKey": "group_1",
      "taskKeys": ["task_1"],
      "content": "基于当前资料整理后的简短客服回答。"
    }
  ]
}
~~~

输出要求：

- groupKey 必须逐字复制服务器输入。
- taskKeys 必须属于该 group。
- 每个 required group 恰好覆盖一次。
- content 只能表达 allowedFacts 和必要的对话连接词。
- 不允许把原始 evidence JSON、标题、内部备注或检索日志发出。
- 空 content、未知 group、重复 group、缺任务、跨组 task 都拒绝。

非法输出只允许一次缺失任务定向修复，修复请求不带完整原文；
修复仍失败时按结构化失败收敛，不退回未经验证的自由文本。

## 10. Validator 设计

Validator 必须分成五层，不能只做 JSON parse 或字符串包含检查。

### 10.1 SchemaValidator

- schemaVersion、类型、必填字段、枚举、数组非 nil。
- 拒绝未知字段或根据协议允许的未知字段策略处理。
- 拒绝空 parts 作为已完成回复。

### 10.2 TaskCoverageValidator

构造集合：

~~~
requiredTaskKeys
outputTaskKeys
coveredTaskKeys
~~~

必须满足：

~~~
requiredTaskKeys == coveredTaskKeys
outputTaskKeys ⊆ requiredTaskKeys
每个 taskKey 只出现一次
~~~

业务人工、澄清、资源任务不要求文本 part，但必须有对应状态证据。

### 10.3 EvidenceBoundaryValidator

对每个句子或 claim 检查：

- 是否能映射到当前 Task 的 allowedFacts。
- 是否越过 Store、Tenant、Session 或 KnowledgeBase scope。
- 是否把 evidence 的 instruction/example 当成事实。
- 是否出现未经授权的地址、价格、时间、服务承诺。
- 是否把旧回答文本当成当前事实。

无法映射的句子必须删除或让任务进入澄清，不可通过“模型大概说对了”放行。

### 10.4 AnswerQualityValidator

检查：

- 结论是否在前。
- 是否出现原始知识库格式、内部标题、编号、Prompt 或日志。
- 是否超出长度和段落约束。
- 是否有重复句、无关大段引用、半截截断。
- 是否与客户当前问题匹配。

该层不做语义臆测；只按结构、事实绑定和有限规则判断。

### 10.5 ActionAndCommitValidator

- 文本中不能宣称未提交的资源动作已完成。
- resource task 必须有 ActionLedger。
- handoff task 必须有稳定 handoff_key。
- TurnVersion、TaskVersion、租约、Session、Route、Binding 必须一致。
- Commit 前不能存在相同稳定 ClientMsgID 的已提交消息。

## 11. 连续消息、标签和幂等

### 11.1 处理标签

用户提出的“标签”应落成持久化任务证据，而不是只写进 Prompt：

~~~
task_key
source_message_id
turn_id
turn_version
status
covered_by_task_id
committed_message_id
committed_outbox_id
~~~

Prompt 可以携带这些标签的摘要，但正确性必须由数据库唯一键和 CAS 保证。

### 11.2 新消息加入当前 Turn

- AI 尚未真正发送时，新消息升级 TurnVersion，旧 Job 失去提交资格。
- 新消息与已回答问题相同，建立 covered 关系，不重复调用模型。
- 新消息是新问题，建立新 Task，只处理新增问题。
- 客户看到 AI 回复后提出新问题，创建新 Turn 或新版本，不能错误去重。
- System、欢迎语、自动化资源不结束客户问题 Turn。
- 人工消息、Session 变化、会话关闭和 AI 关闭中断剩余任务。

### 11.3 去重原则

只使用确定性去重：

- 文本 Unicode、空格和尾部标点标准化后 hash。
- 图片使用媒体指纹。
- 定位和小程序使用资源类型加资源指纹。
- 不使用模糊语义相似度直接吞掉客户消息。

重复判断必须限定在 Tenant、Conversation、Session、Turn 窗口和消息类型内。

### 11.4 任务批次

单次最多处理 6 个未完成 Task；超过时创建后续 Job。
后续 Job 必须引用同一个 Turn 和未完成 Task，不需要客户再次发送消息。
批次边界不能丢失顺序，也不能重复已经 committed、delivered、covered 的 Task。

## 12. Commit、CAS 和 Outbox

### 12.1 Commit 原子事务

一个 Commit 事务内完成：

1. 锁定 Turn。
2. 校验 TurnVersion、Session、Tenant、Store、Binding、Job lease。
3. 锁定待提交 Task。
4. 校验所有输出 group、fact、action 和 handoff 证据。
5. 创建 AI Message。
6. 创建 Outbox。
7. 更新 Task 终态和提交证据。
8. 更新 Turn committed version 和 request ID。
9. 更新会话计数。

事务成功后才发布 WebSocket；WebSocket 失败不能回滚数据库提交。

### 12.2 Outbox 门禁

Outbox Claim 前再次读取 Message、Task、Turn：

- 版本失效且尚未发出：cancelled_stale_turn。
- Task 已 covered 或 superseded：取消对应未发送 Outbox。
- 已进入上游发送过程：不强行中断；结果落库后由最新 Turn 决定是否补答。
- 发送失败：只重试投递，不重新执行 Intent、Knowledge 或 Generate。
- 同一 ClientMsgID 已有发送证据：幂等收敛。

### 12.3 进程退出

- Commit 前退出：无 AI Message 和 Outbox，Job 可恢复。
- Commit 后、WebSocket 前退出：数据库证据存在，恢复时只补推送。
- Outbox 发送中退出：根据上游 request ID 和本地发送证据幂等查询或收敛。
- 租约丢失：当前 Worker 不能再 Commit，必须取消 Context。

## 13. 失败分类和客户结果

| 内部结果码 | 说明 | 是否重跑模型 | 客户结果 | 是否人工 |
| --- | --- | --- | --- | --- |
| knowledge_hit | 有可靠资料 | 否 | 基于资料简答 | 否 |
| knowledge_no_hit | 检索成功但无资料 | 否 | 明确资料缺失或澄清 | 按业务策略 |
| knowledge_unavailable | 技术检索失败 | 否，等 Gateway 重试 | 技术提示或稍后重试 | 仅业务要求时 |
| structured_output_invalid | JSON 或覆盖失败 | 仅一次定向修复 | 明确失败或澄清 | 否 |
| generation_failed | Generate 技术失败 | 按槽内重试 | 技术提示 | 默认否 |
| clarification_required | 缺必要信息 | 否 | 只问一个关键问题 | 否 |
| business_handoff_required | 业务确需人工 | 否 | 人工接管提示 | 是 |
| media_understanding_pending | 图片或语音尚未准备 | 否 | 延迟，不重复回复 | 否 |
| media_understanding_failed | 媒体无法理解 | 否 | 请重发或转文字 | 按策略 |
| covered_by_previous_reply | 已有相同有效回答 | 否 | 不再发重复答案 | 否 |
| outbox_delivery_failed | 已生成但投递失败 | 否 | Outbox 重试 | 否 |
| scope_conflict | 范围或租户不一致 | 否 | 技术失败 | 否 |

特别禁止：

- generation_failed -> customer_requested_human
- knowledge 正文包含人工 -> business_handoff_required
- empty_output -> completed
- outbox_failed -> rerun_model

## 14. 代码实施清单

以下清单按层次列出，不要求一次性大改；每一步都必须保持可编译和可回滚。

### 14.1 Models / Enums

建议检查或补齐：

~~~
internal/models/models.go
internal/models/ai_reply_turn.go
internal/models/ai_reply_turn_task.go
internal/models/ai_reply_turn_action.go
internal/models/ai_reply_job.go
internal/models/channel_message_outbox.go
internal/pkg/enums/ai_reply.go
~~~

要求：

- 字段和索引兼容 SQLite/MySQL。
- Task 唯一索引：tenant_id + turn_id + task_key。
- 待处理索引：status + next_retry_at。
- Outbox 唯一约束保留 Tenant、Conversation、RequestID、ClientMsgID 语义。
- 新字段 nullable 或向后兼容，旧 release 启动不因新字段失败。

### 14.2 Repositories

建议新增或整理：

~~~
internal/repositories/ai_reply_turn_repository.go
internal/repositories/ai_reply_turn_task_repository.go
internal/repositories/ai_reply_turn_action_repository.go
internal/repositories/ai_reply_job_repository.go
internal/repositories/channel_message_outbox_repository.go
~~~

Repository 只负责：

- 带租户和会话范围的查询。
- FOR UPDATE 锁和 CAS 条件。
- 唯一键冲突的幂等读取。
- 按状态、重试时间扫描。

Repository 不负责：

- 判断是否该转人工。
- 拼装 Prompt。
- 生成客户文案。
- 调用模型或 FastGPT。

### 14.3 Services

建议拆分职责：

~~~
internal/services/ai_reply_turn_service.go
internal/services/ai_reply_task_service.go
internal/services/ai_reply_scope_service.go
internal/services/ai_reply_commit_service.go
internal/services/ai_reply_outbox_service.go
internal/services/ai_reply_handoff_service.go
~~~

Service 负责事务、状态机、幂等和跨表编排。
已有服务能承载时优先扩展，不创建同义的第二套 Turn、Job 或 Handoff 模型。

### 14.4 Runtime executor

重点文件：

~~~
internal/ai/runtime/executor/task_knowledge.go
internal/ai/runtime/executor/intent_pipeline.go
internal/ai/runtime/executor/reply_plan_v2.go
internal/ai/runtime/executor/reply_plan_v4_builder.go
internal/ai/runtime/executor/reply_composition_plan.go
internal/ai/runtime/executor/reply_output_v3.go
internal/ai/runtime/executor/validator_v3.go
internal/ai/runtime/executor/fact_source_boundary_validator.go
internal/ai/runtime/executor/commit_invariant_validator.go
internal/ai/runtime/executor/runtime_v3_plan.go
internal/ai/runtime/executor/intent_tasks_v3.go
~~~

实施顺序：

1. 先把 Evidence、Action、Handoff 三种类型分开。
2. 再修复每 Task 的计划和事实绑定。
3. 再替换长文直接投影逻辑。
4. 再加强 Generate 输入和输出验证。
5. 最后接入 Commit 和 Outbox 的 Task 终态。

禁止先从 Prompt 文本下手，否则无法证明状态和事实边界。

### 14.5 当前真实代码落点与改造动作

以下不是假设文件名，而是基于本基线已经存在的代码落点。后续实现必须先读取函数当前
调用关系，再决定是修改、抽取还是删除逻辑。

| 当前落点 | 当前职责 | 必须改成 |
| --- | --- | --- |
| internal/ai/runtime/executor/task_knowledge.go: retrieveRuntimeTaskKnowledge | 批量检索并汇总 Task 知识结果 | 每个 Task 保留独立 status、evidence、answerability 和 failureClass |
| internal/ai/runtime/executor/task_knowledge.go: buildRuntimeEvidenceBundle | 把检索结果整理为 EvidenceBundle | 只输出当前 Task 可用的最小 Evidence，不把原始正文当答案 |
| internal/ai/runtime/executor/task_knowledge.go: knowledgeContentRequiresHandoff | 从知识正文判断人工动作 | 删除正文词触发；只接受 Intent 或结构化 Store policy 的授权 |
| internal/ai/runtime/executor/task_knowledge.go: runtimeKnowledgeAnswerGroup | 根据知识结果生成分组 | 分组只由后端 AnswerGroup 规则决定，不承载 Handoff 语义 |
| internal/ai/runtime/executor/intent_pipeline.go: applyKnowledgeActionBindings | 把知识结果绑定到任务动作 | 仅绑定 evidence/action ledger；禁止把未分类知识文本提升为 human route |
| internal/ai/runtime/executor/intent_pipeline.go: markIntentAsKnowledgeHandoff | 当前知识动作升级人工 | 只处理已经有结构化 reason source 的业务人工任务 |
| internal/ai/runtime/executor/intent_pipeline.go: buildReplyTaskPlans | 从 Intent 生成任务计划 | 确保每个任务的 source span、task type、knowledge scope 和 output mode 独立 |
| internal/ai/runtime/executor/reply_plan_v4_builder.go: BuildReplyPlanV4 | 生成 ReplyPlan V4 | 为每个 Task 生成 allowedFacts、requiredFacts、forbiddenClaims 和 Handoff source |
| internal/ai/runtime/executor/reply_composition_plan.go: BuildFinalAnswerGroups | 合并相邻任务为 AnswerGroup | 只按顺序、输出模式和事实 scope 合并，不跨知识或人工边界 |
| internal/ai/runtime/executor/validator_v3.go: deterministicGroundedKnowledgeContent | 用 supporting evidence 生成兜底文本 | 替换为 Evidence 到 Answer 的结构化摘要；禁止正文 trim 后直接发送 |
| internal/ai/runtime/executor/validator_v3.go: groundedEvidenceAnswerForTask | 将证据投影到任务答案 | 只允许已筛选 AnswerableFact，并执行 claim/source 绑定 |
| internal/ai/runtime/executor/validator_v3.go: boundedGroundedAnswer | 限制答案长度 | 改为按句子和要点压缩，不能在任意 rune 位置截断 |
| internal/ai/runtime/executor/validator_v3.go: validateV3KnowledgeQuality | 检查知识质量 | 增加 topic、answerability、claim type、evidence scope 和长文结构检查 |
| internal/ai/runtime/executor/validator_v3.go: validateV3ActionClaims | 检查动作宣称 | 检查文本不能宣称未提交的人工、定位、图片或小程序动作 |
| internal/ai/runtime/executor/reply_output_v3.go: applyRuntimeReplyOutputV3 | 解析并应用 Generate 结果 | 严格检查 group、task coverage、content scope，失败不得退回自由文本 |
| internal/services/ai_reply_turn_task_service.go: ClaimBatchDB | 领取待处理任务 | 领取时绑定 TurnVersion、JobID 和稳定 TaskKey，旧版本立即失去资格 |
| internal/services/ai_reply_turn_task_service.go: MarkKnowledgeResultsDB | 写入知识阶段结果 | 逐 Task 写入，不能用整轮结果覆盖其他 Task |
| internal/services/ai_reply_turn_task_service.go: MarkCommittedMessagesDB | 写入提交证据 | 同时写 Message、Outbox、Task 终态和去重证据 |
| internal/services/ai_reply_turn_service.go: ValidateCommitDB | Commit 前 CAS | 增加 Task 集合、lease、Route、Binding 和已覆盖检查 |
| internal/services/ai_reply_turn_service.go: MarkCommittedDB | 标记 Turn 已提交 | 只在原子事务成功后更新 committed version |
| internal/services/ai_reply_job_service.go: executeClaimed | 执行一轮 Runtime | 只编排阶段，不把技术失败转换为客户人工诉求 |
| internal/services/ai_reply_job_service.go: finishClaimed | 处理 Runtime 结果 | 依据 result code 分离 retry、technical failure、business handoff 和 partial success |
| internal/services/ai_reply_job_service.go: dispatchControlledFailure | 失败派单 | 只对有业务授权的 Handoff 派单，不能因模型或知识技术失败默认派单 |
| internal/services/ai_reply_job_service.go: validateCompletionEvidence | 校验完成证据 | 要求每个完成 Task 有 Message、Outbox 或 Interrupt 证据 |
| internal/services/channel_message_outbox_service.go | 投递企微消息 | Claim 和发送前读取 Task/Turn 版本，过期消息取消，失败只重试投递 |

### 14.6 推荐提交拆分

为方便 review、回滚和并行分支合并，建议拆成以下独立提交；每个提交都不能混入前端、
客服派单或无关模型配置：

1. contract-and-enums：补齐内部状态、结果码和兼容字段。
2. evidence-boundary：分离 Knowledge Evidence、AnswerableFact、Action 和 Handoff。
3. task-isolated-plan：完善 Task 级 ReplyPlan、AnswerGroup 和 coverage。
4. answer-composition：替换长文直接投影，增加短答结构化编排。
5. strict-validator：加强 reply_output.v3、事实来源和动作声明验证。
6. commit-outbox-gate：把 Task 终态、Turn CAS 和 Outbox 门禁接成一个事务协议。
7. replay-and-observability：补回放、并发、失败注入和指标。

共享高风险文件包括 models、enums、AIReplyJob、MessageService、Outbox、ReplyPlan 和
Runtime contracts。修改这些文件前必须 fetch origin，并检查 codex/customer-audit、
codex/ai-billing 以及其他活跃分支的同文件变化；不允许直接覆盖并行分支修改。

## 15. JSON Schema 设计要求

### 15.1 Schema 版本

保留并严格区分：

~~~
intent_tasks.v3
reply_plan.v4
generate_task_input.v2
reply_output.v3
media_observation.v1
~~~

Schema 版本只表示协议版本，不表示模型版本或部署版本。

### 15.2 原始类型显式声明

发送到 NewAPI 或 DeepSeek Responses 的 Schema 每个可推断原始节点都必须有显式 type，
包括 const、enum、数组 items 和对象 properties。
这只是上游兼容性变换，不得删掉本地语义约束。

### 15.3 Null 与空数组

状态数组统一输出空数组而不是 null：

~~~json
{
  "tasks": [],
  "parts": [],
  "evidenceRefs": [],
  "actionRefs": [],
  "forbiddenClaims": []
}
~~~

null 只在字段语义明确允许未知或未生成时使用。

### 15.4 JSON 失败修复

协议修复输入只包含：

~~~json
{
  "errorCode": "missing_required_group",
  "paths": ["parts[0].groupKey"],
  "requiredGroupKeys": ["group_1"],
  "allowedTaskKeys": ["task_1"],
  "instruction": "只修复协议错误，不改变任务、事实和路由"
}
~~~

不得携带完整上次输出或整段知识正文。

## 16. 测试、监控、灰度与回滚

### 16.1 必测场景

- 知识正文含人工词但不触发 handoff。
- 结构化 Store policy 明确要求人工时触发 handoff。
- 长知识内容只输出摘要事实，不输出完整原文。
- evidence 与 answer claim 跨 Task 时拒绝。
- no_hit、failed、hit 三种结果互不污染。
- 闲聊任务不带酒店知识。
- 地址任务不使用其他门店地址。
- 图片未理解时等待或澄清，不直接转人工。
- 语音转写失败时不丢失同轮文字问题。
- unknown group、missing task、duplicate task、cross group task 全部拒绝。
- 两个 Worker 并发时只有一个 Commit。
- Commit 后进程退出不重复发送。
- Outbox failed 只重试发送。

### 16.2 性能目标

- 正常链路不增加固定等待。
- 知识查询并行，并发上限 4。
- 新增本地验证和索引查询 p95 不超过 50ms。
- 正常单题调用次数不增加。
- 只有协议错误才产生定向修复调用。
- Outbox 重试不重新调用模型。

### 16.3 指标

至少记录：

~~~
ai_reply_task_total{type,status}
ai_reply_task_failure_total{stage,result_code}
ai_reply_handoff_total{source}
ai_reply_knowledge_hit_total
ai_reply_knowledge_no_hit_total
ai_reply_knowledge_failed_total
ai_reply_output_rejected_total{reason}
ai_reply_covered_duplicate_total
ai_reply_outbox_cancelled_stale_total
ai_reply_stage_duration_ms{stage}
ai_reply_model_call_total{slot,reason}
~~~

关键告警：

- knowledge_hit 但 answer_parts=0。
- handoff_source=knowledge_content。
- committed task 没有 message、outbox 或 interrupt 证据。
- task coverage 缺失。
- 同一稳定 ClientMsgID 多次发送。
- V3 开关开启但运行 revision 不符合预期。

### 16.4 灰度和回滚

实施顺序：

1. 独立 worktree 核对最新主线和活跃分支同文件变化。
2. 先完成 Evidence、Action、Handoff 分离。
3. 完成 per-task Knowledge 和 ReplyPlan。
4. 完成 Generate、Validator、Commit、Outbox。
5. 补齐回放和并发故障测试。
6. 提交独立 commit，review 后合并。
7. 写入唯一 REVISION，先灰度单一门店或员工绑定。
8. 观察 30 分钟以上，再决定是否扩大。

部署后必须核对：

~~~
仓库 commit SHA
镜像 digest
release REVISION
服务状态
V3 运行开关
Model Profile revision
NewAPI 和 FastGPT BaseURL
AutoMigrate 结果
真实链路 RunLog
~~~

回滚只关闭灰度并切回上一已验证 release；保留新增 nullable 字段和表，不删除运行证据。

## 17. 实施验收标准

只有满足以下全部条件，才可以说 V3 完整实现：

### 正确性

- 任意知识问题命中时，不因知识正文中的人工词自动派单。
- 任意知识问题只输出与当前问题相关的最小事实。
- 任意无命中问题不编造。
- 任意连续多问题都能逐题得到明确终态。
- 一个任务失败不影响同轮其他任务。
- 图片、语音、资源动作均有独立来源和状态。
- 地址、门店和资源不能跨 scope。

### 一致性

- 每个 committed 或 delivered Task 都有 Message、Outbox 或业务 Interrupt 证据。
- 每个 handoff 都有结构化 reason source。
- 每个 AnswerGroup 都能反向找到 Task 和 evidence。
- 旧 Job、旧 TurnVersion 和重复 Outbox 无法覆盖新版本。

### 可运维

- 能从一个 TaskKey 定位完整链路。
- 能区分 knowledge_no_hit、knowledge_failed、generation_failed 和 handoff。
- 日志不泄露密钥、Prompt 或客户全文。
- 灰度可关闭，旧版本可回滚。

## 18. 给后续智能体的执行指令

后续智能体接手时必须：

1. 读取本文件和 docs/design/reply-runtime-engine.md。
2. 检查当前 worktree、分支、HEAD、origin/main 和活跃并行分支。
3. 用 rg 找到本文列出的真实函数，确认代码是否与文档一致。
4. 先写失败测试证明三个根因：知识正文人工词误派单、长 evidence 原样发送、
   每 Task 的 evidence/action/coverage 未隔离。
5. 再实现最小领域修复，避免新建平行模型。
6. 每个阶段完成后记录文件、字段、索引、状态和回滚边界。
7. 执行 Go 格式化、单测、竞态测试和全量测试。
8. 只在用户明确授权后部署；部署后核对 REVISION、服务状态和真实链路证据。
9. 不在 codex/customer-audit 脏工作区直接修改。
10. 不把某个客户问题写成关键词特例；规则必须按 task type、fact source、scope、
    evidence quality 和 action authorization 通用实现。

## 19. 最终判断

按本文实施后，目标不是让某几句测试话术答对，而是让每一条回复都能回答四个问题：

1. 客户这次到底提出了哪些独立 Task？
2. 每个 Task 使用了哪个被授权的事实来源？
3. 为什么生成了这段话、资源或人工动作？
4. 这次提交和发送是否有不可重复、可恢复的证据？

只要任一答案无法从数据库和结构化 RunLog 中还原，就不能视为链路完成。

# AgentDesk 开发续接说明

本文件用于换电脑后继续当前 Codex 会话和 AgentDesk 开发。

## 代码与运行状态

- 远端仓库：`git@github.com:520skyincloud/agentdesk.git`
- 最新主分支提交：`083937b` 起包含完整迁移备份；本文件之后的提交会包含 Codex 会话备份。
- 当前主要分支：`main` 与 `wxwork-protocol-agentdesk` 保持同步。
- 完整运行备份：`backups/migration-20260630-090044/`
- 恢复脚本：`scripts/restore_full_backup.sh`

新电脑恢复项目：

```bash
git clone git@github.com:520skyincloud/agentdesk.git
cd agentdesk
chmod +x scripts/restore_full_backup.sh
./scripts/restore_full_backup.sh backups/migration-20260630-090044
```

## Codex 会话备份

当前长会话的原始 Codex rollout 文件已备份到：

- `backups/codex-session/rollout-agent-desk-main-019e81e2-a5e3-7c70-9c68-0dbfb36dd257.jsonl.gz.part-*`
- `backups/codex-session/thread-019e81e2-a5e3-7c70-9c68-0dbfb36dd257.json`

因为原始会话文件较大，已按 50MB 切分，避免超过 GitHub 单文件限制。

新电脑重组会话备份：

```bash
chmod +x scripts/restore_codex_session_backup.sh
./scripts/restore_codex_session_backup.sh
```

脚本会生成：

- `.codex-session-restore/rollout-agent-desk-main-019e81e2-a5e3-7c70-9c68-0dbfb36dd257.jsonl`
- `.codex-session-restore/thread-019e81e2-a5e3-7c70-9c68-0dbfb36dd257.json`

如果新电脑 Codex 客户端支持导入本地 rollout/thread 文件，可以导入上述 JSONL。若不支持，开新 Codex 线程后把本文件和 `.codex-session-restore` 里的 JSONL 作为上下文即可继续开发。

## 最近关键改动

- 企微员工号协议唯一依据固定为 `https://wework.apifox.cn/llms.txt`。
- CLI / 企业微信客服号作为产品入口已废弃，新主链路是企微员工号协议 SAAS + AgentDesk 会话工作台。
- 会话页新增账号是两列弹窗：现场扫码与远程门店自助开户链接。
- 实例池支持清理未登录临时占用：`resolve_login_binding`，不自动解绑真实登录账号。
- 每个员工号绑定独立智能客服配置，原全局智能客服入口只保留兼容。
- 全托管/半托管/非托管模式影响转人工提醒走总部网页端还是门店群。
- 知识库 guard 已修正：知识库未命中不再直接固定兜底，先做意图判断；办入住、定位、小程序、转人工、寒暄、确认等走智能服务链路。
- 完整运行数据已备份到 `backups/migration-20260630-090044/`。

## 继续开发注意事项

- 修改企微员工号协议前，必须查 `wework.apifox.cn` 对应接口页面，不猜字段。
- 后端遵守 `models -> repositories -> services -> handlers`。
- 前端业务接口统一走 `web/lib/api/admin.ts`，不要在页面组件里裸 `fetch`。
- 改完后至少跑：

```bash
pnpm --dir web typecheck
docker run --rm -v "$PWD":/src -v agentdesk-go-cache:/go/pkg/mod -v agentdesk-go-build:/root/.cache/go-build -w /src golang:1.26-alpine sh -lc '/usr/local/go/bin/go test ./internal/handlers/dashboard ./internal/bootstrap ./internal/services ./internal/ai/runtime/executor -run "TestKnowledgePolicy|TestAIHandoff|TestAgentTeamSchedule|TestAuth" -count=1'
docker compose build agent-desk && docker compose up -d agent-desk
```

## 2026-08-21 回复知识裁决与通用库收口

### 目标与运行边界

本轮基于生产基线 `003dfb7` 做局部修复，生产回滚基线命名为“原神”。
目标是解决单层或双层错误召回、通用知识未正确兜底、公开经营主体问题未查
知识、信息问询误追房号，以及多问题中“可回答问题 + 待接待问题”互相吞掉。

回复主链路保持不变：一次 Intent、各原子问题并行知识检索、最多一次批量
Judge、一次 Generate、Commit、Outbox。没有新增第二次 Intent 或 Generate，
也没有修改计费口径、消息聚合、Task/Outbox、语音识别或接管状态机。

### 代码改动

- `internal/ai/runtime/executor/answerability_gate.go`：按原子问题保留检索结果，
  汇总一次 Judge，重建胜出证据，并保留混合批次中的可回答问题。
- `internal/ai/runtime/executor/knowledge_evidence_judge.go`：新增严格 JSON 的
  `direct/supporting/unrelated` 批量裁决；4 秒、2,048 token、零重试。
- `internal/ai/runtime/internal/impl/retrievers/knowledge_retriever.go`：保留
  `RawHits`，支持在不重新检索、不重复写日志的情况下重建授权证据。
- `internal/ai/runtime/internal/impl/callbacks/*`：Trace 新增
  `pipeline.evidenceJudge`，记录候选指纹、模型耗时、逐题选择和延迟接待任务。
- `internal/ai/runtime/reply_trigger_service.go`：混合批次先提交已有知识答案，
  再复用现有确认服务处理延迟接待，不增加第二次 Generate。
- `internal/services/model_profile_template_service.go` 与
  `internal/services/store_ai_model_setting_service.go`：新增内部 usage
  `knowledge_judge_llm`，不触发旧 FastGPT Profile 同步。
- `internal/pkg/replyintent/defaults.go`：将酒店、品牌、公司及老板、创始人、
  董事长等公开身份归入 `hotel_info/company_profile` 并要求查知识。
- `internal/services/conversation_handoff_confirmation_service.go`：追问房号优先
  使用真实客户原消息；语音使用 `mediaText/mediaSummary`。只有明确的客房内
  故障、送物、换物或现场动作才追房号，普通设施问询不追问。

对应测试位于：

- `internal/ai/runtime/executor/knowledge_evidence_judge_test.go`
- `internal/ai/runtime/deferred_knowledge_handoff_test.go`
- `internal/ai/runtime/executor/intent_pipeline_test.go`
- `internal/ai/runtime/executor/intent_human_route_test.go`
- `internal/services/conversation_human_dispatch_service_test.go`
- `internal/services/model_profile_template_service_test.go`
- `internal/services/store_ai_model_setting_service_test.go`

### 裁决规则

门店库和通用库继续并行检索。所有有候选的原子问题共享一次批量 Judge，
即使某题只有一个知识层也要判断，避免单层错误召回直接进入 Generate。
Judge 只判候选是否直接回答、仅能补充或无关，最终由代码固定裁决：

```text
store direct > general direct > no direct evidence
```

胜出层可以携带同层 supporting 证据，另一层完全不进入 Generate。Judge
不可用、超时、调用失败或返回非法协议时不重试，也不让回复任务失败；Trace
记为 `fallback`，继续使用原有确定性规则“门店有效命中优先，否则通用”。

多问题中若一部分有 direct、另一部分无 direct 或命中“转接”，Generate 只
回答有证据部分；答案提交成功后，再对待处理部分发送现有接待确认。若全部
问题都需接待，仍沿用 Generate 前的既有接待路径。

### 接口与数据影响

- 无数据库表、Migration、外部 API、DTO、枚举、WebSocket 或前端变更。
- 继续使用既有 `SystemConfig` 键
  `reply_runtime.general_knowledge_base_by_store`，值为 Store ID 到 Agent Desk
  KnowledgeBase ID 的映射。
- 模型模板新增内部槽 `knowledge_judge_llm`。生产模板应配置
  `deepseek-v4-flash`、`timeoutMs=4000`、`maxOutputTokens=2048`、
  `maxRetryCount=0`。
- 运行 Trace 新增内部 `pipeline.evidenceJudge` 字段；其
  `deferredHandoff/deferredTaskIds` 只用于提交后调用现有接待确认服务。
- 通用知识库切换只更新现有 KnowledgeBase 的 `dataset_id`，不改变门店映射
  或租户边界。

### 聚焦验证

最终提交前运行：

```bash
go test ./internal/ai/runtime/executor -run 'Test(KnowledgeEvidenceJudge|NormalizeKnowledgeEvidenceJudgeConfig|ParseKnowledgeEvidenceJudgeResponse|KnowledgePolicy)' -count=1
go test ./internal/ai/runtime -run 'TestDeferredKnowledgeHandoffFromTrace' -count=1
go test ./internal/services -run 'TestConversationHandoff(CollectsRoomForInRoomCategories|DoesNotCollectRoomForInformationQuestions|RoomDecisionUsesVoiceTranscript)' -count=1
go test ./internal/ai/runtime/executor ./internal/ai/runtime/internal/impl/retrievers ./internal/ai/runtime ./internal/services -count=1
```

覆盖重点包括：通用 direct 可以越过无关门店候选；两层均 direct 时门店优先；
Judge 失败保留旧确定性选择；多个原子问题只调用一次 Judge；单层候选仍判定；
有答案与无答案/转接并存时先答后确认；“有空调不”“小程序不能用”“电梯坏了”
和“停车场很吵”不追房号；客房内明确故障、送换物和现场动作仍追房号。

上述命令已在 2026-08-21 最终代码收口后重新执行，四个相关包全部通过；
结果不是较早并行修改前的缓存结论。

### 生产冒烟发现的连续消息漏答修复

首次生产 C02 冒烟中，合并输入已经包含“早餐有吗 / 停车免费吗 / 剃须刀在哪”，
但 Intent 只产出最后一题，导致 Judge、检索和 Generate 均只处理剃须刀。后段
没有截断答案，根因是连续消息在 Intent 阶段被错误缩成最后一条。

本轮最小修复为：Intent 动态提示明确逐条覆盖当前短消息组；检索把 Intent
遗漏但仍明显是问题或请求的短消息按原顺序补进同一次批量 Judge；Generate
边界按真实短消息组判断多问，不再只依赖 `intentTasks` 数量。纯背景如
“好困啊”不会被补成独立知识任务。

混合场景“空调坏了 / 我住1302 / 早餐几点”还补充了短消息组房号复用：只在
同 conversation、同 session、8 秒内、最后一次 AI/人工回复之后读取明确房号，
用于待处理空调任务的接待确认；旧房号和跨回复房号不会复用。

评测器新增逐问题 `RequiredOutcomes`，C02 必须分别覆盖早餐、停车、剃须刀；
X03 必须同时有早餐答案和空调故障的 deferred handoff。旧的
`MustContainAny` 不再把“只答中一题”误判为通过。

### Judge 后 active ReplyPlan 与接待确认收口

生产 X03 回归暴露了两个结构边界：Judge 的 `T1/T2` 来自真实检索问题顺序，
不能按数字直接映射为 Intent ReplyPlan 的 `task-1/task-2`；同时 deferred 场景
若 Generate 返回非法 JSON 或缺少可回答 part，空 `ReplyText` 会绕过原先位于
Commit 分支内的接待确认，Job 被当作成功但客户收不到消息。

本轮在 Judge 后按 `batch.Questions` 的真实客户顺序重建 active ReplyPlan：保留
可回答知识任务、补回 Intent 漏掉但检索已恢复的问题，并排除将由接待流程处理
的任务。Intent 专项 Prompt、多任务输出契约、Generate 用户输入、变量混合范围
和输出解析统一读取该 active plan，不再依赖 `Tn -> task-n` 位置转换。明确的
定位、小程序、人工等非知识任务不会被连续消息补题误送入知识检索；原 ReplyPlan
中 Text 为空的服务请求仍保留原 Intent/SubIntent，只补入真实问题文本。

deferred 时即使只剩一个可回答任务也要求结构化输出；非法或缺失 part 继续
fail-close，不放出未经归属的模型文本。进入接待确认的任务原文不会再写入
Generate 提示，避免模型把待处理主题复述进其他答案。外层执行把 deferred dispatch 与文本
Commit 解耦：有答案时先提交答案再发送接待确认；无答案文本时仍经过消息时效
检查后发送接待确认。没有新增 Intent、Judge、Generate、协议修复重试或固定等待。

本收口仅修改 Runtime/Executor 内部实现和测试；没有数据库、Migration、外部
API、DTO、枚举、WebSocket、前端、模型配置、计费或 Token 口径变化。聚焦验证为：

```bash
go test -p=1 ./internal/ai/runtime/executor ./internal/ai/runtime/internal/impl/retrievers ./internal/ai/runtime ./internal/services ./cmd/reply-runtime-eval -count=1
```

回滚可直接部署 `0f82a1d`，或完整恢复“原神”锚点
`backup/yuanshen-20260821-003dfb7`；本轮没有外部数据迁移需要反向处理。共享文件
主要是 Runtime Trace collector 和回复触发 service，push 前需核对并行分支同文件
修改，建议本提交作为 Judge/连续消息提交之后的独立收口提交合入。

### 通用知识库蓝绿上线

清洗成品为 `/private/tmp/agentdesk-general-kb-final/general-kb-final-90.csv`，
包含 90 条 FAQ、379 个问法，SHA256 为
`f7ef325546e62b8b686fa1e466952f1dbf7f47fdaddaca0c43885ca0b5f72533`。
精确操作单位于
`/private/tmp/agentdesk-general-kb-final/fastgpt-blue-green-rollout.md`。

上线时在南七 Store Team 内新建未被生产引用的 staging Dataset，使用 backup
模式导入并等待训练完成，再做真实检索验证。通过后以旧 `dataset_id` 为条件，
CAS 更新 Agent Desk KnowledgeBase `7` 的 `dataset_id`；
`reply_runtime.general_knowledge_base_by_store={"1":"7"}` 保持不变。旧 Dataset
和 collection 不删除、不禁用，以便原子回滚。严禁直接向当前生产引用的通用
Dataset 追加 90 条数据，因为创建 collection 后会立即参与真实检索。

代码部署后还需把生产 Intent Profile `1` 更新为当前默认 Prompt，并给实际按
`id ASC` 生效的模型模板 `1` 增加 `knowledge_judge_llm` 槽。完成 Dataset 切换
后，用空调存在性/故障、早餐+马桶堵塞、公开经营主体、吹风机、地巾、禁烟、
加盟及长文字/语音多问题做真实冒烟，并确认 Trace 中 Judge 每轮最多一次、
Generate 仍为一次。

### 风险与回滚

- Judge 正常路径最多增加 4 秒；它的语义误判会影响候选选择，因此必须观察
  `pipeline.evidenceJudge`。失败降级会保留旧检索结果，优先保障回复可用性，
  但也意味着旧规则下的弱相关候选可能继续出现。
- 混合批次的知识答案和接待确认是先后两个既有提交动作；确认消息使用同一
  handoff token 作为稳定发送 ID，并做三次短重试，失败不会回滚已经提交的知识
  答案。仍需观察极端进程退出窗口和既有 Outbox 日志。
- 通用库数据切换属于外部 FastGPT/生产数据库操作，不随代码 commit 自动完成。
  staging 未训练完成、Store Team 不一致或 CAS 影响行数不是 1 时必须停止上线。
- `conversation_handoff_confirmation_service.go`、模型 usage 常量及 Runtime Trace
  是共享高风险文件。合并前后需要核对 `codex/customer-audit` 与
  `codex/ai-billing` 的同文件修改，保留双方字段和语义。

“原神”回滚锚点：Git 标签 `backup/yuanshen-20260821-003dfb7`，bundle 为
`/private/tmp/agentdesk-yuanshen-20260821-003dfb7.bundle`。应用完整备份位于
`/opt/agentdesk/backups/yuanshen-20260821-101926`，FastGPT 完整备份位于
`/opt/backups/yuanshen-20260821-101926`。代码回滚可重新部署该标签对应 release；
通用库回滚只需 CAS 恢复 KnowledgeBase `7` 的旧 `dataset_id`；Judge 可通过
恢复旧模板/Intent Profile 或部署“原神”关闭。不要删除旧 Dataset，直到观察期
结束。

### 并行分支影响

本轮没有 Migration 或公开契约，业务合并可以独立回滚。`codex/ai-billing`
可能同时修改模型模板 usage 与计费记录相关文件，`codex/customer-audit` 可能
读取 Runtime Trace 或接待状态。提交和 push 前必须先 `git fetch origin`，检查
双方同文件差异；建议先保留计费分支的模型/usage 语义，再合入本轮新增的
`knowledge_judge_llm` 和 Trace 字段，最后运行上述回复链路聚焦测试。禁止通过
覆盖文件解决冲突。

# 回复链路优化计划（P0 / P1 / P2）

> 分支：`codex/deepseek-responses-schema`
> 基线：`7b78bcc` → 已推进到 `c1e000d`
> 日期：2026-08-13
> 目标：在不引入新模型、不破坏既有 Runtime V2 契约的前提下，让回复更"聪明、通人性、不乱承诺、不误发"，并补上模型故障时的韧性。

---

## 实施进度（更新于 2026-08-13 16:30）

| 项 | 状态 | commit |
|---|---|---|
| P0-1 承诺白名单 + 关系承接 | ✅ 已部署 | `c1e000d` |
| P0-2 视觉重试走槽位 | ✅ 已部署 | `c1e000d` |
| P1-1 意图"要信息 vs 要动作" | ✅ 已部署 | `c1e000d` |
| P1-2 生成阶段关系承接 | ✅ 已部署 | `c1e000d` |
| P1-3 知识未命中分两种 | ⏳ 待做 | — |
| P2-1 副链纳入主链状态机 | ⏳ 待做（需 ai-billing 对齐） | — |
| P2-2 模型通道熔断 | ⏳ 待做（需 ai-billing 对齐） | — |

**⚠️ 诚实边界**：P0/P1 已部署但**尚未做真实端到端模型测试**（部署后无新客户消息）。"更好/更聪明/更有人味"需真实对话验收，不能仅凭代码通过就断言。

---

## 0. 现状速览（计划前提）

- 主链 `AIReplyJob → Runtime → Commit` 已具备：租约、任务作废、动作账本、7 道校验、技术失败先重试再转人工。
- 遗留两大结构问题：
  1. **承诺拦截是"黑名单 + 事后打回重跑"**，黑名单永远列不全，且打回浪费一次模型调用；
  2. **副链（人工超时恢复AI）绕过主链租约与重试预算**，模型故障时会"恢复→失败→再人工"死循环。
- 图片识别失败已确认为 **vision 模型调用失败（`context canceled`，newapi 网关上游抖动）**，本次已加一次同步重试，但 vision 调用仍是裸 HTTP，没走槽位的 `MaxRetryCount`。

---

## P0-1：承诺从"黑名单拦截"升级为"白名单 + 黑名单兜底"

### 目标
让模型**只能承诺"系统能实际执行的动作"**，其余一律不得承诺，从源头杜绝"我帮你查/记下了/去查"这类空头承诺；黑名单继续兜底防漏。

### 改动点
- `internal/ai/runtime/instruction/assembler.go`（基础服务风格）：新增一段"动作白名单"规则——AI 只能执行且只能承诺这几类：`发定位 / 发小程序 / 发电话 / 转人工 / 建工单 / 查天气`；除此之外（含"查优惠、问同事、内部确认、后续跟进"）一律说明"需要同事处理"或澄清，**不得用第一人称承诺会去办**。
- `internal/ai/runtime/executor/safety_validator.go`：`futureCommitPhrases()` 继续扩充黑名单（保留刚加的），作为白名单之外的第二道兜底。
- `internal/ai/runtime/executor/intent_pipeline.go`：`buildIntentStagePrompt` 的"禁止"清单里补一条 `no_future_commit` 语义标签，与白名单一致。

### 影响面
- 纯 Runtime 提示词 + 校验层，**不涉及 models/migration/DTO/API/WebSocket**。
- 只改 `internal/ai/runtime/`，不触碰 `codex/ai-billing` 的模型/计费语义。
- 风险：白名单过严可能让"合理询问"（如"我看看有没有房"）也被拦——需保证白名单只约束"承诺执行"，不约束"澄清/说明"。

### 验证
```bash
go test -tags dev ./internal/ai/runtime/executor/ -run 'FutureCommit|Safety' -count=1
go test ./... -count=1
go vet -tags dev ./...
```
新增用例：`我帮你查`/`我记下了` → 拦截；`有停车场，地下车库有充电桩` → 放行。

---

## P0-2：视觉理解重试参数化（走槽位 MaxRetryCount）

### 目标
视觉理解不再"硬编码重试 1 次"，改为读取 vision 槽位配置的 `MaxRetryCount`，并做短退避，让图片识别更稳。

### 改动点
- `internal/services/media_understanding_service.go`：
  - `understandImage` 改为按 `resolved.MaxRetryCount`（来自 `ModelCallResolverService`，已有该字段）循环重试，超时/网络/429/5xx 才重试，其余立即失败。
  - 保留 `isRetryableMediaError`，把它从"固定 1 次"改成"循环上限"。
- 确认 `resolved.MaxRetryCount` 已正确透传（`model_call_resolver_service.go:198` 已有 `MaxRetryCount: slot.MaxRetryCount`）。

### 影响面
- 仅 `internal/services/media_understanding_service.go`，无 schema 变更。
- 边界：不改变 ASR/文档解析（它们的重试单独评估），只动 vision。

### 验证
```bash
go test -tags dev ./internal/services -run 'Media|Vision' -count=1
go vet -tags dev ./...
```

---

## P1-1：意图识别增强"诉求 vs 动作"判断

### 目标
客户说"来点优惠呗"是**要信息/要结果**，不是"要 AI 现在去办"。让意图层先判断"这是问询还是执行请求"，减少把问询误当承诺。

### 改动点
- `internal/ai/runtime/executor/intent_model_detector.go` 的 `runtimeIntentDetectSystemPrompt()`：新增一条分类纪律——"先区分【要信息】vs【要执行动作】；优惠/房态/会员等价格与权益问题默认是要信息，除非客户明确说'帮我办/帮我下单/帮我处理'，否则不得判为执行动作"。
- 关联 `capability_deriver.go`：确认这类"要信息"不会派生 `NeedsResource/NeedsTool`。

### 影响面
- 纯提示词（system prompt 字符串），无 schema 变化。
- 风险：意图分类变化可能影响已有 golden 测试（`TestRuntimeIntentDetectGoldenCallCountAndMessageOrder` 只测调用次数，不测具体分类，风险低）。

### 验证
```bash
go test -tags dev ./internal/ai/runtime/executor/ -run 'Intent' -count=1
go test -tags dev ./... -count=1
```

---

## P1-2：生成阶段"先接情绪再给答案"

### 目标
让回复少一点"执行器感"、多一点"接住客户"的人味，尤其"老客户/常客/再次咨询"这类关系型表达。

### 改动点
- `internal/ai/runtime/instruction/assembler.go` 基础风格：在现有"少用您、不 emoji、1-3 句"基础上，补一条**关系型承接**规则——"当客户表达'老客户/又来了/常来'等关系诉求，先一句话承接关系（如'老客户啦'），再给答案；但承接≠承诺给优惠，只表达'帮你看看规则/平台有没有'，不给确定结论"。
- `internal/ai/runtime/executor/recent_answer_context.go` / `contextcompiler/compiler.go`：确认 `PersonaPrompt`（门店可配语气）已进入 Generate 阶段（`compiler.go:314` 已读取），本项不改机制，只补默认文案。

### 影响面
- 纯提示词。不改变模型调用次数、Intent Schema、证据/动作账本。
- 风险：语气更口语化后，需确保"承接"不变成"承诺"——配合 P0-1 白名单一起生效。

### 验证
```bash
go test -tags dev ./internal/ai/runtime/... -run 'Context|Instruction' -count=1
```

---

## P1-3："没查到知识"分两种，回复更聪明

### 目标
区分"知识库确实没有"（该澄清）与"客户问的是跨门店常识/通用问题"（可给通用建议 + 说明以门店为准），不再一刀切都只追问。

### 改动点
- `internal/ai/runtime/executor/task_knowledge.go` / `reply_plan_v2.go`：`knowledgeStatus == no_context` 时，用确定性规则判断 query 是否属于"通用常识型"（设施/停车/发票/WiFi 等普适问题），若是则 `outputMode` 给 `text` + 约束 `"answer_with_general_guidance_and_store_disclaimer"`，否则维持 `clarification`。
- 约束在 `fact_grounding_validator.go` 放行这种"带免责声明"的通用回复（`本店以实际为准` 类），仍禁止无依据断言具体门店事实。

### 影响面
- 涉及 `task_knowledge.go` / `reply_plan_v2.go` / `fact_grounding_validator.go`，**均属 Runtime 核心保护区**，需谨慎。
- 不新增模型调用、不改知识检索契约。
- 需与 ai-billing 确认"通用常识型"清单不触碰计费/知识范围口径。

### 验证
```bash
go test -tags dev ./internal/ai/runtime/executor/ -run 'Knowledge|Fact|Clarify|Plan' -count=1
```

---

## P2-1：副链（人工超时恢复AI）纳入主链状态机

### 目标
消除"人工超时恢复 AI"绕过租约/重试预算的死循环风险，让恢复动作与主链一致：先检查模型通道健康、走退避重试、幂等收敛，避免反复"恢复→失败→再人工"。

### 改动点（架构级，需 ai-billing 对齐）
- `internal/services/ai_manual_resume_task_service.go`：
  - `processOne` 里 `TriggerAIReplySyncHook` 调用前，先查询通道熔断状态（见 P2-2）；熔断中则不改 `task_status`，直接推迟 `next_retry_at`，不进入人工。
  - `failOrRetry` 的"重试次数耗尽即转人工"改为：**技术失败**（intent/generation/commit）不再累加人工转接，而是继续退避重试到通道恢复；只有**明确的业务人工诉求**才转人工。
- `internal/ai/runtime/reply_trigger_service.go`：确认 `TriggerReplySync` 对 `manual_resume_` 前缀 request 的幂等收敛（已有 `executeIntentHumanRoute` 的 `manual_resume_` 短路）继续保留。

### 影响面
- **共享高风险**：`ai_manual_resume_task_service.go` 涉及"技术失败 vs 业务转人工"语义，属于 ai-billing 关心的边界，必须对齐后再改。
- 不新增表/DTO/API。可能需要在 `AIManualResumeTask` 加一个"是否技术失败"标记列（migration），需评审。
- 回滚：切回上一二进制即可（若加列，回滚需评估列兼容）。

### 验证
```bash
go test -tags dev ./internal/services -run 'AIManualResume|ManualSession|Handoff' -count=1
go test -race -tags dev ./internal/ai/... ./internal/services -run 'AIReply|Resume|Handoff' -count=1
```

---

## P2-2：模型通道级熔断

### 目标
当 intent/reply/vision 任一模型通道连续失败 N 次，短时间"停用"该通道，避免每条消息都撞一次、失败一次、转一次人工。

### 改动点（架构级）
- 新增轻量内存熔断器 `internal/ai/runtime/channelbreaker/`（或 `internal/pkg/`），基于 `AIUsageGatewayCall`（已有 `Stage/HTTPStatus/LastErrorClass/FinishedAt` 字段）统计近 N 次失败。
- 接入点：
  - `intent_model_detector.go` / `reply_output_v2.go` 的模型调用前查询熔断状态；
  - `media_understanding_service.go` 的 vision 调用前查询熔断状态。
- 熔断触发后：**技术失败快速降级**（不反复调模型），明确人工/知识转人工不受影响。

### 影响面
- **共享高风险**：熔断策略会影响"模型调用次数 + 计费口径"，必须与 ai-billing 对齐阈值与降级语义。
- 不新增表（用现有 `AIUsageGatewayCall` 统计）或仅新增内部内存状态。
- 回滚：纯代码，切回上一 release。

### 验证
```bash
go test -tags dev ./internal/ai/runtime/channelbreaker/... -count=1
go test -race -tags dev ./internal/ai/... ./internal/services -run 'AIReply|Runtime|Intent|Knowledge|Breaker' -count=1
```

---

## 实施顺序与依赖

| 阶段 | 项 | 依赖 | 是否需 ai-billing 对齐 |
|---|---|---|---|
| 第一批（立即） | P0-1、P0-2 | 无 | 否 |
| 第一批 | P1-1、P1-2 | 无 | 否 |
| 第二批 | P1-3 | 无 | 建议确认 |
| 第三批 | P2-1 | P2-2 | **必须** |
| 第三批 | P2-2 | 无 | **必须** |

> 建议：先做 P0+P1（纯 Runtime，不碰共享契约，风险低、见效快），P2 作为独立 PR 与 ai-billing 对齐后再合并。

## 分支影响与回滚

- P0/P1：仅 `internal/ai/runtime/` 与 `internal/services/media_understanding_service.go`，不碰 models/migration/DTO/API/WebSocket，不影响 `codex/customer-audit`。
- P2：`ai_manual_resume_task_service.go` 属共享高风险，需先 `git fetch` 核对并行分支，单独 PR。
- 回滚：所有项均可切回上一 release；P2-1 若加列需评估列兼容。

## 验收口径（整体）

1. 不新增模型调用次数（除 P0-2 视觉重试、P2 熔断降级）。
2. 不乱承诺：白名单 + 黑名单 + 事实接地三层都生效。
3. 非酒店定位不误发。
4. 图片识别失败率下降（重试 + 熔断避免反复撞）。
5. 技术失败不再引发"恢复→失败→再人工"死循环。

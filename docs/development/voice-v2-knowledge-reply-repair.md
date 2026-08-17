# 语音与文字一致性及 V2 知识回复修复实施记录

## 1. 目标

本次修复针对真实运行链路中的一类通用问题：客户发送的语音和等价文字没有进入同一语义输入，连续消息的临时合并对象又被媒体解析逻辑覆盖，导致入住、地址、外卖、优惠、停车、咖啡等知识问题出现“文字能答、语音不答”“知识任务没有执行”或误触发人工。

本次不为某一个词增加关键词特例，也不修改 FastGPT、NewAPI、公开接口、Intent JSON Schema、计费口径或模型调用次数。修复原则是恢复 V2 的轻量主链：

```text
入站消息
  -> 媒体结果归一化（语音 ready 只取 ASR）
  -> 连续消息语义合并（只生成内存文本投影）
  -> Intent/任务拆分
  -> 按任务检索知识与执行资源
  -> 模型组织最终语言
  -> 任务级提交与人工门禁
```

## 2. 真实故障与根因

### 2.1 语音和文字走了不同输入

旧行为会把语音文件名、`[语音]` 标签或媒体摘要拼到业务文本中。即使 ASR 已经得到“办理入住”或“酒店地址”，Intent 和知识检索看到的也不是与文字消息相同的 canonical text，模型可能把它当成媒体消息、上下文补充或无法确认的问题。

现在的规则：

- `ready/understood` 的语音优先使用纯 ASR transcript。
- transcript 缺失时才使用已经存在的媒体摘要。
- `pending/processing/failed` 不进入业务语义链，等待媒体完成后由既有唤醒机制重新处理。
- 不把音频文件名、`[语音]`、`语音内容是`等展示标签注入语义输入。
- 相同内容的文字和已完成 ASR 语音产生相同的 runtime text。

### 2.2 连续消息合并后再次解析当前语音

连续消息会先合并成一段带顺序的语义文本。原实现仍保留最后一条消息的 voice 类型和 payload，后续 runtime 再次读取 payload 时可能丢掉前面已经合并的消息，只留下当前语音的媒体结果。

现在合并对象是明确的内存语义投影：

- `MessageType=text`。
- `Payload` 清空。
- 原始数据库 Message 不修改。
- 合并文本按原消息顺序保留，供 Intent、任务拆分、知识查询和 Generate 共用。

### 2.3 人工能力被错误提升为整轮人工

同一轮可能同时包含业务知识任务和人工任务，例如“先问入住流程，再说房间有问题需要人工”。旧的顶层 `NeedsHumanRoute` 会让整轮提前进入人工链，知识检索和 Generate 根本没有机会执行。

现在人工路由是 task capability：

- `human_complaint_risk` 任务仍保留 `NeedsHumanRoute` 和派单原因。
- 只有整轮没有任何可回答业务、知识、资源或普通服务任务时，顶层 `NeedsHumanRoute` 才为 true。
- 混合轮次保留知识/资源任务，正常执行检索和生成；人工任务由任务账本单独收敛。
- 入住流程知识任务优先作为主意图，避免被人工任务或资源任务覆盖。

## 3. 代码级实现

### 3.1 语义输入归一化

文件：`internal/pkg/utils/message.go`

`BuildRuntimeMessageTextWithPayload` 对 voice 做专门分支。该函数是 runtime 语义输入的统一入口，后续阶段不得绕过它直接读取音频文件名或原始 payload 作为问题文本。

伪代码：

```go
if messageType == Voice {
    text, summary, status := RuntimeMediaUnderstandingFromPayload(payload)
    if status is pending/processing/failed { return "" }
    if text != "" { return text }
    return summary
}
return existingTextAndMediaUnderstanding(messageType, content, payload)
```

图片、附件等媒体仍保留既有“媒体标签 + 理解结果”语义；只有已完成语音的 transcript 作为客户业务问题，不再被媒体标签污染。

### 3.2 连续消息语义投影

文件：`internal/ai/runtime/reply_trigger_service.go`

`mergeRecentCustomerBurstMessage` 只负责将同一有效窗口的客户消息投影为一条临时文本消息。投影包含顺序和必要时间前缀，但不改变数据库中的原消息、不生成第二个业务 Message、不创建额外模型任务。

关键不变量：

1. 所有可用消息都通过 `BuildRuntimeMessageTextWithPayload` 生成片段。
2. 空的媒体结果不阻断其他文本问题。
3. 合并结果 `MessageType=text` 且 `Payload=""`。
4. 后续任何阶段只能消费合并后的 `Content`，不能重新解析最后一条原始媒体 payload。

### 3.3 任务级能力派生

文件：

- `internal/ai/runtime/executor/intent_model_detector.go`
- `internal/ai/runtime/executor/capability_deriver.go`
- `internal/ai/runtime/executor/task_ledger.go`

模型输出先归一为 `IntentTasks`，再由已发布的 Intent 配置派生能力。业务能力来自配置和任务类型，而不是来自客户词语白名单：

- `hotel_info`：知识任务。
- `hotel_variable`：结构化资源任务，可与知识任务并存。
- `service_request`：普通业务回答任务，不能因旁边存在人工任务而被吞掉。
- `human_complaint_risk`：人工任务。

主意图选择顺序：

1. 入住流程等 `hotel_info` 业务知识任务。
2. `hotel_variable` 资源/门店变量任务。
3. 其他 `hotel_info` 知识任务。
4. 其他普通业务任务。
5. 仅剩人工任务时才使用人工意图。

顶层人工门禁：

```text
NeedsHumanRoute = hasHumanTask && !hasAnswerableBusinessTask
```

这不是取消人工，而是把人工判断放在正确的任务边界内，避免“一个人工任务导致所有知识任务不检索”。

### 3.4 知识检索与最终回答边界

知识检索继续使用当前 V2 runtime 的既有执行器和 FastGPT 网关。此次不增加查询次数，不把检索原文直接发送给客户：

- 每个知识任务独立决定 `hit/no_hit/failed`。
- 成功证据作为 Generate 的参考上下文。
- 最终客户可见语言仍由 Generate 模型组织。
- no-hit 不编造门店事实；按既有澄清或明确无资料策略处理。
- 技术失败不伪装成客户要求人工。

语音、文字、连续消息最终都使用同一份任务文本和同一份知识检索入口，因此“办理入住”“酒店地址”“外卖”“优惠”等属于同一类问题的所有表达都能走统一链路。

## 4. JSON 与内部数据边界

本次不改公开协议和 Intent Schema。现有内部 JSON 仍遵守以下边界：

### Intent

- 模型负责提出 `intentTasks`、任务顺序、任务文本和意图。
- 服务端根据启用的 `ReplyIntentConfig` 派生 `needsKnowledge`、`needsResource`、`needsHumanRoute`。
- 顶层字段只能汇总任务，不得覆盖任务能力。

### ReplyPlan

- 每个任务保留稳定 `taskKey`、来源顺序和输出模式。
- 知识结果只绑定对应任务，不把整轮历史知识答案注入当前新问题。
- 资源动作必须由结构化任务执行，不能用知识文本代替定位、小程序等资源。

### Trace

- `IntentTasks` 记录任务级人工能力、知识能力和资源能力。
- 顶层 `NeedsHumanRoute` 只表示 human-only 轮次。
- `MatchedConfig` 用于诊断和配置追踪，不作为绕过任务链的人工开关。

## 5. 已落地的回归保护

已增加针对真实故障的测试，不做与本次修复无关的全量审计：

- ready 语音与同文文字得到相同 canonical text。
- ASR 失败/处理中内容不进入语义上下文。
- 连续消息投影为 text，不能再次解析末条 voice payload。
- 入住知识任务在混合任务中保持主意图。
- 混合人工与知识任务时，知识链仍可执行。
- V2 capability adapter 保留任务级人工能力。
- 原有人工确认在新业务主题到达时继续按现有状态机清理，不扩大修改范围。

## 6. 发布与回滚

发布范围仅为微宝应用代码：

- 不改 FastGPT。
- 不改 NewAPI。
- 不改数据库公开协议。
- 保留新增代码的向后兼容行为。

发布前确认：

1. 代码提交到当前修复分支，并推送到 `origin`、`weibao`。
2. 将最新 `origin/main` 合入，保留主线已有接管界面修复。
3. 构建发布包并上传到 `/opt/agentdesk/releases/<release>`。
4. 更新 `/opt/agentdesk/current` 和 `REVISION`。
5. 重启 `agentdesk.service`。
6. 确认服务 active、HTTP 200、日志无 panic，且 `REVISION` 等于发布提交 SHA。

回滚只切回上一个 release 并重启服务；本次没有破坏性表结构或外部协议变更。

## 7. 验收标准

只验证与本次故障直接相关的行为：

- 同一句问题用文字和 ready 语音发送，Intent、知识查询和最终回答的业务输入一致。
- 连续发送图片/语音后补充问题，前面媒体结果和后面文字都进入同一批任务。
- 入住、地址、外卖、优惠、停车、咖啡等知识类问题按启用配置进入知识链；知识内容由模型组织成客户可读回答，而不是直接倾倒检索原文。
- 混合人工诉求和知识问题时，知识任务不被整轮人工门禁截断。
- 纯人工投诉/安全问题仍进入人工确认和现有派单链路。
- 语音 ASR 尚未完成时不生成错误答案，完成后由既有唤醒机制重试。

# 回复动作目录 + 知识编造/转人工修复 · 测试报告

> 分支：`codex/deepseek-responses-schema`
> 基线：`886a6a5`
> 日期：2026-08-13
> 范围：动作目录（新增页面 + 后端 + 链路接入）+ 4 个聊天问题的结构性修复

---

## 一、交付内容

### 1. 动作目录（新页面 + 后端真实可用）

- 代码注册表 `internal/ai/runtime/actions/`：8 个动作（6 内置可用 + 查天气工具 + 2 外部预留），执行器通过 `RegisterExecutor` 由 runtime 层注入（避免循环依赖）。
- 两张新表（AutoMigrate）：
  - `t_reply_action_definition`：动作定义 + 启用开关
  - `t_knowledge_action_binding`：知识记录(SourceRecordID) ↔ 动作 绑定
- 后端 CRUD：repository / service / handler / route / 平台权限 `replyAction.view` `replyAction.update`。
- 前端新页面 `web/app/dashboard/reply-actions/`：列表 + 开关 + 类型徽章 + "未接入"灰显。
- 导航入口 + 中英文 i18n。

### 2. 链路接入（知识命中 → 结构化动作）

- `task_knowledge.go`：检索命中后查绑定，命中 `human_handoff` 提升为人工路由计划。
- 确定性兜底：知识正文含"转人工/人工客服/需人工…"时自动按转人工处理（覆盖存量知识，无需手动绑定）。
- `intent_pipeline.go`：改写 plan + 收敛 intent，驱动既有二次确认链（"要我帮您转人工吗？回复确认/取消"）。

### 3. 知识编造修复（事实接地校验）

- 新增 `fact_grounding_validator.go`：`clarification` 任务禁止确定性断言（有/没有/可以/不能…），命中触发一次协议修复。
- `validation_result.v1` Schema 增加 `factGrounding` 校验项。

---

## 二、验证结果（全部通过）

| 命令 | 结果 |
|---|---|
| `go build ./...` | ✅ 通过 |
| `gofmt` | ✅ 全部格式化 |
| `go vet -tags dev ./...` | ✅ 无告警 |
| `go test ./... -count=1` | ✅ 全部通过（无 FAIL） |
| `go test -tags dev ./... -count=1` | ✅ 全部通过（无 FAIL） |
| `go test -race -tags dev ./internal/ai/... ./internal/services -run 'AIReply\|Runtime\|Intent\|Knowledge\|Outbox\|Handoff\|Checkin' -count=1` | ✅ 通过（exit 0） |
| `tsc --noEmit`（等价 pnpm typecheck） | ✅ 通过（exit 0） |
| `eslint`（新改前端文件） | ✅ 通过（exit 0） |

### 新增单元测试

| 测试 | 覆盖 |
|---|---|
| `TestRegistryRegistersBuiltinActions` | 8 个动作注册 + kind 正确 |
| `TestExternalActionNotProvisioned` | 外部动作未接入不可执行 |
| `TestRegisterDuplicatePanics` | 重复 code 启动期暴露 |
| `TestValidateReplyFactGroundingBlocksUngroundedAssertion` | 澄清任务断言被拦截 |
| `TestValidateReplyFactGroundingAllowsClarification` | 澄清句不误伤 |
| `TestValidateReplyFactGroundingSkipsGroundedTask` | 有证据的 text 任务不拦截 |
| `TestKnowledgeContentRequiresHandoff` | 知识正文转人工判定 |

---

## 三、回归说明（诚实边界）

1. **tenant_integrity 审计测试**：新表 `KnowledgeActionBinding` 已补入租户审计策略与关系，并将测试期望值从 `120/313` 更新为 `121/315`，测试通过。

2. **"不保留旧链路、不并存" 的落点**：
   - 知识命中"转人工"的行为，已从"模型口头复述（如『稍等我看看怎么转接哈』）"改为"结构化二次确认"，二者不并存 —— 提升后 `executeIntentHumanRoute` 短路返回，不会再进入 Generate 产出复述文本。
   - 意图层 `needsHumanRoute`（客户**明确说**"转人工"的触发）保留：这是与知识层不同触发源，最终都汇聚到同一个 `ConversationHandoffConfirmationService` 二次确认链，不是重复链路。

3. **P1/P2（AIManualResumeTask 状态机原子性）本次未改**：那是我上一轮审查额外发现的独立风险，不属本次已确认的动作目录方案范围，也未在本方案文档中立项，故未擅自扩大改动。如需一并处理，请另行确认。

---

## 四、遗留与建议

- `query_room_status`（PMS 房态）、`query_member_level`（会员）为外部预留动作：目录中灰显、禁止启用，执行器为 `ErrActionNotProvisioned`。接入真实系统时写执行器 + 打开开关即可。
- 动作目录当前为**平台全局开关**（所有门店共用），按方案评审已确认；若后续需门店级开关，再评估扩展。

## 五、未提交项

- `.codex-build/`（按要求不提交）
- 所有源码改动已在工作区，**未 git add / 未 commit**，等你确认后再提交。

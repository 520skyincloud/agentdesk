# 回复链路完整修复交接（2026-08-18）

> 分支：`codex/reply-chain-complete-fix`，提交 `f631134`，基线 `ab5cf9e`（`codex/voice-v2-knowledge-repair`）
> 生产发布：`20260818-replychain-f631134`（二进制 SHA256 `7364209bdcc92726870407cb95516c445e8930c08b438a3e60772135fa1fe125`）
> 回滚：`ln -sfn /opt/agentdesk/releases/20260817-voice-v2-ab5cf9e /opt/agentdesk/current && systemctl restart agentdesk`

## 目标

恢复 V2 时期的正确回复效果，消除误转人工与客户可见技术失败提示。

## 改动内容

1. 意图层：流程咨询与员工执行动作分流。`办入住/退房/开票/停车`等咨询归 `hotel_info`；`帮我办理/派人来/送到房间`等明确执行保留 `service_request`。覆盖 V2/V3 适配和无 task 数组的顶层意图（`normalizeInformationalProcedureIntent`）。
2. 知识证据：每题最多 4 条最强 supporting 证据；额度公平分配（先保强证据再受总上限约束）；blocked/related 不占额度；异常 FAQ 不得回答正常流程问题；条件性"联系客服"不再整体判异常。
3. 人工话术门禁：新增 `handoff_claim_validator.go`，无真实人工任务或已提交 handoff action 时禁止"已转人工/人工接手/同事会联系"类表述，V2/V3 均接入。
4. 技术失败：`sendTechnicalFailureNotice` 不再发客户可见"这条消息暂时没有处理成功"，仅写内部终态与结构化日志（result_code=`technical_failure_no_customer_notice`）。
5. 取消确认："不要/不用了"取消人工确认不再重跑 Generate。
6. 测试修正：
   - `TestDeriveRuntimeIntentCapabilitiesUsesPublishedConfig` 对齐既有契约（混合轮次 hotel_variable 主意图，见 `TestRuntimePipelineHotelVariableMixedHotelInfoRequiresKnowledge`）。
   - `TestNormalProcedureFAQWithConditionalContactAdviceRemainsUsable` 样本改为生产真实的"问题：/答案："包装形态；无侧车元数据的真实 FAQ 由该标记识别为 imported_faq，不放宽 procedure/policy 门禁。

## 验证

- `go test ./internal/ai/... ./internal/services/... ./internal/repositories/... -count=1` 全绿。
- `go vet` 通过；未触碰 `weather_tool.go`（基线既有格式问题）。
- 部署后：服务 active、HTTP 200、无 panic；前端为 ab5cf9e 配套构建（前后端同源，无混版）。

## 共享文件与并行分支

改动集中在 `internal/ai/runtime`（detector/pipeline/knowledge/validator）与 `internal/services`（ai_reply_job、handoff confirmation）。未改公开 API、Intent Schema、计费、企微协议、migration。

## 遗留

- 生产回归（其风会话：入住/地址/外卖/优惠）待真实消息验证。
- 昨日遗留：知识同义词问法（exact 门槛）在 V3 分支仍未修，当前 V2 主链已通过 imported_faq 识别缓解大部分场景。

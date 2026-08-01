# FastGPT 门店托管知识库

> 状态：当前运行设计。若本文与代码冲突，以 `internal/pkg/fastgpt`、`internal/services/fastgpt_*` 和 `internal/ai/rag/fastgpt_engine.go` 的真实调用链为准，并先修正文档再发布。

## 1. 事实归属

```text
Tenant
  -> Store
    -> FastGPT managed Team
      -> Dataset / Collection

Store
  -> active ModelProfile revision

StoreStaffBinding
  -> active Credential revision
```

- Store.KnowledgeBaseID 是门店知识库唯一事实源。
- 一个 Store 同时只激活一个 KnowledgeBase；同店多个 Binding 共享 Dataset 和知识内容。
- Model Profile 属于 Store，NewAPI Credential 属于 StoreStaffBinding。
- FastGPT target/applied 状态必须同时记录实际提供凭据的 Binding 和 revision。
- 企微实例、会话路由中的 KnowledgeBaseID 只是 Store 投影，不提供独立选择能力。

## 2. 托管连接和隔离

- FastGPT 根地址使用 `AGENT_DESK_FASTGPT_BASE_URL`。
- 集成令牌只使用 `AGENT_DESK_FASTGPT_INTEGRATION_TOKEN`，且只从部署环境注入。
- 所有远端操作先解析 Tenant + Store；无 Store scope 直接失败。
- 网关使用稳定 `externalStoreId`，FastGPT 服务端必须验证 externalStoreId、teamId 和 datasetId 一致。
- 不支持平台 FastGPT API Key、知识库自带 Key、旧直连 transport、公共 Dataset 或本地 FAQ/Chunk fallback。
- Base URL、Token、门店 API Key、密文、nonce 和完整指纹不得进入浏览器、API、日志、错误或 WebSocket。

## 3. Profile 与 Credential 单向派生

```text
Store active Profile
  + selected StoreStaffBinding active Credential
  -> FastGPT target revision
  -> 远端测试和同步
  -> applied Binding/Profile/Credential revision
```

- Profile 事实源只有本地 StoreModelProfileAssignment。
- 凭据事实源只有当前调用 Binding 的 StoreModelCredential。
- FastGPT Profile 是单向派生投影，不提供第二套编辑入口。
- 同步失败保留旧 applied revision；迟到任务不能覆盖新 target。
- RAG 前校验 Store、KnowledgeBase、Binding、Profile、Credential 和 applied revision 全部一致。
- 同一 Store 的另一个 Binding 使用知识前，必须有自己的可用 Credential 和对应测试证据；不得借用其他员工号 Key。

## 4. Dataset 与持久任务

- Team 以 externalStoreId 幂等确保。
- Dataset 只通过门店知识库 provision 流程创建。
- provision、上传、索引轮询和 Profile 同步进入带 Tenant、Store、TargetBinding 和 revision 快照的持久任务。
- worker 通过租约和 compare-and-swap 领取；租约过期可接管，任务不能并发完成两次。
- target 与当前事实不一致时终止，不继续调用远端。
- 普通失败退避重试，达到上限后进入 failed；错误只保存安全分类。
- Dataset 删除要求显式确认，远端成功后才清空 Store 和运行投影。

## 5. 检索和知识进化

- 请求只携带 externalStoreId、datasetId、查询文本、token limit、相似度、模式、rerank 和 TopK。
- DTO 不接受 Provider、BaseURL、Key、Token、任意请求头或跨 Store Team ID。
- 返回 DatasetID 与请求不一致时整次失败。
- 上游 score 统一解析后由本地 threshold 和 TopK 截断。
- 知识进化候选归属 Store；不同 Binding 的会话可以共同贡献，但同一原始 Message 只处理一次。
- 会话继承只改变阅读链，不复制消息或重新触发知识进化。

## 6. Usage 归因

- 只同步 Store 当前权威 KnowledgeBase 的 Usage。
- 每个 cursor 窗口固化 StoreStaffBindingID、Profile revision、Credential revision、Key fingerprint 和远端 Profile revision。
- 幂等键使用远端稳定事件 ID；同步失败不得重新触发模型调用。
- 无法识别用途槽、Binding、revision 或 request ID 时失败关闭，不生成猜测账单。
- 所有事件幂等落库后才 CAS 推进 cursor，迟到 worker 不能回退新窗口。

## 7. Migration 72 与上线

- fresh SQLite/MySQL 和具有已知 Migration 历史的受控 MySQL 均受支持。
- Migration 72 根据现有 Credential、Profile test、Usage、FastGPT target/applied 和 KnowledgeBase 证据回填 Binding 归属。
- 未配置空 Credential 可按活动 Binding 建立空记录；已有 Key 或 Usage 且无法唯一解析时阻止迁移。
- 不搬运旧本地 FAQ/Document/Chunk，不猜测远端 Dataset，不硬编码 Store ID。
- 生产必须先在独立恢复库运行 Migration 72，再进行受控 Store 的 Team、Dataset、上传、索引、检索、Profile、Usage 和删除生命周期验收。

真实集成测试只允许显式开启：

```text
FASTGPT_MANAGED_INTEGRATION_LIFECYCLE=1
FASTGPT_MANAGED_INTEGRATION_BASE_URL=<candidate-base-url>
FASTGPT_MANAGED_INTEGRATION_TOKEN=<candidate-service-token>
FASTGPT_MANAGED_INTEGRATION_STORE_ID=<temporary-store-id>
go test ./internal/pkg/fastgpt -run TestGatewayManagedIntegrationLifecycle -count=1
```

普通单元测试、连通性检查或 mock 不能替代真实生命周期证据。

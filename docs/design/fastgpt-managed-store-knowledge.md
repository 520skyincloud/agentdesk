# FastGPT 门店托管知识库

> 状态：B6 当前设计。若本文与代码冲突，先追踪 `internal/pkg/fastgpt`、`internal/services/fastgpt_*` 和 `internal/ai/rag/fastgpt_engine.go` 的真实调用链，再修正文档。

## 1. 边界与事实源

最终拓扑只有一套：

```text
Tenant
  -> Store
    -> FastGPT managed Team
      -> Dataset / Collection
```

- Tenant 是数据隔离根，Store 是 FastGPT 远端 Team、模型凭据、用量和知识库的最小业务主体。
- `Store.KnowledgeBaseID` 是门店当前知识库的唯一事实源。
- 一个 Store 同时只能激活一个知识库；一个知识库只能属于一个 Tenant + Store。
- `WxWorkProtocolInstance.KnowledgeBaseID` 与 `ConversationRouteState.KnowledgeBaseID` 只是由 Store 同步出的投影，不提供独立选择能力。
- 新建、激活和删除知识库时，Store 与全部企微、会话路由投影必须在同一事务内同步。
- 企微账号页面不选择知识库，也不能编辑 FastGPT 地址、令牌、Dataset、模型或 Profile。

## 2. 托管连接与隔离

- FastGPT 根地址使用 `AGENT_DESK_FASTGPT_BASE_URL`。
- 服务端集成令牌只使用 `AGENT_DESK_FASTGPT_INTEGRATION_TOKEN`，并且只从部署环境注入。
- 不支持平台 FastGPT API Key、知识库自带 Key、旧直连 API 或 legacy transport。
- 所有远端操作必须先调用 `ForStore(storeID)`；没有 Store scope 的请求直接失败。
- 网关只发送 `X-Agent-Desk-Token`，每次请求同时携带稳定 `externalStoreId`。
- FastGPT 必须在服务端校验 `externalStoreId -> teamId -> datasetId`，任何不一致都失败关闭，不能回退到其他 Store 或公共 Dataset。
- BaseURL、Integration Token、门店模型 Key、密文和完整指纹不得进入数据库备注、API 响应、日志、错误、WebSocket 或浏览器。

## 3. Model Profile 单向派生

```text
Store active ModelProfile revision
  + Store active Credential revision
  -> FastGPT target revision
  -> 远端测试与同步
  -> applied Profile/Credential revision
```

- Model Profile 的事实源只有本地 active Store Assignment 和 Store Credential。
- FastGPT Profile 是单向派生投影，不提供独立创建、编辑或保存接口。
- 页面只显示模型名、target/applied revision、就绪状态、测试时间和重新同步动作。
- 同步失败时保留旧 applied revision；迟到任务不得覆盖新的 target revision。
- RAG 检索前必须同时校验 Store 当前知识库、Assignment、Credential 和 FastGPT applied revision 全部一致且 ready。

## 4. Dataset 与任务

- Team 创建以稳定 `externalStoreId` 幂等执行。
- Dataset 只能通过“新建门店知识库”流程 provision；通用知识库 CRUD 不允许创建 FastGPT 类型。
- Dataset provision、文件上传/索引轮询和 Profile 同步进入带 Tenant + Store 的持久任务。
- Collection 查询/删除、Dataset 激活/删除是用户显式触发的同步操作；远端成功后，本地状态和 Store/企微/会话路由投影才在事务内提交。它们不伪装成持久任务。
- 每个任务保存 `TargetProfileID`、`TargetProfileRevision` 和 `TargetCredentialRevision` 快照。
- Worker 使用租约和 compare-and-swap 领取任务；租约过期可接管，同一任务不能并发完成两次。
- 任务 target 与当前 target 不一致时永久失败，不继续调用远端。
- 普通失败按 `30s * attempt^2` 退避；第五次失败进入终态 `failed`，target 已变化则首次即终态失败。终态任务释放租约并清理临时上传资产，不允许无限重试。
- 错误只保存安全错误分类，不保存上游原文、Key 或令牌。
- Dataset 删除要求输入知识库全名；远端确认已删除后，才清空 Store、企微和会话路由投影。

## 5. Usage 归因

- 只同步 Store 当前权威知识库的 FastGPT Usage。
- 每个 cursor 窗口保存当时的本地 Profile、Credential、Key fingerprint 和远端 FastGPT Profile revision。
- FastGPT 模型 Usage 的 `profileRevision` 必须是正 `int64`；`AIUsageEvent.ModelProfileRevision` 也使用数值 revision，不接受字符串猜测或当前 revision 倒填。
- UsageEvent 使用窗口快照归因，禁止用当前配置倒填历史请求。
- 无法识别用途槽、远端 revision 未知或归因不完整时失败关闭，不生成猜测账单。
- 幂等键为 `fastgpt:<teamId>:<externalEventId>`；同步失败不允许重新触发模型调用。
- 有事件的响应必须返回非空且不同于当前值的 `nextCursor`；空事件且无新 cursor 时保持原值，空事件但有新 cursor 时允许推进。
- 事件全部幂等落库后，才以 cursor + Profile/Credential/Fingerprint 完整窗口快照执行 CAS 推进。并发 worker 已推进时视为成功 no-op；迟到失败只能在原快照仍匹配时写安全错误分类，不能回退新窗口。

## 6. 安全检索契约

- Agent Desk 只向 FastGPT 发送 `externalStoreId`、`datasetId`、查询文本、token limit、相似度、检索模式、是否 rerank 和 TopK。
- 检索 DTO 不接受 Profile、Provider、BaseURL、API Key、Integration Token、任意请求头或跨 Store Team ID。
- 返回结果只投影 Dataset/Data/Collection 标识、来源名、问题、答案和分数；任一结果的 DatasetID 与请求不一致时整次请求失败。
- 上游返回的多种 score 结构统一解析后，再由本地执行 threshold 和 TopK 截断；原始上游响应和错误正文不进入前端、日志或账单。

## 7. 迁移与上线门禁

- 当前部署只支持 fresh 数据库。Migration 072 只初始化当前 Schema 的 Store 投影、任务和 Usage 窗口，不读取旧本地知识表或旧远端 Dataset。
- Tenant/Store 建立后通过受管 provision 新建 Team 和 Dataset；禁止把历史 remote ID 填入新任务。
- 新 Dataset 的 Profile 先只提交到候选知识库，不修改 Store 当前 applied revision；完成上传、索引和检索验收后，启用操作才原子切换 Store、企微和会话路由，并把旧记录标记为可清理。
- 非终态旧任务补齐当前 target；无法补齐或 target 已过期的任务标记失败。
- 已应用 revision 可证明时回填 Usage cursor 归因；部分冲突归因阻止迁移，不静默覆盖。
- SQLite 与 MySQL 必须运行同一幂等场景测试。
- 真实上线前必须完成受控 Store 的 Team、Dataset、上传、索引、检索、Profile 同步、Usage 和物理删除生命周期测试。
- 旧远端 Dataset/Collection 不进入最终运行链；新资源验收后按 Tenant + Store + remote ID 清单受控清理。

## 8. 退役链边界

- FastGPT 独立 Profile 写接口、企微模型/Profile 前端、旧租户模型授权、StoreSetting 和
  模型分配后端链已经删除。
- 当前 migration 不再读取旧 AIConfig、授权、本地 Document/FAQ/Chunk 或历史远端
  Dataset；fresh Schema 也不创建这些表。
- 旧接口必须保持 404，任何后续功能不得恢复兼容 caller、第二套 Profile 或本地检索
  fallback。

真实候选环境测试只允许显式开启：

```text
FASTGPT_MANAGED_INTEGRATION_LIFECYCLE=1
FASTGPT_MANAGED_INTEGRATION_BASE_URL=<candidate-base-url>
FASTGPT_MANAGED_INTEGRATION_TOKEN=<candidate-service-token>
FASTGPT_MANAGED_INTEGRATION_STORE_ID=<temporary-store-id>
go test ./internal/pkg/fastgpt -run TestGatewayManagedIntegrationLifecycle -count=1
```

该测试必须在隔离 Store 中依次创建临时 Dataset、上传文件、等待索引、执行检索、物理删除 Collection 和 Dataset，并校验最终状态。普通单元测试、mock 或只测试连通性都不能替代这份真实生命周期证据。

生产切换还必须取得 FastGPT 多租户服务负责人书面确认，完成受控单 Store 灰度和回滚演练。在门禁通过前，不修改既有 FastGPT 地址、端口、数据卷、备份或其他第三方集成。

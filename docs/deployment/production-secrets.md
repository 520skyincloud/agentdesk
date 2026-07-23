# 生产密钥与外部凭据交付手册

> 状态：B13 发布前权威操作手册。若本文与代码冲突，以
> `internal/pkg/config/config.go`、`internal/pkg/securex/aesgcm.go`、
> `internal/services/store_model_credential_service.go` 和
> `docker-compose.yml` 的真实运行链为准，并先修正文档再继续发布。

## 1. 结论

丽斯未来单门店灰度需要三类材料，不能互相替代：

1. **部署现场生成并保管的秘密**：数据库口令、邀请码密钥、会话签名密钥、资产签名密钥、门店模型凭据加密主密钥。
2. **FastGPT 服务方签发的平台集成凭据**：FastGPT Base URL 和 Integration Token。
3. **NewAPI 服务方签发的门店凭据**：为选定灰度门店准备的一条现有 NewAPI API Key。

用户不需要在聊天、Issue、PR、Markdown 或 Git 中提供任何真实密钥。真实值只能由授权人员写入目标环境的秘密管理器、权限为 `0600` 且被 Git 忽略的本地 `.env`，或通过门店凭据页面提交。

当前灰度还需要用户确认一个**非秘密信息**：丽斯未来具体使用哪一家门店作为 pilot Store。不得默认选择 Store `301`。

## 2. 丽斯未来灰度最小清单

| 项目 | 是否秘密 | 由谁产生 | 放置位置 | 当前是否必需 |
| --- | --- | --- | --- | --- |
| MySQL 应用账号密码 | 是 | 平台运维现场生成 | `AGENT_DESK_MYSQL_PASSWORD` | 是 |
| MySQL root 密码 | 是 | 平台运维现场生成 | `AGENT_DESK_MYSQL_ROOT_PASSWORD` | 使用 Compose 内置 MySQL 时是 |
| MySQL DSN | 是，包含应用密码 | 平台运维按数据库信息组装 | `AGENT_DESK_DB_DSN` | 是 |
| 邀请码 AES 密钥 | 是 | 平台运维现场生成 | `AGENT_DESK_INVITATION_ENCRYPTION_KEY` | 是 |
| 客户会话签名秘密 | 是 | 平台运维现场生成 | `AGENT_DESK_CUSTOMER_SESSION_SECRET` | 是 |
| 资产 URL 签名秘密 | 是 | 平台运维现场生成 | `AGENT_DESK_ASSET_URL_SIGNING_SECRET` | 是 |
| Store Credential 主密钥 | 是，最高敏感级 | 平台运维现场生成并独立备份 | `AGENT_DESK_STORE_MODEL_CREDENTIAL_MASTER_KEY` | 是 |
| Store Credential 主密钥版本号 | 否 | 平台运维命名 | `AGENT_DESK_STORE_MODEL_CREDENTIAL_MASTER_KEY_ID` | 是 |
| FastGPT Base URL | 否，但不对租户展示 | FastGPT 服务负责人提供 | `AGENT_DESK_FASTGPT_BASE_URL` | AI/FastGPT 灰度时是 |
| FastGPT Integration Token | 是 | FastGPT 服务负责人签发 | `AGENT_DESK_FASTGPT_INTEGRATION_TOKEN` | AI/FastGPT 灰度时是 |
| pilot Store NewAPI API Key | 是 | NewAPI 账号所有者提供 | 门店凭据页面/API，不进 `.env` | AI/NewAPI 灰度时是 |

除上述项目外，还需设置：

```text
AGENT_DESK_FASTGPT_ENABLED=true
AGENT_DESK_BACKGROUND_WORKERS_ENABLED=true
AGENT_DESK_FASTGPT_RETRIEVAL_TOKEN_LIMIT=400
```

这些是运行开关或非秘密参数，不是密钥。正式活动服务必须保持 worker 开启；只有隔离迁移、恢复演练或只读预检实例才可关闭。

## 3. 部署必需秘密

### 3.1 MySQL

使用当前 `docker-compose.yml` 内置 MySQL 时必须准备：

- `AGENT_DESK_MYSQL_PASSWORD`：应用数据库账号密码。
- `AGENT_DESK_MYSQL_ROOT_PASSWORD`：MySQL 容器初始化使用，应用不读取。
- `AGENT_DESK_DB_DSN`：应用连接串，内部包含应用数据库账号密码。

建议两个密码分别生成 32 字节随机十六进制值，避免 DSN 特殊字符转义问题：

```bash
openssl rand -hex 32
openssl rand -hex 32
```

Compose 默认数据库名和应用用户名均为 `cs_ai_agent`。DSN 结构如下，尖括号内容必须替换，不能原样保留：

```text
cs_ai_agent:<AGENT_DESK_MYSQL_PASSWORD>@tcp(mysql:3306)/cs_ai_agent?charset=utf8mb4&parseTime=True&loc=Local
```

如果使用外部 MySQL，`AGENT_DESK_DB_DSN` 仍必需；Compose 内置 MySQL 的两个初始化密码是否使用，由实际部署编排决定。DSN 不得写入 YAML、命令行参数、诊断报告或 Git。

### 3.2 `AGENT_DESK_INVITATION_ENCRYPTION_KEY`

- 格式：**恰好 32 个随机字节，再用标准 Base64 编码**。
- 生成命令：`openssl rand -base64 32`。
- 用途：加密租户公司邀请码，并参与邀请注册请求的安全校验。
- 禁止：复用客户会话、资产签名或 Store Credential 主密钥。
- 丢失后果：现有邀请码密文无法正常查看；不能直接换值后继续使用旧密文。
- 轮换边界：必须先设计邀请码重加密或统一失效并重新生成流程，不能只改环境变量。

### 3.3 `AGENT_DESK_CUSTOMER_SESSION_SECRET`

- 格式：至少 32 字节；建议生成 48 个随机字节后 Base64 编码。
- 生成命令：`openssl rand -base64 48`。
- 用途：签发和校验客户侧会话。
- 轮换后果：已有客户会话会失效，需要重新建立会话。
- 禁止：与邀请码密钥或资产签名秘密相同。

### 3.4 `AGENT_DESK_ASSET_URL_SIGNING_SECRET`

- 格式：至少 32 字节；建议生成 48 个随机字节后 Base64 编码。
- 生成命令：`openssl rand -base64 48`。
- 用途：签名受保护的资产访问 URL。
- 轮换后果：尚未过期的旧签名 URL 会立即失效。
- 禁止：与客户会话秘密或其他密钥相同。

### 3.5 `AGENT_DESK_STORE_MODEL_CREDENTIAL_MASTER_KEY`

- 格式：**恰好 32 个随机字节**；部署模板统一使用 Base64。
- 生成命令：`openssl rand -base64 32`。
- 用途：使用 AES-256-GCM 加密所有门店的 NewAPI API Key。
- AAD 绑定：Tenant、Store 和 Credential revision；密文不能跨门店或跨 revision 搬用。
- 保管级别：最高。密钥必须在数据库备份之外另行加密备份，并限制为最少授权人员可访问。
- 丢失后果：数据库中的全部门店模型凭据不可解密，只能恢复主密钥备份或要求各门店重新提交 Key。
- 当前轮换限制：运行时只接受当前 `MasterKeyID` 对应的一把主密钥，没有多密钥 keyring。禁止直接替换；轮换必须先实现并演练显式重加密迁移。

### 3.6 `AGENT_DESK_STORE_MODEL_CREDENTIAL_MASTER_KEY_ID`

- 这不是秘密，是主密钥的不可混淆版本标识。
- 示例格式：`store-credential-2026-07-v1`。
- 必须与实际主密钥版本一一对应，不能在密钥不变时随意修改，也不能更换密钥后沿用旧 ID。
- 数据库 Credential 记录会保存该 ID，用于拒绝使用错误主密钥解密。

## 4. FastGPT 平台集成凭据

### 4.1 `AGENT_DESK_FASTGPT_BASE_URL`

这是部署级 FastGPT 托管集成服务根地址，不是门店 NewAPI 网关地址，也不是普通 FastGPT 聊天应用链接。

服务必须实现 Agent Desk 托管集成契约，包括：

```text
/api/integration/agent-desk/tenant/ensure
/api/integration/agent-desk/dataset/*
/api/integration/agent-desk/usage/list
```

Agent Desk 会在根地址后拼接上述路径。生产必须使用 HTTPS，URL 不得内嵌账号或密码；生产配置预检会在连接数据库或启动 worker 前拒绝 HTTP。FastGPT 服务负责人还必须确认 Tenant + Store 隔离、Team/Dataset 归属和 Usage cursor 契约。

### 4.2 `AGENT_DESK_FASTGPT_INTEGRATION_TOKEN`

- 由 FastGPT 托管集成服务签发。
- Agent Desk 仅通过请求头 `X-Agent-Desk-Token` 使用。
- 用途：创建/确认门店 Team、管理 Dataset、同步模型 Profile、检索和读取 FastGPT Usage。
- 不得当作 NewAPI API Key，也不得发给租户或门店员工。
- 不得进入数据库、API 返回、浏览器、日志、WebSocket 或错误信息。
- 轮换时必须由 FastGPT 服务端与 Agent Desk 部署环境原子切换，再完成 Team、Dataset、检索和 Usage readiness 复验。

FastGPT Base URL 与 Integration Token 必须成对来自同一环境。测试环境 Token 不能用于生产地址，生产 Token 也不能用于生命周期测试 Store。

## 5. pilot Store NewAPI API Key

### 5.1 只需要一条门店 Key

当前系统只支持一个统一 NewAPI 网关。一个 Store 提交一条已有的 NewAPI API Key，该 Key 必须能够调用当前 active Model Profile 的全部九个用途槽：

1. Reply
2. Intent Detect
3. Memory Summary
4. Customer Tag
5. Vision
6. ASR
7. Embedding
8. Rerank
9. Document Parser

转人工二次确认复用 Intent Detect，人工通知摘要复用 Reply，不形成新的模型槽。不需要为九个槽分别提供九条 Key，也不存在平台级 NewAPI 用量 Token、租户授权池或企微账号独立 Key。

### 5.2 Key 的提交位置

NewAPI Key **不能写入 `.env`**，也不能由 Codex、脚本或 Migration 从旧 `AIConfig.APIKey` 复制。必须在选定 pilot Store 的凭据工作流中提交：

- 平台管理员或获得对应权限的管理员，可在“接入公司 -> 模型接入”中为门店录入。
- 公司主管可按 Tenant 数据范围管理。
- 门店员工只有在 `AllowCredentialSelfService=true` 时才能自行录入。
- 开启 `RequireSupervisorApproval=true` 时，门店自助提交必须由不同的公司主管审批。

每次配置、审批、拒绝或停用都要求当前账号密码、显式二次确认和不可变审计。候选 Key 会先测试九槽并同步 FastGPT；全部成功后才切换为 active。失败时保留旧 active revision，不允许半切换。

### 5.3 展示与存储

- 明文只在提交、测试、同步或模型调用所需的进程内存中短暂存在，不持久化为明文。
- 数据库只保存 AES-256-GCM 密文、nonce、revision、主密钥 ID 和指纹。
- 前端只返回 `hasKey`、状态、revision 和指纹末 6 位，不返回明文、密文、nonce 或完整指纹。
- 用户需要通过门店凭据页面自行输入真实 Key；不要把 Key 发到聊天中让我代填。

## 6. 可选集成凭据

以下项目只有启用对应功能时才需要，丽斯未来 NewAPI/FastGPT 首次灰度默认不要求。

### 6.1 邮件

启用 `AGENT_DESK_EMAIL_ENABLED=true` 后必须配置：

- `AGENT_DESK_EMAIL_HOST`
- `AGENT_DESK_EMAIL_USERNAME`
- `AGENT_DESK_EMAIL_PASSWORD`
- `AGENT_DESK_EMAIL_FROM`
- `AGENT_DESK_EMAIL_PUBLIC_URL`

其中密码是秘密，其余仍应按部署配置管理。

### 6.2 OIDC

启用 `AGENT_DESK_OIDC_ENABLED=true` 后必须配置：

- `AGENT_DESK_OIDC_ISSUER`
- `AGENT_DESK_OIDC_CLIENT_ID`
- `AGENT_DESK_OIDC_CLIENT_SECRET`
- `AGENT_DESK_OIDC_REDIRECT_URL`
- `AGENT_DESK_OIDC_STATE_SECRET`

`STATE_SECRET` 至少 32 字节，并且必须独立生成。

### 6.3 OSS

当 `storage.default=oss` 时必须配置：

- `AGENT_DESK_OSS_ACCESS_KEY_ID`
- `AGENT_DESK_OSS_ACCESS_KEY_SECRET`

Endpoint、Bucket、Base URL 等非秘密配置继续放在部署 YAML；Access Key 只从环境注入。

### 6.4 平台级企业微信登录/通知

启用 `AGENT_DESK_WXWORK_ENABLED=true` 后，生产预检至少要求：

- `AGENT_DESK_WXWORK_CORP_ID`
- `AGENT_DESK_WXWORK_CORP_SECRET`
- `AGENT_DESK_WXWORK_AGENT_ID`
- `AGENT_DESK_WXWORK_OAUTH_REDIRECT`
- `AGENT_DESK_WXWORK_STATE_SECRET`

回调或消息加解密场景还可能使用：

- `AGENT_DESK_WXWORK_RSA_PRIVATE_KEY`
- `AGENT_DESK_WXWORK_TOKEN`
- `AGENT_DESK_WXWORK_ENCODING_AES_KEY`

这组配置是平台企业微信登录/通知身份，不是 Store 的企业微信员工号协议凭据。后者必须继续按 `wework.apifox.cn` 协议和现有账号绑定流程管理，禁止混用。

## 7. 明确不需要、禁止复用的旧凭据

本次不得提供或恢复：

- 旧 `AIConfig.APIKey`
- `TenantAIModelGrant` 或 StoreSetting 的旧授权 Key
- 平台级 NewAPI Access Token、User ID 或用量 Token
- FastGPT `TokenName` 统计身份
- Qdrant、本地 FAQ、旧 Agent 或旧 hook bridge 凭据
- 企微员工号以外的旧微信协议字段

即使旧库中存在这些值，也必须视为历史风险并轮换，不能迁入新运行链。

## 8. 安全生成与注入

### 8.1 本地 Compose

只在目标部署主机执行：

```bash
umask 077
cp .env.example .env
chmod 600 .env
```

然后使用秘密管理器的生成器，或在受控终端分别执行前述 `openssl rand` 命令，把每次生成的不同值填入 `.env`。不要把一条随机值复制到多个变量。

填写后只运行不会输出插值内容的校验：

```bash
docker compose config --quiet
go test ./internal/pkg/config/... -count=1
```

禁止运行 `docker compose config` 后把完整输出贴到聊天或 CI 日志，因为非 `--quiet` 模式会展开环境变量。

也可以把生产环境文件保存在仓库外，避免在仓库根目录产生 `.env`。安全文件和父目录分别使用 `0600`、`0700`，并通过绝对路径显式传入：

```bash
docker compose --env-file "/absolute/secure/path/production.env" config --quiet
docker compose --env-file "/absolute/secure/path/production.env" up -d --build
```

不得把仓库外安全文件复制进 Git 工作树，也不得在命令中逐项展开真实值。

### 8.2 生产秘密管理器

生产优先使用部署平台的秘密管理器。每条记录至少保存以下非秘密元数据：

- 变量名或凭据类型
- 所属环境
- 所有者
- 创建时间
- 版本号
- 最近验证时间
- 下次轮换窗口
- 恢复保管人

不得在交接文档中保存真实值、密文、nonce、完整指纹或秘密管理器的可直接访问链接。

## 9. 轮换和事故处理

| 凭据 | 可否直接替换 | 必需动作 |
| --- | --- | --- |
| MySQL 应用密码 | 可，需协调 | 先改数据库账号，再原子更新 DSN/环境并重启验证 |
| MySQL root 密码 | 由数据库流程决定 | 不影响应用 DSN，但必须同步运维保管 |
| 邀请码密钥 | 不可直接替换 | 先重加密或统一轮换现有邀请码 |
| 客户会话秘密 | 可，但会中断会话 | 安排维护窗口并接受全部客户会话失效 |
| 资产签名秘密 | 可，但旧 URL 失效 | 安排窗口并重新签发需要继续访问的 URL |
| Store Credential 主密钥 | 当前不可直接替换 | 先实现并演练显式重加密迁移，或要求门店重录 Key |
| FastGPT Integration Token | 可，需双端原子切换 | 切换后复验 Team/Dataset/Profile/检索/Usage |
| Store NewAPI API Key | 通过凭据工作流替换 | 候选测试、FastGPT 同步、审批和 active revision 切换 |

任何密钥疑似泄露时，先停止相关新写入或外呼，保留不可变审计，再按上表轮换。不得通过删除审计、修改历史 revision 或恢复旧 Key 来“消除”事故。

## 10. B13 现场交付顺序

1. 用户只确认 pilot Store 的名称或 ID，以及该 Store 是否允许凭据自助、是否需要主管审批。
2. 平台运维在目标环境生成并保管部署秘密，不在聊天中传值。
3. FastGPT 负责人通过受控渠道交付 Base URL 和 Integration Token。
4. 启动隔离预检实例，验证生产配置门禁、登录、权限和旧 API `404`。
5. 平台发布完整九槽 Model Profile，并指派给 pilot Store。
6. NewAPI Key 所有者在门店凭据页面提交 Key，完成九槽测试、FastGPT 同步和审批。
7. 完成真实 FastGPT 检索、AI 回复、转人工、规则派单、行业标签、Request ID 人民币账单和备份恢复证据。
8. 全部 readiness 通过前，不切换正式 `8083`，不执行 B14 物理删表。

B14 的固定白名单、三阶段命令、一次性令牌和失败恢复要求见
[`docs/deployment/b14-schema-cleanup.md`](b14-schema-cleanup.md)。该工具可随发布镜像构建，但当前
`No-Go` 未解除前只能运行 `inspect`，不得运行生产 `prepare` 或 `execute`。

# 生产密钥与外部凭据交付手册

> 状态：B13 发布前权威操作手册。若本文与代码冲突，以
> `internal/pkg/config/config.go`、`internal/pkg/securex/aesgcm.go`、
> `internal/services/store_model_credential_service.go` 和
> `docker-compose.yml` 的真实运行链为准，并先修正文档再继续发布。

## 1. 结论

统一环境上线需要三类材料，不能互相替代：

1. **部署现场生成并保管的秘密**：数据库口令、邀请码密钥、会话签名密钥、资产签名密钥、门店模型凭据加密主密钥。
2. **FastGPT 服务方签发的平台集成凭据**：FastGPT Base URL 和 Integration Token。
3. **NewAPI 服务方签发的员工绑定凭据**：为 pilot Store 中每个启用 AI 的 `StoreStaffBinding` 分别准备一条 NewAPI API Key。

用户不需要在聊天、Issue、PR、Markdown 或 Git 中提供任何真实密钥。真实值只能由授权人员写入目标环境的秘密管理器、权限为 `0600` 且被 Git 忽略的本地 `.env`，或通过门店凭据页面提交。

代码合并不需要任何真实 Tenant、Store、来源数据库或外部 Key。正式部署既支持 fresh
SQLite/MySQL，也支持从已经执行到已知 Migration 版本的现有数据库受控升级。旧库升级必须
先完成 B13：停写、加密仓库外备份、下载到独立位置、恢复到独立 MySQL、在恢复库运行
AutoMigrate 与 Migration 72，并核对记录数量和关键关系。任何存在歧义的 Store、Binding、
实例、会话或 Credential 归属都会阻止迁移，禁止猜测或硬编码历史 Store ID。

测试 Store 的 Credential 策略由部署方在页面设置。一个 Store 可以有多个活动
`StoreStaffBinding`，每个 Binding 独立保存 Credential revision 和计费归因。启用
`AllowCredentialSelfService=true` 时，仅允许该 Binding 当前唯一绑定且持有权限的
`store_staff` 为自己的 Binding 录入；启用 `RequireSupervisorApproval=true` 时，必须由
同 Tenant、不同于提交人的公司主管审批。

历史交付过的环境文件摘要、测试 HTTP 端点和测试 Key 只作安全事件追溯，不能作为当前
部署输入。目标主机必须重新生成或受控注入全部秘密，验证权限、HTTPS、Compose 解析和
外部连通性。旧 `AIConfig.APIKey` 等退出运行链的明文不得迁移；现有统一 Credential 密文、
nonce、revision 和真实业务数据则必须在 B13 恢复演练中原样保全，并由 Migration 72 按确定
证据归属到 Binding。

## 2. pilot Store 灰度最小清单

| 项目 | 是否秘密 | 由谁产生 | 放置位置 | 当前是否必需 |
| --- | --- | --- | --- | --- |
| MySQL 应用账号密码 | 是 | 平台运维现场生成 | `AGENT_DESK_MYSQL_PASSWORD` | 是 |
| MySQL root 密码 | 是 | 平台运维现场生成 | `AGENT_DESK_MYSQL_ROOT_PASSWORD` | 使用 Compose 内置 MySQL 时是 |
| MySQL DSN | 是，包含应用密码 | 平台运维按数据库信息组装 | `AGENT_DESK_DB_DSN` | 是 |
| 初始超级管理员用户名 | 否 | 平台运维指定 | `AGENT_DESK_BOOTSTRAP_ADMIN_USERNAME` | fresh 初始化时是 |
| 初始超级管理员密码 | 是 | 平台运维受控注入 | `AGENT_DESK_BOOTSTRAP_ADMIN_PASSWORD` | fresh 初始化时是 |
| 邀请码 AES 密钥 | 是 | 平台运维现场生成 | `AGENT_DESK_INVITATION_ENCRYPTION_KEY` | 是 |
| 客户会话签名秘密 | 是 | 平台运维现场生成 | `AGENT_DESK_CUSTOMER_SESSION_SECRET` | 是 |
| 资产 URL 签名秘密 | 是 | 平台运维现场生成 | `AGENT_DESK_ASSET_URL_SIGNING_SECRET` | 是 |
| Store Credential 主密钥 | 是，最高敏感级 | 平台运维现场生成并独立备份 | `AGENT_DESK_STORE_MODEL_CREDENTIAL_MASTER_KEY` | 是 |
| Store Credential 主密钥版本号 | 否 | 平台运维命名 | `AGENT_DESK_STORE_MODEL_CREDENTIAL_MASTER_KEY_ID` | 是 |
| FastGPT Base URL | 否，但不对租户展示 | FastGPT 服务负责人提供 | `AGENT_DESK_FASTGPT_BASE_URL` | AI/FastGPT 灰度时是 |
| FastGPT Integration Token | 是 | FastGPT 服务负责人签发 | `AGENT_DESK_FASTGPT_INTEGRATION_TOKEN` | AI/FastGPT 灰度时是 |
| pilot Binding NewAPI API Key | 是 | NewAPI 账号所有者提供 | 员工绑定凭据页面/API，不进 `.env` | 对每个启用 AI 的 Binding 是 |

除上述项目外，还需设置：

```text
AGENT_DESK_FASTGPT_ENABLED=true
AGENT_DESK_BACKGROUND_WORKERS_ENABLED=true
AGENT_DESK_FASTGPT_RETRIEVAL_TOKEN_LIMIT=400
```

这些是运行开关或非秘密参数，不是密钥。正式活动服务必须保持 worker 开启；只有隔离迁移、恢复演练或只读预检实例才可关闭。

## 3. 部署必需秘密

### 3.1 初始超级管理员

fresh 数据库第一次执行 Migration 2 时，可通过以下环境变量指定平台超级管理员：

```text
AGENT_DESK_BOOTSTRAP_ADMIN_USERNAME
AGENT_DESK_BOOTSTRAP_ADMIN_PASSWORD
```

二者只从进程环境读取，不接受 YAML 配置，也不得写入源码、Migration、镜像层或交接文档。
密码进入数据库前使用 bcrypt 哈希，接口和日志不得返回明文。变量只控制尚未执行 Migration 2
的 fresh 数据库；不会在重启时覆盖已经存在的管理员账号或密码。

### 3.2 MySQL

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

### 3.3 `AGENT_DESK_INVITATION_ENCRYPTION_KEY`

- 格式：**恰好 32 个随机字节，再用标准 Base64 编码**。
- 生成命令：`openssl rand -base64 32`。
- 用途：加密租户公司邀请码，并参与邀请注册请求的安全校验。
- 禁止：复用客户会话、资产签名或 Store Credential 主密钥。
- 丢失后果：现有邀请码密文无法正常查看；不能直接换值后继续使用旧密文。
- 轮换边界：必须先设计邀请码重加密或统一失效并重新生成流程，不能只改环境变量。

### 3.4 `AGENT_DESK_CUSTOMER_SESSION_SECRET`

- 格式：至少 32 字节；建议生成 48 个随机字节后 Base64 编码。
- 生成命令：`openssl rand -base64 48`。
- 用途：签发和校验客户侧会话。
- 轮换后果：已有客户会话会失效，需要重新建立会话。
- 禁止：与邀请码密钥或资产签名秘密相同。

### 3.5 `AGENT_DESK_ASSET_URL_SIGNING_SECRET`

- 格式：至少 32 字节；建议生成 48 个随机字节后 Base64 编码。
- 生成命令：`openssl rand -base64 48`。
- 用途：签名受保护的资产访问 URL。
- 轮换后果：尚未过期的旧签名 URL 会立即失效。
- 禁止：与客户会话秘密或其他密钥相同。

### 3.6 `AGENT_DESK_STORE_MODEL_CREDENTIAL_MASTER_KEY`

- 格式：**恰好 32 个随机字节**；部署模板统一使用 Base64。
- 生成命令：`openssl rand -base64 32`。
- 用途：使用 AES-256-GCM 加密所有门店员工绑定的 NewAPI API Key。
- AAD 绑定：Tenant、Store、StoreStaffBinding 和 Credential revision；密文不能跨租户、门店、Binding 或 revision 搬用。
- 保管级别：最高。密钥必须在数据库备份之外另行加密备份，并限制为最少授权人员可访问。
- 丢失后果：数据库中的全部门店模型凭据不可解密，只能恢复主密钥备份或要求各门店重新提交 Key。
- 当前轮换限制：运行时只接受当前 `MasterKeyID` 对应的一把主密钥，没有多密钥 keyring。禁止直接替换；轮换必须先实现并演练显式重加密迁移。

### 3.7 `AGENT_DESK_STORE_MODEL_CREDENTIAL_MASTER_KEY_ID`

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

Agent Desk 会在根地址后拼接上述路径。生产默认必须使用 HTTPS，URL 不得内嵌账号或密码；生产配置预检会在连接数据库或启动 worker 前拒绝 HTTP。仅当已明确批准直连 HTTP 集成端点时，才可同时设置 `AGENT_DESK_FASTGPT_ALLOW_INSECURE_HTTP=true`。该开关只放宽 FastGPT Base URL 校验，不会创建回退地址或双写连接。FastGPT 服务负责人还必须确认 Tenant + Store 隔离、Team/Dataset 归属和 Usage cursor 契约。

### 4.2 `AGENT_DESK_FASTGPT_INTEGRATION_TOKEN`

- 由 FastGPT 托管集成服务签发。
- Agent Desk 仅通过请求头 `X-Agent-Desk-Token` 使用。
- 用途：创建/确认门店 Team、管理 Dataset、同步模型 Profile、检索和读取 FastGPT Usage。
- 不得当作 NewAPI API Key，也不得发给租户或门店员工。
- 不得进入数据库、API 返回、浏览器、日志、WebSocket 或错误信息。
- 轮换时必须由 FastGPT 服务端与 Agent Desk 部署环境原子切换，再完成 Team、Dataset、检索和 Usage readiness 复验。

FastGPT Base URL 与 Integration Token 必须成对来自同一环境。测试环境 Token 不能用于生产地址，生产 Token 也不能用于生命周期测试 Store。

## 5. pilot Binding NewAPI API Key

### 5.1 每个启用 AI 的 Binding 一条 Key

当前系统只支持一个统一 NewAPI 网关。Store 负责行业、知识、标签、记忆和 active Model
Profile；`StoreStaffBinding` 负责 NewAPI Credential、模型调用证据和账单归因。每个启用 AI
的 Binding 提交一条已有的 NewAPI API Key，该 Key 必须能够调用 Store 当前 active Model
Profile 的全部九个用途槽：

1. Reply
2. Intent Detect
3. Memory Summary
4. Customer Tag
5. Vision
6. ASR
7. Embedding
8. Rerank
9. Document Parser

转人工二次确认复用 Intent Detect，人工通知摘要复用 Reply，不形成新的模型槽。同一个
Binding 不需要为九个槽分别提供九条 Key；同一 Store 的不同 Binding 不能共用数据库中的
Credential 记录。Credential 也不直接属于可替换的企微实例，因此同一 Binding 更换企微账号
时继续使用原 Credential。系统不存在平台级 NewAPI 用量 Token或租户授权池。

### 5.2 Key 的提交位置

NewAPI Key **不能写入 `.env`**，也不能由 Codex、脚本或 Migration 从旧 `AIConfig.APIKey`
复制。必须在选定 pilot Store 的 Binding 凭据工作流中提交：

- 平台管理员或获得对应权限的管理员，可在“接入公司 -> 模型接入”中为指定 Binding 录入。
- 公司主管可按 Tenant 数据范围管理。
- 门店员工只有在 `AllowCredentialSelfService=true` 时才能为自己的活动 Binding 自行录入。
- 开启 `RequireSupervisorApproval=true` 时，门店自助提交必须由不同的公司主管审批。

每次配置、审批、拒绝或停用都要求当前账号密码、显式二次确认和不可变审计。候选 Key 会先测试九槽并同步 FastGPT；全部成功后才切换为 active。失败时保留旧 active revision，不允许半切换。

### 5.3 展示与存储

- 明文只在提交、测试、同步或模型调用所需的进程内存中短暂存在，不持久化为明文。
- 数据库只保存 AES-256-GCM 密文、nonce、revision、主密钥 ID 和指纹。
- 前端只返回 `hasKey`、状态、revision 和指纹末 6 位，不返回明文、密文、nonce 或完整指纹。
- 用户需要通过门店凭据页面自行输入真实 Key；不要把 Key 发到聊天中让我代填。

## 6. 可选集成凭据

以下项目只有启用对应功能时才需要，本次 NewAPI/FastGPT pilot 默认不要求。

### 6.1 邮件

启用 `AGENT_DESK_EMAIL_ENABLED=true` 后必须配置：

- `AGENT_DESK_EMAIL_HOST`
- `AGENT_DESK_EMAIL_PORT`
- `AGENT_DESK_EMAIL_TLS_MODE`
- `AGENT_DESK_EMAIL_USERNAME`
- `AGENT_DESK_EMAIL_PASSWORD`
- `AGENT_DESK_EMAIL_FROM`
- `AGENT_DESK_EMAIL_PUBLIC_URL`

其中密码是秘密，其余仍应按部署配置管理。`AGENT_DESK_EMAIL_TLS_MODE` 只接受
`tls`、`starttls` 或 `plain`：465 端口的隐式 TLS 服务应使用 `tls`，587 端口通常使用
`starttls`。

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

### 6.5 到店联动与企业微信服务商

到店联动默认关闭。只有正式 HTTPS 域名、小程序合法域名，以及所选 Provider 的真实前置
条件都完成后，才能设置：

```text
AGENT_DESK_ARRIVAL_ENABLED=true
AGENT_DESK_ARRIVAL_PUBLIC_BASE_URL=https://weibao.omnireva.com
AGENT_DESK_ARRIVAL_CONTACT_PROVIDER=static_plugin_ticket
AGENT_DESK_ARRIVAL_BIND_TICKET_TTL_MINUTES=30
AGENT_DESK_ARRIVAL_BIND_PENDING_SCAN_WINDOW_MINUTES=30
```

三种模式都必须由秘密管理设施独立生成或注入：

- `AGENT_DESK_MINIPROGRAM_APP_SECRET`
- `AGENT_DESK_ARRIVAL_SESSION_SECRET`
- `AGENT_DESK_ARRIVAL_IDENTITY_HMAC_KEY`
- `AGENT_DESK_ARRIVAL_DATA_MASTER_KEY`

只有 `customer_acquisition`、`contact_way` 服务商模式需要：

- `AGENT_DESK_WECOM_AUTH_TYPE=<1 during installation testing; 0 after formal publication>`
- `AGENT_DESK_WECOM_SUITE_SECRET`
- `AGENT_DESK_WECOM_PROVIDER_CALLBACK_TOKEN`
- `AGENT_DESK_WECOM_PROVIDER_ENCODING_AES_KEY`

`AGENT_DESK_WECOM_AUTH_TYPE` 是服务商模式的非秘密企业微信应用阶段配置：企业微信后台处于“安装测试”
阶段时固定为 `1`，正式发布后固定为 `0`。应用只接受这两个整数；其他值会阻止启动。切换
阶段必须修改仓库外生产环境文件并强制重建应用容器，普通 restart 不会重新加载环境变量。

其中 `AGENT_DESK_ARRIVAL_DATA_MASTER_KEY` 是独立的 32 字节 base64 主密钥，用于小程序身份、
CorpID、永久授权码、企业 token、外部联系人和协议会话等 Arrival 数据；不得复用 Store
模型凭据主密钥。`AGENT_DESK_ARRIVAL_DATA_MASTER_KEY_ID` 是非秘密标识，但必须明确配置，
用于后续轮换和审计。

以下属于非秘密部署配置：

- `AGENT_DESK_MINIPROGRAM_APP_ID`
- `AGENT_DESK_ARRIVAL_CONTACT_PROVIDER`
- `AGENT_DESK_ARRIVAL_WECHAT_API_BASE_URL`
- session、邀请、联系码和投递频控时长

服务商模式另需非秘密的 `AGENT_DESK_WECOM_SUITE_ID`、
`AGENT_DESK_ARRIVAL_WECOM_API_BASE_URL` 和
`AGENT_DESK_ARRIVAL_QR_ALLOWED_HOST_SUFFIXES`。

生产预检要求公开地址及当前模式实际使用的上游 API 为有效 HTTPS，拒绝 IP、localhost
和明文 HTTP。
二维码来源白名单只能包含经确认的企业微信官方资源域名，不能加入通配公网域或用户输入。
Provider 只允许 `static_plugin_ticket`、`customer_acquisition` 或 `contact_way`。静态
模式只要求每店真实 `plugId` 和唯一员工实例，不要求 Suite 配置；另两种模式继续要求
完整服务商配置。禁止根据企业微信错误自动切换 Provider。

服务商模式的企业微信后台按以下固定路径配置：

```text
数据回调 URL：https://weibao.omnireva.com/api/third/wecom/provider/data-callback
指令回调 URL：https://weibao.omnireva.com/api/third/wecom/provider/command-callback
应用设置 URL：https://weibao.omnireva.com/wecom/provider/settings
```

回调 Token、EncodingAESKey、suite secret、小程序 AppSecret、suite ticket、永久授权码和访问
token 不得进入 URL、前端、普通日志、审计明细或文档。完整运行边界见
`docs/design/arrival-link-engine.md`。

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

### 8.3 代码合并与生产验证延期

代码合并与生产现场验收是两个门禁：

- `production.env`、真实 Key 和客户数据不属于源码或 PR 交付物；缺少它们不阻止代码评审、测试和合并。
- 文件权限、变量完整性、Compose 解析、FastGPT HTTPS/鉴权和目标数据库连通性只能在实际部署主机验证。
- 未完成现场验证时不得切换正式 `8083`、启动生产 worker 或宣称生产验收完成。
- fresh 与已知历史库受控升级均受支持；历史库必须先通过 B13 加密备份、独立恢复和 Migration 72 演练。
- B14 物理删除是独立操作，只能在 B13 全部验收后按已批准白名单执行，当前部署不得顺带扩大删除范围。
- 真实变量值和 NewAPI Key 不因延期改由聊天、Git、PR、Markdown、Migration 或命令行参数交付。NewAPI Key 仍由实际持有人在统一门店凭据页面重新录入。

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

## 10. 现场交付顺序

1. 在目标主机生成或受控注入安全文件，确认父目录 `0700`、文件 `0600`，且不回显变量值。
2. 运行不展开秘密的 Compose 与生产配置预检，确认 FastGPT Base URL 为同环境 HTTPS、
   Integration Token 成对，目标 MySQL 可连接且 Migration 历史属于受支持基线。
3. fresh 环境可直接启动统一镜像；历史环境必须先停写并完成 B13 加密备份、异机保存、独立
   MySQL 恢复及新镜像迁移演练，核对通过后再进入维护窗口。
4. 为当前生产镜像建立可回滚标签，停止旧应用和 worker，再以同一安全环境文件
   `--force-recreate` 启动统一镜像，让 AutoMigrate 与 DML runner 完成 Schema 与 DML 升级。
5. 登录平台，确认原 Tenant、Store、用户、Binding、企微实例、客户、会话和消息均保留；
   fresh 环境则通过现有页面分别创建 Store 和用户，再显式建立一个 Store 到多个 Binding。
6. 配置 Store 自助录入/主管审批策略，发布完整九槽 Model Profile 并指派给 Store。
7. NewAPI Key 实际持有人在指定 Binding 的凭据页面提交 Key；需要审批时由不同公司主管批准。
8. 完成多 Binding、同 Binding 更换企微账号自动分段、跨 Binding 人工继承、九槽测试、
   FastGPT provision/sync、真实检索、AI 回复、转人工、规则派单、行业
   标签及 Request ID 人民币账单证据。
9. 确认健康检查、Migration 72、企业微信回调和 worker 正常后切换正式 `8083`；保留 B13
   备份和旧镜像直到观察期结束。

B14 只负责已批准旧表的物理删除，不属于常规启动或 Migration 72。只有 B13 全部验收、停机、
加密备份和独立恢复验证通过后才能单独执行；不得通过手工删表或扩大白名单绕过门禁。

## 11. 当前交付结论与剩余现场材料

代码仓库、测试和 PR 不需要接收真实 Key 或客户数据。正式部署现场仍需：

| 材料 | 是否秘密 | 交付方式 |
| --- | --- | --- |
| fresh 或受支持历史 SQLite/MySQL 目标 | DSN 是秘密 | 目标主机秘密管理器或 `0600` 环境文件 |
| FastGPT 同环境 HTTPS Base URL | 否 | 部署配置 |
| FastGPT Integration Token | 是 | 目标主机秘密管理器或 `0600` 环境文件 |
| 测试 Binding NewAPI API Key | 是 | 实际持有人在指定 Binding 凭据页面录入 |
| 当前统一数据库备份材料 | 是，运维级 | 仓库外生成、保管并恢复演练 |

旧 `AIConfig.APIKey`、历史测试 HTTP 地址与聊天中出现过的 Key 应视为已暴露并轮换，不能
写入新环境。现有业务库中的 Store ID、Binding、统一 Credential revision 和会话历史属于
受保护业务数据，必须由迁移基于当前环境事实解析和保留，不能人工重编号。正式现场验证未完成
只影响发布结论，不影响当前统一代码完成评审和合并。

# AgentDesk

[English](README.md) | 简体中文

开源的 AI Agent 客服系统，支持知识库问答、人工接管、工单闭环和私有化部署。

> 面向需要同时处理在线咨询、知识库问答、人工协同和服务跟踪的团队。它不是把 LLM 接进聊天框，而是一套围绕客服场景设计的 AI Helpdesk 基础系统。

## 产品预览

租户接入、门店运营、客户会话、规则派单、托管知识和 AI 回复都在同一套系统中完成。

### 客户侧在线咨询

![客户侧在线咨询](screenshots/1.png)

客户可以在 Web 聊天页中直接发起咨询。AI Agent 会先接待，基于知识库回答问题；当用户明确要求人工介入时，会触发转人工确认流程。

### 客服工作台

![客服工作台](screenshots/2.png)

客服工作台支持会话列表、消息处理、AI 转人工、客服回复、按门店隔离的客户标签、关联客户和工单信息查看，适合客服日常接待使用。

### 托管知识与模型 Profile

每家门店只使用一个托管 FastGPT 数据集。平台通过统一 NewAPI 网关发布完整九槽模型 Profile，并为门店指派一个生效 revision；门店凭据始终脱敏、按 revision 管理且可独立审计。

## 为什么选择它

- **AI 先接待**：让 AI Agent 优先处理常见问题、标准流程和知识库问答。
- **知识约束回答**：通过 RAG 和 Answerability Gate 判断知识片段是否足以回答，减少超出知识库范围的乱答。
- **自然转人工**：当知识库不足、用户明确要求或流程需要人工确认时，进入人工接管。
- **会话到工单闭环**：在线会话、客服接待、工单创建、状态流转和处理记录在同一套系统里完成。
- **适合二次开发**：后端使用 Go，前端使用 Next.js，支持 Skills、MCP 和 OpenAI-compatible 模型接入。
- **可私有化部署**：支持 SQLite / MySQL、托管 FastGPT 知识服务和统一 NewAPI 网关，适合企业部署。

## 核心能力

- **AI 回复运行时**：行业意图识别、回复规划、知识检索、结果校验、确认、工具调用和人工协同统一由一套回复引擎完成。
- **在线会话系统**：支持访客会话、消息收发、未读状态、会话分配、转接和关闭。
- **客服工作台**：客服可接管会话、回复用户、转接同事、关联客户和创建工单。
- **托管知识库 RAG**：支持按租户和门店隔离的 FastGPT 数据集、文件同步、检索日志和可回答性分析。
- **Answerability Gate**：判断检索内容是否足以支撑回答，不足时返回兜底提示并建议联系人工。
- **工单系统**：支持从会话创建工单、分类、指派、状态流转、进展记录和闭环处理。
- **客服组织管理**：支持客服档案、客服组、排班和自动分配能力。
- **AI 扩展能力**：支持 Skills、MCP 调试和外部工具接入。
- **多入口接入**：提供管理后台、客服工作台、客户侧 Web 页面和嵌入式 SDK。

## 适用场景

- 官网在线客服
- SaaS 产品支持
- AI + 人工混合接待
- 企业内部服务台
- 售后、报障、投诉和运营支持
- 需要知识库问答与人工协同的客服团队

## 快速开始

推荐先用 Docker Compose 体验完整服务：

```bash
cp .env.example .env
# 填完 .env 中所有必填空值，并限制文件访问权限。
chmod 600 .env
docker compose config --quiet
docker compose up -d --build
```

Compose 会在数据库、邀请码、客户会话、文件签名或门店凭据加密密钥缺失时拒绝启动。运行备份与 `.env` 必须保存在 Git 仓库之外。变量格式、生成方式、保管责任、轮换限制以及 FastGPT Token 与门店 NewAPI Key 的区别，见[生产密钥与外部凭据交付手册](docs/deployment/production-secrets.md)。

正式活动服务保持 `AGENT_DESK_BACKGROUND_WORKERS_ENABLED=true`。只有隔离的历史库迁移、恢复演练或只读预检实例才设置为 `false`，防止副本派发会话、消费协议 outbox、执行 FastGPT 任务或运行其他定时任务。

完整英文配置与排查说明见 [Docker Compose Quick Start](https://agent-desk.huabei.pro/zh/docs/getting-started/docker-compose.html)。

如需在官网或产品中嵌入客服入口，见 [Web Widget Integration](https://agent-desk.huabei.pro/zh/docs/integration/web-widget.html)。

Compose 默认会启动：

- `agent-desk`：应用服务，端口 `8083`
- `mysql`：MySQL 8.4，数据卷 `mysql-data`

启动后访问：

- 管理后台：`http://localhost:8083/dashboard`
- 客服工作台：`http://localhost:8083/dashboard/conversations`
- 客户侧 Web 接入示例：`http://localhost:8083/support/demo`
- 客户侧聊天页：`http://localhost:8083/support/chat`

默认管理员账号：

- 用户名：`admin`
- 密码：`ChangeMe123!`

> 首次用于公网或团队环境前，请务必修改默认管理员密码，并配置独立的鉴权、会话和模型密钥。

## 本地开发

### 环境要求

- Go `1.26+`
- Node.js `20+`
- `pnpm`

### 准备配置

```bash
cp config/config.example.yaml config/config.yaml
```

默认配置使用：

- SQLite：`data/app.db`
- Backend：`http://127.0.0.1:8083`
- 托管 FastGPT 使用部署配置和仅环境变量注入的 Integration Token。
- NewAPI 模型 Profile 由平台管理员维护，每个门店使用自己的加密 API Key。

安装前端依赖：

```bash
cd web
pnpm install
cd ..
```

同时启动后端和前端开发服务：

```bash
make dev
```

或分别启动：

```bash
make run-go
make web-dev
```

开发环境默认入口：

- 管理后台：`http://localhost:3000/dashboard`
- 客服工作台：`http://localhost:3000/dashboard/conversations`
- 客户侧 Web 接入示例：`http://localhost:3000/support/demo`
- 客户侧聊天页：`http://localhost:3000/support/chat`

## 技术栈

- Backend：Golang + Gin + GORM + `github.com/mlogclub/simple`
- Frontend：Next.js 16 + React 19 + shadcn/ui + Tailwind CSS
- Database：SQLite / MySQL
- Knowledge Service：托管 FastGPT
- AI：统一 NewAPI 网关 + OpenAI-compatible 模型 + RAG + Skills + MCP

## 项目结构

```text
.
├── cmd/                    # server / migration / generator / testdata
├── internal/
│   ├── bootstrap/          # 启动、路由、数据库和迁移初始化
│   ├── builders/           # model / 聚合结果到 response DTO 的映射
│   ├── handlers/           # dashboard / api / third HTTP handlers
│   ├── middleware/         # Gin middleware
│   ├── migration/          # 幂等数据迁移
│   ├── models/             # GORM models
│   ├── repositories/       # 数据访问层
│   ├── services/           # 业务编排和事务边界
│   ├── ai/                 # LLM / RAG / Runtime / Skills / MCP
│   └── pkg/                # config / dto / enums / httpx / utils 等基础包
├── web/                    # Next.js 前端工程
│   ├── app/dashboard/      # 管理后台与客服工作台
│   ├── app/support/        # 客户侧接入和聊天页面
│   ├── components/         # React 组件
│   ├── lib/                # API client、SDK 源码和工具函数
│   └── public/sdk/         # 构建后的嵌入式 SDK
├── config/                 # 配置文件
├── docker/                 # Docker 配置
└── docs/                   # 项目文档
```

## 常用命令

```bash
make dev            # 同时启动后端和前端开发服务
make run            # 构建前端 SPA 后启动后端
make run-go         # 启动后端，自动确保 SPA 已构建
make web-dev        # 启动前端开发服务
make build          # 构建前端 SPA 和当前平台 Go 二进制
make build-linux    # 构建 linux/amd64 二进制
make release        # 构建常用平台二进制
make web-build-spa  # 构建 web 静态 SPA 和嵌入式 SDK
make test           # 运行 Go 测试，自动确保 SPA 已构建
make check          # 运行 Go 测试、前端 typecheck 和 lint
make generator      # 执行代码生成
make enums          # 生成前端枚举
make migration      # 执行 migration
make testdata       # 初始化演示/测试数据
```

## AI Agent 工作流

```mermaid
flowchart TD
    A[用户发起咨询<br/>Web 客服入口 / Open API] --> B[创建或匹配会话]
    B --> C[客户发送消息]
    C --> D[触发 AI Reply Runtime]
    D --> E[加载会话历史 / 租户行业 / 门店模型 Profile]
    E --> F[检索当前门店 FastGPT 数据集]
    F --> G{知识片段是否足以回答?}
    G -- 否 --> Z[返回知识库兜底提示<br/>并建议联系人工客服]
    G -- 是 --> H[准备 Skills / MCP Tools]
    H --> I[将可信知识上下文交给 Agent]
    I --> J{直接回复?}
    J -- 是 --> K[LLM 基于知识生成回复并返回用户]
    J -- 否 --> N{是否调用 Graph / MCP Tool?}
    N -- 是 --> O[执行 Skill / Graph / MCP Tool]
    O --> P{需要用户确认?}
    P -- 否 --> I
    P -- 是 --> Q[向用户发起确认]
    Q --> R{用户确认结果}
    R -- 确认转人工 --> S[会话转人工并进入待接入池]
    S --> T[自动分配或人工分配]
    T --> U[客服工作台接管]
    U --> V{是否需要工单跟踪?}
    V -- 是 --> W[创建或关联工单]
    V -- 否 --> X[人工继续处理]
    W --> X
    X --> Y[问题解决并关闭]
    R -- 确认建单 --> AA[从当前会话创建工单]
    AA --> I
    R -- 取消 --> K
    N -- 否 --> K
```

## 业务闭环

```mermaid
flowchart LR
    A[客户咨询] --> B[AI Agent 接待]
    B --> C{知识库可回答?}
    C -- 是 --> D[AI 基于可信知识回复]
    C -- 否 --> E[兜底提示 / 建议人工]
    D --> F{是否需要人工?}
    E --> G[人工接管]
    F -- 否 --> H[会话结束或沉淀数据]
    F -- 是 --> G
    G --> I[客服工作台处理]
    I --> J{是否需要跟踪?}
    J -- 是 --> K[创建 / 关联工单]
    J -- 否 --> L[直接解决]
    K --> M[工单流转与进展记录]
    M --> N[处理完成]
    L --> N
```

## Docker 镜像

如果只需要构建应用镜像，可以自行准备 MySQL 和已配置的外部 AI 服务，并挂载配置文件：

```bash
docker build -t mlogclub/agent-desk .
docker run --rm -p 8083:8083 --env-file .env \
  -e APP_ENV=production -e AGENT_DESK_ENV=production \
  -v $(pwd)/docker/agent-desk.yaml:/app/config/config.yaml:ro \
  -v agent-desk-data:/app/data \
  mlogclub/agent-desk
```

Compose 仅使用 [docker/agent-desk.yaml](docker/agent-desk.yaml) 保存非敏感设置；所有部署秘密来自被忽略的 `.env` 或生产秘密管理器。NewAPI 调用与账单查询只使用各门店通过凭据工作流提交的加密凭据，不存在平台级 NewAPI 用量 Token，也不得把门店 Key 写入 `.env`。

禁止在后台 worker 开启时启动历史数据库副本。迁移和 readiness 预检必须设置 `AGENT_DESK_BACKGROUND_WORKERS_ENABLED=false`，并在完成验收前隔离外部网络。

## 开源定位

`AgentDesk` 适合作为以下方向的开源基础项目：

- AI 客服系统
- AI Helpdesk / AI Support Platform
- RAG 可回答性判定 + Human Handoff 的落地样板
- 面向企业场景的 AI Agent 应用框架

如果你在寻找一个以 AI Agent 为中心，而不是仅仅把 LLM 嵌进聊天框的客服系统，这个项目就是为此设计的。

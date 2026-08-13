# 回复动作目录（Reply Action Catalog）设计方案

> 状态：待确认（方案评审稿）
>
> 日期：2026-08-13
>
> 分支：`codex/deepseek-responses-schema`
>
> 目标：把散落在各处的「动作」（转人工、发定位、发小程序、建工单、查天气）收敛成一个**统一、带开关、可扩展**的动作目录；让「知识库答案 → 执行动作」这条路结构化走通，而不是让模型口头复述。

---

## 0. 用大白话说清要解决什么

现在 AI 客服背后有一堆"能干的事"：转人工、发定位、发小程序、发电话、建工单、查天气。但它们是**散在各处、各自为政**的：

- 转人工是一个"开关"（`needsHumanRoute`）；
- 发定位/小程序/电话是另一个"下拉框"（`resourceType`）；
- 建工单、查天气是"工具"（`graph/create_ticket`、`get_weather`）；
- 知识库里写一句"订错房间→转人工"，系统只把它当**普通文字**，AI 只能嘴上说"我帮你转"，不会真正按下"转人工"按钮。

这次要做的，是盖一个**统一的总控面板（动作目录）**：

1. 所有"能干的事"都在一个清单里登记，各有一个**开关**；
2. 知识库里某条答案，可以**绑定**清单里的一个动作；
3. 客户问题命中了这条知识，系统就**自动执行绑定的动作**（该二次确认的二次确认、该查数据的查数据），而不是让 AI 拿嘴猜；
4. 将来要接 PMS 查房态、会员系统查等级，就往清单里**加一行 + 打开开关**，不用改表、不用改页面、不用给 AI 加提示词。

---

## 1. 核心概念

### 1.1 动作（Action）

一个动作 = "系统能替客户干的一件事"。每个动作有这几样：

| 字段 | 含义 | 例子（转人工） |
|---|---|---|
| code | 唯一代号 | `human_handoff` |
| name | 中文名 | 转人工 |
| kind | 类型 | `builtin`（系统内置） |
| description | 说明 | 转给人工客服接待 |
| inputSchema | 要客户补什么信息（参数） | 无 |
| requireConfirmation | 要不要二次确认 | 要 |
| enabled | 开关 | 开 |

### 1.2 动作目录（Action Catalog）

把所有动作登记在一起的清单。后台有一个**新页面**管理这个清单，核心就是**每个动作一行，能开能关**。

### 1.3 关键分类（决定"预留"怎么做）

动作分三类，**这是方案的地基**：

| 类型 | 含义 | 现在 | 例子 |
|---|---|---|---|
| `builtin` | 系统自己就能干，不依赖外部 | 能执行 | 转人工、发定位、发小程序、发电话、建工单 |
| `external` | 要接外部系统才能干 | **只有定义，执行器是"未接入"占位** | 查房态(PMS)、查会员等级 |
| `tool` | 已经接好的工具 | 能执行 | 查天气 |

**核心原则**：`external` 动作现在只登记"名字 + 要什么参数 + 开关(默认关)"，**不写"怎么查"的真实代码**。等 PMS/会员真接进来了，才写真实执行器，然后把开关打开。

> 一句话记住：**槽位和开关先开好，接线（执行器）等真接入那天再接。**

---

## 2. 数据模型（两张新表）

### 2.1 `t_reply_action_definition` —— 动作定义表

存"系统支持哪些动作"以及每个动作的元数据和开关。

```text
id                 bigint      主键
code               varchar(64) 唯一，动作代号（human_handoff / query_room_status）
name               varchar(120) 中文名
kind               varchar(20) builtin / external / tool
description        text        说明
input_schema       text        需要客户补的参数（JSON Schema，用于"先问什么"）
require_confirmation bool       要不要二次确认
executor_ref       varchar(80) 执行器引用（代码层注册名）
enabled            bool        启用开关（默认：builtin/tool 开，external 关）
sort_no            int         排序
status             int         状态（enums.Status）
remark             text
audit_fields ...
```

### 2.2 `t_knowledge_action_binding` —— 知识记录 ↔ 动作绑定表

存"哪条知识答案，绑定哪个动作"。知识答案的真实文本在 FastGPT，Agent Desk 侧用 `SourceRecordID` 稳定引用它，所以绑定键是 `source_record_id`。

```text
id                bigint       主键
tenant_id         bigint
store_id          bigint
knowledge_base_id bigint
source_record_id  varchar(255) FastGPT 命中记录的稳定标识
action_code       varchar(64)  绑定的动作代号
enabled           bool         该条绑定的开关
sort_no           int
remark            text
audit_fields ...
唯一键：(tenant_id, store_id, knowledge_base_id, source_record_id)
```

> 为什么用 `SourceRecordID`：知识正文存在 FastGPT，Agent Desk 只存引用。检索命中后拿到的正是 `SourceRecordID`（`KnowledgeRetrieveHit.SourceRecordID`），用它做绑定键最稳。

---

## 3. 代码注册表（`internal/ai/runtime/actions/`）

新增一个动作注册包，把"动作的定义 + 执行器"集中起来。注册表是**代码注册**（内置动作写死在代码里，后台只能开关、不能新建/删除内置动作）。

```go
// registry.go
type Definition struct {
    Code                string
    Name                string
    Kind                string // builtin / external / tool
    Description         string
    InputSchema         string // JSON Schema
    RequireConfirmation bool
    Executor            Executor
    DefaultEnabled      bool
}

type Executor interface {
    // 校验参数是否齐、执行动作、返回给客户的话术或结构化结果
    Run(ctx context.Context, input ActionInput) (ActionOutput, error)
}

var ErrActionNotProvisioned = errors.New("action not provisioned yet")
```

**内置动作清单（第一批）**：

| code | name | kind | requireConfirmation | 现状 |
|---|---|---|---|---|
| `human_handoff` | 转人工 | builtin | 是 | 执行器复用现有 `ConversationHumanDispatchService`（二次确认链） |
| `create_ticket` | 建工单 | builtin | 是 | 执行器复用现有建单确认链 |
| `provide_location` | 发定位 | builtin | 否 | 复用现有定位发送 |
| `provide_mini_program` | 发小程序 | builtin | 否 | 复用现有小程序发送 |
| `provide_phone` | 发电话 | builtin | 否 | 复用现有电话发送（缺配置给安全文本） |
| `query_weather` | 查天气 | tool | 否 | 复用现有 `get_weather` 工具 |
| `query_room_status` | 查房态 | external | 否 | **预留，执行器返回 `ErrActionNotProvisioned`** |
| `query_member_level` | 查会员等级 | external | 否 | **预留，执行器返回 `ErrActionNotProvisioned`** |

> 后两个 `external` 动作：登记在目录里、默认 `enabled=false`、后台灰显，但**不可执行**。将来接 PMS/会员时，写真实 Executor 覆盖掉占位、打开开关即可。

---

## 4. 链路接入（核心：知识命中 → 动作 → ActionLedger）

这是让整条回路"变聪明"的关键改动，插在**知识检索之后、Generate 之前**。

现有链路（简化）：

```text
IntentDetect → 逐题知识检索(拿到 SourceRecordID) → 构建 Evidence → Generate → Validate → Commit
```

新链路：

```text
IntentDetect → 逐题知识检索(拿到 SourceRecordID)
  → 【新增】查 t_knowledge_action_binding，命中且 enabled → 生成结构化动作，注入 ActionLedger
  → 构建 Evidence + 动作
  → Generate（只自然表达，不再口头"我要转人工"）
  → Validate（校验动作证据）
  → Commit（执行动作 + 提交消息）
```

具体落点：

1. 在 `internal/ai/runtime/executor/task_knowledge.go` 的 `buildRuntimeEvidenceBundle` 拿到每个 task 的命中 `SourceRecordID` 后，查询绑定，把命中动作的 task 从普通知识任务**提升为动作任务**（复用 `ReplyPlanTaskV2.OutputMode = "handoff"` 或新的 `action` 模式）。

2. 复用现有 `ensureRuntimeActionLedger`（`reply_plan_v2.go:170`）把动作写进 ActionLedger，走已有的 `human_handoff` / 资源动作提交链。

3. 这样 `human_handoff` 动作走 `executeIntentHumanRoute` 的**二次确认**（"要我帮您转人工吗？回复确认/取消"），`query_weather` 走工具调用，`external` 未接入的动作在执行时返回"当前功能未接入"，不让 AI 编造。

**第一阶段只打通 `human_handoff` 这一条**，作为"这条路能走"的样板，其余动作按同一模式接入。

---

## 5. 后端（真实可用）

按 AGENTS.md 分层规范落地：

1. **models**：`internal/models/reply_action_definition.go`、`internal/models/knowledge_action_binding.go`（两张表 + AutoMigrate 注册）。
2. **repositories**：`reply_action_definition_repository.go`、`knowledge_action_binding_repository.go`。
3. **services**：`reply_action_catalog_service.go`（List/Get/SetEnabled/GetEnabledActionMap），`knowledge_action_binding_service.go`（List/Set/Delete）。
4. **handlers**：`internal/handlers/dashboard/reply_action_handler.go`（`/reply-action/list`、`/reply-action/enabled_options`、`/reply-action/update_status` 等）。
5. **路由**：`internal/bootstrap/routes.go` 注册到 `dashboardGroup.Group("/reply-action")`。
6. **权限**：`internal/pkg/constants/auth.go` 新增平台级权限（`aiConfig.update` 或独立 `replyAction.manage`，与现有 `aiConfig` 组对齐）。

---

## 6. 前端（真实新页面）

新增 `web/app/dashboard/reply-actions/`，参考 `reply-intent-configs/page.tsx` 的结构（`DashboardCrudPage` 或列表 + 开关组件）。

页面内容：
- 动作目录列表：动作名、代号、类型徽章（内置/外部/工具）、是否二次确认、开关；
- 外部未接入动作**灰显 + "未接入"徽章**，且**不可打开开关**（或打开时提示"外部系统未接入"）；
- 点开详情：说明 + 需要客户补的参数（inputSchema 的可读化展示）。

导航：在后台侧边栏加入口（`web/app/dashboard/layout.tsx` 或导航配置），权限 `canManage` 控制显示。

---

## 7. 实施计划（分阶段）

### 阶段一（本次，先落地"目录 + 开关 + 转人工链路样板"）

1. 代码注册表 + 8 个动作定义（6 内置可用 + 2 外部预留）；
2. 两张新表 + AutoMigrate；
3. 动作目录 CRUD 后端 + 权限 + 路由；
4. 前端新页面（真实可用的列表 + 开关 + 详情）；
5. 链路接入：知识命中 → `human_handoff` 动作 → 二次确认（打通样板）。

### 阶段二（后续）

1. 知识资源面板加"给知识记录绑定动作"的入口（操作 `t_knowledge_action_binding`）；
2. 接 PMS → 写 `query_room_status` 真实执行器 + 打开开关；
3. 接会员 → 写 `query_member_level` 真实执行器 + 打开开关；
4. 接天气 → 已有工具，确认动作目录能正确路由。

---

## 8. 影响面 / 回滚 / 验收

- **models/migration**：新增两张表（AutoMigrate），无既有表改动。
- **DTO/枚举/公开 API/WebSocket**：新增后台接口，不动现有公开契约。
- **并行分支**：新增文件为主，链路接入点 `task_knowledge.go` / `reply_plan_v2.go` 落在 Runtime 保护区，需与 `codex/ai-billing` 对齐动作语义。
- **回滚**：纯新增，切回上一 release 即可；表不删除。
- **验收**：
  ```bash
  go test -tags dev ./internal/ai/runtime/... ./internal/services -run 'ReplyAction|ActionCatalog|KnowledgeAction|Handoff' -count=1
  go vet -tags dev ./...
  cd web && pnpm typecheck
  ```

---

## 9. 需要你拍板的三个点

1. **动作命名**：`human_handoff` / `query_room_status` / `query_member_level` 这几个代号 OK 吗？（可改，但定了就别乱动，代码和知识绑定都引用它）
2. **开关粒度**：动作的开关是**平台全局**（所有门店共用），还是**按门店**（每家门店各自开关）？我建议先做**全局 + 知识绑定层再按门店过滤**，简单可控。
3. **"预留动作是否允许在后台被打开"**：我建议外部未接入的动作**灰显、禁止打开**，避免运营误开。你认可吗？

确认这三点后，我按阶段一开始实现（先出后端 + 页面 + 转人工样板链路，不动服务器，本地验证后提交）。

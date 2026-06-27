# AGENTS.md

本文件定义本项目内 AI Agent 的强制开发规则。除非用户明确要求偏离，否则必须遵循。

## 1. 基本原则

- 适用范围：仓库根目录及所有子目录
- 优先级：用户明确指令 > 本文件 > 默认实现习惯
- 若与用户要求冲突：先执行用户要求，并在变更说明中标注偏离点

## 1.1 企业微信员工号协议强制前提

- 后续所有“企业微信员工号 / 企微员工号 / WxWorkProtocolInstance”相关开发，唯一协议依据是：`https://wework.apifox.cn/llms.txt` 及其链接的具体接口页面。
- 开发前必须先查阅对应接口页面的字段、类型、必填项、示例和说明；禁止凭记忆、猜测、其他项目经验补字段。
- 禁止把企业微信员工号协议与以下渠道混用：企业微信 CLI、企业微信客服号 / 微信客服 API、个人微信协议、旧 `weixins.apifox.cn` 字段。
- 若接口文档未说明某能力或字段，不得实现成“看起来可用”的假功能；必须在 UI 和后端错误中明确标注“缺少协议字段/接口，暂不可用”。
- 员工号消息发送必须按 `wework.apifox.cn` 文档使用 `conversation_id`；单聊联系人 ID 前缀为 `S:`，群 ID 前缀为 `R:`。

## 2. 固定技术栈

- 后端：`Golang` + `Gin` + `GORM` + `github.com/mlogclub/simple`
- 数据库：同时兼容 `SQLite` 和 `MySQL`
- 前端：`Next.js(App Router)` + `React` + `shadcn/ui` + `Tailwind CSS`
- 前端包管理器：`pnpm`

## 3. 目录约定

```text
.
├── cmd/
│   ├── server/
│   ├── migration/
│   └── generator/
├── internal/
│   ├── bootstrap/
│   ├── builders/
│   ├── handlers/
│   │   ├── api/
│   │   ├── dashboard/
│   │   └── third/
│   ├── middleware/
│   ├── migration/
│   ├── models/
│   ├── repositories/
│   ├── services/
│   └── pkg/
│       ├── config/
│       ├── dto/
│       ├── enums/
│       ├── errorsx/
│       ├── httpx/
│       ├── logx/
│       └── utils/
├── web/
└── docs/
```

## 4. 后端分层

必须遵循单向依赖：`models -> repositories -> services -> handlers`

- `models`：只定义实体和表映射
- `repositories`：只封装数据访问
- `services`：负责业务规则、事务编排、聚合逻辑
- `handlers`：只做参数解析、权限校验、service 调用、响应封装

禁止：

- handler 直接调用 repository
- 直接将 GORM model 返回前端
- 在 models/repositories 中写业务编排

## 4.1 分层全链路（models -> repositories -> services -> handlers -> builders）

本章是对“分层”规则的**可执行细化**：每一层只做该层应该做的事情，数据以 DTO 为中心流转，GORM 细节集中在 repository，事务边界集中在 service，返回组装集中在 builders/handler。

### 4.1.1 依赖方向（必须）

只允许如下依赖（单向）：

- `models` → 不依赖任何业务层
- `repositories` → 依赖 `models`、基础库（`gorm`/`simple/sqls`）
- `services` → 依赖 `repositories`、`models`、`enums/errorsx/utils`，负责事务与业务编排
- `builders` → 依赖 `models`、`dto/response`（必要时可依赖少量 `services` 用于补充展示字段，但优先在 service 里聚合好）
- `handlers` → 依赖 `services`、`builders`、`pkg/dto/request`、`pkg/httpx/params`、`pkg/httpx` 响应封装

禁止反向依赖：

- `repositories` 不能依赖 `services/handlers/builders`
- `models` 不能依赖 `repositories/services/handlers/builders`
- `handlers` 不能依赖 `repositories`（必须通过 service）

### 4.1.2 数据形态与流转（推荐统一）

一条典型 CRUD/业务动作的数据流：

1. **handler** 读取参数（query/body/form/path），做权限校验，调用 **service**
2. **service** 执行业务规则（校验、幂等、状态机、聚合），必要时开启事务，调用 **repository**
3. **repository** 只做数据读写（CRUD + 查询），返回 `models` 或必要的聚合结构
4. **builders** 将 `models`/聚合结果映射为 `response DTO`
5. **handler** 返回 `httpx.WriteJSON(...)`

强约束：

- **handler 入参使用 request DTO**
- **handler 出参使用 response DTO**
- **禁止直接返回 models 到前端**

### 4.1.3 各层“允许/禁止”清单

#### models（实体层）

- **允许**
  - 字段定义、表名、索引/约束标签、关联关系（GORM tags）
  - 轻量常量/枚举字段类型（更推荐放 `internal/pkg/enums`）
- **禁止**
  - 业务方法（例如 `CanDispatch()` 这类规则判断应在 service）
  - DB 访问、事务、复杂计算

#### repositories（数据访问层）

- **允许**
  - CRUD：`Get/Take/Find/FindOne/FindPageBy.../Create/Update/Updates/UpdateColumn/Delete`
  - 与查询相关的“可复用”方法：`FindByUserID`、`CountByStatus`、`FindActiveBy...`
  - 只要是“数据访问细节”，都应在这里（SQL 条件、排序、分页、锁）
- **禁止**
  - 业务编排（跨表流程、状态流转、事件发布等）
  - 权限判断、登录态判断
  - 直接拼接返回 DTO（DTO 映射属于 builders/handler）

Repository 最佳实践：

- **按主键读写优先提供统一方法**：`Get/Updates/Delete`，避免 service 层重复写 `id = ?`
- **查询条件优先使用 `sqls.Cnd` / `sqls.NewCnd()`**
- **repository 方法签名统一接收 `db *gorm.DB`**（支持 `sqls.DB()` 与 `ctx.Tx`）

#### services（业务层）

- **允许**
  - 业务规则：参数规范化、跨实体校验、状态机、幂等、并发语义
  - 聚合：需要组合多个 repository 结果
  - 事务编排：`sqls.WithTransaction(func(ctx *sqls.TxContext) error { ... })`
  - 调用 builders 前的领域对象整理（如果 builders 只做映射更干净）
- **禁止**
  - handler 才该做的事情：参数解析/HTTP 细节/响应封装
  - repository 才该做的事情：散落 GORM 查询（除非一次性复杂 SQL 且不值得抽）

Service 最佳实践：

- **事务只在“需要原子性”的地方开**，并确保事务内所有 DB 操作都走 `ctx.Tx`
- **service 内调用 repository**，不要“既有 repo 又直接 GORM”混搭造成风格分裂

#### builders（输出构建层）

定位：将 `models`（或 service 聚合结果）转换为 `response DTO`，避免 handler 写一堆映射样板代码。

- **允许**
  - `Model -> ResponseDTO` 的纯映射
  - 时间格式化、枚举 label 填充（必要时）
  - 批量构建：`BuildXxxList([]models.Xxx) []response.Xxx`
- **禁止**
  - DB 访问（builders 不应查询数据库）
  - 权限判断、事务、复杂业务流程

builders 推荐形式：

- 位置：`internal/builders/*_builder.go`
- 方法：`BuildXxx(item *models.Xxx) *response.Xxx` / `BuildXxxList(list []models.Xxx) []response.Xxx`

#### handlers（接口层）

- **允许**
  - 参数解析：`params.ReadJSON/ReadForm/NewPagedSqlCnd/GetInt64...`
  - 权限：`AuthService.GetAuthPrincipal/RequirePermission/HasPermission`
  - 调 service，调 builders，包装 `httpx.WriteJSON`
- **禁止**
  - 直接调用 repository
  - 直接返回 models
  - 在 handler 内写业务编排（例如“先写 A 再写 B”）

### 4.1.4 “事务”最佳实践（替代模糊口号）

事务边界应由 service 决定，原则如下：

- **必须开事务**（`sqls.WithTransaction`）
  - 多条写 SQL（例如更新主表 + 写日志表/事件表/关系表）
  - “读-改-写”且要求一致性（并发下不能错）
  - 跨多个 repository 的写操作需要原子性
- **不需要开事务**
  - 只有一次写 SQL（单条 `Create/Updates/UpdateColumn/Delete`）
  - 只有一次写 SQL + 纯计算/参数清洗

事务内规则：

- 在事务内，所有 DB 调用必须使用 `ctx.Tx`（repository 方法的 `db` 参数传 `ctx.Tx`）
- 禁止事务内混用 `sqls.DB()`（会脱离事务）

### 4.1.5 一个“标准接口”的代码骨架（示例）

```go
// Handler: 参数/权限/响应
func XxxPostUpdate(ctx *gin.Context) {
  operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionXxxUpdate)
  if err != nil {
    httpx.WriteJSON(ctx, err)
    return
  }
  req := request.UpdateXxxRequest{}
  if err := params.ReadJSON(ctx, &req); err != nil {
    httpx.WriteJSON(ctx, err)
    return
  }
  if err := services.XxxService.UpdateXxx(req, operator); err != nil {
    httpx.WriteJSON(ctx, err)
    return
  }
  httpx.WriteJSON(ctx, nil)
}
```

```go
// Service: 业务规则 + 事务编排 + 调 repository
func (s *xxxService) UpdateXxx(req request.UpdateXxxRequest, operator *dto.AuthPrincipal) error {
  current := repositories.XxxRepository.Get(sqls.DB(), req.ID)
  if current == nil {
    return errorsx.InvalidParam("对象不存在")
  }
  // 只有一次写 SQL：不需要事务
  return repositories.XxxRepository.Updates(sqls.DB(), req.ID, map[string]any{
    "name": strings.TrimSpace(req.Name),
    "update_user_id": operator.UserID,
    "update_user_name": operator.Username,
    "updated_at": time.Now(),
  })
}
```

```go
// Builder: Model -> ResponseDTO
func BuildXxx(item *models.Xxx) *response.Xxx {
  if item == nil {
    return nil
  }
  return &response.Xxx{
    id: item.ID,
    // ...
  }
}
```

## 5. simple 使用约定

- DB 初始化后必须执行：`sqls.SetDB(db)`
- 查询条件优先使用：`sqls.Cnd`
- 参数绑定优先使用：`internal/pkg/httpx/params`
- HTTP 响应统一使用：`internal/pkg/httpx.WriteJSON`
- 写操作事务边界按 **4.1.4 事务最佳实践** 执行（禁止“单条写 SQL 也默认开事务”的口号式规则）

## 6. 数据库兼容规则

- 字段类型使用兼容集合：`varchar`、`text`、`int`、`bigint`、`datetime`
- 主键统一使用 `int64`
- 避免数据库私有语法和方言特性
- 时间存储和解析策略保持统一，MySQL 使用 `parseTime=True`

## 7. 代码生成与 Migration

### 7.1 代码生成

- 入口：`cmd/generator/generator.go`
- 命令：`make generator`
- 生成库：`github.com/mlogclub/codegen`
- 注册方式：`codegen.GetGenerateStruct(&models.XXX{})`
- 生成文件建议放在 `generated` 目录，命名为 `*_gen.go`
- 生成代码只负责基础 CRUD，业务逻辑必须写在手写 service/handler 中

标准流程：

1. 定义或修改 model
2. 在 generator 中注册
3. 执行 `make generator`
4. 在手写层补业务逻辑
5. 执行测试与自检

### 7.2 Migration

- DDL 变更默认不走 `internal/migration/runner.go`
- 表结构新增、修改、索引变更统一通过 `sqls.DB().AutoMigrate(models.Models...)`
- `internal/migration/runner.go` 只用于 DML：初始化数据、回填、修复、重映射等
- migration 必须幂等，且 `version` 单调递增
- 执行顺序：先 `AutoMigrate`，再 `migration.Migrate(...)`

## 8. 接口规范

### 8.1 DTO 与返回

- DTO 分离：`request` / `response` 分开定义
- JSON 字段统一使用 `camelCase`
- 禁止透传底层 SQL 错误
- 错误码分段：
  - `1000-1999` 参数错误
  - `2000-2999` 业务错误
  - `3000-3999` 认证/权限错误
  - `5000-5999` 系统错误

### 8.2 路径分层

- `/api/dashboard/*`：业务后台接口
- `/api/third/*`：第三方平台调用接口
- `/api/*`：开放接口

禁止新增 `/api/v1` 这类版本前缀。

### 8.3 dashboard 接口风格

- 资源路径优先平铺：如 `/api/dashboard/project`
- 列表、创建、更新、删除优先使用 `/list`、`/create`、`/update`、`/delete`
- 查询条件优先通过 `query` 或 `body` 传递
- 除详情接口外，尽量不使用 path param
- 详情接口允许 `GET /api/dashboard/project/{id}`
- 从属资源优先通过 `projectId`、`episodeId` 等普通参数过滤，不鼓励深层嵌套路由

### 8.4 路由注册

- Gin 引擎统一在 `internal/bootstrap/server.go` 中创建，中间件也在这里按顺序注册
- 路由按分组函数拆到 `internal/bootstrap/routes.go` 及 `internal/bootstrap/*_routes.go`
- 业务后台统一通过 `dashboardGroup := app.Group("/api/dashboard", middleware.AuthMiddleware)` 注册
- 开放接口按领域归档到 `/api/*` 分组，第三方回调归档到 `/api/third/*` 分组
- 在分组内部通过 `group.GET/POST/PUT/DELETE/Any(...)` 显式挂载 handler
- 不要为每个资源单独再创建一层顶级 `app.Group("/api/dashboard/xxx")`
- 认证与鉴权中间件优先挂在 `/api/dashboard` 或 `/api/admin` 这一层

### 8.5 Gin 显式路由规则

本项目使用 Gin 显式路由，不使用框架自动路由。handler 方法名只用于代码组织，最终 URL 以 `internal/bootstrap/*_routes.go` 中注册的路径为准。

- 路由挂载方式示例：

```go
func registerDashboardQuickReplyRoutes(group *gin.RouterGroup) {
  group.Any("/list", dashboard.QuickReplyAnyList)
  group.GET("/:id", dashboard.QuickReplyGetBy)
  group.POST("/create", dashboard.QuickReplyPostCreate)
  group.POST("/update", dashboard.QuickReplyPostUpdate)
  group.POST("/delete", dashboard.QuickReplyPostDelete)
}
```

- 在上面的注册下，资源基础路径由外层 `dashboardGroup.Group("/quick-reply")` 决定，最终完整路径为 `/api/dashboard/quick-reply/list`、`/api/dashboard/quick-reply/{id}` 等
- handler 命名建议保留现有可读前缀：`XxxAnyList`、`XxxGetBy`、`XxxPostCreate`、`XxxPostUpdate`、`XxxPostDelete`
- handler 命名不产生路由；新增接口前必须修改对应 `register...Routes` 函数
- HTTP Method 必须由 Gin 注册方法决定：
  - 列表查询：优先 `group.Any("/list", XxxAnyList)`，前端按 `GET /list` 使用
  - 详情查询：优先 `group.GET("/:id", XxxGetBy)`
  - 写接口：统一 `group.POST("/create|/update|/delete", XxxPost...)`
  - 业务动作：统一显式路径，如 `group.POST("/send_message", ConversationPostSend_message)`
- path param 只在详情或强路径语义场景使用；普通过滤条件继续使用 query/body

当前项目常见正确映射示例：

- `registerDashboardUserRoutes(dashboardGroup.Group("/user"))`
  - `group.Any("/list", dashboard.UserAnyList)` -> `ANY /api/dashboard/user/list`
  - `group.GET("/:id", dashboard.UserGetBy)` -> `GET /api/dashboard/user/{id}`
  - `group.POST("/create", dashboard.UserPostCreate)` -> `POST /api/dashboard/user/create`
  - `group.POST("/update", dashboard.UserPostUpdate)` -> `POST /api/dashboard/user/update`
  - `group.POST("/delete", dashboard.UserPostDelete)` -> `POST /api/dashboard/user/delete`
- `registerDashboardConversationRoutes(dashboardGroup.Group("/conversation"))`
  - `group.Any("/list", dashboard.ConversationAnyList)` -> `ANY /api/dashboard/conversation/list`
  - `group.GET("/:id", dashboard.ConversationGetBy)` -> `GET /api/dashboard/conversation/{id}`
  - `group.Any("/message/list", dashboard.ConversationAnyMessage_list)` -> `ANY /api/dashboard/conversation/message/list`
  - `group.POST("/send_message", dashboard.ConversationPostSend_message)` -> `POST /api/dashboard/conversation/send_message`

容易写错的点：

- 不要以为新增 `XxxGetList` 方法会自动出现 `/list` 路由；必须在 routes 文件显式注册
- 不要把详情接口注册成 `/detail`，当前约定详情为 `GET /:id`
- 不要随意新增深层嵌套路由；从属资源优先通过 `projectId`、`conversationId` 等普通参数过滤
- 如果接口契约要求下划线路径，直接在 Gin 路由里写下划线路径，例如 `group.POST("/send_message", ...)`
- handler 新增方法前，先写出对应 Gin 路由注册，确认最终 URL 与前端约定一致再落代码

### 8.6 Handler 约定

- 每个资源一个 handler 文件，位置为 `internal/handlers/{api|dashboard|third}/*_handler.go`
- handler 统一形式：`func XxxPostCreate(ctx *gin.Context)`
- 方法命名建议：
  - `XxxAnyList(ctx *gin.Context)`
  - `XxxGetBy(ctx *gin.Context)`
  - `XxxPostCreate(ctx *gin.Context)`
  - `XxxPostUpdate(ctx *gin.Context)`
  - `XxxPostDelete(ctx *gin.Context)`
  - 业务动作可扩展 `XxxPostTest(ctx *gin.Context)`、`XxxPostGenerate(ctx *gin.Context)` 等

- `AnyList` 分页列表要求：
  - 使用 `params.NewPagedSqlCnd(...)`
  - 每个筛选字段通过 `params.QueryFilter` 显式声明
  - 默认排序优先 `.Desc("id")`；特殊排序需明确理由
  - service 层优先使用 `FindPageByCnd(...)`
  - handler 层做 DTO 映射，禁止直接返回 model 列表

- 分页返回统一为：

```go
httpx.WriteJSON(ctx, &web.PageResult{Results: results, Page: paging})
```

- 分页 `data` 结构必须为：
  - `data.results`
  - `data.page.page`
  - `data.page.limit`
  - `data.page.total`

- 详情优先返回 `httpx.WriteJSON(ctx, dto)`
- 删除优先返回 `httpx.WriteJSON(ctx, nil)`
- JSON body 优先使用 `params.ReadJSON`
- form 参数优先使用 `params.ReadForm`
- 获取单个参数可以使用 `params.GetInt64`、`params.GetInt64Arr`、`params.Get` 等
- 分页和 query 优先使用 `params.NewPagedSqlCnd`
- 登录态用户通过 `services.AuthService.GetAuthPrincipal(ctx)` 或 `RequirePermission(ctx, ...)` 获取
- 权限判断统一通过 `services.AuthService.HasPermission(...)` 或 `RequirePermission(...)`
- 鉴权失败统一返回 `httpx.WriteJSON(ctx, err)`
- `gorm.ErrRecordNotFound` 等错误应转换成明确业务提示
- 后端数据返回时，将数据转换成Response DTO相关的逻辑可以放到 `internal/builders` 包下。

### 8.7 枚举的定义

- 系统中的常量统一定义到 `/internal/pkg/enums` 包下
- 模型的状态优先使用 `/internal/pkg/enums/enums.go` 中的 `Status`，只有不满足需求的时候在考虑新增状态枚举
- 前后端共用枚举必须遵循文档 [docs/design/specs/backend-frontend-enum-ast-spec.md](docs/design/specs/backend-frontend-enum-ast-spec.md)
- 前后端共用枚举只允许在后端定义，前端必须使用 `make enums` 生成结果，禁止手写重复业务枚举

## 9. Go 代码规范

- 日志统一使用标准库 `log/slog`
- 新增日志禁止引入其他日志库
- 日志字段优先使用结构化键值对
- 新增 Go 代码统一使用 `any`，禁止新增 `interface{}`
- 修改 Go 代码后必须执行 `gofmt`

## 10. 前端规范

### 10.1 工程事实

- 前端目录：`web`
- 框架：`Next.js 16` + App Router
- 页面目录：`web/app/*`
- 组件目录：`web/components/*`
- shadcn/ui 基础组件目录：`web/components/ui/*`
- 工具目录：`web/lib/*`、`web/hooks/*`
- 别名：`@/*`
- 样式入口：`web/app/globals.css`
- shadcn 配置：`web/components.json`

### 10.2 组件与页面

- 基础组件优先使用 `shadcn/ui`
- 已有 `shadcn/ui` 能覆盖的场景，禁止重复封装等价基础组件
- 缺少 `dialog`、`textarea`、`select` 等基础组件且业务确实需要时，必须按规范安装，不要手写替代品
- 不要修改 `web/components/ui/*`
- 业务组件放在 `web/components/*` 或对应业务目录
- API 调用统一封装在服务层，禁止页面里散落裸 `fetch`
- 前端业务接口统一通过 `web/lib/api/*` 下的 service 方法发起，禁止在 `page.tsx`、业务组件、store 中直接对业务接口使用裸 `fetch`
- `web/lib/api/client.ts` 是默认请求入口；新增业务 API 优先复用 `request()`，不要重复实现一套新的请求客户端
- 后端返回为统一 `JsonResult` 时，前端必须统一处理 `success`、`errorCode`、`message`、`data`，禁止只按 HTTP status 判断成功失败
- 业务代码中不要自行解析 `JsonResult.data`、拼装通用错误处理、手写鉴权刷新逻辑；这些逻辑必须收敛在公共请求封装内
- 需要登录态的请求必须复用统一封装附带的认证头、`3000/3002` 刷新 token、登录失效清理能力，禁止在页面层各自处理
- 仅在调用第三方外部服务、下载二进制流、SSE/流式响应、WebSocket 握手等统一封装暂不适配的场景下，才允许直接使用底层 `fetch`；使用时必须在代码中注明原因

### 10.3 shadcn 使用流程

- 先确认 `web/components.json` 已存在；存在时禁止再次 `init`
- 命令统一在 `web` 目录执行
- 安装依赖统一使用 `pnpm`
- 新增基础组件优先使用：
  - `cd web && pnpm dlx shadcn@latest add button`
  - `cd web && pnpm dlx shadcn@latest add button dialog form table`

### 10.4 Next.js 约定

- 优先使用 App Router
- 需要客户端状态或副作用时显式加 `"use client"`
- 页面与布局遵循 `layout.tsx`、`page.tsx` 约定
- 检查优先复用现有 scripts：`dev`、`build`、`start`、`lint`、`format`、`typecheck`

### 10.5 枚举管理

- 前端所有枚举统一定义在 `web/lib/enums.ts` 文件中
- 枚举统一由后端定义，前端枚举使用 `make enums` 指令生成

### 10.6 后台列表与表单基线

- 后台类 CRUD 页面优先参考：`docs/design/specs/frontend-list-form-best-practice.md`
- 基线案例：`web/app/dashboard/quick-replies`
- 默认采用“`page.tsx` 管列表与状态，`_components/edit.tsx` 管弹窗表单”的两层结构
- 表单默认采用：`react-hook-form` + `zod` + `web/components/ui/field.tsx`
- API 调用统一留在页面层或服务层，表单组件不直接请求接口
- 新增或修改后台列表/表单页面后，AI Agent 必须先自查是否符合该文档约定，再执行 `cd web && pnpm typecheck`

### 10.7 前端其他规范

- 所有前端展示时间统一格式化为 `yyyy-MM-dd HH:mm:ss` 推荐统一使用 `web/lib/utils.ts` 中的 `formatDateTime` 方法
- 下拉框组件不要使用shadcn的select组件，而是使用shadcn的combobox组件。项目级别使用combobox封装了一个下拉框组件 `web/components/option-combobox.tsx`，尽量通用。
- 如果是组件中使用到的数据，尽量组件中自己去加载，不要在外面加载之后传到组件中。要保证组件的独立性。

## 11. 提交前检查清单

每次修改后至少确认：

1. 没有跨层调用或反向依赖
2. 写操作有明确事务边界
3. 返回仍符合统一 JsonResult 结构
4. 兼容 SQLite 与 MySQL
5. 补充了必要测试，至少覆盖 service 核心路径
6. Go 改动已执行 `gofmt`
7. 前端改动至少通过 `pnpm lint` 或 `pnpm typecheck`（在 `web` 目录）

# 贡献指南

## 开发基线

- 后端：Go 1.26、Gin、GORM、`github.com/mlogclub/simple`
- 前端：Next.js App Router、React、shadcn/ui、Tailwind CSS
- 数据库：SQLite 与 MySQL 必须同时兼容
- 前端包管理器：pnpm

开始开发前先阅读 [AGENTS.md](AGENTS.md) 和
[文档索引](docs/README.md)。

## 分层

后端依赖方向固定为：

```text
models -> repositories -> services -> handlers
```

- models 只定义实体和映射；
- repositories 只负责数据访问；
- services 负责规则、状态机和事务；
- handlers 只负责参数、权限、service 调用和响应；
- builders 将领域对象转换为 response DTO。

禁止 handler 直接访问 repository，禁止直接把 GORM model 返回前端。

## 数据与迁移

- DDL 通过 `AutoMigrate` 管理；
- DML 回填与初始化放入 `internal/migration`；
- Migration 必须幂等并使用未冲突的单调版本号；
- 所有新结构必须验证 SQLite 和 MySQL；
- 当前产品基线是 fresh 数据库，不得偷偷恢复旧兼容表或旧运行链。

## 前端

- 复用 `web/components/ui` 和现有业务组件；
- API 调用统一放在 `web/lib/api`；
- 页面不直接使用裸 `fetch` 调业务接口；
- 业务枚举只在后端定义，并通过 `make enums` 生成前端结果；
- 修改页面后至少运行 `pnpm typecheck` 和相关行为测试。

## 安全

- 不提交真实 `.env`、密钥、数据库、备份、证书或生产日志；
- 测试凭据必须明显为假值；
- Tenant 与 Store 范围是强制上限，不能只依赖前端隐藏；
- 第三方回调失败不得返回假成功；
- 企业微信员工号协议只依据 `https://wework.apifox.cn/llms.txt`。

## 验证

提交前运行：

```bash
gofmt -w <修改的 Go 文件>
go test ./... -count=1
go vet ./...
cd web
pnpm typecheck
pnpm lint
pnpm build
```

前端 `.test.mjs` 文件应全部执行。还需运行：

```bash
git diff --check
```

## 提交与 PR

- 一个提交只承载一个可解释、可回滚的目标；
- 提交信息说明实际行为，不使用模糊的“更新代码”；
- PR 说明包含目标、根因、数据与接口变化、权限、Migration、测试、风险和回滚边界；
- 修改共享契约时列出并行分支影响和建议合并顺序；
- 不使用 force push 覆盖其他开发者工作；
- 不提交 `docs/generated/` 下的临时报告。

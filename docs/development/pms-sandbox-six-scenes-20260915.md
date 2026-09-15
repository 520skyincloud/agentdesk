# 测试 PMS 六场景实施记录

## 范围

仅 test-2。现有 Intent 拆题，Executor 固定执行测试业务查询，客户确认已投递方案后办理。沿用 Commit、Outbox、员工接管和稳定消息 ID。不改外部 HPMS、生产或“薇薇”备份，不复制真实住客资料。

## 进度

- [x] 保留已有工作区修改并建立独立分支。
- [x] fetch origin 并核查 customer-audit、ai-billing。
- [x] 六场景固定执行及混合问题处理。
- [x] Sandbox 数据、事务办理、后台与权限。
- [x] 自动测试与前端验证。
- [ ] 备份、发布和最多 12 个真实输入验证。

代码和自动化验证已完成；部署前仍需在 test-2 备份当前运行版本、初始化演示数据，并
完成真实消息、办理回读和枕头商品卡片收件端验收。未完成这些步骤前，不把六场景标记为
生产可用。

## 配置边界

`pms.environment=test-2` 且 `pms.sandboxEnabled=true` 才开放测试后台。
`pms.enabled=true` 且 `pms.provider=sandbox` 才切换会话数据源。
Provider 由服务端选择，Sandbox 不可用时不能回落 HPMS。

## 共享影响

新增兼容 SQLite/MySQL 的 Sandbox 表，通过 AutoMigrate 建立；扩展 PMSOperation，不改变已有消息及 Outbox 契约。新增门店权限内的后台 DTO/API。旧空 Provider 记录解释为 HPMS，并且只由外部续住入口处理。

customer-audit 在 models、routes、权限、导航、reply_trigger、资源服务有并行变更；该分支的租户化修改未合并，不能整体 cherry-pick 覆盖。本次在独立分支完成新增业务契约，合并时先处理表/权限/路由，再接业务与 UI，逐项校验门店权限语义。ai-billing 当前没有本次运行文件的新增并行修改。

## 验证

聚焦 config、PMS、repositories、services、tools、executor、runtime 和新增 dashboard 路由测试；前端 typecheck/build 及桌面、移动端页面检查。真实验收分开记录分类、逐题回复、数据变化、回读和实际送达，未确认收件端打开的枕头卡片不能记为完成。

最近一次自动验证：

```bash
go test -p=1 \
  ./internal/ai/runtime/executor \
  ./internal/ai/runtime \
  ./internal/services \
  ./internal/pms \
  ./internal/repositories \
  ./internal/handlers/dashboard \
  ./internal/bootstrap \
  ./internal/pkg/config \
  ./cmd/reply-runtime-eval \
  -count=1
git diff --check
cd web && pnpm typecheck
cd web && pnpm exec eslint \
  app/dashboard/pms-sandbox/page.tsx \
  app/dashboard/pms-sandbox/_components/edit.tsx \
  lib/api/pms-sandbox.ts
```

上述自动检查已通过。真实 test-2 验收仍必须逐项记录：

| 项目 | 状态 |
| --- | --- |
| 分类正确 | 待部署后验证 |
| 逐题回复完整 | 待部署后验证 |
| 后台测试数据真实变化 | 待部署后验证 |
| 办理结果回查一致 | 待部署后验证 |
| 企微实际送达及枕头卡片可打开 | 待收件端验证 |

## 回退边界

关闭 Sandbox Provider 或回退测试程序，保留消息和操作审计。不得恢复旧客户数据库；旧测试方案在重置数据后失效。外部 HPMS 和生产从未切换。

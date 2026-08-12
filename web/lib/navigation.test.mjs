import assert from "node:assert/strict"
import { readFile } from "node:fs/promises"
import test from "node:test"
import ts from "typescript"
import vm from "node:vm"

async function loadNavigation() {
  const source = await readFile(new URL("./navigation.tsx", import.meta.url), "utf8")
  const compiled = ts.transpileModule(source, {
    compilerOptions: {
      target: ts.ScriptTarget.ES2020,
      module: ts.ModuleKind.CommonJS,
      jsx: ts.JsxEmit.ReactJSX,
    },
    fileName: "navigation.tsx",
  })
  const jsxRuntime = {
    jsx: (type, props) => ({ type, props }),
    jsxs: (type, props) => ({ type, props }),
  }
  const icons = new Proxy({}, { get: (_target, name) => String(name) })
  const sandbox = {
    exports: {},
    module: { exports: {} },
    require: (id) => {
      if (id === "react/jsx-runtime") return jsxRuntime
      if (id === "lucide-react") return icons
      if (id === "@/lib/navigation-active") {
        return {
          isDashboardNavItemActive: (pathname, itemUrl) =>
            itemUrl === "/dashboard"
              ? pathname === itemUrl
              : pathname === itemUrl || pathname.startsWith(`${itemUrl}/`),
        }
      }
      throw new Error(`Unexpected module: ${id}`)
    },
    Set,
  }
  sandbox.exports = sandbox.module.exports
  vm.runInNewContext(compiled.outputText, sandbox)
  return sandbox.module.exports
}

const allPermissions = [
  "dashboard.view",
  "serviceAnalytics.view",
  "billing.view",
  "conversationRecord.view",
  "store.view",
  "storeWorkbench.view",
  "conversation.view",
  "conversation.handover",
  "ticket.view",
  "customer.view",
  "channel.view",
  "agent.view",
  "agentTeamSchedule.view",
  "arrivalConnection.view",
  "knowledgeBase.view",
  "quickReply.view",
  "tag.view",
  "aiConfig.view",
  "agentRunLog.view",
  "skillDefinition.view",
  "user.view",
  "role.view",
  "permission.view",
  "notification.view",
  "tenant.view",
  "storageSetting.view",
  "wxworkDevicePool.view",
  "mcp.view",
]

const tenantContext = { isPlatformAccount: false, hasActiveTenant: true }
const platformContext = { isPlatformAccount: true, hasActiveTenant: true }
const platformOnlyContext = { isPlatformAccount: true, hasActiveTenant: false }

const companyAll = [
  "/dashboard",
  "/dashboard/service-analytics",
  "/dashboard/conversations",
  "/dashboard/conversation-dispatch",
  "/dashboard/conversation-monitor",
  "/dashboard/tickets",
  "/dashboard/customers",
  "/dashboard/billing-query",
]
const organizationAll = [
  "/dashboard/stores",
  "/dashboard/store-workbench",
  "/dashboard/agents",
  "/dashboard/agent-team-schedules",
  "/dashboard/wxwork-protocol-instances",
  "/dashboard/arrival-connections",
]
const serviceAll = [
  "/dashboard/knowledge",
  "/dashboard/knowledge-candidates",
  "/dashboard/quick-replies",
  "/dashboard/tags",
  "/dashboard/skill-definition",
]
const accessAll = [
  "/dashboard/users",
  "/dashboard/roles",
  "/dashboard/permissions",
]
const platformAll = [
  "/dashboard/channels",
  "/dashboard/model-profiles",
  "/dashboard/agent-run-logs",
  "/dashboard/storage-settings",
  "/dashboard/wxwork-device-pool",
  "/dashboard/mcp",
  "/dashboard/reply-intent-configs",
  "/dashboard/industry-tag-templates",
]

const expectedRoleUrls = {
  super_admin: [...companyAll, ...organizationAll, ...serviceAll, ...accessAll, ...platformAll],
  admin: [...companyAll, ...organizationAll, ...serviceAll, ...accessAll, ...platformAll],
  tenant_admin: [
    ...companyAll,
    "/dashboard/stores",
    "/dashboard/agents",
    "/dashboard/agent-team-schedules",
    "/dashboard/wxwork-protocol-instances",
    "/dashboard/arrival-connections",
    ...serviceAll,
    ...accessAll,
  ],
  cs_team_leader: [
    ...companyAll,
    "/dashboard/stores",
    "/dashboard/agents",
    "/dashboard/agent-team-schedules",
    "/dashboard/wxwork-protocol-instances",
    "/dashboard/arrival-connections",
    "/dashboard/quick-replies",
    "/dashboard/tags",
  ],
  cs_user: [
    "/dashboard",
    "/dashboard/conversations",
    "/dashboard/conversation-monitor",
    "/dashboard/tickets",
    "/dashboard/customers",
    "/dashboard/stores",
    "/dashboard/agents",
    "/dashboard/arrival-connections",
  ],
  store_staff: [
    "/dashboard/conversations",
    "/dashboard/billing-query",
    "/dashboard/store-workbench",
  ],
}

function sectionKeys(sections) {
  return Array.from(sections, (section) => section.titleKey)
}

function itemUrls(sections) {
  return Array.from(sections.flatMap((section) => section.items), (item) => item.url)
}

test("built-in roles receive the exact product page matrix", async () => {
  const { filterDashboardNavForSession } = await loadNavigation()
  for (const [role, expected] of Object.entries(expectedRoleUrls)) {
    const context = role === "super_admin" || role === "admin" ? platformContext : tenantContext
    const sections = filterDashboardNavForSession(allPermissions, context, [role])
    assert.deepEqual(itemUrls(sections), expected, role)
  }
})

test("platform administrators without a selected company only see platform-safe pages", async () => {
  const { filterDashboardNavForSession } = await loadNavigation()
  const sections = filterDashboardNavForSession(
    allPermissions,
    platformOnlyContext,
    ["super_admin"],
  )

  assert.deepEqual(sectionKeys(sections), [
    "nav.companyWorkspace",
    "nav.accessManagement",
    "nav.platformManagement",
  ])
  assert.deepEqual(itemUrls(sections), [
    "/dashboard/billing-query",
    "/dashboard/roles",
    "/dashboard/permissions",
    ...platformAll,
  ])
})

test("permissions remain required inside an allowed built-in role", async () => {
  const { filterDashboardNavForSession } = await loadNavigation()
  const sections = filterDashboardNavForSession(
    ["conversation.view", "serviceAnalytics.view"],
    tenantContext,
    ["cs_user"],
  )

  assert.deepEqual(itemUrls(sections), ["/dashboard/conversations"])
})

test("custom roles retain permission-driven navigation", async () => {
  const { filterDashboardNavForSession } = await loadNavigation()
  const customUrls = itemUrls(
    filterDashboardNavForSession(allPermissions, tenantContext, ["custom_auditor"]),
  )
  assert.deepEqual(customUrls, [
    ...companyAll,
    ...organizationAll,
    ...serviceAll,
    ...accessAll,
  ])

  const mixedUrls = itemUrls(
    filterDashboardNavForSession(
      allPermissions,
      tenantContext,
      ["custom_auditor", "cs_user"],
    ),
  )
  assert.deepEqual(mixedUrls, expectedRoleUrls.cs_user)
})

test("multiple built-in roles receive the union of their page scopes", async () => {
  const { filterDashboardNavForSession } = await loadNavigation()
  const urls = itemUrls(
    filterDashboardNavForSession(
      allPermissions,
      tenantContext,
      ["cs_user", "store_staff"],
    ),
  )
  assert.deepEqual(urls, [
    "/dashboard",
    "/dashboard/conversations",
    "/dashboard/conversation-monitor",
    "/dashboard/tickets",
    "/dashboard/customers",
    "/dashboard/billing-query",
    "/dashboard/stores",
    "/dashboard/store-workbench",
    "/dashboard/agents",
    "/dashboard/arrival-connections",
  ])
})

test("direct URL access uses the same role and permission contract", async () => {
  const { dashboardPathIsAccessible } = await loadNavigation()

  assert.equal(
    dashboardPathIsAccessible(
      "/dashboard/service-analytics",
      allPermissions,
      tenantContext,
      ["cs_user"],
    ),
    false,
  )
  assert.equal(
    dashboardPathIsAccessible(
      "/dashboard/quick-replies",
      allPermissions,
      tenantContext,
      ["cs_team_leader"],
    ),
    true,
  )
  assert.equal(
    dashboardPathIsAccessible(
      "/dashboard/knowledge",
      allPermissions,
      tenantContext,
      ["cs_team_leader"],
    ),
    false,
  )
  assert.equal(
    dashboardPathIsAccessible(
      "/dashboard/store-workbench",
      allPermissions,
      tenantContext,
      ["store_staff"],
    ),
    true,
  )
  assert.equal(
    dashboardPathIsAccessible(
      "/dashboard",
      allPermissions,
      tenantContext,
      ["store_staff"],
    ),
    false,
  )
  assert.equal(
    dashboardPathIsAccessible(
      "/dashboard/channels",
      allPermissions,
      tenantContext,
      ["super_admin"],
    ),
    false,
  )
})

test("retired product pages are inaccessible even with full permissions", async () => {
  const { dashboardPathIsAccessible } = await loadNavigation()
  for (const path of [
    "/dashboard/companies",
    "/dashboard/company-detail/1",
    "/dashboard/settings",
    "/dashboard/reply-intent-profiles",
  ]) {
    assert.equal(
      dashboardPathIsAccessible(path, allPermissions, platformContext, ["super_admin"]),
      false,
      path,
    )
  }
})

test("fallback path follows the same role matrix", async () => {
  const { firstAccessibleDashboardPath } = await loadNavigation()

  assert.equal(
    firstAccessibleDashboardPath(allPermissions, tenantContext, ["store_staff"]),
    "/dashboard/conversations",
  )
  assert.equal(
    firstAccessibleDashboardPath(
      ["store.view", "arrivalConnection.view"],
      tenantContext,
      ["cs_user"],
    ),
    "/dashboard/stores",
  )
  assert.equal(
    firstAccessibleDashboardPath(allPermissions, platformOnlyContext, ["super_admin"]),
    "/dashboard/billing-query",
  )
})

test("tenant context detection excludes retired and platform-only pages", async () => {
  const { dashboardPathRequiresTenant } = await loadNavigation()

  assert.equal(dashboardPathRequiresTenant("/dashboard"), true)
  assert.equal(dashboardPathRequiresTenant("/dashboard/conversations/12"), true)
  assert.equal(dashboardPathRequiresTenant("/dashboard/users"), true)
  assert.equal(dashboardPathRequiresTenant("/dashboard/settings"), false)
  assert.equal(dashboardPathRequiresTenant("/dashboard/reply-intent-profiles"), false)
  assert.equal(dashboardPathRequiresTenant("/dashboard/reply-intent-configs"), false)
  assert.equal(dashboardPathRequiresTenant("/dashboard/roles"), false)
  assert.equal(dashboardPathRequiresTenant("/dashboard/billing-query"), false)
})

test("unregistered dashboard paths remain blocked", async () => {
  const { dashboardPathIsAccessible } = await loadNavigation()

  assert.equal(
    dashboardPathIsAccessible(
      "/dashboard/unregistered-module",
      allPermissions,
      tenantContext,
      ["tenant_admin"],
    ),
    false,
  )
})

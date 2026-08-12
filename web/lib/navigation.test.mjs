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
  "storeWorkbench.view",
  "conversation.view",
  "conversation.handover",
  "ticket.view",
  "customer.view",
  "channel.view",
  "agent.view",
  "agentTeamSchedule.view",
  "arrivalConnection.view",
  "aiAgent.view",
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

function sectionKeys(sections) {
  return Array.from(sections, (section) => section.titleKey)
}

function itemUrls(sections) {
  return Array.from(sections.flatMap((section) => section.items), (item) => item.url)
}

test("platform accounts without a selected company only see platform-safe navigation", async () => {
  const { filterDashboardNavForSession } = await loadNavigation()
  const sections = filterDashboardNavForSession(allPermissions, {
    isPlatformAccount: true,
    hasActiveTenant: false,
  })

  assert.deepEqual(sectionKeys(sections), ["nav.companyWorkspace", "nav.accessManagement", "nav.platformManagement"])
  assert.equal(itemUrls(sections).includes("/dashboard/users"), false)
  assert.equal(itemUrls(sections).includes("/dashboard/channels"), true)
  assert.equal(itemUrls(sections).includes("/dashboard/reply-intent-profiles"), true)
  assert.equal(itemUrls(sections).includes("/dashboard/reply-intent-configs"), true)
  assert.equal(itemUrls(sections).includes("/dashboard"), false)
  assert.equal(itemUrls(sections).includes("/dashboard/billing-query"), true)
})

test("tenant accounts see company work areas but no platform controls", async () => {
  const { filterDashboardNavForSession } = await loadNavigation()
  const sections = filterDashboardNavForSession(allPermissions, {
    isPlatformAccount: false,
    hasActiveTenant: true,
  })
  const keys = sectionKeys(sections)
  const urls = itemUrls(sections)

  assert.equal(keys.includes("nav.companyWorkspace"), true)
  assert.equal(keys.includes("nav.customerServiceOrganization"), true)
  assert.equal(keys.includes("nav.serviceCapabilities"), true)
  assert.equal(keys.includes("nav.platformManagement"), false)
  assert.equal(urls.includes("/dashboard/ai-agents"), false)
  assert.equal(urls.includes("/dashboard/model-profiles"), false)
  assert.equal(urls.includes("/dashboard/agent-run-logs"), false)
  assert.equal(urls.includes("/dashboard/wxwork-protocol-instances"), true)
  assert.equal(urls.includes("/dashboard/settings"), true)
  assert.equal(urls.includes("/dashboard/channels"), false)
  assert.equal(urls.includes("/dashboard/reply-intent-profiles"), false)
  assert.equal(urls.includes("/dashboard/reply-intent-configs"), false)
  assert.equal(urls.includes("/dashboard/billing-query"), true)
})

test("standalone customer service users do not see service or access management sections", async () => {
  const { filterDashboardNavForSession } = await loadNavigation()
  const sections = filterDashboardNavForSession(
    allPermissions,
    { isPlatformAccount: false, hasActiveTenant: true },
    ["cs_user"],
  )
  const keys = sectionKeys(sections)

  assert.equal(keys.includes("nav.companyWorkspace"), true)
  assert.equal(keys.includes("nav.customerServiceOrganization"), true)
  assert.equal(keys.includes("nav.serviceCapabilities"), false)
  assert.equal(keys.includes("nav.accessManagement"), false)
})

test("team leaders and administrators retain service and access management sections", async () => {
  const { filterDashboardNavForSession } = await loadNavigation()
  const context = { isPlatformAccount: false, hasActiveTenant: true }
  const elevatedRoles = ["cs_team_leader", "tenant_admin", "admin", "super_admin"]

  for (const elevatedRole of elevatedRoles) {
    for (const roles of [[elevatedRole], ["cs_user", elevatedRole]]) {
      const keys = sectionKeys(filterDashboardNavForSession(allPermissions, context, roles))
      assert.equal(keys.includes("nav.serviceCapabilities"), true, roles.join(","))
      assert.equal(keys.includes("nav.accessManagement"), true, roles.join(","))
    }
  }
})

test("platform accounts inside a company can use both company and platform navigation", async () => {
  const { filterDashboardNavForSession } = await loadNavigation()
  const sections = filterDashboardNavForSession(allPermissions, {
    isPlatformAccount: true,
    hasActiveTenant: true,
  })
  const keys = sectionKeys(sections)
  const urls = itemUrls(sections)

  assert.equal(keys.includes("nav.companyWorkspace"), true)
  assert.equal(keys.includes("nav.platformManagement"), true)
  assert.equal(urls.includes("/dashboard/model-profiles"), true)
  assert.equal(urls.includes("/dashboard/agent-run-logs"), true)
  assert.equal(urls.includes("/dashboard/billing-query"), true)
})

test("view permissions still control individual entries inside an allowed context", async () => {
  const { filterDashboardNavForSession } = await loadNavigation()
  const sections = filterDashboardNavForSession(["conversation.view"], {
    isPlatformAccount: false,
    hasActiveTenant: true,
  })
  const urls = itemUrls(sections)

  assert.equal(urls.includes("/dashboard/conversations"), true)
  assert.equal(urls.includes("/dashboard/conversation-dispatch"), false)
  assert.equal(urls.includes("/dashboard"), false)
  assert.equal(urls.includes("/dashboard/settings"), false)
  assert.equal(urls.includes("/dashboard/tickets"), false)
  assert.equal(urls.includes("/dashboard/roles"), false)
})

test("dispatch orchestration requires the existing handover permission", async () => {
  const { filterDashboardNavForSession, dashboardPathIsAccessible } = await loadNavigation()
  const context = { isPlatformAccount: false, hasActiveTenant: true }
  const viewOnly = filterDashboardNavForSession(["conversation.view"], context)
  const handover = filterDashboardNavForSession(
    ["conversation.view", "conversation.handover"],
    context,
  )

  assert.equal(itemUrls(viewOnly).includes("/dashboard/conversation-dispatch"), false)
  assert.equal(itemUrls(handover).includes("/dashboard/conversation-dispatch"), true)
  assert.equal(
    dashboardPathIsAccessible("/dashboard/conversation-dispatch", ["conversation.view"], context),
    false,
  )
  assert.equal(
    dashboardPathIsAccessible(
      "/dashboard/conversation-dispatch",
      ["conversation.view", "conversation.handover"],
      context,
    ),
    true,
  )
})

test("operations overview requires its explicit permission", async () => {
  const { filterDashboardNavForSession } = await loadNavigation()
  const sections = filterDashboardNavForSession(["dashboard.view"], {
    isPlatformAccount: false,
    hasActiveTenant: true,
  })
  const urls = itemUrls(sections)

  assert.equal(urls.includes("/dashboard"), true)
  assert.equal(urls.includes("/dashboard/conversations"), false)
})

test("analytics and conversation records use their own permissions", async () => {
  const { filterDashboardNavForSession, dashboardPathIsAccessible } = await loadNavigation()
  const context = { isPlatformAccount: false, hasActiveTenant: true }
  const analyticsOnly = filterDashboardNavForSession(["serviceAnalytics.view"], context)
  const recordsOnly = filterDashboardNavForSession(["conversationRecord.view"], context)

  assert.equal(itemUrls(analyticsOnly).includes("/dashboard/service-analytics"), true)
  assert.equal(itemUrls(analyticsOnly).includes("/dashboard/conversation-monitor"), false)
  assert.equal(itemUrls(recordsOnly).includes("/dashboard/conversation-monitor"), true)
  assert.equal(itemUrls(recordsOnly).includes("/dashboard/conversations"), false)
  assert.equal(dashboardPathIsAccessible("/dashboard/service-analytics", [], context), false)
  assert.equal(dashboardPathIsAccessible("/dashboard/conversation-monitor", ["conversationRecord.view"], context), true)
})

test("store workbench uses its own permission instead of channel access", async () => {
  const { filterDashboardNavForSession } = await loadNavigation()
  const withWorkbench = filterDashboardNavForSession(["storeWorkbench.view"], {
    isPlatformAccount: false,
    hasActiveTenant: true,
  })
  const withChannelOnly = filterDashboardNavForSession(["channel.view"], {
    isPlatformAccount: false,
    hasActiveTenant: true,
  })

  assert.equal(itemUrls(withWorkbench).includes("/dashboard/store-workbench"), true)
  assert.equal(itemUrls(withChannelOnly).includes("/dashboard/store-workbench"), false)
  assert.equal(itemUrls(withChannelOnly).includes("/dashboard/wxwork-protocol-instances"), true)
})

test("arrival linking is tenant-scoped and uses its explicit permission", async () => {
  const { filterDashboardNavForSession, dashboardPathIsAccessible } = await loadNavigation()
  const tenantContext = { isPlatformAccount: false, hasActiveTenant: true }
  const platformContext = { isPlatformAccount: true, hasActiveTenant: false }

  assert.equal(
    itemUrls(
      filterDashboardNavForSession(["arrivalConnection.view"], tenantContext),
    ).includes("/dashboard/arrival-connections"),
    true,
  )
  assert.equal(
    itemUrls(
      filterDashboardNavForSession(["channel.view"], tenantContext),
    ).includes("/dashboard/arrival-connections"),
    false,
  )
  assert.equal(
    dashboardPathIsAccessible(
      "/dashboard/arrival-connections",
      ["arrivalConnection.view"],
      tenantContext,
    ),
    true,
  )
  assert.equal(
    dashboardPathIsAccessible(
      "/dashboard/arrival-connections",
      ["arrivalConnection.view"],
      platformContext,
    ),
    false,
  )
})

test("direct dashboard routes reuse navigation permissions and context", async () => {
  const { dashboardPathIsAccessible, firstAccessibleDashboardPath } = await loadNavigation()
  const tenantContext = { isPlatformAccount: false, hasActiveTenant: true }

  assert.equal(
    dashboardPathIsAccessible("/dashboard/store-workbench", ["storeWorkbench.view"], tenantContext),
    true,
  )
  assert.equal(
    dashboardPathIsAccessible("/dashboard/conversations", ["storeWorkbench.view"], tenantContext),
    false,
  )
  assert.equal(dashboardPathIsAccessible("/dashboard/companies", ["customer.view"], tenantContext), false)
  assert.equal(dashboardPathIsAccessible("/dashboard/company-detail/1", ["customer.view"], tenantContext), false)
  assert.equal(
    dashboardPathIsAccessible("/dashboard/notifications", [], tenantContext),
    false,
  )
  assert.equal(
    dashboardPathIsAccessible("/dashboard/channels", ["tenant.view"], tenantContext),
    false,
  )
  assert.equal(dashboardPathIsAccessible("/dashboard/help", allPermissions, tenantContext), false)
  assert.equal(dashboardPathIsAccessible("/dashboard/unregistered-module", allPermissions, tenantContext), false)
  assert.equal(firstAccessibleDashboardPath(["storeWorkbench.view"], tenantContext), "/dashboard/store-workbench")
  assert.equal(
    firstAccessibleDashboardPath(
      ["storeWorkbench.view", "conversation.view", "billing.view"],
      tenantContext,
    ),
    "/dashboard/conversations",
  )
})

test("tenant page guard follows the same navigation context contract", async () => {
  const { dashboardPathRequiresTenant } = await loadNavigation()

  assert.equal(dashboardPathRequiresTenant("/dashboard"), true)
  assert.equal(dashboardPathRequiresTenant("/dashboard/conversations/12"), true)
  assert.equal(dashboardPathRequiresTenant("/dashboard/users"), true)
  assert.equal(dashboardPathRequiresTenant("/dashboard/settings"), true)
  assert.equal(dashboardPathRequiresTenant("/dashboard/channels"), false)
  assert.equal(dashboardPathRequiresTenant("/dashboard/reply-intent-profiles"), false)
  assert.equal(dashboardPathRequiresTenant("/dashboard/reply-intent-configs"), false)
  assert.equal(dashboardPathRequiresTenant("/dashboard/roles"), false)
  assert.equal(dashboardPathRequiresTenant("/dashboard/billing-query"), false)
})

test("billing uses one permission-gated route across platform and tenant scopes", async () => {
  const { filterDashboardNavForSession, dashboardPathIsAccessible } = await loadNavigation()
  const platformContext = { isPlatformAccount: true, hasActiveTenant: false }
  const tenantContext = { isPlatformAccount: false, hasActiveTenant: true }

  assert.equal(itemUrls(filterDashboardNavForSession(["billing.view"], platformContext)).includes("/dashboard/billing-query"), true)
  assert.equal(itemUrls(filterDashboardNavForSession(["billing.view"], tenantContext)).includes("/dashboard/billing-query"), true)
  assert.equal(dashboardPathIsAccessible("/dashboard/billing-query", [], platformContext), false)
  assert.equal(dashboardPathIsAccessible("/dashboard/billing-query", ["billing.view"], platformContext), true)
})

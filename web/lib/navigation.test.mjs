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
  "conversation.view",
  "ticket.view",
  "customer.view",
  "company.view",
  "channel.view",
  "agent.view",
  "agentTeamSchedule.view",
  "aiAgent.view",
  "knowledgeBase.view",
  "quickReply.view",
  "tag.view",
  "aiConfig.view",
  "skillDefinition.view",
  "user.view",
  "role.view",
  "permission.view",
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

  assert.deepEqual(sectionKeys(sections), ["nav.accessManagement", "nav.platformManagement"])
  assert.equal(itemUrls(sections).includes("/dashboard/users"), false)
  assert.equal(itemUrls(sections).includes("/dashboard/channels"), true)
  assert.equal(itemUrls(sections).includes("/dashboard"), false)
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
  assert.equal(urls.includes("/dashboard/ai-agents"), true)
  assert.equal(urls.includes("/dashboard/wxwork-protocol-instances"), true)
  assert.equal(urls.includes("/dashboard/settings"), true)
  assert.equal(urls.includes("/dashboard/channels"), false)
})

test("platform accounts inside a company can use both company and platform navigation", async () => {
  const { filterDashboardNavForSession } = await loadNavigation()
  const sections = filterDashboardNavForSession(allPermissions, {
    isPlatformAccount: true,
    hasActiveTenant: true,
  })
  const keys = sectionKeys(sections)

  assert.equal(keys.includes("nav.companyWorkspace"), true)
  assert.equal(keys.includes("nav.platformManagement"), true)
})

test("view permissions still control individual entries inside an allowed context", async () => {
  const { filterDashboardNavForSession } = await loadNavigation()
  const sections = filterDashboardNavForSession(["conversation.view"], {
    isPlatformAccount: false,
    hasActiveTenant: true,
  })
  const urls = itemUrls(sections)

  assert.equal(urls.includes("/dashboard/conversations"), true)
  assert.equal(urls.includes("/dashboard"), false)
  assert.equal(urls.includes("/dashboard/settings"), false)
  assert.equal(urls.includes("/dashboard/tickets"), false)
  assert.equal(urls.includes("/dashboard/roles"), false)
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

test("tenant page guard follows the same navigation context contract", async () => {
  const { dashboardPathRequiresTenant } = await loadNavigation()

  assert.equal(dashboardPathRequiresTenant("/dashboard"), true)
  assert.equal(dashboardPathRequiresTenant("/dashboard/conversations/12"), true)
  assert.equal(dashboardPathRequiresTenant("/dashboard/users"), true)
  assert.equal(dashboardPathRequiresTenant("/dashboard/settings"), true)
  assert.equal(dashboardPathRequiresTenant("/dashboard/channels"), false)
  assert.equal(dashboardPathRequiresTenant("/dashboard/roles"), false)
})

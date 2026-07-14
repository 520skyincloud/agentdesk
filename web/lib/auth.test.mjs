import assert from "node:assert/strict"
import test from "node:test"

class MemoryStorage {
  values = new Map()

  getItem(key) {
    return this.values.get(key) ?? null
  }

  setItem(key, value) {
    this.values.set(key, String(value))
  }

  removeItem(key) {
    this.values.delete(key)
  }
}

globalThis.window = {
  localStorage: new MemoryStorage(),
  sessionStorage: new MemoryStorage(),
  dispatchEvent: () => true,
}

const { clearSession, readSession, setActiveTenantId, writeSession } = await import(
  "./auth.ts"
)

function platformSession() {
  return {
    accessToken: "token",
    user: {
      id: 1,
      tenantId: 0,
      username: "admin",
      nickname: "Admin",
      avatar: "",
      status: 0,
      roles: ["admin"],
      registrationSource: "platform_created",
      approvalStatus: "approved",
      mustChangePassword: false,
    },
    permissions: ["tenant.switch"],
    roles: ["admin"],
    activeTenantId: 0,
    activeTenantName: "",
    canSwitchTenant: true,
    isPlatformAccount: true,
  }
}

test("platform tenant id and name share the same tab-local context", () => {
  writeSession(platformSession())
  setActiveTenantId(101, "Company A")

  const session = readSession()
  assert.equal(session.activeTenantId, 101)
  assert.equal(session.activeTenantName, "Company A")
})

test("tab-local tenant context wins over a localStorage session written by another tab", () => {
  writeSession(platformSession())
  setActiveTenantId(101, "Company A")
  const sharedSession = platformSession()
  sharedSession.activeTenantId = 202
  sharedSession.activeTenantName = "Company B"
  window.localStorage.setItem("agent-desk-session", JSON.stringify(sharedSession))

  const session = readSession()
  assert.equal(session.activeTenantId, 101)
  assert.equal(session.activeTenantName, "Company A")
})

test("returning to platform clears both tab-local tenant values", () => {
  writeSession(platformSession())
  setActiveTenantId(101, "Company A")
  setActiveTenantId(0)

  const session = readSession()
  assert.equal(session.activeTenantId, 0)
  assert.equal(session.activeTenantName, "")
  clearSession()
})

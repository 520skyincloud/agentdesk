export type AuthUser = {
  id: number
  tenantId: number
  username: string
  nickname: string
  avatar: string
  status: number
  roles: string[]
  registrationSource: string
  approvalStatus: string
  mustChangePassword: boolean
}

export type AuthSession = {
  accessToken: string
  expiresAt?: string
  user: AuthUser
  permissions: string[]
  roles: string[]
  activeTenantId: number
  activeTenantName: string
  canSwitchTenant: boolean
  isPlatformAccount: boolean
}

const SESSION_STORAGE_KEY = "agent-desk-session"
const ACTIVE_TENANT_STORAGE_KEY = "agent-desk-active-tenant"
const ACTIVE_TENANT_NAME_STORAGE_KEY = "agent-desk-active-tenant-name"
export const AUTH_SESSION_EXPIRED_EVENT = "agent-desk-auth-expired"

function hasWindow() {
  return typeof window !== "undefined"
}

export function readSession(): AuthSession | null {
  if (!hasWindow()) {
    return null
  }

  const raw = window.localStorage.getItem(SESSION_STORAGE_KEY)
  if (!raw) {
    return null
  }

  try {
    const session = JSON.parse(raw) as AuthSession
    if (!session.isPlatformAccount) {
      return {
        ...session,
        activeTenantId: session.user.tenantId > 0 ? session.user.tenantId : 0,
        activeTenantName: session.activeTenantName || "",
      }
    }
    const activeTenantId = Number(
      window.sessionStorage.getItem(ACTIVE_TENANT_STORAGE_KEY) || 0
    )
    const normalizedTenantId =
      Number.isSafeInteger(activeTenantId) && activeTenantId > 0 ? activeTenantId : 0
    return {
      ...session,
      activeTenantId: normalizedTenantId,
      activeTenantName:
        normalizedTenantId > 0
          ? window.sessionStorage.getItem(ACTIVE_TENANT_NAME_STORAGE_KEY) || ""
          : "",
    }
  } catch {
    window.localStorage.removeItem(SESSION_STORAGE_KEY)
    return null
  }
}

export function writeSession(session: AuthSession) {
  if (!hasWindow()) {
    return
  }
  const previous = readSession()
  if (!previous || previous.user.id !== session.user.id) {
    window.sessionStorage.removeItem(ACTIVE_TENANT_STORAGE_KEY)
    window.sessionStorage.removeItem(ACTIVE_TENANT_NAME_STORAGE_KEY)
  }
  if (!session.isPlatformAccount && session.user.tenantId > 0) {
    window.sessionStorage.setItem(ACTIVE_TENANT_STORAGE_KEY, String(session.user.tenantId))
    window.sessionStorage.setItem(
      ACTIVE_TENANT_NAME_STORAGE_KEY,
      session.activeTenantName || ""
    )
  } else if (session.activeTenantId > 0) {
    window.sessionStorage.setItem(ACTIVE_TENANT_STORAGE_KEY, String(session.activeTenantId))
    window.sessionStorage.setItem(
      ACTIVE_TENANT_NAME_STORAGE_KEY,
      session.activeTenantName || ""
    )
  } else {
    window.sessionStorage.removeItem(ACTIVE_TENANT_STORAGE_KEY)
    window.sessionStorage.removeItem(ACTIVE_TENANT_NAME_STORAGE_KEY)
  }
  window.localStorage.setItem(SESSION_STORAGE_KEY, JSON.stringify(session))
}

export function clearSession() {
  if (!hasWindow()) {
    return
  }
  window.localStorage.removeItem(SESSION_STORAGE_KEY)
  window.sessionStorage.removeItem(ACTIVE_TENANT_STORAGE_KEY)
  window.sessionStorage.removeItem(ACTIVE_TENANT_NAME_STORAGE_KEY)
}

export function readActiveTenantId(session: AuthSession | null = readSession()) {
  if (!session) {
    return 0
  }
  if (!session.isPlatformAccount) {
    return session.user.tenantId > 0 ? session.user.tenantId : 0
  }
  if (!hasWindow()) {
    return session.activeTenantId > 0 ? session.activeTenantId : 0
  }
  const value = Number(window.sessionStorage.getItem(ACTIVE_TENANT_STORAGE_KEY) || 0)
  return Number.isSafeInteger(value) && value > 0 ? value : 0
}

export function setActiveTenantId(tenantId: number, tenantName = "") {
  if (!hasWindow()) {
    return
  }
  const session = readSession()
  if (!session?.isPlatformAccount || !session.canSwitchTenant) {
    return
  }
  const normalized = Number.isSafeInteger(tenantId) && tenantId > 0 ? tenantId : 0
  if (normalized > 0) {
    window.sessionStorage.setItem(ACTIVE_TENANT_STORAGE_KEY, String(normalized))
    window.sessionStorage.setItem(ACTIVE_TENANT_NAME_STORAGE_KEY, tenantName)
  } else {
    window.sessionStorage.removeItem(ACTIVE_TENANT_STORAGE_KEY)
    window.sessionStorage.removeItem(ACTIVE_TENANT_NAME_STORAGE_KEY)
  }
  window.localStorage.setItem(
    SESSION_STORAGE_KEY,
    JSON.stringify({
      ...session,
      activeTenantId: normalized,
      activeTenantName: normalized > 0 ? tenantName : "",
    }),
  )
}

export function expireSession() {
  if (!hasWindow()) {
    return
  }
  clearSession()
  window.dispatchEvent(new Event(AUTH_SESSION_EXPIRED_EVENT))
}

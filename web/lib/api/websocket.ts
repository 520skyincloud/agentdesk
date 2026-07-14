const API_BASE_URL =
  process.env.NEXT_PUBLIC_API_BASE_URL?.trim() ||
  (process.env.NODE_ENV === "development" ? "http://127.0.0.1:8083" : "")

export function createWebSocketBaseUrl() {
  if (API_BASE_URL) {
    return API_BASE_URL.replace(/^http/, "ws").replace(/\/$/, "")
  }

  if (typeof window === "undefined") {
    return ""
  }

  const protocol = window.location.protocol === "https:" ? "wss:" : "ws:"
  return `${protocol}//${window.location.host}`
}

export function createAuthenticatedWebSocketUrl(
  path: string,
  accessToken: string,
  tenantId = 0
) {
  const params = new URLSearchParams({ accessToken })
  if (tenantId > 0) {
    params.set("tenantId", String(tenantId))
  }
  return `${createWebSocketBaseUrl()}${path}?${params.toString()}`
}

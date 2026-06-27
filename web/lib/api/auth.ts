import { clearSession, writeSession, type AuthSession } from "@/lib/auth"
import { request } from "@/lib/api/client"

export type LoginRequest = {
  username: string
  password: string
}

export type AuthOptions = {
  wxworkEnabled: boolean
  oidcEnabled: boolean
}

export async function fetchAuthOptions() {
  return request<AuthOptions>("/api/auth/options", {
    skipAuth: true,
  })
}

export async function loginWithPassword(payload: LoginRequest) {
  const data = await request<AuthSession>("/api/auth/login", {
    method: "POST",
    body: JSON.stringify(payload),
    skipAuth: true,
  })
  writeSession(data)
  return data
}

export async function exchangeWxWorkTicket(ticket: string) {
  const data = await request<AuthSession>("/api/auth/wxwork_exchange", {
    method: "POST",
    body: JSON.stringify({ ticket }),
    skipAuth: true,
  })
  writeSession(data)
  return data
}

export async function exchangeOIDCTicket(ticket: string) {
  const data = await request<AuthSession>("/api/auth/oidc_exchange", {
    method: "POST",
    body: JSON.stringify({ ticket }),
    skipAuth: true,
  })
  writeSession(data)
  return data
}

export async function fetchProfile() {
  return request<AuthSession>("/api/auth/profile")
}

export async function logout() {
  try {
    await request("/api/auth/logout", {
      method: "POST",
    })
  } finally {
    clearSession()
  }
}

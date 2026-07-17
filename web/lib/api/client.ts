import { expireSession, readActiveTenantId, readSession } from "@/lib/auth"
import { readStoredLocale } from "@/i18n/config"
import { translateCurrentMessage } from "@/i18n/messages"
import { repairMojibakeDeep } from "@/lib/utils"

const API_BASE_URL =
  process.env.NEXT_PUBLIC_API_BASE_URL?.trim() || ""

type JsonResult<T> = {
  errorCode: number
  message: string
  data: T
  success: boolean
}

type RequestOptions = RequestInit & {
  skipAuth?: boolean
  baseUrl?: string
  onResponse?: (response: Response) => void
}

function buildRequestHeaders(options: RequestOptions) {
  const session = readSession()
  const authHeaders = new Headers(options.headers)
  if (!options.skipAuth && session?.accessToken) {
    authHeaders.set("Authorization", `Bearer ${session.accessToken}`)
    const activeTenantId = readActiveTenantId(session)
    if (activeTenantId > 0 && !authHeaders.has("X-Tenant-ID")) {
      authHeaders.set("X-Tenant-ID", String(activeTenantId))
    }
  }
  if (
    !authHeaders.has("Content-Type") &&
    options.body &&
    !(typeof FormData !== "undefined" && options.body instanceof FormData)
  ) {
    authHeaders.set("Content-Type", "application/json")
  }
  const locale = readStoredLocale()
  authHeaders.set("Accept-Language", locale)
  authHeaders.set("X-Locale", locale)
  return authHeaders
}

async function parseResult<T>(response: Response) {
  const payload = (await response.json()) as JsonResult<T>
  if (!response.ok || !payload.success) {
    if (payload.errorCode === 3000 || payload.errorCode === 3002) {
      expireSession()
    }
    const error = new Error(payload.message || translateCurrentMessage("api.requestFailed"))
    ;(error as Error & { errorCode?: number }).errorCode = payload.errorCode
    throw error
  }
  return repairMojibakeDeep(payload.data)
}

export async function request<T>(
  path: string,
  options: RequestOptions = {}
): Promise<T> {
  const { headers, skipAuth, baseUrl, onResponse, ...rest } = options
  delete (rest as RequestOptions).baseUrl
  delete (rest as RequestOptions).onResponse
  const authHeaders = buildRequestHeaders({ ...rest, headers, skipAuth })

  const requestBaseUrl = baseUrl !== undefined ? baseUrl : API_BASE_URL
  const response = await fetch(`${requestBaseUrl}${path}`, {
    ...rest,
    headers: authHeaders,
    cache: "no-store",
  })
  onResponse?.(response)

  return parseResult<T>(response)
}

export async function requestBlob(path: string, options: RequestOptions = {}): Promise<Blob> {
  const { headers, skipAuth, baseUrl, onResponse, ...rest } = options
  const authHeaders = buildRequestHeaders({ ...rest, headers, skipAuth })
  const requestBaseUrl = baseUrl !== undefined ? baseUrl : API_BASE_URL
  const response = await fetch(`${requestBaseUrl}${path}`, {
    ...rest,
    headers: authHeaders,
    cache: "no-store",
  })
  onResponse?.(response)
  if (!response.ok) {
    try {
      await parseResult<never>(response)
    } catch (error) {
      throw error
    }
    throw new Error(translateCurrentMessage("api.requestFailed"))
  }
  return response.blob()
}

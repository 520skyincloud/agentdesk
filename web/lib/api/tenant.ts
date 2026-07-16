import { request } from "@/lib/api/client"
import { Status, TenantVerificationStatus } from "@/lib/generated/enums"

export type TenantPageResult<T> = {
  results: T[]
  page: {
    page: number
    limit: number
    total: number
  }
}

export type AdminTenant = {
  id: number
  tenantCode: string
  legalName: string
  shortName: string
  registrationType: string
  registrationNo: string
  contactName: string
  contactMobile: string
  contactEmail: string
  address: string
  verificationStatus: TenantVerificationStatus
  verifiedAt?: string
  status: Status
  remark: string
  supervisorUserId: number
  supervisorUsername: string
  supervisorNickname: string
  agentCount: number
  storeCount: number
  agentTeamCount: number
  lastActiveAt?: string
  createdAt: string
  updatedAt: string
  createUserName: string
  updateUserName: string
}

export type TenantSupervisorPayload = {
  username: string
  nickname: string
  mobile: string
  email: string
}

export type TenantBasePayload = {
  legalName: string
  shortName: string
  registrationType: string
  registrationNo: string
  contactName: string
  contactMobile: string
  contactEmail: string
  address: string
  remark: string
}

export type CreateTenantPayload = TenantBasePayload & {
  supervisor: TenantSupervisorPayload
}

export type UpdateTenantPayload = TenantBasePayload & {
  id: number
}

export type TenantInvitation = {
  tenantId: number
  tenantName: string
  code: string
  codeLast4: string
  inviteLink: string
  version: number
  usedCount: number
  lastUsedAt?: string
  expiresAt?: string
  expired: boolean
  createdAt: string
  rotatedAt?: string
}

export type CreateTenantResult = {
  tenant: AdminTenant
  supervisorUsername: string
  supervisorPassword: string
  defaultAgentTeamId: number
  invitation: TenantInvitation
}

export type TenantAIModelGrant = {
  aiConfigId: number
  name: string
  provider: string
  modelType: string
  modelName: string
  status: Status
  requestCount: number
  promptTokens: number
  completionTokens: number
  cachedPromptTokens: number
  assignmentCount: number
  assignedUsageCodes: string[]
  assignedEmployeeCount: number
}

export type TenantAIModelUsage = {
  usageCode: string
  usageName: string
  expectedModelType: string
  aiConfigId: number
  effectiveAiConfigId: number
  effectiveModelName: string
  effectiveSource: string
}

export type TenantAIModelAccess = {
  tenantId: number
  wxWorkInstanceId: number
  grants: TenantAIModelGrant[]
  usages: TenantAIModelUsage[]
}

export type UpdateTenantAIModelAccessPayload = {
  tenantId: number
  grantedAiConfigIds: number[]
  defaults: Array<{ usageCode: string; aiConfigId: number }>
}

function toQueryString(query?: Record<string, string | number | undefined>) {
  if (!query) return ""

  const params = new URLSearchParams()
  Object.entries(query).forEach(([key, value]) => {
    if (value === undefined || value === "") return
    params.set(key, String(value))
  })
  const output = params.toString()
  return output ? `?${output}` : ""
}

export function fetchTenants(
  query?: Record<string, string | number | undefined>
) {
  return request<TenantPageResult<AdminTenant>>(
    `/api/dashboard/tenant/list${toQueryString(query)}`
  )
}

export function fetchTenant(id: number) {
  return request<AdminTenant>(`/api/dashboard/tenant/${id}`)
}

export function createTenant(payload: CreateTenantPayload) {
  return request<CreateTenantResult>("/api/dashboard/tenant/create", {
    method: "POST",
    body: JSON.stringify(payload),
  })
}

export function updateTenant(payload: UpdateTenantPayload) {
  return request<void>("/api/dashboard/tenant/update", {
    method: "POST",
    body: JSON.stringify(payload),
  })
}

export function updateTenantStatus(id: number, status: Status) {
  return request<void>("/api/dashboard/tenant/update_status", {
    method: "POST",
    body: JSON.stringify({ id, status }),
  })
}

export function fetchTenantAIModelAccess(tenantId: number) {
  return request<TenantAIModelAccess>("/api/dashboard/tenant/model_access", {
    method: "POST",
    body: JSON.stringify({ tenantId }),
  })
}

export function updateTenantAIModelAccess(
  payload: UpdateTenantAIModelAccessPayload
) {
  return request<TenantAIModelAccess>(
    "/api/dashboard/tenant/update_model_access",
    {
      method: "POST",
      body: JSON.stringify(payload),
    }
  )
}

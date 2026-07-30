import { request } from "@/lib/api/client"

export type ArrivalPageResult<T> = {
  results: T[]
  page: {
    page: number
    limit: number
    total: number
  }
}

export type ArrivalConnection = {
  id: number
  tenantId: number
  storeId: number
  storeCode: string
  storeName: string
  brandName: string
  scene: string
  connectionStatus: string
  authorizationStatus: string
  authorizedCorpName: string
  contactMemberConfigured: boolean
  wxWorkProtocolInstanceId: number
  wxWorkProtocolAccountName: string
  wxWorkProtocolHealth: string
  lastVerifiedAt?: string
  lastErrorCode: string
  recentScanCount: number
  recentBoundCount: number
  contactProvider: string
  staticContactPlugId: string
  acquisitionLinkStatus: string
  acquisitionQuotaTotal: number
  acquisitionQuotaBalance: number
  acquisitionFailureCode: string
  acquisitionLastVerifiedAt?: string
  updatedAt?: string
}

export type ArrivalAuthorizationOption = {
  id: number
  corpName: string
  status: string
}

export type ArrivalProtocolInstanceOption = {
  id: number
  name: string
  healthStatus: string
  storeId: number
}

export type ArrivalInvitation = {
  invitationUrl: string
  expiresAt: string
}

export type ArrivalConnectionVerification = {
  connectionStatus: string
  authorizationOk: boolean
  memberOk: boolean
  instanceOk: boolean
  providerMode: string
  providerOk: boolean
  quotaTotal: number
  quotaBalance: number
  errorCode: string
}

export type ArrivalAuditLog = {
  id: number
  storeId: number
  action: string
  entityType: string
  entityId: number
  result: string
  detailJson: string
  operatorName: string
  createdAt: string
}

export type ArrivalProviderInvitation = {
  valid: boolean
  storeName: string
  brandName: string
  connectionStatus: string
  authorized: boolean
  expiresAt: string
}

export type ArrivalAuthorizationBegin = {
  authorizationUrl: string
  authorizationState: string
  alreadyAuthorized: boolean
}

export type ArrivalProviderOption = {
  value: string
  label: string
}

export type ArrivalProviderInstanceOption = {
  id: number
  name: string
  healthStatus: string
  boundStoreId: number
}

export type ArrivalProviderOptions = {
  storeName: string
  connectionStatus: string
  members: ArrivalProviderOption[]
  instances: ArrivalProviderInstanceOption[]
}

function queryString(values: Record<string, string | number | undefined>) {
  const query = new URLSearchParams()
  Object.entries(values).forEach(([key, value]) => {
    if (value !== undefined && value !== "") {
      query.set(key, String(value))
    }
  })
  const encoded = query.toString()
  return encoded ? `?${encoded}` : ""
}

export function fetchArrivalConnections(values: {
  page?: number
  limit?: number
  keyword?: string
}) {
  return request<ArrivalPageResult<ArrivalConnection>>(
    `/api/dashboard/arrival-connection/list${queryString(values)}`
  )
}

export function fetchArrivalAuthorizationOptions() {
  return request<ArrivalAuthorizationOption[]>(
    "/api/dashboard/arrival-connection/authorization/options"
  )
}

export function fetchArrivalProtocolInstanceOptions(storeId: number) {
  return request<ArrivalProtocolInstanceOption[]>(
    `/api/dashboard/arrival-connection/protocol-instance/options${queryString({ storeId })}`
  )
}

export function updateArrivalConnectionProvider(payload: {
  storeId: number
  contactProvider: string
  staticContactPlugId?: string
  wxWorkProtocolInstanceId?: number
}) {
  return request<ArrivalConnection>(
    "/api/dashboard/arrival-connection/provider/update",
    {
      method: "POST",
      body: JSON.stringify(payload),
    }
  )
}

export function createArrivalInvitation(payload: {
  storeId: number
  tenantAuthorizationId?: number
}) {
  return request<ArrivalInvitation>(
    "/api/dashboard/arrival-connection/invitation/create",
    {
      method: "POST",
      body: JSON.stringify(payload),
    }
  )
}

export function verifyArrivalConnection(connectionId: number) {
  return request<ArrivalConnectionVerification>(
    "/api/dashboard/arrival-connection/verify",
    {
      method: "POST",
      body: JSON.stringify({ connectionId }),
    }
  )
}

export function disableArrivalConnection(connectionId: number, reason: string) {
  return request<void>("/api/dashboard/arrival-connection/disable", {
    method: "POST",
    body: JSON.stringify({ connectionId, reason }),
  })
}

export function sendArrivalBindingCard(conversationId: number) {
  return request<void>(
    "/api/dashboard/conversation/send_arrival_binding_card",
    {
      method: "POST",
      body: JSON.stringify({ conversationId }),
    }
  )
}

export function fetchArrivalAuditLogs(values: {
  page?: number
  limit?: number
  storeId?: number
  action?: string
  result?: string
}) {
  return request<ArrivalPageResult<ArrivalAuditLog>>(
    `/api/dashboard/arrival-connection/audit/list${queryString(values)}`
  )
}

export function validateArrivalProviderInvitation(token: string) {
  return request<ArrivalProviderInvitation>(
    `/api/wecom/provider/invitation${queryString({ token })}`,
    { skipAuth: true }
  )
}

export function beginArrivalProviderAuthorization(invitationToken: string) {
  return request<ArrivalAuthorizationBegin>(
    "/api/wecom/provider/authorization/begin",
    {
      method: "POST",
      body: JSON.stringify({ invitationToken }),
      skipAuth: true,
    }
  )
}

export function fetchArrivalProviderOptions(state: string) {
  return request<ArrivalProviderOptions>(
    `/api/wecom/provider/options${queryString({ state })}`,
    { skipAuth: true }
  )
}

export function completeArrivalProviderConnection(payload: {
  authorizationState: string
  contactMemberToken: string
  wxWorkProtocolInstanceId: number
}) {
  return request<ArrivalConnectionVerification>(
    "/api/wecom/provider/connection/complete",
    {
      method: "POST",
      body: JSON.stringify(payload),
      skipAuth: true,
    }
  )
}

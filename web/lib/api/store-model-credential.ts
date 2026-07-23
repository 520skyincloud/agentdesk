import { request } from "@/lib/api/client"

export type StoreModelCredential = {
  tenantId: number
  storeId: number
  storeCode: string
  storeName: string
  activeProfileId: number
  activeProfileName: string
  activeProfileRevision: number
  activeModelNames: string[]
  pendingProfileId: number
  pendingProfileName: string
  pendingProfileRevision: number
  pendingModelNames: string[]
  hasKey: boolean
  keyMask: string
  fingerprintLast6: string
  credentialRevision: number
  credentialStatus: string
  candidateRevision: number
  candidateStatus: string
  candidateApprovalStatus: string
  candidateProfileId: number
  candidateProfileRevision: number
  candidateFingerprintLast6: string
  candidateRequestedAt?: string | null
  allowCredentialSelfService: boolean
  requireSupervisorApproval: boolean
  canSelfService: boolean
  lastTestStatus: string
  lastTestedAt?: string | null
  lastTestLatencyMs: number
  lastFastGPTSyncStatus: string
  lastFastGPTSyncedAt?: string | null
  lastErrorClass: string
  lastErrorMessage: string
}

export type StoreModelCredentialAudit = {
  id: number
  action: string
  result: string
  fromRevision: number
  toRevision: number
  profileId: number
  profileRevision: number
  fingerprintLast6: string
  operatorName: string
  operatorRole: string
  approverName: string
  errorClass: string
  requestId: string
  clientIp: string
  createdAt: string
}

export type StoreModelCredentialScope = {
  tenantId: number
  storeId: number
}

export function fetchStoreModelCredential(scope: StoreModelCredentialScope) {
  return request<StoreModelCredential>("/api/dashboard/store-model-credential/get", {
    method: "POST",
    body: JSON.stringify(scope),
  })
}

export function updateStoreModelCredential(
  scope: StoreModelCredentialScope,
  payload: { apiKey: string; currentPassword: string; confirmed: boolean },
) {
  return request<StoreModelCredential>("/api/dashboard/store-model-credential/update", {
    method: "POST",
    body: JSON.stringify({ ...scope, ...payload }),
  })
}

export function activatePendingStoreModelProfile(
  scope: { tenantId: number; storeId: number },
  payload: {
    templateId: number
    confirmRevision: number
    currentPassword: string
    confirmed: boolean
  },
) {
  return request<StoreModelCredential>("/api/dashboard/store-model-profile/activate_pending", {
    method: "POST",
    body: JSON.stringify({ ...scope, ...payload }),
  })
}

export function approveStoreModelCredential(
  scope: StoreModelCredentialScope,
  payload: { candidateRevision: number; currentPassword: string; confirmed: boolean },
) {
  return request<StoreModelCredential>("/api/dashboard/store-model-credential/approve", {
    method: "POST",
    body: JSON.stringify({ ...scope, ...payload }),
  })
}

export function rejectStoreModelCredential(
  scope: StoreModelCredentialScope,
  payload: { candidateRevision: number; currentPassword: string; confirmed: boolean },
) {
  return request<StoreModelCredential>("/api/dashboard/store-model-credential/reject", {
    method: "POST",
    body: JSON.stringify({ ...scope, ...payload }),
  })
}

export function disableStoreModelCredential(
  scope: StoreModelCredentialScope,
  payload: { currentPassword: string; confirmed: boolean },
) {
  return request<StoreModelCredential>("/api/dashboard/store-model-credential/disable", {
    method: "POST",
    body: JSON.stringify({ ...scope, ...payload }),
  })
}

export function fetchStoreModelCredentialAudit(scope: StoreModelCredentialScope, limit = 30) {
  return request<StoreModelCredentialAudit[]>("/api/dashboard/store-model-credential/audit", {
    method: "POST",
    body: JSON.stringify({ ...scope, limit }),
  })
}

export function updateStoreModelCredentialPolicy(payload: {
  tenantId: number
  storeIds: number[]
  allowCredentialSelfService: boolean
  requireSupervisorApproval: boolean
  currentPassword: string
  confirmed: boolean
}) {
  return request<void>("/api/dashboard/store-model-credential/policy", {
    method: "POST",
    body: JSON.stringify(payload),
  })
}

export function batchUpdateStoreModelCredentialPolicy(payload: {
  tenantId: number
  storeIds: number[]
  allowCredentialSelfService: boolean
  requireSupervisorApproval: boolean
  currentPassword: string
  confirmed: boolean
}) {
  return request<void>("/api/dashboard/store-model-credential/batch_policy", {
    method: "POST",
    body: JSON.stringify(payload),
  })
}

export function fetchOwnStoreModelCredential() {
  return request<StoreModelCredential>("/api/dashboard/store-workbench/model_credential")
}

export function updateOwnStoreModelCredential(payload: {
  apiKey: string
  currentPassword: string
  confirmed: boolean
}) {
  return request<StoreModelCredential>("/api/dashboard/store-workbench/model_credential/update", {
    method: "POST",
    body: JSON.stringify(payload),
  })
}

import { request, requestBlob } from "@/lib/api/client"

export type BillingQueryRequest = {
  tenantId?: number
  storeIds?: number[]
  startDate: string
  endDate: string
  modelName?: string
  requestId?: string
  limit?: number
}

export type BillingTenantOption = {
  tenantId: number
  tenantCode: string
  tenantName: string
}

export type BillingStoreOption = {
  tenantId: number
  tenantName: string
  storeId: number
  storeCode: string
  storeName: string
  bindingCount: number
  credentialStatus: string
  credentialRevision: number
  modelProfileRevision: number
  modelNames: string[]
}

export type BillingQueryOptions = {
  scopeMode: "platform" | "tenant" | "store"
  canFilterTenants: boolean
  defaultTenantId: number
  defaultStoreId: number
  defaultStoreStaffBindingId: number
  tenants: BillingTenantOption[]
  stores: BillingStoreOption[]
}

export type BillingTokenSummary = {
  unlimitedQuota: boolean
  totalGranted: number
  totalUsed: number
  totalAvailable: number
  grantedCny: number
  usedCny: number
  availableCny: number
  expiresAt: number
}

export type BillingOfficialUsageLog = {
  storeId: number
  storeName: string
  storeStaffBindingId: number
  storeStaffAccountName: string
  id: number
  createdAt: number
  modelName: string
  promptTokens: number
  completionTokens: number
  useTime: number
  quota: number
  costCny: number
  requestId: string
}

export type BillingOfficialStore = {
  tenantId: number
  tenantName: string
  storeId: number
  storeCode: string
  storeName: string
  storeStaffBindingId: number
  storeStaffAccountName: string
  credentialRevision: number
  modelProfileRevision: number
  modelNames: string[]
  status: "ready" | "failed"
  errorClass: string
  errorMessage: string
  truncated: boolean
  periodLogCount: number
  periodQuota: number
  periodCostCny: number
  periodPromptTokens: number
  periodOutputTokens: number
  summary: BillingTokenSummary
  logs: BillingOfficialUsageLog[]
}

export type BillingLocalUsageEvent = {
  id: number
  tenantId: number
  tenantName: string
  storeId: number
  storeName: string
  storeStaffBindingId: number
  storeStaffAccountName: string
  requestId: string
  stage: string
  operationType: string
  modelName: string
  modelProfileRevision: number
  usageSlot: string
  credentialRevision: number
  promptTokens: number
  completionTokens: number
  cachedPromptTokens: number
  latencyMs: number
  status: string
  errorClass: string
  createdAt: string
}

export type BillingReconciliationItem = {
  storeId: number
  storeName: string
  storeStaffBindingId: number
  storeStaffAccountName: string
  requestId: string
  status: "matched" | "official_only" | "local_only"
  officialModel: string
  localModel: string
  officialTokens: number
  localTokens: number
  officialCostCny: number
  officialAt: string | null
  localAt: string | null
}

export type BillingQueryResult = {
  scopeMode: "platform" | "tenant" | "store"
  tenantId: number
  tenantName: string
  startDate: string
  endDate: string
  businessTimezone: string
  queriedAt: string
  official: {
    aggregate: {
      storeCount: number
      successfulStores: number
      failedStores: number
      credentialAccountCount: number
      successfulCredentialAccounts: number
      failedCredentialAccounts: number
      logCount: number
      periodQuota: number
      periodCostCny: number
      periodPromptTokens: number
      periodOutputTokens: number
    }
    stores: BillingOfficialStore[]
  }
  local: {
    aggregate: {
      eventCount: number
      requestCount: number
      failedCount: number
      promptTokens: number
      completionTokens: number
      cachedPromptTokens: number
    }
    events: BillingLocalUsageEvent[]
    truncated: boolean
  }
  reconciliation: {
    officialLogCount: number
    localGatewayCallCount: number
    matchedCount: number
    officialOnlyCount: number
    localOnlyCount: number
    missingRequestIdCount: number
    matchRate: number
    items: BillingReconciliationItem[]
    truncated: boolean
  }
}

export function fetchBillingQueryOptions() {
  return request<BillingQueryOptions>("/api/dashboard/billing-query/options", {
    method: "POST",
    body: JSON.stringify({}),
  })
}

export function fetchBillingQuery(payload: BillingQueryRequest) {
  return request<BillingQueryResult>("/api/dashboard/billing-query/get", {
    method: "POST",
    body: JSON.stringify(payload),
  })
}

export function exportBillingQuery(payload: BillingQueryRequest) {
  return requestBlob("/api/dashboard/billing-query/export", {
    method: "POST",
    body: JSON.stringify(payload),
  })
}

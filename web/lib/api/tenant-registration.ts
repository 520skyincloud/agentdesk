import { request } from "@/lib/api/client"
import type { TenantInvitation, TenantPageResult } from "@/lib/api/tenant"
import {
  Status,
  TenantRegistrationReviewDecision,
  UserApprovalStatus,
  UserRegistrationSource,
} from "@/lib/generated/enums"

export type TenantInvitationValidation = {
  valid: boolean
  tenantLegalName?: string
  tenantShortName?: string
}

export type TenantRegistrationPayload = {
  username: string
  nickname: string
  mobile: string
  email: string
  password: string
  confirmPassword: string
  invitationCode: string
}

export type TenantRegistrationResult = {
  userId: number
  username: string
  tenantName: string
  approvalStatus: UserApprovalStatus
  replayed: boolean
}

export type TenantRegistrationRecord = {
  userId: number
  username: string
  nickname: string
  mobile?: string
  email?: string
  status: Status
  approvalStatus: UserApprovalStatus
  approvalRemark: string
  registrationSource: UserRegistrationSource
  createdAt: string
  reviewedAt?: string
  reviewedBy?: number
}

export type TenantRegistrationListQuery = {
  approvalStatus?: UserApprovalStatus | "all"
  username?: string
  nickname?: string
  page?: number
  limit?: number
}

export type ReviewTenantRegistrationPayload = {
  userId: number
  decision: TenantRegistrationReviewDecision
  roleIds: number[]
  storeName: string
  remark: string
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

export function validateTenantInvitation(invitationCode: string) {
  return request<TenantInvitationValidation>("/api/auth/register/validate_invite", {
    method: "POST",
    body: JSON.stringify({ invitationCode }),
    skipAuth: true,
  })
}

export function registerTenantUser(
  payload: TenantRegistrationPayload,
  requestId: string
) {
  return request<TenantRegistrationResult>("/api/auth/register", {
    method: "POST",
    headers: { "X-Request-Id": requestId },
    body: JSON.stringify(payload),
    skipAuth: true,
  })
}

export function fetchCurrentTenantInvitation() {
  return request<TenantInvitation>("/api/dashboard/tenant-invitation/current")
}

export function rotateCurrentTenantInvitation() {
  return request<TenantInvitation>("/api/dashboard/tenant-invitation/rotate", {
    method: "POST",
  })
}

export function fetchTenantRegistrations(query: TenantRegistrationListQuery) {
  return request<TenantPageResult<TenantRegistrationRecord>>(
    `/api/dashboard/tenant-registration/list${toQueryString({
      approvalStatus:
        query.approvalStatus && query.approvalStatus !== "all"
          ? query.approvalStatus
          : undefined,
      username: query.username?.trim() || undefined,
      nickname: query.nickname?.trim() || undefined,
      page: query.page,
      limit: query.limit,
    })}`
  )
}

export function reviewTenantRegistration(
  payload: ReviewTenantRegistrationPayload,
  requestId: string
) {
  return request<TenantRegistrationRecord>("/api/dashboard/tenant-registration/review", {
    method: "POST",
    headers: { "X-Request-Id": requestId },
    body: JSON.stringify(payload),
  })
}

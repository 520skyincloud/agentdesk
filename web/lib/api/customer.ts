import { request } from "@/lib/api/client"
import type { PageResult } from "@/lib/api/admin"
import type { ContactType } from "@/lib/generated/enums"
import type { AgentCustomerTag } from "@/lib/api/agent"

export type AdminCustomer = {
  id: number
  name: string
  avatar: string
  gender: number
  lastActiveAt?: string
  primaryMobile: string
  primaryEmail: string
  status: number
  remark: string
  storeRelations?: StoreCustomerRelation[]
  createdAt: string
  updatedAt: string
}

export type StoreCustomerRelation = {
  id: number
  customerId: number
  storeId: number
  storeName: string
  wxWorkInstanceId: number
  wxWorkInstanceName: string
  lastConversationId: number
  lastActiveAt?: string
  visitCount: number
  tags: string
  stableNotes: string
  customerTags: AgentCustomerTag[]
  status: number
  createdAt: string
  updatedAt: string
}

export type CreateAdminCustomerPayload = {
  name: string
  gender: number
  primaryMobile: string
  primaryEmail: string
  remark: string
}

export type UpdateAdminCustomerPayload = CreateAdminCustomerPayload & {
  id: number
}

/** Matches POST /customer/save/profile request body. */
export type SaveCustomerProfileContactLine = {
  id?: number
  contactType: ContactType | string
  contactValue: string
  remark: string
  isPrimary: boolean
}

export type SaveCustomerProfilePayload = {
  id?: number
  name: string
  gender: number
  remark: string
  contacts: SaveCustomerProfileContactLine[]
}

/** Matches POST /customer/list JSON body. */
export type CustomerListRequest = {
  page: number
  limit: number
  status?: number
  gender?: number
  /** Fuzzy match against customer name, primary phone, primary email, and contacts. */
  keyword?: string
}

export function fetchCustomers(body: CustomerListRequest) {
  return request<PageResult<AdminCustomer>>("/api/dashboard/customer/list", {
    method: "POST",
    body: JSON.stringify(body),
  })
}

export function fetchCustomer(id: number) {
  return request<AdminCustomer | null>(`/api/dashboard/customer/${id}`)
}

export function fetchCustomerStoreRelations(id: number) {
  return request<StoreCustomerRelation[]>(`/api/dashboard/customer/${id}/store_relations`)
}

export function createCustomer(payload: CreateAdminCustomerPayload) {
  return request<AdminCustomer>("/api/dashboard/customer/create", {
    method: "POST",
    body: JSON.stringify(payload),
  })
}

/** Saves the customer profile and the full contact list in one request and transaction. */
export function saveCustomerProfile(payload: SaveCustomerProfilePayload) {
  return request<AdminCustomer>("/api/dashboard/customer/save_profile", {
    method: "POST",
    body: JSON.stringify(payload),
  })
}

export function updateCustomer(payload: UpdateAdminCustomerPayload) {
  return request<void>("/api/dashboard/customer/update", {
    method: "POST",
    body: JSON.stringify(payload),
  })
}

export function updateCustomerStatus(id: number, status: number) {
  return request<void>("/api/dashboard/customer/update_status", {
    method: "POST",
    body: JSON.stringify({ id, status }),
  })
}

export function deleteCustomer(id: number) {
  return request<void>("/api/dashboard/customer/delete", {
    method: "POST",
    body: JSON.stringify({ id }),
  })
}

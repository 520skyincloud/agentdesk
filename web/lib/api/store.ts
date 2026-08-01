import { request } from "@/lib/api/client"
import type { PageResult } from "@/lib/api/admin"

export type Store = {
  id: number
  tenantId: number
  storeCode: string
  name: string
  brandName: string
  address: string
  navigationName: string
  longitude: string
  latitude: string
  mapProvider: string
  contactPhone: string
  knowledgeBaseId: number
  activeStaffCount: number
  currentInstanceCount: number
  status: number
  remark: string
  createdAt: string
  updatedAt: string
}

export type StoreOption = Pick<Store, "id" | "storeCode" | "name">

export type StorePayload = {
  name: string
  brandName: string
  address: string
  navigationName: string
  longitude: string
  latitude: string
  mapProvider: string
  contactPhone: string
  remark: string
}

function toQueryString(query?: Record<string, string | number | undefined>) {
  const params = new URLSearchParams()
  Object.entries(query ?? {}).forEach(([key, value]) => {
    if (value === undefined || value === "") return
    params.set(key, String(value))
  })
  const output = params.toString()
  return output ? `?${output}` : ""
}

export function fetchStores(query?: Record<string, string | number | undefined>) {
  return request<PageResult<Store>>(
    `/api/dashboard/store/list${toQueryString(query)}`
  )
}

export function fetchStore(id: number) {
  return request<Store>(`/api/dashboard/store/${id}`)
}

export function fetchStoreOptions() {
  return request<StoreOption[]>("/api/dashboard/store/options")
}

export function createStore(payload: StorePayload) {
  return request<Store>("/api/dashboard/store/create", {
    method: "POST",
    body: JSON.stringify(payload),
  })
}

export function updateStore(id: number, payload: StorePayload) {
  return request<Store>("/api/dashboard/store/update", {
    method: "POST",
    body: JSON.stringify({ id, ...payload }),
  })
}

export function updateStoreStatus(id: number, status: number) {
  return request<void>("/api/dashboard/store/update_status", {
    method: "POST",
    body: JSON.stringify({ id, status }),
  })
}

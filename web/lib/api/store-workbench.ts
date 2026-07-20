import { request } from "@/lib/api/client"

export type StoreWorkbenchData = {
  bound: boolean
  tenantId: number
  tenantName: string
  userId: number
  username: string
  nickname: string
  avatar: string
  bindingId: number
  bindingStatus: number
  storeId: number
  storeCode: string
  storeName: string
  brandName: string
  agentTeamId: number
  agentTeamName: string
  wxWorkInstanceId: number
  wxWorkEmployeeId: string
  wxWorkEmployeeName: string
  wxWorkEmployeeAvatar: string
  wxWorkHealthStatus: string
  wxWorkLastHeartbeatAt?: string
  aiReplyEnabled: boolean
  knowledgeBaseId: number
  knowledgeBaseName: string
  managedMode: string
  serviceHours: string
  storeRoomConversationId: string
  storeRoomNotifyEnabled: boolean
  storeRoomAtList: string
  fallbackToHQ: boolean
  manualTimeoutMinutes: number
  storeAddress: string
  storeNavigationName: string
  storeLongitude: string
  storeLatitude: string
  storeMapProvider: string
  updatedAt?: string
}

export type UpdateStoreWorkbenchPayload = {
  managedMode: string
  serviceHours: string
  storeRoomConversationId: string
  storeRoomNotifyEnabled: boolean
  storeRoomAtList: string
  manualTimeoutMinutes: number
  storeAddress: string
  storeNavigationName: string
  storeLongitude: string
  storeLatitude: string
  storeMapProvider: string
}

export type StoreWorkbenchRoom = {
  roomId: string
  conversationId: string
  name: string
  owner: string
  memberCount: number
}

export type StoreWorkbenchRoomMember = {
  userId: string
  name: string
  displayName?: string
  realName?: string
  roomRemark?: string
  accountId?: string
  avatar: string
}

export function fetchStoreWorkbench() {
  return request<StoreWorkbenchData>("/api/dashboard/store-workbench/current")
}

export function updateStoreWorkbench(payload: UpdateStoreWorkbenchPayload) {
  return request<StoreWorkbenchData>("/api/dashboard/store-workbench/update", {
    method: "POST",
    body: JSON.stringify(payload),
  })
}

export function fetchStoreWorkbenchRooms(payload: { startIndex?: number; limit?: number } = {}) {
  return request<StoreWorkbenchRoom[]>("/api/dashboard/store-workbench/room_list", {
    method: "POST",
    body: JSON.stringify({
      startIndex: payload.startIndex ?? 0,
      limit: payload.limit ?? 200,
    }),
  })
}

export function fetchStoreWorkbenchRoomMembers(roomId: string) {
  return request<StoreWorkbenchRoomMember[]>("/api/dashboard/store-workbench/room_member_list", {
    method: "POST",
    body: JSON.stringify({ roomId }),
  })
}

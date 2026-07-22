import { request } from "@/lib/api/client"
import type { TagTree } from "@/lib/api/admin"

export type Paging = {
  page: number
  limit: number
  total: number
}

export type PageResult<T> = {
  results: T[]
  page: Paging
}

export type CursorResult<T> = {
  results: T[]
  cursor: string
  hasMore: boolean
}

export type AgentCustomerTag = {
  id: number
  tagId: number
  name: string
  source: "ai" | "manual" | string
  confidence: number
  evidenceCount: number
  manualProtected: boolean
  updatedAt?: string
}

export type CustomerTagChangeLog = {
  id: number
  action: "add" | "refresh" | "replace" | "remove" | string
  oldTagId: number
  oldTagName?: string
  newTagId: number
  newTagName?: string
  evidenceMessageIds: number[]
  source: "ai" | "manual" | "system" | string
  confidence: number
  operatorType: string
  operatorId: number
  operatorName: string
  createdAt: string
}

export type AgentConversationParticipant = {
  id: number
  participantType: string
  participantId: number
  externalParticipantId?: string
  joinedAt?: string
  leftAt?: string
  status: number
}

export type AgentConversationManualAttention = {
  dot: boolean
  level: "none" | "normal" | "urgent" | "serving" | string
  label: string
  expiresAt?: string
}

export type AgentConversation = {
  id: number
  aiAgentId?: number
  channelId?: number
  customerId?: number
  customerName: string
  customerAvatar?: string
  status: number
  serviceMode: number
  priority: number
  currentAssigneeId: number
  currentAssigneeName?: string
  currentTeamId: number
  currentTeamName?: string
  lastMessageId: number
  lastMessageAt?: string
  lastActiveAt?: string
  lastMessageSummary?: string
  customerUnreadCount: number
  agentUnreadCount: number
  customerLastReadMessageId: number
  customerLastReadSeqNo: number
  customerLastReadAt?: string
  agentLastReadMessageId: number
  agentLastReadSeqNo: number
  agentLastReadAt?: string
  customerOnline: boolean
  closedAt?: string
  routeStatus?: string
  routeStatusLabel?: string
  routeTarget?: string
  handoffReason?: string
  needHumanFollowUp?: boolean
  autoHandoffEnabled?: boolean
  manualExpireAt?: string
  manualAttention?: AgentConversationManualAttention
  storeId?: number
  storeName?: string
  wxWorkInstanceId?: number
  wxWorkExternalUserId?: string
  wxWorkEmployeeName?: string
  wxWorkEmployeeUserId?: string
  customerTags?: AgentCustomerTag[]
  participants?: AgentConversationParticipant[]
}

export type AgentConversationDetail = AgentConversation & {
  participants?: AgentConversationParticipant[]
}

export type AgentMessage = {
  id: number
  conversationId: number
  clientMsgId?: string
  senderType: string
  senderId: number
  senderName?: string
  senderAvatar?: string
  sendSource?: string
  sendSourceLabel?: string
  messageType: string
  content: string
  payload?: string
  seqNo: number
  sendStatus: number
  sentAt?: string
  deliveredAt?: string
  readAt?: string
  customerRead: boolean
  customerReadAt?: string
  agentRead: boolean
  agentReadAt?: string
  recalledAt?: string
  quotedMessageId?: number
}

export type AgentAsset = {
  id: number
  assetId: string
  provider: string
  storageKey: string
  filename: string
  fileSize: number
  mimeType: string
  status: number
  url: string
  createdAt: string
  updatedAt: string
  createUserId: number
  createUserName: string
  updateUserId: number
  updateUserName: string
}

function toQueryString(query?: Record<string, string | number | undefined>) {
  if (!query) {
    return ""
  }

  const params = new URLSearchParams()
  Object.entries(query).forEach(([key, value]) => {
    if (value === undefined || value === "") {
      return
    }
    params.set(key, String(value))
  })
  const output = params.toString()
  return output ? `?${output}` : ""
}

export function fetchAgentConversations(
  query?: Record<string, string | number | undefined>
) {
  return request<PageResult<AgentConversation>>(
    `/api/dashboard/conversation/conversations${toQueryString(query)}`
  )
}

export function fetchAgentConversationDetail(id: number) {
  return request<AgentConversationDetail>(`/api/dashboard/conversation/${id}`)
}

export function fetchAgentMessages(
  query?: Record<string, string | number | undefined>
) {
  return request<CursorResult<AgentMessage>>(
    `/api/dashboard/conversation/message_list${toQueryString(query)}`
  )
}

export function sendAgentMessage(payload: {
  conversationId: number
  messageType: string
  content: string
  payload?: string
  clientMsgId?: string
}) {
  return request<AgentMessage>("/api/dashboard/conversation/send_message", {
    method: "POST",
    body: JSON.stringify(payload),
  })
}

export function recallAgentMessage(messageId: number) {
  return request<AgentMessage>("/api/dashboard/conversation/recall_message", {
    method: "POST",
    body: JSON.stringify({ messageId }),
  })
}

export function markAgentMessageRead(conversationId: number, messageId = 0) {
  return request<void>("/api/dashboard/conversation/read", {
    method: "POST",
    body: JSON.stringify({ conversationId, messageId }),
  })
}

export function uploadAgentConversationImage(conversationId: number, file: File) {
  const formData = new FormData()
  formData.set("conversationId", String(conversationId))
  formData.set("file", file)
  return request<AgentAsset>("/api/dashboard/conversation/upload_image", {
    method: "POST",
    body: formData,
  })
}

export function uploadAgentConversationAttachment(conversationId: number, file: File) {
  const formData = new FormData()
  formData.set("conversationId", String(conversationId))
  formData.set("file", file)
  return request<AgentAsset>("/api/dashboard/conversation/upload_attachment", {
    method: "POST",
    body: formData,
  })
}

export function closeAgentConversation(
  conversationId: number,
  closeReason: string
) {
  return request<void>("/api/dashboard/conversation/close", {
    method: "POST",
    body: JSON.stringify({ conversationId, closeReason }),
  })
}

export function assignAgentConversation(
  conversationId: number,
  assigneeId: number,
  reason: string
) {
  return request<void>("/api/dashboard/conversation/assign", {
    method: "POST",
    body: JSON.stringify({ conversationId, assigneeId, reason }),
  })
}

export function transferAgentConversation(
  conversationId: number,
  toUserId: number,
  reason: string
) {
  return request<void>("/api/dashboard/conversation/transfer", {
    method: "POST",
    body: JSON.stringify({ conversationId, toUserId, reason }),
  })
}

export function setAgentConversationAutoHandoffEnabled(
  conversationId: number,
  autoHandoffEnabled: boolean,
) {
  return request<void>("/api/dashboard/conversation/set_auto_handoff_enabled", {
    method: "POST",
    body: JSON.stringify({ conversationId, autoHandoffEnabled }),
  })
}

export function linkConversationToCustomer(payload: {
  conversationId: number
  customerId: number
}) {
  return request<void>("/api/dashboard/conversation/link_customer", {
    method: "POST",
    body: JSON.stringify(payload),
  })
}

export function addCustomerTag(payload: {
  conversationId: number
  tagId: number
}) {
  return request<void>("/api/dashboard/conversation/customer_tag/add", {
    method: "POST",
    body: JSON.stringify(payload),
  })
}

export function removeCustomerTag(payload: {
  conversationId: number
  tagId: number
}) {
  return request<void>("/api/dashboard/conversation/customer_tag/remove", {
    method: "POST",
    body: JSON.stringify(payload),
  })
}

export function replaceCustomerTag(payload: {
  conversationId: number
  oldTagId: number
  newTagId: number
}) {
  return request<void>("/api/dashboard/conversation/customer_tag/replace", {
    method: "POST",
    body: JSON.stringify(payload),
  })
}

export function fetchCustomerTagOptions(conversationId: number) {
  const params = new URLSearchParams({ conversationId: String(conversationId) })
  return request<TagTree[]>(`/api/dashboard/conversation/customer_tag/options?${params}`)
}

export function fetchCustomerTagChangeLogs(
  conversationId: number,
  page = 1,
  limit = 20
) {
  const params = new URLSearchParams({
    conversationId: String(conversationId),
    page: String(page),
    limit: String(limit),
  })
  return request<PageResult<CustomerTagChangeLog>>(
    `/api/dashboard/conversation/customer_tag/change_log?${params}`
  )
}

export function retryConversationEvolution(conversationId: number) {
  return request<void>("/api/dashboard/conversation/evolution/retry", {
    method: "POST",
    body: JSON.stringify({ conversationId }),
  })
}

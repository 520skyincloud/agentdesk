import { request } from "@/lib/api/client"
import type { Tag } from "@/lib/api/admin"

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
  standardName: string
  semanticKey: string
  conflictGroup?: string
  source: string
  confidence: number
  evidenceCount: number
  manualProtected: boolean
  updatedAt?: string
}

export type CustomerTagChangeLog = {
  id: number
  action: string
  oldTagId: number
  oldTagName?: string
  newTagId: number
  newTagName?: string
  evidenceMessageIds: number[]
  source: string
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

export type AgentConversationTakeoverState = {
  requestId?: number
  requestStatus?: string
  requesterUserId?: number
  requesterName?: string
  teamId?: number
  teamName?: string
  reason?: string
  reviewRemark?: string
  requestedAt?: string
  reviewedAt?: string
  canReply: boolean
  canRequest: boolean
  canDirectTakeover: boolean
  canReview: boolean
  canResumeAi: boolean
  isCurrentAssignee: boolean
  pendingForMe: boolean
  pendingForAnother: boolean
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
  takeoverState?: AgentConversationTakeoverState
  storeId?: number
  storeName?: string
  storeStaffBindingId?: number
  wxWorkInstanceId?: number
  currentSessionNo?: number
  wxWorkReplyReady: boolean
  wxWorkReplyStatus?: "ready" | "waiting_target_message" | "unavailable" | "not_applicable" | string
  wxWorkExternalUserId?: string
  wxWorkEmployeeName?: string
  wxWorkEmployeeUserId?: string
  customerTags?: AgentCustomerTag[]
  participants?: AgentConversationParticipant[]
  channelSessions?: ConversationChannelSession[]
  historySegments?: ConversationHistorySegment[]
  relatedConversations?: AgentConversation[]
  continuityLinks?: ConversationContinuityLink[]
}

export type AgentConversationDetail = AgentConversation

export type ConversationChannelSession = {
  sessionNo: number
  storeId: number
  storeStaffBindingId: number
  wxWorkInstanceId: number
  channelId: number
  startReason: string
  storeStaffDisplayName: string
  wxWorkEmployeeDisplayName: string
  startedAt: string
  endedAt?: string
  status: number
}

export type ConversationHistorySegment = ConversationChannelSession & {
  index: number
  conversationId: number
  inheritedHistory: boolean
  currentConversation: boolean
}

export type ConversationContinuityLink = {
  predecessorConversationId: number
  successorConversationId: number
  reason: string
  createdAt: string
}

export type AgentMessage = {
  id: number
  conversationId: number
  sessionNo: number
  historySegmentIndex?: number
  inheritedHistory: boolean
  historicalOnly: boolean
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

export type StoreConversationInheritancePreviewItem = {
  conversationId: number
  customerId: number
  customerName: string
  lastMessageAt?: string
  currentSessionNo: number
  eligible: boolean
  resolutionMode: "create_successor" | "link_existing" | string
  targetConversationId?: number
  conflictReason?: string
}

export type StoreConversationInheritancePreview = {
  sourceStoreStaffBindingId: number
  targetStoreStaffBindingId: number
  targetWxWorkInstanceId: number
  storeId: number
  storeName: string
  previewVersion: string
  eligibleCount: number
  linkedExistingCount: number
  conflictCount: number
  items: StoreConversationInheritancePreviewItem[]
}

export type BatchStoreConversationInheritanceResult = {
  inheritedCount: number
  createdCount: number
  linkedCount: number
  conversationIds: number[]
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

export function requestAgentConversationTakeover(
  conversationId: number,
  reason: string,
) {
  return request<void>("/api/dashboard/conversation/takeover/request", {
    method: "POST",
    body: JSON.stringify({ conversationId, reason }),
  })
}

export function directTakeoverAgentConversation(
  conversationId: number,
  reason: string,
) {
  return request<void>("/api/dashboard/conversation/takeover/direct", {
    method: "POST",
    body: JSON.stringify({ conversationId, reason }),
  })
}

export function reviewAgentConversationTakeover(payload: {
  requestId: number
  approved: boolean
  remark?: string
}) {
  return request<void>("/api/dashboard/conversation/takeover/review", {
    method: "POST",
    body: JSON.stringify(payload),
  })
}

export function resumeAgentConversationAI(conversationId: number) {
  return request<void>("/api/dashboard/conversation/resume_ai", {
    method: "POST",
    body: JSON.stringify({ conversationId }),
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

export function inheritStoreConversation(payload: {
  conversationId: number
  targetStoreStaffBindingId: number
  targetWxWorkInstanceId: number
  reason: string
}) {
  return request<AgentConversation>("/api/dashboard/conversation/inherit", {
    method: "POST",
    body: JSON.stringify(payload),
  })
}

export function previewStoreConversationInheritance(payload: {
  sourceStoreStaffBindingId: number
  targetStoreStaffBindingId: number
  targetWxWorkInstanceId: number
}) {
  return request<StoreConversationInheritancePreview>(
    "/api/dashboard/conversation/inherit/preview",
    {
      method: "POST",
      body: JSON.stringify(payload),
    }
  )
}

export function batchInheritStoreConversations(payload: {
  sourceStoreStaffBindingId: number
  targetStoreStaffBindingId: number
  targetWxWorkInstanceId: number
  conversationIds: number[]
  previewVersion: string
  reason: string
}) {
  return request<BatchStoreConversationInheritanceResult>(
    "/api/dashboard/conversation/inherit/batch",
    {
      method: "POST",
      body: JSON.stringify(payload),
    }
  )
}

export function fetchCustomerTagOptions(conversationId: number) {
  return request<Tag[]>(
    `/api/dashboard/conversation/customer_tag/options?conversationId=${conversationId}`
  )
}

export function fetchCustomerTagChangeLogs(
  conversationId: number,
  query?: { page?: number; limit?: number },
) {
  return request<PageResult<CustomerTagChangeLog>>(
    `/api/dashboard/conversation/customer_tag/change_log${toQueryString({
      conversationId,
      page: query?.page,
      limit: query?.limit,
    })}`
  )
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

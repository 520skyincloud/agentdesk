import { request, requestBlob } from "@/lib/api/client"
import {
  AgentPresenceStatus,
  AnalyticsDataQuality,
  AnalyticsFactOrigin,
  ConversationEvaluationStatus,
  QualityInspectionStatus,
  QualityRuleType,
  QualitySamplingStatus,
} from "@/lib/generated/enums"

export type Paging = { page: number; limit: number; total: number }
export type PageResult<T> = { results: T[]; page: Paging }

function queryString(values: Record<string, string | number | boolean | undefined>) {
  const query = new URLSearchParams()
  Object.entries(values).forEach(([key, value]) => {
    if (value === undefined || value === "") return
    query.set(key, String(value))
  })
  const output = query.toString()
  return output ? `?${output}` : ""
}

export type AnalyticsSummary = {
  sessionCount: number
  uniqueCustomerCount: number
  closedSessionCount: number
  humanQueueCount: number
  assignedCount: number
  humanRepliedCount: number
  unansweredCount: number
  queueFailureCount: number
  transferSessionCount: number
  repeatConsultationCount: number
  totalMessageCount: number
  customerMessageCount: number
  aiMessageCount: number
  humanMessageCount: number
  assignmentAccessRate: number
  effectiveAccessRate: number
  transferRate: number
  repeatConsultationRate: number
  averageQueueSeconds: number
  p50QueueSeconds: number
  p90QueueSeconds: number
  averageFirstReplySeconds: number
  p50FirstReplySeconds: number
  p90FirstReplySeconds: number
  averageResponseSeconds: number
  p50ResponseSeconds: number
  p90ResponseSeconds: number
  averageHumanWaitSeconds: number
  p50HumanWaitSeconds: number
  p90HumanWaitSeconds: number
  averageSessionSeconds: number
  p50SessionSeconds: number
  p90SessionSeconds: number
  averageMessagesPerSession: number
  queueSlaRate: number
  firstReplySlaRate: number
  responseSlaRate: number
  qualityInspectableCount: number
  qualityInspectionCount: number
  qualityPendingCount: number
  qualityPassedCount: number
  qualityFailedCount: number
  qualityCoverageRate: number
  qualityPassRate: number
  averageQualityScore: number
  evaluationInviteCount: number
  evaluationSubmittedCount: number
  satisfiedCount: number
  evaluationParticipationRate: number
  satisfactionRate: number
  averageSatisfaction: number
  exactSessionCount: number
  estimatedSessionCount: number
  incompleteSessionCount: number
}

export type AnalyticsTrend = {
  date: string
  sessions: number
  humanQueues: number
  humanReplies: number
  messages: number
  averageQueue: number
  averageFirstReply: number
  averageResponse: number
  averageSession: number
}

export type AnalyticsAgent = {
  agentId: number
  agentName: string
  teamId: number
  teamName: string
  squadNames: string[]
  currentStatus: "offline" | AgentPresenceStatus | string
  currentActiveCount: number
  maxConcurrentCount: number
  assignedCount: number
  repliedCount: number
  unansweredCount: number
  humanMessageCount: number
  responseCount: number
  serviceSeconds: number
  averageFirstReplySeconds: number
  p50FirstReplySeconds: number
  p90FirstReplySeconds: number
  averageResponseSeconds: number
  p50ResponseSeconds: number
  p90ResponseSeconds: number
  responseSlaRate: number
  onlineSeconds: number
  idleSeconds: number
  busySeconds: number
  breakSeconds: number
  firstOnlineAt?: string
  lastOnlineAt?: string
  utilizationRate: number
  qualityInspectableCount: number
  qualityInspectionCount: number
  qualityPendingCount: number
  qualityPassedCount: number
  qualityFailedCount: number
  qualityPassRate: number
  averageQualityScore: number
  evaluationInviteCount: number
  evaluationSubmittedCount: number
  satisfiedCount: number
  evaluationParticipationRate: number
  satisfactionRate: number
  averageSatisfaction: number
}

export type AnalyticsSource = {
  storeId: number
  storeName: string
  wxWorkInstanceId: number
  wxWorkEmployeeName: string
  sessionCount: number
  humanQueueCount: number
  humanRepliedCount: number
  messageCount: number
  averageFirstReply: number
  effectiveAccessRate: number
  qualityInspectableCount: number
  qualityInspectionCount: number
  qualityPassedCount: number
  qualityCoverageRate: number
  qualityPassRate: number
  averageQualityScore: number
  evaluationInviteCount: number
  evaluationSubmittedCount: number
  satisfiedCount: number
  evaluationParticipationRate: number
  satisfactionRate: number
  averageSatisfaction: number
}

export type AnalyticsRealtime = {
  openSessionCount: number
  aiActiveCount: number
  queueingCount: number
  assignedActiveCount: number
  waitingReplyCount: number
  longestQueueSeconds: number
  queueSlaAlertCount: number
  onlineAgentCount: number
  idleAgentCount: number
  busyAgentCount: number
  breakAgentCount: number
  offlineAgentCount: number
  availableCapacity: number
  todaySessionCount: number
  todayQueueCount: number
  todayAssignedCount: number
  todayHumanRepliedCount: number
  todayTransferCount: number
  todayQueueFailureCount: number
  todayMessageCount: number
  todayAverageQueueSeconds: number
  todayAverageFirstReplySeconds: number
}

export type AnalyticsDistribution = {
  key: string
  label: string
  count: number
  rate: number
}

export type AnalyticsOverview = {
  startAt: string
  endAt: string
  generatedAt: string
  summary: AnalyticsSummary
  realtime: AnalyticsRealtime
  trend: AnalyticsTrend[]
  firstReplyDistribution: AnalyticsDistribution[]
  responseDistribution: AnalyticsDistribution[]
  sessionDurationDistribution: AnalyticsDistribution[]
  agents: AnalyticsAgent[]
  sources: AnalyticsSource[]
  dispatch: {
    decisionCount: number
    selectedCount: number
    autoCount: number
    manualCount: number
    ruleCount: number
    modelCount: number
    hybridCount: number
    fallbackCount: number
    failedCount: number
    staleCount: number
    overrideCount: number
    transferCount: number
    autoRate: number
    averageDecisionLatencyMillis: number
  }
}

export type AnalyticsDimensionItem = {
  id: number
  name: string
  parentId: number
}

export type AnalyticsDimensions = {
  teams: AnalyticsDimensionItem[]
  squads: AnalyticsDimensionItem[]
  agents: AnalyticsDimensionItem[]
  channels: AnalyticsDimensionItem[]
  stores: AnalyticsDimensionItem[]
  wxWorkInstances: AnalyticsDimensionItem[]
}

export type ServiceSession = {
  id: number
  conversationId: number
  sessionNo: number
  customerId: number
  customerName: string
  channelId: number
  channelName: string
  storeId: number
  storeName: string
  wxWorkInstanceId: number
  wxWorkEmployeeName: string
  status: string
  startedAt: string
  queueEnteredAt?: string
  assignedAt?: string
  firstHumanReplyAt?: string
  endedAt?: string
  assignedTeamId: number
  assignedTeamName: string
  assignedAgentId: number
  assignedAgentName: string
  customerMessageCount: number
  aiMessageCount: number
  humanMessageCount: number
  assignmentCount: number
  transferCount: number
  queueSeconds: number
  firstResponseSeconds: number
  totalHumanWaitSeconds: number
  closeReason: string
  lastMessageAt?: string
  resolutionCode: string
  categoryCode: string
  tagIds: number[]
  sessionSummary: string
  factOrigin: AnalyticsFactOrigin
  dataQuality: AnalyticsDataQuality
  estimatedFields: string[]
}

export type SessionMessage = {
  id: number
  conversationId: number
  senderType: "customer" | "agent" | "ai" | "system" | string
  senderId: number
  senderName?: string
  messageType: string
  content: string
  payload?: string
  seqNo: number
  sentAt?: string
  recalledAt?: string
}

export type QualityTemplateItem = {
  id: number
  code: string
  name: string
  description: string
  ruleType: QualityRuleType
  metricCode: string
  maxScore: number
  required: boolean
  hardFail: boolean
  sortNo: number
}

export type QualityTemplate = {
  id: number
  name: string
  description: string
  totalScore: number
  passScore: number
  version: number
  isDefault: boolean
  items: QualityTemplateItem[]
}

export type QualityInspectionItem = {
  templateItemId: number
  itemCode: string
  itemName: string
  ruleType: QualityRuleType
  maxScore: number
  score: number
  passed: boolean
  hardFailed: boolean
  metricValue: string
  evidence: string
  messageIds: number[]
  comment: string
}

export type QualityInspection = {
  id: number
  conversationId: number
  sessionNo: number
  assignmentId: number
  agentId: number
  agentName: string
  teamId: number
  teamName: string
  templateId: number
  status: QualityInspectionStatus
  totalScore: number
  maxScore: number
  result: string
  hardFailed: boolean
  summary: string
  inspectedBy: number
  inspectedAt?: string
  items: QualityInspectionItem[]
}

export type QualityPoolEntry = {
  assignmentId: number
  conversationId: number
  sessionNo: number
  customerName: string
  agentId: number
  agentName: string
  teamId: number
  teamName: string
  storeName: string
  wxWorkEmployeeName: string
  assignedAt: string
  finishedAt?: string
  humanReplyCount: number
  inspection?: QualityInspection
}

export function fetchAnalyticsOverview(query: Record<string, string | number | undefined>) {
  return request<AnalyticsOverview>(`/api/dashboard/service-analytics/overview${queryString(query)}`)
}

export function exportAnalyticsOverview(query: Record<string, string | number | undefined>) {
  return requestBlob(`/api/dashboard/service-analytics/export${queryString(query)}`)
}

export function fetchAnalyticsDimensions() {
  return request<AnalyticsDimensions>("/api/dashboard/service-analytics/dimensions")
}

export function fetchServiceSessions(query: Record<string, string | number | boolean | undefined>) {
  return request<PageResult<ServiceSession>>(`/api/dashboard/service-session/list${queryString(query)}`)
}

export function fetchServiceSessionDimensions() {
  return request<AnalyticsDimensions>("/api/dashboard/service-session/dimensions")
}

export function fetchServiceSessionMessages(sessionId: number, page = 1, limit = 100) {
  return request<PageResult<SessionMessage>>(
    `/api/dashboard/service-session/message_list${queryString({ sessionId, page, limit })}`,
  )
}

export function fetchServiceSession(sessionId: number) {
  return request<ServiceSession>(`/api/dashboard/service-session/${sessionId}`)
}

export function updateServiceSessionAnnotation(payload: {
  id: number
  resolutionCode: string
  categoryCode: string
  sessionSummary: string
  tagIds: number[]
}) {
  return request<ServiceSession>("/api/dashboard/service-session/annotate", {
    method: "POST",
    body: JSON.stringify(payload),
  })
}

export function serviceSessionExportPath(
  query: Record<string, string | number | boolean | undefined>,
) {
  return `/api/dashboard/service-session/export${queryString(query)}`
}

export function exportServiceSessions(
  query: Record<string, string | number | boolean | undefined>,
) {
  return requestBlob(serviceSessionExportPath(query))
}

export function fetchQualityPool(query: Record<string, string | number | undefined>) {
  return request<PageResult<QualityPoolEntry>>(`/api/dashboard/quality-inspection/pool${queryString(query)}`)
}

export function fetchQualityTemplates() {
  return request<QualityTemplate[]>("/api/dashboard/quality-template/list")
}

export function saveQualityTemplate(payload: {
  id?: number
  name: string
  description: string
  passScore: number
  isDefault: boolean
  items: Array<{
    id?: number
    code: string
    name: string
    description: string
    ruleType: QualityRuleType
    metricCode: string
    maxScore: number
    required: boolean
    hardFail: boolean
    sortNo: number
  }>
}) {
  return request<QualityTemplate>("/api/dashboard/quality-template/save", {
    method: "POST",
    body: JSON.stringify(payload),
  })
}

export function fetchQualityInspection(id: number) {
  return request<QualityInspection>(`/api/dashboard/quality-inspection/${id}`)
}

export function saveQualityInspection(payload: {
  id?: number
  assignmentId: number
  templateId: number
  status: QualityInspectionStatus
  summary: string
  items: Array<{
    templateItemId: number
    score: number
    violated?: boolean
    evidence: string
    messageIds: number[]
    comment: string
  }>
}) {
  return request<QualityInspection>("/api/dashboard/quality-inspection/save", {
    method: "POST",
    body: JSON.stringify(payload),
  })
}

export type ServiceAnalyticsPolicy = {
  queueTargetSeconds: number
  firstResponseTargetSeconds: number
  responseTargetSeconds: number
  repeatConsultationHours: number
  satisfactionThreshold: number
  evaluationExpiryHours: number
  defaultSampleSize: number
}

export function fetchAnalyticsPolicy() {
  return request<ServiceAnalyticsPolicy>("/api/dashboard/service-analytics/policy")
}

export function updateAnalyticsPolicy(payload: ServiceAnalyticsPolicy) {
  return request<ServiceAnalyticsPolicy>("/api/dashboard/service-analytics/policy/update", {
    method: "POST",
    body: JSON.stringify(payload),
  })
}

export type QualitySamplingItem = {
  assignmentId: number
  conversationId: number
  sessionNo: number
  agentId: number
  inspectionId: number
}

export type QualitySamplingBatch = {
  id: number
  name: string
  criteriaJson: string
  seed: string
  sampleSize: number
  status: QualitySamplingStatus
  createdBy: number
  createdAt: string
  completedAt?: string
  items: QualitySamplingItem[]
}

export function createQualitySampling(payload: {
  name: string
  teamId?: number
  agentId?: number
  startAt: string
  endAt: string
  sampleSize: number
}) {
  return request<QualitySamplingBatch>("/api/dashboard/quality-sampling/create", {
    method: "POST",
    body: JSON.stringify(payload),
  })
}

export function fetchQualitySampling(id: number) {
  return request<QualitySamplingBatch>(`/api/dashboard/quality-sampling/${id}`)
}

export function fetchQualitySamplingList(query: Record<string, string | number | undefined>) {
  return request<PageResult<QualitySamplingBatch>>(
    `/api/dashboard/quality-sampling/list${queryString(query)}`,
  )
}

export type ReportViewPreset = {
  id: number
  pageCode: string
  name: string
  filtersJson: string
  columnsJson: string
  sortJson: string
  isDefault: boolean
}

export function fetchReportViewPresets(pageCode: string) {
  return request<ReportViewPreset[]>(
    `/api/dashboard/report-view-preset/list${queryString({ pageCode })}`,
  )
}

export function saveReportViewPreset(payload: Omit<ReportViewPreset, "id"> & { id?: number }) {
  return request<ReportViewPreset>("/api/dashboard/report-view-preset/save", {
    method: "POST",
    body: JSON.stringify(payload),
  })
}

export function deleteReportViewPreset(id: number) {
  return request<void>(`/api/dashboard/report-view-preset/delete${queryString({ id })}`, {
    method: "POST",
  })
}

export type AgentPresence = {
  status: "offline" | AgentPresenceStatus
  breakReason: string
  startedAt?: string
  lastSeenAt?: string
}

export function fetchCurrentAgentPresence() {
  return request<AgentPresence>("/api/dashboard/agent-presence/current")
}

export function updateAgentPresence(payload: {
  status: AgentPresenceStatus
  breakReason?: string
}) {
  return request<AgentPresence>("/api/dashboard/agent-presence/update", {
    method: "POST",
    body: JSON.stringify(payload),
  })
}

export type ConversationEvaluation = {
  id: number
  conversationId: number
  sessionNo: number
  assignmentId: number
  customerId: number
  status: ConversationEvaluationStatus
  inviteChannel: string
  invitedAt: string
  expiresAt: string
  submittedAt?: string
  rating: number
  tagCodes: string[]
  comment: string
}

export type ConversationEvaluationInvite = {
  evaluation: ConversationEvaluation
  path: string
}

export function inviteConversationEvaluation(payload: {
  serviceSessionId: number
  assignmentId?: number
}) {
  return request<ConversationEvaluationInvite>("/api/dashboard/conversation-evaluation/invite", {
    method: "POST",
    body: JSON.stringify(payload),
  })
}

export function fetchConversationEvaluations(query: Record<string, string | number | undefined>) {
  return request<PageResult<ConversationEvaluation>>(
    `/api/dashboard/conversation-evaluation/list${queryString(query)}`,
  )
}

export type PublicConversationEvaluation = {
  status: ConversationEvaluationStatus
  companyName: string
  expiresAt: string
  submittedAt?: string
  rating: number
}

export function validateConversationEvaluation(token: string) {
  return request<PublicConversationEvaluation>(
    `/api/evaluation/validate${queryString({ token })}`,
    { skipAuth: true },
  )
}

export function submitConversationEvaluation(payload: {
  token: string
  rating: number
  tagCodes: string[]
  comment: string
}) {
  return request<PublicConversationEvaluation>("/api/evaluation/submit", {
    method: "POST",
    body: JSON.stringify(payload),
    skipAuth: true,
  })
}

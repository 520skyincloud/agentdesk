import { request } from "@/lib/api/client"

export type SandboxDataset = { id: number; storeId: number; version: number; createdAt: string }
export type SandboxRoomType = { id: number; name: string; rank: number; priceCents: number; enabled: boolean }
export type SandboxRoom = {
  id: number; roomTypeId: number; number: string; floor: string
  cleanStatus: string; description: string; enabled: boolean
}
export type SandboxOrder = {
  id: number; number: string; guestName: string; phone: string
  roomTypeId: number; roomTypeName: string; roomId: number; roomNumber: string
  checkIn: string; checkOut: string; payableCents: number
  includesBreakfast: boolean; status: string; version: number
}
export type SandboxGrade = {
  id: number; name: string; benefits: string[]; birthdayBenefits: string[]
  freeUpgradeMaxRank: number; enabled: boolean
}
export type SandboxMember = {
  id: number; name: string; phone: string; gradeId: number; gradeName: string
  birthday: string; validUntil: string; enabled: boolean
}
export type SandboxRule = {
  id: number; code: string; name: string; text: string; action: string
  amountCents: number; checkoutTime: string; minimumGradeId: number; enabled: boolean
  validFrom?: string | null; validUntil?: string | null
}
export type SandboxResource = {
  id: number; code: string; name: string; token: string; sourceMessageId: number
  messageType: string; enabled: boolean; cardPayload?: string
}
export type SandboxBinding = { id: number; customerId: number; orderId: number; memberId: number }
export type SandboxOperation = {
  id: number; provider: string; storeId: number; datasetId: number
  conversationId: number; sourceMessageId: number; previewMessageId: number
  confirmationMessageId: number; operationType: string; status: string
  previewText: string; resultText: string; errorMessage: string
  plan?: { before: SandboxOrder; after: SandboxOrder; addedCents: number; commitment: string }
  expiresAt?: string; createdAt: string; deliveryStatus: string
}
export type SandboxOption = { value: string; label: string }
export type SandboxCustomerOption = { id: number; name: string; conversationId: number }
export type SandboxWorkspace = {
  label: string; active: boolean; dataset?: SandboxDataset
  roomTypes: SandboxRoomType[]; rooms: SandboxRoom[]; orders: SandboxOrder[]
  grades: SandboxGrade[]; members: SandboxMember[]; rules: SandboxRule[]
  resources: SandboxResource[]; bindings: SandboxBinding[]; operations: SandboxOperation[]
  options: Record<string, SandboxOption[]>
}
export type SandboxSave = {
  roomType?: SandboxRoomType; room?: SandboxRoom; order?: SandboxOrder
  grade?: SandboxGrade; member?: SandboxMember; rule?: SandboxRule; resource?: SandboxResource
}

const base = "/api/dashboard/pms-sandbox"
function post<T>(path: string, body: unknown) {
  return request<T>(`${base}/${path}`, { method: "POST", body: JSON.stringify(body) })
}
export function fetchSandboxStores() {
  return request<{ id: number; name: string }[]>(`${base}/stores`)
}
export function fetchSandboxWorkspace(storeId: number) {
  return request<SandboxWorkspace>(`${base}/list?storeId=${storeId}`)
}
export function fetchSandboxCustomers(storeId: number) {
  return request<SandboxCustomerOption[]>(`${base}/customers?storeId=${storeId}`)
}
export function initializeSandbox(storeId: number) {
  return post<SandboxWorkspace>("initialize", { storeId })
}
export function resetSandbox(storeId: number, dataset: SandboxDataset) {
  return post<SandboxWorkspace>("reset", { storeId, datasetId: dataset.id, version: dataset.version })
}
export function saveSandbox(storeId: number, dataset: SandboxDataset, value: SandboxSave) {
  return post<SandboxWorkspace>("update", { storeId, datasetId: dataset.id, version: dataset.version, ...value })
}
export function bindSandboxCustomer(storeId: number, dataset: SandboxDataset, value: Omit<SandboxBinding, "id">) {
  return post<SandboxWorkspace>("bind", { storeId, datasetId: dataset.id, version: dataset.version, ...value })
}
export function cancelSandboxOperation(storeId: number, operationId: number) {
  return post<void>("cancel", { storeId, operationId })
}

"use client"

import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import {
  Columns3Icon,
  DownloadIcon,
  EyeIcon,
  MoreHorizontalIcon,
  RefreshCwIcon,
  SaveIcon,
  SearchIcon,
  ShieldCheckIcon,
  ShuffleIcon,
  Trash2Icon,
  XIcon,
} from "lucide-react"
import { toast } from "sonner"

import { useAuth } from "@/components/auth-provider"
import { ConversationCloseDialog } from "@/components/conversation-actions/close-dialog"
import { ConversationTransferDialog } from "@/components/conversation-actions/transfer-dialog"
import { ListPagination } from "@/components/list-pagination"
import { OptionCombobox, type ComboboxOption } from "@/components/option-combobox"
import { TagBadges, TagSelector } from "@/components/tag-selector"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Input } from "@/components/ui/input"
import { Skeleton } from "@/components/ui/skeleton"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import {
  createAdminWebSocketUrl,
  autoAssignConversationDispatch,
  fetchConversationDetail,
  fetchConversationMessages,
  fetchTagsAll,
  markConversationRead,
  type AdminConversationDetail,
  type AdminMessage,
  type TagTree,
} from "@/lib/api/admin"
import {
  createQualitySampling,
  deleteReportViewPreset,
  exportServiceSessions,
  fetchReportViewPresets,
  fetchServiceSessionDimensions,
  fetchServiceSessions,
  saveReportViewPreset,
  type AnalyticsDimensions,
  type PageResult,
  type QualitySamplingBatch,
  type ReportViewPreset,
  type ServiceSession,
} from "@/lib/api/service-analytics"
import { AnalyticsDataQuality, AnalyticsDataQualityLabels } from "@/lib/generated/enums"
import { formatDateTime } from "@/lib/utils"
import { ConversationDetailDialog } from "./_components/detail"
import { SessionWorkflowDialog } from "./_components/session-workflow"

type QuickView = "all" | "open" | "human" | "waiting" | "sla_breached" | "quality_pending" | "quality_completed"
type ColumnKey = "customer" | "status" | "source" | "agent" | "messages" | "response" | "resolution" | "tags" | "quality" | "started"

const columns: Array<{ key: ColumnKey; label: string }> = [
  { key: "customer", label: "客户 / 会话" },
  { key: "status", label: "状态" },
  { key: "source", label: "门店 / 员工号" },
  { key: "agent", label: "客服组 / 客服" },
  { key: "messages", label: "消息构成" },
  { key: "response", label: "响应" },
  { key: "resolution", label: "解决状态" },
  { key: "tags", label: "标签" },
  { key: "quality", label: "数据质量" },
  { key: "started", label: "开始时间" },
]

function localDateInput(daysAgo = 0) {
  const value = new Date()
  value.setDate(value.getDate() - daysAgo)
  const year = value.getFullYear()
  const month = String(value.getMonth() + 1).padStart(2, "0")
  const day = String(value.getDate()).padStart(2, "0")
  return `${year}-${month}-${day}`
}

function duration(seconds: number) {
  if (!Number.isFinite(seconds) || seconds <= 0) return "-"
  if (seconds < 60) return `${Math.round(seconds)}秒`
  if (seconds < 3600) return `${(seconds / 60).toFixed(1)}分`
  return `${(seconds / 3600).toFixed(1)}时`
}

function statusBadge(status: string) {
  return status === "closed" ? <Badge variant="outline">已结束</Badge> : <Badge variant="secondary">进行中</Badge>
}

function options(items: AnalyticsDimensions[keyof AnalyticsDimensions] | undefined, label: string): ComboboxOption[] {
  return [{ value: "", label }, ...(items ?? []).map((item) => ({ value: String(item.id), label: item.name || `#${item.id}` }))]
}

function isQuickView(value: string | null): value is QuickView {
  return value === "all" || value === "open" || value === "human" || value === "waiting" || value === "sla_breached" || value === "quality_pending" || value === "quality_completed"
}

export default function ConversationRecordsPage() {
  const { session: authSession } = useAuth()
  const permissions = useMemo(() => new Set(authSession?.permissions ?? []), [authSession?.permissions])
  const canView = permissions.has("conversationRecord.view")
  const canAnnotate = permissions.has("conversationRecord.annotate")
  const canExport = permissions.has("conversationRecord.export")
  const canQuality = permissions.has("qualityInspection.view")
  const canManageQuality = permissions.has("qualityInspection.manage")
  const canSample = permissions.has("qualitySampling.create")
  const canManageViews = permissions.has("reportViewPreset.manage")
  const canInviteEvaluation = permissions.has("conversationEvaluation.invite")
  const canViewTags = permissions.has("tag.view")
  const canAssign = permissions.has("conversation.assign")
  const canTransfer = permissions.has("conversation.transfer")
  const canClose = permissions.has("conversation.close")

  const [keywordInput, setKeywordInput] = useState("")
  const [keyword, setKeyword] = useState("")
  const [categoryCodeInput, setCategoryCodeInput] = useState("")
  const [categoryCode, setCategoryCode] = useState("")
  const [conversationId, setConversationId] = useState("")
  const [quickView, setQuickView] = useState<QuickView>("all")
  const [startAt, setStartAt] = useState(() => localDateInput(29))
  const [endAt, setEndAt] = useState(() => localDateInput())
  const [teamId, setTeamId] = useState("")
  const [squadId, setSquadId] = useState("")
  const [agentId, setAgentId] = useState("")
  const [channelId, setChannelId] = useState("")
  const [storeId, setStoreId] = useState("")
  const [wxWorkInstanceId, setWxWorkInstanceId] = useState("")
  const [dataQuality, setDataQuality] = useState("")
  const [resolutionCode, setResolutionCode] = useState("")
  const [tagId, setTagId] = useState("")
  const [page, setPage] = useState(1)
  const [limit, setLimit] = useState(20)
  const [loading, setLoading] = useState(true)
  const [refreshing, setRefreshing] = useState(false)
  const [dimensions, setDimensions] = useState<AnalyticsDimensions | null>(null)
  const [tags, setTags] = useState<TagTree[]>([])
  const [result, setResult] = useState<PageResult<ServiceSession>>({ results: [], page: { page: 1, limit: 20, total: 0 } })
  const [visibleColumns, setVisibleColumns] = useState<Set<ColumnKey>>(() => new Set(columns.map((item) => item.key)))
  const [presets, setPresets] = useState<ReportViewPreset[]>([])
  const [presetOpen, setPresetOpen] = useState(false)
  const [presetName, setPresetName] = useState("")
  const [presetSaving, setPresetSaving] = useState(false)
  const [samplingOpen, setSamplingOpen] = useState(false)
  const [samplingName, setSamplingName] = useState(() => `人工回复质检抽样-${localDateInput()}`)
  const [sampleSize, setSampleSize] = useState(10)
  const [samplingSaving, setSamplingSaving] = useState(false)
  const [samplingResult, setSamplingResult] = useState<QualitySamplingBatch | null>(null)
  const [selectedSession, setSelectedSession] = useState<ServiceSession | null>(null)
  const [workflowOpen, setWorkflowOpen] = useState(false)
  const [legacyOpen, setLegacyOpen] = useState(false)
  const [legacyLoading, setLegacyLoading] = useState(false)
  const [legacyDetail, setLegacyDetail] = useState<AdminConversationDetail | null>(null)
  const [legacyMessages, setLegacyMessages] = useState<AdminMessage[]>([])
  const [legacyCursor, setLegacyCursor] = useState("")
  const [legacyHasMore, setLegacyHasMore] = useState(false)
  const [legacyLoadingMore, setLegacyLoadingMore] = useState(false)
  const [actionLoadingId, setActionLoadingId] = useState<number | null>(null)
  const [assignOpen, setAssignOpen] = useState(false)
  const [transferOpen, setTransferOpen] = useState(false)
  const [closeOpen, setCloseOpen] = useState(false)
  const selectedSessionRef = useRef<ServiceSession | null>(null)
  const urlFiltersAppliedRef = useRef(false)
  const [urlFiltersReady, setUrlFiltersReady] = useState(false)

  useEffect(() => { selectedSessionRef.current = selectedSession }, [selectedSession])

  useEffect(() => {
    const params = new URLSearchParams(window.location.search)
    const view = params.get("view")
    const mappings: Array<[string, (value: string) => void]> = [
      ["startAt", setStartAt],
      ["endAt", setEndAt],
      ["teamId", setTeamId],
      ["squadId", setSquadId],
      ["agentId", setAgentId],
      ["channelId", setChannelId],
      ["storeId", setStoreId],
      ["wxWorkInstanceId", setWxWorkInstanceId],
      ["dataQuality", setDataQuality],
      ["resolutionCode", setResolutionCode],
      ["categoryCode", (value) => { setCategoryCode(value); setCategoryCodeInput(value) }],
      ["tagId", setTagId],
      ["conversationId", setConversationId],
    ]
    mappings.forEach(([key, update]) => {
      const value = params.get(key)
      if (value) {
        update(value)
        urlFiltersAppliedRef.current = true
      }
    })
    if (isQuickView(view)) {
      setQuickView(view)
      urlFiltersAppliedRef.current = true
    }
    setUrlFiltersReady(true)
  }, [])

  useEffect(() => {
    if (!canView) return
    void fetchServiceSessionDimensions().then(setDimensions).catch((error) => toast.error(error instanceof Error ? error.message : "筛选项加载失败"))
  }, [canView])

  useEffect(() => {
    if (!canViewTags) {
      setTags([])
      return
    }
    void fetchTagsAll().then(setTags).catch((error) => toast.error(error instanceof Error ? error.message : "标签加载失败"))
  }, [canViewTags, authSession?.activeTenantId])

  useEffect(() => {
    if (!canManageViews || !urlFiltersReady) return
    void fetchReportViewPresets("conversation-records").then((items) => {
      setPresets(items)
      const preset = items.find((item) => item.isDefault)
      if (preset && !urlFiltersAppliedRef.current) applyPreset(preset)
    }).catch((error) => toast.error(error instanceof Error ? error.message : "保存视图加载失败"))
    // Default views are applied once when the authenticated tenant changes.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [canManageViews, authSession?.activeTenantId, urlFiltersReady])

  const query = useMemo(() => ({
    keyword: keyword || undefined,
    conversationId: conversationId || undefined,
    startAt,
    endAt,
    assignedTeamId: teamId || undefined,
    assignedSquadId: squadId || undefined,
    assignedAgentId: agentId || undefined,
    channelId: channelId || undefined,
    storeId: storeId || undefined,
    wxWorkInstanceId: wxWorkInstanceId || undefined,
    dataQuality: dataQuality || undefined,
    resolutionCode: resolutionCode || undefined,
    categoryCode: categoryCode || undefined,
    tagId: tagId || undefined,
    status: quickView === "open" ? "open" : undefined,
    humanOnly: quickView === "human" ? true : undefined,
    waitingReply: quickView === "waiting" ? true : undefined,
    slaBreached: quickView === "sla_breached" ? true : undefined,
    qualityStatus: quickView === "quality_pending" ? "pending" : quickView === "quality_completed" ? "completed" : undefined,
    page,
    limit,
  }), [agentId, categoryCode, channelId, conversationId, dataQuality, endAt, keyword, limit, page, quickView, resolutionCode, squadId, startAt, storeId, tagId, teamId, wxWorkInstanceId])

  const load = useCallback(async (refreshOnly = false) => {
    if (!canView || !urlFiltersReady) return
    if (refreshOnly) {
      setRefreshing(true)
    } else {
      setLoading(true)
    }
    try {
      setResult(await fetchServiceSessions(query))
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "会话记录加载失败")
    } finally {
      setLoading(false)
      setRefreshing(false)
    }
  }, [canView, query, urlFiltersReady])

  useEffect(() => { void load() }, [load])

  const visibleSquads = useMemo(() => dimensions?.squads.filter((item) => !teamId || String(item.parentId) === teamId) ?? [], [dimensions, teamId])
  const visibleAgents = useMemo(() => dimensions?.agents.filter((item) => !teamId || String(item.parentId) === teamId) ?? [], [dimensions, teamId])
  const visibleWxWork = useMemo(() => dimensions?.wxWorkInstances.filter((item) => !storeId || String(item.parentId) === storeId) ?? [], [dimensions, storeId])

  function changeTeam(value: string) {
    setTeamId(value)
    if (squadId && !dimensions?.squads.some((item) => String(item.id) === squadId && (!value || String(item.parentId) === value))) setSquadId("")
    if (agentId && !dimensions?.agents.some((item) => String(item.id) === agentId && (!value || String(item.parentId) === value))) setAgentId("")
    setPage(1)
  }

  function changeStore(value: string) {
    setStoreId(value)
    if (wxWorkInstanceId && !dimensions?.wxWorkInstances.some((item) => String(item.id) === wxWorkInstanceId && (!value || String(item.parentId) === value))) setWxWorkInstanceId("")
    setPage(1)
  }

  function currentFilters() {
    return { keyword, conversationId, quickView, startAt, endAt, teamId, squadId, agentId, channelId, storeId, wxWorkInstanceId, dataQuality, resolutionCode, categoryCode, tagId }
  }

  function applyPreset(preset: ReportViewPreset) {
    try {
      const filters = JSON.parse(preset.filtersJson || "{}") as Partial<ReturnType<typeof currentFilters>>
      setKeyword(filters.keyword ?? "")
      setKeywordInput(filters.keyword ?? "")
      setConversationId(filters.conversationId ?? "")
      setQuickView(filters.quickView ?? "all")
      setStartAt(filters.startAt ?? localDateInput(29))
      setEndAt(filters.endAt ?? localDateInput())
      setTeamId(filters.teamId ?? "")
      setSquadId(filters.squadId ?? "")
      setAgentId(filters.agentId ?? "")
      setChannelId(filters.channelId ?? "")
      setStoreId(filters.storeId ?? "")
      setWxWorkInstanceId(filters.wxWorkInstanceId ?? "")
      setDataQuality(filters.dataQuality ?? "")
      setResolutionCode(filters.resolutionCode ?? "")
      setCategoryCode(filters.categoryCode ?? "")
      setCategoryCodeInput(filters.categoryCode ?? "")
      setTagId(filters.tagId ?? "")
      const savedColumns = JSON.parse(preset.columnsJson || "[]") as ColumnKey[]
      if (savedColumns.length) setVisibleColumns(new Set(savedColumns))
      setPage(1)
    } catch {
      toast.error("保存视图配置无效")
    }
  }

  async function savePreset() {
    if (!presetName.trim()) {
      toast.error("请输入视图名称")
      return
    }
    setPresetSaving(true)
    try {
      const saved = await saveReportViewPreset({
        pageCode: "conversation-records",
        name: presetName.trim(),
        filtersJson: JSON.stringify(currentFilters()),
        columnsJson: JSON.stringify(Array.from(visibleColumns)),
        sortJson: JSON.stringify({ startedAt: "desc" }),
        isDefault: presets.length === 0,
      })
      setPresets((current) => [...current, saved])
      setPresetOpen(false)
      setPresetName("")
      toast.success("当前视图已保存")
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "保存视图失败")
    } finally {
      setPresetSaving(false)
    }
  }

  async function removePreset(id: number) {
    try {
      await deleteReportViewPreset(id)
      setPresets((current) => current.filter((item) => item.id !== id))
      toast.success("保存视图已删除")
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "删除视图失败")
    }
  }

  async function exportRecords() {
    try {
      const blob = await exportServiceSessions({ ...query, page: undefined, limit: undefined })
      const url = URL.createObjectURL(blob)
      const link = document.createElement("a")
      link.href = url
      link.download = `conversation-records-${localDateInput()}.csv`
      link.click()
      URL.revokeObjectURL(url)
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "导出失败")
    }
  }

  async function createSamplingBatch() {
    setSamplingSaving(true)
    try {
      const batch = await createQualitySampling({ name: samplingName, teamId: Number(teamId) || undefined, agentId: Number(agentId) || undefined, startAt, endAt, sampleSize })
      setSamplingResult(batch)
      toast.success(`已固定抽取 ${batch.sampleSize} 个人工接待分段`)
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "创建抽样批次失败")
    } finally {
      setSamplingSaving(false)
    }
  }

  function openWorkflow(item: ServiceSession) {
    setSelectedSession(item)
    setWorkflowOpen(true)
  }

  const loadLegacy = useCallback(async (item: ServiceSession) => {
    setLegacyLoading(true)
    try {
      const [detail, messages] = await Promise.all([
        fetchConversationDetail(item.conversationId),
        fetchConversationMessages({ conversationId: item.conversationId, limit: 50 }),
      ])
      setLegacyDetail(detail)
      setLegacyMessages(Array.isArray(messages.results) ? messages.results : [])
      setLegacyCursor(messages.cursor ?? "")
      setLegacyHasMore(Boolean(messages.hasMore))
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "完整会话详情加载失败")
    } finally {
      setLegacyLoading(false)
    }
  }, [])

  async function openLegacy() {
    if (!selectedSession) return
    setWorkflowOpen(false)
    setLegacyOpen(true)
    await loadLegacy(selectedSession)
  }

  const loadMoreLegacy = useCallback(async () => {
    const item = selectedSessionRef.current
    const cursor = Number.parseInt(legacyCursor, 10)
    if (!item || legacyLoadingMore || !legacyHasMore || !Number.isFinite(cursor) || cursor <= 0) return
    setLegacyLoadingMore(true)
    try {
      const next = await fetchConversationMessages({ conversationId: item.conversationId, cursor, limit: 50 })
      setLegacyMessages((current) => [...(Array.isArray(next.results) ? next.results : []), ...current])
      setLegacyCursor(next.cursor ?? "")
      setLegacyHasMore(Boolean(next.hasMore))
    } finally {
      setLegacyLoadingMore(false)
    }
  }, [legacyCursor, legacyHasMore, legacyLoadingMore])

  const refreshSelectedConversation = useCallback(async () => {
    await load(true)
    const selected = selectedSessionRef.current
    if (legacyOpen && selected) {
      await loadLegacy(selected)
    }
  }, [legacyOpen, load, loadLegacy])

  async function handleRead(item: ServiceSession) {
    setActionLoadingId(item.conversationId)
    try {
      await markConversationRead(item.conversationId)
      toast.success(`会话 #${item.conversationId} 已标记为已读`)
      await refreshSelectedConversation()
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "标记已读失败")
    } finally {
      setActionLoadingId(null)
    }
  }

  async function handleDispatch(item: ServiceSession) {
    if (!canAssign || item.status !== "open") return
    setActionLoadingId(item.conversationId)
    try {
      await autoAssignConversationDispatch(item.conversationId)
      toast.success(`会话 #${item.conversationId} 已重新触发自动派单`)
      await refreshSelectedConversation()
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "重新派单失败")
    } finally {
      setActionLoadingId(null)
    }
  }

  useEffect(() => {
    if (!legacyOpen || !selectedSession) return
    const socket = new WebSocket(createAdminWebSocketUrl())
    socket.onopen = () => socket.send(JSON.stringify({ type: "subscribe", topics: [`conversation:${selectedSession.conversationId}`] }))
    socket.onmessage = (event) => {
      try {
        const payload = JSON.parse(event.data) as { type?: string; data?: { conversationId?: number } }
        if (payload.data?.conversationId === selectedSession.conversationId && payload.type !== "pong") void loadLegacy(selectedSession)
      } catch {
        // Ignore malformed realtime payloads and retain the current detail.
      }
    }
    return () => socket.close()
  }, [legacyOpen, loadLegacy, selectedSession])

  if (!canView) return <div className="p-6 text-sm text-muted-foreground">无权查看会话记录</div>

  return (
    <div className="flex flex-1 flex-col gap-4 p-4 lg:p-6">
      <header className="flex flex-col gap-3 border-b pb-4 xl:flex-row xl:items-end xl:justify-between">
        <div><h1 className="text-2xl font-semibold">会话记录</h1><p className="mt-1 text-sm text-muted-foreground">按客户每次服务轮次检索、复盘、标注与人工回复质检</p></div>
        <div className="flex flex-wrap gap-2">
          {canManageViews ? <DropdownMenu><DropdownMenuTrigger render={<Button variant="outline" size="sm" />}><SaveIcon />保存视图</DropdownMenuTrigger><DropdownMenuContent align="end"><DropdownMenuLabel>个人视图</DropdownMenuLabel>{presets.map((preset) => <DropdownMenuItem key={preset.id} onClick={() => applyPreset(preset)}><span className="flex-1">{preset.name}</span><Button variant="ghost" size="icon-sm" title="删除视图" onClick={(event) => { event.stopPropagation(); void removePreset(preset.id) }}><Trash2Icon /></Button></DropdownMenuItem>)}<DropdownMenuSeparator /><DropdownMenuItem onClick={() => setPresetOpen(true)}>保存当前筛选与列</DropdownMenuItem></DropdownMenuContent></DropdownMenu> : null}
          <DropdownMenu><DropdownMenuTrigger render={<Button variant="outline" size="sm" />}><Columns3Icon />显示列</DropdownMenuTrigger><DropdownMenuContent align="end">{columns.map((column) => <DropdownMenuCheckboxItem key={column.key} checked={visibleColumns.has(column.key)} onCheckedChange={(checked) => setVisibleColumns((current) => { const next = new Set(current); if (checked) { next.add(column.key) } else { next.delete(column.key) } return next })}>{column.label}</DropdownMenuCheckboxItem>)}</DropdownMenuContent></DropdownMenu>
          {canSample ? <Button variant="outline" size="sm" onClick={() => { setSamplingResult(null); setSamplingOpen(true) }}><ShuffleIcon />随机抽样</Button> : null}
          {canExport ? <Button variant="outline" size="sm" onClick={() => void exportRecords()}><DownloadIcon />导出</Button> : null}
          <Button variant="outline" size="sm" disabled={refreshing} onClick={() => void load(true)}><RefreshCwIcon className={refreshing ? "animate-spin" : ""} />刷新</Button>
        </div>
      </header>

      <div className="flex flex-wrap gap-1 border-b pb-3">{([
        ["all", "全部"], ["open", "进行中"], ["human", "含人工回复"], ["waiting", "待人工回复"], ["sla_breached", "SLA 超时"], ["quality_pending", "待质检"], ["quality_completed", "已质检"],
      ] as Array<[QuickView, string]>).map(([value, label]) => <Button key={value} size="sm" variant={quickView === value ? "secondary" : "ghost"} onClick={() => { setQuickView(value); setPage(1) }}>{label}</Button>)}{conversationId ? <Badge variant="outline" className="ml-2 gap-1 pl-2">会话 #{conversationId}<Button variant="ghost" size="icon-sm" title="清除会话筛选" onClick={() => { setConversationId(""); setPage(1) }}><XIcon /></Button></Badge> : null}</div>

      <section className="grid gap-2 border-b pb-4 sm:grid-cols-2 lg:grid-cols-4 2xl:grid-cols-9">
        <label className="space-y-1 text-xs text-muted-foreground">开始日期<Input type="date" value={startAt} onChange={(event) => { setStartAt(event.target.value); setPage(1) }} className="h-9" /></label>
        <label className="space-y-1 text-xs text-muted-foreground">结束日期<Input type="date" value={endAt} onChange={(event) => { setEndAt(event.target.value); setPage(1) }} className="h-9" /></label>
        <label className="space-y-1 text-xs text-muted-foreground">客服组<OptionCombobox value={teamId} options={options(dimensions?.teams, "全部客服组")} placeholder="全部客服组" onChange={changeTeam} /></label>
        <label className="space-y-1 text-xs text-muted-foreground">客服小组<OptionCombobox value={squadId} options={options(visibleSquads, "全部小组")} placeholder="全部小组" onChange={(value) => { setSquadId(value); setPage(1) }} /></label>
        <label className="space-y-1 text-xs text-muted-foreground">客服<OptionCombobox value={agentId} options={options(visibleAgents, "全部客服")} placeholder="全部客服" onChange={(value) => { setAgentId(value); setPage(1) }} /></label>
        <label className="space-y-1 text-xs text-muted-foreground">接入渠道<OptionCombobox value={channelId} options={options(dimensions?.channels, "全部渠道")} placeholder="全部渠道" onChange={(value) => { setChannelId(value); setPage(1) }} /></label>
        <label className="space-y-1 text-xs text-muted-foreground">门店<OptionCombobox value={storeId} options={options(dimensions?.stores, "全部门店")} placeholder="全部门店" onChange={changeStore} /></label>
        <label className="space-y-1 text-xs text-muted-foreground">企微员工号<OptionCombobox value={wxWorkInstanceId} options={options(visibleWxWork, "全部员工号")} placeholder="全部员工号" onChange={(value) => { setWxWorkInstanceId(value); setPage(1) }} /></label>
        <label className="space-y-1 text-xs text-muted-foreground">数据质量<OptionCombobox value={dataQuality} options={[{ value: "", label: "全部质量" }, ...Object.values(AnalyticsDataQuality).map((value) => ({ value, label: AnalyticsDataQualityLabels[value] }))]} placeholder="全部质量" onChange={(value) => { setDataQuality(value); setPage(1) }} /></label>
      </section>

      <form className="grid gap-2 sm:grid-cols-2 lg:grid-cols-[minmax(16rem,2fr)_minmax(12rem,1fr)_minmax(11rem,1fr)_minmax(13rem,1fr)_auto]" onSubmit={(event) => { event.preventDefault(); setKeyword(keywordInput.trim()); setCategoryCode(categoryCodeInput.trim()); setPage(1) }}>
        <div className="relative"><SearchIcon className="absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" /><Input className="pl-9" value={keywordInput} onChange={(event) => setKeywordInput(event.target.value)} placeholder="搜索客户姓名或会话编号" /></div>
        <Input value={categoryCodeInput} onChange={(event) => setCategoryCodeInput(event.target.value)} placeholder="咨询分类" />
        <OptionCombobox value={resolutionCode} options={[{ value: "", label: "全部解决状态" }, { value: "resolved", label: "已解决" }, { value: "follow_up", label: "需跟进" }, { value: "unresolved", label: "未解决" }]} placeholder="全部解决状态" onChange={(value) => { setResolutionCode(value); setPage(1) }} />
        {canViewTags ? <TagSelector mode="single" value={Number(tagId) || 0} onChange={(value) => { setTagId(value > 0 ? String(value) : ""); setPage(1) }} tags={tags} placeholder="全部标签" searchPlaceholder="搜索标签路径" emptyText="暂无标签" rootOption={{ value: 0, label: "全部标签" }} /> : <div />}
        <Button type="submit">查询</Button>
      </form>

      <section className="overflow-hidden border bg-background">
        <div className="overflow-x-auto">
          <Table className="min-w-300">
            <TableHeader><TableRow>{columns.filter((column) => visibleColumns.has(column.key)).map((column) => <TableHead key={column.key}>{column.label}</TableHead>)}<TableHead className="w-20 text-right">操作</TableHead></TableRow></TableHeader>
            <TableBody>{loading ? Array.from({ length: 6 }).map((_, index) => <TableRow key={index}><TableCell colSpan={visibleColumns.size + 1}><Skeleton className="h-10" /></TableCell></TableRow>) : result.results.length ? result.results.map((item) => <TableRow key={item.id} className="cursor-pointer" onClick={() => openWorkflow(item)}>
              {visibleColumns.has("customer") ? <TableCell><div className="font-medium">{item.customerName || `客户 #${item.customerId}`}</div><div className="text-xs text-muted-foreground">会话 #{item.conversationId} · 第 {item.sessionNo} 轮</div></TableCell> : null}
              {visibleColumns.has("status") ? <TableCell>{statusBadge(item.status)}</TableCell> : null}
              {visibleColumns.has("source") ? <TableCell><div>{item.channelName || "未识别渠道"}</div><div className="text-xs text-muted-foreground">{item.storeName || "未识别门店"} / {item.wxWorkEmployeeName || "未识别员工号"}</div></TableCell> : null}
              {visibleColumns.has("agent") ? <TableCell><div>{item.assignedTeamName || "-"}</div><div className="text-xs text-muted-foreground">{item.assignedAgentName || "未分配"}</div></TableCell> : null}
              {visibleColumns.has("messages") ? <TableCell><span className="text-muted-foreground">客</span> {item.customerMessageCount} · <span className="text-muted-foreground">AI</span> {item.aiMessageCount} · <span className="text-muted-foreground">人工</span> {item.humanMessageCount}</TableCell> : null}
              {visibleColumns.has("response") ? <TableCell><div>排队 {duration(item.queueSeconds)}</div><div className="text-xs text-muted-foreground">首响 {duration(item.firstResponseSeconds)}</div></TableCell> : null}
              {visibleColumns.has("resolution") ? <TableCell>{item.resolutionCode ? <Badge variant="outline">{item.resolutionCode}</Badge> : <span className="text-muted-foreground">未标记</span>}</TableCell> : null}
              {visibleColumns.has("tags") ? <TableCell><TagBadges ids={item.tagIds} tags={tags} /></TableCell> : null}
              {visibleColumns.has("quality") ? <TableCell><Badge variant={item.dataQuality === AnalyticsDataQuality.Exact ? "secondary" : "outline"}>{AnalyticsDataQualityLabels[item.dataQuality]}</Badge></TableCell> : null}
              {visibleColumns.has("started") ? <TableCell>{formatDateTime(item.startedAt)}</TableCell> : null}
              <TableCell className="text-right" onClick={(event) => event.stopPropagation()}><DropdownMenu><DropdownMenuTrigger render={<Button variant="ghost" size="icon-sm" title="会话操作" />}><MoreHorizontalIcon /></DropdownMenuTrigger><DropdownMenuContent align="end"><DropdownMenuItem onClick={() => openWorkflow(item)}><EyeIcon />记录详情</DropdownMenuItem><DropdownMenuItem onClick={() => { setSelectedSession(item); setLegacyOpen(true); void loadLegacy(item) }}><ShieldCheckIcon />完整会话详情</DropdownMenuItem></DropdownMenuContent></DropdownMenu></TableCell>
            </TableRow>) : <TableRow><TableCell colSpan={visibleColumns.size + 1} className="h-48 text-center text-muted-foreground">当前筛选范围没有会话记录</TableCell></TableRow>}</TableBody>
          </Table>
        </div>
        <div className="border-t p-3"><ListPagination page={result.page.page} total={result.page.total} limit={result.page.limit} loading={loading || refreshing} onPageChange={setPage} onLimitChange={(value) => { setLimit(value); setPage(1) }} /></div>
      </section>

      <SessionWorkflowDialog open={workflowOpen} session={selectedSession} canAnnotate={canAnnotate} canViewTags={canViewTags} canViewQuality={canQuality} canManageQuality={canManageQuality} canInviteEvaluation={canInviteEvaluation} onOpenChange={setWorkflowOpen} onOpenFullDetail={() => void openLegacy()} onUpdated={(updated) => { setSelectedSession(updated); setResult((current) => ({ ...current, results: current.results.map((item) => item.id === updated.id ? updated : item) })) }} />

      <ConversationDetailDialog
        open={legacyOpen}
        loading={legacyLoading}
        saving={actionLoadingId === selectedSession?.conversationId}
        item={legacyDetail}
        detail={legacyDetail}
        messages={legacyMessages}
        messagesHasMore={legacyHasMore}
        loadingMoreMessages={legacyLoadingMore}
        canAssign={canAssign && selectedSession?.status === "open"}
        canTransfer={canTransfer && selectedSession?.status === "open"}
        canClose={canClose && selectedSession?.status === "open"}
        onLoadMoreMessages={loadMoreLegacy}
        onOpenChange={(open) => {
          if (actionLoadingId === null) setLegacyOpen(open)
        }}
        onOpenAssign={() => setAssignOpen(true)}
        onDispatch={async () => {
          if (selectedSession) await handleDispatch(selectedSession)
        }}
        onOpenTransfer={() => setTransferOpen(true)}
        onRead={async () => {
          if (selectedSession) await handleRead(selectedSession)
        }}
        onOpenClose={() => setCloseOpen(true)}
      />

      <ConversationTransferDialog
        open={canAssign && assignOpen}
        mode="assign"
        conversationId={selectedSession?.conversationId ?? null}
        onOpenChange={setAssignOpen}
        onSuccess={refreshSelectedConversation}
      />
      <ConversationTransferDialog
        open={canTransfer && transferOpen}
        mode="transfer"
        conversationId={selectedSession?.conversationId ?? null}
        onOpenChange={setTransferOpen}
        onSuccess={refreshSelectedConversation}
      />
      <ConversationCloseDialog
        open={canClose && closeOpen}
        conversationId={selectedSession?.conversationId ?? null}
        onOpenChange={setCloseOpen}
        onSuccess={refreshSelectedConversation}
      />

      <Dialog open={presetOpen} onOpenChange={setPresetOpen}><DialogContent className="max-w-md"><DialogHeader><DialogTitle>保存当前视图</DialogTitle><DialogDescription>保存当前筛选条件、显示列和排序，仅当前账号可见。</DialogDescription></DialogHeader><label className="space-y-1.5 text-sm">视图名称<Input value={presetName} maxLength={100} onChange={(event) => setPresetName(event.target.value)} /></label><div className="flex justify-end gap-2"><Button variant="outline" onClick={() => setPresetOpen(false)}>取消</Button><Button disabled={presetSaving} onClick={() => void savePreset()}>{presetSaving ? "保存中" : "保存"}</Button></div></DialogContent></Dialog>

      <Dialog open={samplingOpen} onOpenChange={setSamplingOpen}><DialogContent className="max-w-lg"><DialogHeader><DialogTitle>固定随机质检抽样</DialogTitle><DialogDescription>从当前日期、客服组和客服范围内随机选择含人工回复的接待分段，创建后样本不会随数据变化。</DialogDescription></DialogHeader>{samplingResult ? <div className="space-y-3 border p-4"><div className="flex items-center justify-between"><span className="font-medium">{samplingResult.name}</span><Badge variant="secondary">{samplingResult.sampleSize} 条</Badge></div><div className="text-xs text-muted-foreground">批次 #{samplingResult.id} · {formatDateTime(samplingResult.createdAt)}</div><div className="max-h-52 overflow-y-auto text-sm">{samplingResult.items.map((item) => <div key={item.assignmentId} className="border-b py-2">会话 #{item.conversationId} · 第 {item.sessionNo} 轮 · Assignment #{item.assignmentId}</div>)}</div></div> : <div className="grid gap-4 sm:grid-cols-2"><label className="space-y-1.5 text-sm sm:col-span-2">批次名称<Input value={samplingName} onChange={(event) => setSamplingName(event.target.value)} /></label><label className="space-y-1.5 text-sm">抽样开始<Input type="date" value={startAt} onChange={(event) => setStartAt(event.target.value)} /></label><label className="space-y-1.5 text-sm">抽样结束<Input type="date" value={endAt} onChange={(event) => setEndAt(event.target.value)} /></label><label className="space-y-1.5 text-sm sm:col-span-2">抽样数量<Input type="number" min={1} max={1000} value={sampleSize} onChange={(event) => setSampleSize(Number(event.target.value))} /></label></div>}<div className="flex justify-end gap-2"><Button variant="outline" onClick={() => setSamplingOpen(false)}>关闭</Button>{!samplingResult ? <Button disabled={samplingSaving} onClick={() => void createSamplingBatch()}>{samplingSaving ? "抽样中" : "创建抽样批次"}</Button> : null}</div></DialogContent></Dialog>
    </div>
  )
}

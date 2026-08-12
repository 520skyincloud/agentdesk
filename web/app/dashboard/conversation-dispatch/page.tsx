"use client"

import {
  ArrowRightLeftIcon,
  CheckCircle2Icon,
  Clock3Icon,
  MessageSquareTextIcon,
  RefreshCwIcon,
  RotateCcwIcon,
  ScaleIcon,
  SearchIcon,
  SendIcon,
  TimerIcon,
  UserRoundCheckIcon,
} from "lucide-react"
import { useRouter } from "next/navigation"
import { useCallback, useEffect, useMemo, useRef, useState, type KeyboardEvent } from "react"
import { toast } from "sonner"

import { useAuth } from "@/components/auth-provider"
import {
  DashboardPage,
  DashboardTableShell,
  DashboardTableStateRow,
  DashboardToolbar,
} from "@/components/dashboard-page"
import { ListPagination } from "@/components/list-pagination"
import {
  OptionCombobox,
  type ComboboxOption,
} from "@/components/option-combobox"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { ButtonGroup } from "@/components/ui/button-group"
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { Textarea } from "@/components/ui/textarea"
import { useI18n } from "@/i18n/provider"
import {
  assignConversation,
  assignConversationDispatch,
  autoAssignConversationDispatch,
  createAdminWebSocketUrl,
  fetchAgentTeamsAll,
  fetchConversationDispatchAgentLoads,
  fetchConversationDispatchStats,
  fetchConversationDispatchTasks,
  releaseConversationDispatch,
  transferConversationDispatch,
  type AdminAgentTeam,
  type ConversationDispatchAgentLoad,
  type ConversationDispatchStats,
  type ConversationDispatchTask,
  type PageResult,
} from "@/lib/api/admin"
import { createRealtimeConnectionManager } from "@/lib/realtime-connection"
import { formatDateTime } from "@/lib/utils"

const STATUS_OPTIONS = [
  { value: "all", labelKey: "conversationDispatch.statusAll" },
  { value: "pending", labelKey: "conversationDispatch.statusPending" },
  { value: "assigned", labelKey: "conversationDispatch.statusAssigned" },
  { value: "processing", labelKey: "conversationDispatch.statusProcessing" },
  { value: "warning", labelKey: "conversationDispatch.statusWarning" },
  { value: "timeout", labelKey: "conversationDispatch.statusTimeout" },
  { value: "closed", labelKey: "conversationDispatch.statusClosed" },
] as const

function statusBadgeVariant(status: string) {
  switch (status) {
    case "timeout":
      return "destructive" as const
    case "warning":
      return "secondary" as const
    case "pending":
    case "assigned":
      return "outline" as const
    case "processing":
      return "secondary" as const
    default:
      return "outline" as const
  }
}

function formatDuration(seconds: number) {
  if (!Number.isFinite(seconds) || seconds <= 0) return "0s"
  if (seconds < 60) return `${Math.floor(seconds)}s`
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${minutes}m`
  const hours = Math.floor(minutes / 60)
  const rest = minutes % 60
  return rest > 0 ? `${hours}h ${rest}m` : `${hours}h`
}

function isPendingDispatchTask(task: ConversationDispatchTask) {
  return task.currentAssigneeId === 0 && ["pending", "warning", "timeout"].includes(task.status)
}

function agentLabel(agent: ConversationDispatchAgentLoad) {
  return agent.displayName || agent.nickname || agent.username || `#${agent.userId}`
}

type ActionDialogState = {
  type: "takeover" | "assign" | "transfer" | "release"
  task: ConversationDispatchTask
} | null

export default function ConversationDispatchPage() {
  const t = useI18n()
  const router = useRouter()
  const { session } = useAuth()
  const permissions = useMemo(
    () => new Set(session?.permissions ?? []),
    [session?.permissions],
  )
  const canAssign = permissions.has("conversation.assign")
  const canTakeover = canAssign && ["super_admin", "admin", "tenant_admin", "cs_team_leader"].some(
    (role) => session?.roles.includes(role),
  )
  const canTransfer = permissions.has("conversation.transfer")
  const canRecycle = permissions.has("conversation.recycle")
  const canManageActions = canAssign || canTransfer || canRecycle
  const canViewTeams = permissions.has("agentTeam.view")
  const statusOptions = useMemo(
    () => STATUS_OPTIONS.map((item) => ({ value: item.value, label: t(item.labelKey) })),
    [t]
  )
  const [keywordInput, setKeywordInput] = useState("")
  const [keyword, setKeyword] = useState("")
  const [statusInput, setStatusInput] = useState("all")
  const [status, setStatus] = useState("all")
  const [teamInput, setTeamInput] = useState("0")
  const [teamFilter, setTeamFilter] = useState("0")
  const [page, setPage] = useState(1)
  const [limit, setLimit] = useState(20)
  const [loading, setLoading] = useState(true)
  const [refreshing, setRefreshing] = useState(false)
  const [actionLoadingId, setActionLoadingId] = useState<number | null>(null)
  const [teams, setTeams] = useState<ComboboxOption[]>([
    { value: "0", label: t("conversationDispatch.allTeams") },
  ])
  const [result, setResult] = useState<PageResult<ConversationDispatchTask>>({
    results: [],
    page: { page: 1, limit: 20, total: 0 },
  })
  const [stats, setStats] = useState<ConversationDispatchStats>({
    total: 0,
    pending: 0,
    assigned: 0,
    processing: 0,
    warning: 0,
    timeout: 0,
    closed: 0,
    manageablePending: 0,
    manageableAssigned: 0,
    availableAgents: 0,
  })
  const [agents, setAgents] = useState<ConversationDispatchAgentLoad[]>([])
  const [dialog, setDialog] = useState<ActionDialogState>(null)
  const [dialogAssignee, setDialogAssignee] = useState("0")
  const [dialogReason, setDialogReason] = useState("")

  const availabilityLabels = useMemo<Record<string, string>>(
    () => ({
      available: t("conversationDispatch.availabilityAvailable"),
      profile_disabled: t("conversationDispatch.availabilityProfileDisabled"),
      auto_assign_disabled: t("conversationDispatch.availabilityAutoAssignDisabled"),
      capacity_missing: t("conversationDispatch.availabilityCapacityMissing"),
      account_disabled: t("conversationDispatch.availabilityAccountDisabled"),
      permission_missing: t("conversationDispatch.availabilityPermissionMissing"),
      no_active_schedule: t("conversationDispatch.availabilityNoActiveSchedule"),
      out_of_shift: t("conversationDispatch.availabilityOutOfShift"),
      offline: t("conversationDispatch.availabilityOffline"),
      break: t("conversationDispatch.availabilityBreak"),
      busy: t("conversationDispatch.availabilityBusy"),
      at_capacity: t("conversationDispatch.availabilityAtCapacity"),
    }),
    [t]
  )

  const agentOptions = useMemo(
    () => [
      { value: "0", label: t("conversationDispatch.selectAgent") },
      ...agents
        .filter((item) => item.manuallyAssignable && (!dialog?.task.teamId || item.teamId === dialog.task.teamId))
        .map((item) => ({
          value: String(item.userId),
          label: `${agentLabel(item)} · ${item.activeCount}/${item.maxConcurrentCount || "∞"} · ${availabilityLabels[item.availabilityCode] || item.availabilityReason}`,
        })),
    ],
    [agents, availabilityLabels, dialog?.task.teamId, t]
  )

  const query = useMemo(
    () => ({
      keyword: keyword.trim() || undefined,
      status: status === "all" ? undefined : status,
      teamId: teamFilter === "0" ? undefined : teamFilter,
      page,
      limit,
    }),
    [keyword, limit, page, status, teamFilter]
  )

  const loadData = useCallback(async () => {
    setLoading(true)
    try {
      const [tasksData, statsData, loadsData] = await Promise.all([
        fetchConversationDispatchTasks(query),
        fetchConversationDispatchStats(query),
        fetchConversationDispatchAgentLoads({
          teamId: teamFilter === "0" ? undefined : teamFilter,
        }),
      ])
      setResult(tasksData)
      setStats(statsData)
      setAgents(loadsData)
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("conversationDispatch.loadFailed"))
    } finally {
      setLoading(false)
    }
  }, [query, teamFilter, t])

  const loadDataRef = useRef(loadData)
  useEffect(() => {
    loadDataRef.current = loadData
  }, [loadData])

  useEffect(() => {
    if (!canViewTeams) {
      setTeams([{ value: "0", label: t("conversationDispatch.allTeams") }])
      return
    }
    let cancelled = false
    async function loadTeams() {
      try {
        const data = await fetchAgentTeamsAll()
        if (!cancelled) {
          setTeams([
            { value: "0", label: t("conversationDispatch.allTeams") },
            ...data.map((item: AdminAgentTeam) => ({
              value: String(item.id),
              label: item.name,
            })),
          ])
        }
      } catch (error) {
        if (!cancelled) {
          toast.error(error instanceof Error ? error.message : t("conversationDispatch.loadTeamsFailed"))
        }
      }
    }
    void loadTeams()
    return () => {
      cancelled = true
    }
  }, [canViewTeams, t])

  useEffect(() => {
    void loadData()
  }, [loadData])

  useEffect(() => {
    if (!permissions.has("conversation.handover")) return
    let refreshTimer: number | null = null
    const scheduleRefresh = () => {
      if (document.visibilityState === "hidden" || refreshTimer !== null) return
      refreshTimer = window.setTimeout(() => {
        refreshTimer = null
        void loadDataRef.current()
      }, 300)
    }
    const realtime = createRealtimeConnectionManager({
      createSocket: () => new WebSocket(createAdminWebSocketUrl()),
      canReconnect: () => Boolean(session?.activeTenantId),
      onOpen: scheduleRefresh,
      onMessage: (event) => {
        try {
          const payload = JSON.parse(event.data) as { type?: string }
          if (payload.type?.startsWith("conversation.") || payload.type?.startsWith("message.")) {
            scheduleRefresh()
          }
        } catch {
          // Ignore malformed realtime payloads; hidden-tab polling remains available.
        }
      },
    })
    const hiddenPolling = window.setInterval(() => {
      if (document.visibilityState === "hidden") {
        void loadDataRef.current()
      }
    }, 60_000)
    const handleVisibilityChange = () => {
      if (document.visibilityState === "visible") scheduleRefresh()
    }
    document.addEventListener("visibilitychange", handleVisibilityChange)
    realtime.connect()
    return () => {
      realtime.disconnect()
      document.removeEventListener("visibilitychange", handleVisibilityChange)
      window.clearInterval(hiddenPolling)
      if (refreshTimer !== null) window.clearTimeout(refreshTimer)
    }
  }, [permissions, session?.activeTenantId])

  function submitFilters() {
    setKeyword(keywordInput)
    setStatus(statusInput)
    setTeamFilter(teamInput)
    setPage(1)
  }

  function handleKeywordKeyDown(event: KeyboardEvent<HTMLInputElement>) {
    if (event.key === "Enter") {
      submitFilters()
    }
  }

  async function handleRefresh() {
    setRefreshing(true)
    try {
      await loadData()
    } finally {
      setRefreshing(false)
    }
  }

  async function handleAutoAssign(task: ConversationDispatchTask) {
    if (!canAssign) {
      return
    }
    setActionLoadingId(task.conversationId)
    try {
      await autoAssignConversationDispatch(task.conversationId, task.teamId || undefined)
      toast.success(t("conversationDispatch.autoAssignSuccess"))
      await loadData()
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("conversationDispatch.autoAssignFailed"))
    } finally {
      setActionLoadingId(null)
    }
  }

  function openActionDialog(type: "takeover" | "assign" | "transfer" | "release", task: ConversationDispatchTask) {
    if (!canUseAction(type)) {
      return
    }
    setDialog({ type, task })
    setDialogAssignee(type === "assign" ? String(task.recommendedAssigneeId || 0) : "0")
    setDialogReason("")
  }

  async function submitActionDialog() {
    if (!dialog || !canUseAction(dialog.type)) return
    if (!dialogReason.trim()) {
      toast.error(t("conversationDispatch.reasonRequired"))
      return
    }
    const conversationId = dialog.task.conversationId
    setActionLoadingId(conversationId)
    try {
      if (dialog.type === "takeover") {
        const assigneeId = session?.user.id ?? 0
        if (!assigneeId) {
          toast.error(t("conversationDispatch.takeoverRequiresSignIn"))
          return
        }
        await assignConversation(conversationId, assigneeId, dialogReason)
        toast.success(t("conversationDispatch.takeoverSuccess"))
        setDialog(null)
        router.push(`/dashboard/conversations?conversationId=${conversationId}`)
        return
      } else if (dialog.type === "assign") {
        const assigneeId = Number(dialogAssignee)
        if (!assigneeId) {
          toast.error(t("conversationDispatch.selectAgentRequired"))
          return
        }
        await assignConversationDispatch(conversationId, assigneeId, dialogReason)
        toast.success(t("conversationDispatch.assignSuccess"))
      } else if (dialog.type === "transfer") {
        const assigneeId = Number(dialogAssignee)
        if (!assigneeId) {
          toast.error(t("conversationDispatch.selectAgentRequired"))
          return
        }
        await transferConversationDispatch(conversationId, assigneeId, dialogReason)
        toast.success(t("conversationDispatch.transferSuccess"))
      } else {
        await releaseConversationDispatch(conversationId, dialogReason)
        toast.success(t("conversationDispatch.releaseSuccess"))
      }
      setDialog(null)
      await loadData()
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("conversationDispatch.actionFailed"))
    } finally {
      setActionLoadingId(null)
    }
  }

  function canUseAction(type: "takeover" | "assign" | "transfer" | "release") {
    if (type === "takeover") return canTakeover
    if (type === "assign") return canAssign
    if (type === "transfer") return canTransfer
    return canRecycle
  }

  const statCards = [
    { key: "pending", label: t("conversationDispatch.statusPending"), value: stats.pending, icon: <Clock3Icon className="size-4" /> },
    { key: "assigned", label: t("conversationDispatch.statusAssigned"), value: stats.assigned, icon: <SendIcon className="size-4" /> },
    { key: "processing", label: t("conversationDispatch.statusProcessing"), value: stats.processing, icon: <MessageSquareTextIcon className="size-4" /> },
    { key: "timeout", label: t("conversationDispatch.statusTimeout"), value: stats.timeout, icon: <TimerIcon className="size-4" /> },
    { key: "availableAgents", label: t("conversationDispatch.availableAgents"), value: stats.availableAgents, icon: <UserRoundCheckIcon className="size-4" /> },
  ]

  return (
    <DashboardPage>
      <div className="grid gap-3 md:grid-cols-5">
        {statCards.map((item) => (
          <div key={item.key} className="rounded-lg border bg-card p-3 text-card-foreground">
            <div className="flex items-center justify-between text-sm text-muted-foreground">
              <span>{item.label}</span>
              {item.icon}
            </div>
            <div className="mt-2 text-2xl font-semibold tabular-nums">{item.value}</div>
          </div>
        ))}
      </div>

      <DashboardToolbar
        actions={
          <Button variant="outline" size="icon" onClick={handleRefresh} disabled={refreshing} title={t("common.refresh")}>
            <RefreshCwIcon className={refreshing ? "size-4 animate-spin" : "size-4"} />
          </Button>
        }
      >
        <div className="relative min-w-[220px] flex-1">
          <SearchIcon className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            value={keywordInput}
            onChange={(event) => setKeywordInput(event.target.value)}
            onKeyDown={handleKeywordKeyDown}
            placeholder={t("conversationDispatch.keywordPlaceholder")}
            className="pl-9"
          />
        </div>
        <OptionCombobox
          value={statusInput}
          options={statusOptions}
          onChange={setStatusInput}
          placeholder={t("conversationDispatch.allStatuses")}
        />
        {canViewTeams ? (
          <OptionCombobox
            value={teamInput}
            options={teams}
            onChange={setTeamInput}
            placeholder={t("conversationDispatch.allTeams")}
          />
        ) : null}
        <Button onClick={submitFilters}>{t("common.query")}</Button>
      </DashboardToolbar>

      <div className="grid gap-4 2xl:grid-cols-[minmax(0,1fr)_20rem]">
        <DashboardTableShell
          className="min-w-0"
          pagination={
            <ListPagination
              page={result.page.page}
              total={result.page.total}
              limit={result.page.limit}
              loading={loading || refreshing}
              onPageChange={setPage}
              onLimitChange={(nextLimit) => {
                setLimit(nextLimit)
                setPage(1)
              }}
            />
          }
        >
          <Table className="min-w-[72rem] table-fixed">
            <TableHeader>
              <TableRow>
                <TableHead className="w-[20rem]">{t("conversationDispatch.columnConversation")}</TableHead>
                <TableHead className="w-[10rem]">{t("conversationDispatch.columnSource")}</TableHead>
                <TableHead className="w-[9rem]">{t("conversationDispatch.columnState")}</TableHead>
                <TableHead className="w-[14rem]">{t("conversationDispatch.columnAssignee")}</TableHead>
                <TableHead className="w-[9rem]">{t("conversationDispatch.columnWait")}</TableHead>
                {canManageActions ? (
                  <TableHead className="w-[10rem] text-right">{t("common.actions")}</TableHead>
                ) : null}
              </TableRow>
            </TableHeader>
            <TableBody>
              {result.results.length === 0 ? (
                <DashboardTableStateRow
                  colSpan={canManageActions ? 6 : 5}
                  loading={loading}
                  emptyText={t("conversationDispatch.emptyTasks")}
                />
              ) : (
                result.results.map((task) => (
                  <TableRow key={task.conversationId}>
                  <TableCell className="align-top">
                    <div className="truncate font-medium">
                        {task.customerName || t("conversationDispatch.unknownCustomer", { id: task.conversationId })}
                      </div>
                    <div className="mt-1 truncate text-sm text-muted-foreground">
                        {task.lastMessageSummary || "-"}
                      </div>
                      {task.handoffReason ? (
                        <div className="mt-1 truncate text-xs text-muted-foreground">
                          {task.handoffReason}
                        </div>
                      ) : null}
                      <div className="mt-2 flex flex-wrap gap-1.5">
                        <Badge variant="outline">
                          {t("conversationDispatch.workloadWeight", { value: task.workloadWeight || 1 })}
                        </Badge>
                        <Badge variant="outline">
                          {t("conversationDispatch.priorityValue", { value: task.priority || 0 })}
                        </Badge>
                      </div>
                    </TableCell>
                    <TableCell className="align-top">
                      <div className="truncate text-sm">{task.storeName || "-"}</div>
                      <div className="truncate text-xs text-muted-foreground">
                        {task.wxWorkEmployeeName || task.wxWorkEmployeeUserId || "-"}
                      </div>
                      <div className="mt-1 truncate text-xs text-muted-foreground">{task.teamName || "-"}</div>
                    </TableCell>
                    <TableCell className="align-top">
                      <div className="flex flex-wrap gap-1.5">
                        <Badge variant={statusBadgeVariant(task.status)}>{task.statusLabel}</Badge>
                        {task.dispatchModeLabel ? <Badge variant="outline">{task.dispatchModeLabel}</Badge> : null}
                      </div>
                      {task.routeStatusLabel ? (
                        <div className="mt-1 text-xs text-muted-foreground">{task.routeStatusLabel}</div>
                      ) : null}
                    </TableCell>
                    <TableCell className="align-top">
                      <div className="truncate text-sm">{task.currentAssigneeName || "-"}</div>
                      {task.recommendedAssigneeName ? (
                        <div className="mt-1 truncate text-xs text-muted-foreground">
                          <ScaleIcon className="mr-1 inline size-3" />
                          {task.recommendedAssigneeName}
                        </div>
                      ) : null}
                      {task.assignmentReason ? (
                        <div className="mt-1 line-clamp-2 break-words text-xs text-muted-foreground">
                          {task.assignmentReason}
                        </div>
                      ) : null}
                    </TableCell>
                    <TableCell className="align-top whitespace-nowrap">
                      <div className="font-medium tabular-nums">
                        {task.slaType ? formatDuration(task.waitingSeconds) : "-"}
                      </div>
                      <div className="text-xs text-muted-foreground">
                        {task.slaDeadlineAt
                          ? t(
                              task.slaType === "queue"
                                ? "conversationDispatch.queueSlaUntil"
                                : "conversationDispatch.firstResponseSlaUntil",
                              { time: formatDateTime(task.slaDeadlineAt) },
                            )
                          : "-"}
                      </div>
                    </TableCell>
                    {canManageActions ? (
                      <TableCell className="align-top text-right whitespace-nowrap">
                        <ButtonGroup>
                          {canAssign ? (
                            <>
                              {canTakeover ? (
                                <Button
                                  size="icon"
                                  variant="outline"
                                  title={t("conversationDispatch.takeover")}
                                  disabled={!task.manageable || !isPendingDispatchTask(task) || actionLoadingId === task.conversationId}
                                  onClick={() => openActionDialog("takeover", task)}
                                >
                                  <UserRoundCheckIcon className="size-4" />
                                </Button>
                              ) : null}
                              <Button
                                size="icon"
                                variant="outline"
                                title={t("conversationDispatch.autoAssign")}
                                disabled={!task.manageable || !isPendingDispatchTask(task) || task.dispatchMode !== "rule" || actionLoadingId === task.conversationId}
                                onClick={() => handleAutoAssign(task)}
                              >
                                <ScaleIcon className="size-4" />
                              </Button>
                              <Button
                                size="icon"
                                variant="outline"
                                title={t("conversationDispatch.assign")}
                                disabled={!task.manageable || !isPendingDispatchTask(task) || actionLoadingId === task.conversationId}
                                onClick={() => openActionDialog("assign", task)}
                              >
                                <CheckCircle2Icon className="size-4" />
                              </Button>
                            </>
                          ) : null}
                          {canTransfer ? (
                            <Button
                              size="icon"
                              variant="outline"
                              title={t("conversationDispatch.transfer")}
                              disabled={!task.manageable || !task.currentAssigneeId || actionLoadingId === task.conversationId}
                              onClick={() => openActionDialog("transfer", task)}
                            >
                              <ArrowRightLeftIcon className="size-4" />
                            </Button>
                          ) : null}
                          {canRecycle ? (
                            <Button
                              size="icon"
                              variant="outline"
                              title={t("conversationDispatch.release")}
                              disabled={!task.manageable || !task.currentAssigneeId || actionLoadingId === task.conversationId}
                              onClick={() => openActionDialog("release", task)}
                            >
                              <RotateCcwIcon className="size-4" />
                            </Button>
                          ) : null}
                        </ButtonGroup>
                      </TableCell>
                    ) : null}
                  </TableRow>
                ))
              )}
            </TableBody>
          </Table>
        </DashboardTableShell>

        <div className="space-y-3">
          <div className="rounded-lg border bg-card p-4">
            <div className="text-sm font-medium">{t("conversationDispatch.agentLoad")}</div>
            <div className="mt-3 grid gap-x-6 md:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-1">
              {agents.length === 0 ? (
                <div className="py-8 text-center text-sm text-muted-foreground">
                  {t("conversationDispatch.emptyAgents")}
                </div>
              ) : (
                agents.map((agent) => (
                  <div key={agent.profileId} className="border-t py-3">
                    <div className="flex items-start justify-between gap-2">
                      <div className="min-w-0">
                        <div className="truncate text-sm font-medium">{agentLabel(agent)}</div>
                        <div className="truncate text-xs text-muted-foreground">{agent.teamName || "-"}</div>
                      </div>
                      <Badge variant={agent.available ? "secondary" : "outline"}>
                        {agent.available ? t("conversationDispatch.available") : t("conversationDispatch.unavailable")}
                      </Badge>
                    </div>
                    <div className="mt-3 grid grid-cols-2 gap-x-3 gap-y-2 text-xs">
                      <div>
                        <div className="text-muted-foreground">{t("conversationDispatch.activeCount")}</div>
                        <div className="mt-1 font-medium tabular-nums">
                          {agent.activeCount}/{agent.maxConcurrentCount || "∞"}
                        </div>
                      </div>
                      <div>
                        <div className="text-muted-foreground">{t("conversationDispatch.pendingFirstReply")}</div>
                        <div className="mt-1 font-medium tabular-nums">{agent.pendingFirstReply}</div>
                      </div>
                      <div>
                        <div className="text-muted-foreground">{t("conversationDispatch.weightedOpenLoad")}</div>
                        <div className="mt-1 font-medium tabular-nums">{agent.weightedOpenLoad}</div>
                      </div>
                      <div>
                        <div className="text-muted-foreground">{t("conversationDispatch.shiftWorkloadWeight")}</div>
                        <div className="mt-1 font-medium tabular-nums">{agent.shiftWorkloadWeight}</div>
                      </div>
                    </div>
                    <div className="mt-2 text-xs text-muted-foreground">
                      {t("conversationDispatch.normalizedLoad", { value: agent.normalizedLoad.toFixed(2) })}
                    </div>
                    <div className="mt-1 text-xs text-muted-foreground">
                      {availabilityLabels[agent.availabilityCode] || agent.availabilityReason || t("conversationDispatch.unavailable")}
                    </div>
                  </div>
                ))
              )}
            </div>
          </div>
        </div>
      </div>

      <Dialog open={Boolean(dialog && canUseAction(dialog.type))} onOpenChange={(open) => !open && setDialog(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>
              {dialog?.type === "assign"
                ? t("conversationDispatch.assign")
                : dialog?.type === "takeover"
                  ? t("conversationDispatch.takeover")
                : dialog?.type === "transfer"
                  ? t("conversationDispatch.transfer")
                  : t("conversationDispatch.release")}
            </DialogTitle>
          </DialogHeader>
          {dialog?.type !== "release" && dialog?.type !== "takeover" ? (
            <OptionCombobox
              value={dialogAssignee}
              options={agentOptions}
              onChange={setDialogAssignee}
              placeholder={t("conversationDispatch.selectAgent")}
              searchPlaceholder={t("conversationDispatch.searchAgent")}
              emptyText={t("conversationDispatch.emptyAgents")}
            />
          ) : null}
          <Textarea
            value={dialogReason}
            onChange={(event) => setDialogReason(event.target.value)}
            placeholder={t(
              dialog?.type === "takeover"
                ? "conversationDispatch.takeoverReasonPlaceholder"
                : "conversationDispatch.reasonPlaceholder",
            )}
            aria-required="true"
          />
          <DialogFooter>
            <Button variant="outline" onClick={() => setDialog(null)}>
              {t("common.cancel")}
            </Button>
            <Button onClick={submitActionDialog} disabled={Boolean(dialog && actionLoadingId === dialog.task.conversationId)}>
              {t("common.confirm")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </DashboardPage>
  )
}

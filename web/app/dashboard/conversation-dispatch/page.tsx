"use client"

import {
  ArrowRightLeftIcon,
  BotIcon,
  CheckCircle2Icon,
  Clock3Icon,
  MessageSquareTextIcon,
  RefreshCwIcon,
  RotateCcwIcon,
  SearchIcon,
  SendIcon,
  TimerIcon,
  UserRoundCheckIcon,
} from "lucide-react"
import { useCallback, useEffect, useMemo, useState, type KeyboardEvent } from "react"
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
  assignConversationDispatch,
  autoAssignConversationDispatch,
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
  if (!Number.isFinite(seconds) || seconds <= 0) return "0m"
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${minutes}m`
  const hours = Math.floor(minutes / 60)
  const rest = minutes % 60
  return rest > 0 ? `${hours}h ${rest}m` : `${hours}h`
}

function agentLabel(agent: ConversationDispatchAgentLoad) {
  return agent.displayName || agent.nickname || agent.username || `#${agent.userId}`
}

type ActionDialogState = {
  type: "assign" | "transfer" | "release"
  task: ConversationDispatchTask
} | null

export default function ConversationDispatchPage() {
  const t = useI18n()
  const { session } = useAuth()
  const permissions = useMemo(
    () => new Set(session?.permissions ?? []),
    [session?.permissions],
  )
  const canHandover = permissions.has("conversation.handover")
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

  const agentOptions = useMemo(
    () => [
      { value: "0", label: t("conversationDispatch.selectAgent") },
      ...agents.map((item) => ({
        value: String(item.userId),
        label: `${agentLabel(item)} · ${item.activeCount}/${item.maxConcurrentCount || "∞"}`,
      })),
    ],
    [agents, t]
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
    if (!canHandover) {
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

  function openActionDialog(type: "assign" | "transfer" | "release", task: ConversationDispatchTask) {
    if (!canHandover) {
      return
    }
    setDialog({ type, task })
    setDialogAssignee(type === "transfer" ? String(task.currentAssigneeId || 0) : String(task.recommendedAssigneeId || 0))
    setDialogReason("")
  }

  async function submitActionDialog() {
    if (!canHandover || !dialog) return
    const conversationId = dialog.task.conversationId
    setActionLoadingId(conversationId)
    try {
      if (dialog.type === "assign") {
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
                {canHandover ? (
                  <TableHead className="w-[10rem] text-right">{t("common.actions")}</TableHead>
                ) : null}
              </TableRow>
            </TableHeader>
            <TableBody>
              {result.results.length === 0 ? (
                <DashboardTableStateRow
                  colSpan={canHandover ? 6 : 5}
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
                      {task.decisionConfidence ? (
                        <div className="mt-1 text-xs text-muted-foreground">
                          {t("conversationDispatch.confidence", { value: task.decisionConfidence })}
                        </div>
                      ) : null}
                    </TableCell>
                    <TableCell className="align-top">
                      <div className="truncate text-sm">{task.currentAssigneeName || "-"}</div>
                      {task.recommendedAssigneeName ? (
                        <div className="mt-1 truncate text-xs text-muted-foreground">
                          <BotIcon className="mr-1 inline size-3" />
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
                      <div className="font-medium tabular-nums">{formatDuration(task.waitingSeconds)}</div>
                      <div className="text-xs text-muted-foreground">
                        {task.manualExpireAt
                          ? t("conversationDispatch.manualWindowUntil", {
                              time: formatDateTime(task.manualExpireAt),
                            })
                          : "-"}
                      </div>
                    </TableCell>
                    {canHandover ? (
                      <TableCell className="align-top text-right whitespace-nowrap">
                        <ButtonGroup>
                          <Button
                            size="icon"
                            variant="outline"
                            title={t("conversationDispatch.autoAssign")}
                            disabled={!task.manageable || task.status !== "pending" || actionLoadingId === task.conversationId}
                            onClick={() => handleAutoAssign(task)}
                          >
                            <BotIcon className="size-4" />
                          </Button>
                          <Button
                            size="icon"
                            variant="outline"
                            title={t("conversationDispatch.assign")}
                            disabled={!task.manageable || task.status !== "pending" || actionLoadingId === task.conversationId}
                            onClick={() => openActionDialog("assign", task)}
                          >
                            <CheckCircle2Icon className="size-4" />
                          </Button>
                          <Button
                            size="icon"
                            variant="outline"
                            title={t("conversationDispatch.transfer")}
                            disabled={!task.manageable || !task.currentAssigneeId || actionLoadingId === task.conversationId}
                            onClick={() => openActionDialog("transfer", task)}
                          >
                            <ArrowRightLeftIcon className="size-4" />
                          </Button>
                          <Button
                            size="icon"
                            variant="outline"
                            title={t("conversationDispatch.release")}
                            disabled={!task.manageable || !task.currentAssigneeId || actionLoadingId === task.conversationId}
                            onClick={() => openActionDialog("release", task)}
                          >
                            <RotateCcwIcon className="size-4" />
                          </Button>
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
                        <div className="text-muted-foreground">{t("conversationDispatch.shiftAssignedWeight")}</div>
                        <div className="mt-1 font-medium tabular-nums">{agent.shiftAssignedWeight}</div>
                      </div>
                    </div>
                    <div className="mt-2 text-xs text-muted-foreground">
                      {t("conversationDispatch.normalizedLoad", { value: agent.normalizedLoad.toFixed(2) })}
                    </div>
                  </div>
                ))
              )}
            </div>
          </div>
        </div>
      </div>

      <Dialog open={canHandover && Boolean(dialog)} onOpenChange={(open) => !open && setDialog(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>
              {dialog?.type === "assign"
                ? t("conversationDispatch.assign")
                : dialog?.type === "transfer"
                  ? t("conversationDispatch.transfer")
                  : t("conversationDispatch.release")}
            </DialogTitle>
          </DialogHeader>
          {dialog?.type !== "release" ? (
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
            placeholder={t("conversationDispatch.reasonPlaceholder")}
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

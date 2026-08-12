"use client"

import Link from "next/link"
import { useRouter } from "next/navigation"
import { useCallback, useEffect, useMemo, useState, type ReactNode } from "react"
import {
  ActivityIcon,
  AlertTriangleIcon,
  ArrowRightIcon,
  BotIcon,
  Clock3Icon,
  HeadphonesIcon,
  MessageSquareReplyIcon,
  RefreshCwIcon,
  UsersIcon,
} from "lucide-react"
import {
  CartesianGrid,
  Legend,
  Line,
  LineChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts"
import { toast } from "sonner"

import { useAuth } from "@/components/auth-provider"
import { Badge } from "@/components/ui/badge"
import { Button, buttonVariants } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { useI18n } from "@/i18n/provider"
import {
  fetchDashboardOverview,
  type DashboardOverview,
  type DashboardRange,
} from "@/lib/api/dashboard"
import {
  dashboardPathIsAccessible,
  filterDashboardNavForSession,
} from "@/lib/navigation"
import { cn, formatDateTime } from "@/lib/utils"

function duration(seconds: number) {
  if (!Number.isFinite(seconds) || seconds <= 0) return "0秒"
  if (seconds < 60) return `${Math.round(seconds)}秒`
  if (seconds < 3600) return `${(seconds / 60).toFixed(1)}分`
  return `${(seconds / 3600).toFixed(1)}时`
}

function Metric({ title, value, detail, icon, href, alert }: {
  title: string
  value: string | number
  detail: string
  icon: ReactNode
  href: string
  alert?: boolean
}) {
  return (
    <Link href={href} className="group min-h-28 border bg-background p-4 transition-colors hover:border-primary/40 hover:bg-muted/20">
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="text-sm text-muted-foreground">{title}</div>
          <div className={cn("mt-2 text-2xl font-semibold tabular-nums", alert && "text-destructive")}>{value}</div>
          <div className="mt-1 truncate text-xs text-muted-foreground">{detail}</div>
        </div>
        <div className={cn("flex size-9 shrink-0 items-center justify-center rounded-md bg-sky-50 text-sky-700", alert && "bg-red-50 text-red-700")}>{icon}</div>
      </div>
    </Link>
  )
}

type TeamLoad = {
  id: number
  name: string
  total: number
  online: number
  idle: number
  busy: number
  breaks: number
  active: number
  capacity: number
}

function buildTeamLoads(data: DashboardOverview): TeamLoad[] {
  const groups = new Map<number, TeamLoad>()
  for (const agent of data.agents) {
    const current = groups.get(agent.teamId) ?? {
      id: agent.teamId,
      name: agent.teamName || "未分组",
      total: 0,
      online: 0,
      idle: 0,
      busy: 0,
      breaks: 0,
      active: 0,
      capacity: 0,
    }
    current.total += 1
    current.active += agent.currentActiveCount
    current.capacity += agent.maxConcurrentCount
    if (agent.currentStatus !== "offline") current.online += 1
    if (agent.currentStatus === "busy") current.busy += 1
    if (agent.currentStatus === "break") current.breaks += 1
    if (agent.currentStatus === "idle" || agent.currentStatus === "online") current.idle += 1
    groups.set(agent.teamId, current)
  }
  return Array.from(groups.values()).sort((a, b) => (b.active / Math.max(b.capacity, 1)) - (a.active / Math.max(a.capacity, 1)))
}

function LoadingState() {
  return <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-6">{Array.from({ length: 6 }).map((_, index) => <Skeleton key={index} className="h-28" />)}</div>
}

export function DashboardHome() {
  const t = useI18n()
  const router = useRouter()
  const { session } = useAuth()
  const navContext = useMemo(
    () => ({
      isPlatformAccount: Boolean(session?.isPlatformAccount),
      hasActiveTenant: (session?.activeTenantId ?? 0) > 0,
    }),
    [session?.activeTenantId, session?.isPlatformAccount],
  )
  const canViewOverview = session
    ? dashboardPathIsAccessible(
        "/dashboard",
        session.permissions,
        navContext,
        session.roles,
      )
    : false
  const canViewAnalytics = session?.permissions.includes("serviceAnalytics.view") ?? false
  const canViewRecords = session?.permissions.includes("conversationRecord.view") ?? false
  const canManageDispatch = session?.permissions.includes("conversation.handover") ?? false
  const fallbackPath = useMemo(() => {
    if (!session || canViewOverview) return null
    const sections = filterDashboardNavForSession(
      session.permissions,
      navContext,
      session.roles,
    )
    return sections.flatMap((section) => section.items).find((item) => item.url !== "/dashboard")?.url ?? null
  }, [canViewOverview, navContext, session])
  const [range, setRange] = useState<DashboardRange>("7d")
  const [data, setData] = useState<DashboardOverview | null>(null)
  const [loading, setLoading] = useState(true)
  const [refreshing, setRefreshing] = useState(false)

  const loadData = useCallback(async (nextRange: DashboardRange, refreshingOnly = false) => {
    if (!canViewOverview) {
      setLoading(false)
      return
    }
    if (refreshingOnly) {
      setRefreshing(true)
    } else {
      setLoading(true)
    }
    try {
      const result = await fetchDashboardOverview(nextRange)
      setData(result)
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("dashboardHome.loadFailed"))
    } finally {
      setLoading(false)
      setRefreshing(false)
    }
  }, [canViewOverview, t])

  useEffect(() => {
    if (!canViewOverview) {
      if (fallbackPath) router.replace(fallbackPath)
      return
    }
    void loadData(range)
  }, [canViewOverview, fallbackPath, loadData, range, router])

  const teamLoads = useMemo(() => data ? buildTeamLoads(data) : [], [data])
  const rangeOptions: Array<{ value: DashboardRange; label: string }> = [
    { value: "today", label: "今日" },
    { value: "7d", label: "近7天" },
    { value: "30d", label: "近30天" },
  ]

  if (!canViewOverview) {
    return <div className="flex min-h-60 items-center justify-center p-6 text-sm text-muted-foreground">{fallbackPath ? t("common.loading") : t("common.noAccessibleModules")}</div>
  }

  return (
    <div className="flex flex-1 flex-col gap-5 p-4 lg:p-6">
      <header className="flex flex-col gap-3 border-b pb-4 xl:flex-row xl:items-end xl:justify-between">
        <div>
          <h1 className="text-2xl font-semibold">客服运营总览</h1>
          <p className="mt-1 text-sm text-muted-foreground">当前快照与今日累计分开计算 · 更新时间 {formatDateTime(data?.generatedAt)}</p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <div className="flex rounded-md border p-1">
            {rangeOptions.map((item) => <Button key={item.value} variant={range === item.value ? "secondary" : "ghost"} size="sm" onClick={() => setRange(item.value)}>{item.label}</Button>)}
          </div>
          {canViewAnalytics ? <Link href="/dashboard/service-analytics/" className={buttonVariants({ variant: "outline", size: "sm" })}>运营分析<ArrowRightIcon /></Link> : null}
          <Button variant="outline" size="sm" onClick={() => void loadData(range, true)} disabled={loading || refreshing}><RefreshCwIcon className={refreshing ? "animate-spin" : ""} />刷新</Button>
        </div>
      </header>

      {loading && !data ? <LoadingState /> : data ? (
        <>
          <section>
            <div className="mb-2 flex items-center justify-between"><h2 className="text-sm font-semibold">当前快照</h2><span className="text-xs text-muted-foreground">按当前登录账号的数据范围</span></div>
            <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-6">
              <Metric title="正在排队" value={data.realtime.queueingCount} detail={`最长等待 ${duration(data.realtime.longestQueueSeconds)}`} icon={<Clock3Icon className="size-4" />} href={canManageDispatch ? "/dashboard/conversation-dispatch/?status=pending" : "/dashboard/conversations/"} alert={data.realtime.queueSlaAlertCount > 0} />
              <Metric title="SLA 告警" value={data.realtime.queueSlaAlertCount} detail="已超过本公司排队阈值" icon={<AlertTriangleIcon className="size-4" />} href={canManageDispatch ? "/dashboard/conversation-dispatch/?sla=overdue" : "/dashboard/conversations/"} alert={data.realtime.queueSlaAlertCount > 0} />
              <Metric title="人工处理中" value={data.realtime.assignedActiveCount} detail={`待人工回复 ${data.realtime.waitingReplyCount}`} icon={<HeadphonesIcon className="size-4" />} href="/dashboard/conversations/" />
              <Metric title="AI 接待中" value={data.realtime.aiActiveCount} detail={`全部开放轮次 ${data.realtime.openSessionCount}`} icon={<BotIcon className="size-4" />} href="/dashboard/conversations/" />
              <Metric title="在线客服" value={data.realtime.onlineAgentCount} detail={`空闲 ${data.realtime.idleAgentCount} · 忙碌 ${data.realtime.busyAgentCount} · 休息 ${data.realtime.breakAgentCount}`} icon={<UsersIcon className="size-4" />} href="/dashboard/agents/" />
              <Metric title="可用接待容量" value={data.realtime.availableCapacity} detail={`离线客服 ${data.realtime.offlineAgentCount}`} icon={<ActivityIcon className="size-4" />} href="/dashboard/agents/" alert={data.realtime.queueingCount > 0 && data.realtime.availableCapacity === 0} />
            </div>
          </section>

          <section className="border bg-background">
            <header className="flex items-center justify-between border-b px-4 py-3"><h2 className="text-sm font-semibold">今日累计</h2><span className="text-xs text-muted-foreground">自然日 00:00 至当前</span></header>
            <div className="grid grid-cols-2 divide-x divide-y md:grid-cols-3 xl:grid-cols-6 xl:divide-y-0">
              {[
                ["服务轮次", data.realtime.todaySessionCount],
                ["进入人工池", data.realtime.todayQueueCount],
                ["成功分配", data.realtime.todayAssignedCount],
                ["人工首响", data.realtime.todayHumanRepliedCount],
                ["转派轮次", data.realtime.todayTransferCount],
                ["消息总量", data.realtime.todayMessageCount],
              ].map(([label, value]) => <div key={String(label)} className="min-h-24 p-4"><div className="text-xs text-muted-foreground">{label}</div><div className="mt-2 text-2xl font-semibold tabular-nums">{Number(value).toLocaleString()}</div></div>)}
            </div>
          </section>

          <div className="grid gap-4 2xl:grid-cols-[1.08fr_0.92fr]">
            <section className="border bg-background">
              <header className="flex items-center justify-between border-b px-4 py-3"><h2 className="text-sm font-semibold">服务趋势</h2>{canViewRecords ? <Link href="/dashboard/conversation-monitor/" className="text-xs text-primary hover:underline">查看会话记录</Link> : null}</header>
              <div className="h-80 p-3">
                <ResponsiveContainer width="100%" height="100%">
                  <LineChart data={data.trend} margin={{ top: 12, right: 16, bottom: 4, left: 0 }}>
                    <CartesianGrid strokeDasharray="3 3" vertical={false} />
                    <XAxis dataKey="date" tick={{ fontSize: 11 }} />
                    <YAxis allowDecimals={false} tick={{ fontSize: 11 }} />
                    <Tooltip /><Legend />
                    <Line type="monotone" dataKey="sessions" name="会话轮次" stroke="#2563eb" strokeWidth={2} dot={false} />
                    <Line type="monotone" dataKey="humanQueues" name="进入人工池" stroke="#d97706" strokeWidth={2} dot={false} />
                    <Line type="monotone" dataKey="humanReplies" name="人工首响" stroke="#059669" strokeWidth={2} dot={false} />
                  </LineChart>
                </ResponsiveContainer>
              </div>
            </section>

            <section className="border bg-background">
              <header className="flex items-center justify-between border-b px-4 py-3"><h2 className="text-sm font-semibold">客服组当前负载</h2><Badge variant="outline">{teamLoads.length} 个客服组</Badge></header>
              <div className="overflow-x-auto">
                <Table className="min-w-180">
                  <TableHeader><TableRow><TableHead>客服组</TableHead><TableHead>在线</TableHead><TableHead>忙碌</TableHead><TableHead>休息</TableHead><TableHead>当前接待</TableHead><TableHead>容量</TableHead><TableHead>负载率</TableHead></TableRow></TableHeader>
                  <TableBody>{teamLoads.length ? teamLoads.map((team) => {
                    const loadRate = team.capacity > 0 ? team.active / team.capacity * 100 : 0
                    return <TableRow key={team.id}><TableCell className="font-medium">{team.name}</TableCell><TableCell>{team.online} / {team.total}</TableCell><TableCell>{team.busy}</TableCell><TableCell>{team.breaks}</TableCell><TableCell>{team.active}</TableCell><TableCell>{team.capacity}</TableCell><TableCell><span className={cn("font-medium", loadRate >= 85 && "text-destructive")}>{loadRate.toFixed(1)}%</span></TableCell></TableRow>
                  }) : <TableRow><TableCell colSpan={7} className="h-40 text-center text-muted-foreground">暂无客服组负载数据</TableCell></TableRow>}</TableBody>
                </Table>
              </div>
            </section>
          </div>

          <section className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
            <Metric title={`${rangeOptions.find((item) => item.value === range)?.label}会话`} value={data.summary.sessionCount} detail={`独立客户 ${data.summary.uniqueCustomerCount}`} icon={<MessageSquareReplyIcon className="size-4" />} href="/dashboard/service-analytics/" />
            <Metric title="有效人工接入率" value={`${data.summary.effectiveAccessRate.toFixed(1)}%`} detail={`人工已回复 ${data.summary.humanRepliedCount}`} icon={<HeadphonesIcon className="size-4" />} href="/dashboard/service-analytics/" />
            <Metric title="平均人工首响" value={duration(data.summary.averageFirstReplySeconds)} detail={`达标率 ${data.summary.firstReplySlaRate.toFixed(1)}%`} icon={<Clock3Icon className="size-4" />} href="/dashboard/service-analytics/" />
            <Metric title="客户满意率" value={`${data.summary.satisfactionRate.toFixed(1)}%`} detail={`已评价 ${data.summary.evaluationSubmittedCount}`} icon={<ActivityIcon className="size-4" />} href="/dashboard/service-analytics/" />
          </section>
        </>
      ) : <div className="flex min-h-60 items-center justify-center border border-dashed text-sm text-muted-foreground">暂无可用的运营数据</div>}
    </div>
  )
}

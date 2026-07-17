"use client"

import Link from "next/link"
import { useCallback, useEffect, useMemo, useState, type ReactNode } from "react"
import {
  ActivityIcon,
  ArrowRightLeftIcon,
  Building2Icon,
  Clock3Icon,
  DownloadIcon,
  GaugeIcon,
  MessageSquareIcon,
  RefreshCwIcon,
  Settings2Icon,
  ShieldCheckIcon,
  UserRoundIcon,
  UsersIcon,
} from "lucide-react"
import {
  Bar,
  BarChart,
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
import { OptionCombobox, type ComboboxOption } from "@/components/option-combobox"
import { Badge } from "@/components/ui/badge"
import { Button, buttonVariants } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Skeleton } from "@/components/ui/skeleton"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import {
  fetchAnalyticsDimensions,
  fetchAnalyticsOverview,
  fetchAnalyticsPolicy,
  exportAnalyticsOverview,
  updateAnalyticsPolicy,
  type AnalyticsAgent,
  type AnalyticsDimensions,
  type AnalyticsDistribution,
  type AnalyticsOverview,
  type ServiceAnalyticsPolicy,
} from "@/lib/api/service-analytics"
import { AnalyticsDataQuality, AnalyticsDataQualityLabels } from "@/lib/generated/enums"
import { cn, formatDateTime } from "@/lib/utils"
import { QualityOperations } from "./_components/quality-operations"

function localDateInput(daysAgo = 0) {
  const value = new Date()
  value.setDate(value.getDate() - daysAgo)
  const year = value.getFullYear()
  const month = String(value.getMonth() + 1).padStart(2, "0")
  const day = String(value.getDate()).padStart(2, "0")
  return `${year}-${month}-${day}`
}

function duration(seconds: number) {
  if (!Number.isFinite(seconds) || seconds <= 0) return "0秒"
  if (seconds < 60) return `${Math.round(seconds)}秒`
  if (seconds < 3600) return `${(seconds / 60).toFixed(1)}分`
  return `${(seconds / 3600).toFixed(1)}时`
}

function percent(value: number) {
  return `${(Number.isFinite(value) ? value : 0).toFixed(1)}%`
}

function number(value: number) {
  return new Intl.NumberFormat("zh-CN", { maximumFractionDigits: 1 }).format(value || 0)
}

const metricTone = {
  blue: "bg-sky-50 text-sky-700",
  green: "bg-emerald-50 text-emerald-700",
  amber: "bg-amber-50 text-amber-700",
  rose: "bg-rose-50 text-rose-700",
  zinc: "bg-zinc-100 text-zinc-700",
} as const

function MetricTile({
  title,
  value,
  detail,
  icon,
  tone = "blue",
  href,
}: {
  title: string
  value: string | number
  detail: string
  icon: ReactNode
  tone?: keyof typeof metricTone
  href?: string
}) {
  const content = (
    <div className="min-h-28 border bg-background p-4">
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <p className="text-sm text-muted-foreground">{title}</p>
          <p className="mt-2 text-2xl font-semibold tabular-nums">{value}</p>
          <p className="mt-1 text-xs text-muted-foreground">{detail}</p>
        </div>
        <div className={cn("flex size-9 shrink-0 items-center justify-center rounded-md", metricTone[tone])}>{icon}</div>
      </div>
    </div>
  )
  if (!href) return content
  return <Link href={href} className="block outline-none transition-colors hover:bg-muted/30 focus-visible:ring-2 focus-visible:ring-ring">{content}</Link>
}

function Panel({ title, meta, action, children, className }: { title: string; meta?: string; action?: ReactNode; children: ReactNode; className?: string }) {
  return (
    <section className={cn("border bg-background", className)}>
      <header className="flex min-h-12 items-center justify-between gap-3 border-b px-4 py-3">
        <h2 className="text-sm font-semibold">{title}</h2>
        <div className="flex items-center gap-2">
          {meta ? <span className="text-xs text-muted-foreground">{meta}</span> : null}
          {action}
        </div>
      </header>
      {children}
    </section>
  )
}

function VolumeTrendChart({ data }: { data: AnalyticsOverview["trend"] }) {
  return (
    <div className="h-80 w-full p-3">
      <ResponsiveContainer width="100%" height="100%">
        <LineChart data={data} margin={{ left: 0, right: 16, top: 12, bottom: 4 }}>
          <CartesianGrid strokeDasharray="3 3" vertical={false} />
          <XAxis dataKey="date" tick={{ fontSize: 12 }} />
          <YAxis tick={{ fontSize: 12 }} allowDecimals={false} />
          <Tooltip />
          <Legend />
          <Line type="monotone" dataKey="sessions" name="会话量" stroke="#2563eb" strokeWidth={2} dot={false} />
          <Line type="monotone" dataKey="humanQueues" name="转人工" stroke="#d97706" strokeWidth={2} dot={false} />
          <Line type="monotone" dataKey="humanReplies" name="人工已回复" stroke="#059669" strokeWidth={2} dot={false} />
        </LineChart>
      </ResponsiveContainer>
    </div>
  )
}

function SpeedTrendChart({ data }: { data: AnalyticsOverview["trend"] }) {
  return (
    <div className="h-80 w-full p-3">
      <ResponsiveContainer width="100%" height="100%">
        <LineChart data={data} margin={{ left: 0, right: 16, top: 12, bottom: 4 }}>
          <CartesianGrid strokeDasharray="3 3" vertical={false} />
          <XAxis dataKey="date" tick={{ fontSize: 12 }} />
          <YAxis tick={{ fontSize: 12 }} />
          <Tooltip />
          <Legend />
          <Line type="monotone" dataKey="averageQueue" name="平均排队" stroke="#d97706" strokeWidth={2} dot={false} />
          <Line type="monotone" dataKey="averageFirstReply" name="平均首响" stroke="#2563eb" strokeWidth={2} dot={false} />
          <Line type="monotone" dataKey="averageResponse" name="平均响应" stroke="#059669" strokeWidth={2} dot={false} />
        </LineChart>
      </ResponsiveContainer>
    </div>
  )
}

function DistributionChart({ data, color }: { data: AnalyticsDistribution[]; color: string }) {
  return (
    <div className="h-64 w-full p-3">
      <ResponsiveContainer width="100%" height="100%">
        <BarChart data={data} margin={{ left: 0, right: 12, top: 12, bottom: 4 }}>
          <CartesianGrid strokeDasharray="3 3" vertical={false} />
          <XAxis dataKey="label" tick={{ fontSize: 11 }} interval={0} />
          <YAxis tick={{ fontSize: 11 }} allowDecimals={false} />
          <Tooltip />
          <Bar dataKey="count" name="会话数" fill={color} radius={[3, 3, 0, 0]} />
        </BarChart>
      </ResponsiveContainer>
    </div>
  )
}

function statusBadge(agent: AnalyticsAgent) {
  if (agent.currentStatus === "busy") return <Badge className="bg-amber-100 text-amber-800">忙碌</Badge>
  if (agent.currentStatus === "break") return <Badge className="bg-sky-100 text-sky-800">休息</Badge>
  if (agent.currentStatus === "online") return <Badge className="bg-emerald-100 text-emerald-800">在线</Badge>
  if (agent.currentStatus === "idle") return <Badge className="bg-emerald-100 text-emerald-800">空闲</Badge>
  return <Badge variant="secondary">离线</Badge>
}

type AgentPerformanceView = "workload" | "response" | "quality" | "attendance"

function AgentPerformanceTable({
  agents,
  view,
  recordsHref,
}: {
  agents: AnalyticsAgent[]
  view: AgentPerformanceView
  recordsHref: (agentId: number, quickView?: string) => string
}) {
  return (
    <div className="overflow-x-auto">
      <Table className="min-w-240">
        <TableHeader>
          <TableRow>
            <TableHead className="sticky left-0 z-10 bg-background">客服</TableHead>
            <TableHead>客服组 / 小组</TableHead>
            {view === "workload" ? <><TableHead>派单</TableHead><TableHead>有效接入</TableHead><TableHead>未回复</TableHead><TableHead>人工消息</TableHead><TableHead>响应分段</TableHead><TableHead>服务时长</TableHead></> : null}
            {view === "response" ? <><TableHead>平均首响</TableHead><TableHead>首响 P50</TableHead><TableHead>首响 P90</TableHead><TableHead>平均响应</TableHead><TableHead>响应 P50</TableHead><TableHead>响应 P90</TableHead><TableHead>响应达标率</TableHead></> : null}
            {view === "quality" ? <><TableHead>可质检</TableHead><TableHead>已质检</TableHead><TableHead>通过率</TableHead><TableHead>质检均分</TableHead><TableHead>评价邀请</TableHead><TableHead>参评率</TableHead><TableHead>满意率</TableHead><TableHead>满意度</TableHead></> : null}
            {view === "attendance" ? <><TableHead>当前状态</TableHead><TableHead>在线时长</TableHead><TableHead>空闲</TableHead><TableHead>忙碌</TableHead><TableHead>休息</TableHead><TableHead>首次上线</TableHead><TableHead>最后在线</TableHead></> : null}
          </TableRow>
        </TableHeader>
        <TableBody>
          {agents.length ? (
            agents.map((agent) => (
              <TableRow key={agent.agentId}>
                <TableCell className="sticky left-0 z-10 bg-background font-medium"><Link href={recordsHref(agent.agentId)} className="text-primary hover:underline">{agent.agentName || `#${agent.agentId}`}</Link></TableCell>
                <TableCell>
                  <div>{agent.teamName || "-"}</div>
                  <div className="max-w-48 truncate text-xs text-muted-foreground">{agent.squadNames?.join("、") || "全组"}</div>
                </TableCell>
                {view === "workload" ? <><TableCell>{agent.assignedCount}</TableCell><TableCell><Link href={recordsHref(agent.agentId, "human")} className="text-primary hover:underline">{agent.repliedCount}</Link></TableCell><TableCell><Link href={recordsHref(agent.agentId, "waiting")} className="text-primary hover:underline">{agent.unansweredCount}</Link></TableCell><TableCell>{agent.humanMessageCount}</TableCell><TableCell>{agent.responseCount}</TableCell><TableCell>{duration(agent.serviceSeconds)}</TableCell></> : null}
                {view === "response" ? <><TableCell>{duration(agent.averageFirstReplySeconds)}</TableCell><TableCell>{duration(agent.p50FirstReplySeconds)}</TableCell><TableCell>{duration(agent.p90FirstReplySeconds)}</TableCell><TableCell>{duration(agent.averageResponseSeconds)}</TableCell><TableCell>{duration(agent.p50ResponseSeconds)}</TableCell><TableCell>{duration(agent.p90ResponseSeconds)}</TableCell><TableCell>{percent(agent.responseSlaRate)}</TableCell></> : null}
                {view === "quality" ? <><TableCell><Link href={recordsHref(agent.agentId, "quality_pending")} className="text-primary hover:underline">{agent.qualityInspectableCount}</Link></TableCell><TableCell>{agent.qualityInspectionCount}</TableCell><TableCell>{percent(agent.qualityPassRate)}</TableCell><TableCell>{agent.qualityInspectionCount ? agent.averageQualityScore.toFixed(1) : "-"}</TableCell><TableCell>{agent.evaluationInviteCount}</TableCell><TableCell>{percent(agent.evaluationParticipationRate)}</TableCell><TableCell>{percent(agent.satisfactionRate)}</TableCell><TableCell>{agent.evaluationSubmittedCount ? agent.averageSatisfaction.toFixed(1) : "-"}</TableCell></> : null}
                {view === "attendance" ? <><TableCell>{statusBadge(agent)}</TableCell><TableCell>{duration(agent.onlineSeconds)}</TableCell><TableCell>{duration(agent.idleSeconds)}</TableCell><TableCell>{duration(agent.busySeconds)}</TableCell><TableCell>{duration(agent.breakSeconds)}</TableCell><TableCell>{formatDateTime(agent.firstOnlineAt)}</TableCell><TableCell>{formatDateTime(agent.lastOnlineAt)}</TableCell></> : null}
              </TableRow>
            ))
          ) : (
            <TableRow><TableCell colSpan={10} className="h-28 text-center text-muted-foreground">暂无客服数据</TableCell></TableRow>
          )}
        </TableBody>
      </Table>
    </div>
  )
}

function QualityAgentTable({ agents }: { agents: AnalyticsAgent[] }) {
  return (
    <div className="overflow-x-auto">
      <Table className="min-w-220">
        <TableHeader>
          <TableRow>
            <TableHead className="sticky left-0 z-10 bg-background">客服</TableHead>
            <TableHead>客服组 / 小组</TableHead>
            <TableHead>可质检分段</TableHead>
            <TableHead>已完成</TableHead>
            <TableHead>待质检</TableHead>
            <TableHead>通过</TableHead>
            <TableHead>未通过</TableHead>
            <TableHead>通过率</TableHead>
            <TableHead>平均分</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {agents.length ? agents.map((agent) => (
            <TableRow key={agent.agentId}>
              <TableCell className="sticky left-0 z-10 bg-background font-medium">{agent.agentName || `#${agent.agentId}`}</TableCell>
              <TableCell>
                <div>{agent.teamName || "-"}</div>
                <div className="max-w-48 truncate text-xs text-muted-foreground">{agent.squadNames?.join("、") || "全组"}</div>
              </TableCell>
              <TableCell className="tabular-nums">{agent.qualityInspectableCount}</TableCell>
              <TableCell className="tabular-nums">{agent.qualityInspectionCount}</TableCell>
              <TableCell className="tabular-nums">{agent.qualityPendingCount}</TableCell>
              <TableCell className="tabular-nums">{agent.qualityPassedCount}</TableCell>
              <TableCell className="tabular-nums">{agent.qualityFailedCount}</TableCell>
              <TableCell>{percent(agent.qualityPassRate)}</TableCell>
              <TableCell>{agent.qualityInspectionCount ? agent.averageQualityScore.toFixed(1) : "-"}</TableCell>
            </TableRow>
          )) : (
            <TableRow><TableCell colSpan={9} className="h-28 text-center text-muted-foreground">所选范围内暂无人工回复质检样本</TableCell></TableRow>
          )}
        </TableBody>
      </Table>
    </div>
  )
}

function options(items: AnalyticsDimensions[keyof AnalyticsDimensions] | undefined, allLabel: string): ComboboxOption[] {
  return [{ value: "", label: allLabel }, ...(items ?? []).map((item) => ({ value: String(item.id), label: item.name || `#${item.id}` }))]
}

export default function ServiceAnalyticsPage() {
  const { session } = useAuth()
  const canView = session?.permissions.includes("serviceAnalytics.view") ?? false
  const canExport = session?.permissions.includes("serviceAnalytics.export") ?? false
  const canManagePolicy = session?.permissions.includes("serviceAnalytics.managePolicy") ?? false
  const canViewEvaluations = session?.permissions.includes("conversationEvaluation.view") ?? false
  const canViewQuality = session?.permissions.includes("qualityInspection.view") ?? false
  const canManageTemplates = session?.permissions.includes("qualityTemplate.manage") ?? false
  const [startAt, setStartAt] = useState(() => localDateInput(6))
  const [endAt, setEndAt] = useState(() => localDateInput())
  const [teamId, setTeamId] = useState("")
  const [squadId, setSquadId] = useState("")
  const [agentId, setAgentId] = useState("")
  const [storeId, setStoreId] = useState("")
  const [wxWorkInstanceId, setWxWorkInstanceId] = useState("")
  const [dataQuality, setDataQuality] = useState("")
  const [dimensions, setDimensions] = useState<AnalyticsDimensions | null>(null)
  const [data, setData] = useState<AnalyticsOverview | null>(null)
  const [loading, setLoading] = useState(true)
  const [policyOpen, setPolicyOpen] = useState(false)
  const [policyLoading, setPolicyLoading] = useState(false)
  const [policySaving, setPolicySaving] = useState(false)
  const [exporting, setExporting] = useState(false)
  const [policy, setPolicy] = useState<ServiceAnalyticsPolicy | null>(null)

  const recordsHref = useCallback((overrides: Record<string, string | number | undefined> = {}) => {
    const values: Record<string, string | number | undefined> = {
      startAt,
      endAt,
      teamId: teamId || undefined,
      squadId: squadId || undefined,
      agentId: agentId || undefined,
      storeId: storeId || undefined,
      wxWorkInstanceId: wxWorkInstanceId || undefined,
      dataQuality: dataQuality || undefined,
      ...overrides,
    }
    const query = new URLSearchParams()
    Object.entries(values).forEach(([key, value]) => {
      if (value !== undefined && value !== "") query.set(key, String(value))
    })
    return `/dashboard/conversation-monitor/?${query.toString()}`
  }, [agentId, dataQuality, endAt, squadId, startAt, storeId, teamId, wxWorkInstanceId])

  useEffect(() => {
    if (!canView) return
    void fetchAnalyticsDimensions()
      .then(setDimensions)
      .catch((error) => toast.error(error instanceof Error ? error.message : "筛选项加载失败"))
  }, [canView])

  const load = useCallback(async () => {
    if (!canView) return
    setLoading(true)
    try {
      setData(await fetchAnalyticsOverview({
        startAt,
        endAt,
        teamId: teamId || undefined,
        squadId: squadId || undefined,
        agentId: agentId || undefined,
        storeId: storeId || undefined,
        wxWorkInstanceId: wxWorkInstanceId || undefined,
        dataQuality: dataQuality || undefined,
      }))
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "报表加载失败")
    } finally {
      setLoading(false)
    }
  }, [agentId, canView, dataQuality, endAt, squadId, startAt, storeId, teamId, wxWorkInstanceId])

  useEffect(() => {
    void load()
  }, [load])

  const visibleSquads = useMemo(
    () => dimensions?.squads.filter((item) => !teamId || String(item.parentId) === teamId) ?? [],
    [dimensions, teamId],
  )
  const visibleAgents = useMemo(
    () => dimensions?.agents.filter((item) => !teamId || String(item.parentId) === teamId) ?? [],
    [dimensions, teamId],
  )
  const visibleWxWork = useMemo(
    () => dimensions?.wxWorkInstances.filter((item) => !storeId || String(item.parentId) === storeId) ?? [],
    [dimensions, storeId],
  )

  function changeTeam(value: string) {
    setTeamId(value)
    if (squadId && !dimensions?.squads.some((item) => String(item.id) === squadId && (!value || String(item.parentId) === value))) setSquadId("")
    if (agentId && !dimensions?.agents.some((item) => String(item.id) === agentId && (!value || String(item.parentId) === value))) setAgentId("")
  }

  function changeStore(value: string) {
    setStoreId(value)
    if (wxWorkInstanceId && !dimensions?.wxWorkInstances.some((item) => String(item.id) === wxWorkInstanceId && (!value || String(item.parentId) === value))) setWxWorkInstanceId("")
  }

  function setRange(days: number) {
    setStartAt(localDateInput(days - 1))
    setEndAt(localDateInput())
  }

  async function openPolicy() {
    setPolicyOpen(true)
    if (policy) return
    setPolicyLoading(true)
    try {
      setPolicy(await fetchAnalyticsPolicy())
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "统计口径加载失败")
    } finally {
      setPolicyLoading(false)
    }
  }

  async function savePolicy() {
    if (!policy) return
    const values = [
      policy.queueTargetSeconds,
      policy.firstResponseTargetSeconds,
      policy.responseTargetSeconds,
      policy.repeatConsultationHours,
      policy.satisfactionThreshold,
      policy.evaluationExpiryHours,
      policy.defaultSampleSize,
    ]
    if (values.some((value) => !Number.isInteger(value) || value <= 0)) {
      toast.error("统计口径必须填写正整数")
      return
    }
    if (policy.repeatConsultationHours > 168 || policy.satisfactionThreshold > 5 || policy.evaluationExpiryHours > 720 || policy.defaultSampleSize > 1000) {
      toast.error("复询、满意度、评价有效期或抽样数量超出允许范围")
      return
    }
    setPolicySaving(true)
    try {
      setPolicy(await updateAnalyticsPolicy(policy))
      setPolicyOpen(false)
      toast.success("统计口径已更新")
      await load()
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "统计口径保存失败")
    } finally {
      setPolicySaving(false)
    }
  }

  async function exportReport() {
    setExporting(true)
    try {
      const blob = await exportAnalyticsOverview({
        startAt,
        endAt,
        teamId: teamId || undefined,
        squadId: squadId || undefined,
        agentId: agentId || undefined,
        storeId: storeId || undefined,
        wxWorkInstanceId: wxWorkInstanceId || undefined,
        dataQuality: dataQuality || undefined,
      })
      const url = URL.createObjectURL(blob)
      const link = document.createElement("a")
      link.href = url
      link.download = `service-analytics-${localDateInput()}.csv`
      link.click()
      URL.revokeObjectURL(url)
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "报表导出失败")
    } finally {
      setExporting(false)
    }
  }

  if (!canView) {
    return <div className="p-6 text-sm text-muted-foreground">无权查看客服运营报表</div>
  }

  return (
    <div className="flex flex-1 flex-col gap-4 p-4 lg:p-6">
      <header className="flex flex-col gap-3 border-b pb-4 xl:flex-row xl:items-end xl:justify-between">
        <div>
          <h1 className="text-2xl font-semibold">客服运营报表</h1>
          <p className="mt-1 text-sm text-muted-foreground">更新时间 {formatDateTime(data?.generatedAt)}</p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <Button variant="outline" size="sm" onClick={() => setRange(1)}>今日</Button>
          <Button variant="outline" size="sm" onClick={() => setRange(7)}>近7天</Button>
          <Button variant="outline" size="sm" onClick={() => setRange(30)}>近30天</Button>
          <Button variant="outline" size="sm" onClick={() => void load()} disabled={loading}>
            <RefreshCwIcon className={loading ? "animate-spin" : ""} />
            刷新
          </Button>
          {canExport ? <Button variant="outline" size="sm" onClick={() => void exportReport()} disabled={exporting}><DownloadIcon />{exporting ? "导出中" : "导出"}</Button> : null}
          {canManagePolicy ? <Button variant="outline" size="sm" onClick={() => void openPolicy()}><Settings2Icon />统计口径</Button> : null}
        </div>
      </header>

      <section className="grid gap-2 border-b pb-4 sm:grid-cols-2 lg:grid-cols-4 2xl:grid-cols-8">
        <label className="space-y-1 text-xs text-muted-foreground">
          开始日期
          <Input type="date" value={startAt} onChange={(event) => setStartAt(event.target.value)} className="h-9" />
        </label>
        <label className="space-y-1 text-xs text-muted-foreground">
          结束日期
          <Input type="date" value={endAt} onChange={(event) => setEndAt(event.target.value)} className="h-9" />
        </label>
        <label className="space-y-1 text-xs text-muted-foreground">
          客服组
          <OptionCombobox value={teamId} options={options(dimensions?.teams, "全部客服组")} placeholder="全部客服组" onChange={changeTeam} triggerClassName="h-9" />
        </label>
        <label className="space-y-1 text-xs text-muted-foreground">
          客服小组
          <OptionCombobox value={squadId} options={options(visibleSquads, "全部小组")} placeholder="全部小组" onChange={setSquadId} triggerClassName="h-9" />
        </label>
        <label className="space-y-1 text-xs text-muted-foreground">
          客服
          <OptionCombobox value={agentId} options={options(visibleAgents, "全部客服")} placeholder="全部客服" onChange={setAgentId} triggerClassName="h-9" />
        </label>
        <label className="space-y-1 text-xs text-muted-foreground">
          门店
          <OptionCombobox value={storeId} options={options(dimensions?.stores, "全部门店")} placeholder="全部门店" onChange={changeStore} triggerClassName="h-9" />
        </label>
        <label className="space-y-1 text-xs text-muted-foreground">
          企微员工号
          <OptionCombobox value={wxWorkInstanceId} options={options(visibleWxWork, "全部员工号")} placeholder="全部员工号" onChange={setWxWorkInstanceId} triggerClassName="h-9" />
        </label>
        <label className="space-y-1 text-xs text-muted-foreground">
          数据质量
          <OptionCombobox
            value={dataQuality}
            options={[
              { value: "", label: "全部质量" },
              ...Object.values(AnalyticsDataQuality).map((value) => ({ value, label: AnalyticsDataQualityLabels[value] })),
            ]}
            placeholder="全部质量"
            onChange={setDataQuality}
            triggerClassName="h-9"
          />
        </label>
      </section>

      {loading && !data ? (
        <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-4">{Array.from({ length: 8 }).map((_, index) => <Skeleton key={index} className="h-28" />)}</div>
      ) : data ? (
        <Tabs defaultValue="overview" className="space-y-4">
          <div>
            <TabsList className="grid h-auto w-full grid-cols-3 gap-1 rounded-md p-1 group-data-horizontal/tabs:h-auto sm:grid-cols-6">
              <TabsTrigger value="overview">服务总览</TabsTrigger>
              <TabsTrigger value="response">响应效率</TabsTrigger>
              <TabsTrigger value="agents">客服表现</TabsTrigger>
              <TabsTrigger value="quality">质检与满意度</TabsTrigger>
              <TabsTrigger value="dispatch">派单质量</TabsTrigger>
              <TabsTrigger value="sources">来源分析</TabsTrigger>
            </TabsList>
          </div>

          <TabsContent value="overview" className="space-y-4">
            <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
              <MetricTile title="总会话量" value={number(data.summary.sessionCount)} detail={`客户 ${number(data.summary.uniqueCustomerCount)}`} icon={<ActivityIcon className="size-4" />} href={recordsHref()} />
              <MetricTile title="总消息量" value={number(data.summary.totalMessageCount)} detail={`会均 ${number(data.summary.averageMessagesPerSession)}`} icon={<MessageSquareIcon className="size-4" />} tone="green" />
              <MetricTile title="有效接入率" value={percent(data.summary.effectiveAccessRate)} detail={`人工已回复 ${data.summary.humanRepliedCount}`} icon={<GaugeIcon className="size-4" />} tone="green" href={recordsHref({ view: "human" })} />
              <MetricTile title="平均会话时长" value={duration(data.summary.averageSessionSeconds)} detail={`P50 ${duration(data.summary.p50SessionSeconds)} · P90 ${duration(data.summary.p90SessionSeconds)}`} icon={<Clock3Icon className="size-4" />} tone="amber" href={recordsHref()} />
              <MetricTile title="24小时复询率" value={percent(data.summary.repeatConsultationRate)} detail={`复询 ${data.summary.repeatConsultationCount}`} icon={<UserRoundIcon className="size-4" />} tone="rose" />
              <MetricTile title="转派率" value={percent(data.summary.transferRate)} detail={`转派会话 ${data.summary.transferSessionCount}`} icon={<ArrowRightLeftIcon className="size-4" />} tone="amber" />
              <MetricTile title="人工已回复" value={data.summary.humanRepliedCount} detail={`人工消息 ${number(data.summary.humanMessageCount)}`} icon={<ShieldCheckIcon className="size-4" />} href={recordsHref({ view: "human" })} />
              <MetricTile title="人工未回复" value={data.summary.unansweredCount} detail={`排队失败 ${data.summary.queueFailureCount}`} icon={<UsersIcon className="size-4" />} tone="rose" href={recordsHref({ view: "waiting" })} />
              <MetricTile title="精确数据" value={data.summary.exactSessionCount} detail={`总轮次 ${data.summary.sessionCount}`} icon={<ShieldCheckIcon className="size-4" />} tone="green" />
              <MetricTile title="估算数据" value={data.summary.estimatedSessionCount} detail="历史回填需留意" icon={<ActivityIcon className="size-4" />} tone="amber" />
              <MetricTile title="不完整数据" value={data.summary.incompleteSessionCount} detail="不参与部分效率结论" icon={<ActivityIcon className="size-4" />} tone="rose" />
              <MetricTile title="参评率" value={percent(data.summary.evaluationParticipationRate)} detail={`已评价 ${data.summary.evaluationSubmittedCount}`} icon={<UserRoundIcon className="size-4" />} />
            </div>
            <Panel title="服务量趋势"><VolumeTrendChart data={data.trend} /></Panel>
            <Panel title="会话时长分布"><DistributionChart data={data.sessionDurationDistribution} color="#d97706" /></Panel>
          </TabsContent>

          <TabsContent value="response" className="space-y-4">
            <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
              <MetricTile title="排队时长" value={duration(data.summary.averageQueueSeconds)} detail={`P50 ${duration(data.summary.p50QueueSeconds)} · P90 ${duration(data.summary.p90QueueSeconds)} · 达标 ${percent(data.summary.queueSlaRate)}`} icon={<Clock3Icon className="size-4" />} tone="amber" href={recordsHref({ view: "human" })} />
              <MetricTile title="人工首响" value={duration(data.summary.averageFirstReplySeconds)} detail={`P50 ${duration(data.summary.p50FirstReplySeconds)} · P90 ${duration(data.summary.p90FirstReplySeconds)} · 达标 ${percent(data.summary.firstReplySlaRate)}`} icon={<GaugeIcon className="size-4" />} href={recordsHref({ view: "human" })} />
              <MetricTile title="连续响应" value={duration(data.summary.averageResponseSeconds)} detail={`P50 ${duration(data.summary.p50ResponseSeconds)} · P90 ${duration(data.summary.p90ResponseSeconds)} · 达标 ${percent(data.summary.responseSlaRate)}`} icon={<ActivityIcon className="size-4" />} tone="green" href={recordsHref({ view: "human" })} />
              <MetricTile title="客户总等待" value={duration(data.summary.averageHumanWaitSeconds)} detail={`P50 ${duration(data.summary.p50HumanWaitSeconds)} · P90 ${duration(data.summary.p90HumanWaitSeconds)}`} icon={<UsersIcon className="size-4" />} tone="rose" href={recordsHref({ view: "waiting" })} />
            </div>
            <Panel title="响应速度趋势"><SpeedTrendChart data={data.trend} /></Panel>
            <div className="grid gap-4 xl:grid-cols-2">
              <Panel title="首次响应分布"><DistributionChart data={data.firstReplyDistribution} color="#2563eb" /></Panel>
              <Panel title="连续响应分布"><DistributionChart data={data.responseDistribution} color="#059669" /></Panel>
            </div>
          </TabsContent>

          <TabsContent value="agents" className="space-y-4">
            <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
              <MetricTile title="参与客服" value={data.agents.length} detail={`在线 ${data.realtime.onlineAgentCount}`} icon={<UsersIcon className="size-4" />} />
              <MetricTile title="人工消息" value={number(data.summary.humanMessageCount)} detail={`客户消息 ${number(data.summary.customerMessageCount)}`} icon={<MessageSquareIcon className="size-4" />} tone="green" />
              <MetricTile title="分配接入率" value={percent(data.summary.assignmentAccessRate)} detail={`已分配 ${data.summary.assignedCount}`} icon={<GaugeIcon className="size-4" />} tone="amber" />
              <MetricTile title="人工质检均分" value={data.summary.averageQualityScore ? data.summary.averageQualityScore.toFixed(1) : "-"} detail="人工回复样本" icon={<ShieldCheckIcon className="size-4" />} tone="rose" />
            </div>
            <Tabs defaultValue="workload" className="space-y-3">
              <TabsList className="grid h-auto w-full max-w-xl grid-cols-4">
                <TabsTrigger value="workload">工作量</TabsTrigger>
                <TabsTrigger value="response">响应</TabsTrigger>
                <TabsTrigger value="quality">质量</TabsTrigger>
                <TabsTrigger value="attendance">出勤</TabsTrigger>
              </TabsList>
              {(["workload", "response", "quality", "attendance"] as AgentPerformanceView[]).map((view) => (
                <TabsContent key={view} value={view}>
                  <Panel title={{ workload: "客服工作量", response: "客服响应效率", quality: "客服质量与满意度", attendance: "客服在线状态" }[view]}>
                    <AgentPerformanceTable agents={data.agents} view={view} recordsHref={(id, quickView) => recordsHref({ agentId: id, view: quickView })} />
                  </Panel>
                </TabsContent>
              ))}
            </Tabs>
          </TabsContent>

          <TabsContent value="quality" className="space-y-4">
            <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
              <MetricTile title="待质检" value={data.summary.qualityPendingCount} detail={`可质检分段 ${data.summary.qualityInspectableCount}`} icon={<ActivityIcon className="size-4" />} tone="amber" href={recordsHref({ view: "quality_pending" })} />
              <MetricTile title="已完成质检" value={data.summary.qualityInspectionCount} detail={`覆盖率 ${percent(data.summary.qualityCoverageRate)}`} icon={<ShieldCheckIcon className="size-4" />} href={recordsHref({ view: "quality_completed" })} />
              <MetricTile title="质检通过率" value={percent(data.summary.qualityPassRate)} detail={`通过 ${data.summary.qualityPassedCount} · 未通过 ${data.summary.qualityFailedCount}`} icon={<GaugeIcon className="size-4" />} tone="green" />
              <MetricTile title="人工质检均分" value={data.summary.qualityInspectionCount ? data.summary.averageQualityScore.toFixed(1) : "-"} detail="仅统计人工客服回复分段" icon={<ShieldCheckIcon className="size-4" />} tone="rose" />
              <MetricTile title="评价邀请" value={data.summary.evaluationInviteCount} detail={`已提交 ${data.summary.evaluationSubmittedCount}`} icon={<UserRoundIcon className="size-4" />} />
              <MetricTile title="评价参与率" value={percent(data.summary.evaluationParticipationRate)} detail="已提交 / 已邀请" icon={<ActivityIcon className="size-4" />} tone="amber" />
              <MetricTile title="客户满意率" value={percent(data.summary.satisfactionRate)} detail={`满意 ${data.summary.satisfiedCount}`} icon={<GaugeIcon className="size-4" />} tone="green" />
              <MetricTile title="平均满意度" value={data.summary.evaluationSubmittedCount ? data.summary.averageSatisfaction.toFixed(1) : "-"} detail="满分 5 分" icon={<ShieldCheckIcon className="size-4" />} tone="green" />
            </div>
            <Panel
              title="客服人工回复质检"
              meta={`${data.agents.filter((agent) => agent.qualityInspectableCount > 0).length} 名客服`}
              action={<div className="flex flex-wrap justify-end gap-2"><QualityOperations canViewEvaluations={canViewEvaluations} canViewQuality={canViewQuality} canManageTemplates={canManageTemplates} startAt={startAt} endAt={endAt} teamId={teamId} agentId={agentId} /><Link href={recordsHref({ view: "quality_pending" })} className={buttonVariants({ variant: "outline", size: "sm" })}><ShieldCheckIcon />质检会话</Link></div>}
            >
              <QualityAgentTable agents={data.agents.filter((agent) => agent.qualityInspectableCount > 0)} />
            </Panel>
          </TabsContent>

          <TabsContent value="dispatch" className="space-y-4">
            <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
              <MetricTile title="派单决策" value={data.dispatch.decisionCount} detail={`成功选择 ${data.dispatch.selectedCount} · 自动率 ${percent(data.dispatch.autoRate)}`} icon={<ActivityIcon className="size-4" />} href={recordsHref({ view: "human" })} />
              <MetricTile title="自动派单" value={data.dispatch.autoCount} detail={`规则 ${data.dispatch.ruleCount} · 模型 ${data.dispatch.modelCount} · 协同 ${data.dispatch.hybridCount}`} icon={<GaugeIcon className="size-4" />} tone="green" />
              <MetricTile title="降级 / 失败" value={data.dispatch.fallbackCount + data.dispatch.failedCount} detail={`降级 ${data.dispatch.fallbackCount} · 失败 ${data.dispatch.failedCount} · 过期 ${data.dispatch.staleCount}`} icon={<ShieldCheckIcon className="size-4" />} tone="rose" />
              <MetricTile title="人工干预" value={data.dispatch.overrideCount} detail={`人工派单 ${data.dispatch.manualCount} · 转派 ${data.dispatch.transferCount} · 均耗时 ${Math.round(data.dispatch.averageDecisionLatencyMillis)}ms`} icon={<ArrowRightLeftIcon className="size-4" />} tone="amber" />
            </div>
          </TabsContent>

          <TabsContent value="sources" className="space-y-4">
            <Panel title="门店员工号来源" meta={`${data.sources.length} 个来源`}>
              <div className="overflow-x-auto">
                <Table className="min-w-320">
                  <TableHeader><TableRow><TableHead>门店</TableHead><TableHead>企微员工号</TableHead><TableHead>会话</TableHead><TableHead>消息</TableHead><TableHead>转人工</TableHead><TableHead>人工已回复</TableHead><TableHead>有效接入率</TableHead><TableHead>平均首响</TableHead><TableHead>质检覆盖</TableHead><TableHead>质检通过</TableHead><TableHead>质检均分</TableHead><TableHead>参评率</TableHead><TableHead>满意率</TableHead><TableHead>满意度</TableHead></TableRow></TableHeader>
                  <TableBody>{data.sources.length ? data.sources.map((source) => <TableRow key={`${source.storeId}-${source.wxWorkInstanceId}`}><TableCell className="font-medium"><Link href={recordsHref({ storeId: source.storeId || undefined, wxWorkInstanceId: source.wxWorkInstanceId || undefined })} className="inline-flex items-center gap-2 text-primary hover:underline"><Building2Icon className="size-4" />{source.storeName || "未识别门店"}</Link></TableCell><TableCell>{source.wxWorkEmployeeName || "-"}</TableCell><TableCell>{source.sessionCount}</TableCell><TableCell>{source.messageCount}</TableCell><TableCell>{source.humanQueueCount}</TableCell><TableCell>{source.humanRepliedCount}</TableCell><TableCell>{percent(source.effectiveAccessRate)}</TableCell><TableCell>{duration(source.averageFirstReply)}</TableCell><TableCell>{percent(source.qualityCoverageRate)}</TableCell><TableCell>{percent(source.qualityPassRate)}</TableCell><TableCell>{source.qualityInspectionCount ? source.averageQualityScore.toFixed(1) : "-"}</TableCell><TableCell>{percent(source.evaluationParticipationRate)}</TableCell><TableCell>{percent(source.satisfactionRate)}</TableCell><TableCell>{source.evaluationSubmittedCount ? source.averageSatisfaction.toFixed(1) : "-"}</TableCell></TableRow>) : <TableRow><TableCell colSpan={14} className="h-28 text-center text-muted-foreground">暂无来源数据</TableCell></TableRow>}</TableBody>
                </Table>
              </div>
            </Panel>
          </TabsContent>
        </Tabs>
      ) : (
        <div className="border border-dashed p-10 text-center text-sm text-muted-foreground">暂无统计数据</div>
      )}

      <Dialog open={policyOpen} onOpenChange={setPolicyOpen}>
        <DialogContent className="max-w-xl">
          <DialogHeader>
            <DialogTitle>统计口径</DialogTitle>
            <DialogDescription>用于本公司的排队、首次响应、连续响应达标率与复询统计。</DialogDescription>
          </DialogHeader>
          {policyLoading || !policy ? <div className="grid gap-3 py-2 sm:grid-cols-2"><Skeleton className="h-16" /><Skeleton className="h-16" /><Skeleton className="h-16" /><Skeleton className="h-16" /></div> : (
            <div className="grid gap-4 py-2 sm:grid-cols-2">
              <label className="space-y-1.5 text-sm">排队达标阈值（秒）<Input type="number" min={1} value={policy.queueTargetSeconds} onChange={(event) => setPolicy({ ...policy, queueTargetSeconds: Number(event.target.value) })} /></label>
              <label className="space-y-1.5 text-sm">首次响应阈值（秒）<Input type="number" min={1} value={policy.firstResponseTargetSeconds} onChange={(event) => setPolicy({ ...policy, firstResponseTargetSeconds: Number(event.target.value) })} /></label>
              <label className="space-y-1.5 text-sm">连续响应阈值（秒）<Input type="number" min={1} value={policy.responseTargetSeconds} onChange={(event) => setPolicy({ ...policy, responseTargetSeconds: Number(event.target.value) })} /></label>
              <label className="space-y-1.5 text-sm">复询周期（小时）<Input type="number" min={1} max={168} value={policy.repeatConsultationHours} onChange={(event) => setPolicy({ ...policy, repeatConsultationHours: Number(event.target.value) })} /></label>
              <label className="space-y-1.5 text-sm">满意评价阈值（1-5分）<Input type="number" min={1} max={5} value={policy.satisfactionThreshold} onChange={(event) => setPolicy({ ...policy, satisfactionThreshold: Number(event.target.value) })} /></label>
              <label className="space-y-1.5 text-sm">评价链接有效期（小时）<Input type="number" min={1} max={720} value={policy.evaluationExpiryHours} onChange={(event) => setPolicy({ ...policy, evaluationExpiryHours: Number(event.target.value) })} /></label>
              <label className="space-y-1.5 text-sm">默认质检抽样数<Input type="number" min={1} max={1000} value={policy.defaultSampleSize} onChange={(event) => setPolicy({ ...policy, defaultSampleSize: Number(event.target.value) })} /></label>
            </div>
          )}
          <div className="flex justify-end gap-2">
            <Button variant="outline" onClick={() => setPolicyOpen(false)}>取消</Button>
            <Button onClick={() => void savePolicy()} disabled={policyLoading || policySaving || !policy}>{policySaving ? "保存中" : "保存"}</Button>
          </div>
        </DialogContent>
      </Dialog>
    </div>
  )
}

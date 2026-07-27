"use client"

import { useCallback, useEffect, useMemo, useState, type ReactNode } from "react"
import {
  CheckCircle2Icon,
  ChevronsUpDownIcon,
  CircleAlertIcon,
  CircleDollarSignIcon,
  DownloadIcon,
  GaugeIcon,
  RefreshCwIcon,
  Rows3Icon,
  SearchIcon,
  WalletCardsIcon,
} from "lucide-react"
import { toast } from "sonner"

import { useAuth } from "@/components/auth-provider"
import { OptionCombobox } from "@/components/option-combobox"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Input } from "@/components/ui/input"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import { Skeleton } from "@/components/ui/skeleton"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import {
  exportBillingQuery,
  fetchBillingQuery,
  fetchBillingQueryOptions,
  type BillingOfficialStore,
  type BillingQueryOptions,
  type BillingQueryRequest,
  type BillingQueryResult,
  type BillingStoreOption,
} from "@/lib/api/billing-query"
import { useAIConfigurationRealtime } from "@/hooks/use-ai-configuration-realtime"
import { cn, formatDateTime } from "@/lib/utils"

const maximumSelectedStores = 50

function localDateInput(daysAgo = 0) {
  const value = new Date()
  value.setDate(value.getDate() - daysAgo)
  const year = value.getFullYear()
  const month = String(value.getMonth() + 1).padStart(2, "0")
  const day = String(value.getDate()).padStart(2, "0")
  return `${year}-${month}-${day}`
}

function integer(value: number) {
  return new Intl.NumberFormat("zh-CN", { maximumFractionDigits: 0 }).format(value || 0)
}

function cny(value: number) {
  return new Intl.NumberFormat("zh-CN", {
    style: "currency",
    currency: "CNY",
    minimumFractionDigits: 2,
    maximumFractionDigits: 4,
  }).format(value || 0)
}

function percent(value: number) {
  return `${((Number.isFinite(value) ? value : 0) * 100).toFixed(1)}%`
}

function unixTime(value: number) {
  if (!value) return "-"
  return new Date(value * 1000).toLocaleString("zh-CN", { hour12: false })
}

function elapsed(value: number) {
  if (!value) return "-"
  if (value < 1000) return `${value} ms`
  return `${(value / 1000).toFixed(2)} s`
}

function Metric({ title, value, detail, icon, tone = "blue" }: {
  title: string
  value: string
  detail: string
  icon: ReactNode
  tone?: "blue" | "green" | "amber" | "rose" | "zinc"
}) {
  const tones = {
    blue: "bg-sky-50 text-sky-700",
    green: "bg-emerald-50 text-emerald-700",
    amber: "bg-amber-50 text-amber-700",
    rose: "bg-rose-50 text-rose-700",
    zinc: "bg-zinc-100 text-zinc-700",
  }
  return (
    <div className="min-h-28 border bg-background p-4">
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <p className="text-sm text-muted-foreground">{title}</p>
          <p className="mt-2 break-words text-2xl font-semibold tabular-nums">{value}</p>
          <p className="mt-1 text-xs text-muted-foreground">{detail}</p>
        </div>
        <span className={cn("flex size-9 shrink-0 items-center justify-center rounded-md", tones[tone])}>{icon}</span>
      </div>
    </div>
  )
}

function Section({ title, meta, children }: { title: string; meta?: string; children: ReactNode }) {
  return (
    <section className="border bg-background">
      <header className="flex min-h-12 flex-wrap items-center justify-between gap-2 border-b px-4 py-3">
        <h2 className="text-sm font-semibold">{title}</h2>
        {meta ? <span className="text-xs text-muted-foreground">{meta}</span> : null}
      </header>
      {children}
    </section>
  )
}

function StorePicker({ stores, selected, disabled, onChange }: {
  stores: BillingStoreOption[]
  selected: number[]
  disabled?: boolean
  onChange: (storeIds: number[]) => void
}) {
  const [search, setSearch] = useState("")
  const selectedSet = useMemo(() => new Set(selected), [selected])
  const filtered = useMemo(() => {
    const keyword = search.trim().toLowerCase()
    if (!keyword) return stores
    return stores.filter((store) => `${store.storeName} ${store.storeCode}`.toLowerCase().includes(keyword))
  }, [search, stores])
  const label = selected.length === 0
    ? `全部门店 (${stores.length})`
    : selected.length === 1
      ? stores.find((store) => store.storeId === selected[0])?.storeName || "已选 1 家"
      : `已选 ${selected.length} 家门店`

  function toggle(storeId: number) {
    if (selectedSet.has(storeId)) {
      onChange(selected.filter((id) => id !== storeId))
      return
    }
    if (selected.length >= maximumSelectedStores) {
      toast.error(`单次最多选择 ${maximumSelectedStores} 家门店`)
      return
    }
    onChange([...selected, storeId])
  }

  return (
    <Popover>
      <PopoverTrigger
        render={<Button variant="outline" className="w-full justify-between font-normal" disabled={disabled} />}
      >
        <span className="truncate">{label}</span>
        <ChevronsUpDownIcon className="size-4 shrink-0 opacity-50" />
      </PopoverTrigger>
      <PopoverContent align="start" className="w-[min(92vw,24rem)] gap-2 p-0">
        <div className="border-b p-2">
          <Input value={search} onChange={(event) => setSearch(event.target.value)} placeholder="搜索门店名称或编码" />
        </div>
        <div className="flex items-center justify-between border-b px-3 py-2 text-xs">
          <span className="text-muted-foreground">已选 {selected.length} / {maximumSelectedStores}</span>
          <div className="flex gap-1">
            <Button size="xs" variant="ghost" onClick={() => onChange(stores.slice(0, maximumSelectedStores).map((store) => store.storeId))}>全选</Button>
            <Button size="xs" variant="ghost" onClick={() => onChange([])}>全部门店</Button>
          </div>
        </div>
        <div className="max-h-72 overflow-y-auto p-1">
          {filtered.length === 0 ? <p className="px-3 py-6 text-center text-sm text-muted-foreground">没有匹配门店</p> : null}
          {filtered.map((store) => {
            const checked = selectedSet.has(store.storeId)
            return (
              <label
                key={store.storeId}
                className="flex w-full cursor-pointer items-center gap-3 rounded-md px-2 py-2 text-left hover:bg-muted"
              >
                <Checkbox checked={checked} onCheckedChange={() => toggle(store.storeId)} />
                <span className="min-w-0 flex-1">
                  <span className="block truncate text-sm">{store.storeName}</span>
                  <span className="block truncate text-xs text-muted-foreground">{store.storeCode} · {store.modelNames.join(" / ") || "模型未就绪"}</span>
                </span>
                <Badge variant={store.credentialStatus === "active" ? "default" : "outline"} className="shrink-0">
                  {store.credentialStatus === "active" ? "凭据就绪" : "未就绪"}
                </Badge>
              </label>
            )
          })}
        </div>
      </PopoverContent>
    </Popover>
  )
}

function statusBadge(status: string) {
  if (status === "ready" || status === "success" || status === "matched") return <Badge>正常</Badge>
  if (status === "failed") return <Badge variant="destructive">失败</Badge>
  if (status === "official_only") return <Badge variant="secondary">仅官方</Badge>
  if (status === "local_only") return <Badge variant="outline">仅本地</Badge>
  return <Badge variant="outline">{status || "-"}</Badge>
}

function OfficialStoreTable({ stores }: { stores: BillingOfficialStore[] }) {
  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>接入公司 / 门店</TableHead>
          <TableHead>模型</TableHead>
          <TableHead>状态</TableHead>
          <TableHead className="text-right">期间请求</TableHead>
          <TableHead className="text-right">期间金额</TableHead>
          <TableHead className="text-right">累计已用</TableHead>
          <TableHead className="text-right">当前可用</TableHead>
          <TableHead>Revision</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {stores.map((store) => (
          <TableRow key={`${store.tenantId}-${store.storeId}`}>
            <TableCell>
              <div className="font-medium">{store.storeName}</div>
              <div className="text-xs text-muted-foreground">{store.tenantName} · {store.storeCode}</div>
            </TableCell>
            <TableCell className="max-w-72 whitespace-normal">
              <span className="line-clamp-2">{store.modelNames.join(" / ") || "-"}</span>
            </TableCell>
            <TableCell>
              {statusBadge(store.status)}
              {store.errorMessage ? <div className="mt-1 max-w-64 whitespace-normal text-xs text-destructive">{store.errorMessage}</div> : null}
            </TableCell>
            <TableCell className="text-right tabular-nums">{integer(store.periodLogCount)}</TableCell>
            <TableCell className="text-right font-medium tabular-nums">{cny(store.periodCostCny)}</TableCell>
            <TableCell className="text-right tabular-nums">{store.status === "ready" ? cny(store.summary.usedCny) : "-"}</TableCell>
            <TableCell className="text-right tabular-nums">{store.summary.unlimitedQuota ? "不限" : store.status === "ready" ? cny(store.summary.availableCny) : "-"}</TableCell>
            <TableCell className="font-mono text-xs">P{store.modelProfileRevision || "-"} / K{store.credentialRevision || "-"}</TableCell>
          </TableRow>
        ))}
        {stores.length === 0 ? <TableRow><TableCell colSpan={8} className="h-28 text-center text-muted-foreground">暂无官方账单</TableCell></TableRow> : null}
      </TableBody>
    </Table>
  )
}

export default function BillingQueryPage() {
  const { session } = useAuth()
  const canView = session?.permissions.includes("billing.view") ?? false
  const canExport = session?.permissions.includes("billing.export") ?? false
  const [options, setOptions] = useState<BillingQueryOptions | null>(null)
  const [result, setResult] = useState<BillingQueryResult | null>(null)
  const [tenantId, setTenantId] = useState(0)
  const [storeIds, setStoreIds] = useState<number[]>([])
  const [startDate, setStartDate] = useState(localDateInput(6))
  const [endDate, setEndDate] = useState(localDateInput())
  const [modelName, setModelName] = useState("")
  const [requestId, setRequestId] = useState("")
  const [loading, setLoading] = useState(true)
  const [querying, setQuerying] = useState(false)
  const [exporting, setExporting] = useState(false)

  const visibleStores = useMemo(() => {
    if (!options) return []
    if (options.scopeMode === "platform" && tenantId > 0) {
      return options.stores.filter((store) => store.tenantId === tenantId)
    }
    return options.stores
  }, [options, tenantId])

  const payload = useCallback((limit = 500): BillingQueryRequest => ({
    tenantId: tenantId || undefined,
    storeIds,
    startDate,
    endDate,
    modelName: modelName.trim() || undefined,
    requestId: requestId.trim() || undefined,
    limit,
  }), [endDate, modelName, requestId, startDate, storeIds, tenantId])

  const query = useCallback(async (nextPayload?: BillingQueryRequest) => {
    if (!canView) return
    if (!nextPayload && storeIds.length === 0 && visibleStores.length > maximumSelectedStores) {
      toast.error(`当前公司超过 ${maximumSelectedStores} 家门店，请先选择具体门店`)
      return
    }
    setQuerying(true)
    try {
      setResult(await fetchBillingQuery(nextPayload ?? payload()))
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "读取模型账单失败")
    } finally {
      setQuerying(false)
    }
  }, [canView, payload, storeIds.length, visibleStores.length])

  const load = useCallback(async (range: { startDate: string; endDate: string }) => {
    if (!canView) {
      setLoading(false)
      return
    }
    setLoading(true)
    try {
      const next = await fetchBillingQueryOptions()
      setOptions(next)
      const nextTenantId = next.defaultTenantId || next.tenants[0]?.tenantId || 0
      const tenantStores = next.scopeMode === "platform" && nextTenantId > 0
        ? next.stores.filter((store) => store.tenantId === nextTenantId)
        : next.stores
      const nextStoreIds = next.defaultStoreId > 0
        ? [next.defaultStoreId]
        : tenantStores.length > maximumSelectedStores
          ? tenantStores.slice(0, maximumSelectedStores).map((store) => store.storeId)
          : []
      setTenantId(nextTenantId)
      setStoreIds(nextStoreIds)
      setResult(await fetchBillingQuery({
        tenantId: nextTenantId || undefined,
        storeIds: nextStoreIds,
        startDate: range.startDate,
        endDate: range.endDate,
        limit: 500,
      }))
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "读取账单范围失败")
    } finally {
      setLoading(false)
    }
  }, [canView])

  useEffect(() => {
    void load({ startDate: localDateInput(6), endDate: localDateInput() })
  }, [load])

  useAIConfigurationRealtime((event) => {
    if (
      event.type !== "store_model_profile.changed" &&
      event.type !== "store_model_credential.changed"
    ) {
      return
    }
    if (tenantId > 0 && event.tenantId > 0 && event.tenantId !== tenantId) return
    if (storeIds.length > 0 && !storeIds.includes(event.storeId)) return
    void fetchBillingQueryOptions().then(setOptions).catch(() => undefined)
    void query()
  }, canView)

  async function download() {
    if (!canExport) return
    if (storeIds.length === 0 && visibleStores.length > maximumSelectedStores) {
      toast.error(`当前公司超过 ${maximumSelectedStores} 家门店，请先选择具体门店`)
      return
    }
    setExporting(true)
    try {
      const blob = await exportBillingQuery(payload(10000))
      const url = URL.createObjectURL(blob)
      const link = document.createElement("a")
      link.href = url
      link.download = `model-billing-${startDate}-${endDate}.csv`
      document.body.appendChild(link)
      link.click()
      link.remove()
      URL.revokeObjectURL(url)
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "导出模型账单失败")
    } finally {
      setExporting(false)
    }
  }

  function changeTenant(value: string) {
    const nextTenantId = Number(value) || 0
    setTenantId(nextTenantId)
    const tenantStores = options?.stores.filter((store) => store.tenantId === nextTenantId) ?? []
    setStoreIds(tenantStores.length > maximumSelectedStores
      ? tenantStores.slice(0, maximumSelectedStores).map((store) => store.storeId)
      : [])
  }

  const officialLogs = useMemo(() => result?.official.stores.flatMap((store) =>
    store.logs.map((item) => ({ ...item, tenantName: store.tenantName }))) ?? [], [result])
  const availableCny = result?.official.stores.reduce((total, store) =>
    store.status === "ready" && !store.summary.unlimitedQuota ? total + store.summary.availableCny : total, 0) ?? 0
  const hasUnlimited = result?.official.stores.some((store) => store.status === "ready" && store.summary.unlimitedQuota) ?? false
  const scopeLabel = options?.scopeMode === "platform" ? "平台范围" : options?.scopeMode === "store" ? "本门店" : "当前公司"

  if (loading) {
    return <div className="space-y-4 p-4 md:p-6"><Skeleton className="h-9 w-56" /><Skeleton className="h-28 w-full" /><Skeleton className="h-96 w-full" /></div>
  }
  if (!canView) {
    return <div className="p-6"><div className="border p-8 text-center text-sm text-muted-foreground">当前账号没有模型账单查看权限</div></div>
  }

  return (
    <div className="space-y-5 p-4 md:p-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <div className="flex items-center gap-2">
            <h1 className="text-xl font-semibold">模型账单</h1>
            <Badge variant="outline">{scopeLabel}</Badge>
          </div>
          <p className="mt-1 text-xs text-muted-foreground">{result ? `${result.startDate} 至 ${result.endDate} · ${result.businessTimezone}` : "Asia/Shanghai"}</p>
        </div>
        <div className="flex items-center gap-2">
          <Tooltip>
            <TooltipTrigger render={<Button variant="outline" size="icon" onClick={() => void load({ startDate, endDate })} disabled={loading || querying} aria-label="刷新范围" />}>
              <RefreshCwIcon className={cn("size-4", (loading || querying) && "animate-spin")} />
            </TooltipTrigger>
            <TooltipContent>刷新范围</TooltipContent>
          </Tooltip>
          {canExport ? (
            <Tooltip>
              <TooltipTrigger render={<Button variant="outline" size="icon" onClick={() => void download()} disabled={exporting || querying} aria-label="导出 CSV" />}>
                <DownloadIcon className={cn("size-4", exporting && "animate-pulse")} />
              </TooltipTrigger>
              <TooltipContent>导出 CSV</TooltipContent>
            </Tooltip>
          ) : null}
        </div>
      </div>

      <section className="border bg-background p-4">
        <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-6">
          {options?.canFilterTenants ? (
            <label className="space-y-1.5 xl:col-span-1">
              <span className="text-xs font-medium">接入公司</span>
              <OptionCombobox
                value={tenantId ? String(tenantId) : ""}
                options={(options.tenants ?? []).map((tenant) => ({ value: String(tenant.tenantId), label: tenant.tenantName }))}
                placeholder="选择接入公司"
                onChange={changeTenant}
              />
            </label>
          ) : null}
          <label className="space-y-1.5 xl:col-span-1">
            <span className="text-xs font-medium">门店</span>
            <StorePicker stores={visibleStores} selected={storeIds} onChange={setStoreIds} disabled={options?.scopeMode === "store"} />
          </label>
          <label className="space-y-1.5">
            <span className="text-xs font-medium">开始日期</span>
            <Input type="date" value={startDate} onChange={(event) => setStartDate(event.target.value)} />
          </label>
          <label className="space-y-1.5">
            <span className="text-xs font-medium">结束日期</span>
            <Input type="date" value={endDate} onChange={(event) => setEndDate(event.target.value)} />
          </label>
          <label className="space-y-1.5">
            <span className="text-xs font-medium">模型</span>
            <Input value={modelName} onChange={(event) => setModelName(event.target.value)} placeholder="精确模型名" />
          </label>
          <label className="space-y-1.5">
            <span className="text-xs font-medium">Request ID</span>
            <Input value={requestId} onChange={(event) => setRequestId(event.target.value)} placeholder="精确查询" />
          </label>
        </div>
        <div className="mt-3 flex justify-end">
          <Button onClick={() => void query()} disabled={querying || (options?.canFilterTenants && tenantId <= 0)}>
            <SearchIcon className="size-4" />
            {querying ? "查询中" : "查询"}
          </Button>
        </div>
      </section>

      <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-5">
        <Metric title="期间官方金额" value={cny(result?.official.aggregate.periodCostCny ?? 0)} detail={`${integer(result?.official.aggregate.logCount ?? 0)} 次官方调用`} icon={<CircleDollarSignIcon />} tone="green" />
        <Metric title="当前可用额度" value={hasUnlimited ? "不限" : cny(availableCny)} detail={`${integer(result?.official.aggregate.successfulStores ?? 0)} 家门店可查询`} icon={<WalletCardsIcon />} tone="blue" />
        <Metric title="本地请求" value={integer(result?.local.aggregate.requestCount ?? 0)} detail={`失败 ${integer(result?.local.aggregate.failedCount ?? 0)}`} icon={<Rows3Icon />} tone="zinc" />
        <Metric title="Request ID 匹配率" value={percent(result?.reconciliation.matchRate ?? 0)} detail={`精确匹配 ${integer(result?.reconciliation.matchedCount ?? 0)}`} icon={<GaugeIcon />} tone="amber" />
        <Metric title="门店查询异常" value={integer(result?.official.aggregate.failedStores ?? 0)} detail={`共 ${integer(result?.official.aggregate.storeCount ?? 0)} 家门店`} icon={<CircleAlertIcon />} tone={(result?.official.aggregate.failedStores ?? 0) > 0 ? "rose" : "green"} />
      </div>

      <Tabs defaultValue="official" className="gap-4">
        {result?.scopeMode === "store" ? null : (
          <TabsList variant="line" className="h-10">
            <TabsTrigger value="official">官方账单</TabsTrigger>
            <TabsTrigger value="local">本地归因</TabsTrigger>
            <TabsTrigger value="reconciliation">Request ID 对账</TabsTrigger>
          </TabsList>
        )}
        <TabsContent value="official" className="space-y-4">
          <Section title="门店额度与期间汇总" meta={result ? `查询于 ${formatDateTime(result.queriedAt)}` : undefined}>
            <OfficialStoreTable stores={result?.official.stores ?? []} />
          </Section>
          <Section title="官方单次请求" meta={officialLogs.some((item) => item.requestId === "") ? "部分上游记录缺少 Request ID" : undefined}>
            <Table>
              <TableHeader><TableRow><TableHead>时间</TableHead><TableHead>接入公司 / 门店</TableHead><TableHead>模型</TableHead><TableHead className="text-right">输入 / 输出</TableHead><TableHead className="text-right">耗时</TableHead><TableHead className="text-right">人民币金额</TableHead><TableHead>Request ID</TableHead></TableRow></TableHeader>
              <TableBody>
                {officialLogs.map((item) => <TableRow key={`${item.storeId}-${item.id}`}><TableCell>{unixTime(item.createdAt)}</TableCell><TableCell><div className="font-medium">{item.storeName}</div><div className="text-xs text-muted-foreground">{item.tenantName}</div></TableCell><TableCell>{item.modelName || "-"}</TableCell><TableCell className="text-right tabular-nums">{integer(item.promptTokens)} / {integer(item.completionTokens)}</TableCell><TableCell className="text-right tabular-nums">{elapsed(item.useTime)}</TableCell><TableCell className="text-right font-medium tabular-nums">{cny(item.costCny)}</TableCell><TableCell className="max-w-72 font-mono text-xs whitespace-normal break-all">{item.requestId || "-"}</TableCell></TableRow>)}
                {officialLogs.length === 0 ? <TableRow><TableCell colSpan={7} className="h-28 text-center text-muted-foreground">当前范围没有官方调用明细</TableCell></TableRow> : null}
              </TableBody>
            </Table>
          </Section>
        </TabsContent>

        <TabsContent value="local" className="space-y-4">
          <Section title="本地不可变调用证据" meta={result?.local.truncated ? "结果已按服务端上限截断" : undefined}>
            <Table>
              <TableHeader><TableRow><TableHead>时间</TableHead><TableHead>接入公司 / 门店</TableHead><TableHead>阶段 / 用途</TableHead><TableHead>模型</TableHead><TableHead className="text-right">输入 / 输出 / 缓存</TableHead><TableHead className="text-right">延迟</TableHead><TableHead>状态</TableHead><TableHead>Request ID</TableHead></TableRow></TableHeader>
              <TableBody>
                {(result?.local.events ?? []).map((item) => <TableRow key={item.id}><TableCell>{formatDateTime(item.createdAt)}</TableCell><TableCell><div className="font-medium">{item.storeName}</div><div className="text-xs text-muted-foreground">{item.tenantName}</div></TableCell><TableCell><div>{item.stage || "-"}</div><div className="font-mono text-xs text-muted-foreground">{item.usageSlot || item.operationType || "-"}</div></TableCell><TableCell>{item.modelName || "-"}</TableCell><TableCell className="text-right tabular-nums">{integer(item.promptTokens)} / {integer(item.completionTokens)} / {integer(item.cachedPromptTokens)}</TableCell><TableCell className="text-right tabular-nums">{integer(item.latencyMs)} ms</TableCell><TableCell>{statusBadge(item.status)}{item.errorClass ? <div className="mt-1 text-xs text-destructive">{item.errorClass}</div> : null}</TableCell><TableCell className="max-w-72 font-mono text-xs whitespace-normal break-all">{item.requestId || "-"}</TableCell></TableRow>)}
                {(result?.local.events.length ?? 0) === 0 ? <TableRow><TableCell colSpan={8} className="h-28 text-center text-muted-foreground">当前范围没有本地调用证据</TableCell></TableRow> : null}
              </TableBody>
            </Table>
          </Section>
        </TabsContent>

        <TabsContent value="reconciliation" className="space-y-4">
          <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
            <Metric title="精确匹配" value={integer(result?.reconciliation.matchedCount ?? 0)} detail="Store + Request ID" icon={<CheckCircle2Icon />} tone="green" />
            <Metric title="仅官方" value={integer(result?.reconciliation.officialOnlyCount ?? 0)} detail="官方有记录，本地无匹配" icon={<CircleAlertIcon />} tone="amber" />
            <Metric title="仅本地" value={integer(result?.reconciliation.localOnlyCount ?? 0)} detail="本地有记录，官方无匹配" icon={<CircleAlertIcon />} tone="rose" />
            <Metric title="缺少 Request ID" value={integer(result?.reconciliation.missingRequestIdCount ?? 0)} detail="无法进入精确匹配" icon={<Rows3Icon />} tone="zinc" />
          </div>
          <Section title="对账明细" meta={result?.reconciliation.truncated ? "结果已按服务端上限截断" : undefined}>
            <Table>
              <TableHeader><TableRow><TableHead>时间</TableHead><TableHead>门店</TableHead><TableHead>状态</TableHead><TableHead>官方 / 本地模型</TableHead><TableHead className="text-right">官方 / 本地 Token</TableHead><TableHead className="text-right">官方金额</TableHead><TableHead>Request ID</TableHead></TableRow></TableHeader>
              <TableBody>
                {(result?.reconciliation.items ?? []).map((item, index) => <TableRow key={`${item.storeId}-${item.requestId}-${index}`}><TableCell>{formatDateTime(item.officialAt || item.localAt || "")}</TableCell><TableCell>{item.storeName}</TableCell><TableCell>{statusBadge(item.status)}</TableCell><TableCell><div>{item.officialModel || "-"}</div><div className="text-xs text-muted-foreground">{item.localModel || "-"}</div></TableCell><TableCell className="text-right tabular-nums">{integer(item.officialTokens)} / {integer(item.localTokens)}</TableCell><TableCell className="text-right font-medium tabular-nums">{cny(item.officialCostCny)}</TableCell><TableCell className="max-w-72 font-mono text-xs whitespace-normal break-all">{item.requestId || "-"}</TableCell></TableRow>)}
                {(result?.reconciliation.items.length ?? 0) === 0 ? <TableRow><TableCell colSpan={7} className="h-28 text-center text-muted-foreground">当前范围没有可对账记录</TableCell></TableRow> : null}
              </TableBody>
            </Table>
          </Section>
        </TabsContent>
      </Tabs>
    </div>
  )
}

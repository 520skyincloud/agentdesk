"use client"

import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import {
  ArrowUpDownIcon,
  CalendarClockIcon,
  ChevronLeftIcon,
  ChevronRightIcon,
  CircleDollarSignIcon,
  Clock3Icon,
  CopyIcon,
  DownloadIcon,
  GaugeIcon,
  KeyRoundIcon,
  LoaderCircleIcon,
  RefreshCwIcon,
  SearchIcon,
  WalletCardsIcon,
  WifiIcon,
  WifiOffIcon,
} from "lucide-react"
import { toast } from "sonner"

import { useAuth } from "@/components/auth-provider"
import { OptionCombobox } from "@/components/option-combobox"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { useStoreModelCredentialRealtime } from "@/hooks/use-store-model-credential-realtime"
import {
  fetchBillingQuery,
  fetchStoreModelCredential,
  fetchStoreModelCredentialStores,
  type BillingQuery,
  type StoreModelCredentialStoreOption,
} from "@/lib/api/admin"
import { cn, formatDateTime } from "@/lib/utils"

type SortKey =
  | "createdAt"
  | "modelName"
  | "promptTokens"
  | "completionTokens"
  | "useTime"
  | "costCny"

type SortDirection = "asc" | "desc"

const pageSizeOptions = [10, 20, 50, 100].map((value) => ({
  value: String(value),
  label: `${value} 条/页`,
}))

const modelToneClasses = [
  "border-blue-200 bg-blue-50 text-blue-700 dark:border-blue-900 dark:bg-blue-950/50 dark:text-blue-300",
  "border-emerald-200 bg-emerald-50 text-emerald-700 dark:border-emerald-900 dark:bg-emerald-950/50 dark:text-emerald-300",
  "border-violet-200 bg-violet-50 text-violet-700 dark:border-violet-900 dark:bg-violet-950/50 dark:text-violet-300",
  "border-amber-200 bg-amber-50 text-amber-800 dark:border-amber-900 dark:bg-amber-950/50 dark:text-amber-300",
  "border-rose-200 bg-rose-50 text-rose-700 dark:border-rose-900 dark:bg-rose-950/50 dark:text-rose-300",
  "border-cyan-200 bg-cyan-50 text-cyan-700 dark:border-cyan-900 dark:bg-cyan-950/50 dark:text-cyan-300",
]

function formatCNY(value: number, fractionDigits = 2) {
  return new Intl.NumberFormat("zh-CN", {
    style: "currency",
    currency: "CNY",
    minimumFractionDigits: fractionDigits,
    maximumFractionDigits: fractionDigits,
  }).format(value || 0)
}

function formatInteger(value: number) {
  return new Intl.NumberFormat("zh-CN").format(value || 0)
}

function formatEpochSeconds(value: number) {
  if (!value) return "长期有效"
  return formatDateTime(value * 1000)
}

function formatLogTime(value: number) {
  const formatted = formatEpochSeconds(value)
  const [date, time] = formatted.split(" ")
  return { date: date || formatted, time: time || "" }
}

function formatUseTime(value: number) {
  if (!value) return "-"
  return `${value} s`
}

function modelTone(modelName: string) {
  let hash = 0
  for (let index = 0; index < modelName.length; index += 1) {
    hash = (hash * 31 + modelName.charCodeAt(index)) >>> 0
  }
  return modelToneClasses[hash % modelToneClasses.length]
}

function getUseTimeTone(value: number) {
  if (!value || value <= 5) {
    return "border-emerald-200 bg-emerald-50 text-emerald-700 dark:border-emerald-900 dark:bg-emerald-950/50 dark:text-emerald-300"
  }
  if (value <= 15) {
    return "border-amber-200 bg-amber-50 text-amber-800 dark:border-amber-900 dark:bg-amber-950/50 dark:text-amber-300"
  }
  return "border-rose-200 bg-rose-50 text-rose-700 dark:border-rose-900 dark:bg-rose-950/50 dark:text-rose-300"
}

function csvValue(value: string | number) {
  const raw = String(value ?? "")
  const safe = /^[=+\-@]/.test(raw) ? `'${raw}` : raw
  return `"${safe.replaceAll('"', '""')}"`
}

function formatDateInput(value: Date) {
  const year = value.getFullYear()
  const month = String(value.getMonth() + 1).padStart(2, "0")
  const day = String(value.getDate()).padStart(2, "0")
  return `${year}-${month}-${day}`
}

function createDefaultBillingDateRange() {
  const end = new Date()
  const start = new Date(end)
  start.setDate(end.getDate() - 6)
  return {
    startDate: formatDateInput(start),
    endDate: formatDateInput(end),
  }
}

function SortHeader({
  label,
  sortKey,
  activeSortKey,
  onSort,
  className,
}: {
  label: string
  sortKey: SortKey
  activeSortKey: SortKey
  onSort: (key: SortKey) => void
  className?: string
}) {
  return (
    <button
      type="button"
      className={cn(
        "inline-flex h-8 items-center gap-1 font-medium text-muted-foreground transition-colors hover:text-foreground",
        activeSortKey === sortKey && "text-foreground",
        className,
      )}
      onClick={() => onSort(sortKey)}
    >
      {label}
      <ArrowUpDownIcon className="size-3.5" />
    </button>
  )
}

function ModelBadge({ name }: { name: string }) {
  const label = name.trim() || "-"
  return (
    <span
      className={cn(
        "inline-flex max-w-full items-center rounded-md border px-2 py-1 text-xs font-medium",
        modelTone(label),
      )}
      title={label}
    >
      <span className="truncate">{label}</span>
    </span>
  )
}

export default function BillingQueryPage() {
  const { session } = useAuth()
  const [initialDateRange] = useState(createDefaultBillingDateRange)
  const [stores, setStores] = useState<StoreModelCredentialStoreOption[]>([])
  const [selectedStoreId, setSelectedStoreId] = useState(0)
  const [billing, setBilling] = useState<BillingQuery | null>(null)
  const [loadingStores, setLoadingStores] = useState(true)
  const [loadingBilling, setLoadingBilling] = useState(false)
  const [refreshNotice, setRefreshNotice] = useState("")
  const [searchText, setSearchText] = useState("")
  const [sortKey, setSortKey] = useState<SortKey>("createdAt")
  const [sortDirection, setSortDirection] = useState<SortDirection>("desc")
  const [pageSize, setPageSize] = useState(10)
  const [page, setPage] = useState(1)
  const [startDate, setStartDate] = useState(initialDateRange.startDate)
  const [endDate, setEndDate] = useState(initialDateRange.endDate)
  const abortRef = useRef<AbortController | null>(null)
  const revisionRef = useRef(0)
  const appliedDateRangeRef = useRef(initialDateRange)
  const isSuperAdmin = session?.roles.includes("super_admin") ?? false

  useEffect(() => {
    let cancelled = false
    setLoadingStores(true)
    void fetchStoreModelCredentialStores()
      .then((items) => {
        if (cancelled) return
        setStores(items)
        setSelectedStoreId((current) =>
          current > 0 && items.some((item) => item.storeId === current)
            ? current
            : items[0]?.storeId || 0,
        )
      })
      .catch((error) => {
        if (!cancelled) {
          toast.error(error instanceof Error ? error.message : "读取门店失败")
        }
      })
      .finally(() => {
        if (!cancelled) setLoadingStores(false)
      })
    return () => {
      cancelled = true
    }
  }, [])

  const loadBilling = useCallback(
    async (showError = true) => {
      if (selectedStoreId <= 0) {
        setBilling(null)
        return
      }
      abortRef.current?.abort()
      const controller = new AbortController()
      abortRef.current = controller
      setLoadingBilling(true)
      try {
        const result = await fetchBillingQuery(
          selectedStoreId,
          appliedDateRangeRef.current.startDate,
          appliedDateRangeRef.current.endDate,
          controller.signal,
        )
        if (controller.signal.aborted) return
        revisionRef.current = result.credentialRevision
        setBilling(result)
        setRefreshNotice("")
      } catch (error) {
        if (controller.signal.aborted) return
        setBilling(null)
        if (showError) {
          toast.error(error instanceof Error ? error.message : "查询门店计费失败")
        }
      } finally {
        if (abortRef.current === controller) {
          abortRef.current = null
          setLoadingBilling(false)
        }
      }
    },
    [selectedStoreId],
  )

  const compareRevision = useCallback(async () => {
    if (selectedStoreId <= 0) return
    try {
      const metadata = await fetchStoreModelCredential(selectedStoreId)
      if (
        metadata.credentialRevision > 0 &&
        revisionRef.current > 0 &&
        metadata.credentialRevision !== revisionRef.current
      ) {
        abortRef.current?.abort()
        setBilling(null)
        setRefreshNotice("门店计费凭据已更新，正在重新查询")
        await loadBilling(false)
      }
    } catch {
      // The billing request remains the user-visible error surface.
    }
  }, [loadBilling, selectedStoreId])

  useEffect(() => {
    revisionRef.current = 0
    setBilling(null)
    setRefreshNotice("")
    setSearchText("")
    setPage(1)
    void loadBilling()
    void compareRevision()
    return () => abortRef.current?.abort()
  }, [compareRevision, loadBilling])

  useEffect(() => {
    if (selectedStoreId <= 0) return
    const timer = window.setInterval(() => {
      void compareRevision()
    }, 30000)
    return () => window.clearInterval(timer)
  }, [compareRevision, selectedStoreId])

  const realtimeStatus = useStoreModelCredentialRealtime(
    selectedStoreId,
    (payload) => {
      if (payload.credentialRevision === revisionRef.current) return
      abortRef.current?.abort()
      setBilling(null)
      setRefreshNotice("门店计费凭据已更新，正在重新查询")
      void loadBilling(false)
    },
    () => {
      void compareRevision()
    },
    () => {
      void compareRevision()
    },
  )

  const storeOptions = useMemo(
    () =>
      stores.map((item) => ({
        value: String(item.storeId),
        label: item.storeCode
          ? `${item.storeName} (${item.storeCode})`
          : item.storeName,
      })),
    [stores],
  )
  const selectedStore = stores.find((item) => item.storeId === selectedStoreId)

  const filteredLogs = useMemo(() => {
    const keyword = searchText.trim().toLowerCase()
    const logs = billing?.logs ? [...billing.logs] : []
    const filtered = keyword
      ? logs.filter(
          (item) =>
            item.modelName.toLowerCase().includes(keyword) ||
            item.requestId.toLowerCase().includes(keyword),
        )
      : logs
    filtered.sort((left, right) => {
      const leftValue = left[sortKey]
      const rightValue = right[sortKey]
      const result =
        typeof leftValue === "string" && typeof rightValue === "string"
          ? leftValue.localeCompare(rightValue)
          : Number(leftValue) - Number(rightValue)
      return sortDirection === "asc" ? result : -result
    })
    return filtered
  }, [billing?.logs, searchText, sortDirection, sortKey])

  const totalPages = Math.max(1, Math.ceil(filteredLogs.length / pageSize))
  const currentPage = Math.min(page, totalPages)
  const pageStart = (currentPage - 1) * pageSize
  const pagedLogs = filteredLogs.slice(pageStart, pageStart + pageSize)
  const pageEnd = Math.min(pageStart + pageSize, filteredLogs.length)

  useEffect(() => {
    setPage(1)
  }, [pageSize, searchText])

  function updateSort(nextSortKey: SortKey) {
    if (sortKey === nextSortKey) {
      setSortDirection((current) => (current === "asc" ? "desc" : "asc"))
      return
    }
    setSortKey(nextSortKey)
    setSortDirection(nextSortKey === "modelName" ? "asc" : "desc")
  }

  function queryBillingByDate() {
    if (!startDate || !endDate) {
      toast.error("请选择开始日期和结束日期")
      return
    }
    if (startDate > endDate) {
      toast.error("结束日期不能早于开始日期")
      return
    }
    appliedDateRangeRef.current = { startDate, endDate }
    setPage(1)
    void loadBilling()
  }

  function downloadCSV() {
    if (!billing) return
    const header = [
      "时间",
      "模型",
      "输入 Token",
      "输出 Token",
      "用时",
      "费用（人民币）",
      "请求 ID",
    ]
    const rows = filteredLogs.map((item) => [
      formatEpochSeconds(item.createdAt),
      item.modelName,
      item.promptTokens,
      item.completionTokens,
      item.useTime,
      item.costCny,
      item.requestId,
    ])
    const metadata = [
      ["门店", billing.storeName || "当前门店"],
      ["开始日期", billing.startDate],
      ["结束日期", billing.endDate],
      ["费用单位", "人民币"],
      ["筛选期间费用（人民币）", billing.periodCostCny],
      [],
    ]
    const content = [...metadata, header, ...rows]
      .map((row) => row.map(csvValue).join(","))
      .join("\n")
    const blob = new Blob([`\uFEFF${content}`], {
      type: "text/csv;charset=utf-8",
    })
    const url = URL.createObjectURL(blob)
    const link = document.createElement("a")
    link.href = url
    link.download = `${billing.storeName || "store"}-billing-${billing.startDate}-${billing.endDate}.csv`
    link.style.display = "none"
    document.body.appendChild(link)
    link.click()
    link.remove()
    window.setTimeout(() => URL.revokeObjectURL(url), 0)
    toast.success(`已导出 ${filteredLogs.length} 条计费记录`)
  }

  async function copyText(value: string, message: string) {
    try {
      await navigator.clipboard.writeText(value)
      toast.success(message)
    } catch {
      toast.error("复制失败，请稍后重试")
    }
  }

  function copyBillingSummary() {
    if (!billing) return
    const summary = [
      `门店：${billing.storeName || "当前门店"}`,
      `令牌名称：${billing.summary.name || "未命名"}`,
      `令牌总额：${billing.summary.unlimitedQuota ? "不限额" : formatCNY(billing.summary.grantedCny)}`,
      `剩余额度：${billing.summary.unlimitedQuota ? "不限额" : formatCNY(billing.summary.availableCny)}`,
      `账户累计已用：${formatCNY(billing.summary.usedCny, 6)}`,
      `查询日期：${billing.startDate} 至 ${billing.endDate}`,
      `筛选期间花费：${formatCNY(billing.periodCostCny, 6)}`,
      "费用单位：人民币",
      `有效期至：${formatEpochSeconds(billing.summary.expiresAt)}`,
    ].join("\n")
    void copyText(summary, "令牌信息已复制")
  }

  return (
    <main className="mx-auto flex w-full max-w-[1500px] flex-col gap-4 p-4 sm:p-6">
      <section className="overflow-hidden rounded-lg border border-border bg-card shadow-sm">
        <header className="flex flex-col gap-3 border-b border-border px-4 py-4 sm:px-5 lg:flex-row lg:items-center lg:justify-between">
          <div className="flex min-w-0 items-start gap-3">
            <div className="flex size-9 shrink-0 items-center justify-center rounded-md bg-primary/10 text-primary">
              <KeyRoundIcon className="size-4.5" />
            </div>
            <div className="min-w-0">
              <div className="flex flex-wrap items-center gap-2">
                <h1 className="text-lg font-semibold">计费查询</h1>
                <Badge variant="outline">New API 实际账单</Badge>
              </div>
              <p className="mt-1 text-sm leading-6 text-muted-foreground">
                服务端自动使用当前门店生效凭据查询，页面不接收、不保存也不展示密钥和模型地址。
              </p>
            </div>
          </div>
          <Badge
            variant={realtimeStatus === "connected" ? "default" : "outline"}
            className="w-fit"
          >
            {realtimeStatus === "connected" ? (
              <WifiIcon className="size-3" />
            ) : (
              <WifiOffIcon className="size-3" />
            )}
            {realtimeStatus === "connected"
              ? "实时更新已连接"
              : realtimeStatus === "connecting"
                ? "实时更新连接中"
                : "实时更新已断开"}
          </Badge>
        </header>

        <div className="p-4 sm:p-5">
          <div className="grid gap-2 rounded-lg border border-border bg-muted/40 p-1.5 xl:grid-cols-[minmax(260px,1fr)_170px_170px_auto]">
            <div className="flex min-h-11 min-w-0 items-center gap-2 px-2">
              <SearchIcon className="size-4 shrink-0 text-muted-foreground" />
              {isSuperAdmin ? (
                <OptionCombobox
                  value={selectedStoreId ? String(selectedStoreId) : ""}
                  options={storeOptions}
                  placeholder={loadingStores ? "正在读取门店" : "选择要查询的门店"}
                  disabled={loadingStores}
                  triggerClassName="h-10 min-w-0 flex-1 border-0 bg-transparent px-0 shadow-none hover:bg-transparent"
                  onChange={(value) => setSelectedStoreId(Number(value))}
                />
              ) : (
                <div className="min-w-0 flex-1 text-sm">
                  <span className="text-muted-foreground">当前门店：</span>
                  <span className="font-medium">
                    {selectedStore?.storeName || "未绑定"}
                  </span>
                </div>
              )}
            </div>
            <label className="flex min-h-11 items-center gap-2 rounded-md border border-border bg-card px-3">
              <span className="shrink-0 text-xs text-muted-foreground">开始</span>
              <Input
                type="date"
                value={startDate}
                max={endDate || undefined}
                className="h-9 min-w-0 border-0 bg-transparent px-0 shadow-none focus-visible:ring-0"
                aria-label="开始日期"
                onChange={(event) => setStartDate(event.target.value)}
              />
            </label>
            <label className="flex min-h-11 items-center gap-2 rounded-md border border-border bg-card px-3">
              <span className="shrink-0 text-xs text-muted-foreground">结束</span>
              <Input
                type="date"
                value={endDate}
                min={startDate || undefined}
                className="h-9 min-w-0 border-0 bg-transparent px-0 shadow-none focus-visible:ring-0"
                aria-label="结束日期"
                onChange={(event) => setEndDate(event.target.value)}
              />
            </label>
            <Button
              className="h-11 shrink-0 px-6"
              disabled={loadingBilling || selectedStoreId <= 0}
              onClick={queryBillingByDate}
            >
              {loadingBilling ? (
                <LoaderCircleIcon className="size-4 animate-spin" />
              ) : (
                <SearchIcon className="size-4" />
              )}
              {loadingBilling ? "查询中" : "查询"}
            </Button>
          </div>

          <div className="mt-3 flex flex-wrap items-center gap-x-5 gap-y-2 text-xs text-muted-foreground">
            <span className="inline-flex items-center gap-1.5">
              <KeyRoundIcon className="size-3.5" />
              凭据版本{" "}
              {billing?.credentialRevision ||
                selectedStore?.credentialRevision ||
                "-"}
            </span>
            <span className="inline-flex items-center gap-1.5">
              <CalendarClockIcon className="size-3.5" />
              日期 {billing?.startDate || startDate} 至{" "}
              {billing?.endDate || endDate}
            </span>
            <span className="inline-flex items-center gap-1.5">
              <GaugeIcon className="size-3.5" />
              {billing?.logs.length || 0} 条调用记录
            </span>
            <span className="inline-flex items-center gap-1.5">
              <Clock3Icon className="size-3.5" />
              查询时间 {formatDateTime(billing?.queriedAt)}
            </span>
          </div>
        </div>
      </section>

      {refreshNotice ? (
        <div className="flex items-center gap-2 rounded-lg border border-blue-200 bg-blue-50 px-4 py-3 text-sm text-blue-950 dark:border-blue-900 dark:bg-blue-950/50 dark:text-blue-200">
          <LoaderCircleIcon className="size-4 animate-spin" />
          {refreshNotice}
        </div>
      ) : null}

      {billing ? (
        <>
          <section className="overflow-hidden rounded-lg border border-border bg-card shadow-sm">
            <header className="flex flex-col gap-3 border-b border-border px-4 py-3 sm:flex-row sm:items-center sm:justify-between sm:px-5">
              <div className="flex items-center gap-2">
                <WalletCardsIcon className="size-4 text-primary" />
                <div>
                  <h2 className="font-semibold">令牌信息</h2>
                  <p className="mt-0.5 text-xs text-muted-foreground">
                    当前门店账户总览与筛选期间数据
                  </p>
                </div>
              </div>
              <Button
                type="button"
                variant="ghost"
                size="sm"
                onClick={copyBillingSummary}
              >
                <CopyIcon className="size-4" />
                复制令牌信息
              </Button>
            </header>

            <div className="grid sm:grid-cols-2 xl:grid-cols-6">
              <div className="border-b border-border p-4 sm:border-r xl:border-b-0">
                <div className="text-xs text-muted-foreground">令牌名称</div>
                <div className="mt-2 truncate font-semibold" title={billing.summary.name}>
                  {billing.summary.name || billing.storeName || "当前门店"}
                </div>
                <div className="mt-1 text-xs text-muted-foreground">
                  门店独立计费账户
                </div>
              </div>
              <div className="border-b border-border p-4 xl:border-b-0 xl:border-r">
                <div className="text-xs text-muted-foreground">令牌总额</div>
                <div className="mt-2 text-xl font-semibold tabular-nums">
                  {billing.summary.unlimitedQuota
                    ? "不限额"
                    : formatCNY(billing.summary.grantedCny)}
                </div>
                <div className="mt-1 text-xs text-muted-foreground">
                  {billing.summary.unlimitedQuota
                    ? "不设额度上限"
                    : `Quota ${formatInteger(billing.summary.totalGranted)}`}
                </div>
              </div>
              <div className="border-b border-border p-4 sm:border-r xl:border-b-0">
                <div className="text-xs text-muted-foreground">剩余额度</div>
                <div className="mt-2 text-xl font-semibold tabular-nums text-emerald-700 dark:text-emerald-300">
                  {billing.summary.unlimitedQuota
                    ? "不限额"
                    : formatCNY(billing.summary.availableCny)}
                </div>
                <div className="mt-1 text-xs text-muted-foreground">
                  {billing.summary.unlimitedQuota
                    ? "按实际调用累计"
                    : `Quota ${formatInteger(billing.summary.totalAvailable)}`}
                </div>
              </div>
              <div className="border-b border-border p-4 xl:border-b-0 xl:border-r">
                <div className="text-xs text-muted-foreground">筛选期间花费</div>
                <div className="mt-2 text-xl font-semibold tabular-nums text-primary">
                  {formatCNY(billing.periodCostCny, 6)}
                </div>
                <div className="mt-1 text-xs text-muted-foreground">
                  账户累计 {formatCNY(billing.summary.usedCny, 6)}
                </div>
              </div>
              <div className="border-b border-border p-4 sm:border-r xl:border-b-0">
                <div className="text-xs text-muted-foreground">筛选期间 Token</div>
                <div className="mt-2 text-xl font-semibold tabular-nums">
                  {formatInteger(
                    billing.periodPromptTokens + billing.periodOutputTokens,
                  )}
                </div>
                <div className="mt-1 text-xs text-muted-foreground">
                  输入 {formatInteger(billing.periodPromptTokens)} / 输出{" "}
                  {formatInteger(billing.periodOutputTokens)}
                </div>
              </div>
              <div className="p-4">
                <div className="text-xs text-muted-foreground">有效期至</div>
                <div className="mt-2 font-semibold">
                  {formatEpochSeconds(billing.summary.expiresAt)}
                </div>
                <div className="mt-1 text-xs text-muted-foreground">
                  费用单位：人民币
                </div>
              </div>
            </div>
          </section>

          <section className="overflow-hidden rounded-lg border border-border bg-card shadow-sm">
            <header className="border-b border-border px-4 py-3 sm:px-5">
              <div className="flex flex-col gap-3 xl:flex-row xl:items-center xl:justify-between">
                <div className="flex items-center gap-2">
                  <CircleDollarSignIcon className="size-4 text-primary" />
                  <div>
                    <div className="flex flex-wrap items-center gap-2">
                      <h2 className="font-semibold">调用详情</h2>
                      <Badge
                        variant="outline"
                        className="border-emerald-200 bg-emerald-50 text-emerald-700 dark:border-emerald-900 dark:bg-emerald-950/50 dark:text-emerald-300"
                      >
                        费用单位：人民币
                      </Badge>
                    </div>
                    <p className="mt-1 text-xs text-muted-foreground">
                      支持按模型或请求 ID 筛选；不展示 Prompt 与上游敏感响应。
                    </p>
                  </div>
                </div>

                <div className="flex flex-col gap-2 sm:flex-row">
                  <div className="relative min-w-0 sm:w-72">
                    <SearchIcon className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
                    <Input
                      value={searchText}
                      className="h-9 pl-9"
                      placeholder="搜索模型或请求 ID"
                      onChange={(event) => setSearchText(event.target.value)}
                    />
                  </div>
                  <OptionCombobox
                    value={String(pageSize)}
                    options={pageSizeOptions}
                    placeholder="每页条数"
                    triggerClassName="h-9 sm:w-32"
                    onChange={(value) => setPageSize(Number(value))}
                  />
                  <Button
                    variant="outline"
                    disabled={!filteredLogs.length}
                    onClick={downloadCSV}
                  >
                    <DownloadIcon className="size-4" />
                    导出 CSV
                  </Button>
                </div>
              </div>
            </header>

            <div className="hidden overflow-x-auto lg:block">
              <Table>
                <TableHeader>
                  <TableRow className="hover:bg-transparent">
                    <TableHead className="w-44">
                      <SortHeader
                        label="时间"
                        sortKey="createdAt"
                        activeSortKey={sortKey}
                        onSort={updateSort}
                      />
                    </TableHead>
                    <TableHead className="min-w-48">
                      <SortHeader
                        label="模型"
                        sortKey="modelName"
                        activeSortKey={sortKey}
                        onSort={updateSort}
                      />
                    </TableHead>
                    <TableHead className="text-right">
                      <SortHeader
                        label="提示"
                        sortKey="promptTokens"
                        activeSortKey={sortKey}
                        onSort={updateSort}
                        className="justify-end"
                      />
                    </TableHead>
                    <TableHead className="text-right">
                      <SortHeader
                        label="补全"
                        sortKey="completionTokens"
                        activeSortKey={sortKey}
                        onSort={updateSort}
                        className="justify-end"
                      />
                    </TableHead>
                    <TableHead className="text-right">
                      <SortHeader
                        label="用时"
                        sortKey="useTime"
                        activeSortKey={sortKey}
                        onSort={updateSort}
                        className="justify-end"
                      />
                    </TableHead>
                    <TableHead className="text-right">
                      <SortHeader
                        label="花费"
                        sortKey="costCny"
                        activeSortKey={sortKey}
                        onSort={updateSort}
                        className="justify-end"
                      />
                    </TableHead>
                    <TableHead className="min-w-72">请求 ID</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {pagedLogs.map((item) => {
                    const logTime = formatLogTime(item.createdAt)
                    return (
                      <TableRow key={`${item.id}-${item.requestId}`}>
                        <TableCell>
                          <div className="tabular-nums">{logTime.date}</div>
                          <div className="mt-0.5 text-xs tabular-nums text-muted-foreground">
                            {logTime.time}
                          </div>
                        </TableCell>
                        <TableCell>
                          <ModelBadge name={item.modelName} />
                        </TableCell>
                        <TableCell className="text-right tabular-nums">
                          {formatInteger(item.promptTokens)}
                        </TableCell>
                        <TableCell className="text-right tabular-nums">
                          {formatInteger(item.completionTokens)}
                        </TableCell>
                        <TableCell className="text-right">
                          <span
                            className={cn(
                              "inline-flex rounded-md border px-2 py-1 text-xs font-medium tabular-nums",
                              getUseTimeTone(item.useTime),
                            )}
                          >
                            {formatUseTime(item.useTime)}
                          </span>
                        </TableCell>
                        <TableCell className="text-right font-medium tabular-nums">
                          {formatCNY(item.costCny, 6)}
                        </TableCell>
                        <TableCell>
                          <div className="flex min-w-0 items-center gap-1">
                            <code
                              className="min-w-0 flex-1 truncate text-xs text-muted-foreground"
                              title={item.requestId}
                            >
                              {item.requestId || "-"}
                            </code>
                            {item.requestId ? (
                              <Button
                                type="button"
                                variant="ghost"
                                size="icon"
                                className="size-8 shrink-0"
                                title="复制请求 ID"
                                aria-label="复制请求 ID"
                                onClick={() =>
                                  void copyText(item.requestId, "请求 ID 已复制")
                                }
                              >
                                <CopyIcon className="size-3.5" />
                              </Button>
                            ) : null}
                          </div>
                        </TableCell>
                      </TableRow>
                    )
                  })}
                  {!pagedLogs.length ? (
                    <TableRow>
                      <TableCell
                        colSpan={7}
                        className="h-32 text-center text-muted-foreground"
                      >
                        没有找到符合条件的调用记录
                      </TableCell>
                    </TableRow>
                  ) : null}
                </TableBody>
              </Table>
            </div>

            <div className="divide-y divide-border lg:hidden">
              {pagedLogs.map((item) => {
                const logTime = formatLogTime(item.createdAt)
                return (
                  <article
                    key={`${item.id}-${item.requestId}-mobile`}
                    className="space-y-3 p-4"
                  >
                    <div className="flex items-start justify-between gap-3">
                      <ModelBadge name={item.modelName} />
                      <div className="shrink-0 text-right text-xs text-muted-foreground">
                        <div>{logTime.date}</div>
                        <div className="mt-0.5">{logTime.time}</div>
                      </div>
                    </div>
                    <div className="grid grid-cols-2 gap-3 text-sm">
                      <div>
                        <div className="text-xs text-muted-foreground">提示 / 补全</div>
                        <div className="mt-1 tabular-nums">
                          {formatInteger(item.promptTokens)} /{" "}
                          {formatInteger(item.completionTokens)}
                        </div>
                      </div>
                      <div>
                        <div className="text-xs text-muted-foreground">用时 / 花费</div>
                        <div className="mt-1 tabular-nums">
                          {formatUseTime(item.useTime)} /{" "}
                          {formatCNY(item.costCny, 6)}
                        </div>
                      </div>
                    </div>
                    <div className="flex min-w-0 items-center gap-1 rounded-md bg-muted/60 px-2 py-1.5">
                      <code
                        className="min-w-0 flex-1 truncate text-xs text-muted-foreground"
                        title={item.requestId}
                      >
                        {item.requestId || "-"}
                      </code>
                      {item.requestId ? (
                        <Button
                          type="button"
                          variant="ghost"
                          size="icon"
                          className="size-8 shrink-0"
                          title="复制请求 ID"
                          aria-label="复制请求 ID"
                          onClick={() =>
                            void copyText(item.requestId, "请求 ID 已复制")
                          }
                        >
                          <CopyIcon className="size-3.5" />
                        </Button>
                      ) : null}
                    </div>
                  </article>
                )
              })}
              {!pagedLogs.length ? (
                <div className="px-4 py-16 text-center text-sm text-muted-foreground">
                  没有找到符合条件的调用记录
                </div>
              ) : null}
            </div>

            <footer className="flex flex-col gap-3 border-t border-border px-4 py-3 sm:flex-row sm:items-center sm:justify-between sm:px-5">
              <div className="text-xs text-muted-foreground">
                {filteredLogs.length
                  ? `显示第 ${pageStart + 1}-${pageEnd} 条，共 ${filteredLogs.length} 条`
                  : "共 0 条"}
              </div>
              <div className="flex items-center justify-between gap-2 sm:justify-end">
                <Button
                  type="button"
                  variant="outline"
                  size="icon"
                  className="size-9"
                  disabled={currentPage <= 1}
                  title="上一页"
                  aria-label="上一页"
                  onClick={() => setPage((current) => Math.max(1, current - 1))}
                >
                  <ChevronLeftIcon className="size-4" />
                </Button>
                <span className="min-w-20 text-center text-sm tabular-nums">
                  {currentPage} / {totalPages}
                </span>
                <Button
                  type="button"
                  variant="outline"
                  size="icon"
                  className="size-9"
                  disabled={currentPage >= totalPages}
                  title="下一页"
                  aria-label="下一页"
                  onClick={() =>
                    setPage((current) => Math.min(totalPages, current + 1))
                  }
                >
                  <ChevronRightIcon className="size-4" />
                </Button>
              </div>
            </footer>
          </section>
        </>
      ) : (
        <section className="flex min-h-72 items-center justify-center rounded-lg border border-dashed border-border bg-card text-sm text-muted-foreground">
          {loadingBilling ? (
            <span className="flex items-center gap-2">
              <LoaderCircleIcon className="size-4 animate-spin" />
              正在查询当前门店账单
            </span>
          ) : selectedStoreId > 0 ? (
            <div className="flex max-w-md flex-col items-center gap-3 px-6 text-center">
              <Clock3Icon className="size-6 text-muted-foreground" />
              <span>当前门店尚无可展示账单，请确认模型密钥已成功生效</span>
              <Button
                variant="outline"
                disabled={loadingBilling}
                onClick={queryBillingByDate}
              >
                <RefreshCwIcon className="size-4" />
                重新查询
              </Button>
            </div>
          ) : (
            "当前账号尚未绑定可查询门店"
          )}
        </section>
      )}
    </main>
  )
}

"use client"

import {
  CheckCircle2Icon,
  ClipboardCopyIcon,
  EllipsisIcon,
  ExternalLinkIcon,
  Link2Icon,
  Loader2Icon,
  RefreshCwIcon,
  SearchIcon,
  ShieldCheckIcon,
  UnplugIcon,
} from "lucide-react"
import { useCallback, useEffect, useMemo, useState } from "react"
import { toast } from "sonner"

import { useAuth } from "@/components/auth-provider"
import { OptionCombobox } from "@/components/option-combobox"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Skeleton } from "@/components/ui/skeleton"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { Textarea } from "@/components/ui/textarea"
import { useI18n } from "@/i18n/provider"
import {
  createArrivalInvitation,
  disableArrivalConnection,
  fetchArrivalAuditLogs,
  fetchArrivalAuthorizationOptions,
  fetchArrivalConnections,
  verifyArrivalConnection,
  type ArrivalAuditLog,
  type ArrivalAuthorizationOption,
  type ArrivalConnection,
  type ArrivalInvitation,
  type ArrivalPageResult,
} from "@/lib/api/arrival"
import { cn, formatDateTime } from "@/lib/utils"

const pageSize = 20

export default function ArrivalConnectionsPage() {
  const t = useI18n()
  const { session } = useAuth()
  const permissions = useMemo(
    () => new Set(session?.permissions ?? []),
    [session?.permissions]
  )
  const canView = permissions.has("arrivalConnection.view")
  const canManage = permissions.has("arrivalConnection.manage")
  const canInvite = permissions.has("arrivalConnection.invite")
  const canViewAudit = permissions.has("arrivalAudit.view")

  const [tab, setTab] = useState("connections")
  const [query, setQuery] = useState("")
  const [submittedQuery, setSubmittedQuery] = useState("")
  const [page, setPage] = useState(1)
  const [data, setData] = useState<ArrivalPageResult<ArrivalConnection> | null>(
    null
  )
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState("")
  const [processingId, setProcessingId] = useState(0)
  const [inviteTarget, setInviteTarget] =
    useState<ArrivalConnection | null>(null)
  const [disableTarget, setDisableTarget] =
    useState<ArrivalConnection | null>(null)

  const load = useCallback(async () => {
    if (!canView) {
      setLoading(false)
      return
    }
    setLoading(true)
    setLoadError("")
    try {
      const next = await fetchArrivalConnections({
        page,
        limit: pageSize,
        keyword: submittedQuery,
      })
      setData(next)
    } catch (error) {
      const message =
        error instanceof Error
          ? error.message
          : t("arrivalConnection.loadFailed")
      setLoadError(message)
      toast.error(message)
    } finally {
      setLoading(false)
    }
  }, [canView, page, submittedQuery, t])

  useEffect(() => {
    void load()
  }, [load])

  async function verify(item: ArrivalConnection) {
    if (!canManage || !item.id || processingId) return
    setProcessingId(item.id)
    try {
      const result = await verifyArrivalConnection(item.id)
      if (result.errorCode) {
        toast.error(t("arrivalConnection.verifyFailedCode", { code: result.errorCode }))
      } else {
        toast.success(t("arrivalConnection.verifySuccess"))
      }
      await load()
    } catch (error) {
      toast.error(
        error instanceof Error
          ? error.message
          : t("arrivalConnection.verifyFailed")
      )
    } finally {
      setProcessingId(0)
    }
  }

  function submitSearch() {
    setPage(1)
    setSubmittedQuery(query.trim())
  }

  if (!canView) {
    return (
      <div className="flex h-full items-center justify-center p-6">
        <div className="max-w-md text-center">
          <ShieldCheckIcon className="mx-auto size-9 text-muted-foreground" />
          <h1 className="mt-4 text-lg font-semibold">
            {t("arrivalConnection.noPermission")}
          </h1>
        </div>
      </div>
    )
  }

  const rows = data?.results ?? []
  const activeCount = rows.filter(
    (item) => item.connectionStatus === "active"
  ).length
  const pendingCount = rows.filter((item) =>
    item.connectionStatus.startsWith("pending_")
  ).length
  const abnormalCount = rows.filter((item) =>
    ["invalid", "disabled"].includes(item.connectionStatus)
  ).length

  return (
    <div className="flex h-full min-h-0 flex-col">
      <header className="flex flex-col gap-3 border-b px-4 py-4 sm:flex-row sm:items-center sm:justify-between lg:px-6">
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            <Link2Icon className="size-5 text-primary" />
            <h1 className="text-lg font-semibold">
              {t("arrivalConnection.title")}
            </h1>
          </div>
          <p className="mt-1 text-sm text-muted-foreground">
            {t("arrivalConnection.subtitle")}
          </p>
        </div>
        <Button
          variant="outline"
          disabled={loading}
          onClick={() => void load()}
        >
          <RefreshCwIcon className={cn("size-4", loading && "animate-spin")} />
          {t("common.refresh")}
        </Button>
      </header>

      <div className="grid grid-cols-2 border-b bg-muted/20 lg:grid-cols-4">
        <Metric
          label={t("arrivalConnection.totalStores")}
          value={data?.page.total ?? 0}
        />
        <Metric
          label={t("arrivalConnection.currentPageActive")}
          value={activeCount}
          tone="success"
        />
        <Metric
          label={t("arrivalConnection.currentPagePending")}
          value={pendingCount}
          tone="warning"
        />
        <Metric
          label={t("arrivalConnection.currentPageAbnormal")}
          value={abnormalCount}
          tone="danger"
        />
      </div>

      <Tabs
        value={tab}
        onValueChange={(value) => setTab(String(value))}
        className="min-h-0 flex-1 gap-0"
      >
        <div className="flex flex-col gap-3 border-b px-4 py-3 sm:flex-row sm:items-center sm:justify-between lg:px-6">
          <TabsList variant="line">
            <TabsTrigger value="connections">
              {t("arrivalConnection.connectionsTab")}
            </TabsTrigger>
            {canViewAudit ? (
              <TabsTrigger value="audit">
                {t("arrivalConnection.auditTab")}
              </TabsTrigger>
            ) : null}
          </TabsList>
          {tab === "connections" ? (
            <form
              className="flex w-full gap-2 sm:w-auto"
              onSubmit={(event) => {
                event.preventDefault()
                submitSearch()
              }}
            >
              <div className="relative min-w-0 flex-1 sm:w-72">
                <SearchIcon className="pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-muted-foreground" />
                <Input
                  value={query}
                  className="pl-8"
                  placeholder={t("arrivalConnection.searchPlaceholder")}
                  onChange={(event) => setQuery(event.target.value)}
                />
              </div>
              <Button type="submit" variant="outline">
                {t("common.query")}
              </Button>
            </form>
          ) : null}
        </div>

        <TabsContent
          value="connections"
          className="min-h-0 overflow-auto p-4 lg:p-6"
        >
          {loading ? (
            <ConnectionTableSkeleton />
          ) : loadError ? (
            <EmptyState
              icon={UnplugIcon}
              title={t("arrivalConnection.loadFailed")}
              detail={loadError}
            />
          ) : rows.length === 0 ? (
            <EmptyState
              icon={Link2Icon}
              title={t("arrivalConnection.empty")}
            />
          ) : (
            <>
              <div className="overflow-hidden rounded-lg border bg-card">
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>{t("arrivalConnection.store")}</TableHead>
                      <TableHead>{t("arrivalConnection.status")}</TableHead>
                      <TableHead>{t("arrivalConnection.corp")}</TableHead>
                      <TableHead>{t("arrivalConnection.instance")}</TableHead>
                      <TableHead>{t("arrivalConnection.recentActivity")}</TableHead>
                      <TableHead>{t("arrivalConnection.lastVerified")}</TableHead>
                      <TableHead className="w-14 text-right">
                        {t("common.actions")}
                      </TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {rows.map((item) => (
                      <TableRow key={item.storeId}>
                        <TableCell>
                          <div className="min-w-44">
                            <div className="font-medium">{item.storeName}</div>
                            <div className="mt-1 text-xs text-muted-foreground">
                              {[item.brandName, item.storeCode]
                                .filter(Boolean)
                                .join(" · ") || "-"}
                            </div>
                          </div>
                        </TableCell>
                        <TableCell>
                          <ConnectionStatusBadge status={item.connectionStatus} />
                          {item.lastErrorCode ? (
                            <div className="mt-1 max-w-48 truncate text-xs text-destructive">
                              {item.lastErrorCode}
                            </div>
                          ) : null}
                        </TableCell>
                        <TableCell>
                          <div className="max-w-44 truncate">
                            {item.authorizedCorpName || "-"}
                          </div>
                          {item.authorizationStatus ? (
                            <div className="mt-1 text-xs text-muted-foreground">
                              {item.authorizationStatus}
                            </div>
                          ) : null}
                        </TableCell>
                        <TableCell>
                          <div className="max-w-44 truncate">
                            {item.wxWorkProtocolAccountName || "-"}
                          </div>
                          <div className="mt-1 flex items-center gap-1.5 text-xs text-muted-foreground">
                            <span
                              className={cn(
                                "size-1.5 rounded-full bg-muted-foreground",
                                item.wxWorkProtocolHealth === "online" &&
                                  "bg-emerald-500"
                              )}
                            />
                            {item.wxWorkProtocolHealth ||
                              t("arrivalConnection.notConfigured")}
                          </div>
                        </TableCell>
                        <TableCell>
                          <div className="font-medium tabular-nums">
                            {item.recentScanCount}
                          </div>
                          <div className="mt-1 text-xs text-muted-foreground">
                            {t("arrivalConnection.boundCount", {
                              count: item.recentBoundCount,
                            })}
                          </div>
                        </TableCell>
                        <TableCell className="whitespace-nowrap text-sm text-muted-foreground">
                          {formatDateTime(item.lastVerifiedAt)}
                        </TableCell>
                        <TableCell className="text-right">
                          <DropdownMenu>
                            <DropdownMenuTrigger
                              render={
                                <Button
                                  variant="ghost"
                                  size="icon-sm"
                                  aria-label={t("arrivalConnection.moreActions", {
                                    store: item.storeName,
                                  })}
                                  title={t("arrivalConnection.moreActions", {
                                    store: item.storeName,
                                  })}
                                />
                              }
                            >
                              {processingId === item.id ? (
                                <Loader2Icon className="animate-spin" />
                              ) : (
                                <EllipsisIcon />
                              )}
                            </DropdownMenuTrigger>
                            <DropdownMenuContent align="end" className="w-44">
                              {canInvite ? (
                                <DropdownMenuItem
                                  onClick={() => setInviteTarget(item)}
                                >
                                  <ExternalLinkIcon />
                                  {t("arrivalConnection.createInvitation")}
                                </DropdownMenuItem>
                              ) : null}
                              {canManage && item.id ? (
                                <DropdownMenuItem
                                  disabled={processingId > 0}
                                  onClick={() => void verify(item)}
                                >
                                  <CheckCircle2Icon />
                                  {t("arrivalConnection.verify")}
                                </DropdownMenuItem>
                              ) : null}
                              {canManage &&
                              item.id &&
                              item.connectionStatus !== "disabled" ? (
                                <>
                                  <DropdownMenuSeparator />
                                  <DropdownMenuItem
                                    variant="destructive"
                                    onClick={() => setDisableTarget(item)}
                                  >
                                    <UnplugIcon />
                                    {t("arrivalConnection.disable")}
                                  </DropdownMenuItem>
                                </>
                              ) : null}
                            </DropdownMenuContent>
                          </DropdownMenu>
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </div>
              <Pagination
                page={data?.page.page ?? page}
                total={data?.page.total ?? 0}
                limit={data?.page.limit ?? pageSize}
                onPageChange={setPage}
              />
            </>
          )}
        </TabsContent>

        {canViewAudit ? (
          <TabsContent
            value="audit"
            className="min-h-0 overflow-auto p-4 lg:p-6"
          >
            <ArrivalAuditView active={tab === "audit"} />
          </TabsContent>
        ) : null}
      </Tabs>

      <InvitationDialog
        target={inviteTarget}
        onOpenChange={(open) => !open && setInviteTarget(null)}
        onCreated={() => void load()}
      />
      <DisableDialog
        target={disableTarget}
        onOpenChange={(open) => !open && setDisableTarget(null)}
        onDisabled={() => {
          setDisableTarget(null)
          void load()
        }}
      />
    </div>
  )
}

function Metric({
  label,
  value,
  tone = "default",
}: {
  label: string
  value: number
  tone?: "default" | "success" | "warning" | "danger"
}) {
  return (
    <div className="border-r border-b px-4 py-3 last:border-r-0 lg:border-b-0 lg:px-6">
      <div className="text-xs text-muted-foreground">{label}</div>
      <div
        className={cn(
          "mt-1 text-xl font-semibold tabular-nums",
          tone === "success" && "text-emerald-700",
          tone === "warning" && "text-amber-700",
          tone === "danger" && "text-destructive"
        )}
      >
        {value}
      </div>
    </div>
  )
}

function ConnectionStatusBadge({ status }: { status: string }) {
  const t = useI18n()
  const config: Record<string, { label: string; className: string }> = {
    active: {
      label: t("arrivalConnection.statusActive"),
      className: "border-emerald-200 bg-emerald-50 text-emerald-700",
    },
    pending_authorization: {
      label: t("arrivalConnection.statusPendingAuthorization"),
      className: "border-amber-200 bg-amber-50 text-amber-800",
    },
    pending_binding: {
      label: t("arrivalConnection.statusPendingBinding"),
      className: "border-sky-200 bg-sky-50 text-sky-700",
    },
    invalid: {
      label: t("arrivalConnection.statusInvalid"),
      className: "border-red-200 bg-red-50 text-red-700",
    },
    disabled: {
      label: t("arrivalConnection.statusDisabled"),
      className: "border-border bg-muted text-muted-foreground",
    },
  }
  const current = config[status] ?? {
    label: status || "-",
    className: "border-border bg-muted text-muted-foreground",
  }
  return (
    <Badge variant="outline" className={current.className}>
      {current.label}
    </Badge>
  )
}

function InvitationDialog({
  target,
  onOpenChange,
  onCreated,
}: {
  target: ArrivalConnection | null
  onOpenChange: (open: boolean) => void
  onCreated: () => void
}) {
  const t = useI18n()
  const [authorizations, setAuthorizations] = useState<
    ArrivalAuthorizationOption[]
  >([])
  const [authorizationId, setAuthorizationId] = useState("0")
  const [result, setResult] = useState<ArrivalInvitation | null>(null)
  const [loadingOptions, setLoadingOptions] = useState(false)
  const [creating, setCreating] = useState(false)

  useEffect(() => {
    if (!target) return
    setResult(null)
    setAuthorizationId("0")
    setLoadingOptions(true)
    void fetchArrivalAuthorizationOptions()
      .then(setAuthorizations)
      .catch((error) =>
        toast.error(
          error instanceof Error
            ? error.message
            : t("arrivalConnection.loadAuthorizationsFailed")
        )
      )
      .finally(() => setLoadingOptions(false))
  }, [t, target])

  async function create() {
    if (!target || creating) return
    setCreating(true)
    try {
      const next = await createArrivalInvitation({
        storeId: target.storeId,
        tenantAuthorizationId: Number(authorizationId) || undefined,
      })
      setResult(next)
      toast.success(t("arrivalConnection.invitationCreated"))
      onCreated()
    } catch (error) {
      toast.error(
        error instanceof Error
          ? error.message
          : t("arrivalConnection.invitationCreateFailed")
      )
    } finally {
      setCreating(false)
    }
  }

  async function copyLink() {
    if (!result?.invitationUrl) return
    await navigator.clipboard.writeText(result.invitationUrl)
    toast.success(t("arrivalConnection.invitationCopied"))
  }

  return (
    <Dialog open={Boolean(target)} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{t("arrivalConnection.invitationTitle")}</DialogTitle>
          <DialogDescription>
            {target?.storeName ?? "-"}
          </DialogDescription>
        </DialogHeader>
        {result ? (
          <div className="space-y-3">
            <div className="rounded-lg border bg-muted/30 p-3">
              <div className="break-all font-mono text-xs leading-5">
                {result.invitationUrl}
              </div>
              <div className="mt-2 text-xs text-muted-foreground">
                {t("arrivalConnection.expiresAt", {
                  time: formatDateTime(result.expiresAt),
                })}
              </div>
            </div>
            <div className="flex flex-wrap gap-2">
              <Button variant="outline" onClick={() => void copyLink()}>
                <ClipboardCopyIcon />
                {t("arrivalConnection.copyInvitation")}
              </Button>
              <Button
                onClick={() =>
                  window.open(result.invitationUrl, "_blank", "noopener,noreferrer")
                }
              >
                <ExternalLinkIcon />
                {t("arrivalConnection.openInvitation")}
              </Button>
            </div>
          </div>
        ) : (
          <div className="space-y-2">
            <Label>{t("arrivalConnection.authorizationReuse")}</Label>
            <OptionCombobox
              value={authorizationId}
              disabled={loadingOptions}
              placeholder={
                loadingOptions
                  ? t("common.loading")
                  : t("arrivalConnection.newAuthorization")
              }
              options={[
                {
                  value: "0",
                  label: t("arrivalConnection.newAuthorization"),
                },
                ...authorizations.map((item) => ({
                  value: String(item.id),
                  label: item.corpName || `#${item.id}`,
                })),
              ]}
              onChange={setAuthorizationId}
            />
          </div>
        )}
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            {t("common.close")}
          </Button>
          {!result ? (
            <Button disabled={creating} onClick={() => void create()}>
              {creating ? <Loader2Icon className="animate-spin" /> : <Link2Icon />}
              {creating
                ? t("arrivalConnection.creatingInvitation")
                : t("arrivalConnection.createInvitation")}
            </Button>
          ) : null}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function DisableDialog({
  target,
  onOpenChange,
  onDisabled,
}: {
  target: ArrivalConnection | null
  onOpenChange: (open: boolean) => void
  onDisabled: () => void
}) {
  const t = useI18n()
  const [reason, setReason] = useState("")
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    if (target) setReason("")
  }, [target])

  async function disable() {
    if (!target?.id || saving) return
    setSaving(true)
    try {
      await disableArrivalConnection(target.id, reason.trim())
      toast.success(t("arrivalConnection.disabledSuccess"))
      onDisabled()
    } catch (error) {
      toast.error(
        error instanceof Error
          ? error.message
          : t("arrivalConnection.disableFailed")
      )
    } finally {
      setSaving(false)
    }
  }

  return (
    <Dialog open={Boolean(target)} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t("arrivalConnection.disableTitle")}</DialogTitle>
          <DialogDescription>{target?.storeName ?? "-"}</DialogDescription>
        </DialogHeader>
        <div className="space-y-2">
          <Label htmlFor="arrival-disable-reason">
            {t("arrivalConnection.disableReason")}
          </Label>
          <Textarea
            id="arrival-disable-reason"
            rows={3}
            value={reason}
            onChange={(event) => setReason(event.target.value)}
          />
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            {t("common.cancel")}
          </Button>
          <Button variant="destructive" disabled={saving} onClick={() => void disable()}>
            {saving ? <Loader2Icon className="animate-spin" /> : <UnplugIcon />}
            {t("arrivalConnection.confirmDisable")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function ArrivalAuditView({ active }: { active: boolean }) {
  const t = useI18n()
  const [page, setPage] = useState(1)
  const [data, setData] = useState<ArrivalPageResult<ArrivalAuditLog> | null>(
    null
  )
  const [loading, setLoading] = useState(false)
  const [loaded, setLoaded] = useState(false)

  const load = useCallback(async () => {
    if (!active) return
    setLoading(true)
    try {
      setData(await fetchArrivalAuditLogs({ page, limit: pageSize }))
      setLoaded(true)
    } catch (error) {
      toast.error(
        error instanceof Error
          ? error.message
          : t("arrivalConnection.auditLoadFailed")
      )
    } finally {
      setLoading(false)
    }
  }, [active, page, t])

  useEffect(() => {
    void load()
  }, [load])

  if (loading && !loaded) return <ConnectionTableSkeleton />
  if (!data?.results.length) {
    return (
      <EmptyState
        icon={ShieldCheckIcon}
        title={t("arrivalConnection.auditEmpty")}
      />
    )
  }
  return (
    <>
      <div className="overflow-hidden rounded-lg border bg-card">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t("arrivalConnection.auditTime")}</TableHead>
              <TableHead>{t("arrivalConnection.auditAction")}</TableHead>
              <TableHead>{t("arrivalConnection.auditStore")}</TableHead>
              <TableHead>{t("arrivalConnection.auditOperator")}</TableHead>
              <TableHead>{t("arrivalConnection.auditResult")}</TableHead>
              <TableHead>{t("arrivalConnection.auditDetail")}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {data.results.map((item) => (
              <TableRow key={item.id}>
                <TableCell className="whitespace-nowrap text-muted-foreground">
                  {formatDateTime(item.createdAt)}
                </TableCell>
                <TableCell className="font-medium">{item.action}</TableCell>
                <TableCell>#{item.storeId || "-"}</TableCell>
                <TableCell>{item.operatorName || "-"}</TableCell>
                <TableCell>
                  <Badge
                    variant="outline"
                    className={
                      item.result === "success"
                        ? "border-emerald-200 bg-emerald-50 text-emerald-700"
                        : "border-red-200 bg-red-50 text-red-700"
                    }
                  >
                    {item.result}
                  </Badge>
                </TableCell>
                <TableCell>
                  <code className="block max-w-80 truncate text-xs text-muted-foreground">
                    {item.detailJson || "-"}
                  </code>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
      <Pagination
        page={data.page.page}
        total={data.page.total}
        limit={data.page.limit}
        onPageChange={setPage}
      />
    </>
  )
}

function Pagination({
  page,
  total,
  limit,
  onPageChange,
}: {
  page: number
  total: number
  limit: number
  onPageChange: (page: number) => void
}) {
  const t = useI18n()
  const totalPages = Math.max(1, Math.ceil(total / Math.max(limit, 1)))
  return (
    <div className="mt-3 flex flex-col gap-2 text-sm text-muted-foreground sm:flex-row sm:items-center sm:justify-between">
      <span>{t("pagination.total", { total })}</span>
      <div className="flex items-center gap-2">
        <span>
          {t("pagination.pageSummary", { page, totalPages })}
        </span>
        <Button
          variant="outline"
          size="sm"
          disabled={page <= 1}
          onClick={() => onPageChange(page - 1)}
        >
          {t("pagination.previous")}
        </Button>
        <Button
          variant="outline"
          size="sm"
          disabled={page >= totalPages}
          onClick={() => onPageChange(page + 1)}
        >
          {t("pagination.next")}
        </Button>
      </div>
    </div>
  )
}

function EmptyState({
  icon: Icon,
  title,
  detail,
}: {
  icon: typeof Link2Icon
  title: string
  detail?: string
}) {
  return (
    <div className="flex min-h-72 items-center justify-center rounded-lg border border-dashed bg-muted/10 p-6 text-center">
      <div className="max-w-md">
        <Icon className="mx-auto size-9 text-muted-foreground" />
        <h2 className="mt-3 font-medium">{title}</h2>
        {detail ? (
          <p className="mt-2 break-words text-sm text-muted-foreground">
            {detail}
          </p>
        ) : null}
      </div>
    </div>
  )
}

function ConnectionTableSkeleton() {
  return (
    <div className="space-y-2 rounded-lg border p-3">
      {Array.from({ length: 7 }, (_, index) => (
        <Skeleton key={index} className="h-12 w-full" />
      ))}
    </div>
  )
}

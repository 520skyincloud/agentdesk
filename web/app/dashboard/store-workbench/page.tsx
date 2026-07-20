"use client"

import { useCallback, useEffect, useMemo, useState, type ComponentType, type ReactNode } from "react"
import {
  BotIcon,
  Building2Icon,
  LocateFixedIcon,
  MapPinIcon,
  RefreshCwIcon,
  SaveIcon,
  ShieldCheckIcon,
  StoreIcon,
  UsersRoundIcon,
  WifiIcon,
  WifiOffIcon,
} from "lucide-react"
import { toast } from "sonner"

import { StoreRoomPicker } from "./_components/store-room-picker"
import { useAuth } from "@/components/auth-provider"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Skeleton } from "@/components/ui/skeleton"
import { Switch } from "@/components/ui/switch"
import {
  fetchStoreWorkbench,
  updateStoreWorkbench,
  type StoreWorkbenchData,
  type UpdateStoreWorkbenchPayload,
} from "@/lib/api/store-workbench"
import { cn, formatDateTime } from "@/lib/utils"

const managedModes = [
  { value: "full", label: "总部托管", description: "人工请求进入总部客服工作台" },
  { value: "semi", label: "协同接待", description: "值班时通知门店群，其他时间转总部" },
  { value: "none", label: "门店接待", description: "人工请求只通知门店群" },
]

function formFromData(data: StoreWorkbenchData): UpdateStoreWorkbenchPayload {
  return {
    managedMode: data.managedMode || "semi",
    serviceHours: data.serviceHours || "",
    storeRoomConversationId: data.storeRoomConversationId || "",
    storeRoomNotifyEnabled: data.storeRoomNotifyEnabled,
    storeRoomAtList: data.storeRoomAtList || "",
    manualTimeoutMinutes: data.manualTimeoutMinutes || 10,
    storeAddress: data.storeAddress || "",
    storeNavigationName: data.storeNavigationName || "",
    storeLongitude: data.storeLongitude || "",
    storeLatitude: data.storeLatitude || "",
    storeMapProvider: data.storeMapProvider || "",
  }
}

export default function StoreWorkbenchPage() {
  const { session } = useAuth()
  const permissions = useMemo(() => new Set(session?.permissions ?? []), [session?.permissions])
  const canView = permissions.has("storeWorkbench.view")
  const canUpdate = permissions.has("storeWorkbench.update")
  const [data, setData] = useState<StoreWorkbenchData | null>(null)
  const [form, setForm] = useState<UpdateStoreWorkbenchPayload | null>(null)
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState("")
  const [saving, setSaving] = useState(false)
  const [locating, setLocating] = useState(false)

  const load = useCallback(async () => {
    if (!canView) {
      setLoading(false)
      return
    }
    setLoading(true)
    setLoadError("")
    try {
      const next = await fetchStoreWorkbench()
      setData(next)
      setForm(formFromData(next))
    } catch (error) {
      const message = error instanceof Error ? error.message : "加载门店工作台失败"
      setData(null)
      setForm(null)
      setLoadError(message)
      toast.error(message)
    } finally {
      setLoading(false)
    }
  }, [canView])

  useEffect(() => {
    void load()
  }, [load])

  function patch(next: Partial<UpdateStoreWorkbenchPayload>) {
    setForm((current) => (current ? { ...current, ...next } : current))
  }

  async function save() {
    if (!canUpdate || !data?.bound || !form || saving) return
    setSaving(true)
    try {
      const next = await updateStoreWorkbench(form)
      setData(next)
      setForm(formFromData(next))
      toast.success("门店配置已保存")
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "保存门店配置失败")
    } finally {
      setSaving(false)
    }
  }

  function locateStore() {
    if (!canUpdate || locating || !navigator.geolocation) {
      if (!navigator.geolocation) toast.error("当前浏览器不支持定位")
      return
    }
    setLocating(true)
    navigator.geolocation.getCurrentPosition(
      (position) => {
        patch({
          storeLongitude: position.coords.longitude.toFixed(6),
          storeLatitude: position.coords.latitude.toFixed(6),
          storeMapProvider: "browser_geolocation",
        })
        setLocating(false)
      },
      () => {
        toast.error("未能获取当前位置，请检查浏览器定位权限")
        setLocating(false)
      },
      { enableHighAccuracy: true, timeout: 10000 },
    )
  }

  if (!canView) {
    return (
      <div className="flex h-full items-center justify-center p-6">
        <div className="max-w-md text-center">
          <ShieldCheckIcon className="mx-auto size-9 text-muted-foreground" />
          <h1 className="mt-4 text-lg font-semibold">无门店工作台查看权限</h1>
          <p className="mt-2 text-sm text-muted-foreground">请由管理员在角色管理中分配门店工作台权限。</p>
        </div>
      </div>
    )
  }

  if (loading) return <StoreWorkbenchLoading />

  if (loadError) {
    return (
      <div className="flex h-full flex-col gap-4 p-4 lg:p-6">
        <PageHeader data={null} loading={false} onRefresh={() => void load()} />
        <div className="flex min-h-80 flex-1 items-center justify-center rounded-lg border border-destructive/30 bg-destructive/5 p-6 text-center">
          <div className="max-w-md">
            <WifiOffIcon className="mx-auto size-10 text-destructive" />
            <h2 className="mt-4 text-lg font-semibold">门店工作台加载失败</h2>
            <p className="mt-2 break-words text-sm leading-6 text-muted-foreground">{loadError}</p>
            <Button className="mt-4" variant="outline" onClick={() => void load()}>
              <RefreshCwIcon className="size-4" />
              重新加载
            </Button>
          </div>
        </div>
      </div>
    )
  }

  if (!data?.bound || !form) {
    return (
      <div className="flex h-full flex-col gap-4 p-4 lg:p-6">
        <PageHeader data={data} loading={false} onRefresh={() => void load()} />
        <div className="flex min-h-80 flex-1 items-center justify-center rounded-lg border border-dashed bg-muted/20 p-6 text-center">
          <div className="max-w-md">
            <StoreIcon className="mx-auto size-10 text-muted-foreground" />
            <h2 className="mt-4 text-lg font-semibold">当前账号尚未绑定门店</h2>
            <p className="mt-2 text-sm leading-6 text-muted-foreground">
              公司主管需要先在用户管理中给系统账号分配门店员工号角色、完成企微绑定，并安排综合客服组。
            </p>
          </div>
        </div>
      </div>
    )
  }

  const bindingDisabled = data.bindingStatus !== 0
  const editable = canUpdate && !bindingDisabled

  return (
    <div className="flex h-full flex-col gap-4 p-4 lg:p-6">
      <PageHeader data={data} loading={loading} onRefresh={() => void load()}>
        {editable ? (
          <Button disabled={saving} onClick={() => void save()}>
            <SaveIcon className="size-4" />
            {saving ? "保存中" : "保存配置"}
          </Button>
        ) : null}
      </PageHeader>

      {bindingDisabled ? (
        <div className="rounded-lg border border-amber-300 bg-amber-50 px-3 py-2 text-sm text-amber-900">
          当前门店员工绑定已停用，配置仅供查看。
        </div>
      ) : null}

      <div className="grid gap-4 xl:grid-cols-[minmax(0,1.55fr)_minmax(320px,0.75fr)]">
        <div className="grid content-start gap-4">
          <section className="rounded-lg border bg-card p-4">
            <SectionTitle icon={UsersRoundIcon} title="人工接待方式" />
            <div className="mt-4 grid grid-cols-1 gap-2 sm:grid-cols-3">
              {managedModes.map((mode) => (
                <button
                  key={mode.value}
                  type="button"
                  aria-pressed={form.managedMode === mode.value}
                  disabled={!editable}
                  className={cn(
                    "min-h-20 rounded-lg border px-3 py-3 text-left transition-colors",
                    form.managedMode === mode.value
                      ? "border-primary bg-primary/8 text-foreground"
                      : "bg-background hover:bg-muted/40",
                    !editable && "cursor-not-allowed opacity-70",
                  )}
                  onClick={() => patch({ managedMode: mode.value })}
                >
                  <span className="block text-sm font-semibold">{mode.label}</span>
                  <span className="mt-1 block text-xs leading-5 text-muted-foreground">{mode.description}</span>
                </button>
              ))}
            </div>
            <div className="mt-4 grid gap-4 sm:grid-cols-2">
              <Field label="门店服务时间">
                <Input
                  disabled={!editable}
                  value={form.serviceHours}
                  placeholder="09:00-12:00,13:30-22:00"
                  onChange={(event) => patch({ serviceHours: event.target.value })}
                />
              </Field>
              <Field label="人工跟进超时（分钟）">
                <Input
                  type="number"
                  min={1}
                  max={120}
                  disabled={!editable}
                  value={form.manualTimeoutMinutes}
                  onChange={(event) => patch({ manualTimeoutMinutes: Number(event.target.value || 1) })}
                />
              </Field>
            </div>
          </section>

          <section className="rounded-lg border bg-card p-4">
            <div className="flex items-center justify-between gap-3">
              <SectionTitle icon={UsersRoundIcon} title="门店通知群" />
              <div className="flex items-center gap-2">
                <Label htmlFor="store-room-notify" className="text-sm text-muted-foreground">启用通知</Label>
                <Switch
                  id="store-room-notify"
                  disabled={!editable}
                  checked={form.storeRoomNotifyEnabled}
                  onCheckedChange={(checked) => patch({ storeRoomNotifyEnabled: checked })}
                />
              </div>
            </div>
            <div className="mt-4">
              <StoreRoomPicker
                instanceAvailable={data.wxWorkInstanceId > 0}
                roomConversationId={form.storeRoomConversationId}
                atList={form.storeRoomAtList}
                disabled={!editable}
                onRoomChange={(value) => patch({ storeRoomConversationId: value })}
                onAtListChange={(value) => patch({ storeRoomAtList: value })}
              />
            </div>
          </section>

          <section className="rounded-lg border bg-card p-4">
            <div className="flex items-center justify-between gap-3">
              <SectionTitle icon={MapPinIcon} title="门店位置" />
              {editable ? (
                <Button type="button" variant="outline" size="sm" disabled={locating} onClick={locateStore}>
                  <LocateFixedIcon className={locating ? "size-4 animate-pulse" : "size-4"} />
                  {locating ? "定位中" : "获取当前位置"}
                </Button>
              ) : null}
            </div>
            <div className="mt-4 grid gap-4 sm:grid-cols-2">
              <Field label="门店地址" className="sm:col-span-2">
                <Input disabled={!editable || data.wxWorkInstanceId <= 0} value={form.storeAddress} onChange={(event) => patch({ storeAddress: event.target.value })} />
              </Field>
              <Field label="导航名称" className="sm:col-span-2">
                <Input disabled={!editable || data.wxWorkInstanceId <= 0} value={form.storeNavigationName} onChange={(event) => patch({ storeNavigationName: event.target.value })} />
              </Field>
              <Field label="经度">
                <Input disabled={!editable || data.wxWorkInstanceId <= 0} value={form.storeLongitude} onChange={(event) => patch({ storeLongitude: event.target.value })} />
              </Field>
              <Field label="纬度">
                <Input disabled={!editable || data.wxWorkInstanceId <= 0} value={form.storeLatitude} onChange={(event) => patch({ storeLatitude: event.target.value })} />
              </Field>
            </div>
          </section>
        </div>

        <aside className="grid content-start gap-4">
          <section className="rounded-lg border bg-card p-4">
            <SectionTitle icon={Building2Icon} title="当前归属" />
            <div className="mt-4 divide-y">
              <InfoRow label="接入公司" value={data.tenantName || `#${data.tenantId}`} />
              <InfoRow label="接入公司" value={data.tenantName || "暂未关联"} />
              <InfoRow label="门店" value={data.storeName || `#${data.storeId}`} />
              <InfoRow label="门店编码" value={data.storeCode || "-"} />
              <InfoRow label="综合客服组" value={data.agentTeamName || "暂未分配"} />
              <InfoRow label="系统账号" value={data.nickname || data.username} />
            </div>
          </section>

          <section className="rounded-lg border bg-card p-4">
            <SectionTitle icon={WifiIcon} title="企微与接待策略" />
            <div className="mt-4 divide-y">
              <InfoRow
                label="企微员工号"
                value={data.wxWorkEmployeeName || data.wxWorkEmployeeId || "尚未绑定"}
              />
              <InfoRow
                label="连接状态"
                value={healthStatusLabel(data.wxWorkHealthStatus)}
                icon={data.wxWorkHealthStatus === "online" ? WifiIcon : WifiOffIcon}
                tone={data.wxWorkHealthStatus === "online" ? "success" : "muted"}
              />
              <InfoRow label="AI 托管" value={data.aiReplyEnabled ? "已启用" : "未启用"} icon={BotIcon} tone={data.aiReplyEnabled ? "success" : "muted"} />
              <InfoRow label="知识库" value={data.knowledgeBaseName || "暂未绑定"} />
              <InfoRow label="最近心跳" value={data.wxWorkLastHeartbeatAt ? formatDateTime(data.wxWorkLastHeartbeatAt) : "-"} />
              <InfoRow label="配置更新" value={data.updatedAt ? formatDateTime(data.updatedAt) : "-"} />
            </div>
          </section>
        </aside>
      </div>
    </div>
  )
}

function PageHeader({
  data,
  loading,
  onRefresh,
  children,
}: {
  data: StoreWorkbenchData | null
  loading: boolean
  onRefresh: () => void
  children?: ReactNode
}) {
  return (
    <header className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
      <div className="min-w-0">
        <div className="flex flex-wrap items-center gap-2">
          <h1 className="text-xl font-semibold">门店工作台</h1>
          {data?.bound ? (
            <Badge variant="outline" className="rounded-md">{data.storeName || "已绑定门店"}</Badge>
          ) : null}
        </div>
        <p className="mt-1 text-sm text-muted-foreground">门店接待配置与企微运行状态</p>
      </div>
      <div className="flex items-center gap-2">
        <Button type="button" variant="outline" size="icon" title="刷新" disabled={loading} onClick={onRefresh}>
          <RefreshCwIcon className={loading ? "size-4 animate-spin" : "size-4"} />
        </Button>
        {children}
      </div>
    </header>
  )
}

function SectionTitle({ icon: Icon, title }: { icon: ComponentType<{ className?: string }>; title: string }) {
  return (
    <div className="flex items-center gap-2">
      <Icon className="size-4 text-primary" />
      <h2 className="text-sm font-semibold">{title}</h2>
    </div>
  )
}

function Field({ label, className, children }: { label: string; className?: string; children: ReactNode }) {
  return (
    <div className={cn("grid gap-1.5", className)}>
      <Label className="text-xs text-muted-foreground">{label}</Label>
      {children}
    </div>
  )
}

function InfoRow({
  label,
  value,
  icon: Icon,
  tone = "default",
}: {
  label: string
  value: string
  icon?: ComponentType<{ className?: string }>
  tone?: "default" | "success" | "muted"
}) {
  return (
    <div className="grid min-h-11 grid-cols-[96px_minmax(0,1fr)] items-center gap-3 py-2 text-sm">
      <span className="text-muted-foreground">{label}</span>
      <span className={cn("flex min-w-0 items-center justify-end gap-1.5 text-right font-medium", tone === "success" && "text-emerald-700", tone === "muted" && "text-muted-foreground")}>
        {Icon ? <Icon className="size-3.5 shrink-0" /> : null}
        <span className="break-words">{value}</span>
      </span>
    </div>
  )
}

function healthStatusLabel(status: string) {
  if (status === "online") return "在线"
  if (status === "offline") return "离线"
  if (status === "pending_binding") return "待绑定"
  if (status === "login_qrcode") return "等待扫码"
  return status || "未知"
}

function StoreWorkbenchLoading() {
  return (
    <div className="grid gap-4 p-4 lg:p-6">
      <div className="flex items-center justify-between">
        <Skeleton className="h-8 w-44" />
        <Skeleton className="h-9 w-28" />
      </div>
      <div className="grid gap-4 xl:grid-cols-[minmax(0,1.55fr)_minmax(320px,0.75fr)]">
        <div className="grid gap-4">
          <Skeleton className="h-52 w-full" />
          <Skeleton className="h-72 w-full" />
          <Skeleton className="h-52 w-full" />
        </div>
        <div className="grid content-start gap-4">
          <Skeleton className="h-72 w-full" />
          <Skeleton className="h-64 w-full" />
        </div>
      </div>
    </div>
  )
}

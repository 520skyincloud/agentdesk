"use client"

import { useEffect, useMemo, useState } from "react"
import { RefreshCwIcon, SaveIcon, ServerCogIcon } from "lucide-react"
import { toast } from "sonner"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import {
  fetchWxWorkProtocolDevicePool,
  fetchWxWorkProtocolDevicePoolSettings,
  syncWxWorkProtocolDevicePool,
  updateWxWorkProtocolDevicePoolSettings,
  type WxWorkProtocolDevicePoolInstance,
  type WxWorkProtocolDevicePoolSettings,
} from "@/lib/api/admin"
import { formatDateTime } from "@/lib/utils"

const defaultSettings: WxWorkProtocolDevicePoolSettings = {
  adminBaseUrl: "https://chat-api.juhebot.com",
  username: "",
  passwordSet: false,
  tokenSet: false,
  tokenExpireAt: null,
}

export default function WxWorkDevicePoolPage() {
  const [settings, setSettings] = useState(defaultSettings)
  const [password, setPassword] = useState("")
  const [items, setItems] = useState<WxWorkProtocolDevicePoolInstance[]>([])
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [syncing, setSyncing] = useState(false)

  const stats = useMemo(() => {
    const idle = items.filter((item) => item.syncStatus === "idle" && item.boundWxWorkProtocolInstanceId <= 0).length
    const bound = items.filter((item) => item.boundWxWorkProtocolInstanceId > 0).length
    const online = items.filter((item) => item.uin || item.syncStatus === "online").length
    return { total: items.length, idle, bound, online }
  }, [items])

  async function load() {
    setLoading(true)
    try {
      const [nextSettings, page] = await Promise.all([
        fetchWxWorkProtocolDevicePoolSettings(),
        fetchWxWorkProtocolDevicePool({ page: 1, limit: 200 }),
      ])
      setSettings({ ...defaultSettings, ...nextSettings })
      setItems(page.results ?? [])
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "加载实例池失败")
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    void load()
  }, [])

  async function handleSave() {
    setSaving(true)
    try {
      const saved = await updateWxWorkProtocolDevicePoolSettings({
        adminBaseUrl: settings.adminBaseUrl,
        username: settings.username,
        password,
      })
      setSettings({ ...defaultSettings, ...saved })
      setPassword("")
      toast.success("实例池配置已保存")
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "保存失败")
    } finally {
      setSaving(false)
    }
  }

  async function handleSync() {
    setSyncing(true)
    try {
      const result = await syncWxWorkProtocolDevicePool()
      toast.success(`同步完成：${result.syncedCount} 个实例，${result.idleCount} 个空闲，${result.boundCount} 个已绑定`)
      await load()
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "同步失败")
    } finally {
      setSyncing(false)
    }
  }

  return (
    <div className="flex h-full flex-col gap-4 p-4 lg:p-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <div className="flex items-center gap-2 text-sm text-muted-foreground">
            <ServerCogIcon className="size-4" />
            系统管理
          </div>
          <h1 className="mt-1 text-xl font-semibold tracking-tight">企微员工号实例池</h1>
          <p className="mt-1 text-sm text-muted-foreground">
            从聚合智能后台同步真实 XBot 实例。新增员工号扫码时，系统会自动认领未登录、未绑定、未过期的 GUID。
          </p>
        </div>
        <div className="flex gap-2">
          <Button variant="outline" onClick={() => void load()} disabled={loading || syncing}>
            <RefreshCwIcon className="size-4" />
            刷新
          </Button>
          <Button onClick={() => void handleSync()} disabled={loading || syncing || saving}>
            <RefreshCwIcon className={syncing ? "size-4 animate-spin" : "size-4"} />
            {syncing ? "同步中" : "同步实例"}
          </Button>
        </div>
      </div>

      <section className="agentdesk-surface rounded-2xl p-4">
        <div className="grid gap-4 lg:grid-cols-[1.3fr_0.7fr]">
          <div>
            <h2 className="text-base font-medium">聚合智能后台账号</h2>
            <p className="mt-1 text-sm text-muted-foreground">
              凭据只保存在运行时数据库，读取配置时不会返回密码明文。这里登录的是后台实例管理接口，不是员工号消息协议接口。
            </p>
            <div className="mt-4 grid gap-4 md:grid-cols-3">
              <Field label="后台 API 地址">
                <Input value={settings.adminBaseUrl} onChange={(event) => setSettings((current) => ({ ...current, adminBaseUrl: event.target.value }))} />
              </Field>
              <Field label="账号">
                <Input value={settings.username} onChange={(event) => setSettings((current) => ({ ...current, username: event.target.value }))} placeholder="手机号 / 用户名" />
              </Field>
              <Field label={settings.passwordSet ? "密码（已设置）" : "密码"}>
                <Input type="password" value={password} onChange={(event) => setPassword(event.target.value)} placeholder={settings.passwordSet ? "留空表示不修改" : "请输入密码"} />
              </Field>
            </div>
            <div className="mt-4 flex flex-wrap items-center gap-2">
              <Button onClick={() => void handleSave()} disabled={saving || syncing}>
                <SaveIcon className="size-4" />
                {saving ? "保存中" : "保存配置"}
              </Button>
              <Badge variant={settings.tokenSet ? "default" : "secondary"}>{settings.tokenSet ? "Token 已缓存" : "未登录"}</Badge>
              <span className="text-xs text-muted-foreground">Token 过期：{formatDateTime(settings.tokenExpireAt)}</span>
            </div>
          </div>
          <div className="grid grid-cols-2 gap-3 lg:grid-cols-2">
            <Stat label="总实例" value={stats.total} />
            <Stat label="空闲可扫码" value={stats.idle} tone="green" />
            <Stat label="已登录" value={stats.online} tone="blue" />
            <Stat label="本地已绑定" value={stats.bound} tone="amber" />
          </div>
        </div>
      </section>

      <section className="agentdesk-surface min-h-0 flex-1 overflow-hidden rounded-2xl p-4">
        <div className="mb-3 flex items-center justify-between">
          <div>
            <h2 className="text-base font-medium">实例列表</h2>
            <p className="text-sm text-muted-foreground">`uin` 为空且未绑定的实例才会被新增扫码流程自动认领。</p>
          </div>
          {loading ? <span className="text-sm text-muted-foreground">加载中...</span> : null}
        </div>
        <div className="overflow-auto rounded-xl border border-[#dbe7f6] bg-white">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>GUID</TableHead>
                <TableHead>状态</TableHead>
                <TableHead>本地绑定</TableHead>
                <TableHead>企微登录</TableHead>
                <TableHead>套餐 / 版本</TableHead>
                <TableHead>Bridge</TableHead>
                <TableHead>到期时间</TableHead>
                <TableHead>最近同步</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {items.length === 0 ? (
                <TableRow>
                  <TableCell colSpan={8} className="h-28 text-center text-muted-foreground">
                    暂无实例。保存账号后点击“同步实例”。
                  </TableCell>
                </TableRow>
              ) : (
                items.map((item) => (
                  <TableRow key={item.id}>
                    <TableCell className="min-w-[260px] font-mono text-xs">{item.guid}</TableCell>
                    <TableCell><PoolStatusBadge item={item} /></TableCell>
                    <TableCell className="min-w-[160px]">
                      {item.boundWxWorkProtocolInstanceId > 0 ? (
                        <div className="space-y-1">
                          <div className="font-medium">{item.boundEmployeeName || `员工号 #${item.boundWxWorkProtocolInstanceId}`}</div>
                          <div className="text-xs text-muted-foreground">{item.boundStoreName || "未绑定门店"}</div>
                        </div>
                      ) : (
                        <span className="text-muted-foreground">未绑定</span>
                      )}
                    </TableCell>
                    <TableCell>
                      {item.uin ? <span className="font-mono text-xs">{item.uin}</span> : <span className="text-muted-foreground">未登录</span>}
                    </TableCell>
                    <TableCell className="min-w-[120px]">{item.seatName || "-"}</TableCell>
                    <TableCell className="max-w-[180px] truncate font-mono text-xs">{item.bridgeId || "-"}</TableCell>
                    <TableCell>{formatDateTime(item.expiredAt)}</TableCell>
                    <TableCell>{formatDateTime(item.lastSyncedAt)}</TableCell>
                  </TableRow>
                ))
              )}
            </TableBody>
          </Table>
        </div>
      </section>
    </div>
  )
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="grid gap-1.5">
      <Label className="text-xs text-muted-foreground">{label}</Label>
      {children}
    </div>
  )
}

function Stat({ label, value, tone = "gray" }: { label: string; value: number; tone?: "gray" | "green" | "blue" | "amber" }) {
  const color = {
    gray: "bg-slate-50 text-slate-700 border-slate-200",
    green: "bg-emerald-50 text-emerald-700 border-emerald-200",
    blue: "bg-sky-50 text-sky-700 border-sky-200",
    amber: "bg-amber-50 text-amber-700 border-amber-200",
  }[tone]
  return (
    <div className={`rounded-2xl border p-4 ${color}`}>
      <div className="text-2xl font-semibold">{value}</div>
      <div className="mt-1 text-xs opacity-75">{label}</div>
    </div>
  )
}

function PoolStatusBadge({ item }: { item: WxWorkProtocolDevicePoolInstance }) {
  if (item.available) {
    return <Badge className="bg-emerald-600 text-white">空闲可用</Badge>
  }
  if (item.boundWxWorkProtocolInstanceId > 0) {
    return <Badge className="bg-amber-600 text-white">已绑定</Badge>
  }
  if (item.syncStatus === "expired") {
    return <Badge variant="destructive">已过期</Badge>
  }
  if (item.uin || item.syncStatus === "online") {
    return <Badge variant="secondary">已登录</Badge>
  }
  if (item.syncStatus === "unavailable") {
    return <Badge variant="destructive">不可用</Badge>
  }
  return <Badge variant="outline">{item.syncStatus || "未知"}</Badge>
}

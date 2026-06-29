"use client"

import { Suspense, useEffect, useMemo, useState } from "react"
import { CheckCircle2Icon, CopyIcon, LocateFixedIcon, QrCodeIcon, RefreshCwIcon } from "lucide-react"
import { useSearchParams } from "next/navigation"
import { toast } from "sonner"

import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Switch } from "@/components/ui/switch"
import { Textarea } from "@/components/ui/textarea"
import {
  checkWxWorkProtocolRemoteSetupLogin,
  fetchWxWorkProtocolRemoteSetup,
  getWxWorkProtocolRemoteSetupLoginQrcode,
  updateWxWorkProtocolRemoteSetup,
  type WxWorkProtocolInstance,
  type WxWorkProtocolRemoteLoginQRCodeResult,
} from "@/lib/api/admin"
import { repairMojibakeText } from "@/lib/utils"

type FormState = {
  employeeName: string
  storeName: string
  storeAddress: string
  storeNavigationName: string
  storeLongitude: string
  storeLatitude: string
  storeMapProvider: string
  serviceHours: string
  storeRoomConversationId: string
  storeRoomNotifyEnabled: boolean
  storeRoomAtList: string
  fallbackToHQ: boolean
  manualTimeoutMinutes: number
  autoAcceptFriendRequest: boolean
}

const defaultForm: FormState = {
  employeeName: "",
  storeName: "",
  storeAddress: "",
  storeNavigationName: "",
  storeLongitude: "",
  storeLatitude: "",
  storeMapProvider: "",
  serviceHours: "09:00-22:00",
  storeRoomConversationId: "",
  storeRoomNotifyEnabled: false,
  storeRoomAtList: "",
  fallbackToHQ: true,
  manualTimeoutMinutes: 10,
  autoAcceptFriendRequest: true,
}

export default function WxWorkRemoteSetupPage() {
  return (
    <Suspense fallback={<div className="flex min-h-screen items-center justify-center bg-[#f6f8fb] text-sm text-muted-foreground">加载远程开户链接...</div>}>
      <WxWorkRemoteSetupContent />
    </Suspense>
  )
}

function WxWorkRemoteSetupContent() {
  const searchParams = useSearchParams()
  const [token, setToken] = useState("")
  const [instance, setInstance] = useState<WxWorkProtocolInstance | null>(null)
  const [form, setForm] = useState<FormState>(defaultForm)
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [qrcode, setQrcode] = useState<WxWorkProtocolRemoteLoginQRCodeResult | null>(null)
  const [checking, setChecking] = useState(false)

  useEffect(() => {
    setToken(searchParams.get("token") || "")
  }, [searchParams])

  useEffect(() => {
    if (!token) return
    void loadRemoteSetup(token)
  }, [token])

  async function loadRemoteSetup(nextToken: string) {
    setLoading(true)
    try {
      const data = await fetchWxWorkProtocolRemoteSetup(nextToken)
      setInstance(data)
      setForm({
        employeeName: repairMojibakeText(data.employeeName || ""),
        storeName: repairMojibakeText(data.storeName || ""),
        storeAddress: repairMojibakeText(data.storeAddress || ""),
        storeNavigationName: repairMojibakeText(data.storeNavigationName || data.storeName || ""),
        storeLongitude: data.storeLongitude || "",
        storeLatitude: data.storeLatitude || "",
        storeMapProvider: data.storeMapProvider || "",
        serviceHours: data.serviceHours || defaultForm.serviceHours,
        storeRoomConversationId: data.storeRoomConversationId || "",
        storeRoomNotifyEnabled: data.storeRoomNotifyEnabled,
        storeRoomAtList: data.storeRoomAtList || "",
        fallbackToHQ: data.fallbackToHQ !== false,
        manualTimeoutMinutes: data.manualTimeoutMinutes || 10,
        autoAcceptFriendRequest: data.autoAcceptFriendRequest !== false,
      })
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "加载远程配置失败")
    } finally {
      setLoading(false)
    }
  }

  const qrcodeImage = useMemo(() => {
    const value = qrcode?.qrcode || qrcode?.qrcodeContent || ""
    if (!value) return ""
    if (value.startsWith("data:image")) return value
    if (value.startsWith("http://") || value.startsWith("https://")) return value
    return value
  }, [qrcode])

  function setValue<K extends keyof FormState>(key: K, value: FormState[K]) {
    setForm((current) => ({ ...current, [key]: value }))
  }

  async function getLoginQRCode() {
    if (!token) return
    try {
      const data = await getWxWorkProtocolRemoteSetupLoginQrcode(token)
      setQrcode(data)
      toast.success("已获取登录二维码")
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "获取二维码失败")
    }
  }

  async function checkLogin() {
    if (!token) return
    setChecking(true)
    try {
      const raw = await checkWxWorkProtocolRemoteSetupLogin(token)
      await navigator.clipboard.writeText(raw)
      toast.success("已检查扫码状态，协议原文已复制")
      await loadRemoteSetup(token)
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "检查扫码状态失败")
    } finally {
      setChecking(false)
    }
  }

  function getCurrentLocation() {
    if (!navigator.geolocation) {
      toast.error("当前浏览器不支持定位")
      return
    }
    navigator.geolocation.getCurrentPosition(
      (position) => {
        setForm((current) => ({
          ...current,
          storeLatitude: String(position.coords.latitude),
          storeLongitude: String(position.coords.longitude),
          storeMapProvider: "browser_geolocation",
        }))
        toast.success("已填入当前坐标，请确认是在门店现场获取")
      },
      (error) => toast.error(error.message || "获取坐标失败"),
      { enableHighAccuracy: true, timeout: 12000, maximumAge: 30000 },
    )
  }

  async function save() {
    if (!token) return
    setSaving(true)
    try {
      await updateWxWorkProtocolRemoteSetup({ token, ...form })
      toast.success("已提交门店配置")
      await loadRemoteSetup(token)
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "保存失败")
    } finally {
      setSaving(false)
    }
  }

  if (loading) {
    return <div className="flex min-h-screen items-center justify-center bg-[#f6f8fb] text-sm text-muted-foreground">加载远程开户链接...</div>
  }

  return (
    <main className="min-h-screen bg-[#f6f8fb] px-4 py-8 text-foreground">
      <div className="mx-auto max-w-5xl space-y-5">
        <section className="rounded-3xl border border-[#dbe7f6] bg-white p-6 shadow-[0_20px_60px_rgba(35,74,122,0.08)]">
          <div className="flex flex-col gap-4 md:flex-row md:items-center md:justify-between">
            <div>
              <div className="text-sm font-medium text-muted-foreground">知悉微宝</div>
              <h1 className="mt-1 text-2xl font-semibold tracking-normal">企微员工号远程开户</h1>
              <p className="mt-2 max-w-2xl text-sm leading-6 text-muted-foreground">请用门店要接待客户的企业微信员工号扫码登录，并补充门店位置、服务时间和转人工通知群。链接生成时已自动绑定协议平台空闲实例，这里只配置当前账号。</p>
            </div>
            <div className="rounded-2xl bg-[#f4f7fb] px-4 py-3 text-sm text-muted-foreground">
              实例：<span className="font-mono text-foreground">{instance?.guid || "-"}</span>
            </div>
          </div>
        </section>

        <div className="grid gap-5 lg:grid-cols-[360px_1fr]">
          <section className="rounded-3xl border border-[#dbe7f6] bg-white p-5 shadow-[0_16px_42px_rgba(35,74,122,0.06)]">
            <div className="flex items-center justify-between gap-3">
              <div>
                <h2 className="font-semibold">1. 扫码登录员工号</h2>
                <p className="mt-1 text-sm text-muted-foreground">二维码来自协议平台真实登录接口。</p>
              </div>
              <QrCodeIcon className="size-5 text-muted-foreground" />
            </div>
            <div className="mt-4 flex min-h-64 items-center justify-center rounded-2xl border border-dashed border-[#cbd8ea] bg-[#f8fafc] p-4">
              {qrcodeImage ? (
                qrcodeImage.startsWith("http") || qrcodeImage.startsWith("data:image") ? (
                  // eslint-disable-next-line @next/next/no-img-element
                  <img src={qrcodeImage} alt="登录二维码" className="max-h-56 max-w-full rounded-xl bg-white p-2" />
                ) : (
                  <div className="break-all text-xs leading-5 text-muted-foreground">{qrcodeImage}</div>
                )
              ) : (
                <div className="text-center text-sm text-muted-foreground">点击下方按钮获取登录二维码</div>
              )}
            </div>
            <div className="mt-4 grid gap-2">
              <Button type="button" className="rounded-xl" onClick={() => void getLoginQRCode()}>
                <QrCodeIcon className="size-4" />
                获取登录二维码
              </Button>
              <Button type="button" variant="outline" className="rounded-xl" onClick={() => void checkLogin()} disabled={checking}>
                <RefreshCwIcon className={checking ? "size-4 animate-spin" : "size-4"} />
                检查扫码状态
              </Button>
              {instance?.employeeUserId ? (
                <div className="flex items-center gap-2 rounded-xl bg-emerald-50 px-3 py-2 text-sm text-emerald-700">
                  <CheckCircle2Icon className="size-4" /> 已同步：{repairMojibakeText(instance.employeeName) || instance.employeeUserId}
                </div>
              ) : null}
            </div>
          </section>

          <section className="rounded-3xl border border-[#dbe7f6] bg-white p-5 shadow-[0_16px_42px_rgba(35,74,122,0.06)]">
            <h2 className="font-semibold">2. 填写门店资料</h2>
            <div className="mt-4 grid gap-4 md:grid-cols-2">
              <Field label="员工号显示名"><Input value={form.employeeName} onChange={(event) => setValue("employeeName", event.target.value)} placeholder="例如：吴朝伟" /></Field>
              <Field label="门店名称"><Input value={form.storeName} onChange={(event) => setValue("storeName", event.target.value)} placeholder="例如：丽斯未来酒店杭州某某店" /></Field>
              <Field label="门店地址"><Input value={form.storeAddress} onChange={(event) => setValue("storeAddress", event.target.value)} placeholder="填写可导航地址" /></Field>
              <Field label="定位卡片标题"><Input value={form.storeNavigationName} onChange={(event) => setValue("storeNavigationName", event.target.value)} placeholder="默认可用门店名称" /></Field>
              <Field label="纬度"><Input value={form.storeLatitude} onChange={(event) => setValue("storeLatitude", event.target.value)} placeholder="例如：30.27415" /></Field>
              <Field label="经度"><Input value={form.storeLongitude} onChange={(event) => setValue("storeLongitude", event.target.value)} placeholder="例如：120.15515" /></Field>
              <div className="md:col-span-2">
                <Button type="button" variant="outline" className="rounded-xl" onClick={getCurrentLocation}>
                  <LocateFixedIcon className="size-4" />
                  一键获取当前坐标
                </Button>
                <p className="mt-2 text-xs leading-5 text-muted-foreground">请门店员工在门店现场点击，浏览器授权定位后会自动填入坐标。坐标用于以后客户要定位时发送真实微信定位卡片。</p>
              </div>
              <Field label="客服服务时间"><Input value={form.serviceHours} onChange={(event) => setValue("serviceHours", event.target.value)} placeholder="例如：09:00-22:00" /></Field>
              <Field label="人工超时分钟"><Input type="number" value={form.manualTimeoutMinutes} onChange={(event) => setValue("manualTimeoutMinutes", Number(event.target.value || 10))} /></Field>
              <Field label="门店群 conversation_id"><Input value={form.storeRoomConversationId} onChange={(event) => setValue("storeRoomConversationId", event.target.value)} placeholder="R: 开头，门店群发一条消息后可在回调/会话里复制" /></Field>
              <Field label="门店群 @ 成员"><Input value={form.storeRoomAtList} onChange={(event) => setValue("storeRoomAtList", event.target.value)} placeholder="多个用英文逗号，0 表示 @ 全员" /></Field>
              <SwitchRow label="值班时间转人工提醒门店群" checked={form.storeRoomNotifyEnabled} onCheckedChange={(value) => setValue("storeRoomNotifyEnabled", value)} />
              <SwitchRow label="非值班或无群时进总部网页端" checked={form.fallbackToHQ} onCheckedChange={(value) => setValue("fallbackToHQ", value)} />
              <SwitchRow label="自动通过好友申请" checked={form.autoAcceptFriendRequest} onCheckedChange={(value) => setValue("autoAcceptFriendRequest", value)} />
              <div className="md:col-span-2">
                <label className="text-sm font-medium">备注</label>
                <Textarea className="mt-2 min-h-20 rounded-xl" value={`门店群 ID 获取方式：把该员工号拉进门店群，让群里任意人发一条消息，总部后台即可从企微回调/会话原文看到 R: 开头的 conversation_id。当前协议没有“列出全部群并一键选择”的已确认接口，所以这里不做假按钮。`} readOnly />
              </div>
            </div>
            <div className="mt-5 flex flex-col gap-3 sm:flex-row sm:justify-end">
              <Button type="button" variant="outline" className="rounded-xl" onClick={() => navigator.clipboard.writeText(window.location.href).then(() => toast.success("链接已复制"))}>
                <CopyIcon className="size-4" /> 复制本页链接
              </Button>
              <Button type="button" className="rounded-xl" onClick={() => void save()} disabled={saving}>
                {saving ? "保存中..." : "保存门店配置"}
              </Button>
            </div>
          </section>
        </div>
      </div>
    </main>
  )
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label className="block text-sm font-medium text-foreground">
      {label}
      <div className="mt-2">{children}</div>
    </label>
  )
}

function SwitchRow({ label, checked, onCheckedChange }: { label: string; checked: boolean; onCheckedChange: (value: boolean) => void }) {
  return (
    <div className="flex items-center justify-between rounded-2xl border border-[#dbe7f6] bg-[#f8fafc] px-4 py-3">
      <span className="text-sm font-medium">{label}</span>
      <Switch checked={checked} onCheckedChange={onCheckedChange} />
    </div>
  )
}

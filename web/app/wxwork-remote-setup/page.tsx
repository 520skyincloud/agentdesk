"use client"

import { Suspense, useEffect, useMemo, useState } from "react"
import { CheckCircle2Icon, CopyIcon, LocateFixedIcon, QrCodeIcon, RefreshCwIcon } from "lucide-react"
import { useSearchParams } from "next/navigation"
import { toast } from "sonner"

import { WxWorkProtocolLoginProxyField } from "@/components/wxwork-protocol/wxwork-protocol-login-proxy-field"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Switch } from "@/components/ui/switch"
import { Textarea } from "@/components/ui/textarea"
import {
  checkWxWorkProtocolRemoteSetupLogin,
  fetchWxWorkProtocolRemoteSetup,
  getWxWorkProtocolRemoteSetupLoginQrcode,
  sendWxWorkProtocolRemoteSetupEmailCode,
  updateWxWorkProtocolRemoteSetup,
  verifyWxWorkProtocolRemoteSetupEmail,
  verifyWxWorkProtocolRemoteSetupLogin,
  type WxWorkProtocolInstance,
  type WxWorkProtocolLoginStatus,
  type WxWorkProtocolRemoteLoginQRCodeResult,
} from "@/lib/api/admin"
import { getBrowserCoordinates } from "@/lib/browser-geolocation"
import { repairMojibakeText } from "@/lib/utils"

type FormState = {
  email: string
  employeeName: string
  storeName: string
  storeAddress: string
  storeContactPhone: string
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
  email: "",
  employeeName: "",
  storeName: "",
  storeAddress: "",
  storeContactPhone: "",
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
    <Suspense fallback={<div className="flex min-h-screen items-center justify-center bg-[#f6f8fb] text-sm text-muted-foreground">加载企微员工号绑定...</div>}>
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
  const [loginStatus, setLoginStatus] = useState<WxWorkProtocolLoginStatus | null>(null)
  const [loginCode, setLoginCode] = useState("")
  const [loginProxy, setLoginProxy] = useState("")
  const [loginVerifying, setLoginVerifying] = useState(false)
  const [emailCode, setEmailCode] = useState("")
  const [emailVerificationToken, setEmailVerificationToken] = useState("")
  const [emailSending, setEmailSending] = useState(false)
  const [emailVerifying, setEmailVerifying] = useState(false)
  const [locatingStoreCoordinates, setLocatingStoreCoordinates] = useState(false)

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
        email: "",
        employeeName: repairMojibakeText(data.employeeName || ""),
        storeName: repairMojibakeText(data.storeName || ""),
        storeAddress: repairMojibakeText(data.storeAddress || ""),
        storeContactPhone: repairMojibakeText(data.storeContactPhone || ""),
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
      toast.error(error instanceof Error ? error.message : "加载企微员工号绑定信息失败")
    } finally {
      setLoading(false)
    }
  }

  const qrcodeImage = useMemo(() => {
    const value = qrcode?.qrcode || qrcode?.qrcodeContent || ""
    if (!value) return ""
    if (value.startsWith("data:image")) return value
    if (value.startsWith("http://") || value.startsWith("https://")) return value
    return `data:image/png;base64,${value}`
  }, [qrcode])

  function setValue<K extends keyof FormState>(key: K, value: FormState[K]) {
    setForm((current) => ({ ...current, [key]: value }))
  }

  async function getLoginQRCode() {
    if (!token) return
    if (!loginProxy.trim() && !instance?.proxyConfigured) {
      toast.error("请先填写扫码设备上的异地登录代理地址")
      return
    }
    try {
      const data = await getWxWorkProtocolRemoteSetupLoginQrcode(
        token,
        loginProxy.trim() || undefined
      )
      setQrcode(data)
      setLoginStatus(null)
      setLoginCode("")
      toast.success("已获取登录二维码")
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "获取二维码失败")
    }
  }

  async function checkLogin() {
    if (!token) return
    setChecking(true)
    try {
      const status = await checkWxWorkProtocolRemoteSetupLogin(token)
      setLoginStatus(status)
      if (status.status === "success") {
        toast.success("员工号登录成功")
        await loadRemoteSetup(token)
      }
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "检查扫码状态失败")
    } finally {
      setChecking(false)
    }
  }

  async function verifyLogin() {
    if (!token || !loginCode.trim()) {
      toast.error("请输入新设备显示的确认码")
      return
    }
    setLoginVerifying(true)
    try {
      const status = await verifyWxWorkProtocolRemoteSetupLogin(token, loginCode.trim())
      setLoginStatus(status)
      if (status.status === "success") {
        setLoginCode("")
        toast.success("确认码验证成功，员工号已登录")
        await loadRemoteSetup(token)
      } else if (status.status === "verification_required") {
        toast.error(status.message || "确认码未通过，请核对后重试")
      }
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "确认码验证失败")
    } finally {
      setLoginVerifying(false)
    }
  }

  async function getCurrentLocation() {
    setLocatingStoreCoordinates(true)
    try {
      const coordinates = await getBrowserCoordinates()
      setForm((current) => ({
        ...current,
        storeLatitude: coordinates.latitude.toFixed(6),
        storeLongitude: coordinates.longitude.toFixed(6),
        storeMapProvider: "browser_geolocation",
      }))
      toast.success(`已填入当前坐标（精度约 ${Math.round(coordinates.accuracy)} 米），请确认是在门店现场获取`)
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "获取坐标失败，请手动填写经纬度")
    } finally {
      setLocatingStoreCoordinates(false)
    }
  }

  async function save() {
    if (!token) return
    if (!emailVerificationToken) {
      toast.error("请先验证系统账号登记邮箱")
      return
    }
    setSaving(true)
    try {
      await updateWxWorkProtocolRemoteSetup({ token, emailVerificationToken, ...form })
      toast.success("已提交门店配置")
      await loadRemoteSetup(token)
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "保存失败")
    } finally {
      setSaving(false)
    }
  }

  async function sendEmailCode() {
    if (!token || !form.email.trim()) {
      toast.error("请填写系统账号登记邮箱")
      return
    }
    setEmailSending(true)
    try {
      await sendWxWorkProtocolRemoteSetupEmailCode({ token, email: form.email.trim() })
      setEmailVerificationToken("")
      toast.success("验证码已发送，请检查邮箱")
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "验证码发送失败")
    } finally {
      setEmailSending(false)
    }
  }

  async function verifyEmail() {
    if (!token || !form.email.trim() || !emailCode.trim()) {
      toast.error("请填写邮箱和验证码")
      return
    }
    setEmailVerifying(true)
    try {
      const result = await verifyWxWorkProtocolRemoteSetupEmail({ token, email: form.email.trim(), code: emailCode.trim() })
      setEmailVerificationToken(result.verificationToken)
      toast.success("邮箱验证成功")
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "邮箱验证失败")
    } finally {
      setEmailVerifying(false)
    }
  }

  if (loading) {
    return <div className="flex min-h-screen items-center justify-center bg-[#f6f8fb] text-sm text-muted-foreground">加载企微员工号绑定...</div>
  }

  return (
    <main className="min-h-screen bg-[#f6f8fb] px-4 py-8 text-foreground">
      <div className="mx-auto max-w-5xl space-y-5">
        <section className="rounded-3xl border border-[#dbe7f6] bg-white p-6 shadow-[0_20px_60px_rgba(35,74,122,0.08)]">
          <div className="flex flex-col gap-4 md:flex-row md:items-center md:justify-between">
            <div>
              <div className="text-sm font-medium text-muted-foreground">知悉微宝</div>
              <h1 className="mt-1 text-2xl font-semibold tracking-normal">企微员工号绑定</h1>
              <p className="mt-2 max-w-2xl text-sm leading-6 text-muted-foreground">该链接已锁定公司主管选定的系统账号和门店。请用实际接待客户的企微员工号扫码，并补充门店资料。</p>
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
            <div className="mt-4">
              <WxWorkProtocolLoginProxyField
                value={loginProxy}
                configured={instance?.proxyConfigured}
                onChange={setLoginProxy}
              />
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
              <Button
                type="button"
                className="rounded-xl"
                onClick={() => void getLoginQRCode()}
                disabled={!loginProxy.trim() && !instance?.proxyConfigured}
              >
                <QrCodeIcon className="size-4" />
                设置代理并获取登录二维码
              </Button>
              <Button type="button" variant="outline" className="rounded-xl" onClick={() => void checkLogin()} disabled={checking}>
                <RefreshCwIcon className={checking ? "size-4 animate-spin" : "size-4"} />
                检查扫码状态
              </Button>
              {loginStatus ? (
                <div className={`rounded-xl px-3 py-2 text-sm ${loginStatus.status === "success" ? "bg-emerald-50 text-emerald-700" : loginStatus.status === "failed" || loginStatus.status === "refused" || loginStatus.status === "expired" ? "bg-red-50 text-red-700" : "bg-amber-50 text-amber-800"}`}>
                  {loginStatus.message}
                </div>
              ) : null}
              {loginStatus?.requiresCode ? (
                <div className="grid gap-2 rounded-xl border border-amber-200 bg-amber-50 p-3">
                  <div className="text-sm font-medium text-amber-950">输入新设备登录确认码</div>
                  <Input inputMode="numeric" autoComplete="one-time-code" value={loginCode} onChange={(event) => setLoginCode(event.target.value)} placeholder="确认码" />
                  <Button type="button" onClick={() => void verifyLogin()} disabled={loginVerifying || !loginCode.trim()}>
                    {loginVerifying ? "验证中..." : "验证并继续登录"}
                  </Button>
                </div>
              ) : null}
              {instance?.employeeUserId ? (
                <div className="flex items-center gap-2 rounded-xl bg-emerald-50 px-3 py-2 text-sm text-emerald-700">
                  <CheckCircle2Icon className="size-4" /> 已同步：{repairMojibakeText(instance.employeeName) || instance.employeeUserId}
                </div>
              ) : null}
			  {instance?.knowledgeProvisionStatus ? (
				<div className={`rounded-xl px-3 py-2 text-sm ${instance.knowledgeProvisionStatus === "failed" ? "bg-red-50 text-red-700" : instance.knowledgeProvisionStatus === "ready" ? "bg-emerald-50 text-emerald-700" : "bg-amber-50 text-amber-700"}`}>
				  知识库：{instance.knowledgeProvisionStatus === "ready" ? "已创建" : instance.knowledgeProvisionStatus === "failed" ? "创建失败" : "正在创建"}
				  {instance.knowledgeProvisionError ? <div className="mt-1 text-xs leading-5">{instance.knowledgeProvisionError}</div> : null}
				</div>
			  ) : null}
            </div>
          </section>

          <section className="rounded-3xl border border-[#dbe7f6] bg-white p-5 shadow-[0_16px_42px_rgba(35,74,122,0.06)]">
            <h2 className="font-semibold">2. 确认账号并填写门店资料</h2>
            <div className="mt-4 grid gap-4 md:grid-cols-2">
              <div className="md:col-span-2 rounded-2xl border border-[#dbe7f6] bg-[#f8fbff] px-4 py-3 text-sm text-muted-foreground">
                已绑定系统账号：<span className="font-medium text-foreground">{repairMojibakeText(instance?.storeStaffUserName || "") || `账号 #${instance?.storeStaffUserId || "-"}`}</span>
                <span className="mx-2">·</span>
                门店：<span className="font-medium text-foreground">{repairMojibakeText(instance?.storeName || "") || "待补充"}</span>。本页不会注册新账号或分配角色。
              </div>
              <Field label="员工号显示名"><Input value={form.employeeName} onChange={(event) => setValue("employeeName", event.target.value)} placeholder="例如：吴朝伟" /></Field>
              <Field label="门店名称"><Input value={form.storeName} onChange={(event) => setValue("storeName", event.target.value)} placeholder="例如：示例酒店杭州某某店" /></Field>
			  <div className="md:col-span-2 rounded-2xl border border-[#dbe7f6] bg-[#f8fbff] p-4">
					<label className="text-sm font-medium">系统账号登记邮箱</label>
					<p className="mt-1 text-xs leading-5 text-muted-foreground">请输入该系统账号在用户管理中登记的邮箱。验证仅用于确认绑定操作，不会创建账号或改变角色。</p>
				<div className="mt-3 grid gap-2 sm:grid-cols-[1fr_150px_auto_auto]">
				  <Input type="email" value={form.email} onChange={(event) => { setValue("email", event.target.value); setEmailVerificationToken("") }} placeholder="name@example.com" disabled={Boolean(emailVerificationToken)} />
				  <Input inputMode="numeric" value={emailCode} onChange={(event) => setEmailCode(event.target.value)} placeholder="6 位验证码" disabled={Boolean(emailVerificationToken)} />
				  <Button type="button" variant="outline" onClick={() => void sendEmailCode()} disabled={emailSending || Boolean(emailVerificationToken)}>{emailSending ? "发送中" : "发送验证码"}</Button>
				  <Button type="button" variant={emailVerificationToken ? "outline" : "default"} onClick={() => void verifyEmail()} disabled={emailVerifying || Boolean(emailVerificationToken)}>{emailVerificationToken ? "已验证" : emailVerifying ? "验证中" : "验证邮箱"}</Button>
				</div>
			  </div>
              <Field label="门店地址"><Input value={form.storeAddress} onChange={(event) => setValue("storeAddress", event.target.value)} placeholder="填写可导航地址" /></Field>
              <Field label="联系电话"><Input value={form.storeContactPhone} onChange={(event) => setValue("storeContactPhone", event.target.value)} placeholder="例如：0551-88888888 / 13800000000" /></Field>
              <Field label="定位卡片标题"><Input value={form.storeNavigationName} onChange={(event) => setValue("storeNavigationName", event.target.value)} placeholder="默认可用门店名称" /></Field>
              <Field label="纬度"><Input value={form.storeLatitude} onChange={(event) => setValue("storeLatitude", event.target.value)} placeholder="例如：30.27415" /></Field>
              <Field label="经度"><Input value={form.storeLongitude} onChange={(event) => setValue("storeLongitude", event.target.value)} placeholder="例如：120.15515" /></Field>
              <div className="md:col-span-2">
                <Button type="button" variant="outline" className="rounded-xl" onClick={() => void getCurrentLocation()} disabled={locatingStoreCoordinates}>
                  {locatingStoreCoordinates ? <RefreshCwIcon className="size-4 animate-spin" /> : <LocateFixedIcon className="size-4" />}
                  {locatingStoreCoordinates ? "正在定位" : "一键获取当前坐标"}
                </Button>
                <p className="mt-2 text-xs leading-5 text-muted-foreground">请门店员工在门店现场点击；浏览器定位不可用时，请从地图复制经纬度手动填写。坐标用于客户明确索要本门店定位时发送真实微信定位卡片。</p>
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

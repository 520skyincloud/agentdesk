"use client"

import { useEffect, useMemo, useRef, useState } from "react"
import {
  CheckCircle2Icon,
  CopyIcon,
  LinkIcon,
  LoaderCircleIcon,
  QrCodeIcon,
  RefreshCwIcon,
  UserRoundCheckIcon,
} from "lucide-react"
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
import { Input } from "@/components/ui/input"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import {
  checkWxWorkProtocolLoginQrcode,
  createWxWorkProtocolRemoteSetup,
  fetchChannels,
  fetchUserDetail,
  fetchUsersAll,
  startWxWorkProtocolLogin,
  syncWxWorkProtocolProfile,
  verifyWxWorkProtocolLogin,
  type AdminChannel,
  type AdminUser,
  type StartWxWorkProtocolLoginResult,
  type WxWorkProtocolInstance,
  type WxWorkProtocolLoginStatus,
} from "@/lib/api/admin"
import { Status } from "@/lib/generated/enums"
import { repairMojibakeText } from "@/lib/utils"

type WxWorkProtocolBindingDialogProps = {
  open: boolean
  user?: AdminUser | null
  onOpenChange: (open: boolean) => void
  onChanged?: (instance: WxWorkProtocolInstance) => void | Promise<void>
}

function isStoreStaff(user: AdminUser) {
  return user.status === Status.Ok && (user.roles || []).some((role) => role.code === "store_staff")
}

function canResumeBinding(user: AdminUser) {
  if (!user.storeStaff?.wxWorkInstanceId) return true
  return ["login_qrcode", "remote_setup"].includes(user.storeStaff.wxWorkHealthStatus || "")
}

function userLabel(user: AdminUser) {
  const name = repairMojibakeText(user.nickname || user.username)
  const store = repairMojibakeText(user.storeStaff?.storeName || "")
  return [name, user.username !== name ? user.username : "", store].filter(Boolean).join(" · ")
}

function qrCodeSource(value: string) {
  const source = value.trim()
  if (!source) return ""
  if (source.startsWith("data:image") || source.startsWith("http://") || source.startsWith("https://")) {
    return source
  }
  return `data:image/png;base64,${source}`
}

export function WxWorkProtocolBindingDialog({
  open,
  user: lockedUser,
  onOpenChange,
  onChanged,
}: WxWorkProtocolBindingDialogProps) {
  const { session } = useAuth()
  const permissionSet = useMemo(() => new Set(session?.permissions ?? []), [session?.permissions])
  const canCreate = permissionSet.has("channel.create") && permissionSet.has("user.view")
  const canUpdate = permissionSet.has("channel.update")
  const [channels, setChannels] = useState<AdminChannel[]>([])
  const [users, setUsers] = useState<AdminUser[]>([])
  const [channelId, setChannelId] = useState("0")
  const [userId, setUserId] = useState("0")
  const [loading, setLoading] = useState(false)
  const [mode, setMode] = useState<"onsite" | "remote">("onsite")
  const [starting, setStarting] = useState(false)
  const [loginResult, setLoginResult] = useState<StartWxWorkProtocolLoginResult | null>(null)
  const [loginStatus, setLoginStatus] = useState<WxWorkProtocolLoginStatus | null>(null)
  const [loginCode, setLoginCode] = useState("")
  const [verifying, setVerifying] = useState(false)
  const [remoteURL, setRemoteURL] = useState("")
  const checkingRef = useRef(false)
  const completedRef = useRef(false)
  const onChangedRef = useRef(onChanged)

  useEffect(() => {
    onChangedRef.current = onChanged
  }, [onChanged])

  const selectedUser = users.find((item) => item.id === Number(userId)) || null
  const userOptions = users.map((item) => ({ value: String(item.id), label: userLabel(item) }))
  const channelOptions = channels.map((item) => ({
    value: String(item.id),
    label: repairMojibakeText(item.name || item.channelId),
  }))

  useEffect(() => {
    if (!open || !canCreate) return
    let disposed = false
    setLoading(true)
    setLoginResult(null)
    setLoginStatus(null)
    setLoginCode("")
    setRemoteURL("")
    completedRef.current = false
    Promise.all([
      fetchChannels({ channelType: "wxwork_protocol", status: Status.Ok, limit: 200 }),
      lockedUser ? fetchUserDetail(lockedUser.id).then((item) => [item]) : fetchUsersAll({ roleCode: "store_staff", status: Status.Ok }),
    ])
      .then(([channelPage, userList]) => {
        if (disposed) return
        const bindableUsers = userList.filter((item) => isStoreStaff(item) && canResumeBinding(item))
        setChannels(channelPage.results)
        setUsers(bindableUsers)
        const firstUser = bindableUsers[0] || null
        setChannelId(String(channelPage.results[0]?.id || 0))
        setUserId(String(firstUser?.id || 0))
      })
      .catch((error) => toast.error(error instanceof Error ? error.message : "加载绑定选项失败"))
      .finally(() => {
        if (!disposed) setLoading(false)
      })
    return () => {
      disposed = true
    }
  }, [canCreate, lockedUser, open])

  useEffect(() => {
    if (!open || !loginResult?.instance.id || completedRef.current) return
    let disposed = false
    const check = async () => {
      if (checkingRef.current || disposed) return
      checkingRef.current = true
      try {
        const status = await checkWxWorkProtocolLoginQrcode(loginResult.instance.id)
        if (disposed) return
        setLoginStatus(status)
        if (status.status === "success") {
          completedRef.current = true
          if (canUpdate) {
            await syncWxWorkProtocolProfile(loginResult.instance.id).catch(() => "")
          }
          toast.success("企微员工号绑定成功")
          await onChangedRef.current?.(loginResult.instance)
        }
      } catch (error) {
        if (!disposed) {
          setLoginStatus({
            status: "failed",
            statusCode: -1,
            requiresCode: false,
            message: error instanceof Error ? error.message : "检查扫码状态失败",
          })
        }
      } finally {
        checkingRef.current = false
      }
    }
    void check()
    const timer = window.setInterval(() => void check(), 3000)
    return () => {
      disposed = true
      window.clearInterval(timer)
    }
  }, [canUpdate, loginResult?.instance, open])

  function selectUser(value: string) {
    setUserId(value)
    setLoginResult(null)
    setLoginStatus(null)
    setRemoteURL("")
    completedRef.current = false
  }

  function validateSelection() {
    if (!Number(userId)) {
      toast.error("请选择已有系统账号")
      return false
    }
    if (!selectedUser?.storeStaff?.storeId || !selectedUser.storeStaff.bindingId) {
      toast.error("该门店员工号尚未绑定有效门店")
      return false
    }
    if (!Number(channelId)) {
      toast.error("暂无可用企微协议渠道")
      return false
    }
    return true
  }

  async function startOnsiteBinding() {
    if (!validateSelection()) return
    setStarting(true)
    setLoginStatus(null)
    setLoginCode("")
    completedRef.current = false
    try {
      const result = await startWxWorkProtocolLogin({
        channelId: Number(channelId),
        storeStaffUserId: Number(userId),
      })
      setLoginResult(result)
      setLoginStatus({
        status: "pending",
        statusCode: 0,
        requiresCode: false,
        message: "等待企微员工号扫码确认",
      })
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "生成登录二维码失败")
    } finally {
      setStarting(false)
    }
  }

  async function verifyLoginCode() {
    if (!loginResult?.instance.id || !loginCode.trim()) return
    setVerifying(true)
    try {
      const status = await verifyWxWorkProtocolLogin(loginResult.instance.id, loginCode.trim())
      setLoginStatus(status)
      if (status.status === "success") {
        completedRef.current = true
        if (canUpdate) {
          await syncWxWorkProtocolProfile(loginResult.instance.id).catch(() => "")
        }
        toast.success("企微员工号绑定成功")
        await onChangedRef.current?.(loginResult.instance)
      }
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "登录确认码验证失败")
    } finally {
      setVerifying(false)
    }
  }

  async function createRemoteBindingLink() {
    if (!validateSelection()) return
    if (!selectedUser?.email) {
      toast.error("该系统账号尚未登记邮箱，请先在用户管理补充邮箱")
      return
    }
    setStarting(true)
    try {
      const item = await createWxWorkProtocolRemoteSetup({
        channelId: Number(channelId),
        storeStaffUserId: Number(userId),
        remark: "企微员工号绑定链接",
      })
      const url = item.remoteSetupUrl || `${window.location.origin}/wxwork-remote-setup?token=${encodeURIComponent(item.remoteSetupToken || "")}`
      setRemoteURL(url)
      await navigator.clipboard.writeText(url)
      toast.success("企微员工号绑定链接已复制")
      await onChangedRef.current?.(item)
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "生成绑定链接失败")
    } finally {
      setStarting(false)
    }
  }

  async function copyRemoteURL() {
    if (!remoteURL) return
    await navigator.clipboard.writeText(remoteURL)
    toast.success("绑定链接已复制")
  }

  const qrSource = qrCodeSource(loginResult?.qrcode || "")

  return (
    <Dialog open={open && canCreate} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[calc(100vh-2rem)] overflow-y-auto sm:max-w-3xl">
        <DialogHeader>
          <DialogTitle>绑定企微员工号</DialogTitle>
          <DialogDescription>选择已经绑定门店的系统员工号，再绑定其实际使用的企微账号。门店归属以系统员工号的 Store ID 为准。</DialogDescription>
        </DialogHeader>

        {loading ? (
          <div className="flex min-h-52 items-center justify-center text-sm text-muted-foreground">
            <LoaderCircleIcon className="mr-2 size-4 animate-spin" />
            加载账号
          </div>
        ) : users.length === 0 ? (
          <div className="rounded-lg border border-dashed px-4 py-10 text-center text-sm text-muted-foreground">
            暂无可绑定账号。请先在用户管理创建或邀请注册账号，并分配门店员工号角色。
          </div>
        ) : (
          <div className="space-y-5">
            <div className="grid gap-4">
              <label className="space-y-2 text-sm font-medium">
                <span>已有系统账号</span>
                <OptionCombobox
                  value={userId}
                  options={userOptions}
                  onChange={selectUser}
                  disabled={Boolean(lockedUser)}
                  placeholder="选择已有系统账号"
                  searchPlaceholder="搜索姓名或账号"
                  emptyText="没有匹配账号"
                />
              </label>
              {channels.length > 1 ? (
                <label className="space-y-2 text-sm font-medium">
                  <span>企微协议渠道</span>
                  <OptionCombobox
                    value={channelId}
                    options={channelOptions}
                    onChange={setChannelId}
                    placeholder="选择协议渠道"
                    searchPlaceholder="搜索协议渠道"
                    emptyText="没有可用渠道"
                  />
                </label>
              ) : null}
            </div>

            <div className="flex flex-wrap items-center gap-2 rounded-lg border bg-muted/30 px-3 py-2 text-sm">
              <UserRoundCheckIcon className="size-4 text-primary" />
              <span className="font-medium">{selectedUser ? userLabel(selectedUser) : "未选择账号"}</span>
              <Badge variant="outline">{selectedUser?.storeStaff?.agentTeamName || "暂未分配客服组"}</Badge>
            </div>

            <Tabs value={mode} onValueChange={(value) => setMode(value === "remote" ? "remote" : "onsite")}>
              <TabsList className="grid w-full grid-cols-2">
                <TabsTrigger value="onsite"><QrCodeIcon />现场扫码</TabsTrigger>
                <TabsTrigger value="remote"><LinkIcon />发送绑定链接</TabsTrigger>
              </TabsList>

              <TabsContent value="onsite" className="mt-4 space-y-4">
                <div className="grid gap-4 sm:grid-cols-[240px_1fr]">
                  <div className="flex aspect-square items-center justify-center rounded-lg border bg-muted/30 p-4">
                    {qrSource ? (
                      // eslint-disable-next-line @next/next/no-img-element
                      <img src={qrSource} alt="企微员工号登录二维码" className="size-full object-contain" />
                    ) : (
                      <QrCodeIcon className="size-12 text-muted-foreground" />
                    )}
                  </div>
                  <div className="space-y-3">
                    <div className="rounded-lg border px-3 py-3 text-sm">
                      <div className="font-medium">{loginStatus?.message || "尚未生成登录二维码"}</div>
                      {loginResult?.instance.guid ? (
                        <div className="mt-1 font-mono text-xs text-muted-foreground">{loginResult.instance.guid}</div>
                      ) : null}
                    </div>
                    {loginStatus?.requiresCode ? (
                      <div className="space-y-2 rounded-lg border border-amber-200 bg-amber-50 p-3">
                        <Input
                          inputMode="numeric"
                          autoComplete="one-time-code"
                          value={loginCode}
                          onChange={(event) => setLoginCode(event.target.value)}
                          placeholder="新设备登录确认码"
                        />
                        <Button className="w-full" onClick={() => void verifyLoginCode()} disabled={verifying || !loginCode.trim()}>
                          {verifying ? <LoaderCircleIcon className="animate-spin" /> : <CheckCircle2Icon />}
                          {verifying ? "验证中" : "验证确认码"}
                        </Button>
                      </div>
                    ) : null}
                    <Button className="w-full" onClick={() => void startOnsiteBinding()} disabled={starting}>
                      {starting ? <LoaderCircleIcon className="animate-spin" /> : loginResult ? <RefreshCwIcon /> : <QrCodeIcon />}
                      {starting ? "生成中" : loginResult ? "重新生成二维码" : "生成登录二维码"}
                    </Button>
                  </div>
                </div>
              </TabsContent>

              <TabsContent value="remote" className="mt-4 space-y-4">
                <div className="rounded-lg border px-4 py-4">
                  <div className="text-sm font-medium">系统账号登记邮箱</div>
                  <div className="mt-1 text-sm text-muted-foreground">{selectedUser?.email || "尚未登记邮箱"}</div>
                </div>
                {remoteURL ? (
                  <div className="flex items-center gap-2">
                    <Input value={remoteURL} readOnly className="font-mono text-xs" />
                    <Button type="button" variant="outline" size="icon" onClick={() => void copyRemoteURL()} title="复制绑定链接">
                      <CopyIcon />
                    </Button>
                  </div>
                ) : null}
                <Button className="w-full" onClick={() => void createRemoteBindingLink()} disabled={starting || !selectedUser?.email}>
                  {starting ? <LoaderCircleIcon className="animate-spin" /> : <LinkIcon />}
                  {starting ? "生成中" : "生成并复制绑定链接"}
                </Button>
              </TabsContent>
            </Tabs>
          </div>
        )}

        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>关闭</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

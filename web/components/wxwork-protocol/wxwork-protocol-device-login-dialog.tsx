"use client"

import { useCallback, useEffect, useRef, useState } from "react"
import {
  CheckIcon,
  LoaderCircleIcon,
  QrCodeIcon,
  RefreshCwIcon,
  ShieldCheckIcon,
} from "lucide-react"
import { toast } from "sonner"

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
import {
  checkWxWorkProtocolLoginQrcode,
  getWxWorkProtocolLoginQrcode,
  syncWxWorkProtocolProfile,
  verifyWxWorkProtocolLogin,
  type WxWorkProtocolInstance,
  type WxWorkProtocolLoginQRCodeResult,
  type WxWorkProtocolLoginStatus,
} from "@/lib/api/admin"
import { repairMojibakeText } from "@/lib/utils"

type WxWorkProtocolDeviceLoginDialogProps = {
  open: boolean
  instance: WxWorkProtocolInstance | null
  onOpenChange: (open: boolean) => void
  onChanged?: () => void | Promise<void>
}

const pendingLoginStatus: WxWorkProtocolLoginStatus = {
  status: "pending",
  statusCode: 0,
  requiresCode: false,
  message: "等待企微员工号扫码",
}

function qrCodeSource(value: string) {
  const source = value.trim()
  if (!source) return ""
  if (
    source.startsWith("data:image") ||
    source.startsWith("http://") ||
    source.startsWith("https://")
  ) {
    return source
  }
  return `data:image/png;base64,${source}`
}

function loginStep(status: WxWorkProtocolLoginStatus | null, hasQRCode: boolean) {
  if (status?.status === "success") return 4
  if (hasQRCode) return 3
  return 2
}

export function WxWorkProtocolDeviceLoginDialog({
  open,
  instance,
  onOpenChange,
  onChanged,
}: WxWorkProtocolDeviceLoginDialogProps) {
  const instanceId = instance?.id ?? 0
  const [qrcode, setQRCode] =
    useState<WxWorkProtocolLoginQRCodeResult | null>(null)
  const [status, setStatus] = useState<WxWorkProtocolLoginStatus | null>(null)
  const [code, setCode] = useState("")
  const [generating, setGenerating] = useState(false)
  const [verifying, setVerifying] = useState(false)
  const checkingRef = useRef(false)
  const completedRef = useRef(false)
  const autoStartedInstanceRef = useRef(0)
  const requestSequenceRef = useRef(0)
  const onChangedRef = useRef(onChanged)

  useEffect(() => {
    onChangedRef.current = onChanged
  }, [onChanged])

  const finishLogin = useCallback(async () => {
    if (!instanceId || completedRef.current) return
    completedRef.current = true
    await syncWxWorkProtocolProfile(instanceId).catch(() => "")
    await onChangedRef.current?.()
    toast.success("企微员工号已登录")
  }, [instanceId])

  const generateQRCode = useCallback(async () => {
    if (!instanceId) return
    const requestSequence = ++requestSequenceRef.current
    completedRef.current = false
    setGenerating(true)
    setQRCode(null)
    setStatus(null)
    setCode("")
    try {
      const result = await getWxWorkProtocolLoginQrcode(instanceId)
      if (requestSequence !== requestSequenceRef.current) return
      if (!result.qrcode.trim()) {
        throw new Error("企微员工号协议未返回可展示的登录二维码")
      }
      setQRCode(result)
      setStatus(pendingLoginStatus)
    } catch (error) {
      if (requestSequence !== requestSequenceRef.current) return
      const message =
        error instanceof Error ? error.message : "生成登录二维码失败"
      setStatus({
        status: "failed",
        statusCode: -1,
        requiresCode: false,
        message,
      })
      toast.error(message)
    } finally {
      if (requestSequence === requestSequenceRef.current) {
        setGenerating(false)
      }
    }
  }, [instanceId])

  useEffect(() => {
    if (!open) {
      autoStartedInstanceRef.current = 0
      requestSequenceRef.current += 1
      setQRCode(null)
      setStatus(null)
      setCode("")
      return
    }
    if (!instanceId || autoStartedInstanceRef.current === instanceId) return
    autoStartedInstanceRef.current = instanceId
    void generateQRCode()
  }, [generateQRCode, instanceId, open])

  useEffect(() => {
    if (
      !open ||
      !instanceId ||
      !qrcode ||
      completedRef.current ||
      status?.status === "failed" ||
      status?.status === "refused" ||
      status?.status === "expired"
    ) {
      return
    }
    let disposed = false
    const check = async () => {
      if (checkingRef.current || disposed) return
      checkingRef.current = true
      try {
        const next = await checkWxWorkProtocolLoginQrcode(instanceId)
        if (disposed) return
        setStatus(next)
        if (next.status === "success") {
          await finishLogin()
        }
      } catch (error) {
        if (disposed) return
        setStatus((current) => ({
          ...(current ?? pendingLoginStatus),
          message:
            error instanceof Error ? error.message : "检查扫码状态失败",
        }))
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
  }, [finishLogin, instanceId, open, qrcode, status?.status])

  async function verifyCode() {
    const normalizedCode = code.trim()
    if (!instanceId || !normalizedCode || verifying) return
    setVerifying(true)
    try {
      const next = await verifyWxWorkProtocolLogin(
        instanceId,
        normalizedCode
      )
      setStatus(next)
      if (next.status === "success") {
        await finishLogin()
      } else if (next.requiresCode) {
        toast.error(next.message || "确认码无效，请重新输入")
      }
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : "登录确认码验证失败"
      )
    } finally {
      setVerifying(false)
    }
  }

  const step = loginStep(status, Boolean(qrcode))
  const employeeName =
    repairMojibakeText(instance?.employeeName || "") || "当前企微员工号"
  const storeName = repairMojibakeText(instance?.storeName || "")

  return (
    <Dialog open={open && Boolean(instance)} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-3xl">
        <DialogHeader>
          <DialogTitle>设备登录</DialogTitle>
          <DialogDescription>
            {[employeeName, storeName].filter(Boolean).join(" · ")}
          </DialogDescription>
        </DialogHeader>

        <ol className="grid grid-cols-4 gap-2" aria-label="设备登录进度">
          {["选择实例", "环境准备", "扫码登录", "登录成功"].map(
            (label, index) => {
              const number = index + 1
              const complete = number < step || step === 4
              const active = number === step && step !== 4
              return (
                <li
                  key={label}
                  className="flex min-w-0 items-center gap-2 text-sm"
                >
                  <span
                    className={[
                      "flex size-7 shrink-0 items-center justify-center rounded-full border",
                      complete
                        ? "border-primary bg-primary text-primary-foreground"
                        : active
                          ? "border-primary text-primary"
                          : "border-border text-muted-foreground",
                    ].join(" ")}
                  >
                    {complete ? <CheckIcon className="size-4" /> : number}
                  </span>
                  <span
                    className={
                      active || complete
                        ? "truncate font-medium"
                        : "truncate text-muted-foreground"
                    }
                  >
                    {label}
                  </span>
                </li>
              )
            }
          )}
        </ol>

        <div className="grid gap-5 sm:grid-cols-[minmax(0,1fr)_18rem]">
          <div className="flex aspect-square min-h-72 items-center justify-center rounded-lg border bg-white p-5">
            {generating ? (
              <LoaderCircleIcon className="size-10 animate-spin text-primary" />
            ) : qrcode ? (
              // eslint-disable-next-line @next/next/no-img-element
              <img
                src={qrCodeSource(qrcode.qrcode)}
                alt="企微员工号设备登录二维码"
                className="size-full object-contain"
              />
            ) : (
              <QrCodeIcon className="size-14 text-muted-foreground" />
            )}
          </div>

          <div className="flex min-w-0 flex-col gap-3">
            <div
              className="rounded-lg border px-4 py-3"
              aria-live="polite"
            >
              <div className="flex items-start gap-2">
                {status?.status === "success" ? (
                  <ShieldCheckIcon className="mt-0.5 size-5 shrink-0 text-emerald-600" />
                ) : status?.requiresCode ? (
                  <ShieldCheckIcon className="mt-0.5 size-5 shrink-0 text-amber-600" />
                ) : (
                  <LoaderCircleIcon
                    className={[
                      "mt-0.5 size-5 shrink-0 text-primary",
                      qrcode ? "animate-spin" : "",
                    ].join(" ")}
                  />
                )}
                <div className="min-w-0">
                  <div className="font-medium">
                    {status?.message ||
                      (generating
                        ? "正在准备登录二维码"
                        : "等待生成登录二维码")}
                  </div>
                  {status?.requiresCode ? (
                    <p className="mt-1 text-sm text-muted-foreground">
                      请将企微客户端显示的登录确认码填入下方。
                    </p>
                  ) : null}
                </div>
              </div>
            </div>

            {status?.requiresCode ? (
              <div className="space-y-3 rounded-lg border border-amber-200 bg-amber-50 p-4">
                <Input
                  aria-label="登录确认码"
                  inputMode="numeric"
                  autoComplete="one-time-code"
                  maxLength={32}
                  value={code}
                  onChange={(event) => setCode(event.target.value)}
                  onKeyDown={(event) => {
                    if (event.key === "Enter") void verifyCode()
                  }}
                  placeholder="请输入验证码"
                />
                <Button
                  className="w-full"
                  onClick={() => void verifyCode()}
                  disabled={verifying || !code.trim()}
                >
                  {verifying ? (
                    <LoaderCircleIcon className="animate-spin" />
                  ) : (
                    <ShieldCheckIcon />
                  )}
                  {verifying ? "提交中" : "提交验证码"}
                </Button>
              </div>
            ) : null}

            <Button
              variant="outline"
              className="mt-auto w-full"
              onClick={() => void generateQRCode()}
              disabled={generating || verifying}
            >
              <RefreshCwIcon className={generating ? "animate-spin" : ""} />
              重新生成二维码
            </Button>
          </div>
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            {status?.status === "success" ? "完成" : "取消登录"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

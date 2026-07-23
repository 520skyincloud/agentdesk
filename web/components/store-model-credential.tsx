"use client"

import { useCallback, useEffect, useMemo, useState } from "react"
import {
  CheckCircle2Icon,
  EyeIcon,
  EyeOffIcon,
  KeyRoundIcon,
  LoaderCircleIcon,
  RefreshCwIcon,
  ShieldAlertIcon,
  XCircleIcon,
} from "lucide-react"
import { toast } from "sonner"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Skeleton } from "@/components/ui/skeleton"
import { Switch } from "@/components/ui/switch"
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip"
import {
  activatePendingStoreModelProfile,
  approveStoreModelCredential,
  disableStoreModelCredential,
  fetchOwnStoreModelCredential,
  fetchStoreModelCredential,
  fetchStoreModelCredentialAudit,
  rejectStoreModelCredential,
  updateOwnStoreModelCredential,
  updateStoreModelCredential,
  updateStoreModelCredentialPolicy,
  type StoreModelCredential,
  type StoreModelCredentialAudit,
} from "@/lib/api/store-model-credential"
import { formatDateTime } from "@/lib/utils"

type CredentialMode = "manager" | "self"

type StoreModelCredentialPanelProps = {
  mode: CredentialMode
  tenantId?: number
  storeId?: number
  canUpdate: boolean
  onChanged?: (data: StoreModelCredential) => void
}

type StoreModelCredentialDialogProps = {
  open: boolean
  tenantId: number
  storeId: number
  storeName: string
  canUpdate: boolean
  onOpenChange: (open: boolean) => void
  onChanged?: (data: StoreModelCredential) => void
}

const credentialStatusLabels: Record<string, string> = {
  unconfigured: "未配置",
  pending_approval: "待审批",
  testing: "验证中",
  syncing_fastgpt: "同步中",
  ready: "待激活",
  active: "已启用",
  failed: "失败",
  disabled: "已停用",
}

const auditActionLabels: Record<string, string> = {
  configure: "配置",
  submit: "提交",
  approve: "批准",
  reject: "拒绝",
  test: "连接验证",
  sync_fastgpt: "同步知识服务",
  switch_profile: "切换模型方案",
  activate: "激活",
  disable: "停用",
  policy_update: "策略更新",
}

function statusLabel(value: string) {
  return credentialStatusLabels[value] || value || "未配置"
}

function resultLabel(value: string) {
  if (value === "success") return "成功"
  if (value === "failure") return "失败"
  if (value === "pending") return "处理中"
  return value || "-"
}

function fastGPTStatusLabel(value: string) {
  if (value === "ready") return "已就绪"
  if (value === "not_required") return "无需同步"
  if (value === "failed") return "失败"
  if (value === "syncing") return "同步中"
  return value || "尚未同步"
}

function statusVariant(value: string): "default" | "secondary" | "outline" | "destructive" {
  if (value === "active" || value === "ready") return "default"
  if (value === "failed") return "destructive"
  if (value === "disabled" || value === "unconfigured") return "outline"
  return "secondary"
}

export function StoreModelCredentialDialog({
  open,
  tenantId,
  storeId,
  storeName,
  canUpdate,
  onOpenChange,
  onChanged,
}: StoreModelCredentialDialogProps) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="flex max-h-[88vh] flex-col gap-0 overflow-hidden p-0 sm:max-w-4xl">
        <DialogHeader className="border-b px-6 py-5">
          <DialogTitle className="flex items-center gap-2 text-lg">
            <KeyRoundIcon className="size-5" />
            门店模型与凭据
          </DialogTitle>
          <DialogDescription>{storeName || `门店 #${storeId}`}</DialogDescription>
        </DialogHeader>
        <div className="min-h-0 flex-1 overflow-y-auto px-6 py-5">
          {open ? (
            <StoreModelCredentialPanel
              mode="manager"
              tenantId={tenantId}
              storeId={storeId}
              canUpdate={canUpdate}
              onChanged={onChanged}
            />
          ) : null}
        </div>
      </DialogContent>
    </Dialog>
  )
}

export function StoreModelCredentialPanel({
  mode,
  tenantId = 0,
  storeId = 0,
  canUpdate,
  onChanged,
}: StoreModelCredentialPanelProps) {
  const [data, setData] = useState<StoreModelCredential | null>(null)
  const [audit, setAudit] = useState<StoreModelCredentialAudit[]>([])
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [newKey, setNewKey] = useState("")
  const [currentPassword, setCurrentPassword] = useState("")
  const [confirmed, setConfirmed] = useState(false)
  const [showKey, setShowKey] = useState(false)
  const [allowSelfService, setAllowSelfService] = useState(false)
  const [requireApproval, setRequireApproval] = useState(false)

  const managerScope = useMemo(
    () => ({ tenantId, storeId }),
    [tenantId, storeId],
  )

  const load = useCallback(async () => {
    if (mode === "manager" && (tenantId <= 0 || storeId <= 0)) {
      setLoading(false)
      return
    }
    setLoading(true)
    try {
      if (mode === "manager") {
        const [next, logs] = await Promise.all([
          fetchStoreModelCredential(managerScope),
          fetchStoreModelCredentialAudit(managerScope),
        ])
        setData(next)
        setAudit(logs)
        setAllowSelfService(next.allowCredentialSelfService)
        setRequireApproval(next.requireSupervisorApproval)
      } else {
        const next = await fetchOwnStoreModelCredential()
        setData(next)
        setAudit([])
      }
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "读取门店模型凭据失败")
      setData(null)
      setAudit([])
    } finally {
      setLoading(false)
    }
  }, [managerScope, mode, storeId, tenantId])

  useEffect(() => {
    void load()
  }, [load])

  function resetSensitiveFields() {
    setNewKey("")
    setCurrentPassword("")
    setConfirmed(false)
    setShowKey(false)
  }

  async function refreshAfter(next: StoreModelCredential, message: string) {
    setData(next)
    setAllowSelfService(next.allowCredentialSelfService)
    setRequireApproval(next.requireSupervisorApproval)
    resetSensitiveFields()
    if (mode === "manager") {
      setAudit(await fetchStoreModelCredentialAudit(managerScope))
    }
    onChanged?.(next)
    toast.success(message)
  }

  function sensitivePayloadReady(requireKey: boolean) {
    if (requireKey && !newKey.trim()) {
      toast.error("请输入新的 NewAPI API Key")
      return false
    }
    if (!currentPassword) {
      toast.error("请输入当前账号密码")
      return false
    }
    if (!confirmed) {
      toast.error("请完成二次确认")
      return false
    }
    return true
  }

  async function submitCredential() {
    if (!data || !canSubmitCredential || saving || !sensitivePayloadReady(true)) return
    setSaving(true)
    try {
      const payload = { apiKey: newKey.trim(), currentPassword, confirmed }
      const next = mode === "manager"
        ? await updateStoreModelCredential(managerScope, payload)
        : await updateOwnStoreModelCredential(payload)
      await refreshAfter(
        next,
        next.candidateApprovalStatus === "pending" ? "凭据已提交审批" : "凭据验证并激活完成",
      )
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "门店模型凭据更新失败")
      void load()
    } finally {
      setSaving(false)
    }
  }

  async function decideCandidate(action: "approve" | "reject") {
    if (!data || mode !== "manager" || !canUpdate || saving || !sensitivePayloadReady(false)) return
    setSaving(true)
    try {
      const payload = {
        candidateRevision: data.candidateRevision,
        currentPassword,
        confirmed,
      }
      const next = action === "approve"
        ? await approveStoreModelCredential(managerScope, payload)
        : await rejectStoreModelCredential(managerScope, payload)
      await refreshAfter(next, action === "approve" ? "凭据审批并激活完成" : "凭据申请已拒绝")
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "凭据审批失败")
      void load()
    } finally {
      setSaving(false)
    }
  }

  async function disableCredential() {
    if (!data || mode !== "manager" || !canUpdate || saving || !sensitivePayloadReady(false)) return
    setSaving(true)
    try {
      const next = await disableStoreModelCredential(managerScope, { currentPassword, confirmed })
      await refreshAfter(next, "门店模型凭据已停用")
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "停用门店模型凭据失败")
      void load()
    } finally {
      setSaving(false)
    }
  }

  async function activatePendingProfile() {
    if (
      !data ||
      mode !== "manager" ||
      !canUpdate ||
      saving ||
      data.pendingProfileId <= 0 ||
      !sensitivePayloadReady(false)
    ) return
    setSaving(true)
    try {
      const next = await activatePendingStoreModelProfile(managerScope, {
        templateId: data.pendingProfileId,
        confirmRevision: data.pendingProfileRevision,
        currentPassword,
        confirmed,
      })
      await refreshAfter(next, "待选模型方案已验证并切换")
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "模型方案切换失败，当前方案继续使用")
      void load()
    } finally {
      setSaving(false)
    }
  }

  async function savePolicy() {
    if (!data || mode !== "manager" || !canUpdate || saving) return
    if (!sensitivePayloadReady(false)) return
    setSaving(true)
    try {
      await updateStoreModelCredentialPolicy({
        tenantId: data.tenantId,
        storeIds: [data.storeId],
        allowCredentialSelfService: allowSelfService,
        requireSupervisorApproval: requireApproval,
        currentPassword,
        confirmed: true,
      })
      resetSensitiveFields()
      await load()
      toast.success("门店凭据策略已保存")
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "门店凭据策略保存失败")
    } finally {
      setSaving(false)
    }
  }

  if (loading) return <CredentialLoading />
  if (!data) {
    return (
      <div className="flex min-h-48 flex-col items-center justify-center gap-3 text-center text-sm text-muted-foreground">
        <ShieldAlertIcon className="size-8" />
        <span>未能读取门店模型状态</span>
        <Button variant="outline" size="sm" onClick={() => void load()}>
          <RefreshCwIcon className="size-4" />
          重新加载
        </Button>
      </div>
    )
  }

  const canUpdateCredential = canUpdate && (mode === "manager" || data.canSelfService)
  const pendingApproval = data.candidateApprovalStatus === "pending" && data.candidateRevision > 0
  const liveCandidate = data.candidateRevision > 0 && [
    "pending_approval",
    "testing",
    "syncing_fastgpt",
    "ready",
  ].includes(data.candidateStatus)
  const canSubmitCredential = canUpdateCredential && !liveCandidate

  return (
    <div className="space-y-5">
      <section className="space-y-4">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div className="min-w-0">
            <div className="flex flex-wrap items-center gap-2">
              <h3 className="text-base font-semibold">{data.storeName}</h3>
              <Badge variant={statusVariant(data.credentialStatus)}>{statusLabel(data.credentialStatus)}</Badge>
            </div>
            <div className="mt-1 font-mono text-xs text-muted-foreground">{data.storeCode || `#${data.storeId}`}</div>
          </div>
          <Tooltip>
            <TooltipTrigger
              render={<Button variant="outline" size="icon-sm" disabled={saving} onClick={() => void load()} />}
              aria-label="刷新门店模型状态"
            >
              <RefreshCwIcon className={saving ? "animate-spin" : undefined} />
            </TooltipTrigger>
            <TooltipContent>刷新状态</TooltipContent>
          </Tooltip>
        </div>

        <div className="grid gap-3 md:grid-cols-2">
          <StatusBlock label="模型方案" value={
            data.activeProfileId > 0
              ? `${data.activeProfileName || "当前方案"} · r${data.activeProfileRevision}`
              : data.pendingProfileId > 0
                ? `${data.pendingProfileName || "待验证方案"} · r${data.pendingProfileRevision}`
                : "尚未指派"
          } />
          <StatusBlock label="API Key" value={
            data.hasKey
              ? `${data.keyMask}${data.fingerprintLast6 ? ` · ${data.fingerprintLast6}` : ""} · r${data.credentialRevision}`
              : "尚未配置"
          } mono />
          <StatusBlock label="连接验证" value={
            data.lastTestStatus
              ? `${data.lastTestStatus === "passed" ? "通过" : "失败"}${data.lastTestedAt ? ` · ${formatDateTime(data.lastTestedAt)}` : ""}`
              : "尚未验证"
          } />
          <StatusBlock label="知识服务同步" value={
            data.lastFastGPTSyncStatus
              ? `${fastGPTStatusLabel(data.lastFastGPTSyncStatus)}${data.lastFastGPTSyncedAt ? ` · ${formatDateTime(data.lastFastGPTSyncedAt)}` : ""}`
              : "尚未同步"
          } />
        </div>

        <div className="flex flex-wrap gap-1.5">
          {(data.activeModelNames.length > 0 ? data.activeModelNames : data.pendingModelNames).map((name) => (
            <Badge key={name} variant="outline">{name}</Badge>
          ))}
        </div>

        {data.candidateRevision > 0 ? (
          <div className="flex flex-wrap items-center gap-2 border-l-2 border-primary px-3 py-2 text-sm">
            <span className="font-medium">候选 r{data.candidateRevision}</span>
            <Badge variant={statusVariant(data.candidateStatus)}>{statusLabel(data.candidateStatus)}</Badge>
            {data.candidateFingerprintLast6 ? (
              <span className="font-mono text-xs text-muted-foreground">******{data.candidateFingerprintLast6}</span>
            ) : null}
            {data.candidateRequestedAt ? (
              <span className="text-xs text-muted-foreground">{formatDateTime(data.candidateRequestedAt)}</span>
            ) : null}
          </div>
        ) : null}

        {data.lastErrorMessage ? (
          <div className="rounded-lg border border-destructive/30 bg-destructive/5 px-3 py-2 text-sm text-destructive">
            {data.lastErrorMessage}
          </div>
        ) : null}
      </section>

      {mode === "manager" ? (
        <section className="border-t pt-5">
          <h3 className="text-sm font-semibold">门店维护策略</h3>
          <div className="mt-3 grid gap-3 sm:grid-cols-2">
            <PolicyToggle
              id={`credential-self-service-${data.storeId}`}
              label="允许门店员工自行填写"
              checked={allowSelfService}
              disabled={!canUpdate || saving}
              onCheckedChange={(checked) => {
                setAllowSelfService(checked)
                if (!checked) setRequireApproval(false)
              }}
            />
            <PolicyToggle
              id={`credential-approval-${data.storeId}`}
              label="门店提交后需要主管审批"
              checked={requireApproval}
              disabled={!canUpdate || saving || !allowSelfService}
              onCheckedChange={setRequireApproval}
            />
          </div>
          {canUpdate ? (
            <div className="mt-3 flex justify-end">
              <Button type="button" variant="outline" size="sm" disabled={saving} onClick={() => void savePolicy()}>
                保存策略
              </Button>
            </div>
          ) : null}
        </section>
      ) : null}

      <section className="border-t pt-5">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <h3 className="text-sm font-semibold">{data.hasKey ? "更新 API Key" : "配置 API Key"}</h3>
          {mode === "self" && !data.canSelfService ? (
            <Badge variant="outline">主管未开放自助维护</Badge>
          ) : null}
        </div>
        <div className="mt-3 grid gap-3 sm:grid-cols-2">
          {!pendingApproval ? (
            <div className="space-y-1.5 sm:col-span-2">
              <Label htmlFor={`new-api-key-${mode}-${data.storeId}`}>新的 NewAPI API Key</Label>
              <div className="relative">
                <Input
                  id={`new-api-key-${mode}-${data.storeId}`}
                  type={showKey ? "text" : "password"}
                  autoComplete="new-password"
                  value={newKey}
                  disabled={!canSubmitCredential || saving}
                  className="pr-10 font-mono"
                  onChange={(event) => setNewKey(event.target.value)}
                />
                <Tooltip>
                  <TooltipTrigger
                    render={<Button type="button" variant="ghost" size="icon-sm" className="absolute top-1/2 right-1 -translate-y-1/2" />}
                    disabled={!canSubmitCredential}
                    onClick={() => setShowKey((value) => !value)}
                    aria-label={showKey ? "隐藏新 API Key" : "显示新 API Key"}
                  >
                    {showKey ? <EyeOffIcon /> : <EyeIcon />}
                  </TooltipTrigger>
                  <TooltipContent>{showKey ? "隐藏" : "显示"}</TooltipContent>
                </Tooltip>
              </div>
            </div>
          ) : null}
          <div className="space-y-1.5 sm:col-span-2">
            <Label htmlFor={`credential-password-${mode}-${data.storeId}`}>当前账号密码</Label>
            <Input
              id={`credential-password-${mode}-${data.storeId}`}
              type="password"
              autoComplete="current-password"
              value={currentPassword}
              disabled={!(mode === "manager" ? canUpdate : canSubmitCredential) || saving}
              onChange={(event) => setCurrentPassword(event.target.value)}
            />
          </div>
        </div>
        <label className="mt-3 flex items-start gap-2 text-sm">
          <Checkbox
            checked={confirmed}
            disabled={!(mode === "manager" ? canUpdate : canSubmitCredential) || saving}
            onCheckedChange={(value) => setConfirmed(value === true)}
            aria-label="确认执行门店模型凭据敏感操作"
          />
          <span>确认执行本次门店模型凭据敏感操作</span>
        </label>
        <div className="mt-4 flex flex-wrap justify-end gap-2">
          {mode === "manager" && data.credentialStatus === "active" ? (
            <Button type="button" variant="outline" disabled={saving || !canUpdate} onClick={() => void disableCredential()}>
              <XCircleIcon className="size-4" />
              停用
            </Button>
          ) : null}
          {mode === "manager" &&
          data.hasKey &&
          data.activeProfileId > 0 &&
          data.pendingProfileId > 0 &&
          !liveCandidate ? (
            <Button
              type="button"
              variant="outline"
              disabled={saving || !canUpdate}
              onClick={() => void activatePendingProfile()}
            >
              <RefreshCwIcon className="size-4" />
              验证并切换待选方案
            </Button>
          ) : null}
          {mode === "manager" && pendingApproval ? (
            <>
              <Button type="button" variant="outline" disabled={saving || !canUpdate} onClick={() => void decideCandidate("reject")}>
                拒绝
              </Button>
              <Button type="button" disabled={saving || !canUpdate} onClick={() => void decideCandidate("approve")}>
                <CheckCircle2Icon className="size-4" />
                批准并验证
              </Button>
            </>
          ) : (
            <Button type="button" disabled={saving || !canSubmitCredential || !newKey.trim()} onClick={() => void submitCredential()}>
              {saving ? <LoaderCircleIcon className="size-4 animate-spin" /> : <KeyRoundIcon className="size-4" />}
              {liveCandidate ? "候选处理中" : data.hasKey ? "更新并验证" : "配置并验证"}
            </Button>
          )}
        </div>
      </section>

      {mode === "manager" ? (
        <section className="border-t pt-5">
          <h3 className="text-sm font-semibold">不可修改审计记录</h3>
          <div className="mt-3 overflow-x-auto border-y">
            <table className="w-full min-w-[680px] text-left text-xs">
              <thead className="bg-muted/50 text-muted-foreground">
                <tr>
                  <th className="px-3 py-2 font-medium">时间</th>
                  <th className="px-3 py-2 font-medium">动作</th>
                  <th className="px-3 py-2 font-medium">结果</th>
                  <th className="px-3 py-2 font-medium">版本</th>
                  <th className="px-3 py-2 font-medium">操作者</th>
                  <th className="px-3 py-2 font-medium">请求 ID</th>
                </tr>
              </thead>
              <tbody className="divide-y">
                {audit.map((item) => (
                  <tr key={item.id}>
                    <td className="whitespace-nowrap px-3 py-2">{formatDateTime(item.createdAt)}</td>
                    <td className="px-3 py-2">{auditActionLabels[item.action] || item.action}</td>
                    <td className="px-3 py-2">{resultLabel(item.result)}</td>
                    <td className="whitespace-nowrap px-3 py-2 font-mono">r{item.fromRevision} → r{item.toRevision}</td>
                    <td className="px-3 py-2">{item.operatorName || "系统"}</td>
                    <td className="max-w-48 truncate px-3 py-2 font-mono" title={item.requestId}>{item.requestId || "-"}</td>
                  </tr>
                ))}
                {audit.length === 0 ? (
                  <tr><td colSpan={6} className="px-3 py-8 text-center text-muted-foreground">暂无审计记录</td></tr>
                ) : null}
              </tbody>
            </table>
          </div>
        </section>
      ) : null}
    </div>
  )
}

function StatusBlock({ label, value, mono = false }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="min-w-0 border-l-2 border-border px-3 py-1">
      <div className="text-xs text-muted-foreground">{label}</div>
      <div className={`mt-1 break-words text-sm font-medium ${mono ? "font-mono" : ""}`}>{value}</div>
    </div>
  )
}

function PolicyToggle({
  id,
  label,
  checked,
  disabled,
  onCheckedChange,
}: {
  id: string
  label: string
  checked: boolean
  disabled: boolean
  onCheckedChange: (checked: boolean) => void
}) {
  return (
    <div className="flex min-h-12 items-center justify-between gap-3 rounded-lg border px-3 py-2">
      <Label htmlFor={id} className="text-sm">{label}</Label>
      <Switch id={id} checked={checked} disabled={disabled} onCheckedChange={onCheckedChange} />
    </div>
  )
}

function CredentialLoading() {
  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between gap-3">
        <Skeleton className="h-6 w-44" />
        <Skeleton className="size-8" />
      </div>
      <div className="grid gap-3 sm:grid-cols-2">
        <Skeleton className="h-14" />
        <Skeleton className="h-14" />
        <Skeleton className="h-14" />
        <Skeleton className="h-14" />
      </div>
      <Skeleton className="h-44" />
    </div>
  )
}

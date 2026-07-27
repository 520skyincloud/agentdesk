"use client"

import { useEffect, useMemo, useState } from "react"
import { BrainCircuitIcon, KeyRoundIcon, SearchIcon } from "lucide-react"
import { toast } from "sonner"

import { OptionCombobox } from "@/components/option-combobox"
import { StoreModelCredentialDialog } from "@/components/store-model-credential"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Switch } from "@/components/ui/switch"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import {
  batchAssignStoreModelProfile,
  fetchStoreModelProfileAssignments,
  type StoreModelProfileAssignments,
} from "@/lib/api/admin"
import type { AdminTenant } from "@/lib/api/tenant"
import { batchUpdateStoreModelCredentialPolicy } from "@/lib/api/store-model-credential"
import { useAIConfigurationRealtime } from "@/hooks/use-ai-configuration-realtime"

type TenantModelAccessDialogProps = {
  open: boolean
  tenant: AdminTenant | null
  canUpdate: boolean
  onOpenChange: (open: boolean) => void
}

function readinessLabel(value: string) {
  if (value === "ready") return "已就绪"
  if (value === "pending") return "待验证"
  if (value === "blocked") return "受阻"
  return "未配置"
}

export function TenantModelAccessDialog({
  open,
  tenant,
  canUpdate,
  onOpenChange,
}: TenantModelAccessDialogProps) {
  const [data, setData] = useState<StoreModelProfileAssignments | null>(null)
  const [loading, setLoading] = useState(false)
  const [saving, setSaving] = useState(false)
  const [query, setQuery] = useState("")
  const [profileId, setProfileId] = useState(0)
  const [selectedStoreIds, setSelectedStoreIds] = useState<Set<number>>(new Set())
  const [confirming, setConfirming] = useState(false)
  const [policyConfirming, setPolicyConfirming] = useState(false)
  const [policyPassword, setPolicyPassword] = useState("")
  const [allowCredentialSelfService, setAllowCredentialSelfService] = useState(false)
  const [requireSupervisorApproval, setRequireSupervisorApproval] = useState(false)
  const [credentialStore, setCredentialStore] = useState<StoreModelProfileAssignments["stores"][number] | null>(null)

  useEffect(() => {
    if (!open || !tenant) return
    let cancelled = false
    setLoading(true)
    setConfirming(false)
    setPolicyConfirming(false)
    setPolicyPassword("")
    setCredentialStore(null)
    setSelectedStoreIds(new Set())
    fetchStoreModelProfileAssignments(tenant.id)
      .then((next) => {
        if (cancelled) return
        setData(next)
        setProfileId(next.profiles[0]?.templateId ?? 0)
      })
      .catch((error) => {
        if (!cancelled) toast.error(error instanceof Error ? error.message : "读取门店模型指派失败")
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [open, tenant])

  useAIConfigurationRealtime((event) => {
    if (!open || !tenant || (event.tenantId > 0 && event.tenantId !== tenant.id)) return
    if (
      event.type !== "store_model_profile.changed" &&
      event.type !== "store_model_credential.changed"
    ) {
      return
    }
    void fetchStoreModelProfileAssignments(tenant.id)
      .then(setData)
      .catch(() => undefined)
  }, open && Boolean(tenant))

  const selectedProfile = useMemo(
    () => data?.profiles.find((item) => item.templateId === profileId) ?? null,
    [data, profileId],
  )
  const filteredStores = useMemo(() => {
    const keyword = query.trim().toLowerCase()
    if (!keyword) return data?.stores ?? []
    return (data?.stores ?? []).filter((item) =>
      [item.storeName, item.storeCode, item.activeTemplateName, item.pendingTemplateName]
        .join(" ")
        .toLowerCase()
        .includes(keyword),
    )
  }, [data, query])
  const allFilteredSelected =
    filteredStores.length > 0 && filteredStores.every((item) => selectedStoreIds.has(item.storeId))

  function setStoreSelected(storeId: number, selected: boolean) {
    setConfirming(false)
    setPolicyConfirming(false)
    setPolicyPassword("")
    setSelectedStoreIds((current) => {
      const next = new Set(current)
      if (selected) next.add(storeId)
      else next.delete(storeId)
      return next
    })
  }

  function toggleFiltered(selected: boolean) {
    setConfirming(false)
    setPolicyConfirming(false)
    setPolicyPassword("")
    setSelectedStoreIds((current) => {
      const next = new Set(current)
      for (const store of filteredStores) {
        if (selected) next.add(store.storeId)
        else next.delete(store.storeId)
      }
      return next
    })
  }

  async function assign() {
    if (!tenant || !selectedProfile || selectedStoreIds.size === 0 || !canUpdate) return
    if (!confirming) {
      setConfirming(true)
      return
    }
    setSaving(true)
    try {
      await batchAssignStoreModelProfile({
        tenantId: tenant.id,
        storeIds: [...selectedStoreIds],
        templateId: selectedProfile.templateId,
        confirmRevision: selectedProfile.revision,
      })
      const next = await fetchStoreModelProfileAssignments(tenant.id)
      setData(next)
      setSelectedStoreIds(new Set())
      setConfirming(false)
      toast.success(`已为门店提交 ${selectedProfile.name} r${selectedProfile.revision}`)
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "门店模型指派失败")
    } finally {
      setSaving(false)
    }
  }

  async function saveCredentialPolicy() {
    if (!tenant || selectedStoreIds.size === 0 || !canUpdate) return
    if (!policyConfirming) {
      setPolicyConfirming(true)
      setConfirming(false)
      return
    }
    if (!policyPassword) {
      toast.error("请输入当前账号密码")
      return
    }
    setSaving(true)
    try {
      await batchUpdateStoreModelCredentialPolicy({
        tenantId: tenant.id,
        storeIds: [...selectedStoreIds],
        allowCredentialSelfService,
        requireSupervisorApproval,
        currentPassword: policyPassword,
        confirmed: true,
      })
      setPolicyConfirming(false)
      setPolicyPassword("")
      toast.success(`已更新 ${selectedStoreIds.size} 家门店的凭据策略`)
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "批量凭据策略更新失败")
    } finally {
      setSaving(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="flex max-h-[88vh] flex-col gap-0 overflow-hidden p-0 sm:max-w-5xl">
        <DialogHeader className="border-b px-6 py-5">
          <DialogTitle className="flex items-center gap-2 text-lg">
            <BrainCircuitIcon className="size-5" />
            门店模型指派
          </DialogTitle>
          <DialogDescription>{tenant?.shortName ?? "接入公司"}</DialogDescription>
        </DialogHeader>

        {loading || !data ? (
          <div className="flex min-h-80 items-center justify-center text-sm text-muted-foreground">
            正在读取门店模型状态...
          </div>
        ) : (
          <div className="flex min-h-0 flex-1 flex-col overflow-hidden">
            <div className="grid gap-4 border-b px-6 py-4 md:grid-cols-[minmax(260px,1fr)_minmax(260px,1fr)]">
              <div>
                <div className="mb-2 text-xs font-medium text-muted-foreground">目标模型方案</div>
                <OptionCombobox
                  value={profileId ? String(profileId) : ""}
                  options={data.profiles.map((item) => ({
                    value: String(item.templateId),
                    label: `${item.name} · r${item.revision} · ${item.status === "active" ? "生效" : "候选"}`,
                  }))}
                  placeholder="选择候选或生效方案"
                  disabled={!canUpdate || data.profiles.length === 0}
                  onChange={(value) => {
                    setProfileId(Number(value))
                    setConfirming(false)
                    setPolicyConfirming(false)
                    setPolicyPassword("")
                  }}
                />
                {selectedProfile ? (
                  <div className="mt-2 flex flex-wrap gap-1.5">
                    {selectedProfile.modelNames.map((name) => (
                      <Badge key={name} variant="outline">{name}</Badge>
                    ))}
                  </div>
                ) : null}
              </div>
              <div>
                <div className="mb-2 text-xs font-medium text-muted-foreground">门店筛选</div>
                <div className="relative">
                  <SearchIcon className="absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" />
                  <Input
                    value={query}
                    onChange={(event) => setQuery(event.target.value)}
                    placeholder="搜索门店、编码或模型方案"
                    className="pl-9"
                  />
                </div>
              </div>
            </div>

            <div className="flex items-center justify-between gap-3 border-b px-6 py-3 text-sm">
              <label className="flex items-center gap-2 font-medium">
                <Checkbox
                  checked={allFilteredSelected}
                  disabled={!canUpdate || filteredStores.length === 0}
                  onCheckedChange={(value) => toggleFiltered(value === true)}
                  aria-label="选择当前筛选的全部门店"
                />
                当前筛选
              </label>
              <div className="text-muted-foreground">
                已选 {selectedStoreIds.size} / {data.stores.length}
              </div>
            </div>

            {canUpdate ? (
              <div className="flex flex-wrap items-center gap-x-5 gap-y-3 border-b px-6 py-3 text-sm">
                <label className="flex items-center gap-2">
                  <Switch
                    checked={allowCredentialSelfService}
                    onCheckedChange={(checked) => {
                      setAllowCredentialSelfService(checked)
                      if (!checked) setRequireSupervisorApproval(false)
                      setPolicyConfirming(false)
                      setPolicyPassword("")
                    }}
                  />
                  门店员工自助填写
                </label>
                <label className="flex items-center gap-2">
                  <Switch
                    checked={requireSupervisorApproval}
                    disabled={!allowCredentialSelfService}
                    onCheckedChange={(checked) => {
                      setRequireSupervisorApproval(checked)
                      setPolicyConfirming(false)
                      setPolicyPassword("")
                    }}
                  />
                  提交后主管审批
                </label>
                {policyConfirming ? (
                  <div className="flex min-w-56 flex-1 items-center gap-2 sm:max-w-xs">
                    <Label htmlFor="batch-credential-policy-password" className="sr-only">当前账号密码</Label>
                    <Input
                      id="batch-credential-policy-password"
                      type="password"
                      autoComplete="current-password"
                      value={policyPassword}
                      placeholder="输入当前账号密码"
                      disabled={saving}
                      onChange={(event) => setPolicyPassword(event.target.value)}
                    />
                  </div>
                ) : null}
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  className="sm:ml-auto"
                  disabled={saving || selectedStoreIds.size === 0}
                  onClick={() => void saveCredentialPolicy()}
                >
                  {policyConfirming ? `确认应用到 ${selectedStoreIds.size} 家门店` : "应用凭据策略"}
                </Button>
              </div>
            ) : null}

            <div className="min-h-0 flex-1 divide-y overflow-y-auto">
              {filteredStores.map((store) => (
                <div key={store.storeId} className="grid gap-3 px-6 py-3 hover:bg-muted/40 md:grid-cols-[28px_minmax(180px,1fr)_minmax(200px,1fr)_120px_36px] md:items-center">
                  <Checkbox
                    checked={selectedStoreIds.has(store.storeId)}
                    disabled={!canUpdate}
                    onCheckedChange={(value) => setStoreSelected(store.storeId, value === true)}
                    aria-label={`选择 ${store.storeName}`}
                  />
                  <div className="min-w-0">
                    <div className="truncate text-sm font-medium">{store.storeName}</div>
                    <div className="font-mono text-xs text-muted-foreground">{store.storeCode}</div>
                  </div>
                  <div className="min-w-0 text-sm">
                    <div className="truncate">
                      {store.activeTemplateId > 0
                        ? `${store.activeTemplateName} · r${store.activeTemplateRevision}`
                        : "尚无生效方案"}
                    </div>
                    {store.pendingTemplateId > 0 ? (
                      <div className="mt-0.5 truncate text-xs text-muted-foreground">
                        待切换 {store.pendingTemplateName} · r{store.pendingTemplateRevision}
                      </div>
                    ) : null}
                    {store.lastErrorMessage ? (
                      <div className="mt-0.5 truncate text-xs text-destructive">{store.lastErrorMessage}</div>
                    ) : null}
                  </div>
                  <Badge variant={store.readinessStatus === "ready" ? "default" : "outline"}>
                    {readinessLabel(store.readinessStatus)}
                  </Badge>
                  <Tooltip>
                    <TooltipTrigger
                      render={<Button type="button" variant="ghost" size="icon-sm" />}
                      onClick={() => setCredentialStore(store)}
                      aria-label={`管理 ${store.storeName} 的模型凭据`}
                    >
                      <KeyRoundIcon />
                    </TooltipTrigger>
                    <TooltipContent>模型凭据</TooltipContent>
                  </Tooltip>
                </div>
              ))}
              {filteredStores.length === 0 ? (
                <div className="py-16 text-center text-sm text-muted-foreground">没有符合条件的门店</div>
              ) : null}
            </div>
          </div>
        )}

        <DialogFooter className="mx-0 mb-0 rounded-none px-6">
          <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>关闭</Button>
          {canUpdate ? (
            <Button
              type="button"
              disabled={saving || !selectedProfile || selectedStoreIds.size === 0}
              onClick={() => void assign()}
            >
              {saving
                ? "提交中..."
                : confirming
                  ? `确认指派 ${selectedStoreIds.size} 家门店`
                  : `指派 ${selectedStoreIds.size} 家门店`}
            </Button>
          ) : null}
        </DialogFooter>
      </DialogContent>
      <StoreModelCredentialDialog
        open={Boolean(credentialStore && tenant)}
        tenantId={tenant?.id ?? 0}
        storeId={credentialStore?.storeId ?? 0}
        storeName={credentialStore?.storeName ?? ""}
        canUpdate={canUpdate}
        onOpenChange={(nextOpen) => {
          if (!nextOpen) setCredentialStore(null)
        }}
        onChanged={() => {
          if (!tenant) return
          void fetchStoreModelProfileAssignments(tenant.id).then(setData).catch(() => undefined)
        }}
      />
    </Dialog>
  )
}

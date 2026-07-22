"use client"

import { useEffect, useMemo, useState } from "react"
import { BrainCircuitIcon, SearchIcon } from "lucide-react"
import { toast } from "sonner"

import { OptionCombobox } from "@/components/option-combobox"
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
import {
  batchAssignStoreModelProfile,
  fetchStoreModelProfileAssignments,
  type StoreModelProfileAssignments,
} from "@/lib/api/admin"
import type { AdminTenant } from "@/lib/api/tenant"

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

  useEffect(() => {
    if (!open || !tenant) return
    let cancelled = false
    setLoading(true)
    setConfirming(false)
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
    setSelectedStoreIds((current) => {
      const next = new Set(current)
      if (selected) next.add(storeId)
      else next.delete(storeId)
      return next
    })
  }

  function toggleFiltered(selected: boolean) {
    setConfirming(false)
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

            <div className="min-h-0 flex-1 divide-y overflow-y-auto">
              {filteredStores.map((store) => (
                <label key={store.storeId} className="grid cursor-pointer gap-3 px-6 py-3 hover:bg-muted/40 md:grid-cols-[28px_minmax(180px,1fr)_minmax(200px,1fr)_120px] md:items-center">
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
                </label>
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
    </Dialog>
  )
}

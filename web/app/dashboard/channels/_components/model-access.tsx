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
import { fetchAIConfigsAll, type AIConfig } from "@/lib/api/admin"
import {
  fetchTenantAIModelAccess,
  updateTenantAIModelAccess,
  type AdminTenant,
  type TenantAIModelAccess,
} from "@/lib/api/tenant"
import { Status } from "@/lib/generated/enums"

type TenantModelAccessDialogProps = {
  open: boolean
  tenant: AdminTenant | null
  canUpdate: boolean
  onOpenChange: (open: boolean) => void
}

const numberFormatter = new Intl.NumberFormat("zh-CN")

export function TenantModelAccessDialog({
  open,
  tenant,
  canUpdate,
  onOpenChange,
}: TenantModelAccessDialogProps) {
  const [loading, setLoading] = useState(false)
  const [saving, setSaving] = useState(false)
  const [query, setQuery] = useState("")
  const [configs, setConfigs] = useState<AIConfig[]>([])
  const [access, setAccess] = useState<TenantAIModelAccess | null>(null)
  const [selectedIds, setSelectedIds] = useState<Set<number>>(new Set())
  const [defaults, setDefaults] = useState<Record<string, number>>({})

  useEffect(() => {
    if (!open || !tenant) return
    let cancelled = false
    setLoading(true)
    Promise.all([
      fetchAIConfigsAll({ status: Status.Ok }),
      fetchTenantAIModelAccess(tenant.id),
    ])
      .then(([nextConfigs, nextAccess]) => {
        if (cancelled) return
        setConfigs(nextConfigs)
        setAccess(nextAccess)
        setSelectedIds(new Set(nextAccess.grants.map((item) => item.aiConfigId)))
        setDefaults(
          Object.fromEntries(
            nextAccess.usages.map((item) => [item.usageCode, item.aiConfigId || 0])
          )
        )
      })
      .catch((error) => {
        if (!cancelled) {
          toast.error(error instanceof Error ? error.message : "读取模型授权失败")
          onOpenChange(false)
        }
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [open, onOpenChange, tenant])

  const grantByConfigId = useMemo(
    () => new Map(access?.grants.map((item) => [item.aiConfigId, item]) ?? []),
    [access]
  )

  const filteredGroups = useMemo(() => {
    const keyword = query.trim().toLowerCase()
    const groups = new Map<string, AIConfig[]>()
    for (const config of configs) {
      if (config.status !== Status.Ok) continue
      if (
        keyword &&
        ![config.name, config.modelName, config.provider, config.modelType]
          .join(" ")
          .toLowerCase()
          .includes(keyword)
      ) {
        continue
      }
      const list = groups.get(config.modelType) ?? []
      list.push(config)
      groups.set(config.modelType, list)
    }
    return [...groups.entries()]
  }, [configs, query])

  function toggleConfig(configId: number, checked: boolean) {
    const currentGrant = grantByConfigId.get(configId)
    if (!checked && currentGrant && currentGrant.assignedEmployeeCount > 0) {
      toast.error(
        `该模型仍分配给 ${currentGrant.assignedEmployeeCount} 个企微员工号，请先调整账号设置`
      )
      return
    }
    setSelectedIds((current) => {
      const next = new Set(current)
      if (checked) next.add(configId)
      else next.delete(configId)
      return next
    })
    if (!checked) {
      setDefaults((current) =>
        Object.fromEntries(
          Object.entries(current).map(([code, id]) => [code, id === configId ? 0 : id])
        )
      )
    }
  }

  async function save() {
    if (!tenant || !access || !canUpdate) return
    setSaving(true)
    try {
      const next = await updateTenantAIModelAccess({
        tenantId: tenant.id,
        grantedAiConfigIds: [...selectedIds],
        defaults: access.usages.map((item) => ({
          usageCode: item.usageCode,
          aiConfigId: defaults[item.usageCode] ?? 0,
        })),
      })
      setAccess(next)
      setSelectedIds(new Set(next.grants.map((item) => item.aiConfigId)))
      toast.success("模型授权已更新")
      onOpenChange(false)
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "保存模型授权失败")
    } finally {
      setSaving(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="flex max-h-[88vh] max-w-5xl flex-col gap-0 overflow-hidden p-0">
        <DialogHeader className="border-b px-6 py-5">
          <DialogTitle className="flex items-center gap-2 text-lg">
            <BrainCircuitIcon className="size-5" />
            模型授权
          </DialogTitle>
          <DialogDescription>
            {tenant?.shortName ?? "接入公司"}
          </DialogDescription>
        </DialogHeader>

        {loading || !access ? (
          <div className="flex min-h-80 items-center justify-center text-sm text-muted-foreground">
            正在读取模型授权...
          </div>
        ) : (
          <div className="grid min-h-0 flex-1 overflow-hidden lg:grid-cols-[1.2fr_0.8fr]">
            <section className="flex min-h-0 flex-col border-b lg:border-r lg:border-b-0">
              <div className="border-b px-5 py-4">
                <div className="mb-3 flex items-center justify-between gap-3">
                  <h3 className="text-sm font-semibold">授权模型</h3>
                  <Badge variant="secondary">{selectedIds.size} 个</Badge>
                </div>
                <div className="relative">
                  <SearchIcon className="absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" />
                  <Input
                    value={query}
                    onChange={(event) => setQuery(event.target.value)}
                    placeholder="搜索名称、模型或供应商"
                    className="pl-9"
                  />
                </div>
              </div>
              <div className="min-h-0 flex-1 overflow-y-auto px-5 py-3">
                {filteredGroups.map(([modelType, items]) => (
                  <div key={modelType} className="mb-5 last:mb-0">
                    <div className="mb-1 text-xs font-medium text-muted-foreground uppercase">
                      {modelType}
                    </div>
                    <div className="divide-y rounded-md border">
                      {items.map((config) => {
                        const grant = grantByConfigId.get(config.id)
                        const checked = selectedIds.has(config.id)
                        const locked = Boolean(grant && grant.assignedEmployeeCount > 0)
                        return (
                          <label
                            key={config.id}
                            className="flex cursor-pointer items-start gap-3 px-3 py-3 hover:bg-muted/40"
                          >
                            <Checkbox
                              checked={checked}
                              disabled={!canUpdate || (checked && locked)}
                              onCheckedChange={(value) => toggleConfig(config.id, value === true)}
                              aria-label={`授权 ${config.name}`}
                            />
                            <div className="min-w-0 flex-1">
                              <div className="flex flex-wrap items-center gap-2 text-sm font-medium">
                                <span>{config.name}</span>
                                {locked ? <Badge variant="outline">使用中</Badge> : null}
                              </div>
                              <div className="mt-1 truncate text-xs text-muted-foreground">
                                {config.modelName} · {config.provider}
                              </div>
                              {grant ? (
                                <div className="mt-1 text-xs text-muted-foreground">
                                  {numberFormatter.format(grant.requestCount)} 次 · {numberFormatter.format(grant.promptTokens + grant.completionTokens)} tokens
                                </div>
                              ) : null}
                            </div>
                          </label>
                        )
                      })}
                    </div>
                  </div>
                ))}
                {filteredGroups.length === 0 ? (
                  <div className="py-12 text-center text-sm text-muted-foreground">
                    没有匹配的模型接入
                  </div>
                ) : null}
              </div>
            </section>

            <section className="min-h-0 overflow-y-auto px-5 py-4">
              <div className="mb-4 flex items-center justify-between">
                <h3 className="text-sm font-semibold">用途默认模型</h3>
                <span className="text-xs text-muted-foreground">未指定时按授权池顺序</span>
              </div>
              <div className="space-y-4">
                {access.usages.map((usage) => {
                  const options = configs
                    .filter(
                      (config) =>
                        selectedIds.has(config.id) &&
                        config.status === Status.Ok &&
                        config.modelType === usage.expectedModelType
                    )
                    .map((config) => ({
                      value: String(config.id),
                      label: `${config.name} · ${config.modelName}`,
                    }))
                  return (
                    <div key={usage.usageCode}>
                      <div className="mb-1.5 flex items-center justify-between gap-3">
                        <label className="text-sm font-medium">{usage.usageName}</label>
                        <span className="font-mono text-[11px] text-muted-foreground">
                          {usage.expectedModelType}
                        </span>
                      </div>
                      <OptionCombobox
                        value={String(defaults[usage.usageCode] ?? 0)}
                        onChange={(value) =>
                          setDefaults((current) => ({
                            ...current,
                            [usage.usageCode]: Number(value),
                          }))
                        }
                        options={[{ value: "0", label: "自动选择" }, ...options]}
                        disabled={!canUpdate}
                        placeholder="选择默认模型"
                      />
                      <div className="mt-1 text-xs text-muted-foreground">
                        当前生效：{usage.effectiveModelName || "暂无可用模型"}
                      </div>
                    </div>
                  )
                })}
              </div>
            </section>
          </div>
        )}

        <DialogFooter className="border-t px-6 py-4">
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            取消
          </Button>
          {canUpdate ? (
            <Button disabled={loading || saving || !access} onClick={() => void save()}>
              {saving ? "保存中..." : "保存授权"}
            </Button>
          ) : null}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

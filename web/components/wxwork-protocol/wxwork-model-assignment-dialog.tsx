"use client"

import { useEffect, useState } from "react"
import { BrainCircuitIcon } from "lucide-react"
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
import {
  fetchWxWorkModelAssignments,
  updateWxWorkModelAssignments,
  type WxWorkProtocolInstance,
} from "@/lib/api/admin"
import type { TenantAIModelAccess } from "@/lib/api/tenant"
import { repairMojibakeText } from "@/lib/utils"

type WxWorkModelAssignmentDialogProps = {
  open: boolean
  instance: WxWorkProtocolInstance | null
  canUpdate: boolean
  onOpenChange: (open: boolean) => void
}

export function WxWorkModelAssignmentDialog({
  open,
  instance,
  canUpdate,
  onOpenChange,
}: WxWorkModelAssignmentDialogProps) {
  const { session } = useAuth()
  const tenantId = session?.activeTenantId ?? 0
  const [loading, setLoading] = useState(false)
  const [saving, setSaving] = useState(false)
  const [access, setAccess] = useState<TenantAIModelAccess | null>(null)
  const [assignments, setAssignments] = useState<Record<string, number>>({})

  useEffect(() => {
    if (!open || !instance || tenantId <= 0) return
    let cancelled = false
    setLoading(true)
    fetchWxWorkModelAssignments(tenantId, instance.id)
      .then((next) => {
        if (cancelled) return
        setAccess(next)
        setAssignments(
          Object.fromEntries(
            next.usages.map((usage) => [usage.usageCode, usage.aiConfigId || 0])
          )
        )
      })
      .catch((error) => {
        if (!cancelled) {
          toast.error(error instanceof Error ? error.message : "读取账号模型分配失败")
          onOpenChange(false)
        }
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [instance, onOpenChange, open, tenantId])

  async function save() {
    if (!instance || !access || tenantId <= 0 || !canUpdate) return
    setSaving(true)
    try {
      await updateWxWorkModelAssignments({
        tenantId,
        wxWorkInstanceId: instance.id,
        assignments: access.usages.map((usage) => ({
          usageCode: usage.usageCode,
          aiConfigId: assignments[usage.usageCode] ?? 0,
        })),
      })
      toast.success("账号模型分配已更新")
      onOpenChange(false)
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "保存账号模型分配失败")
    } finally {
      setSaving(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-2xl p-0">
        <DialogHeader className="border-b px-6 py-5">
          <DialogTitle className="flex items-center gap-2 text-lg">
            <BrainCircuitIcon className="size-5" />
            账号模型分配
          </DialogTitle>
          <DialogDescription>
            {instance
              ? repairMojibakeText(instance.employeeName) || instance.guid
              : "企微员工号"}
          </DialogDescription>
        </DialogHeader>

        {loading || !access ? (
          <div className="flex min-h-64 items-center justify-center text-sm text-muted-foreground">
            正在读取授权模型...
          </div>
        ) : (
          <div className="max-h-[60vh] space-y-5 overflow-y-auto px-6 py-5">
            <div className="flex items-center justify-between border-b pb-4">
              <span className="text-sm font-medium">租户授权池</span>
              <Badge variant="secondary">{access.grants.length} 个模型</Badge>
            </div>
            {access.usages.map((usage) => {
              const options = access.grants
                .filter((grant) => grant.modelType === usage.expectedModelType)
                .map((grant) => ({
                  value: String(grant.aiConfigId),
                  label: `${grant.name} · ${grant.modelName}`,
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
                    value={String(assignments[usage.usageCode] ?? 0)}
                    onChange={(value) =>
                      setAssignments((current) => ({
                        ...current,
                        [usage.usageCode]: Number(value),
                      }))
                    }
                    options={[
                      { value: "0", label: "使用租户默认" },
                      ...options,
                    ]}
                    disabled={!canUpdate}
                    placeholder="选择授权模型"
                  />
                  <div className="mt-1 text-xs text-muted-foreground">
                    当前生效：{usage.effectiveModelName || "暂无可用模型"}
                  </div>
                </div>
              )
            })}
          </div>
        )}

        <DialogFooter className="border-t px-6 py-4">
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            取消
          </Button>
          {canUpdate ? (
            <Button disabled={loading || saving || !access} onClick={() => void save()}>
              {saving ? "保存中..." : "保存分配"}
            </Button>
          ) : null}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

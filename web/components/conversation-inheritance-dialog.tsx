"use client"

import { useEffect, useMemo, useState } from "react"
import { ArrowRightLeftIcon, LoaderCircleIcon } from "lucide-react"
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
import { Textarea } from "@/components/ui/textarea"
import {
  batchInheritStoreConversations,
  inheritStoreConversation,
  previewStoreConversationInheritance,
  type StoreConversationInheritancePreview,
} from "@/lib/api/agent"
import {
  fetchWxWorkProtocolInstances,
  type WxWorkProtocolInstance,
} from "@/lib/api/admin"
import { Status } from "@/lib/generated/enums"
import { formatDateTime, repairMojibakeText } from "@/lib/utils"

type ConversationInheritanceDialogProps = {
  open: boolean
  sourceStoreId: number
  sourceStoreStaffBindingId: number
  conversationId?: number
  onOpenChange: (open: boolean) => void
  onSuccess: () => Promise<void> | void
}

export function ConversationInheritanceDialog({
  open,
  sourceStoreId,
  sourceStoreStaffBindingId,
  conversationId,
  onOpenChange,
  onSuccess,
}: ConversationInheritanceDialogProps) {
  const batchMode = !conversationId
  const [instances, setInstances] = useState<WxWorkProtocolInstance[]>([])
  const [loading, setLoading] = useState(false)
  const [targetInstanceId, setTargetInstanceId] = useState(0)
  const [reason, setReason] = useState("")
  const [preview, setPreview] =
    useState<StoreConversationInheritancePreview | null>(null)
  const [selectedIds, setSelectedIds] = useState<number[]>([])
  const [previewing, setPreviewing] = useState(false)
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    if (!open) return
    let cancelled = false
    setTargetInstanceId(0)
    setReason("")
    setPreview(null)
    setSelectedIds([])
    setLoading(true)
    void fetchWxWorkProtocolInstances({ status: Status.Ok, limit: 200 })
      .then((result) => {
        if (!cancelled) setInstances(result.results ?? [])
      })
      .catch((error) => {
        if (!cancelled) {
          setInstances([])
          toast.error(error instanceof Error ? error.message : "目标企微实例加载失败")
        }
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [open])

  const targets = useMemo(
    () =>
      instances.filter(
        (item) =>
          item.status === Status.Ok &&
          item.storeId === sourceStoreId &&
          item.storeStaffBindingId > 0 &&
          item.storeStaffBindingId !== sourceStoreStaffBindingId &&
          item.healthStatus.trim().toLowerCase() === "online" &&
          !item.protocolExpired
      ),
    [instances, sourceStoreId, sourceStoreStaffBindingId]
  )
  const target = targets.find((item) => item.id === targetInstanceId) ?? null
  const targetOptions = targets.map((item) => ({
    value: String(item.id),
    label:
      [
        repairMojibakeText(item.storeStaffUserName),
        repairMojibakeText(item.employeeName) || item.employeeUserId,
      ]
        .filter(Boolean)
        .join(" · ") || `企微实例 #${item.id}`,
  }))

  async function loadPreview() {
    if (!target || previewing) return
    setPreviewing(true)
    try {
      const result = await previewStoreConversationInheritance({
        sourceStoreStaffBindingId,
        targetStoreStaffBindingId: target.storeStaffBindingId,
        targetWxWorkInstanceId: target.id,
      })
      setPreview(result)
      setSelectedIds(
        (result.items ?? [])
          .filter((item) => item.eligible)
          .map((item) => item.conversationId)
      )
    } catch (error) {
      setPreview(null)
      setSelectedIds([])
      toast.error(error instanceof Error ? error.message : "会话交接预览失败")
    } finally {
      setPreviewing(false)
    }
  }

  async function submit() {
    if (!target || !reason.trim() || saving) return
    if (batchMode && (!preview || selectedIds.length === 0)) return
    setSaving(true)
    try {
      if (batchMode && preview) {
        const result = await batchInheritStoreConversations({
          sourceStoreStaffBindingId,
          targetStoreStaffBindingId: target.storeStaffBindingId,
          targetWxWorkInstanceId: target.id,
          conversationIds: selectedIds,
          previewVersion: preview.previewVersion,
          reason: reason.trim(),
        })
        toast.success(
          `已完成 ${result.inheritedCount} 条会话交接，其中 ${result.createdCount} 条创建接续会话、${result.linkedCount} 条接续已有会话`,
        )
      } else {
        await inheritStoreConversation({
          conversationId: conversationId ?? 0,
          targetStoreStaffBindingId: target.storeStaffBindingId,
          targetWxWorkInstanceId: target.id,
          reason: reason.trim(),
        })
        toast.success("会话已继承到目标门店员工号")
      }
      onOpenChange(false)
      await onSuccess()
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "会话继承失败")
    } finally {
      setSaving(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={(nextOpen) => !saving && onOpenChange(nextOpen)}>
      <DialogContent className="max-h-[86vh] overflow-y-auto sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <ArrowRightLeftIcon className="size-4" />
            {batchMode ? "批量会话交接" : "安排会话继承"}
          </DialogTitle>
          <DialogDescription>
            仅可交接到同一门店内在线且未被替换的企微实例；目标已有该客户会话时保留两边原始消息并建立接续关系。
          </DialogDescription>
        </DialogHeader>

        <div className="space-y-4">
          <div className="space-y-2">
            <label className="text-sm font-medium">目标门店员工号与企微实例</label>
            <OptionCombobox
              value={targetInstanceId > 0 ? String(targetInstanceId) : ""}
              options={targetOptions}
              placeholder={loading ? "正在加载实例" : "选择目标实例"}
              searchPlaceholder="搜索门店员工号或企微账号"
              emptyText="没有符合条件的在线目标实例"
              disabled={loading || saving}
              onChange={(value) => {
                setTargetInstanceId(Number(value) || 0)
                setPreview(null)
                setSelectedIds([])
              }}
            />
          </div>

          {batchMode ? (
            <div className="space-y-3">
              <Button
                type="button"
                variant="outline"
                disabled={!target || previewing || saving}
                onClick={() => void loadPreview()}
              >
                {previewing ? <LoaderCircleIcon className="animate-spin" /> : null}
                预览可交接会话
              </Button>
              {preview ? (
                <div className="space-y-2">
                  <div className="flex flex-wrap gap-2">
                    <Badge variant="secondary">可交接 {preview.eligibleCount}</Badge>
                    {preview.linkedExistingCount > 0 ? (
                      <Badge variant="outline">接续已有会话 {preview.linkedExistingCount}</Badge>
                    ) : null}
                    <Badge variant="outline">冲突 {preview.conflictCount}</Badge>
                  </div>
                  <div className="max-h-64 divide-y overflow-y-auto rounded-md border">
                    {(preview.items ?? []).map((item) => {
                      const checked = selectedIds.includes(item.conversationId)
                      return (
                        <label
                          key={item.conversationId}
                          className="flex items-start gap-3 px-3 py-2.5 text-sm"
                        >
                          <Checkbox
                            checked={checked}
                            disabled={!item.eligible || saving}
                            onCheckedChange={(nextChecked) =>
                              setSelectedIds((current) =>
                                nextChecked
                                  ? [...new Set([...current, item.conversationId])]
                                  : current.filter((id) => id !== item.conversationId)
                              )
                            }
                          />
                          <span className="min-w-0 flex-1">
                            <span className="block truncate font-medium">
                              {repairMojibakeText(item.customerName) ||
                                `客户 #${item.customerId}`}
                            </span>
                            <span className="mt-0.5 block text-xs text-muted-foreground">
                              {item.conflictReason ||
                                (item.resolutionMode === "link_existing"
                                  ? `将作为历史接续到会话 #${item.targetConversationId}`
                                  : `会话 #${item.conversationId} · ${formatDateTime(item.lastMessageAt)}`)}
                            </span>
                          </span>
                        </label>
                      )
                    })}
                  </div>
                </div>
              ) : null}
            </div>
          ) : null}

          <div className="space-y-2">
            <label htmlFor="conversation-inheritance-reason" className="text-sm font-medium">
              交接原因
            </label>
            <Textarea
              id="conversation-inheritance-reason"
              value={reason}
              maxLength={255}
              rows={4}
              disabled={saving}
              placeholder="填写离职、换号或业务交接原因"
              onChange={(event) => setReason(event.target.value)}
            />
          </div>
        </div>

        <DialogFooter>
          <Button type="button" variant="outline" disabled={saving} onClick={() => onOpenChange(false)}>
            取消
          </Button>
          <Button
            type="button"
            disabled={
              !target ||
              !reason.trim() ||
              saving ||
              (batchMode && (!preview || selectedIds.length === 0))
            }
            onClick={() => void submit()}
          >
            {saving ? <LoaderCircleIcon className="animate-spin" /> : null}
            {batchMode ? `确认交接 ${selectedIds.length} 条` : "确认继承"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

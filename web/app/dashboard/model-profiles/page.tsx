"use client"

import { useCallback, useEffect, useMemo, useState } from "react"
import {
  CheckCircle2Icon,
  CircleAlertIcon,
  CopyPlusIcon,
  Edit3Icon,
  PlusIcon,
  RefreshCwIcon,
  RocketIcon,
  ServerCogIcon,
} from "lucide-react"
import { toast } from "sonner"

import { useAuth } from "@/components/auth-provider"
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
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import {
  createModelProfile,
  fetchModelProfileCatalog,
  publishModelProfile,
  updateModelProfile,
  validateModelProfile,
  type ModelProfileCatalog,
  type ModelProfileTemplate,
  type ModelProfileValidation,
} from "@/lib/api/admin"
import { formatDateTime } from "@/lib/utils"
import { EditDialog, type ModelProfileFormValues } from "./_components/edit"

const statusLabels: Record<ModelProfileTemplate["status"], string> = {
  draft: "草稿",
  candidate: "候选",
  active: "生效",
  retired: "已退役",
  disabled: "已停用",
}

function statusVariant(status: ModelProfileTemplate["status"]) {
  if (status === "active") return "default" as const
  if (status === "candidate") return "secondary" as const
  return "outline" as const
}

function slotLimit(profile: ModelProfileTemplate, index: number) {
  const slot = profile.slots[index]
  if (slot.modelType === "embedding") return `${slot.dimension || 0} 维`
  if (slot.maxContextTokens > 0 || slot.maxOutputTokens > 0) {
    return `${slot.maxContextTokens || 0} / ${slot.maxOutputTokens || 0}`
  }
  return `${slot.timeoutMs} ms`
}

export default function DashboardModelProfilesPage() {
  const { session } = useAuth()
  const permissions = useMemo(
    () => new Set(session?.permissions ?? []),
    [session?.permissions],
  )
  const canUpdate =
    Boolean(session?.isPlatformAccount) && permissions.has("aiConfig.update")
  const [catalog, setCatalog] = useState<ModelProfileCatalog | null>(null)
  const [selectedId, setSelectedId] = useState(0)
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [editorOpen, setEditorOpen] = useState(false)
  const [editingProfile, setEditingProfile] = useState<ModelProfileTemplate | null>(null)
  const [validation, setValidation] = useState<ModelProfileValidation | null>(null)
  const [publishTarget, setPublishTarget] = useState<ModelProfileTemplate | null>(null)

  const load = useCallback(async (preferredId = 0) => {
    setLoading(true)
    try {
      const next = await fetchModelProfileCatalog()
      setCatalog(next)
      setSelectedId((current) => {
        const requested = preferredId || current
        return next.profiles.some((item) => item.id === requested)
          ? requested
          : (next.profiles[0]?.id ?? 0)
      })
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "读取模型方案失败")
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  const selected = useMemo(
    () => catalog?.profiles.find((item) => item.id === selectedId) ?? null,
    [catalog, selectedId],
  )

  async function submitProfile(values: ModelProfileFormValues) {
    setSaving(true)
    try {
      const saved = editingProfile
        ? await updateModelProfile({
            id: editingProfile.id,
            name: values.name.trim(),
            description: values.description.trim(),
            gatewayBaseUrl: values.gatewayBaseUrl.trim(),
            slots: values.slots,
          })
        : await createModelProfile({
            code: values.code.trim(),
            name: values.name.trim(),
            description: values.description.trim(),
            gatewayBaseUrl: values.gatewayBaseUrl.trim(),
            slots: values.slots,
          })
      toast.success(editingProfile ? "草稿已更新" : "模型方案已创建")
      setEditorOpen(false)
      setValidation(null)
      await load(saved.id)
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "保存模型方案失败")
    } finally {
      setSaving(false)
    }
  }

  async function createRevision() {
    if (!selected) return
    setSaving(true)
    try {
      const created = await createModelProfile({ sourceTemplateId: selected.id })
      toast.success(`已创建版本 ${created.revision}`)
      setValidation(null)
      await load(created.id)
      setEditingProfile(created)
      setEditorOpen(true)
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "创建新版本失败")
    } finally {
      setSaving(false)
    }
  }

  async function runValidation() {
    if (!selected) return
    try {
      const result = await validateModelProfile(selected.id)
      setValidation(result)
      if (result.status === "passed") toast.success("九槽结构校验通过")
      else toast.error(`结构校验发现 ${result.issues.length} 项问题`)
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "结构校验失败")
    }
  }

  async function confirmPublish() {
    if (!publishTarget) return
    setSaving(true)
    try {
      const published = await publishModelProfile(publishTarget.id, publishTarget.revision)
      toast.success(`版本 ${published.revision} 已提交为候选`)
      setPublishTarget(null)
      setValidation(null)
      await load(published.id)
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "候选发布失败")
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="min-w-0">
      <header className="flex flex-col gap-4 border-b pb-5 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <h1 className="text-xl font-semibold">模型配置</h1>
          <div className="mt-1 flex items-center gap-2 text-sm text-muted-foreground">
            <ServerCogIcon className="size-4" />
            <span>NewAPI · 九槽模型方案</span>
          </div>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <Button type="button" variant="outline" size="sm" onClick={() => void load()} disabled={loading}>
            <RefreshCwIcon className={loading ? "animate-spin" : ""} />
            刷新
          </Button>
          {canUpdate ? (
            <Button
              type="button"
              size="sm"
              disabled={loading || (catalog?.requiredSlots.length ?? 0) !== 9}
              onClick={() => {
                setEditingProfile(null)
                setEditorOpen(true)
              }}
            >
              <PlusIcon />
              新建方案
            </Button>
          ) : null}
        </div>
      </header>

      <div className="grid min-h-[620px] lg:grid-cols-[300px_minmax(0,1fr)]">
        <aside className="border-b py-4 lg:border-r lg:border-b-0 lg:pr-4">
          <div className="mb-3 flex items-center justify-between px-1">
            <span className="text-sm font-medium">方案版本</span>
            <Badge variant="secondary">{catalog?.profiles.length ?? 0}</Badge>
          </div>
          <div className="max-h-[680px] divide-y overflow-y-auto border-y">
            {catalog?.profiles.map((item) => (
              <button
                key={item.id}
                type="button"
                className={`w-full px-3 py-3 text-left transition-colors hover:bg-muted/50 ${
                  item.id === selectedId ? "bg-muted" : ""
                }`}
                onClick={() => {
                  setSelectedId(item.id)
                  setValidation(null)
                }}
              >
                <div className="flex items-center justify-between gap-2">
                  <span className="truncate text-sm font-medium">{item.name}</span>
                  <Badge variant={statusVariant(item.status)}>{statusLabels[item.status]}</Badge>
                </div>
                <div className="mt-1 flex items-center justify-between gap-2 text-xs text-muted-foreground">
                  <span className="truncate font-mono">{item.code}</span>
                  <span>r{item.revision}</span>
                </div>
              </button>
            ))}
            {!loading && !catalog?.profiles.length ? (
              <div className="px-3 py-12 text-center text-sm text-muted-foreground">暂无模型方案</div>
            ) : null}
          </div>
        </aside>

        <main className="min-w-0 py-4 lg:pl-5">
          {selected ? (
            <>
              <div className="flex flex-col gap-4 border-b pb-4 xl:flex-row xl:items-start xl:justify-between">
                <div className="min-w-0">
                  <div className="flex flex-wrap items-center gap-2">
                    <h2 className="text-lg font-semibold">{selected.name}</h2>
                    <Badge variant={statusVariant(selected.status)}>{statusLabels[selected.status]}</Badge>
                    <Badge variant="outline">版本 {selected.revision}</Badge>
                  </div>
                  <p className="mt-1 max-w-3xl text-sm text-muted-foreground">{selected.description || "-"}</p>
                  <div className="mt-3 grid gap-x-8 gap-y-1 text-xs text-muted-foreground sm:grid-cols-2">
                    <span className="truncate font-mono">{selected.gatewayBaseUrl || "网关未配置"}</span>
                    <span>更新于 {formatDateTime(selected.updatedAt)}</span>
                  </div>
                </div>
                {canUpdate ? (
                  <div className="flex flex-wrap items-center gap-2">
                    {selected.status === "draft" ? (
                    <Button
                      type="button"
                      variant="outline"
                      size="sm"
                      onClick={() => {
                        setEditingProfile(selected)
                        setEditorOpen(true)
                      }}
                    >
                      <Edit3Icon />
                      编辑
                    </Button>
                    ) : (
                      <Button type="button" variant="outline" size="sm" onClick={() => void createRevision()} disabled={saving}>
                        <CopyPlusIcon />
                        新版本
                      </Button>
                    )}
                    <Button type="button" variant="outline" size="sm" onClick={() => void runValidation()}>
                      <CheckCircle2Icon />
                      结构校验
                    </Button>
                    {selected.status === "draft" ? (
                      <Button type="button" size="sm" onClick={() => setPublishTarget(selected)}>
                        <RocketIcon />
                        提交候选
                      </Button>
                    ) : null}
                  </div>
                ) : null}
              </div>

              <div className="overflow-x-auto py-4">
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>用途</TableHead>
                      <TableHead>模型</TableHead>
                      <TableHead>类型</TableHead>
                      <TableHead>API</TableHead>
                      <TableHead>上限 / 超时</TableHead>
                      <TableHead>状态</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {selected.slots.map((slot, index) => (
                      <TableRow key={slot.usageCode}>
                        <TableCell>
                          <div className="font-medium">{slot.displayName}</div>
                          <div className="mt-0.5 font-mono text-xs text-muted-foreground">{slot.usageCode}</div>
                        </TableCell>
                        <TableCell className="max-w-64 truncate font-mono text-xs">
                          {slot.modelName || "未配置"}
                        </TableCell>
                        <TableCell><Badge variant="secondary">{slot.modelType}</Badge></TableCell>
                        <TableCell className="text-xs">{slot.apiMode}</TableCell>
                        <TableCell className="text-xs tabular-nums">{slotLimit(selected, index)}</TableCell>
                        <TableCell>
                          <Badge
                            variant={
                              !slot.enabled
                                ? "destructive"
                                : slot.modelName.trim()
                                  ? "secondary"
                                  : "outline"
                            }
                          >
                            {!slot.enabled
                              ? "已停用"
                              : slot.modelName.trim()
                                ? "已填写模型"
                                : "待填写模型"}
                          </Badge>
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </div>

              {validation ? (
                <section className="border-t pt-4">
                  <div className="mb-3 flex items-center gap-2 text-sm font-medium">
                    {validation.status === "passed" ? (
                      <CheckCircle2Icon className="size-4 text-emerald-600" />
                    ) : (
                      <CircleAlertIcon className="size-4 text-destructive" />
                    )}
                    <span>{validation.status === "passed" ? "结构校验通过" : "结构校验未通过"}</span>
                  </div>
                  {validation.issues.length ? (
                    <div className="divide-y border-y">
                      {validation.issues.map((issue, index) => (
                        <div key={`${issue.usageCode}-${index}`} className="flex gap-3 py-2 text-sm">
                          <span className="w-36 shrink-0 font-mono text-xs text-muted-foreground">{issue.usageCode || "profile"}</span>
                          <span>{issue.message}</span>
                        </div>
                      ))}
                    </div>
                  ) : null}
                </section>
              ) : null}
            </>
          ) : (
            <div className="flex min-h-96 items-center justify-center text-sm text-muted-foreground">
              {loading ? "正在读取模型方案..." : "请选择或新建模型方案"}
            </div>
          )}
        </main>
      </div>

      <EditDialog
        open={editorOpen}
        saving={saving}
        profile={editingProfile}
        requiredSlots={catalog?.requiredSlots ?? []}
        onOpenChange={setEditorOpen}
        onSubmit={submitProfile}
      />

      <Dialog open={Boolean(publishTarget)} onOpenChange={(open) => !open && setPublishTarget(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>提交候选版本</DialogTitle>
            <DialogDescription>
              {publishTarget ? `${publishTarget.name} · 版本 ${publishTarget.revision}` : ""}
            </DialogDescription>
          </DialogHeader>
          <div className="border-y py-3 text-sm">
            九槽结构通过后进入候选状态。门店继续使用原生效版本，直至凭据和就绪校验完成。
          </div>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => setPublishTarget(null)}>取消</Button>
            <Button type="button" onClick={() => void confirmPublish()} disabled={saving}>
              {saving ? "提交中..." : `确认版本 ${publishTarget?.revision ?? ""}`}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}

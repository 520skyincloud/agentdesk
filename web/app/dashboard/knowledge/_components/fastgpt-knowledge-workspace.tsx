"use client"

import { useCallback, useEffect, useState } from "react"
import { CheckCircle2Icon, CloudIcon, CopyIcon, DatabaseIcon, Link2Icon, Loader2Icon, RefreshCwIcon, Trash2Icon } from "lucide-react"
import { toast } from "sonner"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  activateFastGPTKnowledgeBase,
  deleteFastGPTDataset,
  fetchFastGPTStoreReadiness,
  resyncFastGPTStoreProfile,
  type FastGPTStoreReadiness,
  type KnowledgeBase,
} from "@/lib/api/admin"
import { formatDateTime } from "@/lib/utils"
import { FastGPTFilePanel } from "./fastgpt-file-panel"
import { KnowledgeResourcePanel } from "./knowledge-resource-panel"

type FastGPTKnowledgeWorkspaceProps = {
  knowledgeBase: KnowledgeBase
  canUpdate: boolean
  canDelete: boolean
}

export function FastGPTKnowledgeWorkspace({ knowledgeBase, canUpdate, canDelete }: FastGPTKnowledgeWorkspaceProps) {
  const [readiness, setReadiness] = useState<FastGPTStoreReadiness | null>(null)
  const [loadingReadiness, setLoadingReadiness] = useState(false)
  const [activating, setActivating] = useState(false)
  const [resyncing, setResyncing] = useState(false)
  const [deleteConfirmation, setDeleteConfirmation] = useState("")
  const [deletingDataset, setDeletingDataset] = useState(false)
  const [showDeleteConfirmation, setShowDeleteConfirmation] = useState(false)

  const loadReadiness = useCallback(async () => {
    if (knowledgeBase.storeId <= 0) return
    setLoadingReadiness(true)
    try {
      setReadiness(await fetchFastGPTStoreReadiness(knowledgeBase.storeId))
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "加载 FastGPT 就绪状态失败")
    } finally {
      setLoadingReadiness(false)
    }
  }, [knowledgeBase.storeId])

  useEffect(() => {
    void loadReadiness()
  }, [loadReadiness])

  async function activate() {
    if (!canUpdate) return
    if (knowledgeBase.storeId <= 0) {
      toast.error("知识库尚未关联门店")
      return
    }
    setActivating(true)
    try {
      await activateFastGPTKnowledgeBase(knowledgeBase.storeId, knowledgeBase.id)
      await loadReadiness()
      toast.success("已设为该门店当前知识库")
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "设置当前知识库失败")
    } finally {
      setActivating(false)
    }
  }

  async function resyncProfile() {
    if (!canUpdate || knowledgeBase.storeId <= 0) return
    setResyncing(true)
    try {
      await resyncFastGPTStoreProfile(knowledgeBase.storeId)
      await loadReadiness()
      toast.success("模型同步任务已进入队列")
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "模型重同步失败")
    } finally {
      setResyncing(false)
    }
  }

  async function copyDatasetID() {
    try {
      await navigator.clipboard.writeText(knowledgeBase.datasetId)
      toast.success("数据集 ID 已复制")
    } catch {
      toast.error("复制失败，请手动复制")
    }
  }

  async function removeDataset() {
    if (!canDelete) return
    setDeletingDataset(true)
    try {
      await deleteFastGPTDataset(knowledgeBase.id, deleteConfirmation)
      toast.success("FastGPT 数据集已删除，门店当前知识库已解除绑定")
      window.location.assign("/dashboard/knowledge")
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "删除 FastGPT 数据集失败")
    } finally {
      setDeletingDataset(false)
    }
  }

  const profileLabel = readiness?.modelProfileName.trim() || knowledgeBase.fastgptProfileName.trim() || "未同步"
  const isCurrentKnowledgeBase = readiness?.knowledgeBaseId === knowledgeBase.id
  const readinessStatus = readiness?.readinessStatus || "unconfigured"

  return (
    <div className="h-full min-h-0 overflow-y-auto bg-[#f8fbff] p-5 lg:p-6">
      <div className="mx-auto flex max-w-[90rem] flex-col gap-5">
        <section className="border border-[#dbe7f6] bg-white px-5 py-5 shadow-sm">
          <div className="flex flex-wrap items-start justify-between gap-4">
            <div className="min-w-0">
              <div className="flex items-center gap-2 text-sm text-[#58739a]">
                <CloudIcon className="size-4 text-primary" />
                门店 FastGPT 云端知识库
              </div>
              <h1 className="mt-2 truncate text-xl font-semibold tracking-normal text-foreground">{knowledgeBase.datasetName || knowledgeBase.name}</h1>
              <div className="mt-2 flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
                <span>所属门店：{knowledgeBase.storeId > 0 ? knowledgeBase.storeId : "未关联"}</span>
                <span className="text-[#b6c5d8]">/</span>
                <span>创建于 {formatDateTime(knowledgeBase.createdAt)}</span>
              </div>
            </div>
            <div className="flex items-center gap-2">
              <Badge className="border border-emerald-200 bg-emerald-50 text-emerald-700 hover:bg-emerald-50">
                <CheckCircle2Icon className="mr-1 size-3.5" />
                数据集已连接
              </Badge>
              {canDelete ? <Button variant="outline" size="sm" className="border-destructive/40 text-destructive hover:bg-destructive hover:text-destructive-foreground" onClick={() => setShowDeleteConfirmation((value) => !value)}>
                <Trash2Icon className="size-3.5" />
                删除知识库
              </Button> : null}
            </div>
          </div>
          <div className="mt-5 flex flex-wrap items-center gap-2 rounded-md border border-[#e2eaf5] bg-[#f8fbff] px-3 py-2 text-xs">
            <DatabaseIcon className="size-3.5 shrink-0 text-[#5d7fa9]" />
            <span className="text-muted-foreground">FastGPT 数据集 ID</span>
            <code className="min-w-0 flex-1 break-all font-mono text-foreground">{knowledgeBase.datasetId}</code>
            <Button variant="ghost" size="icon" className="size-7" onClick={() => void copyDatasetID()} aria-label="复制数据集 ID">
              <CopyIcon className="size-3.5" />
            </Button>
          </div>
          <div className="mt-3 rounded-md border border-[#e2eaf5] bg-[#f8fbff] px-3 py-2 text-xs">
            <div className="font-medium text-foreground">当前模型方案：{profileLabel}</div>
            <div className="mt-1 text-muted-foreground">
              方案版本 {readiness?.appliedProfileRevision || "未应用"} / 目标 {readiness?.targetProfileRevision || "未配置"}
              <span className="px-1.5">·</span>
              凭据版本 {readiness?.appliedCredentialRevision || "未应用"} / 目标 {readiness?.targetCredentialRevision || "未配置"}
            </div>
          </div>
          {canDelete && showDeleteConfirmation ? (
            <div className="mt-4 border border-destructive/30 bg-destructive/5 p-4">
              <div className="text-sm font-medium text-destructive">永久删除这个知识库</div>
              <p className="mt-1 text-xs leading-5 text-muted-foreground">
				这会真实删除 FastGPT 中的数据集及其中的文件，并解除门店及其企微、会话路由投影绑定。该操作不可恢复。
              </p>
              <div className="mt-3 flex flex-col gap-2 sm:flex-row">
                <Input
                  value={deleteConfirmation}
                  onChange={(event) => setDeleteConfirmation(event.target.value)}
                  placeholder={`输入“${knowledgeBase.name}”确认删除`}
                  aria-label="知识库名称确认"
                />
                <Button
                  variant="destructive"
                  disabled={deletingDataset || deleteConfirmation.trim() !== knowledgeBase.name.trim()}
                  onClick={() => void removeDataset()}
                >
                  {deletingDataset ? <Loader2Icon className="size-4 animate-spin" /> : <Trash2Icon className="size-4" />}
                  真实删除
                </Button>
              </div>
            </div>
          ) : null}
        </section>

        <div className="grid gap-5 xl:grid-cols-[minmax(0,1fr)_22rem]">
          <section className="min-h-[35rem] overflow-hidden border border-[#dbe7f6] bg-white shadow-sm">
            <FastGPTFilePanel knowledgeBase={knowledgeBase} canUpload={canUpdate} canDelete={canDelete} />
          </section>

          <aside className="h-fit border border-[#dbe7f6] bg-white p-4 shadow-sm">
            <div className="flex items-center gap-2">
              <Link2Icon className="size-4 text-primary" />
              <h2 className="text-sm font-semibold">门店运行状态</h2>
            </div>
            <p className="mt-2 text-xs leading-5 text-muted-foreground">
              门店只有一个当前知识库，所属企微账号与已有会话路由会同步使用该绑定。
            </p>
            <div className="mt-4 rounded-md border border-[#e2eaf5] bg-[#f8fbff] p-3">
              <div className="flex items-center justify-between gap-3">
                <div className="text-xs font-medium text-muted-foreground">FastGPT 就绪状态</div>
                <Badge variant={readinessStatus === "ready" ? "default" : "secondary"}>{readinessStatus}</Badge>
              </div>
              {loadingReadiness ? <Loader2Icon className="mt-3 size-4 animate-spin text-primary" /> : (
                <div className="mt-3 space-y-2 text-xs text-muted-foreground">
                  <div>门店知识库：{isCurrentKnowledgeBase ? "当前启用" : "未启用"}</div>
                  <div>FastGPT Team：{readiness?.teamStatus || "unconfigured"}</div>
                  {readiness?.lastSyncedAt ? <div>最近同步：{formatDateTime(readiness.lastSyncedAt)}</div> : null}
                  {readiness?.lastErrorClass ? <div className="break-all text-destructive">诊断：{readiness.lastErrorClass}</div> : null}
                </div>
              )}
            </div>
            {canUpdate ? <div className="mt-4 grid gap-2">
              <Button className="w-full" onClick={() => void activate()} disabled={activating || isCurrentKnowledgeBase}>
                {activating ? <Loader2Icon className="size-4 animate-spin" /> : <Link2Icon className="size-4" />}
                {isCurrentKnowledgeBase ? "当前门店知识库" : "设为门店当前知识库"}
              </Button>
              <Button variant="outline" className="w-full" onClick={() => void resyncProfile()} disabled={resyncing || !isCurrentKnowledgeBase}>
                {resyncing ? <Loader2Icon className="size-4 animate-spin" /> : <RefreshCwIcon className="size-4" />}
                重新同步模型
              </Button>
            </div> : null}
          </aside>
        </div>

        <section className="min-h-[22rem] overflow-hidden border border-[#dbe7f6] bg-white shadow-sm">
          <KnowledgeResourcePanel knowledgeBase={knowledgeBase} canSync={canUpdate} canDelete={canDelete} />
        </section>
      </div>
    </div>
  )
}

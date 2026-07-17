"use client"

import { useCallback, useEffect, useMemo, useState } from "react"
import { CheckCircle2Icon, CloudIcon, CopyIcon, DatabaseIcon, Link2Icon, Loader2Icon, RadioIcon, Trash2Icon } from "lucide-react"
import { toast } from "sonner"

import { OptionCombobox } from "@/components/option-combobox"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  activateFastGPTKnowledgeBase,
  deleteFastGPTDataset,
  fetchWxWorkProtocolInstances,
  type KnowledgeBase,
  type WxWorkProtocolInstance,
} from "@/lib/api/admin"
import { formatDateTime } from "@/lib/utils"
import { FastGPTFilePanel } from "./fastgpt-file-panel"
import { KnowledgeResourcePanel } from "./knowledge-resource-panel"

type FastGPTKnowledgeWorkspaceProps = {
  knowledgeBase: KnowledgeBase
  canUpdate: boolean
  canDelete: boolean
}

function instanceLabel(instance: WxWorkProtocolInstance) {
  const storeName = instance.storeName.trim() || `门店 ${instance.storeId}`
  const employeeName = instance.employeeName.trim()
  return employeeName ? `${storeName} · ${employeeName}` : storeName
}

export function FastGPTKnowledgeWorkspace({ knowledgeBase, canUpdate, canDelete }: FastGPTKnowledgeWorkspaceProps) {
  const [instances, setInstances] = useState<WxWorkProtocolInstance[]>([])
  const [loadingInstances, setLoadingInstances] = useState(false)
  const [selectedInstanceId, setSelectedInstanceId] = useState("")
  const [activating, setActivating] = useState(false)
  const [deleteConfirmation, setDeleteConfirmation] = useState("")
  const [deletingDataset, setDeletingDataset] = useState(false)
  const [showDeleteConfirmation, setShowDeleteConfirmation] = useState(false)

  const loadInstances = useCallback(async () => {
    setLoadingInstances(true)
    try {
      const result = await fetchWxWorkProtocolInstances({ limit: 200 })
      setInstances(result.results.filter((item) => item.storeId === knowledgeBase.storeId))
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "加载门店员工号失败")
    } finally {
      setLoadingInstances(false)
    }
  }, [knowledgeBase.storeId])

  useEffect(() => {
    void loadInstances()
  }, [loadInstances])

  const activeInstances = useMemo(
    () => instances.filter((item) => item.knowledgeBaseId === knowledgeBase.id),
    [instances, knowledgeBase.id],
  )
  const instanceOptions = useMemo(
    () => instances.map((item) => ({ value: String(item.id), label: instanceLabel(item) })),
    [instances],
  )

  useEffect(() => {
    if (selectedInstanceId || instances.length === 0) {
      return
    }
    setSelectedInstanceId(String(activeInstances[0]?.id ?? instances[0].id))
  }, [activeInstances, instances, selectedInstanceId])

  async function activate() {
    if (!canUpdate) return
    const instanceID = Number(selectedInstanceId)
    if (!Number.isInteger(instanceID) || instanceID <= 0) {
      toast.error("请选择要启用该知识库的门店员工号")
      return
    }
    setActivating(true)
    try {
      await activateFastGPTKnowledgeBase(instanceID, knowledgeBase.id)
      await loadInstances()
      toast.success("已设为该员工号后续消息的当前知识库")
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "设置当前知识库失败")
    } finally {
      setActivating(false)
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
      toast.success("FastGPT 数据集已删除，相关员工号已解除当前知识库绑定")
      window.location.assign("/dashboard/knowledge")
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "删除 FastGPT 数据集失败")
    } finally {
      setDeletingDataset(false)
    }
  }

  const profileLabel = knowledgeBase.fastgptProfileName.trim() || "未同步"
  const profileMeta = knowledgeBase.fastgptProfileName.trim()
    ? `版本 ${knowledgeBase.fastgptProfileRevision.trim() || "未提供"} · ${knowledgeBase.fastgptProfileStatus.trim() || "unknown"}`
    : "等待 FastGPT Tenant/Profile 服务端接口同步"

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
            <div className="font-medium text-foreground">当前模型 Profile：{profileLabel}</div>
            <div className="mt-1 text-muted-foreground">{profileMeta}</div>
          </div>
          {canDelete && showDeleteConfirmation ? (
            <div className="mt-4 border border-destructive/30 bg-destructive/5 p-4">
              <div className="text-sm font-medium text-destructive">永久删除这个知识库</div>
              <p className="mt-1 text-xs leading-5 text-muted-foreground">
                这会真实删除 FastGPT 中的数据集及其中的文件，并解除当前使用它的员工号绑定。该操作不可恢复。
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
              <h2 className="text-sm font-semibold">员工号启用范围</h2>
            </div>
            <p className="mt-2 text-xs leading-5 text-muted-foreground">
              每个员工号只会检索一个当前知识库。切换只作用于之后的新消息，不改写历史会话。
            </p>
            <div className="mt-4 rounded-md border border-[#e2eaf5] bg-[#f8fbff] p-3">
              <div className="text-xs font-medium text-muted-foreground">当前使用这个知识库的员工号</div>
              {loadingInstances ? <Loader2Icon className="mt-3 size-4 animate-spin text-primary" /> : activeInstances.length > 0 ? (
                <div className="mt-3 space-y-2">
                  {activeInstances.map((item) => (
                    <div key={item.id} className="flex items-center gap-2 text-sm">
                      <RadioIcon className="size-3.5 text-emerald-600" />
                      <span className="min-w-0 truncate">{instanceLabel(item)}</span>
                    </div>
                  ))}
                </div>
              ) : <div className="mt-3 text-sm text-muted-foreground">暂未启用到任何员工号</div>}
            </div>
            {canUpdate ? <div className="mt-4 space-y-2">
              <label className="text-xs font-medium text-muted-foreground">设为指定员工号的当前知识库</label>
              <OptionCombobox
                value={selectedInstanceId}
                onChange={(value) => setSelectedInstanceId(value ?? "")}
                options={instanceOptions}
                placeholder={loadingInstances ? "正在加载员工号" : "选择本门店员工号"}
                searchPlaceholder="搜索员工号"
                emptyText="当前门店暂无员工号"
              />
              <Button className="w-full" onClick={() => void activate()} disabled={activating || loadingInstances || instanceOptions.length === 0}>
                {activating ? <Loader2Icon className="size-4 animate-spin" /> : <Link2Icon className="size-4" />}
                设为当前启用库
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

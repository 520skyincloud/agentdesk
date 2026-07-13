"use client"

import { useCallback, useEffect, useMemo, useState } from "react"
import { ImageIcon, Loader2Icon, RefreshCwIcon, Trash2Icon, UploadIcon } from "lucide-react"
import { toast } from "sonner"

import { OptionCombobox } from "@/components/option-combobox"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import {
  deleteKnowledgeResourceGroup,
  fetchKnowledgeResourceGroups,
  fetchWxWorkProtocolInstances,
  syncKnowledgeResourceGroup,
  type KnowledgeBase,
  type KnowledgeResourceGroup,
  type WxWorkProtocolInstance,
} from "@/lib/api/admin"
import { formatDateTime } from "@/lib/utils"

type KnowledgeResourcePanelProps = {
  knowledgeBase: KnowledgeBase | null
}

export function KnowledgeResourcePanel({ knowledgeBase }: KnowledgeResourcePanelProps) {
  const [instances, setInstances] = useState<WxWorkProtocolInstance[]>([])
  const [groups, setGroups] = useState<KnowledgeResourceGroup[]>([])
  const [selectedInstanceID, setSelectedInstanceID] = useState("")
  const [query, setQuery] = useState("")
  const [loadingInstances, setLoadingInstances] = useState(false)
  const [loadingGroups, setLoadingGroups] = useState(false)
  const [syncing, setSyncing] = useState(false)
  const [deletingID, setDeletingID] = useState<number | null>(null)

  const instanceOptions = useMemo(
    () => instances.map((instance) => ({
      value: String(instance.id),
      label: instance.storeName || instance.employeeName || `员工号 #${instance.id}`,
    })),
    [instances],
  )

  const refreshInstances = useCallback(async () => {
    if (!knowledgeBase) {
      setInstances([])
      setSelectedInstanceID("")
      return
    }
    setLoadingInstances(true)
    try {
      const page = await fetchWxWorkProtocolInstances({
        knowledgeBaseId: knowledgeBase.id,
        status: 0,
        page: 1,
        limit: 200,
      })
      setInstances(page.results)
      setSelectedInstanceID((current) => {
        if (page.results.some((item) => String(item.id) === current)) {
          return current
        }
        return page.results.length > 0 ? String(page.results[0].id) : ""
      })
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "读取已绑定员工号失败")
      setInstances([])
      setSelectedInstanceID("")
    } finally {
      setLoadingInstances(false)
    }
  }, [knowledgeBase])

  const refreshGroups = useCallback(async () => {
    if (!knowledgeBase || !selectedInstanceID) {
      setGroups([])
      return
    }
    setLoadingGroups(true)
    try {
      const page = await fetchKnowledgeResourceGroups({
        knowledgeBaseId: knowledgeBase.id,
        wxWorkInstanceId: Number(selectedInstanceID),
        page: 1,
        limit: 100,
      })
      setGroups(page.results)
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "读取知识图片资源失败")
      setGroups([])
    } finally {
      setLoadingGroups(false)
    }
  }, [knowledgeBase, selectedInstanceID])

  useEffect(() => {
    void refreshInstances()
  }, [refreshInstances])

  useEffect(() => {
    void refreshGroups()
  }, [refreshGroups])

  async function handleSync() {
    if (!knowledgeBase || !selectedInstanceID) {
      toast.error("请先选择已绑定当前知识库的员工号")
      return
    }
    if (!query.trim()) {
      toast.error("请输入一条能命中该图片知识的检索问题")
      return
    }
    setSyncing(true)
    try {
      const group = await syncKnowledgeResourceGroup({
        knowledgeBaseId: knowledgeBase.id,
        wxWorkInstanceId: Number(selectedInstanceID),
        query: query.trim(),
      })
      toast.success(`已同步 ${group.items.length} 张知识图片`)
      await refreshGroups()
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "同步知识图片失败")
    } finally {
      setSyncing(false)
    }
  }

  async function handleDelete(group: KnowledgeResourceGroup) {
    setDeletingID(group.id)
    try {
      await deleteKnowledgeResourceGroup(group.id)
      toast.success("知识图片资源已删除")
      await refreshGroups()
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "删除知识图片资源失败")
    } finally {
      setDeletingID(null)
    }
  }

  if (!knowledgeBase) {
    return <div className="p-6 text-sm text-muted-foreground">请选择知识库后管理图片资源。</div>
  }

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="border-b border-[#dbe7f6] bg-[#f8fbff] px-6 py-5">
        <div className="flex flex-col gap-4 lg:flex-row lg:items-end">
          <div className="min-w-0 flex-1 space-y-1.5">
            <Label htmlFor="knowledge-resource-instance">发送员工号</Label>
            <OptionCombobox
              value={selectedInstanceID}
              options={instanceOptions}
              placeholder={loadingInstances ? "正在读取员工号..." : "选择已绑定该知识库的员工号"}
              searchPlaceholder="搜索店名或员工号"
              emptyText="没有可用员工号"
              disabled={loadingInstances || instanceOptions.length === 0}
              onChange={setSelectedInstanceID}
            />
          </div>
          <div className="min-w-0 flex-[2] space-y-1.5">
            <Label htmlFor="knowledge-resource-query">同步检索问题</Label>
            <Input
              id="knowledge-resource-query"
              value={query}
              onChange={(event) => setQuery(event.target.value)}
              placeholder="例如：找不到酒店怎么走"
              disabled={syncing}
            />
          </div>
          <div className="flex shrink-0 gap-2">
            <Button type="button" variant="outline" size="icon" onClick={() => void refreshGroups()} disabled={loadingGroups} aria-label="刷新图片资源">
              <RefreshCwIcon className={loadingGroups ? "size-4 animate-spin" : "size-4"} />
            </Button>
            <Button type="button" onClick={() => void handleSync()} disabled={syncing || !selectedInstanceID}>
              {syncing ? <Loader2Icon className="mr-2 size-4 animate-spin" /> : <UploadIcon className="mr-2 size-4" />}
              同步图片
            </Button>
          </div>
        </div>
        <p className="mt-3 text-xs leading-5 text-muted-foreground">
          仅同步 FastGPT 当前命中记录中明确声明的图片。图片会保存到本系统资产库，并按公司、门店和知识库隔离；更换企微员工号后仍可复用。
        </p>
      </div>

      <div className="min-h-0 flex-1 overflow-y-auto">
        {loadingGroups ? (
          <div className="flex items-center justify-center gap-2 py-12 text-sm text-muted-foreground">
            <Loader2Icon className="size-4 animate-spin" /> 正在读取图片资源...
          </div>
        ) : groups.length === 0 ? (
          <div className="flex flex-col items-center justify-center gap-2 py-16 text-center text-sm text-muted-foreground">
            <ImageIcon className="size-8 text-[#9ab5d8]" />
            <span>当前员工号尚未同步知识图片资源。</span>
          </div>
        ) : (
          <div className="divide-y divide-[#e3edf9]">
            {groups.map((group) => (
              <div key={group.id} className="px-6 py-4">
                <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
                  <div className="min-w-0">
                    <div className="flex flex-wrap items-center gap-2">
                      <span className="font-medium text-foreground">{group.title || "FastGPT 图片资源"}</span>
                      <Badge variant="secondary">{group.items.length} 张图片</Badge>
                    </div>
                    <div className="mt-1 text-xs text-muted-foreground">
                      来源记录：{group.sourceRecordId} · 同步于 {formatDateTime(group.updatedAt)}
                    </div>
                  </div>
                  <Button type="button" variant="ghost" size="icon" className="text-destructive hover:text-destructive" onClick={() => void handleDelete(group)} disabled={deletingID === group.id} aria-label="删除知识图片资源">
                    {deletingID === group.id ? <Loader2Icon className="size-4 animate-spin" /> : <Trash2Icon className="size-4" />}
                  </Button>
                </div>
                <div className="mt-3 flex flex-wrap gap-2">
                  {group.items.map((item) => (
                    <span key={item.id} className="inline-flex max-w-full items-center gap-1 rounded-md border border-[#dbe7f6] bg-white px-2 py-1 text-xs text-muted-foreground">
                      <ImageIcon className="size-3 shrink-0 text-[#5f8ed8]" />
                      <span className="truncate">{item.title || `图片 ${item.sortNo}`}</span>
                    </span>
                  ))}
                </div>
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  )
}

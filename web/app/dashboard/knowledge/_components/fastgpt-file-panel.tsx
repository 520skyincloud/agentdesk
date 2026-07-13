"use client"

import { useCallback, useEffect, useRef, useState } from "react"
import { FileTextIcon, Loader2Icon, RefreshCwIcon, SearchIcon, Trash2Icon, UploadIcon } from "lucide-react"
import { toast } from "sonner"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  deleteFastGPTCollection,
  fetchFastGPTCollections,
  testFastGPTDatasetSearch,
  uploadFastGPTKnowledgeFile,
  type FastGPTCollection,
  type KnowledgeBase,
} from "@/lib/api/admin"

export function FastGPTFilePanel({ knowledgeBase }: { knowledgeBase: KnowledgeBase | null }) {
  const inputRef = useRef<HTMLInputElement>(null)
  const [collections, setCollections] = useState<FastGPTCollection[]>([])
  const [loading, setLoading] = useState(false)
  const [uploading, setUploading] = useState(false)
  const [deletingId, setDeletingId] = useState("")
  const [query, setQuery] = useState("")
  const [searching, setSearching] = useState(false)

  const refresh = useCallback(async () => {
    if (!knowledgeBase?.datasetId) {
      setCollections([])
      return
    }
    setLoading(true)
    try {
      setCollections(await fetchFastGPTCollections(knowledgeBase.id))
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "读取 FastGPT 文件失败")
    } finally {
      setLoading(false)
    }
  }, [knowledgeBase])

  useEffect(() => { void refresh() }, [refresh])

  async function upload(file: File | undefined) {
    if (!knowledgeBase || !file) return
    setUploading(true)
    try {
      await uploadFastGPTKnowledgeFile(knowledgeBase.id, file)
      toast.success("文件已进入 FastGPT 解析队列")
      window.setTimeout(() => void refresh(), 1500)
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "上传失败")
    } finally {
      setUploading(false)
      if (inputRef.current) inputRef.current.value = ""
    }
  }

  async function remove(collectionId: string) {
    if (!knowledgeBase) return
    setDeletingId(collectionId)
    try {
      await deleteFastGPTCollection(knowledgeBase.id, collectionId)
      toast.success("FastGPT 文件已删除")
      await refresh()
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "删除失败")
    } finally {
      setDeletingId("")
    }
  }

  async function search() {
    if (!knowledgeBase || !query.trim()) return
    setSearching(true)
    try {
      const result = await testFastGPTDatasetSearch(knowledgeBase.id, query.trim())
      const count = Array.isArray(result.raw) ? result.raw.length : 0
      toast.success(`真实检索完成${count > 0 ? `，返回 ${count} 条结果` : ""}`)
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "检索测试失败")
    } finally {
      setSearching(false)
    }
  }

  if (!knowledgeBase) return <div className="p-6 text-sm text-muted-foreground">请先选择知识库。</div>
  if (!knowledgeBase.datasetId) return <div className="p-6 text-sm text-muted-foreground">该知识库尚未绑定平台 FastGPT 数据集。</div>

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="border-b border-[#dbe7f6] bg-[#f8fbff] px-6 py-5">
        <div className="flex flex-wrap items-end gap-2">
          <div className="min-w-64 flex-1">
            <div className="text-sm font-medium">{knowledgeBase.datasetName || knowledgeBase.name}</div>
            <div className="mt-1 text-xs text-muted-foreground">数据集 ID：{knowledgeBase.datasetId}</div>
          </div>
          <input ref={inputRef} type="file" className="hidden" accept=".pdf,.docx,.md,.txt,.html,.csv" onChange={(event) => void upload(event.target.files?.[0])} />
          <Button type="button" onClick={() => inputRef.current?.click()} disabled={uploading}>{uploading ? <Loader2Icon className="size-4 animate-spin" /> : <UploadIcon className="size-4" />}上传文件</Button>
          <Button type="button" variant="outline" size="icon" onClick={() => void refresh()} disabled={loading} aria-label="刷新"><RefreshCwIcon className={loading ? "size-4 animate-spin" : "size-4"} /></Button>
        </div>
        <div className="mt-4 flex gap-2">
          <Input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="真实测试这个数据集能否检索到内容" />
          <Button type="button" variant="outline" onClick={() => void search()} disabled={searching || !query.trim()}>{searching ? <Loader2Icon className="size-4 animate-spin" /> : <SearchIcon className="size-4" />}检索测试</Button>
        </div>
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto divide-y divide-[#e3edf9]">
        {collections.length === 0 && !loading ? <div className="p-8 text-center text-sm text-muted-foreground">暂无 FastGPT 文件。</div> : collections.map((item) => (
          <div key={item.id} className="flex items-center gap-3 px-6 py-4">
            <FileTextIcon className="size-5 text-[#5d7fa9]" />
            <div className="min-w-0 flex-1"><div className="truncate text-sm font-medium">{item.name}</div><div className="mt-1 text-xs text-muted-foreground">已索引 {item.dataAmount} 条 · 待训练 {item.trainingAmount} 条</div></div>
            <Badge variant={item.trainingAmount > 0 ? "secondary" : "outline"}>{item.trainingAmount > 0 ? "解析中" : "可检索"}</Badge>
            <Button type="button" variant="ghost" size="icon" className="text-destructive" onClick={() => void remove(item.id)} disabled={deletingId === item.id} aria-label="删除文件">{deletingId === item.id ? <Loader2Icon className="size-4 animate-spin" /> : <Trash2Icon className="size-4" />}</Button>
          </div>
        ))}
      </div>
    </div>
  )
}

"use client"

import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import {
  FileTextIcon,
  Loader2Icon,
  RefreshCwIcon,
  SearchIcon,
  Trash2Icon,
  UploadIcon,
} from "lucide-react"
import { toast } from "sonner"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  deleteFastGPTCollection,
  fetchFastGPTCollections,
  fetchFastGPTDatasetJobs,
  testFastGPTDatasetSearch,
  uploadFastGPTKnowledgeFile,
  type FastGPTCollection,
  type FastGPTDatasetJob,
  type FastGPTSearchResult,
  type KnowledgeBase,
} from "@/lib/api/admin"
import { formatDateTime } from "@/lib/utils"

const activeJobStatuses = new Set(["pending", "uploading", "parsing", "indexing"])

function jobLabel(action: string) {
  switch (action) {
    case "create_dataset":
      return "创建知识库"
    case "upload_file":
      return "上传文件"
    case "poll_upload":
      return "解析与索引"
    case "sync_profile":
      return "同步模型方案"
    default:
      return action || "后台任务"
  }
}

function jobStatusLabel(status: string) {
  switch (status) {
    case "pending":
      return "等待处理"
    case "uploading":
      return "正在上传"
    case "parsing":
      return "正在解析"
    case "indexing":
      return "正在索引"
    case "ready":
      return "已完成"
    case "failed":
      return "失败"
    default:
      return status || "未知"
  }
}

export function FastGPTFilePanel({ knowledgeBase, canUpload, canDelete }: { knowledgeBase: KnowledgeBase | null; canUpload: boolean; canDelete: boolean }) {
  const inputRef = useRef<HTMLInputElement>(null)
  const [collections, setCollections] = useState<FastGPTCollection[]>([])
  const [jobs, setJobs] = useState<FastGPTDatasetJob[]>([])
  const [loading, setLoading] = useState(false)
  const [uploading, setUploading] = useState(false)
  const [deletingId, setDeletingId] = useState("")
  const [query, setQuery] = useState("")
  const [searching, setSearching] = useState(false)
  const [searchResult, setSearchResult] = useState<FastGPTSearchResult | null>(null)

  const refresh = useCallback(async () => {
    if (!knowledgeBase?.datasetId) {
      setCollections([])
      setJobs([])
      return
    }
    setLoading(true)
    try {
      const [nextCollections, nextJobs] = await Promise.all([
        fetchFastGPTCollections(knowledgeBase.id),
        fetchFastGPTDatasetJobs(knowledgeBase.id),
      ])
      setCollections(nextCollections)
      setJobs(nextJobs)
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "读取 FastGPT 知识库失败")
    } finally {
      setLoading(false)
    }
  }, [knowledgeBase?.datasetId, knowledgeBase?.id])

  useEffect(() => {
    void refresh()
  }, [refresh])

  const hasActiveJobs = useMemo(() => jobs.some((item) => activeJobStatuses.has(item.status)), [jobs])

  useEffect(() => {
    if (!hasActiveJobs) {
      return
    }
    const timer = window.setInterval(() => void refresh(), 4000)
    return () => window.clearInterval(timer)
  }, [hasActiveJobs, refresh])

  async function upload(file: File | undefined) {
    if (!knowledgeBase || !file || !canUpload) return
    setUploading(true)
    try {
      await uploadFastGPTKnowledgeFile(knowledgeBase.id, file)
      toast.success("文件已进入 FastGPT 处理队列")
      await refresh()
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "上传失败")
    } finally {
      setUploading(false)
      if (inputRef.current) inputRef.current.value = ""
    }
  }

  async function remove(collectionId: string) {
    if (!knowledgeBase || !canDelete) return
    setDeletingId(collectionId)
    try {
      await deleteFastGPTCollection(knowledgeBase.id, collectionId)
      toast.success("文件已从 FastGPT 知识库删除")
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
      setSearchResult(result)
      toast.success("已完成当前知识库的真实检索测试")
    } catch (error) {
      setSearchResult(null)
      toast.error(error instanceof Error ? error.message : "检索测试失败")
    } finally {
      setSearching(false)
    }
  }

  const readyFiles = collections.filter((item) => item.trainingAmount <= 0).length

  if (!knowledgeBase) return <div className="p-6 text-sm text-muted-foreground">请先选择知识库。</div>
  if (!knowledgeBase.datasetId) return <div className="p-6 text-sm text-muted-foreground">该知识库尚未绑定平台 FastGPT 数据集。</div>

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="border-b border-[#dbe7f6] px-5 py-4">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div>
            <h2 className="text-base font-semibold">文件与检索</h2>
            <p className="mt-1 text-xs leading-5 text-muted-foreground">文件由 FastGPT 完成解析和索引。仅已完成解析的内容会参与后续检索。</p>
          </div>
          <div className="flex items-center gap-2">
            {canUpload ? <input ref={inputRef} type="file" className="hidden" accept=".pdf,.doc,.docx,.md,.txt,.html,.csv,.xlsx" onChange={(event) => void upload(event.target.files?.[0])} /> : null}
            {canUpload ? <Button type="button" onClick={() => inputRef.current?.click()} disabled={uploading}>
              {uploading ? <Loader2Icon className="size-4 animate-spin" /> : <UploadIcon className="size-4" />}
              上传文件
            </Button> : null}
            <Button type="button" variant="outline" size="icon" onClick={() => void refresh()} disabled={loading} aria-label="刷新文件和任务">
              <RefreshCwIcon className={loading ? "size-4 animate-spin" : "size-4"} />
            </Button>
          </div>
        </div>
        <div className="mt-4 grid grid-cols-3 border border-[#e2eaf5] bg-[#f8fbff] text-center text-xs">
          <div className="border-r border-[#e2eaf5] px-3 py-2"><div className="text-lg font-semibold text-foreground">{collections.length}</div><div className="mt-1 text-muted-foreground">文件</div></div>
          <div className="border-r border-[#e2eaf5] px-3 py-2"><div className="text-lg font-semibold text-emerald-700">{readyFiles}</div><div className="mt-1 text-muted-foreground">可检索</div></div>
          <div className="px-3 py-2"><div className="text-lg font-semibold text-amber-700">{jobs.filter((item) => activeJobStatuses.has(item.status)).length}</div><div className="mt-1 text-muted-foreground">处理中</div></div>
        </div>
      </div>
      <div className="grid min-h-0 flex-1 lg:grid-cols-[minmax(0,1fr)_18rem]">
        <div className="min-h-0 overflow-y-auto">
          <div className="border-b border-[#e3edf9] px-5 py-3 text-sm font-medium">已上传文件</div>
          {collections.length === 0 && !loading ? <div className="px-5 py-12 text-center text-sm text-muted-foreground">暂未上传文件。</div> : collections.map((item) => (
            <div key={item.id} className="flex items-center gap-3 border-b border-[#eef3fa] px-5 py-3.5">
              <FileTextIcon className="size-5 shrink-0 text-[#5d7fa9]" />
              <div className="min-w-0 flex-1">
                <div className="truncate text-sm font-medium">{item.name}</div>
                <div className="mt-1 text-xs text-muted-foreground">已索引 {item.dataAmount} 条内容{item.trainingAmount > 0 ? ` · 待处理 ${item.trainingAmount} 条` : ""}</div>
              </div>
              <Badge variant={item.trainingAmount > 0 ? "secondary" : "outline"} className={item.trainingAmount > 0 ? "text-amber-700" : "border-emerald-200 text-emerald-700"}>{item.trainingAmount > 0 ? "处理中" : "可检索"}</Badge>
              {canDelete ? <Button type="button" variant="ghost" size="icon" className="text-destructive" onClick={() => void remove(item.id)} disabled={deletingId === item.id} aria-label="删除文件">
                {deletingId === item.id ? <Loader2Icon className="size-4 animate-spin" /> : <Trash2Icon className="size-4" />}
              </Button> : null}
            </div>
          ))}
        </div>

        <div className="border-t border-[#dbe7f6] bg-[#fbfdff] lg:border-t-0 lg:border-l">
          <div className="border-b border-[#e3edf9] px-4 py-3 text-sm font-medium">处理任务</div>
          <div className="max-h-72 overflow-y-auto">
            {jobs.length === 0 ? <div className="px-4 py-8 text-center text-xs text-muted-foreground">暂无任务记录</div> : jobs.map((job) => (
              <div key={job.id} className="border-b border-[#eef3fa] px-4 py-3">
                <div className="flex items-center justify-between gap-2"><span className="text-xs font-medium">{jobLabel(job.action)}</span><Badge variant={job.status === "failed" ? "destructive" : "secondary"}>{jobStatusLabel(job.status)}</Badge></div>
                {job.filename ? <div className="mt-1 truncate text-xs text-muted-foreground">{job.filename}</div> : null}
                <div className="mt-1 text-[11px] text-muted-foreground">{formatDateTime(job.updatedAt)} · 第 {job.attemptCount} 次</div>
                {job.lastError ? <div className="mt-2 text-xs leading-5 text-destructive">{job.lastError}</div> : null}
              </div>
            ))}
          </div>
        </div>
      </div>

      <div className="border-t border-[#dbe7f6] bg-[#f8fbff] px-5 py-4">
        <label className="text-sm font-medium">检索测试</label>
        <p className="mt-1 text-xs text-muted-foreground">只查询当前知识库，不向客户发送消息，也不影响回复 Runtime。</p>
        <div className="mt-3 flex gap-2">
          <Input value={query} onChange={(event) => setQuery(event.target.value)} onKeyDown={(event) => { if (event.key === "Enter") void search() }} placeholder="输入问题，例如：停车场在哪里？" />
          <Button type="button" variant="outline" onClick={() => void search()} disabled={searching || !query.trim()}>
            {searching ? <Loader2Icon className="size-4 animate-spin" /> : <SearchIcon className="size-4" />}
            检索
          </Button>
        </div>
        {searchResult ? (
          <div className="mt-3 max-h-72 overflow-y-auto border border-[#dbe7f6] bg-white">
            <div className="flex items-center justify-between gap-3 border-b border-[#e7eef8] px-3 py-2 text-xs text-muted-foreground">
              <span>命中 {searchResult.hits.length} 条</span>
              <span>{searchResult.latencyMs} ms</span>
            </div>
            {searchResult.hits.length === 0 ? (
              <div className="px-3 py-6 text-center text-xs text-muted-foreground">当前知识库没有匹配内容。</div>
            ) : searchResult.hits.map((hit, index) => (
              <div key={`${hit.dataId}-${index}`} className="border-b border-[#eef3fa] px-3 py-3 last:border-b-0">
                <div className="flex items-center justify-between gap-3 text-xs">
                  <span className="truncate font-medium text-foreground">{hit.sourceName || `命中 ${index + 1}`}</span>
                  <span className="shrink-0 text-muted-foreground">相关度 {(hit.score * 100).toFixed(1)}%</span>
                </div>
                {hit.question ? <div className="mt-2 text-xs font-medium leading-5 text-foreground">{hit.question}</div> : null}
                {hit.answer ? <div className="mt-1 whitespace-pre-wrap text-xs leading-5 text-muted-foreground">{hit.answer}</div> : null}
              </div>
            ))}
          </div>
        ) : null}
      </div>
    </div>
  )
}

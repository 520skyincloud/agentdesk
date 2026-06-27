"use client"

import { useState } from "react"
import {
  CheckIcon,
  DownloadIcon,
  ExternalLinkIcon,
  FileCheckIcon,
  PencilIcon,
  XIcon,
} from "lucide-react"
import Link from "next/link"
import { toast } from "sonner"

import { DashboardListPage } from "@/components/dashboard/list"
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
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Textarea } from "@/components/ui/textarea"
import {
  approveKnowledgeCandidate,
  exportKnowledgeCandidatesWeekly,
  fetchKnowledgeCandidates,
  markKnowledgeCandidateImported,
  rejectKnowledgeCandidate,
  updateKnowledgeCandidate,
  type KnowledgeCandidate,
} from "@/lib/api/admin"
import { formatDateTime } from "@/lib/utils"

const SOURCE_OPTIONS = [
  { value: "all", label: "全部来源" },
  { value: "qiyu_hq", label: "总部七鱼人工" },
  { value: "store_wecom", label: "门店企微人工" },
  { value: "ai_no_answer", label: "AI未解答" },
]

const STATUS_OPTIONS = [
  { value: "all", label: "全部状态" },
  { value: "pending", label: "待审核" },
  { value: "approved", label: "已通过" },
  { value: "rejected", label: "已驳回" },
  { value: "exported", label: "已导出" },
  { value: "imported", label: "已导入" },
]

function statusBadgeVariant(status: string) {
  switch (status) {
    case "approved":
      return "default" as const
    case "rejected":
      return "destructive" as const
    case "exported":
    case "imported":
      return "secondary" as const
    default:
      return "outline" as const
  }
}

function sourceBadgeVariant(source: string) {
  return source === "ai_no_answer" ? "destructive" : "secondary"
}

export default function KnowledgeCandidatesPage() {
  const [editing, setEditing] = useState<KnowledgeCandidate | null>(null)
  const [question, setQuestion] = useState("")
  const [answer, setAnswer] = useState("")
  const [summary, setSummary] = useState("")
  const [confidence, setConfidence] = useState("0.6")
  const [saving, setSaving] = useState(false)
  const [reloadKey, setReloadKey] = useState(0)

  function openEdit(item: KnowledgeCandidate) {
    setEditing(item)
    setQuestion(item.question)
    setAnswer(item.answer)
    setSummary(item.summary)
    setConfidence(String(item.confidence || 0.6))
  }

  async function saveEdit() {
    if (!editing) return
    setSaving(true)
    try {
      await updateKnowledgeCandidate({
        id: editing.id,
        question,
        answer,
        summary,
        confidence: Number(confidence) || 0,
        status: editing.status,
      })
      toast.success("已更新待归档问答")
      setEditing(null)
      setReloadKey((value) => value + 1)
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "更新失败")
    } finally {
      setSaving(false)
    }
  }

  async function runAction(action: () => Promise<void>, success: string) {
    try {
      await action()
      toast.success(success)
      setReloadKey((value) => value + 1)
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "操作失败")
    }
  }

  async function exportWeekly() {
    try {
      const ret = await exportKnowledgeCandidatesWeekly({ status: "approved" })
      if (ret.count === 0) {
        toast.info("没有可导出的已通过问答")
      } else {
        toast.success(`已导出 ${ret.count} 条：${ret.markdownPath}`)
      }
      setReloadKey((value) => value + 1)
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "导出失败")
    }
  }

  return (
    <>
      <DashboardListPage<KnowledgeCandidate>
        reloadKey={reloadKey}
        filters={[
          {
            name: "question",
            label: "问题",
            placeholder: "搜索问题",
            defaultValue: "",
            trim: true,
            className: "w-full sm:w-72",
          },
          {
            name: "source",
            label: "来源",
            type: "select",
            defaultValue: "all",
            allValue: "all",
            options: SOURCE_OPTIONS,
            className: "w-full sm:w-40",
          },
          {
            name: "status",
            label: "状态",
            type: "select",
            defaultValue: "pending",
            allValue: "all",
            options: STATUS_OPTIONS,
            className: "w-full sm:w-36",
          },
        ]}
        fetchList={fetchKnowledgeCandidates}
        getItemId={(item) => item.id}
        renderToolbarActions={() => (
          <Button variant="outline" onClick={() => void exportWeekly()}>
            <DownloadIcon />
            周导出
          </Button>
        )}
        columns={[
          {
            key: "store",
            label: "门店/知识库",
            className: "w-[180px]",
            render: (item) => (
              <div className="space-y-1">
                <div className="font-medium">{item.storeName || `门店 ${item.storeId}`}</div>
                <div className="text-xs text-muted-foreground">
                  {item.knowledgeBaseName || `知识库 ${item.knowledgeBaseId}`}
                </div>
              </div>
            ),
          },
          {
            key: "qa",
            label: "问题与建议答案",
            render: (item) => (
              <div className="min-w-0 space-y-1">
                <div className="line-clamp-2 font-medium">{item.question}</div>
                <div className="line-clamp-2 text-sm text-muted-foreground">
                  {item.answer || "暂无建议答案"}
                </div>
              </div>
            ),
          },
          {
            key: "source",
            label: "来源",
            className: "w-[120px]",
            render: (item) => (
              <Badge variant={sourceBadgeVariant(item.source)}>{item.sourceName || item.source}</Badge>
            ),
          },
          {
            key: "frequency",
            label: "频次",
            className: "w-20 text-right",
            render: (item) => item.frequency,
          },
          {
            key: "status",
            label: "状态",
            className: "w-[110px]",
            render: (item) => (
              <Badge variant={statusBadgeVariant(item.status)}>{item.statusName || item.status}</Badge>
            ),
          },
          {
            key: "time",
            label: "创建时间",
            className: "w-[150px]",
            render: (item) => (
              <span className="text-sm text-muted-foreground">
                {formatDateTime(item.createdAt)}
              </span>
            ),
          },
          {
            key: "actions",
            label: "操作",
            className: "w-[260px] text-right",
            render: (item) => (
              <div className="flex flex-wrap justify-end gap-2">
                <Button variant="outline" size="sm" onClick={() => openEdit(item)}>
                  <PencilIcon />
                  编辑
                </Button>
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => void runAction(() => approveKnowledgeCandidate(item.id), "已通过")}
                >
                  <CheckIcon />
                  通过
                </Button>
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => void runAction(() => rejectKnowledgeCandidate(item.id), "已驳回")}
                >
                  <XIcon />
                  驳回
                </Button>
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => void runAction(() => markKnowledgeCandidateImported(item.id), "已标记导入")}
                >
                  <FileCheckIcon />
                  导入
                </Button>
                {item.conversationId > 0 ? (
                  <Button
                    variant="ghost"
                    size="sm"
                    render={
                      <Link href={`/dashboard/conversations?conversationId=${item.conversationId}`} />
                    }
                  >
                    <ExternalLinkIcon />
                  </Button>
                ) : null}
              </div>
            ),
          },
        ]}
        labels={{
          refresh: "刷新",
          query: "查询",
          loading: "加载中",
          empty: "暂无待归档问答",
          loadFailed: "加载待归档问答失败",
        }}
      />

      <Dialog open={!!editing} onOpenChange={(open) => !open && setEditing(null)}>
        <DialogContent className="max-w-2xl">
          <DialogHeader>
            <DialogTitle>编辑待归档问答</DialogTitle>
            <DialogDescription>
              人工审核后再导出，避免把错误答案直接污染门店知识库。
            </DialogDescription>
          </DialogHeader>
          <div className="grid gap-4">
            <div className="grid gap-2">
              <Label htmlFor="candidate-question">问题</Label>
              <Textarea
                id="candidate-question"
                value={question}
                onChange={(event) => setQuestion(event.target.value)}
                rows={3}
              />
            </div>
            <div className="grid gap-2">
              <Label htmlFor="candidate-answer">建议答案</Label>
              <Textarea
                id="candidate-answer"
                value={answer}
                onChange={(event) => setAnswer(event.target.value)}
                rows={6}
              />
            </div>
            <div className="grid gap-2">
              <Label htmlFor="candidate-summary">摘要</Label>
              <Textarea
                id="candidate-summary"
                value={summary}
                onChange={(event) => setSummary(event.target.value)}
                rows={3}
              />
            </div>
            <div className="grid gap-2">
              <Label htmlFor="candidate-confidence">置信度</Label>
              <Input
                id="candidate-confidence"
                value={confidence}
                onChange={(event) => setConfidence(event.target.value)}
                inputMode="decimal"
              />
            </div>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setEditing(null)}>
              取消
            </Button>
            <Button onClick={() => void saveEdit()} disabled={saving}>
              保存
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  )
}

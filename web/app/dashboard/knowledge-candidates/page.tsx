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
import { Label } from "@/components/ui/label"
import { Textarea } from "@/components/ui/textarea"
import {
  approveKnowledgeCandidate,
  batchApproveKnowledgeCandidates,
  batchRejectKnowledgeCandidates,
  exportKnowledgeCandidatesWeekly,
  fetchKnowledgeCandidates,
  markKnowledgeCandidateImported,
  qualityCheckKnowledgeCandidates,
  rejectKnowledgeCandidate,
  updateKnowledgeCandidate,
  type KnowledgeCandidate,
  type KnowledgeCandidateQualityReport,
} from "@/lib/api/admin"
import { formatDateTime } from "@/lib/utils"

const SOURCE_OPTIONS = [
  { value: "all", label: "全部来源" },
  { value: "qiyu_hq", label: "总部网页人工" },
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
  const [selectedIds, setSelectedIds] = useState<number[]>([])
  const [qualityReports, setQualityReports] = useState<KnowledgeCandidateQualityReport[]>([])

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

  async function runBatchAction(action: (ids: number[]) => Promise<void>, success: string) {
    if (selectedIds.length === 0) {
      toast.error("请先选择待归档问答")
      return
    }
    try {
      await action(selectedIds)
      toast.success(`${success} ${selectedIds.length} 条`)
      setSelectedIds([])
      setReloadKey((value) => value + 1)
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "批量操作失败")
    }
  }

  async function runQualityCheck() {
    if (selectedIds.length === 0) {
      toast.error("请先选择待归档问答")
      return
    }
    try {
      const ret = await qualityCheckKnowledgeCandidates(selectedIds)
      setQualityReports(ret.reports)
      toast.success(`质检完成：建议通过 ${ret.approveIds.length}，复核 ${ret.reviewIds.length}，驳回 ${ret.rejectIds.length}`)
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "质检失败")
    }
  }

  function toggleSelected(id: number) {
    setSelectedIds((values) =>
      values.includes(id) ? values.filter((value) => value !== id) : [...values, id],
    )
  }

  function toggleSelectedPage(ids: number[]) {
    if (ids.length === 0) return
    setSelectedIds((values) => {
      const allSelected = ids.every((id) => values.includes(id))
      if (allSelected) {
        return values.filter((id) => !ids.includes(id))
      }
      return Array.from(new Set([...values, ...ids]))
    })
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
        renderToolbarActions={({ result }) => {
          const pageIds = result.results.map((item) => item.id)
          const allPageSelected = pageIds.length > 0 && pageIds.every((id) => selectedIds.includes(id))
          return (
            <div className="flex flex-wrap gap-2">
              <Button
                variant="outline"
                disabled={pageIds.length === 0}
                onClick={() => toggleSelectedPage(pageIds)}
              >
                {allPageSelected ? "取消当前页" : "全选当前页"}
              </Button>
              <Button
                variant="outline"
                disabled={selectedIds.length === 0}
                onClick={() => void runQualityCheck()}
              >
                <FileCheckIcon />
                先质检{selectedIds.length ? ` ${selectedIds.length}` : ""}
              </Button>
              <Button
                variant="outline"
                disabled={selectedIds.length === 0}
                onClick={() => void runBatchAction(batchApproveKnowledgeCandidates, "已批量通过")}
              >
                <CheckIcon />
                批量通过{selectedIds.length ? ` ${selectedIds.length}` : ""}
              </Button>
              <Button
                variant="outline"
                disabled={selectedIds.length === 0}
                onClick={() => void runBatchAction(batchRejectKnowledgeCandidates, "已批量驳回")}
              >
                <XIcon />
                批量驳回{selectedIds.length ? ` ${selectedIds.length}` : ""}
              </Button>
              <Button variant="outline" onClick={() => void exportWeekly()}>
                <DownloadIcon />
                周导出
              </Button>
            </div>
          )
        }}
        columns={[
          {
            key: "select",
            label: "",
            className: "w-10",
            render: (item) => (
              <Checkbox
                checked={selectedIds.includes(item.id)}
                onCheckedChange={() => toggleSelected(item.id)}
                aria-label={`选择候选 ${item.id}`}
              />
            ),
          },
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
      <Dialog open={qualityReports.length > 0} onOpenChange={(open) => !open && setQualityReports([])}>
        <DialogContent className="max-w-3xl">
          <DialogHeader>
            <DialogTitle>知识候选质检报告</DialogTitle>
            <DialogDescription>
              只把人工回答出来的语言问答沉淀为知识；行动安排、隐私、安全、赔付类先复核。
            </DialogDescription>
          </DialogHeader>
          <div className="max-h-[60vh] space-y-3 overflow-y-auto pr-1">
            {qualityReports.map((report) => (
              <div key={report.id} className="rounded-xl border bg-white p-3 shadow-sm">
                <div className="flex items-center justify-between gap-3">
                  <div className="min-w-0 font-medium">#{report.id} {report.question || "未填写问题"}</div>
                  <Badge variant={report.decision === "approve" ? "default" : report.decision === "reject" ? "destructive" : "secondary"}>
                    {report.decisionName}
                  </Badge>
                </div>
                <div className="mt-2 text-sm text-muted-foreground line-clamp-2">{report.answer || "未填写答案"}</div>
                <ul className="mt-2 space-y-1 text-xs text-slate-500">
                  {report.reasons.map((reason) => (
                    <li key={reason}>· {reason}</li>
                  ))}
                </ul>
              </div>
            ))}
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setQualityReports([])}>知道了</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  )
}

"use client"

import { useState } from "react"
import Link from "next/link"
import { ClipboardCheckIcon, ListChecksIcon, PlusIcon, Settings2Icon, StarIcon, Trash2Icon } from "lucide-react"
import { toast } from "sonner"

import { OptionCombobox } from "@/components/option-combobox"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Skeleton } from "@/components/ui/skeleton"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { Textarea } from "@/components/ui/textarea"
import {
  fetchConversationEvaluations,
  fetchQualitySamplingList,
  fetchQualityTemplates,
  saveQualityTemplate,
  type ConversationEvaluation,
  type QualitySamplingBatch,
  type QualityTemplate,
  type QualityTemplateItem,
} from "@/lib/api/service-analytics"
import {
  ConversationEvaluationStatus,
  ConversationEvaluationStatusLabels,
  QualityRuleType,
  QualityRuleTypeLabels,
  QualitySamplingStatusLabels,
} from "@/lib/generated/enums"
import { formatDateTime } from "@/lib/utils"

type QualityOperationsProps = {
  canViewEvaluations: boolean
  canViewQuality: boolean
  canManageTemplates: boolean
  startAt: string
  endAt: string
  teamId: string
  agentId: string
}

type TemplateDraft = Omit<QualityTemplate, "id" | "version" | "totalScore"> & { id?: number }

function templateDraft(template?: QualityTemplate): TemplateDraft {
  if (template) return {
    id: template.id,
    name: template.name,
    description: template.description,
    passScore: template.passScore,
    isDefault: template.isDefault,
    items: template.items.map((item) => ({ ...item })),
  }
  return {
    name: "人工服务质检模板",
    description: "",
    passScore: 80,
    isDefault: false,
    items: [{ id: 0, code: "service_quality", name: "服务质量", description: "", ruleType: QualityRuleType.Score, metricCode: "", maxScore: 100, required: true, hardFail: false, sortNo: 1 }],
  }
}

function ratingStars(rating: number) {
  return <span className="inline-flex" aria-label={`${rating} 星`}>{Array.from({ length: 5 }, (_, index) => <StarIcon key={index} className={`size-3.5 ${index < rating ? "fill-amber-400 text-amber-400" : "text-zinc-200"}`} />)}</span>
}

export function QualityOperations(props: QualityOperationsProps) {
  const [evaluationOpen, setEvaluationOpen] = useState(false)
  const [samplingOpen, setSamplingOpen] = useState(false)
  const [templateOpen, setTemplateOpen] = useState(false)
  const [evaluations, setEvaluations] = useState<ConversationEvaluation[]>([])
  const [batches, setBatches] = useState<QualitySamplingBatch[]>([])
  const [templates, setTemplates] = useState<QualityTemplate[]>([])
  const [selectedBatch, setSelectedBatch] = useState<QualitySamplingBatch | null>(null)
  const [draft, setDraft] = useState<TemplateDraft>(() => templateDraft())
  const [loading, setLoading] = useState(false)
  const [saving, setSaving] = useState(false)

  async function openEvaluations() {
    setEvaluationOpen(true)
    setLoading(true)
    try {
      const result = await fetchConversationEvaluations({ startAt: props.startAt, endAt: props.endAt, teamId: props.teamId || undefined, agentId: props.agentId || undefined, page: 1, limit: 50 })
      setEvaluations(result.results ?? [])
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "评价记录加载失败")
    } finally {
      setLoading(false)
    }
  }

  async function openSampling() {
    setSamplingOpen(true)
    setLoading(true)
    try {
      const result = await fetchQualitySamplingList({ startAt: props.startAt, endAt: props.endAt, page: 1, limit: 50 })
      setBatches(result.results ?? [])
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "抽样批次加载失败")
    } finally {
      setLoading(false)
    }
  }

  async function openTemplates() {
    setTemplateOpen(true)
    setLoading(true)
    try {
      const result = await fetchQualityTemplates()
      setTemplates(result)
      setDraft(templateDraft(result.find((item) => item.isDefault) ?? result[0]))
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "质检模板加载失败")
    } finally {
      setLoading(false)
    }
  }

  function updateItem(index: number, values: Partial<QualityTemplateItem>) {
    setDraft((current) => ({ ...current, items: current.items.map((item, itemIndex) => itemIndex === index ? { ...item, ...values } : item) }))
  }

  function addItem() {
    setDraft((current) => ({ ...current, items: [...current.items, { id: 0, code: `item_${current.items.length + 1}`, name: "新质检项", description: "", ruleType: QualityRuleType.Score, metricCode: "", maxScore: 10, required: true, hardFail: false, sortNo: current.items.length + 1 }] }))
  }

  async function saveTemplate() {
    if (!draft.name.trim() || draft.items.length === 0) {
      toast.error("模板名称和质检项不能为空")
      return
    }
    const totalScore = draft.items.reduce((sum, item) => sum + item.maxScore, 0)
    if (draft.passScore < 0 || draft.passScore > totalScore) {
      toast.error("合格分不能超过模板总分")
      return
    }
    setSaving(true)
    try {
      const saved = await saveQualityTemplate({
        id: draft.id,
        name: draft.name.trim(),
        description: draft.description.trim(),
        passScore: draft.passScore,
        isDefault: draft.isDefault,
        items: draft.items.map((item, index) => ({
          id: item.id || undefined,
          code: item.code || `item_${index + 1}`,
          name: item.name.trim(),
          description: item.description.trim(),
          ruleType: item.ruleType,
          metricCode: item.metricCode,
          maxScore: item.maxScore,
          required: item.required,
          hardFail: item.hardFail,
          sortNo: index + 1,
        })),
      })
      const refreshed = await fetchQualityTemplates()
      setTemplates(refreshed)
      setDraft(templateDraft(saved))
      toast.success(`质检模板 v${saved.version} 已保存`)
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "质检模板保存失败")
    } finally {
      setSaving(false)
    }
  }

  return (
    <>
      <div className="flex flex-wrap gap-2">
        {props.canViewEvaluations ? <Button variant="outline" size="sm" onClick={() => void openEvaluations()}><StarIcon />评价记录</Button> : null}
        {props.canViewQuality ? <Button variant="outline" size="sm" onClick={() => void openSampling()}><ListChecksIcon />抽样批次</Button> : null}
        {props.canManageTemplates ? <Button variant="outline" size="sm" onClick={() => void openTemplates()}><Settings2Icon />质检模板</Button> : null}
      </div>

      <Dialog open={evaluationOpen} onOpenChange={setEvaluationOpen}>
        <DialogContent className="max-h-[85vh] max-w-5xl overflow-y-auto">
          <DialogHeader><DialogTitle>客户评价记录</DialogTitle><DialogDescription>当前报表时间与组织范围内的评价邀请和提交结果。</DialogDescription></DialogHeader>
          {loading ? <Skeleton className="h-64 w-full" /> : <div className="overflow-x-auto border"><Table className="min-w-200"><TableHeader><TableRow><TableHead>会话</TableHead><TableHead>状态</TableHead><TableHead>评分</TableHead><TableHead>标签</TableHead><TableHead>评价内容</TableHead><TableHead>邀请时间</TableHead><TableHead>提交时间</TableHead></TableRow></TableHeader><TableBody>{evaluations.length ? evaluations.map((item) => <TableRow key={item.id}><TableCell><Link className="text-primary hover:underline" href={`/dashboard/conversation-monitor/?conversationId=${item.conversationId}`}>#{item.conversationId} / {item.sessionNo}</Link></TableCell><TableCell><Badge variant={item.status === ConversationEvaluationStatus.Submitted ? "default" : "secondary"}>{ConversationEvaluationStatusLabels[item.status]}</Badge></TableCell><TableCell>{item.rating ? ratingStars(item.rating) : "-"}</TableCell><TableCell>{item.tagCodes.join("、") || "-"}</TableCell><TableCell className="max-w-64 whitespace-normal">{item.comment || "-"}</TableCell><TableCell>{formatDateTime(item.invitedAt)}</TableCell><TableCell>{formatDateTime(item.submittedAt)}</TableCell></TableRow>) : <TableRow><TableCell colSpan={7} className="h-28 text-center text-muted-foreground">暂无评价记录</TableCell></TableRow>}</TableBody></Table></div>}
        </DialogContent>
      </Dialog>

      <Dialog open={samplingOpen} onOpenChange={setSamplingOpen}>
        <DialogContent className="max-h-[85vh] max-w-5xl overflow-y-auto">
          <DialogHeader><DialogTitle>质检抽样批次</DialogTitle><DialogDescription>抽样创建后样本固定，不会因后续筛选或人员变化重排。</DialogDescription></DialogHeader>
          {selectedBatch ? <div className="space-y-3"><Button variant="outline" size="sm" onClick={() => setSelectedBatch(null)}>返回批次</Button><div className="border"><Table><TableHeader><TableRow><TableHead>会话</TableHead><TableHead>客服ID</TableHead><TableHead>质检状态</TableHead></TableRow></TableHeader><TableBody>{selectedBatch.items.map((item) => <TableRow key={item.assignmentId}><TableCell><Link className="text-primary hover:underline" href={`/dashboard/conversation-monitor/?conversationId=${item.conversationId}&view=${item.inspectionId ? "quality_completed" : "quality_pending"}`}>#{item.conversationId} / {item.sessionNo}</Link></TableCell><TableCell>{item.agentId}</TableCell><TableCell>{item.inspectionId ? "已质检" : "待质检"}</TableCell></TableRow>)}</TableBody></Table></div></div> : loading ? <Skeleton className="h-64 w-full" /> : <div className="border"><Table><TableHeader><TableRow><TableHead>批次</TableHead><TableHead>状态</TableHead><TableHead>样本</TableHead><TableHead>已质检</TableHead><TableHead>创建时间</TableHead><TableHead className="text-right">操作</TableHead></TableRow></TableHeader><TableBody>{batches.length ? batches.map((batch) => <TableRow key={batch.id}><TableCell className="font-medium">{batch.name}</TableCell><TableCell>{QualitySamplingStatusLabels[batch.status]}</TableCell><TableCell>{batch.sampleSize}</TableCell><TableCell>{batch.items.filter((item) => item.inspectionId > 0).length}</TableCell><TableCell>{formatDateTime(batch.createdAt)}</TableCell><TableCell className="text-right"><Button variant="ghost" size="sm" onClick={() => setSelectedBatch(batch)}>查看样本</Button></TableCell></TableRow>) : <TableRow><TableCell colSpan={6} className="h-28 text-center text-muted-foreground">暂无抽样批次</TableCell></TableRow>}</TableBody></Table></div>}
        </DialogContent>
      </Dialog>

      <Dialog open={templateOpen} onOpenChange={setTemplateOpen}>
        <DialogContent className="max-h-[92vh] max-w-6xl overflow-hidden p-0">
          <DialogHeader className="border-b px-6 py-4"><DialogTitle>人工回复质检模板</DialogTitle><DialogDescription>保存修改会生成新版本，历史质检继续绑定原模板版本。</DialogDescription></DialogHeader>
          {loading ? <div className="p-6"><Skeleton className="h-96 w-full" /></div> : <div className="grid min-h-0 md:grid-cols-[240px_minmax(0,1fr)]">
            <aside className="max-h-[72vh] overflow-y-auto border-r p-3">
              <Button variant="outline" className="mb-3 w-full" onClick={() => setDraft(templateDraft())}><PlusIcon />新建模板</Button>
              <div className="space-y-1">{templates.map((template) => <button key={template.id} type="button" className={`w-full border px-3 py-2 text-left text-sm ${draft.id === template.id ? "border-primary bg-primary/5" : "hover:bg-muted"}`} onClick={() => setDraft(templateDraft(template))}><span className="block truncate font-medium">{template.name}</span><span className="mt-1 block text-xs text-muted-foreground">v{template.version} · {template.totalScore}分 {template.isDefault ? "· 默认" : ""}</span></button>)}</div>
            </aside>
            <div className="max-h-[72vh] overflow-y-auto p-5">
              <div className="grid gap-4 sm:grid-cols-[1fr_140px]">
                <label className="space-y-1.5 text-sm">模板名称<Input value={draft.name} onChange={(event) => setDraft({ ...draft, name: event.target.value })} /></label>
                <label className="space-y-1.5 text-sm">合格分<Input type="number" min={0} value={draft.passScore} onChange={(event) => setDraft({ ...draft, passScore: Number(event.target.value) })} /></label>
              </div>
              <label className="mt-4 block space-y-1.5 text-sm">说明<Textarea rows={2} value={draft.description} onChange={(event) => setDraft({ ...draft, description: event.target.value })} /></label>
              <label className="mt-4 flex items-center gap-2 text-sm"><Checkbox checked={draft.isDefault} onCheckedChange={(checked) => setDraft({ ...draft, isDefault: checked === true })} />设为默认模板</label>
              <div className="mt-5 flex items-center justify-between border-b pb-2"><h3 className="text-sm font-semibold">评分项</h3><Button variant="outline" size="sm" onClick={addItem}><PlusIcon />添加</Button></div>
              <div className="divide-y border-x border-b">{draft.items.map((item, index) => <div key={`${item.id}-${index}`} className="grid gap-3 p-4 lg:grid-cols-[minmax(180px,1fr)_150px_150px_96px_auto]">
                <Input value={item.name} aria-label="质检项名称" onChange={(event) => updateItem(index, { name: event.target.value })} />
                <OptionCombobox value={item.ruleType} options={Object.values(QualityRuleType).map((value) => ({ value, label: QualityRuleTypeLabels[value] }))} placeholder="规则类型" onChange={(value) => { const ruleType = value as QualityRuleType; updateItem(index, { ruleType, metricCode: ruleType === QualityRuleType.Metric ? "first_response_sla" : "", maxScore: ruleType === QualityRuleType.Prohibited ? 0 : item.maxScore || 10, hardFail: ruleType === QualityRuleType.Prohibited }) }} />
                {item.ruleType === QualityRuleType.Metric ? <OptionCombobox value={item.metricCode} options={[{ value: "first_response_sla", label: "人工首响 SLA" }, { value: "response_sla", label: "连续响应 SLA" }]} placeholder="系统指标" onChange={(value) => updateItem(index, { metricCode: value })} /> : <Input value={item.description} aria-label="质检项说明" placeholder="评分说明" onChange={(event) => updateItem(index, { description: event.target.value })} />}
                <Input type="number" min={0} value={item.maxScore} disabled={item.ruleType === QualityRuleType.Prohibited} aria-label="满分" onChange={(event) => updateItem(index, { maxScore: Number(event.target.value) })} />
                <div className="flex items-center justify-end gap-2"><label className="flex items-center gap-1 text-xs"><Checkbox checked={item.required} onCheckedChange={(checked) => updateItem(index, { required: checked === true })} />必评</label><Button variant="ghost" size="icon" title="删除质检项" disabled={draft.items.length === 1} onClick={() => setDraft((current) => ({ ...current, items: current.items.filter((_, itemIndex) => itemIndex !== index) }))}><Trash2Icon /></Button></div>
              </div>)}</div>
              <div className="mt-5 flex items-center justify-between"><span className="text-sm text-muted-foreground">模板总分 {draft.items.reduce((sum, item) => sum + item.maxScore, 0)}</span><Button disabled={saving} onClick={() => void saveTemplate()}><ClipboardCheckIcon />{saving ? "保存中" : "保存新版本"}</Button></div>
            </div>
          </div>}
        </DialogContent>
      </Dialog>
    </>
  )
}

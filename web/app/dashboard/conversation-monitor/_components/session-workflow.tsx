"use client"

import { useCallback, useEffect, useMemo, useState } from "react"
import {
  ClipboardCopyIcon,
  MessageSquareIcon,
  SaveIcon,
  ShieldCheckIcon,
  StarIcon,
} from "lucide-react"
import { toast } from "sonner"

import { OptionCombobox } from "@/components/option-combobox"
import { TagSelector } from "@/components/tag-selector"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Skeleton } from "@/components/ui/skeleton"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { Textarea } from "@/components/ui/textarea"
import { fetchTagsAll, type TagTree } from "@/lib/api/admin"
import {
  fetchQualityPool,
  fetchQualityTemplates,
  fetchServiceSessionMessages,
  inviteConversationEvaluation,
  saveQualityInspection,
  updateServiceSessionAnnotation,
  type QualityInspection,
  type QualityPoolEntry,
  type QualityTemplate,
  type QualityTemplateItem,
  type ServiceSession,
  type SessionMessage,
} from "@/lib/api/service-analytics"
import { QualityInspectionStatus, QualityRuleType } from "@/lib/generated/enums"
import { cn, formatDateTime } from "@/lib/utils"

type QualityInput = {
  templateItemId: number
  score: number
  violated: boolean
  evidence: string
  messageIds: number[]
  comment: string
}

function messageTone(senderType: string) {
  if (senderType === "customer") return "mr-auto border bg-background"
  if (senderType === "system") return "mx-auto border border-dashed bg-muted/50 text-muted-foreground"
  if (senderType === "ai") return "ml-auto border border-sky-200 bg-sky-50"
  return "ml-auto border border-emerald-200 bg-emerald-50"
}

function senderLabel(message: SessionMessage) {
  if (message.senderType === "customer") return message.senderName || "客户"
  if (message.senderType === "agent") return message.senderName || "人工客服"
  if (message.senderType === "ai") return "AI 客服"
  return "系统"
}

function initialQualityItems(template: QualityTemplate, inspection?: QualityInspection): QualityInput[] {
  return template.items.map((item) => {
    const existing = inspection?.items.find((value) => value.templateItemId === item.id)
    return {
      templateItemId: item.id,
      score: existing?.score ?? 0,
      violated: existing ? !existing.passed && item.ruleType === QualityRuleType.Prohibited : false,
      evidence: existing?.evidence ?? "",
      messageIds: existing?.messageIds ?? [],
      comment: existing?.comment ?? "",
    }
  })
}

function QualityRuleEditor({
  item,
  value,
  disabled,
  evidenceMessages,
  onChange,
}: {
  item: QualityTemplateItem
  value: QualityInput
  disabled: boolean
  evidenceMessages: SessionMessage[]
  onChange: (value: QualityInput) => void
}) {
  return (
    <div className="border-b py-4 last:border-b-0">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <div className="flex items-center gap-2 font-medium">
            {item.name}
            {item.required ? <Badge variant="outline">必评</Badge> : null}
            {item.hardFail ? <Badge variant="destructive">一票否决</Badge> : null}
          </div>
          {item.description ? <p className="mt-1 text-xs text-muted-foreground">{item.description}</p> : null}
        </div>
        {item.ruleType === QualityRuleType.Score ? (
          <label className="flex items-center gap-2 text-sm">评分<Input className="h-8 w-24" type="number" min={0} max={item.maxScore} value={value.score} disabled={disabled} onChange={(event) => onChange({ ...value, score: Number(event.target.value) })} /><span className="text-muted-foreground">/ {item.maxScore}</span></label>
        ) : item.ruleType === QualityRuleType.Metric ? (
          <Badge variant="secondary">系统自动计算</Badge>
        ) : (
          <label className="flex items-center gap-2 text-sm"><Checkbox checked={value.violated} disabled={disabled} onCheckedChange={(checked) => onChange({ ...value, violated: checked === true })} />命中禁忌项</label>
        )}
      </div>

      {item.ruleType !== QualityRuleType.Metric ? (
        <div className="mt-3 grid gap-3 lg:grid-cols-2">
          <label className="space-y-1 text-xs text-muted-foreground">说明或证据<Textarea value={value.evidence} disabled={disabled} className="min-h-20" onChange={(event) => onChange({ ...value, evidence: event.target.value })} /></label>
          <div className="space-y-1 text-xs text-muted-foreground">
            人工回复证据
            <div className="max-h-32 space-y-2 overflow-y-auto border p-2">
              {evidenceMessages.length ? evidenceMessages.map((message) => (
                <label key={message.id} className="flex cursor-pointer items-start gap-2 text-xs text-foreground">
                  <Checkbox
                    checked={value.messageIds.includes(message.id)}
                    disabled={disabled}
                    onCheckedChange={(checked) => onChange({
                      ...value,
                      messageIds: checked === true ? [...value.messageIds, message.id] : value.messageIds.filter((id) => id !== message.id),
                    })}
                  />
                  <span className="line-clamp-2">{message.content || message.payload || "-"}</span>
                </label>
              )) : <div className="py-4 text-center text-muted-foreground">该接待分段没有可选人工回复</div>}
            </div>
          </div>
        </div>
      ) : null}
    </div>
  )
}

export function SessionWorkflowDialog({
  open,
  session,
  canAnnotate,
  canViewTags,
  canViewQuality,
  canManageQuality,
  canInviteEvaluation,
  onOpenChange,
  onUpdated,
  onOpenFullDetail,
}: {
  open: boolean
  session: ServiceSession | null
  canAnnotate: boolean
  canViewTags: boolean
  canViewQuality: boolean
  canManageQuality: boolean
  canInviteEvaluation: boolean
  onOpenChange: (open: boolean) => void
  onUpdated: (session: ServiceSession) => void
  onOpenFullDetail: () => void
}) {
  const [loading, setLoading] = useState(false)
  const [messages, setMessages] = useState<SessionMessage[]>([])
  const [pool, setPool] = useState<QualityPoolEntry[]>([])
  const [templates, setTemplates] = useState<QualityTemplate[]>([])
  const [assignmentId, setAssignmentId] = useState("")
  const [templateId, setTemplateId] = useState("")
  const [qualityItems, setQualityItems] = useState<QualityInput[]>([])
  const [qualitySummary, setQualitySummary] = useState("")
  const [qualitySaving, setQualitySaving] = useState(false)
  const [annotationSaving, setAnnotationSaving] = useState(false)
  const [resolutionCode, setResolutionCode] = useState("")
  const [categoryCode, setCategoryCode] = useState("")
  const [sessionSummary, setSessionSummary] = useState("")
  const [tagIds, setTagIds] = useState<number[]>([])
  const [tags, setTags] = useState<TagTree[]>([])

  const selectedEntry = pool.find((item) => String(item.assignmentId) === assignmentId)
  const selectedInspection = selectedEntry?.inspection
  const selectedTemplate = templates.find((item) => String(item.id) === templateId)
  const qualityLocked = selectedInspection?.status === QualityInspectionStatus.Completed
  const evidenceMessages = useMemo(
    () => messages.filter((message) => message.senderType === "agent" && message.senderId === selectedEntry?.agentId),
    [messages, selectedEntry?.agentId],
  )

  const load = useCallback(async () => {
    if (!open || !session) return
    setLoading(true)
    setResolutionCode(session.resolutionCode)
    setCategoryCode(session.categoryCode)
    setSessionSummary(session.sessionSummary)
    setTagIds(session.tagIds)
    try {
      const [messageResult, poolResult, templateResult] = await Promise.all([
        fetchServiceSessionMessages(session.id, 1, 500),
        canViewQuality ? fetchQualityPool({ conversationId: session.conversationId, sessionNo: session.sessionNo, page: 1, limit: 100 }) : Promise.resolve({ results: [], page: { page: 1, limit: 100, total: 0 } }),
        canViewQuality ? fetchQualityTemplates() : Promise.resolve([]),
      ])
      setMessages(messageResult.results ?? [])
      setPool(poolResult.results ?? [])
      setTemplates(templateResult ?? [])
      const firstEntry = poolResult.results?.[0]
      const firstTemplate = firstEntry?.inspection
        ? templateResult.find((item) => item.id === firstEntry.inspection?.templateId)
        : templateResult.find((item) => item.isDefault) ?? templateResult[0]
      setAssignmentId(firstEntry ? String(firstEntry.assignmentId) : "")
      setTemplateId(firstTemplate ? String(firstTemplate.id) : "")
      setQualityItems(firstTemplate ? initialQualityItems(firstTemplate, firstEntry?.inspection) : [])
      setQualitySummary(firstEntry?.inspection?.summary ?? "")
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "会话记录详情加载失败")
    } finally {
      setLoading(false)
    }
  }, [canViewQuality, open, session])

  useEffect(() => { void load() }, [load])

  useEffect(() => {
    if (!open || !session || !canViewTags) {
      setTags([])
      return
    }
    void fetchTagsAll().then(setTags).catch((error) => toast.error(error instanceof Error ? error.message : "标签加载失败"))
  }, [canViewTags, open, session])

  function selectAssignment(value: string) {
    setAssignmentId(value)
    const entry = pool.find((item) => String(item.assignmentId) === value)
    const template = entry?.inspection
      ? templates.find((item) => item.id === entry.inspection?.templateId)
      : templates.find((item) => item.isDefault) ?? templates[0]
    setTemplateId(template ? String(template.id) : "")
    setQualityItems(template ? initialQualityItems(template, entry?.inspection) : [])
    setQualitySummary(entry?.inspection?.summary ?? "")
  }

  function selectTemplate(value: string) {
    setTemplateId(value)
    const template = templates.find((item) => String(item.id) === value)
    setQualityItems(template ? initialQualityItems(template) : [])
    setQualitySummary("")
  }

  async function saveAnnotation() {
    if (!session) return
    setAnnotationSaving(true)
    try {
      const updated = await updateServiceSessionAnnotation({
        id: session.id,
        resolutionCode,
        categoryCode,
        sessionSummary,
        tagIds,
      })
      onUpdated(updated)
      toast.success("服务小记已保存")
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "服务小记保存失败")
    } finally {
      setAnnotationSaving(false)
    }
  }

  async function saveQuality(status: QualityInspectionStatus) {
    if (!canManageQuality || !selectedEntry || !selectedTemplate) return
    setQualitySaving(true)
    try {
      const inspection = await saveQualityInspection({
        id: selectedInspection?.id,
        assignmentId: selectedEntry.assignmentId,
        templateId: selectedTemplate.id,
        status,
        summary: qualitySummary,
        items: qualityItems,
      })
      setPool((current) => current.map((entry) => entry.assignmentId === selectedEntry.assignmentId ? { ...entry, inspection } : entry))
      toast.success(status === QualityInspectionStatus.Completed ? "人工回复质检已完成" : "质检草稿已保存")
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "质检保存失败")
    } finally {
      setQualitySaving(false)
    }
  }

  async function inviteEvaluation() {
    if (!session) return
    try {
      const result = await inviteConversationEvaluation({ serviceSessionId: session.id })
      const url = `${window.location.origin}${result.path}`
      await navigator.clipboard.writeText(url)
      toast.success("评价链接已生成并复制")
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "评价邀请生成失败")
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="flex h-[90vh] max-w-6xl flex-col overflow-hidden">
        <DialogHeader>
          <DialogTitle>{session?.customerName || "会话记录"} · 第 {session?.sessionNo || 1} 轮</DialogTitle>
          <DialogDescription>{session?.storeName || "未识别门店"} / {session?.wxWorkEmployeeName || "未识别员工号"} · {formatDateTime(session?.startedAt)}</DialogDescription>
        </DialogHeader>
        {loading || !session ? <div className="grid flex-1 gap-3 py-4"><Skeleton className="h-12" /><Skeleton className="h-full" /></div> : (
          <Tabs defaultValue="messages" className="flex min-h-0 flex-1 flex-col">
            <TabsList className="w-full justify-start">
              <TabsTrigger value="messages">聊天原文</TabsTrigger>
              <TabsTrigger value="summary">服务小记</TabsTrigger>
              {canViewQuality ? <TabsTrigger value="quality">人工质检</TabsTrigger> : null}
            </TabsList>

            <TabsContent value="messages" className="min-h-0 flex-1 overflow-hidden">
              <div className="flex h-full flex-col border">
                <div className="flex items-center justify-between border-b px-4 py-2">
                  <div className="text-xs text-muted-foreground">客户 {session.customerMessageCount} · AI {session.aiMessageCount} · 人工 {session.humanMessageCount}</div>
                  <Button variant="outline" size="sm" onClick={onOpenFullDetail}><MessageSquareIcon />完整会话详情</Button>
                </div>
                <ScrollArea className="min-h-0 flex-1 bg-muted/20 p-4">
                  <div className="space-y-3 pr-3">
                    {messages.length ? messages.map((message) => <div key={message.id} className={cn("max-w-[78%] p-3 text-sm", messageTone(message.senderType))}><div className="mb-1 flex items-center justify-between gap-4 text-xs text-muted-foreground"><span>{senderLabel(message)}</span><span>{formatDateTime(message.sentAt)}</span></div><div className="whitespace-pre-wrap break-words">{message.content || message.payload || "-"}</div></div>) : <div className="py-20 text-center text-sm text-muted-foreground">该服务轮次暂无消息</div>}
                  </div>
                </ScrollArea>
              </div>
            </TabsContent>

            <TabsContent value="summary" className="min-h-0 flex-1 overflow-y-auto">
              <div className="grid gap-5 border p-5 lg:grid-cols-[0.75fr_1.25fr]">
                <div className="space-y-4">
                  <label className="space-y-1.5 text-sm">问题解决状态<OptionCombobox value={resolutionCode} placeholder="暂未标记" onChange={setResolutionCode} disabled={!canAnnotate} options={[{ value: "", label: "暂未标记" }, { value: "resolved", label: "已解决" }, { value: "follow_up", label: "需跟进" }, { value: "unresolved", label: "未解决" }]} /></label>
                  <label className="space-y-1.5 text-sm">咨询分类<Input value={categoryCode} disabled={!canAnnotate} maxLength={50} onChange={(event) => setCategoryCode(event.target.value)} placeholder="例如：预订变更" /></label>
                  {canViewTags ? <label className="space-y-1.5 text-sm">会话标签<TagSelector mode="multiple" value={tagIds} onChange={setTagIds} tags={tags} disabled={!canAnnotate} placeholder="选择会话标签" searchPlaceholder="搜索标签路径" emptyText="暂无标签" /></label> : null}
                  <div className="grid grid-cols-2 gap-3 text-sm"><div className="border p-3"><div className="text-xs text-muted-foreground">排队</div><div className="mt-1 font-medium">{Math.round(session.queueSeconds)} 秒</div></div><div className="border p-3"><div className="text-xs text-muted-foreground">人工首响</div><div className="mt-1 font-medium">{Math.round(session.firstResponseSeconds)} 秒</div></div></div>
                  <div className="text-xs text-muted-foreground">数据质量：{session.dataQuality} · 来源：{session.factOrigin}{session.estimatedFields.length ? ` · 估算字段 ${session.estimatedFields.join("、")}` : ""}</div>
                </div>
                <label className="space-y-1.5 text-sm">服务小记<Textarea className="min-h-60" value={sessionSummary} disabled={!canAnnotate} maxLength={5000} onChange={(event) => setSessionSummary(event.target.value)} placeholder="记录客户诉求、处理结果和后续事项" /></label>
                <div className="flex justify-end gap-2 lg:col-span-2">
                  {canInviteEvaluation && session.humanMessageCount > 0 ? <Button variant="outline" onClick={() => void inviteEvaluation()}><ClipboardCopyIcon />邀请评价</Button> : null}
                  {canAnnotate ? <Button onClick={() => void saveAnnotation()} disabled={annotationSaving}><SaveIcon />{annotationSaving ? "保存中" : "保存服务小记"}</Button> : null}
                </div>
              </div>
            </TabsContent>

            {canViewQuality ? <TabsContent value="quality" className="min-h-0 flex-1 overflow-y-auto">
              {pool.length ? <div className="border p-5">
                <div className="grid gap-3 border-b pb-4 md:grid-cols-2">
                  <label className="space-y-1.5 text-sm">人工接待分段<OptionCombobox value={assignmentId} placeholder="请选择接待分段" onChange={selectAssignment} options={pool.map((item) => ({ value: String(item.assignmentId), label: `${item.agentName || `#${item.agentId}`} · ${formatDateTime(item.assignedAt)}` }))} /></label>
                  <label className="space-y-1.5 text-sm">质检模板<OptionCombobox value={templateId} placeholder="请选择质检模板" disabled={qualityLocked || !canManageQuality} onChange={selectTemplate} options={templates.map((item) => ({ value: String(item.id), label: `${item.name} v${item.version}` }))} /></label>
                </div>
                {selectedInspection ? <div className="mt-4 flex flex-wrap items-center gap-2"><Badge variant={selectedInspection.hardFailed ? "destructive" : "secondary"}>{selectedInspection.status === QualityInspectionStatus.Completed ? "已完成" : "草稿"}</Badge><span className="text-sm">得分 {selectedInspection.totalScore} / {selectedInspection.maxScore}</span>{selectedInspection.hardFailed ? <span className="text-sm text-destructive">命中一票否决项</span> : null}</div> : null}
                <div>{selectedTemplate?.items.map((item, index) => <QualityRuleEditor key={item.id} item={item} value={qualityItems[index] ?? { templateItemId: item.id, score: 0, violated: false, evidence: "", messageIds: [], comment: "" }} disabled={qualityLocked || !canManageQuality} evidenceMessages={evidenceMessages} onChange={(next) => setQualityItems((current) => current.map((value, itemIndex) => itemIndex === index ? next : value))} />)}</div>
                <label className="space-y-1.5 text-sm">质检评语<Textarea value={qualitySummary} disabled={qualityLocked || !canManageQuality} onChange={(event) => setQualitySummary(event.target.value)} /></label>
                {canManageQuality && !qualityLocked ? <div className="mt-4 flex justify-end gap-2"><Button variant="outline" disabled={qualitySaving} onClick={() => void saveQuality(QualityInspectionStatus.Draft)}><SaveIcon />保存草稿</Button><Button disabled={qualitySaving} onClick={() => void saveQuality(QualityInspectionStatus.Completed)}><ShieldCheckIcon />完成质检</Button></div> : null}
              </div> : <div className="flex min-h-60 flex-col items-center justify-center border border-dashed text-sm text-muted-foreground"><StarIcon className="mb-3 size-8" />该轮次没有可质检的人工回复分段</div>}
            </TabsContent> : null}
          </Tabs>
        )}
      </DialogContent>
    </Dialog>
  )
}

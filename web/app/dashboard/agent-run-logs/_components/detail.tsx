"use client"

import { useEffect, useMemo, useState, type ReactNode } from "react"
import { BotMessageSquareIcon, WorkflowIcon } from "lucide-react"
import { toast } from "sonner"

import { ImMessageHTML } from "@/components/im-message-html"
import { JsonTreeViewer } from "@/components/json-tree-viewer"
import { ProjectDialog } from "@/components/project-dialog"
import { Button } from "@/components/ui/button"
import { fetchAgentRunLog, type AgentRunLog } from "@/lib/api/admin"
import { useI18n } from "@/i18n/provider"
import { formatDateTime } from "@/lib/utils"

type AgentRunLogDetailDialogProps = {
  open: boolean
  logId: number | null
  onOpenChange: (open: boolean) => void
}

type TFunction = (key: string, values?: Record<string, string | number>) => string

export function AgentRunLogDetailDialog({
  open,
  logId,
  onOpenChange,
}: AgentRunLogDetailDialogProps) {
  const t = useI18n()
  const [loading, setLoading] = useState(false)
  const [activeLog, setActiveLog] = useState<AgentRunLog | null>(null)

  useEffect(() => {
    if (!open || !logId) {
      return
    }

    let cancelled = false
    const currentLogId = logId

    async function loadDetail() {
      setLoading(true)
      try {
        const data = await fetchAgentRunLog(currentLogId)
        if (!cancelled) {
          setActiveLog(data)
        }
      } catch (error) {
        if (!cancelled) {
          toast.error(error instanceof Error ? error.message : t("agentRunLog.loadDetailFailed"))
          onOpenChange(false)
        }
      } finally {
        if (!cancelled) {
          setLoading(false)
        }
      }
    }

    void loadDetail()

    return () => {
      cancelled = true
    }
  }, [logId, onOpenChange, open, t])

  useEffect(() => {
    if (open) {
      return
    }
    setLoading(false)
    setActiveLog(null)
  }, [open])

  const activeTraceData = useMemo(
    () => safeParseJSON(activeLog?.traceData ?? ""),
    [activeLog?.traceData]
  )
  const activeToolSearchTrace = useMemo(
    () => safeParseJSON(activeLog?.toolSearchTrace ?? ""),
    [activeLog?.toolSearchTrace]
  )
  const activeGraphToolTrace = useMemo(
    () => safeParseJSON(activeLog?.graphToolTrace ?? ""),
    [activeLog?.graphToolTrace]
  )

  return (
    <ProjectDialog
      open={open}
      onOpenChange={onOpenChange}
      title={
        <span className="flex items-center gap-2">
          <WorkflowIcon className="size-4" />
          {t("agentRunLog.detailTitle")}
        </span>
      }
      description={t("agentRunLog.detailDescription")}
      size="xl"
      allowFullscreen
      defaultFullscreen
      bodyClassName="min-h-0"
      footer={
        <Button variant="outline" onClick={() => onOpenChange(false)}>
          {t("agentRunLog.close")}
        </Button>
      }
    >
      {loading ? (
        <div className="py-10 text-sm text-muted-foreground">{t("agentRunLog.loading")}</div>
      ) : activeLog ? (
        <>
          <MetaStrip
            items={[
              { label: t("agentRunLog.logId"), value: String(activeLog.id) },
              { label: t("agentRunLog.conversationId"), value: String(activeLog.conversationId || "-") },
              { label: t("agentRunLog.messageId"), value: String(activeLog.messageId || "-") },
              { label: "AI Agent", value: String(activeLog.aiAgentId || "-") },
            ]}
          />

          <InfoBlock
            title={t("agentRunLog.planningStage")}
            lines={[
              `plannedAction: ${activeLog.plannedAction || "-"}`,
              `plannedSkillCode: ${activeLog.plannedSkillCode || "-"}`,
              `plannedSkillName: ${activeLog.plannedSkillName || "-"}`,
              `graphToolCode: ${activeLog.graphToolCode || "-"}`,
              `recommendedAction: ${activeLog.recommendedAction || "-"}`,
              `riskLevel: ${activeLog.riskLevel || "-"}`,
              `ticketDraftReady: ${activeLog.ticketDraftReady ? "true" : "false"}`,
              `plannedToolCode: ${activeLog.plannedToolCode || "-"}`,
              `planReason: ${activeLog.planReason || "-"}`,
              `handoffReason: ${activeLog.handoffReason || "-"}`,
              `skillRouteTrace: ${activeLog.skillRouteTrace || "-"}`,
            ]}
          />
          <InfoBlock
            title={t("agentRunLog.hitlStatus")}
            lines={[
              `hitlStatus: ${activeLog.hitlStatus || "-"}`,
              `hitlStatusName: ${getHitlStatusLabel(activeLog.hitlStatus, t) || "-"}`,
              `hitlSummary: ${getHitlSummary(activeLog.hitlStatus, t) || "-"}`,
            ]}
          />
          <InfoBlock
            title={t("agentRunLog.executionResult")}
            lines={[
              `finalAction: ${activeLog.finalAction || "-"}`,
              `finalStatus: ${activeLog.finalStatus || "-"}`,
              `interruptType: ${activeLog.interruptType || "-"}`,
              `resumeSource: ${activeLog.resumeSource || "-"}`,
              `latencyMs: ${activeLog.latencyMs} ms`,
              `createdAt: ${formatDateTime(activeLog.createdAt)}`,
            ]}
          />

          <JsonBlock
            title={t("agentRunLog.dynamicTools")}
            jsonValue={activeToolSearchTrace}
            fallbackValue={activeLog.toolSearchTrace}
          />
          <JsonBlock
            title={t("agentRunLog.graphToolCall")}
            jsonValue={activeGraphToolTrace}
            fallbackValue={activeLog.graphToolTrace}
          />
          <TextBlock
            icon={<BotMessageSquareIcon className="size-4" />}
            title={t("agentRunLog.userMessage")}
            value={activeLog.userMessage}
            renderAsHtml
          />
          <TextBlock
            icon={<WorkflowIcon className="size-4" />}
            title={t("agentRunLog.botReply")}
            value={activeLog.replyText}
          />
          <TextBlock title={t("agentRunLog.errorMessage")} value={activeLog.errorMessage} tone="danger" />
          <JsonBlock
            title={t("agentRunLog.trace")}
            jsonValue={activeTraceData}
            fallbackValue={activeLog.traceData}
          />
        </>
      ) : (
        <div className="py-10 text-sm text-muted-foreground">{t("agentRunLog.notFound")}</div>
      )}
    </ProjectDialog>
  )
}

function getHitlStatusLabel(status: string | undefined, t: TFunction) {
  switch (status) {
    case "pending":
      return t("agentRunLog.hitlPending")
    case "confirmed":
      return t("agentRunLog.hitlConfirmed")
    case "cancelled":
      return t("agentRunLog.hitlCancelled")
    case "expired":
      return t("agentRunLog.hitlExpired")
    case "triggered":
      return t("agentRunLog.hitlTriggered")
    default:
      return ""
  }
}

function getHitlSummary(status: string | undefined, t: TFunction) {
  switch (status) {
    case "pending":
      return t("agentRunLog.hitlPendingSummary")
    case "confirmed":
      return t("agentRunLog.hitlConfirmedSummary")
    case "cancelled":
      return t("agentRunLog.hitlCancelledSummary")
    case "expired":
      return t("agentRunLog.hitlExpiredSummary")
    case "triggered":
      return t("agentRunLog.hitlTriggeredSummary")
    default:
      return ""
  }
}

function safeParseJSON(value: string) {
  if (!value.trim()) {
    return null
  }
  try {
    return JSON.parse(value)
  } catch {
    return null
  }
}

function MetaStrip({
  items,
}: {
  items: Array<{ label: string; value: string }>
}) {
  return (
    <div className="rounded-lg border bg-muted/20 px-4 py-3">
      <div className="flex flex-wrap gap-x-6 gap-y-2 text-sm">
        {items.map((item) => (
          <div key={item.label} className="flex min-w-0 items-center gap-2">
            <span className="shrink-0 text-xs text-muted-foreground">{item.label}</span>
            <span className="min-w-0 truncate font-medium">{item.value}</span>
          </div>
        ))}
      </div>
    </div>
  )
}

function InfoBlock({ title, lines }: { title: string; lines: string[] }) {
  return (
    <div className="rounded-lg border p-4">
      <div className="text-sm font-medium">{title}</div>
      <div className="mt-3 space-y-2 text-sm text-muted-foreground">
        {lines.map((line) => (
          <div key={line}>{line}</div>
        ))}
      </div>
    </div>
  )
}

function TextBlock({
  title,
  value,
  icon,
  tone = "default",
  renderAsHtml = false,
}: {
  title: string
  value?: string
  icon?: ReactNode
  tone?: "default" | "danger"
  renderAsHtml?: boolean
}) {
  const normalizedValue = value?.trim() || ""
  const html = useMemo(() => {
    if (!renderAsHtml || !normalizedValue) {
      return ""
    }
    return sanitizeRichHTML(normalizedValue)
  }, [normalizedValue, renderAsHtml])

  return (
    <div className="rounded-lg border p-4">
      <div className="flex items-center gap-2 text-sm font-medium">
        {icon}
        {title}
      </div>
      {renderAsHtml && normalizedValue ? (
        <ImMessageHTML
          html={html}
          className="mt-3 select-text text-muted-foreground"
        />
      ) : (
        <div
          className={
            tone === "danger"
              ? "mt-3 select-text whitespace-pre-wrap wrap-break-word text-sm text-destructive"
              : "mt-3 select-text whitespace-pre-wrap wrap-break-word text-sm text-muted-foreground"
          }
        >
          {normalizedValue || "-"}
        </div>
      )}
    </div>
  )
}

function JsonBlock({
  title,
  jsonValue,
  fallbackValue,
}: {
  title: string
  jsonValue: unknown
  fallbackValue?: string
}) {
  const normalizedFallback = fallbackValue?.trim() || ""

  return (
    <div className="rounded-lg border p-4">
      <div className="text-sm font-medium">{title}</div>
      {jsonValue ? (
        <JsonTreeViewer value={jsonValue} className="mt-3" />
      ) : (
        <div className="mt-3 select-text whitespace-pre-wrap wrap-break-word text-sm text-muted-foreground">
          {normalizedFallback || "-"}
        </div>
      )}
    </div>
  )
}

function sanitizeRichHTML(value: string) {
  if (typeof window === "undefined") {
    return value
  }

  const doc = new DOMParser().parseFromString(value, "text/html")
  const allowedTags = new Set([
    "a",
    "b",
    "blockquote",
    "br",
    "code",
    "div",
    "em",
    "h1",
    "h2",
    "h3",
    "h4",
    "h5",
    "h6",
    "hr",
    "img",
    "li",
    "ol",
    "p",
    "pre",
    "span",
    "strong",
    "table",
    "tbody",
    "td",
    "th",
    "thead",
    "tr",
    "u",
    "ul",
  ])
  const allowedAttrs = new Set([
    "alt",
    "class",
    "colspan",
    "href",
    "rel",
    "rowspan",
    "src",
    "target",
    "title",
  ])
  const walker = doc.createTreeWalker(doc.body, NodeFilter.SHOW_ELEMENT)
  const elements: Element[] = []

  while (walker.nextNode()) {
    elements.push(walker.currentNode as Element)
  }

  for (const element of elements) {
    const tag = element.tagName.toLowerCase()
    if (!allowedTags.has(tag)) {
      element.replaceWith(...Array.from(element.childNodes))
      continue
    }

    for (const attr of Array.from(element.attributes)) {
      const name = attr.name.toLowerCase()
      const attrValue = attr.value.trim()
      if (name.startsWith("on") || !allowedAttrs.has(name)) {
        element.removeAttribute(attr.name)
        continue
      }
      if ((name === "href" || name === "src") && !isSafeURL(attrValue)) {
        element.removeAttribute(attr.name)
      }
    }

    if (tag === "a") {
      element.setAttribute("target", "_blank")
      element.setAttribute("rel", "noreferrer noopener")
    }
  }

  return doc.body.innerHTML
}

function isSafeURL(value: string) {
  if (!value) {
    return false
  }
  if (value.startsWith("/")) {
    return true
  }
  if (value.startsWith("data:image/")) {
    return true
  }
  try {
    const url = new URL(value, window.location.origin)
    return ["http:", "https:"].includes(url.protocol)
  } catch {
    return false
  }
}

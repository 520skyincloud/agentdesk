"use client"

import { useEffect, useMemo, useState } from "react"
import { SearchIcon } from "lucide-react"
import { toast } from "sonner"

import { DashboardListPage } from "@/components/dashboard/list"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { AgentRunLogDetailDialog } from "./_components/detail"
import {
  fetchAgentRunLogs,
  fetchAIAgentsAll,
  type AIAgent,
  type AgentRunLog,
} from "@/lib/api/admin"
import { useI18n } from "@/i18n/provider"
import { formatDateTime } from "@/lib/utils"

type TFunction = (key: string, values?: Record<string, string | number>) => string

function getActionOptions(t: TFunction) {
  return [
    { value: "all", label: t("agentRunLog.allActions") },
    { value: "rag", label: "RAG" },
    { value: "skill", label: "Skill" },
    { value: "tool", label: "Tool" },
    { value: "graph", label: "Graph" },
    { value: "handoff", label: t("agentRunLog.handoff") },
    { value: "reply", label: t("agentRunLog.reply") },
    { value: "fallback", label: t("agentRunLog.fallback") },
  ]
}

function getFinalStatusOptions(t: TFunction) {
  return [
    { value: "all", label: t("agentRunLog.allStatus") },
    { value: "completed", label: "completed" },
    { value: "interrupted", label: "interrupted" },
    { value: "expired", label: "expired" },
    { value: "error", label: "error" },
    { value: "fallback", label: "fallback" },
  ]
}

function getHitlStatusOptions(t: TFunction) {
  return [
    { value: "all", label: t("agentRunLog.allHitl") },
    { value: "pending", label: t("agentRunLog.hitlPending") },
    { value: "confirmed", label: t("agentRunLog.hitlConfirmed") },
    { value: "cancelled", label: t("agentRunLog.hitlCancelled") },
    { value: "expired", label: t("agentRunLog.hitlExpired") },
    { value: "triggered", label: t("agentRunLog.hitlTriggered") },
  ]
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

function actionBadgeVariant(action: string) {
  switch (action) {
    case "handoff":
      return "destructive" as const
    case "skill":
      return "default" as const
    case "tool":
      return "default" as const
    case "graph":
      return "default" as const
    case "rag":
      return "secondary" as const
    case "fallback":
      return "outline" as const
    default:
      return "secondary" as const
  }
}

export default function DashboardAgentRunLogsPage() {
  const t = useI18n()
  const [detailOpen, setDetailOpen] = useState(false)
  const [activeLogId, setActiveLogId] = useState<number | null>(null)
  const [aiAgents, setAiAgents] = useState<AIAgent[]>([])
  const actionOptions = useMemo(() => getActionOptions(t), [t])
  const finalStatusOptions = useMemo(() => getFinalStatusOptions(t), [t])
  const hitlStatusOptions = useMemo(() => getHitlStatusOptions(t), [t])

  const aiAgentOptions = useMemo(
    () => [
      { value: "all", label: t("agentRunLog.allAgents") },
      ...aiAgents.map((item) => ({
        value: String(item.id),
        label: item.name,
      })),
    ],
    [aiAgents, t]
  )

  useEffect(() => {
    async function loadAIAgents() {
      try {
        const data = await fetchAIAgentsAll()
        setAiAgents(data)
      } catch (error) {
        toast.error(error instanceof Error ? error.message : t("agentRunLog.loadAgentsFailed"))
      }
    }
    void loadAIAgents()
  }, [t])

  return (
    <>
      <DashboardListPage<AgentRunLog>
        filters={[
          {
            name: "userMessage",
            label: t("agentRunLog.filterUserMessage"),
            placeholder: t("agentRunLog.filterUserMessage"),
            defaultValue: "",
            trim: true,
            className: "min-w-0",
            inputClassName: "pl-9",
            icon: <SearchIcon className="size-4" />,
          },
          {
            name: "plannedAction",
            label: t("agentRunLog.plannedAction"),
            type: "select",
            defaultValue: "all",
            allValue: "all",
            options: actionOptions,
            placeholder: t("agentRunLog.plannedAction"),
            searchPlaceholder: t("agentRunLog.searchAction"),
            emptyText: t("agentRunLog.emptyAction"),
            className: "min-w-0",
          },
          {
            name: "finalAction",
            label: t("agentRunLog.finalAction"),
            type: "select",
            defaultValue: "all",
            allValue: "all",
            options: actionOptions,
            placeholder: t("agentRunLog.finalAction"),
            searchPlaceholder: t("agentRunLog.searchAction"),
            emptyText: t("agentRunLog.emptyAction"),
            className: "min-w-0",
          },
          {
            name: "finalStatus",
            label: t("agentRunLog.finalStatus"),
            type: "select",
            defaultValue: "all",
            allValue: "all",
            options: finalStatusOptions,
            placeholder: t("agentRunLog.finalStatus"),
            searchPlaceholder: t("agentRunLog.searchStatus"),
            emptyText: t("agentRunLog.emptyStatus"),
            className: "min-w-0",
          },
          {
            name: "hitlStatus",
            label: t("agentRunLog.hitlStatus"),
            type: "select",
            defaultValue: "all",
            allValue: "all",
            options: hitlStatusOptions,
            placeholder: t("agentRunLog.hitlStatus"),
            searchPlaceholder: t("agentRunLog.searchHitl"),
            emptyText: t("agentRunLog.emptyStatus"),
            className: "min-w-0",
          },
          {
            name: "aiAgentId",
            label: t("agentRunLog.selectAgent"),
            type: "select",
            defaultValue: "all",
            allValue: "all",
            options: aiAgentOptions,
            placeholder: t("agentRunLog.selectAgent"),
            searchPlaceholder: t("agentRunLog.searchAgent"),
            emptyText: t("agentRunLog.emptyAgent"),
            className: "min-w-0",
          },
        ]}
        fetchList={fetchAgentRunLogs}
        renderContent={({ result, loading }) =>
          !loading && result.results.length === 0 ? (
            <div className="py-14 text-center text-sm text-muted-foreground">
              {t("agentRunLog.emptyRows")}
            </div>
          ) : (
            <div>
              <div className="hidden grid-cols-[160px_minmax(0,1.8fr)_110px_minmax(0,1.2fr)_130px_90px_76px] gap-3 border-b border-[#dbe7f6] bg-[#f6f9ff] px-4 py-3 text-sm text-muted-foreground lg:grid">
                <div>{t("agentRunLog.time")}</div>
                <div>{t("agentRunLog.userMessage")}</div>
                <div>{t("agentRunLog.plannedAction")}</div>
                <div>{t("agentRunLog.skillTool")}</div>
                <div>{t("agentRunLog.finalStatus")}</div>
                <div className="text-right">{t("agentRunLog.duration")}</div>
                <div className="text-right">{t("agentRunLog.actions")}</div>
              </div>

              <div className="divide-y">
                {result.results.map((item) => (
                  <article
                    key={item.id}
                    className="grid grid-cols-1 gap-2 px-4 py-3 lg:grid-cols-[160px_minmax(0,1.8fr)_110px_minmax(0,1.2fr)_130px_90px_76px] lg:items-center lg:gap-3"
                  >
                    <div className="min-w-0 text-sm text-muted-foreground">
                      {formatDateTime(item.createdAt)}
                    </div>

                    <div className="min-w-0">
                      <UserMessagePreview value={item.userMessage} t={t} />
                      {item.errorMessage ? (
                        <div className="truncate text-xs text-destructive">{item.errorMessage}</div>
                      ) : null}
                    </div>

                    <div className="min-w-0">
                      <Badge variant={actionBadgeVariant(item.plannedAction)}>
                        {item.plannedAction || "-"}
                      </Badge>
                    </div>

                    <div className="min-w-0 text-sm">
                      {item.plannedSkillCode || item.graphToolCode || item.plannedToolCode ? (
                        <div className="min-w-0 space-y-1">
                          <div className="truncate font-medium">
                            {item.plannedSkillCode || item.graphToolCode || item.plannedToolCode}
                          </div>
                          {item.plannedSkillName ? (
                            <div className="truncate text-xs text-muted-foreground">
                              {item.plannedSkillName}
                            </div>
                          ) : item.handoffReason ? (
                            <div className="truncate text-xs text-muted-foreground">
                              {t("agentRunLog.handoffReason", { reason: item.handoffReason })}
                            </div>
                          ) : item.recommendedAction ? (
                            <div className="truncate text-xs text-muted-foreground">
                              {t("agentRunLog.routingRecommendation", { action: item.recommendedAction })}
                              {item.riskLevel ? ` / ${item.riskLevel} risk` : ""}
                              {item.ticketDraftReady ? ` / ${t("agentRunLog.draftReady")}` : ""}
                            </div>
                          ) : null}
                        </div>
                      ) : (
                        <span className="text-muted-foreground">-</span>
                      )}
                    </div>

                    <div className="min-w-0">
                      <div className="space-y-1">
                        <Badge variant={actionBadgeVariant(item.finalAction)}>
                          {item.finalAction || "-"}
                        </Badge>
                        <div className="truncate text-xs text-muted-foreground">
                          {getHitlStatusLabel(item.hitlStatus, t)
                            ? `${getHitlStatusLabel(item.hitlStatus, t)} / ${item.finalStatus || "-"}`
                            : item.finalStatus || "-"}
                        </div>
                      </div>
                    </div>

                    <div className="text-sm text-muted-foreground lg:text-right">
                      {item.latencyMs} ms
                    </div>

                    <div className="lg:text-right">
                      <Button
                        variant="outline"
                        size="sm"
                        onClick={() => {
                          setActiveLogId(item.id)
                          setDetailOpen(true)
                        }}
                      >
                        {t("agentRunLog.detail")}
                      </Button>
                    </div>
                  </article>
                ))}
              </div>
            </div>
          )
        }
        labels={{
          refresh: t("agentRunLog.refresh"),
          query: t("agentRunLog.query"),
          loading: t("agentRunLog.loadingRows"),
          empty: t("agentRunLog.emptyRows"),
          loadFailed: t("agentRunLog.loadFailed"),
        }}
      />
      <AgentRunLogDetailDialog
        open={detailOpen}
        logId={activeLogId}
        onOpenChange={(open) => {
          setDetailOpen(open)
          if (!open) {
            setActiveLogId(null)
          }
        }}
      />
    </>
  )
}

function UserMessagePreview({ value, t }: { value?: string; t: TFunction }) {
  const preview = useMemo(() => summarizeUserMessage(value, t), [value, t])

  return (
    <div className="truncate text-sm text-foreground">
      {preview}
    </div>
  )
}

function summarizeUserMessage(value: string | undefined, t: TFunction) {
  const normalized = value?.trim()
  if (!normalized) {
    return "-"
  }
  const text = extractTextFromHTML(normalized).replace(/\s+/g, " ").trim()
  if (text) {
    return text
  }
  if (containsHTML(normalized)) {
    if (/<img[\s>]/i.test(normalized)) {
      return t("agentRunLog.imageMessage")
    }
    return t("agentRunLog.richMessage")
  }
  return normalized
}

function containsHTML(value: string) {
  return /<[^>]+>/.test(value)
}

function extractTextFromHTML(value: string) {
  if (typeof window === "undefined") {
    return value
  }
  const doc = new DOMParser().parseFromString(value, "text/html")
  return doc.body.textContent || ""
}

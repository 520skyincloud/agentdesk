"use client"

import { useCallback, useEffect, useState } from "react"
import { RefreshCwIcon } from "lucide-react"
import { toast } from "sonner"

import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { Card, CardContent } from "@/components/ui/card"
import { useI18n } from "@/i18n/provider"
import {
  fetchDashboardOverview,
  type DashboardOverview,
  type DashboardRange,
} from "@/lib/api/dashboard"
import { SummaryCards } from "./summary-cards"
import { TrendPanel } from "./trend-panel"
import { TeamLoadPanel } from "./team-load-panel"
import { AlertList } from "./alert-list"

function LoadingCards() {
  return (
    <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-6">
      {Array.from({ length: 6 }).map((_, index) => (
        <Card key={index} className="agentdesk-surface rounded-2xl">
          <CardContent className="space-y-3 p-6">
            <Skeleton className="h-4 w-24" />
            <Skeleton className="h-8 w-20" />
            <Skeleton className="h-4 w-full" />
          </CardContent>
        </Card>
      ))}
    </div>
  )
}

export function DashboardHome() {
  const t = useI18n()
  const [range, setRange] = useState<DashboardRange>("7d")
  const [data, setData] = useState<DashboardOverview | null>(null)
  const [loading, setLoading] = useState(true)
  const [refreshing, setRefreshing] = useState(false)

  const loadData = useCallback(
    async (nextRange: DashboardRange, showRefreshing = false) => {
      if (showRefreshing) {
        setRefreshing(true)
      } else {
        setLoading(true)
      }
      try {
        const result = await fetchDashboardOverview(nextRange)
        setData(result)
      } catch (error) {
        toast.error(error instanceof Error ? error.message : t("dashboardHome.loadFailed"))
      } finally {
        setLoading(false)
        setRefreshing(false)
      }
    },
    [t]
  )

  useEffect(() => {
    void loadData(range)
  }, [loadData, range])

  const rangeOptions: Array<{ value: DashboardRange; label: string }> = [
    { value: "today", label: t("dashboardHome.rangeToday") },
    { value: "7d", label: t("dashboardHome.range7d") },
    { value: "30d", label: t("dashboardHome.range30d") },
  ]

  return (
    <div className="flex flex-1 flex-col gap-6 p-4 lg:p-6">
      <div className="agentdesk-surface flex flex-col gap-3 rounded-2xl p-5 xl:flex-row xl:items-start xl:justify-between">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">{t("dashboardHome.title")}</h1>
          <p className="mt-1 text-sm text-muted-foreground">
            {t("dashboardHome.description")}
            {data ? t("dashboardHome.updatedAt", { time: data.generatedAt }) : ""}
          </p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <div className="rounded-xl border border-[#dbe7f6] bg-[#f6f9ff] p-1 shadow-inner shadow-blue-100/40">
            {rangeOptions.map((item) => (
              <Button
                key={item.value}
                variant={range === item.value ? "secondary" : "ghost"}
                size="sm"
                onClick={() => setRange(item.value)}
              >
                {item.label}
              </Button>
            ))}
          </div>
          <Button
            variant="outline"
            size="sm"
            className="agentdesk-soft-button rounded-xl"
            onClick={() => void loadData(range, true)}
            disabled={loading || refreshing}
          >
            <RefreshCwIcon className={refreshing ? "mr-2 size-4 animate-spin" : "mr-2 size-4"} />
            {t("dashboardHome.refresh")}
          </Button>
        </div>
      </div>

      {loading && !data ? (
        <LoadingCards />
      ) : data ? (
        <>
          <SummaryCards summary={data.summary} />

          <TrendPanel
            title={t("dashboardHome.conversationTrend")}
            description={t("dashboardHome.conversationTrendDescription")}
            trend={data.conversationStats.trend}
            distribution={data.conversationStats.statusDistribution}
          />

          <div className="grid gap-4 xl:grid-cols-[1.15fr_0.85fr]">
            <TeamLoadPanel agentStats={data.agentStats} />

            <Card className="agentdesk-surface rounded-2xl">
              <CardContent className="grid gap-4 p-6 sm:grid-cols-2">
                <div className="rounded-2xl border border-[#dbe7f6] bg-[#f6f9ff] p-4 shadow-inner shadow-blue-100/30">
                  <div className="text-sm text-muted-foreground">{t("dashboardHome.enabledAiAgents")}</div>
                  <div className="mt-2 text-3xl font-semibold">{data.aiStats.enabledAiAgents}</div>
                </div>
                <div className="rounded-2xl border border-[#dbe7f6] bg-[#f6f9ff] p-4 shadow-inner shadow-blue-100/30">
                  <div className="text-sm text-muted-foreground">{t("dashboardHome.enabledChannels")}</div>
                  <div className="mt-2 text-3xl font-semibold">{data.aiStats.enabledChannels}</div>
                </div>
                <div className="rounded-2xl border border-[#dbe7f6] bg-[#f6f9ff] p-4 shadow-inner shadow-blue-100/30">
                  <div className="text-sm text-muted-foreground">{t("dashboardHome.todayKnowledgeRetrieves")}</div>
                  <div className="mt-2 text-3xl font-semibold">
                    {data.aiStats.todayKnowledgeRetrieves}
                  </div>
                </div>
                <div className="rounded-2xl border border-[#dbe7f6] bg-[#f6f9ff] p-4 shadow-inner shadow-blue-100/30">
                  <div className="text-sm text-muted-foreground">{t("dashboardHome.todayKnowledgeRetrieveFailRate")}</div>
                  <div className="mt-2 text-3xl font-semibold">
                    {data.aiStats.todayKnowledgeRetrieveFailRate.toFixed(1)}%
                  </div>
                </div>
                <div className="rounded-2xl border border-[#dbe7f6] bg-[#f6f9ff] p-4 shadow-inner shadow-blue-100/30">
                  <div className="text-sm text-muted-foreground">{t("dashboardHome.todaySkillRunFailCount")}</div>
                  <div className="mt-2 text-3xl font-semibold">
                    {data.aiStats.todaySkillRunFailCount}
                  </div>
                </div>
                <div className="rounded-2xl border border-[#dbe7f6] bg-[#f6f9ff] p-4 shadow-inner shadow-blue-100/30">
                  <div className="text-sm text-muted-foreground">{t("dashboardHome.todayAiHandoffCount")}</div>
                  <div className="mt-2 text-3xl font-semibold">
                    {data.aiStats.todayAiHandoffCount}
                  </div>
                </div>
              </CardContent>
            </Card>
          </div>

          <AlertList alerts={data.alerts} />
        </>
      ) : (
        <Card>
          <CardContent className="flex min-h-60 items-center justify-center p-6 text-sm text-muted-foreground">
            {t("dashboardHome.empty")}
          </CardContent>
        </Card>
      )}
    </div>
  )
}

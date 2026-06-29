"use client"

import Link from "next/link"
import {
  BotMessageSquareIcon,
  CircleDashedIcon,
  HeadsetIcon,
  SparklesIcon,
  WavesIcon,
} from "lucide-react"

import type { DashboardOverview } from "@/lib/api/dashboard"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { useI18n } from "@/i18n/provider"

type SummaryCardsProps = {
  summary: DashboardOverview["summary"]
}

type SummaryCardItem = {
  key: keyof DashboardOverview["summary"]
  titleKey: string
  descriptionKey: string
  link: string
  icon: typeof BotMessageSquareIcon
  format?: (value: number) => string
}

const cards: SummaryCardItem[] = [
  {
    key: "todayNewConversations",
    titleKey: "dashboardHome.summaryTodayNewConversations",
    descriptionKey: "dashboardHome.summaryTodayNewConversationsDescription",
    link: "/dashboard/conversations",
    icon: BotMessageSquareIcon,
  },
  {
    key: "processingConversations",
    titleKey: "dashboardHome.summaryProcessingConversations",
    descriptionKey: "dashboardHome.summaryProcessingConversationsDescription",
    link: "/dashboard/conversations",
    icon: WavesIcon,
  },
  {
    key: "pendingDispatchConversations",
    titleKey: "dashboardHome.summaryPendingDispatchConversations",
    descriptionKey: "dashboardHome.summaryPendingDispatchConversationsDescription",
    link: "/dashboard/conversations",
    icon: CircleDashedIcon,
  },
  {
    key: "onlineAgents",
    titleKey: "dashboardHome.summaryOnlineAgents",
    descriptionKey: "dashboardHome.summaryOnlineAgentsDescription",
    link: "/dashboard/agents",
    icon: HeadsetIcon,
  },
  {
    key: "aiServiceRate",
    titleKey: "dashboardHome.summaryAiServiceRate",
    descriptionKey: "dashboardHome.summaryAiServiceRateDescription",
    link: "/dashboard/conversations",
    icon: SparklesIcon,
    format: (value: number) => `${value.toFixed(1)}%`,
  },
]

export function SummaryCards({ summary }: SummaryCardsProps) {
  const t = useI18n()

  return (
    <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-6">
      {cards.map((item) => {
        const Icon = item.icon
        const rawValue = summary[item.key]
        const value =
          typeof item.format === "function"
            ? item.format(Number(rawValue))
            : Number(rawValue).toLocaleString()

        return (
          <Link key={item.key} href={item.link}>
            <Card className="h-full border-[#dce7f4] bg-card shadow-[0_10px_28px_rgba(35,74,122,0.06)] transition-all hover:-translate-y-0.5 hover:border-primary/35 hover:shadow-[0_14px_34px_rgba(35,74,122,0.09)] dark:border-border/60 dark:shadow-none">
              <CardHeader className="flex flex-row items-start justify-between space-y-0 pb-3">
                <div className="space-y-1">
                  <CardTitle className="text-sm font-medium">{t(item.titleKey)}</CardTitle>
                  <CardDescription>{t(item.descriptionKey)}</CardDescription>
                </div>
                <div className="rounded-xl bg-primary/10 p-2 text-primary">
                  <Icon className="size-4" />
                </div>
              </CardHeader>
              <CardContent>
                <div className="text-3xl font-semibold tracking-tight">{value}</div>
              </CardContent>
            </Card>
          </Link>
        )
      })}
    </div>
  )
}

import { request } from "@/lib/api/client"
import type { AnalyticsOverview } from "@/lib/api/service-analytics"

export type DashboardRange = "today" | "7d" | "30d"

export type DashboardOverview = AnalyticsOverview

export function fetchDashboardOverview(range: DashboardRange) {
  return request<DashboardOverview>(`/api/dashboard/dashboard/overview?range=${range}`)
}

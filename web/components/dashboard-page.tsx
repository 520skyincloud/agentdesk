"use client"

import type { ReactNode } from "react"

import { cn } from "@/lib/utils"
import { TableCell, TableRow } from "@/components/ui/table"
import { useI18n } from "@/i18n/provider"

export function DashboardPage({
  className,
  children,
}: {
  className?: string
  children: ReactNode
}) {
  return (
    <div className={cn("flex flex-1 flex-col gap-5 p-4 lg:p-6", className)}>
      {children}
    </div>
  )
}

export function DashboardToolbar({
  className,
  actions,
  children,
}: {
  className?: string
  actions?: ReactNode
  children: ReactNode
}) {
  return (
    <div
      className={cn(
        "flex flex-col gap-3 rounded-2xl border border-[#dce7f4] bg-card p-4 shadow-[0_10px_30px_rgba(35,74,122,0.06)] lg:flex-row lg:items-center lg:justify-between dark:border-border/60 dark:shadow-none",
        className
      )}
    >
      <div className="flex min-w-0 flex-1 flex-col gap-2 sm:flex-row sm:flex-wrap sm:items-center">
        {children}
      </div>
      {actions ? (
        <div className="flex shrink-0 flex-wrap items-center gap-2">{actions}</div>
      ) : null}
    </div>
  )
}

export function DashboardTableShell({
  className,
  pagination,
  children,
}: {
  className?: string
  pagination?: ReactNode
  children: ReactNode
}) {
  return (
    <div className={cn("space-y-4", className)}>
      <div className="overflow-hidden rounded-2xl border border-[#dce7f4] bg-card text-card-foreground shadow-[0_12px_34px_rgba(35,74,122,0.06)] dark:border-border/60 dark:shadow-none">
        {children}
      </div>
      {pagination ? <div>{pagination}</div> : null}
    </div>
  )
}

export function DashboardTableStateRow({
  colSpan,
  loading,
  loadingText,
  emptyText,
}: {
  colSpan: number
  loading?: boolean
  loadingText?: string
  emptyText?: string
}) {
  const t = useI18n()

  return (
    <TableRow>
      <TableCell colSpan={colSpan} className="py-12 text-center text-muted-foreground">
        {loading ? (loadingText ?? t("common.loadingData")) : (emptyText ?? t("common.emptyData"))}
      </TableCell>
    </TableRow>
  )
}

export function DashboardTableSummary({
  children,
}: {
  children: ReactNode
}) {
  return (
    <div className="flex items-center justify-between text-sm text-muted-foreground">
      {children}
    </div>
  )
}

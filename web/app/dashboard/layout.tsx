"use client"

import { Loader2Icon } from "lucide-react"
import { usePathname, useRouter } from "next/navigation"
import type { CSSProperties, ReactNode } from "react"
import { useEffect } from "react"

import { AppSidebar } from "@/components/app-sidebar"
import { useAuth } from "@/components/auth-provider"
import { NotificationProvider } from "@/components/notification-provider"
import { SiteHeader } from "@/components/site-header"
import { useI18n } from "@/i18n/provider"
import {
  dashboardPathIsAccessible,
  dashboardPathRequiresTenant,
  firstAccessibleDashboardPath,
} from "@/lib/navigation"
import { SidebarInset, SidebarProvider } from "@/components/ui/sidebar"

export default function DashboardLayout({
  children,
}: {
  children: ReactNode
}) {
  const t = useI18n()
  const { ready, session } = useAuth()
  const pathname = usePathname()
  const router = useRouter()
  const isLoginRoute = pathname?.startsWith("/dashboard/login") ?? false
  const navContext = {
    isPlatformAccount: Boolean(session?.isPlatformAccount),
    hasActiveTenant: (session?.activeTenantId ?? 0) > 0,
  }
  const missingTenantContext = Boolean(
    ready &&
      session?.isPlatformAccount &&
      session.activeTenantId <= 0 &&
      dashboardPathRequiresTenant(pathname)
  )
  const fallbackPath = session
    ? firstAccessibleDashboardPath(session.permissions, navContext, session.roles)
    : null
  const routeAccessible = session
    ? dashboardPathIsAccessible(
        pathname,
        session.permissions,
        navContext,
        session.roles
      )
    : true
  const inaccessibleRoute = Boolean(
    ready &&
      session &&
      !missingTenantContext &&
      !routeAccessible &&
      !(pathname === "/dashboard" && !fallbackPath)
  )
  const tenantContextKey = session
    ? `${session.user.id}:${session.activeTenantId}`
    : "anonymous"

  useEffect(() => {
    if (ready && !session && !isLoginRoute) {
      router.replace("/dashboard/login")
    }
  }, [isLoginRoute, ready, router, session])

  useEffect(() => {
    if (missingTenantContext) {
      router.replace("/dashboard/channels")
    }
  }, [missingTenantContext, router])

  useEffect(() => {
    if (inaccessibleRoute) {
      router.replace(fallbackPath ?? "/dashboard")
    }
  }, [fallbackPath, inaccessibleRoute, router])

  if (isLoginRoute) {
    return <>{children}</>
  }

  if (!ready || !session || missingTenantContext || inaccessibleRoute) {
    return (
      <div className="flex min-h-screen items-center justify-center bg-[linear-gradient(160deg,#f3f1e8_0%,#f8faf5_46%,#e8f7f2_100%)] p-6">
        <div className="flex items-center gap-3">
          <div className="flex size-10 items-center justify-center rounded-full bg-primary/10 text-primary">
            <Loader2Icon className="size-5 animate-spin" />
          </div>
          <div className="space-y-1">
            <p className="text-base font-medium">{t("auth.checkingSession")}</p>
            <p className="text-sm text-muted-foreground">
              {t("auth.syncingProfile")}
            </p>
          </div>
        </div>
      </div>
    )
  }

  return (
    <SidebarProvider
      className="h-svh min-h-0 overflow-hidden"
      style={
        {
          "--sidebar-width": "calc(var(--spacing) * 54)",
          "--header-height": "calc(var(--spacing) * 12)",
        } as CSSProperties
      }
    >
      <NotificationProvider key={tenantContextKey}>
        <AppSidebar variant="inset" />
        <SidebarInset className="overflow-hidden border border-[#dce7f4] bg-background shadow-[0_18px_48px_rgba(35,74,122,0.08)] dark:border-border/60 dark:shadow-none">
          <SiteHeader />
          <div className="@container/main flex min-h-0 flex-1 flex-col gap-2 overflow-auto bg-background">
            {children}
          </div>
        </SidebarInset>
      </NotificationProvider>
    </SidebarProvider>
  )
}

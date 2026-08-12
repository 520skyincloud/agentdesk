"use client"

import type { ComponentProps } from "react"
import Link from "next/link"
import { useMemo } from "react"

import { useI18n } from "@/i18n/provider"
import {
  filterDashboardNavForSession,
  filterDashboardSecondaryNavForSession,
} from "@/lib/navigation"
import { useAuth } from "@/components/auth-provider"
import { NavMain } from "@/components/nav-main"
import { NavSecondary } from "@/components/nav-secondary"
import { NavUser } from "@/components/nav-user"
import { TenantContextSwitcher } from "@/components/tenant-context-switcher"
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarHeader,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
} from "@/components/ui/sidebar"

export function AppSidebar({ ...props }: ComponentProps<typeof Sidebar>) {
  const t = useI18n()
  const { session } = useAuth()
  const navContext = useMemo(
    () => ({
      isPlatformAccount: Boolean(session?.isPlatformAccount),
      hasActiveTenant: (session?.activeTenantId ?? 0) > 0,
    }),
    [session?.activeTenantId, session?.isPlatformAccount]
  )
  const navSections = useMemo(
    () => filterDashboardNavForSession(session?.permissions, navContext, session?.roles),
    [navContext, session?.permissions, session?.roles]
  )
  const secondaryNavItems = useMemo(
    () => filterDashboardSecondaryNavForSession(session?.permissions, navContext),
    [navContext, session?.permissions]
  )
  const brandHref = navContext.hasActiveTenant ? "/dashboard" : "/dashboard/channels"
  const user = {
    name: session?.user.nickname || session?.user.username || t("common.notSignedIn"),
    email: session?.user.username || t("common.guest"),
    avatar: session?.user.avatar || "",
  }

  return (
    <Sidebar collapsible="icon" className="border-r border-sidebar-border bg-sidebar" {...props}>
      <SidebarHeader className="gap-2 px-3 pt-3">
        <SidebarMenu>
          <SidebarMenuItem>
            <SidebarMenuButton
              className="h-12 rounded-xl data-[slot=sidebar-menu-button]:p-2!"
              render={<Link href={brandHref} />}
            >
              <img
                src="/images/zhixi-weibao-logo.png"
                alt={t("app.brand")}
                width="32"
                height="32"
                className="size-7 shrink-0 object-contain"
              />
              <span className="text-base font-semibold tracking-tight text-sidebar-foreground">{t("app.brand")}</span>
            </SidebarMenuButton>
          </SidebarMenuItem>
        </SidebarMenu>
        <TenantContextSwitcher />
      </SidebarHeader>
      <SidebarContent>
        {navSections.map((section) => (
          <NavMain
            key={section.titleKey}
            icon={section.icon}
            sectionKey={section.titleKey}
            title={t(section.titleKey)}
            items={section.items.map((item) => ({
              ...item,
              title: t(item.titleKey),
            }))}
          />
        ))}
        {secondaryNavItems.length > 0 ? (
          <NavSecondary items={secondaryNavItems} className="mt-auto" />
        ) : null}
      </SidebarContent>
      <SidebarFooter>
        <NavUser user={user} />
      </SidebarFooter>
    </Sidebar>
  )
}

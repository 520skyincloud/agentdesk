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
  const navSections = useMemo(
    () => filterDashboardNavForSession(session?.permissions, session?.roles),
    [session?.permissions, session?.roles]
  )
  const secondaryNavItems = useMemo(
    () => filterDashboardSecondaryNavForSession(session?.permissions, session?.roles),
    [session?.permissions, session?.roles]
  )
  const user = {
    name: session?.user.nickname || session?.user.username || t("common.notSignedIn"),
    email: session?.user.username || t("common.guest"),
    avatar: session?.user.avatar || "",
  }

  return (
    <Sidebar collapsible="icon" {...props}>
      <SidebarHeader>
        <SidebarMenu>
          <SidebarMenuItem>
            <SidebarMenuButton
              className="data-[slot=sidebar-menu-button]:p-1.5!"
              render={<Link href="/dashboard" />}
            >
              <img
                src="/images/logo.svg"
                alt={t("app.brand")}
                width="32"
                height="32"
                className="size-7 shrink-0 object-contain"
              />
              <span className="text-base font-semibold">{t("app.brand")}</span>
            </SidebarMenuButton>
          </SidebarMenuItem>
        </SidebarMenu>
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

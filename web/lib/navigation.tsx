import {
  ActivitySquareIcon,
  BotMessageSquareIcon,
  BrainCircuitIcon,
  Building2Icon,
  CalendarClockIcon,
  FileCheck2Icon,
  FileTextIcon,
  HardDriveIcon,
  HomeIcon,
  ServerCogIcon,
  KeyRoundIcon,
  LayoutDashboardIcon,
  MessageSquareCodeIcon,
  MessageSquareShareIcon,
  MessageSquareMoreIcon,
  NetworkIcon,
  SlidersHorizontalIcon,
  ShieldCheckIcon,
  TagsIcon,
  UserCogIcon,
  UsersIcon,
} from "lucide-react";
import type { ReactNode } from "react";

import { isDashboardNavItemActive } from "@/lib/navigation-active";

export type DashboardNavMenuItem = {
  title: string;
  titleKey: string;
  url: string;
  icon: ReactNode;
};

export type DashboardNavItemConfig = Omit<DashboardNavMenuItem, "title"> & {
  /**
   * Keep in sync with backend Permission.Code. Missing value means any signed-in
   * admin can see the module.
   */
  requiredPermission?: string;
  context?: DashboardNavContextScope;
};

export type DashboardNavSectionConfig = {
  titleKey: string;
  icon: ReactNode;
  context: DashboardNavContextScope;
  items: DashboardNavItemConfig[];
};

export type DashboardNavContextScope = "always" | "platform" | "tenant";

export type DashboardNavContext = {
  isPlatformAccount: boolean;
  hasActiveTenant: boolean;
};

type DashboardRouteAccessRule = {
  url: string;
  requiredPermission?: string;
  context: DashboardNavContextScope;
};

function contextVisible(
  scope: DashboardNavContextScope,
  context: DashboardNavContext,
): boolean {
  if (scope === "platform") {
    return context.isPlatformAccount;
  }
  if (scope === "tenant") {
    return context.hasActiveTenant;
  }
  return true;
}

function navItemVisible(
  item: DashboardNavItemConfig,
  permissionSet: Set<string>,
  context: DashboardNavContext,
): boolean {
  if (!contextVisible(item.context ?? "always", context)) {
    return false;
  }
  if (!item.requiredPermission) {
    return true;
  }
  return permissionSet.has(item.requiredPermission);
}

export function filterDashboardNavForSession(
  permissions: readonly string[] | undefined,
  context: DashboardNavContext,
): { titleKey: string; icon: ReactNode; items: DashboardNavMenuItem[] }[] {
  const permissionSet = new Set(permissions ?? []);
  return dashboardNavSections
    .filter((section) => contextVisible(section.context, context))
    .map((section) => ({
      titleKey: section.titleKey,
      icon: section.icon,
      items: section.items
        .filter((item) => navItemVisible(item, permissionSet, context))
        .map(({ titleKey, url, icon }) => ({ title: titleKey, titleKey, url, icon })),
    }))
    .filter((section) => section.items.length > 0);
}

export function filterDashboardSecondaryNavForSession(
  permissions: readonly string[] | undefined,
  context: DashboardNavContext,
): DashboardNavMenuItem[] {
  const permissionSet = new Set(permissions ?? []);
  return dashboardSecondaryNav
    .filter((item) => navItemVisible(item, permissionSet, context))
    .map(({ titleKey, url, icon }) => ({ title: titleKey, titleKey, url, icon }));
}

const dashboardSupplementalRouteAccessRules: DashboardRouteAccessRule[] = [
  {
    url: "/dashboard/notifications",
    requiredPermission: "notification.view",
    context: "tenant",
  },
];

const retiredDashboardRoutes = [
  "/dashboard/companies",
  "/dashboard/company-detail",
] as const;

function findDashboardRouteAccessRule(
  pathname: string | null | undefined,
): DashboardRouteAccessRule | null {
  if (!pathname) {
    return null;
  }
  for (const section of dashboardNavSections) {
    for (const item of section.items) {
      if (isDashboardNavItemActive(pathname, item.url)) {
        return {
          url: item.url,
          requiredPermission: item.requiredPermission,
          context: item.context ?? section.context,
        };
      }
    }
  }
  return dashboardSupplementalRouteAccessRules.find((rule) =>
    isDashboardNavItemActive(pathname, rule.url),
  ) ?? null;
}

export function dashboardPathIsAccessible(
  pathname: string | null | undefined,
  permissions: readonly string[] | undefined,
  context: DashboardNavContext,
): boolean {
  if (retiredDashboardRoutes.some((url) => isDashboardNavItemActive(pathname ?? "", url))) {
    return false;
  }
  const rule = findDashboardRouteAccessRule(pathname);
  if (!rule) {
    return true;
  }
  if (!contextVisible(rule.context, context)) {
    return false;
  }
  return !rule.requiredPermission || new Set(permissions ?? []).has(rule.requiredPermission);
}

export function firstAccessibleDashboardPath(
  permissions: readonly string[] | undefined,
  context: DashboardNavContext,
): string | null {
  for (const section of filterDashboardNavForSession(permissions, context)) {
    const item = section.items[0];
    if (item) {
      return item.url;
    }
  }
  return null;
}

export function dashboardPathRequiresTenant(pathname: string | null | undefined): boolean {
  if (!pathname) {
    return false;
  }
  return dashboardNavSections.some((section) =>
    section.items.some((item) => {
      const scope = item.context ?? section.context;
      return scope === "tenant" && isDashboardNavItemActive(pathname, item.url);
    }),
  );
}

export const dashboardNavSections: DashboardNavSectionConfig[] = [
  // {
  //   title: "Overview",
  //   items: [
  //     {
  //       title: "Overview",
  //       url: "/",
  //       icon: <LayoutDashboardIcon />,
  //     },
  //   ],
  // },
  {
    titleKey: "nav.companyWorkspace",
    icon: <BotMessageSquareIcon />,
    context: "tenant",
    items: [
      {
        titleKey: "nav.overview",
        url: "/dashboard",
        icon: <LayoutDashboardIcon />,
        requiredPermission: "dashboard.view",
      },
      {
        titleKey: "nav.serviceAnalytics",
        url: "/dashboard/service-analytics",
        icon: <ActivitySquareIcon />,
        requiredPermission: "serviceAnalytics.view",
      },
      {
        titleKey: "nav.conversations",
        url: "/dashboard/conversations",
        icon: <BotMessageSquareIcon />,
        requiredPermission: "conversation.view",
      },
      {
        titleKey: "nav.conversationDispatch",
        url: "/dashboard/conversation-dispatch",
        icon: <MessageSquareShareIcon />,
        requiredPermission: "conversation.handover",
      },
      {
        titleKey: "nav.conversationRecords",
        url: "/dashboard/conversation-monitor",
        icon: <FileCheck2Icon />,
        requiredPermission: "conversationRecord.view",
      },
      {
        titleKey: "nav.tickets",
        url: "/dashboard/tickets",
        icon: <FileTextIcon />,
        requiredPermission: "ticket.view",
      },
      {
        titleKey: "nav.customers",
        url: "/dashboard/customers",
        icon: <UsersIcon />,
        requiredPermission: "customer.view",
      },
    ],
  },
  {
    titleKey: "nav.customerServiceOrganization",
    icon: <UserCogIcon />,
    context: "tenant",
    items: [
      {
        titleKey: "nav.storeWorkbench",
        url: "/dashboard/store-workbench",
        icon: <HomeIcon />,
        requiredPermission: "storeWorkbench.view",
      },
      {
        titleKey: "nav.agents",
        url: "/dashboard/agents",
        icon: <UserCogIcon />,
        requiredPermission: "agent.view",
      },
      {
        titleKey: "nav.agentTeamSchedules",
        url: "/dashboard/agent-team-schedules",
        icon: <CalendarClockIcon />,
        requiredPermission: "agentTeamSchedule.view",
      },
      {
        titleKey: "nav.wxworkProtocolInstances",
        url: "/dashboard/wxwork-protocol-instances",
        icon: <NetworkIcon />,
        requiredPermission: "channel.view",
      },
    ],
  },
  {
    titleKey: "nav.serviceCapabilities",
    icon: <BrainCircuitIcon />,
    context: "tenant",
    items: [
      {
        titleKey: "nav.channelSettings",
        url: "/dashboard/settings",
        icon: <NetworkIcon />,
        requiredPermission: "channel.view",
      },
      {
        titleKey: "nav.knowledge",
        url: "/dashboard/knowledge",
        icon: <FileTextIcon />,
        requiredPermission: "knowledgeBase.view",
      },
      {
        titleKey: "nav.knowledgeCandidates",
        url: "/dashboard/knowledge-candidates",
        icon: <FileCheck2Icon />,
        requiredPermission: "knowledgeBase.view",
      },
      {
        titleKey: "nav.quickReplies",
        url: "/dashboard/quick-replies",
        icon: <MessageSquareMoreIcon />,
        requiredPermission: "quickReply.view",
      },
      {
        titleKey: "nav.tags",
        url: "/dashboard/tags",
        icon: <TagsIcon />,
        requiredPermission: "tag.view",
      },
      {
        titleKey: "nav.skillDefinition",
        url: "/dashboard/skill-definition",
        icon: <MessageSquareCodeIcon />,
        requiredPermission: "skillDefinition.view",
      },
      {
        titleKey: "nav.replyIntentConfigs",
        url: "/dashboard/reply-intent-configs",
        icon: <SlidersHorizontalIcon />,
        requiredPermission: "aiConfig.view",
      },
    ],
  },
  {
    titleKey: "nav.accessManagement",
    icon: <ShieldCheckIcon />,
    context: "always",
    items: [
      {
        titleKey: "nav.users",
        url: "/dashboard/users",
        icon: <UsersIcon />,
        requiredPermission: "user.view",
        context: "tenant",
      },
      {
        titleKey: "nav.roles",
        url: "/dashboard/roles",
        icon: <ShieldCheckIcon />,
        requiredPermission: "role.view",
      },
      {
        titleKey: "nav.permissions",
        url: "/dashboard/permissions",
        icon: <KeyRoundIcon />,
        requiredPermission: "permission.view",
      },
    ],
  },
  {
    titleKey: "nav.platformManagement",
    icon: <Building2Icon />,
    context: "platform",
    items: [
      {
        titleKey: "nav.channels",
        url: "/dashboard/channels",
        icon: <Building2Icon />,
        requiredPermission: "tenant.view",
      },
      {
        titleKey: "nav.aiConfigs",
        url: "/dashboard/ai-configs",
        icon: <BrainCircuitIcon />,
        requiredPermission: "aiConfig.view",
      },
      {
        titleKey: "nav.agentRunLogs",
        url: "/dashboard/agent-run-logs",
        icon: <ActivitySquareIcon />,
        requiredPermission: "agentRunLog.view",
      },
      {
        titleKey: "nav.storageSettings",
        url: "/dashboard/storage-settings",
        icon: <HardDriveIcon />,
        requiredPermission: "storageSetting.view",
      },
      {
        titleKey: "nav.wxworkDevicePool",
        url: "/dashboard/wxwork-device-pool",
        icon: <ServerCogIcon />,
        requiredPermission: "wxworkDevicePool.view",
      },
      {
        titleKey: "nav.mcp",
        url: "/dashboard/mcp",
        icon: <MessageSquareCodeIcon />,
        requiredPermission: "mcp.view",
      },
      {
        titleKey: "nav.replyIntentProfiles",
        url: "/dashboard/reply-intent-profiles",
        icon: <BrainCircuitIcon />,
        requiredPermission: "aiConfig.view",
      },
    ],
  },
];

export const dashboardSecondaryNav: DashboardNavItemConfig[] = [
  // {
  //   title: "System Settings",
  //   url: "/settings",
  //   icon: <Settings2Icon />,
  // },
  // {
  //   title: "Help Center",
  //   url: "/help",
  //   icon: <LifeBuoyIcon />,
  // },
];

export const dashboardQuickActions = [
  {
    title: "View Conversations",
    icon: <BotMessageSquareIcon />,
  },
  {
    title: "Invite Members",
    icon: <UserCogIcon />,
  },
  {
    title: "Connect Bot",
    icon: <MessageSquareCodeIcon />,
  },
] as const;

export function getPageTitle(pathname: string): string {
  return getPageTitleKey(pathname);
}

export function getPageTitleKey(pathname: string): string {
  let matchedTitle = "nav.dashboardHome";
  let longestMatch = 0;

  for (const section of dashboardNavSections) {
    for (const item of section.items) {
      if (pathname === item.url || pathname.startsWith(item.url + "/")) {
        const matchLength = item.url.length;
        if (matchLength > longestMatch) {
          longestMatch = matchLength;
          matchedTitle = item.titleKey;
        }
      }
    }
  }

  for (const item of dashboardSecondaryNav) {
    if (pathname === item.url || pathname.startsWith(item.url + "/")) {
      const matchLength = item.url.length;
      if (matchLength > longestMatch) {
        longestMatch = matchLength;
        matchedTitle = item.titleKey;
      }
    }
  }

  return matchedTitle;
}

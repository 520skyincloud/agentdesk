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
  Link2Icon,
  MessageSquareCodeIcon,
  MessageSquareShareIcon,
  MessageSquareMoreIcon,
  NetworkIcon,
  ReceiptTextIcon,
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
  allowedRoles?: readonly DashboardBuiltinRole[];
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
  allowedRoles?: readonly DashboardBuiltinRole[];
};

const dashboardBuiltinRoles = [
  "super_admin",
  "admin",
  "tenant_admin",
  "cs_team_leader",
  "cs_user",
  "store_staff",
] as const;

type DashboardBuiltinRole = (typeof dashboardBuiltinRoles)[number];

const builtinRoleSet = new Set<string>(dashboardBuiltinRoles);
const platformAdminRoles = ["super_admin", "admin"] as const;
const tenantManagementRoles = ["super_admin", "admin", "tenant_admin"] as const;
const tenantSupervisorRoles = [
  "super_admin",
  "admin",
  "tenant_admin",
  "cs_team_leader",
] as const;
const customerServiceRoles = [
  "super_admin",
  "admin",
  "tenant_admin",
  "cs_team_leader",
  "cs_user",
] as const;
const conversationRoles = [...customerServiceRoles, "store_staff"] as const;
const billingRoles = [...tenantSupervisorRoles, "store_staff"] as const;
const storeWorkbenchRoles = ["super_admin", "admin", "store_staff"] as const;

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
  roles?: readonly string[],
): boolean {
  if (!contextVisible(item.context ?? "always", context)) {
    return false;
  }
  if (!roleVisible(item.allowedRoles, roles)) {
    return false;
  }
  if (!item.requiredPermission) {
    return true;
  }
  return permissionSet.has(item.requiredPermission);
}

function roleVisible(
  allowedRoles: readonly DashboardBuiltinRole[] | undefined,
  roles: readonly string[] | undefined,
): boolean {
  const builtinRoles = (roles ?? []).filter((role): role is DashboardBuiltinRole =>
    builtinRoleSet.has(role),
  );
  if (builtinRoles.length === 0 || !allowedRoles) {
    return true;
  }
  const allowedRoleSet = new Set<string>(allowedRoles);
  return builtinRoles.some((role) => allowedRoleSet.has(role));
}

export function filterDashboardNavForSession(
  permissions: readonly string[] | undefined,
  context: DashboardNavContext,
  roles?: readonly string[],
): { titleKey: string; icon: ReactNode; items: DashboardNavMenuItem[] }[] {
  const permissionSet = new Set(permissions ?? []);
  return dashboardNavSections
    .filter((section) => contextVisible(section.context, context))
    .map((section) => ({
      titleKey: section.titleKey,
      icon: section.icon,
      items: section.items
        .filter((item) => navItemVisible(item, permissionSet, context, roles))
        .map(({ titleKey, url, icon }) => ({ title: titleKey, titleKey, url, icon })),
    }))
    .filter((section) => section.items.length > 0);
}

export function filterDashboardSecondaryNavForSession(
  permissions: readonly string[] | undefined,
  context: DashboardNavContext,
  roles?: readonly string[],
): DashboardNavMenuItem[] {
  const permissionSet = new Set(permissions ?? []);
  return dashboardSecondaryNav
    .filter((item) => navItemVisible(item, permissionSet, context, roles))
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
  "/dashboard/settings",
  "/dashboard/reply-intent-profiles",
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
          allowedRoles: item.allowedRoles,
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
  roles?: readonly string[],
): boolean {
  if (retiredDashboardRoutes.some((url) => isDashboardNavItemActive(pathname ?? "", url))) {
    return false;
  }
  const rule = findDashboardRouteAccessRule(pathname);
  if (!rule) {
    return false;
  }
  if (!contextVisible(rule.context, context)) {
    return false;
  }
  if (!roleVisible(rule.allowedRoles, roles)) {
    return false;
  }
  return !rule.requiredPermission || new Set(permissions ?? []).has(rule.requiredPermission);
}

export function firstAccessibleDashboardPath(
  permissions: readonly string[] | undefined,
  context: DashboardNavContext,
  roles?: readonly string[],
): string | null {
  for (const section of filterDashboardNavForSession(permissions, context, roles)) {
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
    context: "always",
    items: [
      {
        titleKey: "nav.overview",
        url: "/dashboard",
        icon: <LayoutDashboardIcon />,
        requiredPermission: "dashboard.view",
        context: "tenant",
        allowedRoles: customerServiceRoles,
      },
      {
        titleKey: "nav.serviceAnalytics",
        url: "/dashboard/service-analytics",
        icon: <ActivitySquareIcon />,
        requiredPermission: "serviceAnalytics.view",
        context: "tenant",
        allowedRoles: tenantSupervisorRoles,
      },
      {
        titleKey: "nav.conversations",
        url: "/dashboard/conversations",
        icon: <BotMessageSquareIcon />,
        requiredPermission: "conversation.view",
        context: "tenant",
        allowedRoles: conversationRoles,
      },
      {
        titleKey: "nav.conversationDispatch",
        url: "/dashboard/conversation-dispatch",
        icon: <MessageSquareShareIcon />,
        requiredPermission: "conversation.handover",
        context: "tenant",
        allowedRoles: tenantSupervisorRoles,
      },
      {
        titleKey: "nav.conversationRecords",
        url: "/dashboard/conversation-monitor",
        icon: <FileCheck2Icon />,
        requiredPermission: "conversationRecord.view",
        context: "tenant",
        allowedRoles: customerServiceRoles,
      },
      {
        titleKey: "nav.tickets",
        url: "/dashboard/tickets",
        icon: <FileTextIcon />,
        requiredPermission: "ticket.view",
        context: "tenant",
        allowedRoles: customerServiceRoles,
      },
      {
        titleKey: "nav.customers",
        url: "/dashboard/customers",
        icon: <UsersIcon />,
        requiredPermission: "customer.view",
        context: "tenant",
        allowedRoles: customerServiceRoles,
      },
      {
        titleKey: "nav.modelBilling",
        url: "/dashboard/billing-query",
        icon: <ReceiptTextIcon />,
        requiredPermission: "billing.view",
        context: "always",
        allowedRoles: billingRoles,
      },
    ],
  },
  {
    titleKey: "nav.customerServiceOrganization",
    icon: <UserCogIcon />,
    context: "tenant",
    items: [
      {
        titleKey: "nav.stores",
        url: "/dashboard/stores",
        icon: <Building2Icon />,
        requiredPermission: "store.view",
        allowedRoles: customerServiceRoles,
      },
      {
        titleKey: "nav.storeWorkbench",
        url: "/dashboard/store-workbench",
        icon: <HomeIcon />,
        requiredPermission: "storeWorkbench.view",
        allowedRoles: storeWorkbenchRoles,
      },
      {
        titleKey: "nav.agents",
        url: "/dashboard/agents",
        icon: <UserCogIcon />,
        requiredPermission: "agent.view",
        allowedRoles: customerServiceRoles,
      },
      {
        titleKey: "nav.agentTeamSchedules",
        url: "/dashboard/agent-team-schedules",
        icon: <CalendarClockIcon />,
        requiredPermission: "agentTeamSchedule.view",
        allowedRoles: tenantSupervisorRoles,
      },
      {
        titleKey: "nav.wxworkProtocolInstances",
        url: "/dashboard/wxwork-protocol-instances",
        icon: <NetworkIcon />,
        requiredPermission: "channel.view",
        allowedRoles: tenantSupervisorRoles,
      },
      {
        titleKey: "nav.arrivalConnections",
        url: "/dashboard/arrival-connections",
        icon: <Link2Icon />,
        requiredPermission: "arrivalConnection.view",
        allowedRoles: customerServiceRoles,
      },
    ],
  },
  {
    titleKey: "nav.serviceCapabilities",
    icon: <BrainCircuitIcon />,
    context: "tenant",
    items: [
      {
        titleKey: "nav.knowledge",
        url: "/dashboard/knowledge",
        icon: <FileTextIcon />,
        requiredPermission: "knowledgeBase.view",
        allowedRoles: tenantManagementRoles,
      },
      {
        titleKey: "nav.knowledgeCandidates",
        url: "/dashboard/knowledge-candidates",
        icon: <FileCheck2Icon />,
        requiredPermission: "knowledgeBase.view",
        allowedRoles: tenantManagementRoles,
      },
      {
        titleKey: "nav.quickReplies",
        url: "/dashboard/quick-replies",
        icon: <MessageSquareMoreIcon />,
        requiredPermission: "quickReply.view",
        allowedRoles: tenantSupervisorRoles,
      },
      {
        titleKey: "nav.tags",
        url: "/dashboard/tags",
        icon: <TagsIcon />,
        requiredPermission: "tag.view",
        allowedRoles: tenantSupervisorRoles,
      },
      {
        titleKey: "nav.skillDefinition",
        url: "/dashboard/skill-definition",
        icon: <MessageSquareCodeIcon />,
        requiredPermission: "skillDefinition.view",
        allowedRoles: tenantManagementRoles,
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
        allowedRoles: tenantManagementRoles,
      },
      {
        titleKey: "nav.roles",
        url: "/dashboard/roles",
        icon: <ShieldCheckIcon />,
        requiredPermission: "role.view",
        allowedRoles: tenantManagementRoles,
      },
      {
        titleKey: "nav.permissions",
        url: "/dashboard/permissions",
        icon: <KeyRoundIcon />,
        requiredPermission: "permission.view",
        allowedRoles: tenantManagementRoles,
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
        allowedRoles: platformAdminRoles,
      },
      {
        titleKey: "nav.modelProfiles",
        url: "/dashboard/model-profiles",
        icon: <BrainCircuitIcon />,
        requiredPermission: "aiConfig.view",
        allowedRoles: platformAdminRoles,
      },
      {
        titleKey: "nav.agentRunLogs",
        url: "/dashboard/agent-run-logs",
        icon: <ActivitySquareIcon />,
        requiredPermission: "agentRunLog.view",
        allowedRoles: platformAdminRoles,
      },
      {
        titleKey: "nav.storageSettings",
        url: "/dashboard/storage-settings",
        icon: <HardDriveIcon />,
        requiredPermission: "storageSetting.view",
        allowedRoles: platformAdminRoles,
      },
      {
        titleKey: "nav.wxworkDevicePool",
        url: "/dashboard/wxwork-device-pool",
        icon: <ServerCogIcon />,
        requiredPermission: "wxworkDevicePool.view",
        allowedRoles: platformAdminRoles,
      },
      {
        titleKey: "nav.mcp",
        url: "/dashboard/mcp",
        icon: <MessageSquareCodeIcon />,
        requiredPermission: "mcp.view",
        allowedRoles: platformAdminRoles,
      },
      {
        titleKey: "nav.replyIntentConfigs",
        url: "/dashboard/reply-intent-configs",
        icon: <SlidersHorizontalIcon />,
        requiredPermission: "aiConfig.view",
        allowedRoles: platformAdminRoles,
      },
      {
        titleKey: "nav.industryTagTemplates",
        url: "/dashboard/industry-tag-templates",
        icon: <TagsIcon />,
        requiredPermission: "aiConfig.view",
        allowedRoles: platformAdminRoles,
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

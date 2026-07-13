"use client";

import {
  PanelLeftCloseIcon,
  PanelLeftOpenIcon,
  PlusIcon,
  RefreshCwIcon,
  UserCogIcon,
} from "lucide-react";
import { useCallback, useEffect, useMemo, useState } from "react";

import {
  DashboardCrudPage,
  type DashboardCrudColumn,
  type DashboardCrudFilter,
} from "@/components/dashboard/crud";
import { useAuth } from "@/components/auth-provider";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import {
  createAgentProfile,
  deleteAgentProfile,
  fetchAgentProfiles,
  updateAgentProfile,
  type AdminAgentProfile,
  type AdminAgentTeam,
  type CreateAdminAgentProfilePayload,
} from "@/lib/api/admin";
import { useI18n } from "@/i18n/provider";
import { ServiceStatus } from "@/lib/generated/enums";
import { formatDateTime } from "@/lib/utils";
import { EditDialog } from "./_components/edit";
import { AgentTeamSidebar } from "./_components/team-sidebar";
import { SquadArrangement } from "./_components/squad-arrangement";

type TFunction = (key: string, values?: Record<string, string | number>) => string;

function getServiceStatusOptions(t: TFunction) {
  return [
    { value: "all", label: t("agentProfile.allStatuses") },
    { value: String(ServiceStatus.Idle), label: t("agentProfile.statusIdle") },
    { value: String(ServiceStatus.Busy), label: t("agentProfile.statusBusy") },
  ];
}

function getStatusLabel(value: number, t: TFunction) {
  return (
    getServiceStatusOptions(t).find((item) => item.value === String(value))
      ?.label ?? String(value)
  );
}

export default function DashboardAgentsPage() {
  const t = useI18n();
  const { session } = useAuth();
  const serviceStatusOptions = useMemo(() => getServiceStatusOptions(t), [t]);
  const [selectedTeam, setSelectedTeam] = useState<AdminAgentTeam | null>(null);
  const [sidebarCollapsed, setSidebarCollapsed] = useState(false);
  const [loadRefreshKey, setLoadRefreshKey] = useState(0);
  const [activeView, setActiveView] = useState("members");
  const [squadCreateRequestKey, setSquadCreateRequestKey] = useState(0);
  const permissions = new Set(session?.permissions ?? []);
  const canCreateAgent = Boolean(
    selectedTeam?.manageable && permissions.has("agent.create"),
  );
  const canUpdateAgent = Boolean(
    selectedTeam?.manageable && permissions.has("agent.update"),
  );
  const canDeleteAgent = Boolean(
    selectedTeam?.manageable && permissions.has("agent.delete"),
  );

  const handleTeamsChange = useCallback((nextTeams: AdminAgentTeam[]) => {
    setSelectedTeam((current) => {
      if (nextTeams.length === 0) {
        return null;
      }
      if (!current) {
        return nextTeams[0];
      }
      return nextTeams.find((item) => item.id === current.id) ?? nextTeams[0];
    });
  }, []);

  useEffect(() => {
    const timer = window.setInterval(() => {
      setLoadRefreshKey((value) => value + 1);
    }, 20_000);
    return () => window.clearInterval(timer);
  }, []);

  const filters = useMemo<DashboardCrudFilter[]>(
    () => [
      {
        name: "displayName",
        label: t("agentProfile.filterDisplayName"),
        placeholder: t("agentProfile.filterDisplayName"),
        defaultValue: "",
        trim: true,
        className: "w-full sm:w-72",
      },
      {
        name: "agentCode",
        label: t("agentProfile.filterAgentCode"),
        placeholder: t("agentProfile.filterAgentCode"),
        defaultValue: "",
        trim: true,
        className: "w-full sm:w-44",
      },
      {
        name: "serviceStatus",
        label: t("agentProfile.allStatuses"),
        type: "select",
        defaultValue: "all",
        allValue: "all",
        options: serviceStatusOptions,
        className: "w-full sm:w-36",
      },
    ],
    [serviceStatusOptions, t],
  );

  const columns = useMemo<DashboardCrudColumn<AdminAgentProfile>[]>(
    () => [
      {
        key: "agent",
        label: t("agentProfile.columnAgent"),
        className: "min-w-[170px]",
        render: (item) => (
          <div className="flex items-start gap-3">
            <div className="agentdesk-icon-tile mt-0.5 overflow-hidden">
              {item.avatar ? (
                <img
                  src={item.avatar}
                  alt={item.displayName}
                  className="size-full object-cover"
                />
              ) : (
                <UserCogIcon className="size-4 text-muted-foreground" />
              )}
            </div>
            <div className="min-w-0">
              <div className="font-medium">{item.displayName}</div>
              <div className="text-xs text-muted-foreground">
                {item.nickname ||
                  item.username ||
                  t("agentProfile.userFallback", { id: item.userId })}
              </div>
              <div className="mt-1 text-xs text-muted-foreground">
                {t("agentProfile.agentCode", { code: item.agentCode })}
              </div>
              <div className="mt-2 flex flex-wrap items-center gap-1 md:hidden">
                <Badge variant="outline">
                  {getStatusLabel(item.serviceStatus, t)}
                </Badge>
                <span className="text-[11px] text-muted-foreground">
                  {t("agentProfile.mobileCapacity", {
                    count: item.maxConcurrentCount,
                  })}
                </span>
              </div>
            </div>
          </div>
        ),
      },
      {
        key: "rules",
        label: t("agentProfile.columnRules"),
        className: "hidden md:table-cell",
        render: (item) => (
          <>
            <Badge variant="outline">
              {getStatusLabel(item.serviceStatus, t)}
            </Badge>
            <div className="mt-2 text-sm text-muted-foreground">
              {t("agentProfile.capacityPriority", {
                capacity: item.maxConcurrentCount,
                priority: item.priorityLevel,
              })}
            </div>
          </>
        ),
      },
      {
        key: "dispatch",
        label: t("agentProfile.columnDispatch"),
        className: "hidden lg:table-cell",
        render: (item) => (
          <div className="flex flex-wrap gap-1.5">
            <Badge variant={item.autoAssignEnabled ? "secondary" : "outline"}>
              {item.autoAssignEnabled
                ? t("agentProfile.autoAssign")
                : t("agentProfile.noAutoAssign")}
            </Badge>
            <Badge
              variant={item.receiveOfflineMessage ? "secondary" : "outline"}
            >
              {item.receiveOfflineMessage
                ? t("agentProfile.offlineReceive")
                : t("agentProfile.noOfflineReceive")}
            </Badge>
          </div>
        ),
      },
      {
        key: "load",
        label: t("agentProfile.columnLoad"),
        className: "min-w-[105px]",
        render: (item) =>
          item.activeTaskCount > 0 ? (
            <div className="space-y-1 text-xs tabular-nums">
              <div className="font-medium text-foreground">
                {t("agentProfile.activeTasks", {
                  count: item.activeTaskCount,
                })}
                <span className="text-muted-foreground">
                  {" "}/ {item.maxConcurrentCount}
                </span>
              </div>
              <div className="flex flex-wrap gap-x-2 gap-y-1">
                <span
                  className={
                    item.pendingReplyCount > 0
                      ? "font-medium text-destructive"
                      : "text-muted-foreground"
                  }
                >
                  {t("agentProfile.pendingReplies", {
                    count: item.pendingReplyCount,
                  })}
                </span>
                <span className="text-muted-foreground">
                  {t("agentProfile.processingTasks", {
                    count: item.processingTaskCount,
                  })}
                </span>
              </div>
            </div>
          ) : (
            <span className="text-xs text-muted-foreground">
              {t("agentProfile.noActiveTasks")}
            </span>
          ),
      },
      {
        key: "recent",
        label: t("agentProfile.columnRecent"),
        className: "hidden xl:table-cell",
        render: (item) => (
          <>
            <div className="text-sm">
              {t("agentProfile.onlineAt", {
                time: formatDateTime(item.lastOnlineAt),
              })}
            </div>
            <div className="text-sm text-muted-foreground">
              {t("agentProfile.statusAt", {
                time: formatDateTime(item.lastStatusAt),
              })}
            </div>
          </>
        ),
      },
    ],
    [t],
  );

  return (
    <div className="flex h-[calc(100vh-4rem)] min-h-0 flex-col overflow-hidden lg:flex-row">
      <div
        className={`shrink-0 overflow-hidden transition-[width,height] duration-200 ${
          sidebarCollapsed
            ? "h-0 w-full lg:h-full lg:w-0"
            : "h-64 w-full lg:h-full lg:w-80"
        }`}
      >
        <AgentTeamSidebar
          selectedTeamId={selectedTeam?.id ?? null}
          onSelectTeam={setSelectedTeam}
          onTeamsChange={handleTeamsChange}
          onCreateSquad={(team) => {
            setSelectedTeam(team);
            setActiveView("squads");
            setSquadCreateRequestKey((value) => value + 1);
          }}
        />
      </div>
      <div className="relative hidden shrink-0 bg-background lg:block">
        <Button
          variant="outline"
          size="icon"
          className="absolute top-4 left-1/2 z-10 size-7 -translate-x-1/2 rounded-full border-border bg-background shadow-sm"
          onClick={() => setSidebarCollapsed((value) => !value)}
          aria-label={
            sidebarCollapsed
              ? t("agentProfile.expandTeams")
              : t("agentProfile.collapseTeams")
          }
        >
          {sidebarCollapsed ? (
            <PanelLeftOpenIcon className="size-3.5" />
          ) : (
            <PanelLeftCloseIcon className="size-3.5" />
          )}
        </Button>
      </div>
      <div className="min-h-0 min-w-0 flex-1 overflow-auto p-3 sm:p-4 lg:p-6">
        <Tabs value={activeView} onValueChange={setActiveView} className="h-full min-h-0">
          <div className="mb-4 flex flex-wrap items-center justify-between gap-3">
            <div className="min-w-0">
              <h1 className="truncate text-lg font-semibold">{selectedTeam?.name ?? t("agentProfile.teamTitle")}</h1>
              <p className="text-sm text-muted-foreground">{selectedTeam?.description || t("agentProfile.teamWorkspaceDescription")}</p>
            </div>
            <TabsList variant="line">
              <TabsTrigger value="members">{t("agentProfile.memberView")}</TabsTrigger>
              <TabsTrigger value="squads">{t("agentProfile.squadView")}</TabsTrigger>
              <TabsTrigger value="scope">{t("agentProfile.scopeView")}</TabsTrigger>
            </TabsList>
          </div>
          <TabsContent value="members" className="min-h-0">
          <div className="flex h-full flex-col gap-6">
          <DashboardCrudPage<AdminAgentProfile, CreateAdminAgentProfilePayload>
            key={selectedTeam?.id ?? "all"}
            layout="fragment"
            tableShellClassName="min-h-0"
            reloadKey={`${selectedTeam?.id ?? 0}:${loadRefreshKey}`}
            filters={filters}
            columns={columns}
            fetchList={(query) =>
              fetchAgentProfiles({
                teamId: selectedTeam?.id,
                displayName:
                  typeof query.displayName === "string"
                    ? query.displayName
                    : undefined,
                agentCode:
                  typeof query.agentCode === "string"
                    ? query.agentCode
                    : undefined,
                serviceStatus:
                  typeof query.serviceStatus === "string"
                    ? query.serviceStatus
                    : undefined,
                page: Number(query.page),
                limit: Number(query.limit),
              })
            }
            getItemId={(item) => item.id}
            createItem={createAgentProfile}
            updateItem={(item, payload) =>
              updateAgentProfile({ id: item.id, ...payload })
            }
            deleteItem={(item) => deleteAgentProfile(item.id)}
            canEdit={() => canUpdateAgent}
            canDelete={() => canDeleteAgent}
            renderToolbarActions={({ onRefresh, onCreate, loading }) => (
              <>
                <Button variant="outline" onClick={onRefresh} disabled={loading}>
                  <RefreshCwIcon className={loading ? "animate-spin" : undefined} />
                  {t("agentProfile.refresh")}
                </Button>
                {canCreateAgent ? (
                  <Button onClick={onCreate}>
                    <PlusIcon />
                    {t("agentProfile.new")}
                  </Button>
                ) : null}
              </>
            )}
            renderEditDialog={({
              open,
              saving,
              itemId,
              onOpenChange,
              onSubmit,
            }) => (
              <EditDialog
                open={open}
                saving={saving}
                itemId={itemId}
                defaultTeamId={selectedTeam?.id ?? null}
                onOpenChange={onOpenChange}
                onSubmit={onSubmit}
              />
            )}
            labels={{
              refresh: t("agentProfile.refresh"),
              create: t("agentProfile.new"),
              query: t("agentProfile.query"),
              loading: t("agentProfile.loadingRows"),
              empty: selectedTeam
                ? t("agentProfile.emptyTeamRows")
                : t("agentProfile.emptyRows"),
              actions: t("agentProfile.columnActions"),
              edit: t("agentProfile.edit"),
              delete: t("agentProfile.delete"),
              processing: t("agentProfile.deleting"),
              moreActions: (item) =>
                t("agentProfile.moreActions", { name: item.displayName }),
              loadFailed: t("agentProfile.loadFailed"),
              saveFailed: t("agentProfile.saveFailed"),
              deleteFailed: t("agentProfile.deleteFailed"),
              created: (payload) =>
                t("agentProfile.created", { name: payload.displayName }),
              updated: (item) =>
                t("agentProfile.updated", { name: item.displayName }),
              deleted: (item) =>
                t("agentProfile.deleted", { name: item.displayName }),
            }}
          />
        </div>
          </TabsContent>
          <TabsContent value="squads" className="min-h-0 overflow-hidden">
            {selectedTeam ? (
              <SquadArrangement
                team={selectedTeam}
                createRequestKey={squadCreateRequestKey}
                canCreate={selectedTeam.manageable && permissions.has("agentTeam.create")}
                canUpdate={selectedTeam.manageable && permissions.has("agentTeam.update")}
                canDelete={selectedTeam.manageable && permissions.has("agentTeam.delete")}
              />
            ) : (
              <div className="py-16 text-center text-muted-foreground">{t("agentProfile.selectTeamFirst")}</div>
            )}
          </TabsContent>
          <TabsContent value="scope" className="min-h-0">
            {selectedTeam ? (
              <div className="grid gap-4 border bg-background p-5 sm:grid-cols-3">
                <div><div className="text-sm text-muted-foreground">{t("agentProfile.serviceStoreStaff")}</div><div className="mt-1 text-2xl font-semibold tabular-nums">{selectedTeam.storeStaffUserIds.length}</div></div>
                <div><div className="text-sm text-muted-foreground">{t("agentProfile.serviceWxWorkInstances")}</div><div className="mt-1 text-2xl font-semibold tabular-nums">{selectedTeam.wxWorkInstanceScopeIds.length}</div></div>
                <div><div className="text-sm text-muted-foreground">{t("agentProfile.coveredStores")}</div><div className="mt-1 text-2xl font-semibold tabular-nums">{selectedTeam.storeScopeIds.length}</div></div>
              </div>
            ) : null}
          </TabsContent>
        </Tabs>
      </div>
    </div>
  );
}

"use client";

import { BotMessageSquareIcon, PowerIcon } from "lucide-react";
import { useMemo } from "react";

import {
  DashboardCrudPage,
  createDashboardStatusColumn,
  createDashboardStatusToggleAction,
  type DashboardCrudColumn,
  type DashboardCrudFilter,
} from "@/components/dashboard/crud";
import { Badge } from "@/components/ui/badge";
import {
  createAIAgent,
  deleteAIAgent,
  fetchAIAgents,
  updateAIAgent,
  updateAIAgentSort,
  updateAIAgentStatus,
  type AIAgent,
  type CreateAIAgentPayload,
} from "@/lib/api/admin";
import { IMConversationServiceMode, Status } from "@/lib/generated/enums";
import { useI18n } from "@/i18n/provider";
import { EditDialog } from "./_components/edit";

type TFunction = (key: string, values?: Record<string, string | number>) => string;

function getStatusOptions(t: TFunction) {
  return [
    { value: "all", label: t("aiAgent.allStatuses") },
    { value: String(Status.Ok), label: t("aiAgent.enabled") },
    { value: String(Status.Disabled), label: t("aiAgent.disabled") },
    { value: String(Status.Deleted), label: t("status.deleted") },
  ];
}

function getStatusLabel(value: string, t: TFunction) {
  return (
    getStatusOptions(t).find((item) => item.value === value)?.label ??
    t("aiAgent.allStatuses")
  );
}

function getServiceModeLabel(mode: number, t: TFunction) {
  switch (mode) {
    case IMConversationServiceMode.AIOnly:
      return t("aiAgent.serviceAiOnly");
    case IMConversationServiceMode.HumanOnly:
      return t("aiAgent.serviceHumanOnly");
    case IMConversationServiceMode.AIFirst:
      return t("aiAgent.serviceAiFirst");
    default:
      return "-";
  }
}

function getNextStatus(item: AIAgent) {
  return item.status === Status.Ok ? Status.Disabled : Status.Ok;
}

export default function DashboardAIAgentsPage() {
  const t = useI18n();
  const statusOptions = useMemo(() => getStatusOptions(t), [t]);

  const filters = useMemo<DashboardCrudFilter[]>(
    () => [
      {
        name: "name",
        label: t("aiAgent.filterName"),
        placeholder: t("aiAgent.filterName"),
        defaultValue: "",
        trim: true,
        className: "w-full sm:w-56",
      },
      {
        name: "status",
        label: t("aiAgent.allStatuses"),
        type: "select",
        defaultValue: "all",
        allValue: "all",
        options: statusOptions,
        className: "w-full sm:w-52",
      },
    ],
    [statusOptions, t],
  );

  const columns = useMemo<DashboardCrudColumn<AIAgent>[]>(
    () => [
      {
        key: "agent",
        label: "Agent",
        render: (item) => (
          <div className="flex items-center gap-3">
            <div className="agentdesk-icon-tile">
              <BotMessageSquareIcon className="size-4" />
            </div>
            <div className="font-medium">{item.name}</div>
          </div>
        ),
      },
      {
        key: "aiConfig",
        label: t("aiAgent.columnAiConfig"),
        render: (item) => item.aiConfigName || "-",
      },
      {
        key: "serviceMode",
        label: t("aiAgent.columnServiceMode"),
        render: (item) => getServiceModeLabel(item.serviceMode, t),
      },
      {
        key: "knowledge",
        label: t("aiAgent.columnKnowledge"),
        render: (item) => {
          const knowledgeIds = item.knowledgeIds ?? [];
          const knowledgeBaseNames = item.knowledgeBaseNames ?? [];
          return (
            <div className="flex flex-wrap gap-1">
              {knowledgeIds.length === 0 ? (
                <span className="text-sm text-muted-foreground">
                  {t("aiAgent.notConfigured")}
                </span>
              ) : (
                knowledgeBaseNames.map((name, index) => (
                  <Badge
                    key={knowledgeIds[index] ?? `${item.id}-${index}`}
                    variant="secondary"
                  >
                    {name}
                  </Badge>
                ))
              )}
            </div>
          );
        },
      },
      {
        key: "skills",
        label: t("aiAgent.columnSkills"),
        render: (item) => {
          const skills = item.skills ?? [];
          return (
            <div className="flex flex-wrap gap-1">
              {skills.length === 0 ? (
                <span className="text-sm text-muted-foreground">
                  {t("aiAgent.ragOnly")}
                </span>
              ) : (
                skills.map((skill) => (
                  <Badge key={skill.id} variant="outline">
                    {skill.name}
                  </Badge>
                ))
              )}
            </div>
          );
        },
      },
      {
        key: "capabilities",
        label: t("aiAgent.columnCapabilities"),
        render: (item) => {
          const skills = item.skills ?? [];
          const directTools = item.directTools ?? [];
          const directToolServerCodes = Array.from(
            new Set(directTools.map((tool) => tool.serverCode).filter(Boolean)),
          );
          return (
            <div className="space-y-2">
              <div className="flex flex-wrap gap-1">
                <Badge variant="secondary">{skills.length} Skills</Badge>
                <Badge variant="secondary">{directTools.length} Tools</Badge>
              </div>
              <div className="flex flex-wrap gap-1">
                {directToolServerCodes.length === 0 ? (
                  <span className="text-sm text-muted-foreground">
                    {t("aiAgent.noMcpServer")}
                  </span>
                ) : (
                  directToolServerCodes.map((serverCode) => (
                    <Badge key={serverCode} variant="outline">
                      {serverCode}
                    </Badge>
                  ))
                )}
              </div>
            </div>
          );
        },
      },
      createDashboardStatusColumn<AIAgent, number>({
        label: t("aiAgent.columnStatus"),
        getStatus: (item) => item.status,
        getLabel: (status) => getStatusLabel(String(status), t),
        getBadgeVariant: (status) =>
          status === Status.Ok ? "default" : "secondary",
        isEnabled: (status) => status === Status.Ok,
        toggle: {
          getNextStatus,
          updateStatus: (item, nextStatus) =>
            updateAIAgentStatus(item.id, nextStatus),
          successMessage: (item, nextStatus) =>
            t("aiAgent.statusChanged", {
              name: item.name,
              status:
                nextStatus === Status.Ok
                  ? t("aiAgent.enabled")
                  : t("aiAgent.stop"),
            }),
          errorMessage: t("aiAgent.statusUpdateFailed"),
          ariaLabel: (item) => t("aiAgent.toggleStatus", { name: item.name }),
        },
      }),
    ],
    [t],
  );

  return (
    <DashboardCrudPage<AIAgent, CreateAIAgentPayload>
      filters={filters}
      columns={columns}
      fetchList={(query) =>
        fetchAIAgents({
          name: typeof query.name === "string" ? query.name : undefined,
          status: typeof query.status === "string" ? query.status : undefined,
          page: Number(query.page),
          limit: Number(query.limit),
        })
      }
      getItemId={(item) => item.id}
      createItem={createAIAgent}
      updateItem={(item, payload) => updateAIAgent({ id: item.id, ...payload })}
      deleteItem={(item) => deleteAIAgent(item.id)}
      rowActions={[
        createDashboardStatusToggleAction<AIAgent, number>({
          icon: <PowerIcon />,
          label: (item) =>
            item.status === Status.Ok ? t("aiAgent.stop") : t("aiAgent.enabled"),
          getNextStatus,
          updateStatus: (item, nextStatus) =>
            updateAIAgentStatus(item.id, nextStatus),
          successMessage: (item, nextStatus) =>
            t("aiAgent.statusChanged", {
              name: item.name,
              status:
                nextStatus === Status.Ok
                  ? t("aiAgent.enabled")
                  : t("aiAgent.stop"),
            }),
          errorMessage: t("aiAgent.statusUpdateFailed"),
        }),
      ]}
      sort={{
        enabled: true,
        onReorder: (items) => updateAIAgentSort(items.map((item) => item.id)),
        successMessage: t("aiAgent.sortUpdated"),
        errorMessage: t("aiAgent.sortUpdateFailed"),
        handleLabel: t("aiAgent.dragSort", { name: "" }),
      }}
      renderEditDialog={({ open, saving, itemId, onOpenChange, onSubmit }) => (
        <EditDialog
          open={open}
          saving={saving}
          itemId={itemId}
          onOpenChange={onOpenChange}
          onSubmit={onSubmit}
        />
      )}
      labels={{
        refresh: t("aiAgent.refresh"),
        create: t("aiAgent.new"),
        query: t("aiAgent.query"),
        loading: t("aiAgent.loadingRows"),
        empty: t("aiAgent.emptyRows"),
        actions: t("aiAgent.columnActions"),
        edit: t("aiAgent.edit"),
        delete: t("aiAgent.delete"),
        processing: t("aiAgent.processing"),
        moreActions: (item) => t("aiAgent.moreActions", { name: item.name }),
        loadFailed: t("aiAgent.loadFailed"),
        saveFailed: t("aiAgent.saveFailed"),
        deleteFailed: t("aiAgent.deleteFailed"),
        created: (payload) => t("aiAgent.created", { name: payload.name }),
        updated: (_item, payload) => t("aiAgent.updated", { name: payload.name }),
        deleted: (item) => t("aiAgent.deleted", { name: item.name }),
      }}
    />
  );
}

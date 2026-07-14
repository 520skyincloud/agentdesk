"use client"

import { useMemo } from "react"
import {
  Building2Icon,
  Globe2Icon,
  MessageCircleMoreIcon,
  NetworkIcon,
  PlusIcon,
  RefreshCwIcon,
} from "lucide-react"

import { useAuth } from "@/components/auth-provider"
import {
  createDashboardStatusColumn,
  DashboardCrudPage,
} from "@/components/dashboard/crud"
import { DashboardPage } from "@/components/dashboard-page"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { useI18n } from "@/i18n/provider"
import {
  createChannel,
  deleteChannel,
  fetchChannels,
  updateChannel,
  updateChannelStatus,
  type AdminChannel,
  type CreateAdminChannelPayload,
} from "@/lib/api/admin"
import { getEnumOptions } from "@/lib/enums"
import { Status, StatusLabels } from "@/lib/generated/enums"
import { ChannelEditDialog } from "./_components/channel-edit"

function getChannelTypeLabel(channelType: string, t: (key: string) => string) {
  if (channelType === "wechat_mp") return t("channel.typeWechatMp")
  if (channelType === "wxwork_protocol") return t("channel.typeWxworkProtocol")
  if (channelType === "wxwork_kf") {
    return `${t("channel.typeWxworkKf")} (${t("channel.legacy")})`
  }
  if (channelType === "wxwork_cli") {
    return `${t("channel.typeWxworkCli")} (${t("channel.legacy")})`
  }
  if (channelType === "web") return t("channel.typeWeb")
  return channelType || "-"
}

function isEditableChannel(item: AdminChannel) {
  return ["web", "wechat_mp", "wxwork_protocol"].includes(item.channelType)
}

function getStatusLabel(status: Status, t: (key: string) => string) {
  if (status === Status.Disabled) return t("status.disabled")
  if (status === Status.Deleted) return t("status.deleted")
  return t("status.ok")
}

function ChannelIcon({ channelType }: { channelType: string }) {
  if (channelType === "wechat_mp") {
    return <MessageCircleMoreIcon className="size-4" />
  }
  if (channelType === "wxwork_protocol") {
    return <NetworkIcon className="size-4" />
  }
  if (channelType === "web") return <Globe2Icon className="size-4" />
  return <Building2Icon className="size-4" />
}

export default function DashboardSettingsPage() {
  const t = useI18n()
  const { ready, session } = useAuth()
  const permissions = useMemo(
    () => new Set(session?.permissions ?? []),
    [session?.permissions]
  )
  const canView = permissions.has("channel.view")
  const canCreate = permissions.has("channel.create")
  const canUpdate = permissions.has("channel.update")
  const canDelete = permissions.has("channel.delete")
  const showActionsColumn = canUpdate || canDelete
  const statusOptions = [
    { value: "all", label: t("status.all") },
    ...getEnumOptions(StatusLabels).map((option) => ({
      value: String(option.value),
      label: getStatusLabel(option.value as Status, t),
    })),
  ]
  const channelTypeOptions = [
    { value: "all", label: t("channel.allTypes") },
    { value: "web", label: t("channel.typeWeb") },
    { value: "wechat_mp", label: t("channel.typeWechatMp") },
    { value: "wxwork_protocol", label: t("channel.typeWxworkProtocol") },
  ]

  if (!ready) return null

  if (!canView) {
    return (
      <DashboardPage>
        <div className="flex min-h-56 items-center justify-center rounded-lg border bg-card px-6 text-center text-sm text-muted-foreground">
          {t("channel.viewDenied")}
        </div>
      </DashboardPage>
    )
  }

  return (
    <DashboardCrudPage<AdminChannel, CreateAdminChannelPayload>
      filters={[
        {
          name: "name",
          label: t("channel.filterName"),
          placeholder: t("channel.filterName"),
          defaultValue: "",
          trim: true,
          className: "w-full sm:w-56",
        },
        {
          name: "channelId",
          label: t("channel.filterChannelId"),
          placeholder: t("channel.filterChannelId"),
          defaultValue: "",
          trim: true,
          className: "w-full sm:w-64",
        },
        {
          name: "channelType",
          label: t("channel.allTypes"),
          type: "select",
          defaultValue: "all",
          allValue: "all",
          options: channelTypeOptions,
          className: "w-full sm:w-44",
        },
        {
          name: "status",
          label: t("status.all"),
          type: "select",
          defaultValue: "all",
          allValue: "all",
          options: statusOptions,
          className: "w-full sm:w-36",
        },
      ]}
      columns={[
        {
          key: "channel",
          label: t("channel.columnChannel"),
          className: "min-w-52",
          render: (item) => (
            <div className="flex items-center gap-3">
              <div className="flex size-9 shrink-0 items-center justify-center rounded-lg border border-[#dbe7f6] bg-[#f6f9ff] text-primary">
                <ChannelIcon channelType={item.channelType} />
              </div>
              <div className="min-w-0">
                <div className="truncate font-medium">{item.name}</div>
                <div className="text-xs text-muted-foreground">
                  {getChannelTypeLabel(item.channelType, t)}
                </div>
              </div>
            </div>
          ),
        },
        {
          key: "type",
          label: t("channel.columnType"),
          render: (item) => (
            <Badge variant="outline">
              {getChannelTypeLabel(item.channelType, t)}
            </Badge>
          ),
        },
        {
          key: "channelId",
          label: t("channel.columnChannelId"),
          className: "min-w-48",
          render: (item) => (
            <span className="font-mono text-xs">{item.channelId || "-"}</span>
          ),
        },
        {
          key: "agent",
          label: t("channel.columnAgent"),
          className: "min-w-36",
          render: (item) => item.aiAgentName || "-",
        },
        createDashboardStatusColumn<AdminChannel, Status>({
          label: t("channel.columnStatus"),
          getStatus: (item) => item.status as Status,
          getLabel: (status) => getStatusLabel(status, t),
          getBadgeVariant: (status) =>
            status === Status.Ok ? "default" : "outline",
          isEnabled: (status) => status === Status.Ok,
          toggle: canUpdate
            ? {
                disabled: (item) => !isEditableChannel(item),
                getNextStatus: (item) =>
                  item.status === Status.Ok ? Status.Disabled : Status.Ok,
                updateStatus: (item, nextStatus) =>
                  updateChannelStatus(item.id, nextStatus),
                successMessage: (item, nextStatus) =>
                  t(
                    nextStatus === Status.Ok
                      ? "channel.statusEnabled"
                      : "channel.statusDisabled",
                    { name: item.name }
                  ),
                errorMessage: t("channel.statusUpdateFailed"),
                ariaLabel: (item) =>
                  t("channel.toggleStatus", { name: item.name }),
              }
            : undefined,
        }),
      ]}
      fetchList={fetchChannels}
      getItemId={(item) => item.id}
      createItem={createChannel}
      updateItem={(item, payload) => updateChannel({ id: item.id, ...payload })}
      showEdit={(item) => canUpdate && isEditableChannel(item)}
      deleteItem={canDelete ? (item) => deleteChannel(item.id) : undefined}
      showActionsColumn={showActionsColumn}
      renderToolbarActions={({ onRefresh, onCreate, loading }) => (
        <>
          <Button variant="outline" onClick={onRefresh} disabled={loading}>
            <RefreshCwIcon className={loading ? "animate-spin" : undefined} />
            {t("channel.refresh")}
          </Button>
          {canCreate ? (
            <Button onClick={onCreate}>
              <PlusIcon />
              {t("channel.new")}
            </Button>
          ) : null}
        </>
      )}
      renderEditDialog={({ open, saving, itemId, onOpenChange, onSubmit }) => (
        <ChannelEditDialog
          open={open}
          saving={saving}
          itemId={itemId}
          canResetSecret={canUpdate}
          onOpenChange={onOpenChange}
          onSubmit={onSubmit}
        />
      )}
      labels={{
        refresh: t("channel.refresh"),
        create: t("channel.new"),
        query: t("channel.query"),
        loading: t("channel.loading"),
        empty: t("channel.empty"),
        actions: t("channel.columnActions"),
        edit: t("channel.edit"),
        delete: t("channel.delete"),
        processing: t("channel.processing"),
        moreActions: (item) => t("channel.moreActions", { name: item.name }),
        loadFailed: t("channel.loadFailed"),
        saveFailed: t("channel.saveFailed"),
        deleteFailed: t("channel.deleteFailed"),
        created: (payload) => t("channel.created", { name: payload.name }),
        updated: (_item, payload) => t("channel.updated", { name: payload.name }),
        deleted: (item) => t("channel.deleted", { name: item.name }),
      }}
    />
  )
}

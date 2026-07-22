"use client"

import { useMemo, useState } from "react"
import { useRouter } from "next/navigation"
import {
  Building2Icon,
  BrainCircuitIcon,
  Clock3Icon,
  HeadphonesIcon,
  LogInIcon,
  PlusIcon,
  RefreshCwIcon,
  ShieldCheckIcon,
  StoreIcon,
  UserRoundCogIcon,
  UsersRoundIcon,
} from "lucide-react"
import { toast } from "sonner"

import { useAuth } from "@/components/auth-provider"
import {
  createDashboardStatusColumn,
  DashboardCrudPage,
  type DashboardCrudRowAction,
} from "@/components/dashboard/crud"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { useI18n } from "@/i18n/provider"
import {
  createTenant,
  fetchTenants,
  updateTenant,
  updateTenantStatus,
  type AdminTenant,
  type CreateTenantResult,
} from "@/lib/api/tenant"
import { setActiveTenantId } from "@/lib/auth"
import {
  Status,
  TenantVerificationStatus,
} from "@/lib/generated/enums"
import { formatDateTime } from "@/lib/utils"
import { TenantCreationResultDialog } from "./_components/creation-result"
import {
  TenantEditDialog,
  type TenantFormPayload,
} from "./_components/edit"
import { TenantModelAccessDialog } from "./_components/model-access"

function getStatusLabel(status: Status, t: (key: string) => string) {
  return t(status === Status.Ok ? "status.ok" : "status.disabled")
}

function getVerificationLabel(
  status: TenantVerificationStatus,
  t: (key: string) => string
) {
  if (status === TenantVerificationStatus.Verified) {
    return t("tenant.verificationVerified")
  }
  if (status === TenantVerificationStatus.Rejected) {
    return t("tenant.verificationRejected")
  }
  return t("tenant.verificationPending")
}

export default function DashboardChannelsPage() {
  const t = useI18n()
  const router = useRouter()
  const { session, refreshProfile } = useAuth()
  const permissions = useMemo(
    () => new Set(session?.permissions ?? []),
    [session?.permissions]
  )
  const canCreate = permissions.has("tenant.create")
  const canUpdate = permissions.has("tenant.update")
  const canUpdateStatus = permissions.has("tenant.updateStatus")
  const canSwitch = permissions.has("tenant.switch") && Boolean(session?.canSwitchTenant)
  const canViewModelAccess = permissions.has("tenantModelGrant.view")
  const canUpdateModelAccess = permissions.has("tenantModelGrant.update")
  const showActionsColumn = canUpdate || canSwitch || canViewModelAccess
  const [creationResult, setCreationResult] =
    useState<CreateTenantResult | null>(null)
  const [modelAccessTenant, setModelAccessTenant] =
    useState<AdminTenant | null>(null)

  const rowActions = useMemo<DashboardCrudRowAction<AdminTenant>[]>(
    () => {
      const actions: DashboardCrudRowAction<AdminTenant>[] = []
      if (canViewModelAccess) {
        actions.push({
          key: "model-access",
          label: "模型授权",
          icon: <BrainCircuitIcon />,
          run: async ({ item }) => setModelAccessTenant(item),
        })
      }
      if (canSwitch) {
        actions.push({
          key: "enter-tenant",
          label: (item) =>
            session?.activeTenantId === item.id
              ? t("tenant.currentTenant")
              : t("tenant.enterTenant"),
          icon: <LogInIcon />,
          disabled: (item) =>
            item.status !== Status.Ok || session?.activeTenantId === item.id,
          run: async ({ item }) => {
            const previousTenantId = session?.activeTenantId ?? 0
            const previousTenantName = session?.activeTenantName ?? ""
            try {
              setActiveTenantId(item.id, item.shortName)
              await refreshProfile({ preserveSessionOnError: true })
              toast.success(t("tenant.enteredTenant", { name: item.shortName }))
              router.push("/dashboard")
              router.refresh()
            } catch (error) {
              setActiveTenantId(previousTenantId, previousTenantName)
              await refreshProfile({ preserveSessionOnError: true }).catch(() => undefined)
              throw error
            }
          },
        })
      }
      return actions
    },
    [
      canSwitch,
      canViewModelAccess,
      refreshProfile,
      router,
      session?.activeTenantId,
      session?.activeTenantName,
      t,
    ]
  )

  const statusColumn = createDashboardStatusColumn<AdminTenant, Status>({
    label: t("tenant.columnStatus"),
    getStatus: (item) => item.status,
    getLabel: (status) => getStatusLabel(status, t),
    getBadgeVariant: (status) => (status === Status.Ok ? "default" : "outline"),
    isEnabled: (status) => status === Status.Ok,
    toggle: canUpdateStatus
      ? {
          getNextStatus: (item) =>
            item.status === Status.Ok ? Status.Disabled : Status.Ok,
          updateStatus: async (item, nextStatus) => {
            await updateTenantStatus(item.id, nextStatus)
            if (
              nextStatus === Status.Disabled &&
              session?.activeTenantId === item.id
            ) {
              setActiveTenantId(0)
              await refreshProfile({ preserveSessionOnError: true })
            }
          },
          successMessage: (item, nextStatus) =>
            t(
              nextStatus === Status.Ok
                ? "tenant.statusEnabled"
                : "tenant.statusDisabled",
              { name: item.shortName }
            ),
          errorMessage: t("tenant.statusUpdateFailed"),
          ariaLabel: (item) => t("tenant.toggleStatus", { name: item.shortName }),
        }
      : undefined,
  })

  return (
    <>
      <DashboardCrudPage<AdminTenant, TenantFormPayload>
        filters={[
          {
            name: "legalName",
            label: t("tenant.filterLegalName"),
            placeholder: t("tenant.filterLegalName"),
            defaultValue: "",
            trim: true,
            className: "w-full sm:w-56",
          },
          {
            name: "tenantCode",
            label: t("tenant.filterTenantCode"),
            placeholder: t("tenant.filterTenantCode"),
            defaultValue: "",
            trim: true,
            className: "w-full sm:w-48",
          },
          {
            name: "registrationNo",
            label: t("tenant.filterRegistrationNo"),
            placeholder: t("tenant.filterRegistrationNo"),
            defaultValue: "",
            trim: true,
            className: "w-full sm:w-56",
          },
          {
            name: "verificationStatus",
            label: t("tenant.filterVerification"),
            type: "select",
            defaultValue: "all",
            allValue: "all",
            options: [
              { value: "all", label: t("tenant.allVerificationStatuses") },
              {
                value: TenantVerificationStatus.Pending,
                label: t("tenant.verificationPending"),
              },
              {
                value: TenantVerificationStatus.Verified,
                label: t("tenant.verificationVerified"),
              },
              {
                value: TenantVerificationStatus.Rejected,
                label: t("tenant.verificationRejected"),
              },
            ],
            className: "w-full sm:w-40",
          },
          {
            name: "status",
            label: t("tenant.filterStatus"),
            type: "select",
            defaultValue: "all",
            allValue: "all",
            valueType: "number",
            options: [
              { value: "all", label: t("status.all") },
              { value: String(Status.Ok), label: t("status.ok") },
              { value: String(Status.Disabled), label: t("status.disabled") },
            ],
            className: "w-full sm:w-36",
          },
        ]}
        columns={[
          {
            key: "company",
            label: t("tenant.columnCompany"),
            className: "min-w-[250px]",
            render: (item) => (
              <div className="flex items-center gap-3">
                <div className="flex size-9 shrink-0 items-center justify-center rounded-lg border border-[#dbe7f6] bg-[#f6f9ff] text-primary">
                  <Building2Icon className="size-4" />
                </div>
                <div className="min-w-0">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="font-medium">{item.shortName}</span>
                    {session?.activeTenantId === item.id ? (
                      <Badge variant="secondary">{t("tenant.currentTenant")}</Badge>
                    ) : null}
                  </div>
                  <div className="truncate text-xs text-muted-foreground" title={item.legalName}>
                    {item.legalName}
                  </div>
                  <div className="mt-0.5 font-mono text-xs text-muted-foreground">
                    {item.tenantCode}
                  </div>
                </div>
              </div>
            ),
          },
          {
            key: "registration",
            label: t("tenant.columnRegistration"),
            className: "min-w-[190px]",
            render: (item) => (
              <div>
                <div className="text-sm">{t("tenant.registrationTypeCreditCode")}</div>
                <div className="mt-1 font-mono text-xs text-muted-foreground">
                  {item.registrationNo}
                </div>
              </div>
            ),
          },
          {
            key: "industry",
            label: t("tenant.columnIndustry"),
            className: "min-w-[170px]",
            render: (item) => (
              <div className="space-y-1">
                <Badge variant="outline">{item.industryName || "-"}</Badge>
                <div className="font-mono text-xs text-muted-foreground">
                  {item.industryCode || "-"} · R{item.industryRevision || 0}
                </div>
              </div>
            ),
          },
          {
            key: "supervisor",
            label: t("tenant.columnSupervisor"),
            className: "min-w-[150px]",
            render: (item) => (
              <div className="flex items-center gap-2">
                <UserRoundCogIcon className="size-4 text-muted-foreground" />
                <div>
                  <div className="text-sm">{item.supervisorNickname || "-"}</div>
                  <div className="text-xs text-muted-foreground">
                    {item.supervisorUsername || "-"}
                  </div>
                </div>
              </div>
            ),
          },
          {
            key: "contact",
            label: t("tenant.columnContact"),
            className: "min-w-[180px]",
            render: (item) => (
              <div className="text-sm">
                <div>{item.contactName || "-"}</div>
                <div className="text-xs text-muted-foreground">
                  {item.contactMobile || item.contactEmail || "-"}
                </div>
              </div>
            ),
          },
          {
            key: "resources",
            label: t("tenant.columnResources"),
            className: "min-w-[245px]",
            render: (item) => (
              <div className="space-y-1.5 text-xs">
                <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-muted-foreground">
                  <span className="inline-flex items-center gap-1">
                    <HeadphonesIcon className="size-3.5" />
                    {t("tenant.resourceAgents", { count: item.agentCount })}
                  </span>
                  <span className="inline-flex items-center gap-1">
                    <StoreIcon className="size-3.5" />
                    {t("tenant.resourceStores", { count: item.storeCount })}
                  </span>
                  <span className="inline-flex items-center gap-1">
                    <UsersRoundIcon className="size-3.5" />
                    {t("tenant.resourceTeams", { count: item.agentTeamCount })}
                  </span>
                </div>
                <div
                  className="flex items-center gap-1.5 text-muted-foreground"
                  title={
                    item.lastActiveAt
                      ? formatDateTime(item.lastActiveAt)
                      : t("tenant.neverActive")
                  }
                >
                  <Clock3Icon className="size-3.5 shrink-0" />
                  <span>{t("tenant.lastActive")}</span>
                  <span className="font-medium text-foreground">
                    {item.lastActiveAt
                      ? formatDateTime(item.lastActiveAt)
                      : t("tenant.neverActive")}
                  </span>
                </div>
              </div>
            ),
          },
          {
            key: "verification",
            label: t("tenant.columnVerification"),
            render: (item) => (
              <Badge
                variant={
                  item.verificationStatus === TenantVerificationStatus.Verified
                    ? "outline"
                    : "secondary"
                }
              >
                <ShieldCheckIcon />
                {getVerificationLabel(item.verificationStatus, t)}
              </Badge>
            ),
          },
          statusColumn,
        ]}
        fetchList={fetchTenants}
        getItemId={(item) => item.id}
        createItem={async (payload) => {
          if (!payload.supervisor) throw new Error(t("tenant.supervisorRequired"))
          const result = await createTenant({
            intentProfileId: payload.intentProfileId,
            legalName: payload.legalName,
            shortName: payload.shortName,
            registrationType: payload.registrationType,
            registrationNo: payload.registrationNo,
            contactName: payload.contactName,
            contactMobile: payload.contactMobile,
            contactEmail: payload.contactEmail,
            address: payload.address,
            remark: payload.remark,
            supervisor: payload.supervisor,
          })
          setCreationResult(result)
          return result
        }}
        updateItem={(item, payload) => {
          const tenant = { ...payload }
          delete tenant.supervisor
          return updateTenant({ id: item.id, ...tenant })
        }}
        showEdit={canUpdate}
        showActionsColumn={showActionsColumn}
        rowActions={rowActions}
        renderEditDialog={({ open, saving, itemId, onOpenChange, onSubmit }) => (
          <TenantEditDialog
            open={open}
            saving={saving}
            itemId={itemId}
            onOpenChange={onOpenChange}
            onSubmit={onSubmit}
          />
        )}
        renderToolbarActions={({ onRefresh, onCreate, loading }) => (
          <>
            <Button variant="outline" onClick={onRefresh} disabled={loading}>
              <RefreshCwIcon className={loading ? "animate-spin" : undefined} />
              {t("tenant.refresh")}
            </Button>
            {canCreate ? (
              <Button onClick={onCreate} disabled={loading}>
                <PlusIcon />
                {t("tenant.new")}
              </Button>
            ) : null}
          </>
        )}
        labels={{
          refresh: t("tenant.refresh"),
          create: t("tenant.new"),
          query: t("common.query"),
          loading: t("tenant.loading"),
          empty: t("tenant.empty"),
          actions: t("common.actions"),
          edit: t("tenant.edit"),
          delete: t("tenant.delete"),
          processing: t("tenant.processing"),
          moreActions: (item) => t("tenant.moreActions", { name: item.shortName }),
          loadFailed: t("tenant.loadFailed"),
          saveFailed: t("tenant.saveFailed"),
          deleteFailed: t("tenant.deleteFailed"),
          created: (payload) => t("tenant.created", { name: payload.shortName }),
          updated: (_item, payload) =>
            t("tenant.updated", { name: payload.shortName }),
        }}
      />

      <TenantCreationResultDialog
        result={creationResult}
        onOpenChange={(open) => {
          if (!open) setCreationResult(null)
        }}
      />
      <TenantModelAccessDialog
        open={Boolean(modelAccessTenant)}
        tenant={modelAccessTenant}
        canUpdate={canUpdateModelAccess}
        onOpenChange={(open) => {
          if (!open) setModelAccessTenant(null)
        }}
      />
    </>
  )
}

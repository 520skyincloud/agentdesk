"use client"

import { useEffect, useMemo, useState } from "react"
import { useRouter } from "next/navigation"
import { BanIcon, CheckCircle2Icon, UsersRoundIcon } from "lucide-react"
import { toast } from "sonner"

import { useAuth } from "@/components/auth-provider"
import {
  createDashboardStatusColumn,
  createDashboardStatusToggleAction,
  DashboardCrudPage,
  type DashboardCrudRowAction,
} from "@/components/dashboard/crud"
import {
  createCompany,
  deleteCompany,
  fetchCompanies,
  fetchCompany,
  updateCompany,
  updateCompanyStatus,
  type AdminCompany,
  type CreateAdminCompanyPayload,
} from "@/lib/api/company"
import {
  fetchReplyIntentProfiles,
  type ReplyIntentProfile,
} from "@/lib/api/admin"
import { getEnumOptions } from "@/lib/enums"
import { Status, StatusLabels } from "@/lib/generated/enums"
import { useI18n } from "@/i18n/provider"

function getStatusLabel(status: Status, t: (key: string) => string) {
  if (status === Status.Disabled) {
    return t("status.disabled")
  }
  if (status === Status.Deleted) {
    return t("status.deleted")
  }
  return t("status.ok")
}

export default function DashboardCompaniesPage() {
  const t = useI18n()
  const router = useRouter()
  const { session } = useAuth()
  const [intentProfiles, setIntentProfiles] = useState<ReplyIntentProfile[]>([])
  const permissionSet = new Set(session?.permissions ?? [])
  const canCreate = permissionSet.has("company.create")
  const canUpdate = permissionSet.has("company.update")
  const canDelete = permissionSet.has("company.delete")
  const canViewAccounts = permissionSet.has("channel.view")
  const listStatusOptions = [
    { value: "all", label: t("status.all") },
    ...getEnumOptions(StatusLabels)
      .filter((item) => Number(item.value) !== Status.Deleted)
      .map((item) => ({
        value: String(item.value),
        label: getStatusLabel(item.value as Status, t),
      })),
  ]
  const intentProfileOptions = useMemo(
    () => [
      { value: "0", label: "暂不绑定（不能启用 AI）" },
      ...intentProfiles.map((item) => ({
        value: String(item.id),
        label: `${item.name}${item.industryCode ? ` · ${item.industryCode}` : ""}`,
      })),
    ],
    [intentProfiles],
  )

  useEffect(() => {
    async function loadIntentProfiles() {
      try {
        const page = await fetchReplyIntentProfiles({ status: Status.Ok, limit: 200 })
        setIntentProfiles(page.results)
      } catch (error) {
        toast.error(error instanceof Error ? error.message : "读取意图行业失败")
      }
    }
    void loadIntentProfiles()
  }, [])

  async function createCompanyWithPermission(payload: CreateAdminCompanyPayload) {
    if (!canCreate) throw new Error("无权创建客户企业")
    return createCompany(payload)
  }

  async function updateCompanyWithPermission(company: AdminCompany, payload: CreateAdminCompanyPayload) {
    if (!canUpdate) throw new Error("无权更新客户企业")
    return updateCompany({ id: company.id, ...payload })
  }

  async function deleteCompanyWithPermission(company: AdminCompany) {
    if (!canDelete) throw new Error("无权删除客户企业")
    return deleteCompany(company.id)
  }

  async function updateCompanyStatusWithPermission(company: AdminCompany, nextStatus: Status) {
    if (!canUpdate) throw new Error("无权更新客户企业状态")
    return updateCompanyStatus(company.id, nextStatus)
  }

  function openCompanyAccounts(company: AdminCompany) {
    if (!canViewAccounts) {
      toast.error("无权查看接入账号")
      return
    }
    router.push(`/dashboard/company-detail?id=${company.id}`)
  }

  const rowActions: DashboardCrudRowAction<AdminCompany>[] = []
  if (canViewAccounts) {
    rowActions.push({
      key: "accounts",
      label: "账号列表",
      icon: <UsersRoundIcon className="size-4" />,
      run: async ({ item }) => openCompanyAccounts(item),
    })
  }
  if (canUpdate) {
    rowActions.push(
      createDashboardStatusToggleAction<AdminCompany, Status>({
        disabled: (item) => item.status === Status.Deleted,
        icon: (item) =>
          item.status === Status.Ok ? <BanIcon /> : <CheckCircle2Icon />,
        label: (item) =>
          item.status === Status.Ok ? t("company.disable") : t("company.enable"),
        getNextStatus: (item) =>
          item.status === Status.Ok ? Status.Disabled : Status.Ok,
        updateStatus: updateCompanyStatusWithPermission,
        successMessage: (item, nextStatus) =>
          t(nextStatus === Status.Ok ? "company.enabled" : "company.disabled", {
            name: item.name,
          }),
        errorMessage: t("company.statusUpdateFailed"),
      }),
    )
  }

  return (
    <>
    <DashboardCrudPage<AdminCompany, CreateAdminCompanyPayload>
      filters={[
        {
          name: "name",
          label: t("company.filterName"),
          placeholder: t("company.filterName"),
          defaultValue: "",
          trim: true,
          className: "w-full sm:w-72",
        },
        {
          name: "code",
          label: t("company.filterCode"),
          placeholder: t("company.filterCode"),
          defaultValue: "",
          trim: true,
          className: "w-full sm:w-44",
        },
        {
          name: "status",
          label: t("status.all"),
          type: "select",
          defaultValue: "all",
          allValue: "all",
          options: listStatusOptions,
          className: "w-full sm:w-36",
        },
      ]}
      columns={[
        {
          key: "id",
          label: "ID",
          className: "w-20",
          render: (item) => item.id,
        },
        {
          key: "name",
          label: t("company.columnName"),
          render: (item) => <span className="font-medium">{item.name}</span>,
        },
        {
          key: "code",
          label: t("company.columnCode"),
          render: (item) => (
            <span className="text-muted-foreground">{item.code || "-"}</span>
          ),
        },
        {
          key: "intentProfile",
          label: "意图行业",
          render: (item) => (
            <span className="text-muted-foreground">
              {intentProfiles.find((profile) => profile.id === item.intentProfileId)?.name || item.intentProfileName || "未绑定"}
            </span>
          ),
        },
        {
          key: "customerCount",
          label: t("company.columnCustomerCount"),
          className: "w-28",
          render: (item) => item.customerCount,
        },
        createDashboardStatusColumn<AdminCompany, Status>({
          label: t("company.columnStatus"),
          className: "w-24",
          getStatus: (item) => item.status as Status,
          getLabel: (status) =>
            StatusLabels[status] ? getStatusLabel(status, t) : t("company.unknownStatus"),
          getBadgeVariant: (status) =>
            status === Status.Ok
              ? "default"
              : status === Status.Deleted
                ? "outline"
                : "secondary",
        }),
        {
          key: "remark",
          label: t("company.columnRemark"),
          render: (item) => (
            <div className="line-clamp-2 max-w-[320px] text-muted-foreground">
              {item.remark || "-"}
            </div>
          ),
        },
      ]}
      fetchList={fetchCompanies}
      getItemId={(item) => item.id}
      createItem={createCompanyWithPermission}
      updateItem={updateCompanyWithPermission}
      showCreate={canCreate}
      showEdit={canUpdate}
      deleteItem={canDelete ? deleteCompanyWithPermission : undefined}
      canDelete={(item) => item.status !== Status.Deleted}
      showActionsColumn={rowActions.length > 0 || canDelete}
      form={{
        fetchDetail: fetchCompany,
        fields: [
          {
            name: "name",
            label: t("company.columnName"),
            placeholder: t("company.namePlaceholder"),
            required: true,
            requiredMessage: t("company.nameRequired"),
            trim: true,
          },
          {
            name: "code",
            label: t("company.columnCode"),
            placeholder: t("company.optional"),
            trim: true,
          },
          {
            name: "intentProfileId",
            label: "意图行业",
            type: "select",
            defaultValue: "0",
            valueFromItem: (item) => String(item.intentProfileId || 0),
            options: intentProfileOptions,
            description: "公司默认的 IntentDetect 提示词和意图分类体系；员工号未单独设置时继承这里。公司或账号未绑定行业时不能启用 AI。",
          },
          {
            name: "remark",
            label: t("company.columnRemark"),
            placeholder: t("company.remarkPlaceholder"),
            type: "textarea",
            rows: 4,
            trim: true,
          },
        ],
        transformSubmitValues: (values) => ({
          name: String(values.name ?? ""),
          code: String(values.code ?? ""),
          intentProfileId: Number(values.intentProfileId ?? 0),
          remark: String(values.remark ?? ""),
        }),
        labels: {
          createTitle: t("company.createTitle"),
          editTitle: t("company.editTitle"),
          create: t("company.create"),
          save: t("company.save"),
          saving: t("company.saving"),
          cancel: t("company.cancel"),
          loadingDetail: t("company.loadingDetail"),
          required: t("company.nameRequired"),
          invalidNumber: t("company.nameRequired"),
          minValue: () => t("company.nameRequired"),
          maxValue: () => t("company.nameRequired"),
        },
      }}
      rowActions={rowActions}
      labels={{
        refresh: t("company.refresh"),
        create: t("company.new"),
        query: t("company.query"),
        loading: t("company.loading"),
        empty: t("company.empty"),
        actions: t("company.columnActions"),
        edit: t("company.edit"),
        delete: t("company.delete"),
        processing: t("company.processing"),
        moreActions: (item) => t("company.moreActions", { name: item.name }),
        loadFailed: t("company.loadFailed"),
        saveFailed: t("company.saveFailed"),
        deleteFailed: t("company.deleteFailed"),
        created: (payload) => t("company.created", { name: payload.name }),
        updated: (item) => t("company.updated", { name: item.name }),
        deleted: (item) => t("company.deleted", { name: item.name }),
      }}
    />
    </>
  )
}

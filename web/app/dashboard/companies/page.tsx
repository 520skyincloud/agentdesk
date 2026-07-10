"use client"

import { useEffect, useMemo, useState } from "react"
import { useRouter } from "next/navigation"
import { BanIcon, CheckCircle2Icon, SlidersHorizontalIcon, UsersRoundIcon } from "lucide-react"
import { toast } from "sonner"

import { useAuth } from "@/components/auth-provider"
import {
  createDashboardStatusColumn,
  createDashboardStatusToggleAction,
  DashboardCrudPage,
  type DashboardCrudRowAction,
} from "@/components/dashboard/crud"
import { OptionCombobox } from "@/components/option-combobox"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import {
  createCompany,
  deleteCompany,
  fetchCompanyAIModelSettings,
  fetchCompanies,
  fetchCompany,
  updateCompany,
  updateCompanyAIModelSettings,
  updateCompanyStatus,
  type AdminCompany,
  type CreateAdminCompanyPayload,
} from "@/lib/api/company"
import {
  fetchAIConfigsAll,
  fetchReplyIntentProfiles,
  type AIConfig,
  type ReplyIntentProfile,
  type StoreAIModelSetting,
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

function CompanyAIModelSettingsDialog({
  open,
  company,
  settings,
  aiConfigs,
  loading,
  saving,
  canSave,
  onOpenChange,
  onChange,
  onSubmit,
}: {
  open: boolean
  company: AdminCompany | null
  settings: StoreAIModelSetting[]
  aiConfigs: AIConfig[]
  loading: boolean
  saving: boolean
  canSave: boolean
  onOpenChange: (open: boolean) => void
  onChange: (settings: StoreAIModelSetting[]) => void
  onSubmit: () => void
}) {
  function updateSetting(usageCode: string, patch: Partial<StoreAIModelSetting>) {
    onChange(settings.map((item) => (item.usageCode === usageCode ? { ...item, ...patch } : item)))
  }

  function pickGlobalDefault(setting: StoreAIModelSetting) {
    const list = aiConfigs.filter((config) => config.status === Status.Ok && config.modelType === setting.expectedModelType)
    if (setting.usageCode === "intent_detect_llm") {
      return list.find((config) => config.intentDetectEnabled) || list[0]
    }
    return list[0]
  }

  function copyGlobalDefault(setting: StoreAIModelSetting) {
    const config = pickGlobalDefault(setting)
    if (!config) {
      toast.error("没有可复制的全局默认模型配置")
      return
    }
    updateSetting(setting.usageCode, {
      enabled: true,
      provider: config.provider || "openai",
      baseUrl: config.baseUrl || "",
      apiKey: "",
      apiMode: config.apiMode || "chat_completions",
      modelType: setting.expectedModelType,
      modelName: config.modelName || "",
      dimension: config.dimension || 0,
      maxContextTokens: config.maxContextTokens || 0,
      maxOutputTokens: config.maxOutputTokens || 0,
      timeoutMs: config.timeoutMs || 30000,
      maxRetryCount: config.maxRetryCount || 0,
      rpmLimit: config.rpmLimit || 0,
      tpmLimit: config.tpmLimit || 0,
      remark: setting.remark || "",
    })
    toast.success("已复制全局非密钥参数，请填写公司级 API Key")
  }

  function updateNumberSetting(usageCode: string, key: keyof StoreAIModelSetting, value: string) {
    updateSetting(usageCode, { [key]: Number(value || 0) } as Partial<StoreAIModelSetting>)
  }

  function sourceLabel(source: string) {
    if (source === "company_override") return "公司默认"
    return "系统全局默认"
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[88vh] max-w-5xl overflow-y-auto rounded-3xl p-5">
        <DialogHeader>
          <DialogTitle>公司模型设置</DialogTitle>
          <DialogDescription>
            {company ? `${company.name} 的公司默认模型。实际回复优先级：员工号设置 > 公司默认 > 系统全局默认。` : "公司级默认模型设置"}
          </DialogDescription>
        </DialogHeader>
        {loading ? (
          <div className="rounded-2xl border border-[#dbe7f6] bg-[#f8fbff] p-6 text-sm text-muted-foreground">正在读取公司模型设置...</div>
        ) : (
          <div className="grid gap-3">
            {settings.map((setting) => (
              <div key={setting.usageCode} className="rounded-2xl border border-[#dbe7f6] bg-white p-4 shadow-[0_8px_24px_rgba(35,74,122,0.05)]">
                <div className="flex flex-col gap-3 md:flex-row md:items-start md:justify-between">
                  <div className="min-w-0 flex-1">
                    <div className="font-semibold text-foreground">{setting.usageName}</div>
                    <div className="mt-1 text-xs text-muted-foreground">用途：{setting.usageCode} · 类型：{setting.expectedModelType}</div>
                    <div className="mt-2 text-xs leading-5 text-muted-foreground">
                      当前生效：{setting.effectiveModelName || setting.effectiveAiConfigName || "-"}（{sourceLabel(setting.effectiveModelSource)}）
                      {setting.effectiveBaseUrl ? ` · ${setting.effectiveBaseUrl}` : ""}
                    </div>
                  </div>
                  <div className="flex shrink-0 items-center gap-2">
                    <Button type="button" variant="outline" size="sm" className="rounded-xl" disabled={!canSave} onClick={() => copyGlobalDefault(setting)}>
                      复制全局默认参数
                    </Button>
                    <label className="flex cursor-pointer items-center gap-2 text-sm text-muted-foreground">
                      <Checkbox
                        checked={setting.enabled}
                        disabled={!canSave}
                        onCheckedChange={(checked) => updateSetting(setting.usageCode, { enabled: checked === true })}
                      />
                      启用公司默认
                    </label>
                  </div>
                </div>
                <div className="mt-4 grid gap-3 md:grid-cols-2">
                  <div className="space-y-1">
                    <div className="text-xs font-medium text-muted-foreground">供应商</div>
                    <Input value={setting.provider || "openai"} disabled={!canSave || !setting.enabled} onChange={(event) => updateSetting(setting.usageCode, { provider: event.target.value })} className="rounded-xl border-[#dbe7f6]" placeholder="openai" />
                  </div>
                  <div className="space-y-1">
                    <div className="text-xs font-medium text-muted-foreground">API 模式</div>
                    <OptionCombobox
                      value={setting.apiMode || "chat_completions"}
                      options={[
                        { value: "chat_completions", label: "Chat Completions" },
                        { value: "responses", label: "Responses API" },
                      ]}
                      placeholder="选择 API 模式"
                      triggerClassName="h-10 rounded-xl border-[#dbe7f6] bg-white"
                      disabled={!canSave || !setting.enabled}
                      onChange={(value) => updateSetting(setting.usageCode, { apiMode: value })}
                    />
                  </div>
                  <div className="space-y-1 md:col-span-2">
                    <div className="text-xs font-medium text-muted-foreground">Base URL</div>
                    <Input value={setting.baseUrl || ""} disabled={!canSave || !setting.enabled} onChange={(event) => updateSetting(setting.usageCode, { baseUrl: event.target.value })} className="rounded-xl border-[#dbe7f6]" placeholder="https://api.openai.com/v1" />
                  </div>
                  <div className="space-y-1">
                    <div className="text-xs font-medium text-muted-foreground">模型名</div>
                    <Input value={setting.modelName || ""} disabled={!canSave || !setting.enabled} onChange={(event) => updateSetting(setting.usageCode, { modelName: event.target.value, modelType: setting.expectedModelType })} className="rounded-xl border-[#dbe7f6]" placeholder="gpt-4.1-mini / qwen-vl-plus" />
                  </div>
                  <div className="space-y-1">
                    <div className="text-xs font-medium text-muted-foreground">API Key</div>
                    <Input type="password" value={setting.apiKey || ""} disabled={!canSave || !setting.enabled} onChange={(event) => updateSetting(setting.usageCode, { apiKey: event.target.value })} className="rounded-xl border-[#dbe7f6]" placeholder={setting.hasApiKey ? "已设置，留空不修改" : "请输入 API Key"} />
                  </div>
                  <div className="grid gap-3 md:col-span-2 md:grid-cols-4">
                    <div className="space-y-1">
                      <div className="text-xs font-medium text-muted-foreground">上下文 Token</div>
                      <Input type="number" value={setting.maxContextTokens || 0} disabled={!canSave || !setting.enabled} onChange={(event) => updateNumberSetting(setting.usageCode, "maxContextTokens", event.target.value)} className="rounded-xl border-[#dbe7f6]" />
                    </div>
                    <div className="space-y-1">
                      <div className="text-xs font-medium text-muted-foreground">输出 Token</div>
                      <Input type="number" value={setting.maxOutputTokens || 0} disabled={!canSave || !setting.enabled} onChange={(event) => updateNumberSetting(setting.usageCode, "maxOutputTokens", event.target.value)} className="rounded-xl border-[#dbe7f6]" />
                    </div>
                    <div className="space-y-1">
                      <div className="text-xs font-medium text-muted-foreground">超时 ms</div>
                      <Input type="number" value={setting.timeoutMs || 30000} disabled={!canSave || !setting.enabled} onChange={(event) => updateNumberSetting(setting.usageCode, "timeoutMs", event.target.value)} className="rounded-xl border-[#dbe7f6]" />
                    </div>
                    <div className="space-y-1">
                      <div className="text-xs font-medium text-muted-foreground">重试次数</div>
                      <Input type="number" value={setting.maxRetryCount || 0} disabled={!canSave || !setting.enabled} onChange={(event) => updateNumberSetting(setting.usageCode, "maxRetryCount", event.target.value)} className="rounded-xl border-[#dbe7f6]" />
                    </div>
                  </div>
                  <div className="grid gap-3 md:col-span-2 md:grid-cols-3">
                    <div className="space-y-1">
                      <div className="text-xs font-medium text-muted-foreground">向量维度</div>
                      <Input type="number" value={setting.dimension || 0} disabled={!canSave || !setting.enabled || setting.expectedModelType !== "embedding"} onChange={(event) => updateNumberSetting(setting.usageCode, "dimension", event.target.value)} className="rounded-xl border-[#dbe7f6]" />
                    </div>
                    <div className="space-y-1">
                      <div className="text-xs font-medium text-muted-foreground">RPM 限制</div>
                      <Input type="number" value={setting.rpmLimit || 0} disabled={!canSave || !setting.enabled} onChange={(event) => updateNumberSetting(setting.usageCode, "rpmLimit", event.target.value)} className="rounded-xl border-[#dbe7f6]" />
                    </div>
                    <div className="space-y-1">
                      <div className="text-xs font-medium text-muted-foreground">TPM 限制</div>
                      <Input type="number" value={setting.tpmLimit || 0} disabled={!canSave || !setting.enabled} onChange={(event) => updateNumberSetting(setting.usageCode, "tpmLimit", event.target.value)} className="rounded-xl border-[#dbe7f6]" />
                    </div>
                  </div>
                </div>
              </div>
            ))}
          </div>
        )}
        <DialogFooter>
          <Button type="button" variant="outline" className="rounded-xl" onClick={() => onOpenChange(false)}>
            取消
          </Button>
          <Button type="button" className="rounded-xl" disabled={!canSave || saving || loading || !company} onClick={onSubmit}>
            {saving ? "保存中..." : "保存公司设置"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

export default function DashboardCompaniesPage() {
  const t = useI18n()
  const router = useRouter()
  const { session } = useAuth()
  const [modelSettingsCompany, setModelSettingsCompany] = useState<AdminCompany | null>(null)
  const [modelSettings, setModelSettings] = useState<StoreAIModelSetting[]>([])
  const [aiConfigs, setAIConfigs] = useState<AIConfig[]>([])
  const [modelSettingsLoading, setModelSettingsLoading] = useState(false)
  const [modelSettingsSaving, setModelSettingsSaving] = useState(false)
  const [intentProfiles, setIntentProfiles] = useState<ReplyIntentProfile[]>([])
  const permissionSet = new Set(session?.permissions ?? [])
  const canViewModelSettings = permissionSet.has("aiConfig.view")
  const canUpdateModelSettings = permissionSet.has("aiConfig.update")
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
      { value: "0", label: "使用系统默认" },
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

  async function openCompanyModelSettings(company: AdminCompany) {
    setModelSettingsCompany(company)
    setModelSettingsLoading(true)
    try {
      const [settings, configs] = await Promise.all([
        fetchCompanyAIModelSettings(company.id),
        fetchAIConfigsAll({ status: Status.Ok }),
      ])
      setModelSettings(settings)
      setAIConfigs(configs)
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "读取公司模型设置失败")
      setModelSettingsCompany(null)
    } finally {
      setModelSettingsLoading(false)
    }
  }

  async function saveCompanyModelSettings() {
    if (!modelSettingsCompany) return
    setModelSettingsSaving(true)
    try {
      const next = await updateCompanyAIModelSettings({
        companyId: modelSettingsCompany.id,
        storeId: 0,
        wxWorkInstanceId: 0,
        settings: modelSettings.map((item) => ({
          usageCode: item.usageCode,
          aiConfigId: Number(item.aiConfigId || 0),
          enabled: item.enabled,
          provider: item.provider || "openai",
          baseUrl: item.baseUrl || "",
          apiKey: item.apiKey || "",
          apiMode: item.apiMode || "chat_completions",
          modelType: item.modelType || item.expectedModelType,
          modelName: item.modelName || "",
          dimension: Number(item.dimension || 0),
          maxContextTokens: Number(item.maxContextTokens || 0),
          maxOutputTokens: Number(item.maxOutputTokens || 0),
          timeoutMs: Number(item.timeoutMs || 30000),
          maxRetryCount: Number(item.maxRetryCount || 0),
          rpmLimit: Number(item.rpmLimit || 0),
          tpmLimit: Number(item.tpmLimit || 0),
          remark: item.remark || "",
        })),
      })
      setModelSettings(next)
      toast.success("公司模型设置已保存")
      setModelSettingsCompany(null)
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "保存公司模型设置失败")
    } finally {
      setModelSettingsSaving(false)
    }
  }

  const rowActions: DashboardCrudRowAction<AdminCompany>[] = []
	rowActions.push({
		key: "accounts",
		label: "账号列表",
		icon: <UsersRoundIcon className="size-4" />,
		run: async ({ item }) => router.push(`/dashboard/company-detail?id=${item.id}`),
	})
  if (canViewModelSettings) {
    rowActions.push({
      key: "modelSettings",
      label: "模型设置",
      icon: <SlidersHorizontalIcon className="size-4" />,
      run: async ({ item }) => openCompanyModelSettings(item),
    })
  }
  rowActions.push(
    createDashboardStatusToggleAction<AdminCompany, Status>({
      disabled: (item) => item.status === Status.Deleted,
      icon: (item) =>
        item.status === Status.Ok ? <BanIcon /> : <CheckCircle2Icon />,
      label: (item) =>
        item.status === Status.Ok ? t("company.disable") : t("company.enable"),
      getNextStatus: (item) =>
        item.status === Status.Ok ? Status.Disabled : Status.Ok,
      updateStatus: (item, nextStatus) => updateCompanyStatus(item.id, nextStatus),
      successMessage: (item, nextStatus) =>
        t(nextStatus === Status.Ok ? "company.enabled" : "company.disabled", {
          name: item.name,
        }),
      errorMessage: t("company.statusUpdateFailed"),
    }),
  )

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
              {intentProfiles.find((profile) => profile.id === item.intentProfileId)?.name || item.intentProfileName || "系统默认"}
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
      createItem={createCompany}
      updateItem={(item, payload) => updateCompany({ id: item.id, ...payload })}
      deleteItem={(item) => deleteCompany(item.id)}
      canDelete={(item) => item.status !== Status.Deleted}
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
            description: "公司默认的 IntentDetect 提示词和意图分类体系；员工号未单独设置时继承这里。",
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
    <CompanyAIModelSettingsDialog
      open={Boolean(modelSettingsCompany)}
      company={modelSettingsCompany}
      settings={modelSettings}
      aiConfigs={aiConfigs}
      loading={modelSettingsLoading}
      saving={modelSettingsSaving}
      canSave={canUpdateModelSettings}
      onOpenChange={(open) => {
        if (!open) {
          setModelSettingsCompany(null)
          setModelSettings([])
        }
      }}
      onChange={setModelSettings}
      onSubmit={() => void saveCompanyModelSettings()}
    />
    </>
  )
}

"use client"

import { Suspense, useEffect, useState } from "react"
import { useRouter, useSearchParams } from "next/navigation"
import { ArrowLeftIcon, Building2Icon, LinkIcon, RefreshCwIcon, SlidersHorizontalIcon } from "lucide-react"
import { toast } from "sonner"

import { useAuth } from "@/components/auth-provider"
import { CompanyAIModelSettingsDialog } from "@/components/company-ai-model-settings-dialog"
import { WxWorkProtocolInstanceManager } from "@/components/wxwork-protocol/wxwork-protocol-instance-manager"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { createWxWorkProtocolRemoteSetup, fetchAIConfigsAll, type AIConfig, type StoreAIModelSetting } from "@/lib/api/admin"
import {
  fetchCompany,
  fetchCompanyAIModelSettings,
  updateCompanyAIModelSettings,
  type AdminCompany,
} from "@/lib/api/company"
import { Status } from "@/lib/generated/enums"
import { formatDateTime, repairMojibakeText } from "@/lib/utils"

export default function DashboardCompanyDetailPage() {
	return (
		<Suspense fallback={<div className="p-6"><div className="rounded-2xl border border-[#dbe7f6] bg-white p-6 text-sm text-muted-foreground">正在加载公司详情...</div></div>}>
			<DashboardCompanyDetailContent />
		</Suspense>
	)
}

function DashboardCompanyDetailContent() {
	const router = useRouter()
  const { session } = useAuth()
  const searchParams = useSearchParams()
  const companyId = Number(searchParams.get("id") || 0)
  const [company, setCompany] = useState<AdminCompany | null>(null)
  const [loading, setLoading] = useState(true)
  const [creatingRemote, setCreatingRemote] = useState(false)
  const [modelSettingsOpen, setModelSettingsOpen] = useState(false)
  const [modelSettings, setModelSettings] = useState<StoreAIModelSetting[]>([])
  const [aiConfigs, setAIConfigs] = useState<AIConfig[]>([])
  const [modelSettingsLoading, setModelSettingsLoading] = useState(false)
  const [modelSettingsSaving, setModelSettingsSaving] = useState(false)
  const permissionSet = new Set(session?.permissions ?? [])
  const canViewWxWorkAccounts = permissionSet.has("channel.view")
  const canCreateWxWorkAccounts = canViewWxWorkAccounts && permissionSet.has("channel.create")
  const canViewModelSettings = permissionSet.has("aiConfig.view")
  const canUpdateModelSettings = permissionSet.has("aiConfig.update")

  useEffect(() => {
    async function loadCompany() {
      if (!companyId) {
        setLoading(false)
        return
      }
      setLoading(true)
      try {
        const data = await fetchCompany(companyId)
        setCompany(data)
      } catch (error) {
        toast.error(error instanceof Error ? error.message : "读取公司详情失败")
      } finally {
        setLoading(false)
      }
    }
    void loadCompany()
  }, [companyId])

  async function createCompanyRemoteSetupLink() {
    if (!canCreateWxWorkAccounts || !company) return
    setCreatingRemote(true)
    try {
      const item = await createWxWorkProtocolRemoteSetup({
        companyId: company.id,
        remark: `${repairMojibakeText(company.name)} 远程门店开户链接`,
      })
      const url = item.remoteSetupUrl || `${window.location.origin}/wxwork-remote-setup?token=${encodeURIComponent(item.remoteSetupToken || "")}`
      await navigator.clipboard.writeText(url)
      toast.success("该公司开户链接已复制")
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "生成开户链接失败")
    } finally {
      setCreatingRemote(false)
    }
  }

  async function openCompanyModelSettings() {
    if (!company) return
    if (!canViewModelSettings) {
      toast.error("没有查看模型设置权限")
      return
    }
    setModelSettingsOpen(true)
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
      setModelSettingsOpen(false)
    } finally {
      setModelSettingsLoading(false)
    }
  }

  async function saveCompanyModelSettings() {
    if (!canUpdateModelSettings || !company) return
    setModelSettingsSaving(true)
    try {
      const next = await updateCompanyAIModelSettings({
        companyId: company.id,
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
      setModelSettingsOpen(false)
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "保存公司模型设置失败")
    } finally {
      setModelSettingsSaving(false)
    }
  }

  if (loading) {
    return (
      <div className="p-6">
        <div className="rounded-2xl border border-[#dbe7f6] bg-white p-6 text-sm text-muted-foreground">正在加载公司详情...</div>
      </div>
    )
  }

  if (!company) {
    return (
      <div className="p-6">
        <div className="rounded-2xl border border-[#dbe7f6] bg-white p-6">
          <div className="font-semibold text-foreground">公司不存在或已删除</div>
          <Button type="button" variant="outline" className="mt-4 rounded-xl" onClick={() => router.push("/dashboard/companies")}>
            <ArrowLeftIcon className="size-4" />
            返回公司列表
          </Button>
        </div>
      </div>
    )
  }

  return (
    <div className="space-y-5 p-6">
      <div className="rounded-3xl border border-[#dbe7f6] bg-white p-5 shadow-[0_16px_40px_rgba(35,74,122,0.06)]">
        <div className="flex flex-col gap-4 lg:flex-row lg:items-start lg:justify-between">
          <div className="flex min-w-0 items-start gap-4">
            <div className="agentdesk-icon-tile mt-1">
              <Building2Icon className="size-5" />
            </div>
            <div className="min-w-0">
              <div className="flex flex-wrap items-center gap-2">
                <h1 className="truncate text-2xl font-semibold text-foreground">{repairMojibakeText(company.name)}</h1>
                <Badge variant={company.status === Status.Ok ? "default" : "secondary"}>{company.status === Status.Ok ? "启用" : "停用"}</Badge>
              </div>
              <div className="mt-2 flex flex-wrap gap-3 text-sm text-muted-foreground">
                <span>公司编码：{company.code || "-"}</span>
                <span>客户数：{company.customerCount || 0}</span>
                <span>更新时间：{company.updatedAt ? formatDateTime(company.updatedAt) : "-"}</span>
              </div>
              {company.remark ? (
                <p className="mt-3 max-w-3xl text-sm leading-6 text-muted-foreground">{repairMojibakeText(company.remark)}</p>
              ) : null}
            </div>
          </div>
          <div className="flex flex-wrap gap-2">
            <Button type="button" variant="outline" className="rounded-xl" onClick={() => router.push("/dashboard/companies")}>
              <ArrowLeftIcon className="size-4" />
              返回列表
            </Button>
            {canViewModelSettings ? (
              <Button type="button" variant="outline" className="rounded-xl" onClick={() => void openCompanyModelSettings()}>
                <SlidersHorizontalIcon className="size-4" />
                公司模型设置
              </Button>
            ) : null}
            {canCreateWxWorkAccounts ? (
              <Button type="button" className="rounded-xl" disabled={creatingRemote} onClick={() => void createCompanyRemoteSetupLink()}>
                {creatingRemote ? <RefreshCwIcon className="size-4 animate-spin" /> : <LinkIcon className="size-4" />}
                生成该公司开户链接
              </Button>
            ) : null}
          </div>
        </div>
      </div>

      {canViewWxWorkAccounts ? (
        <div className="space-y-3">
          <div>
            <h2 className="text-lg font-semibold text-foreground">企微员工号账号</h2>
            <p className="mt-1 text-sm text-muted-foreground">
              员工号就是门店账号。这里新增或生成的开户链接会锁定到当前公司；门店负责人只需要填写店名/账号名称。
            </p>
          </div>
          <WxWorkProtocolInstanceManager
            layout="fragment"
            tableShellClassName="rounded-3xl border border-[#dbe7f6] bg-white shadow-[0_16px_40px_rgba(35,74,122,0.06)]"
            companyId={company.id}
            companyName={company.name}
            lockCompany
          />
        </div>
      ) : null}

      <CompanyAIModelSettingsDialog
        open={canViewModelSettings && modelSettingsOpen}
        company={company}
        settings={modelSettings}
        aiConfigs={aiConfigs}
        loading={modelSettingsLoading}
        saving={modelSettingsSaving}
        canSave={canViewModelSettings && canUpdateModelSettings}
        onOpenChange={(open) => {
          setModelSettingsOpen(open)
          if (!open) {
            setModelSettings([])
          }
        }}
        onChange={setModelSettings}
        onSubmit={() => void saveCompanyModelSettings()}
      />
    </div>
  )
}

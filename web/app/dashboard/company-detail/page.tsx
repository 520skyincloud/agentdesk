"use client"

import { Suspense, useEffect, useState } from "react"
import { useRouter, useSearchParams } from "next/navigation"
import { ArrowLeftIcon, Building2Icon, LinkIcon, RefreshCwIcon } from "lucide-react"
import { toast } from "sonner"

import { useAuth } from "@/components/auth-provider"
import { WxWorkProtocolInstanceManager } from "@/components/wxwork-protocol/wxwork-protocol-instance-manager"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { createWxWorkProtocolRemoteSetup } from "@/lib/api/admin"
import {
  fetchCompany,
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
  const permissionSet = new Set(session?.permissions ?? [])
  const canViewWxWorkAccounts = permissionSet.has("channel.view")
  const canCreateWxWorkAccounts = canViewWxWorkAccounts && permissionSet.has("channel.create")

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

    </div>
  )
}

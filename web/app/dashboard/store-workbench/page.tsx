"use client"

import { useEffect, useMemo, useState } from "react"
import {
  Clock3Icon,
  MapPinIcon,
  QrCodeIcon,
  UsersRoundIcon,
} from "lucide-react"
import { toast } from "sonner"

import { useAuth } from "@/components/auth-provider"
import { OptionCombobox } from "@/components/option-combobox"
import { StoreModelCredentialPanel } from "@/components/store-model-credential-panel"
import { Badge } from "@/components/ui/badge"
import {
  fetchStoreModelCredentialStores,
  type StoreModelCredentialStoreOption,
} from "@/lib/api/admin"

const setupItems = [
  { title: "门店资料", description: "门店名称、地址、导航坐标与联系电话。", icon: MapPinIcon },
  { title: "企业微信员工号", description: "门店接待账号状态与知识库绑定。", icon: QrCodeIcon },
  { title: "人工通知群", description: "值班群、提醒成员和人工兜底范围。", icon: UsersRoundIcon },
  { title: "服务时间", description: "门店值班时间与总部兜底时段。", icon: Clock3Icon },
]

export default function StoreWorkbenchPage() {
  const { session } = useAuth()
  const [stores, setStores] = useState<StoreModelCredentialStoreOption[]>([])
  const [selectedStoreId, setSelectedStoreId] = useState(0)
  const [loading, setLoading] = useState(true)
  const isSuperAdmin = session?.roles.includes("super_admin") ?? false

  useEffect(() => {
    let cancelled = false
    setLoading(true)
    void fetchStoreModelCredentialStores()
      .then((items) => {
        if (cancelled) return
        setStores(items)
        setSelectedStoreId((current) => current > 0 && items.some((item) => item.storeId === current) ? current : items[0]?.storeId || 0)
      })
      .catch((error) => {
        if (!cancelled) toast.error(error instanceof Error ? error.message : "读取门店失败")
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [])

  const selectedStore = stores.find((item) => item.storeId === selectedStoreId)
  const storeOptions = useMemo(
    () => stores.map((item) => ({
      value: String(item.storeId),
      label: item.storeCode ? `${item.storeName} (${item.storeCode})` : item.storeName,
    })),
    [stores],
  )

  return (
    <main className="mx-auto flex w-full max-w-7xl flex-col gap-5 p-4 sm:p-6">
      <section className="flex flex-col gap-4 border-b border-border pb-5 sm:flex-row sm:items-end sm:justify-between">
        <div>
          <Badge variant="outline">门店工作台</Badge>
          <h1 className="mt-2 text-2xl font-semibold">门店配置与模型凭据</h1>
          <p className="mt-1 text-sm text-muted-foreground">门店只需维护一次模型密钥，模型地址与平台配置由超级管理员统一管理。</p>
        </div>
        <div className="w-full sm:w-80">
          {isSuperAdmin ? (
            <OptionCombobox
              value={selectedStoreId ? String(selectedStoreId) : ""}
              options={storeOptions}
              placeholder={loading ? "正在读取门店" : "选择门店"}
              disabled={loading}
              onChange={(value) => setSelectedStoreId(Number(value))}
            />
          ) : (
            <div className="border border-border bg-card px-3 py-2 text-sm">
              <span className="text-muted-foreground">当前门店：</span>
              <span className="font-medium">{selectedStore?.storeName || (loading ? "正在读取" : "未绑定")}</span>
            </div>
          )}
        </div>
      </section>

      {selectedStoreId > 0 ? (
        <StoreModelCredentialPanel storeId={selectedStoreId} />
      ) : (
        <section className="border border-dashed border-border py-12 text-center text-sm text-muted-foreground">
          {loading ? "正在读取门店绑定" : "当前账号尚未绑定可管理门店"}
        </section>
      )}

      <section>
        <div className="mb-3">
          <h2 className="text-base font-semibold">其他门店配置</h2>
          <p className="mt-1 text-sm text-muted-foreground">这些配置继续沿用现有门店资料与企微员工号管理流程。</p>
        </div>
        <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
          {setupItems.map((item) => {
            const Icon = item.icon
            return (
              <article key={item.title} className="border border-border bg-card p-4">
                <Icon className="size-4 text-primary" />
                <h3 className="mt-3 font-medium">{item.title}</h3>
                <p className="mt-1 text-sm leading-6 text-muted-foreground">{item.description}</p>
              </article>
            )
          })}
        </div>
      </section>
    </main>
  )
}

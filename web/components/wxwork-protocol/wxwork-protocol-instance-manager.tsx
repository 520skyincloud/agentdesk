"use client"

import { useEffect, useMemo, useState } from "react"
import { BellRingIcon, CopyIcon, LocateFixedIcon, LogOutIcon, QrCodeIcon, RotateCwIcon, SquareIcon, UserRoundCogIcon, UsersRoundIcon } from "lucide-react"
import { toast } from "sonner"

import {
  createDashboardStatusColumn,
  DashboardCrudPage,
} from "@/components/dashboard/crud"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  createWxWorkProtocolInstance,
  deleteWxWorkProtocolInstance,
  fetchChannels,
  fetchKnowledgeBasesAll,
  fetchWxWorkProtocolInstance,
  fetchWxWorkProtocolInstances,
  getWxWorkProtocolLoginQrcode,
  logoutWxWorkProtocolInstance,
  recoverWxWorkProtocolInstance,
  setWxWorkProtocolNotifyUrl,
  stopWxWorkProtocolInstance,
  syncWxWorkProtocolFriendRequests,
  syncWxWorkProtocolProfile,
  updateWxWorkProtocolInstance,
  type AdminChannel,
  type CreateWxWorkProtocolInstancePayload,
  type KnowledgeBase,
  type WxWorkProtocolInstance,
} from "@/lib/api/admin"
import { getEnumOptions } from "@/lib/enums"
import { Status, StatusLabels } from "@/lib/generated/enums"
import { formatDateTime } from "@/lib/utils"

const CALLBACK_URL = "http://112.124.109.106:2332/api/third/wxwork-protocol/callback"

type WxWorkProtocolInstanceManagerProps = {
  layout?: "page" | "fragment"
  onChanged?: () => void
  tableShellClassName?: string
}

function getStatusLabel(status: Status) {
  if (status === Status.Disabled) return "禁用"
  if (status === Status.Deleted) return "已删除"
  return "启用"
}

function healthBadgeVariant(healthStatus: string) {
  if (healthStatus === "online") return "default" as const
  if (healthStatus === "offline") return "secondary" as const
  return "outline" as const
}

export function WxWorkProtocolInstanceManager({
  layout = "page",
  onChanged,
  tableShellClassName,
}: WxWorkProtocolInstanceManagerProps) {
  const [channels, setChannels] = useState<AdminChannel[]>([])
  const [knowledgeBases, setKnowledgeBases] = useState<KnowledgeBase[]>([])
  const [reloadKey, setReloadKey] = useState(0)

  useEffect(() => {
    async function loadOptions() {
      try {
        const [channelPage, kbList] = await Promise.all([
          fetchChannels({ channelType: "wxwork_protocol", status: Status.Ok, limit: 200 }),
          fetchKnowledgeBasesAll({ status: Status.Ok }),
        ])
        setChannels(channelPage.results)
        setKnowledgeBases(kbList)
      } catch (error) {
        toast.error(error instanceof Error ? error.message : "加载选项失败")
      }
    }
    void loadOptions()
  }, [])

  const statusOptions = [
    { value: "all", label: "全部状态" },
    ...getEnumOptions(StatusLabels)
      .filter((option) => option.value !== Status.Deleted)
      .map((option) => ({
        value: String(option.value),
        label: getStatusLabel(option.value as Status),
      })),
  ]

  const channelOptions = useMemo(
    () => channels.map((item) => ({ value: String(item.id), label: item.name || item.channelId })),
    [channels],
  )

  const knowledgeBaseOptions = useMemo(
    () => knowledgeBases.map((item) => ({ value: String(item.id), label: item.name })),
    [knowledgeBases],
  )

  async function copyCallbackUrl() {
    await navigator.clipboard.writeText(CALLBACK_URL)
    toast.success("已复制公网回调地址")
  }

  async function copyProtocolResponse(title: string, response: string) {
    if (response?.trim()) {
      await navigator.clipboard.writeText(response)
    }
    toast.success(response?.trim() ? `${title}完成，协议原文已复制` : `${title}完成`)
  }

  function notifyChanged() {
    setReloadKey((value) => value + 1)
    onChanged?.()
  }

  function renderGeoPicker(context: {
    setValue: (name: string, value: string) => void
  }) {
    return (
      <div className="rounded-md border bg-muted/30 p-3">
        <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
          <div className="text-sm text-muted-foreground">
            门店在现场打开后台时，可用浏览器定位自动填入经纬度。地址名称仍建议人工核对。
          </div>
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={() => {
              if (!navigator.geolocation) {
                toast.error("当前浏览器不支持定位")
                return
              }
              navigator.geolocation.getCurrentPosition(
                (position) => {
                  context.setValue("storeLatitude", String(position.coords.latitude))
                  context.setValue("storeLongitude", String(position.coords.longitude))
                  context.setValue("storeMapProvider", "browser_geolocation")
                  toast.success("已填入当前坐标，请确认是否为门店位置")
                },
                (error) => {
                  toast.error(error.message || "获取坐标失败，请检查浏览器定位授权")
                },
                { enableHighAccuracy: true, timeout: 12000, maximumAge: 30000 }
              )
            }}
          >
            <LocateFixedIcon className="size-4" />
            一键获取当前坐标
          </Button>
        </div>
      </div>
    )
  }

  return (
    <DashboardCrudPage<WxWorkProtocolInstance, CreateWxWorkProtocolInstancePayload>
      layout={layout}
      reloadKey={reloadKey}
      tableShellClassName={tableShellClassName}
      filters={[
        {
          name: "guid",
          label: "GUID",
          placeholder: "搜索 GUID",
          defaultValue: "",
          trim: true,
          className: "w-full sm:w-72",
        },
        {
          name: "channelId",
          label: "协议渠道",
          type: "select",
          defaultValue: "all",
          allValue: "all",
          options: [{ value: "all", label: "全部渠道" }, ...channelOptions],
          className: "w-full sm:w-48",
        },
        {
          name: "knowledgeBaseId",
          label: "知识库",
          type: "select",
          defaultValue: "all",
          allValue: "all",
          options: [{ value: "all", label: "全部知识库" }, ...knowledgeBaseOptions],
          className: "w-full sm:w-48",
        },
        {
          name: "status",
          label: "状态",
          type: "select",
          defaultValue: "all",
          allValue: "all",
          options: statusOptions,
          className: "w-full sm:w-36",
        },
      ]}
      columns={[
        {
          key: "instance",
          label: "员工号实例",
          render: (item) => (
            <div className="flex min-w-0 items-center gap-3">
              <div className="flex size-10 items-center justify-center rounded-md bg-muted">
                <UserRoundCogIcon className="size-4" />
              </div>
              <div className="min-w-0">
                <div className="truncate font-medium">{item.employeeName || item.guid}</div>
                <div className="truncate font-mono text-xs text-muted-foreground">{item.guid}</div>
              </div>
            </div>
          ),
        },
        {
          key: "binding",
          label: "绑定",
          render: (item) => (
            <div className="space-y-1">
              <div>{item.channelName || `渠道 ${item.channelId}`}</div>
              <div className="text-xs text-muted-foreground">
                门店 {item.storeName || item.storeId} / {item.knowledgeBaseName || `知识库 ${item.knowledgeBaseId}`}
              </div>
              {item.storeAddress || item.storeLatitude || item.storeLongitude ? (
                <div className="text-xs text-muted-foreground">
                  {item.storeAddress || "未填地址"} {item.storeLatitude && item.storeLongitude ? `(${item.storeLatitude}, ${item.storeLongitude})` : ""}
                </div>
              ) : null}
            </div>
          ),
        },
        {
          key: "health",
          label: "在线状态",
          render: (item) => (
            <div className="space-y-1">
              <Badge variant={healthBadgeVariant(item.healthStatus)}>{item.healthStatus || "unknown"}</Badge>
              <div className="text-xs text-muted-foreground">
                {item.lastHeartbeatAt ? formatDateTime(item.lastHeartbeatAt) : "-"}
              </div>
            </div>
          ),
        },
        createDashboardStatusColumn<WxWorkProtocolInstance, Status>({
          label: "启用状态",
          getStatus: (item) => item.status as Status,
          getLabel: (status) => getStatusLabel(status),
          getBadgeVariant: (status) => (status === Status.Ok ? "default" : "outline"),
          isEnabled: (status) => status === Status.Ok,
        }),
      ]}
      fetchList={fetchWxWorkProtocolInstances}
      getItemId={(item) => item.id}
      createItem={async (payload) => {
        const ret = await createWxWorkProtocolInstance(payload)
        notifyChanged()
        return ret
      }}
      updateItem={async (item, payload) => {
        const ret = await updateWxWorkProtocolInstance({ id: item.id, ...payload })
        notifyChanged()
        return ret
      }}
      deleteItem={async (item) => {
        const ret = await deleteWxWorkProtocolInstance(item.id)
        notifyChanged()
        return ret
      }}
      rowActions={[
        {
          key: "setNotifyUrl",
          label: "设置回调",
          icon: <BellRingIcon className="size-4" />,
          run: async ({ item }) => {
            await setWxWorkProtocolNotifyUrl(item.id, CALLBACK_URL)
            toast.success("已向协议平台设置回调地址")
          },
        },
        {
          key: "loginQrcode",
          label: "获取登录二维码",
          icon: <QrCodeIcon className="size-4" />,
          run: async ({ item }) => {
            const resp = await getWxWorkProtocolLoginQrcode(item.id)
            await copyProtocolResponse("登录二维码", resp)
          },
        },
        {
          key: "syncProfile",
          label: "同步账号资料",
          icon: <RotateCwIcon className="size-4" />,
          run: async ({ item }) => {
            const resp = await syncWxWorkProtocolProfile(item.id)
            await copyProtocolResponse("同步账号资料", resp)
            notifyChanged()
          },
        },
        {
          key: "syncFriendRequests",
          label: "同步好友申请",
          icon: <UsersRoundIcon className="size-4" />,
          run: async ({ item }) => {
            const resp = await syncWxWorkProtocolFriendRequests(item.id)
            await copyProtocolResponse("同步好友申请", resp)
          },
        },
        {
          key: "recover",
          label: "恢复实例",
          icon: <RotateCwIcon className="size-4" />,
          run: async ({ item }) => {
            const resp = await recoverWxWorkProtocolInstance(item.id)
            await copyProtocolResponse("恢复实例", resp)
            notifyChanged()
          },
        },
        {
          key: "stop",
          label: "停止实例",
          icon: <SquareIcon className="size-4" />,
          run: async ({ item }) => {
            const resp = await stopWxWorkProtocolInstance(item.id)
            await copyProtocolResponse("停止实例", resp)
            notifyChanged()
          },
        },
        {
          key: "logout",
          label: "退出登录",
          icon: <LogOutIcon className="size-4" />,
          run: async ({ item }) => {
            const resp = await logoutWxWorkProtocolInstance(item.id)
            await copyProtocolResponse("退出登录", resp)
            notifyChanged()
          },
        },
        {
          key: "copyCallbackUrl",
          label: "复制回调地址",
          icon: <CopyIcon className="size-4" />,
          run: async () => copyCallbackUrl(),
        },
      ]}
      form={{
        fetchDetail: fetchWxWorkProtocolInstance,
        fields: [
          { name: "guid", label: "GUID", type: "text", required: true },
          {
            name: "channelId",
            label: "协议渠道",
            type: "select",
            required: true,
            options: channelOptions,
          },
          { name: "employeeUserId", label: "员工 UserID", type: "text" },
          { name: "employeeName", label: "员工名称", type: "text" },
          { name: "storeId", label: "门店ID", type: "number", required: true, min: 1 },
          { name: "storeAddress", label: "门店地址", type: "text", placeholder: "例如：上海市..." },
          { name: "storeNavigationName", label: "导航名称", type: "text", placeholder: "例如：丽斯未来酒店某某店" },
          { name: "storeLatitude", label: "门店纬度", type: "text", placeholder: "例如：31.230416" },
          { name: "storeLongitude", label: "门店经度", type: "text", placeholder: "例如：121.473701" },
          { name: "storeMapProvider", label: "坐标来源", type: "text", placeholder: "browser_geolocation / amap / tencent" },
          { name: "storeGeoPicker", label: "门店坐标", type: "custom", render: renderGeoPicker },
          {
            name: "knowledgeBaseId",
            label: "门店知识库",
            type: "select",
            required: true,
            options: knowledgeBaseOptions,
          },
          { name: "aiAgentId", label: "AI Agent ID", type: "number", min: 0 },
          { name: "notifyUrl", label: "全局回调地址", type: "text" },
          { name: "proxy", label: "代理配置", type: "text" },
          { name: "bridgeId", label: "Bridge ID", type: "text" },
          { name: "staffUserIds", label: "门店员工后台账号ID", type: "text" },
	          { name: "serviceHours", label: "服务时间", type: "text" },
	          { name: "fallbackToHQ", label: "总部兜底接管", type: "switch" },
	          { name: "manualTimeoutMinutes", label: "人工超时分钟", type: "number", min: 1, max: 120 },
	          { name: "aiReplyEnabled", label: "AI 托管回复", type: "switch" },
	          { name: "autoAcceptFriendRequest", label: "自动通过好友申请", type: "switch" },
	          { name: "autoAcceptFriendRemarkTemplate", label: "好友通过备注模板", type: "text" },
	          { name: "contextMaxMessages", label: "AI 上下文消息数", type: "number", min: 5, max: 200 },
	          { name: "contextMaxTokens", label: "AI 上下文 Token 上限", type: "number", min: 1000, max: 32000 },
	          { name: "contextCompressionEnabled", label: "超限自动压缩上下文", type: "switch" },
          {
            name: "status",
            label: "启用状态",
            type: "select",
            required: true,
            options: [
              { value: String(Status.Ok), label: "启用" },
              { value: String(Status.Disabled), label: "禁用" },
            ],
          },
          { name: "remark", label: "备注", type: "textarea" },
        ],
        transformSubmitValues: (values) => ({
          guid: String(values.guid || ""),
          channelId: Number(values.channelId || 0),
          employeeUserId: String(values.employeeUserId || ""),
          employeeName: String(values.employeeName || ""),
          storeId: Number(values.storeId || 0),
          storeAddress: String(values.storeAddress || ""),
          storeNavigationName: String(values.storeNavigationName || ""),
          storeLatitude: String(values.storeLatitude || ""),
          storeLongitude: String(values.storeLongitude || ""),
          storeMapProvider: String(values.storeMapProvider || ""),
          knowledgeBaseId: Number(values.knowledgeBaseId || 0),
          aiAgentId: Number(values.aiAgentId || 0),
          notifyUrl: String(values.notifyUrl || CALLBACK_URL),
          proxy: String(values.proxy || ""),
          bridgeId: String(values.bridgeId || ""),
          staffUserIds: String(values.staffUserIds || ""),
          serviceHours: String(values.serviceHours || ""),
	          fallbackToHQ: values.fallbackToHQ !== false,
	          manualTimeoutMinutes: Number(values.manualTimeoutMinutes || 10),
	          aiReplyEnabled: values.aiReplyEnabled !== false,
	          autoAcceptFriendRequest: values.autoAcceptFriendRequest === true,
	          autoAcceptFriendRemarkTemplate: String(values.autoAcceptFriendRemarkTemplate || ""),
	          contextMaxMessages: Number(values.contextMaxMessages || 30),
	          contextMaxTokens: Number(values.contextMaxTokens || 8000),
	          contextCompressionEnabled: values.contextCompressionEnabled !== false,
	          status: Number(values.status || Status.Ok),
          remark: String(values.remark || ""),
        }),
        labels: {
          createTitle: "新增企微员工号实例",
          editTitle: "编辑企微员工号实例",
          create: "新建实例",
          save: "保存",
          saving: "保存中...",
          cancel: "取消",
          loadingDetail: "加载中...",
          required: "必填",
          invalidNumber: "请输入有效数字",
          minValue: (min) => `不能小于 ${min}`,
          maxValue: (max) => `不能大于 ${max}`,
        },
      }}
      labels={{
        refresh: "刷新",
        create: "新建实例",
        query: "查询",
        loading: "加载中...",
        empty: "暂无企微员工号实例",
        actions: "操作",
        edit: "编辑",
        delete: "删除",
        processing: "处理中...",
        moreActions: (item) => `更多操作：${item.employeeName || item.guid}`,
        loadFailed: "加载失败",
        saveFailed: "保存失败",
        deleteFailed: "删除失败",
        created: () => "实例已创建",
        updated: () => "实例已更新",
        deleted: () => "实例已删除",
      }}
    />
  )
}

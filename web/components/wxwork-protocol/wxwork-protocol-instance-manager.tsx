"use client"

import { useEffect, useMemo, useState } from "react"
import { BellRingIcon, BotIcon, CopyIcon, LinkIcon, LocateFixedIcon, LogOutIcon, MapPinIcon, PlusIcon, QrCodeIcon, RotateCwIcon, SquareIcon, UserRoundCogIcon, UsersRoundIcon } from "lucide-react"
import { toast } from "sonner"

import {
  createDashboardStatusColumn,
  DashboardCrudPage,
} from "@/components/dashboard/crud"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import {
	createWxWorkProtocolRemoteSetup,
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
  startWxWorkProtocolLogin,
  stopWxWorkProtocolInstance,
  syncWxWorkProtocolFriendRequests,
  syncWxWorkProtocolProfile,
  updateWxWorkProtocolAIAgent,
  updateWxWorkProtocolInstance,
  type CreateAIAgentPayload,
  type AdminChannel,
  type CreateWxWorkProtocolInstancePayload,
  type KnowledgeBase,
  type WxWorkProtocolInstance,
} from "@/lib/api/admin"
import { EditDialog as AIAgentEditDialog } from "@/app/dashboard/ai-agents/_components/edit"
import { getEnumOptions } from "@/lib/enums"
import { Status, StatusLabels } from "@/lib/generated/enums"
import { formatDateTime, repairMojibakeText } from "@/lib/utils"

const CALLBACK_URL = "http://112.124.109.106:2332/api/third/wxwork-protocol/callback"
const DEFAULT_WELCOME_MESSAGE = "您好，欢迎来到丽斯未来。自助入住可以在小程序里办理，需要门店定位的话我也可以发您。"

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
  const [aiAgentInstance, setAIAgentInstance] = useState<WxWorkProtocolInstance | null>(null)
  const [aiAgentSaving, setAIAgentSaving] = useState(false)
  const [createDialogOpen, setCreateDialogOpen] = useState(false)
  const [creatingLocal, setCreatingLocal] = useState(false)
  const [creatingRemote, setCreatingRemote] = useState(false)

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

  async function createLocalLoginInstance() {
    setCreatingLocal(true)
    try {
      const item = await startWxWorkProtocolLogin(channels[0]?.id ?? 0)
      if (item.rawResponse?.trim()) {
        await navigator.clipboard.writeText(item.rawResponse)
      }
      toast.success(`已自动绑定空闲实例：${item.instance.guid}，登录二维码原文已复制`)
      notifyChanged()
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "自动绑定空闲实例失败")
    } finally {
      setCreatingLocal(false)
    }
  }

  async function createRemoteSetupLink() {
    setCreatingRemote(true)
    try {
      const item = await createWxWorkProtocolRemoteSetup({
        channelId: channels[0]?.id ?? 0,
        remark: "远程门店开户链接",
      })
      const url = item.remoteSetupUrl || `${window.location.origin}/wxwork-remote-setup?token=${encodeURIComponent(item.remoteSetupToken || "")}`
      await navigator.clipboard.writeText(url)
      toast.success("远程开户注册链接已复制")
      notifyChanged()
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "生成远程链接失败")
    } finally {
      setCreatingRemote(false)
    }
  }

  async function openAIAgentConfig(item: WxWorkProtocolInstance) {
    setAIAgentInstance(item)
  }

  async function saveAIAgentConfig(payload: CreateAIAgentPayload) {
    if (!aiAgentInstance) return
    setAIAgentSaving(true)
    try {
      await updateWxWorkProtocolAIAgent({ id: aiAgentInstance.id, ...payload })
      toast.success("智能客服配置已保存")
      setAIAgentInstance(null)
      notifyChanged()
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "保存智能客服配置失败")
      throw error
    } finally {
      setAIAgentSaving(false)
    }
  }

  function renderGeoPicker(context: {
    setValue: (name: string, value: string) => void
  }) {
    return (
      <div className="agentdesk-subtle-surface rounded-xl p-3">
        <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
          <div className="text-sm leading-6 text-muted-foreground">
            门店在现场打开后台时，可用浏览器定位自动填入经纬度。地址名称仍建议人工核对。
          </div>
          <Button
            type="button"
            variant="outline"
            size="sm"
            className="agentdesk-soft-button h-9 rounded-lg"
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

  function renderLocationGuide() {
    return (
      <div className="agentdesk-subtle-surface rounded-xl border border-[#edf1f7] p-3">
        <div className="flex items-start gap-3">
          <div className="agentdesk-icon-tile mt-0.5">
            <MapPinIcon className="size-4" />
          </div>
          <div className="space-y-1 text-sm leading-6 text-muted-foreground">
            <div className="font-medium text-foreground">门店定位绑定</div>
            <div>客户说“发定位 / 怎么走 / 酒店在哪”时，系统会直接发送这里绑定的定位消息，不进大模型瞎编。</div>
            <div>最快测试方式：用当前员工号收到一条门店定位，系统会自动写回经纬度；也可以用“一键获取当前坐标”手动填。</div>
          </div>
        </div>
      </div>
    )
  }

  return (
    <>
    <DashboardCrudPage<WxWorkProtocolInstance, CreateWxWorkProtocolInstancePayload>
      layout={layout}
      reloadKey={reloadKey}
      tableShellClassName={tableShellClassName}
      renderToolbarActions={(state) => (
        <>
          <Button variant="outline" className="rounded-lg border-[#dce7f4] bg-card" onClick={state.onRefresh} disabled={state.loading}>
            <RotateCwIcon className={state.loading ? "size-4 animate-spin" : "size-4"} />
            刷新
          </Button>
          <Button className="rounded-lg" onClick={() => setCreateDialogOpen(true)}>
            <PlusIcon className="size-4" />
            新增账号
          </Button>
        </>
      )}
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
          defaultValue: String(Status.Ok),
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
              <div className="agentdesk-icon-tile">
                <UserRoundCogIcon className="size-4" />
              </div>
              <div className="min-w-0">
                <div className="truncate font-semibold text-foreground">{repairMojibakeText(item.employeeName) || item.guid}</div>
                <div className="truncate font-mono text-xs text-muted-foreground">{item.guid}</div>
              </div>
            </div>
          ),
        },
        {
          key: "binding",
          label: "绑定",
          render: (item) => (
            <div className="space-y-1 text-sm">
              <div className="font-medium text-foreground">{item.channelName || `渠道 ${item.channelId}`}</div>
              <div className="text-xs text-muted-foreground">
                门店 {repairMojibakeText(item.storeName) || item.storeId} / {repairMojibakeText(item.knowledgeBaseName) || `知识库 ${item.knowledgeBaseId}`}
              </div>
              {item.storeAddress || item.storeLatitude || item.storeLongitude ? (
                <div className="text-xs text-muted-foreground">
                  {item.storeAddress || "未填地址"} {item.storeLatitude && item.storeLongitude ? `(${item.storeLatitude}, ${item.storeLongitude})` : ""}
                </div>
              ) : null}
              <div className="flex flex-wrap gap-1 pt-1">
                <Badge variant={item.storeLatitude && item.storeLongitude ? "default" : "outline"} className="rounded-md text-[10px]">
                  {item.storeLatitude && item.storeLongitude ? "已绑定位" : "未绑定位"}
                </Badge>
                <Badge variant={item.defaultMiniProgramPayload ? "default" : "outline"} className="rounded-md text-[10px]">
                  {item.defaultMiniProgramPayload ? "已绑小程序" : "未绑小程序"}
                </Badge>
              </div>
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
        {
          key: "aiAgent",
          label: "智能客服",
          render: (item) => (
            <div className="space-y-1 text-sm">
              <div className="flex flex-wrap gap-1">
                <Badge variant={item.aiAgentConfigured ? "default" : "outline"} className="rounded-md text-[10px]">
                  {item.aiAgentConfigured ? "已配置" : "未配置"}
                </Badge>
                <Badge variant={item.aiReplyEnabled ? "default" : "secondary"} className="rounded-md text-[10px]">
                  {item.aiReplyEnabled ? "AI开启" : "AI关闭"}
                </Badge>
              </div>
              <div className="max-w-48 truncate text-xs text-muted-foreground">
                {repairMojibakeText(item.aiAgentName) || "点击操作配置"}
              </div>
              {item.aiConfigName ? (
                <div className="max-w-48 truncate text-xs text-muted-foreground">模型：{repairMojibakeText(item.aiConfigName)}</div>
              ) : null}
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
          key: "aiAgentConfig",
          label: "智能客服配置",
          icon: <BotIcon className="size-4" />,
          run: async ({ item }) => openAIAgentConfig(item),
        },
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
          { name: "employeeAvatar", label: "员工头像 URL", type: "text" },
          { name: "storeId", label: "门店ID", type: "number", required: true, min: 1 },
          { name: "storeLocationGuide", label: "门店定位说明", type: "custom", render: renderLocationGuide },
          { name: "storeAddress", label: "门店地址", type: "text", placeholder: "例如：上海市..." },
          { name: "storeNavigationName", label: "导航名称", type: "text", placeholder: "例如：丽斯未来酒店某某店" },
          { name: "storeLatitude", label: "门店纬度", type: "text", placeholder: "例如：31.230416" },
	          { name: "storeLongitude", label: "门店经度", type: "text", placeholder: "例如：121.473701" },
	          { name: "storeMapProvider", label: "坐标来源", type: "text", placeholder: "browser_geolocation / amap / tencent" },
	          { name: "storeGeoPicker", label: "门店坐标", type: "custom", render: renderGeoPicker },
		          {
		            name: "resourceBindingSection",
		            label: "资源绑定已迁移",
		            type: "section",
		            description: "小程序与企业绑定；门店知识库、提示词、模型和技能在本账号的“智能客服配置”里维护。这里仅保留员工号和门店运营资料，避免多入口配置互相覆盖。",
		          },
	          { name: "notifyUrl", label: "全局回调地址", type: "text" },
	          { name: "proxy", label: "代理配置", type: "text" },
	          { name: "bridgeId", label: "Bridge ID", type: "text" },
	          { name: "staffUserIds", label: "门店员工后台账号ID", type: "text" },
	          {
	            name: "manualRouteSection",
	            label: "人工接待路由",
	            type: "section",
	            description: "按时间段自动选择：命中客服组排班且已绑定门店群时，转人工提醒发到门店群；非值班、未绑定群或关闭门店群提醒时，进入总部网页端待接管。",
	          },
	          { name: "serviceHours", label: "服务时间", type: "text" },
	          { name: "storeRoomNotifyEnabled", label: "值班时间优先提醒门店群", type: "switch" },
	          { name: "storeRoomConversationId", label: "门店群 conversation_id", type: "text", placeholder: "R: 开头的群 conversation_id" },
	          { name: "storeRoomAtList", label: "门店群 @ 成员ID", type: "text", placeholder: "多个用英文逗号分隔，0 表示 @ 全员" },
	          { name: "fallbackToHQ", label: "非值班/无群时进入总部网页端", type: "switch" },
	          { name: "manualTimeoutMinutes", label: "人工超时分钟", type: "number", min: 1, max: 120 },
		          { name: "aiReplyEnabled", label: "AI 托管回复", type: "switch" },
		          { name: "autoAcceptFriendRequest", label: "自动通过好友申请", type: "switch" },
		          { name: "autoAcceptFriendRemarkTemplate", label: "好友通过备注模板", type: "text" },
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
	        transformSubmitValues: (values, context) => ({
          guid: String(values.guid || ""),
          channelId: Number(values.channelId || 0),
          employeeUserId: String(values.employeeUserId || ""),
          employeeName: String(values.employeeName || ""),
          employeeAvatar: String(values.employeeAvatar || ""),
          storeId: Number(values.storeId || 0),
          storeAddress: String(values.storeAddress || ""),
          storeNavigationName: String(values.storeNavigationName || ""),
          storeLatitude: String(values.storeLatitude || ""),
	          storeLongitude: String(values.storeLongitude || ""),
	          storeMapProvider: String(values.storeMapProvider || ""),
		          defaultMiniProgramPayload: context.item?.defaultMiniProgramPayload || "",
		          welcomeMessage: context.item?.welcomeMessage || DEFAULT_WELCOME_MESSAGE,
		          welcomeSendMiniProgram: context.item?.welcomeSendMiniProgram ?? true,
		          welcomeAskLocation: context.item?.welcomeAskLocation ?? true,
		          knowledgeBaseId: context.item?.knowledgeBaseId || 0,
          aiAgentId: context.item?.aiAgentId || 0,
          notifyUrl: String(values.notifyUrl || CALLBACK_URL),
          proxy: String(values.proxy || ""),
          bridgeId: String(values.bridgeId || ""),
	          staffUserIds: String(values.staffUserIds || ""),
	          serviceHours: String(values.serviceHours || ""),
	          storeRoomConversationId: String(values.storeRoomConversationId || ""),
	          storeRoomNotifyEnabled: values.storeRoomNotifyEnabled === true,
	          storeRoomAtList: String(values.storeRoomAtList || ""),
	          fallbackToHQ: values.fallbackToHQ !== false,
	          manualTimeoutMinutes: Number(values.manualTimeoutMinutes || 10),
	          aiReplyEnabled: values.aiReplyEnabled !== false,
		          personaPrompt: context.item?.personaPrompt || "",
	          autoAcceptFriendRequest: values.autoAcceptFriendRequest === true,
	          autoAcceptFriendRemarkTemplate: String(values.autoAcceptFriendRemarkTemplate || ""),
		          contextMaxMessages: context.item?.contextMaxMessages || 30,
		          contextMaxTokens: context.item?.contextMaxTokens || 8000,
		          contextCompressionEnabled: context.item?.contextCompressionEnabled ?? true,
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
        moreActions: (item) => `更多操作：${repairMojibakeText(item.employeeName) || item.guid}`,
        loadFailed: "加载失败",
        saveFailed: "保存失败",
        deleteFailed: "删除失败",
        created: () => "实例已创建",
        updated: () => "实例已更新",
        deleted: () => "实例已删除",
      }}
    />
    <AIAgentEditDialog
      open={Boolean(aiAgentInstance)}
      saving={aiAgentSaving}
      itemId={aiAgentInstance?.aiAgentId || null}
      title={aiAgentInstance ? `智能客服配置：${repairMojibakeText(aiAgentInstance.employeeName) || aiAgentInstance.guid}` : undefined}
      onOpenChange={(open) => {
        if (!open) setAIAgentInstance(null)
      }}
      onSubmit={saveAIAgentConfig}
    />
    <Dialog open={createDialogOpen} onOpenChange={setCreateDialogOpen}>
      <DialogContent className="max-w-3xl rounded-3xl p-5">
        <DialogHeader>
          <DialogTitle>新增企微员工号</DialogTitle>
          <DialogDescription>
            先从系统管理的实例池认领一个真实空闲 GUID，再走扫码登录。现场负责人在旁边用左侧；外地门店用右侧链接自助完成。
          </DialogDescription>
        </DialogHeader>
        <div className="grid gap-4 md:grid-cols-2">
          <div className="rounded-2xl border border-[#dbe7f6] bg-white p-5 shadow-[0_12px_32px_rgba(35,74,122,0.06)]">
            <div className="flex items-start gap-3">
              <div className="agentdesk-icon-tile"><QrCodeIcon className="size-4" /></div>
              <div className="min-w-0 flex-1">
                <div className="font-semibold text-foreground">总部现场扫码</div>
                <p className="mt-2 text-sm leading-6 text-muted-foreground">
                  适合账号负责人就在你旁边。点击后系统自动认领一个空闲实例，并生成登录二维码原文。
                </p>
              </div>
            </div>
            <Button type="button" variant="outline" className="mt-5 w-full rounded-xl" disabled={creatingLocal || creatingRemote} onClick={() => void createLocalLoginInstance()}>
              <QrCodeIcon className="size-4" />
              {creatingLocal ? "生成中" : "生成现场扫码"}
            </Button>
          </div>
          <div className="rounded-2xl border border-[#dbe7f6] bg-white p-5 shadow-[0_12px_32px_rgba(35,74,122,0.06)]">
            <div className="flex items-start gap-3">
              <div className="agentdesk-icon-tile"><LinkIcon className="size-4" /></div>
              <div className="min-w-0 flex-1">
                <div className="font-semibold text-foreground">远程门店自助开户</div>
                <p className="mt-2 text-sm leading-6 text-muted-foreground">
                  生成链接发给外地门店。对方打开后扫码登录，并填写门店名称、坐标、服务时间和通知群。
                </p>
              </div>
            </div>
            <Button type="button" className="mt-5 w-full rounded-xl" disabled={creatingLocal || creatingRemote} onClick={() => void createRemoteSetupLink()}>
              <LinkIcon className="size-4" />
              {creatingRemote ? "生成中" : "生成并复制链接"}
            </Button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
    </>
  )
}

"use client"

import { useEffect, useMemo, useState } from "react"
import { BotIcon, LinkIcon, LocateFixedIcon, MapPinIcon, PlusIcon, QrCodeIcon, RotateCwIcon, UserRoundCogIcon, UsersRoundIcon } from "lucide-react"
import { toast } from "sonner"

import {
  createDashboardStatusColumn,
  DashboardCrudPage,
} from "@/components/dashboard/crud"
import { OptionCombobox } from "@/components/option-combobox"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import {
	createWxWorkProtocolRemoteSetup,
	createWxWorkProtocolInstance,
  deleteWxWorkProtocolInstance,
  fetchChannels,
  fetchKnowledgeBasesAll,
  fetchWxWorkProtocolInstance,
  fetchWxWorkProtocolInstances,
  fetchWxWorkProtocolRoomList,
  fetchWxWorkProtocolRoomMembers,
  getWxWorkProtocolLoginQrcode,
  logoutWxWorkProtocolInstance,
  startWxWorkProtocolLogin,
  updateWxWorkProtocolAIAgent,
  updateWxWorkProtocolInstance,
  type CreateAIAgentPayload,
  type AdminChannel,
  type CreateWxWorkProtocolInstancePayload,
  type KnowledgeBase,
  type WxWorkProtocolInstance,
  type WxWorkProtocolRoomMemberOption,
  type WxWorkProtocolRoomOption,
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
  hideCreateActions?: boolean
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

function StoreRoomPicker({
  context,
}: {
  context: {
    values: Record<string, string | boolean | string[]>
    setValue: (name: string, value: string | boolean | string[]) => void
  }
}) {
  const instanceId = Number(context.values.instanceId || 0)
  const selectedRoomConversationId = String(context.values.storeRoomConversationId || "")
  const selectedAtList = String(context.values.storeRoomAtList || "")
  const [rooms, setRooms] = useState<WxWorkProtocolRoomOption[]>([])
  const [members, setMembers] = useState<WxWorkProtocolRoomMemberOption[]>([])
  const [loadingRooms, setLoadingRooms] = useState(false)
  const [loadingMembers, setLoadingMembers] = useState(false)

  const roomOptions = rooms.map((room) => ({
    value: room.conversationId || `R:${room.roomId}`,
    label: `${repairMojibakeText(room.name)}${room.memberCount > 0 ? ` · ${room.memberCount}人` : ""}`,
  }))
  const selectedMemberIds = selectedAtList
    .split(",")
    .map((item) => item.trim())
    .filter(Boolean)
  const selectedRoom = rooms.find((room) => (room.conversationId || `R:${room.roomId}`) === selectedRoomConversationId)

  async function loadRooms() {
    if (!instanceId) {
      toast.error("请先保存账号，再读取门店群")
      return
    }
    setLoadingRooms(true)
    try {
      const list = await fetchWxWorkProtocolRoomList({ id: instanceId, limit: 200 })
      setRooms(list)
      if (list.length === 0) {
        toast.info("协议接口没有返回可选群。请确认该员工号是群主或已同步客户群。")
      } else {
        toast.success(`已读取 ${list.length} 个群`)
      }
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "读取门店群失败")
    } finally {
      setLoadingRooms(false)
    }
  }

  async function loadMembers() {
    if (!instanceId || !selectedRoomConversationId) {
      toast.error("请先选择门店群")
      return
    }
    setLoadingMembers(true)
    try {
      const list = await fetchWxWorkProtocolRoomMembers({
        id: instanceId,
        roomId: selectedRoomConversationId,
        userList: [],
      })
      setMembers(list)
      if (list.length === 0) {
        toast.info("协议接口没有返回群成员列表。当前文档接口是批量获取成员详情，若上游不支持空列表返回全部成员，需要先通过群详情拿成员 ID。")
      } else {
        toast.success(`已读取 ${list.length} 个群成员`)
      }
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "读取群成员失败")
    } finally {
      setLoadingMembers(false)
    }
  }

  function toggleMember(userId: string) {
    const next = selectedMemberIds.includes(userId)
      ? selectedMemberIds.filter((item) => item !== userId)
      : [...selectedMemberIds, userId]
    context.setValue("storeRoomAtList", next.join(","))
  }

  return (
    <div className="rounded-2xl border border-[#dbe7f6] bg-white p-4 shadow-[0_8px_24px_rgba(35,74,122,0.05)]">
      <div className="flex flex-col gap-3 md:flex-row md:items-end">
        <div className="min-w-0 flex-1 space-y-2">
          <div className="text-xs font-medium text-foreground/85">门店通知群</div>
          <OptionCombobox
            value={selectedRoomConversationId}
            options={roomOptions}
            placeholder={rooms.length > 0 ? "选择门店群" : "先刷新群列表"}
            triggerClassName="h-10 rounded-xl border-[#dbe7f6] bg-white"
            onChange={(value) => {
              context.setValue("storeRoomConversationId", value)
              context.setValue("storeRoomAtList", "")
              setMembers([])
            }}
          />
        </div>
        <Button type="button" variant="outline" className="rounded-xl" disabled={loadingRooms} onClick={() => void loadRooms()}>
          <RotateCwIcon className={loadingRooms ? "size-4 animate-spin" : "size-4"} />
          刷新群列表
        </Button>
        <Button type="button" variant="outline" className="rounded-xl" disabled={loadingMembers || !selectedRoomConversationId} onClick={() => void loadMembers()}>
          <UsersRoundIcon className={loadingMembers ? "size-4 animate-spin" : "size-4"} />
          读取群成员
        </Button>
      </div>
      <div className="mt-3 text-xs leading-5 text-muted-foreground">
        {selectedRoom ? `已选择：${repairMojibakeText(selectedRoom.name)}（${selectedRoom.conversationId}）` : "转人工命中门店值班时间时，会把提醒发到这里选中的群。"}
      </div>
      <div className="mt-4 rounded-xl bg-[#f6f9ff] p-3">
        <div className="mb-2 flex items-center justify-between gap-3">
          <div className="text-xs font-medium text-foreground/85">需要 @ 的群成员</div>
          <label className="flex cursor-pointer items-center gap-2 text-xs text-muted-foreground">
            <Checkbox checked={selectedMemberIds.includes("0")} onCheckedChange={() => toggleMember("0")} />
            @全员
          </label>
        </div>
        {members.length > 0 ? (
          <div className="grid gap-2 sm:grid-cols-2">
            {members.map((member) => {
              const checked = selectedMemberIds.includes(member.userId)
              return (
                <label key={member.userId} className="flex cursor-pointer items-center gap-2 rounded-lg border border-[#dbe7f6] bg-white px-3 py-2 text-sm">
                  <Checkbox checked={checked} onCheckedChange={() => toggleMember(member.userId)} />
                  <span className="min-w-0 flex-1 truncate">{repairMojibakeText(member.name)}</span>
                  <span className="max-w-24 truncate font-mono text-[10px] text-muted-foreground">{member.userId}</span>
                </label>
              )
            })}
          </div>
        ) : (
          <div className="text-xs leading-5 text-muted-foreground">
            选择群后点击“读取群成员”。如果协议没有返回成员，系统不会要求门店员工手输 ID；后续会通过群详情接口补齐成员来源。
          </div>
        )}
      </div>
    </div>
  )
}

export function WxWorkProtocolInstanceManager({
  layout = "page",
  onChanged,
  tableShellClassName,
  hideCreateActions = false,
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

  const managedModeOptions = [
    { value: "full", label: "全托管：只走总部网页端客服" },
    { value: "semi", label: "半托管：按时间段走门店群或总部网页端" },
    { value: "none", label: "非托管：只通知门店群" },
  ]

  const channelOptions = useMemo(
    () => channels.map((item) => ({ value: String(item.id), label: item.name || item.channelId })),
    [channels],
  )

  const knowledgeBaseOptions = useMemo(
    () => knowledgeBases.map((item) => ({ value: String(item.id), label: item.name })),
    [knowledgeBases],
  )

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

  async function replaceLoggedInAccount(item: WxWorkProtocolInstance) {
    const logoutResp = await logoutWxWorkProtocolInstance(item.id)
    const qrcodeResp = await getWxWorkProtocolLoginQrcode(item.id)
    const copiedText = [logoutResp, qrcodeResp].filter((text) => text?.trim()).join("\n\n")
    if (copiedText) {
      await navigator.clipboard.writeText(copiedText)
    }
    toast.success(copiedText ? "已下掉当前账号，重新登录二维码原文已复制" : "已下掉当前账号，请重新扫码登录")
    notifyChanged()
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

  function renderStoreRoomPicker(context: {
    values: Record<string, string | boolean | string[]>
    setValue: (name: string, value: string | boolean | string[]) => void
  }) {
    return <StoreRoomPicker context={context} />
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
          {!hideCreateActions ? (
            <Button className="rounded-lg" onClick={() => setCreateDialogOpen(true)}>
              <PlusIcon className="size-4" />
              新增账号
            </Button>
          ) : null}
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
                <div className="truncate text-xs text-muted-foreground">{item.storeName ? `门店：${repairMojibakeText(item.storeName)}` : "未绑定门店"}</div>
              </div>
            </div>
          ),
        },
        {
          key: "binding",
          label: "绑定",
          render: (item) => (
            <div className="space-y-1 text-sm">
              <div className="font-medium text-foreground">{repairMojibakeText(item.storeName) || `门店 ${item.storeId || "未绑定"}`}</div>
              <div className="text-xs text-muted-foreground">
                {repairMojibakeText(item.knowledgeBaseName) || `知识库 ${item.knowledgeBaseId || "未配置"}`}
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
          key: "replaceLogin",
          label: "更换登录员工号",
          icon: <QrCodeIcon className="size-4" />,
          confirm: (item) => ({
            title: "更换登录员工号",
            description: `会先让 ${repairMojibakeText(item.employeeName) || "当前员工号"} 退出协议登录，然后生成新的扫码登录二维码。确认继续？`,
            confirmText: "退出并重新扫码",
            cancelText: "取消",
          }),
          run: async ({ item }) => replaceLoggedInAccount(item),
        },
      ]}
      form={{
        fetchDetail: fetchWxWorkProtocolInstance,
        fields: [
          { name: "instanceId", label: "实例ID", type: "custom", valueFromItem: (item) => item.id, render: () => null },
          {
            name: "accountIdentitySection",
            label: "员工号资料",
            type: "section",
            description: "这里显示的是通过协议扫码登录的门店企业微信员工号。账号头像、UserID、GUID、回调、代理和 Bridge 等技术信息由系统同步和维护，不再开放手动填写。",
          },
          { name: "employeeName", label: "员工号名称", type: "text", placeholder: "扫码同步后会自动带出，可手动改展示名" },
          { name: "storeId", label: "绑定门店", type: "number", required: true, min: 1, description: "当前仍按门店 ID 保存；后续门店员工自助页会改成搜索门店后选择绑定。" },
          { name: "storeLocationGuide", label: "门店定位说明", type: "custom", render: renderLocationGuide },
          { name: "storeAddress", label: "门店地址", type: "text", placeholder: "例如：上海市..." },
          { name: "storeNavigationName", label: "导航名称", type: "text", placeholder: "例如：丽斯未来酒店某某店" },
          { name: "storeLatitude", label: "门店纬度", type: "text", placeholder: "例如：31.230416" },
          { name: "storeLongitude", label: "门店经度", type: "text", placeholder: "例如：121.473701" },
          { name: "storeMapProvider", label: "坐标来源", type: "text", placeholder: "browser_geolocation / amap / tencent" },
          { name: "storeGeoPicker", label: "门店坐标", type: "custom", render: renderGeoPicker },
          {
            name: "resourceBindingSection",
            label: "资源绑定",
            type: "section",
            description: "小程序跟随企业/品牌统一绑定；门店知识库、模型、提示词和技能统一在本账号的“智能客服配置”里维护。",
          },
          {
            name: "manualRouteSection",
            label: "人工接待路由",
            type: "section",
            description: "托管模式决定转人工提醒去哪：全托管只进总部网页端；半托管按服务时间在门店群和总部网页端之间切换；非托管只通知门店群。",
          },
          {
            name: "managedMode",
            label: "门店托管模式",
            type: "select",
            required: true,
            defaultValue: "semi",
            options: managedModeOptions,
            description: "这个策略绑定到门店员工登录 AgentDesk 后的系统账号上，每个门店只允许一个；协议实例再绑定这个门店员工账号。",
          },
          { name: "serviceHours", label: "门店服务时间", type: "text", placeholder: "例如：09:00-22:00；多个时段后续由排班页维护" },
          { name: "storeRoomNotifyEnabled", label: "启用门店群通知", type: "switch" },
          { name: "storeRoomConversationId", label: "门店群", type: "custom", render: () => null },
          { name: "storeRoomAtList", label: "@ 成员", type: "custom", render: () => null },
          { name: "storeRoomPicker", label: "门店群和 @ 成员", type: "custom", render: renderStoreRoomPicker },
          { name: "manualTimeoutMinutes", label: "人工超时分钟", type: "number", min: 1, max: 120 },
          {
            name: "automationSection",
            label: "自动化开关",
            type: "section",
            description: "AI 回复开关只控制当前员工号是否由智能客服托管；智能客服本身的模型、知识库和提示词请点列表里的“智能客服配置”。",
          },
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
          guid: context.item?.guid || "",
          channelId: context.item?.channelId || channels[0]?.id || 0,
          employeeUserId: context.item?.employeeUserId || "",
          employeeName: String(values.employeeName || ""),
          employeeAvatar: context.item?.employeeAvatar || "",
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
          notifyUrl: context.item?.notifyUrl || CALLBACK_URL,
          proxy: context.item?.proxy || "",
          bridgeId: context.item?.bridgeId || "",
          staffUserIds: context.item?.staffUserIds || "",
          managedMode: String(values.managedMode || context.item?.managedMode || "semi"),
          serviceHours: String(values.serviceHours || ""),
          storeRoomConversationId: String(values.storeRoomConversationId || ""),
          storeRoomNotifyEnabled: values.storeRoomNotifyEnabled === true,
          storeRoomAtList: String(values.storeRoomAtList || ""),
          fallbackToHQ: String(values.managedMode || context.item?.managedMode || "semi") !== "none",
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

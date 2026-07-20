"use client"

import { useEffect, useMemo, useRef, useState } from "react"
import { CopyIcon, DatabaseZapIcon, LocateFixedIcon, MapPinIcon, MessageSquareTextIcon, PlusIcon, QrCodeIcon, RotateCwIcon, SlidersHorizontalIcon, UploadIcon, UserRoundCogIcon, UsersRoundIcon, XIcon } from "lucide-react"
import { toast } from "sonner"

import { useAuth } from "@/components/auth-provider"
import { WxWorkModelAssignmentDialog } from "@/components/wxwork-protocol/wxwork-model-assignment-dialog"
import { FastGPTModelProfileDialog } from "@/components/wxwork-protocol/fastgpt-model-profile-dialog"
import { WxWorkProtocolBindingDialog } from "@/components/wxwork-protocol/wxwork-protocol-binding-dialog"
import {
  createDashboardStatusColumn,
  DashboardCrudPage,
  type DashboardCrudRowAction,
} from "@/components/dashboard/crud"
import { OptionCombobox } from "@/components/option-combobox"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Switch } from "@/components/ui/switch"
import { Textarea } from "@/components/ui/textarea"
import {
  createWxWorkProtocolReplacementSetup,
  deleteWxWorkProtocolInstance,
  fetchChannels,
  fetchKnowledgeBasesAll,
  fetchReplyIntentProfiles,
  fetchWxWorkProtocolInstance,
  fetchWxWorkProtocolInstances,
  fetchWxWorkProtocolRoomList,
  fetchWxWorkProtocolRoomMembers,
  updateWxWorkProtocolInstance,
  uploadAsset,
  type AdminChannel,
  type CreateWxWorkProtocolInstancePayload,
  type KnowledgeBase,
  type ReplyIntentProfile,
  type WxWorkProtocolInstance,
  type WxWorkProtocolRoomMemberOption,
  type WxWorkProtocolRoomOption,
} from "@/lib/api/admin"
import { deleteAsset } from "@/lib/api/asset"
import { getEnumOptions } from "@/lib/enums"
import { Status, StatusLabels } from "@/lib/generated/enums"
import { getBrowserCoordinates } from "@/lib/browser-geolocation"
import { formatDateTime, repairMojibakeText } from "@/lib/utils"

const CALLBACK_URL = "http://112.124.109.106:2332/api/third/wxwork-protocol/callback"
const DEFAULT_WELCOME_MESSAGE = "您好，欢迎来到丽斯未来。自助入住可以在小程序里办理，需要门店定位的话我也可以发您。"

type WxWorkProtocolInstanceManagerProps = {
  layout?: "page" | "fragment"
  onChanged?: () => void
  tableShellClassName?: string
  hideCreateActions?: boolean
}

type WelcomeCapableInstance = WxWorkProtocolInstance & {
  welcomeEnabled?: boolean
  welcomeImageAssetId?: string
  welcomeImageUrl?: string
  contactAutomationLastAt?: string | null
  contactAutomationLastError?: string
}

type WelcomeSettingsDraft = {
  enabled: boolean
  message: string
  imageAssetId: string
  imageUrl: string
  uploadedImageRecordId: number
  sendMiniProgram: boolean
  sendLocation: boolean
}

type ReceptionSettingsDraft = {
  personaPrompt: string
  frontDeskMode: "unmanned" | "staffed" | "scheduled"
  frontDeskHours: string
}

function buildAssetFileURL(assetId: string) {
  const value = assetId.trim()
  return value ? `/api/asset/file/${encodeURIComponent(value)}` : ""
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

  async function copyMemberId(userId: string) {
    try {
      await navigator.clipboard.writeText(userId)
      toast.success("已复制成员 ID")
    } catch {
      toast.error("复制成员 ID 失败")
    }
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
          <div className="grid gap-2">
            {members.map((member) => {
              const checked = selectedMemberIds.includes(member.userId)
              const realName = repairMojibakeText(member.realName || member.displayName || member.name)
              const roomRemark = repairMojibakeText(member.roomRemark || "")
              const accountId = member.accountId || ""
              return (
                <label key={member.userId} className="flex cursor-pointer items-center gap-3 rounded-lg border border-[#dbe7f6] bg-white px-3 py-2 text-sm">
                  <Checkbox checked={checked} onCheckedChange={() => toggleMember(member.userId)} />
                  <span className="min-w-0 flex-1">
                    <span className="block truncate font-medium text-foreground">{realName || member.userId}</span>
                    {roomRemark && roomRemark !== realName ? (
                      <span className="mt-0.5 block truncate text-xs text-muted-foreground">群内备注：{roomRemark}</span>
                    ) : null}
                    {accountId ? (
                      <span className="mt-0.5 block truncate text-xs text-muted-foreground">账号：{accountId}</span>
                    ) : null}
                    <span className="mt-0.5 block break-all font-mono text-[11px] leading-4 text-muted-foreground" title={member.userId}>{member.userId}</span>
                  </span>
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon"
                    className="size-8 shrink-0 rounded-lg"
                    title="复制成员 ID"
                    onClick={(event) => {
                      event.preventDefault()
                      event.stopPropagation()
                      void copyMemberId(member.userId)
                    }}
                  >
                    <CopyIcon className="size-4" />
                  </Button>
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

function WelcomeSettingsDialog({
  instance,
  draft,
  saving,
  onOpenChange,
  onChange,
  onSave,
}: {
  instance: WelcomeCapableInstance | null
  draft: WelcomeSettingsDraft
  saving: boolean
  onOpenChange: (open: boolean) => void
  onChange: (draft: WelcomeSettingsDraft) => void
  onSave: () => Promise<boolean>
}) {
  const fileInputRef = useRef<HTMLInputElement>(null)
  const committedImageRecordIdRef = useRef(0)
  const [uploading, setUploading] = useState(false)

  useEffect(() => {
    if (instance) committedImageRecordIdRef.current = 0
  }, [instance])
  const hasMiniProgram = Boolean(instance?.defaultMiniProgramPayload?.trim())
  const hasLocation = Boolean(instance?.storeLongitude?.trim() && instance?.storeLatitude?.trim())
  const hasContent = Boolean(
    draft.message.trim() ||
    draft.imageAssetId ||
    (draft.sendMiniProgram && hasMiniProgram) ||
    (draft.sendLocation && hasLocation),
  )

  async function uploadWelcomeImage(file: File) {
    if (!file.type.startsWith("image/")) {
      toast.error("请选择图片文件")
      return
    }
    if (file.size > 10 * 1024 * 1024) {
      toast.error("欢迎语图片不能超过 10MB")
      return
    }
    setUploading(true)
    try {
      const previousUploadedRecordId = draft.uploadedImageRecordId
      const asset = await uploadAsset(file, "wxwork-welcome")
      onChange({
        ...draft,
        imageAssetId: asset.assetId,
        imageUrl: asset.url || buildAssetFileURL(asset.assetId),
        uploadedImageRecordId: asset.id,
      })
      if (previousUploadedRecordId > 0) {
        await cleanupUploadedImage(previousUploadedRecordId)
      }
      toast.success("欢迎语图片已上传")
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "欢迎语图片上传失败")
    } finally {
      setUploading(false)
      if (fileInputRef.current) fileInputRef.current.value = ""
    }
  }

  async function cleanupUploadedImage(recordId: number) {
    if (recordId <= 0) return
    try {
      await deleteAsset(recordId)
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "临时欢迎语图片清理失败")
    }
  }

  async function removeWelcomeImage() {
    const uploadedRecordId = draft.uploadedImageRecordId
    onChange({ ...draft, imageAssetId: "", imageUrl: "", uploadedImageRecordId: 0 })
    await cleanupUploadedImage(uploadedRecordId)
  }

  async function closeDialog() {
    if (saving || uploading) return
    const uploadedRecordId = draft.uploadedImageRecordId
    if (uploadedRecordId > 0 && uploadedRecordId !== committedImageRecordIdRef.current) {
      await cleanupUploadedImage(uploadedRecordId)
    }
    onOpenChange(false)
  }

  async function saveAndClose() {
    const saved = await onSave()
    if (!saved) return
    committedImageRecordIdRef.current = draft.uploadedImageRecordId
    onOpenChange(false)
  }

  return (
    <Dialog open={Boolean(instance)} onOpenChange={(open) => {
      if (open) {
        onOpenChange(true)
        return
      }
      void closeDialog()
    }}>
      <DialogContent className="max-h-[88vh] max-w-2xl overflow-y-auto rounded-3xl p-5">
        <DialogHeader>
          <DialogTitle>欢迎语设置</DialogTitle>
          <DialogDescription>
            {instance
              ? `${repairMojibakeText(instance.employeeName) || instance.guid} 新增好友后，系统按下方顺序发送。`
              : "设置企微员工号的新好友欢迎内容。"}
          </DialogDescription>
        </DialogHeader>

        <div className="space-y-4">
          <div className="flex items-center justify-between gap-4 rounded-2xl border border-[#dbe7f6] bg-[#f8fbff] p-4">
            <div>
              <div className="font-medium text-foreground">启用新好友欢迎语</div>
              <div className="mt-1 text-xs leading-5 text-muted-foreground">关闭后不会发送任何欢迎内容。</div>
            </div>
            <Switch checked={draft.enabled} onCheckedChange={(enabled) => onChange({ ...draft, enabled })} />
          </div>

          <div className="space-y-2">
            <div className="text-sm font-medium text-foreground">文字内容</div>
            <Textarea
              value={draft.message}
              maxLength={500}
              disabled={!draft.enabled}
              className="min-h-28 rounded-xl border-[#dbe7f6]"
              placeholder="例如：您好，欢迎添加本店企微。办理入住、停车或其他问题都可以直接发我。"
              onChange={(event) => onChange({ ...draft, message: event.target.value })}
            />
            <div className="text-right text-xs text-muted-foreground">{draft.message.length}/500</div>
          </div>

          <div className="space-y-2">
            <div className="text-sm font-medium text-foreground">图片</div>
            <input
              ref={fileInputRef}
              type="file"
              accept="image/*"
              className="hidden"
              onChange={(event) => {
                const file = event.target.files?.[0]
                if (file) void uploadWelcomeImage(file)
              }}
            />
            {draft.imageUrl ? (
              <div className="relative w-fit overflow-hidden rounded-xl border border-[#dbe7f6] bg-[#f8fbff] p-2">
                <img src={draft.imageUrl} alt="欢迎语图片预览" className="max-h-48 max-w-full rounded-lg object-contain" />
                <Button
                  type="button"
                  size="icon-sm"
                  variant="destructive"
                  className="absolute right-3 top-3"
                  disabled={!draft.enabled || uploading}
                  aria-label="移除欢迎语图片"
                  onClick={() => void removeWelcomeImage()}
                >
                  <XIcon className="size-4" />
                </Button>
              </div>
            ) : (
              <Button
                type="button"
                variant="outline"
                className="h-24 w-full rounded-xl border-dashed border-[#cbdcf1] bg-[#f8fbff]"
                disabled={!draft.enabled || uploading}
                onClick={() => fileInputRef.current?.click()}
              >
                {uploading ? <RotateCwIcon className="size-4 animate-spin" /> : <UploadIcon className="size-4" />}
                {uploading ? "上传中" : "上传欢迎语图片"}
              </Button>
            )}
          </div>

          <div className="grid gap-3 sm:grid-cols-2">
            <div className="flex items-center justify-between gap-3 rounded-2xl border border-[#dbe7f6] bg-white p-4">
              <div className="min-w-0">
                <div className="font-medium text-foreground">发送入住小程序</div>
                <div className="mt-1 text-xs leading-5 text-muted-foreground">
                  {hasMiniProgram ? "使用当前账号已经绑定的小程序。" : "当前账号还没有绑定小程序。"}
                </div>
              </div>
              <Switch
                checked={draft.sendMiniProgram && hasMiniProgram}
                disabled={!draft.enabled || !hasMiniProgram}
                onCheckedChange={(sendMiniProgram) => onChange({ ...draft, sendMiniProgram })}
              />
            </div>
            <div className="flex items-center justify-between gap-3 rounded-2xl border border-[#dbe7f6] bg-white p-4">
              <div className="min-w-0">
                <div className="font-medium text-foreground">发送门店定位</div>
                <div className="mt-1 text-xs leading-5 text-muted-foreground">
                  {hasLocation ? "使用当前账号已经绑定的门店坐标。" : "当前账号还没有绑定门店坐标。"}
                </div>
              </div>
              <Switch
                checked={draft.sendLocation && hasLocation}
                disabled={!draft.enabled || !hasLocation}
                onCheckedChange={(sendLocation) => onChange({ ...draft, sendLocation })}
              />
            </div>
          </div>

          <div className="rounded-2xl bg-[#f6f9ff] p-3 text-xs leading-5 text-muted-foreground">
            发送顺序：文字 → 图片 → 小程序 → 定位。图片和小程序都会通过真实企微协议消息发送。
          </div>
        </div>

        <DialogFooter>
          <Button type="button" variant="outline" onClick={() => void closeDialog()} disabled={saving || uploading}>取消</Button>
          <Button type="button" onClick={() => void saveAndClose()} disabled={saving || uploading || (draft.enabled && !hasContent)}>
            {saving ? "保存中" : "保存设置"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function buildWelcomeInstanceUpdatePayload(instance: WelcomeCapableInstance, draft: WelcomeSettingsDraft) {
  return {
    id: instance.id,
    guid: instance.guid,
    channelId: instance.channelId,
    employeeUserId: instance.employeeUserId,
    employeeName: instance.employeeName,
    employeeAvatar: instance.employeeAvatar,
    storeStaffUserId: instance.storeStaffUserId || 0,
    intentProfileId: instance.intentProfileId || 0,
    storeId: instance.storeId || 0,
    storeName: instance.storeName || instance.employeeName,
    storeAddress: instance.storeAddress || "",
    storeContactPhone: instance.storeContactPhone || "",
    storeNavigationName: instance.storeNavigationName || "",
    storeLongitude: instance.storeLongitude || "",
    storeLatitude: instance.storeLatitude || "",
    storeMapProvider: instance.storeMapProvider || "",
    defaultMiniProgramPayload: instance.defaultMiniProgramPayload || "",
    welcomeEnabled: draft.enabled,
    welcomeMessage: draft.message.trim(),
    welcomeImageAssetId: draft.imageAssetId,
    welcomeSendMiniProgram: draft.sendMiniProgram,
    welcomeAskLocation: draft.sendLocation,
    knowledgeBaseId: instance.knowledgeBaseId || 0,
    notifyUrl: instance.notifyUrl || CALLBACK_URL,
    proxy: instance.proxy || "",
    bridgeId: instance.bridgeId || "",
    staffUserIds: instance.staffUserIds || "",
    managedMode: instance.managedMode || "semi",
    serviceHours: instance.serviceHours || "",
    frontDeskMode: instance.frontDeskMode || "unmanned",
    frontDeskHours: instance.frontDeskHours || "",
    storeRoomConversationId: instance.storeRoomConversationId || "",
    storeRoomNotifyEnabled: instance.storeRoomNotifyEnabled === true,
    storeRoomAtList: instance.storeRoomAtList || "",
    fallbackToHQ: instance.fallbackToHQ !== false,
    manualTimeoutMinutes: instance.manualTimeoutMinutes || 10,
    aiReplyEnabled: instance.aiReplyEnabled !== false,
    personaPrompt: instance.personaPrompt || "",
    autoAcceptFriendRequest: instance.autoAcceptFriendRequest === true,
    autoAcceptFriendRemarkTemplate: instance.autoAcceptFriendRemarkTemplate || "",
    contextMaxMessages: instance.contextMaxMessages || 15,
    contextMaxTokens: instance.contextMaxTokens || 8000,
    contextCompressionEnabled: instance.contextCompressionEnabled !== false,
    status: instance.status,
    remark: instance.remark || "",
  } satisfies CreateWxWorkProtocolInstancePayload & {
    id: number
    welcomeEnabled: boolean
    welcomeImageAssetId: string
  }
}

function buildReceptionInstanceUpdatePayload(instance: WelcomeCapableInstance, draft: ReceptionSettingsDraft) {
  const payload = buildWelcomeInstanceUpdatePayload(instance, {
    enabled: instance.welcomeEnabled !== false,
    message: instance.welcomeMessage || "",
    imageAssetId: instance.welcomeImageAssetId || "",
    imageUrl: instance.welcomeImageUrl || "",
    uploadedImageRecordId: 0,
    sendMiniProgram: instance.welcomeSendMiniProgram === true,
    sendLocation: instance.welcomeAskLocation === true,
  })
  return {
    ...payload,
    personaPrompt: draft.personaPrompt.trim(),
    frontDeskMode: draft.frontDeskMode,
    frontDeskHours: draft.frontDeskMode === "scheduled" ? draft.frontDeskHours.trim() : "",
  }
}

function ReceptionSettingsDialog({
  instance,
  draft,
  saving,
  onOpenChange,
  onChange,
  onSave,
}: {
  instance: WelcomeCapableInstance | null
  draft: ReceptionSettingsDraft
  saving: boolean
  onOpenChange: (open: boolean) => void
  onChange: (draft: ReceptionSettingsDraft) => void
  onSave: () => void
}) {
  const modes: Array<{ value: ReceptionSettingsDraft["frontDeskMode"]; label: string; description: string }> = [
    { value: "unmanned", label: "无人化酒店", description: "不设常驻前台，不会无依据引导客人去前台。" },
    { value: "staffed", label: "有前台酒店", description: "仍需知识库或门店配置明确支持，不能凭经营模式编造能力。" },
    { value: "scheduled", label: "分时段前台", description: "仅在配置时段内披露前台状态。" },
  ]
  return (
    <Dialog open={Boolean(instance)} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-2xl rounded-3xl p-5">
        <DialogHeader>
          <DialogTitle>接待人设</DialogTitle>
          <DialogDescription>
            {instance ? `${repairMojibakeText(instance.employeeName) || instance.guid} 的接待身份和门店经营模式。` : ""}
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-5">
          <div className="space-y-2">
            <div className="text-sm font-medium">门店接待模式</div>
            <div className="grid gap-2 sm:grid-cols-3">
              {modes.map((mode) => (
                <button
                  key={mode.value}
                  type="button"
                  className={`rounded-xl border p-3 text-left transition-colors ${draft.frontDeskMode === mode.value ? "border-primary bg-primary/5" : "border-border bg-background hover:bg-muted/50"}`}
                  onClick={() => onChange({ ...draft, frontDeskMode: mode.value })}
                >
                  <div className="text-sm font-medium">{mode.label}</div>
                  <div className="mt-1 text-xs leading-5 text-muted-foreground">{mode.description}</div>
                </button>
              ))}
            </div>
          </div>
          {draft.frontDeskMode === "scheduled" ? (
            <div className="space-y-2">
              <label htmlFor="frontDeskHours" className="text-sm font-medium">前台服务时段</label>
              <Input
                id="frontDeskHours"
                value={draft.frontDeskHours}
                placeholder="例如：08:00-22:00；多个时段用分号分隔"
                onChange={(event) => onChange({ ...draft, frontDeskHours: event.target.value })}
              />
            </div>
          ) : null}
          <div className="space-y-2">
            <label htmlFor="personaPrompt" className="text-sm font-medium">人设提示词</label>
            <Textarea
              id="personaPrompt"
              value={draft.personaPrompt}
              rows={7}
              placeholder="例如：你是线上酒店接待，说话简短、自然。"
              onChange={(event) => onChange({ ...draft, personaPrompt: event.target.value })}
            />
            <p className="text-xs leading-5 text-muted-foreground">经营模式作为结构化上下文生效，不会新增意图分类，也不会改动意图识别 JSON。</p>
          </div>
        </div>
        <DialogFooter>
          <Button type="button" variant="outline" onClick={() => onOpenChange(false)} disabled={saving}>取消</Button>
          <Button type="button" onClick={onSave} disabled={saving}>{saving ? "保存中" : "保存设置"}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

export function WxWorkProtocolInstanceManager({
  layout = "page",
  onChanged,
  tableShellClassName,
  hideCreateActions = false,
}: WxWorkProtocolInstanceManagerProps) {
  const { session } = useAuth()
  const [channels, setChannels] = useState<AdminChannel[]>([])
  const [knowledgeBases, setKnowledgeBases] = useState<KnowledgeBase[]>([])
  const [intentProfiles, setIntentProfiles] = useState<ReplyIntentProfile[]>([])
  const [reloadKey, setReloadKey] = useState(0)
  const [modelAssignmentInstance, setModelAssignmentInstance] = useState<WxWorkProtocolInstance | null>(null)
  const [fastGPTModelProfileInstance, setFastGPTModelProfileInstance] = useState<WxWorkProtocolInstance | null>(null)
  const [welcomeSettingsInstance, setWelcomeSettingsInstance] = useState<WelcomeCapableInstance | null>(null)
  const [welcomeSettingsDraft, setWelcomeSettingsDraft] = useState<WelcomeSettingsDraft>({
    enabled: true,
    message: "",
    imageAssetId: "",
    imageUrl: "",
    uploadedImageRecordId: 0,
    sendMiniProgram: false,
    sendLocation: false,
  })
  const [welcomeSettingsSaving, setWelcomeSettingsSaving] = useState(false)
  const [receptionSettingsInstance, setReceptionSettingsInstance] = useState<WelcomeCapableInstance | null>(null)
  const [receptionSettingsDraft, setReceptionSettingsDraft] = useState<ReceptionSettingsDraft>({
    personaPrompt: "",
    frontDeskMode: "unmanned",
    frontDeskHours: "",
  })
  const [receptionSettingsSaving, setReceptionSettingsSaving] = useState(false)
  const [locatingStoreCoordinates, setLocatingStoreCoordinates] = useState(false)
  const [bindingDialogOpen, setBindingDialogOpen] = useState(false)
  const permissionSet = useMemo(() => new Set(session?.permissions ?? []), [session?.permissions])
  const canViewChannels = permissionSet.has("channel.view")
  const canCreateChannels = canViewChannels && permissionSet.has("channel.create")
  const canUpdateChannels = canViewChannels && permissionSet.has("channel.update")
  const canDeleteChannels = canViewChannels && permissionSet.has("channel.delete")
  const canViewKnowledgeBases = permissionSet.has("knowledgeBase.view")
  const canViewUsers = permissionSet.has("user.view")
  const canViewModelAssignments = permissionSet.has("tenantModelAssignment.view")
  const canUpdateModelAssignments = permissionSet.has("tenantModelAssignment.update")
  const canManageFastGPTModelProfile = permissionSet.has("aiConfig.update")

  useEffect(() => {
    if (!canViewChannels) {
      return
    }

    async function loadOptions() {
      try {
        const [channelPage, kbList, intentProfilePage] = await Promise.all([
          fetchChannels({ channelType: "wxwork_protocol", status: Status.Ok, limit: 200 }),
          canViewKnowledgeBases ? fetchKnowledgeBasesAll({ status: Status.Ok }) : Promise.resolve([]),
          canViewModelAssignments
            ? fetchReplyIntentProfiles({ status: Status.Ok, limit: 200 })
            : Promise.resolve({ results: [] as ReplyIntentProfile[] }),
        ])
        setChannels(channelPage.results)
        setKnowledgeBases(kbList)
        setIntentProfiles(intentProfilePage.results)
      } catch (error) {
        toast.error(error instanceof Error ? error.message : "加载选项失败")
      }
    }
    void loadOptions()
  }, [canViewChannels, canViewKnowledgeBases, canViewModelAssignments])

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

  const intentProfileOptions = useMemo(
    () => [
      { value: "0", label: "暂不设置" },
      ...intentProfiles.map((item) => ({
        value: String(item.id),
        label: `${item.name}${item.industryCode ? ` · ${item.industryCode}` : ""}`,
      })),
    ],
    [intentProfiles],
  )

  function notifyChanged() {
    setReloadKey((value) => value + 1)
    onChanged?.()
  }

  async function replaceLoggedInAccount(item: WxWorkProtocolInstance) {
    if (!canUpdateChannels) return
    const replacement = await createWxWorkProtocolReplacementSetup({ id: item.id })
    const url = replacement.remoteSetupUrl || `${window.location.origin}/wxwork-remote-setup?token=${encodeURIComponent(replacement.remoteSetupToken || "")}`
    await navigator.clipboard.writeText(url)
    toast.success("替换链接已复制；新员工号验证成功前，旧员工号继续工作")
    notifyChanged()
  }

  function openModelAssignments(item: WxWorkProtocolInstance) {
    if (!canViewModelAssignments) return
    setModelAssignmentInstance(item)
  }

  function openWelcomeSettings(item: WxWorkProtocolInstance) {
    const extended = item as WelcomeCapableInstance
    const welcomeImageAssetId = extended.welcomeImageAssetId || ""
    setWelcomeSettingsInstance(extended)
    setWelcomeSettingsDraft({
      enabled: extended.welcomeEnabled !== false,
      message: repairMojibakeText(extended.welcomeMessage || ""),
      imageAssetId: welcomeImageAssetId,
      imageUrl: extended.welcomeImageUrl || buildAssetFileURL(welcomeImageAssetId),
      uploadedImageRecordId: 0,
      sendMiniProgram: extended.welcomeSendMiniProgram === true,
      sendLocation: extended.welcomeAskLocation === true,
    })
  }

  function openReceptionSettings(item: WxWorkProtocolInstance) {
    const extended = item as WelcomeCapableInstance
    const mode = extended.frontDeskMode === "staffed" || extended.frontDeskMode === "scheduled" ? extended.frontDeskMode : "unmanned"
    setReceptionSettingsInstance(extended)
    setReceptionSettingsDraft({
      personaPrompt: repairMojibakeText(extended.personaPrompt || ""),
      frontDeskMode: mode,
      frontDeskHours: extended.frontDeskHours || "",
    })
  }

  async function saveReceptionSettings() {
    if (!receptionSettingsInstance) return
    if (receptionSettingsDraft.frontDeskMode === "scheduled" && !receptionSettingsDraft.frontDeskHours.trim()) {
      toast.error("请填写前台服务时段")
      return
    }
    setReceptionSettingsSaving(true)
    try {
      await updateWxWorkProtocolInstance(buildReceptionInstanceUpdatePayload(receptionSettingsInstance, receptionSettingsDraft))
      toast.success("接待人设已保存")
      setReceptionSettingsInstance(null)
      notifyChanged()
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "保存接待人设失败")
    } finally {
      setReceptionSettingsSaving(false)
    }
  }

  async function saveWelcomeSettings(): Promise<boolean> {
    if (!welcomeSettingsInstance) return false
    setWelcomeSettingsSaving(true)
    try {
      const payload = buildWelcomeInstanceUpdatePayload(welcomeSettingsInstance, welcomeSettingsDraft)
      await updateWxWorkProtocolInstance(payload)
      toast.success("欢迎语设置已保存")
      notifyChanged()
      return true
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "保存欢迎语设置失败")
      return false
    } finally {
      setWelcomeSettingsSaving(false)
    }
  }

  function renderGeoPicker(context: {
    setValue: (name: string, value: string) => void
  }) {
    return (
      <div className="agentdesk-subtle-surface rounded-xl p-3">
        <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
          <div className="text-sm leading-6 text-muted-foreground">
            门店在现场打开后台时，可用浏览器定位自动填入经纬度；定位不可用时请从地图复制坐标手动填写。地址名称仍建议人工核对。
          </div>
          <Button
            type="button"
            variant="outline"
            size="sm"
            className="agentdesk-soft-button h-9 rounded-lg"
            disabled={locatingStoreCoordinates}
            onClick={async () => {
              setLocatingStoreCoordinates(true)
              try {
                const coordinates = await getBrowserCoordinates()
                context.setValue("storeLatitude", coordinates.latitude.toFixed(6))
                context.setValue("storeLongitude", coordinates.longitude.toFixed(6))
                context.setValue("storeMapProvider", "browser_geolocation")
                toast.success(`已填入当前坐标（精度约 ${Math.round(coordinates.accuracy)} 米），请确认是否为门店位置`)
              } catch (error) {
                toast.error(error instanceof Error ? error.message : "获取坐标失败，请手动填写经纬度")
              } finally {
                setLocatingStoreCoordinates(false)
              }
            }}
          >
            {locatingStoreCoordinates ? <RotateCwIcon className="size-4 animate-spin" /> : <LocateFixedIcon className="size-4" />}
            {locatingStoreCoordinates ? "正在定位" : "一键获取当前坐标"}
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
            <div>请在门店现场使用“一键获取当前坐标”，或从地图复制经纬度手动填写；客户发送的定位不会改写这里的门店坐标。</div>
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

  const rowActions: DashboardCrudRowAction<WxWorkProtocolInstance>[] = []
  if (canUpdateChannels) {
    rowActions.push({
      key: "receptionSettings",
      label: "接待人设",
      icon: <UserRoundCogIcon className="size-4" />,
      run: async ({ item }) => openReceptionSettings(item),
    })
    rowActions.push({
      key: "welcomeSettings",
      label: "欢迎语设置",
      icon: <MessageSquareTextIcon className="size-4" />,
      run: async ({ item }) => openWelcomeSettings(item),
    })
  }
  if (canViewModelAssignments) {
    rowActions.push({
      key: "modelAssignments",
      label: "模型分配",
      icon: <SlidersHorizontalIcon className="size-4" />,
      run: async ({ item }) => openModelAssignments(item),
    })
  }
  if (canManageFastGPTModelProfile) {
    rowActions.push({
      key: "fastGPTModelProfile",
      label: "知识库模型设置",
      icon: <DatabaseZapIcon className="size-4" />,
      run: async ({ item }) => setFastGPTModelProfileInstance(item),
    })
  }
  if (canUpdateChannels) {
    rowActions.push({
      key: "replaceLogin",
      label: "更换登录员工号",
      icon: <QrCodeIcon className="size-4" />,
      confirm: (item) => ({
        title: "更换登录员工号",
        description: `会生成独立替换链接。${repairMojibakeText(item.employeeName) || "当前员工号"} 会继续工作，直到新员工号扫码并通过原门店主邮箱验证后才停用。`,
        confirmText: "生成替换链接",
        cancelText: "取消",
      }),
      run: async ({ item }) => replaceLoggedInAccount(item),
    })
  }

  if (!canViewChannels) {
    return null
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
          {!hideCreateActions && canCreateChannels && canViewUsers ? (
            <Button className="rounded-lg" onClick={() => setBindingDialogOpen(true)}>
              <PlusIcon className="size-4" />
              绑定企微员工号
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
	                <div className="truncate text-xs text-muted-foreground">{item.storeName ? `店名：${repairMojibakeText(item.storeName)}` : "未填写店名"}</div>
              </div>
            </div>
          ),
        },
        {
          key: "binding",
          label: "绑定",
          render: (item) => (
            <div className="space-y-1 text-sm">
	              <div className="font-medium text-foreground">{repairMojibakeText(item.storeName) || repairMojibakeText(item.employeeName) || `账号 ${item.id}`}</div>
	              <div className="text-xs text-muted-foreground">系统账号：{repairMojibakeText(item.storeStaffUserName) || `#${item.storeStaffUserId || "未绑定"}`}</div>
              <div className="text-xs text-muted-foreground">
                {repairMojibakeText(item.knowledgeBaseName) || `知识库 ${item.knowledgeBaseId || "未配置"}`}
              </div>
              <div className="text-xs text-muted-foreground">
                意图行业：{intentProfiles.find((profile) => profile.id === item.intentProfileId)?.name || item.intentProfileName || "未设置"}
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
          key: "aiRuntime",
          label: "AI 托管",
          render: (item) => (
            <div className="space-y-1 text-sm">
              <div className="flex flex-wrap gap-1">
                <Badge variant={item.aiReplyEnabled ? "default" : "secondary"} className="rounded-md text-[10px]">
                  {item.aiReplyEnabled ? "AI开启" : "AI关闭"}
                </Badge>
                <Badge variant="outline" className="rounded-md text-[10px]">
                  客户 {item.customerCount || 0}
                </Badge>
                {item.manualAttentionCount > 0 ? (
                  <Badge variant="destructive" className="rounded-md text-[10px]">
                    待人工 {item.manualAttentionCount}
                  </Badge>
                ) : null}
              </div>
              <div className="max-w-48 truncate text-xs text-muted-foreground">
                知识库：{repairMojibakeText(item.knowledgeBaseName) || `#${item.knowledgeBaseId || "未配置"}`}
              </div>
              <div className="max-w-48 truncate text-xs text-muted-foreground">
                意图行业：{intentProfiles.find((profile) => profile.id === item.intentProfileId)?.name || item.intentProfileName || "未设置"}
              </div>
              <div className="max-w-48 truncate text-xs text-muted-foreground">员工号覆盖租户默认，仅可选择平台授权模型</div>
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
      showCreate={false}
      showEdit={canUpdateChannels}
      createItem={async () => { throw new Error("请从绑定企微员工号入口创建") }}
      updateItem={async (item, payload) => {
        if (!canUpdateChannels) throw new Error("当前账号没有更新接入渠道的权限")
        const ret = await updateWxWorkProtocolInstance({ id: item.id, ...payload })
        notifyChanged()
        return ret
      }}
      deleteItem={
        canDeleteChannels
          ? async (item) => {
              const ret = await deleteWxWorkProtocolInstance(item.id)
              notifyChanged()
              return ret
            }
          : undefined
      }
      rowActions={rowActions}
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
	          { name: "storeName", label: "门店名称", type: "text", placeholder: "例如：丽斯未来酒店杭州某某店", description: "该名称来自绑定的系统账号所代表的门店。" },
          {
            name: "intentProfileId",
            label: "意图行业",
            type: "select",
            defaultValue: "0",
            valueFromItem: (item) => String(item.intentProfileId || 0),
            options: intentProfileOptions,
            description: "决定这个员工号使用的意图检测提示词和分类；启用 AI 前必须设置。",
          },
	          { name: "storeId", label: "门店 ID", type: "custom", valueFromItem: (item) => item.storeId, render: () => null },
          { name: "storeLocationGuide", label: "门店定位说明", type: "custom", render: renderLocationGuide },
          { name: "storeAddress", label: "门店地址", type: "text", placeholder: "例如：上海市..." },
          { name: "storeContactPhone", label: "联系电话", type: "text", placeholder: "例如：0551-88888888 / 13800000000", description: "客户询问酒店电话时发送这个账号配置的电话变量，不从地址或备注里猜。" },
          { name: "storeNavigationName", label: "导航名称", type: "text", placeholder: "例如：丽斯未来酒店某某店" },
          { name: "storeLatitude", label: "门店纬度", type: "text", placeholder: "例如：31.230416" },
          { name: "storeLongitude", label: "门店经度", type: "text", placeholder: "例如：121.473701" },
          { name: "storeMapProvider", label: "坐标来源", type: "text", placeholder: "browser_geolocation / amap / tencent" },
          { name: "storeGeoPicker", label: "门店坐标", type: "custom", render: renderGeoPicker },
	          {
	            name: "resourceBindingSection",
            label: "资源绑定",
            type: "section",
	            description: "门店知识库决定酒店信息类回复；电话、定位、小程序等变量来自当前员工号绑定。平台管理员可在“模型分配”里选择租户已授权模型。",
	          },
	          {
	            name: "knowledgeBaseId",
	            label: "当前启用知识库",
	            type: "select",
	            defaultValue: "0",
	            valueFromItem: (item) => String(item.knowledgeBaseId || 0),
	            options: [{ value: "0", label: "暂不绑定" }, ...knowledgeBaseOptions],
	            description: "当前员工号只检索这里明确绑定的一个 FastGPT 数据集；切换后不会并行召回其他门店知识库。",
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
            description: "这个策略绑定到已分配门店员工号角色的系统账号上；该账号代表一家门店，再绑定实际使用的企微员工号。",
          },
          { name: "serviceHours", label: "门店自行接待时段", type: "text", placeholder: "例如：09:00-22:00；半托管模式按此时段通知门店群" },
          { name: "storeRoomNotifyEnabled", label: "启用门店群通知", type: "switch" },
          { name: "storeRoomConversationId", label: "门店群", type: "custom", render: () => null },
          { name: "storeRoomAtList", label: "@ 成员", type: "custom", render: () => null },
          { name: "storeRoomPicker", label: "门店群和 @ 成员", type: "custom", render: renderStoreRoomPicker },
          { name: "manualTimeoutMinutes", label: "人工超时分钟", type: "number", min: 1, max: 120 },
          {
            name: "automationSection",
            label: "自动化开关",
            type: "section",
            description: "AI 回复开关只控制当前员工号是否由回复引擎托管；模型按员工号覆盖、租户默认、授权池兜底依次解析。",
          },
          { name: "aiReplyEnabled", label: "AI 托管回复", type: "switch" },
          {
            name: "autoAcceptFriendRequest",
            label: "自动通过好友申请",
            type: "switch",
            description: "收到企微好友申请回调后立即处理；定时同步只用于补偿漏回调。",
          },
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
	          storeStaffUserId: context.item?.storeStaffUserId || 0,
          intentProfileId: Number(values.intentProfileId || 0),
	          storeId: Number(values.storeId || 0),
	          storeName: String(values.storeName || context.item?.storeName || values.employeeName || ""),
	          storeAddress: String(values.storeAddress || ""),
          storeContactPhone: String(values.storeContactPhone || ""),
          storeNavigationName: String(values.storeNavigationName || ""),
          storeLatitude: String(values.storeLatitude || ""),
          storeLongitude: String(values.storeLongitude || ""),
          storeMapProvider: String(values.storeMapProvider || ""),
          defaultMiniProgramPayload: context.item?.defaultMiniProgramPayload || "",
          welcomeEnabled: (context.item as WelcomeCapableInstance | undefined)?.welcomeEnabled ?? true,
          welcomeMessage: context.item?.welcomeMessage || DEFAULT_WELCOME_MESSAGE,
          welcomeImageAssetId: (context.item as WelcomeCapableInstance | undefined)?.welcomeImageAssetId || "",
          welcomeSendMiniProgram: context.item?.welcomeSendMiniProgram ?? true,
          welcomeAskLocation: context.item?.welcomeAskLocation ?? true,
	          knowledgeBaseId: Number(values.knowledgeBaseId || 0),
          notifyUrl: context.item?.notifyUrl || CALLBACK_URL,
          proxy: context.item?.proxy || "",
          bridgeId: context.item?.bridgeId || "",
          staffUserIds: context.item?.staffUserIds || "",
          managedMode: String(values.managedMode || context.item?.managedMode || "semi"),
          serviceHours: String(values.serviceHours || ""),
          frontDeskMode: context.item?.frontDeskMode || "unmanned",
          frontDeskHours: context.item?.frontDeskHours || "",
          storeRoomConversationId: String(values.storeRoomConversationId || ""),
          storeRoomNotifyEnabled: values.storeRoomNotifyEnabled === true,
          storeRoomAtList: String(values.storeRoomAtList || ""),
          fallbackToHQ: String(values.managedMode || context.item?.managedMode || "semi") !== "none",
          manualTimeoutMinutes: Number(values.manualTimeoutMinutes || 10),
          aiReplyEnabled: values.aiReplyEnabled !== false,
          personaPrompt: context.item?.personaPrompt || "",
          autoAcceptFriendRequest: values.autoAcceptFriendRequest === true,
          autoAcceptFriendRemarkTemplate: String(values.autoAcceptFriendRemarkTemplate || ""),
          contextMaxMessages: context.item?.contextMaxMessages || 15,
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
    <WxWorkModelAssignmentDialog
      open={canViewModelAssignments && Boolean(modelAssignmentInstance)}
      instance={modelAssignmentInstance}
      canUpdate={canViewModelAssignments && canUpdateModelAssignments}
      onOpenChange={(open) => {
        if (!open) {
          setModelAssignmentInstance(null)
        }
      }}
    />
    <FastGPTModelProfileDialog
      open={Boolean(fastGPTModelProfileInstance)}
      instance={fastGPTModelProfileInstance}
      canSave={canManageFastGPTModelProfile}
      onOpenChange={(open) => {
        if (!open) setFastGPTModelProfileInstance(null)
      }}
      onSaved={() => {
        setReloadKey((value) => value + 1)
        onChanged?.()
      }}
    />
    <WelcomeSettingsDialog
      instance={welcomeSettingsInstance}
      draft={welcomeSettingsDraft}
      saving={welcomeSettingsSaving}
      onOpenChange={(open) => {
        if (!open) setWelcomeSettingsInstance(null)
      }}
      onChange={setWelcomeSettingsDraft}
      onSave={saveWelcomeSettings}
    />
    <ReceptionSettingsDialog
      instance={receptionSettingsInstance}
      draft={receptionSettingsDraft}
      saving={receptionSettingsSaving}
      onOpenChange={(open) => {
        if (!open) setReceptionSettingsInstance(null)
      }}
      onChange={setReceptionSettingsDraft}
      onSave={() => void saveReceptionSettings()}
    />
    <WxWorkProtocolBindingDialog
      open={canCreateChannels && canViewUsers && bindingDialogOpen}
      onOpenChange={setBindingDialogOpen}
      onChanged={() => notifyChanged()}
    />
    </>
  )
}

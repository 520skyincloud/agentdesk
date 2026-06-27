"use client"

import { useEffect, useMemo, useState } from "react"
import { zodResolver } from "@hookform/resolvers/zod"
import { Controller, Resolver, useForm, useWatch } from "react-hook-form"
import { z } from "zod/v4"
import { CopyIcon, ExternalLinkIcon } from "lucide-react"
import { toast } from "sonner"

import { getWidgetDemoPath } from "@/components/support-chat/demo-navigation"
import { OptionCombobox } from "@/components/option-combobox"
import { ProjectDialog } from "@/components/project-dialog"
import { Button } from "@/components/ui/button"
import {
  Field,
  FieldContent,
  FieldError,
  FieldLabel,
} from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"
import {
  type AIAgent,
  type AdminChannel,
  type CreateAdminChannelPayload,
  type WxWorkKFAccount,
  fetchAIAgentsAll,
  fetchChannel,
  fetchWxWorkKFAccounts,
  resetChannelUserTokenSecret,
} from "@/lib/api/admin"
import { useI18n } from "@/i18n/provider"

type ChannelFormDialogProps = {
  open: boolean
  saving: boolean
  itemId: number | null
  onOpenChange: (open: boolean) => void
  onSubmit: (payload: CreateAdminChannelPayload) => Promise<void>
}

type Translate = (key: string, values?: Record<string, string | number>) => string

type WebChannelConfig = {
  title?: string
  subtitle?: string
  themeColor?: string
  position?: "left" | "right"
  width?: string
  userTokenSecret?: string
}

type WechatMPChannelConfig = {
  title?: string
  subtitle?: string
  themeColor?: string
  userTokenSecret?: string
}

type WxWorkCLIChannelConfig = {
  bridgeToken?: string
  defaultChatType?: number
}

type WxWorkProtocolChannelConfig = {
  appKey?: string
  appSecret?: string
  baseUrl?: string
  callbackToken?: string
  wecdnBaseUrl?: string
  publicAssetBaseUrl?: string
}

function getDefaultWebChannelConfig(t: Translate): Required<WebChannelConfig> {
  return {
    title: t("channel.defaultTitleWeb"),
    subtitle: t("channel.defaultSubtitle"),
    themeColor: "#2563eb",
    position: "right",
    width: "380px",
    userTokenSecret: "",
  }
}

function createSchema(t: Translate) {
  return z
    .object({
      channelType: z.enum(["web", "wechat_mp", "wxwork_protocol"], t("channel.typeRequired")),
      aiAgentId: z.string().trim().regex(/^\d+$/, t("channel.agentRequired")),
      name: z.string().trim().min(1, t("channel.nameRequired")),
      openKfId: z.string().trim(),
      protocolAppKey: z.string().trim(),
      protocolAppSecret: z.string().trim(),
      protocolBaseUrl: z.string().trim(),
      protocolCallbackToken: z.string().trim(),
      protocolWecdnBaseUrl: z.string().trim(),
      protocolPublicAssetBaseUrl: z.string().trim(),
      bridgeToken: z.string().trim(),
      defaultChatType: z.enum(["1", "2"]),
      widgetTitle: z.string().trim(),
      widgetSubtitle: z.string().trim(),
      widgetThemeColor: z.string().trim(),
      widgetPosition: z.enum(["left", "right"]),
      widgetWidth: z.string().trim(),
      userTokenSecret: z.string().trim(),
      remark: z.string().trim(),
    })
    .superRefine((values, ctx) => {
      if (values.channelType === "wxwork_protocol") {
        if (!values.protocolAppKey.trim()) {
          ctx.addIssue({
            code: "custom",
            path: ["protocolAppKey"],
            message: "请填写 App Key",
          })
        }
        if (!values.protocolAppSecret.trim()) {
          ctx.addIssue({
            code: "custom",
            path: ["protocolAppSecret"],
            message: "请填写 App Secret",
          })
        }
      }
    })
}

type EditForm = {
  channelType: "web" | "wechat_mp" | "wxwork_protocol"
  aiAgentId: string
  name: string
  openKfId: string
  protocolAppKey: string
  protocolAppSecret: string
  protocolBaseUrl: string
  protocolCallbackToken: string
  protocolWecdnBaseUrl: string
  protocolPublicAssetBaseUrl: string
  bridgeToken: string
  defaultChatType: "1" | "2"
  widgetTitle: string
  widgetSubtitle: string
  widgetThemeColor: string
  widgetPosition: "left" | "right"
  widgetWidth: string
  userTokenSecret: string
  remark: string
}

function createEmptyForm(t: Translate): EditForm {
  const defaultWebChannelConfig = getDefaultWebChannelConfig(t)
  return {
    channelType: "web",
    aiAgentId: "",
    name: "",
    openKfId: "",
    protocolAppKey: "",
    protocolAppSecret: "",
    protocolBaseUrl: "https://chat-api.juhebot.com/open/GuidRequest",
    protocolCallbackToken: "",
    protocolWecdnBaseUrl: "",
    protocolPublicAssetBaseUrl: "",
    bridgeToken: "",
    defaultChatType: "1",
    widgetTitle: defaultWebChannelConfig.title,
    widgetSubtitle: defaultWebChannelConfig.subtitle,
    widgetThemeColor: defaultWebChannelConfig.themeColor,
    widgetPosition: defaultWebChannelConfig.position,
    widgetWidth: defaultWebChannelConfig.width,
    userTokenSecret: "",
    remark: "",
  }
}

function parseOpenKfId(configJson: string): string {
  if (!configJson.trim()) {
    return ""
  }
  try {
    const parsed = JSON.parse(configJson) as { openKfId?: string }
    return typeof parsed.openKfId === "string" ? parsed.openKfId.trim() : ""
  } catch {
    return ""
  }
}

function parseWxWorkProtocolChannelConfig(configJson: string): Required<WxWorkProtocolChannelConfig> {
  const fallback = {
    appKey: "",
    appSecret: "",
    baseUrl: "https://chat-api.juhebot.com/open/GuidRequest",
    callbackToken: "",
    wecdnBaseUrl: "",
    publicAssetBaseUrl: "",
  }
  if (!configJson.trim()) {
    return fallback
  }
  try {
    const parsed = JSON.parse(configJson) as WxWorkProtocolChannelConfig
    return {
      appKey: parsed.appKey?.trim() || "",
      appSecret: parsed.appSecret?.trim() || "",
      baseUrl: parsed.baseUrl?.trim() || fallback.baseUrl,
      callbackToken: parsed.callbackToken?.trim() || "",
      wecdnBaseUrl: parsed.wecdnBaseUrl?.trim() || "",
      publicAssetBaseUrl: parsed.publicAssetBaseUrl?.trim() || "",
    }
  } catch {
    return fallback
  }
}

function parseWxWorkCLIChannelConfig(configJson: string): Required<WxWorkCLIChannelConfig> {
  if (!configJson.trim()) {
    return { bridgeToken: "", defaultChatType: 1 }
  }
  try {
    const parsed = JSON.parse(configJson) as WxWorkCLIChannelConfig
    return {
      bridgeToken: parsed.bridgeToken?.trim() || "",
      defaultChatType: parsed.defaultChatType === 2 ? 2 : 1,
    }
  } catch {
    return { bridgeToken: "", defaultChatType: 1 }
  }
}

function parseWebChannelConfig(configJson: string, t: Translate): Required<WebChannelConfig> {
  const defaultWebChannelConfig = getDefaultWebChannelConfig(t)
  if (!configJson.trim()) {
    return defaultWebChannelConfig
  }
  try {
    const parsed = JSON.parse(configJson) as WebChannelConfig
    const position = parsed.position === "left" ? "left" : "right"
    return {
      title: parsed.title?.trim() || defaultWebChannelConfig.title,
      subtitle: parsed.subtitle?.trim() ?? defaultWebChannelConfig.subtitle,
      themeColor:
        parsed.themeColor?.trim() || defaultWebChannelConfig.themeColor,
      position,
      width: parsed.width?.trim() || defaultWebChannelConfig.width,
      userTokenSecret: parsed.userTokenSecret?.trim() || "",
    }
  } catch {
    return defaultWebChannelConfig
  }
}

function parseWechatMPChannelConfig(configJson: string, t: Translate): Required<WechatMPChannelConfig> {
  const defaultWebChannelConfig = getDefaultWebChannelConfig(t)
  const fallback = {
    title: t("channel.defaultTitleWechat"),
    subtitle: defaultWebChannelConfig.subtitle,
    themeColor: defaultWebChannelConfig.themeColor,
    userTokenSecret: "",
  }
  if (!configJson.trim()) {
    return fallback
  }
  try {
    const parsed = JSON.parse(configJson) as WechatMPChannelConfig
    return {
      title: parsed.title?.trim() || fallback.title,
      subtitle: parsed.subtitle?.trim() ?? fallback.subtitle,
      themeColor:
        parsed.themeColor?.trim() || defaultWebChannelConfig.themeColor,
      userTokenSecret: parsed.userTokenSecret?.trim() || "",
    }
  } catch {
    return fallback
  }
}

function buildForm(item: AdminChannel | null, t: Translate): EditForm {
  if (!item) {
    return createEmptyForm(t)
  }
  const isWechatMP = item.channelType === "wechat_mp"
  const isWxWorkProtocol = item.channelType === "wxwork_protocol"
  const webConfig = parseWebChannelConfig(item.configJson, t)
  const wechatConfig = isWechatMP
    ? parseWechatMPChannelConfig(item.configJson, t)
    : null
  const wxWorkProtocolConfig = isWxWorkProtocol
    ? parseWxWorkProtocolChannelConfig(item.configJson)
    : null
  return {
    channelType:
      item.channelType === "wxwork_protocol"
          ? "wxwork_protocol"
        : item.channelType === "wechat_mp"
          ? "wechat_mp"
          : "web",
    aiAgentId: item.aiAgentId > 0 ? String(item.aiAgentId) : "",
    name: item.name,
    openKfId: parseOpenKfId(item.configJson),
    protocolAppKey: wxWorkProtocolConfig?.appKey ?? "",
    protocolAppSecret: wxWorkProtocolConfig?.appSecret ?? "",
    protocolBaseUrl: wxWorkProtocolConfig?.baseUrl ?? "https://chat-api.juhebot.com/open/GuidRequest",
    protocolCallbackToken: wxWorkProtocolConfig?.callbackToken ?? "",
    protocolWecdnBaseUrl: wxWorkProtocolConfig?.wecdnBaseUrl ?? "",
    protocolPublicAssetBaseUrl: wxWorkProtocolConfig?.publicAssetBaseUrl ?? "",
    bridgeToken: "",
    defaultChatType: "1",
    widgetTitle: wechatConfig?.title ?? webConfig.title,
    widgetSubtitle: wechatConfig?.subtitle ?? webConfig.subtitle,
    widgetThemeColor: wechatConfig?.themeColor ?? webConfig.themeColor,
    widgetPosition: webConfig.position,
    widgetWidth: webConfig.width,
    userTokenSecret: wechatConfig?.userTokenSecret ?? webConfig.userTokenSecret,
    remark: item.remark || "",
  }
}

function buildPayload(form: EditForm, status: number, t: Translate): CreateAdminChannelPayload {
  const channelType = form.channelType
  const defaultWebChannelConfig = getDefaultWebChannelConfig(t)
  const webLikeConfig = {
    title:
      form.widgetTitle.trim() ||
      (channelType === "wechat_mp" ? t("channel.defaultTitleWechat") : defaultWebChannelConfig.title),
    subtitle: form.widgetSubtitle.trim(),
    themeColor:
      form.widgetThemeColor.trim() || defaultWebChannelConfig.themeColor,
    userTokenSecret: form.userTokenSecret.trim(),
  }
  const configJson =
    channelType === "wxwork_protocol"
        ? JSON.stringify({
            appKey: form.protocolAppKey.trim(),
            appSecret: form.protocolAppSecret.trim(),
            baseUrl: form.protocolBaseUrl.trim() || "https://chat-api.juhebot.com/open/GuidRequest",
            callbackToken: form.protocolCallbackToken.trim(),
            wecdnBaseUrl: form.protocolWecdnBaseUrl.trim(),
            publicAssetBaseUrl: form.protocolPublicAssetBaseUrl.trim(),
          })
      : channelType === "wechat_mp"
        ? JSON.stringify(webLikeConfig)
        : JSON.stringify({
            ...webLikeConfig,
            position: form.widgetPosition || defaultWebChannelConfig.position,
            width: form.widgetWidth.trim() || defaultWebChannelConfig.width,
            userTokenSecret: form.userTokenSecret.trim(),
          })
  return {
    channelType,
    aiAgentId: Number(form.aiAgentId),
    name: form.name.trim(),
    configJson,
    status,
    remark: form.remark.trim(),
  }
}

type ChannelFormBodyProps = Omit<ChannelFormDialogProps, "open">

export function EditDialog({
  open,
  saving,
  itemId,
  onOpenChange,
  onSubmit,
}: ChannelFormDialogProps) {
  if (!open) {
    return null
  }

  return (
    <ChannelFormBody
      key={itemId ? `edit-${itemId}` : "create"}
      itemId={itemId}
      saving={saving}
      onOpenChange={onOpenChange}
      onSubmit={onSubmit}
    />
  )
}

function ChannelFormBody({
  saving,
  itemId,
  onOpenChange,
  onSubmit,
}: ChannelFormBodyProps) {
  const t = useI18n()
  const formId = "channel-edit-form"
  const emptyForm = useMemo(() => createEmptyForm(t), [t])
  const schema = useMemo(() => createSchema(t), [t])
  const resolver = useMemo(
    () =>
      zodResolver(schema as never) as Resolver<
        z.input<typeof schema>,
        undefined,
        z.output<typeof schema>
      >,
    [schema],
  )
  const [loading, setLoading] = useState(false)
  const [aiAgents, setAIAgents] = useState<AIAgent[]>([])
  const [wxWorkKFAccounts, setWxWorkKFAccounts] = useState<WxWorkKFAccount[]>([])
  const [wxWorkKFAccountsLoading, setWxWorkKFAccountsLoading] = useState(false)
  const [wxWorkKFAccountsError, setWxWorkKFAccountsError] = useState("")
  const [channelDetail, setChannelDetail] = useState<AdminChannel | null>(null)
  const [currentStatus, setCurrentStatus] = useState(0)
  const form = useForm<
    z.input<typeof schema>,
    undefined,
    z.output<typeof schema>
  >({
    resolver,
    defaultValues: emptyForm,
  })
  const {
    control,
    handleSubmit,
    register,
    reset,
    setValue,
    formState: { errors },
  } = form
  const channelType = useWatch({ control, name: "channelType" })
  const protocolCallbackToken = useWatch({ control, name: "protocolCallbackToken" })
  const userTokenSecret = useWatch({ control, name: "userTokenSecret" })

  useEffect(() => {
    async function loadAIAgents() {
      try {
        const data = await fetchAIAgentsAll({ status: 1 })
        setAIAgents(data)
      } catch (error) {
        console.error("Failed to load AI agents:", error)
      }
    }
    void loadAIAgents()
  }, [])

  useEffect(() => {
    async function loadDetail() {
      if (!itemId) {
        setCurrentStatus(0)
        setChannelDetail(null)
        reset(emptyForm)
        return
      }
      setLoading(true)
      try {
        const data = await fetchChannel(itemId)
        setChannelDetail(data)
        setCurrentStatus(data.status)
        reset(buildForm(data, t))
      } catch (error) {
        console.error("Failed to load channel:", error)
      } finally {
        setLoading(false)
      }
    }
    void loadDetail()
  }, [emptyForm, itemId, reset, t])

  const aiAgentOptions = aiAgents.map((item) => ({
    value: String(item.id),
    label: item.name,
  }))
  const channelTypeOptions = [
    { value: "web", label: t("channel.typeWeb") },
    { value: "wechat_mp", label: t("channel.typeWechatMp") },
    { value: "wxwork_protocol", label: "企微员工号协议" },
  ] as const
  const widgetPositionOptions = [
    { value: "right", label: t("channel.positionRight") },
    { value: "left", label: t("channel.positionLeft") },
  ] as const
  async function onFormSubmit(values: EditForm) {
    await onSubmit(buildPayload(values, currentStatus, t))
  }

  async function handleResetUserTokenSecret() {
    if (!itemId) {
      return
    }
    if (!window.confirm(t("channel.resetSecretConfirm"))) {
      return
    }
    try {
      const result = await resetChannelUserTokenSecret(itemId)
      setValue("userTokenSecret", result.userTokenSecret, {
        shouldDirty: true,
      })
      if (channelDetail) {
        const parsed = JSON.parse(channelDetail.configJson || "{}") as Record<string, unknown>
        parsed.userTokenSecret = result.userTokenSecret
        setChannelDetail({
          ...channelDetail,
          configJson: JSON.stringify(parsed),
        })
      }
      toast.success(t("channel.resetSecretSuccess"))
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("channel.resetSecretFailed"))
    }
  }

  async function copyUserTokenSecret() {
    if (!userTokenSecret) {
      return
    }
    try {
      await navigator.clipboard.writeText(userTokenSecret)
      toast.success(t("channel.copySecretSuccess"))
    } catch {
      toast.error(t("channel.copyFailed"))
    }
  }

  return (
    <ProjectDialog
      open={true}
      onOpenChange={onOpenChange}
      title={itemId ? t("channel.editTitle") : t("channel.createTitle")}
      size="lg"
      allowFullscreen
      footer={
        <>
          <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
            {t("channel.cancel")}
          </Button>
          <Button type="submit" form={formId} disabled={saving || loading}>
            {saving ? t("channel.saving") : t("channel.save")}
          </Button>
        </>
      }
    >
      {loading ? (
        <div className="flex items-center justify-center py-12">
          <div className="text-muted-foreground">{t("channel.loadingDetail")}</div>
        </div>
      ) : (
        <form id={formId} onSubmit={handleSubmit(onFormSubmit)} className="space-y-5">
          <div className="grid grid-cols-1 gap-4">
            <Field data-invalid={!!errors.name}>
              <FieldLabel htmlFor="channel-name">{t("channel.name")}</FieldLabel>
              <FieldContent>
                <Input id="channel-name" {...register("name")} />
                <FieldError errors={[errors.name]} />
              </FieldContent>
            </Field>

            <Field data-invalid={!!errors.aiAgentId}>
              <FieldLabel>{t("channel.columnAgent")}</FieldLabel>
              <FieldContent>
                <Controller
                  control={control}
                  name="aiAgentId"
                  render={({ field }) => (
                    <OptionCombobox
                      value={field.value}
                      options={aiAgentOptions}
                      placeholder={t("channel.agentRequired")}
                      searchPlaceholder={t("channel.searchAiAgent")}
                      emptyText={t("channel.emptyAiAgent")}
                      onChange={field.onChange}
                    />
                  )}
                />
                <FieldError errors={[errors.aiAgentId]} />
              </FieldContent>
            </Field>

            <Field data-invalid={!!errors.channelType}>
              <FieldLabel>{t("channel.channelType")}</FieldLabel>
              <FieldContent>
                <Controller
                  control={control}
                  name="channelType"
                  render={({ field }) => (
                    <OptionCombobox
                      value={field.value}
                      options={[...channelTypeOptions]}
                      placeholder={t("channel.selectChannelType")}
                      searchPlaceholder={t("channel.searchChannelType")}
                      emptyText={t("channel.emptyChannelType")}
                      onChange={field.onChange}
                    />
                  )}
                />
                <FieldError errors={[errors.channelType]} />
              </FieldContent>
            </Field>
          </div>

          <div className="space-y-4 rounded-md border p-4">
            <div>
              <div className="text-sm font-medium">{t("channel.configTitle")}</div>
              <div className="text-xs text-muted-foreground">
                {channelType === "wxwork_protocol"
                  ? "通过聚合聊天开放平台统一网关接入企微员工号，消息回调进入 AgentDesk 后再由 AI 或总部网页端接管。"
                  : channelType === "wechat_mp"
                    ? t("channel.configWechatDescription")
                    : t("channel.configWebDescription")}
              </div>
            </div>

            {channelType === "wxwork_protocol" ? (
              <div className="space-y-4">
                <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
                  <Field data-invalid={!!errors.protocolAppKey}>
                    <FieldLabel htmlFor="channel-protocol-app-key">App Key</FieldLabel>
                    <FieldContent>
                      <Input id="channel-protocol-app-key" {...register("protocolAppKey")} />
                      <FieldError errors={[errors.protocolAppKey]} />
                    </FieldContent>
                  </Field>
                  <Field data-invalid={!!errors.protocolAppSecret}>
                    <FieldLabel htmlFor="channel-protocol-app-secret">App Secret</FieldLabel>
                    <FieldContent>
                      <Input id="channel-protocol-app-secret" type="password" {...register("protocolAppSecret")} />
                      <FieldError errors={[errors.protocolAppSecret]} />
                    </FieldContent>
                  </Field>
                </div>
                <Field data-invalid={!!errors.protocolBaseUrl}>
                  <FieldLabel htmlFor="channel-protocol-base-url">统一请求地址</FieldLabel>
                  <FieldContent>
                    <Input
                      id="channel-protocol-base-url"
                      className="font-mono text-xs"
                      {...register("protocolBaseUrl")}
                    />
                    <FieldError errors={[errors.protocolBaseUrl]} />
                  </FieldContent>
                </Field>
                <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
                  <Field data-invalid={!!errors.protocolWecdnBaseUrl}>
                    <FieldLabel htmlFor="channel-protocol-wecdn-base-url">私有化云存储地址</FieldLabel>
                    <FieldContent>
                      <Input
                        id="channel-protocol-wecdn-base-url"
                        className="font-mono text-xs"
                        placeholder="http://公网IP:34789"
                        {...register("protocolWecdnBaseUrl")}
                      />
                      <FieldError errors={[errors.protocolWecdnBaseUrl]} />
                    </FieldContent>
                  </Field>
                  <Field data-invalid={!!errors.protocolPublicAssetBaseUrl}>
                    <FieldLabel htmlFor="channel-protocol-public-asset-base-url">AgentDesk 公网地址</FieldLabel>
                    <FieldContent>
                      <Input
                        id="channel-protocol-public-asset-base-url"
                        className="font-mono text-xs"
                        placeholder="https://kefuceshi.example.com"
                        {...register("protocolPublicAssetBaseUrl")}
                      />
                      <FieldError errors={[errors.protocolPublicAssetBaseUrl]} />
                    </FieldContent>
                  </Field>
                </div>
                <div className="rounded-md bg-muted px-3 py-2 text-xs text-muted-foreground">
                  发送图片、语音、文件、视频、GIF 时，系统会先让私有化云存储从 AgentDesk 公网地址拉取资产，换取 file_id/aes_key/md5 后再调用企微发送接口。
                </div>
                {itemId ? (
                  <Field data-invalid={!!errors.protocolCallbackToken}>
                    <FieldLabel htmlFor="channel-protocol-callback-token">Callback Token</FieldLabel>
                    <FieldContent>
                      <div className="flex flex-col gap-2 sm:flex-row">
                        <Input
                          id="channel-protocol-callback-token"
                          readOnly
                          className="font-mono text-xs"
                          {...register("protocolCallbackToken")}
                        />
                        <Button
                          type="button"
                          variant="outline"
                          onClick={() => void navigator.clipboard.writeText(protocolCallbackToken || "")}
                          disabled={!protocolCallbackToken}
                        >
                          <CopyIcon className="size-4" />
                          {t("channel.copy")}
                        </Button>
                      </div>
                      <FieldError errors={[errors.protocolCallbackToken]} />
                    </FieldContent>
                  </Field>
                ) : (
                  <div className="rounded-md bg-muted px-3 py-2 text-sm text-muted-foreground">
                    保存后系统会自动生成 Callback Token。
                  </div>
                )}
                <WxWorkProtocolAccessGuide />
              </div>
            ) : null}

            {channelType === "web" || channelType === "wechat_mp" ? (
              <>
                <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
                  <Field data-invalid={!!errors.widgetTitle}>
                    <FieldLabel htmlFor="channel-widget-title">{t("channel.widgetTitle")}</FieldLabel>
                    <FieldContent>
                      <Input id="channel-widget-title" {...register("widgetTitle")} />
                      <FieldError errors={[errors.widgetTitle]} />
                    </FieldContent>
                  </Field>

                  <Field data-invalid={!!errors.widgetSubtitle}>
                    <FieldLabel htmlFor="channel-widget-subtitle">{t("channel.widgetSubtitle")}</FieldLabel>
                    <FieldContent>
                      <Input
                        id="channel-widget-subtitle"
                        {...register("widgetSubtitle")}
                      />
                      <FieldError errors={[errors.widgetSubtitle]} />
                    </FieldContent>
                  </Field>

                  <Field data-invalid={!!errors.widgetThemeColor}>
                    <FieldLabel htmlFor="channel-widget-theme-color">{t("channel.themeColor")}</FieldLabel>
                    <FieldContent>
                      <Input
                        id="channel-widget-theme-color"
                        placeholder="#2563eb"
                        {...register("widgetThemeColor")}
                      />
                      <FieldError errors={[errors.widgetThemeColor]} />
                    </FieldContent>
                  </Field>

                  {channelType === "web" ? (
                    <>
                      <Field data-invalid={!!errors.widgetPosition}>
                        <FieldLabel>{t("channel.mountPosition")}</FieldLabel>
                        <FieldContent>
                          <Controller
                            control={control}
                            name="widgetPosition"
                            render={({ field }) => (
                              <OptionCombobox
                                value={field.value}
                                options={[...widgetPositionOptions]}
                                placeholder={t("channel.selectMountPosition")}
                                searchPlaceholder={t("channel.searchMountPosition")}
                                emptyText={t("channel.emptyMountPosition")}
                                onChange={field.onChange}
                              />
                            )}
                          />
                          <FieldError errors={[errors.widgetPosition]} />
                        </FieldContent>
                      </Field>

                      <Field data-invalid={!!errors.widgetWidth}>
                        <FieldLabel htmlFor="channel-widget-width">{t("channel.widgetWidth")}</FieldLabel>
                        <FieldContent>
                          <Input
                            id="channel-widget-width"
                            placeholder="380px"
                            {...register("widgetWidth")}
                          />
                          <FieldError errors={[errors.widgetWidth]} />
                        </FieldContent>
                      </Field>
                    </>
                  ) : null}
                </div>
                <div className="space-y-3 rounded-md border p-3">
                  <div>
                    <div className="text-sm font-medium">{t("channel.userJwtSecret")}</div>
                    <div className="text-xs text-muted-foreground">
                      {t("channel.userJwtSecretDescription")}
                    </div>
                  </div>
                  {!itemId ? (
                    <div className="rounded-md bg-muted px-3 py-2 text-sm text-muted-foreground">
                      {t("channel.secretAfterSave")}
                    </div>
                  ) : (
                    <Field data-invalid={!!errors.userTokenSecret}>
                      <FieldLabel htmlFor="channel-user-token-secret">Secret</FieldLabel>
                      <FieldContent>
                        <div className="flex flex-col gap-2 sm:flex-row">
                          <Input
                            id="channel-user-token-secret"
                            readOnly
                            className="font-mono text-xs"
                            {...register("userTokenSecret")}
                          />
                          <div className="flex gap-2">
                            <Button
                              type="button"
                              variant="outline"
                              onClick={copyUserTokenSecret}
                              disabled={!userTokenSecret}
                            >
                              <CopyIcon className="size-4" />
                              {t("channel.copy")}
                            </Button>
                            <Button
                              type="button"
                              variant="outline"
                              onClick={() => void handleResetUserTokenSecret()}
                            >
                              {t("channel.reset")}
                            </Button>
                          </div>
                        </div>
                        <FieldError errors={[errors.userTokenSecret]} />
                      </FieldContent>
                    </Field>
                  )}
                </div>
                {channelType === "wechat_mp" ? (
                  <WechatMPAccessGuide channelId={channelDetail?.channelId || ""} />
                ) : (
                  <WebAccessGuide channelId={channelDetail?.channelId || ""} />
                )}
              </>
            ) : null}
          </div>

          <Field data-invalid={!!errors.remark}>
            <FieldLabel htmlFor="channel-remark">{t("channel.remark")}</FieldLabel>
            <FieldContent>
              <Textarea id="channel-remark" rows={3} {...register("remark")} />
              <FieldError errors={[errors.remark]} />
            </FieldContent>
          </Field>
        </form>
      )}
    </ProjectDialog>
  )
}

function WebAccessGuide({ channelId }: { channelId: string }) {
  const t = useI18n()
  const [origin, setOrigin] = useState("")

  useEffect(() => {
    setOrigin(window.location.origin)
  }, [])

  const accessUrl = useMemo(() => {
    if (!origin || !channelId) {
      return ""
    }
    const url = new URL("/support/chat/", origin)
    url.searchParams.set("channelId", channelId)
    return url.toString()
  }, [channelId, origin])

  const testUrl = useMemo(() => {
    if (!origin || !channelId) {
      return ""
    }
    const url = new URL(getWidgetDemoPath(), origin)
    url.searchParams.set("channelId", channelId)
    return url.toString()
  }, [channelId, origin])

  const snippet = useMemo(() => {
    if (!origin || !channelId) {
      return ""
    }
    return `<script>
  window.AgentDeskConfig = {
    channelId: "${channelId}"
  };
</script>
<script async src="${origin}/sdk/agent-desk-sdk.min.js"></script>`
  }, [channelId, origin])

  async function copyText(text: string, successMessage: string) {
    if (!text) {
      return
    }
    try {
      await navigator.clipboard.writeText(text)
      toast.success(successMessage)
    } catch {
      toast.error(t("channel.copyFailed"))
    }
  }

  return (
    <div className="space-y-4 border-t pt-4">
      <div>
        <div className="text-sm font-medium">{t("channel.webAccessInfo")}</div>
        <div className="text-xs text-muted-foreground">
          {channelId
            ? t("channel.webAccessReady")
            : t("channel.webAccessPending")}
        </div>
      </div>

      {!channelId ? (
        <div className="rounded-md bg-muted px-3 py-2 text-sm text-muted-foreground">
          {t("channel.newChannelPending")}
        </div>
      ) : (
        <div className="space-y-4">
          <div className="space-y-2">
            <div className="text-xs font-medium text-muted-foreground">{t("channel.directAccessUrl")}</div>
            <div className="flex flex-col gap-2 sm:flex-row">
              <Input readOnly value={accessUrl} className="font-mono text-xs" />
              <div className="flex gap-2">
                <Button
                  type="button"
                  variant="outline"
                  size="icon"
                  title={t("channel.copyLink")}
                  onClick={() => copyText(accessUrl, t("channel.copiedAccessLink"))}
                >
                  <CopyIcon className="size-4" />
                </Button>
                <Button
                  type="button"
                  variant="outline"
                  size="icon"
                  title={t("channel.openLink")}
                  onClick={() => window.open(accessUrl, "_blank", "noopener,noreferrer")}
                >
                  <ExternalLinkIcon className="size-4" />
                </Button>
              </div>
            </div>
          </div>

          <div className="space-y-2">
            <div className="flex items-center justify-between gap-2">
              <div className="text-xs font-medium text-muted-foreground">
                {t("channel.embeddedSnippet")}
              </div>
              <Button
                type="button"
                variant="outline"
                size="sm"
                onClick={() => copyText(snippet, t("channel.copiedSnippet"))}
              >
                <CopyIcon className="size-4" />
                {t("channel.copyCode")}
              </Button>
            </div>
            <pre className="max-h-48 overflow-auto rounded-md bg-muted p-3 text-xs leading-5">
              <code>{snippet}</code>
            </pre>
          </div>

          <div className="flex flex-col gap-2 rounded-md bg-muted px-3 py-3 text-xs text-muted-foreground">
            <div className="font-medium text-foreground">{t("channel.accessGuide")}</div>
            <div>{t("channel.webGuide1")}</div>
            <div>{t("channel.webGuide2")}</div>
            <div>{t("channel.webGuide3")}</div>
            <div>{t("channel.webGuide4")}</div>
            <div className="pt-1">
              <Button
                type="button"
                variant="outline"
                size="sm"
                onClick={() => window.open(testUrl, "_blank", "noopener,noreferrer")}
              >
                <ExternalLinkIcon className="size-4" />
                {t("channel.openTestPage")}
              </Button>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}

function WechatMPAccessGuide({ channelId }: { channelId: string }) {
  const t = useI18n()
  const [origin, setOrigin] = useState("")

  useEffect(() => {
    setOrigin(window.location.origin)
  }, [])

  const menuUrl = useMemo(() => {
    if (!origin || !channelId) {
      return ""
    }
    const url = new URL("/support/chat/", origin)
    url.searchParams.set("channelId", channelId)
    return url.toString()
  }, [channelId, origin])

  async function copyText(text: string) {
    if (!text) {
      return
    }
    try {
      await navigator.clipboard.writeText(text)
      toast.success(t("channel.copiedWechatMenuUrl"))
    } catch {
      toast.error(t("channel.copyFailed"))
    }
  }

  return (
    <div className="space-y-4 border-t pt-4">
      <div>
        <div className="text-sm font-medium">{t("channel.wechatAccessInfo")}</div>
        <div className="text-xs text-muted-foreground">
          {channelId
            ? t("channel.wechatAccessReady")
            : t("channel.wechatAccessPending")}
        </div>
      </div>

      {!channelId ? (
        <div className="rounded-md bg-muted px-3 py-2 text-sm text-muted-foreground">
          {t("channel.newChannelPending")}
        </div>
      ) : (
        <div className="space-y-4">
          <div className="space-y-2">
            <div className="text-xs font-medium text-muted-foreground">
              {t("channel.wechatMenuUrl")}
            </div>
            <div className="flex flex-col gap-2 sm:flex-row">
              <Input readOnly value={menuUrl} className="font-mono text-xs" />
              <div className="flex gap-2">
                <Button
                  type="button"
                  variant="outline"
                  size="icon"
                  title={t("channel.copyLink")}
                  onClick={() => copyText(menuUrl)}
                >
                  <CopyIcon className="size-4" />
                </Button>
                <Button
                  type="button"
                  variant="outline"
                  size="icon"
                  title={t("channel.openLink")}
                  onClick={() => window.open(menuUrl, "_blank", "noopener,noreferrer")}
                >
                  <ExternalLinkIcon className="size-4" />
                </Button>
              </div>
            </div>
          </div>

          <div className="flex flex-col gap-2 rounded-md bg-muted px-3 py-3 text-xs text-muted-foreground">
            <div className="font-medium text-foreground">{t("channel.accessGuide")}</div>
            <div>{t("channel.webGuide1")}</div>
            <div>{t("channel.wechatGuide2")}</div>
            <div>{t("channel.wechatGuide3")}</div>
          </div>
        </div>
      )}
    </div>
  )
}

function WxWorkProtocolAccessGuide() {
  const [origin, setOrigin] = useState("")

  useEffect(() => {
    setOrigin(window.location.origin)
  }, [])

  const callbackUrl = useMemo(() => {
    if (!origin) {
      return ""
    }
    return new URL("/api/third/wxwork-protocol/callback", origin).toString()
  }, [origin])

  async function copyCallbackUrl() {
    if (!callbackUrl) {
      return
    }
    try {
      await navigator.clipboard.writeText(callbackUrl)
      toast.success("已复制回调地址")
    } catch {
      toast.error("复制失败")
    }
  }

  return (
    <div className="space-y-4 border-t pt-4">
      <div>
        <div className="text-sm font-medium">企微员工号协议接入</div>
        <div className="text-xs text-muted-foreground">
          先保存渠道，再到企微员工号实例页面绑定 guid、门店和知识库。
        </div>
      </div>
      <div className="space-y-2">
        <div className="text-xs font-medium text-muted-foreground">AgentDesk 回调地址</div>
        <div className="flex flex-col gap-2 sm:flex-row">
          <Input readOnly value={callbackUrl} className="font-mono text-xs" />
          <Button type="button" variant="outline" onClick={copyCallbackUrl} disabled={!callbackUrl}>
            <CopyIcon className="size-4" />
            复制
          </Button>
        </div>
      </div>
      <div className="rounded-md bg-muted px-3 py-3 text-xs leading-5 text-muted-foreground">
        平台 API 统一请求地址为 <span className="font-mono">/open/GuidRequest</span>，AgentDesk 会自动包装
        <span className="font-mono"> app_key/app_secret/path/data </span>
        后调用，例如 <span className="font-mono">/msg/send_text</span>、<span className="font-mono">/client/set_notify_url</span>。
      </div>
    </div>
  )
}

function WxWorkCLIAccessGuide({ channelId }: { channelId: string }) {
  const t = useI18n()
  const [origin, setOrigin] = useState("")

  useEffect(() => {
    setOrigin(window.location.origin)
  }, [])

  const inboundUrl = useMemo(() => {
    if (!origin) {
      return ""
    }
    return new URL("/api/third/wecom-cli/inbound", origin).toString()
  }, [origin])

  const outboxPollUrl = useMemo(() => {
    if (!origin) {
      return ""
    }
    return new URL("/api/third/wecom-cli/outbox/poll", origin).toString()
  }, [origin])

  const snippet = useMemo(() => {
    if (!origin || !channelId) {
      return ""
    }
    return `# bridge env
AGENT_DESK_BASE_URL=${origin}
AGENT_DESK_CHANNEL_ID=${channelId}
AGENT_DESK_BRIDGE_TOKEN=<复制上方Token>

# inbound
POST ${inboundUrl}

# outbound
POST ${outboxPollUrl}`
  }, [channelId, inboundUrl, origin, outboxPollUrl])

  async function copySnippet() {
    if (!snippet) {
      return
    }
    try {
      await navigator.clipboard.writeText(snippet)
      toast.success(t("channel.copiedSnippet"))
    } catch {
      toast.error(t("channel.copyFailed"))
    }
  }

  return (
    <div className="space-y-4 border-t pt-4">
      <div>
        <div className="text-sm font-medium">{t("channel.wxworkCliAccessInfo")}</div>
        <div className="text-xs text-muted-foreground">
          {channelId
            ? t("channel.wxworkCliAccessReady")
            : t("channel.wxworkCliAccessPending")}
        </div>
      </div>

      {!channelId ? (
        <div className="rounded-md bg-muted px-3 py-2 text-sm text-muted-foreground">
          {t("channel.newChannelPending")}
        </div>
      ) : (
        <div className="space-y-4">
          <div className="flex items-center justify-between gap-2">
            <div className="text-xs font-medium text-muted-foreground">
              {t("channel.embeddedSnippet")}
            </div>
            <Button type="button" variant="outline" size="sm" onClick={copySnippet}>
              <CopyIcon className="size-4" />
              {t("channel.copyCode")}
            </Button>
          </div>
          <pre className="max-h-48 overflow-auto rounded-md bg-muted p-3 text-xs leading-5">
            <code>{snippet}</code>
          </pre>
          <div className="flex flex-col gap-2 rounded-md bg-muted px-3 py-3 text-xs text-muted-foreground">
            <div className="font-medium text-foreground">{t("channel.accessGuide")}</div>
            <div>{t("channel.wxworkCliGuide1")}</div>
            <div>{t("channel.wxworkCliGuide2")}</div>
            <div>{t("channel.wxworkCliGuide3")}</div>
          </div>
        </div>
      )}
    </div>
  )
}

"use client"

import { useEffect, useMemo, useState } from "react"
import { zodResolver } from "@hookform/resolvers/zod"
import {
  Controller,
  type Control,
  type FieldErrors,
  type Resolver,
  type UseFormRegister,
  useForm,
  useWatch,
} from "react-hook-form"
import { z } from "zod/v4"
import {
  CopyIcon,
  ExternalLinkIcon,
  KeyRoundIcon,
  NetworkIcon,
} from "lucide-react"
import { toast } from "sonner"

import { useConfirm } from "@/components/confirm-provider"
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
import { useI18n } from "@/i18n/provider"
import {
  fetchAIAgentsAll,
  fetchChannel,
  resetChannelUserTokenSecret,
  type AIAgent,
  type AdminChannel,
  type CreateAdminChannelPayload,
} from "@/lib/api/admin"
import { Status } from "@/lib/generated/enums"

type ChannelType = "web" | "wechat_mp" | "wxwork_protocol"
type Translate = (key: string, values?: Record<string, string | number>) => string

type WebChannelConfig = {
  title?: string
  subtitle?: string
  themeColor?: string
  position?: "left" | "right"
  width?: string
  userTokenSecret?: string
}

type WxWorkProtocolChannelConfig = {
  appKey?: string
  appSecret?: string
  baseUrl?: string
  devicePoolUrl?: string
  callbackToken?: string
  wecdnBaseUrl?: string
  publicAssetBaseUrl?: string
}

type ChannelForm = {
  channelType: ChannelType
  aiAgentId: string
  name: string
  widgetTitle: string
  widgetSubtitle: string
  widgetThemeColor: string
  widgetPosition: "left" | "right"
  widgetWidth: string
  userTokenSecret: string
  protocolAppKey: string
  protocolAppSecret: string
  protocolBaseUrl: string
  protocolDevicePoolUrl: string
  protocolCallbackToken: string
  protocolWecdnBaseUrl: string
  protocolPublicAssetBaseUrl: string
  remark: string
}

type ChannelEditDialogProps = {
  open: boolean
  saving: boolean
  itemId: number | null
  canResetSecret: boolean
  onOpenChange: (open: boolean) => void
  onSubmit: (payload: CreateAdminChannelPayload) => Promise<void>
}

const defaultProtocolBaseUrl = "https://chat-api.juhebot.com/open/GuidRequest"

function emptyForm(t: Translate): ChannelForm {
  return {
    channelType: "web",
    aiAgentId: "",
    name: "",
    widgetTitle: t("channel.defaultTitleWeb"),
    widgetSubtitle: t("channel.defaultSubtitle"),
    widgetThemeColor: "#2563eb",
    widgetPosition: "right",
    widgetWidth: "380px",
    userTokenSecret: "",
    protocolAppKey: "",
    protocolAppSecret: "",
    protocolBaseUrl: defaultProtocolBaseUrl,
    protocolDevicePoolUrl: "",
    protocolCallbackToken: "",
    protocolWecdnBaseUrl: "",
    protocolPublicAssetBaseUrl: "",
    remark: "",
  }
}

function readJSON<T>(raw: string): Partial<T> {
  if (!raw.trim()) return {}
  try {
    return JSON.parse(raw) as Partial<T>
  } catch {
    return {}
  }
}

function formFromChannel(item: AdminChannel, t: Translate): ChannelForm {
  const next = emptyForm(t)
  const channelType = (
    ["web", "wechat_mp", "wxwork_protocol"].includes(item.channelType)
      ? item.channelType
      : "web"
  ) as ChannelType
  next.channelType = channelType
  next.aiAgentId = String(item.aiAgentId || "")
  next.name = item.name || ""
  next.remark = item.remark || ""

  if (channelType === "wxwork_protocol") {
    const config = readJSON<WxWorkProtocolChannelConfig>(item.configJson)
    next.protocolAppKey = config.appKey?.trim() || ""
    next.protocolAppSecret = config.appSecret?.trim() || ""
    next.protocolBaseUrl = config.baseUrl?.trim() || defaultProtocolBaseUrl
    next.protocolDevicePoolUrl = config.devicePoolUrl?.trim() || ""
    next.protocolCallbackToken = config.callbackToken?.trim() || ""
    next.protocolWecdnBaseUrl = config.wecdnBaseUrl?.trim() || ""
    next.protocolPublicAssetBaseUrl = config.publicAssetBaseUrl?.trim() || ""
    return next
  }

  const config = readJSON<WebChannelConfig>(item.configJson)
  next.widgetTitle = config.title?.trim() || next.widgetTitle
  next.widgetSubtitle = config.subtitle?.trim() || next.widgetSubtitle
  next.widgetThemeColor = config.themeColor?.trim() || next.widgetThemeColor
  next.widgetPosition = config.position === "left" ? "left" : "right"
  next.widgetWidth = config.width?.trim() || next.widgetWidth
  next.userTokenSecret = config.userTokenSecret?.trim() || ""
  return next
}

function payloadFromForm(
  values: ChannelForm,
  current: AdminChannel | null
): CreateAdminChannelPayload {
  let config: WebChannelConfig | WxWorkProtocolChannelConfig
  if (values.channelType === "wxwork_protocol") {
    config = {
      appKey: values.protocolAppKey.trim(),
      appSecret: values.protocolAppSecret.trim(),
      baseUrl: values.protocolBaseUrl.trim(),
      devicePoolUrl: values.protocolDevicePoolUrl.trim(),
      callbackToken: values.protocolCallbackToken.trim(),
      wecdnBaseUrl: values.protocolWecdnBaseUrl.trim(),
      publicAssetBaseUrl: values.protocolPublicAssetBaseUrl.trim(),
    }
  } else {
    config = {
      title: values.widgetTitle.trim(),
      subtitle: values.widgetSubtitle.trim(),
      themeColor: values.widgetThemeColor.trim(),
      userTokenSecret: values.userTokenSecret.trim(),
      ...(values.channelType === "web"
        ? {
            position: values.widgetPosition,
            width: values.widgetWidth.trim(),
          }
        : {}),
    }
  }

  return {
    channelType: values.channelType,
    aiAgentId: Number(values.aiAgentId),
    name: values.name.trim(),
    configJson: JSON.stringify(config),
    status: current?.status ?? Status.Ok,
    remark: values.remark.trim(),
  }
}

function createSchema(t: Translate) {
  return z
    .object({
      channelType: z.enum(["web", "wechat_mp", "wxwork_protocol"]),
      aiAgentId: z.string().trim().regex(/^\d+$/, t("channel.agentRequired")),
      name: z.string().trim().min(1, t("channel.nameRequired")).max(100),
      widgetTitle: z.string().trim().max(100),
      widgetSubtitle: z.string().trim().max(200),
      widgetThemeColor: z
        .string()
        .trim()
        .regex(/^#[0-9a-fA-F]{6}$/, t("channel.colorInvalid")),
      widgetPosition: z.enum(["left", "right"]),
      widgetWidth: z.string().trim().max(32),
      userTokenSecret: z.string().trim(),
      protocolAppKey: z.string().trim(),
      protocolAppSecret: z.string().trim(),
      protocolBaseUrl: z.string().trim(),
      protocolDevicePoolUrl: z.string().trim(),
      protocolCallbackToken: z.string().trim(),
      protocolWecdnBaseUrl: z.string().trim(),
      protocolPublicAssetBaseUrl: z.string().trim(),
      remark: z.string().trim().max(500),
    })
    .superRefine((values, ctx) => {
      if (values.channelType === "wxwork_protocol") {
        if (!values.protocolAppKey) {
          ctx.addIssue({
            code: "custom",
            path: ["protocolAppKey"],
            message: t("channel.protocolAppKeyRequired"),
          })
        }
        if (!values.protocolAppSecret) {
          ctx.addIssue({
            code: "custom",
            path: ["protocolAppSecret"],
            message: t("channel.protocolAppSecretRequired"),
          })
        }
        if (!values.protocolBaseUrl) {
          ctx.addIssue({
            code: "custom",
            path: ["protocolBaseUrl"],
            message: t("channel.protocolBaseUrlRequired"),
          })
        }
      }
      if (values.channelType === "web" && !values.widgetWidth) {
        ctx.addIssue({
          code: "custom",
          path: ["widgetWidth"],
          message: t("channel.widgetWidthRequired"),
        })
      }
    })
}

export function ChannelEditDialog(props: ChannelEditDialogProps) {
  if (!props.open) return null
  return <ChannelEditDialogBody key={props.itemId ?? "create"} {...props} />
}

function ChannelEditDialogBody({
  open,
  saving,
  itemId,
  canResetSecret,
  onOpenChange,
  onSubmit,
}: ChannelEditDialogProps) {
  const t = useI18n()
  const confirm = useConfirm()
  const [loading, setLoading] = useState(true)
  const [agents, setAgents] = useState<AIAgent[]>([])
  const [channel, setChannel] = useState<AdminChannel | null>(null)
  const schema = useMemo(() => createSchema(t), [t])
  const resolver = useMemo(
    () => zodResolver(schema as never) as Resolver<ChannelForm>,
    [schema]
  )
  const form = useForm<ChannelForm>({
    resolver,
    defaultValues: emptyForm(t),
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
  const themeColor = useWatch({ control, name: "widgetThemeColor" })
  const userTokenSecret = useWatch({ control, name: "userTokenSecret" })

  useEffect(() => {
    let cancelled = false
    Promise.all([
      fetchAIAgentsAll({ status: Status.Ok }),
      itemId ? fetchChannel(itemId) : Promise.resolve(null),
    ])
      .then(([agentList, item]) => {
        if (cancelled) return
        setAgents(agentList)
        setChannel(item)
        reset(item ? formFromChannel(item, t) : emptyForm(t))
      })
      .catch((error) => {
        if (!cancelled) {
          toast.error(
            error instanceof Error ? error.message : t("channel.loadDetailFailed")
          )
        }
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })

    return () => {
      cancelled = true
    }
  }, [itemId, reset, t])

  async function copyText(value: string, successMessage: string) {
    if (!value) return
    try {
      await navigator.clipboard.writeText(value)
      toast.success(successMessage)
    } catch {
      toast.error(t("channel.copyFailed"))
    }
  }

  async function resetSecret() {
    if (!itemId || !canResetSecret) return
    const accepted = await confirm({
      title: t("channel.resetSecretTitle"),
      description: t("channel.resetSecretConfirm"),
      confirmText: t("channel.reset"),
    })
    if (!accepted) return
    try {
      const result = await resetChannelUserTokenSecret(itemId)
      setValue("userTokenSecret", result.userTokenSecret, { shouldDirty: true })
      toast.success(t("channel.resetSecretSuccess"))
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : t("channel.resetSecretFailed")
      )
    }
  }

  async function submit(values: ChannelForm) {
    await onSubmit(payloadFromForm(values, channel))
  }

  const creating = !itemId
  const formId = "channel-settings-form"
  const channelTypeOptions = [
    { value: "web", label: t("channel.typeWeb") },
    { value: "wechat_mp", label: t("channel.typeWechatMp") },
    { value: "wxwork_protocol", label: t("channel.typeWxworkProtocol") },
  ]
  const agentOptions = agents.map((agent) => ({
    value: String(agent.id),
    label: agent.name,
  }))

  return (
    <ProjectDialog
      open={open}
      onOpenChange={onOpenChange}
      title={t(creating ? "channel.createTitle" : "channel.editTitle")}
      size="xl"
      allowFullscreen
      footer={
        <>
          <Button
            type="button"
            variant="outline"
            onClick={() => onOpenChange(false)}
            disabled={saving}
          >
            {t("common.cancel")}
          </Button>
          <Button type="submit" form={formId} disabled={saving || loading}>
            {saving ? t("channel.saving") : t("channel.save")}
          </Button>
        </>
      }
    >
      {loading ? (
        <div className="flex min-h-48 items-center justify-center text-sm text-muted-foreground">
          {t("channel.loadingDetail")}
        </div>
      ) : (
        <form
          id={formId}
          className="space-y-6"
          onSubmit={handleSubmit(submit)}
        >
          <div className="grid gap-4 md:grid-cols-2">
            <Field data-invalid={!!errors.channelType}>
              <FieldLabel>{t("channel.channelType")}</FieldLabel>
              <FieldContent>
                <Controller
                  name="channelType"
                  control={control}
                  render={({ field }) => (
                    <OptionCombobox
                      value={field.value}
                      options={channelTypeOptions}
                      placeholder={t("channel.selectChannelType")}
                      searchPlaceholder={t("channel.searchChannelType")}
                      emptyText={t("channel.emptyChannelType")}
                      onChange={field.onChange}
                      disabled={!creating}
                    />
                  )}
                />
                <FieldError errors={[errors.channelType]} />
              </FieldContent>
            </Field>

            <Field data-invalid={!!errors.aiAgentId}>
              <FieldLabel>{t("channel.columnAgent")}</FieldLabel>
              <FieldContent>
                <Controller
                  name="aiAgentId"
                  control={control}
                  render={({ field }) => (
                    <OptionCombobox
                      value={field.value}
                      options={agentOptions}
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

            <Field className="md:col-span-2" data-invalid={!!errors.name}>
              <FieldLabel htmlFor="channel-name">{t("channel.name")}</FieldLabel>
              <FieldContent>
                <Input id="channel-name" {...register("name")} />
                <FieldError errors={[errors.name]} />
              </FieldContent>
            </Field>
          </div>

          {channelType === "wxwork_protocol" ? (
            <ProtocolFields register={register} errors={errors} />
          ) : (
            <WebFields
              channelType={channelType}
              channelId={channel?.channelId || ""}
              control={control}
              errors={errors}
              register={register}
              themeColor={themeColor}
              userTokenSecret={userTokenSecret}
              canResetSecret={canResetSecret}
              onColorChange={(value) =>
                setValue("widgetThemeColor", value, {
                  shouldDirty: true,
                  shouldValidate: true,
                })
              }
              onCopy={(value) =>
                copyText(value, t("channel.copySecretSuccess"))
              }
              onReset={() => void resetSecret()}
              onCopyLink={(value) =>
                copyText(value, t("channel.copiedAccessLink"))
              }
            />
          )}

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

type Register = UseFormRegister<ChannelForm>
type FormErrors = FieldErrors<ChannelForm>

function WebFields({
  channelType,
  channelId,
  control,
  errors,
  register,
  themeColor,
  userTokenSecret,
  canResetSecret,
  onColorChange,
  onCopy,
  onReset,
  onCopyLink,
}: {
  channelType: "web" | "wechat_mp"
  channelId: string
  control: Control<ChannelForm>
  errors: FormErrors
  register: Register
  themeColor: string
  userTokenSecret: string
  canResetSecret: boolean
  onColorChange: (value: string) => void
  onCopy: (value: string) => void
  onReset: () => void
  onCopyLink: (value: string) => void
}) {
  const t = useI18n()
  const origin = typeof window === "undefined" ? "" : window.location.origin
  const accessUrl = useMemo(() => {
    if (!origin || !channelId) return ""
    const url = new URL("/support/chat/", origin)
    url.searchParams.set("channelId", channelId)
    return url.toString()
  }, [channelId, origin])

  return (
    <div className="space-y-5 border-t pt-5">
      <div className="grid gap-4 md:grid-cols-2">
        <Field data-invalid={!!errors.widgetTitle}>
          <FieldLabel htmlFor="channel-widget-title">
            {t("channel.widgetTitle")}
          </FieldLabel>
          <FieldContent>
            <Input id="channel-widget-title" {...register("widgetTitle")} />
            <FieldError errors={[errors.widgetTitle]} />
          </FieldContent>
        </Field>

        <Field data-invalid={!!errors.widgetSubtitle}>
          <FieldLabel htmlFor="channel-widget-subtitle">
            {t("channel.widgetSubtitle")}
          </FieldLabel>
          <FieldContent>
            <Input id="channel-widget-subtitle" {...register("widgetSubtitle")} />
            <FieldError errors={[errors.widgetSubtitle]} />
          </FieldContent>
        </Field>

        <Field data-invalid={!!errors.widgetThemeColor}>
          <FieldLabel htmlFor="channel-theme-color">
            {t("channel.themeColor")}
          </FieldLabel>
          <FieldContent>
            <div className="flex items-center gap-2">
              <Input
                id="channel-theme-color-picker"
                type="color"
                className="size-9 shrink-0 cursor-pointer p-1"
                value={/^#[0-9a-fA-F]{6}$/.test(themeColor) ? themeColor : "#2563eb"}
                onChange={(event) => onColorChange(event.target.value)}
                aria-label={t("channel.themeColor")}
              />
              <Input id="channel-theme-color" {...register("widgetThemeColor")} />
            </div>
            <FieldError errors={[errors.widgetThemeColor]} />
          </FieldContent>
        </Field>

        {channelType === "web" ? (
          <Field data-invalid={!!errors.widgetPosition}>
            <FieldLabel>{t("channel.mountPosition")}</FieldLabel>
            <FieldContent>
              <Controller
                name="widgetPosition"
                control={control}
                render={({ field }) => (
                  <OptionCombobox
                    value={field.value}
                    options={[
                      { value: "right", label: t("channel.positionRight") },
                      { value: "left", label: t("channel.positionLeft") },
                    ]}
                    placeholder={t("channel.selectMountPosition")}
                    onChange={field.onChange}
                  />
                )}
              />
              <FieldError errors={[errors.widgetPosition]} />
            </FieldContent>
          </Field>
        ) : null}

        {channelType === "web" ? (
          <Field data-invalid={!!errors.widgetWidth}>
            <FieldLabel htmlFor="channel-widget-width">
              {t("channel.widgetWidth")}
            </FieldLabel>
            <FieldContent>
              <Input id="channel-widget-width" {...register("widgetWidth")} />
              <FieldError errors={[errors.widgetWidth]} />
            </FieldContent>
          </Field>
        ) : null}
      </div>

      {channelId ? (
        <div className="grid gap-4 border-t pt-5 md:grid-cols-2">
          <Field className="md:col-span-2">
            <FieldLabel>{t("channel.directAccessUrl")}</FieldLabel>
            <FieldContent>
              <div className="flex gap-2">
                <Input readOnly value={accessUrl} className="font-mono text-xs" />
                <Button
                  type="button"
                  variant="outline"
                  size="icon"
                  title={t("channel.copyLink")}
                  onClick={() => onCopyLink(accessUrl)}
                >
                  <CopyIcon />
                </Button>
                <Button
                  type="button"
                  variant="outline"
                  size="icon"
                  title={t("channel.openLink")}
                  onClick={() => window.open(accessUrl, "_blank", "noopener,noreferrer")}
                >
                  <ExternalLinkIcon />
                </Button>
              </div>
            </FieldContent>
          </Field>

          <Field className="md:col-span-2">
            <FieldLabel>{t("channel.userJwtSecret")}</FieldLabel>
            <FieldContent>
              <div className="flex gap-2">
                <Input
                  readOnly
                  value={userTokenSecret}
                  className="font-mono text-xs"
                />
                <Button
                  type="button"
                  variant="outline"
                  size="icon"
                  title={t("channel.copy")}
                  onClick={() => onCopy(userTokenSecret)}
                  disabled={!userTokenSecret}
                >
                  <CopyIcon />
                </Button>
                {canResetSecret ? (
                  <Button
                    type="button"
                    variant="outline"
                    size="icon"
                    title={t("channel.reset")}
                    onClick={onReset}
                  >
                    <KeyRoundIcon />
                  </Button>
                ) : null}
              </div>
            </FieldContent>
          </Field>
        </div>
      ) : null}
    </div>
  )
}

function ProtocolFields({
  register,
  errors,
}: {
  register: Register
  errors: FormErrors
}) {
  const t = useI18n()
  return (
    <div className="space-y-5 border-t pt-5">
      <div className="flex items-center gap-2 text-sm font-medium">
        <NetworkIcon className="size-4 text-primary" />
        {t("channel.protocolSection")}
      </div>
      <div className="grid gap-4 md:grid-cols-2">
        <Field data-invalid={!!errors.protocolAppKey}>
          <FieldLabel htmlFor="channel-protocol-app-key">
            {t("channel.protocolAppKey")}
          </FieldLabel>
          <FieldContent>
            <Input id="channel-protocol-app-key" {...register("protocolAppKey")} />
            <FieldError errors={[errors.protocolAppKey]} />
          </FieldContent>
        </Field>

        <Field data-invalid={!!errors.protocolAppSecret}>
          <FieldLabel htmlFor="channel-protocol-app-secret">
            {t("channel.protocolAppSecret")}
          </FieldLabel>
          <FieldContent>
            <Input
              id="channel-protocol-app-secret"
              type="password"
              autoComplete="new-password"
              {...register("protocolAppSecret")}
            />
            <FieldError errors={[errors.protocolAppSecret]} />
          </FieldContent>
        </Field>

        <Field className="md:col-span-2" data-invalid={!!errors.protocolBaseUrl}>
          <FieldLabel htmlFor="channel-protocol-base-url">
            {t("channel.protocolBaseUrl")}
          </FieldLabel>
          <FieldContent>
            <Input
              id="channel-protocol-base-url"
              className="font-mono text-xs"
              {...register("protocolBaseUrl")}
            />
            <FieldError errors={[errors.protocolBaseUrl]} />
          </FieldContent>
        </Field>

        <Field data-invalid={!!errors.protocolDevicePoolUrl}>
          <FieldLabel htmlFor="channel-protocol-device-pool-url">
            {t("channel.protocolDevicePoolUrl")}
          </FieldLabel>
          <FieldContent>
            <Input
              id="channel-protocol-device-pool-url"
              className="font-mono text-xs"
              {...register("protocolDevicePoolUrl")}
            />
            <FieldError errors={[errors.protocolDevicePoolUrl]} />
          </FieldContent>
        </Field>

        <Field data-invalid={!!errors.protocolWecdnBaseUrl}>
          <FieldLabel htmlFor="channel-protocol-wecdn-url">
            {t("channel.protocolWecdnBaseUrl")}
          </FieldLabel>
          <FieldContent>
            <Input
              id="channel-protocol-wecdn-url"
              className="font-mono text-xs"
              {...register("protocolWecdnBaseUrl")}
            />
            <FieldError errors={[errors.protocolWecdnBaseUrl]} />
          </FieldContent>
        </Field>

        <Field className="md:col-span-2" data-invalid={!!errors.protocolPublicAssetBaseUrl}>
          <FieldLabel htmlFor="channel-protocol-public-asset-url">
            {t("channel.protocolPublicAssetBaseUrl")}
          </FieldLabel>
          <FieldContent>
            <Input
              id="channel-protocol-public-asset-url"
              className="font-mono text-xs"
              {...register("protocolPublicAssetBaseUrl")}
            />
            <FieldError errors={[errors.protocolPublicAssetBaseUrl]} />
          </FieldContent>
        </Field>

        <Field className="md:col-span-2" data-invalid={!!errors.protocolCallbackToken}>
          <FieldLabel htmlFor="channel-protocol-callback-token">
            {t("channel.protocolCallbackToken")}
          </FieldLabel>
          <FieldContent>
            <Input
              id="channel-protocol-callback-token"
              readOnly
              className="font-mono text-xs"
              {...register("protocolCallbackToken")}
            />
            <FieldError errors={[errors.protocolCallbackToken]} />
          </FieldContent>
        </Field>
      </div>
    </div>
  )
}

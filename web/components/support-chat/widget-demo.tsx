"use client"

import { SignJWT } from "jose"
import { CheckIcon, CopyIcon } from "lucide-react"
import { useEffect, useMemo, useState } from "react"

import type { AgentDeskConfig } from "@/lib/sdk/config-types"
import { useI18n } from "@/i18n/provider"

const STORAGE_KEY = "agent-desk-web-widget-test-config"
const DEFAULT_JWT_TTL_MINUTES = "30"
const INITIAL_CONFIG: AgentDeskConfig = {
  channelId: "",
  baseUrl: "",
  apiBaseUrl: "",
}

type AuthMode = "guest" | "jwt"

type WidgetDemoConfig = AgentDeskConfig & {
  authMode?: AuthMode
  jwtSecret?: string
  jwtUserId?: string
  jwtName?: string
  jwtTtlMinutes?: string
}

function getDefaultConfig(defaultName: string): WidgetDemoConfig {
  if (typeof window === "undefined") {
    return INITIAL_CONFIG
  }

  const savedText = window.localStorage.getItem(STORAGE_KEY)
  const savedConfig = savedText
    ? (JSON.parse(savedText) as Partial<WidgetDemoConfig>)
    : {}
  const query = new URLSearchParams(window.location.search)

  return {
    channelId: query.get("channelId") ?? savedConfig.channelId ?? "",
    baseUrl: "",
    apiBaseUrl: "",
    authMode: (query.get("authMode") as AuthMode | null) ?? savedConfig.authMode ?? "guest",
    jwtSecret: savedConfig.jwtSecret ?? "",
    jwtUserId: query.get("userId") ?? savedConfig.jwtUserId ?? "demo-user-001",
    jwtName: query.get("name") ?? savedConfig.jwtName ?? defaultName,
    jwtTtlMinutes: savedConfig.jwtTtlMinutes ?? DEFAULT_JWT_TTL_MINUTES,
  }
}

function removeMountedWidget() {
  if (typeof window === "undefined") {
    return
  }

  window.AgentDeskWidget?.destroy()
  document
    .querySelectorAll(
      '[data-agent-desk-widget="launcher"], [data-agent-desk-widget="frame"], [data-agent-desk-widget="script"]'
    )
    .forEach((node) => node.remove())

  delete window.AgentDeskConfig
  delete window.__CS_AI_AGENT_WIDGET_CONFIG__
  delete window.__CS_AI_AGENT_WIDGET_STATE__
  delete window.AgentDeskWidget
}

function injectWidget(config: AgentDeskConfig) {
  removeMountedWidget()
  window.AgentDeskConfig = config

  const script = document.createElement("script")
  script.async = true
  script.src = `${window.location.origin}/sdk/agent-desk-sdk.min.js`
  script.dataset.agentDeskWidget = "script"
  document.body.appendChild(script)
}

function buildWidgetConfig(config: WidgetDemoConfig): AgentDeskConfig {
  const nextConfig: AgentDeskConfig = {
    channelId: config.channelId.trim(),
    baseUrl: "",
    apiBaseUrl: "",
  }
  if (config.authMode === "jwt") {
    nextConfig.getUserToken = undefined
  }
  return nextConfig
}

async function signUserToken(config: WidgetDemoConfig, t: (key: string) => string) {
  const userId = (config.jwtUserId || "").trim()
  const name = (config.jwtName || "").trim()
  const secret = (config.jwtSecret || "").trim()
  const ttl = Number(config.jwtTtlMinutes || DEFAULT_JWT_TTL_MINUTES)

  if (!userId) {
    throw new Error(t("widgetDemo.missingUserId"))
  }
  if (!name) {
    throw new Error(t("widgetDemo.missingName"))
  }
  if (!secret) {
    throw new Error(t("widgetDemo.missingSecret"))
  }
  if (!Number.isFinite(ttl) || ttl <= 0) {
    throw new Error(t("widgetDemo.invalidTtl"))
  }

  return new SignJWT({ userId, name })
    .setProtectedHeader({ alg: "HS256", typ: "JWT" })
    .setIssuedAt()
    .setExpirationTime(`${ttl}m`)
    .sign(new TextEncoder().encode(secret))
}

export function SupportWidgetDemo() {
  const t = useI18n()
  const [config, setConfig] = useState<WidgetDemoConfig>({
    ...INITIAL_CONFIG,
    authMode: "guest",
    jwtSecret: "",
    jwtUserId: "demo-user-001",
    jwtName: t("widgetDemo.defaultName"),
    jwtTtlMinutes: DEFAULT_JWT_TTL_MINUTES,
  })
  const [status, setStatus] = useState(t("widgetDemo.missingChannel"))
  const [origin, setOrigin] = useState("")
  const [generatedToken, setGeneratedToken] = useState("")
  const [latestDirectChatUrl, setLatestDirectChatUrl] = useState("")
  const [copied, setCopied] = useState(false)
  const [snippetCopied, setSnippetCopied] = useState(false)

  async function mountWidget(configToMount: WidgetDemoConfig) {
    const cleanConfig = {
      ...configToMount,
      channelId: configToMount.channelId.trim(),
      baseUrl: "",
      apiBaseUrl: "",
      getUserToken: undefined,
    }
    const nextConfig = buildWidgetConfig(cleanConfig)
    if (cleanConfig.authMode === "jwt") {
      nextConfig.getUserToken = () => signUserToken(cleanConfig, t)
    }
    setConfig(cleanConfig)
    setGeneratedToken("")
    setLatestDirectChatUrl("")
    window.localStorage.setItem(STORAGE_KEY, JSON.stringify(cleanConfig))

    if (!nextConfig.channelId) {
      removeMountedWidget()
      setStatus(t("widgetDemo.missingChannel"))
      return
    }

    injectWidget(nextConfig)
    setStatus(
      cleanConfig.authMode === "jwt"
        ? t("widgetDemo.mountedJwt")
        : t("widgetDemo.mountedGuest")
    )
  }

  useEffect(() => {
    const timer = window.setTimeout(() => {
      const initialConfig = getDefaultConfig(t("widgetDemo.defaultName"))
      setOrigin(window.location.origin)
      setConfig(initialConfig)
      setStatus(initialConfig.channelId ? t("widgetDemo.mounted") : t("widgetDemo.missingChannel"))

      if (initialConfig.channelId) {
        void mountWidget(initialConfig).catch((error) => {
          removeMountedWidget()
          setGeneratedToken("")
          setStatus(error instanceof Error ? error.message : t("widgetDemo.mountFailed"))
        })
      }
    }, 0)

    return () => {
      window.clearTimeout(timer)
      removeMountedWidget()
    }
  }, [t])

  const snippet = useMemo(() => {
    const scriptSrc = origin
      ? `${origin}/sdk/agent-desk-sdk.min.js`
      : "/sdk/agent-desk-sdk.min.js"

    const configLines = [`    channelId: "${config.channelId || ""}"`]
    if (config.authMode === "jwt") {
      configLines.push(`    async getUserToken() {
      const res = await fetch("/api/support/user-token", { credentials: "include" });
      const data = await res.json();
      return data.userToken;
    }`)
    }

    return `<script>
  window.AgentDeskConfig = {
${configLines.join(",\n")}
  };
</script>
<script async src="${scriptSrc}"></script>`
  }, [config.authMode, config.channelId, origin])

  function updateField<K extends keyof WidgetDemoConfig>(
    key: K,
    value: WidgetDemoConfig[K]
  ) {
    setConfig((current) => ({ ...current, [key]: value }))
  }

  async function handleMount() {
    try {
      await mountWidget(config)
    } catch (error) {
      removeMountedWidget()
      setGeneratedToken("")
      setStatus(error instanceof Error ? error.message : t("widgetDemo.mountFailed"))
    }
  }

  async function handleCopyDirectUrl() {
    if (!window.AgentDeskWidget || typeof navigator === "undefined") {
      return
    }
    try {
      const url = await window.AgentDeskWidget.getChatUrl()
      setLatestDirectChatUrl(url)
      if (config.authMode === "jwt") {
        setGeneratedToken(new URL(url).searchParams.get("userToken") || "")
      }
      await navigator.clipboard.writeText(url)
      setCopied(true)
      window.setTimeout(() => setCopied(false), 1600)
    } catch (error) {
      setStatus(error instanceof Error ? error.message : t("widgetDemo.linkFailed"))
    }
  }

  async function handleOpenDirectChat() {
    if (!window.AgentDeskWidget) {
      return
    }
    try {
      const url = await window.AgentDeskWidget.getChatUrl()
      setLatestDirectChatUrl(url)
      if (config.authMode === "jwt") {
        setGeneratedToken(new URL(url).searchParams.get("userToken") || "")
      }
      window.open(url, "_blank", "noopener,noreferrer")
    } catch (error) {
      setStatus(error instanceof Error ? error.message : t("widgetDemo.linkFailed"))
    }
  }

  async function handleCopySnippet() {
    if (typeof navigator === "undefined") {
      return
    }
    await navigator.clipboard.writeText(snippet)
    setSnippetCopied(true)
    window.setTimeout(() => setSnippetCopied(false), 1600)
  }

  return (
    <main className="min-h-svh bg-[#f4f8fd] px-6 py-8 text-foreground">
      <div className="mx-auto grid max-w-6xl gap-6 lg:grid-cols-[360px_minmax(0,1fr)]">
        <section className="agentdesk-surface rounded-2xl p-5">
          <div className="text-base font-semibold">{t("widgetDemo.title")}</div>
          <div className="mt-1 text-sm text-muted-foreground">{status}</div>

          <div className="mt-5 grid gap-3">
            <TextField
              label="channelId"
              value={config.channelId}
              onChange={(value) => updateField("channelId", value)}
            />
            <SegmentedControl
              label={t("widgetDemo.authMode")}
              value={config.authMode || "guest"}
              onChange={(value) => updateField("authMode", value)}
              options={[
                { label: t("widgetDemo.guest"), value: "guest" },
                { label: t("widgetDemo.jwtUser"), value: "jwt" },
              ]}
            />
            {config.authMode === "jwt" ? (
              <div className="agentdesk-subtle-surface grid gap-3 rounded-xl p-3">
                <TextField
                  label="userId"
                  value={config.jwtUserId}
                  onChange={(value) => updateField("jwtUserId", value)}
                />
                <TextField
                  label="name"
                  value={config.jwtName}
                  onChange={(value) => updateField("jwtName", value)}
                />
                <TextField
                  label="JWT Secret"
                  value={config.jwtSecret}
                  onChange={(value) => updateField("jwtSecret", value)}
                  type="password"
                />
                <TextField
                  label={t("widgetDemo.ttlMinutes")}
                  value={config.jwtTtlMinutes}
                  onChange={(value) => updateField("jwtTtlMinutes", value)}
                  type="number"
                />
              </div>
            ) : null}
          </div>

          <div className="mt-5 flex gap-2">
            <button
              type="button"
              onClick={() => void handleMount()}
              className="rounded-lg bg-primary px-4 py-2 text-sm font-medium text-primary-foreground shadow-[0_9px_18px_rgba(37,99,235,0.18)]"
            >
              {t("widgetDemo.mount")}
            </button>
            <button
              type="button"
              onClick={() => {
                removeMountedWidget()
                setStatus(t("widgetDemo.unmounted"))
              }}
              className="agentdesk-soft-button rounded-lg px-4 py-2 text-sm font-medium"
            >
              {t("widgetDemo.unmount")}
            </button>
          </div>
        </section>

        <section className="agentdesk-surface rounded-2xl p-5">
          <div className="text-base font-semibold">{t("widgetDemo.snippetTitle")}</div>
          {config.authMode === "jwt" ? (
            <div className="mt-2 rounded-xl border border-amber-200 bg-amber-50 px-3 py-2 text-sm text-amber-800 shadow-[0_8px_18px_rgba(245,158,11,0.08)]">
              {t("widgetDemo.jwtNotice")}
            </div>
          ) : null}
          <div className="relative mt-4">
            <button
              type="button"
              onClick={() => void handleCopySnippet()}
              className="absolute right-2 top-2 inline-flex size-8 items-center justify-center rounded-lg border border-white/10 bg-white/10 text-blue-100 transition hover:bg-white/20 hover:text-white"
              aria-label={snippetCopied ? t("widgetDemo.copiedSnippet") : t("widgetDemo.copySnippet")}
              title={snippetCopied ? t("widgetDemo.copied") : t("widgetDemo.copyCode")}
            >
              {snippetCopied ? (
                <CheckIcon className="size-4" />
              ) : (
                <CopyIcon className="size-4" />
              )}
            </button>
            <pre className="overflow-x-auto rounded-2xl border border-[#dbe7f6] bg-[#0f1f3d] p-4 pr-12 text-xs leading-5 text-blue-50 shadow-[0_16px_34px_rgba(37,99,235,0.12)]">
              <code>{snippet}</code>
            </pre>
          </div>
          <div className="mt-5">
            <div className="text-sm font-medium text-foreground">{t("widgetDemo.directChat")}</div>
            <div className="mt-2 flex flex-col gap-2 sm:flex-row">
              <input
                readOnly
                value={latestDirectChatUrl || t("widgetDemo.directChatPlaceholder")}
                className="h-9 min-w-0 flex-1 rounded-xl border border-[#dbe7f6] bg-white px-3 font-mono text-xs outline-none transition focus:border-primary/50 focus:ring-2 focus:ring-primary/10"
              />
              <div className="flex gap-2">
                <button
                  type="button"
                  disabled={!config.channelId}
                  onClick={() => void handleCopyDirectUrl()}
                  className="agentdesk-soft-button rounded-lg px-3 py-2 text-sm font-medium disabled:cursor-not-allowed disabled:opacity-50"
                >
                  {copied ? t("widgetDemo.copied") : t("widgetDemo.copy")}
                </button>
                <button
                  type="button"
                  disabled={!config.channelId}
                  onClick={() => void handleOpenDirectChat()}
                  className="rounded-lg bg-primary px-3 py-2 text-sm font-medium text-primary-foreground disabled:cursor-not-allowed disabled:opacity-50"
                >
                  {t("widgetDemo.openNewWindow")}
                </button>
              </div>
            </div>
          </div>
          {generatedToken ? (
            <div className="mt-4">
              <div className="text-sm font-medium text-foreground">{t("widgetDemo.currentToken")}</div>
              <textarea
                readOnly
                value={generatedToken}
                className="mt-2 h-28 w-full resize-none rounded-xl border border-[#dbe7f6] bg-white p-3 font-mono text-xs outline-none transition focus:border-primary/50 focus:ring-2 focus:ring-primary/10"
              />
            </div>
          ) : null}
        </section>
      </div>
    </main>
  )
}

function TextField({
  label,
  value,
  onChange,
  type = "text",
}: {
  label: string
  value?: string
  onChange: (value: string) => void
  type?: string
}) {
  return (
    <label className="grid gap-1.5 text-sm">
      <span className="font-medium text-foreground">{label}</span>
      <input
        type={type}
        value={value || ""}
        onChange={(event) => onChange(event.target.value)}
        className="h-9 rounded-xl border border-[#dbe7f6] bg-white px-3 text-sm outline-none transition focus:border-primary/50 focus:ring-2 focus:ring-primary/10"
      />
    </label>
  )
}

function SegmentedControl<T extends string>({
  label,
  value,
  options,
  onChange,
}: {
  label: string
  value: T
  options: Array<{ label: string; value: T }>
  onChange: (value: T) => void
}) {
  return (
    <div className="grid gap-1.5 text-sm">
      <div className="font-medium text-foreground">{label}</div>
      <div className="grid grid-cols-2 rounded-xl border border-[#dbe7f6] bg-[#f6f9ff] p-1 shadow-inner shadow-blue-100/40">
        {options.map((option) => (
          <button
            key={option.value}
            type="button"
            onClick={() => onChange(option.value)}
            className={
              option.value === value
                ? "rounded-lg bg-white px-3 py-1.5 text-sm font-medium text-primary shadow-[0_4px_12px_rgba(37,99,235,0.10)]"
                : "rounded-lg px-3 py-1.5 text-sm text-muted-foreground transition hover:bg-white/70 hover:text-primary"
            }
          >
            {option.label}
          </button>
        ))}
      </div>
    </div>
  )
}

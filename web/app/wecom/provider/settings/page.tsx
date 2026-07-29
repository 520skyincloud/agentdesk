"use client"

import {
  Building2Icon,
  CheckCircle2Icon,
  ExternalLinkIcon,
  Link2Icon,
  Loader2Icon,
  RefreshCwIcon,
  ShieldCheckIcon,
  UserRoundCheckIcon,
  WifiIcon,
} from "lucide-react"
import { Suspense, useCallback, useEffect, useState } from "react"

import { OptionCombobox } from "@/components/option-combobox"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { useI18n } from "@/i18n/provider"
import {
  beginArrivalProviderAuthorization,
  completeArrivalProviderConnection,
  fetchArrivalProviderOptions,
  validateArrivalProviderInvitation,
  type ArrivalConnectionVerification,
  type ArrivalProviderInvitation,
  type ArrivalProviderOptions,
} from "@/lib/api/arrival"
import { cn, formatDateTime } from "@/lib/utils"

const invitationStorageKey = "agentdesk:arrival-provider-invitation"
const stateStorageKey = "agentdesk:arrival-provider-state"

type PortalPhase =
  | "initializing"
  | "invitation"
  | "redirecting"
  | "selection"
  | "completing"
  | "completed"
  | "error"

export default function WeComProviderSettingsPage() {
  return (
    <Suspense fallback={<PortalLoading />}>
      <WeComProviderSettingsContent />
    </Suspense>
  )
}

function WeComProviderSettingsContent() {
  const t = useI18n()
  const [phase, setPhase] = useState<PortalPhase>("initializing")
  const [invitationToken, setInvitationToken] = useState("")
  const [authorizationState, setAuthorizationState] = useState("")
  const [invitation, setInvitation] =
    useState<ArrivalProviderInvitation | null>(null)
  const [options, setOptions] = useState<ArrivalProviderOptions | null>(null)
  const [memberToken, setMemberToken] = useState("")
  const [instanceId, setInstanceId] = useState("")
  const [verification, setVerification] =
    useState<ArrivalConnectionVerification | null>(null)
  const [errorMessage, setErrorMessage] = useState("")

  const loadOptions = useCallback(
    async (state: string) => {
      setPhase("initializing")
      setErrorMessage("")
      try {
        const next = await fetchArrivalProviderOptions(state)
        setOptions(next)
        setMemberToken(next.members.length === 1 ? next.members[0].value : "")
        setInstanceId(
          next.instances.length === 1 ? String(next.instances[0].id) : ""
        )
        setPhase("selection")
      } catch (error) {
        setErrorMessage(
          error instanceof Error
            ? error.message
            : t("arrivalProvider.optionsLoadFailed")
        )
        setPhase("error")
      }
    },
    [t]
  )

  const validateInvitation = useCallback(
    async (token: string) => {
      setPhase("initializing")
      setErrorMessage("")
      try {
        const next = await validateArrivalProviderInvitation(token)
        setInvitation(next)
        setPhase("invitation")
      } catch (error) {
        setErrorMessage(
          error instanceof Error
            ? error.message
            : t("arrivalProvider.invitationInvalid")
        )
        setPhase("error")
      }
    },
    [t]
  )

  useEffect(() => {
    const currentURL = new URL(window.location.href)
    const inviteFromURL = currentURL.searchParams.get("invite")?.trim() ?? ""
    const stateFromURL = currentURL.searchParams.get("state")?.trim() ?? ""
    const authorizationResult =
      currentURL.searchParams.get("authorization")?.trim() ?? ""

    if (inviteFromURL) {
      sessionStorage.setItem(invitationStorageKey, inviteFromURL)
    }
    if (stateFromURL) {
      sessionStorage.setItem(stateStorageKey, stateFromURL)
    }
    const storedInvitation =
      inviteFromURL || sessionStorage.getItem(invitationStorageKey) || ""
    const storedState =
      stateFromURL || sessionStorage.getItem(stateStorageKey) || ""

    window.history.replaceState(
      window.history.state,
      "",
      window.location.pathname
    )
    setInvitationToken(storedInvitation)
    setAuthorizationState(storedState)

    if (authorizationResult === "failed") {
      setErrorMessage(t("arrivalProvider.authorizationFailed"))
      setPhase("error")
      return
    }
    // The provider callback marker is removed from the URL after first load.
    // Revalidate the stored state on refresh so a completed, unexpired attempt
    // can resume member binding without issuing another invitation.
    if (storedState) {
      void loadOptions(storedState)
      return
    }
    if (storedInvitation) {
      void validateInvitation(storedInvitation)
      return
    }
    setErrorMessage(t("arrivalProvider.invitationMissing"))
    setPhase("error")
  }, [loadOptions, t, validateInvitation])

  async function beginAuthorization() {
    if (!invitationToken || phase === "redirecting") return
    setPhase("redirecting")
    setErrorMessage("")
    try {
      const result = await beginArrivalProviderAuthorization(invitationToken)
      sessionStorage.setItem(stateStorageKey, result.authorizationState)
      setAuthorizationState(result.authorizationState)
      if (result.alreadyAuthorized) {
        await loadOptions(result.authorizationState)
        return
      }
      const target = new URL(result.authorizationUrl)
      if (
        target.protocol !== "https:" ||
        target.hostname !== "open.work.weixin.qq.com"
      ) {
        throw new Error(t("arrivalProvider.authorizationUrlInvalid"))
      }
      window.location.assign(target.toString())
    } catch (error) {
      setErrorMessage(
        error instanceof Error
          ? error.message
          : t("arrivalProvider.authorizationFailed")
      )
      setPhase("error")
    }
  }

  async function completeConnection() {
    if (
      !authorizationState ||
      !memberToken ||
      !Number(instanceId) ||
      phase === "completing"
    ) {
      return
    }
    setPhase("completing")
    setErrorMessage("")
    try {
      const result = await completeArrivalProviderConnection({
        authorizationState,
        contactMemberToken: memberToken,
        wxWorkProtocolInstanceId: Number(instanceId),
      })
      setVerification(result)
      sessionStorage.removeItem(invitationStorageKey)
      sessionStorage.removeItem(stateStorageKey)
      setPhase("completed")
    } catch (error) {
      setErrorMessage(
        error instanceof Error
          ? error.message
          : t("arrivalProvider.completeFailed")
      )
      setPhase("selection")
    }
  }

  function retry() {
    if (authorizationState && options) {
      setErrorMessage("")
      setPhase("selection")
      return
    }
    if (invitationToken) {
      void validateInvitation(invitationToken)
      return
    }
    setErrorMessage(t("arrivalProvider.invitationMissing"))
  }

  return (
    <main className="min-h-screen bg-[#f4f7f9] text-foreground">
      <header className="border-b bg-white">
        <div className="mx-auto flex min-h-14 max-w-5xl items-center justify-between gap-4 px-4 py-3">
          <div className="flex min-w-0 items-center gap-2.5">
            <div className="flex size-8 shrink-0 items-center justify-center rounded-lg bg-emerald-700 text-white">
              <Link2Icon className="size-4" />
            </div>
            <div className="min-w-0">
              <div className="truncate text-sm font-semibold">
                {t("app.brand")}
              </div>
              <div className="truncate text-xs text-muted-foreground">
                {t("arrivalProvider.title")}
              </div>
            </div>
          </div>
          <Badge
            variant="outline"
            className="border-emerald-200 bg-emerald-50 text-emerald-700"
          >
            <ShieldCheckIcon className="size-3.5" />
            {t("arrivalProvider.secureAuthorization")}
          </Badge>
        </div>
      </header>

      <div className="mx-auto max-w-5xl px-4 py-6 sm:py-10">
        <PortalSteps phase={phase} />

        <section className="mt-6 overflow-hidden rounded-lg border bg-white shadow-sm">
          {phase === "initializing" ? (
            <PortalLoading embedded />
          ) : phase === "invitation" ? (
            <InvitationPanel
              invitation={invitation}
              redirecting={false}
              onBegin={() => void beginAuthorization()}
            />
          ) : phase === "redirecting" ? (
            <InvitationPanel
              invitation={invitation}
              redirecting
              onBegin={() => undefined}
            />
          ) : phase === "selection" || phase === "completing" ? (
            <SelectionPanel
              options={options}
              memberToken={memberToken}
              instanceId={instanceId}
              errorMessage={errorMessage}
              completing={phase === "completing"}
              onMemberChange={setMemberToken}
              onInstanceChange={setInstanceId}
              onComplete={() => void completeConnection()}
            />
          ) : phase === "completed" ? (
            <CompletedPanel verification={verification} />
          ) : (
            <ErrorPanel message={errorMessage} onRetry={retry} />
          )}
        </section>

        <div className="mt-4 flex items-start gap-2 text-xs leading-5 text-muted-foreground">
          <ShieldCheckIcon className="mt-0.5 size-3.5 shrink-0" />
          <span>{t("arrivalProvider.securityNote")}</span>
        </div>
      </div>
    </main>
  )
}

function PortalSteps({ phase }: { phase: PortalPhase }) {
  const t = useI18n()
  const current =
    phase === "completed"
      ? 4
      : phase === "selection" || phase === "completing"
        ? 3
        : phase === "redirecting"
          ? 2
          : 1
  const steps = [
    { label: t("arrivalProvider.stepStore"), icon: Building2Icon },
    { label: t("arrivalProvider.stepAuthorization"), icon: ShieldCheckIcon },
    { label: t("arrivalProvider.stepBinding"), icon: UserRoundCheckIcon },
    { label: t("arrivalProvider.stepComplete"), icon: CheckCircle2Icon },
  ]
  return (
    <ol className="grid grid-cols-4 overflow-hidden rounded-lg border bg-white">
      {steps.map((step, index) => {
        const number = index + 1
        const active = number <= current
        return (
          <li
            key={step.label}
            className={cn(
              "flex min-w-0 items-center gap-2 border-r px-2 py-3 last:border-r-0 sm:px-4",
              active && "bg-emerald-50/60"
            )}
          >
            <step.icon
              className={cn(
                "size-4 shrink-0 text-muted-foreground",
                active && "text-emerald-700"
              )}
            />
            <span
              className={cn(
                "truncate text-xs text-muted-foreground sm:text-sm",
                active && "font-medium text-foreground"
              )}
            >
              {step.label}
            </span>
          </li>
        )
      })}
    </ol>
  )
}

function InvitationPanel({
  invitation,
  redirecting,
  onBegin,
}: {
  invitation: ArrivalProviderInvitation | null
  redirecting: boolean
  onBegin: () => void
}) {
  const t = useI18n()
  return (
    <div className="grid min-h-80 md:grid-cols-[minmax(0,1fr)_280px]">
      <div className="p-5 sm:p-7">
        <div className="flex items-center gap-2 text-sm font-medium text-emerald-700">
          <Building2Icon className="size-4" />
          {t("arrivalProvider.invitedStore")}
        </div>
        <h1 className="mt-3 text-2xl font-semibold">
          {invitation?.storeName || "-"}
        </h1>
        <div className="mt-1 text-sm text-muted-foreground">
          {invitation?.brandName || t("arrivalProvider.brandUnavailable")}
        </div>
        <dl className="mt-6 grid gap-3 text-sm sm:grid-cols-2">
          <Info
            label={t("arrivalProvider.currentStatus")}
            value={invitation?.connectionStatus || "-"}
          />
          <Info
            label={t("arrivalProvider.invitationExpiry")}
            value={formatDateTime(invitation?.expiresAt)}
          />
        </dl>
      </div>
      <div className="flex flex-col justify-center border-t bg-[#f7faf9] p-5 md:border-t-0 md:border-l">
        <ShieldCheckIcon className="size-7 text-emerald-700" />
        <div className="mt-3 font-semibold">
          {invitation?.authorized
            ? t("arrivalProvider.authorizationReusable")
            : t("arrivalProvider.authorizationRequired")}
        </div>
        <Button
          className="mt-5 w-full"
          disabled={redirecting}
          onClick={onBegin}
        >
          {redirecting ? (
            <Loader2Icon className="animate-spin" />
          ) : (
            <ExternalLinkIcon />
          )}
          {redirecting
            ? t("arrivalProvider.redirecting")
            : invitation?.authorized
              ? t("arrivalProvider.continueBinding")
              : t("arrivalProvider.openAuthorization")}
        </Button>
      </div>
    </div>
  )
}

function SelectionPanel({
  options,
  memberToken,
  instanceId,
  errorMessage,
  completing,
  onMemberChange,
  onInstanceChange,
  onComplete,
}: {
  options: ArrivalProviderOptions | null
  memberToken: string
  instanceId: string
  errorMessage: string
  completing: boolean
  onMemberChange: (value: string) => void
  onInstanceChange: (value: string) => void
  onComplete: () => void
}) {
  const t = useI18n()
  return (
    <div className="p-5 sm:p-7">
      <div className="flex items-center gap-2 text-sm font-medium text-emerald-700">
        <UserRoundCheckIcon className="size-4" />
        {options?.storeName || "-"}
      </div>
      <h1 className="mt-3 text-xl font-semibold">
        {t("arrivalProvider.bindingTitle")}
      </h1>
      {errorMessage ? (
        <div className="mt-4 rounded-lg border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-700">
          {errorMessage}
        </div>
      ) : null}
      <div className="mt-6 grid gap-5 md:grid-cols-2">
        <div className="space-y-2">
          <label className="text-sm font-medium">
            {t("arrivalProvider.contactMember")}
          </label>
          <OptionCombobox
            value={memberToken}
            options={(options?.members ?? []).map((item) => ({
              value: item.value,
              label: item.label,
            }))}
            placeholder={t("arrivalProvider.selectContactMember")}
            onChange={onMemberChange}
          />
        </div>
        <div className="space-y-2">
          <label className="text-sm font-medium">
            {t("arrivalProvider.employeeInstance")}
          </label>
          <OptionCombobox
            value={instanceId}
            options={(options?.instances ?? []).map((item) => ({
              value: String(item.id),
              label: `${item.name} · ${item.healthStatus || t("arrivalProvider.healthUnknown")}`,
            }))}
            placeholder={t("arrivalProvider.selectEmployeeInstance")}
            onChange={onInstanceChange}
          />
        </div>
      </div>
      <div className="mt-5 border-t pt-5">
        <div className="flex items-center gap-2 text-sm text-muted-foreground">
          <WifiIcon className="size-4" />
          {t("arrivalProvider.availableCounts", {
            members: options?.members.length ?? 0,
            instances: options?.instances.length ?? 0,
          })}
        </div>
        <div className="mt-5 flex justify-end">
          <Button
            disabled={!memberToken || !instanceId || completing}
            onClick={onComplete}
          >
            {completing ? (
              <Loader2Icon className="animate-spin" />
            ) : (
              <Link2Icon />
            )}
            {completing
              ? t("arrivalProvider.completing")
              : t("arrivalProvider.completeConnection")}
          </Button>
        </div>
      </div>
    </div>
  )
}

function CompletedPanel({
  verification,
}: {
  verification: ArrivalConnectionVerification | null
}) {
  const t = useI18n()
  return (
    <div className="flex min-h-80 items-center justify-center p-6 text-center">
      <div className="max-w-lg">
        <div className="mx-auto flex size-12 items-center justify-center rounded-full bg-emerald-100 text-emerald-700">
          <CheckCircle2Icon className="size-7" />
        </div>
        <h1 className="mt-4 text-xl font-semibold">
          {t("arrivalProvider.completedTitle")}
        </h1>
        <p className="mt-2 text-sm text-muted-foreground">
          {t("arrivalProvider.completedStatus", {
            status: verification?.connectionStatus || "active",
          })}
        </p>
      </div>
    </div>
  )
}

function ErrorPanel({
  message,
  onRetry,
}: {
  message: string
  onRetry: () => void
}) {
  const t = useI18n()
  return (
    <div className="flex min-h-80 items-center justify-center p-6 text-center">
      <div className="max-w-lg">
        <div className="mx-auto flex size-12 items-center justify-center rounded-full bg-red-100 text-red-700">
          <Link2Icon className="size-6" />
        </div>
        <h1 className="mt-4 text-xl font-semibold">
          {t("arrivalProvider.errorTitle")}
        </h1>
        <p className="mt-2 break-words text-sm leading-6 text-muted-foreground">
          {message}
        </p>
        <Button className="mt-5" variant="outline" onClick={onRetry}>
          <RefreshCwIcon />
          {t("arrivalProvider.retry")}
        </Button>
      </div>
    </div>
  )
}

function PortalLoading({ embedded = false }: { embedded?: boolean }) {
  const t = useI18n()
  return (
    <div
      className={cn(
        "flex items-center justify-center text-sm text-muted-foreground",
        embedded ? "min-h-80" : "min-h-screen bg-[#f4f7f9]"
      )}
    >
      <Loader2Icon className="mr-2 size-4 animate-spin" />
      {t("arrivalProvider.loading")}
    </div>
  )
}

function Info({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className="mt-1 font-medium">{value}</dd>
    </div>
  )
}

"use client"

import {
  Building2Icon,
  CheckIcon,
  CopyIcon,
  LinkIcon,
  LoaderCircleIcon,
  RefreshCwIcon,
} from "lucide-react"
import { useEffect, useMemo, useState } from "react"
import { toast } from "sonner"

import { useConfirm } from "@/components/confirm-provider"
import { useI18n } from "@/i18n/provider"
import { fetchAuthOptions } from "@/lib/api/auth"
import {
  fetchCurrentTenantInvitation,
  rotateCurrentTenantInvitation,
} from "@/lib/api/tenant-registration"
import type { TenantInvitation } from "@/lib/api/tenant"
import { formatDateTime } from "@/lib/utils"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"

type InvitationDialogProps = {
  open: boolean
  canRotate: boolean
  onOpenChange: (open: boolean) => void
}

export function InvitationDialog({
  open,
  canRotate,
  onOpenChange,
}: InvitationDialogProps) {
  const t = useI18n()
  const confirm = useConfirm()
  const [loading, setLoading] = useState(false)
  const [rotating, setRotating] = useState(false)
  const [copied, setCopied] = useState<"code" | "link" | null>(null)
  const [registrationEnabled, setRegistrationEnabled] = useState(false)
  const [invitation, setInvitation] = useState<TenantInvitation | null>(null)
  const absoluteInviteLink = useMemo(() => {
    if (!invitation?.inviteLink) {
      return ""
    }
    if (typeof window === "undefined") {
      return invitation.inviteLink
    }
    return new URL(invitation.inviteLink, window.location.origin).toString()
  }, [invitation?.inviteLink])

  useEffect(() => {
    if (!open) {
      return
    }
    let cancelled = false
    setLoading(true)
    setCopied(null)
    void Promise.all([fetchCurrentTenantInvitation(), fetchAuthOptions()])
      .then(([nextInvitation, options]) => {
        if (!cancelled) {
          setInvitation(nextInvitation)
          setRegistrationEnabled(options.tenantRegistrationEnabled)
        }
      })
      .catch((error) => {
        if (!cancelled) {
          setInvitation(null)
          toast.error(
            error instanceof Error
              ? error.message
              : t("tenantRegistration.invitationLoadFailed")
          )
        }
      })
      .finally(() => {
        if (!cancelled) {
          setLoading(false)
        }
      })
    return () => {
      cancelled = true
    }
  }, [open, t])

  async function copyValue(value: string, target: "code" | "link") {
    try {
      await navigator.clipboard.writeText(value)
      setCopied(target)
      toast.success(t("tenantRegistration.copied"))
    } catch {
      toast.error(t("tenantRegistration.copyFailed"))
    }
  }

  async function rotateInvitation() {
    const confirmed = await confirm({
      title: t("tenantRegistration.rotateTitle"),
      description: t("tenantRegistration.rotateDescription"),
      confirmText: t("tenantRegistration.rotateConfirm"),
      variant: "destructive",
    })
    if (!confirmed) {
      return
    }
    setRotating(true)
    try {
      const nextInvitation = await rotateCurrentTenantInvitation()
      setInvitation(nextInvitation)
      setCopied(null)
      toast.success(t("tenantRegistration.rotateSuccess"))
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : t("tenantRegistration.rotateFailed")
      )
    } finally {
      setRotating(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>{t("tenantRegistration.inviteDialogTitle")}</DialogTitle>
          <DialogDescription>
            {t("tenantRegistration.inviteDialogDescription")}
          </DialogDescription>
        </DialogHeader>

        {loading ? (
          <div className="flex min-h-52 items-center justify-center gap-2 text-muted-foreground">
            <LoaderCircleIcon className="size-4 animate-spin" />
            {t("tenantRegistration.invitationLoading")}
          </div>
        ) : invitation ? (
          <div className="space-y-5">
            <div className="flex flex-wrap items-center justify-between gap-3 border-b pb-4">
              <div className="flex min-w-0 items-center gap-3">
                <div className="flex size-10 shrink-0 items-center justify-center rounded-lg bg-[#eef5ff] text-primary dark:bg-primary/10">
                  <Building2Icon className="size-5" />
                </div>
                <div className="min-w-0">
                  <div className="truncate font-medium">{invitation.tenantName}</div>
                  <div className="text-xs text-muted-foreground">
                    {t("tenantRegistration.invitationVersion", {
                      version: invitation.version,
                    })}
                  </div>
                </div>
              </div>
              <Badge variant={registrationEnabled ? "secondary" : "outline"}>
                {registrationEnabled
                  ? t("tenantRegistration.registrationOpen")
                  : t("tenantRegistration.registrationClosed")}
              </Badge>
            </div>

            {!registrationEnabled ? (
              <Alert>
                <AlertTitle>{t("tenantRegistration.registrationClosed")}</AlertTitle>
                <AlertDescription>
                  {t("tenantRegistration.registrationClosedHint")}
                </AlertDescription>
              </Alert>
            ) : null}

            <div className="space-y-2">
              <label htmlFor="tenant-invitation-code" className="text-sm font-medium">
                {t("tenantRegistration.invitationCode")}
              </label>
              <div className="flex gap-2">
                <Input
                  id="tenant-invitation-code"
                  value={invitation.code}
                  readOnly
                  className="font-mono text-xs"
                />
                <Button
                  type="button"
                  variant="outline"
                  size="icon"
                  aria-label={t("tenantRegistration.copyCode")}
                  onClick={() => void copyValue(invitation.code, "code")}
                >
                  {copied === "code" ? <CheckIcon /> : <CopyIcon />}
                </Button>
              </div>
            </div>

            <div className="space-y-2">
              <label htmlFor="tenant-invitation-link" className="text-sm font-medium">
                {t("tenantRegistration.invitationLink")}
              </label>
              <div className="flex gap-2">
                <div className="relative min-w-0 flex-1">
                  <LinkIcon className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" />
                  <Input
                    id="tenant-invitation-link"
                    value={absoluteInviteLink}
                    readOnly
                    className="pl-9 text-xs"
                  />
                </div>
                <Button
                  type="button"
                  variant="outline"
                  size="icon"
                  aria-label={t("tenantRegistration.copyLink")}
                  onClick={() => void copyValue(absoluteInviteLink, "link")}
                >
                  {copied === "link" ? <CheckIcon /> : <CopyIcon />}
                </Button>
              </div>
            </div>

            <dl className="grid grid-cols-2 gap-x-6 gap-y-3 border-t pt-4 text-sm sm:grid-cols-4">
              <div>
                <dt className="text-xs text-muted-foreground">
                  {t("tenantRegistration.usedCount")}
                </dt>
                <dd className="mt-1 font-medium">{invitation.usedCount}</dd>
              </div>
              <div>
                <dt className="text-xs text-muted-foreground">
                  {t("tenantRegistration.lastUsedAt")}
                </dt>
                <dd className="mt-1 font-medium">{formatDateTime(invitation.lastUsedAt)}</dd>
              </div>
              <div>
                <dt className="text-xs text-muted-foreground">
                  {t("tenantRegistration.createdAt")}
                </dt>
                <dd className="mt-1 font-medium">{formatDateTime(invitation.createdAt)}</dd>
              </div>
              <div>
                <dt className="text-xs text-muted-foreground">
                  {t("tenantRegistration.rotatedAt")}
                </dt>
                <dd className="mt-1 font-medium">{formatDateTime(invitation.rotatedAt)}</dd>
              </div>
            </dl>
          </div>
        ) : (
          <Alert variant="destructive">
            <AlertTitle>{t("tenantRegistration.invitationLoadFailed")}</AlertTitle>
            <AlertDescription>
              {t("tenantRegistration.invitationLoadFailedHint")}
            </AlertDescription>
          </Alert>
        )}

        <DialogFooter>
          <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
            {t("common.close")}
          </Button>
          {canRotate && invitation ? (
            <Button
              type="button"
              variant="destructive"
              disabled={rotating}
              onClick={() => void rotateInvitation()}
            >
              <RefreshCwIcon className={rotating ? "animate-spin" : undefined} />
              {rotating
                ? t("tenantRegistration.rotating")
                : t("tenantRegistration.rotateInvitation")}
            </Button>
          ) : null}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

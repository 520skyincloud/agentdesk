"use client"

import { CheckCircle2Icon, CopyIcon } from "lucide-react"
import { toast } from "sonner"

import { ProjectDialog } from "@/components/project-dialog"
import { Button } from "@/components/ui/button"
import { useI18n } from "@/i18n/provider"
import type { CreateTenantResult } from "@/lib/api/tenant"

type TenantCreationResultDialogProps = {
  result: CreateTenantResult | null
  onOpenChange: (open: boolean) => void
}

export function TenantCreationResultDialog({
  result,
  onOpenChange,
}: TenantCreationResultDialogProps) {
  const t = useI18n()
  if (!result) return null

  const invitationLink = getAbsoluteInvitationLink(result.invitation.inviteLink)
  const rows = [
    { label: t("tenant.resultTenantCode"), value: result.tenant.tenantCode },
    {
      label: t("tenant.resultSupervisorUsername"),
      value: result.supervisorUsername,
    },
    {
      label: t("tenant.resultSupervisorPassword"),
      value: result.supervisorPassword,
    },
    { label: t("tenant.resultInvitationCode"), value: result.invitation.code },
    { label: t("tenant.resultInvitationLink"), value: invitationLink },
    {
      label: t("tenant.resultDefaultTeamId"),
      value: String(result.defaultAgentTeamId),
    },
  ]

  async function copyText(value: string) {
    try {
      await navigator.clipboard.writeText(value)
      toast.success(t("tenant.copySuccess"))
    } catch {
      toast.error(t("tenant.copyFailed"))
    }
  }

  async function copyAll() {
    const content = rows.map((row) => `${row.label}: ${row.value}`).join("\n")
    await copyText(content)
  }

  return (
    <ProjectDialog
      open
      onOpenChange={onOpenChange}
      title={t("tenant.resultTitle")}
      description={t("tenant.resultDescription")}
      size="md"
      footer={
        <>
          <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
            {t("common.close")}
          </Button>
          <Button type="button" onClick={() => void copyAll()}>
            <CopyIcon />
            {t("tenant.copyAll")}
          </Button>
        </>
      }
    >
      <div className="flex items-start gap-3 border-b border-border/70 pb-4">
        <CheckCircle2Icon className="mt-0.5 size-5 shrink-0 text-emerald-600" />
        <div className="min-w-0">
          <div className="font-medium">{result.tenant.legalName}</div>
          <div className="mt-1 text-sm text-muted-foreground">
            {t("tenant.resultReady")}
          </div>
        </div>
      </div>

      <div className="divide-y divide-border/70 border-y border-border/70">
        {rows.map((row) => (
          <div
            key={row.label}
            className="grid gap-2 py-3 sm:grid-cols-[160px_minmax(0,1fr)_36px] sm:items-center"
          >
            <div className="text-sm text-muted-foreground">{row.label}</div>
            <div className="break-all font-mono text-sm">{row.value || "-"}</div>
            <Button
              type="button"
              variant="ghost"
              size="icon-sm"
              className="justify-self-end"
              onClick={() => void copyText(row.value)}
              aria-label={t("tenant.copyField", { field: row.label })}
              title={t("tenant.copyField", { field: row.label })}
            >
              <CopyIcon />
            </Button>
          </div>
        ))}
      </div>
    </ProjectDialog>
  )
}

function getAbsoluteInvitationLink(inviteLink: string) {
  if (!inviteLink || !inviteLink.startsWith("/") || typeof window === "undefined") {
    return inviteLink
  }
  return `${window.location.origin}${inviteLink}`
}

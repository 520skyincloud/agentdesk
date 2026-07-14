"use client"

import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import {
  CheckIcon,
  ChevronDownIcon,
  MessageSquareTextIcon,
  PencilIcon,
  PlusIcon,
  RefreshCcwIcon,
  SendIcon,
  UserRoundIcon,
} from "lucide-react"
import { toast } from "sonner"

import { type CustomerFormSavePayload } from "@/components/customer-form"
import { CustomerFormDialog } from "@/components/customer-form-dialog"
import { CustomerLinkOrCreateDialog } from "@/components/customer-link-or-create-dialog"
import { useAuth } from "@/components/auth-provider"
import { useConfirm } from "@/components/confirm-provider"
import { ContentEditor } from "@/components/content-editor"
import { ProjectDialog } from "@/components/project-dialog"
import { isRichTextEmpty, SafeRichHTML } from "@/components/safe-rich-html"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Separator } from "@/components/ui/separator"
import { saveCustomerProfile } from "@/lib/api/customer"
import {
  changeTicketStatus,
  createTicketProgress,
  fetchTicketDetail,
  type CreateTicketPayload,
  type TicketDetail,
  type TicketStatus,
  type UpdateTicketPayload,
  updateTicket,
} from "@/lib/api/ticket"
import { useI18n } from "@/i18n/provider"
import { cn, formatDateTime } from "@/lib/utils"
import { EditDialog } from "./edit"
import { TicketAssignDialog } from "./ticket-assign-dialog"
import { getTicketStatusMeta, TicketStatusBadge } from "./ticket-status-badge"

type TicketDetailDialogProps = {
  ticketId: number | null
  open: boolean
  onOpenChange: (open: boolean) => void
  onChanged: () => void
}

type TFunction = (key: string, values?: Record<string, string | number>) => string

function getStatusOptions(t: TFunction): Array<{ value: TicketStatus; label: string }> {
  return [
    { value: "pending", label: t("ticket.statusPending") },
    { value: "in_progress", label: t("ticket.statusInProgress") },
    { value: "done", label: t("ticket.statusDone") },
  ]
}

function getStatusLabel(status: TicketStatus, t: TFunction) {
  return getStatusOptions(t).find((option) => option.value === status)?.label ?? status
}

function sourceLabel(source: string, t: TFunction) {
  switch (source) {
    case "manual":
      return t("ticket.manualCreated")
    case "conversation":
      return t("ticket.conversationGenerated")
    default:
      return source || "-"
  }
}

function metadataValue(value?: string | number | null) {
  if (value === undefined || value === null || value === "") {
    return "-"
  }
  return String(value)
}

function getTicketCustomerId(ticket?: TicketDetail["ticket"] | null) {
  return Number(ticket?.customer?.id || ticket?.customerId || 0)
}

export function TicketDetailDialog({
  ticketId,
  open,
  onOpenChange,
  onChanged,
}: TicketDetailDialogProps) {
  const t = useI18n()
  const confirm = useConfirm()
  const { session } = useAuth()
  const [detail, setDetail] = useState<TicketDetail | null>(null)
  const [loading, setLoading] = useState(false)
  const [statusSaving, setStatusSaving] = useState<TicketStatus | null>(null)
  const [progressSaving, setProgressSaving] = useState(false)
  const [progressOpen, setProgressOpen] = useState(false)
  const [progressContent, setProgressContent] = useState("")
  const [assignOpen, setAssignOpen] = useState(false)
  const [editOpen, setEditOpen] = useState(false)
  const [editSaving, setEditSaving] = useState(false)
  const [customerEditOpen, setCustomerEditOpen] = useState(false)
  const [customerEditSaving, setCustomerEditSaving] = useState(false)
  const [customerLinkOpen, setCustomerLinkOpen] = useState(false)
  const loadSeqRef = useRef(0)
  const dialogSeqRef = useRef(0)
  const currentTicketIdRef = useRef<number | null>(null)
  const permissions = useMemo(
    () => new Set(session?.permissions ?? []),
    [session?.permissions],
  )
  const canUpdate = permissions.has("ticket.update")
  const canAssign = permissions.has("ticket.assign") && permissions.has("agent.view")
  const canChangeStatus = permissions.has("ticket.changeStatus")
  const canAddProgress = permissions.has("ticket.progress")
  const canViewTags = permissions.has("tag.view")
  const canViewCustomers = permissions.has("customer.view")
  const canCreateCustomers = permissions.has("customer.create")
  const canUpdateCustomers = permissions.has("customer.update")
  currentTicketIdRef.current = open ? ticketId : null

  function isCurrentOperation(targetTicketId: number, dialogSeq: number) {
    return currentTicketIdRef.current === targetTicketId && dialogSeqRef.current === dialogSeq
  }

  const loadDetail = useCallback(async (targetTicketId = ticketId, dialogSeq = dialogSeqRef.current) => {
    if (!targetTicketId || !isCurrentOperation(targetTicketId, dialogSeq)) {
      return
    }
    const seq = loadSeqRef.current + 1
    loadSeqRef.current = seq
    if (!open || !targetTicketId) {
      setDetail(null)
      setLoading(false)
      return
    }
    setLoading(true)
    try {
      const data = await fetchTicketDetail(targetTicketId)
      if (loadSeqRef.current !== seq || !isCurrentOperation(targetTicketId, dialogSeq)) {
        return
      }
      setDetail(data)
    } catch (error) {
      if (loadSeqRef.current !== seq || !isCurrentOperation(targetTicketId, dialogSeq)) {
        return
      }
      toast.error(error instanceof Error ? error.message : t("ticket.loadDetailFailed"))
    } finally {
      if (loadSeqRef.current === seq) {
        setLoading(false)
      }
    }
  }, [open, t, ticketId])

  useEffect(() => {
    dialogSeqRef.current += 1
    setDetail(null)
    setLoading(false)
    setStatusSaving(null)
    setProgressSaving(false)
    setProgressOpen(false)
    setEditSaving(false)
    setCustomerEditSaving(false)
    setAssignOpen(false)
    setEditOpen(false)
    setCustomerEditOpen(false)
    setCustomerLinkOpen(false)
    setProgressContent("")
  }, [open, ticketId])

  useEffect(() => {
    void loadDetail(ticketId, dialogSeqRef.current)
  }, [loadDetail, ticketId])

  async function handleStatusChange(status: TicketStatus) {
    if (!canChangeStatus || !detail || detail.ticket.status === status) {
      return
    }
    const confirmed = await confirm({
      title: t("ticket.statusChangeConfirmTitle"),
      description: t("ticket.statusChangeConfirmDescription", {
        from: getStatusLabel(detail.ticket.status, t),
        to: getStatusLabel(status, t),
      }),
      confirmText: t("ticket.statusChangeConfirm"),
    })
    if (!confirmed) {
      return
    }
    const activeTicketId = detail.ticket.id
    const activeDialogSeq = dialogSeqRef.current
    setStatusSaving(status)
    try {
      await changeTicketStatus({ ticketId: activeTicketId, status })
      if (!isCurrentOperation(activeTicketId, activeDialogSeq)) {
        return
      }
      toast.success(t("ticket.statusUpdated"))
      await loadDetail(activeTicketId, activeDialogSeq)
      if (!isCurrentOperation(activeTicketId, activeDialogSeq)) {
        return
      }
      onChanged()
    } catch (error) {
      if (!isCurrentOperation(activeTicketId, activeDialogSeq)) {
        return
      }
      toast.error(error instanceof Error ? error.message : t("ticket.statusUpdateFailed"))
    } finally {
      if (isCurrentOperation(activeTicketId, activeDialogSeq)) {
        setStatusSaving(null)
      }
    }
  }

  async function handleCreateProgress() {
    if (!canAddProgress || !detail) {
      return
    }
    const activeTicketId = detail.ticket.id
    const activeDialogSeq = dialogSeqRef.current
    const content = progressContent.trim()
    if (isRichTextEmpty(content)) {
      toast.error(t("ticket.progressRequired"))
      return
    }
    setProgressSaving(true)
    try {
      await createTicketProgress({
        ticketId: activeTicketId,
        content,
      })
      if (!isCurrentOperation(activeTicketId, activeDialogSeq)) {
        return
      }
      toast.success(t("ticket.progressRecorded"))
      setProgressContent("")
      setProgressOpen(false)
      await loadDetail(activeTicketId, activeDialogSeq)
      if (!isCurrentOperation(activeTicketId, activeDialogSeq)) {
        return
      }
      onChanged()
    } catch (error) {
      if (!isCurrentOperation(activeTicketId, activeDialogSeq)) {
        return
      }
      toast.error(error instanceof Error ? error.message : t("ticket.progressCreateFailed"))
    } finally {
      if (isCurrentOperation(activeTicketId, activeDialogSeq)) {
        setProgressSaving(false)
      }
    }
  }

  async function handleAssigned() {
    const activeTicketId = ticket?.id
    const activeDialogSeq = dialogSeqRef.current
    if (!activeTicketId || !isCurrentOperation(activeTicketId, activeDialogSeq)) {
      return
    }
    await loadDetail(activeTicketId, activeDialogSeq)
    if (!isCurrentOperation(activeTicketId, activeDialogSeq)) {
      return
    }
    onChanged()
  }

  async function handleUpdateTicket(payload: CreateTicketPayload | UpdateTicketPayload) {
    if (!canUpdate || !("ticketId" in payload) || payload.ticketId <= 0) {
      toast.error(t("ticket.selectTicket"))
      return
    }
    const activeDialogSeq = dialogSeqRef.current
    setEditSaving(true)
    try {
      await updateTicket(payload)
      if (!isCurrentOperation(payload.ticketId, activeDialogSeq)) {
        return
      }
      toast.success(t("ticket.updated"))
      setEditOpen(false)
      await loadDetail(payload.ticketId, activeDialogSeq)
      if (!isCurrentOperation(payload.ticketId, activeDialogSeq)) {
        return
      }
      onChanged()
    } catch (error) {
      if (!isCurrentOperation(payload.ticketId, activeDialogSeq)) {
        return
      }
      toast.error(error instanceof Error ? error.message : t("ticket.updateFailed"))
    } finally {
      if (isCurrentOperation(payload.ticketId, activeDialogSeq)) {
        setEditSaving(false)
      }
    }
  }

  async function handleUpdateCustomer(payload: CustomerFormSavePayload) {
    if (!canUpdateCustomers) {
      return
    }
    const activeCustomerId = getTicketCustomerId(ticket)
    if (!ticket?.id || activeCustomerId <= 0) {
      toast.error(t("ticket.noLinkedCustomer"))
      return
    }
    if (customerEditSaving) {
      return
    }
    const activeTicketId = ticket.id
    const activeDialogSeq = dialogSeqRef.current
    setCustomerEditSaving(true)
    try {
      await saveCustomerProfile({ ...payload, id: activeCustomerId })
      if (!isCurrentOperation(activeTicketId, activeDialogSeq)) {
        return
      }
      toast.success(t("ticket.saved"))
      setCustomerEditOpen(false)
      await loadDetail(activeTicketId, activeDialogSeq)
      if (!isCurrentOperation(activeTicketId, activeDialogSeq)) {
        return
      }
      onChanged()
    } catch (error) {
      if (!isCurrentOperation(activeTicketId, activeDialogSeq)) {
        return
      }
      toast.error(error instanceof Error ? error.message : t("ticket.saveFailed"))
    } finally {
      if (isCurrentOperation(activeTicketId, activeDialogSeq)) {
        setCustomerEditSaving(false)
      }
    }
  }

  async function handleCustomerLinked() {
    const activeTicketId = ticket?.id
    const activeDialogSeq = dialogSeqRef.current
    if (!activeTicketId || !isCurrentOperation(activeTicketId, activeDialogSeq)) {
      return
    }
    await loadDetail(activeTicketId, activeDialogSeq)
    if (!isCurrentOperation(activeTicketId, activeDialogSeq)) {
      return
    }
    onChanged()
  }

  const ticket = detail?.ticket
  const customerId = getTicketCustomerId(ticket)
  const statusOptions = getStatusOptions(t)
  const canManageCustomer = customerId > 0
    ? canUpdateCustomers
    : canUpdate && (canViewCustomers || canCreateCustomers)

  return (
    <>
      <ProjectDialog
        open={open}
        onOpenChange={onOpenChange}
        title={
          <div className="flex min-w-0 items-center gap-2 pr-16 text-base">
            <span className="truncate">{ticket?.title ?? t("ticket.detailTitle")}</span>
            {ticket ? <TicketStatusBadge status={ticket.status} /> : null}
          </div>
        }
        description={
          ticket ? (
            <span className="flex flex-wrap items-center gap-2 text-sm">
              <span className="font-mono">{ticket.ticketNo}</span>
              <span>{sourceLabel(ticket.source, t)}</span>
              <span>{t("ticket.creator", { name: metadataValue(ticket.createdByName || ticket.createdBy) })}</span>
            </span>
          ) : undefined
        }
        size="xxl"
        defaultFullscreen
        bodyScrollable={false}
        bodyClassName="flex w-full"
      >
        {loading && !ticket ? (
          <div className="flex h-130 items-center justify-center gap-2 text-sm text-muted-foreground">
            <RefreshCcwIcon className="size-4 animate-spin" />
            {t("ticket.loading")}
          </div>
        ) : ticket ? (
          <div className="grid h-full w-full grid-cols-1 overflow-hidden border-t border-[#dbe7f6] lg:grid-cols-[minmax(0,1fr)_380px]">
            <div className="min-h-0 space-y-5 overflow-y-auto border-b bg-[#fbfdff] p-6 lg:border-r lg:border-b-0">
              <section className="space-y-2">
                <div className="text-sm font-medium text-muted-foreground">{t("ticket.description")}</div>
                <div className="agentdesk-subtle-surface rounded-xl px-3 py-2">
                  <SafeRichHTML html={ticket.description} fallback={t("ticket.noDescription")} />
                </div>
              </section>

              <section className="grid gap-4 md:grid-cols-[minmax(0,1fr)_220px]">
                <div className="space-y-2">
                  <div className="text-sm font-medium text-muted-foreground">{t("ticket.currentStatus")}</div>
                  <div className="flex flex-wrap items-center gap-2">
                    {canChangeStatus ? (
                      <DropdownMenu>
                        <DropdownMenuTrigger
                          render={
                            <Button
                              type="button"
                              variant="outline"
                              disabled={!!statusSaving}
                              className={cn(
                                "h-9 min-w-36 justify-between gap-2 border px-3 font-medium",
                                getTicketStatusMeta(ticket.status)?.className,
                              )}
                            />
                          }
                        >
                          <span>{statusSaving ? t("ticket.updating") : getStatusLabel(ticket.status, t)}</span>
                          <ChevronDownIcon className="size-4 opacity-70" />
                        </DropdownMenuTrigger>
                        <DropdownMenuContent align="start" className="w-44 min-w-44">
                          {statusOptions.map((option) => {
                            const selected = ticket.status === option.value
                            return (
                              <DropdownMenuItem
                                key={option.value}
                                disabled={!!statusSaving || selected}
                                onClick={() => void handleStatusChange(option.value)}
                                className="justify-between"
                              >
                                <span>{option.label}</span>
                                {selected ? <CheckIcon className="size-4 text-primary" /> : null}
                              </DropdownMenuItem>
                            )
                          })}
                        </DropdownMenuContent>
                      </DropdownMenu>
                    ) : (
                      <TicketStatusBadge status={ticket.status} />
                    )}
                  </div>
                </div>
                <div className="space-y-2">
                  <div className="text-sm font-medium text-muted-foreground">{t("ticket.columnAssignee")}</div>
                  <div className="agentdesk-subtle-surface flex items-center justify-between gap-2 rounded-xl px-3 py-2">
                    <div className="flex min-w-0 items-center gap-2 text-sm">
                      <UserRoundIcon className="size-4 shrink-0 text-muted-foreground" />
                      <span className="truncate">{ticket.currentAssigneeName || t("ticket.unassigned")}</span>
                    </div>
                    {canAssign ? (
                      <Button type="button" size="sm" variant="outline" onClick={() => setAssignOpen(true)}>
                        {t("ticket.assign")}
                      </Button>
                    ) : null}
                  </div>
                </div>
              </section>

              <section className="space-y-2">
                <div className="flex items-center justify-between gap-2">
                  <div className="text-sm font-medium text-muted-foreground">{t("ticket.tags")}</div>
                  {canUpdate ? (
                    <Button type="button" size="sm" variant="outline" onClick={() => setEditOpen(true)}>
                      {t("ticket.edit")}
                    </Button>
                  ) : null}
                </div>
                {ticket.tags && ticket.tags.length > 0 ? (
                  <div className="flex flex-wrap gap-1.5">
                    {ticket.tags.map((tag) => (
                      <Badge key={tag.id} variant="outline">
                        {tag.name}
                      </Badge>
                    ))}
                  </div>
                ) : (
                  <div className="text-sm text-muted-foreground">{t("ticket.emptyTags")}</div>
                )}
              </section>

              <section className="agentdesk-subtle-surface space-y-3 rounded-xl p-3 text-sm">
                <div className="flex items-center justify-between gap-2">
                  <div className="font-medium text-muted-foreground">{t("ticket.customerInfo")}</div>
                  {canManageCustomer ? (
                    <Button
                      type="button"
                      variant="ghost"
                      size="sm"
                      className="h-7 shrink-0 gap-1 px-2 text-xs"
                      onClick={() => {
                        if (customerId > 0) {
                          setCustomerEditOpen(true)
                          return
                        }
                        setCustomerLinkOpen(true)
                      }}
                    >
                      <PencilIcon className="size-3.5" />
                      {customerId > 0 ? t("ticket.edit") : t("ticket.linkOrCreate")}
                    </Button>
                  ) : null}
                </div>
                <div className="grid gap-3 sm:grid-cols-2">
                  <MetadataItem label={t("ticket.customer")} value={ticket.customer?.name || ticket.customerId} />
                  <MetadataItem label={t("ticket.contact")} value={ticket.customer?.primaryMobile || ticket.customer?.primaryEmail} />
                </div>
              </section>

              <section className="agentdesk-subtle-surface space-y-3 rounded-xl p-3 text-sm">
                <div className="font-medium text-muted-foreground">{t("ticket.ticketInfo")}</div>
                <div className="grid gap-3 sm:grid-cols-2">
                  <MetadataItem label={t("ticket.source")} value={sourceLabel(ticket.source, t)} />
                  <MetadataItem label={t("ticket.channel")} value={ticket.channel} />
                  <MetadataItem label={t("ticket.conversationId")} value={ticket.conversationId || undefined} />
                  <MetadataItem label={t("ticket.columnUpdated")} value={ticket.updatedAt ? formatDateTime(ticket.updatedAt) : undefined} />
                </div>
              </section>
            </div>

            <aside className="flex min-h-0 flex-col bg-card">
              <div className="flex items-center justify-between gap-2 px-4 py-2">
                <div className="flex items-center gap-2 text-sm font-medium">
                  <MessageSquareTextIcon className="size-4 text-muted-foreground" />
                  {t("ticket.progress")}
                </div>
                {canAddProgress ? (
                  <Button type="button" size="sm" onClick={() => setProgressOpen(true)}>
                    <PlusIcon className="size-3.5" />
                    {t("ticket.addProgress")}
                  </Button>
                ) : null}
              </div>
              <Separator />
              <div className="min-h-0 flex-1 overflow-y-auto p-4">
                {detail.progresses && detail.progresses.length > 0 ? (
                  <div className="space-y-3">
                    {detail.progresses.map((progress, index) => (
                      <div key={progress.id} className="flex gap-3">
                        <div className="flex flex-col items-center">
                          <span className="mt-1 size-2 rounded-full bg-primary" />
                          <span
                            className={cn(
                              "mt-1 w-px flex-1 bg-border",
                              index === detail.progresses!.length - 1 ? "opacity-0" : "opacity-100",
                            )}
                          />
                        </div>
                        <div className="min-w-0 flex-1 pb-3">
                          <div className="flex flex-wrap items-center gap-2 text-sm text-muted-foreground">
                            <span>{progress.authorName || t("ticket.userFallback", { id: progress.authorId })}</span>
                            <span>{progress.createdAt ? formatDateTime(progress.createdAt) : "-"}</span>
                          </div>
                          <SafeRichHTML html={progress.content} className="mt-1" />
                        </div>
                      </div>
                    ))}
                  </div>
                ) : (
                  <div className="rounded-2xl border border-dashed border-[#dbe7f6] bg-[#f6f9ff] px-3 py-6 text-center text-sm text-muted-foreground shadow-inner shadow-blue-100/30">
                    {t("ticket.noProgress")}
                  </div>
                )}
              </div>
            </aside>
          </div>
        ) : (
          <div className="flex h-[360px] items-center justify-center text-sm text-muted-foreground">{t("ticket.chooseTicket")}</div>
        )}
      </ProjectDialog>

      <TicketAssignDialog
        open={canAssign && assignOpen}
        ticketId={ticket?.id ?? null}
        currentAssigneeId={ticket?.currentAssigneeId}
        onOpenChange={setAssignOpen}
        onSuccess={handleAssigned}
      />
      <EditDialog
        open={canUpdate && editOpen}
        saving={editSaving}
        itemId={ticket?.id ?? null}
        canManageTags={canViewTags}
        onOpenChange={setEditOpen}
        onSubmit={handleUpdateTicket}
      />
      <CustomerFormDialog
        open={canUpdateCustomers && customerEditOpen}
        onOpenChange={setCustomerEditOpen}
        saving={customerEditSaving}
        itemId={customerId > 0 ? customerId : null}
        onSave={handleUpdateCustomer}
      />
      <CustomerLinkOrCreateDialog
        open={canUpdate && customerLinkOpen}
        onOpenChange={setCustomerLinkOpen}
        ticketId={ticket?.id ?? null}
        onSuccess={handleCustomerLinked}
      />
      <Dialog
        open={canAddProgress && progressOpen}
        onOpenChange={(nextOpen) => {
          if (progressSaving) {
            return
          }
          setProgressOpen(nextOpen)
          if (!nextOpen) {
            setProgressContent("")
          }
        }}
      >
        <DialogContent className="max-w-2xl gap-0 p-0 sm:max-w-3xl">
          <DialogHeader className="px-6 pt-6">
            <DialogTitle>{t("ticket.addProgress")}</DialogTitle>
          </DialogHeader>
          <div className="px-6 py-4">
            <ContentEditor
              value={{ mode: "html", raw: progressContent }}
              onChange={(next) => setProgressContent(next.raw)}
              placeholder={t("ticket.progressPlaceholder")}
              disabled={progressSaving}
              allowedModes={["html"]}
              height={260}
            />
          </div>
          <DialogFooter className="mx-0 mb-0 px-6 py-4">
            <Button
              type="button"
              variant="outline"
              disabled={progressSaving}
              onClick={() => {
                setProgressOpen(false)
                setProgressContent("")
              }}
            >
              {t("ticket.cancel")}
            </Button>
            <Button type="button" disabled={progressSaving} onClick={() => void handleCreateProgress()}>
              <SendIcon className="size-3.5" />
              {progressSaving ? t("ticket.submitting") : t("ticket.submit")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  )
}

function MetadataItem({ label, value }: { label: string; value?: string | number | null }) {
  return (
    <div className="min-w-0">
      <div className="text-sm text-muted-foreground">{label}</div>
      <div className="mt-1 truncate">{metadataValue(value)}</div>
    </div>
  )
}

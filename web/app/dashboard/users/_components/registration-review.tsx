"use client"

import {
  CheckCircle2Icon,
  LoaderCircleIcon,
  SearchIcon,
  ShieldCheckIcon,
  XCircleIcon,
} from "lucide-react"
import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { toast } from "sonner"

import {
  DashboardTableShell,
  DashboardTableStateRow,
  DashboardToolbar,
} from "@/components/dashboard-page"
import {
  useDashboardPagedList,
  type DashboardListFilter,
} from "@/components/dashboard/list"
import { ListPagination } from "@/components/list-pagination"
import { OptionCombobox } from "@/components/option-combobox"
import { useAppLocale, useI18n } from "@/i18n/provider"
import { fetchRoleListAll, type AdminRole } from "@/lib/api/admin"
import {
  fetchTenantRegistrations,
  reviewTenantRegistration,
  type ReviewTenantRegistrationPayload,
  type TenantRegistrationRecord,
} from "@/lib/api/tenant-registration"
import {
  Status,
  TenantRegistrationReviewDecision,
  UserApprovalStatus,
} from "@/lib/generated/enums"
import { getRoleDisplayName } from "@/lib/role-i18n"
import { formatDateTime, generateUUID } from "@/lib/utils"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { ButtonGroup } from "@/components/ui/button-group"
import { Checkbox } from "@/components/ui/checkbox"
import {
  Drawer,
  DrawerContent,
  DrawerDescription,
  DrawerFooter,
  DrawerHeader,
  DrawerTitle,
} from "@/components/ui/drawer"
import { Input } from "@/components/ui/input"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { Textarea } from "@/components/ui/textarea"

type RegistrationReviewPanelProps = {
  canReview: boolean
  canApprove: boolean
}

type ReviewTarget = {
  record: TenantRegistrationRecord
  decision: TenantRegistrationReviewDecision
}

export function RegistrationReviewPanel({
  canReview,
  canApprove,
}: RegistrationReviewPanelProps) {
  const t = useI18n()
  const [reviewTarget, setReviewTarget] = useState<ReviewTarget | null>(null)
  const filters = useMemo<DashboardListFilter[]>(
    () => [
      {
        name: "approvalStatus",
        label: t("tenantRegistration.reviewStatus"),
        defaultValue: UserApprovalStatus.Pending,
      },
      {
        name: "username",
        label: t("tenantRegistration.username"),
        defaultValue: "",
        trim: true,
      },
      {
        name: "nickname",
        label: t("tenantRegistration.nickname"),
        defaultValue: "",
        trim: true,
      },
    ],
    [t]
  )
  const fetchList = useCallback(
    (query: Record<string, string | number | boolean | string[] | number[] | undefined>) =>
      fetchTenantRegistrations({
        approvalStatus: query.approvalStatus as UserApprovalStatus,
        username: typeof query.username === "string" ? query.username : undefined,
        nickname: typeof query.nickname === "string" ? query.nickname : undefined,
        page: Number(query.page),
        limit: Number(query.limit),
      }),
    []
  )
  const list = useDashboardPagedList<TenantRegistrationRecord>({
    filters,
    fetchList,
    loadFailed: t("tenantRegistration.reviewLoadFailed"),
  })

  function openReview(
    record: TenantRegistrationRecord,
    decision: TenantRegistrationReviewDecision
  ) {
    if (!canReview || (decision === TenantRegistrationReviewDecision.Approve && !canApprove)) {
      return
    }
    setReviewTarget({ record, decision })
  }

  return (
    <>
      <DashboardToolbar>
        <div className="w-full sm:w-44">
          <OptionCombobox
            value={String(list.draftFilters.approvalStatus ?? UserApprovalStatus.Pending)}
            options={[
              {
                value: UserApprovalStatus.Pending,
                label: t("tenantRegistration.statusPending"),
              },
              {
                value: UserApprovalStatus.Approved,
                label: t("tenantRegistration.statusApproved"),
              },
              {
                value: UserApprovalStatus.Rejected,
                label: t("tenantRegistration.statusRejected"),
              },
            ]}
            onChange={(value) => list.setDraftFilter("approvalStatus", value)}
            placeholder={t("tenantRegistration.reviewStatus")}
            searchPlaceholder={t("tenantRegistration.searchStatus")}
            emptyText={t("common.emptyOptions")}
          />
        </div>
        <div className="relative w-full sm:w-56">
          <SearchIcon className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            value={String(list.draftFilters.username ?? "")}
            onChange={(event) => list.setDraftFilter("username", event.target.value)}
            placeholder={t("tenantRegistration.searchUsername")}
            className="pl-9"
          />
        </div>
        <div className="relative w-full sm:w-56">
          <SearchIcon className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            value={String(list.draftFilters.nickname ?? "")}
            onChange={(event) => list.setDraftFilter("nickname", event.target.value)}
            placeholder={t("tenantRegistration.searchNickname")}
            className="pl-9"
          />
        </div>
        <Button variant="outline" onClick={list.applyFilters} disabled={list.loading}>
          {t("common.query")}
        </Button>
      </DashboardToolbar>

      <DashboardTableShell
        pagination={
          <ListPagination
            page={list.result.page.page}
            total={list.result.page.total}
            limit={list.result.page.limit}
            loading={list.loading}
            onPageChange={list.handlePageChange}
            onLimitChange={list.handleLimitChange}
          />
        }
      >
        <Table>
          <TableHeader className="bg-[#f6f9ff]">
            <TableRow>
              <TableHead>{t("tenantRegistration.columnApplicant")}</TableHead>
              <TableHead>{t("tenantRegistration.columnContact")}</TableHead>
              <TableHead>{t("tenantRegistration.columnSubmittedAt")}</TableHead>
              <TableHead>{t("tenantRegistration.columnReviewState")}</TableHead>
              {canReview ? (
                <TableHead className="w-[164px] text-right">
                  {t("common.actions")}
                </TableHead>
              ) : null}
            </TableRow>
          </TableHeader>
          <TableBody>
            {list.result.results.map((record) => (
              <TableRow key={record.userId}>
                <TableCell>
                  <div className="font-medium">{record.nickname || record.username}</div>
                  <div className="text-xs text-muted-foreground">{record.username}</div>
                </TableCell>
                <TableCell>
                  <div className="text-sm">{record.mobile || "-"}</div>
                  <div className="text-xs text-muted-foreground">{record.email || "-"}</div>
                </TableCell>
                <TableCell>{formatDateTime(record.createdAt)}</TableCell>
                <TableCell>
                  <div className="space-y-1">
                    <ApprovalStatusBadge status={record.approvalStatus} />
                    {record.reviewedAt ? (
                      <div className="text-xs text-muted-foreground">
                        {formatDateTime(record.reviewedAt)}
                      </div>
                    ) : null}
                    {record.approvalRemark ? (
                      <div className="max-w-64 truncate text-xs text-muted-foreground">
                        {record.approvalRemark}
                      </div>
                    ) : null}
                  </div>
                </TableCell>
                {canReview ? (
                  <TableCell className="text-right">
                    {record.approvalStatus === UserApprovalStatus.Pending ? (
                      <ButtonGroup className="ml-auto">
                        {canApprove ? (
                          <Button
                            type="button"
                            size="sm"
                            variant="outline"
                            onClick={() =>
                              openReview(record, TenantRegistrationReviewDecision.Approve)
                            }
                          >
                            <CheckCircle2Icon />
                            {t("tenantRegistration.approve")}
                          </Button>
                        ) : null}
                        <Button
                          type="button"
                          size="sm"
                          variant="outline"
                          onClick={() =>
                            openReview(record, TenantRegistrationReviewDecision.Reject)
                          }
                        >
                          <XCircleIcon />
                          {t("tenantRegistration.reject")}
                        </Button>
                      </ButtonGroup>
                    ) : (
                      <span className="text-sm text-muted-foreground">-</span>
                    )}
                  </TableCell>
                ) : null}
              </TableRow>
            ))}
            {list.loading || list.result.results.length === 0 ? (
              <DashboardTableStateRow
                colSpan={canReview ? 5 : 4}
                loading={list.loading}
                loadingText={t("tenantRegistration.reviewLoading")}
                emptyText={t("tenantRegistration.reviewEmpty")}
              />
            ) : null}
          </TableBody>
        </Table>
      </DashboardTableShell>

      <RegistrationReviewDrawer
        target={reviewTarget}
        canReview={canReview}
        canApprove={canApprove}
        onOpenChange={(open) => {
          if (!open) {
            setReviewTarget(null)
          }
        }}
        onReviewed={async () => {
          setReviewTarget(null)
          await list.loadData()
        }}
      />
    </>
  )
}

function ApprovalStatusBadge({ status }: { status: UserApprovalStatus }) {
  const t = useI18n()
  if (status === UserApprovalStatus.Approved) {
    return <Badge variant="secondary">{t("tenantRegistration.statusApproved")}</Badge>
  }
  if (status === UserApprovalStatus.Rejected) {
    return <Badge variant="destructive">{t("tenantRegistration.statusRejected")}</Badge>
  }
  return <Badge variant="outline">{t("tenantRegistration.statusPending")}</Badge>
}

type RegistrationReviewDrawerProps = {
  target: ReviewTarget | null
  canReview: boolean
  canApprove: boolean
  onOpenChange: (open: boolean) => void
  onReviewed: () => Promise<void>
}

function RegistrationReviewDrawer({
  target,
  canReview,
  canApprove,
  onOpenChange,
  onReviewed,
}: RegistrationReviewDrawerProps) {
  const canSubmit = Boolean(
    target &&
      canReview &&
      (target.decision !== TenantRegistrationReviewDecision.Approve || canApprove)
  )
  return (
    <Drawer open={canSubmit} onOpenChange={onOpenChange} direction="right">
      {target && canSubmit ? (
        <RegistrationReviewDrawerBody
          key={`${target.record.userId}-${target.decision}`}
          target={target}
          canSubmit={canSubmit}
          onOpenChange={onOpenChange}
          onReviewed={onReviewed}
        />
      ) : null}
    </Drawer>
  )
}

function RegistrationReviewDrawerBody({
  target,
  canSubmit,
  onOpenChange,
  onReviewed,
}: {
  target: ReviewTarget
  canSubmit: boolean
  onOpenChange: (open: boolean) => void
  onReviewed: () => Promise<void>
}) {
  const t = useI18n()
  const { locale } = useAppLocale()
  const approving = target.decision === TenantRegistrationReviewDecision.Approve
  const [rolesLoading, setRolesLoading] = useState(approving)
  const [roles, setRoles] = useState<AdminRole[]>([])
  const [roleKeyword, setRoleKeyword] = useState("")
  const [selectedRoleIds, setSelectedRoleIds] = useState<number[]>([])
  const [remark, setRemark] = useState("")
  const [saving, setSaving] = useState(false)
  const requestRef = useRef<{ fingerprint: string; requestId: string } | null>(null)

  useEffect(() => {
    if (!approving || !canSubmit) {
      setRolesLoading(false)
      return
    }
    let cancelled = false
    setRolesLoading(true)
    void fetchRoleListAll()
      .then((items) => {
        if (!cancelled) {
          setRoles(
            items.filter((role) => role.assignable && role.status === Status.Ok)
          )
        }
      })
      .catch((error) => {
        if (!cancelled) {
          setRoles([])
          toast.error(
            error instanceof Error
              ? error.message
              : t("tenantRegistration.rolesLoadFailed")
          )
        }
      })
      .finally(() => {
        if (!cancelled) {
          setRolesLoading(false)
        }
      })
    return () => {
      cancelled = true
    }
  }, [approving, canSubmit, t])

  const filteredRoles = useMemo(() => {
    const keyword = roleKeyword.trim().toLowerCase()
    if (!keyword) {
      return roles
    }
    return roles.filter((role) =>
      `${role.name} ${getRoleDisplayName(role.code, role.name, locale)} ${role.code}`
        .toLowerCase()
        .includes(keyword)
    )
  }, [locale, roleKeyword, roles])

  function toggleRole(roleId: number, checked: boolean) {
    setSelectedRoleIds((current) =>
      checked
        ? [...new Set([...current, roleId])]
        : current.filter((id) => id !== roleId)
    )
  }

  function requestIdFor(payload: ReviewTenantRegistrationPayload) {
    const fingerprint = JSON.stringify(payload)
    if (requestRef.current?.fingerprint !== fingerprint) {
      requestRef.current = { fingerprint, requestId: generateUUID() }
    }
    return requestRef.current.requestId
  }

  async function submitReview() {
    if (!canSubmit || saving) {
      return
    }
    const normalizedRemark = remark.trim()
    if (approving && selectedRoleIds.length === 0) {
      toast.error(t("tenantRegistration.roleRequired"))
      return
    }
    if (!approving && !normalizedRemark) {
      toast.error(t("tenantRegistration.rejectReasonRequired"))
      return
    }
    const payload: ReviewTenantRegistrationPayload = {
      userId: target.record.userId,
      decision: target.decision,
      roleIds: approving ? selectedRoleIds : [],
      remark: normalizedRemark,
    }
    setSaving(true)
    try {
      await reviewTenantRegistration(payload, requestIdFor(payload))
      toast.success(
        approving
          ? t("tenantRegistration.approveSuccess")
          : t("tenantRegistration.rejectSuccess")
      )
      await onReviewed()
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : t("tenantRegistration.reviewFailed")
      )
    } finally {
      setSaving(false)
    }
  }

  return (
    <DrawerContent className="min-w-xl">
      <DrawerHeader>
        <DrawerTitle>
          {approving
            ? t("tenantRegistration.approveTitle")
            : t("tenantRegistration.rejectTitle")}
        </DrawerTitle>
        <DrawerDescription>
          {t("tenantRegistration.reviewApplicant", {
            name: target.record.nickname || target.record.username,
            username: target.record.username,
          })}
        </DrawerDescription>
      </DrawerHeader>

      <div className="flex-1 space-y-5 overflow-y-auto px-4 pb-4">
        <div className="grid grid-cols-2 gap-3 border-y py-4 text-sm">
          <div>
            <div className="text-xs text-muted-foreground">
              {t("tenantRegistration.mobile")}
            </div>
            <div className="mt-1 font-medium">{target.record.mobile || "-"}</div>
          </div>
          <div>
            <div className="text-xs text-muted-foreground">
              {t("tenantRegistration.email")}
            </div>
            <div className="mt-1 truncate font-medium">{target.record.email || "-"}</div>
          </div>
        </div>

        {approving ? (
          <section className="space-y-3">
            <div>
              <h3 className="font-medium">{t("tenantRegistration.assignRoles")}</h3>
              <p className="mt-1 text-xs text-muted-foreground">
                {t("tenantRegistration.assignRolesHint")}
              </p>
            </div>
            <div className="relative">
              <SearchIcon className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" />
              <Input
                value={roleKeyword}
                onChange={(event) => setRoleKeyword(event.target.value)}
                placeholder={t("tenantRegistration.searchRoles")}
                className="pl-9"
              />
            </div>
            <div className="max-h-72 space-y-2 overflow-y-auto rounded-lg border p-2">
              {rolesLoading ? (
                <div className="flex min-h-32 items-center justify-center gap-2 text-muted-foreground">
                  <LoaderCircleIcon className="size-4 animate-spin" />
                  {t("tenantRegistration.rolesLoading")}
                </div>
              ) : filteredRoles.length > 0 ? (
                filteredRoles.map((role) => {
                  const checked = selectedRoleIds.includes(role.id)
                  return (
                    <label
                      key={role.id}
                      className="flex cursor-pointer items-start gap-3 rounded-md px-2 py-2 hover:bg-muted/60"
                    >
                      <Checkbox
                        checked={checked}
                        onCheckedChange={(value) => toggleRole(role.id, value === true)}
                      />
                      <span className="min-w-0 flex-1">
                        <span className="flex items-center gap-2 font-medium">
                          <ShieldCheckIcon className="size-4 text-primary" />
                          {getRoleDisplayName(role.code, role.name, locale)}
                        </span>
                        <span className="mt-1 block text-xs text-muted-foreground">
                          {role.code}
                        </span>
                      </span>
                    </label>
                  )
                })
              ) : (
                <div className="flex min-h-32 items-center justify-center text-muted-foreground">
                  {t("tenantRegistration.rolesEmpty")}
                </div>
              )}
            </div>
          </section>
        ) : null}

        <div className="space-y-2">
          <label htmlFor="registration-review-remark" className="text-sm font-medium">
            {approving
              ? t("tenantRegistration.reviewRemark")
              : t("tenantRegistration.rejectReason")}
          </label>
          <Textarea
            id="registration-review-remark"
            value={remark}
            maxLength={500}
            rows={5}
            onChange={(event) => setRemark(event.target.value)}
            placeholder={
              approving
                ? t("tenantRegistration.reviewRemarkPlaceholder")
                : t("tenantRegistration.rejectReasonPlaceholder")
            }
          />
          <div className="text-right text-xs text-muted-foreground">
            {remark.length}/500
          </div>
        </div>
      </div>

      <DrawerFooter>
        <Button type="button" variant="outline" disabled={saving} onClick={() => onOpenChange(false)}>
          {t("common.cancel")}
        </Button>
        <Button
          type="button"
          variant={approving ? "default" : "destructive"}
          disabled={!canSubmit || saving || rolesLoading}
          onClick={() => void submitReview()}
        >
          {saving ? <LoaderCircleIcon className="animate-spin" /> : null}
          {saving
            ? t("tenantRegistration.reviewing")
            : approving
              ? t("tenantRegistration.confirmApprove")
              : t("tenantRegistration.confirmReject")}
        </Button>
      </DrawerFooter>
    </DrawerContent>
  )
}

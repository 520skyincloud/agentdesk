"use client"

import { type KeyboardEvent, useCallback, useEffect, useMemo, useState } from "react"
import {
  ArrowRightLeftIcon,
  Building2Icon,
  ClipboardCheckIcon,
  KeyRoundIcon,
  MoreHorizontalIcon,
  PlusIcon,
  QrCodeIcon,
  SearchIcon,
  ShieldIcon,
  Trash2Icon,
  UserPlusIcon,
  UserRoundIcon,
  UsersRoundIcon,
} from "lucide-react"
import { toast } from "sonner"

import {
  DashboardPage,
  DashboardTableShell,
  DashboardTableStateRow,
  DashboardToolbar,
} from "@/components/dashboard-page"
import {
  useDashboardPagedList,
  type DashboardListFilter,
} from "@/components/dashboard/list"
import { ListPagination } from "@/components/list-pagination"
import {
  assignUserRoles,
  bindStoreStaffUserToAgentTeam,
  createUser,
  deleteUser,
  fetchAgentTeamsAll,
  fetchRoleListAll,
  fetchUserDetail,
  fetchUsers,
  resetUserPassword,
  updateUser,
  updateUserStatus,
  type AdminRole,
  type AdminAgentTeam,
  type AdminUser,
  type CreateAdminUserPayload,
  type ResetPasswordResult,
  type UpdateAdminUserPayload,
} from "@/lib/api/admin"
import { useAuth } from "@/components/auth-provider"
import { useConfirm } from "@/components/confirm-provider"
import { OptionCombobox } from "@/components/option-combobox"
import { Status } from "@/lib/generated/enums"
import { useAppLocale, useI18n } from "@/i18n/provider"
import { getRoleDisplayName } from "@/lib/role-i18n"
import { formatDateTime } from "@/lib/utils"
import { AssignRolesDrawer } from "./_components/assign-roles"
import { CreateUserDrawer } from "./_components/create"
import { EditDrawer } from "./_components/edit"
import { InvitationDialog } from "./_components/invitation-dialog"
import { InitialPasswordDialog } from "./_components/initial-password-dialog"
import { RegistrationReviewPanel } from "./_components/registration-review"
import { ResetPasswordDialogs } from "./_components/reset-password"
import { WxWorkProtocolBindingDialog } from "@/components/wxwork-protocol/wxwork-protocol-binding-dialog"
import { StoreModelCredentialDialog } from "@/components/store-model-credential"
import { ConversationInheritanceDialog } from "@/components/conversation-inheritance-dialog"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { ButtonGroup } from "@/components/ui/button-group"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Input } from "@/components/ui/input"
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"

export default function DashboardUsersPage() {
  const t = useI18n()
  const confirm = useConfirm()
  const { locale } = useAppLocale()
  const { session } = useAuth()
  const permissions = useMemo(() => new Set(session?.permissions ?? []), [session?.permissions])
  const canCreateUsers = permissions.has("user.create")
  const canUpdateUsers = permissions.has("user.update")
  const canDeleteUsers = permissions.has("user.delete")
  const canAssignRoles = permissions.has("user.assignRole") && permissions.has("role.view")
  const canViewInvitation = permissions.has("tenantInvite.view")
  const canRotateInvitation = permissions.has("tenantInvite.rotate")
  const canViewRegistrations = permissions.has("tenantRegistration.view")
  const canReviewRegistrations = permissions.has("tenantRegistration.review")
  const canViewAgentTeams = permissions.has("agentTeam.view")
  const canUpdateAgentTeams = permissions.has("agentTeam.update")
  const canBindWxWork = permissions.has("channel.create") && permissions.has("user.view")
  const canViewModelCredential = permissions.has("aiConfig.view")
  const canUpdateModelCredential = permissions.has("aiConfig.update")
  const canInheritConversation = permissions.has("conversation.inherit")
  const hasUserRowActions = canUpdateUsers || canDeleteUsers || canAssignRoles || canBindWxWork || canViewModelCredential || canInheritConversation
  const [agentTeams, setAgentTeams] = useState<AdminAgentTeam[]>([])
  const [assigningTeamUserId, setAssigningTeamUserId] = useState<number | null>(null)
  const [creatingOpen, setCreatingOpen] = useState(false)
  const [invitationOpen, setInvitationOpen] = useState(false)
  const [activeTab, setActiveTab] = useState<"accounts" | "registrations">("accounts")
  const [savingCreate, setSavingCreate] = useState(false)
  const [initialPassword, setInitialPassword] = useState<{
    username: string
    password: string
  } | null>(null)
  const [savingEdit, setSavingEdit] = useState(false)
  const [savingPassword, setSavingPassword] = useState(false)
  const [savingRoles, setSavingRoles] = useState(false)
  const [editingUser, setEditingUser] = useState<AdminUser | null>(null)
  const [resettingUser, setResettingUser] = useState<AdminUser | null>(null)
  const [assigningRolesUser, setAssigningRolesUser] = useState<AdminUser | null>(null)
  const [bindingWxWorkUser, setBindingWxWorkUser] = useState<AdminUser | null>(null)
  const [credentialUser, setCredentialUser] = useState<AdminUser | null>(null)
  const [handoffUser, setHandoffUser] = useState<AdminUser | null>(null)
  const [assignRoleOptions, setAssignRoleOptions] = useState<AdminRole[]>([])
  const [assignRoleIds, setAssignRoleIds] = useState<number[]>([])
  const [assignRolesLoading, setAssignRolesLoading] = useState(false)
  const [resetPasswordResult, setResetPasswordResult] =
    useState<ResetPasswordResult | null>(null)
  const [actionLoadingId, setActionLoadingId] = useState<number | null>(null)
  const filters = useMemo<DashboardListFilter[]>(
    () => [
      {
        name: "username",
        label: t("user.filterUsername"),
        defaultValue: "",
        trim: true,
      },
      { name: "roleCode", label: "用户类型", defaultValue: "all" },
      { name: "agentTeamId", label: "客服组归属", defaultValue: "all" },
    ],
    [t],
  )
  const fetchList = useCallback(
    (query: Record<string, string | number | boolean | string[] | number[] | undefined>) =>
      fetchUsers({
        username: typeof query.username === "string" ? query.username : undefined,
        roleCode: query.roleCode === "store_staff" ? "store_staff" : undefined,
        agentTeamId:
          canViewAgentTeams &&
          query.agentTeamId !== "all" &&
          query.agentTeamId !== undefined
            ? String(query.agentTeamId)
            : undefined,
        page: Number(query.page),
        limit: Number(query.limit),
      }),
    [canViewAgentTeams],
  )
  const list = useDashboardPagedList<AdminUser>({
    filters,
    fetchList,
    loadFailed: t("user.loadFailed"),
  })
  const agentTeamFilterOptions = useMemo(
    () => [
      { value: "all", label: "全部客服组" },
      { value: "0", label: "暂未分配客服组" },
      ...agentTeams.map((team) => ({ value: String(team.id), label: team.name })),
    ],
    [agentTeams],
  )
  const manageableAgentTeamOptions = useMemo(
    () => [
      { value: "0", label: "暂未分配客服组" },
      ...agentTeams
        .filter((team) => team.manageable && team.status === Status.Ok)
        .map((team) => ({ value: String(team.id), label: team.name })),
    ],
    [agentTeams],
  )

  useEffect(() => {
    if (!canViewAgentTeams) {
      setAgentTeams([])
      return
    }
    fetchAgentTeamsAll()
      .then(setAgentTeams)
      .catch((error) => toast.error(error instanceof Error ? error.message : "加载客服组失败"))
  }, [canViewAgentTeams])

  function applyFilters() {
    list.applyFilters()
  }

  function handleFilterKeyDown(event: KeyboardEvent<HTMLInputElement>) {
    if (event.key !== "Enter") {
      return
    }

    event.preventDefault()
    applyFilters()
  }

  function openEditDrawer(user: AdminUser) {
    if (!canEditUser(user)) {
      return
    }
    setEditingUser(user)
  }

  function canEditUser(user: AdminUser | null) {
    return Boolean(
      user && canUpdateUsers && (user.manageable || user.id === session?.user.id)
    )
  }

  function openCreateDrawer() {
    if (!canCreateUsers) {
      return
    }
    setCreatingOpen(true)
  }

  function openInvitationDialog() {
    if (!canViewInvitation) {
      return
    }
    setInvitationOpen(true)
  }

  async function openAssignRolesDrawer(user: AdminUser) {
    if (!canAssignRoles || !user.manageable) {
      return
    }
    setActionLoadingId(user.id)
    setAssigningRolesUser(user)
    setAssignRolesLoading(true)
    try {
      const [roles, userDetail] = await Promise.all([
        fetchRoleListAll(),
        fetchUserDetail(user.id),
      ])
      setAssignRoleOptions(roles)
      setAssignRoleIds((userDetail.roles || []).map((role) => role.id))
    } catch (error) {
      setAssigningRolesUser(null)
      toast.error(error instanceof Error ? error.message : t("user.loadRoleAssignFailed"))
    } finally {
      setAssignRolesLoading(false)
      setActionLoadingId(null)
    }
  }

  function handlePageChange(nextPage: number) {
    list.handlePageChange(nextPage)
  }

  function handleLimitChange(nextLimit: number) {
    list.handleLimitChange(nextLimit)
  }

  function handleEditDrawerOpenChange(open: boolean) {
    if (savingEdit) {
      return
    }
    if (!open) {
      setEditingUser(null)
    }
  }

  function handleCreateDrawerOpenChange(open: boolean) {
    if (savingCreate) {
      return
    }
    if (!open) {
      setCreatingOpen(false)
    }
  }

  async function handleCreateUser(payload: CreateAdminUserPayload) {
    if (
      !canCreateUsers ||
      savingCreate ||
      (payload.roleIds.length > 0 && !canAssignRoles)
    ) {
      return
    }

    setSavingCreate(true)
    try {
      const result = await createUser(payload)
      toast.success(t("user.created", { username: result.user.username }))
      setCreatingOpen(false)
      setInitialPassword({
        username: result.user.username,
        password: result.password,
      })
      await list.loadData()
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("user.createFailed"))
    } finally {
      setSavingCreate(false)
    }
  }

  function handleAssignRolesOpenChange(open: boolean) {
    if (savingRoles) {
      return
    }
    if (!open) {
      setAssigningRolesUser(null)
      setAssignRoleOptions([])
      setAssignRoleIds([])
    }
  }

  async function handleSaveUser(payload: UpdateAdminUserPayload) {
    if (
      !editingUser ||
      !canEditUser(editingUser) ||
      payload.id !== editingUser.id ||
      savingEdit
    ) {
      return
    }

    setSavingEdit(true)
    try {
      await updateUser(payload)
      toast.success(t("user.updated", { username: editingUser?.username || t("user.fallbackUser") }))
      setEditingUser(null)
      await list.loadData()
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("user.updateFailed"))
    } finally {
      setSavingEdit(false)
    }
  }

  async function handleAssignRoles(roleIds: number[], storeId: number) {
    if (
      !canAssignRoles ||
      !assigningRolesUser?.manageable ||
      savingRoles
    ) {
      return
    }

    setSavingRoles(true)
    try {
      await assignUserRoles(assigningRolesUser.id, roleIds, storeId)
      toast.success(t("user.rolesUpdated", { username: assigningRolesUser.username }))
      setAssigningRolesUser(null)
      setAssignRoleOptions([])
      setAssignRoleIds([])
      await list.loadData()
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("user.saveRolesFailed"))
    } finally {
      setSavingRoles(false)
    }
  }

  function openResetDrawer(user: AdminUser) {
    if (!canUpdateUsers || !user.manageable) {
      return
    }
    setResetPasswordResult(null)
    setResettingUser(user)
  }

  function handleResetDrawerOpenChange(open: boolean) {
    if (savingPassword) {
      return
    }
    if (!open) {
      setResetPasswordResult(null)
      setResettingUser(null)
    }
  }

  async function handleResetPassword() {
    if (!canUpdateUsers || !resettingUser?.manageable || savingPassword) {
      return
    }

    setSavingPassword(true)
    try {
      const result = await resetUserPassword(resettingUser.id)
      setResetPasswordResult(result)
      toast.success(t("user.passwordReset", { username: resettingUser.username }))
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("user.resetPasswordFailed"))
    } finally {
      setSavingPassword(false)
    }
  }

  async function handleToggleStatus(user: AdminUser) {
    if (!canUpdateUsers || !user.manageable || actionLoadingId != null) {
      return
    }
    setActionLoadingId(user.id)
    try {
      const nextStatus = user.status === Status.Ok ? Status.Disabled : Status.Ok
      await updateUserStatus(user.id, nextStatus)
      toast.success(
        t("user.statusUpdated", {
          username: user.username,
          status: nextStatus === Status.Ok ? t("user.enabled") : t("user.disabled"),
        })
      )
      await list.loadData()
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("user.statusUpdateFailed"))
    } finally {
      setActionLoadingId(null)
    }
  }

  async function handleBindAgentTeam(user: AdminUser, value: string) {
    if (
      !canViewAgentTeams ||
      !canUpdateAgentTeams ||
      !user.storeStaff?.bindingId
    ) {
      return
    }
    const teamId = Number(value || 0)
    const currentTeamId = user.storeStaff?.agentTeamId || 0
    if (teamId === currentTeamId || assigningTeamUserId != null) {
      return
    }
    if (currentTeamId > 0) {
      const targetName = agentTeams.find((team) => team.id === teamId)?.name || "暂未分配客服组"
      if (!window.confirm(`确认将“${user.nickname || user.username}”调整到“${targetName}”？其绑定的企微员工号会同步更新。`)) {
        return
      }
    }
    setAssigningTeamUserId(user.id)
    try {
      await bindStoreStaffUserToAgentTeam({ userId: user.id, teamId })
      toast.success(teamId > 0 ? "客服组归属已更新" : "已设为暂未分配客服组")
      await list.loadData()
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "更新客服组归属失败")
    } finally {
      setAssigningTeamUserId(null)
    }
  }

  async function handleDeleteUser(user: AdminUser) {
    if (!canDeleteUsers || !user.manageable || actionLoadingId != null) {
      return
    }
    const confirmed = await confirm({
      title: t("user.confirmDeleteTitle"),
      description: t("user.confirmDeleteDescription", {
        username: user.username,
      }),
      confirmText: t("user.confirmDelete"),
      variant: "destructive",
    })
    if (!confirmed) {
      return
    }
    setActionLoadingId(user.id)
    try {
      await deleteUser(user.id)
      toast.success(t("user.deleted", { username: user.username }))
      await list.loadData()
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("user.deleteFailed"))
    } finally {
      setActionLoadingId(null)
    }
  }

  function registrationSourceLabel(source: string) {
    switch (source) {
      case "platform_created":
        return t("tenantRegistration.sourcePlatform")
      case "tenant_created":
        return t("tenantRegistration.sourceTenant")
      case "invitation":
        return t("tenantRegistration.sourceInvitation")
      case "wxwork":
        return t("tenantRegistration.sourceWxWork")
      case "oidc":
        return t("tenantRegistration.sourceOIDC")
      default:
        return t("tenantRegistration.sourceLegacy")
    }
  }

  function approvalStatusLabel(status: string) {
    switch (status) {
      case "pending":
        return t("tenantRegistration.statusPending")
      case "rejected":
        return t("tenantRegistration.statusRejected")
      default:
        return t("tenantRegistration.statusApproved")
    }
  }

  function canBindUserWxWork(user: AdminUser) {
    if (!canBindWxWork || !(user.roles || []).some((role) => role.code === "store_staff")) {
      return false
    }
    if (!user.storeStaff?.wxWorkInstanceId) return true
    return ["login_qrcode", "remote_setup"].includes(user.storeStaff.wxWorkHealthStatus || "")
  }

  return (
    <>
      <DashboardPage>
        <Tabs
          value={activeTab}
          onValueChange={(value) =>
            setActiveTab(value === "registrations" ? "registrations" : "accounts")
          }
          className="gap-5"
        >
          {canViewRegistrations ? (
            <TabsList className="border border-[#dbe7f6] bg-[#f6f9ff] p-1 shadow-inner shadow-blue-100/40">
              <TabsTrigger value="accounts">
                <UsersRoundIcon />
                {t("tenantRegistration.accountsTab")}
              </TabsTrigger>
              <TabsTrigger value="registrations">
                <ClipboardCheckIcon />
                {t("tenantRegistration.reviewTab")}
              </TabsTrigger>
            </TabsList>
          ) : null}

          {activeTab === "accounts" || !canViewRegistrations ? (
            <>
              <DashboardToolbar
                actions={
                  canCreateUsers || canViewInvitation ? (
                    <>
                      {canViewInvitation ? (
                        <Button
                          variant="outline"
                          onClick={openInvitationDialog}
                          disabled={list.loading}
                        >
                          <UserPlusIcon />
                          {t("tenantRegistration.inviteRegistration")}
                        </Button>
                      ) : null}
                      {canCreateUsers ? (
                        <Button onClick={openCreateDrawer} disabled={list.loading}>
                          <PlusIcon />
                          {t("user.addUser")}
                        </Button>
                      ) : null}
                    </>
                  ) : null
                }
              >
          <div className="relative w-full sm:w-72">
            <SearchIcon className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" />
            <Input
              value={String(list.draftFilters.username ?? "")}
              onChange={(event) =>
                list.setDraftFilter("username", event.target.value)
              }
              onKeyDown={handleFilterKeyDown}
              placeholder={t("user.filterUsername")}
              className="pl-9"
            />
          </div>
          <div className="w-full sm:w-44">
            <OptionCombobox
              value={String(list.draftFilters.roleCode ?? "all")}
              options={[
                { value: "all", label: "全部用户类型" },
                { value: "store_staff", label: "门店员工" },
              ]}
              onChange={(value) => list.setDraftFilter("roleCode", value)}
              placeholder="用户类型"
              searchPlaceholder="搜索用户类型"
              emptyText="无匹配类型"
            />
          </div>
          {canViewAgentTeams ? (
            <div className="w-full sm:w-48">
              <OptionCombobox
                value={String(list.draftFilters.agentTeamId ?? "all")}
                options={agentTeamFilterOptions}
                onChange={(value) => list.setDraftFilter("agentTeamId", value)}
                placeholder="客服组归属"
                searchPlaceholder="搜索客服组"
                emptyText="无匹配客服组"
              />
            </div>
          ) : null}
          <Button variant="outline" onClick={applyFilters} disabled={list.loading}>
            {t("user.query")}
          </Button>
              </DashboardToolbar>
              <DashboardTableShell
          pagination={
            <ListPagination
              page={list.result.page.page}
              total={list.result.page.total}
              limit={list.result.page.limit}
              loading={list.loading}
              onPageChange={handlePageChange}
              onLimitChange={handleLimitChange}
            />
          }
              >
            <Table>
              <TableHeader className="bg-[#f6f9ff]">
                <TableRow>
                  <TableHead>{t("user.columnUser")}</TableHead>
                  <TableHead>门店与客服组</TableHead>
                  <TableHead>{t("user.columnRoles")}</TableHead>
                  <TableHead>{t("tenantRegistration.columnSourceReview")}</TableHead>
                  <TableHead>{t("user.columnStatus")}</TableHead>
                  <TableHead>{t("user.columnLastLogin")}</TableHead>
                  <TableHead>{t("user.columnContact")}</TableHead>
                  {hasUserRowActions ? (
                    <TableHead className="w-[92px] text-right">{t("user.columnActions")}</TableHead>
                  ) : null}
                </TableRow>
              </TableHeader>
              <TableBody>
                {list.result.results.map((item) => (
                  <TableRow key={item.id}>
                    <TableCell>
                      <div className="flex items-center gap-3">
                        <div className="flex size-10 items-center justify-center rounded-2xl border border-[#dbe7f6] bg-[#f6f9ff] text-primary shadow-inner shadow-blue-100/30">
                          <UserRoundIcon className="size-4" />
                        </div>
                        <div>
                          <div className="font-medium">{item.nickname || item.username}</div>
                          <div className="text-xs text-muted-foreground">{item.username}</div>
                        </div>
                      </div>
                    </TableCell>
                    <TableCell>
                      {(item.roles || []).some((role) => role.code === "store_staff") ? (
                        <div className="min-w-52 space-y-2">
                          <div className="flex items-start gap-2 text-xs text-muted-foreground">
                            <Building2Icon className="mt-0.5 size-3.5 shrink-0" />
                            <div className="min-w-0">
                              <div className="truncate text-sm font-medium text-foreground">
                                {item.storeStaff?.storeName || "尚未绑定门店"}
                              </div>
                              <div className="truncate">
                                {item.storeStaff?.wxWorkInstanceId && ["login_qrcode", "remote_setup"].includes(item.storeStaff.wxWorkHealthStatus || "")
                                  ? "企微：绑定中"
                                  : item.storeStaff?.wxWorkInstanceId
                                  ? `企微：${[
                                      item.storeStaff.wxWorkEmployeeName,
                                      item.storeStaff.wxWorkEmployeeId,
                                    ]
                                      .filter(Boolean)
                                      .join(" · ") || `实例 #${item.storeStaff.wxWorkInstanceId}`}`
                                  : "暂未绑定企微员工号"}
                              </div>
                            </div>
                          </div>
                          {canViewAgentTeams ? (
                            canUpdateAgentTeams && item.storeStaff?.bindingId ? (
                              <OptionCombobox
                                value={String(item.storeStaff.agentTeamId || 0)}
                                options={manageableAgentTeamOptions}
                                onChange={(value) => void handleBindAgentTeam(item, value)}
                                placeholder={item.storeStaff.agentTeamName || "暂未分配客服组"}
                                searchPlaceholder="搜索客服组"
                                emptyText="无可管理客服组"
                                disabled={
                                  assigningTeamUserId === item.id ||
                                  (item.storeStaff.agentTeamId > 0 &&
                                    !agentTeams.find(
                                      (team) =>
                                        team.id === item.storeStaff?.agentTeamId && team.manageable,
                                    ))
                                }
                              />
                            ) : (
                              <Badge variant="outline">
                                {item.storeStaff?.agentTeamName || "暂未分配客服组"}
                              </Badge>
                            )
                          ) : null}
                        </div>
                      ) : (
                        <span className="text-sm text-muted-foreground">-</span>
                      )}
                    </TableCell>
                    <TableCell>
                      <div className="flex flex-wrap gap-1.5">
                        {(item.roles || []).length > 0 ? (
                          item.roles?.map((role) => (
                            <Badge key={role.id} variant="outline">
                              <ShieldIcon className="size-3" />
                              {getRoleDisplayName(role.code, role.name, locale)}
                            </Badge>
                          ))
                        ) : (
                          <span className="text-sm text-muted-foreground">{t("user.unassigned")}</span>
                        )}
                      </div>
                    </TableCell>
                    <TableCell>
                      <div className="space-y-1.5">
                        <div className="text-sm">
                          {registrationSourceLabel(item.registrationSource)}
                        </div>
                        <Badge
                          variant={
                            item.approvalStatus === "rejected"
                              ? "destructive"
                              : item.approvalStatus === "pending"
                                ? "outline"
                                : "secondary"
                          }
                        >
                          {approvalStatusLabel(item.approvalStatus)}
                        </Badge>
                      </div>
                    </TableCell>
                    <TableCell>
                      <Badge variant={item.status === Status.Ok ? "secondary" : "outline"}>
                        {item.status === Status.Ok ? t("user.enabled") : t("user.disabled")}
                      </Badge>
                      {item.isSystem ? (
                        <Badge variant="outline" className="ml-2">
                          {t("user.system")}
                        </Badge>
                      ) : null}
                    </TableCell>
                    <TableCell>
                      <div className="text-sm">{formatDateTime(item.lastLoginAt)}</div>
                      <div className="text-xs text-muted-foreground">
                        {item.lastLoginIp || "-"}
                      </div>
                    </TableCell>
                    <TableCell>
                      <div className="text-sm">{item.mobile || "-"}</div>
                      <div className="text-xs text-muted-foreground">
                        {item.email || "-"}
                      </div>
                    </TableCell>
                    {hasUserRowActions ? (
                      <TableCell className="text-right">
                        <ButtonGroup className="ml-auto">
                          {canEditUser(item) ? (
                            <Button
                              variant="outline"
                              size="sm"
                              onClick={() => openEditDrawer(item)}
                              disabled={actionLoadingId === item.id}
                            >
                              {t("user.edit")}
                            </Button>
                          ) : null}
                          {item.manageable &&
                          (canAssignRoles || canUpdateUsers || canDeleteUsers || canBindUserWxWork(item) ||
                            (canViewModelCredential && Boolean(item.storeStaff?.storeId)) ||
                            (canInheritConversation && Boolean(item.storeStaff?.bindingId && item.storeStaff?.storeId))) ? (
                            <DropdownMenu>
                          <DropdownMenuTrigger
                            render={<Button variant="outline" size="icon-sm" />}
                            aria-label={t("user.moreActions", { username: item.username })}
                          >
                            <MoreHorizontalIcon />
                          </DropdownMenuTrigger>
                          <DropdownMenuContent align="end" className="w-40 min-w-40">
                            {canViewModelCredential && item.storeStaff?.storeId ? (
                              <DropdownMenuItem onClick={() => setCredentialUser(item)}>
                                <KeyRoundIcon />
                                模型凭据
                              </DropdownMenuItem>
                            ) : null}
                            {canBindUserWxWork(item) ? (
                              <DropdownMenuItem onClick={() => setBindingWxWorkUser(item)}>
                                <QrCodeIcon />
                                {item.storeStaff?.wxWorkInstanceId ? "继续企微绑定" : "绑定企微员工号"}
                              </DropdownMenuItem>
                            ) : null}
                            {canInheritConversation && item.storeStaff?.bindingId && item.storeStaff?.storeId ? (
                              <DropdownMenuItem onClick={() => setHandoffUser(item)}>
                                <ArrowRightLeftIcon />
                                批量会话交接
                              </DropdownMenuItem>
                            ) : null}
                            {canAssignRoles ? (
                              <DropdownMenuItem
                                onClick={() => void openAssignRolesDrawer(item)}
                                disabled={actionLoadingId === item.id}
                              >
                                <ShieldIcon />
                                {actionLoadingId === item.id
                                  ? t("user.processing")
                                  : t("user.assignRoles")}
                              </DropdownMenuItem>
                            ) : null}
                            {canUpdateUsers ? (
                              <>
                                <DropdownMenuItem
                                  onClick={() => openResetDrawer(item)}
                                  disabled={actionLoadingId === item.id}
                                >
                                  <KeyRoundIcon />
                                  {t("user.resetPassword")}
                                </DropdownMenuItem>
                                <DropdownMenuItem
                                  onClick={() => void handleToggleStatus(item)}
                                  disabled={actionLoadingId === item.id}
                                >
                                  <ShieldIcon />
                                  {actionLoadingId === item.id
                                    ? t("user.processing")
                                    : item.status === Status.Ok
                                      ? t("user.disabled")
                                      : t("user.enabled")}
                                </DropdownMenuItem>
                              </>
                            ) : null}
                            {canDeleteUsers ? (
                              <DropdownMenuItem
                                onClick={() => void handleDeleteUser(item)}
                                disabled={actionLoadingId === item.id}
                                className="text-destructive focus:text-destructive"
                              >
                                <Trash2Icon />
                                {t("user.delete")}
                              </DropdownMenuItem>
                            ) : null}
                          </DropdownMenuContent>
                            </DropdownMenu>
                          ) : null}
                        </ButtonGroup>
                      </TableCell>
                    ) : null}
                  </TableRow>
                ))}
                {list.loading || list.result.results.length === 0 ? (
                  <DashboardTableStateRow
                    colSpan={hasUserRowActions ? 8 : 7}
                    loading={list.loading}
                    loadingText={t("user.loadingRows")}
                    emptyText={t("user.emptyRows")}
                  />
                ) : null}
              </TableBody>
            </Table>
              </DashboardTableShell>
            </>
          ) : (
            <RegistrationReviewPanel
              canReview={canReviewRegistrations}
              canApprove={canReviewRegistrations && canAssignRoles}
            />
          )}
        </Tabs>
      </DashboardPage>
      <CreateUserDrawer
        open={creatingOpen && canCreateUsers}
        saving={savingCreate}
        canAssignRoles={canAssignRoles}
        onOpenChange={handleCreateDrawerOpenChange}
        onSubmit={handleCreateUser}
      />
      <InitialPasswordDialog
        open={!!initialPassword}
        username={initialPassword?.username ?? ""}
        password={initialPassword?.password ?? ""}
        onOpenChange={(open) => {
          if (!open) {
            setInitialPassword(null)
          }
        }}
      />
      <EditDrawer
        open={canEditUser(editingUser)}
        saving={savingEdit}
        itemId={editingUser?.id ?? null}
        onOpenChange={handleEditDrawerOpenChange}
        onSubmit={handleSaveUser}
      />
      <ResetPasswordDialogs
        open={Boolean(resettingUser?.manageable && canUpdateUsers)}
        saving={savingPassword}
        item={resettingUser}
        password={resetPasswordResult?.password || ""}
        onOpenChange={handleResetDrawerOpenChange}
        onConfirm={handleResetPassword}
      />
      <AssignRolesDrawer
        open={Boolean(assigningRolesUser?.manageable && canAssignRoles)}
        saving={savingRoles}
        loading={assignRolesLoading}
        item={assigningRolesUser}
        roles={assignRoleOptions}
        selectedRoleIds={assignRoleIds}
        onOpenChange={handleAssignRolesOpenChange}
        onSubmit={handleAssignRoles}
      />
      <InvitationDialog
        open={invitationOpen && canViewInvitation}
        canRotate={canRotateInvitation}
        onOpenChange={setInvitationOpen}
      />
      <WxWorkProtocolBindingDialog
        open={Boolean(bindingWxWorkUser && canBindWxWork)}
        user={bindingWxWorkUser}
        onOpenChange={(open) => {
          if (!open) setBindingWxWorkUser(null)
        }}
        onChanged={async () => {
          await list.loadData()
        }}
      />
      <StoreModelCredentialDialog
        open={Boolean(credentialUser?.storeStaff?.storeId && canViewModelCredential)}
        tenantId={credentialUser?.tenantId ?? 0}
        storeId={credentialUser?.storeStaff?.storeId ?? 0}
        storeStaffBindingId={credentialUser?.storeStaff?.bindingId ?? 0}
        storeName={credentialUser?.storeStaff?.storeName ?? ""}
        canUpdate={canUpdateModelCredential}
        onOpenChange={(open) => {
          if (!open) setCredentialUser(null)
        }}
        onChanged={() => {
          void list.loadData()
        }}
      />
      <ConversationInheritanceDialog
        open={Boolean(
          handoffUser?.storeStaff?.storeId &&
            handoffUser.storeStaff.bindingId &&
            canInheritConversation
        )}
        sourceStoreId={handoffUser?.storeStaff?.storeId ?? 0}
        sourceStoreStaffBindingId={handoffUser?.storeStaff?.bindingId ?? 0}
        onOpenChange={(open) => {
          if (!open) setHandoffUser(null)
        }}
        onSuccess={async () => {
          setHandoffUser(null)
          await list.loadData()
        }}
      />
    </>
  )
}

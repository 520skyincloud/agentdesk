"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import { zodResolver } from "@hookform/resolvers/zod";
import {
  CheckIcon,
  ChevronsUpDownIcon,
  PlusIcon,
  SearchIcon,
  UsersRoundIcon,
  XIcon,
} from "lucide-react";
import { Controller, type Resolver, useForm } from "react-hook-form";
import { toast } from "sonner";
import { z } from "zod/v4";

import { OptionCombobox } from "@/components/option-combobox";
import { useAuth } from "@/components/auth-provider";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from "@/components/ui/command";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  Field,
  FieldContent,
  FieldError,
  FieldLabel,
} from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover";
import { Textarea } from "@/components/ui/textarea";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { useI18n } from "@/i18n/provider";
import {
  type AdminAgentTeam,
  type AdminUser,
  type CreateAdminAgentTeamPayload,
  fetchAgentTeam,
  fetchUsersAll,
} from "@/lib/api/admin";
import { AgentTeamDispatchMode, Status } from "@/lib/generated/enums";
import { cn } from "@/lib/utils";

type TFunction = (key: string, values?: Record<string, string | number>) => string;

type TeamEditDialogProps = {
  open: boolean;
  saving: boolean;
  itemId: number | null;
  canChangeLeader: boolean;
  onOpenChange: (open: boolean) => void;
  onSubmit: (payload: CreateAdminAgentTeamPayload) => Promise<void>;
};

type EditForm = {
  name: string;
  leaderUserId: string;
  storeStaffUserIds: string;
  dispatchMode: string;
  status: string;
  description: string;
  remark: string;
};

type AssignmentFilter = "available" | "unassigned" | "other" | "all";
type BindingFilter = "all" | "bound" | "unbound";

const emptyForm: EditForm = {
  name: "",
  leaderUserId: "0",
  storeStaffUserIds: "",
  dispatchMode: AgentTeamDispatchMode.Rule,
  status: String(Status.Ok),
  description: "",
  remark: "",
};

function createEditFormSchema(t: TFunction) {
  return z.object({
    name: z.string().trim().min(1, t("agentProfile.teamNameRequired")),
    leaderUserId: z.string().trim().regex(/^\d+$/, t("agentProfile.leaderInvalid")),
    storeStaffUserIds: z.string().trim(),
    dispatchMode: z.enum([
      AgentTeamDispatchMode.Manual,
      AgentTeamDispatchMode.Rule,
    ]),
    status: z.enum([String(Status.Ok), String(Status.Disabled)], {
      message: t("agentProfile.teamStatusRequired"),
    }),
    description: z.string().trim(),
    remark: z.string().trim(),
  });
}

function getStatusOptions(t: TFunction) {
  return [
    { value: String(Status.Ok), label: t("agentProfile.enabled") },
    { value: String(Status.Disabled), label: t("agentProfile.disabled") },
  ];
}

function parseIdList(value: string) {
  return value
    .split(/[,，\s]+/)
    .map((part) => Number(part.trim()))
    .filter((id, index, ids) => Number.isFinite(id) && id > 0 && ids.indexOf(id) === index);
}

function buildForm(item: AdminAgentTeam | null): EditForm {
  if (!item) {
    return emptyForm;
  }
  return {
    name: item.name,
    leaderUserId: String(item.leaderUserId),
    storeStaffUserIds: (item.storeStaffUserIds || []).join(","),
    dispatchMode:
      item.dispatchMode === AgentTeamDispatchMode.Manual
        ? AgentTeamDispatchMode.Manual
        : AgentTeamDispatchMode.Rule,
    status: String(item.status),
    description: item.description || "",
    remark: item.remark || "",
  };
}

function buildPayload(form: EditForm): CreateAdminAgentTeamPayload {
  return {
    name: form.name.trim(),
    leaderUserId: Number(form.leaderUserId),
    storeStaffUserIds: parseIdList(form.storeStaffUserIds),
    storeScopeIds: [],
    wxWorkInstanceScopeIds: [],
    dispatchMode: form.dispatchMode,
    status: Number(form.status),
    description: form.description.trim(),
    remark: form.remark.trim(),
  };
}

function staffName(user: AdminUser) {
  return user.nickname || user.username;
}

function staffSearchText(user: AdminUser) {
  return [
    user.nickname,
    user.username,
    user.storeStaff?.storeName,
    user.storeStaff?.wxWorkEmployeeName,
    user.storeStaff?.wxWorkEmployeeId,
    user.storeStaff?.agentTeamName,
  ]
    .filter(Boolean)
    .join(" ")
    .toLowerCase();
}

function StoreStaffAccountRow({
  user,
  itemId,
  action,
  actionLabel,
  actionIcon,
  disabled = false,
  t,
}: {
  user: AdminUser;
  itemId: number | null;
  action: () => void;
  actionLabel: string;
  actionIcon: "add" | "remove";
  disabled?: boolean;
  t: TFunction;
}) {
  const teamID = user.storeStaff?.agentTeamId || 0;
  const teamLabel =
    teamID > 0
      ? teamID === itemId
        ? t("agentProfile.staffBelongsCurrentTeam")
        : user.storeStaff?.agentTeamName || t("agentProfile.staffBelongsOtherTeam")
      : t("agentProfile.staffUnassigned");
  return (
    <div className={cn("flex min-h-16 items-center gap-3 border-b px-3 py-2 last:border-b-0", disabled && "opacity-55")}>
      <div className="min-w-0 flex-1">
        <div className="flex min-w-0 flex-wrap items-center gap-2">
          <span className="truncate text-sm font-medium">{staffName(user)}</span>
          <Badge variant={teamID === itemId && itemId != null ? "secondary" : "outline"} className="shrink-0">
            {teamLabel}
          </Badge>
        </div>
        <div className="mt-1 truncate text-xs text-muted-foreground">
          {[
            user.storeStaff?.storeName || t("agentProfile.storeUnbound"),
            user.storeStaff?.wxWorkEmployeeName,
            user.storeStaff?.wxWorkEmployeeId,
          ]
            .filter(Boolean)
            .join(" · ")}
        </div>
      </div>
      <Button
        type="button"
        variant="ghost"
        size="icon-sm"
        onClick={action}
        disabled={disabled}
        aria-label={actionLabel}
        title={actionLabel}
      >
        {actionIcon === "add" ? <PlusIcon /> : <XIcon />}
      </Button>
    </div>
  );
}

export function EditDialog({
  open,
  saving,
  itemId,
  canChangeLeader,
  onOpenChange,
  onSubmit,
}: TeamEditDialogProps) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      {open ? (
        <TeamEditDialogBody
          key={itemId ? `edit-${itemId}` : "create"}
          itemId={itemId}
          saving={saving}
          canChangeLeader={canChangeLeader}
          onOpenChange={onOpenChange}
          onSubmit={onSubmit}
        />
      ) : null}
    </Dialog>
  );
}

type TeamEditDialogBodyProps = Omit<TeamEditDialogProps, "open">;

function TeamEditDialogBody({
  saving,
  itemId,
  canChangeLeader,
  onOpenChange,
  onSubmit,
}: TeamEditDialogBodyProps) {
  const t = useI18n();
  const { session } = useAuth();
  const canViewUsers = session?.permissions.includes("user.view") ?? false;
  const [leaders, setLeaders] = useState<AdminUser[]>([]);
  const [storeStaffUsers, setStoreStaffUsers] = useState<AdminUser[]>([]);
  const [userSelectOpen, setUserSelectOpen] = useState(false);
  const [staffDialogOpen, setStaffDialogOpen] = useState(false);
  const [staffKeyword, setStaffKeyword] = useState("");
  const [selectedStaffKeyword, setSelectedStaffKeyword] = useState("");
  const [assignmentFilter, setAssignmentFilter] = useState<AssignmentFilter>("all");
  const [bindingFilter, setBindingFilter] = useState<BindingFilter>("all");
  const [loading, setLoading] = useState(false);
  const [staffLoading, setStaffLoading] = useState(false);
  const leaderOptions = leaders.map((user) => ({
    value: String(user.id),
    label: `${user.nickname || user.username} (${user.username})`,
  }));
  const statusOptions = useMemo(() => getStatusOptions(t), [t]);
  const editFormSchema = useMemo(() => createEditFormSchema(t), [t]);
  const editFormResolver = useMemo(
    () => zodResolver(editFormSchema) as Resolver<EditForm>,
    [editFormSchema],
  );
  const form = useForm<EditForm>({
    resolver: editFormResolver,
    defaultValues: emptyForm,
  });
  const {
    control,
    handleSubmit,
    reset,
    register,
    setValue,
    watch,
    formState: { errors },
  } = form;
  const selectedValue = watch("storeStaffUserIds");
  const selectedUserIDs = useMemo(() => parseIdList(selectedValue || ""), [selectedValue]);
  const selectedUserIDSet = useMemo(() => new Set(selectedUserIDs), [selectedUserIDs]);
  const selectedUsers = useMemo(
    () =>
      selectedUserIDs
        .map((id) => storeStaffUsers.find((user) => user.id === id))
        .filter((user): user is AdminUser => Boolean(user)),
    [selectedUserIDs, storeStaffUsers],
  );
  const selectedStoreCount = useMemo(
    () => new Set(selectedUsers.map((user) => user.storeStaff?.storeId).filter(Boolean)).size,
    [selectedUsers],
  );

  const loadUsers = useCallback(async () => {
    if (!canViewUsers) {
      setLeaders([]);
      setStoreStaffUsers([]);
      return;
    }
    setStaffLoading(true);
    try {
      const [leaderData, staffData] = await Promise.all([
        fetchUsersAll({ roleCode: "cs_team_leader" }),
        fetchUsersAll({ roleCode: "store_staff" }),
      ]);
      setLeaders(leaderData);
      setStoreStaffUsers(staffData);
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("agentProfile.loadUsersFailed"));
    } finally {
      setStaffLoading(false);
    }
  }, [canViewUsers, t]);

  useEffect(() => {
    async function loadDetail() {
      if (!itemId) {
        reset(emptyForm);
        return;
      }
      setLoading(true);
      try {
        const data = await fetchAgentTeam(itemId);
        reset(buildForm(data));
      } catch (error) {
        toast.error(error instanceof Error ? error.message : t("agentProfile.loadTeamDetailFailed"));
      } finally {
        setLoading(false);
      }
    }
    void loadDetail();
  }, [itemId, reset, t]);

  useEffect(() => {
    void loadUsers();
  }, [loadUsers]);

  const assignmentOptions = useMemo(
    () => [
      { value: "available", label: t("agentProfile.staffAssignmentAvailable") },
      { value: "unassigned", label: t("agentProfile.staffOwnershipUnassigned") },
      { value: "other", label: t("agentProfile.staffOwnershipOther") },
      { value: "all", label: t("agentProfile.staffOwnershipAll") },
    ],
    [t],
  );
  const bindingOptions = useMemo(
    () => [
      { value: "all", label: t("agentProfile.allBindingStatuses") },
      { value: "bound", label: t("agentProfile.storeBound") },
      { value: "unbound", label: t("agentProfile.storeUnbound") },
    ],
    [t],
  );
  const filteredAvailableStoreStaffUsers = useMemo(() => {
    const keyword = staffKeyword.trim().toLowerCase();
    return [...storeStaffUsers]
      .filter((user) => !selectedUserIDSet.has(user.id))
      .filter((user) => !keyword || staffSearchText(user).includes(keyword))
      .filter((user) => {
        const teamID = user.storeStaff?.agentTeamId || 0;
        if (assignmentFilter === "unassigned") {
          return teamID === 0;
        }
        if (assignmentFilter === "available") {
          return teamID === 0 || teamID === itemId;
        }
        if (assignmentFilter === "other") {
          return teamID > 0 && teamID !== itemId;
        }
        return true;
      })
      .filter((user) => {
        if (bindingFilter === "bound") {
          return Boolean(user.storeStaff?.bindingId);
        }
        if (bindingFilter === "unbound") {
          return !user.storeStaff?.bindingId;
        }
        return true;
      })
      .sort((a, b) => {
        const boundDiff = Number(Boolean(b.storeStaff?.bindingId)) - Number(Boolean(a.storeStaff?.bindingId));
        return boundDiff || (a.storeStaff?.storeName || "").localeCompare(b.storeStaff?.storeName || "") || a.id - b.id;
      });
  }, [assignmentFilter, bindingFilter, itemId, selectedUserIDSet, staffKeyword, storeStaffUsers]);
  const filteredSelectedUsers = useMemo(() => {
    const keyword = selectedStaffKeyword.trim().toLowerCase();
    if (!keyword) {
      return selectedUsers;
    }
    return selectedUsers.filter((user) => staffSearchText(user).includes(keyword));
  }, [selectedStaffKeyword, selectedUsers]);

  function updateSelectedUsers(ids: number[]) {
    setValue(
      "storeStaffUserIds",
      [...new Set(ids)].sort((a, b) => a - b).join(","),
      { shouldDirty: true, shouldValidate: true },
    );
  }

  function toggleUser(user: AdminUser) {
    if (!user.storeStaff?.bindingId) {
      return;
    }
    if (selectedUserIDSet.has(user.id)) {
      updateSelectedUsers(selectedUserIDs.filter((id) => id !== user.id));
      return;
    }
    updateSelectedUsers([...selectedUserIDs, user.id]);
  }

  function selectFilteredUsers() {
    updateSelectedUsers([
      ...selectedUserIDs,
      ...filteredAvailableStoreStaffUsers.filter((user) => user.storeStaff?.bindingId).map((user) => user.id),
    ]);
  }

  async function onFormSubmit(values: EditForm) {
    await onSubmit(buildPayload(values));
  }

  return (
    <>
      <DialogContent className="max-h-[calc(100vh-2rem)] gap-0 overflow-y-auto p-0 sm:max-w-xl">
        <DialogHeader className="px-6 pt-6">
          <DialogTitle>
            {itemId ? t("agentProfile.teamEditTitle") : t("agentProfile.teamCreateTitle")}
          </DialogTitle>
        </DialogHeader>
        {loading ? (
          <div className="flex items-center justify-center py-12 text-muted-foreground">
            {t("agentProfile.loading")}
          </div>
        ) : (
          <form onSubmit={handleSubmit(onFormSubmit)}>
            <div className="space-y-4 p-6">
              <Field data-invalid={!!errors.name}>
                <FieldLabel htmlFor="agent-team-name">{t("agentProfile.teamName")}</FieldLabel>
                <FieldContent>
                  <Input id="agent-team-name" placeholder={t("agentProfile.teamNamePlaceholder")} {...register("name")} />
                  <FieldError errors={[errors.name]} />
                </FieldContent>
              </Field>
              <Field data-invalid={!!errors.leaderUserId}>
                <FieldLabel>{t("agentProfile.leader")}</FieldLabel>
                <FieldContent>
                  <Controller
                    control={control}
                    name="leaderUserId"
                    render={({ field }) => (
                      <Popover open={userSelectOpen} onOpenChange={setUserSelectOpen}>
                        <PopoverTrigger
                          render={
                            <Button
                              variant="outline"
                              role="combobox"
                              aria-expanded={userSelectOpen}
                              disabled={!canChangeLeader || !canViewUsers}
                              className="w-full justify-between font-normal"
                            />
                          }
                        >
                          <span className="truncate">
                            {field.value === "0"
                              ? t("agentProfile.noLeader")
                              : leaderOptions.find((option) => option.value === field.value)?.label ??
                                (!canViewUsers
                                  ? t("agentProfile.userFallback", { id: Number(field.value) })
                                  : t("agentProfile.selectLeader"))}
                          </span>
                          <ChevronsUpDownIcon className="ml-2 size-4 shrink-0 opacity-50" />
                        </PopoverTrigger>
                        <PopoverContent className="w-[var(--radix-popper-anchor-width)] p-0" align="start">
                          <Command>
                            <CommandInput placeholder={t("agentProfile.searchUser")} />
                            <CommandList>
                              <CommandEmpty>{t("agentProfile.emptyUser")}</CommandEmpty>
                              <CommandGroup>
                                <CommandItem
                                  value={t("agentProfile.noLeader")}
                                  onSelect={() => {
                                    field.onChange("0");
                                    setUserSelectOpen(false);
                                  }}
                                >
                                  <CheckIcon className={cn("mr-2 size-4", field.value === "0" ? "opacity-100" : "opacity-0")} />
                                  {t("agentProfile.noLeader")}
                                </CommandItem>
                                {leaderOptions.map((option) => (
                                  <CommandItem
                                    key={option.value}
                                    value={option.label}
                                    onSelect={() => {
                                      field.onChange(option.value);
                                      setUserSelectOpen(false);
                                    }}
                                  >
                                    <CheckIcon className={cn("mr-2 size-4", field.value === option.value ? "opacity-100" : "opacity-0")} />
                                    {option.label}
                                  </CommandItem>
                                ))}
                              </CommandGroup>
                            </CommandList>
                          </Command>
                        </PopoverContent>
                      </Popover>
                    )}
                  />
                  <FieldError errors={[errors.leaderUserId]} />
                </FieldContent>
              </Field>
              <Field data-invalid={!!errors.status}>
                <FieldLabel>{t("agentProfile.status")}</FieldLabel>
                <FieldContent>
                  <Controller
                    control={control}
                    name="status"
                    render={({ field }) => (
                      <OptionCombobox
                        options={statusOptions}
                        value={field.value}
                        onChange={field.onChange}
                        placeholder={t("agentProfile.selectStatus")}
                        searchPlaceholder={t("agentProfile.searchStatus")}
                        emptyText={t("agentProfile.emptyStatus")}
                      />
                    )}
                  />
                  <FieldError errors={[errors.status]} />
                </FieldContent>
              </Field>
              <Field data-invalid={!!errors.dispatchMode}>
                <FieldLabel>{t("agentProfile.dispatchMode")}</FieldLabel>
                <FieldContent>
                  <Controller
                    control={control}
                    name="dispatchMode"
                    render={({ field }) => (
                      <ToggleGroup
                        multiple={false}
                        value={[field.value]}
                        onValueChange={(value) => value[0] && field.onChange(value[0])}
                        variant="outline"
                        className="grid w-full grid-cols-2"
                      >
                        <ToggleGroupItem value={AgentTeamDispatchMode.Manual}>
                          {t("agentProfile.dispatchManual")}
                        </ToggleGroupItem>
                        <ToggleGroupItem value={AgentTeamDispatchMode.Rule}>
                          {t("agentProfile.dispatchRule")}
                        </ToggleGroupItem>
                      </ToggleGroup>
                    )}
                  />
                  <FieldError errors={[errors.dispatchMode]} />
                </FieldContent>
              </Field>
              <input type="hidden" {...register("storeStaffUserIds")} />
              <Field>
                <FieldLabel>{t("agentProfile.serviceStoreStaff")}</FieldLabel>
                <FieldContent>
                  <div className="rounded-lg border border-[#dbe7f6] bg-[#f8fbff] p-3 dark:border-border dark:bg-muted/30">
                    <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
                      <div className="min-w-0">
                        <div className="text-sm font-medium">
                          {selectedUserIDs.length > 0
                            ? t("agentProfile.selectedStoreStaffSummary", { count: selectedUserIDs.length, stores: selectedStoreCount })
                            : t("agentProfile.noStoreStaffSelected")}
                        </div>
                        <div className="mt-1 text-xs text-muted-foreground">
                          {t("agentProfile.storeStaffScopeHint")}
                        </div>
                      </div>
                      {canViewUsers ? <Button type="button" variant="outline" onClick={() => setStaffDialogOpen(true)}>
                        <UsersRoundIcon />
                        {t("agentProfile.manageStoreStaff")}
                      </Button> : null}
                    </div>
                    {selectedUsers.length > 0 ? (
                      <div className="mt-3 flex flex-wrap gap-1.5">
                        {selectedUsers.slice(0, 5).map((user) => (
                          <Badge key={user.id} variant="secondary">
                            {staffName(user)} · {user.storeStaff?.storeName || t("agentProfile.storeUnbound")}
                          </Badge>
                        ))}
                        {selectedUserIDs.length > 5 ? <Badge variant="outline">+{selectedUserIDs.length - 5}</Badge> : null}
                      </div>
                    ) : null}
                  </div>
                </FieldContent>
              </Field>
              <Field>
                <FieldLabel htmlFor="agent-team-description">{t("agentProfile.description")}</FieldLabel>
                <FieldContent>
                  <Input id="agent-team-description" placeholder={t("agentProfile.descriptionPlaceholder")} {...register("description")} />
                </FieldContent>
              </Field>
              <Field>
                <FieldLabel htmlFor="agent-team-remark">{t("agentProfile.remark")}</FieldLabel>
                <FieldContent>
                  <Textarea id="agent-team-remark" rows={4} placeholder={t("agentProfile.remarkPlaceholder")} {...register("remark")} />
                </FieldContent>
              </Field>
            </div>
            <DialogFooter className="mx-0 mb-0 px-6 py-4">
              <Button type="button" variant="outline" onClick={() => onOpenChange(false)} disabled={saving}>
                {t("agentProfile.cancel")}
              </Button>
              <Button type="submit" disabled={saving || loading}>
                {saving ? t("agentProfile.saving") : t("agentProfile.save")}
              </Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>

      <Dialog open={staffDialogOpen && canViewUsers} onOpenChange={setStaffDialogOpen}>
        <DialogContent className="flex h-[calc(100vh-2rem)] flex-col gap-0 overflow-hidden p-0 sm:h-[min(760px,calc(100vh-2rem))] sm:max-w-6xl">
          <DialogHeader className="shrink-0 border-b px-6 py-4">
            <DialogTitle>{t("agentProfile.manageStoreStaff")}</DialogTitle>
            <div className="text-sm text-muted-foreground">
              {t("agentProfile.storeStaffPickerDescription")}
            </div>
          </DialogHeader>
          <div className="grid min-h-0 flex-1 grid-cols-1 grid-rows-2 divide-y lg:grid-cols-2 lg:grid-rows-1 lg:divide-x lg:divide-y-0">
            <section className="flex min-h-0 flex-col">
              <div className="shrink-0 space-y-3 border-b p-4">
                <div className="flex items-center justify-between gap-3">
                  <div>
                    <div className="text-sm font-semibold">{t("agentProfile.availableStoreStaffTitle")}</div>
                    <div className="mt-0.5 text-xs text-muted-foreground">
                      {t("agentProfile.currentFilteredCount", { count: filteredAvailableStoreStaffUsers.length })}
                    </div>
                  </div>
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    onClick={selectFilteredUsers}
                    disabled={!filteredAvailableStoreStaffUsers.some((user) => user.storeStaff?.bindingId)}
                  >
                    {t("agentProfile.addAllFiltered")}
                  </Button>
                </div>
                <div className="relative">
                  <SearchIcon className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" />
                  <Input
                    value={staffKeyword}
                    onChange={(event) => setStaffKeyword(event.target.value)}
                    placeholder={t("agentProfile.searchStoreStaffAndTeam")}
                    className="pl-9"
                  />
                </div>
                <div className="grid gap-2 sm:grid-cols-2">
                  <OptionCombobox
                    options={assignmentOptions}
                    value={assignmentFilter}
                    onChange={(value) => setAssignmentFilter(value as AssignmentFilter)}
                    placeholder={t("agentProfile.assignmentScope")}
                    searchPlaceholder={t("agentProfile.searchAssignmentScope")}
                    emptyText={t("agentProfile.emptyAssignmentScope")}
                  />
                  <OptionCombobox
                    options={bindingOptions}
                    value={bindingFilter}
                    onChange={(value) => setBindingFilter(value as BindingFilter)}
                    placeholder={t("agentProfile.bindingStatus")}
                    searchPlaceholder={t("agentProfile.searchBindingStatus")}
                    emptyText={t("agentProfile.emptyBindingStatus")}
                  />
                </div>
              </div>
              <div className="min-h-0 flex-1 overflow-y-auto overscroll-contain">
                {staffLoading ? (
                  <div className="py-12 text-center text-sm text-muted-foreground">{t("agentProfile.loading")}</div>
                ) : filteredAvailableStoreStaffUsers.length === 0 ? (
                  <div className="py-12 text-center text-sm text-muted-foreground">
                    {t("agentProfile.noAvailableStoreStaff")}
                  </div>
                ) : (
                  filteredAvailableStoreStaffUsers.map((user) => (
                    <StoreStaffAccountRow
                      key={user.id}
                      user={user}
                      itemId={itemId}
                      action={() => toggleUser(user)}
                      actionLabel={t("agentProfile.addStoreStaff", { name: staffName(user) })}
                      actionIcon="add"
                      disabled={!user.storeStaff?.bindingId}
                      t={t}
                    />
                  ))
                )}
              </div>
            </section>
            <section className="flex min-h-0 flex-col bg-muted/20">
              <div className="shrink-0 space-y-3 border-b p-4">
                <div className="flex items-center justify-between gap-3">
                  <div>
                    <div className="text-sm font-semibold">{t("agentProfile.selectedStoreStaffTitle")}</div>
                    <div className="mt-0.5 text-xs text-muted-foreground">
                      {t("agentProfile.selectedStoreAndStaffCount", { count: selectedUserIDs.length, stores: selectedStoreCount })}
                    </div>
                  </div>
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    onClick={() => updateSelectedUsers([])}
                    disabled={selectedUserIDs.length === 0}
                  >
                    {t("agentProfile.removeAllSelected")}
                  </Button>
                </div>
                <div className="relative">
                  <SearchIcon className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" />
                  <Input
                    value={selectedStaffKeyword}
                    onChange={(event) => setSelectedStaffKeyword(event.target.value)}
                    placeholder={t("agentProfile.searchSelectedStoreStaff")}
                    className="pl-9"
                  />
                </div>
              </div>
              <div className="min-h-0 flex-1 overflow-y-auto overscroll-contain bg-background">
                {filteredSelectedUsers.length === 0 ? (
                  <div className="py-12 text-center text-sm text-muted-foreground">
                    {selectedUserIDs.length === 0
                      ? t("agentProfile.noStoreStaffSelected")
                      : t("agentProfile.noMatchingSelectedStoreStaff")}
                  </div>
                ) : (
                  filteredSelectedUsers.map((user) => (
                    <StoreStaffAccountRow
                      key={user.id}
                      user={user}
                      itemId={itemId}
                      action={() => toggleUser(user)}
                      actionLabel={t("agentProfile.removeStoreStaff", { name: staffName(user) })}
                      actionIcon="remove"
                      t={t}
                    />
                  ))
                )}
              </div>
            </section>
          </div>
          <DialogFooter className="mx-0 mb-0 shrink-0 border-t px-6 py-4">
            <div className="mr-auto text-xs text-muted-foreground">
              {t("agentProfile.storeStaffPickerFooter", { total: storeStaffUsers.length, selected: selectedUserIDs.length })}
            </div>
            <Button type="button" onClick={() => setStaffDialogOpen(false)}>
              {t("agentProfile.confirmStoreStaffSelection")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}

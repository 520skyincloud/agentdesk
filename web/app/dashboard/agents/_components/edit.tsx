"use client";

import { zodResolver } from "@hookform/resolvers/zod";
import { CheckIcon, ChevronsUpDownIcon, SearchIcon } from "lucide-react";
import { useCallback, useEffect, useMemo, useState } from "react";
import { Controller, type Resolver, useForm } from "react-hook-form";
import { toast } from "sonner";
import { z } from "zod/v4";

import { ImageInput } from "@/components/image-input";
import { OptionCombobox } from "@/components/option-combobox";
import { ProjectDialog } from "@/components/project-dialog";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
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
import { ScrollArea } from "@/components/ui/scroll-area";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import {
  fetchAgentProfile,
  fetchAgentTeamsAll,
  fetchUsersAll,
  fetchWxWorkProtocolInstances,
  type AdminAgentProfile,
  type AdminAgentTeam,
  type AdminUser,
  type CreateAdminAgentProfilePayload,
  type WxWorkProtocolInstance,
} from "@/lib/api/admin";
import { useI18n } from "@/i18n/provider";
import { ServiceStatus, Status } from "@/lib/generated/enums";

type TFunction = (key: string, values?: Record<string, string | number>) => string;

type AgentEditDialogProps = {
  open: boolean;
  saving: boolean;
  itemId: number | null;
  defaultTeamId: number | null;
  onOpenChange: (open: boolean) => void;
  onSubmit: (payload: CreateAdminAgentProfilePayload) => Promise<void>;
};

const emptyForm: EditForm = {
  userId: "",
  teamId: "",
  storeScopeIds: "",
  wxWorkInstanceScopeIds: "",
  agentCode: "",
  displayName: "",
  avatar: "",
  serviceStatus: String(ServiceStatus.Idle) as "0" | "1",
  maxConcurrentCount: "0",
  priorityLevel: "0",
  autoAssignEnabled: true,
  receiveOfflineMessage: false,
  remark: "",
};

type EditForm = {
  userId: string;
  teamId: string;
  storeScopeIds: string;
  wxWorkInstanceScopeIds: string;
  agentCode: string;
  displayName: string;
  avatar: string;
  serviceStatus: "0" | "1";
  maxConcurrentCount: string;
  priorityLevel: string;
  autoAssignEnabled: boolean;
  receiveOfflineMessage: boolean;
  remark: string;
};

function createEditFormSchema(t: TFunction) {
  return z.object({
  userId: z.string().trim().min(1, t("agentProfile.userRequired")),
  teamId: z.string().trim().min(1, t("agentProfile.teamRequired")),
  storeScopeIds: z.string().trim(),
  wxWorkInstanceScopeIds: z.string().trim(),
  agentCode: z.string().trim().min(1, t("agentProfile.agentCodeRequired")),
  displayName: z.string().trim().min(1, t("agentProfile.displayNameRequired")),
  avatar: z.string().trim(),
  serviceStatus: z.enum(["0", "1"], {
    message: t("agentProfile.statusRequired"),
  }),
  maxConcurrentCount: z
    .string()
    .trim()
    .regex(/^\d+$/, t("agentProfile.maxConcurrentInvalid")),
  priorityLevel: z
    .string()
    .trim()
    .regex(/^-?\d+$/, t("agentProfile.priorityInvalid")),
  autoAssignEnabled: z.boolean(),
  receiveOfflineMessage: z.boolean(),
  remark: z.string().trim(),
  });
}

function getServiceStatusOptions(t: TFunction) {
  return [
    { value: String(ServiceStatus.Idle), label: t("agentProfile.statusIdle") },
    { value: String(ServiceStatus.Busy), label: t("agentProfile.statusBusy") },
  ];
}

function buildForm(item: AdminAgentProfile | null): EditForm {
  if (!item) {
    return emptyForm;
  }
  return {
    userId: String(item.userId),
    teamId: String(item.teamId),
    storeScopeIds: (item.storeScopeIds || []).join(","),
    wxWorkInstanceScopeIds: (item.wxWorkInstanceScopeIds || []).join(","),
    agentCode: item.agentCode,
    displayName: item.displayName,
    avatar: item.avatar || "",
    serviceStatus: String(item.serviceStatus) as EditForm["serviceStatus"],
    maxConcurrentCount: String(item.maxConcurrentCount),
    priorityLevel: String(item.priorityLevel),
    autoAssignEnabled: item.autoAssignEnabled,
    receiveOfflineMessage: item.receiveOfflineMessage,
    remark: item.remark || "",
  };
}

function buildFormWithDefaultTeam(
  item: AdminAgentProfile | null,
  defaultTeamId: number | null,
): EditForm {
  const form = buildForm(item);
  if (!item && defaultTeamId) {
    return {
      ...form,
      teamId: String(defaultTeamId),
    };
  }
  return form;
}

function buildPayload(form: EditForm): CreateAdminAgentProfilePayload {
  return {
    userId: Number(form.userId),
    teamId: Number(form.teamId),
    storeScopeIds: parseIdList(form.storeScopeIds),
    wxWorkInstanceScopeIds: parseIdList(form.wxWorkInstanceScopeIds),
    agentCode: form.agentCode.trim(),
    displayName: form.displayName.trim(),
    avatar: form.avatar.trim(),
    serviceStatus: Number(form.serviceStatus),
    maxConcurrentCount: Number(form.maxConcurrentCount),
    priorityLevel: Number(form.priorityLevel),
    autoAssignEnabled: form.autoAssignEnabled,
    receiveOfflineMessage: form.receiveOfflineMessage,
    remark: form.remark.trim(),
  };
}

function parseIdList(value: string) {
  return value
    .split(/[,，\s]+/)
    .map((part) => Number(part.trim()))
    .filter((id, index, ids) => Number.isFinite(id) && id > 0 && ids.indexOf(id) === index)
}

function wxWorkInstanceName(item: WxWorkProtocolInstance) {
  return item.employeeName || item.employeeUserId || item.guid || `#${item.id}`;
}

function wxWorkInstanceStoreLabel(item: WxWorkProtocolInstance) {
  return item.storeName || item.storeCode || (item.storeId > 0 ? `#${item.storeId}` : "");
}

function wxWorkInstanceSearchText(item: WxWorkProtocolInstance) {
  return [
    item.employeeName,
    item.employeeUserId,
    item.guid,
    item.storeName,
    item.storeCode,
    item.companyName,
    item.healthStatus,
  ].join(" ").toLowerCase();
}

export function EditDialog({
  open,
  saving,
  itemId,
  defaultTeamId,
  onOpenChange,
  onSubmit,
}: AgentEditDialogProps) {
  if (!open) {
    return null;
  }

  return (
    <AgentEditDialogBody
      key={itemId ? `edit-${itemId}` : "create"}
      open={open}
      itemId={itemId}
      defaultTeamId={defaultTeamId}
      saving={saving}
      onOpenChange={onOpenChange}
      onSubmit={onSubmit}
    />
  );
}

type AgentEditDialogBodyProps = {
  open: boolean;
  saving: boolean;
  itemId: number | null;
  defaultTeamId: number | null;
  onOpenChange: (open: boolean) => void;
  onSubmit: (payload: CreateAdminAgentProfilePayload) => Promise<void>;
};

function AgentEditDialogBody({
  open,
  saving,
  itemId,
  defaultTeamId,
  onOpenChange,
  onSubmit,
}: AgentEditDialogBodyProps) {
  const t = useI18n();
  const [users, setUsers] = useState<AdminUser[]>([]);
  const [teams, setTeams] = useState<AdminAgentTeam[]>([]);
  const [wxWorkInstances, setWxWorkInstances] = useState<WxWorkProtocolInstance[]>([]);
  const [userSelectOpen, setUserSelectOpen] = useState(false);
  const [wxWorkDialogOpen, setWxWorkDialogOpen] = useState(false);
  const [wxWorkKeyword, setWxWorkKeyword] = useState("");
  const [loading, setLoading] = useState(false);
  const [wxWorkLoading, setWxWorkLoading] = useState(false);
  const userOptions = users.map((user) => ({
    value: String(user.id),
    label: `${user.nickname || user.username} (${user.username})`,
  }));
  const teamOptions = teams.map((team) => ({
    value: String(team.id),
    label: team.name,
  }));
  const serviceStatusOptions = useMemo(() => getServiceStatusOptions(t), [t]);
  const loadOptions = useCallback(async () => {
    setWxWorkLoading(true);
    try {
      const [usersData, teamsData, wxWorkData] = await Promise.all([
        fetchUsersAll(),
        fetchAgentTeamsAll(),
        fetchWxWorkProtocolInstances({ page: 1, limit: 500 }),
      ]);
      setUsers(usersData);
      setTeams(teamsData);
      setWxWorkInstances(wxWorkData.results);
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("agentProfile.loadOptionsFailed"));
    } finally {
      setWxWorkLoading(false);
    }
  }, [t]);
  const editFormSchema = useMemo(() => createEditFormSchema(t), [t]);
  const editFormResolver = useMemo(
    () => zodResolver(editFormSchema) as Resolver<EditForm>,
    [editFormSchema],
  );
  const form = useForm<EditForm>({
    resolver: editFormResolver,
    defaultValues: buildFormWithDefaultTeam(null, defaultTeamId),
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
  const teamIdValue = watch("teamId");
  const wxWorkScopeValue = watch("wxWorkInstanceScopeIds");
  const selectedTeam = useMemo(
    () => teams.find((team) => team.id === Number(teamIdValue)) ?? null,
    [teamIdValue, teams],
  );
  const selectedWxWorkIds = useMemo(() => parseIdList(wxWorkScopeValue || ""), [wxWorkScopeValue]);
  const selectedWxWorkIdSet = useMemo(() => new Set(selectedWxWorkIds), [selectedWxWorkIds]);
  const availableWxWorkInstances = useMemo(() => {
    if (!selectedTeam || selectedTeam.wxWorkInstanceScopeIds.length === 0) {
      return wxWorkInstances;
    }
    const teamScope = new Set(selectedTeam.wxWorkInstanceScopeIds);
    return wxWorkInstances.filter((item) => teamScope.has(item.id));
  }, [selectedTeam, wxWorkInstances]);
  const selectedWxWorkInstances = useMemo(
    () =>
      selectedWxWorkIds
        .map((id) => wxWorkInstances.find((item) => item.id === id))
        .filter((item): item is WxWorkProtocolInstance => Boolean(item)),
    [selectedWxWorkIds, wxWorkInstances],
  );
  const selectedStoreCount = useMemo(() => {
    const storeIds = new Set<number>();
    selectedWxWorkInstances.forEach((item) => {
      if (item.storeId > 0) {
        storeIds.add(item.storeId);
      }
    });
    return storeIds.size;
  }, [selectedWxWorkInstances]);
  const inheritedWxWorkCount = selectedTeam?.wxWorkInstanceScopeIds.length ?? 0;
  const inheritedStoreCount = selectedTeam?.storeScopeIds.length ?? 0;
  const filteredWxWorkInstances = useMemo(() => {
    const keyword = wxWorkKeyword.trim().toLowerCase();
    const items = [...availableWxWorkInstances].sort((a, b) => {
      const storeA = wxWorkInstanceStoreLabel(a);
      const storeB = wxWorkInstanceStoreLabel(b);
      return storeA.localeCompare(storeB) || a.id - b.id;
    });
    if (!keyword) {
      return items;
    }
    return items.filter((item) => wxWorkInstanceSearchText(item).includes(keyword));
  }, [availableWxWorkInstances, wxWorkKeyword]);

  useEffect(() => {
    async function loadDetail() {
      if (!itemId) {
        reset(buildFormWithDefaultTeam(null, defaultTeamId));
        return;
      }
      setLoading(true);
      try {
        const data = await fetchAgentProfile(itemId);
        reset(buildForm(data));
      } catch (error) {
        toast.error(error instanceof Error ? error.message : t("agentProfile.loadDetailFailed"));
      } finally {
        setLoading(false);
      }
    }
    void loadDetail();
  }, [itemId, defaultTeamId, reset, t]);

  useEffect(() => {
    if (open) {
      void loadOptions();
    }
  }, [loadOptions, open]);

  async function onFormSubmit(values: EditForm) {
    await onSubmit(buildPayload(values));
  }

  function updateWxWorkScope(ids: number[]) {
    const nextValue = [...new Set(ids)].sort((a, b) => a - b).join(",");
    setValue("wxWorkInstanceScopeIds", nextValue, {
      shouldDirty: true,
      shouldValidate: true,
    });
  }

  function toggleWxWorkInstance(id: number) {
    const nextIds = new Set(selectedWxWorkIds);
    if (nextIds.has(id)) {
      nextIds.delete(id);
    } else {
      nextIds.add(id);
    }
    updateWxWorkScope([...nextIds]);
  }

  function selectFilteredWxWorkInstances() {
    updateWxWorkScope([
      ...selectedWxWorkIds,
      ...filteredWxWorkInstances.map((item) => item.id),
    ]);
  }

  const formId = "agent-edit-form";

  return (
    <>
    <ProjectDialog
      open={open}
      onOpenChange={onOpenChange}
      title={itemId ? t("agentProfile.editTitle") : t("agentProfile.createTitle")}
      size="lg"
      footer={
        <>
          <Button
            type="button"
            variant="outline"
            onClick={() => onOpenChange(false)}
            disabled={saving}
          >
            {t("agentProfile.cancel")}
          </Button>
          <Button type="submit" form={formId} disabled={saving || loading}>
            {saving ? t("agentProfile.saving") : t("agentProfile.save")}
          </Button>
        </>
      }
    >
      {loading ? (
        <div className="flex items-center justify-center py-12">
          <div className="text-muted-foreground">{t("agentProfile.loading")}</div>
        </div>
      ) : (
        <form
          id={formId}
          onSubmit={handleSubmit(onFormSubmit)}
          className="space-y-4"
        >
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <Field data-invalid={!!errors.userId}>
              <FieldLabel>{t("agentProfile.linkedUser")}</FieldLabel>
              <FieldContent>
                <Controller
                  control={control}
                  name="userId"
                  render={({ field }) => (
                    <Popover
                      open={userSelectOpen}
                      onOpenChange={setUserSelectOpen}
                    >
                      <PopoverTrigger
                        render={
                          <Button
                            variant="outline"
                            role="combobox"
                            aria-expanded={userSelectOpen}
                            className="w-full justify-between font-normal"
                          />
                        }
                      >
                        <span className="truncate">
                          {userOptions.find(
                            (option) => option.value === field.value,
                          )?.label ?? t("agentProfile.selectUser")}
                        </span>
                        <ChevronsUpDownIcon className="ml-2 size-4 shrink-0 opacity-50" />
                      </PopoverTrigger>
                      <PopoverContent
                        className="w-[var(--radix-popper-anchor-width)] p-0"
                        align="start"
                      >
                        <Command>
                          <CommandInput placeholder={t("agentProfile.searchUser")} />
                          <CommandList>
                            <CommandEmpty>{t("agentProfile.emptyUser")}</CommandEmpty>
                            <CommandGroup>
                              {userOptions.map((option) => (
                                <CommandItem
                                  key={option.value}
                                  value={option.label}
                                  onSelect={() => {
                                    field.onChange(option.value);
                                    setUserSelectOpen(false);
                                  }}
                                >
                                  <CheckIcon
                                    className={`mr-2 size-4 ${
                                      field.value === option.value
                                        ? "opacity-100"
                                        : "opacity-0"
                                    }`}
                                  />
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
                <FieldError errors={[errors.userId]} />
              </FieldContent>
            </Field>
            <Field data-invalid={!!errors.displayName}>
              <FieldLabel htmlFor="agent-display-name">{t("agentProfile.displayName")}</FieldLabel>
              <FieldContent>
                <Input
                  id="agent-display-name"
                  placeholder={t("agentProfile.displayNamePlaceholder")}
                  {...register("displayName")}
                />
                <FieldError errors={[errors.displayName]} />
              </FieldContent>
            </Field>
            <Field data-invalid={!!errors.teamId}>
              <FieldLabel>{t("agentProfile.team")}</FieldLabel>
              <FieldContent>
                <Controller
                  control={control}
                  name="teamId"
                  render={({ field }) => (
                    <OptionCombobox
                      options={teamOptions}
                      value={field.value}
                      onChange={(value) => {
                        field.onChange(value);
                        updateWxWorkScope([]);
                      }}
                      placeholder={t("agentProfile.selectTeam")}
                      searchPlaceholder={t("agentProfile.searchTeams")}
                      emptyText={t("agentProfile.noTeams")}
                    />
                  )}
                />
                <FieldError errors={[errors.teamId]} />
              </FieldContent>
            </Field>
            <input type="hidden" {...register("storeScopeIds")} />
            <input type="hidden" {...register("wxWorkInstanceScopeIds")} />
            <Field className="sm:col-span-2">
              <FieldLabel>{t("agentProfile.agentServiceWxWorkInstances")}</FieldLabel>
              <FieldContent>
                <div className="rounded-xl border border-[#dbe7f6] bg-[#f8fbff] p-3">
                  <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
                    <div className="min-w-0">
                      <div className="text-sm font-medium">
                        {selectedWxWorkIds.length > 0
                          ? t("agentProfile.agentWxWorkCustomSummary", {
                              count: selectedWxWorkIds.length,
                              stores: selectedStoreCount,
                            })
                          : selectedTeam
                            ? inheritedWxWorkCount > 0
                              ? t("agentProfile.agentWxWorkInheritedSummary", {
                                  count: inheritedWxWorkCount,
                                  stores: inheritedStoreCount,
                                })
                              : t("agentProfile.agentWxWorkInheritedUnlimited")
                            : t("agentProfile.agentWxWorkNoTeam")}
                      </div>
                      <div className="mt-1 text-xs text-muted-foreground">
                        {t("agentProfile.agentWxWorkScopeHint")}
                      </div>
                    </div>
                    <Button
                      type="button"
                      variant="outline"
                      onClick={() => setWxWorkDialogOpen(true)}
                      disabled={!selectedTeam}
                    >
                      {t("agentProfile.manageAgentWxWorkInstances")}
                    </Button>
                  </div>
                  {selectedWxWorkInstances.length > 0 ? (
                    <div className="mt-3 flex flex-wrap gap-1.5">
                      {selectedWxWorkInstances.slice(0, 5).map((item) => (
                        <Badge key={item.id} variant="secondary">
                          {wxWorkInstanceName(item)}
                          {wxWorkInstanceStoreLabel(item) ? ` · ${wxWorkInstanceStoreLabel(item)}` : ""}
                        </Badge>
                      ))}
                      {selectedWxWorkIds.length > 5 ? (
                        <Badge variant="outline">+{selectedWxWorkIds.length - 5}</Badge>
                      ) : null}
                    </div>
                  ) : null}
                </div>
              </FieldContent>
            </Field>
          </div>

          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <Field data-invalid={!!errors.agentCode}>
              <FieldLabel htmlFor="agent-code">{t("agentProfile.agentCodeLabel")}</FieldLabel>
              <FieldContent>
                <Input
                  id="agent-code"
                  placeholder={t("agentProfile.agentCodePlaceholder")}
                  {...register("agentCode")}
                />
                <FieldError errors={[errors.agentCode]} />
              </FieldContent>
            </Field>

            <Field className="min-h-32">
              <FieldLabel>{t("agentProfile.avatar")}</FieldLabel>
              <FieldContent>
                <Controller
                  control={control}
                  name="avatar"
                  render={({ field }) => (
                    <ImageInput
                      value={field.value}
                      onChange={field.onChange}
                      disabled={saving}
                      prefix="avatar"
                      placeholder={t("agentProfile.avatarUpload")}
                      className="size-16 rounded-full"
                    />
                  )}
                />
              </FieldContent>
            </Field>
          </div>

          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <Field data-invalid={!!errors.serviceStatus}>
              <FieldLabel>{t("agentProfile.serviceStatus")}</FieldLabel>
              <FieldContent>
                <Controller
                  control={control}
                  name="serviceStatus"
                  render={({ field }) => (
                    <OptionCombobox
                      options={serviceStatusOptions}
                      value={field.value}
                      onChange={field.onChange}
                      placeholder={t("agentProfile.selectStatus")}
                      searchPlaceholder={t("agentProfile.searchStatus")}
                      emptyText={t("agentProfile.emptyStatus")}
                    />
                  )}
                />
                <FieldError errors={[errors.serviceStatus]} />
              </FieldContent>
            </Field>
          </div>

          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <Field data-invalid={!!errors.maxConcurrentCount}>
              <FieldLabel htmlFor="agent-max-concurrent-count">
                {t("agentProfile.maxConcurrent")}
              </FieldLabel>
              <FieldContent>
                <Input
                  id="agent-max-concurrent-count"
                  type="number"
                  min={0}
                  {...register("maxConcurrentCount")}
                />
                <FieldError errors={[errors.maxConcurrentCount]} />
              </FieldContent>
            </Field>
            <Field data-invalid={!!errors.priorityLevel}>
              <FieldLabel htmlFor="agent-priority-level">{t("agentProfile.priority")}</FieldLabel>
              <FieldContent>
                <Input
                  id="agent-priority-level"
                  type="number"
                  step={1}
                  {...register("priorityLevel")}
                />
                <FieldError errors={[errors.priorityLevel]} />
              </FieldContent>
            </Field>
          </div>

          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <Field>
              <FieldLabel>{t("agentProfile.autoAssignEnabled")}</FieldLabel>
              <FieldContent>
                <Controller
                  control={control}
                  name="autoAssignEnabled"
                  render={({ field }) => (
                    <Switch
                      checked={field.value}
                      onCheckedChange={field.onChange}
                    />
                  )}
                />
              </FieldContent>
            </Field>
            <Field>
              <FieldLabel>{t("agentProfile.receiveOfflineMessage")}</FieldLabel>
              <FieldContent>
                <Controller
                  control={control}
                  name="receiveOfflineMessage"
                  render={({ field }) => (
                    <Switch
                      checked={field.value}
                      onCheckedChange={field.onChange}
                    />
                  )}
                />
              </FieldContent>
            </Field>
          </div>

          <Field>
            <FieldLabel htmlFor="agent-remark">{t("agentProfile.remark")}</FieldLabel>
            <FieldContent>
              <Textarea
                id="agent-remark"
                rows={4}
                placeholder={t("agentProfile.remarkPlaceholder")}
                {...register("remark")}
              />
            </FieldContent>
          </Field>
        </form>
      )}
    </ProjectDialog>
    <Dialog open={wxWorkDialogOpen} onOpenChange={setWxWorkDialogOpen}>
      <DialogContent className="flex max-h-[calc(100vh-2rem)] flex-col gap-0 overflow-hidden p-0 sm:max-w-4xl">
        <DialogHeader className="px-6 pt-6">
          <DialogTitle>{t("agentProfile.manageAgentWxWorkInstances")}</DialogTitle>
        </DialogHeader>
        <div className="border-b px-6 py-4">
          <div className="flex flex-col gap-3 lg:flex-row lg:items-center lg:justify-between">
            <div className="relative min-w-0 flex-1">
              <SearchIcon className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" />
              <Input
                value={wxWorkKeyword}
                onChange={(event) => setWxWorkKeyword(event.target.value)}
                placeholder={t("agentProfile.searchWxWorkInstances")}
                className="pl-9"
              />
            </div>
            <div className="flex flex-wrap gap-2">
              <Button
                type="button"
                variant="outline"
                onClick={selectFilteredWxWorkInstances}
                disabled={filteredWxWorkInstances.length === 0}
              >
                {t("agentProfile.selectFilteredWxWork")}
              </Button>
              <Button
                type="button"
                variant="outline"
                onClick={() => updateWxWorkScope([])}
                disabled={selectedWxWorkIds.length === 0}
              >
                {t("agentProfile.clearWxWorkSelection")}
              </Button>
            </div>
          </div>
          <div className="mt-2 text-xs text-muted-foreground">
            {t("agentProfile.agentWxWorkSelectionMeta", {
              selected: selectedWxWorkIds.length,
              total: availableWxWorkInstances.length,
            })}
          </div>
        </div>
        <ScrollArea className="min-h-0 flex-1">
          <div className="space-y-2 p-4">
            {wxWorkLoading ? (
              <div className="py-12 text-center text-sm text-muted-foreground">
                {t("agentProfile.loading")}
              </div>
            ) : filteredWxWorkInstances.length === 0 ? (
              <div className="py-12 text-center text-sm text-muted-foreground">
                {t("agentProfile.emptyWxWorkInstances")}
              </div>
            ) : (
              filteredWxWorkInstances.map((item) => {
                const checked = selectedWxWorkIdSet.has(item.id);
                return (
                  <div
                    key={item.id}
                    className="flex items-start gap-3 rounded-xl border border-[#dbe7f6] bg-white p-3"
                  >
                    <Checkbox
                      checked={checked}
                      onCheckedChange={() => toggleWxWorkInstance(item.id)}
                      className="mt-1"
                    />
                    <button
                      type="button"
                      className="min-w-0 flex-1 text-left"
                      onClick={() => toggleWxWorkInstance(item.id)}
                    >
                      <div className="flex flex-wrap items-center gap-2">
                        <span className="font-medium">{wxWorkInstanceName(item)}</span>
                        <Badge variant="outline">ID {item.id}</Badge>
                        <Badge variant={item.status === Status.Ok ? "secondary" : "outline"}>
                          {item.healthStatus || t("agentProfile.unknownWxWorkHealth")}
                        </Badge>
                      </div>
                      <div className="mt-1 text-xs text-muted-foreground">
                        {[
                          item.employeeUserId,
                          wxWorkInstanceStoreLabel(item),
                          item.companyName,
                        ].filter(Boolean).join(" · ") || "-"}
                      </div>
                    </button>
                  </div>
                );
              })
            )}
          </div>
        </ScrollArea>
        <DialogFooter className="mx-0 mb-0 px-6 py-4">
          <Button type="button" onClick={() => setWxWorkDialogOpen(false)}>
            {t("agentProfile.confirmWxWorkSelection")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
    </>
  );
}

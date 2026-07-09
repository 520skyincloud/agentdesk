"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import { zodResolver } from "@hookform/resolvers/zod";
import { CheckIcon, ChevronsUpDownIcon, SearchIcon } from "lucide-react";
import { Controller, type Resolver, useForm } from "react-hook-form";
import { toast } from "sonner";
import { z } from "zod/v4";

import {
  type AdminAgentTeam,
  type CreateAdminAgentTeamPayload,
  type WxWorkProtocolInstance,
  fetchAgentTeam,
  fetchWxWorkProtocolInstances,
  fetchUsersAll,
  type AdminUser,
} from "@/lib/api/admin";
import { OptionCombobox } from "@/components/option-combobox";
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
import { Textarea } from "@/components/ui/textarea";
import { useI18n } from "@/i18n/provider";
import { Status } from "@/lib/generated/enums";

type TFunction = (key: string, values?: Record<string, string | number>) => string;

type TeamEditDialogProps = {
  open: boolean;
  saving: boolean;
  itemId: number | null;
  onOpenChange: (open: boolean) => void;
  onSubmit: (payload: CreateAdminAgentTeamPayload) => Promise<void>;
};

const emptyForm: EditForm = {
  name: "",
  leaderUserId: "0",
  companyScopeIds: "",
  storeScopeIds: "",
  wxWorkInstanceScopeIds: "",
  status: String(Status.Ok),
  description: "",
  remark: "",
};

type EditForm = {
  name: string;
  leaderUserId: string;
  companyScopeIds: string;
  storeScopeIds: string;
  wxWorkInstanceScopeIds: string;
  status: string;
  description: string;
  remark: string;
};

function createEditFormSchema(t: TFunction) {
  return z.object({
  name: z.string().trim().min(1, t("agentProfile.teamNameRequired")),
  leaderUserId: z.string().trim().regex(/^\d+$/, t("agentProfile.leaderInvalid")),
  companyScopeIds: z.string().trim(),
  storeScopeIds: z.string().trim(),
  wxWorkInstanceScopeIds: z.string().trim(),
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

function buildForm(item: AdminAgentTeam | null): EditForm {
  if (!item) {
    return emptyForm;
  }
  return {
    name: item.name,
    leaderUserId: String(item.leaderUserId),
    companyScopeIds: (item.companyScopeIds || []).join(","),
    storeScopeIds: (item.storeScopeIds || []).join(","),
    wxWorkInstanceScopeIds: (item.wxWorkInstanceScopeIds || []).join(","),
    status: String(item.status),
    description: item.description || "",
    remark: item.remark || "",
  };
}

function buildPayload(form: EditForm): CreateAdminAgentTeamPayload {
  return {
    name: form.name.trim(),
    leaderUserId: Number(form.leaderUserId),
    companyScopeIds: parseIdList(form.companyScopeIds),
    storeScopeIds: parseIdList(form.storeScopeIds),
    wxWorkInstanceScopeIds: parseIdList(form.wxWorkInstanceScopeIds),
    status: Number(form.status),
    description: form.description.trim(),
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
  onOpenChange,
  onSubmit,
}: TeamEditDialogBodyProps) {
  const t = useI18n();
  const [users, setUsers] = useState<AdminUser[]>([]);
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
  const statusOptions = useMemo(() => getStatusOptions(t), [t]);
  const loadUsers = useCallback(async () => {
    try {
      const data = await fetchUsersAll();
      setUsers(data);
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("agentProfile.loadUsersFailed"));
    }
  }, [t]);
  const loadWxWorkInstances = useCallback(async () => {
    setWxWorkLoading(true);
    try {
      const data = await fetchWxWorkProtocolInstances({ page: 1, limit: 500 });
      setWxWorkInstances(data.results);
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("agentProfile.loadWxWorkInstancesFailed"));
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
  const wxWorkScopeValue = watch("wxWorkInstanceScopeIds");
  const selectedWxWorkIds = useMemo(() => parseIdList(wxWorkScopeValue || ""), [wxWorkScopeValue]);
  const selectedWxWorkIdSet = useMemo(() => new Set(selectedWxWorkIds), [selectedWxWorkIds]);
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
  const filteredWxWorkInstances = useMemo(() => {
    const keyword = wxWorkKeyword.trim().toLowerCase();
    const items = [...wxWorkInstances].sort((a, b) => {
      const storeA = wxWorkInstanceStoreLabel(a);
      const storeB = wxWorkInstanceStoreLabel(b);
      return storeA.localeCompare(storeB) || a.id - b.id;
    });
    if (!keyword) {
      return items;
    }
    return items.filter((item) => wxWorkInstanceSearchText(item).includes(keyword));
  }, [wxWorkInstances, wxWorkKeyword]);

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

  useEffect(() => {
    void loadWxWorkInstances();
  }, [loadWxWorkInstances]);

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

  async function onFormSubmit(values: EditForm) {
    await onSubmit(buildPayload(values));
  }

  return (
    <>
    <DialogContent className="max-w-xl gap-0 p-0 sm:max-w-xl">
      <DialogHeader className="px-6 pt-6">
        <DialogTitle>{itemId ? t("agentProfile.teamEditTitle") : t("agentProfile.teamCreateTitle")}</DialogTitle>
      </DialogHeader>
      {loading ? (
        <div className="flex items-center justify-center py-12">
          <div className="text-muted-foreground">{t("agentProfile.loading")}</div>
        </div>
      ) : (
        <form onSubmit={handleSubmit(onFormSubmit)}>
          <div className="space-y-4 p-6">
            <Field data-invalid={!!errors.name}>
              <FieldLabel htmlFor="agent-team-name">{t("agentProfile.teamName")}</FieldLabel>
              <FieldContent>
                <Input
                  id="agent-team-name"
                  placeholder={t("agentProfile.teamNamePlaceholder")}
                  {...register("name")}
                />
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
                            className="w-full justify-between font-normal"
                          />
                        }
                      >
                        <span className="truncate">
                          {field.value === "0"
                            ? t("agentProfile.noLeader")
                            : userOptions.find((option) => option.value === field.value)?.label ?? t("agentProfile.selectLeader")}
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
                                <CheckIcon
                                  className={`mr-2 size-4 ${field.value === "0" ? "opacity-100" : "opacity-0"}`}
                                />
                                {t("agentProfile.noLeader")}
                              </CommandItem>
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
                                      field.value === option.value ? "opacity-100" : "opacity-0"
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
            <Field>
              <FieldLabel htmlFor="agent-team-company-scope-ids">可管理公司ID</FieldLabel>
              <FieldContent>
                <Input
                  id="agent-team-company-scope-ids"
                  placeholder="多个用逗号分隔；组长可管理这些公司下的门店知识库"
                  {...register("companyScopeIds")}
                />
              </FieldContent>
            </Field>
            <input type="hidden" {...register("storeScopeIds")} />
            <input type="hidden" {...register("wxWorkInstanceScopeIds")} />
            <Field>
              <FieldLabel>{t("agentProfile.serviceWxWorkInstances")}</FieldLabel>
              <FieldContent>
                <div className="rounded-xl border border-[#dbe7f6] bg-[#f8fbff] p-3">
                  <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
                    <div className="min-w-0">
                      <div className="text-sm font-medium">
                        {selectedWxWorkIds.length > 0
                          ? t("agentProfile.selectedWxWorkSummary", {
                              count: selectedWxWorkIds.length,
                              stores: selectedStoreCount,
                            })
                          : t("agentProfile.noWxWorkSelected")}
                      </div>
                      <div className="mt-1 text-xs text-muted-foreground">
                        {t("agentProfile.wxWorkScopeHint")}
                      </div>
                    </div>
                    <Button
                      type="button"
                      variant="outline"
                      onClick={() => setWxWorkDialogOpen(true)}
                    >
                      {t("agentProfile.manageWxWorkInstances")}
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
            <Field>
              <FieldLabel htmlFor="agent-team-description">{t("agentProfile.description")}</FieldLabel>
              <FieldContent>
                <Input
                  id="agent-team-description"
                  placeholder={t("agentProfile.descriptionPlaceholder")}
                  {...register("description")}
                />
              </FieldContent>
            </Field>
            <Field>
              <FieldLabel htmlFor="agent-team-remark">{t("agentProfile.remark")}</FieldLabel>
              <FieldContent>
                <Textarea
                  id="agent-team-remark"
                  rows={4}
                  placeholder={t("agentProfile.remarkPlaceholder")}
                  {...register("remark")}
                />
              </FieldContent>
            </Field>
          </div>
          <DialogFooter className="mx-0 mb-0 px-6 py-4">
            <Button
              type="button"
              variant="outline"
              onClick={() => onOpenChange(false)}
              disabled={saving}
            >
              {t("agentProfile.cancel")}
            </Button>
            <Button type="submit" disabled={saving || loading}>
              {saving ? t("agentProfile.saving") : t("agentProfile.save")}
            </Button>
          </DialogFooter>
        </form>
      )}
    </DialogContent>
    <Dialog open={wxWorkDialogOpen} onOpenChange={setWxWorkDialogOpen}>
      <DialogContent className="flex max-h-[calc(100vh-2rem)] flex-col gap-0 overflow-hidden p-0 sm:max-w-4xl">
        <DialogHeader className="px-6 pt-6">
          <DialogTitle>{t("agentProfile.manageWxWorkInstances")}</DialogTitle>
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
            {t("agentProfile.wxWorkSelectionMeta", {
              selected: selectedWxWorkIds.length,
              total: wxWorkInstances.length,
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

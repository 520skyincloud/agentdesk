"use client"
import { zodResolver } from "@hookform/resolvers/zod"
import { useCallback, useEffect, useMemo, useState } from "react"
import { Controller, type Resolver, useForm } from "react-hook-form"
import { toast } from "sonner"
import { z } from "zod/v4"

import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import {
  Field,
  FieldContent,
  FieldError,
  FieldLabel,
} from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { OptionCombobox } from "@/components/option-combobox"
import { Textarea } from "@/components/ui/textarea"
import {
  type AdminAgentTeam,
  type AdminAgentProfile,
  type AdminAgentTeamSchedule,
  type AdminAgentTeamSquad,
  type CreateAdminAgentTeamSchedulePayload,
  fetchAgentProfilesAll,
  fetchAgentTeamSchedule,
  fetchAgentTeamSquads,
  fetchAgentTeamsAll,
} from "@/lib/api/admin"
import { useI18n } from "@/i18n/provider"
import { ScheduleAgentOverrides } from "./agent-overrides"

type TFunction = (key: string, values?: Record<string, string | number>) => string

type ScheduleEditDialogProps = {
  open: boolean
  saving: boolean
  itemId: number | null
  defaultValues?: Partial<CreateAdminAgentTeamSchedulePayload> | null
  onOpenChange: (open: boolean) => void
  onSubmit: (payload: CreateAdminAgentTeamSchedulePayload) => Promise<void>
  onDelete?: (id: number) => Promise<void>
}

const emptyForm: EditForm = {
  teamId: "",
  squadId: "0",
  includedAgentProfileIds: [],
  excludedAgentProfileIds: [],
  startAt: "",
  endAt: "",
  remark: "",
}

type EditForm = {
  teamId: string
  squadId: string
  includedAgentProfileIds: number[]
  excludedAgentProfileIds: number[]
  startAt: string
  endAt: string
  remark: string
}

function createEditFormSchema(t: TFunction) {
  return z.object({
  teamId: z.string().trim().regex(/^\d+$/, t("agentTeamSchedule.teamRequired")),
  squadId: z.string().trim().regex(/^\d+$/, t("agentTeamSchedule.squadRequired")),
  includedAgentProfileIds: z.array(z.number().int().positive()),
  excludedAgentProfileIds: z.array(z.number().int().positive()),
  startAt: z.string().trim().min(1, t("agentTeamSchedule.startRequired")),
  endAt: z.string().trim().min(1, t("agentTeamSchedule.endRequired")),
  remark: z.string().trim(),
}).superRefine((value, ctx) => {
  const startAt = parseDateTimeLocal(value.startAt)
  const endAt = parseDateTimeLocal(value.endAt)
  if (!startAt || !endAt) {
    return
  }
  if (!endAt || endAt <= startAt) {
    ctx.addIssue({
      code: "custom",
      path: ["endAt"],
      message: t("agentTeamSchedule.endAfterStart"),
    })
    return
  }
  if (endAt.getTime() - startAt.getTime() > 24 * 60 * 60 * 1000) {
    ctx.addIssue({
      code: "custom",
      path: ["endAt"],
      message: t("agentTeamSchedule.durationMax24"),
    })
  }
  if (startAt < startOfLocalDay(new Date())) {
    ctx.addIssue({
      code: "custom",
      path: ["startAt"],
      message: t("agentTeamSchedule.historyReadonly"),
    })
  }
})
}

function toDateTimeLocal(value?: string) {
  if (!value) {
    return ""
  }
  return value.replace(" ", "T").slice(0, 16)
}

function parseDateTimeLocal(value: string) {
  const ret = new Date(value)
  return Number.isNaN(ret.getTime()) ? null : ret
}

function startOfLocalDay(value: Date) {
  const ret = new Date(value)
  ret.setHours(0, 0, 0, 0)
  return ret
}

function todayDateTimeLocalMin() {
  const today = startOfLocalDay(new Date())
  const month = String(today.getMonth() + 1).padStart(2, "0")
  const day = String(today.getDate()).padStart(2, "0")
  return `${today.getFullYear()}-${month}-${day}T00:00`
}

function buildForm(item: AdminAgentTeamSchedule | null, defaultValues?: Partial<CreateAdminAgentTeamSchedulePayload> | null): EditForm {
  if (!item) {
    return {
      teamId: defaultValues?.teamId ? String(defaultValues.teamId) : emptyForm.teamId,
      squadId: String(defaultValues?.squadId ?? 0),
      includedAgentProfileIds: defaultValues?.includedAgentProfileIds ?? [],
      excludedAgentProfileIds: defaultValues?.excludedAgentProfileIds ?? [],
      startAt: toDateTimeLocal(defaultValues?.startAt),
      endAt: toDateTimeLocal(defaultValues?.endAt),
      remark: defaultValues?.remark ?? emptyForm.remark,
    }
  }
  return {
    teamId: String(item.teamId),
    squadId: String(item.squadId ?? 0),
    includedAgentProfileIds: item.includedAgentProfileIds ?? [],
    excludedAgentProfileIds: item.excludedAgentProfileIds ?? [],
    startAt: toDateTimeLocal(item.startAt),
    endAt: toDateTimeLocal(item.endAt),
    remark: item.remark || "",
  }
}

function buildPayload(form: EditForm): CreateAdminAgentTeamSchedulePayload {
  return {
    teamId: Number(form.teamId),
    squadId: Number(form.squadId) || 0,
    includedAgentProfileIds: form.includedAgentProfileIds,
    excludedAgentProfileIds: form.excludedAgentProfileIds,
    startAt: form.startAt.trim(),
    endAt: form.endAt.trim(),
    remark: form.remark.trim(),
  }
}

export function EditDialog({
  open,
  saving,
  itemId,
  defaultValues,
  onOpenChange,
  onSubmit,
  onDelete,
}: ScheduleEditDialogProps) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      {open ? (
        <ScheduleEditDialogBody
          key={itemId ? `edit-${itemId}` : "create"}
          itemId={itemId}
          defaultValues={defaultValues}
          saving={saving}
          onOpenChange={onOpenChange}
          onSubmit={onSubmit}
          onDelete={onDelete}
        />
      ) : null}
    </Dialog>
  )
}

type ScheduleEditDialogBodyProps = Omit<ScheduleEditDialogProps, "open">

function ScheduleEditDialogBody({
  saving,
  itemId,
  defaultValues,
  onOpenChange,
  onSubmit,
  onDelete,
}: ScheduleEditDialogBodyProps) {
  const t = useI18n()
  const [teams, setTeams] = useState<AdminAgentTeam[]>([])
  const [squads, setSquads] = useState<AdminAgentTeamSquad[]>([])
  const [profiles, setProfiles] = useState<AdminAgentProfile[]>([])
  const [loading, setLoading] = useState(false)
  const loadOptions = useCallback(async () => {
    try {
      const teamsData = await fetchAgentTeamsAll()
      setTeams(teamsData.filter((team) => team.manageable))
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("agentTeamSchedule.loadOptionsFailed"))
    }
  }, [t])
  const editFormSchema = useMemo(() => createEditFormSchema(t), [t])
  const editFormResolver = useMemo(
    () => zodResolver(editFormSchema) as Resolver<EditForm>,
    [editFormSchema],
  )
  const form = useForm<EditForm>({
    resolver: editFormResolver,
    defaultValues: emptyForm,
  })
  const {
    control,
    getValues,
    handleSubmit,
    reset,
    register,
    setValue,
    watch,
    formState: { errors },
  } = form
  const minDateTime = todayDateTimeLocalMin()
  const selectedTeamId = Number(watch("teamId")) || 0
  const selectedSquadId = Number(watch("squadId")) || 0
  const includedAgentProfileIds = watch("includedAgentProfileIds")
  const excludedAgentProfileIds = watch("excludedAgentProfileIds")
  const plannedCoverage = useMemo(() => {
    const squad = squads.find((item) => item.id === selectedSquadId)
    const baseIds = new Set(selectedSquadId > 0 ? squad?.memberProfileIds ?? [] : profiles.map((profile) => profile.id))
    includedAgentProfileIds.forEach((profileId) => baseIds.add(profileId))
    excludedAgentProfileIds.forEach((profileId) => baseIds.delete(profileId))
    const eligible = profiles.filter((profile) => baseIds.has(profile.id) && profile.autoAssignEnabled && profile.maxConcurrentCount > 0)
    return {
      agentCount: eligible.length,
      capacity: eligible.reduce((total, profile) => total + profile.maxConcurrentCount, 0),
    }
  }, [excludedAgentProfileIds, includedAgentProfileIds, profiles, selectedSquadId, squads])

  useEffect(() => {
    async function loadDetail() {
      if (!itemId) {
        reset(buildForm(null, defaultValues))
        return
      }
      setLoading(true)
      try {
        const data = await fetchAgentTeamSchedule(itemId)
        reset(buildForm(data))
      } catch (error) {
        toast.error(error instanceof Error ? error.message : t("agentTeamSchedule.loadDetailFailed"))
      } finally {
        setLoading(false)
      }
    }
    void loadDetail()
  }, [defaultValues, itemId, reset, t])

  useEffect(() => {
    void loadOptions()
  }, [loadOptions])

  useEffect(() => {
    if (!selectedTeamId) {
      setSquads([])
      setProfiles([])
      return
    }
    let ignore = false
    void Promise.all([
      fetchAgentTeamSquads(selectedTeamId),
      fetchAgentProfilesAll({ teamId: selectedTeamId }),
    ])
      .then(([squadData, profileData]) => {
        if (ignore) return
        const enabledSquads = squadData.filter((item) => item.status === 0)
        setSquads(enabledSquads)
        setProfiles(profileData)
        const validProfileIds = new Set(profileData.map((profile) => profile.id))
        setValue("includedAgentProfileIds", getValues("includedAgentProfileIds").filter((id) => validProfileIds.has(id)))
        setValue("excludedAgentProfileIds", getValues("excludedAgentProfileIds").filter((id) => validProfileIds.has(id)))
        const currentSquadId = Number(getValues("squadId")) || 0
        if (currentSquadId > 0 && !enabledSquads.some((item) => item.id === currentSquadId)) {
          setValue("squadId", "0")
        }
      })
      .catch((error) => { if (!ignore) toast.error(error instanceof Error ? error.message : t("agentTeamSchedule.loadSquadsFailed")) })
    return () => { ignore = true }
  }, [getValues, selectedTeamId, setValue, t])

  async function onFormSubmit(values: EditForm) {
    await onSubmit(buildPayload(values))
  }

  return (
    <DialogContent className="max-w-2xl gap-0 p-0 sm:max-w-2xl">
      <DialogHeader className="px-6 pt-6">
        <DialogTitle>{itemId ? t("agentTeamSchedule.editTitle") : t("agentTeamSchedule.createTitle")}</DialogTitle>
      </DialogHeader>
      {loading ? (
        <div className="flex items-center justify-center py-12">
          <div className="text-muted-foreground">{t("agentTeamSchedule.loading")}</div>
        </div>
      ) : (
        <form onSubmit={handleSubmit(onFormSubmit)}>
          <div className="space-y-4 p-6">
            <div className="grid grid-cols-1 gap-4">
              <Field data-invalid={!!errors.teamId}>
                <FieldLabel>{t("agentTeamSchedule.team")}</FieldLabel>
                <FieldContent>
                  <Controller
                    control={control}
                    name="teamId"
                    render={({ field }) => (
                      <OptionCombobox
                        value={field.value}
                        options={teams.map((team) => ({
                          value: String(team.id),
                          label: team.name,
                        }))}
                        placeholder={t("agentTeamSchedule.teamRequired")}
                        searchPlaceholder={t("agentTeamSchedule.searchTeam")}
                        emptyText={t("agentTeamSchedule.emptyTeam")}
                        onChange={field.onChange}
                      />
                    )}
                  />
                  <FieldError errors={[errors.teamId]} />
                </FieldContent>
              </Field>
              <Field data-invalid={!!errors.squadId}>
                <FieldLabel>{t("agentTeamSchedule.squad")}</FieldLabel>
                <FieldContent>
                  <Controller
                    control={control}
                    name="squadId"
                    render={({ field }) => (
                      <OptionCombobox
                        value={field.value}
                        options={[
                          { value: "0", label: t("agentTeamSchedule.wholeTeamDuty") },
                          ...squads.map((squad) => ({ value: String(squad.id), label: squad.name })),
                        ]}
                        placeholder={t("agentTeamSchedule.wholeTeamDuty")}
                        searchPlaceholder={t("agentTeamSchedule.searchSquad")}
                        emptyText={t("agentTeamSchedule.emptySquad")}
                        disabled={!selectedTeamId}
                        onChange={field.onChange}
                      />
                    )}
                  />
                  <FieldError errors={[errors.squadId]} />
                </FieldContent>
              </Field>
            </div>
            <ScheduleAgentOverrides
              profiles={profiles}
              includedProfileIds={includedAgentProfileIds}
              excludedProfileIds={excludedAgentProfileIds}
              disabled={!selectedTeamId}
              onChange={(included, excluded) => {
                setValue("includedAgentProfileIds", included, { shouldDirty: true })
                setValue("excludedAgentProfileIds", excluded, { shouldDirty: true })
              }}
            />
            <div className="flex flex-wrap items-center justify-between gap-2 text-sm">
              <span>{t("agentTeamSchedule.plannedCoverage", { count: plannedCoverage.agentCount, capacity: plannedCoverage.capacity })}</span>
              {plannedCoverage.agentCount <= 1 ? (
                <span className="text-destructive">
                  {plannedCoverage.agentCount === 0
                    ? t("agentTeamSchedule.noPlannedAgentWarning")
                    : t("agentTeamSchedule.singlePlannedAgentWarning")}
                </span>
              ) : null}
            </div>
            <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
              <Field data-invalid={!!errors.startAt}>
                <FieldLabel htmlFor="agent-team-schedule-start-at">{t("agentTeamSchedule.startTime")}</FieldLabel>
                <FieldContent>
                  <Input id="agent-team-schedule-start-at" type="datetime-local" min={minDateTime} {...register("startAt")} />
                  <FieldError errors={[errors.startAt]} />
                </FieldContent>
              </Field>
              <Field data-invalid={!!errors.endAt}>
                <FieldLabel htmlFor="agent-team-schedule-end-at">{t("agentTeamSchedule.endTime")}</FieldLabel>
                <FieldContent>
                  <Input id="agent-team-schedule-end-at" type="datetime-local" min={minDateTime} {...register("endAt")} />
                  <FieldError errors={[errors.endAt]} />
                </FieldContent>
              </Field>
            </div>
            <Field>
              <FieldLabel htmlFor="agent-team-schedule-remark">{t("agentTeamSchedule.remark")}</FieldLabel>
              <FieldContent>
                <Textarea id="agent-team-schedule-remark" rows={4} placeholder={t("agentTeamSchedule.remarkPlaceholder")} {...register("remark")} />
              </FieldContent>
            </Field>
          </div>
          <DialogFooter className="mx-0 mb-0 px-6 py-4">
            {itemId && onDelete ? (
              <Button type="button" variant="destructive" onClick={() => void onDelete(itemId)} disabled={saving}>
                {t("agentTeamSchedule.delete")}
              </Button>
            ) : null}
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)} disabled={saving}>
              {t("agentTeamSchedule.cancel")}
            </Button>
            <Button type="submit" disabled={saving || loading}>
              {saving ? t("agentTeamSchedule.saving") : t("agentTeamSchedule.save")}
            </Button>
          </DialogFooter>
        </form>
      )}
    </DialogContent>
  )
}

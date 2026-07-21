"use client"

import { useEffect, useMemo, useRef, useState } from "react"
import { ArrowLeftIcon, CheckIcon, Loader2Icon, PlusIcon, XIcon } from "lucide-react"
import { toast } from "sonner"

import { OptionCombobox } from "@/components/option-combobox"
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
import { Label } from "@/components/ui/label"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { Textarea } from "@/components/ui/textarea"
import {
  fetchAgentTeamsAll,
  fetchAgentProfilesAll,
  fetchAgentTeamSquads,
  generateAgentTeamScheduleBatch,
  previewAgentTeamScheduleBatch,
  type AdminAgentTeam,
  type AdminAgentProfile,
  type AdminAgentTeamSquad,
  type AdminAgentTeamScheduleBatchPreview,
  type BatchAdminAgentTeamSchedulePayload,
} from "@/lib/api/admin"
import { useI18n } from "@/i18n/provider"
import { cn } from "@/lib/utils"
import { ScheduleAgentOverrides } from "./agent-overrides"

type BatchScheduleDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  onSuccess: (created: number) => void | Promise<void>
}

type TFunction = (key: string, values?: Record<string, string | number>) => string

const weekdayKeys = [
  "weekdayMon",
  "weekdayTue",
  "weekdayWed",
  "weekdayThu",
  "weekdayFri",
  "weekdaySat",
  "weekdaySun",
] as const

function getWeekdayOptions(t: TFunction) {
  return weekdayKeys.map((key, index) => ({
    value: index + 1,
    label: t(`agentTeamSchedule.${key}`),
  }))
}

function todayDateValue() {
  const today = new Date()
  const year = today.getFullYear()
  const month = String(today.getMonth() + 1).padStart(2, "0")
  const day = String(today.getDate()).padStart(2, "0")
  return `${year}-${month}-${day}`
}

function defaultFormState() {
  const today = todayDateValue()
  return {
    selectedTeamIds: [] as number[],
    squadId: 0,
    includedAgentProfileIds: [] as number[],
    excludedAgentProfileIds: [] as number[],
    startDate: today,
    endDate: today,
    weekdays: [1, 2, 3, 4, 5],
    timeRanges: [{ startTime: "09:00", endTime: "18:00" }],
    remark: "",
  }
}

type BatchFormState = ReturnType<typeof defaultFormState>
type DialogStep = "form" | "preview"

function buildPayload(form: BatchFormState): BatchAdminAgentTeamSchedulePayload {
  const firstRange = form.timeRanges[0] ?? { startTime: "", endTime: "" }
  return {
    teamIds: [...form.selectedTeamIds],
    squadId: form.squadId,
    includedAgentProfileIds: [...form.includedAgentProfileIds],
    excludedAgentProfileIds: [...form.excludedAgentProfileIds],
    startDate: form.startDate,
    endDate: form.endDate,
    weekdays: [...form.weekdays],
    startTime: firstRange.startTime,
    endTime: firstRange.endTime,
    timeRanges: form.timeRanges.map((item) => ({ ...item })),
    remark: form.remark.trim(),
  }
}

function getWeekdayLabel(value: number, t: TFunction) {
  const weekdayOptions = getWeekdayOptions(t)
  return weekdayOptions.find((option) => option.value === value)?.label ?? String(value)
}

function validateForm(form: BatchFormState, t: TFunction) {
  const today = todayDateValue()
  if (form.selectedTeamIds.length === 0) {
    return t("agentTeamSchedule.selectAtLeastOneTeam")
  }
  if (!form.startDate || !form.endDate) {
    return t("agentTeamSchedule.selectDateRange")
  }
  if (form.startDate < today) {
    return t("agentTeamSchedule.startBeforeToday")
  }
  if (form.endDate < form.startDate) {
    return t("agentTeamSchedule.endBeforeStart")
  }
  if (form.weekdays.length === 0) {
    return t("agentTeamSchedule.selectAtLeastOneWeekday")
  }
  if (form.timeRanges.length === 0 || form.timeRanges.some((item) => !item.startTime || !item.endTime)) {
    return t("agentTeamSchedule.selectStartEndTime")
  }
  if (form.timeRanges.some((item) => item.endTime === item.startTime)) {
    return t("agentTeamSchedule.batchTimeCannotMatch")
  }
  const rangeKeys = form.timeRanges.map((item) => `${item.startTime}-${item.endTime}`)
  if (new Set(rangeKeys).size !== rangeKeys.length) {
    return t("agentTeamSchedule.duplicateTimeRange")
  }
  return ""
}

export function BatchScheduleDialog({
  open,
  onOpenChange,
  onSuccess,
}: BatchScheduleDialogProps) {
  const t = useI18n()
  const [teams, setTeams] = useState<AdminAgentTeam[]>([])
  const [squads, setSquads] = useState<AdminAgentTeamSquad[]>([])
  const [profiles, setProfiles] = useState<AdminAgentProfile[]>([])
  const [form, setForm] = useState(defaultFormState)
  const [step, setStep] = useState<DialogStep>("form")
  const [preview, setPreview] = useState<AdminAgentTeamScheduleBatchPreview | null>(null)
  const [previewPayload, setPreviewPayload] = useState<BatchAdminAgentTeamSchedulePayload | null>(null)
  const [loadingTeams, setLoadingTeams] = useState(false)
  const [previewing, setPreviewing] = useState(false)
  const [submitting, setSubmitting] = useState(false)
  const openRef = useRef(open)
  const previewRequestIdRef = useRef(0)
  const busy = loadingTeams || previewing || submitting
  const weekdayOptions = useMemo(() => getWeekdayOptions(t), [t])

  const teamOptions = useMemo(
    () =>
      teams
        .filter((team) => !form.selectedTeamIds.includes(team.id))
        .map((team) => ({ value: String(team.id), label: team.name })),
    [form.selectedTeamIds, teams]
  )

  const selectedTeams = useMemo(() => {
    const teamMap = new Map(teams.map((team) => [team.id, team]))
    return form.selectedTeamIds.map((teamId) => teamMap.get(teamId)).filter(Boolean) as AdminAgentTeam[]
  }, [form.selectedTeamIds, teams])

  const selectedWeekdays = useMemo(
    () => new Set(form.weekdays),
    [form.weekdays]
  )

  const hasConflict = preview?.conflict === true

  useEffect(() => {
    openRef.current = open
  }, [open])

  useEffect(() => {
    if (!open) {
      previewRequestIdRef.current += 1
      setForm(defaultFormState())
      setStep("form")
      setPreview(null)
      setPreviewPayload(null)
      setPreviewing(false)
      setSubmitting(false)
      return
    }

    let ignore = false
    async function loadTeams() {
      setLoadingTeams(true)
      try {
        const data = await fetchAgentTeamsAll()
        if (!ignore) {
        setTeams(data.filter((team) => team.manageable))
        }
      } catch (error) {
        if (!ignore) {
          toast.error(error instanceof Error ? error.message : t("agentTeamSchedule.loadTeamsFailed"))
        }
      } finally {
        if (!ignore) {
          setLoadingTeams(false)
        }
      }
    }

    void loadTeams()
    return () => {
      ignore = true
    }
  }, [open, t])

  useEffect(() => {
    if (!open || form.selectedTeamIds.length !== 1) {
      setSquads([])
      setProfiles([])
      return
    }
    let ignore = false
    void Promise.all([
      fetchAgentTeamSquads(form.selectedTeamIds[0]),
      fetchAgentProfilesAll({ teamId: form.selectedTeamIds[0] }),
    ])
      .then(([squadData, profileData]) => {
        if (ignore) return
        setSquads(squadData.filter((item) => item.status === 0))
        setProfiles(profileData)
      })
      .catch((error) => { if (!ignore) toast.error(error instanceof Error ? error.message : t("agentTeamSchedule.loadSquadsFailed")) })
    return () => { ignore = true }
  }, [form.selectedTeamIds, open, t])

  function updateForm(values: Partial<BatchFormState>) {
    previewRequestIdRef.current += 1
    setForm((current) => ({ ...current, ...values }))
    setPreview(null)
    setPreviewPayload(null)
    setPreviewing(false)
    setStep("form")
  }

  function handleOpenChange(nextOpen: boolean) {
    if (!nextOpen && busy) {
      return
    }
    onOpenChange(nextOpen)
  }

  function handleTeamSelect(value: string) {
    const teamId = Number(value)
    if (!Number.isFinite(teamId) || form.selectedTeamIds.includes(teamId)) {
      return
    }
    const selectedTeamIds = [...form.selectedTeamIds, teamId]
    updateForm({
      selectedTeamIds,
      squadId: selectedTeamIds.length === 1 ? form.squadId : 0,
      includedAgentProfileIds: [],
      excludedAgentProfileIds: [],
    })
  }

  function removeTeam(teamId: number) {
    const selectedTeamIds = form.selectedTeamIds.filter((selectedTeamId) => selectedTeamId !== teamId)
    updateForm({
      selectedTeamIds,
      squadId: selectedTeamIds.length === 1 ? form.squadId : 0,
      includedAgentProfileIds: [],
      excludedAgentProfileIds: [],
    })
  }

  function toggleWeekday(weekday: number) {
    const nextWeekdays = selectedWeekdays.has(weekday)
      ? form.weekdays.filter((value) => value !== weekday)
      : [...form.weekdays, weekday].sort((a, b) => a - b)
    updateForm({ weekdays: nextWeekdays })
  }

  function updateTimeRange(index: number, field: "startTime" | "endTime", value: string) {
    updateForm({
      timeRanges: form.timeRanges.map((item, itemIndex) =>
        itemIndex === index ? { ...item, [field]: value } : item
      ),
    })
  }

  function addTimeRange() {
    updateForm({
      timeRanges: [...form.timeRanges, { startTime: "09:00", endTime: "18:00" }],
    })
  }

  function removeTimeRange(index: number) {
    if (form.timeRanges.length <= 1) return
    updateForm({
      timeRanges: form.timeRanges.filter((_, itemIndex) => itemIndex !== index),
    })
  }

  async function handlePreview() {
    const validationMessage = validateForm(form, t)
    if (validationMessage) {
      toast.error(validationMessage)
      return
    }

    const payload = buildPayload(form)
    const requestId = previewRequestIdRef.current + 1
    previewRequestIdRef.current = requestId
    setPreviewing(true)
    try {
      const data = await previewAgentTeamScheduleBatch(payload)
      if (!openRef.current || previewRequestIdRef.current !== requestId) {
        return
      }
      setPreview(data)
      setPreviewPayload(payload)
      setStep("preview")
    } catch (error) {
      if (openRef.current && previewRequestIdRef.current === requestId) {
        toast.error(error instanceof Error ? error.message : t("agentTeamSchedule.previewFailed"))
      }
    } finally {
      if (openRef.current && previewRequestIdRef.current === requestId) {
        setPreviewing(false)
      }
    }
  }

  async function handleSubmit() {
    if (!preview || !previewPayload) {
      toast.error(t("agentTeamSchedule.previewFirst"))
      return
    }
    if (preview.conflict) {
      toast.error(t("agentTeamSchedule.conflictCannotSubmit"))
      return
    }

    const payload = previewPayload
    setSubmitting(true)
    try {
      const data = await generateAgentTeamScheduleBatch(payload)
      toast.success(t("agentTeamSchedule.batchCreated", { count: data.created }))
      setPreview(null)
      setPreviewPayload(null)
      onOpenChange(false)
      try {
        await onSuccess(data.created)
      } catch (error) {
        toast.error(error instanceof Error ? t("agentTeamSchedule.batchRefreshFailed", { message: error.message }) : t("agentTeamSchedule.batchRefreshFailedFallback"))
      }
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("agentTeamSchedule.generateFailed"))
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent className="flex max-h-[calc(100vh-2rem)] flex-col gap-0 overflow-hidden p-0 sm:max-w-4xl">
        <DialogHeader className="shrink-0 px-6 pt-6">
          <DialogTitle>{t("agentTeamSchedule.batch")}</DialogTitle>
          <DialogDescription>
            {t("agentTeamSchedule.batchDescription")}
          </DialogDescription>
        </DialogHeader>

        <div className="min-h-0 flex-1 overflow-y-auto px-6 py-5">
          {step === "form" ? (
            <div className="space-y-5">
              <div className="space-y-2">
                <Label>{t("agentTeamSchedule.team")}</Label>
                <div className="flex gap-2">
                  <div className="min-w-0 flex-1">
                    <OptionCombobox
                      value=""
                      options={teamOptions}
                      placeholder={loadingTeams ? t("agentTeamSchedule.loadingTeams") : t("agentTeamSchedule.addTeam")}
                      searchPlaceholder={t("agentTeamSchedule.searchTeam")}
                      emptyText={teams.length === 0 ? t("agentTeamSchedule.noTeams") : t("agentTeamSchedule.allTeamsSelected")}
                      disabled={loadingTeams}
                      onChange={handleTeamSelect}
                    />
                  </div>
                </div>
                {selectedTeams.length > 0 ? (
                  <div className="flex flex-wrap gap-2">
                    {selectedTeams.map((team) => (
                      <Badge key={team.id} variant="secondary" className="gap-1 pr-1">
                        <span className="max-w-44 truncate">{team.name}</span>
                        <Button
                          type="button"
                          variant="ghost"
                          size="icon-sm"
                          className="size-5 rounded-sm"
                          onClick={() => removeTeam(team.id)}
                          aria-label={t("agentTeamSchedule.removeTeam", { name: team.name })}
                        >
                          <XIcon className="size-3" />
                        </Button>
                      </Badge>
                    ))}
                  </div>
                ) : (
                  <div className="text-sm text-muted-foreground">{t("agentTeamSchedule.noSelectedTeams")}</div>
                )}
              </div>

              <div className="space-y-2">
                <Label>{t("agentTeamSchedule.squad")}</Label>
                <OptionCombobox
                  value={String(form.squadId)}
                  options={[
                    { value: "0", label: t("agentTeamSchedule.wholeTeamDuty") },
                    ...squads.map((squad) => ({ value: String(squad.id), label: squad.name })),
                  ]}
                  placeholder={t("agentTeamSchedule.wholeTeamDuty")}
                  searchPlaceholder={t("agentTeamSchedule.searchSquad")}
                  emptyText={t("agentTeamSchedule.emptySquad")}
                  disabled={form.selectedTeamIds.length !== 1}
                  onChange={(value) => updateForm({ squadId: Number(value) || 0 })}
                />
                <p className="text-xs text-muted-foreground">
                  {form.selectedTeamIds.length === 1 ? t("agentTeamSchedule.squadHint") : t("agentTeamSchedule.squadSingleTeamHint")}
                </p>
              </div>

              <ScheduleAgentOverrides
                profiles={profiles}
                includedProfileIds={form.includedAgentProfileIds}
                excludedProfileIds={form.excludedAgentProfileIds}
                disabled={form.selectedTeamIds.length !== 1}
                onChange={(includedAgentProfileIds, excludedAgentProfileIds) =>
                  updateForm({ includedAgentProfileIds, excludedAgentProfileIds })
                }
              />

              <div className="grid gap-4 sm:grid-cols-2">
                <div className="space-y-2">
                  <Label htmlFor="batch-schedule-start-date">{t("agentTeamSchedule.startDate")}</Label>
                  <Input
                    id="batch-schedule-start-date"
                    type="date"
                    min={todayDateValue()}
                    value={form.startDate}
                    onChange={(event) => updateForm({ startDate: event.target.value })}
                  />
                </div>
                <div className="space-y-2">
                  <Label htmlFor="batch-schedule-end-date">{t("agentTeamSchedule.endDate")}</Label>
                  <Input
                    id="batch-schedule-end-date"
                    type="date"
                    min={form.startDate || todayDateValue()}
                    value={form.endDate}
                    onChange={(event) => updateForm({ endDate: event.target.value })}
                  />
                </div>
              </div>

              <div className="space-y-2">
                <Label>{t("agentTeamSchedule.weekday")}</Label>
                <div className="flex flex-wrap gap-2">
                  {weekdayOptions.map((option) => {
                    const selected = selectedWeekdays.has(option.value)
                    return (
                      <Button
                        key={option.value}
                        type="button"
                        variant={selected ? "default" : "outline"}
                        size="sm"
                        aria-pressed={selected}
                        onClick={() => toggleWeekday(option.value)}
                      >
                        {selected ? <CheckIcon className="size-4" /> : null}
                        {option.label}
                      </Button>
                    )
                  })}
                </div>
              </div>

              <div className="space-y-3">
                <div className="flex items-center justify-between gap-3">
                  <Label>{t("agentTeamSchedule.timeRanges")}</Label>
                  <Button type="button" variant="outline" size="sm" onClick={addTimeRange}>
                    <PlusIcon />
                    {t("agentTeamSchedule.addTimeRange")}
                  </Button>
                </div>
                <div className="space-y-3">
                  {form.timeRanges.map((item, index) => {
                    const overnight = Boolean(item.startTime && item.endTime && item.endTime < item.startTime)
                    return (
                      <div key={index} className="grid items-end gap-3 border-b pb-3 last:border-b-0 last:pb-0 sm:grid-cols-[1fr_1fr_auto]">
                        <div className="space-y-2">
                          <Label htmlFor={`batch-schedule-start-time-${index}`}>
                            {t("agentTeamSchedule.startTime")}
                          </Label>
                          <Input
                            id={`batch-schedule-start-time-${index}`}
                            type="time"
                            value={item.startTime}
                            onChange={(event) => updateTimeRange(index, "startTime", event.target.value)}
                          />
                        </div>
                        <div className="space-y-2">
                          <div className="flex min-h-5 items-center gap-2">
                            <Label htmlFor={`batch-schedule-end-time-${index}`}>
                              {t("agentTeamSchedule.endTime")}
                            </Label>
                            {overnight ? <Badge variant="outline">{t("agentTeamSchedule.overnight")}</Badge> : null}
                          </div>
                          <Input
                            id={`batch-schedule-end-time-${index}`}
                            type="time"
                            value={item.endTime}
                            onChange={(event) => updateTimeRange(index, "endTime", event.target.value)}
                          />
                        </div>
                        <Button
                          type="button"
                          variant="ghost"
                          size="icon"
                          disabled={form.timeRanges.length <= 1}
                          onClick={() => removeTimeRange(index)}
                          aria-label={t("agentTeamSchedule.removeTimeRange")}
                          title={t("agentTeamSchedule.removeTimeRange")}
                        >
                          <XIcon />
                        </Button>
                      </div>
                    )
                  })}
                </div>
                <p className="text-xs text-muted-foreground">{t("agentTeamSchedule.timeRangesHint")}</p>
              </div>

              <div className="space-y-2">
                <Label htmlFor="batch-schedule-remark">{t("agentTeamSchedule.remark")}</Label>
                <Textarea
                  id="batch-schedule-remark"
                  rows={4}
                  placeholder={t("agentTeamSchedule.remarkPlaceholder")}
                  value={form.remark}
                  onChange={(event) => updateForm({ remark: event.target.value })}
                />
              </div>
            </div>
          ) : (
            <div className="space-y-4">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <div className="text-sm text-muted-foreground">
                  {hasConflict
                    ? t("agentTeamSchedule.previewSummaryConflict", { total: preview?.total ?? 0 })
                    : t("agentTeamSchedule.previewSummaryReady", { total: preview?.total ?? 0 })}
                </div>
                {hasConflict ? (
                  <Badge variant="destructive">{t("agentTeamSchedule.hasConflict")}</Badge>
                ) : (
                  <Badge variant="secondary">{t("agentTeamSchedule.noConflict")}</Badge>
                )}
              </div>
              <div className="overflow-x-auto rounded-2xl border border-[#dbe7f6] bg-white shadow-[0_10px_24px_rgba(37,99,235,0.08)]">
                <div className="min-w-[760px]">
                  <Table>
                    <TableHeader className="bg-[#f6f9ff]">
                      <TableRow>
                        <TableHead>{t("agentTeamSchedule.team")}</TableHead>
                        <TableHead>{t("agentTeamSchedule.date")}</TableHead>
                        <TableHead>{t("agentTeamSchedule.weekday")}</TableHead>
                        <TableHead>{t("agentTeamSchedule.time")}</TableHead>
                        <TableHead>{t("agentTeamSchedule.coverage")}</TableHead>
                        <TableHead>{t("agentTeamSchedule.remark")}</TableHead>
                        <TableHead>{t("agentTeamSchedule.status")}</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {preview?.items.map((item, index) => (
                        <TableRow
                          key={`${item.teamId}-${item.startAt}-${index}`}
                          className={cn(
                            item.conflict && "bg-destructive/10 text-destructive hover:bg-destructive/10"
                          )}
                        >
                          <TableCell>
                            <div className="font-medium">{item.teamName || t("agentTeamSchedule.teamFallback", { id: item.teamId })}</div>
                            <div className="text-xs text-muted-foreground">{t("agentTeamSchedule.teamId", { id: item.teamId })}</div>
                          </TableCell>
                          <TableCell>{item.date}</TableCell>
                          <TableCell>{getWeekdayLabel(item.weekday, t)}</TableCell>
                          <TableCell>
                            {item.startAt.slice(11, 16)} - {item.endAt.slice(11, 16)}
                          </TableCell>
                          <TableCell>
                            <div>{t("agentTeamSchedule.coverageValue", { count: item.eligibleAgentCount, capacity: item.totalCapacity })}</div>
                            {item.coverageWarning ? (
                              <div className="mt-1 text-xs text-amber-700">{item.coverageWarning}</div>
                            ) : null}
                          </TableCell>
                          <TableCell className="max-w-56 truncate">{item.remark || "-"}</TableCell>
                          <TableCell>
                            {item.conflict ? (
                              <span className="font-medium">
                                {item.conflictReason || t("agentTeamSchedule.conflictFallback")}
                              </span>
                            ) : (
                              <span className="text-muted-foreground">{t("agentTeamSchedule.canGenerate")}</span>
                            )}
                          </TableCell>
                        </TableRow>
                      ))}
                      {preview && preview.items.length === 0 ? (
                        <TableRow>
                          <TableCell colSpan={7} className="py-10 text-center text-muted-foreground">
                            {t("agentTeamSchedule.emptyPreview")}
                          </TableCell>
                        </TableRow>
                      ) : null}
                    </TableBody>
                  </Table>
                </div>
              </div>
            </div>
          )}
        </div>

        <DialogFooter className="mx-0 mb-0 shrink-0 border-t px-6 py-4">
          {step === "preview" ? (
            <Button
              type="button"
              variant="outline"
              onClick={() => setStep("form")}
              disabled={submitting}
            >
              <ArrowLeftIcon />
              {t("agentTeamSchedule.backToEdit")}
            </Button>
          ) : null}
          <Button
            type="button"
            variant="outline"
            onClick={() => handleOpenChange(false)}
            disabled={busy}
          >
            {t("agentTeamSchedule.cancel")}
          </Button>
          {step === "form" ? (
            <Button type="button" onClick={() => void handlePreview()} disabled={busy}>
              {previewing ? <Loader2Icon className="animate-spin" /> : <CheckIcon />}
              {t("agentTeamSchedule.preview")}
            </Button>
          ) : (
            <Button
              type="button"
              onClick={() => void handleSubmit()}
              disabled={submitting || hasConflict || !preview || !previewPayload || preview.items.length === 0}
            >
              {submitting ? <Loader2Icon className="animate-spin" /> : <CheckIcon />}
              {t("agentTeamSchedule.generate")}
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

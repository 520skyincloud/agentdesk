"use client"

import {
  DndContext,
  DragOverlay,
  PointerSensor,
  pointerWithin,
  useDraggable,
  useDroppable,
  useSensor,
  useSensors,
  type DragEndEvent,
  type DragStartEvent,
} from "@dnd-kit/core"
import {
  CalendarDaysIcon,
  GripVerticalIcon,
  PencilIcon,
  PlusIcon,
  SearchIcon,
  Trash2Icon,
  UserMinusIcon,
  UsersRoundIcon,
} from "lucide-react"
import { useCallback, useEffect, useMemo, useState } from "react"
import { createPortal } from "react-dom"
import { toast } from "sonner"

import { OptionCombobox } from "@/components/option-combobox"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Input } from "@/components/ui/input"
import {
  createAgentTeamSquad,
  deleteAgentTeamSquad,
  fetchAgentProfiles,
  fetchAgentTeamSquads,
  replaceAgentTeamSquadMembers,
  updateAgentTeamSquad,
  type AdminAgentProfile,
  type AdminAgentTeam,
  type AdminAgentTeamSquad,
  type CreateAdminAgentTeamSquadPayload,
} from "@/lib/api/admin"
import { cn, formatDateTime } from "@/lib/utils"
import { useI18n } from "@/i18n/provider"
import { SquadEditDialog } from "./squad-edit"

type SquadArrangementProps = {
  team: AdminAgentTeam
  createRequestKey: number
  canCreate: boolean
  canUpdate: boolean
  canDelete: boolean
}

function DraggableAgent({ profile, selected, disabled = false, onSelectedChange }: {
  profile: AdminAgentProfile
  selected: boolean
  disabled?: boolean
  onSelectedChange: (checked: boolean) => void
}) {
  const t = useI18n()
  const { attributes, listeners, setNodeRef, isDragging } = useDraggable({
    id: `profile-${profile.id}`,
    data: { profileId: profile.id },
    disabled,
  })
  return (
    <div
      ref={setNodeRef}
      className={cn("flex items-center gap-2 border-b px-3 py-2.5 last:border-b-0", isDragging && "opacity-35")}
    >
      <button type="button" disabled={disabled} aria-label={t("agentProfile.dragAgentToSquad", { name: profile.displayName })} className="cursor-grab touch-none text-muted-foreground active:cursor-grabbing disabled:cursor-default disabled:opacity-40" {...listeners} {...attributes}>
        <GripVerticalIcon className="size-4" />
      </button>
      <Checkbox aria-label={t("agentProfile.selectAgentForSquad", { name: profile.displayName })} disabled={disabled} checked={selected} onCheckedChange={(value) => onSelectedChange(value === true)} />
      <div className="min-w-0 flex-1">
        <div className="truncate font-medium">{profile.displayName}</div>
        <div className="truncate text-xs text-muted-foreground">{profile.agentCode}</div>
      </div>
      <Badge variant="outline" className="tabular-nums">{profile.activeTaskCount}/{profile.maxConcurrentCount}</Badge>
    </div>
  )
}

function AgentDragPreview({ profile }: { profile: AdminAgentProfile }) {
  return (
    <div className="flex h-full w-full cursor-grabbing items-center gap-2 border border-primary/60 bg-background px-3 py-2.5 shadow-xl">
      <GripVerticalIcon className="size-4 shrink-0 text-primary" />
      <div className="min-w-0 flex-1">
        <div className="truncate font-medium">{profile.displayName}</div>
        <div className="truncate text-xs text-muted-foreground">{profile.agentCode}</div>
      </div>
      <Badge variant="outline" className="shrink-0 tabular-nums">{profile.activeTaskCount}/{profile.maxConcurrentCount}</Badge>
    </div>
  )
}

function SquadContainer({ squad, profiles, canUpdate = false, canDelete = false, onRemove, onEdit, onDelete, onSchedule }: {
  squad: AdminAgentTeamSquad
  profiles: AdminAgentProfile[]
  onRemove: (profileId: number) => void
  onEdit: () => void
  onDelete: () => void
  onSchedule: () => void
  canUpdate?: boolean
  canDelete?: boolean
}) {
  const t = useI18n()
  const { setNodeRef, isOver } = useDroppable({ id: `squad-${squad.id}`, data: { squadId: squad.id } })
  return (
    <section ref={setNodeRef} className={cn("border bg-background transition-colors", isOver && "border-primary bg-primary/5")}>
      <header className="flex items-start justify-between gap-3 border-b px-3 py-3">
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-2">
            <h3 className="font-medium">{squad.name}</h3>
            {squad.activeScheduleId > 0 ? <Badge>{t("agentProfile.onDuty")}</Badge> : <Badge variant="outline">{t("agentProfile.notOnDuty")}</Badge>}
          </div>
          <p className="mt-1 text-xs text-muted-foreground">
            {t("agentProfile.squadMemberCount", { count: profiles.length })}
            {squad.leaderName ? ` · ${t("agentProfile.squadLeaderName", { name: squad.leaderName })}` : ""}
          </p>
          {squad.nextScheduleStartAt ? <p className="mt-1 text-xs text-muted-foreground">{t("agentProfile.nextDuty", { time: formatDateTime(squad.nextScheduleStartAt) })}</p> : null}
        </div>
        <div className="flex shrink-0 gap-1">
          {canUpdate ? <Button variant="ghost" size="icon-sm" onClick={onSchedule} aria-label={t("agentProfile.scheduleSquad")}><CalendarDaysIcon /></Button> : null}
          {canUpdate ? <Button variant="ghost" size="icon-sm" onClick={onEdit} aria-label={t("agentProfile.editSquad")}><PencilIcon /></Button> : null}
          {canDelete ? <Button variant="ghost" size="icon-sm" onClick={onDelete} aria-label={t("agentProfile.deleteSquad")}><Trash2Icon /></Button> : null}
        </div>
      </header>
      <div className="min-h-16 p-2">
        {profiles.length ? profiles.map((profile) => (
          <div key={profile.id} className="flex items-center gap-2 px-2 py-2 hover:bg-muted/60">
            <UsersRoundIcon className="size-4 text-muted-foreground" />
            <span className="min-w-0 flex-1 truncate">{profile.displayName}</span>
            <span className="text-xs tabular-nums text-muted-foreground">{profile.activeTaskCount}/{profile.maxConcurrentCount}</span>
            {canUpdate ? <Button variant="ghost" size="icon-sm" onClick={() => onRemove(profile.id)} aria-label={t("agentProfile.removeFromSquad", { name: profile.displayName })}><UserMinusIcon /></Button> : null}
          </div>
        )) : <div className="flex min-h-16 items-center justify-center text-sm text-muted-foreground">{t("agentProfile.dropAgentsHere")}</div>}
      </div>
    </section>
  )
}

export function SquadArrangement({ team, createRequestKey, canCreate, canUpdate, canDelete }: SquadArrangementProps) {
  const t = useI18n()
  const sensors = useSensors(useSensor(PointerSensor, { activationConstraint: { distance: 6 } }))
  const [profiles, setProfiles] = useState<AdminAgentProfile[]>([])
  const [squads, setSquads] = useState<AdminAgentTeamSquad[]>([])
  const [keyword, setKeyword] = useState("")
  const [selectedIds, setSelectedIds] = useState<number[]>([])
  const [bulkSquadId, setBulkSquadId] = useState("")
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [dialogOpen, setDialogOpen] = useState(false)
  const [editingSquad, setEditingSquad] = useState<AdminAgentTeamSquad | null>(null)
  const [activeProfileId, setActiveProfileId] = useState(0)

  const loadData = useCallback(async () => {
    setLoading(true)
    try {
      const [profileData, squadData] = await Promise.all([
        fetchAgentProfiles({ teamId: team.id, page: 1, limit: 200 }).then((page) => page.results),
        fetchAgentTeamSquads(team.id),
      ])
      setProfiles(profileData)
      setSquads(squadData.map((squad) => ({
        ...squad,
        memberProfileIds: squad.memberProfileIds ?? [],
      })))
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("agentProfile.loadSquadsFailed"))
    } finally {
      setLoading(false)
    }
  }, [t, team.id])

  useEffect(() => { void loadData() }, [loadData])
  useEffect(() => {
    if (createRequestKey > 0 && canCreate) {
      setEditingSquad(null)
      setDialogOpen(true)
    }
  }, [canCreate, createRequestKey])

  const profileMap = useMemo(() => new Map(profiles.map((profile) => [profile.id, profile])), [profiles])
  const activeProfile = activeProfileId > 0 ? profileMap.get(activeProfileId) : undefined
  const filteredProfiles = useMemo(() => {
    const query = keyword.trim().toLowerCase()
    return profiles.filter((profile) => !query || profile.displayName.toLowerCase().includes(query) || profile.agentCode.toLowerCase().includes(query))
  }, [keyword, profiles])

  async function saveMembers(squad: AdminAgentTeamSquad, memberProfileIds: number[], undoIds?: number[]) {
    const uniqueIds = [...new Set(memberProfileIds)]
    setSquads((items) => items.map((item) => item.id === squad.id ? { ...item, memberProfileIds: uniqueIds } : item))
    try {
      await replaceAgentTeamSquadMembers({ squadId: squad.id, agentProfileIds: uniqueIds })
      toast.success(t("agentProfile.squadMembersSaved"), undoIds ? {
        action: { label: t("agentProfile.undo"), onClick: () => void saveMembers(squad, undoIds) },
      } : undefined)
    } catch (error) {
      setSquads((items) => items.map((item) => item.id === squad.id ? squad : item))
      toast.error(error instanceof Error ? error.message : t("agentProfile.saveSquadMembersFailed"))
    }
  }

  function handleDragStart(event: DragStartEvent) {
    setActiveProfileId(Number(event.active.data.current?.profileId) || 0)
  }

  function handleDragEnd(event: DragEndEvent) {
    setActiveProfileId(0)
    const profileId = Number(event.active.data.current?.profileId)
    const squadId = Number(event.over?.data.current?.squadId)
    const squad = squads.find((item) => item.id === squadId)
    if (!profileId || !squad || squad.memberProfileIds.includes(profileId)) return
    void saveMembers(squad, [...squad.memberProfileIds, profileId], squad.memberProfileIds)
  }

  async function handleSaveSquad(payload: CreateAdminAgentTeamSquadPayload) {
    setSaving(true)
    try {
      if (editingSquad) await updateAgentTeamSquad({ id: editingSquad.id, ...payload })
      else await createAgentTeamSquad(payload)
      toast.success(t(editingSquad ? "agentProfile.squadUpdated" : "agentProfile.squadCreated"))
      setDialogOpen(false)
      setEditingSquad(null)
      await loadData()
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("agentProfile.saveSquadFailed"))
    } finally { setSaving(false) }
  }

  async function handleDelete(squad: AdminAgentTeamSquad) {
    if (!window.confirm(t("agentProfile.confirmDeleteSquad", { name: squad.name }))) return
    try {
      await deleteAgentTeamSquad(squad.id)
      setSquads((items) => items.filter((item) => item.id !== squad.id))
      toast.success(t("agentProfile.squadDeleted"))
    } catch (error) { toast.error(error instanceof Error ? error.message : t("agentProfile.deleteSquadFailed")) }
  }

  function openSchedule(squad: AdminAgentTeamSquad) {
    window.location.href = `/dashboard/agent-team-schedules?teamId=${team.id}&squadId=${squad.id}&action=create`
  }

  if (loading) return <div className="py-16 text-center text-muted-foreground">{t("agentProfile.loading")}</div>

  return (
    <DndContext sensors={sensors} autoScroll={false} collisionDetection={pointerWithin} onDragStart={handleDragStart} onDragCancel={() => setActiveProfileId(0)} onDragEnd={handleDragEnd}>
      <div className="grid min-h-0 gap-4 xl:h-full xl:grid-cols-[minmax(280px,0.8fr)_minmax(420px,1.4fr)]">
        <section className="flex max-h-[520px] min-h-0 flex-col border bg-background xl:max-h-none">
          <header className="border-b p-3">
            <div className="flex items-center justify-between"><div><h2 className="font-medium">{t("agentProfile.teamAgentPool")}</h2><p className="text-xs text-muted-foreground">{t("agentProfile.teamAgentPoolCount", { count: profiles.length })}</p></div></div>
            <div className="relative mt-3"><SearchIcon className="absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" /><Input value={keyword} onChange={(event) => setKeyword(event.target.value)} className="pl-9" placeholder={t("agentProfile.searchAgent")} /></div>
            <div className="mt-3 flex flex-col gap-2 sm:flex-row">
              <div className="min-w-0 flex-1">
                <OptionCombobox disabled={!canUpdate} value={bulkSquadId} options={squads.map((squad) => ({ value: String(squad.id), label: squad.name }))} placeholder={t("agentProfile.selectSquad")} onChange={setBulkSquadId} />
              </div>
              <Button className="w-full shrink-0 sm:w-auto" disabled={!canUpdate || !bulkSquadId || selectedIds.length === 0} onClick={() => {
                const squad = squads.find((item) => String(item.id) === bulkSquadId)
                if (squad) void saveMembers(squad, [...squad.memberProfileIds, ...selectedIds], squad.memberProfileIds)
              }}>{t("agentProfile.addSelectedToSquad", { count: selectedIds.length })}</Button>
            </div>
          </header>
          <div className="min-h-0 flex-1 overflow-y-auto">
            {filteredProfiles.map((profile) => <DraggableAgent key={profile.id} profile={profile} disabled={!canUpdate} selected={selectedIds.includes(profile.id)} onSelectedChange={(checked) => setSelectedIds((ids) => checked ? [...ids, profile.id] : ids.filter((id) => id !== profile.id))} />)}
          </div>
        </section>
        <section className="min-h-0 space-y-3 pr-1 xl:overflow-y-auto">
          <div className="flex items-center justify-between gap-3"><div><h2 className="font-medium">{t("agentProfile.squadArrangement")}</h2><p className="text-xs text-muted-foreground">{t("agentProfile.squadArrangementSummary", { count: squads.length })}</p></div>{canCreate ? <Button className="shrink-0 whitespace-nowrap" onClick={() => { setEditingSquad(null); setDialogOpen(true) }}><PlusIcon />{t("agentProfile.newSquad")}</Button> : null}</div>
          {squads.length ? squads.map((squad) => <SquadContainer key={squad.id} squad={squad} profiles={squad.memberProfileIds.map((id) => profileMap.get(id)).filter(Boolean) as AdminAgentProfile[]} canUpdate={canUpdate} canDelete={canDelete} onRemove={(profileId) => void saveMembers(squad, squad.memberProfileIds.filter((id) => id !== profileId), squad.memberProfileIds)} onEdit={() => { setEditingSquad(squad); setDialogOpen(true) }} onDelete={() => void handleDelete(squad)} onSchedule={() => openSchedule(squad)} />) : <div className="border border-dashed py-16 text-center text-muted-foreground">{t("agentProfile.noSquads")}</div>}
        </section>
      </div>
      <SquadEditDialog open={dialogOpen} saving={saving} teamId={team.id} item={editingSquad} profiles={profiles} onOpenChange={setDialogOpen} onSubmit={handleSaveSquad} />
      {activeProfile ? createPortal(
        <DragOverlay dropAnimation={null}>
          <AgentDragPreview profile={activeProfile} />
        </DragOverlay>,
        document.body,
      ) : null}
    </DndContext>
  )
}

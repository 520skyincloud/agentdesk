"use client"

import { XIcon } from "lucide-react"

import { OptionCombobox } from "@/components/option-combobox"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Label } from "@/components/ui/label"
import { useI18n } from "@/i18n/provider"
import type { AdminAgentProfile } from "@/lib/api/admin"

type ScheduleAgentOverridesProps = {
  profiles: AdminAgentProfile[]
  includedProfileIds: number[]
  excludedProfileIds: number[]
  disabled?: boolean
  onChange: (includedProfileIds: number[], excludedProfileIds: number[]) => void
}

function profileLabel(profile: AdminAgentProfile) {
  return profile.displayName || profile.nickname || profile.username || `#${profile.id}`
}

export function ScheduleAgentOverrides({
  profiles,
  includedProfileIds,
  excludedProfileIds,
  disabled = false,
  onChange,
}: ScheduleAgentOverridesProps) {
  const t = useI18n()
  const profileById = new Map(profiles.map((profile) => [profile.id, profile]))
  const selectedIds = new Set([...includedProfileIds, ...excludedProfileIds])
  const availableOptions = profiles
    .filter((profile) => !selectedIds.has(profile.id))
    .map((profile) => ({ value: String(profile.id), label: profileLabel(profile) }))

  function addIncluded(value: string) {
    const profileId = Number(value)
    if (profileId > 0 && !selectedIds.has(profileId)) {
      onChange([...includedProfileIds, profileId], excludedProfileIds)
    }
  }

  function addExcluded(value: string) {
    const profileId = Number(value)
    if (profileId > 0 && !selectedIds.has(profileId)) {
      onChange(includedProfileIds, [...excludedProfileIds, profileId])
    }
  }

  function renderSelected(ids: number[], remove: (profileId: number) => void) {
    if (ids.length === 0) {
      return <div className="text-xs text-muted-foreground">{t("agentTeamSchedule.overrideNone")}</div>
    }
    return (
      <div className="flex flex-wrap gap-1.5">
        {ids.map((profileId) => (
          <Badge key={profileId} variant="secondary" className="gap-1 pr-1">
            <span className="max-w-40 truncate">
              {profileById.get(profileId) ? profileLabel(profileById.get(profileId)!) : `#${profileId}`}
            </span>
            <Button
              type="button"
              variant="ghost"
              size="icon-sm"
              className="size-5 rounded-sm"
              disabled={disabled}
              onClick={() => remove(profileId)}
              aria-label={t("agentTeamSchedule.removeOverrideAgent")}
            >
              <XIcon className="size-3" />
            </Button>
          </Badge>
        ))}
      </div>
    )
  }

  return (
    <div className="grid gap-4 border-y py-4 sm:grid-cols-2">
      <div className="space-y-2">
        <Label>{t("agentTeamSchedule.includedAgents")}</Label>
        <OptionCombobox
          value=""
          options={availableOptions}
          placeholder={t("agentTeamSchedule.addIncludedAgent")}
          searchPlaceholder={t("agentTeamSchedule.searchAgent")}
          emptyText={t("agentTeamSchedule.noOverrideAgents")}
          disabled={disabled || profiles.length === 0}
          onChange={addIncluded}
        />
        {renderSelected(includedProfileIds, (profileId) =>
          onChange(includedProfileIds.filter((id) => id !== profileId), excludedProfileIds)
        )}
        <p className="text-xs text-muted-foreground">{t("agentTeamSchedule.includedAgentsHint")}</p>
      </div>
      <div className="space-y-2">
        <Label>{t("agentTeamSchedule.excludedAgents")}</Label>
        <OptionCombobox
          value=""
          options={availableOptions}
          placeholder={t("agentTeamSchedule.addExcludedAgent")}
          searchPlaceholder={t("agentTeamSchedule.searchAgent")}
          emptyText={t("agentTeamSchedule.noOverrideAgents")}
          disabled={disabled || profiles.length === 0}
          onChange={addExcluded}
        />
        {renderSelected(excludedProfileIds, (profileId) =>
          onChange(includedProfileIds, excludedProfileIds.filter((id) => id !== profileId))
        )}
        <p className="text-xs text-muted-foreground">{t("agentTeamSchedule.excludedAgentsHint")}</p>
      </div>
    </div>
  )
}

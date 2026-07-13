"use client"

import { zodResolver } from "@hookform/resolvers/zod"
import { useEffect, useMemo } from "react"
import { Controller, type Resolver, useForm } from "react-hook-form"
import { z } from "zod/v4"

import { OptionCombobox } from "@/components/option-combobox"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Field, FieldContent, FieldError, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"
import type {
  AdminAgentProfile,
  AdminAgentTeamSquad,
  CreateAdminAgentTeamSquadPayload,
} from "@/lib/api/admin"
import { Status } from "@/lib/generated/enums"
import { useI18n } from "@/i18n/provider"

type FormValues = {
  name: string
  leaderUserId: string
  remark: string
}

type SquadEditDialogProps = {
  open: boolean
  saving: boolean
  teamId: number
  item: AdminAgentTeamSquad | null
  profiles: AdminAgentProfile[]
  onOpenChange: (open: boolean) => void
  onSubmit: (payload: CreateAdminAgentTeamSquadPayload) => Promise<void>
}

export function SquadEditDialog({
  open,
  saving,
  teamId,
  item,
  profiles,
  onOpenChange,
  onSubmit,
}: SquadEditDialogProps) {
  const t = useI18n()
  const schema = useMemo(
    () => z.object({
      name: z.string().trim().min(1, t("agentProfile.squadNameRequired")),
      leaderUserId: z.string(),
      remark: z.string(),
    }),
    [t],
  )
  const form = useForm<FormValues>({
    resolver: zodResolver(schema) as Resolver<FormValues>,
    defaultValues: { name: "", leaderUserId: "0", remark: "" },
  })

  useEffect(() => {
    if (!open) return
    form.reset({
      name: item?.name ?? "",
      leaderUserId: String(item?.leaderUserId ?? 0),
      remark: item?.remark ?? "",
    })
  }, [form, item, open])

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>
            {item ? t("agentProfile.editSquad") : t("agentProfile.createSquad")}
          </DialogTitle>
        </DialogHeader>
        <form
          className="space-y-4"
          onSubmit={form.handleSubmit(async (values) => {
            await onSubmit({
              teamId,
              name: values.name.trim(),
              leaderUserId: Number(values.leaderUserId) || 0,
              memberIds: item?.memberProfileIds ?? [],
              status: item?.status ?? Status.Ok,
              remark: values.remark.trim(),
            })
          })}
        >
          <Field data-invalid={!!form.formState.errors.name}>
            <FieldLabel htmlFor="squad-name">{t("agentProfile.squadName")}</FieldLabel>
            <FieldContent>
              <Input id="squad-name" {...form.register("name")} />
              <FieldError errors={[form.formState.errors.name]} />
            </FieldContent>
          </Field>
          <Field>
            <FieldLabel>{t("agentProfile.squadLeader")}</FieldLabel>
            <FieldContent>
              <Controller
                control={form.control}
                name="leaderUserId"
                render={({ field }) => (
                  <OptionCombobox
                    value={field.value}
                    options={[
                      { value: "0", label: t("agentProfile.noSquadLeader") },
                      ...profiles.map((profile) => ({
                        value: String(profile.userId),
                        label: profile.displayName,
                      })),
                    ]}
                    placeholder={t("agentProfile.noSquadLeader")}
                    searchPlaceholder={t("agentProfile.searchAgent")}
                    onChange={field.onChange}
                  />
                )}
              />
            </FieldContent>
          </Field>
          <Field>
            <FieldLabel htmlFor="squad-remark">{t("agentProfile.remark")}</FieldLabel>
            <FieldContent>
              <Textarea id="squad-remark" rows={3} {...form.register("remark")} />
            </FieldContent>
          </Field>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
              {t("agentProfile.cancel")}
            </Button>
            <Button type="submit" disabled={saving}>
              {saving ? t("agentProfile.saving") : t("agentProfile.save")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

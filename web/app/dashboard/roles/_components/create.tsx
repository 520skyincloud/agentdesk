"use client"

import { useMemo } from "react"
import { Controller, Resolver, useForm } from "react-hook-form"
import { zodResolver } from "@hookform/resolvers/zod"
import { z } from "zod/v4"

import { type CreateAdminRolePayload } from "@/lib/api/admin"
import { Button } from "@/components/ui/button"
import {
  Drawer,
  DrawerContent,
  DrawerDescription,
  DrawerFooter,
  DrawerHeader,
  DrawerTitle,
} from "@/components/ui/drawer"
import {
  Field,
  FieldContent,
  FieldError,
  FieldLabel,
} from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { OptionCombobox } from "@/components/option-combobox"
import { Textarea } from "@/components/ui/textarea"
import { useI18n } from "@/i18n/provider"

type CreateRoleDrawerProps = {
  open: boolean
  saving: boolean
  onOpenChange: (open: boolean) => void
  onSubmit: (payload: CreateAdminRolePayload) => Promise<void>
}

type CreateForm = {
  name: string
  code: string
  scope: "platform" | "tenant"
  authorityLevel: number
  remark: string
}

const emptyForm: CreateForm = {
  name: "",
  code: "",
  scope: "tenant",
  authorityLevel: 10,
  remark: "",
}

function buildEmptyForm(): CreateForm {
  return {
    ...emptyForm,
  }
}

function buildPayload(form: CreateForm): CreateAdminRolePayload {
  return {
    name: form.name.trim(),
    code: form.code.trim(),
    scope: form.scope,
    authorityLevel: form.authorityLevel,
    remark: form.remark.trim(),
  }
}

export function CreateRoleDrawer({
  open,
  saving,
  onOpenChange,
  onSubmit,
}: CreateRoleDrawerProps) {
  return (
    <Drawer open={open} onOpenChange={onOpenChange} direction="right">
      {open ? (
        <CreateRoleDrawerBody
          key="create-role"
          saving={saving}
          onOpenChange={onOpenChange}
          onSubmit={onSubmit}
        />
      ) : null}
    </Drawer>
  )
}

type CreateRoleDrawerBodyProps = {
  saving: boolean
  onOpenChange: (open: boolean) => void
  onSubmit: (payload: CreateAdminRolePayload) => Promise<void>
}

function CreateRoleDrawerBody({
  saving,
  onOpenChange,
  onSubmit,
}: CreateRoleDrawerBodyProps) {
  const t = useI18n()
  const createFormSchema = useMemo(
    () =>
      z.object({
        name: z.string().trim().min(1, t("role.nameRequired")),
        code: z
          .string()
          .trim()
          .min(1, t("role.codeRequired"))
          .regex(/^[A-Za-z][A-Za-z0-9:_-]*$/, t("role.codeInvalid")),
        scope: z.enum(["platform", "tenant"]),
        authorityLevel: z.number().int().min(1).max(99),
        remark: z.string().trim(),
      }),
    [t]
  )
  const createFormResolver = useMemo(
    () => zodResolver(createFormSchema as never) as Resolver<CreateForm>,
    [createFormSchema]
  )
  const form = useForm<CreateForm>({
    resolver: createFormResolver,
    defaultValues: buildEmptyForm(),
  })
  const {
    handleSubmit,
    control,
    register,
    reset,
    formState: { errors },
  } = form

  async function onFormSubmit(values: CreateForm) {
    await onSubmit(buildPayload(values))
    reset(buildEmptyForm())
  }

  return (
    <DrawerContent className="min-w-2xl">
      <DrawerHeader>
        <DrawerTitle>{t("role.createTitle")}</DrawerTitle>
        <DrawerDescription>{t("role.createDescription")}</DrawerDescription>
      </DrawerHeader>
      <form
        className="flex h-full flex-col"
        onSubmit={handleSubmit(onFormSubmit)}
      >
        <div className="space-y-4 overflow-y-auto px-4 pb-4">
          <Field data-invalid={!!errors.name}>
            <FieldLabel htmlFor="create-role-name">{t("role.name")}</FieldLabel>
            <FieldContent>
              <Input
                id="create-role-name"
                placeholder={t("role.namePlaceholder")}
                autoComplete="off"
                aria-invalid={!!errors.name}
                {...register("name")}
              />
              <FieldError errors={[errors.name]} />
            </FieldContent>
          </Field>
          <Field data-invalid={!!errors.code}>
            <FieldLabel htmlFor="create-role-code">{t("role.code")}</FieldLabel>
            <FieldContent>
              <Input
                id="create-role-code"
                placeholder={t("role.codePlaceholder")}
                autoComplete="off"
                aria-invalid={!!errors.code}
                {...register("code")}
              />
              <FieldError errors={[errors.code]} />
            </FieldContent>
          </Field>
          <Field data-invalid={!!errors.scope}>
            <FieldLabel>{t("role.scope")}</FieldLabel>
            <FieldContent>
              <Controller
                control={control}
                name="scope"
                render={({ field }) => (
                  <OptionCombobox
                    value={field.value}
                    options={[
                      { value: "tenant", label: t("role.scopeTenant") },
                      { value: "platform", label: t("role.scopePlatform") },
                    ]}
                    onChange={(value) => field.onChange(value || "tenant")}
                    placeholder={t("role.scope")}
                    searchPlaceholder={t("role.searchScope")}
                    emptyText={t("role.emptyScope")}
                  />
                )}
              />
              <FieldError errors={[errors.scope]} />
            </FieldContent>
          </Field>
          <Field data-invalid={!!errors.authorityLevel}>
            <FieldLabel htmlFor="create-role-authority-level">
              {t("role.authorityLevel")}
            </FieldLabel>
            <FieldContent>
              <Input
                id="create-role-authority-level"
                type="number"
                min={1}
                max={99}
                aria-invalid={!!errors.authorityLevel}
                {...register("authorityLevel", { valueAsNumber: true })}
              />
              <div className="text-xs text-muted-foreground">
                {t("role.authorityLevelHint")}
              </div>
              <FieldError errors={[errors.authorityLevel]} />
            </FieldContent>
          </Field>
          <Field data-invalid={!!errors.remark}>
            <FieldLabel htmlFor="create-role-remark">{t("role.remark")}</FieldLabel>
            <FieldContent>
              <Textarea
                id="create-role-remark"
                placeholder={t("role.optional")}
                aria-invalid={!!errors.remark}
                {...register("remark")}
              />
              <FieldError errors={[errors.remark]} />
            </FieldContent>
          </Field>
        </div>
        <DrawerFooter className="border-t">
          <Button type="submit" disabled={saving}>
            {saving ? t("role.creating") : t("role.create")}
          </Button>
          <Button
            type="button"
            variant="outline"
            onClick={() => onOpenChange(false)}
            disabled={saving}
          >
            {t("role.cancel")}
          </Button>
        </DrawerFooter>
      </form>
    </DrawerContent>
  )
}

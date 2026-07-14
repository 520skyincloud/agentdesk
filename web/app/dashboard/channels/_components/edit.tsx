"use client"

import { useEffect, useMemo, useState } from "react"
import { zodResolver } from "@hookform/resolvers/zod"
import { Controller, type Resolver, useForm } from "react-hook-form"
import { z } from "zod/v4"
import { toast } from "sonner"

import { OptionCombobox } from "@/components/option-combobox"
import { ProjectDialog } from "@/components/project-dialog"
import { Button } from "@/components/ui/button"
import {
  Field,
  FieldContent,
  FieldError,
  FieldLabel,
} from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"
import { useI18n } from "@/i18n/provider"
import {
  fetchTenant,
  type AdminTenant,
  type TenantBasePayload,
  type TenantSupervisorPayload,
} from "@/lib/api/tenant"

export type TenantFormPayload = TenantBasePayload & {
  supervisor?: TenantSupervisorPayload
}

type TenantEditDialogProps = {
  open: boolean
  saving: boolean
  itemId: number | null
  onOpenChange: (open: boolean) => void
  onSubmit: (payload: TenantFormPayload) => Promise<void>
}

type TenantForm = {
  legalName: string
  shortName: string
  registrationType: string
  registrationNo: string
  contactName: string
  contactMobile: string
  contactEmail: string
  address: string
  remark: string
  supervisorUsername: string
  supervisorNickname: string
  supervisorMobile: string
  supervisorEmail: string
}

const emptyForm: TenantForm = {
  legalName: "",
  shortName: "",
  registrationType: "unified_social_credit_code",
  registrationNo: "",
  contactName: "",
  contactMobile: "",
  contactEmail: "",
  address: "",
  remark: "",
  supervisorUsername: "",
  supervisorNickname: "",
  supervisorMobile: "",
  supervisorEmail: "",
}

function buildForm(item: AdminTenant | null): TenantForm {
  if (!item) return emptyForm

  return {
    legalName: item.legalName || "",
    shortName: item.shortName || "",
    registrationType:
      item.registrationType || "unified_social_credit_code",
    registrationNo: item.registrationNo || "",
    contactName: item.contactName || "",
    contactMobile: item.contactMobile || "",
    contactEmail: item.contactEmail || "",
    address: item.address || "",
    remark: item.remark || "",
    supervisorUsername: "",
    supervisorNickname: "",
    supervisorMobile: "",
    supervisorEmail: "",
  }
}

function buildPayload(values: TenantForm, creating: boolean): TenantFormPayload {
  const payload: TenantFormPayload = {
    legalName: values.legalName.trim(),
    shortName: values.shortName.trim(),
    registrationType: values.registrationType.trim().toLowerCase(),
    registrationNo: values.registrationNo.trim().toUpperCase(),
    contactName: values.contactName.trim(),
    contactMobile: values.contactMobile.trim(),
    contactEmail: values.contactEmail.trim().toLowerCase(),
    address: values.address.trim(),
    remark: values.remark.trim(),
  }
  if (creating) {
    payload.supervisor = {
      username: values.supervisorUsername.trim(),
      nickname: values.supervisorNickname.trim(),
      mobile: values.supervisorMobile.trim(),
      email: values.supervisorEmail.trim().toLowerCase(),
    }
  }
  return payload
}

export function TenantEditDialog(props: TenantEditDialogProps) {
  const { open, itemId } = props
  if (!open) return null

  return <TenantEditDialogBody key={itemId ?? "create"} {...props} />
}

function TenantEditDialogBody({
  open,
  saving,
  itemId,
  onOpenChange,
  onSubmit,
}: TenantEditDialogProps) {
  const t = useI18n()
  const [loading, setLoading] = useState(Boolean(itemId))
  const schema = useMemo(
    () =>
      z
        .object({
          legalName: z.string().trim().min(1, t("tenant.legalNameRequired")).max(200),
          shortName: z.string().trim().min(1, t("tenant.shortNameRequired")).max(100),
          registrationType: z.string().trim().min(1),
          registrationNo: z
            .string()
            .trim()
            .toUpperCase()
            .regex(/^[0-9ABCDEFGHJKLMNPQRTUWXY]{18}$/, t("tenant.registrationNoInvalid")),
          contactName: z.string().trim().max(100),
          contactMobile: z
            .string()
            .trim()
            .max(32)
            .refine(
              (value) => value.length === 0 || /^[0-9+\-\s()]{6,32}$/.test(value),
              t("tenant.mobileInvalid")
            ),
          contactEmail: z
            .string()
            .trim()
            .max(100)
            .refine(
              (value) => value.length === 0 || /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(value),
              t("tenant.emailInvalid")
            ),
          address: z.string().trim().max(500),
          remark: z.string().trim(),
          supervisorUsername: z.string().trim().max(100),
          supervisorNickname: z.string().trim().max(100),
          supervisorMobile: z.string().trim().max(32),
          supervisorEmail: z.string().trim().max(100),
        })
        .superRefine((values, ctx) => {
          if (itemId) return

          const requiredSupervisorFields: Array<
            keyof Pick<
              TenantForm,
              | "supervisorUsername"
              | "supervisorNickname"
              | "supervisorMobile"
              | "supervisorEmail"
            >
          > = [
            "supervisorUsername",
            "supervisorNickname",
            "supervisorMobile",
            "supervisorEmail",
          ]
          requiredSupervisorFields.forEach((field) => {
            if (!values[field].trim()) {
              ctx.addIssue({
                code: "custom",
                path: [field],
                message: t("tenant.supervisorFieldRequired"),
              })
            }
          })
          if (
            values.supervisorMobile.trim() &&
            !/^[0-9+\-\s()]{6,32}$/.test(values.supervisorMobile.trim())
          ) {
            ctx.addIssue({
              code: "custom",
              path: ["supervisorMobile"],
              message: t("tenant.mobileInvalid"),
            })
          }
          if (
            values.supervisorEmail.trim() &&
            !/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(values.supervisorEmail.trim())
          ) {
            ctx.addIssue({
              code: "custom",
              path: ["supervisorEmail"],
              message: t("tenant.emailInvalid"),
            })
          }
        }),
    [itemId, t]
  )
  const resolver = useMemo(
    () => zodResolver(schema as never) as Resolver<TenantForm>,
    [schema]
  )
  const form = useForm<TenantForm>({
    resolver,
    defaultValues: emptyForm,
  })
  const {
    control,
    handleSubmit,
    register,
    reset,
    formState: { errors },
  } = form

  useEffect(() => {
    let cancelled = false
    if (!itemId) return

    void fetchTenant(itemId)
      .then((item) => {
        if (!cancelled) reset(buildForm(item))
      })
      .catch((error) => {
        if (!cancelled) {
          toast.error(
            error instanceof Error ? error.message : t("tenant.loadDetailFailed")
          )
        }
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })

    return () => {
      cancelled = true
    }
  }, [itemId, reset, t])

  async function submit(values: TenantForm) {
    await onSubmit(buildPayload(values, !itemId))
  }

  const formId = "tenant-edit-form"
  const creating = !itemId

  return (
    <ProjectDialog
      open={open}
      onOpenChange={onOpenChange}
      title={t(creating ? "tenant.createTitle" : "tenant.editTitle")}
      description={t(
        creating ? "tenant.createDescription" : "tenant.editDescription"
      )}
      size="lg"
      allowFullscreen
      footer={
        <>
          <Button
            type="button"
            variant="outline"
            onClick={() => onOpenChange(false)}
            disabled={saving}
          >
            {t("common.cancel")}
          </Button>
          <Button type="submit" form={formId} disabled={saving || loading}>
            {saving
              ? t("tenant.saving")
              : t(creating ? "tenant.create" : "tenant.save")}
          </Button>
        </>
      }
    >
      {loading ? (
        <div className="flex items-center justify-center py-16 text-sm text-muted-foreground">
          {t("tenant.loadingDetail")}
        </div>
      ) : (
        <form
          id={formId}
          onSubmit={handleSubmit(submit)}
          className="grid gap-4 md:grid-cols-2"
        >
          <SectionTitle title={t("tenant.companySection")} />

          <Field data-invalid={!!errors.legalName}>
            <FieldLabel htmlFor="tenant-legal-name">{t("tenant.legalName")}</FieldLabel>
            <FieldContent>
              <Input
                id="tenant-legal-name"
                placeholder={t("tenant.legalNamePlaceholder")}
                aria-invalid={!!errors.legalName}
                {...register("legalName")}
              />
              <FieldError errors={[errors.legalName]} />
            </FieldContent>
          </Field>

          <Field data-invalid={!!errors.shortName}>
            <FieldLabel htmlFor="tenant-short-name">{t("tenant.shortName")}</FieldLabel>
            <FieldContent>
              <Input
                id="tenant-short-name"
                placeholder={t("tenant.shortNamePlaceholder")}
                aria-invalid={!!errors.shortName}
                {...register("shortName")}
              />
              <FieldError errors={[errors.shortName]} />
            </FieldContent>
          </Field>

          <Field data-invalid={!!errors.registrationType}>
            <FieldLabel>{t("tenant.registrationType")}</FieldLabel>
            <FieldContent>
              <Controller
                name="registrationType"
                control={control}
                render={({ field }) => (
                  <OptionCombobox
                    value={field.value}
                    onChange={field.onChange}
                    options={[
                      {
                        value: "unified_social_credit_code",
                        label: t("tenant.registrationTypeCreditCode"),
                      },
                    ]}
                    placeholder={t("tenant.registrationType")}
                    disabled
                  />
                )}
              />
              <FieldError errors={[errors.registrationType]} />
            </FieldContent>
          </Field>

          <Field data-invalid={!!errors.registrationNo}>
            <FieldLabel htmlFor="tenant-registration-no">
              {t("tenant.registrationNo")}
            </FieldLabel>
            <FieldContent>
              <Input
                id="tenant-registration-no"
                className="font-mono"
                placeholder={t("tenant.registrationNoPlaceholder")}
                aria-invalid={!!errors.registrationNo}
                {...register("registrationNo")}
              />
              <FieldError errors={[errors.registrationNo]} />
            </FieldContent>
          </Field>

          <SectionTitle title={t("tenant.contactSection")} />

          <Field data-invalid={!!errors.contactName}>
            <FieldLabel htmlFor="tenant-contact-name">{t("tenant.contactName")}</FieldLabel>
            <FieldContent>
              <Input
                id="tenant-contact-name"
                placeholder={t("tenant.contactNamePlaceholder")}
                aria-invalid={!!errors.contactName}
                {...register("contactName")}
              />
              <FieldError errors={[errors.contactName]} />
            </FieldContent>
          </Field>

          <Field data-invalid={!!errors.contactMobile}>
            <FieldLabel htmlFor="tenant-contact-mobile">{t("tenant.contactMobile")}</FieldLabel>
            <FieldContent>
              <Input
                id="tenant-contact-mobile"
                placeholder={t("tenant.contactMobilePlaceholder")}
                aria-invalid={!!errors.contactMobile}
                {...register("contactMobile")}
              />
              <FieldError errors={[errors.contactMobile]} />
            </FieldContent>
          </Field>

          <Field data-invalid={!!errors.contactEmail}>
            <FieldLabel htmlFor="tenant-contact-email">{t("tenant.contactEmail")}</FieldLabel>
            <FieldContent>
              <Input
                id="tenant-contact-email"
                type="email"
                placeholder={t("tenant.contactEmailPlaceholder")}
                aria-invalid={!!errors.contactEmail}
                {...register("contactEmail")}
              />
              <FieldError errors={[errors.contactEmail]} />
            </FieldContent>
          </Field>

          <Field className="md:col-span-2" data-invalid={!!errors.address}>
            <FieldLabel htmlFor="tenant-address">{t("tenant.address")}</FieldLabel>
            <FieldContent>
              <Input
                id="tenant-address"
                placeholder={t("tenant.addressPlaceholder")}
                aria-invalid={!!errors.address}
                {...register("address")}
              />
              <FieldError errors={[errors.address]} />
            </FieldContent>
          </Field>

          {creating ? (
            <>
              <SectionTitle title={t("tenant.supervisorSection")} />

              <Field data-invalid={!!errors.supervisorUsername}>
                <FieldLabel htmlFor="tenant-supervisor-username">
                  {t("tenant.supervisorUsername")}
                </FieldLabel>
                <FieldContent>
                  <Input
                    id="tenant-supervisor-username"
                    autoComplete="off"
                    placeholder={t("tenant.supervisorUsernamePlaceholder")}
                    aria-invalid={!!errors.supervisorUsername}
                    {...register("supervisorUsername")}
                  />
                  <FieldError errors={[errors.supervisorUsername]} />
                </FieldContent>
              </Field>

              <Field data-invalid={!!errors.supervisorNickname}>
                <FieldLabel htmlFor="tenant-supervisor-nickname">
                  {t("tenant.supervisorNickname")}
                </FieldLabel>
                <FieldContent>
                  <Input
                    id="tenant-supervisor-nickname"
                    placeholder={t("tenant.supervisorNicknamePlaceholder")}
                    aria-invalid={!!errors.supervisorNickname}
                    {...register("supervisorNickname")}
                  />
                  <FieldError errors={[errors.supervisorNickname]} />
                </FieldContent>
              </Field>

              <Field data-invalid={!!errors.supervisorMobile}>
                <FieldLabel htmlFor="tenant-supervisor-mobile">
                  {t("tenant.supervisorMobile")}
                </FieldLabel>
                <FieldContent>
                  <Input
                    id="tenant-supervisor-mobile"
                    placeholder={t("tenant.supervisorMobilePlaceholder")}
                    aria-invalid={!!errors.supervisorMobile}
                    {...register("supervisorMobile")}
                  />
                  <FieldError errors={[errors.supervisorMobile]} />
                </FieldContent>
              </Field>

              <Field data-invalid={!!errors.supervisorEmail}>
                <FieldLabel htmlFor="tenant-supervisor-email">
                  {t("tenant.supervisorEmail")}
                </FieldLabel>
                <FieldContent>
                  <Input
                    id="tenant-supervisor-email"
                    type="email"
                    placeholder={t("tenant.supervisorEmailPlaceholder")}
                    aria-invalid={!!errors.supervisorEmail}
                    {...register("supervisorEmail")}
                  />
                  <FieldError errors={[errors.supervisorEmail]} />
                </FieldContent>
              </Field>
            </>
          ) : null}

          <SectionTitle title={t("tenant.remarkSection")} />
          <Field className="md:col-span-2" data-invalid={!!errors.remark}>
            <FieldLabel htmlFor="tenant-remark">{t("tenant.remark")}</FieldLabel>
            <FieldContent>
              <Textarea
                id="tenant-remark"
                rows={3}
                placeholder={t("tenant.remarkPlaceholder")}
                aria-invalid={!!errors.remark}
                {...register("remark")}
              />
              <FieldError errors={[errors.remark]} />
            </FieldContent>
          </Field>
        </form>
      )}
    </ProjectDialog>
  )
}

function SectionTitle({ title }: { title: string }) {
  return (
    <div className="border-b border-border/70 pb-2 md:col-span-2">
      <h3 className="text-sm font-semibold">{title}</h3>
    </div>
  )
}

"use client"

import { useEffect, useMemo, useState } from "react"
import { zodResolver } from "@hookform/resolvers/zod"
import { TriangleAlertIcon } from "lucide-react"
import { Controller, type Resolver, useForm } from "react-hook-form"
import { z } from "zod/v4"
import { toast } from "sonner"

import { OptionCombobox } from "@/components/option-combobox"
import { ProjectDialog } from "@/components/project-dialog"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
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
  fetchTenantIndustryOptions,
  type AdminTenant,
  type TenantBasePayload,
  type TenantIndustryOption,
  type TenantSupervisorPayload,
} from "@/lib/api/tenant"

export type TenantFormPayload = TenantBasePayload & {
  supervisor?: TenantSupervisorPayload
  confirmIndustryChange: boolean
  industryChangeReason: string
}

type TenantEditDialogProps = {
  open: boolean
  saving: boolean
  itemId: number | null
  onOpenChange: (open: boolean) => void
  onSubmit: (payload: TenantFormPayload) => Promise<void>
}

type TenantForm = {
  intentProfileId: string
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
  confirmIndustryChange: boolean
  industryChangeReason: string
}

const emptyForm: TenantForm = {
  intentProfileId: "",
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
  confirmIndustryChange: false,
  industryChangeReason: "",
}

function buildForm(item: AdminTenant | null): TenantForm {
  if (!item) return emptyForm

  return {
    intentProfileId: String(item.intentProfileId || ""),
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
    confirmIndustryChange: false,
    industryChangeReason: "",
  }
}

function buildPayload(
  values: TenantForm,
  creating: boolean,
  originalIntentProfileId: number,
): TenantFormPayload {
  const intentProfileId = Number(values.intentProfileId)
  const industryChanged = !creating && intentProfileId !== originalIntentProfileId
  const payload: TenantFormPayload = {
    intentProfileId,
    confirmIndustryChange:
      industryChanged && values.confirmIndustryChange === true,
    industryChangeReason: industryChanged
      ? values.industryChangeReason.trim()
      : "",
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
  const [loading, setLoading] = useState(true)
  const [industryOptions, setIndustryOptions] = useState<TenantIndustryOption[]>([])
  const [originalIntentProfileId, setOriginalIntentProfileId] = useState(0)
  const schema = useMemo(
    () =>
      z
        .object({
          intentProfileId: z
            .string()
            .trim()
            .refine((value) => Number(value) > 0, t("tenant.industryRequired")),
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
          confirmIndustryChange: z.boolean(),
          industryChangeReason: z.string().trim().max(500),
        })
        .superRefine((values, ctx) => {
          if (
            itemId &&
            Number(values.intentProfileId) !== originalIntentProfileId
          ) {
            if (!values.confirmIndustryChange) {
              ctx.addIssue({
                code: "custom",
                path: ["confirmIndustryChange"],
                message: t("tenant.industryChangeConfirmRequired"),
              })
            }
            if (!values.industryChangeReason.trim()) {
              ctx.addIssue({
                code: "custom",
                path: ["industryChangeReason"],
                message: t("tenant.industryChangeReasonRequired"),
              })
            }
          }

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
    [itemId, originalIntentProfileId, t]
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
    watch,
    formState: { errors },
  } = form

  useEffect(() => {
    let cancelled = false

    setLoading(true)
    void Promise.all([
      fetchTenantIndustryOptions(),
      itemId ? fetchTenant(itemId) : Promise.resolve(null),
    ])
      .then(([options, item]) => {
        if (cancelled) return
        setIndustryOptions(options)
        setOriginalIntentProfileId(item?.intentProfileId || 0)
        reset(buildForm(item))
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
    await onSubmit(buildPayload(values, !itemId, originalIntentProfileId))
  }

  const formId = "tenant-edit-form"
  const creating = !itemId
  const selectedIntentProfileId = Number(watch("intentProfileId"))
  const industryChanged =
    !creating &&
    originalIntentProfileId > 0 &&
    selectedIntentProfileId !== originalIntentProfileId
  const industryComboboxOptions = industryOptions.map((item) => ({
    value: String(item.id),
    label: `${item.name} · ${item.industryCode} · R${item.revision}`,
  }))

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

          <SectionTitle title={t("tenant.industrySection")} />

          <Field
            className="md:col-span-2"
            data-invalid={!!errors.intentProfileId}
          >
            <FieldLabel>{t("tenant.industry")}</FieldLabel>
            <FieldContent>
              <Controller
                name="intentProfileId"
                control={control}
                render={({ field }) => (
                  <OptionCombobox
                    value={field.value}
                    onChange={field.onChange}
                    options={industryComboboxOptions}
                    placeholder={t("tenant.industryPlaceholder")}
                    searchPlaceholder={t("tenant.industrySearchPlaceholder")}
                    emptyText={t("tenant.industryEmpty")}
                  />
                )}
              />
              <p className="text-xs leading-5 text-muted-foreground">
                {t("tenant.industryDescription")}
              </p>
              <FieldError errors={[errors.intentProfileId]} />
            </FieldContent>
          </Field>

          {industryChanged ? (
            <div className="grid gap-4 border-l-2 border-destructive bg-destructive/5 p-4 md:col-span-2 md:grid-cols-2">
              <div className="flex gap-3 md:col-span-2">
                <TriangleAlertIcon className="mt-0.5 size-5 shrink-0 text-destructive" />
                <div>
                  <div className="text-sm font-medium text-foreground">
                    {t("tenant.industryChangeTitle")}
                  </div>
                  <p className="mt-1 text-xs leading-5 text-muted-foreground">
                    {t("tenant.industryChangeDescription")}
                  </p>
                </div>
              </div>
              <Field data-invalid={!!errors.industryChangeReason}>
                <FieldLabel htmlFor="tenant-industry-change-reason">
                  {t("tenant.industryChangeReason")}
                </FieldLabel>
                <FieldContent>
                  <Textarea
                    id="tenant-industry-change-reason"
                    rows={3}
                    placeholder={t("tenant.industryChangeReasonPlaceholder")}
                    aria-invalid={!!errors.industryChangeReason}
                    {...register("industryChangeReason")}
                  />
                  <FieldError errors={[errors.industryChangeReason]} />
                </FieldContent>
              </Field>
              <Field data-invalid={!!errors.confirmIndustryChange}>
                <FieldContent className="h-full justify-center">
                  <Controller
                    name="confirmIndustryChange"
                    control={control}
                    render={({ field }) => (
                      <label
                        htmlFor="tenant-confirm-industry-change"
                        className="flex cursor-pointer items-start gap-3 text-sm leading-6"
                      >
                        <Checkbox
                          id="tenant-confirm-industry-change"
                          checked={field.value}
                          onCheckedChange={(checked) =>
                            field.onChange(checked === true)
                          }
                        />
                        <span>{t("tenant.industryChangeConfirm")}</span>
                      </label>
                    )}
                  />
                  <FieldError errors={[errors.confirmIndustryChange]} />
                </FieldContent>
              </Field>
            </div>
          ) : null}

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

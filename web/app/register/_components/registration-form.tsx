"use client"

import Link from "next/link"
import { useSearchParams } from "next/navigation"
import { zodResolver } from "@hookform/resolvers/zod"
import {
  Building2Icon,
  CheckCircle2Icon,
  LoaderCircleIcon,
  ShieldCheckIcon,
} from "lucide-react"
import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { type Resolver, useForm } from "react-hook-form"
import { z } from "zod/v4"

import { fetchAuthOptions } from "@/lib/api/auth"
import {
  registerTenantUser,
  validateTenantInvitation,
  type TenantInvitationValidation,
  type TenantRegistrationPayload,
  type TenantRegistrationResult,
} from "@/lib/api/tenant-registration"
import { useI18n } from "@/i18n/provider"
import { generateUUID } from "@/lib/utils"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import {
  Field,
  FieldContent,
  FieldDescription,
  FieldError,
  FieldGroup,
  FieldLabel,
} from "@/components/ui/field"
import { Input } from "@/components/ui/input"

type RegistrationFormValues = TenantRegistrationPayload

const emptyForm: RegistrationFormValues = {
  username: "",
  nickname: "",
  mobile: "",
  email: "",
  password: "",
  confirmPassword: "",
  invitationCode: "",
}

type StableRequest = {
  fingerprint: string
  requestId: string
}

function passwordByteLength(value: string) {
  return new TextEncoder().encode(value).length
}

export function TenantRegistrationForm() {
  const t = useI18n()
  const searchParams = useSearchParams()
  const [optionsLoading, setOptionsLoading] = useState(true)
  const [registrationEnabled, setRegistrationEnabled] = useState(false)
  const [invitationLoading, setInvitationLoading] = useState(false)
  const [validatedCode, setValidatedCode] = useState("")
  const [invitation, setInvitation] = useState<TenantInvitationValidation | null>(null)
  const [submitting, setSubmitting] = useState(false)
  const [result, setResult] = useState<TenantRegistrationResult | null>(null)
  const stableRequestRef = useRef<StableRequest | null>(null)
  const initialInviteRef = useRef("")
  const schema = useMemo(
    () =>
      z
        .object({
          username: z
            .string()
            .trim()
            .regex(/^[A-Za-z0-9._-]{3,100}$/, t("tenantRegistration.usernameInvalid")),
          nickname: z
            .string()
            .trim()
            .min(1, t("tenantRegistration.nicknameRequired"))
            .max(100, t("tenantRegistration.nicknameTooLong")),
          mobile: z
            .string()
            .trim()
            .regex(/^[0-9+ -]{6,32}$/, t("tenantRegistration.mobileInvalid")),
          email: z
            .string()
            .trim()
            .refine(
              (value) => /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(value),
              t("tenantRegistration.emailInvalid")
            ),
          password: z
            .string()
            .refine(
              (value) => passwordByteLength(value) >= 8 && passwordByteLength(value) <= 72,
              t("tenantRegistration.passwordInvalid")
            ),
          confirmPassword: z.string(),
          invitationCode: z
            .string()
            .trim()
            .regex(/^inv_[0-9a-f]{48}$/, t("tenantRegistration.invitationInvalid")),
        })
        .refine((value) => value.password === value.confirmPassword, {
          path: ["confirmPassword"],
          message: t("tenantRegistration.passwordMismatch"),
        }),
    [t]
  )
  const resolver = useMemo(
    () => zodResolver(schema as never) as Resolver<RegistrationFormValues>,
    [schema]
  )
  const form = useForm<RegistrationFormValues>({
    resolver,
    defaultValues: emptyForm,
  })
  const {
    formState: { errors },
    handleSubmit,
    register,
    setError,
    setValue,
    trigger,
  } = form

  const validateInvitation = useCallback(
    async (code: string) => {
      const normalized = code.trim()
      setInvitationLoading(true)
      try {
        const nextInvitation = await validateTenantInvitation(normalized)
        setValidatedCode(normalized)
        setInvitation(nextInvitation.valid ? nextInvitation : null)
        if (!nextInvitation.valid) {
          setError("invitationCode", {
            type: "validate",
            message: t("tenantRegistration.invitationInvalid"),
          })
        }
        return nextInvitation.valid ? nextInvitation : null
      } catch (error) {
        setValidatedCode(normalized)
        setInvitation(null)
        setError("invitationCode", {
          type: "validate",
          message:
            error instanceof Error
              ? error.message
              : t("tenantRegistration.invitationValidationFailed"),
        })
        return null
      } finally {
        setInvitationLoading(false)
      }
    },
    [setError, t]
  )

  useEffect(() => {
    let cancelled = false
    void fetchAuthOptions()
      .then((options) => {
        if (!cancelled) {
          setRegistrationEnabled(options.tenantRegistrationEnabled)
        }
      })
      .catch(() => {
        if (!cancelled) {
          setRegistrationEnabled(false)
        }
      })
      .finally(() => {
        if (!cancelled) {
          setOptionsLoading(false)
        }
      })
    return () => {
      cancelled = true
    }
  }, [])

  useEffect(() => {
    if (optionsLoading || !registrationEnabled) {
      return
    }
    const invitationCode = searchParams.get("invite")?.trim() ?? ""
    if (!invitationCode || initialInviteRef.current === invitationCode) {
      return
    }
    initialInviteRef.current = invitationCode
    setValue("invitationCode", invitationCode, { shouldDirty: false, shouldValidate: true })
    void validateInvitation(invitationCode)
  }, [optionsLoading, registrationEnabled, searchParams, setValue, validateInvitation])

  function requestIdFor(payload: TenantRegistrationPayload) {
    const fingerprint = JSON.stringify(payload)
    if (stableRequestRef.current?.fingerprint !== fingerprint) {
      stableRequestRef.current = { fingerprint, requestId: generateUUID() }
    }
    return stableRequestRef.current.requestId
  }

  async function handleInvitationValidation() {
    if (!(await trigger("invitationCode"))) {
      return
    }
    await validateInvitation(form.getValues("invitationCode"))
  }

  async function onSubmit(values: RegistrationFormValues) {
    if (submitting) {
      return
    }
    const payload: TenantRegistrationPayload = {
      ...values,
      username: values.username.trim(),
      nickname: values.nickname.trim(),
      mobile: values.mobile.trim(),
      email: values.email.trim(),
      invitationCode: values.invitationCode.trim(),
    }
    let currentInvitation = invitation
    if (!currentInvitation || validatedCode !== payload.invitationCode) {
      currentInvitation = await validateInvitation(payload.invitationCode)
    }
    if (!currentInvitation) {
      return
    }

    setSubmitting(true)
    try {
      const registrationResult = await registerTenantUser(payload, requestIdFor(payload))
      setResult(registrationResult)
    } catch (error) {
      setError("root", {
        type: "server",
        message:
          error instanceof Error ? error.message : t("tenantRegistration.submitFailed"),
      })
    } finally {
      setSubmitting(false)
    }
  }

  if (optionsLoading) {
    return (
      <Card className="w-full">
        <CardContent className="flex min-h-64 items-center justify-center gap-2 text-muted-foreground">
          <LoaderCircleIcon className="size-4 animate-spin" />
          {t("tenantRegistration.checkingAvailability")}
        </CardContent>
      </Card>
    )
  }

  if (!registrationEnabled) {
    return (
      <Card className="w-full max-w-xl">
        <CardHeader className="text-center">
          <div className="mx-auto flex size-11 items-center justify-center rounded-lg bg-muted text-muted-foreground">
            <ShieldCheckIcon className="size-5" />
          </div>
          <CardTitle>{t("tenantRegistration.unavailableTitle")}</CardTitle>
        </CardHeader>
        <CardContent className="space-y-5 text-center">
          <p className="text-sm text-muted-foreground">
            {t("tenantRegistration.unavailableDescription")}
          </p>
          <Button
            render={<Link href="/dashboard/login" />}
            nativeButton={false}
            variant="outline"
          >
            {t("tenantRegistration.backToLogin")}
          </Button>
        </CardContent>
      </Card>
    )
  }

  if (result) {
    return (
      <Card className="w-full max-w-xl">
        <CardHeader className="text-center">
          <div className="mx-auto flex size-11 items-center justify-center rounded-lg bg-emerald-50 text-emerald-700 dark:bg-emerald-950/50 dark:text-emerald-300">
            <CheckCircle2Icon className="size-5" />
          </div>
          <CardTitle>{t("tenantRegistration.successTitle")}</CardTitle>
        </CardHeader>
        <CardContent className="space-y-5">
          <Alert>
            <Building2Icon />
            <AlertTitle>{result.tenantName}</AlertTitle>
            <AlertDescription>
              {t("tenantRegistration.successDescription", { username: result.username })}
            </AlertDescription>
          </Alert>
          <Button
            render={<Link href="/dashboard/login" />}
            nativeButton={false}
            className="w-full"
          >
            {t("tenantRegistration.backToLogin")}
          </Button>
        </CardContent>
      </Card>
    )
  }

  return (
    <Card className="w-full overflow-hidden">
      <CardHeader className="border-b bg-[#f8fbff] dark:bg-muted/30">
        <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
          <div>
            <CardTitle>{t("tenantRegistration.title")}</CardTitle>
            <p className="mt-2 text-sm text-muted-foreground">
              {t("tenantRegistration.description")}
            </p>
          </div>
          {invitation?.valid ? (
            <Badge variant="secondary" className="shrink-0">
              <Building2Icon />
              {invitation.tenantShortName || invitation.tenantLegalName}
            </Badge>
          ) : null}
        </div>
      </CardHeader>
      <CardContent>
        <form onSubmit={handleSubmit(onSubmit)}>
          <FieldGroup>
            <Field data-invalid={!!errors.invitationCode}>
              <FieldLabel htmlFor="registration-invitation">
                {t("tenantRegistration.invitationCode")}
              </FieldLabel>
              <FieldContent>
                <div className="flex gap-2">
                  <Input
                    id="registration-invitation"
                    autoComplete="off"
                    aria-invalid={!!errors.invitationCode}
                    placeholder={t("tenantRegistration.invitationPlaceholder")}
                    {...register("invitationCode", {
                      onChange: () => {
                        setInvitation(null)
                        setValidatedCode("")
                      },
                    })}
                  />
                  <Button
                    type="button"
                    variant="outline"
                    disabled={invitationLoading}
                    onClick={() => void handleInvitationValidation()}
                  >
                    {invitationLoading ? (
                      <LoaderCircleIcon className="animate-spin" />
                    ) : (
                      t("tenantRegistration.validateInvitation")
                    )}
                  </Button>
                </div>
                <FieldError errors={[errors.invitationCode]} />
                {invitation?.valid ? (
                  <FieldDescription className="text-emerald-700 dark:text-emerald-300">
                    {t("tenantRegistration.invitationTenant", {
                      tenant: invitation.tenantLegalName || invitation.tenantShortName || "-",
                    })}
                  </FieldDescription>
                ) : null}
              </FieldContent>
            </Field>

            <div className="grid gap-5 sm:grid-cols-2">
              <Field data-invalid={!!errors.username}>
                <FieldLabel htmlFor="registration-username">
                  {t("tenantRegistration.username")}
                </FieldLabel>
                <FieldContent>
                  <Input
                    id="registration-username"
                    autoComplete="username"
                    aria-invalid={!!errors.username}
                    placeholder={t("tenantRegistration.usernamePlaceholder")}
                    {...register("username")}
                  />
                  <FieldError errors={[errors.username]} />
                </FieldContent>
              </Field>
              <Field data-invalid={!!errors.nickname}>
                <FieldLabel htmlFor="registration-nickname">
                  {t("tenantRegistration.nickname")}
                </FieldLabel>
                <FieldContent>
                  <Input
                    id="registration-nickname"
                    autoComplete="name"
                    aria-invalid={!!errors.nickname}
                    placeholder={t("tenantRegistration.nicknamePlaceholder")}
                    {...register("nickname")}
                  />
                  <FieldError errors={[errors.nickname]} />
                </FieldContent>
              </Field>
              <Field data-invalid={!!errors.mobile}>
                <FieldLabel htmlFor="registration-mobile">
                  {t("tenantRegistration.mobile")}
                </FieldLabel>
                <FieldContent>
                  <Input
                    id="registration-mobile"
                    type="tel"
                    autoComplete="tel"
                    aria-invalid={!!errors.mobile}
                    placeholder={t("tenantRegistration.mobilePlaceholder")}
                    {...register("mobile")}
                  />
                  <FieldError errors={[errors.mobile]} />
                </FieldContent>
              </Field>
              <Field data-invalid={!!errors.email}>
                <FieldLabel htmlFor="registration-email">
                  {t("tenantRegistration.email")}
                </FieldLabel>
                <FieldContent>
                  <Input
                    id="registration-email"
                    type="email"
                    autoComplete="email"
                    aria-invalid={!!errors.email}
                    placeholder={t("tenantRegistration.emailPlaceholder")}
                    {...register("email")}
                  />
                  <FieldError errors={[errors.email]} />
                </FieldContent>
              </Field>
              <Field data-invalid={!!errors.password}>
                <FieldLabel htmlFor="registration-password">
                  {t("tenantRegistration.password")}
                </FieldLabel>
                <FieldContent>
                  <Input
                    id="registration-password"
                    type="password"
                    autoComplete="new-password"
                    aria-invalid={!!errors.password}
                    placeholder={t("tenantRegistration.passwordPlaceholder")}
                    {...register("password")}
                  />
                  <FieldError errors={[errors.password]} />
                </FieldContent>
              </Field>
              <Field data-invalid={!!errors.confirmPassword}>
                <FieldLabel htmlFor="registration-confirm-password">
                  {t("tenantRegistration.confirmPassword")}
                </FieldLabel>
                <FieldContent>
                  <Input
                    id="registration-confirm-password"
                    type="password"
                    autoComplete="new-password"
                    aria-invalid={!!errors.confirmPassword}
                    placeholder={t("tenantRegistration.confirmPasswordPlaceholder")}
                    {...register("confirmPassword")}
                  />
                  <FieldError errors={[errors.confirmPassword]} />
                </FieldContent>
              </Field>
            </div>

            {errors.root?.message ? (
              <Alert variant="destructive">
                <AlertTitle>{t("tenantRegistration.submitFailed")}</AlertTitle>
                <AlertDescription>{errors.root.message}</AlertDescription>
              </Alert>
            ) : null}

            <Field>
              <Button type="submit" disabled={submitting || invitationLoading}>
                {submitting ? (
                  <>
                    <LoaderCircleIcon className="animate-spin" />
                    {t("tenantRegistration.submitting")}
                  </>
                ) : (
                  t("tenantRegistration.submit")
                )}
              </Button>
              <FieldDescription className="text-center">
                {t("tenantRegistration.pendingHint")}
              </FieldDescription>
            </Field>
            <FieldDescription className="text-center">
              <Link href="/dashboard/login">{t("tenantRegistration.hasAccount")}</Link>
            </FieldDescription>
          </FieldGroup>
        </form>
      </CardContent>
    </Card>
  )
}

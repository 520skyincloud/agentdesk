"use client"

import Image from "next/image"
import Link from "next/link"
import { useRouter, useSearchParams } from "next/navigation"
import { startTransition, useEffect, useState } from "react"
import { toast } from "sonner"

import { useAuth } from "@/components/auth-provider"
import { fetchAuthOptions, loginWithEmailCode, loginWithPassword, sendLoginEmailCode, type AuthOptions } from "@/lib/api/auth"
import { useI18n } from "@/i18n/provider"
import { cn } from "@/lib/utils"
import { Button } from "@/components/ui/button"
import { Card, CardContent } from "@/components/ui/card"
import {
  Field,
  FieldDescription,
  FieldGroup,
  FieldLabel,
  FieldSeparator,
} from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { KeyRoundIcon, MailIcon } from "lucide-react"

function detectWxWorkEnvironment() {
  if (typeof navigator === "undefined") {
    return false
  }
  const userAgent = navigator.userAgent.toLowerCase()
  return userAgent.includes("wxwork")
}

export function LoginForm({
  className,
  ...props
}: React.ComponentProps<"div">) {
  const t = useI18n()
  const router = useRouter()
  const searchParams = useSearchParams()
  const { session } = useAuth()
  const [isPending, setIsPending] = useState(false)
  const [isWxWorkEnv, setIsWxWorkEnv] = useState(false)
  const [authOptions, setAuthOptions] = useState<AuthOptions>({
    wxworkEnabled: false,
    oidcEnabled: false,
    emailCodeEnabled: false,
  })
	const [email, setEmail] = useState("")
	const [emailCode, setEmailCode] = useState("")
	const [emailCodeSent, setEmailCodeSent] = useState(false)
  const nextPath = searchParams.get("next")
  const wxworkError = searchParams.get("wxworkError")
  const oidcError = searchParams.get("oidcError")
  const redirectPath =
    nextPath && nextPath.startsWith("/") ? nextPath : "/dashboard"
  const enabledProviderCount =
    Number(authOptions.wxworkEnabled) + Number(authOptions.oidcEnabled)

  useEffect(() => {
    if (session) {
      router.replace(redirectPath)
    }
  }, [redirectPath, router, session])

  useEffect(() => {
    if (wxworkError) {
      toast.error(wxworkError)
    }
  }, [wxworkError])

  useEffect(() => {
    if (oidcError) {
      toast.error(oidcError)
    }
  }, [oidcError])

  useEffect(() => {
    setIsWxWorkEnv(detectWxWorkEnvironment())
  }, [])

  useEffect(() => {
    let cancelled = false

    void fetchAuthOptions()
      .then((options) => {
        if (!cancelled) {
          setAuthOptions(options)
        }
      })
      .catch(() => {
        if (!cancelled) {
          setAuthOptions({ wxworkEnabled: false, oidcEnabled: false, emailCodeEnabled: false })
        }
      })

    return () => {
      cancelled = true
    }
  }, [])

  async function handleSubmit(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const formData = new FormData(event.currentTarget)
    const username = formData.get("username")?.toString().trim() ?? ""
    const password = formData.get("password")?.toString() ?? ""

    setIsPending(true)

    try {
      await loginWithPassword({ username, password })
      toast.success(t("auth.loginSuccess"))
      startTransition(() => {
        router.push(redirectPath)
      })
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("auth.loginFailed"))
    } finally {
      setIsPending(false)
    }
  }

  async function handleSendEmailCode() {
    if (!email.trim()) {
      toast.error("请输入已绑定的邮箱")
      return
    }
    setIsPending(true)
    try {
      await sendLoginEmailCode(email.trim())
      setEmailCodeSent(true)
      toast.success("验证码已发送，请检查邮箱")
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "验证码发送失败")
    } finally {
      setIsPending(false)
    }
  }

  async function handleEmailLogin() {
    if (!email.trim() || !emailCode.trim()) {
      toast.error("请输入邮箱和验证码")
      return
    }
    setIsPending(true)
    try {
      await loginWithEmailCode({ email: email.trim(), code: emailCode.trim() })
      toast.success(t("auth.loginSuccess"))
      startTransition(() => router.push(redirectPath))
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("auth.loginFailed"))
    } finally {
      setIsPending(false)
    }
  }

  return (
    <div
      className={cn("flex flex-col gap-6", className)}
      {...props}
    >
      <Card className="overflow-hidden p-0">
        <CardContent className="grid p-0 md:grid-cols-2">
          <form className="p-6 md:p-8" onSubmit={handleSubmit}>
            <FieldGroup>
              <div className="flex flex-col items-center gap-2 text-center">
                <h1 className="text-2xl font-bold">{t("auth.welcome")}</h1>
                <p className="text-balance text-muted-foreground">
                  {t("auth.loginDescription", { brand: t("app.brand") })}
                </p>
              </div>
              <Field>
                <FieldLabel htmlFor="username">{t("auth.username")}</FieldLabel>
                <Input
                  id="username"
                  name="username"
                  placeholder={t("auth.usernamePlaceholder")}
                  autoComplete="username"
                  required
                />
              </Field>
              <Field>
                <div className="flex items-center">
                  <FieldLabel htmlFor="password">{t("auth.password")}</FieldLabel>
                  <a
                    href="#"
                    className="ml-auto text-sm underline-offset-2 hover:underline"
                  >
                    {t("auth.forgotPassword")}
                  </a>
                </div>
                <Input
                  id="password"
                  name="password"
                  type="password"
                  placeholder={t("auth.passwordPlaceholder")}
                  autoComplete="current-password"
                  required
                />
              </Field>
              <Field>
                <Button type="submit" disabled={isPending}>
                  {isPending ? t("auth.signingIn") : t("auth.signIn")}
                </Button>
              </Field>
              {authOptions.emailCodeEnabled ? (
                <>
                  <FieldSeparator className="*:data-[slot=field-separator-content]:bg-card">邮箱验证码登录</FieldSeparator>
                  <Field>
                    <FieldLabel htmlFor="email-login">门店主邮箱</FieldLabel>
                    <Input id="email-login" type="email" value={email} onChange={(event) => setEmail(event.target.value)} placeholder="name@example.com" autoComplete="email" />
                  </Field>
                  <Field>
                    <FieldLabel htmlFor="email-code">验证码</FieldLabel>
                    <div className="grid grid-cols-[1fr_auto] gap-2">
                      <Input id="email-code" inputMode="numeric" value={emailCode} onChange={(event) => setEmailCode(event.target.value)} placeholder="6 位验证码" autoComplete="one-time-code" />
                      <Button type="button" variant="outline" onClick={() => void handleSendEmailCode()} disabled={isPending}>
                        <MailIcon className="size-4" />{emailCodeSent ? "重新发送" : "发送验证码"}
                      </Button>
                    </div>
                  </Field>
                  <Field><Button type="button" onClick={() => void handleEmailLogin()} disabled={isPending}>邮箱登录</Button></Field>
                </>
              ) : null}
              {enabledProviderCount > 0 ? (
                <>
                  <FieldSeparator className="*:data-[slot=field-separator-content]:bg-card">
                    {t("auth.continueWith")}
                  </FieldSeparator>
                  <Field
                    className={cn(
                      "grid gap-4",
                      enabledProviderCount === 1 ? "grid-cols-1" : "grid-cols-2"
                    )}
                  >
                    {authOptions.wxworkEnabled ? (
                      <Button
                        type="button"
                        variant="outline"
                        aria-label={t("auth.wxworkSignIn")}
                        onClick={() => {
                          const path = isWxWorkEnv
                            ? "/api/auth/wxwork_login"
                            : "/api/auth/wxwork_qr_login"
                          window.location.href = `${path}?next=${encodeURIComponent(redirectPath)}`
                        }}
                        >
                          <Image
                            src="/images/wxwork.svg"
                            alt=""
                          width={16}
                            height={16}
                            className="size-4 shrink-0"
                          />
                        <span>{t("auth.wxworkSignIn")}</span>
                      </Button>
                    ) : null}
                    {authOptions.oidcEnabled ? (
                      <Button
                        type="button"
                        variant="outline"
                        aria-label={t("auth.oidcSignIn")}
                        onClick={() => {
                          window.location.href = `/api/auth/oidc_login?next=${encodeURIComponent(redirectPath)}`
                        }}
                      >
                        <KeyRoundIcon className="size-4 shrink-0" />
                        <span>{t("auth.oidcSignIn")}</span>
                      </Button>
                    ) : null}
                  </Field>
                </>
              ) : null}
              {/* <FieldDescription className="text-center">
                {t("auth.noAccount")} <a href="#">{t("auth.signUp")}</a>
              </FieldDescription> */}
            </FieldGroup>
          </form>
          <div className="relative hidden bg-[#eef4fb] md:block">
            {/* shadcn login-04 uses a plain img for the decorative side panel. */}
            {/* eslint-disable-next-line @next/next/no-img-element */}
            <img
              src="/images/login-illustration.svg"
              alt={t("auth.imageAlt")}
              className="absolute inset-0 h-full w-full object-cover dark:brightness-[0.2] dark:grayscale"
            />
          </div>
        </CardContent>
      </Card>
      <FieldDescription className="px-6 text-center">
        {t("auth.termsPrefix")}{" "}
        <Link href="/legal/terms" target="_blank" rel="noreferrer">
          {t("auth.termsOfService")}
        </Link>{" "}
        {t("auth.termsJoiner")}{" "}
        <Link href="/legal/privacy" target="_blank" rel="noreferrer">
          {t("auth.privacyPolicy")}
        </Link>
        .
      </FieldDescription>
    </div>
  )
}

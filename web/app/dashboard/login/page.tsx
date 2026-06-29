import { LocaleSwitcher } from "@/components/locale-switcher"
import { LoginForm } from "@/components/login-form"
import { Suspense } from "react"

export default function LoginPage() {
  return (
    <div className="flex min-h-svh flex-col items-center justify-center bg-[#f4f8fd] p-6 md:p-10">
      <div className="w-full max-w-sm md:max-w-4xl">
        <div className="mb-4 flex justify-end">
          <LocaleSwitcher />
        </div>
        <Suspense fallback={<div className="min-h-96" />}>
          <LoginForm />
        </Suspense>
      </div>
    </div>
  )
}

import { Suspense } from "react"

import { LocaleSwitcher } from "@/components/locale-switcher"
import { TenantRegistrationForm } from "./_components/registration-form"

export default function TenantRegistrationPage() {
  return (
    <main className="flex min-h-svh flex-col bg-[#f4f8fd] px-4 py-6 sm:px-6 lg:py-10">
      <div className="mx-auto flex w-full max-w-3xl flex-1 flex-col">
        <div className="mb-4 flex justify-end">
          <LocaleSwitcher />
        </div>
        <div className="flex flex-1 items-center justify-center">
          <Suspense fallback={<div className="min-h-[680px] w-full" />}>
            <TenantRegistrationForm />
          </Suspense>
        </div>
      </div>
    </main>
  )
}

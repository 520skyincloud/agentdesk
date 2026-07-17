"use client"

import { Suspense } from "react"
import { useSearchParams } from "next/navigation"

import { EvaluationForm } from "./_components/evaluation-form"

export default function EvaluationPage() {
  return (
    <Suspense fallback={<div className="flex min-h-dvh items-center justify-center bg-[#f4f7fa] text-sm text-muted-foreground">正在加载评价...</div>}>
      <EvaluationContent />
    </Suspense>
  )
}

function EvaluationContent() {
  const searchParams = useSearchParams()
  return <EvaluationForm token={searchParams.get("token") || ""} />
}

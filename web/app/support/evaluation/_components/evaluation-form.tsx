"use client"

import Image from "next/image"
import { useEffect, useMemo, useState } from "react"
import { CheckCircle2Icon, Clock3Icon, StarIcon } from "lucide-react"

import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { Textarea } from "@/components/ui/textarea"
import {
  submitConversationEvaluation,
  validateConversationEvaluation,
  type PublicConversationEvaluation,
} from "@/lib/api/service-analytics"
import { ConversationEvaluationStatus } from "@/lib/generated/enums"
import { cn, formatDateTime } from "@/lib/utils"

const positiveTags = [
  { code: "resolved", label: "问题已解决" },
  { code: "fast", label: "回复及时" },
  { code: "professional", label: "服务专业" },
  { code: "friendly", label: "态度友好" },
]
const negativeTags = [
  { code: "unresolved", label: "问题未解决" },
  { code: "slow", label: "等待较久" },
  { code: "unclear", label: "回复不清楚" },
  { code: "rude", label: "服务态度需改善" },
]

export function EvaluationForm({ token }: { token: string }) {
  const [evaluation, setEvaluation] = useState<PublicConversationEvaluation | null>(null)
  const [rating, setRating] = useState(0)
  const [tags, setTags] = useState<string[]>([])
  const [comment, setComment] = useState("")
  const [loading, setLoading] = useState(true)
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState("")

  useEffect(() => {
    if (!token.trim()) {
      setError("评价链接无效")
      setLoading(false)
      return
    }
    void validateConversationEvaluation(token)
      .then((result) => {
        setEvaluation(result)
        if (result.rating > 0) setRating(result.rating)
      })
      .catch((reason) => setError(reason instanceof Error ? reason.message : "评价链接无效或已过期"))
      .finally(() => setLoading(false))
  }, [token])

  const availableTags = useMemo(() => rating >= 4 ? positiveTags : negativeTags, [rating])

  async function submit() {
    if (rating < 1 || rating > 5 || submitting) return
    setSubmitting(true)
    setError("")
    try {
      setEvaluation(await submitConversationEvaluation({ token, rating, tagCodes: tags, comment: comment.trim() }))
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "评价提交失败")
    } finally {
      setSubmitting(false)
    }
  }

  const completed = evaluation?.status === ConversationEvaluationStatus.Submitted
  const unavailable = evaluation?.status === ConversationEvaluationStatus.Expired || evaluation?.status === ConversationEvaluationStatus.Cancelled

  return (
    <main className="min-h-dvh bg-[#f4f7fa] px-4 py-8 text-foreground sm:py-14">
      <div className="mx-auto w-full max-w-lg border bg-background shadow-sm">
        <header className="flex items-center gap-3 border-b px-5 py-4">
          <Image src="/images/zhixi-weibao-logo.png" alt="智洗微宝" width={36} height={36} className="size-9 object-contain" unoptimized />
          <div className="min-w-0">
            <p className="truncate text-sm font-semibold">{evaluation?.companyName || "客户服务评价"}</p>
            <p className="text-xs text-muted-foreground">本次人工服务</p>
          </div>
        </header>

        {loading ? (
          <div className="space-y-4 p-6"><Skeleton className="h-6 w-40" /><Skeleton className="h-14 w-full" /><Skeleton className="h-24 w-full" /></div>
        ) : completed ? (
          <div className="flex min-h-72 flex-col items-center justify-center px-6 py-10 text-center">
            <CheckCircle2Icon className="size-12 text-emerald-600" />
            <h1 className="mt-4 text-xl font-semibold">评价已提交</h1>
            <div className="mt-3 flex gap-1" aria-label={`${evaluation.rating} 星`}>
              {Array.from({ length: 5 }, (_, index) => <StarIcon key={index} className={cn("size-5", index < evaluation.rating ? "fill-amber-400 text-amber-400" : "text-muted")} />)}
            </div>
            <p className="mt-4 text-sm text-muted-foreground">提交时间 {formatDateTime(evaluation.submittedAt)}</p>
          </div>
        ) : unavailable || error && !evaluation ? (
          <div className="flex min-h-72 flex-col items-center justify-center px-6 py-10 text-center">
            <Clock3Icon className="size-11 text-muted-foreground" />
            <h1 className="mt-4 text-lg font-semibold">无法继续评价</h1>
            <p className="mt-2 text-sm text-muted-foreground">{error || "该评价链接已失效"}</p>
          </div>
        ) : (
          <div className="space-y-6 p-6">
            <div>
              <h1 className="text-lg font-semibold">请为本次服务评分</h1>
              <div className="mt-4 flex justify-between gap-2">
                {Array.from({ length: 5 }, (_, index) => {
                  const value = index + 1
                  return (
                    <button key={value} type="button" className="flex size-12 items-center justify-center rounded-md outline-none hover:bg-amber-50 focus-visible:ring-2 focus-visible:ring-amber-500" onClick={() => { setRating(value); setTags([]) }} aria-label={`${value} 星`}>
                      <StarIcon className={cn("size-8", value <= rating ? "fill-amber-400 text-amber-400" : "text-zinc-300")} />
                    </button>
                  )
                })}
              </div>
            </div>

            {rating > 0 ? (
              <div className="space-y-3">
                <div className="flex flex-wrap gap-2">
                  {availableTags.map((tag) => <Button key={tag.code} type="button" variant={tags.includes(tag.code) ? "default" : "outline"} size="sm" onClick={() => setTags((current) => current.includes(tag.code) ? current.filter((item) => item !== tag.code) : [...current, tag.code])}>{tag.label}</Button>)}
                </div>
                <Textarea value={comment} maxLength={500} rows={4} placeholder="补充评价（选填）" onChange={(event) => setComment(event.target.value)} />
              </div>
            ) : null}
            {error ? <p className="text-sm text-destructive">{error}</p> : null}
            <Button className="w-full" disabled={rating === 0 || submitting} onClick={() => void submit()}>{submitting ? "提交中" : "提交评价"}</Button>
            {evaluation?.expiresAt ? <p className="text-center text-xs text-muted-foreground">有效期至 {formatDateTime(evaluation.expiresAt)}</p> : null}
          </div>
        )}
      </div>
    </main>
  )
}

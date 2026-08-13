"use client"

import { useEffect, useMemo, useState } from "react"
import { toast } from "sonner"

import { useAuth } from "@/components/auth-provider"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent } from "@/components/ui/card"
import { Switch } from "@/components/ui/switch"
import {
  fetchReplyActions,
  updateReplyActionStatus,
  type ReplyAction,
} from "@/lib/api/admin"
import { RefreshCwIcon } from "lucide-react"

const kindLabels: Record<string, string> = {
  builtin: "内置",
  external: "外部",
  tool: "工具",
}

export default function ReplyActionsPage() {
  const { session } = useAuth()
  const canManage = Boolean(
    session?.isPlatformAccount && session?.permissions?.includes("aiConfig.update"),
  )
  const [items, setItems] = useState<ReplyAction[]>([])
  const [loading, setLoading] = useState(false)
  const [toggling, setToggling] = useState<number | null>(null)

  const load = useMemo(
    () => async () => {
      setLoading(true)
      try {
        setItems(await fetchReplyActions())
      } catch (error) {
        toast.error(error instanceof Error ? error.message : "加载动作目录失败")
      } finally {
        setLoading(false)
      }
    },
    [],
  )

  useEffect(() => {
    void load()
  }, [load])

  async function handleToggle(item: ReplyAction, next: boolean) {
    if (!canManage) return
    setToggling(item.id)
    try {
      await updateReplyActionStatus({ id: item.id, enabled: next })
      setItems((prev) =>
        prev.map((it) => (it.id === item.id ? { ...it, enabled: next } : it)),
      )
      toast.success(`已${next ? "启用" : "停用"}「${item.name}」`)
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "状态更新失败")
    } finally {
      setToggling(null)
    }
  }

  return (
    <div className="mx-auto max-w-5xl space-y-6 p-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-semibold text-foreground">回复动作目录</h1>
          <p className="mt-1 text-sm text-muted-foreground">
            统一登记 AI 客服能执行的动作。外部系统未接入时不可启用。
          </p>
        </div>
        <Button variant="outline" size="sm" onClick={() => void load()} disabled={loading}>
          <RefreshCwIcon className="size-4" />
          刷新
        </Button>
      </div>

      <div className="grid gap-4">
        {items.map((item) => {
          const externalUnprovisioned = item.kind === "external" && !item.provisioned
          const disabled = externalUnprovisioned
          return (
            <Card key={item.id}>
              <CardContent className="flex items-start justify-between gap-4 p-5">
                <div className="min-w-0 space-y-1.5">
                  <div className="flex items-center gap-2">
                    <span className="font-medium text-foreground">{item.name}</span>
                    <Badge variant="outline">{kindLabels[item.kind] ?? item.kind}</Badge>
                    {item.requireConfirmation ? (
                      <Badge variant="secondary">需二次确认</Badge>
                    ) : null}
                    {externalUnprovisioned ? (
                      <Badge variant="destructive">未接入</Badge>
                    ) : null}
                  </div>
                  <div className="font-mono text-xs text-muted-foreground">
                    {item.code}
                  </div>
                  {item.description ? (
                    <p className="text-sm text-muted-foreground">{item.description}</p>
                  ) : null}
                </div>
                <div className="flex shrink-0 items-center gap-2">
                  <span className="text-xs text-muted-foreground">
                    {item.enabled ? "已启用" : "已停用"}
                  </span>
                  <Switch
                    checked={item.enabled}
                    disabled={disabled || !canManage || toggling === item.id}
                    onCheckedChange={(next) => void handleToggle(item, next)}
                    aria-label={`切换 ${item.name}`}
                  />
                </div>
              </CardContent>
            </Card>
          )
        })}
        {!loading && items.length === 0 ? (
          <p className="py-8 text-center text-sm text-muted-foreground">暂无动作</p>
        ) : null}
      </div>
    </div>
  )
}

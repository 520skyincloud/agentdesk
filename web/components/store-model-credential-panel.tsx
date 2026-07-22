"use client"

import { useCallback, useEffect, useState } from "react"
import {
  CheckCircle2Icon,
  KeyRoundIcon,
  LoaderCircleIcon,
  RefreshCwIcon,
  ShieldCheckIcon,
} from "lucide-react"
import { toast } from "sonner"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import {
  fetchStoreModelCredential,
  updateStoreModelCredential,
  type StoreModelCredential,
} from "@/lib/api/admin"
import { formatDateTime } from "@/lib/utils"
import { useStoreModelCredentialRealtime } from "@/hooks/use-store-model-credential-realtime"

type Props = {
  storeId: number
  onChanged?: (credential: StoreModelCredential) => void
}

function statusVariant(status: string): "default" | "secondary" | "outline" | "destructive" {
  if (status === "active" || status === "ready" || status === "passed") return "default"
  if (status === "failed") return "destructive"
  if (status === "testing" || status === "syncing") return "secondary"
  return "outline"
}

function statusLabel(status: string) {
  switch (status) {
    case "active":
      return "已生效"
    case "ready":
      return "已同步"
    case "passed":
      return "测试通过"
    case "failed":
      return "失败"
    case "testing":
      return "测试中"
    case "syncing":
      return "同步中"
    case "unconfigured":
      return "未配置"
    default:
      return status || "未配置"
  }
}

export function StoreModelCredentialPanel({ storeId, onChanged }: Props) {
  const [credential, setCredential] = useState<StoreModelCredential | null>(null)
  const [apiKey, setApiKey] = useState("")
  const [loading, setLoading] = useState(false)
  const [saving, setSaving] = useState(false)

  const load = useCallback(async (showError = true) => {
    if (storeId <= 0) {
      setCredential(null)
      return null
    }
    setLoading(true)
    try {
      const result = await fetchStoreModelCredential(storeId)
      setCredential(result)
      onChanged?.(result)
      return result
    } catch (error) {
      if (showError) {
        toast.error(error instanceof Error ? error.message : "读取门店模型凭据失败")
      }
      return null
    } finally {
      setLoading(false)
    }
  }, [onChanged, storeId])

  useEffect(() => {
    setApiKey("")
    void load()
  }, [load])

  useStoreModelCredentialRealtime(
    storeId,
    () => {
      void load(false)
    },
    undefined,
    () => {
      void load(false)
    },
  )

  async function updateCredential() {
    const nextKey = apiKey.trim()
    if (!nextKey) {
      toast.error("请输入新的模型密钥")
      return
    }
    setSaving(true)
    try {
      const result = await updateStoreModelCredential(storeId, nextKey)
      setCredential(result)
      setApiKey("")
      onChanged?.(result)
      toast.success("新密钥已通过模型测试和 FastGPT 同步，现已生效")
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "密钥更新失败，旧密钥仍在使用")
    } finally {
      setSaving(false)
    }
  }

  return (
    <section className="border border-border bg-card">
      <div className="flex flex-col gap-3 border-b border-border px-4 py-4 sm:flex-row sm:items-start sm:justify-between">
        <div className="flex min-w-0 items-start gap-3">
          <div className="flex size-9 shrink-0 items-center justify-center rounded-md bg-primary/10 text-primary">
            <KeyRoundIcon className="size-4" />
          </div>
          <div className="min-w-0">
            <h2 className="text-base font-semibold">门店唯一模型密钥</h2>
            <p className="mt-1 text-sm leading-6 text-muted-foreground">
              当前门店只维护一个密钥，系统会自动用于回复与知识库链路；模型地址和模型配置由平台统一管理。
            </p>
          </div>
        </div>
        <Button type="button" variant="outline" size="icon" disabled={loading || saving || storeId <= 0} onClick={() => void load()} title="刷新状态">
          <RefreshCwIcon className={loading ? "size-4 animate-spin" : "size-4"} />
        </Button>
      </div>

      <div className="grid gap-5 p-4 lg:grid-cols-[minmax(0,1fr)_minmax(280px,0.7fr)]">
        <div className="space-y-4">
          <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
            <div className="border border-border p-3">
              <div className="text-xs text-muted-foreground">当前门店</div>
              <div className="mt-1 truncate font-medium">{credential?.storeName || "正在读取"}</div>
            </div>
            <div className="border border-border p-3">
              <div className="text-xs text-muted-foreground">凭据状态</div>
              <div className="mt-1">
                <Badge variant={statusVariant(credential?.credentialStatus || "")}>
                  {statusLabel(credential?.credentialStatus || "")}
                </Badge>
              </div>
            </div>
            <div className="border border-border p-3">
              <div className="text-xs text-muted-foreground">凭据版本</div>
              <div className="mt-1 font-medium">{credential?.credentialRevision || "-"}</div>
            </div>
            <div className="border border-border p-3">
              <div className="text-xs text-muted-foreground">FastGPT 同步</div>
              <div className="mt-1">
                <Badge variant={statusVariant(credential?.fastgptSyncStatus || "")}>
                  {statusLabel(credential?.fastgptSyncStatus || "")}
                </Badge>
              </div>
            </div>
          </div>

          <div className="flex flex-wrap gap-x-5 gap-y-2 text-xs text-muted-foreground">
            <span>最后测试：{formatDateTime(credential?.lastTestedAt)}</span>
            <span>测试耗时：{credential?.lastTestLatencyMs ? `${credential.lastTestLatencyMs} ms` : "-"}</span>
            <span>FastGPT 同步：{formatDateTime(credential?.fastgptLastSyncedAt)}</span>
          </div>
        </div>

        <div className="border border-border p-4">
          <div className="flex items-start gap-2 text-sm">
            <ShieldCheckIcon className="mt-0.5 size-4 shrink-0 text-primary" />
            <p className="leading-6 text-muted-foreground">
              系统不会返回旧密钥。输入新密钥后会真实测试当前回复与知识库核心链路并同步 FastGPT，通过后才切换；语音模型不阻断切换，失败时旧版本继续运行。
            </p>
          </div>
          <div className="mt-4 space-y-2">
            <Label htmlFor="store-model-api-key">新模型密钥</Label>
            <Input
              id="store-model-api-key"
              type="password"
              autoComplete="new-password"
              value={apiKey}
              disabled={saving || storeId <= 0}
              placeholder={credential?.hasKey ? "输入新密钥以替换当前版本" : "输入门店模型密钥"}
              onChange={(event) => setApiKey(event.target.value)}
            />
          </div>
          <Button className="mt-3 w-full" disabled={saving || !apiKey.trim() || storeId <= 0} onClick={() => void updateCredential()}>
            {saving ? <LoaderCircleIcon className="size-4 animate-spin" /> : <CheckCircle2Icon className="size-4" />}
            {saving ? "正在测试并同步" : credential?.hasKey ? "验证并切换密钥" : "验证并启用密钥"}
          </Button>
        </div>
      </div>
    </section>
  )
}

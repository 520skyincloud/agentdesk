"use client"

import { useEffect, useMemo, useState } from "react"
import { CheckCircle2Icon, FlaskConicalIcon, LoaderCircleIcon, ShieldCheckIcon } from "lucide-react"
import { toast } from "sonner"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import {
  fetchFastGPTModelProfile,
  fetchFastGPTProfileTemplate,
  testFastGPTModelProfile,
  updateFastGPTModelProfile,
  type FastGPTProfileTemplate,
  type FastGPTProfileTemplateCredential,
  type FastGPTModelCredential,
  type FastGPTModelProfile,
  type FastGPTModelProfilePayload,
  type FastGPTModelProfileTestResult,
  type WxWorkProtocolInstance,
} from "@/lib/api/admin"
import { repairMojibakeText } from "@/lib/utils"

type CredentialDraft = FastGPTModelCredential & { apiKey: string }

type ProfileDraft = {
  id: string
  name: string
  revision: number
  embedding: CredentialDraft
  documentParser: CredentialDraft
  vision: CredentialDraft
  rerank: CredentialDraft
}

type Props = {
  open: boolean
  instance: WxWorkProtocolInstance | null
  canSave: boolean
  onOpenChange: (open: boolean) => void
  onSaved?: () => void
}

const emptyCredential = (): CredentialDraft => ({
  provider: "openai",
  baseUrl: "",
  model: "",
  apiKey: "",
  keyConfigured: false,
  keyFingerprint: "",
})

function toCredentialDraft(value?: FastGPTModelCredential | null): CredentialDraft {
  return { ...emptyCredential(), ...(value || {}), apiKey: "" }
}

function inheritTemplateCredential(
  profile: FastGPTModelCredential | null | undefined,
  template: FastGPTProfileTemplateCredential | null | undefined,
): CredentialDraft {
  const current = toCredentialDraft(profile)
  if (!template) return current
  return {
    ...current,
    provider: template.provider,
    baseUrl: template.baseUrl,
    model: template.model,
  }
}

function createDraft(
  instance: WxWorkProtocolInstance | null,
  profile?: FastGPTModelProfile | null,
  template?: FastGPTProfileTemplate | null,
): ProfileDraft {
  return {
    id: profile?.id || "",
    name: template?.revision ? template.name : profile?.name || `${repairMojibakeText(instance?.employeeName || instance?.storeName || "门店")} 知识库模型`,
    revision: profile?.revision || 0,
    embedding: inheritTemplateCredential(profile?.embedding, template?.embedding),
    documentParser: inheritTemplateCredential(profile?.documentParser, template?.documentParser),
    vision: inheritTemplateCredential(profile?.vision, template?.vision),
    rerank: inheritTemplateCredential(profile?.rerank, template?.rerank),
  }
}

function credentialIsComplete(value: CredentialDraft) {
  return Boolean(value.provider.trim() && value.baseUrl.trim() && value.model.trim() && (value.apiKey.trim() || value.keyConfigured))
}

function CredentialEditor({
  title,
  description,
  value,
  disabled,
  routingDisabled,
  onChange,
}: {
  title: string
  description: string
  value: CredentialDraft
  disabled: boolean
  routingDisabled: boolean
  onChange: (next: CredentialDraft) => void
}) {
  const update = (field: keyof CredentialDraft, next: string) => onChange({ ...value, [field]: next })
  return (
    <section className="border-t border-border py-4 first:border-t-0 first:pt-0">
      <div className="mb-3 flex items-start justify-between gap-3">
        <div>
          <h3 className="text-sm font-semibold text-foreground">{title}</h3>
          <p className="mt-1 text-xs leading-5 text-muted-foreground">{description}</p>
        </div>
        {value.keyConfigured ? <Badge variant="outline">密钥已配置 · {value.keyFingerprint || "已加密"}</Badge> : null}
      </div>
      <div className="grid gap-3 md:grid-cols-2">
        <div className="space-y-1.5">
          <Label>Provider</Label>
          <Input value={value.provider} disabled={disabled || routingDisabled} placeholder="openai" onChange={(event) => update("provider", event.target.value)} />
        </div>
        <div className="space-y-1.5">
          <Label>模型名</Label>
          <Input value={value.model} disabled={disabled || routingDisabled} placeholder="请输入供应商真实模型名" onChange={(event) => update("model", event.target.value)} />
        </div>
        <div className="space-y-1.5 md:col-span-2">
          <Label>Base URL</Label>
          <Input value={value.baseUrl} disabled={disabled || routingDisabled} placeholder="https://api.example.com/v1" onChange={(event) => update("baseUrl", event.target.value)} />
        </div>
        <div className="space-y-1.5 md:col-span-2">
          <Label>API Key</Label>
          <Input
            type="password"
            value={value.apiKey}
            disabled={disabled}
            autoComplete="new-password"
            placeholder={value.keyConfigured ? "留空沿用当前加密密钥；输入新值则替换" : "请输入 API Key"}
            onChange={(event) => update("apiKey", event.target.value)}
          />
        </div>
      </div>
    </section>
  )
}

export function FastGPTModelProfileDialog({ open, instance, canSave, onOpenChange, onSaved }: Props) {
  const [draft, setDraft] = useState<ProfileDraft>(() => createDraft(instance))
  const [template, setTemplate] = useState<FastGPTProfileTemplate | null>(null)
  const [loading, setLoading] = useState(false)
  const [testing, setTesting] = useState(false)
  const [saving, setSaving] = useState(false)
  const [testResult, setTestResult] = useState<FastGPTModelProfileTestResult | null>(null)

  useEffect(() => {
    if (!open || !instance) return
    let cancelled = false
    setLoading(true)
    setTestResult(null)
    Promise.all([fetchFastGPTModelProfile(instance.id), fetchFastGPTProfileTemplate()])
      .then(([profile, profileTemplate]) => {
        if (!cancelled) {
          setTemplate(profileTemplate)
          setDraft(createDraft(instance, profile, profileTemplate))
        }
      })
      .catch((error) => {
        if (!cancelled) toast.error(error instanceof Error ? error.message : "读取 FastGPT 模型 Profile 失败")
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [instance, open])

  const valid = useMemo(() => {
    return Boolean(
      draft.name.trim() &&
        credentialIsComplete(draft.embedding) &&
        credentialIsComplete(draft.documentParser) &&
        credentialIsComplete(draft.vision) &&
        credentialIsComplete(draft.rerank),
    )
  }, [draft])

  function mutate(mutator: (current: ProfileDraft) => ProfileDraft) {
    setDraft((current) => mutator(current))
    setTestResult(null)
  }

  function buildPayload(testToken = ""): FastGPTModelProfilePayload {
    return {
      wxWorkInstanceId: instance?.id || 0,
      profileId: draft.id,
      name: draft.name.trim(),
      embedding: draft.embedding,
      documentParser: draft.documentParser,
      vision: draft.vision,
      rerankEnabled: true,
      rerank: draft.rerank,
      testToken,
    }
  }

  async function handleTest() {
    if (!valid) {
      toast.error("请先完整填写启用阶段的地址、模型和密钥")
      return
    }
    setTesting(true)
    try {
      const result = await testFastGPTModelProfile(buildPayload())
      setTestResult(result)
      toast.success("所有启用阶段均已通过真实调用")
    } catch (error) {
      setTestResult(null)
      toast.error(error instanceof Error ? error.message : "真实模型测试失败")
    } finally {
      setTesting(false)
    }
  }

  async function handleSave() {
    if (!testResult?.testToken) {
      toast.error("当前配置必须先通过真实测试")
      return
    }
    setSaving(true)
    try {
      const result = await updateFastGPTModelProfile(buildPayload(testResult.testToken))
      setDraft(createDraft(instance, result.profile, template))
      setTestResult(null)
      toast.success(`已保存并绑定到 ${result.boundDatasetCount} 个门店知识库`)
      onSaved?.()
      onOpenChange(false)
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "保存 FastGPT 模型 Profile 失败")
    } finally {
      setSaving(false)
    }
  }

  const disabled = loading || testing || saving || !canSave
  const routingInherited = Boolean(template?.revision)
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="flex max-h-[calc(100dvh-1.5rem)] w-[calc(100%-1.5rem)] flex-col gap-0 overflow-hidden p-0 sm:max-w-3xl">
        <DialogHeader className="shrink-0 border-b px-5 py-4 pr-12">
          <DialogTitle>FastGPT 知识库模型设置</DialogTitle>
          <DialogDescription>
            {repairMojibakeText(instance?.employeeName || "当前员工号")} 所属门店的知识解析、向量化和视觉模型。此处密钥由 FastGPT 加密保存，不影响 Agent Desk 回复生成模型。
          </DialogDescription>
        </DialogHeader>
        <div className="min-h-0 flex-1 overflow-y-auto overscroll-contain px-5 py-4">
          {loading ? (
            <div className="flex min-h-48 items-center justify-center gap-2 text-muted-foreground">
              <LoaderCircleIcon className="size-4 animate-spin" /> 正在读取门店 Profile
            </div>
          ) : (
            <>
              <div className="mb-4 flex items-start gap-3 rounded-lg border border-blue-200 bg-blue-50 p-3 text-sm text-blue-950">
                <ShieldCheckIcon className="mt-0.5 size-4 shrink-0" />
                <div className="leading-6">
                  Provider、Base URL 和模型名统一继承平台模板{template?.revision ? `版本 ${template.revision}` : ""}，此处只维护当前门店的独立 API Key。密钥不会返回前端；已配置密钥留空即可沿用。保存 Profile 不会覆盖知识库现有的检索限制。
                </div>
              </div>
              <div className="mb-4 grid gap-3 md:grid-cols-[1fr_auto] md:items-end">
                <div className="space-y-1.5">
                  <Label>Profile 名称</Label>
                  <Input value={draft.name} disabled={disabled || routingInherited} onChange={(event) => mutate((current) => ({ ...current, name: event.target.value }))} />
                </div>
                <div className="flex h-8 items-center gap-2 text-xs text-muted-foreground">
                  <Badge variant="secondary">版本 {draft.revision || "未创建"}</Badge>
                  <Badge variant="outline">{draft.id ? "已绑定" : "待创建"}</Badge>
                </div>
              </div>
              <CredentialEditor title="向量模型" description="文件索引和客户问题检索使用的 Embedding 模型。" value={draft.embedding} disabled={disabled} routingDisabled={routingInherited} onChange={(value) => mutate((current) => ({ ...current, embedding: value }))} />
              <CredentialEditor title="文档理解模型" description="文档解析和问答拆分使用的文本模型。" value={draft.documentParser} disabled={disabled} routingDisabled={routingInherited} onChange={(value) => mutate((current) => ({ ...current, documentParser: value }))} />
              <CredentialEditor title="视觉理解模型" description="图片知识解析使用的多模态模型；测试会真实发送一张内置图片。" value={draft.vision} disabled={disabled} routingDisabled={routingInherited} onChange={(value) => mutate((current) => ({ ...current, vision: value }))} />
              <CredentialEditor
                title="重排模型"
                description="必须配置。使用 OpenAI 兼容的 rerank 请求格式，并按知识库现有的重排保留条数筛选召回结果。"
                value={draft.rerank}
                disabled={disabled}
                routingDisabled={routingInherited}
                onChange={(value) => mutate((current) => ({ ...current, rerank: value }))}
              />
              {testResult ? (
                <div className="mt-2 flex flex-wrap items-center gap-2 rounded-lg border border-emerald-200 bg-emerald-50 p-3 text-sm text-emerald-950">
                  <CheckCircle2Icon className="size-4" />
                  {testResult.results.map((item) => <Badge key={item.stage} variant="outline">{item.stage} 已通过</Badge>)}
                  <span className="text-xs">测试凭证 10 分钟内有效</span>
                </div>
              ) : null}
            </>
          )}
        </div>
        <DialogFooter className="m-0 shrink-0 rounded-none rounded-b-xl px-5 py-4">
          <Button type="button" variant="outline" onClick={() => onOpenChange(false)} disabled={testing || saving}>取消</Button>
          <Button type="button" variant="outline" onClick={() => void handleTest()} disabled={disabled || !valid}>
            {testing ? <LoaderCircleIcon className="size-4 animate-spin" /> : <FlaskConicalIcon className="size-4" />}
            {testing ? "真实测试中" : "测试全部模型"}
          </Button>
          <Button type="button" onClick={() => void handleSave()} disabled={disabled || !testResult?.testToken}>保存并绑定</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

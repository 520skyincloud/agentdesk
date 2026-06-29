"use client"

import { useEffect, useState } from "react"
import { SaveIcon } from "lucide-react"
import { toast } from "sonner"

import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Switch } from "@/components/ui/switch"
import { fetchStorageSetting, updateStorageSetting, type StorageSetting } from "@/lib/api/admin"

const defaultSetting: StorageSetting = {
  defaultProvider: "oss",
  maxUploadSizeMb: 50,
  localRoot: "./data/storage",
  localBaseUrl: "/uploads",
  ossEndpoint: "oss-cn-beijing.aliyuncs.com",
  ossBucket: "skychucun",
  ossAccessKeyId: "",
  ossAccessKeySecret: "",
  ossBaseUrl: "https://skychucun.oss-cn-beijing.aliyuncs.com",
  ossObjectPrefix: "desk",
  ossPrivate: false,
  ossSignedUrlExpireSeconds: 600,
  wecdnBaseUrl: "",
  publicAssetBaseUrl: "http://kefuceshi.omnireva.com",
}

export default function StorageSettingsPage() {
  const [form, setForm] = useState<StorageSetting>(defaultSetting)
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    let mounted = true
    async function load() {
      setLoading(true)
      try {
        const data = await fetchStorageSetting()
        if (!mounted) return
        setForm({
          ...defaultSetting,
          ...data,
          ossAccessKeySecret: "",
        })
      } catch (error) {
        toast.error(error instanceof Error ? error.message : "加载存储设置失败")
      } finally {
        if (mounted) setLoading(false)
      }
    }
    void load()
    return () => {
      mounted = false
    }
  }, [])

  function patch(next: Partial<StorageSetting>) {
    setForm((current) => ({ ...current, ...next }))
  }

  async function handleSave() {
    setSaving(true)
    try {
      const saved = await updateStorageSetting(form)
      setForm({ ...defaultSetting, ...saved, ossAccessKeySecret: "" })
      toast.success("存储设置已保存")
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "保存失败")
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="flex h-full flex-col gap-4 p-4 lg:p-6">
      <div className="flex items-center justify-between gap-3">
        <div>
          <h1 className="text-xl font-semibold">存储设置</h1>
          <p className="mt-1 text-sm text-muted-foreground">
            配置 OSS、文件目录和企微富媒体发送所需的公网访问地址。
          </p>
        </div>
        <Button onClick={() => void handleSave()} disabled={loading || saving}>
          <SaveIcon className="size-4" />
          {saving ? "保存中" : "保存"}
        </Button>
      </div>

      <div className="grid gap-4 xl:grid-cols-2">
        <section className="agentdesk-surface rounded-2xl p-4">
          <h2 className="text-base font-medium">OSS 存储桶</h2>
          <div className="mt-4 grid gap-4 sm:grid-cols-2">
            <Field label="默认存储类型">
              <select
                className="h-10 rounded-xl border border-[#dbe7f6] bg-white px-3 text-sm shadow-[0_4px_12px_rgba(37,99,235,0.06)]"
                value={form.defaultProvider}
                onChange={(event) => patch({ defaultProvider: event.target.value })}
              >
                <option value="local">本地存储</option>
                <option value="oss">阿里云 OSS</option>
              </select>
            </Field>
            <Field label="上传大小上限 MB">
              <Input
                type="number"
                min={1}
                value={form.maxUploadSizeMb}
                onChange={(event) => patch({ maxUploadSizeMb: Number(event.target.value || 1) })}
              />
            </Field>
            <Field label="Endpoint">
              <Input value={form.ossEndpoint} onChange={(event) => patch({ ossEndpoint: event.target.value })} />
            </Field>
            <Field label="Bucket">
              <Input value={form.ossBucket} onChange={(event) => patch({ ossBucket: event.target.value })} />
            </Field>
            <Field label="目录前缀">
              <Input value={form.ossObjectPrefix} onChange={(event) => patch({ ossObjectPrefix: event.target.value })} placeholder="desk" />
            </Field>
            <Field label="公开访问 Base URL">
              <Input value={form.ossBaseUrl} onChange={(event) => patch({ ossBaseUrl: event.target.value })} />
            </Field>
            <Field label="AccessKey ID">
              <Input value={form.ossAccessKeyId} onChange={(event) => patch({ ossAccessKeyId: event.target.value })} />
            </Field>
            <Field label={form.ossAccessKeySecretSet ? "AccessKey Secret（已设置）" : "AccessKey Secret"}>
              <Input
                type="password"
                value={form.ossAccessKeySecret || ""}
                onChange={(event) => patch({ ossAccessKeySecret: event.target.value })}
                placeholder={form.ossAccessKeySecretSet ? "留空表示不修改" : "请输入 AccessKey Secret"}
              />
            </Field>
          </div>
          <div className="mt-4 flex items-center gap-2">
            <Switch checked={form.ossPrivate} onCheckedChange={(checked) => patch({ ossPrivate: checked })} />
            <span className="text-sm">私有 Bucket 使用签名 URL</span>
          </div>
        </section>

        <section className="agentdesk-surface rounded-2xl p-4">
          <h2 className="text-base font-medium">企微富媒体链路</h2>
          <div className="mt-4 grid gap-4">
            <Field label="AgentDesk 公网地址">
              <Input value={form.publicAssetBaseUrl} onChange={(event) => patch({ publicAssetBaseUrl: event.target.value })} />
            </Field>
            <Field label="私有化云存储 wecdn_web 地址">
              <Input value={form.wecdnBaseUrl} onChange={(event) => patch({ wecdnBaseUrl: event.target.value })} placeholder="http://112.124.109.106:34789" />
            </Field>
            <Field label="本地存储目录">
              <Input value={form.localRoot} onChange={(event) => patch({ localRoot: event.target.value })} />
            </Field>
            <Field label="本地访问路径">
              <Input value={form.localBaseUrl} onChange={(event) => patch({ localBaseUrl: event.target.value })} />
            </Field>
          </div>
          <p className="mt-4 rounded-xl border border-[#dbe7f6] bg-[#f6f9ff] px-3 py-2 text-xs leading-5 text-muted-foreground shadow-inner shadow-blue-100/30">
            网页客服发送图片、语音、文件、视频、GIF 时，系统会先把文件上传到 OSS 的 desk 目录，再让 wecdn_web 拉取公网文件并换取企微协议 file_id/aes_key/md5 后发送。
          </p>
        </section>
      </div>
    </div>
  )
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="grid gap-1.5">
      <Label className="text-xs text-muted-foreground">{label}</Label>
      {children}
    </div>
  )
}

"use client"

import { zodResolver } from "@hookform/resolvers/zod"
import { useEffect, useMemo, useState } from "react"
import { Controller, type Resolver, useForm } from "react-hook-form"
import { toast } from "sonner"
import { z } from "zod/v4"

import { OptionCombobox } from "@/components/option-combobox"
import { ProjectDialog } from "@/components/project-dialog"
import { Button } from "@/components/ui/button"
import { Field, FieldContent, FieldError, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import {
  fetchWxWorkProtocolInstances,
  provisionFastGPTDataset,
  type WxWorkProtocolInstance,
} from "@/lib/api/admin"

type FormValues = {
	storeStaffBindingId: string
  name: string
}

type FastGPTProvisionDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  onProvisioned: () => Promise<void>
}

const formSchema = z.object({
	storeStaffBindingId: z.string().trim().refine((value) => Number(value) > 0, "请选择门店员工号"),
  name: z.string().trim().min(1, "请填写知识库名称").max(100, "知识库名称不能超过 100 个字符"),
})

function buildStoreOptions(instances: WxWorkProtocolInstance[]) {
	const byBinding = new Map<number, { value: string; label: string }>()
	for (const instance of instances) {
		if (instance.storeId <= 0 || instance.storeStaffBindingId <= 0 || byBinding.has(instance.storeStaffBindingId)) {
      continue
    }
    const storeName = instance.storeName.trim() || instance.employeeName.trim() || `门店 ${instance.storeId}`
    const employeeName = instance.employeeName.trim()
		byBinding.set(instance.storeStaffBindingId, {
			value: String(instance.storeStaffBindingId),
      label: employeeName && employeeName !== storeName ? `${storeName} (${employeeName})` : storeName,
    })
  }
	return [...byBinding.values()]
}

export function FastGPTProvisionDialog({ open, onOpenChange, onProvisioned }: FastGPTProvisionDialogProps) {
  const [instances, setInstances] = useState<WxWorkProtocolInstance[]>([])
  const [loadingInstances, setLoadingInstances] = useState(false)
  const [submitting, setSubmitting] = useState(false)
  const storeOptions = useMemo(() => buildStoreOptions(instances), [instances])
  const resolver = useMemo(() => zodResolver(formSchema) as Resolver<FormValues>, [])
  const form = useForm<FormValues>({
    resolver,
		defaultValues: { storeStaffBindingId: "", name: "" },
  })
  const { control, handleSubmit, register, reset, setError, formState: { errors } } = form

  useEffect(() => {
    if (!open) {
      return
    }
    let cancelled = false
    setLoadingInstances(true)
    void fetchWxWorkProtocolInstances({ limit: 200 })
      .then((page) => {
        if (!cancelled) {
          setInstances(page.results)
        }
      })
      .catch((error) => {
        if (!cancelled) {
          toast.error(error instanceof Error ? error.message : "加载门店员工号失败")
        }
      })
      .finally(() => {
        if (!cancelled) {
          setLoadingInstances(false)
        }
      })
    return () => {
      cancelled = true
    }
  }, [open])

  useEffect(() => {
    if (open) {
		reset({ storeStaffBindingId: "", name: "" })
    }
  }, [open, reset])

	async function submit(values: FormValues) {
		setSubmitting(true)
		try {
			const bindingId = Number(values.storeStaffBindingId)
			const selected = instances.find((instance) => instance.storeStaffBindingId === bindingId)
			if (!selected || selected.storeId <= 0) {
				throw new Error("所选门店员工号归属已变化，请刷新后重试")
			}
			await provisionFastGPTDataset(selected.storeId, bindingId, values.name.trim())
      await onProvisioned()
      toast.success("已提交 FastGPT 知识库创建任务")
      onOpenChange(false)
    } catch (error) {
      setError("name", {
        type: "server",
        message: error instanceof Error ? error.message : "创建知识库失败",
      })
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <ProjectDialog
      open={open}
      onOpenChange={(nextOpen) => {
        if (!submitting) {
          onOpenChange(nextOpen)
        }
      }}
      title="新建门店知识库"
      description="平台会在 FastGPT 中创建数据集；门店侧不需要填写地址、密钥或模型参数。"
      size="md"
      footer={(
        <>
          <Button type="button" variant="outline" disabled={submitting} onClick={() => onOpenChange(false)}>
            取消
          </Button>
          <Button type="submit" form="fastgpt-provision-form" disabled={loadingInstances || submitting}>
            {submitting ? "正在创建" : "创建知识库"}
          </Button>
        </>
      )}
    >
      <form id="fastgpt-provision-form" className="space-y-4" onSubmit={handleSubmit(submit)}>
		<Field data-invalid={Boolean(errors.storeStaffBindingId)}>
          <FieldLabel htmlFor="fastgpt-store">门店员工号</FieldLabel>
          <FieldContent>
            <Controller
              control={control}
				name="storeStaffBindingId"
              render={({ field }) => (
                <OptionCombobox
                  value={field.value}
                  onChange={field.onChange}
                  options={storeOptions}
                  placeholder={loadingInstances ? "正在加载门店员工号" : "选择要创建知识库的门店员工号"}
                  searchPlaceholder="搜索门店员工号"
                  emptyText="暂无可用门店员工号"
                />
              )}
            />
			<FieldError errors={[errors.storeStaffBindingId]} />
          </FieldContent>
        </Field>
        <Field data-invalid={Boolean(errors.name)}>
          <FieldLabel htmlFor="fastgpt-dataset-name">知识库名称</FieldLabel>
          <FieldContent>
            <Input id="fastgpt-dataset-name" placeholder="例如：2026 年门店服务资料" {...register("name")} />
            <FieldError errors={[errors.name]} />
          </FieldContent>
        </Field>
      </form>
    </ProjectDialog>
  )
}

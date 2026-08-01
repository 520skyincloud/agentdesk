"use client"

import { useEffect, useMemo, useState } from "react"
import { toast } from "sonner"

import { OptionCombobox } from "@/components/option-combobox"
import { fetchStoreOptions, type StoreOption } from "@/lib/api/store"

type StoreOptionComboboxProps = {
  value: number
  disabled?: boolean
  onChange: (storeId: number) => void
}

export function StoreOptionCombobox({
  value,
  disabled = false,
  onChange,
}: StoreOptionComboboxProps) {
  const [stores, setStores] = useState<StoreOption[] | null>(null)

  useEffect(() => {
    if (disabled) return
    let cancelled = false
    void fetchStoreOptions()
      .then((items) => {
        if (!cancelled) setStores(Array.isArray(items) ? items : [])
      })
      .catch((error) => {
        if (!cancelled) {
          setStores([])
          toast.error(error instanceof Error ? error.message : "门店列表加载失败")
        }
      })
    return () => {
      cancelled = true
    }
  }, [disabled])

  const loading = !disabled && stores === null

  const options = useMemo(
    () =>
      (stores ?? []).map((store) => ({
        value: String(store.id),
        label: store.storeCode ? `${store.name} · ${store.storeCode}` : store.name,
      })),
    [stores]
  )

  return (
    <OptionCombobox
      value={value > 0 ? String(value) : ""}
      options={options}
      placeholder={loading ? "正在加载门店" : "选择门店"}
      searchPlaceholder="搜索门店名称或编号"
      emptyText="暂无可用门店"
      disabled={disabled || loading}
      onChange={(nextValue) => onChange(Number(nextValue) || 0)}
    />
  )
}

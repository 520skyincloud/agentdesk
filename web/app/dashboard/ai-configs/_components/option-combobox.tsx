"use client"

import { CheckIcon, ChevronsUpDownIcon } from "lucide-react"
import { useState } from "react"

import { Button } from "@/components/ui/button"
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from "@/components/ui/command"
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover"
import { useI18n } from "@/i18n/provider"
import { cn } from "@/lib/utils"

export type ComboboxOption = {
  value: string
  label: string
}

type OptionComboboxProps = {
  value: string
  options: ComboboxOption[]
  placeholder: string
  searchPlaceholder?: string
  emptyText?: string
  disabled?: boolean
  onChange: (value: string) => void
}

export function OptionCombobox({
  value,
  options,
  placeholder,
  searchPlaceholder,
  emptyText,
  disabled = false,
  onChange,
}: OptionComboboxProps) {
  const t = useI18n()
  const [open, setOpen] = useState(false)
  const selectedLabel =
    options.find((option) => option.value === value)?.label ?? placeholder

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger
        render={
          <Button
            variant="outline"
            role="combobox"
            className="w-full justify-between font-normal"
            disabled={disabled}
          />
        }
      >
        <span className="truncate">{selectedLabel}</span>
        <ChevronsUpDownIcon className="ml-2 size-4 shrink-0 opacity-50" />
      </PopoverTrigger>
      <PopoverContent className="w-(--radix-popover-trigger-width) p-0" align="start">
        <Command>
          <CommandInput placeholder={searchPlaceholder ?? t("common.searchKeyword")} />
          <CommandList>
            <CommandEmpty>{emptyText ?? t("common.emptyOptions")}</CommandEmpty>
            <CommandGroup>
              {options.map((option) => (
                <CommandItem
                  key={option.value}
                  value={`${option.label} ${option.value}`}
                  onSelect={() => {
                    onChange(option.value)
                    setOpen(false)
                  }}
                >
                  <CheckIcon
                    className={cn(
                      "mr-2 size-4",
                      option.value === value ? "opacity-100" : "opacity-0"
                    )}
                  />
                  {option.label}
                </CommandItem>
              ))}
            </CommandGroup>
          </CommandList>
        </Command>
      </PopoverContent>
    </Popover>
  )
}

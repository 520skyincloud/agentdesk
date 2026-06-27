"use client"

import { LanguagesIcon } from "lucide-react"

import { useAppLocale, useI18n } from "@/i18n/provider"
import { SUPPORTED_LOCALES, type AppLocale } from "@/i18n/config"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"

export function LocaleSwitcher() {
  const t = useI18n()
  const { locale, setLocale } = useAppLocale()

  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        render={<Button variant="outline" size="sm" />}
        aria-label={t("common.language")}
      >
        <LanguagesIcon />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-40 min-w-40">
        <DropdownMenuRadioGroup
          value={locale}
          onValueChange={(value) => setLocale(value as AppLocale)}
        >
          {SUPPORTED_LOCALES.map((option) => (
            <DropdownMenuRadioItem key={option} value={option}>
              {t(`locale.${option}`)}
            </DropdownMenuRadioItem>
          ))}
        </DropdownMenuRadioGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

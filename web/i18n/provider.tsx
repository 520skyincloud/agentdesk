"use client"

import {
  createContext,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react"

import {
  DEFAULT_LOCALE,
  type AppLocale,
  readStoredLocale,
  writeStoredLocale,
} from "@/i18n/config"
import { translateMessage } from "@/i18n/messages"

type LocaleContextValue = {
  locale: AppLocale
  setLocale: (locale: AppLocale) => void
  t: (key: string, values?: Record<string, string | number>) => string
}

const LocaleContext = createContext<LocaleContextValue>({
  locale: DEFAULT_LOCALE,
  setLocale: () => {},
  t: (key) => key,
})

export function AppI18nProvider({ children }: { children: ReactNode }) {
  const [locale, setLocaleState] = useState<AppLocale>(DEFAULT_LOCALE)

  useEffect(() => {
    const storedLocale = readStoredLocale()
    setLocaleState(storedLocale)
    document.documentElement.lang = storedLocale
    document.title = translateMessage(storedLocale, "app.metadataTitle")
  }, [])

  useEffect(() => {
    document.title = translateMessage(locale, "app.metadataTitle")
  }, [locale])

  const value = useMemo<LocaleContextValue>(
    () => ({
      locale,
      t: (key, values) => translateMessage(locale, key, values),
      setLocale: (nextLocale) => {
        setLocaleState(nextLocale)
        writeStoredLocale(nextLocale)
        document.documentElement.lang = nextLocale
        document.title = translateMessage(nextLocale, "app.metadataTitle")
      },
    }),
    [locale]
  )

  return (
    <LocaleContext.Provider value={value}>
      {children}
    </LocaleContext.Provider>
  )
}

export function useAppLocale() {
  return useContext(LocaleContext)
}

export function useI18n() {
  return useContext(LocaleContext).t
}

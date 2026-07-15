"use client"

import {
  createContext,
  useContext,
  useEffect,
  useMemo,
  useSyncExternalStore,
  type ReactNode,
} from "react"

import {
  DEFAULT_LOCALE,
  LOCALE_STORAGE_KEY,
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

const LOCALE_CHANGE_EVENT = "agentdesk:locale-change"

function subscribeLocale(onStoreChange: () => void) {
  const handleStorage = (event: StorageEvent) => {
    if (event.key === LOCALE_STORAGE_KEY) {
      onStoreChange()
    }
  }
  window.addEventListener("storage", handleStorage)
  window.addEventListener(LOCALE_CHANGE_EVENT, onStoreChange)
  return () => {
    window.removeEventListener("storage", handleStorage)
    window.removeEventListener(LOCALE_CHANGE_EVENT, onStoreChange)
  }
}

export function AppI18nProvider({ children }: { children: ReactNode }) {
  const locale = useSyncExternalStore(
    subscribeLocale,
    readStoredLocale,
    () => DEFAULT_LOCALE
  )

  useEffect(() => {
    document.documentElement.lang = locale
    document.title = translateMessage(locale, "app.metadataTitle")
  }, [locale])

  const value = useMemo<LocaleContextValue>(
    () => ({
      locale,
      t: (key, values) => translateMessage(locale, key, values),
      setLocale: (nextLocale) => {
        writeStoredLocale(nextLocale)
        window.dispatchEvent(new Event(LOCALE_CHANGE_EVENT))
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

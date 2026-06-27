export const SUPPORTED_LOCALES = ["zh-CN", "en-US"] as const
export type AppLocale = (typeof SUPPORTED_LOCALES)[number]

export const DEFAULT_LOCALE: AppLocale = "zh-CN"
export const LOCALE_STORAGE_KEY = "cs_ai_agent_locale"

const LOCALE_ALIASES: Record<string, AppLocale> = {
  zh: "zh-CN",
  "zh-cn": "zh-CN",
  zh_cn: "zh-CN",
  "zh-hans": "zh-CN",
  en: "en-US",
  "en-us": "en-US",
  en_us: "en-US",
}

export function normalizeLocale(value: string | null | undefined): AppLocale {
  return normalizeSupportedLocale(value) ?? DEFAULT_LOCALE
}

function normalizeSupportedLocale(
  value: string | null | undefined
): AppLocale | null {
  if (!value) {
    return null
  }
  const key = value.trim().toLowerCase()
  return LOCALE_ALIASES[key] ?? null
}

export function isSupportedLocale(
  value: string | null | undefined
): value is AppLocale {
  if (!value) {
    return false
  }
  return SUPPORTED_LOCALES.includes(value as AppLocale)
}

export function resolveBrowserLocale({
  storedLocale,
  navigatorLanguages,
}: {
  storedLocale?: string | null
  navigatorLanguages?: readonly string[] | null
}): AppLocale {
  if (isSupportedLocale(storedLocale)) {
    return storedLocale
  }

  for (const locale of navigatorLanguages ?? []) {
    const normalized = normalizeSupportedLocale(locale)
    if (normalized) {
      return normalized
    }
  }

  return DEFAULT_LOCALE
}

export function readStoredLocale(): AppLocale {
  if (typeof window === "undefined") {
    return DEFAULT_LOCALE
  }
  return resolveBrowserLocale({
    storedLocale: window.localStorage.getItem(LOCALE_STORAGE_KEY),
    navigatorLanguages: window.navigator.languages,
  })
}

export function writeStoredLocale(locale: AppLocale) {
  if (typeof window === "undefined") {
    return
  }
  window.localStorage.setItem(LOCALE_STORAGE_KEY, locale)
}

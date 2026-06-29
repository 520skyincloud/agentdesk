import { clsx, type ClassValue } from "clsx"
import { twMerge } from "tailwind-merge"

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}

export function generateUUID() {
  if (typeof globalThis.crypto?.randomUUID === "function") {
    return globalThis.crypto.randomUUID()
  }

  const bytes = new Uint8Array(16)
  if (typeof globalThis.crypto?.getRandomValues === "function") {
    globalThis.crypto.getRandomValues(bytes)
  } else {
    for (let i = 0; i < bytes.length; i += 1) {
      bytes[i] = Math.floor(Math.random() * 256)
    }
  }

  bytes[6] = (bytes[6] & 0x0f) | 0x40
  bytes[8] = (bytes[8] & 0x3f) | 0x80

  const hex = Array.from(bytes, (byte) => byte.toString(16).padStart(2, "0"))
  return [
    hex.slice(0, 4).join(""),
    hex.slice(4, 6).join(""),
    hex.slice(6, 8).join(""),
    hex.slice(8, 10).join(""),
    hex.slice(10, 16).join(""),
  ].join("-")
}

function pad(value: number) {
  return value.toString().padStart(2, "0")
}

export function formatDateTime(value?: string | number | Date | null) {
  if (!value) {
    return "-"
  }

  const date = value instanceof Date ? value : new Date(value)
  if (Number.isNaN(date.getTime())) {
    return "-"
  }

  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`
}

export function repairMojibakeText(value?: string | null) {
  const text = String(value ?? "")
  if (!looksLikeUtf8Mojibake(text)) {
    return text
  }
  const repaired = decodeWindows1252AsUtf8(text)
  if (!repaired || repaired === text) {
    return text
  }
  return scoreReadableCJK(repaired) > scoreReadableCJK(text) ? repaired : text
}

function looksLikeUtf8Mojibake(text: string) {
  if (!text) {
    return false
  }
  return /[\u0080-\u009fÃÂÄÅÆÇÈÉÊËÌÍÎÏÐÑÒÓÔÕÖ×ØÙÚÛÜÝÞßàáâãäåæçèéêëìíîïðñòóôõöøùúûüýþÿ¼½¾¥œ€]/.test(text)
}

const WINDOWS_1252_REVERSE: Record<number, number> = {
  0x20ac: 0x80,
  0x201a: 0x82,
  0x0192: 0x83,
  0x201e: 0x84,
  0x2026: 0x85,
  0x2020: 0x86,
  0x2021: 0x87,
  0x02c6: 0x88,
  0x2030: 0x89,
  0x0160: 0x8a,
  0x2039: 0x8b,
  0x0152: 0x8c,
  0x017d: 0x8e,
  0x2018: 0x91,
  0x2019: 0x92,
  0x201c: 0x93,
  0x201d: 0x94,
  0x2022: 0x95,
  0x2013: 0x96,
  0x2014: 0x97,
  0x02dc: 0x98,
  0x2122: 0x99,
  0x0161: 0x9a,
  0x203a: 0x9b,
  0x0153: 0x9c,
  0x017e: 0x9e,
  0x0178: 0x9f,
}

function decodeWindows1252AsUtf8(text: string) {
  try {
    const bytes = Uint8Array.from(Array.from(text), (char) => {
      const code = char.charCodeAt(0)
      return WINDOWS_1252_REVERSE[code] ?? (code & 0xff)
    })
    return new TextDecoder("utf-8", { fatal: false }).decode(bytes)
  } catch {
    return text
  }
}

function scoreReadableCJK(text: string) {
  const cjk = (text.match(/[\u3400-\u9fff]/g) ?? []).length
  const replacement = (text.match(/�/g) ?? []).length
  const mojibake = (text.match(/[\u0080-\u009fÃÂÄÅÆÇÈÉÊËÌÍÎÏÐÑÒÓÔÕÖ×ØÙÚÛÜÝÞßàáâãäåæçèéêëìíîïðñòóôõöøùúûüýþÿ¼½¾¥œ€]/g) ?? []).length
  return cjk * 3 - replacement * 4 - mojibake
}

export function repairMojibakeDeep<T>(value: T): T {
  if (typeof value === "string") {
    return repairMojibakeText(value) as T
  }
  if (!value || typeof value !== "object") {
    return value
  }
  if (
    value instanceof Date ||
    (typeof Blob !== "undefined" && value instanceof Blob) ||
    (typeof File !== "undefined" && value instanceof File)
  ) {
    return value
  }
  if (Array.isArray(value)) {
    return value.map((item) => repairMojibakeDeep(item)) as T
  }
  return Object.fromEntries(
    Object.entries(value as Record<string, unknown>).map(([key, item]) => [
      key,
      repairMojibakeDeep(item),
    ])
  ) as T
}

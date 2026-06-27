import MarkdownIt from "markdown-it"

import { translateCurrentMessage } from "@/i18n/messages"

export type MessageAssetPayload = {
  assetId: string
  filename?: string
  fileSize?: number
  mimeType?: string
  url?: string
  wxMedia?: Record<string, unknown>
}

type RichPayload = Record<string, unknown>

const messageMarkdown = new MarkdownIt({
  html: false,
  linkify: true,
  breaks: true,
})

function t(key: string) {
  return translateCurrentMessage(key)
}

export function parseMessageAssetPayload(payload?: string): MessageAssetPayload | null {
  if (!payload?.trim()) {
    return null
  }
  try {
    const parsed = JSON.parse(payload) as MessageAssetPayload
    if (!parsed?.assetId?.trim()) {
      return null
    }
    return parsed
  } catch {
    return null
  }
}

function parseRichPayload(payload?: string): RichPayload | null {
  if (!payload?.trim()) {
    return null
  }
  try {
    const parsed = JSON.parse(payload) as RichPayload
    return parsed && typeof parsed === "object" ? parsed : null
  } catch {
    return null
  }
}

export function renderIMMessageHTML(message: {
  messageType: string
  content: string
  payload?: string
}) {
  if (message.messageType === "html") {
    return message.content
  }

  const asset = parseMessageAssetPayload(message.payload)
  const richPayload = parseRichPayload(message.payload)
  if (message.messageType === "image") {
    const url = resolveAssetURL(asset)
    if (url) {
      return `<p><img src="${escapeHTMLAttr(url)}" alt="${escapeHTMLAttr(
        asset?.filename || "image"
      )}"></p>`
    }
    return `<p>${escapeHTML(t("supportChat.imageSummary"))}</p>`
  }

  if (message.messageType === "attachment") {
    if (asset && resolveAssetURL(asset)) {
      return renderAttachmentHTML(asset, message.content, t("supportChat.attachmentSummary"))
    }
    return `<p>${escapeHTML(message.content || t("supportChat.attachmentSummary"))}</p>`
  }

  if (message.messageType === "voice") {
    const url = resolveAssetURL(asset)
    if (url) {
      return `<div class="im-media"><audio controls preload="metadata" src="${escapeHTMLAttr(url)}"></audio><div class="im-attachment-meta">${escapeHTML(asset?.filename || "语音消息")}</div></div>`
    }
    return `<p>收到一条语音消息</p>`
  }

  if (message.messageType === "video") {
    const url = resolveAssetURL(asset)
    if (url) {
      return `<div class="im-media"><video controls preload="metadata" src="${escapeHTMLAttr(url)}"></video><div class="im-attachment-meta">${escapeHTML(asset?.filename || "视频消息")}</div></div>`
    }
    return `<p>收到一条视频消息</p>`
  }

  if (message.messageType === "gif") {
    const url = resolveAssetURL(asset)
    if (url) {
      return `<p><img src="${escapeHTMLAttr(url)}" alt="${escapeHTMLAttr(asset?.filename || "GIF")}"></p>`
    }
    return renderInfoCardHTML("GIF 动图", message.content || "收到一条动图消息")
  }

  if (message.messageType === "location") {
    return renderLocationHTML(richPayload, message.content)
  }

  if (message.messageType === "link") {
    return renderLinkHTML(richPayload, message.content)
  }

  if (message.messageType === "feed" || message.messageType === "feed_live") {
    return renderFeedHTML(richPayload, message.content)
  }

  if (message.messageType === "mini_program") {
    return renderMiniProgramHTML(richPayload, message.content)
  }

  if (message.messageType === "contact_card") {
    return renderContactCardHTML(richPayload, message.content)
  }

  if (message.messageType === "quote") {
    return renderQuoteHTML(richPayload, message.content)
  }

  if (message.messageType === "merged_forward") {
    return renderMergedForwardHTML(richPayload, message.content)
  }

  if (message.messageType === "shop_product") {
    return renderShopProductHTML(richPayload, message.content)
  }

  return renderTextMessageHTML(message.content || "")
}

export function summarizeIMMessage(message: {
  messageType: string
  content: string
  payload?: string
}) {
  if (message.messageType === "image") {
    return t("supportChat.imageSummary")
  }
  if (message.messageType === "attachment") {
    const asset = parseMessageAssetPayload(message.payload)
    return asset?.filename?.trim()
      ? `${t("supportChat.attachmentSummary")} ${asset.filename.trim()}`
      : t("supportChat.attachmentSummary")
  }
  if (message.messageType === "voice") {
    return "[语音]"
  }
  if (message.messageType === "video") {
    return "[视频]"
  }
  if (message.messageType === "gif") {
    return "[动图]"
  }
  if (message.messageType === "location") {
    const payload = parseRichPayload(message.payload)
    return `[位置] ${stringField(payload, "title") || stringField(payload, "address") || message.content || "位置消息"}`
  }
  if (message.messageType === "link") {
    const payload = parseRichPayload(message.payload)
    return `[链接] ${stringField(payload, "title") || message.content || "链接消息"}`
  }
  if (message.messageType === "feed" || message.messageType === "feed_live") {
    const payload = parseRichPayload(message.payload)
    return `[视频号] ${stringField(payload, "nickname") || stringField(payload, "desc") || message.content || "视频号消息"}`
  }
  if (message.messageType === "mini_program") {
    const payload = parseRichPayload(message.payload)
    return `[小程序] ${stringField(payload, "title") || message.content || "小程序消息"}`
  }
  if (message.messageType === "contact_card") {
    const payload = parseRichPayload(message.payload)
    return `[名片] ${stringField(payload, "nickname") || stringField(payload, "name") || message.content || "名片消息"}`
  }
  if (message.messageType === "merged_forward") {
    return `[聊天记录] ${message.content || "合并转发"}`
  }
  if (message.messageType === "html") {
    const text = extractTextFromHTML(message.content)
    if (text.trim()) {
      return text.substring(0, 100)
    }
    if (message.content.includes("<img")) {
      return t("supportChat.imageSummary")
    }
    return t("supportChat.messageSummary")
  }
  return message.content?.substring(0, 100) || t("supportChat.messageSummary")
}

function resolveAssetURL(asset: MessageAssetPayload | null) {
  if (!asset) {
    return ""
  }
  if (asset.url?.trim()) {
    return asset.url.trim()
  }
  const wxURL = stringField(asset.wxMedia, "url")
  if (wxURL) {
    return wxURL
  }
  if (asset.assetId?.trim()) {
    return `/api/asset/file/${encodeURIComponent(asset.assetId.trim())}`
  }
  return ""
}

function stringField(payload: RichPayload | null | undefined, key: string) {
  const value = payload?.[key]
  return typeof value === "string" ? value.trim() : ""
}

function numberField(payload: RichPayload | null | undefined, key: string) {
  const value = payload?.[key]
  return typeof value === "number" && Number.isFinite(value) ? value : null
}

function renderInfoCardHTML(title: string, description?: string, imageURL?: string, href?: string) {
  const safeTitle = escapeHTML(title || "消息")
  const safeDescription = description?.trim()
    ? `<div class="im-card-desc">${escapeHTML(description.trim())}</div>`
    : ""
  const image = imageURL?.trim()
    ? `<img class="im-card-thumb" src="${escapeHTMLAttr(imageURL.trim())}" alt="${safeTitle}">`
    : ""
  const body = `<div class="im-card">${image}<div class="im-card-main"><div class="im-card-title">${safeTitle}</div>${safeDescription}</div></div>`
  if (!href?.trim()) {
    return body
  }
  return `<a class="im-card-link" href="${escapeHTMLAttr(href.trim())}" target="_blank" rel="noreferrer">${body}</a>`
}

function renderLocationHTML(payload: RichPayload | null, content: string) {
  const title = stringField(payload, "title") || content || "位置"
  const address = stringField(payload, "address")
  const latitude = numberField(payload, "latitude")
  const longitude = numberField(payload, "longitude")
  const coord = latitude !== null && longitude !== null ? `${latitude}, ${longitude}` : ""
  const mapHref = latitude !== null && longitude !== null
    ? `https://uri.amap.com/marker?position=${longitude},${latitude}&name=${encodeURIComponent(title)}`
    : ""
  const meta = [address, coord].filter(Boolean).join("\n")
  return renderInfoCardHTML(`位置：${title}`, meta, "", mapHref)
}

function renderLinkHTML(payload: RichPayload | null, content: string) {
  return renderInfoCardHTML(
    stringField(payload, "title") || content || "链接消息",
    stringField(payload, "description") || stringField(payload, "desc"),
    stringField(payload, "image_url") || stringField(payload, "thumb_url") || stringField(payload, "cover_url"),
    stringField(payload, "url")
  )
}

function renderFeedHTML(payload: RichPayload | null, content: string) {
  return renderInfoCardHTML(
    stringField(payload, "nickname") || content || "视频号消息",
    stringField(payload, "desc") || stringField(payload, "description"),
    stringField(payload, "cover_url") || stringField(payload, "thumb_url") || stringField(payload, "avatar"),
    stringField(payload, "url")
  )
}

function renderMiniProgramHTML(payload: RichPayload | null, content: string) {
  return renderInfoCardHTML(
    `小程序：${stringField(payload, "title") || content || "小程序"}`,
    stringField(payload, "description") || stringField(payload, "app_name") || stringField(payload, "username"),
    stringField(payload, "thumb_url") || stringField(payload, "image_url"),
    stringField(payload, "url") || stringField(payload, "page_path")
  )
}

function renderContactCardHTML(payload: RichPayload | null, content: string) {
  return renderInfoCardHTML(
    `名片：${stringField(payload, "nickname") || stringField(payload, "name") || content || "联系人"}`,
    stringField(payload, "corp_name") || stringField(payload, "user_id") || stringField(payload, "username"),
    stringField(payload, "avatar") || stringField(payload, "avatar_url")
  )
}

function renderQuoteHTML(payload: RichPayload | null, content: string) {
  const quoted = stringField(payload, "quote_content") || stringField(payload, "refer_content") || stringField(payload, "content")
  return `<div class="im-quote"><div>${escapeHTML(content || "引用消息")}</div>${quoted ? `<blockquote>${escapeHTML(quoted)}</blockquote>` : ""}</div>`
}

function renderMergedForwardHTML(payload: RichPayload | null, content: string) {
  const list = Array.isArray(payload?.message_list) ? payload?.message_list.slice(0, 4) : []
  const items = list
    .map((item) => {
      if (!item || typeof item !== "object") return ""
      const row = item as RichPayload
      return `<li>${escapeHTML(stringField(row, "content") || stringField(row, "title") || stringField(row, "address") || "消息")}</li>`
    })
    .filter(Boolean)
    .join("")
  const more = Array.isArray(payload?.message_list) && payload.message_list.length > 4
    ? `<div class="im-card-desc">还有 ${payload.message_list.length - 4} 条</div>`
    : ""
  return `<div class="im-card"><div class="im-card-main"><div class="im-card-title">${escapeHTML(content || "聊天记录")}</div>${items ? `<ul class="im-forward-list">${items}</ul>` : ""}${more}</div></div>`
}

function renderShopProductHTML(payload: RichPayload | null, content: string) {
  return renderInfoCardHTML(
    stringField(payload, "title") || stringField(payload, "product_name") || content || "微信小店商品",
    stringField(payload, "description") || stringField(payload, "price"),
    stringField(payload, "thumb_url") || stringField(payload, "image_url"),
    stringField(payload, "url")
  )
}

export function formatFileSize(size: number) {
  if (!Number.isFinite(size) || size <= 0) {
    return ""
  }
  const units = ["B", "KB", "MB", "GB"]
  let value = size
  let index = 0
  while (value >= 1024 && index < units.length - 1) {
    value /= 1024
    index += 1
  }
  const digits = value >= 10 || index === 0 ? 0 : 1
  return `${value.toFixed(digits)} ${units[index]}`
}

function extractTextFromHTML(html: string): string {
  if (typeof document === "undefined") {
    return ""
  }
  const div = document.createElement("div")
  div.innerHTML = html
  return div.textContent || div.innerText || ""
}

function renderTextMessageHTML(content: string) {
  const value = content.trim()
  if (!value) {
    return "<p></p>"
  }
  return messageMarkdown.render(value)
}

function escapeHTML(value: string) {
  return value
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#39;")
    .replaceAll("\n", "<br>")
}

function escapeHTMLAttr(value: string) {
  return value
    .replaceAll("&", "&amp;")
    .replaceAll('"', "&quot;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
}

function getAttachmentIconSVG() {
  return `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"></path><path d="M14 2v6h6"></path><path d="M9 15h6"></path><path d="M9 11h2"></path></svg>`
}

function renderAttachmentHTML(asset: MessageAssetPayload, content: string, fallbackTitle: string) {
  const title = escapeHTML(asset.filename || content || fallbackTitle)
  const meta = formatFileSize(asset.fileSize ?? 0)
  const metaHTML = meta ? `<div class="im-attachment-meta">${escapeHTML(meta)}</div>` : ""
  return `<div class="im-attachment"><a href="${escapeHTMLAttr(
    asset.url || ""
  )}" target="_blank" rel="noreferrer" download="${escapeHTMLAttr(
    asset.filename || ""
  )}" class="im-attachment-link"><span class="im-attachment-icon" aria-hidden="true">${getAttachmentIconSVG()}</span><span class="im-attachment-content"><span class="im-attachment-title">${title}</span>${metaHTML}</span></a></div>`
}

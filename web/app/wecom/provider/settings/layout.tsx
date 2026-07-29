import type { Metadata } from "next"
import type { ReactNode } from "react"

export const metadata: Metadata = {
  title: "门店企微授权 | 知悉微宝",
  referrer: "no-referrer",
  robots: {
    index: false,
    follow: false,
  },
}

export default function WeComProviderSettingsLayout({
  children,
}: {
  children: ReactNode
}) {
  return children
}

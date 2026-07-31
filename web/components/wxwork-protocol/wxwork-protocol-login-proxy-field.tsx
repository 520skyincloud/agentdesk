"use client"

import { Input } from "@/components/ui/input"

type WxWorkProtocolLoginProxyFieldProps = {
  value: string
  configured?: boolean
  disabled?: boolean
  onChange: (value: string) => void
}

export function WxWorkProtocolLoginProxyField({
  value,
  configured = false,
  disabled = false,
  onChange,
}: WxWorkProtocolLoginProxyFieldProps) {
  return (
    <label className="block space-y-2 text-sm font-medium">
      <span>异地登录代理</span>
      <Input
        type="password"
        autoComplete="off"
        spellCheck={false}
        value={value}
        disabled={disabled}
        onChange={(event) => onChange(event.target.value)}
        placeholder="http://host:port / socks4://... / socks5://..."
      />
      <span className="block text-xs font-normal leading-5 text-muted-foreground">
        {configured
          ? "已保存代理；留空将复用，填写新地址将替换。"
          : "启动扫码设备上的聚合聊天本地代理后，粘贴其代理地址。"}
      </span>
    </label>
  )
}

"use client";

import { cn } from "@/lib/utils";
import { useI18n } from "@/i18n/provider";

export type RealtimeConnectionStatusValue =
  | "connecting"
  | "connected"
  | "disconnected";

type RealtimeConnectionStatusProps = {
  status: RealtimeConnectionStatusValue;
  compact?: boolean;
};

const statusTextKey: Record<RealtimeConnectionStatusValue, string> = {
  connecting: "realtime.connecting",
  connected: "realtime.connected",
  disconnected: "realtime.disconnected",
};

const compactStatusTextKey: Record<RealtimeConnectionStatusValue, string> = {
  connecting: "realtime.compactConnecting",
  connected: "realtime.compactConnected",
  disconnected: "realtime.compactDisconnected",
};

export function RealtimeConnectionStatus({
  status,
  compact = false,
}: RealtimeConnectionStatusProps) {
  const t = useI18n();
  const toneClass =
    status === "connected"
      ? "border-emerald-200/80 bg-emerald-50 text-emerald-700"
      : status === "connecting"
        ? "border-amber-200/80 bg-amber-50 text-amber-700"
        : "border-[#dbe7f6] bg-[#f6f9ff] text-muted-foreground";

  return (
    <div
      className={cn(
        compact
          ? "inline-flex items-center gap-1.5 rounded-full border px-2 py-0.5 text-[10px] font-medium tracking-[0.01em]"
          : "inline-flex items-center gap-2 rounded-full border px-2.5 py-1 text-[11px] font-medium tracking-[0.02em]",
        toneClass,
      )}
    >
      <span
        className={cn(
          "inline-block size-2 rounded-full",
          status === "connected"
            ? "bg-emerald-500 shadow-[0_0_0_4px_rgba(16,185,129,0.14)]"
            : status === "connecting"
              ? "bg-amber-500 shadow-[0_0_0_4px_rgba(245,158,11,0.16)]"
              : "bg-muted-foreground shadow-[0_0_0_4px_rgba(100,116,139,0.12)]",
        )}
      />
      <span>
        {t(compact ? compactStatusTextKey[status] : statusTextKey[status])}
      </span>
    </div>
  );
}

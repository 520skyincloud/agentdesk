"use client";

import { Building2Icon, CircleUserRoundIcon } from "lucide-react";

import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import { Badge } from "@/components/ui/badge";
import type { StoreWorkbenchData } from "@/lib/api/store-workbench";
import { repairMojibakeText } from "@/lib/utils";

type StoreStaffConversationIdentityProps = {
  data: StoreWorkbenchData | null;
  loading: boolean;
  customerCount: number;
  manualAttentionCount: number;
  variant?: "sidebar" | "compact";
};

function healthLabel(status?: string) {
  switch (status) {
    case "online":
      return "在线";
    case "offline":
      return "离线";
    default:
      return "待连接";
  }
}

export function StoreStaffConversationIdentity({
  data,
  loading,
  customerCount,
  manualAttentionCount,
  variant = "sidebar",
}: StoreStaffConversationIdentityProps) {
  const employeeName =
    repairMojibakeText(data?.wxWorkEmployeeName) || repairMojibakeText(data?.nickname) || data?.username || "门店员工";
  const storeName = repairMojibakeText(data?.storeName) || "未绑定门店";
  const healthStatus = data?.wxWorkHealthStatus || "unknown";

  if (variant === "compact") {
    return (
      <div className="flex shrink-0 items-center gap-3 border-b border-border bg-background/95 px-4 py-3 lg:hidden">
        <Avatar className="size-9 shrink-0 rounded-md">
          <AvatarImage src={data?.wxWorkEmployeeAvatar || data?.avatar || ""} />
          <AvatarFallback className="rounded-md bg-muted text-xs font-semibold text-muted-foreground">
            {employeeName.slice(0, 1)}
          </AvatarFallback>
        </Avatar>
        <div className="min-w-0 flex-1">
          <div className="truncate text-sm font-semibold text-foreground">
            {loading ? "正在加载账号" : employeeName}
          </div>
          <div className="mt-0.5 truncate text-xs text-muted-foreground">{storeName}</div>
        </div>
        <div className="shrink-0 text-right text-[11px] text-muted-foreground">
          <div>{customerCount} 位客户</div>
          <div className={manualAttentionCount > 0 ? "text-destructive" : undefined}>
            {manualAttentionCount} 个待人工
          </div>
        </div>
      </div>
    );
  }

  return (
    <div className="hidden w-72 shrink-0 flex-col border-r border-border bg-background/95 lg:flex xl:w-80">
      <div className="border-b border-border px-4 py-4">
        <div className="flex min-w-0 items-center gap-2">
          <div className="flex size-8 shrink-0 items-center justify-center rounded-md bg-primary/10 text-primary">
            <CircleUserRoundIcon className="size-4" />
          </div>
          <div className="min-w-0">
            <div className="truncate text-sm font-semibold text-foreground">我的企微账号</div>
            <div className="mt-0.5 truncate text-[11px] text-muted-foreground">{storeName}</div>
          </div>
        </div>
        <div className="mt-3 grid grid-cols-2 gap-2 text-xs">
          <div className="rounded-md border border-border bg-muted/40 px-3 py-2">
            <div className="text-muted-foreground">客户</div>
            <div className="mt-1 text-lg font-semibold leading-none text-foreground">{customerCount}</div>
          </div>
          <div className="rounded-md border border-border bg-muted/40 px-3 py-2">
            <div className="text-muted-foreground">待人工</div>
            <div className="mt-1 text-lg font-semibold leading-none text-destructive">{manualAttentionCount}</div>
          </div>
        </div>
      </div>

      <div className="min-h-0 flex-1 overflow-y-auto p-3.5">
        {loading ? (
          <div className="rounded-md border border-dashed border-border bg-muted/40 px-3 py-6 text-center text-xs text-muted-foreground">
            正在加载账号
          </div>
        ) : data?.bound ? (
          <div className="rounded-md border border-primary/20 bg-primary/5 p-3">
            <div className="flex items-center gap-3">
              <Avatar className="size-11 shrink-0 rounded-md">
                <AvatarImage src={data.wxWorkEmployeeAvatar || data.avatar || ""} />
                <AvatarFallback className="rounded-md bg-background text-sm font-semibold text-muted-foreground">
                  {employeeName.slice(0, 1)}
                </AvatarFallback>
              </Avatar>
              <div className="min-w-0 flex-1">
                <div className="truncate text-sm font-semibold text-foreground">{employeeName}</div>
                <div className="mt-0.5 truncate text-xs text-muted-foreground">{storeName}</div>
              </div>
              <span
                className={`size-2.5 shrink-0 rounded-full ${
                  healthStatus === "online"
                    ? "bg-emerald-500"
                    : healthStatus === "offline"
                      ? "bg-[#aab4c3]"
                      : "bg-amber-500"
                }`}
                title={healthLabel(healthStatus)}
              />
            </div>
            <div className="mt-3 flex flex-wrap gap-1.5">
              <Badge variant="secondary" className="h-5 rounded-md px-1.5 text-[10px] font-normal">
                {healthLabel(healthStatus)}
              </Badge>
              <Badge
                variant={data.aiReplyEnabled ? "secondary" : "outline"}
                className="h-5 rounded-md px-1.5 text-[10px] font-normal"
              >
                {data.aiReplyEnabled ? "AI托管" : "人工接待"}
              </Badge>
            </div>
            <div className="mt-3 flex items-center gap-2 border-t border-border/70 pt-3 text-xs text-muted-foreground">
              <Building2Icon className="size-3.5 shrink-0" />
              <span className="truncate">{data.storeCode ? `${data.storeCode} · ${storeName}` : storeName}</span>
            </div>
          </div>
        ) : (
          <div className="rounded-md border border-dashed border-border bg-muted/40 px-3 py-6 text-center text-xs text-muted-foreground">
            尚未绑定门店员工号
          </div>
        )}
      </div>
    </div>
  );
}

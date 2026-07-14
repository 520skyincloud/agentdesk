"use client"

import { useCallback, useEffect, useMemo, useState } from "react"
import { useRouter } from "next/navigation"
import {
  Building2Icon,
  ChevronsUpDownIcon,
  Loader2Icon,
  Settings2Icon,
  ShieldCheckIcon,
} from "lucide-react"
import { toast } from "sonner"

import { useAuth } from "@/components/auth-provider"
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
  CommandSeparator,
} from "@/components/ui/command"
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover"
import {
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
} from "@/components/ui/sidebar"
import { useI18n } from "@/i18n/provider"
import { fetchTenants, type AdminTenant } from "@/lib/api/tenant"
import { setActiveTenantId } from "@/lib/auth"
import { Status } from "@/lib/generated/enums"

export function TenantContextSwitcher() {
  const t = useI18n()
  const router = useRouter()
  const { session, refreshProfile } = useAuth()
  const [open, setOpen] = useState(false)
  const [loading, setLoading] = useState(false)
  const [switching, setSwitching] = useState(false)
  const [tenants, setTenants] = useState<AdminTenant[]>([])

  const permissions = useMemo(
    () => new Set(session?.permissions ?? []),
    [session?.permissions]
  )
  const canSwitch = Boolean(
    session?.isPlatformAccount &&
      session.canSwitchTenant &&
      permissions.has("tenant.switch") &&
      permissions.has("tenant.view")
  )
  const activeTenantId = session?.activeTenantId ?? 0
  const activeTenantName =
    session?.activeTenantName ||
    (activeTenantId > 0 ? t("tenant.currentTenant") : t("tenant.platformManagement"))
  const contextHint = session?.isPlatformAccount
    ? activeTenantId > 0
      ? t("tenant.currentTenant")
      : t("tenant.platformContext")
    : t("tenant.companyMembership")

  const loadTenants = useCallback(async () => {
    if (!canSwitch) return
    setLoading(true)
    try {
      const result = await fetchTenants({ page: 1, limit: 200, status: Status.Ok })
      setTenants(result.results)
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("tenant.loadFailed"))
    } finally {
      setLoading(false)
    }
  }, [canSwitch, t])

  useEffect(() => {
    if (open) {
      void loadTenants()
    }
  }, [loadTenants, open])

  async function switchContext(tenantId: number, tenantName: string) {
    if (!session || switching || tenantId === activeTenantId) return
    const previousTenantId = activeTenantId
    const previousTenantName = session.activeTenantName || ""
    setSwitching(true)
    try {
      setActiveTenantId(tenantId, tenantName)
      await refreshProfile({ preserveSessionOnError: true })
      setOpen(false)
      if (tenantId > 0) {
        toast.success(t("tenant.enteredTenant", { name: tenantName }))
        router.push("/dashboard")
      } else {
        toast.success(t("tenant.enteredPlatform"))
        router.push("/dashboard/channels")
      }
      router.refresh()
    } catch (error) {
      setActiveTenantId(previousTenantId, previousTenantName)
      await refreshProfile({ preserveSessionOnError: true }).catch(() => undefined)
      toast.error(error instanceof Error ? error.message : t("tenant.switchFailed"))
    } finally {
      setSwitching(false)
    }
  }

  const contextButton = (
    <SidebarMenuButton
      size="lg"
      className="h-14 rounded-lg border border-sidebar-border bg-sidebar-accent/45 px-2.5 hover:bg-sidebar-accent"
      tooltip={`${contextHint}: ${activeTenantName}`}
      aria-label={`${contextHint}: ${activeTenantName}`}
    >
      <span className="flex size-8 shrink-0 items-center justify-center rounded-md bg-sidebar-primary text-sidebar-primary-foreground">
        {session?.isPlatformAccount && activeTenantId === 0 ? (
          <ShieldCheckIcon />
        ) : (
          <Building2Icon />
        )}
      </span>
      <span className="grid min-w-0 flex-1 text-left leading-tight">
        <span className="truncate text-[11px] text-sidebar-foreground/65">{contextHint}</span>
        <span className="truncate text-sm font-medium" title={activeTenantName}>
          {activeTenantName}
        </span>
      </span>
      {canSwitch ? (
        switching ? (
          <Loader2Icon className="size-4 shrink-0 animate-spin" />
        ) : (
          <ChevronsUpDownIcon className="size-4 shrink-0 opacity-55" />
        )
      ) : null}
    </SidebarMenuButton>
  )

  return (
    <SidebarMenu>
      <SidebarMenuItem>
        {canSwitch ? (
          <Popover open={open} onOpenChange={setOpen}>
            <PopoverTrigger render={contextButton} />
            <PopoverContent align="start" side="bottom" className="w-72 p-0">
              <Command>
                <CommandInput placeholder={t("tenant.searchCompany")} />
                <CommandList>
                  {loading ? (
                    <div className="flex items-center justify-center gap-2 px-3 py-6 text-sm text-muted-foreground">
                      <Loader2Icon className="size-4 animate-spin" />
                      {t("tenant.loading")}
                    </div>
                  ) : (
                    <>
                      <CommandEmpty>{t("tenant.emptySearch")}</CommandEmpty>
                      <CommandGroup heading={t("tenant.switchContext")}>
                        <CommandItem
                          value={t("tenant.platformManagement")}
                          data-checked={activeTenantId === 0}
                          onSelect={() => void switchContext(0, t("tenant.platformManagement"))}
                        >
                          <ShieldCheckIcon />
                          <span>{t("tenant.platformManagement")}</span>
                        </CommandItem>
                        {tenants.map((tenant) => (
                          <CommandItem
                            key={tenant.id}
                            value={`${tenant.shortName} ${tenant.legalName} ${tenant.tenantCode}`}
                            data-checked={activeTenantId === tenant.id}
                            onSelect={() => void switchContext(tenant.id, tenant.shortName)}
                          >
                            <Building2Icon />
                            <span className="min-w-0 flex-1">
                              <span className="block truncate">{tenant.shortName}</span>
                              <span className="block truncate text-xs text-muted-foreground">
                                {tenant.tenantCode}
                              </span>
                            </span>
                          </CommandItem>
                        ))}
                      </CommandGroup>
                    </>
                  )}
                  <CommandSeparator />
                  <CommandGroup>
                    <CommandItem
                      value={t("tenant.manageCompanies")}
                      onSelect={() => {
                        setOpen(false)
                        router.push("/dashboard/channels")
                      }}
                    >
                      <Settings2Icon />
                      <span>{t("tenant.manageCompanies")}</span>
                    </CommandItem>
                  </CommandGroup>
                </CommandList>
              </Command>
            </PopoverContent>
          </Popover>
        ) : (
          contextButton
        )}
      </SidebarMenuItem>
    </SidebarMenu>
  )
}

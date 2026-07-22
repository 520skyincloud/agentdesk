"use client"

import { useCallback, useEffect, useState } from "react"
import { HistoryIcon, RefreshCwIcon } from "lucide-react"
import { toast } from "sonner"

import { useI18n } from "@/i18n/provider"
import {
  fetchCustomerTagChangeLogs,
  type CustomerTagChangeLog,
} from "@/lib/api/agent"
import { formatDateTime } from "@/lib/utils"
import { ProjectDialog } from "@/components/project-dialog"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"

export function CustomerTagHistoryDialog({
  conversationId,
  triggerVariant = "ghost",
}: {
  conversationId: number
  triggerVariant?: "ghost" | "outline"
}) {
  const t = useI18n()
  const [open, setOpen] = useState(false)
  const [loading, setLoading] = useState(false)
  const [items, setItems] = useState<CustomerTagChangeLog[]>([])

  const load = useCallback(async () => {
    if (conversationId <= 0) return
    setLoading(true)
    try {
      const result = await fetchCustomerTagChangeLogs(conversationId, { page: 1, limit: 50 })
      setItems(Array.isArray(result.results) ? result.results : [])
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("conversation.customerTagHistoryLoadFailed"))
    } finally {
      setLoading(false)
    }
  }, [conversationId, t])

  useEffect(() => {
    if (open) void load()
  }, [load, open])

  return (
    <>
      <Button
        type="button"
        variant={triggerVariant}
        size={triggerVariant === "ghost" ? "sm" : "icon-sm"}
        className={triggerVariant === "ghost" ? "h-7 gap-1 px-2 text-xs" : undefined}
        onClick={() => setOpen(true)}
        title={t("conversation.customerTagHistory")}
      >
        <HistoryIcon />
        {triggerVariant === "ghost" ? t("conversation.history") : null}
      </Button>
      <ProjectDialog
        open={open}
        onOpenChange={setOpen}
        title={t("conversation.customerTagHistory")}
        size="xl"
        footer={
          <>
            <Button variant="outline" onClick={() => void load()} disabled={loading}>
              <RefreshCwIcon className={loading ? "animate-spin" : ""} />
              {t("tag.refresh")}
            </Button>
            <Button onClick={() => setOpen(false)}>{t("common.close")}</Button>
          </>
        }
      >
        <div className="max-h-[58vh] overflow-auto rounded-md border">
          <Table>
            <TableHeader className="sticky top-0 bg-background">
              <TableRow>
                <TableHead>{t("conversation.customerTagHistoryAction")}</TableHead>
                <TableHead>{t("conversation.customerTagHistoryChange")}</TableHead>
                <TableHead>{t("conversation.customerTagHistorySource")}</TableHead>
                <TableHead>{t("conversation.customerTagHistoryOperator")}</TableHead>
                <TableHead>{t("conversation.customerTagHistoryTime")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {items.map((item) => (
                <TableRow key={item.id}>
                  <TableCell><Badge variant="outline">{t(`conversation.customerTagAction.${item.action}`)}</Badge></TableCell>
                  <TableCell>
                    {item.action === "replace"
                      ? `${item.oldTagName || `#${item.oldTagId}`} -> ${item.newTagName || `#${item.newTagId}`}`
                      : item.newTagName || item.oldTagName || "-"}
                  </TableCell>
                  <TableCell>{item.source === "manual" ? t("conversation.customerTagSourceManual") : t("conversation.customerTagSourceAI")}</TableCell>
                  <TableCell>{item.operatorName || t("conversation.customerTagOperatorSystem")}</TableCell>
                  <TableCell className="whitespace-nowrap text-muted-foreground">{formatDateTime(item.createdAt)}</TableCell>
                </TableRow>
              ))}
              {!loading && items.length === 0 ? (
                <TableRow><TableCell colSpan={5} className="h-24 text-center text-muted-foreground">{t("conversation.customerTagHistoryEmpty")}</TableCell></TableRow>
              ) : null}
              {loading ? (
                <TableRow><TableCell colSpan={5} className="h-24 text-center text-muted-foreground">{t("common.loading")}</TableCell></TableRow>
              ) : null}
            </TableBody>
          </Table>
        </div>
      </ProjectDialog>
    </>
  )
}

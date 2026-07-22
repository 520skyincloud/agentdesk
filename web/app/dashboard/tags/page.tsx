"use client"

import { useCallback, useEffect, useMemo, useState } from "react"
import {
  ChevronRightIcon,
  PencilIcon,
  RefreshCwIcon,
  SearchIcon,
  TagIcon,
} from "lucide-react"
import { toast } from "sonner"

import { useAuth } from "@/components/auth-provider"
import {
  DashboardPage,
  DashboardTableShell,
  DashboardTableStateRow,
  DashboardToolbar,
} from "@/components/dashboard-page"
import { OptionCombobox } from "@/components/option-combobox"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Switch } from "@/components/ui/switch"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import {
  fetchTagsAll,
  updateTag,
  updateTagStatus,
  type TagTree,
} from "@/lib/api/admin"
import { updateTagTreeStatus } from "@/lib/tag-tree"
import { cn } from "@/lib/utils"
import { useI18n } from "@/i18n/provider"
import { EditDialog } from "./_components/edit"

type TagNode = TagTree & {
  children: TagNode[]
  depth: number
}

function displayName(item: Pick<TagTree, "displayAlias" | "name">) {
  return item.displayAlias.trim() || item.name
}

function withDepth(nodes: TagTree[] | null | undefined, depth = 0): TagNode[] {
  return (Array.isArray(nodes) ? nodes : []).map((node) => ({
    ...node,
    depth,
    children: withDepth(node.children, depth + 1),
  }))
}

function collectParentIds(nodes: TagNode[]): Set<number> {
  const result = new Set<number>()
  const walk = (items: TagNode[]) => {
    items.forEach((item) => {
      if (item.children.length > 0) {
        result.add(item.id)
        walk(item.children)
      }
    })
  }
  walk(nodes)
  return result
}

function filterTree(nodes: TagNode[], keyword: string, status?: number): TagNode[] {
  const normalized = keyword.trim().toLowerCase()
  if (!normalized && status === undefined) {
    return nodes
  }

  return nodes.flatMap((node) => {
    const children = filterTree(node.children, keyword, status)
    const text = [node.name, node.displayAlias, node.semanticKey, node.applicableScene]
      .join(" ")
      .toLowerCase()
    const matchesKeyword = !normalized || text.includes(normalized)
    const matchesStatus = status === undefined || node.status === status
    return matchesKeyword && matchesStatus || children.length > 0
      ? [{ ...node, children }]
      : []
  })
}

export default function DashboardTagsPage() {
  const t = useI18n()
  const { session } = useAuth()
  const permissions = useMemo(
    () => new Set(session?.permissions ?? []),
    [session?.permissions],
  )
  const canUpdate = permissions.has("tag.update")
  const [keywordInput, setKeywordInput] = useState("")
  const [statusFilterInput, setStatusFilterInput] = useState("all")
  const [keyword, setKeyword] = useState("")
  const [statusFilter, setStatusFilter] = useState("all")
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [actionLoadingId, setActionLoadingId] = useState<number | null>(null)
  const [editingItem, setEditingItem] = useState<TagNode | null>(null)
  const [tree, setTree] = useState<TagNode[]>([])
  const [expandedIds, setExpandedIds] = useState<Set<number>>(new Set())

  const statusOptions = useMemo(
    () => [
      { value: "all", label: t("status.all") },
      { value: "0", label: t("status.ok") },
      { value: "1", label: t("status.disabled") },
    ],
    [t],
  )

  const loadData = useCallback(async () => {
    setLoading(true)
    try {
      const data = withDepth(await fetchTagsAll())
      setTree(data)
      setExpandedIds(collectParentIds(data))
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("tag.loadFailed"))
    } finally {
      setLoading(false)
    }
  }, [t])

  useEffect(() => {
    void loadData()
  }, [loadData])

  const filteredTree = useMemo(
    () => filterTree(
      tree,
      keyword,
      statusFilter === "all" ? undefined : Number(statusFilter),
    ),
    [keyword, statusFilter, tree],
  )

  const flatList = useMemo(() => {
    const result: TagNode[] = []
    const walk = (nodes: TagNode[]) => {
      nodes.forEach((node) => {
        result.push(node)
        if (expandedIds.has(node.id)) {
          walk(node.children)
        }
      })
    }
    walk(filteredTree)
    return result
  }, [expandedIds, filteredTree])

  function applyFilters() {
    setKeyword(keywordInput)
    setStatusFilter(statusFilterInput)
  }

  function toggleExpanded(id: number) {
    setExpandedIds((current) => {
      const next = new Set(current)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }

  async function handleToggleStatus(item: TagNode) {
    if (!canUpdate || item.children.length > 0 || actionLoadingId !== null) {
      return
    }
    setActionLoadingId(item.id)
    try {
      const nextStatus = item.status === 0 ? 1 : 0
      await updateTagStatus(item.id, nextStatus)
      setTree((current) => updateTagTreeStatus(current, item.id, nextStatus))
      toast.success(t(nextStatus === 0 ? "tag.enabled" : "tag.disabled", {
        name: displayName(item),
      }))
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("tag.statusUpdateFailed"))
    } finally {
      setActionLoadingId(null)
    }
  }

  return (
    <>
      <DashboardPage>
        <DashboardToolbar
          actions={
            <>
              <Button variant="outline" onClick={() => void loadData()} disabled={loading}>
                <RefreshCwIcon className={loading ? "animate-spin" : ""} />
                {t("tag.refresh")}
              </Button>
              <Button variant="outline" onClick={() => setExpandedIds(collectParentIds(tree))} disabled={loading}>
                {t("tag.expandAll")}
              </Button>
              <Button variant="outline" onClick={() => setExpandedIds(new Set())} disabled={loading}>
                {t("tag.collapseAll")}
              </Button>
            </>
          }
        >
          <div className="relative w-full sm:w-72">
            <SearchIcon className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" />
            <Input
              value={keywordInput}
              onChange={(event) => setKeywordInput(event.target.value)}
              onKeyDown={(event) => {
                if (event.key === "Enter") {
                  event.preventDefault()
                  applyFilters()
                }
              }}
              placeholder={t("tag.filterName")}
              className="pl-9"
            />
          </div>
          <div className="w-full sm:w-40">
            <OptionCombobox
              value={statusFilterInput}
              options={statusOptions}
              placeholder={t("status.all")}
              searchPlaceholder={t("tag.searchStatus")}
              emptyText={t("tag.emptyStatus")}
              disabled={loading}
              onChange={(value) => setStatusFilterInput(value ?? "all")}
            />
          </div>
          <Button variant="outline" onClick={applyFilters} disabled={loading}>
            <SearchIcon />
            {t("tag.query")}
          </Button>
        </DashboardToolbar>

        <DashboardTableShell>
          <Table>
            <TableHeader className="bg-muted/40">
              <TableRow>
                <TableHead className="min-w-[280px]">{t("tag.columnName")}</TableHead>
                <TableHead className="min-w-[180px]">{t("tag.columnBehavior")}</TableHead>
                <TableHead className="min-w-[190px]">{t("tag.columnSemanticKey")}</TableHead>
                <TableHead className="w-[150px]">{t("tag.columnStatus")}</TableHead>
                {canUpdate ? <TableHead className="w-[88px] text-right">{t("tag.columnActions")}</TableHead> : null}
              </TableRow>
            </TableHeader>
            <TableBody>
              {flatList.map((item) => {
                const hasChildren = item.children.length > 0
                const alias = item.displayAlias.trim()
                return (
                  <TableRow key={item.id}>
                    <TableCell>
                      <div className="flex items-start gap-2" style={{ paddingLeft: item.depth * 22 }}>
                        {hasChildren ? (
                          <Button
                            type="button"
                            variant="ghost"
                            size="icon-sm"
                            onClick={() => toggleExpanded(item.id)}
                            aria-label={expandedIds.has(item.id) ? t("tag.collapse") : t("tag.expand")}
                          >
                            <ChevronRightIcon className={cn("size-4 transition-transform", expandedIds.has(item.id) && "rotate-90")} />
                          </Button>
                        ) : (
                          <span className="size-8 shrink-0" />
                        )}
                        <span className="mt-0.5 flex size-7 shrink-0 items-center justify-center rounded-md border bg-muted/50 text-muted-foreground">
                          <TagIcon className="size-3.5" />
                        </span>
                        <span className="min-w-0">
                          <span className="block font-medium">{alias || item.name}</span>
                          {alias ? <span className="block text-xs text-muted-foreground">{item.name}</span> : null}
                        </span>
                      </div>
                    </TableCell>
                    <TableCell>
                      <div className="flex flex-wrap gap-1.5">
                        {item.applicableScene ? <Badge variant="outline">{item.applicableScene}</Badge> : null}
                        {item.aiEnabled ? <Badge variant="secondary">{t("tag.aiEnabled")}</Badge> : null}
                        {item.replyEnabled ? <Badge variant="secondary">{t("tag.replyEnabled")}</Badge> : null}
                        {!item.aiEnabled && !item.replyEnabled && !item.applicableScene ? "-" : null}
                      </div>
                    </TableCell>
                    <TableCell>
                      <code className="break-all text-xs text-muted-foreground">{item.semanticKey || "-"}</code>
                    </TableCell>
                    <TableCell>
                      <div className="flex items-center gap-2">
                        {!hasChildren && canUpdate ? (
                          <Switch
                            checked={item.status === 0}
                            disabled={actionLoadingId !== null}
                            onCheckedChange={() => void handleToggleStatus(item)}
                            aria-label={t("tag.toggleStatus", { name: displayName(item) })}
                          />
                        ) : null}
                        <Badge variant={item.status === 0 ? "default" : "outline"}>
                          {hasChildren ? t("tag.fixedCategory") : item.status === 0 ? t("status.ok") : t("status.disabled")}
                        </Badge>
                      </div>
                    </TableCell>
                    {canUpdate ? (
                      <TableCell className="text-right">
                        {!hasChildren ? (
                          <Button variant="outline" size="icon-sm" onClick={() => setEditingItem(item)} title={t("tag.editAlias")}>
                            <PencilIcon />
                          </Button>
                        ) : null}
                      </TableCell>
                    ) : null}
                  </TableRow>
                )
              })}
              {loading || flatList.length === 0 ? (
                <DashboardTableStateRow
                  colSpan={4 + (canUpdate ? 1 : 0)}
                  loading={loading}
                  loadingText={t("tag.loading")}
                  emptyText={t("tag.empty")}
                />
              ) : null}
            </TableBody>
          </Table>
        </DashboardTableShell>
      </DashboardPage>

      <EditDialog
        open={editingItem !== null}
        saving={saving}
        itemId={editingItem?.id ?? null}
        onOpenChange={(open) => {
          if (!open && !saving) setEditingItem(null)
        }}
        onSubmit={async (displayAlias) => {
          if (!editingItem || !canUpdate || saving) return
          setSaving(true)
          try {
            await updateTag({ id: editingItem.id, displayAlias })
            toast.success(t("tag.updated", { name: displayName(editingItem) }))
            setEditingItem(null)
            await loadData()
          } catch (error) {
            toast.error(error instanceof Error ? error.message : t("tag.saveFailed"))
          } finally {
            setSaving(false)
          }
        }}
      />
    </>
  )
}

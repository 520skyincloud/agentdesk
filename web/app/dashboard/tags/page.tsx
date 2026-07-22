"use client"

import { useCallback, useEffect, useMemo, useState } from "react"
import type { CSSProperties } from "react"
import {
  closestCenter,
  DndContext,
  type DragEndEvent,
  KeyboardSensor,
  MouseSensor,
  TouchSensor,
  useSensor,
  useSensors,
} from "@dnd-kit/core"
import {
  arrayMove,
  SortableContext,
  sortableKeyboardCoordinates,
  useSortable,
  verticalListSortingStrategy,
} from "@dnd-kit/sortable"
import { CSS } from "@dnd-kit/utilities"
import {
  ChevronRightIcon,
  GripVerticalIcon,
  MoreHorizontalIcon,
  PlusIcon,
  RefreshCwIcon,
  SearchIcon,
  TagIcon,
  Trash2Icon,
} from "lucide-react"
import { toast } from "sonner"

import {
  createTag,
  deleteTag,
  fetchTags,
  fetchTagsAll,
  updateTag,
  updateTagSort,
  updateTagStatus,
  type CreateTagPayload,
  type Tag,
  type TagTree,
} from "@/lib/api/admin"
import {
  DashboardPage,
  DashboardTableShell,
  DashboardTableStateRow,
  DashboardToolbar,
} from "@/components/dashboard-page"
import { OptionCombobox } from "@/components/option-combobox"
import { EditDialog } from "./_components/edit"
import { ConflictRules } from "./_components/conflict-rules"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { ButtonGroup } from "@/components/ui/button-group"
import { Switch } from "@/components/ui/switch"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { cn } from "@/lib/utils"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Input } from "@/components/ui/input"
import { updateTagTreeStatus } from "@/lib/tag-tree"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { useI18n } from "@/i18n/provider"
import { fetchCompanies } from "@/lib/api/company"

type TagNode = TagTree & {
  children: TagNode[]
  depth: number
}

function withDepth(nodes: TagTree[] | null | undefined, depth = 0): TagNode[] {
  const safeNodes = Array.isArray(nodes) ? nodes : []

  return safeNodes.map((node) => ({
    ...node,
    depth,
    children: withDepth(node.children, depth + 1),
  }))
}

function collectParentIds(nodes: TagNode[]): Set<number> {
  const ids = new Set<number>()
  const walk = (items: TagNode[]) => {
    items.forEach((item) => {
      if (item.children.length > 0) {
        ids.add(item.id)
        walk(item.children)
      }
    })
  }
  walk(nodes)
  return ids
}

function filterTree(nodes: TagNode[], keyword: string, status?: number): TagNode[] {
  if (!keyword && status === undefined) {
    return nodes
  }

  const result: TagNode[] = []

  function matchesFilter(node: TagNode): boolean {
    const nameMatch = !keyword || node.name.toLowerCase().includes(keyword.toLowerCase())
    const statusMatch = status === undefined || node.status === status
    return nameMatch && statusMatch
  }

  function hasMatchingDescendant(node: TagNode): boolean {
    if (matchesFilter(node)) {
      return true
    }
    return node.children.some(hasMatchingDescendant)
  }

  function filterNode(node: TagNode): TagNode | null {
    if (!hasMatchingDescendant(node)) {
      return null
    }

    const filteredChildren = node.children
      .map(filterNode)
      .filter((child): child is TagNode => child !== null)

    if (matchesFilter(node)) {
      return { ...node, children: filteredChildren }
    }

    if (filteredChildren.length > 0) {
      return { ...node, children: filteredChildren }
    }

    return null
  }

  nodes.forEach((node) => {
    const filtered = filterNode(node)
    if (filtered) {
      result.push(filtered)
    }
  })

  return result
}

type SortableRowProps = {
  item: TagNode & { hasChildren: boolean }
  disabled: boolean
  expanded: boolean
  onToggleExpand: () => void
  onEdit: (item: TagNode) => void
  onToggleStatus: (item: TagNode) => void
  onDelete: (item: TagNode) => void
  actionLoadingId: number | null
  conflictLabel: string
  companyName: string
}

function SortableRow({
  item,
  disabled,
  expanded,
  onToggleExpand,
  onEdit,
  onToggleStatus,
  onDelete,
  actionLoadingId,
  conflictLabel,
  companyName,
}: SortableRowProps) {
  const t = useI18n()
  const {
    attributes,
    listeners,
    setNodeRef,
    transform,
    transition,
    isDragging,
  } = useSortable({
    id: item.id,
    disabled,
  })

  const style: CSSProperties = {
    transform: CSS.Transform.toString(transform),
    transition,
  }

  return (
    <TableRow
      ref={setNodeRef}
      style={style}
      className={cn(
        isDragging && "relative z-10 bg-[#eef5ff] shadow-[0_12px_28px_rgba(37,99,235,0.12)]",
        !disabled && "cursor-move"
      )}
    >
      <TableCell className="w-14">
        <Button
          type="button"
          variant="ghost"
          size="icon"
          className="size-8 cursor-grab active:cursor-grabbing"
          disabled={disabled}
          aria-label={t("tag.dragSort", { name: item.name })}
          {...attributes}
          {...listeners}
        >
          <GripVerticalIcon className="size-4 text-muted-foreground" />
        </Button>
      </TableCell>
      <TableCell>
        <div
          className="flex items-center gap-2"
          style={{ paddingLeft: item.depth * 24 }}
        >
          {item.hasChildren ? (
            <button
              type="button"
              onClick={onToggleExpand}
              className="flex size-6 items-center justify-center rounded-lg transition hover:bg-[#f2f7ff] hover:text-primary"
            >
              <ChevronRightIcon
                className={`size-4 transition-transform ${
                  expanded ? "rotate-90" : ""
                }`}
              />
            </button>
          ) : (
            <span className="w-6" />
          )}
          <div className="flex size-8 items-center justify-center rounded-xl border border-[#dbe7f6] bg-[#f6f9ff] text-primary shadow-inner shadow-blue-100/30">
            <TagIcon className="size-4" />
          </div>
          <span className="font-medium">{item.name}</span>
        </div>
      </TableCell>
      <TableCell>
        {item.companyId === 0 ? "全局" : companyName || `公司 ${item.companyId}`}
      </TableCell>
      <TableCell>
        <Badge variant={item.systemDefined ? "default" : "outline"}>
          {item.systemDefined ? "系统" : "自定义"}
        </Badge>
      </TableCell>
      <TableCell>{item.parentId === 0 ? "-" : item.aiEnabled ? "开启" : "关闭"}</TableCell>
      <TableCell>{item.parentId === 0 ? "-" : item.replyEnabled ? "开启" : "关闭"}</TableCell>
      <TableCell>{item.applicableScene ? sceneLabel(item.applicableScene) : "-"}</TableCell>
      <TableCell className="max-w-52 text-sm text-muted-foreground">
        <span className="line-clamp-2">{conflictLabel || "-"}</span>
      </TableCell>
      <TableCell>
        <div className="flex items-center gap-3">
          <Switch
            checked={item.status === 0}
            disabled={actionLoadingId === item.id}
            onCheckedChange={() => void onToggleStatus(item)}
            aria-label={t("tag.toggleStatus", { name: item.name })}
          />
          <Badge variant={item.status === 0 ? "default" : "outline"}>
            {item.status === 0 ? t("status.ok") : t("status.disabled")}
          </Badge>
        </div>
      </TableCell>
      <TableCell className="text-right">
        <ButtonGroup className="ml-auto">
          <Button
            variant="outline"
            size="sm"
            onClick={() => onEdit(item)}
          >
            {t("tag.edit")}
          </Button>
          <DropdownMenu>
            <DropdownMenuTrigger
              render={<Button variant="outline" size="icon-sm" />}
              aria-label={t("tag.moreActions", { name: item.name })}
            >
              <MoreHorizontalIcon />
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end" className="w-40 min-w-40">
              {!item.systemDefined ? (
                <DropdownMenuItem
                  onClick={() => void onDelete(item)}
                  className="text-destructive focus:text-destructive"
                >
                  <Trash2Icon />
                  {actionLoadingId === item.id ? t("tag.deleting") : t("tag.delete")}
                </DropdownMenuItem>
              ) : null}
            </DropdownMenuContent>
          </DropdownMenu>
        </ButtonGroup>
      </TableCell>
    </TableRow>
  )
}

function sceneLabel(scene: string) {
  const labels: Record<string, string> = {
    room_assignment: "房间安排",
    room_selection: "房型选择",
    arrival_service: "到店入住",
    stay_service: "连住续住",
    checkout_service: "退房服务",
    invoice_service: "发票服务",
    parking_service: "停车服务",
    pet_service: "宠物服务",
    room_service: "客房服务",
    customer_profile: "客户画像",
  }
  return labels[scene] ?? scene
}

export default function DashboardTagsPage() {
  const t = useI18n()
  const [keywordInput, setKeywordInput] = useState("")
  const [statusFilterInput, setStatusFilterInput] = useState("all")
  const [keyword, setKeyword] = useState("")
  const [statusFilter, setStatusFilter] = useState("all")
  const [loading, setLoading] = useState(true)
  const [sorting, setSorting] = useState(false)
  const [saving, setSaving] = useState(false)
  const [actionLoadingId, setActionLoadingId] = useState<number | null>(null)
  const [dialogOpen, setDialogOpen] = useState(false)
  const [editingItem, setEditingItem] = useState<{ id: number; name: string } | null>(null)
  const [allTags, setAllTags] = useState<Tag[]>([])
  const [companyNames, setCompanyNames] = useState<Map<number, string>>(new Map())
  const [tree, setTree] = useState<TagNode[]>([])
  const [expandedIds, setExpandedIds] = useState<Set<number>>(new Set())

  const sensors = useSensors(
    useSensor(MouseSensor, {
      activationConstraint: { distance: 8 },
    }),
    useSensor(TouchSensor, {
      activationConstraint: { delay: 150, tolerance: 8 },
    }),
    useSensor(KeyboardSensor, {
      coordinateGetter: sortableKeyboardCoordinates,
    })
  )

  const listStatusOptions = useMemo(
    () => [
      { value: "all", label: t("status.all") },
      { value: "0", label: t("status.ok") },
      { value: "1", label: t("status.disabled") },
    ],
    [t]
  )

  const loadData = useCallback(async () => {
    setLoading(true)
    try {
      const [treeData, listData, companyData] = await Promise.all([
        fetchTagsAll(),
        fetchTags({ page: 1, limit: 10000 }),
        fetchCompanies({ page: 1, limit: 1000 }).catch(() => null),
      ])
      const nextTree = withDepth(treeData)
      setTree(nextTree)
      setAllTags(Array.isArray(listData.results) ? listData.results : [])
      if (companyData) {
        setCompanyNames(
          new Map(
            (Array.isArray(companyData.results) ? companyData.results : []).map((company) => [
              company.id,
              company.name,
            ])
          )
        )
      }
      setExpandedIds(collectParentIds(nextTree))
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("tag.loadFailed"))
    } finally {
      setLoading(false)
    }
  }, [t])

  useEffect(() => {
    void loadData()
  }, [loadData])

  function handleStatusFilterChange(value: string | null) {
    setStatusFilterInput(value ?? "all")
  }

  function applyFilters() {
    setKeyword(keywordInput)
    setStatusFilter(statusFilterInput)
  }

  function handleFilterKeyDown(event: React.KeyboardEvent<HTMLInputElement>) {
    if (event.key !== "Enter") {
      return
    }
    event.preventDefault()
    applyFilters()
  }

  function toggleExpanded(id: number) {
    setExpandedIds((prev) => {
      const next = new Set(prev)
      if (next.has(id)) {
        next.delete(id)
      } else {
        next.add(id)
      }
      return next
    })
  }

  function expandAll() {
    setExpandedIds(collectParentIds(tree))
  }

  function collapseAll() {
    setExpandedIds(new Set())
  }

  function openCreateDialog() {
    setEditingItem(null)
    setDialogOpen(true)
  }

  function openEditDialog(item: TagNode) {
    setEditingItem(item)
    setDialogOpen(true)
  }

  function handleDialogOpenChange(open: boolean) {
    if (saving) {
      return
    }
    if (!open) {
      setEditingItem(null)
    }
    setDialogOpen(open)
  }

  async function handleSubmit(payload: CreateTagPayload) {
    if (saving) {
      return
    }

    setSaving(true)
    try {
      if (editingItem) {
        await updateTag({
          id: editingItem.id,
          ...payload,
        })
        toast.success(t("tag.updated", { name: editingItem.name }))
      } else {
        await createTag(payload)
        toast.success(t("tag.created", { name: payload.name }))
      }
      setDialogOpen(false)
      setEditingItem(null)
      await loadData()
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("tag.saveFailed"))
    } finally {
      setSaving(false)
    }
  }

  async function handleToggleStatus(item: TagNode) {
    setActionLoadingId(item.id)
    try {
      const nextStatus = item.status === 0 ? 1 : 0
      await updateTagStatus(item.id, nextStatus)
      toast.success(t(nextStatus === 0 ? "tag.enabled" : "tag.disabled", { name: item.name }))
      setTree((prev) => updateTagTreeStatus(prev, item.id, nextStatus))
      setAllTags((prev) =>
        prev.map((tag) => (tag.id === item.id ? { ...tag, status: nextStatus } : tag))
      )
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("tag.statusUpdateFailed"))
    } finally {
      setActionLoadingId(null)
    }
  }

  async function handleDelete(item: TagNode) {
    setActionLoadingId(item.id)
    try {
      await deleteTag(item.id)
      toast.success(t("tag.deleted", { name: item.name }))
      await loadData()
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("tag.deleteFailed"))
    } finally {
      setActionLoadingId(null)
    }
  }

  const filteredTree = useMemo(
    () =>
      filterTree(
        tree,
        keyword.trim(),
        statusFilter === "all" ? undefined : Number(statusFilter)
      ),
    [keyword, statusFilter, tree]
  )

  const conflictLabels = useMemo(() => {
    const result = new Map<number, string>()
    allTags.forEach((tag) => {
      if (!tag.conflictGroup) {
        return
      }
      const names = allTags
        .filter((candidate) => candidate.id !== tag.id && candidate.conflictGroup === tag.conflictGroup)
        .map((candidate) => candidate.name)
      result.set(tag.id, [...new Set(names)].join(" / "))
    })
    return result
  }, [allTags])

  type FlatItem = TagNode & { hasChildren: boolean }
  const [flatList, setFlatList] = useState<FlatItem[]>([])

  useEffect(() => {
    const items: FlatItem[] = []
    function collectVisible(nodes: TagNode[]) {
      nodes.forEach((node) => {
        const hasChildren = node.children.length > 0
        items.push({ ...node, hasChildren })
        if (expandedIds.has(node.id)) {
          collectVisible(node.children)
        }
      })
    }
    collectVisible(filteredTree)
    setFlatList(items)
  }, [filteredTree, expandedIds])

  async function handleDragEnd(event: DragEndEvent) {
    const { active, over } = event
    if (!over || active.id === over.id || sorting) {
      return
    }

    const activeItem = flatList.find((item) => item.id === active.id)
    const overItem = flatList.find((item) => item.id === over.id)
    if (!activeItem || !overItem) {
      return
    }

    if (activeItem.parentId !== overItem.parentId) {
      toast.error(t("tag.sameParentOnly"))
      return
    }

    const oldIndex = flatList.findIndex((item) => item.id === active.id)
    const newIndex = flatList.findIndex((item) => item.id === over.id)
    if (oldIndex < 0 || newIndex < 0) {
      return
    }

    const previousList = [...flatList]
    const nextList = arrayMove(flatList, oldIndex, newIndex)
    setFlatList(nextList)
    setSorting(true)

    try {
      const siblings = allTags.filter((t) => t.parentId === activeItem.parentId)
      const siblingIds = siblings.map((t) => t.id)
      const movedId = active.id as number
      const targetId = over.id as number
      const movedIndex = siblingIds.indexOf(movedId)
      const targetIndex = siblingIds.indexOf(targetId)
      if (movedIndex < 0 || targetIndex < 0) {
        throw new Error(t("tag.notFound"))
      }
      const newSiblingIds = arrayMove(siblingIds, movedIndex, targetIndex)
      await updateTagSort(newSiblingIds)
      toast.success(t("tag.sortUpdated"))
      await loadData()
    } catch (error) {
      setFlatList(previousList)
      toast.error(error instanceof Error ? error.message : t("tag.sortUpdateFailed"))
    } finally {
      setSorting(false)
    }
  }

  return (
    <>
      <DashboardPage>
        <Tabs defaultValue="catalog" className="gap-4">
          <TabsList className="w-fit">
            <TabsTrigger value="catalog">标签目录</TabsTrigger>
            <TabsTrigger value="conflicts">互斥规则</TabsTrigger>
          </TabsList>
          <TabsContent value="catalog" className="space-y-4">
            <DashboardToolbar
              actions={
                <>
                  <Button variant="outline" onClick={() => void loadData()} disabled={loading}>
                    <RefreshCwIcon className={loading ? "animate-spin" : ""} />
                    {t("tag.refresh")}
                  </Button>
                  <Button variant="outline" onClick={expandAll} disabled={loading}>
                    {t("tag.expandAll")}
                  </Button>
                  <Button variant="outline" onClick={collapseAll} disabled={loading}>
                    {t("tag.collapseAll")}
                  </Button>
                  <Button onClick={openCreateDialog}>
                    <PlusIcon />
                    {t("tag.new")}
                  </Button>
                </>
              }
            >
              <div className="relative w-full sm:w-72">
                <SearchIcon className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" />
                <Input
                  value={keywordInput}
                  onChange={(event) => setKeywordInput(event.target.value)}
                  onKeyDown={handleFilterKeyDown}
                  placeholder={t("tag.filterName")}
                  className="pl-9"
                />
              </div>
              <div className="w-full sm:w-40">
                <OptionCombobox
                  value={statusFilterInput}
                  options={listStatusOptions}
                  placeholder={t("status.all")}
                  searchPlaceholder={t("tag.searchStatus")}
                  emptyText={t("tag.emptyStatus")}
                  disabled={loading}
                  onChange={handleStatusFilterChange}
                />
              </div>
              <Button variant="outline" onClick={applyFilters} disabled={loading}>
                <SearchIcon />
                {t("tag.query")}
              </Button>
            </DashboardToolbar>
            <DashboardTableShell>
              <div className="overflow-x-auto">
                <DndContext
                  sensors={sensors}
                  collisionDetection={closestCenter}
                  onDragEnd={handleDragEnd}
                >
                  <Table className="min-w-[1320px]">
                    <TableHeader className="bg-[#f6f9ff]">
                      <TableRow>
                        <TableHead className="w-14" />
                        <TableHead className="min-w-[260px]">{t("tag.columnName")}</TableHead>
                        <TableHead className="w-32">作用域</TableHead>
                        <TableHead className="w-24">类型</TableHead>
                        <TableHead className="w-24">AI 提取</TableHead>
                        <TableHead className="w-24">回复参考</TableHead>
                        <TableHead className="w-32">适用场景</TableHead>
                        <TableHead className="min-w-48">互斥成员</TableHead>
                        <TableHead>{t("tag.columnStatus")}</TableHead>
                        <TableHead className="w-[92px] text-right">
                          {t("tag.columnActions")}
                        </TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      <SortableContext
                        items={flatList.map((item) => item.id)}
                        strategy={verticalListSortingStrategy}
                      >
                        {flatList.map((item) => (
                          <SortableRow
                            key={item.id}
                            item={item}
                            disabled={loading || sorting}
                            expanded={expandedIds.has(item.id)}
                            onToggleExpand={() => toggleExpanded(item.id)}
                            onEdit={openEditDialog}
                            onToggleStatus={handleToggleStatus}
                            onDelete={handleDelete}
                            actionLoadingId={actionLoadingId}
                            conflictLabel={conflictLabels.get(item.id) ?? ""}
                            companyName={companyNames.get(item.companyId) ?? ""}
                          />
                        ))}
                      </SortableContext>
                      {loading || flatList.length === 0 ? (
                        <DashboardTableStateRow
                          colSpan={10}
                          loading={loading}
                          loadingText={t("tag.loading")}
                          emptyText={t("tag.empty")}
                        />
                      ) : null}
                    </TableBody>
                  </Table>
                </DndContext>
              </div>
            </DashboardTableShell>
          </TabsContent>
          <TabsContent value="conflicts">
            <ConflictRules tags={allTags} onChanged={loadData} />
          </TabsContent>
        </Tabs>
      </DashboardPage>
      <EditDialog
        open={dialogOpen}
        saving={saving}
        itemId={editingItem?.id ?? null}
        onOpenChange={handleDialogOpenChange}
        onSubmit={handleSubmit}
      />
    </>
  )
}

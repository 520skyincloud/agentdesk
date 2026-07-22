"use client"

import { useCallback, useEffect, useMemo, useState } from "react"
import { Link2Icon, PlusIcon, RefreshCwIcon, Trash2Icon } from "lucide-react"
import { toast } from "sonner"

import { OptionCombobox } from "@/components/option-combobox"
import { TagSelector } from "@/components/tag-selector"
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
import {
  assignTagConflictGroup,
  createTagConflictGroup,
  deleteTagConflictGroup,
  fetchTagConflictGroups,
  type Tag,
  type TagConflictGroup,
  type TagTree,
} from "@/lib/api/admin"
import { fetchCompanies, type AdminCompany } from "@/lib/api/company"

type ConflictRulesProps = {
  tags: Tag[]
  onChanged: () => Promise<void>
}

function asFlatTagTree(tags: Tag[]): TagTree[] {
  return tags.map((tag) => ({ ...tag, children: [] }))
}

export function ConflictRules({ tags, onChanged }: ConflictRulesProps) {
  const [groups, setGroups] = useState<TagConflictGroup[]>([])
  const [companies, setCompanies] = useState<AdminCompany[]>([])
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [createCompanyId, setCreateCompanyId] = useState("0")
  const [createTagIds, setCreateTagIds] = useState<number[]>([])
  const [assignTagId, setAssignTagId] = useState("")
  const [assignGroupKey, setAssignGroupKey] = useState("")

  const load = useCallback(async () => {
    setLoading(true)
    try {
      const [groupData, companyData] = await Promise.all([
        fetchTagConflictGroups(),
        fetchCompanies({ page: 1, limit: 1000, status: 0 }),
      ])
      setGroups(Array.isArray(groupData) ? groupData : [])
      setCompanies(Array.isArray(companyData.results) ? companyData.results : [])
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "加载互斥规则失败")
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  const companyOptions = useMemo(
    () => [
      { value: "0", label: "全局" },
      ...companies.map((company) => ({ value: String(company.id), label: company.name })),
    ],
    [companies]
  )
  const effectiveLeaves = useMemo(
    () => tags.filter((tag) => tag.parentId > 0 && tag.status === 0 && tag.mergedIntoTagId === 0),
    [tags]
  )
  const customCreateTags = useMemo(
    () =>
      asFlatTagTree(
        effectiveLeaves.filter(
          (tag) => !tag.systemDefined && tag.companyId === Number(createCompanyId)
        )
      ),
    [createCompanyId, effectiveLeaves]
  )
  const assignTagOptions = useMemo(
    () => effectiveLeaves.map((tag) => ({ value: String(tag.id), label: tag.name })),
    [effectiveLeaves]
  )
  const selectedAssignTag = effectiveLeaves.find((tag) => tag.id === Number(assignTagId))
  const assignGroupOptions = useMemo(() => {
    const options = [{ value: "", label: "移出互斥组" }]
    groups.forEach((group) => {
      const sameCompany = group.companyId === (selectedAssignTag?.companyId ?? -1)
      if (group.systemDefined || sameCompany) {
        options.push({
          value: group.groupKey,
          label: group.members.map((member) => member.name).join(" / "),
        })
      }
    })
    return options
  }, [groups, selectedAssignTag])

  async function createGroup() {
    if (createTagIds.length < 2) {
      toast.error("请至少选择两个自定义标签")
      return
    }
    setSaving(true)
    try {
      await createTagConflictGroup({
        companyId: Number(createCompanyId),
        tagIds: createTagIds,
      })
      setCreateTagIds([])
      await Promise.all([load(), onChanged()])
      toast.success("互斥组已创建")
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "创建互斥组失败")
    } finally {
      setSaving(false)
    }
  }

  async function assignGroup() {
    if (!assignTagId) {
      toast.error("请选择标签")
      return
    }
    setSaving(true)
    try {
      await assignTagConflictGroup({ tagId: Number(assignTagId), groupKey: assignGroupKey })
      await Promise.all([load(), onChanged()])
      toast.success(assignGroupKey ? "互斥组已更新" : "标签已移出互斥组")
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "更新互斥组失败")
    } finally {
      setSaving(false)
    }
  }

  async function removeGroup(group: TagConflictGroup) {
    setSaving(true)
    try {
      await deleteTagConflictGroup({ companyId: group.companyId, groupKey: group.groupKey })
      await Promise.all([load(), onChanged()])
      toast.success("互斥组已删除")
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "删除互斥组失败")
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="space-y-4">
      <div className="grid gap-3 border-b pb-4 lg:grid-cols-[180px_minmax(260px,1fr)_auto]">
        <OptionCombobox
          value={createCompanyId}
          options={companyOptions}
          placeholder="选择作用域"
          searchPlaceholder="搜索公司"
          emptyText="暂无公司"
          disabled={saving}
          onChange={(value) => {
            setCreateCompanyId(value)
            setCreateTagIds([])
          }}
        />
        <TagSelector
          mode="multiple"
          value={createTagIds}
          onChange={setCreateTagIds}
          tags={customCreateTags}
          placeholder="选择自定义标签"
          searchPlaceholder="搜索自定义标签"
          emptyText="当前作用域暂无自定义标签"
          disabled={saving}
          showSelectedBadges={false}
        />
        <Button onClick={() => void createGroup()} disabled={saving || createTagIds.length < 2}>
          <PlusIcon />
          新建互斥组
        </Button>
      </div>

      <div className="grid gap-3 border-b pb-4 lg:grid-cols-[minmax(220px,1fr)_minmax(260px,1fr)_auto]">
        <OptionCombobox
          value={assignTagId}
          options={assignTagOptions}
          placeholder="选择标签"
          searchPlaceholder="搜索标签"
          emptyText="暂无有效标签"
          disabled={saving}
          onChange={(value) => {
            setAssignTagId(value)
            const tag = effectiveLeaves.find((item) => item.id === Number(value))
            setAssignGroupKey(tag?.conflictGroup ?? "")
          }}
        />
        <OptionCombobox
          value={assignGroupKey}
          options={assignGroupOptions}
          placeholder="选择互斥组"
          searchPlaceholder="搜索成员"
          emptyText="暂无可加入的互斥组"
          disabled={saving || !assignTagId}
          onChange={setAssignGroupKey}
        />
        <Button variant="outline" onClick={() => void assignGroup()} disabled={saving || !assignTagId}>
          <Link2Icon />
          应用
        </Button>
      </div>

      <div className="flex justify-end">
        <Button variant="outline" size="sm" onClick={() => void load()} disabled={loading}>
          <RefreshCwIcon className={loading ? "animate-spin" : ""} />
          刷新
        </Button>
      </div>

      <div className="overflow-x-auto rounded-md border">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>成员</TableHead>
              <TableHead className="w-32">类型</TableHead>
              <TableHead className="w-36">作用域</TableHead>
              <TableHead className="w-20 text-right">操作</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {groups.map((group) => (
              <TableRow key={group.groupKey}>
                <TableCell>
                  <div className="flex flex-wrap gap-1.5">
                    {group.members.map((member) => (
                      <Badge key={member.tagId} variant="outline">{member.name}</Badge>
                    ))}
                  </div>
                </TableCell>
                <TableCell>{group.systemDefined ? "标准" : "自定义"}</TableCell>
                <TableCell>
                  {group.companyId === 0
                    ? "全局"
                    : companies.find((company) => company.id === group.companyId)?.name ?? `公司 ${group.companyId}`}
                </TableCell>
                <TableCell className="text-right">
                  {!group.systemDefined ? (
                    <Button
                      variant="ghost"
                      size="icon-sm"
                      title="删除互斥组"
                      disabled={saving}
                      onClick={() => void removeGroup(group)}
                    >
                      <Trash2Icon />
                    </Button>
                  ) : null}
                </TableCell>
              </TableRow>
            ))}
            {!loading && groups.length === 0 ? (
              <TableRow>
                <TableCell colSpan={4} className="py-10 text-center text-muted-foreground">
                  暂无互斥规则
                </TableCell>
              </TableRow>
            ) : null}
          </TableBody>
        </Table>
      </div>
    </div>
  )
}

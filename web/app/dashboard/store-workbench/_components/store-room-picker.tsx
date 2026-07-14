"use client"

import { useMemo, useState } from "react"
import { RefreshCwIcon, UsersRoundIcon } from "lucide-react"
import { toast } from "sonner"

import { OptionCombobox } from "@/components/option-combobox"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import {
  fetchStoreWorkbenchRoomMembers,
  fetchStoreWorkbenchRooms,
  type StoreWorkbenchRoom,
  type StoreWorkbenchRoomMember,
} from "@/lib/api/store-workbench"
import { repairMojibakeText } from "@/lib/utils"

type StoreRoomPickerProps = {
  instanceAvailable: boolean
  roomConversationId: string
  atList: string
  disabled: boolean
  onRoomChange: (value: string) => void
  onAtListChange: (value: string) => void
}

export function StoreRoomPicker({
  instanceAvailable,
  roomConversationId,
  atList,
  disabled,
  onRoomChange,
  onAtListChange,
}: StoreRoomPickerProps) {
  const [rooms, setRooms] = useState<StoreWorkbenchRoom[]>([])
  const [members, setMembers] = useState<StoreWorkbenchRoomMember[]>([])
  const [loadingRooms, setLoadingRooms] = useState(false)
  const [loadingMembers, setLoadingMembers] = useState(false)
  const selectedMemberIds = useMemo(
    () => atList.split(",").map((item) => item.trim()).filter(Boolean),
    [atList],
  )
  const roomOptions = useMemo(() => {
    const options = rooms.map((room) => ({
      value: room.conversationId || `R:${room.roomId}`,
      label: `${repairMojibakeText(room.name) || room.roomId}${room.memberCount > 0 ? ` · ${room.memberCount}人` : ""}`,
    }))
    if (roomConversationId && !options.some((option) => option.value === roomConversationId)) {
      options.unshift({ value: roomConversationId, label: `当前通知群 · ${roomConversationId}` })
    }
    return options
  }, [roomConversationId, rooms])

  async function loadRooms() {
    if (!instanceAvailable || loadingRooms) return
    setLoadingRooms(true)
    try {
      const list = await fetchStoreWorkbenchRooms()
      setRooms(list)
      if (list.length === 0) {
        toast.info("协议只返回当前员工号作为群主并可同步的客户群，暂未发现可选群")
      }
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "读取门店群失败")
    } finally {
      setLoadingRooms(false)
    }
  }

  async function loadMembers() {
    if (!instanceAvailable || !roomConversationId || loadingMembers) return
    setLoadingMembers(true)
    try {
      const list = await fetchStoreWorkbenchRoomMembers(roomConversationId)
      setMembers(list)
      if (list.length === 0) {
        toast.info("协议没有返回该群的可选成员")
      }
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "读取群成员失败")
    } finally {
      setLoadingMembers(false)
    }
  }

  function toggleMember(userId: string) {
    if (disabled) return
    const next = selectedMemberIds.includes(userId)
      ? selectedMemberIds.filter((item) => item !== userId)
      : [...selectedMemberIds, userId]
    onAtListChange(next.join(","))
  }

  if (!instanceAvailable) {
    return (
      <div className="rounded-lg border border-dashed px-3 py-4 text-sm text-muted-foreground">
        当前门店尚未绑定企微员工号，通知群暂不可配置。
      </div>
    )
  }

  return (
    <div className="grid gap-3">
      <div className="grid gap-2 md:grid-cols-[minmax(0,1fr)_auto_auto] md:items-end">
        <OptionCombobox
          value={roomConversationId}
          options={roomOptions}
          placeholder={rooms.length > 0 ? "选择门店通知群" : "刷新可管理客户群"}
          searchPlaceholder="搜索群名称"
          emptyText="没有匹配的客户群"
          disabled={disabled}
          triggerClassName="h-10"
          onChange={(value) => {
            onRoomChange(value)
            onAtListChange("")
            setMembers([])
          }}
        />
        <Button type="button" variant="outline" disabled={loadingRooms} onClick={() => void loadRooms()}>
          <RefreshCwIcon className={loadingRooms ? "size-4 animate-spin" : "size-4"} />
          刷新群
        </Button>
        <Button
          type="button"
          variant="outline"
          disabled={loadingMembers || !roomConversationId}
          onClick={() => void loadMembers()}
        >
          <UsersRoundIcon className={loadingMembers ? "size-4 animate-pulse" : "size-4"} />
          读取成员
        </Button>
      </div>

      <div className="rounded-lg border bg-muted/25 p-3">
        <label className="flex min-h-9 cursor-pointer items-center gap-2 border-b pb-3 text-sm font-medium">
          <Checkbox
            checked={selectedMemberIds.includes("0")}
            disabled={disabled}
            onCheckedChange={() => toggleMember("0")}
          />
          @全员
        </label>
        {members.length > 0 ? (
          <div className="mt-3 grid max-h-56 gap-1.5 overflow-y-auto pr-1 sm:grid-cols-2">
            {members.map((member) => {
              const name = repairMojibakeText(
                member.realName || member.displayName || member.roomRemark || member.name,
              )
              return (
                <label
                  key={member.userId}
                  className="flex min-w-0 cursor-pointer items-center gap-2 rounded-md border bg-background px-2.5 py-2 text-sm"
                >
                  <Checkbox
                    checked={selectedMemberIds.includes(member.userId)}
                    disabled={disabled}
                    onCheckedChange={() => toggleMember(member.userId)}
                  />
                  <span className="min-w-0">
                    <span className="block truncate font-medium">{name || member.userId}</span>
                    <span className="block truncate text-xs text-muted-foreground">{member.userId}</span>
                  </span>
                </label>
              )
            })}
          </div>
        ) : (
          <p className="mt-3 text-xs leading-5 text-muted-foreground">
            选择通知群后读取协议真实返回的成员；未返回成员时不会要求手填 ID。
          </p>
        )}
      </div>
    </div>
  )
}

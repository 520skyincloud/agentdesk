import { LockKeyholeIcon } from "lucide-react"

import { Badge } from "@/components/ui/badge"
import type { AgentCustomerTag } from "@/lib/api/agent"

export function CustomerTagBadges({ tags }: { tags?: AgentCustomerTag[] }) {
  if (!tags?.length) return null

  return (
    <div className="flex flex-wrap items-center gap-1.5">
      {tags.map((tag) => (
        <Badge
          key={tag.tagId}
          variant="outline"
          className="max-w-full gap-1 px-2 text-[12px] font-normal"
          title={tag.manualProtected ? tag.standardName : undefined}
        >
          {tag.manualProtected ? <LockKeyholeIcon className="size-3" /> : null}
          <span className="break-all">{tag.name}</span>
        </Badge>
      ))}
    </div>
  )
}

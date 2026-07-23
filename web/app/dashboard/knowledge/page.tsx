"use client"

import { Button } from "@/components/ui/button"
import { useAuth } from "@/components/auth-provider"
import { useIsMobile } from "@/hooks/use-mobile"
import type { KnowledgeBase } from "@/lib/api/admin"
import { useI18n } from "@/i18n/provider"
import { PanelLeftCloseIcon, PanelLeftOpenIcon } from "lucide-react"
import { useState } from "react"

import { FastGPTKnowledgeWorkspace } from "./_components/fastgpt-knowledge-workspace"
import { KnowledgeBaseList } from "./_components/knowledge-base-list"

export default function DashboardKnowledgePage() {
  const t = useI18n()
  const { session } = useAuth()
  const isMobile = useIsMobile()
  const [selectedKnowledgeBase, setSelectedKnowledgeBase] = useState<KnowledgeBase | null>(null)
  const [sidebarCollapsed, setSidebarCollapsed] = useState(false)
  const permissions = new Set(session?.permissions ?? [])

  function selectKnowledgeBase(knowledgeBase: KnowledgeBase | null) {
    setSelectedKnowledgeBase(knowledgeBase)
    if (knowledgeBase && isMobile) {
      setSidebarCollapsed(true)
    }
  }

  return (
    <div className="agentdesk-surface flex h-[calc(100vh-4rem)] overflow-hidden rounded-lg">
      <div className={`shrink-0 overflow-hidden transition-[width] duration-200 ${sidebarCollapsed ? "w-0" : "w-full md:w-80"}`}>
        <KnowledgeBaseList
          selectedKnowledgeBaseId={selectedKnowledgeBase?.id ?? null}
          onSelectKnowledgeBase={selectKnowledgeBase}
          canCreate={permissions.has("knowledgeBase.create")}
          canUpdate={permissions.has("knowledgeBase.update")}
        />
      </div>
      <div className="relative shrink-0 bg-[#f8fbff]">
        <Button
          variant="outline"
          size="icon"
          className={`agentdesk-soft-button absolute top-4 z-10 size-8 rounded-lg md:right-auto md:left-1/2 md:-translate-x-1/2 ${sidebarCollapsed ? "left-3" : "right-3"}`}
          onClick={() => setSidebarCollapsed((value) => !value)}
          aria-label={sidebarCollapsed ? t("knowledge.expandList") : t("knowledge.collapseList")}
        >
          {sidebarCollapsed ? <PanelLeftOpenIcon className="size-3.5" /> : <PanelLeftCloseIcon className="size-3.5" />}
        </Button>
      </div>
      <div className="min-h-0 min-w-0 flex-1 overflow-hidden bg-card">
        {selectedKnowledgeBase ? (
          <FastGPTKnowledgeWorkspace
            knowledgeBase={selectedKnowledgeBase}
            canUpdate={permissions.has("knowledgeBase.update")}
            canDelete={permissions.has("knowledgeBase.delete")}
          />
        ) : (
          <div className="flex h-full items-center justify-center text-sm text-muted-foreground">
            {t("knowledge.emptyBases")}
          </div>
        )}
      </div>
    </div>
  )
}

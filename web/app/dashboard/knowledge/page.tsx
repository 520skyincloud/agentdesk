"use client"

import { Button } from "@/components/ui/button"
import { useAuth } from "@/components/auth-provider"
import { useIsMobile } from "@/hooks/use-mobile"
import {
  Sheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import type { KnowledgeBase } from "@/lib/api/admin"
import { KnowledgeBaseType } from "@/lib/generated/enums"
import { useI18n } from "@/i18n/provider"
import {
  Bug,
  DownloadIcon,
  LayoutGridIcon,
  LayoutListIcon,
  PanelLeftCloseIcon,
  PanelLeftOpenIcon,
  PlusIcon,
  RefreshCwIcon
} from "lucide-react"
import { useState } from "react"
import { DebugPanel } from "./_components/debug-panel"
import { DocumentList, type DocumentListActionState } from "./_components/document-list"
import { FAQList, type FAQListActionState } from "./_components/faq-list"
import { KnowledgeBaseList } from "./_components/knowledge-base-list"
import { FastGPTKnowledgeWorkspace } from "./_components/fastgpt-knowledge-workspace"
import { RetrieveLogList } from "./_components/retrieve-log-list"

export default function DashboardKnowledgeDocumentsPage() {
  const t = useI18n()
  const { session } = useAuth()
  const isMobile = useIsMobile()
  const [selectedKnowledgeBase, setSelectedKnowledgeBase] = useState<KnowledgeBase | null>(null)
  const [sidebarCollapsed, setSidebarCollapsed] = useState(false)
  const [debugPanelOpen, setDebugPanelOpen] = useState(false)
  const [activeTab, setActiveTab] = useState("documents")
  const [documentActionState, setDocumentActionState] = useState<DocumentListActionState | null>(null)
  const [faqActionState, setFAQActionState] = useState<FAQListActionState | null>(null)
  const permissions = new Set(session?.permissions ?? [])
  const canCreateKnowledgeBase = permissions.has("knowledgeBase.create")
  const canUpdateKnowledgeBase = permissions.has("knowledgeBase.update")
  const canDeleteKnowledgeBase = permissions.has("knowledgeBase.delete")
  const canViewDocuments = permissions.has("knowledgeDocument.view")
  const canCreateDocuments = permissions.has("knowledgeDocument.create")
  const canUpdateDocuments = permissions.has("knowledgeDocument.update")
  const canDeleteDocuments = permissions.has("knowledgeDocument.delete")
  const canViewFAQ = permissions.has("knowledgeFAQ.view")
  const canCreateFAQ = permissions.has("knowledgeFAQ.create")
  const canUpdateFAQ = permissions.has("knowledgeFAQ.update")
  const canDeleteFAQ = permissions.has("knowledgeFAQ.delete")
  const isFAQKnowledgeBase = selectedKnowledgeBase?.knowledgeType === KnowledgeBaseType.FAQ
  const isFastGPTCloudKnowledgeBase = selectedKnowledgeBase?.knowledgeType === KnowledgeBaseType.FastGPTCloud
  const activeTabUnavailable =
    (activeTab === "retrieveLogs" && !canViewDocuments) ||
    ((activeTab === "fastgptFiles" || activeTab === "resources") && !isFastGPTCloudKnowledgeBase)
  const visibleActiveTab = activeTabUnavailable ? "documents" : activeTab
  const handleSelectKnowledgeBase = (knowledgeBase: KnowledgeBase | null) => {
    setSelectedKnowledgeBase(knowledgeBase)
    if (knowledgeBase && isMobile) {
      setSidebarCollapsed(true)
    }
  }

  return (
    <div className="agentdesk-surface flex h-[calc(100vh-4rem)] overflow-hidden rounded-2xl">
      <div
        className={`shrink-0 overflow-hidden transition-[width] duration-200 ${
          sidebarCollapsed ? "w-0" : "w-full md:w-80"
        }`}
      >
        <KnowledgeBaseList
          selectedKnowledgeBaseId={selectedKnowledgeBase?.id ?? null}
          onSelectKnowledgeBase={handleSelectKnowledgeBase}
          canCreate={canCreateKnowledgeBase}
          canUpdate={canUpdateKnowledgeBase}
          canDelete={canDeleteKnowledgeBase}
        />
      </div>
      <div className="relative shrink-0 bg-[#f8fbff]">
        <Button
          variant="outline"
          size="icon"
          className={`agentdesk-soft-button absolute top-4 z-10 size-8 rounded-full md:right-auto md:left-1/2 md:-translate-x-1/2 ${
            sidebarCollapsed ? "left-3" : "right-3"
          }`}
          onClick={() => setSidebarCollapsed((value) => !value)}
          aria-label={sidebarCollapsed ? t("knowledge.expandList") : t("knowledge.collapseList")}
        >
          {sidebarCollapsed ? (
            <PanelLeftOpenIcon className="size-3.5" />
          ) : (
            <PanelLeftCloseIcon className="size-3.5" />
          )}
        </Button>
      </div>
      <div className="min-w-0 min-h-0 flex-1 overflow-hidden bg-card">
        {isFastGPTCloudKnowledgeBase && selectedKnowledgeBase ? (
          <FastGPTKnowledgeWorkspace
            knowledgeBase={selectedKnowledgeBase}
            canUpdate={canUpdateKnowledgeBase}
            canDelete={canDeleteKnowledgeBase}
          />
        ) : (
          <Tabs value={visibleActiveTab} onValueChange={setActiveTab} className="h-full min-h-0 gap-0">
          <div className="border-b border-[#dbe7f6] bg-[#f8fbff] px-6 py-4">
            <div className="flex items-center gap-2">
              <TabsList className="rounded-xl border border-[#dbe7f6] bg-[#f6f9ff] p-1 shadow-inner shadow-blue-100/40">
                <TabsTrigger value="documents">
                  {isFAQKnowledgeBase ? t("knowledge.faq") : t("knowledge.document")}
                </TabsTrigger>
                {canViewDocuments ? <TabsTrigger value="retrieveLogs">{t("knowledge.retrieveLogs")}</TabsTrigger> : null}
              </TabsList>
              {visibleActiveTab === "documents" && !isFAQKnowledgeBase && !isFastGPTCloudKnowledgeBase && canViewDocuments && documentActionState ? (
                <div className="ml-auto flex items-center gap-1">
                  <Button
                    variant="ghost"
                    size="icon"
                    className="agentdesk-soft-button size-8 rounded-lg"
                    onClick={documentActionState.onRefresh}
                    disabled={documentActionState.loading}
                    aria-label={t("knowledge.refreshDocuments")}
                  >
                    <RefreshCwIcon className={documentActionState.loading ? "size-4 animate-spin" : "size-4"} />
                  </Button>
                  <Button
                    variant={documentActionState.viewMode === "list" ? "secondary" : "ghost"}
                    size="icon"
                    className="agentdesk-soft-button size-8 rounded-lg"
                    onClick={() => documentActionState.onChangeViewMode("list")}
                    aria-label={t("knowledge.listLayout")}
                  >
                    <LayoutListIcon className="size-4" />
                  </Button>
                  <Button
                    variant={documentActionState.viewMode === "grid" ? "secondary" : "ghost"}
                    size="icon"
                    className="agentdesk-soft-button size-8 rounded-lg"
                    onClick={() => documentActionState.onChangeViewMode("grid")}
                    aria-label={t("knowledge.gridLayout")}
                  >
                    <LayoutGridIcon className="size-4" />
                  </Button>
                  <Button
                    variant="ghost"
                    size="icon"
                    className="agentdesk-soft-button size-8 rounded-lg"
                    onClick={() => setDebugPanelOpen(true)}
                    aria-label={t("knowledge.openDebugPanel")}
                  >
                    <Bug className="size-4" />
                  </Button>
                  {canCreateDocuments ? (
                    <Button
                      variant="ghost"
                      size="icon"
                      className="agentdesk-soft-button size-8 rounded-lg"
                      onClick={documentActionState.onCreate}
                      aria-label={t("knowledge.newDocument")}
                    >
                      <PlusIcon className="size-4" />
                    </Button>
                  ) : null}
                </div>
              ) : null}
              {visibleActiveTab === "documents" && isFAQKnowledgeBase && canViewFAQ && faqActionState ? (
                <div className="ml-auto flex items-center gap-1">
                  <Button
                    variant="ghost"
                    size="icon"
                    className="agentdesk-soft-button size-8 rounded-lg"
                    onClick={faqActionState.onRefresh}
                    disabled={faqActionState.loading}
                    aria-label={t("knowledge.refreshFAQ")}
                  >
                    <RefreshCwIcon className={faqActionState.loading ? "size-4 animate-spin" : "size-4"} />
                  </Button>
                  {canCreateFAQ ? (
                    <Button
                      variant="ghost"
                      size="icon"
                      className="agentdesk-soft-button size-8 rounded-lg"
                      onClick={faqActionState.onImport}
                      disabled={faqActionState.importing}
                      aria-label={t("knowledge.importFAQ")}
                    >
                      <DownloadIcon className="size-4" />
                    </Button>
                  ) : null}
                  {canViewDocuments ? (
                    <Button
                      variant="ghost"
                      size="icon"
                      className="agentdesk-soft-button size-8 rounded-lg"
                      onClick={() => setDebugPanelOpen(true)}
                      aria-label={t("knowledge.openDebugPanel")}
                    >
                      <Bug className="size-4" />
                    </Button>
                  ) : null}
                  {canCreateFAQ ? (
                    <Button
                      variant="ghost"
                      size="icon"
                      className="agentdesk-soft-button size-8 rounded-lg"
                      onClick={faqActionState.onCreate}
                      aria-label={t("knowledge.newFAQ")}
                    >
                      <PlusIcon className="size-4" />
                    </Button>
                  ) : null}
                </div>
              ) : null}
            </div>
          </div>
          <TabsContent value="documents" className="min-h-0 flex-1">
            {isFAQKnowledgeBase && canViewFAQ ? (
              <FAQList
                knowledgeBaseId={selectedKnowledgeBase?.id ?? null}
                onActionStateChange={setFAQActionState}
                canCreate={canCreateFAQ}
                canUpdate={canUpdateFAQ}
                canDelete={canDeleteFAQ}
              />
            ) : isFAQKnowledgeBase ? (
              <KnowledgeContentDenied />
            ) : canViewDocuments ? (
              <DocumentList
                knowledgeBaseId={selectedKnowledgeBase?.id ?? null}
                onActionStateChange={setDocumentActionState}
                canCreate={canCreateDocuments}
                canUpdate={canUpdateDocuments}
                canDelete={canDeleteDocuments}
              />
            ) : (
              <KnowledgeContentDenied />
            )}
          </TabsContent>
          {canViewDocuments ? (
            <TabsContent value="retrieveLogs" className="min-h-0 flex-1">
              <RetrieveLogList
                knowledgeBaseId={selectedKnowledgeBase?.id ?? null}
              />
            </TabsContent>
          ) : null}
          </Tabs>
        )}
      </div>
      <Sheet open={debugPanelOpen && canViewDocuments} onOpenChange={setDebugPanelOpen}>
        <SheetContent side="right" className="w-[calc(100vw-1rem)] sm:w-auto sm:min-w-170">
          <SheetHeader>
            <SheetTitle>{t("knowledge.ragDebug")}</SheetTitle>
          </SheetHeader>
          {canViewDocuments ? <DebugPanel knowledgeBaseId={selectedKnowledgeBase?.id ?? null} /> : null}
        </SheetContent>
      </Sheet>
    </div>
  )
}

function KnowledgeContentDenied() {
  const t = useI18n()

  return (
    <div className="flex h-full items-center justify-center text-sm text-muted-foreground">
      {t("knowledge.contentViewDenied")}
    </div>
  )
}

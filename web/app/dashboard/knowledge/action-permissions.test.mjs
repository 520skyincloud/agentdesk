import assert from "node:assert/strict"
import { readFile } from "node:fs/promises"
import test from "node:test"

const [pageSource, baseListSource, documentListSource, faqListSource, zhSource, enSource] = await Promise.all([
  readFile(new URL("./page.tsx", import.meta.url), "utf8"),
  readFile(new URL("./_components/knowledge-base-list.tsx", import.meta.url), "utf8"),
  readFile(new URL("./_components/document-list.tsx", import.meta.url), "utf8"),
  readFile(new URL("./_components/faq-list.tsx", import.meta.url), "utf8"),
  readFile(new URL("../../../messages/zh-CN.json", import.meta.url), "utf8"),
  readFile(new URL("../../../messages/en-US.json", import.meta.url), "utf8"),
])

test("knowledge page separates base, document and FAQ permissions", () => {
  for (const code of [
    "knowledgeBase.create",
    "knowledgeBase.update",
    "knowledgeBase.delete",
    "knowledgeDocument.view",
    "knowledgeDocument.create",
    "knowledgeDocument.update",
    "knowledgeDocument.delete",
    "knowledgeFAQ.view",
    "knowledgeFAQ.create",
    "knowledgeFAQ.update",
    "knowledgeFAQ.delete",
  ]) {
    assert.match(pageSource, new RegExp(`permissions\\.has\\("${code.replace(".", "\\.")}\"\\)`))
  }
  assert.match(pageSource, /canCreate=\{canCreateKnowledgeBase\}/)
  assert.match(pageSource, /canUpdate=\{canUpdateKnowledgeBase\}/)
  assert.match(pageSource, /canDelete=\{canDeleteKnowledgeBase\}/)
  assert.match(pageSource, /isFAQKnowledgeBase && canViewFAQ/)
  assert.match(pageSource, /canViewDocuments \? \(\s*<DocumentList/)
  assert.match(pageSource, /canViewDocuments \? <TabsTrigger value="retrieveLogs">/)
  assert.match(pageSource, /open=\{debugPanelOpen && canViewDocuments\}/)
  assert.equal(JSON.parse(zhSource).knowledge.contentViewDenied, "当前账号没有查看该知识内容的权限")
  assert.equal(JSON.parse(enSource).knowledge.contentViewDenied, "This account cannot view this knowledge content.")
})

test("knowledge base mutations, sorting and rebuild use base action permissions", () => {
  assert.match(baseListSource, /canCreate: boolean/)
  assert.match(baseListSource, /canUpdate: boolean/)
  assert.match(baseListSource, /canDelete: boolean/)
  assert.match(baseListSource, /if \(!canCreate\)/)
  assert.match(baseListSource, /if \(!canUpdate\)/)
  assert.match(baseListSource, /if \(!canDelete\)/)
  assert.match(baseListSource, /editingItemId \? !canUpdate : !canCreate/)
  assert.match(baseListSource, /disabled=\{loading \|\| sorting \|\| !canUpdate\}/)
  assert.match(baseListSource, /canUpdate \|\| canDelete \? <DropdownMenu>/)
})

test("knowledge document actions follow create, update and delete permissions", () => {
  assert.match(documentListSource, /const hasActions = canUpdate \|\| canDelete/)
  assert.match(documentListSource, /if \(!canCreate\)/)
  assert.match(documentListSource, /if \(!canUpdate\)/)
  assert.match(documentListSource, /if \(!canDelete\)/)
  assert.match(documentListSource, /editingItem \? !canUpdate : !canCreate/)
  assert.match(documentListSource, /\{hasActions \? <DropdownMenu>/)
  assert.match(documentListSource, /\{canUpdate \? <DropdownMenuItem/)
  assert.match(documentListSource, /\{canDelete \? <DropdownMenuItem/)
})

test("knowledge FAQ import, CRUD and rebuild use FAQ action permissions", () => {
  assert.match(faqListSource, /if \(!canCreate\)/)
  assert.match(faqListSource, /if \(!canUpdate\)/)
  assert.match(faqListSource, /if \(!canDelete\)/)
  assert.match(faqListSource, /showCreate=\{canCreate\}/)
  assert.match(faqListSource, /showEdit=\{canUpdate\}/)
  assert.match(faqListSource, /deleteItem=\{canDelete \? deleteFAQWithPermission : undefined\}/)
  assert.match(faqListSource, /showActionsColumn=\{canUpdate \|\| canDelete\}/)
  assert.match(faqListSource, /rowActions=\{canUpdate \? \[/)
  assert.match(faqListSource, /\{canCreate \? <FAQImportDialog/)
})

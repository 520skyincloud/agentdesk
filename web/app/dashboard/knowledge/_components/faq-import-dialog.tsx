"use client";

import { DownloadIcon, FileUpIcon, InfoIcon } from "lucide-react";
import { useMemo, useRef, useState } from "react";
import { toast } from "sonner";

import { ProjectDialog } from "@/components/project-dialog";
import { Button } from "@/components/ui/button";
import {
  Field,
  FieldContent,
  FieldDescription,
  FieldGroup,
  FieldLabel,
} from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { ScrollArea } from "@/components/ui/scroll-area";
import { createKnowledgeFAQ, type CreateKnowledgeFAQPayload } from "@/lib/api/admin";
import { useI18n } from "@/i18n/provider";

type FAQImportDialogProps = {
  open: boolean;
  knowledgeBaseId: number | null;
  importing: boolean;
  onOpenChange: (open: boolean) => void;
  onImportingChange: (importing: boolean) => void;
  onImported: () => Promise<void>;
};

type ParsedFAQRow = {
  rowNo: number;
  question: string;
  answer: string;
  similarQuestions: string[];
  remark: string;
};

type ParseResult = {
  rows: ParsedFAQRow[];
  warnings: string[];
};

type TFunction = (key: string, values?: Record<string, string | number>) => string;

const acceptedHeaderMap: Record<string, keyof Omit<ParsedFAQRow, "rowNo">> = {
  question: "question",
  answer: "answer",
  similarquestions: "similarQuestions",
  "similarQuestions": "similarQuestions",
  remark: "remark",
  "\u6807\u51c6\u95ee\u9898": "question",
  "\u95ee\u9898": "question",
  "\u7b54\u6848": "answer",
  "\u76f8\u4f3c\u95ee": "similarQuestions",
  "\u76f8\u4f3c\u95ee\u9898": "similarQuestions",
  "\u5907\u6ce8": "remark",
};

function normalizeHeader(value: string) {
  return value.trim().replace(/^\uFEFF/, "").toLowerCase();
}

function parseDelimitedText(input: string): string[][] {
  const text = input.replace(/\r\n/g, "\n").replace(/\r/g, "\n");
  const rows: string[][] = [];
  let row: string[] = [];
  let cell = "";
  let inQuotes = false;
  let delimiter = ",";

  function pushCell() {
    row.push(cell.trim());
    cell = "";
  }

  function pushRow() {
    if (row.length === 1 && row[0] === "" && rows.length === 0) {
      row = [];
      return;
    }
    if (row.some((item) => item !== "")) {
      rows.push(row);
    }
    row = [];
  }

  for (let i = 0; i < text.length; i += 1) {
    const char = text[i];
    const next = text[i + 1];

    if (!inQuotes && rows.length === 0 && row.length === 0 && cell.length > 0 && char === "\t") {
      delimiter = "\t";
    }

    if (char === '"') {
      if (inQuotes && next === '"') {
        cell += '"';
        i += 1;
        continue;
      }
      inQuotes = !inQuotes;
      continue;
    }

    if (!inQuotes && char === delimiter) {
      pushCell();
      continue;
    }

    if (!inQuotes && char === "\n") {
      pushCell();
      pushRow();
      continue;
    }

    cell += char;
  }

  if (cell.length > 0 || row.length > 0) {
    pushCell();
    pushRow();
  }

  return rows;
}

function parseSimilarQuestions(value: string) {
  return value
    .split(/\r?\n|\|/g)
    .map((item) => item.trim())
    .filter(Boolean);
}

function parseFAQFileContent(input: string, t: TFunction): ParseResult {
  const table = parseDelimitedText(input);
  if (table.length === 0) {
    throw new Error(t("knowledge.fileEmpty"));
  }

  const headerRow = table[0];
  const headerMap = new Map<keyof Omit<ParsedFAQRow, "rowNo">, number>();
  for (let index = 0; index < headerRow.length; index += 1) {
    const header = acceptedHeaderMap[normalizeHeader(headerRow[index])];
    if (header && !headerMap.has(header)) {
      headerMap.set(header, index);
    }
  }

  if (!headerMap.has("question") || !headerMap.has("answer")) {
    throw new Error(t("knowledge.missingFAQColumns"));
  }

  const rows: ParsedFAQRow[] = [];
  const warnings: string[] = [];

  for (let index = 1; index < table.length; index += 1) {
    const current = table[index];
    const rowNo = index + 1;
    const question = current[headerMap.get("question") ?? -1]?.trim() ?? "";
    const answer = current[headerMap.get("answer") ?? -1]?.trim() ?? "";
    const similarQuestionsRaw = current[headerMap.get("similarQuestions") ?? -1]?.trim() ?? "";
    const remark = current[headerMap.get("remark") ?? -1]?.trim() ?? "";

    if (!question && !answer && !similarQuestionsRaw && !remark) {
      continue;
    }
    if (!question || !answer) {
      warnings.push(t("knowledge.skipRowMissingFAQ", { row: rowNo }));
      continue;
    }

    rows.push({
      rowNo,
      question,
      answer,
      similarQuestions: parseSimilarQuestions(similarQuestionsRaw),
      remark,
    });
  }

  return { rows, warnings };
}

function downloadTemplate() {
  const templateContent = [
    "question,answer,similarQuestions,remark",
    '"How do I reset my password?","Open profile settings and choose Reset Password.","forgot password|where is reset password","Account FAQ"',
    '"Which channels are supported?","Web chat and WeCom customer service channels are currently supported.","available channels|supported channels","Channel guide"',
  ].join("\n");
  const blob = new Blob([templateContent], { type: "text/csv;charset=utf-8;" });
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url;
  link.download = "knowledge-faq-import-template.csv";
  link.click();
  URL.revokeObjectURL(url);
}

function buildPayload(row: ParsedFAQRow, knowledgeBaseId: number): CreateKnowledgeFAQPayload {
  return {
    knowledgeBaseId,
    question: row.question,
    answer: row.answer,
    similarQuestions: row.similarQuestions,
    remark: row.remark,
  };
}

export function FAQImportDialog({
  open,
  knowledgeBaseId,
  importing,
  onOpenChange,
  onImportingChange,
  onImported,
}: FAQImportDialogProps) {
  const t = useI18n();
  const fileInputRef = useRef<HTMLInputElement | null>(null);
  const [fileName, setFileName] = useState("");
  const [rows, setRows] = useState<ParsedFAQRow[]>([]);
  const [warnings, setWarnings] = useState<string[]>([]);

  const previewRows = useMemo(() => rows.slice(0, 5), [rows]);

  function resetState() {
    setFileName("");
    setRows([]);
    setWarnings([]);
    if (fileInputRef.current) {
      fileInputRef.current.value = "";
    }
  }

  async function handleFileChange(event: React.ChangeEvent<HTMLInputElement>) {
    const file = event.target.files?.[0];
    if (!file) {
      return;
    }

    try {
      const content = await file.text();
      const parsed = parseFAQFileContent(content, t);
      setFileName(file.name);
      setRows(parsed.rows);
      setWarnings(parsed.warnings);
      if (parsed.rows.length === 0) {
        toast.error(t("knowledge.importNoRows"));
      } else {
        toast.success(t("knowledge.parsedFAQRows", { count: parsed.rows.length }));
      }
    } catch (error) {
      resetState();
      toast.error(error instanceof Error ? error.message : t("knowledge.parseImportFailed"));
    }
  }

  async function handleImport() {
    if (!knowledgeBaseId || rows.length === 0 || importing) {
      return;
    }

    onImportingChange(true);
    let successCount = 0;
    const failedRows: string[] = [];

    try {
      for (const row of rows) {
        try {
          await createKnowledgeFAQ(buildPayload(row, knowledgeBaseId));
          successCount += 1;
        } catch (error) {
          failedRows.push(
            t("knowledge.importRowFailed", {
              row: row.rowNo,
              message: error instanceof Error ? error.message : t("knowledge.importFailed"),
            })
          );
        }
      }

      await onImported();

      if (successCount > 0) {
        toast.success(t("knowledge.importSuccess", { count: successCount }));
      }
      if (failedRows.length > 0) {
        toast.error(t("knowledge.importSomeFailed", { count: failedRows.length }));
        setWarnings((current) => [...current, ...failedRows]);
        return;
      }

      resetState();
      onOpenChange(false);
    } finally {
      onImportingChange(false);
    }
  }

  return (
    <ProjectDialog
      open={open}
      onOpenChange={(nextOpen) => {
        if (!nextOpen && !importing) {
          resetState();
        }
        onOpenChange(nextOpen);
      }}
      title={t("knowledge.importFAQTitle")}
      description={t("knowledge.importFAQDescription")}
      size="lg"
      footer={
        <>
          <Button type="button" variant="outline" onClick={() => downloadTemplate()}>
            <DownloadIcon className="size-4" />
            {t("knowledge.downloadTemplate")}
          </Button>
          <Button type="button" variant="outline" onClick={() => onOpenChange(false)} disabled={importing}>
            {t("knowledge.cancel")}
          </Button>
          <Button type="button" onClick={() => void handleImport()} disabled={importing || rows.length === 0}>
            {importing
              ? t("knowledge.importing")
              : rows.length > 0
                ? t("knowledge.startImportWithCount", { count: rows.length })
                : t("knowledge.startImport")}
          </Button>
        </>
      }
    >
      <FieldGroup>
        <Field>
          <FieldLabel htmlFor="faq-import-file">{t("knowledge.importFile")}</FieldLabel>
          <FieldContent>
            <div className="flex items-center gap-2">
              <Input
                id="faq-import-file"
                ref={fileInputRef}
                type="file"
                accept=".csv,text/csv,.txt"
                onChange={(event) => void handleFileChange(event)}
              />
              <Button
                type="button"
                variant="outline"
                onClick={() => fileInputRef.current?.click()}
              >
                <FileUpIcon className="size-4" />
                {t("knowledge.chooseFile")}
              </Button>
            </div>
            <FieldDescription>
              {t("knowledge.importFileDescription")}
            </FieldDescription>
          </FieldContent>
        </Field>

        {fileName ? (
          <div className="rounded-xl border border-[#dbe7f6] bg-[#f6f9ff] px-3 py-2 text-sm shadow-inner shadow-blue-100/30">
            {t("knowledge.currentFile", { name: fileName })}
          </div>
        ) : null}

        {warnings.length > 0 ? (
          <div className="rounded-xl border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-900 shadow-[0_10px_22px_rgba(245,158,11,0.08)]">
            <div className="mb-2 flex items-center gap-2 font-medium">
              <InfoIcon className="size-4" />
              {t("knowledge.importHint")}
            </div>
            <ul className="space-y-1">
              {warnings.map((item, index) => (
                <li key={`${item}-${index}`}>{item}</li>
              ))}
            </ul>
          </div>
        ) : null}

        <div className="overflow-hidden rounded-2xl border border-[#dbe7f6] bg-white shadow-[0_10px_24px_rgba(37,99,235,0.08)]">
          <div className="border-b px-4 py-3 text-sm font-medium">
            {t("knowledge.importPreview")}
          </div>
          {previewRows.length > 0 ? (
            <ScrollArea className="max-h-80">
              <div className="divide-y">
                {previewRows.map((row) => (
                  <div key={row.rowNo} className="space-y-2 px-4 py-3 text-sm">
                    <div>
                      <span className="text-muted-foreground">{t("knowledge.rowNumber", { row: row.rowNo })}</span>
                    </div>
                    <div>
                      <div className="font-medium">{row.question}</div>
                      <div className="mt-1 whitespace-pre-wrap text-muted-foreground">
                        {row.answer}
                      </div>
                    </div>
                    <div className="text-muted-foreground">
                      {t("knowledge.similarQuestionShort", {
                        value: row.similarQuestions.length > 0 ? row.similarQuestions.join(" / ") : t("knowledge.none"),
                      })}
                    </div>
                    <div className="text-muted-foreground">{t("knowledge.remark")}：{row.remark || t("knowledge.none")}</div>
                  </div>
                ))}
              </div>
            </ScrollArea>
          ) : (
            <div className="px-4 py-12 text-center text-sm text-muted-foreground">
              {t("knowledge.previewAfterUpload")}
            </div>
          )}
        </div>
      </FieldGroup>
    </ProjectDialog>
  );
}

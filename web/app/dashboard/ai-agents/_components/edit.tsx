"use client";

import { zodResolver } from "@hookform/resolvers/zod";
import {
  ArrowDownIcon,
  ArrowUpIcon,
  ChevronDownIcon,
  InfoIcon,
  PlusIcon,
  Trash2Icon,
} from "lucide-react";
import { useEffect, useMemo, useState, type ReactNode } from "react";
import { Controller, Resolver, useForm } from "react-hook-form";
import { toast } from "sonner";
import { z } from "zod/v4";

import { OptionCombobox } from "@/components/option-combobox";
import { ProjectDialog } from "@/components/project-dialog";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Field,
  FieldContent,
  FieldError,
  FieldLabel,
} from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import {
  Popover,
  PopoverContent,
  PopoverDescription,
  PopoverTrigger,
} from "@/components/ui/popover";
import {
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
} from "@/components/ui/tabs";
import { Textarea } from "@/components/ui/textarea";
import {
  fetchAIAgent,
  fetchAIConfigsAll,
  fetchAgentTeamsAll,
  fetchKnowledgeBasesAll,
  fetchMCPCatalog,
  fetchSkillDefinitionsAll,
  type AIAgent,
  type AIConfig,
  type AdminAgentTeam,
  type CreateAIAgentPayload,
  type KnowledgeBase,
  type MCPToolCatalogItem,
  type MCPToolSourceType,
  type SkillDefinition,
} from "@/lib/api/admin";
import { useI18n } from "@/i18n/provider";
import {
  AIAgentFallbackMode,
  AIAgentHandoffMode,
  AIModelType,
  IMConversationServiceMode,
  Status,
} from "@/lib/generated/enums";

type DirectToolItem = CreateAIAgentPayload["directTools"][number];

type DirectToolOption = {
  value: string;
  label: string;
  meta: DirectToolItem;
  sourceType: MCPToolSourceType;
  autoInjected: boolean;
  groupLabel: string;
};

type GraphToolOption = {
  value: string;
  label: string;
};

type EditDialogProps = {
  open: boolean;
  saving: boolean;
  itemId: number | null;
  onOpenChange: (open: boolean) => void;
  onSubmit: (payload: CreateAIAgentPayload) => Promise<void>;
};

type TFunction = (key: string, values?: Record<string, string | number>) => string;

type EditForm = {
  name: string;
  description: string;
  aiConfigId: string;
  serviceMode: string;
  systemPrompt: string;
  welcomeMessage: string;
  replyTimeoutSeconds: number;
  handoffMode: string;
  fallbackMode: string;
  fallbackMessage: string;
};

function getServiceModeOptions(t: TFunction) {
  return [
    { value: String(IMConversationServiceMode.AIOnly), label: t("aiAgent.serviceAiOnly") },
    { value: String(IMConversationServiceMode.HumanOnly), label: t("aiAgent.serviceHumanOnly") },
    { value: String(IMConversationServiceMode.AIFirst), label: t("aiAgent.serviceAiFirst") },
  ];
}

function getHandoffModeOptions(t: TFunction) {
  return [
    { value: String(AIAgentHandoffMode.WaitPool), label: t("aiAgent.handoffWaitPool") },
    { value: String(AIAgentHandoffMode.DefaultTeamPool), label: t("aiAgent.handoffDefaultTeamPool") },
    { value: String(AIAgentHandoffMode.AIHoldAndNotify), label: t("aiAgent.handoffAiHoldAndNotify") },
  ];
}

function getFallbackModeOptions(t: TFunction) {
  return [
    { value: String(AIAgentFallbackMode.NoAnswer), label: t("aiAgent.fallbackNoAnswer") },
    { value: String(AIAgentFallbackMode.SuggestRetry), label: t("aiAgent.fallbackSuggestRetry") },
  ];
}

function buildForm(item: AIAgent | null): EditForm {
  if (!item) {
    return {
      name: "",
      description: "",
      aiConfigId: "",
      serviceMode: String(IMConversationServiceMode.AIFirst),
      systemPrompt: "",
      welcomeMessage: "",
      replyTimeoutSeconds: 180,
      handoffMode: String(AIAgentHandoffMode.WaitPool),
      fallbackMode: String(AIAgentFallbackMode.NoAnswer),
      fallbackMessage: "",
    };
  }
  return {
    name: item.name,
    description: item.description || "",
    aiConfigId: item.aiConfigId > 0 ? String(item.aiConfigId) : "",
    serviceMode: String(item.serviceMode),
    systemPrompt: item.systemPrompt || "",
    welcomeMessage: item.welcomeMessage || "",
    replyTimeoutSeconds: item.replyTimeoutSeconds ?? 180,
    handoffMode: String(item.handoffMode),
    fallbackMode: String(item.fallbackMode),
    fallbackMessage: item.fallbackMessage || "",
  };
}

function buildPayload(
  form: EditForm,
  knowledgeIds: number[],
  teamIds: number[],
  skillIds: number[],
  directTools: CreateAIAgentPayload["directTools"],
  graphTools: CreateAIAgentPayload["graphTools"],
): CreateAIAgentPayload {
  return {
    name: form.name.trim(),
    description: form.description.trim(),
    aiConfigId: Number(form.aiConfigId),
    serviceMode: Number(form.serviceMode),
    systemPrompt: form.systemPrompt.trim(),
    welcomeMessage: form.welcomeMessage.trim(),
    replyTimeoutSeconds: Number(form.replyTimeoutSeconds),
    teamIds,
    handoffMode: Number(form.handoffMode),
    fallbackMode: Number(form.fallbackMode),
    fallbackMessage: form.fallbackMessage.trim(),
    knowledgeIds,
    skillIds,
    directTools,
    graphTools,
  };
}

export function EditDialog({
  open,
  saving,
  itemId,
  onOpenChange,
  onSubmit,
}: EditDialogProps) {
  if (!open) {
    return null;
  }
  return (
    <EditDialogBody
      key={itemId ? `edit-${itemId}` : "create"}
      open={open}
      saving={saving}
      itemId={itemId}
      onOpenChange={onOpenChange}
      onSubmit={onSubmit}
    />
  );
}

function EditDialogBody({
  open,
  saving,
  itemId,
  onOpenChange,
  onSubmit,
}: EditDialogProps) {
  const formId = "ai-agent-edit-form";
  const t = useI18n();
  const [loading, setLoading] = useState(false);
  const schema = useMemo(
    () =>
      z.object({
        name: z.string().trim().min(1, t("aiAgent.nameRequired")),
        description: z.string().trim(),
        aiConfigId: z.string().trim().regex(/^\d+$/, t("aiAgent.aiConfigRequired")),
        serviceMode: z.string().trim().min(1, t("aiAgent.serviceModeRequired")),
        systemPrompt: z.string().trim(),
        welcomeMessage: z.string().trim(),
        replyTimeoutSeconds: z
          .number()
          .min(0, t("aiAgent.replyTimeoutInvalid")),
        handoffMode: z.string().trim().min(1, t("aiAgent.handoffModeRequired")),
        fallbackMode: z.string().trim().min(1, t("aiAgent.fallbackModeRequired")),
        fallbackMessage: z.string().trim(),
      }),
    [t],
  );
  const resolver = useMemo(
    () => zodResolver(schema as never) as Resolver<EditForm>,
    [schema],
  );
  const serviceModeOptions = useMemo(() => getServiceModeOptions(t), [t]);
  const handoffModeOptions = useMemo(() => getHandoffModeOptions(t), [t]);
  const fallbackModeOptions = useMemo(() => getFallbackModeOptions(t), [t]);
  const form = useForm<EditForm>({
    resolver,
    defaultValues: buildForm(null),
  });
  const {
    control,
    handleSubmit,
    register,
    reset,
    watch,
    formState: { errors },
  } = form;
  const [selectedKnowledgeIds, setSelectedKnowledgeIds] = useState<number[]>(
    [],
  );
  const [selectedTeamIds, setSelectedTeamIds] = useState<number[]>([]);
  const [selectedSkillIds, setSelectedSkillIds] = useState<number[]>([]);
  const [knowledgeToAdd, setKnowledgeToAdd] = useState("");
  const [teamToAdd, setTeamToAdd] = useState("");
  const [skillToAdd, setSkillToAdd] = useState("");
  const [directToolGroupToAdd, setDirectToolGroupToAdd] = useState("");
  const [directToolToAdd, setDirectToolToAdd] = useState("");
  const [graphToolToAdd, setGraphToolToAdd] = useState("");
  const [aiConfigs, setAIConfigs] = useState<AIConfig[]>([]);
  const [knowledgeBases, setKnowledgeBases] = useState<KnowledgeBase[]>([]);
  const [agentTeams, setAgentTeams] = useState<AdminAgentTeam[]>([]);
  const [skills, setSkills] = useState<SkillDefinition[]>([]);
  const [directTools, setDirectTools] = useState<DirectToolItem[]>([]);
  const [graphTools, setGraphTools] = useState<string[]>([]);
  const [directToolOptions, setDirectToolOptions] = useState<DirectToolOption[]>(
    [],
  );
  const [graphToolOptions, setGraphToolOptions] = useState<GraphToolOption[]>(
    [],
  );
  const [toolCatalog, setToolCatalog] = useState<MCPToolCatalogItem[]>([]);

  useEffect(() => {
    async function loadDetail() {
      if (!itemId) {
        reset(buildForm(null));
        setSelectedKnowledgeIds([]);
        setSelectedTeamIds([]);
        setSelectedSkillIds([]);
        setDirectTools([]);
        setGraphTools([]);
        setKnowledgeToAdd("");
        setTeamToAdd("");
        setSkillToAdd("");
        setDirectToolGroupToAdd("");
        setDirectToolToAdd("");
        setGraphToolToAdd("");
        return;
      }
      setLoading(true);
      try {
        const data = await fetchAIAgent(itemId);
        reset(buildForm(data));
        setSelectedKnowledgeIds(data.knowledgeIds ?? []);
        setSelectedTeamIds((data.teams ?? []).map((team) => team.id));
        setSelectedSkillIds(data.skillIds ?? []);
        setDirectTools(data.directTools ?? []);
        setGraphTools(data.graphTools ?? []);
        setKnowledgeToAdd("");
        setTeamToAdd("");
        setSkillToAdd("");
        setDirectToolGroupToAdd("");
        setDirectToolToAdd("");
        setGraphToolToAdd("");
      } catch (error) {
        toast.error(
          error instanceof Error ? error.message : t("aiAgent.loadDetailFailed"),
        );
      } finally {
        setLoading(false);
      }
    }
    void loadDetail();
  }, [itemId, reset, t]);

  useEffect(() => {
    async function loadAIConfigs() {
      try {
        const data = await fetchAIConfigsAll({
          modelType: AIModelType.LLM,
        });
        setAIConfigs(data);
      } catch (error) {
        toast.error(
          error instanceof Error ? error.message : t("aiAgent.loadAiConfigFailed"),
        );
      }
    }
    void loadAIConfigs();
  }, [t]);

  useEffect(() => {
    async function loadAgentTeams() {
      try {
        const data = await fetchAgentTeamsAll();
        setAgentTeams(data);
      } catch (error) {
        toast.error(error instanceof Error ? error.message : t("aiAgent.loadTeamsFailed"));
      }
    }
    void loadAgentTeams();
  }, [t]);

  useEffect(() => {
    async function loadKnowledgeBases() {
      try {
        const data = await fetchKnowledgeBasesAll({
          status: Status.Ok,
        });
        setKnowledgeBases(data);
      } catch (error) {
        toast.error(error instanceof Error ? error.message : t("aiAgent.loadKnowledgeFailed"));
      }
    }
    void loadKnowledgeBases();
  }, [t]);

  useEffect(() => {
    async function loadSkills() {
      try {
        const data = await fetchSkillDefinitionsAll({
          status: Status.Ok,
        });
        setSkills(data);
      } catch (error) {
        toast.error(error instanceof Error ? error.message : t("aiAgent.loadSkillsFailed"));
      }
    }
    void loadSkills();
  }, [t]);

  useEffect(() => {
    async function loadDirectToolOptions() {
      try {
        const catalog = await fetchMCPCatalog();
        setToolCatalog(catalog);
        setDirectToolOptions(
          catalog
            .filter((tool) => !tool.autoInjected && tool.sourceType === "mcp")
            .map((tool) => ({
              value: tool.toolCode,
              label: `${tool.title || tool.toolName} · ${tool.toolCode}`,
              sourceType: tool.sourceType,
              autoInjected: tool.autoInjected,
              groupLabel:
                tool.sourceType === "builtin"
                  ? t("aiAgent.builtinTools")
                  : tool.serverCode,
              meta: {
                toolCode: tool.toolCode,
                serverCode: tool.serverCode,
                toolName: tool.toolName,
                title: tool.title || tool.toolName,
                description: tool.description || "",
                arguments: undefined,
              },
            })),
        );
        setGraphToolOptions(
          catalog
            .filter((tool) => tool.sourceType === "graph")
            .map((tool) => ({
              value: tool.toolCode,
              label: `${tool.title || tool.toolName} · ${tool.toolCode}`,
            })),
        );
      } catch (error) {
        toast.error(
          error instanceof Error ? error.message : t("aiAgent.loadDirectToolsFailed"),
        );
      }
    }
    void loadDirectToolOptions();
  }, [t]);

  const aiConfigOptions = useMemo(
    () =>
      aiConfigs.map((item) => ({
        value: String(item.id),
        label: `${item.name} · ${item.modelName}`,
      })),
    [aiConfigs],
  );

  const teamOptions = useMemo(
    () =>
      agentTeams.map((item: AdminAgentTeam) => ({
        value: String(item.id),
        label: item.name,
      })),
    [agentTeams],
  );

  const knowledgeOptions = useMemo(
    () =>
      knowledgeBases.map((item) => ({
        value: String(item.id),
        label: item.name,
      })),
    [knowledgeBases],
  );

  const skillOptions = useMemo(
    () =>
      skills.map((item) => ({
        value: String(item.id),
        label: item.name,
      })),
    [skills],
  );

  const addableKnowledgeOptions = useMemo(
    () =>
      knowledgeOptions.filter(
        (option) => !selectedKnowledgeIds.includes(Number(option.value)),
      ),
    [knowledgeOptions, selectedKnowledgeIds],
  );

  const selectedKnowledgeOptions = useMemo(
    () =>
      selectedKnowledgeIds
        .map((id) =>
          knowledgeOptions.find((option) => Number(option.value) === id),
        )
        .filter(
          (option): option is { value: string; label: string } => !!option,
        ),
    [knowledgeOptions, selectedKnowledgeIds],
  );

  const addableSkillOptions = useMemo(
    () =>
      skillOptions.filter(
        (option) => !selectedSkillIds.includes(Number(option.value)),
      ),
    [selectedSkillIds, skillOptions],
  );

  const selectedSkillOptions = useMemo(
    () =>
      selectedSkillIds
        .map((id) => skillOptions.find((option) => Number(option.value) === id))
        .filter(
          (option): option is { value: string; label: string } => !!option,
        ),
    [selectedSkillIds, skillOptions],
  );

  const addableDirectToolOptions = useMemo(
    () =>
      directToolOptions.filter(
        (option) =>
          option.groupLabel === directToolGroupToAdd &&
          !directTools.some((tool) => tool.toolCode === option.value),
      ),
    [directToolOptions, directToolGroupToAdd, directTools],
  );

  const directToolGroupOptions = useMemo(
    () =>
      Array.from(
        new Map(
          directToolOptions.map((option) => [
            option.groupLabel,
            {
              value: option.groupLabel,
              label: option.groupLabel,
            },
          ]),
        ).values(),
      ),
    [directToolOptions],
  );

  const directToolsGrouped = useMemo(() => {
    const groups = new Map<string, DirectToolItem[]>();
    for (const tool of directTools) {
      const groupLabel = tool.serverCode || t("aiAgent.ungrouped");
      const current = groups.get(groupLabel) ?? [];
      current.push(tool);
      groups.set(groupLabel, current);
    }
    return Array.from(groups.entries());
  }, [directTools, t]);

  const addableGraphToolOptions = useMemo(
    () =>
      graphToolOptions.filter(
        (option) => !graphTools.includes(option.value),
      ),
    [graphToolOptions, graphTools],
  );

  const addableTeamOptions = useMemo(
    () =>
      teamOptions.filter(
        (option) => !selectedTeamIds.includes(Number(option.value)),
      ),
    [teamOptions, selectedTeamIds],
  );

  const selectedTeamOptions = useMemo(
    () =>
      selectedTeamIds
        .map((id) => teamOptions.find((option) => Number(option.value) === id))
        .filter(
          (option): option is { value: string; label: string } => !!option,
        ),
    [selectedTeamIds, teamOptions],
  );

  const handoffMode = watch("handoffMode");
  const selectedHandoffModeLabel =
    handoffModeOptions.find((item) => item.value === handoffMode)?.label ??
    t("aiAgent.notSelected");

  async function onFormSubmit(values: EditForm) {
    await onSubmit(
      buildPayload(
        values,
        selectedKnowledgeIds,
        selectedTeamIds,
        selectedSkillIds,
        directTools,
        graphTools,
      ),
    );
  }

  function handleAddTeam(value: string) {
    const id = Number(value);
    if (!Number.isFinite(id) || id <= 0 || selectedTeamIds.includes(id)) {
      return;
    }
    setSelectedTeamIds((prev) => [...prev, id]);
    setTeamToAdd("");
  }

  function handleRemoveTeam(id: number) {
    setSelectedTeamIds((prev) => prev.filter((item) => item !== id));
  }

  function handleAddKnowledge(value: string) {
    const id = Number(value);
    if (!Number.isFinite(id) || id <= 0 || selectedKnowledgeIds.includes(id)) {
      return;
    }
    setSelectedKnowledgeIds((prev) => [...prev, id]);
    setKnowledgeToAdd("");
  }

  function handleMoveKnowledge(index: number, direction: -1 | 1) {
    const targetIndex = index + direction;
    if (targetIndex < 0 || targetIndex >= selectedKnowledgeIds.length) {
      return;
    }
    setSelectedKnowledgeIds((prev) => {
      const next = [...prev];
      const current = next[index];
      next[index] = next[targetIndex];
      next[targetIndex] = current;
      return next;
    });
  }

  function handleRemoveKnowledge(id: number) {
    setSelectedKnowledgeIds((prev) => prev.filter((item) => item !== id));
  }

  function handleAddSkill(value: string) {
    const id = Number(value);
    if (!Number.isFinite(id) || id <= 0 || selectedSkillIds.includes(id)) {
      return;
    }
    setSelectedSkillIds((prev) => [...prev, id]);
    setSkillToAdd("");
  }

  function handleRemoveSkill(id: number) {
    setSelectedSkillIds((prev) => prev.filter((item) => item !== id));
  }

  function handleAddDirectTool(value: string) {
    const option = directToolOptions.find((item) => item.value === value);
    if (!option) {
      return;
    }
    setDirectTools((prev) => {
      if (
        prev.some(
          (item) =>
            item.toolCode === option.meta.toolCode,
        )
      ) {
        return prev;
      }
      return [...prev, option.meta];
    });
    setDirectToolGroupToAdd(option.groupLabel);
    setDirectToolToAdd("");
  }

  function handleRemoveDirectTool(value: string) {
    setDirectTools((prev) => prev.filter((item) => item.toolCode !== value));
  }

  function handleAddGraphTool(value: string) {
    if (!value || graphTools.includes(value)) {
      return;
    }
    setGraphTools((prev) => [...prev, value]);
    setGraphToolToAdd("");
  }

  function handleRemoveGraphTool(value: string) {
    setGraphTools((prev) => prev.filter((item) => item !== value));
  }

  return (
    <ProjectDialog
      open={open}
      onOpenChange={onOpenChange}
      title={itemId ? t("aiAgent.editTitle") : t("aiAgent.createTitle")}
      size="xl"
      defaultFullscreen
      footer={
        <>
          <Button
            type="button"
            variant="outline"
            onClick={() => onOpenChange(false)}
          >
            {t("aiAgent.cancel")}
          </Button>
          <Button type="submit" form={formId} disabled={saving || loading}>
            {saving ? t("aiAgent.saving") : t("aiAgent.save")}
          </Button>
        </>
      }
    >
      {loading ? (
        <div className="flex items-center justify-center py-12">
          <div className="text-muted-foreground">{t("aiAgent.loading")}</div>
        </div>
      ) : (
        <form
          id={formId}
          onSubmit={handleSubmit(onFormSubmit)}
          className="space-y-6"
        >
          <SectionCard
            title={t("aiAgent.sectionBasic")}
          >
            <div className="grid grid-cols-1 gap-4 xl:grid-cols-3">
              <Field data-invalid={!!errors.name}>
                <FieldLabel htmlFor="ai-agent-name">{t("aiAgent.name")}</FieldLabel>
                <FieldContent>
                  <Input id="ai-agent-name" {...register("name")} />
                  <FieldError errors={[errors.name]} />
                </FieldContent>
              </Field>

              <Field data-invalid={!!errors.aiConfigId}>
                <FieldLabel>{t("aiAgent.aiConfig")}</FieldLabel>
                <FieldContent>
                  <Controller
                    control={control}
                    name="aiConfigId"
                    render={({ field }) => (
                      <OptionCombobox
                        value={field.value}
                        options={aiConfigOptions}
                        placeholder={t("aiAgent.selectAiConfig")}
                        searchPlaceholder={t("aiAgent.searchAiConfig")}
                        emptyText={t("aiAgent.emptyAiConfig")}
                        onChange={field.onChange}
                      />
                    )}
                  />
                  <FieldError errors={[errors.aiConfigId]} />
                </FieldContent>
              </Field>

              <Field data-invalid={!!errors.serviceMode}>
                <FieldLabel>{t("aiAgent.columnServiceMode")}</FieldLabel>
                <FieldContent>
                  <Controller
                    control={control}
                    name="serviceMode"
                    render={({ field }) => (
                      <OptionCombobox
                        value={field.value}
                        options={serviceModeOptions}
                        placeholder={t("aiAgent.selectServiceMode")}
                        searchPlaceholder={t("aiAgent.searchServiceMode")}
                        emptyText={t("aiAgent.emptyServiceMode")}
                        onChange={field.onChange}
                      />
                    )}
                  />
                  <FieldError errors={[errors.serviceMode]} />
                </FieldContent>
              </Field>
            </div>

            <Field data-invalid={!!errors.description}>
              <FieldLabel htmlFor="ai-agent-description">{t("aiAgent.description")}</FieldLabel>
              <FieldContent>
                <Textarea id="ai-agent-description" {...register("description")} />
                <FieldError errors={[errors.description]} />
              </FieldContent>
            </Field>
            <Field data-invalid={!!errors.welcomeMessage}>
              <FieldLabel htmlFor="ai-agent-welcome-message">{t("aiAgent.welcomeMessage")}</FieldLabel>
              <FieldContent>
                <Textarea
                  id="ai-agent-welcome-message"
                  rows={5}
                  {...register("welcomeMessage")}
                />
                <FieldError errors={[errors.welcomeMessage]} />
              </FieldContent>
            </Field>

            <Field data-invalid={!!errors.systemPrompt}>
              <FieldLabel htmlFor="ai-agent-system-prompt">
                {t("aiAgent.systemPrompt")}
              </FieldLabel>
              <FieldContent>
                <Textarea
                  id="ai-agent-system-prompt"
                  rows={8}
                  {...register("systemPrompt")}
                />
                <FieldError errors={[errors.systemPrompt]} />
              </FieldContent>
            </Field>
          </SectionCard>

          <SectionCard
            title={t("aiAgent.sectionStrategy")}
          >
            <div className="grid grid-cols-1 gap-4 xl:grid-cols-[minmax(0,1fr)_minmax(320px,1.15fr)]">
              <div className="space-y-4">
                <Field data-invalid={!!errors.handoffMode}>
                  <FieldLabel>{t("aiAgent.handoffMode")}</FieldLabel>
                  <FieldContent>
                    <Controller
                      control={control}
                      name="handoffMode"
                      render={({ field }) => (
                        <OptionCombobox
                          value={field.value}
                          options={handoffModeOptions}
                          placeholder={t("aiAgent.selectHandoffMode")}
                          searchPlaceholder={t("aiAgent.searchHandoffMode")}
                          emptyText={t("aiAgent.emptyHandoffMode")}
                          onChange={field.onChange}
                        />
                      )}
                    />
                    <FieldError errors={[errors.handoffMode]} />
                  </FieldContent>
                </Field>

                <Field data-invalid={!!errors.replyTimeoutSeconds}>
                  <FieldLabel
                    htmlFor="ai-agent-reply-timeout-seconds"
                    className="flex items-center gap-1"
                  >
                    {t("aiAgent.replyTimeoutSeconds")}
                    <Popover>
                      <PopoverTrigger
                        render={
                          <button
                            type="button"
                            className="inline-flex items-center justify-center text-muted-foreground hover:text-foreground"
                          >
                            <InfoIcon className="size-4" />
                          </button>
                        }
                      />
                      <PopoverContent side="top" align="start" className="max-w-xs">
                        <PopoverDescription>
                          {t("aiAgent.replyTimeoutHelp")}
                        </PopoverDescription>
                      </PopoverContent>
                    </Popover>
                  </FieldLabel>
                  <FieldContent>
                    <Input
                      id="ai-agent-reply-timeout-seconds"
                      type="number"
                      min={0}
                      step={1}
                      {...register("replyTimeoutSeconds", { valueAsNumber: true })}
                    />
                    <FieldError errors={[errors.replyTimeoutSeconds]} />
                  </FieldContent>
                </Field>
              </div>

              <div className="rounded-lg border bg-muted/10 p-4">
                <div className="mb-1 text-sm font-medium">{t("aiAgent.teams")}</div>
                <div className="mb-4 text-xs text-muted-foreground">
                  {t("aiAgent.currentHandoffMode", { mode: selectedHandoffModeLabel })}
                  {handoffMode ===
                    String(AIAgentHandoffMode.DefaultTeamPool)
                    ? t("aiAgent.handoffNeedsTeam")
                    : t("aiAgent.handoffOnlyWhenNeeded")}
                </div>
                <Field>
                  <FieldContent className="space-y-3">
                    <OptionCombobox
                      value={teamToAdd}
                      options={addableTeamOptions}
                      placeholder={t("aiAgent.selectTeam")}
                      searchPlaceholder={t("aiAgent.searchTeam")}
                      emptyText={t("aiAgent.emptyTeam")}
                      onChange={handleAddTeam}
                    />
                    <div className="flex flex-wrap gap-2">
                      {selectedTeamOptions.length === 0 ? (
                        <span className="text-sm text-muted-foreground">
                          {t("aiAgent.noTeams")}
                        </span>
                      ) : (
                        selectedTeamOptions.map((option) => (
                          <Badge
                            key={option.value}
                            variant="secondary"
                            className="gap-1 pr-1"
                          >
                            {option.label}
                            <Button
                              type="button"
                              variant="ghost"
                              size="icon"
                              className="size-5"
                              onClick={() =>
                                handleRemoveTeam(Number(option.value))
                              }
                              aria-label={t("aiAgent.removeTeam", { name: option.label })}
                            >
                              <Trash2Icon className="size-3" />
                            </Button>
                          </Badge>
                        ))
                      )}
                    </div>
                  </FieldContent>
                </Field>
              </div>
            </div>
          </SectionCard>

          <SectionCard
            title={t("aiAgent.sectionCapabilities")}
          >
            <Tabs defaultValue="knowledge" className="gap-4">
              <TabsList className="w-fit">
                <TabsTrigger value="knowledge">{t("aiAgent.knowledgeTab")}</TabsTrigger>
                <TabsTrigger value="skills">{t("aiAgent.skillsTab")}</TabsTrigger>
                <TabsTrigger value="direct-tools">{t("aiAgent.directToolsTab")}</TabsTrigger>
                <TabsTrigger value="graph-tools">{t("aiAgent.graphToolsTab")}</TabsTrigger>
              </TabsList>

              <TabsContent value="knowledge" className="space-y-4">
                <div className="text-xs text-muted-foreground">
                  {t("aiAgent.knowledgeHint")}
                </div>
                <Field data-invalid={selectedKnowledgeIds.length === 0}>
                  <FieldContent className="space-y-3">
                    <div className="flex items-center gap-2">
                      <div className="flex-1">
                        <OptionCombobox
                          value={knowledgeToAdd}
                          options={addableKnowledgeOptions}
                          placeholder={t("aiAgent.selectKnowledge")}
                          searchPlaceholder={t("aiAgent.searchKnowledge")}
                          emptyText={t("aiAgent.emptyKnowledge")}
                          onChange={handleAddKnowledge}
                        />
                      </div>
                      <Button
                        type="button"
                        variant="outline"
                        disabled={!knowledgeToAdd}
                        onClick={() => handleAddKnowledge(knowledgeToAdd)}
                      >
                        <PlusIcon />
                        {t("aiAgent.add")}
                      </Button>
                    </div>
                    {selectedKnowledgeIds.length === 0 ? (
                      <div className="rounded-md border border-dashed px-3 py-4 text-sm text-muted-foreground">
                        {t("aiAgent.knowledgeRequired")}
                      </div>
                    ) : (
                      <div className="space-y-2 rounded-md border p-3">
                        {selectedKnowledgeOptions.map((option, index) => (
                          <div key={option.value} className="flex items-center gap-2">
                            <Badge
                              variant="secondary"
                              className="min-w-8 justify-center"
                            >
                              {index + 1}
                            </Badge>
                            <div className="flex-1 text-sm">{option.label}</div>
                            <Button
                              type="button"
                              variant="outline"
                              size="icon-sm"
                              disabled={index === 0}
                              onClick={() => handleMoveKnowledge(index, -1)}
                            >
                              <ArrowUpIcon />
                            </Button>
                            <Button
                              type="button"
                              variant="outline"
                              size="icon-sm"
                              disabled={
                                index === selectedKnowledgeOptions.length - 1
                              }
                              onClick={() => handleMoveKnowledge(index, 1)}
                            >
                              <ArrowDownIcon />
                            </Button>
                            <Button
                              type="button"
                              variant="outline"
                              size="icon-sm"
                              onClick={() =>
                                handleRemoveKnowledge(Number(option.value))
                              }
                            >
                              <Trash2Icon />
                            </Button>
                          </div>
                        ))}
                      </div>
                    )}
                    {selectedKnowledgeIds.length === 0 ? (
                      <FieldError errors={[{ message: t("aiAgent.knowledgeRequired") }]} />
                    ) : null}
                  </FieldContent>
                </Field>

                <div className="grid grid-cols-1 gap-4 xl:grid-cols-1">
                  <Field data-invalid={!!errors.fallbackMode}>
                    <FieldLabel>{t("aiAgent.fallbackMode")}</FieldLabel>
                    <FieldContent className="space-y-3">
                      <Controller
                        control={control}
                        name="fallbackMode"
                        render={({ field }) => (
                          <OptionCombobox
                            value={field.value}
                            options={fallbackModeOptions}
                            placeholder={t("aiAgent.selectFallbackMode")}
                            searchPlaceholder={t("aiAgent.searchFallbackMode")}
                            emptyText={t("aiAgent.emptyFallbackMode")}
                            onChange={field.onChange}
                          />
                        )}
                      />
                      <FieldError errors={[errors.fallbackMode]} />
                    </FieldContent>
                  </Field>

                  <Field data-invalid={!!errors.fallbackMessage}>
                    <FieldLabel htmlFor="ai-agent-fallback-message">
                      {t("aiAgent.fallbackMessage")}
                    </FieldLabel>
                    <FieldContent>
                      <div className="mb-1 text-xs text-muted-foreground">
                        {t("aiAgent.fallbackMessageHint")}
                      </div>
                      <Textarea
                        id="ai-agent-fallback-message"
                        rows={5}
                        {...register("fallbackMessage")}
                      />
                      <FieldError errors={[errors.fallbackMessage]} />
                    </FieldContent>
                  </Field>
                </div>
              </TabsContent>

              <TabsContent value="skills" className="space-y-4">
                <div className="text-xs text-muted-foreground">
                  {t("aiAgent.skillsHint")}
                </div>
                <Field>
                  <FieldContent className="space-y-3">
                    <div className="flex items-center gap-2">
                      <div className="flex-1">
                        <OptionCombobox
                          value={skillToAdd}
                          options={addableSkillOptions}
                          placeholder={t("aiAgent.selectSkill")}
                          searchPlaceholder={t("aiAgent.searchSkill")}
                          emptyText={t("aiAgent.emptySkill")}
                          onChange={handleAddSkill}
                        />
                      </div>
                      <Button
                        type="button"
                        variant="outline"
                        disabled={!skillToAdd}
                        onClick={() => handleAddSkill(skillToAdd)}
                      >
                        <PlusIcon />
                        {t("aiAgent.add")}
                      </Button>
                    </div>
                    <div className="flex flex-wrap gap-2">
                      {selectedSkillOptions.length === 0 ? (
                        <span className="text-sm text-muted-foreground">
                          {t("aiAgent.noSkillsHint")}
                        </span>
                      ) : (
                        selectedSkillOptions.map((option) => (
                          <Badge
                            key={option.value}
                            variant="secondary"
                            className="gap-1 pr-1"
                          >
                            {option.label}
                            <Button
                              type="button"
                              variant="ghost"
                              size="icon"
                              className="size-5"
                              onClick={() =>
                                handleRemoveSkill(Number(option.value))
                              }
                              aria-label={t("aiAgent.removeSkill", { name: option.label })}
                            >
                              <Trash2Icon className="size-3" />
                            </Button>
                          </Badge>
                        ))
                      )}
                    </div>
                  </FieldContent>
                </Field>
              </TabsContent>

              <TabsContent value="direct-tools" className="space-y-4">
                <div className="text-xs text-muted-foreground">
                  {t("aiAgent.directToolsHint")}
                </div>
                <Field>
                  <FieldContent className="space-y-3">
                    <div className="flex items-center gap-2">
                      <div className="w-52">
                        <OptionCombobox
                          value={directToolGroupToAdd}
                          options={directToolGroupOptions}
                          placeholder={t("aiAgent.selectToolGroup")}
                          searchPlaceholder={t("aiAgent.searchToolGroup")}
                          emptyText={t("aiAgent.emptyToolGroup")}
                          onChange={(value) => {
                            setDirectToolGroupToAdd(value);
                            setDirectToolToAdd("");
                          }}
                        />
                      </div>
                      <div className="flex-1">
                        <OptionCombobox
                          value={directToolToAdd}
                          options={addableDirectToolOptions}
                          placeholder={t("aiAgent.selectDirectTool")}
                          searchPlaceholder={t("aiAgent.searchDirectTool")}
                          emptyText={t("aiAgent.emptyDirectTool")}
                          onChange={handleAddDirectTool}
                        />
                      </div>
                      <Button
                        type="button"
                        variant="outline"
                        disabled={!directToolGroupToAdd || !directToolToAdd}
                        onClick={() => handleAddDirectTool(directToolToAdd)}
                      >
                        <PlusIcon />
                        {t("aiAgent.add")}
                      </Button>
                    </div>
                    <div className="space-y-3">
                      {directTools.length === 0 ? (
                        <span className="text-sm text-muted-foreground">
                          {t("aiAgent.noDirectToolsHint")}
                        </span>
                      ) : (
                        directToolsGrouped.map(([groupLabel, tools]) => (
                          <div key={groupLabel} className="rounded-md border p-3">
                            <div className="mb-2 text-xs font-medium text-muted-foreground">
                              {groupLabel}
                            </div>
                            <div className="flex flex-wrap gap-2">
                              {tools.map((tool) => {
                                const value = tool.toolCode;
                                const catalogItem = toolCatalog.find(
                                  (item) => item.toolCode === tool.toolCode,
                                );
                                return (
                                  <Badge
                                    key={value}
                                    variant="secondary"
                                    className="gap-1 pr-1"
                                  >
                                    {tool.title || catalogItem?.title || value}
                                    <span className="text-[10px] text-muted-foreground/80">
                                      {tool.serverCode || "MCP"}
                                    </span>
                                    <Button
                                      type="button"
                                      variant="ghost"
                                      size="icon"
                                      className="size-5"
                                      onClick={() =>
                                        handleRemoveDirectTool(value)
                                      }
                                      aria-label={t("aiAgent.removeDirectTool", { name: value })}
                                    >
                                      <Trash2Icon className="size-3" />
                                    </Button>
                                  </Badge>
                                );
                              })}
                            </div>
                          </div>
                        ))
                      )}
                    </div>
                  </FieldContent>
                </Field>
              </TabsContent>

              <TabsContent value="graph-tools" className="space-y-4">
                <div className="text-xs text-muted-foreground">
                  {t("aiAgent.graphToolsHint")}
                </div>
                <Field>
                  <FieldContent className="space-y-3">
                    <div className="flex items-center gap-2">
                      <div className="flex-1">
                        <OptionCombobox
                          value={graphToolToAdd}
                          options={addableGraphToolOptions}
                          placeholder={t("aiAgent.selectGraphTool")}
                          searchPlaceholder={t("aiAgent.searchGraphTool")}
                          emptyText={t("aiAgent.emptyGraphTool")}
                          onChange={handleAddGraphTool}
                        />
                      </div>
                      <Button
                        type="button"
                        variant="outline"
                        disabled={!graphToolToAdd}
                        onClick={() => handleAddGraphTool(graphToolToAdd)}
                      >
                        <PlusIcon />
                        {t("aiAgent.add")}
                      </Button>
                    </div>
                    <div className="flex flex-wrap gap-2">
                      {graphTools.length === 0 ? (
                        <span className="text-sm text-muted-foreground">
                          {t("aiAgent.noGraphToolsHint")}
                        </span>
                      ) : (
                        graphTools.map((toolCode) => {
                          const catalogItem = toolCatalog.find(
                            (item) => item.toolCode === toolCode,
                          );
                          return (
                            <Badge
                              key={toolCode}
                              variant="secondary"
                              className="gap-1 pr-1"
                            >
                              {catalogItem?.title || toolCode}
                              <span className="text-[10px] text-muted-foreground/80">
                                graph
                              </span>
                              <Button
                                type="button"
                                variant="ghost"
                                size="icon"
                                className="size-5"
                                onClick={() => handleRemoveGraphTool(toolCode)}
                                aria-label={t("aiAgent.removeGraphTool", { name: toolCode })}
                              >
                                <Trash2Icon className="size-3" />
                              </Button>
                            </Badge>
                          );
                        })
                      )}
                    </div>
                  </FieldContent>
                </Field>
              </TabsContent>
            </Tabs>
          </SectionCard>
        </form>
      )}
    </ProjectDialog>
  );
}

function SectionCard({
  title,
  description,
  children,
  defaultOpen = true,
}: {
  title: string;
  description?: string;
  children: ReactNode;
  defaultOpen?: boolean;
}) {
  const [open, setOpen] = useState(defaultOpen);

  return (
    <section className="rounded-lg border bg-card p-5">
      <button
        type="button"
        className="flex w-full items-start justify-between gap-4 text-left"
        onClick={() => setOpen((prev) => !prev)}
        aria-expanded={open}
      >
        <div className="min-w-0">
          <div className="text-base font-semibold">{title}</div>
          {description ? (
            <div className="mt-1 text-sm text-muted-foreground">
              {description}
            </div>
          ) : null}
        </div>
        <span className="mt-0.5 inline-flex size-7 shrink-0 items-center justify-center rounded-full border bg-muted/30 text-muted-foreground transition-colors hover:bg-muted/50">
          <ChevronDownIcon
            className={`size-4 transition-transform duration-200 ${
              open ? "rotate-180" : "rotate-0"
            }`}
          />
        </span>
      </button>
      {open ? <div className="mt-3 space-y-4">{children}</div> : null}
    </section>
  );
}

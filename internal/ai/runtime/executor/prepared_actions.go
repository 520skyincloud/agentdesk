package executor

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"agent-desk/internal/ai/runtime/contracts"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/services"

	"github.com/mlogclub/simple/sqls"
)

func prepareRuntimeActions(
	req RunInput,
	taskState runtimeTaskBatchState,
	plan contracts.ReplyPlanV2,
	evidence contracts.EvidenceBundleV1,
	ledger contracts.ActionLedgerV1,
) ([]contracts.PreparedActionV1, contracts.ActionLedgerV1, error) {
	if len(ledger.Actions) == 0 {
		return nil, ledger, nil
	}
	sequenceByTask := make(map[string]int, len(plan.Tasks))
	for _, task := range plan.Tasks {
		sequenceByTask[task.TaskKey] = task.Sequence
	}
	prepared := make([]contracts.PreparedActionV1, 0, len(ledger.Actions))
	for _, action := range ledger.Actions {
		if !runtimeActionRequiresOutboundMessage(action.ActionType) {
			continue
		}
		item, err := buildPreparedRuntimeAction(req, action, sequenceByTask[action.TaskKey], evidence)
		if err != nil {
			if taskState.Enabled && taskState.TurnID > 0 {
				markErr := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
					return services.AIReplyTurnActionService.FailDB(
						ctx.Tx, req.Conversation.TenantID, taskState.TurnID, taskState.TurnVersion,
						action.ActionKey, string(services.AIReplyExecutionErrorResourceInvariantBroken), time.Now(),
					)
				})
				if markErr != nil {
					return nil, contracts.ActionLedgerV1{}, fmt.Errorf("prepare action %s: %w; persist failure: %v", action.ActionKey, err, markErr)
				}
			}
			return nil, contracts.ActionLedgerV1{}, services.NewAIReplyExecutionError(
				services.AIReplyExecutionErrorResourceInvariantBroken,
				fmt.Errorf("prepare action %s: %w", action.ActionKey, err),
			)
		}
		prepared = append(prepared, item)
	}
	if len(prepared) == 0 {
		return nil, ledger, nil
	}
	if taskState.Enabled && taskState.TurnID > 0 {
		err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
			for _, action := range prepared {
				if _, err := services.AIReplyTurnActionService.PrepareDB(
					ctx.Tx,
					req.Conversation.TenantID,
					taskState.TurnID,
					taskState.TurnVersion,
					action.ActionKey,
					action.PreparedRevision,
				); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return nil, contracts.ActionLedgerV1{}, err
		}
		ledger.Actions = services.AIReplyTurnActionService.ContractsForTurn(
			sqls.DB(), req.Conversation.TenantID, taskState.TurnID, taskState.TurnVersion,
		)
	} else {
		preparedKeys := make(map[string]string, len(prepared))
		for _, item := range prepared {
			preparedKeys[item.ActionKey] = item.PreparedRevision
		}
		for index := range ledger.Actions {
			if _, ok := preparedKeys[ledger.Actions[index].ActionKey]; ok {
				ledger.Actions[index].Status = "prepared"
				ledger.Actions[index].ResultCode = ""
			}
		}
	}
	sort.SliceStable(prepared, func(i, j int) bool {
		if prepared[i].Sequence == prepared[j].Sequence {
			return prepared[i].ActionKey < prepared[j].ActionKey
		}
		return prepared[i].Sequence < prepared[j].Sequence
	})
	if err := validateActionLedgerContract(ledger); err != nil {
		return nil, contracts.ActionLedgerV1{}, err
	}
	return prepared, ledger, nil
}

func buildPreparedRuntimeAction(
	req RunInput,
	action contracts.ActionLedgerItemV1,
	sequence int,
	evidence contracts.EvidenceBundleV1,
) (contracts.PreparedActionV1, error) {
	resourceType := ""
	if action.ResourceType != nil {
		resourceType = strings.TrimSpace(*action.ResourceType)
	}
	prepared := contracts.PreparedActionV1{
		ActionKey: action.ActionKey, TaskKey: action.TaskKey, ActionType: action.ActionType,
		ResourceType: resourceType, Sequence: sequence,
	}
	instance, err := resolvePreparedActionInstance(req)
	if err != nil {
		return contracts.PreparedActionV1{}, err
	}
	switch action.ActionType {
	case "send_location":
		prepared.MessageType = string(enums.IMMessageTypeLocation)
		prepared.Content, prepared.Payload, err = services.WxWorkProtocolDefaultResourceService.BuildDefaultLocationMessage(instance)
	case "send_mini_program":
		prepared.MessageType = string(enums.IMMessageTypeMiniProgram)
		prepared.Content, prepared.Payload, err = services.WxWorkProtocolDefaultResourceService.BuildDefaultMiniProgramMessage(instance)
	case "send_phone":
		prepared.MessageType = string(enums.IMMessageTypeText)
		prepared.Content, prepared.Payload, err = services.WxWorkProtocolDefaultResourceService.BuildRuntimePhoneMessage(instance)
	case "send_knowledge_image":
		prepared.MessageType = string(enums.IMMessageTypeImage)
		prepared.ResourceRef = strings.TrimPrefix(resourceType, "image:")
		prepared.Content, prepared.Payload, err = buildPreparedKnowledgeImage(req, prepared.ResourceRef, evidence)
	default:
		return contracts.PreparedActionV1{}, fmt.Errorf("unsupported outbound action type %q", action.ActionType)
	}
	if err != nil {
		return contracts.PreparedActionV1{}, err
	}
	if strings.TrimSpace(prepared.Content) == "" || (preparedActionPayloadRequired(prepared.ActionType) && strings.TrimSpace(prepared.Payload) == "") {
		return contracts.PreparedActionV1{}, fmt.Errorf("prepared action payload is empty")
	}
	prepared.PreparedRevision = preparedActionRevision(prepared)
	return prepared, nil
}

func preparedActionPayloadRequired(actionType string) bool {
	return strings.TrimSpace(actionType) != "send_phone"
}

func resolvePreparedActionInstance(req RunInput) (*models.WxWorkProtocolInstance, error) {
	route := services.ConversationRouteService.GetByConversationIDInTenant(req.Conversation.ID, req.Conversation.TenantID)
	if route == nil || route.WxWorkInstanceID <= 0 {
		return nil, fmt.Errorf("runtime instance route is unavailable")
	}
	instance := services.WxWorkProtocolInstanceService.GetByTenantID(route.WxWorkInstanceID, req.Conversation.TenantID)
	if instance == nil {
		return nil, fmt.Errorf("runtime instance is unavailable")
	}
	return services.StoreService.HydrateRuntimeInstanceDB(sqls.DB(), instance)
}

func buildPreparedKnowledgeImage(req RunInput, resourceRef string, evidence contracts.EvidenceBundleV1) (string, string, error) {
	resourceRef = strings.TrimSpace(resourceRef)
	for _, resource := range evidence.Resources {
		if resource.Ref != resourceRef || resource.Type != "image" || resource.AssetID == nil {
			continue
		}
		assetID := strings.TrimSpace(*resource.AssetID)
		asset := services.AssetService.GetByAssetIDInTenant(assetID, req.Conversation.TenantID)
		if asset == nil || asset.Status != enums.AssetStatusSuccess {
			return "", "", fmt.Errorf("knowledge image asset is unavailable")
		}
		payload, err := services.BuildIMMessageAssetPayload(asset)
		if err != nil {
			return "", "", err
		}
		content := strings.TrimSpace(resource.Title)
		if content == "" {
			content = strings.TrimSpace(asset.Filename)
		}
		return content, payload, nil
	}
	return "", "", fmt.Errorf("knowledge image resource ref %q is unavailable", resourceRef)
}

func preparedActionRevision(action contracts.PreparedActionV1) string {
	payload := strings.Join([]string{
		action.ActionKey, action.TaskKey, action.ActionType, action.ResourceType,
		action.ResourceRef, action.MessageType, action.Content, action.Payload,
	}, "\n")
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:16])
}

func runtimeActionRequiresOutboundMessage(actionType string) bool {
	switch strings.TrimSpace(actionType) {
	case "send_location", "send_mini_program", "send_phone", "send_knowledge_image":
		return true
	default:
		return false
	}
}

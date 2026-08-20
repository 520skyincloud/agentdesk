package services

import (
	"context"

	"agent-desk/internal/models"
)

var TriggerAIReplyAsyncHook func(conversation models.Conversation, message models.Message)
var TriggerAIReplySyncHook func(ctx context.Context, conversation models.Conversation, message models.Message) error
var TriggerStandaloneOneReplyAsyncHook func(conversation models.Conversation, message models.Message)

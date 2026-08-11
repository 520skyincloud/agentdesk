package executor

import (
	"agent-desk/internal/ai/runtime/contracts"
	"agent-desk/internal/pkg/strictjson"
)

func parseRuntimeReplyOutputV2(content string) (contracts.ReplyOutputV2, error) {
	return strictjson.DecodeObject[contracts.ReplyOutputV2]([]byte(content), strictjson.DecodeOptions{
		MaxBytes: 64 * 1024,
		Schema:   contracts.MustSchema(contracts.SchemaReplyOutputV2),
	})
}

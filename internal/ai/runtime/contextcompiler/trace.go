package contextcompiler

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"agent-desk/internal/ai/runtime/contracts"

	"github.com/cloudwego/eino/schema"
)

func contextFingerprint(
	input CompileInput,
	messages []*schema.Message,
	evidence *contracts.EvidenceBundleV1,
	evidenceV2 *contracts.EvidenceBundleV2,
	tagText string,
) string {
	h := sha256.New()
	writeFingerprintPart(h, string(input.Stage))
	writeFingerprintPart(h, strconv.FormatInt(input.Model.ProfileRevision, 10))
	writeFingerprintPart(h, strconv.FormatInt(input.Model.CredentialRevision, 10))
	writeFingerprintPart(h, strconv.FormatInt(input.IntentProfileRevision, 10))
	if input.DialogueState != nil {
		writeFingerprintPart(h, strconv.FormatInt(input.DialogueState.Revision, 10))
	}
	writeFingerprintPart(h, fmt.Sprintf("%d/%d", input.Scope.TurnID, input.Scope.TurnVersion))
	for _, message := range messages {
		if message == nil {
			continue
		}
		writeFingerprintPart(h, string(message.Role))
		contentHash := sha256.Sum256([]byte(message.Content))
		writeFingerprintPart(h, hex.EncodeToString(contentHash[:]))
	}
	if evidence != nil {
		for _, item := range evidence.Items {
			writeFingerprintPart(h, item.Ref)
			contentHash := sha256.Sum256([]byte(item.Content))
			writeFingerprintPart(h, hex.EncodeToString(contentHash[:]))
		}
	}
	if evidenceV2 != nil {
		for _, item := range evidenceV2.Items {
			writeFingerprintPart(h, item.Ref)
			writeFingerprintPart(h, item.Answerability)
			writeFingerprintPart(h, item.TopicMatch)
			contentHash := sha256.Sum256([]byte(item.Content))
			writeFingerprintPart(h, hex.EncodeToString(contentHash[:]))
		}
	}
	if input.ReplyPlanV4 != nil {
		writeFingerprintPart(h, input.ReplyPlanV4.PlanFingerprint)
	}
	if input.ResourceEligibility != nil {
		raw := fmt.Sprintf("%v", input.ResourceEligibility.Items)
		resourceHash := sha256.Sum256([]byte(raw))
		writeFingerprintPart(h, hex.EncodeToString(resourceHash[:]))
	}
	writeFingerprintPart(h, strings.TrimSpace(tagText))
	return hex.EncodeToString(h.Sum(nil))
}

type fingerprintWriter interface {
	Write([]byte) (int, error)
}

func writeFingerprintPart(writer fingerprintWriter, value string) {
	_, _ = writer.Write([]byte(strconv.Itoa(len(value))))
	_, _ = writer.Write([]byte{':'})
	_, _ = writer.Write([]byte(value))
	_, _ = writer.Write([]byte{'\n'})
}

package contextcompiler

import (
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/cloudwego/eino/schema"
)

type TokenEstimator interface {
	CountText(modelName, text string) int
	CountMessages(modelName string, messages []*schema.Message) int
	Name() string
}

type TokenizerMatcher func(modelName string) bool

type EstimatorRegistry struct {
	mu       sync.RWMutex
	entries  []estimatorEntry
	fallback TokenEstimator
}

type estimatorEntry struct {
	match     TokenizerMatcher
	estimator TokenEstimator
}

func NewEstimatorRegistry() *EstimatorRegistry {
	return &EstimatorRegistry{fallback: ConservativeEstimator{}}
}

func (r *EstimatorRegistry) Register(match TokenizerMatcher, estimator TokenEstimator) {
	if r == nil || match == nil || estimator == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, estimatorEntry{match: match, estimator: estimator})
}

func (r *EstimatorRegistry) Resolve(modelName string) TokenEstimator {
	if r == nil {
		return ConservativeEstimator{}
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, entry := range r.entries {
		if entry.match(modelName) {
			return entry.estimator
		}
	}
	if r.fallback != nil {
		return r.fallback
	}
	return ConservativeEstimator{}
}

type ConservativeEstimator struct{}

func (ConservativeEstimator) Name() string { return "conservative" }

func (ConservativeEstimator) CountText(_ string, text string) int {
	cjkRunes := 0
	nonCJKBytes := 0
	for _, r := range text {
		if isCJKRune(r) {
			cjkRunes++
		} else {
			nonCJKBytes += utf8.RuneLen(r)
		}
	}
	return cjkRunes + (nonCJKBytes+2)/3
}

func (e ConservativeEstimator) CountMessages(modelName string, messages []*schema.Message) int {
	tokens := 16 + 8*len(messages)
	for _, message := range messages {
		if message == nil {
			continue
		}
		tokens += e.CountText(modelName, message.Content)
	}
	return tokens
}

func isCJKRune(r rune) bool {
	return unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r) ||
		unicode.Is(unicode.Hangul, r) || unicode.Is(unicode.Bopomofo, r)
}

func ModelNameContains(parts ...string) TokenizerMatcher {
	normalized := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.ToLower(strings.TrimSpace(part)); part != "" {
			normalized = append(normalized, part)
		}
	}
	return func(modelName string) bool {
		modelName = strings.ToLower(strings.TrimSpace(modelName))
		for _, part := range normalized {
			if strings.Contains(modelName, part) {
				return true
			}
		}
		return false
	}
}

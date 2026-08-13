// Package channelbreaker 提供回复链路模型通道的进程内熔断。
//
// 当同一通道（stage:model）连续失败达到阈值时，短时间"熔断"该通道，
// 避免每条客户消息都去撞一个已经坏的通道、失败后再兜底转人工。
// 熔断只影响模型调用次数与降级时机，不改变业务转人工语义。
package channelbreaker

import (
	"sync"
	"time"
)

const (
	// FailureThreshold 连续失败达到该次数后打开熔断。
	FailureThreshold = 5
	// OpenDuration 熔断打开时长。
	OpenDuration = 60 * time.Second
)

// State 是单个通道的熔断状态。
type state struct {
	consecutiveFailures int
	openUntil           time.Time
}

// Registry 按 key（stage:model）维护各通道的熔断状态。
type Registry struct {
	mu     sync.Mutex
	states map[string]*state
}

var global = &Registry{states: map[string]*state{}}

// Key 生成熔断键，stage 与 model 任一为空时返回空串。
func Key(stage, model string) string {
	stage = trim(stage)
	model = trim(model)
	if stage == "" || model == "" {
		return ""
	}
	return stage + ":" + model
}

// RecordFailure 记录一次失败，达到阈值后打开熔断。
func RecordFailure(stage, model string, now time.Time) {
	key := Key(stage, model)
	if key == "" {
		return
	}
	global.mu.Lock()
	defer global.mu.Unlock()
	st := global.states[key]
	if st == nil {
		st = &state{}
		global.states[key] = st
	}
	st.consecutiveFailures++
	if st.consecutiveFailures >= FailureThreshold {
		st.openUntil = now.Add(OpenDuration)
		st.consecutiveFailures = 0
	}
}

// RecordSuccess 记录一次成功，关闭熔断并清零计数。
func RecordSuccess(stage, model string) {
	key := Key(stage, model)
	if key == "" {
		return
	}
	global.mu.Lock()
	defer global.mu.Unlock()
	if st := global.states[key]; st != nil {
		st.consecutiveFailures = 0
		st.openUntil = time.Time{}
	}
}

// IsOpen 判断通道是否处于熔断打开状态，并返回恢复时间。
func IsOpen(stage, model string, now time.Time) (open bool, retryAt time.Time) {
	key := Key(stage, model)
	if key == "" {
		return false, time.Time{}
	}
	global.mu.Lock()
	defer global.mu.Unlock()
	st := global.states[key]
	if st == nil || st.openUntil.IsZero() {
		return false, time.Time{}
	}
	if now.Before(st.openUntil) {
		return true, st.openUntil
	}
	// 熔断窗口已过，自动复位。
	st.openUntil = time.Time{}
	st.consecutiveFailures = 0
	return false, time.Time{}
}

// Reset 清空全部熔断状态（测试与运维用）。
func Reset() {
	global.mu.Lock()
	defer global.mu.Unlock()
	global.states = map[string]*state{}
}

func trim(value string) string {
	for len(value) > 0 && (value[0] == ' ' || value[0] == '\t') {
		value = value[1:]
	}
	for len(value) > 0 && (value[len(value)-1] == ' ' || value[len(value)-1] == '\t') {
		value = value[:len(value)-1]
	}
	return value
}

package toolloop

import (
	"fmt"

	"github.com/JIAOZAI1/acore/model"
)

const (
	maxInt64 = int64(^uint64(0) >> 1)
	minInt64 = -maxInt64 - 1
)

func addUsage(left, right model.Usage) (model.Usage, error) {
	fields := [...]struct {
		name  string
		left  int64
		right int64
	}{
		{name: "input tokens", left: left.InputTokens, right: right.InputTokens},
		{name: "output tokens", left: left.OutputTokens, right: right.OutputTokens},
		{name: "cache read", left: left.CacheRead, right: right.CacheRead},
		{name: "cache write", left: left.CacheWrite, right: right.CacheWrite},
		{name: "reasoning tokens", left: left.ReasoningTokens, right: right.ReasoningTokens},
		{name: "total tokens", left: left.TotalTokens, right: right.TotalTokens},
	}

	var values [len(fields)]int64
	for index, field := range fields {
		value, ok := checkedAddInt64(field.left, field.right)
		if !ok {
			return model.Usage{}, fmt.Errorf("%w: %s", ErrUsageOverflow, field.name)
		}
		values[index] = value
	}
	return model.Usage{
		InputTokens:     values[0],
		OutputTokens:    values[1],
		CacheRead:       values[2],
		CacheWrite:      values[3],
		ReasoningTokens: values[4],
		TotalTokens:     values[5],
	}, nil
}

func checkedAddInt64(left, right int64) (int64, bool) {
	if right > 0 && left > maxInt64-right {
		return 0, false
	}
	if right < 0 && left < minInt64-right {
		return 0, false
	}
	return left + right, true
}

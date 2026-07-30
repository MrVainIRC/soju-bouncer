package soju

import (
	"testing"
	"time"
)

func TestChatHistoryAroundEndAllowsClockSkew(t *testing.T) {
	now := time.Date(2026, 7, 29, 12, 49, 9, 200_000_000, time.UTC)

	if got, want := chatHistoryAroundEnd(now, now.Add(800*time.Millisecond)), now.Add(800*time.Millisecond+chatHistoryClockSkewTolerance); !got.Equal(want) {
		t.Fatalf("future reference end = %v, want %v", got, want)
	}
	if got, want := chatHistoryAroundEnd(now, now.Add(-time.Hour)), now.Add(chatHistoryClockSkewTolerance); !got.Equal(want) {
		t.Fatalf("past reference end = %v, want %v", got, want)
	}
}

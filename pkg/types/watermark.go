package types

import "time"

// NewWatermark returns a fresh watermark, a UUIDv4 like every other ID.
// Every write to a Memory or Edge mints a new one. Watermarks identify
// versions; they do not order them — history is ordered by created_at.
func NewWatermark() string { return NewID() }

// WatermarkHistoryEntry is one row in the per-Memory or per-Edge
// version history. OSS retention defaults to 7 days.
type WatermarkHistoryEntry struct {
	TargetID         string    `json:"target_id"` // memory or edge ID
	Watermark        string    `json:"watermark"`
	Op               string    `json:"op"` // imprint | update | patch | append | link | unlink | forget
	AgentID          string    `json:"agent_id"`
	CreatedAt        time.Time `json:"created_at"`
	ContentMD5Before string    `json:"content_md5_before,omitempty"`
	ContentMD5After  string    `json:"content_md5_after,omitempty"`
}

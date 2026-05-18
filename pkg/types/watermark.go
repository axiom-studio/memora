package types

import "time"

// NewWatermark returns a fresh watermark string in wmk_<ULID> format.
// Watermarks are workspace-scoped monotonic version handles; every
// write to a Memory or Edge bumps a new one.
func NewWatermark() string { return NewID(WatermarkPrefix) }

// WatermarkHistoryEntry is one row in the per-Memory or per-Edge
// version history. OSS retention defaults to 7 days.
type WatermarkHistoryEntry struct {
	TargetID         string    `json:"target_id"` // mem_... or edg_...
	Watermark        string    `json:"watermark"`
	Op               string    `json:"op"` // imprint | update | patch | append | link | unlink | forget
	AgentID          string    `json:"agent_id"`
	CreatedAt        time.Time `json:"created_at"`
	ContentMD5Before string    `json:"content_md5_before,omitempty"`
	ContentMD5After  string    `json:"content_md5_after,omitempty"`
}

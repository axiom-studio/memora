package types

import "time"

// Pin is a saved recall query bound to a specific watermark.
type Pin struct {
	PinID       string    `json:"pin_id"`
	WorkspaceID string    `json:"workspace_id"`
	Query       string    `json:"query"`
	Mode        string    `json:"mode,omitempty"`
	K           int       `json:"k,omitempty"`
	Watermark   string    `json:"watermark"`
	Label       string    `json:"label,omitempty"`
	CreatedBy   string    `json:"created_by,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

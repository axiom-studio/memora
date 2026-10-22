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

// Validate checks a pin's IDs: pin and workspace are UUIDs, and the
// creating agent, when recorded, is one too.
func (p *Pin) Validate() error {
	if err := parseErr("pin_id", p.PinID); err != nil {
		return err
	}
	if err := parseErr("workspace_id", p.WorkspaceID); err != nil {
		return err
	}
	if p.CreatedBy != "" {
		return parseErr("created_by", p.CreatedBy)
	}
	return nil
}

package domain

import "errors"

// QuickTaskSnapshot records provenance; definitions live in the Run entries.
type QuickTaskSnapshot struct {
	SavedChannelID  string `json:"saved_channel_id,omitempty"`
	CredentialRunID string `json:"credential_run_id,omitempty"`
}

func (task *QuickTaskSnapshot) validate(snapshot RunSnapshot) error {
	if task.CredentialRunID != "" && (!IsUUID(task.CredentialRunID) || task.SavedChannelID != "") {
		return errors.New("invalid quick run credential reference")
	}
	if task.SavedChannelID != "" && (!IsUUID(task.SavedChannelID) || task.SavedChannelID != snapshot.Channel.ID) {
		return errors.New("quick run saved channel mismatch")
	}
	if len(snapshot.Entries) != 1 || snapshot.Entries[0].TargetKind != "suite" {
		return errors.New("quick run requires one Suite entry")
	}
	return nil
}

func (task *QuickTaskSnapshot) clone() *QuickTaskSnapshot {
	if task == nil {
		return nil
	}
	result := *task
	return &result
}

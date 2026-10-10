package catalog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

func (command *CreateChannelCommand) UnmarshalJSON(data []byte) error {
	type current CreateChannelCommand
	var value current
	if err := decodeChannelCommand(data, &value); err != nil {
		return err
	}
	*command = CreateChannelCommand(value)
	return nil
}

func (command *UpdateChannelCommand) UnmarshalJSON(data []byte) error {
	type current UpdateChannelCommand
	var value current
	if err := decodeChannelCommand(data, &value); err != nil {
		return err
	}
	*command = UpdateChannelCommand(value)
	return nil
}

func decodeChannelCommand(data []byte, command any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(command); err != nil {
		return fmt.Errorf("%w: invalid channel command: %v; configure protocols on model mappings", ErrInvalid, err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("%w: channel command must contain one object", ErrInvalid)
	}
	return nil
}

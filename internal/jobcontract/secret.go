package jobcontract

import "encoding/base64"

// UnmarshalJSON accepts one canonical base64 wire representation. Go's default
// []byte decoder also accepts numeric arrays, which are not this contract.
func (secret *Secret) UnmarshalJSON(raw []byte) error {
	var wire struct {
		Slot  string `json:"slot"`
		Value string `json:"value"`
	}
	if err := decode(raw, &wire); err != nil {
		return invalid("secrets")
	}
	if len(wire.Value) > base64.StdEncoding.EncodedLen(MaxSecretBytes) {
		return invalid("secrets.value")
	}
	value, err := base64.StdEncoding.Strict().DecodeString(wire.Value)
	if err != nil {
		clear(value)
		return invalid("secrets.value")
	}
	if base64.StdEncoding.EncodeToString(value) != wire.Value {
		clear(value)
		return invalid("secrets.value")
	}
	clear(secret.Value)
	*secret = Secret{Slot: wire.Slot, Value: value}
	return nil
}

package jobcontract

import (
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
)

const (
	MaxAssetReceiptBytes       = 1024
	AssetUploadTimeout         = 10 * time.Minute
	AssetResponseHeaderTimeout = 2 * time.Minute
	FeatureContentUpload       = "content_upload"
)

var (
	ErrAssetIntegrity = errors.New("asset_integrity_mismatch")
	ErrAssetRead      = errors.New("asset_source_read_failed")
	ErrAssetStorage   = errors.New("asset_storage_unavailable")
)

// ContentIdentity names immutable bytes, independently of their use by a job.
type ContentIdentity struct {
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

func (identity ContentIdentity) Validate() error {
	validSize := identity.Size >= 1 && identity.Size <= MaxAssetBytes
	if !IsSHA256(identity.SHA256) || !validSize {
		return invalid("content")
	}
	return nil
}

// AssetReceipt confirms durable content ownership by one node installation.
// It does not acknowledge job admission or asset execution support.
type AssetReceipt struct {
	Contract           int    `json:"contract"`
	NodeID             string `json:"node_id"`
	NodeInstallationID string `json:"node_installation_id"`
	SHA256             string `json:"sha256"`
	Size               int64  `json:"size"`
}

func (receipt AssetReceipt) Validate() error {
	if receipt.Contract != CurrentContract {
		return ErrUnsupported
	}
	if !domain.IsUUID(receipt.NodeID) || !domain.IsUUID(receipt.NodeInstallationID) {
		return invalid("asset_receipt.identity")
	}
	identity := ContentIdentity{SHA256: receipt.SHA256, Size: receipt.Size}
	if identity.Validate() != nil {
		return invalid("asset_receipt.content")
	}
	return nil
}

func DecodeAssetReceipt(reader io.Reader) (AssetReceipt, error) {
	if reader == nil {
		return AssetReceipt{}, invalid("asset_receipt")
	}
	document, err := io.ReadAll(io.LimitReader(reader, MaxAssetReceiptBytes+1))
	if err != nil {
		return AssetReceipt{}, err
	}
	if len(document) > MaxAssetReceiptBytes {
		return AssetReceipt{}, ErrTooLarge
	}
	if _, err := objectFields(document, []string{
		"contract", "node_id", "node_installation_id", "sha256", "size",
	}, []string{}); err != nil {
		return AssetReceipt{}, invalid("asset_receipt")
	}
	var receipt AssetReceipt
	if err := json.Unmarshal(document, &receipt); err != nil {
		return AssetReceipt{}, invalid("asset_receipt")
	}
	return receipt, receipt.Validate()
}

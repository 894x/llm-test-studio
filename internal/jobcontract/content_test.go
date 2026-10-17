package jobcontract_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/894x/llm-test-studio/internal/jobcontract"
)

func assetReceipt() jobcontract.AssetReceipt {
	return jobcontract.AssetReceipt{
		Contract:           jobcontract.CurrentContract,
		NodeID:             "11111111-1111-4111-8111-111111111111",
		NodeInstallationID: "22222222-2222-4222-8222-222222222222",
		SHA256:             strings.Repeat("a", 64), Size: 1,
	}
}

func TestContentIdentitySizeAndHashBoundaries(t *testing.T) {
	for _, size := range []int64{-1, 0, 1, 42, jobcontract.MaxAssetBytes, jobcontract.MaxAssetBytes + 1} {
		identity := jobcontract.ContentIdentity{SHA256: strings.Repeat("a", 64), Size: size}
		valid := size >= 1 && size <= jobcontract.MaxAssetBytes
		if (identity.Validate() == nil) != valid {
			t.Fatalf("size %d validation does not match current bounds", size)
		}
	}
	for _, hash := range []string{"", strings.Repeat("a", 63), strings.Repeat("A", 64), strings.Repeat("g", 64)} {
		if (jobcontract.ContentIdentity{SHA256: hash, Size: 1}).Validate() == nil {
			t.Fatal("accepted noncanonical content hash")
		}
	}
}

func TestAssetReceiptRequiresEveryCurrentFieldAndNoAliases(t *testing.T) {
	receipt := assetReceipt()
	document, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := jobcontract.DecodeAssetReceipt(bytes.NewReader(document))
	if err != nil || decoded != receipt {
		t.Fatalf("current receipt rejected: %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(document, &fields); err != nil {
		t.Fatal(err)
	}
	for field := range fields {
		for _, mode := range []string{"missing", "null", "wrong_type"} {
			t.Run(field+"/"+mode, func(t *testing.T) {
				changed := make(map[string]json.RawMessage, len(fields))
				for key, value := range fields {
					changed[key] = value
				}
				switch mode {
				case "missing":
					delete(changed, field)
				case "null":
					changed[field] = json.RawMessage("null")
				case "wrong_type":
					changed[field] = json.RawMessage("{}")
				}
				raw, _ := json.Marshal(changed)
				if _, err := jobcontract.DecodeAssetReceipt(bytes.NewReader(raw)); !errors.Is(err, jobcontract.ErrInvalid) {
					t.Fatalf("accepted malformed current receipt: %v", err)
				}
			})
		}
	}
	for _, raw := range []string{
		"null", "[]", string(document) + " {}",
		strings.Replace(string(document), `"size":1`, `"size":1,"size":1`, 1),
		strings.Replace(string(document), `"size":1`, `"size":1,"\u0073ize":1`, 1),
		strings.Replace(string(document), `"size":1`, `"Size":1`, 1),
		strings.Replace(string(document), `"size":1`, `"size":1,"path":"private"`, 1),
		strings.Replace(string(document), `"size":1`, `"size":1.0`, 1),
	} {
		if _, err := jobcontract.DecodeAssetReceipt(strings.NewReader(raw)); !errors.Is(err, jobcontract.ErrInvalid) {
			t.Fatalf("noncurrent envelope accepted: %v", err)
		}
	}
}

func TestAssetReceiptValidatesIdentityContractAndBoundedReader(t *testing.T) {
	for _, size := range []int64{1, jobcontract.MaxAssetBytes} {
		receipt := assetReceipt()
		receipt.Size = size
		document, _ := json.Marshal(receipt)
		padded := string(document) + strings.Repeat(" ", jobcontract.MaxAssetReceiptBytes-len(document))
		if _, err := jobcontract.DecodeAssetReceipt(strings.NewReader(padded)); err != nil {
			t.Fatal("valid receipt at the byte/size boundary rejected")
		}
		if _, err := jobcontract.DecodeAssetReceipt(strings.NewReader(padded + " ")); !errors.Is(err, jobcontract.ErrTooLarge) {
			t.Fatal("receipt larger than the byte boundary accepted")
		}
	}
	for _, change := range []func(*jobcontract.AssetReceipt){
		func(value *jobcontract.AssetReceipt) { value.NodeID = "node" },
		func(value *jobcontract.AssetReceipt) { value.NodeInstallationID = "installation" },
		func(value *jobcontract.AssetReceipt) { value.SHA256 = strings.Repeat("A", 64) },
		func(value *jobcontract.AssetReceipt) { value.Size = 0 },
		func(value *jobcontract.AssetReceipt) { value.Size = jobcontract.MaxAssetBytes + 1 },
	} {
		receipt := assetReceipt()
		change(&receipt)
		if !errors.Is(receipt.Validate(), jobcontract.ErrInvalid) {
			t.Fatal("invalid receipt identity accepted")
		}
	}
	receipt := assetReceipt()
	receipt.Contract = 0
	document, _ := json.Marshal(receipt)
	if _, err := jobcontract.DecodeAssetReceipt(bytes.NewReader(document)); !errors.Is(err, jobcontract.ErrUnsupported) {
		t.Fatal("unsupported contract was defaulted")
	}
	if _, err := jobcontract.DecodeAssetReceipt(nil); !errors.Is(err, jobcontract.ErrInvalid) {
		t.Fatal("nil source accepted")
	}
	reader := &receiptCountingReader{remaining: jobcontract.MaxAssetReceiptBytes + 100}
	if _, err := jobcontract.DecodeAssetReceipt(reader); !errors.Is(err, jobcontract.ErrTooLarge) {
		t.Fatal("oversized receipt accepted")
	}
	if reader.read != jobcontract.MaxAssetReceiptBytes+1 {
		t.Fatal("receipt decoder read past its fixed byte bound")
	}
	if _, err := jobcontract.DecodeAssetReceipt(receiptFailedReader{}); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatal("source failure lost")
	}
}

type receiptCountingReader struct{ remaining, read int }

func (reader *receiptCountingReader) Read(buffer []byte) (int, error) {
	count := min(len(buffer), reader.remaining)
	for index := range count {
		buffer[index] = ' '
	}
	reader.remaining -= count
	reader.read += count
	if count == 0 {
		return 0, io.EOF
	}
	return count, nil
}

type receiptFailedReader struct{}

func (receiptFailedReader) Read([]byte) (int, error) { return 0, io.ErrClosedPipe }

package casecodec

import (
	"crypto/sha1"
	"encoding/hex"
)

// Keep the original namespace so existing Case, Suite and Plan references remain
// valid after retiring the database import service.
const identityNamespace = "builtin.cases/v2"

func stableCaseID(sourceKey string) string {
	namespace := [16]byte{0x76, 0x80, 0x78, 0x2d, 0x7a, 0xe8, 0x55, 0x8b, 0x9f, 0x32, 0x17, 0xd1, 0x3f, 0x31, 0xa6, 0x6b}
	hash := sha1.New()
	_, _ = hash.Write(namespace[:])
	_, _ = hash.Write([]byte(identityNamespace + "/" + sourceKey))
	value := hash.Sum(nil)[:16]
	value[6] = (value[6] & 0x0f) | 0x50
	value[8] = (value[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(value)
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32]
}

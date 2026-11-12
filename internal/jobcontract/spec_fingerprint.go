package jobcontract

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"hash"
	"io"
	"math"
	"reflect"
	"sort"
	"time"
)

// Fingerprint the complete current typed content without JSON re-encoding.
// This is process-local proof bookkeeping, never the published job digest.
func specFingerprint(spec JobSpec) ([sha256.Size]byte, error) {
	h := sha256.New()
	if err := fingerprintValue(h, reflect.ValueOf(spec)); err != nil {
		return [sha256.Size]byte{}, err
	}
	var result [sha256.Size]byte
	copy(result[:], h.Sum(nil))
	return result, nil
}
func fingerprintNumber(h hash.Hash, value uint64) {
	var raw [8]byte
	binary.LittleEndian.PutUint64(raw[:], value)
	_, _ = h.Write(raw[:])
}
func fingerprintString(h hash.Hash, value string) {
	fingerprintNumber(h, uint64(len(value)))
	_, _ = io.WriteString(h, value)
}
func fingerprintValue(h hash.Hash, value reflect.Value) error {
	if !value.IsValid() {
		fingerprintNumber(h, 0)
		return nil
	}
	fingerprintNumber(h, uint64(value.Kind())+1)
	if value.Type() == reflect.TypeOf(time.Time{}) {
		fingerprintString(h, value.Interface().(time.Time).String())
		return nil
	}
	switch value.Kind() {
	case reflect.Bool:
		if value.Bool() {
			fingerprintNumber(h, 1)
		} else {
			fingerprintNumber(h, 0)
		}
	case reflect.String:
		fingerprintString(h, value.String())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		fingerprintNumber(h, uint64(value.Int()))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		fingerprintNumber(h, value.Uint())
	case reflect.Float32, reflect.Float64:
		fingerprintNumber(h, math.Float64bits(value.Float()))
	case reflect.Pointer, reflect.Interface:
		if value.IsNil() {
			fingerprintNumber(h, 0)
			return nil
		}
		fingerprintNumber(h, 1)
		if value.Kind() == reflect.Interface {
			fingerprintString(h, value.Elem().Type().String())
		}
		return fingerprintValue(h, value.Elem())
	case reflect.Struct:
		for index := range value.NumField() {
			field := value.Type().Field(index)
			if field.PkgPath != "" {
				continue
			}
			fingerprintString(h, field.Name)
			if err := fingerprintValue(h, value.Field(index)); err != nil {
				return err
			}
		}
	case reflect.Slice, reflect.Array:
		if value.Kind() == reflect.Slice && value.IsNil() {
			fingerprintNumber(h, 0)
			return nil
		}
		fingerprintNumber(h, 1)
		fingerprintNumber(h, uint64(value.Len()))
		if value.Kind() == reflect.Slice && value.Type().Elem().Kind() == reflect.Uint8 {
			_, _ = h.Write(value.Bytes())
			return nil
		}
		for index := range value.Len() {
			if err := fingerprintValue(h, value.Index(index)); err != nil {
				return err
			}
		}
	case reflect.Map:
		if value.IsNil() {
			fingerprintNumber(h, 0)
			return nil
		}
		fingerprintNumber(h, 1)
		if value.Type().Key().Kind() != reflect.String {
			return fmt.Errorf("unsupported fingerprint map key")
		}
		keys := value.MapKeys()
		sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
		fingerprintNumber(h, uint64(len(keys)))
		for _, key := range keys {
			fingerprintString(h, key.String())
			if err := fingerprintValue(h, value.MapIndex(key)); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("unsupported fingerprint value: %s", value.Kind())
	}
	return nil
}

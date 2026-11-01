package dns

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// decodeMockCreds is a strict decoder for mock credentials (no unknown
// fields — typos surface as errors, not silent no-ops).
func decodeMockCreds(raw []byte) (MockCredentials, error) {
	var c MockCredentials
	if len(raw) == 0 {
		return c, fmt.Errorf("%w: kimlik bilgisi boş", ErrUnknownKind)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return c, fmt.Errorf("%w: kimlik bilgisi şeması uyumsuz: %v", ErrUnknownKind, err)
	}
	return c, nil
}

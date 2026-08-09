// Package traceid generates short trace IDs for operation tracking.
package traceid

import (
	"crypto/rand"
	"fmt"
)

// Generate returns a new trace ID in the format "tr_" + 8 hex characters.
func Generate() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "tr_00000000"
	}
	return fmt.Sprintf("tr_%x", b)
}

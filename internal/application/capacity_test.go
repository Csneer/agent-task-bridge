package application

import (
	"strings"
	"testing"
)

func TestEncodedResultAndBatchLimitIsInclusive(t *testing.T) {
	// Both CommandResult and CommitBatch pass through this exact encoded-byte
	// gate before any writes. JSON quotation adds two bytes to the string.
	if e := saneSize(strings.Repeat("x", 1048576-2)); e != nil {
		t.Fatal(e)
	}
	if e := saneSize(strings.Repeat("x", 1048576-1)); e == nil || errorOf(e).Data.Kind != "RESOURCE_LIMIT" {
		t.Fatal("oversized value accepted", e)
	}
}

// Package fixtures builds public protocol inputs. It never mutates daemon storage.
package fixtures

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"time"
)

const (
	Authority  = "authority_test"
	Owner      = "principal_O"
	PrincipalA = "principal_A"
	PrincipalB = "principal_B"
	Acceptance = "principal_X"
	Reader     = "principal_R"
	AgentA     = "agent_A"
	AgentB     = "agent_B"
)

var StartTime = time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)

func Timestamp(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }

// Key returns a fresh UUIDv4 without introducing a production UUID dependency.
func Key() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// Token generates disposable fixture credentials rather than committing secrets.
func Token() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b[:])
}

func ImmutableRef(uri, mediaType string, content []byte) map[string]any {
	digest := sha256.Sum256(content)
	return map[string]any{"kind": "immutable", "uri": uri, "media_type": mediaType,
		"version": "1", "sha256": hex.EncodeToString(digest[:])}
}

func Contract(eligible []string, recovery string, rework bool) map[string]any {
	return map[string]any{
		"profile": "delivery-acceptance-v1", "owner_principal_id": Owner,
		"goal":        "Review the fixed technical proposal and return an evidence-backed review.",
		"constraints": []string{"Produce a candidate artifact; do not perform external mutations."},
		"input_refs":  []any{}, "expected_output": "A review artifact with findings and cited evidence.",
		"acceptance_refs": []any{}, "context_refs": []any{},
		"eligible_agent_ids": eligible, "acceptance_principal_id": Acceptance,
		"recovery_policy": recovery, "allow_rework": rework,
		"lease_policy": map[string]any{"min_seconds": 5, "max_seconds": 300},
		"extensions":   map[string]any{},
	}
}

func Grant(principal string, agent any, actions []string, until time.Time) map[string]any {
	return map[string]any{"subject_principal_id": principal, "agent_id": agent,
		"actions": actions, "not_before": Timestamp(StartTime.Add(-time.Hour)), "expires_at": Timestamp(until)}
}

func Grants(eligible []string) []any {
	until := StartTime.Add(24 * time.Hour)
	grants := []any{
		Grant(Acceptance, nil, []string{"task.read", "submission.decide"}, until),
		Grant(Reader, nil, []string{"task.read"}, until),
		Grant(PrincipalA, nil, []string{"task.read", "task.link", "task.publish"}, until),
		Grant(PrincipalB, nil, []string{"task.read"}, until),
	}
	for _, agent := range eligible {
		principal := PrincipalB
		if agent == AgentA {
			principal = PrincipalA
		}
		grants = append(grants, Grant(principal, agent, []string{"task.read", "task.claim"}, until))
	}
	return grants
}

func Create(eligible []string, recovery string, rework bool) map[string]any {
	return map[string]any{"contract": Contract(eligible, recovery, rework),
		"initial_grants": Grants(eligible), "idempotency_key": Key()}
}

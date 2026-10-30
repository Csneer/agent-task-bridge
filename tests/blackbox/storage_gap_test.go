package blackbox

import (
	"github.com/Csneer/agent-task-bridge/tests/fixtures"
	"database/sql"
	"testing"
)

// This is storage-corruption fault injection, not a way to create business fixtures.
// All legitimate facts originate through HTTP; all recovery assertions use HTTP.
func TestV10StorageGapRequiresReinitialization(t *testing.T) {
	for _, fault := range []string{"interior", "tail", "all"} {
		t.Run(fault, func(t *testing.T) {
			h := newHarness(t)
			f := h.active("safe_reopen", true)
			opened := h.ok(fixtures.Owner, "events.open", map[string]any{"scope": map[string]any{}})
			cursor := opened["cursor"]
			if fault != "interior" {
				page := h.ok(fixtures.Owner, "events.read", map[string]any{"cursor": cursor, "limit": 100})
				cursor = page["next_cursor"]
			}
			h.server.Close()
			h.server = nil
			if err := h.service.Close(); err != nil {
				t.Fatal(err)
			}
			h.service = nil
			db, err := sql.Open("sqlite", h.path)
			if err != nil {
				t.Fatal(err)
			}
			query := "DELETE FROM batches WHERE seq = (SELECT MIN(seq) FROM batches)"
			if fault == "tail" {
				query = "DELETE FROM batches WHERE seq = (SELECT MAX(seq) FROM batches)"
			}
			if fault == "all" {
				query = "DELETE FROM batches"
			}
			result, err := db.Exec(query)
			if err != nil {
				db.Close()
				t.Fatal(err)
			}
			changed, err := result.RowsAffected()
			if err != nil || changed == 0 {
				db.Close()
				t.Fatal("fault did not remove a batch")
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			h.open(nil)
			h.kind(h.call(fixtures.Owner, "events.read", map[string]any{"cursor": cursor, "limit": 100}), "REINITIALIZE_REQUIRED")
			if h.read(f.id)["state"] != "active" {
				t.Fatal("fault unexpectedly changed canonical Task")
			}
		})
	}
}

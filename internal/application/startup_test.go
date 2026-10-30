package application_test

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Csneer/agent-task-bridge/bridge"
	"github.com/Csneer/agent-task-bridge/tests/fixtures"
)

func TestReopenRejectsUnsupportedVersionsWithoutRelabeling(t *testing.T) {
	for _, kind := range []string{"protocol", "storage"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "bridge.sqlite")
			cfg, _ := fixtures.Bootstrap()
			opts := bridge.Options{Key: make([]byte, 32), DisableMaintenance: true, Clock: func() time.Time { return fixtures.StartTime }}
			s, err := bridge.Open(path, &cfg, opts)
			if err != nil {
				t.Fatal(err)
			}
			if err = s.Close(); err != nil {
				t.Fatal(err)
			}
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			statement := "UPDATE meta SET value='future-version' WHERE key='protocol_version'"
			if kind == "storage" {
				statement = "PRAGMA user_version=99"
			}
			if _, err = db.Exec(statement); err != nil {
				t.Fatal(err)
			}
			if s, err = bridge.Open(path, &cfg, opts); err == nil {
				s.Close()
				t.Fatal("unsupported version silently accepted")
			}
			if kind == "protocol" {
				var version string
				if err = db.QueryRow("SELECT value FROM meta WHERE key='protocol_version'").Scan(&version); err != nil || version != "future-version" {
					t.Fatal("version was relabeled", err)
				}
			} else {
				var version int
				if err = db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 99 {
					t.Fatal("schema version was relabeled", err)
				}
			}
		})
	}
}

func TestEmptyAuthorityStillPersistsClockLowerBound(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bridge.sqlite")
	cfg, _ := fixtures.Bootstrap()
	opts := bridge.Options{Key: make([]byte, 32), DisableMaintenance: true, Clock: func() time.Time { return fixtures.StartTime }}
	s, err := bridge.Open(path, &cfg, opts)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	opts.Clock = func() time.Time { return fixtures.StartTime.Add(-2 * time.Second) }
	if s, err = bridge.Open(path, nil, opts); err == nil {
		s.Close()
		t.Fatal("backward clock accepted on empty authority")
	} else if !strings.Contains(err.Error(), "UNAVAILABLE") {
		t.Fatal(err)
	}
}

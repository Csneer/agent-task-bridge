package blackbox

import (
	"context"
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Csneer/agent-task-bridge/bridge"
	"github.com/Csneer/agent-task-bridge/tests/fixtures"
	"net/http/httptest"
)

// TestCrashHost runs only in a child test process explicitly created below.
// It exits at a known real transaction point; no synthetic database edits occur.
func TestCrashHost(t *testing.T) {
	if os.Getenv("BRIDGE_TEST_CRASH_HOST") != "1" {
		return
	}
	key, err := base64.RawStdEncoding.DecodeString(os.Getenv("BRIDGE_TEST_KEY"))
	if err != nil {
		t.Fatal(err)
	}
	point := os.Getenv("BRIDGE_TEST_POINT")
	s, err := bridge.Open(os.Getenv("BRIDGE_TEST_DB"), nil, bridge.Options{Key: key, Clock: func() time.Time { return fixtures.StartTime }, DisableMaintenance: true, Hook: func(e bridge.HookEvent) error {
		if e.Method == "run.submit" && e.Point == point {
			if err := os.WriteFile(os.Getenv("BRIDGE_TEST_MARKER"), []byte(e.Point), 0600); err != nil {
				os.Exit(72)
			}
			os.Exit(71)
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	server := httptest.NewServer(s.Handler())
	defer server.Close()
	if err := os.WriteFile(os.Getenv("BRIDGE_TEST_ADDRESS"), []byte(server.URL), 0600); err != nil {
		t.Fatal(err)
	}
	select {}
}

func TestV22CrashBeforeAndAfterCommit(t *testing.T) {
	for _, point := range []string{"before_commit", "after_commit_before_response"} {
		t.Run(point, func(t *testing.T) {
			h := newHarness(t)
			f := h.active("safe_reopen", true)
			client := h.client(f.actor)
			request, err := client.Prepare("run.submit", f.submitParams())
			if err != nil {
				t.Fatal(err)
			}
			h.server.Close()
			h.server = nil
			if err := h.service.Close(); err != nil {
				t.Fatal(err)
			}
			h.service = nil
			dir := t.TempDir()
			address, marker := filepath.Join(dir, "address"), filepath.Join(dir, "point")
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCrashHost$", "-test.timeout=12s")
			cmd.Env = append(os.Environ(), "BRIDGE_TEST_CRASH_HOST=1", "BRIDGE_TEST_KEY="+base64.RawStdEncoding.EncodeToString(h.key), "BRIDGE_TEST_POINT="+point, "BRIDGE_TEST_DB="+h.path, "BRIDGE_TEST_MARKER="+marker, "BRIDGE_TEST_ADDRESS="+address)
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if cmd.ProcessState == nil {
					_ = cmd.Process.Kill()
					_ = cmd.Wait()
				}
			}()
			for {
				raw, err := os.ReadFile(address)
				if err == nil {
					client.URL = string(raw)
					break
				}
				select {
				case <-ctx.Done():
					t.Fatal("crash host not ready")
				case <-time.After(10 * time.Millisecond):
				}
			}
			_, err = client.Do(ctx, request)
			if err == nil {
				t.Fatal("expected connection loss from controlled crash")
			}
			err = cmd.Wait()
			if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 71 {
				t.Fatalf("child did not exit at requested hook: %v", err)
			}
			observed, err := os.ReadFile(marker)
			if err != nil || string(observed) != point {
				t.Fatal("crash point not observed")
			}
			h.open(nil)
			client.URL = h.server.URL
			if point == "before_commit" {
				h.state(f.id, "active")
				if h.countEvent(f.id, "submission.created") != 0 {
					t.Fatal("uncommitted submission survived crash")
				}
			} else {
				h.state(f.id, "submitted")
				if h.countEvent(f.id, "submission.created") != 1 {
					t.Fatal("committed submission missing after crash")
				}
			}
			recovered, err := client.Do(context.Background(), request)
			if err != nil || recovered.Error != nil {
				t.Fatalf("recover: %v %v", err, recovered.Error)
			}
			if recovered.Replayed != (point == "after_commit_before_response") {
				t.Fatal("wrong recovery receipt state")
			}
			h.state(f.id, "submitted")
			if h.countEvent(f.id, "submission.created") != 1 {
				t.Fatal("recovery duplicated submission")
			}
		})
	}
}

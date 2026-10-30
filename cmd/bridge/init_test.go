package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestInitPrivateAndExclusive(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "private")
	var output bytes.Buffer
	if err := initialize([]string{"--dir", dir}, &output); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"", "bridge.key", "bridge.sqlite", "admin.json", "credentials.json", "bootstrap.json"} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		want := os.FileMode(0600)
		if name == "" {
			want = 0700
		}
		if info.Mode().Perm() != want {
			t.Fatalf("%s mode %o", name, info.Mode().Perm())
		}
	}
	raw, err := os.ReadFile(filepath.Join(dir, "credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	var tokens map[string]string
	if err = json.Unmarshal(raw, &tokens); err != nil {
		t.Fatal(err)
	}
	admin, err := os.ReadFile(filepath.Join(dir, "admin.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(tokens) != 3 {
		t.Fatal("expected three identities")
	}
	for _, token := range tokens {
		if len(token) != 43 || strings.Contains(output.String(), token) || bytes.Contains(admin, []byte(token)) {
			t.Fatal("token malformed or leaked")
		}
	}
	if bytes.Contains(admin, []byte(`"token":`)) {
		t.Fatal("admin contains raw token field")
	}
	key, _ := os.ReadFile(filepath.Join(dir, "bridge.key"))
	if err := initialize([]string{"--dir", dir}, &output); err == nil {
		t.Fatal("existing directory accepted")
	}
	after, _ := os.ReadFile(filepath.Join(dir, "bridge.key"))
	if !bytes.Equal(key, after) {
		t.Fatal("key replaced")
	}
	empty := filepath.Join(t.TempDir(), "empty")
	if err = os.Mkdir(empty, 0700); err != nil {
		t.Fatal(err)
	}
	if err = initialize([]string{"--dir", empty}, &output); err == nil {
		t.Fatal("empty existing directory accepted")
	}
	link := filepath.Join(t.TempDir(), "link")
	if err = os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	if err = initialize([]string{"--dir", link}, &output); err == nil {
		t.Fatal("symlink accepted")
	}
	invalid := filepath.Join(t.TempDir(), "invalid")
	if err = initialize([]string{"--dir", invalid, "--valid-for", "0"}, &output); err == nil {
		t.Fatal("zero lifetime accepted")
	}
	if _, err = os.Stat(invalid); !os.IsNotExist(err) {
		t.Fatal("invalid setup created target")
	}
}

func TestInitFailureDoesNotInstallTarget(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "private")
	var output bytes.Buffer
	// Sub-millisecond validity truncates to an empty interval and is rejected
	// during configuration import, after staging files have been created.
	if err := initialize([]string{"--dir", dir, "--valid-for", "1ns"}, &output); err == nil {
		t.Fatal("invalid interval succeeded")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("partial target installed")
	}
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || !strings.HasPrefix(entries[0].Name(), ".bridge-init-") {
		t.Fatal("missing identifiable staging directory")
	}
	info, err := entries[0].Info()
	if err != nil || info.Mode().Perm() != 0700 {
		t.Fatal("staging is not private")
	}
	if output.Len() != 0 {
		t.Fatal("failure emitted success output")
	}
}

func TestInitConcurrentExclusive(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "private")
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { var out bytes.Buffer; results <- initialize([]string{"--dir", dir}, &out) }()
	}
	successes := 0
	for i := 0; i < 2; i++ {
		if <-results == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("successful initializers: %d", successes)
	}
}

func TestInitCLIAndMinimalHTTP(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("Node required for public HTTP minimal walkthrough")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "bridge")
	build := exec.Command("go", "build", "-ldflags", "-X main.version=v-test -X main.commit=test-commit -X main.buildDate=test-date", "-o", binary, "./cmd/bridge")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %s: %v", out, err)
	}
	for _, arg := range []string{"version", "--version", "--help"} {
		out, err := exec.Command(binary, arg).CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %s %v", arg, out, err)
		}
		if arg != "--help" && !strings.Contains(string(out), "v-test commit=test-commit built=test-date") {
			t.Fatal("missing version metadata")
		}
	}
	dir := filepath.Join(t.TempDir(), "private")
	if out, err := exec.Command(binary, "init", "--dir", dir).CombinedOutput(); err != nil {
		t.Fatalf("init: %s %v", out, err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	daemon := exec.Command(binary, "--db", filepath.Join(dir, "bridge.sqlite"), "--key", filepath.Join(dir, "bridge.key"), "--listen", address, "--log-format", "json")
	var logs bytes.Buffer
	daemon.Stdout = &logs
	daemon.Stderr = &logs
	if err = daemon.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- daemon.Wait() }()
	defer func() {
		daemon.Process.Signal(syscall.SIGTERM)
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("daemon shutdown: %v", err)
			}
		case <-time.After(12 * time.Second):
			daemon.Process.Kill()
			<-done
			t.Error("daemon shutdown timed out")
		}
		// Inspect only after Wait: process output copying has finished.
		requests, stopped := 0, false
		for _, line := range bytes.Split(bytes.TrimSpace(logs.Bytes()), []byte("\n")) {
			var entry map[string]any
			if err := json.Unmarshal(line, &entry); err != nil {
				t.Errorf("non-JSON runtime log: %v", err)
				continue
			}
			if entry["msg"] == "request completed" {
				requests++
				if entry["method"] == nil || entry["status"] != float64(200) || entry["error_kind"] != "" || entry["duration_ms"] == nil {
					t.Error("incomplete request observation")
				}
			}
			if entry["msg"] == "bridge stopped" {
				stopped = true
				if entry["requests"] != float64(requests) || entry["errors"] != float64(0) {
					t.Error("shutdown counters do not match completed requests")
				}
			}
		}
		if requests == 0 || !stopped {
			t.Error("missing request or shutdown observations")
		}
		raw, _ := os.ReadFile(filepath.Join(dir, "credentials.json"))
		var tokens map[string]string
		json.Unmarshal(raw, &tokens)
		for _, token := range tokens {
			if bytes.Contains(logs.Bytes(), []byte(token)) {
				t.Error("runtime log leaked credentials")
			}
		}
	}()
	client := http.Client{Timeout: time.Second}
	ready := false
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		response, e := client.Get("http://" + address + "/ready")
		if e == nil {
			response.Body.Close()
			if response.StatusCode == 204 {
				ready = true
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !ready {
		t.Fatal("daemon did not become ready")
	}
	ids := map[string]bool{}
	for i := 0; i < 2; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		command := exec.CommandContext(ctx, "node", filepath.Join(root, "examples/minimal/run.mjs"), "--dir", dir, "--url", "http://"+address)
		out, err := command.CombinedOutput()
		cancel()
		if err != nil {
			t.Fatalf("minimal HTTP: %s %v", out, err)
		}
		var result struct {
			TaskID  string   `json:"task_id"`
			State   string   `json:"state"`
			History []string `json:"history_actions"`
		}
		if err = json.Unmarshal(out, &result); err != nil {
			t.Fatal(fmt.Sprintf("invalid example output: %s", out))
		}
		if result.State != "accepted" || len(result.History) != 6 || ids[result.TaskID] {
			t.Fatal("incomplete or repeated task")
		}
		ids[result.TaskID] = true
		raw, _ := os.ReadFile(filepath.Join(dir, "credentials.json"))
		var tokens map[string]string
		json.Unmarshal(raw, &tokens)
		for _, token := range tokens {
			if bytes.Contains(out, []byte(token)) {
				t.Fatal("example leaked credentials")
			}
		}
	}
}

package bridgeclient

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var ErrUnknown = errors.New("result unknown; retry immutable journal request")
var ErrConflict = errors.New("journal key already binds another request")

type RPCError struct {
	Code json.Number
	Kind string
}

func (e *RPCError) Error() string { return "RPC rejected: " + string(e.Code) + " " + e.Kind }

type Client struct {
	URL, Token, Agent, Authority, Journal string
	HTTP                                  *http.Client
	authorityMu                           sync.RWMutex
}
type Outcome struct {
	Key      string
	Response []byte
}

func New(endpoint, token, agent, journal string) (*Client, error) {
	u, e := url.Parse(endpoint)
	if e != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("invalid bridge URL")
	}
	if u.Scheme != "https" {
		ip := net.ParseIP(u.Hostname())
		if u.Scheme != "http" || (u.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback())) {
			return nil, errors.New("TLS required except loopback")
		}
	}
	if token == "" || journal == "" {
		return nil, errors.New("credential and private journal required")
	}
	if e = os.MkdirAll(journal, 0700); e != nil {
		return nil, e
	}
	info, e := os.Lstat(journal)
	if e != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("journal must be private directory")
	}
	endpoint = strings.TrimRight(endpoint, "/")
	if !strings.HasSuffix(endpoint, "/rpc") {
		endpoint += "/rpc"
	}
	return &Client{URL: endpoint, Token: token, Agent: agent, Journal: journal, HTTP: &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func UUID() string {
	b := make([]byte, 16)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}
func isWrite(method string) bool {
	switch method {
	case "task.create", "task.createChild", "task.updateDraft", "task.publish", "task.claim", "assignment.renew", "assignment.release", "assignment.revoke", "run.start", "run.report", "run.submit", "task.accept", "task.reject", "task.reopen", "task.cancel", "task.close", "grant.issue", "grant.revoke":
		return true
	}
	return false
}
func (c *Client) Discover(ctx context.Context) error {
	out, e := c.Call(ctx, "bridge.capabilities", map[string]any{})
	if e != nil {
		return e
	}
	v, e := Parse(out.Response)
	if e != nil {
		return e
	}
	data := v.(map[string]any)["result"].(map[string]any)["data"].(map[string]any)
	if e = Validate("Capabilities", data); e != nil {
		return errors.New("incompatible capabilities")
	}
	authority := data["authority_id"].(string)
	c.authorityMu.Lock()
	defer c.authorityMu.Unlock()
	if c.Authority != "" && c.Authority != authority {
		return errors.New("authority changed")
	}
	c.Authority = authority
	return nil
}
func (c *Client) Call(ctx context.Context, method string, params map[string]any) (Outcome, error) {
	// Snapshot caller data, so later mutations cannot alter an in-flight request.
	raw, e := Canonical(params)
	if e != nil {
		return Outcome{}, e
	}
	v, e := Parse(raw)
	if e != nil {
		return Outcome{}, e
	}
	p := v.(map[string]any)
	if method != "bridge.capabilities" {
		if c.authorityID() == "" {
			if e = c.Discover(ctx); e != nil {
				return Outcome{}, e
			}
		}
		if a, ok := p["authority_id"]; ok && a != c.authorityID() {
			return Outcome{}, errors.New("authority mismatch")
		}
		p["authority_id"] = c.authorityID()
		if c.Agent != "" {
			if _, ok := p["as_agent_id"]; !ok {
				p["as_agent_id"] = c.Agent
			}
		}
	}
	key := ""
	if isWrite(method) {
		if k, ok := p["idempotency_key"].(string); ok {
			key = k
		} else {
			key = UUID()
			p["idempotency_key"] = key
		}
	}
	request := map[string]any{"jsonrpc": "2.0", "id": UUID(), "method": method, "params": p}
	if e = Validate("Request", request); e != nil {
		return Outcome{Key: key}, errors.New("request violates M1 schema")
	}
	raw, e = Canonical(request)
	if e != nil {
		return Outcome{Key: key}, e
	}
	if key != "" {
		raw, e = c.persist(key, raw)
		if e != nil {
			return Outcome{Key: key}, e
		}
	}
	return c.send(ctx, key, raw)
}
func (c *Client) persist(key string, raw []byte) ([]byte, error) {
	if e := Validate("Key", key); e != nil {
		return nil, e
	}
	path := filepath.Join(c.Journal, key+".request.json")
	// Link is atomic and never overwrites an existing immutable request.
	if e := privateCreate(path, raw); e != nil && !errors.Is(e, os.ErrExist) {
		return nil, e
	}
	old, e := os.ReadFile(path)
	if e != nil {
		return nil, e
	}
	a, e := Parse(old)
	if e != nil {
		return nil, e
	}
	b, e := Parse(raw)
	if e != nil {
		return nil, e
	}
	ma, mb := a.(map[string]any), b.(map[string]any)
	ha, e := RequestHash(ma["method"].(string), ma["params"].(map[string]any))
	if e != nil {
		return nil, e
	}
	hb, e := RequestHash(mb["method"].(string), mb["params"].(map[string]any))
	if e != nil {
		return nil, e
	}
	if ha != hb {
		return nil, ErrConflict
	}
	return old, nil
}
func (c *Client) Retry(ctx context.Context, key string) (Outcome, error) {
	if e := Validate("Key", key); e != nil {
		return Outcome{}, e
	}
	raw, e := os.ReadFile(filepath.Join(c.Journal, key+".request.json"))
	if e != nil {
		return Outcome{Key: key}, e
	}
	v, e := Parse(raw)
	if e != nil {
		return Outcome{Key: key}, e
	}
	if e = Validate("Request", v); e != nil {
		return Outcome{Key: key}, e
	}
	request := v.(map[string]any)
	p := request["params"].(map[string]any)
	if !isWrite(request["method"].(string)) || p["idempotency_key"] != key {
		return Outcome{Key: key}, errors.New("invalid journal request")
	}
	if c.authorityID() == "" {
		if e = c.Discover(ctx); e != nil {
			return Outcome{Key: key}, e
		}
	}
	if p["authority_id"] != c.authorityID() {
		return Outcome{Key: key}, errors.New("authority mismatch")
	}
	return c.send(ctx, key, raw)
}
func (c *Client) send(ctx context.Context, key string, raw []byte) (Outcome, error) {
	out := Outcome{Key: key}
	if len(raw) > 262144 {
		return out, errors.New("request exceeds M1 limit")
	}
	requestValue, e := Parse(raw)
	if e != nil {
		return out, e
	}
	request := requestValue.(map[string]any)
	if key != "" {
		if e = privateCreate(filepath.Join(c.Journal, key+".unknown"), []byte("A send may have committed. Only success resolves this; refusal does not.\n")); e != nil && !errors.Is(e, os.ErrExist) {
			return out, e
		}
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, c.URL, bytes.NewReader(raw))
	if e != nil {
		return out, e
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	response, e := c.HTTP.Do(req)
	if e != nil {
		return out, ErrUnknown
	}
	defer response.Body.Close()
	body, e := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if e != nil || len(body) > maxResponseBytes {
		return out, ErrUnknown
	}
	if response.StatusCode != 200 {
		if response.StatusCode >= 500 {
			return out, ErrUnknown
		}
		return out, fmt.Errorf("transport rejected: HTTP %d", response.StatusCode)
	}
	value, e := Parse(body)
	if e != nil {
		return out, ErrUnknown
	}
	m, ok := value.(map[string]any)
	if !ok || m["id"] != request["id"] {
		return out, ErrUnknown
	}
	if rpc, ok := m["error"].(map[string]any); ok {
		if Validate("ErrorResponse", m) != nil {
			return out, ErrUnknown
		}
		code := rpc["code"].(json.Number)
		if code == "-32603" || code == "-32015" {
			return out, ErrUnknown
		}
		kind := ""
		if data, ok := rpc["data"].(map[string]any); ok {
			kind, _ = data["kind"].(string)
		}
		out.Response = body
		return out, &RPCError{Code: code, Kind: kind}
	}
	if Validate("SuccessResponse", m) != nil {
		return out, ErrUnknown
	}
	result := m["result"].(map[string]any)
	meta := result["meta"].(map[string]any)
	if request["method"] != "bridge.capabilities" && meta["authority_id"] != c.authorityID() {
		return out, ErrUnknown
	}
	definition := "CommandResult"
	switch request["method"] {
	case "bridge.capabilities":
		definition = "Capabilities"
	case "task.get":
		definition = "Task"
	case "record.get":
		definition = "Record"
	case "task.discover":
		definition = "DiscoverResult"
	case "snapshot.open":
		definition = "SnapshotOpenResult"
	case "snapshot.page":
		definition = "SnapshotPageResult"
	case "events.open":
		definition = "EventOpenResult"
	case "events.read":
		definition = "ReplayResult"
	}
	if Validate(definition, result["data"]) != nil {
		return out, ErrUnknown
	}
	out.Response = body
	if key != "" {
		if e = privateCreate(filepath.Join(c.Journal, key+".success.json"), body); e != nil && !errors.Is(e, os.ErrExist) {
			return out, ErrUnknown
		}
	}
	return out, nil
}

// PrivateWrite writes sensitive output atomically without following destination symlinks.
func PrivateWrite(path string, data []byte) error { return privateCreate(path, data) }
func privateCreate(path string, data []byte) error {
	dir := filepath.Dir(path)
	f, e := os.CreateTemp(dir, ".bridge-")
	if e != nil {
		return e
	}
	name := f.Name()
	defer os.Remove(name)
	if _, e = f.Write(data); e != nil {
		f.Close()
		return e
	}
	if e = f.Sync(); e != nil {
		f.Close()
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	if e = os.Link(name, path); e != nil {
		return e
	}
	d, e := os.Open(dir)
	if e != nil {
		return e
	}
	defer d.Close()
	return d.Sync()
}

// authorityID synchronizes discovery with all concurrent protocol operations.
func (c *Client) authorityID() string {
	c.authorityMu.RLock()
	defer c.authorityMu.RUnlock()
	return c.Authority
}

// 100 full 1 MiB commit batches plus bounded JSON-RPC/cursor envelope headroom.
const maxResponseBytes = 101 * 1024 * 1024

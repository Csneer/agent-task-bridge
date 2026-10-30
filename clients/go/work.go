package bridgeclient

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"time"
)

// Supervise gates work on a current fenced command, then requires repeated
// successful fencing/renewal. Any unknown result cancels cooperative work.
// The callback owns revision/lease state and must issue run.start, run.report,
// or a strictly extending assignment.renew, never merely task.get.
func Supervise(ctx context.Context, interval time.Duration, fence func(context.Context) error, work func(context.Context) error) error {
	if interval <= 0 {
		return errors.New("positive lease interval required")
	}
	child, cancel := context.WithCancel(ctx)
	defer cancel()
	if e := fence(child); e != nil {
		return e
	}
	finished := make(chan error, 1)
	go func() { finished <- work(child) }()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case e := <-finished:
			return e
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if e := fence(child); e != nil {
				cancel()
				return e
			}
		}
	}
}

// ReadImmutable fetches only explicit file/http(s) resources, without forwarding
// bridge credentials, and verifies byte identity before returning content.
func ReadImmutable(ctx context.Context, ref map[string]any, maxBytes int64) ([]byte, error) {
	if e := Validate("ImmutableRef", ref); e != nil {
		return nil, errors.New("invalid immutable reference")
	}
	if maxBytes <= 0 {
		return nil, errors.New("positive resource limit required")
	}
	u, e := url.Parse(ref["uri"].(string))
	if e != nil {
		return nil, e
	}
	var reader io.ReadCloser
	switch u.Scheme {
	case "file":
		if u.Host != "" && u.Host != "localhost" {
			return nil, errors.New("nonlocal file reference")
		}
		reader, e = os.Open(u.Path)
	case "http", "https":
		if e = resourceURL(u); e != nil {
			return nil, e
		}
		client := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("too many redirects")
			}
			return resourceURL(req.URL)
		}}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return nil, err
		}
		response, err := client.Do(req)
		if err != nil {
			return nil, errors.New("resource fetch failed")
		}
		if response.StatusCode != 200 {
			response.Body.Close()
			return nil, errors.New("resource returned non-success")
		}
		reader = response.Body
	default:
		return nil, errors.New("unsupported resource scheme")
	}
	if e != nil {
		return nil, e
	}
	defer reader.Close()
	raw, e := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	if e != nil {
		return nil, e
	}
	if int64(len(raw)) > maxBytes {
		return nil, errors.New("resource exceeds limit")
	}
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != ref["sha256"] {
		return nil, errors.New("resource digest mismatch")
	}
	return raw, nil
}
func resourceURL(u *url.URL) error {
	if u.User != nil {
		return errors.New("resource credentials forbidden")
	}
	if u.Scheme == "https" {
		return nil
	}
	ip := net.ParseIP(u.Hostname())
	if u.Scheme == "http" && (u.Hostname() == "localhost" || (ip != nil && ip.IsLoopback())) {
		return nil
	}
	return errors.New("resource TLS required except loopback")
}

// SuperviseLease additionally enforces the last confirmed server deadline while
// a renewal is in flight. A stalled network cannot keep work running past it.
// margin covers local clock/network uncertainty; clocks must be managed as in M1.
func SuperviseLease(ctx context.Context, interval, margin time.Duration, fence func(context.Context) (time.Time, error), work func(context.Context) error) error {
	if interval <= 0 || margin < 0 {
		return errors.New("invalid supervision timing")
	}
	child, cancel := context.WithCancel(ctx)
	defer cancel()
	deadline, e := fence(child)
	if e != nil {
		return e
	}
	remaining := time.Until(deadline.Add(-margin))
	if remaining <= 0 {
		return errors.New("lease deadline reached")
	}
	watchdog := time.NewTimer(remaining)
	defer watchdog.Stop()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	finished := make(chan error, 1)
	go func() { finished <- work(child) }()
	type renewal struct {
		deadline time.Time
		err      error
	}
	renewed := make(chan renewal, 1)
	pending := false
	for {
		select {
		case e := <-finished:
			return e
		case <-ctx.Done():
			return ctx.Err()
		case <-watchdog.C:
			return errors.New("lease deadline reached")
		case <-ticker.C:
			if !pending {
				pending = true
				go func() { d, e := fence(child); renewed <- renewal{d, e} }()
			}
		case result := <-renewed:
			pending = false
			if result.err != nil {
				return result.err
			}
			if !result.deadline.After(deadline) {
				return errors.New("renewal did not extend lease")
			}
			deadline = result.deadline
			remaining = time.Until(deadline.Add(-margin))
			if remaining <= 0 {
				return errors.New("lease deadline reached")
			}
			watchdog.Reset(remaining)
		}
	}
}

// RenewLease reads the current revision then submits a fenced renewal. CAS
// failure or an unknown result is returned; callers must stop work and recover.
func (c *Client) RenewLease(ctx context.Context, taskID string, lease map[string]any, seconds int) (time.Time, error) {
	out, e := c.Call(ctx, "task.get", map[string]any{"task_id": taskID})
	if e != nil {
		return time.Time{}, e
	}
	v, e := Parse(out.Response)
	if e != nil {
		return time.Time{}, e
	}
	task := v.(map[string]any)["result"].(map[string]any)["data"].(map[string]any)
	out, e = c.Call(ctx, "assignment.renew", map[string]any{"task_id": taskID, "expected_revision": task["revision"], "lease": lease, "lease_seconds": seconds})
	if e != nil {
		return time.Time{}, e
	}
	v, e = Parse(out.Response)
	if e != nil {
		return time.Time{}, e
	}
	data := v.(map[string]any)["result"].(map[string]any)["data"].(map[string]any)
	for _, r := range data["records"].([]any) {
		record := r.(map[string]any)
		if record["record_type"] == "Assignment" {
			a := record["record"].(map[string]any)
			if a["assignment_id"] == lease["assignment_id"] {
				return time.Parse("2006-01-02T15:04:05.000Z", a["lease_expires_at"].(string))
			}
		}
	}
	return time.Time{}, errors.New("renewal omitted Assignment")
}

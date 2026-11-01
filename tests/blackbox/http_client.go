// Package blackbox implements public-HTTP test support without database access.
package blackbox

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
)

type Client struct {
	URL       string
	Token     string
	Authority string
	Agent     string
	HTTP      *http.Client
	Observe   func(Prepared, Response, error)
	sequence  atomic.Uint64
}

// Prepared contains immutable encoded request bytes, allowing exact-key replay.
// Its bytes can contain a lease credential and must not be logged as evidence.
type Prepared struct {
	body   []byte
	ID     string
	Method string
}

type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		Kind    string         `json:"kind"`
		Retry   string         `json:"retry"`
		Details map[string]any `json:"details"`
	} `json:"data"`
}

type Response struct {
	Status        int
	Header        http.Header
	ID            string
	Data          map[string]any
	Replayed      bool
	Error         *RPCError
	TransportCode string
}

func (c *Client) Prepare(method string, input map[string]any) (Prepared, error) {
	params := make(map[string]any, len(input)+2)
	for k, v := range input {
		params[k] = v
	}
	if method != "bridge.capabilities" {
		if _, exists := params["authority_id"]; !exists {
			params["authority_id"] = c.Authority
		}
		if c.Agent != "" {
			if _, exists := params["as_agent_id"]; !exists {
				params["as_agent_id"] = c.Agent
			}
		}
	}
	id := fmt.Sprintf("request_%d", c.sequence.Add(1))
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	return Prepared{body: body, ID: id, Method: method}, err
}

func (c *Client) Do(ctx context.Context, p Prepared) (result Response, callErr error) {
	defer func() {
		if c.Observe != nil {
			c.Observe(p, result, callErr)
		}
	}()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL+"/rpc", bytes.NewReader(p.body))
	if err != nil {
		return Response{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.Token)
	client := c.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	res, err := client.Do(req)
	if err != nil {
		return Response{}, err
	}
	defer res.Body.Close()
	result = Response{Status: res.StatusCode, Header: res.Header.Clone()}
	if res.StatusCode == http.StatusNoContent {
		return result, nil
	}
	var envelope struct {
		JSONRPC string `json:"jsonrpc"`
		ID      string `json:"id"`
		Result  struct {
			Data map[string]any `json:"data"`
			Meta struct {
				Replayed bool `json:"replayed"`
			} `json:"meta"`
		} `json:"result"`
		Error json.RawMessage `json:"error"`
	}
	decoder := json.NewDecoder(io.LimitReader(res.Body, 128<<20))
	decoder.UseNumber()
	if err = decoder.Decode(&envelope); err != nil {
		return result, fmt.Errorf("decode HTTP %d response: %w", res.StatusCode, err)
	}
	if err = decoder.Decode(new(any)); err == nil {
		return result, fmt.Errorf("response contains trailing JSON")
	} else if err != io.EOF {
		return result, fmt.Errorf("finish HTTP %d response: %w", res.StatusCode, err)
	}
	result.ID = envelope.ID
	result.Data = envelope.Result.Data
	result.Replayed = envelope.Result.Meta.Replayed
	if len(envelope.Error) > 0 && string(envelope.Error) != "null" {
		if envelope.JSONRPC == "2.0" {
			result.Error = new(RPCError)
			if err = json.Unmarshal(envelope.Error, result.Error); err != nil {
				return result, err
			}
		} else {
			var transport struct {
				Code string `json:"code"`
			}
			if err = json.Unmarshal(envelope.Error, &transport); err != nil {
				return result, err
			}
			result.TransportCode = transport.Code
		}
	}
	if envelope.JSONRPC == "2.0" && envelope.ID != p.ID {
		return result, fmt.Errorf("response id mismatch")
	}
	return result, nil
}

func (c *Client) Call(ctx context.Context, method string, params map[string]any) (Response, error) {
	p, err := c.Prepare(method, params)
	if err != nil {
		return Response{}, err
	}
	return c.Do(ctx, p)
}

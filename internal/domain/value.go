package domain

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const Version = "bridge-m1-1"
const MaxInteger int64 = 9007199254740991
const TimeFormat = "2006-01-02T15:04:05.000Z"

func Stamp(t time.Time) string     { return t.UTC().Truncate(time.Millisecond).Format(TimeFormat) }
func ParseTime(s string) time.Time { t, _ := time.Parse(TimeFormat, s); return t }
func Ptr[T any](v T) *T            { return &v }
func String(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
func NewID(prefix string) string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return prefix + "_" + hex.EncodeToString(b)
}
func ToObject(v any) Object {
	b, e := json.Marshal(v)
	if e != nil {
		panic(e)
	}
	var o Object
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if e = d.Decode(&o); e != nil {
		panic(e)
	}
	return o
}
func Decode[T any](v any) (T, error) {
	var result T
	b, e := json.Marshal(v)
	if e == nil {
		e = json.Unmarshal(b, &result)
	}
	return result, e
}
func S(o Object, key string) string { v, _ := o[key].(string); return v }
func N(o Object, key string) int64 {
	switch v := o[key].(type) {
	case json.Number:
		n, _ := v.Int64()
		return n
	case int64:
		return v
	case int:
		return int64(v)
	case float64:
		return int64(v)
	}
	return 0
}
func O(o Object, key string) Object {
	switch v := o[key].(type) {
	case Object:
		return v
	case map[string]any:
		return Object(v)
	}
	return nil
}
func Contains(values []string, v string) bool {
	for _, x := range values {
		if x == v {
			return true
		}
	}
	return false
}
func RecordOf(kind string, value any) Record { return Record{kind, ToObject(value)} }
func (r Record) ID() string {
	if r.RecordType == "Task" {
		return S(r.Record, "task_id")
	}
	return S(r.Record, strings.ToLower(r.RecordType)+"_id")
}
func (r Record) TaskID() string        { return S(r.Record, "task_id") }
func (r Record) AsTask() (Task, error) { return Decode[Task](r.Record) }

type Error struct {
	Code    int       `json:"code"`
	Message string    `json:"message"`
	Data    ErrorData `json:"data"`
}
type ErrorData struct {
	Kind    string `json:"kind"`
	Retry   string `json:"retry"`
	Details Object `json:"details,omitempty"`
}

func (e *Error) Error() string { return e.Data.Kind + ": " + e.Message }

var Codes = map[string]int{"FORBIDDEN": -32001, "NOT_FOUND": -32002, "STALE_REVISION": -32003, "LEASE_FENCED": -32004, "INVALID_STATE": -32005, "GATE_CLOSED": -32006, "POLICY_DENIED": -32007, "IDEMPOTENCY_CONFLICT": -32008, "REINITIALIZE_REQUIRED": -32009, "INVALID_CURSOR": -32010, "RECEIPT_NOT_FOUND": -32011, "RECEIPT_UNAVAILABLE": -32012, "AUTHORITY_MISMATCH": -32013, "RESOURCE_LIMIT": -32014, "UNAVAILABLE": -32015, "PARSE_ERROR": -32700, "INVALID_REQUEST": -32600, "METHOD_NOT_FOUND": -32601, "INVALID_PARAMS": -32602, "INTERNAL_ERROR": -32603, "UNAUTHENTICATED": 401}

func E(kind string) *Error {
	code, ok := Codes[kind]
	if !ok {
		panic(fmt.Sprintf("unknown error kind %q", kind))
	}
	retry := "none"
	switch kind {
	case "STALE_REVISION", "LEASE_FENCED", "GATE_CLOSED":
		retry = "read_state"
	case "REINITIALIZE_REQUIRED":
		retry = "reinitialize"
	case "UNAVAILABLE", "INTERNAL_ERROR", "RECEIPT_NOT_FOUND":
		retry = "same_request"
	}
	return &Error{code, kind, ErrorData{Kind: kind, Retry: retry}}
}
func Stale(revision int64) *Error {
	e := E("STALE_REVISION")
	e.Data.Details = Object{"current_revision": revision}
	return e
}

// Copyright 2026 Mateusz Urbanek.

package proxy

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

// upstreamTransport limits decoded bytes before the SDK parses HTTP errors or JSON.
// SSE is long-lived, so its bound resets only at a blank event separator.
type upstreamTransport struct{ base http.RoundTripper }

func (t upstreamTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method == http.MethodDelete {
		// SDK Close sends DELETE before canceling its connection and allows
		// five seconds per session. Bound this best-effort cleanup separately
		// from calls and discovery, even if the remote handler never replies.
		ctx, cancel := context.WithTimeout(req.Context(), 500*time.Millisecond)
		defer cancel()
		req = req.Clone(ctx)
	}
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") && resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if req.Method == http.MethodPost {
			// RoundTrip must return as soon as headers arrive: the SDK marks a
			// request sent then, and can cancel a still-open response stream.
			resp.Body = &taskEventBody{ReadCloser: resp.Body, reader: bufio.NewReader(resp.Body)}
			resp.ContentLength = -1
			resp.Header.Del("Content-Length")
		} else {
			resp.Body = &eventBody{ReadCloser: resp.Body}
		}
	} else {
		resp.Body = &limitedBody{ReadCloser: resp.Body, remaining: maxBodyBytes}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 && strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
			body, err := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if err != nil {
				return nil, err
			}
			body, err = markTaskTools(body)
			if err != nil {
				return nil, err
			}
			resp.Body = io.NopCloser(bytes.NewReader(body))
			resp.ContentLength = int64(len(body))
			resp.Header.Del("Content-Length")
		}
	}
	return resp, nil
}

// taskEventBody transforms complete SSE events as they arrive, never waiting for
// the response to end. Its input and output are bounded to one event at a time.
type taskEventBody struct {
	io.ReadCloser
	reader  *bufio.Reader
	pending []byte
	readErr error
}

func (b *taskEventBody) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for len(b.pending) == 0 && b.readErr == nil {
		var event []byte
		lineStart := 0
		for {
			if len(event) >= maxBodyBytes {
				b.readErr = errors.New("upstream SSE event exceeds 8 MiB")
				break
			}
			ch, err := b.reader.ReadByte()
			if err != nil {
				b.readErr = err
				break
			}
			event = append(event, ch)
			if ch == '\n' {
				line := bytes.TrimSuffix(event[lineStart:len(event)-1], []byte{'\r'})
				lineStart = len(event)
				if len(line) == 0 {
					b.pending, b.readErr = transformTaskEvent(event)
					break
				}
			}
		}
		if b.readErr == io.EOF {
			// An unterminated event is not dispatched by SSE, but preserve
			// its bytes so the SDK can apply its usual EOF behavior.
			b.pending = event
		}
	}
	if len(b.pending) != 0 {
		n := copy(p, b.pending)
		b.pending = b.pending[n:]
		return n, nil
	}
	return 0, b.readErr
}

func transformTaskEvent(event []byte) ([]byte, error) {
	lines := bytes.SplitAfter(event, []byte{'\n'})
	var data []byte
	firstData := -1
	for i, raw := range lines {
		line := bytes.TrimSuffix(bytes.TrimSuffix(raw, []byte{'\n'}), []byte{'\r'})
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		if firstData < 0 {
			firstData = i
		} else {
			data = append(data, '\n')
		}
		value := line[len("data:"):]
		if len(value) != 0 && value[0] == ' ' {
			value = value[1:]
		}
		data = append(data, value...)
	}
	if firstData < 0 {
		return event, nil
	}
	updated, err := markTaskTools(data)
	if err != nil {
		// The SDK reads POST SSE asynchronously and discards ordinary body
		// read errors. Send a correlated JSON-RPC error instead so discovery
		// fails rather than silently accepting an invalid tool inventory.
		var message struct {
			ID json.RawMessage `json:"id"`
		}
		if json.Unmarshal(data, &message) != nil || len(message.ID) == 0 {
			return nil, err
		}
		updated, _ = json.Marshal(map[string]any{
			"jsonrpc": "2.0",
			"id":      message.ID,
			"error": map[string]any{
				"code":    -32603,
				"message": err.Error(),
			},
		})
	}
	if bytes.Equal(updated, data) {
		return event, nil
	}
	var result []byte
	for i, raw := range lines {
		line := bytes.TrimSuffix(bytes.TrimSuffix(raw, []byte{'\n'}), []byte{'\r'})
		if bytes.HasPrefix(line, []byte("data:")) {
			if i == firstData {
				result = append(result, "data: "...)
				result = append(result, updated...)
				if bytes.HasSuffix(raw, []byte("\r\n")) {
					result = append(result, '\r')
				}
				result = append(result, '\n')
			}
			continue
		}
		result = append(result, raw...)
	}
	return result, nil
}

// The SDK does not model execution.taskSupport. Carry required-task information
// privately through Tool.Meta and remove it before publication.
func markTaskTools(body []byte) ([]byte, error) {
	var message map[string]json.RawMessage
	if json.Unmarshal(body, &message) != nil || message["result"] == nil {
		return body, nil
	}
	var result map[string]json.RawMessage
	if json.Unmarshal(message["result"], &result) != nil || result["tools"] == nil {
		return body, nil
	}
	var tools []map[string]json.RawMessage
	if err := json.Unmarshal(result["tools"], &tools); err != nil {
		return nil, err
	}
	changed := false
	for _, tool := range tools {
		if tool["_meta"] != nil {
			var meta map[string]json.RawMessage
			if err := json.Unmarshal(tool["_meta"], &meta); err == nil && meta["mcp-gateway/task-required"] != nil {
				delete(meta, "mcp-gateway/task-required")
				tool["_meta"], _ = json.Marshal(meta)
				changed = true
			}
		}
		if tool["execution"] == nil {
			continue
		}
		var execution map[string]json.RawMessage
		if err := json.Unmarshal(tool["execution"], &execution); err != nil || execution == nil {
			return nil, errors.New("invalid tool execution mode")
		}
		if execution["taskSupport"] == nil {
			// ToolExecution defaults taskSupport to forbidden when absent.
			continue
		}
		var mode string
		if err := json.Unmarshal(execution["taskSupport"], &mode); err != nil {
			return nil, errors.New("invalid tool execution mode")
		}
		if mode != "forbidden" && mode != "optional" && mode != "required" {
			return nil, errors.New("unknown tool execution mode")
		}
		if mode == "required" {
			var meta map[string]json.RawMessage
			if len(tool["_meta"]) != 0 {
				if err := json.Unmarshal(tool["_meta"], &meta); err != nil {
					return nil, err
				}
			}
			if meta == nil {
				meta = make(map[string]json.RawMessage)
			}
			meta["mcp-gateway/task-required"] = json.RawMessage(`true`)
			tool["_meta"], _ = json.Marshal(meta)
			changed = true
		}
	}
	if !changed {
		return body, nil
	}
	result["tools"], _ = json.Marshal(tools)
	message["result"], _ = json.Marshal(result)
	return json.Marshal(message)
}

type limitedBody struct {
	io.ReadCloser
	remaining int64
}

func (b *limitedBody) Read(p []byte) (int, error) {
	if b.remaining < 0 {
		return 0, errors.New("upstream response exceeds 8 MiB")
	}
	if int64(len(p)) > b.remaining+1 {
		p = p[:b.remaining+1]
	}
	n, err := b.ReadCloser.Read(p)
	b.remaining -= int64(n)
	if b.remaining < 0 {
		return 0, errors.New("upstream response exceeds 8 MiB")
	}
	return n, err
}

type eventBody struct {
	io.ReadCloser
	size    int
	newline bool
}

func (b *eventBody) Read(p []byte) (int, error) {
	// Restrict a single read to the remaining event budget; the SDK cannot
	// buffer an unbounded line even if the peer never sends a delimiter.
	if b.size >= maxBodyBytes {
		return 0, errors.New("upstream SSE event exceeds 8 MiB")
	}
	if len(p) > maxBodyBytes-b.size {
		p = p[:maxBodyBytes-b.size]
	}
	n, err := b.ReadCloser.Read(p)
	for _, v := range p[:n] {
		b.size++
		if v == '\n' {
			if b.newline {
				b.size = 0
			}
			b.newline = true
		} else if v != '\r' {
			b.newline = false
		}
	}
	return n, err
}

package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
)

// Normalize only public conversation events for observational views. Reasoning
// records are excluded; a user_message is counted once, not again as response_item.
func observationRecord(line []byte) []byte {
	var e struct {
		Type      string `json:"type"`
		Timestamp string `json:"timestamp"`
		Payload   struct {
			Type    string                        `json:"type"`
			Role    string                        `json:"role"`
			Message string                        `json:"message"`
			Name    string                        `json:"name"`
			Content []struct{ Type, Text string } `json:"content"`
		} `json:"payload"`
	}
	if json.Unmarshal(line, &e) != nil || (e.Type != "event_msg" && e.Type != "response_item") {
		return line
	}
	role := ""
	var content any
	switch {
	case e.Type == "event_msg" && e.Payload.Type == "user_message":
		role, content = "user", e.Payload.Message
	case e.Type == "response_item" && e.Payload.Type == "message" && e.Payload.Role == "assistant":
		role = "assistant"
		blocks := []map[string]string{}
		for _, b := range e.Payload.Content {
			if b.Type == "output_text" {
				blocks = append(blocks, map[string]string{"type": "text", "text": b.Text})
			}
		}
		content = blocks
	case e.Type == "response_item" && (e.Payload.Type == "function_call" || e.Payload.Type == "custom_tool_call"):
		role, content = "assistant", []map[string]string{{"type": "tool_use", "name": e.Payload.Name}}
	default:
		return line
	}
	b, _ := json.Marshal(map[string]any{"type": role, "timestamp": e.Timestamp, "message": map[string]any{"role": role, "content": content}})
	return b
}

func codexUsageTurns(path string, n int) []usageTurn {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil
	}
	b, err := io.ReadAll(io.NewSectionReader(f, max(int64(0), st.Size()-4<<20), 4<<20))
	if err != nil {
		return nil
	}
	lines := bytes.Split(b, []byte{'\n'})
	var out []usageTurn
	seen := map[int64]bool{}
	for i := len(lines) - 1; i >= 0 && len(out) < n; i-- {
		if !bytes.Contains(lines[i], []byte(`"token_count"`)) {
			continue
		}
		var e struct {
			Type      string `json:"type"`
			Timestamp string `json:"timestamp"`
			Payload   struct {
				Type string `json:"type"`
				Info struct {
					Last struct {
						In       int64 `json:"input_tokens"`
						Cached   int64 `json:"cached_input_tokens"`
						Out      int64 `json:"output_tokens"`
						Thinking int64 `json:"reasoning_output_tokens"`
					} `json:"last_token_usage"`
					Total struct {
						Tokens int64 `json:"total_tokens"`
					} `json:"total_token_usage"`
				} `json:"info"`
			} `json:"payload"`
		}
		if json.Unmarshal(lines[i], &e) != nil || e.Type != "event_msg" || e.Payload.Type != "token_count" {
			continue
		}
		u := e.Payload.Info.Last
		key := e.Payload.Info.Total.Tokens
		if u.In == 0 || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, usageTurn{TS: e.Timestamp, Input: max(int64(0), u.In-u.Cached), CacheRead: u.Cached, Output: u.Out, Thinking: u.Thinking, Context: u.In})
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func isCodexTranscript(path string) bool { return strings.Contains(path, "/rollout-") }

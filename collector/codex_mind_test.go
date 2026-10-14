package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCodexComposerStates(t *testing.T) {
	cases := []struct{ name, screen, want string }{
		{"empty", "› \n  90% context left", "idle"},
		{"placeholder", "› Ask Codex to do anything", "idle"},
		{"queue placeholder", "› Claim and complete coding tasks", "idle"},
		{"draft", "› don't submit this yet", "typing"},
		{"spinner", "• Working (2s • esc to interrupt)\n› Ask Codex to do anything", "working"},
		{"cap", "■ You've hit your usage limit. Try again later.\n› Ask Codex to do anything", "capped"},
		{"approval", "Would you like to run this command?\n› 1. Yes", "waiting"},
		{"quoted cap", "  command output: usage limit reached\n› Ask Codex to do anything", "idle"},
		{"unrecognized", "No recognized controls", "unknown"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := classifyScreen(tc.screen, "codex")
			if got != tc.want {
				t.Fatalf("state = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestCodexPaneIdleAndSendGuard(t *testing.T) {
	isolateMindTest(t, `#!/bin/sh
case $1 in
  list-panes) echo '%19|test:9.1|codex|test';;
  capture-pane) cat "$HOME/screen";;
  send-keys) echo "$*" >> "$HOME/keys";;
esac
`)
	oldProbe := paneTypingProbe
	paneTypingProbe = 0
	t.Cleanup(func() { paneTypingProbe = oldProbe })
	t.Setenv("EIGHT_NO_SUMMON", "0")
	for _, screen := range []string{
		"› unsent operator text",
		"• Working (esc to interrupt)\n› Ask Codex to do anything",
		"■ You've hit your usage limit.\n› Ask Codex to do anything",
		"Waiting for approval\n› 1. Yes",
		"unknown screen",
	} {
		if err := os.WriteFile(filepath.Join(os.Getenv("HOME"), "screen"), []byte(screen), 0o600); err != nil {
			t.Fatal(err)
		}
		if paneIdle("%19") || sendToPane("%19", "must not type", nil) {
			t.Fatalf("unsafe screen accepted: %q", screen)
		}
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("HOME"), "keys")); !os.IsNotExist(err) {
		t.Fatal("unsafe screen received keystrokes")
	}
	// Claude's prompt marker can occur in Codex tool output. Only the Codex
	// composer determines whether that harness has unsent input.
	if err := os.WriteFile(filepath.Join(os.Getenv("HOME"), "screen"), []byte("❯ quoted Claude draft\n› Ask Codex to do anything"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !paneIdle("%19") {
		t.Fatal("idle Codex blocked by another harness's quoted prompt")
	}
	if !sendToPane("%19", "test", nil) {
		t.Fatal("idle Codex must accept a prompt")
	}
	// Cover the settle and the old second-Enter interval before restoring the
	// fake command, so no deferred keystrokes can escape the test boundary.
	time.Sleep(1500 * time.Millisecond)
	keys, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), "keys"))
	if err != nil || strings.TrimSpace(string(keys)) != "send-keys -t %19 -l test\nsend-keys -t %19 Enter" {
		t.Fatalf("Codex must submit exactly once: %q (%v)", keys, err)
	}
}

func TestCodexPublicTranscriptAndUsage(t *testing.T) {
	user := observationRecord([]byte(`{"type":"event_msg","timestamp":"2026-09-29T00:00:00Z","payload":{"type":"user_message","message":"do the task"}}`))
	var message struct {
		Type    string
		Message struct{ Content string }
	}
	if err := json.Unmarshal(user, &message); err != nil || message.Type != "user" || message.Message.Content != "do the task" {
		t.Fatalf("operator turn not normalized: %s (%v)", user, err)
	}
	path := filepath.Join(t.TempDir(), "rollout-test.jsonl")
	usage := `{"type":"event_msg","timestamp":"2026-09-29T00:00:01Z","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":100,"cached_input_tokens":80,"output_tokens":10,"reasoning_output_tokens":4},"total_token_usage":{"total_tokens":110}}}}` + "\n"
	if err := os.WriteFile(path, []byte(usage+usage), 0o600); err != nil {
		t.Fatal(err)
	}
	turns := codexUsageTurns(path, 10)
	if len(turns) != 1 || turns[0].Context != 100 || turns[0].Input != 20 || turns[0].CacheRead != 80 || turns[0].Thinking != 4 {
		t.Fatalf("usage not normalized/deduplicated: %+v", turns)
	}
}

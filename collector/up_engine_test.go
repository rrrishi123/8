package main

import "testing"

func TestBrowserEngineSelection(t *testing.T) {
	for _, tc := range []struct {
		name, preference, want string
		s                      substrate
	}{
		{"both-default-chrome", "", "chrome", substrate{Chrome: "chrome", Firefox: "firefox", Gecko: "gecko"}},
		{"explicit-firefox", "firefox", "firefox", substrate{Chrome: "chrome", Firefox: "firefox", Gecko: "gecko"}},
		{"chrome-only", "", "chrome", substrate{Chrome: "chrome"}},
		{"firefox-fallback", "", "firefox", substrate{Firefox: "firefox", Gecko: "gecko"}},
		{"explicit-chrome-missing", "chrome", "", substrate{Firefox: "firefox", Gecko: "gecko"}},
		{"explicit-firefox-missing", "firefox", "", substrate{Chrome: "chrome"}},
		{"no-gecko", "", "", substrate{Firefox: "firefox"}},
		{"browserless", "", "", substrate{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := selectBrowserEngine(tc.s, tc.preference); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

// TestOwnsBootConfig: only the canonical :7070 witness may own the shared
// boot.json that `watch` revives from. A :7199 smoke (or any other listener)
// must not — it would poison the witness's revive path (regression 2026-10-28).
func TestOwnsBootConfig(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want bool
	}{
		{"default-unset", nil, true},                          // no -listen → :7070
		{"explicit-7070", []string{"-listen", ":7070"}, true}, // canonical witness
		{"wired-7070", []string{"-listen", ":7070", "-brokers", "fox=http://127.0.0.1:4445"}, true},
		{"smoke-7199", []string{"-listen", ":7199"}, false}, // fleet-ci smoke — must not own
		{"nonstandard", []string{"-listen", ":9090"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ownsBootConfig(tc.args); got != tc.want {
				t.Errorf("ownsBootConfig(%v) = %v, want %v", tc.args, got, tc.want)
			}
		})
	}
}

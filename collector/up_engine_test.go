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

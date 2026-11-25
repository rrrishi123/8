package main

import (
	"runtime/debug"
	"testing"
)

func TestBuildFromInfo(t *testing.T) {
	cases := []struct {
		name string
		bi   *debug.BuildInfo
		ok   bool
		want string
	}{
		{"no info", nil, false, ""},
		{"go install by tag", &debug.BuildInfo{Main: debug.Module{Version: "v0.0.4"}}, true, "v0.0.4"},
		{"devel falls through to vcs", &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}, Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "cd64cad0123456789abcdef0123456789abcdef0"}, {Key: "vcs.modified", Value: "false"}}}, true, "cd64cad01234"},
		{"dirty checkout", &debug.BuildInfo{Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "abc"}, {Key: "vcs.modified", Value: "true"}}}, true, "abc-dirty"},
		{"nothing embedded", &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}}, true, ""},
	}
	for _, c := range cases {
		if got := buildFromInfo(c.bi, c.ok); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

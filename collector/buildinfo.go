package main

import (
	"runtime/debug"
	"strings"
)

// buildFromInfo — provenance for a binary nobody stamped. build.sh sets
// main.buildSHA via -ldflags, but the obtainable release is `go install
// github.com/rrrishi123/8/collector@vX` (install.sh, brew), and `go install`
// cannot pass ldflags: every stranger's /health said "build":"" (stranger seat,
// 2026-11-25). The Go toolchain already embeds what we need — the module
// version when installed by tag, the VCS revision when built from a checkout —
// so fall back to that instead of attesting nothing.
//
// Order: module version (v0.0.4) > vcs.revision[:12] (+ "-dirty" when the
// tree was modified) > "". "(devel)" is what a bare `go build` reports and
// carries no information, so it is skipped.
func buildFromInfo(bi *debug.BuildInfo, ok bool) string {
	if !ok || bi == nil {
		return ""
	}
	if v := strings.TrimSpace(bi.Main.Version); v != "" && v != "(devel)" {
		return v
	}
	var rev, dirty string
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			if s.Value == "true" {
				dirty = "-dirty"
			}
		}
	}
	if rev == "" {
		return ""
	}
	if len(rev) > 12 {
		rev = rev[:12]
	}
	return rev + dirty
}

func init() {
	if buildSHA == "" {
		buildSHA = buildFromInfo(debug.ReadBuildInfo())
	}
}

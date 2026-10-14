//go:build darwin

package main

import (
	"os"
	"syscall"
	"time"
)

// birthFromSys reads the true file birthtime where the OS keeps one (darwin
// Birthtimespec). Elsewhere there is no portable birthtime — callers fall back
// to ModTime.
func birthFromSys(fi os.FileInfo) time.Time {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return time.Unix(st.Birthtimespec.Sec, st.Birthtimespec.Nsec)
	}
	return time.Time{}
}

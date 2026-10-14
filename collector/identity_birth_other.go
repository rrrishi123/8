//go:build !darwin

package main

import (
	"os"
	"time"
)

// birthFromSys: no portable birthtime off darwin — zero time tells fileBirth to
// fall back to ModTime.
func birthFromSys(os.FileInfo) time.Time { return time.Time{} }

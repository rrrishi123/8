package main

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"time"
)

func contextWithTimeout(sec int) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), time.Duration(sec)*time.Second)
}
func execCommandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, name, args...)
}
func envWithout(env []string, drop string) []string {
	out := make([]string, 0, len(env))
	for _, e := range env {
		if strings.HasPrefix(e, drop+"=") {
			continue
		}
		out = append(out, e)
	}
	return out
}
func devNull() *os.File { f, _ := os.Open(os.DevNull); return f }

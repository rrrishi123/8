#!/bin/sh
# 4-system v0.0.2 installer (go-install flavor) — the obtainable release, bus #310.
#   curl -sL https://raw.githubusercontent.com/rrrishi123/8/refs/heads/v0.0.2/install.sh | sh
# Builds from the signed v0.0.2 tags via the public Go module proxy — no
# credentials, no prebuilt trust. Needs Go >= 1.22 on the host; prebuilt
# darwin-arm64/linux-amd64 binaries land on the GitHub releases when minted.
set -eu

VER="v0.0.2"
BIN="${EIGHT_BIN:-$HOME/.8/bin}"

command -v go >/dev/null 2>&1 || {
  echo "go not found — install Go >=1.22 (https://go.dev/dl) or wait for the prebuilt release binaries" >&2
  exit 1
}

mkdir -p "$BIN"
echo "installing the 4-system $VER into $BIN"
GOBIN="$BIN" go install "github.com/rrrishi123/8/collector@$VER"                  # the witness (carries up/watch)
GOBIN="$BIN" go install "github.com/rrrishi123/http-mcp/cmd/eight@$VER"           # the one-command dispatcher
GOBIN="$BIN" go install "github.com/rrrishi123/http-mcp/cmd/mcp@$VER" && mv "$BIN/mcp" "$BIN/http-mcp"
GOBIN="$BIN" go install "github.com/rrrishi123/http-mcp/cmd/wire@$VER"            # the MITM witness proxy
GOBIN="$BIN" go install "github.com/rrrishi123/http-mcp/cmd/channel@$VER"         # the BiDi broker
GOBIN="$BIN" go install "github.com/rrrishi123/pilot@$VER"
GOBIN="$BIN" go install "github.com/rrrishi123/adapters/browser/cmd/browser@$VER"

echo
echo "installed: $(ls "$BIN" | tr '\n' ' ')"
echo "start here:"
echo "  $BIN/collector up      # discover substrate + boot the witness (degrades if deps absent)"
echo "  $BIN/collector watch   # supervise it"
echo "  $BIN/eight             # the dispatcher over the wire atoms"
case ":$PATH:" in *":$BIN:"*) ;; *) echo "add to PATH:  export PATH=\"$BIN:\$PATH\"" ;; esac

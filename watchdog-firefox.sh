#!/usr/bin/env bash
# Firefox chain watchdog — checks health of :4445 (broker) and :4444 (geckodriver).
# If either is down, re-runs start-auto-firefox.sh to bring the whole chain back.
# Called by systemd firefox-chain-health.timer every 60s.
set -uo pipefail
LOG=/tmp/claude-1000/firefox-watchdog.log
mkdir -p /tmp/claude-1000
say(){ echo "$(date +%H:%M:%S) $*" | tee -a "$LOG"; }

BROKER_UP=false
GECKO_UP=false

# Check broker :4445 /health
if curl -sf --max-time 5 http://localhost:4445/health >/dev/null 2>&1; then
    BROKER_UP=true
fi

# Check geckodriver :4444
if ss -tlnp 2>/dev/null | grep -q ':4444 '; then
    GECKO_UP=true
fi

if $BROKER_UP && $GECKO_UP; then
    # Healthy — silent (don't spam log every 60s)
    exit 0
fi

say "🔥 Firefox chain degraded — broker=$BROKER_UP geckodriver=$GECKO_UP"
say "   running start-auto-firefox.sh..."
timeout 90 bash /home/rishi/Work/8/start-auto-firefox.sh >>"$LOG" 2>&1
RC=$?
say "   done (rc=$RC)"

# Re-verify
if curl -sf --max-time 5 http://localhost:4445/health >/dev/null 2>&1; then
    say "✅ broker :4445 healthy after restart"
else
    say "❌ broker :4445 STILL DOWN after restart"
fi

#!/usr/bin/env bash
# devices.sh — bring REAL LOCAL devices into the stack as seats on 8.
#
# Appium is a DRIVER, peer to geckodriver: geckodriver bridges Firefox (giving 8
# its channel + request seats); Appium bridges real iOS/Android (giving 8 a
# request-physics seat per device). No cloud — this is the generic LOCAL stack.
#
# It starts a local Appium server, enumerates connected devices (adb +
# libimobiledevice), creates ONE Appium session per device THROUGH the wire
# shape (POST <hub>/session — the CALL atom), and registers each as a seat in
# ~/.8/sessions.json. 8 then sees + drives every device on its call-path:
#   /shot     -> device screenshot          (pixels)
#   /source   -> UiAutomator2 / XCUITest XML (the device's native UI tree)
#   /act      -> W3C actions: tap / swipe / type
# the SAME path it drives a browser request seat with. One witness, every target.
#
# iOS note: XCUITest needs WebDriverAgent signed with a PROVISIONING PROFILE on
# the device. If a session create fails on signing, that is the one blocker —
# sign WDA for the device (or hand me the profile) and re-run.
set -uo pipefail
HUB="${APPIUM_HUB:-http://127.0.0.1:4723}"
SESS="$HOME/.8/sessions.json"; mkdir -p "$(dirname "$SESS")"

# 1. the Appium driver (start if down — idempotent, like geckodriver)
if ! curl -s -m3 "$HUB/status" >/dev/null 2>&1; then
  echo "appium: starting on $HUB"
  nohup appium --port "${HUB##*:}" --relaxed-security >/tmp/appium.out 2>&1 &
  for _ in $(seq 1 40); do curl -s -m2 "$HUB/status" >/dev/null 2>&1 && break; sleep 0.5; done
fi
curl -s -m3 "$HUB/status" >/dev/null 2>&1 || { echo "appium: FAILED to start"; exit 1; }
echo "appium: up — $(curl -s "$HUB/status" | jq -r '.value.build.version' 2>/dev/null)"

reg(){ printf '{"id":"%s","hub":"%s","kind":"local","physics":"call","created_at":"%s"}\n' "$1" "$HUB" "$(date -u +%FT%TZ)" >> "$SESS"; }
create(){ curl -s -m150 "$HUB/session" -H 'Content-Type: application/json' -d "$1" | jq -r '.value.sessionId // empty'; }

# 2. Android — one seat per connected device (UiAutomator2, no signing needed)
for udid in $(adb devices 2>/dev/null | awk 'NR>1 && $2=="device"{print $1}'); do
  sid=$(create "{\"capabilities\":{\"alwaysMatch\":{\"platformName\":\"Android\",\"appium:automationName\":\"UiAutomator2\",\"appium:udid\":\"$udid\",\"appium:noReset\":true,\"appium:newCommandTimeout\":600}}}")
  if [ -n "$sid" ]; then reg "$sid"; echo "android seat: $sid  ($udid)"; else echo "android $udid: session create failed"; fi
done

# 3. iOS — one seat per connected device (XCUITest; needs WDA provisioning)
for udid in $(idevice_id -l 2>/dev/null); do
  ver=$(ideviceinfo -u "$udid" -k ProductVersion 2>/dev/null)
  sid=$(create "{\"capabilities\":{\"alwaysMatch\":{\"platformName\":\"iOS\",\"appium:automationName\":\"XCUITest\",\"appium:udid\":\"$udid\",\"appium:platformVersion\":\"$ver\",\"appium:noReset\":true,\"appium:newCommandTimeout\":600}}}")
  if [ -n "$sid" ]; then reg "$sid"; echo "ios seat: $sid  ($udid $ver)"; else echo "ios $udid: session create failed — likely WebDriverAgent provisioning (sign WDA for this device)"; fi
done
echo "done — connected devices registered as seats on 8 ($SESS). 8 now sees + drives them."

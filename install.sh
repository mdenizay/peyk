#!/usr/bin/env bash
#
# Peyk installer — https://github.com/mdenizay/peyk
#
#   curl -fsSL https://raw.githubusercontent.com/mdenizay/peyk/main/install.sh | sudo bash
#
# For a private repo, export GITHUB_TOKEN first; it is used only to download
# the release and is then offered to peyk's config for self-updates.
#
# This script is intentionally tiny: verify the platform, download the latest
# release binary, verify its SHA256 against checksums.txt, then hand over to
# `peyk setup` (which is resumable and does the real provisioning).
set -euo pipefail

REPO="mdenizay/peyk"
INSTALL_DIR="/opt/peyk/bin"
BIN="${INSTALL_DIR}/peyk"

say()  { printf '\033[1;36m[peyk]\033[0m %s\n' "$*"; }
fail() { printf '\033[1;31m[peyk]\033[0m %s\n' "$*" >&2; exit 1; }

[ "$(id -u)" -eq 0 ] || fail "Run as root: curl ... | sudo bash"

. /etc/os-release 2>/dev/null || fail "Cannot read /etc/os-release"
[ "${ID:-}" = "ubuntu" ] || fail "Peyk supports Ubuntu only (found: ${ID:-unknown})"
case "${VERSION_ID:-}" in
  22.04|24.04) ;;
  *) fail "Peyk supports Ubuntu 22.04 / 24.04 (found: ${VERSION_ID:-unknown})" ;;
esac

case "$(uname -m)" in
  x86_64)  ARCH=amd64 ;;
  aarch64) ARCH=arm64 ;;
  *) fail "Unsupported architecture: $(uname -m)" ;;
esac

AUTH=()
[ -n "${GITHUB_TOKEN:-}" ] && AUTH=(-H "Authorization: Bearer ${GITHUB_TOKEN}")

command -v python3 >/dev/null || fail "python3 is required (apt-get install -y python3)"

say "Resolving latest release…"
RELEASE_JSON="$(curl -fsSL "${AUTH[@]}" -H "Accept: application/vnd.github+json" \
  "https://api.github.com/repos/${REPO}/releases/latest")" \
  || fail "Could not query releases. Private repo? export GITHUB_TOKEN first."

ASSET="peyk_linux_${ARCH}.tar.gz"
# Parse tag + API asset URLs (API URLs work for private repos with a token).
PARSED="$(printf '%s' "$RELEASE_JSON" | PEYK_ASSET="$ASSET" python3 -c '
import json, os, sys
r = json.load(sys.stdin)
assets = {a["name"]: a["url"] for a in r.get("assets", [])}
print(r.get("tag_name", ""))
print(assets.get(os.environ["PEYK_ASSET"], ""))
print(assets.get("checksums.txt", ""))
')" || fail "Could not parse the release listing."
TAG="$(sed -n 1p <<<"$PARSED")"
ASSET_URL="$(sed -n 2p <<<"$PARSED")"
SUMS_URL="$(sed -n 3p <<<"$PARSED")"

[ -n "$TAG" ] || fail "No release found for ${REPO}. Publish a release first."
say "Latest release: ${TAG}"
[ -n "$ASSET_URL" ] || fail "Release ${TAG} has no asset ${ASSET}"
[ -n "$SUMS_URL" ]  || fail "Release ${TAG} has no checksums.txt"

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

say "Downloading ${ASSET}…"
curl -fsSL "${AUTH[@]}" -H "Accept: application/octet-stream" -o "${TMP}/${ASSET}" "$ASSET_URL"
curl -fsSL "${AUTH[@]}" -H "Accept: application/octet-stream" -o "${TMP}/checksums.txt" "$SUMS_URL"

say "Verifying SHA256…"
( cd "$TMP" && grep " ${ASSET}\$" checksums.txt | sha256sum -c - ) \
  || fail "Checksum verification FAILED — aborting install."

say "Installing to ${BIN}…"
mkdir -p "$INSTALL_DIR"
tar -xzf "${TMP}/${ASSET}" -C "$TMP" peyk
install -m 0755 "${TMP}/peyk" "$BIN"
ln -sf "$BIN" /usr/local/bin/peyk

say "Installed: $(peyk version)"

# When piped through `curl | bash`, stdin is the script itself — the
# interactive wizard needs the real terminal.
if [ -r /dev/tty ] && [ -w /dev/tty ]; then
  say "Starting server setup (resumable — re-run 'sudo peyk setup' anytime)…"
  exec peyk setup < /dev/tty > /dev/tty 2>&1
else
  say "No terminal available. Run the setup wizard with: sudo peyk setup"
fi

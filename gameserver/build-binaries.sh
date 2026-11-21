#!/bin/bash
# Rebuild every prebuilt gameserver binary and refresh gameserver/update.json.
# Clients check that manifest on startup and self-replace when it is newer.
set -euo pipefail
cd "$(dirname "$0")"
VERSION="${1:-$(sed -n 's/^const VERSION = "\(.*\)"$/\1/p' main.go)}"
[ -n "$VERSION" ] || { echo "cannot read VERSION from main.go" >&2; exit 1; }

TARGETS=(
  "linux   amd64 bin/gameserver-linux-amd64"
  "linux   arm64 bin/gameserver-linux-arm64"
  "windows amd64 bin/gameserver.exe"
)

for t in "${TARGETS[@]}"; do
  read -r goos goarch out <<<"$t"
  echo "building $out (gameserver $VERSION, $goos/$goarch)"
  CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" go build -trimpath -ldflags="-s -w" -o "$out" .
  chmod +x "$out"  # shared storage can drop the mode bit
done

sum() {
  if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | cut -d' ' -f1
  else shasum -a 256 "$1" | cut -d' ' -f1; fi
}

cat > update.json <<EOF
{
  "version": "$VERSION",
  "notes": "${2:-gameserver $VERSION}",
  "builds": {
    "linux-amd64": {
      "file": "bin/gameserver-linux-amd64",
      "sha256": "$(sum bin/gameserver-linux-amd64)"
    },
    "linux-arm64": {
      "file": "bin/gameserver-linux-arm64",
      "sha256": "$(sum bin/gameserver-linux-arm64)"
    },
    "windows-amd64": {
      "file": "bin/gameserver.exe",
      "sha256": "$(sum bin/gameserver.exe)"
    }
  }
}
EOF

echo
echo "gameserver $VERSION ready:"
ls -l bin/
echo "manifest: gameserver/update.json"

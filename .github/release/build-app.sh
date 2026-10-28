#!/bin/sh
# Builds a universal DSDock.app (arm64 + x86_64) on macOS.
#   .github/release/build-app.sh <version> <out-dir>
set -eu
VERSION=$1
OUT=$2
SHORT=$(echo "$VERSION" | sed -E 's/^v//; s/[-+].*$//')
case "$SHORT" in [0-9]*) ;; *) SHORT=0.0.0 ;; esac  # CI builds have no release number
APP="$OUT/DSDock.app"
rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
LDFLAGS="-s -w -X github.com/hikariming/dsdock/internal/cli.Version=$VERSION"
for arch in arm64 amd64; do
  # Go passes -arch to clang for darwin cgo builds, so one Mac builds both.
  CGO_ENABLED=1 GOOS=darwin GOARCH=$arch go build -tags desktop -trimpath -ldflags "$LDFLAGS" -o "$OUT/dsdock-app-$arch" .
done
lipo -create -output "$APP/Contents/MacOS/dsdock-app" "$OUT/dsdock-app-arm64" "$OUT/dsdock-app-amd64"
rm "$OUT/dsdock-app-arm64" "$OUT/dsdock-app-amd64"
sed -e "s/{{VERSION}}/$VERSION/" -e "s/{{SHORT_VERSION}}/$SHORT/" .github/release/Info.plist > "$APP/Contents/Info.plist"
plutil -lint "$APP/Contents/Info.plist"
# Ad-hoc signature only until there is a Developer ID (not notarized).
codesign --force --deep --sign - "$APP"
lipo -info "$APP/Contents/MacOS/dsdock-app"

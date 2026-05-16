#!/bin/bash
set -euo pipefail

APP="ggt"
OUTPUT_DIR="dist"
VERSION="${1:-$(git describe --tags --always --dirty 2>/dev/null || echo "dev")}"

PLATFORMS=(
    "linux/amd64"
    "linux/arm64"
    "darwin/amd64"
    "darwin/arm64"
    "windows/amd64"
)

echo "Building $APP v$VERSION"
echo ""

rm -rf "$OUTPUT_DIR"
mkdir -p "$OUTPUT_DIR"

for PLATFORM in "${PLATFORMS[@]}"; do
    GOOS="${PLATFORM%/*}"
    GOARCH="${PLATFORM#*/}"

    output_name="$APP-${GOOS}-${GOARCH}"
    if [ "$GOOS" = "windows" ]; then
        output_name+=".exe"
    fi

    echo "  → $GOOS/$GOARCH ..."

    GOOS=$GOOS GOARCH=$GOARCH \
        go build -ldflags "-s -w -X main.version=$VERSION" \
        -o "$OUTPUT_DIR/$output_name" .

    echo "    ✓ $(ls -lh "$OUTPUT_DIR/$output_name" | awk '{print $5}')"
done

echo ""
echo "All builds complete: $OUTPUT_DIR/"
ls -lh "$OUTPUT_DIR/"

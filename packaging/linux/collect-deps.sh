#!/bin/bash
set -euo pipefail

DEST_DIR="${1:-./libs}"
mkdir -p "$DEST_DIR"

declare -A SEEN

# List of binaries to scan
BINARIES=(
    gdalbuildvrt
    gdal_translate
    gdalwarp
    gdal_rasterize
    gdal_trace_outline
    gdal_merge_simple
    pkRenew
)

collect_deps() {
    local bin="$1"
    ldd "$bin" 2>/dev/null | awk '/=>/ {print $3}' | while read -r lib; do
        [ -z "$lib" ] && continue
        [ "$lib" = "not" ] && continue
        local base
        base="$(basename "$lib")"
        if [ -z "${SEEN[$base]:-}" ]; then
            SEEN[$base]=1
            if [ -f "$lib" ]; then
                cp -L "$lib" "$DEST_DIR/$base"
                # Recursively collect deps of this library
                collect_deps "$lib"
            fi
        fi
    done
}

for bin in "${BINARIES[@]}"; do
    if command -v "$bin" >/dev/null 2>&1; then
        binpath="$(command -v "$bin")"
        collect_deps "$binpath"
    fi
done

echo "Collected libraries into $DEST_DIR"

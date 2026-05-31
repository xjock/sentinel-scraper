#!/bin/bash
set -euo pipefail

VERSION="${1:-dev}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/../.." && pwd)"
APPDIR="${ROOT_DIR}/dist/AppDir"

rm -rf "${APPDIR}"
mkdir -p "${APPDIR}/usr/bin"
mkdir -p "${APPDIR}/usr/lib"
mkdir -p "${APPDIR}/usr/share/applications"
mkdir -p "${APPDIR}/usr/share/icons/hicolor/256x256/apps"

# Build Go binary
echo "Building Go binary..."
GOOS=linux GOARCH=amd64 go build -ldflags "-s -w -X main.version=${VERSION}" -o "${APPDIR}/usr/bin/sentinel-scraper" "${ROOT_DIR}"

# Collect GDAL dependencies
echo "Collecting GDAL dependencies..."
bash "${SCRIPT_DIR}/collect-deps.sh" "${APPDIR}/usr/lib"

# Copy GDAL binaries
for tool in gdalbuildvrt gdal_translate gdalwarp gdal_rasterize gdal_trace_outline gdal_merge_simple pkRenew gdalinfo; do
    if command -v "$tool" >/dev/null 2>&1; then
        cp "$(command -v "$tool")" "${APPDIR}/usr/bin/"
    else
        echo "Warning: $tool not found in PATH"
    fi
done

# Copy PROJ data
if [ -d /usr/share/proj ]; then
    mkdir -p "${APPDIR}/usr/share/proj"
    cp -r /usr/share/proj/* "${APPDIR}/usr/share/proj/"
elif [ -d /usr/local/share/proj ]; then
    mkdir -p "${APPDIR}/usr/share/proj"
    cp -r /usr/local/share/proj/* "${APPDIR}/usr/share/proj/"
else
    echo "Warning: PROJ data not found"
fi

# AppImage metadata
cp "${SCRIPT_DIR}/AppRun" "${APPDIR}/AppRun"
chmod +x "${APPDIR}/AppRun"
cp "${SCRIPT_DIR}/sentinel-scraper.desktop" "${APPDIR}/sentinel-scraper.desktop"
cp "${SCRIPT_DIR}/sentinel-scraper.desktop" "${APPDIR}/usr/share/applications/sentinel-scraper.desktop"
cp "${SCRIPT_DIR}/icon.png" "${APPDIR}/sentinel-scraper.png"
cp "${SCRIPT_DIR}/icon.png" "${APPDIR}/usr/share/icons/hicolor/256x256/apps/sentinel-scraper.png"

# Download appimagetool if needed
if [ ! -f /tmp/appimagetool-x86_64.AppImage ]; then
    wget -q "https://github.com/AppImage/appimagetool/releases/download/continuous/appimagetool-x86_64.AppImage" -O /tmp/appimagetool-x86_64.AppImage
    chmod +x /tmp/appimagetool-x86_64.AppImage
fi

# Build AppImage
mkdir -p "${ROOT_DIR}/dist"
/tmp/appimagetool-x86_64.AppImage "${APPDIR}" "${ROOT_DIR}/dist/sentinel-scraper_${VERSION}_linux_amd64.AppImage"

echo "AppImage built: ${ROOT_DIR}/dist/sentinel-scraper_${VERSION}_linux_amd64.AppImage"

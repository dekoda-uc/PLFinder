#!/usr/bin/env bash
# Crea un shim de pkg-config "webkit2gtk-4.0" / "javascriptcoregtk-4.0" que
# apunta a las librerías reales 4.1, para sistemas (Ubuntu 24.04+, Debian 13+,
# Fedora reciente...) que ya no traen el paquete -4.0.
#
# Uso:
#   ./linux-pkgconfig-shim.sh
#   PKG_CONFIG_PATH=/tmp/pkgconfig-shim CGO_ENABLED=1 go build -o httpscan .
set -euo pipefail

OUT_DIR="${1:-/tmp/pkgconfig-shim}"

if pkg-config --exists webkit2gtk-4.0 2>/dev/null; then
  echo "Tu sistema ya tiene webkit2gtk-4.0 nativamente, no necesitas este shim."
  echo "Compila directamente con: CGO_ENABLED=1 go build -o httpscan ."
  exit 0
fi

WEBKIT_PC=$(pkg-config --variable=pcfiledir webkit2gtk-4.1 2>/dev/null || true)
if [ -z "${WEBKIT_PC:-}" ]; then
  # fallback: buscar el .pc directamente
  WEBKIT_FILE=$(find /usr/lib* /usr/share/pkgconfig -name "webkit2gtk-4.1.pc" 2>/dev/null | head -1 || true)
else
  WEBKIT_FILE="$WEBKIT_PC/webkit2gtk-4.1.pc"
fi

if [ -z "${WEBKIT_FILE:-}" ] || [ ! -f "$WEBKIT_FILE" ]; then
  echo "No se encontró webkit2gtk-4.1.pc ni webkit2gtk-4.0.pc en el sistema."
  echo "Instala las dependencias primero, por ejemplo en Ubuntu/Debian:"
  echo "  sudo apt install build-essential pkg-config libgtk-3-dev libwebkit2gtk-4.1-dev"
  exit 1
fi

JS_FILE="$(dirname "$WEBKIT_FILE")/javascriptcoregtk-4.1.pc"

mkdir -p "$OUT_DIR"
cp "$WEBKIT_FILE" "$OUT_DIR/webkit2gtk-4.0.pc"
sed -i 's/^Name: .*/Name: WebKit2GTK (shim 4.0 sobre 4.1)/' "$OUT_DIR/webkit2gtk-4.0.pc"

if [ -f "$JS_FILE" ]; then
  cp "$JS_FILE" "$OUT_DIR/javascriptcoregtk-4.0.pc"
  sed -i 's/^Name: .*/Name: JavaScriptCoreGTK (shim 4.0 sobre 4.1)/' "$OUT_DIR/javascriptcoregtk-4.0.pc"
fi

echo "Shim creado en: $OUT_DIR"
echo ""
echo "Ahora compila con:"
echo "  PKG_CONFIG_PATH=$OUT_DIR CGO_ENABLED=1 go build -o httpscan ."

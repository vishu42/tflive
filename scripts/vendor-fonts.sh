#!/usr/bin/env bash
# Vendors the Minimalist Monochrome typefaces as latin-subset woff2 files into
# web/public/fonts/. Run once; the resulting files are committed to the repo so
# the console never depends on a third-party font host at runtime.
set -euo pipefail

DEST="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/web/public/fonts"
UA="Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

mkdir -p "$DEST"

# family-spec:output-basename pairs. The spec is the css2 API `family=` value.
fetch() {
  local spec="$1" out="$2"
  local css
  css="$(curl -sSf -A "$UA" "https://fonts.googleapis.com/css2?family=${spec}&display=swap")"

  # Google returns one @font-face per unicode subset, each preceded by a
  # `/* subset */` comment. Keep only the latin block's URL.
  local url
  url="$(printf '%s\n' "$css" \
    | awk '/\/\* latin \*\//{f=1} f&&/src: url\(/{print; exit}' \
    | sed -E 's/.*url\(([^)]+)\).*/\1/')"

  if [ -z "$url" ]; then
    echo "FAILED to resolve latin woff2 for ${spec}" >&2
    return 1
  fi

  curl -sSf -A "$UA" "$url" -o "${DEST}/${out}.woff2"
  echo "  ${out}.woff2  $(wc -c < "${DEST}/${out}.woff2" | tr -d ' ') bytes"
}

echo "Vendoring fonts into ${DEST}"
fetch "Playfair+Display:wght@400"        playfair-display-400
fetch "Playfair+Display:wght@700"        playfair-display-700
fetch "Playfair+Display:ital,wght@1,400" playfair-display-400-italic
fetch "Source+Serif+4:wght@400"          source-serif-4-400
fetch "Source+Serif+4:wght@600"          source-serif-4-600
fetch "JetBrains+Mono:wght@400"          jetbrains-mono-400
fetch "JetBrains+Mono:wght@500"          jetbrains-mono-500
echo "Done."

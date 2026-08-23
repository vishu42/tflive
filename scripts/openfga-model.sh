#!/usr/bin/env bash
# Transform the OpenFGA authorization model from its DSL source of truth into
# the committed JSON artifact that cmd/openfga-provisioner embeds.
#
#   scripts/openfga-model.sh check      exit non-zero when the committed JSON
#                                       is not a fresh transform of the DSL
#   scripts/openfga-model.sh generate   rewrite the committed JSON
#
# The DSL is what humans edit and review; the JSON is generated. Never edit the
# JSON by hand — `check` will reject it.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
dsl="$root/openfga/authorization-model.fga"
json="$root/openfga/authorization-model.json"

for tool in fga python3; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    echo "scripts/openfga-model.sh: $tool is required but not installed" >&2
    exit 127
  fi
done

# `fga model transform` emits minified JSON. Re-indent it so the generated
# artifact stays diffable, preserving the CLI's key order rather than sorting.
transform() {
  fga model transform --file "$dsl" --input-format fga --output-format json |
    python3 -m json.tool --indent 2
}

case "${1:-check}" in
  generate)
    transform > "$json"
    echo "wrote openfga/authorization-model.json from openfga/authorization-model.fga"
    ;;
  check)
    fresh="$(mktemp)"
    trap 'rm -f "$fresh"' EXIT
    transform > "$fresh"
    if ! diff -u "$json" "$fresh" >/dev/null; then
      echo "openfga/authorization-model.json is stale: it does not match a fresh transform of openfga/authorization-model.fga" >&2
      echo "run scripts/openfga-model.sh generate and commit the result" >&2
      diff -u --label openfga/authorization-model.json --label "transform of openfga/authorization-model.fga" "$json" "$fresh" >&2 || true
      exit 1
    fi
    echo "openfga/authorization-model.json matches openfga/authorization-model.fga"
    ;;
  *)
    echo "usage: scripts/openfga-model.sh [check|generate]" >&2
    exit 2
    ;;
esac

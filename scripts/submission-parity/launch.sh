#!/usr/bin/env bash
set -euo pipefail
script="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)/run.sh"
exec bash -c "$(cat -- "$script")" "$script" "$@"

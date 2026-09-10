#!/usr/bin/env bash
set -euo pipefail
script="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)/run.sh"
# Read the complete runner before executing long restores. Editing its source in
# another terminal cannot change a running shell's subsequent file reads.
exec bash -c "$(cat -- "$script")" "$script" "$@"

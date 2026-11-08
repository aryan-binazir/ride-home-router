#!/usr/bin/env bash
set -euo pipefail
exec python3 "$(dirname "$0")/tla_gate.py" "$@"

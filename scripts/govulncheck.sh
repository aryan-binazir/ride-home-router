#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
ignored=$(grep -oE '^GO-[0-9]+-[0-9]+' .govulncheck-ignore 2>/dev/null | sort -u || true)
reachable=$(go tool govulncheck -format json ./... | jq -r 'select(.finding.trace[0].function != null) | .finding.osv' | sort -u)
failing=$(comm -23 <(printf '%s\n' "$reachable" | sed '/^$/d') <(printf '%s\n' "$ignored" | sed '/^$/d'))
for id in $ignored; do echo "ignored: $id (see .govulncheck-ignore)"; done
if [ -n "$failing" ]; then
  echo "reachable vulnerabilities:"
  printf '  %s\n' $failing
  go tool govulncheck ./... || true
  exit 1
fi
echo "no unignored reachable vulnerabilities"

#!/usr/bin/env bash
# Model-check the TLA+ specs in verification/tla with TLC.
#
#   scripts/tla.sh            every spec
#   scripts/tla.sh Import     only specs whose name contains "Import"
#
# Each verification/tla/<Spec>[.<check>].cfg must finish with no error.
# Each verification/tla/mutations/<Spec>.<name>.cfg breaks a guard and must
# fail on the property named in its "\* expect: invariant|property <Name>"
# line, which proves the passing check can still fail.
#
# TLC is tla2tools.jar v1.7.4, pinned by checksum and cached outside the
# repo. Set TLA2TOOLS to use a local jar and JAVA to pick a Java 11+ binary.
set -euo pipefail

repo=$(cd "$(dirname "$0")/.." && pwd)
dir="$repo/verification/tla"
filter=${1:-}

version=1.7.4
checksum=936a262061c914694dfd669a543be24573c45d5aa0ff20a8b96b23d01e050e88
jar=${TLA2TOOLS:-${XDG_CACHE_HOME:-$HOME/.cache}/ride-home-router/tla2tools-$version.jar}

if [[ ! -f $jar ]]; then
	mkdir -p "$(dirname "$jar")"
	echo "Downloading tla2tools.jar $version to $jar" >&2
	download=$(mktemp "$jar.XXXXXX")
	if ! curl -fsSL --retry 3 -o "$download" "https://github.com/tlaplus/tlaplus/releases/download/v$version/tla2tools.jar"; then
		rm -f "$download"
		exit 1
	fi
	mv "$download" "$jar"
fi
if [[ -z ${TLA2TOOLS:-} ]] && ! echo "$checksum  $jar" | sha256sum -c --status; then
	echo "tla.sh: checksum mismatch for $jar; delete it and rerun" >&2
	exit 1
fi

java=${JAVA:-}
if [[ -z $java ]]; then
	if java -version >/dev/null 2>&1; then
		java=java
	elif command -v mise >/dev/null && java=$(mise which java --tool java@temurin-25 2>/dev/null); then
		:
	else
		echo "tla.sh: needs Java 11+ on PATH, in JAVA, or via 'mise install java@temurin-25'" >&2
		exit 1
	fi
fi

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

# tlc <spec.tla> <config> <log>
tlc() {
	local spec=$1 config=$2 log=$3
	(cd "$dir" && "$java" -XX:+UseParallelGC -Djava.io.tmpdir="$work" -cp "$jar" tlc2.TLC \
		-workers auto -cleanup -metadir "$work/states-$RANDOM" -config "$config" "$spec") >"$log" 2>&1 || true
}

failures=0
checks=0
report() {
	local status=$1 name=$2 detail=$3 log=$4
	checks=$((checks + 1))
	printf '%-4s %-60s %s\n' "$status" "$name" "$detail"
	if [[ $status != ok ]]; then
		failures=$((failures + 1))
		grep -E '^Error:' "$log" | head -n 5 | sed 's/^/     | /'
		echo '     | last lines of the TLC output (read the full trace with /statechart):'
		tail -n 25 "$log" | sed 's/^/     | /'
	fi
}

shopt -s nullglob
for config in "$dir"/*.cfg; do
	name=$(basename "$config")
	spec=${name%%.*}.tla
	[[ -z $filter || $name == *"$filter"* ]] || continue
	log="$work/$name.log"
	start=$SECONDS
	tlc "$spec" "$name" "$log"
	states=$(grep -oE '[0-9,]+ distinct states found, 0 states left' "$log" | tail -n 1 | cut -d' ' -f1 || true)
	if grep -q '^Model checking completed. No error has been found.' "$log"; then
		report ok "$name" "pass, ${states:-?} states, $((SECONDS - start))s" "$log"
	else
		report FAIL "$name" "expected a full pass" "$log"
	fi
done

for config in "$dir"/mutations/*.cfg; do
	name=$(basename "$config")
	spec=${name%%.*}.tla
	[[ -z $filter || $name == *"$filter"* ]] || continue
	read -r kind property < <(sed -n 's/^\\\* expect: \(invariant\|property\) \([A-Za-z0-9_]*\)$/\1 \2/p' "$config")
	log="$work/$name.log"
	start=$SECONDS
	tlc "$spec" "mutations/$name" "$log"
	case ${kind:-} in
	invariant) expected="^Error: Invariant $property is violated" ;;
	property) expected='^Error: Temporal properties were violated' ;;
	*)
		report FAIL "mutations/$name" "missing '\\* expect: invariant|property <Name>' line" "$log"
		continue
		;;
	esac
	if [[ $kind == property ]] && { [[ $(grep -cE '^(PROPERTY|PROPERTIES|INVARIANT|INVARIANTS)( |$)' "$config") != 1 ]] || ! grep -qx "PROPERTY $property" "$config"; }; then
		report FAIL "mutations/$name" "a property mutation must check only 'PROPERTY $property'" "$log"
	elif grep -qE "$expected" "$log"; then
		report ok "mutations/$name" "fails $property as expected, $((SECONDS - start))s" "$log"
	else
		report FAIL "mutations/$name" "expected $property to fail" "$log"
	fi
done

if ((checks == 0)); then
	echo "tla.sh: no config matches '$filter'" >&2
	exit 1
fi
if ((failures > 0)); then
	echo "tla.sh: $failures check(s) did not give the expected result" >&2
	exit 1
fi

#!/usr/bin/env bash
set -euo pipefail

if ! command -v lefthook >/dev/null 2>&1; then
	echo "lefthook not found. Install it first: https://lefthook.dev/installation/" >&2
	exit 127
fi

git config --unset core.hooksPath >/dev/null 2>&1 || true
lefthook install

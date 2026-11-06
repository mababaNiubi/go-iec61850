#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later
set -euo pipefail
project_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
cd -- "$project_root"
race=()
examples=false
for argument in "$@"; do
    case "$argument" in
        --race) race=(-race) ;;
        --examples) examples=true ;;
        *) printf 'usage: bash scripts/test.sh [--race] [--examples]\n' >&2; exit 2 ;;
    esac
done
mkdir -p _local/reports/unit
go test -json -count=1 -timeout=120s "${race[@]}" ./... | tee _local/reports/unit/go-tests.jsonl
go vet ./...
if $examples; then
    for example in mms/read-write mms/read-batch mms/browse mms/named-list mms/report goose/publish-subscribe sv/publish-subscribe sv/data-layout scd/breaker; do
        label=${example//\//-}
        go run "./examples/$example" | tee "_local/reports/unit/$label.log"
    done
fi
printf 'Go tests and static checks passed.\n'

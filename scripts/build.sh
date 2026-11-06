#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later
set -euo pipefail
project_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
cd -- "$project_root"
target_os=${GOOS:-$(go env GOOS)}
target_arch=${GOARCH:-$(go env GOARCH)}
[[ $target_os =~ ^[a-z0-9]+$ && $target_arch =~ ^[a-z0-9]+$ ]] || exit 2
output_root="_local/bin/$target_os-$target_arch"
mkdir -p -- "$output_root"
suffix=
if [[ $target_os == windows ]]; then suffix=.exe; fi
for app in mms-client mms-server goose-publisher goose-subscriber sv-publisher sv-subscriber; do
    name=${app//\//-}$suffix
    CGO_ENABLED=0 GOOS="$target_os" GOARCH="$target_arch" go build -trimpath -o "$output_root/$name" "./cmd/$app"
    printf '%s\n' "$output_root/$name"
done

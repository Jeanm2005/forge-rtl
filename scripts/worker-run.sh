#!/usr/bin/env bash
set -Eeuo pipefail
# stdout is reserved for the bounded artifact transport.
exec 3>&1
exec 1>&2
mkdir -p build
printf 'sandbox runner pending\n' > build/status
exec tar --format=ustar -cf /proc/self/fd/3 -C build .

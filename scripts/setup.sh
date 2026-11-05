#!/usr/bin/env bash
set -Eeuo pipefail
cd "$(dirname "$0")/.."
if [[ $(uname -s) != Linux || $(uname -m) != x86_64 ]]; then
    echo 'Supported setup platform: Linux x86-64 (including Ubuntu on WSL2).' >&2
    exit 1
fi
source scripts/toolchain.env
missing=0
for tool in curl tar gzip sha256sum g++ make git node timeout; do
    command -v "$tool" >/dev/null || missing=1
done
if [[ $missing == 1 ]]; then
    command -v apt-get >/dev/null || { echo 'Install the prerequisites listed in README.md.' >&2; exit 1; }
    admin=()
    if [[ $EUID != 0 ]]; then
        command -v sudo >/dev/null || { echo 'System dependencies require root or sudo.' >&2; exit 1; }
        admin=(sudo)
    fi
    "${admin[@]}" apt-get update
    "${admin[@]}" apt-get install -y --no-install-recommends \
        ca-certificates curl tar gzip coreutils build-essential git make nodejs
fi
node --input-type=module -e 'if (Number(process.versions.node.split(".")[0]) < 18) process.exit(1)'
mkdir -p .tools
install_dir="$PWD/.tools/oss-cad-suite"
if [[ ! -f $install_dir/.forge-toolchain-sha256 ]] ||
   [[ $(cat "$install_dir/.forge-toolchain-sha256") != "$OSS_CAD_SHA256" ]]; then
    staging=$(mktemp -d "$PWD/.tools/install.XXXXXX")
    trap 'rm -rf "$staging"' EXIT
    archive="$staging/$OSS_CAD_ARCHIVE"
    curl --fail --location --retry 3 --connect-timeout 15 --max-time 600 \
        "https://github.com/YosysHQ/oss-cad-suite-build/releases/download/$OSS_CAD_RELEASE/$OSS_CAD_ARCHIVE" \
        --output "$archive"
    printf '%s  %s\n' "$OSS_CAD_SHA256" "$archive" | sha256sum --check --strict
    tar -xzf "$archive" -C "$staging"
    for tool in verilator yosys eqy; do test -x "$staging/oss-cad-suite/bin/$tool"; done
    printf '%s\n' "$OSS_CAD_SHA256" > "$staging/oss-cad-suite/.forge-toolchain-sha256"
    rm -rf "$install_dir"
    mv "$staging/oss-cad-suite" "$install_dir"
fi
printf 'export PATH=%q:"$PATH"\n' "$install_dir/bin" > .tools/env.sh
export PATH="$install_dir/bin:$PATH"
verilator --version
yosys -V
eqy --version
printf '\nSetup complete. Run:\n  source .tools/env.sh\n  make check\n'

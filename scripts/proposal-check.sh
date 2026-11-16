#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
export PYTHONPATH="$PWD/proposal"
export PYTHONDONTWRITEBYTECODE=1
python -m ruff format --check proposal
python -m ruff check proposal
python -m mypy --config-file proposal/pyproject.toml --cache-dir build/mypy proposal
python -m unittest discover -s proposal/tests -v

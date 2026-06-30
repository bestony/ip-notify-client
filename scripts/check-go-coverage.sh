#!/usr/bin/env bash
set -euo pipefail

coverage_file="${COVERAGE_FILE:-coverage.out}"

go test ./... -covermode=atomic -coverprofile="${coverage_file}"

total_coverage="$(go tool cover -func="${coverage_file}" | awk '/^total:/ {print $NF}')"
echo "Go total statement coverage: ${total_coverage}"

if [[ "${total_coverage}" != "100.0%" ]]; then
  echo "coverage gate failed: expected 100.0%, got ${total_coverage}" >&2
  exit 1
fi

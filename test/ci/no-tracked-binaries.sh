#!/usr/bin/env bash
# Fails if any tracked file is a compiled executable or shared library
# (ELF, Mach-O, PE). Release binaries come from goreleaser with checksums
# and cosign signatures; a binary committed to the tree has neither and
# its source cannot be audited (#5993).
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

bad=$(git ls-files -z | xargs -0 file --mime-type -F '|' |
  grep -E '\| *application/(x-executable|x-pie-executable|x-sharedlib|x-mach-binary|x-dosexec|vnd\.microsoft\.portable-executable)$' |
  cut -d'|' -f1 || true)

if [ -n "$bad" ]; then
  echo "Tracked compiled binaries found; remove them with 'git rm --cached':" >&2
  echo "$bad" >&2
  exit 1
fi
echo "no tracked binaries"

#!/usr/bin/env bash

set -euo pipefail

repository_root="$(
  cd "$(dirname "${BASH_SOURCE[0]}")/../.." >/dev/null 2>&1
  pwd
)"
temporary_root="$(mktemp -d "${TMPDIR:-/tmp}/chronodesk-python-toolchain.XXXXXX")"
trap 'rm -rf "$temporary_root"' EXIT

venv_path="$temporary_root/alternate venv"
requirements_path="$temporary_root/requirements-test.txt"
bootstrap_path="$temporary_root/bootstrap-python"
bootstrap_log="$temporary_root/bootstrap.log"
runtime_log="$temporary_root/runtime.log"

printf 'example-dependency==1.0\n' >"$requirements_path"

cat >"$bootstrap_path" <<'BOOTSTRAP'
#!/usr/bin/env bash
set -euo pipefail

printf '%s\n' "$*" >>"$FAKE_BOOTSTRAP_LOG"
if [[ "$#" -ge 3 && "$1" == "-m" && "$2" == "pip" ]]; then
  printf 'error: externally-managed-environment\n' >&2
  exit 73
fi
if [[ "$#" -eq 3 && "$1" == "-m" && "$2" == "venv" ]]; then
  mkdir -p "$3/bin"
  cat >"$3/bin/python" <<'PYTHON'
#!/usr/bin/env bash
set -euo pipefail

printf '%s\n' "$*" >>"$FAKE_RUNTIME_LOG"
if [[ "$#" -eq 1 && "$1" == "--version" ]]; then
  printf 'Python test-double\n'
  exit 0
fi
if [[ "$#" -ge 3 && "$1" == "-m" && "$2" == "pip" ]]; then
  case "$3" in
    install)
      exit 0
      ;;
    check)
      printf 'No broken requirements found.\n'
      exit 0
      ;;
    --version)
      printf 'pip test-double\n'
      exit 0
      ;;
  esac
fi
printf 'unexpected fake venv Python invocation: %s\n' "$*" >&2
exit 64
PYTHON
  chmod +x "$3/bin/python"
  exit 0
fi
printf 'unexpected bootstrap Python invocation: %s\n' "$*" >&2
exit 64
BOOTSTRAP
chmod +x "$bootstrap_path"

export FAKE_BOOTSTRAP_LOG="$bootstrap_log"
export FAKE_RUNTIME_LOG="$runtime_log"

install_test_dependencies() {
  make --no-print-directory -s -C "$repository_root" \
    VENV="$venv_path" \
    BOOTSTRAP_PYTHON="$bootstrap_path" \
    PYTHON_REQUIREMENTS="$requirements_path" \
    install-test-deps
}

assert_line_count() {
  local expected="$1"
  local pattern="$2"
  local file="$3"
  local actual
  actual="$(grep -c -- "$pattern" "$file" 2>/dev/null || true)"
  if [[ "$actual" != "$expected" ]]; then
    printf 'expected %s occurrences of %q in %s, got %s\n' \
      "$expected" "$pattern" "$file" "$actual" >&2
    exit 1
  fi
}

install_test_dependencies
[[ -x "$venv_path/bin/python" ]]
assert_line_count 1 "-m venv $venv_path" "$bootstrap_log"
assert_line_count 0 "-m pip" "$bootstrap_log"
assert_line_count 1 "-m pip install -r $requirements_path" "$runtime_log"
assert_line_count 1 "-m pip check" "$runtime_log"

install_test_dependencies
assert_line_count 1 "-m venv $venv_path" "$bootstrap_log"
assert_line_count 0 "-m pip" "$bootstrap_log"
assert_line_count 1 "-m pip install -r $requirements_path" "$runtime_log"
assert_line_count 2 "-m pip check" "$runtime_log"

printf 'second-dependency==2.0\n' >>"$requirements_path"
install_test_dependencies
assert_line_count 1 "-m venv $venv_path" "$bootstrap_log"
assert_line_count 0 "-m pip" "$bootstrap_log"
assert_line_count 2 "-m pip install -r $requirements_path" "$runtime_log"
assert_line_count 3 "-m pip check" "$runtime_log"

for target in install-deps fmt fmt-check build-sdk test-sdk test-python-static smoke verify; do
  dry_run="$(
    make --no-print-directory -n -C "$repository_root" \
      VENV="$venv_path" \
      BOOTSTRAP_PYTHON="$bootstrap_path" \
      PYTHON_REQUIREMENTS="$requirements_path" \
      "$target"
  )"
  if ! grep -Fq -- "$venv_path/bin/python" <<<"$dry_run"; then
    printf '%s does not use the repository virtualenv Python:\n%s\n' \
      "$target" "$dry_run" >&2
    exit 1
  fi
  if grep -Eq '(^|[[:space:]])python3([[:space:]]|$)' <<<"$dry_run"; then
    printf '%s falls back to system python3:\n%s\n' "$target" "$dry_run" >&2
    exit 1
  fi
done

printf 'PEP 668 bootstrap safeguard passed.\n'
printf 'Python toolchain Make regression passed.\n'

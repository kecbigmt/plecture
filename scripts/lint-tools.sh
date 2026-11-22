#!/usr/bin/env bash
# Sourced, not executed: the one place the linter versions are pinned, read
# by every scripts/check-*.sh that runs a linter, so CI and a local
# check-all.sh run the same binaries.
#
# A tool is installed on first use into a versioned cache directory and
# reused afterwards. The release tarball of the one non-Go tool is verified
# against a pinned checksum, because nothing else vouches for its bytes;
# the Go tools are verified by the module checksum database on install.

STATICCHECK_MODULE="honnef.co/go/tools/cmd/staticcheck"
STATICCHECK_VERSION="v0.8.2"
GOLANGCI_LINT_MODULE="github.com/golangci/golangci-lint/v2/cmd/golangci-lint"
GOLANGCI_LINT_VERSION="v2.15.0"
ACTIONLINT_MODULE="github.com/rhysd/actionlint/cmd/actionlint"
ACTIONLINT_VERSION="v1.7.12"
SHELLCHECK_VERSION="v0.11.0"

lint_tools_dir() {
  printf '%s\n' "${PLECTURE_LINT_TOOLS_DIR:-${XDG_CACHE_HOME:-$HOME/.cache}/plecture/lint-tools}"
}

# An install stages in a sibling of its final directory and is renamed into
# place, so an interrupted install never leaves a half-written tool behind.
lint_tools_tmpdir() {
  mkdir -p "$(lint_tools_dir)"
  mktemp -d "$(lint_tools_dir)/.install.XXXXXX"
}

shellcheck_sha256() {
  case "$1" in
    linux.x86_64) echo 8c3be12b05d5c177a04c29e3c78ce89ac86f1595681cab149b65b97c4e227198 ;;
    linux.aarch64) echo 12b331c1d2db6b9eb13cfca64306b1b157a86eb69db83023e261eaa7e7c14588 ;;
    darwin.aarch64) echo 56affdd8de5527894dca6dc3d7e0a99a873b0f004d7aabc30ae407d3f48b0a79 ;;
    darwin.x86_64) echo 3c89db4edcab7cf1c27bff178882e0f6f27f7afdf54e859fa041fca10febe4c6 ;;
    *) return 1 ;;
  esac
}

sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | cut -d' ' -f1
  else
    shasum -a 256 "$1" | cut -d' ' -f1
  fi
}

# install_go_tool <name> <module> <version> prints the installed binary's path.
install_go_tool() {
  local name="$1" module="$2" version="$3"
  local dir
  dir="$(lint_tools_dir)/$name@$version"
  if [ ! -x "$dir/$name" ]; then
    local tmp
    tmp="$(lint_tools_tmpdir)"
    # GOWORK=off and an empty GOFLAGS: `go install module@version` must
    # resolve the tool's own pinned build list, never this repository's.
    if ! GOWORK=off GOFLAGS='' GOBIN="$tmp" go install "$module@$version" >&2; then
      rm -rf "$tmp"
      echo "lint-tools: go install $module@$version failed" >&2
      return 1
    fi
    rm -rf "$dir"
    mv "$tmp" "$dir"
  fi
  printf '%s\n' "$dir/$name"
}

install_shellcheck() {
  local dir platform want
  dir="$(lint_tools_dir)/shellcheck@$SHELLCHECK_VERSION"
  if [ -x "$dir/shellcheck" ]; then
    printf '%s\n' "$dir/shellcheck"
    return 0
  fi
  platform="$(uname -s | tr '[:upper:]' '[:lower:]').$(uname -m)"
  platform="${platform/arm64/aarch64}"
  if ! want="$(shellcheck_sha256 "$platform")"; then
    echo "lint-tools: no pinned shellcheck $SHELLCHECK_VERSION checksum for $platform; add one to scripts/lint-tools.sh" >&2
    return 1
  fi
  local url="${SHELLCHECK_RELEASE_BASE:-https://github.com/koalaman/shellcheck/releases/download}/$SHELLCHECK_VERSION/shellcheck-$SHELLCHECK_VERSION.$platform.tar.xz"
  local tmp
  tmp="$(lint_tools_tmpdir)"
  if ! curl -fsSL -o "$tmp/shellcheck.tar.xz" "$url"; then
    rm -rf "$tmp"
    echo "lint-tools: download failed: $url" >&2
    return 1
  fi
  local got
  got="$(sha256_of "$tmp/shellcheck.tar.xz")"
  if [ "$got" != "$want" ]; then
    rm -rf "$tmp"
    echo "lint-tools: checksum mismatch for $url: got $got, want $want" >&2
    return 1
  fi
  tar -xJf "$tmp/shellcheck.tar.xz" -C "$tmp"
  mkdir -p "$tmp/bin"
  mv "$tmp/shellcheck-$SHELLCHECK_VERSION/shellcheck" "$tmp/bin/shellcheck"
  rm -rf "$dir"
  mv "$tmp/bin" "$dir"
  rm -rf "$tmp"
  printf '%s\n' "$dir/shellcheck"
}

# lint_tool <name> installs the pinned tool if needed and prints its path.
lint_tool() {
  case "$1" in
    staticcheck) install_go_tool staticcheck "$STATICCHECK_MODULE" "$STATICCHECK_VERSION" ;;
    golangci-lint) install_go_tool golangci-lint "$GOLANGCI_LINT_MODULE" "$GOLANGCI_LINT_VERSION" ;;
    actionlint) install_go_tool actionlint "$ACTIONLINT_MODULE" "$ACTIONLINT_VERSION" ;;
    shellcheck) install_shellcheck ;;
    *) echo "lint-tools: unknown tool $1" >&2; return 1 ;;
  esac
}

# go_workspace_modules prints each module directory go.work uses, relative
# to the current directory, decoded by the go command rather than scanned.
go_workspace_modules() {
  go work edit -json go.work | jq -r '.Use[]?.DiskPath | ltrimstr("./")'
}

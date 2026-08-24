# Generic base image for a standalone `plect serve` deployment. It ships
# plect, the official catalog's plugin executables, the agent CLIs, and a
# neutral example config baked at /etc/plect — a downstream deployment
# overlays its own team-specific config on top by adding a later layer (see
# deploy/docker/README.md's "Extending this image" section), not by
# forking this Dockerfile.
#
# Every version below is an explicit pin (a base image digest, an exact
# package/CLI version, or a commit checked out before `docker build` runs).
# There is no `@latest` and no floating tag: a rebuild against the same
# checked-out commit reproduces the same image modulo the pinned upstream
# artifacts it downloads (gh, Node.js, and the npm-distributed agent CLIs),
# each pinned by exact version and, where upstream publishes one, verified
# by checksum. Debian's own package versions are not independently pinned
# beyond the base image digest — bookworm is Debian's frozen stable release,
# so its apt packages move only via security updates, an accepted trade-off
# against snapshot-pinning apt for this phase.

# ---- builder ---------------------------------------------------------
FROM golang:1.25.6-bookworm@sha256:f4490d7b261d73af4543c46ac6597d7d101b6e1755bcdd8c5159fda7046b6b3e AS builder

WORKDIR /src
COPY go.work go.work.sum ./
COPY app/ app/
COPY contracts/ contracts/
COPY plugins/ plugins/

RUN mkdir -p /out/bin \
  && go -C app build -o /out/bin/plect ./cmd/plect

# /opt/plect-repo is this image's path catalog source: the same repository
# layout a `git+https://github.com/kecbigmt/plecture --subdir plugins`
# catalog would fetch (FetchGit clones the whole repository; --subdir only
# narrows the trust/hash boundary, it never changes what's on disk
# alongside it), copied instead of cloned since the build context already
# has it at the exact commit being built. That full sibling layout is not
# optional: plugins/claude/src/channel-server's go.mod (like other
# in-repo plugin executables) `replace`s contracts/channel-protocol by a
# path relative to the repository root, which only resolves when
# contracts/ is physically present next to plugins/ at build time.
#
# `plect init` below mounts it through a `path://` source rather than
# `git+https://`. A path catalog mounts its source directory directly
# rather than caching a fetched snapshot (see docs/design/plugin-
# packaging.md's Lockfile section), so nothing at runtime needs network
# access to resolve it — the pin is simply "whatever commit this image was
# built from," which is why building this image from a checked-out commit
# is the whole reproducibility unit, not a git-revision field recorded
# anywhere in /etc/plect.
RUN mkdir -p /opt/plect-repo \
  && cp -a go.work go.work.sum app contracts plugins /opt/plect-repo/

# `plect init` registers that catalog, enables the neutral example plugin
# set, writes /etc/plect/config.toml's per-deployment fields, and — because
# tmux/claude/github's own plugin.toml declares `build` commands for their
# executables — compiles those executables into /opt/plect-repo/plugins in
# the same step (see docs/design/plugin-packaging.md's Executable Build
# Model). The resource_allowlist pattern below is a placeholder: a
# downstream deployment's own config.toml overlay is expected to replace it
# with the org/repo(s) it actually operates on (README's Extending section).
ENV HOME=/root
RUN /out/bin/plect init --yes \
  --config-home /etc/plect \
  --catalog-alias official \
  --catalog-source path:///opt/plect-repo \
  --catalog-subdir plugins \
  --enable tmux --enable claude --enable github \
  --allowlist '^https://github\.com/your-org/' \
  --workspace-dirs-root /var/lib/plect/workspace_dirs

COPY deploy/docker/example-config/workflows/ /etc/plect/workflows/

# Fails the build loud if the example config doesn't actually load and
# resolve — this is the one place a broken example ships silently
# otherwise, since nothing else in this Dockerfile ever loads it.
RUN /out/bin/plect workflow show example_work --config-home /etc/plect >/dev/null

# ---- runtime -----------------------------------------------------------
FROM debian:bookworm-slim@sha256:abd67ffcfa541b485a3dff59865ab629aa048a6c613e639d36e7456b0b229241 AS runtime

# git, tmux, jq, gh: the shared shell surface every plugin task in the
# example config (and most others in the official catalog) assumes is on
# PATH. runit: the outer supervisor (see deploy/docker/README.md's Process
# model section for the s6-overlay/supervisord/runit choice). curl: the bus
# healthcheck (`healthcheck.sh`) and this layer's own download steps.
# ca-certificates: TLS for gh/npm/the agent CLIs' own network calls.
# procps: `ps`/`kill`/etc. for CloudShell operational debugging (README's
# Operations section) — not load-bearing for the container itself.
RUN apt-get update -qq && apt-get install -y --no-install-recommends \
  ca-certificates=20250419~deb12u1 \
  curl=7.88.1-10+deb12u15 \
  git=1:2.39.5-0+deb12u3 \
  jq=1.6-2.1+deb12u2 \
  procps=2:4.0.2-3 \
  runit=2.1.2-54 \
  tmux=3.3a-3 \
  xz-utils=5.4.1-1+deb12u1 \
  && rm -rf /var/lib/apt/lists/* \
  # Debian's runit package registers its own placeholder "default-syslog"
  # service (literally `sleep 6000`, via runit-helper) as a side effect of
  # installation. This container supervises exactly one service; an
  # unexplained second `runsv` process is exactly the kind of drift the
  # restart/health acceptance criteria are meant to catch, not something
  # worth debugging later.
  && rm -f /etc/service/default-syslog

# gh CLI: an upstream release tarball, not apt's rolling package, so the
# version is this image's own pin rather than whatever bookworm's mirror
# currently carries.
ARG GH_VERSION=2.98.0
ARG GH_SHA256=3b8ac6b30336802fc1a858d7c084e11cdf24ac1a761ca90b68022d7d729208de
RUN curl -fsSL -o /tmp/gh.tar.gz \
  "https://github.com/cli/cli/releases/download/v${GH_VERSION}/gh_${GH_VERSION}_linux_amd64.tar.gz" \
  && echo "${GH_SHA256}  /tmp/gh.tar.gz" | sha256sum -c - \
  && tar -xzf /tmp/gh.tar.gz -C /usr/local --strip-components=1 "gh_${GH_VERSION}_linux_amd64/bin/gh" \
  && rm /tmp/gh.tar.gz

# Node.js: pinned tarball (checksum-verified) rather than apt's nodejs
# package or a floating NodeSource setup script, purely to run the two
# npm-distributed agent CLIs below — no application code runs on it.
ARG NODE_VERSION=24.19.0
ARG NODE_SHA256=14b342e71204f811bde6153be8e04b62aef63c236fef92b55f9c83154b409647
RUN curl -fsSL -o /tmp/node.tar.xz \
  "https://nodejs.org/dist/v${NODE_VERSION}/node-v${NODE_VERSION}-linux-x64.tar.xz" \
  && echo "${NODE_SHA256}  /tmp/node.tar.xz" | sha256sum -c - \
  && tar -xJf /tmp/node.tar.xz -C /usr/local --strip-components=1 \
  && rm /tmp/node.tar.xz

# Agent CLIs, exact versions (no `@latest`/`next` dist-tag). A downstream
# deployment that needs a different pin overlays these two ARGs at its own
# build, not by editing this Dockerfile.
ARG CLAUDE_CODE_VERSION=2.1.231
ARG CODEX_VERSION=0.149.1
RUN npm install -g --no-fund --no-audit \
  "@anthropic-ai/claude-code@${CLAUDE_CODE_VERSION}" \
  "@openai/codex@${CODEX_VERSION}" \
  && npm cache clean --force

# Fixed, non-default uid/gid: an ECS EFS access point maps to a specific
# numeric owner, so this value is part of the container's contract with its
# Terraform, not an implementation detail free to float between rebuilds.
ARG PLECT_UID=10000
ARG PLECT_GID=10000
RUN groupadd -g "${PLECT_GID}" plect \
  && useradd -u "${PLECT_UID}" -g "${PLECT_GID}" -M -s /usr/sbin/nologin plect

COPY --from=builder /out/bin/plect /usr/local/bin/plect
# Only plugins/ — the catalog's own subdir, recorded as such in
# /etc/plect/catalogs.toml — needs to survive into the runtime image.
# app/ and contracts/ earned their keep only as the builder stage's
# go.mod-`replace` targets (see the comment on /opt/plect-repo above); a
# `plect` invocation resolving this path catalog at runtime never reads
# them, so they don't ride along into the deployed image.
COPY --from=builder /opt/plect-repo/plugins /opt/plect-repo/plugins
COPY --from=builder /etc/plect /etc/plect
# `plect init` wrote catalogs.toml/plect.lock 0600 as the builder stage's
# root — correct for a file just written on a developer machine, wrong
# once this image runs as the unprivileged `plect` user, which only ever
# reads this tree. `chpst -u` in service/plect-serve/run drops to that
# user for every plect invocation, so config.toml, catalogs.toml, and
# plect.lock all need to stay readable by it.
RUN chmod -R a+rX /etc/plect

COPY deploy/docker/entrypoint.sh /usr/local/bin/entrypoint.sh
COPY deploy/docker/healthcheck.sh /usr/local/bin/plect-healthcheck
COPY deploy/docker/service/ /etc/service/
RUN chmod +x /usr/local/bin/entrypoint.sh /usr/local/bin/plect-healthcheck /etc/service/plect-serve/run

# This env block is the runtime layout contract documented in README.md's
# Persistence section: HOME/XDG_*/PLECT_BUS_SOCKET under /var/lib/plect
# (the single ECS-volume mount point) except XDG_RUNTIME_DIR, which is
# deliberately not persisted (entrypoint.sh recreates it fresh every boot).
ENV PLECT_CONFIG_HOME=/etc/plect \
  HOME=/var/lib/plect/home \
  XDG_DATA_HOME=/var/lib/plect/data \
  XDG_STATE_HOME=/var/lib/plect/state \
  XDG_RUNTIME_DIR=/run/plect \
  PLECT_BUS_SOCKET=/run/plect/bus.sock \
  TMPDIR=/tmp \
  PATH=/usr/local/bin:$PATH

VOLUME ["/var/lib/plect"]

# --unix-socket needs no port/host — the bus never listens on TCP.
HEALTHCHECK --interval=30s --timeout=5s --start-period=30s --retries=3 \
  CMD ["/usr/local/bin/plect-healthcheck"]

# entrypoint.sh starts as root (needed once per boot to fix ownership of a
# freshly attached, root-owned ECS volume — see its own comments) and never
# execs away from PID 1: modern bash (>= 4.4) reaps arbitrary reparented
# children when it IS pid 1, which is the property this image actually
# needs from an init, on top of runit's start/restart-on-crash supervision
# of the one process (`plect serve`) this container runs.
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]

ARG BASE_IMAGE=debian:bookworm-slim
FROM ${BASE_IMAGE}
COPY --chown=10000:10000 claude /usr/local/bin/claude
COPY workspace.toml /etc/plect/workspaces/fault.toml
COPY workflow.toml /etc/plect/workflows/fault.toml
COPY workflow_nested.toml /etc/plect/workflows/fault_nested.toml
COPY guarded_runtime.toml /etc/plect/tasks/fault_guarded_runtime.toml
COPY entrypoint.sh /usr/local/bin/fault-entrypoint
RUN chmod +x /usr/local/bin/fault-entrypoint
ENV PLECT_CONFIG_HOME=/var/lib/plect/fault-config \
    XDG_CACHE_HOME=/var/lib/plect/fault-cache
ENTRYPOINT ["/usr/local/bin/fault-entrypoint"]

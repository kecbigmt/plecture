ARG BASE_IMAGE=debian:bookworm-slim
FROM ${BASE_IMAGE}
COPY --chown=10000:10000 claude /usr/local/bin/claude
COPY workspace.toml /etc/plect/workspaces/fault.toml
COPY workflow.toml /etc/plect/workflows/fault.toml
COPY entrypoint.sh /usr/local/bin/fault-entrypoint
COPY service-run /etc/service/plect-serve/run
RUN chmod +x /usr/local/bin/fault-entrypoint /etc/service/plect-serve/run
ENV PLECT_CONFIG_HOME=/var/lib/plect/fault-config \
    XDG_CACHE_HOME=/var/lib/plect/fault-cache
ENTRYPOINT ["/usr/local/bin/fault-entrypoint"]

FROM python:3.11-slim

# Create a non-root user matching the distroless convention (UID 65534 = nobody).
RUN groupadd --gid 65534 nonroot && \
    useradd  --uid 65534 --gid 65534 --no-create-home --shell /usr/sbin/nologin nonroot

WORKDIR /workspace

# Drop to non-root for all subsequent layers and at runtime.
USER 65534:65534

# No extra packages are installed.  Tool scripts bring their own dependencies
# via pip install at Job-init time, injected by the executor before running
# the entrypoint.

# No shell entrypoint — the executor calls python3.11 <entrypoint> directly.

FROM alpine:3.20

# Install only the tools bash scripts typically need.
RUN apk add --no-cache bash jq git

# Non-root user (UID 65534 = nobody, already exists in Alpine).
USER 65534:65534

WORKDIR /workspace

FROM golang:1.22-alpine

# Non-root user (UID 65534 = nobody, already exists in Alpine).
USER 65534:65534

WORKDIR /workspace

# ---------------------------------------------------------------------------- #
# Stage 1 – builder
# ---------------------------------------------------------------------------- #
FROM golang:1.22-alpine AS builder

WORKDIR /src

# Cache module downloads separately from compilation.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags="-s -w" -o /out/agent ./cmd/agent

# ---------------------------------------------------------------------------- #
# Stage 2 – minimal runtime (distroless/static, no shell, no libc)
# ---------------------------------------------------------------------------- #
FROM gcr.io/distroless/static:nonroot

# nonroot user in distroless/static:nonroot is UID 65534.
USER 65534:65534

COPY --from=builder /out/agent /agent

EXPOSE 8080

ENTRYPOINT ["/agent"]

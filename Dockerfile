# syntax=docker/dockerfile:1.7

# ---- builder ----
FROM golang:1.25-alpine AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=docker
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w -X main.version=${VERSION}" \
    -o /out/gaf-exporter \
    ./cmd/gaf-exporter

# ---- runtime ----
FROM gcr.io/distroless/static:nonroot

COPY --from=builder /out/gaf-exporter /gaf-exporter

EXPOSE 9876

# distroless has no shell or curl, so the binary healthchecks itself.
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD ["/gaf-exporter", "--healthcheck"]

USER nonroot:nonroot

ENTRYPOINT ["/gaf-exporter"]
CMD ["--config", "/etc/gafferstape/config.yaml"]

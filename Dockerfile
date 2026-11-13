FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS builder
RUN apk add --no-cache git
WORKDIR /app
COPY src/go.mod src/go.sum ./
RUN go mod download
COPY src/ ./
ARG VERSION=dev
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build \
    -ldflags="-s -w -X github.com/Duops/SherlockOps/internal/version.Version=${VERSION}" \
    -o /sherlockops ./cmd/sherlockops

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -S appgroup && adduser -S appuser -G appgroup
COPY --from=builder /sherlockops /usr/local/bin/sherlockops
COPY config/config.example.yaml /etc/sherlockops/config.yaml
RUN mkdir -p /data && chown appuser:appgroup /data
EXPOSE 8080 8081
VOLUME ["/data"]
USER appuser
ENTRYPOINT ["sherlockops"]
CMD ["-config", "/etc/sherlockops/config.yaml"]

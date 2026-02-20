# Stage 1: Build the Go binary (runs on the build host, cross-compiles for target).
FROM --platform=$BUILDPLATFORM golang:1.26.0 AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
ARG VERSION=dev
ARG COMMIT=unknown
ARG DATE=unknown
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath \
    -ldflags "-w -extldflags '-static' \
    -X 'main.version=${VERSION}' \
    -X 'main.commit=${COMMIT}' \
    -X 'main.date=${DATE}'" \
    -o muster .

# Stage 2: Minimal scratch-based runtime.
FROM gsoci.azurecr.io/giantswarm/alpine:3.20.3-giantswarm AS certs
FROM scratch

COPY --from=certs /etc/passwd /etc/passwd
COPY --from=certs /etc/group /etc/group
COPY --from=certs /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

COPY --from=builder /app/muster /muster
USER giantswarm

ENTRYPOINT ["/muster"]

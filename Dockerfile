# The certs stage runs on the build platform (its files are arch-independent):
# the current CA bundle from alpine's ca-certificates, and the giantswarm user
# (UID/GID 1000, as the chart's security context sets) that the scratch image
# names.
FROM --platform=$BUILDPLATFORM gsoci.azurecr.io/giantswarm/alpine:3.24.2 AS certs
RUN apk add --no-cache ca-certificates \
 && addgroup -g 1000 giantswarm \
 && adduser -D -H -u 1000 -G giantswarm -s /sbin/nologin giantswarm

FROM scratch

COPY --from=certs /etc/passwd /etc/passwd
COPY --from=certs /etc/group /etc/group
COPY --from=certs /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

ARG TARGETOS
ARG TARGETARCH
COPY muster-${TARGETOS}-${TARGETARCH} /muster
USER giantswarm

ENTRYPOINT ["/muster"]

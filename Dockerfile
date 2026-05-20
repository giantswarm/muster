FROM gsoci.azurecr.io/giantswarm/alpine:3.20.3-giantswarm AS certs
FROM scratch

COPY --from=certs /etc/passwd /etc/passwd
COPY --from=certs /etc/group /etc/group
COPY --from=certs /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

ARG TARGETOS
ARG TARGETARCH
COPY muster-${TARGETOS}-${TARGETARCH} /muster
USER giantswarm

ENTRYPOINT ["/muster"]

FROM gcr.io/distroless/base-debian12:nonroot

# Copy the muster binary with execute permissions
COPY --chmod=755 muster /usr/local/bin/muster

# Switch to non-root user (distroless already has nonroot user)
USER nonroot:nonroot

# Set working directory
WORKDIR /home/nonroot

# Expose port
EXPOSE 8090

# Default command
ENTRYPOINT ["/usr/local/bin/muster"]

FROM alpine:3.23

# Build-time variables
# IMAGE_NAME is passed from docker-release-action (the 'project' parameter)
ARG IMAGE_NAME
# TARGETARCH and TARGETOS are automatically set by Docker buildx
ARG TARGETARCH
ARG TARGETOS

# Copy the ARG value to an ENV variable that will persist at runtime
ENV IMAGE_NAME=${IMAGE_NAME}

# Install runtime dependencies
# Use --no-scripts to avoid trigger errors with QEMU emulation on ARM64 (Alpine 3.23 issue)
RUN apk update && \
    apk add --no-cache --no-scripts git ca-certificates wget && \
    update-ca-certificates 2>/dev/null || true

# Install kustomize
ARG KUSTOMIZE_VERSION=v5.3.0
RUN wget -O /tmp/kustomize.tar.gz \
    "https://github.com/kubernetes-sigs/kustomize/releases/download/kustomize%2F${KUSTOMIZE_VERSION}/kustomize_${KUSTOMIZE_VERSION}_linux_${TARGETARCH}.tar.gz" && \
    tar -xzf /tmp/kustomize.tar.gz -C /usr/local/bin && \
    rm /tmp/kustomize.tar.gz && \
    chmod +x /usr/local/bin/kustomize

# Install helm (needed for --enable-helm flag)
ARG HELM_VERSION=v3.16.2
RUN wget -O /tmp/helm.tar.gz \
    "https://get.helm.sh/helm-${HELM_VERSION}-linux-${TARGETARCH}.tar.gz" && \
    tar -xzf /tmp/helm.tar.gz -C /tmp && \
    mv /tmp/linux-${TARGETARCH}/helm /usr/local/bin/helm && \
    rm -rf /tmp/helm.tar.gz /tmp/linux-${TARGETARCH} && \
    chmod +x /usr/local/bin/helm

# Create a non-root user with UID 1001 to match GitHub Actions runner
RUN addgroup -g 1001 ${IMAGE_NAME} && \
    adduser -D -u 1001 -G ${IMAGE_NAME} ${IMAGE_NAME}

# Configure git to trust any directory (safe for container use)
# This must be done as root before switching to non-root user
RUN git config --system --add safe.directory '*'

# Copy the pre-built binary from GoReleaser's dist directory
# GoReleaser creates binaries in dist/${IMAGE_NAME}_${TARGETOS}_${TARGETARCH}_v1/ for amd64
# and dist/${IMAGE_NAME}_${TARGETOS}_${TARGETARCH}/ for arm64
COPY dist/${IMAGE_NAME}_${TARGETOS}_${TARGETARCH}*/${IMAGE_NAME} /usr/local/bin/${IMAGE_NAME}

# Set proper ownership and permissions
RUN chmod +x /usr/local/bin/${IMAGE_NAME} && \
    chown ${IMAGE_NAME}:${IMAGE_NAME} /usr/local/bin/${IMAGE_NAME}

# Switch to the non-root user & set working directory
USER ${IMAGE_NAME}
WORKDIR /home/${IMAGE_NAME}

# Use exec form with environment variable substitution
ENTRYPOINT ["/bin/sh", "-c", "/usr/local/bin/${IMAGE_NAME}"]

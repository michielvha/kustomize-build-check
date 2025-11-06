FROM alpine:3.22

# Build-time variables
# IMAGE_NAME is passed from docker-release-action (the 'project' parameter)
ARG IMAGE_NAME
# TARGETARCH and TARGETOS are automatically set by Docker buildx
ARG TARGETARCH
ARG TARGETOS

# Install runtime dependencies
RUN apk add --no-cache git ca-certificates wget

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

# Copy the pre-built binary from GoReleaser's dist directory
# GoReleaser creates binaries in dist/${IMAGE_NAME}_${TARGETOS}_${TARGETARCH}_v1/ for amd64
# and dist/${IMAGE_NAME}_${TARGETOS}_${TARGETARCH}/ for arm64
COPY dist/${IMAGE_NAME}_${TARGETOS}_${TARGETARCH}*/${IMAGE_NAME} /usr/local/bin/${IMAGE_NAME}
RUN chmod +x /usr/local/bin/${IMAGE_NAME}

# Use the binary name from IMAGE_NAME variable
ENTRYPOINT ["/bin/sh", "-c", "/usr/local/bin/${IMAGE_NAME}"]

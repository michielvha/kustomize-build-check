# syntax=docker/dockerfile:1

# ---- fetch stage -------------------------------------------------------------
# Downloads run on the BUILD platform, not the target platform. Fetching under
# QEMU emulation was the source of the ARM64 apk trigger workaround this image
# used to carry; doing it here removes the need for that entirely.
#
# The runtime base ships no wget and no curl, which is the other reason these
# downloads cannot happen there.
FROM --platform=$BUILDPLATFORM cgr.dev/chainguard/wolfi-base@sha256:0a8fd427de5882aed77471b0a432c3675eda6b6a0ae952b5d640b46da628cdbe AS fetch

ARG TARGETARCH
ARG KUSTOMIZE_VERSION=v5.8.1
ARG HELM_VERSION=v4.2.3

# wolfi-base defaults to a non-root user, so package installs need an explicit
# switch. The fetch stage is discarded, so root here never reaches the image.
USER root
# tar is not a separate package here; busybox provides /usr/bin/tar.
RUN apk add --no-cache curl

WORKDIR /out

# kustomize and helm are static Go binaries, verified with `file` on the released
# artifacts, which is what makes them safe to carry into a glibc base from a
# musl-free build. Anything added here that is dynamically linked would need
# re-verifying; see NF-05.
RUN curl -sfL -o kustomize.tar.gz \
      "https://github.com/kubernetes-sigs/kustomize/releases/download/kustomize%2F${KUSTOMIZE_VERSION}/kustomize_${KUSTOMIZE_VERSION}_linux_${TARGETARCH}.tar.gz" && \
    tar -xzf kustomize.tar.gz kustomize && \
    rm kustomize.tar.gz && \
    chmod 0755 kustomize

RUN curl -sfL -o helm.tar.gz \
      "https://get.helm.sh/helm-${HELM_VERSION}-linux-${TARGETARCH}.tar.gz" && \
    tar -xzf helm.tar.gz "linux-${TARGETARCH}/helm" && \
    mv "linux-${TARGETARCH}/helm" helm && \
    rm -rf helm.tar.gz "linux-${TARGETARCH}" && \
    chmod 0755 helm

# ---- runtime stage -----------------------------------------------------------
# Wolfi rather than alpine, to cut CVE surface. Not distroless: this tool shells
# out to git, and replacing that with a pure-Go implementation would cost 48
# extra modules and change behaviour for a partial win. See
# docs/specs/container-hardening.spec.md.
FROM cgr.dev/chainguard/wolfi-base@sha256:0a8fd427de5882aed77471b0a432c3675eda6b6a0ae952b5d640b46da628cdbe

ARG IMAGE_NAME
ARG TARGETOS
ARG TARGETARCH
ENV IMAGE_NAME=${IMAGE_NAME}

# git is the one runtime dependency that cannot be a static binary. It arrives
# via apk, dynamically linked against this base's own glibc, which is fine
# because apk installs its dependency closure into the same image. It is proven
# by EXECUTING it as the runtime user, not by `file`.
# Same here: root only for the install, then dropped before the entrypoint.
USER root
RUN apk add --no-cache git ca-certificates

COPY --from=fetch /out/kustomize /usr/local/bin/kustomize
COPY --from=fetch /out/helm      /usr/local/bin/helm

# UID 1001 matches the GitHub Actions runner, so the workspace bind mount is
# writable without a chown.
RUN adduser -D -u 1001 runner

# Trust any directory: the workspace is owned by the runner user, not by us.
# Must run as root, before the USER switch.
RUN git config --system --add safe.directory '*'

# The binary is installed at a name-agnostic path so the entrypoint can be
# exec-form without hardcoding the workload name, which vega.yaml owns.
COPY dist/${IMAGE_NAME}_${TARGETOS}_${TARGETARCH}*/${IMAGE_NAME} /usr/local/bin/entrypoint
RUN chmod 0755 /usr/local/bin/entrypoint

USER 1001
WORKDIR /home/runner

# Exec form, single element, no shell in the process tree.
ENTRYPOINT ["/usr/local/bin/entrypoint"]

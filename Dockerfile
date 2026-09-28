# The bench image: one static Go binary (load generator, both samplers, report).
#
#   docker build --build-arg VERSION=$(git rev-parse --short HEAD) -t vllm-serve-bench .
#   docker run --rm vllm-serve-bench version
#
# Base images are pinned by digest, like the engine image: a re-pushed tag must
# not change what built or ran the numbers.

# Build on the runner's native platform and cross-compile, so a multi-arch
# build does not run the Go toolchain under emulation.
FROM --platform=$BUILDPLATFORM golang:1.25-bookworm@sha256:3b4a11519ad929d1e1d261a12cff056f0c85b735253d7d861346b9c6f8b36437 AS build
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
WORKDIR /src
# go.mod has no requirements (standard library only), so there is no module
# download layer to cache separately.
COPY go.mod ./
COPY cmd/ cmd/
COPY internal/ internal/
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/bench ./cmd/bench

# distroless/base, not distroless/static: the Go binary is static and would run
# on either, but the GPU sampler execs `nvidia-smi`, which the NVIDIA container
# toolkit injects from the host at run time (a CDI device in Compose, the device
# plugin in Kubernetes). nvidia-smi is a dynamically linked glibc program;
# distroless/static has no glibc, so it would fail to start there. Where no GPU
# is injected at all, run with --gpu-sampler=false.
FROM gcr.io/distroless/base-debian12:nonroot@sha256:7f0c72cd138b442ae0deeb69c08b1acf5525439ba251a49ad93c320a061567e5
ARG VERSION=dev
LABEL org.opencontainers.image.source="https://github.com/wrbooth/vllm-serve-bench" \
      org.opencontainers.image.title="vllm-serve-bench" \
      org.opencontainers.image.description="Closed-loop load generator, 1 Hz telemetry samplers and report tool for a vLLM engine" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.version="${VERSION}"
COPY --from=build /out/bench /usr/local/bin/bench
# Relative --out paths land here; mount a volume on it to keep results.
WORKDIR /results
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/bench"]
CMD ["version"]

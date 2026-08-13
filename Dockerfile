# syntax=docker/dockerfile:1
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    for attempt in 1 2 3; do \
      go mod download && exit 0; \
      if [ "$attempt" -eq 3 ]; then exit 1; fi; \
      sleep 2; \
    done
COPY . .
ARG VERSION=dev
ARG COMMIT=none
ARG BUILD_DATE=unknown
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${BUILD_DATE}" \
    -o /out/tubicen ./cmd/tubicen

FROM prom/prometheus:v3.13.2 AS prometheus

FROM gcr.io/distroless/base-debian12 AS runtime-base
COPY --from=build /out/tubicen /usr/local/bin/tubicen
COPY --from=prometheus /bin/promtool /usr/local/bin/promtool
ENTRYPOINT ["/usr/local/bin/tubicen"]
CMD ["help"]

# Local and scheduled container runs do not need workspace write access as root.
FROM runtime-base AS cli
USER nonroot:nonroot

# GitHub mounts GITHUB_WORKSPACE for a container action using the default user.
FROM runtime-base AS action

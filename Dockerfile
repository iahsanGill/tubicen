# syntax=docker/dockerfile:1
ARG PROMETHEUS_VERSION=v3.13.2

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

FROM prom/prometheus:${PROMETHEUS_VERSION} AS prometheus

FROM gcr.io/distroless/base-debian12:nonroot
COPY --from=build /out/tubicen /usr/local/bin/tubicen
COPY --from=prometheus /bin/promtool /usr/local/bin/promtool
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/tubicen"]
CMD ["help"]

# syntax=docker/dockerfile:1
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY cmd/demo-service ./cmd/demo-service
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/checkout-api ./cmd/demo-service

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/checkout-api /checkout-api
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/checkout-api"]

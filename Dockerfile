# pin the build stage to the runner's own arch so the compiler never runs emulated
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build

# buildx provides these automatically, one build per requested platform
ARG TARGETOS
ARG TARGETARCH

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
# cgo off produces a static binary, which is what distroless/static needs
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
  go build -trimpath -ldflags="-s -w" -o /qbittorrent-peers-exporter .

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /qbittorrent-peers-exporter /qbittorrent-peers-exporter

EXPOSE 9714
USER nonroot:nonroot
ENTRYPOINT ["/qbittorrent-peers-exporter"]

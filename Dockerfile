# Build a static host-native binary; the dashboard runs as a systemd service on the host,
# so it can safely call the host's Docker Compose CLI for explicit manual updates.
FROM --platform=$BUILDPLATFORM golang:1.24-alpine AS build
ARG TARGETOS=linux
ARG TARGETARCH=amd64
WORKDIR /src
COPY go.mod ./
COPY main.go ./
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -ldflags="-s -w" -o /out/container-watch .

FROM scratch AS artifacts
COPY --from=build /out/container-watch /container-watch

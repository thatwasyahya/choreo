# Build the server.
FROM golang:1.27-alpine AS build

WORKDIR /src

# Dependencies first: this layer only changes when go.mod or go.sum do, so a
# code edit does not re-download the module cache.
COPY server/go.mod server/go.sum ./
RUN go mod download

COPY server/ ./

ARG VERSION=dev
# CGO off so the binary carries nothing from the build image.
RUN CGO_ENABLED=0 go build \
        -trimpath \
        -ldflags "-s -w -X main.version=${VERSION}" \
        -o /out/choreod \
        ./cmd/choreod

# Run it.
#
# Distroless: no shell, no package manager, no busybox. If someone gets code
# execution, there is nothing in the image to pivot with. Operating rule 6.
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/choreod /choreod

USER nonroot:nonroot
EXPOSE 8080

ENTRYPOINT ["/choreod"]

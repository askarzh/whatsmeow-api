# syntax=docker/dockerfile:1

FROM golang:1.26-alpine AS builder
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w -X main.version=${VERSION}" \
    -o /out/whatsmeow-api \
    ./cmd/whatsmeow-api

# Pre-create an empty /data with nonroot ownership so a Docker named volume
# mounted at /data inherits that ownership on first init (Docker copies the
# image directory's metadata into a fresh named volume). Without this the
# volume ends up root-owned and the nonroot daemon can't write to it.
RUN mkdir -p /out/data

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /out/whatsmeow-api /whatsmeow-api
COPY --from=builder --chown=nonroot:nonroot /out/data /data

USER nonroot
EXPOSE 8080
ENTRYPOINT ["/whatsmeow-api"]
CMD ["serve"]

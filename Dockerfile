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

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /out/whatsmeow-api /whatsmeow-api

USER nonroot
EXPOSE 8080
ENTRYPOINT ["/whatsmeow-api"]
CMD ["serve"]

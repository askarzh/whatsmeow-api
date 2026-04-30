FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/whatsmeow-api ./cmd/whatsmeow-api

FROM alpine:3.20
RUN apk add --no-cache ca-certificates ffmpeg && adduser -D -H app
USER app
WORKDIR /app
COPY --from=build /out/whatsmeow-api /usr/local/bin/whatsmeow-api
EXPOSE 8080
ENTRYPOINT ["whatsmeow-api"]
CMD ["serve"]

# Self-host whatsmeow-api with docker-compose

Two profiles ship in this directory:

- **sqlite** — single container, file-backed. Simpler.
- **postgres** — daemon + Postgres container. Production-shaped.

## Quickstart (SQLite)

    cp .env.example .env
    # edit .env: set WMAPI_AUTH_TOKEN to a strong random value
    #            (e.g. `openssl rand -hex 32`)

    docker compose --profile sqlite up -d

The daemon listens on `http://localhost:8080`. The data directory is a
named Docker volume (`whatsmeow-api_daemon-data`), so your pairing
survives restarts.

## Quickstart (Postgres)

    cp .env.example .env
    # edit .env:
    #   WMAPI_AUTH_TOKEN=...
    #   WMAPI_STORAGE_BACKEND=postgres
    #   WMAPI_STORAGE_POSTGRES_DSN=postgres://whatsmeow:whatsmeow@postgres:5432/whatsmeow_api?sslmode=disable
    #   POSTGRES_PASSWORD=whatsmeow  # any value, must match the DSN

    docker compose --profile postgres up -d

## Pair your WhatsApp account

After the daemon is up, scan a QR code with your phone:

    export WMAPI_TOKEN=<the value you set in .env>

    curl -N -H "Authorization: Bearer $WMAPI_TOKEN" \
         http://localhost:8080/v1/login/qr

The SSE stream emits `qr` events containing the data to encode. Render
the QR (any library that accepts a string) and scan it on
WhatsApp → Settings → Linked Devices → Link a Device.

Alternatively, request a phone-pair code:

    curl -X POST -H "Authorization: Bearer $WMAPI_TOKEN" \
         -H "Content-Type: application/json" \
         -d '{"phone":"+15551234567"}' \
         http://localhost:8080/v1/login/phone

The 8-character code is emitted on the SSE response stream; type it on
the linked-device screen.

After pairing, `curl -H "Authorization: Bearer $WMAPI_TOKEN"
http://localhost:8080/v1/status` returns your JID and
`wa_connected: true`.

## Inspecting the running container

The image is distroless (no shell), so `docker compose exec daemon sh`
won't work. Use logs and HTTP for diagnostics:

    docker compose logs -f daemon
    curl -H "Authorization: Bearer $WMAPI_TOKEN" \
         http://localhost:8080/v1/stats

## Bind mounts vs. named volumes

The compose file uses a named volume for `/data`. If you prefer a bind
mount (e.g. `- ./data:/data`), make sure the host directory is writable
by UID 65532 (the `nonroot` user inside the distroless image):

    mkdir -p ./data && sudo chown 65532:65532 ./data

## Tearing down

    docker compose --profile sqlite down            # keeps the volume
    docker compose --profile sqlite down --volumes  # deletes pairing data

See `../cookbook.md` for a curl recipe per HTTP endpoint.

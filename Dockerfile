# syntax=docker/dockerfile:1

FROM golang:1.23 AS builder
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/harvester ./cmd/harvester

FROM gcr.io/distroless/base-debian12:nonroot
WORKDIR /app
COPY --from=builder /out/harvester /app/harvester

EXPOSE 8765
VOLUME ["/data"]

USER nonroot

ENTRYPOINT ["/app/harvester"]

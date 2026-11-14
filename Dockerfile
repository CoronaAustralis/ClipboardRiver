FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev

RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w -X main.version=$VERSION" -o /out/cb-river-server ./cmd/cb-river-server

FROM alpine:3.22

WORKDIR /app

RUN apk add --no-cache ca-certificates && \
    addgroup -S cbr && \
    adduser -S -G cbr cbr && \
    mkdir -p /app/data && \
    chown -R cbr:cbr /app

COPY --from=build /out/cb-river-server /app/cb-river-server

EXPOSE 8080
VOLUME ["/app/data"]

CMD ["./cb-river-server", "--config", "/app/data/config.json"]

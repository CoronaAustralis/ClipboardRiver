FROM golang:1.26-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /out/cb-river-server ./cmd/cb-river-server

FROM alpine:3.22

WORKDIR /app

RUN apk add --no-cache ca-certificates && \
    addgroup -S cbr && \
    adduser -S -G cbr cbr && \
    mkdir -p /app/data && \
    chown -R cbr:cbr /app

COPY --from=build /out/cb-river-server /app/cb-river-server

ENV CBR_CONFIG=./data/config.json

EXPOSE 8080
VOLUME ["/app/data"]

USER cbr

CMD ["./cb-river-server"]

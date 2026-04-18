FROM golang:1.22-alpine AS builder

ARG TARGETOS
ARG TARGETARCH

RUN apk add --no-cache upx tzdata

WORKDIR /src
COPY go.mod ./
COPY *.go ./
COPY static/ ./static/

RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o frpc-webui . \
    && upx --best --lzma frpc-webui

FROM scratch

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=builder /src/frpc-webui /app/frpc-webui

WORKDIR /app

VOLUME /app/data

ENV WEB_PORT=7500

ENTRYPOINT ["/app/frpc-webui"]

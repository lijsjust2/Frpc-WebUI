FROM golang:1.22-alpine AS builder

ARG TARGETOS
ARG TARGETARCH
ARG FRP_VERSION

RUN apk add --no-cache upx tzdata curl jq

WORKDIR /src
COPY go.mod ./
COPY *.go ./
COPY static/ ./static/

RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o frpc-webui . \
    && upx --best --lzma frpc-webui

RUN if [ -z "${FRP_VERSION}" ]; then \
        echo "FRP_VERSION not specified, fetching latest from GitHub..." && \
        FRP_VERSION=$(curl -sL https://api.github.com/repos/fatedier/frp/releases/latest | jq -r '.tag_name'); \
    fi && \
    if [ -z "${FRP_VERSION}" ] || [ "${FRP_VERSION}" = "null" ]; then \
        echo "ERROR: Unable to determine frp version. Please set FRP_VERSION build arg."; \
        exit 1; \
    fi && \
    echo "Using frp version: ${FRP_VERSION}" && \
    FRP_VERSION_CLEAN=$(echo "${FRP_VERSION}" | sed 's/^v//') && \
    FRP_VERSION_TAG="v${FRP_VERSION_CLEAN}" && \
    FRPC_URL="https://github.com/fatedier/frp/releases/download/${FRP_VERSION_TAG}/frp_${FRP_VERSION_CLEAN}_linux_${TARGETARCH}.tar.gz" && \
    echo "Downloading frpc from: ${FRPC_URL}" && \
    curl -fSL "${FRPC_URL}" -o frpc.tar.gz && \
    tar -xzf frpc.tar.gz && \
    mv "frp_${FRP_VERSION_CLEAN}_linux_${TARGETARCH}/frpc" ./frpc && \
    chmod +x ./frpc && \
    rm -rf "frp_${FRP_VERSION_CLEAN}_linux_${TARGETARCH}" frpc.tar.gz && \
    echo "frpc ${FRP_VERSION} downloaded successfully"

FROM scratch

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=builder /src/frpc-webui /app/frpc-webui
COPY --from=builder /src/frpc /app/frpc

WORKDIR /app

VOLUME /app/data

ENV WEB_PORT=7500
ENV FRPC_PATH=/app/frpc

ENTRYPOINT ["/app/frpc-webui"]

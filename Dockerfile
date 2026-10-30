FROM node:22-bookworm-slim

ARG TARGETARCH

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        bash ca-certificates chromium curl dnsutils git iproute2 jq \
        netcat-openbsd nmap openssh-client openssl procps \
        python3 python3-pip python3-venv ripgrep unzip zip \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY --chmod=755 dist/${TARGETARCH}/artex ./artex
COPY --chmod=755 start.sh ./start.sh
COPY config.example.json ./config.example.json
COPY skills ./skills

RUN npm ci --omit=dev --ignore-scripts --prefix skills/api-recon/scripts \
    && npm cache clean --force

ENV CHROMIUM=/usr/bin/chromium

EXPOSE 8787

ENTRYPOINT ["./start.sh"]

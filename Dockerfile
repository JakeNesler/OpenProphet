# openprophet-agent — self-hosted OSS trading agent.
# One runtime process: `node agent/server.js` (the dashboard), which spawns the Go trading
# backend (prophet_bot) and, each heartbeat, an `opencode` subprocess for the LLM turn.
# So the image bundles: the Node app + its native modules, the prebuilt Go binary, and the
# opencode CLI (authenticated at runtime via the ANTHROPIC_API_KEY secret — no interactive login).

# ── Stage 1: build the Go trading backend (CGO for the sqlite driver) ──
FROM golang:1.27.1-bookworm AS gobuild
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=1 go build -ldflags='-s -w' -o prophet_bot ./cmd/bot

# ── Stage 2: build Node deps (native: better-sqlite3, sharp/onnx) ──
FROM node:24.21.0-bookworm AS nodedeps
WORKDIR /app
RUN apt-get update && apt-get install -y --no-install-recommends python3 make g++ ca-certificates \
    && rm -rf /var/lib/apt/lists/*
COPY package.json package-lock.json ./
RUN npm ci --omit=dev

# ── Stage 3: runtime ──
FROM node:24.21.0-bookworm
WORKDIR /app
# The image has no .git, so the startup update check reads the commit from this env instead:
#   docker build --build-arg OPENPROPHET_COMMIT=$(git rev-parse HEAD) .
ARG OPENPROPHET_COMMIT=""
ENV NODE_ENV=production \
    AGENT_PORT=3737 \
    SERVER_HOST=127.0.0.1 \
    DATABASE_PATH=/app/data/prophet_trader.db \
    OPENPROPHET_COMMIT=$OPENPROPHET_COMMIT
# opencode CLI for the per-heartbeat LLM subprocess (package is opencode-ai)
RUN npm install -g opencode-ai@1.18.32 && rm -rf /root/.npm
COPY --from=nodedeps /app/node_modules ./node_modules
COPY . .
COPY --from=gobuild /src/prophet_bot ./prophet_bot
# data/ (SQLite + agent-config.json) is a mounted PVC in the cluster; create the mountpoint
RUN mkdir -p /app/data
EXPOSE 3737
CMD ["node", "agent/server.js"]

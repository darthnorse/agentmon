# ---- Stage 1: build the SPA ----
FROM node:22-alpine AS web
WORKDIR /web
COPY web/package*.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# ---- Stage 2: build hubd with the SPA embedded ----
FROM golang:1.25-alpine AS hubd
WORKDIR /src
COPY . /src
COPY --from=web /web/dist /src/hubd/internal/webui/dist
WORKDIR /src/hubd
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" \
    -o /out/agentmon-hubd ./cmd/agentmon-hubd

# ---- Stage 3: minimal runtime ----
FROM gcr.io/distroless/static-debian12
COPY --from=hubd /out/agentmon-hubd /agentmon-hubd
VOLUME ["/data"]
EXPOSE 8080
USER 65532:65532
ENTRYPOINT ["/agentmon-hubd"]
CMD ["--config", "/data/config.yaml"]

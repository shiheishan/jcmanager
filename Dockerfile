FROM node:22-bookworm AS web-build
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.26.1-bookworm AS go-build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web-build /src/web/dist ./web/dist
RUN mkdir -p /out/agents \
  && GOOS=linux GOARCH=amd64 go build -o /out/jcmanager-server ./cmd/server \
  && GOOS=linux GOARCH=amd64 go build -o /out/agents/jcmanager-agent-linux-amd64 ./cmd/agent \
  && GOOS=linux GOARCH=arm64 go build -o /out/agents/jcmanager-agent-linux-arm64 ./cmd/agent

FROM debian:bookworm-slim
RUN apt-get update \
  && apt-get install -y --no-install-recommends ca-certificates \
  && rm -rf /var/lib/apt/lists/*
WORKDIR /app
COPY --from=go-build /out/jcmanager-server ./jcmanager-server
COPY --from=go-build /out/agents ./agents
COPY --from=web-build /src/web/dist ./web/dist
ENV JCMANAGER_WEB_DIST=/app/web/dist
EXPOSE 8080 50051
VOLUME ["/var/lib/jcmanager"]
ENTRYPOINT ["./jcmanager-server", "-db-path", "/var/lib/jcmanager/jcmanager.db"]

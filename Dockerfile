FROM node:24-alpine AS frontend
WORKDIR /src/frontend
COPY frontend/package*.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run build

FROM golang:1.26-alpine AS backend
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend /src/frontend/dist ./frontend/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/gomoku .

FROM alpine:3.22
RUN adduser -D -H -u 10001 gomoku \
	&& mkdir -p /app/frontend /var/lib/gomoku \
	&& chown -R gomoku:gomoku /var/lib/gomoku
WORKDIR /app
COPY --from=backend /out/gomoku /app/gomoku
COPY --from=frontend /src/frontend/dist /app/frontend/dist
USER gomoku
ENV ADDR=0.0.0.0:8080 \
	DB_PATH=/var/lib/gomoku/wuziqi.db \
	STATIC_DIR=/app/frontend/dist \
	RATE_LIMIT_ENABLED=true \
	RATE_LIMIT_REQUESTS=300 \
	RATE_LIMIT_WINDOW_SECONDS=60 \
	MAX_JSON_BODY_BYTES=16384
VOLUME ["/var/lib/gomoku"]
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
	CMD wget -qO- http://127.0.0.1:8080/api/health >/dev/null || exit 1
CMD ["/app/gomoku"]

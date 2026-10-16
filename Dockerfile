FROM node:24-alpine AS web
WORKDIR /src/frontend
COPY frontend/package*.json ./
RUN npm ci --ignore-scripts
COPY frontend/ ./
RUN npm run typecheck && npm run build
FROM golang:1.26-alpine AS api
WORKDIR /src
COPY backend/go.* ./
RUN go mod download
COPY backend/ ./
RUN CGO_ENABLED=0 go build -trimpath -o /server ./cmd/server && CGO_ENABLED=0 go build -trimpath -o /bot ./cmd/bot && CGO_ENABLED=0 go build -trimpath -o /digest ./cmd/digest
FROM alpine:3.23
RUN apk add --no-cache ca-certificates && adduser -D -u 10001 app
COPY --from=api /server /server
COPY --from=api /bot /bot
COPY --from=api /digest /digest
COPY --from=web /src/frontend/dist /web
USER app
ENV STATIC_DIR=/web
EXPOSE 8080
ENTRYPOINT ["/server"]

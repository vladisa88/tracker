FROM node:24-alpine AS web
WORKDIR /src/frontend
COPY frontend/package*.json ./
RUN npm install --ignore-scripts
COPY frontend/ ./
RUN npm run typecheck && npm run build
FROM golang:1.26-alpine AS api
WORKDIR /src
COPY backend/go.* ./
RUN go mod download
COPY backend/ ./
RUN CGO_ENABLED=0 go build -trimpath -o /server ./cmd/server
FROM alpine:3.23
RUN apk add --no-cache ca-certificates && adduser -D -u 10001 app
COPY --from=api /server /server
COPY --from=web /src/frontend/dist /web
USER app
ENV STATIC_DIR=/web
EXPOSE 8080
ENTRYPOINT ["/server"]

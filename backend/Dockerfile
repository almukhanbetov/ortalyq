FROM golang:1.26.8-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Версия релиза (полный SHA коммита) — в диагностике и `server version`. Локально — dev.
ARG APP_VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X ortalyq/backend/internal/config.buildVersion=${APP_VERSION}" -o /out/server ./cmd/server

FROM alpine:3.20
ARG APP_VERSION=dev
LABEL org.opencontainers.image.version=$APP_VERSION
RUN apk add --no-cache ca-certificates tzdata && adduser -D -u 10001 app
USER app
COPY --from=build /out/server /usr/local/bin/server
EXPOSE 18080
ENTRYPOINT ["server"]
CMD ["serve"]

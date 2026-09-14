FROM php:8.3-cli-alpine
RUN apk add --no-cache wget ca-certificates \
    && adduser -D -u 10001 platfarm
WORKDIR /app
USER platfarm
EXPOSE 8080

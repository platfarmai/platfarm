FROM alpine:3.20
RUN apk add --no-cache wget ca-certificates \
    && adduser -D -u 10001 platfarm
WORKDIR /app
USER platfarm
EXPOSE 8080

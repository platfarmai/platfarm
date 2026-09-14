FROM alpine:3.20
RUN apk add --no-cache python3 py3-pip py3-cryptography wget ca-certificates \
    && pip3 install --break-system-packages --no-cache-dir fastapi uvicorn pyjwt \
    && adduser -D -u 10001 platfarm
WORKDIR /app
USER platfarm
EXPOSE 8080

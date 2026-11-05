# Stage 1: build the console SPA
FROM node:22-alpine AS web
WORKDIR /web
COPY web/package.json ./
RUN npm install --no-audit --no-fund --registry=https://registry.npmmirror.com
COPY web/ ./
RUN npm run build

# Stage 2: Go service with the SPA embedded
FROM golang:1.26.10-alpine AS build
WORKDIR /src
ENV GOPROXY="https://goproxy.cn|https://proxy.golang.org|direct"
ENV GOTOOLCHAIN=auto
COPY . .
COPY --from=web /web/dist ./web/dist
RUN go mod tidy \
    && CGO_ENABLED=0 go build -o /out/server . \
    && CGO_ENABLED=0 go build -o /out/contract-test ./tests/contract

FROM alpine:3.20
RUN apk add --no-cache wget
COPY --from=build /out/server /server
COPY --from=build /out/contract-test /contract-test
EXPOSE 8080
ENTRYPOINT ["/server"]

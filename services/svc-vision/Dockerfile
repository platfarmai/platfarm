# 1) 构建前端（web/dist 供 go:embed）
FROM node:20-alpine AS web
WORKDIR /src/web
COPY web/package*.json ./
RUN npm ci || npm install
COPY web/ ./
RUN npm run build

# 2) 构建 Go 二进制（含内嵌 web/dist + 契约测试）
FROM golang:1.26.10-alpine AS build
WORKDIR /src
ENV GOPROXY="https://goproxy.cn|https://proxy.golang.org|direct"
# go.mod 需要 go1.24（minio 依赖链），基础镜像 1.23 → 允许按需下载 toolchain（经 GOPROXY）
ENV GOTOOLCHAIN=auto
COPY . .
COPY --from=web /src/web/dist ./web/dist
RUN go mod tidy \
    && CGO_ENABLED=0 go build -o /out/server . \
    && CGO_ENABLED=0 go build -o /out/contract-test ./tests/contract

# 3) 运行镜像（ffmpeg/ffprobe + 时区 + wget 探活）
FROM alpine:3.20
RUN sed -i 's/dl-cdn.alpinelinux.org/mirrors.tuna.tsinghua.edu.cn/g' /etc/apk/repositories \
    && apk add --no-cache ffmpeg wget tzdata \
    && mkdir -p /app/data /recordings
WORKDIR /app
COPY --from=build /out/server /server
COPY --from=build /out/contract-test /contract-test
EXPOSE 8080
ENTRYPOINT ["/server"]

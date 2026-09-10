FROM golang:1.23-alpine AS build
WORKDIR /src
ENV GOPROXY="https://goproxy.cn|https://proxy.golang.org|direct"
COPY . .
RUN go mod tidy \
    && CGO_ENABLED=0 go build -o /out/server . \
    && CGO_ENABLED=0 go build -o /out/contract-test ./tests/contract

FROM alpine:3.20
COPY --from=build /out/server /server
COPY --from=build /out/contract-test /contract-test
EXPOSE 8080
ENTRYPOINT ["/server"]

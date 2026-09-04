FROM golang:1.23-alpine AS build
WORKDIR /src
ENV GOPROXY=https://goproxy.cn,direct
COPY . .
RUN go mod tidy && CGO_ENABLED=0 go build -o /out/auth .

FROM alpine:3.20
COPY --from=build /out/auth /auth
EXPOSE 8080
ENTRYPOINT ["/auth"]

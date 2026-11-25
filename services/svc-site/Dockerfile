FROM golang:1.26.10-alpine AS build
WORKDIR /src
ENV GOTOOLCHAIN=auto
COPY . .
RUN CGO_ENABLED=0 go build -o /out/server .

FROM alpine:3.20
RUN apk add --no-cache wget
COPY --from=build /out/server /server
EXPOSE 8080
ENTRYPOINT ["/server"]

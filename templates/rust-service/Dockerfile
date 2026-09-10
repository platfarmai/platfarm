FROM rust:1-alpine AS build
RUN sed -i 's/dl-cdn.alpinelinux.org/mirrors.tuna.tsinghua.edu.cn/g' /etc/apk/repositories \
    && apk add --no-cache musl-dev \
    && printf '[source.crates-io]\nreplace-with = "tuna"\n[source.tuna]\nregistry = "sparse+https://mirrors.tuna.tsinghua.edu.cn/crates.io-index/"\n' \
       > "$CARGO_HOME/config.toml"
WORKDIR /src
COPY . .
RUN cargo build --release --bins

FROM alpine:3.20
COPY --from=build /src/target/release/server /server
COPY --from=build /src/target/release/contract-test /contract-test
EXPOSE 8080
ENTRYPOINT ["/server"]

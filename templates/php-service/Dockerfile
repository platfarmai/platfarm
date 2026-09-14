# Release images: FROM ghcr.io/platfarmai/runtime-php:${PF_IMAGE_TAG:-latest}
FROM php:8.3-cli-alpine
RUN apk add --no-cache wget
WORKDIR /app
COPY . .
EXPOSE 8080
CMD ["php", "-S", "0.0.0.0:8080", "router.php"]

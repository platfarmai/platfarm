FROM php:8.3-cli-alpine
RUN apk add --no-cache wget
WORKDIR /app
COPY . .
EXPOSE 8080
CMD ["php", "-S", "0.0.0.0:8080", "router.php"]

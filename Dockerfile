# syntax=docker/dockerfile:1

FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /bin/ditingyun ./cmd/ditingyun

FROM alpine:3.20
RUN adduser -D -u 10001 diting && mkdir -p /data && chown diting /data
USER diting
COPY --from=build /bin/ditingyun /usr/local/bin/ditingyun
COPY --from=build /src/public /public
ENV PORT=8080 DATA_DIR=/data
EXPOSE 8080
VOLUME ["/data"]
ENTRYPOINT ["ditingyun"]

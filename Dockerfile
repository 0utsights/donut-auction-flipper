FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go test ./... && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/donut-server ./cmd/server

FROM alpine:3.22
RUN adduser -D -u 10001 app && mkdir /data && chown app:app /data
USER app
COPY --from=build /out/donut-server /usr/local/bin/donut-server
EXPOSE 8080
ENV DN_ADDRESS=0.0.0.0:8080 DN_HISTORY_FILE=/data/history.json.gz
VOLUME ["/data"]
ENTRYPOINT ["donut-server"]

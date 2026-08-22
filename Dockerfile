FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY infra ./infra
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/backend ./cmd/backend && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/simulator ./cmd/simulator && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/loadtest ./cmd/loadtest && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/collector ./cmd/collector

FROM alpine:3.22 AS backend
RUN adduser -D -u 10001 app
USER app
COPY --from=build /out/backend /usr/local/bin/backend
EXPOSE 8080
ENTRYPOINT ["backend"]

FROM alpine:3.22 AS collector
RUN adduser -D -u 10001 app
USER app
COPY --from=build /out/collector /usr/local/bin/collector
ENTRYPOINT ["collector"]

FROM alpine:3.22 AS simulator
RUN adduser -D -u 10001 app
USER app
COPY --from=build /out/simulator /usr/local/bin/simulator
ENTRYPOINT ["simulator"]

FROM alpine:3.22 AS loadtest
RUN adduser -D -u 10001 app
USER app
COPY --from=build /out/loadtest /usr/local/bin/loadtest
ENTRYPOINT ["loadtest"]

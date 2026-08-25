FROM node:22-alpine AS build
WORKDIR /app
COPY package.json package-lock.json tsconfig.json ./
RUN npm ci
COPY src ./src
RUN npm test && npm prune --omit=dev

FROM node:22-alpine
RUN addgroup -g 10001 collector && adduser -D -u 10001 -G collector collector && mkdir -p /app /data/profiles /data/captures && chown -R collector:collector /app /data
WORKDIR /app
COPY --from=build /app/package.json /app/package-lock.json ./
COPY --from=build /app/node_modules ./node_modules
COPY --from=build /app/dist ./dist
COPY --chmod=755 entrypoint.sh /usr/local/bin/collector-entrypoint
USER collector
ENV DN_COLLECTOR_CONFIG=/run/secrets/collector-accounts.json
VOLUME ["/data/profiles", "/data/captures"]
ENTRYPOINT ["collector-entrypoint"]

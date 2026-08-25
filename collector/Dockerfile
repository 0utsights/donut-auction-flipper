FROM node:22-alpine AS build
ARG BUILD_DNS
WORKDIR /app
COPY package.json package-lock.json tsconfig.json ./
RUN if [ -n "$BUILD_DNS" ]; then echo "nameserver $BUILD_DNS" > /etc/resolv.conf; fi \
    && npm ci
COPY src ./src
RUN npm test && npm prune --omit=dev

FROM node:22-alpine
RUN addgroup -g 10001 collector && adduser -D -u 10001 -G collector collector && mkdir -p /app /data/profiles /data/captures && chown -R collector:collector /app /data
WORKDIR /app
COPY --from=build /app/package.json /app/package-lock.json ./
COPY --from=build /app/node_modules ./node_modules
COPY --from=build /app/dist ./dist
USER collector
ENV DN_COLLECTOR_CONFIG=/run/secrets/collector-accounts.json
VOLUME ["/data/profiles", "/data/captures"]
ENTRYPOINT ["node", "dist/manager.js"]

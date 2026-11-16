# The frontend build, which Go embeds. Tailwind scans internal/ for classes.
FROM node:26-trixie-slim AS assets
WORKDIR /src
RUN npm install -g pnpm@12.18.0
COPY package.json pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile
COPY tsconfig.json vite.config.ts ./
COPY frontend frontend
COPY internal internal
RUN pnpm run build

# libvips comes from a quickstack-vips kit (scripts/vips-kit), whose
# libraries ship next to the binary in /opt/quickstack/lib, the same layout
# as `make dist`. Generated Go is committed, so templ and sqlc don't run.
FROM golang:1.27-trixie AS build
ARG TARGETARCH
WORKDIR /src
COPY scripts/vips-kit scripts/
RUN scripts/vips-kit linux-$TARGETARCH /vips
COPY go.mod go.sum ./
RUN go mod download
COPY cmd cmd
COPY internal internal
COPY --from=assets /src/internal/web/dist internal/web/dist
RUN mkdir -p /app/bin /app/lib /app/share \
	&& PKG_CONFIG_LIBDIR=/vips/lib/pkgconfig CGO_LDFLAGS='-Wl,-rpath,$ORIGIN/../lib' \
		go build -trimpath -ldflags='-s -w' -o /app/bin/quickstack ./cmd/quickstack \
	&& cp -a /vips/lib/*.so.* /app/lib/ \
	&& cp -R /vips/share/licenses /app/share/

FROM debian:trixie-slim
RUN apt-get update \
	&& apt-get install -y --no-install-recommends ca-certificates \
	&& rm -rf /var/lib/apt/lists/* \
	&& useradd --system --home-dir /var/lib/quickstack quickstack \
	&& install -d -o quickstack -g quickstack /var/lib/quickstack/media
COPY --from=build /app /opt/quickstack
USER quickstack
ENV QUICKSTACK_ADDR=:8360
EXPOSE 8360
# Uploads, unless QUICKSTACK_BLOB_URL points at a bucket.
VOLUME /var/lib/quickstack/media
ENTRYPOINT ["/opt/quickstack/bin/quickstack"]

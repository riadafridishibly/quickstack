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
# as `make dist`. TAGS are Go build tags; with resvg, the static resvg
# library comes from a quickstack-resvg kit. Generated Go is committed, so
# templ and sqlc don't run.
FROM golang:1.27-trixie AS build
ARG TARGETARCH
ARG TAGS=
WORKDIR /src
COPY scripts/vips-kit scripts/resvg-kit scripts/install scripts/
RUN scripts/vips-kit linux-$TARGETARCH /vips
RUN case ",$TAGS," in *,resvg,*) scripts/resvg-kit linux-$TARGETARCH /resvg ;; esac
COPY go.mod go.sum ./
RUN go mod download
COPY cmd cmd
COPY internal internal
COPY --from=assets /src/internal/web/dist internal/web/dist
RUN mkdir -p /app/bin /app/lib /app/share \
	&& PKG_CONFIG_LIBDIR=/vips/lib/pkgconfig:/resvg/lib/pkgconfig CGO_LDFLAGS='-Wl,-rpath,$ORIGIN/../lib' \
		go build -trimpath -tags="$TAGS" -ldflags='-s -w' -o /app/bin/quickstack ./cmd/quickstack \
	&& cp -a /vips/lib/*.so.* /app/lib/ \
	&& cp -R /vips/share/licenses /app/share/ \
	&& if [ -d /resvg ]; then \
		cp -R /resvg/share/licenses /app/share/licenses/quickstack-resvg \
		&& mkdir /app/share/licenses/dejavu-fonts \
		&& cp internal/media/fonts/LICENSE /app/share/licenses/dejavu-fonts/; \
	fi \
	&& cp scripts/install /app/install

FROM debian:trixie-slim
RUN apt-get update \
	&& apt-get install -y --no-install-recommends ca-certificates \
	&& rm -rf /var/lib/apt/lists/*
RUN --mount=type=bind,from=build,source=/app,target=/release /release/install
USER quickstack
ENV QUICKSTACK_ADDR=:8360
EXPOSE 8360
# Uploads, unless QUICKSTACK_BLOB_URL points at a bucket.
VOLUME /var/lib/quickstack/media
ENTRYPOINT ["/opt/quickstack/bin/quickstack"]

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

# libvips comes through cgo, so the binary links against the libvips-dev
# headers here and libvips42t64 at run time. Generated Go is committed, so
# templ and sqlc don't run.
FROM golang:1.27-trixie AS build
RUN apt-get update \
	&& apt-get install -y --no-install-recommends libvips-dev \
	&& rm -rf /var/lib/apt/lists/*
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd cmd
COPY internal internal
COPY --from=assets /src/internal/web/dist internal/web/dist
RUN go build -trimpath -o /quickstack ./cmd/quickstack

FROM debian:trixie-slim
RUN apt-get update \
	&& apt-get install -y --no-install-recommends libvips42t64 libheif-plugin-libde265 ca-certificates \
	&& rm -rf /var/lib/apt/lists/* \
	&& useradd --system --home-dir /var/lib/quickstack quickstack \
	&& install -d -o quickstack -g quickstack /var/lib/quickstack/media
COPY --from=build /quickstack /usr/local/bin/quickstack
USER quickstack
ENV QUICKSTACK_ADDR=:8360
EXPOSE 8360
# Uploads, unless QUICKSTACK_BLOB_URL points at a bucket.
VOLUME /var/lib/quickstack/media
ENTRYPOINT ["/usr/local/bin/quickstack"]

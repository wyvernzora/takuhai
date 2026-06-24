# takuhai n8n nodes — minimal init container.
#
# Stage 1 compiles the nodes with corepack pnpm; stage 2 is a tiny busybox image that
# bakes in the built package and, on start, copies it into the shared volume n8n
# reads via N8N_CUSTOM_EXTENSIONS (see entrypoint.sh).
#
# Built from the REPO ROOT context (CI selects file=integrations/n8n/Dockerfile) so the
# canonical node icon docs/assets/logo-face.svg is in scope — copy-assets reads it via
# ../../docs/assets, the same path a local `pnpm build` uses. The WORKDIR mirrors the
# repo layout to keep that path identical.

# CI passes VERSION to every image so all three share a tag; there is no Go binary here
# to stamp it into, so this declaration simply consumes the shared build arg.
ARG VERSION=dev

# ---- build ----
FROM node:24-alpine AS build
WORKDIR /src/integrations/n8n
RUN corepack enable
COPY integrations/n8n/package.json integrations/n8n/pnpm-lock.yaml ./
RUN corepack pnpm install --frozen-lockfile
COPY integrations/n8n/ ./
# The node icon's single source of truth (resolved as ../../docs/assets at build time).
COPY docs/assets/logo-face.svg /src/docs/assets/logo-face.svg
RUN corepack pnpm build
# Reduce to the loadable package: the n8n manifest (package.json) + compiled dist/.
RUN mkdir -p /pkg && cp -r package.json dist /pkg/

# ---- init image ----
FROM busybox:1.37-musl
LABEL org.opencontainers.image.source="https://github.com/wyvernzora/takuhai"
COPY --from=build /pkg /takuhai-nodes
COPY integrations/n8n/entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh
# The directory n8n is configured to scan (N8N_CUSTOM_EXTENSIONS); overridable.
ENV TAKUHAI_NODES_TARGET=/opt/n8n/custom
ENTRYPOINT ["/entrypoint.sh"]

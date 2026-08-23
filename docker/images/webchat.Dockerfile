FROM node:26-alpine AS build
WORKDIR /src
COPY package.json package-lock.json* ./
COPY apps/webchat/package.json apps/webchat/package.json
COPY tools/document-runtime/package.json tools/document-runtime/package.json
RUN npm ci
COPY apps/webchat apps/webchat
RUN npm --workspace @sparkclaw/webchat run build

FROM nginx:1.29-alpine
# The stock nginx entrypoint renders /etc/nginx/templates/*.template with
# envsubst on start. The filter limits substitution to SPARKCLAW_* variables
# so nginx's own $request_uri/$http_* variables pass through untouched.
ENV NGINX_ENVSUBST_FILTER=^SPARKCLAW_ \
    SPARKCLAW_JINGSI_LAN_PORT=18793
COPY docker/images/webchat.nginx.conf.template /etc/nginx/templates/default.conf.template
COPY --from=build /src/apps/webchat/dist /usr/share/nginx/html
EXPOSE 18790

FROM node:26-alpine AS build
WORKDIR /src
COPY package.json package-lock.json* ./
COPY apps/webchat/package.json apps/webchat/package.json
COPY tools/document-runtime/package.json tools/document-runtime/package.json
RUN npm ci
COPY apps/webchat apps/webchat
RUN npm --workspace @sparkclaw/webchat run build

FROM nginx:1.29-alpine
COPY docker/images/webchat.nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=build /src/apps/webchat/dist /usr/share/nginx/html
EXPOSE 18790

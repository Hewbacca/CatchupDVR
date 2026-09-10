FROM docker.io/library/node:22-alpine AS web
WORKDIR /src/web
COPY web/package*.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM docker.io/library/golang:1.24-alpine AS backend
WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/catchupd ./cmd/catchupd

FROM docker.io/library/alpine:3.21
RUN apk add --no-cache ffmpeg libva-utils intel-media-driver mesa-va-gallium ca-certificates tzdata
COPY --from=backend /out/catchupd /usr/local/bin/catchupd
COPY --from=web /src/web/dist /opt/catchup/web
ENV WEB_DIR=/opt/catchup/web
EXPOSE 8080
ENTRYPOINT ["catchupd"]

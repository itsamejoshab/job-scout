# Frontend build stage
FROM node:20-alpine AS frontend

WORKDIR /src/webui

COPY webui/package.json webui/package-lock.json ./
RUN npm ci

COPY webui/ ./
RUN npm run build

# Go build stage
FROM golang:1.25-bookworm AS build

WORKDIR /src

# Cache module downloads
COPY go.mod go.sum ./
RUN go mod download

# Build the single binary (api|worker chosen at runtime via arg)
COPY . .
COPY --from=frontend /src/webui/dist ./webui/dist
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -o /bin/jobscout ./cmd/jobscout

# Final stage
FROM alpine:3.20
RUN apk add --no-cache ca-certificates wget

COPY --from=build /bin/jobscout /usr/local/bin/jobscout

EXPOSE 8000
ENTRYPOINT ["jobscout"]
CMD ["api"]

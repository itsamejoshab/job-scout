# Build stage
FROM golang:1.25-bookworm AS build

WORKDIR /src

# Cache module downloads
COPY go.mod go.sum ./
RUN go mod download

# Build the single binary (api|worker chosen at runtime via arg)
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -o /bin/jobscout ./cmd/jobscout

# Final stage
FROM alpine:3.20
RUN apk add --no-cache ca-certificates wget

COPY --from=build /bin/jobscout /usr/local/bin/jobscout

EXPOSE 8000
ENTRYPOINT ["jobscout"]
CMD ["api"]

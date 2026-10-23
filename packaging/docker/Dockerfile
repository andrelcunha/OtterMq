# Stage 1: Build Stage
FROM golang:1.27-alpine AS go-builder

# Install build tools
RUN apk add --no-cache make git gcc g++

ENV CGO_ENABLED=1 GOOS=linux GOARCH=amd64

# Set the Current Working Directory inside the container
WORKDIR /app

# Copy go.mod and go.sum files
COPY go.mod go.sum ./

# Download all dependencies. Dependencies will be cached if the go.mod and go.sum files are not changed
RUN go mod download

# Install the swag CLI at the same version as the swaggo/swag module in go.mod,
# so the generated docs match the library and builds are reproducible
RUN go install github.com/swaggo/swag/cmd/swag@$(go list -m -f '{{.Version}}' github.com/swaggo/swag)

# Copy the source from the current directory to the Working Directory inside the container
COPY . .

# Generate swagger docs
RUN make docs

# Build the Go app using the Makefile
RUN make build

####################################################################
# Stage 2: Management UI (SvelteKit static build from web/ui)
FROM node:22-alpine AS node-builder

WORKDIR /ui

# Install dependencies first so they're cached until package*.json change
COPY web/ui/package.json web/ui/package-lock.json ./
RUN npm ci

COPY web/ui ./
RUN npm run build

####################################################################
# Stage 3: Final Stage
FROM alpine:latest

# Set the Current Working Directory inside the container
WORKDIR /app

# Copy the built binaries from the builder stage
COPY --from=go-builder /app/bin/ottermq .
# The broker serves the UI from OTTERMQ_UI_PATH, whose default is this directory
COPY --from=node-builder /ui/build /usr/local/share/ottermq/ui/
# Expose ports 3000 for the web admin, 5672 for the broker and 9090 for prometheus exporter
EXPOSE 3000
EXPOSE 5672
EXPOSE 9090

# Command to run both broker and web admin binaries
CMD ["./ottermq"]

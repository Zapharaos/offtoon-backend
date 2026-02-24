FROM golang:1.26-alpine AS build
# Build stage

WORKDIR /app

# Install make and git for the build process
RUN apk --no-cache add make

# Copy go.mod and go.sum files first (for better layer caching)
COPY go.mod go.sum ./
RUN go mod download && go mod verify

# Copy the rest of the application to the build stage
COPY . .

# Build the application using the Makefile
# Set default build args which can be overridden at build time
ARG TAG=v1.0.0
ARG BUILD=0
RUN make build TAG=${TAG} BUILD=${BUILD}

FROM alpine:latest
# Use alpine for a lightweight base with shell utilities

WORKDIR /root/

# Create logs directory
RUN mkdir -p /root/logs

# Copy the binary from the build stage
COPY --from=build /app/bin/offtoon .

# Copy config files from the build stage
COPY --from=build /app/config ./config

# Create volume for logs
VOLUME /root/logs

# Expose the port the app runs on
EXPOSE 3000

# Command to run
CMD ["./offtoon"]

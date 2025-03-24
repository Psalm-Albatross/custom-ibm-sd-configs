# Use the official Golang image as the build stage
FROM golang:1.24.1-alpine AS builder
# SET ARGUMENTS
ARG PORT
# SET ENVIRONMENT VARIABLES
ENV GO111MODULE=on
ENV CGO_ENABLED=0
ENV GOOS=linux
ENV GOARCH=amd64
ENV PORT=9000

# Set the working directory inside the container
WORKDIR /ibm_sd_app

# Copy the Go module files and download dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of the application code
COPY . .

# Build the Go application
RUN CGO_ENABLED=$CGO_ENABLED GOOS=$GOOS GOARCH=$GOARCH go build -o /ibm_sd_app/main .

# Use a minimal base image for the final stage
FROM alpine:latest

# Set the working directory inside the container
WORKDIR /root/

# Copy the built application from the builder stage
COPY --from=builder /ibm_sd_app/main .

# Expose the port the application runs on
EXPOSE $PORT

# Command to run the application
CMD ["./main"]

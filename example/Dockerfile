# Use a minimal base image
FROM alpine:latest

# Set the working directory
WORKDIR /app

# Copy the binary from the build context
COPY ../bin/main_linux_amd64 /app/main

# Make the binary executable
RUN chmod +x /app/main

# Expose the port the app runs on
EXPOSE 8080

# Command to run the binary
CMD ["./main"]

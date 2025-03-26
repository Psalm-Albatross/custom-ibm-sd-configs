# Custom IBM SD Configs

This project uses a Makefile to manage building binaries for different OS/Arch combinations.

## Purpose

The purpose of this tool is to fetch and manage IBM Cloud VM instances across multiple accounts and regions. It provides an API to retrieve instance details, integrates with Prometheus for monitoring, and supports secure HTTPS communication.

## Features

- Fetch and manage IBM Cloud VM instances across multiple accounts and regions.
- Integration with Prometheus for monitoring.
- Support for secure HTTPS communication using Public CA or self-signed certificates.
- Flexible deployment options: standalone, Docker, or Docker Compose.
- Authentication via IBM Cloud API keys or IAM roles.
- Lightweight and easy to configure.

## Prerequisites

Make sure you have `gox` installed. You can install it using the following command:
```sh
go install github.com/mitchellh/gox@latest
```

If you encounter an error stating that `gox` is not installed, ensure that your `GOPATH` and `PATH` environment variables are set correctly. You can add the following lines to your shell profile (e.g., `.bashrc`, `.zshrc`):

```sh
export GOPATH=$(go env GOPATH)
export PATH=$PATH:$GOPATH/bin
```

## HTTPS Configuration

To enable HTTPS for secure communication, you can use either a Public CA certificate or a self-signed certificate.

### Using a Public CA Certificate

1. Obtain a certificate and private key from a trusted Certificate Authority (CA).
2. Place the certificate and private key in a secure directory (e.g., `/etc/custom-ibm-sd-configs/`).
3. Set the following environment variables:
    ```sh
    export HTTPS_CERT_FILE=/etc/custom-ibm-sd-configs/server.crt
    export HTTPS_KEY_FILE=/etc/custom-ibm-sd-configs/server.key
    ```
4. Start the tool, and it will automatically use the provided certificate and key for HTTPS.

### Using a Self-Signed Certificate

1. Generate a self-signed certificate and private key:
    ```sh
    openssl req -x509 -newkey rsa:4096 -keyout server.key -out server.crt -days 365 -nodes
    ```
2. Place the generated files in a secure directory (e.g., `/etc/custom-ibm-sd-configs/`).
3. Set the following environment variables:
    ```sh
    export HTTPS_CERT_FILE=/etc/custom-ibm-sd-configs/server.crt
    export HTTPS_KEY_FILE=/etc/custom-ibm-sd-configs/server.key
    ```
4. Start the tool, and it will use the self-signed certificate for HTTPS.

### Disabling HTTPS

If you want to disable HTTPS and use HTTP instead, unset the `HTTPS_CERT_FILE` and `HTTPS_KEY_FILE` environment variables.

## Makefile Usage

### Build All Targets

To build binaries for all specified OS/Arch combinations, run:
```sh
make
```

This will clean the output directory, create it, and then build the binaries.

### Clean Output Directory

To clean the output directory, run:
```sh
make clean
```

This will remove the `bin` directory and all its contents.

### Build Binaries

To build the binaries without cleaning the output directory first, run:
```sh
make build
```

This will create the `bin` directory if it doesn't exist and then build the binaries.

## Deployment Options

### Standalone on Linux Server

1. Build the binary:
    ```sh
    make build
    ```

2. Copy the binary to your Linux server:
    ```sh
    scp bin/custom-ibm-sd-configs_amd64 user@server:/path/to/deploy
    ```

3. SSH into the server and run the binary:
    ```sh
    ssh user@server
    cd /path/to/deploy
    chmod +x custom-ibm-sd-configs_amd64
    ./custom-ibm-sd-configs_amd64
    ```

### Docker Deployment

1. Build the Docker image:
    ```sh
    docker build -t custom-ibm-sd-configs -f example/Dockerfile .
    ```

2. Run the Docker container:
    ```sh
    docker run -p 8080:8080 custom-ibm-sd-configs
    ```

### Docker-Compose Deployment

1. Navigate to the `example` directory:
    ```sh
    cd example
    ```

2. Run Docker Compose:
    ```sh
    docker-compose up --build
    ```

## Prometheus Integration

To use this tool with Prometheus to scrape VM instances, add the following job to your Prometheus configuration:

```yaml
scrape_configs:
  - job_name: 'ibm_sd_configs'
    static_configs:
      - targets: ['localhost:8080']
        labels:
          group: 'ibm_instances'
```

## Authentication with IBM Cloud

### API Keys

To authenticate with IBM Cloud using API keys, set the environment variable `IBMCLOUD_API_KEY_<ACCOUNT>` for each account. For example:
```sh
export IBMCLOUD_API_KEY_ACCOUNT1=your_api_key_here
```

### IAM Role

To authenticate using IAM roles, ensure that your IAM role has the necessary permissions to access the IBM Cloud VPC API. The required permissions include:
- `VPC Infrastructure Services > VPC Read-Only Access`
- `IAM Services > Service ID Read-Only Access`

## Permissions Required

To fetch instances from IBM Cloud, the following permissions are required:
- `VPC Infrastructure Services > VPC Read-Only Access`
- `IAM Services > Service ID Read-Only Access`

## HTTP Endpoints

The tool exposes the following HTTP endpoints:

- **`GET /instances`**  
  Fetches instances from specified accounts, regions, and resource groups.  
  Query Parameters:
  - `accounts`: Comma-separated list of IBM Cloud accounts (default: `account1,account2`).
  - `regions`: Comma-separated list of IBM Cloud regions (default: `us-east`).
  - `resource_groups`: Comma-separated list of resource groups (default: `default`).  
  Example:
  ```sh
  curl "http://localhost:8080/instances?accounts=account1,account2&regions=us-east,eu-de"
  ```

- **`GET /help`**  
  Displays the help page with usage instructions and examples.  
  Example:
  ```sh
  curl http://localhost:8080/help
  ```

- **`GET /prometheus`**  
  Generates Prometheus file-based service discovery JSON.  
  Query Parameters:
  - `accounts`: Comma-separated list of IBM Cloud accounts (default: `account1,account2`).
  - `regions`: Comma-separated list of IBM Cloud regions (default: `us-east`).
  - `resource_groups`: Comma-separated list of resource groups (default: `default`).
  - `output_file`: Path to the output file (optional).  
  Example:
  ```sh
  curl "http://localhost:8080/prometheus?accounts=account1&regions=us-east&output_file=prometheus_sd.json"
  ```

- **`GET /health`**  
  Returns the health status of the service.  
  Example:
  ```sh
  curl http://localhost:8080/health
  ```

- **`GET /masking-demo`**  
  Demonstrates sensitive data masking for API keys, tokens, URLs, and IPs.  
  Example:
  ```sh
  curl http://localhost:8080/masking-demo
  ```

- **`GET /redis-fallback-demo`**  
  Demonstrates Redis caching fallback with in-memory data.  
  Example:
  ```sh
  curl http://localhost:8080/redis-fallback-demo
  ```

- **`GET /prometheus-versioning-demo`**  
  Demonstrates Prometheus file-based service discovery JSON versioning.  
  Example:
  ```sh
  curl http://localhost:8080/prometheus-versioning-demo
  ```

## Tool Arguments

The tool supports the following command-line arguments:

- **`--accounts`**  
  Comma-separated list of IBM Cloud accounts. Default is `account1,account2`.  
  Example:
  ```sh
  ./custom-ibm-sd-configs_amd64 --accounts=account1,account2
  ```

- **`--regions`**  
  Comma-separated list of IBM Cloud regions. Default is `us-east`.  
  Example:
  ```sh
  ./custom-ibm-sd-configs_amd64 --regions=us-east,eu-de
  ```

- **`--port`**  
  Specifies the port on which the HTTP server runs. Default is `8080`.  
  Example:
  ```sh
  ./custom-ibm-sd-configs_amd64 --port=9090
  ```

- **`--resource_groups`**  
  Comma-separated list of IBM Cloud resource groups. Default is `default`.  
  Example:
  ```sh
  ./custom-ibm-sd-configs_amd64 --resource_groups=default
  ```

- **`--output-sd-file`**  
  Path to the output file for Prometheus file-based service discovery JSON. Default is `./prometheus_sd.json`.  
  Example:
  ```sh
  ./custom-ibm-sd-configs_amd64 --output-sd-file=/path/to/prometheus_sd.json
  ```

- **`--cert`**  
  Path to the TLS certificate file (optional).  
  Example:
  ```sh
  ./custom-ibm-sd-configs_amd64 --cert=/path/to/server.crt
  ```

- **`--key`**  
  Path to the TLS key file (optional).  
  Example:
  ```sh
  ./custom-ibm-sd-configs_amd64 --key=/path/to/server.key
  ```

- **`--version`**  
  Displays the tool version.  
  Example:
  ```sh
  ./custom-ibm-sd-configs_amd64 --version
  ```

- **`--help`**  
  Displays the help page with usage instructions.  
  Example:
  ```sh
  ./custom-ibm-sd-configs_amd64 --help
  ```

## Example

```sh
# Build all targets
make

# Clean the output directory
make clean

# Build binaries without cleaning
make build

# Run the binary on a Linux server
./custom-ibm-sd-configs_amd64 --port=9090 --log-level=debug

# Enable HTTPS with Public CA
export HTTPS_CERT_FILE=/path/to/server.crt
export HTTPS_KEY_FILE=/path/to/server.key
./custom-ibm-sd-configs_amd64 --port=8443

# Enable HTTPS with self-signed certificate
openssl req -x509 -newkey rsa:4096 -keyout server.key -out server.crt -days 365 -nodes
export HTTPS_CERT_FILE=server.crt
export HTTPS_KEY_FILE=server.key
./custom-ibm-sd-configs_amd64 --port=8443

# Build and run Docker container
docker build -t custom-ibm-sd-configs -f example/Dockerfile .
docker run -p 8080:8080 custom-ibm-sd-configs

# Run with Docker Compose
cd example
docker-compose up --build
```

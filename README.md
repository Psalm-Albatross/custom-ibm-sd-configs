# Custom IBM SD Configs

This project uses a Makefile to manage building binaries for different OS/Arch combinations.

## Purpose

The purpose of this tool is to fetch and manage IBM Cloud VM instances across multiple accounts and regions. It provides an API to retrieve instance details and integrates with Prometheus for monitoring.

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
    scp bin/main_linux_amd64 user@server:/path/to/deploy
    ```

3. SSH into the server and run the binary:
    ```sh
    ssh user@server
    cd /path/to/deploy
    chmod +x main_linux_amd64
    ./main_linux_amd64
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

## Example

```sh
# Build all targets
make

# Clean the output directory
make clean

# Build binaries without cleaning
make build

# Run the binary on a Linux server
./main_linux_amd64

# Build and run Docker container
docker build -t custom-ibm-sd-configs -f example/Dockerfile .
docker run -p 8080:8080 custom-ibm-sd-configs

# Run with Docker Compose
cd example
docker-compose up --build
```

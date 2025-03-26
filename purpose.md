# IBM Cloud Service Discovery Tool

## Overview

The IBM Cloud Service Discovery Tool is a powerful utility designed to simplify the process of discovering and managing IBM Cloud resources. It provides a centralized way to fetch, filter, and organize information about virtual server instances (VSIs) across multiple accounts, regions, and resource groups. The tool also integrates with Prometheus for service discovery and supports caching for improved performance.

---

## Purpose

The primary purpose of this tool is to:
1. **Automate Resource Discovery**: Fetch details of IBM Cloud resources (e.g., VSIs) across multiple accounts, regions, and resource groups.
2. **Enable Monitoring**: Generate Prometheus-compatible service discovery files for monitoring and alerting.
3. **Optimize Performance**: Use Redis caching to reduce redundant API calls and improve response times.
4. **Enhance Security**: Mask sensitive data (e.g., API keys, tokens) in logs and outputs.
5. **Simplify Management**: Provide a user-friendly HTTP API for querying and managing resources.

---

## Features

### 1. **Multi-Account and Multi-Region Support**
   - Fetch instances from multiple IBM Cloud accounts and regions simultaneously.
   - Dynamically discover available regions using the IBM Cloud API.

### 2. **Resource Group Filtering**
   - Filter instances by specific resource groups to narrow down results.

### 3. **Tag Management**
   - Retrieve and display tags associated with each instance for better categorization and management.

### 4. **Prometheus Integration**
   - Generate Prometheus-compatible file-based service discovery JSON files.
   - Automatically create backups of existing Prometheus files for versioning.

### 5. **Caching with Redis**
   - Cache instance data in Redis to reduce API calls and improve performance.
   - Gracefully handle Redis unavailability by falling back to in-memory data.

### 6. **Sensitive Data Masking**
   - Mask sensitive information (e.g., API keys, tokens, IPs) in logs and outputs to enhance security.

### 7. **Pagination Handling**
   - Handle paginated API responses to ensure all resources are fetched.

### 8. **Health Checks**
   - Provide a `/health` endpoint to monitor the health of the tool.

### 9. **Comprehensive HTTP API**
   - `/instances`: Fetch instances based on accounts, regions, and resource groups.
   - `/prometheus`: Generate Prometheus service discovery files.
   - `/help`: Display usage instructions.
   - `/masking-demo`: Demonstrate sensitive data masking.
   - `/redis-fallback-demo`: Showcase Redis caching fallback.
   - `/prometheus-versioning-demo`: Demonstrate Prometheus file versioning.

---

## Usage

### 1. **Command-Line Arguments**
   - `-accounts`: Comma-separated list of IBM Cloud accounts (default: `account1,account2`).
   - `-regions`: Comma-separated list of IBM Cloud regions (default: `us-east`).
   - `-port`: Port to run the HTTP server on (default: `8080`).
   - `-resource_groups`: Comma-separated list of resource groups (default: `default`).
   - `-output-sd-file`: Path to the Prometheus service discovery JSON file.

### 2. **Endpoints**
   - **Fetch Instances**: 
     ```
     GET /instances?accounts=account1,account2&regions=us-east,eu-de&resource_groups=default
     ```
   - **Generate Prometheus File**:
     ```
     GET /prometheus?accounts=account1&regions=us-east&resource_groups=default&output_file=./prometheus_sd.json
     ```
   - **Health Check**:
     ```
     GET /health
     ```
   - **Help**:
     ```
     GET /help
     ```

### 3. **Configuration**
   - The tool reads configuration from a `config.json` file if available. Example:
     ```json
     {
       "accounts": {
         "account1": "api_key_1",
         "account2": "api_key_2"
       },
       "port": "8080",
       "regions": {
         "us-east": "us-east",
         "eu-de": "eu-de"
       },
       "resource_groups": {
         "default": "default"
       },
       "output_sd_file": "./prometheus_sd.json"
     }
     ```

---

## Benefits

1. **Time-Saving**: Automates the tedious process of manually fetching and organizing resource data.
2. **Scalable**: Supports multiple accounts, regions, and resource groups, making it suitable for large-scale environments.
3. **Secure**: Ensures sensitive data is masked and not exposed in logs or outputs.
4. **Customizable**: Allows users to configure accounts, regions, and resource groups via command-line arguments or a configuration file.
5. **Monitoring-Ready**: Seamlessly integrates with Prometheus for monitoring and alerting.

---

## Example Scenarios

### 1. Fetch Instances Across Multiple Accounts and Regions
   ```
   curl "http://localhost:8080/instances?accounts=account1,account2&regions=us-east,eu-de"
   ```

### 2. Generate Prometheus Service Discovery File
   ```
   curl "http://localhost:8080/prometheus?accounts=account1&regions=us-east&resource_groups=default&output_file=./prometheus_sd.json"
   ```

### 3. Check Tool Health
   ```
   curl http://localhost:8080/health
   ```

---

## Future Enhancements

1. **Support for Additional IBM Cloud Services**: Extend the tool to fetch data for other IBM Cloud services (e.g., databases, storage).
2. **Advanced Filtering**: Add support for more granular filtering options (e.g., by instance type, status).
3. **Metrics Dashboard**: Integrate with Grafana to visualize instance data and metrics.
4. **Authentication**: Add support for user authentication to secure the HTTP API.

---

## Conclusion

The IBM Cloud Service Discovery Tool is an essential utility for IBM Cloud users who need to manage and monitor their resources efficiently. With its robust feature set, secure design, and seamless integration with Prometheus, this tool simplifies resource discovery and enhances operational visibility.

For any questions or contributions, feel free to reach out or submit a pull request!
package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/IBM/go-sdk-core/v5/core"
	"github.com/IBM/platform-services-go-sdk/globaltaggingv1"
	"github.com/IBM/platform-services-go-sdk/resourcemanagerv2"
	"github.com/IBM/vpc-go-sdk/vpcv1"
	"github.com/go-redis/redis/v8"
	"github.com/hashicorp/vault/api"
	"github.com/spf13/viper"
)

// Config structure
type Config struct {
	Accounts       map[string]string `json:"accounts"`
	Port           string            `json:"port"`
	Regions        map[string]string `json:"regions"`
	ResourceGroups map[string]string `json:"resource_groups"` // Add ResourceGroups field
	OutputSDFile   string            `json:"output_sd_file"`
}

// Instance struct
type Instance struct {
	Name             string   `json:"name"`
	ID               string   `json:"id"`
	Region           string   `json:"region"`
	Account          string   `json:"account"`
	PublicIP         string   `json:"public_ip"`
	PrivateIP        string   `json:"private_ip"`
	Status           string   `json:"status"`
	AvailabilityZone string   `json:"availability_zone"`
	InstanceID       string   `json:"instance_id"`
	Profile          string   `json:"profile"`
	Tags             []string `json:"tags"` // Add Tags field
}

var (
	ctx     = context.Background()
	rdb     *redis.Client
	expiry  = 5 * time.Minute // Cache expiry time
	version string            // Version variable to be set by ldflags
)

func init() {
	// Load Redis configuration from environment variables or fallback to defaults
	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		redisAddr = "localhost:6379" // Default Redis address
	}

	redisPassword := os.Getenv("REDIS_PASSWORD") // Redis password (optional)

	// Initialize Redis client with dynamic configuration
	rdb = redis.NewClient(&redis.Options{
		Addr:     redisAddr,     // Redis server address
		Password: redisPassword, // Redis authentication password
		TLSConfig: &tls.Config{ // Enable TLS for Redis communication
			InsecureSkipVerify: false,
		},
	})

	// Attempt to load configuration from config.json
	viper.SetConfigName("config")
	viper.SetConfigType("json")
	viper.AddConfigPath(".")
	if err := viper.ReadInConfig(); err != nil {
		log.Printf("⚠️ Warning: Config file not found or unreadable, falling back to tool arguments: %v", err)
	}
}

// Updated getAPIKey to mask sensitive data in logs
func getAPIKey(account string) (string, error) {
	// 1️⃣ Check environment variable
	envKey := os.Getenv("IBMCLOUD_API_KEY_" + strings.ToUpper(account))
	if envKey != "" {
		return envKey, nil
	}

	// 2️⃣ Check config file
	viper.SetConfigName("config")
	viper.SetConfigType("json")
	viper.AddConfigPath(".")
	if err := viper.ReadInConfig(); err == nil {
		if key := viper.GetString("accounts." + account); key != "" {
			return key, nil
		}
	}

	// 3️⃣ Check HashiCorp Vault
	vaultConfig := &api.Config{Address: "http://127.0.0.1:8200"}
	vaultClient, err := api.NewClient(vaultConfig)
	if err == nil {
		vaultClient.SetToken(os.Getenv("VAULT_TOKEN"))
		secret, err := vaultClient.Logical().Read("secret/data/ibmcloud/" + account)
		if err == nil && secret != nil {
			if data, ok := secret.Data["data"].(map[string]interface{}); ok {
				if key, exists := data["api_key"].(string); exists {
					return key, nil
				}
			}
		}
	}

	return "", fmt.Errorf("API key for account %s not found", maskAccount(account))
}

func fetchAllInstances(account string, resourceGroups []string) ([]Instance, error) {
	apiKey, err := getAPIKey(account)
	if err != nil {
		return nil, fmt.Errorf("failed to get API key: %v", err)
	}

	// Fetch all available IBM Cloud regions dynamically
	regions, err := getAllRegions(apiKey)
	if err != nil {
		return nil, err
	}

	var allInstances []Instance
	var wg sync.WaitGroup
	instanceChan := make(chan []Instance)
	workerPool := make(chan struct{}, 10) // Limit concurrency to 10 workers

	for _, region := range regions {
		for _, resourceGroup := range resourceGroups {
			wg.Add(1)
			workerPool <- struct{}{} // Acquire a worker slot
			go func(region, resourceGroup string) {
				defer wg.Done()
				defer func() { <-workerPool }() // Release the worker slot

				instances, err := fetchInstancesForRegionAndResourceGroup(apiKey, region, account, resourceGroup)
				if err != nil {
					log.Printf("⚠️ Error fetching instances for region %s and resource group %s: %v", region, resourceGroup, err)
					return
				}
				instanceChan <- instances
			}(region, resourceGroup)
		}
	}

	// Collect results from goroutines
	go func() {
		wg.Wait()
		close(instanceChan)
	}()

	for instances := range instanceChan {
		allInstances = append(allInstances, instances...)
	}

	// Cache results in Redis
	cacheKey := fmt.Sprintf("instances:%s", account)
	instancesJSON, err := json.Marshal(allInstances)
	if err == nil {
		err = rdb.Set(ctx, cacheKey, instancesJSON, expiry).Err()
		if err == nil {
			log.Printf("✅ Cached %d instances for %s in Redis", len(allInstances), account)
		} else {
			log.Printf("⚠️ Error caching instances: %v", err)
		}
	} else {
		log.Printf("⚠️ Error marshalling instances: %v", err)
	}

	return allInstances, nil
}

// Updated fetchInstances to dynamically fetch regions
func fetchInstances(account string) ([]Instance, error) {
	cacheKey := fmt.Sprintf("instances:%s", account)
	cachedInstances, err := rdb.Get(ctx, cacheKey).Result()
	if err == nil {
		var instances []Instance
		if err := json.Unmarshal([]byte(cachedInstances), &instances); err == nil {
			log.Printf("✅ Retrieved instances for %s from Redis cache", account)
			return instances, nil
		}
		log.Printf("⚠️ Error unmarshalling cached instances for %s: %v", account, err)
	} else {
		log.Printf("ℹ️ No cached instances found for %s, fetching from API", account)
	}

	apiKey, err := getAPIKey(account)
	if err != nil {
		return nil, fmt.Errorf("failed to get API key: %v", err)
	}

	// Dynamically fetch regions instead of using a static list
	regions, err := getAllRegions(apiKey)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch regions: %v", err)
	}

	var allInstances []Instance
	var wg sync.WaitGroup
	instanceChan := make(chan []Instance)

	for _, region := range regions {
		wg.Add(1)
		go func(region string) {
			defer wg.Done()

			instances, err := fetchInstancesForRegion(apiKey, region, account)
			if err != nil {
				log.Printf("⚠️ Error fetching instances for region %s: %v", region, err)
				return
			}
			instanceChan <- instances
		}(region)
	}

	// Collect results from goroutines
	go func() {
		wg.Wait()
		close(instanceChan)
	}()

	for instances := range instanceChan {
		allInstances = append(allInstances, instances...)
	}

	log.Printf("✅ Fetched %d instances for account %s", len(allInstances), maskAccount(account))

	// Cache the instances in Redis
	cacheInstancesInRedis(cacheKey, allInstances)

	return allInstances, nil
}

func getAllRegions(apiKey string) ([]string, error) {
	authenticator := &core.IamAuthenticator{ApiKey: apiKey}
	vpcService, err := vpcv1.NewVpcV1(&vpcv1.VpcV1Options{Authenticator: authenticator})
	if err != nil {
		return nil, fmt.Errorf("failed to create VPC service: %v", err)
	}

	vpcService.SetServiceURL("https://global.iaas.cloud.ibm.com/v1") // Global endpoint

	options := vpcService.NewListRegionsOptions()
	result, _, err := vpcService.ListRegions(options)
	if err != nil {
		return nil, fmt.Errorf("failed to list regions: %v", err)
	}

	var regions []string
	for _, region := range result.Regions {
		regions = append(regions, *region.Name)
	}

	log.Printf("🌎 Available IBM Cloud regions: %v", regions)
	return regions, nil
}

// Update fetchInstanceTags to use globaltaggingv1
func fetchInstanceTags(apiKey, resourceID string) ([]string, error) {
	authenticator := &core.IamAuthenticator{ApiKey: apiKey}
	taggingService, err := globaltaggingv1.NewGlobalTaggingV1(&globaltaggingv1.GlobalTaggingV1Options{
		Authenticator: authenticator,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create tagging service: %v", err)
	}

	options := taggingService.NewListTagsOptions()
	options.SetAttachedTo(resourceID)
	options.SetLimit(100)

	result, _, err := taggingService.ListTags(options)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch tags for resource %s: %v", resourceID, err)
	}

	var tags []string
	for _, tag := range result.Items {
		tags = append(tags, *tag.Name)
	}

	return tags, nil
}

// Update fetchInstancesForRegion to include tags
func fetchInstancesForRegion(apiKey, region, account string) ([]Instance, error) {
	authenticator := &core.IamAuthenticator{ApiKey: apiKey}
	vpcService, err := vpcv1.NewVpcV1(&vpcv1.VpcV1Options{Authenticator: authenticator})
	if err != nil {
		return nil, fmt.Errorf("failed to create VPC service: %v", err)
	}

	vpcServiceURL := fmt.Sprintf("https://%s.iaas.cloud.ibm.com/v1", region)
	vpcService.SetServiceURL(vpcServiceURL)
	log.Printf("🔍 Fetching instances from %s", maskURL(vpcServiceURL))

	// Fetch Floating IPs (for public IP mapping)
	floatingIPMap, err := fetchFloatingIPs(vpcService)
	if err != nil {
		log.Printf("⚠️ Warning: Could not fetch floating IPs for %s: %v", region, err)
	}

	instances := []Instance{}
	options := vpcService.NewListInstancesOptions()

	for {
		result, response, err := vpcService.ListInstances(options)
		if err != nil {
			return nil, fmt.Errorf("failed to list instances in %s: %v (HTTP %d)", region, err, response.StatusCode)
		}

		for _, instance := range result.Instances {
			var privateIP, publicIP string
			for _, iface := range instance.NetworkInterfaces {
				if iface.PrimaryIP != nil {
					privateIP = *iface.PrimaryIP.Address
				}
				if ip, found := floatingIPMap[*iface.ID]; found {
					publicIP = ip
				}
			}

			profile := ""
			if instance.Profile != nil && instance.Profile.Name != nil {
				profile = *instance.Profile.Name
			}

			// Fetch tags for the instance
			tags, err := fetchInstanceTags(apiKey, *instance.CRN)
			if err != nil {
				log.Printf("⚠️ Warning: Could not fetch tags for instance %s: %v", *instance.Name, err)
			}

			instances = append(instances, Instance{
				Name:             *instance.Name,
				ID:               *instance.ID,
				Region:           region,
				Account:          account,
				Status:           *instance.Status,
				AvailabilityZone: *instance.Zone.Name,
				InstanceID:       *instance.CRN,
				PrivateIP:        privateIP,
				PublicIP:         publicIP,
				Profile:          profile,
				Tags:             tags, // Add tags to the instance
			})
		}

		// 🔥 Fix: Extract the 'start' parameter safely
		if result.Next != nil && result.Next.Href != nil {
			nextURL, err := url.Parse(*result.Next.Href)
			if err != nil {
				log.Printf("⚠️ Warning: Failed to parse Next URL for region %s: %v", region, err)
				break
			}

			queryParams := nextURL.Query()
			startParam := queryParams.Get("start")

			if startParam == "" {
				log.Printf("⚠️ Warning: 'start' parameter missing in Next URL for region %s", region)
				break
			}
			// 🔍 Add log statement to track pagination
			log.Printf("🔍 Next pagination token for region %s: %s", region, maskToken(startParam))
			options.SetStart(startParam) // ✅ Use extracted pagination token
		} else {
			break
		}
	}

	return instances, nil
}

func fetchInstancesForRegionAndResourceGroup(apiKey, region, account, resourceGroupName string) ([]Instance, error) {
	log.Printf("🔍 Starting to fetch instances for region '%s' and resource group '%s'", region, resourceGroupName)

	authenticator := &core.IamAuthenticator{ApiKey: apiKey}
	vpcService, err := vpcv1.NewVpcV1(&vpcv1.VpcV1Options{Authenticator: authenticator})
	if err != nil {
		return nil, fmt.Errorf("failed to create VPC service: %v", err)
	}

	vpcServiceURL := fmt.Sprintf("https://%s.iaas.cloud.ibm.com/v1", region)
	vpcService.SetServiceURL(vpcServiceURL)
	log.Printf("🔍 Fetching instances from VPC service URL: %s", maskURL(vpcServiceURL))

	// Fetch the resource group ID for the given resource group name
	resourceGroupID, err := getResourceGroupID(apiKey, resourceGroupName)
	if err != nil {
		log.Printf("❌ Failed to fetch resource group ID for '%s': %v", resourceGroupName, err)
		return nil, fmt.Errorf("failed to fetch resource group ID for %s: %v", resourceGroupName, err)
	}
	log.Printf("✅ Resource group '%s' resolved to ID '%s'", resourceGroupName, resourceGroupID)

	// Fetch Floating IPs (for public IP mapping)
	floatingIPMap, err := fetchFloatingIPs(vpcService)
	if err != nil {
		log.Printf("⚠️ Warning: Could not fetch floating IPs for region '%s': %v", region, err)
	}

	instances := []Instance{}
	options := vpcService.NewListInstancesOptions()
	options.SetResourceGroupID(resourceGroupID) // Apply the resource group ID filter

	for {
		result, response, err := vpcService.ListInstances(options)
		if err != nil {
			log.Printf("❌ Error listing instances in region '%s' for resource group '%s': %v (HTTP %d)", region, resourceGroupName, err, response.StatusCode)
			return nil, fmt.Errorf("failed to list instances in %s: %v (HTTP %d)", region, err, response.StatusCode)
		}

		for _, instance := range result.Instances {
			var privateIP, publicIP string
			for _, iface := range instance.NetworkInterfaces {
				if iface.PrimaryIP != nil {
					privateIP = *iface.PrimaryIP.Address
				}
				if ip, found := floatingIPMap[*iface.ID]; found {
					publicIP = ip
				}
			}

			profile := ""
			if instance.Profile != nil && instance.Profile.Name != nil {
				profile = *instance.Profile.Name
			}

			// Fetch tags for the instance
			tags, err := fetchInstanceTags(apiKey, *instance.CRN)
			if err != nil {
				log.Printf("⚠️ Warning: Could not fetch tags for instance '%s': %v", *instance.Name, err)
			}

			instances = append(instances, Instance{
				Name:             *instance.Name,
				ID:               *instance.ID,
				Region:           region,
				Account:          account,
				Status:           *instance.Status,
				AvailabilityZone: *instance.Zone.Name,
				InstanceID:       *instance.CRN,
				PrivateIP:        privateIP,
				PublicIP:         publicIP,
				Profile:          profile,
				Tags:             tags,
			})
		}

		// Handle pagination
		if result.Next != nil && result.Next.Href != nil {
			nextURL, err := url.Parse(*result.Next.Href)
			if err != nil {
				log.Printf("⚠️ Warning: Failed to parse Next URL for region '%s': %v", region, err)
				break
			}

			queryParams := nextURL.Query()
			startParam := queryParams.Get("start")

			if startParam == "" {
				log.Printf("⚠️ Warning: 'start' parameter missing in Next URL for region '%s'", region)
				break
			}
			log.Printf("🔍 Next pagination token for region '%s': %s", region, maskToken(startParam))
			options.SetStart(startParam)
		} else {
			break
		}
	}

	log.Printf("✅ Fetched %d instances for region '%s' and resource group '%s'", len(instances), region, resourceGroupName)
	return instances, nil
}

// Cache for resource group IDs to reduce redundant API calls
var resourceGroupIDCache = sync.Map{}

// Updated getResourceGroupID to use caching
func getResourceGroupID(apiKey, resourceGroupName string) (string, error) {
	log.Printf("🔍 Resolving resource group name '%s' to ID", resourceGroupName)

	if cachedID, found := resourceGroupIDCache.Load(resourceGroupName); found {
		log.Printf("✅ Resource group '%s' resolved to cached ID '%s'", resourceGroupName, cachedID.(string))
		return cachedID.(string), nil
	}

	authenticator := &core.IamAuthenticator{ApiKey: apiKey}
	resourceManagerService, err := resourcemanagerv2.NewResourceManagerV2(&resourcemanagerv2.ResourceManagerV2Options{
		Authenticator: authenticator,
	})
	if err != nil {
		log.Printf("❌ Failed to create resource manager service: %v", err)
		return "", fmt.Errorf("failed to create resource manager service: %v", err)
	}

	options := resourceManagerService.NewListResourceGroupsOptions()
	result, _, err := resourceManagerService.ListResourceGroups(options)
	if err != nil {
		log.Printf("❌ Failed to list resource groups: %v", err)
		return "", fmt.Errorf("failed to list resource groups: %v", err)
	}

	for _, group := range result.Resources {
		if *group.Name == resourceGroupName {
			resourceGroupIDCache.Store(resourceGroupName, *group.ID) // Cache the ID
			log.Printf("✅ Resource group '%s' resolved to ID '%s'", resourceGroupName, *group.ID)
			return *group.ID, nil
		}
	}

	log.Printf("❌ Resource group '%s' not found", resourceGroupName)
	return "", fmt.Errorf("resource group %s not found", resourceGroupName)
}

func fetchInstanceIPs(apiKey, region string) (map[string]Instance, error) {
	authenticator := &core.IamAuthenticator{ApiKey: apiKey}
	vpcService, err := vpcv1.NewVpcV1(&vpcv1.VpcV1Options{Authenticator: authenticator})
	if err != nil {
		return nil, err
	}

	correctedRegion := strings.TrimSuffix(region, "-1") // Ensure correct region format
	vpcServiceURL := fmt.Sprintf("https://%s.iaas.cloud.ibm.com/v1", correctedRegion)
	vpcService.SetServiceURL(vpcServiceURL)
	log.Printf("✅ Using VPC service URL: %s", maskURL(vpcServiceURL))

	// Fetch all floating IPs first
	floatingIPMap, err := fetchFloatingIPs(vpcService)
	if err != nil {
		log.Printf("⚠️ Warning: Could not fetch floating IPs: %v", err)
	}

	// Fetch instances
	options := vpcService.NewListInstancesOptions()
	instancesResult, _, err := vpcService.ListInstances(options)
	if err != nil {
		log.Printf("❌ Error fetching instances from VPC: %v", err)
		return nil, err
	}

	instanceMap := make(map[string]Instance)

	for _, instance := range instancesResult.Instances {
		var privateIP, publicIP string

		for _, iface := range instance.NetworkInterfaces {
			if iface.PrimaryIP != nil {
				privateIP = *iface.PrimaryIP.Address
			}

			// Check if this interface has a mapped floating IP
			if iface.ID != nil {
				if ip, found := floatingIPMap[*iface.ID]; found {
					publicIP = ip
					log.Printf("🌍 Public IP %s assigned to instance %s", maskIP(publicIP), *instance.Name)
				}
			}
		}

		if publicIP == "" {
			log.Printf("⚠️ Instance %s (%s) in %s has no public IP assigned!", *instance.Name, *instance.ID, correctedRegion)
		}

		instanceMap[*instance.ID] = Instance{
			Name:             *instance.Name,
			ID:               *instance.ID,
			Region:           correctedRegion,
			PrivateIP:        privateIP,
			PublicIP:         publicIP, // ✅ Now correctly assigned
			Status:           *instance.Status,
			AvailabilityZone: *instance.Zone.Name,
			InstanceID:       *instance.CRN,
		}
	}

	return instanceMap, nil
}

func fetchFloatingIPs(vpcService *vpcv1.VpcV1) (map[string]string, error) {
	options := vpcService.NewListFloatingIpsOptions()
	result, _, err := vpcService.ListFloatingIps(options)
	if err != nil {
		log.Printf("❌ Error fetching floating IPs: %v", err)
		return nil, err
	}

	floatingIPMap := make(map[string]string)

	for _, fip := range result.FloatingIps {
		if fip.Target == nil {
			log.Printf("⚠️ Floating IP %s has no target assigned! Skipping...", maskIP(*fip.Address))
			continue
		}

		switch target := fip.Target.(type) {
		case *vpcv1.FloatingIPTargetNetworkInterfaceReference:
			if target.ID != nil && fip.Address != nil {
				floatingIPMap[*target.ID] = *fip.Address
				log.Printf("✅ Floating IP %s mapped to VM Network Interface ID %s", maskIP(*fip.Address), *target.ID)
			}
		default:
			log.Printf("⚠️ Floating IP %s is not attached to a network interface (Target type: %T)", maskIP(*fip.Address), target)
		}
	}

	return floatingIPMap, nil
}

func instanceHandler(w http.ResponseWriter, r *http.Request) {
	accounts := r.URL.Query().Get("accounts")
	if accounts == "" {
		accounts = "account1"
	}

	regions := r.URL.Query().Get("regions")
	if regions == "" {
		regions = "us-east"
	}

	resourceGroups := r.URL.Query().Get("resource_groups")
	if resourceGroups == "" {
		resourceGroups = "default"
	}

	accountList := strings.Split(accounts, ",")
	regionList := strings.Split(regions, ",")
	resourceGroupList := strings.Split(resourceGroups, ",")
	var allInstances []Instance
	instanceCache := make(map[string]map[string]Instance) // Cache IPs per region

	var wg sync.WaitGroup
	instanceChan := make(chan []Instance)

	for _, account := range accountList {
		wg.Add(1)
		go func(account string) {
			defer wg.Done()
			instances, err := fetchAllInstances(account, resourceGroupList)
			if err != nil {
				log.Printf("Error fetching instances for %s: %v", account, err)
				return
			}

			for i, inst := range instances {
				if !contains(regionList, inst.Region) {
					continue
				}

				if _, exists := instanceCache[inst.Region]; !exists {
					apiKey, err := getAPIKey(account)
					if err != nil {
						log.Printf("Error fetching API key for %s: %v", account, err)
						continue
					}
					instanceCache[inst.Region], _ = fetchInstanceIPs(apiKey, inst.Region)
				}

				if ipInfo, found := instanceCache[inst.Region][inst.ID]; found {
					instances[i].PrivateIP = ipInfo.PrivateIP
					instances[i].PublicIP = ipInfo.PublicIP
					instances[i].Profile = ipInfo.Profile
				}
			}

			instanceChan <- instances
		}(account)
	}

	go func() {
		wg.Wait()
		close(instanceChan)
	}()

	for instances := range instanceChan {
		allInstances = append(allInstances, instances...)
	}

	log.Printf("✅ Total instances fetched: %d", len(allInstances))

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(allInstances)
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

func helpHandler(w http.ResponseWriter, r *http.Request) {
	helpText := fmt.Sprintf(`
IBM Cloud Service Discovery Tool

Version: %s

Usage:
  -accounts string
        Comma-separated list of IBM Cloud accounts (default "account1,account2")
  -regions string
        Comma-separated list of IBM Cloud regions (default "us-east")

Endpoints:
  /instances - Fetch instances from specified accounts and regions
  /help - Display this help message
  /prometheus - Prometheus metrics endpoint

Examples:
  Fetch instances from default accounts and regions:
    curl http://localhost:8080/instances

  Fetch instances from specific accounts and regions:
    curl "http://localhost:8080/instances?accounts=account1,account2&regions=us-east,eu-de"
`, version)
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(helpText))
}

func prometheusHandler(w http.ResponseWriter, r *http.Request) {
	accounts := r.URL.Query().Get("accounts")
	if accounts == "" {
		accounts = "account1,account2"
	}

	regions := r.URL.Query().Get("regions")
	if regions == "" {
		regions = "us-east"
	}

	resourceGroups := r.URL.Query().Get("resource_groups")
	if resourceGroups == "" {
		resourceGroups = "default"
	}

	outputFile := r.URL.Query().Get("output_file")
	if outputFile == "" {
		outputFile = viper.GetString("output_sd_file") // Fallback to config.json
	}

	accountList := strings.Split(accounts, ",")
	regionList := strings.Split(regions, ",")
	resourceGroupList := strings.Split(resourceGroups, ",")

	var allInstances []Instance
	instanceChan := make(chan []Instance)
	var wg sync.WaitGroup

	for _, account := range accountList {
		wg.Add(1)
		go func(account string) {
			defer wg.Done()
			instances, err := fetchAllInstances(account, resourceGroupList)
			if err != nil {
				log.Printf("Error fetching instances for %s: %v", account, err)
				return
			}

			// Filter instances by regions
			filteredInstances := []Instance{}
			for _, inst := range instances {
				if contains(regionList, inst.Region) {
					filteredInstances = append(filteredInstances, inst)
				}
			}

			instanceChan <- filteredInstances
		}(account)
	}

	go func() {
		wg.Wait()
		close(instanceChan)
	}()

	for instances := range instanceChan {
		allInstances = append(allInstances, instances...)
	}

	targets := []map[string]interface{}{}
	for _, instance := range allInstances {
		labels := map[string]string{
			"instance":          instance.Name,
			"region":            instance.Region,
			"account":           instance.Account,
			"status":            instance.Status,
			"private_ip":        instance.PrivateIP,
			"public_ip":         instance.PublicIP,
			"instance_id":       instance.InstanceID,
			"availability_zone": instance.AvailabilityZone,
			"profile":           instance.Profile,
			"resource_group":    instance.Account,
		}

		// Add tags as separate labels
		for i, tag := range instance.Tags {
			labels[fmt.Sprintf("tag_%d", i)] = tag
		}

		target := map[string]interface{}{
			"targets": []string{instance.PrivateIP},
			"labels":  labels,
		}
		targets = append(targets, target)
	}

	// Write to file if outputFile is specified
	if outputFile != "" {
		file, err := os.Create(outputFile)
		if err != nil {
			log.Printf("Error creating output file: %v", err)
			http.Error(w, "Failed to create output file", http.StatusInternalServerError)
			return
		}
		defer file.Close()

		encoder := json.NewEncoder(file)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(targets); err != nil {
			log.Printf("Error writing to output file: %v", err)
			http.Error(w, "Failed to write to output file", http.StatusInternalServerError)
			return
		}

		log.Printf("Prometheus file-based service discovery JSON written to %s", outputFile)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(targets)
}

func healthCheckHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"healthy"}`))
}

// Add a new endpoint to demonstrate sensitive data masking
func maskingDemoHandler(w http.ResponseWriter, r *http.Request) {
	apiKey := "example-api-key"
	token := "example-token"
	url := "https://example.com"
	ip := "192.168.1.1"

	response := map[string]string{
		"masked_api_key": maskSensitiveData(apiKey),
		"masked_token":   maskToken(token),
		"masked_url":     maskURL(url),
		"masked_ip":      maskIP(ip),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// Add a new endpoint to demonstrate Redis caching fallback
func redisFallbackDemoHandler(w http.ResponseWriter, r *http.Request) {
	cacheKey := "demo:instances"
	instances := []Instance{
		{Name: "Instance1", ID: "id1", Region: "us-east", Account: "account1"},
		{Name: "Instance2", ID: "id2", Region: "us-south", Account: "account2"},
	}

	// Attempt to cache instances
	cacheInstancesInRedis(cacheKey, instances)

	// Attempt to retrieve cached instances
	cachedInstances, err := rdb.Get(ctx, cacheKey).Result()
	if err != nil {
		log.Printf("⚠️ Redis unavailable, falling back to in-memory data: %v", err)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(instances)
		return
	}

	var retrievedInstances []Instance
	if err := json.Unmarshal([]byte(cachedInstances), &retrievedInstances); err != nil {
		log.Printf("⚠️ Error unmarshalling cached data: %v", err)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(instances)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(retrievedInstances)
}

// Add a new endpoint to demonstrate Prometheus file versioning
func prometheusVersioningDemoHandler(w http.ResponseWriter, r *http.Request) {
	config := Config{
		Accounts:       map[string]string{"default": "account1"},
		Regions:        map[string]string{"default": "us-east"},
		ResourceGroups: map[string]string{"default": "default"},
		OutputSDFile:   "./prometheus_sd_demo.json",
	}

	writeSDConfig(config)

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status":"Prometheus file versioning demo completed"}`))
}

// Ensure writeSDConfig is used in main
func main() {
	// Define command-line arguments with fallback to viper (config.json)
	accounts := flag.String("accounts", viper.GetString("accounts"), "Comma-separated list of IBM Cloud accounts")
	regions := flag.String("regions", viper.GetString("regions"), "Comma-separated list of IBM Cloud regions")
	port := flag.String("port", viper.GetString("port"), "Port to run the server on")
	resourceGroups := flag.String("resource_groups", viper.GetString("resource_groups"), "Comma-separated list of IBM Cloud resource groups")
	showVersion := flag.Bool("version", false, "Show tool version")
	outputSDFile := flag.String("output-sd-file", viper.GetString("output_sd_file"), "Path to output file_sd_configs JSON file")
	certFile := flag.String("cert", "", "Path to the TLS certificate file (optional)")
	keyFile := flag.String("key", "", "Path to the TLS key file (optional)")
	flag.Parse()

	// Handle version flag
	if *showVersion {
		fmt.Printf("IBM Cloud Service Discovery Tool Version: %s\n", version)
		return
	}

	// Fallback to default values if config.json is missing and arguments are not provided
	if *accounts == "" {
		*accounts = "account1,account2"
	}
	if *regions == "" {
		*regions = "us-east"
	}
	if *port == "" {
		*port = "8080"
	}
	if *resourceGroups == "" {
		*resourceGroups = "default"
	}
	if *outputSDFile == "" {
		*outputSDFile = "./prometheus_sd.json"
	}

	// Log the configuration being used
	log.Printf("✅ Using configuration: accounts=%s, regions=%s, port=%s, resource_groups=%s, output_sd_file=%s",
		*accounts, *regions, *port, *resourceGroups, *outputSDFile)

	// Create the Prometheus file-based service discovery JSON file if outputSDFile is provided
	if *outputSDFile != "" {
		config := Config{
			Accounts:       map[string]string{"default": *accounts},
			Regions:        map[string]string{"default": *regions},
			ResourceGroups: map[string]string{"default": *resourceGroups},
			OutputSDFile:   *outputSDFile,
		}
		writeSDConfig(config) // Ensure this function is used
		log.Printf("✅ Prometheus file-based service discovery JSON written to %s", *outputSDFile)
	}

	// Start the HTTP server
	http.HandleFunc("/instances", func(w http.ResponseWriter, r *http.Request) {
		r.URL.RawQuery = fmt.Sprintf("accounts=%s&regions=%s&resource_groups=%s", *accounts, *regions, *resourceGroups)
		instanceHandler(w, r)
	})

	http.HandleFunc("/help", helpHandler)
	http.HandleFunc("/prometheus", prometheusHandler)
	http.HandleFunc("/health", healthCheckHandler)
	http.HandleFunc("/masking-demo", maskingDemoHandler)
	http.HandleFunc("/redis-fallback-demo", redisFallbackDemoHandler)
	http.HandleFunc("/prometheus-versioning-demo", prometheusVersioningDemoHandler)

	// Check if TLS certificates are provided
	if *certFile != "" && *keyFile != "" {
		log.Printf("🔒 Starting HTTPS server on :%s", *port)
		log.Fatal(http.ListenAndServeTLS(":"+*port, *certFile, *keyFile, nil))
	} else {
		log.Printf("🌐 Starting HTTP server on :%s", *port)
		log.Fatal(http.ListenAndServe(":"+*port, nil))
	}
}

func maskAccount(account string) string {
	if len(account) > 4 {
		return account[:2] + strings.Repeat("*", len(account)-4) + account[len(account)-2:]
	}
	return account
}

func maskURL(rawURL string) string {
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	return parsedURL.Scheme + "://" + parsedURL.Host + "/****"
}

func maskToken(token string) string {
	if len(token) > 4 {
		return token[:2] + strings.Repeat("*", len(token)-4) + token[len(token)-2:]
	}
	return token
}

func maskIP(ip string) string {
	parts := strings.Split(ip, ".")
	if len(parts) == 4 {
		return parts[0] + ".***.***." + parts[3]
	}
	return ip
}

// Graceful fallback for Redis caching
func cacheInstancesInRedis(key string, instances []Instance) error {
	instancesJSON, err := json.Marshal(instances)
	if err != nil {
		log.Printf("⚠️ Error marshalling instances: %v", err)
		return err
	}

	err = rdb.Set(ctx, key, instancesJSON, expiry).Err()
	if err != nil {
		log.Printf("⚠️ Redis unavailable, skipping caching: %v", err)
		return err
	}

	return nil
}

// Updated sensitive data masking for logs
func maskSensitiveData(data string) string {
	if len(data) > 4 {
		return data[:2] + strings.Repeat("*", len(data)-4) + data[len(data)-2:]
	}
	return data
}

// Add versioning for Prometheus output file
func writeSDConfig(config Config) {
	outputFile := config.OutputSDFile
	backupFile := outputFile + ".bak"

	// Create a backup of the existing file
	if _, err := os.Stat(outputFile); err == nil {
		if err := os.Rename(outputFile, backupFile); err != nil {
			log.Printf("⚠️ Failed to create backup of output file: %v", err)
			return
		}
		log.Printf("✅ Backup created: %s", backupFile)
	}

	file, err := os.Create(outputFile)
	if err != nil {
		log.Printf("❌ Error creating output file: %v", err)
		return
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(config); err != nil {
		log.Printf("❌ Error encoding JSON: %v", err)
		return
	}

	log.Printf("✅ Prometheus file successfully written to %s", outputFile)
}

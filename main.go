package main

import (
	"context"
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
	"github.com/IBM/vpc-go-sdk/vpcv1"
	"github.com/go-redis/redis/v8"
	"github.com/hashicorp/vault/api"
	"github.com/spf13/viper"
)

// Config structure
type Config struct {
	Accounts map[string]string `json:"accounts"`
}

// Instance struct
type Instance struct {
	Name             string `json:"name"`
	ID               string `json:"id"`
	Region           string `json:"region"`
	Account          string `json:"account"`
	PublicIP         string `json:"public_ip"`
	PrivateIP        string `json:"private_ip"`
	Status           string `json:"status"`
	AvailabilityZone string `json:"availability_zone"`
	InstanceID       string `json:"instance_id"`
	Profile          string `json:"profile"`
}

var (
	ctx    = context.Background()
	rdb    *redis.Client
	expiry = 5 * time.Minute // Cache expiry time
)

const version = "1.0.0"

func init() {
	rdb = redis.NewClient(&redis.Options{
		Addr: "localhost:6379", // Redis server address
	})
}

// Load API Key from different sources
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

	return "", fmt.Errorf("API key for account %s not found", account)
}

func fetchAllInstances(account string) ([]Instance, error) {
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

	// Get list of supported VPC regions
	regions := []string{"us-south", "us-east", "eu-de", "eu-gb", "jp-tok", "jp-osa", "au-syd"}

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
	instancesJSON, err := json.Marshal(allInstances)
	if err == nil {
		err = rdb.Set(ctx, cacheKey, instancesJSON, expiry).Err()
		if err == nil {
			log.Printf("✅ Cached instances for %s in Redis", account)
		} else {
			log.Printf("⚠️ Error caching instances for %s in Redis: %v", account, err)
		}
	} else {
		log.Printf("⚠️ Error marshalling instances for %s: %v", account, err)
	}

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
		accounts = "account1,account2"
	}

	regions := r.URL.Query().Get("regions")
	if regions == "" {
		regions = "us-east"
	}

	accountList := strings.Split(accounts, ",")
	regionList := strings.Split(regions, ",")
	var allInstances []Instance
	instanceCache := make(map[string]map[string]Instance) // Cache IPs per region

	var wg sync.WaitGroup
	instanceChan := make(chan []Instance)

	for _, account := range accountList {
		wg.Add(1)
		go func(account string) {
			defer wg.Done()
			instances, err := fetchInstances(account)
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

	accountList := strings.Split(accounts, ",")
	var allInstances []Instance
	instanceChan := make(chan []Instance)
	var wg sync.WaitGroup

	for _, account := range accountList {
		wg.Add(1)
		go func(account string) {
			defer wg.Done()
			instances, err := fetchInstances(account)
			if err != nil {
				log.Printf("Error fetching instances for %s: %v", account, err)
				return
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

	targets := []map[string]interface{}{}
	for _, instance := range allInstances {
		target := map[string]interface{}{
			"targets": []string{instance.PrivateIP},
			"labels": map[string]string{
				"instance":          instance.Name,
				"region":            instance.Region,
				"account":           instance.Account,
				"status":            instance.Status,
				"private_ip":        instance.PrivateIP,
				"public_ip":         instance.PublicIP,
				"instance_id":       instance.InstanceID,
				"availability_zone": instance.AvailabilityZone,
				"profile":           instance.Profile,
			},
		}
		targets = append(targets, target)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(targets)
}

func main() {
	accounts := flag.String("accounts", "account1,account2", "Comma-separated list of IBM Cloud accounts")
	regions := flag.String("regions", "us-east", "Comma-separated list of IBM Cloud regions")
	showVersion := flag.Bool("version", false, "Show tool version")
	flag.Parse()

	if *showVersion {
		fmt.Printf("IBM Cloud Service Discovery Tool Version: %s\n", version)
		return
	}

	if len(os.Args) == 1 {
		fmt.Printf("IBM Cloud Service Discovery Tool Version: %s\n", version)
		fmt.Println("Usage:")
		flag.PrintDefaults()
		return
	}

	http.HandleFunc("/instances", func(w http.ResponseWriter, r *http.Request) {
		r.URL.RawQuery = fmt.Sprintf("accounts=%s&regions=%s", *accounts, *regions)
		instanceHandler(w, r)
	})

	http.HandleFunc("/help", helpHandler)
	http.HandleFunc("/prometheus", prometheusHandler)

	fmt.Println("IBM Cloud Service Discovery running on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
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

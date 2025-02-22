package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/IBM/go-sdk-core/v5/core"
	"github.com/IBM/platform-services-go-sdk/globaltaggingv1"
	"github.com/IBM/platform-services-go-sdk/resourcecontrollerv2"
	"github.com/IBM/vpc-go-sdk/vpcv1"
	"github.com/sirupsen/logrus"
)

// Instance struct with IPs
type Instance struct {
	Name      string `json:"name"`
	ID        string `json:"id"`
	Region    string `json:"region"`
	Account   string `json:"account"`
	PublicIP  string `json:"public_ip"`
	PrivateIP string `json:"private_ip"`
}

// Logger with stylish icons
var log = logrus.New()

func init() {
	log.SetFormatter(&logrus.TextFormatter{
		FullTimestamp:   true,
		TimestampFormat: time.RFC3339,
	})
	log.SetOutput(os.Stdout)
	log.SetLevel(logrus.InfoLevel)
}

// Fetch VPC Instance Details (IP Addresses)
func fetchInstanceIPs(apiKey, region string) (map[string]Instance, error) {
	authenticator := &core.IamAuthenticator{ApiKey: apiKey}
	vpcService, err := vpcv1.NewVpcV1(&vpcv1.VpcV1Options{Authenticator: authenticator})
	if err != nil {
		return nil, err
	}

	vpcService.SetServiceURL(fmt.Sprintf("https://%s.iaas.cloud.ibm.com/v1", region))

	// Fetch instances
	options := vpcService.NewListInstancesOptions()
	result, _, err := vpcService.ListInstances(options)
	if err != nil {
		return nil, err
	}

	instanceMap := make(map[string]Instance)

	for _, instance := range result.Instances {
		var privateIP, publicIP string

		// Extract network interfaces
		for _, iface := range instance.NetworkInterfaces {
			if iface.PrimaryIP != nil {
				privateIP = *iface.PrimaryIP.Address
			}
			if iface.FloatingIps != nil && len(iface.FloatingIps) > 0 {
				publicIP = *iface.FloatingIps[0].Address
			}
		}

		instanceMap[*instance.ID] = Instance{
			Name:      *instance.Name,
			ID:        *instance.ID,
			Region:    region,
			PrivateIP: privateIP,
			PublicIP:  publicIP,
		}
	}

	return instanceMap, nil
}

// Get IAM Token from Metadata Service
func getIAMTokenFromMetadata() (string, error) {
	metadataURL := "http://169.254.169.254/instance_identity/v1/token"
	req, err := http.NewRequest("PUT", metadataURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Metadata-Flavor", "ibm")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var tokenResponse map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&tokenResponse); err != nil {
		return "", err
	}

	return tokenResponse["access_token"], nil
}

// Get IBM Cloud Authenticator (IAM Role or API Key)
func getAuthenticator(account string) (core.Authenticator, error) {
	// Try IAM Role-based authentication first
	iamToken, err := getIAMTokenFromMetadata()
	if err == nil {
		return &core.BearerTokenAuthenticator{BearerToken: iamToken}, nil
	}

	// Fallback to API Key authentication
	apiKey := os.Getenv("IBMCLOUD_API_KEY_" + strings.ToUpper(account))
	if apiKey == "" {
		return nil, fmt.Errorf("API key not found for account %s", account)
	}

	return &core.IamAuthenticator{ApiKey: apiKey}, nil
}

// Fetch Instances with IAM or API Key Authenticator
func fetchInstances(account string, filterTags []string) ([]Instance, error) {
	log.Infof("🔄 Fetching instances for IBM Cloud Account: [%s]", account)

	authenticator, err := getAuthenticator(account)
	if err != nil {
		log.Errorf("❌ Authenticator error for %s: %v", account, err)
		return nil, err
	}

	service, err := resourcecontrollerv2.NewResourceControllerV2(&resourcecontrollerv2.ResourceControllerV2Options{
		Authenticator: authenticator,
	})
	if err != nil {
		log.Errorf("❌ IBM Resource Service Error: %v", err)
		return nil, err
	}

	listOptions := service.NewListResourceInstancesOptions()
	result, _, err := service.ListResourceInstances(listOptions)
	if err != nil {
		log.Errorf("❌ Error listing instances for %s: %v", account, err)
		return nil, err
	}

	log.Infof("✅ Successfully retrieved %d instances from IBM Cloud API", len(result.Resources))

	var instances []Instance
	for _, res := range result.Resources {
		tags, err := getInstanceTags(*res.GUID, account)
		if err != nil {
			log.Warnf("⚠️ Failed to fetch tags for instance [%s]", *res.GUID)
			continue
		}

		if !hasRequiredTags(tags, filterTags) {
			log.Infof("🚫 Instance [%s] skipped due to tag mismatch", *res.GUID)
			continue
		}

		instances = append(instances, Instance{
			Name:    *res.Name,
			ID:      *res.GUID,
			Region:  *res.RegionID,
			Account: account,
			Tags:    tags,
		})
	}

	log.Infof("📦 Final filtered instance count: [%d]", len(instances))
	return instances, nil
}

// Fetch tags for a given IBM Cloud instance
func getInstanceTags(instanceID string, account string) ([]string, error) {
	authenticator, err := getAuthenticator(account)
	if err != nil {
		return nil, err
	}

	service, err := globaltaggingv1.NewGlobalTaggingV1(&globaltaggingv1.GlobalTaggingV1Options{
		Authenticator: authenticator,
	})
	if err != nil {
		return nil, err
	}

	options := service.NewListTagsOptions()
	options.SetAttachedTo(instanceID)

	result, _, err := service.ListTags(options)
	if err != nil {
		return nil, err
	}

	var tags []string
	for _, tag := range result.Items {
		tags = append(tags, *tag.Name)
	}

	return tags, nil
}

// Check if an instance has the required tags
func hasRequiredTags(instanceTags, requiredTags []string) bool {
	if len(requiredTags) == 0 {
		return true // No filtering applied
	}

	tagSet := make(map[string]struct{})
	for _, tag := range instanceTags {
		tagSet[tag] = struct{}{}
	}

	for _, requiredTag := range requiredTags {
		if _, found := tagSet[requiredTag]; !found {
			return false
		}
	}
	return true
}

func ibmServiceDiscoveryHandler(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()

	accounts := r.URL.Query().Get("accounts")
	if accounts == "" {
		accounts = "account1,account2"
	}

	tagsQuery := r.URL.Query().Get("tags")
	var filterTags []string
	if tagsQuery != "" {
		filterTags = strings.Split(tagsQuery, ",")
	}

	log.Infof("🔍 Received request for IBM Cloud SD | Accounts: [%s] | Tags: [%v] | Timestamp: [%s]",
		accounts, filterTags, startTime.Format(time.RFC3339))

	accountList := strings.Split(accounts, ",")
	var allInstances []map[string]string

	for _, account := range accountList {
		instances, err := fetchInstances(account, filterTags)
		if err != nil {
			log.Errorf("❌ Error fetching instances for %s: %v", account, err)
			continue
		}

		log.Infof("✅ Fetched %d instances for account %s", len(instances), account)

		for _, inst := range instances {
			allInstances = append(allInstances, map[string]string{
				"__address__":    inst.PrivateIP + ":9100",
				"instance_name":  inst.Name,
				"public_ip":      inst.PublicIP,
				"private_ip":     inst.PrivateIP,
				"region":         inst.Region,
				"account":        inst.Account,
				"instance_id":    inst.ID,
				"instance_type":  inst.Type,
				"cloud_provider": "ibm",
			})
		}
	}

	responseTime := time.Since(startTime)
	log.Infof("📡 Response Sent | Instances: [%d] | Processing Time: [%s]", len(allInstances), responseTime)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(allInstances)
}

func prometheusHandler(w http.ResponseWriter, r *http.Request) {
	accounts := r.URL.Query().Get("accounts")
	if accounts == "" {
		accounts = "account1,account2"
	}

	accountList := strings.Split(accounts, ",")
	var allTargets []map[string]interface{}

	for _, account := range accountList {
		instances, err := fetchInstances(account)
		if err != nil {
			log.Printf("Error fetching instances for %s: %v", account, err)
			continue
		}

		for _, inst := range instances {
			target := map[string]interface{}{
				"targets": []string{inst.PrivateIP + ":9100"}, // Node Exporter Port
				"labels": map[string]string{
					"job":           "ibm-instance",
					"instance_name": inst.Name,
					"public_ip":     inst.PublicIP,
					"region":        inst.Region,
					"account":       inst.Account,
				},
			}
			allTargets = append(allTargets, target)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(allTargets)
}

func main() {
	http.HandleFunc("/instances", instanceHandler)    // Normal JSON
	http.HandleFunc("/prometheus", prometheusHandler) // Prometheus format

	fmt.Println("IBM Cloud Service Discovery running on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}

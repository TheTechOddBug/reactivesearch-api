package util

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strings"
)

// ACCAPI URL
var ACCAPI = "https://accapi.appbase.io/"

// var ACCAPI = "http://localhost:3000/"

// TimeValidity to be obtained from ACCAPI
var TimeValidity int64

// Tier is the value of the user's plan
var Tier *Plan

// Feature custom events
var FeatureCustomEvents bool

// Feature suggestions
var FeatureSuggestions bool

// MaxErrorTime before showing errors if invalid trial / plan in hours
var MaxErrorTime int64 = 24 // in hrs

// NodeCount is the current node count, defaults to 1
var NodeCount = 1

// ArcUsage struct is used to report time usage
type ArcUsage struct {
	ArcID          string `json:"arc_id"`
	SubscriptionID string `json:"subscription_id"`
	Quantity       int    `json:"quantity"`
	ClusterID      string `json:"cluster_id"`
}

type ClusterPlan struct {
	Tier                *Plan  `json:"tier"`
	FeatureCustomEvents bool   `json:"feature_custom_events"`
	FeatureSuggestions  bool   `json:"feature_suggestions"`
	Trial               bool   `json:"trial"`
	TrialValidity       int64  `json:"trial_validity"`
	TierValidity        int64  `json:"tier_validity"`
	TimeValidity        int64  `json:"time_validity"`
	SubscriptionID      string `json:"subscription_id"`
}

// ArcUsageResponse stores the response from ACCAPI
type ArcUsageResponse struct {
	Accepted      bool   `json:"accepted"`
	FailureReason string `json:"failure_reason"`
	ErrorMsg      string `json:"error_msg"`
	WarningMsg    string `json:"warning_msg"`
	StatusCode    int    `json:"status_code"`
	TimeValidity  int64  `json:"time_validity"`
}

// ArcInstance TBD: remove struct
type ArcInstance struct {
	SubscriptionID string `json:"subscription_id"`
}

// ArcInstanceResponse TBD: Remove struct
type ArcInstanceResponse struct {
	ArcInstances []ArcInstanceDetails `json:"instances"`
}

// Cluster plan response type
type ClusterPlanResponse struct {
	Plan ClusterPlan `json:"plan"`
}

// ArcInstanceDetails contains the info about an Arc Instance
type ArcInstanceDetails struct {
	NodeCount            int                    `json:"node_count"`
	Description          string                 `json:"description"`
	SubscriptionID       string                 `json:"subscription_id"`
	SubscriptionCanceled bool                   `json:"subscription_canceled"`
	Trial                bool                   `json:"trial"`
	TrialValidity        int64                  `json:"trial_validity"`
	CreatedAt            int64                  `json:"created_at"`
	Tier                 *Plan                  `json:"tier"`
	TierValidity         int64                  `json:"tier_validity"`
	TimeValidity         int64                  `json:"time_validity"`
	Metadata             map[string]interface{} `json:"metadata"`
	FeatureCustomEvents  bool                   `json:"feature_custom_events"`
	FeatureSuggestions   bool                   `json:"feature_suggestions"`
}

// BillingMiddleware function to be called for each request
func BillingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Println("current time validity value: ", TimeValidity)
		// Blacklist subscription routes
		if strings.HasPrefix(r.RequestURI, "/arc/subscription") || strings.HasPrefix(r.RequestURI, "/arc/plan") {
			next.ServeHTTP(w, r)
		} else if TimeValidity > 0 { // Valid plan
			next.ServeHTTP(w, r)
		} else if TimeValidity <= 0 && -TimeValidity < 3600*MaxErrorTime { // Negative validity, plan has been expired
			// Print warning message if remaining time is less than max allowed time
			log.Println("Warning: Payment is required. Arc will start sending out error messages in next", MaxErrorTime, "hours")
			next.ServeHTTP(w, r)
		} else {
			// Write an error and stop the handler chain
			http.Error(w, "payment required", http.StatusPaymentRequired)
		}
	})
}

func getArcInstance(arcID string) (ArcInstance, error) {
	arcInstance := ArcInstance{}
	response := ArcInstanceResponse{}
	url := ACCAPI + "arc/instances?arcid=" + arcID
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Add("Content-Type", "application/json")
	req.Header.Add("cache-control", "no-cache")

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Println("error while sending request: ", err)
		return arcInstance, err
	}
	defer res.Body.Close()
	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		log.Println("error reading res body: ", err)
		return arcInstance, err
	}
	err = json.Unmarshal(body, &response)
	if len(response.ArcInstances) != 0 {
		arcInstanceByID := response.ArcInstances[0]
		arcInstance.SubscriptionID = arcInstanceByID.SubscriptionID
		TimeValidity = arcInstanceByID.TimeValidity
		Tier = arcInstanceByID.Tier
		FeatureCustomEvents = arcInstanceByID.FeatureCustomEvents
		FeatureSuggestions = arcInstanceByID.FeatureSuggestions
	} else {
		return arcInstance, errors.New("No valid instance found for the provided ARC_ID")
	}

	if err != nil {
		log.Println("error while unmarshalling res body: ", err)
		return arcInstance, err
	}
	return arcInstance, nil
}

func getArcClusterInstance(clusterID string) (ArcInstance, error) {
	arcInstance := ArcInstance{}
	var response ArcInstanceResponse
	url := ACCAPI + "byoc/" + clusterID
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Add("Content-Type", "application/json")
	req.Header.Add("cache-control", "no-cache")

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Println("error while sending request: ", err)
		return arcInstance, err
	}
	defer res.Body.Close()
	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		log.Println("error reading res body: ", err)
		return arcInstance, err
	}
	err = json.Unmarshal(body, &response)

	if err != nil {
		log.Println("error while unmarshalling res body: ", err)
		return arcInstance, err
	}
	if len(response.ArcInstances) != 0 {
		arcInstanceDetails := response.ArcInstances[0]
		arcInstance.SubscriptionID = arcInstanceDetails.SubscriptionID
		TimeValidity = arcInstanceDetails.TimeValidity
		Tier = arcInstanceDetails.Tier
		FeatureCustomEvents = arcInstanceDetails.FeatureCustomEvents
		FeatureSuggestions = arcInstanceDetails.FeatureSuggestions
	} else {
		return arcInstance, errors.New("No valid instance found for the provided CLUSTER_ID")
	}
	return arcInstance, nil
}

// Fetches the cluster plan details for the encrypted cluster id
func getClusterPlan(clusterID string) (ClusterPlan, error) {
	clusterPlan := ClusterPlan{}
	var response ClusterPlanResponse
	url := ACCAPI + "v1/plan/" + clusterID
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Add("Content-Type", "application/json")
	req.Header.Add("cache-control", "no-cache")

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Println("error while sending request: ", err)
		return clusterPlan, err
	}
	defer res.Body.Close()
	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		log.Println("error reading res body: ", err)
		return clusterPlan, err
	}
	err = json.Unmarshal(body, &response)

	if err != nil {
		log.Println("error while unmarshalling res body: ", err)
		return clusterPlan, err
	}

	if response.Plan.Tier == nil {
		return clusterPlan, fmt.Errorf("error while getting the cluster plan")
	}
	// Set the plan for clusters
	Tier = response.Plan.Tier
	TimeValidity = response.Plan.TimeValidity
	FeatureCustomEvents = response.Plan.FeatureCustomEvents
	FeatureSuggestions = response.Plan.FeatureSuggestions

	return clusterPlan, nil
}

// SetClusterPlan fetches the cluster plan & sets the Tier value
func SetClusterPlan() {
	log.Printf("=> Getting cluster plan details")
	clusterID := os.Getenv("CLUSTER_ID")
	if clusterID == "" {
		log.Fatalln("CLUSTER_ID env required but not present")
		return
	}
	_, err := getClusterPlan(clusterID)
	if err != nil {
		log.Fatalln("Unable to fetch the cluster plan. Please make sure that you're using a valid CLUSTER_ID. If the issue persists please contact support@appbase.io with your ARC_ID or registered e-mail address.", err)
		return
	}
}

func reportUsageRequest(arcUsage ArcUsage) (ArcUsageResponse, error) {
	response := ArcUsageResponse{}
	url := ACCAPI + "arc/report_usage"
	marshalledRequest, err := json.Marshal(arcUsage)
	log.Println("Arc usage for Arc ID: ", arcUsage)
	if err != nil {
		log.Println("error while marshalling req body: ", err)
		return response, err
	}
	req, _ := http.NewRequest(http.MethodPut, url, bytes.NewBuffer(marshalledRequest))
	req.Header.Add("Content-Type", "application/json")
	req.Header.Add("cache-control", "no-cache")

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Println("error while sending request: ", err)
		return response, err
	}
	defer res.Body.Close()
	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		log.Println("error reading res body: ", err)
		return response, err
	}
	err = json.Unmarshal(body, &response)

	if err != nil {
		log.Println("error while unmarshalling res body: ", err)
		return response, err
	}
	return response, nil
}

func reportClusterUsageRequest(arcUsage ArcUsage) (ArcUsageResponse, error) {
	response := ArcUsageResponse{}
	url := ACCAPI + "byoc/report_usage"
	marshalledRequest, err := json.Marshal(arcUsage)
	log.Println("Arc usage for Cluster ID: ", arcUsage)
	if err != nil {
		log.Println("error while marshalling req body: ", err)
		return response, err
	}
	req, _ := http.NewRequest(http.MethodPut, url, bytes.NewBuffer(marshalledRequest))
	req.Header.Add("Content-Type", "application/json")
	req.Header.Add("cache-control", "no-cache")

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Println("error while sending request: ", err)
		return response, err
	}
	defer res.Body.Close()
	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		log.Println("error reading res body: ", err)
		return response, err
	}
	err = json.Unmarshal(body, &response)

	if err != nil {
		log.Println("error while unmarshalling res body: ", err)
		return response, err
	}
	return response, nil
}

// ReportUsage reports Arc usage, intended to be called every hour
func ReportUsage() {
	url := os.Getenv("ES_CLUSTER_URL")
	if url == "" {
		log.Fatalln("ES_CLUSTER_URL env required but not present")
		return
	}
	arcID := os.Getenv("ARC_ID")
	if arcID == "" {
		log.Fatalln("ARC_ID env required but not present")
		return
	}

	result, err := getArcInstance(arcID)
	if err != nil {
		log.Fatalln("Unable to fetch the arc instance. Please make sure that you're using a valid ARC_ID. If the issue persists please contact support@appbase.io with your ARC_ID or registered e-mail address.")
		return
	}

	NodeCount, err = fetchNodeCount(url)
	if err != nil || NodeCount <= 0 {
		log.Println("Unable to fetch a correct node count: ", err)
	}

	subID := result.SubscriptionID
	if subID == "" {
		log.Println("SUBSCRIPTION_ID not found. Initializing in trial mode")
		return
	}

	usageBody := ArcUsage{}
	usageBody.ArcID = arcID
	usageBody.SubscriptionID = subID
	usageBody.Quantity = NodeCount
	response, err1 := reportUsageRequest(usageBody)
	if err1 != nil {
		log.Println("Please contact support@appbase.io with your ARC_ID or registered e-mail address. Usage is not getting reported: ", err1)
	}

	if response.WarningMsg != "" {
		log.Println("warning:", response.WarningMsg)
	}
	if response.ErrorMsg != "" {
		log.Println("error:", response.ErrorMsg)
	}
}

// ReportHostedArcUsage reports Arc usage by hosted cluster, intended to be called every hour
func ReportHostedArcUsage() {
	log.Printf("=> Reporting hosted arc usage")
	url := os.Getenv("ES_CLUSTER_URL")
	if url == "" {
		log.Fatalln("ES_CLUSTER_URL env required but not present")
		return
	}
	clusterID := os.Getenv("CLUSTER_ID")
	if clusterID == "" {
		log.Fatalln("CLUSTER_ID env required but not present")
		return
	}

	// getArcClusterInstance(clusterId)
	result, err := getArcClusterInstance(clusterID)
	if err != nil {
		log.Fatalln("Unable to fetch the arc instance. Please make sure that you're using a valid CLUSTER_ID. If the issue persists please contact support@appbase.io with your ARC_ID or registered e-mail address.", err)
		return
	}

	NodeCount, err = fetchNodeCount(url)
	if err != nil || NodeCount <= 0 {
		log.Println("Unable to fetch a correct node count: ", err)
	}

	subID := result.SubscriptionID
	if subID == "" {
		log.Println("SUBSCRIPTION_ID not found. Initializing in trial mode")
		return
	}

	usageBody := ArcUsage{}
	usageBody.ClusterID = clusterID
	usageBody.SubscriptionID = subID
	usageBody.Quantity = NodeCount
	response, err1 := reportClusterUsageRequest(usageBody)
	if err1 != nil {
		log.Println("Please contact support@appbase.io with your CLUSTER_ID or registered e-mail address. Usage is not getting reported: ", err1)
	}

	if response.WarningMsg != "" {
		log.Println("warning:", response.WarningMsg)
	}
	if response.ErrorMsg != "" {
		log.Println("error:", response.ErrorMsg)
	}
}

// fetchNodeCount returns the number of current ElasticSearch nodes
func fetchNodeCount(url string) (int, error) {
	nodes, err := GetTotalNodes()
	if err != nil {
		return 0, err
	}
	return nodes, nil
}

package management

import (
	"encoding/json"
	"fmt"
	"github.com/dimfeld/httptreemux/v5"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/scrapers/azure/logger"
	"github.com/google/uuid"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

var client *AzureManagementClient

func TestMain(m *testing.M) {
	router := httptreemux.NewContextMux()
	handler := http.NewServeMux()
	group := router.NewGroup("/subscriptions/:subscriptionid")
	group.GET("/providers/Microsoft.ContainerService/managedClusters", listKubernetesClusters)
	group.GET("/providers/Microsoft.ContainerRegistry/registries", listContainerRegistries)
	group.GET("/resourcegroups/:resourcegroup/providers/Microsoft.Compute/virtualMachines", listVirtualMachines)
	group.GET("/resourceGroups/:resourcegroup/providers/Microsoft.Network/loadBalancers", listLoadBalancers)
	group.GET("/resourcegroups", listResourceGroups)
	group.GET("/providers/Microsoft.Network/virtualNetworks", listVirtualNetworks)
	group.GET("/providers/Microsoft.Network/azureFirewalls", listFirewalls)
	group.GET("/resources?$filter=:filter/servers/databases", listDatabases)
	handler.HandleFunc("/v2.0/.well-known/openid-configuration", token)

	srv := httptest.NewServer(router)

	// =========================================================================
	// Build the logger.

	log, err := logger.New("AZURE-MANAGEMENT-SCRAPER-TEST")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	// =========================================================================
	// Create client with random subscription Id.

	uniqueId, _ := uuid.NewUUID()

	_ = os.Setenv("AZURE_CLIENT_ID", uniqueId.String())
	_ = os.Setenv("AZURE_CLIENT_SECRET", uniqueId.String())
	_ = os.Setenv("AZURE_TENANT_ID", uniqueId.String())

	client, _ = NewAzureManagementClient(nil, log, uniqueId.String(), srv.URL)
	client.Client.SetAuthToken(uniqueId.String())
	client.Client.SetBaseURL(fmt.Sprintf("%s/subscriptions/%s", srv.URL, uniqueId.String()))

	m.Run()
}

func listResourceGroups(w http.ResponseWriter, r *http.Request) {
	randomId, _ := uuid.NewUUID()
	resourceGroup := v1.ResourceGroupListResult{Value: []*v1.ResourceGroup{
		{
			Location:   "flanksource_a",
			Properties: v1.ResourceProperty{ProvisioningState: "Succeeded"},
			ID:         randomId.String(),
			Name:       "flanksource_resource_group_a",
			Type:       "Resource Group",
			ManagedBy:  "flanksource_a_user",
		},
		{
			Location:   "flanksource_b",
			Properties: v1.ResourceProperty{ProvisioningState: "Succeeded"},
			ID:         randomId.String(),
			Name:       "flanksource_resource_group_b",
			Type:       "Resource Group",
			ManagedBy:  "flanksource_b_user",
		},
	}}
	res, er := json.Marshal(resourceGroup)
	if er != nil {
		client.Log.Fatal(er)
	}
	_, _ = w.Write(res)
}

func listKubernetesClusters(w http.ResponseWriter, r *http.Request) {
	randomId, _ := uuid.NewUUID()

	k8s := v1.KubernetesClusterListResult{Value: []*v1.KubernetesCluster{
		{
			Location:   "flanksource_a",
			Properties: v1.ResourceProperty{},
			Tags:       nil,
			ID:         randomId.String(),
			Name:       "flanksource_kubernetes_cluster_a",
			Type:       "Kubernetes cluster",
		},
		{
			Location:   "flanksource_b",
			Properties: v1.ResourceProperty{},
			Tags:       nil,
			ID:         randomId.String(),
			Name:       "flanksource_kubernetes_cluster_b",
			Type:       "Kubernetes cluster",
		},
	}}
	res, er := json.Marshal(k8s)
	if er != nil {
		client.Log.Fatal(er)
	}
	_, _ = w.Write(res)
}

func listContainerRegistries(w http.ResponseWriter, r *http.Request) {
	randomId, _ := uuid.NewUUID()

	acs := v1.ContainerRegistryListResult{Value: []*v1.ContainerRegistry{
		{
			Location:   "flanksource_a",
			Properties: v1.ResourceProperty{},
			Tags:       nil,
			ID:         randomId.String(),
			Name:       "flanksource_container_registry_a",
			Type:       "Container Registry",
		},
		{
			Location:   "flanksource_b",
			Properties: v1.ResourceProperty{},
			Tags:       nil,
			ID:         randomId.String(),
			Name:       "flanksource_container_registry_b",
			Type:       "Container Registry",
		},
	}}
	res, er := json.Marshal(acs)
	if er != nil {
		client.Log.Fatal(er)
	}
	_, _ = w.Write(res)
}

func listVirtualMachines(w http.ResponseWriter, r *http.Request) {
	randomId, _ := uuid.NewUUID()

	acs := v1.VirtualMachineListResult{Value: []*v1.VirtualMachine{
		{
			Location: "flanksource_a",
			ID:       randomId.String(),
			Name:     "flanksource_virtual_machine_a",
			Type:     "Virtual Machine",
		},
		{
			Location: "flanksource_b",
			ID:       randomId.String(),
			Name:     "flanksource_virtual_machine_b",
			Type:     "Virtual Machine",
		},
	}}
	res, er := json.Marshal(acs)
	if er != nil {
		client.Log.Fatal(er)
	}
	_, _ = w.Write(res)
}

func listLoadBalancers(w http.ResponseWriter, r *http.Request) {
	randomId, _ := uuid.NewUUID()

	acs := v1.LoadBalancerListResult{Value: []*v1.LoadBalancer{
		{
			Location: "flanksource_a",
			ID:       randomId.String(),
			Name:     "flanksource_load_balancer_a",
			Type:     "Load Balancer",
		},
		{
			Location: "flanksource_b",
			ID:       randomId.String(),
			Name:     "flanksource_load_balancer_b",
			Type:     "Load Balancer",
		},
	}}
	res, er := json.Marshal(acs)
	if er != nil {
		client.Log.Fatal(er)
	}
	_, _ = w.Write(res)
}

func listVirtualNetworks(w http.ResponseWriter, r *http.Request) {
	randomId, _ := uuid.NewUUID()

	acs := v1.VirtualNetworkListResult{Value: []*v1.VirtualNetwork{
		{
			Location: "flanksource_a",
			ID:       randomId.String(),
			Name:     "flanksource_virtual_network_a",
			Type:     "Virtual Network",
		},
		{
			Location: "flanksource_b",
			ID:       randomId.String(),
			Name:     "flanksource_virtual_network_b",
			Type:     "Virtual Network",
		},
	}}
	res, er := json.Marshal(acs)
	if er != nil {
		client.Log.Fatal(er)
	}
	_, _ = w.Write(res)
}
func listFirewalls(w http.ResponseWriter, r *http.Request) {
	randomId, _ := uuid.NewUUID()

	acs := v1.VirtualNetworkListResult{Value: []*v1.VirtualNetwork{
		{
			Location: "flanksource_a",
			ID:       randomId.String(),
			Name:     "flanksource_firewall_a",
			Type:     "Firewall",
		},
		{
			Location: "flanksource_b",
			ID:       randomId.String(),
			Name:     "flanksource_firewall_b",
			Type:     "Firewall",
		},
	}}
	res, er := json.Marshal(acs)
	if er != nil {
		client.Log.Fatal(er)
	}
	_, _ = w.Write(res)
}
func listDatabases(w http.ResponseWriter, r *http.Request) {
	randomId, _ := uuid.NewUUID()

	acs := v1.DatabaseListResult{Value: []*v1.Database{
		{
			Location: "flanksource_a",
			ID:       randomId.String(),
			Name:     "flanksource_database_a",
			Type:     "Database",
		},
		{
			Location: "flanksource_b",
			ID:       randomId.String(),
			Name:     "flanksource_database_b",
			Type:     "Database",
		},
	}}
	res, er := json.Marshal(acs)
	if er != nil {
		client.Log.Fatal(er)
	}
	_, _ = w.Write(res)
}

func token(w http.ResponseWriter, r *http.Request) {
	fmt.Println(r)
}
func myHandlerFunc(w http.ResponseWriter, r *http.Request) {
	w.Write([]byte(`hello world`))
}

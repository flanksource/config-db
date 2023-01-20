package v1

import (
	"github.com/flanksource/kommons"
	"github.com/go-resty/resty/v2"
	"go.uber.org/zap"
)

type AzureDevops struct {
	BaseScraper         `json:",inline"`
	Organization        string         `yaml:"organization" json:"organization"`
	PersonalAccessToken kommons.EnvVar `yaml:"personalAccessToken" json:"personalAccessToken"`
	Projects            []string       `yaml:"projects" json:"projects"`
	Pipelines           []string       `yaml:"pipelines" json:"pipelines"`
}
type AzureManagement struct {
	BaseScraper        `json:",inline"`
	SubscriptionId     string              `yaml:"subscriptionId" json:"subscriptionId"`
	ResourceGroups     []ResourceGroup     `json:"resourceGroups,omitempty"`
	VirtualMachines    []VirtualMachine    `json:"virtualMachines,omitempty"`
	KubernetesClusters []KubernetesCluster `json:"kubernetesClusters,omitempty"`
	ContainerRegistry  []ContainerRegistry `json:"containerRegistry,omitempty"`
	VirtualNetwork     []VirtualNetwork    `json:"VirtualNetwork,omitempty"`
	Firewall           []Firewall          `json:"firewall,omitempty"`
	LoadBalancer       []LoadBalancer      `json:"loadBalancer,omitempty"`
	Databases          []Database          `json:"databases,omitempty"`
}

// =========================================================================
// Network Profile.

type ManagedOutboundIPs struct {
	Count int `json:"count,omitempty"`
}
type EffectiveOutboundIP struct {
	Id string `json:"id,omitempty"`
}
type LoadBalancerProfile struct {
	ManagedOutboundIPs   ManagedOutboundIPs    `json:"managedOutboundIPs,omitempty"`
	EffectiveOutboundIPS []EffectiveOutboundIP `json:"effectiveOutboundIPs,omitempty"`
}

type NetworkInterfaceProperty struct {
	DeleteOption string `json:"deleteOption,omitempty"`
}

type NetworkInterface struct {
	Id                       string                   `json:"id,omitempty"`
	NetworkInterfaceProperty NetworkInterfaceProperty `json:"properties,omitempty"`
}

type NetworkProfile struct {
	NetworkPlugin       string              `json:"networkPlugin,omitempty"`
	LoadBalancerSku     string              `json:"loadBalancerSku,omitempty"`
	LoadBalancerProfile LoadBalancerProfile `json:"loadBalancerProfile,omitempty"`
	PodCidr             string              `json:"podCidr,omitempty"`
	ServiceCidr         string              `json:"serviceCidr,omitempty"`
	DnsServiceIP        string              `json:"dnsServiceIP,omitempty"`
	DockerBridgeCidr    string              `json:"dockerBridgeCidr,omitempty"`
	OutboundType        string              `json:"outboundType,omitempty"`
	PodCidrs            []string            `json:"podCidrs,omitempty"`
	ServiceCidrs        []string            `json:"serviceCidrs,omitempty"`
	IpFamilies          []string            `json:"ipFamilies,omitempty"`
	NetworkInterface    []NetworkInterface  `json:"networkInterfaces,omitempty"`
}

// =========================================================================
// Addon Profile.

type Identity struct {
	Type        string `json:"type,omitempty"`
	ResourceId  string `json:"resourceId,omitempty"`
	ClientId    string `json:"clientId,omitempty"`
	ObjectId    string `json:"objectId,omitempty"`
	PrincipalId string `json:"principalId,omitempty"`
	TenantId    string `json:"tenantId,omitempty"`
}

type OmsAgentConfig struct {
	LogAnalyticsWorkspaceResourceID string `json:"logAnalyticsWorkspaceResourceID,omitempty"`
}

type AzureKeyvaultSecretsProvider struct {
	Enabled bool   `json:"enabled,omitempty"`
	Config  string `json:"config,omitempty"`
}

type Azurepolicy struct {
	Enabled bool   `json:"enabled,omitempty"`
	Config  string `json:"config,omitempty"`
}

type HttpApplicationRouting struct {
	Enabled bool   `json:"enabled,omitempty"`
	Config  string `json:"config,omitempty"`
}

type OmsAgent struct {
	Enabled  bool           `json:"enabled,omitempty"`
	Config   OmsAgentConfig `json:"config,omitempty"`
	Identity Identity       `json:"identity,omitempty"`
}

type AddOnProfile struct {
	AzureKeyvaultSecretsProvider AzureKeyvaultSecretsProvider `json:"azureKeyvaultSecretsProvider,omitempty"`
	AzurePolicy                  Azurepolicy                  `json:"azurepolicy,omitempty"`
	HttpApplicationRouting       HttpApplicationRouting       `json:"httpApplicationRouting,omitempty"`
	OmsAgent                     OmsAgent                     `json:"omsAgent,omitempty"`
}

// =========================================================================
// AgentPool Profile.

type ServicePrincipalProfile struct {
	ClientId string `json:"clientId,omitempty"`
}

type PowerState struct {
	Code string `json:"code,omitempty"`
}

type AgentPoolProfile struct {
	Name                       string     `json:"name,omitempty"`
	Count                      int        `json:"count,omitempty"`
	VmSize                     string     `json:"vmSize,omitempty"`
	OsDiskSizeGB               int        `json:"osDiskSizeGB,omitempty"`
	OsDiskType                 string     `json:"osDiskType,omitempty"`
	KubeletDiskType            string     `json:"kubeletDiskType,omitempty"`
	MaxPods                    string     `json:"maxPods,omitempty"`
	Type                       string     `json:"type,omitempty"`
	AvailabilityZones          []string   `json:"availabilityZones,omitempty"`
	MaxCount                   int        `json:"maxCount,omitempty"`
	MinCount                   int        `json:"minCount,omitempty"`
	EnableAutoScaling          bool       `json:"enableAutoScaling,omitempty"`
	ProvisioningState          bool       `json:"provisioningState,omitempty"`
	PowerState                 PowerState `json:"powerState,omitempty"`
	OrchestratorVersion        string     `json:"orchestratorVersion,omitempty"`
	CurrentOrchestratorVersion string     `json:"currentOrchestratorVersion,omitempty"`
	EnableNodePublicIP         bool       `json:"enableNodePublicIP,omitempty"`
	Mode                       string     `json:"mode,omitempty"`
	OsType                     string     `json:"osType,omitempty"`
	OsSKU                      string     `json:"osSKU,omitempty"`
	NodeImageVersion           string     `json:"nodeImageVersion,omitempty"`
	EnableFIPS                 bool       `json:"enableFIPS,omitempty"`
}

// =========================================================================
// Resource Properties.

type IdentityProfile struct {
	Kubeletidentity Kubeletidentity `json:"kubeletidentity,omitempty"`
}

type Kubeletidentity struct {
	ResourceId string `json:"resourceId,omitempty"`
	ClientId   string `json:"clientId,omitempty"`
	ObjectId   string `json:"objectId,omitempty"`
}

// =========================================================================
// Auto upgrade Profile.

type AutoUpgradeProfile struct {
	UpgradeChannel string `json:"upgradeChannel,omitempty"`
}

// =========================================================================
// Storage profile.

type DiskCSIDriver struct {
	Enabled bool `json:"enabled,omitempty"`
}

type FileCSIDriver struct {
	Enabled bool `json:"enabled,omitempty"`
}

type SnapshotController struct {
	Enabled bool `json:"enabled,omitempty"`
}

type OidcIssuerProfile struct {
	Enabled bool `json:"enabled,omitempty"`
}

type ImageReference struct {
	Publisher    string `json:"publisher,omitempty"`
	Offer        string `json:"offer,omitempty"`
	Sku          string `json:"sku,omitempty"`
	Version      string `json:"version,omitempty"`
	ExactVersion string `json:"exactVersion,omitempty"`
}

type ManagedDisk struct {
	Id string `json:"id,omitempty"`
}

type OsDisk struct {
	OsType       string      `json:"osType,omitempty"`
	Name         string      `json:"name,omitempty"`
	CreateOption string      `json:"createOption,omitempty"`
	ManagedDisk  ManagedDisk `json:"managedDisk,omitempty"`
	DeleteOption string      `json:"deleteOption,omitempty"`
}

type StorageProfile struct {
	DiskCSIDriver      DiskCSIDriver      `json:"diskCSIDriver,omitempty"`
	FileCSIDriver      FileCSIDriver      `json:"fileCSIDriver,omitempty"`
	SnapshotController SnapshotController `json:"snapshotController,omitempty"`
	ImageReference     ImageReference     `json:"imageReference,omitempty"`
	OsDisk             OsDisk             `json:"osDisk,omitempty"`
	DataDisks          []interface{}      `json:"dataDisks,omitempty"`
}

// =========================================================================
// OS profile.

type PublicKey struct {
	Path    string `json:"path,omitempty"`
	KeyData string `json:"keyData,omitempty"`
}

type PatchSetting struct {
	PatchMode      string `json:"patchMode,omitempty"`
	AssessmentMode string `json:"assessmentMode,omitempty"`
}

type Ssh struct {
	PublicKeys []PublicKey `json:"publicKeys,omitempty"`
}

type LinuxConfiguration struct {
	DisablePasswordAuthentication bool         `json:"disablePasswordAuthentication,omitempty"`
	Ssh                           Ssh          `json:"ssh,omitempty"`
	ProvisionVMAgent              bool         `json:"provisionVMAgent,omitempty"`
	PatchSettings                 PatchSetting `json:"patchSettings,omitempty"`
	EnableVMAgentPlatformUpdates  bool         `json:"enableVMAgentPlatformUpdates,omitempty"`
}

type OsProfile struct {
	ComputerName                string             `json:"computerName,omitempty"`
	AdminUsername               string             `json:"adminUsername,omitempty"`
	LinuxConfiguration          LinuxConfiguration `json:"linuxConfiguration,omitempty"`
	Secrets                     []interface{}      `json:"secrets,omitempty"`
	AllowExtensionOperations    bool               `json:"allowExtensionOperations,omitempty"`
	RequireGuestProvisionSignal bool               `json:"requireGuestProvisionSignal,omitempty"`
}

// =========================================================================
// Diagnostics profile.

type DiagnosticsProfile struct {
	BootDiagnostics BootDiagnostics `json:"bootDiagnostics,omitempty"`
}

type BootDiagnostics struct {
	Enabled bool `json:"enabled,omitempty"`
}

// =========================================================================
// Security Profile.

type SecurityProfile struct{}

// =========================================================================
// Workload AutoScaler Profile.

type WorkloadAutoScalerProfile struct{}

// =========================================================================
// Hardware profile.

type HardwareProfile struct {
	Name   string `json:"name,omitempty"`
	Tier   string `json:"tier,omitempty"`
	VmSize string `json:"vmSize,omitempty"`
}

// =========================================================================
// Policies.

type QuarantinePolicy struct {
	Status bool `json:"status,omitempty"`
}

type TrustPolicy struct {
	Type   bool `json:"type,omitempty"`
	Status bool `json:"status,omitempty"`
}

type RetentionPolicy struct {
	Days            string `json:"days,omitempty"`
	LastUpdatedTime string `json:"lastUpdatedTime,omitempty"`
	Status          bool   `json:"status,omitempty"`
}

type Policy struct {
	QuarantinePolicy QuarantinePolicy `json:"quarantinePolicy,omitempty"`
	TrustPolicy      TrustPolicy      `json:"trustPolicy,omitempty"`
	RetentionPolicy  RetentionPolicy  `json:"retentionPolicy,omitempty"`
}

// =========================================================================
// Subnets.

type NetworkSecurityGroup struct {
	Id string `json:"id,omitempty"`
}

type RouteTable struct {
	Id string `json:"id,omitempty"`
}

// =========================================================================
// IPConfiguration.

type PublicIPAddress struct {
	Id string `json:"id,omitempty"`
}
type IpConfigurationProperty struct {
	ProvisionState            string          `json:"provisionState,omitempty"`
	PrivateIPAllocationMethod string          `json:"privateIPAllocationMethod,omitempty"`
	PublicIPAddress           PublicIPAddress `json:"publicIPAddress,omitempty"`
	Subnet                    Subnet          `json:"subnet,omitempty"`
}

type IpConfiguration struct {
	Id                      string                    `json:"id,omitempty"`
	Name                    string                    `json:"name,omitempty"`
	Etag                    string                    `json:"etag,omitempty"`
	Type                    string                    `json:"type,omitempty"`
	IpConfigurationProperty []IpConfigurationProperty `json:"ipConfigurations,omitempty"`
}

type SubnetProperty struct {
	ProvisioningState                 string               `json:"provisioningState,omitempty"`
	AddressPrefix                     string               `json:"addressPrefix,omitempty"`
	NetworkSecurityGroup              NetworkSecurityGroup `json:"networkSecurityGroup,omitempty"`
	RouteTable                        RouteTable           `json:"routeTable,omitempty"`
	IpConfigurations                  []IpConfiguration    `json:"ipConfigurations,omitempty"`
	Delegations                       []interface{}        `json:"delegations,omitempty"`
	PrivateEndpointNetworkPolicies    string               `json:"privateEndpointNetworkPolicies,omitempty"`
	PrivateLinkServiceNetworkPolicies string               `json:"privateLinkServiceNetworkPolicies,omitempty"`
}

type Subnet struct {
	Name           string         `json:"name,omitempty"`
	Id             string         `json:"id,omitempty"`
	Etag           string         `json:"etag,omitempty"`
	Type           string         `json:"type,omitempty"`
	SubnetProperty SubnetProperty `json:"properties,omitempty"`
}

// =========================================================================
// Address space.

type AddressSpace struct {
	AddressPrefixes []string `json:"addressPrefixes,omitempty"`
}

// =========================================================================
// Resource property.

type ResourceIdentity struct {
	Type        string `json:"type,omitempty"`
	PrincipalId string `json:"principalId,omitempty"`
	TenantId    string `json:"tenantId,omitempty"`
}

type Sku struct {
	Name     string `json:"name,omitempty"`
	Tier     string `json:"tier,omitempty"`
	Capacity int    `json:"capacity"`
}

// =========================================================================
// IP Configuration.

type FrontendIPConfigurationProperties struct {
	ProvisioningState         string `json:"provisioningState,omitempty"`
	PrivateIPAddress          string `json:"privateIPAddress,omitempty"`
	PrivateIPAllocationMethod string `json:"privateIPAllocationMethod,omitempty"`
	Subnet                    string `json:"subnet,omitempty"`
	PrivateIPAddressVersion   string `json:"privateIPAddressVersion,omitempty"`
}

type FrontendIPConfiguration struct {
	Name                              string                            `json:"name,omitempty"`
	Id                                string                            `json:"id,omitempty"`
	Etag                              string                            `json:"etag,omitempty"`
	Type                              string                            `json:"type,omitempty"`
	FrontendIPConfigurationProperties FrontendIPConfigurationProperties `json:"properties,omitempty"`
	Zones                             []string                          `json:"zones,omitempty"`
}

type AdditionalProperties struct{}

type FirewallPolicy struct {
	Id string `json:"id,omitempty"`
}

type ResourceProperty struct {
	ResourceGuid              string                    `json:"resourceGuid,omitempty"`
	LoginServer               string                    `json:"loginServer,omitempty"`
	CreationDate              string                    `json:"creationDate,omitempty"`
	AddressSpace              AddressSpace              `json:"addressSpace,omitempty"`
	AdminUserEnabled          bool                      `json:"adminUserEnabled,omitempty"`
	VmId                      string                    `json:"vmId,omitempty"`
	TimeCreated               string                    `json:"timeCreated,omitempty"`
	ProvisioningState         string                    `json:"provisioningState,omitempty"`
	PowerState                PowerState                `json:"powerState,omitempty"`
	KubernetesVersion         string                    `json:"kubernetesVersion,omitempty"`
	CurrentKubernetesVersion  string                    `json:"currentKubernetesVersion,omitempty"`
	DnsPrefix                 string                    `json:"dnsPrefix,omitempty"`
	Fqdn                      string                    `json:"fqdn,omitempty"`
	AzurePortalFQDN           string                    `json:"azurePortalFQDN,omitempty"`
	NodeResourceGroup         string                    `json:"nodeResourceGroup,omitempty"`
	EnableRBAC                bool                      `json:"enableRBAC,omitempty"`
	MaxAgentPools             int                       `json:"maxAgentPools,omitempty"`
	IdentityProfile           IdentityProfile           `json:"identityProfile,omitempty"`
	NetworkProfile            NetworkProfile            `json:"networkProfile,omitempty"`
	AutoUpgradeProfile        AutoUpgradeProfile        `json:"autoUpgradeProfile,omitempty"`
	SecurityProfile           SecurityProfile           `json:"securityProfile,omitempty"`
	StorageProfile            StorageProfile            `json:"storageProfile,omitempty"`
	OsProfile                 OsProfile                 `json:"osProfile,omitempty"`
	OidcIssuerProfile         OidcIssuerProfile         `json:"oidcIssuerProfile,omitempty"`
	WorkloadAutoScalerProfile WorkloadAutoScalerProfile `json:"workloadAutoScalerProfile,omitempty"`
	DiagnosticsProfile        DiagnosticsProfile        `json:"diagnosticsProfile,omitempty"`
	AgentPoolProfiles         []AgentPoolProfile        `json:"agentPoolProfiles,omitempty"`
	ServicePrincipalProfile   ServicePrincipalProfile   `json:"servicePrincipalProfile,omitempty"`
	AddOnProfile              AddOnProfile              `json:"addonProfiles,omitempty"`
	HardwareProfile           HardwareProfile           `json:"hardwareProfile,omitempty"`
	DisableLocalAccounts      string                    `json:"disableLocalAccounts,omitempty"`
	ResourceIdentity          ResourceIdentity          `json:"identity,omitempty"`
	Sku                       Sku                       `json:"sku,omitempty"`

	// Database
	Kind      string `json:"kind,omitempty"`
	ManagedBy string `json:"managedBy,omitempty"`

	// Firewall
	ThreatIntelMode        string               `json:"threatIntelMode,omitempty"`
	AdditionalProperties   AdditionalProperties `json:"additionalProperties,omitempty"`
	IpConfigurations       []IpConfiguration    `json:"ipConfigurations,omitempty"`
	NetworkRuleCollections []interface{}        `json:"networkRuleCollections,omitempty"`
	NatRuleCollections     []interface{}        `json:"natRuleCollections,omitempty"`
	FirewallPolicy         FirewallPolicy       `json:"firewallPolicy,omitempty"`

	// ACR
	Policy Policy `json:"policies,omitempty"`

	// Virtual Machine
	Subnets                []Subnet      `json:"subnets,omitempty"`
	VirtualNetworkPeerings []interface{} `json:"virtualNetworkPeerings,omitempty"`
	EnableDdosProtection   bool          `json:"enableDdosProtection,omitempty"`

	// Load balancer configurations.
	FrontendIPConfiguration []FrontendIPConfiguration `json:"frontendIPConfigurations"`
	BackendAddressPools     []interface{}             `json:"backendAddressPools,omitempty"`
	LoadBalancingRules      []interface{}             `json:"loadBalancingRules,omitempty"`
	Probes                  []interface{}             `json:"probes,omitempty"`
	InboundNatRules         []interface{}             `json:"inboundNatRules,omitempty"`
	OutboundRules           []interface{}             `json:"outboundRules,omitempty"`
	InboundNatPools         []interface{}             `json:"inboundNatPools,omitempty"`
}

// ResourceGroupProperties - The resource group properties.
type ResourceGroupProperties struct {
	ProvisioningState *string `json:"provisioningState,omitempty"`
}

// Database - Database information.
type Database struct {
	Location  string            `json:"location,omitempty"`
	ManagedBy string            `json:"managedBy,omitempty"`
	Tags      map[string]string `json:"tags,omitempty"`
	ID        string            `json:"id,omitempty"`
	Name      string            `json:"name,omitempty"`
	Type      string            `json:"type,omitempty"`
	SKU       Sku               `json:"sku,omitempty"`
	Kind      string            `json:"kind,omitempty"`
}

// VirtualNetwork - Virtual network information.
type VirtualNetwork struct {
	Location   string            `json:"location,omitempty"`
	Properties ResourceProperty  `json:"properties,omitempty"`
	Tags       map[string]string `json:"tags,omitempty"`
	ID         string            `json:"id,omitempty"`
	Name       string            `json:"name,omitempty"`
	Type       string            `json:"type,omitempty"`
	Etag       string            `json:"etag,omitempty"`
}

// VirtualMachine - Virtual machine information.
type VirtualMachine struct {
	Location   string           `json:"location,omitempty"`
	Properties ResourceProperty `json:"properties,omitempty"`
	ID         string           `json:"id,omitempty"`
	Name       string           `json:"name,omitempty"`
	Type       string           `json:"type,omitempty"`
}

// LoadBalancer - LoadBalancer information.
type LoadBalancer struct {
	Location   string            `json:"location,omitempty"`
	Properties ResourceProperty  `json:"properties,omitempty"`
	Tags       map[string]string `json:"tags,omitempty"`
	ID         string            `json:"id,omitempty"`
	Name       string            `json:"name,omitempty"`
	Type       string            `json:"type,omitempty"`
	Etag       string            `json:"etag,omitempty"`
	SKU        Sku               `json:"sku,omitempty"`
}

// Firewall - Firewall information.
type Firewall struct {
	Location   string            `json:"location,omitempty"`
	Properties ResourceProperty  `json:"properties,omitempty"`
	Tags       map[string]string `json:"tags,omitempty"`
	ID         string            `json:"id,omitempty"`
	Name       string            `json:"name,omitempty"`
	Type       string            `json:"type,omitempty"`
	Etag       string            `json:"etag,omitempty"`
}

// ContainerRegistry - Container registration information.
type ContainerRegistry struct {
	SKU        Sku               `json:"sku,omitempty"`
	Location   string            `json:"location,omitempty"`
	Properties ResourceProperty  `json:"properties,omitempty"`
	Tags       map[string]string `json:"tags,omitempty"`
	ID         string            `json:"id,omitempty"`
	Name       string            `json:"name,omitempty"`
	Type       string            `json:"type,omitempty"`
}

// KubernetesCluster - Kubernetes cluster information.
type KubernetesCluster struct {
	Location   string            `json:"location,omitempty"`
	Properties ResourceProperty  `json:"properties,omitempty"`
	Tags       map[string]string `json:"tags,omitempty"`
	ID         string            `json:"id,omitempty"`
	Name       string            `json:"name,omitempty"`
	Type       string            `json:"type,omitempty"`
	Identity   Identity          `json:"identity,omitempty"`
	SKU        Sku               `json:"sku,omitempty"`
}

// ResourceGroup - Resource group information.
type ResourceGroup struct {
	Location   string            `json:"location,omitempty"`
	ManagedBy  string            `json:"managedBy,omitempty"`
	Properties ResourceProperty  `json:"properties,omitempty"`
	Tags       map[string]string `json:"tags,omitempty"`
	ID         string            `json:"id,omitempty"`
	Name       string            `json:"name,omitempty"`
	Type       string            `json:"type,omitempty"`
}

// ResourceGroupListResult - List of resource groups.
type ResourceGroupListResult struct {
	// An array of resource groups.
	Value []*ResourceGroup `json:"value,omitempty"`
}

// VirtualMachineListResult - List of virtual machines.
type VirtualMachineListResult struct {
	// An array of virtual machines.
	Value []*VirtualMachine `json:"value,omitempty"`
}

// VirtualNetworkListResult - List of virtual networks.
type VirtualNetworkListResult struct {
	// An array of virtual network.
	Value []*VirtualNetwork `json:"value,omitempty"`
}

// FirewallListResult - List of firewalls.
type FirewallListResult struct {
	// An array of firewall.
	Value []*Firewall `json:"value,omitempty"`
}

// KubernetesClusterListResult - List of kubernetes clusters.
type KubernetesClusterListResult struct {
	// An array of kubernetes clusters.
	Value []*KubernetesCluster `json:"value,omitempty"`
}

// LoadBalancerListResult - List of load balancers.
type LoadBalancerListResult struct {
	// An array of load balancers.
	Value []*LoadBalancer `json:"value,omitempty"`
}

// ContainerRegistryListResult - List of container registries.
type ContainerRegistryListResult struct {
	// An array of container registries.
	Value []*ContainerRegistry `json:"value,omitempty"`
}

// DatabaseListResult - List of databases.
type DatabaseListResult struct {
	// An array of databases.
	Value []*Database `json:"value,omitempty"`
}

type AzureManagementClient struct {
	Organisation string
	Log          *zap.SugaredLogger
	*resty.Client
	*ScrapeContext
}

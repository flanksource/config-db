package aws

import (
	"strings"
	"time"

	configservice "github.com/aws/aws-sdk-go-v2/service/configservice/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	ssmTypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	supportTypes "github.com/aws/aws-sdk-go-v2/service/support/types"
	v1 "github.com/flanksource/config-db/api/v1"
)

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func makeMap(m map[string]string) map[string]string {
	if m == nil {
		return make(map[string]string)
	}
	return m
}

// NewENI ...
func NewENI(b types.InstanceNetworkInterface) ENI {
	a := ENI{}
	if b.Attachment != nil {
		a.AttachmentID = *b.Attachment.AttachmentId
		a.AttachTime = b.Attachment.AttachTime
		a.AttachmentStatus = string(b.Attachment.Status)
		a.DeleteOnTermination = *b.Attachment.DeleteOnTermination
		if b.Attachment.DeviceIndex != nil {
			a.DeviceIndex = *b.Attachment.DeviceIndex
		}
		if b.Attachment.NetworkCardIndex != nil {
			a.NetworkCardIndex = *b.Attachment.NetworkCardIndex
		}
	}
	a.Description = deref(b.Description)
	for _, sg := range b.Groups {
		a.Groups = append(a.Groups, *sg.GroupId)
	}
	for _, prefix := range b.Ipv4Prefixes {
		a.Ipv4Prefixes = append(a.Ipv4Prefixes, deref(prefix.Ipv4Prefix))
	}
	for _, ip := range b.Ipv6Addresses {
		a.Ipv6Addresses = append(a.Ipv6Addresses, deref(ip.Ipv6Address))
	}

	for _, prefix := range b.Ipv6Prefixes {
		a.Ipv6Prefixes = append(a.Ipv6Prefixes, deref(prefix.Ipv6Prefix))
	}
	a.MacAddress = deref(b.MacAddress)
	a.NetworkInterfaceID = deref(b.NetworkInterfaceId)
	a.PrivateDNSName = deref(b.PrivateDnsName)
	a.PrivateIPAddress = deref(b.PrivateIpAddress)
	for _, ip := range b.PrivateIpAddresses {
		a.PrivateIPAddresses = append(a.PrivateIPAddresses, deref(ip.PrivateIpAddress))
	}

	a.SourceDestCheck = b.SourceDestCheck
	a.Status = string(b.Status)
	return a
}

// ENI ...
type ENI struct {

	// The time stamp when the attachment initiated.
	AttachTime *time.Time

	// The ID of the network interface attachment.
	AttachmentID string

	// Indicates whether the network interface is deleted when the instance is
	// terminated.
	DeleteOnTermination bool

	// The index of the device on the instance for the network interface attachment.
	DeviceIndex int32

	// The index of the network card.
	NetworkCardIndex int32

	// The attachment state.
	AttachmentStatus string

	// The description.
	Description string

	// One or more security groups.
	Groups []string

	// The IPv4 delegated prefixes that are assigned to the network interface.
	Ipv4Prefixes []string

	// One or more IPv6 addresses associated with the network interface.
	Ipv6Addresses []string

	// The IPv6 delegated prefixes that are assigned to the network interface.
	Ipv6Prefixes []string

	// The MAC address.
	MacAddress string

	// The ID of the network interface.
	NetworkInterfaceID string

	// The ID of the Amazon Web Services account that created the network interface.
	OwnerID string

	// The private DNS name.
	PrivateDNSName string

	// The IPv4 address of the network interface within the subnet.
	PrivateIPAddress string

	// One or more private IPv4 addresses associated with the network interface.
	PrivateIPAddresses []string

	// Indicates whether source/destination checking is enabled.
	SourceDestCheck *bool

	// The status of the network interface.
	Status string
}

// ComplianceDetail ...
type ComplianceDetail struct {
	ID string
	// Supplementary information about how the evaluation determined the compliance.
	Annotation string

	// Indicates whether the Amazon Web Services resource complies with the Config rule
	// that evaluated it. For the EvaluationResult data type, Config supports only the
	// COMPLIANT, NON_COMPLIANT, and NOT_APPLICABLE values. Config does not support the
	// INSUFFICIENT_DATA value for the EvaluationResult data type.
	ComplianceType string

	// The time when the Config rule evaluated the Amazon Web Services resource.
	ConfigRuleInvokedTime *time.Time

	// The time when Config recorded the evaluation result.
	ResultRecordedTime *time.Time
}

// NewComplianceDetail ...
func NewComplianceDetail(detail configservice.EvaluationResult) ComplianceDetail {
	other := ComplianceDetail{
		ID:                    *detail.EvaluationResultIdentifier.EvaluationResultQualifier.ConfigRuleName,
		ComplianceType:        string(detail.ComplianceType),
		ConfigRuleInvokedTime: detail.ConfigRuleInvokedTime,
		ResultRecordedTime:    detail.ResultRecordedTime,
	}
	if detail.Annotation != nil {
		other.Annotation = *detail.Annotation
	}
	return other
}

// NewPatchDetail ...
func NewPatchDetail(b ssmTypes.PatchComplianceData) PatchDetail {
	a := PatchDetail{}
	a.CVEIds = deref(b.CVEIds)
	a.Classification = deref(b.Classification)
	a.KBId = deref(b.KBId)
	a.InstalledTime = b.InstalledTime
	a.Severity = deref(b.Severity)
	a.State = string(b.State)
	a.Title = deref(b.Title)
	return a

}

// PatchDetail ...
type PatchDetail struct {

	// The classification of the patch, such as SecurityUpdates, Updates, and
	// CriticalUpdates.
	//
	// This member is required.
	Classification string

	// The date/time the patch was installed on the managed node. Not all operating
	// systems provide this level of information.
	//
	// This member is required.
	InstalledTime *time.Time

	KBId string

	// The severity of the patchsuch as Critical, Important, and Moderate.
	//
	// This member is required.
	Severity string

	// The state of the patch on the managed node, such as INSTALLED or FAILED. For
	// descriptions of each patch state, see About patch compliance
	// (https://docs.aws.amazon.com/systems-manager/latest/userguide/sysman-compliance-about.html#sysman-compliance-monitor-patch)
	// in the Amazon Web Services Systems Manager User Guide.
	//
	// This member is required.
	State string

	// The title of the patch.
	//
	// This member is required.
	Title string

	// The IDs of one or more Common Vulnerabilities and Exposure (CVE) issues that are
	// resolved by the patch.
	CVEIds string
}

// IsLinux ...
func (p PatchDetail) IsLinux() bool {
	parts := strings.Split(p.Title, ":") // e.g. git.x86_64:0:2.32.0-1.amzn2.0.1
	return len(parts) == 3
}

// GetName ...
func (p PatchDetail) GetName() string {
	return p.KBId
}

// GetVersion ...
func (p PatchDetail) GetVersion() string {
	if p.IsLinux() {
		return strings.ReplaceAll(p.Title, p.KBId+":", "")
	}
	return ""
}

// GetTitle ...
func (p PatchDetail) GetTitle() string {
	if p.Title == "" {
		return p.KBId
	}
	return p.Title
}

// IsInstalled ...
func (p PatchDetail) IsInstalled() bool {
	return p.State == string(ssmTypes.PatchComplianceDataStateInstalled)
}

// IsMissing ...
func (p PatchDetail) IsMissing() bool {
	return p.State == string(ssmTypes.PatchComplianceDataStateMissing)
}

// IsPendingReboot ...
func (p PatchDetail) IsPendingReboot() bool {
	return p.State == string(ssmTypes.PatchComplianceDataStateInstalledPendingReboot)
}

// IsFailed ...
func (p PatchDetail) IsFailed() bool {
	return p.State == string(ssmTypes.PatchComplianceDataStateFailed)
}

// NewInstance ...
func NewInstance(b types.Instance) *Instance {
	a := Instance{}
	a.AmiLaunchIndex = b.AmiLaunchIndex
	a.Architecture = string(b.Architecture)
	a.BlockDeviceMappings = b.BlockDeviceMappings
	a.BootMode = b.BootMode
	a.CapacityReservationID = deref(b.CapacityReservationId)
	a.EbsOptimized = b.EbsOptimized
	a.ElasticGpuAssociations = b.ElasticGpuAssociations
	a.ElasticInferenceAcceleratorAssociations = b.ElasticInferenceAcceleratorAssociations
	a.EnaSupport = b.EnaSupport
	a.Hypervisor = string(b.Hypervisor)
	if b.IamInstanceProfile != nil {
		a.IamInstanceProfile = deref(b.IamInstanceProfile.Arn)
	}
	a.ImageID = deref(b.ImageId)
	a.InstanceID = deref(b.InstanceId)
	a.InstanceLifecycle = string(b.InstanceLifecycle)
	a.InstanceType = string(b.InstanceType)
	a.Ipv6Address = deref(b.Ipv6Address)
	a.KernelID = deref(b.KernelId)
	a.KeyName = deref(b.KeyName)
	a.LaunchTime = b.LaunchTime

	a.OutpostArn = deref(b.OutpostArn)
	// a.Placement = string(b.Placement)
	a.PlatformDetails = deref(b.PlatformDetails)
	a.PrivateDNSName = deref(b.PrivateDnsName)
	a.PrivateIPAddress = deref(b.PrivateIpAddress)
	for _, code := range b.ProductCodes {
		a.ProductCodes = append(a.ProductCodes, *code.ProductCodeId)
	}
	a.PublicDNSName = deref(b.PublicDnsName)
	a.PublicIPAddress = deref(b.PublicIpAddress)
	a.RamdiskID = deref(b.RamdiskId)
	a.RootDeviceName = deref(b.RootDeviceName)
	a.RootDeviceType = string(b.RootDeviceType)
	for _, sg := range b.SecurityGroups {
		a.SecurityGroups = makeMap(a.SecurityGroups)
		a.SecurityGroups[*sg.GroupId] = deref(sg.GroupName)
	}
	if b.SourceDestCheck != nil {
		a.SourceDestCheck = *b.SourceDestCheck

	}
	a.SpotInstanceRequestID = deref(b.SpotInstanceRequestId)
	a.SriovNetSupport = deref(b.SriovNetSupport)
	a.State = string(b.State.Name)
	if b.StateReason != nil {
		a.StateReason = string(*b.StateReason.Message)
	}
	a.StateTransitionReason = deref(b.StateTransitionReason)
	a.SubnetID = deref(b.SubnetId)
	for _, tag := range b.Tags {
		a.Tags = makeMap(a.Tags)
		a.Tags[*tag.Key] = deref(tag.Value)
	}
	a.UsageOperation = deref(b.UsageOperation)
	a.UsageOperationUpdateTime = b.UsageOperationUpdateTime
	a.VirtualizationType = string(b.VirtualizationType)
	a.VpcID = deref(b.VpcId)

	for _, eni := range b.NetworkInterfaces {
		a.NetworkInterfaces = append(a.NetworkInterfaces, NewENI(eni))
	}
	return &a
}

// GetHostname ...
func (i Instance) GetHostname() string {
	if name, ok := i.Tags["Name"]; ok {
		return name
	}
	return i.PrivateDNSName
}

// GetID ...
func (i Instance) GetID() string {
	return i.InstanceID
}

// GetIP ...
func (i Instance) GetIP() string {
	return i.PrivateIPAddress
}

// GetPlatform ...
func (i Instance) GetPlatform() string {
	if i.Inventory != nil && i.Inventory["PlatformName"] != "" {
		return i.Inventory["PlatformName"]
	}
	return i.PlatformDetails
}

// GetPatches ...
func (i Instance) GetPatches() []v1.Patch {
	patches := []v1.Patch{}
	for _, p := range i.Patches {
		patches = append(patches, p)
	}
	return patches
}

// Instance  +k8s:deepcopy-gen=true
type Instance struct {
	Inventory  map[string]string            `json:"Inventory,omitempty"`
	PatchState *ssmTypes.InstancePatchState `json:"PatchState,omitempty"`
	Patches    []PatchDetail                `json:"Patches,omitempty"`
	Compliance []ComplianceDetail           `json:"Compliance,omitempty"`
	// The AMI launch index, which can be used to find this instance in the launch
	// group.
	AmiLaunchIndex *int32 `json:"ami_launch_index,omitempty"`

	// The architecture of the image.
	Architecture string `json:"architecture,omitempty"`

	// Any block device mapping entries for the instance.
	BlockDeviceMappings []types.InstanceBlockDeviceMapping `json:"block_device_mappings,omitempty"`

	// The boot mode of the instance. For more information, see Boot modes
	// (https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/ami-boot.html) in the
	// Amazon EC2 User Guide.
	BootMode types.BootModeValues `json:"boot_mode,omitempty"`

	// The ID of the Capacity Reservation.
	CapacityReservationID string `json:"capacity_reservation_id,omitempty"`

	// Information about the Capacity Reservation targeting option.
	// CapacityReservationSpecification *CapacityReservationSpecificationResponse

	// The CPU options for the instance.
	// CpuOptions *CpuOptions

	// Indicates whether the instance is optimized for Amazon EBS I/O. This
	// optimization provides dedicated throughput to Amazon EBS and an optimized
	// configuration stack to provide optimal I/O performance. This optimization isn't
	// available with all instance types. Additional usage charges apply when using an
	// EBS Optimized instance.
	EbsOptimized *bool `json:"ebs_optimized,omitempty"`

	// The Elastic GPU associated with the instance.
	ElasticGpuAssociations []types.ElasticGpuAssociation `json:"elastic_gpu_associations,omitempty"`

	// The elastic inference accelerator associated with the instance.
	ElasticInferenceAcceleratorAssociations []types.ElasticInferenceAcceleratorAssociation `json:"elastic_inference_accelerator_associations,omitempty"`

	// Specifies whether enhanced networking with ENA is enabled.
	EnaSupport *bool `json:"ena_support,omitempty"`

	// Indicates whether the instance is enabled for Amazon Web Services Nitro
	// Enclaves.
	// EnclaveOptions *EnclaveOptions

	// Indicates whether the instance is enabled for hibernation.
	// HibernationOptions *HibernationOptions

	// The hypervisor type of the instance. The value xen is used for both Xen and
	// Nitro hypervisors.
	Hypervisor string `json:"hypervisor,omitempty"`

	// The IAM instance profile associated with the instance, if applicable.
	IamInstanceProfile string `json:"iam_instance_profile,omitempty"`

	// The ID of the AMI used to launch the instance.
	ImageID string `json:"image_id,omitempty"`

	// The ID of the instance.
	InstanceID string `json:"instance_id,omitempty"`

	// Indicates whether this is a Spot Instance or a Scheduled Instance.
	InstanceLifecycle string `json:"instance_lifecycle,omitempty"`

	// The instance type.
	InstanceType string `json:"instance_type,omitempty"`

	// The IPv6 address assigned to the instance.
	Ipv6Address string `json:"ipv_6_address,omitempty"`

	// The kernel associated with this instance, if applicable.
	KernelID string `json:"kernel_id,omitempty"`

	// The name of the key pair, if this instance was launched with an associated key
	// pair.
	KeyName string `json:"key_name,omitempty"`

	// The time the instance was launched.
	LaunchTime *time.Time `json:"launch_time,omitempty"`

	// The license configurations for the instance.
	// Licenses []LicenseConfiguration

	// // The metadata options for the instance.
	// MetadataOptions *InstanceMetadataOptionsResponse

	// // The monitoring for the instance.
	// Monitoring *Monitoring

	// [EC2-VPC] The network interfaces for the instance.
	NetworkInterfaces []ENI `json:"network_interfaces,omitempty"`

	// The Amazon Resource Name (ARN) of the Outpost.
	OutpostArn string `json:"outpost_arn,omitempty"`

	// The location where the instance launched, if applicable.
	Placement string `json:"placement,omitempty"`

	// The value is Windows for Windows instances; otherwise blank.
	// Platform PlatformValues

	// The platform details value for the instance. For more information, see AMI
	// billing information fields
	// (https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/billing-info-fields.html)
	// in the Amazon EC2 User Guide.
	PlatformDetails string `json:"platform_details,omitempty"`

	// (IPv4 only) The private DNS hostname name assigned to the instance. This DNS
	// hostname can only be used inside the Amazon EC2 network. This name is not
	// available until the instance enters the running state. [EC2-VPC] The
	// Amazon-provided DNS server resolves Amazon-provided private DNS hostnames if
	// you've enabled DNS resolution and DNS hostnames in your VPC. If you are not
	// using the Amazon-provided DNS server in your VPC, your custom domain name
	// servers must resolve the hostname as appropriate.
	PrivateDNSName string `json:"private_dns_name,omitempty"`

	// The options for the instance hostname.
	// PrivateDnsNameOptions *PrivateDnsNameOptionsResponse

	// The private IPv4 address assigned to the instance.
	PrivateIPAddress string `json:"private_ip_address,omitempty"`

	// The product codes attached to this instance, if applicable.
	ProductCodes []string `json:"product_codes,omitempty"`

	// (IPv4 only) The public DNS name assigned to the instance. This name is not
	// available until the instance enters the running state. For EC2-VPC, this name is
	// only available if you've enabled DNS hostnames for your VPC.
	PublicDNSName string `json:"public_dns_name,omitempty"`

	// The public IPv4 address, or the Carrier IP address assigned to the instance, if
	// applicable. A Carrier IP address only applies to an instance launched in a
	// subnet associated with a Wavelength Zone.
	PublicIPAddress string `json:"public_ip_address,omitempty"`

	// The RAM disk associated with this instance, if applicable.
	RamdiskID string `json:"ramdisk_id,omitempty"`

	// The device name of the root device volume (for example, /dev/sda1).
	RootDeviceName string `json:"root_device_name,omitempty"`

	// The root device type used by the AMI. The AMI can use an EBS volume or an
	// instance store volume.
	RootDeviceType string `json:"root_device_type,omitempty"`

	// The security groups for the instance.
	SecurityGroups map[string]string `json:"security_groups,omitempty"`

	// Indicates whether source/destination checking is enabled.
	SourceDestCheck bool `json:"source_dest_check,omitempty"`

	// If the request is a Spot Instance request, the ID of the request.
	SpotInstanceRequestID string `json:"spot_instance_request_id,omitempty"`

	// Specifies whether enhanced networking with the Intel 82599 Virtual Function
	// interface is enabled.
	SriovNetSupport string `json:"sriov_net_support,omitempty"`

	// The current state of the instance.
	State string `json:"state,omitempty"`

	// The reason for the most recent state transition.
	StateReason string `json:"state_reason,omitempty"`

	// The reason for the most recent state transition. This might be an empty string.
	StateTransitionReason string `json:"state_transition_reason,omitempty"`

	// [EC2-VPC] The ID of the subnet in which the instance is running.
	SubnetID string `json:"subnet_id,omitempty"`

	// Any tags assigned to the instance.
	Tags map[string]string `json:"tags,omitempty"`

	// The usage operation value for the instance. For more information, see AMI
	// billing information fields
	// (https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/billing-info-fields.html)
	// in the Amazon EC2 User Guide.
	UsageOperation string `json:"usage_operation,omitempty"`

	// The time that the usage operation was last updated.
	UsageOperationUpdateTime *time.Time `json:"usage_operation_update_time,omitempty"`

	// The virtualization type of the instance.
	VirtualizationType string `json:"virtualization_type,omitempty"`

	// [EC2-VPC] The ID of the VPC in which the instance is running.
	VpcID string `json:"vpc_id,omitempty"`

	TrustedAdvisorChecks []TrustedAdvisorCheck `json:"trusted_advisor_checks,omitempty"`
}

// TrustedAdvisorCheck ...
type TrustedAdvisorCheck struct {
	EstimatedMonthlySavings float64           `json:"estimated_monthly_savings,omitempty"`
	CheckID                 string            `json:"check_id,omitempty"`
	CheckName               string            `json:"check_name,omitempty"`
	CheckCategory           string            `json:"check_category,omitempty"`
	CheckStatus             string            `json:"check_status,omitempty"`
	SecurityGroupID         string            `json:"security_group_id,omitempty"`
	Metdata                 map[string]string `json:"metadata,omitempty"`
}

// TrustedAdvisorCheckResult The results of a Trusted Advisor check returned by
// DescribeTrustedAdvisorCheckResult.
type TrustedAdvisorCheckResult struct {
	CheckName        string `json:"check_name,omitempty"`
	CheckDescription string `json:"check_description,omitempty"`
	CheckCategory    string `json:"check_category,omitempty"`
	// Summary information that relates to the category of the check. Cost Optimizing
	// is the only category that is currently supported.
	//
	// This member is required.
	CategorySpecificSummary TrustedAdvisorCategorySpecificSummary `json:"category_specific_summary,omitempty"`

	// The unique identifier for the Trusted Advisor check.
	//
	// This member is required.
	CheckID string `json:"check_id,omitempty"`

	// The details about each resource listed in the check result.
	//
	// This member is required.
	FlaggedResources []TrustedAdvisorResourceDetail `json:"flagged_resources,omitempty"`

	// Details about AWS resources that were analyzed in a call to Trusted Advisor
	// DescribeTrustedAdvisorCheckSummaries.
	//
	// This member is required.
	ResourcesSummary TrustedAdvisorResourcesSummary `json:"resources_summary,omitempty"`

	// The alert status of the check: "ok" (green), "warning" (yellow), "error" (red),
	// or "not_available".
	//
	// This member is required.
	Status string `json:"status,omitempty"`

	// The time of the last refresh of the check.
	//
	// This member is required.
	Timestamp string `json:"timestamp,omitempty"`
}

// TrustedAdvisorCategorySpecificSummary The container for summary information that relates to the category of the
// Trusted Advisor check.
type TrustedAdvisorCategorySpecificSummary struct {

	// The summary information about cost savings for a Trusted Advisor check that is
	// in the Cost Optimizing category.
	CostOptimizing TrustedAdvisorCostOptimizingSummary `json:"cost_optimizing,omitempty"`
}

// TrustedAdvisorCostOptimizingSummary The estimated cost savings that might be realized if the recommended operations
// are taken.
type TrustedAdvisorCostOptimizingSummary struct {

	// The estimated monthly savings that might be realized if the recommended
	// operations are taken.
	//
	// This member is required.
	EstimatedMonthlySavings float64 `json:"estimated_monthly_savings,omitempty"`

	// The estimated percentage of savings that might be realized if the recommended
	// operations are taken.
	//
	// This member is required.
	EstimatedPercentMonthlySavings float64 `json:"estimated_percent_monthly_savings,omitempty"`
}

// TrustedAdvisorResourcesSummary Details about AWS resources that were analyzed in a call to Trusted Advisor
// DescribeTrustedAdvisorCheckSummaries.
type TrustedAdvisorResourcesSummary struct {

	// The number of AWS resources that were flagged (listed) by the Trusted Advisor
	// check.
	//
	// This member is required.
	ResourcesFlagged int64 `json:"resources_flagged,omitempty"`

	// The number of AWS resources ignored by Trusted Advisor because information was
	// unavailable.
	//
	// This member is required.
	ResourcesIgnored int64 `json:"resources_ignored,omitempty"`

	// The number of AWS resources that were analyzed by the Trusted Advisor check.
	//
	// This member is required.
	ResourcesProcessed int64 `json:"resources_processed,omitempty"`

	// The number of AWS resources ignored by Trusted Advisor because they were marked
	// as suppressed by the user.
	//
	// This member is required.
	ResourcesSuppressed int64 `json:"resources_suppressed,omitempty"`
}

// TrustedAdvisorResourceDetail Contains information about a resource identified by a Trusted Advisor check.
type TrustedAdvisorResourceDetail struct {

	// Additional information about the identified resource. The exact metadata and its
	// order can be obtained by inspecting the TrustedAdvisorCheckDescription object
	// returned by the call to DescribeTrustedAdvisorChecks. Metadata contains all the
	// data that is shown in the Excel download, even in those cases where the UI shows
	// just summary data.
	//
	// This member is required.
	// Modifying the inbuilt metadata type to map[string]string to also retain the header information
	Metadata map[string]string `json:"metadata,omitempty"`

	// The unique identifier for the identified resource.
	//
	// This member is required.
	ResourceID string `json:"resource_id,omitempty"`

	// The status code for the resource identified in the Trusted Advisor check.
	//
	// This member is required.
	Status string `json:"status,omitempty"`

	// Specifies whether the AWS resource was ignored by Trusted Advisor because it was
	// marked as suppressed by the user.
	IsSuppressed bool `json:"is_suppressed,omitempty"`

	// The AWS Region in which the identified resource is located.
	Region string `json:"region,omitempty"`
}

// NewTrustedAdvisorCategorySpecificSummary ...
func NewTrustedAdvisorCategorySpecificSummary(b *supportTypes.TrustedAdvisorCategorySpecificSummary) TrustedAdvisorCategorySpecificSummary {
	a := TrustedAdvisorCategorySpecificSummary{}
	if b.CostOptimizing != nil {
		a.CostOptimizing = NewTrustedAdvisorCostOptimizingSummary(b.CostOptimizing)
	}
	return a
}

// NewTrustedAdvisorCostOptimizingSummary ...
func NewTrustedAdvisorCostOptimizingSummary(b *supportTypes.TrustedAdvisorCostOptimizingSummary) TrustedAdvisorCostOptimizingSummary {
	a := TrustedAdvisorCostOptimizingSummary{}
	a.EstimatedMonthlySavings = b.EstimatedMonthlySavings
	a.EstimatedPercentMonthlySavings = b.EstimatedPercentMonthlySavings
	return a
}

// NewTrustedAdvisorResourceDetailList ...
func NewTrustedAdvisorResourceDetailList(b []supportTypes.TrustedAdvisorResourceDetail, checkMetadata []string) []TrustedAdvisorResourceDetail {
	a := []TrustedAdvisorResourceDetail{}
	for _, v := range b {
		a = append(a, NewTrustedAdvisorResourceDetail(v, checkMetadata))
	}
	return a
}

// NewTrustedAdvisorResourceDetail ...
func NewTrustedAdvisorResourceDetail(b supportTypes.TrustedAdvisorResourceDetail, checkMetadata []string) TrustedAdvisorResourceDetail {
	a := TrustedAdvisorResourceDetail{}
	a.Metadata = createMapFromLists(checkMetadata, b.Metadata)
	a.ResourceID = deref(b.ResourceId)
	a.Status = deref(b.Status)
	a.IsSuppressed = b.IsSuppressed
	a.Region = deref(b.Region)
	return a
}

// NewTrustedAdvisorResourcesSummary ...
func NewTrustedAdvisorResourcesSummary(b *supportTypes.TrustedAdvisorResourcesSummary) TrustedAdvisorResourcesSummary {
	a := TrustedAdvisorResourcesSummary{}
	a.ResourcesFlagged = b.ResourcesFlagged
	a.ResourcesIgnored = b.ResourcesIgnored
	a.ResourcesProcessed = b.ResourcesProcessed
	a.ResourcesSuppressed = b.ResourcesSuppressed
	return a
}

// NewTrustedAdvisorCheckResult ...
func NewTrustedAdvisorCheckResult(b *supportTypes.TrustedAdvisorCheckResult, checkName, checkDescription, checkCategory string, checkMetadata []string) *TrustedAdvisorCheckResult {
	a := &TrustedAdvisorCheckResult{
		CheckName:        checkName,
		CheckDescription: checkDescription,
		CheckCategory:    checkCategory,
	}
	if b.CategorySpecificSummary != nil {
		a.CategorySpecificSummary = NewTrustedAdvisorCategorySpecificSummary(b.CategorySpecificSummary)
	}
	a.CheckID = deref(b.CheckId)
	a.FlaggedResources = NewTrustedAdvisorResourceDetailList(b.FlaggedResources, checkMetadata)
	a.Status = deref(b.Status)
	a.Timestamp = deref(b.Timestamp)
	if b.ResourcesSummary != nil {
		a.ResourcesSummary = NewTrustedAdvisorResourcesSummary(b.ResourcesSummary)
	}
	return a
}

// list1 creates the keys for the map while list2 makes up the volume
func createMapFromLists(list1 []string, list2 []*string) map[string]string {
	m := make(map[string]string)
	for i, v := range list1 {
		m[v] = *list2[i]
	}
	return m
}

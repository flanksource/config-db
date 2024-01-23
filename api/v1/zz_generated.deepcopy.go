//go:build !ignore_autogenerated
// +build !ignore_autogenerated

/*
Copyright 2023.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Code generated by controller-gen. DO NOT EDIT.

package v1

import (
	"github.com/flanksource/duty/types"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtime "k8s.io/apimachinery/pkg/runtime"
)

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *AWS) DeepCopyInto(out *AWS) {
	*out = *in
	in.BaseScraper.DeepCopyInto(&out.BaseScraper)
	if in.AWSConnection != nil {
		in, out := &in.AWSConnection, &out.AWSConnection
		*out = new(AWSConnection)
		(*in).DeepCopyInto(*out)
	}
	in.CloudTrail.DeepCopyInto(&out.CloudTrail)
	if in.Include != nil {
		in, out := &in.Include, &out.Include
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
	if in.Exclude != nil {
		in, out := &in.Exclude, &out.Exclude
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
	out.CostReporting = in.CostReporting
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new AWS.
func (in *AWS) DeepCopy() *AWS {
	if in == nil {
		return nil
	}
	out := new(AWS)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *AWSConnection) DeepCopyInto(out *AWSConnection) {
	*out = *in
	in.AccessKey.DeepCopyInto(&out.AccessKey)
	in.SecretKey.DeepCopyInto(&out.SecretKey)
	if in.Region != nil {
		in, out := &in.Region, &out.Region
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new AWSConnection.
func (in *AWSConnection) DeepCopy() *AWSConnection {
	if in == nil {
		return nil
	}
	out := new(AWSConnection)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *Authentication) DeepCopyInto(out *Authentication) {
	*out = *in
	in.Username.DeepCopyInto(&out.Username)
	in.Password.DeepCopyInto(&out.Password)
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new Authentication.
func (in *Authentication) DeepCopy() *Authentication {
	if in == nil {
		return nil
	}
	out := new(Authentication)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *Azure) DeepCopyInto(out *Azure) {
	*out = *in
	in.BaseScraper.DeepCopyInto(&out.BaseScraper)
	in.ClientID.DeepCopyInto(&out.ClientID)
	in.ClientSecret.DeepCopyInto(&out.ClientSecret)
	if in.Exclusions != nil {
		in, out := &in.Exclusions, &out.Exclusions
		*out = new(AzureExclusions)
		(*in).DeepCopyInto(*out)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new Azure.
func (in *Azure) DeepCopy() *Azure {
	if in == nil {
		return nil
	}
	out := new(Azure)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *AzureDevops) DeepCopyInto(out *AzureDevops) {
	*out = *in
	in.BaseScraper.DeepCopyInto(&out.BaseScraper)
	in.PersonalAccessToken.DeepCopyInto(&out.PersonalAccessToken)
	if in.Projects != nil {
		in, out := &in.Projects, &out.Projects
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
	if in.Pipelines != nil {
		in, out := &in.Pipelines, &out.Pipelines
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new AzureDevops.
func (in *AzureDevops) DeepCopy() *AzureDevops {
	if in == nil {
		return nil
	}
	out := new(AzureDevops)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *AzureExclusions) DeepCopyInto(out *AzureExclusions) {
	*out = *in
	if in.ActivityLogs != nil {
		in, out := &in.ActivityLogs, &out.ActivityLogs
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new AzureExclusions.
func (in *AzureExclusions) DeepCopy() *AzureExclusions {
	if in == nil {
		return nil
	}
	out := new(AzureExclusions)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *BaseScraper) DeepCopyInto(out *BaseScraper) {
	*out = *in
	in.Transform.DeepCopyInto(&out.Transform)
	if in.CreateFields != nil {
		in, out := &in.CreateFields, &out.CreateFields
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
	if in.DeleteFields != nil {
		in, out := &in.DeleteFields, &out.DeleteFields
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
	if in.Tags != nil {
		in, out := &in.Tags, &out.Tags
		*out = make(JSONStringMap, len(*in))
		for key, val := range *in {
			(*out)[key] = val
		}
	}
	if in.Properties != nil {
		in, out := &in.Properties, &out.Properties
		*out = make(map[string]types.Properties, len(*in))
		for key, val := range *in {
			var outVal []*types.Property
			if val == nil {
				(*out)[key] = nil
			} else {
				in, out := &val, &outVal
				*out = make(types.Properties, len(*in))
				for i := range *in {
					if (*in)[i] != nil {
						in, out := &(*in)[i], &(*out)[i]
						*out = new(types.Property)
						(*in).DeepCopyInto(*out)
					}
				}
			}
			(*out)[key] = outVal
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new BaseScraper.
func (in *BaseScraper) DeepCopy() *BaseScraper {
	if in == nil {
		return nil
	}
	out := new(BaseScraper)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ChangeRetentionSpec) DeepCopyInto(out *ChangeRetentionSpec) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ChangeRetentionSpec.
func (in *ChangeRetentionSpec) DeepCopy() *ChangeRetentionSpec {
	if in == nil {
		return nil
	}
	out := new(ChangeRetentionSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *CloudTrail) DeepCopyInto(out *CloudTrail) {
	*out = *in
	if in.Exclude != nil {
		in, out := &in.Exclude, &out.Exclude
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new CloudTrail.
func (in *CloudTrail) DeepCopy() *CloudTrail {
	if in == nil {
		return nil
	}
	out := new(CloudTrail)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *Connection) DeepCopyInto(out *Connection) {
	*out = *in
	in.Authentication.DeepCopyInto(&out.Authentication)
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new Connection.
func (in *Connection) DeepCopy() *Connection {
	if in == nil {
		return nil
	}
	out := new(Connection)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *CostReporting) DeepCopyInto(out *CostReporting) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new CostReporting.
func (in *CostReporting) DeepCopy() *CostReporting {
	if in == nil {
		return nil
	}
	out := new(CostReporting)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ExternalID) DeepCopyInto(out *ExternalID) {
	*out = *in
	if in.ExternalID != nil {
		in, out := &in.ExternalID, &out.ExternalID
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ExternalID.
func (in *ExternalID) DeepCopy() *ExternalID {
	if in == nil {
		return nil
	}
	out := new(ExternalID)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *File) DeepCopyInto(out *File) {
	*out = *in
	in.BaseScraper.DeepCopyInto(&out.BaseScraper)
	if in.Paths != nil {
		in, out := &in.Paths, &out.Paths
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
	if in.Ignore != nil {
		in, out := &in.Ignore, &out.Ignore
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new File.
func (in *File) DeepCopy() *File {
	if in == nil {
		return nil
	}
	out := new(File)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *FileLocation) DeepCopyInto(out *FileLocation) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new FileLocation.
func (in *FileLocation) DeepCopy() *FileLocation {
	if in == nil {
		return nil
	}
	out := new(FileLocation)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *Filter) DeepCopyInto(out *Filter) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new Filter.
func (in *Filter) DeepCopy() *Filter {
	if in == nil {
		return nil
	}
	out := new(Filter)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *GCPConnection) DeepCopyInto(out *GCPConnection) {
	*out = *in
	if in.Credentials != nil {
		in, out := &in.Credentials, &out.Credentials
		*out = new(types.EnvVar)
		(*in).DeepCopyInto(*out)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new GCPConnection.
func (in *GCPConnection) DeepCopy() *GCPConnection {
	if in == nil {
		return nil
	}
	out := new(GCPConnection)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *GitHubActions) DeepCopyInto(out *GitHubActions) {
	*out = *in
	in.BaseScraper.DeepCopyInto(&out.BaseScraper)
	in.PersonalAccessToken.DeepCopyInto(&out.PersonalAccessToken)
	if in.Workflows != nil {
		in, out := &in.Workflows, &out.Workflows
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new GitHubActions.
func (in *GitHubActions) DeepCopy() *GitHubActions {
	if in == nil {
		return nil
	}
	out := new(GitHubActions)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *GitLocation) DeepCopyInto(out *GitLocation) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new GitLocation.
func (in *GitLocation) DeepCopy() *GitLocation {
	if in == nil {
		return nil
	}
	out := new(GitLocation)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *InvolvedObject) DeepCopyInto(out *InvolvedObject) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new InvolvedObject.
func (in *InvolvedObject) DeepCopy() *InvolvedObject {
	if in == nil {
		return nil
	}
	out := new(InvolvedObject)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in JSONStringMap) DeepCopyInto(out *JSONStringMap) {
	{
		in := &in
		*out = make(JSONStringMap, len(*in))
		for key, val := range *in {
			(*out)[key] = val
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new JSONStringMap.
func (in JSONStringMap) DeepCopy() JSONStringMap {
	if in == nil {
		return nil
	}
	out := new(JSONStringMap)
	in.DeepCopyInto(out)
	return *out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *Kubernetes) DeepCopyInto(out *Kubernetes) {
	*out = *in
	in.BaseScraper.DeepCopyInto(&out.BaseScraper)
	if in.Kubeconfig != nil {
		in, out := &in.Kubeconfig, &out.Kubeconfig
		*out = new(types.EnvVar)
		(*in).DeepCopyInto(*out)
	}
	in.Event.DeepCopyInto(&out.Event)
	in.Exclusions.DeepCopyInto(&out.Exclusions)
	if in.Relationships != nil {
		in, out := &in.Relationships, &out.Relationships
		*out = make([]KubernetesRelationship, len(*in))
		copy(*out, *in)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new Kubernetes.
func (in *Kubernetes) DeepCopy() *Kubernetes {
	if in == nil {
		return nil
	}
	out := new(Kubernetes)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *KubernetesConfigExclusions) DeepCopyInto(out *KubernetesConfigExclusions) {
	*out = *in
	if in.Names != nil {
		in, out := &in.Names, &out.Names
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
	if in.Kinds != nil {
		in, out := &in.Kinds, &out.Kinds
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
	if in.Namespaces != nil {
		in, out := &in.Namespaces, &out.Namespaces
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
	if in.Labels != nil {
		in, out := &in.Labels, &out.Labels
		*out = make(map[string]string, len(*in))
		for key, val := range *in {
			(*out)[key] = val
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new KubernetesConfigExclusions.
func (in *KubernetesConfigExclusions) DeepCopy() *KubernetesConfigExclusions {
	if in == nil {
		return nil
	}
	out := new(KubernetesConfigExclusions)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *KubernetesEvent) DeepCopyInto(out *KubernetesEvent) {
	*out = *in
	if in.Source != nil {
		in, out := &in.Source, &out.Source
		*out = make(map[string]string, len(*in))
		for key, val := range *in {
			(*out)[key] = val
		}
	}
	if in.Metadata != nil {
		in, out := &in.Metadata, &out.Metadata
		*out = new(metav1.ObjectMeta)
		(*in).DeepCopyInto(*out)
	}
	if in.InvolvedObject != nil {
		in, out := &in.InvolvedObject, &out.InvolvedObject
		*out = new(InvolvedObject)
		**out = **in
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new KubernetesEvent.
func (in *KubernetesEvent) DeepCopy() *KubernetesEvent {
	if in == nil {
		return nil
	}
	out := new(KubernetesEvent)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *KubernetesEventConfig) DeepCopyInto(out *KubernetesEventConfig) {
	*out = *in
	in.Exclusions.DeepCopyInto(&out.Exclusions)
	in.SeverityKeywords.DeepCopyInto(&out.SeverityKeywords)
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new KubernetesEventConfig.
func (in *KubernetesEventConfig) DeepCopy() *KubernetesEventConfig {
	if in == nil {
		return nil
	}
	out := new(KubernetesEventConfig)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *KubernetesEventExclusions) DeepCopyInto(out *KubernetesEventExclusions) {
	*out = *in
	if in.Names != nil {
		in, out := &in.Names, &out.Names
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
	if in.Namespaces != nil {
		in, out := &in.Namespaces, &out.Namespaces
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
	if in.Reasons != nil {
		in, out := &in.Reasons, &out.Reasons
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new KubernetesEventExclusions.
func (in *KubernetesEventExclusions) DeepCopy() *KubernetesEventExclusions {
	if in == nil {
		return nil
	}
	out := new(KubernetesEventExclusions)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *KubernetesFile) DeepCopyInto(out *KubernetesFile) {
	*out = *in
	in.BaseScraper.DeepCopyInto(&out.BaseScraper)
	out.Selector = in.Selector
	if in.Files != nil {
		in, out := &in.Files, &out.Files
		*out = make([]PodFile, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new KubernetesFile.
func (in *KubernetesFile) DeepCopy() *KubernetesFile {
	if in == nil {
		return nil
	}
	out := new(KubernetesFile)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *KubernetesRelationship) DeepCopyInto(out *KubernetesRelationship) {
	*out = *in
	out.Kind = in.Kind
	out.Name = in.Name
	out.Namespace = in.Namespace
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new KubernetesRelationship.
func (in *KubernetesRelationship) DeepCopy() *KubernetesRelationship {
	if in == nil {
		return nil
	}
	out := new(KubernetesRelationship)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *KubernetesRelationshipLookup) DeepCopyInto(out *KubernetesRelationshipLookup) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new KubernetesRelationshipLookup.
func (in *KubernetesRelationshipLookup) DeepCopy() *KubernetesRelationshipLookup {
	if in == nil {
		return nil
	}
	out := new(KubernetesRelationshipLookup)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *Mask) DeepCopyInto(out *Mask) {
	*out = *in
	out.Selector = in.Selector
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new Mask.
func (in *Mask) DeepCopy() *Mask {
	if in == nil {
		return nil
	}
	out := new(Mask)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in MaskList) DeepCopyInto(out *MaskList) {
	{
		in := &in
		*out = make(MaskList, len(*in))
		copy(*out, *in)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new MaskList.
func (in MaskList) DeepCopy() MaskList {
	if in == nil {
		return nil
	}
	out := new(MaskList)
	in.DeepCopyInto(out)
	return *out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *MaskSelector) DeepCopyInto(out *MaskSelector) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new MaskSelector.
func (in *MaskSelector) DeepCopy() *MaskSelector {
	if in == nil {
		return nil
	}
	out := new(MaskSelector)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *OpenAPIFieldRef) DeepCopyInto(out *OpenAPIFieldRef) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new OpenAPIFieldRef.
func (in *OpenAPIFieldRef) DeepCopy() *OpenAPIFieldRef {
	if in == nil {
		return nil
	}
	out := new(OpenAPIFieldRef)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *PodFile) DeepCopyInto(out *PodFile) {
	*out = *in
	if in.Path != nil {
		in, out := &in.Path, &out.Path
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new PodFile.
func (in *PodFile) DeepCopy() *PodFile {
	if in == nil {
		return nil
	}
	out := new(PodFile)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in Properties) DeepCopyInto(out *Properties) {
	{
		in := &in
		*out = make(Properties, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new Properties.
func (in Properties) DeepCopy() Properties {
	if in == nil {
		return nil
	}
	out := new(Properties)
	in.DeepCopyInto(out)
	return *out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *Property) DeepCopyInto(out *Property) {
	*out = *in
	if in.GitLocation != nil {
		in, out := &in.GitLocation, &out.GitLocation
		*out = new(GitLocation)
		**out = **in
	}
	if in.FileLocation != nil {
		in, out := &in.FileLocation, &out.FileLocation
		*out = new(FileLocation)
		**out = **in
	}
	if in.OpenAPI != nil {
		in, out := &in.OpenAPI, &out.OpenAPI
		*out = new(OpenAPIFieldRef)
		**out = **in
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new Property.
func (in *Property) DeepCopy() *Property {
	if in == nil {
		return nil
	}
	out := new(Property)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *QueryColumn) DeepCopyInto(out *QueryColumn) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new QueryColumn.
func (in *QueryColumn) DeepCopy() *QueryColumn {
	if in == nil {
		return nil
	}
	out := new(QueryColumn)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *QueryRequest) DeepCopyInto(out *QueryRequest) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new QueryRequest.
func (in *QueryRequest) DeepCopy() *QueryRequest {
	if in == nil {
		return nil
	}
	out := new(QueryRequest)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *RelationshipResult) DeepCopyInto(out *RelationshipResult) {
	*out = *in
	in.ConfigExternalID.DeepCopyInto(&out.ConfigExternalID)
	in.RelatedExternalID.DeepCopyInto(&out.RelatedExternalID)
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new RelationshipResult.
func (in *RelationshipResult) DeepCopy() *RelationshipResult {
	if in == nil {
		return nil
	}
	out := new(RelationshipResult)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in RelationshipResults) DeepCopyInto(out *RelationshipResults) {
	{
		in := &in
		*out = make(RelationshipResults, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new RelationshipResults.
func (in RelationshipResults) DeepCopy() RelationshipResults {
	if in == nil {
		return nil
	}
	out := new(RelationshipResults)
	in.DeepCopyInto(out)
	return *out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ResourceSelector) DeepCopyInto(out *ResourceSelector) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ResourceSelector.
func (in *ResourceSelector) DeepCopy() *ResourceSelector {
	if in == nil {
		return nil
	}
	out := new(ResourceSelector)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *RetentionSpec) DeepCopyInto(out *RetentionSpec) {
	*out = *in
	if in.Changes != nil {
		in, out := &in.Changes, &out.Changes
		*out = make([]ChangeRetentionSpec, len(*in))
		copy(*out, *in)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new RetentionSpec.
func (in *RetentionSpec) DeepCopy() *RetentionSpec {
	if in == nil {
		return nil
	}
	out := new(RetentionSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *RunNowResponse) DeepCopyInto(out *RunNowResponse) {
	*out = *in
	if in.Errors != nil {
		in, out := &in.Errors, &out.Errors
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new RunNowResponse.
func (in *RunNowResponse) DeepCopy() *RunNowResponse {
	if in == nil {
		return nil
	}
	out := new(RunNowResponse)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *SQL) DeepCopyInto(out *SQL) {
	*out = *in
	in.BaseScraper.DeepCopyInto(&out.BaseScraper)
	in.Connection.DeepCopyInto(&out.Connection)
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new SQL.
func (in *SQL) DeepCopy() *SQL {
	if in == nil {
		return nil
	}
	out := new(SQL)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ScrapeConfig) DeepCopyInto(out *ScrapeConfig) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	out.Status = in.Status
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ScrapeConfig.
func (in *ScrapeConfig) DeepCopy() *ScrapeConfig {
	if in == nil {
		return nil
	}
	out := new(ScrapeConfig)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *ScrapeConfig) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ScrapeConfigList) DeepCopyInto(out *ScrapeConfigList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]ScrapeConfig, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ScrapeConfigList.
func (in *ScrapeConfigList) DeepCopy() *ScrapeConfigList {
	if in == nil {
		return nil
	}
	out := new(ScrapeConfigList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *ScrapeConfigList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ScrapeConfigStatus) DeepCopyInto(out *ScrapeConfigStatus) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ScrapeConfigStatus.
func (in *ScrapeConfigStatus) DeepCopy() *ScrapeConfigStatus {
	if in == nil {
		return nil
	}
	out := new(ScrapeConfigStatus)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ScraperSpec) DeepCopyInto(out *ScraperSpec) {
	*out = *in
	if in.AWS != nil {
		in, out := &in.AWS, &out.AWS
		*out = make([]AWS, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	if in.File != nil {
		in, out := &in.File, &out.File
		*out = make([]File, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	if in.Kubernetes != nil {
		in, out := &in.Kubernetes, &out.Kubernetes
		*out = make([]Kubernetes, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	if in.KubernetesFile != nil {
		in, out := &in.KubernetesFile, &out.KubernetesFile
		*out = make([]KubernetesFile, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	if in.AzureDevops != nil {
		in, out := &in.AzureDevops, &out.AzureDevops
		*out = make([]AzureDevops, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	if in.GithubActions != nil {
		in, out := &in.GithubActions, &out.GithubActions
		*out = make([]GitHubActions, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	if in.Azure != nil {
		in, out := &in.Azure, &out.Azure
		*out = make([]Azure, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	if in.SQL != nil {
		in, out := &in.SQL, &out.SQL
		*out = make([]SQL, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	if in.Trivy != nil {
		in, out := &in.Trivy, &out.Trivy
		*out = make([]Trivy, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	in.Retention.DeepCopyInto(&out.Retention)
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ScraperSpec.
func (in *ScraperSpec) DeepCopy() *ScraperSpec {
	if in == nil {
		return nil
	}
	out := new(ScraperSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *Script) DeepCopyInto(out *Script) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new Script.
func (in *Script) DeepCopy() *Script {
	if in == nil {
		return nil
	}
	out := new(Script)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *SeverityKeywords) DeepCopyInto(out *SeverityKeywords) {
	*out = *in
	if in.Warn != nil {
		in, out := &in.Warn, &out.Warn
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
	if in.Error != nil {
		in, out := &in.Error, &out.Error
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new SeverityKeywords.
func (in *SeverityKeywords) DeepCopy() *SeverityKeywords {
	if in == nil {
		return nil
	}
	out := new(SeverityKeywords)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *Template) DeepCopyInto(out *Template) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new Template.
func (in *Template) DeepCopy() *Template {
	if in == nil {
		return nil
	}
	out := new(Template)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *Transform) DeepCopyInto(out *Transform) {
	*out = *in
	out.Script = in.Script
	if in.Include != nil {
		in, out := &in.Include, &out.Include
		*out = make([]Filter, len(*in))
		copy(*out, *in)
	}
	if in.Exclude != nil {
		in, out := &in.Exclude, &out.Exclude
		*out = make([]Filter, len(*in))
		copy(*out, *in)
	}
	if in.Masks != nil {
		in, out := &in.Masks, &out.Masks
		*out = make(MaskList, len(*in))
		copy(*out, *in)
	}
	in.Change.DeepCopyInto(&out.Change)
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new Transform.
func (in *Transform) DeepCopy() *Transform {
	if in == nil {
		return nil
	}
	out := new(Transform)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *TransformChange) DeepCopyInto(out *TransformChange) {
	*out = *in
	if in.Exclude != nil {
		in, out := &in.Exclude, &out.Exclude
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new TransformChange.
func (in *TransformChange) DeepCopy() *TransformChange {
	if in == nil {
		return nil
	}
	out := new(TransformChange)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *Trivy) DeepCopyInto(out *Trivy) {
	*out = *in
	in.BaseScraper.DeepCopyInto(&out.BaseScraper)
	if in.Compliance != nil {
		in, out := &in.Compliance, &out.Compliance
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
	if in.IgnoredLicenses != nil {
		in, out := &in.IgnoredLicenses, &out.IgnoredLicenses
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
	if in.Severity != nil {
		in, out := &in.Severity, &out.Severity
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
	if in.VulnType != nil {
		in, out := &in.VulnType, &out.VulnType
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
	if in.Scanners != nil {
		in, out := &in.Scanners, &out.Scanners
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
	if in.Kubernetes != nil {
		in, out := &in.Kubernetes, &out.Kubernetes
		*out = new(TrivyK8sOptions)
		(*in).DeepCopyInto(*out)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new Trivy.
func (in *Trivy) DeepCopy() *Trivy {
	if in == nil {
		return nil
	}
	out := new(Trivy)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *TrivyK8sOptions) DeepCopyInto(out *TrivyK8sOptions) {
	*out = *in
	if in.Components != nil {
		in, out := &in.Components, &out.Components
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new TrivyK8sOptions.
func (in *TrivyK8sOptions) DeepCopy() *TrivyK8sOptions {
	if in == nil {
		return nil
	}
	out := new(TrivyK8sOptions)
	in.DeepCopyInto(out)
	return out
}

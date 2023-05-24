package v1

import (
	"strings"
)

type Trivy struct {
	BaseScraper `json:",inline"`

	// Common Trivy Flags ...
	Version         string   `json:"version,omitempty" yaml:"version,omitempty"` // Specify the version of Trivy to use
	Compliance      []string `json:"compliance,omitempty" yaml:"compliance,omitempty"`
	IgnoredLicenses []string `json:"ignoredLicenses,omitempty" yaml:"ignoredLicenses,omitempty"`
	IgnoreUnfixed   bool     `json:"ignoreUnfixed,omitempty" yaml:"ignoreUnfixed,omitempty"`
	LicenseFull     bool     `json:"licenseFull,omitempty" yaml:"licenseFull,omitempty"`
	Severity        []string `json:"severity,omitempty" yaml:"severity,omitempty"`
	VulnType        []string `json:"vulnType,omitempty" yaml:"vulnType,omitempty"`
	Scanners        []string `json:"scanners,omitempty" yaml:"scanners,omitempty"`
	Timeout         string   `json:"timeout,omitempty" yaml:"timeout,omitempty"`

	Kubernetes *TrivyK8sOptions `json:"kubernetes,omitempty"`
}

func (t Trivy) IsEmpty() bool {
	return t.Kubernetes == nil
}

// GetK8sArgs returns a slice of arguments that Trivy uses to scan Kubernetes objects.
func (t Trivy) GetK8sArgs() []string {
	var args []string
	args = append(args, "k8s")
	args = append(args, "--format", "json") // hardcoded here. don't allow users this option.
	args = append(args, t.getCommonArgs()...)
	args = append(args, t.Kubernetes.getArgs()...)
	args = append(args, "all")
	return args
}

func (t Trivy) getCommonArgs() []string {
	var args []string
	if len(t.Compliance) > 0 {
		args = append(args, "--compliance", strings.Join(t.Compliance, ","))
	}
	if len(t.IgnoredLicenses) > 0 {
		args = append(args, "--ignored-licenses", strings.Join(t.IgnoredLicenses, ","))
	}
	if t.IgnoreUnfixed {
		args = append(args, "--ignore-unfixed")
	}
	if t.LicenseFull {
		args = append(args, "--license-full")
	}
	if len(t.Severity) > 0 {
		args = append(args, "--severity", strings.Join(t.Severity, ","))
	}
	if len(t.VulnType) > 0 {
		args = append(args, "--vuln-type", strings.Join(t.VulnType, ","))
	}
	if len(t.Scanners) > 0 {
		args = append(args, "--scanners", strings.Join(t.Scanners, ","))
	}
	if t.Timeout != "" {
		args = append(args, "--timeout", t.Timeout)
	}

	return args
}

// TrivyK8sOptions holds in Trivy flags that are Kubernetes specific.
type TrivyK8sOptions struct {
	Components []string `json:"components,omitempty" yaml:"components,omitempty"`
	Context    string   `json:"context,omitempty" yaml:"context,omitempty"`
	Kubeconfig string   `json:"kubeconfig,omitempty" yaml:"kubeconfig,omitempty"`
	Namespace  string   `json:"namespace,omitempty" yaml:"namespace,omitempty"`
}

func (t TrivyK8sOptions) getArgs() []string {
	var args []string
	if len(t.Components) > 0 {
		args = append(args, "--components", strings.Join(t.Components, ","))
	}
	if t.Kubeconfig != "" {
		args = append(args, "--kubeconfig", t.Kubeconfig)
	}
	if t.Namespace != "" {
		args = append(args, "--namespace", t.Namespace)
	}
	if t.Context != "" {
		args = append(args, "--context", t.Context)
	}
	return args
}

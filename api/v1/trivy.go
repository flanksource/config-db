package v1

import "strings"

type Trivy struct {
	BaseScraper `json:",inline"`
	Kubernetes  *TrivyOptions `json:"kubernetes,omitempty"`
}

func (t Trivy) GetKubernetesArgs() []string {
	var args []string
	args = append(args, "k8s")
	args = append(args, "--format", "json") // hardcoded here. don't allow users this option.
	args = append(args, "--offline-scan")   // Testing ... TODO: Remove this
	args = append(args, t.Kubernetes.GetArgs()...)
	args = append(args, "pods")
	return args
}

type TrivyOptions struct {
	Compliance      string   `json:"compliance,omitempty"`
	Components      []string `json:"components,omitempty"`
	IgnoredLicenses []string `json:"ignoredLicenses,omitempty"`
	IgnoreUnfixed   bool     `json:"ignoreUnfixed,omitempty"`
	Kubeconfig      string   `json:"kubeconfig,omitempty"`
	LicenseFull     bool     `json:"licenseFull,omitempty"`
	Namespace       string   `json:"namespace,omitempty"`
	Severity        string   `json:"severity,omitempty"`
	VulnType        string   `json:"vulnType,omitempty"`
}

func (t TrivyOptions) GetArgs() []string {
	var args []string
	if t.Compliance != "" {
		args = append(args, "--compliance", t.Compliance)
	}
	if len(t.Components) > 0 {
		args = append(args, "--components", strings.Join(t.Components, ","))
	}
	if len(t.IgnoredLicenses) > 0 {
		args = append(args, "--ignored-licenses", strings.Join(t.IgnoredLicenses, ","))
	}
	if t.IgnoreUnfixed {
		args = append(args, "--ignore-unfixed")
	}
	if t.Kubeconfig != "" {
		args = append(args, "--kubeconfig", t.Kubeconfig)
	}
	if t.LicenseFull {
		args = append(args, "--license-full")
	}
	if t.Namespace != "" {
		args = append(args, "--namespace", t.Namespace)
	}
	if t.Severity != "" {
		args = append(args, "--severity", t.Severity)
	}
	if t.VulnType != "" {
		args = append(args, "--vuln-type", t.VulnType)
	}
	return args
}

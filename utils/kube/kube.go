/*
Copyright 2017 The Kubernetes Authors.
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

package kube

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"k8s.io/client-go/discovery/cached/disk"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/flanksource/commons/files"
	"gopkg.in/flanksource/yaml.v3"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/homedir"
)

func getRestMapper(config *rest.Config) (meta.RESTMapper, error) {
	// re-use kubectl cache
	host := config.Host
	host = strings.ReplaceAll(host, "https://", "")
	host = strings.ReplaceAll(host, "-", "_")
	host = strings.ReplaceAll(host, ":", "_")
	cacheDir := os.ExpandEnv("$HOME/.kube/cache/discovery/" + host)
	cache, err := disk.NewCachedDiscoveryClientForConfig(config, cacheDir, "", 10*time.Minute)
	if err != nil {
		return nil, err
	}

	return restmapper.NewDeferredDiscoveryRESTMapper(cache), nil
}

func GetKindClient(cfg *rest.Config, kind string) (dynamic.NamespaceableResourceInterface, error) {
	dc, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}

	rm, err := getRestMapper(cfg)
	if err != nil {
		return nil, err
	}

	gvk, err := rm.KindFor(schema.GroupVersionResource{Resource: kind})
	if err != nil {
		return nil, err
	}

	gk := schema.GroupKind{Group: gvk.Group, Kind: gvk.Kind}
	mapping, err := rm.RESTMapping(gk, gvk.Version)
	if err != nil {
		return nil, err
	}

	return dc.Resource(mapping.Resource), nil
}

func NewKubeClientWithConfigPath(kubeConfigPath string) (kubernetes.Interface, *rest.Config, error) {
	config, err := clientcmd.BuildConfigFromFlags("", kubeConfigPath)
	if err != nil {
		return fake.NewSimpleClientset(), nil, err
	}

	client, err := kubernetes.NewForConfig(config)
	return client, config, err
}

func NewKubeClientWithConfig(kubeConfig string) (kubernetes.Interface, *rest.Config, error) {
	getter := func() (*clientcmdapi.Config, error) {
		clientCfg, err := clientcmd.NewClientConfigFromBytes([]byte(kubeConfig))
		if err != nil {
			return nil, err
		}

		apiCfg, err := clientCfg.RawConfig()
		if err != nil {
			return nil, err
		}

		return &apiCfg, nil
	}

	config, err := clientcmd.BuildConfigFromKubeconfigGetter("", getter)
	if err != nil {
		return fake.NewSimpleClientset(), nil, err
	}

	client, err := kubernetes.NewForConfig(config)
	return client, config, err
}

// NewK8sClient ...
func NewK8sClient() (kubernetes.Interface, *rest.Config, error) {
	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		kubeconfig = os.ExpandEnv("$HOME/.kube/config")
	}

	var err error
	var restConfig *rest.Config

	if !files.Exists(kubeconfig) {
		if restConfig, err = rest.InClusterConfig(); err != nil {
			return nil, nil, fmt.Errorf("cannot find kubeconfig")
		}
	}

	if restConfig == nil {
		data, err := os.ReadFile(kubeconfig)
		if err != nil {
			return nil, nil, err
		}
		restConfig, err = clientcmd.RESTConfigFromKubeConfig(data)
		if err != nil {
			return nil, nil, err
		}
	}

	client, err := kubernetes.NewForConfig(restConfig)
	return client, restConfig, err
}

// GetClusterName ...
func GetClusterName(config *rest.Config) string {
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return ""
	}
	kubeadmConfig, err := clientset.CoreV1().ConfigMaps("kube-system").Get(context.TODO(), "kubeadm-config", metav1.GetOptions{})
	if err != nil {
		return ""
	}
	clusterConfiguration := make(map[string]interface{})

	if err := yaml.Unmarshal([]byte(kubeadmConfig.Data["ClusterConfiguration"]), &clusterConfiguration); err != nil {
		return ""
	}
	return clusterConfiguration["clusterName"].(string)
}

// GetKubeconfig ...
func GetKubeconfig() string {
	var kubeConfig string
	if os.Getenv("KUBECONFIG") != "" {
		kubeConfig = os.Getenv("KUBECONFIG")
	} else if home := homedir.HomeDir(); home != "" {
		kubeConfig = filepath.Join(home, ".kube", "config")
		if !files.Exists(kubeConfig) {
			kubeConfig = ""
		}
	}
	return kubeConfig
}

func DefaultRestConfig() (*rest.Config, error) {
	kubeConfig := GetKubeconfig()
	return clientcmd.BuildConfigFromFlags("", kubeConfig)
}

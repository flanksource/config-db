package v1

import (
	gocontext "context"
	"encoding/base64"
	"fmt"
	"net/http"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	signerv4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	dutyKubernetes "github.com/flanksource/duty/kubernetes"

	"github.com/flanksource/duty/connection"
	"github.com/flanksource/duty/context"
	"github.com/flanksource/duty/types"
	"k8s.io/client-go/kubernetes"
	rest "k8s.io/client-go/rest"
)

const (
	clusterIDHeader   = "x-k8s-aws-id"
	emptyStringSha256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	v1Prefix          = "k8s-aws-v1."
)

type EKSConnection struct {
	connection.AWSConnection `json:",inline" yaml:",inline"`

	Cluster string `json:"cluster"`
}

type KubernetesConnection struct {
	Kubeconfig *types.EnvVar  `json:"kubeconfig,omitempty"`
	EKS        *EKSConnection `json:"eks,omitempty"`
}

func (t *KubernetesConnection) Populate(ctx context.Context) (kubernetes.Interface, *rest.Config, error) {
	if t.Kubeconfig != nil {
		return dutyKubernetes.NewClientFromPathOrConfig(ctx.Logger, t.Kubeconfig.ValueStatic)
	}

	if t.EKS != nil {
		if err := t.EKS.Populate(ctx); err != nil {
			return nil, nil, err
		}

		awsConfig, err := t.EKS.AWSConnection.Client(ctx)
		if err != nil {
			return nil, nil, err
		}

		eksEndpoint, ca, err := eksClusterDetails(ctx, t.EKS.Cluster, awsConfig)
		if err != nil {
			return nil, nil, err
		}

		token, err := getToken(ctx, t.EKS.Cluster, awsConfig)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to get token for EKS: %w", err)
		}

		restConfig := &rest.Config{
			Host:        eksEndpoint,
			BearerToken: token,
			TLSClientConfig: rest.TLSClientConfig{
				CAData: ca,
			},
		}

		clientset, err := kubernetes.NewForConfig(restConfig)
		if err != nil {
			return nil, nil, err
		}

		return clientset, restConfig, nil
	}

	return nil, nil, nil
}

func eksClusterDetails(ctx gocontext.Context, clusterName string, conf aws.Config) (string, []byte, error) {
	eksClient := eks.NewFromConfig(conf)
	cluster, err := eksClient.DescribeCluster(ctx, &eks.DescribeClusterInput{Name: &clusterName})
	if err != nil {
		return "", nil, fmt.Errorf("unable to get cluster info: %v", err)
	}

	ca, err := base64.URLEncoding.DecodeString(*cluster.Cluster.CertificateAuthority.Data)
	if err != nil {
		return "", nil, fmt.Errorf("unable to presign URL: %v", err)
	}

	return *cluster.Cluster.Endpoint, ca, nil
}

func getToken(ctx gocontext.Context, cluster string, conf aws.Config) (string, error) {
	cred, err := conf.Credentials.Retrieve(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to retrive credentials from aws config: %w", err)
	}

	signedURI, err := getSignedURI(ctx, cluster, cred)
	if err != nil {
		return "", fmt.Errorf("failed to get signed URI: %w", err)
	}

	token := v1Prefix + base64.RawURLEncoding.EncodeToString([]byte(signedURI))
	return token, nil
}

func getSignedURI(ctx gocontext.Context, cluster string, cred aws.Credentials) (string, error) {
	request, err := http.NewRequest(http.MethodGet, "https://sts.amazonaws.com/?Action=GetCallerIdentity&Version=2011-06-15", nil)
	if err != nil {
		return "", err
	}

	request.Header.Add(clusterIDHeader, cluster)
	request.Header.Add("X-Amz-Expires", "0")
	signer := signerv4.NewSigner()
	signedURI, _, err := signer.PresignHTTP(ctx, cred, request, emptyStringSha256, "sts", "us-east-1", time.Now())
	if err != nil {
		return "", err
	}

	return signedURI, nil
}

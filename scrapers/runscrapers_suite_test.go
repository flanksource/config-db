package scrapers

import (
	"os"
	"path/filepath"
	"testing"

	epg "github.com/fergusstrange/embedded-postgres"
	"github.com/flanksource/commons/logger"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db"
	"github.com/flanksource/duty"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	// +kubebuilder:scaffold:imports
)

func TestRunScrapers(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Scrapers Suite")
}

var (
	postgres *epg.EmbeddedPostgres
	gormDB   *gorm.DB
)

const (
	pgUrl  = "postgres://postgres:postgres@localhost:9876/test?sslmode=disable"
	pgPort = 9876
)

var _ = BeforeSuite(func() {
	var err error

	postgres = epg.NewDatabase(epg.DefaultConfig().Database("test").Port(pgPort))
	if err := postgres.Start(); err != nil {
		Fail(err.Error())
	}

	logger.Infof("Started postgres on port %d", pgPort)
	if _, err := duty.NewDB(pgUrl); err != nil {
		Fail(err.Error())
	}
	if err := db.Init(pgUrl); err != nil {
		Fail(err.Error())
	}

	gormDB, err = duty.NewGorm(pgUrl, duty.DefaultGormConfig())
	Expect(err).ToNot(HaveOccurred())

	if err := os.Chdir(".."); err != nil {
		Fail(err.Error())
	}

	setupTestK8s()
})

var _ = AfterSuite(func() {
	if err := testEnv.Stop(); err != nil {
		logger.Errorf("Error stopping test environment: %v", err)
	}

	logger.Infof("Stopping postgres")
	if err := postgres.Stop(); err != nil {
		Fail(err.Error())
	}
})

var (
	cfg            *rest.Config
	k8sClient      client.Client
	testEnv        *envtest.Environment
	kubeConfigPath string
)

func setupTestK8s() {
	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{filepath.Join("chart", "crds")},
	}

	var err error
	cfg, err = testEnv.Start()
	Expect(err).ToNot(HaveOccurred())
	Expect(cfg).ToNot(BeNil())

	err = v1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	// +kubebuilder:scaffold:scheme
	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).ToNot(HaveOccurred())
	Expect(k8sClient).ToNot(BeNil())

	kubeConfigPath, err = createKubeconfigFileForRestConfig(*cfg)
	Expect(err).ToNot(HaveOccurred())
}

func createKubeconfigFileForRestConfig(restConfig rest.Config) (string, error) {
	clusters := make(map[string]*clientcmdapi.Cluster)
	clusters["default-cluster"] = &clientcmdapi.Cluster{
		Server:                   restConfig.Host,
		CertificateAuthorityData: restConfig.CAData,
	}
	contexts := make(map[string]*clientcmdapi.Context)
	contexts["default-context"] = &clientcmdapi.Context{
		Cluster:  "default-cluster",
		AuthInfo: "default-user",
	}
	authinfos := make(map[string]*clientcmdapi.AuthInfo)
	authinfos["default-user"] = &clientcmdapi.AuthInfo{
		ClientCertificateData: restConfig.CertData,
		ClientKeyData:         restConfig.KeyData,
	}
	clientConfig := clientcmdapi.Config{
		Kind:           "Config",
		APIVersion:     "v1",
		Clusters:       clusters,
		Contexts:       contexts,
		CurrentContext: "default-context",
		AuthInfos:      authinfos,
	}
	kubeConfigFile, err := os.CreateTemp("", "kubeconfig-*")
	if err != nil {
		return "", err
	}
	_ = clientcmd.WriteToFile(clientConfig, kubeConfigFile.Name())
	return kubeConfigFile.Name(), nil
}

module github.com/flanksource/config-db

go 1.20

require (
	github.com/Azure/azure-sdk-for-go/sdk/azcore v1.4.0
	github.com/Azure/azure-sdk-for-go/sdk/azidentity v1.2.2
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/advisor/armadvisor v1.1.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appservice/armappservice v1.0.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute v1.0.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerregistry/armcontainerregistry v1.0.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice v1.0.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/dns/armdns v1.1.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork v1.1.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/privatedns/armprivatedns v1.1.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources v1.1.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/storage/armstorage v1.3.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/subscription/armsubscription v1.1.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/trafficmanager/armtrafficmanager v1.0.0
	github.com/Jeffail/gabs/v2 v2.7.0
	github.com/aws/aws-sdk-go-v2 v1.18.0
	github.com/aws/aws-sdk-go-v2/config v1.18.25
	github.com/aws/aws-sdk-go-v2/credentials v1.13.24
	github.com/aws/aws-sdk-go-v2/service/cloudtrail v1.24.4
	github.com/aws/aws-sdk-go-v2/service/configservice v1.30.1
	github.com/aws/aws-sdk-go-v2/service/ec2 v1.92.1
	github.com/aws/aws-sdk-go-v2/service/ecr v1.18.7
	github.com/aws/aws-sdk-go-v2/service/efs v1.19.9
	github.com/aws/aws-sdk-go-v2/service/eks v1.27.8
	github.com/aws/aws-sdk-go-v2/service/elasticloadbalancing v1.15.6
	github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2 v1.19.7
	github.com/aws/aws-sdk-go-v2/service/iam v1.19.8
	github.com/aws/aws-sdk-go-v2/service/rds v1.42.0
	github.com/aws/aws-sdk-go-v2/service/route53 v1.27.5
	github.com/aws/aws-sdk-go-v2/service/s3 v1.33.1
	github.com/aws/aws-sdk-go-v2/service/ssm v1.36.0
	github.com/aws/aws-sdk-go-v2/service/sts v1.19.0
	github.com/aws/aws-sdk-go-v2/service/support v1.14.7
	github.com/aws/smithy-go v1.13.5
	github.com/evanphx/json-patch v5.6.0+incompatible
	github.com/fergusstrange/embedded-postgres v1.21.0
	github.com/flanksource/commons v1.12.0
	github.com/flanksource/duty v1.0.174
	github.com/flanksource/is-healthy v0.0.0-20230713150444-ad2a5ef4bb37
	github.com/flanksource/ketall v1.1.2
	github.com/go-logr/zapr v1.2.4
	github.com/gobwas/glob v0.2.3
	github.com/gomarkdown/markdown v0.0.0-20230322041520-c84983bdbf2a
	github.com/google/cel-go v0.18.0
	github.com/google/uuid v1.3.1
	github.com/hashicorp/go-getter v1.7.2
	github.com/henvic/httpretty v0.1.0
	github.com/hexops/gotextdiff v1.0.3
	github.com/jackc/pgx/v5 v5.4.3
	github.com/labstack/echo/v4 v4.10.2
	github.com/lib/pq v1.10.9
	github.com/ohler55/ojg v1.18.3
	github.com/oklog/ulid/v2 v2.1.0
	github.com/onsi/ginkgo/v2 v2.9.5
	github.com/onsi/gomega v1.27.7
	github.com/pkg/errors v0.9.1
	github.com/robfig/cron/v3 v3.0.1
	github.com/spf13/cobra v1.7.0
	github.com/spf13/pflag v1.0.5
	github.com/stretchr/testify v1.8.4
	github.com/uber/athenadriver v1.1.14
	github.com/xo/dburl v0.13.1
	gopkg.in/flanksource/yaml.v3 v3.2.3
	gopkg.in/yaml.v3 v3.0.1
	gorm.io/gorm v1.25.4
	k8s.io/api v0.28.0
	k8s.io/apimachinery v0.28.0
	k8s.io/client-go v0.28.0
	sigs.k8s.io/controller-runtime v0.15.0
	sigs.k8s.io/yaml v1.3.0
)

require (
	ariga.io/atlas v0.14.0 // indirect
	cloud.google.com/go/compute/metadata v0.2.3 // indirect
	github.com/Azure/azure-sdk-for-go/sdk/internal v1.2.0 // indirect
	github.com/AzureAD/microsoft-authentication-library-for-go v0.9.0 // indirect
	github.com/Masterminds/semver/v3 v3.2.1 // indirect
	github.com/agext/levenshtein v1.2.3 // indirect
	github.com/antlr/antlr4/runtime/Go/antlr/v4 v4.0.0-20230305170008-8188dc5388df // indirect
	github.com/antonmedv/expr v1.15.2 // indirect
	github.com/apparentlymart/go-textseg/v15 v15.0.0 // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/cespare/xxhash/v2 v2.2.0 // indirect
	github.com/evanphx/json-patch/v5 v5.6.0 // indirect
	github.com/flanksource/mapstructure v1.6.0 // indirect
	github.com/fsnotify/fsnotify v1.6.0 // indirect
	github.com/go-openapi/inflect v0.19.0 // indirect
	github.com/go-task/slim-sprig v0.0.0-20230315185526-52ccab3ef572 // indirect
	github.com/golang-jwt/jwt/v4 v4.5.0 // indirect
	github.com/golang-sql/civil v0.0.0-20220223132316-b832511892a9 // indirect
	github.com/golang-sql/sqlexp v0.1.0 // indirect
	github.com/google/gnostic-models v0.6.8 // indirect
	github.com/google/pprof v0.0.0-20230323073829-e72429f035bd // indirect
	github.com/google/s2a-go v0.1.7 // indirect
	github.com/hashicorp/hcl/v2 v2.18.0 // indirect
	github.com/itchyny/gojq v0.12.13 // indirect
	github.com/itchyny/timefmt-go v0.1.5 // indirect
	github.com/jackc/puddle/v2 v2.2.1 // indirect
	github.com/kylelemons/godebug v1.1.0 // indirect
	github.com/liamylian/jsontime/v2 v2.0.0 // indirect
	github.com/matttproud/golang_protobuf_extensions v1.0.4 // indirect
	github.com/mitchellh/go-wordwrap v1.0.1 // indirect
	github.com/pkg/browser v0.0.0-20210911075715-681adbf594b8 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/prometheus/client_golang v1.16.0 // indirect
	github.com/prometheus/client_model v0.4.0 // indirect
	github.com/prometheus/common v0.44.0 // indirect
	github.com/prometheus/procfs v0.11.1 // indirect
	github.com/robertkrimen/otto v0.2.1 // indirect
	github.com/sirupsen/logrus v1.9.3 // indirect
	github.com/stoewer/go-strcase v1.3.0 // indirect
	github.com/tidwall/gjson v1.16.0 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.1 // indirect
	github.com/xi2/xz v0.0.0-20171230120015-48954b6210f8 // indirect
	github.com/yuin/gopher-lua v1.1.0 // indirect
	github.com/zclconf/go-cty v1.14.0 // indirect
	golang.org/x/exp v0.0.0-20230905200255-921286631fa9 // indirect
	golang.org/x/tools v0.13.0 // indirect
	gomodules.xyz/jsonpatch/v2 v2.4.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20230911183012-2d3300fd4832 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20230911183012-2d3300fd4832 // indirect
	gopkg.in/sourcemap.v1 v1.0.5 // indirect
	gorm.io/driver/postgres v1.5.2 // indirect
	k8s.io/component-base v0.28.0 // indirect
	layeh.com/gopher-json v0.0.0-20201124131017-552bb3c4c3bf // indirect
)

require (
	cloud.google.com/go v0.110.8 // indirect
	cloud.google.com/go/compute v1.23.0 // indirect
	cloud.google.com/go/iam v1.1.2 // indirect
	cloud.google.com/go/storage v1.33.0 // indirect
	github.com/DATA-DOG/go-sqlmock v1.5.0 // indirect
	github.com/Masterminds/goutils v1.1.1 // indirect
	github.com/aws/aws-sdk-go v1.45.8 // indirect
	github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream v1.4.10 // indirect
	github.com/aws/aws-sdk-go-v2/feature/ec2/imds v1.13.3 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.1.33 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.4.27 // indirect
	github.com/aws/aws-sdk-go-v2/internal/ini v1.3.34 // indirect
	github.com/aws/aws-sdk-go-v2/internal/v4a v1.0.25 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.9.11 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/checksum v1.1.28 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.9.27 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/s3shared v1.14.2 // indirect
	github.com/aws/aws-sdk-go-v2/service/sso v1.12.10 // indirect
	github.com/aws/aws-sdk-go-v2/service/ssooidc v1.14.10 // indirect
	github.com/bgentry/go-netrc v0.0.0-20140422174119-9fd32a8b3d3d // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/emicklei/go-restful/v3 v3.11.0 // indirect
	github.com/flanksource/gomplate/v3 v3.20.12
	github.com/ghodss/yaml v1.0.0 // indirect
	github.com/go-errors/errors v1.5.0 // indirect
	github.com/go-logr/logr v1.2.4
	github.com/go-openapi/jsonpointer v0.20.0 // indirect
	github.com/go-openapi/jsonreference v0.20.2 // indirect
	github.com/go-openapi/swag v0.22.4 // indirect
	github.com/go-resty/resty/v2 v2.7.0
	github.com/go-sql-driver/mysql v1.7.0
	github.com/gogo/protobuf v1.3.2 // indirect
	github.com/golang-jwt/jwt v3.2.2+incompatible // indirect
	github.com/golang/groupcache v0.0.0-20210331224755-41bb18bfe9da // indirect
	github.com/golang/protobuf v1.5.3 // indirect
	github.com/google/btree v1.1.2 // indirect
	github.com/google/go-cmp v0.5.9
	github.com/google/gofuzz v1.2.0 // indirect
	github.com/google/shlex v0.0.0-20191202100458-e7afc7fbc510 // indirect
	github.com/googleapis/enterprise-certificate-proxy v0.2.5 // indirect
	github.com/googleapis/gax-go/v2 v2.12.0 // indirect
	github.com/gosimple/slug v1.13.1 // indirect
	github.com/gosimple/unidecode v1.0.1 // indirect
	github.com/gregjones/httpcache v0.0.0-20190611155906-901d90724c79 // indirect
	github.com/hairyhenderson/toml v0.4.2-0.20210923231440-40456b8e66cf // indirect
	github.com/hairyhenderson/yaml v0.0.0-20220618171115-2d35fca545ce // indirect
	github.com/hashicorp/go-cleanhttp v0.5.2 // indirect
	github.com/hashicorp/go-safetemp v1.0.0 // indirect
	github.com/hashicorp/go-version v1.6.0 // indirect
	github.com/imdario/mergo v0.3.16 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20221227161230-091c0ba34f0a // indirect
	github.com/jedib0t/go-pretty/v6 v6.4.6 // indirect
	github.com/jinzhu/inflection v1.0.0 // indirect
	github.com/jinzhu/now v1.1.5 // indirect
	github.com/jmespath/go-jmespath v0.4.0 // indirect
	github.com/josharian/intern v1.0.0 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/klauspost/compress v1.16.7 // indirect
	github.com/kr/pretty v0.3.1 // indirect
	github.com/kr/text v0.2.0 // indirect
	github.com/labstack/gommon v0.4.0 // indirect
	github.com/liggitt/tabwriter v0.0.0-20181228230101-89fcab3d43de // indirect
	github.com/magiconair/properties v1.8.7
	github.com/mailru/easyjson v0.7.7 // indirect
	github.com/mattn/go-colorable v0.1.13 // indirect
	github.com/mattn/go-isatty v0.0.19 // indirect
	github.com/mattn/go-runewidth v0.0.14 // indirect
	github.com/microsoft/go-mssqldb v0.21.0
	github.com/mitchellh/go-homedir v1.1.0 // indirect
	github.com/mitchellh/go-testing-interface v1.14.1 // indirect
	github.com/mitchellh/reflectwalk v1.0.2 // indirect
	github.com/moby/spdystream v0.2.0 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.2 // indirect
	github.com/monochromegane/go-gitignore v0.0.0-20200626010858-205db1a8cc00 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/patrickmn/go-cache v2.1.0+incompatible
	github.com/peterbourgon/diskv v2.0.1+incompatible // indirect
	github.com/rivo/uniseg v0.4.4 // indirect
	github.com/rogpeppe/go-internal v1.11.0 // indirect
	github.com/twmb/murmur3 v1.1.6 // indirect
	github.com/uber-go/tally v3.5.3+incompatible // indirect
	github.com/ugorji/go/codec v1.2.11 // indirect
	github.com/ulikunitz/xz v0.5.11 // indirect
	github.com/valyala/bytebufferpool v1.0.0 // indirect
	github.com/valyala/fasttemplate v1.2.2 // indirect
	github.com/xlab/treeprint v1.2.0 // indirect
	github.com/xwb1989/sqlparser v0.0.0-20180606152119-120387863bf2 // indirect
	go.opencensus.io v0.24.0 // indirect
	go.starlark.net v0.0.0-20230831151029-c9e9adf3fde2 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	go.uber.org/zap v1.25.0 // indirect
	golang.org/x/crypto v0.13.0 // indirect
	golang.org/x/net v0.15.0 // indirect
	golang.org/x/oauth2 v0.12.0 // indirect
	golang.org/x/sync v0.3.0 // indirect
	golang.org/x/sys v0.12.0 // indirect
	golang.org/x/term v0.12.0 // indirect
	golang.org/x/text v0.13.0 // indirect
	golang.org/x/time v0.3.0 // indirect
	golang.org/x/xerrors v0.0.0-20220907171357-04be3eba64a2 // indirect
	google.golang.org/api v0.140.0 // indirect
	google.golang.org/appengine v1.6.8 // indirect
	google.golang.org/genproto v0.0.0-20230911183012-2d3300fd4832 // indirect
	google.golang.org/grpc v1.58.0 // indirect
	google.golang.org/protobuf v1.31.0 // indirect
	gopkg.in/inf.v0 v0.9.1 // indirect
	gopkg.in/yaml.v2 v2.4.0 // indirect
	k8s.io/apiextensions-apiserver v0.28.0 // indirect
	k8s.io/cli-runtime v0.28.0 // indirect
	k8s.io/klog/v2 v2.100.1 // indirect
	k8s.io/kube-openapi v0.0.0-20230905202853-d090da108d2f // indirect
	k8s.io/utils v0.0.0-20230726121419-3b25d923346b // indirect
	sigs.k8s.io/json v0.0.0-20221116044647-bc3834ca7abd // indirect
	sigs.k8s.io/kustomize/api v0.14.0 // indirect
	sigs.k8s.io/kustomize/kyaml v0.14.3 // indirect
	sigs.k8s.io/structured-merge-diff/v4 v4.3.0 // indirect
)

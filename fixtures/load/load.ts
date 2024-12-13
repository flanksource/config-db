import { Kubernetes } from 'k6/x/kubernetes';
import k6 from 'k6';
import encoding from 'k6/encoding'
import http from 'k6/http';
import file from 'k6/x/file';

export const options = {
  thresholds: {
    //http_req_failed: ['rate<0.01'],
    http_req_duration: ['p(99)<1000'],
  },
  scenarios: {
    pods: {
      executor: 'shared-iterations',
      vus: 1,
      iterations: 1,
      maxDuration: '2m',
    },
  },
};

const ns = "testns";
let kubernetes;
let token = ""
function proxyGet(pod, path, port = 8080) {
  if (token == "") {
    let sa = {
      apiVersion: "v1",
      kind: "ServiceAccount",
      metadata: {
        name: "proxy-sa",
        namespace: ns
      }
    }
    console.log(`Creating token for sa:${sa.metadata.name}`)
    kubernetes.apply(JSON.stringify(sa))
    let secretToken = {
      apiVersion: "v1",
      kind: "Secret",
      metadata: {
        name: sa.metadata.name,
        namespace: ns,
        annotations: {
          "kubernetes.io/service-account.name": sa.metadata.name
        }
      },
      type: "kubernetes.io/service-account-token"
    }
    kubernetes.apply(JSON.stringify(secretToken))
    let role = {
      apiVersion: "rbac.authorization.k8s.io/v1",
      kind: "Role",
      metadata: {
        name: "proxy-access",
        namespace: ns
      },
      rules: [
        {
          apiGroups: [""],
          resources: ["pods", "pods/proxy"],
          verbs: ["*"]
        }
      ]
    };
    let roleBinding = {
      apiVersion: "rbac.authorization.k8s.io/v1",
      kind: "RoleBinding",
      metadata: {
        name: role.metadata.name,
        namespace: ns
      },
      roleRef: {
        apiGroup: "rbac.authorization.k8s.io",
        kind: "Role",
        name: role.metadata.name
      },
      subjects: [{
        kind: "ServiceAccount",
        name: sa.metadata.name,
        namespace: ns
      }]
    };

    kubernetes.apply(JSON.stringify(role))
    kubernetes.apply(JSON.stringify(roleBinding))
    let secret = kubernetes.get("Secret", secretToken.metadata.name, ns)
    token = encoding.b64decode(secret.data.token, "std", "s")
  }

  const podUrl = `${kubernetes.kubernetes.config.host}/api/v1/namespaces/${pod.metadata.namespace}/pods/${pod.metadata.name}:${port}/proxy/${path}`;
  let response = http.get(podUrl, {
    headers: {
      'Authorization': `Bearer ${token}`
    }
  });
  if (response.status != 200) {
    console.log(`Failed to call ${podUrl}: ${response.status} ${response.body}`)
  }
  return JSON.parse(response.body)
}

const podSpec = {
  apiVersion: "v1",
  kind: "Pod",
  metadata: {
    name: "podinfo",
    namespace: ns
  },
  spec: {
    containers: [
      {
        name: "podinfo",
        image: "stefanprodan/podinfo",
        ports: [
          {
            containerPort: 9898,
            name: "http",
            protocol: "TCP"
          }
        ]
      }
    ]
  }
}

const namespaceSpec = {
  apiVersion: "v1",
  kind: "Namespace",
  metadata: {
    name: ns,
  }
}

const deploymentSpec = {
  apiVersion: "apps/v1",
  kind: "Deployment",
  metadata: {
    name: "nginx",
    namespace: ns,
  },
  spec: {
    replicas: 3,
    selector: {
      matchLabels: {
        app: "nginx"
      }
    },
    template: {
      metadata: {
        labels: {
          app: "nginx"
        }
      },
      spec: {
        containers: [
          {
            name: "nginx",
            image: "nginx:alpine",
          }
        ]
      }
    }
  }
};

let count = 10
export default function() {
  kubernetes = new Kubernetes();
  console.log(`Connected to ${kubernetes.kubernetes.config.host}`)


  kubernetes.apply(JSON.stringify(namespaceSpec))

  // Create 200 pods
  for (let i = 0; i < count; i++) {
    const podName = `podinfo-${i}`;
    const newPodSpec = JSON.parse(JSON.stringify(podSpec));
    newPodSpec.metadata.name = podName;
    kubernetes.apply(JSON.stringify(newPodSpec))
    console.log(`Created pod: ${podName}`)

  }

  // Wait for pods to be ready
  let allPodsReady = false;
  while (!allPodsReady) {
    const pods = kubernetes.list("Pod", ns);
    allPodsReady = pods.length === count && pods.every(pod => pod.status.phase === 'Running');
    if (!allPodsReady) {
      console.log(`Waiting for ${pods.length}/${count} pods to be ready...`);
      k6.sleep(5);
    }
  }

  // Crash 20 random pods over 1 minute
  const interval = 3; // seconds between crashes
  const podsToCrash = 2;

  for (let i = 0; i < podsToCrash; i++) {
    const randomPodIndex = Math.floor(Math.random() * count);
    const podName = `podinfo-${randomPodIndex}`;


    // Write this to file
    console.log(`Crashing pod: ${podName} at  ${new Date().toLocaleString()}`);
    file.appendString('log.txt', `${podName},crash,${new Date().toISOString()}\n`)

    try {
      let response = proxyGet(kubernetes.get("Pod", podName, ns), "panic", 9898)
      console.log(`Failed to crash pod ${podName}`)
    } catch (error) {
    }


    if (i < podsToCrash - 1) {
      k6.sleep(interval);
    }
  }

  // List all pods to verify
  const pods = kubernetes.list("Pod", ns);
  console.log(`${pods.length} Pods found:`);
  pods.map(function(pod) {
    console.log(`  ${pod.metadata.name} ${pod.status.phase}: restarts=${pod.status.containerStatuses[0].restartCount}`);
  });

  // Create deployment to scale up and down
  console.log(`Creating nginx deployment`);
  kubernetes.apply(JSON.stringify(deploymentSpec))

  k6.sleep(5);
  const deployment1Replica = JSON.parse(JSON.stringify(deploymentSpec));
  deployment1Replica.spec.replicas = 1;
  file.appendString('log.txt', `${deploymentSpec.metadata.name},scaledown,${new Date().toISOString()}\n`)
  kubernetes.apply(JSON.stringify(deployment1Replica))
}

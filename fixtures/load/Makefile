K6_VERSION ?= v1.6.1
XK6_VERSION ?= v1.3.5
XK6_KUBERNETES_VERSION ?= v0.10.3
XK6_FILE_VERSION ?= v1.6.1

.PHONY:
k6:
	go install go.k6.io/xk6/cmd/xk6@$(XK6_VERSION)
	xk6 build $(K6_VERSION) \
		--with github.com/grafana/xk6-kubernetes@$(XK6_KUBERNETES_VERSION) \
		--with github.com/avitalique/xk6-file@$(XK6_FILE_VERSION)

.PHONY:
run: k6
	kubectl delete pods --all -n testns
	 ./k6 run load.ts --insecure-skip-tls-verify

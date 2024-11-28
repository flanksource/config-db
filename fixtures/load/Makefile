.PHONY:
k6:
	go install go.k6.io/xk6/cmd/xk6@latest
	xk6 build --with github.com/grafana/xk6-kubernetes

.PHONY:
run: k6
	 ./k6 run load.ts --insecure-skip-tls-verify

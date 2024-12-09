.PHONY:
k6:
	go install go.k6.io/xk6/cmd/xk6@latest
	xk6 build --with github.com/grafana/xk6-kubernetes --with github.com/avitalique/xk6-file@latest

.PHONY:
run: k6
	kubectl delete ns testns || true
	kubectl create ns testns || true
	 ./k6 run load.ts --insecure-skip-tls-verify

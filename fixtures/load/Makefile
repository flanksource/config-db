.PHONY:
k6:
	go install go.k6.io/xk6/cmd/xk6@latest
	xk6 build v0.45.1 --with github.com/grafana/xk6-kubernetes@v0.10.0 --with github.com/avitalique/xk6-file@latest

.PHONY:
run: k6
	kubectl delete pods --all -n testns
	 ./k6 run load.ts --insecure-skip-tls-verify

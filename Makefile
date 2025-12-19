.PHONY: build run test docker-build docker-push deploy clean

BINARY_NAME=node-lifecycle-controller
IMAGE_NAME=node-lifecycle-controller
IMAGE_TAG=latest

build:
	go build -o bin/$(BINARY_NAME) ./cmd/controller

run:
	go run ./cmd/controller --kubeconfig=$(HOME)/.kube/config

test:
	go test -v ./...

docker-build:
	docker build -t $(IMAGE_NAME):$(IMAGE_TAG) .

docker-push:
	docker push $(IMAGE_NAME):$(IMAGE_TAG)

deploy:
	kubectl apply -f deploy/rbac.yaml
	kubectl apply -f deploy/configmap.yaml
	kubectl apply -f deploy/deployment.yaml

undeploy:
	kubectl delete -f deploy/deployment.yaml
	kubectl delete -f deploy/configmap.yaml
	kubectl delete -f deploy/rbac.yaml

clean:
	rm -rf bin/

lint:
	golangci-lint run ./...

fmt:
	go fmt ./...

mod:
	go mod tidy
	go mod verify

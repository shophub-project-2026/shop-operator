.PHONY: help build test test-integration clean docker-build docker-push lint fmt vet

# Variables
OPERATOR_NAME := shop-operator
IMG_NAME := devops/$(OPERATOR_NAME)
VERSION ?= $(shell git describe --tags --always --dirty)
IMG_TAG := $(IMG_NAME):$(VERSION)

help: ## Display this help screen
	@grep -h -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-30s\033[0m %s\n", $$1, $$2}'

build: ## Build operator binary
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o bin/manager main.go

build-local: ## Build operator binary for local development
	go build -o bin/manager-local main.go

test: ## Run unit tests
	go test -v -race -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

test-integration: ## Run integration tests with TestContainers
	go test -v -race -tags=integration ./test/integration/...

fmt: ## Format code
	go fmt ./...

vet: ## Run go vet
	go vet ./...

lint: ## Run linter
	golangci-lint run ./...

clean: ## Clean build artifacts and test files
	rm -rf bin/
	rm -f coverage.out coverage.html

docker-build: ## Build Docker image
	docker build -t $(IMG_TAG) -f Dockerfile .

docker-push: ## Push Docker image to registry
	docker push $(IMG_TAG)

docker-build-push: docker-build docker-push ## Build and push Docker image

generate: ## Generate CRDs and manifests
	controller-gen object:headerFile="hack/boilerplate.go.txt" paths="./..."
	controller-gen crd paths="./api/..." output:crd:artifacts:config=config/crd/bases
	controller-gen rbac:roleName=shop-operator paths="./pkg/controller/..."

manager: build ## Alias for build target

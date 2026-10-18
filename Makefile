# Build, test and deploy auracle.
#
#   make helm      build and push the image, then install or upgrade the release
#   make secret    create or update the Discord token Secret (needs DISCORD_TOKEN)
#
# Override any variable on the command line, e.g.
#   make helm NAMESPACE=bots OLLAMA_URL=http://ollama.ollama.svc:11434

IMAGE     ?= ghcr.io/aufishgamelabs/auracle
PLATFORMS ?= linux/amd64,linux/arm64
RELEASE   ?= auracle
NAMESPACE ?= auracle
CHART     ?= charts/auracle

# Name of the Secret holding the Discord bot token, and the key within it.
DISCORD_SECRET ?= auracle-discord
DISCORD_KEY    ?= token

# Optional settings. When empty, the chart's values (or VALUES) are used.
OLLAMA_URL   ?= http://host.docker.internal:11434
OLLAMA_MODEL ?= gpt-oss:120b
# Directory on the node holding the docs. Empty uses a PersistentVolumeClaim
# instead (see the chart README).
DOCS_PATH    ?= $(HOME)/llms/auracle
# Optional values file passed to helm, e.g. VALUES=my-values.yaml.
VALUES       ?=

# The image tag is a hash of the Go sources, so it only changes when the code
# does, and a changed tag is what makes `helm upgrade` roll out a new pod.
SOURCES := go.mod go.sum Dockerfile $(shell find cmd internal -name '*.go' -not -name '*_test.go' | sort)
ifeq ($(origin TAG),undefined)
TAG := $(shell cat $(SOURCES) | shasum -a 256 | cut -c1-12)
endif

HELM_ARGS := --namespace $(NAMESPACE) --create-namespace \
	--set image.repository=$(IMAGE) \
	--set image.tag=$(TAG) \
	--set discord.existingSecret=$(DISCORD_SECRET) \
	--set discord.tokenKey=$(DISCORD_KEY) \
	$(if $(VALUES),--values $(VALUES)) \
	$(if $(DOCS_PATH),--set-string docs.hostPath.path=$(DOCS_PATH)) \
	$(if $(OLLAMA_URL),--set ollama.url=$(OLLAMA_URL)) \
	$(if $(OLLAMA_MODEL),--set ollama.model=$(OLLAMA_MODEL))

.PHONY: build test lint image push helm secret check-secret helm-template

build: ## Build the bot and pdf2md into ./bin
	go build -o bin/ ./cmd/...

test: ## Run vet and tests
	go vet ./...
	go test ./...

lint: ## Lint the Helm chart
	helm lint $(CHART) --set discord.existingSecret=$(DISCORD_SECRET)

image: ## Build the image for the local machine
	docker build -t $(IMAGE):$(TAG) .

push: ## Build the image for the cluster's platforms and push it
	docker buildx build --platform $(PLATFORMS) -t $(IMAGE):$(TAG) --push .

helm: push check-secret ## Install or upgrade the release in Kubernetes
	helm upgrade --install $(RELEASE) $(CHART) $(HELM_ARGS) --wait --timeout 5m
	@echo "Deployed $(IMAGE):$(TAG) as $(RELEASE) in namespace $(NAMESPACE)."

helm-template: ## Print the manifests `make helm` would apply
	helm template $(RELEASE) $(CHART) $(HELM_ARGS)

check-secret:
	@kubectl get namespace $(NAMESPACE) >/dev/null 2>&1 || kubectl create namespace $(NAMESPACE)
	@kubectl get secret $(DISCORD_SECRET) --namespace $(NAMESPACE) >/dev/null 2>&1 || { \
		echo "Secret $(DISCORD_SECRET) not found in namespace $(NAMESPACE)."; \
		echo "Create it with: DISCORD_TOKEN=... make secret"; \
		exit 1; }

secret: ## Create or update the Discord token Secret from $DISCORD_TOKEN
	@test -n "$$DISCORD_TOKEN" || { echo "Set DISCORD_TOKEN, e.g. DISCORD_TOKEN=... make secret"; exit 1; }
	@kubectl get namespace $(NAMESPACE) >/dev/null 2>&1 || kubectl create namespace $(NAMESPACE)
	@kubectl create secret generic $(DISCORD_SECRET) --namespace $(NAMESPACE) \
		--from-literal=$(DISCORD_KEY)="$$DISCORD_TOKEN" --dry-run=client -o yaml | kubectl apply -f -

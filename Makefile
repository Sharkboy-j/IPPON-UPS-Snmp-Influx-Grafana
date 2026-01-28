MAKEFLAGS += --no-print-directory

REGISTRY ?= ghcr.io
DOCKERHUB_USER ?= sharkboy-j
IMAGE_NAME ?= ippon-ups-snmp-influx-grafana
IMAGE ?= $(REGISTRY)/$(DOCKERHUB_USER)/$(IMAGE_NAME)
TAG ?= latest
PLATFORMS ?= linux/arm64

BUILDER ?= ippon-builder
DOCKER ?= docker
DOCKER_COMPOSE ?= docker-compose

.PHONY: pull
pull:
	git pull

.PHONY: kill
kill:
	$(DOCKER) kill ippon

.PHONY: start
start:
	$(DOCKER_COMPOSE) up -d

.PHONY: logs
logs:
	$(DOCKER) logs -f --tail 10 ippon

.PHONY: buildx-init
buildx-init:
	@$(DOCKER) buildx inspect $(BUILDER) >/dev/null 2>&1; \
	if [ $$? -ne 0 ]; then $(DOCKER) buildx create --name $(BUILDER) --use; else $(DOCKER) buildx use $(BUILDER); fi

.PHONY: bin-arm64
bin-arm64:
	go mod download
	GOOS=linux GOARCH=arm64 go build -o snmp_ex .
	chmod +x snmp_ex

.PHONY: image-arm64
image-arm64:
	@$(MAKE) buildx-init
	@$(MAKE) bin-arm64
	$(DOCKER) buildx build --platform linux/arm64 -t $(IMAGE):$(TAG) --no-cache --load .

.PHONY: push-arm64
push-arm64:
	@$(MAKE) image-arm64
	$(DOCKER) push $(IMAGE):$(TAG)

.PHONY: push
push: push-arm64

.PHONY: build
build:
	@$(MAKE) pull
	go mod download
	GOARCH=arm GOARM=7 GOOS=linux go build -o snmp_ex .
	chmod +x snmp_ex
	$(DOCKER) kill ippon
	$(DOCKER) rm ippon
	$(DOCKER) build -t ippon --no-cache .
	$(DOCKER_COMPOSE) up -d
	$(DOCKER) logs -f --tail 10 ippon

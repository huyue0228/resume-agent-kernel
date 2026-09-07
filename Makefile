KERNEL_VERSION ?= dev
IMAGE ?= smart-resume-filter-agent-kernel:$(KERNEL_VERSION)
PLATFORM ?= linux/amd64
.PHONY: check build image
check:
	go test -race ./...
	go vet ./...
build:
	mkdir -p dist
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.build=$(KERNEL_VERSION)" -o dist/agent-kernel ./cmd/agent-kernel
image:
	docker build --platform $(PLATFORM) --build-arg KERNEL_VERSION=$(KERNEL_VERSION) -t $(IMAGE) .

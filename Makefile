PYTHON ?= python3
KERNEL_VERSION ?= dev
IMAGE ?= smart-resume-filter-agent-kernel:$(KERNEL_VERSION)
PLATFORM ?= linux/amd64
PUSH ?=
.PHONY: check build image package
check:
	go test -race ./...
	go vet ./...
build:
	mkdir -p dist
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.build=$(KERNEL_VERSION)" -o dist/agent-kernel ./cmd/agent-kernel
image:
	$(PYTHON) tools/image.py --version "$(KERNEL_VERSION)" --image "$(IMAGE)" --platform "$(PLATFORM)" $(PUSH)

package:
	$(PYTHON) tools/package.py --version "$(KERNEL_VERSION)"

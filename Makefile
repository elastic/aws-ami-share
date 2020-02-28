NAME ?= aws-ami-share
VERSION ?= 0.1.0
DEFAULT_LDFLAGS ?= -X main.version=$(VERSION) -X main.commit=$(shell git rev-parse HEAD) -X main.date=$(shell date +'%d/%m/%Y')

define HELP
/////////////////////////
/\t$(REPO) Makefile \t/
/////////////////////////

## Build target

- build:                  It will build $(NAME) for the current architecture in bin/$(REPO).
- install:                It will install $(NAME) in the current system (by default in $(GOPATH)/bin/$(REPO)).
- lint:                   Runs the linters.
endef
export HELP

help:
	@echo "$$HELP"

.PHONY: lint
lint: build
	@gofmt -d -e -s .

build:
	@go build -o bin/$(NAME) -ldflags="$(DEFAULT_LDFLAGS)"

install:
	@go install ./...

.PHONY: help build install lint
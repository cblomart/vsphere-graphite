GOVERSION=$(shell go version)
ifndef COMMIT
	COMMIT=$(shell git log -1 --pretty=format:"%h")
endif
ifdef TRAVIS_TAG
	TAG=$(TRAVIS_TAG)
endif
ifdef DRONE_TAG
	TAG=$(DRONE_TAG)
endif
ifndef TAG
	TAG=$(shell git tag -l --points-at HEAD)
endif
GOOS=$(word 1,$(subst /, ,$(lastword $(GOVERSION))))
GOARCH=$(word 2,$(subst /, ,$(lastword $(GOVERSION))))
RELEASE_DIR=releases
SRC_FILES=$(wildcard *.go)
EXTRA_FLAGS=-X main.commit=$(COMMIT) -X main.tag=$(TAG)
MUSL_BUILD_FLAGS=-ldflags '-linkmode external -s -w -extldflags "-static" $(EXTRA_FLAGS)' -a
BUILD_FLAGS=-ldflags '$(EXTRA_FLAGS) -s' -a
MUSL_CC=musl-gcc
MUSL_CCGLAGS="-static"
STATICCHECK := $(shell command -v staticcheck 2> /dev/null)
INEFFASSIGN := $(shell command -v ineffassign 2> /dev/null)
GOLINT := $(shell command -v golint 2> /dev/null)
GOSEC := $(shell command -v gosec 2> /dev/null)
GITVERSION := $(shell command -v git-version 2> /dev/null)

godeps:
ifndef STATICCHECK
	go get -u honnef.co/go/tools/cmd/staticcheck
endif
ifndef GOLINT
	go get -u golang.org/x/lint/golint
endif
ifndef INEFFASSIGN
	go get -u github.com/gordonklaus/ineffassign
endif
ifndef GOSEC
	go get -u github.com/securego/gosec/cmd/gosec/...
endif
ifndef GITVERSION
	go get -u github.com/cblomart/git-version
endif
	#go get -u golang.org/x/sys/windows/registry
	go get -u github.com/takama/daemon
	go get -u golang.org/x/net/context
	go get -u github.com/vmware/govmomi
	go get -u github.com/marpaia/graphite-golang
	go get -u github.com/influxdata/influxdb1-client/v2
	go get -u github.com/pquerna/ffjson/fflib/v1
	go get -u code.cloudfoundry.org/bytefmt
	go get -u github.com/pquerna/ffjson
	#go get -u github.com/olivere/elastic
	go get -u gopkg.in/olivere/elastic.v5
	go get -u github.com/prometheus/client_golang/prometheus
	go get -u github.com/fluent/fluent-logger-golang/fluent	
	go get -u github.com/valyala/fasthttp

deps:
	@$(MAKE) godeps
	go generate ./...

build-windows-amd64:
	@$(MAKE) build GOOS=windows GOARCH=amd64 SUFFIX=.exe

upx-windows-amd64:
	@$(MAKE) upx GOOS=windows GOARCH=amd64 SUFFIX=.exe

dist-windows-amd64:
	@$(MAKE) dist GOOS=windows GOARCH=amd64 SUFFIX=.exe

build-linux-amd64:
	@$(MAKE) build GOOS=linux GOARCH=amd64

upx-linux-amd64:
	@$(MAKE) upx GOOS=linux GOARCH=amd64

dist-linux-amd64:
	@$(MAKE) dist GOOS=linux GOARCH=amd64

build-darwin-amd64:
	@$(MAKE) build GOOS=darwin GOARCH=amd64

upx-darwin-amd64:
	@$(MAKE) upx GOOS=darwin GOARCH=amd64

dist-darwin-amd64:
	@$(MAKE) dist GOOS=darwin GOARCH=amd64

build-linux-arm:
	@$(MAKE) build GOOS=linux GOARCH=arm GOARM=5

upx-linux-arm:
	@$(MAKE) upx GOOS=linux GOARCH=arm

dist-linux-arm:
	@$(MAKE) dist GOOS=linux GOARCH=arm GOARM=5

docker-build: $(RELEASE_DIR)/$(GOOS)/$(GOARCH)/vsphere-graphite
	cp $(RELEASE_DIR)/$(GOOS)/$(GOARCH)/* docker/main/
	mkdir -p docker/main/etc
	cp vsphere-graphite-example.json docker/main/etc/vsphere-graphite.json
	docker build -f docker/main/Dockerfile -t cblomart/$(PREFIX)vsphere-graphite docker/main
	docker tag cblomart/$(PREFIX)vsphere-graphite cblomart/$(PREFIX)vsphere-graphite:$(COMMIT)
	if [ ! -z "$(TAG)" ];then\
		docker tag cblomart/$(PREFIX)vsphere-graphite cblomart/$(PREFIX)vsphere-graphite:$(TAG);\
		docker tag cblomart/$(PREFIX)vsphere-graphite cblomart/$(PREFIX)vsphere-graphite:latest;\
	fi

docker-push:
	docker push cblomart/$(PREFIX)vsphere-graphite:$(COMMIT)
	if [ ! -z "$(TAG)"];then\
		docker push cblomart/$(PREFIX)vsphere-graphite:$(TAG);\
		docker push cblomart/$(PREFIX)vsphere-graphite:latest;\
	fi


docker-linux-amd64:
	@$(MAKE) docker-build GOOS=linux GOARCH=amd64

docker-linux-arm:
	@$(MAKE) docker-build GOOS=linux GOARCH=arm PREFIX=rpi-

docker-darwin-amd64: ;

docker-windows-amd64: ;

push-linux-amd64:
	@$(MAKE) docker-push

push-linux-arm:
	@$(MAKE) docker-push PREFIX=rpi-

checks:
	staticcheck -f stylish ./...
	gofmt -s -d .
	go vet ./...
	golint ./...
	ineffassign ./
	gosec ./...

$(RELEASE_DIR)/$(GOOS)/$(GOARCH)/vsphere-graphite$(SUFFIX): $(SRC_FILES)
	if [ "$(GOOS)-$(GOARCH)" = "linux-amd64" ] && [ ! -f /etc/alpine-release ] && [ ! -f /etc/arch-release ]; then\
		echo "Using musl";\
		CC=$(MUSL_CC) CCGLAGS=$(MUSL_CCGLAGS) go build $(MUSL_BUILD_FLAGS) -o $(RELEASE_DIR)/linux/amd64//vsphere-graphite .;\
	else\
		go build $(BUILD_FLAGS) -o $(RELEASE_DIR)/$(GOOS)/$(GOARCH)/vsphere-graphite$(SUFFIX) .;\
	fi
	cp vsphere-graphite-example.json $(RELEASE_DIR)/$(GOOS)/$(GOARCH)/vsphere-graphite.json

$(RELEASE_DIR)/vsphere-graphite_$(GOOS)_$(GOARCH).tgz: $(RELEASE_DIR)/$(GOOS)/$(GOARCH)/vsphere-graphite$(SUFFIX)
	cd $(RELEASE_DIR)/$(GOOS)/$(GOARCH); tar czf /tmp/vsphere-graphite_$(GOOS)_$(GOARCH).tgz ./vsphere-graphite$(SUFFIX) ./vsphere-graphite.json

upx: $(RELEASE_DIR)/$(GOOS)/$(GOARCH)/vsphere-graphite$(SUFFIX)
	upx -qq --best $(RELEASE_DIR)/$(GOOS)/$(GOARCH)/vsphere-graphite$(SUFFIX)

dist: $(RELEASE_DIR)/vsphere-graphite_$(GOOS)_$(GOARCH).tgz

build: $(RELEASE_DIR)/$(GOOS)/$(GOARCH)/vsphere-graphite$(SUFFIX)

clean:
	rm -f backend/thininfluxclient/thininfluxclient_ffjson.go
	rm -f version.go
	rm -rf $(RELEASE_DIR)

dev:
	git pull
	@$(MAKE) clean
	@$(MAKE) build-linux-amd64

all:
	@$(MAKE) dist-windows-amd64
	@$(MAKE) dist-linux-amd64
	@$(MAKE) dist-darwin-amd64
	@$(MAKE) dist-linux-arm

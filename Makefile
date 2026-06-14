APP_NAME := xray-web-manager
VERSION ?= v1.0.0
LDFLAGS := -ldflags="-s -w"
ASSETS := config.yaml README.md

RELEASE_DIR := release
BUILD_TEMP_DIR := build_temp

.PHONY: all build run test race frontend package clean deps

all: build

deps:
	@echo "Checking build dependencies..."
	@command -v go >/dev/null 2>&1 || { echo "ERROR: go not found, please install Go (https://go.dev/dl/)"; exit 1; }
	@command -v node >/dev/null 2>&1 || { echo "ERROR: node not found, please install Node.js (https://nodejs.org/)"; exit 1; }
	@command -v npm >/dev/null 2>&1 || { echo "ERROR: npm not found, please install Node.js"; exit 1; }
	@pkg-config --exists libsystemd 2>/dev/null || { \
		echo "libsystemd-dev not found, attempting to install..."; \
		if command -v apt-get >/dev/null 2>&1; then \
			sudo apt-get install -y libsystemd-dev; \
		elif command -v dnf >/dev/null 2>&1; then \
			sudo dnf install -y systemd-devel; \
		elif command -v yum >/dev/null 2>&1; then \
			sudo yum install -y systemd-devel; \
		elif command -v pacman >/dev/null 2>&1; then \
			sudo pacman -S --noconfirm systemd; \
		else \
			echo "ERROR: Cannot auto-install libsystemd-dev, please install manually:"; \
			echo "  Debian/Ubuntu: sudo apt-get install libsystemd-dev"; \
			echo "  Fedora/RHEL:   sudo dnf install systemd-devel"; \
			echo "  Arch:          sudo pacman -S systemd"; \
			exit 1; \
		fi; \
	}
	@echo "All dependencies OK."

build: deps frontend
	@echo "Building for current OS/Arch..."
	@mkdir -p bin/
	go build $(LDFLAGS) -o bin/$(APP_NAME) .

run:
	@echo "Running in dev mode (using external frontend/ and config.yaml)..."
	go run . -dev -c config.yaml

test:
	go test ./... -v

race:
	go test ./... -v -race

frontend:
	@echo "Building production CSS..."
	@if [ ! -d frontend/node_modules ]; then \
		(cd frontend && npm install); \
	fi
	(cd frontend && npx @tailwindcss/cli -i ./tailwind.css -o ./style.css --config tailwind.config.js --minify)

package: frontend \
	$(RELEASE_DIR)/$(APP_NAME)-$(VERSION)-linux-amd64.tar.gz \
	$(RELEASE_DIR)/$(APP_NAME)-$(VERSION)-linux-386.tar.gz \
	$(RELEASE_DIR)/$(APP_NAME)-$(VERSION)-windows-amd64.zip \
	$(RELEASE_DIR)/$(APP_NAME)-$(VERSION)-windows-386.zip
	@echo "All packages built in $(RELEASE_DIR)/"
	@echo "Cleaning up temporary files..."
	@rm -rf $(BUILD_TEMP_DIR)
	@ls -l $(RELEASE_DIR)

$(RELEASE_DIR)/$(APP_NAME)-$(VERSION)-linux-%.tar.gz:
	@mkdir -p $(RELEASE_DIR) $(BUILD_TEMP_DIR)
	@echo "Packaging linux/$*..."
	$(eval GOOS := linux)
	$(eval GOARCH := $*)
	$(eval PKG_NAME := $(APP_NAME)-$(VERSION)-$(GOOS)-$(GOARCH))
	$(eval STAGING_DIR := $(BUILD_TEMP_DIR)/$(PKG_NAME))
	$(eval CGO := $(if $(filter 386,$*),0,1))
	$(eval BTAGS := $(if $(filter 386,$*),-tags nojournal,))
	@mkdir -p $(STAGING_DIR)
	GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=$(CGO) go build $(LDFLAGS) $(BTAGS) -o $(STAGING_DIR)/$(APP_NAME) .
	cp $(ASSETS) $(STAGING_DIR)/
	chmod +x $(STAGING_DIR)/$(APP_NAME)
	@if command -v upx >/dev/null 2>&1; then \
		echo "  -> 压缩 (UPX)..."; \
		upx -9 $(STAGING_DIR)/$(APP_NAME); \
	else \
		echo "  -> UPX 未安装，跳过压缩"; \
	fi
	tar -C $(BUILD_TEMP_DIR) -czvf $@ $(PKG_NAME)

$(RELEASE_DIR)/$(APP_NAME)-$(VERSION)-windows-%.zip:
	@mkdir -p $(RELEASE_DIR) $(BUILD_TEMP_DIR)
	@echo "Packaging windows/$*..."
	$(eval GOOS := windows)
	$(eval GOARCH := $*)
	$(eval EXE_NAME := $(APP_NAME).exe)
	$(eval PKG_NAME := $(APP_NAME)-$(VERSION)-$(GOOS)-$(GOARCH))
	$(eval STAGING_DIR := $(BUILD_TEMP_DIR)/$(PKG_NAME))
	@mkdir -p $(STAGING_DIR)
	GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=0 go build $(LDFLAGS) -o $(STAGING_DIR)/$(EXE_NAME) .
	cp $(ASSETS) $(STAGING_DIR)/
	@if command -v upx >/dev/null 2>&1; then \
		echo "  -> 压缩 (UPX)..."; \
		upx -9 $(STAGING_DIR)/$(EXE_NAME); \
	else \
		echo "  -> UPX 未安装，跳过压缩"; \
	fi
	(cd $(BUILD_TEMP_DIR) && zip -r ../$@ $(PKG_NAME))

clean:
	@echo "Cleaning up..."
	rm -rf $(RELEASE_DIR) $(BUILD_TEMP_DIR) bin/
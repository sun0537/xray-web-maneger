# =============================================================================
# Makefile for Xray Web Manager
# =============================================================================

# --- 变量定义 ---

# APP_NAME: 编译后的可执行文件名称
APP_NAME := xray-web-manager

# VERSION: 默认版本号。可以在命令行中覆盖
# 示例: make package VERSION=v1.0.1
VERSION ?= v1.0.0

# LDFLAGS: Go 编译优化标志 (-s -w 移除调试信息，减小体积)
LDFLAGS := -ldflags="-s -w"

# ASSETS: 要复制到压缩包中的额外文件
ASSETS := config.yaml README.md

# --- 目录 ---
RELEASE_DIR := release
BUILD_TEMP_DIR := build_temp

# --- 目标 (Targets) ---

# .PHONY: 声明这些不是真实的文件名
.PHONY: all build run test race frontend package clean

# 'make all' 或 'make' (默认) 将运行 'package' 目标
all: package

# 'make build': 编译一个适用于您当前系统的快速测试版本
build: frontend
	@echo "Building for current OS/Arch..."
	@mkdir -p bin/
	go build $(LDFLAGS) -o bin/$(APP_NAME) .

run:
	@echo "Running in dev mode (using external frontend/ and config.yaml)..."
	go run . -dev -c config.yaml

# 'make test': 运行所有单元测试 (config, middleware, sse, server)
test:
	go test ./... -v

# 'make race': 运行所有单元测试并开启竞态检测 (用于 sse 等)
race:
	go test ./... -v -race

# 'make frontend': 编译生产环境的 CSS
# ( frontend/ 目录 包含 package.json 和 tailwind.config.js)
frontend:
	@echo "Building production CSS..."
	(cd frontend && npm install)
	(cd frontend && npx @tailwindcss/cli -i ./tailwind.css -o ./style.css --config tailwind.config.js)

# 'make package': 依赖所有 4 个平台的压缩包
package: frontend \
	$(RELEASE_DIR)/$(APP_NAME)-$(VERSION)-linux-amd64.tar.gz \
	$(RELEASE_DIR)/$(APP_NAME)-$(VERSION)-linux-386.tar.gz \
	$(RELEASE_DIR)/$(APP_NAME)-$(VERSION)-windows-amd64.zip \
	$(RELEASE_DIR)/$(APP_NAME)-$(VERSION)-windows-386.zip
	@echo "All packages built in $(RELEASE_DIR)/"
	@echo "Cleaning up temporary files..."
	@rm -rf $(BUILD_TEMP_DIR)
	@ls -l $(RELEASE_DIR)

# --- 编译和打包规则 ---

# 规则 1: 如何制作 Linux 的 .tar.gz 包
# (匹配: ...-linux-amd64.tar.gz, ...-linux-386.tar.gz)
# 依赖 tar zip upx-ucl
$(RELEASE_DIR)/$(APP_NAME)-$(VERSION)-linux-%.tar.gz:
	@mkdir -p $(RELEASE_DIR) $(BUILD_TEMP_DIR)
	@echo "Packaging linux/$*..."
	$(eval GOOS := linux)
	$(eval GOARCH := $*)
	$(eval PKG_NAME := $(APP_NAME)-$(VERSION)-$(GOOS)-$(GOARCH))
	$(eval STAGING_DIR := $(BUILD_TEMP_DIR)/$(PKG_NAME))

	@mkdir -p $(STAGING_DIR)
	GOOS=$(GOOS) GOARCH=$(GOARCH) go build $(LDFLAGS) -o $(STAGING_DIR)/$(APP_NAME) .
	cp $(ASSETS) $(STAGING_DIR)/
	chmod +x $(STAGING_DIR)/$(APP_NAME)
	@echo "  -> 压缩 (UPX)..."
	upx -9 $(STAGING_DIR)/$(APP_NAME)
	tar -C $(BUILD_TEMP_DIR) -czvf $@ $(PKG_NAME)

# S规则 2: 如何制作 Windows 的 .zip 包
# (匹配: ...-windows-amd64.zip, ...-windows-386.zip)
$(RELEASE_DIR)/$(APP_NAME)-$(VERSION)-windows-%.zip:
	@mkdir -p $(RELEASE_DIR) $(BUILD_TEMP_DIR)
	@echo "Packaging windows/$*..."
	$(eval GOOS := windows)
	$(eval GOARCH := $*)
	$(eval EXE_NAME := $(APP_NAME).exe)
	$(eval PKG_NAME := $(APP_NAME)-$(VERSION)-$(GOOS)-$(GOARCH))
	$(eval STAGING_DIR := $(BUILD_TEMP_DIR)/$(PKG_NAME))

	@mkdir -p $(STAGING_DIR)
	GOOS=$(GOOS) GOARCH=$(GOARCH) go build $(LDFLAGS) -o $(STAGING_DIR)/$(EXE_NAME) .
	cp $(ASSETS) $(STAGING_DIR)/
	(cd $(BUILD_TEMP_DIR) && zip -r ../$@ $(PKG_NAME))

# 'make clean': 清理所有编译生成物
clean:
	@echo "Cleaning up..."
	rm -rf $(RELEASE_DIR) $(BUILD_TEMP_DIR) bin/

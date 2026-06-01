APP     := ggt
OUTPUT  := dist
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -ldflags "-s -w -X main.version=$(VERSION)"
PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64

.PHONY: all build build-all clean

all: build

# 构建当前平台的二进制
build:
	go build $(LDFLAGS) -o $(OUTPUT)/$(APP) .

# 构建所有目标平台的二进制
build-all:
	rm -rf $(OUTPUT)
	mkdir -p $(OUTPUT)
	@for platform in $(PLATFORMS); do \
		os=$${platform%/*}; \
		arch=$${platform#*/}; \
		name=$(APP)-$$os-$$arch; \
		[ "$$os" = "windows" ] && name=$$name.exe; \
		printf "  → %s  \t" $$platform; \
		GOOS=$$os GOARCH=$$arch go build $(LDFLAGS) -o $(OUTPUT)/$$name .; \
		ls -lh $(OUTPUT)/$$name | awk '{print $$5}'; \
	done

# 清理构建产物
clean:
	rm -rf $(OUTPUT)

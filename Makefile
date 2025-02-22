.PHONY: all clean build

# List of OS/Arch combinations to build for
TARGETS = \
    linux/amd64 \
    linux/arm64 \
    darwin/amd64 \
    darwin/arm64 \
    windows/amd64

# Output directory for binaries
OUTPUT_DIR = bin

all: clean build

clean:
	rm -rf $(OUTPUT_DIR)

build: check-gox
	mkdir -p $(OUTPUT_DIR)
	gox -osarch="$(TARGETS)" -output="$(OUTPUT_DIR)/{{.Dir}}_{{.OS}}_{{.Arch}}" ./...

check-gox:
	@command -v gox >/dev/null 2>&1 || { echo >&2 "gox is not installed. Please run 'go install github.com/mitchellh/gox@latest'"; exit 1; }

# Example usage:
# make

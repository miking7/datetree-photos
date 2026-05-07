.PHONY: build run dev clean css

TAILWIND := tools/tailwindcss
VERSION := $(shell git describe --tags --dirty 2>/dev/null || echo dev)

# uname-based platform detection so a fresh checkout's `make build` fetches
# the matching Tailwind CLI without manual steps. Binary stays gitignored.
$(TAILWIND):
	mkdir -p tools
	OS=$$(uname -s | tr '[:upper:]' '[:lower:]'); \
	ARCH=$$(uname -m); \
	case "$$OS-$$ARCH" in \
	  darwin-arm64)  SUFFIX=macos-arm64 ;; \
	  darwin-x86_64) SUFFIX=macos-x64 ;; \
	  linux-x86_64)  SUFFIX=linux-x64 ;; \
	  linux-aarch64) SUFFIX=linux-arm64 ;; \
	  *) echo "unsupported platform $$OS-$$ARCH"; exit 1 ;; \
	esac; \
	curl -sSL -o $(TAILWIND) \
	  https://github.com/tailwindlabs/tailwindcss/releases/latest/download/tailwindcss-$$SUFFIX
	chmod +x $(TAILWIND)

css: $(TAILWIND)
	$(TAILWIND) -i static/app.src.css -o static/app.css --minify

build: css
	templ generate
	go build -ldflags "-X main.version=$(VERSION)" -o datetree .

run: build
	./datetree

dev: $(TAILWIND)
	$(TAILWIND) -i static/app.src.css -o static/app.css --watch &
	templ generate --watch &
	go run . --no-open

clean:
	rm -f datetree static/app.css
	find . -name '*_templ.go' -delete

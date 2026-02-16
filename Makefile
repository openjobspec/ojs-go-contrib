.PHONY: test lint test-all

PACKAGES = ojs-gin ojs-echo ojs-fiber ojs-gorm ojs-serverless

test:
	@for pkg in $(PACKAGES); do \
		echo "==> Testing $$pkg"; \
		(cd $$pkg && go test ./... -race -cover) || exit 1; \
	done

lint:
	@for pkg in $(PACKAGES); do \
		echo "==> Linting $$pkg"; \
		(cd $$pkg && go vet ./...) || exit 1; \
	done

test-all: lint test

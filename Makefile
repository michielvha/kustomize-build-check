.PHONY: build verify verify-success verify-failure clean

BINARY_NAME=kustomize-build-check.exe
CMD_PATH=cmd/action/main.go
# Diffing against the empty tree marks every file as changed, so each example
# is actually built instead of being skipped as unaffected.
EMPTY_TREE=4b825dc642cb6eb9a060e54bf8d69288fbee4904

build:
	go build -o $(BINARY_NAME) $(CMD_PATH)

verify-success: build
	@echo "Running success scenario..."
	@rm -f summary_success.md
	env INPUT_BASE-REF=$(EMPTY_TREE) INPUT_ROOT-DIR=examples/success GITHUB_STEP_SUMMARY=summary_success.md ./$(BINARY_NAME)
	@if [ -f summary_success.md ]; then echo "✅ Success summary created"; else echo "❌ Success summary missing"; exit 1; fi
	@cat summary_success.md

verify-failure: build
	@echo "Running failure scenario..."
	@rm -f summary_failure.md
	-env INPUT_BASE-REF=$(EMPTY_TREE) INPUT_ROOT-DIR=examples/failure GITHUB_STEP_SUMMARY=summary_failure.md INPUT_FAIL-ON-ERROR=true ./$(BINARY_NAME)
	@if [ -f summary_failure.md ]; then echo "✅ Failure summary created"; else echo "❌ Failure summary missing"; exit 1; fi
	@cat summary_failure.md

verify: verify-success verify-failure

clean:
	rm -f $(BINARY_NAME) summary_success.md summary_failure.md

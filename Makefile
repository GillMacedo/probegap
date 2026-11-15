.PHONY: test lint demo

test:
	test -z "$$(gofmt -l plugin examples $$(find tests -name '*.go' -not -path '*/vendor/*'))"
	go vet ./...
	go vet $(addprefix ./tests/testdata/,cases exits0 fatal hangs library listenerr nohttp panics)
	cd tests/testdata/chimod && go vet ./...
	go test -race ./...

lint:
	claude plugin validate ./plugin --strict
	claude plugin validate . --strict

# Exit 1 means it found problems, which the broken example has on purpose.
demo:
	go run ./plugin/scripts --pkg ./examples/shop examples/shop/broken.yaml || test $$? -eq 1
	go run ./plugin/scripts --pkg ./examples/shop examples/shop/fixed.yaml
